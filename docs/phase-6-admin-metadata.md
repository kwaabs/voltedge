# Phase 6, slice 1 — admin metadata UI (feature class catalog + create wizard)

> **Update:** the "editing an already-compiled class's subtypes / an
> already-referenced domain's values" gap flagged throughout this doc
> as permanently out of scope has since been closed — see "Editing an
> already-compiled class's subtypes and domain values" near the end.

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
Adding a subtype/domain value to an already-compiled class (see
"Editing an already-compiled class's subtypes and domain values"
below) is backend curl-verified; its frontend is typechecked/linted
clean but not yet browser-verified.

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
  convention) for domain and feature-class codes; `fieldNamePattern`
  (`^[a-z][a-z0-9_]*$`, matching `device_number`/`voltage_class`) for
  field names *and* field-group codes — every hand-written seed file's
  `field_groups` rows (`general`/`electrical`/`maintenance`/`asset`/...)
  are lower_snake_case, unlike domain/feature-class codes, so
  `validateCreateFieldGroup` deliberately doesn't reuse `codePattern`
  (an initial version did, which rejected every field group the
  frontend's own lower-casing Code input could ever produce — caught
  and fixed during browser verification, see "How this was validated"
  below). Full structural validation for a create-feature-class
  request, including cross-reference checks (a field's
  `required_for_subtypes` must reference a subtype code actually
  defined in the same request) and the rule that terminals are only
  meaningful for `edge`/`junction` network roles.
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

- ~~Editing an already-compiled class's subtypes, or a domain's
  values once referenced by a compiled field.~~ **Closed** — see
  "Editing an already-compiled class's subtypes and domain values"
  below. *Adding* a new subtype/value is real and safe; editing or
  removing an existing one is still out of scope, for the reason given
  there.
- **Column type change, rename, or delete on an existing field.**
  Same reasoning — the compiler's existing-table path has never
  supported this, on any class, at any point in this project.
- **Range domains** — `createDomainRequest` and the domains page only
  ever build a domain's `values` list (coded domains); a range
  domain's own bounds aren't in `api/internal/admin`'s request shape
  yet.
- **User accounts, authentication, authorization** — see "Why this is
  its own slice" above; tracked separately.

Editing a feature class's own basic info, and deleting a feature
class/domain/field group, were both originally scoped out of this
slice too — see "Edit and delete, added later" below for the safe
subset that shipped, and what's still deliberately excluded.

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
"Compile" action; the domains page's list + inline create flow; and the
full wizard end to end — filling in basic info, a subtype, a field,
submitting, landing on the success screen, clicking "Compile now," and
confirming the new class appears active with the right counts back on
the list page. This pass caught one real bug before merge: the field
groups page's Code input correctly lower-cases as typed (matching
`general`/`electrical`/... convention), but `validateCreateFieldGroup`
had reused the uppercase `codePattern`, so every field-group create
400'd — impossible to create one through the UI at all. Fixed by
switching that one validator to `fieldNamePattern`
(`api/internal/admin/validate.go`); re-verified by curl (lowercase now
accepted, uppercase now correctly rejected with the right message) and
the demo database's test debris from both the original curl pass and
this verification pass (`TEST_PLAYWRIGHT_CLASS`, `TEST_ADMIN_DOMAIN`,
stray field-group test rows) was cleaned up afterward, leaving only the
intentional `STREET_LIGHT`/`SEC_FUSE`/`LAMP_TYPE` additions.

## Edit and delete, added later

Both were originally scoped out entirely -- "create-only this slice."
Revisited to find the genuinely safe subset of each, the same way the
original slice drew its own scope line around the compiler's real
CHECK-constraint limits, rather than building either capability in full
or skipping both.

- **`PATCH /admin/feature-classes/{code}`** (`update.go`, new) --
  `name`/`is_diagram_container` are pure metadata with no physical-
  table footprint, so they're always editable regardless of status.
  `geometry_type`/`srid`/`network_role` are exactly the three columns
  `compiler/ddl.go` reads to build the physical table's own shape;
  changing any of them on an already-compiled class would leave the
  physical table and its metadata disagreeing, the same class of
  problem that kept editing an already-compiled class's subtypes/domain
  values out of scope in the first place. So those three are only
  accepted while the class is still `draft` (`PhysicalTable` is nil) --
  nothing physical exists yet to disagree with. Touching one requires
  all three together (mirroring `createFeatureClassRequest`'s own
  srid-required-unless-none rule, since SRID's validity depends on
  GeometryType).
- **`DELETE /admin/feature-classes/{code}`**, **`DELETE
  /admin/domains/{code}`**, **`DELETE /admin/field-groups/{code}`**
  (`delete.go`, new) -- each draws its own safe line. A feature class
  can only be deleted while still `draft`; subtypes/fields/
  subtype_field_rules/terminals/symbology_rules all cascade
  automatically (`ON DELETE CASCADE` from `feature_class_id`, already
  in place since migrations 0008/0009/0012). A domain or field group
  can only be deleted while no field currently references it --
  `fields.domain_id`/`group_id` have no cascade of their own by design
  (a field silently losing its domain would be a real, surprising
  data-model change), so a pre-check turns what would otherwise be a
  raw foreign-key-violation 500 into a clear 400 naming the actual
  reason, same discipline `codeExists` (`create.go`) already applies on
  the insert side.
- **Frontend** -- `admin-feature-classes.tsx` gained inline Edit/Delete
  icon buttons next to Compile, shown only for a draft row (a compiled
  row shows none of the three); Edit expands an inline form pre-filled
  with the row's current values, reusing the wizard's own basic-info
  field markup. `admin-domains.tsx`/`admin-field-groups.tsx` each
  gained a trash-icon button per row. All three deletes share one new
  `ConfirmDeleteDialog` (`components/admin/`) -- same AlertDialog,
  can't-dismiss-mid-request pattern the map's own delete/revoke/remove-
  placement dialogs already use, parameterized rather than copy-pasted
  three times since all three are just "call this endpoint, then
  refresh the list" with no dialog-specific data shape of their own. A
  rejected delete (e.g. a referenced domain) surfaces through the app's
  existing error toast and leaves the dialog open, not a silent no-op.

Verified via curl: renamed and flipped `is_diagram_container` on a
draft class (always allowed); changed its `geometry_type`/`network_role`
while still draft (allowed); compiled it, then confirmed the same
geometry-type change was rejected with a clear message naming its
actual status, while renaming still worked post-compile; deleted a
draft class with a real subtype and field attached, confirmed the
cascade removed everything; created and deleted an unreferenced domain
and field group; confirmed deleting `LAMP_TYPE` (referenced by
`STREET_LIGHT`) and `general` (referenced by many classes) both
correctly rejected with the "referenced by at least one field" message
rather than a raw constraint error. `go build ./... && go vet ./... &&
gofmt -l .`, `npx tsc --noEmit`, and `npm run lint` all finish clean.

Browser-verified (Playwright) end to end: Edit/Delete buttons appear
only on a draft row and disappear once compiled; the inline edit form
round-trips correctly, including the SRID field disappearing when
geometry_type is switched to `none`; all three delete confirm dialogs
work, and deleting a draft class with a real subtype+field cascades
correctly. This pass caught a second real, pre-existing bug (unrelated
to this slice's own new code): `Toaster` (`components/ui/toaster.tsx`)
was mounted only inside `MapPage`, so `pushError`/`pushWarning` calls
on any other route -- `/features`, `/admin/*`, `/connectivity`,
`/relationships`, `/settings` -- updated the toast store but had
nowhere to render; the rejected-`LAMP_TYPE`-delete's error toast simply
never appeared. Fixed by mounting `<Toaster />` once in `app.tsx`,
above every route, instead of inside one page's own component --
`position: fixed`, so where it mounts in the tree has no layout impact,
it just needs to exist exactly once. Re-verified: the same rejected
delete now shows a real red error toast, and the map route's own
toasts still work unchanged.

## Editing an already-compiled class's subtypes and domain values

The original slice drew its scope line at "purely-additive operations
on a brand-new feature class" specifically *because* the compiler's
subtype/domain-value `CHECK` constraints were written once, at initial
`CREATE TABLE`, and never revisited by the existing-table `ALTER
TABLE ADD COLUMN` path. Revisited the same way edit/delete was later
revisited: not by building the whole thing, but by finding the
genuinely safe subset.

**The insight that makes it safe:** every operation this slice's own
API surface can produce is a *widening* change — adding a new subtype
code, or a new value to a domain. Postgres validates `ADD CONSTRAINT`
against a table's existing rows by default, and a strictly wider value
list is trivially satisfied by every row that already passed the
narrower one. So `DROP CONSTRAINT` + `ADD CONSTRAINT` with the new,
wider list can never fail against real data — if metadata ever
*narrowed* instead (outside this API, which has no "remove a
subtype"/"remove a domain value" endpoint), the `ADD CONSTRAINT` would
fail loudly, naming the offending rows, rather than silently applying
a stricter rule underneath live data. Editing or removing an existing
subtype/value is deliberately still not exposed anywhere — only
adding.

### Backend

- **`api/internal/compiler/ddl.go`** — `syncCheckConstraints`, called
  every time `Compile` runs against an *existing* table (not just when
  a column is missing), rebuilds the subtype `CHECK` and every coded-
  domain-linked field's `CHECK` from current metadata.
  `replaceCheckConstraint` does the actual `DROP`/`ADD`, looking up the
  constraint's real name via `information_schema` (`findCheckConstraint`)
  rather than assuming Postgres's default naming — the same caution
  `migrations/0019` already applies elsewhere, for the same 63-byte
  identifier-truncation reason.
- **`api/internal/compiler/compiler.go`** — wires `syncCheckConstraints`
  into the existing-table branch, right after the `ADD COLUMN` loop.
- **`api/internal/admin/add.go`** (new) — `addSubtype` (`POST
  /admin/feature-classes/{code}/subtypes`) and `addDomainValue` (`POST
  /admin/domains/{code}/values`). Each is a plain metadata insert
  inside a transaction, with a friendly duplicate-code pre-check (same
  `codeExists`-style discipline as `create.go`) so a repeat code gets a
  clean 400 instead of a raw unique-violation. Neither touches the
  physical table — recompiling (the same, unchanged `POST
  .../compile`) is what actually widens the constraint.

### The metadata/physical drift window

Adding a subtype or domain value is purely a metadata insert; it
doesn't take effect on the physical table until the class is
recompiled. Between the two, creating a feature that uses the new
value correctly fails with a raw Postgres constraint-violation error —
the exact same drift window that already exists (and was already
accepted) for adding a new *field* to an already-compiled class and
forgetting to recompile. Not a new gap this slice introduces.

### Frontend

- **`web/src/routes/admin-feature-classes.tsx`** — a `ListPlus` icon
  button on every row, any status, opens an inline `AddSubtypeRow`
  card: shows the class's current subtypes, a code/name input, an Add
  button. After a successful add, on an already-compiled class, an
  inline note plus a "Recompile" button appear right there so the
  add-then-apply flow never has to leave the row. The existing
  Compile button (previously shown only for a `draft` row) now always
  shows, labeled "Compile" or "Recompile" depending on status — a real,
  small new capability of its own, since there was previously no way
  to trigger a recompile on an already-active class from this page at
  all.
- **`web/src/routes/admin-domains.tsx`** — the same pattern, an
  `AddDomainValueRow` opened via a `ListPlus` button per domain card
  (coded domains only). Unlike the feature-classes page, this page
  doesn't know which feature classes reference a given domain through
  a compiled field, so the post-add note here is a generic reminder to
  recompile any class using the domain, not a targeted button.

### How this was validated

Backend, against a real Postgres + Go API:

1. `go build ./...`, `go vet ./...`, `gofmt -l .` all clean.
2. Added a second subtype to the existing `SEC_FUSE` class via `POST
   /admin/feature-classes/SEC_FUSE/subtypes`; confirmed creating a
   feature with the new subtype code correctly failed (raw Postgres
   500, the expected pre-recompile drift) until `POST
   /admin/feature-classes/SEC_FUSE/compile` was called, after which it
   succeeded and `\d gis.sec_fuse` showed the widened `subtype = ANY
   (ARRAY[1, 2])` constraint.
3. Added a new value to `LAMP_TYPE` (referenced by `STREET_LIGHT`'s
   `lamp_type` field) the same way; same pre-recompile rejection,
   post-recompile acceptance, confirmed via `\d gis.street_light`.
4. Recompiled every other existing compiled feature class in the demo
   database and confirmed no regressions — `syncCheckConstraints` runs
   unconditionally on every existing-table recompile, so this had to
   be checked, not assumed.
5. Recompiled `SEC_FUSE` and `STREET_LIGHT` a second time each with no
   metadata change in between, confirming the constraint rebuild is
   idempotent and existing feature rows survive it untouched.
6. One real bug caught and fixed during this pass: `findCheckConstraint`'s
   first version used literal `$1`/`$2`/`$3` Postgres-style
   placeholders in a raw query passed to bun's `QueryRowContext` — bun's
   raw-query methods expect `?`-style placeholders (which bun rewrites
   internally to `$1, $2, ...`), so the literal `$1` produced `ERROR:
   there is no parameter $1 (SQLSTATE=42P02)`. Fixed by switching to
   `?, ?, ?`; re-verified with the full sequence above.

Frontend: `npx tsc --noEmit` and `npm run lint` (oxlint) both finish
clean. **Not** browser-verified with Playwright for this slice — the
sandbox this was built in couldn't pull the `docker-compose.yml`
images (Postgres/Martin/RustFS) needed to run the full stack, a
network-policy limitation of that particular session, not a code
issue. The new frontend components follow the exact same pattern
(`AddSubtypeRow`/`AddDomainValueRow`, inline card, toast-based error
handling, query invalidation on success) as `EditFeatureClassRow` and
the other admin inline-edit rows already browser-verified in this
doc's "Edit and delete, added later" section above, and the underlying
endpoints are the curl-verified ones described above — but an actual
click-through of the new UI in a real browser is still outstanding and
should happen before this is considered fully closed out.

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

# Add a subtype to an already-compiled class, then recompile to enforce it
curl -X POST http://localhost:3704/admin/feature-classes/SEC_FUSE/subtypes \
  -H "Content-Type: application/json" -d '{"code": 2, "name": "recloser"}'
curl -X POST http://localhost:3704/admin/feature-classes/SEC_FUSE/compile

# Add a value to an already-referenced coded domain, then recompile any class using it
curl -X POST http://localhost:3704/admin/domains/LAMP_TYPE/values \
  -H "Content-Type: application/json" -d '{"code": "INDUCTION", "label": "Induction", "sort_order": 3}'
curl -X POST http://localhost:3704/admin/feature-classes/STREET_LIGHT/compile
```

Then in the browser: open the sidebar's "Admin" group, visit "Domains"
or "Field groups" to author reference data first, then "Feature
classes" → "New feature class" to build one end to end and compile it
from the success screen.

`go build ./... && go vet ./... && gofmt -l .` should all finish clean,
and so should `npx tsc --noEmit`, `npm run lint`, and `npm run build`
in `web/`.
