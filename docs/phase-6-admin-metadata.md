# Phase 6, slice 1 — admin metadata UI (feature class catalog + create wizard)

> **Update:** the "editing an already-compiled class's subtypes / an
> already-referenced domain's values" gap flagged throughout this doc
> as permanently out of scope has since been closed — see "Editing an
> already-compiled class's subtypes and domain values" near the end.
> **Update:** range domains — flagged throughout this doc as unmodeled
> ("range bounds aren't stored anywhere yet") — are now real too. See
> "Range domains" at the very end.
> **Update:** adding a field to an already-existing class, and a real
> page showing a class's fields and domains together, are both real
> now too — see "Adding a field to an existing class, and a real
> detail view" at the very end.
> **Update:** editing an existing field's domain/group/nullable/
> visibility settings in place is real too now — see "Editing an
> existing field's settings" at the very end.
> **Update:** editing and deleting an existing domain's own values, and
> editing a domain's own name/range bounds, are real too now — see
> "Editing and deleting domain values" at the very end.

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
clean but not yet browser-verified. Range domains, adding a field
to an existing class (plus its new detail page), editing an
existing field's settings in place, and editing/deleting an existing
domain's own values are all fully curl- and Playwright-verified end
to end.

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
  supported this, on any class, at any point in this project. *Adding*
  a new field to an existing class is a different, now-closed
  question — see "Adding a field to an existing class, and a real
  detail view" at the end.
- ~~Range domains~~ **Closed** — see "Range domains" at the end of this
  doc.
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

## Range domains

The original slice deliberately built only coded domains — `domain_type
= 'range'` was already a legal value (the `CHECK` on `metadata.domains`
allowed it from Phase 1 onward), but nothing stored an actual min/max
anywhere, and `api/internal/compiler/ddl.go`'s own comment flagged the
gap directly: "nothing to compile into a CHECK for domain_type = 'range'
until that's modeled." Closing it needed a real schema change, not a
UI-only addition — the same shape of gap subtype/domain-value editing
had, just for a different pair of columns.

### Backend

- **`migrations/0020_add_domain_range_bounds.sql`** (new) — adds
  `range_min numeric`/`range_max numeric` to `metadata.domains`, both
  nullable (unused by a coded domain), plus a `CHECK (range_min IS NULL
  OR range_max IS NULL OR range_min <= range_max)` so a backwards
  bound can never land in metadata at all, not even through a future
  write path that skips `api/internal/admin`'s own validation.
- **`api/internal/metadata/models.go`** — `Domain` gained
  `RangeMin`/`RangeMax *float64` fields; `Load`'s existing
  `loadDomains` picks them up automatically (bun selects every mapped
  struct field, no query change needed).
- **`api/internal/compiler/ddl.go`** — `columnDefinition` (used by both
  `buildCreateTable` and the existing-table `ADD COLUMN` path — see
  `compiler.go`) now emits `CHECK (field BETWEEN min AND max)` for a
  field linked to a range domain with both bounds set. `formatNumber`
  renders a bound via `strconv.FormatFloat(..., 'f', -1, 64)` rather
  than `%g`, so a large/small bound never emits scientific notation
  into the SQL text. `syncCheckConstraints` (the same
  recompile-rebuilds-every-domain-linked-CHECK mechanism subtype/domain-
  value editing added) also covers a range-domain field now, for the
  same reason it covers coded ones: there's no admin endpoint that
  edits an *existing* range domain's bounds today, so this branch has
  nothing to widen in practice yet, but it means a future one would
  Just Work without a second compiler change.
- **`api/internal/crud/validate.go`** — `validateDomainRange` mirrors
  the existing coded-domain membership check: a `numeric`/`integer`
  field linked to a range domain gets its value checked against
  `[RangeMin, RangeMax]` before the insert/update, so an out-of-range
  value gets a clean 400 naming the actual domain and bounds instead of
  reaching the database's own `BETWEEN` `CHECK`.
- **`api/internal/admin`** — `createDomainRequest` gained
  `RangeMin`/`RangeMax *float64`; `validateCreateDomain` requires both
  when `domain_type = range` (and rejects either being set for
  `coded`), plus `range_min <= range_max`. `createDomain`/`listDomains`
  thread the two fields straight through to `domainResponse`.
- **`cmd/server/main.go`**'s schema endpoint — `fieldSchema` gained the
  same `RangeMin`/`RangeMax`, populated for a range-domain field the
  same way `DomainValues` already was for a coded one, so the create/
  edit form has something to show a bound against without a second
  request.

### Frontend

- **`web/src/routes/admin-domains.tsx`** — the "New domain" form gained
  a Coded values/Numeric range toggle. Coded shows the existing values
  editor; range shows two plain numeric inputs (min/max), validated
  client-side the same way (`canSave` requires both, min ≤ max) before
  the request ever goes out. An existing range domain's card shows its
  bounds as a `min – max` pill, next to (not replacing) the existing
  coded-values pill row, which is simply empty for a range domain.
- **`web/src/components/map/feature-field-input.tsx`** — the shared
  numeric input (used by both the feature create and edit forms) now
  passes `min`/`max` straight from the field's schema onto the native
  `<input type="number">`, the same low-cost hint a coded domain
  already got for free by becoming a `<select>` instead of a bare
  input.

### How this was validated

This session's sandbox couldn't reach the Docker registry CDN (the same
403 policy block noted earlier in this doc), so this slice was verified
against a **local, non-Docker Postgres 16 + PostGIS install** already
present in the sandbox instead — migrations applied via the real
`migrations/apply.sh`, the real Go API run against it directly:

1. `go build ./... && go vet ./... && gofmt -l .` clean; `migrations/apply.sh`
   ran every migration including 0020 with no errors.
2. `POST /admin/domains` with `domain_type: range`: valid bounds
   accepted; `range_min > range_max` rejected with a clean 400; missing
   either bound rejected; a `coded` domain with `range_min`/`range_max`
   set rejected — all four checked directly.
3. Created a feature class with a `numeric` field linked to the range
   domain, compiled it, confirmed via `\d` that the physical table's
   `CHECK` read exactly `(load_pct >= 0::numeric AND load_pct <=
   100::numeric)`.
4. `POST /features/{code}` with an out-of-range value: rejected with
   `field "load_pct" value 150 is outside LOAD_PCT's range [0, 100]`,
   the friendly Go-side message, not a raw constraint violation; an
   in-range value succeeded (201). `PATCH` with an out-of-range update
   rejected the same way.
5. `GET /feature-classes/{code}/schema` confirmed the field entry
   carries `range_min`/`range_max` for the frontend form to read.

Frontend: `npx tsc --noEmit`, `npm run lint` (oxlint), and `npm run
build` all clean. Browser-verified with Playwright against the same
local stack (`npm run dev`, Vite's own proxy already pointed at
`localhost:3704`): created a range domain through the UI end to end
(name/code, Numeric range toggle, min/max inputs, Create), confirmed it
listed with the correct `range` badge and `0 – 100` bounds pill,
confirmed the pre-existing coded-domain create flow still works
unaffected, zero browser console errors throughout. All test
domains/classes created during verification (`LOAD_PCT`,
`TEST_XFMR_RANGE(2)`, `LOAD_PCT_UI`, `CODED_UI_TEST`,
`TEMP_RANGE_UI`/`TEMP_RANGE_FC`) were deleted afterward, including
dropping their `gis.*` tables.

## Adding a field to an existing class, and a real detail view

Prompted by a direct walkthrough of what the admin UI could and
couldn't actually do yet: "click on a feature class, know its fields,
know the domains" and "modify the class — add fields, attach a
domain" both turned out to trace back to the same single gap.
`is_diagram_container` and domain authoring were already fully real
(see this doc's other "added later" sections); the admin list page
only ever showed a field *count*, never the fields themselves, and
there was no endpoint to add a field to a class that already existed
— only at brand-new-class creation time. Checking the compiler first,
the same way every slice in this doc has: `compiler.go`'s
existing-table `ADD COLUMN` loop already walks every field in metadata
looking for one missing from the physical table — that's been true
since before `api/internal/admin` existed at all (it's what let a
hand-written migration add a field to an already-compiled class). No
compiler change was needed here, only the admin endpoint to actually
insert that field row into metadata in the first place.

### Backend

- **`api/internal/admin/add.go`** — `addField` (`POST
  /admin/feature-classes/{code}/fields`) mirrors `addSubtype`: a
  transaction that loads the class, rejects a duplicate field name,
  resolves `domain_code`/`group_code` by reusing `resolveDomainCodes`/
  `resolveGroupCodes` (already built for `createFeatureClass`, called
  here with a one-element slice), inserts the field, and inserts a
  `subtype_field_rules` row per code in `required_for_subtypes` via a
  new `resolveSubtypeCodes` helper (a code that doesn't exist on this
  class is a 400 naming it, same discipline as everywhere else in this
  package). Reuses `fieldRequest`/adds `fieldResponse` — no new request
  type needed, the create-wizard's own per-field shape was already
  exactly right.

### A real bug, caught by verifying rather than assuming

Curl-testing the happy path first (add a field named `status` — a
plausible-looking real attribute name) surfaced a genuine, previously-
latent landmine: `compiler.go`'s `ADD COLUMN` loop skips a field name
that's already a physical column (`existingCols[f.Name]`), which is
correct for its actual purpose (don't re-add a column that's already
there) but says nothing about *why* the name was already taken. Every
compiled table already has system columns — `id`, `name`, `subtype`,
`status`, `deleted_at`, `geom`, `created_at`, `updated_at`
(`compiler/ddl.go`'s `buildCreateTable`). A field named `status`
would silently ride on the real system status column: `recompile`
would report nothing added (correct — the column exists), but
`syncCheckConstraints` would then treat the new metadata field as an
ordinary domain-linked one and overwrite the column's real
`CHECK (status IN ('draft', 'published'))` with whatever domain the
new field happened to reference — confirmed directly:
`test_addfield_status_check CHECK (status = 'OK'::text)` replaced the
real constraint after one add-then-recompile cycle. Worse, the CRUD
engine would from then on treat `status` as an ordinary writable
attribute, letting a client set the publish-state column directly
through the public attributes API.

Fixed with `reservedFieldNames` (`api/internal/admin/validate.go`) — a
static list of the compiler's own system-column names, checked in
`validateAddField` and (for the same latent risk on a brand-new class
missing subtypes/geometry, where Postgres's own duplicate-column
rejection at `CREATE TABLE` wouldn't catch it) in
`validateCreateFeatureClass`'s own per-field loop. Re-verified after
the fix: `status`/`geom`/`id` all correctly rejected with a clear 400,
and the full happy path re-run confirmed the real system `status`
`CHECK` survives an add-then-recompile cycle untouched.

### Frontend

- **`web/src/routes/admin-feature-class-detail.tsx`** (new) — a
  `/admin/feature-classes/{code}` page reusing the existing
  `useFeatureClassSchema` hook (the same `GET .../schema` response the
  map's own create/edit forms already read — no new backend read
  endpoint needed) to show every field's name, data type, resolved
  domain (a coded domain's values or a range domain's bounds, same
  `DomainCell` rendering `admin-domains.tsx` already established),
  nullability, create/update visibility, and required-for-subtype
  rules, in a real table. Subtypes and terminals are shown too.
  `AddSubtypeRow` moved here from the list page (a per-class action
  belongs on the one-class-at-a-time detail view, not a row action on
  a many-classes-at-once table); a new `AddFieldRow` follows the exact
  same insert-now/recompile-to-apply pattern.
- **`web/src/routes/admin-feature-classes.tsx`** — the list page's
  `ListPlus` "Add subtype" row action is gone (moved to the detail
  page); each row now has an `Eye` "View" button linking to its detail
  page instead.
- **`web/src/app.tsx`** — one new route,
  `admin/feature-classes/:code` — the first dynamic route in this
  app's router.

### How this was validated

Backend, against the local, non-Docker Postgres 16 + PostGIS install
this session's sandbox already had running (same Docker-registry
network-policy block as the two previous "added later" sections in
this doc):

1. `go build ./... && go vet ./... && gofmt -l .` clean.
2. Added a plain field and a domain-linked field with
   `required_for_subtypes` to an already-compiled class; confirmed
   both appear in the schema response immediately, and that using the
   new domain-linked field to create a feature *before* recompiling
   correctly 500s (`column "device_status" ... does not exist` — the
   same drift window every other add-then-recompile flow in this doc
   already has), then succeeds after recompiling, with the domain's
   own `CHECK` correctly enforced.
3. Confirmed a duplicate field name, an unresolvable
   `required_for_subtypes` code, and a malformed (non-lower_snake_case)
   name all reject with a clean 400 naming the actual problem.
4. The reserved-name bug above: found, fixed, and re-verified as
   described.

Frontend: `npx tsc --noEmit`, `npm run lint` (oxlint), and `npm run
build` all clean. Browser-verified with Playwright against the same
local stack (API + a real, non-Docker Martin binary + `npm run dev`):
searched for and opened a real class's detail page, confirmed its
field table showed the correct resolved domain values; opened "Add
field," filled in a plain numeric field, submitted, watched it appear
in the table immediately; clicked Recompile and confirmed via `psql`
that the column was added physically with no change to the class's
other real constraints; separately confirmed "Add subtype" still works
correctly from its new home on the detail page. Zero browser console
errors throughout.

## Editing an existing field's settings

The same walkthrough that surfaced add-field also asked for two more
things in the same breath: editing a field's own settings after
creation, and editing an existing domain's values. Scoped into
separate slices rather than one — this section covers the first, the
smallest and safest of the three.

The question to answer first, same discipline as every other "can
this be edited after the fact" question in this doc: does the setting
get baked into the physical table at compile time, or read live by
whatever actually uses it? `domain_id`/`group_id`, `nullable`,
`visible_on_create`, `visible_on_update`, and `editable_after_publish`
are all the second kind. The CRUD engine (`api/internal/crud/
validate.go`) reads `Nullable` straight off `metadata.Field` on every
write — it's never asserted as a physical `NOT NULL`, `columnDefinition`
(`compiler/ddl.go`) only sets that once, at whichever moment the column
is first added. A domain assignment is exactly the "insert now,
recompile to enforce" pattern this doc has already established for
subtypes and domain values: a following recompile rebuilds the field's
own `CHECK` from `syncCheckConstraints`, same as it always has. None of
the five settings this endpoint touches needed a compiler change —
editing them is real and safe the moment the metadata row changes.

Name and data type are the other kind entirely — physically fixed at
`ADD COLUMN` time with no revisit path anywhere in the compiler — so
`updateFieldRequest` doesn't accept either. That's still the fully
open "Column rename / data type change" row, unchanged by this slice.

One real, if minor, asymmetry worth naming: flipping `nullable` from
`false` to `true` doesn't retroactively loosen an already-compiled
column's own `NOT NULL` (nothing in the compiler revisits that either).
If someone then actually writes a null against such a field, the Go
layer now permits it through `validateValue`, so the write reaches
Postgres and gets rejected there — a raw constraint-violation 500
instead of a clean 400. Not silent data loss, just a rougher failure
mode than usual, and not fixable without giving the compiler an `ALTER
COLUMN DROP NOT NULL` path, which is out of scope here.

### Backend

- **`api/internal/admin/update.go`** — `updateField` (`PATCH
  /admin/feature-classes/{code}/fields/{name}`) loads the field,
  applies whichever of the six `updateFieldRequest` fields are
  present, and does a targeted `UPDATE ... Column(cols...)` naming
  only the columns that actually changed. `domain_code`/`group_code`
  use an explicit empty-string sentinel to mean "clear the assignment
  back to none" — a plain `*string` can't otherwise distinguish
  "clear it" from "leave it alone" through JSON's null-vs-omitted, and
  no real domain/group code is ever legitimately blank. An
  unresolvable `domain_code`/`group_code` is a 400 naming it, same
  discipline as `addField`'s own resolution. An empty request body (no
  field set at all) is also a 400, not a silent no-op.

### Frontend

- **`web/src/routes/admin-feature-class-detail.tsx`** — a new
  `EditFieldRow` component, structurally the sibling of `AddFieldRow`
  but pre-filled from the field's current schema values instead of
  starting blank, and `PATCH`ing instead of `POST`ing. A pencil-icon
  button in a new column on the Fields table opens it for that row.
  `group_code` is deliberately left out of this form even though the
  backend accepts it — the Fields table has never shown which group a
  field belongs to, so there's no current value to display as a
  starting point, and editing a setting the admin can't see the
  before-state of would just be guessing.

### How this was validated

Backend, against a local, non-Docker Postgres/API stack (same setup as
every other section in this doc): built a real field's before/after
state through `GET .../schema` around each `PATCH`; flipped
`nullable`/`editable_after_publish`, confirmed both landed; cleared a
field's domain via the `""` sentinel and confirmed `domain_code`
disappeared from the schema response entirely, then re-set it back;
confirmed an unresolvable `domain_code` 400s naming it; confirmed an
empty request body 400s instead of silently succeeding; confirmed an
unknown field name and an unknown feature class code both 404 with the
right message.

Frontend: `npx tsc --noEmit` and `npm run lint` (oxlint) both clean.
Playwright-verified against the same stack: opened a real class's
detail page, clicked the new pencil icon on a domain-linked field,
confirmed the form opened pre-filled with its actual current settings,
unchecked "Visible on update," saved, confirmed the table's Update
column flipped to "no" immediately with no page reload, then repeated
the same round-trip to flip it back and confirmed the table reflected
that too. Zero browser console errors throughout.

## Editing and deleting domain values

The second of the three gaps from the same walkthrough that produced
field-setting editing above. This one has a sharper edge than the
others: editing a domain value's label/sort_order is pure display
metadata (never referenced by a `CHECK` -- that's built from codes),
so it's unconditionally safe. Deleting one is a narrowing change, the
exact category this doc has repeatedly flagged as unsupported for
subtypes and domain values elsewhere -- so the question that actually
needed answering was whether deleting one here is safe, not just
whether it's technically possible.

It turns out this project already had an answer on record, just never
exercised. `fields.domain_id` is a foreign key to `metadata.domains`
itself (the domain as a whole), never to any individual
`domain_values` row -- so there's no FK-level reason Postgres would
ever reject deleting one value out of a domain a field still uses.
The real safety question is whether real feature data already holds
that code, and `compiler/ddl.go`'s `syncCheckConstraints` doc comment
already documents the answer for exactly this shape of change: a
narrowing `ADD CONSTRAINT` is validated against existing rows by
Postgres itself and fails outright, loudly, naming the actual
conflict, rather than silently applying a narrower rule. Deleting a
domain value is safe for the same reason adding one already was (see
"Adding a subtype/domain value" above) -- both are metadata-only
changes; recompiling is what may or may not translate them into an
actually-valid physical constraint.

### Backend

- **`api/internal/admin/update.go`** — `updateDomain` (`PATCH
  /admin/domains/{code}`, name always, `range_min`/`range_max` only on
  a range domain -- checked against the real row, not just the
  request shape) and `updateDomainValue` (`PATCH /admin/domains/{code}
  /values/{value_code}`, label/sort_order).
- **`api/internal/admin/delete.go`** — `deleteDomainValue` (`DELETE
  /admin/domains/{code}/values/{value_code}`). No referenced-by-a-field
  precheck, unlike `deleteDomain`/`deleteFieldGroup` right above it in
  the same file -- there's genuinely nothing to precheck at the FK
  level, and the real check (does real data use this code) belongs to
  a later recompile, not this endpoint.

### Verifying the safety claim directly, not trusting the comment

Rather than take `syncCheckConstraints`' own doc comment on faith,
this got exercised for real: confirmed `MV_CONDUCTOR`'s
`condition_rating` column had three real rows with `GOOD`, deleted the
`GOOD` value from `CONDITION_RATING` via the new endpoint (succeeded —
no precheck, as designed), then triggered a recompile of
`MV_CONDUCTOR`. It failed exactly as the comment says it should:
`add constraint on condition_rating: ERROR: check constraint
"mv_conductor_condition_rating_check" of relation "mv_conductor" is
violated by some row (SQLSTATE=23514)` — a clean 400, not a 500, and
the physical table's existing `CHECK` was left untouched by the failed
`ADD CONSTRAINT` (Postgres rolls back a failed constraint add). Restored
the value and recompiled again clean.

### Frontend

- **`web/src/routes/admin-domains.tsx`** — a pencil icon on each
  domain's header opens `EditDomainRow` (name, plus min/max for a
  range domain). Each coded value's pill gained an inline pencil
  (opens `EditDomainValueRow`, a compact label/sort-order form) and an
  "×" (opens the same `ConfirmDeleteDialog` the domain-level delete
  action already used, reused rather than duplicated).

### How this was validated

Backend, against the same local, non-Docker Postgres/API stack as
every other section: edited a value's label/sort_order and confirmed
both landed; edited a domain's own name; edited a range domain's
bounds; confirmed `range_min`/`range_max` are rejected on a coded
domain and accepted only together on a range one; deleted a scratch
value and confirmed it was gone; confirmed deleting an unknown value
or domain both 404 with the right message; ran the delete-then-
recompile safety check against real `MV_CONDUCTOR` data described
above.

Frontend: `npx tsc --noEmit` and `npm run lint` (oxlint) both clean.
Playwright-verified: renamed a real domain and reverted it, edited a
real value's label and reverted it, deleted a value from a scratch
domain and confirmed the confirm dialog and the resulting pill list,
edited a scratch range domain's bounds and confirmed the displayed
range updated. Zero browser console errors throughout.

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

# Create a range domain and a field that uses it -- compiling emits a BETWEEN CHECK
curl -X POST http://localhost:3704/admin/domains \
  -H "Content-Type: application/json" \
  -d '{"code": "LOAD_PCT", "name": "Load percentage", "domain_type": "range", "range_min": 0, "range_max": 100}'

# Add a field to an already-compiled class, then recompile to actually add the column
curl -X POST http://localhost:3704/admin/feature-classes/STREET_LIGHT/fields \
  -H "Content-Type: application/json" \
  -d '{"name": "wattage", "data_type": "numeric", "nullable": true, "visible_on_create": true, "visible_on_update": true}'
curl -X POST http://localhost:3704/admin/feature-classes/STREET_LIGHT/compile

# See a class's full fields/subtypes/domains together
curl http://localhost:3704/feature-classes/STREET_LIGHT/schema

# Edit an existing field's settings -- domain/group/nullable/visibility,
# never name or data_type. "" clears a domain/group assignment.
curl -X PATCH http://localhost:3704/admin/feature-classes/STREET_LIGHT/fields/wattage \
  -H "Content-Type: application/json" \
  -d '{"nullable": false, "editable_after_publish": true}'

# Edit an existing coded value's label/sort_order -- always safe
curl -X PATCH http://localhost:3704/admin/domains/LAMP_TYPE/values/LED \
  -H "Content-Type: application/json" -d '{"label": "LED (Light-Emitting Diode)"}'

# Delete a coded value -- recompile any class using it to actually narrow the CHECK
curl -X DELETE http://localhost:3704/admin/domains/LAMP_TYPE/values/INDUCTION
```

Then in the browser: open the sidebar's "Admin" group, visit "Domains"
or "Field groups" to author reference data first, then "Feature
classes" → "New feature class" to build one end to end and compile it
from the success screen.

`go build ./... && go vet ./... && gofmt -l .` should all finish clean,
and so should `npx tsc --noEmit`, `npm run lint`, and `npm run build`
in `web/`.
