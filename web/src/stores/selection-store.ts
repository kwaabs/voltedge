import { create } from "zustand"

export interface FeatureRef {
  featureClassCode: string
  featureId: string
}

export interface ScreenPoint {
  x: number
  y: number
}

/**
 * Not persisted -- which feature is selected (or up for disambiguation)
 * has no meaning across a reload. Written by VoltEdgeMap's click
 * handler, read by FeaturePanel and PeerPicker -- all siblings, not
 * parent/child, same reasoning as map-pointer-store.ts.
 *
 * `candidates` and `selected` are mutually exclusive: a click that hits
 * several stacked features populates `candidates` (for PeerPicker to
 * render a small disambiguation list) rather than guessing which one
 * the user meant; a click that hits exactly one feature -- including
 * picking one out of `candidates` -- goes straight to `selected`.
 */
interface SelectionState {
  selected: FeatureRef | null
  candidates: FeatureRef[] | null
  candidatesPosition: ScreenPoint | null
  select: (featureClassCode: string, featureId: string) => void
  showCandidates: (candidates: FeatureRef[], position: ScreenPoint) => void
  clear: () => void
}

export const useSelectionStore = create<SelectionState>((set) => ({
  selected: null,
  candidates: null,
  candidatesPosition: null,
  select: (featureClassCode, featureId) =>
    set({ selected: { featureClassCode, featureId }, candidates: null, candidatesPosition: null }),
  showCandidates: (candidates, position) => set({ candidates, candidatesPosition: position, selected: null }),
  clear: () => set({ selected: null, candidates: null, candidatesPosition: null }),
}))
