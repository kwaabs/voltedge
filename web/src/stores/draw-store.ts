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
  /** Set only for an add-a-Ring-Main-Unit draft (Phase RMU Slice 3). Unlike a
   * panel this draft DOES carry `geometry` -- an RMU is a map-placed unit, so
   * it's drawn like any point feature -- but its Save POSTs to /rmus (the
   * composite fan-out that also creates the internal bus + ways + wiring)
   * instead of the plain create route. `config` (the RMU_CONFIG value chosen in
   * the menu) selects the way template; it's also seeded onto
   * `seed.attributes.rmu_config` so the shared create form opens with it
   * pre-selected and still collects the RMU's remaining fields (name, subtype)
   * with no bespoke form. */
  rmu?: { config: string }
  /** Set only for an add-a-Packaged-Substation draft (Phase 56). Like an RMU this
   * draft DOES carry `geometry` -- a packaged substation is a map-placed unit, so
   * it's drawn like any point feature -- but its Save POSTs to /packaged-substations
   * (the composite fan-out that also creates the internal components + wiring)
   * instead of the plain create route. `config` (the package config chosen in the
   * menu) selects the component template; it's also seeded onto
   * `seed.attributes.package_config` so the shared create form opens with it
   * pre-selected. `subtype` (the tenure -- Permanent vs Mobile -- chosen in the same
   * menu item) is seeded onto `seed.subtype` so the form's subtype picker opens on
   * it and it always rides top-level on the POST, and still collects the unit's
   * remaining fields (name) with no bespoke form. */
  packagedSubstation?: { config: string; subtype: number }
  /** Set only for an add-a-Kit-Assembly draft. Like a packaged substation this
   * draft DOES carry `geometry` -- the anchor is a map-placed point (an overhead
   * POLE or a pad-mount DIST_XFMR) -- but its Save POSTs to /assemblies (the
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
  /** Set only while an "Add RMU" draw is in flight (startRMUDrawing): the
   * chosen RMU_CONFIG value, stashed until the map click lands so finishDrawing
   * can mark the resulting draft as an RMU fan-out. null for every ordinary
   * draw. Cleared alongside the other drawing state on finish/cancel. */
  drawingRmuConfig: string | null
  /** Set only while an "Add packaged substation" draw is in flight
   * (startPackagedSubstationDrawing): the chosen package config + tenure subtype,
   * stashed until the map click lands so finishDrawing can mark the resulting draft
   * as a /packaged-substations fan-out. null for every ordinary draw. Cleared
   * alongside the other drawing state on finish/cancel. */
  drawingPackagedConfig: string | null
  drawingPackagedSubtype: number | null
  /** Set only while an "Add kit assembly" draw is in flight (startAssemblyDrawing):
   * the chosen anchor class + kit config + (optional) anchor subtype, stashed until
   * the map click lands so finishDrawing can mark the resulting draft as an
   * /assemblies fan-out. null for every ordinary draw. Cleared alongside the other
   * drawing state on finish/cancel. */
  drawingAssemblyAnchorClass: string | null
  drawingAssemblyConfig: string | null
  drawingAssemblySubtype: number | null
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
  draft: DraftFeature | null
  /** Bumped every time a *new* draft opens (finishDrawing / startCopyDraft /
   * startDiagramDraft) but NOT when the draft's geometry is merely dragged
   * (updateDraftGeometry). feature-create-form.tsx keys its "reset the form
   * for a new draft" effect on this, so moving a copy on the map doesn't wipe
   * what's been typed, while a genuinely new draft does start the form fresh. */
  draftToken: number
  startDrawing: (featureClassCode: string, geometryType: DrawGeometryType) => void
  /** Begin placing an RMU: draws like a plain point, but stashes `config` so the
   * finished draft is marked as an /rmus fan-out (Phase RMU Slice 3). */
  startRMUDrawing: (rmuFeatureClass: string, config: string) => void
  /** Begin placing a packaged substation: draws like a plain point, but stashes
   * `config` + `subtype` so the finished draft is marked as a
   * /packaged-substations fan-out (Phase 56). */
  startPackagedSubstationDrawing: (
    packagedFeatureClass: string,
    config: string,
    subtype: number,
  ) => void
  /** Begin placing a kit assembly: draws like a plain point (the anchor), but
   * stashes the anchor class + kit config + (optional) anchor subtype so the
   * finished draft is marked as an /assemblies fan-out. */
  startAssemblyDrawing: (
    anchorFeatureClass: string,
    config: string,
    anchorSubtype?: number,
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
  drawingRmuConfig: null,
  drawingPackagedConfig: null,
  drawingPackagedSubtype: null,
  drawingAssemblyAnchorClass: null,
  drawingAssemblyConfig: null,
  drawingAssemblySubtype: null,
  drawingDuct: false,
  drawingCouplerBusClasses: null,
  drawingTieFeederClasses: null,
  draft: null,
  draftToken: 0,
  startDrawing: (featureClassCode, geometryType) =>
    set({
      drawingFeatureClassCode: featureClassCode,
      drawingGeometryType: geometryType,
      drawingRmuConfig: null,
      drawingPackagedConfig: null,
      drawingPackagedSubtype: null,
      drawingAssemblyAnchorClass: null,
      drawingAssemblyConfig: null,
      drawingAssemblySubtype: null,
      drawingDuct: false,
      drawingCouplerBusClasses: null,
      drawingTieFeederClasses: null,
      draft: null,
    }),
  // An RMU draws as a plain point; the only difference from startDrawing is the
  // stashed config, which finishDrawing reads to mark the draft as a fan-out.
  startRMUDrawing: (rmuFeatureClass, config) =>
    set({
      drawingFeatureClassCode: rmuFeatureClass,
      drawingGeometryType: "point",
      drawingRmuConfig: config,
      drawingPackagedConfig: null,
      drawingPackagedSubtype: null,
      drawingAssemblyAnchorClass: null,
      drawingAssemblyConfig: null,
      drawingAssemblySubtype: null,
      drawingDuct: false,
      drawingCouplerBusClasses: null,
      drawingTieFeederClasses: null,
      draft: null,
    }),
  // A packaged substation draws as a plain point; the only difference from
  // startDrawing is the stashed config + tenure subtype, which finishDrawing reads
  // to mark the draft as a /packaged-substations fan-out.
  startPackagedSubstationDrawing: (packagedFeatureClass, config, subtype) =>
    set({
      drawingFeatureClassCode: packagedFeatureClass,
      drawingGeometryType: "point",
      drawingRmuConfig: null,
      drawingPackagedConfig: config,
      drawingPackagedSubtype: subtype,
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
  startAssemblyDrawing: (anchorFeatureClass, config, anchorSubtype) =>
    set({
      drawingFeatureClassCode: anchorFeatureClass,
      drawingGeometryType: "point",
      drawingRmuConfig: null,
      drawingPackagedConfig: null,
      drawingPackagedSubtype: null,
      drawingAssemblyAnchorClass: anchorFeatureClass,
      drawingAssemblyConfig: config,
      drawingAssemblySubtype: anchorSubtype ?? null,
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
      drawingRmuConfig: null,
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
      drawingRmuConfig: null,
      drawingPackagedConfig: null,
      drawingPackagedSubtype: null,
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
      drawingRmuConfig: null,
      drawingPackagedConfig: null,
      drawingPackagedSubtype: null,
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
      drawingRmuConfig: null,
      drawingPackagedConfig: null,
      drawingPackagedSubtype: null,
      drawingAssemblyAnchorClass: null,
      drawingAssemblyConfig: null,
      drawingAssemblySubtype: null,
      drawingDuct: false,
      drawingCouplerBusClasses: null,
      drawingTieFeederClasses: null,
    }),
  finishDrawing: (geometry) => {
    const featureClassCode = get().drawingFeatureClassCode
    if (!featureClassCode) return
    const rmuConfig = get().drawingRmuConfig
    const packagedConfig = get().drawingPackagedConfig
    const packagedSubtype = get().drawingPackagedSubtype
    const assemblyConfig = get().drawingAssemblyConfig
    const assemblySubtype = get().drawingAssemblySubtype
    const isDuct = get().drawingDuct
    const couplerBusClasses = get().drawingCouplerBusClasses
    const tieFeederClasses = get().drawingTieFeederClasses
    set((s) => ({
      drawingFeatureClassCode: null,
      drawingGeometryType: null,
      drawingRmuConfig: null,
      drawingPackagedConfig: null,
      drawingPackagedSubtype: null,
      drawingAssemblyAnchorClass: null,
      drawingAssemblyConfig: null,
      drawingAssemblySubtype: null,
      drawingDuct: false,
      drawingCouplerBusClasses: null,
      drawingTieFeederClasses: null,
      // A stashed config means this was an "Add RMU" draw: carry the geometry
      // (an RMU is a real placed unit) plus the rmu marker + seeded rmu_config
      // so Save routes to /rmus and the form opens with the config chosen. A duct
      // flag means an "Add cable duct" draw: carry the line geometry plus the duct
      // marker so Save routes to /ducts (no config to seed -- way_count is a plain
      // attribute the form collects). Stashed bus classes mean an "Add bus coupler"
      // draw: carry the breaker point geometry plus the coupler marker (the bus
      // classes the form picks from) + seeded switch_function so Save routes to
      // /couplers with the breaker self-labelled as the coupler. Stashed feeder
      // classes mean a "Create tie" draw: carry the switch point geometry plus
      // the tie marker (the feeder classes the form picks from) + seeded
      // normal_state/device_state so Save routes to /ties with the switch born
      // as an open standby (NOP).
      // A stashed package config means this was an "Add packaged substation" draw:
      // carry the geometry (a packaged substation is a real placed unit) plus the
      // packagedSubstation marker + seeded package_config so Save routes to
      // /packaged-substations and the form opens with the config chosen; the tenure
      // subtype rides along on seed.subtype so the form's subtype picker opens on it
      // and it's always sent top-level on the POST.
      // A stashed assembly config means this was an "Add kit assembly" draw: carry
      // the geometry (the anchor pole/transformer is a real placed point) plus the
      // assembly marker (anchor class + kit config) so Save routes to /assemblies;
      // config isn't an anchor attribute (unlike rmu_config/package_config), so it
      // rides only on the marker, not in seed.attributes. When the option pins an
      // anchor subtype it rides on seed.subtype so the picker opens on it and it's
      // sent top-level; options with no fixed subtype leave the picker free.
      draft: rmuConfig
        ? {
            featureClassCode,
            geometry,
            seed: { attributes: { rmu_config: rmuConfig } },
            rmu: { config: rmuConfig },
          }
        : packagedConfig != null
          ? {
              featureClassCode,
              geometry,
              seed: {
                subtype: packagedSubtype ?? undefined,
                attributes: { package_config: packagedConfig },
              },
              packagedSubstation: { config: packagedConfig, subtype: packagedSubtype ?? 0 },
            }
          : assemblyConfig != null
          ? {
              featureClassCode,
              geometry,
              seed: {
                subtype: assemblySubtype ?? undefined,
                attributes: {},
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
