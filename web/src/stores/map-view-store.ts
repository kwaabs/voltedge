import { create } from "zustand"

/** The map's current viewport as an EPSG:4326 bbox `[minX, minY, maxX, maxY]`. */
export type Bbox4326 = [number, number, number, number]

/**
 * The live map viewport, pushed from voltedge-map.tsx's moveend handler (the
 * OL Map is a component-local ref, never lifted into a store, so this is the
 * one computed value it publishes for siblings to read). Ephemeral view state,
 * not persisted -- it only means anything for what the map is showing right
 * now. Readers: the geographic export dialog clips a "current view" export to
 * `bbox`; the status bar shows `zoom`. Both are null until the map has rendered
 * once (so the export dialog can fall back to a whole-dataset export if it's
 * somehow opened before the first render).
 */
interface MapViewState {
  bbox: Bbox4326 | null
  zoom: number | null
  setBbox: (bbox: Bbox4326) => void
  setZoom: (zoom: number) => void
}

export const useMapViewStore = create<MapViewState>((set) => ({
  bbox: null,
  zoom: null,
  setBbox: (bbox) => set({ bbox }),
  setZoom: (zoom) => set({ zoom }),
}))
