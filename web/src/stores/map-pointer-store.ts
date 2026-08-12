import { create } from "zustand"

/**
 * The mouse's current map position, in EPSG:4326 (lon/lat) -- written by
 * VoltEdgeMap's pointermove/pointerleave listeners, read by StatusBar.
 * A store rather than a prop or context because the writer (the map
 * canvas) and the reader (the status bar) are siblings, not parent/
 * child, and this changes on essentially every mouse move -- exactly
 * the kind of ephemeral, frequently-updating value a plain module-level
 * store handles better than threading through component state.
 */
interface MapPointerState {
  lon: number | null
  lat: number | null
  setPosition: (lon: number, lat: number) => void
  clearPosition: () => void
}

export const useMapPointerStore = create<MapPointerState>((set) => ({
  lon: null,
  lat: null,
  setPosition: (lon, lat) => set({ lon, lat }),
  clearPosition: () => set({ lon: null, lat: null }),
}))
