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
  // Bumped only when `select` is called with `focus: true` -- a direct
  // map click already shows the clicked feature on screen, so it never
  // needs to move the view; a selection driven from a connection/
  // relationship list row (feature-panel.tsx) can point at a feature
  // that's off-screen or not even loaded into the current viewport, so
  // that path asks voltedge-map.tsx to pan/zoom to it too. A counter,
  // not a boolean, so two consecutive focus requests for the same
  // feature (the same row clicked twice) still both re-trigger the pan.
  focusToken: number
  select: (featureClassCode: string, featureId: string, options?: { focus?: boolean }) => void
  showCandidates: (candidates: FeatureRef[], position: ScreenPoint) => void
  clear: () => void
}

export const useSelectionStore = create<SelectionState>((set) => ({
  selected: null,
  candidates: null,
  candidatesPosition: null,
  focusToken: 0,
  select: (featureClassCode, featureId, options) =>
    set((state) => ({
      selected: { featureClassCode, featureId },
      candidates: null,
      candidatesPosition: null,
      focusToken: options?.focus ? state.focusToken + 1 : state.focusToken,
    })),
  showCandidates: (candidates, position) => set({ candidates, candidatesPosition: position, selected: null }),
  clear: () => set({ selected: null, candidates: null, candidatesPosition: null }),
}))
