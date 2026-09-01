package egress

import (
	"context"
	"fmt"
	"net"
	"net/netip"
)

// Resolver resolves a hostname to addresses. It exists so a Dialer can be
// tested against a scripted DNS answer — including the "first call resolves
// to an allowed address, the next resolves to a denied one" shape that proves
// resolve-then-pin closes DNS rebinding, without a real nameserver involved.
type Resolver interface {
	LookupAddrs(ctx context.Context, host string) ([]netip.Addr, error)
}

// SystemResolver uses Go's own resolver, forced onto the pure-Go DNS client
// rather than the platform's cgo/getaddrinfo path. Some C libraries parse
// numeric-looking hostnames (octal, hex, single-integer dotted-quad forms)
// more liberally than Go's own literal parser does; forcing every deployment
// onto the same resolver keeps address-parsing behaviour in one place instead
// of two paths that can silently disagree.
var SystemResolver Resolver = systemResolver{}

type systemResolver struct{}

func (systemResolver) LookupAddrs(ctx context.Context, host string) ([]netip.Addr, error) {
	r := &net.Resolver{PreferGo: true}
	ips, err := r.LookupIP(ctx, "ip", host)
	if err != nil {
		return nil, err
	}
	out := make([]netip.Addr, 0, len(ips))
	for _, ip := range ips {
		if a, ok := netip.AddrFromSlice(ip); ok {
			out = append(out, a)
		}
	}
	return out, nil
}

// ResolveHost resolves host to its canonical addresses.
//
// A host that is already an IP literal is parsed directly with netip's strict
// parser rather than handed to the resolver at all: netip.ParseAddr rejects
// the leading-zero octal ambiguity ("0177.0.0.1" is not a valid octet) outright,
// so a literal host string never reaches DNS with a shape that a permissive C
// resolver might interpret differently than we do.
func ResolveHost(ctx context.Context, resolver Resolver, host string) ([]netip.Addr, error) {
	if lit, err := netip.ParseAddr(host); err == nil {
		return []netip.Addr{Canonicalize(lit)}, nil
	}
	if resolver == nil {
		resolver = SystemResolver
	}
	addrs, err := resolver.LookupAddrs(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("resolving %q: %w", host, err)
	}
	if len(addrs) == 0 {
		return nil, fmt.Errorf("resolving %q: no addresses returned", host)
	}
	out := make([]netip.Addr, len(addrs))
	for i, a := range addrs {
		out[i] = Canonicalize(a)
	}
	return out, nil
}
