# Beacon — Implementation Specification

A protocol-agnostic monitoring platform for AV and network infrastructure.

**Working name:** Beacon (placeholder — rename before repo init)
**Status:** specification. Nothing built.

---

## 0. How to use this document

This is the complete design brief. It describes **what to build and why**, not what libraries to use
or in what order. Where a decision has already been made, it is recorded with its reasoning in §17 —
those are settled, and reversing one should be a deliberate act, not an accident of implementation.

Two things to internalise before writing code:

1. **§3 (Invariants)** are non-negotiable properties. If an implementation choice violates one, the
   choice is wrong, not the invariant.
2. **§18 (Acceptance scenarios)** are the real test of the design. They are five device protocols that
   must be buildable **entirely through the UI** with no code changes. If one of them can't be, the
   node catalogue has a gap and needs a *node* — never a vendor-specific adapter.

Terminology is load-bearing. §4 defines it. In particular: a **Node** is a box on the flow canvas; a
deployed collector is an **Agent**. Never use "node" for the latter.

---

## 1. What we are building

Beacon is a monitoring platform that ships **network primitives, not device integrations.**

It has no idea what a Shure wireless receiver is. It knows how to open a TCP socket, send bytes the
user composed, split the response stream on a delimiter, pull a number out with a regex, subtract 128
from it, and raise an alert when it drops below a threshold — all defined by an operator in a browser,
saved as a reusable **Flow**, and shareable as a **Pack**.

If Beacon "supports Crestron", it is because somebody built a Crestron Pack in the UI. Not because we
wrote a Crestron adapter.

### The problem it solves

Every existing monitoring tool draws the extensibility line in the wrong place. Uptime Kuma wants a JS
monitor type and a redeploy. PRTG wants a custom EXE. Checkmk and Nagios want a Python plugin. Zabbix
is the closest — its item preprocessing chain is genuinely the right idea — but its primitives stop at
HTTP/SNMP/agent and the UX is a two-decade-old form maze.

The gap is a people problem, not a technology one. **The person who knows the gear is not the person
who can write a monitoring plugin.** An AV tech has the vendor's command-strings PDF open on a second
monitor and knows exactly what `< GET 1 BATT_RUN_TIME >` should return. They cannot ship a module to a
monitoring server. They can drag five boxes onto a canvas.

The bet: the AV protocol long tail is mostly the same handful of shapes — send-bytes/read-bytes,
request/response, walk-a-tree, subscribe-to-a-stream — wrapped in an infinite variety of *framing and
encoding*. Make framing and encoding into nodes, and the long tail becomes user-authorable.

### Non-goals

- **Not multi-tenant.** A `site_id` is carried throughout so a future split is not a rewrite, but no
  tenancy logic ships.
- **Not a control system.** Write-capable nodes exist because some devices must be told to start
  emitting telemetry. Every one is admin-gated, visually flagged and audit-logged. This does not grow
  into show control, and that should be stated in the README.
- **Not a NOC replacement.** It exports to Prometheus/Grafana and can forward to an existing alert
  manager rather than pretending to be either.
- **Not a scripting sandbox.** A JS transform node exists for the small percentage the node catalogue
  cannot express. If people are writing long transforms, the catalogue has a gap and *that* is the bug.

---

## 2. Design principles

1. **No vendor names in the codebase.** If a vendor string appears outside a Pack fixture or a test,
   the abstraction has failed. Treat this as a review rule.
2. **Primitives are honest.** A TCP node opens a TCP socket. It does not helpfully trim whitespace,
   assume UTF-8, or normalise line endings unless told to. AV protocols are byte protocols; surprises
   here cost hours of someone's evening.
3. **Bytes first, strings second.** Every transport primitive emits bytes; decoding is an explicit
   node. This single decision is what lets binary protocols be authored in the same builder as ASCII
   ones.
4. **Typed ports, validated at edit time.** You cannot wire bytes into a numeric threshold. The editor
   refuses and explains. Debugging a live flow against expensive gear at 6pm is not when to discover a
   type error.
5. **Every run is inspectable.** Per-node input and output captured on every execution. "Why did this
   go red?" must be answerable by clicking the red node and reading the actual bytes that came back.
6. **Bounded by construction.** Hard wall-clock deadline, node-execution budget, memory cap. Loops are
   explicit nodes with mandatory iteration limits. A user cannot author an infinite loop.
7. **The platform monitors itself.** Scheduler lag, missed runs, agent health, spool depth, sandbox
   terminations, metric cardinality. The first question in an outage is "is it still checking?"
8. **Monitoring must not stop because storage is unhappy.** Execution and persistence are decoupled.
   A storage problem degrades the record, never the checking.
9. **Gear is fragile — rate-limit outward.** A 100 ms poll will knock over a wireless receiver.
   Per-target concurrency caps and minimum intervals are enforced by the engine, not left to the
   author's judgement.

---

## 3. Invariants

Properties that must hold. Violating one is a bug regardless of how convenient it was.

| # | Invariant |
|---|---|
| I1 | A transport node emits `bytes`. Decoding is always a separate, explicit node. |
| I2 | Every flow run terminates. Wall-clock deadline, node budget and loop caps are enforced, and the deadline reaches the socket — not merely the gaps between nodes. |
| I3 | A published flow version is immutable. Editing forks a draft. |
| I4 | A secret is never readable by a user, never written to a run capture, never exported in a Pack, and never reachable from an expression or the sandbox. |
| I5 | A device is assigned to exactly one agent. Two executors must never hold sockets to the same device. |
| I6 | An agent keeps monitoring while disconnected from Core, and backfills on reconnect. |
| I7 | An agent's egress policy is authoritative on the agent. Core cannot silently widen it. |
| I8 | Backfilled state changes older than a configured age update history but do not send notifications. |
| I9 | An unreachable agent marks its devices `unknown` and suppressed — never `down`. |
| I10 | Scheduling lateness never compounds: the next run time advances from the previous *scheduled* time, never from "now". |
| I11 | Metric label values come from a declared, bounded set. Free-text labels are rejected at authoring time. |
| I12 | Maintenance windows and silences suppress *notification*, never *collection*. |

---

## 4. Domain model and glossary

```
Site
 └── Agent                     a deployed executor, typically one per VLAN
      └── Device               a thing with an address, and a place in the dependency graph
           ├── Credential ref  a handle to a secret, never a value
           └── Connection      a configured transport instance
                 ├── poll      opened per run, or pooled
                 └── session   held open, supervised, emits frames asynchronously

Flow      a versioned DAG of nodes — the reusable logic
Monitor   Flow × Device × Schedule × AlertPolicy — the thing that has state and history
Pack      Flows + presets + fixtures + dashboards + alert rules, exportable and signed
```

| Term | Meaning |
|---|---|
| **Core** | The control plane: config, alerting, storage, UI, agent registry. |
| **Agent** | A deployed process that schedules and executes its assigned monitors, holds device sockets, and ships results to Core. **Never called a node.** |
| **Node** | A box on the flow canvas. |
| **Primitive** | A built-in node performing real I/O or byte manipulation. Ships with the platform. |
| **Flow** | A user-authored DAG. Versioned; immutable once published. |
| **Monitor** | A flow bound to a device with a schedule. Has status, history, uptime, alerts. |
| **Run** | One execution of a monitor. Produces a status, zero or more metrics, zero or more events. |
| **Frame** | The typed value travelling on a wire between two nodes. |
| **Session** | A long-lived connection owned by an agent's connection supervisor. |
| **Assignment** | The binding of a monitor to the agent that runs it. |
| **Pack** | An exportable bundle. How vendor support exists without vendor code. |

**The critical separation: a Flow is logic, a Monitor is an instance.** Author
*Wireless Receiver — Channel Health* once, bind it to fourteen receivers. Fix the regex once, publish,
and all fourteen pick it up.

---

## 5. System architecture

Two tiers: a **Core** control plane, and one or more **Agents** that do the work.

```
┌─ CORE ──────────────────────────────────────────────────────┐
│  web        UI, API, auth, the flow editor                   │
│  alerting   state machine, incidents, suppression, delivery  │
│  ingest     accepts batched results from agents              │
│  database   config, flow versions, results, time series      │
│  agent      ← Core runs an agent too                         │
└──────────────────────────────────────────────────────────────┘
        ▲  agents dial out; Core never dials in
        │
┌───────┴───────┐ ┌───────────────┐ ┌───────────────┐
│ agent: av     │ │ agent: lx     │ │ agent: corp   │
│  scheduler    │ │               │ │               │
│  executor     │ │  (identical)  │ │  (identical)  │
│  sessions     │ │               │ │               │
│  icmp probe   │ │               │ │               │
│  local spool  │ │               │ │               │
└───────────────┘ └───────────────┘ └───────────────┘
```

**Core owns:** identity and agent enrolment, device and flow configuration, assignment, the alert
state machine, incidents, dependency suppression, long-term storage, metrics export, and the UI.

**An agent owns:** its assignment set, its own scheduling tick, flow execution, session sockets, ICMP,
its local egress policy, and a durable outbound spool.

The alert state machine stays central because dependency suppression is inherently cross-agent — the
switch might be monitored by one agent and the projector it feeds by another.

### Core runs an agent

The default deployment includes a local agent that monitors whatever Core's own network segment can
reach. **This is a design constraint, not a convenience:** it forces exactly one execution
implementation to exist. If Core executed flows one way and agents another, the two would drift within
a month. Core's local agent gets no special treatment — same enrolment, same capability declaration,
same link, just over loopback.

### Why the web tier never holds a device socket

Web runtimes restart, hot-reload and rescale arbitrarily. A held socket to a device that accepts
exactly one control connection cannot survive that lifecycle.

### Why agents schedule their own work

If Core pushed each due run, monitoring would stop the instant the link dropped — and a broken network
is precisely when the record matters most. Agents tick from local state and spool results. **Core owns
*what* to monitor; the agent owns *when* and *how*.**

---

## 6. The flow model

This is the product. Everything else is plumbing around it.

### 6.1 Frames and types

Every wire carries a **Frame**: a typed value plus metadata (originating node, timing, whether the
captured value was truncated).

| Type | Meaning |
|---|---|
| `bytes` | Raw octets. Everything I/O produces this. |
| `string` | Text — only after an explicit decode. |
| `number` | Floating point. |
| `int` | Arbitrary-precision integer, for 64-bit counters and OIDs that overflow a float. |
| `bool` | |
| `json` | Parsed structure. |
| `record` | Named fields. Output of regex extract, parse, table select. |
| `list<T>` | Ordered collection. Fan-out source. |
| `duration` | Distinct from `number` so unit mistakes are caught at edit time. |
| `timestamp` | Likewise. |
| `status` | `up` / `degraded` / `down` / `unknown`. |
| `error` | Only on error ports. |
| `any` | Escape hatch. Editor warns; runtime does not coerce. |
| `void` | Sequencing-only edges. |

**Connection validation.** The editor permits an exact match, `any` on either side (with a warning),
or a small set of safe widenings. Everything else is refused **with an inline reason and a suggested
fix node** — "insert Decode (utf-8) to connect bytes → string". That suggestion is what makes typed
ports feel like help rather than obstruction; without it they are merely an obstacle.

**Fan-out** (one output to many inputs) is allowed; the value is shared and treated as immutable.
**Fan-in** (many outputs to one input) is refused except on ports explicitly marked variadic —
ambiguous merge order is a debugging nightmare.

**Branch joins are a third case with their own rule.** Wiring both outputs of an `If` into one
downstream input is the single most common shape in real flows. Such a port is a *select*: the first
branch to settle supplies the frame, and readiness means *all reachable upstream branches have
settled*, not *all connected inputs have a frame*. Without this rule an `If` join deadlocks forever,
because the untaken branch never produces anything.

### 6.2 Execution semantics

The graph is a DAG, with one exception: the back-edge into a `Loop` node, which requires a mandatory
iteration cap. Nodes fire when their inputs are satisfied; independent branches run concurrently up to
a per-flow limit. A run that never reaches an `Emit Status` node produces `unknown` plus an execution
warning.

**Run context.** Every node can read (never write) a shared context containing the device (id, name,
host, tags, vars), the monitor (id, name, interval, vars), run metadata (id, scheduled slot, attempt
number, deadline), the previous run's stored values, persisted key/value state, and a `secret()`
accessor. Device vars and monitor vars are how one flow serves fourteen devices with different channel
counts; monitor vars override device vars override flow defaults.

**Inline expressions.** Any string field accepts `{{ expression }}` interpolation evaluated against
the run context. Escaping is per-field-type — a bytes template hex-escapes, a URL field URL-encodes, a
raw field does neither. This closes the injection class where a device name containing a quote breaks
a JSON body.

**Named bindings.** A node can publish its output under a name, readable from any downstream
expression in the same flow. This is what keeps multi-turn protocol flows readable — threading every
intermediate value through an explicit wire turns a six-step conversation into spaghetti. Names are
flow-scoped and assigned once; reading an unassigned name is a publish-time error, not a runtime
`undefined`.

**Errors.** Every node has an implicit error output port. Unconnected, an error propagates and fails
the run with full context captured. Connected, the error frame flows down that branch and the run
continues — which is how "if the API is unreachable, fall back to ping" is expressed.

**Error classes matter more than they look.** Distinguish at minimum: `timeout`, `connect_refused`,
`dns`, `tls`, `auth`, `protocol`, `assertion`, `sandbox_timeout`, `sandbox_memory`,
`agent_unreachable`, `internal`.

The important one is `protocol` — it means *the device answered and we misread it*, which is a flow
bug or a firmware change, not a gear fault. After a firmware update, twelve monitors going `protocol`
at once is a completely different message from twelve going `timeout`, and they should route to
different people.

**Retries** are a monitor property, not a node property, and they re-run the whole flow. Retries
consume the run's wall-clock budget, so worst-case run duration is
`(retries + 1) × timeout + retries × retry_interval`. **Publishing must be blocked when worst-case run
duration exceeds the monitor's interval** — otherwise runs overlap and the schedule falls apart.

**Bounds.** Suggested defaults, all configurable, all enforced:

| Bound | Default | Ceiling |
|---|---|---|
| Run wall clock | 30 s | 300 s |
| Node executions per run | 500 | 5 000 |
| Loop iterations | — | 200 (and they consume the node budget) |
| Concurrent branches per run | 8 | 32 |
| Frame size | 4 MB | 32 MB |
| Captured frame size | 64 KB | truncate and flag |
| Sandbox memory | 16 MB | 128 MB |
| Sandbox CPU | 250 ms polled | hard-killed at the thread |

**The deadline must reach the socket.** Not every I/O API accepts a cancellation signal; those that
don't need an explicit per-transport abort adapter that destroys the socket or closes the client.
A "timeout" that only checks the clock between nodes is not a timeout, and building it that way is the
default outcome if this isn't stated.

### 6.3 Connection Scope

A large share of AV protocols are **multi-turn on a single connection**. A projector sends a greeting
containing a nonce, expects a hash of that nonce plus a password, and only then answers. A wireless
receiver wants a metering rate set, then a full state request, then emits many frames. A control
processor sends a banner that must be discarded before anything useful happens.

A stateless request node cannot express any of that.

**`Connection Scope` is a container node that owns one connection for the lifetime of its body.**
Inside it, `Send` and `Expect` nodes operate on the ambient connection.

```
┌─ Connection Scope: tcp {{device.host}}:4352 ───────────────┐
│                                                             │
│  [Expect: until "\r"] ──▶ [Regex: greeting, capture nonce] │
│                                    │                        │
│                             [If auth required]              │
│                                    │                        │
│                        [Checksum: md5(nonce + secret)]      │
│                                    │                        │
│  [Send: "{{hash}}{{command}}"] ◀───┘                        │
│         │                                                   │
│  [Expect: until "\r"] ──▶ (out)                             │
└─────────────────────────────────────────────────────────────┘
```

Scope configuration: transport, host, port, TLS options, connect and idle timeouts, default framing
inherited by inner `Expect` nodes, and a **mode**:

- **transient** — open, run body, close. The default.
- **pooled** — kept in a per-target pool with a max idle time. Saves handshake cost on chatty devices.
- **session** — handed to the connection supervisor; the body becomes the *frame handler*, invoked on
  every inbound frame rather than on a schedule.

That last mode is the elegant part: **the same visual body serves both polling and subscription.**
Authoring a push-based monitor is not a different mental model, just a different binding.

### 6.4 Node catalogue

Presented in three difficulty tiers, filtered by default in the palette. Most users never leave tiers
1–2, and showing someone CRC configuration on day one is how you lose them.

#### Transport primitives

| Node | Notes |
|---|---|
| **TCP Request** | Host, port, optional TLS, payload, read-until strategy, timeouts, keepalive. |
| **Send** / **Expect** | Inside a Connection Scope only; operate on the ambient connection. |
| **UDP** | Unicast. Payload, expect-replies by count or collection window. |
| **HTTP Request** | Method, URL, headers, body; auth as none/basic/digest/bearer/API-key/mTLS/OAuth2-client-credentials; TLS options including CA override, client cert, and **certificate fingerprint pinning**; redirect policy; timeouts. |
| **GraphQL** | Sugar over HTTP. Must surface the `errors` array separately — GraphQL returns HTTP 200 with errors, so status-code assertions are useless. |
| **WebSocket** | Connect, send, collect frames. Also available in session mode. |
| **SNMP** | v1/v2c/v3. Get, get-next, get-bulk, walk, table. Numeric OIDs. Type-aware value decoding. |
| **ICMP Ping** | Count, interval, size. Returns loss, min/avg/max, jitter. |
| **TCP Connect** | Handshake only, with timing. |
| **DNS Query** | A/AAAA/PTR/SRV/TXT/CNAME, resolver override. |
| **CIDR Sweep** | Bounded ICMP or TCP-connect sweep of a range. Also a port scanner — see §16. |
| **SSH Exec** | Command execution with connection reuse; algorithm overrides for legacy gear. |
| **MQTT** | Publish/collect, or subscribe in session mode. |
| **Modbus TCP** | Read coils and registers. |
| **TLS Inspect** | Expiry, issuer, subject, SANs, fingerprint. |
| **HTTP Listener** *(session)* | Inbound webhook source. |
| **SNMP Trap Listener** *(session)* | Inbound trap source. |

#### Read-until strategies

Shared by `TCP Request` and `Expect`. **This is the framing engine, and it is what unlocks the long
tail.** Get it right and most vendor protocols become configuration.

- **delimiter** — byte string, include or exclude it
- **length prefix** — offset, width, endianness, whether the length counts the header, sanity bounds
- **fixed** — N bytes
- **regex** — match against a decoded view; the match ends the read
- **start/end markers** — with optional escape byte
- **quiet period** — no data for N ms; the universal fallback for prompt-less devices
- **until close** — read to FIN
- **message count** — read N framed messages

Plus per-read options: **strip IAC sequences** (mandatory for the very common port-23-but-not-really-
Telnet class of device), max bytes, and **discard-before** (skip a banner up to a marker or strategy).

> **Deliberate omission: there is no Telnet node.** Devices on port 23 from most AV control vendors
> are raw ASCII with no option negotiation and no prompt. A prompt-based Telnet client is structurally
> the wrong shape and produces hangs nobody can diagnose. Genuine Telnet — switches and PDUs with a
> login prompt — is a *preset* of TCP Request with IAC stripping and a regex read.

> **Deliberate omission: there is no shell/exec node.** It converts a monitoring server into a remote
> code execution surface for anyone with flow-author rights. If someone truly needs it, they go
> through SSH Exec or an HTTP endpoint, which is auditable.

#### Bytes and encoding

| Node | Purpose |
|---|---|
| **Build Bytes** | Structured composer: ASCII literals, hex literals, expression interpolation, typed fields (unsigned/signed 8/16/32, floats, endianness), repeats, and **placeholders for length and checksum** resolved after the body is assembled. This is what makes binary protocols authorable. |
| **Encode / Decode** | utf-8, ascii, latin-1, hex, base64, BCD, gzip, deflate. Explicit, both directions. |
| **Checksum** | CRC-8/16/32 with configurable polynomial, init, reflection and xor-out (every vendor picked a different CRC-16); XOR, sums, two's complement; MD5, SHA-1, SHA-256. |
| **HMAC** | Key from a secret handle. |
| **Split Frames** | Apply any read-until strategy to bytes already in hand → `list<bytes>`. Same strategy set, reused. |
| **Slice** | Offset, length, from-end. |
| **Bit Field** | Extract a bit range; named flags → record. Ubiquitous in status registers. |
| **Endian Swap** | |

#### Parse and extract

| Node | Purpose |
|---|---|
| **Parse** | JSON, XML, YAML, CSV/TSV, key=value, INI, HTTP headers, query string, SNMP varbinds. |
| **Regex Extract** | Named capture groups → record; multi-match → list. **Must use a non-backtracking engine** (users supply the patterns; see §16), and the same engine must power the editor's live match preview or the preview will disagree with the runtime on exactly the patterns that matter. |
| **JSONPath / Pointer** | Select from parsed JSON. |
| **XPath** | Select from XML. |
| **Table Select** | Filter, pick, rename columns on a list of records. Makes SNMP tables usable without expressions. |
| **Split Text** | Delimiter, limit, trim. |
| **Coerce** | String → number/int/bool/timestamp with an explicit format and an on-failure branch. Never implicit. |

#### Transform

| Node | Purpose |
|---|---|
| **Scale & Offset** | `(v × scale) + offset`, optional clamp and round. Exists as a node specifically so that converting a raw RF value to dBm requires zero expression syntax. |
| **Lookup Table** | Value → value map with a default and an optional "unmapped is an error" toggle. Handles large vendor enums as editable configuration. |
| **Unit Convert** | dBm/mW, °C/°F, byte scales, time scales. |
| **Delta / Rate** | Versus the previous run's stored value, with counter-wrap handling (32- and 64-bit) and a null first run. |
| **Aggregate** | min/max/avg/sum/count/percentiles/stddev over a list. |
| **Merge**, **Set / Pick / Omit** | Record surgery. |
| **Expression** | One expression → one value. The workhorse. |
| **Transform (sandboxed)** | The escape hatch. See §6.5. |

#### Control flow

| Node | Purpose |
|---|---|
| **If** | Predicate → true/false ports. |
| **Switch** | Ordered cases plus default. |
| **ForEach** | Iterate a list; body is a subgraph; concurrency limit; **continue-on-error option** (without it, one bad element hides every element after it). |
| **Collect** | Variadic fan-in → list. |
| **Loop** | Body plus back-edge; mandatory iteration cap; optional while-condition and bounded delay. The only legal cycle. |
| **Delay** | Bounded; counts against the run deadline. |
| **Fallback** | Primary and fallback inputs; emits whichever succeeds. Sugar over error ports for the common case. |
| **Call Flow** | Invoke another published flow as a subroutine with mapped inputs and outputs. **The most important reuse primitive** — "get one property" authored once, called twelve times. |
| **Note** | Canvas comment. Not executable, but make it first-class and searchable: this is where people will document their own protocol reverse-engineering, and that documentation is worth as much as the flow. |

#### Evaluate and emit

| Node | Purpose |
|---|---|
| **Assert** | Rows of `field, operator, value, severity, message template`. Operators cover equality, ordering, ranges, containment, prefix/suffix, regex match, set membership, existence, type and schema checks. **Covers roughly 80% of real checks with no expression at all** — this node is why most users never need the expression layer. |
| **Threshold** | Numeric, with warn and critical bands, a hysteresis band, and a hold-for duration. |
| **Emit Status** | Status plus reason template. Terminal. A flow must reach one. |
| **Emit Metric** | Name, type, value, unit, and labels drawn **only from a declared label schema** (§12). |
| **Emit Event** | Severity, message, structured fields. Goes to the event log, not the time series. |
| **Set State** | Write a keyed value. Target is explicit configuration: `run` (feeds the next run's delta/rate), `shared` (visible to other monitors at a chosen scope), or `subscription` (for session frame handlers). One node, three destinations, never ambiguous. |
| **Read State** | The inverse. Indexed lookup by key or prefix. This is what derived monitors run on. |
| **Emit Device Candidate** | Name, address, MAC, attributes, suggested Pack. Feeds the discovery review queue (§13). |

### 6.5 Expressions and the sandbox

Two tiers, deliberately, and most users touch neither.

**Tier 1 — a restricted expression language** for assertions, thresholds and interpolation. It must
be **non-Turing-complete**, so evaluation is guaranteed to terminate without needing a timeout to stop
an infinite loop. It needs the usual arithmetic, comparison, logical and membership operators, field
and index access, and list comprehension macros (all / exists / map / filter).

Two things to get right:

- **Register domain functions** so users don't reach for tier 2: hex parsing, BCD, CRC, raw-to-dBm
  conversion, tick-to-duration, byte extraction at offsets, integer decoding by width and endianness.
- **Guaranteed termination is not bounded cost.** Comprehensions over a large SNMP walk are polynomial
  in *input size*, which AST-size caps do not bound. Budget cost against input size, and cap
  collection sizes explicitly.

**Tier 2 — a genuinely isolated sandbox** for the transform node. Requirements, not implementation:

- Hard memory cap and an enforced CPU deadline, with the process/thread as the real backstop rather
  than a cooperative interrupt (cooperative interrupts do not preempt native operations, including
  regex engines).
- **Runs off the main event loop.** A synchronous sandbox blocking for its full deadline would stall
  every held socket, every heartbeat and every metrics scrape in that process.
- **Only serialised data crosses the boundary.** Never a live host object or function — that is the
  classic escape vector.
- **The serialisation format must handle `bytes` and big integers**, which naive JSON serialisation
  does not (one produces an object of numeric keys, the other throws). These are precisely the two
  types the transform node exists for.
- Frame size must be gated *below* the sandbox memory cap before the node runs.
- Session frame handlers get a much tighter deadline than polled flows.

**Stated honestly in the docs:** the sandbox is defence in depth against mistakes and compromised
accounts, not a hostile-multi-tenant boundary. Over-claiming it is how someone ends up exposing this
to the internet.

---

## 7. Agents

### 7.1 Thick, not thin

An agent receives a flow version and a schedule and executes the whole flow locally. It is **not** an
I/O proxy that Core drives node by node.

| | Thin (I/O proxy) | **Thick (chosen)** |
|---|---|---|
| Round trips per run | one per node | one per run, batched |
| A six-step protocol handshake | six WAN round trips | zero — it happens on-subnet |
| Latency measurement | measures Core→agent→device | measures agent→device |
| Session sockets | cannot be held sensibly | live next to the device |
| Link drops | monitoring stops | **monitoring continues and backfills** |
| Cost | — | larger artifact; version skew must be negotiated |

The link-loss property alone decides it, and the round-trip arithmetic makes thin unworkable for the
exact devices this platform exists to monitor.

**One exception:** *test-run-from-the-editor* is a separate synchronous request. Core asks a named
agent to execute a specific draft flow once, right now, streaming per-node results back. That path is
interactive and bypasses assignment and spooling entirely.

### 7.2 The link

**Agents dial Core. Always. Core never dials an agent.**

Not stylistic. An agent lives in a VLAN that may be firewalled inbound, on DHCP, behind NAT.
Outbound-only means one firewall rule per agent VLAN, zero listening ports on the agent, and a small
box that can be plugged into a rack and simply work.

A single persistent authenticated connection, mutually authenticated, carrying a **binary** message
format — captures are raw device bytes, and text-encoding them would inflate the largest thing on the
wire by a third.

Message types, at minimum:

| Direction | Message | Purpose |
|---|---|---|
| A→C | `hello` | agent identity, version, capability set, egress policy digest, clock |
| C→A | `assign` / `unassign` | monitor configuration, schedule, device, flow version reference |
| C→A | `flow_version` | the immutable graph, if not already cached |
| C→A | `secrets` | credentials scoped to this agent's assigned devices |
| A→C | `results` | batched run results, metrics, events — acknowledged |
| A→C | `heartbeat` | health, queue depth, spool depth, clock |
| C→A | `test_run` | interactive draft execution; streams per-node results back |
| C→A | `reload` / `drain` / `upgrade` | lifecycle |
| A→C | `log` | agent diagnostics, rate-limited |

Reconnection uses exponential backoff with jitter. **The agent does not stop monitoring while
disconnected.**

### 7.3 The spool and backfill

Results are written to a durable local spool bounded by both size and age. On overflow, **shed
captures before shedding results** — a capture is the large payload and the least load-bearing, while
status and metric rows are what uptime and history are computed from. Count both drop classes.

Delivery is at-least-once with acknowledgement. Each result carries its original scheduled slot, which
doubles as the execution fence, so replays deduplicate on insert rather than double-counting.

**Backfill must not page anyone.** Two rules; skipping either guarantees a 3am incident:

1. Core replays the alert state machine over backfilled results so **history and uptime are correct** —
   the incident record shows the device went down at 19:42, not at reconnect time.
2. Core **suppresses notifications** for state changes older than a configured age, and emits one
   summary instead: *"agent lx reconnected after 47 minutes; 3 incidents occurred while offline, 2
   have since recovered."* Nobody needs a page about a flap that resolved half an hour ago.

### 7.4 When an agent goes silent

**An unreachable agent must not mark its devices down.** They become `unknown`, and their alerts are
suppressed with the agent as an implicit parent in the dependency graph. What alerts is the agent —
**one alert, not sixty**.

This is not a detail. A tech who receives sixty pages because one uplink blipped will turn the system
off that night and never turn it back on.

### 7.5 Capabilities and versions

Thick agents mean an agent might not be able to run what Core sends. Handle it explicitly rather than
at runtime.

On connect an agent declares a **capability set**: build version, the node types it implements with
their config schema versions, whether write-capable nodes are locally enabled, whether multicast is
locally enabled, and its egress policy digest.

- Core **refuses to assign** a monitor whose flow needs a missing capability, and surfaces it in the
  UI — *"3 monitors cannot be assigned to agent lx: it does not implement Checksum"* — rather than
  failing at 6pm.
- Agents older than Core is normal and supported within a stated window. Agents newer than Core is
  refused.
- **Upgrades are agent-initiated.** Core advertises an available build; the agent fetches, verifies a
  signature, drains, and restarts within a configured window. Core pushing binaries into machines on
  sensitive VLANs is a worse trust posture for no operational gain.
- **Keep the previous build and roll back automatically** if the new one fails to reconnect within a
  timeout. An agent that upgrades itself into a brick, on a VLAN, in a venue, is a drive across town.

### 7.6 Deployment shape

An agent is a container on any host with an interface in the target segment, or a small single-board
computer in the rack. One image, one deployable, with the ICMP helper bundled inside it rather than
as a separate container.

**One agent per VLAN, one interface each.** Resist multi-homing: it reintroduces the
interface-selection problems that per-VLAN agents eliminate, and agents are cheap.

**Assignment:** every device has exactly one home agent, enforced by the schema. Monitors inherit
their device's agent. Bulk reassignment by tag or subnet in the UI.

**No automatic failover.** An agent going down means its devices go `unknown` and suppressed — an
honest state with a single clear alert. Automatic takeover introduces split-brain (two agents holding
sockets to a device that permits one connection) for a failure mode that is rare and immediately
visible. Manual reassignment is one click.

### 7.7 Clock discipline

Every result carries the agent's timestamp, and agents are machines someone plugged into a rack. This
makes clock skew a first-class problem rather than a footnote:

- Time sync is an install requirement, checked at enrolment and refused if wildly out.
- Each heartbeat carries the agent's clock; Core computes and exports per-agent skew and alerts past a
  threshold.
- Core rejects results with implausible timestamps and counts them.
- **Liveness is judged against Core's receipt time, never the agent's claimed timestamp**, so a skewed
  agent cannot fake being alive.

---

## 8. Scheduling and execution

The scheduler lives **in the agent**, against local state. Core holds the monitor's *configuration*;
the agent holds its *scheduling state*.

Because a single agent process exclusively owns its whole assignment set, this is a simplification
rather than an addition — no distributed claim protocol is needed. But the semantics below are
non-negotiable, because each one exists to fix a specific failure:

**Deterministic jitter.** Each monitor's phase offset is derived from a hash of its id modulo its
interval — deterministic, not random, so the schedule is reproducible, a monitor doesn't wander
between runs, and a restart lands on the same grid. Without it, four hundred monitors created at
deploy time with a 60-second interval all fire in the same second and hammer the access switch once a
minute, forever.

**Lateness never compounds.** The next run time advances from the previous *scheduled* value, never
from "now". A monitor that took 28 seconds to time out still lands on the original grid.

**Catch-up snaps; it does not replay.** After a ten-minute outage, adding one interval repeatedly
would queue 120 backdated runs whose results are worthless and which stampede the network on recovery.
Snap forward to the next grid slot and **record the gap** as a missed-runs counter. A recorded gap is
worth more than silently uncollected data.

**Reset on configuration change.** Enabling a long-disabled monitor, or changing its interval, must
reset its schedule and suppress the missed-run increment for that first tick — otherwise re-enabling
something that was off for a week adds six figures to a counter that alerts.

**Suspect monitors poll faster.** While in the suspect state (§11), the schedule advances by a shorter
interval. When something looks broken, look again sooner.

**Rate limiting is enforced by the engine, not the flow author.** Per-device maximum concurrent
connections (default 1 for session-capable devices) and a per-device minimum request interval as a
token bucket spanning *all* monitors bound to it. A monitor blocked on that bucket records a throttled
outcome, does not count as a failure, and increments a counter — because a non-zero throttle count
means someone has over-scheduled a device and needs to know.

**Execution and persistence are decoupled.** Results go to the spool; a writer drains it. If the
downstream is slow or unreachable, the spool absorbs it and sheds according to §7.3. Monitoring never
stops because storage is unhappy.

**Run captures:** full per-node input/output for the last N runs per monitor, plus **every run in a
suspect window** — from the first failure through the confirming transition. The diagnostic capture is
almost always the *first* failure, not the one that officially changed state, and on a busy monitor
the first failure would otherwise have already aged out. Captures are dropped at write time when
retention says so, never written and pruned later.

---

## 9. Sessions and push

Some devices push. Holding a socket open *is* the subscription; there is no subscribe command. The
session plane handles this, and it reconciles with polling through a single rule.

**Connection supervisor.** Per session connection: a `connecting → open → degraded → closed` state
machine, exponential backoff with jitter, a circuit breaker after repeated failures, an on-open flow
(some devices need a command re-sent after every reconnect to resume emitting), and an optional
keepalive poke for devices that close idle sockets.

On every inbound frame: timestamp it, run the frame-handler flow, upsert subscription state, update
last-frame time.

**The staleness rule — this is the whole reconciliation:**

```
healthy(source)  ⟺  now − last_frame_at < liveness_window
```

A push source that stops pushing becomes **indistinguishable** from a poll target that stops
answering. One rule gives dead-man's-switch semantics and makes alerting, dependency suppression,
uptime maths and metrics export uniform across both models instead of two parallel implementations.

**The trap, and it must be handled in the UI:** a device that is silent *by design* — only emitting on
change — will trip its liveness window constantly. Either it has its own heartbeat, or a keepalive is
configured, or it is not a push source and should be polled. **Force this choice at creation time.** A
silent-by-design source with a liveness window is a false-alarm generator and will destroy trust
faster than any bug.

**Derived monitors** do no I/O. They are scheduled like any other monitor, but their flow reads
subscription state by key and evaluates thresholds. Same scheduler, same result path, same alert
pipeline, same metrics. This is how a push-fed value gets an uptime percentage without special-casing
anything.

**Backpressure at ingest.** A device in a fault loop emits thousands of messages per second.
Rate-limit per connection, count drops, and alert on the drop rate. A broken projector must not fill
the database. Frame handlers also get a tighter execution budget and a per-connection execution rate
cap.

**Connections are themselves monitorable.** Export connection up/down, reconnect counts, last-frame
time, dropped frames. Operators ask "is Beacon connected?" before "is the device up?".

---

## 10. Data model

Entities and the relationships that matter. Field lists are indicative, not exhaustive.

**Identity and topology**
- `sites` — name, timezone (IANA name, never an offset).
- `agents` — name, identity/cert, version, capability set, egress policy, link state, last seen,
  clock skew, spool depth.
- `devices` — site, **agent (exactly one)**, name, host, tags, vars, per-device rate limits,
  discovery metadata, reachability state.
- `device_edges` — child, parent, kind. A **DAG, not a tree** — AV racks have redundant paths, and a
  single parent column cannot express them.
- `credentials` — encrypted at rest, scoped by grant to the devices they belong to, unique by name
  within a site, with last-used tracking.

**Flows**
- `flows` — name, description, category, current version.
- `flow_versions` — **immutable**, with a graph schema version, input/output schema, changelog,
  publisher. Content-addressable so agent caching is trivially correct.

**Monitors and results**
- `monitors` — device, flow version (pinned or tracking latest), name, vars, mode
  (poll / session / derived), interval, timeout, retries, alert policy, and the persisted alert state
  machine (state, state-since, consecutive counts, flap percentage, recent-result ring).
- `monitor_runs` — partitioned by time; carries **the flow version that produced it** (without it you
  cannot explain a historical run once the flow is edited), the scheduled slot, status, outcome,
  a transition flag, error class, message, and optional capture. Unique on (monitor, scheduled slot)
  as an execution fence.
- `monitor_state_periods` — state, from, to, whether in maintenance. **The source of truth for
  uptime** — see below.
- `monitor_last_values` — per-monitor keyed values backing delta/rate and previous-run access.
- `metric_series` — monitor, name, type, unit, labels, label hash. Unique per *(monitor, name, labels)*
  — not globally by label hash, or two monitors emitting the same label set collide.
- `metric_samples` — partitioned by time; duplicate-tolerant on insert with a counter, because
  user-authored flows *will* emit duplicates.
- `events` — partitioned by time.
- `kv_state` — scoped shared state, with expiry.
- `subscription_state` — per connection, keyed, with a numeric column denormalised for fast threshold
  evaluation, and a TTL so keys a device stops sending don't accumulate forever.

**Alerting**
- `alert_policies`, `incidents`, `notifications`, `notification_channels`, `silences`,
  `maintenance_windows`.

**Platform**
- `packs`, `users`, `sessions`, `api_tokens`, `audit_log`.

### Defining uptime

Uptime is **time-weighted from state periods, not counted from runs.**

Run-count uptime is broken here by construction: change a monitor's interval and yesterday's failures
re-weight; missed runs produce no row at all because the scheduler snaps past them; throttled and
unknown runs have no obvious treatment. Time-weighting sidesteps all of it.

```
uptime = Σ(duration in 'up') / Σ(total duration − excluded)
```

**Report both figures — raw, and excluding maintenance.** Operators will ask for both, and a single
number that quietly excludes maintenance is exactly what makes uptime reporting untrustworthy.

### Storage and retention

Time-partitioned tables with a rollup path, structured so a time-series extension is a later drop-in
rather than a migration. **Pre-create partitions ahead of time with a catch-all default** — if
rollover fails or the process is down at a boundary, every insert fails and the spool sheds
everything. Expire by dropping partitions, not deleting rows.

Note that partition boundaries and reporting "days" are different things: one is UTC, the other is the
site's timezone.

Suggested retention shape: full-resolution metrics for days, minute rollups for months, hour rollups
for years; runs without captures for months; captures per the rules in §8; events for months; uptime
periods indefinitely (they are tiny).

---

## 11. Alerting

Everything here exists to prevent one outcome: **a tech who gets a false page at 11pm turns the system
off.** Ship conservative and tighten later, never the reverse.

### State machine

An explicit per-monitor state machine, persisted, not a bare failure counter — because `suspect` is a
state an operator wants to *see* before being paged.

```
      n failures            dwell elapsed          k successes
 up ──────────────▶ suspect ─────────────▶ down ──────────────▶ recovering ──▶ up
  ▲                    │
  └────────────────────┘ 1 success
```

Asymmetric by design: slow to alarm, slow to clear. Suggested defaults: three consecutive failures to
go down, two consecutive successes to come back.

**Pair the count with a minimum dwell time.** At a five-second interval, three failures is fifteen
seconds — far too twitchy for a human notification. Require **both** *n consecutive failures* **and**
*at least T seconds in the failing state*. Two independent conditions; at short intervals the count
alone is meaningless.

**Put transitions in the data model.** A boolean on the run row, true only when status *officially*
changes after retries exhaust. Making "is this a transition?" a stored fact rather than alerting-layer
logic makes alert history queryable and gives incidents a natural anchor.

### Severity by error class

The alert policy maps error class to severity and route. The valuable distinctions:

| Class | Meaning | Route |
|---|---|---|
| `timeout`, `connect_refused`, `dns` | Device unreachable | AV on-call |
| `tls`, `auth` | Credential or certificate problem | Admin — often predictable in advance |
| `protocol` | **Device answered, we misread it** | **Flow author** — this is a flow or firmware issue, not a gear fault |
| `assertion` | Device answered, value out of range | AV on-call. The real signal. |
| `agent_unreachable` | Agent stopped reporting | Admin. One alert, not sixty. |
| sandbox / internal | Beacon's fault | Admin |

### Flapping

Debounce handles a blip. Flapping is oscillation over an hour where every transition individually
passes debounce and produces forty notifications.

Compute a weighted state-change percentage over a **time-based window** (not a fixed sample count —
twenty-one samples at a five-second interval covers under two minutes, while the thing being detected
plays out over an hour), weighting recent transitions more heavily than old ones. Use **two thresholds
with hysteresis**: flapping starts above the high threshold and only stops below the low one,
otherwise the flap detector itself flaps.

While flapping, suppress state-change notifications and send exactly one "X is flapping" alert, plus
one on exit.

Keep the recent-result history in a fixed-size ring on the monitor row rather than querying the run
table — at a thousand monitors, recomputing from a partitioned table every cycle is a thousand queries
for a number that fits in a few dozen bytes.

**Surface the flap percentage in the UI as a first-class number.** It is an excellent diagnostic on
its own: a monitor sitting at 40% almost always indicts a marginal patch lead, a saturated uplink, or
a PoE budget problem — and that is actionable well before anything goes properly down.

### Maintenance, silences, show mode

Named recurring windows with times, weekdays, days of month, months, and an **IANA timezone name** —
never a fixed offset, or a 2am window drifts an hour at DST.

- **Suppress notification, not collection.** Keep checking and storing throughout; mute delivery only.
  Otherwise uptime becomes fiction and you cannot answer "did it come back before doors?".
- **Ad-hoc silences require a mandatory expiry**, a recorded author and a reason. Permanent silences
  are how monitoring quietly dies. Enforce a maximum duration, show active silences prominently, and
  digest them weekly.
- **Show mode** — a venue-specific concept worth building: a named window bound to a device group that
  mutes non-critical alerts, raises polling on the gear actually in use, and focuses the dashboard on
  that group. This is the feature that makes the tool feel purpose-built rather than generic with a
  skin.

### Dependency suppression

Model dependencies as a **graph** and suppress a device's alerts while any ancestor is down.
Operators already think *projector → switch → UPS*, so the UI writes itself, and discovery can propose
the edges from switch topology data.

This raises a question that must be answered explicitly: **what does "a device is down" mean?** Only
monitors have status. Define device health as a persisted rollup — worst-of, over the monitors on that
device flagged as reachability checks — so exactly one monitor per device determines reachability, and
an out-of-range battery reading never suppresses an entire rack.

Three details decide whether this works at all:

1. **Grace period.** A child almost always detects an outage before the parent's next poll. Delay
   child alerts by roughly one parent interval before deciding they are independent — otherwise you
   send the child alert, *then* the parent alert, and suppress nothing.
2. **Suppressed is a state, not a discard.** Record that the child was down and that its alert was
   suppressed by parent X. "What else was affected?" is the first post-mortem question.
3. **Roll up on recovery.** One message — *"stage switch recovered; 14 dependents back, 2 still
   down"* — not sixty recoveries. The two stragglers are the entire signal.

**Agents are implicit parents** (§7.4).

### Delivery

**Build the state machine in-app; ship delivery out.** Debounce, flapping, suppression and windows are
the differentiated logic and belong in the data model and the UI. Per-provider notification
integrations are not: that looks like three integrations, becomes forty, and every one changes its API
forever.

Use a general-purpose notification fan-out service addressed by URL scheme, with a **plain webhook as
an always-available fallback** so the platform never hard-depends on a sidecar. Optionally forward to
an existing alert manager for sites that already run one — a small adapter, not an architecture.

Group alerts by site, parent device and severity so a rack going down is one message. Escalate simply:
if an incident is unacknowledged after N minutes, route to the next channel. No on-call rotation
scheduling — that is a different product, and there is a webhook for it.

Sensible starting timings, borrowed from established practice: group wait ~30s, group interval ~5m,
repeat interval ~4h.

---

## 12. Metrics and Grafana

Prometheus-compatible exposition, scraped by Grafana. The database remains the system of record;
the metrics endpoint is an integration surface.

### Three tiers, not one

Conflating these produces silently wrong dashboards:

1. **Agent-local** — per-agent execution counts, spool depth, link state, local health. Scrape agents
   directly where the network allows; otherwise they reach Core in heartbeats and Core re-exports them
   labelled by agent.
2. **Core process-local** — per-process values, scraped from every Core process.
3. **Core aggregate** — the database-derived view, served by exactly **one** elected process. With N
   processes each querying the database for all monitors, every sum and every rate is N× wrong.

### What to export

Per monitor: up/degraded/down as a numeric gauge, run duration, **last-run timestamp**, execution
counts by outcome, consecutive failures, flap percentage, and an info metric carrying human names.

Per connection: up/down, last-frame timestamp, reconnect count, dropped frames.

Per agent: up/down, last-seen timestamp, spool depth, clock skew, backfilled result count.

Platform self-health: scheduler lag, missed runs, sandbox terminations by reason, series count.

**Ship the last-run timestamp.** It is the one people forget, and it is what lets a dashboard express
*"the platform stopped checking"* as distinct from *"the check failed"*.

Use base units — seconds and bytes, never milliseconds or kilobytes — and keep units out of label
names.

### Cardinality is enforced in code, not documentation

Because users author their own checks, this cannot be a guideline.

- `Emit Metric` requires a **declared label schema** at authoring time. Label values must come from an
  enumeration, a device tag, or a bounded configuration field. **Free-text label values are rejected
  in the editor.**
- Never permit error messages, response bodies, timestamps or serial numbers as labels — those belong
  on the run row.
- Prefer stable identifiers as labels plus a separate info metric carrying human names, so renaming a
  device does not create a new series.
- Enforce a per-monitor series cap with a visible warning, and alert on the platform's own series
  growth rate.

### Exposition-format traps

Worth knowing before they cost an afternoon: escaping rules differ between help text and label values,
and a device name containing a quote can break **the entire scrape**, not one line — so escape
centrally, in one function, with a test. Duplicate metric-name-plus-label-set combinations are a hard
error that rejects the whole page, and with user-authored flows this *will* happen, so deduplicate
before rendering and emit a warning metric instead. Omit explicit timestamps and let the scraper stamp
receipt time.

### Grafana integration, in build order

1. **Scrape target**, with a ready-made compose profile that stands up the monitoring stack pre-wired.
   This alone satisfies "exports to Grafana".
2. **Provisioned dashboards** — site overview, device drill-down, monitor detail, platform self-health.
   **Packs carry their own dashboards**, so installing a Pack brings its board with it.
3. **A read-only database datasource** over defined rollup views for historical and reporting queries
   that a metrics store cannot answer. Grant on the views only — never table access, or the read-only
   role routes straight around the UI's own permission model and can read run captures and encrypted
   credential rows.

Also ship a **deep-link contract** — a monitor URL accepting a time range — so a Grafana panel can link
back into the run capture that explains a dip. Seeing the anomaly in Grafana and clicking through to
the actual bytes the device returned at that moment is what makes the pair better than either alone.

Do not build a custom datasource plugin. It is a separate release artifact with a signing process, to
expose data that is already exposed twice.

---

## 13. Discovery

Discovery is not a special subsystem. It is a flow that emits device candidates.

Discovery flows run on a schedule, per agent, scoped to that agent's segment and egress policy, and
emit into `Emit Device Candidate`. Sources, in descending order of value:

- **SNMP walk of the switches and router** — the ARP cache (every IP that has spoken recently, with
  its MAC), the bridge forwarding table (MAC to physical port), interface state and error counters,
  PoE status, and neighbour discovery.
- **Bounded CIDR sweep** — ICMP or TCP-connect across a named subnet, concurrency-capped and
  constrained by egress scope.
- **Reverse DNS and DHCP lease tables** — names for the addresses found.

This finds **everything with an IP**, not just the subset that advertises itself — and it returns the
physical switch port, which announcement protocols cannot, and which is exactly what the dependency
graph wants.

**Candidates land in a review queue** with accept / edit / ignore, and a diff since the last scan.
"Missing" is an event, never a delete.

**Reachability triage on accept.** A discovered candidate is not necessarily a monitorable one — a
device may be reachable from its own VLAN but not from where the agent sits. Probe on accept and
record `reachable`, `unreachable-from-here` (present in the switch ARP table, silent to us), or
`filtered` (refused rather than timed out, which usually means an ACL). Only the first can carry
monitors; the others are still worth listing, because "there is a device on port 14 we cannot see" is
itself useful.

**Pack device profiles** close the loop: a Pack declares matching rules (system object identifier,
MAC OUI, open ports, HTTP page title), and an accepted candidate that matches gets its monitors
proposed automatically. From the operator's point of view they plugged in a receiver and the platform
offered to monitor it — with no vendor code anywhere in the core.

The switch is the highest-value monitoring target and the least glamorous. Port flap, rising error
counters and PoE faults catch more real AV failures than most device APIs do, and unlike a device
check they tell you *where* the fault is.

---

## 14. Packs

A Pack is how vendor support exists without vendor code.

**Contents:** flows, pre-configured node presets for the palette, recorded device fixtures, suggested
monitor bindings, device-matching profiles, dashboards, and default alert rules. Plus metadata: an
author, a signature, a minimum platform version, a declared **egress scope**, declared **required
agent capabilities**, the names (never values) of credentials it needs, and whether it contains
write-capable nodes.

**Distribution:** a signed, versioned bundle. Export and import from the UI. A community index — a
repository of manifests — with an in-app browser where unverified Packs are marked loudly.

**Installation shows a review diff:** what flows, what node types, what egress scope, which
credentials it will request, and whether write-capable nodes are involved. The installer grants at
most the declared egress scope. Packs never carry secret values.

**Fork-friendly.** Installing a Pack and then editing a flow forks it into the local library and marks
it detached, with a merge prompt when the Pack updates. AV techs will *always* need to tweak
something; make that a supported path rather than something that silently breaks on the next update.

### Fixtures as tests

Every Pack ships recorded device responses. These serve three purposes: they let someone author a Pack
at a desk with the gear locked in a venue; they are the Pack's regression tests in CI; and when a
firmware update changes a response format, the fixture is what tells you which of the twelve flows
broke.

### Seed Packs

Chosen to prove the primitive set rather than to be a vendor list. Each is also a documentation page
and a CI test. **If all seven can be authored entirely through the UI, the primitive set is right.**

| Pack | What it proves |
|---|---|
| **Network Basics** | ICMP, TCP connect, HTTP, DNS, certificate expiry — the tier-1 experience |
| **SNMP Switch** | Walk and table, interface counters, PoE, topology feeding the dependency graph |
| **ASCII over TCP** | Connection Scope, delimiter framing, IAC stripping, regex extract — the generic control-processor shape |
| **Projector Control** | Challenge-response authentication, hashing, multi-turn conversation |
| **REST / GraphQL API** | HTTP auth variants, JSON traversal, the 200-with-errors gotcha |
| **Network Discovery** | ARP/bridge/topology walk, bounded sweep, reverse DNS |
| **Binary over TCP** | Build Bytes, length-prefix framing, CRC, bit fields |

---

## 15. User interface

| Surface | Purpose |
|---|---|
| **Status wall** | Grouped by device group or physical location. Designed to live on a rack-room screen: large, glanceable, no chrome. Degraded is a distinct colour, not a shade of down. |
| **Devices** | Inventory, tree by dependency (which doubles as the topology view). Filter by tag, VLAN, agent, Pack, last-seen. Bulk agent reassignment. |
| **Device detail** | Monitors, connection health, recent events, children, credentials in use, discovery metadata, reachability state. |
| **Monitor detail** | Uptime bar, metric charts, incident history, and **the run inspector**. Accepts a time range for deep-links. |
| **Flow library** | By category, by Pack, with usage counts ("used by 14 monitors"). |
| **The builder** | See below. This is the product. |
| **Incidents** | Open and historical; acknowledge, comment, resolve, and the suppression tree. |
| **Alerts** | Policies, channels, silences, maintenance windows, show mode. |
| **Discovery** | Candidate review queue with diff-since-last-scan and reachability triage. |
| **Agents** | Fleet view — link state, version, capabilities, assigned monitors, spool depth, clock skew. Enrolment, approval, revocation. Per-agent egress policy with a diff view for proposed changes. |
| **Packs** | Installed, available, updates, install review diff. |
| **Settings** | Users and roles, egress allowlist, retention, sandbox limits, backup, self-health. |

### The builder

Five things decide whether an AV tech can actually use this. They are not polish; they are the
product.

**1. Test run against a real device.** A persistent bar: device selector, "Test run", live result.
Executes the **current unsaved draft**, paints every node with pass/fail/skipped and per-node timing.
Nobody can author a byte protocol blind — this is the core authoring loop.

**2. The byte inspector.** Click any edge and get a hex dump with an ASCII gutter, offsets, selectable
ranges, a ruler showing where the framing strategy split the stream, the decode the downstream node
will apply rendered live, and copy-as options. **This is the single most-used debugging tool in the
product.** Budget real time for it.

**3. Capture and replay.** Record a device response once, save it as a fixture on the flow, and replay
it while iterating on parsing with no device present. Feeds directly into Pack testing (§14).

**4. Semantic version diff.** Flows are immutable once published; editing forks a draft. Show
"Regex Extract: pattern changed", "node added: Threshold" — not a JSON diff. Monitors pin a version or
track latest; the flow page shows which monitors publishing would affect.

**5. Palette that finds things.** The catalogue is large, so: **synonym search** — "telnet",
"crestron", "ascii", "port 23" all surface the raw-ASCII TCP preset — and **three difficulty tiers**
filtered by default.

**Guardrails at edit time, not runtime.** Connection type validation with suggested fix nodes.
Unreachable nodes greyed with a warning. Publish blocked on: no `Emit Status`, undeclared metric
labels, a loop without an iteration cap, worst-case run duration exceeding the monitor's interval, or
a required capability no assigned agent declares.

**A protocol-doc paste affordance** is worth building once the basics work: paste a request/response
pair from a vendor PDF and get a pre-wired build → expect → extract triple. Enormous first-run
leverage.

---

## 16. Security model

Be blunt about what this is: **a system that sends arbitrary bytes to arbitrary hosts on a schedule,
with payloads composed in a browser, from machines distributed across every VLAN.** That is a
genuinely dangerous shape. It is also the shape that makes the product work. Nothing below is optional
polish.

### Threat model

| Threat | Mitigation |
|---|---|
| SSRF / internal pivot via a crafted flow | Egress allowlist, default-deny |
| Sandbox escape | Isolated sandbox, no host objects, hard limits |
| Credential exfiltration by a flow | Sealed frames — but see the honest limitation below |
| Credential leakage via Pack export | Export strips secret references |
| DoS of venue gear | Engine-enforced per-device rate limits |
| DoS of the platform | Hard execution bounds, non-backtracking regex |
| **Core compromise reaching every VLAN** | **Agent-local egress policy that Core cannot widen** |
| **Agent compromise yielding device credentials** | Scoped secrets, memory-only by default |
| Rogue agent enrolment | One-time token, explicit approval, revocation |
| Malicious Pack | Signing, review diff, egress scoping |

**Stated assumption:** flow authors are semi-trusted staff, not the public, and agents are inside the
trust boundary. A compromised agent can lie about results. Say this out loud in the documentation —
over-claiming the isolation is how someone ends up exposing this to the internet.

### Egress control

The single most important control, and the one most similar products skip.

- **Default-deny allowlist** of address ranges, ports and protocols.
- **A hard denylist that cannot be overridden:** loopback, link-local and metadata addresses, the
  platform's own management addresses, and the database — **including their IPv6 equivalents and
  IPv4-mapped IPv6 forms**, which are a standard bypass. Normalise every resolved address to a
  canonical form before matching.
- **Resolve, then pin.** Check the allowlist against the *resolved* address and connect to that
  address explicitly. Never re-resolve between check and connect, or DNS rebinding walks straight
  through. A redirect to a denied host is a hard failure, not a follow.
- **Per-flow egress scope** — a flow can be restricted to a subset, so an installed Pack cannot reach
  beyond the ports it needs.
- **Per-agent egress policy, locally authoritative.** Set at enrolment; Core can *propose* a change,
  which the agent surfaces for operator approval with a diff. **This is the control that makes a
  compromised control plane survivable rather than building-wide.** Write-capable nodes and multicast
  are likewise locally enabled, never remotely enabled.
- Log every denial as a security event. Repeated denials from one flow is a real signal.

**The sweep node needs specific care:** a bounded CIDR sweep is a legitimate primitive and also a port
scanner. Cap concurrency and total addresses, rate-limit it, log every sweep with its range, and make
a sweep outside a Pack's declared scope a publish-time failure rather than a runtime truncation.

### Secrets

Stored encrypted at rest with an envelope scheme. **Flow definitions contain a handle, never a value.**

The clean rule — *"nothing ever materialises a secret into a frame"* — does not survive contact with
challenge-response authentication, where a secret must be concatenated with a nonce and hashed in
userspace by a pure transform with no I/O boundary at all. So the honest rule is narrower:

**A secret resolves into a sealed frame**, which is:

- **non-capturable** — excluded from run captures and the byte inspector **by value scan, not field
  name**, so a secret composed into a larger payload is masked in the hex dump too;
- **non-exportable** — cannot reach a metric, an event, or a Pack export;
- **consumable only** by transports, hashing, HMAC and payload composition — and anything derived from
  a sealed frame stays sealed until it passes through a one-way function;
- **never readable** from an expression or the sandbox.

A flow author can *use* a password and compose it into a digest. They can never *see* one, and no
capture ever contains one.

**The honest limitation:** an author can still HMAC a secret and transmit the result, which is
exfiltration by another name. Sealed frames stop accidental disclosure, not a determined author.
Mitigate by **granting credentials to the devices they belong to** rather than making every credential
usable from every flow — and say the limitation plainly in the docs rather than implying more than the
mechanism delivers.

**On agents:** an agent receives only the credentials for its assigned devices, and holds them **in
memory only** by default — never written to local storage. The trade-off is real and must be visible
in the UI: an agent that reboots while Core is unreachable cannot run credentialed monitors until it
reconnects, though uncredentialed ones keep working. Offer an opt-in encrypted local cache for sites
that prefer offline-boot resilience, clearly flagged as a weaker posture, default off.

### Agent enrolment

1. An operator creates a **one-time enrolment token** — short-lived, single use, scoped to an intended
   agent name and egress policy.
2. The agent boots with the Core address and the token, **generates a keypair locally** — the private
   key never leaves the agent — and submits a signing request.
3. Core issues a short-lived client certificate and burns the token. **Enrolment waits in a pending
   approval queue by default**, because "a small computer appeared on the network and joined the
   monitoring system" should be a decision, not an event.
4. Mutual authentication thereafter, with automatic renewal well before expiry.
5. Revocation is immediate and instructs the agent to wipe local state.

### Roles

| Role | Capabilities |
|---|---|
| **Viewer** | Dashboards, status, history. **No run captures** — they can contain payload data. |
| **Operator** | Viewer, plus acknowledge incidents, create bounded silences, run a monitor manually, add devices. |
| **Author** | Operator, plus create/edit/publish flows, view captures, install Packs. **Explicitly privileged** — an author can make agents emit arbitrary traffic within their egress policies. |
| **Admin** | Author, plus credentials, egress policy, users, agent enrolment and revocation, write-capable nodes, sandbox limits, retention. |

Everything an author or admin does is audit-logged with a before/after diff.

**Write-capable nodes** — anything that mutates a device — are off by default per installation,
individually enabled by an admin, **locally enabled per agent**, visually flagged with a distinct node
colour in the editor, and audit-logged per execution. There is a real risk that a monitoring tool
quietly becomes a control system with no interlocks. This is the interlock.

### Operational security

- **Backup the encryption master key separately from the database.** If it lives only in an
  environment variable and is lost, every credential is unrecoverable; if it lives in the same backup
  as the database, the encryption is decorative. Say both, loudly, in the docs.
- **Boot-time reconciliation after a restore:** stale claims cleared, connection states reset,
  schedules reseeded, and missed-run counters suppressed for the first pass — otherwise a restore
  pages someone about a hundred thousand missed runs.

---

## 17. Decisions register

Settled, with reasoning. Reversing one should be deliberate.

| # | Decision | Why |
|---|---|---|
| D1 | **Primitives, not vendor integrations** | The person who knows the gear can't write a plugin. Vendor code is a treadmill with no end. |
| D2 | **Self-hosted, single site** | A rack appliance one person maintains. `site_id` carried so a split later isn't a rewrite. |
| D3 | **Visual node/flow editor** | The target user drags boxes; they don't ship modules. |
| D4 | **Bytes-first; decoding is explicit** | The one decision that lets binary and ASCII protocols share a builder. |
| D5 | **Typed ports validated at edit time** | Type errors surface at 2pm at a desk, not 6pm in front of a console. |
| D6 | **Connection Scope for multi-turn protocols** | A stateless request node cannot express challenge-response, and challenge-response is common. |
| D7 | **Session mode reuses the same flow body** | Push monitoring shouldn't be a second mental model. |
| D8 | **No Telnet node, no shell node** | The first is structurally wrong for port-23 AV devices; the second is an RCE surface. |
| D9 | **Assert node covers most checks without expressions** | Keeps the expression layer optional for the majority. |
| D10 | **Two expression tiers; sandbox arrives late** | If the catalogue is right the sandbox is rarely needed, and its absence is a free signal about which node is missing. |
| D11 | **Core + agents, agents dial out** | One firewall rule, no listening ports, works behind NAT and DHCP. |
| D12 | **Thick agents** | Multi-turn protocols on-subnet; correct latency; **monitoring survives the link dropping**. |
| D13 | **Core runs an agent too** | Forces exactly one execution implementation to exist. |
| D14 | **Agents schedule their own work** | Centralised scheduling puts a hole in the record exactly when the network breaks. |
| D15 | **Agent runtime shares Core's stack** | One executor implementation. Accepts a larger artifact and a small-computer hardware floor in exchange for no drift. |
| D16 | **No automatic agent failover** | Split-brain risk (two executors, one single-connection device) for a rare, immediately visible failure. Manual reassignment is one click. |
| D17 | **Agent-local egress policy is authoritative** | Makes a compromised control plane survivable rather than building-wide. |
| D18 | **Sealed frames, with the limitation stated** | Challenge-response requires hashing a secret in userspace; pretending otherwise makes the flagship use case unimplementable. |
| D19 | **Unicast only; no multicast in v1** | Removes the entire container-networking problem class. The venue routes unicast between VLANs. |
| D20 | **Multicast returns as a per-agent capability** | An agent in a VLAN has adjacency by definition — so it becomes a flag on one agent, not a platform deployment decision. Build the capability mechanism now; it's needed for version skew anyway. |
| D21 | **Discovery interrogates infrastructure** | Finds everything with an IP, not just what announces itself, and returns the physical port. |
| D22 | **One relational database; no separate queue or time-series service** | Every extra stateful service is another thing to back up, monitor and explain at 7pm. |
| D23 | **Uptime is time-weighted from state periods** | Run-count uptime breaks when intervals change and when runs are missed. |
| D24 | **Alert logic in-app; notification delivery outsourced** | The logic is differentiated; per-provider integrations are a forty-way treadmill. |
| D25 | **Metrics exported for Grafana; no custom datasource plugin** | Two existing paths already expose the data. |
| D26 | **Cardinality enforced in the editor** | With user-authored metrics, a guideline is not a control. |
| D27 | **Packs carry fixtures, and fixtures are tests** | Firmware updates break parsing; fixtures are what tell you which flow broke. |
| D28 | **Flow versions immutable; editing forks** | Makes agent caching trivially correct and history explicable. |

---

## 18. Acceptance scenarios

**These are the design's falsification tests, not a feature list.** Each must be buildable **entirely
through the UI**, with no code changes. If one cannot be, the node catalogue has a gap — and the fix is
a *node*, never a vendor adapter.

Protocol details below are indicative and need lab verification before any Pack ships.

### A. Wireless receiver — per-channel RF and battery

Raw ASCII over a persistent TCP socket, angle-bracket framing, **unsolicited pushes** (holding the
socket open *is* the subscription; there is no subscribe command), opt-in metering that must be
enabled with a write command, per-channel fan-out, and raw values that need a fixed offset applied.

Shape: Connection Scope in session mode → on-open sends metering rate and a full state request →
frame handler splits on the delimiter, decodes, regex-extracts verb/channel/property/value, switches
on property, applies scale-and-offset or a lookup per property, writes subscription state. Derived
monitors then read that state per channel and apply thresholds.

**What it proves:** a sibling product family from the same vendor uses *different command names and a
different offset* for the same concepts. In a hard-coded product that is a second adapter and a bug
farm. Here it is the same primitives with different lookup and scale configuration. **That difference
is the entire thesis.**

### B. Audio network controller — clock and subscription health

GraphQL over HTTPS with a self-signed certificate and an API key, deep JSON traversal, and a fault
enumeration with dozens of values of which only a few mean success.

Shape: GraphQL node with a pinned certificate fingerprint → assert the errors array is empty (the API
returns HTTP 200 *with* errors, so status-code assertions are useless) → JSONPath to the device list →
ForEach with continue-on-error → assert connection and clock state, expression-check a frequency
offset, count non-successful channel statuses, lookup the first fault to a human string → aggregate →
threshold → status.

**What it proves:** the large fault enumeration lives in a Pack's lookup table, editable by anyone who
reads a release note — not in a switch statement that needs a platform release.

### C. Control processor — raw ASCII on port 23

The classic "it says Telnet but it isn't" device: no option negotiation, no prompt, a banner to
discard, stray IAC bytes, and no way to know a response is complete except silence.

Shape: Connection Scope with IAC stripping → discard-before to eat the banner → send a bus report
command → quiet-period read → decode, split, regex-extract, count → assert against an expected device
count → send a second command → quiet-period read → assert link status.

**What it proves:** IAC stripping and quiet-period reads are the two unglamorous options that make an
entire device class work, and exactly what an HTTP-shaped monitoring tool lacks. A control device that
fell off the bus is precisely the fault that never shows up in a ping.

### D. Projector — challenge-response authentication

**The hardest test, and the reason Connection Scope exists.** A multi-turn conversation where the
second turn's payload is computed from the first turn's response *and* a secret.

Shape: Connection Scope → expect the greeting → regex-extract the auth flag and nonce → if auth
required, build bytes from nonce plus secret, hash it, and prepend it to the command → send → expect →
regex-extract a multi-digit error status → assert each digit → send a lamp-hours query → expect →
extract, emit metric, threshold → status.

**What it proves:** the sealed-frame model (§16) works — a secret is used inside a computed payload
and never seen. If this is authorable in the UI, so is essentially every challenge-response AV
protocol.

### E. Access switch — the monitor nobody builds

SNMP walk and table handling, lists of records, delta/rate with counter wrap, and topology data
feeding the dependency graph.

Shape: SNMP table read of the interface table → table select → ForEach → emit the *raw* counter as a
metric (emitting a computed rate as a counter breaks downstream rate functions), compute a delta with
wrap handling, threshold the error rate, assert operational status on critical ports → SNMP walk of
the PoE table → lookup numeric status to a name → assert → SNMP walk of neighbour data → emit device
candidates proposing dependency edges.

**What it proves:** port flap, rising error counters and PoE faults catch more real AV failures than
most device APIs — and unlike a device check, they tell you *where* the fault is.

### What these collectively justify

Every node they require is in §6.4. The ones they *justify* — the ones that would otherwise look like
over-engineering — are: Connection Scope with a multi-turn body; session mode sharing that same body;
IAC stripping, quiet-period reads and discard-before; scale-and-offset as a *node* rather than an
expression; large editable lookup tables; hashing inside a composed payload; delta with counter wrap;
device candidate emission; and fixtures.

That last one is required by all five: **you cannot iterate on parsing against live gear during a
show.**

---

## 19. Open questions

Not blockers, but decide them deliberately rather than by accident.

1. **The name.** "Beacon" is a placeholder and heavily used in this space.
2. **Local notification when Core is unreachable.** If Core is down and a critical device fails,
   nobody hears it. A per-agent local webhook for critical-only alerts closes the gap at the cost of a
   second alerting path that can double-notify. Suggested: offer it, default off, deduplicate against
   the scheduled-slot fence when Core catches up.
3. **Pooled assignment for location-independent monitors.** An HTTPS check on a public endpoint
   doesn't care which agent runs it. Genuinely useful; complicates assignment, suppression and metric
   attribution. Suggested: not in v1.
4. **Agent-to-agent reachability testing.** With several agents, the platform can measure the network
   *between* VLANs for free — a real signal ("the AV uplink is congested") that no device check
   provides. Cheap once a fleet exists.
5. **Minimum monitor interval.** A one-second floor is a footgun that will knock over someone's
   receiver. Consider making sub-five-second intervals an admin-only setting.
6. **Whether Grafana or the built-in UI is the venue tech's home screen.** If Grafana wins in practice,
   some dashboard work in the platform is wasted effort. Worth deciding early.
7. **Multi-site.** The schema carries a site identifier but nothing enforces it. If a second site is
   plausible, the cost of enforcing it now is far below the cost of retrofitting it later.
8. **Licence and IP ownership.** Self-hosted, community Packs, possible commercial future. This
   affects how the Pack index works and whether outside contributions are possible — and it should be
   settled before the first commit, not after.
