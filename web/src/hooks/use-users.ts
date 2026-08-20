import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"

import { ApiError, apiFetch } from "@/lib/api"

/**
 * Wire types for the Slice D user-admin API (api/internal/auth/useradmin.go),
 * all behind the admin:users permission. Mirrored here per this app's
 * convention of each hook holding its own copy of the API shape.
 */
export interface AdminUser {
  id: string
  email: string
  display_name: string
  status: "active" | "disabled"
  break_glass: boolean
  roles: string[]
  providers: string[]
  created_at: string
}

export interface AdminRole {
  key: string
  name: string
  description: string
}

interface AdminUsersResponse {
  users: AdminUser[]
  local_auth_enabled: boolean
}

const USERS_KEY = ["auth", "admin", "users"] as const

export function useAdminUsers() {
  return useQuery<AdminUsersResponse, ApiError>({
    queryKey: USERS_KEY,
    queryFn: () => apiFetch<AdminUsersResponse>("/auth/admin/users"),
    retry: false,
  })
}

export function useAdminRoles() {
  return useQuery<{ roles: AdminRole[] }, ApiError>({
    queryKey: ["auth", "admin", "roles"],
    queryFn: () => apiFetch<{ roles: AdminRole[] }>("/auth/admin/roles"),
    retry: false,
    staleTime: 5 * 60_000,
  })
}

export interface CreateUserBody {
  email: string
  display_name: string
  break_glass: boolean
  role?: string
  password?: string
}

/**
 * Every mutation here invalidates the users list on settle so the table
 * reflects the server's fresh row -- the handlers each return the updated
 * account, but re-fetching the list keeps the derived counts / ordering
 * honest without threading each response back into cache by hand.
 */
function useUsersMutation<TBody>(fn: (body: TBody) => Promise<unknown>) {
  const queryClient = useQueryClient()
  return useMutation<unknown, ApiError, TBody>({
    mutationFn: fn,
    onSuccess: () => queryClient.invalidateQueries({ queryKey: USERS_KEY }),
  })
}

export function useCreateUser() {
  return useUsersMutation<CreateUserBody>((body) =>
    apiFetch<AdminUser>("/auth/admin/users", { method: "POST", body: JSON.stringify(body) }),
  )
}

export function useAssignRole() {
  return useUsersMutation<{ userId: string; role: string }>(({ userId, role }) =>
    apiFetch<AdminUser>(`/auth/admin/users/${userId}/roles`, { method: "POST", body: JSON.stringify({ role }) }),
  )
}

export function useRemoveRole() {
  return useUsersMutation<{ userId: string; role: string }>(({ userId, role }) =>
    apiFetch<AdminUser>(`/auth/admin/users/${userId}/roles/${encodeURIComponent(role)}`, { method: "DELETE" }),
  )
}

export function useSetUserStatus() {
  return useUsersMutation<{ userId: string; status: "active" | "disabled" }>(({ userId, status }) =>
    apiFetch<AdminUser>(`/auth/admin/users/${userId}/status`, { method: "POST", body: JSON.stringify({ status }) }),
  )
}

export function useSetUserPassword() {
  return useUsersMutation<{ userId: string; password: string }>(({ userId, password }) =>
    apiFetch<AdminUser>(`/auth/admin/users/${userId}/password`, { method: "POST", body: JSON.stringify({ password }) }),
  )
}

export function useUnlinkIdentity() {
  return useUsersMutation<{ userId: string; provider: string }>(({ userId, provider }) =>
    apiFetch<AdminUser>(`/auth/admin/users/${userId}/identities/${encodeURIComponent(provider)}`, { method: "DELETE" }),
  )
}

/**
 * Flip the global local-auth switch (POST /auth/settings/local-auth). This
 * lives behind admin:auth-settings, not admin:users, so the page only
 * offers it when the caller holds that permission. Invalidates both the
 * users list (which reports the switch state) and the public providers
 * query the login screen reads.
 */
export function useToggleLocalAuth() {
  const queryClient = useQueryClient()
  return useMutation<{ local_auth_enabled: boolean }, ApiError, boolean>({
    mutationFn: (enabled) =>
      apiFetch<{ local_auth_enabled: boolean }>("/auth/settings/local-auth", {
        method: "POST",
        body: JSON.stringify({ enabled }),
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: USERS_KEY })
      queryClient.invalidateQueries({ queryKey: ["auth", "providers"] })
    },
  })
}
