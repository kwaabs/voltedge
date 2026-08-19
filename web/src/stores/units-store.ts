import { create } from "zustand"
import { persist } from "zustand/middleware"

export type Units = "metric" | "imperial"

/**
 * A durable display preference, shared by every measurement read-out in
 * the app -- the ad-hoc measure tool's live tooltips (voltedge-map.tsx)
 * and the feature panel's computed Length/Area rows (feature-panel.tsx)
 * both format against this one flag, so flipping units anywhere flips
 * them everywhere. Persisted (unlike the session-only draw/edit/measure
 * stores): which units a user thinks in is a lasting preference, not a
 * per-session one. Metric default -- this is a utility GIS whose
 * geometry math is metric end to end (EPSG:3857 meters, ol/sphere).
 */
interface UnitsState {
  units: Units
  toggle: () => void
  setUnits: (units: Units) => void
}

export const useUnitsStore = create<UnitsState>()(
  persist(
    (set) => ({
      units: "metric",
      toggle: () => set((s) => ({ units: s.units === "metric" ? "imperial" : "metric" })),
      setUnits: (units) => set({ units }),
    }),
    { name: "voltedge-units" },
  ),
)
