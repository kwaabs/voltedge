# Phase 44 — Cable management & civil infrastructure (joints, ducts, trenches, chambers)

**Status:** design locked 2026-09-09. S1 + S2 + S3 + S4 code-complete
(verification owed).

## Why

VoltEdge models the **electrical** network well — cables already exist as the
`underground_*` subtypes of `MV_CONDUCTOR` / `LV_CONDUCTOR` with `'cable'`-
category `material_specs` (impedance, ampacity, thermal). What is **missing** is
everything the cable physically lives in and the accessories that join it:

- **Joints** — a real cable run is spliced. Today two `underground` cable
  segments meeting at a splice are modelled (if at all) as one continuous edge
  or two edges sharing a vertex; the splice itself is not an asset, has no
  condition/inspection record, and cannot be traced-to.
- **Ducts** — the buried conduit a cable is pulled through. No class exists, so
  there is no way to record duct material/bore, no **spare-way capacity**, and
  no "which cables share this duct" (a dig-once / mutual-heating question).
- **Trenches** — the civil excavation that carries ducts (or direct-buried
  cable). No class, so no route/depth/surface-reinstatement record.
- **Chambers** — manholes / jointing pits / footway boxes where joints live and
  cables are pulled. No class, so joints float with no housing.

This phase adds the **civil / cable-management layer** as a *parallel graph*
alongside the electrical one, plus the **cable joint** as a first-class network
junction.

## Two axes, deliberately kept apart

The model splits along the same seam the summary of every prior structure phase
uses (`POLE`/`TOWER` are `network_role='none'` support furniture; only apparatus
is traced):

| Axis | Role | Traced? | Classes |
|---|---|---|---|
| **Electrical** | `junction` | yes (zero-Z pass-through) | `MV_CABLE_JOINT`, `LV_CABLE_JOINT` |
| **Civil** | `none` | no | `CABLE_TRENCH`, `CABLE_DUCT`, `CABLE_CHAMBER` |

The two are coupled **loosely, by relationship** (locked decision) — the civil
features do **not** sit inline on the electrical graph. A duct *carries* a cable
via a `relationship_class` row, exactly the way `TOWER supports MV_CONDUCTOR`
couples a support to a span without ever joining it electrically
(`seed/tower_relationship_example.sql`, `docs/phase-35-double-circuit.md`). This
keeps the civil inventory answerable ("what's in this duct", "spare ways here")
without letting a trench or duct alter a trace or a load flow.

## The electrical axis — cable joints (junctions)

`MV_CABLE_JOINT` / `LV_CABLE_JOINT`, one per network (the `MV_SWITCH`/`LV_SWITCH`
split precedent, `network` col from migration 0064).

- **Geometry:** `point`, SRID 4326.
- **`network_role='junction'`** — a zero-impedance pass-through node, like a busbar
  or an LV `METER`'s through-path. Splicing a run at a joint turns one implicit
  edge into two **real traced sections** with the joint as the assetized splice
  between them — the trace/section/isolation engines already treat junctions as
  pass-through, so nothing in the engine changes.
- **Terminals:** `T1`, `T2` (straight / transition / termination); `T1`, `T2`,
  `T3` for `branch` (a tee joint). Terminal codes are subtype-scoped exactly as
  `MV_SWITCH` does its `T1`/`T2`.
- **Subtypes:** `straight` (in-line splice), `branch` (tee — 3 terminals),
  `transition` (e.g. paper→XLPE, or cable→OH pothead), `termination` (sealing
  end — a one-terminal leaf; still `junction` so a trace lands on it).
- **Fields:** `insulation_type` (coded — reuse/create a cable-insulation domain:
  XLPE / EPR / PILC / heat-shrink), `condition_rating` (shared `CONDITION_RATING`),
  `install_date` (date, nullable), `last_inspected_at` (date, nullable).
- **network_rules (feed-through):** `MV_CONDUCTOR -> MV_CABLE_JOINT T1`,
  `MV_CABLE_JOINT T2 -> MV_CONDUCTOR` (+ `T3 -> MV_CONDUCTOR` for branch); the
  `LV_*` mirror. Same conductor-in / conductor-out shape a switch uses.

Because the joint is `junction`, it can also be **contained** in a chamber (see
civil axis) via a `containment` relationship — the joint is *where* the cables
are pulled and spliced, the chamber is the box it sits in.

## The civil axis — trench / duct / chamber (none-role inventory)

All three are `network_role='none'` — pure asset inventory + a civil graph, never
traced, never on the electrical path. Modelled on the `TOWER`/`POLE` idiom
(`seed/tower_example.sql`): a lean field set, shared `CONDITION_RATING` +
`install_date` + `last_inspected_at`, class-specific domains for the rest.

### CABLE_TRENCH — the excavation (line)
- `line`, SRID 4326, `none`.
- **Fields:** `surface_type` (coded `TRENCH_SURFACE` — road / footway / verge /
  unmade), `depth_mm` (integer, nullable), `width_mm` (integer, nullable),
  `condition_rating`, `install_date`, `last_inspected_at`.
- A trench *carries* ducts, or *carries* a direct-buried cable (see routing).

### CABLE_DUCT — the conduit (line)
- `line`, SRID 4326, `none`.
- **Fields:** `duct_material` (coded `DUCT_MATERIAL` — PVC / HDPE / steel /
  earthenware), **`way_count`** (integer, not null, default 1 — the number of
  bores/ways this duct provides; the coarse capacity number), `bore_diameter_mm`
  (integer, nullable), `condition_rating`, `install_date`, `last_inspected_at`.
- A duct *carries* cables (`MV_CONDUCTOR`/`LV_CONDUCTOR`); a duct is itself
  carried by a trench.

### CABLE_CHAMBER — the housing (polygon)
- `polygon`, SRID 4326, `none` (a manhole footprint reads as a small polygon on
  the map; `geometry_optional` so one can be dropped without a survey outline).
- **Subtypes:** `manhole`, `footway_box`, `joint_bay`, `jointing_pit`.
- **Fields:** `condition_rating`, `install_date`, `last_inspected_at`, plus
  `access_cover_type` (coded `CHAMBER_COVER` — light / medium / heavy duty) —
  small, matches the lean furniture pattern.
- A chamber *contains* cable joints (`containment` relationship).

## Coupling — the `cable_routing` relationship kind (one widen migration)

The civil→electrical and civil→civil links need a `kind` that isn't any of the
existing ones (`spatial_support` is for overhead structural support; `containment`
is enclosure; these are "this cable is routed through this conduit"). Add **one
new kind `cable_routing`** the same way 0075 (`protection_*`) and 0080
(`metering_sensing`) were added: drop `relationship_classes_kind_check`, re-add
the superset including `cable_routing`, **no data rewrite**. Go + web
`validRelationshipKinds` / `RELATIONSHIP_KINDS` widened to mirror the CHECK.

Relationship classes seeded on that kind:

| origin | kind | destination | cardinality |
|---|---|---|---|
| `CABLE_DUCT` | `cable_routing` | `MV_CONDUCTOR` | many_to_many |
| `CABLE_DUCT` | `cable_routing` | `LV_CONDUCTOR` | many_to_many |
| `CABLE_TRENCH` | `cable_routing` | `CABLE_DUCT` | many_to_many |
| `CABLE_TRENCH` | `cable_routing` | `MV_CONDUCTOR` | many_to_many (direct-buried) |
| `CABLE_TRENCH` | `cable_routing` | `LV_CONDUCTOR` | many_to_many (direct-buried) |

`many_to_many` throughout: a duct carries several cables and a cable passes
through several ducts along its route; a trench holds several ducts; direct-
buried cable can share a trench.

Chamber→joint uses the **existing `containment` kind** (no migration needed):

| origin | kind | destination | cardinality |
|---|---|---|---|
| `CABLE_CHAMBER` | `containment` | `MV_CABLE_JOINT` | one_to_many |
| `CABLE_CHAMBER` | `containment` | `LV_CABLE_JOINT` | one_to_many |

## Capacity — coarse first (locked decision)

Duct-way occupancy is tracked **coarsely**, with **no schema change beyond the
kind-widen migration**:

- **Capacity** = `CABLE_DUCT.way_count` (a plain field).
- **Occupancy** = `COUNT` of `cable_routing` relationship rows whose origin is
  this duct and destination is an `MV_CONDUCTOR`/`LV_CONDUCTOR` (i.e. cables, not
  the trench that carries the duct — that link runs the other direction).
- **Spare ways** = `way_count − occupancy`, clamped at 0.

This deliberately treats "a cable occupies one way" as the unit and does **not**
model *which* way a cable is in — `runtime.feature_relationships` has no
attribute columns, so per-way assignment can't ride on a relationship instance
without new schema. That precision is deferred to the optional Slice 4
(`DUCT_WAY` sub-entities). Coarse spare-way count answers the operational
question ("can I pull another cable here?") for free today.

## Slice plan

- **S1 — civil inventory (IN PROGRESS):** `seed/cable_management_example.sql` =
  the three `none`-role classes `CABLE_TRENCH` (line), `CABLE_DUCT` (line, with
  `way_count`), `CABLE_CHAMBER` (polygon, subtypes) + their domains
  (`TRENCH_SURFACE`, `DUCT_MATERIAL`, `CHAMBER_COVER`) + fields + subtype rules,
  and `seed/cable_management_symbology.sql` giving all three their mandatory
  `map` default rule (publish gate). No new relationship kind, no migration, no
  engine touch — pure inventory. New geometry classes surface in the map
  Add-menu and draw generically (point/line/polygon draw is metadata-driven).
  `task seed-cable-management` + `task seed-cable-management-symbology`, then
  `task compile CODE=CABLE_TRENCH` / `CABLE_DUCT` / `CABLE_CHAMBER`.
- **S2 — cable joints (junctions) (CODE-COMPLETE):**
  `seed/cable_joint_example.sql` = `MV_CABLE_JOINT` + `LV_CABLE_JOINT`
  (`junction`, per-network), subtypes (straight/branch/transition/termination),
  terminals (T1/T2 **generic** subtype_id-null + T3 **branch-scoped** — resolves
  per-code via `metadata/load.go ResolveTerminal`, so a branch joint sees T1/T2/T3
  and the others see T1/T2), fields (insulation_type/condition_rating/install_date/
  last_inspected_at), and the feed-through `network_rules`
  (`{MV,LV}_CONDUCTOR -> joint T1`, `-> T2`, `joint T2 -> conductor`, plus
  `-> T3` / `T3 -> conductor` for the branch tee). Terminal roles are
  primary/secondary/**other** (T3) — deliberately avoids 0070's `tertiary` (a tee
  tap is not a winding), so no migration. `containment` relationship classes
  `CABLE_CHAMBER contains {MV,LV_CABLE_JOINT}` (existing kind, no migration).
  `seed/cable_joint_symbology.sql` = the mandatory map default (deep-teal / slate
  splice dots, no diagram rule). `task seed-cable-joint` +
  `seed-cable-joint-symbology`, then `task compile CODE=MV_CABLE_JOINT` /
  `LV_CABLE_JOINT`. Verify a DOWNSTREAM trace passes through a joint
  (pass-through) and that splicing a run at a joint yields two sections.
- **S3 — capacity + routing (CODE-COMPLETE):**
  `migrations/0082_widen_relationship_kind_cable_routing.sql` widens
  `relationship_classes_kind_check` to add **`cable_routing`** (mirrors 0075 /
  0080, drop-and-re-add superset + updated COMMENT, no data rewrite);
  `seed/cable_routing_example.sql` = the five `cable_routing` relationship
  classes above (all `many_to_many`, NOT EXISTS-guarded); Go
  `validRelationshipKinds` (`api/internal/admin/rules.go`, incl. the invalid()
  400 message) + web `RELATIONSHIP_KINDS`
  (`web/src/routes/admin-feature-class-detail.tsx`) widened to mirror the CHECK.
  **Spare-way computation:** `GET /features/{code}/{id}/capacity` →
  `{way_count, occupied, spare}` (`api/internal/crud/capacity.go` `ductCapacity`
  + `capacityHandler`/route in `handler.go`, riding the `/features` mount's
  `features:read` gate) — 404s a missing row via `getFeature`, 400s a class with
  no `way_count` field (stays honest for a non-duct), counts `cable_routing`
  rows whose ORIGIN is the duct (a raw `runtime.feature_relationships` join, NOT
  the `relationship` model — that package imports `crud`, so importing it back
  would cycle; the trench→duct link has the duct as DESTINATION so it's excluded
  naturally). Web: `web/src/hooks/use-duct-capacity.ts` (`useDuctCapacity`,
  `["duct-capacity", code, id]` key, enabled only when the feature carries
  `way_count`) + a "Spare ways — N of M free (K cables routed)" row on the
  feature-panel AttributesTab; `connect-confirm-panel.tsx` invalidates the
  duct-capacity key on a relationship establish so the count refreshes after a
  cable is assigned. The **assign-cable-to-duct** action is the *existing*
  relationship-establish UI (select duct → New relationship → pick conductor →
  kind `cable_routing`) / MCP `establish_relationship` — no new UI. No new
  feature class.
- **S4 — per-way `DUCT_WAY` sub-entities (CODE-COMPLETE):** a `DUCT_WAY` child
  (`geometry_type='none'`, `network_role='none'`, single `way_number` field, no
  symbology / subtypes / terminals — `seed/duct_way_example.sql`, `task
  seed-duct-way` + `compile CODE=DUCT_WAY`) contained in `CABLE_DUCT` so a
  cable's routing targets a specific bore, giving true per-way occupancy.
  **Templateless, count-driven fan-out** (decided — ways are homogeneous, unlike
  panel/RMU slots, so no template table): `POST /ducts` (`api/internal/duct`
  package — `fanout.go` `CreateDuct` + `http.go` routes + `options.go`
  `ListDuctOptions`, mounted in `cmd/server/main.go` under `features:read` with an
  inner `relationships:commit` gate on POST) reads the duct's `way_count`
  attribute (1..96), then in ONE `RunInTx` creates the duct via shared
  `crud.CreateFeature` and loops `way_number` 1..N creating each `DUCT_WAY` +
  `CABLE_DUCT contains DUCT_WAY` containment (`relationship.EstablishRelationship`).
  `/ducts/options` returns a bare array of duct classes that have a `contains
  DUCT_WAY` containment class (mirrors `/rmus/options`). **Routing kept BOTH
  coarse and per-way** (decided): `seed/duct_way_example.sql` adds `DUCT_WAY
  carries {MV,LV}_CONDUCTOR` `cable_routing` classes AND the S3 `CABLE_DUCT`→
  conductor classes stay; `ductCapacity` (`capacity.go`, rewritten) counts a
  duct's `DUCT_WAY` children (containment join) and, if any exist, returns
  `per_way:true` + a `ways[]` breakdown (each `{feature_id, way_number, occupied}`,
  `occupied` = an `EXISTS` `cable_routing` subquery on the way), else falls back to
  the S3 coarse `cable_routing`-on-duct count (`per_way:false`, no `ways`). Web:
  `use-ducts.ts` (`useDuctOptions`, `CreateDuctResult`), draw-store `duct` draft
  variant + `startDuctDrawing`, feature-create-form `POST /ducts` branch,
  new-feature-menu "Cable duct" section (data-driven gate off `/ducts/options`,
  same as RMU), `use-duct-capacity.ts` gains `per_way`/`ways`, feature-panel
  "Spare ways" row shows a per-bore chip grid (filled = routed, outlined = free)
  when `per_way`. Because `DUCT_WAY` is geometry-less it's unclickable on the map,
  so the duct panel's `ways[]` breakdown is the reach into the children.

## Decisions locked

- **Loose coupling by relationship**, not inline civil-on-electrical-graph — the
  civil layer never alters a trace/load-flow (the `TOWER` precedent).
- **Coarse capacity first** — `way_count` field − `COUNT(cable_routing)`; no
  per-way modelling until S4.
- **Cable joint is a `junction`**, not `none` — it *is* on the electrical path
  (it splices the cable), unlike the civil furniture, so a trace lands on / passes
  through it and the splice becomes a real asset between two real sections.
- **One new relationship kind (`cable_routing`)** for civil→cable + trench→duct;
  reuse `containment` for chamber→joint.

## Precedents to copy verbatim

- `seed/tower_example.sql` — `none`-role furniture class, lean shared fields,
  subtype rules, ON CONFLICT idempotency.
- `seed/tower_relationship_example.sql` — a `relationship_classes` row between
  two existing classes on a non-default kind, `many_to_many`, NOT EXISTS guard.
- `seed/mv_metering_example.sql` — per-network junction class with subtype-scoped
  terminals + feed-through `network_rules` (the joint's shape).
- `migrations/0075` / `0080` — the drop-and-re-add-CHECK widen for a new
  relationship `kind` (the `cable_routing` migration in S3).
- `seed/mv_metering_symbology.sql` / `seed/symbology_example.sql` — map default
  rules; line = `{color,width}`, polygon = `{fill_color,stroke_color}`, point =
  `{icon_ref,color}`.

## Verification owed (Bash EPERM here — user runs)

S1: `task seed-cable-management` + `task seed-cable-management-symbology`;
`task compile CODE=CABLE_TRENCH` / `CABLE_DUCT` / `CABLE_CHAMBER`; confirm
`gis.cable_trench` / `gis.cable_duct` / `gis.cable_chamber` tables exist; the
three classes appear in the map Add-menu and can be drawn (line/line/polygon)
and published (map default present); `way_count` persists on a duct.

S2: `task seed-cable-joint` + `task seed-cable-joint-symbology`;
`task compile CODE=MV_CABLE_JOINT` / `LV_CABLE_JOINT`; confirm
`gis.mv_cable_joint` / `gis.lv_cable_joint` tables; connect a conductor into a
joint's T1 and out of its T2 and confirm a DOWNSTREAM trace walks through it
(zero-Z pass-through, joint appears as a node); a `branch` joint offers a third
terminal T3 and a straight/transition/termination joint does not; a joint can be
contained in a `CABLE_CHAMBER`; both classes publish (map default present).

S3: `task migrate` (applies `0082`) + `task seed-cable-routing`; confirm the
five `cable_routing` relationship classes exist and the kind CHECK now accepts
`cable_routing`. Establish a `CABLE_DUCT --cable_routing--> MV_CONDUCTOR` link
via the feature-panel relationship UI and confirm it succeeds (kind allowed).
`GET /features/CABLE_DUCT/{id}/capacity` returns `{way_count, occupied, spare}`
with `occupied` = number of cables routed and `spare` clamped ≥ 0; the duct
feature panel shows the "Spare ways — N of M free" row and it decrements/refreshes
immediately after assigning another cable; the endpoint 400s for a non-duct class
(no `way_count`).
