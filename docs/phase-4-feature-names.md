# Phase 4 (slice 2) — a universal feature name, and a real trace result table

**Goal:** let any feature, regardless of class, carry an optional
human-readable name alongside its system id -- and use it to replace
the trace result banner's plain count summary with a real data table
listing every feature a trace reached, by class, id, and name.
**Status:** done and verified against a real Postgres/Martin/API/Vite
stack.

## Why this slice exists

Phase 4's first slice (`docs/phase-4-trace-engine.md`) shipped a real
trace result -- a list of features a downstream/upstream walk reached --
but the only feedback surfaced on the map was a count
("4 features reached, stopped at 1 open device"). The user's own
framing of what was missing: a real GUID "has less meaning to users"
than a name does, and every feature -- not just ones in a trace result --
should be able to carry one, optional, without an admin having to
author it into any one feature class's metadata.

That last point is the real design decision this slice turned on: a
per-feature name isn't a `metadata.fields` row like `condition_rating`
or `device_state` (something an admin adds to one feature class at a
time), because the ask was for every feature to get one identically.
The precedent for exactly that shape of thing already existed --
`deleted_at`, added as a universal system column by the schema compiler
after several feature classes were already compiled
(`api/internal/compiler`'s `buildCreateTable` + a backfill `ALTER
TABLE` branch in `Compile`) -- so `name` follows that same path rather
than becoming a new per-class metadata concept.

## What was built

- **`api/internal/compiler/ddl.go`** -- `buildCreateTable` now emits
  `name text` (nullable, no default) as a system column, right after
  `id`, on every newly compiled feature class.
- **`api/internal/compiler/compiler.go`** -- `Compile`'s existing-table
  branch gained a second backfill block, identical in shape to
  `deleted_at`'s: `if !existingCols["name"] { ALTER TABLE ... ADD
  COLUMN name text }`. All five feature classes compiled before this
  slice (`POLE`, `MV_CONDUCTOR`, `DIST_XFMR`, `DIST_SWITCH`,
  `SUBSTATION_YARD`) were re-compiled to backfill it -- confirmed via
  `\d gis.pole` etc. showing the new column, appended at the end of each
  table (Postgres always appends an `ALTER TABLE ADD COLUMN`; only a
  fresh `CREATE TABLE` gets the logical `id, name, ...` ordering
  `buildCreateTable` defines).
- **`api/internal/crud`** -- `name` joins the same system-column
  treatment `id`/`status`/`deleted_at` already get: `selectColumns`
  reads it back, `formatRow` surfaces it at the response's top level
  (never nested under `attributes` -- it's not a metadata field), and
  `createRequest`/`updateRequest` both gained a top-level `Name
  *string`. `insertFeature`/`updateFeature` accept it as their own
  parameter (mirroring how `geometry` is already handled distinctly
  from the dynamic `values` map) -- present-but-empty-string clears a
  name, absent leaves it untouched, same presence-means-replace
  semantics geometry's own update path already established in
  `docs/phase-3-edit-geometry.md`.
- **`src/hooks/use-feature.ts`** -- `FeatureRecord` gained `name:
  string | null`.
- **`src/components/map/feature-create-form.tsx`** /
  **`feature-edit-form.tsx`** -- both gained a "Name" text input above
  every metadata-driven field, since it's a system-level concept, not
  one of `schema.fields`. The edit form seeds it from the feature's
  current name and diffs it the same way it already diffs attributes
  (`nameChanged`, folded into the existing `canSave`/save-body logic
  alongside attribute and geometry changes).
- **`src/components/map/feature-panel.tsx`** -- the floating panel's
  header promotes a set name to the bold primary line, demoting the
  feature class name and a shortened id to a combined secondary line
  ("MV Conductor · eb2527d7…"). No name set: unchanged from before this
  slice, class name primary, shortened id secondary (previously the
  *full*, untruncated id -- now shortened here too, matching the
  truncation every other id display in this app already uses).
- **`src/components/map/trace-result-table.tsx`** (replaces
  `trace-result-banner.tsx`) -- a floating data table instead of a text
  summary: **Feature class**, **Feature ID** (shortened), **Name** (an
  em dash when unset) columns, one row per feature the trace reached.
  A barrier row (the subset the trace actually stopped at) renders with
  a distinct reddish background/text, matching the map's own red
  barrier-highlight color from the previous slice. Rows are clickable --
  selecting the row's feature, same established pattern every other
  clickable row in this app (connection/relationship rows) already
  follows. Resolves each row's name with its own fetch, independent of
  `voltedge-map.tsx`'s own per-feature geometry fetch for the highlight
  -- the two have different jobs and neither depends on the other's
  result, so a handful of duplicate `GET`s for a trace's typically-small
  feature count was a fine trade against coordinating shared state
  between them.

## How this was validated

Driven against a real Postgres/Martin/API/Vite stack, first via curl
directly against the CRUD engine, then via Playwright end to end.

- `PATCH` a real feature's `name`, confirmed it round-trips via a
  follow-up `GET`; created a new feature with `name` set on `POST`,
  confirmed it came back immediately in the create response.
- Set real names on the four-feature trace chain from
  `docs/phase-4-trace-engine.md` ("Feeder tap to switch S1" → "Switch
  S1" → "Feeder from S1 to XF-1" → "XF-1") and confirmed a downstream
  trace's result table showed all four names correctly, in the right
  rows.
- Confirmed the barrier test still works end to end with names in the
  mix: opening "Switch S1" and re-running the trace showed only 2 rows,
  with "Switch S1" visually marked as the barrier row.
- Confirmed the floating panel header promotes a set name to the
  primary line and falls back correctly for an unnamed feature.
- Confirmed the create form's Name input produces a feature whose panel
  header immediately shows the new name.
- Both themes checked for the table and Name inputs.

## Decisions made

- **A universal system column, not a per-class metadata field.**
  Mirrors `deleted_at`'s own precedent exactly -- the alternative (an
  admin adding a `name`-like field to every feature class's own
  `metadata.fields` rows) would mean a feature class is nameable only
  if someone remembered to opt it in, which doesn't match "optional for
  all features" as a blanket, structural guarantee.
- **Presence-means-replace on update, not a separate PATCH concept.**
  Consistent with how `geometry` already works on the same endpoint --
  one request body, whatever's present gets changed, absence means
  "leave it."
- **The trace table fetches names independently of the map's highlight
  fetch**, rather than sharing one fetch/cache between the two
  components. Both need different, small slices of the same feature
  record; coordinating a shared fetch would have added real complexity
  for a handful of duplicate `GET`s on a typically small trace result.

## What's still open

- **Name isn't shown anywhere else names could help** -- e.g.
  connect-confirm-panel.tsx's `EndpointPicker` and the Terminals/
  Relationships tab's counterpart rows still show only the feature
  class name, not a per-feature name, even though `EndpointPicker`
  already fetches the full feature record. Left out of this pass to
  keep it scoped to what was explicitly asked for (the trace table);
  worth revisiting since the underlying data is already available.
- **No search/filter by name** -- nothing in this app currently lets a
  user find a feature *by* its name; it's display-only for now.
- Everything else tracked in `docs/open-items.md` is unchanged by this
  slice.

## Try it yourself

Same stack as `docs/phase-4-trace-engine.md`'s "Try it yourself". Open
`http://localhost:5173`:

- Select any feature, click Edit -- the form now has a Name field at
  the top. Set one, Save -- the panel header now shows it as the bold
  primary line.
- Run a downstream/upstream trace from a named feature -- the result is
  now a data table (class/id/name per row, clickable), not a text
  banner. Open a barrier device mid-chain and re-run -- the barrier's
  row renders distinctly.

`npm run build && npm run lint` and `go build ./... && go vet ./... &&
gofmt -l .` should all finish clean.
