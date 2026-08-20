import { create } from "zustand"

/**
 * Whether the geographic export dialog is open -- the one bit the map toolbar's
 * Export button toggles, mirroring query-store's `open`. Not persisted: a
 * half-configured export has no meaning across a reload, and the dialog owns
 * its own form state while mounted.
 */
interface ExportState {
  open: boolean
  openDialog: () => void
  closeDialog: () => void
  toggle: () => void
}

export const useExportStore = create<ExportState>((set) => ({
  open: false,
  openDialog: () => set({ open: true }),
  closeDialog: () => set({ open: false }),
  toggle: () => set((s) => ({ open: !s.open })),
}))
