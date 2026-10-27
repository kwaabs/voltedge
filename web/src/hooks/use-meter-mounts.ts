import { useMutation } from "@tanstack/react-query"

import { ApiError, apiFetch } from "@/lib/api"

/** The result of attaching a meter to a service point (POST /meter-mounts,
 * api/internal/metermount). The meter + the SERVICE_POINT-supports link always
 * land; `wired` says whether it was also wired to the SP (METER T2 ->
 * SERVICE_POINT T1), and `wire_note` explains any non-wire (the caller lacking
 * connectivity:commit, or a rule rejection). */
export interface AttachMeterResult {
  meter: { id: string; [k: string]: unknown }
  relationship: { id: string } | null
  wired: boolean
  connection?: { id: string } | null
  wire_note?: string
}

export interface AttachMeterBody {
  service_point_feature_class: string
  service_point_feature_id: string
  subtype?: number | null
  attributes: Record<string, unknown>
}

/** Attach a geometry-less meter to a service point (it rides the SP) and wire it
 * to that SP. Needs features:write + relationships:commit (and connectivity:commit
 * for the wire). */
export function useAttachMeter() {
  return useMutation<AttachMeterResult, ApiError, AttachMeterBody>({
    mutationFn: (body) => apiFetch<AttachMeterResult>("/meter-mounts", { method: "POST", body: JSON.stringify(body) }),
  })
}
