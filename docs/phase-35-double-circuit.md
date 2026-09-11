# Phase 35 — Double-circuit structures

A single support structure — a pole, tower, or pylon — often carries **two
independent circuits**: e.g. 11 kV Feeder 1 on the left arm and 11 kV Feeder 2
on the right. This phase is about **depicting** that, because the data model
already expresses it.

## The data model needs no change

A double-circuit pole is **two conductor runs sharing one structure**, not one
thing carrying two:

- **Two `MV_CONDUCTOR` edges** — Feeder 1 and Feeder 2 are each their own
  line/edge geometry with its own topology and feeder identity.
- **One shared `POLE`** (point, `network_role='none'`).
- Linked by the seeded relationship **`POLE supports MV_CONDUCTOR`, kind
  `spatial_support`, cardinality `many_to_many`**
  ([relationship_example.sql](../seed/relationship_example.sql)). `many_to_many`
  is doing the work: one pole supports many conductors; each conductor is
  supported by many poles.

Because **`POLE` is `network_role='none'` it is not a traced node**, so sharing
a structure does **not** connect the two feeders electrically. They stay
topologically separate — a feeder trace on one never leaks onto the other. The
pole is a coincidence of geography, not a junction.

## Depiction — decided with the user

The plan view and the schematic are two different problems, solved two ways.

### Map — Option B: offset the geometry per run

Snapping is *purely geometric — two vertices made coincident — and never
implies connectivity* ([snapping_rules_example.sql](../seed/snapping_rules_example.sql)),
and the map's snap is a generic snap-to-any-visible-vertex digitizing aid. The
`POLE ↔ conductor` link is `spatial_support`, **established explicitly**, so a
conductor vertex is **not required** to sit exactly on the pole centroid. That
frees us to place the two runs apart.

So each conductor run is **digitized to its own side of the pole line** (a small
real perpendicular offset, closer to reality anyway — a conductor attaches at a
crossarm position, which genuinely *is* offset from the pole centre). The offset
applies to the whole run, so Feeder 1 runs parallel to the left of the pole line
and Feeder 2 to the right — two clean parallel lines the length of the shared
route, not just split dots at each pole.

Chosen over **Option A (render-time offset, coincident geometry)** because B is
WYSIWYG, survives export (KMZ/DXF), and needs no label rewrite — see below.

### Map annotation — `circuit_ref` labels ride the offset runs

The map's labels are a metadata-driven per-class config
([`label_def`](../migrations/0055_add_feature_class_label_def.sql), a `{field}`
template). Line labels render with OL `placement:"line"` — the text rides the
conductor's own centerline ([map-layers.ts](../web/src/lib/map-layers.ts)).
Because the geometry is already offset (Option B), **each run's label separates
for free**: F1's label rides the left line, F2's the right. No perpendicular
label offset is needed — which matters because line labels *can't* be pushed
perpendicular today (`offsetY` is forced to `0` for `placement:"line"`); that is
precisely why B beats A (A's coincident geometry would stack the labels).

### Schematic — Option C: pole-head detail (later slice)

Two circuits sharing a pole line **are** coincident in plan — that's
geographically honest. The true left-arm/right-arm split belongs on a **generic
pole-head schematic**, where each circuit renders as a labeled block. Generic
representation only (per the standing "a generic representation will do" call).

## Slice 1 — the `circuit_ref` field (code-complete)

[seed/circuit_ref_example.sql](../seed/circuit_ref_example.sql) (task
`seed-circuit-ref`, then `compile CODE=MV_CONDUCTOR` + `CODE=LV_CONDUCTOR`):

- Adds **`circuit_ref`** — nullable free text, no domain, existing `electrical`
  group — to both `MV_CONDUCTOR` and `LV_CONDUCTOR`. It is the human circuit
  identity/tag ("Feeder 1", "F2"), the same shape as `bay_ref` / `chamber_ref`.
  No `subtype_field_rules` row: optional for every subtype (a run need not
  belong to a named circuit).
- Sets a **guarded demo `label_def`** (`{circuit_ref}`, halo, `min_zoom` 15) on
  both classes, only where `label_def IS NULL`, so an already-authored label is
  never clobbered. A blank `circuit_ref` renders no text — single-circuit
  networks stay label-free; only tagged shared-structure runs show F1/F2.
- Picked up automatically by the metadata-driven create form and generic CRUD —
  **no API/UI code**. Recompile is a non-destructive `ALTER TABLE ADD COLUMN`.

### Why no `circuit_side` field

The physical left/right offset lives in the **digitized geometry** (Option B),
not in an attribute — so the map needs no side column, and the schematic can
place circuits by drawing order / diagram placement. A `circuit_side`
(LEFT/RIGHT/CENTER) would only be needed if the map ever switched to render-time
offset (Option A), where the direction can't be read from coincident geometry.
Deliberately deferred until (and unless) that need appears.

## Slice 2 — pole-head schematic (planned)

A generic **pole-head block** on the diagram canvas: each circuit sharing a
structure rendered as a labeled block (keyed on `circuit_ref`) on its arm — the
Option C detail view.

## Verification owed (Bash blocked here — run locally)

- `task seed-circuit-ref`, then `task compile CODE=MV_CONDUCTOR` and
  `task compile CODE=LV_CONDUCTOR`; confirm `gis.mv_conductor` and
  `gis.lv_conductor` gain a nullable `circuit_ref text` column with no other
  change, and that both classes now publish a `label_def` on
  `/feature-classes/{code}/schema`.
- In the app: tag two overlapping conductor runs `Feeder 1` / `Feeder 2`,
  digitize them offset to either side of the pole line, and confirm each line
  self-labels on its own side at pole zoom.
- api: `go build ./...`, `go vet ./...` (no Go changed, for completeness).
