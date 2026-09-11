import { create } from "zustand"
import { persist } from "zustand/middleware"

interface MapAnnotationsState {
  /** Whether the map-annotation (redlining) overlay is drawn at all -- the
   * master switch that also gates the annotations fetch (see
   * useMapAnnotations). Persisted, so the choice survives reloads like the
   * feature-layer and territory-overlay visibility do. Defaults on: unlike the
   * territory overlay (large boundaries, most sessions never open it),
   * annotations are the point of the redline layer and are meant to be seen as
   * soon as they exist. */
  visible: boolean
  toggle: () => void
}

/**
 * The map-annotation (redlining) overlay's visibility -- a single toggleable
 * client layer of free-form markup (text/box/line/arrow), the geographic
 * sibling of the diagram furniture. Kept out of the feature-layer store
 * (layers-store.ts, keyed by Martin source id) because this is one fixed
 * overlay, not a per-feature-class layer -- the same reasoning as the territory
 * overlay store. Slice 4 will add drawing/editing on top of this read layer.
 */
export const useMapAnnotationsStore = create<MapAnnotationsState>()(
  persist(
    (set) => ({
      visible: true,
      toggle: () => set((s) => ({ visible: !s.visible })),
    }),
    { name: "voltedge-map-annotations" },
  ),
)
