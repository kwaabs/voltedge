import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"

import { ApiError, apiFetch } from "@/lib/api"

// The role editor API (admin:users-gated, /auth/admin/*): the RBAC catalog plus
// create/rename/clone/delete roles and set their permissions. is_system roles are
// read-only server-side (clone to customise).

export interface AdminRoleFull {
  id: string
  key: string
  name: string
  description: string
  is_system: boolean
  permissions: string[]
}

export interface PermissionDef {
  key: string
  description: string
}

const KEY = ["role-admin"] as const

export function useRolesAdmin() {
  return useQuery({
    queryKey: [...KEY, "roles"],
    queryFn: () => apiFetch<{ roles: AdminRoleFull[] }>("/auth/admin/roles"),
    staleTime: 30_000,
  })
}

export function usePermissionCatalog() {
  return useQuery({
    queryKey: [...KEY, "permissions"],
    queryFn: () => apiFetch<{ permissions: PermissionDef[] }>("/auth/admin/permissions"),
    staleTime: 5 * 60_000,
  })
}

function useRoleMutation<TBody, TRes = unknown>(fn: (body: TBody) => Promise<TRes>) {
  const qc = useQueryClient()
  return useMutation<TRes, ApiError, TBody>({
    mutationFn: fn,
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: [...KEY, "roles"] })
      // A role/permission change alters the acting user's own effective set too.
      qc.invalidateQueries({ queryKey: ["auth", "me"] })
    },
  })
}

export function useCreateRole() {
  return useRoleMutation<{ key: string; name: string; description: string }, AdminRoleFull>((body) =>
    apiFetch("/auth/admin/roles", { method: "POST", body: JSON.stringify(body) }),
  )
}

export function useUpdateRole() {
  return useRoleMutation<{ id: string; name: string; description: string }, AdminRoleFull>(({ id, ...body }) =>
    apiFetch(`/auth/admin/roles/${id}`, { method: "PATCH", body: JSON.stringify(body) }),
  )
}

export function useSetRolePermissions() {
  return useRoleMutation<{ id: string; permissions: string[] }, AdminRoleFull>(({ id, permissions }) =>
    apiFetch(`/auth/admin/roles/${id}/permissions`, { method: "PUT", body: JSON.stringify({ permissions }) }),
  )
}

export function useCloneRole() {
  return useRoleMutation<{ id: string; key: string; name: string }, AdminRoleFull>(({ id, ...body }) =>
    apiFetch(`/auth/admin/roles/${id}/clone`, { method: "POST", body: JSON.stringify(body) }),
  )
}

export function useDeleteRole() {
  return useRoleMutation<{ id: string }>(({ id }) => apiFetch(`/auth/admin/roles/${id}`, { method: "DELETE" }))
}
