# Beacon design system — "Graphite"

The visual foundations of Beacon's UI: the thesis, the tokens, the type, the
component kit, and the rules that keep it all honest. Surfaces and behaviour
live in [playbook.md](playbook.md); working mockups (including an interactive
builder) live in [mockups/](mockups/) with `mockups/beacon.css` as the draft
token sheet this document codifies.

**The mockups are normative** (see CLAUDE.md → Building UI): a real surface in
`web/` is built to match its mockup — same shell, anatomy, and copy voice —
and `beacon.css` ports verbatim as the token layer. Divergence goes through
the mockup and docs first, then code. The full index:

| Surface | Mockup | | Surface | Mockup |
|---|---|---|---|---|
| Library + styleguide | `index.html` | | Flows library | `flows.html` |
| Dashboard (home) | `dashboard.html` | | Builder — reference | `builder.html` |
| Status wall | `wall.html` | | Builder — **interactive** | `builder-play.html` |
| Incidents | `incidents.html` | | ↳ publish gates | `builder-publish.html` |
| Devices | `devices.html` | | ↳ byte inspector | `builder-bytes.html` |
| Monitors list | `monitors.html` | | ↳ session mode | `builder-session.html` |
| Monitor detail | `monitor.html` | | ↳ palette search | `builder-search.html` |
| Command palette | `palette.html` | | Packs | `packs.html` |
| Discovery | `discovery.html` | | Alerting | `alerting.html` |
| Agents | `agents.html` | | Settings | `settings.html` |

**Status:** dark theme shipped in mockups and specified here. Light theme is
planned — the token *structure* below is theme-agnostic; provisional light
values are marked.

---

## 1. Thesis

**An instrument, not a dashboard.** Beacon is a professional operations tool
used in dim rack rooms, at desks at 2pm, and in hallways at 6pm before doors.
It borrows discipline from three worlds it actually lives in:

- **High-performance HMI practice** (ISA-101): calm, near-achromatic chrome so
  that saturated color, when it appears, is information. A wall of green lights
  is noise; a quiet wall with one red block is signal.
- **The AV world** the users come from: engraved-label typography, segmented
  meters, tally-light discipline — quoted in structure, never in cosplay. No
  fake LEDs, no brushed metal, no rack screws.
- **Byte-protocol culture**: monospace is the voice of data. Hex dumps,
  addresses, timestamps and values are first-class citizens, not debug output.

Six commitments, in priority order:

1. **Quiet until something matters.** Normal is dim. Brightness is earned by
   abnormal state, and motion is earned only by *unacknowledged* abnormal state.
2. **Color is state.** Chrome is achromatic. A saturated hue always means a
   device state, a data series, or the one interactive accent — never
   decoration.
3. **Rules, not boxes.** Hairlines, whitespace, and the label voice structure a
   page. A box means an object: a canvas node, a popover, a hex viewport.
4. **If it's data, it's mono.** Values, addresses, timestamps, hex, durations,
   counts — IBM Plex Mono, tabular. Prose and labels — Archivo.
5. **A refusal carries a suggestion.** Every blocked action names its reason
   and, where one exists, the fix — ideally insertable.
6. **Segments, not gradients.** Time and level render as discrete segments —
   readable across a room, honest about bucket boundaries.

And one meta-rule that governs every screen: **purposeful density** — every
datum answers a question someone asks on *this* screen; if the question belongs
to another screen, so does the datum.

## 2. What we refuse

Hard review rules. If a change introduces one of these, the change is wrong.

- Card grids: stat-tile rows, icon+heading+two-lines boxes, a gray 1px border
  around every group of content. (Layout comes from rules, not boxes.)
- Gradients as decoration, glassmorphism, glow, backdrop blur.
- Inter/Roboto, 8–16px radius on everything, soft drop shadows on page content.
- Bounce/scale hover effects, skeleton shimmer, counting-up numbers.
- Emoji as icons; sparkle iconography; "magic" language.
- Dark-navy-plus-neon monitoring clichés; walls of glowing green tiles.
- Hue as the only carrier of state (accessibility *and* honesty).
- A silently disabled button. Disabled states always say why, adjacent.
- Vendor names anywhere in UI copy or fixtures (repository rule; use device
  roles: "Wireless RX 07", "Stage Switch A").

## 3. Color

Tokens as they exist in `mockups/beacon.css`. OKLCH-tuned warm graphite —
never blue-black.

### Surfaces & edges (dark)

| Token | Hex | Use |
|---|---|---|
| `--bg0` | `#151614` | page ground, canvas, wall |
| `--bg1` | `#1B1C1A` | panels-as-objects, node fill, palette hover ground |
| `--bg2` | `#232421` | raised: hover, active row, popover fill, selected |
| `--bg3` | `#2B2C29` | pressed, drag, scrollbar thumb, meter troughs |
| `--bg-input` | `#141513` | inputs sit slightly below their panel |
| `--bg-th` | `#191A18` | table header band, group-label band |
| `--bg-hover` | `#1F201D` | row hover |
| `--hairline` | `#242522` | internal rules — the primary structural line |
| `--edge` | `#292A27` | control and badge borders |
| `--edge-strong` | `#34352F` | object borders (nodes, popovers), emphasized rules |

### Ink

| Token | Hex | Use |
|---|---|---|
| `--ink` | `#E9E7E1` | primary text ("tungsten") |
| `--sub` | `#A9A69E` | secondary text |
| `--muted` | `#726F67` | tertiary, labels, placeholders |
| `--faint` | `#4E4C46` | disabled, grid dots, offsets, ambient annotations |

### The one accent

| Token | Hex | Use |
|---|---|---|
| `--signal` | `#8FB6CE` | links, selection, focus rings, active notches, delimiter marks, info |
| `--signal-strong` | `#A9CCE2` | link hover, toggle knobs |
| `--signal-dim` | `rgba(143,182,206,.14)` | selection washes |

Signal blue never carries device state. **Primary buttons are tungsten**
(`--ink` fill, `--bg0` text) — every strong color on screen keeps its meaning.

### State (fixed, never themed)

| Token | Hex | Meaning |
|---|---|---|
| `--up` | `#4CA96A` | confirmed healthy |
| `--up-dim` | `#3E8A58` | resting segments — so healthy history isn't neon |
| `--warn` | `#D9A13C` | degraded, warnings, show mode, amber facts |
| `--down` | `#D25F5A` | down, critical, refusals, error classes |
| `--maint` | `#6E8CA0` | maintenance / silenced |
| `--seg-unknown` | `#3A3B38` | unknown segments |

### Data series (validated)

Eight categorical slots, CVD- and contrast-validated against both surfaces
(dataviz method; re-run the validator when any value or surface changes):

dark: `#3987e5 #d95926 #199e70 #c98500 #d55181 #008300 #9085e9 #e66767`
light: `#2a78d6 #eb6834 #1baf7a #eda100 #e87ba4 #008300 #4a3aa7 #e34948`

Rules: fixed assignment order, never cycled; color follows the entity, never
its rank; beyond three series on all-pairs forms (scatter, small multiples),
fold to "Other" or facet; series colors never impersonate state colors.

### Light theme (provisional)

Same token structure. Starting points: page `#F4F3F0`, panel `#FCFBF9`, ink
`#1B1B19`, hairline `#E3E1DB`; state hexes hold (validated on light surfaces
with icon+label pairing); series use the light column above. To be finalized
with its own contrast pass — light is *derived from tokens*, never a filter.

## 4. State grammar

The accessibility requirement became the identity: **fill = confirmed,
outline = transitional, and shape + text always accompany color.**

| State | Glyph | Color | Source |
|---|---|---|---|
| Up | ● filled circle | up | state machine: up |
| Recovering | ○ outline circle | up | successes pending |
| Down | ■ filled square | down | n failures + dwell met |
| Suspect | □ outline square | down | first failure, unconfirmed |
| Degraded | ▲ filled triangle | warn | status frame: degraded |
| Unknown | ◌ dashed circle | muted | not being asked — never "down" |
| Maintenance | ⊘ slashed circle | maint | window/silence — collected, not notifying |

Glyphs are inline SVG (9–10px in rows, scaled up on the wall), always paired
with a text label outside the wall. Error-class badges (timeout, protocol,
assertion…) use the badge component in down/warn/info colorings — `protocol`
is always visually distinct because it routes differently.

## 5. Typography

Two families, strict jobs. Self-host in `web/` (mockups use Google Fonts).

- **Archivo** (variable: `wght`, `wdth`) — all UI prose, names, controls.
- **IBM Plex Mono** — all data. No exceptions; if it's a value, it's mono.

| Role | Spec |
|---|---|
| Page/section title | Archivo 600 · 16px |
| Body / rows | Archivo 400–500 · 12.5–13px / 1.45 |
| **Label voice** | Archivo 600 · `wdth 78` · 9.5–10.5px · uppercase · tracking .12em · `--muted` |
| Data, values, hex | Plex Mono 400–500 · 10–12px · `tabular-nums` |
| Hero figures | Plex Mono 500 · 22–30px · tight tracking |
| Wall clock | Plex Mono 500 · 30px |

The label voice (condensed caps) is the system's structural signature — it
marks sections, table headers, and rails. Use it to *name structure*, never
for emphasis inside prose. Wordmark: `BEACON` in the label voice at 650,
tracking .14em.

## 6. Geometry, space, elevation

- Base grid 4px; standard paddings 10/14/16/20.
- Radius: **2px** badges · **3px** controls · **4px** objects (nodes, panels) ·
  **6px** overlays. Nothing rounder.
- Rows: 36px tables (dense default) · 30px rails/palette · 27px status bar ·
  34px wall rows · 46px top bar.
- Borders: 1px always. `--hairline` for structure, `--edge` for controls,
  `--edge-strong` for objects. Dashed = provisional/annotation (scope, notes,
  proposals, unverified).
- **Elevation: flat.** Shadows exist only on true overlays (popover, modal,
  drag ghost): `0 6px 24px rgba(0,0,0,.45)` / modals `0 18px 60px rgba(0,0,0,.55)`.
- Focus: 2px `--signal` outline, offset 1. Selection: signal border or a 2px
  left notch. Active nav: bg1 + notch.

## 7. Iconography

16×16 grid, `stroke: currentColor` at 1.3px, round caps/joins, geometric and
literal (rack unit, pulse, radar, antenna, cube, bell, sliders, gauge,
linked-boxes). Icons appear in the rail, palette contexts, and ⌘K rows —
sparingly elsewhere. Never filled blobs, never emoji. The current set lives
inline in the mockups; extract to a sprite in `web/`.

## 8. Component kit

Everything below exists in `mockups/beacon.css` + the mockups; this is the
contract for rebuilding them properly.

- **Buttons** — primary (tungsten fill), secondary (bg2 + edge), ghost,
  danger (outline down); 30px, `btn-sm` 24px. A disabled button always has an
  adjacent stated reason.
- **Inputs / selects** — 30px (26px in rails), bg-input, signal border on
  focus. Code-valued inputs switch to Plex Mono.
- **Toggle** — 26×15, signal when on.
- **Badges** — mono 10.5px, 2px radius; neutral, err, warn, ok, info; dashed
  border = missing/unverified.
- **Tables** — full-bleed, label-voice headers on `--bg-th`, hairline rows,
  36px, hover wash; numeric cells right-aligned mono; entity cells `--ink` 500.
- **Segment strips** — the signature: uptime (16px, 2px gap), table minis
  (11px, 3px wide), meters (spool). Colors: up-dim/warn/down/maint/unknown.
  Never smooth bars, never gradients.
- **Statline** — facts as a prose line with mono numbers; the replacement for
  stat cards, everywhere.
- **Section rule (`.sect`)** — 34px band: label voice + optional count +
  trailing action. The unit of page structure.
- **Rails** — right-hand detail panes: header block, then rule-divided
  sections. Same anatomy on every surface (playbook: "selection drives the
  right rail").
- **Tabs / range pickers** — text tabs with bg2 active; mono range chips.
- **Popover / modal** — the only shadowed objects; modal gates pattern (see
  playbook: publish).
- **Toast** — bottom-center, bg2, 2.2s; for acknowledgements and prototype
  stubs, never for errors that belong inline.
- **Tree rows** — box-drawing connectors in `--faint` mono; state glyph;
  trailing mono tag.
- **Timeline rows** — mono timestamp gutter (52–56px) + prose event, state
  words colored.
- **Hex viewer** — offset gutter (`--faint`) · hex bytes (`--sub`) · ASCII
  gutter (`--muted`); selection `--down` wash; delimiters underlined 2px
  `--signal`; **sealed ranges as hatched red masks** (value-scan masking, I4);
  frame map segment strip above multi-frame streams; copy-as chips.

### The canvas kit

- **Node**: bg1, edge-strong border, 4px radius; header (grab surface) =
  status slot + name (12.5px/550) + mono timing right; config summary line in
  mono 9.5px `--faint`, brightening on hover/selection. Run paint: 2px left
  border up/down + tick/cross; failing nodes show their message inline.
- **Ports**: 9px rings on edge centers; hover = signal ring + wash; **error
  ports exist on every node but render only on hover/selection**.
- **Wires are routed traces**: orthogonal runs, 10px-radius corners, 1.5px
  `#4E4F4A`. Routing: straight when aligned; mid-point elbow forward; clean Z
  between rows; below-loop only for same-row backtracks. Temp wire dashed
  signal; refused wire dashed down; selected wire signal.
- **Connection Scope**: dashed container with a docked header tab (label voice
  + mono config). **Note**: dashed, draggable, muted prose — first-class.
- **Refusal popover**: title `type → type refused` (mono, down), one-clause
  why, one named fix with a working insert button.
- **Canvas ground**: bg0 with 26px dot grid at `#252624` — felt, not seen.
- **Calm by default** (normative for the real builder): byte dock collapsed
  until bytes exist, then opens itself; hints live in the inspector's empty
  state; zoom with true fit-to-content (floor 70%).

## 9. Charts

Follow the dataviz method; parameters for Beacon:

- Surfaces: chart on `--bg0`/`--bg1`; grid `--hairline`; axis text mono 9–10px
  `--muted`.
- Lines 1.8px, no area fills, no gradients, no glow; current-point dot with
  bg ring.
- **Threshold bands**: warn/crit as 9–10% opacity fills of state colors with
  dashed 1px boundary lines — thresholds are chart citizens, not annotations.
- Annotations (events, battery swap): mono 9px `--muted`, or state-colored
  when they *are* state.
- One y-axis, always. Two measures = two charts.
- Series from §3, fixed order; ≥2 series always get a legend; text never wears
  series color.
- Interactive charts ship crosshair + tooltip (hover layer to spec in `web/`).
- Validate any palette change: `validate_palette.js "<hexes>" --mode dark
  --surface "#1B1C1A"` (and light).

## 10. Motion

- Sanctioned: state/overlay transitions 120–160ms ease-out; dock/inspector
  reveals ≤200ms; the wall's **unacknowledged-down pulse** (2.4s ease, ~6%
  lift) — the only looping motion in the product, and it stops on ack.
- Live values snap — no tweening, no count-up. New rows appear, they do not
  slide.
- `prefers-reduced-motion` disables the pulse and all transitions.

## 11. Voice

- Plain, operational, specific. Sentence case everywhere except the label
  voice. No exclamation marks, no apologies, no "Oops".
- **The refusal grammar**: `<thing> refused/blocked` + one-clause reason +
  `Fix: <specific action>` — with the fix actionable in place when possible.
  ("Agent lx does not declare byteops.checksum. Fix: upgrade lx to 0.9.3, or
  pin Projector — East to v3.")
- Consequences over mechanics: "suppressed · parent Stage Switch A", "recorded,
  not discarded", "one alert, not sixty".
- Numbers carry their units with a thin space (`-61 dBm`, `41 s`, `12.4 s of
  60 s`). Timestamps mono; relative + absolute where both matter.
- Buttons say what happens: "Publish v5", "Run draft", "Silence 1 h…". An
  ellipsis promises a follow-up step.
- Empty states are invitations to act, never mood.

## 12. Accessibility

- State is never color-alone: shape + label (grammar §4); series get legends +
  direct labels; sub-3:1 chart colors trigger the relief rule (labels/table).
- Text contrast: ink 13.9:1, sub 7.3:1, muted 3.4:1 on bg0 — muted is for
  supporting text ≥10px, never for essential small text.
- Keyboard: the full spine (⌘K, j/k, ↵, g-chords, ⌫, esc, ?) with visible
  focus everywhere; the mouse is never punished, the keyboard never required.
- Hit targets ≥24px for bare icons (ports get 3px halo hover zones).
- Reduced motion honored (§10). The wall is readable at 3m: 13.5px minimum,
  states also encoded by row tint + glyph.

## 13. Implementation notes

- `mockups/beacon.css` is the draft token sheet — port tokens verbatim to
  `web/` as CSS custom properties; components rebuild in React against them.
- Self-host both fonts with `font-display: swap`; subset Archivo with the
  `wdth` axis intact.
- The editor's connection validation mirrors `internal/flow/types` (generated,
  CI-gated) — the refusal popover's reason strings come from that layer.
- Wall = a route with its own chromeless layout, same tokens.
- Every surface state addressable by URL (playbook: interaction spine).

## 14. Decision log

| Date | Decision |
|---|---|
| 08-28 | Direction: "Graphite" instrument — modern professional, zero hardware cosplay. Dark-first. |
| 08-28 | Type: Archivo (+wdth label voice) + IBM Plex Mono for all data. |
| 08-28 | Tungsten primary buttons; signal blue reserved for interaction, never state. |
| 08-28 | State grammar: fill = confirmed, outline = transitional; never color alone. |
| 08-28 | **No cards** — rules, not boxes. AI-slop catalog refused wholesale. |
| 08-28 | Canvas wires: orthogonal routed traces, not beziers. |
| 09-03 | Workbench shell on every page: top bar · rail (Observe/Build/Platform) · status bar. Wall is the one chromeless exception. |
| 09-03 | Dashboard is the in-app home; the wall is a display surface. |
| 09-03 | ⌘K palette as the power spine (Vapi/Linear-informed); flat 11-destination nav. |
| 09-03 | Purposeful density rule; busy-ness pruning pass across all surfaces. |
| 09-03 | Every rail destination is a real page; interactive builder playground. |
| 09-03 | Builder calm-by-default: collapsed byte dock, hover-revealed error ports, simple-first examples, true fit. |

## 15. Open items

- Light theme: finalize values + contrast pass; theme toggle mechanics.
- Icon set: extract to sprite; design the remaining glyphs (packs detail,
  discovery sub-states).
- Chart hover/tooltip layer spec; brush-to-zoom on monitor charts.
- Empty/first-run states for every surface.
- Device detail surface (playbook backlog).
- Wordmark/logotype treatment beyond the label-voice text mark (blocked on
  the product's final name — spec §19.1).
