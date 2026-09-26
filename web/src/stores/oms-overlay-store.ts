import { create } from "zustand"
import { persist } from "zustand/middleware"

import type { IncidentStatus } from "@/hooks/use-oms"

// OMS incident map overlay state -- the twin of fault-overlay-store.ts. Unlike
// the transient, board-driven affected-area highlight (oms-highlight-store.ts,
// which has no legend entry), this is a PERSISTENT, user-toggled layer with a
// per-status filter, so `visible` and `statuses` persist. The cone selection
// (coneIncidentId + focusToken) -- "which incident did I just click, draw its
// cones" -- is a live per-session pointer and is NOT persisted.

/** The incident status classes, in lifecycle order, for the legend toggles. */
export const INCIDENT_STATUS_ORDER: IncidentStatus[] = [
  "predicted",
  "scheduled",
  "confirmed",
  "dispatched",
  "restored",
  "cancelled",
]

interface OmsOverlayState {
  /** Master on/off for the whole outage overlay (the "Outages" row in the layers panel). */
  visible: boolean
  /** Which status classes are drawn. Defaults to the OPEN incidents (everything
   * but restored/cancelled), matching the board's "Open only" default; the two
   * terminal states are off until the user reveals them from the legend. */
  statuses: Record<IncidentStatus, boolean>
  /** Whether LIVE incidents (customers currently out: predicted / confirmed /
   * dispatched) pulse a "radar ping" ring to draw the eye. On by default;
   * suppressed anyway under reduced-motion. scheduled (a future planned outage)
   * and restored/cancelled never pulse -- nobody is out. */
  pulse: boolean
  /** The incident whose outage cones are currently drawn (set when a marker is
   * clicked), or null. Not persisted. */
  coneIncidentId: string | null
  /** Bumped each time an incident's cones are shown, so the map fits the view
   * once per click (the same one-shot idiom the fault overlay uses). */
  focusToken: number

  toggleVisible: () => void
  setVisible: (v: boolean) => void
  setStatus: (status: IncidentStatus, on: boolean) => void
  setPulse: (on: boolean) => void
  /** Show an incident's outage cones (frozen affected + live de-energized) and
   * fit the view to them once. */
  showCone: (incidentId: string) => void
  clearCone: () => void
}

export const useOmsOverlayStore = create<OmsOverlayState>()(
  persist(
    (set) => ({
      visible: false,
      statuses: {
        predicted: true,
        scheduled: true,
        confirmed: true,
        dispatched: true,
        restored: false,
        cancelled: false,
      },
      pulse: true,
      coneIncidentId: null,
      focusToken: 0,
      toggleVisible: () => set((s) => ({ visible: !s.visible })),
      setVisible: (v) => set({ visible: v }),
      setStatus: (status, on) => set((s) => ({ statuses: { ...s.statuses, [status]: on } })),
      setPulse: (on) => set({ pulse: on }),
      showCone: (incidentId) => set((s) => ({ coneIncidentId: incidentId, focusToken: s.focusToken + 1 })),
      clearCone: () => set({ coneIncidentId: null }),
    }),
    {
      name: "voltedge-oms-overlay",
      // Only the layer preferences persist -- the cone pointer is per-session.
      partialize: (s) => ({ visible: s.visible, statuses: s.statuses, pulse: s.pulse }),
    },
  ),
)
