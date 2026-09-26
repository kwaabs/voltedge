import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"

import { apiFetch } from "@/lib/api"

export interface DiagramFeatureRef {
  feature_class: string
  feature_id: string
}

export interface DiagramInstance {
  id: string
  container: DiagramFeatureRef
  diagram_type: string
  name: string | null
  /** True when this diagram was auto-generated from a template (an assembly
   * single-line) and can be rebuilt via useRegenerateDiagram; false for a
   * hand-built diagram. The canvas shows the Regenerate control only when true. */
  regenerable?: boolean
  created_at: string
}

/** What POST /diagrams/{id}/regenerate returns: the rebuilt diagram is a NEW
 * instance (the old one is replaced), so callers must switch to instance_id.
 * placed/unbound/skipped report a partial rebuild (a slot left with no matching
 * component, or a component a placement rule rejected). */
export interface RegenerateDiagramResult {
  instance_id: string
  placed: number
  unbound: number
  skipped: number
}

/** A single vertex in a drawn line placement's path -- [x, y] in the
 * diagram's own local coordinate space (Phase 12, option B2). */
export type DiagramPathPoint = [number, number]

export interface DiagramPlacement {
  id: string
  feature_class: string
  feature_id: string
  x: number
  y: number
  rotation: number
  /** For a drawn line placement (migrations/0033): the ordered polyline
   * vertices in the diagram's local coordinate space. Absent for a point
   * placement, or a line placed without a hand-drawn path (which the
   * canvas renders from connectivity instead). */
  path?: DiagramPathPoint[]
  /** When true, the canvas omits this placement's name label (migration
   * 0054). Toggled per-feature via PATCH; independent of geometry. */
  label_hidden?: boolean
  /** Per-placement label transform (migration 0056): offset from the
   * label's natural anchor in the glyph-local frame (so it rotates with the
   * glyph), the label's own rotation in degrees, and a font-size multiplier.
   * Absent/0/0/0/1 = the default pinned position. Owned by PATCH, never the
   * place/move upsert, so arranging a label survives a feature drag/rotate. */
  label_dx?: number
  label_dy?: number
  label_rotation?: number
  label_scale?: number
}

/** The non-feature-backed furniture on a diagram (Phase 24 keystone): a
 * grouping box, a free text label, a schematic-only line, and later title
 * blocks / legends / template slots. Unlike a placement it points at no real
 * feature -- it exists only on the canvas, in the diagram's own local x/y
 * space. Slice 1 uses the text/box/line kinds; the rest are model-ready. */
export type DiagramElementKind = "text" | "box" | "line" | "symbol" | "bus" | "tap" | "control" | "title_block" | "legend" | "slot"

export interface DiagramElement {
  id: string
  kind: DiagramElementKind
  x: number
  y: number
  /** Box-like kinds only (box/title_block/legend); absent for text/line. */
  width?: number
  height?: number
  rotation: number
  /** kind === "line": the ordered polyline vertices in local space. */
  path?: DiagramPathPoint[]
  /** Text content for text kinds; the label a box/line may carry. */
  text?: string | null
  /** Kind-specific rendering hints (stroke, dash, fill, colour, fontSize) --
   * always an object, never null on the wire. */
  style: Record<string, unknown>
  z_index: number
  slot_class?: string | null
  slot_subtype?: number | null
  slot_role?: string | null
  bound_class?: string | null
  bound_id?: string | null
}

interface DiagramsResponse {
  diagrams: DiagramInstance[]
}

interface PlacementsResponse {
  placements: DiagramPlacement[]
}

interface ElementsResponse {
  elements: DiagramElement[]
}

/**
 * Every diagram instance hosted by one container feature -- a
 * SUBSTATION can host more than one (a one-line and a
 * control-scheme diagram for the same substation is a real case per
 * docs/metadata-core.md), so this is a list, not a single fetch.
 * Backs the "View diagram" trigger in feature-panel.tsx: only shown
 * when the selected feature's class has is_diagram_container = true,
 * and even then only meaningful once this list is non-empty.
 */
export function useDiagramInstances(featureClassCode: string | undefined, featureId: string | undefined) {
  return useQuery({
    queryKey: ["diagrams", featureClassCode, featureId],
    queryFn: () =>
      apiFetch<DiagramsResponse>(
        `/diagrams?container_feature_class=${encodeURIComponent(featureClassCode ?? "")}&container_feature_id=${encodeURIComponent(featureId ?? "")}`,
      ),
    enabled: Boolean(featureClassCode && featureId),
  })
}

/**
 * One diagram instance by id -- the "View diagram" trigger's own list
 * already has this diagram's summary, but once diagram-store.ts opens
 * it by id alone the canvas needs its own fetch to know that diagram's
 * name (or lack of one) for its header.
 */
export function useDiagramInstance(diagramId: string | undefined) {
  return useQuery({
    queryKey: ["diagram-instance", diagramId],
    queryFn: () => apiFetch<DiagramInstance>(`/diagrams/${diagramId}`),
    enabled: Boolean(diagramId),
  })
}

interface CreateDiagramInstanceBody {
  container: DiagramFeatureRef
  diagram_type: string
  name?: string
}

/**
 * Creates the first (or a further) diagram instance for a container
 * feature -- api/internal/diagram's POST /diagrams was already a real,
 * generically-callable endpoint (curl-verified since Phase 5), just
 * never wired to any UI trigger. Invalidates this container's own
 * diagrams list on success so "View diagram" (feature-panel.tsx) picks
 * the new instance up immediately, no reload.
 */
export function useCreateDiagramInstance() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (body: CreateDiagramInstanceBody) =>
      apiFetch<DiagramInstance>("/diagrams", { method: "POST", body: JSON.stringify(body) }),
    onSuccess: (_data, variables) => {
      queryClient.invalidateQueries({
        queryKey: ["diagrams", variables.container.feature_class, variables.container.feature_id],
      })
    },
  })
}

/**
 * Deletes a whole diagram instance (cascades to its placements server-side).
 * Gated behind diagrams:delete -- the server refuses the DELETE for anyone
 * without it (supervisor/admin/break-glass-admin only, migration 0078), and the
 * only caller (diagram-canvas.tsx) hides its button unless usePermission agrees,
 * so the two stay in step. Invalidates every container's diagram list (we don't
 * know the container here, and the lists are small) so "View diagram" drops the
 * deleted instance immediately.
 */
export function useDeleteDiagramInstance() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (diagramId: string) => apiFetch<void>(`/diagrams/${diagramId}`, { method: "DELETE" }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["diagrams"] })
    },
  })
}

/**
 * Rebuild an auto-generated assembly single-line from the template it was built
 * from (POST /diagrams/{id}/regenerate) -- back to the canonical layout, re-
 * binding the same components. The rebuild REPLACES the instance, so the result
 * carries a NEW instance_id the caller must switch to. Rides the /diagrams mount's
 * diagrams:write gate (a POST). Invalidates every container's diagram list so the
 * replaced instance drops out and the fresh one appears; the caller navigates to
 * result.instance_id.
 */
export function useRegenerateDiagram() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (diagramId: string) =>
      apiFetch<RegenerateDiagramResult>(`/diagrams/${diagramId}/regenerate`, { method: "POST" }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["diagrams"] })
    },
  })
}

/**
 * Every feature placed on one diagram instance, with its own local
 * x/y/rotation -- entirely independent of whatever real geom the same
 * feature might also have on the map (docs/phase-5-diagrams.md). Backs
 * diagram-canvas.tsx.
 */
export function useDiagramPlacements(diagramId: string | undefined) {
  return useQuery({
    queryKey: ["diagram-placements", diagramId],
    queryFn: () => apiFetch<PlacementsResponse>(`/diagrams/${diagramId}/placements`),
    enabled: Boolean(diagramId),
  })
}

/**
 * Every non-feature element on one diagram -- annotation boxes, free text,
 * schematic-only lines (Phase 24 keystone). Fetched alongside the placements
 * the canvas already loads; drawn back-to-front by z_index (the API already
 * orders them that way). Separate query key from placements so a placement
 * move doesn't refetch elements and vice versa.
 */
export function useDiagramElements(diagramId: string | undefined) {
  return useQuery({
    queryKey: ["diagram-elements", diagramId],
    queryFn: () => apiFetch<ElementsResponse>(`/diagrams/${diagramId}/elements`),
    enabled: Boolean(diagramId),
  })
}
