# Phase 24 — Diagram templates & schematic primitives

**Status: Slices 1 (element primitive / keystone) + 2 (symbol stencil library) + 3 (sheet furniture — title_block/legend) + 4 (templates save-as/apply + manual slot binding) + 5 (multi-segment bus routing — bus/tap) CODE-COMPLETE — verification owed (Bash blocked here). Phase 24 complete.**

## Why this phase exists

The diagram subsystem (Phase 5 + Phase 12) can only ever put a **real feature**
on a canvas: `runtime.diagram_placements` always points at a `feature_class_id`
/ `feature_id`. There is no way to draw diagram *furniture* — a box grouping a
bay, a free text note, a schematic-only tie line, a title block, a legend, or an
empty "put a transformer here" slot in a reusable layout.

"Diagrams need templates" turned out to mean four things at once — a **layout
skeleton** you apply and then fill in, a **clone** of an existing diagram, a
sheet of **furniture** (title block / legend / border), and **grouping /
annotation**. Three of the four need one shared thing that does not exist yet:
a **non-feature-backed diagram element** that lives in the diagram's own local
x/y space but is backed by nothing. That primitive is the keystone; the symbol
stencil library is a parallel content track that composes on top of it.

### Planned slices (dependency-ordered)

1. **Element primitive (keystone)** — `runtime.diagram_elements`. ✅ done
2. Symbol stencil library (ANSI schematic symbols) — parallel content track. ✅ done
3. Sheet furniture — title_block / legend kinds rendered. ✅ done
4. Templates — save-as-template / apply, with **manual** slot binding. ✅ done
5. Multi-segment bus routing (Phase 5 open item #1). ✅ done

Decisions locked before building: start at the **element primitive**; template
slots bind **manually** (applying a template drops empty, class-tagged slots and
the user clicks each to pick the real feature, reusing the existing
name-search / add-feature flow). Slots are model-ready in Slice 1 but their
binding UX lands in Slice 4.

## Slice 1 — the element primitive (keystone)

Delivers, standalone (no template machinery): **grouping/annotation boxes**
(Phase 5 open item #3) and **schematic-only lines** (open item #4), plus free
**text** labels.

### Data model — migration `0046_create_diagram_elements.sql`

`runtime.diagram_elements`, the non-feature-backed sibling of
`diagram_placements`:

- `id`, `diagram_instance_id` (FK → `diagram_instances`, **ON DELETE CASCADE**).
- `kind text CHECK (kind IN ('text','box','line','title_block','legend','slot'))`
  — all six in the constraint now, so introducing the later three needs **no
  follow-on migration**. Slice 1 renders only `text` / `box` / `line`.
- `x`, `y`, `width`, `height`, `rotation` as `numeric` (matching
  `diagram_placements` exactly — same local, abstract-not-geographic space);
  `width`/`height` nullable (box-like kinds only).
- `path jsonb` — a `line`'s `[[x,y],...]` polyline, same shape/reasoning as a
  placement's drawn path (not a PostGIS geometry).
- `text` — label content.
- `style jsonb NOT NULL DEFAULT '{}'` — heterogeneous rendering hints (stroke,
  dash, fill, colour, fontSize) kept out of the typed columns to avoid sprawl.
- `z_index integer NOT NULL DEFAULT 0` — draw order.
- Slot expectation (`kind='slot'`, all null otherwise): `slot_class` (the human
  **class code**, not an FK — keeps a saved template portable), `slot_subtype`,
  `slot_role`; plus `bound_class` / `bound_id` for the resolved feature once
  bound (polymorphic, FK-less, same reasoning as `diagram_placements.feature_id`).
- `created_at`, `updated_at`.
- Index on `diagram_instance_id`.

### Backend — `api/internal/diagram`

An element points at no feature, so — unlike `placeFeature` — there is **no**
`resolveFeature` / `is_diagram_placeable` gate; the only structural rules are a
valid `kind` (checked against a closed `elementKinds` set mirroring the CHECK)
and an x/y anchor. A `line` element requires a `path` of ≥2 points.

- `types.go`: `elementKinds` set; `elementRequest` (pointer fields so a PATCH
  distinguishes absent-from-null — nil leaves a field unchanged); `elementResponse`.
- `sql.go`: `DiagramElement` bun model; `createElement` / `updateElement`
  (fetch-then-mutate partial update, scoped by both element id **and** diagram
  id) / `listElements` (ordered `z_index, created_at`) / `removeElement`;
  `parseElementPath`, `validateStyle`, `toElementResponse` helpers. `style`
  defaults to `{}` before insert so the NOT NULL column never sees SQL NULL.
- `handler.go`: `POST/GET /diagrams/{id}/elements`,
  `PATCH/DELETE /diagrams/{id}/elements/{elementID}`.

**RBAC**: routes mount under the existing `/diagrams` tree, whose
`auth.RequirePermission(PermDiagramsRead, PermDiagramsWrite)` gate is
method-aware — GET → `diagrams:read`, POST/PATCH/DELETE → `diagrams:write`. **No
auth change needed.**

### Frontend — `web/src`

- `hooks/use-diagrams.ts`: `DiagramElement` / `DiagramElementKind` types;
  `useDiagramElements(diagramId)` (its own query key, separate from placements).
- `components/map/diagram-canvas.tsx`: fetch elements; render them **behind**
  placements (so a box frames the devices inside it); per-kind rendering —
  `box` (dashed rect, drag to move, bottom-right handle to resize, optional
  label), `text` (drag to move, double-click to edit in place via a
  `foreignObject` input, empty label deletes), `line` (drag to move as one
  piece). Optimistic drag/resize/rename + invalidate, same inline-`apiFetch`
  approach the placement mutations already use. New **Add annotation** header
  menu (Box / Text / Line); Line arms a click-to-draw vertex mode reusing the
  placement line-draw sketch UX (double-click / Finish / Esc), committing a
  `line` element.
- `components/map/remove-element-dialog.tsx`: the element analogue of
  `remove-placement-dialog.tsx` — same AlertDialog can't-dismiss-mid-request
  confirm guarding the hover-"×".

### Deferred out of Slice 1

Slot binding UX; `title_block` / `legend` rendering; template save/apply; bus
T-taps; z-order controls (elements render behind placements for now).

## Slice 2 — the symbol stencil library

Scope chosen: **both** — a non-feature stencil palette **and** ANSI content the
feature-symbology path can reuse. A "stencil" is one curated ANSI/IEEE
schematic glyph (ground, junction node, CT/VT, delta/wye winding, flow arrow,
…) a user stamps onto a diagram as a non-feature `symbol` element. It is
diagram furniture like a box/text/line — backed by no feature, so it never
appears in trace/connectivity — but carries its glyph in `style.iconRef`,
resolved against `metadata.symbology_icons` on the canvas exactly the way a
placement's feature icon already is.

### Data model

- **Migration `0047_add_symbol_element_kind.sql`** — widens
  `runtime.diagram_elements`'s kind CHECK (dropping/recreating the auto-named
  `diagram_elements_kind_check`) to add `'symbol'`. No new columns: a symbol's
  glyph lives in `style.iconRef`, its tint in `style.color`.
- **Migration `0048_create_diagram_stencils.sql`** — `metadata.diagram_stencils`,
  the palette catalog: `icon_ref` (PK, **FK → `metadata.symbology_icons` ON
  DELETE CASCADE**), `label`, `category`, `sort_order`. The SVG artwork is *not*
  duplicated here — the FK guarantees each stencil's glyph is the same
  `symbology_icons` asset a feature symbology rule can reference, which is what
  makes the content reusable by both surfaces.
- **`seed/ansi_stencils.sql`** (seed, not a migration — same as
  `seed/symbology_icons.sql`) — hand-authored ANSI glyphs written into
  `symbology_icons` (the markup) and `diagram_stencils` (the palette entries),
  both idempotent. Also idempotently repoints `PROTECTIVE_RELAY`'s
  `render_context='diagram'` rule from its placeholder `circle` to the real
  `ansi-relay` glyph (guarded to the untouched default), demonstrating the
  shared-asset reuse concretely.
  - **Post-Slice-5 seed expansion** (content only — no migration, no code): the
    seed grew from the original 16 glyphs to ~35, adding the common
    protection/switching set a real one-line needs — circuit breaker (draw-out),
    disconnect / load-break / grounding switches (`Switching` category),
    three-winding transformer, series `Components` (capacitor / reactor /
    battery / motor), and a `Device numbers` category of ANSI relay circles
    (50 / 51 / 87 / 67 / 27 / 59 / 81 / 79 / 86 / 25). Every new glyph is a
    24×24 `{{COLOR}}`-tintable upright SVG and — being a `symbology_icons` row —
    is equally reusable by a feature `render_context='diagram'` rule. Growing
    the library further stays a pure content edit to this one file.

### Backend

- `metadata.DiagramStencil` model + `ListDiagramStencils` (ordered category,
  sort_order, icon_ref).
- `GET /symbology/stencils` (in `cmd/server`, beside `/symbology/icons`, same
  `features:read` gate) — returns the catalog only (icon_ref/label/category/
  sort_order), no SVG; the palette resolves markup from the icon registry it
  already loads.
- `diagram` package: `'symbol'` added to `elementKinds`; `createElement`
  requires a non-empty `style.iconRef` for a symbol (`requireSymbolIconRef`), so
  a symbol never stamps blank.

### Frontend

- `use-diagram-stencils.ts` — `useDiagramStencils()`, a single long-cached
  fetch like `useSymbologyIcons`.
- `stencil-palette.tsx` — a header palette (beside the annotation menu):
  glyph thumbnails (tinted via `iconDataUri`) grouped by category; picking one
  calls `onPick(iconRef)`.
- `diagram-canvas.tsx` — `addSymbol(iconRef)` stamps a `symbol` element at the
  view centre (its x/y is the glyph's **centre**, like a placement). Rendered in
  the element layer: the glyph via `iconDataUri`, **drag** to move,
  hover-revealed **rotate** handle (snapped to `ROTATE_SNAP_DEGREES`, upright =
  0 — the element analogue of the placement rotate handle, via a new
  `ElementRotateState`), hover-"×" to remove.

### Deferred out of Slice 2

`title_block` / `legend` rendering (Slice 3); slot binding + template
save/apply (Slice 4); bus T-taps (Slice 5); an admin editor for the stencil
catalog (seed-authored for now, same as symbology icons); per-symbol colour
picker (defaults to a neutral slate); z-order (symbols render behind placements
with the other elements).

## Slice 3 — sheet furniture (title_block / legend)

Renders the two remaining "furniture" element kinds. **No migration and no
backend change**: `title_block` and `legend` were baked into the
`diagram_elements` kind CHECK in Slice 1 and into the backend `elementKinds`
set, so the server already accepts and round-trips them. This slice is purely
frontend rendering + the add-menu wiring — the last piece of "a sheet of
furniture" from the four things "templates" meant.

Both are ordinary box-like elements (x/y = **top-left**, `width`/`height` set),
so they reuse the existing element drag (`handleElementPointerDown`) and
bottom-right corner resize (`handleElementResizePointerDown`) handlers and the
hover-"×" remove — no new geometry code.

### Title block

A bordered key/value card. Its rows live in `style.fields` — a stable default
schedule (`Title`, `Drawing No.`, `Rev`, `Date`, `Drawn By`, `Sheet`), each
`{ label, value }`. Rendered as a vertical stack of rows with a label column and
a value column separated by a divider; each **value is double-click editable in
place** via a `foreignObject` input (new `editingField` state = `{ id, index,
value }`). Committing rebuilds `style.fields` with that one value replaced and
optimistically patches the whole `style` object (a single jsonb column, so a
value change rewrites it wholesale — the same shape `patchElement` already
round-trips). `addTitleBlock()` drops one at the view centre sized ~a third of
the view.

### Legend

A bordered card whose rows are **auto-derived** from what's actually on the
diagram, not stored — only its heading (`style.title`, default "Legend") lives in
the element. The `legendEntries` memo produces one row per distinct **non-line
feature class** present among `placements` (icon from that class's `diagram`
symbology rule, falling back to `map`; label = class name) followed by one row
per distinct **stamped stencil symbol** (`symbol` element `style.iconRef` → its
`iconDataUri` glyph + the stencil catalog label). It's keyed off `placements` /
`elements` (not their drag-live variants) so a legend never re-derives
mid-drag. The card shows as many rows as fit its height, a muted `+N more` when
it overflows, and an empty-state line when the sheet is bare. `addLegend()`
drops one at the view centre. Curation (hide/reorder rows) and line-class /
conductor rows are deferred.

### Menu wiring

`AddAnnotationMenu` gains a divider then **Title block** and **Legend** entries
(new `onAddTitleBlock` / `onAddLegend` props, `Table` / `List` icons), wired to
`addTitleBlock` / `addLegend` in the header.

### Deferred out of Slice 3

Legend row curation / manual ordering and conductor (line-class) legend rows;
title-block field schema editing (add/remove/rename rows — only values are
editable now); slot binding + template save/apply (Slice 4); bus T-taps (Slice
5); z-order (furniture renders behind placements with the other elements).

## Slice 4 — templates: save-as / apply, with manual slot binding

The payoff slice: "diagrams need templates" as a **layout skeleton you apply and
fill in**. It reuses everything the earlier slices built — a template is just a
saved set of `diagram_elements` — so the only genuinely new idea is the **slot**,
the empty class-tagged placeholder from Slice 1's model that finally gets minted,
rendered, and bound here.

### What a template is

A **template** is a reusable skeleton captured from a real diagram. Saving one
walks the source diagram and:

- turns every **placement** into an empty `slot` (kind = `slot`), keeping its
  x/y/rotation/path and recording its class as `slot_class` — but **dropping the
  feature id**. A template is portable: it says "a `TRANSFORMER` goes here", not
  "feature 42 goes here", so it binds to different real features each time.
- copies every **element** (box / text / line / symbol / title_block / legend,
  and any pre-existing slot) **verbatim**.

The whole skeleton is one opaque `jsonb` payload — templates are written whole
and read whole, never queried element-by-element, so a normalised child table
would buy nothing.

### Data model — migration `0049_create_diagram_templates.sql`

`runtime.diagram_templates`: `id`, `name`, `description`, `payload jsonb`
(`{ "elements": [ …diagram_element shapes… ] }`), `created_at`. In **runtime**,
not metadata — a template is user-authored at runtime from real diagrams, not
admin-compiled config like `metadata.diagram_stencils`. Slots inside the payload
carry `slot_class` as the portable human class code, matching
`diagram_elements.slot_class` (already model-ready since migration 0046 — **no
schema change to `diagram_elements` was needed**).

### Backend — `api/internal/diagram/templates.go`

- `DiagramTemplate` bun model + `templateElement` / `templatePayload` shapes.
- `saveAsTemplate(diagramID, {name, description})` — verify the diagram, read its
  placements + elements, build the payload (placements → slots, elements copied),
  insert one row. Returns a summary (name, element/slot counts).
- `listTemplates()` — the whole library, newest first; unmarshals each payload to
  count slots/elements for its summary (cheap for a hand-authored library).
- `applyTemplate(diagramID, templateID)` — read the template, insert every payload
  entry as a `diagram_elements` row on the target diagram in **one** statement
  (all-or-nothing), slots unbound. **Additive** — existing diagram content is
  untouched.
- `deleteTemplate(templateID)` — remove one from the library; applied copies
  already live as their own rows, so deleting a template never touches a diagram.

Routes (in `handler.go`, under the existing `/diagrams` mount, so the same
method-aware `RequirePermission(diagrams:read, diagrams:write)` gate applies —
GET → read, POST/DELETE → write, **no auth change**):
`POST /diagrams/{id}/save-as-template`, `POST /diagrams/{id}/apply-template`,
`GET /diagrams/templates`, `DELETE /diagrams/templates/{templateID}`. The static
`/templates` sits beside the `/{id}` param route — chi resolves the static
segment first, so a diagram id never shadows it.

### Manual slot binding — the key decision

Binding **materialises a real placement and consumes the slot**, rather than
recording the bound feature on the slot element (the `bound_class`/`bound_id`
columns stay reserved/unused for now). Rationale: a bound feature must be
*first-class* — it has to join trace/connectivity — which only a real
`diagram_placement` does; a slot is a non-feature element that never would. So
binding is a pure frontend composition of two endpoints that already exist:
`POST …/placements` at the slot's own position, then `DELETE …/elements/{slot}`.
No bespoke bind endpoint. This makes slot-binding a one-way "fill in the blank",
exactly right for a skeleton.

### Frontend — `web/src`

- `hooks/use-diagram-templates.ts` — `useDiagramTemplates()` (the library list;
  not long-cached — the save/apply/delete mutations invalidate its key).
- `diagram-canvas.tsx`:
  - **`TemplatesMenu`** header popover (beside the stencil palette): a **save**
    row (name → `POST save-as-template`, `pushSuccess`) and an **apply** list
    (each template shows "N slots · M elements"; click → `POST apply-template` →
    invalidate elements; hover-trash deletes).
  - **Slot render branch** — an unbound slot draws as a dashed placeholder marker
    at its centre with a "+" and its `slot_class` label; clicking it (in select
    mode) opens the binding picker; hover-"×" discards it.
  - **`SlotBindPanel`** overlay — the exact `useFeatureNameSearch` fan-out
    `AddFeatureButton` uses, **filtered to the slot's `slot_class`**; picking a
    feature calls `bindSlot`, which does the place-then-delete above and refetches
    both placements and elements. Esc cancels.

### Deferred out of Slice 4

Capturing a slot's `slot_subtype` / `slot_role` from the source feature (slots
are class-only for now); repositioning an **unbound** slot (bind first, then move
the real placement); the `bound_class`/`bound_id`-on-element binding mode; a
template rename / preview / thumbnail; applying a template with an origin offset
(it drops at the stored coordinates); bus T-taps (Slice 5); z-order.

## Slice 5 — multi-segment bus routing (bus / tap)

Closes the oldest open item on the diagram canvas — **"multi-segment bus
routing, not just a single-bend elbow"** (`docs/phase-5-diagrams.md` "What's
still open", `docs/open-items.md`). Real one-line / protection diagrams route
wires off a shared busbar with **T-junction taps**, not just point-to-point
right-angle elbows between exactly two devices. The existing `line` element is
already a schematic-only polyline, so this slice adds the two *routing* kinds
that the schematic convention needs on top of it.

### What a bus and a tap are

- **bus** — a heavy busbar: the same stored `[[x,y],…]` polyline a `line`
  carries, but drawn thick with rounded joins, so a shared bus reads as the
  spine devices tap onto. Drawn as a **multi-segment orthogonal run** (each
  vertex after the first locks to a right angle off the previous — `orthoSnap`).
- **tap** — a thin right-angle stub off a bus: routed orthogonally like the bus,
  rendered with a **filled T-junction dot at its last vertex** (its bus end),
  which is the T where it meets the busbar.

Both are pure furniture like `line` — backed by no feature, never in
trace/connectivity — differing only in how they render.

### Data model — migration `0050_add_bus_tap_element_kinds.sql`

Widens the `diagram_elements` kind CHECK to add `'bus'` and `'tap'` (drop +
recreate the auto-named `diagram_elements_kind_check`, exactly as `0047` did for
`'symbol'`). **No new columns** — both are line-shaped, `path`-carrying
elements; `x/y` anchor at the first vertex, `style.stroke` colours them.

### Backend — `api/internal/diagram`

- `types.go`: `'bus'` / `'tap'` added to the `elementKinds` set; a new
  `pathKinds` set (`line` / `bus` / `tap`) names the kinds whose geometry *is* a
  polyline.
- `sql.go`: the create-time "a line needs a ≥2-point path" rule generalises to
  `pathKinds[req.Kind]` — a bus or tap with no path (or a 1-point path) is a 400,
  same as a line. No new endpoint, no other change.

### Frontend — `diagram-canvas.tsx`

- **Type**: `'bus'` / `'tap'` added to `DiagramElementKind`.
- **Drawing**: the old `drawingElementLine` boolean becomes an
  `elementDrawKind` (`"line" | "bus" | "tap" | null`) — one armed drawing state,
  three kinds. `line` takes each click as-is (unchanged); `bus` / `tap` route
  **orthogonally** — `orthoSnap(from, next)` locks each new vertex (and the
  rubber-band preview) to horizontal or vertical off the previous one.
  `finishElementLine` commits the sketch as whatever kind is armed.
- **Menu**: `AddAnnotationMenu` gains **Bus** (`Minus` icon) and **Tap**
  (`Waypoints` icon) below Line; the draw banner shows kind-specific guidance
  ("Click to route the bus…" / "Click the device, then the bus…").
- **Render**: a `bus` branch (heavy rounded polyline) and a `tap` branch (thin
  polyline + junction dot at the last vertex), both reusing the existing element
  drag (`handleElementPointerDown`) and hover-"×" remove — no new geometry
  handlers.

### Deferred out of Slice 5

Auto-projecting a tap's foot point onto the nearest bus (the endpoint is placed
by eye with orthogonal locking, not snapped to a bus segment); a bus/tap
re-following a device or bus that later moves (they're independent furniture,
like every other element); editing an individual vertex of a committed bus (move
the whole run, or delete and redraw); dual power-vs-control line semantics
(arrowheaded dashed trip lines — a separate open item); z-order. With this slice
the Phase 5 diagram-canvas open items (#1 bus routing, #3 grouping boxes, #4
schematic-only lines) are all delivered.

## Slice 6 — built-in "Double busbar" template + any-class slots

Everything for a reusable double-busbar arrangement already existed after Slices
2/4/5 — `bus`/`line` primitives, ANSI `symbol` glyphs (disconnect selectors, a
coupler breaker), `text` labels, and `slot` placeholders bound through
`applyTemplate`. A double busbar is therefore not new machinery: it is one more
row in `runtime.diagram_templates` whose payload encodes the arrangement. So this
slice ships **no new endpoint and no new canvas primitive** — a seed file
(`seed/double_busbar_template.sql`, applied by the `seed-double-busbar` task) that
inserts the template, plus one small generalisation to the slot bind flow.

**The template.** Two heavy `bus` lines (Bus A at y=140, Bus B at y=340), three
feeder-bay risers and one bus-coupler riser (`line`), per-bay bus-selector
disconnect glyphs + a bay breaker + the coupler breaker (`symbol`, referencing the
`ansi-disconnect` / `ansi-breaker` stencils from `seed/ansi_stencils.sql`), `text`
labels (Bus A/B, Bay 1–3, Coupler), and three empty `slot`s for the feeders.
`z_index` layers it (buses/risers 0, glyphs 10, slots 20, labels 30). It is purely
schematic — no electrical or connectivity meaning until a bay slot is bound to a
real feeder feature (which then joins trace/connectivity like any placement). The
insert is idempotent on the template name.

**Any-class (untyped) slots.** A portable built-in template can't know a given
deployment's feeder class code, so the bay slots carry **no `slot_class`**. Until
now an untyped slot was unbindable: `SlotBindPanel` filtered results to
`featureClassCode === slot_class` (empty ⇒ nothing matched) and `bindSlot`
early-returned without a `slot_class`. Slice 6 generalises both — an untyped slot
skips the class filter and offers every feature, and `bindSlot` takes the class
from the *picked* feature (`onBind(featureId, featureClass)`; `boundClass =
slot.slot_class ?? featureClass`). A class-tagged slot is unchanged. This makes
"a placeholder for whatever feeder you have" a first-class concept, not a
double-busbar special case.

**Apply moved to diagram creation.** A template is a *starting skeleton*, so it is
now offered only when a diagram is created, never layered onto an existing one. The
Slice 4 in-canvas "Apply a template" list is removed; `CreateDiagramForm` gains an
optional **Start from template** picker (`create-diagram-form.tsx`) — on create it
POSTs `/diagrams/{id}/apply-template` before opening the new diagram (best-effort:
a template failure toasts but the blank diagram still opens). The canvas
`TemplatesMenu` keeps only what must act on the *current* diagram — **Save as
template** — plus a read-only library list you can prune (delete). The
`applyTemplate` endpoint itself is unchanged; only its single UI entry point moved.

### Deferred out of Slice 6

Modelled bus-selection semantics (which bus a bay is on, coupler open/closed, and
traces that respect them) — explicitly out of scope: this slice is the *schematic*
fork the user chose, not the network-model one. Promoting the decorative
selector/breaker glyphs into real switchgear features (they're plain `symbol`s;
the Phase 25 stencil→feature promotion path already exists for glyphs that map to a
class). A dedicated stencil/"insert" macro for the arrangement (the template +
apply flow already delivers the drop). Snapping bay risers to the exact bus
segment.

## Verification owed (Bash blocked in this environment)

**Slice 1**
- `migrate` applies `0046`; table + CHECK + index present.
- API: `go build ./...`; curl create/list/patch/delete an element; bad `kind`
  → 400; `line` without a ≥2-point path → 400; element on a wrong diagram id
  → 404; element cascades away when its diagram is deleted.
- Web: `tsc` / lint / build; box add/move/resize/label/delete; text
  add/move/edit/empty-deletes; line draw/move/delete; annotations sit behind
  placements; RBAC hides mutation controls without `diagrams:write`.

**Slice 2**
- `migrate` applies `0047` (kind CHECK now includes `symbol`) + `0048`
  (`metadata.diagram_stencils`, FK to `symbology_icons`).
- Apply `seed/ansi_stencils.sql`: ~35 rows land in both tables (the original 16
  plus the post-Slice-5 protection/switching batch); re-applying is a no-op; if
  `PROTECTIVE_RELAY` seed present, its diagram rule now shows `ansi-relay`. The
  new `Switching` / `Components` / `Device numbers` categories appear in the
  stencil palette, each glyph tinting and stamping like the originals.
- API: `go build ./...`; `GET /symbology/stencils` returns the catalog;
  `POST …/elements {kind:"symbol", x,y, style:{iconRef}}` succeeds; a `symbol`
  with no `style.iconRef` → 400.
- Web: `tsc` / lint / build; palette opens grouped by category with tinted
  thumbnails; picking a stencil drops a symbol at view centre; drag moves it;
  hover rotate handle rotates (snapped); hover-"×" removes; empty palette when
  catalog unseeded shows the empty state (no crash); RBAC still hides the
  palette's mutations without `diagrams:write`.

**Slice 3** (no migration / no API change — kinds already accepted)
- Web: `tsc` / lint / build.
- Add annotation menu now lists **Title block** and **Legend** below a divider.
- Title block: drops at view centre with the default row schedule; drag by the
  border moves it, corner handle resizes, hover-"×" removes; double-click a
  value cell opens an inline input; Enter/blur persists (reload keeps it),
  Escape cancels; the value patch survives a `GET` round-trip as
  `style.fields`.
- Legend: drops at view centre; auto-lists a row per distinct non-line feature
  class on the sheet (correct icon + class name) and per distinct stamped
  stencil (glyph + catalog label); adding/removing a placement or symbol
  updates the rows; overflow shows `+N more`; a bare sheet shows the
  empty-state line; drag/resize/remove behave like the title block.
- RBAC: both add entries and all furniture mutations stay hidden/blocked
  without `diagrams:write` (they inherit the same `/diagrams` gate).

**Slice 4**
- `migrate` applies `0049` (`runtime.diagram_templates`); no change to
  `diagram_elements` (slot columns already existed from `0046`).
- API: `go build ./...`.
  - `POST /diagrams/{id}/save-as-template {name}` on a diagram with N placements
    + M elements → a template whose payload has N slots + M copied elements;
    empty name → 400; unknown diagram id → 404.
  - `GET /diagrams/templates` lists it with `slot_count`/`element_count` right.
  - `POST /diagrams/{id}/apply-template {template_id}` inserts N+M
    `diagram_elements` rows on the target (slots unbound, furniture as-is),
    leaving existing content untouched; unknown template/diagram → 404; missing
    `template_id` → 400.
  - `DELETE /diagrams/templates/{tid}` removes it; re-delete → 404. Confirm
    `GET /diagrams/templates` (static) is not shadowed by `GET /diagrams/{id}`.
- Web: `tsc` / lint / build.
  - Templates menu: type a name → Save → toast + the template appears in the
    apply list; the list survives a reopen (invalidation works).
  - Apply a template onto a blank diagram → its slots + furniture appear; the
    diagram's own prior content is still there (additive).
  - A slot renders as a dashed "+" marker tagged with its class; clicking it
    opens the picker scoped to that class only; picking a feature makes a real
    placement appear at the slot and the slot vanishes; Esc / × cancel/discard;
    binding a class with no matching feature shows the empty state.
  - Hover-trash deletes a template from the list.
  - RBAC: the whole Templates menu + slot binding stay blocked without
    `diagrams:write`.

**Slice 5**
- `migrate` applies `0050` (kind CHECK now includes `bus` + `tap`); no change to
  `diagram_elements` columns.
- API: `go build ./...`.
  - `POST /diagrams/{id}/elements {kind:"bus", x, y, path:[[…],[…]]}` and the
    same for `tap` succeed; a `bus`/`tap` with no `path` (or a 1-point path)
    → 400, same message shape a bad `line` gets.
  - `GET /diagrams/{id}/elements` round-trips both kinds; they cascade away with
    the diagram.
- Web: `tsc` / lint / build.
  - Add annotation menu lists **Bus** and **Tap** below Line.
  - Bus: arm → banner shows the bus guidance; each click drops a right-angle
    vertex (the rubber-band previews the orthogonal lock); double-click / Finish
    commits a heavy busbar; Esc / Cancel drops the sketch.
  - Tap: arm → click a device point then a bus point; commits a thin stub with a
    filled T-junction dot at its bus end.
  - Both drag as one piece and hover-"×" removes them (RemoveElementDialog);
    they render behind placements with the other furniture.
  - RBAC: Bus/Tap add + their mutations stay blocked without `diagrams:write`
    (same `/diagrams` gate).

**Slice 6** (no migration / no API change — reuses `applyTemplate` + slot bind)
- Apply `seed/ansi_stencils.sql` (for the glyphs) then
  `seed/double_busbar_template.sql` (or `task seed-double-busbar`): one row lands
  in `runtime.diagram_templates`; `payload` parses as valid jsonb; re-applying is
  a no-op (guarded on name); `GET /diagrams/templates` lists **Double busbar**
  with `slot_count = 3` and `element_count = 27`.
- Apply happens at **diagram creation**: on a container feature with no diagram
  yet, the create form shows **Start from template** → pick "Double busbar" →
  Create. The new diagram opens already carrying two heavy buses (A/B), three
  feeder risers + a coupler riser, the disconnect/breaker glyphs (tinted, correct
  positions), the Bus A/B + Bay + Coupler labels, and three dashed "+" bay slots —
  layered correctly (risers behind glyphs behind slots/labels). Picking "Blank
  diagram" opens an empty one.
- The in-canvas Templates menu no longer applies: it saves the current diagram and
  lists saved templates for deletion only (no apply-on-click). RBAC still gates all
  of it on `diagrams:write`.
- Any-class slot: click a bay slot → picker opens **unfiltered** (header/placeholder
  read "feature", not a class); typing a name lists features of *any* class;
  picking one materialises a real placement at the slot (bound to that feature's
  class) and the slot vanishes; Esc / × cancel.
- Regression: a **class-tagged** slot (from a user-authored template) still filters
  to its own class and binds exactly as before.
- Web: `tsc` / lint / build; RBAC still gates the whole flow on `diagrams:write`.
