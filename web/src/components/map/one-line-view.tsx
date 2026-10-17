import { Download, Grid3x3, List, Loader2, Printer, RotateCcw, Ruler, Waypoints, X, Zap } from "lucide-react"
import { useCallback, useEffect, useMemo, useRef, useState } from "react"

import { Button } from "@/components/ui/button"
import { DiagramPrintDialog } from "@/components/map/diagram-print-dialog"
import { useAllSymbology } from "@/hooks/use-all-symbology"
import { useSymbologyIcons } from "@/hooks/use-symbology-icons"
import { type EnergizationRef, refKey, useEnergization } from "@/lib/energization"
import { downloadOneLineDxf } from "@/lib/one-line-dxf"
import { circuitColor, type OneLineConnector, type OneLineNode, type OneLinePayload, useOneLine } from "@/lib/one-line"
import { nearestSegment, type Pt, routeConnectors } from "@/lib/one-line-route"
import { resolveSymbology } from "@/lib/symbology"
import { iconDataUri } from "@/lib/symbology-icons"
import { cn } from "@/lib/utils"
import { useOneLineStore } from "@/stores/one-line-store"
import { useSelectionStore } from "@/stores/selection-store"
import { useTraceStore } from "@/stores/trace-store"

const ICON = 30 // symbol size in abstract one-line units
const PAD = 60 // padding around the content bounds
const DEFAULT_SYMBOL_COLOR = "#0f172a"
const CONNECTOR_COLOR = "#64748b"
const OPEN_RING_COLOR = "#d97706"
const CLOSED_RING_COLOR = "#16a34a"
const DEAD_COLOR = "#94a3b8"
const TRACE_COLOR = "#2563eb" // traced feature highlight (blue) -- matches the wire-handle blue
const BARRIER_COLOR = "#dc2626" // a trace barrier (where the walk stopped) -- red
const PIN_EPS = 0.5 // a wire vertex this close to a node is "pinned" to it
const ORTHO_SNAP = 12 // align-to-neighbour distance (abstract units) when Ortho is on
const GRID = 20 // grid spacing (abstract units) for snap-to-grid
const INSET = 22 // endpoint handle offset from its symbol, along the wire
const ATTACH_R = 20 // drop a wire end this close to a symbol to (re)attach it
const CLICK_SLOP = 4 // pointer movement (CSS px) under which a symbol press counts as a click, not a drag

function connectorWidth(network: string | undefined): number {
  if (network === "MV") return 3
  if (network === "LV") return 1.8
  return 2.2
}

function formatNum(v: number): string {
  return Number.isInteger(v) ? String(v) : v.toFixed(1)
}

function ratingLabel(n: OneLineNode): string | null {
  if (n.rated_kva != null) return `${formatNum(n.rated_kva)} kVA`
  if (n.demand_kw != null) return `${formatNum(n.demand_kw)} kW`
  return null
}

/** A stable per-session key for a connector (conductor id, else its index). */
function wireKeyOf(c: OneLineConnector, i: number): string {
  return c.feature_id ? `f:${c.feature_id}` : `e:${i}`
}

/** What the pointer is currently dragging. */
type DragState =
  | { kind: "node"; id: string; offsetX: number; offsetY: number }
  | { kind: "vertex"; key: string; index: number; offsetX: number; offsetY: number }
  | { kind: "legend"; offsetX: number; offsetY: number }

/** One connector after edits -- its key and resolved display path. */
type EffWire = { key: string; path: [number, number][] }

/**
 * The ephemeral one-line view: a TEMPORARY abstract single-line built from a map
 * selection or the current view (POST /schematic/one-line). Nothing is persisted.
 * It is EDITABLE in place -- drag symbols to arrange, drag a wire to bend it, drag
 * its handles, double-click a handle to drop the bend -- but nothing is ever saved;
 * edits are lost on close. Reuses the feature symbology + the energisation shading.
 */
export function OneLineView() {
  const open = useOneLineStore((s) => s.open)
  const source = useOneLineStore((s) => s.source)
  const rules = useAllSymbology()
  const icons = useSymbologyIcons()
  const svgRef = useRef<SVGSVGElement>(null)
  const [showEnergisation, setShowEnergisation] = useState(true)
  const [ortho, setOrtho] = useState(true)
  const orthoRef = useRef(ortho)
  useEffect(() => void (orthoRef.current = ortho), [ortho])
  const [snap, setSnap] = useState(false)
  const snapRef = useRef(snap)
  useEffect(() => void (snapRef.current = snap), [snap])
  const [showLegend, setShowLegend] = useState(true)
  const [showTrace, setShowTrace] = useState(true)
  const [printOpen, setPrintOpen] = useState(false)
  // The print fit box, measured from the SVG's ACTUAL rendered bounds (getBBox) at
  // the moment Print is clicked -- not the hand-computed scene.fitBox, which was
  // under-counting (far-flung disconnected nodes/the legend fell outside it, so the
  // PDF clipped them even on A0). getBBox is the true union of everything drawn.
  const [printFitBox, setPrintFitBox] = useState<{ x: number; y: number; w: number; h: number } | null>(null)

  // The active trace result (session-persisted), projected onto whatever the
  // one-line happens to be drawing: reached features get a blue halo, the nodes the
  // walk stopped at get a red one, traced conductors a blue glow -- so running a
  // trace while the schematic covers the map is still visible here. Features the
  // trace reached that aren't on this sheet simply aren't highlighted.
  const traceResult = useTraceStore((s) => s.result)
  const traceOverlay = useMemo(() => {
    if (!traceResult) return undefined
    return {
      traced: new Set(traceResult.features.map((f) => refKey(f.feature_class, f.feature_id))),
      barriers: new Set(traceResult.barriers.map((b) => refKey(b.feature_class, b.feature_id))),
      startKey: refKey(traceResult.start.feature_class, traceResult.start.feature_id),
    }
  }, [traceResult])

  const { data, isPending, isError } = useOneLine(source, open)

  // Local, non-persisted edit state. `moved` = per-symbol position overrides;
  // `wirePath`/`wirePins` = per-wire editable polylines (a vertex "pinned" to a
  // node id tracks that node, the rest are free bends). All reset when the
  // one-line changes or on Reset. Nothing here is ever saved.
  const [moved, setMoved] = useState<Record<string, Pt>>({})
  const [wirePath, setWirePath] = useState<Record<string, Pt[]>>({})
  const [wirePins, setWirePins] = useState<Record<string, (string | null)[]>>({})
  const [legendPos, setLegendPos] = useState<Pt | null>(null)
  useEffect(() => {
    setMoved({})
    setWirePath({})
    setWirePins({})
    setLegendPos(null)
  }, [data])

  const origPos = useMemo(() => {
    const m = new Map<string, Pt>()
    if (data) for (const n of data.nodes) m.set(n.feature_id, { x: n.x, y: n.y })
    return m
  }, [data])

  // The effective one-line after edits: moved nodes, wires re-routed (unedited) or
  // resolved from their editable polyline (edited). Everything downstream (SVG,
  // PDF, DXF) reads this so an exported sheet matches the arrangement on screen.
  const { effective, effWires } = useMemo((): { effective: OneLinePayload | null; effWires: EffWire[] } => {
    if (!data) return { effective: null, effWires: [] }
    const nodePosOf = (id: string): Pt | null => moved[id] ?? origPos.get(id) ?? null
    const nodes = data.nodes.map((n) => {
      const p = moved[n.feature_id]
      return p ? { ...n, x: p.x, y: p.y } : n
    })
    const auto = routeConnectors(data.connectors, nodePosOf)
    const connectors = data.connectors.map((c, i) => {
      const key = wireKeyOf(c, i)
      const stored = wirePath[key]
      if (!stored) return auto[i]
      const pins = wirePins[key] ?? []
      const path = stored.map((pt, idx): [number, number] => {
        const pin = pins[idx]
        const np = pin ? nodePosOf(pin) : null
        return np ? [np.x, np.y] : [pt.x, pt.y]
      })
      return { ...c, path }
    })
    const wires = data.connectors.map((c, i) => ({ key: wireKeyOf(c, i), path: connectors[i].path }))
    return { effective: { ...data, nodes, connectors }, effWires: wires }
  }, [data, moved, origPos, wirePath, wirePins])

  // Refs so the (stable) drag handlers read the latest state without re-binding.
  const movedRef = useRef(moved)
  const origPosRef = useRef(origPos)
  const wirePathRef = useRef(wirePath)
  const wirePinsRef = useRef(wirePins)
  const nodesRef = useRef<OneLineNode[]>([])
  const effWiresRef = useRef<EffWire[]>([])
  // Click-vs-drag on a symbol OR a wire: a press that never moves past CLICK_SLOP is
  // a selection (open the feature panel), not a reposition/reshape. downPt is the
  // pointer's client position at press; movedPastSlop flips once it travels far
  // enough. Both are set by beginDrag so every grab is tracked uniformly.
  const downPtRef = useRef<{ x: number; y: number } | null>(null)
  const movedPastSlopRef = useRef(false)
  // Set by onWireDown (and ONLY there): records that the current vertex drag began
  // as a wire GRAB, so endDrag can tell a wire reshape (moved) from a wire click
  // (didn't) -- a click reverts the bend onWireDown speculatively inserted and
  // selects the conductor instead. insertedAt is that bend's index; wasFresh means
  // the wire had no editable polyline before, so reverting deletes the whole entry.
  const wireGrabRef = useRef<{ key: string; insertedAt: number; wasFresh: boolean } | null>(null)
  // key -> the conductor feature behind a wire (only wires that ARE features; plain
  // links have none), so a wire click can resolve what to select. Kept in sync below.
  const wireFeatureRef = useRef<Record<string, { featureClass: string; featureId: string }>>({})
  useEffect(() => void (movedRef.current = moved), [moved])
  useEffect(() => void (origPosRef.current = origPos), [origPos])
  useEffect(() => void (wirePathRef.current = wirePath), [wirePath])
  useEffect(() => void (wirePinsRef.current = wirePins), [wirePins])
  useEffect(() => void (nodesRef.current = effective?.nodes ?? []), [effective])
  useEffect(() => void (effWiresRef.current = effWires), [effWires])
  useEffect(() => {
    const map: Record<string, { featureClass: string; featureId: string }> = {}
    if (data) {
      data.connectors.forEach((c, i) => {
        if (c.feature_class && c.feature_id) map[wireKeyOf(c, i)] = { featureClass: c.feature_class, featureId: c.feature_id }
      })
    }
    wireFeatureRef.current = map
  }, [data])

  const dragRef = useRef<DragState | null>(null)
  const rafRef = useRef<number | null>(null)
  const lastPtRef = useRef<Pt | null>(null)
  const [dragging, setDragging] = useState(false)

  const clientToSvg = useCallback((e: React.PointerEvent): Pt | null => {
    const svg = svgRef.current
    if (!svg) return null
    const ctm = svg.getScreenCTM()
    if (!ctm) return null
    const pt = svg.createSVGPoint()
    pt.x = e.clientX
    pt.y = e.clientY
    const p = pt.matrixTransform(ctm.inverse())
    return { x: p.x, y: p.y }
  }, [])

  // The node whose symbol sits within `r` of (x,y). PIN_EPS finds the exact node a
  // wire endpoint rests on; ATTACH_R is the looser drop target for (re)attaching.
  const nodeIdNear = useCallback((x: number, y: number, r: number): string | null => {
    let best: string | null = null
    let bestD = r
    for (const n of nodesRef.current) {
      const d = Math.hypot(n.x - x, n.y - y)
      if (d <= bestD) {
        bestD = d
        best = n.feature_id
      }
    }
    return best
  }, [])
  const nodeIdAt = useCallback((x: number, y: number) => nodeIdNear(x, y, PIN_EPS), [nodeIdNear])

  // The on-screen position of a wire vertex (a pinned one tracks its node).
  const resolveVertexPos = useCallback((key: string, idx: number): Pt | null => {
    const arr = wirePathRef.current[key]
    if (!arr || idx < 0 || idx >= arr.length) return null
    const pin = wirePinsRef.current[key]?.[idx]
    if (pin) return movedRef.current[pin] ?? origPosRef.current.get(pin) ?? arr[idx]
    return arr[idx]
  }, [])

  // One rAF-coalesced commit of the in-flight drag, so a fast pointer re-renders
  // at most once per frame (keeps dragging smooth). With Ortho on, a dragged point
  // snaps to line up with its neighbours (symbols -> other symbols; wire vertices
  // -> their adjacent vertices), so segments stay horizontal/vertical.
  const applyDrag = useCallback(() => {
    rafRef.current = null
    const t = dragRef.current
    const p = lastPtRef.current
    if (!t || !p) return
    let nx = p.x + t.offsetX
    let ny = p.y + t.offsetY
    if (t.kind === "legend") {
      if (snapRef.current) {
        nx = Math.round(nx / GRID) * GRID
        ny = Math.round(ny / GRID) * GRID
      }
      setLegendPos({ x: nx, y: ny })
      return
    }
    if (t.kind === "node") {
      if (orthoRef.current) {
        for (const o of nodesRef.current) {
          if (o.feature_id === t.id) continue
          if (Math.abs(o.x - nx) < ORTHO_SNAP) nx = o.x
          if (Math.abs(o.y - ny) < ORTHO_SNAP) ny = o.y
        }
      }
      if (snapRef.current) {
        nx = Math.round(nx / GRID) * GRID
        ny = Math.round(ny / GRID) * GRID
      }
      setMoved((m) => ({ ...m, [t.id]: { x: nx, y: ny } }))
    } else {
      if (orthoRef.current) {
        for (const nb of [resolveVertexPos(t.key, t.index - 1), resolveVertexPos(t.key, t.index + 1)]) {
          if (!nb) continue
          if (Math.abs(nb.x - nx) < ORTHO_SNAP) nx = nb.x
          if (Math.abs(nb.y - ny) < ORTHO_SNAP) ny = nb.y
        }
      }
      if (snapRef.current) {
        nx = Math.round(nx / GRID) * GRID
        ny = Math.round(ny / GRID) * GRID
      }
      setWirePath((w) => {
        const arr = w[t.key]
        if (!arr) return w
        const next = [...arr]
        next[t.index] = { x: nx, y: ny }
        return { ...w, [t.key]: next }
      })
    }
  }, [resolveVertexPos])

  const beginDrag = useCallback(
    (state: DragState, e: React.PointerEvent) => {
      dragRef.current = state
      // Arm click-vs-drag tracking for every grab (node, wire, vertex, endpoint,
      // legend). onWireDown re-sets wireGrabRef right after; everyone else leaves it
      // null, so only a wire grab is a candidate wire-select.
      downPtRef.current = { x: e.clientX, y: e.clientY }
      movedPastSlopRef.current = false
      wireGrabRef.current = null
      setDragging(true)
      svgRef.current?.setPointerCapture?.(e.pointerId)
    },
    [],
  )

  const onNodeDown = useCallback(
    (id: string, e: React.PointerEvent) => {
      e.stopPropagation()
      const p = clientToSvg(e)
      const cur = movedRef.current[id] ?? origPosRef.current.get(id)
      if (!p || !cur) return
      beginDrag({ kind: "node", id, offsetX: cur.x - p.x, offsetY: cur.y - p.y }, e)
    },
    [clientToSvg, beginDrag],
  )

  const onVertexDown = useCallback(
    (key: string, index: number, e: React.PointerEvent) => {
      e.stopPropagation()
      const p = clientToSvg(e)
      const cur = wirePathRef.current[key]?.[index]
      if (!p || !cur) return
      beginDrag({ kind: "vertex", key, index, offsetX: cur.x - p.x, offsetY: cur.y - p.y }, e)
    },
    [clientToSvg, beginDrag],
  )

  // Grab a wire body: insert a bend at the grab point (promoting the wire to an
  // editable polyline the first time) and drag it -- this is "reshape the line".
  const onWireDown = useCallback(
    (key: string, e: React.PointerEvent) => {
      e.stopPropagation()
      const p = clientToSvg(e)
      const wire = effWiresRef.current.find((w) => w.key === key)
      if (!p || !wire) return
      const seg = nearestSegment(wire.path, p)
      // Whether this wire already had an editable polyline decides how a no-move
      // click is reverted later (delete the whole entry vs splice out this one bend).
      const wasFresh = !wirePathRef.current[key]
      let pts: Pt[]
      let pins: (string | null)[]
      if (wirePathRef.current[key]) {
        pts = wirePathRef.current[key].map((pt) => ({ ...pt }))
        pins = [...(wirePinsRef.current[key] ?? wire.path.map(() => null))]
      } else {
        pts = wire.path.map(([x, y]) => ({ x, y }))
        pins = wire.path.map(([x, y]) => nodeIdAt(x, y))
      }
      const at = seg.index + 1
      pts.splice(at, 0, { x: seg.point.x, y: seg.point.y })
      pins.splice(at, 0, null)
      setWirePath((w) => ({ ...w, [key]: pts }))
      setWirePins((w) => ({ ...w, [key]: pins }))
      beginDrag({ kind: "vertex", key, index: at, offsetX: seg.point.x - p.x, offsetY: seg.point.y - p.y }, e)
      // Mark this as a WIRE grab (beginDrag cleared it): if the pointer never moves,
      // endDrag reverts the speculative bend above and selects the conductor instead.
      wireGrabRef.current = { key, insertedAt: at, wasFresh }
    },
    [clientToSvg, beginDrag, nodeIdAt],
  )

  // Grab a pinned endpoint handle: DETACH the wire end from its symbol (unpin) and
  // drag it. Dropping it near a symbol re-attaches (see endDrag). `handlePos` is
  // the handle's inset position, so the end starts there, not jumping to the cursor.
  const onEndpointDown = useCallback(
    (key: string, index: number, handlePos: Pt, e: React.PointerEvent) => {
      e.stopPropagation()
      const p = clientToSvg(e)
      if (!p) return
      setWirePins((w) => {
        const arr = [...(w[key] ?? [])]
        arr[index] = null
        return { ...w, [key]: arr }
      })
      setWirePath((w) => {
        const arr = [...(w[key] ?? [])]
        arr[index] = { x: handlePos.x, y: handlePos.y }
        return { ...w, [key]: arr }
      })
      beginDrag({ kind: "vertex", key, index, offsetX: handlePos.x - p.x, offsetY: handlePos.y - p.y }, e)
    },
    [clientToSvg, beginDrag],
  )

  // Drag the legend box anywhere on the sheet. `boxPos` is its current top-left.
  const onLegendDown = useCallback(
    (boxPos: Pt, e: React.PointerEvent) => {
      e.stopPropagation()
      const p = clientToSvg(e)
      if (!p) return
      beginDrag({ kind: "legend", offsetX: boxPos.x - p.x, offsetY: boxPos.y - p.y }, e)
    },
    [clientToSvg, beginDrag],
  )

  const onVertexDelete = useCallback((key: string, index: number) => {
    setWirePath((w) => {
      const arr = w[key]
      if (!arr) return w
      const next = [...arr]
      next.splice(index, 1)
      return { ...w, [key]: next }
    })
    setWirePins((w) => {
      const arr = w[key]
      if (!arr) return w
      const next = [...arr]
      next.splice(index, 1)
      return { ...w, [key]: next }
    })
  }, [])

  const onPointerMove = useCallback(
    (e: React.PointerEvent) => {
      if (!dragRef.current) return
      // Once the pointer travels past the slop, this press is a real drag, not a
      // click -- so endDrag won't treat its release as a selection.
      const down = downPtRef.current
      if (down && Math.hypot(e.clientX - down.x, e.clientY - down.y) > CLICK_SLOP) {
        movedPastSlopRef.current = true
      }
      const p = clientToSvg(e)
      if (!p) return
      lastPtRef.current = p
      if (rafRef.current == null) rafRef.current = requestAnimationFrame(applyDrag)
    },
    [clientToSvg, applyDrag],
  )

  const endDrag = useCallback(() => {
    const t = dragRef.current
    // A symbol pressed without dragging is a SELECTION: open that feature in the
    // map's feature panel (which floats above this overlay) so the user can read
    // its attributes while arranging the sheet. The panel is transient and never
    // part of the print. Resolve the class off the node id (the drag state carries
    // only the id).
    if (t && t.kind === "node" && !movedPastSlopRef.current) {
      const n = nodesRef.current.find((node) => node.feature_id === t.id)
      if (n) useSelectionStore.getState().select(n.feature_class, n.feature_id)
    }
    // A conductor pressed without dragging is also a SELECTION: onWireDown
    // speculatively inserted a bend to start a reshape -- undo it (delete the whole
    // editable polyline if the wire was previously un-edited, else splice out just
    // this bend) and open the conductor in the feature panel instead.
    const grab = wireGrabRef.current
    if (t && t.kind === "vertex" && !movedPastSlopRef.current && grab) {
      if (grab.wasFresh) {
        setWirePath((w) => {
          const { [grab.key]: _drop, ...rest } = w
          return rest
        })
        setWirePins((w) => {
          const { [grab.key]: _drop, ...rest } = w
          return rest
        })
      } else {
        const splice = (arr: Pt[] | (string | null)[] | undefined) =>
          arr ? [...arr.slice(0, grab.insertedAt), ...arr.slice(grab.insertedAt + 1)] : arr
        setWirePath((w) => (w[grab.key] ? { ...w, [grab.key]: splice(w[grab.key]) as Pt[] } : w))
        setWirePins((w) => (w[grab.key] ? { ...w, [grab.key]: splice(w[grab.key]) as (string | null)[] } : w))
      }
      const ref = wireFeatureRef.current[grab.key]
      if (ref) useSelectionStore.getState().select(ref.featureClass, ref.featureId)
    } else if (t && t.kind === "vertex" && movedPastSlopRef.current) {
      // A real reshape drop: snap the dragged vertex onto a symbol it lands on ->
      // (re)attach, so the end pins to that node and tracks it thereafter.
      const pos = wirePathRef.current[t.key]?.[t.index]
      if (pos) {
        const hit = nodeIdNear(pos.x, pos.y, ATTACH_R)
        if (hit) {
          setWirePins((w) => {
            const arr = [...(w[t.key] ?? [])]
            arr[t.index] = hit
            return { ...w, [t.key]: arr }
          })
        }
      }
    }
    downPtRef.current = null
    wireGrabRef.current = null
    dragRef.current = null
    if (rafRef.current != null) {
      cancelAnimationFrame(rafRef.current)
      rafRef.current = null
    }
    setDragging(false)
  }, [nodeIdNear])

  const editedWire = useCallback((key: string) => wirePathRef.current[key] != null, [])
  const pinnedVertex = useCallback((key: string, index: number) => (wirePinsRef.current[key]?.[index] ?? null) != null, [])

  const interaction = useMemo<Interaction>(
    () => ({ onNodeDown, onWireDown, onVertexDown, onEndpointDown, onVertexDelete, onLegendDown, editedWire, pinnedVertex }),
    [onNodeDown, onWireDown, onVertexDown, onEndpointDown, onVertexDelete, onLegendDown, editedWire, pinnedVertex],
  )

  const energRefs: EnergizationRef[] = useMemo(() => {
    if (!data) return []
    const refs: EnergizationRef[] = data.nodes.map((n) => ({ feature_class: n.feature_class, feature_id: n.feature_id }))
    for (const c of data.connectors) {
      if (c.feature_class && c.feature_id) refs.push({ feature_class: c.feature_class, feature_id: c.feature_id })
    }
    return refs
  }, [data])
  const energKey = useMemo(() => energRefs.map((r) => r.feature_id).join(","), [energRefs])
  const { data: deadSet } = useEnergization(energKey || null, energRefs, open && showEnergisation && energRefs.length > 0)

  const scene = useMemo(() => {
    if (!effective || effective.truncated || effective.nodes.length === 0) return null
    return buildScene(
      effective.nodes,
      effective.connectors,
      rules,
      icons,
      showEnergisation ? deadSet : undefined,
      interaction,
      showLegend,
      legendPos,
      showTrace ? traceOverlay : undefined,
    )
  }, [effective, rules, icons, deadSet, showEnergisation, interaction, showLegend, legendPos, showTrace, traceOverlay])

  if (!open) return null

  const edited = Object.keys(moved).length > 0 || Object.keys(wirePath).length > 0 || legendPos != null

  return (
    <div className="absolute inset-0 z-20 flex flex-col bg-slate-50">
      <div className="bg-background/95 flex items-center gap-2 border-b px-3 py-1.5 shadow-sm">
        <span className="text-sm font-semibold">One-line</span>
        <span className="text-muted-foreground text-[11px]">
          {data && !data.truncated
            ? `${data.node_count} feature${data.node_count === 1 ? "" : "s"} · drag symbols or wires · temporary`
            : "temporary"}
        </span>
        <div className="ml-auto flex items-center gap-1">
          {edited && (
            <Button
              variant="ghost"
              size="sm"
              className="h-7 px-2"
              onClick={() => {
                setMoved({})
                setWirePath({})
                setWirePins({})
                setLegendPos(null)
              }}
              title="Reset symbols, wires and legend to the generated layout"
            >
              <RotateCcw className="size-3.5" /> Reset
            </Button>
          )}
          <Button
            variant="ghost"
            size="sm"
            className={cn("h-7 px-2", ortho && "bg-accent text-accent-foreground")}
            onClick={() => setOrtho((v) => !v)}
            title="Ortho: snap dragging to line up with neighbours (right angles)"
          >
            <Ruler className="size-3.5" /> Ortho
          </Button>
          <Button
            variant="ghost"
            size="sm"
            className={cn("h-7 px-2", snap && "bg-accent text-accent-foreground")}
            onClick={() => setSnap((v) => !v)}
            title="Snap dragging to a grid"
          >
            <Grid3x3 className="size-3.5" /> Snap
          </Button>
          <Button
            variant="ghost"
            size="sm"
            className={cn("h-7 px-2", showLegend && "bg-accent text-accent-foreground")}
            onClick={() => setShowLegend((v) => !v)}
            title="Show the legend"
          >
            <List className="size-3.5" /> Legend
          </Button>
          <Button
            variant="ghost"
            size="sm"
            className={cn("h-7 px-2", showEnergisation && "bg-accent text-accent-foreground")}
            onClick={() => setShowEnergisation((v) => !v)}
            title="Shade de-energised features (below an open device)"
          >
            <Zap className="size-3.5" /> Live
          </Button>
          {traceOverlay && (
            <Button
              variant="ghost"
              size="sm"
              className={cn("h-7 px-2", showTrace && "bg-accent text-accent-foreground")}
              onClick={() => setShowTrace((v) => !v)}
              title="Highlight the active trace result on the schematic"
            >
              <Waypoints className="size-3.5" /> Trace
            </Button>
          )}
          <Button
            variant="secondary"
            size="sm"
            className="h-7 shadow-sm"
            disabled={!scene}
            onClick={() => {
              const el = svgRef.current
              // Union each direct child's bbox rather than el.getBBox() on the root
              // <svg> (which is unreliable/empty in some engines). This is the true
              // extent of everything drawn -- every symbol, wire, label, and the
              // legend -- so the print fits (and can't clip) all of it.
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
                const PRINT_PAD = 24
                setPrintFitBox(
                  Number.isFinite(x0)
                    ? { x: x0 - PRINT_PAD, y: y0 - PRINT_PAD, w: x1 - x0 + 2 * PRINT_PAD, h: y1 - y0 + 2 * PRINT_PAD }
                    : null,
                )
              }
              setPrintOpen(true)
            }}
            title="Preview and export to PDF"
          >
            <Printer className="size-3.5" /> PDF
          </Button>
          <Button
            variant="secondary"
            size="sm"
            className="h-7 shadow-sm"
            disabled={!effective || effective.truncated || effective.nodes.length === 0}
            onClick={() => effective && downloadOneLineDxf(effective, rules, icons)}
            title="Export to DXF (open in CAD)"
          >
            <Download className="size-3.5" /> DXF
          </Button>
          <Button
            variant="ghost"
            size="icon"
            className="size-7"
            onClick={() => useOneLineStore.getState().close()}
            title="Close"
          >
            <X className="size-4" />
            <span className="sr-only">Close one-line</span>
          </Button>
        </div>
      </div>

      <div className="relative min-h-0 flex-1">
        {isError ? (
          <Centered>Couldn’t build the one-line.</Centered>
        ) : data?.truncated ? (
          <Centered>Too many features — select a smaller set or zoom in.</Centered>
        ) : !data && isPending ? (
          <Centered>
            <Loader2 className="size-4 animate-spin" /> Building one-line…
          </Centered>
        ) : data && data.nodes.length === 0 ? (
          <Centered>Nothing to draw — the selection has no placeable features.</Centered>
        ) : scene ? (
          <svg
            ref={svgRef}
            className="h-full w-full"
            viewBox={scene.viewBox}
            preserveAspectRatio="xMidYMid meet"
            onPointerMove={onPointerMove}
            onPointerUp={endDrag}
            onPointerCancel={endDrag}
            style={{
              fontFamily: "ui-sans-serif, system-ui, sans-serif",
              touchAction: "none",
              userSelect: "none",
              WebkitUserSelect: "none",
              cursor: dragging ? "grabbing" : "default",
            }}
          >
            {snap && (
              <>
                <defs>
                  <pattern id="ol-grid" width={GRID} height={GRID} patternUnits="userSpaceOnUse">
                    <path d={`M ${GRID} 0 L 0 0 0 ${GRID}`} fill="none" stroke="#e2e8f0" strokeWidth={1} />
                  </pattern>
                </defs>
                <rect x={scene.fitBox.x} y={scene.fitBox.y} width={scene.fitBox.w} height={scene.fitBox.h} fill="url(#ol-grid)" />
              </>
            )}
            {scene.connectors}
            {scene.symbols}
            {scene.overlays}
            {scene.legend}
          </svg>
        ) : null}
      </div>
      {/* Print preview -- the SAME framed-sheet dialog diagrams use (paper size +
          orientation, live preview, download on confirm). buildSheetSvg clones the
          live svg and strips data-print-hidden, so the trace overlay never shows in
          the preview OR the PDF. */}
      <DiagramPrintDialog
        open={printOpen}
        onOpenChange={setPrintOpen}
        diagramName="One-line"
        svg={svgRef.current}
        fitBox={printFitBox ?? scene?.fitBox ?? { x: 0, y: 0, w: 0, h: 0 }}
      />
    </div>
  )
}

function Centered({ children }: { children: React.ReactNode }) {
  return (
    <div className="text-muted-foreground absolute inset-0 flex items-center justify-center gap-2 text-sm">
      {children}
    </div>
  )
}

interface Interaction {
  onNodeDown: (id: string, e: React.PointerEvent) => void
  onWireDown: (key: string, e: React.PointerEvent) => void
  onVertexDown: (key: string, index: number, e: React.PointerEvent) => void
  onEndpointDown: (key: string, index: number, handlePos: Pt, e: React.PointerEvent) => void
  onVertexDelete: (key: string, index: number) => void
  onLegendDown: (boxPos: Pt, e: React.PointerEvent) => void
  editedWire: (key: string) => boolean
  pinnedVertex: (key: string, index: number) => boolean
}

interface Scene {
  connectors: React.ReactNode[]
  symbols: React.ReactNode[]
  overlays: React.ReactNode[]
  legend: React.ReactNode
  viewBox: string
  fitBox: { x: number; y: number; w: number; h: number }
}

/** The active trace result projected onto the one-line: which drawn features the
 * trace reached (traced), which it stopped at (barriers), and the start, all by
 * refKey. Undefined when no trace is shown. */
interface TraceOverlay {
  traced: Set<string>
  barriers: Set<string>
  startKey?: string
}

function buildScene(
  nodes: OneLineNode[],
  connectors: OneLineConnector[],
  rules: Record<string, Parameters<typeof resolveSymbology>[0]>,
  icons: ReturnType<typeof useSymbologyIcons>,
  deadSet: Set<string> | undefined,
  interaction: Interaction,
  withLegend: boolean,
  legendPos: Pt | null,
  trace: TraceOverlay | undefined,
): Scene {
  // Content bounds over every drawn point.
  let minX = Infinity
  let minY = Infinity
  let maxX = -Infinity
  let maxY = -Infinity
  const extend = (x: number, y: number) => {
    if (x < minX) minX = x
    if (y < minY) minY = y
    if (x > maxX) maxX = x
    if (y > maxY) maxY = y
  }
  for (const n of nodes) {
    extend(n.x - ICON, n.y - ICON)
    extend(n.x + ICON, n.y + ICON)
  }
  for (const c of connectors) {
    for (const [x, y] of c.path) extend(x, y)
  }
  if (!isFinite(minX)) {
    minX = 0
    minY = 0
    maxX = 100
    maxY = 100
  }

  const connectorNodes: React.ReactNode[] = []
  const overlays: React.ReactNode[] = []
  connectors.forEach((c, i) => {
    const key = wireKeyOf(c, i)
    const dead = c.feature_class && c.feature_id && deadSet?.has(refKey(c.feature_class, c.feature_id))
    const traced = c.feature_class && c.feature_id && trace?.traced.has(refKey(c.feature_class, c.feature_id))
    const color = dead ? DEAD_COLOR : (circuitColor(c.circuit_ref) ?? CONNECTOR_COLOR)
    const d = c.path.map(([x, y], j) => `${j === 0 ? "M" : "L"} ${x} ${y}`).join(" ")
    // A fat translucent glow UNDER a traced conductor so the trace reads at a glance
    // while the wire keeps its own circuit colour on top.
    if (traced) {
      connectorNodes.push(
        <path
          key={`t${i}`}
          data-print-hidden
          d={d}
          fill="none"
          stroke={TRACE_COLOR}
          strokeOpacity={0.45}
          strokeWidth={connectorWidth(c.network) + 6}
          strokeLinecap="round"
        />,
      )
    }
    connectorNodes.push(
      <path
        key={`c${i}`}
        d={d}
        fill="none"
        stroke={color}
        strokeWidth={connectorWidth(c.network)}
        strokeDasharray={dead ? "6 4" : undefined}
      />,
    )
    // A fat, invisible hit-area so a thin wire is easy to grab and bend.
    connectorNodes.push(
      <path
        key={`hit${i}`}
        d={d}
        fill="none"
        stroke="transparent"
        strokeWidth={14}
        style={{ cursor: "crosshair", pointerEvents: "stroke" }}
        onPointerDown={(e) => interaction.onWireDown(key, e)}
      />,
    )
    // Once a wire is edited, expose its handles: round blue for free bends (drag to
    // move, double-click to drop), amber square for endpoints pinned to a symbol
    // (drag to detach/re-route; drop on a symbol to re-attach). Endpoint handles sit
    // just off their symbol, along the wire, so they don't block dragging the symbol.
    if (interaction.editedWire(key)) {
      c.path.forEach(([x, y], vi) => {
        if (interaction.pinnedVertex(key, vi)) {
          const nb = c.path[vi + 1] ?? c.path[vi - 1]
          if (!nb) return
          const dx = nb[0] - x
          const dy = nb[1] - y
          const len = Math.hypot(dx, dy) || 1
          const hx = x + (dx / len) * INSET
          const hy = y + (dy / len) * INSET
          overlays.push(
            <rect
              key={`e${key}:${vi}`}
              x={hx - 4.5}
              y={hy - 4.5}
              width={9}
              height={9}
              fill="#fffbeb"
              stroke={OPEN_RING_COLOR}
              strokeWidth={1.5}
              style={{ cursor: "grab" }}
              onPointerDown={(e) => interaction.onEndpointDown(key, vi, { x: hx, y: hy }, e)}
            >
              <title>Drag to detach this end · drop on a symbol to re-attach</title>
            </rect>,
          )
          return
        }
        overlays.push(
          <circle
            key={`h${key}:${vi}`}
            cx={x}
            cy={y}
            r={4}
            fill="#ffffff"
            stroke="#2563eb"
            strokeWidth={1.5}
            style={{ cursor: "move" }}
            onPointerDown={(e) => interaction.onVertexDown(key, vi, e)}
            onDoubleClick={() => interaction.onVertexDelete(key, vi)}
          >
            <title>Drag to bend · double-click to remove</title>
          </circle>,
        )
      })
    }
  })

  const symbols: React.ReactNode[] = []
  for (const n of nodes) {
    const attrs: Record<string, unknown> = {}
    if (n.device_state != null) attrs.device_state = n.device_state
    if (n.circuit_ref != null) attrs.circuit_ref = n.circuit_ref
    const classRules = rules[n.feature_class]
    const def =
      resolveSymbology(classRules, n.subtype, attrs, "diagram") ??
      resolveSymbology(classRules, n.subtype, attrs, "map")
    const dead = deadSet?.has(refKey(n.feature_class, n.feature_id)) ?? false
    const color = dead ? DEAD_COLOR : (def?.color ?? DEFAULT_SYMBOL_COLOR)
    const icon = iconDataUri(def?.icon_ref, color, icons)
    const label = ratingLabel(n)
    const tk = refKey(n.feature_class, n.feature_id)
    const isBarrier = trace?.barriers.has(tk) ?? false
    const isTraced = trace?.traced.has(tk) ?? false
    const isStart = trace?.startKey === tk
    symbols.push(
      <g
        key={`n${n.feature_id}`}
        opacity={dead ? 0.6 : 1}
        style={{ cursor: "grab" }}
        onPointerDown={(e) => interaction.onNodeDown(n.feature_id, e)}
      >
        {(isTraced || isBarrier) && (
          // Outer halo ring: blue for a reached feature (red where the trace
          // stopped), a hair larger than the open/closed device rings so both read.
          // data-print-hidden: the trace is an on-screen GUIDE, stripped from the PDF.
          <circle
            data-print-hidden
            cx={n.x}
            cy={n.y}
            r={ICON / 2 + 8}
            fill="none"
            stroke={isBarrier ? BARRIER_COLOR : TRACE_COLOR}
            strokeWidth={isBarrier || isStart ? 3.5 : 2.5}
          />
        )}
        {n.device_state === "OPEN" && (
          <circle cx={n.x} cy={n.y} r={ICON / 2 + 4} fill="none" stroke={OPEN_RING_COLOR} strokeWidth={3} />
        )}
        {n.device_state === "CLOSED" && (
          <circle cx={n.x} cy={n.y} r={ICON / 2 + 4} fill="none" stroke={CLOSED_RING_COLOR} strokeWidth={1.6} />
        )}
        <image href={icon} x={n.x - ICON / 2} y={n.y - ICON / 2} width={ICON} height={ICON} />
        {label && (
          <text x={n.x} y={n.y + ICON / 2 + 13} textAnchor="middle" fontSize={11} fill={dead ? DEAD_COLOR : "#334155"}>
            {label}
          </text>
        )}
      </g>,
    )
  }

  // Legend, placed just left of the content so it never overlaps the drawing and
  // rides along into the PDF/DXF-matching view. Built from what's actually drawn.
  let legend: React.ReactNode = null
  if (withLegend) {
    const built = buildLegend(nodes, connectors, rules, icons, deadSet, minX, minY, legendPos, interaction.onLegendDown)
    if (built) {
      legend = built.node
      extend(built.box.x, built.box.y)
      extend(built.box.x + built.box.w, built.box.y + built.box.h)
    }
  }

  const x = minX - PAD
  const y = minY - PAD
  const w = maxX - minX + 2 * PAD
  const h = maxY - minY + 2 * PAD
  return { connectors: connectorNodes, symbols, overlays, legend, viewBox: `${x} ${y} ${w} ${h}`, fitBox: { x, y, w, h } }
}

/**
 * A compact legend keyed off what's on the sheet: each distinct feature class
 * (its symbol glyph + code), each circuit colour in use, MV/LV line weights, and
 * the de-energised style when any feature is shaded. Drawn in SVG units just left
 * of the content so it exports with the sheet.
 */
function buildLegend(
  nodes: OneLineNode[],
  connectors: OneLineConnector[],
  rules: Record<string, Parameters<typeof resolveSymbology>[0]>,
  icons: ReturnType<typeof useSymbologyIcons>,
  deadSet: Set<string> | undefined,
  contentMinX: number,
  contentMinY: number,
  override: Pt | null,
  onLegendDown: (boxPos: Pt, e: React.PointerEvent) => void,
): { node: React.ReactNode; box: { x: number; y: number; w: number; h: number } } | null {
  type Row =
    | { kind: "glyph"; icon: string; label: string }
    | { kind: "swatch"; color: string; label: string; width: number; dashed?: boolean }
  const rows: Row[] = []

  // Distinct feature classes (first symbol wins), capped so the legend stays small.
  const seenClass = new Set<string>()
  for (const n of nodes) {
    if (seenClass.has(n.feature_class)) continue
    seenClass.add(n.feature_class)
    const def =
      resolveSymbology(rules[n.feature_class], n.subtype, {}, "diagram") ??
      resolveSymbology(rules[n.feature_class], n.subtype, {}, "map")
    rows.push({ kind: "glyph", icon: iconDataUri(def?.icon_ref, def?.color ?? DEFAULT_SYMBOL_COLOR, icons), label: n.feature_class })
    if (seenClass.size >= 12) break
  }

  // Distinct circuits in use.
  const seenCircuit = new Set<string>()
  for (const c of connectors) {
    if (!c.circuit_ref || seenCircuit.has(c.circuit_ref)) continue
    seenCircuit.add(c.circuit_ref)
    rows.push({ kind: "swatch", color: circuitColor(c.circuit_ref) ?? CONNECTOR_COLOR, label: c.circuit_ref, width: 3 })
    if (seenCircuit.size >= 8) break
  }

  // Network line weights present.
  const networks = new Set(connectors.map((c) => c.network).filter(Boolean) as string[])
  if (networks.has("MV")) rows.push({ kind: "swatch", color: CONNECTOR_COLOR, label: "MV line", width: connectorWidth("MV") })
  if (networks.has("LV")) rows.push({ kind: "swatch", color: CONNECTOR_COLOR, label: "LV line", width: connectorWidth("LV") })

  // De-energised style, if anything is shaded.
  const anyDead = deadSet && (nodes.some((n) => deadSet.has(refKey(n.feature_class, n.feature_id))) ||
    connectors.some((c) => c.feature_class && c.feature_id && deadSet.has(refKey(c.feature_class, c.feature_id))))
  if (anyDead) rows.push({ kind: "swatch", color: DEAD_COLOR, label: "De-energised", width: 2.2, dashed: true })

  if (rows.length === 0) return null

  const ROW = 20
  const PADX = 10
  const HEADER = 22
  const W = 180
  const H = HEADER + rows.length * ROW + 8
  // Default placement is just left of the content; a user drag overrides it.
  const box = { x: override?.x ?? contentMinX - 48 - W, y: override?.y ?? contentMinY, w: W, h: H }

  const node = (
    <g key="legend" style={{ cursor: "grab" }} onPointerDown={(e) => onLegendDown({ x: box.x, y: box.y }, e)}>
      <rect x={box.x} y={box.y} width={W} height={H} rx={6} fill="#ffffff" stroke="#cbd5e1" strokeWidth={1} />
      <title>Drag to move the legend</title>
      <text x={box.x + PADX} y={box.y + 15} fontSize={12} fontWeight={600} fill="#0f172a">
        Legend
      </text>
      {rows.map((r, i) => {
        const cy = box.y + HEADER + i * ROW + ROW / 2
        return (
          <g key={`lr${i}`}>
            {r.kind === "glyph" ? (
              <image href={r.icon} x={box.x + PADX} y={cy - 8} width={16} height={16} />
            ) : (
              <line
                x1={box.x + PADX}
                y1={cy}
                x2={box.x + PADX + 16}
                y2={cy}
                stroke={r.color}
                strokeWidth={r.width}
                strokeDasharray={r.dashed ? "4 3" : undefined}
              />
            )}
            <text x={box.x + PADX + 24} y={cy + 4} fontSize={11} fill="#334155">
              {r.label.length > 22 ? `${r.label.slice(0, 21)}…` : r.label}
            </text>
          </g>
        )
      })}
    </g>
  )
  return { node, box }
}
