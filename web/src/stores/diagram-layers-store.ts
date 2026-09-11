import { create } from "zustand"
import { persist } from "zustand/middleware"

/** Composite key so visibility is remembered per (diagram, feature class):
 * hiding a class on one diagram must not hide it on another, unlike the
 * geographic map's single global layer set (src/stores/layers-store.ts). */
function key(diagramId: string, classCode: string): string {
  return `${diagramId}::${classCode}`
}

interface DiagramLayersState {
  /** Only exceptions are stored -- a (diagram, class) with no entry here is
   * visible by default, so a class newly placed on a diagram shows without
   * this store needing to know about it in advance. */
  hidden: Record<string, boolean>
  isHidden: (diagramId: string, classCode: string) => boolean
  toggle: (diagramId: string, classCode: string) => void
}

export const useDiagramLayersStore = create<DiagramLayersState>()(
  persist(
    (set, get) => ({
      hidden: {},
      isHidden: (diagramId, classCode) => Boolean(get().hidden[key(diagramId, classCode)]),
      toggle: (diagramId, classCode) =>
        set((s) => {
          const k = key(diagramId, classCode)
          return { hidden: { ...s.hidden, [k]: !s.hidden[k] } }
        }),
    }),
    { name: "voltedge-diagram-layers" },
  ),
)

/** The composite key a component builds to read `hidden` directly (e.g. in a
 * checkbox's `checked`), matching what `toggle`/`isHidden` use internally. */
export const diagramLayerKey = key
