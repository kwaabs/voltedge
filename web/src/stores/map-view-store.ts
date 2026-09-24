import { create } from "zustand"

/** The map's current viewport as an EPSG:4326 bbox `[minX, minY, maxX, maxY]`. */
export type Bbox4326 = [number, number, number, number]

/**
 * The live map viewport, pushed from voltedge-map.tsx's moveend handler (the
 * OL Map is a component-local ref, never lifted into a store, so this is the
 * one computed value it publishes for siblings to read). This zustand store is
 * module-level, so it OUTLIVES the map component: leaving the map route unmounts
 * VoltEdgeMap, but the store keeps `center`/`zoom`, and the map seeds its View
 * from them on the next mount -- so navigating away and back returns to where
 * you left off instead of snapping to the admin default. All fields are null
 * until the map has rendered once (before that the map falls back to the admin
 * default, and the export dialog to a whole-dataset export).
 *
 * Readers: the map itself restores `center`/`zoom` on mount; the geographic
 * export dialog clips a "current view" export to `bbox`; the status bar shows
 * `zoom`. In-memory only (not localStorage) -- a full page reload starts fresh
 * from the admin default, which is intended.
 */
interface MapViewState {
  bbox: Bbox4326 | null
  /** Last viewport center as [lon, lat] in EPSG:4326. */
  center: [number, number] | null
  zoom: number | null
  /** Current view rotation in radians (0 = north up). Pushed live so the
   * reset-north button can appear/point while the map is being twisted. */
  rotation: number
  setBbox: (bbox: Bbox4326) => void
  setCenter: (center: [number, number]) => void
  setZoom: (zoom: number) => void
  setRotation: (rotation: number) => void
}

export const useMapViewStore = create<MapViewState>((set) => ({
  bbox: null,
  center: null,
  zoom: null,
  rotation: 0,
  setBbox: (bbox) => set({ bbox }),
  setCenter: (center) => set({ center }),
  setZoom: (zoom) => set({ zoom }),
  setRotation: (rotation) => set({ rotation }),
}))
