import { create } from "zustand"
import { persist } from "zustand/middleware"

import type { FaultStatus } from "@/hooks/use-faults"

// Fault map overlay state (Faults Slice 3). Unlike the OMS affected-area
// highlight (oms-highlight-store.ts) -- a transient, incident-driven highlight
// with no legend entry -- the fault overlay is a PERSISTENT, user-toggled layer
// with a per-status filter, so `visible` and `statuses` persist. The impact
// selection (impactFaultId + focusToken), by contrast, is a live "which fault
// did I just click" pointer and is NOT persisted.

/** The status classes, in lifecycle order, for the legend toggles. */
export const FAULT_STATUS_ORDER: FaultStatus[] = ["detected", "investigating", "cleared", "closed"]

interface FaultOverlayState {
  /** Master on/off for the whole fault overlay (the "Faults" row in the layers panel). */
  visible: boolean
  /** Which status classes are drawn. Defaults to the LIVE faults (detected +
   * investigating), matching the board's "Open only" default; cleared/closed are
   * off until the user reveals them from the legend. */
  statuses: Record<FaultStatus, boolean>
  /** Whether LIVE faults (detected + investigating) pulse a "radar ping" ring to
   * draw the eye. On by default; suppressed anyway when the OS asks for reduced
   * motion. cleared/closed never pulse -- they aren't urgent. */
  pulse: boolean
  /** The fault whose de-energized downstream section is currently drawn (set when
   * a marker is clicked), or null. Not persisted. */
  impactFaultId: string | null
  /** Bumped each time a fault's impact is shown, so the map fits the view once
   * per click (the same one-shot idiom oms-highlight-store uses). */
  focusToken: number

  toggleVisible: () => void
  setVisible: (v: boolean) => void
  setStatus: (status: FaultStatus, on: boolean) => void
  setPulse: (on: boolean) => void
  /** Show a fault's impact area and fit the view to it once. */
  showImpact: (faultId: string) => void
  clearImpact: () => void
}

export const useFaultOverlayStore = create<FaultOverlayState>()(
  persist(
    (set) => ({
      visible: false,
      statuses: { detected: true, investigating: true, cleared: false, closed: false },
      pulse: true,
      impactFaultId: null,
      focusToken: 0,
      toggleVisible: () => set((s) => ({ visible: !s.visible })),
      setVisible: (v) => set({ visible: v }),
      setStatus: (status, on) => set((s) => ({ statuses: { ...s.statuses, [status]: on } })),
      setPulse: (on) => set({ pulse: on }),
      showImpact: (faultId) => set((s) => ({ impactFaultId: faultId, focusToken: s.focusToken + 1 })),
      clearImpact: () => set({ impactFaultId: null }),
    }),
    {
      name: "voltedge-fault-overlay",
      // Only the layer preferences persist -- the impact pointer is per-session.
      partialize: (s) => ({ visible: s.visible, statuses: s.statuses, pulse: s.pulse }),
    },
  ),
)
