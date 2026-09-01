package transport

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"time"

	"github.com/qubered/beacon/internal/agent/egress"
	"github.com/qubered/beacon/internal/engine/frame"
	"github.com/qubered/beacon/internal/engine/runtime"
	"github.com/qubered/beacon/internal/flow/graph"
	"github.com/qubered/beacon/internal/flow/types"
	"github.com/qubered/beacon/internal/nodes/framing"
	"github.com/qubered/beacon/internal/nodes/registry"
)

// TCPRequestConfig configures transport.tcp_request.
type TCPRequestConfig struct {
	Host string `json:"host"`
	Port int    `json:"port"`

	// Payload is sent on connect. Optional: plenty of devices answer with a
	// banner and expect nothing.
	Payload []byte `json:"payload,omitempty"`

	TLS *TLSConfig `json:"tls,omitempty"`

	ReadUntil framing.Strategy `json:"read_until"`
	Options   framing.Options  `json:"options,omitempty"`
}

func (c TCPRequestConfig) validate() error {
	if c.Host == "" {
		return fmt.Errorf("tcp_request needs a host")
	}
	if c.Port < 1 || c.Port > 65535 {
		return fmt.Errorf("tcp_request port must be 1-65535 (got %d)", c.Port)
	}
	return c.ReadUntil.Validate()
}

type tcpRequestNode struct{ cfg TCPRequestConfig }

func newTCPRequest(n graph.Node) (runtime.Executable, error) {
	var cfg TCPRequestConfig
	if len(n.Config) > 0 {
		if err := json.Unmarshal(n.Config, &cfg); err != nil {
			return nil, fmt.Errorf("transport.tcp_request: invalid config: %w", err)
		}
	}
	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("transport.tcp_request: %w", err)
	}
	return &tcpRequestNode{cfg: cfg}, nil
}

// Execute opens a TCP socket, optionally sends a payload, and reads one frame.
//
// It does not trim whitespace, assume UTF-8 or normalise line endings
// (principle 2). What the device sent is what the next node receives, and the
// next node is a Decode — which is invariant I1 and the reason the output port
// is bytes rather than string.
func (t *tcpRequestNode) Execute(ctx context.Context, rc *runtime.RunContext, in runtime.Inputs) (runtime.Outputs, error) {
	dialer, err := egress.DialerFrom(ctx)
	if err != nil {
		return nil, frame.Fail(frame.ClassInternal, "%s", err)
	}

	// A payload wired in overrides the configured one, which is how a
	// composed byte string from Build Bytes reaches the socket.
	payload := t.cfg.Payload
	var payloadFrame *frame.Frame
	if f, ok := in["payload"]; ok {
		b, ok := f.Value.([]byte)
		if !ok {
			return nil, frame.Fail(frame.ClassInternal, "payload input is not bytes (got %T)", f.Value)
		}
		payload = b
		payloadFrame = &f
	}

	conn, err := dialer.Dial(ctx, "tcp", t.cfg.Host, t.cfg.Port)
	if err != nil {
		return nil, fail(err, "connecting to %s:%d: %v", t.cfg.Host, t.cfg.Port, err)
	}
	defer conn.Close()

	if t.cfg.TLS != nil {
		conn, err = wrapTLS(ctx, conn, t.cfg.Host, *t.cfg.TLS)
		if err != nil {
			return nil, fail(err, "TLS handshake with %s:%d: %v", t.cfg.Host, t.cfg.Port, err)
		}
	}

	if len(payload) > 0 {
		if _, err := conn.Write(payload); err != nil {
			return nil, fail(err, "sending %d bytes to %s:%d: %v", len(payload), t.cfg.Host, t.cfg.Port, err)
		}
	}

	data, _, err := framing.Read(ctx, conn, t.cfg.ReadUntil, t.cfg.Options)
	if err != nil {
		return nil, fail(err, "reading from %s:%d: %v", t.cfg.Host, t.cfg.Port, err)
	}

	// Derive from the payload frame when there was one, so a payload composed
	// from a secret keeps its seal on the response (invariant I4). A challenge
	// -response exchange is exactly this shape, and constructing a fresh Frame
	// here would launder the seal away at the one node that touches the wire.
	if payloadFrame != nil {
		return runtime.Outputs{"out": payloadFrame.Derive(types.Bytes(), data)}, nil
	}
	return runtime.Outputs{"out": {Type: types.Bytes(), Value: data}}, nil
}

// TCPConnectConfig configures transport.tcp_connect.
type TCPConnectConfig struct {
	Host string `json:"host"`
	Port int    `json:"port"`
}

type tcpConnectNode struct{ cfg TCPConnectConfig }

func newTCPConnect(n graph.Node) (runtime.Executable, error) {
	var cfg TCPConnectConfig
	if len(n.Config) > 0 {
		if err := json.Unmarshal(n.Config, &cfg); err != nil {
			return nil, fmt.Errorf("transport.tcp_connect: invalid config: %w", err)
		}
	}
	if cfg.Host == "" {
		return nil, fmt.Errorf("transport.tcp_connect needs a host")
	}
	if cfg.Port < 1 || cfg.Port > 65535 {
		return nil, fmt.Errorf("transport.tcp_connect port must be 1-65535 (got %d)", cfg.Port)
	}
	return &tcpConnectNode{cfg: cfg}, nil
}

// Execute completes a handshake and reports how long it took. It emits a
// record rather than bytes because there are no bytes: nothing was sent and
// nothing was read. The measurement is the result.
func (t *tcpConnectNode) Execute(ctx context.Context, rc *runtime.RunContext, in runtime.Inputs) (runtime.Outputs, error) {
	dialer, err := egress.DialerFrom(ctx)
	if err != nil {
		return nil, frame.Fail(frame.ClassInternal, "%s", err)
	}

	started := time.Now()
	conn, err := dialer.Dial(ctx, "tcp", t.cfg.Host, t.cfg.Port)
	if err != nil {
		return nil, fail(err, "connecting to %s:%d: %v", t.cfg.Host, t.cfg.Port, err)
	}
	elapsed := time.Since(started)
	remote := conn.RemoteAddr().String()
	conn.Close()

	return runtime.Outputs{"out": {
		Type: types.Record(),
		Value: frame.Record{
			"connected":  true,
			"address":    remote,
			"connect_ms": float64(elapsed.Nanoseconds()) / 1e6,
		},
	}}, nil
}

func wrapTLS(ctx context.Context, conn net.Conn, host string, cfg TLSConfig) (net.Conn, error) {
	tc, err := cfg.clientConfig(host)
	if err != nil {
		return nil, err
	}
	tlsConn := tls.Client(conn, tc)
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		return nil, err
	}
	if err := cfg.checkPin(tlsConn.ConnectionState()); err != nil {
		return nil, err
	}
	return tlsConn, nil
}

func init() {
	registry.MustRegister(registry.Descriptor{
		Type:                "transport.tcp_request",
		Title:               "TCP Request",
		Summary:             "Open a TCP connection, optionally send a payload, and read one framed response.",
		Category:            "Transport",
		Tier:                registry.Tier1,
		Synonyms:            []string{"tcp", "telnet", "ascii", "port 23", "socket", "raw", "serial over ip"},
		ConfigSchemaVersion: 1,
		Inputs:              []registry.Port{{Name: "payload", Type: types.Bytes(), Optional: true}},
		Outputs:             []registry.Port{{Name: "out", Type: types.Bytes()}},
		PerformsEgress:      true,
		New:                 newTCPRequest,
	})

	registry.MustRegister(registry.Descriptor{
		Type:                "transport.tcp_connect",
		Title:               "TCP Connect",
		Summary:             "Complete a TCP handshake and report how long it took. Sends nothing.",
		Category:            "Transport",
		Tier:                registry.Tier1,
		Synonyms:            []string{"tcp", "port check", "open port", "handshake", "reachability"},
		ConfigSchemaVersion: 1,
		Outputs:             []registry.Port{{Name: "out", Type: types.Record()}},
		PerformsEgress:      true,
		New:                 newTCPConnect,
	})
}
