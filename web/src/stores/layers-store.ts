import { create } from "zustand"
import { persist } from "zustand/middleware"

interface LayersState {
  /** Only exceptions are stored -- a source id with no entry here is
   * visible by default, so a newly compiled feature class (which the
   * catalog picks up automatically, see src/lib/martin.ts) shows up
   * visible without this store needing to know about it in advance. */
  hidden: Record<string, boolean>
  toggle: (sourceId: string) => void
  /** Per-layer label suppression, orthogonal to `hidden` (a layer can be
   * visible with its labels off, or vice versa). Same exception-only
   * convention: a source id with no entry here shows its class's labels
   * by default (whenever that class has a label template at all), so a
   * newly compiled class labels normally without this store knowing it. */
  labelsHidden: Record<string, boolean>
  toggleLabels: (sourceId: string) => void
}

export const useLayersStore = create<LayersState>()(
  persist(
    (set) => ({
      hidden: {},
      toggle: (sourceId) =>
        set((s) => ({ hidden: { ...s.hidden, [sourceId]: !s.hidden[sourceId] } })),
      labelsHidden: {},
      toggleLabels: (sourceId) =>
        set((s) => ({ labelsHidden: { ...s.labelsHidden, [sourceId]: !s.labelsHidden[sourceId] } })),
    }),
    { name: "voltedge-layers" },
  ),
)
