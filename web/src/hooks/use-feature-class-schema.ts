import { useQuery } from "@tanstack/react-query"

import { apiFetch } from "@/lib/api"
import type { LabelDef, SymbologyRuleSchema } from "@/lib/symbology"

export interface SubtypeSchema {
  code: number
  name: string
}

/** A material_specs row's real engineering numbers -- see
 * metadata.MaterialSpec's own doc comment (attaches to a domain value,
 * not a field or feature class directly). */
export interface MaterialSpecSummary {
  code: string
  name: string
  category: string
  resistance_ohm_per_km?: number
  reactance_ohm_per_km?: number
  ampacity_amps?: number
  diameter_mm?: number
  weight_kg_per_km?: number
}

export interface FieldSchema {
  name: string
  data_type: "text" | "numeric" | "integer" | "boolean" | "date" | "timestamp" | "uuid" | "jsonb"
  nullable: boolean
  visible_on_create: boolean
  visible_on_update: boolean
  editable_after_publish: boolean
  domain_code?: string
  domain_values?: string[]
  range_min?: number
  range_max?: number
  required_for_subtypes?: number[]
  /** Keyed by domain value code -- only present for the subset of
   * domain_values that carry a material spec. */
  material_specs?: Record<string, MaterialSpecSummary>
}

export interface TerminalSchema {
  code: string
  role: string
  phase_capable: boolean
  /** undefined -- applies to every subtype of the feature class. */
  subtype_code?: number
}

export interface FeatureClassSchema {
  code: string
  name: string
  geometry_type: string
  subtypes: SubtypeSchema[]
  fields: FieldSchema[]
  terminals?: TerminalSchema[]
  symbology?: SymbologyRuleSchema[]
  /** The class's map label configuration (Phase 30) -- one per class,
   * edited in its own dialog, absent/empty template means no labels. */
  label_def?: LabelDef
  /** Nominal voltage namespace (Phase 37 Slice 2), "MV" or "LV", absent for
   * structural / unclassified classes. The create/edit form passes it to the
   * circuit_id field's picker to scope it to same-network circuits. */
  network?: "MV" | "LV"
  /** Map display-zoom band (migration 0090) -- the geographic map hides the
   * whole feature (glyph + label) below min_display_zoom / above
   * max_display_zoom. Each absent = no bound; both absent = draw at every
   * zoom. Consumed by createSymbologyStyle (map-layers.ts). Distinct from a
   * symbology rule's own min_zoom/max_zoom, which pick a style not visibility. */
  min_display_zoom?: number
  max_display_zoom?: number
}

/**
 * The terminals a specific subtype can connect through -- a
 * subtype-scoped terminal (same code) wins over a generic one, mirroring
 * api/internal/metadata's ResolveTerminal (most-specific-wins), since
 * this is the same connectivity picker's job on the client side.
 */
export function terminalsForSubtype(schema: FeatureClassSchema, subtypeCode: number | undefined): TerminalSchema[] {
  const byCode = new Map<string, TerminalSchema>()
  for (const t of schema.terminals ?? []) {
    if (t.subtype_code !== undefined && t.subtype_code !== subtypeCode) continue
    const existing = byCode.get(t.code)
    if (!existing || (existing.subtype_code === undefined && t.subtype_code !== undefined)) {
      byCode.set(t.code, t)
    }
  }
  return [...byCode.values()]
}

/**
 * Everything a create/edit form -- or a connectivity picker -- needs to
 * render itself correctly: subtypes to pick from, one entry per field
 * with its data type, domain values (for a coded field), which subtypes
 * require it, and terminals available per subtype. Heavier than
 * useFeatureClasses (every field + domain values + terminals, not just
 * code/name/geometry_type), so only fetched when a form or picker is
 * actually about to be shown, not for every layer/panel render.
 */
export function useFeatureClassSchema(featureClassCode: string | undefined) {
  return useQuery({
    queryKey: ["feature-class-schema", featureClassCode],
    queryFn: () => apiFetch<FeatureClassSchema>(`/feature-classes/${featureClassCode}/schema`),
    enabled: Boolean(featureClassCode),
    staleTime: 5 * 60_000,
  })
}
