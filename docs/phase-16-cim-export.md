# Phase 16 — CIM/XML export

Adds **CIM RDF/XML** as a new export format alongside KMZ / Shapefile / DXF
(Phase 14). Unlike those — which are geometry containers — **CIM is a
connectivity + equipment model** (IEC 61970/61968). The output isn't rows of
geometry; it's a graph of typed electrical objects (`ACLineSegment`,
`PowerTransformer`, `Breaker`, `EnergyConsumer`, …), each an RDF resource with a
UUID `mRID`, wired together through `Terminal`s that meet at shared
`ConnectivityNode`s. Export-only — there is no CIM *import*.

## Decisions (locked with the user)

- **Target: CDPSM (IEC 61968-13), CIM16.** The distribution-focused CIM profile,
  designed exactly for taking a distribution network model out of a GIS. A
  **single combined RDF/XML file** (not the CGMES-style EQ/TP/SSH/SV/GL profile
  split) — simplest and widely accepted; a profile split can come later if a
  strict CGMES consumer demands it.
- **Consumers: OpenDSS / CYME / PowerFactory / Synergi / regulator — "all of
  them."** No single tool dictates, so we emit **one canonical, standards-clean
  file**. Byte-perfect import into all five is *not* promised (each has quirks and
  partial profile support); per-tool adapters are tracked follow-ups, not scope.
- **Topology-first.** The `ConnectivityNode`/`Terminal` synthesis (Slice 2) is the
  real deliverable; the equipment+geometry skeleton (Slice 1) is the scaffold it
  needs.
- **The mapping is metadata-driven, not a hardcoded Go registry** — consistent
  with the app's authored-metadata DNA (feature classes, fields, domains,
  subtypes, network rules are all data). New tables
  (`metadata.cim_class_mappings`, `metadata.cim_attribute_mappings`,
  `metadata.cim_profiles`) hold the *declarative* mapping; a `seed/cim_mapping_*
  .sql` populates the default CDPSM mapping; the encoder reads them.
  - **No admin UI in this phase** (deferred by the user — "save this for later").
    The mapping is edited via SQL/seed for now; a CRUD UI is a later slice.
- **Behavioral logic stays in Go** (it can't be expressed as data): the
  `ConnectivityNode` synthesis, the conductor-end geometry disambiguation,
  `PowerTransformerEnd` construction, `mRID` derivation, and the RDF/XML
  serialization itself.
- **Substation containment derived from `SUBSTATION_YARD` only.** Each yard
  polygon becomes one CIM `Substation`; equipment whose point falls inside a yard
  (point-in-polygon) is contained by it; everything else goes in one default
  top-level `Line`/`EquipmentContainer`.
- **mRID strategy:** reuse each feature's existing UUID as its equipment `mRID`;
  deterministic **UUIDv5** (namespace + stable key) for every *synthesized*
  object (`BaseVoltage`, `Location`, `PositionPoint`, `Substation`, `Terminal`,
  `ConnectivityNode`) so re-exports diff cleanly.
- **Unmapped attributes are dropped (v1).** The mapping is the explicit contract
  of what's carried — nothing leaks silently. (A `carry`-as-generic-`Name` policy
  is a column on `cim_profiles` for later.)

## Backend vs frontend

CIM is a **backend** encoder, exactly like the existing formats — the connectivity
graph traversal belongs in SQL/Go, not the browser. The frontend change is one
new entry in the export dialog's format dropdown. This mirrors Phase 14's
"export is server-side" split, and the Phase 15 "two engines, one catalog"
reasoning (import reprojection is frontend; anything that reads the DB is
backend).

## The metadata mapping (what the user asked for)

Two declarative levels plus a profile/config row. **These are seeded, not
hardcoded, and not yet UI-editable.**

### `metadata.cim_class_mappings` — feature class (+ subtype) → CIM class

One row per feature class, optionally overridden per subtype. `cim_kind` tells
the encoder *how* to treat it (an edge gets 2 terminals; a junction gets its
terminals from `metadata.terminals`; a container becomes an `EquipmentContainer`;
an asset/protection/skip is non-conducting).

| Feature class | `cim_kind` | `cim_class` | Notes |
|---|---|---|---|
| `MV_CONDUCTOR` | `conducting_edge` | `ACLineSegment` | 2 synthesized terminals |
| `LV_CONDUCTOR` | `conducting_edge` | `ACLineSegment` | |
| `DIST_XFMR` | `conducting_junction` | `PowerTransformer` | + `PowerTransformerEnd` per terminal (T1/T2; T3 ground skipped) |
| `DIST_SWITCH` | `conducting_junction` | `Breaker` / `LoadBreakSwitch` / `Sectionaliser` | subtype override |
| `SERVICE_CUTOUT` | `conducting_junction` | `Fuse` | |
| `LV_LINK_BOX` | `conducting_junction` | `Junction` | |
| `LV_BUSBAR` | `conducting_junction` | `BusbarSection` | |
| `SERVICE_POINT` | `conducting_junction` | `EnergyConsumer` | the LV load |
| `STREET_LIGHT` | `conducting_junction` | `EnergyConsumer` | unmetered load |
| `METER` | `asset` | `UsagePoint` | 61968 Metering — non-conducting, so out of the topology graph (Slice 3 fix) |
| `METER_CHAMBER` | `container` | `EquipmentContainer` | |
| `SUBSTATION_YARD` | `container` | `Substation` | polygon → substation boundary |
| `POLE` | `asset` | `Pole` | non-conducting; linked via `feature_relationships` |
| `PROTECTIVE_RELAY` | `protection` | `ProtectionEquipment` | no geometry |

### `metadata.cim_attribute_mappings` — field → CIM attribute (+ coercion)

Only mapped fields survive. `transform` handles the type/unit gap between our
per-class typed columns and CIM's fixed, typed vocabulary:

| `transform` | Meaning | Uses |
|---|---|---|
| `direct` | copy the value as-is | text/number → same-typed CIM attribute |
| `scale` | multiply by `scale_factor` | e.g. km → m |
| `domain_map` | look up `value_map` (JSONB) | `11KV`→`11000` for `BaseVoltage.nominalVoltage` |
| `boolean` | map coded value → bool via `value_map` | `OPEN`→`true` for `Switch.normalOpen` |
| `from_geometry` | derive from the feature's geometry | `Conductor.length` from the LineString |
| `material_spec` *(Slice 3)* | join the field's domain value → `material_specs` | `r`/`x`/ampacity |
| `phase` *(Slice 4)* | expand a coded `PHASE` value into per-phase objects | `ACLineSegmentPhase`/`EnergyConsumerPhase` |

Representative rows (authoritative set lives in the seed, keyed to actual
`metadata.fields`):

| Field | CIM attribute | transform |
|---|---|---|
| `MV_CONDUCTOR.voltage_class` | `BaseVoltage.nominalVoltage` | `domain_map` `{11KV:11000, 33KV:33000}` |
| `DIST_XFMR.primary_voltage` | `BaseVoltage.nominalVoltage` | `domain_map` |
| *(geometry)* | `Conductor.length` | `from_geometry` |
| `DIST_SWITCH.device_state` | `Switch.normalOpen` | `boolean` |
| `LV_CONDUCTOR.conductor_spec` | `ACLineSegment.r` / `.x` | `material_spec` |
| `DIST_XFMR.rated_kva` | `PowerTransformerEnd.ratedS` | `scale` ×1000 |

### `metadata.cim_profiles` — the CIM/XML configuration

One row per profile the export can target (v1 seeds one: `CDPSM_CIM16`). Holds
the CIM namespace URI, CDPSM profile URI, model-authority-set URI, coordinate CRS
(`urn:ogc:def:crs:EPSG::4326`), and the `unmapped_attr_policy` (`drop`). The
export request names the profile (defaulting to `is_default`).

## Topology: how ConnectivityNodes are synthesized (Slice 2)

Our connectivity (`runtime.feature_connectivity`) is stored as **pairwise edges**
between features — `(from_feature, from_terminal) — (to_feature, to_terminal)`,
undirected. CIM has no such edge; it requires a shared `ConnectivityNode` that
multiple `Terminal`s reference. So:

> Build a graph whose vertices are terminal-endpoints `(feature_id, terminal_id)`
> and whose edges are `feature_connectivity` rows. Each **connected component**
> becomes one `ConnectivityNode`; every endpoint in it becomes a CIM `Terminal`
> pointing at that node.

**Verified wrinkle — conductor ends carry no terminal.** An edge (conductor) is
`network_role = edge` with **no rows in `metadata.terminals`**; a network rule is
`MV_CONDUCTOR (no terminal) → DIST_XFMR (T1)` (see
`seed/connectivity_example.sql:128`). So a conductor touching two junctions
produces two `feature_connectivity` rows, both referencing the same conductor
feature with a **NULL** `from_terminal_id` — end-1 and end-2 are
indistinguishable by terminal. A CIM `ACLineSegment` needs exactly 2 Terminals,
so the encoder disambiguates the two ends by **geometry**: match each connection
to the nearer endpoint of the conductor's LineString. This is the crux of Slice 2
and is settled before that slice starts.

## What the export pipeline is missing today

The current neutral model (`api/internal/export/feature.go`) reads each class in
isolation (`crud.ExportFeatures`) and **drops subtype, terminals, and all
connectivity** — `featureFrom` copies only id/name/status/geometry/attributes.
CIM needs the discarded data:

- **Slice 1** needs `subtype` (for subtype class overrides) — already read by
  `formatRow`, just not carried into `Feature`.
- **Slice 2** needs the `metadata.terminals` defs (already loaded by
  `metadata.Load` into `Loaded.Terminals`, currently unused by the encoder) and
  the `runtime.feature_connectivity` rows spanning the exported feature set
  (nothing in the export path touches that table today).

So the read-path extension is part of Slices 1–2, not a mechanical add.

## Slices

- **Slice 1 — skeleton (equipment + geometry + containment), no topology.**
  Mapping tables + seed; the `"cim"` format registered in `export.go`; the
  RDF/XML writer + `md:FullModel` header; `mRID` reuse + UUIDv5 for synthesized
  objects; `BaseVoltage` objects from voltage-class values; `Location` +
  `PositionPoint` (the GL profile) from the existing 4326 GeoJSON; `Substation`
  containers from `SUBSTATION_YARD` with point-in-polygon assignment; carry
  `subtype` through the neutral model; `"CIM/XML (.xml)"` in the export dialog.
  Output: a valid CIM file with equipment, coordinates, and containment — no
  `ConnectivityNode`s yet.
- **Slice 2 — topology (the core).** Load connectivity + terminals; build the
  endpoint graph; connected-components → `ConnectivityNode`s; emit `Terminal`s;
  `PowerTransformerEnd` wiring; conductor-end geometry disambiguation. After this
  the file is a genuine network model.
- **Slice 3 — electrical detail.** The values a power-system tool solves
  against: `Conductor.length` from the LineString geometry (geodesic),
  `ACLineSegment.r`/`.x` from `metadata.material_specs` (per-km × length),
  `PowerTransformerEnd` ratings (`ratedS`/`ratedU` + the primary winding's
  `BaseVoltage`). Also corrects the Slice-1 `PowerTransformer`/`UsagePoint`
  placeholders. **Phasing moved to Slice 4** — it needs a migration and per-phase
  CIM objects, out of proportion with the rest of Slice 3.
- **Slice 4 — phasing (deferred).** `PhaseCode` / `ACLineSegmentPhase` /
  `EnergyConsumerPhase` per-phase objects, `SinglePhaseKind` refs (L1→A, L2→B,
  L3→C, N→N; composites expanded). Needs a migration to add a `phase` transform
  to the `cim_attribute_mappings` CHECK, so it is its own slice.

## Open items (deferred, tracked)

- **Admin UI for the mappings** — edit `cim_class_mappings` /
  `cim_attribute_mappings` / `cim_profiles` without SQL. Explicitly deferred.
- **Per-tool adapters** — if OpenDSS/CYME/PowerFactory/Synergi reject the
  canonical file, small per-consumer tweaks (naming, profile split, attribute
  quirks). Out of scope until a real import failure is seen.
- **CGMES profile split** (EQ/TP/SSH/SV/GL as separate files) — only if a strict
  CGMES consumer needs it.
- **`carry` unmapped-attribute policy** — emit un-mapped fields as generic
  `cim:Name`/`UserAttribute`. Column exists on `cim_profiles`; not implemented.
- **CIM validation** — no CIMTool/CGMES validator in-repo; structure is
  self-validated. A validation harness could come later.

## Slice 1 — skeleton

### Metadata tables (`migrations/0034_create_cim_mapping.sql`)

`metadata.cim_profiles`, `metadata.cim_class_mappings`,
`metadata.cim_attribute_mappings` — schemas above. Lean catalog style (uuid PK,
`created_at`, CHECKs, thorough `COMMENT`s), matching `metadata.material_specs`; no
`metadata_version_id` (mappings version with the app, not the metadata set).

### What Slice 1 builds

- **Migration** `migrations/0034_create_cim_mapping.sql` — the three mapping
  tables (shapes only).
- **Seed** `seed/cim_mapping_example.sql` — the default `CDPSM_CIM16` profile,
  a class mapping for all 14 classes (+ a `DIST_SWITCH` subtype override:
  `automatic_recloser` → `Recloser`), and four attribute mappings
  (`MV_CONDUCTOR.voltage_class` / `DIST_XFMR.primary_voltage` →
  `BaseVoltage.nominalVoltage` via `domain_map`; `DIST_SWITCH.device_state` /
  `SERVICE_CUTOUT.device_state` → `Switch.normalOpen` via `boolean`). Grounded in
  the real seeded field names (voltage is `voltage_class` on the conductor but
  `primary_voltage` on the transformer; the switch class is `DIST_SWITCH`; state
  is `device_state`, `OPEN`/`CLOSED`).
- **Loader** `api/internal/metadata/cim.go` — `CimProfile`/`CimClassMapping`/
  `CimAttributeMapping` models + `LoadCIMMapping`, which resolves the mappings
  (joined to feature-class code / subtype code / field name) into `CIMMapping`
  with `ClassFor(code, subtype)` / `AttributesFor(code)` lookups.
- **Read-path** `api/internal/export/feature.go` — `Feature.SubtypeCode` carried
  through `featureFrom` (the geometry formats ignore it; CIM uses it for subtype
  class overrides).
- **Encoder** `api/internal/export/cim.go` — the RDF/XML writer: `md:FullModel`
  header; a `GeographicalRegion`/`SubGeographicalRegion`/`CoordinateSystem` +
  default `Line` scaffold; `mRID` = feature UUID, `uuidV5` (SHA-1, no external
  dep) for synthesized objects; distinct `BaseVoltage`s; `Location` +
  `PositionPoint`s (the GL profile) from the 4326 GeoJSON; `Substation`s from
  `SUBSTATION_YARD` with point-in-polygon equipment containment (ray-cast). No
  `ConnectivityNode`s/`Terminal`s yet.
- **Wiring** `api/internal/export/export.go` — `"cim"` registered (content-type
  `application/rdf+xml`, ext `xml`, `write: nil`); the handler loads the mapping
  (clean 400 if unseeded) and routes to `writeCIM`.
- **Frontend** `web/src/components/map/export-dialog.tsx` — `"CIM/XML"` added to
  the format dropdown.

### Slice 1 known simplifications (refined in Slices 2–3)

The encoder emits `Equipment.EquipmentContainer` + `ConductingEquipment.BaseVoltage`
generically for every `conducting_*` feature. That's exact for lines, switches,
busbars, and loads, but two mapped classes aren't strictly `ConductingEquipment`:

- **`PowerTransformer`** carries voltage on its `PowerTransformerEnd`/winding, not
  on the transformer itself — so its `BaseVoltage` link is a Slice-1 placeholder;
  Slice 3 builds the ends and moves it there.
- **`UsagePoint`** (`METER`) is a Metering object, not `Equipment` — its
  container/voltage links are cosmetic in Slice 1 and get corrected when metering
  is modelled properly.

Both are accepted here because Slice 1 is the geometry/containment scaffold and
"byte-perfect import into all five consumers is not promised" (see Decisions).

### Status

**Slice 1 code complete** (migration, seed, loader, read-path, encoder, wiring,
dropdown). Verification is **owed** — the Bash tool is unavailable in this
environment, so `go build ./...` / `go test ./...` and the web
`tsc -b` / `oxlint` have **not** been run.

## Slice 2 — topology

The core deliverable: the equipment from Slice 1 is now wired together through
`ConnectivityNode`s and `Terminal`s, and transformer windings become
`PowerTransformerEnd`s.

### What Slice 2 builds

- **Read-path extension** `api/internal/export/feature.go` — `Layer` gained
  `ClassID` (to pull the class's `feature_connectivity` rows), `NetworkRole`
  (edge vs junction — decides whether a NULL-terminal endpoint is a conductor end
  or a terminal-less junction), and `Terminals`/`Subtypes` (to resolve a
  feature's expected terminal set). `gatherLayers` (`export.go`) populates them
  from the already-loaded `metadata.Load` result; the geometry formats ignore
  them.
- **Connectivity load** `api/internal/export/topology.go` — `connRow` (a
  read-only view of `runtime.feature_connectivity`) + `loadConnectivity`, which
  pulls the edges among the exported classes in SQL and then filters in Go to
  rows whose **both** endpoints survived the export's bbox/territory scope, so the
  graph is closed (no `Terminal` ever points at a feature absent from the file).
  Loaded in the handler alongside the mapping, before any byte is streamed.
- **Graph + components** `topology.go:writeTopology` — union-find over endpoint
  keys (`T|<feature>|<terminalID>` for a named/implicit terminal,
  `E|<feature>|<endIndex>` for a conductor end); each connected component becomes
  one `ConnectivityNode` (deterministic UUIDv5 over its sorted member keys),
  contained by the substation holding any member (else the default `Line`).
- **Conductor-end disambiguation** — a NULL-terminal edge end is matched to the
  nearer end of the conductor's LineString (`endpointKey` uses the far feature's
  representative point; `nearestEndPair` handles direct conductor-to-conductor
  connections by the nearest endpoint pair).
- **Full terminal synthesis** — every conducting feature gets its complete
  expected terminal set (a conductor 2 ends; a junction one `Terminal` per
  non-ground `metadata.terminals` row, subtype-resolved), so an unconnected end
  still yields a valid `Terminal` on its own isolated `ConnectivityNode` rather
  than a malformed one-terminal `ACLineSegment`. Ground-role terminals are
  excluded (a grounded winding is modelled differently).
- **`PowerTransformerEnd`s** — a `PowerTransformer`'s terminals belong to its
  windings, so each transformer `Terminal` also emits a `PowerTransformerEnd`
  (`TransformerEnd.Terminal` + `endNumber`). Ratings/impedances/base voltage on
  the end are Slice 3.
- **Encoder wiring** `api/internal/export/cim.go` — `writeCIM`/`write` carry the
  `[]connRow`; a new Pass 4 calls `writeTopology` after the equipment pass. The
  Slice-1 "no topology" comment is updated.

### Slice 2 notes / deferred to Slice 3

- Each `PowerTransformerEnd` is wired to its transformer and terminal but has no
  `ratedU`/`ratedS`/`BaseVoltage`/`r`/`x` yet (Slice 3).
- `ConnectivityNode` containment reuses the Slice-1 substation point-in-polygon
  test on a member's representative point — good enough; a bay-level container is
  not modelled.
- Distance uses squared degrees (a comparison only, never a length), consistent
  with the encoder never needing real lengths until Slice 3's `Conductor.length`.

### Status

**Slice 2 code complete** (read-path extension, connectivity load, graph +
connected-components, conductor-end geometry disambiguation, full terminal
synthesis, `PowerTransformerEnd`s, encoder Pass 4). Verification is **owed** —
Bash is unavailable here, so `go build ./...` / `go test ./...` have **not** been
run. Slice 3 (electrical detail) is next.

## Slice 3 — electrical detail

The Slice 1–2 model is topologically complete but electrically inert: an
`ACLineSegment` with no length or impedance, a `PowerTransformer` with no
ratings. Slice 3 fills in the values a load-flow tool actually solves against,
and corrects the two Slice-1 placeholder links.

### What Slice 3 builds

- **Material-spec resolution** `api/internal/export/electrical.go` —
  `loadMaterialSpecs` builds the map from a coded conductor-spec value (as stored
  on a feature, e.g. `ABC_95`) to its `metadata.material_specs` row. The export
  read-path never carried material specs, so this joins every material-spec-bearing
  `domain_values` row (via the real `material_spec_id` FK, not a code-equality
  guess) to the `material_specs` catalog, once per export. Loaded in the handler
  alongside the mapping/connectivity.
- **Conductor length** `writeConductorDetail` — for a `conducting_edge` with a
  `from_geometry`→`Conductor.length` mapping, the **geodesic** length of the
  LineString (`haversine` per segment, `earthRadiusM` 6371 km), rounded to the
  millimetre. Emitted as a literal child of the `ACLineSegment`.
- **Series impedance** — for a `material_spec`→`ACLineSegment.r`/`.x` mapping, the
  spec's per-km resistance/reactance × length in km (`specPerKm`), rounded to the
  milliohm. A class with no seeded spec (MV — see the gap below) or a row with no
  coded value simply gets nothing.
- **Transformer ratings** `topology.go:writeTransformerEnd` — now carries the
  end-targeted attribute mappings (`PowerTransformerEnd.*` / `TransformerEnd.*`
  prefix), which drives `ratedS` (nameplate kVA→VA via `scale`) onto **every**
  winding; the primary winding (`endNumber` 1) additionally gets `ratedU` and its
  `TransformerEnd.BaseVoltage` from `primary_voltage` (there is no seeded secondary
  voltage, so the LV end gets neither).
- **Seed** `seed/cim_mapping_example.sql` — a second attribute-mapping `INSERT`
  (carrying `scale_factor`, absent from the Slice-1 block): `Conductor.length`
  (`from_geometry`) for both conductor classes, `ACLineSegment.r`/`.x`
  (`material_spec`) for `LV_CONDUCTOR.conductor_spec`, and
  `PowerTransformerEnd.ratedS` (`scale` ×1000) for `DIST_XFMR.rated_kva`.
- **Slice-1 placeholder corrections** `cim.go` —
  - `PowerTransformer` no longer gets a generic `ConductingEquipment.BaseVoltage`;
    voltage lives on the `PowerTransformerEnd` (built above).
  - `METER`'s `cim_kind` changed `conducting_junction`→`asset`, so a `UsagePoint`
    is emitted with just name + `mRID` + `Location`, and is kept out of the
    topology graph (it is not `Equipment`) rather than being given generic
    equipment/BaseVoltage links.

### Slice 3 notes / gaps

- **MV impedance gap.** Only `LV_CONDUCTOR` has seeded material specs
  (`LV_CABLE_SPEC`). `MV_CONDUCTOR` therefore gets a `Conductor.length` but no
  `r`/`x` — the `material_spec` mapping is deliberately LV-only until MV specs are
  authored. Not a bug: the encoder emits nothing rather than a wrong impedance.
- **`DIST_XFMR.source_r_ohm` / `.source_x_ohm`** (the load-flow Thévenin source
  impedance) are **not** mapped to CIM — they model the source behind the
  transformer, not a CIM equipment property. Left unmapped intentionally.
- **Latent Slice-1 compile fix.** Slice 1's `cim.go` declared its own `toFloat`,
  colliding with `shapefile.go`'s — so the `export` package never actually
  compiled. Removed the duplicate; the encoders now share `shapefile.go`'s richer
  `toFloat`. (This means Slice 1/2's "code complete, verification owed" status was
  optimistic — the package would not have built. It builds now, modulo the owed
  `go build`.)
- **`UsagePoint.Location`** is emitted through the generic `asset` path, which uses
  `PowerSystemResource.Location`; a `UsagePoint` is strictly not a PSR, so this is
  a minor profile imprecision carried for simplicity (consistent with "byte-perfect
  import is not promised").

### Status

**Slice 3 code complete** (`electrical.go` material-spec/length/impedance,
`writeTransformerEnd` ratings, handler spec loading, seed rows, Slice-1 placeholder
corrections, latent `toFloat` fix). Verification is **owed** — Bash is unavailable
here, so `go build ./...` / `go test ./...` have **not** been run. Slice 4
(phasing) is the remaining slice.

## Slice 4 — phasing

The Slice 1–3 model is phase-agnostic — an `ACLineSegment` or `EnergyConsumer`
says nothing about which of L1/L2/L3/N it carries. CDPSM models phasing not as an
attribute on the equipment but as **separate per-phase objects**: an
`ACLineSegmentPhase` (or `EnergyConsumerPhase`) per energised phase, each a sibling
RDF resource naming its single phase (a `SinglePhaseKind` — `A`/`B`/`C`/`N`) and
pointing back at its parent equipment. So one coded `PHASE` value expands into a
*set* of objects — which is why phasing is its own slice, and needed a migration.

### The coded-value → SinglePhaseKind expansion

The `PHASE` domain (`seed/phase_model_example.sql`) is L1/L2/L3/N plus the
single-phase-plus-neutral and four-wire composites. The encoder maps each to its
`SinglePhaseKind` members:

| `PHASE` value | SinglePhaseKind set |
|---|---|
| `L1` / `L2` / `L3` | `A` / `B` / `C` |
| `N` | `N` |
| `L1N` / `L2N` / `L3N` | `A,N` / `B,N` / `C,N` |
| `L1L2L3N` | `A,B,C,N` |

### What Slice 4 builds

- **Migration** `migrations/0035_add_phase_transform.sql` — widens the
  `cim_attribute_mappings.transform` CHECK with `'phase'` (drop + re-add of the
  inline `cim_attribute_mappings_transform_check`).
- **Encoder** `api/internal/export/phasing.go` — `phaseKinds` (the table above) +
  `writePhases`, which, for each `transform='phase'` mapping, expands the feature's
  coded phase value and emits one per-phase object per `SinglePhaseKind`. Each
  carries its `mRID` (deterministic `uuidV5("phase:<class>:<featureID>:<kind>")`),
  its `SinglePhaseKind` via a new `enumRef` helper (an absolute URI into the CIM
  namespace, not a local `#_mRID`), and its parent back-reference
  (`<PhaseClass>.<equipment CIM class>`, derived from the mapped class). Emitted
  after the equipment object closes, like `writeLocation` — the per-phase objects
  are siblings, not children.
- **Seed** `seed/cim_mapping_example.sql` — a third attribute `INSERT`:
  `MV_CONDUCTOR.phase` / `LV_CONDUCTOR.phase` → `ACLineSegmentPhase.phase`;
  `STREET_LIGHT.phase` → `EnergyConsumerPhase.phase` (all `transform='phase'`).

### Which classes are phased (and which aren't)

Only classes that actually carry a `phase` field, and have a clean CDPSM per-phase
object, are mapped:

| Feature class | CIM equipment | Per-phase object |
|---|---|---|
| `MV_CONDUCTOR` | `ACLineSegment` | `ACLineSegmentPhase` (phase nullable → may emit none) |
| `LV_CONDUCTOR` | `ACLineSegment` | `ACLineSegmentPhase` |
| `STREET_LIGHT` | `EnergyConsumer` | `EnergyConsumerPhase` |

`DIST_XFMR`, `LV_BUSBAR`, and `METER` carry a `phase` field too but are
**intentionally not phased**: a transformer is modelled as inherently multi-phase
(no simple per-phase object in this profile), `BusbarSection` has no per-phase
class in CDPSM, and `METER` is a non-conducting `UsagePoint` (asset). `SERVICE_POINT`
has no `phase` field at all, so its `EnergyConsumer` stays phase-agnostic.

### Status

**Slice 4 code complete** (migration `0035`, `phasing.go` expansion + `enumRef`,
`writePhases` wired into the equipment pass, seed rows). Verification is **owed** —
Bash is unavailable here, so `go build ./...` / `go test ./...` and a live
migration + seed apply have **not** been run. This completes the planned Phase 16
slices; the remaining items are the tracked deferrals above (admin UI, per-tool
adapters, CGMES split, `carry` policy, validation harness).
