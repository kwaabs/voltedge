import { create } from "zustand"

export type DrawGeometryType = "point" | "line" | "polygon"

/** Pre-filled form values carried by a draft that was seeded from an existing
 * feature (Phase 20 -- feature-panel.tsx's Duplicate, and later a line split's
 * new part) rather than drawn blank. feature-create-form.tsx initializes its
 * Name / Subtype / attribute inputs from this instead of starting empty. */
export interface DraftSeed {
  name?: string
  subtype?: number
  attributes: Record<string, unknown>
}

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
  /** Present only when this draft was seeded from an existing feature
   * (Duplicate, or a line Split) -- the attribute/name/subtype values to
   * pre-fill. */
  seed?: DraftSeed
  /** Set only for a line-split draft (Phase 20). Unlike every other draft
   * this one carries NO client geometry: the far half's geometry is computed
   * by PostGIS from `splitPoint` against the original (`sourceFeatureId`), so
   * feature-create-form.tsx's Save POSTs to /features/{code}/{id}/split
   * instead of the plain create route, and the map's draggable-draft effect
   * (which gates on `geometry`) correctly ignores it. */
  split?: {
    sourceFeatureId: string
    /** EPSG:4326 lng/lat, already projected onto the line via getClosestPoint. */
    splitPoint: { lng: number; lat: number }
  }
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
  /** Bumped every time a *new* draft opens (finishDrawing / startCopyDraft /
   * startDiagramDraft) but NOT when the draft's geometry is merely dragged
   * (updateDraftGeometry). feature-create-form.tsx keys its "reset the form
   * for a new draft" effect on this, so moving a copy on the map doesn't wipe
   * what's been typed, while a genuinely new draft does start the form fresh. */
  draftToken: number
  startDrawing: (featureClassCode: string, geometryType: DrawGeometryType) => void
  cancelDrawing: () => void
  finishDrawing: (geometry: NonNullable<DraftFeature["geometry"]>) => void
  startCopyDraft: (
    featureClassCode: string,
    geometry: NonNullable<DraftFeature["geometry"]>,
    seed: DraftSeed,
  ) => void
  startSplitDraft: (
    featureClassCode: string,
    sourceFeatureId: string,
    splitPoint: NonNullable<DraftFeature["split"]>["splitPoint"],
    seed: DraftSeed,
  ) => void
  updateDraftGeometry: (geometry: NonNullable<DraftFeature["geometry"]>) => void
  startDiagramDraft: (featureClassCode: string, diagramPlacement: NonNullable<DraftFeature["diagramPlacement"]>) => void
  clearDraft: () => void
}

export const useDrawStore = create<DrawState>((set, get) => ({
  drawingFeatureClassCode: null,
  drawingGeometryType: null,
  draft: null,
  draftToken: 0,
  startDrawing: (featureClassCode, geometryType) =>
    set({ drawingFeatureClassCode: featureClassCode, drawingGeometryType: geometryType, draft: null }),
  cancelDrawing: () => set({ drawingFeatureClassCode: null, drawingGeometryType: null }),
  finishDrawing: (geometry) => {
    const featureClassCode = get().drawingFeatureClassCode
    if (!featureClassCode) return
    set((s) => ({
      drawingFeatureClassCode: null,
      drawingGeometryType: null,
      draft: { featureClassCode, geometry },
      draftToken: s.draftToken + 1,
    }))
  },
  startCopyDraft: (featureClassCode, geometry, seed) =>
    set((s) => ({
      drawingFeatureClassCode: null,
      drawingGeometryType: null,
      draft: { featureClassCode, geometry, seed },
      draftToken: s.draftToken + 1,
    })),
  // A split draft carries no geometry (PostGIS computes the far half from
  // splitPoint on Save) -- just the source feature to cut and where. Same
  // seed-and-bump shape as startCopyDraft otherwise.
  startSplitDraft: (featureClassCode, sourceFeatureId, splitPoint, seed) =>
    set((s) => ({
      drawingFeatureClassCode: null,
      drawingGeometryType: null,
      draft: { featureClassCode, seed, split: { sourceFeatureId, splitPoint } },
      draftToken: s.draftToken + 1,
    })),
  // Geometry only -- keeps everything else (the seed, the class) and the
  // draftToken intact so the create form's typed values survive a drag.
  updateDraftGeometry: (geometry) =>
    set((s) => (s.draft ? { draft: { ...s.draft, geometry } } : {})),
  startDiagramDraft: (featureClassCode, diagramPlacement) =>
    set((s) => ({
      drawingFeatureClassCode: null,
      drawingGeometryType: null,
      draft: { featureClassCode, diagramPlacement },
      draftToken: s.draftToken + 1,
    })),
  clearDraft: () => set({ draft: null }),
}))
