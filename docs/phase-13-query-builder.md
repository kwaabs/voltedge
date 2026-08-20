# Phase 13 — query builder

Turns the map's name-only search box into a **structured query builder**. Today
you can find a feature only by typing part of its name (`FeatureSearch` +
`GET /features/{code}?name=...`, which filters just `name ILIKE` and `status`).
This phase lets you **pick a feature class, load its attributes, and filter on
them with typed operators** (`=`, `≠`, `<`, `>`, `contains`, `in`, `between`,
`is null`, …) combined with a flat **match-all (AND) / match-any (OR)** toggle.
The result is an **array of features rendered in a table**, mirroring the trace
result table: every match highlights on the map, and each row carries a **zoom
button** that flies the map to that feature.

## Decisions

Locked with the user before building:

- **Flat AND/OR first, nested groups later.** Slices 1–3 shipped one `match`
  combinator (`all`/`any`) over a flat list of conditions. The wire shape (a
  `match` + a `conditions` array) was chosen so nested groups could be added
  **without a breaking change** — which is exactly what Slice 4 did, adding an
  optional `groups` array so `(A AND B) OR C` is now expressible.
- **Single feature class per query.** You query one class at a time; its
  attributes are what the operand dropdowns load. No cross-class joins.
- **Highlight all, zoom per-row.** Every matching feature is highlighted on the
  map at once (a distinct highlight layer). Rows do **not** zoom on whole-row
  click; instead each row has an explicit **zoom button** so the user chooses
  which match to fly to — same `select(class, id, {focus:true})` call the trace
  table uses.

## Slices

- **Slice 1 — backend query endpoint.** `POST /features/{code}/query`: typed,
  data-type-gated condition builder over a class's real columns, AND/OR
  combinator, paging identical to the list endpoint. Pure SQL-fragment builder
  with full unit coverage. ✅
- **Slice 2 — FloatingPanel primitive.** A reusable draggable / resizable /
  closeable, viewport-clamped floating window (`ui/floating-panel.tsx`). ✅
- **Slice 3 — query builder UI + map wiring.** Class picker → attribute-aware
  condition rows → AND/OR toggle → results table with per-row zoom button; a
  query-result highlight layer on the map (distinct colour from trace); the
  search box becomes a button that toggles the panel. ✅
- **Slice 4 — nested groups + CSV export.** Recursive condition groups
  (`(A AND B) OR C`) end-to-end — a recursive `buildGroup` on the backend and a
  recursive group-tree builder in the panel — plus CSV export of the result set.
  ✅

## Slice 1 — backend query endpoint

The list endpoint (`GET /features/{code}`) can only filter `name ILIKE` and
`status =`. That can't express "voltage_class = 11kv **and** span_length ≥ 100"
or an OR. Rather than overload the list query params, this adds a dedicated
**POST** endpoint that takes a JSON filter body.

### Endpoint

`POST /features/{code}/query`

The chi route is `POST /{code}/query`; `query` is an unambiguous literal segment
(the only other `POST /{code}/...` route is the bare create, and update is
`PATCH`), so no route collision.

Request body (`queryRequest` in `api/internal/crud/types.go`):

```json
{
  "match": "all",
  "conditions": [
    { "field": "voltage_class", "op": "eq", "value": "11kv" },
    { "field": "span_length",   "op": "gte", "value": 100 },
    { "field": "install_date",  "op": "between", "values": ["2020-01-01", "2023-12-31"] }
  ],
  "sort": "name",
  "order": "asc",
  "limit": 50,
  "offset": 0
}
```

- `match` — `"all"` (AND, the default) or `"any"` (OR) across all conditions.
- `conditions` — flat list of `{field, op, value|values}`. Empty/omitted is
  legal and pages the whole class, exactly like a filter-less list.
- `sort`/`order`/`limit`/`offset` — identical semantics to the list endpoint:
  an unrecognized `sort` silently falls back to `created_at`, `id` is always
  appended as a stable tiebreaker, `limit` clamps to `[1, 500]` defaulting to
  `defaultListLimit` (50).

Response is the same envelope as the list endpoint — `{ "features": [ ... ] }` —
with each row already carrying `geom_json` (via `selectColumns`), so the
frontend can highlight matches on the map without a re-fetch.

### Operators and type gating

Each condition's operator is checked against the resolved column's `data_type`
before any SQL is built. Invalid combinations return a clean `400`, not a
silently-wrong query.

| Operator | Operand | Valid column types | Notes |
| --- | --- | --- | --- |
| `eq`, `ne` | `value` | any | equality / inequality |
| `lt`, `lte`, `gt`, `gte` | `value` | `numeric`, `integer`, `date`, `timestamp` | ordered types only — a `>` on text or boolean is rejected |
| `contains`, `starts_with` | `value` (string) | `text` | `ILIKE`; `contains` wraps `%v%`, `starts_with` uses `v%` |
| `in` | `values` (1+) | any | expands one bind placeholder per value |
| `between` | `values` (exactly 2) | ordered types | `col BETWEEN ? AND ?` |
| `is_null`, `not_null` | none | any | no operand |

**Queryable columns.** `columnDataType` is the whitelist: the system columns
every class has (`name`/`status` → text, `id` → uuid, `created_at`/`updated_at`
→ timestamp, `subtype` → integer *only when the class defines subtypes*) plus
the class's own admin-authored fields (resolved via `loaded.FieldByName`).
`geom` and `deleted_at` are deliberately **not** filterable — `deleted_at` is
always forced `IS NULL` by the query, so exposing it would only invite
contradictions. Any other field name is an unknown-field `400`.

### Safety model

The builder is SQL-injection-safe by construction:

- The only things ever spliced into SQL **text** are (1) a double-quoted column
  name that came from the `columnDataType` whitelist and (2) a fixed operator
  token from a closed `switch`. A client-supplied field name that isn't in the
  whitelist never reaches SQL — it's a `400`.
- **Every** operand is a bind parameter (`?`), including each element of an
  `in` list and both ends of a `between`. No value is ever interpolated.
- The whole condition group is **parenthesized** before being ANDed with the
  always-present `deleted_at IS NULL` guard, so a `match: "any"` (OR) group can
  never leak soft-deleted rows.
- `maxQueryConditions = 50` caps clause count so a malformed/hostile request
  can't hand Postgres a WHERE clause with thousands of terms.

### Files

- **`api/internal/crud/query.go`** (new) — `buildCondition` (one clause →
  fragment + args), `buildConditions` (fold into one AND/OR group),
  `queryFeatures` (assemble + run, reusing `selectColumns`/`scanRows`/`formatRow`
  and the list endpoint's sort/paging), plus the `columnDataType` whitelist,
  `isOrderedType`, and `comparisonSQL` helpers.
- **`api/internal/crud/types.go`** — `queryRequest` and `queryCondition` DTOs.
- **`api/internal/crud/handler.go`** — `queryHandler` and the
  `r.Post("/{code}/query", ...)` route.
- **`api/internal/crud/query_test.go`** (new) — table-driven tests for
  `buildCondition` (every operator, the produced fragment and bind args) and its
  rejections (ordered op on text/boolean, `contains` on non-text or non-string,
  `between` wrong arity, empty `in`, nil `eq` value, unknown field/op, `subtype`
  when the class has none), plus `buildConditions` (AND vs OR join, empty group,
  over-limit, error propagation). All pure — no DB needed.

### Verification owed

The Bash tool is unavailable in this environment, so these were **not** run —
please run them:

```sh
cd api && go build ./... && go vet ./... && go test ./internal/crud/...
```

No migration is needed — the endpoint queries the existing compiled physical
tables.

## Slice 2 — FloatingPanel primitive

The query builder is a **floating, draggable, resizable, closeable** window.
Nothing reusable like that existed — every current overlay (attribute panel,
trace result table, geometry-edit toolbar) is a fixed-position box. Rather than
bake dragging into the query panel, this slice adds a generic shell.

- **`web/src/components/ui/floating-panel.tsx`** (new) — `FloatingPanel`:
  - **Drag** by the header (`cursor-move`), **resize** from the bottom-right
    grip (`cursor-nwse-resize`), **close** with the X button.
  - Owns its own `{x, y, width, height}` rect, seeded from an optional
    `initialRect` (a caller only decides where it first appears). Panel geometry
    is pure view state, so it lives in the component, not a store — a caller
    just wires `onClose`.
  - `clampRect` keeps the window fully inside the viewport on every drag/resize
    **and** re-clamps on window `resize`, so it can never be dragged off-screen
    or opened larger than a small display.
  - Move/up listeners bind on `window` for the gesture's lifetime (held in a
    ref), so a fast drag that outruns the pointer still tracks and the gesture
    ends wherever the pointer is released.
  - `minWidth`/`minHeight` (defaults 320×200) floor the resize.

### Verification owed

```sh
cd web && npm run typecheck && npm run lint
```

## Slice 3 — query builder UI + map wiring

The panel itself, the map highlight, and retiring the old name-search box.

### The builder

- **`web/src/stores/query-store.ts`** (new) — `useQueryStore`: ephemeral (not
  persisted), holds `open` (the toolbar toggle), plus `results` + `resultClass`
  (the last run's matches, read by both the map and the results table). Panel
  geometry is **not** here — the FloatingPanel owns its own position/size.
  Closing the panel also drops the results so a highlight can't outlive the
  window that produced it.
- **`web/src/components/map/feature-query-panel.tsx`** (new) — `FeatureQueryPanel`,
  inside a `FloatingPanel`:
  - **Class picker** — geometric feature classes only (a geometry-less class has
    nothing to highlight or zoom to), sorted by name.
  - **Attribute-aware condition rows** — picking a class loads its schema
    (`useFeatureClassSchema`); each row is `field → operator → value`. The field
    list is the system columns (`name`, `status`, `subtype` when the class has
    them) plus the class's own non-`jsonb` fields. The **operator** dropdown is
    filtered to what the field's `data_type` allows — mirroring the backend's
    gating exactly, so an offered operator can never come back a `400`. The
    **value** control adapts: a dropdown for a coded/domain field, `status`, or
    `subtype`; a Yes/No select for boolean; a date / number / text input by
    type; two inputs for `between`; comma-separated text for `in`; and nothing
    for `is empty` / `is not empty`.
  - **Match toggle** — a flat *match all (AND)* / *match any (OR)* segmented
    control over the whole condition list.
  - **Run** — POSTs `{ match, conditions, limit: 200 }` to
    `/features/{code}/query`, converting each raw input to its JSON type
    (number/boolean/string). Incomplete rows (a value-taking op with an empty
    value) are dropped rather than sent. Zero matches raises a warning toast; an
    API error raises an error toast.
  - **Results table** — one row per match (name, status, short id) with a
    per-row **zoom button** (`Crosshair`) calling
    `select(resultClass, id, {focus:true})` — the same focus-to-feature call the
    trace table uses. Rows do **not** zoom on whole-row click, per the locked
    decision.

### Map highlight

- **`web/src/lib/map-layers.ts`** — new `"query-result"` `HighlightRole`, colour
  `#ec4899` (pink — a hue no other role uses).
- **`web/src/components/map/voltedge-map.tsx`** — a dedicated
  `queryResultSource`/layer (zIndex 6, `highlightStyleForFeature`). Its effect is
  **synchronous** — unlike the trace/isolation/load-flow/fault-current highlights
  it doesn't re-fetch each feature, because the query response already carries
  every match's geometry — it just reads `useQueryStore().results` and repaints,
  tagging each feature `query-result`. Every match highlights at once.

### Toolbar

- **`web/src/routes/map.tsx`** — the name-search box (`FeatureSearch`) is replaced
  by a **Query** button that toggles the panel (`useQueryStore().toggle()`); the
  panel is mounted only while `open`.
- **`web/src/components/map/feature-search.tsx`** is now **orphaned** (no
  importers) and can be deleted. It was left in place only because the Bash tool
  is unavailable here to `rm` it. Note its `use-feature-name-search.ts` hook is
  **still used** by `diagram-canvas.tsx`'s add-feature-by-name search and must
  stay.

### Verification owed

```sh
cd web && npm run typecheck && npm run lint
# and delete the now-orphaned component:
git rm web/src/components/map/feature-search.tsx
```

## Slice 4 — nested groups + CSV export

Two follow-ons: let a query express `(A AND B) OR C` by nesting groups, and let
the user export the result set to CSV.

### Nested groups — the wire shape

The Slice 1 body was deliberately left extensible, so this is **additive, not a
break**: a `queryGroup` is a `match` + `conditions` **+ an optional `groups`
array of child `queryGroup`s**, and `queryRequest` *is itself the root group*
(it already had `match`/`conditions`; it gains `groups`). A flat Slice-3 query
leaves `groups` empty and produces byte-identical SQL to before.

```json
{
  "match": "any",
  "conditions": [{ "field": "voltage_class", "op": "eq", "value": "33kv" }],
  "groups": [
    {
      "match": "all",
      "conditions": [
        { "field": "span_length",  "op": "gte", "value": 5 },
        { "field": "is_energized", "op": "eq",  "value": true }
      ]
    }
  ]
}
```

→ `("voltage_class" = ? OR ("span_length" >= ? AND "is_energized" = ?))`

### Backend — recursive `buildGroup`

`buildConditions` (a single flat fold) is replaced by **`buildGroup`**, which
folds one group into a single **parenthesized** fragment: its own leaf
conditions and each child group's fragment, joined by `AND` (`match: "all"`) or
`OR` (`match: "any"`), recursing into children. Invariants that keep the
recursion honest:

- **Every group is parenthesized**, so a nested `OR` can't change how the level
  above it binds — and the root, ANDed with the always-on `deleted_at IS NULL`
  guard, still can't leak soft-deleted rows through an `OR`.
- **Empty groups drop out** — an empty (or all-empty-children) group contributes
  no fragment rather than an invalid `()`, so a filter-less query still legally
  pages the whole class.
- **`maxGroupDepth = 5`** bounds recursion (root = depth 0); a body nested deeper
  is a clean `400`.
- **`maxQueryConditions = 50` now counts the whole tree.** `countConditions`
  sums every descendant's leaf conditions and the cap is checked **once at the
  root**, so nesting can't route around the ceiling.

Files: `api/internal/crud/types.go` (`queryGroup` type; `Groups` field on
`queryRequest`), `api/internal/crud/query.go` (`buildGroup`, `countConditions`,
`maxGroupDepth`; `queryFeatures` now builds a root group and calls `buildGroup`),
`api/internal/crud/query_test.go` (`TestBuildGroup` — AND/OR joins,
case-insensitive `any`, empty group, the `(A OR (B AND C))` nesting, empty
sub-group dropping out, over-limit **across the tree**, over-deep nesting, and
error propagation from a nested condition — plus `TestCountConditions`). The
existing `TestBuildCondition`/`TestBuildConditionRejects` are unchanged.

### Frontend — recursive group builder + CSV

`feature-query-panel.tsx` is rewritten around a recursive tree instead of a flat
condition list:

- **State** — the builder is a single root `Group` (`{ id, match, conditions,
  groups }`) in `useState`, with a `nextId` ref handing out stable ids. Pure
  module-level tree helpers do the edits: `updateGroupById` (replace one node),
  `removeGroupById` (drop a sub-group), and `applyConditionPatch` (snap the
  operator back to the field's first valid one and clear stale values when the
  field changes). An `actions` object threads the mutators down the tree.
- **`GroupEditor`** — a recursive component: a **match toggle** (shown once a
  group has ≥ 2 items), the group's condition rows (`ConditionRowEditor`, reused
  as-is), then its child groups (each rendered by recursing, indented with a left
  rule and a remove button), and **Add condition** / **Add group** buttons —
  *Add group* is hidden at `depth >= MAX_DEPTH` (5), matching the backend so the
  UI never offers a nesting the server would reject. The root has no border and
  can't be removed.
- **Serialize** — `serializeGroup` walks the tree into the request body,
  **dropping incomplete conditions** (a value-taking op with no value) and
  **pruning empty sub-groups**, so a half-filled row never 400s the run. Body is
  `{ ...serializeGroup(root), limit: 200 }`.
- **CSV export** — a results header shows the match count and an **Export CSV**
  button (disabled at 0 rows). `resultsToCsv` emits `id, name, status` then the
  **sorted union of every row's attribute keys** (so a class with optional
  fields still gets an aligned header), RFC-4180-escaped (`csvEscape`);
  `downloadCsv` triggers a client-side download named `${resultClass}-query.csv`
  via an object-URL anchor. No server round-trip — it exports the rows already in
  hand.

The map highlight, the per-row zoom button, and the `query-store`/toolbar wiring
are unchanged from Slice 3.

### Verification owed

```sh
cd api && go build ./... && go vet ./... && go test ./internal/crud/...
cd web && npm run typecheck && npm run lint
```
