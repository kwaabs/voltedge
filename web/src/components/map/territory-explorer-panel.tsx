import { useQuery } from "@tanstack/react-query"
import { BarChart3, ChevronDown, ChevronRight, Layers, Loader2, MapPin, Search } from "lucide-react"
import { useMemo, useState } from "react"

import { Button } from "@/components/ui/button"
import { FloatingPanel } from "@/components/ui/floating-panel"
import { useFeatureClasses } from "@/hooks/use-feature-classes"
import { type TerritorySummary, useMyTerritories } from "@/hooks/use-territories"
import { apiFetch } from "@/lib/api"
import { cn } from "@/lib/utils"
import { useQueryStore } from "@/stores/query-store"
import { useTerritoryExplorerStore } from "@/stores/territory-explorer-store"

/** One territory with its accessible children nested under it. */
interface TreeNode extends TerritorySummary {
  children: TreeNode[]
}

/** One class's feature count within the selected territory (its group-less
 * /aggregate total), the same shape the query panel's All-classes breakdown
 * uses. */
interface ClassCount {
  code: string
  name: string
  count: number
}

/**
 * Builds the forest from the flat accessible list. A node whose parent isn't in
 * the set -- the user was granted a district but not the region above it --
 * becomes a root, so the tree shows exactly the subtree the grant covers rather
 * than dangling orphans. Sorted top level first (level_rank), then by name, at
 * every depth.
 */
function buildForest(items: TerritorySummary[]): TreeNode[] {
  const byId = new Map<string, TreeNode>()
  for (const item of items) byId.set(item.id, { ...item, children: [] })
  const roots: TreeNode[] = []
  for (const node of byId.values()) {
    const parent = node.parent_id ? byId.get(node.parent_id) : undefined
    if (parent) parent.children.push(node)
    else roots.push(node)
  }
  const sortRec = (nodes: TreeNode[]) => {
    nodes.sort((a, b) => a.level_rank - b.level_rank || a.name.localeCompare(b.name))
    for (const n of nodes) sortRec(n.children)
  }
  sortRec(roots)
  return roots
}

function TerritoryTreeRow({
  node,
  depth,
  selectedId,
  onSelect,
}: {
  node: TreeNode
  depth: number
  selectedId: string | null
  onSelect: (id: string) => void
}) {
  const [expanded, setExpanded] = useState(true)
  const hasChildren = node.children.length > 0
  const isSelected = node.id === selectedId

  return (
    <div>
      <div
        className={cn(
          "hover:bg-accent/60 flex items-center gap-1 rounded py-1 pr-1 text-sm",
          isSelected && "bg-accent text-accent-foreground",
        )}
        style={{ paddingLeft: depth * 14 + 4 }}
      >
        {hasChildren ? (
          <button
            type="button"
            onClick={() => setExpanded((v) => !v)}
            className="text-muted-foreground hover:text-foreground shrink-0"
            aria-label={expanded ? "Collapse" : "Expand"}
          >
            {expanded ? <ChevronDown className="size-3.5" /> : <ChevronRight className="size-3.5" />}
          </button>
        ) : (
          <span className="inline-block size-3.5 shrink-0" />
        )}
        <button
          type="button"
          onClick={() => onSelect(node.id)}
          className="flex min-w-0 flex-1 items-center gap-1.5 text-left"
          title={`${node.name} — ${node.level_name}`}
        >
          <span className="min-w-0 truncate">{node.name}</span>
          <span className="text-muted-foreground shrink-0 text-[10px] uppercase">{node.level_code}</span>
        </button>
      </div>
      {hasChildren && expanded && (
        <div>
          {node.children.map((child) => (
            <TerritoryTreeRow
              key={child.id}
              node={child}
              depth={depth + 1}
              selectedId={selectedId}
              onSelect={onSelect}
            />
          ))}
        </div>
      )}
    </div>
  )
}

/**
 * The Territory Explorer: a floating, browse-only lens (mirrors the query
 * builder's FloatingPanel shell) over the territories the current user may
 * access and the features inside them. The top pane is a tree of the caller's
 * accessible territories (GET /territories/mine); selecting one (a) fits the
 * map to its boundary (via territory-explorer-store's focusToken, honoured in
 * voltedge-map) and (b) fans a group-less /aggregate across every geometric
 * class, scoped to that territory, to list per-class feature counts. Each count
 * row can hand off to the query builder pre-scoped to that class + territory.
 *
 * Only mounted while the store's `open` is true (map.tsx), so nothing fetches
 * until the panel is actually opened.
 */
export function TerritoryExplorerPanel() {
  const selectedId = useTerritoryExplorerStore((s) => s.selectedId)
  const select = useTerritoryExplorerStore((s) => s.select)
  const { data, isPending, isError } = useMyTerritories()
  const { data: classesData } = useFeatureClasses()

  const forest = useMemo(() => buildForest(data?.territories ?? []), [data])
  const selected = useMemo(
    () => (data?.territories ?? []).find((t) => t.id === selectedId) ?? null,
    [data, selectedId],
  )

  // Only geometric classes have features to count/zoom to -- same filter the
  // query panel's All-classes breakdown uses.
  const queryableClasses = useMemo(
    () => (classesData?.feature_classes ?? []).filter((c) => c.geometry_type !== "none"),
    [classesData],
  )

  // Per-class counts for the selected territory: fan a group-less /aggregate
  // out across every geometric class under a territory scope. A class the user
  // can't read (or that errors) contributes a zero rather than failing the
  // sweep, then zero-counts are dropped and the rest sorted by size -- the
  // "what, and how much of each, is in this area" answer.
  const counts = useQuery({
    queryKey: ["territory-explorer-counts", selectedId, queryableClasses.length],
    enabled: Boolean(selectedId) && queryableClasses.length > 0,
    staleTime: 30_000,
    queryFn: async () => {
      const scope = { kind: "territory", territory_id: selectedId }
      const rows = await Promise.all(
        queryableClasses.map((c) =>
          apiFetch<{ total: number }>(`/features/${c.code}/aggregate`, {
            method: "POST",
            body: JSON.stringify({ scope }),
          })
            .then((r) => ({ code: c.code, name: c.name, count: r.total }))
            .catch(() => ({ code: c.code, name: c.name, count: 0 })),
        ),
      )
      return rows.filter((r) => r.count > 0).sort((a, b) => b.count - a.count) as ClassCount[]
    },
  })

  const totalFeatures = (counts.data ?? []).reduce((sum, r) => sum + r.count, 0)

  return (
    <FloatingPanel
      title="Territory Explorer"
      onClose={() => useTerritoryExplorerStore.getState().closePanel()}
      initialRect={{ x: 16, y: 60, width: 360, height: 520 }}
      minWidth={300}
    >
      <div className="flex h-full flex-col">
        {/* Accessible-territory tree */}
        <div className="min-h-0 flex-1 overflow-auto p-2">
          <div className="text-muted-foreground mb-1 flex items-center gap-1.5 px-1 text-xs font-medium">
            <Layers className="size-3.5" /> Your territories
          </div>
          {isPending ? (
            <div className="text-muted-foreground flex items-center gap-2 p-2 text-sm">
              <Loader2 className="size-4 animate-spin" /> Loading…
            </div>
          ) : isError ? (
            <div className="text-destructive p-2 text-sm">Couldn't load territories.</div>
          ) : forest.length === 0 ? (
            <div className="text-muted-foreground p-2 text-sm">No territories available to you.</div>
          ) : (
            forest.map((node) => (
              <TerritoryTreeRow key={node.id} node={node} depth={0} selectedId={selectedId} onSelect={select} />
            ))
          )}
        </div>

        {/* Feature breakdown for the selected territory */}
        <div className="min-h-0 flex-1 overflow-auto border-t p-2">
          {!selected ? (
            <div className="text-muted-foreground flex h-full items-center justify-center p-4 text-center text-sm">
              Select a territory to see the features inside it.
            </div>
          ) : (
            <>
              <div className="mb-2 flex items-start gap-1.5 px-1">
                <MapPin className="text-primary mt-0.5 size-3.5 shrink-0" />
                <div className="min-w-0">
                  <div className="truncate text-sm font-medium">{selected.name}</div>
                  <div className="text-muted-foreground text-xs">
                    {selected.level_name}
                    {counts.data ? ` · ${totalFeatures.toLocaleString()} features` : ""}
                  </div>
                </div>
              </div>
              {counts.isFetching ? (
                <div className="text-muted-foreground flex items-center gap-2 p-2 text-sm">
                  <Loader2 className="size-4 animate-spin" /> Counting features…
                </div>
              ) : counts.isError ? (
                <div className="text-destructive p-2 text-sm">Couldn't count features.</div>
              ) : (counts.data ?? []).length === 0 ? (
                <div className="text-muted-foreground p-2 text-sm">No features in this territory.</div>
              ) : (
                <ul className="flex flex-col gap-0.5">
                  {counts.data!.map((row) => (
                    <li
                      key={row.code}
                      className="group hover:bg-accent/60 flex items-center gap-2 rounded px-2 py-1 text-sm"
                    >
                      <BarChart3 className="text-muted-foreground size-3.5 shrink-0" />
                      <span className="min-w-0 flex-1 truncate">{row.name}</span>
                      <span className="text-muted-foreground tabular-nums">{row.count.toLocaleString()}</span>
                      <Button
                        variant="ghost"
                        size="icon"
                        className="size-6 opacity-0 group-hover:opacity-100"
                        title={`View ${row.name} in ${selected.name} in the query builder`}
                        onClick={() =>
                          useQueryStore.getState().openWithPreset({ classCode: row.code, territoryId: selected.id })
                        }
                      >
                        <Search className="size-3.5" />
                        <span className="sr-only">View in query builder</span>
                      </Button>
                    </li>
                  ))}
                </ul>
              )}
            </>
          )}
        </div>
      </div>
    </FloatingPanel>
  )
}
