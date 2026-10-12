# Phase 3 (closing item) — admin UI for symbology icons and rules

**Goal:** let a user author symbology *without hand-writing SQL* -- both
the icon artwork (`metadata.symbology_icons`) and the rules that decide
which icon/color/width satisfies which condition
(`metadata.symbology_rules`) -- with a live visual preview, so the
"looks like a real GIS" styling that Phase 3's resolution slice made
*possible* (`docs/phase-3-symbology.md`) is now also *editable in the
app*. SQL stays a valid path: the DB is still directly editable, this
just stops it from being the *only* path.

**Status:** built, static review only. This was authored shell-less --
the `bash.exe` in this environment `EPERM`s on every invocation, so no
`tsc`, ESLint, `go build`, `gofmt`, or Playwright run was possible. It
was reviewed statically and against live IDE diagnostics (which fire on
each edit and did surface/clear real Go and TS errors during the build),
but **a `tsc`/lint/`go build`/`gofmt` pass and a live DB + Playwright
click-through are still owed** before this should be trusted end-to-end.
See "What's still open."

## Why this slice exists

`docs/phase-3-symbology.md` closed the *rendering* half: rules in
`metadata.symbology_rules` get resolved client-side (most-specific-wins)
into a real per-class, per-attribute style, and icon markup moved into
`metadata.symbology_icons` in the DB (migration 0023). But authoring
either still meant writing SQL by hand -- `docs/open-items.md`'s "Real
sprite pipeline" row literally listed "an in-app admin editor to
author/edit icon rows (added via SQL today)" as outstanding. The user
asked for exactly that, and for the rule side too: *"some UI should be
built to do these things and also preview the icon so that users don't
have to write SQL (sql option still exists though)."* Scope was
confirmed as the full thing -- icon library CRUD **and** a rule editor.

Three things made this buildable without new infrastructure:

- **The rule editor needs no new lookup endpoint.** Its targeting
  pickers (which subtype? which domain field + value? which render
  context?) are exactly the data the feature-class detail page already
  loads via `GET /feature-classes/{code}/schema` (subtypes, and each
  coded field's `domain_values`). The editor reuses that schema.
- **The visual preview is the map's own code path.** Icons render
  through the same `iconDataUri(iconRef, color, icons)` helper
  (`web/src/lib/symbology-icons.ts`) the map uses, so a thumbnail in the
  admin UI is guaranteed to match what the map draws -- no second
  renderer to drift.
- **The new admin endpoints are the inverse of `resolveSymbologySchema`.**
  That server function already flattens `subtype_id` → `subtype_code`
  and `domain_value_id` → `domain_field` + `domain_value_code` for the
  public schema. The admin rule endpoints do the same id↔code mapping
  in *both* directions: code-shaped on the way in (create/update, which
  re-resolve codes back to ids) and code-shaped **with** the rule `id`
  on the way out (list, so the UI can edit/delete a specific row).

## What was built

### Backend (Go, `api/internal/admin/`)

Net-new, all under the existing unauthenticated `/admin` router (no auth
anywhere is deliberate, per README):

- **`GET/POST /admin/symbology-icons`**, **`PATCH/DELETE
  /admin/symbology-icons/{icon_ref}`** -- full CRUD on
  `metadata.symbology_icons`. Create checks the `icon_ref` PK isn't
  taken; delete is **referentially guarded** -- it refuses if any rule's
  `style_def->>'icon_ref'` still names the icon, so you can't orphan a
  rule's artwork.
- **`GET/POST /admin/feature-classes/{code}/symbology-rules`**,
  **`PATCH/DELETE /admin/symbology-rules/{id}`** -- full CRUD on
  `metadata.symbology_rules` for one feature class. Delete has **no**
  guard (matching `deleteDomainValue`): the "exactly one mandatory
  default rule" invariant is not enforced at delete, so deleting the last
  rule is allowed. (Earlier text said the schema compiler catches this at
  publish; it does not -- the compiler has no symbology check. A class left
  with no default falls back to plain geometry styling in the web matcher.)

New files/edits:

- `symbology.go` (new) -- the CRUD logic. Notable helpers:
  `resolveRuleTargeting` (turns `subtype_code`/`domain_field` +
  `domain_value_code` back into `subtype_id`/`domain_value_id`,
  erroring on anything unresolvable), `buildStyleDef` (marshals only the
  non-nil style fields into the `style_def` jsonb), `ruleLookups` (the
  reverse id→code maps for building list responses), `listSymbologyRules`
  (code-shaped responses carrying the rule `id`).
- `types.go`, `validate.go` (edited) -- request/response structs plus
  `validateSymbologyIcon` (kebab `icon_ref`, non-blank SVG containing
  `<svg` and explicit `width=`/`height=` -- the Chromium 150×150
  detached-image guard from the rendering slice; `{{COLOR}}` is
  deliberately *not* required, since full-color artwork is valid) and
  `validateSymbologyRule` (valid render context, `domain_field` and
  `domain_value_code` both-or-neither, `min_zoom <= max_zoom`).
- `handler.go` (edited) -- the 8 routes + handlers and updated doc
  comment.

### Frontend (React/TS, `web/src/`)

- **`routes/admin-symbology-icons.tsx`** (new) -- the **Symbology
  icons** admin page: a card grid of every icon with a live thumbnail, a
  Tintable/Full-color badge (does the SVG carry `{{COLOR}}`?), and
  inline edit/delete; a "new icon" form with a lowercased `icon_ref`
  input and an `IconEditor` (tint-color picker + SVG textarea + live
  preview). Wired into the app: a route in `app.tsx` and a **Symbology
  icons** item (Shapes icon) in the Admin section of `app-sidebar.tsx`.
- **`components/admin/icon-picker.tsx`** (new) -- a reusable `IconPicker`
  rendering every known icon as a selectable thumbnail button. Used in
  two places.
- **`routes/admin-feature-class-new.tsx`** (edited) -- the create form's
  old text-only icon `<select>` is replaced by the visual `IconPicker`
  (plus color, and width for line geometries).
- **`routes/admin-feature-class-detail.tsx`** (edited) -- a new
  **Symbology** section (`SymbologySection` + `RuleForm`) listing the
  class's rules (each with a rendered swatch) and doing add/edit/
  **duplicate**/delete. `RuleForm`'s style controls adapt to geometry:
  color always; width + dash for lines; fill/stroke for polygons; scale +
  `IconPicker` for points. A **Duplicate** (copy) action on each row
  reuses `RuleForm` seeded from an existing rule but with a `duplicate`
  flag that routes save to the create (POST) path instead of update
  (PATCH) -- the source is left untouched and the copy starts identical,
  so authoring a class's rule set is "make a base rule, duplicate it,
  narrow the copy (a different subtype, a dashed instead of solid line)"
  rather than re-entering every field. The backend needs nothing new for
  this: a duplicate is just another POST, and symbology_rules has no
  unique constraint on targeting (two rules may share every axis) -- if a
  copy of the default rule is left un-narrowed, nothing flags it (an earlier
  revision said the compiler's "exactly one default" check would; the compiler
  has no such check) and the matcher simply picks one of the two.
- **`hooks/use-admin.ts`** (edited) -- `useAdminSymbologyIcons()` and
  `useSymbologyRules(code)` plus the `AdminSymbologyIcon` /
  `AdminSymbologyRule` / `AdminStyleDef` types.

- **`components/admin/symbology-swatch.tsx`** (new) -- a geometry-aware
  `SymbologySwatch`. The first cut previewed *every* rule through the
  point icon path (`iconDataUri`), but lines and polygons carry no
  `icon_ref`, so that always fell back to a generic circle -- the
  preview only ever really worked for points. This renders a point as
  its icon, a **line** as a stroke at its real color/width, and a
  **polygon** as a filled+stroked chip at its real fill/stroke (fill
  opacity and fallback colors echoing `map-layers.ts`, so the swatch
  matches what the map draws). Used in both the rule list row and, for
  line/polygon rules, as a live preview inside `RuleForm` (points
  already preview through `IconPicker`). Same edit also lets the map's
  polygon branch honor a `width` authored in `style_def` instead of
  hardcoding stroke width 2.

- **Line dash patterns** (`web/src/lib/line-dash.ts`, new) -- a line rule
  can now carry a `style_def.dash` of `solid` / `dashed` / `dotted` /
  `dashdot`, the distinction a utility map leans on (overhead vs
  underground, primary vs secondary). Stored as a semantic name, not a
  raw pixel array, so it reads like the rest of `style_def` (dash names a
  pattern the way `icon_ref` names an asset); `line-dash.ts` owns the
  single name→array mapping the map renderer (`lineDashArray`, applied as
  an OL `Stroke` `lineDash` in `map-layers.ts`, with a round `lineCap`
  for dotted so its near-zero dashes render as dots) and the admin swatch
  (`svgDashArray`, an SVG `stroke-dasharray`) both read, so a pattern
  can't mean two things. Exposed as a **Dash** select for line classes in
  both the rule editor and the create-class form (which already had Width
  but not Dash -- the same gap). Backend: `dash` added to `styleDefRequest`
  / `symbologyRequest`, validated against a `validLineDashes` set, written
  by `buildStyleDef` / the create path; `solid` is the omitted default, so
  a solid line's `style_def` stays free of a redundant key. It flows to
  the map for free -- `style_def` is passed through raw by the schema
  endpoint's pre-resolver, so no read-path change was needed.

### Query-key hygiene

Icon mutations invalidate **both** the admin icon key
(`["admin","symbology-icons"]`) and the public map registry key
(`["symbology-icons"]`), so editing an icon updates the map without a
reload. Rule mutations invalidate **both** the rules key
(`["admin","symbology-rules", code]`) and that class's schema key
(`["feature-class-schema", code]`), since the schema carries the
pre-resolved `symbology` array the map reads.

## What's still open

- **Verification.** The load-bearing caveat: no `tsc`, lint, `go build`,
  `gofmt`, or Playwright run was possible in this environment
  (`bash.exe` `EPERM`). Static + IDE-diagnostic review only. That pass
  is owed before trusting this end-to-end.
- **Real artwork.** This is the *tooling*; the seeded icon set is still
  five generic shapes. Authoring an actual IEC/ANSI utility symbol
  library is the remaining half of `docs/open-items.md`'s "Real sprite
  pipeline" row, and a sprite atlas / CDN over the current
  one-`<svg>`-per-row fetch is still deferred until scale demands it.
- **No auth.** Like the rest of `/admin`, these endpoints are wide open
  by design for now.

## Related docs

- `docs/phase-3-symbology.md` -- the rendering/resolution slice this
  builds an editor for.
- `docs/phase-3-symbology-icon-table.md` -- the icon registry / DB move.
- `docs/open-items.md` -- the "Real sprite pipeline" row this narrows
  and the new "Admin UI for symbology icons and rules" Done row.
