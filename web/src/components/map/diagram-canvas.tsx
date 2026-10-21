import { forwardRef, useCallback, useEffect, useImperativeHandle, useMemo, useRef, useState } from "react"
import { useQueries, useQueryClient } from "@tanstack/react-query"
import { Cable, ChevronsLeft, ChevronsRight, CornerDownRight, Crosshair, Download, Grid3x3, Hand, LayoutTemplate, Link2, List, Loader2, Magnet, MapPin, Maximize, Maximize2, Minimize, Minus, MousePointer2, PenLine, Plus, Printer, RefreshCw, Replace, Shapes, Spline, Square, Table, Trash2, Type, Waypoints, X, Zap, ZoomIn, ZoomOut } from "lucide-react"

import { ConfirmDeleteDialog } from "@/components/admin/confirm-delete-dialog"
import { Button } from "@/components/ui/button"
import { DeleteDiagramDialog } from "@/components/map/delete-diagram-dialog"
import { type DiagramLayerEntry, DiagramLayersMenu } from "@/components/map/diagram-layers-menu"
import { type PendingRemoval, RemovePlacementDialog } from "@/components/map/remove-placement-dialog"
import { type PendingElementRemoval, RemoveElementDialog } from "@/components/map/remove-element-dialog"
import { StencilPalette } from "@/components/map/stencil-palette"
import { type Connection } from "@/hooks/use-connections"
import { type Relationship } from "@/hooks/use-relationships"
import { useAllSymbology } from "@/hooks/use-all-symbology"
import { useDiagramStencils } from "@/hooks/use-diagram-stencils"
import { useDiagramTemplates } from "@/hooks/use-diagram-templates"
import { type FeatureClassSummary, useFeatureClasses } from "@/hooks/use-feature-classes"
import {
  type DiagramElement,
  type DiagramElementKind,
  type DiagramFeatureRef,
  type DiagramInstance,
  type DiagramPathPoint,
  type DiagramPlacement,
  useDiagramElements,
  useDiagramInstance,
  useDiagramPlacements,
  useRegenerateDiagram,
} from "@/hooks/use-diagrams"
import { usePermission } from "@/hooks/use-auth"
import { useEditing } from "@/hooks/use-editing"
import { type FeatureRecord, useFeature } from "@/hooks/use-feature"
import { type FeatureSearchResult, MIN_QUERY_LENGTH, useFeatureNameSearch } from "@/hooks/use-feature-name-search"
import { type PanelOption, usePanelOptions } from "@/hooks/use-panels"
import { useSymbologyIcons } from "@/hooks/use-symbology-icons"
import { ApiError, apiFetch } from "@/lib/api"
import { downloadDiagramDxf } from "@/lib/diagram-dxf"
import { refKey, useEnergization } from "@/lib/energization"
import { DiagramPrintDialog } from "@/components/map/diagram-print-dialog"
import { offsetGeometry } from "@/lib/geometry"
import { type HighlightRole, HIGHLIGHT_COLORS } from "@/lib/map-layers"
import { iconDataUri } from "@/lib/symbology-icons"
import { resolveSymbology } from "@/lib/symbology"
import { cn } from "@/lib/utils"
import { type ConnectMode, useConnectStore } from "@/stores/connect-store"
import { diagramLayerKey, useDiagramLayersStore } from "@/stores/diagram-layers-store"
import { useDiagramPlaceStore } from "@/stores/diagram-place-store"
import { useDiagramStore } from "@/stores/diagram-store"
import { useEditModeStore } from "@/stores/edit-mode-store"
import { useDrawStore } from "@/stores/draw-store"
import { useFaultCurrentStore } from "@/stores/fault-current-store"
import { useIsolateStore } from "@/stores/isolate-store"
import { useLoadFlowStore } from "@/stores/load-flow-store"
import { type FeatureRef, useSelectionStore } from "@/stores/selection-store"
import { useToastStore } from "@/stores/toast-store"
import { useTraceStore } from "@/stores/trace-store"

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
// Grid cell size as a fraction of the content extent -- ~25 cells across,
// same extent-relative reasoning as ICON_FRACTION so the grid stays a
// sensible size whatever coordinate scale the diagram was authored in.
const GRID_FRACTION = 0.04
const ICON_FRACTION = 0.12
const PADDING_FRACTION = 0.25
const FONT_FRACTION = 0.035
const ROTATE_SNAP_DEGREES = 15
const ZOOM_FACTOR = 1.25
const SNAP_THRESHOLD_FRACTION = 0.03
// How far off the original a duplicated feature's map geometry lands (metres,
// EPSG:3857) -- mirrors feature-panel.tsx's COPY_OFFSET_METERS so a feature
// duplicated from a diagram is separated on the map exactly as one duplicated
// from the map is (docs/phase-20-split-copy.md).
const DUPLICATE_MAP_OFFSET_METERS = 5

// Phase 46 -- control/trip line semantics. A "signal" line (a hand-drawn
// `control` element, or a derived protection_trip/protection_sensing edge)
// renders dashed with an arrowhead, in a colour that reads as a control signal
// rather than a power wire. Keyed by a stable slug so one <marker> per colour is
// defined in <defs> and referenced by markerEnd. `control` is the freeform
// furniture (Slice 1); the two protection kinds are the derived lines (Slice 2).
const CONTROL_LINE_COLORS: Record<string, string> = {
  control: "var(--muted-foreground)",
  protection_trip: "var(--destructive)",
  protection_sensing: "#2563eb",
}
// The protection relationship kinds the canvas draws as derived control/trip
// signal lines between two placed features (Phase 46 Slice 2). Any other kind
// (containment, composite, panel_bay, ...) is structural, not a wire, so it is
// never drawn on the schematic.
const CONTROL_LINE_KINDS = new Set(["protection_trip", "protection_sensing"])

// Phase 48 Slice 1 -- declared normally-open-point (NOP) marker. On a one-line
// the safety-relevant thing to scan for is where the OPEN points are: normally-
// open ties, bus couplers, and bus selectors. So the badge marks only the
// exceptional case -- a switch whose `normal_state` (NORMAL_STATE domain:
// NORMALLY_OPEN / NORMALLY_CLOSED, seed/bus_coupler_fields.sql) is NORMALLY_OPEN
// -- with a bold amber ring, and leaves everything else unmarked. A closed or
// field-less switch is the assumed default, exactly as a real one-line omits it,
// so the network is never drowned in "normally closed" dots (which would just be
// the map-clutter problem re-created at symbol level).
//
// This is the *declared* design posture only -- deliberately NOT the live
// device_state (a "later nicety" the coupler seed left undone) nor powerflow
// energization; those drive an ACTUAL open/closed + energized render in a later
// slice. The marker means "normally open", never "open right now".
const NORMAL_OPEN_POINT_COLOR = "#f59e0b"

/** The declared switch posture for a placed feature, read from its own
 * `normal_state` attribute: "closed" | "open" | null (field absent or an
 * unrecognised value -> no badge). Pure display; distinct from the live switch
 * position and from powerflow energization, which arrive in a later slice. */
function declaredSwitchState(feature: FeatureRecord | undefined): "closed" | "open" | null {
  const raw = feature?.attributes?.normal_state
  if (raw === "NORMALLY_CLOSED") return "closed"
  if (raw === "NORMALLY_OPEN") return "open"
  return null
}

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

/** The point on segment a→b nearest to p, with its distance, in planar
 * diagram space. Clamps the projection to the segment so the result is a
 * real point ON the bar (mid-span included), never its infinite line --
 * this is what lets a new line snap anywhere along an existing busbar, not
 * just to its end vertices. */
function closestPointOnSegment(
  p: { x: number; y: number },
  a: DiagramPathPoint,
  b: DiagramPathPoint,
): { x: number; y: number; dist: number } {
  const [ax, ay] = a
  const [bx, by] = b
  const dx = bx - ax
  const dy = by - ay
  const len2 = dx * dx + dy * dy
  let t = len2 === 0 ? 0 : ((p.x - ax) * dx + (p.y - ay) * dy) / len2
  t = Math.max(0, Math.min(1, t))
  const cx = ax + t * dx
  const cy = ay + t * dy
  return { x: cx, y: cy, dist: Math.hypot(p.x - cx, p.y - cy) }
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

/** Locks one routed segment to horizontal or vertical (Slice 5 bus/tap
 * routing): snaps `next` onto whichever axis it lies closest to relative
 * to `from`, so a bus/tap is drawn as clean right-angle runs rather than
 * free diagonals -- the "routing" a busbar and its T-taps need that a plain
 * hand-drawn line does not. */
function orthoSnap(from: DiagramPathPoint, next: { x: number; y: number }): { x: number; y: number } {
  const dx = Math.abs(next.x - from[0])
  const dy = Math.abs(next.y - from[1])
  return dx >= dy ? { x: next.x, y: from[1] } : { x: from[0], y: next.y }
}

/** A {x,y} point to the [x, y] pair a stored path/vertex list uses. */
function pointToPair(p: { x: number; y: number }): DiagramPathPoint {
  return [p.x, p.y]
}

/** An SVG path straight through a hand-drawn polyline's own vertices --
 * a line placement's stored `path` (Phase 12, option B2), in the
 * diagram's local coordinate space. Unlike elbowPath (which invents an
 * orthogonal route between two connectivity endpoints), this draws
 * exactly what the user drew, vertex for vertex. */
function polylinePath(points: DiagramPathPoint[]): string {
  return points.map(([x, y], i) => `${i === 0 ? "M" : "L"} ${x} ${y}`).join(" ")
}

/** The middle vertex of a drawn path -- where its label/remove control
 * sit, the polyline analogue of the two-endpoint midpoint. */
function polylineMidpoint(points: DiagramPathPoint[]): { x: number; y: number } {
  const [x, y] = points[Math.floor(points.length / 2)]
  return { x, y }
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
  // Removing a diagram element is an edit -- the X never shows in view mode (S3).
  const editMode = useEditModeStore((s) => s.editMode)
  if (!editMode) return null
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

/** A hover-revealed "copy" badge on a point placement: duplicates the feature.
 * Opens the create form pre-seeded from this feature's attributes/subtype/name
 * (Phase 20's Duplicate spine, docs/phase-20-split-copy.md) and, on Save,
 * creates a brand-new feature and places it on this diagram a short offset off
 * the original. Mirrors RemoveControl's corner-button affordance but is neutral,
 * not destructive -- the conventional two-overlapping-squares "copy" glyph.
 * stopPropagation on pointerdown so clicking it never starts a placement drag. */
function DuplicateControl({ x, y, size, onDuplicate }: { x: number; y: number; size: number; onDuplicate: () => void }) {
  // Duplicating an element is an edit -- hidden in view mode (S3).
  const editMode = useEditModeStore((s) => s.editMode)
  if (!editMode) return null
  return (
    <g
      transform={`translate(${x}, ${y})`}
      className="cursor-pointer opacity-0 transition-opacity group-hover:opacity-100"
      onPointerDown={(e) => e.stopPropagation()}
      onClick={(e) => {
        e.stopPropagation()
        onDuplicate()
      }}
    >
      <title>Duplicate feature</title>
      <circle r={size} style={{ fill: "var(--background)", stroke: "var(--border)" }} strokeWidth={size * 0.14} />
      <rect
        x={-size * 0.55}
        y={-size * 0.55}
        width={size * 0.8}
        height={size * 0.8}
        rx={size * 0.12}
        fill="none"
        style={{ stroke: "var(--muted-foreground)" }}
        strokeWidth={size * 0.16}
      />
      <rect
        x={-size * 0.18}
        y={-size * 0.18}
        width={size * 0.8}
        height={size * 0.8}
        rx={size * 0.12}
        style={{ fill: "var(--background)", stroke: "var(--muted-foreground)" }}
        strokeWidth={size * 0.16}
      />
    </g>
  )
}

/** A hover-revealed eye toggle that hides/shows one placement's name label
 * (migration 0054). Mirrors RemoveControl's affordance (same corner-button
 * treatment, only visible on hover of the placement's group) but is neutral,
 * not destructive: a slash across the eye means the label is currently
 * hidden. stopPropagation on pointerdown so clicking it never starts a
 * placement drag. */
function LabelToggleControl({
  x,
  y,
  size,
  hidden,
  onToggle,
}: {
  x: number
  y: number
  size: number
  hidden: boolean
  onToggle: () => void
}) {
  // Persists a shared per-placement label flag (migration 0054) -- a write, so it
  // is hidden in view mode (S3).
  const editMode = useEditModeStore((s) => s.editMode)
  if (!editMode) return null
  return (
    <g
      transform={`translate(${x}, ${y})`}
      className="cursor-pointer opacity-0 transition-opacity group-hover:opacity-100"
      onPointerDown={(e) => e.stopPropagation()}
      onClick={(e) => {
        e.stopPropagation()
        onToggle()
      }}
    >
      <title>{hidden ? "Show label" : "Hide label"}</title>
      <circle r={size} style={{ fill: "var(--background)", stroke: "var(--border)" }} strokeWidth={size * 0.14} />
      <ellipse
        rx={size * 0.6}
        ry={size * 0.38}
        fill="none"
        style={{ stroke: "var(--muted-foreground)" }}
        strokeWidth={size * 0.16}
      />
      <circle r={size * 0.17} style={{ fill: "var(--muted-foreground)" }} />
      {hidden && (
        <line
          x1={-size * 0.72}
          y1={-size * 0.72}
          x2={size * 0.72}
          y2={size * 0.72}
          style={{ stroke: "var(--muted-foreground)" }}
          strokeWidth={size * 0.16}
          strokeLinecap="round"
        />
      )}
    </g>
  )
}

/** A small hover-revealed toolbar shown under a selected placement's label
 * (migration 0056): rotate the label left/right in 15-degree steps and step
 * its font size up/down. Anchored to the glyph (not the label) and rendered in
 * the un-rotated frame so the buttons themselves never spin, and drawn as
 * discrete steppers -- reposition is a direct drag, but rotate/size read
 * cleanly as taps on a schematic. stopPropagation so a tap never starts a
 * placement drag. */
/** Shortest distance from a point to a line segment (diagram-local units). */
function pointSegmentDistance(px: number, py: number, ax: number, ay: number, bx: number, by: number): number {
  const dx = bx - ax
  const dy = by - ay
  if (dx === 0 && dy === 0) return Math.hypot(px - ax, py - ay)
  let t = ((px - ax) * dx + (py - ay) * dy) / (dx * dx + dy * dy)
  t = Math.max(0, Math.min(1, t))
  return Math.hypot(px - (ax + t * dx), py - (ay + t * dy))
}

/** Shortest distance from a point to a polyline (its nearest segment). */
function distanceToPolyline(px: number, py: number, pts: [number, number][]): number {
  if (pts.length === 0) return Infinity
  if (pts.length === 1) return Math.hypot(px - pts[0][0], py - pts[0][1])
  let min = Infinity
  for (let i = 0; i < pts.length - 1; i++) {
    const d = pointSegmentDistance(px, py, pts[i][0], pts[i][1], pts[i + 1][0], pts[i + 1][1])
    if (d < min) min = d
  }
  return min
}

/**
 * The selected placement's label controls -- nudge (←↑↓→), rotate (↺↻) and
 * resize (−/+) its name label. Rendered in the diagram HEADER, not floating over
 * the feature, so it never blocks the view (user feedback 2026-10-10). Mounts
 * only when a placement with a visible label is selected; operates on whichever
 * placement that is.
 */
function LabelAdjustBar({
  step,
  onNudge,
  onRotate,
  onScale,
}: {
  /** One nudge-button press in glyph-local units, sized to the label's font. */
  step: number
  onNudge: (dx: number, dy: number) => void
  onRotate: (deltaDeg: number) => void
  onScale: (factor: number) => void
}) {
  const btn = (glyph: string, title: string, onClick: () => void) => (
    <button
      type="button"
      title={title}
      onClick={onClick}
      className="hover:bg-accent hover:text-accent-foreground text-muted-foreground flex size-6 items-center justify-center rounded text-sm leading-none"
    >
      {glyph}
    </button>
  )
  return (
    <div className="bg-popover text-popover-foreground flex items-center gap-0.5 rounded-md border px-1 py-0.5 shadow-sm">
      <span className="text-muted-foreground px-1 text-[10px] font-medium">Label</span>
      {btn("←", "Nudge label left", () => onNudge(-step, 0))}
      {btn("↑", "Nudge label up", () => onNudge(0, -step))}
      {btn("↓", "Nudge label down", () => onNudge(0, step))}
      {btn("→", "Nudge label right", () => onNudge(step, 0))}
      {btn("↺", "Rotate label left 15°", () => onRotate(-15))}
      {btn("↻", "Rotate label right 15°", () => onRotate(15))}
      {btn("−", "Smaller label", () => onScale(1 / 1.15))}
      {btn("+", "Larger label", () => onScale(1.15))}
    </div>
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
  gridEnabled,
  onToggleGrid,
  orthoEnabled,
  onToggleOrtho,
}: {
  mode: "select" | "pan"
  onModeChange: (mode: "select" | "pan") => void
  onZoomIn: () => void
  onZoomOut: () => void
  onFit: () => void
  snapEnabled: boolean
  onToggleSnap: () => void
  gridEnabled: boolean
  onToggleGrid: () => void
  orthoEnabled: boolean
  onToggleOrtho: () => void
}) {
  // Starts collapsed on every diagram open (the parent keys this component by
  // diagram id, so a fresh diagram remounts back to collapsed); the user
  // uncollapses it when they want the view controls.
  const [collapsed, setCollapsed] = useState(true)
  const activeClass = "bg-accent text-accent-foreground"

  // Collapsed: a single button parked in the corner, so the toolbar stops
  // covering the drawing but is one click away.
  if (collapsed) {
    return (
      <div className="bg-popover text-popover-foreground absolute top-2 left-2 z-10 flex items-center rounded-md border p-0.5 shadow-sm">
        <Button variant="ghost" size="icon" className="size-7" title="Show toolbar" onClick={() => setCollapsed(false)}>
          <ChevronsRight className="size-4" />
          <span className="sr-only">Show toolbar</span>
        </Button>
      </div>
    )
  }

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
      <Button
        variant="ghost"
        size="icon"
        className={cn("size-7", gridEnabled && activeClass)}
        title={gridEnabled ? "Grid on — vertices snap to the grid" : "Grid off"}
        onClick={onToggleGrid}
      >
        <Grid3x3 className="size-4" />
        <span className="sr-only">{gridEnabled ? "Grid on" : "Grid off"}</span>
      </Button>
      <Button
        variant="ghost"
        size="icon"
        className={cn("size-7", orthoEnabled && activeClass)}
        title={orthoEnabled ? "Ortho on — line segments lock to right angles" : "Ortho off"}
        onClick={onToggleOrtho}
      >
        <CornerDownRight className="size-4" />
        <span className="sr-only">{orthoEnabled ? "Ortho on" : "Ortho off"}</span>
      </Button>
      <div className="bg-border mx-0.5 h-5 w-px" />
      <Button variant="ghost" size="icon" className="size-7" title="Hide toolbar" onClick={() => setCollapsed(true)}>
        <ChevronsLeft className="size-4" />
        <span className="sr-only">Hide toolbar</span>
      </Button>
    </div>
  )
}

/** Imperative handle for the per-canvas status bar. The canvas pushes the
 * cursor's diagram-space position through `set` on every pointer move (and
 * `set(null)` when the pointer leaves). */
export interface DiagramStatusBarHandle {
  set: (pos: { x: number; y: number } | null) => void
}

/** The diagram canvas's own status bar -- a live readout of the cursor's X/Y
 * in the diagram's local coordinate space (the same x/y a placement stores),
 * pinned to the bottom of the canvas the way the map has its own status bar.
 *
 * Each DiagramCanvas renders exactly one of these, so when several diagrams are
 * open side by side each shows the coordinates for its own cursor -- the spaces
 * are independent (every canvas has its own camera/viewBox), so a single shared
 * readout couldn't serve them all. That is the answer to "do we get multiple
 * status bars?": yes, one per open diagram.
 *
 * Updated imperatively (forwardRef `set`) from the canvas's pointer-move
 * handler rather than through the canvas's own React state: the canvas is a
 * large SVG and re-rendering it on every mouse move just to repaint one label
 * would be wasteful, so only this small component re-renders. This mirrors, in
 * spirit, the map's store-backed CoordinateReadout (isolate the high-frequency
 * update) -- scoped per canvas instead of through a global store. */
const DiagramStatusBar = forwardRef<DiagramStatusBarHandle>(function DiagramStatusBar(_props, ref) {
  const [pos, setPos] = useState<{ x: number; y: number } | null>(null)
  useImperativeHandle(ref, () => ({ set: setPos }), [])
  return (
    <div className="bg-background text-muted-foreground flex h-7 shrink-0 items-center gap-4 border-t px-3 text-xs">
      <span className="tabular-nums">
        X: {pos ? Math.round(pos.x) : "—"} Y: {pos ? Math.round(pos.y) : "—"}
      </span>
    </div>
  )
})

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
/**
 * The diagram header's single "Add" menu — the three ways to put a feature on a
 * diagram, folded into one dropdown so the header stops crowding (was three
 * separate icon buttons: from-map, search-by-name, digitize).
 *
 *  - **From map** — arm the map→diagram pick/place flow (draw-store).
 *  - **Search by name** — find an *existing* feature and drop a second
 *    placement of it (the old AddFeatureButton search, inlined here).
 *  - **New off-map feature** — digitize a brand-new geometry_type = 'none'
 *    feature straight onto the diagram (the old DigitizeFeatureButton class
 *    list, inlined here; only shown when such classes exist).
 *
 * While a digitize is armed the trigger is replaced by the same "Click the
 * canvas to place X · Esc" pill the standalone button used, so that feedback
 * survives the consolidation.
 */
function AddMenu({
  diagramId,
  container,
  diagramType,
  placements,
  pickActive,
  onToggleFromMap,
  digitizeClasses,
  digitizingClass,
  onStartDigitize,
  onCancelDigitize,
}: {
  diagramId: string
  container: DiagramFeatureRef | undefined
  diagramType: string | undefined
  placements: DiagramPlacement[]
  pickActive: boolean
  onToggleFromMap: () => void
  digitizeClasses: FeatureClassSummary[]
  digitizingClass: string | null
  onStartDigitize: (code: string) => void
  onCancelDigitize: () => void
}) {
  const [open, setOpen] = useState(false)
  const [query, setQuery] = useState("")
  const [placing, setPlacing] = useState(false)
  const { results, loading } = useFeatureNameSearch(query)
  const containerRef = useRef<HTMLDivElement>(null)
  const queryClient = useQueryClient()
  // The panels addable to this diagram (Phase 42 Slice 3) -- non-empty only on a
  // real board schematic (the backend join returns nothing otherwise), so the
  // "Add panel" section below simply doesn't render on an ordinary one-line.
  const { data: panelOptionsData } = usePanelOptions(container?.feature_class, diagramType)
  const panelOptions = panelOptionsData?.options ?? []

  // Start an add-a-panel draft: the shared create form (feature-panel.tsx) opens
  // with panel_role pre-selected so the user fills the panel's remaining required
  // fields, then Save fans out the whole device set and places it on the board
  // mimic. The column anchor is the next free slot, spread left-to-right like any
  // other new placement; devices land relative to it via their template offsets.
  function handleAddPanel(option: PanelOption) {
    if (!container) return
    const base = nextPlacementPosition(placements)
    setOpen(false)
    useDrawStore.getState().startPanelDraft(
      option.panel_feature_class,
      {
        boardFeatureClass: container.feature_class,
        boardFeatureId: container.feature_id,
        diagramId,
        base,
      },
      option.role,
    )
  }

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

  // A digitize is armed: swap the trigger for the "click the canvas" pill so the
  // user still sees where to drop the new feature (mirrors the old button).
  if (digitizingClass) {
    const className = digitizeClasses.find((c) => c.code === digitizingClass)?.name ?? digitizingClass
    return (
      <Button variant="secondary" size="sm" className="h-7 shrink-0 gap-1.5 px-2 text-xs" onClick={onCancelDigitize}>
        Click the canvas to place {className}
        <span className="text-muted-foreground">Esc</span>
      </Button>
    )
  }

  return (
    <div ref={containerRef} className="relative shrink-0">
      <Button
        variant="ghost"
        size="icon"
        className={cn("size-7", (pickActive || open) && "bg-accent text-accent-foreground")}
        title="Add a feature to this diagram"
        onClick={() => setOpen((o) => !o)}
      >
        <Plus className="size-4" />
        <span className="sr-only">Add a feature</span>
      </Button>
      {open && (
        <div className="bg-popover text-popover-foreground absolute top-full right-0 z-10 mt-1 w-72 rounded-md border py-1 shadow-lg">
          <button
            type="button"
            onClick={() => {
              setOpen(false)
              onToggleFromMap()
            }}
            className={cn(
              "hover:bg-accent hover:text-accent-foreground flex w-full items-center gap-2 px-3 py-1.5 text-left text-sm",
              pickActive && "bg-accent text-accent-foreground",
            )}
          >
            <Crosshair className="size-4" /> From map
          </button>
          <div className="bg-border my-1 h-px" />
          <div className="px-2">
            <p className="text-muted-foreground px-1 pb-1 text-xs font-medium">Search by name</p>
            <input
              type="text"
              className="border-input bg-background h-8 w-full rounded-md border px-2 text-sm outline-none focus-visible:border-ring focus-visible:ring-ring/50 focus-visible:ring-[3px]"
              placeholder="Search by name…"
              value={query}
              onChange={(e) => setQuery(e.target.value)}
            />
          </div>
          <div className="mt-1 max-h-56 overflow-y-auto">
            {query.trim().length < MIN_QUERY_LENGTH ? (
              <p className="text-muted-foreground px-3 py-2 text-sm">Type a feature's name to add it here.</p>
            ) : loading ? (
              <div className="text-muted-foreground flex items-center gap-2 px-3 py-2 text-sm">
                <Loader2 className="size-4 animate-spin" />
                Searching…
              </div>
            ) : addableResults.length === 0 ? (
              <p className="text-muted-foreground px-3 py-2 text-sm">No matching features not already on this diagram.</p>
            ) : (
              addableResults.map((row) => (
                <button
                  key={`${row.featureClassCode}:${row.featureId}`}
                  type="button"
                  disabled={placing}
                  onClick={() => handleAdd(row)}
                  className="hover:bg-accent hover:text-accent-foreground block w-full px-3 py-2 text-left text-sm transition-colors disabled:opacity-50"
                >
                  <p className="truncate font-medium">{row.name}</p>
                  <p className="text-muted-foreground truncate text-xs">{row.className}</p>
                </button>
              ))
            )}
          </div>
          {digitizeClasses.length > 0 && (
            <>
              <div className="bg-border my-1 h-px" />
              <p className="text-muted-foreground px-3 pb-1 text-xs font-medium">New off-map feature</p>
              {/* Bounded + scrollable: the catalog of digitizable classes can be
                  long enough to run the popover off the bottom of the screen, so
                  cap it the same way the search-results block above does. */}
              <div className="max-h-56 overflow-y-auto">
                {digitizeClasses.map((c) => (
                  <button
                    key={c.code}
                    type="button"
                    onClick={() => {
                      setOpen(false)
                      onStartDigitize(c.code)
                    }}
                    className="hover:bg-accent hover:text-accent-foreground flex w-full items-center gap-2 px-3 py-1.5 text-left text-sm"
                  >
                    <PenLine className="size-4" /> {c.name}
                  </button>
                ))}
              </div>
            </>
          )}
          {panelOptions.length > 0 && (
            <>
              <div className="bg-border my-1 h-px" />
              <p className="text-muted-foreground px-3 pb-1 text-xs font-medium">Add switchgear panel</p>
              {panelOptions.map((option) => (
                <button
                  key={`${option.panel_feature_class}:${option.role}`}
                  type="button"
                  onClick={() => handleAddPanel(option)}
                  className="hover:bg-accent hover:text-accent-foreground flex w-full items-center gap-2 px-3 py-1.5 text-left text-sm"
                >
                  <Grid3x3 className="size-4 shrink-0" />
                  <span className="truncate">{option.role_label}</span>
                </button>
              ))}
            </>
          )}
        </div>
      )}
    </div>
  )
}

/**
 * The diagram header's "Connect" menu — folds the two authoring actions
 * (establish a connection, or a relationship, between two placements) into one
 * dropdown. Each just arms the corresponding mode; the trigger highlights while
 * either is armed so the header still shows the active state at a glance.
 */
function ConnectMenu({
  connectActive,
  relateActive,
  onConnect,
  onRelate,
}: {
  connectActive: boolean
  relateActive: boolean
  onConnect: () => void
  onRelate: () => void
}) {
  const [open, setOpen] = useState(false)
  const containerRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (!open) return
    function handlePointerDown(e: PointerEvent) {
      if (containerRef.current && !containerRef.current.contains(e.target as Node)) setOpen(false)
    }
    window.addEventListener("pointerdown", handlePointerDown)
    return () => window.removeEventListener("pointerdown", handlePointerDown)
  }, [open])

  function choose(action: () => void) {
    setOpen(false)
    action()
  }

  return (
    <div ref={containerRef} className="relative shrink-0">
      <Button
        variant="ghost"
        size="icon"
        className={cn("size-7", (connectActive || relateActive || open) && "bg-accent text-accent-foreground")}
        title="Connect or relate two placements"
        onClick={() => setOpen((o) => !o)}
      >
        <Cable className="size-4" />
        <span className="sr-only">Connect or relate placements</span>
      </Button>
      {open && (
        <div className="bg-popover text-popover-foreground absolute top-full right-0 z-10 mt-1 w-56 rounded-md border py-1 shadow-lg">
          <button
            type="button"
            onClick={() => choose(onConnect)}
            className={cn(
              "hover:bg-accent hover:text-accent-foreground flex w-full items-center gap-2 px-3 py-1.5 text-left text-sm",
              connectActive && "bg-accent text-accent-foreground",
            )}
          >
            <Cable className="size-4" /> Connect placements
          </button>
          <button
            type="button"
            onClick={() => choose(onRelate)}
            className={cn(
              "hover:bg-accent hover:text-accent-foreground flex w-full items-center gap-2 px-3 py-1.5 text-left text-sm",
              relateActive && "bg-accent text-accent-foreground",
            )}
          >
            <Link2 className="size-4" /> Relate placements
          </button>
        </div>
      )}
    </div>
  )
}

/**
 * The diagram template library (Phase 24 Slice 4), as a header popover.
 * Two things in one place: **save** the current diagram as a reusable
 * skeleton (its placements become empty class-tagged slots, its furniture is
 * copied), and **apply** a saved template onto this diagram (dropping those
 * slots + furniture for the user to bind). Applying only adds elements, so it
 * invalidates the elements query; saving/deleting changes the library, so
 * those invalidate the templates query.
 */
function TemplatesMenu({ diagramId }: { diagramId: string }) {
  const [open, setOpen] = useState(false)
  const [name, setName] = useState("")
  const [saving, setSaving] = useState(false)
  const containerRef = useRef<HTMLDivElement>(null)
  const templates = useDiagramTemplates()
  const queryClient = useQueryClient()

  useEffect(() => {
    if (!open) return
    function handlePointerDown(e: PointerEvent) {
      if (containerRef.current && !containerRef.current.contains(e.target as Node)) setOpen(false)
    }
    window.addEventListener("pointerdown", handlePointerDown)
    return () => window.removeEventListener("pointerdown", handlePointerDown)
  }, [open])

  async function save() {
    const trimmed = name.trim()
    if (trimmed.length === 0 || saving) return
    setSaving(true)
    try {
      await apiFetch(`/diagrams/${diagramId}/save-as-template`, {
        method: "POST",
        body: JSON.stringify({ name: trimmed }),
      })
      queryClient.invalidateQueries({ queryKey: ["diagram-templates"] })
      setName("")
      useToastStore.getState().pushSuccess(`Saved “${trimmed}” to the template library.`)
    } catch (err) {
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not save this template.")
    } finally {
      setSaving(false)
    }
  }

  async function remove(id: string) {
    try {
      await apiFetch(`/diagrams/templates/${id}`, { method: "DELETE" })
      queryClient.invalidateQueries({ queryKey: ["diagram-templates"] })
    } catch (err) {
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not delete this template.")
    }
  }

  return (
    <div ref={containerRef} className="relative shrink-0">
      <Button
        variant="ghost"
        size="icon"
        className={cn("size-7", open && "bg-accent text-accent-foreground")}
        title="Templates — save this diagram, or manage the library (apply a template when creating a new diagram)"
        onClick={() => setOpen((o) => !o)}
      >
        <LayoutTemplate className="size-4" />
        <span className="sr-only">Templates</span>
      </Button>
      {open && (
        <div className="bg-popover text-popover-foreground absolute top-full right-0 z-10 mt-1 w-72 rounded-md border shadow-lg">
          <div className="border-b p-2">
            <p className="text-muted-foreground px-1 pb-1 text-xs font-medium">Save this diagram</p>
            <div className="flex items-center gap-1">
              <input
                type="text"
                className="border-input bg-background h-8 w-full rounded-md border px-2 text-sm outline-none focus-visible:border-ring focus-visible:ring-ring/50 focus-visible:ring-[3px]"
                placeholder="Template name…"
                value={name}
                onChange={(e) => setName(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === "Enter") void save()
                }}
              />
              <Button size="sm" className="h-8 shrink-0" disabled={name.trim().length === 0 || saving} onClick={() => void save()}>
                {saving ? <Loader2 className="size-4 animate-spin" /> : "Save"}
              </Button>
            </div>
          </div>
          <div className="p-1">
            <p className="text-muted-foreground px-2 py-1 text-xs font-medium">Saved templates</p>
            <p className="text-muted-foreground px-2 pb-1 text-xs">Apply one when creating a new diagram; here you can only prune the library.</p>
            {templates.length === 0 ? (
              <p className="text-muted-foreground px-2 py-2 text-sm">No saved templates yet.</p>
            ) : (
              <div className="max-h-64 overflow-y-auto">
                {templates.map((t) => (
                  <div key={t.id} className="group hover:bg-accent flex items-center gap-1 rounded-md px-1 transition-colors">
                    <div className="min-w-0 flex-1 py-1.5 pl-1 text-sm">
                      <p className="truncate font-medium">{t.name}</p>
                      <p className="text-muted-foreground truncate text-xs">
                        {t.slot_count} {t.slot_count === 1 ? "slot" : "slots"} · {t.element_count}{" "}
                        {t.element_count === 1 ? "element" : "elements"}
                      </p>
                    </div>
                    <button
                      type="button"
                      title="Delete template"
                      onClick={() => void remove(t.id)}
                      className="text-muted-foreground hover:text-destructive shrink-0 rounded p-1 opacity-0 transition-opacity group-hover:opacity-100"
                    >
                      <Trash2 className="size-3.5" />
                      <span className="sr-only">Delete template</span>
                    </button>
                  </div>
                ))}
              </div>
            )}
          </div>
        </div>
      )}
    </div>
  )
}

/**
 * The feature picker for binding a template slot (Phase 24 Slice 4). Reuses the
 * exact name-search fan-out AddFeatureButton uses, filtered to the slot's own
 * `slot_class` -- a "put a TRANSFORMER here" slot only offers transformers. An
 * untyped slot (no slot_class, e.g. a built-in template's generic bay) skips the
 * filter and offers any feature, binding to whatever class the pick belongs to.
 * Rendered as an overlay banner over the canvas (like the "add from map" hint),
 * it hands the chosen feature id + class back to `onBind`, which materialises
 * the placement and consumes the slot.
 */
function SlotBindPanel({
  slot,
  onBind,
  onCreateNew,
  canCreate,
  onCancel,
}: {
  slot: DiagramElement
  onBind: (featureId: string, featureClass: string) => void
  // Promote this slot into a brand-new feature (admin-only). Undefined/!canCreate
  // hides the affordance; an untyped slot (no slot_class) also hides it -- there's
  // no class to create.
  onCreateNew: () => void
  canCreate: boolean
  onCancel: () => void
}) {
  const [query, setQuery] = useState("")
  const { results, loading } = useFeatureNameSearch(query)
  const slotClass = slot.slot_class ?? ""
  // An untyped slot (no slot_class -- e.g. a built-in template's generic bay)
  // accepts any feature; a class-tagged slot stays scoped to its own class.
  const matches = slotClass ? results.filter((r) => r.featureClassCode === slotClass) : results

  return (
    <div className="bg-popover text-popover-foreground absolute top-2 left-1/2 z-20 w-72 -translate-x-1/2 rounded-md border shadow-lg">
      <div className="flex items-center justify-between border-b px-3 py-2">
        <p className="truncate text-sm font-medium">Bind {slotClass || "slot"}</p>
        <button type="button" onClick={onCancel} className="text-muted-foreground hover:text-foreground shrink-0">
          <X className="size-4" />
          <span className="sr-only">Cancel</span>
        </button>
      </div>
      <div className="p-2">
        <input
          autoFocus
          type="text"
          className="border-input bg-background h-8 w-full rounded-md border px-2 text-sm outline-none focus-visible:border-ring focus-visible:ring-ring/50 focus-visible:ring-[3px]"
          placeholder={`Search ${slotClass || "features"} by name…`}
          value={query}
          onChange={(e) => setQuery(e.target.value)}
        />
      </div>
      <div className="max-h-64 overflow-y-auto border-t">
        {query.trim().length < MIN_QUERY_LENGTH ? (
          <p className="text-muted-foreground p-3 text-sm">Type a name to bind this slot to a real {slotClass || "feature"}.</p>
        ) : loading ? (
          <div className="text-muted-foreground flex items-center gap-2 p-3 text-sm">
            <Loader2 className="size-4 animate-spin" />
            Searching…
          </div>
        ) : matches.length === 0 ? (
          <p className="text-muted-foreground p-3 text-sm">No matching {slotClass || "features"} found.</p>
        ) : (
          matches.map((row) => (
            <button
              key={row.featureId}
              type="button"
              onClick={() => onBind(row.featureId, row.featureClassCode)}
              className="hover:bg-accent hover:text-accent-foreground block w-full border-b p-2 text-left text-sm transition-colors last:border-b-0"
            >
              <p className="truncate font-medium">{row.name}</p>
              <p className="text-muted-foreground truncate text-xs">{row.className}</p>
            </button>
          ))
        )}
      </div>
      {canCreate && slotClass && (
        // Admin-only (metadata:write): promote this slot into a brand-new,
        // permanent feature instead of binding an existing one. Opens the shared
        // create form targeted at the slot's class; Save runs the atomic promote.
        <div className="border-t p-2">
          <button
            type="button"
            onClick={onCreateNew}
            className="hover:bg-accent hover:text-accent-foreground flex w-full items-center justify-center gap-2 rounded-md border border-dashed py-2 text-sm font-medium transition-colors"
          >
            <Plus className="size-4" /> Create new {slotClass}
          </button>
        </div>
      )}
    </div>
  )
}

// PromoteClassPicker is the "Both" fallback for promotion (admin-only): when an
// admin double-clicks a piece of decoration that carries no class of its own (a
// plain drawn line, or a symbol whose stencil has no promote_class), this lets
// them pick which feature class it becomes. A mapped stencil never reaches here
// -- its baked class wins straight away. The offered classes are pre-filtered to
// ones that can actually be born from this element (diagram-creatable, matching
// shape), so any pick is placeable.
function PromoteClassPicker({
  element,
  classes,
  onPick,
  onCancel,
}: {
  element: DiagramElement
  classes: FeatureClassSummary[]
  onPick: (code: string) => void
  onCancel: () => void
}) {
  const [query, setQuery] = useState("")
  const isLine = !!(element.path && element.path.length >= 2)
  const q = query.trim().toLowerCase()
  const matches = q
    ? classes.filter((c) => c.name.toLowerCase().includes(q) || c.code.toLowerCase().includes(q))
    : classes

  return (
    <div className="bg-popover text-popover-foreground absolute top-2 left-1/2 z-20 w-72 -translate-x-1/2 rounded-md border shadow-lg">
      <div className="flex items-center justify-between border-b px-3 py-2">
        <p className="truncate text-sm font-medium">Promote to…</p>
        <button type="button" onClick={onCancel} className="text-muted-foreground hover:text-foreground shrink-0">
          <X className="size-4" />
          <span className="sr-only">Cancel</span>
        </button>
      </div>
      <div className="p-2">
        <input
          autoFocus
          type="text"
          className="border-input bg-background h-8 w-full rounded-md border px-2 text-sm outline-none focus-visible:border-ring focus-visible:ring-ring/50 focus-visible:ring-[3px]"
          placeholder="Search feature classes…"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
        />
      </div>
      <div className="max-h-64 overflow-y-auto border-t">
        {matches.length === 0 ? (
          <p className="text-muted-foreground p-3 text-sm">
            No {isLine ? "line" : "point"} feature class matches.
          </p>
        ) : (
          matches.map((fc) => (
            <button
              key={fc.code}
              type="button"
              onClick={() => onPick(fc.code)}
              className="hover:bg-accent hover:text-accent-foreground block w-full border-b p-2 text-left text-sm transition-colors last:border-b-0"
            >
              <p className="truncate font-medium">{fc.name}</p>
              <p className="text-muted-foreground truncate text-xs">{fc.code}</p>
            </button>
          ))
        )}
      </div>
      <p className="text-muted-foreground border-t p-2 text-xs">
        Turns this {isLine ? "line" : "marker"} into a real, permanent feature everyone sees.
      </p>
    </div>
  )
}

/**
 * Adds a non-feature annotation to the diagram (Phase 24 keystone): a
 * grouping box, a free text label, or a schematic-only line. Sits beside
 * AddFeatureButton in the header, deliberately a separate control -- one adds
 * a *real feature* to the schematic, the other adds pure diagram furniture
 * backed by nothing. Box/text drop immediately at the view centre; line arms
 * a click-to-draw mode (handled on the canvas), so its button reflects that
 * armed state rather than acting instantly.
 */
function AddAnnotationMenu({
  onAddBox,
  onAddText,
  onAddLine,
  onAddBus,
  onAddTap,
  onAddControl,
  onAddTitleBlock,
  onAddLegend,
  lineArmed,
}: {
  onAddBox: () => void
  onAddText: () => void
  onAddLine: () => void
  onAddBus: () => void
  onAddTap: () => void
  onAddControl: () => void
  onAddTitleBlock: () => void
  onAddLegend: () => void
  lineArmed: boolean
}) {
  const [open, setOpen] = useState(false)
  const containerRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (!open) return
    function handlePointerDown(e: PointerEvent) {
      if (containerRef.current && !containerRef.current.contains(e.target as Node)) setOpen(false)
    }
    window.addEventListener("pointerdown", handlePointerDown)
    return () => window.removeEventListener("pointerdown", handlePointerDown)
  }, [open])

  function choose(action: () => void) {
    setOpen(false)
    action()
  }

  return (
    <div ref={containerRef} className="relative shrink-0">
      <Button
        variant="ghost"
        size="icon"
        className={cn("size-7", lineArmed && "bg-accent text-accent-foreground")}
        title="Add annotation"
        onClick={() => setOpen((o) => !o)}
      >
        <Shapes className="size-4" />
        <span className="sr-only">Add annotation</span>
      </Button>
      {open && (
        <div className="bg-popover text-popover-foreground absolute top-full right-0 z-10 mt-1 w-40 rounded-md border py-1 shadow-lg">
          <button
            type="button"
            onClick={() => choose(onAddBox)}
            className="hover:bg-accent hover:text-accent-foreground flex w-full items-center gap-2 px-3 py-1.5 text-left text-sm"
          >
            <Square className="size-4" /> Box
          </button>
          <button
            type="button"
            onClick={() => choose(onAddText)}
            className="hover:bg-accent hover:text-accent-foreground flex w-full items-center gap-2 px-3 py-1.5 text-left text-sm"
          >
            <Type className="size-4" /> Text
          </button>
          <button
            type="button"
            onClick={() => choose(onAddLine)}
            className="hover:bg-accent hover:text-accent-foreground flex w-full items-center gap-2 px-3 py-1.5 text-left text-sm"
          >
            <PenLine className="size-4" /> Line
          </button>
          <button
            type="button"
            onClick={() => choose(onAddBus)}
            className="hover:bg-accent hover:text-accent-foreground flex w-full items-center gap-2 px-3 py-1.5 text-left text-sm"
          >
            <Minus className="size-4" /> Bus
          </button>
          <button
            type="button"
            onClick={() => choose(onAddTap)}
            className="hover:bg-accent hover:text-accent-foreground flex w-full items-center gap-2 px-3 py-1.5 text-left text-sm"
          >
            <Waypoints className="size-4" /> Tap
          </button>
          <button
            type="button"
            onClick={() => choose(onAddControl)}
            className="hover:bg-accent hover:text-accent-foreground flex w-full items-center gap-2 px-3 py-1.5 text-left text-sm"
          >
            <Spline className="size-4" /> Control line
          </button>
          <div className="bg-border my-1 h-px" />
          <button
            type="button"
            onClick={() => choose(onAddTitleBlock)}
            className="hover:bg-accent hover:text-accent-foreground flex w-full items-center gap-2 px-3 py-1.5 text-left text-sm"
          >
            <Table className="size-4" /> Title block
          </button>
          <button
            type="button"
            onClick={() => choose(onAddLegend)}
            className="hover:bg-accent hover:text-accent-foreground flex w-full items-center gap-2 px-3 py-1.5 text-left text-sm"
          >
            <List className="size-4" /> Legend
          </button>
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
  // The placement's anchor at drag start, so a line placement's whole `path`
  // can be translated by exactly the delta the anchor moved (see basePath/path
  // below) -- the same mechanism ElementDragState uses for a schematic line.
  startX: number
  startY: number
  x: number
  y: number
  // basePath is the line placement's original polyline; path is that polyline
  // translated by the anchor delta, so dragging a drawn busbar/conductor moves
  // the whole bar as one piece. Both undefined for a point (or loose-end) drag.
  basePath?: DiagramPathPoint[]
  path?: DiagramPathPoint[]
  moved: boolean
  // The click's viewport coordinates, carried so a pointerup that turns out to
  // be a plain click (not a drag) can open the stacked-feature picker at the
  // cursor (see selectOrPickTarget).
  clientX: number
  clientY: number
}

/** Live drag of one element (box/text/line). For a line, `basePath` is its
 * original polyline and `path` is that polyline translated by the same delta
 * the anchor x/y moved, so the whole line follows the cursor as one piece. */
interface ElementDragState {
  id: string
  offsetX: number
  offsetY: number
  startX: number
  startY: number
  x: number
  y: number
  basePath?: DiagramPathPoint[]
  path?: DiagramPathPoint[]
  moved: boolean
}

/** Live resize of a box element from its bottom-right corner handle. */
interface ElementResizeState {
  id: string
  width: number
  height: number
}

/** Live rotation of a symbol element from its rotate handle -- the element
 * analogue of RotateState, snapped the same way (its x/y is the glyph's
 * centre, so the handle angle is measured against that). */
interface ElementRotateState {
  id: string
  centerX: number
  centerY: number
  rotation: number
}

interface RotateState {
  placementId: string
  featureClass: string
  featureId: string
  centerX: number
  centerY: number
  rotation: number
}

/** Live drag of a placement's name label (migration 0056). The label group
 * sits inside the glyph's own rotate() (offset follows the glyph), so a pointer
 * delta measured in SVG space is inverse-rotated by the glyph rotation to land
 * in the glyph-local frame the stored label_dx/label_dy live in. A point
 * placement supplies its glyph rotation; a conductor label has none (0). */
interface LabelDragState {
  placementId: string
  featureClass: string
  featureId: string
  glyphRotationDeg: number
  startPointerX: number
  startPointerY: number
  startDX: number
  startDY: number
  dx: number
  dy: number
  moved: boolean
}

/** Live reshape of one vertex of a line placement's drawn `path`. `path` is a
 * working copy with only the vertex at `index` moved to follow the pointer; the
 * whole array is persisted on drop, with x/y re-anchored to path[0] the way a
 * line is created (see finishLineDraft). */
interface VertexDragState {
  placementId: string
  featureClass: string
  featureId: string
  index: number
  path: DiagramPathPoint[]
  moved: boolean
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

/** One derived control/trip signal line (Phase 46 Slice 2): a
 * protection_trip / protection_sensing relationship whose origin and
 * destination features are both placed on this diagram. `from`/`to` are their
 * anchor points (origin -> destination = the signal direction, so the arrowhead
 * lands on `to`); `kind` picks the colour + arrow marker. Backed by real
 * relationship data, so it's display-only -- not draggable or removable here
 * (revoke the relationship in the feature panel to remove it), exactly as a
 * conductor is shaped by its endpoints rather than edited on the canvas. */
interface ControlLineRender {
  id: string
  kind: string
  name: string
  from: { x: number; y: number }
  to: { x: number; y: number }
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
export function DiagramCanvas({ diagramId: openDiagramId, onClose: close }: { diagramId: string; onClose: () => void }) {
  const selected = useSelectionStore((s) => s.selected)
  // A feature "located" (peeked) from the open panel's Terminals/Relationships
  // list -- its counterpart gets an emerald `peek` halo on the map (see
  // selection-store.ts). Track it here so the same halo also lands on the
  // matching placement in this diagram, keeping map and diagram in step.
  const located = useSelectionStore((s) => s.located)
  const maximized = useDiagramStore((s) => s.maximizedId) === openDiagramId
  const connectSource = useConnectStore((s) => s.source)
  const connectTarget = useConnectStore((s) => s.target)
  const connectMode = useConnectStore((s) => s.mode)
  const traceResult = useTraceStore((s) => s.result)
  const isolateResult = useIsolateStore((s) => s.result)
  const loadFlowResult = useLoadFlowStore((s) => s.result)
  const faultCurrentResult = useFaultCurrentStore((s) => s.result)
  // Phase 12 "Add from map": this canvas both arms the pick (the header
  // button) and, since only it knows this diagram's catalog + placements,
  // validates and then draws whatever the map picked. `*Here` scopes
  // every reaction to this diagram id so that with several diagrams open
  // only the armed one responds.
  const placePhase = useDiagramPlaceStore((s) => s.phase)
  const placeDiagramId = useDiagramPlaceStore((s) => s.diagramId)
  const placePicked = useDiagramPlaceStore((s) => s.picked)
  const placeArmedFeature = useDiagramPlaceStore((s) => s.feature)
  const placeArmedGeometry = useDiagramPlaceStore((s) => s.geometry)
  // "Replace from map" (admin): when set, the same map-pick gesture overwrites
  // this already-placed stub with the picked feature instead of adding a new
  // one. The validation effect below routes the pick down the replace path.
  const placeReplaceTarget = useDiagramPlaceStore((s) => s.replaceTarget)
  const { data: placementsData, isPending, isError } = useDiagramPlacements(openDiagramId ?? undefined)
  const { data: elementsData } = useDiagramElements(openDiagramId ?? undefined)
  const { data: featureClassesData } = useFeatureClasses()
  const rules = useAllSymbology()
  const symbologyIcons = useSymbologyIcons()
  const { data: diagramInstance } = useDiagramInstance(openDiagramId)
  // Deleting a whole diagram is gated on diagrams:delete (migration 0078):
  // supervisor/admin/break-glass-admin only, never editor/viewer. Hide the
  // trigger for anyone the server would refuse anyway; the confirm dialog opens
  // on this id (null = closed).
  // Edit/View mode (S3): the diagram canvas is read-only in view mode -- no
  // dragging/resizing/placing/promoting, and the editing controls are hidden.
  // `editMode` is the raw flag (diagram-layout moves ride diagrams:write, not
  // features:write); `editingEnabled` is the features:write-AND-edit-mode gate the
  // feature-write affordances (place-on-map) use. The server still enforces each
  // permission regardless of the UI mode.
  const editMode = useEditModeStore((s) => s.editMode)
  const editingEnabled = useEditing()
  const canDeleteDiagram = usePermission()("diagrams:delete") && editMode
  // Promoting a stencil into a real, permanent feature is admin-only
  // (metadata:write, the model-authoring capability) -- gates the double-click
  // promote and the slot "Create new" affordance. The server enforces the same
  // permission on POST /diagrams/{id}/elements/{elementID}/promote, so this only
  // hides controls the user couldn't use anyway.
  const canPromote = usePermission()("metadata:write") && editMode
  const [deletingDiagram, setDeletingDiagram] = useState(false)
  // Regenerate rebuilds an auto-generated assembly single-line from its source
  // template (only such diagrams report regenerable). It REPLACES the instance,
  // so we open the returned new id and close this one -- rides diagrams:write,
  // which every editor of this canvas already holds.
  const regenerate = useRegenerateDiagram()
  const handleRegenerate = useCallback(async () => {
    try {
      const result = await regenerate.mutateAsync(openDiagramId)
      useDiagramStore.getState().open(result.instance_id)
      if (result.instance_id !== openDiagramId) useDiagramStore.getState().close(openDiagramId)
      const detail = [`${result.placed} placed`]
      if (result.unbound) detail.push(`${result.unbound} slot${result.unbound === 1 ? "" : "s"} left unbound`)
      if (result.skipped) detail.push(`${result.skipped} skipped`)
      const message = `Diagram rebuilt — ${detail.join(", ")}.`
      if (result.unbound || result.skipped) useToastStore.getState().pushWarning(message)
      else useToastStore.getState().pushSuccess(message)
    } catch (err) {
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not regenerate this diagram.")
    }
  }, [regenerate, openDiagramId])
  const diagramHidden = useDiagramLayersStore((s) => s.hidden)
  const stencils = useDiagramStencils()
  const placements = useMemo(() => placementsData?.placements ?? [], [placementsData])
  const elements = useMemo(() => elementsData?.elements ?? [], [elementsData])
  // Energisation (S2): a per-diagram toggle that shades de-energised placements
  // (below an open device). When on, classify this diagram's placements via the
  // whole-network energisation walk; roleFor reuses the "isolation-deenergized"
  // grey role so a dead feature reads the same on the diagram as under isolate.
  const [showEnergisation, setShowEnergisation] = useState(false)
  const energRefs = useMemo(
    () => placements.map((p) => ({ feature_class: p.feature_class, feature_id: p.feature_id })),
    [placements],
  )
  const { data: energizationDead } = useEnergization(
    openDiagramId ?? null,
    energRefs,
    showEnergisation && placements.length > 0,
  )
  const isPickingHere = placePhase === "picking" && placeDiagramId === openDiagramId
  const isPlacingHere = placePhase === "placing" && placeDiagramId === openDiagramId
  // Whether the in-flight pick is a replace (overwrite a stub) rather than an
  // add. Scoped to this diagram like the others.
  const isReplacingHere = isPickingHere && !!placeReplaceTarget
  // The placement backing the current selection, if the selected feature is on
  // this diagram -- the stub a "Replace from map" would overwrite.
  const selectedPlacement = useMemo(
    () =>
      selected
        ? placements.find((p) => p.feature_class === selected.featureClassCode && p.feature_id === selected.featureId)
        : undefined,
    [selected, placements],
  )
  // "Place on map" (the mirror of Add-from-map): a feature selected on this
  // diagram that has NO map geometry can have its footprint DRAWN on the map,
  // giving it a map presence while keeping its diagram placement. Needs features:
  // write; reads the selected feature to tell geometry-less from already-mapped,
  // and its MAP geometry_type for the draw shape (point/line/polygon -- a true
  // geometry 'none' class can never be drawn, so it's excluded).
  const canWriteFeatures = editingEnabled
  const { data: selectedFeature } = useFeature(selected?.featureClassCode, selected?.featureId)
  const selectedMapGeomType = selected
    ? featureClassesData?.feature_classes.find((fc) => fc.code === selected.featureClassCode)?.geometry_type
    : undefined
  const canPlaceOnMap =
    canWriteFeatures &&
    !!selectedPlacement &&
    !!selectedFeature &&
    !selectedFeature.geometry &&
    (selectedMapGeomType === "point" || selectedMapGeomType === "line" || selectedMapGeomType === "polygon") &&
    !isPickingHere &&
    !isPlacingHere
  // A place-from-map line draw. The full `isLineDrawing` (which also covers
  // digitizing a *new* line-shaped feature) is computed further down, once
  // digitizingClass and geometryTypeByClass exist.
  const isPlaceLineDrawing = isPlacingHere && placeArmedGeometry === "line"
  const svgRef = useRef<SVGSVGElement | null>(null)
  // The canvas's status bar is updated imperatively (see DiagramStatusBar) so
  // pointer moves repaint one label, not the whole SVG.
  const statusBarRef = useRef<DiagramStatusBarHandle | null>(null)
  const [dragState, setDragState] = useState<DragState | null>(null)
  const [rotateState, setRotateState] = useState<RotateState | null>(null)
  // Live per-vertex reshape of a drawn line placement (a busbar/conductor):
  // one vertex of its `path` follows the pointer while the rest hold, so a bar
  // can be bent or lengthened, not just moved. Shown as handles only while the
  // line is selected, like the point rotate handle.
  const [vertexDrag, setVertexDrag] = useState<VertexDragState | null>(null)
  // Live drag of a selected placement's name label (migration 0056).
  const [labelDrag, setLabelDrag] = useState<LabelDragState | null>(null)
  // The in-progress polyline while drawing a line placement (Phase 12
  // Slice 5): committed vertices, plus the live cursor point for a
  // rubber-band segment to wherever the next click would land.
  const [lineDraft, setLineDraft] = useState<DiagramPathPoint[] | null>(null)
  const [linePreview, setLinePreview] = useState<{ x: number; y: number } | null>(null)
  // Non-feature element editing (Phase 24 keystone): dragging/resizing an
  // existing box/text/line, editing a text label in place, drawing a new
  // schematic-only line vertex-by-vertex, and the delete-confirm target.
  const [elementDrag, setElementDrag] = useState<ElementDragState | null>(null)
  const [elementResize, setElementResize] = useState<ElementResizeState | null>(null)
  const [elementRotate, setElementRotate] = useState<ElementRotateState | null>(null)
  const [editingText, setEditingText] = useState<{ id: string; value: string } | null>(null)
  // A single title-block field value being edited in place (Slice 3): which
  // element, which row index into style.fields, and the working value.
  const [editingField, setEditingField] = useState<{ id: string; index: number; value: string } | null>(null)
  // The unbound template slot currently being bound (Slice 4): clicking a slot
  // opens the class-scoped feature picker overlay against it.
  const [slotBinding, setSlotBinding] = useState<DiagramElement | null>(null)
  // The element awaiting a promote-target class, when it carries no mapping of
  // its own (a plain decoration double-clicked by an admin). Holding the
  // element + the classes it's eligible to become opens the picker overlay; a
  // pick resumes promotion with that class.
  const [promotePicker, setPromotePicker] = useState<{ element: DiagramElement; classes: FeatureClassSummary[] } | null>(null)
  // A validated "Replace from map" pick waiting on the user's confirmation before
  // it overwrites the stub (the server-irreversible retire-and-rewire happens on
  // confirm, not the moment the map feature is clicked).
  const [replaceConfirm, setReplaceConfirm] = useState<{ stub: DiagramPlacement; picked: FeatureRef } | null>(null)
  const [pendingElementRemoval, setPendingElementRemoval] = useState<PendingElementRemoval | null>(null)
  // Which schematic polyline the user is drawing vertex-by-vertex, if any
  // (Phase 24): a plain schematic "line", or -- Slice 5 -- a heavy "bus" or a
  // right-angle "tap" (both route orthogonally, see orthoSnap), or -- Phase 46
  // Slice 1 -- a dashed, arrowheaded "control"/trip signal line (freehand like a
  // plain line). Null means not drawing; the same draft/preview state below
  // backs all four.
  const [elementDrawKind, setElementDrawKind] = useState<"line" | "bus" | "tap" | "control" | null>(null)
  const drawingElementLine = elementDrawKind !== null
  const [elemLineDraft, setElemLineDraft] = useState<DiagramPathPoint[] | null>(null)
  const [elemLinePreview, setElemLinePreview] = useState<{ x: number; y: number } | null>(null)
  const queryClient = useQueryClient()

  // The canvas's own camera, independent of the map's OL pan/zoom and
  // of the always-auto-fit viewBox this used to compute on every
  // render -- see the "fit-to-bounds happens once per diagram" effect
  // below for why. "select" is the default tool; "pan" hands drag
  // gestures to the camera instead of to a placement (see
  // handlePlacementPointerDown's own mode guard).
  const [canvasMode, setCanvasMode] = useState<"select" | "pan">("select")
  const [snapEnabled, setSnapEnabled] = useState(true)
  // Drawing aids. Grid: a visual guide + snap every placed/drawn vertex to the
  // nearest grid cell. Ortho: lock each drawn line segment to horizontal/
  // vertical off the previous vertex. They compose -- grid snaps the free coord,
  // ortho picks the axis -- for clean right-angle runs on a schematic.
  // Grid is on by default (a one-line reads cleanest on a grid); it's the
  // user's call to turn it off. Ortho stays opt-in so nothing shifts unless
  // asked.
  const [gridEnabled, setGridEnabled] = useState(true)
  const [orthoEnabled, setOrthoEnabled] = useState(false)
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
  // source/target feature, or a feature reached by a trace/isolate/
  // load-flow/fault-current result, reads identically whether it's
  // shown on the map or the diagram. Unlike voltedge-map.tsx's own
  // highlight effects (which fetch each reached feature's geometry
  // individually to draw it even when off-diagram), this only needs to
  // color placements already on the canvas -- a plain membership check
  // against each result's own feature_class/feature_id, no fetch
  // needed. The four result stores are already mutually exclusive
  // (arming one clears the others -- see feature-panel.tsx), so at
  // most one of the blocks below ever has anything to check.
  function roleFor(featureClass: string, featureId: string): HighlightRole | null {
    if (selected?.featureClassCode === featureClass && selected?.featureId === featureId) return "selected"
    if (connectSource?.featureClassCode === featureClass && connectSource?.featureId === featureId) return "connect-source"
    if (connectTarget?.featureClassCode === featureClass && connectTarget?.featureId === featureId) return "connect-target"
    if (located?.featureClassCode === featureClass && located?.featureId === featureId) return "peek"

    const matches = (ref: { feature_class: string; feature_id: string }) =>
      ref.feature_class === featureClass && ref.feature_id === featureId

    if (traceResult) {
      if (traceResult.barriers.some(matches)) return "trace-barrier"
      if ((traceResult.targets ?? []).some(matches)) return "trace-target"
      if (traceResult.features.some(matches)) return "trace"
    }

    if (isolateResult) {
      if (isolateResult.upstream_boundary && matches(isolateResult.upstream_boundary)) return "isolation-boundary"
      if ((isolateResult.possible_backfeed_paths ?? []).some(matches)) return "isolation-backfeed"
      if (isolateResult.de_energized.some(matches)) return "isolation-deenergized"
    }

    if (loadFlowResult) {
      if (loadFlowResult.barriers.some(matches)) return "load-flow-barrier"
      if (loadFlowResult.nodes.some(matches)) return loadFlowResult.overloaded.some(matches) ? "load-flow-overload" : "load-flow"
    }

    if (faultCurrentResult) {
      if (faultCurrentResult.barriers.some(matches)) return "fault-current-barrier"
      if (faultCurrentResult.nodes.some(matches)) return "fault-current"
    }

    // Energisation shading (S2) -- lowest precedence, so an armed trace/isolate/
    // load-flow/fault result still wins the colour when one is active.
    if (energizationDead && energizationDead.has(refKey(featureClass, featureId))) {
      return "isolation-deenergized"
    }

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
  function selectOrPickTarget(
    featureClass: string,
    featureId: string,
    placementId?: string,
    clientX?: number,
    clientY?: number,
  ) {
    if (useDrawStore.getState().draft) return
    const connectState = useConnectStore.getState()
    // Connect/Relate tool armed (Phase 26): this click is the source of a
    // new workflow in the armed mode. Disarm; the next placement click falls
    // into the pickTarget branch below, then ConnectConfirmPanel confirms.
    if (armingMode) {
      connectState.start(armingMode, { featureClassCode: featureClass, featureId })
      setArmingMode(null)
      return
    }
    if (connectState.mode && !connectState.target) {
      connectState.pickTarget({ featureClassCode: featureClass, featureId })
      return
    }
    // Plain selection: if this placement sits in a stack of 2+ overlapping point
    // placements, open the stacked-feature picker (the same PeerPicker the map
    // uses) at the cursor so the user chooses which one -- rather than silently
    // selecting whichever happens to paint on top, leaving an occluded feature
    // unreachable. A lone feature (or a pick with no cursor position) selects
    // outright. Connect/relate above deliberately skip this: they target the
    // feature actually clicked.
    const stack = clientX != null && clientY != null ? stackAt(clientX, clientY) : []
    if (stack.length >= 2) {
      useSelectionStore.getState().showCandidates(
        stack.map((p) => ({ featureClassCode: p.feature_class, featureId: p.feature_id })),
        { x: clientX!, y: clientY! },
      )
      return
    }
    useSelectionStore.getState().select(featureClass, featureId)
  }

  // Everything under the click point: point symbols whose glyph the click landed
  // on/near, PLUS conductors whose drawn wire runs under the click -- so a symbol
  // sitting on a wire, or two overlapping symbols, both surface. Anchored to the
  // actual cursor (not a feature centre), hidden classes excluded. Returns the
  // single clicked feature when nothing else is there. iconSize is a full glyph
  // width (radius = iconSize/2); the hit radii are a little forgiving.
  function stackAt(clientX: number, clientY: number): DiagramPlacement[] {
    const svg = clientToSvgPoint(clientX, clientY)
    if (!svg) return []
    // Point symbols whose glyph the click landed on.
    const pointHit = iconSize * 0.7
    const points: DiagramPlacement[] = []
    for (const p of effectivePlacements) {
      if (hiddenClassSet.has(p.feature_class)) continue
      if (geometryTypeByClass[p.feature_class] === "line") continue
      if (Math.hypot(p.x - svg.x, p.y - svg.y) <= pointHit) points.push(p)
    }
    // If the click landed on a symbol, disambiguate among the SYMBOLS only -- a
    // wire that just terminates at the clicked symbol isn't a stacked feature to
    // offer (you'd click the wire's own span to pick it). This keeps a normal
    // symbol click a plain select, not a picker over the symbol + its own wires.
    if (points.length > 0) return points
    // Bare area: offer whatever conductor(s) run under the click (crossing wires,
    // or a lone wire -- a single hit just selects it).
    const lineHit = iconSize * 0.45
    const lines: DiagramPlacement[] = []
    for (const item of conductorItems) {
      if (hiddenClassSet.has(item.placement.feature_class)) continue
      const pts: [number, number][] =
        item.placement.path && item.placement.path.length >= 2
          ? item.placement.path
          : [
              [item.points[0].x, item.points[0].y],
              [item.points[1].x, item.points[1].y],
            ]
      if (distanceToPolyline(svg.x, svg.y, pts) <= lineHit) lines.push(item.placement)
    }
    return lines
  }

  // Keyed by the class's DIAGRAM shape, not its map shape: a busbar is
  // geometry_type = 'point' but may set diagram_geometry_type = 'line', in
  // which case it draws as a wire/bar on this canvas (migration 0053).
  // Every render decision below (wire vs icon, line handles) reads this
  // map, so the one fallback here covers them all; unset = inherit.
  const geometryTypeByClass = useMemo(() => {
    const map: Record<string, string> = {}
    featureClassesData?.feature_classes.forEach((fc) => {
      map[fc.code] = fc.diagram_geometry_type ?? fc.geometry_type
    })
    return map
  }, [featureClassesData])

  // Human class name by code (legend row labels for real placements).
  const classNameByCode = useMemo(() => {
    const map: Record<string, string> = {}
    featureClassesData?.feature_classes.forEach((fc) => {
      map[fc.code] = fc.name
    })
    return map
  }, [featureClassesData])

  // Stencil label by icon_ref (legend row labels for stamped symbols).
  const stencilLabelByRef = useMemo(() => {
    const map: Record<string, string> = {}
    for (const s of stencils) map[s.icon_ref] = s.label
    return map
  }, [stencils])

  // Promotable stencils: icon_ref -> feature class code (Phase 25). A symbol
  // stamped from a stencil that declares a promote_class can be double-clicked
  // to become a real feature; the rest (ground studs, arrows, device-number
  // circles) are decoration and carry no entry here.
  const promoteClassByRef = useMemo(() => {
    const map: Record<string, string> = {}
    for (const s of stencils) if (s.promote_class) map[s.icon_ref] = s.promote_class
    return map
  }, [stencils])

  // The rows a `legend` element auto-derives from what's actually on the
  // diagram (Slice 3): one per distinct non-line feature class present (icon
  // from its diagram symbology rule, label = class name) followed by one per
  // distinct stamped stencil symbol (icon + catalog label). Keyed off
  // `placements`/`elements` (not their drag-live variants) so a legend never
  // re-derives mid-drag. Curation and line-class rows are deferred.
  const legendEntries = useMemo(() => {
    const out: { key: string; icon: string; label: string }[] = []
    const seen = new Set<string>()
    for (const p of placements) {
      if (geometryTypeByClass[p.feature_class] === "line") continue
      const key = `class:${p.feature_class}`
      if (seen.has(key)) continue
      seen.add(key)
      const classRules = rules[p.feature_class]
      const def =
        resolveSymbology(classRules, undefined, {}, "diagram") ?? resolveSymbology(classRules, undefined, {}, "map")
      out.push({
        key,
        icon: iconDataUri(def?.icon_ref, def?.color, symbologyIcons),
        label: classNameByCode[p.feature_class] ?? p.feature_class,
      })
    }
    for (const el of elements) {
      if (el.kind !== "symbol") continue
      const iconRef = typeof el.style?.iconRef === "string" ? el.style.iconRef : undefined
      if (!iconRef) continue
      const key = `symbol:${iconRef}`
      if (seen.has(key)) continue
      seen.add(key)
      const color = typeof el.style?.color === "string" ? el.style.color : "#334155"
      out.push({ key, icon: iconDataUri(iconRef, color, symbologyIcons), label: stencilLabelByRef[iconRef] ?? iconRef })
    }
    return out
  }, [placements, elements, geometryTypeByClass, rules, symbologyIcons, classNameByCode, stencilLabelByRef])

  // The Layers control's rows: one per distinct feature class actually placed
  // on this diagram, each with a swatch matching how the canvas draws it -- a
  // glyph for a point/none class, a color bar for a line class (a conductor
  // has no icon). Mirrors legendEntries' class pass, but covers line classes
  // too (they're toggleable) and omits stamped stencils (decoration, not a
  // feature class you'd hide). Sorted by display name for a stable list.
  const diagramLayerEntries = useMemo<DiagramLayerEntry[]>(() => {
    const seen = new Set<string>()
    const out: DiagramLayerEntry[] = []
    for (const p of placements) {
      if (seen.has(p.feature_class)) continue
      seen.add(p.feature_class)
      const classRules = rules[p.feature_class]
      const def =
        resolveSymbology(classRules, undefined, {}, "diagram") ?? resolveSymbology(classRules, undefined, {}, "map")
      const isLine = geometryTypeByClass[p.feature_class] === "line"
      out.push({
        code: p.feature_class,
        name: classNameByCode[p.feature_class] ?? p.feature_class,
        icon: isLine ? undefined : iconDataUri(def?.icon_ref, def?.color, symbologyIcons),
        color: isLine ? def?.color : undefined,
      })
    }
    out.sort((a, b) => a.name.localeCompare(b.name))
    return out
  }, [placements, geometryTypeByClass, rules, symbologyIcons, classNameByCode])

  // Feature classes hidden on THIS diagram (per-diagram key), applied to both
  // point and conductor rendering so a toggled-off class disappears whole.
  const hiddenClassSet = useMemo(() => {
    const set = new Set<string>()
    if (!openDiagramId) return set
    for (const p of placements) {
      if (diagramHidden[diagramLayerKey(openDiagramId, p.feature_class)]) set.add(p.feature_class)
    }
    return set
  }, [placements, diagramHidden, openDiagramId])

  // The one hard rule the digitizing tool exists to respect
  // (docs/phase-5-diagrams.md): a class can be created from inside a
  // diagram only when it needn't be given map geometry here. That's true
  // for geometry_type = 'none' classes (no geom at all) and, since Phase
  // 26, for geometry_optional classes -- a feature of one can be born
  // diagram-only, its nullable geom left NULL (docs/phase-26-optional-
  // geometry.md). A plain spatial class still can't: there's no way to
  // give it real map geometry from this canvas.
  const geometryLessClasses = useMemo(
    () =>
      featureClassesData?.feature_classes.filter(
        (fc) => fc.geometry_type === "none" || fc.geometry_optional,
      ) ?? [],
    [featureClassesData],
  )
  const [digitizingClass, setDigitizingClass] = useState<string | null>(null)
  // Digitizing a NEW feature whose class draws as a line on this diagram
  // (diagram_geometry_type = 'line', e.g. a busbar): it's dug in
  // vertex-by-vertex like a place-from-map line, not dropped at one point
  // (migration 0053). `isLineDrawing` folds both line-draw origins together
  // so every downstream site (rubber-band preview, double-click finish,
  // sketch cleanup) covers the digitize case too.
  const digitizeIsLine = !!digitizingClass && geometryTypeByClass[digitizingClass] === "line"
  const isLineDrawing = isPlaceLineDrawing || digitizeIsLine
  // Any mode where a click on the CANVAS is meant to place/draw geometry, not
  // to interact with an existing feature. While one of these is active the
  // feature layer is made pointer-transparent (see the render), so a closing
  // line-draw click that lands over a peer feature falls through to
  // handleCanvasClick and drops its final vertex, instead of being swallowed
  // by that peer's hit-target and selecting it. Deliberately EXCLUDES connect/
  // relate pick-target mode, where clicking a peer feature is the whole point.
  const drawingActive =
    isLineDrawing ||
    !!digitizingClass ||
    drawingElementLine ||
    (isPlacingHere && !!placeArmedFeature)
  // Phase 26 — diagram-native connection/relationship authoring. When the
  // Connect (Slice 2) or Relate tool is armed, the next placement clicked
  // becomes the workflow's SOURCE (connect-store.start with the armed
  // mode); the click after that lands in selectOrPickTarget's existing
  // pickTarget branch, so the whole "author between these two placements"
  // gesture happens on the diagram without detouring through the map or the
  // feature panel. The confirm (terminal pick + POST /connectivity, or the
  // relationship kind + POST /relationships) is the shared
  // ConnectConfirmPanel. A single armingMode holds which tool is live so the
  // two are mutually exclusive by construction. Connectivity is topological,
  // so a diagram-only (geometry_optional) device becomes a real trace node
  // the same way any feature does. See docs/phase-26-optional-geometry.md.
  const [armingMode, setArmingMode] = useState<ConnectMode | null>(null)

  // Arms (or toggles off) one authoring tool. Toggling: clicking the tool
  // that's already armed -- or whose workflow is mid-flight (connectMode) --
  // cancels back to idle. Otherwise arm it, first clearing every other
  // single-shot diagram mode and any in-flight workflow in the other mode,
  // so only ever one is live.
  function armAuthoring(mode: ConnectMode) {
    const store = useConnectStore.getState()
    if (armingMode === mode || connectMode === mode) {
      setArmingMode(null)
      store.cancel()
      return
    }
    useDiagramPlaceStore.getState().cancel()
    setDigitizingClass(null)
    store.cancel()
    setArmingMode(mode)
  }

  // Which authoring workflow (if any) the canvas is currently guiding, and
  // whether it's still waiting on the SOURCE click (armed, no source yet)
  // vs the TARGET click (source picked, no target). Drives the guide banner
  // and the crosshair cursor for both Connect and Relate.
  const authoringMode: ConnectMode | null =
    armingMode ?? (connectSource && !connectTarget ? connectMode : null)
  const authoringAwaitingSource = armingMode !== null

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

  // Escape while a Connect/Relate tool is armed for its source click (Phase
  // 26) disarms it. Once a source is actually picked the workflow is the
  // shared connect-store one, whose Escape is already handled globally in
  // voltedge-map.tsx (it cancels connect-store) -- same split as the
  // digitize Escape above.
  useEffect(() => {
    if (!armingMode) return
    function handleKeyDown(e: KeyboardEvent) {
      if (e.key === "Escape") setArmingMode(null)
    }
    window.addEventListener("keydown", handleKeyDown)
    return () => window.removeEventListener("keydown", handleKeyDown)
  }, [armingMode])

  // Escape cancels an in-progress element-line draw, dropping the sketch --
  // the element analogue of the placement-line draft's own Escape (which
  // voltedge-map.tsx owns via the place store). Text-edit Escape is handled
  // locally on the input itself.
  useEffect(() => {
    if (!drawingElementLine) return
    function handleKeyDown(e: KeyboardEvent) {
      if (e.key === "Escape") {
        setElementDrawKind(null)
        setElemLineDraft(null)
        setElemLinePreview(null)
      }
    }
    window.addEventListener("keydown", handleKeyDown)
    return () => window.removeEventListener("keydown", handleKeyDown)
  }, [drawingElementLine])

  // Escape closes the slot-binding picker (Slice 4) without binding, the same
  // way it backs out of the other in-progress diagram modes above.
  useEffect(() => {
    if (!slotBinding) return
    function handleKeyDown(e: KeyboardEvent) {
      if (e.key === "Escape") setSlotBinding(null)
    }
    window.addEventListener("keydown", handleKeyDown)
    return () => window.removeEventListener("keydown", handleKeyDown)
  }, [slotBinding])

  // Escape closes the promote-class picker without promoting, same as above.
  useEffect(() => {
    if (!promotePicker) return
    function handleKeyDown(e: KeyboardEvent) {
      if (e.key === "Escape") setPromotePicker(null)
    }
    window.addEventListener("keydown", handleKeyDown)
    return () => window.removeEventListener("keydown", handleKeyDown)
  }, [promotePicker])

  // Validates a feature the map just picked for this diagram (Phase 12),
  // the moment diagram-place-store hands one over: only this canvas knows
  // both the catalog (is it electrical / a line vs a point?) and what's
  // already on the diagram, so the gate lives here rather than in the map
  // click handler. A non-placeable class or a feature already on this
  // diagram is refused with a toast and the pick is cancelled; a legal
  // pick is armed for placement, tagged with how its class draws.
  useEffect(() => {
    if (!isPickingHere || !placePicked) return
    // Replace-from-map: the pick overwrites an already-placed stub in place
    // rather than arming a fresh placement. The retire-and-rewire is
    // server-irreversible, so instead of running on the click it validates the
    // pick, exits picking mode, and raises a confirm dialog -- the replace only
    // runs once the user confirms (executeReplace, below).
    if (placeReplaceTarget) {
      const stub = validateReplace(placeReplaceTarget, placePicked)
      useDiagramPlaceStore.getState().cancel()
      if (stub) setReplaceConfirm({ stub, picked: placePicked })
      return
    }
    const fc = featureClassesData?.feature_classes.find((c) => c.code === placePicked.featureClassCode)
    if (!fc) return // catalog still loading -- re-runs once it arrives
    const store = useDiagramPlaceStore.getState()
    if (!fc.is_diagram_placeable) {
      useToastStore.getState().pushError(`${fc.name} can't be placed on a diagram.`)
      store.cancel()
      return
    }
    if (placements.some((p) => p.feature_class === placePicked.featureClassCode && p.feature_id === placePicked.featureId)) {
      useToastStore.getState().pushError("That feature is already on this diagram.")
      store.cancel()
      return
    }
    // Arm line-drawing off the diagram shape, so a busbar
    // (diagram_geometry_type = 'line') lets you draw a spanning bar even
    // though it's a point on the map (migration 0053).
    store.arm(placePicked, (fc.diagram_geometry_type ?? fc.geometry_type) === "line" ? "line" : "point")
  }, [isPickingHere, placePicked, placeReplaceTarget, featureClassesData, placements])

  // Cancel an armed pick that belongs to this diagram if the canvas goes
  // away (closed, or switched out in a tab pane) -- otherwise the map
  // would stay in crosshair "picking" mode pointed at a diagram that's no
  // longer on screen.
  useEffect(() => {
    return () => {
      const store = useDiagramPlaceStore.getState()
      if (store.diagramId === openDiagramId) store.cancel()
    }
  }, [openDiagramId])

  // Drops any in-progress line draft whenever the place gesture ends
  // (finished, Escaped -- voltedge-map.tsx owns the Escape that resets the
  // store -- or switched to a point). Keeps the transient sketch from
  // lingering on the canvas after the store says we're no longer drawing.
  useEffect(() => {
    if (!isLineDrawing) {
      setLineDraft(null)
      setLinePreview(null)
    }
  }, [isLineDrawing])

  // POSTs a map-picked feature as a new placement and clears the armed
  // pick. A point lands exactly where the click did (no `path`); a line
  // sends its drawn polyline as `path`, with x/y anchored at the first
  // vertex for its label/hit target.
  async function placePickedFeature(
    feature: FeatureRef,
    at: { x: number; y: number },
    path?: DiagramPathPoint[],
  ) {
    if (!openDiagramId) return
    useDiagramPlaceStore.getState().cancel()
    try {
      await apiFetch(`/diagrams/${openDiagramId}/placements`, {
        method: "POST",
        body: JSON.stringify({
          feature_class: feature.featureClassCode,
          feature_id: feature.featureId,
          x: at.x,
          y: at.y,
          rotation: 0,
          ...(path ? { path } : {}),
        }),
      })
      queryClient.invalidateQueries({ queryKey: ["diagram-placements", openDiagramId] })
    } catch (err) {
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not place this feature on the diagram.")
    }
  }

  // Validates a "Replace from map" pick client-side (the server enforces the same
  // rules regardless). Returns the stub placement to overwrite, or null (having
  // toasted why) for a hopeless pick -- so an invalid pick fails fast without ever
  // raising the confirm dialog. The actual retire-and-rewire is executeReplace.
  function validateReplace(target: FeatureRef, picked: FeatureRef): DiagramPlacement | null {
    const stub = placements.find(
      (p) => p.feature_class === target.featureClassCode && p.feature_id === target.featureId,
    )
    if (!stub) {
      useToastStore.getState().pushError("The feature to replace is no longer on this diagram.")
      return null
    }
    if (picked.featureClassCode !== target.featureClassCode) {
      useToastStore.getState().pushError("Replace needs the same feature class as the one you're overwriting.")
      return null
    }
    if (picked.featureId === target.featureId) {
      useToastStore.getState().pushError("That's the same feature — pick a different one to replace it with.")
      return null
    }
    if (placements.some((p) => p.feature_class === picked.featureClassCode && p.feature_id === picked.featureId)) {
      useToastStore.getState().pushError("That map feature is already on this diagram.")
      return null
    }
    return stub
  }

  // Overwrites an already-placed stub with the real map feature the user picked,
  // once confirmed. The stub keeps its spot: the server re-points the placement in
  // place, moves the stub's wiring onto the map feature, and retires the stub.
  async function executeReplace(stub: DiagramPlacement, picked: FeatureRef) {
    if (!openDiagramId) return
    try {
      await apiFetch(`/diagrams/${openDiagramId}/placements/${stub.id}/replace-from-map`, {
        method: "POST",
        body: JSON.stringify({ feature_class: picked.featureClassCode, feature_id: picked.featureId }),
      })
      queryClient.invalidateQueries({ queryKey: ["diagram-placements", openDiagramId] })
      // The stub is gone and the map feature now sits in its place -- follow the
      // selection over so its panel/controls point at the surviving feature.
      useSelectionStore.getState().select(picked.featureClassCode, picked.featureId)
      useToastStore.getState().pushSuccess("Replaced with the map feature.")
    } catch (err) {
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not replace this feature from the map.")
    }
  }

  // Completes the line draft: needs at least two vertices, anchors the
  // placement at the first, and POSTs the whole polyline as `path`. The
  // draft is cleared optimistically (placePickedFeature also cancels the
  // store gesture) so the sketch vanishes the instant it's committed.
  function finishLineDraft(points: DiagramPathPoint[]) {
    if (!placeArmedFeature || points.length < 2) return
    const feature = placeArmedFeature
    setLineDraft(null)
    setLinePreview(null)
    void placePickedFeature(feature, { x: points[0][0], y: points[0][1] }, points)
  }

  // Rounds a diagram-space point to the nearest grid intersection. Only
  // called when the grid is on; the step is the same one the visual grid
  // draws with, so a vertex lands exactly on a line the user can see.
  function snapToGrid(pt: { x: number; y: number }): { x: number; y: number } {
    return { x: Math.round(pt.x / gridStep) * gridStep, y: Math.round(pt.y / gridStep) * gridStep }
  }

  // Applies the active drawing aids to a raw click/cursor point while
  // drawing a line. Feature-geometry snapping first (tie onto a nearby
  // existing busbar/feature), else grid (snap the point to a cell), then
  // ortho (lock the segment horizontal/vertical off the previous vertex) --
  // so a locked run stays on grid, since `prev` is itself grid-aligned.
  // `prev` is the last committed vertex (undefined for the line's first point).
  function applyDrawAids(pt: { x: number; y: number }, prev?: DiagramPathPoint): { x: number; y: number } {
    // Feature-geometry snapping wins over grid/ortho: if the cursor is within
    // reach of an existing placement's geometry (any point along a line
    // placement's path -- mid-span, not just its end vertices -- or a point
    // placement's centre), tie the vertex exactly onto it. Only when nothing
    // is in range do the grid/ortho aids apply.
    const peer = snapToPeerGeometry(pt)
    if (peer) return peer
    let p = pt
    if (gridEnabled) p = snapToGrid(p)
    if (orthoEnabled && prev) p = orthoSnap(prev, p)
    return p
  }

  // Nearest point on any existing placement's geometry within the same
  // proportional threshold the alignment magnet uses, or null if nothing's in
  // range. Crucially it snaps against the SAME geometry that's actually drawn
  // (via conductorItems/pointItems), not the raw placement: a line draws from
  // its hand-drawn `path` when it has one, else from the connectivity-derived
  // bar between its endpoints -- so a busbar added from the map (no diagram
  // path) is still a snap target. Lines snap to the closest point anywhere
  // along the bar (mid-span included); point features snap to their centre.
  function snapToPeerGeometry(pt: { x: number; y: number }): { x: number; y: number } | null {
    // Governed by the same Magnet toggle as drag-alignment snapping, so one
    // switch turns all snapping off.
    if (!snapEnabled) return null
    const ext = Math.max(bounds.maxX - bounds.minX, bounds.maxY - bounds.minY, MIN_EXTENT)
    let best: { x: number; y: number } | null = null
    let bestDist = ext * SNAP_THRESHOLD_FRACTION
    const consider = (a: DiagramPathPoint, b: DiagramPathPoint) => {
      const c = closestPointOnSegment(pt, a, b)
      if (c.dist < bestDist) {
        bestDist = c.dist
        best = { x: c.x, y: c.y }
      }
    }
    for (const item of conductorItems) {
      const path =
        item.placement.path && item.placement.path.length >= 2 ? item.placement.path : null
      if (path) {
        for (let i = 0; i < path.length - 1; i++) consider(path[i], path[i + 1])
      } else {
        // Pathless bar: snap to the segment the render draws between its
        // resolved endpoints (busbar added from the map, elbow conductor).
        consider(pointToPair(item.points[0]), pointToPair(item.points[1]))
      }
    }
    for (const item of pointItems) {
      const d = Math.hypot(pt.x - item.placement.x, pt.y - item.placement.y)
      if (d < bestDist) {
        bestDist = d
        best = { x: item.placement.x, y: item.placement.y }
      }
    }
    return best
  }

  // Captures the clicked point and either drops a map-picked feature
  // (Phase 12 placing mode) or hands off to draw-store.ts's
  // startDiagramDraft for the digitize-a-new-feature flow --
  // feature-panel.tsx picks the resulting draft up from there and renders
  // feature-create-form.tsx, the exact same attribute form the map's own
  // draw flow uses (see that file's own doc comment for why this isn't a
  // second, bespoke form). Guarded against the pan tool: a pan drag ends
  // with a plain `click` event too, which would otherwise place a feature
  // at wherever the drag happened to end.
  function handleCanvasClick(e: React.MouseEvent<SVGSVGElement>) {
    if (e.target !== e.currentTarget || canvasMode === "pan") return
    const svgPoint = clientToSvgPoint(e.clientX, e.clientY)
    if (!svgPoint || !openDiagramId) return
    // Drawing a schematic-only line/bus/tap element: each click drops a
    // vertex, double-click (or the banner's Finish) commits it. A bus/tap
    // routes orthogonally, so each new vertex after the first locks to a
    // right angle off the previous one (orthoSnap); a plain line takes the
    // click point as-is.
    if (elementDrawKind) {
      setElemLineDraft((prev) => {
        const pts = prev ?? []
        const last = pts.length > 0 ? pts[pts.length - 1] : undefined
        // Bus/tap always route orthogonally; a plain line honours the Ortho
        // toggle. Grid snapping (when on) applies to all three.
        const forceOrtho = elementDrawKind !== "line" && elementDrawKind !== "control" && last !== undefined
        let p: { x: number; y: number } = svgPoint
        if (gridEnabled) p = snapToGrid(p)
        if ((forceOrtho || orthoEnabled) && last) p = orthoSnap(last, p)
        return [...pts, [p.x, p.y]]
      })
      return
    }
    const placing = isPlacingHere && placeArmedFeature
    if (!digitizingClass && !placing) return
    if (placing) {
      // A line is drawn vertex-by-vertex (finished on double-click or the
      // banner's Finish); a point drops on the first click.
      if (isPlaceLineDrawing) {
        setLineDraft((prev) => [...(prev ?? []), pointToPair(applyDrawAids(svgPoint, (prev ?? [])[(prev ?? []).length - 1]))])
        return
      }
      void placePickedFeature(placing, gridEnabled ? snapToGrid(svgPoint) : svgPoint)
      return
    }
    // Digitizing a new line-shaped feature (busbar): each click drops a
    // vertex, double-click (or the banner's Finish) commits the polyline.
    // A point-shaped class still creates on the first click.
    if (digitizeIsLine) {
      setLineDraft((prev) => [...(prev ?? []), pointToPair(applyDrawAids(svgPoint, (prev ?? [])[(prev ?? []).length - 1]))])
      return
    }
    const dropAt = gridEnabled ? snapToGrid(svgPoint) : svgPoint
    useDrawStore.getState().startDiagramDraft(digitizingClass!, { diagramId: openDiagramId, x: dropAt.x, y: dropAt.y })
    setDigitizingClass(null)
  }

  // Double-click finishes a line draft. Its own two constituent single
  // clicks each already added a vertex, so the last (duplicate) one is
  // dropped before committing.
  function handleCanvasDoubleClick() {
    if (drawingElementLine && elemLineDraft) {
      const points = elemLineDraft.length >= 2 ? elemLineDraft.slice(0, -1) : elemLineDraft
      void finishElementLine(points)
      return
    }
    if (!isLineDrawing || !lineDraft) return
    const points = lineDraft.length >= 2 ? lineDraft.slice(0, -1) : lineDraft
    if (digitizeIsLine) {
      finishDigitizeLine(points)
      return
    }
    finishLineDraft(points)
  }

  // Completes a digitized new line-shaped feature: needs at least two
  // vertices, then hands off to draw-store.ts's diagram draft carrying the
  // drawn polyline as `path` (anchored at the first vertex, like a
  // place-from-map line). feature-create-form.tsx creates the feature and
  // POSTs the placement with that path -- the create-then-place spine, only
  // now with a line instead of a single point (migration 0053).
  function finishDigitizeLine(points: DiagramPathPoint[]) {
    if (!openDiagramId || !digitizingClass || points.length < 2) return
    const cls = digitizingClass
    setLineDraft(null)
    setLinePreview(null)
    setDigitizingClass(null)
    useDrawStore.getState().startDiagramDraft(cls, {
      diagramId: openDiagramId,
      x: points[0][0],
      y: points[0][1],
      path: points,
    })
  }

  // Tracks the cursor in diagram space while drawing a line so the
  // rubber-band segment can preview where the next click would land. Only
  // does work mid line-draft; otherwise it's the pan handler alone.
  function handleCanvasPointerMove(e: React.PointerEvent<SVGSVGElement>) {
    handleCanvasPanPointerMove(e)
    // Feed the status-bar readout on every move (before the draw-preview early
    // returns below), imperatively so only that small label repaints.
    statusBarRef.current?.set(clientToSvgPoint(e.clientX, e.clientY))
    if (drawingElementLine && elemLineDraft) {
      const svgPoint = clientToSvgPoint(e.clientX, e.clientY)
      if (svgPoint) {
        // Preview exactly what the next click will commit -- grid snap, plus
        // the right-angle lock (forced for bus/tap, else the Ortho toggle) --
        // so the rubber-band reads as the run it'll become.
        const last = elemLineDraft.length > 0 ? elemLineDraft[elemLineDraft.length - 1] : undefined
        const forceOrtho = elementDrawKind !== "line" && elementDrawKind !== "control" && last !== undefined
        let p: { x: number; y: number } = svgPoint
        if (gridEnabled) p = snapToGrid(p)
        if ((forceOrtho || orthoEnabled) && last) p = orthoSnap(last, p)
        setElemLinePreview(p)
      }
      return
    }
    if (!isLineDrawing || !lineDraft) return
    const svgPoint = clientToSvgPoint(e.clientX, e.clientY)
    // Preview where the vertex will actually land under the active aids.
    if (svgPoint) setLinePreview(applyDrawAids(svgPoint, lineDraft[lineDraft.length - 1]))
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
  function handlePlacementPointerDown(e: React.PointerEvent<SVGElement>, placement: DiagramPlacement) {
    if (canvasMode === "pan") return
    if (!editMode) return // view mode: no dragging (selection still bubbles)
    const svgPoint = clientToSvgPoint(e.clientX, e.clientY)
    if (!svgPoint) return
    e.currentTarget.setPointerCapture(e.pointerId)
    setDragState({
      placementId: placement.id,
      featureClass: placement.feature_class,
      featureId: placement.feature_id,
      offsetX: placement.x - svgPoint.x,
      offsetY: placement.y - svgPoint.y,
      startX: placement.x,
      startY: placement.y,
      x: placement.x,
      y: placement.y,
      // Carry the drawn polyline (a busbar/conductor path) so the move
      // translates the whole bar, not just its anchor. A point placement has
      // none, so this stays undefined and the drag is a plain x/y move.
      basePath: placement.path && placement.path.length >= 2 ? placement.path : undefined,
      moved: false,
      clientX: e.clientX,
      clientY: e.clientY,
    })
  }

  // Snapping (the view toolbar's Magnet toggle) aligns the dragged
  // placement's x or y, independently per axis, to any other
  // placement's exact x or y within a threshold scaled to the
  // diagram's own current extent -- the same extent-relative sizing
  // reasoning icon/font sizing already uses, so the snap distance feels
  // consistent regardless of what coordinate scale the diagram was
  // authored in.
  function handlePlacementPointerMove(e: React.PointerEvent<SVGElement>) {
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
    // Grid snap layers on top of the magnet: with the grid on, a dragged
    // symbol lands on a cell (a peer-alignment magnet snap above still wins
    // for the axis it caught, since that value is already grid-aligned).
    if (gridEnabled) ({ x, y } = snapToGrid({ x, y }))
    // A line placement drags its whole polyline: shift every vertex by the same
    // delta its anchor moved (dx/dy off the start), so a busbar/conductor keeps
    // its shape and just relocates -- the same translate ElementDragState does.
    const dx = x - dragState.startX
    const dy = y - dragState.startY
    const path = dragState.basePath?.map(([px, py]) => [px + dx, py + dy] as DiagramPathPoint)
    setDragState((prev) => prev && { ...prev, x, y, path, moved: true })
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
    const { placementId, featureClass, featureId, x, y, path, moved, clientX, clientY } = dragState
    if (!moved) {
      setDragState(null)
      selectOrPickTarget(featureClass, featureId, placementId, clientX, clientY)
      return
    }
    const rotation = placements.find((p) => p.id === placementId)?.rotation ?? 0
    queryClient.setQueryData<{ placements: DiagramPlacement[] }>(["diagram-placements", openDiagramId], (old) =>
      old ? { placements: old.placements.map((p) => (p.id === placementId ? { ...p, x, y, ...(path ? { path } : {}) } : p)) } : old,
    )
    setDragState(null)
    try {
      await apiFetch(`/diagrams/${openDiagramId}/placements`, {
        method: "POST",
        // Include the translated path only for a line placement drag, so a
        // moved busbar/conductor persists its new geometry; placeFeature's
        // upsert writes path = EXCLUDED.path alongside x/y/rotation.
        body: JSON.stringify({ feature_class: featureClass, feature_id: featureId, x, y, rotation, ...(path ? { path } : {}) }),
      })
    } catch (err) {
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not move this feature.")
      queryClient.invalidateQueries({ queryKey: ["diagram-placements", openDiagramId] })
    }
  }

  // Reshape one vertex of a drawn line placement. stopPropagation so grabbing
  // a vertex handle doesn't also start the whole-bar drag beneath it; the
  // working `path` starts as the placement's current polyline.
  function handleVertexPointerDown(
    e: React.PointerEvent<SVGElement>,
    placement: DiagramPlacement,
    index: number,
  ) {
    if (canvasMode === "pan") return
    if (!placement.path) return
    e.stopPropagation()
    e.currentTarget.setPointerCapture(e.pointerId)
    setVertexDrag({
      placementId: placement.id,
      featureClass: placement.feature_class,
      featureId: placement.feature_id,
      index,
      path: placement.path,
      moved: false,
    })
  }

  function handleVertexPointerMove(e: React.PointerEvent<SVGElement>) {
    if (!vertexDrag) return
    const svgPoint = clientToSvgPoint(e.clientX, e.clientY)
    if (!svgPoint) return
    let x = svgPoint.x
    let y = svgPoint.y
    // Snap a moved vertex the same way a dragged placement snaps: to any other
    // placement's exact x/y (magnet), then to the grid cell -- so a reshaped
    // bar still lines up cleanly with the rest of the schematic.
    if (snapEnabled) {
      const ext = Math.max(bounds.maxX - bounds.minX, bounds.maxY - bounds.minY, MIN_EXTENT)
      const threshold = ext * SNAP_THRESHOLD_FRACTION
      const others = placements.filter((p) => p.id !== vertexDrag.placementId)
      x = snapCoordinate(x, others.map((p) => p.x), threshold)
      y = snapCoordinate(y, others.map((p) => p.y), threshold)
    }
    if (gridEnabled) ({ x, y } = snapToGrid({ x, y }))
    setVertexDrag((prev) => {
      if (!prev) return prev
      const path = prev.path.map((pt, i) => (i === prev.index ? ([x, y] as DiagramPathPoint) : pt))
      return { ...prev, path, moved: true }
    })
  }

  async function handleVertexPointerUp() {
    if (!vertexDrag) return
    const { placementId, path, moved } = vertexDrag
    setVertexDrag(null)
    if (!moved) return
    const placement = placements.find((p) => p.id === placementId)
    if (placement) await persistPath(placement, path)
  }

  // Persist a line placement's reshaped polyline via the place/move upsert --
  // the same POST a fresh draw uses. x/y are re-anchored to the first vertex,
  // exactly as finishLineDraft does, so the anchor stays consistent with the
  // path after any reshape (drag/insert/remove). Optimistic; invalidate rolls
  // back on failure. Shared by vertex drag, insert and remove below.
  async function persistPath(placement: DiagramPlacement, path: DiagramPathPoint[]) {
    if (!openDiagramId) return
    const [ax, ay] = path[0]
    queryClient.setQueryData<{ placements: DiagramPlacement[] }>(["diagram-placements", openDiagramId], (old) =>
      old ? { placements: old.placements.map((p) => (p.id === placement.id ? { ...p, x: ax, y: ay, path } : p)) } : old,
    )
    try {
      await apiFetch(`/diagrams/${openDiagramId}/placements`, {
        method: "POST",
        body: JSON.stringify({
          feature_class: placement.feature_class,
          feature_id: placement.feature_id,
          x: ax,
          y: ay,
          rotation: placement.rotation,
          path,
        }),
      })
    } catch (err) {
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not reshape this line.")
      queryClient.invalidateQueries({ queryKey: ["diagram-placements", openDiagramId] })
    }
  }

  // Insert a vertex at the midpoint of segment `segIndex` (between path[segIndex]
  // and path[segIndex+1]), so a straight run can be bent. The new vertex lands
  // exactly on the line, ready to be dragged. Reshaping a busbar/conductor never
  // drops below two points, so no lower guard is needed here.
  function insertVertex(placement: DiagramPlacement, segIndex: number) {
    const path = placement.path
    if (!path || segIndex < 0 || segIndex >= path.length - 1) return
    const [ax, ay] = path[segIndex]
    const [bx, by] = path[segIndex + 1]
    const mid: DiagramPathPoint = [(ax + bx) / 2, (ay + by) / 2]
    const next = [...path.slice(0, segIndex + 1), mid, ...path.slice(segIndex + 1)]
    void persistPath(placement, next)
  }

  // Remove the vertex at `index`, but never below two points -- a line needs
  // its two ends. The backend's validatePlacementPath enforces the same floor,
  // so this guard is just so the affordance no-ops rather than erroring.
  function removeVertex(placement: DiagramPlacement, index: number) {
    const path = placement.path
    if (!path || path.length <= 2) return
    const next = path.filter((_, i) => i !== index) as DiagramPathPoint[]
    void persistPath(placement, next)
  }

  // Flip one placement's label visibility (migration 0054). A dedicated PATCH,
  // not the place/move upsert, so it never disturbs geometry -- and equally,
  // dragging the feature never resets the label. Optimistic, with a rollback
  // via invalidate on failure, matching every other mutation here.
  async function toggleLabelHidden(placement: DiagramPlacement) {
    if (!openDiagramId) return
    const next = !placement.label_hidden
    queryClient.setQueryData<{ placements: DiagramPlacement[] }>(["diagram-placements", openDiagramId], (old) =>
      old ? { placements: old.placements.map((p) => (p.id === placement.id ? { ...p, label_hidden: next } : p)) } : old,
    )
    try {
      await apiFetch(`/diagrams/${openDiagramId}/placements/${placement.id}`, {
        method: "PATCH",
        body: JSON.stringify({ label_hidden: next }),
      })
    } catch (err) {
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not update this label.")
      queryClient.invalidateQueries({ queryKey: ["diagram-placements", openDiagramId] })
    }
  }

  // Duplicate a placed feature from within the diagram. Reuses Phase 20's
  // Duplicate spine (docs/phase-20-split-copy.md): opens the shared create form
  // pre-seeded from the source feature's attributes/subtype/name, and marks the
  // draft to place the new feature on THIS diagram, a short offset off the
  // original so it isn't hidden underneath it. When the source class carries map
  // geometry, an offset copy of that geometry rides along too (so the duplicate
  // is a full map feature, exactly like the panel's own Duplicate); a
  // geometry-less schematic class duplicates as a pure diagram feature. Save is
  // still a plain POST /features then a placement upsert -- RBAC, validation and
  // territory assignment are the manual create's. Connections/relationships are
  // deliberately not copied (they're facts about the original).
  function duplicatePlacement(placement: DiagramPlacement, feature: FeatureRecord | undefined) {
    if (!openDiagramId) return
    // Offset in the diagram's own local x/y space, sized to the icon so the copy
    // lands a clearly-separate, draggable distance off -- the same extent-relative
    // sizing the rest of the canvas uses.
    const offset = iconSize * 1.1
    const geometry = feature?.geometry
      ? offsetGeometry(feature.geometry, DUPLICATE_MAP_OFFSET_METERS, DUPLICATE_MAP_OFFSET_METERS)
      : undefined
    useDrawStore.getState().startDiagramCopyDraft(
      placement.feature_class,
      {
        name: feature?.name ?? undefined,
        subtype: feature?.subtype,
        attributes: { ...(feature?.attributes ?? {}) },
      },
      {
        diagramId: openDiagramId,
        x: placement.x + offset,
        y: placement.y + offset,
        rotation: placement.rotation,
        // A line-shaped point class carrying a hand-drawn diagram path (rare for
        // a point placement, but possible) keeps its shape, translated by the
        // same offset so the copy is separated the way its anchor is.
        ...(placement.path
          ? { path: placement.path.map(([px, py]) => [px + offset, py + offset] as DiagramPathPoint) }
          : {}),
      },
      geometry,
    )
  }

  // Persist part of a placement's label transform (migration 0056) -- the same
  // dedicated, geometry-free PATCH toggleLabelHidden uses, so arranging a label
  // never disturbs the feature and a feature move never resets the label.
  // Optimistic; the invalidate reconciles on failure.
  async function patchLabel(
    placement: DiagramPlacement,
    patch: Partial<Pick<DiagramPlacement, "label_dx" | "label_dy" | "label_rotation" | "label_scale">>,
  ) {
    if (!openDiagramId) return
    queryClient.setQueryData<{ placements: DiagramPlacement[] }>(["diagram-placements", openDiagramId], (old) =>
      old ? { placements: old.placements.map((p) => (p.id === placement.id ? { ...p, ...patch } : p)) } : old,
    )
    try {
      await apiFetch(`/diagrams/${openDiagramId}/placements/${placement.id}`, {
        method: "PATCH",
        body: JSON.stringify(patch),
      })
    } catch (err) {
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not update this label.")
      queryClient.invalidateQueries({ queryKey: ["diagram-placements", openDiagramId] })
    }
  }

  // Drag a selected placement's label. The screen-space pointer delta is
  // inverse-rotated by the glyph's rotation so it lands in the glyph-local
  // frame label_dx/label_dy are stored in (a conductor label passes rotation
  // 0, so the delta is used as-is). Only a real move persists, so a click that
  // doesn't drag leaves the label exactly where it was.
  function handleLabelPointerDown(e: React.PointerEvent<SVGTextElement>, placement: DiagramPlacement, glyphRotationDeg: number) {
    if (canvasMode === "pan") return
    e.stopPropagation()
    const svgPoint = clientToSvgPoint(e.clientX, e.clientY)
    if (!svgPoint) return
    e.currentTarget.setPointerCapture(e.pointerId)
    setLabelDrag({
      placementId: placement.id,
      featureClass: placement.feature_class,
      featureId: placement.feature_id,
      glyphRotationDeg,
      startPointerX: svgPoint.x,
      startPointerY: svgPoint.y,
      startDX: placement.label_dx ?? 0,
      startDY: placement.label_dy ?? 0,
      dx: placement.label_dx ?? 0,
      dy: placement.label_dy ?? 0,
      moved: false,
    })
  }

  function handleLabelPointerMove(e: React.PointerEvent<SVGTextElement>) {
    if (!labelDrag) return
    const svgPoint = clientToSvgPoint(e.clientX, e.clientY)
    if (!svgPoint) return
    const dxs = svgPoint.x - labelDrag.startPointerX
    const dys = svgPoint.y - labelDrag.startPointerY
    const r = (labelDrag.glyphRotationDeg * Math.PI) / 180
    const cos = Math.cos(r)
    const sin = Math.sin(r)
    // local = Rz(-glyphRotation) . screenDelta (SVG rotate is +clockwise).
    const localDX = cos * dxs + sin * dys
    const localDY = -sin * dxs + cos * dys
    setLabelDrag(
      (prev) =>
        prev && {
          ...prev,
          dx: prev.startDX + localDX,
          dy: prev.startDY + localDY,
          moved: prev.moved || Math.abs(dxs) > 1 || Math.abs(dys) > 1,
        },
    )
  }

  async function handleLabelPointerUp() {
    if (!labelDrag) return
    const { placementId, dx, dy, moved } = labelDrag
    setLabelDrag(null)
    if (!moved) return
    const placement = placements.find((p) => p.id === placementId)
    if (placement) await patchLabel(placement, { label_dx: dx, label_dy: dy })
  }

  // Step a label's rotation (degrees, wrapped to [0, 360)) or its font-size
  // multiplier (clamped to a sane 0.5x..3x, rounded so it stays tidy).
  function rotateLabel(placement: DiagramPlacement, deltaDeg: number) {
    const next = ((((placement.label_rotation ?? 0) + deltaDeg) % 360) + 360) % 360
    void patchLabel(placement, { label_rotation: next })
  }

  function scaleLabel(placement: DiagramPlacement, factor: number) {
    const raw = (placement.label_scale ?? 1) * factor
    const next = Math.round(Math.min(3, Math.max(0.5, raw)) * 100) / 100
    void patchLabel(placement, { label_scale: next })
  }

  // Step a label's offset by a fixed amount -- the button-driven counterpart
  // to dragging the label text directly. dx/dy are already in the glyph-local
  // frame label_dx/label_dy are stored in (the same frame the drag persists),
  // so a nudge and a drag compose cleanly.
  function nudgeLabel(placement: DiagramPlacement, dx: number, dy: number) {
    void patchLabel(placement, {
      label_dx: (placement.label_dx ?? 0) + dx,
      label_dy: (placement.label_dy ?? 0) + dy,
    })
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

  // --- Non-feature elements (Phase 24 keystone) -------------------------
  // Create/patch/delete an element, reusing the same inline-apiFetch +
  // invalidate approach every placement mutation above already uses (this
  // canvas deliberately keeps its mutations inline rather than behind hook
  // wrappers). Patches are optimistic where a drag/resize/rename needs the
  // node not to visibly snap back mid-request; the invalidate reconciles.
  async function createElement(kind: DiagramElementKind, body: Record<string, unknown>): Promise<DiagramElement | undefined> {
    if (!openDiagramId) return
    try {
      const created = await apiFetch<DiagramElement>(`/diagrams/${openDiagramId}/elements`, {
        method: "POST",
        body: JSON.stringify({ kind, ...body }),
      })
      queryClient.invalidateQueries({ queryKey: ["diagram-elements", openDiagramId] })
      return created
    } catch (err) {
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not add this annotation.")
      return undefined
    }
  }

  async function patchElement(id: string, body: Record<string, unknown>) {
    if (!openDiagramId) return
    try {
      await apiFetch(`/diagrams/${openDiagramId}/elements/${id}`, { method: "PATCH", body: JSON.stringify(body) })
      queryClient.invalidateQueries({ queryKey: ["diagram-elements", openDiagramId] })
    } catch (err) {
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not update this annotation.")
      queryClient.invalidateQueries({ queryKey: ["diagram-elements", openDiagramId] })
    }
  }

  // Box and text drop at the current view centre (camera ?? fitBox), the one
  // spot guaranteed to be on-screen regardless of pan/zoom. A box gets a
  // default size a quarter of the view; a text opens straight into its own
  // in-place editor so the user names it immediately.
  function addBox() {
    const v = camera ?? fitBox
    const w = v.w * 0.3
    const h = v.h * 0.25
    void createElement("box", {
      x: v.x + v.w / 2 - w / 2,
      y: v.y + v.h / 2 - h / 2,
      width: w,
      height: h,
      style: { stroke: "var(--muted-foreground)", dash: true },
    })
  }

  async function addText() {
    const v = camera ?? fitBox
    const created = await createElement("text", { x: v.x + v.w / 2, y: v.y + v.h / 2, text: "Label", style: {} })
    if (created) setEditingText({ id: created.id, value: created.text ?? "" })
  }

  // Stamps a stencil-palette symbol (Phase 24 Slice 2) at the view centre, the
  // same drop-on-screen spot addBox/addText use. Unlike those, a symbol's x/y
  // is its *centre* (it renders like a placement icon: translate to x/y, rotate,
  // draw the glyph centred), so it lands dead-centre and is ready to drag or
  // rotate into place. The glyph lives in style.iconRef; style.color tints a
  // single-colour {{COLOR}} glyph, a no-op on full-colour artwork.
  function addSymbol(iconRef: string) {
    const v = camera ?? fitBox
    void createElement("symbol", {
      x: v.x + v.w / 2,
      y: v.y + v.h / 2,
      style: { iconRef, color: "#334155" },
    })
  }

  // Element->feature promotion (Phase 25, admin-only): double-click a stencil to
  // turn it into a real feature. A stencil that maps to a class (a typed slot, a
  // `symbol` via its stencil's promote_class, or a line-shaped `bus`/`line` with
  // an explicit promoteClass) promotes straight to that class -- a template
  // busbar becomes a real MV_BUSBAR at the same length, no re-digitizing. A plain
  // decoration with no mapping opens the class picker so the admin chooses what
  // it becomes. Either way it reuses the digitize spine: startDiagramDraft seeds
  // the shared create form (feature-panel.tsx renders it) targeted at the class,
  // carrying the element's position/rotation (and path, for a line) plus its id,
  // so Save swaps the stencil for the feature in one atomic promote.
  //
  // The class a stencil intrinsically stands for, or undefined for pure
  // decoration. Mirrors the server's bakedPromoteClass (diagram/promote.go): a
  // typed `slot` -> its slot_class; a `symbol` glyph -> its stencil's
  // promote_class (promoteClassByRef); a line-shaped element (`bus`/`line`) ->
  // the explicit `promoteClass` on its own style (e.g. the "Double busbar"
  // template tags its buses MV_BUSBAR).
  function bakedPromoteClass(element: DiagramElement): string | undefined {
    if (element.kind === "slot") return element.slot_class ?? undefined
    const explicit = typeof element.style?.promoteClass === "string" ? element.style.promoteClass : undefined
    const iconRef = typeof element.style?.iconRef === "string" ? element.style.iconRef : undefined
    return explicit ?? (iconRef ? promoteClassByRef[iconRef] : undefined)
  }

  // The classes an unmapped element may be promoted into (the picker's list):
  // diagram-creatable classes (geometry-less, so born diagram-only) whose
  // DIAGRAM shape matches the element -- a drawn line can only become a line
  // class, a point marker only a point/none one, so placeFeature's path-shape
  // gate can't reject the result. Placeable classes only.
  function eligiblePromoteClasses(element: DiagramElement): FeatureClassSummary[] {
    const isLine = !!(element.path && element.path.length >= 2)
    return geometryLessClasses.filter((fc) => {
      if (fc.is_diagram_placeable === false) return false
      const shape = fc.diagram_geometry_type ?? fc.geometry_type
      return isLine ? shape === "line" : shape !== "line"
    })
  }

  // Seed the shared create form to promote `element` into `promoteClass`,
  // carrying the stencil's position/path and its id (so Save runs the atomic
  // promote endpoint, which places the new feature where the stencil sat and
  // consumes it). A line-shaped element keeps its whole drawn polyline
  // (migration 0053); a point marker drops at its x/y. A typed slot seeds its
  // pinned subtype so the form opens on it.
  function beginPromote(element: DiagramElement, promoteClass: string) {
    if (!openDiagramId) return
    const path = element.path && element.path.length >= 2 ? element.path : undefined
    const diagramPlacement = {
      diagramId: openDiagramId,
      x: element.x,
      y: element.y,
      rotation: element.rotation,
      promoteFromElementId: element.id,
      ...(path ? { path } : {}),
    }
    const subtypeSeed = element.kind === "slot" ? (element.slot_subtype ?? undefined) : undefined
    if (subtypeSeed != null) {
      useDrawStore.getState().startDiagramCopyDraft(promoteClass, { subtype: subtypeSeed, attributes: {} }, diagramPlacement)
    } else {
      useDrawStore.getState().startDiagramDraft(promoteClass, diagramPlacement)
    }
    // Promotion may have been launched from either overlay -- close both.
    setSlotBinding(null)
    setPromotePicker(null)
  }

  function promoteElement(element: DiagramElement) {
    if (canvasMode === "pan" || !openDiagramId) return
    if (!editMode) return // view mode: a double-click just selects, never promotes
    // Admin-only: promoting a decoration into shared org-wide data is a
    // model-authoring act (metadata:write), enforced again on the server. Tell
    // a non-admin why nothing happens rather than swallowing the gesture.
    if (!canPromote) {
      useToastStore
        .getState()
        .pushWarning("Turning a stencil into a real feature needs the metadata:write permission.")
      return
    }
    // A stencil's own mapping wins; a plain decoration with none opens the
    // class picker (the admin chooses what it becomes) instead of guessing.
    const baked = bakedPromoteClass(element)
    if (baked) {
      beginPromote(element, baked)
      return
    }
    const eligible = eligiblePromoteClasses(element)
    if (eligible.length === 0) {
      useToastStore
        .getState()
        .pushWarning("There's no diagram-native feature class this element can be promoted into.")
      return
    }
    setPromotePicker({ element, classes: eligible })
  }

  // Sheet furniture (Phase 24 Slice 3). A title block drops as a bordered
  // key/value card at the view centre; its rows live in style.fields (a stable
  // default schedule) and each value is double-click editable in place. A
  // legend drops as a bordered card that auto-derives its rows from what's on
  // the diagram (legendEntries) -- only its heading is stored. Both are plain
  // box-like elements (x/y = top-left), draggable and corner-resizable via the
  // existing element handlers.
  function addTitleBlock() {
    const v = camera ?? fitBox
    const w = v.w * 0.34
    const h = v.h * 0.26
    void createElement("title_block", {
      x: v.x + v.w / 2 - w / 2,
      y: v.y + v.h / 2 - h / 2,
      width: w,
      height: h,
      style: {
        stroke: "var(--foreground)",
        fields: [
          { label: "Title", value: "" },
          { label: "Drawing No.", value: "" },
          { label: "Rev", value: "" },
          { label: "Date", value: "" },
          { label: "Drawn By", value: "" },
          { label: "Sheet", value: "" },
        ],
      },
    })
  }

  function addLegend() {
    const v = camera ?? fitBox
    const w = v.w * 0.24
    const h = v.h * 0.34
    void createElement("legend", {
      x: v.x + v.w / 2 - w / 2,
      y: v.y + v.h / 2 - h / 2,
      width: w,
      height: h,
      style: { stroke: "var(--foreground)", title: "Legend" },
    })
  }

  // Commits an in-place title-block field edit: rebuilds style.fields with the
  // one row's value replaced, then optimistically patches the whole style
  // (style is a single jsonb column, so a value change rewrites it wholesale --
  // the same shape createElement/patchElement already round-trip).
  async function commitFieldEdit() {
    if (!editingField || !openDiagramId) return
    const { id, index, value } = editingField
    setEditingField(null)
    const element = elements.find((el) => el.id === id)
    if (!element) return
    const current = Array.isArray(element.style?.fields)
      ? (element.style.fields as { label: string; value: string }[])
      : []
    const fields = current.map((f, i) => (i === index ? { ...f, value } : f))
    const nextStyle = { ...element.style, fields }
    queryClient.setQueryData<{ elements: DiagramElement[] }>(["diagram-elements", openDiagramId], (old) =>
      old ? { elements: old.elements.map((el) => (el.id === id ? { ...el, style: nextStyle } : el)) } : old,
    )
    await patchElement(id, { style: nextStyle })
  }

  // Binds a template slot (Phase 24 Slice 4) to a real feature the user picked
  // in the class-scoped search: materialise a normal placement at the slot's own
  // position (so the bound feature is first-class -- it joins trace/connectivity,
  // unlike the slot marker), then delete the now-consumed slot element. Reuses
  // the existing POST /placements + DELETE /elements endpoints, so binding needs
  // no bespoke server call. Refetches both placements (a new one appeared) and
  // elements (a slot vanished).
  async function bindSlot(slot: DiagramElement, featureId: string, featureClass: string) {
    // A class-tagged slot binds within its own class; an untyped slot (a
    // built-in template's generic bay) takes the class of the picked feature.
    const boundClass = slot.slot_class ?? featureClass
    if (!boundClass || !openDiagramId) return
    try {
      await apiFetch(`/diagrams/${openDiagramId}/placements`, {
        method: "POST",
        body: JSON.stringify({
          feature_class: boundClass,
          feature_id: featureId,
          x: slot.x,
          y: slot.y,
          rotation: slot.rotation,
          ...(slot.path ? { path: slot.path } : {}),
        }),
      })
      await apiFetch(`/diagrams/${openDiagramId}/elements/${slot.id}`, { method: "DELETE" })
      queryClient.invalidateQueries({ queryKey: ["diagram-placements", openDiagramId] })
      queryClient.invalidateQueries({ queryKey: ["diagram-elements", openDiagramId] })
      setSlotBinding(null)
    } catch (err) {
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not bind this slot.")
    }
  }

  // Arms click-to-draw for a schematic polyline -- a plain "line", a heavy
  // "bus", or a right-angle "tap" (Slice 5) -- cancelling any other armed mode
  // first so the canvas is only ever in one drawing state at a time.
  function startElementDraw(kind: "line" | "bus" | "tap" | "control") {
    setDigitizingClass(null)
    useDiagramPlaceStore.getState().cancel()
    setElemLineDraft(null)
    setElemLinePreview(null)
    setElementDrawKind(kind)
  }

  // Commits the in-progress sketch as an element of whatever kind is being
  // drawn: a bus is a heavy busbar, a tap a right-angle stub (rendered with a
  // T-junction dot at its bus end), a control a dashed/arrowheaded trip signal
  // line (Phase 46), a line a plain schematic line -- all just a `path` polyline
  // differing only in how they render.
  async function finishElementLine(points: DiagramPathPoint[]) {
    const kind = elementDrawKind ?? "line"
    setElementDrawKind(null)
    setElemLineDraft(null)
    setElemLinePreview(null)
    if (points.length < 2) return
    // A control line is born dashed in the signal colour so it never reads as a
    // power wire; the others keep the default solid foreground stroke.
    const style =
      kind === "control"
        ? { stroke: CONTROL_LINE_COLORS.control, dash: true }
        : { stroke: "var(--foreground)" }
    await createElement(kind, { x: points[0][0], y: points[0][1], path: points, style })
  }

  // Commits an in-place text edit: an empty label deletes the element (an
  // invisible text is never what the user wants), otherwise it's an
  // optimistic rename + patch.
  async function commitTextEdit() {
    if (!editingText || !openDiagramId) return
    const { id, value } = editingText
    setEditingText(null)
    const trimmed = value.trim()
    if (trimmed.length === 0) {
      queryClient.setQueryData<{ elements: DiagramElement[] }>(["diagram-elements", openDiagramId], (old) =>
        old ? { elements: old.elements.filter((el) => el.id !== id) } : old,
      )
      try {
        await apiFetch(`/diagrams/${openDiagramId}/elements/${id}`, { method: "DELETE" })
      } catch {
        queryClient.invalidateQueries({ queryKey: ["diagram-elements", openDiagramId] })
      }
      return
    }
    queryClient.setQueryData<{ elements: DiagramElement[] }>(["diagram-elements", openDiagramId], (old) =>
      old ? { elements: old.elements.map((el) => (el.id === id ? { ...el, text: trimmed } : el)) } : old,
    )
    await patchElement(id, { text: trimmed })
  }

  function handleElementPointerDown(e: React.PointerEvent<SVGElement>, element: DiagramElement) {
    if (canvasMode === "pan") return
    if (!editMode) return // view mode: no dragging (selection still bubbles)
    const svgPoint = clientToSvgPoint(e.clientX, e.clientY)
    if (!svgPoint) return
    e.stopPropagation()
    e.currentTarget.setPointerCapture(e.pointerId)
    setElementDrag({
      id: element.id,
      offsetX: element.x - svgPoint.x,
      offsetY: element.y - svgPoint.y,
      startX: element.x,
      startY: element.y,
      x: element.x,
      y: element.y,
      basePath: element.path,
      path: element.path,
      moved: false,
    })
  }

  function handleElementPointerMove(e: React.PointerEvent<SVGElement>) {
    if (!elementDrag) return
    const svgPoint = clientToSvgPoint(e.clientX, e.clientY)
    if (!svgPoint) return
    const x = svgPoint.x + elementDrag.offsetX
    const y = svgPoint.y + elementDrag.offsetY
    const dx = x - elementDrag.startX
    const dy = y - elementDrag.startY
    const path = elementDrag.basePath?.map(([px, py]) => [px + dx, py + dy] as DiagramPathPoint)
    setElementDrag((prev) => prev && { ...prev, x, y, path, moved: true })
  }

  async function handleElementPointerUp() {
    if (!elementDrag) return
    const { id, x, y, path, moved } = elementDrag
    setElementDrag(null)
    if (!moved || !openDiagramId) return
    queryClient.setQueryData<{ elements: DiagramElement[] }>(["diagram-elements", openDiagramId], (old) =>
      old ? { elements: old.elements.map((el) => (el.id === id ? { ...el, x, y, ...(path ? { path } : {}) } : el)) } : old,
    )
    await patchElement(id, { x, y, ...(path ? { path } : {}) })
  }

  function handleElementResizePointerDown(e: React.PointerEvent<SVGElement>, element: DiagramElement) {
    if (canvasMode === "pan") return
    if (!editMode) return
    e.stopPropagation()
    e.currentTarget.setPointerCapture(e.pointerId)
    setElementResize({ id: element.id, width: element.width ?? 0, height: element.height ?? 0 })
  }

  function handleElementResizePointerMove(e: React.PointerEvent<SVGElement>) {
    if (!elementResize) return
    const svgPoint = clientToSvgPoint(e.clientX, e.clientY)
    if (!svgPoint) return
    const element = elements.find((el) => el.id === elementResize.id)
    if (!element) return
    const minSize = Math.max(fontSize * 2, MIN_EXTENT * 0.1)
    const width = Math.max(svgPoint.x - element.x, minSize)
    const height = Math.max(svgPoint.y - element.y, minSize)
    setElementResize((prev) => prev && { ...prev, width, height })
  }

  async function handleElementResizePointerUp() {
    if (!elementResize) return
    const { id, width, height } = elementResize
    setElementResize(null)
    await patchElement(id, { width, height })
  }

  // Symbol rotation, the element analogue of handleRotateHandlePointerDown/etc.
  // for placements: the glyph's x/y is its centre, so the pointer angle is
  // measured against that and snapped to ROTATE_SNAP_DEGREES, upright = 0.
  function handleElementRotatePointerDown(e: React.PointerEvent<SVGGElement>, element: DiagramElement) {
    if (canvasMode === "pan") return
    if (!editMode) return
    e.stopPropagation()
    e.currentTarget.setPointerCapture(e.pointerId)
    setElementRotate({ id: element.id, centerX: element.x, centerY: element.y, rotation: element.rotation })
  }

  function handleElementRotatePointerMove(e: React.PointerEvent<SVGGElement>) {
    if (!elementRotate) return
    const svgPoint = clientToSvgPoint(e.clientX, e.clientY)
    if (!svgPoint) return
    const dx = svgPoint.x - elementRotate.centerX
    const dy = svgPoint.y - elementRotate.centerY
    const angleDeg = Math.atan2(dy, dx) * (180 / Math.PI)
    const raw = (((angleDeg + 90) % 360) + 360) % 360
    const snapped = (Math.round(raw / ROTATE_SNAP_DEGREES) * ROTATE_SNAP_DEGREES) % 360
    setElementRotate((prev) => prev && { ...prev, rotation: snapped })
  }

  async function handleElementRotatePointerUp() {
    if (!elementRotate) return
    const { id, rotation } = elementRotate
    setElementRotate(null)
    queryClient.setQueryData<{ elements: DiagramElement[] }>(["diagram-elements", openDiagramId], (old) =>
      old ? { elements: old.elements.map((el) => (el.id === id ? { ...el, rotation } : el)) } : old,
    )
    await patchElement(id, { rotation })
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

  function handleWheelZoom(e: WheelEvent) {
    e.preventDefault()
    const cursor = clientToSvgPoint(e.clientX, e.clientY)
    zoomBy(e.deltaY < 0 ? 1 / ZOOM_FACTOR : ZOOM_FACTOR, cursor ?? undefined)
  }

  // React attaches onWheel as a PASSIVE listener (React 17+), so a
  // preventDefault() inside a React onWheel handler is ignored and warns
  // ("Unable to preventDefault inside passive event listener invocation") --
  // and the page scroll-zooms instead of the canvas. Bind wheel natively with
  // { passive: false } so preventDefault takes. The listener stays mounted once
  // and calls through a ref to the always-latest closure, so it never zooms off
  // a stale camera/fitBox.
  const wheelHandlerRef = useRef(handleWheelZoom)
  wheelHandlerRef.current = handleWheelZoom
  // A stable listener (always calls through to the latest closure) so the
  // callback ref below can add AND remove the exact same function reference.
  const onWheelNative = useCallback((e: WheelEvent) => wheelHandlerRef.current(e), [])
  // Callback ref, NOT a mount effect: the <svg> renders CONDITIONALLY -- it's
  // behind the `isPending`/`isError` gate further down, so it doesn't exist on
  // the first render (placements are still loading). A `useEffect(..., [])`
  // binds once, at mount, when svgRef.current is still null, and with empty
  // deps never retries once the svg finally mounts -- so wheel-zoom silently
  // never attached. A callback ref fires exactly when the node mounts and
  // again (with null) when it unmounts, immune to *why* it was absent. It also
  // mirrors the node into svgRef so clientToSvgPoint and the pan handlers keep
  // reading svgRef.current.
  const attachSvgRef = useCallback(
    (node: SVGSVGElement | null) => {
      if (svgRef.current) svgRef.current.removeEventListener("wheel", onWheelNative)
      svgRef.current = node
      if (node) node.addEventListener("wheel", onWheelNative, { passive: false })
    },
    [onWheelNative],
  )

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
      result = result.map((p) =>
        p.id === dragState.placementId
          ? { ...p, x: dragState.x, y: dragState.y, ...(dragState.path ? { path: dragState.path } : {}) }
          : p,
      )
    }
    if (rotateState) {
      result = result.map((p) => (p.id === rotateState.placementId ? { ...p, rotation: rotateState.rotation } : p))
    }
    if (vertexDrag) {
      // A vertex being dragged shows its live polyline; x/y re-anchor to the
      // first vertex (finishLineDraft convention) so the label/hit-target
      // follow the reshaped bar too.
      result = result.map((p) =>
        p.id === vertexDrag.placementId
          ? { ...p, x: vertexDrag.path[0][0], y: vertexDrag.path[0][1], path: vertexDrag.path }
          : p,
      )
    }
    return result
  }, [placements, dragState, rotateState, vertexDrag])

  // The element being dragged/resized renders at its live position/size, same
  // pattern effectivePlacements uses for placements.
  const effectiveElements = useMemo(() => {
    let result = elements
    if (elementDrag) {
      result = result.map((el) =>
        el.id === elementDrag.id ? { ...el, x: elementDrag.x, y: elementDrag.y, ...(elementDrag.path ? { path: elementDrag.path } : {}) } : el,
      )
    }
    if (elementResize) {
      result = result.map((el) =>
        el.id === elementResize.id ? { ...el, width: elementResize.width, height: elementResize.height } : el,
      )
    }
    if (elementRotate) {
      result = result.map((el) => (el.id === elementRotate.id ? { ...el, rotation: elementRotate.rotation } : el))
    }
    return result
  }, [elements, elementDrag, elementResize, elementRotate])

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
  // Phase 46 Slice 2 -- derived control/trip lines. Fetch each placed feature's
  // relationships (same per-placement fan-out connectionResults uses for power
  // connectivity), so controlLineItems below can draw the protection_trip /
  // protection_sensing edges between two placed features as dashed, arrowheaded
  // signal lines. Reuses the generic GET /relationships -- no diagram-specific
  // endpoint. The shared ["relationships", class, id] query key means the
  // feature panel's Relationships tab and this canvas share one cache entry.
  const relationshipResults = useQueries({
    queries: placements.map((p) => ({
      queryKey: ["relationships", p.feature_class, p.feature_id],
      queryFn: () =>
        apiFetch<{ relationships: Relationship[] }>(
          `/relationships?feature_class=${encodeURIComponent(p.feature_class)}&feature_id=${encodeURIComponent(p.feature_id)}`,
        ),
    })),
  })

  const placementByKey = useMemo(() => {
    const map = new Map<string, DiagramPlacement>()
    effectivePlacements.forEach((p) => map.set(placementKey(p.feature_class, p.feature_id), p))
    return map
  }, [effectivePlacements])

  const pointItems = useMemo(() => {
    const items = effectivePlacements
      .map((p, i) => ({ placement: p, index: i }))
      .filter(
        (item) =>
          geometryTypeByClass[item.placement.feature_class] !== "line" &&
          !hiddenClassSet.has(item.placement.feature_class),
      )
    // Raise the selected placement to the end so it paints on top of anything
    // it overlaps: SVG paint order is array order, so a feature stacked beneath
    // another would otherwise have its hover controls (the label eye-toggle,
    // remove, duplicate and the adjust/rotate handles) hidden behind the upper
    // glyph and unclickable -- the exact "can't turn the other's label off" bug.
    // Array.sort is stable in modern engines, so every other placement keeps its
    // authoring order; `index` stays glued to each item, so the featureResults/
    // connectionResults lookup below is unaffected by the reorder.
    if (selected) {
      items.sort((a, b) => {
        const aSel =
          a.placement.feature_class === selected.featureClassCode && a.placement.feature_id === selected.featureId
        const bSel =
          b.placement.feature_class === selected.featureClassCode && b.placement.feature_id === selected.featureId
        return Number(aSel) - Number(bSel)
      })
    }
    return items
  }, [effectivePlacements, geometryTypeByClass, hiddenClassSet, selected])

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
      if (hiddenClassSet.has(placement.feature_class)) return
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
  }, [effectivePlacements, geometryTypeByClass, hiddenClassSet, connectionResults.map((r) => r.dataUpdatedAt).join(","), placementByKey])

  // The derived control/trip lines to draw (Phase 46 Slice 2): one per
  // protection_trip / protection_sensing relationship whose BOTH endpoints are
  // placed on this diagram (and neither class hidden). A relationship surfaces
  // on both endpoints' fetched lists, so dedupe by relationship id. Each item
  // carries the origin/destination anchors (origin -> destination is the signal
  // direction) and the kind (which picks the colour + arrow marker below).
  const controlLineItems = useMemo<ControlLineRender[]>(() => {
    const byId = new Map<string, ControlLineRender>()
    relationshipResults.forEach((res) => {
      const rels = res.data?.relationships ?? []
      for (const rel of rels) {
        if (!CONTROL_LINE_KINDS.has(rel.kind) || byId.has(rel.id)) continue
        const from = placementByKey.get(placementKey(rel.origin.feature_class, rel.origin.feature_id))
        const to = placementByKey.get(placementKey(rel.destination.feature_class, rel.destination.feature_id))
        if (!from || !to) continue
        if (hiddenClassSet.has(from.feature_class) || hiddenClassSet.has(to.feature_class)) continue
        byId.set(rel.id, {
          id: rel.id,
          kind: rel.kind,
          name: rel.relationship_class_name,
          from: { x: from.x, y: from.y },
          to: { x: to.x, y: to.y },
        })
      }
    })
    return Array.from(byId.values())
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [relationshipResults.map((r) => r.dataUpdatedAt).join(","), placementByKey, hiddenClassSet])

  const bounds = useMemo(() => {
    if (effectivePlacements.length === 0) return { minX: 0, minY: 0, maxX: 400, maxY: 300 }
    const xs = effectivePlacements.map((p) => p.x)
    const ys = effectivePlacements.map((p) => p.y)
    return { minX: Math.min(...xs), maxX: Math.max(...xs), minY: Math.min(...ys), maxY: Math.max(...ys) }
  }, [effectivePlacements])

  // The grid cell size in diagram-space units -- a fixed fraction of the
  // content extent (same extent-relative reasoning as icon/font sizing), so
  // the grid and its snapping feel consistent whatever scale the diagram was
  // authored in. Drives both the visual grid and snapToGrid below.
  const gridStep = useMemo(
    () => Math.max(bounds.maxX - bounds.minX, bounds.maxY - bounds.minY, MIN_EXTENT) * GRID_FRACTION,
    [bounds],
  )

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
  // Unique per open diagram so two canvases can't collide on the SVG pattern id.
  const gridPatternId = `diagram-grid-${openDiagramId}`
  // Prefix for the control/trip-line arrowhead <marker> ids (Phase 46). One
  // marker per CONTROL_LINE_COLORS slug lives in an always-present <defs>;
  // namespaced by the open diagram id so two canvases never collide.
  const arrowMarkerPrefix = `diagram-arrow-${openDiagramId}`

  // Export the diagram as a DXF (Phase 33). Walks the same resolved render
  // model the canvas draws -- conductorItems (endpoints already resolved),
  // pointItems, and elements, each carrying its own feature record for
  // symbology/name -- so the export never drifts from what's on screen. The
  // conversion + browser download live in lib/diagram-dxf.ts.
  const handleExportDxf = () => {
    downloadDiagramDxf({
      diagramName: diagramInstance?.name || diagramInstance?.diagram_type || "diagram",
      elements,
      conductors: conductorItems.map((item) => ({
        placement: item.placement,
        points: item.points,
        fullyConnected: item.fullyConnected,
        feature: featureResults[item.index]?.data,
      })),
      points: pointItems.map(({ placement, index }) => ({ placement, feature: featureResults[index]?.data })),
      rules,
      icons: symbologyIcons,
      iconSize,
      fontSize,
    })
  }

  // Print the diagram to a vector PDF drawing sheet (print engine). Opens a
  // preview dialog with paper/orientation controls; the dialog clones the LIVE
  // canvas <svg> (so the plot never drifts from screen) and lays it
  // scaled-to-fit inside a framed sheet, downloading only on confirm. WYSIWYG:
  // the clone strips only interactive-only chrome (hover handles, remove-"x",
  // inline edit inputs), so any analysis/selection halos on screen plot as shown.
  const [printOpen, setPrintOpen] = useState(false)
  const handlePrint = () => {
    if (svgRef.current) setPrintOpen(true)
  }

  return (
    <div className="bg-background flex h-full w-full min-w-0 flex-col border-l">
      <DiagramPrintDialog
        open={printOpen}
        onOpenChange={setPrintOpen}
        diagramName={diagramInstance?.name || diagramInstance?.diagram_type || "diagram"}
        svg={svgRef.current}
        fitBox={fitBox}
      />
      <div className="flex items-center gap-2 border-b px-3 py-2">
        <DiagramTitle diagramId={openDiagramId} />
        <div className="ml-auto flex shrink-0 items-center gap-1">
          {/* Edit/View mode (S3): the diagram authoring menus (add from map,
              connect/relate, annotations, stencils, templates) show only in edit
              mode -- in view mode the canvas is a read-only drawing. */}
          {editMode && (
            <>
              <AddMenu
                diagramId={openDiagramId}
                container={diagramInstance?.container}
                diagramType={diagramInstance?.diagram_type}
                placements={placements}
                pickActive={isPickingHere || isPlacingHere}
                onToggleFromMap={() =>
                  isPickingHere || isPlacingHere
                    ? useDiagramPlaceStore.getState().cancel()
                    : useDiagramPlaceStore.getState().startPicking(openDiagramId)
                }
                digitizeClasses={geometryLessClasses}
                digitizingClass={digitizingClass}
                onStartDigitize={(code) => setDigitizingClass(code)}
                onCancelDigitize={() => setDigitizingClass(null)}
              />
              <ConnectMenu
                connectActive={armingMode === "connectivity" || connectMode === "connectivity"}
                relateActive={armingMode === "relationship" || connectMode === "relationship"}
                onConnect={() => armAuthoring("connectivity")}
                onRelate={() => armAuthoring("relationship")}
              />
              <AddAnnotationMenu
                onAddBox={addBox}
                onAddText={addText}
                onAddLine={() => startElementDraw("line")}
                onAddBus={() => startElementDraw("bus")}
                onAddTap={() => startElementDraw("tap")}
                onAddControl={() => startElementDraw("control")}
                onAddTitleBlock={addTitleBlock}
                onAddLegend={addLegend}
                lineArmed={drawingElementLine}
              />
              <StencilPalette onPick={addSymbol} />
              <TemplatesMenu diagramId={openDiagramId} />
            </>
          )}
          <DiagramLayersMenu diagramId={openDiagramId} entries={diagramLayerEntries} />
          {/* Replace from map (admin): overwrite the selected diagram feature
              with a real feature picked on the geographic map -- the "add from
              map" gesture, but onto a stub that already exists (its wiring moves
              across, the stub is retired). Shown only when a placement is
              selected and no other pick/place is mid-flight; while replacing it
              stays lit and cancels the pick. */}
          {canPromote && (isReplacingHere || (selectedPlacement && !isPickingHere && !isPlacingHere)) && (
            <Button
              variant="ghost"
              size="icon"
              className={cn("size-7", isReplacingHere && "bg-accent text-accent-foreground")}
              title={
                isReplacingHere
                  ? "Replace from map — click the real feature on the map (Esc to cancel)"
                  : "Replace from map — overwrite this diagram feature with a real feature you pick on the map"
              }
              onClick={() =>
                isReplacingHere
                  ? useDiagramPlaceStore.getState().cancel()
                  : selectedPlacement &&
                    useDiagramPlaceStore.getState().startReplacing(openDiagramId, {
                      featureClassCode: selectedPlacement.feature_class,
                      featureId: selectedPlacement.feature_id,
                    })
              }
            >
              <Replace className="size-4" />
              <span className="sr-only">Replace from map</span>
            </Button>
          )}
          {/* Place on map: the mirror of Add-from-map. A diagram feature with no
              map geometry gets its footprint drawn on the map (arms the map draw
              bound to this feature; the drawn geometry is PATCHed onto it). We
              close the diagram so the map is visible to draw on. */}
          {canPlaceOnMap && (
            <Button
              variant="ghost"
              size="icon"
              className="size-7"
              title="Place on map — draw this diagram feature's geometry on the map"
              onClick={() => {
                if (!selected || !selectedMapGeomType) return
                useDrawStore
                  .getState()
                  .startDigitize(selected.featureClassCode, selected.featureId, selectedMapGeomType as "point" | "line" | "polygon")
                useToastStore
                  .getState()
                  .pushSuccess("Draw the geometry on the map — double-click to finish, Esc to cancel.")
                close()
              }}
            >
              <MapPin className="size-4" />
              <span className="sr-only">Place on map</span>
            </Button>
          )}
          <Button
            variant="ghost"
            size="icon"
            className="size-7"
            title="Export as DXF — open in CAD"
            onClick={handleExportDxf}
          >
            <Download className="size-4" />
            <span className="sr-only">Export as DXF</span>
          </Button>
          <Button
            variant="ghost"
            size="icon"
            className="size-7"
            title="Print — vector PDF drawing sheet"
            onClick={handlePrint}
          >
            <Printer className="size-4" />
            <span className="sr-only">Print to PDF</span>
          </Button>
          <Button
            variant="ghost"
            size="icon"
            className={cn("size-7", showEnergisation && "bg-accent text-accent-foreground")}
            title="Energisation — shade de-energised features (below an open device)"
            onClick={() => setShowEnergisation((v) => !v)}
          >
            <Zap className="size-4" />
            <span className="sr-only">Toggle energisation shading</span>
          </Button>
          <Button
            variant="ghost"
            size="icon"
            className={cn("size-7", maximized && "bg-accent text-accent-foreground")}
            title={maximized ? "Exit full page — show the map alongside" : "Full page — hide the map"}
            onClick={() =>
              maximized
                ? useDiagramStore.getState().restore()
                : useDiagramStore.getState().maximize(openDiagramId)
            }
          >
            {maximized ? <Minimize className="size-4" /> : <Maximize className="size-4" />}
            <span className="sr-only">{maximized ? "Exit full page" : "Full page"}</span>
          </Button>
          {diagramInstance?.regenerable && editMode && (
            <Button
              variant="ghost"
              size="icon"
              className="size-7"
              title="Regenerate — rebuild this single-line from its template"
              disabled={regenerate.isPending}
              onClick={handleRegenerate}
            >
              {regenerate.isPending ? <Loader2 className="size-4 animate-spin" /> : <RefreshCw className="size-4" />}
              <span className="sr-only">Regenerate diagram</span>
            </Button>
          )}
          {canDeleteDiagram && (
            <Button
              variant="ghost"
              size="icon"
              className="text-destructive hover:text-destructive size-7"
              title="Delete this diagram"
              onClick={() => setDeletingDiagram(true)}
            >
              <Trash2 className="size-4" />
              <span className="sr-only">Delete diagram</span>
            </Button>
          )}
          <Button variant="ghost" size="icon" className="size-7" onClick={close}>
            <X className="size-4" />
            <span className="sr-only">Close diagram</span>
          </Button>
        </div>
      </div>
      <DeleteDiagramDialog
        diagramId={deletingDiagram ? openDiagramId : null}
        diagramName={diagramInstance?.name || diagramInstance?.diagram_type || null}
        onOpenChange={(open) => setDeletingDiagram(open)}
        onDeleted={close}
      />
      <div className="relative min-h-0 flex-1 overflow-auto">
        {isPending ? (
          <div className="text-muted-foreground p-4 text-sm">Loading diagram...</div>
        ) : isError ? (
          <div className="text-destructive p-4 text-sm">Could not load this diagram.</div>
        ) : (
          <>
            <DiagramViewToolbar
              key={openDiagramId}
              mode={canvasMode}
              onModeChange={setCanvasMode}
              onZoomIn={() => zoomBy(1 / ZOOM_FACTOR)}
              onZoomOut={() => zoomBy(ZOOM_FACTOR)}
              onFit={() => setCamera(fitBox)}
              snapEnabled={snapEnabled}
              onToggleSnap={() => setSnapEnabled((s) => !s)}
              gridEnabled={gridEnabled}
              onToggleGrid={() => setGridEnabled((g) => !g)}
              orthoEnabled={orthoEnabled}
              onToggleOrtho={() => setOrthoEnabled((o) => !o)}
            />
            {/* Label controls for the selected placement -- a contextual bar
                pinned to the bottom-centre of the canvas (out of the way), not
                floating over the feature where it blocked the view. Shown only
                when a placement with a visible label is selected. */}
            {selectedPlacement && !selectedPlacement.label_hidden && (
              <div className="absolute bottom-3 left-1/2 z-10 -translate-x-1/2">
                <LabelAdjustBar
                  step={fontSize * 0.6}
                  onNudge={(dx, dy) => nudgeLabel(selectedPlacement, dx, dy)}
                  onRotate={(d) => rotateLabel(selectedPlacement, d)}
                  onScale={(f) => scaleLabel(selectedPlacement, f)}
                />
              </div>
            )}
            {(isPickingHere || isPlacingHere) && (
              <div className="bg-popover text-popover-foreground absolute top-2 left-1/2 z-10 flex -translate-x-1/2 items-center gap-2 rounded-md border px-3 py-1.5 text-xs shadow-sm">
                <Crosshair className="text-muted-foreground size-3.5" />
                <span>
                  {isReplacingHere
                    ? "Click the real feature on the map to replace this one"
                    : isPickingHere
                      ? "Click a feature on the map to place it here"
                      : isLineDrawing
                        ? "Click to add line points; double-click to finish"
                        : "Click on the diagram to drop the feature"}
                </span>
                {isLineDrawing && (lineDraft?.length ?? 0) >= 2 && (
                  <button
                    type="button"
                    onClick={() => lineDraft && finishLineDraft(lineDraft)}
                    className="text-primary font-medium"
                  >
                    Finish
                  </button>
                )}
                <button
                  type="button"
                  onClick={() => useDiagramPlaceStore.getState().cancel()}
                  className="text-muted-foreground hover:text-foreground font-medium"
                >
                  Cancel
                </button>
                <span className="text-muted-foreground">or Esc</span>
              </div>
            )}
            {digitizeIsLine && (
              <div className="bg-popover text-popover-foreground absolute top-2 left-1/2 z-10 flex -translate-x-1/2 items-center gap-2 rounded-md border px-3 py-1.5 text-xs shadow-sm">
                <PenLine className="text-muted-foreground size-3.5" />
                <span>Click to add line points; double-click to finish</span>
                {(lineDraft?.length ?? 0) >= 2 && (
                  <button
                    type="button"
                    onClick={() => lineDraft && finishDigitizeLine(lineDraft)}
                    className="text-primary font-medium"
                  >
                    Finish
                  </button>
                )}
                <button
                  type="button"
                  onClick={() => setDigitizingClass(null)}
                  className="text-muted-foreground hover:text-foreground font-medium"
                >
                  Cancel
                </button>
                <span className="text-muted-foreground">or Esc</span>
              </div>
            )}
            {drawingElementLine && (
              <div className="bg-popover text-popover-foreground absolute top-2 left-1/2 z-10 flex -translate-x-1/2 items-center gap-2 rounded-md border px-3 py-1.5 text-xs shadow-sm">
                {elementDrawKind === "bus" ? (
                  <Minus className="text-muted-foreground size-3.5" />
                ) : elementDrawKind === "tap" ? (
                  <Waypoints className="text-muted-foreground size-3.5" />
                ) : elementDrawKind === "control" ? (
                  <Spline className="text-muted-foreground size-3.5" />
                ) : (
                  <PenLine className="text-muted-foreground size-3.5" />
                )}
                <span>
                  {elementDrawKind === "bus"
                    ? "Click to route the bus (right-angle); double-click to finish"
                    : elementDrawKind === "tap"
                      ? "Click the device, then the bus; double-click to finish the tap"
                      : elementDrawKind === "control"
                        ? "Click the source, then the target; double-click to finish the control line"
                        : "Click to add line points; double-click to finish"}
                </span>
                {(elemLineDraft?.length ?? 0) >= 2 && (
                  <button
                    type="button"
                    onClick={() => elemLineDraft && void finishElementLine(elemLineDraft)}
                    className="text-primary font-medium"
                  >
                    Finish
                  </button>
                )}
                <button
                  type="button"
                  onClick={() => {
                    setElementDrawKind(null)
                    setElemLineDraft(null)
                    setElemLinePreview(null)
                  }}
                  className="text-muted-foreground hover:text-foreground font-medium"
                >
                  Cancel
                </button>
                <span className="text-muted-foreground">or Esc</span>
              </div>
            )}
            {authoringMode && (
              <div className="bg-popover text-popover-foreground absolute top-2 left-1/2 z-10 flex -translate-x-1/2 items-center gap-2 rounded-md border px-3 py-1.5 text-xs shadow-sm">
                {authoringMode === "relationship" ? (
                  <Link2 className="text-muted-foreground size-3.5" />
                ) : (
                  <Cable className="text-muted-foreground size-3.5" />
                )}
                <span>
                  {authoringMode === "relationship"
                    ? authoringAwaitingSource
                      ? "Click a feature to relate from"
                      : "Click a second feature to relate to"
                    : authoringAwaitingSource
                      ? "Click a feature to connect from"
                      : "Click a second feature to connect to"}
                </span>
                <button
                  type="button"
                  onClick={() => {
                    setArmingMode(null)
                    useConnectStore.getState().cancel()
                  }}
                  className="text-muted-foreground hover:text-foreground font-medium"
                >
                  Cancel
                </button>
                <span className="text-muted-foreground">or Esc</span>
              </div>
            )}
            {slotBinding && editMode && (
              <SlotBindPanel
                slot={slotBinding}
                onBind={(featureId, featureClass) => void bindSlot(slotBinding, featureId, featureClass)}
                onCreateNew={() => promoteElement(slotBinding)}
                canCreate={canPromote}
                onCancel={() => setSlotBinding(null)}
              />
            )}
            {promotePicker && (
              <PromoteClassPicker
                element={promotePicker.element}
                classes={promotePicker.classes}
                onPick={(code) => beginPromote(promotePicker.element, code)}
                onCancel={() => setPromotePicker(null)}
              />
            )}
            {replaceConfirm && (
              <ConfirmDeleteDialog
                open
                title="Replace this diagram feature?"
                description={`Overwrite ${
                  featureClassesData?.feature_classes.find((c) => c.code === replaceConfirm.stub.feature_class)?.name ??
                  replaceConfirm.stub.feature_class
                } (${replaceConfirm.stub.feature_id.slice(0, 8)}…) with the picked ${
                  featureClassesData?.feature_classes.find((c) => c.code === replaceConfirm.picked.featureClassCode)?.name ??
                  replaceConfirm.picked.featureClassCode
                } (${replaceConfirm.picked.featureId.slice(0, 8)}…). The stub's wiring moves onto the map feature and the stub is retired — this can't be undone.`}
                confirmLabel="Replace"
                onOpenChange={(open) => !open && setReplaceConfirm(null)}
                onConfirm={async () => {
                  const rc = replaceConfirm
                  setReplaceConfirm(null)
                  await executeReplace(rc.stub, rc.picked)
                }}
              />
            )}
            <svg
              ref={attachSvgRef}
              viewBox={viewBox}
              className={cn(
                "h-full w-full",
                (digitizingClass || isPlacingHere || drawingElementLine || armingMode || (connectMode && !connectTarget)) &&
                  "cursor-crosshair",
                canvasMode === "pan" && (panDrag ? "cursor-grabbing" : "cursor-grab"),
              )}
              onClick={handleCanvasClick}
              onDoubleClick={handleCanvasDoubleClick}
              onContextMenu={(e) => e.preventDefault()}
              onPointerDown={handleCanvasPanPointerDown}
              onPointerMove={handleCanvasPointerMove}
              onPointerLeave={() => statusBarRef.current?.set(null)}
              onPointerUp={handleCanvasPanPointerUp}
              onPointerCancel={handleCanvasPanPointerUp}
            >
            {/* Arrowhead markers for control/trip signal lines (Phase 46) -- one
              per signal colour, sized in strokeWidth units so the head scales
              with its line. Referenced by markerEnd on both the hand-drawn
              `control` element and the derived protection lines. Always present
              (unlike the opt-in grid defs), so an arrow never renders headless. */}
            <defs>
              {Object.entries(CONTROL_LINE_COLORS).map(([slug, color]) => (
                <marker
                  key={slug}
                  id={`${arrowMarkerPrefix}-${slug}`}
                  viewBox="0 0 10 10"
                  refX="8"
                  refY="5"
                  markerWidth="5"
                  markerHeight="5"
                  markerUnits="strokeWidth"
                  orient="auto-start-reverse"
                >
                  <path d="M 0 0 L 10 5 L 0 10 z" fill={color} />
                </marker>
              ))}
            </defs>
            {/* Drawing grid (opt-in via the toolbar). Tiled in userSpaceOnUse
              so a cell lands on the same coordinates snapToGrid rounds to, and
              pointerEvents:none so it never intercepts a placement click (which
              relies on e.target being the <svg> itself). */}
            {gridEnabled && (
              <>
                <defs>
                  <pattern id={gridPatternId} width={gridStep} height={gridStep} patternUnits="userSpaceOnUse">
                    <path
                      d={`M ${gridStep} 0 L 0 0 0 ${gridStep}`}
                      fill="none"
                      stroke="var(--border)"
                      strokeWidth={gridStep * 0.03}
                    />
                  </pattern>
                </defs>
                {/* Over-cover: the SVG keeps its default preserveAspectRatio
                  (xMidYMid meet), so a canvas whose aspect ratio differs from
                  the viewBox (notably full-page/maximized) letterboxes the
                  viewBox and shows userSpace well beyond view.w/h in the
                  margins. Painting the grid over a big margin around `view`
                  fills those margins for any aspect ratio; a <pattern> fill is
                  a single tiled paint the browser clips to the viewport, so the
                  extra area is effectively free. */}
                <rect
                  x={view.x - view.w * 3}
                  y={view.y - view.h * 3}
                  width={view.w * 7}
                  height={view.h * 7}
                  fill={`url(#${gridPatternId})`}
                  style={{ pointerEvents: "none" }}
                />
              </>
            )}
            {placements.length === 0 && elements.length === 0 && (
              <text x={bounds.minX + (bounds.maxX - bounds.minX) / 2} y={bounds.minY + (bounds.maxY - bounds.minY) / 2} textAnchor="middle" style={{ fill: "var(--muted-foreground)", fontSize }}>
                Nothing placed on this diagram yet.
              </text>
            )}
            {/* Non-feature elements (Phase 24 keystone) render first, so they
              sit behind the placements/conductors -- a grouping box frames the
              devices inside it rather than covering them. Backed by nothing:
              draggable, and (box) resizable, (text) editable in place. */}
            {effectiveElements.map((element) => {
              const style = element.style ?? {}
              const dragging = elementDrag?.id === element.id
              const dashLen = Math.max(fontSize * 0.8, 4)
              const dragCursor = canvasMode === "pan" ? "" : dragging ? "cursor-grabbing" : "cursor-move"

              if (element.kind === "box") {
                const w = element.width ?? 0
                const h = element.height ?? 0
                const stroke = (style.stroke as string) ?? "var(--muted-foreground)"
                const fill = (style.fill as string) ?? "none"
                return (
                  <g key={element.id} className={cn("group touch-none", dragCursor)}>
                    <rect
                      x={element.x}
                      y={element.y}
                      width={w}
                      height={h}
                      fill={fill}
                      pointerEvents="all"
                      style={{ stroke }}
                      strokeWidth={Math.max(fontSize * 0.4, 1.5)}
                      strokeDasharray={style.dash ? `${dashLen} ${dashLen}` : undefined}
                      onPointerDown={(e) => handleElementPointerDown(e, element)}
                      onPointerMove={handleElementPointerMove}
                      onPointerUp={handleElementPointerUp}
                      onPointerCancel={() => setElementDrag(null)}
                    />
                    {element.text && (
                      <text x={element.x + w / 2} y={element.y - fontSize * 0.5} textAnchor="middle" style={{ fill: stroke, fontSize }}>
                        {element.text}
                      </text>
                    )}
                    <circle
                      cx={element.x + w}
                      cy={element.y + h}
                      r={Math.max(fontSize * 0.5, 3)}
                      className="cursor-nwse-resize opacity-0 transition-opacity group-hover:opacity-100"
                      style={{ fill: "var(--background)", stroke: "var(--ring)" }}
                      strokeWidth={1.5}
                      onPointerDown={(e) => handleElementResizePointerDown(e, element)}
                      onPointerMove={handleElementResizePointerMove}
                      onPointerUp={handleElementResizePointerUp}
                      onPointerCancel={() => setElementResize(null)}
                    />
                    <RemoveControl
                      x={element.x + w}
                      y={element.y}
                      size={removeSize}
                      onRemove={() => setPendingElementRemoval({ id: element.id, label: element.text || "box" })}
                    />
                  </g>
                )
              }

              if (element.kind === "line") {
                const pts = element.path ?? []
                if (pts.length < 2) return null
                const stroke = (style.stroke as string) ?? "var(--foreground)"
                const mid = polylineMidpoint(pts)
                // Promotable (Phase 25) when it carries a promoteClass: double-
                // click turns the drawn line into a real LINE feature keeping
                // its whole length (e.g. a template riser -> MV_CONDUCTOR).
                const promoteClass = typeof style.promoteClass === "string" ? style.promoteClass : undefined
                const promoteClassName = promoteClass ? (classNameByCode[promoteClass] ?? promoteClass) : undefined
                return (
                  <g key={element.id} className={cn("group touch-none", dragCursor)}>
                    <path
                      d={polylinePath(pts)}
                      fill="none"
                      pointerEvents="stroke"
                      style={{ stroke }}
                      strokeWidth={Math.max(fontSize * 0.5, 2)}
                      strokeDasharray={style.dash ? `${dashLen} ${dashLen}` : undefined}
                      onPointerDown={(e) => handleElementPointerDown(e, element)}
                      onPointerMove={handleElementPointerMove}
                      onPointerUp={handleElementPointerUp}
                      onPointerCancel={() => setElementDrag(null)}
                      onDoubleClick={(e) => {
                        e.preventDefault()
                        e.stopPropagation()
                        promoteElement(element)
                      }}
                    >
                      {promoteClassName ? (
                        <title>Double-click to promote to a {promoteClassName} feature</title>
                      ) : canPromote ? (
                        <title>Double-click to promote to a feature</title>
                      ) : null}
                    </path>
                    <RemoveControl
                      x={mid.x + removeSize * 2}
                      y={mid.y - removeSize * 2}
                      size={removeSize}
                      onRemove={() => setPendingElementRemoval({ id: element.id, label: element.text || "line" })}
                    />
                  </g>
                )
              }

              if (element.kind === "control") {
                // A hand-drawn control/trip signal line (Phase 46 Slice 1):
                // the same stored polyline as a line, but always rendered dashed
                // with an arrowhead at its last vertex (the target end), in the
                // signal colour, so it never reads as a power wire. Draggable as
                // one piece, hover-"x" to remove -- exactly like a line.
                const pts = element.path ?? []
                if (pts.length < 2) return null
                const stroke = (style.stroke as string) ?? CONTROL_LINE_COLORS.control
                const mid = polylineMidpoint(pts)
                return (
                  <g key={element.id} className={cn("group touch-none", dragCursor)}>
                    <path
                      d={polylinePath(pts)}
                      fill="none"
                      pointerEvents="stroke"
                      style={{ stroke }}
                      strokeWidth={Math.max(fontSize * 0.5, 2)}
                      strokeDasharray={`${dashLen} ${dashLen}`}
                      markerEnd={`url(#${arrowMarkerPrefix}-control)`}
                      onPointerDown={(e) => handleElementPointerDown(e, element)}
                      onPointerMove={handleElementPointerMove}
                      onPointerUp={handleElementPointerUp}
                      onPointerCancel={() => setElementDrag(null)}
                    />
                    {element.text && (
                      <text x={mid.x} y={mid.y - fontSize * 0.5} textAnchor="middle" style={{ fill: stroke, fontSize }}>
                        {element.text}
                      </text>
                    )}
                    <RemoveControl
                      x={mid.x + removeSize * 2}
                      y={mid.y - removeSize * 2}
                      size={removeSize}
                      onRemove={() => setPendingElementRemoval({ id: element.id, label: element.text || "control line" })}
                    />
                  </g>
                )
              }

              if (element.kind === "bus") {
                // A busbar (Slice 5): the same stored polyline as a line, drawn
                // heavy with rounded joins so a shared bus reads as the spine
                // devices tap onto. Draggable as one piece, hover-"x" to remove.
                // Promotable (Phase 25) when it carries a promoteClass: double-
                // click turns the drawn bar into a real LINE feature keeping its
                // whole length (e.g. a template bus -> MV_BUSBAR).
                const pts = element.path ?? []
                if (pts.length < 2) return null
                const stroke = (style.stroke as string) ?? "var(--foreground)"
                const mid = polylineMidpoint(pts)
                const promoteClass = typeof style.promoteClass === "string" ? style.promoteClass : undefined
                const promoteClassName = promoteClass ? (classNameByCode[promoteClass] ?? promoteClass) : undefined
                return (
                  <g key={element.id} className={cn("group touch-none", dragCursor)}>
                    <path
                      d={polylinePath(pts)}
                      fill="none"
                      pointerEvents="stroke"
                      strokeLinecap="round"
                      strokeLinejoin="round"
                      style={{ stroke }}
                      strokeWidth={Math.max(fontSize * 1.4, 5)}
                      onPointerDown={(e) => handleElementPointerDown(e, element)}
                      onPointerMove={handleElementPointerMove}
                      onPointerUp={handleElementPointerUp}
                      onPointerCancel={() => setElementDrag(null)}
                      onDoubleClick={(e) => {
                        e.preventDefault()
                        e.stopPropagation()
                        promoteElement(element)
                      }}
                    >
                      {promoteClassName ? (
                        <title>Double-click to promote to a {promoteClassName} feature</title>
                      ) : canPromote ? (
                        <title>Double-click to promote to a feature</title>
                      ) : null}
                    </path>
                    <RemoveControl
                      x={mid.x + removeSize * 2}
                      y={mid.y - removeSize * 2}
                      size={removeSize}
                      onRemove={() => setPendingElementRemoval({ id: element.id, label: element.text || "bus" })}
                    />
                  </g>
                )
              }

              if (element.kind === "tap") {
                // A tap (Slice 5): a thin right-angle stub off a bus, drawn from
                // its device end to its bus end. A filled dot at the last vertex
                // (the bus end) is the T-junction where it meets the busbar.
                // Draggable as one piece, hover-"x" to remove.
                const pts = element.path ?? []
                if (pts.length < 2) return null
                const stroke = (style.stroke as string) ?? "var(--foreground)"
                const mid = polylineMidpoint(pts)
                const junction = pts[pts.length - 1]
                return (
                  <g key={element.id} className={cn("group touch-none", dragCursor)}>
                    <path
                      d={polylinePath(pts)}
                      fill="none"
                      pointerEvents="stroke"
                      strokeLinecap="round"
                      strokeLinejoin="round"
                      style={{ stroke }}
                      strokeWidth={Math.max(fontSize * 0.5, 2)}
                      onPointerDown={(e) => handleElementPointerDown(e, element)}
                      onPointerMove={handleElementPointerMove}
                      onPointerUp={handleElementPointerUp}
                      onPointerCancel={() => setElementDrag(null)}
                    />
                    <circle
                      cx={junction[0]}
                      cy={junction[1]}
                      r={Math.max(fontSize * 0.45, 2.5)}
                      style={{ fill: stroke }}
                      pointerEvents="none"
                    />
                    <RemoveControl
                      x={mid.x + removeSize * 2}
                      y={mid.y - removeSize * 2}
                      size={removeSize}
                      onRemove={() => setPendingElementRemoval({ id: element.id, label: element.text || "tap" })}
                    />
                  </g>
                )
              }

              if (element.kind === "symbol") {
                // A stencil glyph (Phase 24 Slice 2): rendered exactly like a
                // placement's feature icon -- translate to its centre (x/y),
                // rotate, draw the icon centred -- but backed by no feature.
                // Draggable to move, hover-revealed handle to rotate (snapped),
                // hover-"x" to remove.
                const iconRef = typeof style.iconRef === "string" ? style.iconRef : undefined
                const glyphColor = typeof style.color === "string" ? style.color : "#334155"
                const glyph = iconDataUri(iconRef, glyphColor, symbologyIcons)
                const rotating = elementRotate?.id === element.id
                // Promotable (Phase 25) when its stencil maps to a feature
                // class: double-click turns the glyph into a real feature, and
                // a native <title> tooltip advertises it.
                const promoteClass = iconRef ? promoteClassByRef[iconRef] : undefined
                const promoteClassName = promoteClass ? (classNameByCode[promoteClass] ?? promoteClass) : undefined
                return (
                  <g
                    key={element.id}
                    transform={`translate(${element.x}, ${element.y})`}
                    className={cn("group touch-none select-none", dragCursor)}
                    onPointerDown={(e) => handleElementPointerDown(e, element)}
                    onPointerMove={handleElementPointerMove}
                    onPointerUp={handleElementPointerUp}
                    onPointerCancel={() => setElementDrag(null)}
                    onDoubleClick={(e) => {
                      e.preventDefault()
                      e.stopPropagation()
                      promoteElement(element)
                    }}
                  >
                    {promoteClassName ? (
                      <title>Double-click to promote to a {promoteClassName} feature</title>
                    ) : canPromote ? (
                      <title>Double-click to promote to a feature</title>
                    ) : null}
                    <g transform={`rotate(${element.rotation})`}>
                      <image href={glyph} x={-iconSize / 2} y={-iconSize / 2} width={iconSize} height={iconSize} />
                    </g>
                    <line
                      x1={0}
                      y1={-iconSize / 2}
                      x2={0}
                      y2={-iconSize / 2 - fontSize * 1.2}
                      className={cn("opacity-0 transition-opacity group-hover:opacity-100", rotating && "opacity-100")}
                      style={{ stroke: "var(--ring)" }}
                      strokeWidth={1}
                    />
                    <g
                      transform={`translate(0, ${-iconSize / 2 - fontSize * 1.2})`}
                      className={cn(
                        "cursor-grab touch-none opacity-0 transition-opacity group-hover:opacity-100",
                        rotating && "opacity-100",
                      )}
                      onPointerDown={(e) => handleElementRotatePointerDown(e, element)}
                      onPointerMove={handleElementRotatePointerMove}
                      onPointerUp={handleElementRotatePointerUp}
                      onPointerCancel={() => setElementRotate(null)}
                    >
                      <circle r={Math.max(iconSize * 0.15, 3)} style={{ fill: "var(--background)", stroke: "var(--ring)" }} strokeWidth={1.5} />
                    </g>
                    <RemoveControl
                      x={iconSize * 0.45}
                      y={-iconSize * 0.45}
                      size={removeSize}
                      onRemove={() => setPendingElementRemoval({ id: element.id, label: iconRef || "symbol" })}
                    />
                  </g>
                )
              }

              if (element.kind === "title_block") {
                // A bordered key/value card (Slice 3). Rows come from
                // style.fields; each value is double-click editable in place.
                // x/y is the top-left; draggable by the border/background,
                // corner-resizable, hover-"x" to remove.
                const w = element.width ?? 0
                const h = element.height ?? 0
                const stroke = (style.stroke as string) ?? "var(--foreground)"
                const fields = Array.isArray(style.fields) ? (style.fields as { label: string; value: string }[]) : []
                const rowH = fields.length > 0 ? h / fields.length : h
                const labelColW = w * 0.42
                const labelSize = fontSize * 0.72
                const valueSize = fontSize * 0.9
                const border = Math.max(fontSize * 0.35, 1.25)
                return (
                  <g key={element.id} className={cn("group touch-none", dragCursor)}>
                    <rect
                      x={element.x}
                      y={element.y}
                      width={w}
                      height={h}
                      fill="var(--background)"
                      pointerEvents="all"
                      style={{ stroke }}
                      strokeWidth={border}
                      onPointerDown={(e) => handleElementPointerDown(e, element)}
                      onPointerMove={handleElementPointerMove}
                      onPointerUp={handleElementPointerUp}
                      onPointerCancel={() => setElementDrag(null)}
                    />
                    {fields.map((f, i) => {
                      const ry = element.y + i * rowH
                      const editing = editingField?.id === element.id && editingField.index === i
                      return (
                        <g key={i}>
                          {i > 0 && (
                            <line x1={element.x} y1={ry} x2={element.x + w} y2={ry} style={{ stroke }} strokeWidth={border * 0.6} />
                          )}
                          <line
                            x1={element.x + labelColW}
                            y1={ry}
                            x2={element.x + labelColW}
                            y2={ry + rowH}
                            style={{ stroke }}
                            strokeWidth={border * 0.6}
                          />
                          <text
                            x={element.x + w * 0.03}
                            y={ry + rowH * 0.62}
                            style={{ fill: "var(--muted-foreground)", fontSize: labelSize }}
                          >
                            {f.label}
                          </text>
                          {editing ? (
                            <foreignObject
                              x={element.x + labelColW + w * 0.02}
                              y={ry + rowH * 0.15}
                              width={Math.max(w - labelColW - w * 0.04, 20)}
                              height={rowH * 0.72}
                            >
                              <input
                                autoFocus
                                value={editingField.value}
                                onChange={(e) => setEditingField({ id: element.id, index: i, value: e.target.value })}
                                onBlur={commitFieldEdit}
                                onKeyDown={(e) => {
                                  if (e.key === "Enter") e.currentTarget.blur()
                                  if (e.key === "Escape") setEditingField(null)
                                }}
                                style={{
                                  width: "100%",
                                  fontSize: valueSize,
                                  color: "var(--foreground)",
                                  background: "var(--background)",
                                  border: "1px solid var(--ring)",
                                  borderRadius: 2,
                                  padding: "0 2px",
                                  outline: "none",
                                }}
                              />
                            </foreignObject>
                          ) : (
                            <text
                              x={element.x + labelColW + w * 0.03}
                              y={ry + rowH * 0.62}
                              style={{ fill: f.value ? "var(--foreground)" : "var(--muted-foreground)", fontSize: valueSize }}
                              onDoubleClick={(e) => {
                                e.stopPropagation()
                                setEditingField({ id: element.id, index: i, value: f.value })
                              }}
                            >
                              {f.value || "—"}
                            </text>
                          )}
                        </g>
                      )
                    })}
                    <circle
                      cx={element.x + w}
                      cy={element.y + h}
                      r={Math.max(fontSize * 0.5, 3)}
                      className="cursor-nwse-resize opacity-0 transition-opacity group-hover:opacity-100"
                      style={{ fill: "var(--background)", stroke: "var(--ring)" }}
                      strokeWidth={1.5}
                      onPointerDown={(e) => handleElementResizePointerDown(e, element)}
                      onPointerMove={handleElementResizePointerMove}
                      onPointerUp={handleElementResizePointerUp}
                      onPointerCancel={() => setElementResize(null)}
                    />
                    <RemoveControl
                      x={element.x + w}
                      y={element.y}
                      size={removeSize}
                      onRemove={() => setPendingElementRemoval({ id: element.id, label: "title block" })}
                    />
                  </g>
                )
              }

              if (element.kind === "legend") {
                // A bordered key -> label card (Slice 3), auto-derived from
                // what's on the sheet (legendEntries): one row per distinct
                // non-line feature class, then per distinct stamped stencil.
                // Only its heading is stored; draggable/resizable like a box.
                const w = element.width ?? 0
                const h = element.height ?? 0
                const stroke = (style.stroke as string) ?? "var(--foreground)"
                const title = typeof style.title === "string" ? style.title : "Legend"
                const border = Math.max(fontSize * 0.35, 1.25)
                const headerH = Math.min(fontSize * 1.8, h * 0.3)
                const rowH = fontSize * 1.5
                const maxRows = Math.max(Math.floor((h - headerH) / rowH), 0)
                const rows = legendEntries.slice(0, maxRows)
                const overflow = legendEntries.length - rows.length
                const rowIcon = Math.min(rowH * 0.7, w * 0.16)
                return (
                  <g key={element.id} className={cn("group touch-none", dragCursor)}>
                    <rect
                      x={element.x}
                      y={element.y}
                      width={w}
                      height={h}
                      fill="var(--background)"
                      pointerEvents="all"
                      style={{ stroke }}
                      strokeWidth={border}
                      onPointerDown={(e) => handleElementPointerDown(e, element)}
                      onPointerMove={handleElementPointerMove}
                      onPointerUp={handleElementPointerUp}
                      onPointerCancel={() => setElementDrag(null)}
                    />
                    <line
                      x1={element.x}
                      y1={element.y + headerH}
                      x2={element.x + w}
                      y2={element.y + headerH}
                      style={{ stroke }}
                      strokeWidth={border * 0.6}
                    />
                    <text
                      x={element.x + w * 0.05}
                      y={element.y + headerH * 0.66}
                      style={{ fill: stroke, fontSize: fontSize * 0.95, fontWeight: 600 }}
                    >
                      {title}
                    </text>
                    {rows.length === 0 ? (
                      <text
                        x={element.x + w * 0.05}
                        y={element.y + headerH + rowH * 0.6}
                        style={{ fill: "var(--muted-foreground)", fontSize: fontSize * 0.8 }}
                      >
                        Nothing on the sheet yet.
                      </text>
                    ) : (
                      rows.map((entry, i) => {
                        const ry = element.y + headerH + i * rowH
                        return (
                          <g key={entry.key}>
                            <image
                              href={entry.icon}
                              x={element.x + w * 0.05}
                              y={ry + (rowH - rowIcon) / 2}
                              width={rowIcon}
                              height={rowIcon}
                            />
                            <text
                              x={element.x + w * 0.05 + rowIcon + w * 0.04}
                              y={ry + rowH * 0.62}
                              style={{ fill: "var(--foreground)", fontSize: fontSize * 0.8 }}
                            >
                              {entry.label}
                            </text>
                          </g>
                        )
                      })
                    )}
                    {overflow > 0 && (
                      <text
                        x={element.x + w * 0.05}
                        y={element.y + headerH + maxRows * rowH + fontSize * 0.7}
                        style={{ fill: "var(--muted-foreground)", fontSize: fontSize * 0.72 }}
                      >
                        +{overflow} more
                      </text>
                    )}
                    <circle
                      cx={element.x + w}
                      cy={element.y + h}
                      r={Math.max(fontSize * 0.5, 3)}
                      className="cursor-nwse-resize opacity-0 transition-opacity group-hover:opacity-100"
                      style={{ fill: "var(--background)", stroke: "var(--ring)" }}
                      strokeWidth={1.5}
                      onPointerDown={(e) => handleElementResizePointerDown(e, element)}
                      onPointerMove={handleElementResizePointerMove}
                      onPointerUp={handleElementResizePointerUp}
                      onPointerCancel={() => setElementResize(null)}
                    />
                    <RemoveControl
                      x={element.x + w}
                      y={element.y}
                      size={removeSize}
                      onRemove={() => setPendingElementRemoval({ id: element.id, label: "legend" })}
                    />
                  </g>
                )
              }

              if (element.kind === "slot") {
                // An unbound template slot (Slice 4): a dashed placeholder
                // marker at the slot's centre, tagged with the class it expects.
                // Clicking it opens the class-scoped picker (setSlotBinding);
                // binding materialises a real placement here and removes this.
                // x/y is the centre (slots are minted from placement positions),
                // so it renders translate-to-centre like a symbol/node.
                const label = element.slot_class ?? "slot"
                const r = iconSize * 0.5
                return (
                  <g
                    key={element.id}
                    transform={`translate(${element.x}, ${element.y})`}
                    className={cn("group touch-none", canvasMode === "pan" ? "" : "cursor-pointer")}
                    onClick={(e) => {
                      if (canvasMode === "pan") return
                      e.stopPropagation()
                      setSlotBinding(element)
                    }}
                  >
                    <circle
                      r={r}
                      fill="var(--background)"
                      pointerEvents="all"
                      style={{ stroke: "var(--muted-foreground)" }}
                      strokeWidth={Math.max(fontSize * 0.35, 1.25)}
                      strokeDasharray={`${dashLen} ${dashLen}`}
                      className="transition-colors group-hover:stroke-[var(--ring)]"
                    />
                    <text
                      x={0}
                      y={fontSize * 0.4}
                      textAnchor="middle"
                      style={{ fill: "var(--muted-foreground)", fontSize: fontSize * 1.3 }}
                    >
                      +
                    </text>
                    <text
                      x={0}
                      y={r + fontSize * 1.1}
                      textAnchor="middle"
                      style={{ fill: "var(--muted-foreground)", fontSize: fontSize * 0.8 }}
                    >
                      {label}
                    </text>
                    <RemoveControl
                      x={r * 0.9}
                      y={-r * 0.9}
                      size={removeSize}
                      onRemove={() => setPendingElementRemoval({ id: element.id, label: `${label} slot` })}
                    />
                  </g>
                )
              }

              // text (and, for now, any other kind falls through to a label)
              const color = (style.color as string) ?? "var(--foreground)"
              const size = typeof style.fontSize === "number" ? style.fontSize : fontSize * 1.2
              if (editingText?.id === element.id) {
                return (
                  <foreignObject key={element.id} x={element.x} y={element.y - size} width={Math.max(size * 12, 120)} height={size * 2}>
                    <input
                      autoFocus
                      value={editingText.value}
                      onChange={(e) => setEditingText({ id: element.id, value: e.target.value })}
                      onBlur={commitTextEdit}
                      onKeyDown={(e) => {
                        if (e.key === "Enter") e.currentTarget.blur()
                        if (e.key === "Escape") setEditingText(null)
                      }}
                      style={{
                        width: "100%",
                        fontSize: size,
                        color: "var(--foreground)",
                        background: "var(--background)",
                        border: "1px solid var(--ring)",
                        borderRadius: 2,
                        padding: "0 2px",
                        outline: "none",
                      }}
                    />
                  </foreignObject>
                )
              }
              return (
                <g key={element.id} className={cn("group touch-none", dragCursor)}>
                  <text
                    x={element.x}
                    y={element.y}
                    style={{ fill: color, fontSize: size }}
                    onPointerDown={(e) => handleElementPointerDown(e, element)}
                    onPointerMove={handleElementPointerMove}
                    onPointerUp={handleElementPointerUp}
                    onPointerCancel={() => setElementDrag(null)}
                    onDoubleClick={(e) => {
                      e.stopPropagation()
                      if (editMode) setEditingText({ id: element.id, value: element.text ?? "" })
                    }}
                  >
                    {element.text || "Label"}
                  </text>
                  <RemoveControl
                    x={element.x + size * 0.6}
                    y={element.y - size}
                    size={removeSize}
                    onRemove={() => setPendingElementRemoval({ id: element.id, label: element.text || "text" })}
                  />
                </g>
              )
            })}
            {/* Derived control/trip signal lines (Phase 46 Slice 2) --
                protection_trip / protection_sensing relationships between two
                placed features, drawn behind the feature layer as dashed,
                arrowheaded elbows in the signal colour (origin -> destination).
                Display-only: revoke the relationship in the feature panel to
                remove one, exactly as a conductor is reshaped by its endpoints
                rather than edited on the canvas. */}
            {controlLineItems.length > 0 && (
              <g style={{ pointerEvents: "none" }}>
                {controlLineItems.map((line) => {
                  const slug = CONTROL_LINE_COLORS[line.kind] ? line.kind : "control"
                  const color = CONTROL_LINE_COLORS[slug]
                  const d = elbowPath(line.from.x, line.from.y, line.to.x, line.to.y)
                  const mid = { x: (line.from.x + line.to.x) / 2, y: (line.from.y + line.to.y) / 2 }
                  const dash = Math.max(fontSize * 0.8, 4)
                  return (
                    <g key={line.id}>
                      <path
                        d={d}
                        fill="none"
                        style={{ stroke: color }}
                        strokeWidth={Math.max(fontSize * 0.45, 1.5)}
                        strokeDasharray={`${dash} ${dash}`}
                        markerEnd={`url(#${arrowMarkerPrefix}-${slug})`}
                      />
                      <text
                        x={mid.x}
                        y={mid.y - fontSize * 0.4}
                        textAnchor="middle"
                        style={{ fill: color, fontSize: fontSize * 0.85 }}
                      >
                        {line.name}
                      </text>
                    </g>
                  )
                })}
              </g>
            )}
            {/* The feature layer (conductors + points). Made pointer-
                transparent while drawing/placing so a click over an existing
                feature reaches the canvas and drops a vertex, rather than
                selecting that feature (see drawingActive). Restored to
                interactive otherwise, and always interactive in connect/pick
                mode since that flow needs to click peers. */}
            <g style={drawingActive ? { pointerEvents: "none" } : undefined}>
            {conductorItems.map((item) => {
              const feature = featureResults[item.index]?.data
              const classRules = rules[item.placement.feature_class]
              const def =
                resolveSymbology(classRules, feature?.subtype, feature?.attributes ?? {}, "diagram") ??
                resolveSymbology(classRules, feature?.subtype, feature?.attributes ?? {}, "map")
              const stroke = def?.color ?? "var(--muted-foreground)"
              const [p1, p2] = item.points
              // A hand-drawn line (Slice 5) renders from its stored `path`;
              // absent one, fall back to today's connectivity-derived elbow
              // between the two endpoints.
              const drawnPath =
                item.placement.path && item.placement.path.length >= 2 ? item.placement.path : null
              const hasGeometry = drawnPath !== null || item.fullyConnected
              // A line with its OWN drawn path can be dragged to move the whole
              // bar (handlePlacementPointerDown carries the path and translates
              // it). A fully-connected conductor with no path of its own is
              // shaped entirely by its two endpoint placements, so there's
              // nothing here to move -- it stays select-only, and you reposition
              // it by moving those endpoints.
              // View mode (S3): nothing is draggable, so the cursor reads as
              // select-only and handlePlacementPointerDown's guard never fires.
              const draggable = editMode && drawnPath !== null
              const selectOnly = hasGeometry && !draggable
              const d = drawnPath ? polylinePath(drawnPath) : elbowPath(p1.x, p1.y, p2.x, p2.y)
              const mid = drawnPath ? polylineMidpoint(drawnPath) : { x: (p1.x + p2.x) / 2, y: (p1.y + p2.y) / 2 }
              const dragging = dragState?.placementId === item.placement.id
              const role = roleFor(item.placement.feature_class, item.placement.feature_id)
              const isSelected = role === "selected"
              return (
                <g key={item.placement.id}>
                  {role && (
                    <path
                      d={d}
                      fill="none"
                      style={{ stroke: HIGHLIGHT_COLORS[role], opacity: 0.4 }}
                      strokeWidth={Math.max(fontSize * 1.1, 5)}
                    />
                  )}
                  <path d={d} fill="none" style={{ stroke }} strokeWidth={Math.max(fontSize * 0.5, 2)} />
                  {/* A transparent, wide hit-stroke over the same path makes the
                      whole bar/wire interactive, not just its tiny midpoint
                      label. For a line with its own path it starts a drag that
                      moves the whole bar (a click with no movement still falls
                      through to select/pick-target, via handlePlacementPointerUp);
                      for a fully-connected, pathless conductor there's nothing to
                      move, so it's select/pick only. pointerEvents:"all" makes a
                      transparent stroke interactive, the same trick the loose-end
                      circle uses. */}
                  {hasGeometry && (
                    <path
                      d={d}
                      fill="none"
                      className={draggable ? (dragging ? "cursor-grabbing" : "cursor-grab") : "cursor-pointer"}
                      style={{ stroke: "transparent", pointerEvents: "all" }}
                      strokeWidth={Math.max(fontSize * 1.6, 12)}
                      onPointerDown={draggable ? (e) => handlePlacementPointerDown(e, item.placement) : (e) => e.stopPropagation()}
                      onPointerMove={draggable ? handlePlacementPointerMove : undefined}
                      onPointerUp={
                        draggable
                          ? handlePlacementPointerUp
                          : () => selectOrPickTarget(item.placement.feature_class, item.placement.feature_id)
                      }
                      onPointerCancel={() => setDragState(null)}
                    />
                  )}
                  <g
                    transform={`translate(${mid.x}, ${mid.y})`}
                    className={cn(
                      "group touch-none",
                      selectOnly ? "cursor-pointer" : dragging ? "cursor-grabbing" : "cursor-grab",
                    )}
                    onPointerDown={selectOnly ? undefined : (e) => handlePlacementPointerDown(e, item.placement)}
                    onPointerMove={selectOnly ? undefined : handlePlacementPointerMove}
                    onPointerUp={
                      selectOnly
                        ? () => selectOrPickTarget(item.placement.feature_class, item.placement.feature_id)
                        : handlePlacementPointerUp
                    }
                    onPointerCancel={() => setDragState(null)}
                  >
                    {/* A loose end (no geometry of its own yet) still needs a
                      drag/click target at this midpoint, just not a
                      visible dot -- fill stays transparent, pointerEvents
                      keeps it interactive despite that. */}
                    {!hasGeometry && <circle r={iconSize * 0.22} style={{ fill: "transparent", pointerEvents: "all" }} />}
                    {!item.placement.label_hidden &&
                      (() => {
                        // A conductor label has no glyph rotation to follow, so
                        // label_dx/dy nudge straight from the natural midpoint
                        // anchor (migration 0056). Draggable only while selected.
                        const ldrag = labelDrag?.placementId === item.placement.id ? labelDrag : null
                        const ldx = ldrag ? ldrag.dx : item.placement.label_dx ?? 0
                        const ldy = ldrag ? ldrag.dy : item.placement.label_dy ?? 0
                        const lrot = item.placement.label_rotation ?? 0
                        const lscale = item.placement.label_scale ?? 1
                        const anchorY = hasGeometry ? -fontSize * 0.6 : iconSize * 0.3 + fontSize * 1.1
                        return (
                          <g transform={`translate(${ldx}, ${anchorY + ldy}) rotate(${lrot})`}>
                            <text
                              textAnchor="middle"
                              className={
                                isSelected ? (ldrag ? "cursor-grabbing touch-none" : "cursor-grab touch-none") : undefined
                              }
                              style={{
                                fill: "var(--foreground)",
                                fontSize: fontSize * lscale,
                                pointerEvents: isSelected ? "all" : "none",
                              }}
                              onPointerDown={isSelected ? (e) => handleLabelPointerDown(e, item.placement, 0) : undefined}
                              onPointerMove={isSelected ? handleLabelPointerMove : undefined}
                              onPointerUp={isSelected ? handleLabelPointerUp : undefined}
                              onPointerCancel={() => setLabelDrag(null)}
                            >
                              {feature?.name || item.placement.feature_class}
                            </text>
                          </g>
                        )
                      })()}
                    <LabelToggleControl
                      x={-removeSize * 2.2}
                      y={-removeSize * 2.2}
                      size={removeSize}
                      hidden={!!item.placement.label_hidden}
                      onToggle={() => void toggleLabelHidden(item.placement)}
                    />
                    <RemoveControl
                      x={removeSize * 2.2}
                      y={-removeSize * 2.2}
                      size={removeSize}
                      onRemove={() => setPendingRemoval({ id: item.placement.id, label: feature?.name || item.placement.feature_class })}
                    />
                    {/* Label nudge/rotate/resize controls moved to the header
                        (LabelAdjustBar) so they no longer float over the feature. */}
                  </g>
                  {/* Per-vertex reshape handles (small dots at each vertex),
                      shown only while a hand-drawn bar is selected -- so the
                      diagram stays clean until you're actually working on this
                      line. Drag one to bend/lengthen the bar; stopPropagation on
                      pointerdown keeps it from also starting the whole-bar drag
                      beneath. Mirrors the point feature's rotate-handle pattern
                      (visible only on selection). */}
                  {draggable && role === "selected" && (
                    <>
                      {/* Add-vertex handles: a small "+" at each segment
                          midpoint. Click to insert a vertex there (then drag
                          it), so a straight run can be bent. Drawn under the
                          vertex handles so an existing vertex always wins the
                          pointer where they'd overlap. */}
                      {drawnPath!.slice(0, -1).map(([ax, ay], si) => {
                        const [bx, by] = drawnPath![si + 1]
                        const mx = (ax + bx) / 2
                        const my = (ay + by) / 2
                        const r = Math.max(fontSize * 0.4, 2.5)
                        return (
                          <g
                            key={`add-${si}`}
                            className="cursor-pointer opacity-60 transition-opacity hover:opacity-100"
                            onPointerDown={(e) => e.stopPropagation()}
                            onClick={(e) => {
                              e.stopPropagation()
                              insertVertex(item.placement, si)
                            }}
                          >
                            <title>Add vertex</title>
                            <circle cx={mx} cy={my} r={r} style={{ fill: "var(--ring)", pointerEvents: "all" }} />
                            <line x1={mx - r * 0.5} y1={my} x2={mx + r * 0.5} y2={my} style={{ stroke: "var(--background)" }} strokeWidth={r * 0.32} strokeLinecap="round" />
                            <line x1={mx} y1={my - r * 0.5} x2={mx} y2={my + r * 0.5} style={{ stroke: "var(--background)" }} strokeWidth={r * 0.32} strokeLinecap="round" />
                          </g>
                        )
                      })}
                      {/* Move handles: drag to reshape; double-click to remove
                          (kept at two points minimum). */}
                      {drawnPath!.map(([vx, vy], vi) => {
                        const vertexDragging = vertexDrag?.placementId === item.placement.id && vertexDrag.index === vi
                        const removable = drawnPath!.length > 2
                        return (
                          <g
                            key={vi}
                            onDoubleClick={
                              removable
                                ? (e) => {
                                    e.stopPropagation()
                                    removeVertex(item.placement, vi)
                                  }
                                : undefined
                            }
                          >
                            <title>{removable ? "Drag to move · double-click to remove" : "Drag to move"}</title>
                            <circle
                              cx={vx}
                              cy={vy}
                              r={Math.max(fontSize * 0.45, 3)}
                              className={vertexDragging ? "cursor-grabbing touch-none" : "cursor-grab touch-none"}
                              style={{ fill: "var(--background)", stroke: "var(--ring)", pointerEvents: "all" }}
                              strokeWidth={1.5}
                              onPointerDown={(e) => handleVertexPointerDown(e, item.placement, vi)}
                              onPointerMove={handleVertexPointerMove}
                              onPointerUp={handleVertexPointerUp}
                              onPointerCancel={() => setVertexDrag(null)}
                            />
                          </g>
                        )
                      })}
                    </>
                  )}
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
                  {(() => {
                    // Phase 48 Slice 1 -- normally-open-point marker. Only the
                    // OPEN points are badged (see the NORMAL_OPEN_POINT_COLOR
                    // note): a bold amber ring, drawn OUTSIDE the glyph's
                    // rotate() so it stays corner-pinned however the glyph turns.
                    // The hollow centre reads as "open"; a normally-closed or
                    // field-less switch is left unmarked -- the assumed default,
                    // so the diagram isn't drowned in dots.
                    if (declaredSwitchState(feature) !== "open") return null
                    const br = Math.max(fontSize * 0.62, iconSize * 0.18)
                    return (
                      <circle
                        cx={iconSize * 0.42}
                        cy={-iconSize * 0.42}
                        r={br}
                        style={{ fill: "var(--background)", stroke: NORMAL_OPEN_POINT_COLOR }}
                        strokeWidth={Math.max(br * 0.5, 1.5)}
                      >
                        <title>Normally open</title>
                      </circle>
                    )
                  })()}
                  {!placement.label_hidden &&
                    (() => {
                      // Label rides inside the glyph's rotate() so its offset
                      // follows the glyph (migration 0056). Natural anchor =
                      // below the icon; label_dx/dy nudge from there, in the
                      // glyph-local frame; label_rotation/scale style the text.
                      // Draggable only while selected, so it never steals a
                      // plain click on an unselected feature.
                      const ldrag = labelDrag?.placementId === placement.id ? labelDrag : null
                      const ldx = ldrag ? ldrag.dx : placement.label_dx ?? 0
                      const ldy = ldrag ? ldrag.dy : placement.label_dy ?? 0
                      const lrot = placement.label_rotation ?? 0
                      const lscale = placement.label_scale ?? 1
                      const anchorY = iconSize / 2 + fontSize * 1.4
                      return (
                        <g transform={`rotate(${placement.rotation})`}>
                          <g transform={`translate(${ldx}, ${anchorY + ldy}) rotate(${lrot})`}>
                            <text
                              x={0}
                              y={0}
                              textAnchor="middle"
                              className={
                                isSelected ? (ldrag ? "cursor-grabbing touch-none" : "cursor-grab touch-none") : undefined
                              }
                              style={{
                                fill: "var(--foreground)",
                                fontSize: fontSize * lscale,
                                pointerEvents: isSelected ? "all" : "none",
                              }}
                              onPointerDown={isSelected ? (e) => handleLabelPointerDown(e, placement, placement.rotation) : undefined}
                              onPointerMove={isSelected ? handleLabelPointerMove : undefined}
                              onPointerUp={isSelected ? handleLabelPointerUp : undefined}
                              onPointerCancel={() => setLabelDrag(null)}
                            >
                              {feature?.name || placement.feature_class}
                            </text>
                          </g>
                        </g>
                      )
                    })()}
                  {/* Label nudge/rotate/resize controls moved to the header
                      (LabelAdjustBar) so they no longer float over the feature. */}
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
                  <LabelToggleControl
                    x={-iconSize * 0.45}
                    y={-iconSize * 0.45}
                    size={removeSize}
                    hidden={!!placement.label_hidden}
                    onToggle={() => void toggleLabelHidden(placement)}
                  />
                  <RemoveControl
                    x={iconSize * 0.45}
                    y={-iconSize * 0.45}
                    size={removeSize}
                    onRemove={() => setPendingRemoval({ id: placement.id, label: feature?.name || placement.feature_class })}
                  />
                  <DuplicateControl
                    x={iconSize * 0.45}
                    y={iconSize * 0.45}
                    size={removeSize}
                    onDuplicate={() => duplicatePlacement(placement, feature)}
                  />
                </g>
              )
            })}
            </g>
            {/* In-progress line draft (Slice 5): the vertices committed so
              far as dots, the polyline through them, and a rubber-band
              segment from the last vertex to the pointer. Drawn last so it
              sits on top of everything; pointerEvents off so it never
              swallows the canvas clicks that are extending it. */}
            {isLineDrawing && lineDraft && lineDraft.length > 0 && (
              <g style={{ pointerEvents: "none" }}>
                {lineDraft.length >= 2 && (
                  <path
                    d={polylinePath(lineDraft)}
                    fill="none"
                    style={{ stroke: "var(--ring)" }}
                    strokeWidth={Math.max(fontSize * 0.5, 2)}
                  />
                )}
                {linePreview && (
                  <line
                    x1={lineDraft[lineDraft.length - 1][0]}
                    y1={lineDraft[lineDraft.length - 1][1]}
                    x2={linePreview.x}
                    y2={linePreview.y}
                    style={{ stroke: "var(--ring)", opacity: 0.5 }}
                    strokeWidth={Math.max(fontSize * 0.5, 2)}
                    strokeDasharray="4 4"
                  />
                )}
                {lineDraft.map(([x, y], i) => (
                  <circle
                    key={i}
                    cx={x}
                    cy={y}
                    r={Math.max(fontSize * 0.4, 2)}
                    style={{ fill: "var(--ring)" }}
                  />
                ))}
              </g>
            )}
            {/* In-progress element-line draft (Phase 24 keystone) -- the same
              committed-vertices + rubber-band sketch the placement line draw
              uses, but commits to a `line` element instead of a placement. */}
            {drawingElementLine && elemLineDraft && elemLineDraft.length > 0 && (
              <g style={{ pointerEvents: "none" }}>
                {elemLineDraft.length >= 2 && (
                  <path
                    d={polylinePath(elemLineDraft)}
                    fill="none"
                    style={{ stroke: "var(--ring)" }}
                    strokeWidth={Math.max(fontSize * 0.5, 2)}
                  />
                )}
                {elemLinePreview && (
                  <line
                    x1={elemLineDraft[elemLineDraft.length - 1][0]}
                    y1={elemLineDraft[elemLineDraft.length - 1][1]}
                    x2={elemLinePreview.x}
                    y2={elemLinePreview.y}
                    style={{ stroke: "var(--ring)", opacity: 0.5 }}
                    strokeWidth={Math.max(fontSize * 0.5, 2)}
                    strokeDasharray="4 4"
                  />
                )}
                {elemLineDraft.map(([x, y], i) => (
                  <circle key={i} cx={x} cy={y} r={Math.max(fontSize * 0.4, 2)} style={{ fill: "var(--ring)" }} />
                ))}
              </g>
            )}
          </svg>
          </>
        )}
      </div>
      <DiagramStatusBar ref={statusBarRef} />
      <RemovePlacementDialog
        diagramId={openDiagramId}
        placement={pendingRemoval}
        onOpenChange={(open) => !open && setPendingRemoval(null)}
      />
      <RemoveElementDialog
        diagramId={openDiagramId}
        element={pendingElementRemoval}
        onOpenChange={(open) => !open && setPendingElementRemoval(null)}
      />
    </div>
  )
}
