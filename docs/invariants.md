# Invariant map

Spec §3 lists twelve properties that must hold. Violating one is a bug regardless
of how convenient it was.

This is where each is enforced and where it is proven. Keep it current — an
invariant with no test is an aspiration.

| # | Invariant | Enforced in | Proven by | Status |
|---|---|---|---|---|
| I1 | A transport node emits `bytes`. Decoding is always a separate, explicit node. | `nodes/transport` port signatures; `flow/types.Check` refuses bytes→string | `flow/types` connection tests; `TestTransports_EmitBytesNotStrings` scans every registered transport descriptor; `byteops.decode` and `parse.regex_extract` exercise the bytes→string→record chain end to end (`test/acceptance`) | ✅ |
| I2 | Every flow run terminates. Deadline, node budget and loop caps enforced — and **the deadline reaches the socket**. | `engine/runtime.Run` (deadline via `context.WithTimeout`, node budget checked before dispatch); `egress.withAbort` destroys a `net.Conn` when the run context fires; `icmp_ping` closes its socket the same way | `TestDeadline_TerminatesASlowRun`, `TestNodeBudget_AbortsWhenExceeded`, `TestDial_AbortDestroysTheSocket`, `TestTCPRequest_DeadlineReachesTheSocket` (a silent device against a 150ms deadline) | ✅ for the tier-1 transports |
| I3 | A published flow version is immutable. Editing forks a draft. | `flow/graph.Version` (`ErrPublished` on any mutation) and a Postgres trigger in migration 0001 | `TestVersion_PublishedIsImmutable`; a live psql session confirmed the trigger refuses an UPDATE | ✅ enforced at both the in-memory and storage layers |
| I4 | A secret is never readable, never captured, never exported, never reachable from an expression or the sandbox. | `secrets`; `Frame.Sealed` + `Frame.Derive`; `engine/capture` masking by value scan | Test that a secret composed into a larger payload is masked in the hex dump; tests that `expr` and `sandbox` refuse sealed frames; a Pack export test | M7 |
| I5 | A device is assigned to exactly one agent. | Schema constraint; `core/assign` | Store test: a second assignment is rejected | M4 |
| I6 | An agent keeps monitoring while disconnected, and backfills on reconnect. | `agent/scheduler`, `agent/spool`, `core/ingest` | Link-partition integration test | M4 |
| I7 | An agent's egress policy is authoritative on the agent. Core cannot silently widen it. | `agent/egress`: default-deny, an un-overridable hard denylist, resolve-then-pin, and `Policy.AllowLoopback` deliberately given no wire representation | `TestPolicy_ZeroValueDeniesEverything`, `TestPolicy_DenylistBeatsAllowlist` (an allow-everything policy still cannot reach loopback or metadata), `TestDial_RebindingFailsClosed`, `TestHTTPRequest_RedirectToADeniedHostFails` | local enforcement ✅ / Core-proposed change with operator approval M4 |
| I8 | Backfilled state changes older than a configured age update history but do not notify. | `core/ingest`, `core/alerting` | Backfill test asserting history is written and notifications are not | M4 |
| I9 | An unreachable agent marks its devices `unknown` and suppressed — never `down`. | `core/alerting` (agents as implicit parents) | Test: agent silence produces one alert, not sixty | M5 |
| I10 | Scheduling lateness never compounds. | `agent/scheduler` | Test: a run overrunning its interval still lands on the original grid | M3 |
| I11 | Metric label values come from a declared, bounded set. Free text is rejected at authoring time. | `nodes/emit`; `flow/validate`; the editor | Publish-gate test rejecting a free-text label | M9 |
| I12 | Maintenance windows and silences suppress *notification*, never *collection*. | `core/alerting` | Test: runs are stored throughout a window and delivery is muted | M5 |

## Design principles that are review rules

Two of the nine principles in spec §2 are enforceable rather than aspirational,
so CI enforces them:

- **No vendor names in the codebase** (principle 1). A vendor string outside a
  Pack fixture or a test means the abstraction has failed. `tools/lint/vendorcheck.sh`
  fails the build. The denylist file it reads is the one sanctioned place a vendor
  name may appear in the tree, and it is excluded from its own scan.
- **Bytes first, strings second** (principle 3). A registry test asserts that no
  transport node declares a `string` primary output.

The other seven are judgement calls and belong in review, not in a linter.
