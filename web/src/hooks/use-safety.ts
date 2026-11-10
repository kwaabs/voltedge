import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"

import { ApiError, apiFetch } from "@/lib/api"

// Client mirror of the operational-safety API (api/internal/safety, mounted at
// /safety, gated safety:read / safety:write). Tags and grounds protect a crew
// working a de-energised section; a BLOCKING tag (danger/out_of_service) bars the
// device from being operated (the server-side switching-step interlock).

export type TagType = "danger" | "out_of_service" | "caution" | "information"
export type SafetyStatus = "active" | "removed"
export type ClearanceStatus = "requested" | "issued" | "surrendered" | "cancelled"

export interface SafetyTag {
  id: string
  device_class: string
  device_id: string
  tag_type: TagType
  blocks_operation: boolean
  reason?: string
  clearance_id?: string
  status: SafetyStatus
  placed_by?: string
  placed_at: string
  removed_by?: string
  removed_at?: string
  notes?: string
  created_at: string
}

export interface SafetyGround {
  id: string
  feature_class: string
  feature_id: string
  reason?: string
  clearance_id?: string
  status: SafetyStatus
  applied_by?: string
  applied_at: string
  removed_by?: string
  removed_at?: string
  notes?: string
  created_at: string
}

export interface Clearance {
  id: string
  reference: string
  incident_id?: string
  switching_order_id?: string
  work_description?: string
  issued_to?: string
  status: ClearanceStatus
  requested_at: string
  issued_at?: string
  surrendered_at?: string
  notes?: string
  created_at: string
  updated_at: string
  tags?: SafetyTag[]
  grounds?: SafetyGround[]
}

const SAFETY_KEY = ["safety"] as const

/** The tag types a user may place, labelled. The first two block operation. */
export const TAG_TYPES: { value: TagType; label: string; blocks: boolean }[] = [
  { value: "danger", label: "Danger — do not operate", blocks: true },
  { value: "out_of_service", label: "Out of service", blocks: true },
  { value: "caution", label: "Caution", blocks: false },
  { value: "information", label: "Information", blocks: false },
]

/** Active safety tags on one device (the panel's Safety tab). */
export function useDeviceTags(deviceClass?: string, deviceId?: string) {
  return useQuery({
    queryKey: [...SAFETY_KEY, "tags", deviceClass ?? "", deviceId ?? ""],
    queryFn: () =>
      apiFetch<{ tags: SafetyTag[] }>(
        `/safety/tags?active=true&device_class=${encodeURIComponent(deviceClass!)}&device_id=${encodeURIComponent(deviceId!)}`,
      ),
    enabled: !!deviceClass && !!deviceId,
    staleTime: 10_000,
  })
}

/** Every active safety tag network-wide (no device filter) -- the operations
 * overlay keyed by (device_class, device_id). `enabled` gates the fetch so the
 * overlay costs nothing when toggled off. */
export function useActiveTags(enabled = true) {
  return useQuery({
    queryKey: [...SAFETY_KEY, "tags", "all"],
    queryFn: () => apiFetch<{ tags: SafetyTag[] }>(`/safety/tags?active=true`),
    enabled,
    staleTime: 10_000,
  })
}

/** Every active applied ground network-wide (no feature filter). */
export function useActiveGrounds(enabled = true) {
  return useQuery({
    queryKey: [...SAFETY_KEY, "grounds", "all"],
    queryFn: () => apiFetch<{ grounds: SafetyGround[] }>(`/safety/grounds?active=true`),
    enabled,
    staleTime: 10_000,
  })
}

/** Active applied grounds on one feature. */
export function useFeatureGrounds(featureClass?: string, featureId?: string) {
  return useQuery({
    queryKey: [...SAFETY_KEY, "grounds", featureClass ?? "", featureId ?? ""],
    queryFn: () =>
      apiFetch<{ grounds: SafetyGround[] }>(
        `/safety/grounds?active=true&feature_class=${encodeURIComponent(featureClass!)}&feature_id=${encodeURIComponent(featureId!)}`,
      ),
    enabled: !!featureClass && !!featureId,
    staleTime: 10_000,
  })
}

/** The clearances board (permits-to-work). `openOnly` narrows to issued (live). */
export function useClearances(opts: { status?: ClearanceStatus; openOnly?: boolean } = {}) {
  const params = new URLSearchParams()
  if (opts.status) params.set("status", opts.status)
  if (opts.openOnly) params.set("open", "true")
  const q = params.toString() ? `?${params.toString()}` : ""
  return useQuery({
    queryKey: [...SAFETY_KEY, "clearances", opts.status ?? "all", opts.openOnly ?? false],
    queryFn: () => apiFetch<{ clearances: Clearance[] }>(`/safety/clearances${q}`),
    staleTime: 15_000,
  })
}

function useSafetyMutation<TBody>(fn: (body: TBody) => Promise<unknown>) {
  const queryClient = useQueryClient()
  return useMutation<unknown, ApiError, TBody>({
    mutationFn: fn,
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: SAFETY_KEY })
    },
  })
}

export interface PlaceTagBody {
  device_class: string
  device_id: string
  tag_type: TagType
  reason?: string
  clearance_id?: string
}

export function usePlaceTag() {
  return useSafetyMutation<PlaceTagBody>((body) =>
    apiFetch("/safety/tags", { method: "POST", body: JSON.stringify(body) }),
  )
}

export function useRemoveTag() {
  return useSafetyMutation<{ id: string }>(({ id }) =>
    apiFetch(`/safety/tags/${id}/remove`, { method: "POST" }),
  )
}

export interface ApplyGroundBody {
  feature_class: string
  feature_id: string
  reason?: string
  clearance_id?: string
}

export function useApplyGround() {
  return useSafetyMutation<ApplyGroundBody>((body) =>
    apiFetch("/safety/grounds", { method: "POST", body: JSON.stringify(body) }),
  )
}

export function useRemoveGround() {
  return useSafetyMutation<{ id: string }>(({ id }) =>
    apiFetch(`/safety/grounds/${id}/remove`, { method: "POST" }),
  )
}

/** Surrender a clearance (releases its tags/grounds, re-permits operation). */
export function useSurrenderClearance() {
  return useSafetyMutation<{ id: string }>(({ id }) =>
    apiFetch(`/safety/clearances/${id}/surrender`, { method: "POST" }),
  )
}

/** Issue a permit-to-work from a switching order — tags the devices it opens and
 * grounds the points it earths. Lives on the /oms surface but needs safety:write;
 * invalidates both caches so the Safety tab and the switching panel refresh. */
export function useIssueClearanceFromOrder() {
  const queryClient = useQueryClient()
  return useMutation<unknown, ApiError, { orderId: string; work_description?: string; issued_to?: string }>({
    mutationFn: ({ orderId, work_description, issued_to }) =>
      apiFetch(`/oms/switching-orders/${orderId}/clearance`, {
        method: "POST",
        body: JSON.stringify({ work_description, issued_to }),
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: SAFETY_KEY })
      queryClient.invalidateQueries({ queryKey: ["oms"] })
    },
  })
}
