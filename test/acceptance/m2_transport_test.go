package acceptance

import (
	"context"
	"net"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/qubered/beacon/internal/agent/egress"
	"github.com/qubered/beacon/internal/config"
	"github.com/qubered/beacon/internal/engine/frame"
	"github.com/qubered/beacon/internal/engine/runtime"
	"github.com/qubered/beacon/internal/flow/graph"
	"github.com/qubered/beacon/internal/nodes/framing"
	"github.com/qubered/beacon/internal/nodes/registry"

	_ "github.com/qubered/beacon/internal/nodes/transport"
)

// The M1 retro's lesson was that the bugs live where two real pieces meet, not
// inside either one. So these run the real engine, the real registry, the real
// egress policy and a real socket — the M1 pipeline with the fixture replaced
// by a device that actually answers.
//
// The fake device stands in for test/devsim until that package is filled in
// alongside the §18 scenarios in M7.

// controlProcessor is the port-23-but-not-really-Telnet shape spec §6.4
// describes: it opens with IAC negotiation, sends a banner, and answers
// commands in raw ASCII terminated by CRLF. This is acceptance scenario C's
// device class, one milestone early and without the Connection Scope half.
func controlProcessor(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })

	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				// IAC WILL ECHO, IAC DO SUPPRESS-GO-AHEAD, then a banner.
				c.Write([]byte("\xff\xfb\x01\xff\xfd\x03"))
				c.Write([]byte("Controller v2.1\r\nready> "))

				buf := make([]byte, 128)
				n, err := c.Read(buf)
				if err != nil {
					return
				}
				if strings.Contains(string(buf[:n]), "?lamphours") {
					c.Write([]byte("LAMPHOURS 1420\r\n"))
				}
			}(c)
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port
}

func lanDialer() *egress.Dialer {
	return &egress.Dialer{
		Policy: egress.Policy{
			Name:          "acceptance",
			AllowLoopback: true,
			Allow: []egress.Rule{
				{Prefix: netip.MustParsePrefix("127.0.0.0/8"), Protocol: egress.ProtoTCP, MinPort: 1, MaxPort: 65535},
			},
		},
	}
}

// controlProcessorGraph is the same bytes -> string -> record -> status spine
// the M1 test proved, with a real transport at the root instead of a fixture.
func controlProcessorGraph(t *testing.T, port int) *graph.Graph {
	t.Helper()
	return &graph.Graph{
		Nodes: []graph.Node{
			{ID: "ask", Type: "transport.tcp_request", Config: mustJSON(t, map[string]any{
				"host":    "127.0.0.1",
				"port":    port,
				"payload": []byte("?lamphours\r\n"),
				"read_until": framing.Strategy{
					Kind:      framing.KindDelimiter,
					Delimiter: []byte("\r\n"),
				},
				"options": map[string]any{
					// Both are mandatory for this device class: without IAC
					// stripping the negotiation bytes corrupt the read, and
					// without discard-before the banner is what gets parsed.
					"strip_iac":      true,
					"discard_before": []byte("ready> "),
				},
			})},
			{ID: "decode", Type: "byteops.decode"},
			{ID: "extract", Type: "parse.regex_extract", Config: mustJSON(t, map[string]any{
				"pattern": `LAMPHOURS (?P<hours>\d+)`,
			})},
			{ID: "check", Type: "emit.assert", Config: mustJSON(t, map[string]any{
				"rows": []map[string]any{
					{"field": "hours", "operator": "lt", "value": 2000, "message": "lamp is near end of life"},
				},
			})},
			{ID: "status", Type: "emit.emit_status"},
		},
		Edges: []graph.Edge{
			{From: graph.Endpoint{Node: "ask", Port: "out"}, To: graph.Endpoint{Node: "decode", Port: "in"}},
			{From: graph.Endpoint{Node: "decode", Port: "out"}, To: graph.Endpoint{Node: "extract", Port: "in"}},
			{From: graph.Endpoint{Node: "extract", Port: "out"}, To: graph.Endpoint{Node: "check", Port: "in"}},
			{From: graph.Endpoint{Node: "check", Port: "out"}, To: graph.Endpoint{Node: "status", Port: "in"}},
		},
	}
}

// TestM2Pipeline_RealSocketThroughTheRealEngine is the M2 counterpart to the
// M1 pipeline test: a transport, the framing engine, IAC stripping, a
// discard-before banner skip, and the decode/extract/assert chain, all wired
// together and run once.
func TestM2Pipeline_RealSocketThroughTheRealEngine(t *testing.T) {
	reg := registry.Default
	g := controlProcessorGraph(t, controlProcessor(t))

	if err := g.Validate(reg.PortTypes()); err != nil {
		t.Fatalf("a transport -> decode -> extract -> assert -> status flow must validate cleanly: %v", err)
	}

	ctx := egress.WithDialer(context.Background(), lanDialer())
	rc := runtime.NewRunContext("m2-run-1", time.Now())
	res := runtime.Run(ctx, g, reg.Factory(), reg.PortMeta(), config.DefaultBounds(), rc, nil)

	if res.Err != nil {
		t.Fatalf("unexpected run error: %v", res.Err)
	}
	if res.Status != frame.StatusUp {
		t.Fatalf("1420 lamp hours is under the 2000 threshold and should report up, got %s (warning: %s)", res.Status, res.Warning)
	}
	for _, id := range []graph.NodeID{"ask", "decode", "extract", "check", "status"} {
		if res.Nodes[id].State != runtime.NodeDone {
			t.Errorf("node %s: expected done, got %s", id, res.Nodes[id].State)
		}
	}
}

// TestM2Pipeline_EgressDenialFailsTheRunNotTheProcess proves the refusal
// travels the same path any other node failure does: the transport's error
// port fires, the skip cascades downstream, and the run ends unknown rather
// than up. A control that crashed the run some other way would be a control
// people disable.
func TestM2Pipeline_EgressDenialFailsTheRunNotTheProcess(t *testing.T) {
	reg := registry.Default
	g := controlProcessorGraph(t, controlProcessor(t))

	// A policy with no rules at all: default-deny.
	ctx := egress.WithDialer(context.Background(), &egress.Dialer{})
	rc := runtime.NewRunContext("m2-run-2", time.Now())
	res := runtime.Run(ctx, g, reg.Factory(), reg.PortMeta(), config.DefaultBounds(), rc, nil)

	if res.Err == nil {
		t.Fatal("a default-deny policy allowed the connection")
	}
	if res.Status != frame.StatusUnknown {
		t.Errorf("status = %s, want unknown when the transport never ran", res.Status)
	}
	if res.Nodes["ask"].State != runtime.NodeError {
		t.Errorf("ask: expected error, got %s", res.Nodes["ask"].State)
	}
	for _, id := range []graph.NodeID{"decode", "extract", "check", "status"} {
		if res.Nodes[id].State != runtime.NodeSkipped {
			t.Errorf("node %s: expected skipped after the transport was refused, got %s", id, res.Nodes[id].State)
		}
	}
}

// TestM2Pipeline_DeadlineTerminatesARunAgainstASilentDevice is invariant I2
// with a real socket in the graph rather than a deliberately slow fake node:
// the run's own deadline has to reach through the engine, the transport and
// the framing loop all the way to the socket.
func TestM2Pipeline_DeadlineTerminatesARunAgainstASilentDevice(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			// Accept and never speak, holding the connection open.
			defer c.Close()
		}
	}()

	reg := registry.Default
	g := &graph.Graph{Nodes: []graph.Node{
		{ID: "ask", Type: "transport.tcp_request", Config: mustJSON(t, map[string]any{
			"host": "127.0.0.1",
			"port": ln.Addr().(*net.TCPAddr).Port,
			"read_until": framing.Strategy{
				Kind:      framing.KindDelimiter,
				Delimiter: []byte("\r\n"),
			},
		})},
	}}

	bounds := config.DefaultBounds()
	bounds.RunWallClock = 200 * time.Millisecond

	ctx := egress.WithDialer(context.Background(), lanDialer())
	rc := runtime.NewRunContext("m2-run-3", time.Now())

	started := time.Now()
	res := runtime.Run(ctx, g, reg.Factory(), reg.PortMeta(), bounds, rc, nil)
	elapsed := time.Since(started)

	if res.Err == nil {
		t.Fatal("a run against a silent device completed successfully")
	}
	if elapsed > 3*time.Second {
		t.Fatalf("the run took %s against a 200ms deadline; the deadline did not reach the socket (I2)", elapsed)
	}
}
