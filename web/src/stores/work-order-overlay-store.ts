import { create } from "zustand"
import { persist } from "zustand/middleware"

// Work-order map overlay state -- a persistent, user-toggled layer that pins the
// OPEN work orders (each at its asset's inherited location) on the map, the twin
// of the fault overlay (fault-overlay-store.ts). Only the on/off preference
// persists; there's no per-status legend (an order's lifecycle is the click-
// through), so the store holds just `visible`.

interface WorkOrderOverlayState {
  visible: boolean
  toggleVisible: () => void
  setVisible: (v: boolean) => void
}

export const useWorkOrderOverlayStore = create<WorkOrderOverlayState>()(
  persist(
    (set) => ({
      visible: false,
      toggleVisible: () => set((s) => ({ visible: !s.visible })),
      setVisible: (v) => set({ visible: v }),
    }),
    { name: "voltedge-work-order-overlay" },
  ),
)
