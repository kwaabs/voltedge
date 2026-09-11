# Phase 26 — optional feature geometry (map OR diagram OR both)

**Status: Slices 1–3 + the diagram-native relationship tool (S2 follow-up) code-complete (verification owed — see checklists).**

## Slice 2 follow-up — diagram-native relationship tool

The S2 Connect tool was connectivity-only; this adds the parallel **Relate** tool
so a relationship can also be authored natively from the diagram (previously only
reachable via the globally-mounted feature panel's "+ New relationship").

Implemented by **generalizing, not duplicating**
([`diagram-canvas.tsx`](../web/src/components/map/diagram-canvas.tsx)): the
`connectArming: boolean` from S2 became `armingMode: ConnectMode | null`, so one
piece of state holds *which* tool (connectivity or relationship) is live — the two
are mutually exclusive by construction, no chance of both armed at once. A shared
`armAuthoring(mode)` helper toggles/arms either tool (clearing the other and any
in-flight workflow), `selectOrPickTarget` starts the workflow in `armingMode`, and
the guide banner + crosshair cursor are driven off a derived `authoringMode` /
`authoringAwaitingSource`. New pieces: a **Relate** toolbar button (Link2 icon)
beside Connect, mode-aware banner copy ("relate from" / "relate to" vs "connect"),
and a mode-aware confirm-panel placeholder ("…to relate to" vs "…to connect to").

No backend change — relationships are established through the same shared
[`ConnectConfirmPanel`](../web/src/components/map/connect-confirm-panel.tsx) (which
already renders the relationship `kind` field and `POST`s to `/relationships`), fed
the same connect-store the map side uses. A diagram-only (`geometry_optional`)
feature can be an origin/destination identically — relationships, like
connectivity, don't touch geometry.

**Verification owed (Bash blocked — user runs locally):**

- [ ] `cd web && npx tsc -b` + lint — `armingMode`/`armAuthoring`/`ConnectMode`
  import/`Link2` all typecheck; no stale `connectArming`.
- [ ] With a diagram open: click **Relate** → banner "Click a feature to relate
  from", crosshair → click placement A (highlights connect-source) → click B →
  ConnectConfirmPanel shows the relationship **Kind** field (no terminal dropdowns)
  → confirm → relationship established, visible on both features' Relationships tab.
- [ ] Arming Connect then Relate (and vice-versa) swaps cleanly — never both
  active; Esc disarms; toggling the active tool cancels.
- [ ] Relate a diagram-only feature to another placement and confirm it appears on
  the Relationships tab.

---

## Slice 3 — clear-to-NULL (demote a map feature to diagram-only)

Goal: take an existing feature that has map geometry and **remove it**, so the
feature keeps living purely on diagrams (and in the network) — the reverse of a
normal create, and the last piece that lets one `geometry_optional` class flow a
feature between map-only, diagram-only, and both.

**Backend — one new geometry state on the shared update path** (all in
[`crud`](../api/internal/crud)):

- The PATCH body's geometry now has **three** states, not two:
  `absent` = leave it, a **value** = replace it, and an explicit **JSON `null`** =
  clear it. `json.RawMessage` preserves the literal `null`, so
  [`update.go`](../api/internal/crud/update.go) distinguishes `clearGeometry`
  (exactly the 4 bytes `null`) from `hasGeometry` (a real value) from neither
  (untouched). This is the whole request surface — no new field.
- **Guards** (clean 400s, not raw PG errors): clearing is rejected on a
  `geometry_type = 'none'` class (no geom column to clear), on a class **without**
  `geometry_optional` (its geom is `NOT NULL` — the add-only philosophy: you must
  opt the class in first), and when a `territory_id` override rides along with a
  clear (a feature off the map belongs to no territory).
- **Territory**: clearing forces `territory_id = NULL` (`assignTerritory` true,
  `territoryID` nil) — the feature is no longer anywhere on the map.
- [`sql.go`](../api/internal/crud/sql.go)'s `updateFeature` gained a
  `clearGeometry bool` param → emits `geom = NULL`, mutually exclusive with a
  value set. Only reachable for a `geometry_optional` class, whose compiled geom
  column is nullable (Slice 1).
- Published features: `validateUpdateCompleteness` only checks required
  **attributes**, never geometry, so clearing on a published feature is clean.
- Because `UpdateFeature` is the single shared front door, REST, the MCP
  `update_feature` tool, and field-sync all get the semantics for free.

**Frontend — a confirmed "Remove map geometry" action** on the feature panel
([`feature-panel.tsx`](../web/src/components/map/feature-panel.tsx)):

- A new **"Remove map geometry"** item (MapPinOff icon) in the feature's *More
  actions* menu, grouped with Duplicate. Gated on `canRemoveGeometry`:
  the class is `geometry_optional`, the feature actually has geometry right now,
  and it's not soft-deleted.
- Opens [`RemoveGeometryDialog`](../web/src/components/map/remove-geometry-dialog.tsx)
  — an AlertDialog (won't close on outside click / mid-request, same as
  DeleteFeatureDialog) that `PATCH`es `{ geometry: null }`, then invalidates the
  `["feature", …]` query and calls `useMapRefreshStore.bump()` — the exact refresh
  pair a geometry edit uses, so the now-geom-null feature (which the server tile
  function already excludes via `geom IS NOT NULL`) vanishes from the map without a
  reload. **Selection is kept**: the feature still exists, and the panel re-renders
  in its geometry-less state (no Length/Area, Territory → Unassigned, Duplicate
  disabled).

**NOT in Slice 3 (deliberate):** a re-place-on-map affordance (add geometry back
to a geom-null feature from the panel) — the edit form only commits a *modified*
existing geometry, so re-placing is a separate follow-up. The dialog copy is
careful not to promise a one-click undo.

**Verification owed (Bash blocked — user runs locally):**

- [ ] `cd api && go build ./...` — the `updateFeature` signature change +
  `bytes` import in update.go.
- [ ] `cd web && npx tsc -b` + lint — new dialog, `MapPinOff` import,
  `canRemoveGeometry`.
- [ ] On a `geometry_optional` class with a placed feature that's also on a
  diagram: More actions → Remove map geometry → confirm → feature disappears from
  the map, panel stays open showing no geometry, the diagram placement still
  renders, and its terminal connections/relationships are intact.
- [ ] `PATCH {geometry:null}` on a **non**-`geometry_optional` geometric class →
  400 "can't be cleared"; on a `geometry_type='none'` class → 400 "no map geometry
  to clear"; with `territory_id` alongside → 400.
- [ ] Trace still walks through the now-diagram-only feature (connectivity is
  topological — clearing geom doesn't drop it from the network).
- [ ] **Mobile regression check**: confirm the field tool's `update_attrs` sync
  payload never serializes `geometry: null` for an attribute-only edit (it
  shouldn't include a geometry key at all) — otherwise a field edit could
  inadvertently clear a feature's map geometry through the shared UpdateFeature.

---

## Slice 2 — diagram-native connection authoring

Goal: author an electrical connection **from the diagram**, so a diagram-only
(`geometry_optional`) device — which has no map geometry to snap to — can be made
a real trace node by connecting it to other placements.

**Key finding — most of it already existed.** Connectivity is topological
([`connectivity/export.go`](../api/internal/connectivity/export.go), keys on
`{feature_class, feature_id, terminal}`), the API is complete
(`POST /connectivity`), and the diagram canvas **already**: picks a placement as a
connect **target** (`selectOrPickTarget`, mirroring the map click handler),
highlights connect-source/connect-target placements with the same colors as the
map (`roleFor`), and shares the global
[`ConnectConfirmPanel`](../web/src/components/map/connect-confirm-panel.tsx)
(terminal dropdowns from the schema + loop check + `POST /connectivity`), which is
mounted on the map page alongside the diagram. So a connection could already be
authored from the diagram via *select a placement → feature panel "+ New
connection" → click a second placement → Confirm*. **No backend change.**

**What Slice 2 added — the diagram-native entry point + polish** (all in
[`diagram-canvas.tsx`](../web/src/components/map/diagram-canvas.tsx)):

- A **Connect tool** (Cable icon) in the canvas header. Arming it (`connectArming`
  state) makes the next placement click the connection's **source**
  (`connectStore.start("connectivity", …)`); the click after that falls into the
  existing `pickTarget` branch — so the whole "connect these two placements"
  gesture happens on the diagram without the map/panel detour. Clicking the tool
  again (or a mid-flight workflow) cancels; it also clears the other single-shot
  diagram modes when arming so two crosshair tools aren't armed at once.
- A **top-center guide banner** for both phases ("Click a feature to connect
  from" / "Click a second feature to connect to"), matching the existing
  digitize/place banners, with Cancel + Esc.
- **Esc** disarms the source phase (the target phase's Esc is already handled
  globally by voltedge-map.tsx's connect-store Escape); **crosshair cursor** while
  arming or picking.
- Copy fix in `ConnectConfirmPanel`: the target placeholder now reads "on the map
  or diagram" instead of "on the map".

Terminal selection, the network-rule authorization gate, loop check, and query
invalidation are all the shared confirm-panel path — a diagram-only feature
participates identically because `resolveEndpoint` never touches geom.

**NOT in Slice 2 (deliberate):** drawing a *direct* connector line between two
connected point placements (e.g. fuse→busbar with no conductor between them). The
diagram already renders conductor placements from their real connectivity, but a
device-to-device link with no placed conductor is intentionally not auto-drawn
(the canvas comment: "the diagram no longer auto-draws a generic connector for
connectivity that isn't backed by an actual placed conductor"). Rendering those is
a separate visual concern, not authoring — candidate follow-up. The connection is
real in topology/trace and visible on each feature's Terminals tab immediately.

**Verification owed (Bash blocked — user runs locally):**

- [ ] `cd web && npx tsc -b` + lint — new state/tool/banner typecheck; `Cable`
  import resolves.
- [ ] With a diagram open: click the Connect tool → banner appears, cursor is
  crosshair → click placement A (highlights as connect-source) → click placement B
  → ConnectConfirmPanel shows both with terminal dropdowns → pick terminals →
  Confirm → connection established (toast/no error), both features' Terminals tabs
  show it.
- [ ] Connect a **diagram-only** (`geometry_optional`, no map geom) placement to
  another placement; confirm it establishes and then appears as a node in a trace
  that walks through it.
- [ ] Esc while armed disarms; Esc mid-workflow cancels (existing global handler);
  a network-rule-less pairing is rejected with the usual toast and the panel stays
  open to retry.

---

## Slice 1 — the `geometry_optional` flag + nullable geom (create/render)

Locked decisions: flag name `geometry_optional`; **add-only** in v1 (no
clear-to-NULL / demote yet — that's S3); diagram-side connection authoring
deferred to S2. Slice 1 is **create + render only, no connectivity work**.

**What shipped (grounded, by file):**

1. [`migrations/0052_add_geometry_optional_flag.sql`](../migrations/0052_add_geometry_optional_flag.sql)
   — `ALTER TABLE metadata.feature_classes ADD COLUMN geometry_optional boolean
   NOT NULL DEFAULT false` + column comment. Mirrors migration 0032's shape.
2. [`metadata/models.go`](../api/internal/metadata/models.go#L29) — `GeometryOptional
   bool \`bun:"geometry_optional"\`` on `FeatureClass`. `Load` /
   `ListFeatureClasses` / `ListAllFeatureClasses` all `.Model(...)`-scan, so the
   column auto-selects with no query change.
3. [`compiler/ddl.go`](../api/internal/compiler/ddl.go#L170) — `buildCreateTable`
   emits the geom column **nullable** when `fc.GeometryOptional` (drops the
   ` NOT NULL` suffix); default false keeps the historic `NOT NULL`.
4. [`compiler/compiler.go`](../api/internal/compiler/compiler.go) — recompile of an
   already-compiled geometric table runs `ALTER COLUMN geom DROP NOT NULL` when
   the flag is set (safe/relaxing, no-op if already nullable). Clearing the flag
   with null rows present is left to Postgres's own `SET NOT NULL` to reject
   loudly — matches `syncCheckConstraints`' narrowing philosophy.
5. [`crud/validate.go`](../api/internal/crud/validate.go#L71) — `validateGeometry`
   accepts an empty geometry when `fc.GeometryOptional` (a supplied one is still
   type-checked). Only reachable on create; update already guards `len>0`.
6. [`crud/create.go`](../api/internal/crud/create.go#L59) — territory assignment now
   keys on whether **this** write carries geometry (`GeometryType=='none' ||
   len(Geometry)==0` skips `territory.Assign`), so a diagram-only feature of a
   geometric class doesn't hit `territory.Assign` with an empty geom. `insertFeature`
   already omits the geom column when geometry is empty; `formatRow` already omits
   the `geometry` key when `geom_json` is NULL — no change needed there.
7. Admin write/read surface: `geometry_optional` added to
   [`admin/types.go`](../api/internal/admin/types.go) (update req/resp, summary,
   create req), handled in [`admin/update.go`](../api/internal/admin/update.go)
   (pure-metadata toggle, any status) + [`admin/create.go`](../api/internal/admin/create.go),
   surfaced in [`admin/list.go`](../api/internal/admin/list.go).
8. Public catalog: `geometry_optional` added to `featureClassResponse` in
   [`cmd/server/main.go`](../api/cmd/server/main.go#L216) so `/feature-classes`
   carries it.
9. Frontend: type on
   [`use-feature-classes.ts`](../web/src/hooks/use-feature-classes.ts) +
   [`use-admin.ts`](../web/src/hooks/use-admin.ts); a checkbox + badge on the
   [Feature-Class detail page](../web/src/routes/admin-feature-class-detail.tsx)
   (shown only for non-`none` classes) and a checkbox on the
   [new-class wizard](../web/src/routes/admin-feature-class-new.tsx); and the
   diagram digitize filter
   [`diagram-canvas.tsx`](../web/src/components/map/diagram-canvas.tsx) widened
   from `geometry_type==='none'` to also include `geometry_optional` classes, so
   such a class is offered in the in-diagram create tool. The create form already
   posts geometry only when the draft has it and creates the placement after, so
   the diagram-only create path needed no change.

**Verification owed (Bash blocked here — user runs locally):**

- [ ] `cd api && go build ./...` — the new struct field, DDL branch, compiler
  step, validator/create changes, admin + server DTOs compile.
- [ ] `cd web && npx tsc -b` + lint — new types/checkboxes/filter typecheck.
- [ ] Apply migration 0052; set `geometry_optional=true` on a spatial class (e.g.
  `SERVICE_CUTOUT`) via the detail page; recompile; confirm
  `\d gis.service_cutout` shows `geom` **nullable** and no error.
- [ ] From a diagram, create that class via the digitize tool with no map
  geometry → feature is created (no `geom`), placement appears, no territory
  error. Confirm it does NOT appear on the map (tile fn filters `geom IS NULL`).
- [ ] A normal spatial create (geometry supplied) on the same class still
  assigns a territory and renders on the map.
- [ ] Sanity: a class left `geometry_optional=false` still rejects an empty
  geometry with the friendly 400 and compiles `geom NOT NULL`.

---


## The observation

Today a feature that should appear on a **diagram** must first exist on the
**geographic map** — an implicit "AND": diagram existence is gated on map
existence. But real assets don't obey that. Some devices are placed on the map
*and* drawn on a schematic; some are only ever schematic; some are only ever
geographic. A **fuse** is the classic case — you often want it on the one-line
diagram without ever giving it a survey location, but you also want the *option*
to place it on the map later.

The goal is per-feature freedom within one class: **map only, diagram only, or
both** — chosen per feature, not baked into the class.

## Why the current model forces "map first"

`geometry_type` is compiled straight into each class's physical `gis.<code>`
table. For a `point`/`line`/`polygon` class the schema compiler emits the geom
column as **`NOT NULL`**:

- [`compiler/ddl.go:170-176`](../api/internal/compiler/ddl.go#L170-L176) —
  `geom geometry(<Type>, <srid>) NOT NULL`.

and the create validator rejects a missing geometry with a friendly 400:

- [`crud/validate.go:71-73`](../api/internal/crud/validate.go#L71-L73) —
  `"geometry is required for feature class %q"`.

So a point-class feature **cannot** exist without a map location — enforced in
two independent layers (DB `NOT NULL` + app validator). The only diagram-native
escape hatch is `geometry_type='none'` (the relay): that class gets **no geom
column at all** ([`ddl.go:170`](../api/internal/compiler/ddl.go#L170)). It's an
*all-or-nothing class switch* — which is exactly why it can't express "this fuse
is geographic, that fuse is schematic-only." `geometry_type` conflates two
distinct things: *does this class have a map column?* and *must every feature
fill it?*

## Everything downstream already tolerates a geometry-less feature

This is what makes the fix small. The plumbing beyond the geom column already
copes with, or ignores, a feature that has no geometry:

- **Placements never look at geometry.** `runtime.diagram_placements.feature_id`
  has no FK; `placeFeature` only checks the feature *exists* and is
  `is_diagram_placeable` ([`diagram/sql.go:250-252`](../api/internal/diagram/sql.go#L250-L252)).
- **Connectivity is topological, not geometric.** `EstablishConnection` keys on
  `{feature_class, feature_id, terminal}`
  ([`connectivity/export.go:56`](../api/internal/connectivity/export.go#L56));
  `resolveEndpoint` loads the feature row and validates the terminal without
  touching geom ([`connectivity/sql.go:83`](../api/internal/connectivity/sql.go#L83)).
  A geometry-less fuse can therefore be a real node in the network and in trace.
- **Map tiles already skip null geometry.** Martin serves per-class tile
  *functions* that filter `WHERE geom IS NOT NULL`
  ([migration 0038:67](../migrations/0038_add_tile_functions.sql#L67)); the read
  path only asks `ST_AsGeoJSON(geom)` for non-`none` classes. A null-geom row
  simply doesn't render on the map — no breakage.

So the *entire* thing standing between the current state and a diagram-only fuse
is that one `NOT NULL`.

## Design: one orthogonal class flag

Add a third capability axis to `metadata.feature_classes`, alongside the two that
already exist:

| Axis | Column | Question |
|---|---|---|
| Map surface | `geometry_type` (point/line/polygon vs none) | Does the class have a map geom column at all? |
| Diagram surface | `is_diagram_placeable` (migration 0032) | Can its features be placed on a diagram? |
| **Map required?** | **`geometry_optional` (new, default `false`)** | Must *every* feature carry a map location? |

`geometry_optional` does exactly one thing:

- **`false`** (default — every existing class) → compiler emits `geom … NOT NULL`.
  Nothing changes.
- **`true`** → compiler emits `geom …` **nullable**, and the create validator
  stops *requiring* geometry for that class (but still validates the GeoJSON type
  when geometry *is* supplied).

### What that unlocks — per feature

For a fuse: `geometry_type=point`, `is_diagram_placeable=true`,
`geometry_optional=true`. Then each individual fuse can be:

- **Both** — has geom (on the map) *and* a diagram placement.
- **Map only** — has geom, no placement.
- **Diagram only** — created with no geom, reachable only via its placement; give
  it a location later (an update) and it joins the map.

Fully backward-compatible: POLE stays `geometry_optional=false` (always
geographic), PROTECTIVE_RELAY stays `geometry_type=none` (always diagram-only).

### Why a flag beats the alternatives

- **A new `geometry_type` value** ("optional_point", …) would duplicate every
  point/line/polygon variant and explode the enum. A boolean is orthogonal — one
  flag covers all three geometry types.
- **A separate nullable-geometry side table** decouples storage but breaks the
  "one physical table per class" model the compiler and tile functions rely on.
  Not worth it.
- **Two classes (map-fuse + schematic-fuse)** duplicates fields, subtypes,
  symbology, and connectivity rules, and can't share identity — pure modeling
  debt.

## What changes (grounded, by file)

1. **Migration** — `ALTER TABLE metadata.feature_classes ADD COLUMN
   geometry_optional boolean NOT NULL DEFAULT false` + column comment. Mirrors
   `is_diagram_placeable` (migration 0032). No backfill (default is the existing
   behavior). A CHECK that it's only ever `true` for a non-`none` class is
   optional but sensible (a `none` class has no geom column to make optional).

2. **Metadata model / load** — `metadata.FeatureClass` += `GeometryOptional bool`
   (`bun:"geometry_optional"`); it rides `metadata.Load` with the rest of the row.

3. **Compiler — emit nullable geom.**
   [`ddl.go:170-176`](../api/internal/compiler/ddl.go#L170-L176): when
   `fc.GeometryOptional`, drop the `NOT NULL` from the generated geom column
   (fresh `CREATE TABLE`).

4. **Compiler — relax an existing column on recompile.** This is the one real
   new compiler behavior. Today the recompile branch is strictly additive (only
   `ADD COLUMN` + CHECK rebuilds — [`compiler.go:71-158`](../api/internal/compiler/compiler.go#L71-L158)).
   Add a reconciliation step: if `fc.GeometryOptional` and the geom column is
   currently `NOT NULL`, run `ALTER TABLE … ALTER COLUMN geom DROP NOT NULL` — a
   **safe, relaxing** alter (no row rewrite, always succeeds). Introspect current
   nullability via `information_schema.columns` (like `existingColumns` /
   `findCheckConstraint` already do), or simply run `DROP NOT NULL`
   unconditionally when the flag is set (it's a no-op on an already-nullable
   column).
   - **Narrowing fails loudly, by design.** If an admin flips the flag *back* to
     `false` while null-geom rows exist, re-adding `SET NOT NULL` errors naming
     the offending rows — the same "safe loud failure" philosophy as
     `syncCheckConstraints` ([`ddl.go:223-232`](../api/internal/compiler/ddl.go#L223-L232)).
   - **Tiles unaffected.** A `geometry_optional` class still has a geom column, so
     it still gets a Martin tile function ([`compiler.go:170-174`](../api/internal/compiler/compiler.go#L170-L174));
     the function's `geom IS NOT NULL` filter transparently omits diagram-only
     rows.

5. **Create validator — allow missing geometry.**
   [`crud/validate.go:62-85`](../api/internal/crud/validate.go#L62-L85): for a
   `geometry_optional` class, a *missing* geometry is allowed (the feature is
   unplaced / diagram-only); a *supplied* geometry is still type-checked. The
   `none`-class "don't send geometry" rejection stays as-is.

6. **Create path — territory assignment keys on geometry presence, not class.**
   [`crud/create.go:63-73`](../api/internal/crud/create.go#L63-L73): today it runs
   `territory.Assign` for any non-`none` class. For a `geometry_optional` feature
   created with *no* geometry, skip assignment (territory stays NULL) — the branch
   should test "did this write carry geometry?", not "is the class geometric?".
   The MCP and REST create paths share this and must both change.

7. **Update path — add or clear geometry later.** Giving a diagram-only fuse a map
   location (or removing one) is a normal update. Confirm
   [`crud/update.go`] permits setting geom on a previously-null row and (decision
   below) whether clearing it back to NULL is allowed. Adding geometry should also
   trigger territory assignment at update time.

8. **Admin — expose the flag.** `updateFeatureClass` already carries
   `is_diagram_placeable` ([`admin/update.go:48-51`](../api/internal/admin/update.go#L48-L51));
   add `geometry_optional` the same way (pointer field, three-state not needed —
   it's a plain bool). Frontend: a checkbox on the Feature Classes detail page
   next to "Can be placed on a diagram" (admin-feature-class-detail.tsx), guarded
   by `metadata:write`, with helper text ("Features may exist without a map
   location — e.g. a schematic-only fuse"). Recompile the class to apply.

9. **Frontend — let a diagram-only feature be created from within a diagram.**
   The digitize-onto-diagram tool currently offers **only** `geometry_type='none'`
   classes ([`diagram-canvas.tsx:1064-1067`](../web/src/components/map/diagram-canvas.tsx#L1064-L1067)).
   Widen that filter to also include `geometry_optional` classes, so you can stamp
   a diagram-only fuse. `feature-create-form.tsx` already posts geometry *only if
   the draft has it* ([:132-145](../web/src/components/map/feature-create-form.tsx#L132-L145)),
   so a no-geometry create already works end-to-end once the validator allows it.

## What does NOT change

- **`geometry_type` enum, placements, tile machinery, connectivity/trace schema.**
  No new geometry type, no placement columns, no side table.
- **The relay / `geometry_type='none'` path** stays exactly as-is for genuinely
  map-less classes.
- **Existing classes** — all default `geometry_optional=false`, identical
  behavior, no recompile required until an admin opts one in.

## Connectivity & trace for a geometry-less feature

Because connectivity is topological (§"already tolerates", above), a diagram-only
fuse participates fully: establish a connection to it by `feature_id` + terminal
exactly like any other feature, and trace walks the connection graph without
needing geom. Two honest caveats to design around:

- **How connections get *authored* for a diagram-only feature.** On the map you
  usually snap geometrically; a geometry-less feature has nothing to snap. The
  connection must be authored from the **diagram** side (the placement/slot UI, or
  an explicit "connect these two placements" action). The underlying
  `EstablishConnection` call is identical — only the *entry point* differs. Worth
  confirming the diagram UI can drive `EstablishConnection` directly.
- **Map trace visualization** naturally shows only geom-bearing features; a
  diagram-only node is in the topology but not drawn on the map. That's expected,
  not a bug.

## Decisions to confirm

1. **Flag name.** `geometry_optional` (default `false`) reads cleanly and is
   backward-compatible. Alternatives: `geometry_required` (default `true`),
   `allow_unplaced`. — *proposed: `geometry_optional`.*
2. **Clearing geometry back to NULL.** Allow a placed (map) feature to *lose* its
   location and become diagram-only? Simpler first cut: allow **add** (NULL → geom)
   but defer **remove** (geom → NULL) until asked. — *proposed: add-only for v1.*
3. **Diagram-side connection authoring** — in scope for this phase, or a
   follow-up? The create/render side works without it, but a diagram-only fuse
   isn't *useful* in trace until it can be connected. — *proposed: create/render
   in Slice 1, diagram-side connect in Slice 2.*

## Proposed slicing

- **Slice 1 — the flag + nullable geom (create/render).** Migration, model,
  compiler (emit + recompile DROP NOT NULL), validator + territory relaxation,
  admin flag + checkbox, widen the diagram digitize filter. Outcome: a fuse can
  be created diagram-only, placed on a diagram, drawn with its Phase 25 ANSI
  glyph, and later given a map location. **No connectivity work.**
- **Slice 2 — diagram-side connection authoring.** Let the diagram UI drive
  `EstablishConnection` between placements so a diagram-only device becomes a real
  trace node. (Mostly a frontend + thin wiring slice — the engine already
  supports it.)
- **Slice 3 (deferred) — clear-to-NULL / demote a map feature to diagram-only**,
  if wanted.

## Verification owed (Bash blocked here — run locally)

- Migration applies; `metadata.feature_classes.geometry_optional` exists,
  default `false`.
- Compile a fresh `geometry_optional=true` class → its `gis.<code>.geom` is
  **nullable**; it still gets a GIST index and a Martin tile function.
- Recompile an existing point class after flipping the flag → `geom` goes
  nullable via `ALTER COLUMN … DROP NOT NULL`; existing rows untouched; flipping
  it back with a null-geom row present fails loudly.
- `POST /features/{code}` with no geometry on a `geometry_optional` class → 201,
  no territory assigned; with geometry → 201, on the map, territory assigned.
  Same class without the flag → still 400 "geometry is required".
- On a diagram: digitize a diagram-only fuse (no map location) → it places and
  renders as the `ansi-fuse` glyph (Phase 25); it does *not* appear on the map.
- `go build ./...`, `tsc` / lint / `vite build` clean.
