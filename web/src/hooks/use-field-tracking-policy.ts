import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"

import { apiFetch, type ApiError } from "@/lib/api"

// Org-wide field-tracking policy, two independent switches (both default ON):
//   - enabled: whether the field app records at all (movement trail + located
//     edits). Off stops it device-side AND as a server backstop.
//   - disclosure_enabled: whether the app TELLS crews they're being recorded (the
//     in-app disclosure icon + banner). Purely a device-UI flag.
// The device reads both from its field-login response. Backed by GET/PUT
// /field-tracking/policy (GET field:tracks:read, PUT admin:field-packs).

export interface FieldTrackingPolicy {
  enabled: boolean
  disclosure_enabled: boolean
}

/** Current org-wide field-tracking policy (both flags). */
export function useFieldTrackingPolicy() {
  return useQuery({
    queryKey: ["field-tracking-policy"],
    queryFn: () => apiFetch<FieldTrackingPolicy>("/field-tracking/policy"),
    staleTime: 30_000,
  })
}

/** Updates whichever flag(s) are passed (PUT accepts a partial), then refreshes the
 * query so the controls reflect the new values. */
export function useSetFieldTrackingPolicy() {
  const queryClient = useQueryClient()
  return useMutation<FieldTrackingPolicy, ApiError, Partial<FieldTrackingPolicy>>({
    mutationFn: (patch) =>
      apiFetch<FieldTrackingPolicy>("/field-tracking/policy", {
        method: "PUT",
        body: JSON.stringify(patch),
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["field-tracking-policy"] })
    },
  })
}
