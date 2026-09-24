import { create } from "zustand"

/** Which way an armed box-zoom fits the view to the drawn rectangle:
 * `in` fits the box to the viewport (drawing a small box zooms in, the classic
 * box zoom); `out` fits the current viewport into the box (a small box zooms
 * out further). `null` = the tool is disarmed. */
export type BoxZoomMode = "in" | "out"

/**
 * The box-zoom tool's armed state. When a `mode` is set, voltedge-map.tsx arms a
 * throwaway Draw interaction that rubber-bands a rectangle over the map; on
 * completion it fits the view to (or from) that box per the mode and disarms
 * (single-shot), so `mode` goes back to null on its own. On desktop, holding
 * Shift while dragging inverts the armed mode for that one box. Like
 * measure-store.ts this owns no geometry itself -- the map owns the interaction
 * and scratch layer keyed off this state, and nothing is ever created.
 * Session-only: an armed tool has no meaning across a reload.
 */
interface BoxZoomState {
  mode: BoxZoomMode | null
  /** Convenience: whether either direction is armed (the map effect gates on this). */
  active: boolean
  /** Arm a direction outright (used when inverting). */
  activate: (mode: BoxZoomMode) => void
  deactivate: () => void
  /** Toggle a direction from its own button: re-arms if a different direction was
   * active, disarms if the same one was. */
  toggle: (mode: BoxZoomMode) => void
}

export const useBoxZoomStore = create<BoxZoomState>((set) => ({
  mode: null,
  active: false,
  activate: (mode) => set({ mode, active: true }),
  deactivate: () => set({ mode: null, active: false }),
  toggle: (mode) =>
    set((s) => (s.mode === mode ? { mode: null, active: false } : { mode, active: true })),
}))
