# Phase 2 (slices 1-3) — Generic CRUD engine + publish/unpublish + delete

**Goal:** one set of HTTP handlers that reads a feature class's compiled
metadata and does validated create/read/update against its physical
table, plus the draft/published lifecycle and a delete/restore/purge
lifecycle — no feature-class-specific code anywhere.
**Status:** all three slices done and verified against a live server and
a real feature class (`POLE`), including every validation path, the
publish completeness gate, and the full soft-delete/restore/purge
sequence. See "Try it yourself" below for the exact commands to
reproduce all of it.

## What was built

- **`api/internal/metadata`** — extracted from the compiler (which now
  uses it too) rather than duplicated: `FeatureClass`, `Field`,
  `Subtype`, `SubtypeFieldRule`, `Domain`, `DomainValue` models, and a
  `Load(ctx, db, code)` that bundles everything a consumer needs about
  one feature class in a handful of queries. Adding `SubtypeFieldRule`
  here (the compiler never needed it) is what made required-field
  validation possible without a second, parallel loader.
- **`api/internal/crud`**:
  - `validate.go` — subtype, geometry (GeoJSON type match), unknown/
    computed-field rejection, `visible_on_create`/`visible_on_update`,
    `editable_after_publish` (locks a field once a row's `status` is
    `published`), required-by-subtype (with `default_override`
    fallback), and per-`data_type` value coercion + coded-domain
    membership checks. Two distinct error types (`validationError` →
    400, `notFoundError` → 404) so the HTTP layer never has to guess a
    status code from a message string.
  - `sql.go` — dynamic, parameterized `INSERT`/`UPDATE`/`SELECT` against
    whatever table the compiler generated. Geometry in via
    `ST_SetSRID(ST_GeomFromGeoJSON(?), srid)`, geometry out via
    `ST_AsGeoJSON(geom)::text`, generic row scanning since the column set
    isn't known until runtime.
  - `publish.go` — `publishFeature`/`unpublishFeature`: idempotent status
    transitions (`draft` ⇄ `published`), with `publish` gated on a
    completeness check against the row's *current* values, not just what
    was true at create time.
  - `delete.go` — `softDeleteFeature`/`restoreFeature`/`hardDeleteFeature`:
    a `deleted_at` timestamp, orthogonal to `status`, plus a hard
    `DELETE FROM` gated on the row already being soft-deleted first.
  - `handler.go` — `chi.Router` mounted at `/features`:
    `GET/POST /{code}`, `GET/PATCH/DELETE /{code}/{id}`,
    `POST /{code}/{id}/publish`, `POST /{code}/{id}/unpublish`,
    `POST /{code}/{id}/restore`, `DELETE /{code}/{id}/purge`.
- **`api/internal/compiler`** — `deleted_at timestamptz` added to every
  compiled table's system columns. Feature classes compiled before this
  slice existed get it backfilled via `ALTER TABLE ADD COLUMN` the next
  time they're compiled, the same incremental path fields already use —
  verified live against a table compiled under the old system columns
  (see "How this was validated").
- Wired into `cmd/server/main.go` as `r.Mount("/features", crud.Routes(db))`.

### Why publish needs its own completeness check

`is_required` (via `subtype_field_rules`) and `nullable` (on `fields`)
are different questions. A field can be required for one subtype while
its column stays nullable — so a different subtype that doesn't require
it can still omit it. That means a required field going `NULL` via a
later `PATCH` isn't necessarily caught by a database `NOT NULL`
constraint; create-time validation alone doesn't guarantee it stays
satisfied. `publishFeature` re-checks every currently-required field
against the row's live values before allowing the transition — this is
the point where "is this feature actually complete" gets enforced, not
create.

### Why delete is two operations, not one

`DELETE /{code}/{id}` only ever soft-deletes: it sets `deleted_at`,
nothing else, and it's always reversible via `restore`. That's
deliberate — a soft delete is safe to call from anywhere (a published row
doesn't need to be unpublished first, same as unpublish needing no
completeness gate: deleted-ness and draft/published are orthogonal), so
there's no reason to make a client think twice before calling it.

Permanently removing a row is a different kind of operation and gets a
different route (`DELETE /{code}/{id}/purge`), gated on the row already
being soft-deleted. That gate means a single wrong call — a typo'd id, a
retried request, a client bug — can flip `deleted_at`, but it can never
purge a row that was still live a moment ago; there's always a reversible
soft-delete step in between. `GET`, `PATCH`, `publish`, and `unpublish`
all reject a soft-deleted row (restore it first), so a row can't drift
into an inconsistent state while it's sitting in the deleted-but-not-
purged state either.

## How this was validated

Ran the actual compiled server (`go run ./cmd/server`) against a real
Postgres, with `POLE` seeded and compiled exactly as in Phase 1, and hit
it with real HTTP requests:

- **Create** — valid subtype + GeoJSON point + attributes → `201`, real
  UUID, geometry round-tripped correctly.
- **Get / List** — both return the same envelope shape as create.
- **Partial update** — changed one attribute, confirmed only that column
  changed and `updated_at` advanced.
- **Seven negative cases**, each the specific validation path it's meant
  to exercise: bad subtype code, missing required field, unknown field
  name, invalid coded-domain value, wrong GeoJSON geometry type,
  nonexistent feature class (404), nonexistent feature id (404). All
  seven returned the right status code and a specific, correct message
  — not a generic "bad request."
- **`editable_after_publish` lock** — flipped one field's metadata flag
  and a real row's `status` to `published` via `psql`, then confirmed the
  API rejects a change to the locked field while a different field on the
  same row still updates fine. This is the one path that depends on live
  row state fetched mid-request, not just static metadata, so it got its
  own explicit test.
- **Publish / unpublish idempotency** — called `publish` twice in a row
  and confirmed the second call is a true no-op (identical `updated_at`
  timestamp, not just identical status), then the same for `unpublish`.
- **The completeness gate, exercised for real, not just read as
  plausible code.** The seed data's required fields all happen to be
  `NOT NULL` at the column level too, so the gate can't normally be
  reached through the API alone — deliberately relaxed one field
  (`condition_rating`) to nullable at both the metadata and physical
  level via `psql`, created a feature with it set, `PATCH`ed it to
  `null` (now legal), confirmed `publish` rejects it with `field
  "condition_rating" is required before publishing`, then fixed the
  value and confirmed `publish` succeeds.
- **`deleted_at` backfill onto a pre-existing table** — recompiled a
  `POLE` table that had been compiled *before* this slice existed (no
  `deleted_at` column), confirmed the compiler's report listed it under
  `columns added` and that `\d gis.pole` showed the new nullable column
  afterward, with the row's existing data untouched.
- **The full delete/restore/purge sequence against a live, published
  row**, in order: purge attempted before any soft delete → rejected
  (`400`, "must be soft-deleted before it can be purged"); soft delete →
  `200`, `deleted_at` set, `status` unchanged (`published`); soft delete
  again → idempotent, identical `deleted_at`, confirming no re-write;
  `list` → row excluded; `get` by id → row still returned, `deleted_at`
  visible; `PATCH`/`publish`/`unpublish` on the deleted row → all three
  rejected with a "restore it first" message; `restore` → `200`,
  `deleted_at` back to `null`, `status` still `published` throughout (it
  was never unpublished); `list` → row reappears; `PATCH` → succeeds
  again now that it's restored; soft delete once more, then `purge` →
  `204`; `get` on the purged id → `404`; `purge` again on the same id →
  `404`, not a crash; confirmed via direct SQL that the row is actually
  gone from `gis.pole`, not just hidden.

## Three real bugs found and fixed along the way

1. **`bun.DB.QueryContext` formats its own placeholders client-side**
   (`db.gen.FormatQuery`, using `?` in appearance order — bun's own
   convention) **before the query ever reaches the driver.** The first
   version of this code hand-built raw Postgres `$1`/`$2` syntax, which
   looked completely correct and even matched what showed up in
   Postgres's own error log — but bun doesn't recognize `$N`, so it
   passed zero args downstream. pgdriver (which only speaks the simple
   query protocol, confirmed by reading its `formatQuery`) then sent the
   literal `$1` text as part of the query, and Postgres correctly
   rejected it: `there is no parameter $1`. Fixed by switching every
   hand-built query in `sql.go` to `?` placeholders.
2. **pgdriver returns some column types as `[]byte`** when scanned
   generically into `any` (confirmed for `uuid`; `ST_AsGeoJSON(...)::text`
   came back as a proper Go `string`, so this isn't universal across
   types). `encoding/json` has no way to know a bare `[]byte` is text, so
   it silently base64-encoded every id in API responses. Fixed by
   normalizing any scanned `[]byte` to `string` once, in `scanRows`,
   rather than at every call site that touches a value.
3. **The completeness gate only ever ran at the draft→published
   transition, not on every write to an already-published row** —
   found by manually reproducing the exact sequence a user hit: publish
   a complete row, `PATCH` a required-but-still-nullable field to
   `null` (legal, since nothing stopped it), then call `publish` again
   expecting a rejection. It came back `200` with the stale
   `"status":"published"` unchanged, because `publishFeature`'s
   idempotent "already published → return as-is" check runs *before*
   the completeness check, and a `publish` call on an already-published
   row never reaches it. Fixed by adding `validateUpdateCompleteness`,
   called from `updateHandler` whenever the row being patched is
   currently `published`: it merges the proposed change into the row's
   existing attributes and runs the same required-field check against
   that merged view, rejecting the *update* itself before it can ever
   leave a published row incomplete. The idempotent short-circuit in
   `publishFeature` is only safe because of this — without it, that
   short-circuit is exactly what let the gap through.

All three are documented as comments at the exact line that would
otherwise look right and be wrong again.

## Decisions made

- **Two error types, not one** — `validationError` (400) and
  `notFoundError` (404) are distinct Go types so `writeError` maps status
  codes by type, never by pattern-matching a message string.
- **Geometry is GeoJSON in, GeoJSON out** — the request/response bodies
  never see WKT or raw PostGIS text.
- **Coded-domain values are checked before the write**, not left to the
  database's own `CHECK` constraint — same information, but a clean
  `field "pole_material" value "PLASTIC" is not a valid POLE_MATERIAL
  value` beats a raw Postgres constraint-violation message.
- **A required field with a `default_override` is satisfied by that
  default**, not by demanding the client send it explicitly — matches
  `subtype_field_rules.default_override`'s documented purpose.
- **Update never touches `subtype` or `geometry`** — changing a feature's
  subtype after creation raises "does it still satisfy the new subtype's
  required fields" questions this slice doesn't answer. Deliberately out
  of scope, not an oversight.
- **Publish and unpublish are both idempotent** — calling either on a row
  already in that state just returns it, no error. Simpler for clients
  that retry, and there's no meaningful "already published" failure mode
  worth surfacing.
- **Unpublish has no gate** — reverting to draft can't violate a
  completeness rule that only applies to published rows.
- **`deleted_at`, not a third `status` value** — a row can be deleted
  regardless of `draft`/`published`, so folding deletion into `status`
  would mean either losing the draft/published information on delete or
  tracking two independent pieces of state in one column. A separate
  nullable timestamp keeps them orthogonal and makes "was this deleted,
  and when" a plain `IS NOT NULL` / value check instead of a third string
  every existing `status == 'published'` comparison would need to learn
  to ignore.
- **Delete and restore are both idempotent, purge is not** — same
  reasoning as publish/unpublish: delete/restore have no meaningful
  "wrong state" failure worth surfacing to a retrying client, but purge
  redefines "already done" as "the row is just gone" — a second purge
  call correctly gets `404`, not a silent `204`.
- **List excludes soft-deleted rows; get does not** — list is the normal
  browsing view, where a deleted row reappearing would look like the
  delete didn't work. A direct get-by-id is usually a follow-up to a
  specific id a client already has (confirming a delete, deciding whether
  to restore), so it keeps returning the row with `deleted_at` visible
  rather than turning a real row into a `404`.

## What's still open

- **No filtering or sorting on list** — just a bounded, offset-paginated
  scan ordered by `created_at, id`.
- **The connectivity/relationship establish flow, attachments, and
  diagrams** — the rest of Phase 2 per the original phase plan, all
  still ahead.
- **`numeric` fields round-trip through Go `float64`** — fine for now,
  but not exact-decimal-safe; worth revisiting if a field ever needs
  real currency-grade precision.

## Try it yourself

Everything below is the exact sequence used to validate this phase,
condensed into copy-pasteable commands.

**On Windows, use `Invoke-RestMethod`, not `curl`/`curl.exe`, for any
call that sends a JSON body.** PowerShell has to flatten arguments back
into one command-line string to hand off to a native binary like
`curl.exe`, and its quoting rules for embedded double quotes are
unreliable enough that a perfectly-formed JSON string routinely arrives
corrupted — that's the exact `invalid character ... looking for
beginning of object key string` failure mode. `Invoke-RestMethod` takes
the body as a real PowerShell string parameter, no command-line
reconstruction involved, so this whole class of bug doesn't apply.
GET-only calls (no body) are fine with either. If `task`/`make` don't
work in your environment, substitute the direct `docker compose exec
...` commands from
[`docs/commands-cheatsheet.md`](./commands-cheatsheet.md) for
`task migrate`/`task seed-pole`/`task compile`.

Two easy-to-miss things about `Invoke-RestMethod` + `ConvertTo-Json`:
- **Always pass `-Depth`** — `ConvertTo-Json`'s default depth is 2, and
  it silently truncates anything nested deeper than that instead of
  erroring. The bodies below nest three levels deep
  (`attributes`/`geometry.coordinates`), so `-Depth 5` is used
  throughout, with room to spare.
- **`Invoke-RestMethod` throws on non-2xx responses** instead of just
  returning the body — the validation-failure examples in step 9 wrap
  the call in `try`/`catch` and read `$_.ErrorDetails.Message` to see
  the error JSON.

### 1. Set up and compile

```
task infra-up
task migrate
task seed-pole
task compile CODE=POLE
```

### 2. Start the API and create a feature

```
task api-dev
```
From a second terminal:
```powershell
$body = @{
  subtype = 1
  geometry = @{ type = "Point"; coordinates = @(-0.1276, 51.5072) }
  attributes = @{
    pole_material    = "WOOD"
    pole_use         = "DISTRIBUTION"
    height_class     = "CLASS_1"
    condition_rating = "GOOD"
  }
} | ConvertTo-Json -Depth 5

$created = Invoke-RestMethod -Method Post -Uri "http://localhost:3704/features/POLE" -ContentType "application/json" -Body $body
$created
$ID = $created.id
```

### 3. Read it back

```powershell
Invoke-RestMethod -Uri "http://localhost:3704/features/POLE/$ID"
Invoke-RestMethod -Uri "http://localhost:3704/features/POLE?limit=5"
```
The `list` response's `features` property prints truncated
(`{@{attributes=; created_at=...`) in the PowerShell console by
default — that's just its table formatter summarizing a nested array,
not missing data. Pipe to `ConvertTo-Json -Depth 5` or index into it
(`(Invoke-RestMethod ...).features[0]`) to see it in full.

### 4. Update one attribute

```powershell
$body = @{ attributes = @{ condition_rating = "FAIR" } } | ConvertTo-Json -Depth 5
Invoke-RestMethod -Method Patch -Uri "http://localhost:3704/features/POLE/$ID" -ContentType "application/json" -Body $body
```

### 5. Publish it

```powershell
Invoke-RestMethod -Method Post -Uri "http://localhost:3704/features/POLE/$ID/publish"
```
Calling this again returns the same result unchanged (idempotent) —
compare the `updated_at` field before and after to confirm nothing
actually re-ran.

### 6. See the completeness gate reject an incomplete row

Your `$ID` is already `published` from step 5. The seed data's required
fields are all `NOT NULL` at the column level too, so reaching the gate
through the API alone needs one field temporarily relaxed — this
mirrors exactly what was done to validate it:
```
task psql
```
then, inside the `psql` shell:
```sql
UPDATE metadata.fields SET nullable = true
  WHERE feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'POLE')
    AND name = 'condition_rating';
ALTER TABLE gis.pole ALTER COLUMN condition_rating DROP NOT NULL;
\q
```
Back in PowerShell, try to null out that now-nullable-but-still-required
field on the already-`published` row:
```powershell
$body = @{ attributes = @{ condition_rating = $null } } | ConvertTo-Json -Depth 5
try {
  Invoke-RestMethod -Method Patch -Uri "http://localhost:3704/features/POLE/$ID" -ContentType "application/json" -Body $body
} catch {
  $_.ErrorDetails.Message
}
```
This is rejected immediately, at the `PATCH` itself — `400` with
`{"error":"field \"condition_rating\" is required while the feature is
published"}`. That's deliberate: the gate runs on every write to an
already-published row, not just at the moment it first becomes
published, precisely so a required field can't quietly go missing
afterward. (The original draft→published gate still exists too — try
creating a *new* `POLE` feature omitting `condition_rating` entirely
and you'll hit the create-time required-field check from step 9 instead,
long before publish ever comes into it.)

Setting the field to a real value instead succeeds, same as any other
update to a published row — `$ID` was never unpublished, so there's no
publish call needed here, just the PATCH going through cleanly this time:
```powershell
$body = @{ attributes = @{ condition_rating = "POOR" } } | ConvertTo-Json -Depth 5
Invoke-RestMethod -Method Patch -Uri "http://localhost:3704/features/POLE/$ID" -ContentType "application/json" -Body $body
```

### 7. Unpublish

```powershell
Invoke-RestMethod -Method Post -Uri "http://localhost:3704/features/POLE/$ID/unpublish"
```

### 8. Inspect the row directly with SQL

`psql` runs in its own shell, separate from PowerShell, so it can't see
`$ID`. Print it first (just type `$ID` in PowerShell and hit enter) and
copy the UUID it prints — `REPLACE_WITH_YOUR_ID` below is a literal
placeholder, not something to paste as-is.

`task psql`, then:
```sql
SELECT id, subtype, status, pole_material, condition_rating, created_at, updated_at
  FROM gis.pole
  WHERE id = 'REPLACE_WITH_YOUR_ID';

-- geometry as GeoJSON, same shape the API returns
SELECT ST_AsGeoJSON(geom) FROM gis.pole WHERE id = 'REPLACE_WITH_YOUR_ID';

-- every metadata_versions row this feature class has ever been compiled under
SELECT version_no, published_at, published_by, status
  FROM metadata.metadata_versions
  WHERE id IN (
    SELECT metadata_version_id FROM metadata.feature_classes WHERE code = 'POLE'
  );
```

### 9. A few of the validation failures, for comparison

```powershell
# bad subtype
$body = @{ subtype = 99; geometry = @{ type = "Point"; coordinates = @(0,0) }; attributes = @{ pole_material = "WOOD"; pole_use = "DISTRIBUTION"; height_class = "CLASS_1"; condition_rating = "GOOD" } } | ConvertTo-Json -Depth 5
try { Invoke-RestMethod -Method Post -Uri "http://localhost:3704/features/POLE" -ContentType "application/json" -Body $body }
catch { $_.ErrorDetails.Message }

# missing required field
$body = @{ subtype = 1; geometry = @{ type = "Point"; coordinates = @(0,0) }; attributes = @{ pole_material = "WOOD" } } | ConvertTo-Json -Depth 5
try { Invoke-RestMethod -Method Post -Uri "http://localhost:3704/features/POLE" -ContentType "application/json" -Body $body }
catch { $_.ErrorDetails.Message }

# bad domain value
$body = @{ subtype = 1; geometry = @{ type = "Point"; coordinates = @(0,0) }; attributes = @{ pole_material = "PLASTIC"; pole_use = "DISTRIBUTION"; height_class = "CLASS_1"; condition_rating = "GOOD" } } | ConvertTo-Json -Depth 5
try { Invoke-RestMethod -Method Post -Uri "http://localhost:3704/features/POLE" -ContentType "application/json" -Body $body }
catch { $_.ErrorDetails.Message }
```
Each returns `400` with a message naming the exact field and problem.

### 10. Delete, restore, and purge

Last on purpose — `purge` at the end of this step permanently removes
`$ID`, so nothing earlier in this walkthrough can depend on the row
still existing afterward.

A hard `purge` before any soft delete is rejected — there's always a
reversible step in between a live row and actually losing it:
```powershell
try {
  Invoke-RestMethod -Method Delete -Uri "http://localhost:3704/features/POLE/$ID/purge"
} catch {
  $_.ErrorDetails.Message
}
```
`400`, `{"error":"feature ... must be soft-deleted before it can be
purged"}`.

Soft delete instead — this is the normal `DELETE`, always safe, always
reversible:
```powershell
Invoke-RestMethod -Method Delete -Uri "http://localhost:3704/features/POLE/$ID"
```
`deleted_at` is now set. Confirm it disappears from `list` but a direct
`get` still returns it:
```powershell
(Invoke-RestMethod -Uri "http://localhost:3704/features/POLE?limit=50").features |
  Where-Object { $_.id -eq $ID }   # nothing
Invoke-RestMethod -Uri "http://localhost:3704/features/POLE/$ID"          # still there, deleted_at set
```
And confirm `PATCH`/`publish`/`unpublish` all refuse to touch it:
```powershell
$body = @{ attributes = @{ condition_rating = "GOOD" } } | ConvertTo-Json -Depth 5
try { Invoke-RestMethod -Method Patch -Uri "http://localhost:3704/features/POLE/$ID" -ContentType "application/json" -Body $body }
catch { $_.ErrorDetails.Message }
try { Invoke-RestMethod -Method Post -Uri "http://localhost:3704/features/POLE/$ID/publish" }
catch { $_.ErrorDetails.Message }
```
Each returns `400` with a "restore it before ..." message.

Restore, then confirm it's back to normal (still `published` the whole
time — it was never unpublished, delete/restore don't touch `status`):
```powershell
Invoke-RestMethod -Method Post -Uri "http://localhost:3704/features/POLE/$ID/restore"
Invoke-RestMethod -Uri "http://localhost:3704/features/POLE/$ID"
```

Finally, soft delete once more and purge for real:
```powershell
Invoke-RestMethod -Method Delete -Uri "http://localhost:3704/features/POLE/$ID"
Invoke-RestMethod -Method Delete -Uri "http://localhost:3704/features/POLE/$ID/purge"
try { Invoke-RestMethod -Uri "http://localhost:3704/features/POLE/$ID" }
catch { $_.ErrorDetails.Message }   # 404 -- the row is actually gone
```
