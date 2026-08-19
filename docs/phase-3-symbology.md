# Phase 3 (closing item) — metadata-driven symbology resolution

**Goal:** close the biggest visible gap left from Phase 3 -- every
feature of a given geometry type rendered identically regardless of
feature class, the map's own "looks like a real GIS" problem
(`docs/open-items.md`) -- by resolving `metadata.symbology_rules`
client-side into a real per-class, per-attribute style, the way
`docs/metadata-core.md` originally designed it back in Phase 1.
**Status:** done and verified against a real Postgres/Martin/API/Vite
stack, including toggling a real switch's `device_state` and confirming
the map's own icon changed shape live, not just color. Two real bugs
surfaced during frontend verification and got fixed along the way --
see "How this was validated."

## Why this slice exists

`metadata.symbology_rules` (migrations/0012) has existed, schema-only,
since Phase 1 -- `docs/open-items.md` flagged it as the map loop's
central open item: "every feature of a given geometry type looks
identical regardless of feature class today... resolution is genuinely
multi-axis (class+subtype+domain-value specificity, zoom-gated,
map-vs-diagram context) and `icon_ref` implies a sprite pipeline that
doesn't exist yet." Three scope decisions got made explicitly before
writing code:

- **Real shapes, not color-only** -- procedural or SVG-based icon
  differentiation, not just tinting the same dot per class.
- **SVGs, not OL's programmatic shape primitives** -- a deliberate
  correction mid-design: `icon_ref` in the metadata design names an
  *asset reference*, and only an actual image (even a tiny hand-authored
  one) fits that concept -- a procedurally-drawn `RegularShape` isn't a
  "ref" to anything. This also makes `DIST_SWITCH`'s open/closed states
  a genuine shape swap (solid vs. hollow triangle) instead of a color-only
  stand-in for what the docs' own example calls a shape change.
  A real sprite-serving pipeline stays explicitly out of scope -- a
  small hardcoded registry (`symbology-icons.ts`) stands in for it.
- **The legend piggybacks on the existing LayersPanel** rather than
  becoming a new standalone component -- each layer-visibility row
  already names a feature class; showing its resolved icon right there
  needed no new UI surface.
- **Zoom-gating deferred.** `min_zoom`/`max_zoom` columns exist on
  `symbology_rules` but nothing in the seeded rules needs them yet;
  wiring them in is cheap to add later once a real rule does.
- **`render_context = 'diagram'` deferred** -- no `diagram_instance`
  exists yet (that's Phase 5), so only `map`-context rules are resolved
  or even sent to the frontend this pass.

## The key fact that made this buildable in one pass

Before writing any code: does Martin's auto-discovered tile source
expose a feature's real attribute columns (`subtype`, `device_state`,
...), or only geometry + id? This determines whether per-attribute
symbology (e.g. "open switches look different from closed ones") can
be resolved from a rendered tile feature directly, or needs an extra
fetch per feature -- which would defeat the point of fast map
rendering. A real tile was fetched and decoded directly: Martin's
auto-discovery mode publishes **every column** as MVT feature
properties, not just geometry. `docs/metadata-core.md`'s own framing
("Martin never reads `symbology_rules`... every icon decision happens
client-side, resolved from the same published metadata cache") turns
out to already be fully supported by the tile format itself -- nothing
about Martin needed to change.

## What was built

### Backend

- **`api/internal/metadata/models.go`** -- `SymbologyRule`, mirroring
  `migrations/0012` (`subtype_id`/`domain_value_id` nullable,
  `style_def jsonb`).
- **`api/internal/metadata/load.go`** -- `LoadSymbologyRules` loads
  every rule for a feature class, both render contexts included; the
  caller filters.
- **`api/cmd/server/main.go`** -- `GET /feature-classes/{code}/schema`
  gains a `symbology` array, each rule pre-resolved into
  `{subtype_code, domain_field, domain_value_code, style_def}`.
  `domain_field` is resolved server-side (`resolveSymbologySchema`) --
  a rule only ever stores a raw `domain_value_id`; matching that
  value's domain against whichever of the feature class's fields uses
  it (per `migrations/0012`'s own column comment) is work done once
  here rather than in every frontend consumer.

### Frontend

- **`symbology-icons.ts`** (new) -- five hand-authored SVGs (circle,
  square, triangle, triangle-outline, diamond), each a template with a
  `{{COLOR}}` placeholder substituted at render time -- so the same
  function produces a correctly-tinted `data:` URI for both OpenLayers'
  `Icon` style *and* a plain `<img>` in the legend, rather than relying
  on OL's own canvas-based recolor trick (which only works inside OL's
  own rendering, not a bare `<img>`).
- **`symbology.ts`** (new) -- framework-agnostic `resolveSymbology`:
  most-specific-wins over a feature's own `subtype` + attributes,
  mirroring `migrations/0012`'s documented resolution order exactly.
  No OL import -- the legend needs the same resolved `StyleDef` without
  dragging map rendering along.
- **`map-layers.ts`** -- `createSymbologyStyle(rules)` replaces the
  old geometry-type-only `styleForFeature` as each layer's real style
  function, built once per feature class at layer-creation time (each
  Martin catalog entry is already scoped to exactly one class). Falls
  back to the old plain styling whenever no rule resolves at all (an
  unseeded class, or a schema fetch still in flight) -- a feature never
  renders invisible just because symbology came up empty.
- **`use-all-symbology.ts`** (new) -- fetches every feature class's
  schema via `useQueries` in parallel, reusing the exact same query key
  `useFeatureClassSchema` already uses per class, so a create/edit form
  opened later for the same class hits an already-warm cache. Memoized
  on each query's `dataUpdatedAt` rather than the fresh array
  `useQueries` returns every render -- otherwise the map's layer-creation
  effect would rebuild every layer on every render, not just when
  symbology data actually changes.
- **`layers-panel.tsx`** -- each row now shows a small resolved icon
  or color swatch next to the feature class name, using the exact same
  `resolveSymbology`/`iconDataUri` the map itself uses -- the legend the
  user asked for, without a new component.
- **`feature-edit-form.tsx`** -- the post-save map-refresh cache-bust
  (`useMapRefreshStore.bump()`) now fires on every successful save, not
  only when geometry changed -- see "How this was validated" for why
  the old `if (dirtyGeometry)` gate was a real bug once symbology
  started depending on live attributes.

### Seed data

`seed/symbology_example.sql` -- a mandatory default rule per existing
feature class, plus `DIST_SWITCH`'s `device_state = OPEN` override
(a genuinely different `icon_ref`, hollow vs. solid triangle, not just
a color change) to exercise real resolution order end to end, not just
the trivial always-matches-everything default case.

## How this was validated

Backend confirmed via `curl` against the real Postgres/API stack:
`GET /feature-classes/DIST_SWITCH/schema` returned both rules, the
`OPEN` override correctly resolved to `domain_field: "device_state"`,
`domain_value_code: "OPEN"`; `GET /feature-classes/POLE/schema` and
`.../MV_CONDUCTOR/schema` confirmed the simple default-only case for a
point and a line class respectively.

Frontend verification against the real dev stack (Vite + Go API +
Postgres + Martin, no mocking) surfaced two real bugs, both fixed and
re-confirmed live before calling this done:

1. **No point markers rendered on the map at all**, in any zoom/pan
   state, across every point feature class -- confirmed via raw canvas
   pixel dumps, not just screenshots. Lines and polygons rendered
   correctly. The first suspect -- `createSymbologyStyle`'s Point-case
   branch (`map-layers.ts`) constructing a brand-new
   `new Style({ image: new Icon({...}) })` inline on every OL
   style-function call -- got a module-level `iconStyleCache` fix, but
   that alone didn't resolve it: instrumenting `drawImage` directly in
   a live browser showed the real cause was our SVGs having no
   explicit `width`/`height` attributes (only `viewBox="0 0 24 24"`),
   which Chromium resolves to a default intrinsic size (150x150, not
   24x24) for a detached `Image()` -- a mismatch that broke
   `VectorTileLayer`'s per-tile render-instruction building in a way a
   plain `VectorLayer` or an isolated `ol/style/Icon` test didn't
   reproduce. Fixed by adding explicit `width="24" height="24"` to
   every template in `symbology-icons.ts`. The `iconStyleCache` stayed
   in (still the right call re: not rebuilding `Icon`/`Image` objects
   per render), but it wasn't the actual fix for this bug.
2. **An attribute-only edit (e.g. `device_state`) never updated the
   map icon**, even after the fix above -- the data-level save worked
   (panel showed the new value immediately), but the map kept
   rendering the stale symbology. `feature-edit-form.tsx` only bumped
   `useMapRefreshStore` (the cache-busting signal that makes vector
   tile sources refetch from Martin) `if (dirtyGeometry)` -- a holdover
   from before symbology resolution existed, when an attribute-only
   change had no visual consequence on the map at all. Now that
   `createSymbologyStyle` resolves per-attribute, any successful save
   can change what a feature looks like, so the bump is now
   unconditional.

With both fixed, live re-verification confirmed: multiple point
feature classes visually distinct on the map itself (not just the
legend), `Switch S1` toggled from `CLOSED` to `OPEN` and back with its
map icon actually switching from a solid green triangle to a hollow
red one and back live, and the Layers dropdown still showing each
class's resolved icon next to its name.

`npm run build && npm run lint` and `go build ./... && go vet ./... &&
gofmt -l .` all finish clean.

## Decisions made

- **SVG templates with a color placeholder, not OL's canvas tint.**
  Baking color into the SVG markup itself (rather than OL's `Icon`
  `color` option, a canvas-compositing trick) is what let the exact
  same `iconDataUri` function serve both the map's OL rendering and a
  plain `<img>` in the legend -- one resolution path, two renderers,
  instead of duplicating color logic for each.
- **A hollow triangle for "open," not a color-only difference.**
  Matches `docs/metadata-core.md`'s own worked example (switch
  open/closed is a *shape* difference, not a modifier) and gives the
  resolution engine a real, non-trivial case to prove correct instead
  of only ever exercising the mandatory-default path.
- **`useAllSymbology` reuses `useFeatureClassSchema`'s cache key.**
  A schema fetch triggered by the map's proactive prefetch and one
  triggered later by opening a create/edit form for the same class are
  the same request -- sharing the cache key means the second one is
  free.
- **No zoom-gating, no diagram context, no real sprite pipeline** --
  all three deliberately deferred; none had a concrete need in this
  pass, and each is a real, separate piece of design work (respectively:
  wiring two existing-but-unused columns, Phase 5's diagram system, and
  a genuine asset-serving concern) not worth answering speculatively
  alongside the resolution engine itself.

## What's still open

- **Zoom-gated rules** -- `min_zoom`/`max_zoom` exist on
  `symbology_rules` and are loaded, but nothing in `resolveSymbology`
  consults them yet.
- **`render_context = 'diagram'`** -- rules exist in the schema and are
  loadable, but nothing renders them; blocked on Phase 5's diagram
  system existing at all.
- **A real icon/sprite pipeline** -- `symbology-icons.ts` was a small,
  hardcoded registry of hand-authored SVGs, not a real asset-serving
  system. *Partly addressed since:* the icon markup moved into a
  `metadata.symbology_icons` DB table served over the API, so icons are
  now editable as data and full-color artwork is supported alongside the
  tintable shapes (`docs/phase-3-symbology-icon-table.md`). The frontend
  registry survives only as a fallback. Still open beyond that: a real
  utility symbol library (IEC/ANSI pictograms), an in-app icon editor,
  and any atlasing/CDN concern.
- **Scale/rotation modifiers** -- `StyleDef.scale` is wired through
  (e.g. for a `kva_rating`-driven size tier), but no seeded rule uses
  it; rotation isn't modeled in `StyleDef` at all yet.
- Everything else tracked in `docs/open-items.md` is unchanged by this
  slice.

## Try it yourself

Open `http://localhost:5173`:

- Pan/zoom the map -- poles, transformers, switches, service points,
  conductors, and the substation yard all now render as visually
  distinct shapes/colors instead of identical dots.
- Select "Switch S1" (or "Switch S0"), Edit, flip `device_state`
  between `CLOSED` and `OPEN`, Save -- watch its map icon switch
  between a solid green triangle and a hollow red one live.
- Open the "Layers" dropdown (top-right toolbar) -- each feature class
  row now shows its resolved icon/color next to its name.

`npm run build && npm run lint` and `go build ./... && go vet ./... &&
gofmt -l .` should all finish clean.

## Zoom-gating, added later

Tracked as an open item since this doc first shipped:
`symbology_rules.min_zoom`/`max_zoom` have existed and been loaded into
`metadata.SymbologyRule` since Phase 1, but the schema endpoint never
sent either to the frontend, so `resolveSymbology` had nothing to gate
on. `symbologySchema` (`api/cmd/server/main.go`) now includes both
(omitted when unset, same `omitempty` convention every other optional
field on that response already uses); `resolveSymbology`
(`symbology.ts`) takes an optional `zoom` parameter and skips any rule
whose bounds don't cover it, before the specificity comparison that
picks the best match -- so a broader, unrestricted rule still wins if
the more specific one is zoom-gated out, the same fallback behavior
`resolveSymbology` already had for "no rule matches at all."

Only `createSymbologyStyle` (`map-layers.ts`), the map's own per-layer
style function, has a real "current zoom" to pass -- `view` is now a
required second argument, and the OL `StyleFunction`'s own `resolution`
argument (previously ignored) is converted via
`view.getZoomForResolution(resolution)`, which respects whatever
projection/resolutions config the view actually uses rather than a
hardcoded EPSG:3857 formula. `layers-panel.tsx`'s legend and
`diagram-canvas.tsx` both call `resolveSymbology` with `zoom` omitted --
a legend has no single zoom to represent, and a diagram's own camera
zoom is a different, author-chosen coordinate scale, not the same unit
as a real map zoom level.

Verified live: setting a real rule's (`STREET_LIGHT`'s default diamond
icon) `min_zoom` to 18 made the feature render as the plain fallback
circle at the map's normal zoom, switching to the real yellow diamond
icon exactly at zoom 18 and back below it, confirmed by pixel color at
each transition in both directions; reverting the test `min_zoom`
restored the icon. `npx tsc --noEmit`, `npm run lint`, and `go build
./... && go vet ./... && gofmt -l .` all finish clean.
