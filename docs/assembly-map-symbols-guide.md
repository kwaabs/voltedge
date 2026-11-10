# Giving a Placed Assembly Its Own Map Symbol

_Last updated: 2026-11-04_

*How to make an assembly — a packaged substation, an RMU, a pole-mounted
transformer — wear its own glyph on the **geographic map**, without turning the
assembly into a feature class.*

---

## The idea in one paragraph

An assembly is **several real feature classes placed together** (a geometric
*anchor* plus geometry-less components wired inside it). Only the anchor draws on
the map, and it keeps its own class and symbology. To give the *whole placed
assembly* a distinct map glyph, you don't make a new class — you **mark the
anchor** and **style by that mark**. There are two ways to do it, and they solve
two different problems:

| Path | Styles by | Good for | Needs |
|---|---|---|---|
| **A — Kit "Map symbol"** | the assembly marker (`assembly_ref`) | one symbol *per kit*, applied as you place | migration 0192 + recompile |
| **B — By-attribute rule** | a real column the anchor carries (e.g. `package_config`) | styling by what the unit *is*, existing units included | nothing extra |

Both live on the **`map` render context**, so your diagrams / one-lines are never
touched. Pick A when "this came from *that kit*" is the thing you want to show;
pick B when a real attribute (config, voltage class, condition) already says it.

> **Before anything shows up:** the icon you choose must be a registered **map
> symbology icon** (Admin → Symbology → Icons), *not* a diagram stencil. A stencil
> of the same name lives in a different catalog the map can't read, and the map
> will silently fall back to a plain circle. This is the single most common reason
> "I set a symbol and nothing changed."

---

## ▶ Path A — a symbol per kit (the "Map symbol" field)

Use this when you want **every unit placed from a given kit** to wear the same
glyph. One setting on the kit, applied at placement *and* backfilled onto units
already placed as that kit.

### Prerequisites (one-time)

`assembly_ref` is a marker column the fan-out stamps on the anchor at placement.
For the map to *match* on it, the column must exist **and** be published as a map
tile property:

1. Apply migration **0192** (adds `assembly_ref` to every feature table + rebuilds
   the tile functions).
2. **Recompile the anchor class** so the column becomes a tile property, e.g.
   `task compile CODE=PACKAGED_SUBSTATION` and `task compile CODE=RMU`.

Skip this and the rule is authored fine but never matches — the map can't see a
column that isn't in the tile.

### Steps

1. **Admin → Assemblies** and open the kit (e.g. *"Packaged substation — compact
   (permanent)"*, or *"Ring main unit — 2 ring + 1 transformer"*).
2. In the editor find **Map symbol (optional)**:
   - **Icon** — pick from the dropdown. These are the registered map icons; if your
     artwork isn't there, add it first (see *Registering an icon* below).
   - **Colour** — a hex colour (used to tint a `{{COLOR}}`-templated icon; a
     full-colour SVG ignores it). The live swatch shows the result.
3. **Save.** Two things happen:
   - A managed `map`-context rule is written for the kit, keyed on
     `assembly_ref = <kit code>` (e.g. `assembly_ref = 'COMPACT_PERMANENT'`).
   - Units **already placed** as that kit are stamped too (a safe backfill — it
     only touches the kit's own anchor class + subtype, and only where
     `assembly_ref` is still empty).
4. **Hard-reload the map** (Ctrl+Shift+R). Every placed unit of that kit now wears
   the glyph; so does every new one you place.

### What you get

- Place *"Packaged substation — compact (permanent)"* → the enclosure is stamped
  `assembly_ref = 'COMPACT_PERMANENT'` → it draws your packaged-station glyph.
- Place *"Ring main unit — 2W_1T"* → stamped `assembly_ref = '2W_1T'` → your RMU
  glyph.

### Gotcha this path *doesn't* cover

A unit placed **before** the kit's marker machinery existed has no `assembly_ref`
and can't be matched by it. The save-time backfill fixes the common case
automatically, but if a unit was created by hand (not via the kit) on a `(class,
subtype)` that *another* kit also claims, the backfill skips it to avoid
mislabelling. For those, use **Path B**.

---

## ▶ Path B — a rule on a real attribute

Use this when a column the anchor already carries *is* the thing you want to style
by — the cleanest choice for **units already on the map**, because it needs no
marker, no backfill, no recompile. A packaged substation always carries
`package_config`; an RMU always carries `rmu_config`.

### Option B1 — in the UI

1. **Admin → Feature Classes → Packaged Substation → Symbology → Add rule.**
2. Set:
   - **Render context:** `Map`
   - **Match field:** `package_config` · **value:** `COMPACT`
   - **Icon:** `voltedge-packaged-station-rmu-dist-xfmr-lv-feeder-pillar`
   - **Colour:** `#2563eb`
3. **Save** and hard-reload the map. Every `COMPACT` packaged substation — existing
   and future — draws the glyph immediately.

### Option B2 — as SQL (idempotent seed)

Handy when you want it in version control / applied the same way as a migration.
This is exactly what `seed/packaged_substation_symbology.sql` does — a mandatory
class **default** plus a `package_config = COMPACT` override:

```sql
-- 1. Mandatory class default (every class must have one rule with subtype_id AND
--    domain_value_id both null; this is why an unmatched unit shows a plain glyph).
INSERT INTO metadata.symbology_rules (feature_class_id, render_context, style_def)
SELECT fc.id, 'map', '{"icon_ref": "square", "color": "#2563eb"}'::jsonb
FROM metadata.feature_classes fc
WHERE fc.code = 'PACKAGED_SUBSTATION'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.symbology_rules
    WHERE feature_class_id = fc.id AND subtype_id IS NULL
      AND domain_value_id IS NULL AND render_context = 'map'
  );

-- 2. package_config = COMPACT -> the packaged-station glyph.
INSERT INTO metadata.symbology_rules (feature_class_id, domain_value_id, render_context, style_def)
SELECT fc.id, dv.id, 'map',
       '{"icon_ref": "voltedge-packaged-station-rmu-dist-xfmr-lv-feeder-pillar", "color": "#2563eb"}'::jsonb
FROM metadata.feature_classes fc
JOIN metadata.fields f         ON f.feature_class_id = fc.id AND f.name = 'package_config'
JOIN metadata.domain_values dv ON dv.domain_id = f.domain_id AND dv.code = 'COMPACT'
WHERE fc.code = 'PACKAGED_SUBSTATION'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.symbology_rules
    WHERE feature_class_id = fc.id AND subtype_id IS NULL
      AND domain_value_id = dv.id AND render_context = 'map'
  );
```

Swap `PACKAGED_SUBSTATION` / `package_config` / `COMPACT` for `RMU` / `rmu_config`
/ `2W_1T` to style RMUs the same way. Symbology rows have no physical footprint —
**no compile needed**, just reload the map.

---

## Registering an icon (needed by either path)

If your artwork isn't in the dropdown, add it to the **map** icon library first:

1. **Admin → Symbology → Icons → Add icon.**
2. **icon_ref:** a lower-kebab name, e.g. `voltedge-packaged-station-rmu-dist-xfmr-lv-feeder-pillar`.
3. **SVG:** paste the `<svg>…</svg>` markup. Use `{{COLOR}}` where you want the
   rule's colour substituted in (a full-colour SVG with no `{{COLOR}}` is drawn
   as-is). A 24×24 `viewBox` matches the built-ins.
4. **Save.** The ref is now pickable in both the kit "Map symbol" dropdown and the
   feature-class rule editor.

> A **diagram stencil** is *not* a map icon. If your ref only exists as a stencil,
> the map falls back to a plain circle. Register it here as a symbology icon too.

---

## ∑ How the map picks a glyph (for debugging)

The map renders from vector tiles; **all styling is decided client-side** against
the feature's own tile properties. For each feature it runs the symbology matcher:

1. Consider only `render_context: "map"` rules for the feature's class.
2. Keep the rules that match: a rule can key on **subtype**, on a **domain value**
   (`package_config = COMPACT`), on a **raw column** (`assembly_ref = COMPACT_PERMANENT`
   — this is what the kit "Map symbol" writes), or on **nothing** (the mandatory
   default).
3. **Most specific wins** (subtype + value > subtype > value > default).
4. Resolve the winner's `icon_ref` through: DB map icons → built-in fallbacks →
   the default **circle**. An unknown/stencil-only ref lands on that circle.

### "I set a symbol and nothing changed" — the checklist

- **Hard-reload the map** (Ctrl+Shift+R). The map caches rules + icons per session.
- **Is the icon a real map icon?** Admin → Symbology → Icons. If it's only a
  diagram stencil, the map draws a circle. ← most common cause.
- **Path A only — is `assembly_ref` a tile property?** Needs migration 0192 +
  `task compile CODE=<anchor>`. Check the class's `/feature-classes/<CODE>/schema`
  response: the rule should appear under `symbology` with `render_context: "map"`.
- **Path A only — was the unit placed before the marker existed?** It has no
  `assembly_ref`; re-save the kit (triggers the backfill) or use Path B.
- **No default rule?** A class with no `subtype_id`/`domain_value_id`-null rule has
  nothing to fall back to — unmatched features draw the plain geometry glyph. Add
  the default (Path B, step 1).

---

## Which path should I use?

- **Style by what it *is*** (config, voltage, condition — already a column) → **Path
  B**. Works for existing units instantly, no recompile.
- **One glyph *per kit*, applied as you place** → **Path A**. Needs the one-time
  0192 + recompile, then it's a single field on the kit and it backfills.

They coexist on the same class — a specific `assembly_ref` rule (Path A) outranks a
broader `package_config` rule (Path B) when both match, and both outrank the
default. Start with the mandatory default so nothing ever falls through.

---

*Related: [Symbology & Map Styling Guide](symbology-guide.md) for authoring icons
and the resolution engine in depth.*
