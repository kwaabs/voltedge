import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"

import { ApiError, apiFetch } from "@/lib/api"

/**
 * Wire types + hooks for the external-links API
 * (api/internal/externallink, mounted at /feature-links behind
 * features:read / features:write). An external link is one outbound
 * pointer from a feature to an external system. A feature can have
 * arbitrarily many, so this is a list per feature, addressed by the
 * (class code, feature id) pair like every other feature-scoped resource.
 *
 * Two kinds share the list:
 *  - `hyperlink` -- an http/https url the panel opens in a new tab.
 *  - `api` (Slice 3) -- points at a catalogued external system + a
 *    resource path; its live data is fetched server-side via
 *    `GET /feature-links/{id}/data` (see useExternalLinkData) and shown
 *    as a data card.
 */
export interface ExternalLink {
  id: string
  kind: "hyperlink" | "api"
  label: string
  url?: string
  system_id?: string
  system_code?: string
  system_name?: string
  resource_path?: string
  created_at: string
}

/** The normalized payload GET /feature-links/{id}/data returns. `data`
 * is whatever the upstream system returned (JSON as-is, or XML decoded
 * into a nested object), so it's rendered generically. */
export interface ExternalLinkData {
  system: { code: string; name: string }
  format: "json" | "xml"
  fetched_at: string
  data: unknown
}

/** The body accepted by POST /feature-links -- a hyperlink carries a
 * url; an api link carries a system_id + resource_path. */
export type CreateLinkInput =
  | { kind?: "hyperlink"; label: string; url: string }
  | { kind: "api"; label: string; system_id: string; resource_path: string }

interface LinksResponse {
  links: ExternalLink[]
}

function linksKey(featureClass: string | undefined, featureId: string | undefined) {
  return ["external-links", featureClass, featureId] as const
}

/** Every outbound link attached to one feature, oldest first. */
export function useExternalLinks(featureClass: string | undefined, featureId: string | undefined) {
  return useQuery<LinksResponse, ApiError>({
    queryKey: linksKey(featureClass, featureId),
    queryFn: () =>
      apiFetch<LinksResponse>(
        `/feature-links?feature_class=${encodeURIComponent(featureClass ?? "")}&feature_id=${encodeURIComponent(featureId ?? "")}`,
      ),
    enabled: Boolean(featureClass && featureId),
    retry: false,
  })
}

/** Creates a link (hyperlink or api) for a feature, then refreshes its
 * link list. */
export function useCreateExternalLink(featureClass: string, featureId: string) {
  const queryClient = useQueryClient()
  return useMutation<ExternalLink, ApiError, CreateLinkInput>({
    mutationFn: (input) =>
      apiFetch<ExternalLink>(`/feature-links`, {
        method: "POST",
        body: JSON.stringify({ feature_class: featureClass, feature_id: featureId, ...input }),
      }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: linksKey(featureClass, featureId) }),
  })
}

/** Fetches an api link's live data through the server-side proxy. Kept
 * off until `enabled` flips (the panel loads it on demand, not on every
 * render), and never auto-retries so an upstream 502 surfaces at once. */
export function useExternalLinkData(linkId: string, enabled: boolean) {
  return useQuery<ExternalLinkData, ApiError>({
    queryKey: ["external-link-data", linkId],
    queryFn: () => apiFetch<ExternalLinkData>(`/feature-links/${linkId}/data`),
    enabled,
    staleTime: 60_000,
    retry: false,
  })
}

/** Removes a link, then refreshes the feature's list. */
export function useDeleteExternalLink(featureClass: string, featureId: string) {
  const queryClient = useQueryClient()
  return useMutation<void, ApiError, string>({
    mutationFn: (linkId) => apiFetch<void>(`/feature-links/${linkId}`, { method: "DELETE" }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: linksKey(featureClass, featureId) }),
  })
}

/**
 * The external_ref map on a feature (migration 0145): the ids this feature carries
 * in OTHER systems (SCADA / AMI / CIS), keyed by system code. Distinct from the
 * links above -- a link is a stored pointer; external_ref is the correlation source
 * of truth the reverse index and the live-fetch below both derive from. One entry
 * per system, each with the correlation `id` (the scalar, or the object's "id")
 * and the raw `value` so an object ref round-trips edits.
 */
export interface ExternalRefEntry {
  system: string
  id: string
  has_id: boolean
  value: unknown
}

interface FeatureRefsResponse {
  refs: ExternalRefEntry[]
}

function refsKey(featureClass: string | undefined, featureId: string | undefined) {
  return ["external-refs", featureClass, featureId] as const
}

/** A feature's whole external_ref map, one entry per system (code order). */
export function useFeatureExternalRefs(featureClass: string | undefined, featureId: string | undefined) {
  return useQuery<FeatureRefsResponse, ApiError>({
    queryKey: refsKey(featureClass, featureId),
    queryFn: () =>
      apiFetch<FeatureRefsResponse>(
        `/feature-links/external-refs?feature_class=${encodeURIComponent(featureClass ?? "")}&feature_id=${encodeURIComponent(featureId ?? "")}`,
      ),
    enabled: Boolean(featureClass && featureId),
    retry: false,
  })
}

/** Replaces a feature's whole external_ref map (PUT), then refreshes it. The body
 * carries the full next map keyed by system code -- the caller merges/removes and
 * sends the result, same contract as the attributes edit. */
export function useSetFeatureExternalRefs(featureClass: string, featureId: string) {
  const queryClient = useQueryClient()
  return useMutation<FeatureRefsResponse, ApiError, Record<string, unknown>>({
    mutationFn: (externalRef) =>
      apiFetch<FeatureRefsResponse>(`/feature-links/external-refs`, {
        method: "PUT",
        body: JSON.stringify({ feature_class: featureClass, feature_id: featureId, external_ref: externalRef }),
      }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: refsKey(featureClass, featureId) }),
  })
}

/** Live data for one of a feature's external_ref systems, fetched server-side via
 * GET /feature-links/external (fills the system's path_template from the ref value
 * and proxies the request). Kept off until `enabled` flips (loaded on demand), and
 * never auto-retries so an upstream failure surfaces at once. Reuses the same
 * normalized shape as a stored api link's data. */
export function useFeatureExternalData(
  featureClass: string,
  featureId: string,
  system: string,
  enabled: boolean,
) {
  return useQuery<ExternalLinkData, ApiError>({
    queryKey: ["feature-external-data", featureClass, featureId, system],
    queryFn: () =>
      apiFetch<ExternalLinkData>(
        `/feature-links/external?feature_class=${encodeURIComponent(featureClass)}&feature_id=${encodeURIComponent(featureId)}&system=${encodeURIComponent(system)}`,
      ),
    enabled,
    staleTime: 60_000,
    retry: false,
  })
}
