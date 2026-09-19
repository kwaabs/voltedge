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
  is_diagram_placeable: boolean
  geometry_optional: boolean
  /** Migration 0053: overrides the diagram draw shape (e.g. a point
   * busbar drawn as a "line" bar on the one-line). Absent = inherit. */
  diagram_geometry_type?: "point" | "line" | null
  /** Migration 0074: admin-defined grouping for the map's "New feature"
   * menu. Absent/null = uncategorised. */
  category?: string | null
  /** Migration 0091: the class's electrical FUNCTION on the traced network
   * (source/consumer/passthrough/transformer/controller/node/sensor/ground).
   * Orthogonal to the structural network_role. Absent/null = unclassified
   * (non-electrical: containers, civil, DC-auxiliary). */
  functional_role?: string | null
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
  /** migrations/0060 -- optional cost, bare numeric in the house currency.
   * cost_per_km for per-length line materials, unit_cost for per-item. */
  cost_per_km?: number
  unit_cost?: number
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

/** Wire type for /admin/symbology-stencils -- api/internal/admin's
 * diagramStencilResponse. One row of the diagram stencil catalog
 * (metadata.diagram_stencils) with its promote_class mapping: the feature
 * class a stamped symbol of this stencil is promoted into on a diagram
 * (Phase 25). promote_class absent = decoration only, not promotable. Same
 * rows the canvas palette reads (useDiagramStencils), but this is the admin
 * write-side list. See docs/phase-25-stencil-promotion.md. */
export interface AdminDiagramStencil {
  icon_ref: string
  label: string
  category: string
  sort_order: number
  promote_class?: string
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

/** Wire type for /admin/diagram-placement-rules -- api/internal/admin's
 * diagramPlacementRuleResponse. One row = one feature class allowed on a
 * given (container class × diagram type) pairing
 * (metadata.diagram_placement_rules, migration 0068). A pairing with no
 * rows is unconstrained (any is_diagram_placeable class); rows present
 * restrict it to exactly the listed classes. Enforced in
 * api/internal/diagram placeFeature. */
export interface AdminDiagramPlacementRule {
  id: string
  container_class: string
  container_name: string
  diagram_type: string
  diagram_type_label: string
  feature_class: string
  feature_class_name: string
}

/** Wire type for /admin/cim-profiles -- api/internal/admin's cimProfileResponse,
 * mirroring metadata.cim_profiles (migration 0034). One export target (e.g. the
 * seeded CDPSM_CIM16 default): the namespace/authority URIs a CIM RDF/XML header
 * needs, the GL-profile coordinate CRS, and the policy for feature attributes
 * that have no CIM mapping. See docs/phase-55-cim-mapping-admin.md. */
export interface AdminCimProfile {
  code: string
  name: string
  cim_namespace: string
  profile_uri: string
  model_authority: string
  coordinate_crs?: string | null
  /** 'drop' (v1 default) or 'carry' (unimplemented). */
  unmapped_attr_policy?: string | null
  is_default: boolean
}

/** Wire type for /admin/cim-class-mappings -- api/internal/admin's
 * cimClassMappingResponse, mirroring metadata.cim_class_mappings. Maps a feature
 * class (optionally one subtype) to a CIM class + kind; subtype_code null = class
 * default, non-null overrides it for that subtype. cim_kind is the behavioral axis
 * the export encoder switches on. A class with no row is silently dropped from CIM
 * export (Phase 55 Slice 2 surfaces that). */
export interface AdminCimClassMapping {
  id: string
  feature_class_code: string
  subtype_code: number | null
  cim_class: string
  cim_kind: string
}

/** Wire type for /admin/cim-attribute-mappings -- api/internal/admin's
 * cimAttributeMappingResponse, mirroring metadata.cim_attribute_mappings. Maps one
 * feature field to one CIM attribute (dotted Class.attr) with a transform bridging
 * the type/unit gap. scale_factor is meaningful for transform 'scale'; value_map
 * (a JSON lookup) for 'domain_map'/'boolean'; unit is informational. */
export interface AdminCimAttributeMapping {
  id: string
  feature_class_code: string
  field_name: string
  cim_attribute: string
  transform: string
  scale_factor: number | null
  unit: string | null
  value_map: Record<string, unknown> | null
}

/** Wire type for /admin/cim-coverage -- api/internal/admin's cimCoverageResponse.
 * A feature class with NO cim_class_mappings row of any kind, so it is silently
 * dropped from every CIM export (Phase 55 Slice 2 warn-on-unmapped). geometry_type
 * and network_role hint whether the class plausibly should export. Resolve by
 * mapping it (add a class mapping) or by marking it cim_kind 'skip' to make the
 * exclusion deliberate. */
export interface AdminCimCoverage {
  code: string
  name: string
  geometry_type: string
  network_role: string
}

/** One real feature of a class rendered through the actual CIM encoder -- the
 * byte-exact CIM/XML a full export would produce for it. sampled is false (xml
 * empty) when there was nothing to render (no instance, or a geometry-less class
 * the CIM export never emits); note then explains which. */
export interface AdminCimPreview {
  class_code: string
  feature_id: string
  xml: string
  sampled: boolean
  note?: string
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

export function useAdminDiagramPlacementRules() {
  return useQuery({
    queryKey: ["admin", "diagram-placement-rules"],
    queryFn: () =>
      apiFetch<{ diagram_placement_rules: AdminDiagramPlacementRule[] }>("/admin/diagram-placement-rules"),
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

/** The stencil catalog for the admin promote-mapping editor. A separate
 * query key from useDiagramStencils' public ["diagram-stencils"] (the canvas
 * palette): a mutation here invalidates both, so the editor and the live
 * canvas both refresh, but they stay independently cached. */
export function useAdminDiagramStencils() {
  return useQuery({
    queryKey: ["admin", "symbology-stencils"],
    queryFn: () => apiFetch<{ stencils: AdminDiagramStencil[] }>("/admin/symbology-stencils"),
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

/** The three CIM export mapping tables (metadata.cim_profiles /
 * cim_class_mappings / cim_attribute_mappings) authored by the CIM mapping admin
 * editor. Each admin mutation invalidates its own query key, matching every other
 * admin resource here. See docs/phase-55-cim-mapping-admin.md. */
export function useAdminCimProfiles() {
  return useQuery({
    queryKey: ["admin", "cim-profiles"],
    queryFn: () => apiFetch<{ cim_profiles: AdminCimProfile[] }>("/admin/cim-profiles"),
    staleTime: ADMIN_STALE_TIME,
  })
}

export function useAdminCimClassMappings() {
  return useQuery({
    queryKey: ["admin", "cim-class-mappings"],
    queryFn: () => apiFetch<{ cim_class_mappings: AdminCimClassMapping[] }>("/admin/cim-class-mappings"),
    staleTime: ADMIN_STALE_TIME,
  })
}

export function useAdminCimAttributeMappings() {
  return useQuery({
    queryKey: ["admin", "cim-attribute-mappings"],
    queryFn: () => apiFetch<{ cim_attribute_mappings: AdminCimAttributeMapping[] }>("/admin/cim-attribute-mappings"),
    staleTime: ADMIN_STALE_TIME,
  })
}

export function useAdminCimCoverage() {
  return useQuery({
    queryKey: ["admin", "cim-coverage"],
    queryFn: () => apiFetch<{ unmapped_classes: AdminCimCoverage[] }>("/admin/cim-coverage"),
    staleTime: ADMIN_STALE_TIME,
  })
}

/** A real-sample CIM/XML preview for one feature class, fetched on demand (the
 * preview dialog passes the class code when it opens; disabled until then). Runs
 * the actual encoder server-side, so it is deliberately NOT cached long -- it
 * reflects live feature data, unlike the mapping tables. Keyed by class code so
 * switching classes refetches. */
export function useAdminCimPreview(classCode: string | null) {
  return useQuery({
    queryKey: ["admin", "cim-preview", classCode],
    queryFn: () => apiFetch<AdminCimPreview>(`/admin/cim-preview/${classCode}`),
    enabled: !!classCode,
    staleTime: 0,
  })
}

/** Wire type for /admin/import-profiles -- api/internal/admin's
 * importProfileResponse, mirroring metadata.import_profiles (migration 0102).
 * One reusable ETL import for a target feature class (optionally one subtype):
 * the source format, the write policy (insert_only appends, upsert matches an
 * existing feature on match_fields), and how each row's geometry is assembled
 * (geometry_mode + its source columns). See docs/etl-import-engine.md. The
 * inverse of AdminCimProfile: columns are mapped IN to our fields, not OUT. */
export interface AdminImportProfile {
  code: string
  name: string
  feature_class_code: string
  subtype_code?: number | null
  source_format: string
  /** For SERVER-parsed multi-layer sources (geopackage): the pinned layer name,
   * used as the default in the run wizard; null lets the operator pick per run. */
  source_layer?: string | null
  write_mode: string
  /** Target field names forming the upsert natural key; empty for insert_only. */
  match_fields: string[]
  geometry_mode: string
  geometry_lon_column?: string | null
  geometry_lat_column?: string | null
  geometry_wkt_column?: string | null
  status: string

  /** Live-DB connection config (source_format postgis/mssql). The password is
   * never returned; has_credential is the read-side flag that one is stored. */
  db_host?: string | null
  db_port?: number | null
  db_name?: string | null
  db_user?: string | null
  db_sslmode?: string
  /** Optional custom read-only SELECT that replaces table-pick (source_layer). */
  source_query?: string | null
  has_credential: boolean
}

/** Wire type for /admin/import-profiles/{code}/field-mappings --
 * api/internal/admin's importFieldMappingResponse, mirroring
 * metadata.import_field_mappings. Maps one source column to one target field
 * with a coercion transform (direct/constant/scale/domain_map); the parameter
 * that matters depends on the transform (constant_value / scale_factor /
 * value_map). Profile-scoped (unlike the global CIM attribute mappings), so it
 * carries its profile_code. */
export interface AdminImportFieldMapping {
  id: string
  profile_code: string
  field_name: string
  source_column?: string | null
  transform: string
  constant_value?: string | null
  scale_factor?: number | null
  value_map?: Record<string, unknown> | null
}

/** The ETL import engine's two config tables (metadata.import_profiles /
 * import_field_mappings, migration 0102) authored by the import admin editor.
 * Each mutation invalidates its own query key, matching every other admin
 * resource here. See docs/etl-import-engine.md. */
export function useAdminImportProfiles() {
  return useQuery({
    queryKey: ["admin", "import-profiles"],
    queryFn: () => apiFetch<{ import_profiles: AdminImportProfile[] }>("/admin/import-profiles"),
    staleTime: ADMIN_STALE_TIME,
  })
}

/** One remote table of a live-DB import profile's source database (mirrors
 * importer.GpkgLayer, reused for the source-table picker). geometry_column is ""
 * when the table has no geometry/geography column. */
export interface AdminImportTable {
  name: string
  geometry_column: string
  geometry_type: string
  srid: number
  feature_count: number
}

/** Opens and pings a live-DB import profile's saved connection (POST
 * /admin/import-profiles/{code}/test-connection). Resolves on success; throws the
 * server's validation error (bad host/credentials) on failure, for the editor to
 * surface. The profile must be saved first (the connection is read from it). */
export async function testAdminImportConnection(profileCode: string): Promise<void> {
  await apiFetch(`/admin/import-profiles/${profileCode}/test-connection`, { method: "POST" })
}

/** Lists a live-DB import profile's remote tables (GET
 * /admin/import-profiles/{code}/tables), each with its geometry column if any,
 * for the source-table picker. */
export async function listAdminImportTables(profileCode: string): Promise<AdminImportTable[]> {
  const res = await apiFetch<{ tables: AdminImportTable[] }>(
    `/admin/import-profiles/${profileCode}/tables`,
  )
  return res.tables ?? []
}

/** Every field mapping of one profile (by code). Disabled until a profile is
 * selected, the same shape useSymbologyRules uses. A mutation invalidates this
 * per-profile key. */
export function useAdminImportFieldMappings(profileCode: string | null) {
  return useQuery({
    queryKey: ["admin", "import-field-mappings", profileCode],
    queryFn: () =>
      apiFetch<{ import_field_mappings: AdminImportFieldMapping[] }>(
        `/admin/import-profiles/${profileCode}/field-mappings`,
      ),
    enabled: !!profileCode,
    staleTime: ADMIN_STALE_TIME,
  })
}
