import { useMemo } from "react"
import { useQueries } from "@tanstack/react-query"
import { X } from "lucide-react"

import { Button } from "@/components/ui/button"
import { type Connection } from "@/hooks/use-connections"
import { useAllSymbology } from "@/hooks/use-all-symbology"
import { type DiagramPlacement, useDiagramPlacements } from "@/hooks/use-diagrams"
import { type FeatureRecord } from "@/hooks/use-feature"
import { apiFetch } from "@/lib/api"
import { iconDataUri } from "@/lib/symbology-icons"
import { resolveSymbology } from "@/lib/symbology"
import { useDiagramStore } from "@/stores/diagram-store"
import { useSelectionStore } from "@/stores/selection-store"

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
export function DiagramCanvas() {
  const openDiagramId = useDiagramStore((s) => s.openDiagramId)
  const close = useDiagramStore((s) => s.close)
  const { data: placementsData, isPending, isError } = useDiagramPlacements(openDiagramId ?? undefined)
  const rules = useAllSymbology()
  const placements = useMemo(() => placementsData?.placements ?? [], [placementsData])

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
    placements.forEach((p) => map.set(placementKey(p.feature_class, p.feature_id), p))
    return map
  }, [placements])

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
    if (placements.length === 0) return { minX: 0, minY: 0, maxX: 400, maxY: 300 }
    const xs = placements.map((p) => p.x)
    const ys = placements.map((p) => p.y)
    return { minX: Math.min(...xs), maxX: Math.max(...xs), minY: Math.min(...ys), maxY: Math.max(...ys) }
  }, [placements])

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
        <Button variant="ghost" size="icon" className="size-7" onClick={close}>
          <X className="size-4" />
          <span className="sr-only">Close diagram</span>
        </Button>
      </div>
      <div className="min-h-0 flex-1 overflow-auto">
        {isPending ? (
          <div className="text-muted-foreground p-4 text-sm">Loading diagram...</div>
        ) : isError ? (
          <div className="text-destructive p-4 text-sm">Could not load this diagram.</div>
        ) : placements.length === 0 ? (
          <div className="text-muted-foreground p-4 text-sm">Nothing placed on this diagram yet.</div>
        ) : (
          <svg viewBox={viewBox} className="h-full w-full">
            {edges.map((edge) => (
              <line
                key={edge.id}
                x1={edge.from.x}
                y1={edge.from.y}
                x2={edge.to.x}
                y2={edge.to.y}
                style={{ stroke: "var(--muted-foreground)" }}
                strokeWidth={2}
              />
            ))}
            {placements.map((placement, i) => {
              const feature = featureResults[i]?.data
              const classRules = rules[placement.feature_class]
              const def =
                resolveSymbology(classRules, feature?.subtype, feature?.attributes ?? {}, "diagram") ??
                resolveSymbology(classRules, feature?.subtype, feature?.attributes ?? {}, "map")
              const icon = iconDataUri(def?.icon_ref, def?.color)
              return (
                <g
                  key={placement.id}
                  transform={`translate(${placement.x}, ${placement.y})`}
                  className="cursor-pointer"
                  onClick={() => useSelectionStore.getState().select(placement.feature_class, placement.feature_id)}
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
