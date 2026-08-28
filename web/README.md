# web

The status wall, the inventory, and — from milestone M6 — the builder.

## Planned structure

```
src/
  lib/types/     GENERATED from internal/flow/types. Do not hand-edit.
  lib/api/       Typed client over Core's API
  routes/
    wall/        Status wall. Designed to live on a rack-room screen: large,
                 glanceable, no chrome. Degraded is a distinct colour, not a
                 shade of down.
    devices/     Inventory, dependency tree (which doubles as the topology view)
    monitors/    Uptime bar, charts, incident history, and the run inspector
    builder/     The canvas. This is the product.
    incidents/   Open and historical, with the suppression tree
    alerts/      Policies, channels, silences, maintenance windows, show mode
    discovery/   Candidate review queue with diff-since-last-scan
    agents/      Fleet view, enrolment, per-agent egress policy with a diff
    packs/       Installed, available, install review diff
    settings/
  builder/
    canvas/      Nodes, edges, selection, layout
    palette/     Synonym search; three difficulty tiers, filtered by default
    inspector/   THE BYTE INSPECTOR. Hex dump, ASCII gutter, offsets, a ruler
                 showing where the framing strategy split the stream, the
                 downstream decode rendered live. The single most-used debugging
                 tool in the product — budget real time for it.
    testrun/     Persistent bar: device selector, test run, per-node pass/fail
                 and timing, executing the current unsaved draft
    diff/        Semantic version diff — "Regex Extract: pattern changed",
                 never a JSON diff
```

## The type mirror

`src/lib/types` is **generated** from `internal/flow/types`. The editor must
refuse an invalid edge before the user lets go of the mouse, using exactly the
rules the runtime uses. Two hand-maintained copies of a validation table diverge,
and they diverge in the direction of the editor permitting something the runtime
rejects. CI fails on drift.

The regex engine is deliberately **not** mirrored. `Regex Extract` uses a
non-backtracking engine, and the live match preview must agree with the runtime
exactly — on precisely the patterns where a backtracking engine would differ. So
the preview calls the API rather than running a second engine in the browser.
