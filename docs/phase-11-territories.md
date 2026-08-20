# Phase 11 — hierarchical territories

**Goal:** every utility operates over a nested set of administrative areas
-- `utility → region → district → area → plot` by default -- and every
geographically-placed feature must ultimately belong to **at least a
district**. This phase introduces the territory tree, attaches every
geographic feature to one, and auto-assigns that attachment by spatial
containment so it stays honest with the feature's geometry.

**Status:** in progress, built in slices (see below). Backend verification
is owed as copy-paste commands -- this environment can't spawn a shell, so
`go build ./...`, `go vet ./...`, and `task migrate` have not been run
here; each slice notes what to run.

## Design decisions (locked before any code)

These were settled up front, grounded in a full read of the existing
architecture (there was no prior organization/tenant/region/boundary
concept anywhere -- this is greenfield):

1. **Levels are an admin-configurable catalog, not a hardcoded enum.**
   A `gis.territory_levels` table defines the ordered levels and which one
   is the minimum a feature must reach. Renaming or re-depthing the
   hierarchy is a data change, matching how feature classes / domains are
   already data, not code.

2. **Assignment is hybrid: spatial auto-assign with manual override.** On
   feature create/edit the deepest territory whose boundary contains the
   feature is resolved and stored; an explicit `territory_id` on the write
   API overrides it. This introduces the repo's *first* spatial-containment
   queries (`ST_Contains` had only ever appeared in design docs). *(Slice E
   refinement: territories are fixed reference data with no in-app
   authoring, so auto-assign is the only path the UI exercises — the
   override remains a backend-only escape hatch, not a form picker.)*

3. **Territories live in the `gis` schema.** They carry real polygon
   geometry and sit beside the physical feature tables the containment
   query runs against.

4. **Straddle rule: centroid containment.** A line/polygon feature that
   crosses a boundary is assigned by which territory contains its
   *centroid* (`ST_Centroid` + `ST_Contains`) -- always resolves to
   exactly one, uniform across point/line/polygon.

5. **No match ⇒ reject the write (once enforcement is on).** A geographic
   feature whose centroid falls outside every district-or-deeper territory
   is rejected, guaranteeing the "at least a district" invariant. This
   requires complete district coverage before enforcement is switched on.

6. **The association is a plain `uuid` system column, not a DB foreign
   key.** `territory_id` is added to every physical table with geometry by
   the compiler, validated in Go -- the same no-enforced-join convention
   as `created_by` / `feature_connectivity.from_feature_id`, keeping the
   containment logic in the app.

7. **Structural integrity is enforced in Go.** A child's level must be
   exactly one rank below its parent's, and a child's boundary must fall
   within its parent's -- validated in `api/internal/territory`, not with
   Postgres triggers.

8. **Rollout is staged so existing data never breaks.** The `territory_id`
   column ships nullable; the "must reach a district" rule only starts
   *rejecting* once an enforcement flag is flipped -- after territories are
   authored and existing rows are backfilled by spatial assignment. Editing
   a territory boundary never silently re-assigns already-placed features;
   reassignment is an explicit action.

## Slices

The build is sliced so each lands a reviewable, independently-correct
piece:

- **Slice A — schema.** `migrations/0027`: the two `gis` tables + seed. ✅
- **Slice B — territory package.** Go models, load/CRUD, and the
  centroid/deepest-match spatial resolver. ✅
- **Slice C — compiler + CRUD plumbing.** Add the `territory_id` system
  column to geometric physical tables; thread it through read/write. ✅
- **Slice D — enforcement.** `territory.Assign` wired into the CRUD write
  path (create + update), behind a runtime `territory_enforcement_enabled`
  flag seeded off. ✅
- **Slice E — reference data + read-only surfacing.** Territories are
  *fixed* administrative reference data (no in-app authoring, no picker):
  a seed migration loads the tree with boundaries, and the feature panel
  shows each feature's auto-assigned territory as a read-only label. ✅
- **Slice F — territory write API + RBAC.** Reverses the "no authoring UI"
  call: create/rename/re-parent/delete + boundary edit endpoints, a new
  `territories:write` permission (admin/break-glass only), re-parenting with
  cycle detection, and a GeoJSON boundaries endpoint for the overlay. ✅
- **Slice G — admin territories page.** A tree view under Admin to
  create/rename/re-parent/delete territories (structure; boundary authoring
  is Slice I). ✅
- **Slice H — map boundary overlay.** A toggleable territory-boundary layer
  fed by `GET /territories/boundaries`; clicking a polygon opens a read-only
  territory panel (name, level, children). ✅
- **Slice I — in-map boundary authoring.** Extend the OpenLayers pipeline
  for MultiPolygon and add a dedicated territory-edit mode wired to the
  PATCH boundary endpoint. ✅
- **Slice J — per-level overlay toggles.** Split the single "Territories"
  overlay switch into per-level checkboxes so one level (e.g. districts)
  can be hidden while the rest stay drawn. ✅

> **Course reversal (Slices F–I).** Slice E locked "territories are fixed
> reference data, no in-app authoring". In use that proved too strict:
> there was no way for an administrator to manage the tree or fix a boundary
> without a migration, and boundaries weren't visible on the map at all.
> Slices F–I reintroduce a **guarded** authoring surface — gated by a
> dedicated `territories:write` permission granted only to admins — plus a
> read overlay. Auto-assignment (Slice D) is unchanged and still the only
> path a feature save takes; territory management is a separate admin act.

## Slice A — schema (`migrations/0027_create_territories.sql`)

Two tables in `gis`:

- **`gis.territory_levels`** — `code`, `name`, `rank` (unique, 0 = top),
  `is_min_feature_level` (bool). A partial unique index
  (`territory_levels_one_min_feature_level`) enforces at-most-one minimum
  level; the seed guarantees exactly one (`district`, rank 2). The catalog
  seeds `utility/region/district/area/plot` as a changeable default.
- **`gis.territories`** — self-referential adjacency list: `parent_id`
  (null at root), `level_id`, `name`, `geom geometry(MultiPolygon, 4326)`
  (nullable until drawn; MultiPolygon because real admin boundaries have
  exclaves). GiST index on `geom`, plus `parent_id` / `level_id` indexes.
  Seeds a single root `Utility` territory (single-tenant today).

The invariants a single-row `CHECK` can't express (parent one rank up;
child boundary within parent) are deferred to Slice B's Go validation, per
design decision 7.

**Verify (owed):** `task migrate` (applies 0027), then confirm the five
levels and one root territory seeded:
`psql -c "SELECT code, rank, is_min_feature_level FROM gis.territory_levels ORDER BY rank;"`
and `psql -c "SELECT name, parent_id FROM gis.territories;"`.

## Slice B — the `territory` package (`api/internal/territory`)

A new read/write package over the two tables, matching the shape of the
existing `metadata` load layer and the raw-SQL geometry handling in
`api/internal/crud`:

- **`models.go`** — `Level` and `Territory` bun models. `Territory.geom`
  is never mapped as a writable bun column (the geometry type can't
  round-trip a plain bind param); reads pull it back as GeoJSON text into
  a `scanonly` `GeomGeoJSON` field via `ST_AsGeoJSON`, and writes go
  through raw SQL wrapping `ST_GeomFromGeoJSON`, exactly as feature geom
  already does.
- **`territory.go`** — `ListLevels` / `GetLevel` / `MinFeatureLevel`,
  `ListTerritories` / `GetTerritory`, and `CreateTerritory` /
  `UpdateTerritory` / `DeleteTerritory`. `validatePlacement` enforces the
  two structural invariants a single-row CHECK can't: a child's level must
  be exactly one rank below its parent's (root ⇒ rank 0), and a child's
  boundary must fall within its parent's (`ST_Contains`, only when both
  have geometry). `DeleteTerritory` refuses a node with children rather
  than letting the self-FK raise a raw 500. Re-parenting/re-leveling is
  deliberately out of this slice (moving a node re-opens the subtree's
  validation). `InvalidError` + `IsInvalid` give callers a clean 400/500
  split, mirroring `crud`'s `validationError`.
- **`resolve.go`** — `ResolveContaining`, the centroid/deepest-match
  point-in-polygon query (the repo's first). It mirrors `crud`'s
  `ST_SetSRID(ST_GeomFromGeoJSON(...), srid)` construction, then
  `ST_Transform`s to 4326 to compare against boundaries stored in 4326.
  (Validating that a manual override is deep enough lives with the
  write-time decision in Slice D's `assign.go`, not here.)

Nothing calls this package yet -- Slice C adds the `territory_id` column
and Slice D wires the resolver into the feature write path.

**Note on the override rule (settled here, applied in Slice D):** a
manual `territory_id` override is validated as *exists + at least the
minimum level* only -- it is **not** required to spatially contain the
feature's centroid. The override exists precisely for the cases
auto-assignment can't serve (a boundary that's missing or imperfect), so
demanding containment would defeat it.

**Verify (owed):** `go build ./...` and `go vet ./...` (compile only --
nothing invokes the package until Slice D).

## Slice C — compiler + CRUD plumbing (the `territory_id` column)

Every geometric physical table gains a nullable `territory_id uuid` system
column, following the exact rail `created_by`/`updated_by` rode:

- **`compiler/ddl.go`** — `buildCreateTable` emits `territory_id uuid` for
  a first-time compile, guarded on `geometry_type != 'none'` (a
  geometry-less class has nothing to place, so it never gets the column).
- **`compiler/compiler.go`** — the recompile path backfills it onto a
  table compiled before the column existed, mirroring the `created_by`
  backfill, so the compiler stays the single source of truth for a table's
  shape.
- **`migrations/0028_add_feature_territory_column.sql`** — loops
  `metadata.feature_classes` and `ALTER TABLE ... ADD COLUMN IF NOT EXISTS
  territory_id uuid` for every already-compiled geometric class, the
  deploy-time twin of migration 0026.
- **`crud/sql.go`** — `selectColumns` reads `territory_id` back on
  geometric classes (alongside `geom_json`), and `formatRow` surfaces it at
  the top level of the response (absent entirely for geometry-less
  classes). Plain uuid, **not** a FK to `gis.territories` -- the same
  no-enforced-join convention as `created_by`.

No value is written or required yet -- `territory_id` reads back `null` on
every existing row until Slice D computes and enforces it.

**Verify (owed):** `task migrate` (0028), `go build ./...`, then recompile
an existing geometric class (e.g. `POST /admin/feature-classes/pole/compile`)
and confirm a `GET /features/pole/{id}` response now carries
`"territory_id": null`.

## Slice D — enforcement (the write-time decision)

The `territory_id` column now gets *populated* and, behind a flag,
*required*. All of the decision logic lives in one place --
`territory.Assign` -- which the CRUD engine calls on every geometric write:

- **`territory/assign.go`** — `Assign(ctx, db, geomGeoJSON, srid, override)`
  returns the `territory_id` to store (or `nil` = leave unassigned), or an
  `InvalidError` to reject the write with a 400. Two paths:
  - **Override** (`territory_id` supplied): validated as *exists + at least
    the minimum level* — **always**, regardless of the enforcement flag,
    because a too-shallow override is a deliberate user error worth
    rejecting even during rollout. It is *not* required to spatially contain
    the feature (see the Slice B override note).
  - **Auto** (no override): `ResolveContaining` finds the deepest territory
    whose boundary contains the feature's centroid. A qualifying match (≥
    minimum level) is **always stored**, even with enforcement off — that's
    how coverage gets populated. The flag governs only the *failure* case:
    "no territory covers this" / "only a too-shallow one does" rejects when
    ON, and simply leaves the feature unassigned (`nil`) when OFF.
  - `EnforcementEnabled` reads `iam.system_settings.territory_enforcement_enabled`,
    failing **permissive** (missing/unparseable ⇒ off) so the invariant
    never rejects a write before coverage exists.
- **`crud/handler.go`** — `createHandler` runs `Assign` for every geometric
  class (passing `req.TerritoryID` as the override) and stores the result;
  `updateHandler` re-resolves only when there's a reason to — an explicit
  override *or* a geometry change that could move the feature across a
  boundary — leaving the existing assignment untouched otherwise. A
  `territory_id` sent to a **non-geometric** class is a 400 (it has no such
  column). `territoryError` maps `territory.InvalidError` to `crud`'s own
  `invalid` so a bad/too-shallow territory surfaces as 400, not 500.
- **`crud/sql.go`** — `insertFeature` writes `territory_id` when non-nil;
  `updateFeature` takes an `assignTerritory` bool so a resolved-to-`nil`
  auto-assignment can explicitly `SET territory_id = NULL` (distinct from
  "don't touch it"), and counts that as a real change so a territory-only
  PATCH doesn't trip the nothing-to-change guard.
- **`migrations/0029_seed_territory_enforcement_flag.sql`** — seeds
  `territory_enforcement_enabled = 'false'` into `iam.system_settings`
  (`ON CONFLICT DO NOTHING`), reusing the same runtime key/value store as
  `local_auth_enabled`. Flip it to `'true'` once districts fully cover the
  service area and existing rows are backfilled.

**Backfill.** Existing feature rows keep `territory_id = null` until
touched. With enforcement off, re-saving a feature (or a future bulk
re-resolve pass) populates it by spatial assignment; enforcement should
only be switched on after that coverage exists — otherwise the first edit
of an uncovered feature is rejected.

**Verify (owed):** `go build ./...` and `go vet ./...`; `task migrate`
(applies 0029). Then, with the flag **off** (default): author a district
territory with a boundary, `POST /features/pole` with geometry inside it and
confirm the response `territory_id` is that district; `POST` with geometry
outside every territory and confirm it still succeeds with
`"territory_id": null`. Flip the flag on
(`UPDATE iam.system_settings SET value = 'true' WHERE key = 'territory_enforcement_enabled';`)
and confirm the outside-coverage create now returns 400. Confirm a
`territory_id` override pointing at a region (too shallow) returns 400 even
with the flag off, and that sending `territory_id` to a non-geometric class
returns 400.

## Slice E — reference data + read-only surfacing

Slice E resolves a question the original plan got wrong. The early sketch
imagined a territories **admin page** with in-app boundary drawing and a
**territory picker** on the feature form. But territories are *fixed
reference data* — official administrative boundaries that come from an
authoritative source and don't change through normal app use — so there is
**no authoring UI and no picker**. Auto-assignment (Slice D) already does
the whole job on save; the picker was only ever the override path, and the
override stays a backend-only escape hatch (`territory_id` on the write
API), reachable by an integration but not surfaced in the UI.

That leaves Slice E with two pieces:

- **`migrations/0030_seed_territory_boundaries.sql`** — seeds a starter
  tree *with boundaries* beneath the root `Utility` (0027 seeded only the
  level catalog and the bare root). The polygons are **illustrative
  placeholders** — simple rectangles anchored around the web map's default
  center (`voltedge-map.tsx` `DEFAULT_CENTER`, central London) so a feature
  drawn there actually resolves and the assignment/enforcement path can be
  exercised end-to-end — strictly nested (region ⊃ district ⊃ area) so the
  tree is geometrically consistent. Replace them with the authoritative
  dataset before enabling enforcement in anger. (Chosen over a CLI importer:
  the data lives with the schema, versioned, like the level catalog.)
- **Read-only territory label on the feature panel.** A feature carries a
  bare `territory_id` uuid; the panel resolves it to a name:
  - **`api/internal/territory/http.go`** — a read-only `GET /territories`
    returning the tree as a flat, **boundary-free** list (`id`, `parent_id`,
    `name`, resolved `level_code`/`level_name`/`level_rank`). No
    create/update/delete surface — territories aren't authored here.
  - **`api/cmd/server/main.go`** — mounts it under the **`features:read`**
    gate (not the `/admin` metadata gate), since every map viewer must
    resolve the label, and boundaries are omitted so it's cheap to fetch
    whole and cache.
  - **`web/src/hooks/use-territories.ts`** — `useTerritories()` (long
    `staleTime`, reference data) + `territoryNameMap` (id → name).
  - **`web/src/components/map/feature-panel.tsx`** — the Attributes tab's
    `<dl>` gains a **Territory** row: the resolved name, `Unassigned` (muted)
    when the geometric feature has no territory, and nothing at all for a
    non-geometric class (whose `territory_id` is `undefined`). Falls back to
    a short id while the list is still loading or if an id isn't found.
  - **`web/src/hooks/use-feature.ts`** — `FeatureRecord` gains the optional
    `territory_id` (`null` = geometric-but-unassigned, `undefined` =
    non-geometric).

**Deferred (possible later slice):** a read-only tree *view* of the
territory hierarchy, and a boundary overlay on the map. Both can build on
`GET /territories` (the overlay fetching geometry on demand); neither is
needed to see a feature's assignment. *(These deferrals are taken up by
Slices G–I below.)*

**Verify (owed):** `go build ./...` and `go vet ./...`; `task migrate`
(applies 0030); `npm run typecheck && npm run lint` in `web/`. Then draw a
feature near the map's default center and confirm the panel's **Territory**
row reads `Whitehall Area` (deepest containing territory); draw one
elsewhere in Westminster (outside Whitehall) and confirm it reads
`Westminster District`; draw one far outside all boundaries (enforcement
off) and confirm it reads `Unassigned`. `GET /territories` should return
the seeded region/two districts/area with their levels and no `geom`.

## Slice F — territory write API + RBAC

Reopens authoring behind a permission. The read side (the label, the
overlay) stays on `features:read`; the mutating verbs require a new
`territories:write` capability.

- **`api/internal/auth/rbac.go`** — new `PermTerritoriesWrite =
  "territories:write"` constant (the Go half of the catalog contract).
- **`migrations/0031_seed_territories_write_permission.sql`** — inserts the
  `territories:write` catalog row and grants it to `admin` and
  `break-glass-admin` **only** (editors edit feature data, not the
  administrative-area tree). The grant is explicit because 0025's
  admin/break-glass CROSS JOIN already ran before this row existed.
- **`api/cmd/server/main.go`** — the `/territories` mount changes from
  `RequirePermission(features:read, features:read)` to
  `RequirePermission(features:read, territories:write)`: GET stays open to
  every viewer, POST/PATCH/DELETE require the admin capability. One mount,
  method-aware, no route collision with `/admin`.
- **`api/internal/territory/territory.go`** — `UpdateTerritory` now supports
  **re-parenting**. `UpdateParams` gains `ReParent bool` + `ParentID
  *string` (ReParent must be set for ParentID to apply, so "move to root"
  (`parent_id: null`) is distinct from "leave alone"). A move re-runs
  `validatePlacement` against the new parent — the node's level is kept, so
  the move is legal only to a parent exactly one rank above, and the moved
  subtree's internal ranks stay intact (only the node's own containment is
  re-checked; descendants remain within the node). `wouldCreateCycle` walks
  the proposed parent's ancestor chain and rejects a move under the node's
  own descendant. Re-*leveling* is still not offered (it would re-open every
  descendant's rank).
- **`api/internal/territory/http.go`** — the mount grows write verbs and two
  read helpers:
  - `POST /` create, `PATCH /{id}` update (name / re-parent /
    edit-or-clear boundary), `DELETE /{id}` delete a leaf. Geometry is sent
    as a nested GeoJSON geometry object; `writeError` maps `InvalidError` →
    400, everything else → 500.
  - `GET /levels` — the level catalog, for the create form's level picker.
  - `GET /boundaries` — every *drawn* territory as a GeoJSON
    `FeatureCollection` (properties carry id/parent/level), the source for
    the Slice H overlay. Undrawn territories are simply absent.

The write functions (`CreateTerritory`/`UpdateTerritory`/`DeleteTerritory`)
already existed from Slice B; this slice exposes them over HTTP and adds the
re-parenting they lacked. No feature-assignment behaviour changes.

**Verify (owed):** `go build ./...` and `go vet ./...`; `task migrate`
(applies 0031). As an admin: `POST /territories` `{level_id, name,
parent_id}` creates a node; `PATCH /territories/{id}` with `{"reparent":
true, "parent_id": "<other>"}` moves it (and is rejected 400 if the new
parent isn't exactly one rank above, or is the node's own descendant);
`DELETE /territories/{id}` refuses 400 if it has children. As a non-admin
(viewer/editor) confirm `GET /territories` still works but any write returns
403. `GET /territories/boundaries` returns a FeatureCollection of the seeded
boundaries.

## Slice G — admin territories page

A new admin surface (`/admin/territories`) to manage the tree, mirroring the
existing admin pages (FormDialog / ConfirmDeleteDialog, Query-hook
mutations, permission-gated route + sidebar entry).

- **`web/src/hooks/use-territories.ts`** — grows the write side:
  `useTerritoryLevels` (the level catalog for the create form),
  `useCreateTerritory` / `useUpdateTerritory` / `useDeleteTerritory`. Each
  mutation invalidates the territory list (and the boundary-overlay key, for
  Slice H) so the tree, the feature label, and the overlay all refresh.
- **`web/src/routes/admin-territories.tsx`** — renders the flat list as an
  indented tree (grouped by `parent_id`, depth-first). Per-row actions: add
  child, rename, move, delete. The **create** form derives the level from the
  chosen parent (a child is exactly one rank below; rank is unique per level)
  rather than offering a level picker that could be set wrong -- and blocks
  when the parent is already at the deepest level. The **move** form only
  offers parents exactly one rank above the node; being shallower, those can
  never be the node's own descendants, so the UI can't even propose a cycle
  (the server rejects one regardless). Delete is the shared confirm; the
  server's "has children" refusal surfaces as a toast.
- **`web/src/app.tsx`** — route gated by `RequirePermission
  perm="territories:write"`.
- **`web/src/components/layout/app-sidebar.tsx`** — a **Territories** entry
  (LandPlot icon) in the Admin group, `perm: "territories:write"`, so only
  admins see it.

Boundary *authoring* is deliberately not here -- the tree page manages
structure; drawing/editing a boundary on the map is Slice I. A territory
created here has no boundary until then (and is simply absent from the
overlay).

**Verify (owed):** `npm run typecheck && npm run lint` in `web/`. As an
admin, open **Admin → Territories**: the seeded tree renders nested; add a
district under a region, rename it, move it under another region, and delete
a leaf. Confirm a non-admin never sees the sidebar entry and hitting
`/admin/territories` directly shows the forbidden view.

## Slice H — map boundary overlay

A toggleable territory-boundary layer on the map, plus a read-only panel for
a clicked territory. Read-only by design: authoring lives on
`/admin/territories` (Slice G) and, for boundaries themselves, Slice I; this
slice is the *viewer* side of the overlay the "Yes, add overlay" decision
asked for.

- **`web/src/hooks/use-territories.ts`** — `useTerritoryBoundaries(enabled)`
  (queryKey `["territory-boundaries"]`, `GET /territories/boundaries`), typed
  `TerritoryBoundaryProps` / `TerritoryBoundaryCollection`. Gated on
  `enabled` (the overlay's visibility) so the large MultiPolygon payload is
  only fetched once the overlay is actually turned on; the Slice G mutations
  already invalidate this key.
- **`web/src/stores/territory-overlay-store.ts`** — one small zustand store:
  `visible` (persisted, like the feature-layer visibility) and a transient
  `selected` (the clicked territory's properties, for the panel). Turning the
  overlay off also clears any open panel.
- **`web/src/lib/map-layers.ts`** — `territoryStyleForFeature`: colors each
  boundary by its level rank (a blue→magenta ramp distinct from every feature/
  highlight/draft/edit role) with a translucent fill and a name label. Styles
  are cached per rank; the per-feature name is set on the shared style right
  before it's returned (OL's own label-per-feature idiom).
- **`web/src/components/map/voltedge-map.tsx`** — a client-side
  `VectorLayer`/`VectorSource` pair (not a Martin tile layer: territories are
  few, boundaries large) added right after the base layer so it renders above
  the basemap but below the feature tiles. `ol/format/GeoJSON` parses the
  boundaries (it handles MultiPolygon natively and reprojects 4326→3857 in one
  pass, unlike the hand-written Point/Line/Polygon converters). The click
  handler is extended: **features always win**; only a click that hits *no*
  feature, over a visible territory, opens the panel (deepest territory wins,
  since the boundaries nest); a click on true empty space clears both.
- **`web/src/components/map/territory-panel.tsx`** — the read-only inspector
  (bottom-left, clear of the feature panel): name, level, parent name, and
  immediate children, all from the store's `selected` plus the cached
  territory list — no per-territory fetch.
- **`web/src/routes/map.tsx`** — mounts `<TerritoryPanel />`.
- **`web/src/components/map/layers-panel.tsx`** — an **Overlays** section with
  a **Territories** checkbox bound to the overlay store.

**Verify (owed):** `npm run typecheck && npm run lint` in `web/`. On the map,
open **Layers → Overlays → Territories**: the drawn boundaries render as
colored, labeled polygons under the features. Click empty space inside one:
the read-only panel opens with the territory's name/level/children; clicking a
feature still selects the feature (overlay never steals it); clicking true
empty space closes the panel. Toggle the overlay off — the layer and panel
both disappear. (Boundaries only show for territories that *have* a boundary;
before Slice I that was seed/admin data only — now it's whatever an editor
draws.)

## Slice I — in-map boundary authoring

Draw and reshape a territory's MultiPolygon boundary directly on the map,
saving through the PATCH endpoint (geometry / clear_geom, already built in
Slice F). The existing feature edit pipeline only handled Point/Line/Polygon;
territories are MultiPolygon, so this uses `ol/format/GeoJSON` (which parses
and writes MultiPolygon natively) rather than extending the hand-written
Point/Line/Polygon converters.

- **`web/src/stores/territory-edit-store.ts`** — the authoring session:
  `editing` (which territory), `tool` (`draw` | `vertices`), and the
  map→toolbar signal `dirtyGeometry` + a distinct `cleared` flag (so
  "boundary removed" is told apart from "boundary untouched" — both leave
  `dirtyGeometry` null). Not persisted.
- **`web/src/hooks/use-territories.ts`** — `UpdateTerritoryBody` grows
  `geometry` (a GeoJSON Polygon/MultiPolygon) and `clear_geom`; the existing
  `useUpdateTerritory` carries them.
- **`web/src/components/map/voltedge-map.tsx`** — a dedicated edit
  source/layer (reuses the cyan `editStyleForFeature`, zIndex 8, above the
  overlay). One effect seeds a single MultiPolygon `Feature` from the
  territory's current boundary (read out of the same boundaries collection
  the overlay uses — forced on while editing — or an empty MultiPolygon for a
  never-drawn territory), guarded to seed once per session. A second attaches
  the picked interaction: **vertices** is a `Modify` over the rings;
  **draw** is a `Draw` (type `Polygon`, no source of its own) whose every
  finished polygon is `appendPolygon`ed onto the one MultiPolygon — so the
  whole boundary stays a single feature. Both write the result back as
  EPSG:4326 GeoJSON. Clear empties the geometry in place. A click during
  authoring feeds the interaction (never selection); Escape cancels.
- **`web/src/components/map/territory-edit-toolbar.tsx`** — floating
  top-center (like the feature geometry toolbar): Draw / Edit vertices /
  Clear / Cancel / Save, a per-tool hint, and the territory's name. Save
  PATCHes the staged change and closes; the server's parent-containment check
  surfaces as a toast, so an out-of-parent boundary never silently saves.
- **`web/src/routes/map.tsx`** — mounts `<TerritoryEditToolbar />`.
- **Entry points.** The read-only **territory panel** grows an *Edit
  boundary* button (only for `territories:write`), and each **admin tree** row
  gets a map-icon action that arms the session and navigates to the map —
  the only entry that works for a territory with *no* boundary yet (it's
  absent from the clickable overlay).

Snapping isn't wired into the territory interactions (the snap source is
built from feature tiles, not territory vertices); a boundary is drawn freehand
and validated server-side. Re-parenting/containment rules are unchanged —
editing a boundary re-checks it against the parent but never re-assigns placed
features or re-validates existing children (Slice F's `UpdateTerritory`).

**Verify (owed):** `cd web && npm run typecheck && npm run lint`. As an
editor: from **Admin → Territories**, click a district's map icon → the map
opens with its name in a top toolbar. **Draw** a polygon inside its parent →
it appears cyan; **Save** → toast-free success, and turning on the overlay
shows the new boundary. Re-open, **Edit vertices**, drag a vertex, Save.
**Clear** then Save removes it. Draw a polygon *outside* the parent → Save
surfaces the "falls outside its parent" 400 as a toast and stays open. Confirm
a viewer (no `territories:write`) sees no *Edit boundary* button on the panel
and the PATCH is refused server-side.

## Slice J — per-level overlay toggles

The Slice H overlay was one master switch: turning "Territories" on drew
*every* level (utility → region → district → area → plot) stacked at once,
with no way to hide just one. This adds a per-level toggle so, e.g.,
districts can be hidden while regions stay drawn.

- **`web/src/stores/territory-overlay-store.ts`** — adds `hiddenLevels`
  (`Record<level_code, boolean>`; absent/false = shown, so a fresh overlay
  shows everything) and `toggleLevel(code)`. Persisted alongside `visible`.
  Hiding the level of the currently-inspected territory also closes its
  panel — the same "don't strand an inspector you can't see" rule the master
  toggle already followed.
- **`web/src/components/map/layers-panel.tsx`** — under the master
  **Territories** checkbox (shown only while it's on) renders one indented
  checkbox per level from `useTerritoryLevels()`, top-first by rank, each
  with a swatch from `territoryColorForRank` so the row reads as the color
  drawn on the map.
- **`web/src/components/map/voltedge-map.tsx`** — the boundary-load effect
  filters the collection by `hiddenLevels` *before* features reach the
  source (and re-runs when the set changes). Because `territoryAtPixel` walks
  that same source, a hidden level also drops out of the click hit-test — no
  invisible-but-clickable polygons.
- **`web/src/lib/map-layers.ts`** — `territoryColorForRank` is now exported
  for the panel swatch (previously module-private).

The master switch still gates the (large) boundary fetch, so per-level
filtering is purely client-side over already-loaded data — flipping a level
never triggers a refetch.

**Verify (owed):** `cd web && npm run typecheck && npm run lint`. Turn on
**Territories**, confirm the level checkboxes appear indented below it. Uncheck
**District** → district polygons vanish (map *and* click) while regions stay;
clicking where a district was now selects the enclosing region instead. The
choice survives a reload. Master-off hides the level list and the whole overlay.

### Fix — territories were also drawn as a feature-class layer

Martin auto-discovers *every* geometry table in the `gis` schema and publishes
it as a tile source (`web/src/lib/martin.ts`); the map's catalog-driven effect
then builds a feature-class layer for each. Because `gis.territories` has a
`geom` column it was caught by this too — so the boundaries drew as an
always-on `territories` layer (it even showed up in **Layers → Feature
classes**), *regardless of the overlay toggle*. That defeated the whole
per-level overlay: territories showed even with every overlay box unchecked.

Fix: `featureClassSourceIds(catalog)` in `web/src/lib/martin.ts` drops the one
non-feature-class source (`TERRITORY_TILE_SOURCE_ID = "territories"`, matching
the table name) from the catalog before it's consumed — both by the layers
panel's feature-class list and the map's per-class tile-layer effect. The
overlay (fed by `GET /territories/boundaries`) is now the *only* path
territories are ever drawn by. Verify: territories no longer appear under
**Feature classes**, and with all overlay boxes off the map shows no
territory boundaries at all.
