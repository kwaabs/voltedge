import { create } from "zustand"

export type MeasureMode = "distance" | "area"

/**
 * The ad-hoc measurement tool's active mode -- `distance` draws a
 * polyline read out as length, `area` a polygon read out as area, null
 * is off. voltedge-map.tsx owns the actual OL Draw interaction, scratch
 * layer, and live tooltips keyed off this (see its measure effect); the
 * measurements it produces are ephemeral (no feature is created) and
 * cleared when the mode goes back to null. Session-only, same as
 * draw-store.ts -- an in-progress measurement has no meaning across a
 * reload. Distinct from the feature panel's own Length/Area rows, which
 * measure an existing feature's real geometry and need no tool at all.
 */
interface MeasureState {
  mode: MeasureMode | null
  setMode: (mode: MeasureMode | null) => void
}

export const useMeasureStore = create<MeasureState>((set) => ({
  mode: null,
  setMode: (mode) => set({ mode }),
}))
