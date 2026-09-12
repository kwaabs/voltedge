import { create } from "zustand"

/**
 * The OMS incident whose outage cone the map is highlighting -- written from
 * the /outages board ("Show on map" on an incident row) and read by
 * voltedge-map.tsx, which fetches the incident's affected service points
 * (GET /oms/incidents/{id}/affected) and draws them as the outage-affected
 * highlight layer.
 *
 * Same "not persisted, one module-level singleton read across routes" shape as
 * trace-store / isolate-store: a page navigation from /outages to the map keeps
 * this because it's a module singleton, but a reload clears it (an outage
 * highlight is a transient investigation, not saved state).
 *
 * `focusToken` is a monotonic one-shot "fit the view to this set" command (same
 * idiom as selection-store's focusToken): every show() bumps it so the map fits
 * once, and a manual pan afterwards isn't yanked back on the next unrelated
 * render.
 */
interface OmsHighlightState {
  incidentId: string | null
  focusToken: number
  show: (incidentId: string) => void
  clear: () => void
}

export const useOmsHighlightStore = create<OmsHighlightState>((set) => ({
  incidentId: null,
  focusToken: 0,
  show: (incidentId) => set((s) => ({ incidentId, focusToken: s.focusToken + 1 })),
  clear: () => set({ incidentId: null }),
}))
