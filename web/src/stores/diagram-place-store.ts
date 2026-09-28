import { create } from "zustand"

import type { FeatureRef } from "@/stores/selection-store"

/** How a placed feature draws on the canvas, decided from the picked
 * feature's class geometry_type at arm time: a point is dropped with one
 * click; a line is drawn as a polyline (its `path`, Slice 5). */
export type DiagramPlaceGeometry = "point" | "line"

type DiagramPlacePhase = "idle" | "picking" | "placing"

/**
 * Not persisted -- an in-progress "place a feature from the map onto a
 * diagram" gesture (Phase 12) has no meaning across a reload, same as
 * connect-store.ts / draw-store.ts.
 *
 * Flow: a diagram canvas's "Add from map" button calls `startPicking`
 * with its own diagram id -> phase "picking", and the map becomes the
 * picker (voltedge-map.tsx's click handler routes a click to `pick`
 * instead of normal selection, mirroring connect-mode; peer-picker.tsx
 * does the same for a stacked-feature click). The canvas that owns
 * `diagramId` then validates the picked feature against its own catalog +
 * placements -- only it knows both -- via an effect: a
 * non-`is_diagram_placeable` class, or one already on the diagram, is
 * rejected with a toast and `cancel`; a legal pick is `arm`ed -> phase
 * "placing", and the canvas drops it where the next canvas click lands.
 * `diagramId` scopes the whole gesture so that with several diagrams open
 * at once only the armed one reacts.
 */
interface DiagramPlaceState {
  phase: DiagramPlacePhase
  diagramId: string | null
  // The raw feature just clicked on the map, awaiting the owning canvas's
  // validation. Consumed (cleared) by `arm` or `cancel`.
  picked: FeatureRef | null
  // The validated feature to place, and how its class draws.
  feature: FeatureRef | null
  geometry: DiagramPlaceGeometry | null
  // When set, the same map-pick gesture is a REPLACE, not an add: instead of
  // arming the picked feature for a fresh placement, the owning canvas swaps
  // this already-placed stub for the picked map feature in place (keeping the
  // stub's spot and moving its wiring). Distinguishes replace-picking from
  // add-picking while both ride the "picking" phase. Cleared by cancel/add.
  replaceTarget: FeatureRef | null
  startPicking: (diagramId: string) => void
  startReplacing: (diagramId: string, target: FeatureRef) => void
  pick: (feature: FeatureRef) => void
  arm: (feature: FeatureRef, geometry: DiagramPlaceGeometry) => void
  cancel: () => void
}

export const useDiagramPlaceStore = create<DiagramPlaceState>((set) => ({
  phase: "idle",
  diagramId: null,
  picked: null,
  feature: null,
  geometry: null,
  replaceTarget: null,
  startPicking: (diagramId) =>
    set({ phase: "picking", diagramId, picked: null, feature: null, geometry: null, replaceTarget: null }),
  // Replace-from-map: arm the map picker exactly like startPicking, but tag the
  // gesture with the stub placement it will overwrite. The owning canvas reads
  // replaceTarget to route the pick down the replace path instead of add.
  startReplacing: (diagramId, target) =>
    set({ phase: "picking", diagramId, picked: null, feature: null, geometry: null, replaceTarget: target }),
  // Only a click that lands while actually picking counts -- a stray map
  // click once the feature is already armed ("placing") must not replace
  // it out from under the canvas's own place-click.
  pick: (feature) => set((state) => (state.phase === "picking" ? { picked: feature } : state)),
  arm: (feature, geometry) => set({ phase: "placing", feature, geometry, picked: null }),
  cancel: () => set({ phase: "idle", diagramId: null, picked: null, feature: null, geometry: null, replaceTarget: null }),
}))
