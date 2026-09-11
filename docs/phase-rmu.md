# Phase RMU — Ring Main Unit as an assembly of switched ways

## Problem

A Ring Main Unit (RMU) is a single map-placed unit, but electrically it is
**several independently-switched ways sharing one internal bus**: two ring
ways (the incoming and outgoing ring cables) plus one or more transformer
(tee) ways feeding distribution transformers. Operators switch each way
independently, and isolation has to come out right: opening a transformer
tee way must isolate only that transformer while the ring keeps feeding
through the bus.

Modelling the RMU as one junction would collapse all the ways into a single
node and make that isolation impossible to represent. So the RMU is modelled
as an **assembly**: a placed container that fans out into N way-devices, each
its own switch, all hanging off a shared internal bus node.

This reuses machinery that already exists — the Phase 42 composite fan-out
(`api/internal/panel`, a placed parent that creates a templated child set +
containment in one transaction) and the `device_state` OPEN trace barrier
(`seed/switching_device_example.sql`, `seed/mv_switching_example.sql`) — so
no new engine mechanism is needed, only new metadata plus one extension to
the fan-out (wiring internal connectivity, not just containment).

## Model of record

One dedicated feature class (`seed/rmu_example.sql`); the internal bus is a
plain **MV_BUSBAR** instance (`seed/mv_busbar_example.sql`) and each switched
way is a plain **MV_SWITCH** instance (`seed/mv_lv_switch_example.sql`) —
neither is a class of its own:

| Class | `network_role` | Geometry | Purpose |
|---|---|---|---|
| **RMU** | `container` | map-placed `point`, `is_diagram_container=true` | the placed unit; owns its ways; carries no topology itself. Like `MV_SWITCHBOARD` but with a real footprint (`point`, not `none`) — it's an outdoor pad-mounted unit. |
| **MV_BUSBAR** | `junction` | `geometry_optional` `point`, no terminals | the common internal bus every way's bus-side attaches to — a plain MV bus instance, not a dedicated RMU class. This node is what makes the ring a ring. |
| **MV_SWITCH** | `junction` | `geometry_optional` `point` | one switched way — a plain MV switch, not a dedicated RMU class. `device_state` (CLOSED/OPEN) + `condition_rating`; **T1** = cable/external side, **T2** = bus/internal side; OPEN = trace barrier. The DIST_SWITCH pattern. Ring vs. transformer (tee) way is carried by MV_SWITCH's **`is_fused`** flag (a fused tee way protects the distribution transformer), not a subtype. |

**Containment:** `RMU contains MV_BUSBAR` and `RMU contains MV_SWITCH`, both
`one_to_many` — the same containment `MV_SWITCHBOARD` uses for its panels.

**Topology the catalog permits** (the fan-out establishes it):

```
MV_CONDUCTOR <-> MV_SWITCH.T1     external cable to a way's cable side  (MV_SWITCH's own rules)
MV_SWITCH.T2 <-> MV_BUSBAR        a way's bus side to the internal bus  (the one leg this seed adds)
```

MV_SWITCH already ships the external `MV_CONDUCTOR <-> T1/T2` legs and the OPEN
trace barrier, so the RMU seed adds only the single internal
`MV_SWITCH.T2 -> MV_BUSBAR` rule (bus side terminal-less). No mirrored bus-side
rule is needed: connection establishment matches a rule in **either** direction
(`matchRuleEitherDirection`, `api/internal/connectivity/rules.go`), and trace
traversal walks the stored `feature_connectivity` edges, not the rules.

A 2+1 RMU therefore traces as: ring-in cable → wayA → bus → wayB → ring-out
cable, with bus → wayC(tee) → transformer cable. Open wayC and only the
transformer isolates; open wayA and the bus is still fed from the ring-out
side. Correct ring behaviour, for free, off the existing OPEN barrier.

## Slices

**Slice 1 — Catalog (`seed/rmu_example.sql`, `task seed-rmu`). CODE-COMPLETE.**
The one dedicated class (RMU; the bus is a plain MV_BUSBAR from
`seed/mv_busbar_example.sql` and the ways are plain MV_SWITCH from
`seed/mv_lv_switch_example.sql`) + the RMU_CONFIG/`rmu_config` field + the two
containment relationship classes + the single internal `MV_SWITCH.T2 ->
MV_BUSBAR` network rule (the ways' external legs, OPEN barrier and
DEVICE_STATE/CONDITION_RATING fields all come with MV_SWITCH). Pure metadata, no
engine change. Compile `RMU` after (MV_BUSBAR + MV_SWITCH are already compiled).

**Slice 2 — Templates + composite fan-out. CODE-COMPLETE.**
`metadata.rmu_templates` / `rmu_template_items` (migration 0073), the
panel-templates pattern keyed by (RMU class × `RMU_CONFIG` value), with two
additions panels never needed: the parent row names the single internal bus
class to create once, and each way item names its `bus_terminal` (T2). Seed
`seed/rmu_templates.sql` (`task seed-rmu-templates`) defines the `2W_1T` and
`2W_2T` recipes. `metadata.LoadRMUTemplate` resolves a recipe to
`{BusbarFeatureClass, []RMUTemplateWay}`. `POST /rmus`
(`api/internal/rmu`, mounted under the features gate + inner
`relationships:commit` + `connectivity:commit`) creates the placed RMU (the
one feature with map geometry + territory), then in one `db.RunInTx`: the
single bus + `RMU contains bus`, then per templated way create + `RMU
contains way` + `connectivity.EstablishConnection(way.T2 → bus)`. The
connection reads see the same uncommitted tx, so a failure anywhere rolls the
whole assembly back. External cable connections stay a normal post-placement
connect action.

A design note worth keeping: the internal bus is created with an **empty
input** by the fan-out, so its class must accept a bare node — no required
subtype (a class with subtypes requires one at create, `crud.validateSubtype`)
and no required fields. There used to be a dedicated attribute-less
`RMU_BUSBAR` class for exactly this; the **2026-09-06 collapse** folded it into
`MV_BUSBAR` (a busbar's real classifier is voltage, MV vs LV, not
standalone-vs-internal) by dropping MV_BUSBAR's air/gas subtype and making
`voltage_class` / `condition_rating` nullable — see
`seed/collapse_rmu_busbar_into_mv_busbar.sql` (`task collapse-rmu-busbar`) for
the one-off DB migration on already-seeded databases.

The switched way went the same way: there used to be a dedicated `RMU_WAY` class
(ring/transformer subtypes), but a way **is** a switch — byte-for-byte the
DIST_SWITCH pattern MV_SWITCH already is. The **2026-09-07 collapse** folded it
into `MV_SWITCH`, with the ring-vs-transformer subtype becoming MV_SWITCH's
`is_fused` flag (transformer tee way → `is_fused = true`; the fan-out stamps it
from the per-way template). The fan-out now creates each way as an MV_SWITCH
(subtype `standard`) and wires its T2 to the bus exactly as before — see
`seed/collapse_rmu_way_into_mv_switch.sql` (`task collapse-rmu-way`) for the
one-off DB migration, which moves every live RMU_WAY feature onto `gis.mv_switch`
keeping its uuid and remaps its T1/T2 connectivity by terminal code.

**Slice 3 — Frontend. CODE-COMPLETE.** An "Add RMU" map placement +
configuration picker + draft create-form that calls `POST /rmus`.

- `GET /rmus/options` (`api/internal/rmu/options.go`, `ListRMUOptions`) lists
  every `(RMU class, config)` that has a fan-out template — one addable RMU per
  row. It rides the mount's `features:read` gate (a GET). The list is empty
  until `seed/rmu_templates.sql` is applied, so the affordance simply doesn't
  appear without the catalog — and nothing in Go or the frontend hardcodes a
  single config code.
- `web/src/hooks/use-rmus.ts` (`useRMUOptions`) reads it; `RMUOption`,
  `CreateRMUResult`, `CreatedRMUWay` mirror the Go structs.
- The draw store (`draw-store.ts`) gains `startRMUDrawing(rmuClass, config)`,
  which draws a plain point but stashes `drawingRmuConfig`; on the map click
  `finishDrawing` marks the draft `rmu: { config }` and seeds
  `rmu_config`. Unlike the panel/split drafts an RMU draft **does** carry
  `geometry` (it's a placed unit), so the existing draggable-draft path handles
  it unchanged.
- `new-feature-menu.tsx` fetches the options, **excludes** RMU classes that have
  a template from the generic drawable list (drawing one there would POST a bare
  RMU with no assembly), and adds a dedicated "Ring Main Unit" section, one item
  per `(class, config)`.
- `feature-create-form.tsx` gains a `draft.rmu` Save branch: `POST /rmus` with
  `{ rmu_feature_class, geometry, subtype, name, attributes }` (attributes carry
  `rmu_config`, falling back to the menu's choice if the class doesn't show the
  field on create), then `bumpMapRefresh` + select the placed RMU. No
  `requestSuggestions` — the RMU container carries no topology; its ways/bus are
  already wired, and the external ring cables connect through the normal
  post-placement connect action.

An `RMU_SCHEMATIC` internal mimic (the way-and-bus one-line, mirroring Phase
42's `BOARD_SCHEMATIC` + placement allow-list) is optional and deferred until
the placement + fan-out loop is proven.

## Status

Slices 1–3 code-complete, verification owed (Bash sandbox blocks builds/psql
here). To verify: apply migration 0073, `task seed-mv-busbar` + `task
seed-mv-lv-switch` + `task seed-rmu` + `task seed-rmu-templates`, compile
`MV_BUSBAR` + `MV_SWITCH` + `RMU`, `go build ./...` the api and `npm run
build`/`tsc` the web app, then in the map's "New feature" menu pick a "Ring Main
Unit" item, click to place it, fill the form, Save, and confirm one RMU + one
MV_BUSBAR + three MV_SWITCH come back (each way with a `connection_id`) and a
downstream trace isolates a single tee way when OPEN. `GET /rmus/options` should
list the seeded `2W_1T`/`2W_2T` rows.

On a database that already had the OLD RMU catalog, run the collapses once
before retrying an RMU create: `task collapse-rmu-busbar` (folds the old
RMU_BUSBAR into MV_BUSBAR) and `task collapse-rmu-way` (folds the old RMU_WAY
into MV_SWITCH — migrating any live way features onto `gis.mv_switch`).
