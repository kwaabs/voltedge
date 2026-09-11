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
