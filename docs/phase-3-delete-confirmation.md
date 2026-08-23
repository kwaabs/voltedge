# Phase 3 (slice 11) — delete a feature with a real confirmation dialog

**Goal:** let a user soft-delete a feature from the map, gated behind a
confirmation dialog that can't be dismissed by an accidental click
outside it — the second of four planned CRUD slices (attribute edit
done, this one, then a geometry-editing toolbar, then snapping).
**Status:** done and verified against a real Postgres/Martin/API/Vite
stack. Along the way, testing the delete flow directly against the
running stack surfaced a real, pre-existing gap this slice needed to
address to actually work end to end — see "A gap this slice had to
close."

## Why this slice exists, and what it needed that didn't exist yet

The user's exact ask: *"delete feature must always come with a
confirmation dialog. (prevent default for this dialog. when user clicks
outside dialog content, dialog should not close)"* — framed in Radix's
own vocabulary (`preventDefault` on an outside-click event), which
turned out to name the mechanism precisely: Radix ships two different
dialog primitives with different default dismiss behavior, and this app
only had one of them installed.

- **`@radix-ui/react-dialog`** was already a dependency (backing
  `ui/sheet.tsx`) — its `Content` closes on an outside pointer-down by
  default. Using it here would have meant fighting that default with
  `onPointerDownOutside`/`onInteractOutside` handlers on every usage.
- **`@radix-ui/react-alert-dialog`** (new dependency this slice) is a
  distinct primitive built for exactly this case: its `Content` does
  **not** close on an outside click by Radix's own default — no
  override needed, just the right primitive for the job. `ui/alert-dialog.tsx`
  wraps it in the same shape every other `ui/` component in this repo
  already follows (`Root`/`Content`/`Header`/`Footer`/`Title`/
  `Description`/`Action`/`Cancel`, all thin `cn()`-merged wrappers, same
  as `dropdown-menu.tsx`).

## A gap this slice had to close

Before writing any UI, the actual `DELETE /features/{code}/{id}`
behavior needed a direct check, since nothing in this app had ever
called it from the browser before (only curl/tests, from Phase 2's own
verification). Confirmed live: soft-delete a real seeded feature via
`curl -X DELETE`, then fetch Martin's raw tile for that area — **the
feature's bytes were still in the tile.** Martin auto-publishes each
feature class's raw table directly; it has no concept of `deleted_at`
at all, so a soft-deleted feature stays fully visible on the map
indefinitely (or at least until whatever eventually re-renders that
tile region from a source that does filter it, which nothing currently
does). Shipping the confirmation dialog without addressing this would
have meant "Delete" appearing to work (row soft-deleted, panel closes)
while the feature kept sitting right there on the map — a correctness
problem serious enough to fix now, not defer, even though it's really a
Martin/tiling concern rather than a dialog concern.

The **real** fix would be Martin serving from a `deleted_at IS NULL`
filtered view instead of each feature class's raw table — a compiler
change (generate the view alongside the table) plus a Martin config
change, well beyond what a confirmation-dialog slice should take on.
Instead, a **pragmatic, purely client-side symptom fix**:

- **`src/stores/deleted-features-store.ts`** (new) — a `Set<string>` of
  feature ids marked deleted this session, not persisted.
- **`src/lib/map-layers.ts`**'s `styleForFeature` checks this set first
  and returns an empty `hiddenStyle` for any matching id, before its
  normal geometry-type styling runs.
- **`src/components/map/voltedge-map.tsx`** calls `layer.changed()` on
  every vector tile layer when the set changes — deliberately not a
  refetch (`setUrl()`'s cache-busting trick from the create-feature
  slice would just fetch the exact same bytes again, since Martin's
  data hasn't changed): the already-loaded tile data sitting in OL's
  own cache is enough, since the fix is style-time hiding, not data
  filtering.

This makes a deleted feature disappear from the map immediately,
correctly, with real seed data confirmed via before/after screenshots
and a stale-tile check — while leaving the actual architectural gap
(Martin has zero soft-delete awareness) explicitly recorded as open
rather than silently worked around and forgotten.

## What was built

- **`src/components/ui/alert-dialog.tsx`** (new) — the shared primitive
  described above.
- **`src/components/map/delete-feature-dialog.tsx`** (new) — title
  names the feature class ("Delete Pole?"), Cancel and a
  destructive-styled Delete button. `onOpenChange` additionally ignores
  any close attempt while a delete request is in flight, so Escape
  can't abandon the dialog mid-request either (Cancel/Delete are
  separately `disabled` for the same reason). On success: marks the id
  in `deleted-features-store.ts`, clears the selection
  (`selection-store.ts`), closes. Errors go through the existing
  toast system, dialog stays open so the user can retry.
- **`src/components/map/feature-panel.tsx`** gained a trash-can icon
  button in the header, next to the existing pencil Edit and X Close
  buttons, disabled once the feature is already soft-deleted (same
  `feature?.deleted_at` check the Edit button already used).

## How this was validated

Driven against a real stack via Playwright, using a real seeded `POLE`.
Every check passed on the first pass:

- The dialog opens as a real centered modal with a full-screen dark
  overlay (not this app's older lightweight floating-panel pattern).
- Clicking the overlay, well outside the dialog's content box, left it
  open with no request sent — confirming Radix AlertDialog's own
  default, not just that the code compiles.
- Cancel and Escape both closed it with no request sent, panel
  unchanged.
- A real Delete click sent `DELETE /features/POLE/{id}`, got a real
  success response, closed the dialog and the panel, **and the
  feature's marker was confirmed gone from the map** via before/after
  screenshots and by re-clicking its old screen position (only the
  remaining overlapping feature showed up in the peer picker
  afterward) -- not just inferred from the API response.
- A direct `GET` afterward confirmed `deleted_at` was set (a real
  soft-delete, row and geometry intact) and `status` unchanged --
  independent fields, as designed back in Phase 2.
- Both themes checked -- overlay darkness, text contrast, and the
  destructive-red Delete button all render correctly in dark mode too.
- The test feature was restored via `POST /restore` and confirmed via
  a follow-up `GET` before finishing, leaving seed data unchanged.

## Decisions made

- **`@radix-ui/react-alert-dialog`, not a hand-rolled dialog.** Every
  other floating overlay in this app so far (the peer picker, the
  connect-confirm panel) was hand-authored, consistent with this
  codebase's general preference for owning simple UI outright over
  pulling in a dependency. A destructive confirmation is different: it
  needs a real focus trap, correct ARIA semantics, and Radix's own
  outside-click-ignore default is exactly the behavior asked for --
  hand-rolling that correctly (and accessibly) would be strictly more
  code and more risk than using the primitive built for it.
- **A pragmatic client-side hide, not the real Martin/view fix, for the
  tile-visibility gap.** Recorded explicitly as a stopgap, not
  presented as solved -- the real fix touches the compiler and Martin's
  config, out of scope for a slice about a confirmation dialog.
- **Soft-delete only, no hard-delete/purge trigger from the UI.** The
  API already supports `DELETE /{code}/{id}/purge` (Phase 2), but nothing
  asked for exposing it here, and doing so from the same dialog would
  blur "reversible" and "not reversible" into one button.

## Resolved later

- **The real Martin/tile-filtering fix shipped** (see
  `docs/phase-19-assistant.md`'s tile-function section and migration
  `0038_add_tile_functions.sql`). Rather than the `deleted_at`-filtered
  *view* originally sketched here, Martin now auto-publishes per-class tile
  *functions* -- `gis.<code>(z,x,y)` returning MVT `bytea` filtered on
  `deleted_at IS NULL`, generated by `gis.sync_tile_function` and refreshed
  by the compiler on every compile, with `auto_publish.tables:false` in
  `infra/martin/config.yaml` so the raw tables are no longer served. The
  function is named exactly like the table, so its Martin source id (the
  bare, lowercased class code) is unchanged and the frontend needed no edits.
  The client-side store below is now purely an instant-feedback optimization,
  no longer load-bearing for correctness. A future UI *restore* would just
  need to drop the id from that store to un-hide the feature in-session; the
  server's filtered tiles already show it again after any refetch.

## What's still open
- **Geometry-editing toolbar** (slice C) and **snapping** (slice D) --
  unchanged from `docs/phase-3-edit-attributes.md`.
- **Hard-delete/purge from the UI** -- deliberately not exposed this
  slice (see "Decisions made").

## Try it yourself

Same stack as `docs/phase-3-edit-attributes.md`'s "Try it yourself" --
`task infra-up && task migrate && task api-dev`, then `cd web && npm run
dev`. Open `http://localhost:5173`:

- Select a feature, click the trash-can icon in the panel header.
- Click the dark overlay outside the dialog -- it stays open. Click
  Cancel (or press Escape) -- it closes, nothing changes.
- Reopen and click Delete -- the feature disappears from the map
  immediately and the panel closes.

`npm run build && npm run lint` and `go build ./... && go vet ./... &&
gofmt -l .` should all finish clean.
