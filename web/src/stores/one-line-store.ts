import { create } from "zustand"

import type { OneLineSource } from "@/lib/one-line"

/**
 * The ephemeral one-line view. Opening it shows a TEMPORARY single-line built
 * from a selection or the current map view -- nothing is persisted; closing it
 * discards it. Module-level like the other map tool stores.
 */
interface OneLineState {
  open: boolean
  source: OneLineSource | null
  show: (source: OneLineSource) => void
  close: () => void
}

export const useOneLineStore = create<OneLineState>((set) => ({
  open: false,
  source: null,
  show: (source) => set({ open: true, source }),
  close: () => set({ open: false, source: null }),
}))
