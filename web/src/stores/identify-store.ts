import { create } from "zustand"

import type { ServiceType } from "@/hooks/use-service-layers"

/**
 * The click-to-identify session for external service layers (Slice 4). A click on
 * the map that misses every internal DB feature but lands where a visible,
 * queryable service layer sits (WMS/Esri MapServer via a server round-trip, or
 * WFS/Esri-feature/vector-tile via a client-side hit-test) opens this panel with
 * whatever those layers report at that point. It is deliberately separate from
 * the DB-coupled FeaturePanel: these features come from a third party and have no
 * VoltEdge id, edit path, or relationships -- they are read-only attributes.
 *
 * One session at a time: a new identifying click replaces the previous result
 * set. Not persisted -- an identify result is tied to a specific click and should
 * not survive a reload.
 */
export interface IdentifyFeatureResult {
  /** A human label derived from a name-ish attribute, when one is present. */
  label?: string
  /** The feature's raw attributes as reported by the service. */
  properties: Record<string, unknown>
}

export interface IdentifyLayerGroup {
  layerId: string
  layerLabel: string
  serviceType: ServiceType
  features: IdentifyFeatureResult[]
  /** Set when this layer's identify request failed (network/parse); its features
   * are then empty and the panel shows the error in place of results. */
  error?: string
}

interface IdentifyState {
  /** Whether the identify panel is showing (opened by an identifying click). */
  open: boolean
  /** True while raster identify requests are still in flight. */
  loading: boolean
  /** The clicked point in [lon, lat] for the panel header, or null. */
  lonLat: [number, number] | null
  /** Per-layer results, in the catalog order the click collected them. */
  groups: IdentifyLayerGroup[]
  /** Open the panel for a fresh click: loading, no results yet. */
  begin: (lonLat: [number, number]) => void
  /** Land the final result set and stop the spinner. */
  resolve: (groups: IdentifyLayerGroup[]) => void
  /** Close and clear (X button / Escape). */
  close: () => void
}

export const useIdentifyStore = create<IdentifyState>((set) => ({
  open: false,
  loading: false,
  lonLat: null,
  groups: [],
  begin: (lonLat) => set({ open: true, loading: true, lonLat, groups: [] }),
  resolve: (groups) => set({ loading: false, groups }),
  close: () => set({ open: false, loading: false, groups: [], lonLat: null }),
}))
