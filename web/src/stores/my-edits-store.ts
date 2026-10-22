import { create } from "zustand"

/**
 * The "My edits" map panel's open/closed state -- the self-service feed of the
 * signed-in user's own recent changes (GET /features/my-activity). Not persisted:
 * like every other ephemeral map-workflow store (query, trace, export jobs), an
 * open panel has no meaning across a reload, and the FloatingPanel owns its own
 * position/size, so this holds nothing but the toggle. The toolbar button in
 * map.tsx reads `open` and calls `toggle`; the panel self-gates on `open`.
 */
interface MyEditsState {
  open: boolean
  openPanel: () => void
  closePanel: () => void
  toggle: () => void
}

export const useMyEditsStore = create<MyEditsState>((set) => ({
  open: false,
  openPanel: () => set({ open: true }),
  closePanel: () => set({ open: false }),
  toggle: () => set((s) => ({ open: !s.open })),
}))
