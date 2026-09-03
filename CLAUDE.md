# Working in this repository

Read [docs/spec/beacon-spec.md](docs/spec/beacon-spec.md) before making design
decisions. It is the contract. [docs/ROADMAP.md](docs/ROADMAP.md) says what order
to build in and what has to be true before a milestone counts as done.

## Non-negotiables

**Spec §3's twelve invariants.** If an implementation choice violates one, the
choice is wrong, not the invariant. [docs/invariants.md](docs/invariants.md) maps
each to where it lives and what proves it. Adding enforcement without adding the
test does not count.

**Terminology is load-bearing.** A **Node** is a box on the flow canvas. A
deployed collector is an **Agent**. Never use "node" for the latter — not in
identifiers, not in comments, not in UI strings.

**No vendor names outside `packs/` and test fixtures.** If a vendor string appears
anywhere else, the abstraction has failed. CI enforces this. When a device family
needs different behaviour, the answer is configuration — a lookup table, a scale
factor, a framing strategy — never a branch on a vendor.

**Bytes first, strings second.** Every transport emits `bytes`; decoding is an
explicit node (I1). Primitives are honest: a TCP node opens a TCP socket and does
not helpfully trim whitespace, assume UTF-8, or normalise line endings unless told
to. AV protocols are byte protocols, and surprises here cost hours of someone's
evening.

**A refusal must carry a suggestion.** Every edit-time rejection — a type
mismatch, a blocked publish — states the reason and, where one exists, names the
node that fixes it. That is what makes typed ports feel like help rather than
obstruction.

**Sealed frames are transitive.** Derive node outputs from their inputs with
`Frame.Derive` so the seal carries. Constructing a fresh `Frame` from sealed input
launders a secret into a hex dump, which is exactly what I4 exists to prevent.

## Where things go

See [docs/architecture.md](docs/architecture.md) for the full map. The boundaries
that matter:

- `engine` does not know what a node does.
- `nodes` do not know where they are running.
- `agent` owns *when*; `core` owns *what*.
- `core/store` is the only package that speaks SQL.

New node types register into `internal/nodes/registry`. Registration **is** the
capability declaration an agent sends to Core, so a new node type or an
incompatible config change needs its `ConfigSchemaVersion` bumped — otherwise an
old agent accepts an assignment it cannot run.

## Building UI

The design system is a contract like the spec. [docs/design/design.md](docs/design/design.md)
owns the foundations — tokens, type, state grammar, the component kit.
[docs/design/playbook.md](docs/design/playbook.md) owns each surface's anatomy,
states, and rules. [docs/design/mockups/](docs/design/mockups/) is the
**normative reference**: `beacon.css` is the token sheet `web/` ports verbatim,
`index.html` maps every surface to its mockup, and a real surface is built to
match its mockup — same shell, same anatomy, same copy voice. When
implementation needs to diverge, change the mockup and the docs first, then the
code. Never the reverse, and never silently.

## Conventions

- Every package has a `doc.go` stating its responsibility and the spec sections
  and invariants it owns. Keep it accurate; it is the map.
- Store methods take a `site.ID` as an explicit parameter, never from a context
  value, so forgetting to scope a query is a compile error (D30).
- Bounds from spec §6.2 come from config and are enforced ceilings. Do not
  hard-code a limit that an operator should be able to lower.
- `make check` before pushing: fmt, vet, lint, tests, repository rules.

## When something does not fit

If an acceptance scenario in spec §18 cannot be built entirely through the UI, the
node catalogue has a gap. **The fix is a node — never a vendor adapter, never a
special case in the engine.** Same for the sandbox: reaching for it is a signal
that a node is missing, and that signal is worth more than the workaround.
