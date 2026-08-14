import { create } from "zustand"

/**
 * Which diagram instance (if any) is open in the map route's
 * side-by-side split view -- not persisted, same reasoning as
 * selection-store.ts. A single id, not a stack: opening a second
 * diagram replaces whichever was open, there's no multi-diagram tiling.
 */
interface DiagramState {
  openDiagramId: string | null
  open: (diagramId: string) => void
  close: () => void
}

export const useDiagramStore = create<DiagramState>((set) => ({
  openDiagramId: null,
  open: (diagramId) => set({ openDiagramId: diagramId }),
  close: () => set({ openDiagramId: null }),
}))
