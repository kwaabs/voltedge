import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"

import { ApiError, apiFetch } from "@/lib/api"

// Client mirror of the asset-tag API (api/internal/assettag, mounted at /tags).
// Every feature carries one or more short, scannable tag codes (printed as a QR +
// Code128 + human-readable label and affixed on the physical asset); a scan
// resolves a code back to the feature. A feature may hold many codes -- the
// system-minted one plus registered legacy/external numbers -- with exactly one
// primary (the one that gets printed). Reads are gated tags:read, the lifecycle
// mutations tags:write; minting is done by a DB trigger, never over HTTP.

export type TagSource = "generated" | "external"
export type TagStatus = "active" | "void" | "superseded"

/** One row of runtime.asset_tags. `is_primary` marks the single printed code;
 * `source` distinguishes a system-minted code from a registered legacy number;
 * `status` is active / void (retired) / superseded (replaced by a reissue but
 * still resolvable). created_by is the acting user for a registered/reissued tag,
 * null for a trigger-minted one. */
export interface AssetTag {
  tag_code: string
  feature_class_code: string
  feature_id: string
  source: TagSource
  is_primary: boolean
  status: TagStatus
  print_count: number
  created_by: string | null
  created_at: string
  updated_at: string
}

interface TagsResponse {
  tags: AssetTag[]
}

/** One row of the org-wide tag console (Admin → Asset Tags): a tag plus its
 * feature's class display name. Mirrors assettag.TagRow. */
export interface TagRow extends AssetTag {
  class_name: string | null
}

/** Filters for the console browse/search. All optional; an empty field widens
 * the result. Mirrors assettag.ListFilter (search / status / source / class /
 * primary-only) plus paging. */
export interface TagListParams {
  search?: string
  status?: TagStatus | ""
  source?: TagSource | ""
  feature_class_code?: string
  primary_only?: boolean
  limit?: number
  offset?: number
}

/** One page of the console plus the unpaged total. Mirrors assettag.ListResult. */
export interface TagListResult {
  rows: TagRow[]
  total: number
  limit: number
  offset: number
}

/** The console's KPI strip. Mirrors assettag.StatsResult. */
export interface TagStats {
  total: number
  active: number
  void: number
  superseded: number
  unprinted: number
  external: number
  generated: number
}

/** What resolving a scanned code returns: the exact tag, the feature it points
 * to (the full read envelope, same shape as GET /features/{code}/{id}), and
 * every tag on that feature. Mirrors assettag.Resolution. */
export interface TagResolution {
  tag: AssetTag
  feature: Record<string, unknown>
  tags: AssetTag[]
}

const TAGS_KEY = ["asset-tags"] as const

/** Resolve a scanned / typed code to its feature. Backs the /t deep-link page.
 * Uses the ?code= query form (not the /{code} path) so codes with URL-reserved
 * characters resolve cleanly. A miss / retired / out-of-scope code throws a 404
 * ApiError -- the page shows a friendly "not found" rather than leaking whether
 * the asset exists. No retry: a 404 is a definitive answer, not a transient. */
export function useResolveTag(code: string | undefined) {
  return useQuery({
    queryKey: [...TAGS_KEY, "resolve", code ?? ""],
    queryFn: () => apiFetch<TagResolution>(`/tags?code=${encodeURIComponent(code!)}`),
    enabled: !!code,
    retry: false,
    staleTime: 0,
  })
}

/** Every tag on a feature, primary first. Backs the feature panel's Tags tab.
 * Disabled until both ids are known. */
export function useFeatureTags(featureClassCode: string | undefined, featureId: string | undefined) {
  const params = new URLSearchParams()
  if (featureClassCode) params.set("feature_class_code", featureClassCode)
  if (featureId) params.set("feature_id", featureId)
  return useQuery({
    queryKey: [...TAGS_KEY, "feature", featureClassCode ?? "", featureId ?? ""],
    queryFn: () => apiFetch<TagsResponse>(`/tags?${params.toString()}`),
    enabled: !!featureClassCode && !!featureId,
    staleTime: 15_000,
  })
}

/** Fetch the one printable (primary) tag per feature for a set of features of ONE
 * class -- the data behind the /features browser's bulk label print. A feature
 * with no active tag is simply absent from the result, so the caller compares
 * the returned count against how many it asked for to note the shortfall. Read
 * (tags:read): it hits the GET ?feature_ids= form and chunks large selections so
 * the query string never overflows. Exposed as a mutation because it fires
 * on-demand from a button, not reactively. */
const PRIMARY_BATCH_CHUNK = 120

export function usePrimaryTagsBatch() {
  return useMutation<AssetTag[], ApiError, { featureClassCode: string; featureIds: string[] }>({
    mutationFn: async ({ featureClassCode, featureIds }) => {
      const out: AssetTag[] = []
      for (let i = 0; i < featureIds.length; i += PRIMARY_BATCH_CHUNK) {
        const slice = featureIds.slice(i, i + PRIMARY_BATCH_CHUNK)
        const params = new URLSearchParams()
        params.set("feature_class_code", featureClassCode)
        params.set("feature_ids", slice.join(","))
        const res = await apiFetch<TagsResponse>(`/tags?${params.toString()}`)
        out.push(...res.tags)
      }
      return out
    },
  })
}

/** Browse / search the whole tag population -- the Admin → Asset Tags console.
 * Hits GET /tags?view=list; the server scopes it to the caller's territories and
 * private-draft visibility, so a scoped user only sees tags they could resolve.
 * keepPreviousData keeps the table populated while a filter or page changes,
 * avoiding a flash to the loading state. */
export function useTagList(params: TagListParams) {
  const qs = new URLSearchParams({ view: "list" })
  if (params.search) qs.set("search", params.search)
  if (params.status) qs.set("status", params.status)
  if (params.source) qs.set("source", params.source)
  if (params.feature_class_code) qs.set("feature_class_code", params.feature_class_code)
  if (params.primary_only) qs.set("primary_only", "true")
  if (params.limit != null) qs.set("limit", String(params.limit))
  if (params.offset != null) qs.set("offset", String(params.offset))
  return useQuery({
    queryKey: [...TAGS_KEY, "list", qs.toString()],
    queryFn: () => apiFetch<TagListResult>(`/tags?${qs.toString()}`),
    placeholderData: (prev) => prev,
    staleTime: 10_000,
  })
}

/** The console's KPI strip. Hits GET /tags?view=stats, scoped like useTagList so
 * the counts match what the table can show. */
export function useTagStats() {
  return useQuery({
    queryKey: [...TAGS_KEY, "stats"],
    queryFn: () => apiFetch<TagStats>(`/tags?view=stats`),
    staleTime: 10_000,
  })
}

// --- Mutations -------------------------------------------------------------
// All gated tags:write on the server; the tab self-gates the controls. Every
// mutation invalidates the whole tag cache so the feature's list reflects the
// new primary / status / reissued code.

function useTagsMutation<TBody>(fn: (body: TBody) => Promise<unknown>) {
  const queryClient = useQueryClient()
  return useMutation<unknown, ApiError, TBody>({
    mutationFn: fn,
    onSuccess: () => queryClient.invalidateQueries({ queryKey: TAGS_KEY }),
  })
}

/** Register a pre-existing / legacy asset number against a feature, optionally
 * promoting it to the feature's primary. */
export interface RegisterTagBody {
  feature_class_code: string
  feature_id: string
  tag_code: string
  make_primary?: boolean
}

export function useRegisterTag() {
  return useTagsMutation<RegisterTagBody>((body) =>
    apiFetch("/tags", { method: "POST", body: JSON.stringify(body) }),
  )
}

/** Promote a tag to its feature's primary (demotes whichever held it). */
export function useSetPrimaryTag() {
  return useTagsMutation<{ code: string }>(({ code }) =>
    apiFetch(`/tags/${encodeURIComponent(code)}/primary`, { method: "POST" }),
  )
}

/** Retire a tag (a damaged/wrong label). The primary can't be voided directly. */
export function useVoidTag() {
  return useTagsMutation<{ code: string }>(({ code }) =>
    apiFetch(`/tags/${encodeURIComponent(code)}/void`, { method: "POST" }),
  )
}

/** Mint a fresh code for the same feature and supersede this one -- the
 * damaged-label replacement path. Primary carries over if this tag held it. */
export function useReissueTag() {
  return useTagsMutation<{ code: string }>(({ code }) =>
    apiFetch(`/tags/${encodeURIComponent(code)}/reissue`, { method: "POST" }),
  )
}

/** Bump print_count when a label is sent to print. Best-effort: the caller fires
 * it as the print sheet goes to the browser's print dialog and ignores failures
 * (a tags:read-only user can still print the label, it just won't count). */
export function useMarkPrinted() {
  return useTagsMutation<{ code: string }>(({ code }) =>
    apiFetch(`/tags/${encodeURIComponent(code)}/printed`, { method: "POST" }),
  )
}

/** Batch twin of useMarkPrinted: bump print_count for a whole sheet of codes in
 * one request when a bulk label sheet goes to print. Write (tags:write), so the
 * caller only fires it for a tags:write user -- a read-only user can still print
 * the sheet, it just won't count. */
export function useMarkManyPrinted() {
  return useTagsMutation<{ codes: string[] }>((body) =>
    apiFetch(`/tags/printed`, { method: "POST", body: JSON.stringify(body) }),
  )
}
