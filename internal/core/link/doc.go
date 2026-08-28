// Package link is the Core side of the agent link. Core accepts connections; it never dials an agent.
//
// Spec §7.2. Mutually authenticated, persistent, binary. Also carries the test_run path from spec §7.1: a separate synchronous request asking a named agent to execute a specific draft flow once, right now, streaming per-node results back. That path bypasses assignment and spooling entirely.
package link
