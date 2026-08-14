import { create } from "zustand"

export type DrawGeometryType = "point" | "line" | "polygon"

export interface DraftFeature {
  featureClassCode: string
  /** GeoJSON geometry in EPSG:4326, ready to send straight to POST
   * /features/{code}. Absent for a geometry-less (`geometry_type =
   * 'none'`) feature digitized straight onto a diagram -- see
   * `diagramPlacement` below, the only case this is ever unset. */
  geometry?: { type: string; coordinates: unknown }
  /** Set only for a draft started from a diagram's digitizing tool
   * (diagram-canvas.tsx) -- where to place the newly created feature
   * once feature-create-form.tsx saves it. Never set for the map's own
   * draw flow, which places nothing anywhere but the map itself. */
  diagramPlacement?: { diagramId: string; x: number; y: number }
}

/**
 * Not persisted -- an in-progress draw or an unsaved draft has no
 * meaning across a reload. Two distinct states, not one: `drawing` is
 * "waiting on clicks on the map to place the geometry" (the map owns
 * this -- see voltedge-map.tsx's draw effect); `draft` is "geometry
 * placed (or, for a diagram-originated draft, deliberately never
 * placed at all), waiting on the create form" (feature-panel.tsx owns
 * this, rendering feature-create-form.tsx either way). They're
 * mutually exclusive by construction -- finishDrawing/startDiagramDraft
 * both move straight into `draft`. `drawingGeometryType` rides along
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
  finishDrawing: (geometry: NonNullable<DraftFeature["geometry"]>) => void
  startDiagramDraft: (featureClassCode: string, diagramPlacement: NonNullable<DraftFeature["diagramPlacement"]>) => void
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
  startDiagramDraft: (featureClassCode, diagramPlacement) =>
    set({ drawingFeatureClassCode: null, drawingGeometryType: null, draft: { featureClassCode, diagramPlacement } }),
  clearDraft: () => set({ draft: null }),
}))
