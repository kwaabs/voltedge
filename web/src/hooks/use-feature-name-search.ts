import { useEffect, useState } from "react"

import { useFeatureClasses } from "@/hooks/use-feature-classes"
import { apiFetch } from "@/lib/api"

export interface FeatureSearchResult {
  featureClassCode: string
  className: string
  featureId: string
  name: string
}

export const MIN_QUERY_LENGTH = 2
const DEBOUNCE_MS = 300
const PER_CLASS_LIMIT = 8

/**
 * Search-by-name across every feature class -- there's no single
 * cross-class search endpoint (each feature class is its own physical
 * table, per the compiler), so this fans out one GET /features/{code}?
 * name= per class in parallel and merges client-side, same reasoning
 * layers-panel.tsx already fans out per Martin catalog entry. Only
 * feature-panel.tsx-style name display was worth a per-row fetch
 * (bounded, small lists); a name search is the opposite shape --
 * unbounded classes, so results are capped per class
 * (PER_CLASS_LIMIT) and the query itself is debounced and
 * length-gated to keep the fan-out cheap while typing. Extracted from
 * feature-search.tsx (the map's search box) so diagram-canvas.tsx's
 * "add an existing feature" picker can reuse the exact same fan-out
 * instead of a second copy.
 */
export function useFeatureNameSearch(query: string, classCodes?: string[]) {
  const [loading, setLoading] = useState(false)
  const [results, setResults] = useState<FeatureSearchResult[]>([])
  const { data: featureClasses } = useFeatureClasses()
  // A stable key for the optional class scope, so the effect re-runs when the
  // caller narrows the fan-out (e.g. "only SERVICE_POINT / METER") without
  // depending on a fresh array identity each render.
  const scopeKey = classCodes ? classCodes.join(",") : ""

  useEffect(() => {
    const trimmed = query.trim()
    if (trimmed.length < MIN_QUERY_LENGTH || !featureClasses) {
      setResults([])
      setLoading(false)
      return
    }

    let cancelled = false
    setLoading(true)
    // Scope the fan-out to the requested classes when given (a picker that only
    // wants supplies, or only switchable devices), else hit every class as the
    // map's global search box does.
    const scope = scopeKey ? new Set(scopeKey.split(",")) : null
    const classes = scope
      ? featureClasses.feature_classes.filter((fc) => scope.has(fc.code))
      : featureClasses.feature_classes

    const timer = setTimeout(async () => {
      const perClass = await Promise.all(
        classes.map(async (fc) => {
          try {
            const res = await apiFetch<{ features: { id: string; name: string | null }[] }>(
              `/features/${fc.code}?name=${encodeURIComponent(trimmed)}&limit=${PER_CLASS_LIMIT}`,
            )
            return res.features
              .filter((f) => f.name)
              .map((f) => ({ featureClassCode: fc.code, className: fc.name, featureId: f.id, name: f.name as string }))
          } catch {
            // A feature class whose search fails (e.g. not yet
            // compiled) is just excluded, not worth failing the whole
            // search over one class.
            return []
          }
        }),
      )
      if (!cancelled) {
        setResults(perClass.flat())
        setLoading(false)
      }
    }, DEBOUNCE_MS)

    return () => {
      cancelled = true
      clearTimeout(timer)
    }
  }, [query, featureClasses, scopeKey])

  return { results, loading }
}
