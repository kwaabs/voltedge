import { create } from "zustand"

export interface FeatureRef {
  featureClassCode: string
  featureId: string
}

/**
 * Not persisted -- which feature is selected has no meaning across a
 * reload (the feature might not even exist anymore by then). Written by
 * VoltEdgeMap's click handler, read by FeaturePanel -- siblings, not
 * parent/child, same reasoning as map-pointer-store.ts.
 */
interface SelectionState {
  selected: FeatureRef | null
  select: (featureClassCode: string, featureId: string) => void
  clear: () => void
}

export const useSelectionStore = create<SelectionState>((set) => ({
  selected: null,
  select: (featureClassCode, featureId) => set({ selected: { featureClassCode, featureId } }),
  clear: () => set({ selected: null }),
}))
