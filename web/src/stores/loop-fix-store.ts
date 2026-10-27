import { create } from "zustand"

/** Ephemeral (not persisted): the feature a loop was found from, driving the
 * loop-fix panel (break options + review connections). Set by the loop check when
 * it finds a ring; cleared when the panel closes. */
interface LoopFixState {
  start: { feature_class: string; feature_id: string } | null
  open: (start: { feature_class: string; feature_id: string }) => void
  close: () => void
}

export const useLoopFixStore = create<LoopFixState>((set) => ({
  start: null,
  open: (start) => set({ start }),
  close: () => set({ start: null }),
}))
