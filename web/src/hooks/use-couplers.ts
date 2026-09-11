import { useQuery } from "@tanstack/react-query"

import { apiFetch } from "@/lib/api"

/**
 * One addable bus-coupler configuration (Phase Bus-coupler Slice 2): a breaker
 * class that can act as a coupler (it carries the switch_function field with a
 * BUS_COUPLER value, from the Slice 1 field-pack) plus the bus classes it may
 * bridge. `breaker_feature_class` is exactly what POST /couplers needs (as
 * breaker_feature_class); `bus_feature_classes` tells the create form which
 * classes to search for the two bus picks. Mirrors api/internal/coupler
 * CouplerOption.
 */
export interface CouplerOption {
  breaker_feature_class: string
  breaker_feature_class_name: string
  bus_feature_classes: string[]
}

/** The POST /couplers response: the placed breaker and the ids of the two
 * connections wiring it to Bus A (rule C) and Bus B (rule D). Mirrors
 * api/internal/coupler CreateCouplerResult. */
export interface CreateCouplerResult {
  breaker: { id: string; [key: string]: unknown }
  connection_a: string
  connection_b: string
}

/**
 * The breaker classes that can be placed as a bus coupler -- GET
 * /couplers/options. Empty until seed/bus_coupler_fields.sql is applied (the
 * switch_function field with a BUS_COUPLER value) AND the busbar wiring rules C/D
 * exist, so the "Bus coupler" affordance simply never shows until the catalog
 * supports it. Read-only.
 */
export function useCouplerOptions() {
  return useQuery({
    queryKey: ["coupler-options"],
    queryFn: () => apiFetch<CouplerOption[]>("/couplers/options"),
  })
}
