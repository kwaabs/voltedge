# Phase 3 (slice 12) — edit an existing feature's geometry

**Goal:** let a user reshape or relocate an existing feature's geometry
from the map, through a toolbar whose tools depend on the feature's own
geometry type — the third of four planned CRUD slices (attribute edit
and delete confirmation done, this one, snapping still open).
**Status:** done and verified against a real Postgres/Martin/API/Vite
stack for both a point and a line feature. One real rendering bug was
caught and fixed during verification — see "How this was validated."

## Why this slice exists, and what it needed that didn't exist yet

The user's exact ask, quoted from the original four-part CRUD request:
*"modifying the feature's geometry. show a toolbar on which it will
contain geometry tools(specific for that feature). eg. move feature,
move vertex, delete vertex etc. tool bar should be specific for the
feature being modified. eg. the drawing tool bar for line features
should have certain controls that point features should not have...
this way user can modify geometry of a feature under modification then
commit the feature. user may decide to modify only feature and not the
attributes or vice versa or even both."*

Before writing any UI, a direct check confirmed real backend work was
needed: `PATCH /features/{code}/{id}` only ever accepted `attributes` --
its own comment said so explicitly ("Subtype and geometry are
deliberately not updatable in this pass"). Geometry doesn't share
subtype's problem, though (changing a feature's *subtype* raises "does
it still satisfy the new subtype's required fields" questions this
system doesn't answer yet; geometry has no such interaction) -- so
lifting that restriction for geometry alone was straightforward, not a
redesign.

## What was built

- **`api/internal/crud`** -- `updateRequest` gained an optional
  `Geometry json.RawMessage` field. `updateHandler` runs the exact same
  `validateGeometry` check `createHandler` already used, just skipped
  entirely when geometry is absent (absence means "not changing it" on
  an update, unlike create where it's always required). `updateFeature`
  (sql.go) gained a `geometry []byte` parameter and, when non-empty,
  builds a `geom = ST_SetSRID(ST_GeomFromGeoJSON(?), <srid>)` `SET`
  clause alongside whatever attribute columns changed -- one `UPDATE`,
  whatever subset of geometry/attributes was actually sent. Curl-tested
  directly against a real seeded `POLE` before any frontend code
  consumed it: a valid new geometry accepted with 200, a mismatched
  geometry type (`LineString` sent to a `POLE`) correctly rejected with
  400, same message format `validateGeometry` already produced for
  create.
- **`src/stores/edit-store.ts`** gained `geometryTool` (`"move" |
  "vertices" | null` -- which map interaction, if any, is attached) and
  `dirtyGeometry` (the GeoJSON a completed drag produced) -- exactly the
  extension its own doc comment anticipated back when attribute editing
  shipped (`docs/phase-3-edit-attributes.md`): both slices are really
  "this feature is under modification," attributes and/or geometry, one
  session, one Save.
- **`src/components/map/geometry-edit-toolbar.tsx`** (new) -- floating,
  centered above the map, shown for the whole edit session (reads
  `editing` directly, same self-contained pattern as
  `connect-confirm-panel.tsx`). Which buttons render is
  geometry-type-specific, per the user's own framing: a point only ever
  gets **Move** (there's no meaningful distinction between "move the
  feature" and "move its vertex" for a single point -- they're the same
  action); a line or polygon gets **Move** and **Edit vertices**, plus a
  hint once vertex-editing is active describing OpenLayers' own default
  gestures (see below).
- **`src/components/map/voltedge-map.tsx`** gained a dedicated
  edit-geometry `VectorLayer` (cyan, `editStyleForFeature` in
  `map-layers.ts` -- a color distinct from every other role this app
  already uses: point/line/polygon, draft, and all three highlight
  roles) holding one real OL `Feature` built from the feature's current
  geometry once per session (an `editSeededForRef` guard, same
  once-per-session pattern `fittedTokenRef` already used for the
  pan-to-feature effect, so a mid-session refetch can never stomp the
  user's in-progress drag). A second effect attaches OL's `Translate`
  interaction (`geometryTool === "move"`) or `Modify` interaction
  (`"vertices"`) to that single feature -- never both at once, since
  they'd fight over the same drag gestures. **No custom
  `deleteCondition`/`insertVertexCondition` on `Modify`** -- its
  defaults (alt+click to delete a vertex, drag a segment's midpoint to
  insert one) are exactly the behavior the toolbar's hint text
  describes; reading OL's own source directly confirmed this rather
  than assuming. Each interaction's end event converts the (in-place
  mutated) OL geometry back to GeoJSON -- reusing `geometryToGeoJSON`,
  the same conversion `Draw`'s `drawend` already used -- and hands it to
  `dirtyGeometry`.
- **`src/lib/map-layers.ts`**'s `styleForFeature` hides the real,
  tile-rendered copy of whichever feature is under edit for the whole
  session (same technique `deleted-features-store.ts` already
  established for a different reason -- an id-keyed check returning an
  empty `Style`), so there's never a stale copy sitting at the
  feature's last-saved position underneath the one the user can
  actually drag.
- **`src/components/map/feature-edit-form.tsx`** -- Save now sends
  whichever changed: attributes alone, geometry alone (via
  `dirtyGeometry`), or both, directly answering "user may decide to
  modify only feature and not the attributes... or even both." A
  successful save that included geometry also bumps
  `map-refresh-store.ts` -- the same cache-busting `bump()` the
  create-feature slice already established for "the browser's own
  vector-tile cache is still showing the old data" (Martin itself has
  no cache with `--cache-size 0` and genuinely reflects a real geometry
  `UPDATE` immediately, unlike the soft-delete case; it's the browser's
  own tile cache that needs the nudge here).

## How this was validated -- and a real bug this caught

Driven against a real stack via Playwright, with a real `POLE` (point)
and a real `MV_CONDUCTOR` (line), every seed feature's original
geometry recorded before touching it and confirmed byte-for-byte
restored afterward via `GET`.

Every scripted scenario passed -- Move-drag a point, Save, confirm the
real `PATCH` and the new position via `GET`; Cancel/Escape both discard
cleanly with no request sent; a line's Edit-vertices tool correctly
supports dragging an existing vertex, dragging a segment's midpoint to
insert one, and alt+click to delete one, in one continuous session;
Save persists the reshaped `LineString`; clicking a different feature
mid-edit is still blocked (the same guard the attribute-edit slice
already established); both themes render the toolbar and cyan styling
correctly -- but verification also caught a real, independent rendering
bug along the way:

**A stale "selected" halo lingered at the feature's pre-drag position
throughout an active drag**, visible as a pale ghost trace alongside the
live, correctly-updating cyan copy, on both the point and the line
test. Root cause, found by re-reading the existing highlight effect
rather than guessing: the map's pre-existing "selected feature" halo
(`docs/phase-3-map-aesthetics.md`) is built from `selectedFeature`, a
server-fetched, cached geometry that only refreshes when Save
invalidates its query -- so for the whole duration of a drag, before any
Save happens, that halo kept showing the feature's real, unmoved
position. This was invisible during the *attribute*-edit slice (nothing
on the map ever moved then) and only became observable once geometry
itself could change mid-session. Fixed by skipping the "selected" halo
entirely for whichever feature is currently under edit, since the cyan
edit-geometry copy already represents it correctly. Re-verified directly
afterward: no ghost trace on either the point-move or the line-vertex
drag test, confirmed via mid-drag and post-mouseup screenshots, not
just inferred from the code change.

A second detail from the same report -- a small orange dot near the
line's dragged vertex -- was investigated and ruled out as a real,
unrelated feature: the conductor's dragged vertex happens to sit at the
exact same coordinates as a real seeded `POLE`, confirmed via `GET`
returning identical coordinates for both, and via a baseline screenshot
(no selection, no edit in progress) showing the same dot in the same
place. Not a bug -- two real features that happen to be electrically
connected at that point, correctly both rendering there.

## Decisions made

- **Point features get only "Move," never "Edit vertices."** Directly
  from the user's own framing -- a single point has nothing to
  distinguish "move the feature" from "move its one vertex."
- **`Modify`'s defaults, not custom conditions.** Alt+click-to-delete
  and drag-a-midpoint-to-insert are exactly what OL already ships;
  building custom versions of behavior the library already provides
  correctly would be needless surface area.
- **The edit-geometry layer is a single mutable OL `Feature`, not
  rebuilt on every interaction change.** Switching from Move to Edit
  vertices mid-session (or back) continues from wherever the feature
  currently is, not from its last-saved position -- matches the mental
  model of one continuous edit session the user described.
- **Hiding the real tile-rendered feature for the whole session, not
  just while a tool is actively engaged.** A session with the toolbar
  open but no tool picked yet is still "this feature is under
  modification" -- showing both the plain style and an idle cyan copy
  side by side would be a confusing, unnecessary transition.

## What's still open

- **Snapping** (slice D) -- the last of the four planned CRUD slices,
  depends on this one's toolbar existing, which it now does.
- **Polygon vertex editing** -- exercised only for a line in this
  verification pass (the seed database has no `SUBSTATION_YARD`
  instances currently); the same `Modify` interaction and
  `editStyleForFeature` branch already cover polygons, but it's worth a
  real check once a seeded polygon exists.
- **The real Martin/tile-filtering gap** (unrelated to this slice,
  carried over from `docs/phase-3-delete-confirmation.md`) -- geometry
  updates don't hit this problem (Martin correctly reflects a real
  `UPDATE`), only soft-deletes do.

## Try it yourself

Same stack as `docs/phase-3-delete-confirmation.md`'s "Try it
yourself" -- `task infra-up && task migrate && task api-dev`, then `cd
web && npm run dev`. Open `http://localhost:5173`:

- Select a feature, click the pencil Edit button. A toolbar appears
  above the map, and the feature turns cyan.
- Click Move and drag the whole feature, or (line/polygon only) click
  Edit vertices and drag a vertex, drag a segment midpoint to add one,
  or Alt+click a vertex to remove it.
- Save (in the Attributes tab, still open) commits whatever
  changed -- attributes, geometry, or both. Cancel or Escape discards
  everything.

`npm run build && npm run lint` and `go build ./... && go vet ./... &&
gofmt -l .` should all finish clean.

## Polygon re-verification, added later

This doc's own vertex-editing work only ever exercised a line feature
-- no polygon existed in the demo dataset at the time. Tracked as an
open item since. `SUBSTATION_YARD` ("Main Substation Yard") is now a
real seeded polygon (Phase 5's diagram containers slice), so the gap
finally had something real to test against. Verified live (Playwright):
selected it, entered edit vertices, dragged the top-left vertex to a
new position, Saved, confirmed via a real `GET` that the new coordinate
actually persisted server-side rather than just moving on screen, then
restored the original geometry exactly (verified again via `GET`). No
bugs found -- the shared line/polygon Modify-interaction code this doc
already argued for held up on a real polygon with no changes needed.
