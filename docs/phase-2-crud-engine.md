# Phase 2 (slices 1-2) — Generic CRUD engine + publish/unpublish

**Goal:** one set of HTTP handlers that reads a feature class's compiled
metadata and does validated create/read/update against its physical
table, plus the draft/published lifecycle — no feature-class-specific
code anywhere.
**Status:** both slices done and verified against a live server and a
real feature class (`POLE`), including every validation path and the
publish completeness gate. See "Try it yourself" below for the exact
commands to reproduce all of it.

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
  - `handler.go` — `chi.Router` mounted at `/features`:
    `GET/POST /{code}`, `GET/PATCH /{code}/{id}`,
    `POST /{code}/{id}/publish`, `POST /{code}/{id}/unpublish`.
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

## Two real bugs found and fixed along the way

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

Both are documented as comments at the exact line that would otherwise
look right and be wrong again.

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

## What's still open

- **No delete.**
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
condensed into copy-pasteable commands. Two things worth knowing before
you start:

- **On Windows, use `curl.exe`, not bare `curl`.** PowerShell aliases
  `curl` to `Invoke-WebRequest`, which doesn't understand `-X`/`-d` the
  same way real curl does — `curl.exe` forces the actual binary Windows
  10+ ships. Quote JSON bodies with **single quotes** in PowerShell; it
  passes the string through literally, internal double quotes and all.
- If `task`/`make` don't work in your environment, substitute the direct
  `docker compose exec ...` commands from
  [`docs/commands-cheatsheet.md`](./commands-cheatsheet.md) for
  `task migrate`/`task seed-pole`/`task compile`.

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
```
curl.exe -s -X POST http://localhost:3704/features/POLE -H "Content-Type: application/json" -d '{"subtype":1,"geometry":{"type":"Point","coordinates":[-0.1276,51.5072]},"attributes":{"pole_material":"WOOD","pole_use":"DISTRIBUTION","height_class":"CLASS_1","condition_rating":"GOOD"}}'
```
Copy the `id` from the response into `$ID` for the rest of this walkthrough:
```
$ID = "paste-the-id-here"
```

### 3. Read it back

```
curl.exe -s http://localhost:3704/features/POLE/$ID
curl.exe -s "http://localhost:3704/features/POLE?limit=5"
```

### 4. Update one attribute

```
curl.exe -s -X PATCH http://localhost:3704/features/POLE/$ID -H "Content-Type: application/json" -d '{"attributes":{"condition_rating":"FAIR"}}'
```

### 5. Publish it

```
curl.exe -s -X POST http://localhost:3704/features/POLE/$ID/publish
```
Calling this again returns the same result unchanged (idempotent) —
compare the `updated_at` field before and after to confirm nothing
actually re-ran.

### 6. See the completeness gate reject an incomplete row

The seed data's required fields are all `NOT NULL` at the column level
too, so reaching this through the API alone needs one field temporarily
relaxed — this mirrors exactly what was done to validate it:
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
Back in PowerShell:
```
curl.exe -s -X PATCH http://localhost:3704/features/POLE/$ID -H "Content-Type: application/json" -d '{"attributes":{"condition_rating":null}}'
curl.exe -s -X POST http://localhost:3704/features/POLE/$ID/publish
```
The publish call returns `400` with
`{"error":"field \"condition_rating\" is required before publishing"}`.
Fix it and publish succeeds:
```
curl.exe -s -X PATCH http://localhost:3704/features/POLE/$ID -H "Content-Type: application/json" -d '{"attributes":{"condition_rating":"POOR"}}'
curl.exe -s -X POST http://localhost:3704/features/POLE/$ID/publish
```

### 7. Unpublish

```
curl.exe -s -X POST http://localhost:3704/features/POLE/$ID/unpublish
```

### 8. Inspect the row directly with SQL

`task psql`, then:
```sql
SELECT id, subtype, status, pole_material, condition_rating, created_at, updated_at
  FROM gis.pole
  WHERE id = 'paste-the-id-here';

-- geometry as GeoJSON, same shape the API returns
SELECT ST_AsGeoJSON(geom) FROM gis.pole WHERE id = 'paste-the-id-here';

-- every metadata_versions row this feature class has ever been compiled under
SELECT version_no, published_at, published_by, status
  FROM metadata.metadata_versions
  WHERE id IN (
    SELECT metadata_version_id FROM metadata.feature_classes WHERE code = 'POLE'
  );
```

### 9. A few of the validation failures, for comparison

```
# bad subtype
curl.exe -s -X POST http://localhost:3704/features/POLE -H "Content-Type: application/json" -d '{"subtype":99,"geometry":{"type":"Point","coordinates":[0,0]},"attributes":{"pole_material":"WOOD","pole_use":"DISTRIBUTION","height_class":"CLASS_1","condition_rating":"GOOD"}}'

# missing required field
curl.exe -s -X POST http://localhost:3704/features/POLE -H "Content-Type: application/json" -d '{"subtype":1,"geometry":{"type":"Point","coordinates":[0,0]},"attributes":{"pole_material":"WOOD"}}'

# bad domain value
curl.exe -s -X POST http://localhost:3704/features/POLE -H "Content-Type: application/json" -d '{"subtype":1,"geometry":{"type":"Point","coordinates":[0,0]},"attributes":{"pole_material":"PLASTIC","pole_use":"DISTRIBUTION","height_class":"CLASS_1","condition_rating":"GOOD"}}'
```
Each returns `400` with a message naming the exact field and problem.
