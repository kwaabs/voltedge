import { create } from "zustand"

export type MapMode = "select" | "pan"

/**
 * Not persisted, deliberately -- unlike theme or sidebar-collapsed,
 * which mode you're in is a moment-to-moment choice with no reason to
 * be remembered between sessions. Starting fresh in "select" every time
 * (the more common action) is the right default.
 */
interface InteractionState {
  mode: MapMode
  setMode: (mode: MapMode) => void
}

export const useInteractionStore = create<InteractionState>((set) => ({
  mode: "select",
  setMode: (mode) => set({ mode }),
}))
