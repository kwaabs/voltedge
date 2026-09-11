# Phase 46 — Control / trip lines (dual line semantics)

## Why

The Phase 5 diagram canvas has one long-open gap left on its "What's still
open" list (docs/phase-5-diagrams.md): **dual line semantics — power wiring
vs. dashed control/trip lines.** Every wire the canvas draws today is either a
real `feature_connectivity` power edge (a conductor, resolved from its own
endpoints) or a decorative `line`/`bus`/`tap`. A protection scheme also needs
**signal** lines — a relay's dashed, arrowheaded trip line to the breaker it
opens, or its dashed sense line back from a CT/VT. Those are **not power
connectivity at all**; they are schematic-only annotations, and the model had
no place for them.

(The other three items on that stale list — multi-segment bus routing, an ANSI
symbol library, and grouping/annotation boxes — were already delivered by
Phase 24's `bus`/`tap`, `symbol`, and `box`/`text` element kinds. Phase 46 is
the genuine remainder.)

The decision (confirmed with the user): fill this **both** ways.

- **Slice 1 — freeform `control` furniture.** A hand-drawn dashed,
  arrowheaded line the operator draws by clicking source → target, exactly like
  the existing `line` kind but born dashed and rendered with an arrowhead. Pure
  diagram furniture, backed by no feature.
- **Slice 2 — derived control lines from relationship data.** The canvas
  additionally *derives* the same dashed-arrow lines automatically from real
  `protection_trip` / `protection_sensing` relationships (migration 0075,
  Phase "protection") between two placed features, so a correctly-related relay
  and breaker draw their trip line with no hand-drawing at all.

Both render identically (dashed polyline + arrowhead), so a diagram reads the
same whether a signal line is authored by hand or falls out of the data.

## Slice 1 — the `control` element kind

A control line is line-shaped, path-carrying furniture — the same storage the
`line`/`bus`/`tap` kinds already use — so it needs **no new column**, only the
kind CHECK widened, exactly as migration 0047 (`symbol`) and 0050 (`bus`/`tap`)
did before it.

- **migrations/0084_add_control_element_kind.sql** — drops and recreates
  `runtime.diagram_elements`'s auto-named `diagram_elements_kind_check` with
  `'control'` added; updates the column COMMENT. Existing rows unaffected (every
  current value is still permitted).
- **api/internal/diagram/types.go** — `'control'` added to both the
  `elementKinds` accept-set (mirrors the DB CHECK; a bad kind is rejected with a
  clear message before it reaches the constraint) and the `pathKinds` subset (a
  create request must carry a `path` of ≥2 points — there's nothing to draw
  otherwise). No handler change: `createElement` already handles every
  `pathKind` generically.
- **web/src/hooks/use-diagrams.ts** — `"control"` added to the
  `DiagramElementKind` type union.
- **web/src/components/map/diagram-canvas.tsx** —
  - An **Add → Control line** menu entry (Spline icon) starts an
    `elementDrawKind = "control"` draw. It draws freehand like `line` (the two
    ortho-snap `forceOrtho` checks both exempt `control`, same as `line`), and
    on finish the element is **born dashed**: `style = { stroke:
    CONTROL_LINE_COLORS.control, dash: true }`.
  - A dedicated **render branch** for `kind === "control"`: dashed polyline,
    `markerEnd` pointing at the shared arrowhead marker, draggable via the same
    element pointer handlers, optional text label, removable.
- **web/src/lib/diagram-dxf.ts** — a `control` export case: a hand-drawn
  control line exports as a **dashed polyline on the annotations layer** (DXF
  has no arrowhead primitive in this writer; the dash carries the "signal, not
  power" reading, and the on-canvas arrowhead is cosmetic).

## Slice 2 — derived control lines from relationships

No schema change and no new endpoint: a **pure client render** over the
existing `GET /relationships?feature_class=&feature_id=`, mirroring exactly how
`conductorItems` is derived client-side from per-placement `/connectivity`.

In **diagram-canvas.tsx**:

- `relationshipResults = useQueries(...)` fetches `/relationships` for every
  placed feature (queryKey `["relationships", class, id]`, reusing the same key
  the feature panel's Relationships tab uses, so the cache is shared).
- `CONTROL_LINE_KINDS = new Set(["protection_trip", "protection_sensing"])`
  is the derived-line filter; `CONTROL_LINE_COLORS` maps each kind (plus the
  freeform `control`) to a stroke colour — `protection_trip` = destructive/red,
  `protection_sensing` = blue, freeform `control` = muted-foreground.
- `controlLineItems` memo dedupes by relationship id (a relationship comes back
  on *both* endpoints' fetches), keeps only `CONTROL_LINE_KINDS`, resolves both
  endpoints through `placementByKey`, and **skips** a line whose either endpoint
  is not placed on this diagram or is in a hidden class. Deps are the join of
  the query `dataUpdatedAt`s plus `placementByKey`/`hiddenClassSet`.
- A **display-only** render block (`pointerEvents: none`) draws each derived
  line as a dashed `elbowPath` between the two placements' anchors, with the
  colour + arrowhead for its kind and a midpoint label showing the relationship
  class name.

### Arrowheads

One SVG `<marker>` per `CONTROL_LINE_COLORS` slug lives in an always-present
`<defs>` (`viewBox="0 0 10 10"`, `markerUnits="strokeWidth"`,
`orient="auto-start-reverse"`, a filled triangle). Marker ids are namespaced by
the open diagram id (`diagram-arrow-${openDiagramId}-${slug}`) so two open
canvases never collide. Both the freeform `control` element and the derived
lines reference these same markers by `markerEnd`.

## What Phase 46 deliberately does NOT do

- **No derived-line DXF export.** The DXF writer exports the `elements` model
  (so the hand-drawn `control` element *is* exported); the *derived* protection
  lines are not part of that model and are not threaded into the export. They
  are a live render over relationship data — export them only if a real need
  appears (would mean passing `controlLineItems` into `buildDiagramDxf`).
- **No new relationship kinds.** It consumes `protection_trip` /
  `protection_sensing` exactly as the protection phase defined them.
- **No engine involvement.** A control/trip line is schematic annotation; it is
  never a `feature_connectivity` edge and never affects trace/powerflow.

## Verification owed (Bash-blocked here; run locally)

- [ ] `migrations/0084` applies cleanly; the CHECK now admits `'control'` and
      still admits every prior kind.
- [ ] `go build ./...` (api) — `types.go` change compiles.
- [ ] `POST /diagrams/{id}/elements` with `kind:"control"` + a ≥2-point `path`
      succeeds; the same without a `path` returns 400 (pathKinds guard).
- [ ] web `tsc` / lint / build clean.
- [ ] Canvas: **Add → Control line** draws a dashed, arrowheaded line by
      clicking source then target; it's draggable and removable; it survives a
      reload (persisted as an element).
- [ ] Canvas: two features related by `protection_trip` (and by
      `protection_sensing`), both placed on the diagram, auto-draw a dashed
      arrowed line in the right colour (red / blue) with the relationship name
      at its midpoint; hiding either endpoint's class removes it; it is
      display-only (no pointer capture).
- [ ] The hand-drawn `control` element exports to DXF as a dashed polyline on
      the annotations layer.
- [ ] RBAC unchanged: element create/delete still gated exactly as the other
      element kinds are.
