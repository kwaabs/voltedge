import { useQuery } from "@tanstack/react-query"

import { apiFetch } from "@/lib/api"

/**
 * One reusable diagram layout skeleton in the library (Phase 24 Slice 4). The
 * full payload (its slots + furniture) stays on the server -- this summary is
 * only what the "apply" picker renders: the name, an optional description, and
 * how much it contains (slots to bind + total elements). Authored by "save as
 * template" and applied onto a fresh diagram.
 */
export interface DiagramTemplateSummary {
  id: string
  name: string
  description?: string | null
  element_count: number
  slot_count: number
  created_at: string
}

interface TemplatesResponse {
  templates: DiagramTemplateSummary[]
}

/**
 * The whole template library (GET /diagrams/templates), newest first. Not
 * long-cached the way stencils/icons are: the list changes the moment a user
 * saves or deletes a template, and the save/apply mutations invalidate this
 * key so the picker stays current. Returns the raw list.
 */
export function useDiagramTemplates(): DiagramTemplateSummary[] {
  const { data } = useQuery({
    queryKey: ["diagram-templates"],
    queryFn: () => apiFetch<TemplatesResponse>("/diagrams/templates"),
  })
  return data?.templates ?? []
}
