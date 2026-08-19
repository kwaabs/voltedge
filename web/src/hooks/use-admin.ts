import { useQuery } from "@tanstack/react-query"

import { apiFetch } from "@/lib/api"

/**
 * Wire types for /admin/* -- api/internal/admin's own DTOs
 * (featureClassSummary, domainResponse, fieldGroupResponse), mirrored
 * here the same way every other hook in this app keeps its own copy of
 * the API's shape rather than sharing generated types.
 */
export interface AdminFeatureClassSummary {
  code: string
  name: string
  geometry_type: string
  srid?: number
  network_role: string
  status: string
  is_diagram_container: boolean
  physical_table?: string
  subtype_count: number
  field_count: number
}

export interface AdminDomainValue {
  code: string
  label: string
  sort_order: number
  material_spec_code?: string
}

export interface AdminDomain {
  code: string
  name: string
  domain_type: "coded" | "range"
  values?: AdminDomainValue[]
  range_min?: number
  range_max?: number
}

export interface AdminFieldGroup {
  code: string
  name: string
  sort_order: number
  icon?: string
}

/** Wire type for /admin/material-specs -- api/internal/admin's
 * materialSpecResponse. A catalog entry (e.g. ACSR_50) a coded domain
 * value can optionally link to via its own material_spec_code, so a
 * field like MV_CONDUCTOR's conductor_spec resolves to real engineering
 * numbers instead of just a display label. See
 * docs/phase-8-materials.md. */
export interface AdminMaterialSpec {
  code: string
  name: string
  category: string
  resistance_ohm_per_km?: number
  reactance_ohm_per_km?: number
  ampacity_amps?: number
  diameter_mm?: number
  weight_kg_per_km?: number
  /** migrations/0022 -- conductor-intrinsic inputs to a first-principles
   * IEEE 738 ampacity calc, on top of the static ampacity_amps above. */
  max_operating_temp_c?: number
  emissivity?: number
  absorptivity?: number
  resistance_temp_coefficient_per_c?: number
}

/** Wire type for /admin/symbology-icons -- api/internal/admin's
 * symbologyIconResponse. One icon_ref -> raw <svg> markup row of the icon
 * library (metadata.symbology_icons, migrations/0023): the artwork a
 * symbology rule's style_def.icon_ref points at. Distinct from the public
 * GET /symbology/icons the map reads (useSymbologyIcons) -- same rows, but
 * this is the admin write-side list. See
 * docs/phase-3-symbology-icon-table.md. */
export interface AdminSymbologyIcon {
  icon_ref: string
  svg: string
}

/** The style half of a symbology rule -- mirrors api/internal/admin's
 * styleDefRequest and web/src/lib/symbology.ts's StyleDef. Every field
 * optional: a point rule carries icon_ref/color/scale, a line
 * color/width, a polygon fill_color/stroke_color. */
export interface AdminStyleDef {
  icon_ref?: string
  color?: string
  scale?: number
  width?: number
  dash?: string
  fill_color?: string
  stroke_color?: string
}

/** Wire type for /admin/feature-classes/{code}/symbology-rules --
 * api/internal/admin's symbologyRuleResponse. One raw symbology_rules row
 * with its id and the same code-shaped targeting the rule editor edits by
 * (subtype_code, domain_field + domain_value_code), the inverse of the
 * pre-resolved, id-less array the map matcher reads off the schema
 * endpoint. */
export interface AdminSymbologyRule {
  id: string
  render_context: "map" | "diagram"
  subtype_code?: number
  domain_field?: string
  domain_value_code?: string
  min_zoom?: number
  max_zoom?: number
  style_def: AdminStyleDef
}

/** Long staleTime, same reasoning useFeatureClasses/useCatalog use --
 * this is admin-authored data that changes rarely (an explicit create
 * action), not something worth polling. Each admin mutation invalidates
 * these queries itself. */
const ADMIN_STALE_TIME = 5 * 60_000

export function useAdminFeatureClasses() {
  return useQuery({
    queryKey: ["admin", "feature-classes"],
    queryFn: () => apiFetch<{ feature_classes: AdminFeatureClassSummary[] }>("/admin/feature-classes"),
    staleTime: ADMIN_STALE_TIME,
  })
}

export function useAdminDomains() {
  return useQuery({
    queryKey: ["admin", "domains"],
    queryFn: () => apiFetch<{ domains: AdminDomain[] }>("/admin/domains"),
    staleTime: ADMIN_STALE_TIME,
  })
}

export function useAdminFieldGroups() {
  return useQuery({
    queryKey: ["admin", "field-groups"],
    queryFn: () => apiFetch<{ field_groups: AdminFieldGroup[] }>("/admin/field-groups"),
    staleTime: ADMIN_STALE_TIME,
  })
}

export function useAdminMaterialSpecs() {
  return useQuery({
    queryKey: ["admin", "material-specs"],
    queryFn: () => apiFetch<{ material_specs: AdminMaterialSpec[] }>("/admin/material-specs"),
    staleTime: ADMIN_STALE_TIME,
  })
}

/** The icon library for the admin editor. Deliberately a SEPARATE query
 * key from useSymbologyIcons' public ["symbology-icons"] (the map's
 * registry): a mutation here invalidates both, so the library page and the
 * live map both refresh, but they stay independently cached (the page
 * wants the raw list, the map wants a memoized icon_ref -> svg registry). */
export function useAdminSymbologyIcons() {
  return useQuery({
    queryKey: ["admin", "symbology-icons"],
    queryFn: () => apiFetch<{ symbology_icons: AdminSymbologyIcon[] }>("/admin/symbology-icons"),
    staleTime: ADMIN_STALE_TIME,
  })
}

/** Every symbology rule for one feature class, for the rule editor on the
 * feature-class detail page. Keyed by code and disabled until a code is
 * present, the same shape useFeatureClassSchema uses. A mutation
 * invalidates both this and ["feature-class-schema", code] (the map/legend
 * read the pre-resolved symbology off the latter). */
export function useSymbologyRules(code: string | undefined) {
  return useQuery({
    queryKey: ["admin", "symbology-rules", code],
    queryFn: () => apiFetch<{ symbology_rules: AdminSymbologyRule[] }>(`/admin/feature-classes/${code}/symbology-rules`),
    enabled: !!code,
    staleTime: ADMIN_STALE_TIME,
  })
}
