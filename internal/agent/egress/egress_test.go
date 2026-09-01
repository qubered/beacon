package egress

import (
	"context"
	"net"
	"net/netip"
	"testing"
)

// allowLAN is the shape of a realistic policy: one venue subnet, TCP only.
func allowLAN() Policy {
	return Policy{
		Name: "test",
		Allow: []Rule{
			{Prefix: netip.MustParsePrefix("10.0.0.0/8"), Protocol: ProtoTCP, MinPort: 1, MaxPort: 65535},
			{Prefix: netip.MustParsePrefix("10.0.0.0/8"), Protocol: ProtoICMP},
		},
	}
}

type scriptedResolver struct {
	answers [][]netip.Addr
	calls   int
}

func (r *scriptedResolver) LookupAddrs(ctx context.Context, host string) ([]netip.Addr, error) {
	i := r.calls
	r.calls++
	if i >= len(r.answers) {
		i = len(r.answers) - 1
	}
	return r.answers[i], nil
}

func mustAddr(t *testing.T, s string) netip.Addr {
	t.Helper()
	a, err := netip.ParseAddr(s)
	if err != nil {
		t.Fatalf("parsing %q: %v", s, err)
	}
	return a
}

// TestPolicy_ZeroValueDeniesEverything: an unconfigured policy must refuse, not
// allow. Default-deny is only default-deny if the empty case is a denial.
func TestPolicy_ZeroValueDeniesEverything(t *testing.T) {
	var p Policy
	if dec := p.Check(mustAddr(t, "10.0.0.5"), 80, ProtoTCP); dec.Allowed {
		t.Fatalf("zero-value policy allowed a connection: %+v", dec)
	}
}

// TestPolicy_DenylistBeatsAllowlist covers the normalisation risk the roadmap
// calls out by name: ::ffff:127.0.0.1, the metadata address, and an explicitly
// allowed loopback range are the same bypass wearing different hats.
func TestPolicy_DenylistBeatsAllowlist(t *testing.T) {
	// A policy that tries as hard as it can to allow the denied ranges.
	p := Policy{
		Allow: []Rule{
			{Prefix: netip.MustParsePrefix("0.0.0.0/0"), Protocol: ProtoTCP, MinPort: 1, MaxPort: 65535},
			{Prefix: netip.MustParsePrefix("::/0"), Protocol: ProtoTCP, MinPort: 1, MaxPort: 65535},
		},
	}

	denied := []string{
		"127.0.0.1",              // loopback
		"127.1.2.3",              // the rest of 127/8, which people forget
		"::1",                    // IPv6 loopback
		"::ffff:127.0.0.1",       // IPv4-mapped loopback — the standard bypass
		"169.254.169.254",        // cloud metadata
		"::ffff:169.254.169.254", // IPv4-mapped metadata
		"fe80::1",                // IPv6 link-local
		"fd00:ec2::254",          // IPv6 metadata
		"0.0.0.0",
	}
	for _, s := range denied {
		addr := Canonicalize(mustAddr(t, s))
		if dec := p.Check(addr, 80, ProtoTCP); dec.Allowed {
			t.Errorf("%s was allowed by an allow-everything policy; the hard denylist must win", s)
		}
	}
}

// TestCanonicalize_UnmapsBeforeMatching proves the mapped form and the plain
// form are the same address by the time anything checks a range, rather than
// each caller remembering to unmap.
func TestCanonicalize_UnmapsBeforeMatching(t *testing.T) {
	mapped := Canonicalize(mustAddr(t, "::ffff:10.0.0.5"))
	if !mapped.Is4() {
		t.Fatalf("expected ::ffff:10.0.0.5 to canonicalise to a 4-byte address, got %v", mapped)
	}
	if dec := allowLAN().Check(mapped, 80, ProtoTCP); !dec.Allowed {
		t.Fatalf("the mapped form of an allowed address was refused: %s", dec.Reason)
	}
}

// TestResolveHost_RejectsOctalLiteral: 0177.0.0.1 is 127.0.0.1 to a permissive
// C resolver. Go's strict literal parser refuses it, so it falls through to
// DNS as a name — where it does not resolve — instead of quietly becoming
// loopback. Either outcome is safe; silently connecting to 127.0.0.1 is not.
func TestResolveHost_RejectsOctalLiteral(t *testing.T) {
	if _, err := netip.ParseAddr("0177.0.0.1"); err == nil {
		t.Fatal("netip.ParseAddr accepted an octal-octet literal; the octal bypass is open")
	}
	if _, err := netip.ParseAddr("2130706433"); err == nil {
		t.Fatal("netip.ParseAddr accepted a bare-integer address; that bypass is open")
	}
}

// TestDial_RebindingFailsClosed is the roadmap's M2 exit gate: a name that
// resolves to an allowed address and then to a denied one must not connect to
// the denied one.
//
// The scripted resolver returns 10.0.0.5 on the first call and 127.0.0.1 on
// every call after. If Dial re-resolved between the check and the connect, the
// dial would land on loopback; because it pins, it lands on 10.0.0.5.
func TestDial_RebindingFailsClosed(t *testing.T) {
	res := &scriptedResolver{answers: [][]netip.Addr{
		{mustAddr(t, "10.0.0.5")},
		{mustAddr(t, "127.0.0.1")},
	}}

	var dialed string
	d := &Dialer{
		Policy:   allowLAN(),
		Resolver: res,
		Net: func(ctx context.Context, network, addr string) (net.Conn, error) {
			dialed = addr
			c1, c2 := net.Pipe()
			go func() { <-ctx.Done(); c2.Close() }()
			return c1, nil
		},
	}

	conn, err := d.Dial(context.Background(), "tcp", "rebind.example", 80)
	if err != nil {
		t.Fatalf("dial to an allowed address failed: %v", err)
	}
	conn.Close()

	if dialed != "10.0.0.5:80" {
		t.Fatalf("dialled %q; the checked address was not the connected address — resolve-then-pin is broken", dialed)
	}
	if res.calls != 1 {
		t.Fatalf("resolver called %d times; anything above 1 means a re-resolution between check and connect", res.calls)
	}
}

// TestAllow_MixedAnswerPinsTheAllowedAddress: a dual-stack device name
// routinely resolves to an allowlisted address and one nobody listed. Refusing
// the pair would stop the monitor while preventing nothing — pinning is what
// defeats rebinding, and the connection goes to the allowed address.
func TestAllow_MixedAnswerPinsTheAllowedAddress(t *testing.T) {
	res := &scriptedResolver{answers: [][]netip.Addr{{
		mustAddr(t, "169.254.169.254"), // denied, and listed first on purpose
		mustAddr(t, "10.0.0.5"),        // allowed
	}}}
	var seen []DeniedError
	d := &Dialer{Policy: allowLAN(), Resolver: res, Auditor: auditorFunc(func(ev DeniedError) {
		seen = append(seen, ev)
	})}

	addr, err := d.Allow(context.Background(), "dual.example", 80, ProtoTCP)
	if err != nil {
		t.Fatalf("a name with one allowed answer was refused: %v", err)
	}
	if addr.String() != "10.0.0.5" {
		t.Fatalf("pinned %s, want the allowed address 10.0.0.5", addr)
	}
	if len(seen) != 0 {
		t.Errorf("a denied answer that blocked nothing was logged as a security event; that is the noise that makes the log unreadable")
	}
}

// TestAllow_AllAnswersDeniedIsRefusedAndAudited: when nothing resolves to an
// allowed address the connection is refused, and every denial is logged
// because it genuinely blocked something.
func TestAllow_AllAnswersDeniedIsRefusedAndAudited(t *testing.T) {
	res := &scriptedResolver{answers: [][]netip.Addr{{
		mustAddr(t, "169.254.169.254"),
		mustAddr(t, "127.0.0.1"),
	}}}
	var seen []DeniedError
	d := &Dialer{Policy: allowLAN(), Resolver: res, Auditor: auditorFunc(func(ev DeniedError) {
		seen = append(seen, ev)
	})}

	if _, err := d.Allow(context.Background(), "bad.example", 80, ProtoTCP); !IsDenied(err) {
		t.Fatalf("expected a denial, got %v", err)
	}
	if len(seen) != 2 {
		t.Fatalf("expected both denials audited, got %d", len(seen))
	}
}

// TestAllow_DenialIsAudited: spec §16 requires every denial to be logged as a
// security event, because repeated denials from one flow is a real signal.
func TestAllow_DenialIsAudited(t *testing.T) {
	var seen []DeniedError
	d := &Dialer{Policy: allowLAN(), Auditor: auditorFunc(func(ev DeniedError) { seen = append(seen, ev) })}

	if _, err := d.Allow(context.Background(), "192.0.2.10", 80, ProtoTCP); err == nil {
		t.Fatal("an address outside the allowlist was allowed")
	}
	if len(seen) != 1 {
		t.Fatalf("expected 1 security event, got %d", len(seen))
	}
	if seen[0].Reason == "" {
		t.Error("the security event carries no reason; an operator cannot act on it")
	}
}

// TestAllow_PortAndProtocolAreChecked: an allowlist that ignored the port
// would turn one allowed device into an allowed subnet.
func TestAllow_PortAndProtocolAreChecked(t *testing.T) {
	p := Policy{Allow: []Rule{
		{Prefix: netip.MustParsePrefix("10.0.0.0/8"), Protocol: ProtoTCP, MinPort: 23, MaxPort: 23},
	}}
	addr := mustAddr(t, "10.0.0.5")

	if dec := p.Check(addr, 23, ProtoTCP); !dec.Allowed {
		t.Fatalf("the allowed port was refused: %s", dec.Reason)
	}
	if dec := p.Check(addr, 22, ProtoTCP); dec.Allowed {
		t.Error("a port outside the rule's range was allowed")
	}
	if dec := p.Check(addr, 23, ProtoUDP); dec.Allowed {
		t.Error("a protocol outside the rule was allowed")
	}
	if dec := p.Check(addr, 23, ProtoICMP); dec.Allowed {
		t.Error("ICMP was allowed by a TCP-only rule")
	}
}

// TestDial_AbortDestroysTheSocket proves invariant I2 reaches the socket: when
// the run deadline fires, a read blocked on a device that never answers
// returns rather than hanging until the process exits.
func TestDial_AbortDestroysTheSocket(t *testing.T) {
	d := &Dialer{
		Policy: allowLAN(),
		Net: func(ctx context.Context, network, addr string) (net.Conn, error) {
			// net.Pipe blocks forever on Read with nothing written — exactly
			// the device that accepts a connection and then says nothing.
			c1, _ := net.Pipe()
			return c1, nil
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	conn, err := d.Dial(ctx, "tcp", "10.0.0.5", 23)
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		buf := make([]byte, 16)
		_, err := conn.Read(buf)
		done <- err
	}()

	cancel()
	err = <-done
	if err == nil {
		t.Fatal("a read on an aborted connection returned successfully")
	}
	te, ok := err.(interface{ Timeout() bool })
	if !ok || !te.Timeout() {
		t.Fatalf("abort produced %v; a transport needs a timeout-shaped error to classify it as ClassTimeout", err)
	}
}

type auditorFunc func(DeniedError)

func (f auditorFunc) Denied(ev DeniedError) { f(ev) }
