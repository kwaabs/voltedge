import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"

import { ApiError, apiFetch } from "@/lib/api"

/**
 * The authenticated user, as returned by GET /auth/me and POST
 * /auth/login (api/internal/auth's userJSON). `permissions` is the
 * caller's effective RBAC set (Slice C), sorted -- the SPA reads it to
 * show or hide controls it knows the server would refuse, so the UI
 * matches enforcement rather than guessing.
 */
export interface SessionUser {
  id: string
  email: string
  display_name: string
  break_glass: boolean
  permissions: string[]
}

/** One SSO button the login screen should offer (GET /auth/providers). */
export interface AuthProvider {
  key: string
  display_name: string
}

export interface ProvidersInfo {
  providers: AuthProvider[]
  local_auth_enabled: boolean
}

export const SESSION_QUERY_KEY = ["auth", "me"] as const

/**
 * The current session. Backed by GET /auth/me: a live session returns the
 * user, no session returns 401. We deliberately do NOT retry -- a 401 is a
 * definitive "not signed in," not a transient failure -- and keep it fresh
 * so a server-side revocation (logout elsewhere, an admin disabling the
 * account) surfaces on the next navigation.
 */
export function useSession() {
  return useQuery<SessionUser, ApiError>({
    queryKey: SESSION_QUERY_KEY,
    queryFn: () => apiFetch<SessionUser>("/auth/me"),
    retry: false,
    staleTime: 30_000,
  })
}

/**
 * True once we know the user is unauthenticated (a 401 from /auth/me),
 * false while loading or when a session exists. Any non-401 error is left
 * for the caller to treat as a real fault, not a redirect-to-login.
 */
export function isUnauthenticated(error: unknown): boolean {
  return error instanceof ApiError && error.status === 401
}

/**
 * Which login options to render. Public (no session yet at the login
 * screen); tells the screen which SSO buttons exist and whether the local
 * form is currently enabled by the break-glass switch.
 */
export function useProviders() {
  return useQuery<ProvidersInfo, ApiError>({
    queryKey: ["auth", "providers"],
    queryFn: () => apiFetch<ProvidersInfo>("/auth/providers"),
    retry: false,
    staleTime: 60_000,
  })
}

/**
 * Local (password) login. On success the server has set the session +
 * CSRF cookies; we prime the session cache with the returned user so the
 * app shell renders immediately without a second /auth/me round trip.
 */
export function useLogin() {
  const queryClient = useQueryClient()
  return useMutation<SessionUser, ApiError, { email: string; password: string }>({
    mutationFn: (body) => apiFetch<SessionUser>("/auth/login", { method: "POST", body: JSON.stringify(body) }),
    onSuccess: (user) => {
      queryClient.setQueryData(SESSION_QUERY_KEY, user)
    },
  })
}

/**
 * Logout -- revokes the session server-side (RequireAuth + RequireCSRF)
 * and clears the cookies. We then drop every cached query, since all of
 * it was fetched as the now-signed-out user.
 */
export function useLogout() {
  const queryClient = useQueryClient()
  return useMutation<void, ApiError, void>({
    mutationFn: () => apiFetch<void>("/auth/logout", { method: "POST" }),
    onSuccess: () => {
      queryClient.setQueryData(SESSION_QUERY_KEY, null)
      queryClient.clear()
    },
  })
}

/**
 * A permission check against the current session. Returns a predicate so a
 * component can gate several controls off one hook call. Absent session ->
 * denies everything (the shell would have redirected already, but this
 * keeps callers safe during the brief load).
 */
export function usePermission() {
  const { data } = useSession()
  const set = new Set(data?.permissions ?? [])
  return (perm: string) => set.has(perm)
}
