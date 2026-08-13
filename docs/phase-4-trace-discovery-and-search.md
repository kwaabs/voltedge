# Phase 4 (slice 3) — trace discovery, name display everywhere, search by name

**Goal:** close three small/medium open items left after slice 2
(`docs/phase-4-feature-names.md`): a real endpoint to discover which
`trace_configurations` exist instead of hardcoded codes, the
per-feature name shown everywhere a counterpart feature is listed (not
just the panel header and trace table), and a way to actually find a
feature *by* its name.
**Status:** done and verified against a real Postgres/Martin/API/Vite
stack. Deliberately excludes isolation/loop-mesh detection -- the one
remaining Phase 4 item explicitly left for later, not picked up here.

## Why this slice exists

Slice 2 closed with three items still open in `docs/open-items.md`,
all sized S or S–M, all direct extensions of work already in place
rather than new design surface:

- `feature-panel.tsx`'s two trace trigger buttons hardcoded
  `DOWNSTREAM_TRACE`/`UPSTREAM_TRACE` by code -- fine for four fixed,
  seeded configs, but wrong the moment `trace_configurations` becomes
  something authored freely.
- A per-feature name, once set, was only ever shown in the floating
  panel's own header and the trace result table -- `EndpointPicker`
  (the connect/relationship confirm panel) and the Terminals/
  Relationships tabs' counterpart rows still showed only the feature
  class name, even though the data was one fetch away.
- Names were display-only -- nothing let a user find a feature *by*
  its name.

## What was built

### 1. `GET /trace/configurations`

- **`api/internal/metadata/load.go`** -- `ListTraceConfigurations`
  reads every `trace_configurations` row, ordered by code.
- **`api/internal/trace/graph.go`** -- `listConfigurations` resolves
  each row's `direction_value_id` back to its raw direction code (e.g.
  `DOWNSTREAM`) via `metadata.DomainValue`, since a client picking a
  trace by *direction* (not knowing a specific seeded code exists)
  needs that raw code, not just the domain value's internal id.
- **`api/internal/trace/handler.go`** -- `Routes` gained `GET
  /trace/configurations`, backed by `listConfigurationsHandler`,
  returning `{"trace_configurations": [{code, name, direction}, ...]}`.
- **`web/src/hooks/use-trace-configurations.ts`** (new) -- a
  `useQuery` hook, same shape and `staleTime` as `useFeatureClasses`
  (metadata that changes rarely, not worth polling).
- **`web/src/components/map/feature-panel.tsx`** -- `runTrace` now
  takes a raw direction (`"DOWNSTREAM" | "UPSTREAM"`), resolves the
  matching `trace_configurations` row via
  `.find((c) => c.direction === direction)`, and sends that row's
  `code` in the trace request. The two trigger buttons are disabled
  (with an explanatory title) when no matching configuration exists,
  instead of assuming one always does.

### 2. Feature name shown everywhere a counterpart is listed

- **`web/src/components/map/connect-confirm-panel.tsx`** --
  `EndpointPicker` (which already calls `useFeature` per endpoint for
  its terminal dropdown) now promotes `feature.name` to the primary
  label when set, with `${className} · ${shortId}` demoted to the
  secondary line -- the exact same primary/secondary promotion
  `feature-panel.tsx`'s own header already established in slice 2.
- **`web/src/components/map/feature-panel.tsx`** -- `TerminalsTab` and
  `RelationshipsTab` render a variable-length list of counterpart rows,
  so a single `useFeature` call (a hook, can't run in a loop) doesn't
  fit. A new shared `useCounterpartNames` hook resolves every row's
  name with its own batched fetch (`Promise.all` over
  `GET /features/{class}/{id}` per counterpart), keyed by a stable
  `feature_class:feature_id` join-string derived from the row list
  (not the row list's own object identity, which is a fresh array on
  every render since each tab recomputes it inline from query data) --
  refetches only when the actual set of counterparts changes. Both
  tabs' counterpart rows now show the same primary-name/secondary-class
  promotion.

### 3. Search by name

- **`api/internal/crud/sql.go`** -- `listFeatures` gained an optional
  `nameQuery` parameter; when non-empty, adds `AND name ILIKE ?` (bound
  as `%query%`) to the existing list query. Absent, list behaves
  exactly as before -- this is a narrow, name-specific search, not the
  general list filtering/sorting still tracked as its own separate open
  item.
- **`api/internal/crud/handler.go`** -- `listHandler` reads an optional
  `?name=` query param and threads it through.
- **`web/src/components/map/feature-search.tsx`** (new) -- there's no
  single cross-class search endpoint (each feature class is its own
  physical table), so this fans out one `GET /features/{code}?name=`
  per feature class in parallel (same fan-out-per-catalog-entry
  reasoning `layers-panel.tsx` already uses for Martin's catalog) and
  merges results client-side. Debounced (300ms) and length-gated
  (2+ characters) before firing, and each class's results capped
  (`PER_CLASS_LIMIT = 8`) to keep the fan-out cheap while typing. A
  feature class whose search request fails is silently excluded rather
  than failing the whole search. Clicking a result selects and focuses
  that feature, same as any other clickable feature reference in this
  app.
- **`web/src/routes/map.tsx`** -- `FeatureSearch` sits in the map's
  top-right toolbar, alongside `NewFeatureMenu` and `LayersPanel`.

## How this was validated

Backend confirmed via `curl` against a real Postgres/API stack before
any frontend work:

- `GET /trace/configurations` returned all four seeded configs with
  correct `code`/`name`/`direction`.
- `GET /features/POLE?name=alpha` (after setting one pole's name to
  "Search Test Pole Alpha") returned exactly that pole;
  `?name=search%20test` (a different substring, same target) matched
  it too; a query with no match returned zero rows; the endpoint with
  no `?name=` at all still returned the full unfiltered list, confirming
  the new parameter is additive, not a behavior change for existing
  callers.

Frontend confirmed via Playwright against the real dev stack (Vite +
Go API + Postgres + Martin, no mocking):

- Selected a networked feature, opened its panel, confirmed the
  downstream/upstream trigger buttons resolve a real config and the
  trace runs, producing the same data-table result slice 2 built.
- Selected the named pole ("Search Test Pole Alpha"), confirmed the
  panel header, connect-panel `EndpointPicker`, and any Terminals/
  Relationships counterpart rows referencing it all show the name as
  the bold primary label, falling back to the class name for features
  with no name set.
- Typed a partial, case-varying match into the new search box,
  confirmed a dropdown listing the matching feature (name, class,
  short id) appeared, and that clicking it selected and focused the
  feature exactly like clicking it on the map would; confirmed a
  no-match query shows an explicit "no results" message and clearing
  the input closes the dropdown.

`npm run build && npm run lint` and `go build ./... && go vet ./... &&
gofmt -l .` all finish clean.

## Decisions made

- **Resolve trace configs by direction, not by assuming a code.**
  `feature-panel.tsx` now looks up a `trace_configurations` row by its
  raw `direction` value rather than a hardcoded string like
  `"DOWNSTREAM_TRACE"` -- the trigger buttons stay correct even if the
  seeded config's code changes, and disable themselves cleanly if no
  matching configuration exists at all instead of firing a request that
  can only 404.
- **Per-row name fetches, not a shared cache with the single selected
  feature's own `useFeature` call.** Same reasoning `trace-result-table.tsx`
  already established in slice 2: a bounded, typically small list of
  counterparts is a fine place to accept a handful of extra `GET`s
  rather than build shared-state coordination between very different
  consumers.
- **Search fans out per feature class instead of adding a cross-class
  search endpoint.** The physical data model is already one table per
  feature class (the compiler's whole reason for existing); a
  dedicated cross-class search endpoint would mean either a
  denormalized name index or a `UNION ALL` across every compiled
  table, neither of which felt justified by an app-level search box
  over a handful of classes at this scale. Debouncing, length-gating,
  and a per-class result cap keep the actual cost of that fan-out low.
- **Name search stays scoped to `?name=` on the existing list endpoint**,
  not folded into the still-open, more general "no filtering or sorting
  on list" item -- a narrower, purpose-built addition rather than
  reaching for the bigger unbuilt feature.

## What's still open

- **Isolation and loop/mesh detection** -- still deliberately deferred,
  unchanged by this slice; see `docs/phase-4-trace-engine.md`.
- **`cross_transformers`/`respect_phase` enforcement** -- still
  unenforced, unchanged by this slice.
- **No persistent/cached graph** -- unchanged; revisit only if real
  data scale gives a signal it's needed.
- **General list filtering/sorting** -- `?name=` is a narrow,
  name-specific addition, not the general case; still open.
- Everything else tracked in `docs/open-items.md` is unchanged by this
  slice.

## Try it yourself

Same stack as `docs/phase-4-trace-engine.md`'s "Try it yourself". Open
`http://localhost:5173`:

- Select a networked feature (e.g. an `MV_CONDUCTOR` or `DIST_XFMR`),
  run a downstream/upstream trace from its panel header -- the trigger
  buttons now resolve the real seeded configuration rather than
  assuming one exists.
- Give a feature a name via its edit form, then open a "+ New
  connection"/"+ New relationship" workflow involving it, or view it as
  a counterpart in another feature's Terminals/Relationships tab -- the
  name shows up as the primary label everywhere that feature is
  referenced, not just its own panel.
- Type a few characters of a known feature name into the "Search by
  name…" box in the map's top-right toolbar -- a dropdown lists
  matches across every feature class; click one to select and focus it.

`npm run build && npm run lint` and `go build ./... && go vet ./... &&
gofmt -l .` should all finish clean.
