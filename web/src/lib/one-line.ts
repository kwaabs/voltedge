import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"

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
  /** The container/assembly group (OneLineGroup id) this node belongs to, if any. */
  group_id?: string
  /** Symbology domain-field values (operations path) so the client resolves the same
   * rule the map does -- an attribute-driven class can't match on subtype alone. */
  attributes?: Record<string, unknown>
}

/** A fenceable grouping of nodes -- a container (substation/bay) or an assembly. */
export interface OneLineGroup {
  id: string
  label: string
  kind: "container" | "assembly"
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
  /** Symbology domain-field values (operations path) -- colours the wire by its real
   * symbology rule (e.g. by voltage), not just its circuit ref. */
  attributes?: Record<string, unknown>
}

export interface OneLinePayload {
  nodes: OneLineNode[]
  connectors: OneLineConnector[]
  /** Container/assembly groupings to fence (operations one-line only). */
  groups?: OneLineGroup[]
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

/**
 * Fetches the live WHOLE-NETWORK one-line (GET /schematic/network-one-line) -- the
 * ADMS operations schematic's source. The layout is recomputed from the current
 * network each call (deterministic for a fixed network), so the operating picture is
 * always current with no persisted diagram to keep in sync. SSE ("operations") +
 * the short staleTime keep it live as devices operate.
 */
export function useNetworkOneLineLive(enabled = true) {
  return useQuery({
    queryKey: ["network-one-line-live"],
    queryFn: () => apiFetch<OneLinePayload>("/schematic/network-one-line"),
    enabled,
    staleTime: 15_000,
    placeholderData: (prev) => prev,
  })
}

/** One operator position nudge for the live operations one-line (ops-owned layout,
 * NOT a GIS diagram -- it only moves where a feature is drawn). */
export interface OneLineOverride {
  feature_class: string
  feature_id: string
  x: number
  y: number
}

const OVERRIDES_KEY = ["oneline-overrides"]

/** Every operator position nudge for the operations one-line. */
export function useOneLineOverrides() {
  return useQuery({
    queryKey: OVERRIDES_KEY,
    queryFn: () => apiFetch<{ overrides: OneLineOverride[] }>("/oneline-layout/overrides"),
    staleTime: 30_000,
  })
}

/** Save (or move) one feature's nudge. operations:write. */
export function useSaveOneLineOverride() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (o: OneLineOverride) =>
      apiFetch<OneLineOverride>("/oneline-layout/overrides", { method: "PUT", body: JSON.stringify(o) }),
    onSuccess: () => qc.invalidateQueries({ queryKey: OVERRIDES_KEY }),
  })
}

/** Clear every nudge -- reset the operations one-line to the pure computed layout. */
export function useResetOneLineOverrides() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: () => apiFetch<void>("/oneline-layout/overrides", { method: "DELETE" }),
    onSuccess: () => qc.invalidateQueries({ queryKey: OVERRIDES_KEY }),
  })
}

/** One operator-routed wire for the operations one-line: its INTERMEDIATE bend vertices.
 * The renderer composes the path as [live start node, ...vertices, live end node], so the
 * endpoints follow the nodes while the bends stick. */
export interface OneLineWireOverride {
  wire_key: string
  vertices: [number, number][]
}

const WIRE_OVERRIDES_KEY = ["oneline-wire-overrides"]

/** Every hand-routed wire for the operations one-line. */
export function useOneLineWireOverrides() {
  return useQuery({
    queryKey: WIRE_OVERRIDES_KEY,
    queryFn: () => apiFetch<{ wires: OneLineWireOverride[] }>("/oneline-layout/wires"),
    staleTime: 30_000,
  })
}

/** Save (or reset, when vertices is empty) one wire's bends. operations:write. */
export function useSaveWireOverride() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (o: OneLineWireOverride) =>
      apiFetch<OneLineWireOverride | void>("/oneline-layout/wires", { method: "PUT", body: JSON.stringify(o) }),
    onSuccess: () => qc.invalidateQueries({ queryKey: WIRE_OVERRIDES_KEY }),
  })
}

/** Clear every hand-routed wire -- straighten all wires back to the auto route. */
export function useResetWireOverrides() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: () => apiFetch<void>("/oneline-layout/wires", { method: "DELETE" }),
    onSuccess: () => qc.invalidateQueries({ queryKey: WIRE_OVERRIDES_KEY }),
  })
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
