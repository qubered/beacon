# Beacon

A protocol-agnostic monitoring platform for AV and network infrastructure.

> **The name is a placeholder.** "Beacon" is heavily used in this space. It is
> confined to the module path, the binary names and the docs so a rename stays a
> find-and-replace — see [ROADMAP](docs/ROADMAP.md#still-open).

**Status: pre-implementation.** The [specification](docs/spec/beacon-spec.md) is
complete and the repository is scaffolded. See the [build plan](docs/ROADMAP.md).

## What it is

Beacon ships **network primitives, not device integrations.**

It has no idea what a wireless receiver is. It knows how to open a TCP socket,
send bytes you composed, split the response stream on a delimiter, pull a number
out with a regex, subtract 128 from it, and raise an alert when it drops below a
threshold — all defined in a browser, saved as a reusable **Flow**, and shareable
as a **Pack**.

If Beacon supports a vendor's gear, it is because somebody built a Pack in the UI.
Not because anyone wrote vendor code.

### Why

Every existing monitoring tool draws the extensibility line in the wrong place —
a JS module and a redeploy, a custom EXE, a Python plugin. The gap is a people
problem, not a technology one: **the person who knows the gear is not the person
who can write a monitoring plugin.** An AV tech has the vendor's command-strings
PDF open on a second monitor and knows exactly what a given query should return.
They cannot ship a module to a monitoring server. They can drag five boxes onto a
canvas.

The bet is that the AV protocol long tail is mostly the same handful of shapes —
send-bytes/read-bytes, request/response, walk-a-tree, subscribe-to-a-stream —
wrapped in an infinite variety of framing and encoding. Make framing and encoding
into nodes, and the long tail becomes user-authorable.

## What it is not

- **Not multi-tenant.** A site identifier is carried and enforced throughout so a
  future split is not a rewrite, but no tenancy logic ships.
- **Not a control system.** Write-capable nodes exist because some devices must be
  told to start emitting telemetry. Every one of them is admin-gated, locally
  enabled per agent, visually flagged in the editor and audit-logged per
  execution. **This does not grow into show control.** That is a deliberate
  boundary, not an unfinished feature, and the interlocks exist to keep it one.
- **Not a NOC replacement.** It exports to Prometheus/Grafana and can forward to
  an existing alert manager rather than pretending to be either.
- **Not a scripting sandbox.** A sandboxed transform node exists for the small
  percentage the node catalogue cannot express. If people are writing long
  transforms, the catalogue has a gap and *that* is the bug.

## Shape

Two tiers. A **Core** control plane, and one or more **Agents** — typically one
per VLAN — that schedule and execute their own assigned monitors, hold device
sockets, and ship results back. Agents dial out; Core never dials in. An agent
keeps monitoring while the link is down and backfills on reconnect.

Core runs an agent too. That is a design constraint, not a convenience: it forces
exactly one execution implementation to exist.

See [docs/architecture.md](docs/architecture.md).

## Getting started

```bash
make dev     # postgres + core + grafana, migrated, on localhost
make test    # unit tests
make check   # fmt, vet, lint, tests, repository rules
```

Requires Go 1.25+, Node 22+ with pnpm, and Docker.

## Security

This is a system that sends arbitrary bytes to arbitrary hosts on a schedule,
with payloads composed in a browser, from machines distributed across every VLAN.
That is a genuinely dangerous shape, and it is also the shape that makes the
product work. Read [docs/security.md](docs/security.md) before deploying it, in
particular the parts about what the isolation does **not** promise.

## Licence

[AGPL-3.0](LICENSE). Packs are content, not derivative works — Pack authors choose
their own licence. See [D31](docs/decisions/0003-licence.md).
