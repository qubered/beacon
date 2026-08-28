# D29 — Go for Core and Agent; React/TypeScript for the web tier

**Status:** accepted
**Constrains:** D13, D15, I2, spec §6.4, §7.6

## Decision

Core and Agent are one Go codebase producing separate binaries from `cmd/`.
The web tier is a separate React/TypeScript application in `web/`, served by
Core as static assets and talking to it over the API.

Postgres is the only stateful dependency (D22).

## Why

D15 requires the agent runtime to share Core's stack so exactly one executor
implementation exists. That makes the stack choice a single decision covering
both tiers, and the agent's constraints are the binding ones:

- **§7.6 puts an agent on a small single-board computer in a rack.** Go
  cross-compiles to a static ~20MB binary with no runtime to install. Every
  alternative either ships an interpreter or needs a per-architecture native
  build step.
- **§6.4 mandates a non-backtracking regex engine**, because users supply the
  patterns. Go's standard `regexp` *is* RE2. In every other candidate stack this
  is a native dependency that must be built for each agent architecture — and the
  editor's live match preview has to agree with the runtime exactly, on precisely
  the patterns that matter.
- **I2 requires the deadline to reach the socket.** Go's `net.Conn` deadlines and
  `context` cancellation reach the file descriptor. A timeout that only checks
  the clock between nodes is not a timeout, and it is the default outcome in
  runtimes where cancellation is cooperative.
- **§6.5 requires a sandbox that runs off the main loop with the thread as the
  real backstop.** A goroutine with a hard kill and a memory-capped interpreter
  satisfies this without blocking held sockets, heartbeats or metric scrapes.
- Native SNMP, ICMP raw sockets, mTLS, SSH and Prometheus exposition all have
  mature Go implementations.

## Cost, accepted

Two languages. The flow builder is a browser application regardless of the
backend language, so this buys nothing to avoid — the boundary is the API, and
it exists in every option. The type system in `internal/flow/types` is mirrored
in TypeScript for the editor; a generator keeps them in step, and a CI check
fails the build if they drift.

## Rejected

**TypeScript everywhere.** One language and a faster editor loop, but it loses on
all four of the constraints above at once: heavy agent artifact, `node-re2` as a
per-architecture native build, cooperative cancellation, and a sandbox story that
is harder to make preemptive.

**Rust.** Wins on artifact size and on memory safety for a byte-protocol engine,
which is a real argument. Rejected on delivery speed and on a thinner ecosystem
for exactly the protocols this product exists to speak.
