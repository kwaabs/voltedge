# Phase 8 — materials catalog

**Goal:** let a coded field's value (e.g. `MV_CONDUCTOR.conductor_spec`
= "ACSR 50mm²") carry real physical/electrical properties -- resistance,
reactance, ampacity -- instead of being just a display label. The
dependency the next slice (electrical analysis: voltage drop, loading/
capacity checks along a traced path) needs before it can do anything
real: those calcs want actual per-unit-length numbers for whatever
conductor a feature says it's made of, not an opaque code.

**Status:** backend curl-verified end to end against a real Postgres
(create/list/update/delete a spec, attach/detach it from a domain value
in both the create-with-inline-values and add-value-later paths, the
delete-guard while referenced, and the narrowing-fails-loudly precedent
this project already established elsewhere doesn't even apply here --
see "Design" below). Frontend Playwright-verified: a new Materials admin
page, a material-spec picker on the Domains page's own value editor,
the wrench indicator on the feature-class detail page's Fields table,
and (see "Follow-up" below) a read-only Materials tab on the map's own
feature panel showing a selected feature's resolved engineering
numbers.

## Design: material and size in one code, not two

`ACSR_50` bakes material and size into a single catalog entry -- the
`_50` denotes 50 mm² cross-section -- rather than treating material and
size as two independent, crossed domains. This matches how conductor
catalogs actually work: resistance and ampacity are properties of the
combination, not something that decomposes cleanly across material ×
size. One row per real-world catalog entry.

## Design: attach to a domain value, not a feature class

The interesting decision was where a material spec attaches. Two
options were on the table:

1. A new field "kind" on the feature class itself -- `MV_CONDUCTOR`
   gets a real `material_spec_id` column, a genuine foreign key the
   compiler has to learn to emit across schemas (`gis.mv_conductor` →
   `metadata.material_specs`), with new CRUD-engine validation to match.
2. Attach to the *domain value* a field already selects. `MV_CONDUCTOR`
   keeps an ordinary coded-domain field (`conductor_spec`, pointing at a
   `CONDUCTOR_SPEC` domain) exactly like `condition_rating` already
   works -- the only change is that some of `CONDUCTOR_SPEC`'s values
   (its `ACSR_50` row) now also carry a link to a `material_specs` row.

Option 2 won: it's additive on top of `metadata.domain_values` (one new
nullable column, `material_spec_id`), needs zero compiler changes, zero
CRUD-engine changes, and reuses the exact admin UI a domain value
already has. A field never has to declare "I carry a material" -- any
coded field automatically inherits it for free the moment its domain's
values happen to be material-linked.

One consequence worth naming: unlike editing a coded domain *value*
(label/sort_order, pure display, see `docs/phase-6-rules-visibility.md`'s
sibling doc on domain-value editing), editing a *material spec*'s own
numbers has zero interaction with `syncCheckConstraints`/`CHECK`
constraints at all -- `material_spec_id` never appears in a physical
`gis.*` table, only in `metadata.domain_values`. There's no
"narrowing fails loudly on recompile" story to worry about here because
nothing about a material spec's numbers is ever baked into a compiled
column's constraint in the first place.

## Backend

- **`migrations/0021_create_material_specs.sql`** — `metadata.material_specs`
  (code, name, category, and five nullable engineering columns, each
  `CHECK`ed `NULL or > 0`: `resistance_ohm_per_km`, `reactance_ohm_per_km`,
  `ampacity_amps`, `diameter_mm`, `weight_kg_per_km`), plus
  `metadata.domain_values.material_spec_id` (nullable FK).
- **`api/internal/metadata/models.go`** — new `MaterialSpec` struct;
  `DomainValue` gained `MaterialSpecID *string`.
- **`api/internal/metadata/load.go`** — `ListMaterialSpecs`.
- **`api/internal/admin`** — `POST/GET /admin/material-specs`,
  `PATCH/DELETE /admin/material-specs/{code}`. `domainValueRequest`
  (used by both `createDomain`'s inline values and
  `addDomainValue`) and `updateDomainValueRequest` both gained an
  optional `material_spec_code`, resolved against a real
  `material_specs` row the same way `resolveDomainCodes`/
  `resolveGroupCodes` already resolve a field's domain/group code --
  new `resolveMaterialSpecCode` mirrors that pattern for a single code.
  `updateDomainValueRequest.material_spec_code` follows the same
  nil/""/value three-state sentinel `updateFieldRequest.domain_code`
  already established: nil leaves the link alone, `""` clears it, any
  other string resolves and attaches.
  `deleteMaterialSpec` blocks while any domain value still references
  it -- unlike `deleteDomainValue` (which needs no such precheck,
  see its own doc comment), this one guards a *real* foreign key, so
  skipping the precheck would surface as a raw Postgres FK-violation
  500 instead of a clear 400.

## Frontend

- **`web/src/hooks/use-admin.ts`** — `AdminMaterialSpec` type,
  `useAdminMaterialSpecs`; `AdminDomainValue` gained
  `material_spec_code`.
- **`web/src/routes/admin-materials.tsx`** (new) — a Materials admin
  page: list, create, edit (pencil), delete, following the same
  Card-list-plus-inline-form pattern every other admin page here uses.
  Five numeric inputs map to the five engineering columns; each is
  optional and only sent when non-empty.
- **`web/src/routes/admin-domains.tsx`** — a shared `MaterialSpecPicker`
  `<select>` (defaults to "none") added to both `AddDomainValueRow` and
  `EditDomainValueRow`. A linked value's pill on the domain card gains a
  small wrench icon and a tooltip naming the spec, so the link is
  visible without opening the edit form.
- **`web/src/components/layout/app-sidebar.tsx`** / **`app.tsx`** —
  new "Materials" nav entry and `/admin/materials` route, alongside
  Domains/Field groups.

## How this was validated

Backend, against the local, non-Docker Postgres this session already
had running:

1. `go build ./... && go vet ./... && gofmt -l .` clean.
2. Created two material specs (`ACSR_50`, `ACSR_95`) with real numbers;
   confirmed a duplicate code 400s; confirmed a negative
   `resistance_ohm_per_km` is rejected by the database's own `CHECK`
   (surfaces as a 500 -- deliberately not given a friendlier message,
   see `validateCreateMaterialSpec`'s own doc comment).
3. Created a `CONDUCTOR_SPEC` domain with one value linked inline
   (`material_spec_code` in the create request) and one unlinked;
   confirmed the list response resolves the link back to a code.
4. Attached a spec to an already-existing value via `PATCH .../values/{code}`;
   confirmed an unknown `material_spec_code` 400s naming the reason;
   confirmed `DELETE /admin/material-specs/{code}` is rejected while
   referenced, then succeeds once cleared via the `""` sentinel.
5. Confirmed editing a spec's own numbers via `PATCH /admin/material-specs/{code}`
   and an unknown code 404s.

Frontend: `npx tsc --noEmit` and `npm run lint` (oxlint) both clean.
Playwright-verified against the same stack: created a material spec on
the new Materials page, edited one of its numbers and confirmed the
updated value rendered, confirmed the duplicate-code error surfaces as
a toast; on the Domains page, created a domain with a value, opened its
editor, attached the material spec via the new picker, and confirmed
the linked value's pill shows the wrench indicator afterward. Zero
browser console errors from the application itself throughout.

## Try it yourself

```bash
# A real catalog entry
curl -X POST http://localhost:3704/admin/material-specs \
  -H "Content-Type: application/json" \
  -d '{"code":"ACSR_50","name":"ACSR 50 mm^2","category":"conductor","resistance_ohm_per_km":0.65,"ampacity_amps":190}'

# Attach it to a coded domain value
curl -X POST http://localhost:3704/admin/domains \
  -H "Content-Type: application/json" \
  -d '{"code":"CONDUCTOR_SPEC","name":"Conductor spec","domain_type":"coded","values":[{"code":"ACSR_50","label":"ACSR 50mm^2","sort_order":1,"material_spec_code":"ACSR_50"}]}'
```

## Follow-up: showing the link on the feature-class detail page

The first round only surfaced a material link on the Domains page
itself -- an admin browsing a feature class's own Fields table (e.g.
`MV_CONDUCTOR`'s `conductor_spec` field) had no way to tell whether any
of that field's domain values were material-backed without navigating
to the Domains page and finding the matching domain by hand. Real user
confusion surfaced this: "how do I attach a material to a feature
class?" -- the honest answer is you don't, directly (see "Design:
attach to a domain value, not a feature class" above), but that answer
is a lot easier to believe when the Fields table shows it.

Rather than widen `GET /feature-classes/{code}/schema`'s own
`domain_values` (currently just bare codes, shared by every other
schema consumer -- create/edit forms included -- so widening it would
touch more than this one display), `admin-feature-class-detail.tsx`'s
`DomainCell` now cross-references `useAdminDomains()` client-side --
already fetched on this page for the edit-field domain picker -- to
look up each value's real label and `material_spec_code`, and renders
the same wrench-icon indicator `admin-domains.tsx`'s own value pills
already use. A useful side effect: every domain-linked field's pills
now show real labels (`Good`/`Fair`/`Poor`) instead of bare codes
(`GOOD`/`FAIR`/`POOR`), matching the Domains page's own display, not
just the material-linked ones.

Playwright-verified: added a real `conductor_spec_test` field to
`MV_CONDUCTOR` pointing at a domain with one material-linked value and
one plain value, confirmed the linked one shows the wrench icon and
tooltip and the plain one doesn't, then removed the scratch field/
domain/spec afterward.

## Follow-up: a read-only Materials tab on the map's feature panel

The map's own `FeaturePanel` had a "Rules" tab (network/relationship/
snapping rules for the selected feature's class -- see
`docs/phase-6-rules-visibility.md`) but nowhere to see a *feature's own*
material data. A direct request to add one ("introduce a new tab called
materials... this is where user will add the feature's materials") led
to a design correction worth recording: a feature's material isn't a
separate thing to author on a new tab. It's already just the value of
one of the feature's own domain-linked fields (e.g. `MV_CONDUCTOR`'s
`conductor_spec`), settable through the Attributes tab's existing edit
form exactly like `condition_rating` or any other coded field. Building
a second add/edit workflow for the same data would duplicate that form
for no reason. The actual gap was narrower: nowhere did a user see the
*resolved* engineering numbers behind whichever value a field currently
holds. So the Rules tab was removed outright (replacing, not adding
alongside) and a **read-only** Materials tab took its place, matching
how Rules was also read-only.

This closes the "reading resolved material numbers from the
feature-facing schema endpoint" gap flagged as out of scope in the
original round above: `GET /feature-classes/{code}/schema`'s
`fieldSchema` gained a `material_specs` map (keyed by domain value
code, `omitempty` so it's absent entirely for a field with no
material-linked values), resolved server-side by a new
`resolveMaterialSpecs` in `api/cmd/server/main.go` -- the same handler
`useFeatureClassSchema` (the map/admin forms' own shared hook) already
calls, not a second admin-only round trip through `/admin/domains`.

`feature-panel.tsx`'s new `MaterialsTab` filters the class's fields
down to the ones carrying `material_specs`, and for each one shows the
feature's current value alongside the resolved spec's name and
whichever of resistance/reactance/ampacity/diameter/weight are set (a
field with no material-linked domain, or a value that isn't
material-linked, shows a plain "not set" / "no material data" message
instead of chips). An "Edit in Attributes" button starts the existing
`useEditStore` edit session *and* switches the panel to the Attributes
tab in the same click, landing directly on the real field to change --
it doesn't open a second form.

## Follow-up: thermal properties for a first-principles ampacity calc

`migrations/0022` adds four more nullable columns to
`metadata.material_specs`: `max_operating_temp_c`, `emissivity`,
`absorptivity`, `resistance_temp_coefficient_per_c`. These are the
conductor-intrinsic inputs an IEEE Std 738 steady-state thermal rating
calc needs on top of the static `ampacity_amps` catalog figure already
there -- letting a later slice compute a conductor's actual current-
carrying capacity from first principles (given real environmental
conditions) rather than only ever reading a fixed catalog number.
Environmental conditions the same calc needs -- ambient temperature,
wind speed/angle, solar heat gain -- are deliberately NOT modeled here:
those vary per calculation run, not per conductor, so they'll be
request-time parameters to whatever endpoint runs the calc, not stored
data. Same admin CRUD surface as the original five columns (Materials
page's create/edit form gained a "Thermal properties" section);
`emissivity`/`absorptivity` are `CHECK`ed `NULL or (0, 1]` rather than
just `NULL or > 0`, since they're unitless fractions.

This is Slice A of a larger, explicitly slice-up electrical-analysis
build (load flow, transformer loading %, symmetrical fault current,
first-principles ampacity) -- see `docs/open-items.md`'s Phase 8
section for the full plan and the design decisions locked for it.

## Follow-up: cost columns

`migrations/0060` adds two more nullable columns to
`metadata.material_specs`: `cost_per_km` and `unit_cost`, both bare
numeric in an assumed single house currency (no currency column —
mixed-currency pricing, if ever needed, is a later migration adding a
currency code, not a reinterpretation of these numbers). Both are
`CHECK`ed `NULL or >= 0` (zero is a meaningful "free" price, unlike the
engineering columns' `> 0`).

Two cost bases rather than one so they don't fight over a column:
`cost_per_km` suits the per-length line materials (conductor/cable) this
catalog actually holds today — it multiplies by a run's length for a
length-based estimate, the same way `resistance_ohm_per_km` scales by
length — while `unit_cost` is a flat per-item price for a material
catalogued per piece (a future pole/fitting). Nothing in the app
consumes either yet: this is catalog data an admin edits (like
`weight_kg_per_km`), ready for a future BOM/estimate calc to read. No
compiler or CRUD-engine change — a domain value still just carries an id
pointer to whichever `material_specs` row is current.

Same admin CRUD surface as every other column: the Materials page's
create/edit form gained a "Cost" section (two inputs, each optional and
sent only when non-empty), the wire type (`AdminMaterialSpec`) and Go
request/response types (`createMaterialSpecRequest`/
`updateMaterialSpecRequest`/`materialSpecResponse`, plus the create/
update/list handlers and the update-validate "at least one field" guard)
gained `cost_per_km`/`unit_cost`, and each material card shows a
`.../km` and/or `.../ea` chip when set.

**Verification owed** (Bash blocked in this environment — user runs
locally): apply `migrations/0060`; `go build ./... && go vet ./... &&
gofmt -l .`; web `tsc --noEmit` + lint; manual — create/edit a spec with
a cost per km and a unit cost, confirm both persist and render as chips,
confirm a negative value is rejected by the `CHECK`.

## What's still out of scope

- **Electrical analysis itself** (voltage drop, loading/capacity
  checks, load flow, fault current) — this slice (and the follow-ups
  above) only build the catalog, the link, visibility of the resolved
  numbers, and now the thermal-property inputs a first-principles
  ampacity calc will need; no calc reads any of it yet.
- **Editing a domain value's material link from the Materials page
  itself** — the link is authored from the Domains page's own value
  editor only, matching where every other domain-value property is
  already edited.
