import { useQuery } from "@tanstack/react-query"

import { ApiError, apiFetch } from "@/lib/api"

// Client mirror of the feature-registry read API (api/internal/featureregistry).
// runtime.feature_registry is a cross-class index of every feature id -> its
// class, kept in sync by a DB trigger the compiler attaches. It backs asset-id
// pickers: type (or paste) an id and resolve what it is, or scan the live assets
// of a class, without knowing which per-class endpoint holds the row. Read-only,
// gated features:read.

/** One registry entry. class_name is the human class label (null if the class
 * was renamed/removed since); deleted_at is set for a soft-deleted feature. */
export interface RegistryEntry {
  feature_id: string
  feature_class_code: string
  class_name: string | null
  status: string | null
  deleted_at: string | null
  created_at: string | null
  territory_id: string | null
}

interface SearchResponse {
  entries: RegistryEntry[]
}

const REGISTRY_KEY = ["feature-registry"] as const

/** Typeahead over the registry: `q` is an id prefix, `assetClass` scopes to one
 * class, `live` (default true) hides soft-deleted rows. Disabled until there is
 * a class or a query so the picker doesn't list the whole network on mount. */
export function useFeatureRegistrySearch(opts: {
  q?: string
  assetClass?: string
  live?: boolean
  enabled?: boolean
}) {
  const live = opts.live ?? true
  const params = new URLSearchParams()
  if (opts.q) params.set("q", opts.q)
  if (opts.assetClass) params.set("class", opts.assetClass)
  if (live) params.set("live", "true")
  const query = params.toString() ? `?${params.toString()}` : ""
  const hasScope = Boolean(opts.q || opts.assetClass)
  return useQuery({
    queryKey: [...REGISTRY_KEY, "search", opts.q ?? "", opts.assetClass ?? "", live],
    queryFn: () => apiFetch<SearchResponse>(`/feature-registry${query}`),
    enabled: (opts.enabled ?? true) && hasScope,
    staleTime: 15_000,
  })
}

/** Resolve one exact id -> its entry. Returns null when the id is unknown (the
 * server answers 404, surfaced here as a null result rather than an error).
 * Disabled until `id` looks like a full-length uuid so we don't 404 on every
 * keystroke. */
export function useFeatureRegistryLookup(id: string | undefined) {
  const trimmed = (id ?? "").trim()
  return useQuery({
    queryKey: [...REGISTRY_KEY, "lookup", trimmed],
    queryFn: async () => {
      try {
        return await apiFetch<RegistryEntry>(`/feature-registry?id=${encodeURIComponent(trimmed)}`)
      } catch (err) {
        // A 404 means "no such id" -- a normal answer for the picker, not a fault.
        if (err instanceof ApiError && err.status === 404) return null
        throw err
      }
    },
    enabled: trimmed.length >= 8,
    staleTime: 15_000,
  })
}
