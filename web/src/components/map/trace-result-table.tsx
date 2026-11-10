import { useEffect, useRef, useState, type ReactNode } from "react"
import { ChevronRight, GripHorizontal, List, ListTree, RefreshCw, Scissors, X } from "lucide-react"

import { AnalysisExplanation } from "@/components/map/analysis-explanation"
import { FeatureInfoButton } from "@/components/map/feature-info-button"
import { LoopFixContent } from "@/components/map/loop-fix-panel"
import { Button } from "@/components/ui/button"
import { ServiceRollupSummary } from "@/components/map/service-rollup-summary"
import { useFeatureClasses } from "@/hooks/use-feature-classes"
import { ApiError, apiFetch } from "@/lib/api"
import { HIGHLIGHT_COLORS } from "@/lib/map-layers"
import { boundaryRoleMap, nearestBoundary, type SectionNode } from "@/lib/network-section"
import { cn } from "@/lib/utils"
import { useDiagramStore } from "@/stores/diagram-store"
import { useSelectionStore } from "@/stores/selection-store"
import { type TraceResult, useTraceStore } from "@/stores/trace-store"
import { useTraceViewStore } from "@/stores/trace-view-store"
import { useToastStore } from "@/stores/toast-store"

const DIRECTION_LABEL: Record<string, string> = {
  CONNECTED: "Connected",
  DOWNSTREAM: "Downstream",
  UPSTREAM: "Upstream",
  SHORTEST_PATH: "Shortest path",
  // Not real api/internal/trace direction codes -- feature-panel.tsx's
  // feeder/section triggers hit their own dedicated endpoints
  // (POST /trace/feeder, POST /trace/section) and normalize their
  // differently-shaped responses into this same TraceResult display
  // rather than building two more result tables from scratch; these
  // two synthetic labels are what that normalization uses in place of
  // a real trace_configurations direction.
  FEEDER: "Feeder",
  SECTION: "Section",
  // Not a real trace_configurations direction either -- feature-panel.tsx's
  // "Supply path" trigger hits POST /trace/supply-path (a fixed barrier-free
  // upstream walk) and normalizes its response into this same TraceResult
  // display, with the serving transformer / switching devices / source as
  // `targets`.
  SUPPLY_PATH: "Supply path",
  // Not a real trace_configurations direction -- feature-panel.tsx's "Service
  // trace" trigger hits POST /trace/service (a downstream walk that collects
  // the reached SERVICE_POINTs) and normalizes its response into this same
  // display, with the consumption rollup surfaced by ServiceRollupSummary and
  // the reached service points listed as `features`/`targets`.
  SERVICE: "Service",
}

/** A one-paragraph description of what the current trace does, keyed by its
 * direction (and config code for the circuit trace, which is a CONNECTED config).
 * Shown in the collapsible "What this trace does" strip pinned to the bottom of the
 * result panel (the same affordance the analysis panels use), so an operator can
 * confirm exactly what they ran without cluttering the trace menu. Returns null for
 * a direction with no description (a custom admin config). */
function traceDescription(direction: string, configCode?: string): ReactNode {
  if (configCode === "MV_CIRCUIT_TRACE") {
    return (
      <p>
        Everything sharing this feature’s <b>circuit</b> (the same <code>circuit_id</code>), across every class — the
        feeder membership stamped by assign-by-trace, regardless of direction.
      </p>
    )
  }
  switch (direction) {
    case "CONNECTED":
      return (
        <p>
          Everything <b>galvanically connected</b> to the start — ignores flow direction and live switch state. “What is
          physically wired together.”
        </p>
      )
    case "DOWNSTREAM":
      return (
        <p>
          Everything fed <b>from here toward the loads</b>. Stops at open switches, and at transformers — the “continue
          past transformers” toggle crosses that boundary.
        </p>
      )
    case "UPSTREAM":
      return (
        <p>
          Everything between here and the source, following the <b>supply direction</b>. Stops at open switches.
        </p>
      )
    case "SHORTEST_PATH":
      return (
        <p>
          The <b>shortest connected path</b> from the start to the feature you picked — the fewest-hop route between the
          two, undirected.
        </p>
      )
    case "FEEDER":
      return (
        <p>
          The whole <b>feeder</b> — up to the feeder head, then everything downstream of it. The electrical feeder this
          feature belongs to.
        </p>
      )
    case "SECTION":
      return (
        <p>
          The <b>section</b> bounded by the nearest switching / protective devices — what you would isolate (or what a
          fault de-energizes) around this feature.
        </p>
      )
    case "SERVICE":
      return (
        <p>
          Downstream to the <b>service points</b> it feeds, rolled up by load and restoration priority. “What this
          serves.”
        </p>
      )
    case "SUPPLY_PATH":
      return (
        <p>
          The single route to this feature’s <b>source</b> — its distribution transformer (LV) or its feeder breaker
          (MV). Options can follow only the live (energized) path and chain across transformers to the primary substation
          source.
        </p>
      )
    default:
      return null
  }
}

/** The concrete name for the gold `trace-target` features, per trace -- the
 * targets mean a DIFFERENT thing in each (a feeder's head, a section's bounding
 * devices, a supply path's source, a shortest-path's destination, a service
 * trace's service points), so the legend and the explanation name the actual
 * thing rather than a vague "milestone". */
function traceTargetLabel(direction: string): string {
  switch (direction) {
    case "FEEDER":
      return "Feeder head"
    case "SECTION":
      return "Boundary"
    case "SUPPLY_PATH":
      return "Source"
    case "SHORTEST_PATH":
      return "Destination"
    case "SERVICE":
      return "Service point"
    default:
      return "Target"
  }
}

/** Minimal pluralizer for the colour-legend prose (handles the one irregular,
 * "boundary" -> "boundaries"); n===1 leaves the word unchanged. */
function plural(word: string, n: number): string {
  if (n === 1) return word
  return /[^aeiou]y$/.test(word) ? `${word.slice(0, -1)}ies` : `${word}s`
}

function shortId(id: string): string {
  return id.length > 8 ? `${id.slice(0, 8)}…` : id
}

/** Renders volts compactly: kV once at/over 1000 (11kV, 0.4kV), plain V
 * below (400 V). Trailing-zero-trimmed so 11000 -> "11 kV", not
 * "11.00 kV". Used for both a feature's stored nominal voltage and its
 * computed load-flow voltage so the two columns read the same way. */
function formatVolts(v: number): string {
  if (v >= 1000) {
    return `${Number((v / 1000).toFixed(2))} kV`
  }
  return `${Number(v.toFixed(1))} V`
}

/** A small pill marking a switching device's live position: green "Closed" (the
 * switch is passing power -- so it reads as a real, present device on the path,
 * not just a silent conductor) or red "Open" (it's isolating -- the same red the
 * trace uses for the open barriers it stops at). Any other coded DEVICE_STATE
 * value is shown title-cased in a neutral pill rather than dropped, so an
 * unexpected domain value stays visible. Renders nothing for a non-switch feature
 * (no device_state), so it can be dropped into every row unconditionally. */
function DeviceStateBadge({ state }: { state: string | null }) {
  if (!state) return null
  const up = state.toUpperCase()
  const closed = up === "CLOSED"
  const open = up === "OPEN"
  const label = closed || open ? (closed ? "Closed" : "Open") : state.charAt(0) + state.slice(1).toLowerCase()
  return (
    <span
      className={cn(
        "shrink-0 rounded px-1.5 py-0.5 text-[11px] font-medium",
        closed && "bg-emerald-500/15 text-emerald-700 dark:text-emerald-400",
        open && "bg-destructive/15 text-destructive",
        !closed && !open && "bg-muted text-muted-foreground",
      )}
      title={`Switching device — currently ${label.toLowerCase()}`}
    >
      {label}
    </span>
  )
}

/** A "Private" badge for a reached feature that belongs to a customer's own
 * network (ownership = "private"). Null for utility-owned/unset, so it only
 * marks the private stretch a trace crossed into. */
function OwnershipBadge({ ownership }: { ownership: string | null }) {
  if (ownership !== "private") return null
  return (
    <span
      className="shrink-0 rounded bg-slate-500/15 px-1.5 py-0.5 text-[11px] font-medium text-slate-600 dark:text-slate-300"
      title="Privately owned — the customer's own network, not utility plant"
    >
      Private
    </span>
  )
}

interface ResolvedRow {
  feature_class: string
  feature_id: string
  name: string | null
  circuitRef: string | null
  nominalVoltageV: number | null
  /** True when the reached feature has real map geometry (rides on the trace
   * response's tracedFeature.on_map -- no extra fetch). A feature can be both
   * on the map and on one or more diagrams. */
  onMap: boolean
  /** The single-line diagram(s) this feature is placed on (name may be empty). */
  diagrams: { instance_id: string; name?: string }[]
  /** Live switch position ("OPEN" / "CLOSED") for a switching device, null for
   * everything else (rides on the trace response's tracedFeature.device_state --
   * no extra fetch). Drives the state badge that marks every switch the trace
   * reached, closed ones included. */
  deviceState: string | null
  /** The reached feature's ownership ("private" for an owner's plant, null for
   * utility-owned/unset -- rides on the trace response's tracedFeature.ownership,
   * no extra fetch). Drives the "Private" badge that flags the stretch of the
   * result that belongs to the customer's own network (the asset-ownership axis),
   * since a trace crosses that boundary rather than stopping at it. */
  ownership: string | null
  /** This feature's one-hop-upstream neighbour in the trace's BFS tree (rides on
   * the trace response's tracedFeature.parent_feature_* -- no extra fetch), used
   * to build the client-side tree view. Null on the start feature and on any node
   * whose parent fell outside the caller's scope (an orphan -- reparented under
   * the root in the tree). Only the standard trace carries these; the synthetic
   * feeder/section/service/supply-path traces return a flat list (all null), so
   * the tree there falls back to every row directly under the root. */
  parentClass: string | null
  parentId: string | null
  isBarrier: boolean
  isTarget: boolean
}

/** Stable `feature_class:feature_id` key -- the id shape the trace engine's
 * parent pointers, barriers, and targets are all keyed on. */
function rowKey(r: { feature_class: string; feature_id: string }): string {
  return `${r.feature_class}:${r.feature_id}`
}

/** One line of the flattened tree render: the row plus its depth (indent),
 * whether it has children (gets a chevron), the size of its whole subtree
 * (every feature below it in the trace, collapse-independent), and whether it's
 * the trace's start (the tree root). Table mode reuses the same shape at depth 0
 * so both views share one row renderer. */
interface TreeLine {
  row: ResolvedRow
  depth: number
  hasChildren: boolean
  descendantCount: number
  isRoot: boolean
  /** Ancestor rail columns for the connector guides, one per ancestor level
   * above this node's own connector (length depth-1; empty for the root and its
   * direct children). true = that ancestor still has siblings below, so a
   * vertical "│" rail is drawn through this row at that column; false = blank
   * (the ancestor's branch ended above, so no line passes through here). */
  railColumns: boolean[]
  /** Whether this node is the last of its parent's children -- picks the elbow
   * "└" (last) vs. tee "├" (more siblings follow) connector in its own column.
   * Meaningless for the root (rendered without a connector). */
  isLastChild: boolean
}

/**
 * Builds the depth-ordered, collapse-aware list of rows for the tree view from
 * each row's parent pointer. Root is the trace's start feature; any row whose
 * parent isn't itself in the result (an orphan, or a scope-clipped parent) is
 * reparented directly under the root so nothing is dropped. Self-references and
 * cycles are guarded by a visited set (a node is emitted at most once). When the
 * start feature isn't among the returned features, rows with no in-set parent
 * become top-level roots instead. Collapsed keys hide their subtree but stay
 * visible themselves.
 */
function buildTreeLines(rows: ResolvedRow[], rootKey: string, collapsed: Set<string>): TreeLine[] {
  const byKey = new Map<string, ResolvedRow>()
  for (const r of rows) byKey.set(rowKey(r), r)
  const rootExists = byKey.has(rootKey)

  const childrenByKey = new Map<string, ResolvedRow[]>()
  const roots: ResolvedRow[] = []
  for (const r of rows) {
    const k = rowKey(r)
    if (k === rootKey) {
      roots.push(r)
      continue
    }
    const pk = r.parentClass && r.parentId ? `${r.parentClass}:${r.parentId}` : null
    const attachTo = pk && pk !== k && byKey.has(pk) ? pk : rootExists ? rootKey : null
    if (attachTo === null) {
      roots.push(r)
      continue
    }
    const list = childrenByKey.get(attachTo)
    if (list) list.push(r)
    else childrenByKey.set(attachTo, [r])
  }

  // Subtree size (every feature below a node), collapse-independent -- shown on
  // collapsible rows so a collapsed branch still tells you how much it hides.
  // childrenByKey is a forest (each row is pushed under exactly one parent, self
  // and cross-set parents reparent to the root), so this can't cycle; the
  // in-progress guard is belt-and-braces.
  const descCount = new Map<string, number>()
  const inProgress = new Set<string>()
  const countDescendants = (k: string): number => {
    const memo = descCount.get(k)
    if (memo !== undefined) return memo
    if (inProgress.has(k)) return 0
    inProgress.add(k)
    const kids = childrenByKey.get(k) ?? []
    let total = kids.length
    for (const c of kids) total += countDescendants(rowKey(c))
    inProgress.delete(k)
    descCount.set(k, total)
    return total
  }

  const lines: TreeLine[] = []
  const visited = new Set<string>()
  // railColumns/isLastChild thread the connector state down the tree: a node's
  // children inherit its rails plus one more column for this node's own
  // connector -- a "│" if this node has siblings after it, blank if it was the
  // last. The root itself contributes no column (it's rendered without a
  // connector), so its children start with empty rails.
  const walk = (r: ResolvedRow, depth: number, railColumns: boolean[], isLastChild: boolean) => {
    const k = rowKey(r)
    if (visited.has(k)) return
    visited.add(k)
    const kids = childrenByKey.get(k) ?? []
    lines.push({
      row: r,
      depth,
      hasChildren: kids.length > 0,
      descendantCount: countDescendants(k),
      isRoot: k === rootKey,
      railColumns,
      isLastChild,
    })
    if (kids.length > 0 && !collapsed.has(k)) {
      const childRail = depth === 0 ? [] : [...railColumns, !isLastChild]
      kids.forEach((c, i) => walk(c, depth + 1, childRail, i === kids.length - 1))
    }
  }
  for (const root of roots) walk(root, 0, [], true)
  // Safety net: anything not reached (shouldn't happen once orphans reparent,
  // but a malformed cycle could) still gets listed rather than silently lost.
  for (const r of rows) if (!visited.has(rowKey(r))) walk(r, 0, [], true)
  return lines
}

/**
 * Keys on the path from `key` up to the tree root, read straight off the
 * rendered (depth-ordered) line list: an ancestor of a visible row is always
 * visible and always precedes it, so walking backwards and picking up each
 * next-shallower depth collects the whole chain to the root without needing a
 * separate parent map. Includes `key` itself. Empty when key is null or isn't
 * currently shown (e.g. it sits inside a branch a collapse just hid). Powers the
 * hover highlight -- pointing at any node lights the route back to Start.
 */
function ancestorPathKeys(lines: TreeLine[], key: string | null): Set<string> {
  const path = new Set<string>()
  if (!key) return path
  const idx = lines.findIndex((l) => rowKey(l.row) === key)
  if (idx < 0) return path
  path.add(key)
  let needDepth = lines[idx].depth - 1
  for (let i = idx - 1; i >= 0 && needDepth >= 0; i--) {
    if (lines[i].depth === needDepth) {
      path.add(rowKey(lines[i].row))
      needDepth -= 1
    }
  }
  return path
}

/** Default anchor (bottom-center) used until the user drags the panel
 * somewhere else -- the same spot the count-only banner it replaced used,
 * clear of the attribute panel's bottom-right corner and the top-center
 * edit toolbar. Dropped the moment `position` is set (a manual drag). */
const DEFAULT_POSITION_CLASS = "bottom-10 left-1/2 -translate-x-1/2"

/** Floors a manual resize can't shrink past -- small enough to still show the
 * header plus a row or two, not so small the table collapses. The default
 * width is computed per-render (wider when a computed-voltage column shows). */
const MIN_WIDTH = 320
const MIN_HEIGHT = 160

/**
 * Floating data table listing every feature a trace reached -- feature
 * class, id, and (if the feature has one) its human-assigned name --
 * replacing an earlier count-only summary banner. Opens bottom-center
 * (DEFAULT_POSITION_CLASS), but is draggable (grab the header) and
 * resizable (drag the bottom-right grip) -- the same mechanics
 * feature-panel.tsx uses -- so it can be moved clear of whatever the user
 * is working on and grown to show a long trace. `position`/`size` live in
 * this component's own state, which React preserves across re-renders (and
 * across successive traces, since the component stays mounted and only
 * returns null when there's no result), so a panel the user has moved or
 * sized stays put for the next trace rather than snapping back.
 * Self-contained, same pattern as every other floating overlay in this app
 * -- reads trace-store.ts directly and returns null rather than being
 * conditionally rendered by a parent.
 *
 * Resolves each row's name with its own fetch, independent of
 * voltedge-map.tsx's own per-feature fetch for the highlight geometry
 * -- the two components have different jobs (this one only needs
 * `name`, the map only needs `geometry`) and neither depends on the
 * other's result, so there's no shared state worth coordinating; a
 * handful of duplicate GETs for a trace's typically-small feature count
 * is a fine trade against that complexity.
 */
export function TraceResultTable() {
  const result = useTraceStore((s) => s.result)
  const computedVoltages = useTraceStore((s) => s.computedVoltages)
  const loading = useTraceStore((s) => s.loading)
  const rerun = useTraceStore((s) => s.rerun)
  const view = useTraceViewStore((s) => s.view)
  const setView = useTraceViewStore((s) => s.setView)
  const { data: featureClasses } = useFeatureClasses()
  const [rows, setRows] = useState<ResolvedRow[] | null>(null)
  // Collapsed subtree roots for the tree view, keyed feature_class:feature_id.
  // Reset whenever the result changes (a new trace's tree starts fully
  // expanded) -- see the effect below.
  const [collapsed, setCollapsed] = useState<Set<string>>(new Set())
  // The tree row currently under the pointer -- its chain of ancestors up to the
  // Start row is highlighted so you can read the route back to the source at a
  // glance. Null when nothing is hovered (or in table mode); reset per result.
  const [hoveredKey, setHoveredKey] = useState<string | null>(null)
  const [position, setPosition] = useState<{ x: number; y: number } | null>(null)
  const [size, setSize] = useState<{ width: number; height: number } | null>(null)
  // Whether the collapsible "Fix" column (shown for a live loop check) is expanded.
  const [fixOpen, setFixOpen] = useState(true)
  const panelRef = useRef<HTMLDivElement>(null)

  // Grab the header and drag -- pointer-capture mechanics identical to
  // feature-panel.tsx's own header drag. `origX/origY` come from the live
  // bounding rect when the panel is still at its default anchor (position
  // null), so the first drag continues smoothly from wherever the anchor
  // put it rather than jumping.
  function handleHeaderPointerDown(e: React.PointerEvent) {
    const rect = panelRef.current?.getBoundingClientRect()
    if (!rect) return
    const startX = e.clientX
    const startY = e.clientY
    const origX = position?.x ?? rect.left
    const origY = position?.y ?? rect.top

    const handleMove = (moveEvt: PointerEvent) => {
      setPosition({ x: origX + (moveEvt.clientX - startX), y: origY + (moveEvt.clientY - startY) })
    }
    const handleUp = () => {
      window.removeEventListener("pointermove", handleMove)
      window.removeEventListener("pointerup", handleUp)
    }
    window.addEventListener("pointermove", handleMove)
    window.addEventListener("pointerup", handleUp)
  }

  // Same mechanics, writing `size` instead of `position`. Pin the panel to
  // its current top-left first if it's still on the centered default anchor,
  // so a bottom-right resize grows down/right intuitively rather than the
  // centered panel appearing to grow symmetrically off its anchor. Floored
  // at MIN_WIDTH/MIN_HEIGHT.
  function handleResizePointerDown(e: React.PointerEvent) {
    e.stopPropagation()
    const rect = panelRef.current?.getBoundingClientRect()
    if (!rect) return
    if (!position) setPosition({ x: rect.left, y: rect.top })
    const startX = e.clientX
    const startY = e.clientY
    const startWidth = size?.width ?? rect.width
    const startHeight = size?.height ?? rect.height

    const handleMove = (moveEvt: PointerEvent) => {
      setSize({
        width: Math.max(MIN_WIDTH, startWidth + (moveEvt.clientX - startX)),
        height: Math.max(MIN_HEIGHT, startHeight + (moveEvt.clientY - startY)),
      })
    }
    const handleUp = () => {
      window.removeEventListener("pointermove", handleMove)
      window.removeEventListener("pointerup", handleUp)
    }
    window.addEventListener("pointermove", handleMove)
    window.addEventListener("pointerup", handleUp)
  }

  // Re-run the SAME trace with one toggle flipped, preserving the other. Both the
  // "Continue past transformers" and "Include planned (as-designed)" toggles live
  // in the result (crossing / scenario only make sense once you've seen the run),
  // reuse the echoed trace_configuration + start, and keep the computed-voltage
  // overlay (same start, so it still applies) and the header Re-run thunk.
  async function rerunWith(overrides: { cross_transformers?: boolean; as_designed?: boolean }) {
    const current = useTraceStore.getState().result
    const computed = useTraceStore.getState().computedVoltages
    if (!current) return
    useTraceStore.getState().setLoading(true)
    try {
      const next = await apiFetch<TraceResult>("/trace", {
        method: "POST",
        body: JSON.stringify({
          trace_configuration: current.trace_configuration,
          start: current.start,
          cross_transformers: current.cross_transformers ?? false,
          as_designed: current.as_designed ?? false,
          ...overrides,
        }),
      })
      useTraceStore.getState().setResult(next, computed, useTraceStore.getState().rerun ?? undefined)
    } catch (err) {
      useTraceStore.getState().setLoading(false)
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not re-run this trace.")
    }
  }

  useEffect(() => {
    if (!result) {
      setRows(null)
      return
    }

    let cancelled = false
    // Reassigned into a typed local -- TypeScript's control-flow
    // narrowing from the `if (!result)` check above doesn't propagate
    // into the nested async function below.
    const trace: NonNullable<typeof result> = result
    const barrierKeys = new Set(trace.barriers.map((b) => `${b.feature_class}:${b.feature_id}`))
    const targetKeys = new Set((trace.targets ?? []).map((t) => `${t.feature_class}:${t.feature_id}`))

    async function loadNames() {
      const resolved = await Promise.all(
        trace.features.map(async (ref) => {
          let name: string | null = null
          try {
            const feature = await apiFetch<{ name: string | null }>(`/features/${ref.feature_class}/${ref.feature_id}`)
            name = feature.name
          } catch {
            // A row whose name fails to load individually still shows
            // up with its class/id -- not worth failing the whole table
            // over one feature's name.
          }
          const key = `${ref.feature_class}:${ref.feature_id}`
          return {
            feature_class: ref.feature_class,
            feature_id: ref.feature_id,
            name,
            // circuit_ref / nominal_voltage_v ride along on the trace
            // response's own feature entries (api/internal/trace's
            // tracedFeature) -- no extra fetch; the per-row GET above is
            // only for the name. Absent when the reached class carries
            // neither a circuit_id field nor any voltage-class field.
            circuitRef: ref.circuit_ref ?? null,
            nominalVoltageV: ref.nominal_voltage_v ?? null,
            // on_map / diagrams also ride the trace response's own feature
            // entries (api/internal/trace's tracedFeature) -- no extra fetch.
            onMap: ref.on_map ?? false,
            diagrams: ref.diagrams ?? [],
            // device_state rides the trace response too -- present only for
            // switching devices, absent (null) for everything else.
            deviceState: ref.device_state ?? null,
            // ownership rides the trace response too -- "private" for an owner's
            // plant, absent (null) for utility-owned/unset.
            ownership: ref.ownership ?? null,
            // Parent pointers ride the trace response too -- present on the
            // standard trace, absent (null) on the synthetic feeder/section/
            // service/supply-path traces, where the tree falls back to flat.
            parentClass: ref.parent_feature_class ?? null,
            parentId: ref.parent_feature_id ?? null,
            isBarrier: barrierKeys.has(key),
            isTarget: targetKeys.has(key),
          }
        }),
      )
      if (!cancelled) setRows(resolved)
    }

    setRows(null)
    // A new trace's tree starts fully expanded -- the previous trace's
    // collapsed set is meaningless for different features.
    setCollapsed(new Set())
    setHoveredKey(null)
    loadNames()
    return () => {
      cancelled = true
    }
  }, [result])

  if (!result) return null

  const directionLabel = DIRECTION_LABEL[result.direction] ?? result.direction
  const traceDesc = traceDescription(result.direction, result.trace_configuration)
  // A loop check reuses this panel (synthetic direction LOOPS_*). A LIVE loop gets
  // a collapsible right-hand "Fix" column so the loop and its fix share one window;
  // the as-built view (trace_configuration carries "as-built") is informational only.
  const isLoopResult = (result.direction ?? "").startsWith("LOOPS_")
  const showFix = isLoopResult && !(result.trace_configuration ?? "").includes("as-built")
  const classNameFor = (code: string) => featureClasses?.feature_classes.find((fc) => fc.code === code)?.name ?? code

  // Network sectioning (migration 0129): each row's section = its nearest
  // upstream boundary device, climbed from the parent tree the trace already
  // carries. Derived client-side from boundary_role -- no engine change. The
  // boundary device itself heads its own section (so it gets no tag); downstream
  // rows show the head's name. Precomputed once per row.
  const sectionRoleByClass = boundaryRoleMap(featureClasses?.feature_classes)
  const sectionNodes = new Map<string, SectionNode>(
    (rows ?? []).map((r) => [
      rowKey(r),
      {
        feature_class: r.feature_class,
        feature_id: r.feature_id,
        parent_feature_class: r.parentClass,
        parent_feature_id: r.parentId,
      },
    ]),
  )
  const rowNameByKey = new Map((rows ?? []).map((r) => [rowKey(r), r.name]))
  const sectionByKey = new Map<string, string | null>(
    (rows ?? []).map((r) => {
      const node = sectionNodes.get(rowKey(r))
      const b = node ? nearestBoundary(node, sectionNodes, sectionRoleByClass) : null
      // A boundary device heads its own section -- don't tag it with itself.
      if (!b || (b.feature_class === r.feature_class && b.feature_id === r.feature_id)) {
        return [rowKey(r), null]
      }
      const headKey = `${b.feature_class}:${b.feature_id}`
      return [rowKey(r), rowNameByKey.get(headKey) || classNameFor(b.feature_class)]
    }),
  )
  const sectionTag = (row: ResolvedRow) => {
    const s = sectionByKey.get(rowKey(row))
    if (!s) return null
    return (
      <span
        className="text-muted-foreground shrink-0 text-[11px]"
        title="Network section — its nearest upstream boundary device"
      >
        § {s}
      </span>
    )
  }
  // Only give computed voltage its own column when a load-flow result for
  // this trace's start was snapshotted at trace time (feature-panel.tsx) --
  // otherwise the whole column would be "—" noise. Nominal voltage always
  // shows: it's topology-intrinsic and needs no solve.
  const hasComputed = computedVoltages !== null

  // Tree view: the depth-ordered, collapse-aware line list built client-side
  // from each row's parent pointer (see buildTreeLines). Only computed in tree
  // mode; the flat table reads `rows` directly. Small result sets -- no memo.
  const rootKey = `${result.start.feature_class}:${result.start.feature_id}`
  const treeLines = view === "tree" && rows ? buildTreeLines(rows, rootKey, collapsed) : null
  // The hovered row's ancestor chain up to Start -- highlighted (row tint +
  // primary connectors + bolder label) so the route back to the source reads at
  // a glance. Empty in table mode or when nothing is hovered.
  const activePathKeys = treeLines ? ancestorPathKeys(treeLines, hoveredKey) : new Set<string>()
  const collapsibleKeys =
    view === "tree" && rows ? buildTreeLines(rows, rootKey, new Set()).filter((l) => l.hasChildren).map((l) => rowKey(l.row)) : []
  const allExpanded = collapsibleKeys.every((k) => !collapsed.has(k))

  const toggleCollapsed = (key: string) =>
    setCollapsed((prev) => {
      const next = new Set(prev)
      if (next.has(key)) next.delete(key)
      else next.add(key)
      return next
    })
  const expandAll = () => setCollapsed(new Set())
  const collapseAll = () => setCollapsed(new Set(collapsibleKeys))

  // One row, shared by both views. `tree` is set only in tree mode: it indents
  // the first cell by depth, prefixes a chevron (or an aligning spacer), and
  // annotates the first cell with a "Start" badge on the root plus the branch's
  // subtree size -- every other cell is identical, so the two views never drift.
  const renderRow = (
    row: ResolvedRow,
    tree?: {
      depth: number
      hasChildren: boolean
      descendantCount: number
      isRoot: boolean
      railColumns: boolean[]
      isLastChild: boolean
    },
  ) => {
    const key = rowKey(row)
    const computed = computedVoltages?.[key]
    const isCollapsed = collapsed.has(key)
    const names = row.diagrams.map((d) => d.name?.trim() || "Untitled")
    // On the hovered row's ancestor chain to Start (tree mode only): tint the
    // row and colour its connectors/rails so the whole route stands out. The
    // barrier/target row backgrounds take precedence over the path tint.
    const onPath = !!tree && activePathKeys.has(key)
    return (
      <tr
        key={key}
        onClick={() => useSelectionStore.getState().locate(row.feature_class, row.feature_id, { focus: true })}
        onDoubleClick={() => useSelectionStore.getState().select(row.feature_class, row.feature_id, { focus: true })}
        onMouseEnter={tree ? () => setHoveredKey(key) : undefined}
        onMouseLeave={tree ? () => setHoveredKey(null) : undefined}
        className={cn(
          "hover:bg-accent cursor-pointer border-b last:border-b-0 select-none",
          onPath && !row.isBarrier && !row.isTarget && "bg-primary/5",
          row.isBarrier && "bg-destructive/10 text-destructive hover:bg-destructive/15",
          !row.isBarrier &&
            row.isTarget &&
            "bg-yellow-500/10 text-yellow-700 hover:bg-yellow-500/15 dark:text-yellow-400",
        )}
        title={
          row.isBarrier
            ? "Trace stopped here (open device)"
            : row.isTarget
              ? "Notable for this trace (a matched target class, feeder head, or section boundary)"
              : undefined
        }
      >
        <td className={cn(tree ? "p-0" : "px-3 py-1.5")}>
          {tree ? (
            // items-stretch lets each guide column fill the row height so the
            // vertical "│" rails run continuously between rows; the vertical
            // padding lives on the content, not the cell, for the same reason.
            <div className="flex items-stretch">
              <span className="w-2 shrink-0" />
              {/* Ancestor rails: a vertical line where an ancestor still has
                  siblings below, blank where its branch already ended. */}
              {tree.railColumns.map((rail, i) => (
                <span key={i} className="relative w-4 shrink-0 self-stretch">
                  {rail && (
                    <span
                      className={cn(
                        "absolute top-0 left-1/2 h-full w-px",
                        onPath ? "bg-primary" : "bg-border",
                      )}
                    />
                  )}
                </span>
              ))}
              {/* This node's own connector: elbow "└" when it's the last child
                  (vertical stops at the row's middle), tee "├" otherwise (line
                  continues down), with a short horizontal stub to the label. */}
              {tree.depth > 0 && (
                <span className="relative w-4 shrink-0 self-stretch">
                  <span
                    className={cn(
                      "absolute top-0 left-1/2 w-px",
                      tree.isLastChild ? "h-1/2" : "h-full",
                      onPath ? "bg-primary" : "bg-border",
                    )}
                  />
                  <span
                    className={cn("absolute top-1/2 left-1/2 h-px w-1/2", onPath ? "bg-primary" : "bg-border")}
                  />
                </span>
              )}
              <div className="flex min-w-0 items-center gap-1 py-1.5 pr-3 pl-0.5">
                {tree.hasChildren ? (
                  <button
                    type="button"
                    onClick={(e) => {
                      e.stopPropagation()
                      toggleCollapsed(key)
                    }}
                    className="hover:bg-muted bg-popover z-10 -ml-0.5 flex size-4 shrink-0 items-center justify-center rounded"
                    title={isCollapsed ? "Expand" : "Collapse"}
                  >
                    <ChevronRight className={cn("size-3 transition-transform", !isCollapsed && "rotate-90")} />
                    <span className="sr-only">{isCollapsed ? "Expand" : "Collapse"}</span>
                  </button>
                ) : (
                  <span className="inline-block size-4 shrink-0" />
                )}
                <FeatureInfoButton featureClass={row.feature_class} featureId={row.feature_id} />
                <span className={cn("truncate", onPath && "font-medium")}>{classNameFor(row.feature_class)}</span>
                {tree.isRoot && (
                  <span className="rounded bg-primary/15 text-primary shrink-0 px-1.5 py-0.5 text-[11px] font-medium">
                    Start
                  </span>
                )}
                <DeviceStateBadge state={row.deviceState} />
                <OwnershipBadge ownership={row.ownership} />
                {sectionTag(row)}
                {tree.hasChildren && tree.descendantCount > 0 && (
                  <span
                    className="text-muted-foreground shrink-0 text-xs tabular-nums"
                    title={`${tree.descendantCount} feature${tree.descendantCount === 1 ? "" : "s"} below this branch`}
                  >
                    ({tree.descendantCount})
                  </span>
                )}
              </div>
            </div>
          ) : (
            <span className="inline-flex items-center gap-1.5">
              <FeatureInfoButton featureClass={row.feature_class} featureId={row.feature_id} />
              {classNameFor(row.feature_class)}
              <DeviceStateBadge state={row.deviceState} />
              <OwnershipBadge ownership={row.ownership} />
              {sectionTag(row)}
            </span>
          )}
        </td>
        <td className="px-3 py-1.5 font-mono">{shortId(row.feature_id)}</td>
        <td className="text-muted-foreground px-3 py-1.5">{row.name || "—"}</td>
        <td className="px-3 py-1.5">
          {!row.onMap && names.length === 0 ? (
            <span className="text-muted-foreground">—</span>
          ) : (
            <div className="flex flex-wrap items-center gap-1">
              {row.onMap && (
                <span className="rounded bg-emerald-500/15 px-1.5 py-0.5 text-[11px] font-medium text-emerald-700 dark:text-emerald-400">
                  Map
                </span>
              )}
              {row.diagrams.map((d, i) => (
                <button
                  key={d.instance_id}
                  type="button"
                  // Open this single-line diagram. stopPropagation so the click
                  // doesn't also fire the row's select-on-map handler.
                  onClick={(e) => {
                    e.stopPropagation()
                    useDiagramStore.getState().open(d.instance_id)
                  }}
                  className="cursor-pointer rounded bg-sky-500/15 px-1.5 py-0.5 text-[11px] font-medium text-sky-700 hover:bg-sky-500/30 hover:underline focus-visible:ring-1 focus-visible:ring-sky-500 focus-visible:outline-none dark:text-sky-400"
                  title={`Open diagram: ${names[i]}`}
                >
                  Diagram: {names[i]}
                </button>
              ))}
            </div>
          )}
        </td>
        <td className="text-muted-foreground px-3 py-1.5">{row.circuitRef || "—"}</td>
        <td className="text-muted-foreground px-3 py-1.5 tabular-nums">
          {row.nominalVoltageV != null ? formatVolts(row.nominalVoltageV) : "—"}
        </td>
        {hasComputed && (
          <td className="text-muted-foreground px-3 py-1.5 tabular-nums">
            {computed != null ? formatVolts(computed) : "—"}
          </td>
        )}
      </tr>
    )
  }

  // Default width matches the former fixed w-[46rem]/w-[38rem] classes (px, so
  // a manual resize has a live numeric value to grow from). Once resized the
  // panel takes an explicit height; until then it shrink-wraps under max-h-64.
  const defaultWidth = hasComputed ? 892 : 764
  const resized = size?.height != null
  const shellStyle: React.CSSProperties = {
    ...(position ? { left: position.x, top: position.y } : {}),
    width: size?.width ?? defaultWidth,
    ...(resized ? { height: size?.height } : {}),
  }

  return (
    <div
      ref={panelRef}
      style={shellStyle}
      className={cn(
        "bg-popover text-popover-foreground fixed z-40 flex flex-col max-w-[calc(100vw-2rem)] overflow-hidden rounded-lg border border-border/70 border-t-2 border-t-[oklch(0.45_0.10_252)] dark:border-t-primary shadow-xl ring-1 ring-black/5",
        !resized && "max-h-64",
        !position && DEFAULT_POSITION_CLASS,
      )}
    >
      <div
        onPointerDown={handleHeaderPointerDown}
        className="flex cursor-grab items-center justify-between gap-2 border-b border-[oklch(0.45_0.10_252)]/20 bg-gradient-to-b from-[oklch(0.45_0.10_252)]/15 to-[oklch(0.45_0.10_252)]/5 dark:border-primary/15 dark:from-accent/50 dark:to-accent/20 px-3 py-2 select-none active:cursor-grabbing"
      >
        <div className="flex min-w-0 items-center gap-1.5">
          <GripHorizontal className="text-primary/70 size-3.5 shrink-0" />
          <div className="min-w-0">
            <p className="truncate text-sm font-medium">{directionLabel} trace</p>
            {result.direction === "SHORTEST_PATH" && (
              <p className="text-muted-foreground text-xs">{result.path_found ? "Path found" : "No path found"}</p>
            )}
            {result.transformer_boundaries && result.transformer_boundaries.length > 0 && (
              <p className="text-muted-foreground text-xs">
                Stopped at {result.transformer_boundaries.length} transformer
                {result.transformer_boundaries.length === 1 ? "" : "s"} (the feeder / LV boundary)
              </p>
            )}
            {result.truncated && (
              <p className="text-xs font-medium text-amber-600 dark:text-amber-400">
                Incomplete — trace hit the node limit; showing the first {result.features.length}.
                Features past this point are not included.
              </p>
            )}
          </div>
        </div>
        <div className="flex shrink-0 items-center gap-0.5">
          {/* Table | Tree segmented toggle -- swaps this same panel between the
              flat feature table and the BFS spanning tree built from parent
              pointers. The choice is a durable preference (trace-view-store). */}
          <div className="border-border/70 mr-0.5 flex items-center rounded-md border p-0.5">
            <Button
              variant={view === "table" ? "secondary" : "ghost"}
              size="icon"
              className="size-5"
              onClick={() => setView("table")}
              title="Table view"
            >
              <List className="size-3.5" />
              <span className="sr-only">Table view</span>
            </Button>
            <Button
              variant={view === "tree" ? "secondary" : "ghost"}
              size="icon"
              className="size-5"
              onClick={() => setView("tree")}
              title="Tree view (by trace hierarchy)"
            >
              <ListTree className="size-3.5" />
              <span className="sr-only">Tree view</span>
            </Button>
          </div>
          {/* Expand / collapse the whole tree -- only meaningful in tree mode
              and only when something is actually nestable. */}
          {view === "tree" && collapsibleKeys.length > 0 && (
            <Button
              variant="ghost"
              size="icon"
              className="size-6"
              onClick={() => (allExpanded ? collapseAll() : expandAll())}
              title={allExpanded ? "Collapse all" : "Expand all"}
            >
              <ChevronRight className={cn("size-3.5 transition-transform", allExpanded && "rotate-90")} />
              <span className="sr-only">{allExpanded ? "Collapse all" : "Expand all"}</span>
            </Button>
          )}
          {/* Re-run the exact trace that produced this result (same endpoint,
              start, and config -- see trace-store.ts's rerun). Hidden when no
              re-runner was stashed; disabled and spinning while a run is in
              flight. */}
          {rerun && (
            <Button
              variant="ghost"
              size="icon"
              className="size-6"
              disabled={loading}
              onClick={() => useTraceStore.getState().rerun?.()}
              title="Re-run this trace"
            >
              <RefreshCw className={cn("size-3.5", loading && "animate-spin")} />
              <span className="sr-only">Re-run trace</span>
            </Button>
          )}
          {/* Show/hide the Fix column (live loop checks only). */}
          {showFix && (
            <Button
              variant={fixOpen ? "secondary" : "ghost"}
              size="icon"
              className="size-6"
              onClick={() => setFixOpen((v) => !v)}
              title={fixOpen ? "Hide fix" : "Fix this loop"}
            >
              <Scissors className="size-3.5" />
              <span className="sr-only">{fixOpen ? "Hide fix" : "Fix this loop"}</span>
            </Button>
          )}
          <Button
            variant="ghost"
            size="icon"
            className="size-6"
            onClick={() => useTraceStore.getState().clear()}
          >
            <X className="size-3.5" />
            <span className="sr-only">Clear trace</span>
          </Button>
        </div>
      </div>

      {/* Body, plus (for a live loop check) a collapsible right-hand Fix column, so
          the loop result and its fix share ONE window. */}
      <div className="flex min-h-0 flex-1 overflow-hidden">
        <div className="flex min-w-0 flex-1 flex-col overflow-hidden">

      {/* "Continue past transformers" toggle -- shown only once a downstream
          trace has actually met the transformer boundary (it stopped at one, or
          it's currently crossing them), which is the only point where the choice
          means anything. Toggling re-runs the same trace with the boundary
          crossed or restored. Not inside the draggable header, so its click never
          starts a drag. */}
      {result.direction === "DOWNSTREAM" &&
        (!!result.cross_transformers || (result.transformer_boundaries?.length ?? 0) > 0) && (
          <label className="border-border/60 bg-muted/30 flex cursor-pointer select-none items-center gap-2 border-b px-3 py-1.5 text-xs">
            <input
              type="checkbox"
              className="size-3.5"
              checked={!!result.cross_transformers}
              disabled={loading}
              onChange={(e) => rerunWith({ cross_transformers: e.target.checked })}
            />
            <span>
              Continue past transformers
              <span className="text-muted-foreground">
                {result.cross_transformers
                  ? " — including their LV network"
                  : " — extend the trace into their LV network"}
              </span>
            </span>
          </label>
        )}

      {/* "Include planned (as-designed)" toggle -- flips the whole trace between the
          as-operated network (default) and the as-designed one (also follow PLANNED /
          UNDER_CONSTRUCTION assets), re-running from the same start. Shown only for a
          standard trace walk: the synthetic flavors (feeder/section/service/supply)
          re-run through their own endpoints, not /trace, and don't carry the flag. */}
      {["DOWNSTREAM", "UPSTREAM", "CONNECTED", "SHORTEST_PATH"].includes(result.direction) && (
        <label className="border-border/60 bg-muted/30 flex cursor-pointer items-center gap-2 border-b px-3 py-1.5 text-xs select-none">
          <input
            type="checkbox"
            className="size-3.5"
            checked={!!result.as_designed}
            disabled={loading}
            onChange={(e) => rerunWith({ as_designed: e.target.checked })}
          />
          <span>
            Include planned (as-designed)
            <span className="text-muted-foreground">
              {result.as_designed
                ? " — following planned / under-construction assets"
                : " — trace the designed network, not just the operated one"}
            </span>
          </span>
        </label>
      )}

      {/* Phase 62 consumption rollup -- renders nothing when the trace reached
          no SERVICE_POINTs, so purely-topological traces are unaffected. */}
      <ServiceRollupSummary service={result.service} />

      {/* Colour legend -- the map tints each reached feature by its ROLE
          (map-layers.ts HIGHLIGHT_COLORS; assigned in voltedge-map.tsx). The
          swatches here read from the SAME constants, so they always match the
          map. Shown only when the trace has milestones or barriers -- a
          single-colour (all-reached) trace needs no legend. */}
      {((result.targets?.length ?? 0) > 0 || result.barriers.length > 0) && (
        <div className="border-border/60 bg-muted/20 text-muted-foreground flex flex-wrap items-center gap-x-3 gap-y-1 border-b px-3 py-1.5 text-xs">
          <span>Highlights:</span>
          <span className="text-foreground inline-flex items-center gap-1" title="Features the trace reached">
            <span className="size-2.5 rounded-full" style={{ backgroundColor: HIGHLIGHT_COLORS.trace }} />
            Reached <span className="text-muted-foreground">({result.features.length})</span>
          </span>
          {(result.targets?.length ?? 0) > 0 && (
            <span
              className="text-foreground inline-flex items-center gap-1"
              title="A key feature the trace marks — its source/head, a protective device, a section boundary, or the destination"
            >
              <span className="size-2.5 rounded-full" style={{ backgroundColor: HIGHLIGHT_COLORS["trace-target"] }} />
              {traceTargetLabel(result.direction)}{" "}
              <span className="text-muted-foreground">({result.targets?.length})</span>
            </span>
          )}
          {result.barriers.length > 0 && (
            <span
              className="text-foreground inline-flex items-center gap-1"
              title="Where the walk stopped — an open device or a boundary it couldn't cross"
            >
              <span className="size-2.5 rounded-full" style={{ backgroundColor: HIGHLIGHT_COLORS["trace-barrier"] }} />
              Barrier <span className="text-muted-foreground">({result.barriers.length})</span>
            </span>
          )}
        </div>
      )}

      <div className={cn("overflow-y-auto", resized ? "min-h-0 flex-1" : "max-h-[13rem]")}>
        {rows === null ? (
          <p className="text-muted-foreground p-3 text-sm">Loading…</p>
        ) : (
          <table className="w-full text-sm">
            <thead className="text-muted-foreground sticky top-0 border-b text-left text-xs">
              <tr>
                <th className="bg-popover px-3 py-1.5 font-medium">Feature class</th>
                <th className="bg-popover px-3 py-1.5 font-medium">Feature ID</th>
                <th className="bg-popover px-3 py-1.5 font-medium">Name</th>
                <th className="bg-popover px-3 py-1.5 font-medium">Placement</th>
                <th className="bg-popover px-3 py-1.5 font-medium">Circuit</th>
                <th className="bg-popover px-3 py-1.5 font-medium">Nominal V</th>
                {hasComputed && <th className="bg-popover px-3 py-1.5 font-medium">Computed V</th>}
              </tr>
            </thead>
            <tbody>
              {treeLines
                ? treeLines.map((line) =>
                    renderRow(line.row, {
                      depth: line.depth,
                      hasChildren: line.hasChildren,
                      descendantCount: line.descendantCount,
                      isRoot: line.isRoot,
                      railColumns: line.railColumns,
                      isLastChild: line.isLastChild,
                    }),
                  )
                : rows.map((row) => renderRow(row))}
            </tbody>
          </table>
        )}
      </div>

      {traceDesc && (
        <AnalysisExplanation title="What this trace does">
          {traceDesc}
          {/* Trace-specific colour key -- names the gold features concretely for
              THIS trace (a feeder's head, a section's boundaries, a supply path's
              source, ...) rather than a vague "milestone"; matches the swatch
              legend above and the map. */}
          <p className="text-muted-foreground mt-2">
            On the map, <b>green</b> is everything reached
            {(result.targets?.length ?? 0) > 0 && (
              <>
                , <b>gold</b> marks the{" "}
                {plural(traceTargetLabel(result.direction).toLowerCase(), result.targets?.length ?? 0)}
              </>
            )}
            {result.barriers.length > 0 && (
              <>
                , <b>red</b> marks where the walk stopped (an open device or a boundary it couldn’t cross)
              </>
            )}
            .
          </p>
        </AnalysisExplanation>
      )}
        </div>

        {showFix && fixOpen && (
          <div className="bg-card/40 flex w-72 shrink-0 flex-col overflow-hidden border-l">
            <div className="text-muted-foreground flex items-center gap-1.5 border-b px-3 py-1.5 text-xs font-medium">
              <Scissors className="text-primary size-3.5" /> Fix loop
            </div>
            <div className="min-h-0 flex-1 overflow-y-auto">
              <LoopFixContent start={result.start} />
            </div>
          </div>
        )}
      </div>

      {/* Bottom-right resize grip -- same affordance and mechanics as
          feature-panel.tsx's. stopPropagation in the handler keeps a resize
          drag from also starting a header move. */}
      <div
        onPointerDown={handleResizePointerDown}
        title="Resize"
        style={{ touchAction: "none" }}
        className="absolute right-0 bottom-0 z-50 flex size-4 cursor-nwse-resize items-end justify-end p-0.5"
      >
        <span className="border-muted-foreground/40 size-2 border-r-2 border-b-2" />
      </div>
    </div>
  )
}
