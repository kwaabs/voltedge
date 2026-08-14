import { useQueries } from "@tanstack/react-query"
import { useMemo } from "react"

import { type FeatureClassSchema } from "@/hooks/use-feature-class-schema"
import { useFeatureClasses } from "@/hooks/use-feature-classes"
import { apiFetch } from "@/lib/api"
import type { SymbologyRuleSchema } from "@/lib/symbology"

/**
 * Symbology rules for every feature class at once, keyed by class code
 * -- the map needs every layer's rules available as soon as it starts
 * rendering tiles, unlike useFeatureClassSchema's on-demand, single-class
 * fetch (only triggered when a create/edit form or connectivity picker
 * is actually about to show). Reuses the exact same query key
 * ["feature-class-schema", code] useFeatureClassSchema does, so a form
 * opened later for the same class hits an already-warm cache instead
 * of refetching the whole schema again.
 *
 * Memoized on each query's dataUpdatedAt (not the query results array
 * itself, which useQueries returns fresh every render) -- voltedge-map.tsx
 * depends on this hook's return value to know when to rebuild its map
 * layers; without a stable reference here, that effect would re-run on
 * every render instead of only when a schema fetch actually resolves.
 */
export function useAllSymbology(): Record<string, SymbologyRuleSchema[]> {
  const { data: featureClasses } = useFeatureClasses()
  const codes = featureClasses?.feature_classes.map((fc) => fc.code) ?? []

  const results = useQueries({
    queries: codes.map((code) => ({
      queryKey: ["feature-class-schema", code],
      queryFn: () => apiFetch<FeatureClassSchema>(`/feature-classes/${code}/schema`),
      staleTime: 5 * 60_000,
    })),
  })
  const updatedAtKey = results.map((r) => r.dataUpdatedAt).join(",")

  return useMemo(() => {
    const out: Record<string, SymbologyRuleSchema[]> = {}
    results.forEach((result, i) => {
      if (result.data?.symbology) out[codes[i]] = result.data.symbology
    })
    return out
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [updatedAtKey])
}
