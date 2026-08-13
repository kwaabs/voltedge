import { useQuery } from "@tanstack/react-query"

import { apiFetch } from "@/lib/api"

export interface SubtypeSchema {
  code: number
  name: string
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
  required_for_subtypes?: number[]
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
