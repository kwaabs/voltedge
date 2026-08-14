/**
 * Client-side symbology resolution -- Martin never sees
 * metadata.symbology_rules or an icon; every styling decision happens
 * here, against the same published metadata a create/edit form already
 * fetches (use-feature-class-schema.ts). Framework-agnostic on purpose
 * (no OpenLayers import) -- map-layers.ts turns a resolved StyleDef
 * into a real OL Style; a legend or any other future consumer can read
 * the same StyleDef without dragging OL along.
 */

/** The actual style, as resolved from one symbology_rules row's
 * style_def JSON (api/internal/metadata's SymbologyRule). icon_ref/
 * color/scale apply to a point; width to a line (plus color, shared);
 * fill_color/stroke_color to a polygon. Not every field is relevant to
 * every geometry type -- map-layers.ts picks whichever apply. */
export interface StyleDef {
  icon_ref?: string
  color?: string
  scale?: number
  width?: number
  fill_color?: string
  stroke_color?: string
}

/** One symbology_rules row, pre-resolved server-side (see
 * api/cmd/server/main.go's resolveSymbologySchema) -- domain_field is
 * already the field *name* a rendered feature's own attribute should
 * be compared against, not a raw domain_value_id the frontend would
 * otherwise have to cross-reference itself. render_context ("map" or
 * "diagram") is sent unfiltered -- the API resolves both contexts'
 * rules the same way, and each consumer filters to the one it renders. */
export interface SymbologyRuleSchema {
  subtype_code?: number
  domain_field?: string
  domain_value_code?: string
  style_def: StyleDef
  render_context: "map" | "diagram"
  /** Optional zoom-level bounds (inclusive) a rule only applies within
   * -- real columns on symbology_rules since Phase 1, loaded server-side
   * but never sent to the frontend, and thus never enforced, until now.
   * Omitted entirely (undefined on both) means "applies at every zoom",
   * same as it always effectively behaved before this existed. */
  min_zoom?: number
  max_zoom?: number
}

function ruleMatches(rule: SymbologyRuleSchema, subtype: number | undefined, attributes: Record<string, unknown>): boolean {
  if (rule.subtype_code !== undefined && rule.subtype_code !== subtype) return false
  if (rule.domain_field !== undefined && attributes[rule.domain_field] !== rule.domain_value_code) return false
  return true
}

/** Mirrors migrations/0012's own resolution order: (subtype +
 * domain_value) > subtype alone > domain_value alone > neither (the
 * mandatory default) -- each axis narrowed is worth one point, ties
 * broken by whichever's found first (authoring is expected to never
 * produce a genuine tie for the same subtype+attribute combination). */
function specificity(rule: SymbologyRuleSchema): number {
  return (rule.subtype_code !== undefined ? 2 : 0) + (rule.domain_field !== undefined ? 1 : 0)
}

/** Resolves the most specific matching rule's style for one feature --
 * subtype and attributes come from that feature's own rendered tile
 * properties (Martin's auto-discovery publishes every column, not just
 * geometry, so no extra fetch is needed per feature). context selects
 * which render_context's rules are even considered -- the schema
 * endpoint now sends both map and diagram rules together, so every
 * caller must say which one it's rendering. zoom is optional -- passing
 * it excludes any rule whose min_zoom/max_zoom don't cover the current
 * level (map-layers.ts's the only caller that has a real one to pass;
 * a legend or the diagram canvas has no single "current zoom" to filter
 * by, so they call this with zoom omitted, same as every rule always
 * behaved before min_zoom/max_zoom were wired in). Returns undefined
 * when no rule matches at all (no rules loaded yet, a feature class
 * that was never seeded with even a default rule, or every matching
 * rule is zoom-gated out at the current level) -- map-layers.ts falls
 * back to its own plain geometry-type styling in that case, same as
 * before this existed. */
export function resolveSymbology(
  rules: SymbologyRuleSchema[] | undefined,
  subtype: number | undefined,
  attributes: Record<string, unknown>,
  context: "map" | "diagram",
  zoom?: number,
): StyleDef | undefined {
  let best: SymbologyRuleSchema | undefined
  let bestScore = -1
  for (const rule of rules ?? []) {
    if (rule.render_context !== context) continue
    if (zoom !== undefined) {
      if (rule.min_zoom !== undefined && zoom < rule.min_zoom) continue
      if (rule.max_zoom !== undefined && zoom > rule.max_zoom) continue
    }
    if (!ruleMatches(rule, subtype, attributes)) continue
    const score = specificity(rule)
    if (score > bestScore) {
      best = rule
      bestScore = score
    }
  }
  return best?.style_def
}
