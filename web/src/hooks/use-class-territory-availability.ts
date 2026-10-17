import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"

import { apiFetch } from "@/lib/api"

/** The territories a feature class is explicitly turned OFF for (the authored
 * set, not the cascade-expanded descendants). Empty = available everywhere.
 * Needs metadata:write. */
export function useClassTerritoryAvailability(code: string | undefined) {
  return useQuery({
    queryKey: ["admin", "feature-classes", code, "territory-availability"],
    queryFn: () =>
      apiFetch<{ disabled_territory_ids: string[] }>(`/admin/feature-classes/${code}/territory-availability`),
    enabled: Boolean(code),
  })
}

/** Replace the whole disabled-territory set for a class (PUT). An empty array
 * re-enables it everywhere. Invalidates the query so the UI reflects the save. */
export function useSetClassTerritoryAvailability(code: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (disabledTerritoryIds: string[]) =>
      apiFetch(`/admin/feature-classes/${code}/territory-availability`, {
        method: "PUT",
        body: JSON.stringify({ disabled_territory_ids: disabledTerritoryIds }),
      }),
    onSuccess: () =>
      qc.invalidateQueries({ queryKey: ["admin", "feature-classes", code, "territory-availability"] }),
  })
}
