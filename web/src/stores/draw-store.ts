import { create } from "zustand"

export interface DraftFeature {
  featureClassCode: string
  /** GeoJSON geometry in EPSG:4326, ready to send straight to POST /features/{code}. */
  geometry: { type: string; coordinates: unknown }
}

/**
 * Not persisted -- an in-progress draw or an unsaved draft has no
 * meaning across a reload. Two distinct states, not one: `drawing` is
 * "waiting for a click on the map to place the geometry" (the map owns
 * this -- see voltedge-map.tsx's draw effect); `draft` is "geometry
 * placed, waiting on the create form" (feature-panel.tsx owns this).
 * They're mutually exclusive by construction -- finishDrawing moves
 * straight from one to the other.
 */
interface DrawState {
  drawingFeatureClassCode: string | null
  draft: DraftFeature | null
  startDrawing: (featureClassCode: string) => void
  cancelDrawing: () => void
  finishDrawing: (geometry: DraftFeature["geometry"]) => void
  clearDraft: () => void
}

export const useDrawStore = create<DrawState>((set, get) => ({
  drawingFeatureClassCode: null,
  draft: null,
  startDrawing: (featureClassCode) => set({ drawingFeatureClassCode: featureClassCode, draft: null }),
  cancelDrawing: () => set({ drawingFeatureClassCode: null }),
  finishDrawing: (geometry) => {
    const featureClassCode = get().drawingFeatureClassCode
    if (!featureClassCode) return
    set({ drawingFeatureClassCode: null, draft: { featureClassCode, geometry } })
  },
  clearDraft: () => set({ draft: null }),
}))
