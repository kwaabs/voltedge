import { useMutation } from "@tanstack/react-query"

import { ApiError, apiFetch } from "@/lib/api"

/** The result of attaching a lamp to a pole (POST /street-lights, api/internal/
 * streetlight). The lamp + the POLE-supports link always land; `wired` says
 * whether it was also auto-wired to the pole's nearby LV, and `wire_note`
 * explains any non-wire (no conductor in range, several, phase mismatch, or the
 * caller lacking connectivity:commit). */
export interface AttachStreetLightResult {
  street_light: { id: string; [k: string]: unknown }
  relationship: { id: string } | null
  wired: boolean
  connection?: { id: string } | null
  wire_note?: string
}

export interface AttachStreetLightBody {
  pole_feature_class: string
  pole_feature_id: string
  subtype?: number | null
  attributes: Record<string, unknown>
  radius_meters?: number
}

/** Attach a geometry-less pole-mounted street light to a pole and best-effort
 * auto-wire it. Needs features:write + relationships:commit (and
 * connectivity:commit for the auto-wire). */
export function useAttachStreetLight() {
  return useMutation<AttachStreetLightResult, ApiError, AttachStreetLightBody>({
    mutationFn: (body) => apiFetch<AttachStreetLightResult>("/street-lights", { method: "POST", body: JSON.stringify(body) }),
  })
}
