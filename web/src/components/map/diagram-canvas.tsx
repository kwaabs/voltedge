import { useEffect, useMemo, useRef, useState } from "react"
import { useQueries, useQueryClient } from "@tanstack/react-query"
import { Loader2, Plus, X } from "lucide-react"

import { Button } from "@/components/ui/button"
import { type Connection } from "@/hooks/use-connections"
import { useAllSymbology } from "@/hooks/use-all-symbology"
import { type DiagramPlacement, useDiagramPlacements } from "@/hooks/use-diagrams"
import { type FeatureRecord } from "@/hooks/use-feature"
import { type FeatureSearchResult, MIN_QUERY_LENGTH, useFeatureNameSearch } from "@/hooks/use-feature-name-search"
import { ApiError, apiFetch } from "@/lib/api"
import { iconDataUri } from "@/lib/symbology-icons"
import { resolveSymbology } from "@/lib/symbology"
import { cn } from "@/lib/utils"
import { useDiagramStore } from "@/stores/diagram-store"
import { useSelectionStore } from "@/stores/selection-store"
import { useToastStore } from "@/stores/toast-store"

interface ConnectionsResponse {
  connections: Connection[]
}

// Placement x/y/rotation is an arbitrary local coordinate space, chosen
// by whoever placed each feature (docs/phase-5-diagrams.md) -- there's
// no fixed "one unit = one pixel" relationship the way a map's real
// geometry has. Sizing icons/text/padding as a fraction of the
// placements' own bounding-box extent, not a hardcoded pixel-like
// constant, keeps them legible regardless of what coordinate scale the
// diagram was authored in (fixed constants against a viewBox whose
// scale is data-dependent were unreadable in practice -- oversized
// text swamping the icons for a diagram spanning a few hundred units).
const MIN_EXTENT = 100
const ICON_FRACTION = 0.12
const PADDING_FRACTION = 0.25
const FONT_FRACTION = 0.035

function placementKey(featureClass: string, featureId: string): string {
  return `${featureClass}:${featureId}`
}

/** A routed, right-angle connector between two placements -- a real
 * schematic's wires are drawn, not simply present between two points
 * (see docs/phase-5-diagrams.md's "Drag-to-reposition and routed
 * edges, added later"). Bends once, at the horizontal midpoint between
 * the two ends -- the simplest orthogonal route, not the multi-segment
 * bus-tap routing a dense protection-scheme diagram would eventually
 * want (still open, see the same doc section). */
function elbowPath(x1: number, y1: number, x2: number, y2: number): string {
  const midX = (x1 + x2) / 2
  return `M ${x1} ${y1} L ${midX} ${y1} L ${midX} ${y2} L ${x2} ${y2}`
}

/** Where a newly-added feature lands -- just outside the current
 * bounding box, spaced by a fraction of its own extent, so successive
 * additions spread out left-to-right instead of stacking on top of
 * each other. There's no drag-to-reposition yet (docs/open-items.md),
 * so this is only a starting point, not a final layout decision. */
function nextPlacementPosition(placements: DiagramPlacement[]): { x: number; y: number } {
  if (placements.length === 0) return { x: 200, y: 150 }
  const xs = placements.map((p) => p.x)
  const ys = placements.map((p) => p.y)
  const minX = Math.min(...xs)
  const maxX = Math.max(...xs)
  const minY = Math.min(...ys)
  const maxY = Math.max(...ys)
  const extent = Math.max(maxX - minX, maxY - minY, MIN_EXTENT)
  return { x: maxX + extent * 0.3, y: minY }
}

/**
 * Search-and-add for an existing feature -- placing a *new*,
 * geometry-less feature straight from the canvas is a separate, bigger
 * piece of work (docs/phase-5-diagrams.md's "What's still open"); this
 * covers the smaller, more common case of putting an already-mapped
 * feature onto the schematic too. Reuses the exact same cross-class
 * name search feature-search.tsx's map toolbar box uses. Results
 * already on this diagram are filtered out -- re-adding one would just
 * move it, which isn't what "add a feature" implies here (moving is
 * the still-open drag-to-reposition item).
 */
function AddFeatureButton({ diagramId, placements }: { diagramId: string; placements: DiagramPlacement[] }) {
  const [open, setOpen] = useState(false)
  const [query, setQuery] = useState("")
  const [placing, setPlacing] = useState(false)
  const { results, loading } = useFeatureNameSearch(query)
  const containerRef = useRef<HTMLDivElement>(null)
  const queryClient = useQueryClient()

  useEffect(() => {
    if (!open) return
    function handlePointerDown(e: PointerEvent) {
      if (containerRef.current && !containerRef.current.contains(e.target as Node)) setOpen(false)
    }
    window.addEventListener("pointerdown", handlePointerDown)
    return () => window.removeEventListener("pointerdown", handlePointerDown)
  }, [open])

  const placedKeys = useMemo(
    () => new Set(placements.map((p) => placementKey(p.feature_class, p.feature_id))),
    [placements],
  )
  const addableResults = results.filter((r) => !placedKeys.has(placementKey(r.featureClassCode, r.featureId)))

  async function handleAdd(row: FeatureSearchResult) {
    setPlacing(true)
    const { x, y } = nextPlacementPosition(placements)
    try {
      await apiFetch(`/diagrams/${diagramId}/placements`, {
        method: "POST",
        body: JSON.stringify({ feature_class: row.featureClassCode, feature_id: row.featureId, x, y, rotation: 0 }),
      })
      queryClient.invalidateQueries({ queryKey: ["diagram-placements", diagramId] })
      setOpen(false)
      setQuery("")
    } catch (err) {
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not add this feature to the diagram.")
    } finally {
      setPlacing(false)
    }
  }

  return (
    <div ref={containerRef} className="relative">
      <Button variant="ghost" size="icon" className="size-7" title="Add feature" onClick={() => setOpen((o) => !o)}>
        <Plus className="size-4" />
        <span className="sr-only">Add feature</span>
      </Button>
      {open && (
        <div className="bg-popover text-popover-foreground absolute top-full right-0 z-10 mt-1 w-72 rounded-md border shadow-lg">
          <div className="p-2">
            <input
              autoFocus
              type="text"
              className="border-input bg-background h-8 w-full rounded-md border px-2 text-sm outline-none focus-visible:border-ring focus-visible:ring-ring/50 focus-visible:ring-[3px]"
              placeholder="Search by name…"
              value={query}
              onChange={(e) => setQuery(e.target.value)}
            />
          </div>
          <div className="max-h-64 overflow-y-auto border-t">
            {query.trim().length < MIN_QUERY_LENGTH ? (
              <p className="text-muted-foreground p-3 text-sm">Type a feature's name to add it here.</p>
            ) : loading ? (
              <div className="text-muted-foreground flex items-center gap-2 p-3 text-sm">
                <Loader2 className="size-4 animate-spin" />
                Searching…
              </div>
            ) : addableResults.length === 0 ? (
              <p className="text-muted-foreground p-3 text-sm">No matching features not already on this diagram.</p>
            ) : (
              addableResults.map((row) => (
                <button
                  key={`${row.featureClassCode}:${row.featureId}`}
                  type="button"
                  disabled={placing}
                  onClick={() => handleAdd(row)}
                  className="hover:bg-accent hover:text-accent-foreground block w-full border-b p-2 text-left text-sm transition-colors last:border-b-0 disabled:opacity-50"
                >
                  <p className="truncate font-medium">{row.name}</p>
                  <p className="text-muted-foreground truncate text-xs">{row.className}</p>
                </button>
              ))
            )}
          </div>
        </div>
      )}
    </div>
  )
}

/**
 * The side-by-side diagram panel -- a hand-rolled SVG canvas, not a
 * graph library (React Flow, Fabric.js) -- reusing symbology-icons.ts's
 * iconDataUri directly, same as map-layers.ts, rather than translating
 * resolved symbology into a second component library's node shape. Only
 * ever mounted while diagram-store.ts's openDiagramId is set; map.tsx
 * owns showing/hiding it alongside VoltEdgeMap.
 *
 * Each placement's own feature record and connectivity are fetched
 * individually (useQueries, same pattern use-all-symbology.ts uses for
 * per-class schema fetches) -- a diagram placement only ever records
 * x/y/rotation, not a feature's name/subtype/attributes or its
 * connections, so both have to come from the same real endpoints the
 * map and feature panel already use, not duplicated here.
 */
interface DragState {
  placementId: string
  featureClass: string
  featureId: string
  offsetX: number
  offsetY: number
  x: number
  y: number
  moved: boolean
}

export function DiagramCanvas() {
  const openDiagramId = useDiagramStore((s) => s.openDiagramId)
  const close = useDiagramStore((s) => s.close)
  const { data: placementsData, isPending, isError } = useDiagramPlacements(openDiagramId ?? undefined)
  const rules = useAllSymbology()
  const placements = useMemo(() => placementsData?.placements ?? [], [placementsData])
  const svgRef = useRef<SVGSVGElement>(null)
  const [dragState, setDragState] = useState<DragState | null>(null)
  const queryClient = useQueryClient()

  // Screen-pixel pointer movement has to be converted into the SVG's
  // own viewBox coordinate space before it means anything as a new
  // x/y -- the viewBox is data-dependent (see the extent-relative
  // sizing comment above) and the SVG element's actual on-screen size
  // rarely matches it 1:1, so a raw clientX/clientY delta would drag a
  // feature faster or slower than the cursor depending on how zoomed
  // in the current diagram happens to be.
  function clientToSvgPoint(clientX: number, clientY: number): { x: number; y: number } | null {
    const svg = svgRef.current
    const ctm = svg?.getScreenCTM()
    if (!svg || !ctm) return null
    const point = svg.createSVGPoint()
    point.x = clientX
    point.y = clientY
    const transformed = point.matrixTransform(ctm.inverse())
    return { x: transformed.x, y: transformed.y }
  }

  function handlePlacementPointerDown(e: React.PointerEvent<SVGGElement>, placement: DiagramPlacement) {
    const svgPoint = clientToSvgPoint(e.clientX, e.clientY)
    if (!svgPoint) return
    e.currentTarget.setPointerCapture(e.pointerId)
    setDragState({
      placementId: placement.id,
      featureClass: placement.feature_class,
      featureId: placement.feature_id,
      offsetX: placement.x - svgPoint.x,
      offsetY: placement.y - svgPoint.y,
      x: placement.x,
      y: placement.y,
      moved: false,
    })
  }

  function handlePlacementPointerMove(e: React.PointerEvent<SVGGElement>) {
    if (!dragState) return
    const svgPoint = clientToSvgPoint(e.clientX, e.clientY)
    if (!svgPoint) return
    setDragState((prev) => prev && { ...prev, x: svgPoint.x + prev.offsetX, y: svgPoint.y + prev.offsetY, moved: true })
  }

  // A pointerup with no real movement is a click, not a drag -- select
  // the feature, same as before drag existed. A real drag writes the
  // new position back immediately: an optimistic update to the query
  // cache first (so the node doesn't visibly snap back to its old spot
  // while the request is in flight), then the real POST upsert this
  // endpoint has supported since the backend slice, then only
  // reconciles from the server on failure.
  async function handlePlacementPointerUp() {
    if (!dragState || !openDiagramId) return
    const { placementId, featureClass, featureId, x, y, moved } = dragState
    if (!moved) {
      setDragState(null)
      useSelectionStore.getState().select(featureClass, featureId)
      return
    }
    const rotation = placements.find((p) => p.id === placementId)?.rotation ?? 0
    queryClient.setQueryData<{ placements: DiagramPlacement[] }>(["diagram-placements", openDiagramId], (old) =>
      old ? { placements: old.placements.map((p) => (p.id === placementId ? { ...p, x, y } : p)) } : old,
    )
    setDragState(null)
    try {
      await apiFetch(`/diagrams/${openDiagramId}/placements`, {
        method: "POST",
        body: JSON.stringify({ feature_class: featureClass, feature_id: featureId, x, y, rotation }),
      })
    } catch (err) {
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not move this feature.")
      queryClient.invalidateQueries({ queryKey: ["diagram-placements", openDiagramId] })
    }
  }

  // The placement actively being dragged renders at the live pointer
  // position rather than its last-saved one -- bounds, edges, and the
  // node itself all read from this instead of `placements` directly,
  // so a wire visibly follows the feature it's attached to while it
  // moves, not just after the drag ends.
  const effectivePlacements = useMemo(() => {
    if (!dragState) return placements
    return placements.map((p) => (p.id === dragState.placementId ? { ...p, x: dragState.x, y: dragState.y } : p))
  }, [placements, dragState])

  const featureResults = useQueries({
    queries: placements.map((p) => ({
      queryKey: ["feature", p.feature_class, p.feature_id],
      queryFn: () => apiFetch<FeatureRecord>(`/features/${p.feature_class}/${p.feature_id}`),
    })),
  })
  const connectionResults = useQueries({
    queries: placements.map((p) => ({
      queryKey: ["connections", p.feature_class, p.feature_id],
      queryFn: () =>
        apiFetch<ConnectionsResponse>(
          `/connectivity?feature_class=${encodeURIComponent(p.feature_class)}&feature_id=${encodeURIComponent(p.feature_id)}`,
        ),
    })),
  })

  const placementByKey = useMemo(() => {
    const map = new Map<string, DiagramPlacement>()
    effectivePlacements.forEach((p) => map.set(placementKey(p.feature_class, p.feature_id), p))
    return map
  }, [effectivePlacements])

  // Only edges where both ends are placed on this same diagram render --
  // a feature can have real connectivity reaching outside the diagram
  // entirely (e.g. a conductor leaving the substation yard), which has
  // nowhere sensible to draw to here.
  const edges = useMemo(() => {
    const seen = new Set<string>()
    const out: { id: string; from: DiagramPlacement; to: DiagramPlacement }[] = []
    for (const result of connectionResults) {
      for (const conn of result.data?.connections ?? []) {
        if (seen.has(conn.id)) continue
        const from = placementByKey.get(placementKey(conn.from.feature_class, conn.from.feature_id))
        const to = placementByKey.get(placementKey(conn.to.feature_class, conn.to.feature_id))
        if (!from || !to) continue
        seen.add(conn.id)
        out.push({ id: conn.id, from, to })
      }
    }
    return out
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [connectionResults.map((r) => r.dataUpdatedAt).join(","), placementByKey])

  const bounds = useMemo(() => {
    if (effectivePlacements.length === 0) return { minX: 0, minY: 0, maxX: 400, maxY: 300 }
    const xs = effectivePlacements.map((p) => p.x)
    const ys = effectivePlacements.map((p) => p.y)
    return { minX: Math.min(...xs), maxX: Math.max(...xs), minY: Math.min(...ys), maxY: Math.max(...ys) }
  }, [effectivePlacements])

  if (!openDiagramId) return null

  const extent = Math.max(bounds.maxX - bounds.minX, bounds.maxY - bounds.minY, MIN_EXTENT)
  const padding = extent * PADDING_FRACTION
  const iconSize = extent * ICON_FRACTION
  const fontSize = extent * FONT_FRACTION
  const viewBox = `${bounds.minX - padding} ${bounds.minY - padding} ${bounds.maxX - bounds.minX + padding * 2} ${bounds.maxY - bounds.minY + padding * 2}`

  return (
    <div className="bg-background flex h-full w-full min-w-0 flex-col border-l">
      <div className="flex items-center justify-between border-b px-3 py-2">
        <p className="text-sm font-medium">Diagram</p>
        <div className="flex items-center gap-1">
          <AddFeatureButton diagramId={openDiagramId} placements={placements} />
          <Button variant="ghost" size="icon" className="size-7" onClick={close}>
            <X className="size-4" />
            <span className="sr-only">Close diagram</span>
          </Button>
        </div>
      </div>
      <div className="min-h-0 flex-1 overflow-auto">
        {isPending ? (
          <div className="text-muted-foreground p-4 text-sm">Loading diagram...</div>
        ) : isError ? (
          <div className="text-destructive p-4 text-sm">Could not load this diagram.</div>
        ) : placements.length === 0 ? (
          <div className="text-muted-foreground p-4 text-sm">Nothing placed on this diagram yet.</div>
        ) : (
          <svg ref={svgRef} viewBox={viewBox} className="h-full w-full">
            {edges.map((edge) => (
              <path
                key={edge.id}
                d={elbowPath(edge.from.x, edge.from.y, edge.to.x, edge.to.y)}
                fill="none"
                style={{ stroke: "var(--muted-foreground)" }}
                strokeWidth={2}
              />
            ))}
            {effectivePlacements.map((placement, i) => {
              const feature = featureResults[i]?.data
              const classRules = rules[placement.feature_class]
              const def =
                resolveSymbology(classRules, feature?.subtype, feature?.attributes ?? {}, "diagram") ??
                resolveSymbology(classRules, feature?.subtype, feature?.attributes ?? {}, "map")
              const icon = iconDataUri(def?.icon_ref, def?.color)
              const dragging = dragState?.placementId === placement.id
              return (
                <g
                  key={placement.id}
                  transform={`translate(${placement.x}, ${placement.y})`}
                  className={cn("touch-none", dragging ? "cursor-grabbing" : "cursor-grab")}
                  onPointerDown={(e) => handlePlacementPointerDown(e, placement)}
                  onPointerMove={handlePlacementPointerMove}
                  onPointerUp={handlePlacementPointerUp}
                  onPointerCancel={() => setDragState(null)}
                >
                  <g transform={`rotate(${placement.rotation})`}>
                    <image href={icon} x={-iconSize / 2} y={-iconSize / 2} width={iconSize} height={iconSize} />
                  </g>
                  <text
                    x={0}
                    y={iconSize / 2 + fontSize * 1.4}
                    textAnchor="middle"
                    style={{ fill: "var(--foreground)", fontSize }}
                  >
                    {feature?.name || placement.feature_class}
                  </text>
                </g>
              )
            })}
          </svg>
        )}
      </div>
    </div>
  )
}
