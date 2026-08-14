import { useEffect, useMemo, useRef, useState } from "react"
import { useQueries, useQueryClient } from "@tanstack/react-query"
import { Loader2, Plus, X } from "lucide-react"

import { Button } from "@/components/ui/button"
import { type PendingDigitize, DigitizeFeatureButton, DigitizeFeatureForm } from "@/components/map/digitize-feature-tool"
import { type PendingRemoval, RemovePlacementDialog } from "@/components/map/remove-placement-dialog"
import { type Connection } from "@/hooks/use-connections"
import { useAllSymbology } from "@/hooks/use-all-symbology"
import { useFeatureClasses } from "@/hooks/use-feature-classes"
import { type DiagramInstance, type DiagramPlacement, useDiagramInstance, useDiagramPlacements } from "@/hooks/use-diagrams"
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
// diagram was authored in.
const MIN_EXTENT = 100
const ICON_FRACTION = 0.12
const PADDING_FRACTION = 0.25
const FONT_FRACTION = 0.035
const ROTATE_SNAP_DEGREES = 15

function placementKey(featureClass: string, featureId: string): string {
  return `${featureClass}:${featureId}`
}

/** A routed, right-angle connector between two points -- a real
 * schematic's wires are drawn, not simply present between two points.
 * Bends once, at the horizontal midpoint -- the simplest orthogonal
 * route, not the multi-segment bus-tap routing a dense
 * protection-scheme diagram would eventually want (still open, see
 * docs/phase-5-diagrams.md). */
function elbowPath(x1: number, y1: number, x2: number, y2: number): string {
  const midX = (x1 + x2) / 2
  return `M ${x1} ${y1} L ${midX} ${y1} L ${midX} ${y2} L ${x2} ${y2}`
}

/** Where a newly-added feature lands -- just outside the current
 * bounding box, spaced by a fraction of its own extent, so successive
 * additions spread out left-to-right instead of stacking on top of
 * each other. Only a starting point: draggable afterward, and a
 * geometry_type = 'line' feature's own x/y stops mattering entirely
 * once both of its real connectivity endpoints are also placed (see
 * "conductors render as wires" below). */
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

/** The small "×" badge that appears on hover of a placement (point or
 * conductor) -- opens RemovePlacementDialog's confirm step rather than
 * un-placing immediately. An earlier version skipped the dialog
 * (reasoning it was cosmetic and one search-and-add away from
 * reversed), but a hover-revealed "x" alone turned out too easy to hit
 * by accident, so it gets the same explicit-choice guard every other
 * permanent-ish action in this app already has. */
function RemoveControl({ x, y, size, onRemove }: { x: number; y: number; size: number; onRemove: () => void }) {
  return (
    <g
      transform={`translate(${x}, ${y})`}
      className="cursor-pointer opacity-0 transition-opacity group-hover:opacity-100"
      onPointerDown={(e) => e.stopPropagation()}
      onClick={(e) => {
        e.stopPropagation()
        onRemove()
      }}
    >
      <circle r={size} style={{ fill: "var(--destructive)" }} />
      <line
        x1={-size * 0.5}
        y1={-size * 0.5}
        x2={size * 0.5}
        y2={size * 0.5}
        style={{ stroke: "var(--destructive-foreground)" }}
        strokeWidth={size * 0.3}
        strokeLinecap="round"
      />
      <line
        x1={-size * 0.5}
        y1={size * 0.5}
        x2={size * 0.5}
        y2={-size * 0.5}
        style={{ stroke: "var(--destructive-foreground)" }}
        strokeWidth={size * 0.3}
        strokeLinecap="round"
      />
    </g>
  )
}

/** The diagram's own editable name -- `runtime.diagram_instances.name`
 * has existed since the backend slice and was already in every API
 * response, just never shown (the header hardcoded the text
 * "Diagram") and never editable (no rename endpoint existed). Click
 * the title to edit; falls back to the diagram_type code when unnamed. */
function DiagramTitle({ diagramId }: { diagramId: string }) {
  const { data: instance } = useDiagramInstance(diagramId)
  const [editing, setEditing] = useState(false)
  const [draft, setDraft] = useState("")
  const queryClient = useQueryClient()

  const displayName = instance?.name || instance?.diagram_type || "Diagram"

  function startEditing() {
    setDraft(instance?.name ?? "")
    setEditing(true)
  }

  async function commit() {
    setEditing(false)
    const trimmed = draft.trim()
    const newName = trimmed.length > 0 ? trimmed : null
    if (newName === (instance?.name ?? null)) return
    try {
      const updated = await apiFetch<DiagramInstance>(`/diagrams/${diagramId}`, {
        method: "PATCH",
        body: JSON.stringify({ name: newName }),
      })
      queryClient.setQueryData(["diagram-instance", diagramId], updated)
    } catch (err) {
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not rename this diagram.")
    }
  }

  if (editing) {
    return (
      <input
        autoFocus
        value={draft}
        onChange={(e) => setDraft(e.target.value)}
        onBlur={commit}
        onKeyDown={(e) => {
          if (e.key === "Enter") e.currentTarget.blur()
          if (e.key === "Escape") setEditing(false)
        }}
        className="border-input bg-background h-7 min-w-0 flex-1 rounded-md border px-2 text-sm font-medium outline-none focus-visible:border-ring focus-visible:ring-ring/50 focus-visible:ring-[3px]"
      />
    )
  }

  return (
    <button
      type="button"
      onClick={startEditing}
      title="Rename diagram"
      className="hover:bg-accent -mx-1 min-w-0 truncate rounded-sm px-1 text-left text-sm font-medium"
    >
      {displayName}
    </button>
  )
}

/**
 * Search-and-add for an existing feature -- placing a *new*,
 * geometry-less feature straight from the canvas is a separate, bigger
 * piece of work (docs/phase-5-diagrams.md's "What's still open"); this
 * covers the smaller, more common case of putting an already-mapped
 * feature onto the schematic too. Reuses the exact same cross-class
 * name search feature-search.tsx's map toolbar box uses. Results
 * already on this diagram are filtered out -- re-adding one would just
 * move it, which isn't what "add a feature" implies here.
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
    <div ref={containerRef} className="relative shrink-0">
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

interface RotateState {
  placementId: string
  featureClass: string
  featureId: string
  centerX: number
  centerY: number
  rotation: number
}

/** One `geometry_type = 'line'` placement (a conductor), resolved
 * against its own real connectivity for where to draw its two ends --
 * see "conductors render as wires" below for why this exists at all. */
interface ConductorRender {
  placement: DiagramPlacement
  index: number
  points: [{ x: number; y: number }, { x: number; y: number }]
  fullyConnected: boolean
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
 *
 * Conductors render as wires, not point icons: a `geometry_type =
 * 'line'` feature class (e.g. MV_CONDUCTOR) is a physical wire, not a
 * device -- forcing it through the same point-icon path every junction
 * device uses put a circle in the middle of what should be one
 * continuous line, and a second, separate elbow was auto-drawn for
 * every underlying `feature_connectivity` row regardless of whether a
 * real conductor was even on the diagram. Both were wrong: a line-class
 * placement now resolves its own two connectivity endpoints and draws
 * *as* the line between them (styled with its own map/diagram color,
 * not an icon); the diagram no longer auto-draws a generic connector
 * for connectivity that isn't backed by an actual placed conductor --
 * if a wire isn't drawn, it isn't shown, matching how a real one-line
 * diagram works. A conductor with fewer than two endpoints placed on
 * this diagram falls back to a small draggable marker at its own
 * stored x/y (a loose end waiting for its other side to be added).
 */
export function DiagramCanvas() {
  const openDiagramId = useDiagramStore((s) => s.openDiagramId)
  const close = useDiagramStore((s) => s.close)
  const selected = useSelectionStore((s) => s.selected)
  const { data: placementsData, isPending, isError } = useDiagramPlacements(openDiagramId ?? undefined)
  const { data: featureClassesData } = useFeatureClasses()
  const rules = useAllSymbology()
  const placements = useMemo(() => placementsData?.placements ?? [], [placementsData])
  const svgRef = useRef<SVGSVGElement>(null)
  const [dragState, setDragState] = useState<DragState | null>(null)
  const [rotateState, setRotateState] = useState<RotateState | null>(null)
  const queryClient = useQueryClient()

  const geometryTypeByClass = useMemo(() => {
    const map: Record<string, string> = {}
    featureClassesData?.feature_classes.forEach((fc) => {
      map[fc.code] = fc.geometry_type
    })
    return map
  }, [featureClassesData])

  // The one hard rule the digitizing tool exists to respect
  // (docs/phase-5-diagrams.md): only geometry_type = 'none' classes can
  // ever be created from inside a diagram, since there's no way to give
  // a spatial class real map geometry from here.
  const geometryLessClasses = useMemo(
    () => featureClassesData?.feature_classes.filter((fc) => fc.geometry_type === "none") ?? [],
    [featureClassesData],
  )
  const [digitizingClass, setDigitizingClass] = useState<string | null>(null)
  const [pendingDigitize, setPendingDigitize] = useState<PendingDigitize | null>(null)

  useEffect(() => {
    if (!digitizingClass && !pendingDigitize) return
    function handleKeyDown(e: KeyboardEvent) {
      if (e.key === "Escape") {
        setPendingDigitize(null)
        setDigitizingClass(null)
      }
    }
    window.addEventListener("keydown", handleKeyDown)
    return () => window.removeEventListener("keydown", handleKeyDown)
  }, [digitizingClass, pendingDigitize])

  function handleCanvasClick(e: React.MouseEvent<SVGSVGElement>) {
    if (!digitizingClass || e.target !== e.currentTarget) return
    const svgPoint = clientToSvgPoint(e.clientX, e.clientY)
    if (!svgPoint) return
    setPendingDigitize({ featureClass: digitizingClass, x: svgPoint.x, y: svgPoint.y })
    setDigitizingClass(null)
  }

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

  // The rotate handle sits outside the icon's own rotate() transform
  // (see the render loop below) so it's always found in the same
  // predictable spot regardless of the feature's current orientation;
  // the new rotation is computed from the pointer's angle relative to
  // the placement's own center, snapped to ROTATE_SNAP_DEGREES for a
  // deliberate, schematic-appropriate feel rather than free-angle drift.
  function handleRotateHandlePointerDown(e: React.PointerEvent<SVGGElement>, placement: DiagramPlacement) {
    e.stopPropagation()
    e.currentTarget.setPointerCapture(e.pointerId)
    setRotateState({
      placementId: placement.id,
      featureClass: placement.feature_class,
      featureId: placement.feature_id,
      centerX: placement.x,
      centerY: placement.y,
      rotation: placement.rotation,
    })
  }

  function handleRotateHandlePointerMove(e: React.PointerEvent<SVGGElement>) {
    if (!rotateState) return
    const svgPoint = clientToSvgPoint(e.clientX, e.clientY)
    if (!svgPoint) return
    const dx = svgPoint.x - rotateState.centerX
    const dy = svgPoint.y - rotateState.centerY
    const angleDeg = Math.atan2(dy, dx) * (180 / Math.PI)
    // The icon's un-rotated (rotation = 0) orientation points "up",
    // which is -90 degrees in atan2's convention -- add 90 so a
    // handle dragged straight up yields rotation 0, not -90.
    const raw = (((angleDeg + 90) % 360) + 360) % 360
    const snapped = (Math.round(raw / ROTATE_SNAP_DEGREES) * ROTATE_SNAP_DEGREES) % 360
    setRotateState((prev) => prev && { ...prev, rotation: snapped })
  }

  async function handleRotateHandlePointerUp() {
    if (!rotateState || !openDiagramId) return
    const { placementId, featureClass, featureId, rotation } = rotateState
    const placement = placements.find((p) => p.id === placementId)
    setRotateState(null)
    if (!placement) return
    queryClient.setQueryData<{ placements: DiagramPlacement[] }>(["diagram-placements", openDiagramId], (old) =>
      old ? { placements: old.placements.map((p) => (p.id === placementId ? { ...p, rotation } : p)) } : old,
    )
    try {
      await apiFetch(`/diagrams/${openDiagramId}/placements`, {
        method: "POST",
        body: JSON.stringify({ feature_class: featureClass, feature_id: featureId, x: placement.x, y: placement.y, rotation }),
      })
    } catch (err) {
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not rotate this feature.")
      queryClient.invalidateQueries({ queryKey: ["diagram-placements", openDiagramId] })
    }
  }

  const [pendingRemoval, setPendingRemoval] = useState<PendingRemoval | null>(null)

  // The placement actively being dragged or rotated renders at its
  // live pointer-driven position/angle rather than its last-saved one
  // -- bounds, conductor endpoints, and the node itself all read from
  // this instead of `placements` directly, so a wire visibly follows
  // the feature it's attached to while it's still mid-drag.
  const effectivePlacements = useMemo(() => {
    let result = placements
    if (dragState) {
      result = result.map((p) => (p.id === dragState.placementId ? { ...p, x: dragState.x, y: dragState.y } : p))
    }
    if (rotateState) {
      result = result.map((p) => (p.id === rotateState.placementId ? { ...p, rotation: rotateState.rotation } : p))
    }
    return result
  }, [placements, dragState, rotateState])

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

  const pointItems = useMemo(
    () => effectivePlacements.map((p, i) => ({ placement: p, index: i })).filter((item) => geometryTypeByClass[item.placement.feature_class] !== "line"),
    [effectivePlacements, geometryTypeByClass],
  )

  // Resolves each conductor's real endpoints from its own connectivity
  // (not a generic connectivity-to-elbow pass over every placement --
  // see the component doc comment above for why that was wrong). A
  // conductor's connectivity is looked up by its own array index into
  // connectionResults, same index every other per-placement fetch here
  // uses.
  const conductorItems = useMemo<ConductorRender[]>(() => {
    const out: ConductorRender[] = []
    effectivePlacements.forEach((placement, i) => {
      if (geometryTypeByClass[placement.feature_class] !== "line") return
      const conns = connectionResults[i]?.data?.connections ?? []
      const seen = new Set<string>()
      const endpoints: { x: number; y: number }[] = []
      for (const conn of conns) {
        const otherSide =
          conn.from.feature_class === placement.feature_class && conn.from.feature_id === placement.feature_id
            ? conn.to
            : conn.from
        const key = placementKey(otherSide.feature_class, otherSide.feature_id)
        if (seen.has(key)) continue
        const otherPlacement = placementByKey.get(key)
        if (!otherPlacement) continue
        seen.add(key)
        endpoints.push({ x: otherPlacement.x, y: otherPlacement.y })
        if (endpoints.length === 2) break
      }
      const fullyConnected = endpoints.length === 2
      const points: [{ x: number; y: number }, { x: number; y: number }] = fullyConnected
        ? [endpoints[0], endpoints[1]]
        : endpoints.length === 1
          ? [endpoints[0], { x: placement.x, y: placement.y }]
          : [{ x: placement.x, y: placement.y }, { x: placement.x, y: placement.y }]
      out.push({ placement, index: i, points, fullyConnected })
    })
    return out
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [effectivePlacements, geometryTypeByClass, connectionResults.map((r) => r.dataUpdatedAt).join(","), placementByKey])

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
  const removeSize = iconSize * 0.18
  const viewBox = `${bounds.minX - padding} ${bounds.minY - padding} ${bounds.maxX - bounds.minX + padding * 2} ${bounds.maxY - bounds.minY + padding * 2}`

  return (
    <div className="bg-background flex h-full w-full min-w-0 flex-col border-l">
      <div className="flex items-center gap-2 border-b px-3 py-2">
        <DiagramTitle diagramId={openDiagramId} />
        <div className="ml-auto flex shrink-0 items-center gap-1">
          <DigitizeFeatureButton
            classes={geometryLessClasses}
            digitizingClass={digitizingClass}
            onStart={(code) => setDigitizingClass(code)}
            onCancel={() => setDigitizingClass(null)}
          />
          <AddFeatureButton diagramId={openDiagramId} placements={placements} />
          <Button variant="ghost" size="icon" className="size-7" onClick={close}>
            <X className="size-4" />
            <span className="sr-only">Close diagram</span>
          </Button>
        </div>
      </div>
      <div className="relative min-h-0 flex-1 overflow-auto">
        {isPending ? (
          <div className="text-muted-foreground p-4 text-sm">Loading diagram...</div>
        ) : isError ? (
          <div className="text-destructive p-4 text-sm">Could not load this diagram.</div>
        ) : (
          <svg
            ref={svgRef}
            viewBox={viewBox}
            className={cn("h-full w-full", digitizingClass && "cursor-crosshair")}
            onClick={handleCanvasClick}
          >
            {placements.length === 0 && (
              <text x={bounds.minX + (bounds.maxX - bounds.minX) / 2} y={bounds.minY + (bounds.maxY - bounds.minY) / 2} textAnchor="middle" style={{ fill: "var(--muted-foreground)", fontSize }}>
                Nothing placed on this diagram yet.
              </text>
            )}
            {conductorItems.map((item) => {
              const feature = featureResults[item.index]?.data
              const classRules = rules[item.placement.feature_class]
              const def =
                resolveSymbology(classRules, feature?.subtype, feature?.attributes ?? {}, "diagram") ??
                resolveSymbology(classRules, feature?.subtype, feature?.attributes ?? {}, "map")
              const stroke = def?.color ?? "var(--muted-foreground)"
              const [p1, p2] = item.points
              const midX = (p1.x + p2.x) / 2
              const midY = (p1.y + p2.y) / 2
              const dragging = dragState?.placementId === item.placement.id
              return (
                <g key={item.placement.id}>
                  <path d={elbowPath(p1.x, p1.y, p2.x, p2.y)} fill="none" style={{ stroke }} strokeWidth={Math.max(fontSize * 0.5, 2)} />
                  <g
                    transform={`translate(${midX}, ${midY})`}
                    className={cn(
                      "group touch-none",
                      item.fullyConnected ? "cursor-pointer" : dragging ? "cursor-grabbing" : "cursor-grab",
                    )}
                    onPointerDown={item.fullyConnected ? undefined : (e) => handlePlacementPointerDown(e, item.placement)}
                    onPointerMove={item.fullyConnected ? undefined : handlePlacementPointerMove}
                    onPointerUp={
                      item.fullyConnected
                        ? () => useSelectionStore.getState().select(item.placement.feature_class, item.placement.feature_id)
                        : handlePlacementPointerUp
                    }
                    onPointerCancel={() => setDragState(null)}
                  >
                    {!item.fullyConnected && <circle r={iconSize * 0.22} style={{ fill: stroke }} />}
                    <text
                      y={item.fullyConnected ? -fontSize * 0.6 : iconSize * 0.3 + fontSize * 1.1}
                      textAnchor="middle"
                      style={{ fill: "var(--foreground)", fontSize }}
                    >
                      {feature?.name || item.placement.feature_class}
                    </text>
                    <RemoveControl
                      x={removeSize * 2.2}
                      y={-removeSize * 2.2}
                      size={removeSize}
                      onRemove={() => setPendingRemoval({ id: item.placement.id, label: feature?.name || item.placement.feature_class })}
                    />
                  </g>
                </g>
              )
            })}
            {pointItems.map(({ placement, index }) => {
              const feature = featureResults[index]?.data
              const classRules = rules[placement.feature_class]
              const def =
                resolveSymbology(classRules, feature?.subtype, feature?.attributes ?? {}, "diagram") ??
                resolveSymbology(classRules, feature?.subtype, feature?.attributes ?? {}, "map")
              const icon = iconDataUri(def?.icon_ref, def?.color)
              const dragging = dragState?.placementId === placement.id
              const isSelected = selected?.featureClassCode === placement.feature_class && selected?.featureId === placement.feature_id
              return (
                <g
                  key={placement.id}
                  transform={`translate(${placement.x}, ${placement.y})`}
                  className={cn("group touch-none", dragging ? "cursor-grabbing" : "cursor-grab")}
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
                  {isSelected && (
                    <g>
                      <line
                        x1={0}
                        y1={-iconSize / 2}
                        x2={0}
                        y2={-iconSize / 2 - fontSize * 1.2}
                        style={{ stroke: "var(--ring)" }}
                        strokeWidth={1}
                      />
                      <g
                        transform={`translate(0, ${-iconSize / 2 - fontSize * 1.2})`}
                        className="cursor-grab touch-none"
                        onPointerDown={(e) => handleRotateHandlePointerDown(e, placement)}
                        onPointerMove={handleRotateHandlePointerMove}
                        onPointerUp={handleRotateHandlePointerUp}
                        onPointerCancel={() => setRotateState(null)}
                      >
                        <circle r={iconSize * 0.15} style={{ fill: "var(--background)", stroke: "var(--ring)" }} strokeWidth={1.5} />
                      </g>
                    </g>
                  )}
                  <RemoveControl
                    x={iconSize * 0.45}
                    y={-iconSize * 0.45}
                    size={removeSize}
                    onRemove={() => setPendingRemoval({ id: placement.id, label: feature?.name || placement.feature_class })}
                  />
                </g>
              )
            })}
          </svg>
        )}
        {pendingDigitize && (
          <DigitizeFeatureForm
            diagramId={openDiagramId}
            pending={pendingDigitize}
            onCancel={() => setPendingDigitize(null)}
            onDone={() => setPendingDigitize(null)}
          />
        )}
      </div>
      <RemovePlacementDialog
        diagramId={openDiagramId}
        placement={pendingRemoval}
        onOpenChange={(open) => !open && setPendingRemoval(null)}
      />
    </div>
  )
}
