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

export interface FeatureClassSchema {
  code: string
  name: string
  geometry_type: string
  subtypes: SubtypeSchema[]
  fields: FieldSchema[]
}

/**
 * Everything a create/edit form needs to render itself correctly --
 * subtypes to pick from, one entry per field with its data type, domain
 * values (for a coded field), and which subtypes require it. Heavier
 * than useFeatureClasses (every field + domain values, not just
 * code/name/geometry_type), so only fetched when a form is actually
 * about to be shown, not for every layer/panel render.
 */
export function useFeatureClassSchema(featureClassCode: string | undefined) {
  return useQuery({
    queryKey: ["feature-class-schema", featureClassCode],
    queryFn: () => apiFetch<FeatureClassSchema>(`/feature-classes/${featureClassCode}/schema`),
    enabled: Boolean(featureClassCode),
    staleTime: 5 * 60_000,
  })
}
