# Beacon surface playbook

The catalog of every surface in the product: what it is for, what it is made of,
what states it has, and the rules that keep it honest. Mockups live in
[mockups/](mockups/) — serve that directory and start at `index.html`. Visual
foundations — thesis, tokens, type, state grammar, component kit, charts,
motion, voice — are codified in [design.md](design.md); this document is about
*surfaces and behaviour*. When the two disagree, fix the disagreement, don't
pick a winner silently.

**Direction, in one line:** an instrument, not a dashboard — calm graphite chrome,
hue reserved for state, mono for data, structure from rules and whitespace rather
than boxes, quiet until something matters.

---

## The shell contract

Every page sits in the same frame. Nothing about it changes between surfaces.

| Region | Contents | Never |
|---|---|---|
| **Top bar** (46px) | `BEACON` mark · context crumb · surface facts and primary actions · `⌘K` chip · clock | marketing, notifications bell, avatars |
| **Rail** (184px) | Three groups — **Observe** (Dashboard, Status wall, Incidents, Devices, Monitors), **Build** (Flows, Packs, Discovery), **Platform** (Agents, Alerting, Settings) — with icons, live counts, active-page signal notch | nesting, a second sidebar, collapse-by-default |
| **Work area** | The surface itself | boxes as layout (a box means an object: node, popover, hex viewport) |
| **Status bar** (27px) | Site truth: `118/124 up · 2 down · 1 suspect` · agents linked · spool · show mode · last result | anything that isn't the current state of the site or platform |

The top bar is a *working surface*: the builder shows its publish gate there
("worst case 12.4 s of 60 s interval ✓"); a draft shows its badge. Facts, not
decoration.

**The one exception:** the status wall is a display surface, not a navigation
surface — it drops the rail and top-bar chrome entirely but keeps the status-bar
component (scaled up for distance).

### The interaction spine

- **⌘K goes anywhere and does anything.** Devices, monitors, flows, incidents,
  actions — two keystrokes. Palette rows carry their context (state, suppression,
  owner) and the shortcut that would have done it, so the palette teaches the
  fast path while performing the slow one.
- **Keys everywhere, mouse never punished.** `j/k` move lists, `↵` opens,
  `g d / g i / g w` jump, `?` shows the sheet.
- **Selection drives the right rail.** A run, an incident, an agent, a node — the
  detail always appears in the same place with the same anatomy: header, then
  rule-divided sections.
- **Every state is a URL.** Monitor + time range, run capture, draft diff — all
  addressable (this is also the Grafana deep-link contract, spec §12).
- **Purposeful density.** Every datum answers a question someone asks on *this*
  screen; if the question belongs to another screen, so does the datum.

---

## Observe

### Dashboard — `dashboard.html`

The in-app home. Answers, in order: *is the site healthy? what is on fire? what
will be on fire? are the eyes themselves open?*

- **Anatomy:** hero (24 h uptime figure + site strip) → open incidents → "Needs
  attention" table → right rail: agents, last-hour feed, Tonight (show mode,
  maintenance, retention).
- **Rules:** "Needs attention" is the pre-alert surface — flap %, throttles,
  spool growth, expiring certs — and every row carries a plain-words *why it
  matters*. Nothing here duplicates the incident list; it shows what has not
  paged anyone *yet*.
- **Spec:** §11 (flap % first-class), §8 (throttled outcome), §19.6 (built-in UI
  as the tech's home).

### Status wall — `wall.html`

A rack-room TV, glanceable from meters away. **Quiet until something matters**:
healthy devices are dim rows in rack-strip columns; the incident band on top is
the only loud thing. Only an *unacknowledged* down pulses (slow, and honors
reduced-motion) — acknowledged incidents hold steady. Show mode and agent health
live in the footer ribbon.

- **States:** incident band empty (thin "all quiet" rule) · 1–4 incidents ·
  suspect entries (outline glyph, amber) · suppressed children shown dim with
  "via <parent>".
- **Rules:** device value column shows *the one number that matters* for that
  device (RF dBm, runtime, error count). Degraded is a distinct color, never a
  shade of down (spec §15). Unknown is never red.

### Incidents — `incidents.html`

Queue plus anatomy of one incident.

- **Anatomy:** list (severity glyph, title, error-class badge, routing, duration,
  ack state) · detail rail: facts → state-machine timeline → suppression tree.
- **Rules:** the timeline narrates the spec's machine exactly — first failure →
  suspect (capture kept, faster polling) → count met → dwell met → down →
  grace period → children suppressed → *one grouped page*. Suppression is
  recorded, never discarded ("was already down" is called out). `protocol`-class
  incidents visibly route to the flow author, not on-call — the platform's most
  opinionated routing decision, shown, not buried.
- **Spec:** §11 throughout; I8, I9, I12.

### Devices — `devices.html`

Inventory beside the dependency graph, so suppression is legible in place.

- **Anatomy:** tree pane (DAG rendered as an indented tree, agent tag per node,
  edit-edges affordance) · toolbar (filters, bulk reassign, add) · full-bleed
  table: state, address, agent, tags, monitors up/total, 30 d strip, last seen.
- **Rules:** a suppressed device says *by whom*, inline, as a link. Discovery
  reachability verdicts (`filtered — ACL?`) surface here. VLAN is a filter, not
  a column.
- **Spec:** §10 (device_edges is a DAG), §13 (reachability triage).

### Monitor detail — `monitor.html`

Where "why did this go red?" gets answered without leaving the page.

- **Anatomy:** header (state, flap badge, run/silence/edit actions) → KPI line
  (uptime excl-maintenance with raw beside it, flap %, last/next run) → 90 d
  state strip → threshold-band charts → run history table → run-inspector rail.
- **Run inspector:** per-node list with timing and sizes; the failing node
  selected; "Why it failed" in one sentence with the arithmetic; the actual
  input bytes with the culprit highlighted; context (device vars, previous
  value).
- **Rules:** uptime always shows both figures (raw and excl-maintenance, D23).
  Throttled runs appear in history as throttled — not failures — with the reason
  in plain words. `TRANSITION` is a stored flag on the run row and renders as
  one.
- **Spec:** §8 (captures, suspect-window retention), §10 (uptime), §11.

---

## Build

### Flows library — `flows.html`

The catalog. A flow is logic; the monitors are its instances — this page keeps
that separation visible.

- **Anatomy:** toolbar (filter, category, source, New flow) · table (name +
  draft tag, category, version, **used-by count**, source lineage, updated) ·
  rail for the selected flow: versions as *semantic* one-liners, used-by
  monitors with tracking/pinned marks, open/export/duplicate.
- **Rules:** version history is never a JSON diff — "Regex Extract: pattern
  changed" (spec §15.4). Pack lineage says `forked` when detached.

### Builder — `builder.html`

The product. Full shell (nothing disappears), then three working columns and a
dock.

- **Palette** (left): search-first, three tiers filtered by default, scope-only
  nodes tagged, "27 more in tier 3" explicit. Search state —
  `builder-search.html`: synonyms surface the right primitive ("telnet" → raw
  ASCII TCP preset) *with the reasoning*, and the deliberate omission gets a
  "why is there no Telnet node?" link instead of silence.
- **Canvas:** nodes are objects (the one legitimate box); **wires are routed
  traces** — right-angle runs with rounded corners, not beziers. Connection
  Scope is a dashed container owning one socket; both `If` outputs joining one
  input is drawn plainly (the branch-join select rule, spec §6.1). Notes are
  first-class canvas citizens — protocol reverse-engineering lives there.
  A test run paints every node pass/fail/skipped with per-node timing; the
  failing assert shows its message inline.
- **Refusals:** an illegal connection draws dashed red and raises a popover that
  names the reason *and the fix*, with an insert button ("string → number
  refused. Insert Coerce…"). No refusal without a suggestion — anywhere.
- **Inspector** (right): rule-divided config sections; ports listed honestly
  (error port "unconnected — propagates"); named bindings surfaced
  (`{{reply}}`); last-run capture one click from config.
- **Dock:** test-run bar (device selector, run draft, fixture save/replay,
  result line) above the byte inspector. **Calm by default is normative:** the
  byte panes stay collapsed until a run produces bytes (then open themselves),
  and error ports render only on hover/selection — present everywhere, visible
  when relevant.

### Byte inspector — `builder-bytes.html`

The single most-used debugging tool in the product (spec §15.2), so it gets a
full expanded state:

- **Anatomy:** frames rail (Split Frames result, selectable) · hex pane with the
  **framing ruler** — a segment map of the frames, delimiters underlined in
  signal, the selected frame tinted — · decode preview and "what downstream
  sees" chain (`"070"` → int 70 → −128 → −58 dBm) · save-as-fixture.
- **Rules:** sealed ranges render as hatched, labeled masks — by value scan, so
  a secret composed into a payload is masked too (I4). The fixture button
  carries its consequence: "runs as this flow's regression test in CI."

### Publish — `builder-publish.html`

Publishing is a gate, and the gate is a checklist you can read:

- Every gate from spec §15 as a pass/fail row — reaches Emit Status, worst-case
  vs interval, declared labels, **agent capability** ("lx does not declare
  `byteops.checksum` — upgrade lx, or pin Projector — East to v3").
- Semantic diff since the last version; on-publish impact (who tracks, who is
  pinned); a changelog field.
- Publish stays disabled while any gate fails, with the reason beside the
  button — never a silent grey button.

### Session mode — `builder-session.html`

The thesis screen: the *same visual body* serves polling and push (D7).

- The scope header re-reads "session — body runs per inbound frame"; an **On
  open** lane holds what re-runs after every reconnect; the handler chain writes
  subscription state (`rf.ch{{ch}}`), shown live beside the canvas with its TTL;
  a "read by" pointer names the derived monitors.
- The dock swaps test-run for session truth: connected time, frames/min, drops,
  latest inbound frame, and the handler's state write with the previous value.
- **The forced choice** (modal): a session source must declare how liveness is
  known — it streams / keepalive poke / poll it instead. You cannot create a
  silent-by-design session source with a liveness window; that is a false-alarm
  generator (spec §9), so the UI refuses to let it exist.

---

## Platform

### Agents — `agents.html`

The fleet, and the trust boundary made visible.

- **Anatomy:** toolbar statline · pending-enrolment band (fingerprint, proposed
  egress, approve/reject — enrolment is a decision, not an event) · fleet table
  (link state, version, monitors/devices, spool meter, skew, egress summary) ·
  detail: capability chips with gaps called out, and the egress **proposal
  diff**.
- **Rules:** the capability warning is written as consequence + fix ("3 monitors
  cannot be assigned… upgrade lx, or pin"). The egress panel states the power
  relationship: *Core can propose, never apply* (I7). A reconnecting agent's
  spool count is amber — it is evidence the design is working, not an error.

### Command palette — `palette.html`

See "interaction spine." Sections: **Go to** (entities with live context),
**Actions** on the top match (with required inputs noted — "requires reason +
expiry"), **Recent**. Footer teaches `g`-chords and `?`.

---

### Builder — interactive prototype — `builder-play.html`

The whole builder surface, live, with the engine simulated. **Calm by
default:** it opens on a simple two-row flow at 100% zoom with the byte dock
collapsed; scenario D loads from the example picker; the bytes dock opens
itself when a run produces bytes, and error ports appear on hover — nothing
shouts until there is something to say. Drag nodes (wires
re-route as traces), drag port → port to connect — mismatched types refuse with
the reason and a working **Insert fix** that creates and wires the missing
node(s); joins into an occupied input toast the select-join rule. Palette search
filters live with synonyms ("telnet" → raw-ASCII TCP, with the why); tier
toggles reveal tier 3. Selecting a node swaps the inspector to that type's real
config form; ⌫ deletes; zoom works; **Run draft** paints the flow node-by-node
and lands the assert failure in the result line; **Publish** computes the gates
from what is actually on the canvas (delete Emit Status and watch the gate
fail). Everything visible is intended UI; only the engine behind it is canned.

### Monitors list — `monitors.html`

The devices pattern applied to checks: state, interval, uptime, flap, agent;
`derived` tagged inline; bulk silence. Rows open monitor detail.

### Discovery — `discovery.html`

Review queue with **diff since last scan**: new candidates band, missing band
("an event, never a delete"), ignored count. Row facts carry the switch port
(the dependency graph's favourite datum) and the reachability verdict —
`reachable` / `filtered — ACL?` / gone-from-ARP. The rail shows the profile
match ("OUI + open port 2202 → AV Wireless Pack") and the monitors accepting
would propose — nothing is created until accepted.

### Packs — `packs.html`

Installed table: signature state (unsigned marked loudly), fixture CI results
(a failing fixture links to the draft that fixes it), egress scope. The update
rail is the install review diff: what changes, egress unchanged, credentials
**named never valued**, write-capable nodes in an amber box with the interlock
spelled out, and local forks promised a merge, never an overwrite.

### Alerting — `alerting.html`

The error-class routing table is the centrepiece — `protocol` routes to the
flow author, with the reasoning in a note under the table. Channels with
grouping timings; silences with mandatory expiry/author/reason and the weekly
digest; maintenance windows with IANA timezones; show mode as a first-class
definition.

### Settings — `settings.html`

Roles with capability summaries; the egress floor with the hard denylist
rendered non-editable; §6.2 bounds as enforced ceilings; retention; platform
self-health (scheduler lag, series growth, dedupe hits); the master-key
warning stated loudly.

---

## Not yet mocked (backlog)

| Surface | Intended shape |
|---|---|
| **Device detail** | Monitor list + connection health + children + credentials-in-use + discovery metadata; same rail anatomy as monitor detail. |
| **Empty states** | First-run: no devices → point at Discovery; no flows → point at Packs. An empty screen is an invitation to act, not a mood. |

---

## Change log

- **2026-09-03** — Shell unified (topbar/rail/status bar contract); Dashboard and
  Flows library added; Observe/Build/Platform nav grouping; ⌘K palette; busy-ness
  pruning pass. Builder states added: publish gates, expanded byte inspector,
  session mode + liveness choice, palette search.
- **2026-08-28** — Direction locked (“Graphite” instrument), state grammar
  (fill = confirmed, outline = transitional), routed-trace wires, first six
  screens.
