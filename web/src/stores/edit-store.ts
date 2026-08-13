import { create } from "zustand"

export interface EditingFeature {
  featureClassCode: string
  featureId: string
}

/**
 * Not persisted -- an in-progress edit has no meaning across a reload.
 * Deliberately just a session marker (which feature, if any, is under
 * modification) rather than holding the edit's own field values --
 * those live as local state in feature-edit-form.tsx, the only thing
 * that actually renders them. This store exists so *other* components
 * can react to "a feature is being edited" without needing the edit's
 * contents: feature-panel.tsx locks the Terminals/Relationships tabs
 * while editing (switching tabs would unmount the form and silently
 * drop unsaved changes), and voltedge-map.tsx blocks normal
 * click-to-select the same way draw-store.ts/connect-store.ts already
 * block it for their own in-progress workflows.
 *
 * Attribute-only for now (see docs/phase-3-map-aesthetics.md's sibling
 * write-up for the sequencing) -- geometry editing is a later slice
 * that will extend this same store rather than replace it, since both
 * are really "this feature is under modification," attributes and/or
 * geometry.
 */
interface EditState {
  editing: EditingFeature | null
  start: (featureClassCode: string, featureId: string) => void
  stop: () => void
}

export const useEditStore = create<EditState>((set) => ({
  editing: null,
  start: (featureClassCode, featureId) => set({ editing: { featureClassCode, featureId } }),
  stop: () => set({ editing: null }),
}))
