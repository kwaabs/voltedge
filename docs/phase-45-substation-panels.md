# Phase 45 — Substation control panels (as inventoried assets)

**Status:** Slice 1 code-complete (verification owed — Bash blocked here, DB
apply + compile run locally). Slices 2–3 specced.

## Why

A receiving substation has two zones. The **switchyard** — power transformer,
busbars, breakers, CTs, VTs, arresters — is the outdoor primary plant, already
well modelled: [`SUBSTATION_YARD`](../seed/substation_yard_example.sql) is a
polygon on the map containing those classes, and they connect and trace. The
**control building** is the secondary side: rows of **panels** — protection
relay panels, control panels, metering panels, RTU/SCADA (remote) panels — each
housing the relays, meters, and control kit for one or more circuits.

[Phase 34](phase-34-substation-bays.md) modelled the control building
*logically* as **`BAY`s** (one circuit = one panel, drawn on a diagram) and
deliberately did **not** model the panel as a thing — "a generic representation
will do." This phase revisits that: the user asked to **inventory panels as
assets** — countable, taggable enclosures with nameplate data.

## The decision: panel and bay are orthogonal

A relay has **two** memberships, and they are different facts:

- **Which circuit it protects** → the **`BAY`** (logical). Already built.
- **Which physical enclosure it sits in** → the **`SUBSTATION_PANEL`** (new,
  physical).

They are **many-to-many**: one shared protection panel serves several bays; one
bay's kit spans a protection + a control + a metering panel. Forcing them 1:1
(the old "a bay *is* its panel") is exactly what breaks once you inventory
panels — so we keep **both**, linked by an associative mapping.

```
SUBSTATION_YARD (polygon, on map)
 ├─ switchyard primary plant: POWER_XFMR, MV_BUSBAR, MV_BREAKER,
 │   MV_CT, MV_VT, MV_ARRESTER ...          (on map, unchanged)
 │
 ├─ BAY (none-geom) = the circuit           ── serves ──┐   many_to_many
 │    └─ PROTECTIVE_RELAY (which circuit)               │   (kind panel_bay)
 │                                                      │
 └─ SUBSTATION_PANEL (none-geom, asset) ◄───────────────┘
      panel_type: PROTECTION / CONTROL / METERING /
                  RTU_SCADA / AUXILIARY_DC / MARSHALLING
      panel_ref, manufacturer, model, control_mode, voltage_class, condition
      └─ PROTECTIVE_RELAY (which enclosure), METER
```

`PROTECTIVE_RELAY` is contained by a **`BAY`** (circuit) *and* a
**`SUBSTATION_PANEL`** (enclosure) — two separate `containment`
relationship_classes, both `one_to_many`, so a relay has at most one bay and at
most one panel. "Remote relay panels" fall out as `panel_type = RTU_SCADA`
and/or `control_mode = REMOTE`.

### Decisions locked (with the user)

- **Inventory panels as assets** — a real `SUBSTATION_PANEL` class you can count,
  tag (`panel_ref`), and give nameplate data (`manufacturer`/`model`), not the
  Phase 34 "bay = its panel" simplification.
- **One class + `panel_type` field**, not separate `RELAY_PANEL`/`CONTROL_PANEL`
  classes — the repo's consistent call (`BAY_ROLE`, `switch_function`,
  `device_number`: *functional variant is a field*). Panel-type variance is a
  field; the shared asset behaviour (tag, nameplate, containment) is the class.
- **Parked under the yard** — `SUBSTATION_YARD contains SUBSTATION_PANEL`
  directly; **no** `CONTROL_BUILDING` layer (the user did not want a mappable
  building). Revisit only if the control room needs its own footprint.
- **`geometry_type='none'`** — a panel is an inventoried asset on a
  control-scheme diagram, not a map pin (like `BAY` / `PROTECTIVE_RELAY`).
- **`panel_bay` is its own associative kind** (many_to_many), not `containment`
  (a panel does not hold a bay — a bay is logical; and containment would make
  `BAY` diagram-placeable inside a panel container) nor `composite` (neither
  owns the other).

### Not to be confused with `MV_SWITCHGEAR_PANEL` (Phase 42)

Two classes carry "panel" in the name; they sit on opposite sides of the
primary/secondary line and never model the same thing:

- **`SUBSTATION_PANEL`** (this phase) = a **secondary control-building cabinet**
  (protection/control/metering/RTU). `network=none`, holds relays + meters, and
  is **orthogonal** to `BAY` — a relay is in a cabinet *and* serves a bay.
- **`MV_SWITCHGEAR_PANEL`** (Phase 42) = a **primary switching cubicle** in an
  indoor metal-clad `MV_SWITCHBOARD`. `network=MV`, holds breaker/CT/VT, and
  **substitutes** for `BAY` (it's the indoor-board way of organizing a circuit,
  vs the outdoor-AIS bay way).

The full when-to-use-which is written up in
[Phase 34 → "BAY vs the two panel classes"](phase-34-substation-bays.md#bay-vs-the-two-panel-classes--which-grouping-when).
None of the three touches the power engine — they are asset structure + drawing
only; topology rides on terminals + connectivity.

## Slice 1 — the class + containment + panel↔bay mapping (code-complete)

- **Migration [0083](../migrations/0083_widen_relationship_kind_panel_bay.sql)**
  widens `metadata.relationship_classes.kind`'s CHECK to admit `panel_bay`
  (drop + re-add, no data rewrite — exactly the 0075/0080/0082 pattern). The Go
  relationship engine switches only on **cardinality**, never kind
  ([relationship/cardinality.go](../api/internal/relationship/cardinality.go)),
  so a new kind is purely additive; `kind` is only a matcher
  ([relationship/rules.go](../api/internal/relationship/rules.go) `matchClass`).
  The two authoring allowlists are widened in step:
  [admin/rules.go](../api/internal/admin/rules.go) `validRelationshipKinds` and
  [web `RELATIONSHIP_KINDS`](../web/src/routes/admin-feature-class-detail.tsx).
- **[seed/substation_panel_example.sql](../seed/substation_panel_example.sql)**
  (`task seed-substation-panel`, then `compile CODE=SUBSTATION_PANEL`) — the
  `SUBSTATION_PANEL` class: `geometry_type='none'`, `network_role='container'`,
  one `standard` subtype; field_group `panel`; new domains `PANEL_TYPE`
  (PROTECTION/CONTROL/METERING/RTU_SCADA/AUXILIARY_DC/MARSHALLING) and
  `CONTROL_MODE` (LOCAL/REMOTE/LOCAL_REMOTE); fields `panel_type` (required),
  `panel_ref`, `control_mode`, `manufacturer`, `model`, `voltage_class`,
  `condition_rating` (rest nullable). Compiles to a `gis.substation_panel` table
  (id, name, subtype, status, the seven fields, audit columns — no
  `geom`/`territory_id`).
- **[seed/substation_panel_containment_example.sql](../seed/substation_panel_containment_example.sql)**
  (`task seed-substation-panel-containment`, no compile) — three
  relationship_classes, metadata-only, guarded/idempotent:
  `SUBSTATION_YARD contains SUBSTATION_PANEL`,
  `SUBSTATION_PANEL contains {PROTECTIVE_RELAY, METER}` (both `containment`,
  `one_to_many`), and `SUBSTATION_PANEL serves BAY` (`panel_bay`,
  `many_to_many`). CTs/VTs are deliberately not panel-contained (they live in
  the yard, only wired to the panel).

No instance seeding — like every seed here, legal pairings only; actual links
are app/API-established at runtime.

## Slice 2 — depiction (built, "just depiction" scope)

Scope chosen: **just depiction** — get a panel glyph visible on the diagram,
keyed on `panel_type`; links stay established through the generic
relationship-suggestion flow (no bespoke authoring UI), and no templated
fan-out. Implemented purely in `seed/substation_panel_symbology.sql`
(`task seed-substation-panel-symbology`), which is the whole slice:

1. Flips `SUBSTATION_PANEL.is_diagram_placeable = true` (migration 0032 gate) so
   the panel can be dropped on the substation's diagram. The yard is a `ONE_LINE`
   container (`seed/diagram_example.sql`) and the `(SUBSTATION_YARD × ONE_LINE)`
   placement-rule pairing is unconstrained, so a placeable class is accepted with
   no extra `diagram_placement_rules` row.
2. Adds one new `panel` glyph to `metadata.symbology_icons` — a cabinet
   rectangle (rounded rect + two dividers + indicator dot), `{{COLOR}}`-tinted
   with explicit width/height like the ANSI stencils.
3. Adds `symbology_rules`: a mandatory **`map` default** (slate square) to clear
   the publish gate (a panel has no map footprint but the gate needs the row),
   a **`diagram` default** (slate panel block), and **six `panel_type`-scoped
   `diagram` overrides** (most-specific-wins on `domain_value_id`) colour-coded
   by role — PROTECTION orange, CONTROL blue, METERING cyan, RTU_SCADA violet,
   AUXILIARY_DC amber, MARSHALLING slate.

Nothing to compile — symbology rows and `is_diagram_placeable` are read
client-side/live. A dedicated `CONTROL_SCHEME` diagram_type, the link-authoring
UI, and a `POST /panels` fan-out were all explicitly left out of this slice
(the fan-out would reuse the Phase 42 assembly spine — see
docs/open-items.md's substation composite-assembly extraction).

## Slice 3 — panel schematic container (specced, optional)

Make `SUBSTATION_PANEL` a diagram container (`is_diagram_container=true`) with a
`PANEL_SCHEMATIC` so a panel face can be drawn with its relays/meters placed on
it, reusing the `METER_CHAMBER`/`MV_SWITCHBOARD` container + allow-list
machinery. Deferred until the inventory + mapping prove out.

## Verification owed (Bash blocked here — run locally)

- `task migrate` (applies 0083); confirm `metadata.relationship_classes`
  accepts `panel_bay` and existing rows still satisfy the CHECK.
- `task seed-substation-panel` then `compile CODE=SUBSTATION_PANEL`; confirm
  `gis.substation_panel` is created with the seven fields and no
  `geom`/`territory_id`, the `panel_type`/`control_mode`/`voltage_class`/
  `condition_rating` CHECKs match their domains, and `SUBSTATION_PANEL` shows up
  as a creatable `geometry_type='none'` class (diagram placement only).
- `task seed-substation-panel-containment`; confirm the `yard→panel`,
  `panel→relay`, `panel→meter` (`containment`) and `panel serves bay`
  (`panel_bay`, many_to_many) relationship_classes exist, and re-running is
  idempotent (guards hold, no dupes, no error).
- Round-trip: create a `SUBSTATION_PANEL`, contain a `PROTECTIVE_RELAY` in it
  while the same relay is bay-contained (both links coexist), and establish
  `SUBSTATION_PANEL serves BAY` for two bays (many_to_many accepts both).
- Slice 2 depiction: `task seed-substation-panel-symbology` (after
  `seed-substation-panel`); confirm `SUBSTATION_PANEL.is_diagram_placeable` is
  now true, the `panel` icon exists, and there is a `map` default rule plus a
  `diagram` default + six `panel_type`-scoped `diagram` rules. Re-running is
  idempotent (NOT EXISTS / ON CONFLICT guards). Then in the UI: `SUBSTATION_PANEL`
  is now offered as a placeable class, and dropping one on the yard's `ONE_LINE`
  renders the cabinet glyph colour-coded by `panel_type` (default slate before a
  type is set). No compile needed.
- api: `go build ./...`, `go vet ./...`; web: typecheck/build.

## Precedents copied

- [seed/bay_example.sql](../seed/bay_example.sql) — the none-geom `container`
  class with a functional-variant field (`bay_role` ↔ `panel_type`).
- [seed/bay_containment_example.sql](../seed/bay_containment_example.sql) — the
  guarded/idempotent containment relationship_classes shape.
- [migration 0082](../migrations/0082_widen_relationship_kind_cable_routing.sql)
  — the widen-the-kind-CHECK + three-allowlists-in-sync pattern.
- [seed/mv_metering_example.sql](../seed/mv_metering_example.sql) /
  [cable_routing](../seed/cable_routing_example.sql) — prior `many_to_many`
  associative (non-containment) relationship kinds.
