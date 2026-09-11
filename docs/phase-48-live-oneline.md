# Phase 48 — Live one-line (state-aware symbology + connectivity straps)

## Why

The diagram canvas can already *place* a full double-busbar substation — two
`MV_BUSBAR` bus lines, the breakers/isolators as symbol glyphs, feeder
`MV_CONDUCTOR`s auto-rendered as wires between their placed endpoints. What it
**cannot** do yet is make that drawing *mean* anything about the live network:

1. **A switch's glyph ignores its position.** Every device draws its static
   class/subtype symbol from symbology, regardless of whether it is open or
   closed ([web/src/components/map/diagram-canvas.tsx:1366-1381](../web/src/components/map/diagram-canvas.tsx#L1366-L1381)
   only covers conductor wires, never device state). On a double busbar the
   *whole point* is "which bus is this feeder selected onto" — and today the two
   selector isolators look identical whether open or closed.
2. **Direct device→device connectivity draws no wire.** The canvas deliberately
   only auto-wires real `geometry_type='line'` conductors; a rule-C connection
   (`MV_BUSBAR T2 → {switch} T1`, no conductor between them) has no feature to
   render from, so the bus-to-selector "strap" is invisible unless hand-drawn as
   `line`/`tap` furniture. See the "conductors render as wires" note at
   [diagram-canvas.tsx:1366-1381](../web/src/components/map/diagram-canvas.tsx#L1366-L1381)
   — the generic per-`feature_connectivity` connector was **removed** on purpose
   (it drew phantom elbows); re-introducing it needs a tighter guard.

Reference artifact for the target render: `docs/diagrams/double-busbar-one-line.svg`.

Both gaps are **pure-client** — the data is already fetched per placement — so
this phase adds no engine or schema change. It reuses the exact overlay pattern
Phase 46 (control/trip lines) established: derive extra render from data the
canvas already holds, display-only, not draggable.

Decision (confirmed with the user): **declared state first** — drive the switch
visual off the switch's own `normal_state` / position field, not a powerflow
run. Live energization is a later slice on the same hook.

## Slice 1 — State-aware switch symbology (declared state)

The switch's state field arrives free: the canvas already fans out each
placement's full feature record via `useQueries`
([diagram-canvas.tsx:3054](../web/src/components/map/diagram-canvas.tsx#L3054)
fetches `/connectivity` per placement; the feature record fetch sits alongside).
So `normal_state` (and the bus-coupler / switch position fields) are in hand at
render time.

**Status: code-complete (2026-09-11), verification owed.** Implemented as a
**corner marker**, not a glyph tint — the resolved glyph is an opaque
icon/data-URI, so recolouring it in place is unreliable; a small ring on the
top-right corner reads unambiguously over any glyph and doesn't fight the icon.
This is the same non-destructive move the existing trace-role highlight ring
uses.

**Emphasis: normally-open points only (EE-driven refinement).** The first cut
badged both states (green closed / grey open); that was corrected. On a one-line
the safety-relevant thing to scan for is where the **open** points are — NOPs,
open ties, bus couplers, bus selectors. Badging every normally-closed switch
just paints the network with dots (the map-clutter problem re-created at symbol
level) and buries the one marker that matters. So the marker now fires **only**
for `NORMALLY_OPEN`; closed and field-less switches are left unmarked as the
assumed default, exactly as a real one-line omits them.

- **web/src/components/map/diagram-canvas.tsx** —
  - `declaredSwitchState(feature)` helper (returns `"closed" | "open" | null`,
    kept general for Slice 3) + `NORMAL_OPEN_POINT_COLOR` (amber `#f59e0b`)
    constant near the Phase 46 `CONTROL_LINE_COLORS` block. Reads the placed
    feature's own `normal_state` attribute — already in hand via the
    per-placement `featureResults` fetch, no new query.
  - In the `pointItems` render, a **bold amber ring** (hollow centre reads as
    "open") drawn **outside** the glyph's `rotate()` so it stays corner-pinned
    regardless of glyph rotation, with a `<title>Normally open</title>` tooltip.
    Drawn only when `declaredSwitchState(feature) === "open"`; everything else
    renders exactly as before.

**Interpretation guard (EE safety):** the marker means "normally open," **never
"open right now."** A `NORMALLY_CLOSED` coupler can legitimately be sitting open
(abnormal switching) — declared posture ≠ live position. The tooltip wording and
the "later slice" framing keep this honest; it's the core reason live
`device_state` (Slice 3) is the real prize, not more declared-state coverage.
- **Which field?** `normal_state` (NORMAL_STATE domain: `NORMALLY_CLOSED` /
  `NORMALLY_OPEN`, seed/bus_coupler_fields.sql) — the switch's *declared* design
  posture. Deliberately NOT the live `device_state` (which the coupler seed
  itself left as "a deliberate later nicety, not done") nor powerflow
  energization; both layer onto this same badge in Slice 3.

No metadata-driven *conditional symbology* — that would be a much larger schema
change (symbology is resolved per class/subtype today). The badge is a
client-side overlay on the already-resolved glyph, deliberately.

## Slice 1b — extend `normal_state` to the selector/tie/sectionalizer family

**Status: code-complete (2026-09-11), apply + verify owed.** Slice 1's marker
only fired on **MV_BREAKER** — the only class carrying `normal_state` (added by
seed/bus_coupler_fields.sql for the coupler). So a double-busbar's *selectors*
couldn't badge. Slice 1b adds the same field to the switch classes where a
normally-open point is a real per-instance fact:

- **seed/switch_normal_state_fields.sql** — reuses the existing `NORMAL_STATE`
  domain + values + `electrical` field group (all re-asserted idempotently),
  and adds a nullable, subtype-ruleless `normal_state text` field to
  **MV_ISOLATOR** (the bus selector), **MV_SWITCH** (generic tie/sectionalizing),
  **MV_SECTIONALIZER**, **MV_RECLOSER**. Set-generating over the four; an
  unseeded class is skipped. Mirrors the bus_coupler_fields.sql idiom exactly.
- **Taskfile.yml** — `seed-switch-normal-state` task; `task compile` owed per
  touched class after apply.

**Deliberately narrow — this is the whole point.** It is NOT added to every
switchable device. For an ordinary in-line feeder switch "normally closed" is
trivially true and carries no information; badging them all recreates the
map-clutter problem at symbol level. LV_SWITCH is left out (this thread is the MV
one-line) — a one-line addition when wanted. This narrowness is the same
judgement the Slice 1 open-point-emphasis refinement made: mark the exception,
not the rule.

The render mechanism is unchanged and generic — it picks these classes up the
moment the field exists on them.

**Known limitation:** the badge is canvas-only; the DXF/SVG **export** path
(`buildDiagramExport`) is unchanged, so an exported diagram won't show the badge
yet. Deferred with Slice 3.

## Slice 2 — Connectivity-derived straps (direct device→device edges)

Re-introduce an auto-drawn wire for a `feature_connectivity` row **only** when it
is *not* already represented by a placed conductor — the strap between a bus and
a selector isolator, a coupler breaker and its buses, etc.

- **web/src/components/map/diagram-canvas.tsx** — add a `StrapRender` derived
  type, shaped exactly like the existing `ControlLineRender` (Phase 46 Slice 2):
  - Source: each placement's already-fetched `/connectivity` rows.
  - Emit a strap **iff both endpoints are placed on this diagram AND neither the
    edge nor either endpoint is a `geometry_type='line'` conductor already
    rendered as a wire** (the dedupe guard — this is the phantom-elbow landmine
    the original removal was about; get it wrong and every connectivity row
    double-draws).
  - Route with the existing `elbowPath` right-angle router; anchor at the two
    devices' glyph anchor points. Display-only, not draggable (revoke the
    connection in the feature panel to remove it) — same contract as a conductor
    wire and a derived control line.
- Thin, solid, power-coloured (distinct from the dashed control lines) so a
  strap reads as "hard bus connection," not a signal.

## Slice 3 (later) — live energization + `double_busbar` template

Out of scope for the first cut, tracked here so the arc is visible:

- **Live energization symbology** — drive the same Slice 1 overlay hook off a
  powerflow run (which bus actually feeds each feeder) instead of the declared
  field. Turns the declared one-line into a *live* one-line. Needs powerflow
  results piped into the canvas.
- **`double_busbar` fan-out template** — create 2× `MV_BUSBAR` + coupler
  `MV_BREAKER` + per-feeder selector `MV_ISOLATOR`s + connectivity in one action,
  reusing the shared assembly fan-out spine (see `substation-assembly-fanout-duplication`).
  Plus optional auto-placement into a fresh diagram (there is no schematic
  auto-router today; `nextPlacementPosition` only spreads glyphs left-to-right).

## What this does NOT change

- No migration, no metadata schema change, no engine change — both slices are
  client-only overlays over already-fetched per-placement data.
- Symbology resolution itself is untouched; Slice 1 recolours the resolved glyph,
  it does not add field-conditional symbology rows.
- The double-busbar topology is already *expressible* (2× `MV_BUSBAR` + coupler
  + selector isolators, rules C/D — see `double-busbar-expressible`); this phase
  only makes it *legible* on the diagram.

## Verification owed

- Bash/tsc blocked in this environment (`bash-tool-blocked`) — static review
  only; a live `pnpm build` + visual check owed.
- A seed exercising an open vs closed selector on the same bay (the selection
  flip) is the proof Slice 1 renders the difference; the coupler open/closed flip
  is already proven for the trace engine (`phase-bus-coupler`).
