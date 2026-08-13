# Phase 3 (slice 10) — edit an existing feature's attributes

**Goal:** let a user update an already-created feature's field values
from the map, through the real `PATCH /features/{code}/{id}` endpoint
Phase 2 already built — the first of four planned slices for a broader
"more CRUD" phase the user asked for (edit attributes, edit geometry,
delete with confirmation, snapping), sequenced attributes-first since it
needed zero backend change and could reuse the create form's field
code almost entirely.
**Status:** done and verified against a real Postgres/Martin/API/Vite
stack — a real field edited through the UI, a real `PATCH` sent with
only the changed key, the panel reflecting the new value with no
reload, Cancel/Escape both discarding cleanly, and the critical
data-loss guard (a map click mid-edit must not silently drop unsaved
changes) confirmed working. No bugs found in this pass — see "How this
was validated."

## Why this slice exists, and how it's scoped

The user asked for four related things in one message: editing a
feature's attributes and/or geometry (with a geometry toolbar
purpose-built per geometry type), a delete confirmation dialog that
can't be dismissed by clicking outside it, whether edits should commit
immediately or pile up before a final save, and where a snapping toggle
should live. Discussed and agreed before building anything:

- **Commit-on-save, no staging.** Matches how every other write in this
  app already works (the create-feature draft is pure client state
  until `POST`) — no new server-side staging table, no undo history.
  The edit form tracks locally which fields actually changed and sends
  only those.
- **Snapping toggle lives in the (future) geometry-editing toolbar**,
  not a permanent bottom-bar control — it's only ever relevant while
  actively placing or moving a vertex.
- **Four slices, in order**: attribute edit (this one) → delete
  confirmation dialog → geometry-editing toolbar → snapping. Each of
  the last three needs something that doesn't exist yet (a real
  Dialog/AlertDialog primitive; `Modify`/`Translate`/`Snap` OpenLayers
  interactions and an extended `PATCH` that accepts geometry; the
  `snapping_rules` metadata plumbed into Go) — confirmed by research
  before committing to the plan, not assumed.

## What was built

- **`src/components/map/feature-field-input.tsx`** (new) and
  **`src/components/map/field-utils.ts`** (new) — `FieldInput`
  (previously private to `feature-create-form.tsx`) and
  `formatFieldLabel`/`formatValue`/`inputClass` (previously duplicated
  between `feature-create-form.tsx` and `feature-panel.tsx`) extracted
  into shared modules now that a second, real caller
  (`feature-edit-form.tsx`) needs the exact same field-rendering
  contract. Split into two files, not one, so Vite's fast-refresh stays
  happy — a file mixing component and non-component exports breaks it.
- **`src/stores/edit-store.ts`** (new) — deliberately just a session
  marker (`editing: {featureClassCode, featureId} | null`, `start`,
  `stop`), not the edit's own field values (those live as local state
  in the form itself, the only thing that renders them). Exists so
  *other* components can react to "a feature is being edited" without
  needing its contents: `feature-panel.tsx` locks the
  Terminals/Relationships tabs while editing, and `voltedge-map.tsx`
  blocks normal click-to-select, the same way `draw-store.ts`/
  `connect-store.ts` already block it for their own in-progress
  workflows. Attribute-only for now — geometry editing is a later slice
  that extends this same store rather than replacing it, since both are
  really "this feature is under modification."
- **`src/components/map/feature-edit-form.tsx`** (new) — fetches the
  feature and its schema itself (`useFeature`/`useFeatureClassSchema`,
  deduped by TanStack Query against the same queries the rest of the
  panel already runs), seeds local `attributes` state from the
  feature's real current values, renders one `FieldInput` per
  `visible_on_update` field. A field with `editable_after_publish:
  false` on a currently-`published` feature renders as plain read-only
  text instead of an input — mirrors
  `api/internal/crud/validate.go`'s own rule exactly (`field %q is
  locked once the feature is published`) rather than letting a user
  fill in a value the server is guaranteed to reject. Save computes the
  diff against the feature's original attributes and `PATCH`es only the
  changed keys (disabled until at least one differs); Cancel discards
  by calling `edit-store.ts`'s `stop()`. Errors go through
  `useToastStore`/`useErrorToast` (the toast system from the prior
  aesthetics slice), not inline panel text.
- **`src/components/map/feature-panel.tsx`** gained a pencil "Edit"
  button in the header (next to the existing Close X), disabled if the
  feature is soft-deleted. Clicking it calls `edit-store.ts`'s
  `start()`. While editing: the header shows "Editing…" instead of the
  feature id and hides Edit/Close (the form's own Cancel/Save serve
  that role instead); the Attributes tab renders `FeatureEditForm` in
  place of the read-only `<dl>`; the Terminals/Relationships tab
  buttons are disabled — both tabs unmount their content on switch
  (confirmed by reading the existing conditional-render code before
  relying on it), which would otherwise silently drop an in-progress
  edit. The existing tab-reset effect (fires whenever `selected`
  changes to a different feature) now also calls `edit-store.ts`'s
  `stop()`, so switching away can't leave a stale `editing` pointer at
  a feature that's no longer selected.
- **`src/components/map/voltedge-map.tsx`** gained two small additions
  alongside the existing draw-mode/connect-mode guards: the click
  handler now also returns early while `useEditStore.getState().editing`
  is set (a click can neither change the selection nor be misread as
  part of the edit, since attribute editing has no map interaction of
  its own this slice), and a new Escape-cancels-edit effect matching
  the pattern already used for draw and connect.

## How this was validated

Driven against a real stack via Playwright, clicking real map pixels to
select a real `POLE` feature, watching real network traffic to the Go
API. Every item passed on the first verification pass — no bugs found,
unusual for a slice this size, credited to the plan-first discussion
(the persist-on-save decision, the exact server-side lock rule read
from `validate.go` before writing the client-side mirror, and the
data-loss guard following an already-proven precedent from draw/connect
mode rather than inventing new behavior) rather than luck:

- A real field edit (`condition_rating`, a coded-domain select) sent a
  `PATCH` with only `{"condition_rating": "FAIR"}` -- not the whole
  attributes object -- and the panel showed the new value with no
  reload.
- Cancel and Escape both discarded the change and sent no request.
- **The critical check**: clicking a different feature on the map
  while mid-edit was confirmed to do nothing -- the original edit
  session stayed intact, proving the click guard actually works, not
  just that it compiles.
- Terminals/Relationships tabs were confirmed `disabled` in the DOM
  while editing, not just visually dimmed.
- A deliberately invalid domain value (injected directly into the
  live `<select>`, bypassing the client's own domain-value dropdown)
  produced a real 400 from the server
  (`field "condition_rating" value "NOT_A_REAL_VALUE" is not a valid
  CONDITION_RATING value`), shown as a toast, panel staying open so the
  edit isn't lost -- confirming the toast-on-error wiring survives a
  real server rejection, not just a client-side validation failure.
- One item -- the locked-field-on-a-published-feature display -- was
  **not exercisable**: every field on every seeded feature class
  currently has `editable_after_publish: true` in the actual metadata
  (checked directly against `GET /feature-classes/{code}/schema` for
  all four classes, and the underlying migration/seed data), so no
  real feature+field combination exists yet to trigger the locked
  branch. The code path mirrors the server's own rule exactly (see
  above) and isn't otherwise reachable without seeding metadata
  specifically to test it -- left as a known verification gap rather
  than pretending it was checked.
- Both themes checked; the form and its error toast render correctly
  in dark mode.
- The one feature modified during testing (`POLE`
  `670f2818-f1bd-455c-b026-7c35afebf0b7`) was confirmed reverted to its
  exact original seed values afterward via a direct `GET`.

## Decisions made

- **Diff-based `PATCH`, not the whole attributes object.** Directly
  answers the user's "user may decide to modify only feature and not
  the attributes... or even both" framing -- only what actually changed
  gets sent, whether that's one field or several.
- **A locked field is shown read-only, not hidden.** The value is still
  relevant context even though it can't be changed right now; hiding it
  would make the form look incomplete rather than explain why a field
  is missing.
- **Editing blocks tab-switching and map clicks rather than trying to
  preserve state across them.** Simpler and safer than making the edit
  form's local state survive a tab unmount or building a
  confirm-before-discard prompt -- the latter is exactly what slice B's
  new Dialog primitive is for, not something to improvise here first.
- **`edit-store.ts` is a bare session marker, not a bigger draft
  object.** Keeps this slice's surface minimal while still giving
  slice C (geometry editing) a natural place to extend from later,
  since both slices are really the same underlying concept --
  attribute values live in the form's own local state, only the "is
  this feature under modification" flag needs to be global.

## What's still open

- **Editing a feature's geometry** (slice C) -- needs `PATCH` extended
  to accept geometry (currently attributes-only,
  `api/internal/crud/types.go`) and new `Modify`/`Translate` OpenLayers
  interactions, geometry-type-specific per the user's ask.
- **Delete confirmation dialog** (slice B) -- no Dialog/AlertDialog
  primitive exists anywhere in this app yet; needed next.
- **Snapping** (slice D) -- `metadata.snapping_rules` is schema-only
  today (no Go loader, nothing in the frontend reads it); depends on
  slice C's toolbar existing first.
- **The untested locked-field path** noted above -- worth a real check
  once any feature class actually has an `editable_after_publish:
  false` field seeded, rather than manufacturing a synthetic one just
  for this slice.

## Try it yourself

Same stack as `docs/phase-3-map-aesthetics.md`'s "Try it yourself" --
`task infra-up && task migrate && task api-dev`, then `cd web && npm run
dev`. Open `http://localhost:5173`:

- Click a feature, open its Attributes tab, click the pencil Edit
  button in the header.
- Change a field's value -- Save enables once something actually
  differs from the original. Click Save to commit through a real
  `PATCH`, or Cancel/Escape to discard.
- While editing, try clicking a different feature on the map, or the
  Terminals/Relationships tabs -- both are blocked until the edit ends.

`npm run build && npm run lint` and `go build ./... && go vet ./... &&
gofmt -l .` should all finish clean.
