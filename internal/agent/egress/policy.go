package egress

import (
	"fmt"
	"net/netip"
)

// Protocol is the outward protocol an egress rule or check applies to.
type Protocol string

const (
	ProtoTCP  Protocol = "tcp"
	ProtoUDP  Protocol = "udp"
	ProtoICMP Protocol = "icmp"
)

// Rule is one allowlist entry: an address range, a protocol, and — for TCP and
// UDP — an inclusive port range. Ports are ignored for ICMP.
type Rule struct {
	Prefix   netip.Prefix
	Protocol Protocol
	MinPort  int
	MaxPort  int
}

func (r Rule) matches(addr netip.Addr, port int, proto Protocol) bool {
	if r.Protocol != proto || !r.Prefix.Contains(addr) {
		return false
	}
	if proto == ProtoICMP {
		return true
	}
	return port >= r.MinPort && port <= r.MaxPort
}

// Policy is one agent's locally authoritative egress policy (invariant I7,
// decision D17). Core may propose a replacement Policy, which the agent
// surfaces for operator approval with a diff — Core never applies one
// directly, and nothing in this package accepts a policy update from a remote
// source, which is what keeps that promise structural rather than a
// convention someone can forget.
//
// The zero value denies everything. That is deliberate: a Policy nobody has
// configured should refuse outbound connections, not silently allow them.
type Policy struct {
	Name  string
	Allow []Rule

	// ExtraDeny holds deployment-specific hard-denied ranges — the platform's
	// own management addresses and the database (spec §16) — layered on top of
	// the built-in constants in denylist.go. Exactly like that built-in list,
	// nothing in Allow can override an ExtraDeny match.
	ExtraDeny []netip.Prefix

	// AllowLoopback stands down *only* the loopback entries of the hard
	// denylist. Link-local, metadata and the unspecified address stay denied,
	// and ExtraDeny is untouched.
	//
	// It exists for two real cases: test/devsim running fake devices in CI,
	// and an operator running a simulator on the agent host while authoring a
	// flow. Without it the acceptance scenarios could not be run at all, and
	// the workaround people would reach for — weakening the denylist itself —
	// is far worse.
	//
	// This is deliberately a local flag with no wire representation. A policy
	// Core proposes cannot carry it, so it can never become a remote widening
	// of an agent's reach (invariant I7, decision D17). Anything that gains
	// the ability to deserialise a Policy from Core must keep it that way.
	AllowLoopback bool
}

// Decision is the result of checking one resolved, canonical address.
type Decision struct {
	Allowed bool
	Reason  string
}

func deny(format string, args ...any) Decision {
	return Decision{Allowed: false, Reason: fmt.Sprintf(format, args...)}
}

// Check evaluates addr:port/proto against the hard denylist, then the extra
// deny list, then the allowlist, in that order — deny always wins.
//
// addr must already be canonical (see Canonicalize). Check does not
// canonicalize on its own: ResolveHost is the one place addresses are
// normalised, and every caller in this package goes through it, so Check
// itself stays a pure lookup with nothing to get wrong.
func (p Policy) Check(addr netip.Addr, port int, proto Protocol) Decision {
	if !addr.IsValid() {
		return deny("invalid address")
	}
	for _, pfx := range hardDenylist {
		if !pfx.Contains(addr) {
			continue
		}
		if p.AllowLoopback && isLoopbackPrefix(pfx) {
			continue
		}
		return deny("%s is in the hard denylist (%s)", addr, pfx)
	}
	for _, pfx := range p.ExtraDeny {
		if pfx.Contains(addr) {
			return deny("%s is in the agent's deny list (%s)", addr, pfx)
		}
	}
	for _, r := range p.Allow {
		if r.matches(addr, port, proto) {
			return Decision{Allowed: true}
		}
	}
	return deny("%s:%d/%s is not in the allowlist", addr, port, proto)
}
