import { useQuery } from "@tanstack/react-query"

import { apiFetch } from "@/lib/api"

/**
 * One addable Packaged Substation (Phase 56): a (packaged class, config, tenure
 * subtype) triple the backend has a fan-out template for. `packaged_feature_class`
 * + `config` + `subtype` are exactly what POST /packaged-substations needs (as
 * packaged_feature_class + attributes.package_config + top-level subtype); the
 * labels drive the "Packaged substation" menu. Unlike an RMU there's a second
 * axis -- the tenure subtype (Permanent vs Mobile) -- so the backend returns one
 * row per config x subtype and each is its own menu item. Mirrors
 * api/internal/packagedsub PackagedSubstationOption.
 */
export interface PackagedSubstationOption {
  packaged_feature_class: string
  packaged_feature_class_name: string
  config: string
  config_label: string
  subtype: number
  subtype_label: string
  template_name: string
}

/** A component fanned out by a packaged-substation create -- its class, the created
 * feature (with its id), the connection wiring it into the unit, and its template
 * offset. Mirrors api/internal/packagedsub CreatedComponent. */
export interface CreatedPackagedComponent {
  feature_class: string
  feature: { id: string; [key: string]: unknown }
  connection_id: string
  offset_x: number
  offset_y: number
  rotation: number
}

/** The POST /packaged-substations response: the placed unit and every component
 * wired into it. Mirrors api/internal/packagedsub CreatePackagedSubstationResult. */
export interface CreatePackagedSubstationResult {
  packaged_substation: { id: string; [key: string]: unknown }
  components: CreatedPackagedComponent[]
}

/**
 * The Packaged Substations that can be placed on the map -- GET
 * /packaged-substations/options. Empty until the backend's template catalog is
 * seeded (it returns nothing without a template), so the "Packaged substation"
 * affordance simply never shows until the catalog is present. Read-only.
 */
export function usePackagedSubstationOptions() {
  return useQuery({
    queryKey: ["packaged-substation-options"],
    queryFn: () => apiFetch<PackagedSubstationOption[]>("/packaged-substations/options"),
  })
}
