import { useQuery } from "@tanstack/react-query"

import { apiFetch } from "@/lib/api"

/**
 * One addable Kit Assembly: an (anchor class, config) pair -- optionally pinned
 * to a particular anchor subtype -- the backend has a fan-out template for. The
 * anchor is a POINT feature (an overhead POLE or a pad-mount DIST_XFMR) whose
 * kit (regulator, arrester, riser, etc.) fans out around it. `anchor_feature_class`
 * + `config` + `anchor_subtype` are exactly what POST /assemblies needs (as
 * anchor_feature_class + config + top-level subtype); the labels drive the
 * "Overhead & pad-mount kits" menu. `anchor_subtype` is absent for options that
 * fit any subtype (e.g. POLE_MOUNT_STD) and set for those that require a specific
 * one (e.g. PAD_MOUNT_URD needs the pad-mount DIST_XFMR subtype). Mirrors the
 * backend AssemblyOption.
 */
export interface AssemblyOption {
  anchor_feature_class: string
  anchor_feature_class_name: string
  config: string
  name: string
  anchor_subtype?: number
}

/** A component fanned out by an assembly create -- its class, the created feature
 * (with its id), the connection or support relationship tying it to the anchor,
 * and its template offset + rotation. Mirrors the backend CreatedComponent. */
export interface CreatedAssemblyComponent {
  feature_class: string
  feature: { id: string; [key: string]: unknown }
  connection_id?: string
  support_relationship_id?: string
  offset_x: number
  offset_y: number
  rotation: number
}

/** The POST /assemblies response: the placed anchor and every component fanned
 * out around it. Mirrors the backend CreateAssemblyResult. */
export interface CreateAssemblyResult {
  anchor: { id: string; [key: string]: unknown }
  components: CreatedAssemblyComponent[]
}

/**
 * The Kit Assemblies that can be placed on the map -- GET /assemblies/options.
 * Empty until the backend's template catalog is seeded (it returns nothing
 * without a template), so the "Overhead & pad-mount kits" affordance simply
 * never shows until the catalog is present. Read-only.
 */
export function useAssemblyOptions() {
  return useQuery({
    queryKey: ["assembly-options"],
    queryFn: () => apiFetch<AssemblyOption[]>("/assemblies/options"),
  })
}
