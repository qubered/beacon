package egress

import "net/netip"

// Canonicalize normalises addr to the form every check in this package is
// matched against: an IPv4-mapped IPv6 address (::ffff:a.b.c.d) collapses to
// its 4-byte form, and any zone identifier is stripped since it has no
// bearing on which range the address falls in.
//
// ::ffff:127.0.0.1, 0177.0.0.1 and a CNAME to a metadata address are the same
// bypass wearing different hats (spec §16 risk note). They stay closed only if
// every address is canonicalised in exactly one place before it is checked —
// this function — rather than each caller normalising its own way and
// eventually disagreeing.
func Canonicalize(addr netip.Addr) netip.Addr {
	return addr.Unmap().WithZone("")
}
