import { useQuery } from "@tanstack/react-query"

import { fetchCatalog } from "@/lib/martin"

/**
 * Server state (Martin's live catalog), so this is Query, not a
 * store -- same reasoning as useHealth. Longer staleTime than the
 * default: which feature classes are compiled changes rarely (an admin
 * action), not something worth polling.
 */
export function useCatalog() {
  return useQuery({
    queryKey: ["martin-catalog"],
    queryFn: fetchCatalog,
    staleTime: 5 * 60_000,
    retry: 1,
  })
}
