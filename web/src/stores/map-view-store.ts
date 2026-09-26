import { create } from "zustand"
import { persist } from "zustand/middleware"

/** The map's current viewport as an EPSG:4326 bbox `[minX, minY, maxX, maxY]`. */
export type Bbox4326 = [number, number, number, number]

/**
 * The live map viewport, pushed from voltedge-map.tsx's moveend handler (the
 * OL Map is a component-local ref, never lifted into a store, so this is the
 * one computed value it publishes for siblings to read). This zustand store is
 * module-level, so it OUTLIVES the map component: leaving the map route unmounts
 * VoltEdgeMap, but the store keeps `center`/`zoom`, and the map seeds its View
 * from them on the next mount -- so navigating away and back returns to where
 * you left off instead of snapping to the admin default.
 *
 * `center`/`zoom` are ALSO persisted to localStorage (partialize below), so the
 * user's last location is restored ALWAYS -- not only across route navigation
 * but across full page reloads and new tabs too. The user's viewport therefore
 * always wins over the admin-configured default once they've moved the map even
 * once. `bbox` and `rotation` are deliberately NOT persisted: `bbox` is derived
 * from center/zoom on the next render, and rotation intentionally resets to
 * north-up on each fresh mount (see voltedge-map.tsx's init-effect cleanup).
 * All fields are null until the map has rendered once for the first time ever
 * (before that the map falls back to the admin default, and the export dialog
 * to a whole-dataset export).
 *
 * Readers: the map itself restores `center`/`zoom` on mount; the geographic
 * export dialog clips a "current view" export to `bbox`; the status bar shows
 * `zoom`.
 */
interface MapViewState {
  bbox: Bbox4326 | null
  /** Last viewport center as [lon, lat] in EPSG:4326. Persisted. */
  center: [number, number] | null
  /** Last viewport zoom. Persisted. */
  zoom: number | null
  /** Current view rotation in radians (0 = north up). Pushed live so the
   * reset-north button can appear/point while the map is being twisted.
   * Not persisted -- each fresh mount rebuilds the view north-up. */
  rotation: number
  setBbox: (bbox: Bbox4326) => void
  setCenter: (center: [number, number]) => void
  setZoom: (zoom: number) => void
  setRotation: (rotation: number) => void
}

export const useMapViewStore = create<MapViewState>()(
  persist(
    (set) => ({
      bbox: null,
      center: null,
      zoom: null,
      rotation: 0,
      setBbox: (bbox) => set({ bbox }),
      setCenter: (center) => set({ center }),
      setZoom: (zoom) => set({ zoom }),
      setRotation: (rotation) => set({ rotation }),
    }),
    {
      name: "voltedge-map-view",
      // Only the viewport worth surviving a reload -- last center + zoom.
      partialize: (s) => ({ center: s.center, zoom: s.zoom }),
    },
  ),
)
