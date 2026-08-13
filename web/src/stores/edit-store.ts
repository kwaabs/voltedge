import { create } from "zustand"

import type { FeatureGeometry } from "@/hooks/use-feature"

export interface EditingFeature {
  featureClassCode: string
  featureId: string
}

/** Which map interaction, if any, is currently attached to the feature
 * under edit -- "move" is a Translate interaction (drag the whole
 * geometry), "vertices" is a Modify interaction (drag a vertex to move
 * it, drag a segment midpoint to insert one, alt+click to delete one).
 * `null` means neither is attached yet -- the user hasn't picked a tool
 * from geometry-edit-toolbar.tsx. Only ever one at a time; both
 * interactions listen for drag gestures on the same feature and would
 * fight each other if both were active. */
export type GeometryTool = "move" | "vertices" | null

/**
 * Not persisted -- an in-progress edit has no meaning across a reload.
 * Deliberately just a session marker (which feature, if any, is under
 * modification, which geometry tool is active, and the geometry that
 * tool has produced so far) rather than holding the edit's *attribute*
 * values -- those still live as local state in feature-edit-form.tsx,
 * the only thing that renders them. This store exists so *other*
 * components can react to "a feature is being edited" without needing
 * the attribute edit's contents: feature-panel.tsx locks the
 * Terminals/Relationships tabs while editing (switching tabs would
 * unmount the form and silently drop unsaved changes), and
 * voltedge-map.tsx blocks normal click-to-select the same way
 * draw-store.ts/connect-store.ts already block it for their own
 * in-progress workflows -- and now also reads `geometryTool` to decide
 * which interaction (if any) to attach, and writes `dirtyGeometry` once
 * that interaction produces a new geometry.
 *
 * `dirtyGeometry` lives here, not in feature-edit-form.tsx, because the
 * thing that produces it (voltedge-map.tsx's Modify/Translate
 * interactions) isn't the same component that needs to read it
 * (feature-edit-form.tsx's Save, which sends it alongside whatever
 * attributes changed) -- the map and the form are siblings, not
 * parent/child, same reasoning as every other cross-component signal in
 * this app (connect-store.ts, draw-store.ts).
 */
interface EditState {
  editing: EditingFeature | null
  geometryTool: GeometryTool
  dirtyGeometry: FeatureGeometry | null
  start: (featureClassCode: string, featureId: string) => void
  setGeometryTool: (tool: GeometryTool) => void
  setDirtyGeometry: (geometry: FeatureGeometry) => void
  stop: () => void
}

export const useEditStore = create<EditState>((set) => ({
  editing: null,
  geometryTool: null,
  dirtyGeometry: null,
  start: (featureClassCode, featureId) =>
    set({ editing: { featureClassCode, featureId }, geometryTool: null, dirtyGeometry: null }),
  setGeometryTool: (tool) => set({ geometryTool: tool }),
  setDirtyGeometry: (geometry) => set({ dirtyGeometry: geometry }),
  stop: () => set({ editing: null, geometryTool: null, dirtyGeometry: null }),
}))
