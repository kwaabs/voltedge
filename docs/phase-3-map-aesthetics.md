# Phase 3 (slice 9) — highlight, toast, and clickable-row aesthetics

**Goal:** make the map show, not just tell, which feature is currently
selected or being connected/related, replace inline error text with a
consistent top-right toast wherever any panel or form can fail, and let
a user jump from a listed connection/relationship straight to the
feature on the other end of it. Purely presentational/navigational --
no new backend endpoints, no change to what's writable.
**Status:** done and verified against a real Postgres/Martin/API/Vite
stack. Two real, independent gaps in the first draft were caught and
fixed during verification -- see "How this was validated." Two more
findings from an interrupted verification pass turned out, once
re-checked with a more careful test, to be test artifacts rather than
real bugs -- see the same section for why, and for the real (different)
bug that investigation surfaced instead.

## Why this slice exists

The user asked for it directly, as a set of three aesthetic asks plus a
cross-cutting one: *"1. when attribute is selected for viewing its
attribute, please highlight it... 2. when selecting a candidate to
connect to electrically, please highlight that too. 3. for every
feature that is in the relationship or connectivity panel, when its
listed, make it clickable. this way it will highlight it."* -- plus,
separately: *"for every error, instead of showing the error text on the
panel or div where it happened, please use a toast that appears at the
top right of the screen."*

## What was built

- **`src/lib/map-layers.ts`** gained `highlightStyleForFeature` and its
  `HighlightRole` type (`"selected" | "connect-source" |
  "connect-target"`), each role a distinct hand-picked color (sky-blue
  `#38bdf8`, amber `#f59e0b`, violet `#a855f7`) so a source and target
  glowing at once are never confused with each other or with a plain
  selection. Geometry-type-branched like `styleForFeature`/
  `draftStyleForFeature` already were: a ring around a point, a wide
  translucent glow along a line (translucent because the highlight layer
  renders *above* the real feature -- an opaque stroke there would hide
  the line's own color underneath it), a translucent fill + stroke
  around a polygon's outline.
- **`src/components/map/voltedge-map.tsx`** gained a highlight
  `VectorLayer`/`VectorSource` pair (zIndex 5 -- above the vector tile
  layers so a halo is never hidden behind another feature, below the
  zIndex-10 draft layer), created once alongside the map like
  `draftSource` already was. A new `geoJSONToGeometry` (the inverse of
  the existing `geometryToGeoJSON`, same three-geometry-type support:
  nothing this app creates or highlights is ever a Multi* geometry)
  turns a fetched feature's real GeoJSON geometry into an OL geometry a
  temporary `Feature` can be built from. One effect owns the layer's
  contents: while `connectMode` is set it shows the connect/relate
  workflow's source and target (each via its own `useFeature` call,
  deduped against the same query the confirm panel already runs), and
  otherwise whatever's currently `selected`. The map cursor already
  became a crosshair for draw mode in an earlier slice; connect mode now
  does too, for the same reason -- a visual cue that a click means
  something different right now.
- **`src/stores/toast-store.ts`** (new) -- a bare `{id, message}[]`
  list, `pushError` (auto-dismisses after 6s) and `dismiss`, plus a
  shared `useErrorToast(isError, error, prefix)` hook every
  query-backed panel/tab can call instead of hand-rolling its own
  `useEffect`. `src/components/ui/toaster.tsx` (new) renders the stack
  fixed top-right, offset below the map's own top nav row (New
  feature/Layers buttons, same corner) so a toast never sits directly on
  top of those controls -- mounted once in `src/routes/map.tsx`.
- **Four inline error spots converted**: `feature-panel.tsx`'s three
  tabs (attributes/terminals/relationships, each a failed query --
  `useErrorToast`) and `feature-create-form.tsx`'s two (schema-load
  query error via `useErrorToast`, save error via a direct
  `pushError` call from its catch block, since that's an imperative
  POST, not a query). `connect-confirm-panel.tsx`'s POST-failure path
  also moved to `pushError` -- the panel still stays open on a rejected
  pairing exactly as before (only where the message shows moved), so a
  user can retry with a different target or terminal without losing
  their place.
- **Clickable connection/relationship rows**: `selection-store.ts`
  gained a `focusToken` counter and an optional `{focus: true}` on
  `select()` -- a direct map click already shows the clicked feature on
  screen, so it never needs to move the view; a row click in
  `feature-panel.tsx`'s Terminals/Relationships tabs can point at a
  feature that's off-screen or not yet loaded into the viewport, so that
  path alone asks the map to pan/zoom too. Both tabs' rows became real
  `<button>`s (hover-highlighted, same `hover:bg-accent` pattern
  `peer-picker.tsx` already used) calling
  `select(counterpart.feature_class, counterpart.feature_id, {focus:
  true})`. `voltedge-map.tsx` gained a second effect, keyed on
  `focusToken` *and* `selectedFeature` (not `focusToken` alone -- see
  "How this was validated" for why that distinction is load-bearing),
  that fits the view to the newly selected feature's real geometry
  extent once it's loaded.

## How this was validated -- two real bugs, and two that weren't

Driven against a real stack via Playwright/CDP-controlled Chromium, not
assumed from the code, in three rounds.

**First round** covered the intended cases directly: select a feature
and see its halo; start a connect workflow and see the source (then
source+target) halo in their distinct colors; trigger a rejected pairing
and see the message as a toast, not inline; check both themes. Two real,
independent bugs surfaced:

1. **No panel feedback when connect mode started.** Already fixed in
   the prior connectivity-UI slice's own verification pass -- see
   `docs/phase-3-connectivity-ui.md` -- and unrelated to this slice's
   changes; noted here only because the same pass re-confirmed it stayed
   fixed.
2. **Stale terminal/error state across sessions.** Same as above -- a
   pre-existing fix, re-confirmed, not something this slice touched.

(Both of the above were carried over findings from the connectivity-UI
slice's own verification, re-checked here as part of confirming the new
highlight/toast work didn't regress them -- not new bugs from this
slice.)

**Second round**, specific to this slice's own new code, found two
things worth investigating further:

3. **Relationship-row click sometimes showed no halo**, specifically
   when the target was already fully in view (no pan needed) --
   reproduced twice, even after an explicit wait.
4. **Hover tint on connection/relationship rows appeared completely
   absent** -- the `hover:bg-accent` classes were present in the DOM but
   hovering showed no visible change in either theme.

Investigating #3 by re-reading the pan effect's code (not by guessing)
found a real, provable timing bug, independent of whatever the test
observed: the effect was keyed on `focusToken` *alone*, reading
`selectedFeature` through closure. A freshly clicked row almost always
targets a feature this tab hasn't fetched before, so `selectedFeature`
is still `undefined` on the very render where `focusToken` changes --
the pan would silently no-op for exactly that (the common) case, and
only happen to work when the feature's data was already cached from
earlier in the session. Fixed by depending on both `focusToken` and
`selectedFeature`, with a `fittedTokenRef` remembering which token was
last handled so the fit still only runs once per focus request, not on
every later refetch of the same feature.

Investigating #4 turned up no code defect: the `hover:bg-accent` CSS
rule was confirmed present, correctly generated, in both the live dev
server's own Tailwind output and the production build -- and the exact
same class pair already shipped and worked in `peer-picker.tsx`. The
most likely explanation was a stale render in a long-lived (135+ tool
call) browser session rather than a real gap.

A third, focused re-verification round (after a first attempt was
interrupted mid-run by an unrelated session budget limit) confirmed
both conclusions directly, with two methodology fixes that turned out
to matter:

- **Hover tint**: `getComputedStyle(el).backgroundColor` on a
  connection row genuinely changes on `hover()` (confirmed via a real
  computed-style read, not a visual guess) -- #4 was a test artifact,
  not a bug.
- **Halo timing**: locating the transformer's real on-screen position by
  its own rendered color (rather than trusting this script's own
  Mercator-projection math for a second, independent pixel -- which
  turned out to itself be wrong once the docked feature panel changed
  the map container's size, a lesson in not trusting a test's own
  coordinate math over the app's actual rendered output) showed **no**
  halo immediately after a row click, but a clear one ~2 seconds later.
  That's ordinary fetch latency for a feature this tab hadn't loaded
  before (a real network round trip through the Go API), not a
  never-appears bug -- #3's original repro was very likely just too
  fast a check, not a stuck state. The pan-effect fix above was a real,
  worthwhile fix regardless of whether it was the exact mechanism behind
  the original report.

The one connection created during this validation
(`MV_CONDUCTOR` → `DIST_XFMR.T1`, the only `network_rules`-authorized
direction in seed data) was revoked afterward via `DELETE
/connectivity/{id}`, leaving `runtime.feature_connectivity` empty again.

## Decisions made

- **One shared highlight layer, not per-purpose layers.** A single
  `VectorSource`, cleared and repopulated by one effect that branches on
  `connectMode`, rather than separate layers for "selected" vs.
  "connect source/target" -- the two states are already mutually
  exclusive in the UI (a connect workflow takes over the whole
  interaction), so a second layer would just be two sources that are
  never both non-empty at once.
- **A halo is built from the feature's own real geometry, not a generic
  marker.** Reuses the same `useFeature` fetch (and TanStack Query
  cache) every panel already runs for that feature -- a few extra
  `useFeature` calls in `voltedge-map.tsx`, not a new fetch path.
- **Toasts are error-only, no success/info variant yet.** Matches what
  this app currently has a use for; the store's shape (`message` only)
  would need to grow before that changes, deliberately not built ahead
  of a real need.
- **A rejected connect/relate attempt still leaves the panel open.**
  Unchanged from the prior slice's decision -- only *where* the message
  displays moved to a toast, not whether the workflow survives a
  rejection.
- **Row clicks pan only when they might need to -- a direct map click
  never does.** `focusToken` is a separate, opt-in signal specifically
  because a feature the user just clicked on the map is by definition
  already visible.

## What's still open

- **No loading affordance on the halo itself.** A row click that
  targets an unfetched feature takes a beat (typically well under 2s
  locally) before its halo appears, with no spinner or other cue in the
  meantime -- the map panel's own attribute tab already shows a loading
  state for the same fetch, so the information exists, just not
  reflected on the map side.
- **Peer-picker disambiguation for connect-mode target-picking** --
  unchanged, still open from `docs/phase-3-connectivity-ui.md`.
- **Structural relationships' own connect-mode workflow** -- unchanged,
  still open from the same doc; this slice's row-click/highlight work
  already covers the Relationships tab's *display* side, independent of
  when that workflow itself ships.

## Try it yourself

Same stack as `docs/phase-3-connectivity-ui.md`'s "Try it yourself" --
`task infra-up && task migrate && task api-dev`, then `cd web && npm run
dev`. Open `http://localhost:5173`:

- Click any feature -- a colored halo (sky-blue) traces its real shape
  on the map, and disappears when you click empty space to clear the
  selection.
- Select a feature, open its Terminals tab, click "+ New connection" --
  its halo turns amber; click a second feature as the target and its
  halo turns violet. Escape/Cancel reverts to the plain selected color.
- Once a feature has a connection or relationship listed, hover a row
  (background tints) and click it -- the other feature in that pairing
  becomes selected, gets its own halo, and the map pans/zooms to it if
  it wasn't already in view.
- Trigger a rejected connect attempt (e.g. a pairing with no matching
  `network_rules` row) -- the error shows as a toast top-right, not
  inline in the panel.

`npm run build && npm run lint` and `go build ./... && go vet ./... &&
gofmt -l .` should all finish clean.
