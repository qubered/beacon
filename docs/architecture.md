# Architecture

Two tiers: a **Core** control plane, and one or more **Agents** that do the work.
Agents dial out; Core never dials in. Core runs an agent too, over loopback, with
no special treatment — that is what forces exactly one execution implementation to
exist (D13).

Terminology is load-bearing. A **Node** is a box on the flow canvas. A deployed
collector is an **Agent**. Never use "node" for the latter.

## Repository map

```
cmd/
  beacon/            Core: UI, API, alerting, ingest, storage, registry — plus a local agent
  beacon-agent/      A deployed executor
  beaconctl/         Operator CLI: migrate, enrol, pack lint/sign, fixture replay

internal/
  buildinfo/         Build identity, declared on the link
  config/            Configuration and the §6.2 bounds as enforced ceilings
  obs/               Logging, self-metrics, tracing
  site/              Site scoping (D30) — an explicit parameter, never a context value
  secrets/           Envelope encryption and sealed-frame plumbing (I4)
  proto/             Binary Core<->Agent message format and version negotiation

  flow/              The authored artifact
    types/           Frame types and edit-time connection validation (D4, D5, I1)
    graph/           DAG model, immutable versions, content addressing (I3, D28)
    validate/        Publish-time gates (§15)
    diff/            Semantic version diff
    expr/            Tier-1 expression language and domain functions

  engine/            The executor. One implementation, used by both tiers (D13, D15)
    frame/           The typed value on a wire, and its seal
    runtime/         Firing, branch-join select, error ports, deadlines, budgets (I2)
    capture/         Per-node capture and masking by value scan
    sandbox/         Tier-2 isolate, off the main loop, hard-killed (arrives last, D10)

  nodes/             The catalogue. Registration here IS the capability declaration
    registry/        Descriptors, config schema versions, capability derivation (§7.5)
    framing/         Read-until strategies — the framing engine
    transport/       Everything that opens a socket. All of it emits bytes (I1)
    byteops/         Build, encode, checksum, HMAC, slice, bit field
    parse/           Parse, regex, JSONPath, XPath, table select, coerce
    transform/       Scale, lookup, unit convert, delta/rate, aggregate, expression
    control/         If, switch, foreach, collect, loop, delay, fallback, call flow
    emit/            Assert, threshold, status, metric, event, state, candidate

  agent/             When and how
    scheduler/       Deterministic jitter, non-compounding lateness, snap-forward (I10)
    executor/        Run context, var precedence, whole-flow retries
    sessions/        Connection supervisor and the staleness rule (§9)
    spool/           Durable outbound spool; sheds captures before results (I6)
    link/            Dials Core. Never listens (D11)
    egress/          Locally authoritative policy. Core cannot widen it (I7, D17)
    ratelimit/       Per-device concurrency and token bucket (principle 9)
    secretcache/     Assigned-device credentials, in memory only by default
    selfmon/         Agent-local metrics (§12 tier 1)

  core/              What to monitor
    api/             REST and streaming; site-scoped, role-gated, audit-logged
    auth/            Users, sessions, tokens, roles, audit log (§16)
    alerting/        State machine, flapping, suppression, incidents, delivery (§11)
    assign/          Monitor→agent binding and capability gating (I5, D16)
    ingest/          Batched results, dedupe fence, backfill replay (§7.3)
    fleet/           Agent enrolment, approval, certificates, revocation, skew
    link/            Accepts agent connections; hosts the test-run path
    discovery/       Candidate queue, reachability triage, Pack profile matching (§13)
    packs/           Install, export, verify, fork and merge (§14)
    metrics/         Prometheus exposition, three tiers, cardinality enforcement (§12)
    store/           Postgres: repositories, migrations, partitions, rollups (D22)

pkg/
  pack/              The Pack bundle format — exported so third parties can author

web/                 React + TypeScript. The status wall, the inventory, the builder
migrations/          Numbered SQL, site-scoped from 0001
packs/               The seven seed Packs (§14) and their fixtures
deploy/              Compose profiles, Dockerfiles, Grafana provisioning
test/
  devsim/            Fake devices, so CI can falsify the design without gear
  acceptance/        The five §18 scenarios, as gates
  fixtures/          Recorded device responses
tools/lint/          Repository rules that CI enforces
```

## The boundaries that matter

**`engine` does not know what a node does.** It knows how to satisfy inputs, fire,
propagate errors, enforce deadlines and capture. Nodes register into
`nodes/registry` and are opaque to it. This is what keeps a single executor honest
as the catalogue grows.

**`nodes` do not know where they are running.** A transport node receives a
context carrying the deadline and the egress policy and uses them; it does not
know whether it is inside Core's local agent or a box in a rack.

**`agent` owns *when*; `core` owns *what*.** The agent schedules from local state
so monitoring survives the link dropping (D14). Core holds configuration and the
alert state machine, which stays central because dependency suppression is
inherently cross-agent — the switch may be monitored by one agent and the
projector it feeds by another.

**`core/store` is the only package that speaks SQL.** Everything else takes a
repository interface. This is what makes the eventual time-series extension a
drop-in rather than a migration (D22).

**Sealing crosses all of them.** `Frame.Sealed` is set in `secrets`, carried
through `engine`, honoured by `capture`, refused by `expr` and `sandbox`, and
stripped from Pack exports. A node that constructs a fresh `Frame` from sealed
input rather than calling `Derive` silently launders a secret into a hex dump.
That is the failure mode invariant I4 exists to prevent, and it is why `Derive`
exists at all.

## The type mirror

`internal/flow/types` is the authority on frame types and connection validation.
The editor needs the same rules to refuse an edge before the user lets go of the
mouse, so the TypeScript equivalent in `web/src` is **generated** from the Go
source, and CI fails on drift. Two hand-maintained copies of a validation table
diverge, and they diverge in the direction of the editor permitting something the
runtime rejects.

The one thing not mirrored is the regex engine. `Regex Extract` must use a
non-backtracking engine, and the editor's live match preview must agree with the
runtime *exactly*, on precisely the patterns where a backtracking engine would
differ. So the preview calls the API rather than running a second engine in the
browser.
