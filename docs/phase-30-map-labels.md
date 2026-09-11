# Phase 30 — Map labelling (per-class, its own editor)

Text labels on geographic-map features, **one label config per feature class**,
authored in a dedicated **Labelling** editor (not the symbology rule dialog).
A class whose config has a non-empty **template** draws that template — literal
text plus `{field}` placeholders resolved against each feature's own tile
properties — on every feature; a class with no config (or an empty template)
draws nothing. Map only; the diagram has its own per-placement label toggle
(`label_hidden`, migration 0054).

## Why this shape

- **One config per class, its own dialog** (user's choice). Labelling is a
  single class-wide decision, orthogonal to which symbology rule a feature
  matches, so it is NOT a per-rule `style_def` key — it lives on the feature
  class itself and has a focused editor.
- **Template expression** (user's choice): `{name} ({voltage} kV)` mixes
  attributes and literal text. Missing/blank fields collapse out (whitespace
  is coalesced), so a partial record reads cleanly and an all-empty template
  yields no label at all.
- **Map only.** The label config drives the OpenLayers vector-tile style; the
  diagram is untouched.

## Storage — `label_def` jsonb on `metadata.feature_classes`

Migration **0055** adds a nullable `label_def jsonb` column. Raw passthrough
end to end (like `symbology_rules.style_def`): the bun `FeatureClass` model
carries `LabelDef json.RawMessage`, the schema endpoint emits it untouched, and
the frontend reads it as a typed `LabelDef`. NULL / empty template = no labels
(every existing class). No compiler involvement — the DDL builder never
enumerates feature_classes columns, and `metadata.Load` selects by struct, so
the new column just rides along.

`LabelDef` shape (all optional except `template`, whose presence is the
on-switch):

| key          | type    | default   | meaning                              |
| ------------ | ------- | --------- | ------------------------------------ |
| `template`   | string  | —         | literal text + `{field}` placeholders |
| `size`       | number  | 12        | font size px                          |
| `color`      | string  | `#1f2937` | text fill                             |
| `bold`       | boolean | false     | font weight                           |
| `italic`     | boolean | false     | font slant                            |
| `halo`       | boolean | true      | legibility outline on/off             |
| `halo_color` | string  | `#ffffff` | outline color                         |
| `halo_width` | number  | 3         | outline width px                      |
| `min_zoom`   | number  | —         | floor below which the label is hidden |

## Backend

- **migrations/0055_add_feature_class_label_def.sql** — `ALTER TABLE
  metadata.feature_classes ADD COLUMN label_def jsonb`.
- **api/internal/metadata/models.go** — `FeatureClass.LabelDef json.RawMessage
  bun:"label_def"`.
- **api/cmd/server/main.go** — `featureClassSchemaResponse.LabelDef
  json.RawMessage json:"label_def,omitempty"`, set from
  `loaded.FeatureClass.LabelDef` in `featureClassSchemaHandler`.
- **api/internal/admin/types.go** — `updateFeatureClassRequest.LabelDef
  *json.RawMessage`.
- **api/internal/admin/update.go** — when `req.LabelDef != nil`, writes the
  `label_def` column; a JSON `null` (or empty) clears it to SQL NULL, a JSON
  object sets it. Rides the existing `PATCH /admin/feature-classes/{code}`
  (gated `metadata:write`). `validateUpdateFeatureClass` allows a label-only
  request (it only constrains name + the geometry trio).

## Frontend

- **web/src/lib/symbology.ts** — new `LabelDef` interface; the old per-rule
  `label_*` keys were removed from `StyleDef`.
- **web/src/lib/map-layers.ts** — `createSymbologyStyle(rules, view, icons,
  labelDef?)`. It builds the geometry style (resolved symbology, else plain
  fallback) then, independently, appends a **second** cached `Text` style when
  `labelDef.template` is set, isn't zoom-gated, and `renderLabelTemplate`
  yields non-empty text. `labelStyleFor` keys the cache on the full appearance
  (font incl. bold/italic, halo, placement); `renderLabelTemplate` substitutes
  `{field}` from tile props and collapses whitespace. Because labelling is
  class-wide, a feature with no matching symbology rule can still be labelled.
- **web/src/hooks/use-feature-class-schema.ts** — `FeatureClassSchema.label_def?:
  LabelDef`.
- **web/src/hooks/use-all-symbology.ts** — new `useAllLabelDefs()` (companion
  to `useAllSymbology`, same warm schema cache) → `Record<code, LabelDef>`.
- **web/src/components/map/voltedge-map.tsx** — passes
  `labelDefsByClassCode[classCode]` into each layer's `createSymbologyStyle`.
- **web/src/routes/admin-feature-class-detail.tsx** — new `LabelSection`
  (shows the current template / "No label" + Add/Edit) and `LabelForm` dialog:
  template input + insert-field dropdown (built-in `name` in its own optgroup,
  since it's a gis column published as a tile prop but not in `schema.fields`,
  plus the user-defined fields) + live styled preview, size, color,
  bold, italic, min-zoom, and halo (on/off + color + width). Saves via `PATCH
  /admin/feature-classes/{code}` `{ label_def: {...} | null }`, invalidating
  `["feature-class-schema", code]` (refreshes both this page and the map).
- **web/src/components/admin/form-dialog.tsx** — the shared admin dialog body
  now caps at `70vh` and scrolls (footer pinned), so tall forms never run off
  the bottom of the centered dialog.

## Per-layer label toggle (follow-up)

The template on/off above is an authoring decision (admin, persisted per
class). A **viewer-side** toggle lets a map user hide/show labels per layer
without touching the class config, from the same Layers/Legend control:

- **web/src/stores/layers-store.ts** — new `labelsHidden: Record<sourceId,
  boolean>` + `toggleLabels`, persisted alongside `hidden` (same
  exception-only convention: absent = labels shown). Orthogonal to `hidden`,
  so a layer can be visible with labels off.
- **web/src/components/map/layers-panel.tsx** — each feature-class row that
  actually **has** a label template (`useAllLabelDefs()[CODE]?.template`)
  shows a `Tag` button; clicking it flips `toggleLabels(id)` (dimmed when
  off). Rows for classes with no template show no toggle (nothing to hide).
- **web/src/components/map/voltedge-map.tsx** — a `labelsHiddenRef` seeds each
  layer's style at creation (passing `undefined` for the label def when off,
  so it never flashes labels on first paint), and a dedicated effect reacts to
  `labelsHidden` with an in-place `layer.setStyle(...)` (passing the label def
  only when on) — a cheap re-style, not a tile re-request, mirroring the
  existing per-layer visibility effect.

## Label placement — offset + rotation (follow-up)

User asked whether a feature's map label can be **moved / rotated / expanded**.
"Expand" is the existing `size`; **move** and **rotate** were added here as three
more class-wide `label_def` keys (per-*class*, the user's choice — every feature
of a class shares the placement, edited in the admin form; there is no
map-canvas dragging, which would need per-instance override storage that does
not exist for map features — only `runtime.diagram_placements` has it).

`label_def` is schema-less jsonb passed through raw end to end, so **no
migration and no Go/API change** — the new keys just ride along:

| key        | type   | default | meaning                                            |
| ---------- | ------ | ------- | -------------------------------------------------- |
| `offset_x` | number | 0       | px shift from the anchor, x right-positive         |
| `offset_y` | number | 14      | px shift, y down-positive; overrides the default drop |
| `rotation` | number | 0       | text rotation, degrees clockwise                   |

Point/polygon labels only — a **line** label rides the line, where OL ignores
offset and rotation fights `placement:"line"`, so the renderer collapses all
three to 0 there.

- **web/src/lib/symbology.ts** — `LabelDef` gains `offset_x?` / `offset_y?` /
  `rotation?`.
- **web/src/lib/map-layers.ts** — `labelStyleFor` reads them (gated on
  `placement === "point"`): `offsetX`, `offsetY` (with `def.offset_y` overriding
  the previously-hardcoded 14), and `rotation` (deg → rad) on the OL `Text`; all
  three folded into the style cache key (the previous key already carried
  `pointOffsetY`).
- **web/src/routes/admin-feature-class-detail.tsx** — the Label form's
  `LabelFields` / `labelFieldsFrom` / `toLabelDef` gain `offsetX` / `offsetY` /
  `rotation`, plus an **Offset X / Offset Y / Rotation°** input row (Offset Y
  placeholder 14; a "point/polygon only" hint) and a `rotate()` transform on the
  live preview. Same "only write non-default keys" discipline (`offset_x` /
  `rotation` drop a 0, but `offset_y` writes even 0 so an author can pin a label
  onto the glyph rather than below it).

## Verification owed (Bash blocked here)

- web: `tsc --noEmit`, lint, `vite build`.
- api: `go build ./...`, `go vet ./...`, `gofmt -l`.
- migrate: run 0055.
- manual: set a class template like `{name}`, confirm it draws on the map,
  respects `min_zoom` + bold/italic/halo, clears when the template is emptied,
  and never appears on the diagram.
- manual (per-layer toggle): a labelled class's Layers-panel row shows the
  `Tag` toggle; turning it off hides that class's labels on the map (geometry
  stays), leaves other classes' labels alone, and survives a reload
  (persisted). A class with no template shows no toggle.
- manual (placement): on a labelled point/polygon class, set Offset X/Y and
  Rotation in the Label form → labels shift and spin on the map for every
  feature of that class; Offset Y = 0 pins the label onto the glyph, blank
  restores the 14px drop; confirm a line class ignores all three (label still
  rides the line); clearing them (blank) restores the default placement.
