import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"

import { ApiError, apiFetch } from "@/lib/api"

/**
 * Wire types for the admin pack-management API (api/internal/fieldpack/admin.go),
 * all behind the admin:field-packs permission. An administrator lists every
 * user's field-tool region packs and disables (hides from the owner's tablet) or
 * deletes them, so stale packs don't crowd the device. Mirrored here per this
 * app's convention of each hook holding its own copy of the API shape.
 */
export interface AdminFieldPack {
  id: string
  status: "queued" | "running" | "succeeded" | "failed" | "cancelled"
  owner_id: string
  owner_email: string
  owner_name: string
  feature_classes?: string[]
  bbox?: number[]
  feature_count?: number
  attachment_count?: number
  tiles_bytes?: number
  data_bytes?: number
  error?: string
  disabled: boolean
  snapshot_at?: string
  created_at: string
  finished_at?: string
}

const PACKS_KEY = ["admin", "field-packs"] as const

export function useAdminFieldPacks() {
  return useQuery<{ packs: AdminFieldPack[] }, ApiError>({
    queryKey: PACKS_KEY,
    queryFn: () => apiFetch<{ packs: AdminFieldPack[] }>("/field-packs"),
    retry: false,
  })
}

/**
 * Every mutation invalidates the pack list on success so the table reflects the
 * server's fresh state (disable/enable/delete all return 204 -- there's no body
 * to fold back, so a re-fetch keeps the list honest).
 */
function usePacksMutation<TVars>(fn: (vars: TVars) => Promise<unknown>) {
  const queryClient = useQueryClient()
  return useMutation<unknown, ApiError, TVars>({
    mutationFn: fn,
    onSuccess: () => queryClient.invalidateQueries({ queryKey: PACKS_KEY }),
  })
}

export function useSetPackDisabled() {
  return usePacksMutation<{ id: string; disabled: boolean }>(({ id, disabled }) =>
    apiFetch(`/field-packs/${id}/${disabled ? "disable" : "enable"}`, { method: "POST" }),
  )
}

export function useDeletePack() {
  return usePacksMutation<{ id: string }>(({ id }) =>
    apiFetch(`/field-packs/${id}`, { method: "DELETE" }),
  )
}
