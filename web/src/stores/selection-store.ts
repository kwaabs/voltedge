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
 * `candidates` and `selected` are mutually exclusive for a normal
 * selection click: hitting several stacked features populates
 * `candidates` (for PeerPicker to render a small disambiguation list)
 * rather than guessing which one the user meant, clearing whatever was
 * previously `selected`; a click that hits exactly one feature --
 * including picking one out of `candidates` -- goes straight to
 * `selected`. Not mutually exclusive during connect-mode target-picking
 * (voltedge-map.tsx, peer-picker.tsx) though: `candidates` there means
 * "possible connect targets," which has nothing to do with whichever
 * feature is the connect workflow's own source and still needs to stay
 * visible in FeaturePanel throughout -- see `showCandidates`'s
 * `clearSelected` option.
 */
interface SelectionState {
  selected: FeatureRef | null
  // A feature merely "located" (peeked) from the open panel's Terminals/
  // Relationships list -- highlighted on the map (a `peek`-role halo, see
  // voltedge-map.tsx) WITHOUT opening its own panel, so the user stays on the
  // current feature while eyeing which counterpart a row refers to. Distinct
  // from `selected` precisely so it does NOT drive FeaturePanel; the list row's
  // info button still calls `select` when the user actually wants to open it.
  // Cleared automatically by any real selection change (`select`/`clear`) so a
  // peek halo never outlives the panel it was cast from.
  located: FeatureRef | null
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
  // Bumped when `locate` is called with `focus: true` -- the "zoom to this row"
  // control on a study/trace result row: it highlights the feature (located halo)
  // AND flies the map to it, WITHOUT opening its attribute panel, so the results
  // stay on screen. A counter, not a boolean, so zooming the same row twice re-pans.
  locatedFocusToken: number
  select: (featureClassCode: string, featureId: string, options?: { focus?: boolean }) => void
  // Re-trigger a pan/zoom to the currently-selected feature without changing
  // the selection -- the status-bar "zoom to feature" control, for when a panel
  // opened but the view didn't move (a selection made without focus, or a
  // feature whose geometry wasn't in view). No-op when nothing is selected.
  focusSelected: () => void
  // Highlight a counterpart on the map without opening its panel (see `located`).
  // With `focus: true`, also fly the map to it (the per-row "zoom" control) -- still
  // without opening its panel, unlike `select`.
  locate: (featureClassCode: string, featureId: string, options?: { focus?: boolean }) => void
  // Drop the peek halo without touching `selected` -- used when the panel's tab
  // that cast it (feature-panel.tsx's Terminals/Relationships list) unmounts.
  clearLocated: () => void
  showCandidates: (candidates: FeatureRef[], position: ScreenPoint, options?: { clearSelected?: boolean }) => void
  // Dismisses the disambiguation list without touching `selected` --
  // peer-picker.tsx uses this once a connect-mode target has been
  // picked (connect-store.ts's own pickTarget, not this store's
  // `select`), since `clear()` would also drop the connect workflow's
  // source feature out of FeaturePanel.
  clearCandidates: () => void
  clear: () => void
}

export const useSelectionStore = create<SelectionState>((set) => ({
  selected: null,
  located: null,
  candidates: null,
  candidatesPosition: null,
  focusToken: 0,
  locatedFocusToken: 0,
  select: (featureClassCode, featureId, options) =>
    set((state) => ({
      selected: { featureClassCode, featureId },
      // Opening a panel supersedes any peek -- the newly selected feature gets
      // its own `selected` halo, so a leftover peek halo would be noise.
      located: null,
      candidates: null,
      candidatesPosition: null,
      focusToken: options?.focus ? state.focusToken + 1 : state.focusToken,
    })),
  focusSelected: () => set((state) => (state.selected ? { focusToken: state.focusToken + 1 } : state)),
  locate: (featureClassCode, featureId, options) =>
    set((state) => ({
      located: { featureClassCode, featureId },
      locatedFocusToken: options?.focus ? state.locatedFocusToken + 1 : state.locatedFocusToken,
    })),
  clearLocated: () => set({ located: null }),
  showCandidates: (candidates, position, options) =>
    set((state) => ({
      candidates,
      candidatesPosition: position,
      selected: options?.clearSelected === false ? state.selected : null,
    })),
  clearCandidates: () => set({ candidates: null, candidatesPosition: null }),
  clear: () => set({ selected: null, located: null, candidates: null, candidatesPosition: null }),
}))
