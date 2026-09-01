package egress

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
)

// DeniedError is returned when the policy refuses a connection. Transports
// map it to a failure class; the run inspector shows Reason verbatim, because
// "10.0.0.5:23/tcp is not in the allowlist" is a message an operator can act
// on and "connection failed" is not.
type DeniedError struct {
	Host     string
	Addr     netip.Addr
	Port     int
	Protocol Protocol
	Reason   string
}

func (e *DeniedError) Error() string {
	return fmt.Sprintf("egress denied: %s", e.Reason)
}

// IsDenied reports whether err is or wraps a policy denial.
func IsDenied(err error) bool {
	var d *DeniedError
	return errors.As(err, &d)
}

// Auditor receives every denial as a security event (spec §16: "Log every
// denial as a security event. Repeated denials from one flow is a real
// signal"). A nil Auditor is valid and drops the events.
type Auditor interface {
	Denied(ev DeniedError)
}

// Dialer is the only sanctioned way for a node to open an outbound
// connection. Nodes never call net.Dial directly — going through here is what
// makes the egress check unskippable rather than something each new transport
// has to remember.
type Dialer struct {
	Policy   Policy
	Resolver Resolver
	Auditor  Auditor

	// Net dials an already-resolved address. It exists for tests; nil means
	// the real network.
	Net func(ctx context.Context, network, addr string) (net.Conn, error)
}

// Allow checks host:port/proto and returns the single pinned address the
// caller must then use. Nothing re-resolves after this point.
//
// This is the whole of the rebinding defence: the address returned here is the
// address connected to. A caller that takes the allow decision and then hands
// the *hostname* to something that resolves it again — an HTTP client, a
// redirect follower — has reopened the hole, which is why Dial below connects
// to the pinned literal and why HTTP pins per-connection in transport.
func (d *Dialer) Allow(ctx context.Context, host string, port int, proto Protocol) (netip.Addr, error) {
	addrs, err := ResolveHost(ctx, d.Resolver, host)
	if err != nil {
		return netip.Addr{}, err
	}

	// Take the first allowed address and pin it; refuse only if none is
	// allowed.
	//
	// Requiring *every* answer to be allowed is tempting and wrong. It buys no
	// security — pinning is what defeats rebinding, and an attacker who
	// returns one allowed and one denied address gains nothing when the
	// connection goes to the allowed one — while it guarantees false refusals
	// on any dual-stack network, where a device name routinely resolves to
	// both an allowlisted IPv4 address and an IPv6 address nobody listed.
	// A monitor that silently stops because of that is a worse outcome than
	// the attack it does not prevent.
	var denials []DeniedError
	for _, addr := range addrs {
		dec := d.Policy.Check(addr, port, proto)
		if dec.Allowed {
			return addr, nil
		}
		denials = append(denials, DeniedError{Host: host, Addr: addr, Port: port, Protocol: proto, Reason: dec.Reason})
	}

	// Audit only once the connection is actually refused. A denied answer
	// alongside an allowed one blocked nothing, and logging it as a security
	// event would fill the log with dual-stack noise — which is how the signal
	// "repeated denials from one flow" stops being worth reading.
	for _, ev := range denials {
		d.audit(ev)
	}
	return netip.Addr{}, &denials[0]
}

// AllowAddr checks an already-resolved address. Use it when the address came
// from somewhere other than a hostname — a redirect target, a sweep range —
// so the denial is still audited.
func (d *Dialer) AllowAddr(addr netip.Addr, port int, proto Protocol) error {
	addr = Canonicalize(addr)
	if dec := d.Policy.Check(addr, port, proto); !dec.Allowed {
		ev := DeniedError{Host: addr.String(), Addr: addr, Port: port, Protocol: proto, Reason: dec.Reason}
		d.audit(ev)
		return &ev
	}
	return nil
}

// Dial resolves, checks and connects — to the pinned address, never to the
// hostname.
//
// The deadline reaches the socket (invariant I2): ctx governs the dial, and
// the caller is expected to keep governing reads and writes with the same ctx
// via an abort adapter. A Dial that respected the deadline and then handed
// back a socket nothing can interrupt is not a timeout.
func (d *Dialer) Dial(ctx context.Context, network, host string, port int) (net.Conn, error) {
	proto := ProtoTCP
	switch network {
	case "tcp", "tcp4", "tcp6":
		proto = ProtoTCP
	case "udp", "udp4", "udp6":
		proto = ProtoUDP
	default:
		return nil, fmt.Errorf("egress: unsupported network %q", network)
	}

	addr, err := d.Allow(ctx, host, port, proto)
	if err != nil {
		return nil, err
	}
	return d.DialPinned(ctx, network, addr, port)
}

// DialPinned connects to an address that has already been checked. It is
// separate from Dial so that a caller which must check and connect in two
// steps — HTTP, which resolves inside its own transport — cannot accidentally
// pass a hostname here.
func (d *Dialer) DialPinned(ctx context.Context, network string, addr netip.Addr, port int) (net.Conn, error) {
	target := net.JoinHostPort(addr.String(), fmt.Sprint(port))
	dial := d.Net
	if dial == nil {
		dial = func(ctx context.Context, network, addr string) (net.Conn, error) {
			var nd net.Dialer
			return nd.DialContext(ctx, network, addr)
		}
	}
	conn, err := dial(ctx, network, target)
	if err != nil {
		return nil, err
	}
	return withAbort(ctx, conn), nil
}

func (d *Dialer) audit(ev DeniedError) {
	if d.Auditor != nil {
		d.Auditor.Denied(ev)
	}
}

// withAbort is the abort adapter every transport needs (spec §6.2: "not every
// I/O API accepts a cancellation signal; those that don't need an explicit
// per-transport abort adapter that destroys the socket").
//
// net.Conn is exactly that case — its Read and Write take no context — so a
// goroutine watches ctx and closes the socket when it fires. Without this, a
// device that accepts a connection and then never speaks holds the run open
// past its deadline, and the run's own timeout only notices between nodes.
func withAbort(ctx context.Context, conn net.Conn) net.Conn {
	if deadline, ok := ctx.Deadline(); ok {
		// Belt: the kernel-level deadline covers the common case with no
		// goroutine at all.
		_ = conn.SetDeadline(deadline)
	}
	done := make(chan struct{})
	ac := &abortConn{Conn: conn, done: done}
	go func() {
		select {
		case <-ctx.Done():
			// Braces: cancellation without a deadline (an aborted test run,
			// a shutting-down agent) still destroys the socket.
			ac.abort()
		case <-done:
		}
	}()
	return ac
}

type abortConn struct {
	net.Conn
	done      chan struct{}
	aborted   atomic.Bool
	closeOnce sync.Once
}

func (c *abortConn) abort() {
	c.aborted.Store(true)
	// Unblocking a blocked Read/Write requires closing the socket; there is no
	// gentler primitive that interrupts one.
	_ = c.Conn.Close()
}

func (c *abortConn) Read(b []byte) (int, error) {
	n, err := c.Conn.Read(b)
	return n, c.translate(err)
}

func (c *abortConn) Write(b []byte) (int, error) {
	n, err := c.Conn.Write(b)
	return n, c.translate(err)
}

func (c *abortConn) Close() error {
	c.closeOnce.Do(func() { close(c.done) })
	return c.Conn.Close()
}

// translate turns the "use of closed network connection" a caller sees after
// an abort into a timeout error, so a transport reports ClassTimeout rather
// than the internal artefact of how the abort was implemented.
func (c *abortConn) translate(err error) error {
	if err != nil && c.aborted.Load() {
		return errAborted
	}
	return err
}

var errAborted error = timeoutError{}

type timeoutError struct{}

func (timeoutError) Error() string { return "i/o timeout: deadline reached, socket destroyed" }
func (timeoutError) Timeout() bool { return true }
