# Phase 25 — stencil → feature promotion

**Status: Slices 1, 1a, 2, 3 code-complete (verification owed — Bash blocked here, run locally).**

## Why

Phase 24 Slice 2 gave the diagram canvas a **symbol stencil library**: a user
stamps an ANSI/IEEE glyph (`ansi-relay`, `ansi-fuse`, …) as a non-feature
`symbol` element — pure decoration, backed by no feature, absent from trace and
connectivity. That's the right default for the many glyphs that *aren't* network
devices (ground studs, north arrows, device-number circles).

But sometimes the glyph you stamped **is** a real device you now want in the
network. Promotion closes that gap: double-click a stamped symbol and turn it
into a real feature in place — the decoration becomes a first-class feature that
trace and connectivity can see.

Promotion is deliberately the **mirror of slot binding** (Phase 24 Slice 4) and
reuses the **diagram digitize spine** (Phase 12): both already turn a
non-feature thing on the canvas into a real `diagram_placement` by creating /
selecting a feature and materialising a placement. Promotion adds nothing to
that spine except a seed (the glyph's mapped class + position) and a cleanup
(deleting the glyph once the feature exists).

## The model

- A stencil maps to at most one feature class via a new nullable
  `metadata.diagram_stencils.promote_class` **code** (not an FK — same "code,
  not FK" choice as `diagram_elements.slot_class`). `NULL` = decoration only,
  not promotable.
- The target must be a `geometry_type = 'none'` class (a diagram-native device):
  a promoted symbol has no map location, so the feature is created with no
  geometry, exactly like the digitize tool already requires. It must also be
  `is_diagram_placeable` — a promoted feature *is* a placed feature.
- Promotion is a **frontend compose**, not a bespoke transactional endpoint —
  matching how digitize and slot binding already work: `create feature` →
  `POST …/placements` → `DELETE …/elements/{symbol}`. (A single-transaction
  `/promote` endpoint was considered and rejected for now: it would diverge from
  the two sibling flows for a marginal atomicity gain the codebase already
  accepts on the digitize path.)

### End-to-end today

Only `ansi-relay → PROTECTIVE_RELAY` works on current seed data — `PROTECTIVE_RELAY`
is the one `geometry_type = 'none'` diagram-native class, and it already carries
a `render_context = 'diagram'` symbology rule pointing at `ansi-relay`, so the
promoted placement re-renders as the **same glyph** — seamless. Any other glyph
(`ansi-fuse`, the device-number circles, …) becomes promotable the moment a
matching geometry-none class is authored and mapped the same way: **pure
content, no code** — one more guarded `UPDATE … SET promote_class` in
`seed/ansi_stencils.sql`.

## Slice 1 — what shipped

### Data

- **`migrations/0051_add_stencil_promote_class.sql`** — `ALTER TABLE
  metadata.diagram_stencils ADD COLUMN promote_class text` (nullable) + column
  comment. No FK, no backfill.
- **`seed/ansi_stencils.sql`** — two idempotent, guarded `UPDATE`s appended
  beside the existing `PROTECTIVE_RELAY` glyph repoint:
  1. `promote_class = 'PROTECTIVE_RELAY'` for the `ansi-relay` stencil (only
     when still `NULL` and the class exists).
  2. `is_diagram_placeable = true` for `PROTECTIVE_RELAY` (only when still
     `false`) — migration 0032 seeds that flag by `network_role` for classes
     present at migration time, but this class is seeded later, so promotion
     would otherwise trip `placeFeature`'s placeable gate.

### Backend

- `metadata.DiagramStencil` += `PromoteClass *string` (`bun:"promote_class"`);
  `ListDiagramStencils` selects it via the model, no query change.
- `cmd/server` `diagramStencil` DTO += `PromoteClass *string`
  (`json:"promote_class,omitempty"`), mapped through
  `diagramStencilsHandler`. `GET /symbology/stencils` now carries the mapping;
  still `features:read`, unchanged.
- **No new endpoint** — promotion composes the existing
  `POST /features/{code}`, `POST /diagrams/{id}/placements`, and
  `DELETE /diagrams/{id}/elements/{elementID}` (each already RBAC-gated).

### Frontend

- `use-diagram-stencils.ts` `DiagramStencil` += optional `promote_class`.
- `draw-store.ts` — `DraftFeature.diagramPlacement` widened with optional
  `rotation` (carry the glyph's angle onto the placement) and
  `promoteFromElementId` (the symbol to delete on save). A plain digitize leaves
  both unset.
- `feature-create-form.tsx` — on save of a `diagramPlacement` draft it now
  posts `rotation ?? 0`, and when `promoteFromElementId` is set it
  `DELETE`s that element and invalidates `["diagram-elements", diagramId]` after
  the placement — swapping the glyph for the real feature. Everything else
  (validation, subtype, required attributes) is the shared create form's, so a
  promoted feature is a *fully valid* feature, filled in the same panel form the
  user already knows.
- `diagram-canvas.tsx` — `promoteClassByRef` memo (icon_ref → class code, from
  the stencils catalog); `promoteSymbol(element)` starts a diagram draft
  targeted at the mapped class carrying the glyph's x/y/rotation and element id;
  the `symbol` render branch gets an `onDoubleClick` that
  `preventDefault()` + `stopPropagation()` then calls `promoteSymbol(element)`,
  and, when promotable, a native `<title>` "Double-click to promote to a
  {class} feature" for discoverability.

### Slice 1a — double-click fix (user-reported)

First run reported "double click doesn't work — instead it highlights the label
in the diagram." Two fixes:

- The symbol `<g>` gained `select-none` and its `onDoubleClick` now takes the
  event and calls `e.preventDefault()` + `e.stopPropagation()` before
  `promoteSymbol` — killing the native text-selection highlight and stopping the
  double-click from bubbling to the canvas-level `handleCanvasDoubleClick`
  (matching the working text-edit / title-block handlers, which also
  `stopPropagation`). Pointer-capture on the `<g>` is *not* the blocker: the
  dblclick bubbles from the `<image>` child to the capturing `<g>`, exactly as
  the shipped text-edit double-click already relies on.
- `promoteSymbol` no longer silently no-ops on an unmapped symbol — it
  `pushWarning`s "This symbol isn't linked to a feature class…". This is also
  the diagnostic for a stale backend: if double-clicking the **relay** still
  warns, the running API isn't returning `promote_class` yet → migration 0051 +
  the `seed/ansi_stencils.sql` UPDATE + a server rebuild haven't been applied.

### UX

Double-click a mapped symbol → the shared create form opens in the feature panel
(same surface the diagram digitize tool uses), pre-targeted to the class and
showing its subtype + required fields → fill + **Save** → the feature is created,
placed where the glyph was (same rotation), and the glyph is removed. Cancel
leaves the symbol untouched.

## Slice 2 — admin promote-mapping editor

Slice 1 only seeded the `ansi-relay → PROTECTIVE_RELAY` mapping by hand-applied
SQL (`seed/ansi_stencils.sql`), which drifted in practice: migration 0051 added
the `promote_class` column but the seed `UPDATE` was applied against the column
before it existed (silently skipped), leaving the relay unmapped and the
double-click surfacing its "not linked" warning. Slice 2 makes the mapping a
first-class, point-and-click admin operation so it never has to be seeded by
hand again.

### Backend

- **`GET /admin/symbology-stencils`** — the write-side catalog list (every
  stencil + its `promote_class`), `metadata:read`. Same rows as the public
  `GET /symbology/stencils`, served through the `/admin` group so the editor
  stays in `metadata:*` land like every other admin page.
- **`PATCH /admin/symbology-stencils/{icon_ref}`** — set or clear
  `promote_class`, `metadata:write`. Body `{promote_class}` uses the same
  `nil`/`""`/value three-state sentinel `updateFieldRequest.domain_code`
  established (nil = leave alone, `""` = clear to decoration-only, a code =
  set). Validation: the target class must exist and be `geometry_type='none'`
  (the one hard promotion rule — a promoted symbol is a geometry-less feature);
  a non-`none` class is rejected with a clear 400. `is_diagram_placeable` is
  **not** blocked here (a mapping may be authored before the flag is flipped,
  in either order) — the editor warns instead.
- New `api/internal/admin/stencil.go` (`listDiagramStencils` +
  `updateDiagramStencil`, the standard RunInTx → select → validate → update →
  DTO shape); DTOs in `types.go`; two routes + handlers in `handler.go`. No new
  permission — reuses `metadata:read`/`metadata:write` with the rest of
  `/admin`.

### Frontend

- `use-admin.ts` — `AdminDiagramStencil` type + `useAdminDiagramStencils()`
  hook (key `["admin","symbology-stencils"]`).
- `web/src/routes/admin-stencils.tsx` — **Stencil promotion** page: the catalog
  grouped by category, each row showing the real glyph (from
  `useAdminSymbologyIcons`) + a feature-class picker. The picker offers only
  geometry-none classes (`useAdminFeatureClasses` filtered) plus a "decoration
  only" clear option; changing it PATCHes immediately and invalidates both the
  admin list and the public `["diagram-stencils"]`, so the canvas picks the
  mapping up at once. A chosen class that isn't `is_diagram_placeable` shows an
  inline warning linking to its Feature Classes page (promotion would otherwise
  fail at placement). Write controls gate on `metadata:write`.
- Registered at `admin/stencils` in `app.tsx`; sidebar entry "Stencil
  promotion" (`Stamp` icon, `metadata:read`) in `app-sidebar.tsx`.

### Making the relay work end-to-end via the UI (no SQL)

1. Feature Classes → `PROTECTIVE_RELAY` → check **Can be placed on a diagram**
   → Save (flips `is_diagram_placeable = true`).
2. Stencil promotion → **Protective relay** row → pick *Protective relay
   (PROTECTIVE_RELAY)* (auto-saves).
3. On the diagram, double-click a stamped relay → the create form opens. (The
   Slice 1 `seed/ansi_stencils.sql` mapping is now just a convenience default
   for fresh installs; the editor is the source of truth.)

## Slice 3 — point features as ANSI glyphs on diagrams

Slices 1–2 make a *stencil* (decoration) promotable into a geometry-none feature.
Slice 3 is the complement the user actually asked for — *"I wish we could use
stencils for diagrams, especially point features and not lines; stencils make it
look better."* The lever is the same `render_context = 'diagram'` symbology rule
`PROTECTIVE_RELAY` already uses (Slice 1's "end-to-end today"): give each **point**
feature class a diagram rule pointing at an ANSI glyph, and its placements draw as
the electric symbol on diagrams while the **map is untouched** and **lines are left
alone**. The diagram canvas already prefers a diagram rule and falls back to the
map rule (`diagram-canvas.tsx`: `resolveSymbology(…,'diagram') ?? resolveSymbology(
…,'map')`), so this is **pure content — no code, no migration.**

### What shipped — `seed/diagram_symbology.sql` (new, manual `psql` apply)

- **Two new glyphs** in `metadata.symbology_icons` for the classes with no fitting
  ANSI symbol: `ansi-meter` (a dial/needle) and `ansi-street-light` (pole +
  luminaire head + rays). Same 24×24, upright, explicit-size, `{{COLOR}}`-tint
  convention as `seed/ansi_stencils.sql`; **not** added to
  `metadata.diagram_stencils` — these are feature symbology, not palette stencils
  (same as the original five shapes in `seed/symbology_icons.sql`).
- **Ten `render_context = 'diagram'` rules**, one per placeable point class (two
  subtype-scoped), each guarded by `NOT EXISTS` so re-apply never clobbers a
  hand-edited rule. Subtype-scoped rules join `metadata.subtypes` on the subtype
  `name` and insert `subtype_id`, so most-specific-wins picks them over the class
  default (mirrors the domain-scoped `DIST_SWITCH OPEN` map rule in
  `seed/symbology_example.sql`).

| Class | Scope | Glyph | Colour |
|---|---|---|---|
| DIST_XFMR | default | `ansi-transformer-2w` | `#2563eb` (its map blue) |
| DIST_SWITCH | default | `ansi-disconnect` | `#16a34a` (its map green) |
| DIST_SWITCH | `automatic_recloser` | `ansi-load-break` | `#16a34a` |
| SERVICE_CUTOUT | default | `ansi-fuse` | `#ea580c` |
| SERVICE_POINT | default | `ansi-terminal` | `#7c3aed` (its map violet) |
| LV_LINK_BOX | default | `ansi-node` | `#334155` |
| LV_BUSBAR | default | `ansi-terminal` | `#334155` |
| METER | default | `ansi-meter` *(new)* | `#0891b2` |
| METER | `ct_metered` | `ansi-ct` | `#0891b2` |
| STREET_LIGHT | default | `ansi-street-light` *(new)* | `#ca8a04` |

Each class keeps its map colour where it had one, so the *shape* changes without
disturbing colour identity. **POLE** (structural, `is_diagram_placeable = false`)
and **METER_CHAMBER** (`container` role, not placeable) are intentionally left on
their map shape — no schematic device symbol fits.

### Relation to promotion

Independent of Slices 1–2 but complementary: promotion turns a stamped glyph into
a geometry-none feature; Slice 3 makes *real point features* (which already exist
and are placed via the digitize/slot flows) render as the same electric glyphs.
Together the diagram reads as one coherent one-line whether a symbol arrived by
placement or by promotion.

### Verification owed (Bash blocked — run locally)

- Apply `seed/diagram_symbology.sql` after migrations + the point-class seeds +
  `seed/ansi_stencils.sql`: the two new `symbology_icons` rows exist; ten
  `render_context='diagram'` rules exist (query
  `metadata.symbology_rules WHERE render_context='diagram'`); re-applying is a
  no-op; guards hold if a class seed is absent.
- On a diagram: place (or promote onto) a DIST_XFMR, DIST_SWITCH, SERVICE_CUTOUT,
  SERVICE_POINT, METER, STREET_LIGHT → each draws as its ANSI glyph in the table
  colour; a `automatic_recloser` switch draws load-break, a `ct_metered` meter
  draws the CT glyph.
- The **map** view of the same features is unchanged (still circle/square/diamond/
  triangle); MV/LV **conductor** lines are unchanged on both map and diagram.
- `ansi-meter` and `ansi-street-light` render tinted (their `{{COLOR}}` resolves to
  the rule colour), upright, at the same size as the other ANSI glyphs.

## Decisions locked (from the design exchange)

- **Fixed mapping, confirm-only target** — the stencil's `promote_class` is the
  target; no per-promote class picker. (Overridable / "always pick" was offered
  and declined.)
- **Show the feature's form** — the create form (with required attributes) is
  shown at promotion time, reusing `FeatureCreateForm`, so the promoted feature
  is valid on creation rather than a bare shell filled in later.

## Deferred

- More `promote_class` mappings (needs geometry-none classes authored for fuse,
  recloser, cap bank, etc. — now point-and-click on the Stencil promotion page
  once the class exists, no SQL).
- **Link to an existing** feature instead of always creating a new one (the
  slot-bind name-search flow, as an alternate promote mode).
- A single-transaction `/promote` endpoint (only if the compose's non-atomicity
  ever bites — same trade-off the digitize/slot flows already carry).
- The reverse — **demoting** a placed feature back to a decoration glyph.
- Promotion for `is_diagram_placeable` classes that *do* have map geometry
  (would need a geometry-capture step; out of scope for diagram-native devices).

## Verification owed (Bash blocked — run locally)

- `migrate` applies `0051`; `metadata.diagram_stencils` has a nullable
  `promote_class` column.
- Apply `seed/ansi_stencils.sql`: `ansi-relay`'s `promote_class` =
  `'PROTECTIVE_RELAY'`; `PROTECTIVE_RELAY.is_diagram_placeable` = true;
  re-applying is a no-op; guards hold if the relay seed is absent.
- `go build ./...`; `GET /symbology/stencils` returns `promote_class` on
  `ansi-relay` and omits it elsewhere.
- `tsc` / lint / `vite build` clean.
- On a diagram: stamp the relay stencil, double-click it → create form opens
  targeted at Protective relay; Save → a real `PROTECTIVE_RELAY` placement
  appears where the glyph was (same rotation, same relay glyph) and the symbol
  is gone. Cancel leaves the symbol intact.
- Double-click a non-mapped glyph (e.g. a device-number circle) → nothing
  happens; hover a mapped glyph → the promote tooltip shows.
- RBAC: the compose's create/place/delete all require `features:write` /
  `diagrams:write` respectively — a read-only session can't promote.

### Slice 2 (admin editor)

- `go build ./...`; `GET /admin/symbology-stencils` returns the catalog with
  `promote_class`; `PATCH /admin/symbology-stencils/ansi-relay` with
  `{"promote_class":"PROTECTIVE_RELAY"}` succeeds; with a non-`none` class code
  → 400; with `{"promote_class":""}` clears it; unknown `icon_ref` → 404.
- `tsc` / lint / `vite build` clean; **Stencil promotion** appears in the Admin
  sidebar (`metadata:read`), glyphs render, the picker lists only geometry-none
  classes.
- End-to-end via UI, no SQL: enable `is_diagram_placeable` on `PROTECTIVE_RELAY`
  in Feature Classes → set its mapping on Stencil promotion → double-click a
  stamped relay on a diagram opens the create form. Picking a non-placeable
  class shows the inline warning.
- A `metadata:read`-only session sees the page read-only (picker disabled); the
  `PATCH` is rejected server-side.
