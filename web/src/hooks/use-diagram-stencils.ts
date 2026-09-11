import { useQuery } from "@tanstack/react-query"

import { apiFetch } from "@/lib/api"

/**
 * One entry in the diagram symbol stencil palette (Phase 24 Slice 2). The
 * glyph SVG is NOT here -- it's the shared metadata.symbology_icons asset
 * useSymbologyIcons already loads, resolved by icon_ref via iconDataUri, so
 * the palette reuses that registry rather than re-fetching markup. This is
 * only the catalog: which icon, and how the picker labels/groups it.
 */
export interface DiagramStencil {
  icon_ref: string
  label: string
  category: string
  sort_order: number
  /** The feature class code a stamped symbol of this stencil can be promoted
   * into (Phase 25). Present only for promotable stencils; absent (decoration
   * only) for the rest. Drives the double-click "promote to a feature" action
   * on the canvas. */
  promote_class?: string
}

interface StencilsResponse {
  stencils: DiagramStencil[]
}

/**
 * The whole stencil palette at once (GET /symbology/stencils), a single
 * long-cached global fetch like useSymbologyIcons -- a small, class-agnostic
 * set the canvas needs available as soon as the palette opens. Returns the
 * raw list (already ordered by category then sort_order on the server); the
 * palette groups it by category for display.
 */
export function useDiagramStencils(): DiagramStencil[] {
  const { data } = useQuery({
    queryKey: ["diagram-stencils"],
    queryFn: () => apiFetch<StencilsResponse>("/symbology/stencils"),
    staleTime: 5 * 60_000,
  })
  return data?.stencils ?? []
}
