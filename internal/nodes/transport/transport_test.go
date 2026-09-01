package transport

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/qubered/beacon/internal/agent/egress"
	"github.com/qubered/beacon/internal/engine/frame"
	"github.com/qubered/beacon/internal/engine/runtime"
	"github.com/qubered/beacon/internal/flow/graph"
	"github.com/qubered/beacon/internal/flow/types"
	"github.com/qubered/beacon/internal/nodes/framing"
	"github.com/qubered/beacon/internal/nodes/registry"
)

// allowLoopback builds a dialer that can reach a test listener.
//
// It sets Policy.AllowLoopback, which is the same switch test/devsim runs
// under in CI: the hard denylist blocks 127.0.0.0/8 in production, and without
// a sanctioned local opt-out no transport could ever be tested against a real
// socket. Link-local and metadata addresses stay denied even here.
func allowLoopback(t *testing.T) *egress.Dialer {
	t.Helper()
	return &egress.Dialer{
		Policy: egress.Policy{
			AllowLoopback: true,
			Allow: []egress.Rule{
				{Prefix: netip.MustParsePrefix("127.0.0.0/8"), Protocol: egress.ProtoTCP, MinPort: 1, MaxPort: 65535},
				{Prefix: netip.MustParsePrefix("127.0.0.0/8"), Protocol: egress.ProtoICMP},
			},
		},
		Resolver: loopbackResolver{},
	}
}

// loopbackResolver answers every name with 127.0.0.1 so a test never touches
// real DNS.
type loopbackResolver struct{}

func (loopbackResolver) LookupAddrs(ctx context.Context, host string) ([]netip.Addr, error) {
	return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
}

func ctxWith(t *testing.T, d *egress.Dialer) (context.Context, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	return egress.WithDialer(ctx, d), cancel
}

func mustNode(t *testing.T, typ string, cfg any) runtime.Executable {
	t.Helper()
	b, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	d, ok := registry.Default.Get(typ)
	if !ok {
		t.Fatalf("node type %q is not registered", typ)
	}
	exec, err := d.New(graph.Node{ID: "n", Type: typ, Config: b})
	if err != nil {
		t.Fatalf("constructing %s: %v", typ, err)
	}
	return exec
}

// TestTransports_EmitBytesNotStrings is invariant I1's registry-level proof:
// a transport that declared a string output would mean decoding had been
// folded into a transport, which is exactly the design failure I1 names.
func TestTransports_EmitBytesNotStrings(t *testing.T) {
	found := 0
	for _, d := range registry.Default.All() {
		if !strings.HasPrefix(d.Type, "transport.") {
			continue
		}
		found++
		for _, p := range d.Outputs {
			if p.Type.Kind == types.KindString {
				t.Errorf("%s declares a string output %q; a transport emits bytes and decoding is a separate node (I1)", d.Type, p.Name)
			}
		}
		if !d.PerformsEgress {
			t.Errorf("%s does not declare PerformsEgress; the agent's egress policy would not be applied to it", d.Type)
		}
	}
	if found == 0 {
		t.Fatal("no transport nodes are registered")
	}
}

// TestTransport_WithoutAnEgressPolicyFailsClosed. A transport that found no
// policy and dialled anyway would have unrestricted network access from every
// VLAN an agent sits in — the outcome I7 exists to prevent, arriving silently.
func TestTransport_WithoutAnEgressPolicyFailsClosed(t *testing.T) {
	nodes := []struct {
		typ string
		cfg any
	}{
		{"transport.tcp_request", TCPRequestConfig{Host: "10.0.0.1", Port: 23, ReadUntil: delimiterStrategy()}},
		{"transport.tcp_connect", TCPConnectConfig{Host: "10.0.0.1", Port: 23}},
		{"transport.http_request", HTTPRequestConfig{URL: "http://10.0.0.1/"}},
		{"transport.tls_inspect", TLSInspectConfig{Host: "10.0.0.1"}},
		{"transport.icmp_ping", PingConfig{Host: "10.0.0.1"}},
	}
	for _, n := range nodes {
		t.Run(n.typ, func(t *testing.T) {
			exec := mustNode(t, n.typ, n.cfg)
			// A bare context: no policy attached.
			_, err := exec.Execute(context.Background(), nil, runtime.Inputs{})
			if err == nil {
				t.Fatal("the node connected with no egress policy in context")
			}
			if !strings.Contains(err.Error(), "egress policy") {
				t.Fatalf("error %q does not name the missing egress policy", err)
			}
		})
	}
}

func TestTCPRequest_SendsAndFramesAResponse(t *testing.T) {
	ln := listenTCP(t, func(c net.Conn) {
		buf := make([]byte, 64)
		n, _ := c.Read(buf)
		fmt.Fprintf(c, "ECHO %s\r\n", strings.TrimSpace(string(buf[:n])))
		c.Close()
	})

	exec := mustNode(t, "transport.tcp_request", TCPRequestConfig{
		Host:      "127.0.0.1",
		Port:      portOf(t, ln),
		Payload:   []byte("PWR?\r\n"),
		ReadUntil: delimiterStrategy(),
	})

	ctx, cancel := ctxWith(t, allowLoopback(t))
	defer cancel()

	out, err := exec.Execute(ctx, nil, runtime.Inputs{})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	got, ok := out["out"].Value.([]byte)
	if !ok {
		t.Fatalf("output is %T, want []byte — a transport emits bytes (I1)", out["out"].Value)
	}
	if string(got) != "ECHO PWR?" {
		t.Fatalf("frame = %q", got)
	}
}

// TestTCPRequest_KeepsTheSealOnAComposedPayload proves invariant I4 survives
// the one node that touches the wire: a response derived from a sealed payload
// stays sealed, so a challenge-response exchange cannot launder a secret into
// a capture.
func TestTCPRequest_KeepsTheSealOnAComposedPayload(t *testing.T) {
	ln := listenTCP(t, func(c net.Conn) {
		buf := make([]byte, 32)
		c.Read(buf)
		c.Write([]byte("OK\r\n"))
		c.Close()
	})

	exec := mustNode(t, "transport.tcp_request", TCPRequestConfig{
		Host: "127.0.0.1", Port: portOf(t, ln), ReadUntil: delimiterStrategy(),
	})

	ctx, cancel := ctxWith(t, allowLoopback(t))
	defer cancel()

	sealed := frame.Frame{Value: []byte("AUTH hunter2\r\n"), Sealed: true}
	out, err := exec.Execute(ctx, nil, runtime.Inputs{"payload": sealed})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !out["out"].Sealed {
		t.Fatal("the response derived from a sealed payload is not sealed; a secret would reach the hex dump (I4)")
	}
}

func TestTCPConnect_ReportsTiming(t *testing.T) {
	ln := listenTCP(t, func(c net.Conn) { c.Close() })

	exec := mustNode(t, "transport.tcp_connect", TCPConnectConfig{Host: "127.0.0.1", Port: portOf(t, ln)})
	ctx, cancel := ctxWith(t, allowLoopback(t))
	defer cancel()

	out, err := exec.Execute(ctx, nil, runtime.Inputs{})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	rec := out["out"].Value.(frame.Record)
	if rec["connected"] != true {
		t.Errorf("connected = %v", rec["connected"])
	}
	if _, ok := rec["connect_ms"].(float64); !ok {
		t.Errorf("connect_ms is %T, want a number", rec["connect_ms"])
	}
}

func TestTCPConnect_RefusedIsClassifiedAsConnectRefused(t *testing.T) {
	// Bind and immediately close, so the port is almost certainly free.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := portOf(t, ln)
	ln.Close()

	exec := mustNode(t, "transport.tcp_connect", TCPConnectConfig{Host: "127.0.0.1", Port: port})
	ctx, cancel := ctxWith(t, allowLoopback(t))
	defer cancel()

	_, err = exec.Execute(ctx, nil, runtime.Inputs{})
	if err == nil {
		t.Fatal("connecting to a closed port succeeded")
	}
	f, ok := err.(frame.Failure)
	if !ok {
		t.Fatalf("error is %T, want frame.Failure", err)
	}
	if f.Class != frame.ClassConnectRefused {
		t.Fatalf("class = %s, want %s — the class is what routes the alert", f.Class, frame.ClassConnectRefused)
	}
}

// TestTCPRequest_DeadlineReachesTheSocket is invariant I2 at the socket rather
// than between nodes: a device that accepts a connection and then says nothing
// must not hold the run open past its deadline.
func TestTCPRequest_DeadlineReachesTheSocket(t *testing.T) {
	ln := listenTCP(t, func(c net.Conn) {
		// Accept and go silent, holding the connection open.
		time.Sleep(10 * time.Second)
		c.Close()
	})

	exec := mustNode(t, "transport.tcp_request", TCPRequestConfig{
		Host: "127.0.0.1", Port: portOf(t, ln), ReadUntil: delimiterStrategy(),
	})

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	ctx = egress.WithDialer(ctx, allowLoopback(t))

	started := time.Now()
	_, err := exec.Execute(ctx, nil, runtime.Inputs{})
	elapsed := time.Since(started)

	if err == nil {
		t.Fatal("a silent device produced a successful read")
	}
	if elapsed > 2*time.Second {
		t.Fatalf("the read took %s against a 150ms deadline; the deadline did not reach the socket (I2)", elapsed)
	}
	if f, ok := err.(frame.Failure); ok && f.Class != frame.ClassTimeout {
		t.Errorf("class = %s, want %s", f.Class, frame.ClassTimeout)
	}
}

func TestHTTPRequest_ReturnsStatusHeadersAndRawBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Device", "rack-1")
		w.WriteHeader(207)
		w.Write([]byte(`{"power":"on"}`))
	}))
	defer srv.Close()

	exec := mustNode(t, "transport.http_request", HTTPRequestConfig{URL: srv.URL})
	ctx, cancel := ctxWith(t, allowLoopback(t))
	defer cancel()

	out, err := exec.Execute(ctx, nil, runtime.Inputs{})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := out["status"].Value.(int64); got != 207 {
		t.Errorf("status = %d, want 207", got)
	}
	body, ok := out["body"].Value.([]byte)
	if !ok {
		t.Fatalf("body is %T, want []byte (I1)", out["body"].Value)
	}
	if string(body) != `{"power":"on"}` {
		t.Errorf("body = %q", body)
	}
	if got := out["headers"].Value.(frame.Record)["X-Device"]; got != "rack-1" {
		t.Errorf("X-Device = %v", got)
	}
}

// TestHTTPRequest_RedirectToADeniedHostFails is the roadmap's M2 exit gate.
//
// Following it would let any reachable server steer an agent at the metadata
// address, which is the SSRF pivot the egress control exists to stop.
func TestHTTPRequest_RedirectToADeniedHostFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://169.254.169.254/latest/meta-data/", http.StatusFound)
	}))
	defer srv.Close()

	exec := mustNode(t, "transport.http_request", HTTPRequestConfig{URL: srv.URL, MaxRedirects: 5})

	// A policy that allows loopback and the metadata address's own range, to
	// prove the *hard denylist* is what refuses the redirect rather than the
	// allowlist merely not covering it.
	d := &egress.Dialer{
		Policy: egress.Policy{
			AllowLoopback: true,
			Allow: []egress.Rule{
				{Prefix: netip.MustParsePrefix("0.0.0.0/0"), Protocol: egress.ProtoTCP, MinPort: 1, MaxPort: 65535},
			},
		},
		Resolver: literalResolver{},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ctx = egress.WithDialer(ctx, d)

	_, err := exec.Execute(ctx, nil, runtime.Inputs{})
	if err == nil {
		t.Fatal("a redirect to the metadata address was followed")
	}
	if !strings.Contains(err.Error(), "169.254.169.254") {
		t.Fatalf("error %q does not name the denied redirect target", err)
	}
}

// TestHTTPRequest_RedirectsAreNotFollowedByDefault: for a monitoring check a
// 302 is usually itself the signal, so the safe default is to report it.
func TestHTTPRequest_RedirectsAreNotFollowedByDefault(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/moved" {
			w.Write([]byte("arrived"))
			return
		}
		http.Redirect(w, r, "/moved", http.StatusFound)
	}))
	defer srv.Close()

	exec := mustNode(t, "transport.http_request", HTTPRequestConfig{URL: srv.URL})
	ctx, cancel := ctxWith(t, allowLoopback(t))
	defer cancel()

	out, err := exec.Execute(ctx, nil, runtime.Inputs{})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := out["status"].Value.(int64); got != 302 {
		t.Fatalf("status = %d, want the 302 reported rather than followed", got)
	}
}

func TestTLSInspect_ReportsTheCertificate(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()

	host, portStr, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "https://"))
	port, _ := strconv.Atoi(portStr)

	exec := mustNode(t, "transport.tls_inspect", TLSInspectConfig{Host: host, Port: port})
	ctx, cancel := ctxWith(t, allowLoopback(t))
	defer cancel()

	out, err := exec.Execute(ctx, nil, runtime.Inputs{})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	rec := out["out"].Value.(frame.Record)
	if rec["expired"] != false {
		t.Errorf("expired = %v, want false for a freshly generated test certificate", rec["expired"])
	}
	if _, ok := rec["days_remaining"].(float64); !ok {
		t.Errorf("days_remaining is %T, want a number for a Threshold node to act on", rec["days_remaining"])
	}
	if fp, _ := rec["fingerprint"].(string); len(fp) != 64 {
		t.Errorf("fingerprint = %q, want 64 hex characters of SHA-256", fp)
	}
}

// TestDNSQuery_ResolverOverrideGoesThroughEgress: pointing this node at a
// nameserver opens an outbound connection like any other, and without the
// check it would be a way to reach an arbitrary host on port 53 from a node
// that looks like it only does lookups.
func TestDNSQuery_ResolverOverrideGoesThroughEgress(t *testing.T) {
	exec := mustNode(t, "transport.dns_query", DNSQueryConfig{
		Name:     "device.example",
		Type:     "A",
		Resolver: "169.254.169.254",
	})

	// Allow everything the allowlist can express; the metadata address is
	// still refused by the hard denylist.
	d := &egress.Dialer{
		Policy: egress.Policy{Allow: []egress.Rule{
			{Prefix: netip.MustParsePrefix("0.0.0.0/0"), Protocol: egress.ProtoUDP, MinPort: 1, MaxPort: 65535},
			{Prefix: netip.MustParsePrefix("0.0.0.0/0"), Protocol: egress.ProtoTCP, MinPort: 1, MaxPort: 65535},
		}},
		Resolver: literalResolver{},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ctx = egress.WithDialer(ctx, d)

	if _, err := exec.Execute(ctx, nil, runtime.Inputs{}); err == nil {
		t.Fatal("a query against a denied nameserver succeeded")
	}
}

// TestDNSQuery_ResolverOverrideDialsTheConfiguredServer: the override is only
// meaningful if the query actually goes to that nameserver — "is the venue's
// DNS answering" is a different question from "can this agent resolve".
func TestDNSQuery_ResolverOverrideDialsTheConfiguredServer(t *testing.T) {
	exec := mustNode(t, "transport.dns_query", DNSQueryConfig{
		Name:     "device.example",
		Type:     "A",
		Resolver: "10.9.9.9:5353",
	})

	dialed := make(chan string, 4)
	d := &egress.Dialer{
		Policy: egress.Policy{Allow: []egress.Rule{
			{Prefix: netip.MustParsePrefix("10.0.0.0/8"), Protocol: egress.ProtoUDP, MinPort: 1, MaxPort: 65535},
			{Prefix: netip.MustParsePrefix("10.0.0.0/8"), Protocol: egress.ProtoTCP, MinPort: 1, MaxPort: 65535},
		}},
		Resolver: literalResolver{},
		Net: func(ctx context.Context, network, addr string) (net.Conn, error) {
			select {
			case dialed <- addr:
			default:
			}
			return nil, fmt.Errorf("no nameserver here")
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ctx = egress.WithDialer(ctx, d)

	_, _ = exec.Execute(ctx, nil, runtime.Inputs{})

	select {
	case addr := <-dialed:
		if addr != "10.9.9.9:5353" {
			t.Fatalf("dialled %q, want the configured nameserver 10.9.9.9:5353", addr)
		}
	default:
		t.Fatal("the configured nameserver was never dialled; the override was ignored")
	}
}

func TestDNSQuery_RejectsAnUnsupportedType(t *testing.T) {
	d, _ := registry.Default.Get("transport.dns_query")
	cfg, _ := json.Marshal(DNSQueryConfig{Name: "example.test", Type: "MX"})
	_, err := d.New(graph.Node{ID: "n", Type: "transport.dns_query", Config: cfg})
	if err == nil {
		t.Fatal("an unsupported record type was accepted")
	}
	if !strings.Contains(err.Error(), "A, AAAA, PTR, SRV, TXT or CNAME") {
		t.Errorf("refusal %q does not list what is supported", err)
	}
}

func TestPing_SummarizesLossAndJitter(t *testing.T) {
	rec := summarize(4, 3, []time.Duration{
		10 * time.Millisecond, 12 * time.Millisecond, 20 * time.Millisecond,
	})
	if got := rec["loss_percent"].(float64); got != 25 {
		t.Errorf("loss_percent = %v, want 25", got)
	}
	if got := rec["min_ms"].(float64); got != 10 {
		t.Errorf("min_ms = %v, want 10", got)
	}
	if got := rec["max_ms"].(float64); got != 20 {
		t.Errorf("max_ms = %v, want 20", got)
	}
	if got := rec["avg_ms"].(float64); got != 14 {
		t.Errorf("avg_ms = %v, want 14", got)
	}
	if _, ok := rec["jitter_ms"].(float64); !ok {
		t.Error("jitter_ms is missing")
	}
}

func TestPing_TotalLossStillReportsRatherThanErroring(t *testing.T) {
	// A device that answers nothing is a 100%-loss result, not an execution
	// failure: the flow's Threshold node decides whether that is down.
	rec := summarize(4, 0, nil)
	if got := rec["loss_percent"].(float64); got != 100 {
		t.Fatalf("loss_percent = %v, want 100", got)
	}
	if _, present := rec["avg_ms"]; present {
		t.Error("avg_ms is present with no replies; a timing figure would be fabricated")
	}
}

func TestTLSConfig_FingerprintNormalisation(t *testing.T) {
	want := "aabbcc"
	for _, in := range []string{"AA:BB:CC", "aa bb cc", "AA-BB-CC", "aabbcc"} {
		if got := normalizeFingerprint(in); got != want {
			t.Errorf("normalizeFingerprint(%q) = %q, want %q", in, got, want)
		}
	}
}

// --- helpers ---

func delimiterStrategy() framing.Strategy {
	return framing.Strategy{Kind: framing.KindDelimiter, Delimiter: []byte("\r\n")}
}

func listenTCP(t *testing.T, handle func(net.Conn)) net.Listener {
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
			go handle(c)
		}
	}()
	return ln
}

func portOf(t *testing.T, ln net.Listener) int {
	t.Helper()
	return ln.Addr().(*net.TCPAddr).Port
}

// literalResolver resolves only IP literals, which is all the redirect test
// needs and keeps real DNS out of the test entirely.
type literalResolver struct{}

func (literalResolver) LookupAddrs(ctx context.Context, host string) ([]netip.Addr, error) {
	a, err := netip.ParseAddr(host)
	if err != nil {
		return nil, fmt.Errorf("no such host %q", host)
	}
	return []netip.Addr{a}, nil
}
