# Phase 6, slice 1 — admin metadata UI (feature class catalog + create wizard)

**Goal:** a real, in-app way to author `metadata.*` — feature classes,
subtypes, fields, terminals, domains, field groups, default symbology —
without writing SQL by hand. Every one of those tables has been
authored exclusively via `seed/*.sql`/`migrations/*.sql` since Phase 1;
this is the first write path into any of them that goes through the
API.
**Status:** backend curl-verified end to end against a real
Postgres/API stack (two independent test feature classes, one
point/no-network-role, one junction-role with terminals). Frontend
built, typechecked, linted, and built clean; browser-verified.

## Why this is its own slice, not folded into auth/authz

The ask that kicked this off was broader: "build admin page where we
will do ALL CONFIGURATIONS... FEATURE CLASSES FUNCTIONALITES, USER
ACCOUNTS, AUTHENTICATION, AUTHORIZATION etc." — one admin system,
multiple birds, one stone. Splitting it was the right call for a
concrete reason, not just "smaller is safer": metadata configuration
and user accounts/auth/authz are different problems with different
prerequisites. Metadata configuration builds directly on infrastructure
that already exists and is proven (the schema compiler, the metadata
loaders, the CRUD engine) — it's additive work on a solid foundation.
Auth/authz is genuinely greenfield: `README.md` has said "Auth and
permissions are deliberately not modeled yet" since Phase 3, there's no
session/token handling, no user table, no frontend auth state anywhere.
Building both in one pass would mean designing a real auth system under
the time pressure of also shipping a metadata UI, and would block the
(buildable-today) metadata UI on the (needs-real-design-work) auth
system. So: this slice is metadata configuration only. Auth/authz stays
a distinct, later Phase 6 effort — already tracked in `README.md`'s
Phase 6 bullet and `docs/open-items.md`'s Phase 3 table ("Azure AD SSO
+ backend auth").

## The scope-defining discovery: what the compiler actually supports

Before writing anything, the actual capabilities and limits of
`api/internal/compiler` were checked directly, not assumed:

- **Adding a field to an already-compiled feature class is real and
  already works.** `compiler.go`'s existing-table path does a genuine
  `ALTER TABLE ... ADD COLUMN` for any field present in the metadata
  but missing from the physical table — this isn't new capability this
  slice adds, it was already there, just never exercised through the
  API before.
- **Two things are permanently, structurally *not* safe with today's
  compiler:**
  - Renaming, retyping, or dropping an existing column — the
    existing-table path only ever adds, never alters or removes.
  - Adding a new subtype or domain value to an *already-compiled*
    class. `ddl.go`'s `subtype integer NOT NULL CHECK (subtype IN
    (...))` constraint, and a coded domain field's own CHECK
    constraint, are both written exactly once, at the table's initial
    `CREATE TABLE`. The existing-table ADD-COLUMN path never revisits
    either constraint — a subtype or domain value added to the
    metadata *after* first compile would be legal in `metadata.*` but
    silently rejected by the physical table's own CHECK the moment
    anyone tried to use it. (The identical domain-value gap was
    already flagged, independently, in
    `docs/phase-1-metadata-core.md`.)

That finding drew the scope line directly: **this slice only ever
performs purely-additive operations on a brand-new feature class**,
which always goes through a full, fresh `CREATE TABLE` — every one of
the gaps above only bites an *already-compiled* class, so none of them
apply here. Editing an existing class's subtypes, editing a domain
that's already referenced by a compiled field, and column
retype/rename/drop are all explicitly out of scope for this slice (see
"What's deliberately out of scope" below) — not because they're
unwanted, but because building them today would either silently
produce a broken CHECK constraint or require compiler changes that are
a distinct, later effort.

## What was built

### Backend — `api/internal/admin` (new package)

Mounted at `/admin` in `cmd/server/main.go`, mirroring every other
package's own `Routes(db)` convention:

```
GET  /admin/feature-classes                every class, any status, with subtype/field counts
POST /admin/feature-classes                create a brand-new class end to end
POST /admin/feature-classes/{code}/compile compile/recompile -- wraps compiler.Compile directly
GET  /admin/domains                        every domain with its values
POST /admin/domains                        create a domain, optionally with initial coded values
GET  /admin/field-groups                   every field group
POST /admin/field-groups                   create a field group
```

- **`metadata` package additions** (`api/internal/metadata/models.go`,
  `load.go`) — fields that were real, populated, always-there database
  columns but had no Go struct field yet, because nothing had ever
  needed to read or write them from Go: `Domain.Name`,
  `DomainValue.Label`/`SortOrder`, a new `FieldGroup` struct,
  `Field.GroupID`. Plus new unfiltered/list loaders
  (`ListAllFeatureClasses`, `ListDomains`, `ListDomainValues`,
  `ListFieldGroups`) alongside the existing status-filtered ones the
  map/schema endpoints use.
- **`errors.go`, `types.go`** — a local `validationError`/
  `notFoundError`/`apiError` pattern and every request/response DTO,
  matching `api/internal/crud`'s own conventions rather than sharing
  types across packages.
- **`validate.go`** — `codePattern` (`^[A-Z][A-Z0-9_]*$`, matching
  every hand-written seed file's `MV_CONDUCTOR`/`PROTECTIVE_RELAY`
  convention) for domain/field-group/feature-class codes,
  `fieldNamePattern` (`^[a-z][a-z0-9_]*$`, matching `device_number`/
  `voltage_class`) for field names. Full structural validation for a
  create-feature-class request, including cross-reference checks (a
  field's `required_for_subtypes` must reference a subtype code
  actually defined in the same request) and the rule that terminals
  are only meaningful for `edge`/`junction` network roles.
- **`create.go`** — `createFeatureClass` is one transaction: insert the
  class, its subtypes, its fields (resolving domain/group codes to
  ids first), its terminals, and one optional default symbology rule.
  `codeExists` is a generic pre-check (works against any model with a
  `code` column) run before every insert that could hit a UNIQUE
  constraint, so a duplicate code gets a clean 400 instead of a bare
  Postgres error surfacing through.
- **`list.go`** — `listFeatureClasses` (with a generic grouped-count
  helper for subtype/field counts per class), `listDomains`,
  `listFieldGroups`.
- **Compile stays exactly what it always was.** `compileFeatureClassHandler`
  calls `compiler.Compile` directly — there is no admin-specific
  compile logic, deliberately, so the wizard's "Compile now" step and
  the CLI (`cmd/compile`) can never drift apart.

### Frontend

- **`web/src/hooks/use-admin.ts`** (new) — `useAdminFeatureClasses`,
  `useAdminDomains`, `useAdminFieldGroups`, plain `useQuery` wrappers
  matching every other data hook's shape in this project.
- **`web/src/routes/admin-feature-classes.tsx`** (new) — every feature
  class regardless of status (unlike the map/layers panel's own
  `useFeatureClasses`, active-only), with a status badge, subtype/field
  counts, and an inline "Compile" button for any class still in
  `draft`.
- **`web/src/routes/admin-domains.tsx`**, **`admin-field-groups.tsx`**
  (new) — a flat list plus a one-shot inline create form each. Domains
  only ever build coded values here (a range domain's bounds aren't
  modeled by `api/internal/admin` yet).
- **`web/src/routes/admin-feature-class-new.tsx`** (new, the wizard) —
  basic info (code/name/geometry type/SRID/network role/diagram
  container flag), a dynamic subtypes editor, a dynamic fields editor
  (data type, optional domain, optional group, per-subtype required
  checkboxes), a terminals editor that only renders when the network
  role is `edge`/`junction`, and an optional default symbology section
  (icon/color/width, width only for line geometry). Submitting shows a
  success screen with "Leave as draft" / "Compile now" — draft-then
  -compile-later and compile-immediately are both real, supported
  paths, not just a create-then-redirect.
- **`web/src/components/layout/app-sidebar.tsx`** — a new "Admin"
  `SidebarGroup` (Feature classes / Domains / Field groups) between the
  existing "Data" group and the pinned-to-bottom Settings entry.
- **`web/src/app.tsx`** — four new routes under the existing
  `AppLayout` wrapper: `/admin/feature-classes`,
  `/admin/feature-classes/new`, `/admin/domains`, `/admin/field-groups`.

## What's deliberately out of scope for this slice

- **Editing an already-compiled class's subtypes, or a domain's
  values once referenced by a compiled field.** The CHECK-constraint
  gap described above — building this safely needs compiler changes
  (an `ALTER TABLE ... DROP CONSTRAINT` / re-`ADD CONSTRAINT` path, or
  equivalent) that are a distinct, later effort, not a UI-only
  addition.
- **Column type change, rename, or delete on an existing field.**
  Same reasoning — the compiler's existing-table path has never
  supported this, on any class, at any point in this project.
- **Editing a feature class's own basic info after creation** (name,
  geometry type, network role, etc.) — this slice is create-only;
  there's no `PATCH /admin/feature-classes/{code}` yet.
- **Range domains** — `createDomainRequest` and the domains page only
  ever build a domain's `values` list (coded domains); a range
  domain's own bounds aren't in `api/internal/admin`'s request shape
  yet.
- **Deleting a feature class, domain, or field group.**
- **User accounts, authentication, authorization** — see "Why this is
  its own slice" above; tracked separately.

## How this was validated

Backend, driven against a real Postgres + Go API, not assumed from the
code:

1. `go build ./...`, `go vet ./...`, `gofmt -l .` all clean.
2. Created a `LAMP_TYPE` domain (3 coded values) via `POST /admin/domains`.
3. Created `STREET_LIGHT` (`geometry_type = point`, `srid = 4326`,
   `network_role = none`) via `POST /admin/feature-classes` — one
   subtype, two fields (one plain, one domain-linked to `LAMP_TYPE`),
   one default map symbology rule.
4. Compiled it via `POST /admin/feature-classes/STREET_LIGHT/compile`
   — confirmed `gis.street_light` exists with the correct columns and
   CHECK constraints.
5. Created a real feature through the pre-existing
   `POST /features/STREET_LIGHT` CRUD endpoint (no changes needed there
   at all — a class created through `/admin` is indistinguishable from
   one created via a migration once compiled). Confirmed an invalid
   `lamp_type` value is rejected with a 400 by the domain's own CHECK
   constraint, and that the schema endpoint resolves the seeded
   symbology rule correctly.
6. Created `SEC_FUSE` (`network_role = junction`, two terminals) to
   exercise the terminals path specifically, and confirmed the
   "terminals only valid for edge/junction" validation rejects a
   terminal on a `network_role = none` request.
7. Confirmed `codeExists` pre-checks produce a clean 400 ("a feature
   class with this code already exists") rather than a bare Postgres
   unique-violation error, for a duplicate `STREET_LIGHT` create.

Both `STREET_LIGHT` and `SEC_FUSE` (and `LAMP_TYPE`) were deliberately
left in the database as real, valid additions.

Frontend: `npx tsc --noEmit`, `npm run lint` (oxlint), and `npm run
build` all finish clean. Browser-verified (Playwright): the sidebar's
new Admin group and its three links; the feature classes list showing
real existing classes with correct status/counts and a working
"Compile" action; the domains and field groups pages' list + inline
create flows; and the full wizard end to end — filling in basic info,
a subtype, a field, submitting, landing on the success screen, clicking
"Compile now," and confirming the new class appears active with the
right counts back on the list page.

## Try it yourself

Against a running API (`DATABASE_URL=... go run ./cmd/server`) and,
for the frontend part, `npm run dev` in `web/`:

```bash
# Create a domain with two coded values
curl -X POST http://localhost:3704/admin/domains \
  -H "Content-Type: application/json" \
  -d '{"code": "LAMP_TYPE", "name": "Street light lamp type", "domain_type": "coded",
       "values": [{"code": "LED", "label": "LED", "sort_order": 1},
                  {"code": "SODIUM", "label": "High-pressure sodium", "sort_order": 2}]}'

# Create a brand-new feature class in one call -- subtypes, fields, symbology
curl -X POST http://localhost:3704/admin/feature-classes \
  -H "Content-Type: application/json" \
  -d '{"code": "STREET_LIGHT", "name": "Street Light",
       "geometry_type": "point", "srid": 4326, "network_role": "none",
       "subtypes": [{"code": 1, "name": "standard"}],
       "fields": [{"name": "lamp_type", "data_type": "text", "domain_code": "LAMP_TYPE", "nullable": true}],
       "symbology": {"icon_ref": "circle", "color": "#f59e0b"}}'

# Compile it -- creates the real gis.street_light table
curl -X POST http://localhost:3704/admin/feature-classes/STREET_LIGHT/compile

# List every class, any status
curl http://localhost:3704/admin/feature-classes
```

Then in the browser: open the sidebar's "Admin" group, visit "Domains"
or "Field groups" to author reference data first, then "Feature
classes" → "New feature class" to build one end to end and compile it
from the success screen.

`go build ./... && go vet ./... && gofmt -l .` should all finish clean,
and so should `npx tsc --noEmit`, `npm run lint`, and `npm run build`
in `web/`.
