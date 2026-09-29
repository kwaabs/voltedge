import { useEffect, useMemo, useState } from "react"
import type { ReactNode } from "react"
import { ChevronRight, List, ListTree } from "lucide-react"

import { Button } from "@/components/ui/button"
import { ancestorPathKeys, buildTreeLines, type TreeItem } from "@/lib/result-tree"
import { cn } from "@/lib/utils"
import { useAnalysisViewStore } from "@/stores/analysis-view-store"
import { useSelectionStore } from "@/stores/selection-store"
import type { TraceFeatureRef } from "@/stores/trace-store"

function shortId(id: string): string {
  return id.length > 8 ? `${id.slice(0, 8)}…` : id
}

function nodeKey(n: { feature_class: string; feature_id: string }): string {
  return `${n.feature_class}:${n.feature_id}`
}

/** The minimal per-node shape this table depends on directly: the identity it
 * keys and selects on, plus the optional single-parent pointer it threads the
 * tree from (absent -> the node roots directly, e.g. every nodal Y-bus node,
 * which has no spanning-tree parent). Everything else a panel wants to show is
 * read inside its own `columns` cell renderers, typed to the panel's own richer
 * node type -- so the radial/meshed panels pass LoadFlowNodeResult and the auto
 * panel passes the radial|nodal union, both satisfying this. */
export interface AnalysisNode {
  feature_class: string
  feature_id: string
  parent_feature_class?: string
  parent_feature_id?: string
}

/** One trailing column (everything after the shared Feature column): a header
 * label and a per-node cell renderer. Panels differ only in these -- the basic
 * and meshed panels list Level/Voltage/Current/Loading, the auto panel inserts a
 * resolved Name column first -- so the table body itself (flat rows or the
 * spanning tree) is written once here. */
export interface NodeColumn<N extends AnalysisNode> {
  header: string
  headerClassName?: string
  cell: (node: N) => ReactNode
}

/**
 * Shared node table for the load-flow family panels (basic / meshed / auto).
 * Renders the solved nodes either as a flat table or as the radial spanning tree
 * the sweep actually produced -- built client-side from each node's
 * parent_feature_* pointers (backend api/internal/powerflow stamps them; a nodal
 * Y-bus result has none and so reads flat even in tree mode). A View toggle
 * (Table | Tree) sits in the panel's own sub-header alongside whatever `toolbar`
 * content the panel passes (its engine/nominal info and Run again). The tree
 * view mirrors trace-result-table.tsx's own tree -- indent rails, a collapse
 * chevron with subtree counts, a Start badge on the root, and a hover highlight
 * that lights the route from any node back to the source -- so the two panels
 * read the same. Column layout is the only thing panels vary, passed as
 * `columns`; barriers (open devices that stopped the walk) always list flat
 * beneath the nodes, same "stopped here" distinction the flat table drew.
 */
export function LoadFlowNodesTable<N extends AnalysisNode>({
  nodes,
  barriers,
  overloadedKeys,
  rootKey,
  classNameFor,
  columns,
  featureSubline,
  overloadTitle = "Exceeds this element's own rating",
  barrierLabel = "Stopped here (open device)",
  toolbar,
  preTable,
  resized,
}: {
  nodes: N[]
  barriers: TraceFeatureRef[]
  overloadedKeys: Set<string>
  /** Key (feature_class:feature_id) of the sweep's start -- the tree root. */
  rootKey: string
  classNameFor: (code: string) => string
  columns: NodeColumn<N>[]
  /** Optional extra line under the class name in the Feature cell (e.g. circuit ref). */
  featureSubline?: (node: N) => ReactNode
  overloadTitle?: string
  barrierLabel?: string
  /** Panel-specific header content (engine/nominal info + Run again). */
  toolbar?: ReactNode
  /** Optional content above the node table, inside the same scroll region (e.g.
   * the meshed panel's closed-loops table), so it scrolls with the nodes. */
  preTable?: ReactNode
  resized: boolean
}) {
  const view = useAnalysisViewStore((s) => s.view)
  const setView = useAnalysisViewStore((s) => s.setView)
  // Collapsed subtree roots and the hovered row, both reset whenever the node
  // set changes (a fresh run starts fully expanded, nothing hovered).
  const [collapsed, setCollapsed] = useState<Set<string>>(new Set())
  const [hoveredKey, setHoveredKey] = useState<string | null>(null)
  useEffect(() => {
    setCollapsed(new Set())
    setHoveredKey(null)
  }, [nodes])

  const items = useMemo<TreeItem<N>[]>(
    () =>
      nodes.map((n) => ({
        key: nodeKey(n),
        parentKey:
          n.parent_feature_class && n.parent_feature_id
            ? `${n.parent_feature_class}:${n.parent_feature_id}`
            : null,
        data: n,
      })),
    [nodes],
  )

  const treeLines = view === "tree" ? buildTreeLines(items, rootKey, collapsed) : null
  const activePathKeys = treeLines ? ancestorPathKeys(treeLines, hoveredKey) : new Set<string>()
  const collapsibleKeys = useMemo(
    () =>
      view === "tree"
        ? buildTreeLines(items, rootKey, new Set())
            .filter((l) => l.hasChildren)
            .map((l) => l.key)
        : [],
    [view, items, rootKey],
  )
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

  // One node row, shared by both views. `tree` is set only in tree mode: it
  // indents the Feature cell by depth, prefixes rails + a chevron, and badges the
  // root as Start -- every trailing cell is identical, so the views never drift.
  const renderNodeRow = (
    node: N,
    tree?: {
      depth: number
      hasChildren: boolean
      descendantCount: number
      isRoot: boolean
      railColumns: boolean[]
      isLastChild: boolean
    },
  ) => {
    const key = nodeKey(node)
    const overloaded = overloadedKeys.has(key)
    const isCollapsed = collapsed.has(key)
    const onPath = !!tree && activePathKeys.has(key)
    const subline = featureSubline?.(node)
    return (
      <tr
        key={key}
        onClick={() => useSelectionStore.getState().select(node.feature_class, node.feature_id, { focus: true })}
        onMouseEnter={tree ? () => setHoveredKey(key) : undefined}
        onMouseLeave={tree ? () => setHoveredKey(null) : undefined}
        className={cn(
          "hover:bg-accent cursor-pointer border-b last:border-b-0",
          onPath && !overloaded && "bg-primary/5",
          overloaded && "bg-destructive/10 text-destructive hover:bg-destructive/15",
        )}
        title={overloaded ? overloadTitle : undefined}
      >
        <td className={cn(tree ? "p-0" : "px-3 py-1.5")}>
          {tree ? (
            <div className="flex items-stretch">
              <span className="w-2 shrink-0" />
              {tree.railColumns.map((rail, i) => (
                <span key={i} className="relative w-4 shrink-0 self-stretch">
                  {rail && (
                    <span
                      className={cn("absolute top-0 left-1/2 h-full w-px", onPath ? "bg-primary" : "bg-border")}
                    />
                  )}
                </span>
              ))}
              {tree.depth > 0 && (
                <span className="relative w-4 shrink-0 self-stretch">
                  <span
                    className={cn(
                      "absolute top-0 left-1/2 w-px",
                      tree.isLastChild ? "h-1/2" : "h-full",
                      onPath ? "bg-primary" : "bg-border",
                    )}
                  />
                  <span className={cn("absolute top-1/2 left-1/2 h-px w-1/2", onPath ? "bg-primary" : "bg-border")} />
                </span>
              )}
              <div className="flex min-w-0 flex-col py-1.5 pr-3 pl-0.5">
                <div className="flex min-w-0 items-center gap-1">
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
                  <span className={cn("truncate", onPath && "font-medium")}>{classNameFor(node.feature_class)}</span>
                  {tree.isRoot && (
                    <span className="rounded bg-primary/15 text-primary shrink-0 px-1.5 py-0.5 text-[11px] font-medium">
                      Start
                    </span>
                  )}
                  {tree.hasChildren && tree.descendantCount > 0 && (
                    <span
                      className="text-muted-foreground shrink-0 text-xs tabular-nums"
                      title={`${tree.descendantCount} node${tree.descendantCount === 1 ? "" : "s"} below this branch`}
                    >
                      ({tree.descendantCount})
                    </span>
                  )}
                </div>
                <div className="text-muted-foreground pl-5 font-mono text-xs">{shortId(node.feature_id)}</div>
                {subline && <div className="text-muted-foreground pl-5 text-xs">{subline}</div>}
              </div>
            </div>
          ) : (
            <>
              <div>{classNameFor(node.feature_class)}</div>
              <div className="text-muted-foreground font-mono text-xs">{shortId(node.feature_id)}</div>
              {subline && <div className="text-muted-foreground text-xs">{subline}</div>}
            </>
          )}
        </td>
        {columns.map((col, i) => (
          <td key={i} className="px-3 py-1.5">
            {col.cell(node)}
          </td>
        ))}
      </tr>
    )
  }

  return (
    <>
      <div className="text-muted-foreground flex items-center gap-2 border-b px-3 py-1.5 text-xs">
        <div className="border-border/70 flex shrink-0 items-center rounded-md border p-0.5">
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
            title="Tree view (by feed hierarchy)"
          >
            <ListTree className="size-3.5" />
            <span className="sr-only">Tree view</span>
          </Button>
        </div>
        {view === "tree" && collapsibleKeys.length > 0 && (
          <Button
            variant="ghost"
            size="icon"
            className="size-6 shrink-0"
            onClick={() => (allExpanded ? collapseAll() : expandAll())}
            title={allExpanded ? "Collapse all" : "Expand all"}
          >
            <ChevronRight className={cn("size-3.5 transition-transform", allExpanded && "rotate-90")} />
            <span className="sr-only">{allExpanded ? "Collapse all" : "Expand all"}</span>
          </Button>
        )}
        {toolbar && <div className="flex min-w-0 flex-1 items-center gap-x-4 gap-y-1">{toolbar}</div>}
      </div>

      <div className={cn("overflow-y-auto", resized && "min-h-0 flex-1")}>
        {preTable}
        <table className="w-full text-sm">
          <thead className="text-muted-foreground sticky top-0 border-b text-left text-xs">
            <tr>
              <th className="bg-popover px-3 py-1.5 font-medium">Feature</th>
              {columns.map((col, i) => (
                <th key={i} className={cn("bg-popover px-3 py-1.5 font-medium", col.headerClassName)}>
                  {col.header}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {treeLines
              ? treeLines.map((line) =>
                  renderNodeRow(line.data, {
                    depth: line.depth,
                    hasChildren: line.hasChildren,
                    descendantCount: line.descendantCount,
                    isRoot: line.isRoot,
                    railColumns: line.railColumns,
                    isLastChild: line.isLastChild,
                  }),
                )
              : nodes.map((node) => renderNodeRow(node))}
            {barriers.map((b) => (
              <tr
                key={nodeKey(b)}
                onClick={() => useSelectionStore.getState().select(b.feature_class, b.feature_id, { focus: true })}
                className="bg-muted/50 text-muted-foreground hover:bg-muted cursor-pointer border-b last:border-b-0"
                title={barrierLabel}
              >
                <td className="px-3 py-1.5">
                  <div>{classNameFor(b.feature_class)}</div>
                  <div className="font-mono text-xs">{shortId(b.feature_id)}</div>
                </td>
                <td className="px-3 py-1.5" colSpan={columns.length}>
                  {barrierLabel}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </>
  )
}
