import { create } from "zustand"

/**
 * "Drop a pin on the map" mode for the Pins dialog. Instead of typing or
 * accepting the view-centre coordinates, the user arms this from the dialog,
 * the dialog closes, and the next map click captures a lng/lat (voltedge-map's
 * handleClick reads `active`, same armed-picker pattern as the radius-query and
 * diagram-place pickers). The picked point is then consumed by the dialog, which
 * reopens pre-filled — see map-pin-placement handling in status-bar.tsx.
 *
 * One drop per arm: pick() disarms. Escape (handled while placing) cancels.
 */
interface MapPinPlacementState {
  /** True while waiting for the user to click the map to drop a pin. */
  active: boolean
  /** The last dropped point, consumed by the dialog to pre-fill a new pin. */
  picked: { lng: number; lat: number } | null
  /** Arm placement — the next map click becomes a dropped pin. */
  arm: () => void
  /** Record the clicked point and disarm. */
  pick: (lng: number, lat: number) => void
  /** Cancel placement without dropping (Escape). */
  cancel: () => void
  /** Clear the picked point once the dialog has read it. */
  consume: () => void
}

export const useMapPinPlacementStore = create<MapPinPlacementState>((set) => ({
  active: false,
  picked: null,
  arm: () => set({ active: true, picked: null }),
  pick: (lng, lat) => set({ active: false, picked: { lng, lat } }),
  cancel: () => set({ active: false }),
  consume: () => set({ picked: null }),
}))
