import { create } from "zustand"
import { persist } from "zustand/middleware"

/**
 * Desktop sidebar expanded/collapsed state. Deliberately its own store,
 * not local component state -- persisted so the choice survives a
 * reload, and readable from anywhere (header, keyboard shortcut) without
 * prop-drilling through the layout tree.
 */
interface UIState {
  sidebarOpen: boolean
  setSidebarOpen: (open: boolean) => void
  toggleSidebar: () => void
}

export const useUIStore = create<UIState>()(
  persist(
    (set) => ({
      sidebarOpen: true,
      setSidebarOpen: (open) => set({ sidebarOpen: open }),
      toggleSidebar: () => set((s) => ({ sidebarOpen: !s.sidebarOpen })),
    }),
    { name: "voltedge-ui" },
  ),
)
