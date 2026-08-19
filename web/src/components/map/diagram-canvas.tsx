import { useEffect, useMemo, useRef, useState } from "react"
import { useQueries, useQueryClient } from "@tanstack/react-query"
import { Hand, Loader2, Magnet, Maximize2, MousePointer2, Plus, X, ZoomIn, ZoomOut } from "lucide-react"

import { Button } from "@/components/ui/button"
import { DigitizeFeatureButton } from "@/components/map/digitize-feature-tool"
import { type PendingRemoval, RemovePlacementDialog } from "@/components/map/remove-placement-dialog"
import { type Connection } from "@/hooks/use-connections"
import { useAllSymbology } from "@/hooks/use-all-symbology"
import { useFeatureClasses } from "@/hooks/use-feature-classes"
import { type DiagramInstance, type DiagramPlacement, useDiagramInstance, useDiagramPlacements } from "@/hooks/use-diagrams"
import { type FeatureRecord } from "@/hooks/use-feature"
import { type FeatureSearchResult, MIN_QUERY_LENGTH, useFeatureNameSearch } from "@/hooks/use-feature-name-search"
import { useSymbologyIcons } from "@/hooks/use-symbology-icons"
import { ApiError, apiFetch } from "@/lib/api"
import { type HighlightRole, HIGHLIGHT_COLORS } from "@/lib/map-layers"
import { iconDataUri } from "@/lib/symbology-icons"
import { resolveSymbology } from "@/lib/symbology"
import { cn } from "@/lib/utils"
import { useConnectStore } from "@/stores/connect-store"
import { useDiagramStore } from "@/stores/diagram-store"
import { useDrawStore } from "@/stores/draw-store"
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
const ZOOM_FACTOR = 1.25
const SNAP_THRESHOLD_FRACTION = 0.03

function placementKey(featureClass: string, featureId: string): string {
  return `${featureClass}:${featureId}`
}

/** Placement-to-placement alignment snapping while dragging (see the
 * view toolbar's Magnet toggle) -- snaps to another placement's exact
 * x or y, independently per axis, whichever's within `threshold`.
 * Deliberately not a fixed grid: a one-line diagram's meaningful
 * alignment is "level with this other device," not an arbitrary grid
 * cell. */
function snapCoordinate(value: number, candidates: number[], threshold: number): number {
  let snapped = value
  let bestDist = threshold
  for (const c of candidates) {
    const dist = Math.abs(c - value)
    if (dist < bestDist) {
      bestDist = dist
      snapped = c
    }
  }
  return snapped
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

/**
 * Select vs. pan tool, zoom in/out/fit, and a placement-alignment snap
 * toggle. The diagram canvas has its own independent camera (see
 * DiagramCanvas's `camera`/`fitBox` state) rather than the browser's
 * native scroll -- it needs its own small set of controls for the same
 * ideas the map's OL pan/zoom gets for free, since an SVG viewBox has
 * no built-in equivalent. Positioned like the map's own zoom buttons
 * (top-left corner of the canvas), not the header, since it's about
 * navigating the canvas itself rather than the diagram as a whole.
 */
function DiagramViewToolbar({
  mode,
  onModeChange,
  onZoomIn,
  onZoomOut,
  onFit,
  snapEnabled,
  onToggleSnap,
}: {
  mode: "select" | "pan"
  onModeChange: (mode: "select" | "pan") => void
  onZoomIn: () => void
  onZoomOut: () => void
  onFit: () => void
  snapEnabled: boolean
  onToggleSnap: () => void
}) {
  const activeClass = "bg-accent text-accent-foreground"
  return (
    <div className="bg-popover text-popover-foreground absolute top-2 left-2 z-10 flex items-center gap-0.5 rounded-md border p-0.5 shadow-sm">
      <Button
        variant="ghost"
        size="icon"
        className={cn("size-7", mode === "select" && activeClass)}
        title="Select"
        onClick={() => onModeChange("select")}
      >
        <MousePointer2 className="size-4" />
        <span className="sr-only">Select</span>
      </Button>
      <Button
        variant="ghost"
        size="icon"
        className={cn("size-7", mode === "pan" && activeClass)}
        title="Pan"
        onClick={() => onModeChange("pan")}
      >
        <Hand className="size-4" />
        <span className="sr-only">Pan</span>
      </Button>
      <div className="bg-border mx-0.5 h-5 w-px" />
      <Button variant="ghost" size="icon" className="size-7" title="Zoom in" onClick={onZoomIn}>
        <ZoomIn className="size-4" />
        <span className="sr-only">Zoom in</span>
      </Button>
      <Button variant="ghost" size="icon" className="size-7" title="Zoom out" onClick={onZoomOut}>
        <ZoomOut className="size-4" />
        <span className="sr-only">Zoom out</span>
      </Button>
      <Button variant="ghost" size="icon" className="size-7" title="Fit to view" onClick={onFit}>
        <Maximize2 className="size-4" />
        <span className="sr-only">Fit to view</span>
      </Button>
      <div className="bg-border mx-0.5 h-5 w-px" />
      <Button
        variant="ghost"
        size="icon"
        className={cn("size-7", snapEnabled && activeClass)}
        title={snapEnabled ? "Snapping on" : "Snapping off"}
        onClick={onToggleSnap}
      >
        <Magnet className="size-4" />
        <span className="sr-only">{snapEnabled ? "Snapping on" : "Snapping off"}</span>
      </Button>
    </div>
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
  const connectSource = useConnectStore((s) => s.source)
  const connectTarget = useConnectStore((s) => s.target)
  const { data: placementsData, isPending, isError } = useDiagramPlacements(openDiagramId ?? undefined)
  const { data: featureClassesData } = useFeatureClasses()
  const rules = useAllSymbology()
  const symbologyIcons = useSymbologyIcons()
  const placements = useMemo(() => placementsData?.placements ?? [], [placementsData])
  const svgRef = useRef<SVGSVGElement>(null)
  const [dragState, setDragState] = useState<DragState | null>(null)
  const [rotateState, setRotateState] = useState<RotateState | null>(null)
  const queryClient = useQueryClient()

  // The canvas's own camera, independent of the map's OL pan/zoom and
  // of the always-auto-fit viewBox this used to compute on every
  // render -- see the "fit-to-bounds happens once per diagram" effect
  // below for why. "select" is the default tool; "pan" hands drag
  // gestures to the camera instead of to a placement (see
  // handlePlacementPointerDown's own mode guard).
  const [canvasMode, setCanvasMode] = useState<"select" | "pan">("select")
  const [snapEnabled, setSnapEnabled] = useState(true)
  const [camera, setCamera] = useState<{ x: number; y: number; w: number; h: number } | null>(null)
  const [panDrag, setPanDrag] = useState<{
    startClientX: number
    startClientY: number
    startCamera: { x: number; y: number; w: number; h: number }
  } | null>(null)
  const lastFitDiagramRef = useRef<string | null>(null)

  // A placement/conductor's highlight role, if any -- reuses the exact
  // same roles and colors the map's own highlight layer uses
  // (map-layers.ts's HIGHLIGHT_COLORS) so a selected or connect-mode
  // source/target feature reads identically whether it's shown on the
  // map or the diagram.
  function roleFor(featureClass: string, featureId: string): HighlightRole | null {
    if (selected?.featureClassCode === featureClass && selected?.featureId === featureId) return "selected"
    if (connectSource?.featureClassCode === featureClass && connectSource?.featureId === featureId) return "connect-source"
    if (connectTarget?.featureClassCode === featureClass && connectTarget?.featureId === featureId) return "connect-target"
    return null
  }

  // A plain click on a placement either picks it as a connect-mode
  // target (if a connect/relate workflow is waiting on one -- see
  // connect-store.ts, mirroring voltedge-map.tsx's own click handler)
  // or falls back to the normal global selection every other click
  // already used. Bails out entirely while a create-draft is open (see
  // draw-store.ts), same guard voltedge-map.tsx's own click handler
  // applies -- a click shouldn't silently change what's selected out
  // from under an in-progress, unsaved create form.
  function selectOrPickTarget(featureClass: string, featureId: string) {
    if (useDrawStore.getState().draft) return
    const connectState = useConnectStore.getState()
    if (connectState.mode && !connectState.target) {
      connectState.pickTarget({ featureClassCode: featureClass, featureId })
      return
    }
    useSelectionStore.getState().select(featureClass, featureId)
  }

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

  // Escape while armed (waiting on a canvas click) cancels back to
  // idle. Escape once a draft actually exists -- the attribute form is
  // open, rendered by feature-panel.tsx/feature-create-form.tsx, not
  // this component -- is handled globally in voltedge-map.tsx, the same
  // place that already handles it for the map's own draw flow.
  useEffect(() => {
    if (!digitizingClass) return
    function handleKeyDown(e: KeyboardEvent) {
      if (e.key === "Escape") setDigitizingClass(null)
    }
    window.addEventListener("keydown", handleKeyDown)
    return () => window.removeEventListener("keydown", handleKeyDown)
  }, [digitizingClass])

  // Captures the clicked point and hands off to draw-store.ts's
  // startDiagramDraft -- feature-panel.tsx picks the resulting draft up
  // from there and renders feature-create-form.tsx, the exact same
  // attribute form the map's own draw flow uses (see that file's own
  // doc comment for why this isn't a second, bespoke form). Guarded
  // against the pan tool: a pan drag ends with a plain `click` event
  // too, which would otherwise place a feature at wherever the drag
  // happened to end.
  function handleCanvasClick(e: React.MouseEvent<SVGSVGElement>) {
    if (!digitizingClass || e.target !== e.currentTarget || canvasMode === "pan") return
    const svgPoint = clientToSvgPoint(e.clientX, e.clientY)
    if (!svgPoint || !openDiagramId) return
    useDrawStore.getState().startDiagramDraft(digitizingClass, { diagramId: openDiagramId, x: svgPoint.x, y: svgPoint.y })
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

  // No-ops entirely while the pan tool is active -- the pointerdown
  // isn't captured or prevented here, so it bubbles up to the SVG's own
  // pan handler instead (see handleCanvasPanPointerDown), letting a
  // drag that starts on top of a placement still pan the canvas rather
  // than move the feature.
  function handlePlacementPointerDown(e: React.PointerEvent<SVGGElement>, placement: DiagramPlacement) {
    if (canvasMode === "pan") return
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

  // Snapping (the view toolbar's Magnet toggle) aligns the dragged
  // placement's x or y, independently per axis, to any other
  // placement's exact x or y within a threshold scaled to the
  // diagram's own current extent -- the same extent-relative sizing
  // reasoning icon/font sizing already uses, so the snap distance feels
  // consistent regardless of what coordinate scale the diagram was
  // authored in.
  function handlePlacementPointerMove(e: React.PointerEvent<SVGGElement>) {
    if (!dragState) return
    const svgPoint = clientToSvgPoint(e.clientX, e.clientY)
    if (!svgPoint) return
    let x = svgPoint.x + dragState.offsetX
    let y = svgPoint.y + dragState.offsetY
    if (snapEnabled) {
      const ext = Math.max(bounds.maxX - bounds.minX, bounds.maxY - bounds.minY, MIN_EXTENT)
      const threshold = ext * SNAP_THRESHOLD_FRACTION
      const others = placements.filter((p) => p.id !== dragState.placementId)
      x = snapCoordinate(x, others.map((p) => p.x), threshold)
      y = snapCoordinate(y, others.map((p) => p.y), threshold)
    }
    setDragState((prev) => prev && { ...prev, x, y, moved: true })
  }

  // A pointerup with no real movement is a click, not a drag -- select
  // the feature (or, mid connect/relate workflow, pick it as the
  // target), same as before drag existed. A real drag writes the new
  // position back immediately: an optimistic update to the query cache
  // first (so the node doesn't visibly snap back to its old spot while
  // the request is in flight), then the real POST upsert this endpoint
  // has supported since the backend slice, then only reconciles from
  // the server on failure.
  async function handlePlacementPointerUp() {
    if (!dragState || !openDiagramId) return
    const { placementId, featureClass, featureId, x, y, moved } = dragState
    if (!moved) {
      setDragState(null)
      selectOrPickTarget(featureClass, featureId)
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
    if (canvasMode === "pan") return
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

  // Zooms the camera by `factor` (< 1 zooms in, > 1 zooms out) around
  // `center` (a diagram-space point) if given, or the current view's
  // own center otherwise -- the same "keep this point fixed under the
  // cursor" math a map or image editor's scroll-to-zoom uses. Clamped
  // relative to fitBox's own width so zooming stays sane regardless of
  // how far the user has already gone, in either direction.
  function zoomBy(factor: number, center?: { x: number; y: number }) {
    const current = camera ?? fitBox
    const cx = center?.x ?? current.x + current.w / 2
    const cy = center?.y ?? current.y + current.h / 2
    const newW = Math.min(Math.max(current.w * factor, fitBox.w * 0.04), fitBox.w * 20)
    const newH = current.h * (newW / current.w)
    const fracX = (cx - current.x) / current.w
    const fracY = (cy - current.y) / current.h
    setCamera({ x: cx - fracX * newW, y: cy - fracY * newH, w: newW, h: newH })
  }

  function handleWheelZoom(e: React.WheelEvent<SVGSVGElement>) {
    e.preventDefault()
    const cursor = clientToSvgPoint(e.clientX, e.clientY)
    zoomBy(e.deltaY < 0 ? 1 / ZOOM_FACTOR : ZOOM_FACTOR, cursor ?? undefined)
  }

  // The pan tool's own drag, on the SVG element itself rather than a
  // placement's <g> -- active only in "pan" mode (handlePlacementPointerDown
  // no-ops and lets the same pointerdown bubble up here when it starts
  // on top of a placement). Converts the screen-pixel drag delta into
  // diagram-space units via the current CTM's own scale, the same
  // conversion clientToSvgPoint uses for a single point.
  function handleCanvasPanPointerDown(e: React.PointerEvent<SVGSVGElement>) {
    if (canvasMode !== "pan") return
    e.currentTarget.setPointerCapture(e.pointerId)
    setPanDrag({ startClientX: e.clientX, startClientY: e.clientY, startCamera: camera ?? fitBox })
  }

  function handleCanvasPanPointerMove(e: React.PointerEvent<SVGSVGElement>) {
    if (!panDrag) return
    const svg = svgRef.current
    const ctm = svg?.getScreenCTM()
    if (!svg || !ctm) return
    const dx = (e.clientX - panDrag.startClientX) / ctm.a
    const dy = (e.clientY - panDrag.startClientY) / ctm.d
    setCamera({ ...panDrag.startCamera, x: panDrag.startCamera.x - dx, y: panDrag.startCamera.y - dy })
  }

  function handleCanvasPanPointerUp() {
    setPanDrag(null)
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

  // The auto-fit-to-bounds rect -- what the viewBox used to always be,
  // on every render. Still what "Fit to view" (the toolbar's Maximize2
  // button) recomputes on demand, and what the camera below initializes
  // to; it just no longer drives the viewBox directly on every render.
  const fitBox = useMemo(() => {
    const ext = Math.max(bounds.maxX - bounds.minX, bounds.maxY - bounds.minY, MIN_EXTENT)
    const pad = ext * PADDING_FRACTION
    return {
      x: bounds.minX - pad,
      y: bounds.minY - pad,
      w: bounds.maxX - bounds.minX + pad * 2,
      h: bounds.maxY - bounds.minY + pad * 2,
    }
  }, [bounds])

  // Fits to bounds once per diagram, the moment its placements finish
  // loading -- not on every render the way the viewBox used to. A
  // manual pan/zoom should stick instead of snapping back to fit the
  // instant a placement moves and `bounds` recomputes; `lastFitDiagramRef`
  // is what makes this "once," even though `fitBox` itself (an effect
  // dependency) keeps changing on every placement move.
  useEffect(() => {
    if (isPending || !openDiagramId || lastFitDiagramRef.current === openDiagramId) return
    lastFitDiagramRef.current = openDiagramId
    setCamera(fitBox)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [openDiagramId, isPending, fitBox])

  if (!openDiagramId) return null

  const extent = Math.max(bounds.maxX - bounds.minX, bounds.maxY - bounds.minY, MIN_EXTENT)
  const iconSize = extent * ICON_FRACTION
  const fontSize = extent * FONT_FRACTION
  const removeSize = iconSize * 0.18
  const view = camera ?? fitBox
  const viewBox = `${view.x} ${view.y} ${view.w} ${view.h}`

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
          <>
            <DiagramViewToolbar
              mode={canvasMode}
              onModeChange={setCanvasMode}
              onZoomIn={() => zoomBy(1 / ZOOM_FACTOR)}
              onZoomOut={() => zoomBy(ZOOM_FACTOR)}
              onFit={() => setCamera(fitBox)}
              snapEnabled={snapEnabled}
              onToggleSnap={() => setSnapEnabled((s) => !s)}
            />
            <svg
              ref={svgRef}
              viewBox={viewBox}
              className={cn(
                "h-full w-full",
                digitizingClass && "cursor-crosshair",
                canvasMode === "pan" && (panDrag ? "cursor-grabbing" : "cursor-grab"),
              )}
              onClick={handleCanvasClick}
              onWheel={handleWheelZoom}
              onPointerDown={handleCanvasPanPointerDown}
              onPointerMove={handleCanvasPanPointerMove}
              onPointerUp={handleCanvasPanPointerUp}
              onPointerCancel={handleCanvasPanPointerUp}
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
              const role = roleFor(item.placement.feature_class, item.placement.feature_id)
              return (
                <g key={item.placement.id}>
                  {role && (
                    <path
                      d={elbowPath(p1.x, p1.y, p2.x, p2.y)}
                      fill="none"
                      style={{ stroke: HIGHLIGHT_COLORS[role], opacity: 0.4 }}
                      strokeWidth={Math.max(fontSize * 1.1, 5)}
                    />
                  )}
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
                        ? () => selectOrPickTarget(item.placement.feature_class, item.placement.feature_id)
                        : handlePlacementPointerUp
                    }
                    onPointerCancel={() => setDragState(null)}
                  >
                    {/* A loose end (not yet fully connected) still needs a
                      drag/click target at this midpoint, just not a
                      visible dot -- fill stays transparent, pointerEvents
                      keeps it interactive despite that. */}
                    {!item.fullyConnected && <circle r={iconSize * 0.22} style={{ fill: "transparent", pointerEvents: "all" }} />}
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
              const icon = iconDataUri(def?.icon_ref, def?.color, symbologyIcons)
              const dragging = dragState?.placementId === placement.id
              const role = roleFor(placement.feature_class, placement.feature_id)
              const isSelected = role === "selected"
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
                  {role && (
                    <circle
                      r={iconSize * 0.62}
                      style={{ fill: `${HIGHLIGHT_COLORS[role]}26`, stroke: HIGHLIGHT_COLORS[role] }}
                      strokeWidth={Math.max(fontSize * 0.4, 1.5)}
                    />
                  )}
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
          </>
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
