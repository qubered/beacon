package transport

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net"
	"net/netip"
	"os"
	"sort"
	"time"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"

	"github.com/qubered/beacon/internal/agent/egress"
	"github.com/qubered/beacon/internal/engine/frame"
	"github.com/qubered/beacon/internal/engine/runtime"
	"github.com/qubered/beacon/internal/flow/graph"
	"github.com/qubered/beacon/internal/flow/types"
	"github.com/qubered/beacon/internal/nodes/registry"
)

// PingConfig configures transport.icmp_ping.
type PingConfig struct {
	Host string `json:"host"`

	Count int `json:"count,omitempty"`

	// Interval between echoes. Kept modest by default: a burst of back-to-back
	// echoes measures the device's interrupt handling more than the network.
	Interval time.Duration `json:"interval,omitempty"`

	// PayloadSize is the ICMP data length in bytes.
	PayloadSize int `json:"payload_size,omitempty"`

	// Timeout is the per-echo wait. The run deadline still outranks it.
	Timeout time.Duration `json:"timeout,omitempty"`
}

func (c *PingConfig) applyDefaults() {
	if c.Count <= 0 {
		c.Count = 4
	}
	if c.Interval <= 0 {
		c.Interval = 200 * time.Millisecond
	}
	if c.PayloadSize <= 0 {
		c.PayloadSize = 32
	}
	if c.Timeout <= 0 {
		c.Timeout = time.Second
	}
}

type pingNode struct{ cfg PingConfig }

func newPing(n graph.Node) (runtime.Executable, error) {
	var cfg PingConfig
	if len(n.Config) > 0 {
		if err := json.Unmarshal(n.Config, &cfg); err != nil {
			return nil, fmt.Errorf("transport.icmp_ping: invalid config: %w", err)
		}
	}
	if cfg.Host == "" {
		return nil, fmt.Errorf("transport.icmp_ping needs a host")
	}
	cfg.applyDefaults()
	if cfg.Count > 100 {
		return nil, fmt.Errorf("transport.icmp_ping count is capped at 100 (got %d)", cfg.Count)
	}
	return &pingNode{cfg: cfg}, nil
}

// Execute sends Count echoes and reports loss and timing.
//
// Loss is not a failure here. A flow that pings a device and gets 25% loss
// should decide for itself whether that is degraded or down via a Threshold
// node — this node reports what happened, and the flow holds the policy.
// Only being unable to ping at all is an error.
func (p *pingNode) Execute(ctx context.Context, rc *runtime.RunContext, in runtime.Inputs) (runtime.Outputs, error) {
	dialer, err := egress.DialerFrom(ctx)
	if err != nil {
		return nil, frame.Fail(frame.ClassInternal, "%s", err)
	}

	// ICMP has no port, so the policy is checked with port 0 against an ICMP
	// rule. The address is pinned exactly as a TCP connection's would be:
	// resolving once and echoing to that address is what stops a name from
	// resolving to something allowed and then being pinged somewhere else.
	addr, err := dialer.Allow(ctx, p.cfg.Host, 0, egress.ProtoICMP)
	if err != nil {
		return nil, fail(err, "pinging %s: %v", p.cfg.Host, err)
	}

	conn, proto, err := listenICMP(addr.Is4())
	if err != nil {
		return nil, frame.Fail(frame.ClassInternal, "opening an ICMP socket: %v — an agent needs either the CAP_NET_RAW capability or an unprivileged ICMP socket (net.ipv4.ping_group_range on Linux)", err)
	}
	defer conn.Close()

	// The abort adapter for ICMP: the run deadline closes the socket, so a
	// read blocked waiting for a reply cannot outlive the run (invariant I2).
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			conn.Close()
		case <-done:
		}
	}()

	var rtts []time.Duration
	sent, received := 0, 0
	id := os.Getpid() & 0xffff

	for seq := 0; seq < p.cfg.Count; seq++ {
		if err := ctx.Err(); err != nil {
			break
		}
		if seq > 0 {
			select {
			case <-time.After(p.cfg.Interval):
			case <-ctx.Done():
				return nil, frame.Fail(frame.ClassTimeout, "pinging %s: run deadline reached after %d of %d echoes", p.cfg.Host, sent, p.cfg.Count)
			}
		}

		rtt, err := p.echo(ctx, conn, proto, addr, id, seq)
		sent++
		if err == nil {
			received++
			rtts = append(rtts, rtt)
		}
	}

	if sent == 0 {
		return nil, frame.Fail(frame.ClassTimeout, "pinging %s: no echoes were sent before the deadline", p.cfg.Host)
	}

	return runtime.Outputs{"out": {Type: types.Record(), Value: summarize(sent, received, rtts)}}, nil
}

func (p *pingNode) echo(ctx context.Context, conn *icmp.PacketConn, proto int, addr netip.Addr, id, seq int) (time.Duration, error) {
	body := &icmp.Echo{ID: id, Seq: seq, Data: make([]byte, p.cfg.PayloadSize)}
	msgType := icmpEchoType(addr.Is4())
	msg := icmp.Message{Type: msgType, Code: 0, Body: body}
	wire, err := msg.Marshal(nil)
	if err != nil {
		return 0, err
	}

	deadline := time.Now().Add(p.cfg.Timeout)
	if runDeadline, ok := ctx.Deadline(); ok && runDeadline.Before(deadline) {
		deadline = runDeadline
	}
	if err := conn.SetDeadline(deadline); err != nil {
		return 0, err
	}

	dst := destinationFor(conn, addr)
	started := time.Now()
	if _, err := conn.WriteTo(wire, dst); err != nil {
		return 0, err
	}

	reply := make([]byte, 1500)
	for {
		n, _, err := conn.ReadFrom(reply)
		if err != nil {
			return 0, err
		}
		parsed, err := icmp.ParseMessage(proto, reply[:n])
		if err != nil {
			continue
		}
		echo, ok := parsed.Body.(*icmp.Echo)
		if !ok || echo.Seq != seq {
			// Another monitor's reply, or an error message about somebody
			// else's packet. Keep waiting for ours rather than counting this
			// as the answer.
			continue
		}
		return time.Since(started), nil
	}
}

// summarize computes the loss and timing figures a Threshold node acts on.
func summarize(sent, received int, rtts []time.Duration) frame.Record {
	rec := frame.Record{
		"sent":         int64(sent),
		"received":     int64(received),
		"loss_percent": float64(sent-received) / float64(sent) * 100,
	}
	if len(rtts) == 0 {
		return rec
	}

	sort.Slice(rtts, func(i, j int) bool { return rtts[i] < rtts[j] })
	var total time.Duration
	for _, r := range rtts {
		total += r
	}
	avg := total / time.Duration(len(rtts))

	// Jitter as mean absolute deviation from the average. Standard deviation
	// would be defensible too, but mean deviation is what an AV tech reading
	// "12ms jitter" expects it to mean, and matching the expectation is worth
	// more than the statistical nicety.
	var deviation float64
	for _, r := range rtts {
		deviation += math.Abs(float64(r - avg))
	}

	rec["min_ms"] = ms(rtts[0])
	rec["max_ms"] = ms(rtts[len(rtts)-1])
	rec["avg_ms"] = ms(avg)
	rec["jitter_ms"] = deviation / float64(len(rtts)) / 1e6
	return rec
}

func ms(d time.Duration) float64 { return float64(d.Nanoseconds()) / 1e6 }

// listenICMP prefers an unprivileged datagram ICMP socket and falls back to a
// raw one.
//
// The order matters for the deployment story: an agent in a container that can
// use the unprivileged socket should not need CAP_NET_RAW, and requiring the
// capability everywhere because the fallback was tried first would make the
// agent harder to run than it needs to be.
func listenICMP(ipv4Target bool) (*icmp.PacketConn, int, error) {
	type attempt struct {
		network string
		address string
		proto   int
	}
	var attempts []attempt
	if ipv4Target {
		attempts = []attempt{
			{"udp4", "0.0.0.0", ipv4.ICMPTypeEcho.Protocol()},
			{"ip4:icmp", "0.0.0.0", ipv4.ICMPTypeEcho.Protocol()},
		}
	} else {
		attempts = []attempt{
			{"udp6", "::", ipv6.ICMPTypeEchoRequest.Protocol()},
			{"ip6:ipv6-icmp", "::", ipv6.ICMPTypeEchoRequest.Protocol()},
		}
	}

	var lastErr error
	for _, a := range attempts {
		conn, err := icmp.ListenPacket(a.network, a.address)
		if err == nil {
			return conn, a.proto, nil
		}
		lastErr = err
	}
	return nil, 0, lastErr
}

func init() {
	registry.MustRegister(registry.Descriptor{
		Type:                "transport.icmp_ping",
		Title:               "ICMP Ping",
		Summary:             "Send ICMP echoes and report loss, min/avg/max and jitter.",
		Category:            "Transport",
		Tier:                registry.Tier1,
		Synonyms:            []string{"ping", "icmp", "echo", "reachability", "latency", "packet loss"},
		ConfigSchemaVersion: 1,
		Outputs:             []registry.Port{{Name: "out", Type: types.Record()}},
		PerformsEgress:      true,
		New:                 newPing,
	})
}

// icmpEchoType and destinationFor keep the IPv4/IPv6 branching in one place
// rather than scattered through echo.
func icmpEchoType(is4 bool) icmp.Type {
	if is4 {
		return ipv4.ICMPTypeEcho
	}
	return ipv6.ICMPTypeEchoRequest
}

// destinationFor builds the address to echo to, matching the socket kind:
// a datagram ICMP socket addresses by UDPAddr, a raw one by IPAddr.
func destinationFor(conn *icmp.PacketConn, addr netip.Addr) net.Addr {
	ip := net.IP(addr.AsSlice())
	if _, ok := conn.LocalAddr().(*net.UDPAddr); ok {
		return &net.UDPAddr{IP: ip}
	}
	return &net.IPAddr{IP: ip}
}
