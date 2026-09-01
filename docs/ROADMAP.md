# Build plan

The specification describes *what* to build and why. This describes *in what
order*, and — more usefully — what has to be true before each step counts as
done.

Read [the spec](spec/beacon-spec.md) first. Nothing here overrides it.

## How this plan is sequenced

Three rules produced this order.

**1. The falsification tests are gates, not a final phase.** Spec §18's five
acceptance scenarios are the design's falsification tests. Deferring all five to
the end would mean discovering a catalogue gap after the catalogue is finished.
Each one is instead attached to the milestone that first makes it possible, and
each is a CI gate from that point on. If a scenario cannot be built entirely
through the UI, the fix is a **node** — never a vendor adapter, never a special
case in the engine.

**2. Build the executor once, early, and never build a second one.** D13 and D15
exist to prevent drift between Core's execution and an agent's. So the engine is
milestone 1, and both binaries consume it from the start. The Core/Agent *split*
(M4) is then a transport and lifecycle problem, not an execution problem.

**3. Where a decision is hard to reverse, make it early even if the feature is
late.** Egress control (M2), the capability mechanism (M4) and cardinality
enforcement (M9) are all cheap to build in and painful to retrofit — because each
one is a *refusal*, and refusals added later break flows people already wrote.
The sandbox is the opposite case and lands last on purpose (D10): every time
someone reaches for it before it exists, that is a free signal about which node is
missing from the catalogue.

---

## M0 — Foundations

**Goal:** a repository anyone can clone and get running in one command, with the
rules of the codebase encoded rather than described.

Ships:

- Go module, package skeleton with responsibilities documented per package, three
  buildable commands. *(done)*
- The frame type system and edit-time connection validation
  (`internal/flow/types`), the `Frame` value with its seal (`internal/engine/frame`),
  and the node registry from which agent capability sets derive
  (`internal/nodes/registry`). *(done)*
- Postgres schema baseline: sites, agents, devices, device edges, credentials,
  flows, flow versions, monitors — site-scoped from migration 0001 (D30).
  Partition machinery for the time-series tables with **pre-created partitions
  and a catch-all default**, because if rollover fails every insert fails and the
  spool sheds everything.
- Config with the §6.2 bounds as enforced ceilings, structured logging, process
  self-metrics.
- `docker compose up` brings up Postgres, Core and Grafana.
- CI: build, vet, test, lint, migration round-trip, and the **vendor-name check**
  (design principle 1 — a vendor string outside a Pack fixture or a test fails the
  build).

**Exit gate:** `make dev` from a clean clone reaches a running Core with a
migrated database. CI is green and enforces the vendor-name rule.

---

## M1 — The flow spine ✅ shipped

**Goal:** a flow can be defined, validated and executed against recorded bytes,
with every node's input and output captured. No network yet.

Ships:

- `internal/flow/graph`: DAG model, typed ports, immutable published versions,
  content addressing, the Loop back-edge as the only legal cycle (I3, D28).
- `internal/engine/runtime`: the executor. Input-satisfied firing, bounded branch
  concurrency, fan-out sharing an immutable value, variadic fan-in, implicit error
  ports, and the **branch-join select rule** — readiness means all reachable
  upstream branches have settled, not all connected inputs have a frame.
- Deadline, node budget and loop caps, with the deadline plumbed as a context that
  every future transport must honour at the socket (I2).
- `internal/engine/capture`: per-node input/output, truncation flags, masking by
  value scan.
- First nodes, chosen to exercise the engine rather than to be useful:
  `byteops.decode`, `parse.regex_extract`, `emit.assert`, `emit.emit_status`,
  `control.if`.
- `beaconctl flow run --fixture` executes a flow against a recorded byte stream.

**Exit gate:** an `If` whose two branches join into one downstream input
completes rather than deadlocking. A flow with a 200ms deadline and a deliberately
slow node terminates at 200ms. A run that reaches no `Emit Status` yields
`unknown` plus an execution warning. Every one of these has a test.

**Risk:** the branch-join rule and the error-port semantics are the two places
where a plausible implementation is subtly wrong and the symptom is a hang in
production. Write the tests first.

**What actually happened:** the risk called it correctly. Building this surfaced
two real bugs that no amount of reading the spec first would have caught —
`capture.Recorder` didn't structurally satisfy `runtime.Capturer` because
`Inputs`/`Outputs` are named types, not aliases, and it compiled cleanly right up
until something tried to use it that way; and the unconnected-error-port path
aborted the run without cascading the skip through the failing node's normal
outputs, so a downstream node ended up with no capture entry at all instead of
one marked skipped. Both were caught by the first test that wired two real
pieces together rather than a fake standing in for one side — worth remembering
for M2 through M4, which have far more seams than M1 did.

---

## M2 — Framing, transports, and the outward-facing controls ✅ shipped

**Goal:** real bytes over real sockets, with the safety controls in place before
anything can send.

Ships:

- `internal/nodes/framing`: the read-until strategy set — delimiter, length
  prefix, fixed, regex, start/end markers, quiet period, until close, message
  count — plus IAC stripping, max bytes and discard-before. **This is the framing
  engine and it is what unlocks the long tail.** Get it right here; it is reused
  by `Expect` and `Split Frames` unchanged.
- Tier-1 transports: ICMP Ping, TCP Connect, TCP Request, HTTP Request, DNS Query,
  TLS Inspect. Each with an explicit abort adapter that destroys the socket when
  the deadline fires.
- `internal/agent/egress`: default-deny allowlist; the un-overridable denylist
  including IPv6 equivalents and IPv4-mapped forms; **resolve-then-pin** with no
  re-resolution between check and connect; redirect-to-denied as a hard failure;
  every denial logged as a security event.
- `internal/agent/ratelimit`: per-device concurrency cap and a token bucket
  spanning all monitors bound to a device. Throttled is a distinct outcome, not a
  failure, and it increments a counter.

**Exit gate:** a DNS rebinding attempt fails closed, with a test that resolves to
an allowed address and then to a denied one. A redirect to a denied host fails.
Two monitors on one device respect a shared bucket. A ping monitor works end to
end from `beaconctl`.

**Risk:** egress normalisation. `::ffff:127.0.0.1`, `0177.0.0.1`, and a CNAME to
a metadata address are all the same bypass wearing different hats. Canonicalise
once, in one function, and test the ugly forms explicitly.

**What actually happened:** the normalisation risk was largely handled by
picking the right tools — `netip`'s strict parser rejects `0177.0.0.1` and
`2130706433` outright, and one `Canonicalize` call unmapping `::ffff:` forms
before any range check closed the rest. Two things did surface that reading the
spec first would not have caught.

The first was the HTTP client. Checking the URL's host and then handing the
*hostname* to `http.Transport` re-resolves inside the transport's own dial,
which reopens rebinding after the check has passed — the pin has to happen
inside `DialContext`, not before `client.Do`. Anything that resolves on its own
behalf has this shape, so SSH, MQTT and the SNMP client in M8 each need the same
treatment rather than inheriting it.

The second was a genuine tension the spec does not resolve: the hard denylist
blocks loopback, which is correct in production and makes `test/devsim`
unreachable in CI — and the acceptance scenarios in M7 and M8 are all built
against it. Weakening the denylist would have been the wrong answer, so
loopback is stood down by `Policy.AllowLoopback`, a local flag with **no wire
representation**, deliberately so a policy Core proposes cannot carry it. When
`internal/proto` learns to deserialise a Policy in M4, keeping that field
un-serialisable is load-bearing for I7, not an oversight to tidy up.

A review caught two defects the tests had missed, both worth remembering.

A regex read-until pattern that can match the empty string — `.*`, `\d*`, and
anything else with a leading `*` or `?` quantifier — matched **before the first
read**, so the read returned an empty frame having never touched the socket. A
dead device and an empty response became indistinguishable, which is precisely
the confusion the error classes exist to prevent. `Validate` now refuses such a
pattern at edit time, and `Read` carries the same zero-advance backstop `Split`
already had. The general lesson for M7's `Expect`: the framing engine is asked
whether it has a complete frame *before* it reads, so any strategy that can be
satisfied by an empty buffer is a silent-success bug.

The second was in the rate limiter: the concurrency gate was a buffered
channel, whose capacity is fixed at creation, so *lowering* a device's cap did
nothing. The limit read as 1 in the configuration and behaved as 4 at the
device — the worst shape a safety limit can have. It is now a counting
semaphore that can be resized in both directions. Anything else in M3 that
looks reconfigurable needs a test that actually reconfigures it.

Also worth carrying forward: the roadmap's M1 note about seams held again. The
bugs were at boundaries between two real pieces — the framing engine against a
real socket, the egress check against a real HTTP client — and not in either
piece tested alone. Note that both defects above were found by review rather
than by a test, and both were in code that had tests passing over it.

---

## M3 — Monitors, scheduling, storage, and something to look at

**Goal:** monitors run on a schedule, results persist, uptime is computable, and
there is a screen showing it.

Ships:

- `internal/agent/scheduler`: deterministic phase jitter from a hash of the
  monitor id; lateness that never compounds (I10); catch-up that **snaps forward
  and records a gap** rather than replaying backdated runs; schedule reset with a
  suppressed missed-run increment on configuration change.
- `internal/agent/executor`: run context (device/monitor/run/previous/state/
  `secret()`), var precedence, whole-flow retries.
- `internal/agent/spool`: durable, bounded by size and age, **shedding captures
  before results**, with both drop classes counted.
- Storage: `monitor_runs` with the scheduled slot as a unique execution fence,
  `monitor_state_periods`, `monitor_last_values`, `events`. Retention by dropping
  partitions. Captures dropped at write time, never written and pruned later.
- Uptime as time-weighted from state periods, reported **both raw and excluding
  maintenance** (D23).
- Web tier bootstrap: status wall and device list, read-only.

**Exit gate:** 400 monitors on a 60s interval spread across the minute rather
than firing in one second. A monitor that takes 28s to time out still lands on the
original grid. A 10-minute stop produces one recorded gap, not 120 queued runs.
Changing an interval does not re-weight yesterday's uptime.

---

## M4 — The split

**Goal:** a remote agent in a different network segment, enrolled, assigned and
reporting — and still monitoring when the link drops.

Ships:

- `internal/proto` and both link packages: persistent, mutually authenticated,
  binary. The message set from §7.2.
- Enrolment: one-time scoped token, agent-generated keypair, short-lived
  certificate, **pending approval queue by default**, immediate revocation with a
  local-state wipe.
- Capability declaration generated from the node registry; Core refuses assignment
  on a gap and says which node type is missing.
- Assignment: one agent per device enforced by the schema (I5), monitors
  inheriting their device's agent, bulk reassignment by tag or subnet.
- Ingest: at-least-once with acknowledgement, deduplicated on the scheduled-slot
  fence. Backfill replays the state machine for history while suppressing
  notifications older than the configured age (I8), emitting one summary.
- Clock discipline: skew checked at enrolment, carried on every heartbeat,
  exported and alerted; **liveness judged against Core's receipt time**.
- Test-run-from-the-editor as a separate synchronous streaming path.
- Core's local agent, enrolled over loopback with no special treatment (D13).

**Exit gate:** pull the link for 20 minutes; the agent keeps running its
monitors, spools, and on reconnect the history shows the outage at its real time
with exactly one summary notification. Assign a flow needing a node the agent
lacks and get a UI message naming the node, not a 6pm failure.

**Risk:** backfill notification suppression. Getting this wrong pages someone at
3am for a flap that resolved half an hour earlier, and that is the failure that
makes people turn the system off.

---

## M5 — Alerting

**Goal:** the platform can be trusted to page a human.

Ships:

- The persisted state machine: `up → suspect → down → recovering → up`, requiring
  **both** *n* consecutive failures **and** a minimum dwell time.
- Transitions as a stored boolean on the run row, not alerting-layer logic.
- Error-class routing — the valuable distinction is `protocol` (*the device
  answered and we misread it*) going to the flow author rather than the AV
  on-call, because twelve monitors going `protocol` after a firmware update is a
  completely different message from twelve going `timeout`.
- Flapping: weighted state-change percentage over a **time-based** window, two
  thresholds with hysteresis, computed from a fixed-size ring on the monitor row.
  Flap percentage surfaced in the UI as a first-class number.
- Dependency suppression over the device **DAG**, with device health as a
  persisted worst-of rollup over monitors flagged as reachability checks; a grace
  period of roughly one parent interval; suppression recorded as a state rather
  than a discard; and recovery rolled up into one message.
- Agents as implicit parents (I9) — one alert, not sixty.
- Maintenance windows and silences that suppress notification only (I12), with
  IANA timezone names, mandatory silence expiry, a maximum duration and a weekly
  digest. Show mode.
- Delivery: fan-out by URL scheme, with a plain webhook always available as a
  fallback (D24). Grouping and escalation.

**Exit gate:** take down a parent switch with fourteen dependents and receive one
alert, then one recovery summary. Oscillate a monitor for an hour and receive one
"flapping" message plus one on exit. A silence cannot be created without an
expiry.

---

## M6 — The builder

**Goal:** an AV tech who has never seen the codebase can author a working flow
against a real device.

Ships — and none of these five is polish; they are the product:

1. **Test run against a real device.** A persistent bar with a device selector
   executing the *current unsaved draft*, painting every node pass/fail/skipped
   with per-node timing. Nobody can author a byte protocol blind.
2. **The byte inspector.** Hex dump with an ASCII gutter, offsets, selectable
   ranges, a ruler showing where the framing strategy split the stream, the
   downstream decode rendered live, and copy-as options. **The single most-used
   debugging tool in the product — budget real time for it.**
3. **Capture and replay.** Record a response once, save it as a fixture on the
   flow, iterate on parsing with no device present.
4. **Semantic version diff** — "Regex Extract: pattern changed", never a JSON
   diff — with the list of monitors publishing would affect.
5. **A palette that finds things.** Synonym search, so "telnet", "ascii" and
   "port 23" all surface the raw-ASCII TCP preset. Three tiers, filtered by
   default.

Plus the guardrails, all at edit time: connection refusals carrying a suggested
fix node; unreachable nodes greyed; and publish blocked on no `Emit Status`,
undeclared metric labels, an uncapped loop, worst-case run duration exceeding the
interval, or a capability no assigned agent declares.

**Exit gate:** someone who has not read this repository authors and publishes a
working HTTP check without assistance. The type mirror between Go and TypeScript
is generated, and CI fails on drift.

**Risk:** the byte inspector is the piece most likely to be under-budgeted because
it looks like a widget. It is the difference between a tool a tech can debug with
and one they abandon.

---

## M7 — Connection Scope and sessions → **acceptance A, C, D**

**Goal:** multi-turn and push protocols, which is where the product's thesis
lives.

Ships:

- `Connection Scope` as a container node owning one connection for the lifetime of
  its body, with `Send` and `Expect` operating on the ambient connection. Modes:
  transient, pooled, session.
- Named bindings — flow-scoped, assigned once, unassigned reads being a
  publish-time error rather than a runtime `undefined`. Without these a six-step
  conversation becomes spaghetti.
- Inline `{{ }}` interpolation with **per-field-type escaping**: a bytes template
  hex-escapes, a URL field URL-encodes, a raw field does neither.
- Sealed frames end to end: composable into a payload, hashable, never capturable,
  never exportable, never readable from an expression (I4).
- `internal/agent/sessions`: the connection supervisor, on-open flows, keepalive
  pokes, backpressure with counted drops, and the **staleness rule** as the single
  reconciliation between push and poll.
- Derived monitors reading subscription state — same scheduler, same result path,
  same alert pipeline.
- The UI must **force the choice at creation time** for a source that is silent by
  design: give it a heartbeat, configure a keepalive, or poll it instead. A
  silent-by-design source with a liveness window is a false-alarm generator and
  will destroy trust faster than any bug.

**Gates — acceptance scenarios, built entirely in the UI against `test/devsim`:**

- **C — control processor, raw ASCII on port 23.** Proves IAC stripping and
  quiet-period reads.
- **D — projector, challenge-response.** The hardest test and the reason
  Connection Scope exists. Proves the sealed-frame model: a secret used inside a
  computed payload, never seen, never captured.
- **A — wireless receiver, session mode with per-channel fan-out.** Proves the
  same visual body serves both polling and subscription, and that a sibling
  product family with different command names and a different offset is *lookup
  and scale configuration*, not a second adapter. **That difference is the entire
  thesis.**

---

## M8 — The rest of the catalogue → **acceptance B, E**

Ships: SNMP (get/next/bulk/walk/table, type-aware decoding); GraphQL with the
`errors` array surfaced separately; WebSocket, UDP, SSH Exec, MQTT, Modbus TCP,
CIDR Sweep; the byte nodes (Build Bytes with post-assembly length and checksum
placeholders, Encode/Decode, Checksum, HMAC, Split Frames, Slice, Bit Field,
Endian Swap); the parse and transform sets; ForEach, Loop, Fallback, Call Flow;
and the tier-1 expression language with its domain functions.

**Gates:**

- **B — audio network controller.** Proves a large fault enumeration lives in a
  Pack's lookup table, editable by anyone who reads a release note.
- **E — access switch.** Proves the monitor nobody builds: port flap, rising error
  counters and PoE faults catch more real AV failures than most device APIs, and
  unlike a device check they tell you *where* the fault is.

**Watch for:** the expression language's cost model. Guaranteed termination is not
bounded cost — a comprehension over a large SNMP walk is polynomial in input size,
which an AST-size cap does not bound. Budget against input size and cap collection
sizes explicitly.

---

## M9 — Metrics, discovery, Packs

Ships:

- Prometheus exposition in **three tiers that must not be conflated**, with the
  aggregate view served by exactly one elected process. Base units. Centralised
  escaping with a test. Deduplication before rendering. **And the last-run
  timestamp**, which is what lets a dashboard distinguish "the platform stopped
  checking" from "the check failed".
- Cardinality enforced in the editor, not in documentation (D26, I11).
- Grafana: compose profile, provisioned dashboards, a read-only datasource over
  **rollup views only** — never table access, or the read-only role routes around
  the permission model straight to run captures and credential rows — and the
  deep-link contract back into the run capture that explains a dip.
- Discovery: SNMP walk of switch ARP, bridge and neighbour tables; bounded CIDR
  sweep; reverse DNS. Review queue with diff-since-last-scan; "missing" as an
  event, never a delete; reachability triage on accept.
- Packs: signed versioned bundles, install review diff, egress scoping at install,
  fork-and-merge, fixtures wired as CI regression tests (D27), and the Pack index
  browser with unverified Packs marked loudly.

---

## M10 — Sandbox, hardening, seed Packs, 1.0

Ships: the tier-2 sandbox (last, per D10 — and note which nodes people asked for
while it was absent, because that list is the catalogue's gap list); the seven
seed Packs from §14, each also a documentation page and a CI test; agent
self-upgrade with automatic rollback; boot-time reconciliation after a restore;
backup and key-management documentation stating both halves of the master-key rule
loudly; and the security documentation that says plainly what the sandbox and the
sealed-frame model do **not** promise.

**Exit gate:** all seven seed Packs authored entirely through the UI. If they can
be, the primitive set is right.

---

## Explicitly deferred

Not forgotten — decided against for v1, with the reason.

| Deferred | Why | Revisit |
|---|---|---|
| Multicast (D19) | Removes the entire container-networking problem class. The venue routes unicast between VLANs. | As a per-agent capability flag (D20) — the mechanism ships in M4 regardless, since version skew needs it. |
| Pooled assignment for location-independent monitors (§19.3) | Genuinely useful, but complicates assignment, suppression and metric attribution. | After a real fleet exists. |
| Agent-to-agent reachability testing (§19.4) | Cheap once a fleet exists, and a real signal no device check provides. | Post-1.0. |
| Per-agent local notification when Core is unreachable (§19.2) | Closes a real gap at the cost of a second alerting path that can double-notify. | Offer it, default off, deduplicated against the scheduled-slot fence. |
| Protocol-doc paste affordance (§15) | Enormous first-run leverage, but only once the basics work. | After M6. |
| Custom Grafana datasource plugin (D25) | A separate release artifact with a signing process, to expose data already exposed twice. | Never. |

## Still open

Carried from spec §19. These need a decision, not a default.

1. **The name.** "Beacon" is a placeholder and heavily used in this space. It is
   currently confined to the module path, `cmd/` binary names and the docs, so a
   rename stays a find-and-replace. That stops being true once a Pack format
   version and a public API ship — so decide before M9.
2. **Minimum monitor interval.** A one-second floor is a footgun that will knock
   over someone's receiver. *Suggested:* a five-second floor, with anything below
   it an admin-only setting. Needs confirming before M3.
3. **Whether Grafana or the built-in UI is the venue tech's home screen.** If
   Grafana wins in practice, some of M6's dashboard work is wasted effort. Worth
   deciding early — the builder is not in question, only the dashboards around it.
4. **A CLA**, if relicensing should remain possible. Decide before the first
   outside contribution, not after.
