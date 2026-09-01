// Package egress is the locally authoritative egress policy. This is the single most important security control.
//
// Invariant I7 and decision D17: Core can propose a policy change, which the agent surfaces for operator approval with a diff. Core can never silently widen it. This is what makes a compromised control plane survivable rather than building-wide.
//
// Default-deny allowlist of ranges, ports and protocols, plus a hard denylist that cannot be overridden: loopback, link-local, metadata addresses, the platform's own management addresses and the database — including their IPv6 equivalents and IPv4-mapped IPv6 forms, which are a standard bypass. Normalise every resolved address to a canonical form before matching.
//
// Resolve, then pin. Check the allowlist against the resolved address and connect to that address explicitly; never re-resolve between check and connect or DNS rebinding walks straight through. A redirect to a denied host is a hard failure, not a follow.
//
// Write-capable nodes and multicast are likewise locally enabled, never remotely enabled. Every denial is logged as a security event.
//
// Policy.AllowLoopback is the one sanctioned relaxation, for test/devsim in CI and for a simulator running on the agent host. It stands down only the loopback entries — link-local and metadata addresses stay denied — and it deliberately has no wire representation, so a policy Core proposes cannot carry it. Keep it that way when internal/proto learns to deserialise a Policy in M4.
package egress
