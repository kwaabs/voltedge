# Phase 2 (slice 1) — Generic CRUD engine

**Goal:** one set of HTTP handlers that reads a feature class's compiled
metadata and does validated create/read/update against its physical
table — no feature-class-specific code anywhere.
**Status:** done and verified against a live server and a real feature
class (`POLE`), including every validation path.

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
  - `handler.go` — `chi.Router` mounted at `/features`:
    `GET/POST /{code}`, `GET/PATCH /{code}/{id}`.
- Wired into `cmd/server/main.go` as `r.Mount("/features", crud.Routes(db))`.

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

## What's still open

- **No publish/unpublish endpoint.** Every row the CRUD engine creates
  stays `status = 'draft'` forever as far as this slice is concerned —
  the column already exists (the compiler creates it), but nothing
  flips it. This was the option deliberately not picked when Phase 2
  started; it's next.
- **No delete.**
- **No filtering or sorting on list** — just a bounded, offset-paginated
  scan ordered by `created_at, id`.
- **The connectivity/relationship establish flow, attachments, and
  diagrams** — the rest of Phase 2 per the original phase plan, all
  still ahead.
- **`numeric` fields round-trip through Go `float64`** — fine for now,
  but not exact-decimal-safe; worth revisiting if a field ever needs
  real currency-grade precision.
