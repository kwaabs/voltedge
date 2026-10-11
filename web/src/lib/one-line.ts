import { useQuery } from "@tanstack/react-query"

import { apiFetch } from "@/lib/api"
import type { Bbox4326 } from "@/stores/map-view-store"
import type { FeatureRef } from "@/stores/selection-store"

/** A point feature on the one-line, at abstract (x,y). */
export interface OneLineNode {
  feature_class: string
  feature_id: string
  subtype?: number
  x: number
  y: number
  device_state?: string
  circuit_ref?: string
  rated_kva?: number
  demand_kw?: number
}

/** A wire on the one-line -- a conductor (carries its class/id) or a plain link. */
export interface OneLineConnector {
  feature_class?: string
  feature_id?: string
  subtype?: number
  circuit_ref?: string
  network?: string
  /** The point-node feature ids this wire joins (its path runs between them).
   * Used to re-route the wire live when a node is dragged in the editor. */
  endpoints?: string[]
  path: [number, number][]
}

export interface OneLinePayload {
  nodes: OneLineNode[]
  connectors: OneLineConnector[]
  truncated: boolean
  node_count: number
}

/** The palette conductors are coloured by (hashed from their circuit_ref) on both
 * the SVG view and the DXF export, so the two match. */
export const CIRCUIT_COLORS = [
  "#2563eb", "#dc2626", "#16a34a", "#9333ea", "#ea580c",
  "#0891b2", "#ca8a04", "#db2777", "#4f46e5", "#65a30d",
]

/** A stable colour for a circuit ref (undefined when there is none). */
export function circuitColor(ref: string | undefined): string | undefined {
  if (!ref) return undefined
  let h = 0
  for (let i = 0; i < ref.length; i++) h = (h * 31 + ref.charCodeAt(i)) >>> 0
  return CIRCUIT_COLORS[h % CIRCUIT_COLORS.length]
}

/** Where a one-line is built from: an explicit selection, or the current view. */
export type OneLineSource =
  | { kind: "selection"; refs: FeatureRef[] }
  | { kind: "view"; bbox: Bbox4326 }

function sourceKey(source: OneLineSource | null): string {
  if (!source) return "none"
  if (source.kind === "view") return `view:${source.bbox.map((n) => n.toFixed(5)).join(",")}`
  return `selection:${source.refs.map((r) => `${r.featureClassCode}:${r.featureId}`).sort().join(",")}`
}

/**
 * Fetches the EPHEMERAL one-line for a selection or the current view (POST
 * /schematic/one-line). Nothing is persisted -- the server lays the graph out and
 * returns it as data; the client renders and exports it.
 */
export function useOneLine(source: OneLineSource | null, enabled: boolean) {
  return useQuery({
    queryKey: ["one-line", sourceKey(source)],
    queryFn: () => {
      if (!source) throw new Error("no source")
      const body =
        source.kind === "selection"
          ? { feature_refs: source.refs.map((r) => ({ feature_class: r.featureClassCode, feature_id: r.featureId })) }
          : {
              bbox: {
                min_x: source.bbox[0],
                min_y: source.bbox[1],
                max_x: source.bbox[2],
                max_y: source.bbox[3],
              },
            }
      return apiFetch<OneLinePayload>("/schematic/one-line", { method: "POST", body: JSON.stringify(body) })
    },
    enabled: enabled && source != null,
    staleTime: 30_000,
  })
}
