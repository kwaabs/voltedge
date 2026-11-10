import { useEffect, useMemo, useRef, useState } from "react"
import {
  Bell,
  Check,
  Flame,
  Maximize2,
  Move,
  Palette,
  Printer,
  Redo2,
  RotateCcw,
  ShieldAlert,
  Spline,
  TriangleAlert,
  Undo2,
  X,
  Zap,
} from "lucide-react"

import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { DiagramPrintDialog } from "@/components/map/diagram-print-dialog"
import { OperateMenu } from "@/components/map/operate-menu"
import { Button } from "@/components/ui/button"
import { useAllSymbology } from "@/hooks/use-all-symbology"
import { usePermission } from "@/hooks/use-auth"
import { useFeatureClasses } from "@/hooks/use-feature-classes"
import { useAlarms } from "@/hooks/use-alarms"
import { useIncidents } from "@/hooks/use-oms"
import { useAbnormalDevices } from "@/hooks/use-operations"
import { useActiveGrounds, useActiveTags } from "@/hooks/use-safety"
import { useSymbologyIcons } from "@/hooks/use-symbology-icons"
import { refKey, useEnergization } from "@/lib/energization"
import {
  circuitColor,
  type OneLineConnector,
  type OneLineNode,
  type OneLinePayload,
  useOneLineOverrides,
  useOneLineWireOverrides,
  useResetOneLineOverrides,
  useResetWireOverrides,
  useSaveOneLineOverride,
  useSaveWireOverride,
} from "@/lib/one-line"
import { routeConnectors } from "@/lib/one-line-route"
import { resolveSymbology } from "@/lib/symbology"
import { iconDataUri } from "@/lib/symbology-icons"
import { cn } from "@/lib/utils"
import { useIsolateStore } from "@/stores/isolate-store"
import { useSelectionStore } from "@/stores/selection-store"
import { useTraceStore } from "@/stores/trace-store"

type Pos = { x: number; y: number }

// Matches the ephemeral one-line renderer (one-line-view.tsx) so the operations view
// reads identically. Abstract one-line units (the layout engine's coordinate space).
const ICON = 30
const PAD = 60
const DEFAULT_SYMBOL_COLOR = "#0f172a"
const CONNECTOR_COLOR = "#64748b"
const OPEN_RING_COLOR = "#d97706"
const CLOSED_RING_COLOR = "#16a34a"
const DEAD_COLOR = "#94a3b8"
const ONE_LINE_COL = 110 // engine column spacing -- the zoom/density unit
const LABEL_COLS_MAX = 55 // show rating labels only when <= this many columns are in view
const LEAF_COLS_MAX = 130 // collapse consumer/sensor leaf glyphs past this density
// ADMS condition badge colours (match diagram-canvas.tsx).
const ALARM_BADGE = "#ef4444"
const ABNORMAL_BADGE = "#f59e0b"
const SAFETY_BADGE = "#eab308"
const INCIDENT_BADGE = "#e11d48"
const HIGHLIGHT_RING = "#38bdf8" // sky -- the clicked/highlighted device (matches the map's "selected")
// Analysis overlays projected onto the live board. Deliberately a DIFFERENT visual
// language from the live energisation shading (grey = what IS dead): these are
// HYPOTHETICAL / investigative answers drawn on top.
const TRACE_COLOR = "#2563eb" // a trace's reached features (blue) -- matches one-line-view
const BARRIER_COLOR = "#dc2626" // where a trace stopped / the isolation's upstream boundary (red)
const ISOLATE_COLOR = "#ea580c" // "would lose power if this device opens" (orange) -- not the grey of already-dead
const BACKFEED_COLOR = "#eab308" // a possible back-feed path to verify (amber, dashed)
// Container/assembly fences: a labelled box drawn behind a group's members so an
// operator reads a substation / assembly as one thing (Light fencing -- bbox over
// wherever the group's nodes landed, so it follows the arrange overrides).
const CONTAINER_FENCE = "#64748b" // slate
const ASSEMBLY_FENCE = "#8b5cf6" // violet
const FENCE_PAD = ICON * 0.9
const FENCE_LABEL_H = 16

function connectorWidth(network: string | undefined): number {
  if (network === "MV") return 3
  if (network === "LV") return 1.8
  return 2.2
}

interface Box {
  x: number
  y: number
  w: number
  h: number
}

function contentBox(payload: OneLinePayload | undefined): Box {
  let minX = Infinity
  let minY = Infinity
  let maxX = -Infinity
  let maxY = -Infinity
  const grow = (x: number, y: number) => {
    if (x < minX) minX = x
    if (y < minY) minY = y
    if (x > maxX) maxX = x
    if (y > maxY) maxY = y
  }
  payload?.nodes.forEach((n) => grow(n.x, n.y))
  payload?.connectors.forEach((c) => c.path.forEach(([x, y]) => grow(x, y)))
  if (!Number.isFinite(minX)) return { x: 0, y: 0, w: 100, h: 100 }
  return { x: minX - PAD, y: minY - PAD, w: maxX - minX + PAD * 2, h: maxY - minY + PAD * 2 }
}

interface OverlaySets {
  alarms?: Set<string>
  abnormal?: Set<string>
  safety?: Set<string>
  incidents?: Set<string>
}

/**
 * The ADMS operations schematic renderer: a LIVE whole-network one-line, computed
 * from the model on demand (no persisted diagram), with pan/zoom, energisation
 * shading, device-state rings, ADMS condition badges, and right-click operate. This
 * replaces the diagram-engine canvas on the operations route -- it renders the
 * network's current state, it isn't an authored drawing, so there's nothing to keep
 * in sync and nothing coupled to GIS editing.
 */
export function OperationsOneLine({ payload: rawPayload, operations = true }: { payload: OneLinePayload; operations?: boolean }) {
  // Go marshals a nil slice as JSON `null`, so nodes/connectors can arrive null for a
  // sparse payload -- normalise to [] so every map/forEach below is safe.
  const payload = useMemo<OneLinePayload>(
    () => ({
      nodes: rawPayload.nodes ?? [],
      connectors: rawPayload.connectors ?? [],
      groups: rawPayload.groups ?? [],
      truncated: rawPayload.truncated,
      node_count: rawPayload.node_count,
    }),
    [rawPayload],
  )
  // useAllSymbology returns the rules map DIRECTLY (keyed by class code), not a query
  // object -- destructuring `.data` off it yields undefined and every symbol falls back
  // to the generic glyph. Use the returned map as-is (matches one-line-view.tsx).
  const rules = useAllSymbology()
  const icons = useSymbologyIcons()
  const { data: featureClasses } = useFeatureClasses()

  const [camera, setCamera] = useState<Box | null>(null)
  const svgRef = useRef<SVGSVGElement | null>(null)
  const panRef = useRef<{ startX: number; startY: number; cam: Box } | null>(null)

  const fit = useMemo(() => contentBox(payload), [payload])
  const view = camera ?? fit

  // Overlay toggles (default on in operations mode). Each gates its own read.
  const [showEnerg, setShowEnerg] = useState(true)
  const [showAlarms, setShowAlarms] = useState(operations)
  const [showAbnormal, setShowAbnormal] = useState(operations)
  const [showSafety, setShowSafety] = useState(operations)
  const [showIncidents, setShowIncidents] = useState(operations)

  const energRefs = useMemo(() => {
    const refs = payload.nodes.map((n) => ({ feature_class: n.feature_class, feature_id: n.feature_id }))
    payload.connectors.forEach((c) => {
      if (c.feature_class && c.feature_id) refs.push({ feature_class: c.feature_class, feature_id: c.feature_id })
    })
    return refs
  }, [payload])
  const { data: deadSet } = useEnergization("ops-oneline", energRefs, showEnerg && energRefs.length > 0)

  const { data: alarmList } = useAlarms({ status: "live" }, showAlarms)
  const { data: abnormalList } = useAbnormalDevices(undefined, showAbnormal)
  const { data: activeTags } = useActiveTags(showSafety)
  const { data: activeGrounds } = useActiveGrounds(showSafety)
  const { data: openIncidents } = useIncidents({ openOnly: true, enabled: showIncidents })

  const overlays: OverlaySets = useMemo(() => {
    const o: OverlaySets = {}
    if (showAlarms && alarmList) {
      const s = new Set<string>()
      for (const a of alarmList.alarms) if (a.device_class && a.device_id) s.add(refKey(a.device_class, a.device_id))
      o.alarms = s
    }
    if (showAbnormal && abnormalList) {
      o.abnormal = new Set(abnormalList.devices.map((d) => refKey(d.device_class, d.device_id)))
    }
    if (showSafety) {
      const s = new Set<string>()
      for (const t of activeTags?.tags ?? []) s.add(refKey(t.device_class, t.device_id))
      for (const g of activeGrounds?.grounds ?? []) s.add(refKey(g.feature_class, g.feature_id))
      o.safety = s
    }
    if (showIncidents && openIncidents) {
      const s = new Set<string>()
      for (const i of openIncidents.incidents)
        if (i.predicted_device_class && i.predicted_device_id)
          s.add(refKey(i.predicted_device_class, i.predicted_device_id))
      o.incidents = s
    }
    return o
  }, [showAlarms, alarmList, showAbnormal, abnormalList, showSafety, activeTags, activeGrounds, showIncidents, openIncidents])
  const anyBadges = Boolean(overlays.alarms || overlays.abnormal || overlays.safety || overlays.incidents)

  // Arrange (Phase B): operator position nudges persisted in an OPERATIONS-owned
  // override layer (ops.oneline_overrides) -- NOT a GIS diagram. A nudged feature is
  // drawn at its override on top of the deterministic computed layout; wires re-route
  // live. Dragging requires operations:write + arrange mode.
  const can = usePermission()
  const canArrange = can("operations:write")
  const canOperateAny = can("oms:write") || can("safety:write") || can("trace:run")
  const [arrange, setArrange] = useState(false)
  const { data: overrideData } = useOneLineOverrides()
  const saveOverride = useSaveOneLineOverride()
  const resetOverrides = useResetOneLineOverrides()
  // Local (optimistic) nudges applied immediately while dragging / after a save, so
  // the glyph tracks the pointer without waiting for the round-trip.
  const [localOv, setLocalOv] = useState<Record<string, Pos>>({})
  const fetchedOv = useMemo(() => {
    const m: Record<string, Pos> = {}
    overrideData?.overrides?.forEach((o) => {
      m[refKey(o.feature_class, o.feature_id)] = { x: o.x, y: o.y }
    })
    return m
  }, [overrideData])
  const nodePos = (n: OneLineNode): Pos => {
    const k = refKey(n.feature_class, n.feature_id)
    return localOv[k] ?? fetchedOv[k] ?? { x: n.x, y: n.y }
  }
  // Connectors re-route from current (possibly nudged) node positions, exactly like
  // the editable ephemeral one-line; untouched when there are no overrides.
  const routedConnectors = useMemo(() => {
    if (Object.keys(localOv).length === 0 && Object.keys(fetchedOv).length === 0) return payload.connectors
    const byId: Record<string, OneLineNode> = {}
    payload.nodes.forEach((n) => {
      byId[n.feature_id] = n
    })
    const posOf = (id: string): Pos | null => {
      const n = byId[id]
      if (!n) return null
      const k = refKey(n.feature_class, n.feature_id)
      return localOv[k] ?? fetchedOv[k] ?? { x: n.x, y: n.y }
    }
    return routeConnectors(payload.connectors, posOf)
  }, [payload, localOv, fetchedOv])
  const nodeDragRef = useRef<{ key: string; cls: string; id: string } | null>(null)

  // Wire routing (operations-owned, ops.oneline_wire_overrides): the INTERMEDIATE bend
  // vertices per wire. Endpoints always follow the live nodes, so a hand-routed wire
  // survives the live recompute and node drags; only the bends are stored.
  const { data: wireOvData } = useOneLineWireOverrides()
  const saveWire = useSaveWireOverride()
  const resetWires = useResetWireOverrides()
  const [localWireOv, setLocalWireOv] = useState<Record<string, [number, number][]>>({})
  const fetchedWireOv = useMemo(() => {
    const m: Record<string, [number, number][]> = {}
    wireOvData?.wires?.forEach((wo) => {
      m[wo.wire_key] = wo.vertices
    })
    return m
  }, [wireOvData])
  const wireDragRef = useRef<{ key: string; index: number } | null>(null)
  // A stable key for a wire: a real conductor's feature_id, else its sorted node pair.
  const wireKeyOf = (c: OneLineConnector): string | null => {
    if (c.feature_id) return c.feature_id
    if (c.endpoints && c.endpoints.length === 2) return `link:${[...c.endpoints].sort().join("|")}`
    return null
  }
  const wireVerts = (wk: string): [number, number][] => localWireOv[wk] ?? fetchedWireOv[wk] ?? []

  // --- Arrange EDIT SESSION ---------------------------------------------------------
  // Edits are STAGED in the local buffers (localOv / localWireOv) and only written on
  // Save -- never auto-persisted per action. Undo/redo snapshot those buffers; Save
  // batch-commits; Discard throws the staged changes away. `dirty` = there are staged
  // changes; `hasPersisted` = something is already saved on the server.
  type EditSnapshot = { ov: Record<string, Pos>; wov: Record<string, [number, number][]> }
  const [undoStack, setUndoStack] = useState<EditSnapshot[]>([])
  const [redoStack, setRedoStack] = useState<EditSnapshot[]>([])
  const dirty = Object.keys(localOv).length > 0 || Object.keys(localWireOv).length > 0
  const hasPersisted = Object.keys(fetchedOv).length > 0 || Object.keys(fetchedWireOv).length > 0
  // Snapshot the current staged state onto the undo stack (call BEFORE applying an edit).
  const pushHistory = () => {
    setUndoStack((s) => [...s, { ov: { ...localOv }, wov: { ...localWireOv } }])
    setRedoStack([])
  }
  const undo = () => {
    if (undoStack.length === 0) return
    const prev = undoStack[undoStack.length - 1]
    setRedoStack((r) => [...r, { ov: { ...localOv }, wov: { ...localWireOv } }])
    setUndoStack((s) => s.slice(0, -1))
    setLocalOv(prev.ov)
    setLocalWireOv(prev.wov)
  }
  const redo = () => {
    if (redoStack.length === 0) return
    const next = redoStack[redoStack.length - 1]
    setUndoStack((s) => [...s, { ov: { ...localOv }, wov: { ...localWireOv } }])
    setRedoStack((r) => r.slice(0, -1))
    setLocalOv(next.ov)
    setLocalWireOv(next.wov)
  }
  const clearSession = () => {
    setLocalOv({})
    setLocalWireOv({})
    setUndoStack([])
    setRedoStack([])
  }
  const discardSession = () => clearSession()
  // Save: batch-write every staged node nudge + wire routing, then clear the session
  // (the mutations invalidate the override queries, so the display picks up the now-saved
  // values from the server).
  const saveSession = async () => {
    const puts: Promise<unknown>[] = []
    for (const [k, p] of Object.entries(localOv)) {
      const sep = k.indexOf("|")
      puts.push(saveOverride.mutateAsync({ feature_class: k.slice(0, sep), feature_id: k.slice(sep + 1), x: p.x, y: p.y }))
    }
    for (const [wk, verts] of Object.entries(localWireOv)) {
      puts.push(saveWire.mutateAsync({ wire_key: wk, vertices: verts }))
    }
    try {
      await Promise.all(puts)
    } finally {
      clearSession()
    }
  }
  const savingSession = saveOverride.isPending || saveWire.isPending
  // Leaving Arrange with unsaved staged changes prompts first.
  const [exitConfirm, setExitConfirm] = useState(false)
  const toggleArrange = () => {
    if (arrange && dirty) {
      setExitConfirm(true)
      return
    }
    setArrange((v) => !v)
  }

  // Fences: a labelled box around each container/assembly group's members, computed
  // over FINAL node positions (so it follows arrange overrides). Only groups with >=2
  // members on-screen (the backend already filters, re-checked here defensively).
  const fences = useMemo(() => {
    const groups = payload.groups ?? []
    if (groups.length === 0) return []
    const meta = new Map(groups.map((g) => [g.id, g]))
    const box = new Map<string, { minX: number; minY: number; maxX: number; maxY: number; n: number }>()
    for (const n of payload.nodes) {
      if (!n.group_id || !meta.has(n.group_id)) continue
      const k = refKey(n.feature_class, n.feature_id)
      const p = localOv[k] ?? fetchedOv[k] ?? { x: n.x, y: n.y }
      const b = box.get(n.group_id) ?? { minX: Infinity, minY: Infinity, maxX: -Infinity, maxY: -Infinity, n: 0 }
      b.minX = Math.min(b.minX, p.x)
      b.minY = Math.min(b.minY, p.y)
      b.maxX = Math.max(b.maxX, p.x)
      b.maxY = Math.max(b.maxY, p.y)
      b.n += 1
      box.set(n.group_id, b)
    }
    const out: { id: string; label: string; kind: string; x: number; y: number; w: number; h: number }[] = []
    for (const [id, b] of box) {
      if (b.n < 2) continue
      const g = meta.get(id)!
      out.push({
        id,
        label: g.label,
        kind: g.kind,
        x: b.minX - FENCE_PAD,
        y: b.minY - FENCE_PAD - FENCE_LABEL_H,
        w: b.maxX - b.minX + FENCE_PAD * 2,
        h: b.maxY - b.minY + FENCE_PAD * 2 + FENCE_LABEL_H,
      })
    }
    return out
  }, [payload, localOv, fetchedOv])

  // Zoom-based declutter: measure zoom as columns in view (engine column spacing).
  const colsInView = view.w / ONE_LINE_COL
  const showLabels = colsInView <= LABEL_COLS_MAX
  const collapseLeaves = colsInView > LEAF_COLS_MAX
  const leafClassCodes = useMemo(() => {
    const set = new Set<string>()
    featureClasses?.feature_classes.forEach((fc) => {
      if (fc.functional_role === "consumer" || fc.functional_role === "sensor") set.add(fc.code)
    })
    return set
  }, [featureClasses])
  // Code -> human class name ("mv_fuse" -> "MV Fuse") for the selection readout.
  const classNameByCode = useMemo(() => {
    const m: Record<string, string> = {}
    featureClasses?.feature_classes.forEach((fc) => {
      m[fc.code] = fc.name
    })
    return m
  }, [featureClasses])

  // Bus classes: diagram_geometry_type='line' (migration 0053) means "render this point
  // junction as a spanning BAR, not a dot" -- a busbar. The layout still places it as a
  // node; we draw it as a bar here and tap its devices onto it (the one-line convention).
  const busClassCodes = useMemo(() => {
    const s = new Set<string>()
    featureClasses?.feature_classes.forEach((fc) => {
      if (fc.diagram_geometry_type === "line") s.add(fc.code)
    })
    return s
  }, [featureClasses])
  const nodeById = useMemo(() => {
    const m: Record<string, OneLineNode> = {}
    payload.nodes.forEach((n) => {
      m[n.feature_id] = n
    })
    return m
  }, [payload])

  // Legend: every feature class actually on the board (a point glyph, or a line swatch
  // for a conductor), resolved from the SAME symbology the canvas draws. Alphabetical.
  const legendItems = useMemo(() => {
    const seen = new Set<string>()
    const items: { code: string; name: string; icon?: string; lineColor?: string }[] = []
    const nameOf = (code: string) => classNameByCode[code] ?? humanizeClass(code)
    payload.nodes.forEach((n) => {
      if (seen.has(n.feature_class)) return
      seen.add(n.feature_class)
      const def =
        resolveSymbology(rules[n.feature_class], n.subtype, {}, "diagram") ??
        resolveSymbology(rules[n.feature_class], n.subtype, {}, "map")
      const color = def?.color ?? DEFAULT_SYMBOL_COLOR
      items.push({ code: n.feature_class, name: nameOf(n.feature_class), icon: iconDataUri(def?.icon_ref, color, icons) })
    })
    payload.connectors.forEach((c) => {
      if (!c.feature_class || seen.has(c.feature_class)) return
      seen.add(c.feature_class)
      const def =
        resolveSymbology(rules[c.feature_class], c.subtype, {}, "diagram") ??
        resolveSymbology(rules[c.feature_class], c.subtype, {}, "map")
      items.push({ code: c.feature_class, name: nameOf(c.feature_class), lineColor: def?.color ?? CONNECTOR_COLOR })
    })
    items.sort((a, b) => a.name.localeCompare(b.name))
    return items
  }, [payload, rules, icons, classNameByCode])
  const [showLegend, setShowLegend] = useState(true)

  // Print: frame the current view (or the whole network) onto a paper sheet via the
  // shared diagram print dialog (clones the live SVG -> PDF). A whole-network plot can be
  // enormous, so the DEFAULT scope is the current view.
  const [printFitBox, setPrintFitBox] = useState<{ x: number; y: number; w: number; h: number } | null>(null)
  const openPrint = (scope: "view" | "all") => {
    // Current view = EXACTLY the framed viewBox (WYSIWYG, matches the canvas). Entire
    // network = the true drawn extent (union of child bboxes), which can be sparse if the
    // layout scatters disconnected pieces -- that's the whole network, warts and all.
    if (scope === "view") {
      setPrintFitBox({ x: view.x, y: view.y, w: view.w, h: view.h })
      return
    }
    const el = svgRef.current
    let all = { x: view.x, y: view.y, w: view.w, h: view.h }
    if (el) {
      let x0 = Infinity
      let y0 = Infinity
      let x1 = -Infinity
      let y1 = -Infinity
      for (const child of Array.from(el.children)) {
        const g = child as SVGGraphicsElement
        if (typeof g.getBBox !== "function") continue
        try {
          const b = g.getBBox()
          if (!b || (b.width === 0 && b.height === 0)) continue
          x0 = Math.min(x0, b.x)
          y0 = Math.min(y0, b.y)
          x1 = Math.max(x1, b.x + b.width)
          y1 = Math.max(y1, b.y + b.height)
        } catch {
          /* skip an un-measurable child */
        }
      }
      if (Number.isFinite(x0)) {
        const PAD = 24
        all = { x: x0 - PAD, y: y0 - PAD, w: x1 - x0 + 2 * PAD, h: y1 - y0 + 2 * PAD }
      }
    }
    setPrintFitBox(all)
  }
  // Bus geometry: for each bus node, the horizontal bar [x0..x1] at its y, sized to span
  // every device tapped onto it (from the connectors that touch it) plus a small overhang.
  const buses = useMemo(() => {
    const busIds = new Set<string>()
    payload.nodes.forEach((n) => {
      if (busClassCodes.has(n.feature_class)) busIds.add(n.feature_id)
    })
    const barById = new Map<string, { x0: number; x1: number; y: number }>()
    if (busIds.size === 0) return { busIds, barById }
    const posOf = (n: OneLineNode) => {
      const k = refKey(n.feature_class, n.feature_id)
      return localOv[k] ?? fetchedOv[k] ?? { x: n.x, y: n.y }
    }
    const tapXs = new Map<string, number[]>()
    routedConnectors.forEach((c) => {
      const eps = c.endpoints
      if (!eps || eps.length < 2) return
      const [a, b] = eps
      const busId = busIds.has(a) ? a : busIds.has(b) ? b : ""
      if (!busId) return
      const tapN = nodeById[busId === a ? b : a]
      if (!tapN) return
      if (!tapXs.has(busId)) tapXs.set(busId, [])
      tapXs.get(busId)!.push(posOf(tapN).x)
    })
    const MIN_W = ICON * 2
    const PAD_X = ICON * 0.6
    payload.nodes.forEach((n) => {
      if (!busIds.has(n.feature_id)) return
      const bp = posOf(n)
      const xs = [bp.x, ...(tapXs.get(n.feature_id) ?? [])]
      let x0 = Math.min(...xs) - PAD_X
      let x1 = Math.max(...xs) + PAD_X
      if (x1 - x0 < MIN_W) {
        const c = (x0 + x1) / 2
        x0 = c - MIN_W / 2
        x1 = c + MIN_W / 2
      }
      barById.set(n.feature_id, { x0, x1, y: bp.y })
    })
    return { busIds, barById }
  }, [payload, routedConnectors, busClassCodes, nodeById, localOv, fetchedOv])
  // A connector that touches a bus is re-drawn as a vertical STUB from the tapped device
  // up/down to the bar (at the device's own x), instead of an elbow to the bus centre.
  const busStub = (c: OneLineConnector): [number, number][] | null => {
    const eps = c.endpoints
    if (!eps || eps.length < 2) return null
    const [a, b] = eps
    const busId = buses.busIds.has(a) ? a : buses.busIds.has(b) ? b : ""
    if (!busId) return null
    const tapN = nodeById[busId === a ? b : a]
    const bar = buses.barById.get(busId)
    if (!tapN || !bar) return null
    const k = refKey(tapN.feature_class, tapN.feature_id)
    const tp = localOv[k] ?? fetchedOv[k] ?? { x: tapN.x, y: tapN.y }
    return [
      [tp.x, tp.y],
      [tp.x, bar.y],
    ]
  }

  // Go-to: centre on a located feature (operations search / "zoom to") when the
  // focus token bumps -- never on ordinary selection.
  const located = useSelectionStore((s) => s.located)
  const locatedFocusToken = useSelectionStore((s) => s.locatedFocusToken)
  useEffect(() => {
    if (locatedFocusToken === 0 || !located) return
    const n = payload.nodes.find(
      (nn) => nn.feature_class === located.featureClassCode && nn.feature_id === located.featureId,
    )
    if (!n) return
    const w = ONE_LINE_COL * 12
    const aspect = fit.w > 0 ? fit.h / fit.w : 0.6
    const h = w * aspect
    setCamera({ x: n.x - w / 2, y: n.y - h / 2, w, h })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [locatedFocusToken])

  const clientToSvg = (clientX: number, clientY: number) => {
    const svg = svgRef.current
    if (!svg) return null
    const ctm = svg.getScreenCTM()
    if (!ctm) return null
    const p = svg.createSVGPoint()
    p.x = clientX
    p.y = clientY
    const t = p.matrixTransform(ctm.inverse())
    return { x: t.x, y: t.y }
  }

  const onWheel = (e: React.WheelEvent) => {
    const p = clientToSvg(e.clientX, e.clientY)
    if (!p) return
    const f = e.deltaY < 0 ? 0.85 : 1 / 0.85
    const w = view.w * f
    const h = view.h * f
    setCamera({ x: p.x - (p.x - view.x) * f, y: p.y - (p.y - view.y) * f, w, h })
  }

  const panMovedRef = useRef(false)
  const onBackgroundPointerDown = (e: React.PointerEvent) => {
    if (e.button !== 0) return
    panRef.current = { startX: e.clientX, startY: e.clientY, cam: view }
    panMovedRef.current = false
    ;(e.target as Element).setPointerCapture?.(e.pointerId)
  }
  const onPointerMove = (e: React.PointerEvent) => {
    const wd = wireDragRef.current
    if (wd) {
      const p = clientToSvg(e.clientX, e.clientY)
      if (p) {
        setLocalWireOv((m) => {
          const cur = m[wd.key] ?? wireVerts(wd.key)
          const next = cur.slice()
          next[wd.index] = [p.x, p.y]
          return { ...m, [wd.key]: next }
        })
      }
      return
    }
    const nd = nodeDragRef.current
    if (nd) {
      const p = clientToSvg(e.clientX, e.clientY)
      if (p) setLocalOv((m) => ({ ...m, [nd.key]: { x: p.x, y: p.y } }))
      return
    }
    const pan = panRef.current
    if (!pan) return
    // Past a small threshold it's a pan, not a click -- so a click on empty space still
    // clears the selection while a drag-to-pan keeps it.
    if (Math.abs(e.clientX - pan.startX) > 3 || Math.abs(e.clientY - pan.startY) > 3) panMovedRef.current = true
    const rect = svgRef.current?.getBoundingClientRect()
    if (!rect) return
    const sx = pan.cam.w / rect.width
    const sy = pan.cam.h / rect.height
    setCamera({
      x: pan.cam.x - (e.clientX - pan.startX) * sx,
      y: pan.cam.y - (e.clientY - pan.startY) * sy,
      w: pan.cam.w,
      h: pan.cam.h,
    })
  }
  const endDrag = () => {
    const wd = wireDragRef.current
    if (wd) {
      // Staged only -- Save commits. History was pushed on drag start.
      wireDragRef.current = null
      return
    }
    const nd = nodeDragRef.current
    if (nd) {
      nodeDragRef.current = null
      return
    }
    // A background click (pointer didn't travel) clears the current selection.
    if (panRef.current && !panMovedRef.current) setHighlighted(null)
    panRef.current = null
  }
  // Nuclear resets (shown only when there's nothing staged): clear the SAVED layout on
  // the server. They also drop any session state so the buffers/history can't go stale.
  const resetLayout = () => {
    clearSession()
    resetOverrides.mutate()
  }
  const straightenWires = () => {
    clearSession()
    resetWires.mutate()
  }

  // Right-click operate menu.
  const [menu, setMenu] = useState<{
    x: number
    y: number
    cls: string
    id: string
    state: string | null
    isConductor?: boolean
  } | null>(null)
  // Left-click highlights a device (a ring on the schematic) WITHOUT opening any
  // panel; attributes/details are reached from the right-click menu instead.
  const [highlighted, setHighlighted] = useState<{ cls: string; id: string } | null>(null)
  const highlightedName = highlighted ? (classNameByCode[highlighted.cls] ?? humanizeClass(highlighted.cls)) : null

  // Analysis overlay: project the active ISOLATE study or TRACE result onto the board.
  // The operate-menu's "Isolate" and the feature panel's trace actions arm these stores;
  // this is what makes their answer visible here. They're mutually exclusive in the UI
  // (running one clears the others), but isolate takes priority defensively. Each is a
  // "what-if / what-reaches" answer, drawn in its own colour so it never reads as the
  // live energisation state.
  const traceResult = useTraceStore((s) => s.result)
  const isolateResult = useIsolateStore((s) => s.result)
  const clearTrace = useTraceStore((s) => s.clear)
  const clearIsolate = useIsolateStore((s) => s.clear)
  const analysis = useMemo(() => {
    if (isolateResult) {
      const affected = new Set(isolateResult.de_energized.map((f) => refKey(f.feature_class, f.feature_id)))
      const barriers = new Set<string>()
      if (isolateResult.upstream_boundary) {
        barriers.add(refKey(isolateResult.upstream_boundary.feature_class, isolateResult.upstream_boundary.feature_id))
      }
      const backfeed = new Set((isolateResult.possible_backfeed_paths ?? []).map((f) => refKey(f.feature_class, f.feature_id)))
      return {
        kind: "isolate" as const,
        color: ISOLATE_COLOR,
        affected,
        barriers,
        backfeed,
        startKey: refKey(isolateResult.device.feature_class, isolateResult.device.feature_id),
        count: affected.size,
        clear: clearIsolate,
      }
    }
    if (traceResult) {
      return {
        kind: "trace" as const,
        color: TRACE_COLOR,
        affected: new Set(traceResult.features.map((f) => refKey(f.feature_class, f.feature_id))),
        barriers: new Set(traceResult.barriers.map((b) => refKey(b.feature_class, b.feature_id))),
        backfeed: new Set<string>(),
        startKey: refKey(traceResult.start.feature_class, traceResult.start.feature_id),
        count: traceResult.features.length,
        clear: clearTrace,
      }
    }
    return null
  }, [isolateResult, traceResult, clearIsolate, clearTrace])

  return (
    <div className="relative h-full min-h-0 w-full overflow-hidden bg-slate-50 dark:bg-slate-950">
      {/* Top-left readout: the selected feature (class + id), or the operate hint when
          nothing is selected. Left-click a symbol/wire to select it. */}
      {highlighted ? (
        <div className="bg-background/90 absolute left-2 top-2 z-10 max-w-[18rem] rounded-md border px-2.5 py-1.5 shadow-sm">
          <div className="text-foreground truncate text-sm font-medium">{highlightedName}</div>
          <div className="text-muted-foreground truncate font-mono text-[11px]" title={highlighted.id}>
            {highlighted.id}
          </div>
        </div>
      ) : (
        canOperateAny && (
          <div className="bg-background/80 text-muted-foreground absolute left-2 top-2 z-10 rounded-md border border-dashed px-2 py-1 text-xs">
            Click a feature to select it · right-click to operate
          </div>
        )
      )}
      {/* Overlay toolbar */}
      <div className="bg-background/90 absolute right-2 top-2 z-10 flex items-center gap-0.5 rounded-md border p-0.5 shadow-sm">
        <ToggleBtn on={showEnerg} onClick={() => setShowEnerg((v) => !v)} title="Energisation shading">
          <Zap className="size-4" />
        </ToggleBtn>
        <ToggleBtn on={showAlarms} onClick={() => setShowAlarms((v) => !v)} title="Alarm badges">
          <Bell className="size-4" />
        </ToggleBtn>
        <ToggleBtn on={showAbnormal} onClick={() => setShowAbnormal((v) => !v)} title="Off-normal badges">
          <TriangleAlert className="size-4" />
        </ToggleBtn>
        <ToggleBtn on={showSafety} onClick={() => setShowSafety((v) => !v)} title="Safety badges">
          <ShieldAlert className="size-4" />
        </ToggleBtn>
        <ToggleBtn on={showIncidents} onClick={() => setShowIncidents((v) => !v)} title="Incident badges">
          <Flame className="size-4" />
        </ToggleBtn>
        <div className="bg-border mx-0.5 h-5 w-px" />
        <Button variant="ghost" size="icon" className="size-7" title="Fit to content" onClick={() => setCamera(null)}>
          <Maximize2 className="size-4" />
        </Button>
        <ToggleBtn on={showLegend} onClick={() => setShowLegend((v) => !v)} title="Legend">
          <Palette className="size-4" />
        </ToggleBtn>
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button variant="ghost" size="icon" className="size-7" title="Print to PDF">
              <Printer className="size-4" />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end">
            <DropdownMenuItem onClick={() => openPrint("view")}>Print current view</DropdownMenuItem>
            <DropdownMenuItem onClick={() => openPrint("all")}>Print entire network</DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
        {canArrange && (
          <>
            <ToggleBtn
              on={arrange}
              onClick={toggleArrange}
              title="Arrange — drag symbols to reposition, drag a wire's handles to bend it, double-click a wire to add a bend / a handle to remove it. Changes are staged; Save or Discard to finish. Never touches real geometry."
            >
              <Move className="size-4" />
            </ToggleBtn>
            {arrange && (
              <>
                {dirty ? (
                  <>
                    <Button
                      variant="ghost"
                      size="icon"
                      className="size-7"
                      title="Undo"
                      disabled={undoStack.length === 0}
                      onClick={undo}
                    >
                      <Undo2 className="size-4" />
                    </Button>
                    <Button
                      variant="ghost"
                      size="icon"
                      className="size-7"
                      title="Redo"
                      disabled={redoStack.length === 0}
                      onClick={redo}
                    >
                      <Redo2 className="size-4" />
                    </Button>
                    <Button
                      variant="ghost"
                      size="icon"
                      className="size-7 text-green-600"
                      title="Save layout"
                      disabled={savingSession}
                      onClick={() => void saveSession()}
                    >
                      <Check className="size-4" />
                    </Button>
                    <Button
                      variant="ghost"
                      size="icon"
                      className="size-7"
                      title="Discard staged changes"
                      disabled={savingSession}
                      onClick={discardSession}
                    >
                      <X className="size-4" />
                    </Button>
                  </>
                ) : (
                  hasPersisted && (
                    <>
                      {Object.keys(fetchedOv).length > 0 && (
                        <Button
                          variant="ghost"
                          size="icon"
                          className="size-7"
                          title="Reset layout — clear all SAVED symbol nudges, back to the computed layout"
                          onClick={resetLayout}
                        >
                          <RotateCcw className="size-4" />
                        </Button>
                      )}
                      {Object.keys(fetchedWireOv).length > 0 && (
                        <Button
                          variant="ghost"
                          size="icon"
                          className="size-7"
                          title="Straighten wires — clear all SAVED hand-routing, back to the auto route"
                          onClick={straightenWires}
                        >
                          <Spline className="size-4" />
                        </Button>
                      )}
                    </>
                  )
                )}
              </>
            )}
          </>
        )}
      </div>

      <svg
        ref={svgRef}
        className="h-full w-full touch-none select-none"
        viewBox={`${view.x} ${view.y} ${view.w} ${view.h}`}
        preserveAspectRatio="xMidYMid meet"
        style={{ cursor: panRef.current ? "grabbing" : "grab" }}
        onWheel={onWheel}
        onPointerDown={onBackgroundPointerDown}
        onPointerMove={onPointerMove}
        onPointerUp={endDrag}
        onPointerCancel={endDrag}
        onContextMenu={(e) => e.preventDefault()}
      >
        {/* Fences: container/assembly groups, drawn behind everything */}
        {fences.map((f) => {
          const stroke = f.kind === "assembly" ? ASSEMBLY_FENCE : CONTAINER_FENCE
          return (
            <g key={`fence-${f.id}`} className="pointer-events-none">
              <rect
                x={f.x}
                y={f.y}
                width={f.w}
                height={f.h}
                rx={10}
                fill={stroke}
                fillOpacity={0.05}
                stroke={stroke}
                strokeOpacity={0.6}
                strokeWidth={1.4}
                strokeDasharray="6 4"
              />
              <text x={f.x + 6} y={f.y + 12} fontSize={11} fontWeight={600} fill={stroke} fillOpacity={0.85}>
                {f.label}
              </text>
            </g>
          )
        })}
        {/* Connectors (re-routed from nudged node positions when arranged) */}
        {routedConnectors.map((c, i) => {
          const dead = c.feature_class && c.feature_id && deadSet?.has(refKey(c.feature_class, c.feature_id))
          // Colour a conductor by its SYMBOLOGY (class/subtype/attrs), same as the map
          // and the nodes; fall back to circuit colour, then a neutral grey. The backend
          // carries the symbology domain-field attributes so an attribute-driven rule
          // (e.g. by voltage) resolves the same rule the map picks.
          const condAttrs = { ...(c.attributes ?? {}), circuit_ref: c.circuit_ref, network: c.network }
          const condRules = c.feature_class ? rules[c.feature_class] : undefined
          const condDef = condRules
            ? (resolveSymbology(condRules, c.subtype, condAttrs, "diagram") ??
              resolveSymbology(condRules, c.subtype, condAttrs, "map"))
            : undefined
          const color = dead ? DEAD_COLOR : (condDef?.color ?? circuitColor(c.circuit_ref) ?? CONNECTOR_COLOR)
          // Path composition: a wire onto a busbar is an auto vertical stub (no editing);
          // otherwise, if it has hand-routed bends, the path is [live start, ...bends, live
          // end] so the bends stick while the endpoints follow the nodes; else the auto route.
          const wk = wireKeyOf(c)
          const eps = c.endpoints
          const fromN = eps && eps[0] ? nodeById[eps[0]] : undefined
          const toN = eps && eps[1] ? nodeById[eps[1]] : undefined
          const fromPos = fromN ? nodePos(fromN) : null
          const toPos = toN ? nodePos(toN) : null
          const stub = busStub(c)
          const verts = wk && !stub ? wireVerts(wk) : []
          const editable = Boolean(!stub && wk && fromPos && toPos && arrange && canArrange)
          const pathPts: [number, number][] = stub
            ? stub
            : verts.length > 0 && fromPos && toPos
              ? [[fromPos.x, fromPos.y], ...verts, [toPos.x, toPos.y]]
              : (c.path as [number, number][])
          const d = pathPts.map(([x, y], j) => `${j === 0 ? "M" : "L"} ${x} ${y}`).join(" ")
          const w = connectorWidth(c.network)
          // A real conductor (has a feature id) is clickable/inspectable; a plain link
          // (no feature) is not.
          const isFeature = Boolean(c.feature_class && c.feature_id)
          const isHl = isFeature && highlighted?.cls === c.feature_class && highlighted?.id === c.feature_id
          const onAnalysis = isFeature && analysis ? analysis.affected.has(refKey(c.feature_class!, c.feature_id!)) : false
          return (
            <g key={`c${i}`}>
              {onAnalysis && (
                // A fat translucent glow under an analysed conductor (isolate/trace), the
                // wire keeping its own colour on top.
                <path d={d} fill="none" stroke={analysis!.color} strokeOpacity={0.45} strokeWidth={w + 6} strokeLinecap="round" />
              )}
              {isHl && (
                <path d={d} fill="none" stroke={HIGHLIGHT_RING} strokeOpacity={0.4} strokeWidth={w + 6} strokeLinecap="round" />
              )}
              <path d={d} fill="none" stroke={color} strokeWidth={w} strokeDasharray={dead ? "6 4" : undefined} />
              {(isFeature || editable) && (
                <path
                  d={d}
                  fill="none"
                  stroke="transparent"
                  strokeWidth={Math.max(w + 8, 12)}
                  style={{ cursor: editable ? "copy" : "context-menu", pointerEvents: "stroke" }}
                  onPointerDown={(e) => {
                    e.stopPropagation()
                    if (e.button === 0 && isFeature) setHighlighted({ cls: c.feature_class!, id: c.feature_id! })
                  }}
                  onContextMenu={
                    isFeature
                      ? (e) => {
                          e.preventDefault()
                          e.stopPropagation()
                          setHighlighted({ cls: c.feature_class!, id: c.feature_id! })
                          setMenu({ x: e.clientX, y: e.clientY, cls: c.feature_class!, id: c.feature_id!, state: null, isConductor: true })
                        }
                      : undefined
                  }
                  onDoubleClick={
                    editable
                      ? (e) => {
                          e.stopPropagation()
                          const sp = clientToSvg(e.clientX, e.clientY)
                          if (!sp || !fromPos || !toPos) return
                          pushHistory()
                          const nv = insertBend([fromPos.x, fromPos.y], verts, [toPos.x, toPos.y], [sp.x, sp.y])
                          setLocalWireOv((m) => ({ ...m, [wk!]: nv }))
                        }
                      : undefined
                  }
                />
              )}
              {editable &&
                verts.map((v, vi) => (
                  <circle
                    key={`wv${vi}`}
                    cx={v[0]}
                    cy={v[1]}
                    r={4.5}
                    fill="#ffffff"
                    stroke="#2563eb"
                    strokeWidth={1.5}
                    style={{ cursor: "grab" }}
                    onPointerDown={(e) => {
                      e.stopPropagation()
                      if (e.button !== 0) return
                      pushHistory()
                      wireDragRef.current = { key: wk!, index: vi }
                      svgRef.current?.setPointerCapture(e.pointerId)
                    }}
                    onDoubleClick={(e) => {
                      e.stopPropagation()
                      pushHistory()
                      const nv = verts.filter((_, k) => k !== vi)
                      setLocalWireOv((m) => ({ ...m, [wk!]: nv }))
                    }}
                  >
                    <title>Drag to bend · double-click to remove</title>
                  </circle>
                ))}
            </g>
          )
        })}
        {/* Symbols (drawn at their nudged position when arranged) */}
        {payload.nodes.map((n) => {
          if (collapseLeaves && leafClassCodes.has(n.feature_class)) return null
          // Symbology inputs: the carried domain-field attributes (so an attribute-driven
          // rule resolves as on the map) plus the live overlay fields.
          const attrs: Record<string, unknown> = { ...(n.attributes ?? {}) }
          if (n.device_state != null) attrs.device_state = n.device_state
          if (n.circuit_ref != null) attrs.circuit_ref = n.circuit_ref
          const classRules = rules[n.feature_class]
          const def =
            resolveSymbology(classRules, n.subtype, attrs, "diagram") ??
            resolveSymbology(classRules, n.subtype, attrs, "map")
          const dead = deadSet?.has(refKey(n.feature_class, n.feature_id)) ?? false
          const color = dead ? DEAD_COLOR : (def?.color ?? DEFAULT_SYMBOL_COLOR)
          const icon = iconDataUri(def?.icon_ref, color, icons)
          const p = nodePos(n)
          const pn = { ...n, x: p.x, y: p.y }
          const nKey = refKey(n.feature_class, n.feature_id)
          const isBus = buses.busIds.has(n.feature_id)
          const bar = isBus ? buses.barById.get(n.feature_id) : undefined
          const aBarrier = analysis?.barriers.has(nKey) ?? false
          const aBackfeed = analysis?.backfeed.has(nKey) ?? false
          const aStart = analysis?.startKey === nKey
          const aAffected = analysis?.affected.has(nKey) ?? false
          const aRing = aBarrier || aBackfeed || aStart || aAffected
          return (
            <g
              key={`n${n.feature_id}`}
              opacity={dead && !printFitBox ? 0.6 : 1}
              style={{ cursor: arrange && canArrange ? "grab" : "context-menu" }}
              onPointerDown={(e) => {
                e.stopPropagation() // don't start a background pan when pressing a node
                if (e.button !== 0) return
                if (arrange && canArrange) {
                  pushHistory()
                  nodeDragRef.current = { key: refKey(n.feature_class, n.feature_id), cls: n.feature_class, id: n.feature_id }
                  svgRef.current?.setPointerCapture(e.pointerId)
                } else {
                  setHighlighted({ cls: n.feature_class, id: n.feature_id })
                }
              }}
              onContextMenu={(e) => {
                e.preventDefault()
                e.stopPropagation()
                setHighlighted({ cls: n.feature_class, id: n.feature_id })
                setMenu({ x: e.clientX, y: e.clientY, cls: n.feature_class, id: n.feature_id, state: n.device_state ?? null })
              }}
            >
              {highlighted?.cls === n.feature_class &&
                highlighted?.id === n.feature_id &&
                (bar ? (
                  // A bus highlights as a glow ALONG the bar, not a dot at its centre.
                  <line
                    x1={bar.x0 - 3}
                    y1={p.y}
                    x2={bar.x1 + 3}
                    y2={p.y}
                    stroke={HIGHLIGHT_RING}
                    strokeOpacity={0.5}
                    strokeWidth={13}
                    strokeLinecap="round"
                  />
                ) : (
                  <circle cx={p.x} cy={p.y} r={ICON / 2 + 7} fill="none" stroke={HIGHLIGHT_RING} strokeWidth={2.5} />
                ))}
              {aRing &&
                (bar ? (
                  // Analysis overlay on a bus: a glow along the bar in the role colour.
                  <line
                    x1={bar.x0 - 3}
                    y1={p.y}
                    x2={bar.x1 + 3}
                    y2={p.y}
                    stroke={aBarrier ? BARRIER_COLOR : aBackfeed ? BACKFEED_COLOR : analysis!.color}
                    strokeOpacity={0.5}
                    strokeWidth={13}
                    strokeDasharray={aBackfeed ? "6 4" : undefined}
                    strokeLinecap="round"
                  />
                ) : (
                  // Analysis halo: red where a trace stopped / the isolation's upstream
                  // boundary, amber (dashed) for a possible back-feed, else the overlay
                  // colour. The studied device and barriers get a heavier ring.
                  <circle
                    cx={p.x}
                    cy={p.y}
                    r={ICON / 2 + 8}
                    fill="none"
                    stroke={aBarrier ? BARRIER_COLOR : aBackfeed ? BACKFEED_COLOR : analysis!.color}
                    strokeWidth={aBarrier || aStart ? 3.5 : 2.5}
                    strokeDasharray={aBackfeed ? "4 3" : undefined}
                  />
                ))}
              {n.device_state === "OPEN" && (
                <circle cx={p.x} cy={p.y} r={ICON / 2 + 4} fill="none" stroke={OPEN_RING_COLOR} strokeWidth={3} />
              )}
              {n.device_state === "CLOSED" && (
                <circle cx={p.x} cy={p.y} r={ICON / 2 + 4} fill="none" stroke={CLOSED_RING_COLOR} strokeWidth={1.6} />
              )}
              {isBus && bar ? (
                // Busbar: a thick horizontal bar spanning its taps (its wires stub onto it).
                <>
                  <line x1={bar.x0} y1={p.y} x2={bar.x1} y2={p.y} stroke={color} strokeWidth={6} strokeLinecap="round" />
                  {/* Fat transparent hit-line so the whole bar is easy to click / right-click. */}
                  <line x1={bar.x0} y1={p.y} x2={bar.x1} y2={p.y} stroke="transparent" strokeWidth={18} />
                </>
              ) : (
                <image href={icon} x={p.x - ICON / 2} y={p.y - ICON / 2} width={ICON} height={ICON} />
              )}
              {anyBadges && <NodeBadges node={pn} overlays={overlays} />}
              {showLabels && <NodeLabel node={pn} dead={dead} />}
            </g>
          )
        })}
        {/* Print legend: in the SVG (so it's in the PDF), only while the print dialog is
            open -- the live board uses the HTML legend below. */}
        {printFitBox && <PrintLegend box={printFitBox} items={legendItems} />}
      </svg>

      {/* On-screen legend: HTML (scrollable) -- the full feature set + the condition key. */}
      {showLegend && (
        <div className="bg-popover/95 absolute bottom-3 left-3 z-20 flex max-h-[60%] w-52 flex-col rounded-md border shadow-md">
          <div className="flex items-center justify-between border-b px-2 py-1">
            <span className="text-muted-foreground text-[10px] font-medium uppercase tracking-wide">Legend</span>
            <button type="button" onClick={() => setShowLegend(false)} className="hover:bg-foreground/10 rounded p-0.5" title="Hide legend">
              <X className="size-3" />
            </button>
          </div>
          <div className="min-h-0 flex-1 space-y-2 overflow-y-auto p-2">
            <div className="space-y-1">
              {CONDITION_LEGEND.map((c) => (
                <div key={c.label} className="flex items-center gap-2 text-xs">
                  <span className="size-2.5 shrink-0 rounded-full" style={{ backgroundColor: c.color }} />
                  <span className="text-muted-foreground">{c.label}</span>
                </div>
              ))}
            </div>
            {legendItems.length > 0 && (
              <div className="space-y-1 border-t pt-2">
                <div className="text-muted-foreground text-[10px] font-medium uppercase tracking-wide">Features</div>
                {legendItems.map((it) => (
                  <div key={it.code} className="flex items-center gap-2 text-xs">
                    {it.icon ? (
                      <img src={it.icon} alt="" className="size-4 shrink-0" />
                    ) : (
                      <span className="h-0.5 w-4 shrink-0 rounded" style={{ backgroundColor: it.lineColor }} />
                    )}
                    <span className="truncate">{it.name}</span>
                  </div>
                ))}
              </div>
            )}
          </div>
        </div>
      )}

      <DiagramPrintDialog
        open={printFitBox != null}
        onOpenChange={(o) => {
          if (!o) setPrintFitBox(null)
        }}
        diagramName="Operations one-line"
        svg={svgRef.current}
        fitBox={printFitBox ?? { x: 0, y: 0, w: 0, h: 0 }}
      />

      {payload.truncated && (
        <div className="bg-background/90 absolute bottom-2 left-2 rounded-md border px-2 py-1 text-xs text-amber-600">
          Network too large to lay out fully ({payload.node_count} features).
        </div>
      )}

      {/* Analysis overlay banner: what study is projected, and a way to clear it. */}
      {analysis && (
        <div className="bg-background/95 absolute left-1/2 top-2 z-10 flex -translate-x-1/2 items-center gap-2 rounded-md border px-3 py-1.5 text-xs shadow-sm">
          <span className="inline-block size-2.5 shrink-0 rounded-full" style={{ backgroundColor: analysis.color }} />
          <span className="font-medium">{analysis.kind === "isolate" ? "Isolation study" : "Trace"}</span>
          <span className="text-muted-foreground">
            {analysis.kind === "isolate"
              ? `${analysis.count} feature${analysis.count === 1 ? "" : "s"} would lose power`
              : `${analysis.count} reached`}
          </span>
          <button
            type="button"
            onClick={() => analysis.clear()}
            className="hover:bg-foreground/10 ml-1 rounded px-1.5 py-0.5 font-medium"
          >
            Clear
          </button>
        </div>
      )}

      {menu && (
        <OperateMenu
          featureClass={menu.cls}
          featureId={menu.id}
          deviceState={menu.state}
          x={menu.x}
          y={menu.y}
          isConductor={menu.isConductor}
          onClose={() => setMenu(null)}
        />
      )}

      {/* Leaving Arrange with staged (unsaved) layout changes. */}
      <AlertDialog open={exitConfirm} onOpenChange={(o) => !o && setExitConfirm(false)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Unsaved layout changes</AlertDialogTitle>
            <AlertDialogDescription>
              You have staged arrangement changes. Save them to the operations layout, or discard them?
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={savingSession}>Keep editing</AlertDialogCancel>
            <Button
              variant="outline"
              disabled={savingSession}
              onClick={() => {
                discardSession()
                setExitConfirm(false)
                setArrange(false)
              }}
            >
              Discard
            </Button>
            <AlertDialogAction
              disabled={savingSession}
              onClick={(e) => {
                e.preventDefault()
                void saveSession().then(() => {
                  setExitConfirm(false)
                  setArrange(false)
                })
              }}
            >
              Save &amp; exit
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  )
}

function ToggleBtn({
  on,
  onClick,
  title,
  children,
}: {
  on: boolean
  onClick: () => void
  title: string
  children: React.ReactNode
}) {
  return (
    <Button
      variant="ghost"
      size="icon"
      className={cn("size-7", on && "bg-accent text-accent-foreground")}
      title={title}
      onClick={onClick}
    >
      {children}
    </Button>
  )
}

/** A vertical stack of ADMS condition dots on the glyph's left edge. Alert-type
 * conditions (an active alarm, an open incident) PULSE with a radar ping so they catch
 * the eye as live events; standing-state conditions (off-normal position, a safety
 * tag) sit steady -- motion is reserved for "something is happening now". */
function NodeBadges({ node, overlays }: { node: OneLineNode; overlays: OverlaySets }) {
  const k = refKey(node.feature_class, node.feature_id)
  const conds: { color: string; label: string; pulse: boolean }[] = []
  if (overlays.alarms?.has(k)) conds.push({ color: ALARM_BADGE, label: "Active alarm", pulse: true })
  if (overlays.abnormal?.has(k)) conds.push({ color: ABNORMAL_BADGE, label: "Off-normal configuration", pulse: false })
  if (overlays.safety?.has(k)) conds.push({ color: SAFETY_BADGE, label: "Safety tag / ground applied", pulse: false })
  if (overlays.incidents?.has(k)) conds.push({ color: INCIDENT_BADGE, label: "On an open incident", pulse: true })
  if (conds.length === 0) return null
  const r = 4
  return (
    <>
      {conds.map((c, i) => {
        const cx = node.x - ICON / 2 - 3
        const cy = node.y - ICON / 2 + i * r * 2.3
        return (
          <g key={c.label}>
            {c.pulse && (
              <circle cx={cx} cy={cy} r={r} fill={c.color} opacity={0.55}>
                {/* Radar ping: expand + fade, looping -- pure SVG so it scales with zoom. */}
                <animate attributeName="r" values={`${r};${r * 2.9}`} dur="1.4s" repeatCount="indefinite" />
                <animate attributeName="opacity" values="0.55;0" dur="1.4s" repeatCount="indefinite" />
              </circle>
            )}
            <circle cx={cx} cy={cy} r={r} fill={c.color} stroke="var(--background)" strokeWidth={1}>
              {c.pulse && (
                // A gentle breathe on the solid dot too, so it reads as live even mid-ping.
                <animate attributeName="opacity" values="1;0.55;1" dur="1.4s" repeatCount="indefinite" />
              )}
              <title>{c.label}</title>
            </circle>
          </g>
        )
      })}
    </>
  )
}

function NodeLabel({ node, dead }: { node: OneLineNode; dead: boolean }) {
  const label = node.rated_kva != null ? `${fmt(node.rated_kva)} kVA` : node.demand_kw != null ? `${fmt(node.demand_kw)} kW` : null
  if (!label) return null
  return (
    <text x={node.x} y={node.y + ICON / 2 + 13} textAnchor="middle" fontSize={11} fill={dead ? DEAD_COLOR : "#334155"}>
      {label}
    </text>
  )
}

function fmt(v: number): string {
  return Number.isInteger(v) ? String(v) : v.toFixed(1)
}

/** Fallback human label for a class code when its name isn't loaded yet
 * ("mv_fuse" -> "Mv Fuse"). The real class name (useFeatureClasses) is preferred. */
function humanizeClass(code: string): string {
  return code
    .split("_")
    .map((w) => (w ? w[0].toUpperCase() + w.slice(1).toLowerCase() : w))
    .join(" ")
}

type LegendItem = { code: string; name: string; icon?: string; lineColor?: string }

/** The ADMS condition / energisation key (matches the renderer's overlay colours). */
const CONDITION_LEGEND = [
  { color: ALARM_BADGE, label: "Active alarm" },
  { color: ABNORMAL_BADGE, label: "Off-normal position" },
  { color: SAFETY_BADGE, label: "Safety tag / ground" },
  { color: INCIDENT_BADGE, label: "On an open incident" },
  { color: DEAD_COLOR, label: "De-energised" },
]

/** An SVG legend group sized to `box` (the print fit box) so it rides into the PDF --
 * the conditions key plus every feature class drawn. Rendered only while the print
 * dialog is open (so the live canvas keeps the HTML legend). */
function PrintLegend({ box, items }: { box: { x: number; y: number; w: number; h: number }; items: LegendItem[] }) {
  // Size the legend to the SMALLER of a sensible fraction of the sheet and a cap tied to
  // the symbol size, so it stays a readable corner block on a big network instead of
  // ballooning to the full height. Wrap into columns when there are many classes.
  const fs = Math.min(Math.max(box.h * 0.012, 6), ICON * 0.7)
  const rowH = fs * 1.6
  const pad = fs
  const sw = fs * 1.1
  const colW = Math.max(fs * 13, box.w * 0.12)
  const bodyRows = 1 + CONDITION_LEGEND.length + 1 + items.length // title + conditions + "Features" + items
  const maxRowsPerCol = Math.max(8, Math.floor((box.h * 0.9) / rowH))
  const cols = Math.max(1, Math.ceil(bodyRows / maxRowsPerCol))
  const rowsPerCol = Math.ceil(bodyRows / cols)
  const totalW = cols * colW + pad
  const totalH = Math.min(bodyRows, rowsPerCol) * rowH + pad * 2

  // Flatten every row into one ordered list, then lay them out column-major.
  const rows: { swatch: "title" | "header" | { color?: string; icon?: string; line?: string }; label: string; bold?: boolean }[] = [
    { swatch: "title", label: "Legend", bold: true },
    ...CONDITION_LEGEND.map((c) => ({ swatch: { color: c.color }, label: c.label })),
    { swatch: "header", label: "Features", bold: true },
    ...items.map((it) => ({ swatch: { icon: it.icon, line: it.lineColor }, label: it.name })),
  ]

  return (
    <g transform={`translate(${box.x + pad},${box.y + pad})`}>
      <rect x={-pad * 0.5} y={-pad * 0.5} width={totalW} height={totalH} rx={fs * 0.5} fill="#ffffff" stroke="#cbd5e1" strokeWidth={Math.max(fs * 0.06, 0.4)} />
      {rows.map((r, i) => {
        const col = Math.floor(i / rowsPerCol)
        const rowInCol = i % rowsPerCol
        const x = col * colW
        const y = pad + fs + rowInCol * rowH
        const textX = x + sw + fs * 0.5
        return (
          <g key={i}>
            {typeof r.swatch === "object" &&
              (r.swatch.icon ? (
                <image href={r.swatch.icon} x={x} y={y - sw + fs * 0.1} width={sw} height={sw} />
              ) : r.swatch.line ? (
                <line x1={x} y1={y - fs * 0.35} x2={x + sw} y2={y - fs * 0.35} stroke={r.swatch.line} strokeWidth={fs * 0.22} strokeLinecap="round" />
              ) : (
                <circle cx={x + sw / 2} cy={y - fs * 0.35} r={sw * 0.4} fill={r.swatch.color} />
              ))}
            <text x={r.swatch === "title" || r.swatch === "header" ? x : textX} y={y} fontSize={r.bold ? fs * 1.05 : fs} fontWeight={r.bold ? 700 : 400} fill={r.swatch === "header" ? "#64748b" : "#334155"}>
              {r.label}
            </text>
          </g>
        )
      })}
    </g>
  )
}

/** Squared distance from point p to segment [a,b] -- for picking which wire segment a
 * double-click lands on when inserting a bend. */
function distToSeg(p: [number, number], a: [number, number], b: [number, number]): number {
  const dx = b[0] - a[0]
  const dy = b[1] - a[1]
  const len2 = dx * dx + dy * dy
  let t = len2 > 0 ? ((p[0] - a[0]) * dx + (p[1] - a[1]) * dy) / len2 : 0
  t = Math.max(0, Math.min(1, t))
  const cx = a[0] + t * dx
  const cy = a[1] + t * dy
  return (p[0] - cx) ** 2 + (p[1] - cy) ** 2
}

/** Insert a new bend at `point` into a wire's intermediate vertices, on the nearest
 * segment of the full path [from, ...verts, to], returning the new intermediate list. */
function insertBend(
  from: [number, number],
  verts: [number, number][],
  to: [number, number],
  point: [number, number],
): [number, number][] {
  const full = [from, ...verts, to]
  let bestSeg = 0
  let bestD = Infinity
  for (let i = 0; i < full.length - 1; i++) {
    const dist = distToSeg(point, full[i], full[i + 1])
    if (dist < bestD) {
      bestD = dist
      bestSeg = i
    }
  }
  const nv = verts.slice()
  nv.splice(bestSeg, 0, point)
  return nv
}
