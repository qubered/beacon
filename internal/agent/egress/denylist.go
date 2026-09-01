package egress

import "net/netip"

// hardDenylist can never be satisfied by an allow rule (spec §16). It is not
// configurable through Policy — deployment-specific additions belong in
// Policy.ExtraDeny — because these ranges are wrong for every deployment, not
// just this one.
//
// IPv4-mapped IPv6 forms of these ranges (::ffff:127.0.0.1 and similar) are
// covered without a separate entry: Canonicalize unmaps every address to its
// 4-byte form before anything checks it against this list.
var hardDenylist = mustPrefixes(
	"127.0.0.0/8",       // IPv4 loopback
	"::1/128",           // IPv6 loopback
	"0.0.0.0/8",         // "this network" — meaningless as a connect target
	"169.254.0.0/16",    // IPv4 link-local; contains 169.254.169.254, the cloud metadata address
	"fe80::/10",         // IPv6 link-local
	"fd00:ec2::254/128", // AWS IMDSv2's IPv6 metadata address, outside fe80::/10
	"::/128",            // the unspecified address
)

// loopbackPrefixes are the entries Policy.AllowLoopback stands down. Keeping
// them as a named subset rather than a string comparison means adding a
// loopback range to hardDenylist without adding it here fails closed.
var loopbackPrefixes = mustPrefixes("127.0.0.0/8", "::1/128")

func isLoopbackPrefix(p netip.Prefix) bool {
	for _, lp := range loopbackPrefixes {
		if lp == p {
			return true
		}
	}
	return false
}

func mustPrefixes(cidrs ...string) []netip.Prefix {
	out := make([]netip.Prefix, len(cidrs))
	for i, c := range cidrs {
		out[i] = netip.MustParsePrefix(c)
	}
	return out
}
