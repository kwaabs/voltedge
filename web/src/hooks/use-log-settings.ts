import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"

import { ApiError, apiFetch } from "@/lib/api"

/**
 * The runtime log-level control (api/internal/logsettings): read and set the API
 * server's structured-logging verbosity live, with no restart. Gated server-side
 * on admin:users -- a platform-operations control, the same audience as the
 * logging guide. The chosen level persists in iam.system_settings and is
 * re-applied at boot, so it survives a restart.
 */

export type LogLevel = "debug" | "info" | "warn" | "error"

/** The /log-settings payload: the active level plus the selectable options (the
 * server owns the list so the control never offers a level it would reject). */
export interface LogSettings {
  level: LogLevel
  options: LogLevel[]
}

export function useLogSettings() {
  return useQuery({
    queryKey: ["admin", "log-settings"],
    queryFn: () => apiFetch<LogSettings>("/log-settings"),
    staleTime: 60_000,
    retry: 1,
  })
}

export function useSetLogLevel() {
  const queryClient = useQueryClient()
  return useMutation<LogSettings, ApiError, LogLevel>({
    mutationFn: (level) =>
      apiFetch<LogSettings>("/log-settings", {
        method: "PUT",
        body: JSON.stringify({ level }),
      }),
    onSuccess: (data) => {
      // The response carries the now-active level; seed the cache with it so the
      // control reflects the change without a refetch round-trip.
      queryClient.setQueryData(["admin", "log-settings"], data)
    },
  })
}
