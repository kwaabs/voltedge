import { useQuery } from "@tanstack/react-query"

import { apiFetch } from "@/lib/api"

/**
 * Wire types for /docs -- api/internal/docs's Doc / docContent. The catalog
 * (GET /docs) is already filtered by the server to the manuals this user may
 * read; required_permission is still returned so the viewer can badge the
 * administrative ones. GET /docs/{id} adds the raw Markdown in `content`.
 */
export interface DocSummary {
  id: string
  title: string
  description: string
  category: string
  /** An RBAC permission key the reader must hold; absent for general manuals.
   * The server enforces it -- this is only here so the UI can label the doc. */
  required_permission?: string
}

export interface DocContent extends DocSummary {
  content: string
}

/** The manuals the current user is allowed to open. Server-filtered, so the
 * list never advertises a document a direct fetch would refuse. */
export function useDocs() {
  return useQuery({
    queryKey: ["docs", "list"],
    queryFn: () => apiFetch<{ docs: DocSummary[] }>("/docs"),
    select: (data) => data.docs,
  })
}

/** One manual's Markdown, fetched only when a document is open. */
export function useDoc(id: string | null) {
  return useQuery({
    queryKey: ["docs", "content", id],
    queryFn: () => apiFetch<DocContent>(`/docs/${id}`),
    enabled: !!id,
  })
}
