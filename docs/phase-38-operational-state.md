# Phase 38 — Operational lifecycle (`operational_state`)

A feature's **operational lifecycle** is a *declared*, human-set state that
tracks where a physical asset is in its life: it is planned, it is in service,
it is temporarily tagged out, it is retired. This is deliberately **not** the
same thing as electrical energization, and **not** the same thing as the
publish `status` column. Keeping the three apart is the whole point of this
phase.

## Why this is a new axis, not an extension of `status`

The system already has a `status` column on every compiled table — but it is a
**publish workflow**, hardcoded by the compiler to `draft` / `published`
([compiler/ddl.go](../api/internal/compiler/ddl.go) — the `status text NOT NULL
DEFAULT 'draft' CHECK (status IN ('draft','published'))` column), transitioned
only by its own endpoints (`POST /{code}/{id}/publish`,
`POST /{code}/{id}/unpublish` — [crud/handler.go](../api/internal/crud/handler.go),
[crud/publish.go](../api/internal/crud/publish.go)), and it gates on
completeness and locks post-publish fields. It answers *"is this record
finalised?"* — an editorial question, orthogonal to everything else
([crud/delete.go](../api/internal/crud/delete.go): *"draft/published are
orthogonal questions"*).

So `draft` is **not** the first step of a PLANNED→…→RETIRED chain — it lives on
a different axis. Overloading `status` with operational values would entangle
operational lifecycle with publish-completeness gating, field-locking, and the
`?status=` list/search filter that assumes exactly two values. Three genuinely
independent axes:

| Axis | Where it lives | Values | Set by | Changes |
| --- | --- | --- | --- | --- |
| Record workflow (exists) | `status` system column | draft ↔ published | `/publish`, `/unpublish` | editorial, slow |
| **Operational lifecycle (this phase)** | `operational_state` coded-domain field | PLANNED → IN_SERVICE → ISOLATED → RETIRED | a human, via the edit form | operational, slow |
| Electrical energization | *nothing — derived* | energized / de-energized | the network (switch positions + topology) | instant, network-wide |

Energization stays **derived** (a downstream trace from a source through closed
switches — what [`/trace/isolate`](../api/internal/trace/isolate.go) already
computes). It is never a stored field: a stored "energized" flag would have to
cascade-write across the whole downstream network on every switch toggle and
would silently drift. Operational lifecycle is the opposite — a person declares
it, deliberately, one asset at a time — which is exactly what a stored state
field is for.

## What the user wants

- A **declared per-feature operational state** with the transitions
  `PLANNED → IN_SERVICE → ISOLATED (tagged out) → RETIRED`.
- New features should start life at `PLANNED`, and a person moves them along.
- Orthogonal to publish `status` and to derived energization (above).

## Shape (decided)

- Model it as a **coded-domain metadata field** `operational_state` — the same
  mechanism `device_state` (OPEN/CLOSED on `DIST_SWITCH`) already uses: a
  `metadata.fields` row with a coded `metadata.domains` + `metadata.domain_values`
  set. It compiles to `operational_state text CHECK (operational_state IN
  (...))` ([compiler/ddl.go](../api/internal/compiler/ddl.go) `columnDefinition`,
  coded case) and flows through generic CRUD and the create/edit forms with
  **no API or compiler change**.
- **Not** a change to the hardcoded `status` column.
- **Nullable** at the DB level (see "born PLANNED" below for why non-nullable
  can't be compiled onto tables that already hold rows), with the create form
  defaulting the picker to `PLANNED`.
- Applies to **physical asset classes** — every class that represents a built,
  installable thing that gets commissioned and retired. This is a *broader* set
  than Phase 37's `circuit_id`: it includes the structural classes (`POLE`,
  `BAY`, `SUBSTATION_YARD`, `METER_CHAMBER`) that were deliberately left out of
  circuit membership, because a pole is still planned/in-service/retired even
  though it belongs to no circuit. The exact list is the seed's to enumerate;
  per-class opt-in is just "does this class have the field," like every
  metadata field.

## Slice plan

1. **The field (free dropdown)** — the domain + values + the `operational_state`
   field on the asset classes, a create-form default of `PLANNED`, a one-time
   backfill of existing rows, and recompile. Any transition is allowed (it's a
   plain dropdown). **This is the MVP the user asked for.**
2. **Enforced transitions (optional)** — reject illegal jumps
   (e.g. `RETIRED → PLANNED`) via a transition rule, and/or dedicated
   transition endpoints mirroring `/publish`. Only if a free dropdown proves too
   loose.
3. **Barrier wiring (optional)** — make `ISOLATED` (and `RETIRED`) halt a
   downstream/upstream trace, so a tagged-out asset drops out of the derived
   energized set. Requires a small trace-engine change (multiple barrier
   conditions per class — see the slice).

---

## Slice 1 — the `operational_state` field (free dropdown) — CODE-COMPLETE

The whole MVP: a coded field every asset class carries, defaulting new features
to `PLANNED`, editable to any other value from the feature edit form.

**Decisions locked (were open):** dedicated **`operational` field group** (not
`electrical` — reads wrong on a pole; see below); **broadest class scope** — all
physical assets incl. structural (below); **create-form default** delivers "born
PLANNED" (no schema default is possible). Files: `seed/operational_state_example.sql`
+ `seed/operational_state_backfill.sql`, tasks `seed-operational-state` /
`seed-operational-state-backfill`, and a one-line default in
`feature-create-form.tsx`. No Go/compiler change.

### The domain (seed)

`seed/operational_state_example.sql` (task `seed-operational-state`), modelled
on how `TRACE_DIRECTION` / `device_state`'s domain is seeded
([seed/trace_configurations_example.sql](../seed/trace_configurations_example.sql)
for the domain+values idiom):

```
metadata.domains:        OPERATIONAL_STATE  (domain_type 'coded')
metadata.domain_values:  PLANNED     'Planned'      sort 1
                         IN_SERVICE  'In service'   sort 2
                         ISOLATED    'Isolated'     sort 3
                         RETIRED     'Retired'      sort 4
```

`sort_order` puts `PLANNED` first so the create form's picker lands on it by
default (the forms render a coded field's options in `sort_order`).

### The field + which classes get it (seed)

Same INSERT-over-a-class-set idiom as `circuit_ref`
([seed/circuit_ref_example.sql](../seed/circuit_ref_example.sql)):

```
INSERT INTO metadata.fields (feature_class_id, name, data_type, domain_id, group_id, nullable)
SELECT fc.id, 'operational_state', 'text', d.id, fg.id, true
FROM metadata.feature_classes fc
JOIN metadata.domains d      ON d.code  = 'OPERATIONAL_STATE'
JOIN metadata.field_groups fg ON fg.code = 'status'      -- or 'electrical'; see note
WHERE fc.code IN (<physical asset classes>)
ON CONFLICT (feature_class_id, name) DO NOTHING;
```

- **Nullable** — deliberately. A non-nullable field compiles to `... NOT NULL`,
  and the compiler adds it to an already-populated table via
  `ALTER TABLE ADD COLUMN` with **no `DEFAULT`**
  ([compiler/ddl.go](../api/internal/compiler/ddl.go) — custom columns never get
  a `DEFAULT`, only system columns do), which Postgres rejects on a table that
  already has rows. Nullable + a backfill (below) is the clean path.
- **Field group** — reuse an existing group so the create form buckets it
  sensibly. If a `status`/`lifecycle` group exists, use it; otherwise the
  `electrical` group already used by `circuit_ref` is fine. (Confirm the group
  code before writing the seed — do not invent one.)
- **Class list** — the physical asset classes. Start from the circuit
  membership list plus the structural classes, i.e. the electrical apparatus
  **and** `POLE`, `BAY`, `SUBSTATION_YARD`, `METER_CHAMBER`. Final list is the
  seed's call; leave out anything abstract/annotation-only.

### "Born PLANNED" — the default, and existing rows

There is no DB-level default (compiler doesn't emit one for custom columns), so
"new features start at PLANNED" is delivered by the **create form** preselecting
the lowest-`sort_order` value — which `PLANNED` is. No code change if the form
already defaults a required/coded picker to its first option; otherwise a tiny
create-form tweak to preselect the first value for `operational_state`.

Existing rows get `NULL` from `ALTER ADD COLUMN`. A **one-time backfill** in the
same seed maps the old world onto the new axis, which also directly answers "how
do I get everything off draft":

```
-- Published (real, energised) assets are In service; drafts are still Planned.
UPDATE gis.<table> SET operational_state = 'IN_SERVICE'
  WHERE operational_state IS NULL AND status = 'published';
UPDATE gis.<table> SET operational_state = 'PLANNED'
  WHERE operational_state IS NULL AND status = 'draft';
```

(One pair of statements per stamped physical table — the seed enumerates them,
same as the circuit-membership recompile list.)

### Frontend

- **Create form** — one-line default: the initial `attributes` now seed
  `operational_state: "PLANNED"` (under the seed spread, so a Duplicate draft's
  own value still wins; dropped at save on a class without the field). That's
  what makes a new feature's picker land on *Planned* instead of the coded
  field's blank "—" ([feature-field-input.tsx](../web/src/components/map/feature-field-input.tsx)
  renders a `<select>` whose first option is empty).
- **Edit form** — nothing: it renders the stored value through the same
  metadata-driven `FieldInput`, so any-value → any-value works (free dropdown).
- **Feature detail panel** — nothing: the panel already resolves a coded-domain
  attribute to its label (the same resolution the subtype fix used). So
  `operational_state` shows *"In service"*, not `IN_SERVICE`, automatically.

### Slice 1 — verification owed (Bash blocked here)

- api: `go build ./...`, `go vet ./...` — expected clean; **no Go change was
  made** (pure metadata + one web line). Confirm nothing regressed.
- web: `tsc --noEmit`, lint, `vite build` (the create-form default is the only
  web touch).
- migrate/seed, in order: `task seed-operational-state` → recompile each stamped
  class (`task compile CODE=…`, so the physical tables gain the
  `operational_state` column with its `CHECK`) → `task seed-operational-state-backfill`.
- manual: create a new feature → its `operational_state` picker defaults to
  *Planned*; save, edit it to *In service*, reopen → round-trips; the detail
  panel shows the label, not the raw code; an existing published feature reads
  *In service* after the backfill, a draft reads *Planned*; a structural `POLE`
  carries the field (under an "Operational" section), an abstract/annotation
  class does not.

---

## Slice 2 — enforced transitions — CODE-COMPLETE

Slice 1 is a free dropdown: any value → any value. That's too loose (a `RETIRED`
asset shouldn't jump back to `PLANNED`), so Slice 2 constrains it. Legal graph:

```
PLANNED ─▶ IN_SERVICE ─▶ ISOLATED ─▶ IN_SERVICE   (re-energise after tag-out)
                       │
IN_SERVICE / ISOLATED ─┴▶ RETIRED   (terminal)
PLANNED ───────────────▶ RETIRED    (cancelled before build)
```

**Decision: the validation-rule approach, made fully generic and domain-scoped.**
Not dedicated endpoints — those add surface for no gain here (no transition needs
its own permission or side effect yet; the free-field edit already flows through
one write path and one edit form). The transition graph is a property of the
**coded domain** (`OPERATIONAL_STATE`), not a field or a class, so it's authored
once and inherited by all 23 classes that carry an `operational_state` field —
and any coded domain can opt into a state machine the same way. No field name is
hardcoded in Go.

**What was built:**

- **Migration [0065_create_domain_transitions.sql](../migrations/0065_create_domain_transitions.sql)**
  — `metadata.domain_transitions (domain_id, from_value_id, to_value_id)`, a
  unique `(domain, from, to)` edge, a `CHECK (from <> to)` (a self-edge is
  meaningless — "no change" is always allowed by the engine), FKs cascade off
  `domains`/`domain_values`. A domain with **no rows is unconstrained** — the
  default for every existing coded domain (`device_state`, phase…), so nothing
  changes until the seed populates `OPERATIONAL_STATE`.
- **[metadata/models.go](../api/internal/metadata/models.go)** — `DomainTransition`
  model (value-id FKs, the same shape `trace_barrier_rules.halts_on_value_id`
  uses).
- **[metadata/load.go](../api/internal/metadata/load.go)** — `loadDomains` now
  also fetches a coded domain's edges and resolves the value-id FKs back to
  **codes**, into `Loaded.DomainTransitions` (`domainID → fromCode → []toCode`).
  Helper `TransitionAllowed(domainID, from, to)`: true when the domain has no
  graph, when `from`/`to` is empty (first-declaration / un-declaration — not a
  lifecycle move), when `from == to`, or when the edge is explicitly authored.
- **[crud/validate.go](../api/internal/crud/validate.go)** — `validateTransitions`
  walks every changed coded field, compares the current row's value → the new
  value via `TransitionAllowed`, and 400s an illegal jump naming the codes + the
  domain.
- **[crud/update.go](../api/internal/crud/update.go)** — one hook after the
  coerced values are built, before the completeness re-check. It reads the
  already-fetched `existing` row, so it's inherently **update-only** (create
  declares an initial value, no "from"). Being in `UpdateFeature` means the REST
  PATCH and the MCP `update_feature` tool are both enforced from one place.
- **Seed [operational_state_transitions.sql](../seed/operational_state_transitions.sql)**
  + task `seed-operational-state-transitions` — the 6 legal edges, idempotent on
  the unique index. Metadata-only, **no recompile** (pure lookup rows).

**Apply order:** `task migrate` (0065) → `task seed-operational-state-transitions`.
No class recompile needed — the physical tables are untouched.

**Deliberately still allowed** (not lifecycle transitions): `NULL → any` (setting
the field for the first time on an older/unbackfilled row), `any → NULL`
(clearing the declaration), and same-value no-ops. Enforcement only bites on a
move between two *set* states.

**Not built (intentionally):** a frontend affordance to grey out illegal next
states in the edit picker. The 400 surfaces as the edit form's existing error
toast, which is sufficient; narrowing the dropdown would need the schema endpoint
to ship the transition graph. Revisit if the toast proves annoying in practice.

**Verification owed (Bash blocked):** `go build ./...` + `go vet` (expect clean),
web `tsc`/lint (no web change this slice, so unaffected), then run migration 0065
+ `seed-operational-state-transitions` and confirm: `RETIRED → PLANNED` 400s,
`IN_SERVICE → ISOLATED` succeeds, editing a non-lifecycle field on the same
feature is unaffected, and a NULL → `IN_SERVICE` first-set still works.

---

## Slice 3 — barrier wiring: `ISOLATED` stops a trace — CODE-COMPLETE

The pay-off that ties this back to the energization discussion: a tagged-out
asset should drop out of the *derived* energized set. Make `operational_state =
ISOLATED` (and `RETIRED`) a **trace barrier**, exactly like `device_state =
OPEN` on a switch — a `trace_barrier_rules` row per class on the
downstream/upstream configs
([seed/trace_configurations_example.sql](../seed/trace_configurations_example.sql)
is the template). Then energization-by-derivation reads the declared operational
state for free: no stored energized flag, but a tagged-out transformer stops the
walk.

**The engine change (the catch), now made.** The barrier engine used to keep
**one** barrier condition per feature class — `resolveBarriers` built a
`map[classID]barrierCheck` and the last rule for a class won. `DIST_SWITCH`
already spends a slot on `device_state = OPEN`, so an `operational_state =
ISOLATED` rule on the *same* class would have silently clobbered it. So
[trace/graph.go](../api/internal/trace/graph.go) now keeps a **slice per class**:

- `barrierCheck` type comment rewritten — a class can carry more than one check.
- `resolveBarriers` returns `map[classID][]barrierCheck` and **appends** each
  rule instead of overwriting.
- `checkBarrier` reads the live row once and returns true if **any** of the
  class's checks match (OR-combined).
- `bfs` signature takes the new map type; the two barrier-less callers
  ([feeder.go](../api/internal/trace/feeder.go),
  [isolate.go](../api/internal/trace/isolate.go)) pass `map[string][]barrierCheck{}`.

Small, contained, and it generalises the barrier model (multiple stop conditions
per class) rather than special-casing this field. `section.go` and `runTrace`
pass `resolveBarriers`' result straight through, so they picked up the new type
with no change.

**The seed** ([operational_state_barriers.sql](../seed/operational_state_barriers.sql),
task `seed-operational-state-barriers`): one set-generating INSERT adds an
`operational_state = ISOLATED` and an `operational_state = RETIRED` barrier on
`DOWNSTREAM_TRACE` + `UPSTREAM_TRACE` (the two configs with
`stop_at_open_devices = true` — attaching to `CONNECTED`/`SHORTEST_PATH` would be
inert, and *should* be: a connectivity audit is about physical wiring, not live
state). Scoped to classes with `network_role IN ('edge','junction')` — the only
ones a trace traverses — so structural / non-traced classes (`POLE`,
`PROTECTIVE_RELAY`, containers) that also carry the field don't get inert rules.
Idempotent via `WHERE NOT EXISTS`, metadata-only, **no recompile**.

**Decisions locked** (were open questions):

- **`RETIRED` is a barrier too**, not just `ISOLATED`. A retired asset *may*
  still have live `runtime.feature_connectivity` edges (nothing auto-disconnects
  it), so relying on it "never tracing anyway" is unsafe — an explicit barrier
  guarantees a decommissioned asset drops out of the derived energized set.
  `PLANNED` is deliberately **not** a barrier: a planned extension should still
  be traceable for planning.
- **Not applied to `CONNECTED`/`SHORTEST_PATH`** — those configs don't respect
  barriers at all (`stop_at_open_devices = false`), same as the open-switch case.

**Apply order:** the Go change ships with the binary; then
`seed-operational-state-barriers` (after `seed-operational-state` and the trace
configs exist). No migration, no recompile.

**Verification owed (Bash blocked):** `go build ./...` + `go vet` (expect clean —
the signature change is internal to `package trace`), then with the seed applied:
set a mid-line `DIST_SWITCH` (or `DIST_XFMR`) to `operational_state = ISOLATED`
and confirm a `DOWNSTREAM_TRACE` from upstream stops at it and lists it under
`barriers`; confirm the same switch still stops on `device_state = OPEN`
independently (both checks coexist); confirm a `CONNECTED_TRACE` still walks
through it (barriers off).

---

## Follow-on — publish/unpublish UI (the `status` review gate) — CODE-COMPLETE

Surfaced directly from this phase's status-vs-operational_state discussion: a
user saw a feature stuck on `status = Draft` while `operational_state =
IN_SERVICE` and asked why they couldn't change it. Two findings came out of it:

1. **`status` is correctly *not* an editable attribute** — it's the publish
   system column ([compiler/ddl.go](../api/internal/compiler/ddl.go)), not a
   `metadata.fields` row, so it rightly never appears in the edit form. But the
   control that's *supposed* to change it was never built on the web: the backend
   endpoints (`POST /{code}/{id}/publish`, `/unpublish` —
   [crud/handler.go](../api/internal/crud/handler.go)) were fully wired, yet
   nothing in `web/src` called them — so `status` could only change out-of-band
   (a raw API call or MCP).

2. **What draft/published actually is.** Auditing every reader of the feature
   `status` column: it drives **only** (a) the completeness gate — can't publish
   until required-by-subtype fields are filled, and a published row must stay
   complete on every later write ([publish.go](../api/internal/crud/publish.go),
   `validateUpdateCompleteness`); (b) field locking — `!editable_after_publish`
   fields freeze once published ([validate.go](../api/internal/crud/validate.go));
   (c) an *optional* `?status=` list/search filter that defaults to **both**. It
   is **not** a visibility or network control — draft features are fully on the
   map, fully traceable, fully exported, fully synced to the mobile field app
   (no tile function, trace query, export, or field pack filters by it). So
   draft/published is a **data-quality / review flag**, orthogonal to
   `operational_state` (declared life) and to derived energization. Decision
   (user, 2026-09-02): **embrace it as a review gate** — `published` means
   "reviewed & finalized".

**What was built** — all in
[feature-panel.tsx](../web/src/components/map/feature-panel.tsx)'s
`AttributesTab`, no backend change:

- An **inline control on the Status row** (not an overflow-menu item — the user
  was looking *at* the Status row, so that's where the affordance belongs). The
  row now shows a **Badge** (`secondary` for Draft, `success` for Published) with
  a small **Publish** / **Unpublish** button beside it. Label + icon flip by the
  current `status` (`CheckCircle2` publish / `Undo2` unpublish, `Loader2` while
  in flight).
- `handleSetPublished(publish)` — a `POST` to the matching endpoint, then
  `queryClient.invalidateQueries({ queryKey: ["feature", code, id] })` so the
  Status row *and* the edit form's post-publish field locks (both read the same
  `useFeature` cache) refresh. **No map refresh** — the vector tiles don't style
  by publish status, so a bump would be wasted.
- **Gated on `features:write`** (`usePermission()("features:write")`) — the same
  permission the POST endpoints require via the method-aware `/features` mount
  gate ([cmd/server/main.go](../api/cmd/server/main.go)). The control is **hidden**
  (not disabled) without it, and on a soft-deleted feature — same guard
  Duplicate/Delete use.
- **Publish stays server-gated on completeness** — a draft missing a required
  field 400s from `publishFeature`, surfaced verbatim as the existing error
  toast; the UI doesn't re-implement the check. Unpublish is ungated, matching
  the backend.

**Deliberately not built:** a confirmation dialog (publish/unpublish is cheaply
reversible by its opposite, unlike Delete/RemoveGeometry which do use one);
disabling Publish client-side on incompleteness (the server is the authority and
returns a specific message); narrowing the control by a *separate* publish
permission.

**Open (deferred) — who may publish.** The gate is `features:write` today, so
*any* map editor can publish. If a true review workflow wants a distinct
sign-off role (editor drafts, supervisor publishes), the clean next step is a
dedicated `features:publish` permission: a new `PermFeaturesPublish` constant, a
per-method gate on just the publish/unpublish routes (they'd move behind their
own `RequirePermission`), role-seed assignments, and swapping the UI's
`usePermission()("features:write")` → `"features:publish"`. Not built pending a
decision on whether publishing should be reviewer-only.

**Verification owed (Bash blocked):** web `tsc --noEmit`, lint, `vite build`.
Manual: on a draft feature the Status row shows a **Draft** badge + **Publish**
→ click → badge flips to **Published** and the edit form locks
non-`editable_after_publish` fields; published shows **Unpublish** → click →
back to Draft; an incomplete draft's Publish surfaces the completeness 400 as a
toast and stays draft; a viewer lacking `features:write` sees the badge but no
button.

## Not in scope

- Storing energization on features (stays derived — see the top of this doc).
- An admin UI to author the operational states / transition graph (seed-only for
  now, like the network attribute in Phase 37).
- Time-series / history of state changes (only the current value is stored;
  `updated_at`/`updated_by` already record the last change).
