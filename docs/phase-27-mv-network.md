# Phase 27 — MV network (substation yard down to the distribution transformer)

## Why

The LV network below a distribution transformer is well developed (Phase 9:
conductors, cutouts, link boxes, metering, unmetered CT). The MV side above
it was not. Before this phase the *entire* MV world was five feature classes:
`MV_CONDUCTOR` (edge), `DIST_XFMR` (the MV/LV boundary junction, which also
stands in as a substation power transformer by wiring convention),
`DIST_SWITCH` (the one real MV switch — `manual_switch` / `automatic_recloser`
subtypes), `PROTECTIVE_RELAY` (diagram-only, `geometry_type='none'`), and
`SUBSTATION_YARD` (a polygon site container). Every other piece of substation
and feeder plant — breaker, isolator, sectionalizer, surge arrester, MV fuse,
instrument transformers, MV busbar — existed **only as decorative ANSI
one-line glyphs** in `seed/ansi_stencils.sql`, with no feature class, no
attributes, no connectivity, no trace participation, no CIM mapping.

Phase 27 fills that gap: it models the MV plant from a substation yard down to
(and terminating at) a distribution transformer as real, connectable,
traceable feature classes — "just like we did for the LV network."

## Shape decisions (locked with the user before building)

* **One distinct feature class per device kind** (not one consolidated
  multi-subtype `MV_SWITCH`). A breaker interrupts fault current, an isolator
  is an off-load disconnector, a sectionalizer counts fault passages — they
  map to distinct CIM classes (`Breaker` / `Disconnector` / `Sectionaliser`)
  and warrant distinct classes. Subtypes stay for construction/physical kind.
* **`geometry_optional = true` on every new class** (migration 0052), plus
  `is_diagram_placeable = true` (migration 0032). This is the "map, diagram,
  or both — per feature" model: a pole-mounted feeder recloser is placed
  geographically; a bay breaker inside a yard is placed only on a one-line;
  the same class serves both, and an individual feature may carry NULL map
  geometry. The compiler emits a nullable `geom` column on first `CREATE
  TABLE` when `geometry_optional` is set. Both flags are set **explicitly** in
  the `feature_classes` INSERT because 0032/0052's backfills only touched rows
  that existed when they ran; a class inserted afterwards takes the column
  DEFAULT (`false`) otherwise.
* **CT/VT (instrument transformers) are in scope this phase** (Slice 3), as
  schematic measuring annotations — not load-carrying, so not trace barriers.
* **The recloser is not duplicated.** It already lives on `DIST_SWITCH` as its
  `automatic_recloser` subtype; Phase 27 leaves it there.

Everything reuses the settled Phase 9 seven-step seed recipe (feature_classes
→ subtypes → domains → fields → subtype_field_rules → terminals →
network_rules → trace_barrier_rules → `task compile CODE=…`) and the existing
shared domains (`DEVICE_STATE`, `PHASE`, `VOLTAGE_CLASS`, `CONDITION_RATING`,
`CT_RATIO`) and ANSI glyph artwork. No new engine mechanism is introduced.

## The class set

| Slice | Class | network_role | terminals | OPEN barrier? | CIM | glyph |
|---|---|---|---|---|---|---|
| 1 | `MV_BREAKER` | junction | T1 line / T2 load | yes | `Breaker` | `ansi-breaker` |
| 1 | `MV_ISOLATOR` | junction | T1 line / T2 load | yes | `Disconnector` | `ansi-disconnect` |
| 1 | `MV_SECTIONALIZER` | junction | T1 line / T2 load | yes | `Sectionaliser` | `ansi-load-break` |
| 2 | `MV_FUSE` | junction | T1 line / T2 load | yes (blown/pulled) | `Fuse` | `ansi-fuse` |
| 2 | `MV_ARRESTER` | junction | T1 line / T2 ground | no (shunt) | `SurgeArrester` | `ansi-surge-arrester` |
| 3 | `MV_CT` | junction | T1 (tap) | no | `CurrentTransformer` | `ansi-ct` |
| 3 | `MV_VT` | junction | T1 line / T2 ground | no | `PotentialTransformer` | `ansi-vt` |
| 4 | `MV_BUSBAR` | junction | T1 + T2/T3/T4 fan-out | no | `BusbarSection` | `bus` |

## Slice plan

* **Slice 1 — MV switching devices** — `MV_BREAKER`, `MV_ISOLATOR`,
  `MV_SECTIONALIZER`. `seed/mv_switching_example.sql`, task `seed-mv-switching`.
* **Slice 2 — MV protection** — `MV_ARRESTER` (shunt-to-ground),
  `MV_FUSE` (inline, OPEN barrier). `seed/mv_protection_example.sql`.
* **Slice 3 — MV measuring** — `MV_CT`, `MV_VT` (+ new `VT_RATIO` domain;
  reuses `CT_RATIO`). `seed/mv_measuring_example.sql`.
* **Slice 4 — MV busbar + substation wiring** — `MV_BUSBAR` and the
  bus/bay/coupler connectivity rules tying the yard together.
  `seed/mv_busbar_example.sql`.
* **Slice 5 — symbology + stencil promotion** — map + diagram symbology rules
  for the new classes and `promote_class` wiring so the existing ANSI glyphs
  stamp real features. `seed/mv_symbology_example.sql`.
* **Slice 6 — CIM mapping** — `cim_class_mappings` / `cim_attribute_mappings`
  rows so the new classes export to CDPSM/CIM16.

---

## Slice 1 — MV switching devices (`seed/mv_switching_example.sql`)

Three inline junction classes, each a byte-for-byte copy of the `DIST_SWITCH`
shape (`seed/switching_device_example.sql`) plus the two new geometry knobs:

* **`MV_BREAKER`** — subtypes `vacuum_breaker` / `sf6_breaker`.
* **`MV_ISOLATOR`** — subtypes `single_break` / `double_break`.
* **`MV_SECTIONALIZER`** — subtypes `hydraulic` / `electronic`.

Each carries a required `device_state` (CLOSED/OPEN) and `condition_rating`,
two terminals `T1` (line, primary) / `T2` (load, secondary), and the three
`network_rules` (`MV_CONDUCTOR -> device T1`, `-> device T2`, and
`device T2 -> MV_CONDUCTOR` so a DOWNSTREAM trace walks through). Rule shapes
are authored as one set-generating INSERT each across the three classes.

**Barriers.** An OPEN device halts any barrier-respecting trace. The barrier
INSERT keys on `trace_configurations.stop_at_open_devices = true` rather than a
hardcoded config list, so the two target-class traces (`TRACE_TO_TRANSFORMER`
/ `LV_NETWORK_TRACE`) that also respect barriers are covered without naming
them — a fault upstream of an open MV device must not be reported as reaching
the transformer through it.

**Apply order.**

```
task seed-mv-switching     # after seed-connectivity + trace_configurations_example.sql
task compile CODE=MV_BREAKER
task compile CODE=MV_ISOLATOR
task compile CODE=MV_SECTIONALIZER
```

### Slice 1 — verification owed (Bash blocked here; run locally)

* `task seed-mv-switching` applies cleanly (idempotent — re-run is a no-op).
* The three `task compile CODE=…` runs each create a `gis.mv_breaker` /
  `gis.mv_isolator` / `gis.mv_sectionalizer` table with a **nullable** `geom`
  column (confirms `geometry_optional` took on first CREATE TABLE) and a
  `subtype` CHECK over `(1,2)`.
* `metadata.feature_classes` shows the three rows with
  `is_diagram_placeable = true` and `geometry_optional = true`.
* A DOWNSTREAM/isolation trace through a `MV_BREAKER` whose `device_state` =
  OPEN halts at the breaker; CLOSED lets it pass. Same for the other two.
* Each device is placeable both on the map and on a one-line diagram, and a
  feature saved with no map geometry is accepted (geometry_optional path).

---

## Slice 2 — MV protection (`seed/mv_protection_example.sql`)

Two classes with deliberately different topology:

* **`MV_FUSE`** — inline, the same three-rule / OPEN-barrier shape as the
  Slice 1 switching devices. `device_state` OPEN = a blown or pulled fuse;
  subtypes `expulsion_fuse` / `current_limiting_fuse`. It halts isolation
  traces exactly like an open breaker.
* **`MV_ARRESTER`** — a **shunt** device, not inline. `T1` (primary) taps the
  line; `T2` (ground, `phase_capable = false`, mirroring `DIST_XFMR.T3`) is
  the earth connection. There is no earth feature class, so `T2` carries no
  `network_rule` — it is a documented dead-end to ground. The arrester is
  always in-circuit: **no `device_state`, no barrier**. Subtypes
  `gapless_mov` / `gapped_sic`. Its only required field is `condition_rating`
  (a rated/MCOV-voltage field is a natural follow-up but there is no domain
  for it yet — left out rather than seeded speculatively).

**Apply order.**

```
task seed-mv-protection    # after seed-connectivity + trace_configurations_example.sql
task compile CODE=MV_FUSE
task compile CODE=MV_ARRESTER
```

### Slice 2 — verification owed (Bash blocked here; run locally)

* `task seed-mv-protection` applies cleanly and is idempotent.
* `task compile CODE=MV_FUSE` / `CODE=MV_ARRESTER` each create a `gis.*` table
  with a nullable `geom` and a `subtype` CHECK over `(1,2)`.
* An isolation trace halts at a `MV_FUSE` with `device_state = OPEN`, passes
  when CLOSED; a `MV_ARRESTER` never halts a trace (it has no `device_state`,
  and its only connection is the incoming `MV_CONDUCTOR -> T1` tap — a trace
  reaching it dead-ends at the shunt, it is not walked through).
* `MV_ARRESTER.T2` (ground) is present with `phase_capable = false` and has no
  `network_rules` row on either side.

---

## Slice 3 — MV measuring / instrument transformers (`seed/mv_measuring_example.sql`)

Two standalone instrument-transformer classes. Note this deliberately differs
from the LV decision (docs/phase-9 folds LV service CT/VT into a `ct_ratio`
attribute on `METER`): on the MV side an instrument transformer is standalone
yard/bay plant with its own location, condition and ratio, so — as the user
locked — it earns its own class.

* **`MV_CT`** — a current transformer. Non-interrupting, so modelled as a
  single-terminal **tap** (`T1`) on the line, never walked through — the same
  attach-and-dead-end shape as `MV_ARRESTER`, not an inline pass-through node.
  Fields `ct_ratio` (reuses the existing `CT_RATIO` domain) + `condition_rating`;
  subtypes `metering_ct` / `protection_ct` (accuracy vs. protection core).
* **`MV_VT`** — a voltage/potential transformer. Shunt line-to-ground (`T1`
  line / `T2` ground), topologically identical to the arrester, no barrier.
  Fields `vt_ratio` (new `VT_RATIO` domain — 11 kV/110 V, 33 kV/110 V, mirroring
  the two `VOLTAGE_CLASS` levels) + `condition_rating`; subtypes `inductive_vt`
  / `capacitive_vt` (CVT).

Neither carries `device_state`, so neither seeds a `trace_barrier_rule`.

**Apply order.**

```
task seed-mv-measuring     # after seed-connectivity
task compile CODE=MV_CT
task compile CODE=MV_VT
```

### Slice 3 — verification owed (Bash blocked here; run locally)

* `task seed-mv-measuring` applies cleanly and is idempotent (the re-asserted
  `CT_RATIO` values are a no-op if the LV CT seed already ran).
* `task compile CODE=MV_CT` / `CODE=MV_VT` each create a `gis.*` table with a
  nullable `geom` and a `subtype` CHECK over `(1,2)`.
* The new `VT_RATIO` domain exists with its two values; `MV_VT.vt_ratio` and
  `MV_CT.ct_ratio` resolve to their domains.
* Both attach to the line via `MV_CONDUCTOR -> device T1` and neither halts or
  is walked through by a trace.

---

## Slice 4 — MV busbar + substation wiring (`seed/mv_busbar_example.sql`)

`MV_BUSBAR` on the `LV_BUSBAR` shape: `T1` incoming, `T2` the equipotential bar
that fans out to every bay (a solid bar, not switchable ways — the bay switches
*are* the Slice 1 devices). Subtypes `air_insulated` / `gas_insulated`; fields
`voltage_class` (reuses `VOLTAGE_CLASS`) + `condition_rating`.

**Substation wiring** — four rule shapes make a whole yard expressible:

| | rule | purpose |
|---|---|---|
| A | `MV_CONDUCTOR -> MV_BUSBAR T1` | feeder / transformer tail onto the bus |
| B | `MV_BUSBAR T2 -> MV_CONDUCTOR` | bar fans out to a bay feeder (any N) |
| C | `MV_BUSBAR T2 -> {switch} T1` | bar feeds a bay switch's line side directly |
| D | `{switch} T2 -> MV_BUSBAR T1` | switch load side onto a bus (incomer / coupler / section switch) |

`{switch}` ranges over the Slice 1 devices (`MV_BREAKER` / `MV_ISOLATOR` /
`MV_SECTIONALIZER`), authored as set-generating INSERTs. C+D together express a
bus coupler or bus-section switch between two bus instances with no new rule
shape. If Slice 1 isn't applied, C/D insert zero rows (graceful).

**Yard containment** — one set-generating `containment` relationship_class
(`SUBSTATION_YARD contains {plant}`, one_to_many, the same kind `METER_CHAMBER`
uses) ties the yard to every MV plant class that exists — busbar, all switching,
protection and measuring devices. Rows for unseeded classes are skipped.

**Apply order.**

```
task seed-mv-busbar        # after seed-connectivity + seed-substation-yard
                           # (+ mv-switching/protection/measuring for full wiring/containment)
task compile CODE=MV_BUSBAR
```

### Slice 4 — verification owed (Bash blocked here; run locally)

* `task seed-mv-busbar` applies cleanly and is idempotent; applying it before
  the other MV slices still succeeds (just seeds fewer C/D/containment rows).
* `task compile CODE=MV_BUSBAR` creates `gis.mv_busbar` with a nullable `geom`
  and a `subtype` CHECK over `(1,2)`.
* A DOWNSTREAM trace walks incomer → `MV_BUSBAR` T1 → T2 → bay feeder / bay
  breaker; an OPEN bay breaker off the bus halts its branch only.
* `SUBSTATION_YARD contains …` relationship_classes exist for each seeded MV
  plant class.

---

## Slice 5 — symbology + stencil promotion (`seed/mv_symbology_example.sql`)

Gives the eight new classes their look on both surfaces and closes the
palette→feature loop, reusing the existing ANSI artwork — **no new glyphs**.
Three idempotent parts:

1. **Map defaults** (`render_context='map'`) — the mandatory per-class default
   rule (`subtype_id` + `domain_value_id` both NULL). Simple shapes from the
   client registry (`web/src/lib/symbology-icons.ts`:
   circle/square/triangle/diamond), colour-coded by function — switching green
   `#16a34a`, protection orange `#ea580c`, instrument cyan `#0891b2`, bus slate
   `#334155`. This is the fallback for when a feature *is* placed geographically
   (the classes are `geometry_optional`, so they can be).
2. **Diagram glyphs** (`render_context='diagram'`) — one rule per class pointing
   at its ANSI one-line glyph, so a one-line placement reads as the real symbol.
   The canvas prefers the `diagram` rule and falls back to `map`.
3. **Stencil promotion** — a single guarded set-generating UPDATE on
   `metadata.diagram_stencils.promote_class` (migration 0051) mapping each
   switching/protection/measuring stencil to its new class, so a stamped glyph
   can be promoted to a real feature — the `ansi-relay → PROTECTIVE_RELAY`
   pattern, now extended to breaker/isolator/sectionalizer/fuse/arrester/CT/VT.

| class | map shape | diagram glyph | promote from stencil |
|---|---|---|---|
| `MV_BREAKER` | square | `ansi-breaker` | `ansi-breaker` |
| `MV_ISOLATOR` | triangle | `ansi-disconnect` | `ansi-disconnect` |
| `MV_SECTIONALIZER` | diamond | `ansi-load-break` | `ansi-load-break` |
| `MV_FUSE` | triangle | `ansi-fuse` | `ansi-fuse` |
| `MV_ARRESTER` | diamond | `ansi-surge-arrester` | `ansi-surge-arrester` |
| `MV_CT` | circle | `ansi-ct` | `ansi-ct` |
| `MV_VT` | square | `ansi-vt` | `ansi-vt` |
| `MV_BUSBAR` | square | `ansi-terminal` | — (drawn with the `bus` primitive) |

The four `is_diagram_placeable` fix-up UPDATEs `ansi_stencils.sql` needed for
`PROTECTIVE_RELAY` are **not** repeated — Slices 1–4 already set the flag in the
class INSERTs. No OPEN-state map override is seeded: the shape registry has no
hollow variant of square/diamond, so the shape-preserving trick `DIST_SWITCH`
uses (triangle → `triangle-outline` on OPEN) isn't available for the non-triangle
classes; deferred to a slice that adds those shapes. The OPEN **trace barrier**
(Slices 1–2) is the functional behaviour and is unaffected.

**Apply order.** Nothing to compile — symbology and stencils resolve entirely
client-side.

```
task seed-mv-symbology     # after the four class slices + seed/ansi_stencils.sql
```

### Slice 5 — verification owed (Bash blocked here; run locally)

* `task seed-mv-symbology` applies cleanly and is idempotent (re-run inserts 0
  rows and re-sets 0 promotions).
* Each of the eight classes has exactly one default `map` rule and one `diagram`
  rule; on a one-line each renders as its ANSI glyph, on the map as its shape.
* `metadata.diagram_stencils.promote_class` is set for the seven mapped stencils
  and unchanged for any that already had a mapping.
* Stamping e.g. the breaker glyph on a one-line and promoting it produces a real
  `MV_BREAKER` feature.

---

## Slice 6 — CIM mapping (`seed/mv_cim_mapping_example.sql`)

Extends the base CDPSM/CIM16 mapping (`seed/cim_mapping_example.sql`,
docs/phase-16-cim-export.md) so the eight new classes export instead of being
dropped as unmapped. All eight are point junctions → `cim_kind =
'conducting_junction'` (the LV_BUSBAR / DIST_SWITCH kind). The distinct CIM
classes are exactly why each device got its own feature class:

| feature class | CIM class |
|---|---|
| `MV_BREAKER` | `Breaker` |
| `MV_ISOLATOR` | `Disconnector` |
| `MV_SECTIONALIZER` | `Sectionaliser` |
| `MV_FUSE` | `Fuse` |
| `MV_ARRESTER` | `SurgeArrester` |
| `MV_CT` | `CurrentTransformer` |
| `MV_VT` | `PotentialTransformer` |
| `MV_BUSBAR` | `BusbarSection` |

**Attribute mappings.** `device_state → Switch.normalOpen` (boolean) for the
four CIM Switch subclasses (breaker/isolator/sectionalizer/fuse), and
`voltage_class → BaseVoltage.nominalVoltage` (domain_map) for `MV_BUSBAR`,
reusing the exact transforms the base seed uses for `DIST_SWITCH` /
`MV_CONDUCTOR`. The instrument-transformer ratios (`ct_ratio`/`vt_ratio`) and
`condition_rating` have no simple CDPSM scalar to derive without inventing
units, so they're left unmapped rather than seeded speculatively — the same
posture the base seed takes on the MV impedance gap.

**Apply order.** No compile step — pure metadata rows.

```
task seed-mv-cim-mapping    # after the four class slices + seed-cim-mapping
```

### Slice 6 — verification owed (Bash blocked here; run locally)

* `task seed-mv-cim-mapping` applies cleanly and is idempotent (re-run inserts 0
  class rows and 0 attribute rows).
* Each of the eight classes has one class-default `cim_class_mappings` row with
  the CIM class above and `cim_kind='conducting_junction'`.
* A CIM/XML export of a model containing these features emits `cim:Breaker`,
  `cim:Disconnector`, … objects; an OPEN breaker serializes
  `Switch.normalOpen=true`; an `MV_BUSBAR` references a `BaseVoltage` at its
  nominal voltage.

---

## Follow-up — `diagram_geometry_type`: a busbar that's a point on the map, a bar on the one-line

`MV_BUSBAR` exposed a tension the model hadn't yet resolved. Physically a busbar
is a *point* junction (`geometry_type='point'`, `network_role='junction'`): on
the geographic map it's a dot where the bays converge, and its "bar" is a
topology fact — terminal `T2`'s equipotential fan-out — not a drawn line. But on
a schematic one-line the same busbar has to *read* as a busbar: a heavy spanning
bar, not a dot. The two surfaces genuinely want two different shapes for the one
class.

The diagram already stored geometry independently of the map — a placement's
hand-drawn `path` (a polyline in diagram space, migration 0033) lives on
`runtime.diagram_placements`, untied to the feature's geographic `geom`. What was
missing was permission to *use* a line path for a class the map calls a point.
The gate `validatePlacementPath` (`api/internal/diagram/sql.go`) only allowed a
non-empty path when `geometry_type='line'`, and the web canvas drove every
wire-vs-icon decision off the same `geometry_type`. So a point class could never
be drawn as a bar on a diagram.

**`diagram_geometry_type` (migration 0053)** is the override that unties the two:

* A nullable `text` column on `metadata.feature_classes`, `CHECK IN ('point',
  'line')`, `NULL` = **inherit** `geometry_type` (the map shape). It changes
  *only* how a class is drawn on a **diagram**; the map, the physical `gis.<code>`
  table, connectivity and traces are all untouched.
* **Pure metadata — no recompile.** Like the `is_diagram_*` flags, it never
  touches the physical table, so it's editable at any status and takes effect the
  moment it's saved. `MV_BUSBAR` ships with it set to `'line'`
  (`seed/mv_busbar_example.sql`), so a fresh install already draws the bar.
* One accessor centralises the fallback: `FeatureClass.DiagramGeom()`
  (`api/internal/metadata/models.go`) returns the override or, when `NULL`,
  `GeometryType`. The path gate now consults `DiagramGeom()` instead of
  `GeometryType`, and every web render decision funnels through one map —
  `geometryTypeByClass` in `diagram-canvas.tsx` is built as
  `fc.diagram_geometry_type ?? fc.geometry_type`, so the single fallback cascades
  to the arm-for-line-drawing gate and all icon-vs-wire render sites at once.

**Drawing a line-shaped feature.** Both diagram placement paths honour the
override:

* *Add from map* (place an existing feature onto the diagram) already armed
  line-drawing off `DiagramGeom()`, so a busbar picked from the map draws as a
  polyline.
* *Digitize a new feature* (the diagram's "New feature" tool) originally always
  dropped a **single point** via `startDiagramDraft`, ignoring the diagram shape
  — so a brand-new busbar couldn't be *drawn* as a bar. Fixed: `digitizeIsLine`
  (`= geometryTypeByClass[digitizingClass] === "line"`) folds into
  `isLineDrawing`, so a line-shaped digitize collects vertices click-by-click
  (double-click / *Finish* to commit), and `finishDigitizeLine` hands the drawn
  polyline to the draft's new `diagramPlacement.path`, which
  `feature-create-form.tsx` forwards to the placement POST. A point-shaped class
  still creates on the first click.

**Admin control.** The class detail page's *Diagram settings* card
(`/admin/feature-classes/<code>`) has a **Diagram shape** `<select>` — *Inherit
map shape (point)* / *Point* / *Line* — PATCHing `diagram_geometry_type` (`""`
clears to `NULL`; three-state on the wire so an untouched form sends nothing). A
`Diagram shape: line` badge shows when an override is set. So any point class can
be promoted to draw as a bar on diagrams from the UI, with no seed edit and no
compile.

**Touch-points** (for the next person who greps for this): migration 0053;
`FeatureClass.DiagramGeom()` + `DiagramGeometryType` field; `featureClassResponse`
(`cmd/server/main.go`) and the admin `updateFeatureClass*` / `featureClassSummary`
DTOs; `validatePlacementPath` caller in `diagram/sql.go`; web `geometryTypeByClass`
+ arm gate + `digitizeIsLine`/`isLineDrawing`/`finishDigitizeLine`
(`diagram-canvas.tsx`), `diagramPlacement.path` on the draft (`draw-store.ts`)
forwarded by `feature-create-form.tsx`, the `FeatureClassSummary` /
`AdminFeatureClassSummary` wire types, and the *Diagram shape* select + badge in
`admin-feature-class-detail.tsx`.

### Follow-up — verification owed (Bash blocked here; run locally)

* Apply migration 0053; `metadata.feature_classes.diagram_geometry_type` exists
  with the `('point','line')` CHECK.
* `task seed-mv-busbar` (or re-run it) leaves `MV_BUSBAR.diagram_geometry_type =
  'line'` (idempotent — the `UPDATE` re-asserts it even on a row seeded before
  0053).
* In admin, set a point class's **Diagram shape** to *Line* and save — no
  recompile prompt, badge appears; clear it back to *Inherit* and the badge goes.
* On a one-line, placing `MV_BUSBAR` arms line-drawing and renders it as a
  bar/wire, while the same feature stays a point on the geographic map.
* Digitizing a *new* `MV_BUSBAR` via the diagram's **New feature** tool draws a
  polyline (click vertices, double-click/*Finish* to commit) rather than
  dropping a single point.
* `go build ./...` / web typecheck pass with the new field/method and DTOs.

---

## Operational note — adding a required field to a class that already has rows

The compiler is **additive-only** (`api/internal/compiler/ddl.go`): first compile
`CREATE TABLE`s the class, every later compile `ALTER TABLE ADD COLUMN`s new
fields. It never rewrites an existing column. That has one blind spot: it can't
add a **required** (`nullable=false`, i.e. `NOT NULL`) field to a class that
**already has rows** — Postgres rejects `ALTER TABLE … ADD COLUMN … NOT NULL` on
a populated table because there's no value for the existing rows:

```
compile MV_FUSE: add column condition_rating:
  ERROR: column "condition_rating" of relation "mv_fuse"
         contains null values (SQLSTATE=23502)
```

(This is why `condition_rating` on the Slice 1–4 classes is enforced NOT NULL in
the field def **and** `is_required` in the subtype rule — the physical NOT NULL
is the half that blocks additive compilation.)

Two recovery paths:

**(1) Drop-and-rebuild — `task uncompile` (DESTRUCTIVE, discards the rows).**
Use when the table's rows are disposable (example data, a fresh dev class).
`uncompile` is the exact inverse of `compile` — it drops the physical
`gis.<code>` table, its Martin tile function, and (via `CASCADE`) the spatial
index, then resets the class metadata to `status='draft'`, `physical_table=NULL`,
leaving **every other metadata row intact** (fields, subtypes, terminals,
symbology/CIM/connectivity rules — all the Phase 27 wiring). A following
`compile` rebuilds the table from scratch, and an empty table takes the NOT NULL
column without complaint. It prints the row count it discarded (no undo — back
the data up first if it matters).

```
task uncompile CODE=MV_FUSE    # drop gis.mv_fuse + tile fn, reset to draft
task compile   CODE=MV_FUSE    # rebuild fresh with the required field
```

(`api/internal/compiler/uncompile.go` + `api/cmd/uncompile`.)

The same action is in the **web admin** — on a compiled class's detail page
(`/admin/feature-classes/<code>`) an **Uncompile** button sits beside
**Recompile** (`metadata:write`, POST `/admin/feature-classes/{code}/uncompile`
→ `compiler.Uncompile`). It confirms first, reports the row count it discarded,
then you press **Recompile**. The button is hidden on a draft class (no table to
drop).

**(2) Non-destructive backfill (keeps the rows).** Use when the rows must
survive. Add the column nullable, fill every row with a valid value, then promote
it to NOT NULL — after which `compile` sees the column already present and does
nothing to it. Use a real domain value for coded fields (`condition_rating`'s
`CONDITION_RATING` domain is `GOOD`/`FAIR`/`POOR`):

```sql
ALTER TABLE gis.mv_fuse ADD COLUMN condition_rating text;
UPDATE gis.mv_fuse SET condition_rating = 'GOOD' WHERE condition_rating IS NULL;
ALTER TABLE gis.mv_fuse ALTER COLUMN condition_rating SET NOT NULL;
```

```
task compile CODE=MV_FUSE      # no-op on the column, now that it matches
```

---

## Status

Slices 1–6 are code-complete (seeds + Taskfile tasks + this doc). Everything is
static — the Bash tool EPERMs in this environment, so every `psql` apply, every
`task compile CODE=…`, and the trace/barrier/export checks in each slice's
"verification owed" block are still owed and must be run locally before the
phase is called verified.
