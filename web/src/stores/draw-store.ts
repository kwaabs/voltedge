import { create } from "zustand"

export type DrawGeometryType = "point" | "line" | "polygon"

export interface DraftFeature {
  featureClassCode: string
  /** GeoJSON geometry in EPSG:4326, ready to send straight to POST /features/{code}. */
  geometry: { type: string; coordinates: unknown }
}

/**
 * Not persisted -- an in-progress draw or an unsaved draft has no
 * meaning across a reload. Two distinct states, not one: `drawing` is
 * "waiting on clicks on the map to place the geometry" (the map owns
 * this -- see voltedge-map.tsx's draw effect); `draft` is "geometry
 * placed, waiting on the create form" (feature-panel.tsx owns this).
 * They're mutually exclusive by construction -- finishDrawing moves
 * straight from one to the other. `drawingGeometryType` rides along
 * with `drawingFeatureClassCode` (set together, cleared together) since
 * voltedge-map.tsx's Draw interaction needs to know point vs. line vs.
 * polygon to pick the right OL draw type -- new-feature-menu.tsx
 * already knows a feature class's geometry_type at the moment it
 * starts a draw, so it's passed in rather than re-derived.
 */
interface DrawState {
  drawingFeatureClassCode: string | null
  drawingGeometryType: DrawGeometryType | null
  draft: DraftFeature | null
  startDrawing: (featureClassCode: string, geometryType: DrawGeometryType) => void
  cancelDrawing: () => void
  finishDrawing: (geometry: DraftFeature["geometry"]) => void
  clearDraft: () => void
}

export const useDrawStore = create<DrawState>((set, get) => ({
  drawingFeatureClassCode: null,
  drawingGeometryType: null,
  draft: null,
  startDrawing: (featureClassCode, geometryType) =>
    set({ drawingFeatureClassCode: featureClassCode, drawingGeometryType: geometryType, draft: null }),
  cancelDrawing: () => set({ drawingFeatureClassCode: null, drawingGeometryType: null }),
  finishDrawing: (geometry) => {
    const featureClassCode = get().drawingFeatureClassCode
    if (!featureClassCode) return
    set({
      drawingFeatureClassCode: null,
      drawingGeometryType: null,
      draft: { featureClassCode, geometry },
    })
  },
  clearDraft: () => set({ draft: null }),
}))
