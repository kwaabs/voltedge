import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"

import { apiFetch } from "@/lib/api"

// ---------------------------------------------------------------------------
// Placing a kit on the map (GET /assemblies/options + POST /assemblies). These
// back the toolbar's Assemblies menu (new-assembly-menu.tsx) and the create
// form's fan-out save (feature-create-form.tsx) -- distinct from the kit CATALOG
// authoring below (/assemblies/templates), which this hook file also serves.
// ---------------------------------------------------------------------------

/** One addable kit in the map's Assemblies menu -- mirrors api/internal/assembly
 * AssemblyOption. anchor_subtype is non-null only when the recipe forces it. */
export interface AssemblyOption {
  anchor_feature_class: string
  anchor_feature_class_name: string
  config: string
  name: string
  anchor_subtype?: number | null
  /** The kit's default anchor attributes (migration 0194) -- the create form seeds
   * these so a required config field (package_config / rmu_config) arrives
   * pre-selected instead of being re-entered. Absent when the kit declares none. */
  anchor_attributes?: Record<string, unknown> | null
}

/** The whole addable (anchor class x config) list for the Assemblies menu.
 * Refetched whenever a kit is authored (the catalog mutations below invalidate
 * ["assembly-options"]), so a newly-authored kit appears without a reload. */
export function useAssemblyOptions() {
  return useQuery({
    queryKey: ["assembly-options"],
    queryFn: () => apiFetch<AssemblyOption[]>("/assemblies/options"),
    staleTime: 5 * 60_000,
  })
}

/** One fanned-out component in a POST /assemblies result (api/internal/assembly
 * CreatedComponent). */
export interface CreatedAssemblyComponent {
  feature_class: string
  feature: { id: string } & Record<string, unknown>
  connection_id?: string
  support_relationship_id?: string
  offset_x: number
  offset_y: number
  rotation: number
}

/** POST /assemblies response: the placed anchor plus every component created and
 * wired. The create form reads result.anchor.id to select the placed anchor. */
export interface CreateAssemblyResult {
  anchor: { id: string } & Record<string, unknown>
  components: CreatedAssemblyComponent[]
}

/** One component in a kit recipe. Mirrors api/internal/assembly KitComponentInput
 * (feature-class CODES, snake_case) so fetched JSON is used as-is. A kit is the
 * "place one anchor feature and get its whole pre-wired set" recipe. */
export interface KitComponent {
  component_feature_class: string
  subtype_code?: number | null
  attributes?: Record<string, unknown>
  support_kind?: string | null
  support_parent_position?: number | null
  wire_to_position?: number | null
  own_terminal?: string | null
  upstream_terminal?: string | null
  position: number
  offset_x: number
  offset_y: number
  rotation: number
}

export interface Kit {
  id: string
  anchor_feature_class: string
  anchor_feature_class_name?: string
  code: string
  name: string
  anchor_subtype_code?: number | null
  diagram_template_name?: string | null
  /** The assembly's own glyph on the geographic map (migration 0192): an icon ref +
   * hex color. A managed map-context symbology rule on the anchor class renders it;
   * the components' / diagram symbology is untouched. Null = no assembly map symbol. */
  map_icon_ref?: string | null
  map_color?: string | null
  /** Template defaults applied to the ANCHOR feature at placement (migration 0194),
   * merged under the placing user's own attributes. Lets a kit pin its anchor's
   * required config field (e.g. package_config) so the recipe supplies it. */
  anchor_attributes?: Record<string, unknown> | null
  components: KitComponent[]
}

export interface KitSummary {
  id: string
  anchor_feature_class: string
  anchor_feature_class_name: string
  code: string
  name: string
  component_count: number
  diagram_template_name?: string | null
}

export interface KitInput {
  anchor_feature_class: string
  code: string
  name: string
  anchor_subtype_code?: number | null
  diagram_template_name?: string | null
  map_icon_ref?: string | null
  map_color?: string | null
  anchor_attributes?: Record<string, unknown> | null
  components: KitComponent[]
}

/** POST /assemblies/templates/validate -- structural errors (block a save) plus a
 * best-effort live placement dry-run (placement_check, advisory). */
export interface KitValidation {
  valid: boolean
  errors: string[]
  placement_check?: string
}

export function useAssemblyKits() {
  return useQuery({
    queryKey: ["assembly-kits"],
    queryFn: () => apiFetch<KitSummary[]>("/assemblies/templates"),
    staleTime: 30_000,
  })
}

export function useAssemblyKit(id: string | null) {
  return useQuery({
    queryKey: ["assembly-kit", id],
    queryFn: () => apiFetch<Kit>(`/assemblies/templates/${id}`),
    enabled: !!id,
  })
}

/** One positioned element of a kit's single-line TEMPLATE (mirrors
 * assembly.KitDiagramPreviewElement) -- the stored skeleton, not a generated
 * diagram. */
export interface KitDiagramPreviewElement {
  kind: string
  x: number
  y: number
  width?: number
  height?: number
  text?: string
  slot_class?: string
}

/** The single-line template a kit maps to (GET /assemblies/templates/diagram-preview).
 * template_name is "" when the kit generates no single-line. */
export interface KitDiagramPreview {
  template_name: string
  elements: KitDiagramPreviewElement[]
}

/** Resolve the single-line template for a draft kit (#2-light editor preview): an
 * explicit name wins, else it resolves by (anchor class, code) as placement does.
 * Enabled once an anchor class is chosen. */
export function useKitDiagramPreview(anchor: string, code: string, name: string) {
  const params = new URLSearchParams()
  if (anchor) params.set("anchor", anchor)
  if (code) params.set("code", code)
  if (name) params.set("name", name)
  return useQuery({
    queryKey: ["assembly-kit-diagram-preview", anchor, code, name],
    queryFn: () => apiFetch<KitDiagramPreview>(`/assemblies/templates/diagram-preview?${params.toString()}`),
    enabled: Boolean(anchor),
    staleTime: 30_000,
  })
}

/** Create (no id) or update (id) a kit. Invalidates the list and the single-kit
 * cache so the editor and the map's Assemblies menu both refresh. */
export function useSaveAssemblyKit() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, input }: { id?: string; input: KitInput }) =>
      apiFetch<Kit>(id ? `/assemblies/templates/${id}` : "/assemblies/templates", {
        method: id ? "PATCH" : "POST",
        body: JSON.stringify(input),
      }),
    onSuccess: (kit) => {
      qc.invalidateQueries({ queryKey: ["assembly-kits"] })
      qc.invalidateQueries({ queryKey: ["assembly-kit", kit.id] })
      qc.invalidateQueries({ queryKey: ["assembly-options"] })
    },
  })
}

export function useDeleteAssemblyKit() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: string) => apiFetch(`/assemblies/templates/${id}`, { method: "DELETE" }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["assembly-kits"] })
      qc.invalidateQueries({ queryKey: ["assembly-options"] })
    },
  })
}

export function useValidateAssemblyKit() {
  return useMutation({
    mutationFn: (input: KitInput) =>
      apiFetch<KitValidation>("/assemblies/templates/validate", {
        method: "POST",
        body: JSON.stringify(input),
      }),
  })
}
