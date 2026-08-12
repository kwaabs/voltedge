# Phase 2 (slice 4) — Zero-trust connectivity engine

**Goal:** the only path that turns a candidate electrical connection into
real topology, gated on `metadata.network_rules` -- zero trust: absence
of a matching rule means the connection is invalid, and a matching rule
with `is_valid = false` means the same thing for a different reason.
Never written as a side effect of digitizing or of a rule merely
matching (see docs/metadata-core.md's "Suggested vs. established"
section).
**Status:** done and verified against a live server and two real,
network-participant feature classes (`MV_CONDUCTOR`, `DIST_XFMR`),
including all three zero-trust outcomes, idempotent establish, listing
from both sides of a connection, revoke, and the guards against
connecting a deleted or nonexistent feature. See "Try it yourself" below.

## What was built

- **`migrations/0016_create_runtime_schema.sql`** — reserves a new
  `runtime` schema (generic runtime infrastructure, not compiled per
  feature class -- same family docs/metadata-core.md groups
  `feature_connectivity`/`feature_relationships`/attachments/diagrams
  under) and creates `runtime.feature_connectivity`. `from_feature_id`/
  `to_feature_id` are polymorphic (`uuid`, no FK) since the target table
  varies by feature class; `from_feature_class_id`/`to_feature_class_id`
  are real FKs into `metadata.feature_classes`, and the API is what
  enforces that a given feature id actually exists in that class's
  physical table. A `UNIQUE NULLS NOT DISTINCT` constraint on
  `(from_feature_id, from_terminal_id, to_feature_id, to_terminal_id)`
  is what makes establish idempotent.
- **`api/internal/metadata`** — `Terminal` and `NetworkRule` models
  added alongside the existing `FeatureClass`/`Field`/etc. `Load`
  now also fetches a feature class's terminals (`Loaded.Terminals`,
  empty for classes with none, e.g. `POLE`); a new
  `LoadNetworkRules(ctx, db, fromFCID, toFCID)` returns *every*
  matching-pair row, valid or not, since the connectivity engine needs
  to tell "no rule" apart from "a rule, but disabled." `Loaded` gained
  `ResolveTerminal` (subtype-scoped terminal lookup, most-specific-wins)
  and `TerminalByID`.
- **`api/internal/crud`** — `GetFeature` and `IsNotFoundError` exported
  (thin wrappers around the existing unexported functions) so
  `connectivity` can resolve a feature's subtype and `deleted_at` state
  without duplicating the physical-table lookup and generic scanning
  `crud` already has.
- **`api/internal/connectivity`** (new package):
  - `rules.go` — `matchRule`: given every network_rules row for a
    from/to feature-class pair, finds the most specific applicable
    *active* rule, or explains precisely why none applies (no match at
    all vs. a disabled match).
  - `sql.go` — `Connectivity` bun model (this table's shape is fixed and
    known at compile time, unlike `crud`'s dynamic per-feature-class
    tables, so it's a plain bun model, not hand-built SQL);
    `resolveEndpoint` (feature-class + specific-row + terminal
    resolution, with the deleted-row guard); `establishConnection`,
    `listConnections`, `revokeConnection`; batched enrichment
    (`formatConnections`) so a list response shows feature-class codes,
    terminal codes, and rule names, not bare ids.
  - `handler.go` — `chi.Router` mounted at `/connectivity`:
    `POST /` (establish), `GET /?feature_class=&feature_id=` (list),
    `DELETE /{id}` (revoke).
- Wired into `cmd/server/main.go` as
  `r.Mount("/connectivity", connectivity.Routes(db))`.
- **`seed/connectivity_example.sql`** — `MV_CONDUCTOR` (edge) and
  `DIST_XFMR` (junction, terminals T1/T2/T3), the network-participant
  sample configurations from docs/metadata-core.md, minimal to what
  exercises all three zero-trust outcomes: a valid rule into T1, a
  disabled rule into T3, and T2 left with no rule row at all.

## Why zero trust, and why two different rejections

`network_rules`' own migration comment says it plainly: "a missing row
means 'not valid' -- absence of a rule is itself the rejection, not an
exception case to special-case." `matchRule` treats that literally --
there's no fallback, no default-permit path, no "looks close enough."
If nothing in `metadata.network_rules` says two specific feature
classes/subtypes/terminals may connect, the API refuses, full stop.

The `is_valid` flag exists precisely so a rule can be *temporarily*
disabled without deleting it (its own migration comment again) -- an
architect retiring a legacy connection pattern doesn't want to lose the
row's history by deleting it outright. That's a materially different
situation from a pattern that was never legal to begin with, so
`matchRule` reports them differently: "a network rule exists ... but is
disabled" versus "no network rule permits this connection." Same
practical outcome (no connection), different diagnosis for whoever's
fixing the data.

## How this was validated

Ran the actual compiled server against a real Postgres, with
`MV_CONDUCTOR` and `DIST_XFMR` seeded and compiled, real feature rows
created for each, and hit it with real HTTP requests:

- **The valid case** — connecting the conductor to the transformer's
  `T1` terminal (a real, active rule exists) → `200`, response includes
  the matched rule's name (`MV_CONDUCTOR -> DIST_XFMR T1`) and both
  endpoints, terminal code included on the side that has one.
- **The disabled case** — same conductor, `T3` instead (a rule exists,
  `is_valid = false`) → `400`, `"a network rule exists for this
  connection but is disabled (is_valid = false) -- no electrical
  connectivity"`.
- **The absent case** — same conductor, `T2` (no rule row at all for
  that terminal) → `400`, `"no network rule permits this connection --
  zero trust default: absence of a rule means the connection is
  invalid"`. Confirmed this is a genuinely different code path from the
  disabled case, not the same message reused.
- **Idempotent establish** — called establish twice with identical
  from/to/terminal → identical `id` and `established_at` both times, not
  a duplicate row (confirmed the unique constraint's `NULLS NOT
  DISTINCT` upsert path, not just "looks idempotent from the response").
- **List from both sides** — listing by the transformer's id and by the
  conductor's id both returned the same connection, enriched with
  human-readable feature-class codes and terminal code, not raw uuids.
- **Revoke** — `204`, then a second revoke on the same id → `404` (not a
  silent success), then confirmed the connection no longer appears in
  either feature's list.
- **Guards** — connecting a nonexistent feature id → `404`; requesting
  an undefined terminal code (`T99`) → `400`; soft-deleting a feature via
  Phase 2 slice 3's delete endpoint, then trying to connect it → `400`,
  `"... is deleted -- restore it before connecting"`.
- **A real bug, found and fixed during this testing**: the first version
  of `establishConnection`'s idempotent upsert failed outright --
  `ERROR: missing FROM-clause entry for table "feature_connectivity"`.
  bun aliases an inserted model's table using its Go struct name by
  default (`Connectivity` → `"connectivity"`), not the table's own bare
  name, so the `ON CONFLICT DO UPDATE SET established_at =
  feature_connectivity.established_at` clause -- written assuming the
  table could be referenced by its own name -- pointed at an alias that
  no longer existed in the query. Fixed by pinning an explicit
  `alias:feature_connectivity` in the model's `bun.BaseModel` tag, so
  the SQL and the Go tag are guaranteed consistent rather than the SQL
  depending on correctly guessing what bun would derive from a struct
  name.

## Decisions made

- **A `runtime` schema, not `gis`** — `gis` is compiler-generated
  physical tables, one per compiled feature class; `feature_connectivity`
  is hand-written and spans two feature classes by nature, so folding it
  into `gis` would blur what "compiled by the schema compiler" means.
  Matches docs/metadata-core.md explicitly calling this out as a
  separate family of generic runtime infrastructure.
- **A plain bun model for `feature_connectivity`, raw SQL for feature
  rows** — the split isn't arbitrary: `crud`'s tables have a shape only
  known at runtime (whatever the compiler generated), so generic,
  hand-built SQL is the only option there. `feature_connectivity`'s
  shape is fixed at compile time, same category as `metadata.*`, so it
  gets the same bun-model treatment those already use.
- **Establish is idempotent, revoke is not** — same reasoning as
  publish/unpublish and delete/restore: re-establishing an identical
  connection has no meaningful failure mode worth surfacing, but a
  second revoke on an already-gone connection correctly reports `404` --
  there's nothing left to revoke, so pretending otherwise would hide a
  client bug.
- **No soft-delete/restore pair for connections** — a feature row has a
  draft/published lifecycle worth protecting with a reversible delete; a
  connection doesn't have an equivalent lifecycle of its own. It exists
  or it doesn't, so `revokeConnection` is a real, permanent removal.
- **`get` allows a deleted feature to be looked up, `connect` does not**
  — `crud`'s `GET` deliberately still returns a soft-deleted row (so a
  client can inspect it or decide to restore it). Establishing a *new*
  connection to something already marked deleted is a different
  question with an obvious wrong answer, so `resolveEndpoint` rejects it
  explicitly rather than silently allowing it because `GetFeature`
  itself doesn't complain.
- **`phase_rule` is read and stored, never evaluated** — matches
  `network_rules.phase_rule`'s own migration comment: it's free text
  today, "not yet a structured expression language." `matchRule` uses it
  only to pick which rule authorized a connection, never to compute
  whether the phase condition actually holds; that's explicitly deferred
  to Phase 4's trace engine work.

## What's still open

- **`feature_relationships`** (structural/spatial support, e.g. pole ↔
  conductor span, governed by `relationship_classes` instead of
  `network_rules`) -- same explicit-confirm discipline, no rule-based
  zero-trust gate the same way (`relationship_classes` has no `is_valid`
  flag; legality is closer to "does this pairing of feature classes
  exist as a row at all").
- **Attachments** -- RustFS-backed, genuinely different design surface
  (upload flow, size/type limits) not yet discussed in enough detail to
  build.
- **No trace engine yet** -- `feature_connectivity` is exactly what
  Phase 4's trace engine will walk edge by edge, but nothing reads it
  that way today; this slice only writes and lists it.
- **No `PATCH` for a connection** -- a connection is re-established (with
  a possibly different rule matching) or revoked, never edited in place.
  Revisit if that turns out to be too blunt in practice.

## Try it yourself

Everything below is the exact sequence used to validate this slice,
condensed into copy-pasteable commands. See
[`docs/phase-2-crud-engine.md`](./phase-2-crud-engine.md)'s "Try it
yourself" intro for the `Invoke-RestMethod`-not-`curl.exe` note and the
recompile-on-a-persistent-environment note -- both apply here too.

### 1. Set up, seed, and compile both feature classes

```
task infra-up
task migrate
task seed-connectivity
task compile CODE=MV_CONDUCTOR
task compile CODE=DIST_XFMR
task api-dev
```

### 2. Create one conductor and one transformer

From a second terminal:
```powershell
$conductorBody = @{
  subtype = 1
  geometry = @{ type = "LineString"; coordinates = @(@(-0.1276, 51.5072), @(-0.1266, 51.5062)) }
  attributes = @{ voltage_class = "11KV"; condition_rating = "GOOD" }
} | ConvertTo-Json -Depth 5
$conductor = Invoke-RestMethod -Method Post -Uri "http://localhost:3704/features/MV_CONDUCTOR" -ContentType "application/json" -Body $conductorBody
$CONDUCTOR_ID = $conductor.id

$xfmrBody = @{
  subtype = 1
  geometry = @{ type = "Point"; coordinates = @(-0.1270, 51.5067) }
  attributes = @{ primary_voltage = "11KV"; condition_rating = "GOOD" }
} | ConvertTo-Json -Depth 5
$xfmr = Invoke-RestMethod -Method Post -Uri "http://localhost:3704/features/DIST_XFMR" -ContentType "application/json" -Body $xfmrBody
$XFMR_ID = $xfmr.id
```

### 3. Establish via T1 -- the valid case

```powershell
$body = @{
  from = @{ feature_class = "MV_CONDUCTOR"; feature_id = $CONDUCTOR_ID }
  to   = @{ feature_class = "DIST_XFMR"; feature_id = $XFMR_ID; terminal = "T1" }
} | ConvertTo-Json -Depth 5
$conn = Invoke-RestMethod -Method Post -Uri "http://localhost:3704/connectivity" -ContentType "application/json" -Body $body
$conn
```
`200`, with `network_rule_name` naming the rule that authorized it.

### 4. T3 and T2 -- the two rejection cases

```powershell
# T3: a rule exists but is disabled
$body = @{
  from = @{ feature_class = "MV_CONDUCTOR"; feature_id = $CONDUCTOR_ID }
  to   = @{ feature_class = "DIST_XFMR"; feature_id = $XFMR_ID; terminal = "T3" }
} | ConvertTo-Json -Depth 5
try { Invoke-RestMethod -Method Post -Uri "http://localhost:3704/connectivity" -ContentType "application/json" -Body $body }
catch { $_.ErrorDetails.Message }

# T2: no rule row at all
$body = @{
  from = @{ feature_class = "MV_CONDUCTOR"; feature_id = $CONDUCTOR_ID }
  to   = @{ feature_class = "DIST_XFMR"; feature_id = $XFMR_ID; terminal = "T2" }
} | ConvertTo-Json -Depth 5
try { Invoke-RestMethod -Method Post -Uri "http://localhost:3704/connectivity" -ContentType "application/json" -Body $body }
catch { $_.ErrorDetails.Message }
```
Both `400`, with two different messages -- compare them side by side to
see the distinction the zero-trust matcher is making.

### 5. Idempotent re-establish

```powershell
Invoke-RestMethod -Method Post -Uri "http://localhost:3704/connectivity" -ContentType "application/json" -Body $body
```
Wait -- reuse the T1 `$body` from step 3, not the T2 one still in scope
from step 4. Re-run it and compare `id`/`established_at` against the
step-3 response: identical, confirming this was a no-op, not a second row.

### 6. List from both sides

```powershell
(Invoke-RestMethod -Uri "http://localhost:3704/connectivity?feature_class=DIST_XFMR&feature_id=$XFMR_ID").connections
(Invoke-RestMethod -Uri "http://localhost:3704/connectivity?feature_class=MV_CONDUCTOR&feature_id=$CONDUCTOR_ID").connections
```
Same connection, either way.

### 7. Revoke

```powershell
Invoke-RestMethod -Method Delete -Uri "http://localhost:3704/connectivity/$($conn.id)"
try { Invoke-RestMethod -Method Delete -Uri "http://localhost:3704/connectivity/$($conn.id)" }
catch { $_.ErrorDetails.Message }   # 404 -- already gone
```

### 8. Guards

```powershell
# nonexistent feature id
$body = @{
  from = @{ feature_class = "MV_CONDUCTOR"; feature_id = "00000000-0000-0000-0000-000000000000" }
  to   = @{ feature_class = "DIST_XFMR"; feature_id = $XFMR_ID; terminal = "T1" }
} | ConvertTo-Json -Depth 5
try { Invoke-RestMethod -Method Post -Uri "http://localhost:3704/connectivity" -ContentType "application/json" -Body $body }
catch { $_.ErrorDetails.Message }

# undefined terminal code
$body = @{
  from = @{ feature_class = "MV_CONDUCTOR"; feature_id = $CONDUCTOR_ID }
  to   = @{ feature_class = "DIST_XFMR"; feature_id = $XFMR_ID; terminal = "T99" }
} | ConvertTo-Json -Depth 5
try { Invoke-RestMethod -Method Post -Uri "http://localhost:3704/connectivity" -ContentType "application/json" -Body $body }
catch { $_.ErrorDetails.Message }

# a soft-deleted feature can't be connected
Invoke-RestMethod -Method Delete -Uri "http://localhost:3704/features/MV_CONDUCTOR/$CONDUCTOR_ID"
$body = @{
  from = @{ feature_class = "MV_CONDUCTOR"; feature_id = $CONDUCTOR_ID }
  to   = @{ feature_class = "DIST_XFMR"; feature_id = $XFMR_ID; terminal = "T1" }
} | ConvertTo-Json -Depth 5
try { Invoke-RestMethod -Method Post -Uri "http://localhost:3704/connectivity" -ContentType "application/json" -Body $body }
catch { $_.ErrorDetails.Message }
Invoke-RestMethod -Method Post -Uri "http://localhost:3704/features/MV_CONDUCTOR/$CONDUCTOR_ID/restore"   # clean up
```
