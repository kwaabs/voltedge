import { useQuery } from "@tanstack/react-query"

import { apiFetch } from "@/lib/api"

/**
 * One addable Ring Main Unit (Phase RMU Slice 3): a (RMU class, config) pairing
 * the backend has a fan-out template for. `rmu_feature_class` + `config` are
 * exactly what POST /rmus needs (as rmu_feature_class + attributes.rmu_config);
 * the labels drive the "Add RMU" menu. Mirrors api/internal/rmu RMUOption.
 */
export interface RMUOption {
  rmu_feature_class: string
  rmu_feature_class_name: string
  config: string
  config_label: string
  template_name: string
}

/** A way fanned out by an RMU create -- its class, the created way feature (with
 * its id), the connection wiring it to the internal bus, and its template
 * offset. Mirrors api/internal/rmu CreatedWay. */
export interface CreatedRMUWay {
  feature_class: string
  feature: { id: string; [key: string]: unknown }
  connection_id: string
  offset_x: number
  offset_y: number
  rotation: number
}

/** The POST /rmus response: the placed RMU, its internal bus, and every way
 * wired into it. Mirrors api/internal/rmu CreateRMUResult. */
export interface CreateRMUResult {
  rmu: { id: string; [key: string]: unknown }
  busbar: { id: string; [key: string]: unknown }
  ways: CreatedRMUWay[]
}

/**
 * The Ring Main Units that can be placed on the map -- GET /rmus/options. Empty
 * until seed/rmu_templates.sql is applied (the backend returns nothing without a
 * template), so the "Add RMU" affordance simply never shows until the catalog is
 * present. Read-only.
 */
export function useRMUOptions() {
  return useQuery({
    queryKey: ["rmu-options"],
    queryFn: () => apiFetch<RMUOption[]>("/rmus/options"),
  })
}
