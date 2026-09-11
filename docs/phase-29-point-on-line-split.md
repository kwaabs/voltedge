# Phase 29 — point-on-line split suggestions

**Goal:** when a user creates a POINT feature that lands *on* an existing line
(a switch or tap dropped onto a conductor, say), the in-app assistant proposes
**"Split \<line\> here"** as a suggestion chip — right beside the
connect/relate chips it already shows after a create. Accepting it cuts the
line at the point's location so the point sits at the new junction. It never
happens automatically; the human's click is the commit, exactly like every
other assistant suggestion.

**Status:** Slices 1 (map, backend), 2 (map, frontend) and 3 (diagram
sectionalize, backend) code-complete, **verification owed** (Bash is blocked in
this authoring environment — no `go build`/`vet`/`tsc`/psql/browser could run;
the checks below are owed on a real stack). Slice 3b (diagram sectionalize UI)
not started.

## Why this exists, and what was already there

Asked whether placing a point on a line splits it or suggests a split, the
honest answer was **neither** — before this phase there was no auto-split and no
split *suggestion* anywhere. A split only ever happened through the manual
Phase 20 tool (`docs/phase-20-split-copy.md`): select a line, pick "Split",
click the line → `POST /features/{code}/{id}/split`. The assistant
(`docs/phase-19-assistant.md`) only ever proposed `connection` and
`relationship` actions. So the workflow to put a device *on* a conductor was
two deliberate, disconnected steps (split, then place, then connect) with no
prompt tying them together.

Most of the machinery was already built, which is why the map slice is small:

- **The write path is done.** `crud.SplitLine` / `POST
  /features/{code}/{id}/split` (`api/internal/crud/split.go`) cuts a
  `LineString` at a `split_point` via `ST_LineLocatePoint`/`ST_LineSubstring`,
  shortens the original to the near half, and creates the far half as a new
  feature. `features:write`, `LineString`-only, near-endpoint cuts rejected.
- **The suggestion surface is done.** `api/internal/assistant/suggest.go`
  produces ranked, per-target action groups; `assistant-suggestions.tsx`
  renders them as chips; a click POSTs prebuilt args. It already fires on every
  create-with-geometry (`feature-create-form.tsx`).
- **The manual split UI plumbing is done.** `draw-store.ts`'s
  `startSplitDraft(line, splitPoint, seed)` → the split branch in
  `feature-create-form.tsx` already seeds the far half's attributes from the
  source line and POSTs to `/split`.

So the only genuinely new work is **(1) detecting the point-on-line
coincidence** and **(2) surfacing a `split` action kind** through the existing
chip flow.

## Locked decisions

Three forks, settled with the user before any code:

- **Commit = seed the split form**, not one-click. Accepting the chip opens the
  existing manual-split create form pre-filled from the source line; the user
  confirms the far half's subtype/name/attributes and Saves. This reuses the
  proven `/split` commit path verbatim and adds almost no new write logic, and
  eyeballing the far half's attributes on a split is genuinely useful (a split
  wire often *should* get a distinct name/asset id). One-click (server-side
  attribute copy) is a possible later enhancement, not v1.
- **Surfaces = map + diagram.** The map is this phase; the diagram is its own
  slice (below), because a diagram "line" is a different thing entirely.
- **Diagram = full bus sectionalize** (not cosmetic-only) — see Slice 3.

## Slice 1 — detection engine + backend action (this slice)

- **`api/internal/crud/split_suggest.go`** (new) — `SuggestSplits(point)`, the
  read half of the assist. For each **line-geometry** feature class that's
  compiled, it scans the physical table for simple `LineString`s whose closest
  point to the source lands within a **tight tolerance** and **strictly in the
  line's interior**:
  - **Tolerance** is a new, tight constant
    (`splitSuggestDefaultToleranceMeters = 1.0`) — deliberately *not* the
    connectivity suggest radius (hundreds of metres). A split candidate is a
    line the user *deliberately* dropped a point onto, so anything past about a
    metre is a different line, not a split target.
  - **Interior guard** reuses `split.go`'s `splitEndpointEpsilon` — a point on
    (or a hair from) an endpoint is no split, the same guard `SplitLine`
    enforces, so a candidate the engine offers is one the write path will
    actually accept.
  - **The cut point** is `ST_ClosestPoint(line, point)` snapped onto the line
    (so a point a few centimetres off still cuts exactly on the line),
    transformed back to **EPSG:4326 lng/lat** — the wire convention
    `startSplitDraft` and `POST .../split` both already speak.
  - **Point sources only.** A line/polygon/`none` source has no single
    "where to cut" location, so `SuggestSplits` returns empty (not an error) for
    them — the assist is specifically "a point dropped onto a line."
  - Rule-free by design: unlike a connection suggestion, *any* simple
    `LineString` the point sits on is splittable — no `network_rule` needed — so
    this is a plain spatial query, not a rule-oracle walk. The per-class scan
    (subtype-column detection, geography distance, nearest-first) mirrors the
    connectivity engine's `nearbyCandidates`.
- **`api/internal/assistant/suggest.go`** — a new `splitActions` beside
  `connectionActions`/`relationshipActions`, gated on a new `wantSplits :=
  kindWanted(...,"split") && user.Has(auth.PermFeaturesWrite)` (a split ends in
  `features:write`, so it gates on that independently — same "don't propose what
  the user can't carry out" reasoning the other two use). It emits `action{Kind:
  "split", Tool: "split_line", Title: "Split \<line\> here", Target: line, Args:
  {feature_class, feature_id, split_point:{lng,lat}}}`. This slots into the
  existing `groupAndCap` untouched: a line target that's *also* a legal
  connection candidate lists both (`[connect…, split]`) under one row; split
  candidates always carry a real distance (~1 m) so they clear the 200 m
  group-level distance filter easily. Unlike the establish kinds, `Args` isn't a
  ready-to-POST body — it's the input the frontend hands to the manual-split
  form (Slice 2).
  - No new request field: `SuggestSplits` re-reads the source point's own
    geometry by id, so the existing `{feature_class, feature_id}` body the
    frontend already sends is enough. An empty `Kinds` (the default
    `requestSuggestions` sends) now runs splits too.

### Verification owed (Slice 1)

Bash is blocked in this environment, so none of this has been run yet:

- `go build ./... && go vet ./... && gofmt -l .` clean (new `crud` file +
  `assistant` edits; confirm no import cycle — `crud` imports neither
  `assistant` nor `connectivity`/`relationship`, checked statically).
- Curl against a real Postgres/API: create a point feature *on* a seeded
  conductor, `POST /assistant/suggestions` with just `{feature_class,
  feature_id}`, and confirm a `split` action comes back with the target line and
  a `split_point` that actually lies on it; place the point a few metres *off*
  any line and confirm no `split` action; place it right at a conductor's
  endpoint and confirm the interior guard drops it; confirm a user without
  `features:write` gets no `split` actions (200, filtered) while still getting
  connect/relate.

## Slice 2 — chip surfacing + commit (this slice)

The chip flow, entirely reusing the Phase 20 manual-split plumbing:

- **`web/src/stores/assistant-store.ts`** — widened `SuggestionAction.kind` to
  add `"split"` and `tool` to add `"split_line"`, with a comment spelling out
  that a `split` action's `args` (`{feature_class, feature_id, split_point:
  {lng,lat}}`) is **not** a ready-to-POST body like the establish kinds — it's
  the input handed to the manual-split create form.
- **`web/src/components/map/assistant-suggestions.tsx`** — a third branch at the
  top of `establish()`: for `kind === "split"` it fetches the source line
  (`GET /features/{class}/{id}` — the action target only carries name/subtype,
  not attributes) and calls
  `useDrawStore.getState().startSplitDraft(feature_class, feature_id,
  split_point, {name, subtype, attributes})`, then dismisses the chip and
  returns. **No POST, no query invalidation, no success toast** — the form
  opening *is* the feedback, and nothing is committed until that form's own
  Save hits `/split`. The catch block gains a split-specific fallback ("Could
  not open the split for this line") for a failed line fetch.
  - **Icon/label:** a single-action split chip shows a `Scissors` (not `Plus`)
    button; a split action inside a multi-action expanded row is labelled
    "Split" (not "Establish"). The title itself ("Split \<line\> here") comes
    from the backend.

Because the split routes through the existing create form, a split target that's
*also* a connection candidate shows both under one row — the "+" establish and
the Scissors split side by side. After the split's own Save completes, the
existing re-suggest on both halves *and* the point makes the connect chips
appear on their own — no compound auto-connect transaction.

**Cosmetic gap (acceptable v1):** the dashed live cut-preview in
`voltedge-map.tsx` freeze-frames from `splitPreviewCoordsRef`, which the manual
tool populates during its hover phase. The chip flow has no hover phase, so that
ref is null and no dashed preview shows while the split-draft form is open — the
split still commits correctly, there's just no preview line.

### Verification owed (Slice 2)

- `tsc --noEmit` / lint / `vite build` clean (new `Scissors` import, widened
  union types, the `SplitArgs` cast, the `FeatureRecord`/`useDrawStore` imports).
- In a live browser: create a point on a conductor → confirm a "Split \<line\>
  here" chip with a Scissors icon appears alongside any connect chips; click it →
  the manual-split create form opens pre-filled with the line's name/subtype/
  attributes; Save → the line is cut at the point and the far half is created;
  confirm no chip appears for a point placed off any line.

## Slice 3 — diagram bus sectionalize, backend (this slice)

A diagram "line" is not a map line. It's one of three things, and only one is
splittable:

1. A conductor drawn as a **derived wire** between two placed connectivity
   endpoints — no independent geometry, nothing to cut.
2. A conductor/busbar drawn as a **hand-drawn placement `path`** (migration
   0033, the `diagram_geometry_type = 'line'` busbars — see
   `docs/phase-27-mv-network.md`'s follow-up). The only splittable case.
3. Schematic stencil/bus elements — decorative, not feature-backed.

And `POST .../split` cannot do it: a diagram busbar's real feature is often a
**point** (`MV_BUSBAR` is `geometry_type = 'point'`, drawn as a bar via its
`diagram_geometry_type` override), which `SplitLine` rejects, and a diagram
click is in arbitrary schematic coordinates with no lng/lat. So the diagram case
is **full bus sectionalizing** — its own new backend, built this slice.

### Locked decisions (Slice 3, via AskUserQuestion)

- **Scope = full backend this slice, UI next.** The whole transactional
  sectionalize endpoint ships now; the diagram-canvas gesture that calls it is
  Slice 3b.
- **The far half inherits the original's map geometry.** `MV_BUSBAR` is
  `geometry_optional`, so a busbar may or may not carry a map point. If the
  original *has* one, the far half gets an **offset copy** of it (nudged
  `sectionalizeOffsetMeters = 5 m` in EPSG:3857 so the two points don't stack
  invisibly — the same offset-and-drag idea as a Phase 20 copy); if the original
  is diagram-only, the far half is minted diagram-only too (no map geometry).
- **A connection whose other end isn't on this diagram stays with the original
  (near) half.** It has no diagram anchor to project onto the bar, so there's no
  basis to move it — the conservative default.

### What it does

**`api/internal/diagram/sectionalize.go`** (new) —
`POST /diagrams/{id}/placements/{placementID}/sectionalize`, all in one
`db.RunInTx` (mirroring `crud.SplitLine`):

- **Request:** `{ at: {x, y}, new_bus: {subtype?, name?, attributes},
  sectionalizer?: {feature_class, feature_id, near_terminal?, far_terminal?} }`.
  `at` is the cut point in the **diagram's own local x/y space** (a placement
  `path` coordinate), *not* lng/lat — a diagram click has no geographic
  location. `new_bus` fills out the far-half feature exactly like a manual
  create; it deliberately carries **no geometry** (that's inherited, above).
- **RBAC beyond the mount.** `/diagrams` already gated `diagrams:write`
  method-aware, but sectionalize *creates a feature* and *commits connectivity*,
  so the handler independently requires `features:write` (403 otherwise) and,
  when a `sectionalizer` is given, `connectivity:commit` — the same "gate on
  what the action actually does" discipline `splitActions` uses, so a
  diagrams-only user can't escalate into the feature/topology write surfaces
  through this one route.
- **The cut.** Load the placement (scoped by its diagram); confirm its class is
  drawn as a line (`DiagramGeom() == "line"`) and it actually carries a
  hand-drawn `path` (cases 1 and 3 above have none → 400). Cut the path at the
  point on it nearest `at` using `splitPolyline` — the plain planar port of the
  frontend's `web/src/lib/geometry.ts` `splitCoordinates` (diagram coordinates
  are abstract, so there's no projection to reconcile and no PostGIS to defer
  to). A cut within `sectionalizeEndpointEpsilon` of either end is rejected, the
  same guard `SplitLine` enforces in fraction-of-length terms.
- **The two halves.** The original placement is shortened to the near-half path
  (`UPDATE ... SET path`). The far half becomes a **new `MV_BUSBAR` feature**
  (via the shared `crud.CreateFeature`, so it validates and gets a territory
  exactly like any create) plus **its own placement** carrying the far-half
  path, anchored at that path's midpoint. The new feature's map geometry is the
  inherited offset copy (or none), per the locked decision.
- **Dividing connectivity.** For every `runtime.feature_connectivity` row where
  the original bus is an endpoint, find the *other* feature's placement on this
  diagram, project its anchor onto the original full bar (`locateFraction`), and
  if it lands past the cut, **re-home that connection to the far-half feature
  id** (flip `from_feature_id`/`to_feature_id`; class and terminal unchanged —
  both halves are the same bus class, and the new id is brand-new so it can
  never collide with the `(from_feature_id, from_terminal_id, to_feature_id,
  to_terminal_id)` unique key). An off-diagram other-end has no anchor, so it
  stays with the near half.
- **Optional sectionalizer bridge.** If a `sectionalizer` (a placed bus-tie
  switch) is given, best-effort `connectivity.EstablishConnection` wires it to
  **both** halves — `near_terminal`/`far_terminal` pin the switch's own
  terminals facing each bus so the two halves don't both land on the same switch
  terminal. A class with no matching bus↔switch `network_rule` can't be wired;
  that's collected as a **warning**, never an error, so a valid geometric cut
  isn't undone by a missing rule. (These failures are rule/validation errors
  that return before any SQL runs, so they don't poison the transaction.)
- **Response:** `{ near_bus, far_bus, far_placement_id, connections_moved,
  warnings? }` — the original (unchanged id) as near, the freshly minted far,
  its new placement id, the re-home count, and any soft warnings.

### Known wart (acceptable v1)

`crud.CreateFeature` returns `crud`'s own validation errors, and diagram's
`isValidationError` is a concrete type-assert against `*diagram.validationError`
(crud exports only `IsNotFoundError`, no validation predicate). So a bad
`new_bus` payload (e.g. an invalid subtype) currently surfaces as **500 rather
than 400** — consistent with the package's existing `resolveFeature`, which
likewise only translates crud's not-found. Not a correctness bug; the cut either
commits wholly or rolls back wholly.

### Verification owed (Slice 3)

- `go build ./... && go vet ./... && gofmt -l .` clean (new `diagram` file +
  route registration; new imports `auth`, `connectivity` — no cycle: neither
  imports `diagram`, checked statically).
- Curl against a real Postgres/API on a seeded one-line diagram with a drawn
  `MV_BUSBAR` bar carrying connections along it: `POST
  .../placements/{placementID}/sectionalize` with an interior `at` → confirm two
  bus features + two placements, the original path shortened, the far-half path
  present, and `connections_moved` matching the connections whose other end sits
  past the cut; connections before the cut and off-diagram ones stay on the
  original. Cut at a bar end → 400. Sectionalize a point/pathless placement →
  400. A user with `diagrams:write` but not `features:write` → 403. Supply a
  `sectionalizer` without `connectivity:commit` → 403; with it and a valid
  bus↔switch rule → both halves wired; with no matching rule → cut still
  succeeds, `warnings` populated. Confirm the far half inherits an offset map
  point when the original has one, and is geometry-less when it doesn't.

## Slice 3b — diagram sectionalize UI (not started)

The canvas gesture that drives the Slice 3 endpoint: a "Sectionalize" tool that,
on a drawn busbar, takes a click along the bar as `at`, optionally lets the user
drop/pick the bus-tie switch as the `sectionalizer`, seeds the `new_bus` far
half (like the map split's create-form seed), and POSTs. Plus surfacing the
`split` assistant suggestion on the diagram surface the way Slice 2 did on the
map. Deferred from Slice 3 so the backend could land and be verified first.
