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
   * draw flow, which places nothing anywhere but the map itself.
   *
   * `rotation` (default 0) is the placed feature's angle -- carried from a
   * promoted symbol's own rotation so the placement matches the glyph it
   * replaces. `promoteFromElementId`, when present, marks this draft as a
   * stencil->feature promotion (Phase 25): after the feature is created and
   * placed, that symbol element is deleted, so the glyph is swapped for the
   * real feature in one gesture. Absent for a plain digitize (nothing to
   * delete).
   *
   * `path` (migration 0053) is the drawn polyline in diagram space, set only
   * when the class draws as a line on the diagram (diagram_geometry_type =
   * 'line', e.g. a busbar) -- so a new line-shaped feature is dug in
   * vertex-by-vertex like a place-from-map line, not dropped at one point. */
  diagramPlacement?: {
    diagramId: string
    x: number
    y: number
    rotation?: number
    promoteFromElementId?: string
    path?: [number, number][]
  }
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
  /** Set only for an add-a-switchgear-panel draft (Phase 42 Slice 3). Like a
   * split it carries NO client geometry (the panel and its devices are all
   * geometry-less container/device classes): feature-create-form.tsx's Save
   * POSTs to /panels (the composite fan-out) instead of the plain create route,
   * then places the returned panel and each device onto the board schematic at
   * `base` + the device's template offset. `featureClassCode` is the panel
   * class; `seed.attributes.panel_role` pre-selects the role that picks the
   * device template, so the shared create form still collects the panel's own
   * remaining required fields (voltage class, subtype, name) with no bespoke
   * form. */
  panel?: {
    boardFeatureClass: string
    boardFeatureId: string
    diagramId: string
    /** Anchor for the panel's column on the board mimic, in the diagram's own
     * local x/y space (nextPlacementPosition). Devices land at base + their
     * template offset; draggable afterward. */
    base: { x: number; y: number }
  }
  /** Set only for an add-a-Kit-Assembly draft. The anchor is a map-placed point
   * (a POLE, a DIST_XFMR, an RMU, a PACKAGED_SUBSTATION enclosure, ...) -- so this
   * draft DOES carry `geometry` -- but its Save POSTs to /assemblies (the
   * composite fan-out that also creates the kit's components + wiring) instead of
   * the plain create route. `anchorFeatureClass` is the anchor's own class (the
   * draft's featureClassCode, kept here too so the Save reads straight from the
   * marker); `config` (the kit config chosen in the menu) selects the component
   * template. `anchorSubtype`, when the option pins one, rides onto `seed.subtype`
   * so the form's subtype picker opens on it and it always rides top-level on the
   * POST; options with no fixed subtype leave the picker free. The anchor's own
   * required fields (a POLE's material/use/height/condition, a DIST_XFMR's primary
   * voltage/condition) are collected by the shared create form with no bespoke form. */
  assembly?: { anchorFeatureClass: string; config: string; anchorSubtype?: number }
  /** Set only for an add-a-cable-duct draft (Phase 44 Slice 4). Like an RMU this
   * draft DOES carry `geometry` -- a duct is a map-placed line -- but its Save POSTs
   * to /ducts (the composite fan-out that also creates the DUCT_WAY children the
   * duct's way_count calls for, all containment-wired) instead of the plain create
   * route. There's no config to seed: way_count is an ordinary attribute the shared
   * create form already collects, so no bespoke form is needed. */
  duct?: boolean
  /** Set only for an add-a-bus-coupler draft (Phase Bus-coupler Slice 2). Like an
   * RMU this draft DOES carry `geometry` -- the coupler breaker is a map-placed
   * point -- but its Save POSTs to /couplers (the composite that also wires the
   * breaker between two named buses via busbar rules C + D) instead of the plain
   * create route. `busFeatureClasses` are the classes the two buses may be picked
   * from (from the coupler option); the two actual bus picks live in the create
   * form's own state (feature-create-form.tsx), not here, since they're chosen in
   * the form rather than on the map. switch_function=BUS_COUPLER is seeded so the
   * form opens self-labelling. */
  coupler?: { busFeatureClasses: string[] }
  /** Set only for an add-a-feeder-tie draft (Phase 50 Slice 2). Like a coupler
   * this draft DOES carry `geometry` -- the tie switch is a map-placed point --
   * but its Save POSTs to /ties (the composite that also wires the switch between
   * two named conductors via switch rules 1 + 3) instead of the plain create
   * route. `feederFeatureClasses` are the classes the two feeders may be picked
   * from (from the tie option); the two actual feeder picks live in the create
   * form's own state (feature-create-form.tsx). normal_state=NORMALLY_OPEN and
   * device_state=OPEN are seeded so the form opens with the tie born as an open
   * standby (NOP) path. */
  tie?: { feederFeatureClasses: string[] }
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
  /** Set only while an "Add kit assembly" draw is in flight (startAssemblyDrawing):
   * the chosen anchor class + kit config + (optional) anchor subtype, stashed until
   * the map click lands so finishDrawing can mark the resulting draft as an
   * /assemblies fan-out. null for every ordinary draw. Cleared alongside the other
   * drawing state on finish/cancel. */
  drawingAssemblyAnchorClass: string | null
  drawingAssemblyConfig: string | null
  drawingAssemblySubtype: number | null
  /** The kit's default anchor attributes, stashed until the click lands so
   * finishDrawing can seed them onto the draft (so a required config field arrives
   * pre-selected). null for every non-assembly draw. */
  drawingAssemblyAttributes: Record<string, unknown> | null
  /** Set only while an "Add cable duct" draw is in flight (startDuctDrawing):
   * marks the drawn line as a duct fan-out so finishDrawing tags the resulting
   * draft with `duct: true` and its Save routes to /ducts. false for every
   * ordinary draw. Cleared alongside the other drawing state on finish/cancel. */
  drawingDuct: boolean
  /** Set only while an "Add bus coupler" draw is in flight (startCouplerDrawing):
   * the eligible bus classes stashed until the map click lands, so finishDrawing
   * can mark the resulting draft as a /couplers fan-out carrying them. null for
   * every ordinary draw. Cleared alongside the other drawing state on
   * finish/cancel. */
  drawingCouplerBusClasses: string[] | null
  /** Set only while an "Create tie" draw is in flight (startTieDrawing): the
   * eligible feeder classes stashed until the map click lands, so finishDrawing
   * can mark the resulting draft as a /ties fan-out carrying them. null for
   * every ordinary draw. Cleared alongside the other drawing state on
   * finish/cancel. */
  drawingTieFeederClasses: string[] | null
  /** Set while a "Place on map" (digitize-onto-existing) draw is in flight
   * (startDigitize, from diagram-canvas.tsx's selected diagram-only feature):
   * the EXISTING feature whose geometry is being drawn. Unlike every other draw
   * this doesn't create a feature -- finishDrawing routes the drawn geometry to
   * `geometryUpdate` (a PATCH of this feature's geom) instead of a create `draft`.
   * Cleared on finish/cancel. */
  digitizeTarget: { featureClassCode: string; featureId: string } | null
  /** A completed "Place on map" draw: the existing feature + the geometry drawn
   * for it, waiting for the committer (digitize-committer.tsx) to PATCH it onto
   * the feature and clear this. A fresh object each finish, so the committer's
   * effect fires once per draw. */
  geometryUpdate: { featureClassCode: string; featureId: string; geometry: NonNullable<DraftFeature["geometry"]> } | null
  /** Which gesture the placed-draft toolbar (draft-edit-toolbar.tsx) has armed on
   * a draft's geometry: "move" drags the whole shape (Translate), "reshape" edits
   * its vertices (Modify, line/polygon only). voltedge-map.tsx's draft-interaction
   * effect reads this. Defaults to "move"; the toolbar resets it per new draft. */
  draftGeometryTool: "move" | "reshape"
  draft: DraftFeature | null
  /** Bumped every time a *new* draft opens (finishDrawing / startCopyDraft /
   * startDiagramDraft) but NOT when the draft's geometry is merely dragged
   * (updateDraftGeometry). feature-create-form.tsx keys its "reset the form
   * for a new draft" effect on this, so moving a copy on the map doesn't wipe
   * what's been typed, while a genuinely new draft does start the form fresh. */
  draftToken: number
  startDrawing: (featureClassCode: string, geometryType: DrawGeometryType) => void
  /** Begin placing a kit assembly: draws like a plain point (the anchor), but
   * stashes the anchor class + kit config + (optional) anchor subtype + the kit's
   * default anchor attributes so the finished draft is marked as an /assemblies
   * fan-out with the anchor's config field pre-seeded. */
  startAssemblyDrawing: (
    anchorFeatureClass: string,
    config: string,
    anchorSubtype?: number,
    anchorAttributes?: Record<string, unknown> | null,
  ) => void
  /** Begin placing a cable duct: draws like a plain line, but flags the draft so
   * the finished draft is marked as a /ducts fan-out (Phase 44 Slice 4). */
  startDuctDrawing: (ductFeatureClass: string) => void
  /** Begin placing a bus coupler: draws like a plain point (the breaker), but
   * stashes the eligible bus classes so the finished draft is marked as a
   * /couplers fan-out (Phase Bus-coupler Slice 2). */
  startCouplerDrawing: (breakerFeatureClass: string, busFeatureClasses: string[]) => void
  /** Begin placing a feeder tie: draws like a plain point (the switch), but
   * stashes the eligible feeder classes so the finished draft is marked as a
   * /ties fan-out (Phase 50 Slice 2). */
  startTieDrawing: (switchFeatureClass: string, feederFeatureClasses: string[]) => void
  /** Begin a "Place on map" draw for an EXISTING diagram-only feature: draws
   * point/line/polygon per its class, but finishDrawing routes the result to
   * `geometryUpdate` (a geom PATCH of this feature) rather than creating a new
   * one -- the mirror of the diagram's "Add from map". */
  startDigitize: (featureClassCode: string, featureId: string, geometryType: DrawGeometryType) => void
  /** Clear the pending geometryUpdate after the committer has PATCHed it. */
  clearGeometryUpdate: () => void
  /** Arm a gesture on the current placed draft's geometry (draft-edit-toolbar). */
  setDraftGeometryTool: (tool: "move" | "reshape") => void
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
  /** Duplicate a feature from within a diagram (diagram-canvas.tsx's per-placement
   * Duplicate control). Combines startCopyDraft's `seed` (pre-fills the create form
   * from the source feature) with startDiagramDraft's `diagramPlacement` (places the
   * new feature on the diagram once saved), and carries the offset map `geometry`
   * only when the source class actually has geometry -- a geometry-less schematic
   * class duplicates as a pure diagram feature. The shared create form already
   * handles all three together, so no bespoke save path is needed. */
  startDiagramCopyDraft: (
    featureClassCode: string,
    seed: DraftSeed,
    diagramPlacement: NonNullable<DraftFeature["diagramPlacement"]>,
    geometry?: DraftFeature["geometry"],
  ) => void
  startPanelDraft: (
    panelFeatureClass: string,
    panel: NonNullable<DraftFeature["panel"]>,
    role: string,
  ) => void
  clearDraft: () => void
}

export const useDrawStore = create<DrawState>((set, get) => ({
  drawingFeatureClassCode: null,
  drawingGeometryType: null,
  drawingAssemblyAnchorClass: null,
  drawingAssemblyConfig: null,
  drawingAssemblySubtype: null,
  drawingAssemblyAttributes: null,
  drawingDuct: false,
  drawingCouplerBusClasses: null,
  drawingTieFeederClasses: null,
  digitizeTarget: null,
  geometryUpdate: null,
  draftGeometryTool: "move",
  draft: null,
  draftToken: 0,
  setDraftGeometryTool: (tool) => set({ draftGeometryTool: tool }),
  // "Place on map": arm the same point/line/polygon draw as startDrawing, but
  // stash the existing feature as digitizeTarget so finishDrawing PATCHes its geom
  // instead of creating a new feature. Clears any draft + prior geometryUpdate.
  startDigitize: (featureClassCode, featureId, geometryType) =>
    set({
      drawingFeatureClassCode: featureClassCode,
      drawingGeometryType: geometryType,
      drawingAssemblyAnchorClass: null,
      drawingAssemblyConfig: null,
      drawingAssemblySubtype: null,
      drawingDuct: false,
      drawingCouplerBusClasses: null,
      drawingTieFeederClasses: null,
      digitizeTarget: { featureClassCode, featureId },
      geometryUpdate: null,
      draft: null,
    }),
  clearGeometryUpdate: () => set({ geometryUpdate: null }),
  startDrawing: (featureClassCode, geometryType) =>
    set({
      drawingFeatureClassCode: featureClassCode,
      drawingGeometryType: geometryType,
      drawingAssemblyAnchorClass: null,
      drawingAssemblyConfig: null,
      drawingAssemblySubtype: null,
      drawingDuct: false,
      drawingCouplerBusClasses: null,
      drawingTieFeederClasses: null,
      draft: null,
    }),
  // A kit assembly draws as a plain point (the anchor pole or pad-mount
  // transformer); the only difference from startDrawing is the stashed anchor
  // class + kit config + optional anchor subtype, which finishDrawing reads to
  // mark the draft as an /assemblies fan-out.
  startAssemblyDrawing: (anchorFeatureClass, config, anchorSubtype, anchorAttributes) =>
    set({
      drawingFeatureClassCode: anchorFeatureClass,
      drawingGeometryType: "point",
      drawingAssemblyAnchorClass: anchorFeatureClass,
      drawingAssemblyConfig: config,
      drawingAssemblySubtype: anchorSubtype ?? null,
      drawingAssemblyAttributes: anchorAttributes ?? null,
      drawingDuct: false,
      drawingCouplerBusClasses: null,
      drawingTieFeederClasses: null,
      draft: null,
    }),
  // A cable duct draws as a plain line; the only difference from startDrawing is
  // the drawingDuct flag, which finishDrawing reads to mark the draft as a
  // /ducts fan-out.
  startDuctDrawing: (ductFeatureClass) =>
    set({
      drawingFeatureClassCode: ductFeatureClass,
      drawingGeometryType: "line",
      drawingAssemblyAnchorClass: null,
      drawingAssemblyConfig: null,
      drawingAssemblySubtype: null,
      drawingDuct: true,
      drawingCouplerBusClasses: null,
      drawingTieFeederClasses: null,
      draft: null,
    }),
  // A coupler draws as a plain point (the breaker); the only difference from
  // startDrawing is the stashed bus classes, which finishDrawing reads to mark
  // the draft as a /couplers fan-out carrying the buses the form may pick from.
  startCouplerDrawing: (breakerFeatureClass, busFeatureClasses) =>
    set({
      drawingFeatureClassCode: breakerFeatureClass,
      drawingGeometryType: "point",
      drawingAssemblyAnchorClass: null,
      drawingAssemblyConfig: null,
      drawingAssemblySubtype: null,
      drawingDuct: false,
      drawingCouplerBusClasses: busFeatureClasses,
      drawingTieFeederClasses: null,
      draft: null,
    }),
  // A tie draws as a plain point (the switch); the only difference from
  // startDrawing is the stashed feeder classes, which finishDrawing reads to
  // mark the draft as a /ties fan-out carrying the feeders the form may pick
  // from.
  startTieDrawing: (switchFeatureClass, feederFeatureClasses) =>
    set({
      drawingFeatureClassCode: switchFeatureClass,
      drawingGeometryType: "point",
      drawingAssemblyAnchorClass: null,
      drawingAssemblyConfig: null,
      drawingAssemblySubtype: null,
      drawingDuct: false,
      drawingCouplerBusClasses: null,
      drawingTieFeederClasses: feederFeatureClasses,
      draft: null,
    }),
  cancelDrawing: () =>
    set({
      drawingFeatureClassCode: null,
      drawingGeometryType: null,
      drawingAssemblyAnchorClass: null,
      drawingAssemblyConfig: null,
      drawingAssemblySubtype: null,
      drawingDuct: false,
      drawingCouplerBusClasses: null,
      drawingTieFeederClasses: null,
      digitizeTarget: null,
    }),
  finishDrawing: (geometry) => {
    const featureClassCode = get().drawingFeatureClassCode
    if (!featureClassCode) return
    // "Place on map": the drawn geometry belongs to an EXISTING feature. Route it
    // to geometryUpdate (a geom PATCH) and stop -- no create draft, no form.
    const digitize = get().digitizeTarget
    if (digitize) {
      set({
        drawingFeatureClassCode: null,
        drawingGeometryType: null,
        digitizeTarget: null,
        geometryUpdate: { featureClassCode: digitize.featureClassCode, featureId: digitize.featureId, geometry },
      })
      return
    }
    const assemblyConfig = get().drawingAssemblyConfig
    const assemblySubtype = get().drawingAssemblySubtype
    const assemblyAttributes = get().drawingAssemblyAttributes
    const isDuct = get().drawingDuct
    const couplerBusClasses = get().drawingCouplerBusClasses
    const tieFeederClasses = get().drawingTieFeederClasses
    set((s) => ({
      drawingFeatureClassCode: null,
      drawingGeometryType: null,
      drawingAssemblyAnchorClass: null,
      drawingAssemblyConfig: null,
      drawingAssemblySubtype: null,
      drawingAssemblyAttributes: null,
      drawingDuct: false,
      drawingCouplerBusClasses: null,
      drawingTieFeederClasses: null,
      // A stashed assembly config means this was an "Add kit assembly" draw (any
      // anchor -- pole, transformer, RMU, packaged-substation enclosure, ...): carry
      // the geometry (the anchor is a real placed point) plus the assembly marker
      // (anchor class + kit config) so Save routes to /assemblies. The kit's own
      // anchor defaults (e.g. package_config / rmu_config) are applied server-side
      // from the template (migration 0194), so they aren't seeded here. When the
      // option pins an anchor subtype it rides on seed.subtype so the picker opens on
      // it and it's sent top-level. A duct flag means an "Add cable duct" draw (line
      // geometry -> /ducts); stashed bus classes an "Add bus coupler" draw; stashed
      // feeder classes a "Create tie" draw (the switch born NORMALLY_OPEN).
      draft: assemblyConfig != null
          ? {
              featureClassCode,
              geometry,
              seed: {
                subtype: assemblySubtype ?? undefined,
                attributes: assemblyAttributes ?? {},
              },
              assembly: {
                anchorFeatureClass: featureClassCode,
                config: assemblyConfig,
                anchorSubtype: assemblySubtype ?? undefined,
              },
            }
          : isDuct
          ? { featureClassCode, geometry, duct: true }
          : couplerBusClasses
            ? {
                featureClassCode,
                geometry,
                seed: { attributes: { switch_function: "BUS_COUPLER" } },
                coupler: { busFeatureClasses: couplerBusClasses },
              }
            : tieFeederClasses
              ? {
                  featureClassCode,
                  geometry,
                  seed: { attributes: { normal_state: "NORMALLY_OPEN", device_state: "OPEN" } },
                  tie: { feederFeatureClasses: tieFeederClasses },
                }
              : { featureClassCode, geometry },
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
  // seed + diagramPlacement (+ optional offset geometry), all of which the shared
  // create form already knows how to save together. Same seed-and-bump shape as
  // startCopyDraft otherwise.
  startDiagramCopyDraft: (featureClassCode, seed, diagramPlacement, geometry) =>
    set((s) => ({
      drawingFeatureClassCode: null,
      drawingGeometryType: null,
      draft: { featureClassCode, seed, diagramPlacement, ...(geometry ? { geometry } : {}) },
      draftToken: s.draftToken + 1,
    })),
  // A panel draft carries no geometry (Save POSTs to /panels, which fans out the
  // whole geometry-less assembly) -- just the board to contain it, the diagram to
  // place it on, and the anchor. The role is seeded onto panel_role so the shared
  // create form opens with it pre-selected. Same bump shape as startSplitDraft.
  startPanelDraft: (panelFeatureClass, panel, role) =>
    set((s) => ({
      drawingFeatureClassCode: null,
      drawingGeometryType: null,
      draft: {
        featureClassCode: panelFeatureClass,
        seed: { attributes: { panel_role: role } },
        panel,
      },
      draftToken: s.draftToken + 1,
    })),
  clearDraft: () => set({ draft: null }),
}))
