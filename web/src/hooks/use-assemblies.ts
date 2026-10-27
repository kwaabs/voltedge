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
