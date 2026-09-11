# Phase 31 — Per-placement diagram label transform

Until now a feature placed on a diagram got a name label pinned to a hardcoded
spot (below a point glyph, at a conductor's midpoint) and the only thing you
could do to it was hide it (`label_hidden`, migration 0054). This phase makes
that label **repositionable, rotatable and resizable per placement**, while it
still travels with its glyph.

## Design

- **One transform per placement**, stored on `runtime.diagram_placements`
  (migration 0056): `label_dx`, `label_dy` (offset from the label's natural
  anchor), `label_rotation` (degrees), `label_scale` (font-size multiplier).
  Defaults `0/0/0/1` reproduce the old pinned position, so every existing label
  is unchanged.
- **Offset follows the glyph** (user's choice): `label_dx/label_dy` live in the
  glyph-LOCAL frame. A point label is rendered inside the glyph's own
  `rotate()`, so moving or rotating the feature carries the label along at the
  same relative position. A conductor has no glyph rotation, so its offset is
  used as-is from the midpoint anchor.
- **Geometry-free, owned by PATCH** — like `label_hidden`, the transform is
  written only by `PATCH /diagrams/{id}/placements/{placementID}`, never the
  place/move upsert. So arranging a label never disturbs the feature, and
  dragging/rotating the feature never resets the label.
- **Interaction**: reposition is a **direct drag** of the label (only while the
  placement is selected). Rotate and resize are **discrete steppers** in a
  small hover toolbar under a selected label (rotate ±15°, size ±) — steppers
  read cleanly on a schematic and avoid fragile through-transform drag math.

## Storage — migration 0056

`ALTER TABLE runtime.diagram_placements ADD COLUMN label_dx / label_dy /
label_rotation double precision NOT NULL DEFAULT 0, label_scale double
precision NOT NULL DEFAULT 1`. NOT NULL + defaults, so existing rows backfill to
the pinned default.

## Backend

- **api/internal/diagram/sql.go** — `DiagramPlacement` gains `LabelDX/LabelDY/
  LabelRotation/LabelScale`. `placeFeature` sets `LabelScale: 1` on a fresh
  insert; the conflict clause still only updates `x/y/rotation/path`, so an
  arranged label survives a re-place. New `toPlacementResponse` helper is the
  single source of truth for the wire shape (place/update/list all use it, so
  they can't drift on the label columns). `updatePlacement` now accumulates the
  changed columns and issues one `Column(cols...)` update (or none) — every
  request field is a pointer, so an omitted field is a no-op.
- **api/internal/diagram/types.go** — `placementResponse` always emits the four
  label fields; `updatePlacementRequest` gains four `*float64` pointers
  alongside the existing `*bool` `LabelHidden`.
- **handler.go** decodes the request generically — no change needed.

## Frontend

- **web/src/hooks/use-diagrams.ts** — `DiagramPlacement` gains optional
  `label_dx/label_dy/label_rotation/label_scale`.
- **web/src/components/map/diagram-canvas.tsx**:
  - `patchLabel(placement, patch)` — optimistic PATCH of the transform fields,
    mirroring `toggleLabelHidden`.
  - `LabelDragState` + `handleLabelPointerDown/Move/Up` — drag a selected
    label; the screen-space delta is inverse-rotated by the glyph rotation
    (`Rz(-θ)`) into the glyph-local frame the offset is stored in (a conductor
    passes rotation 0). Only a real move persists.
  - `rotateLabel` (±15°, wrapped) / `scaleLabel` (×factor, clamped 0.5–3,
    rounded) → `patchLabel`.
  - `LabelAdjustControls` — hover toolbar (↺ ↻ − +) rendered in the un-rotated
    frame under a selected label, so the buttons never spin.
  - Both label render sites (point placement, conductor midpoint) wrap the
    `<text>` in `translate(dx, anchorY+dy) rotate(label_rotation)`, scale the
    font by `label_scale`, and make the text draggable + show the toolbar only
    while selected.

## Verification owed (Bash blocked here)

- web: `tsc --noEmit`, lint, `vite build`.
- api: `go build ./...`, `go vet ./...`, `gofmt -l`.
- migrate: run 0056.
- manual: select a point placement → drag its label, rotate ±15°, resize;
  rotate the glyph and confirm the label offset follows it; do the same on a
  conductor (no glyph rotation); confirm a feature move/re-place doesn't reset
  an arranged label; hidden labels show no drag target or toolbar.
