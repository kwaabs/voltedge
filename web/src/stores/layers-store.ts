import { create } from "zustand"
import { persist } from "zustand/middleware"

interface LayersState {
  /** Only exceptions are stored -- a source id with no entry here is
   * visible by default, so a newly compiled feature class (which the
   * catalog picks up automatically, see src/lib/martin.ts) shows up
   * visible without this store needing to know about it in advance. */
  hidden: Record<string, boolean>
  toggle: (sourceId: string) => void
}

export const useLayersStore = create<LayersState>()(
  persist(
    (set) => ({
      hidden: {},
      toggle: (sourceId) =>
        set((s) => ({ hidden: { ...s.hidden, [sourceId]: !s.hidden[sourceId] } })),
    }),
    { name: "voltedge-layers" },
  ),
)
