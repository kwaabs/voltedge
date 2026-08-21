import { create } from "zustand"

/**
 * The two independent bits of export UI state. `open` is the config dialog (the
 * map toolbar's Export button toggles it, mirroring query-store's `open`).
 * `jobsPanelOpen` is the floating panel that watches submitted jobs -- separate
 * because the panel outlives the dialog: enqueueing a job closes the dialog but
 * opens the panel so the caller can watch it run and grab the download link.
 * Neither is persisted: a half-configured export has no meaning across a reload,
 * and the jobs list is server-owned (re-fetched fresh when the panel reopens).
 */
interface ExportState {
  open: boolean
  jobsPanelOpen: boolean
  openDialog: () => void
  closeDialog: () => void
  toggle: () => void
  openJobsPanel: () => void
  closeJobsPanel: () => void
}

export const useExportStore = create<ExportState>((set) => ({
  open: false,
  jobsPanelOpen: false,
  openDialog: () => set({ open: true }),
  closeDialog: () => set({ open: false }),
  toggle: () => set((s) => ({ open: !s.open })),
  openJobsPanel: () => set({ jobsPanelOpen: true }),
  closeJobsPanel: () => set({ jobsPanelOpen: false }),
}))
