import { create } from "zustand"

/** The map's current viewport as an EPSG:4326 bbox `[minX, minY, maxX, maxY]`. */
export type Bbox4326 = [number, number, number, number]

/**
 * The live map viewport, pushed from voltedge-map.tsx's moveend handler (the
 * OL Map is a component-local ref, never lifted into a store, so this is the
 * one computed value it publishes for siblings to read). Ephemeral view state,
 * not persisted -- it only means anything for what the map is showing right
 * now. The sole reader today is the geographic export dialog, which clips a
 * "current view" export to this bbox. `bbox` is null until the map has rendered
 * once (so the dialog can fall back to a whole-dataset export if it's somehow
 * opened before the first render).
 */
interface MapViewState {
  bbox: Bbox4326 | null
  setBbox: (bbox: Bbox4326) => void
}

export const useMapViewStore = create<MapViewState>((set) => ({
  bbox: null,
  setBbox: (bbox) => set({ bbox }),
}))
