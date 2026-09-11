import { create } from "zustand"
import { persist } from "zustand/middleware"

/**
 * A saved map bookmark -- a named location the user can jump back to. Purely a
 * personal navigation aid (not network data), so it lives per-browser in
 * localStorage rather than the server, the same split the basemap choice uses.
 */
export interface MapPin {
  id: string
  name: string
  lng: number
  lat: number
  /** Epoch ms, so the list can show newest first. */
  createdAt: number
}

interface MapPinsState {
  pins: MapPin[]
  addPin: (pin: { name: string; lng: number; lat: number }) => void
  removePin: (id: string) => void
  clearPins: () => void
}

function newId(): string {
  // crypto.randomUUID is available in every browser the app targets; the
  // fallback keeps a dev build from throwing if it's somehow absent.
  return typeof crypto !== "undefined" && "randomUUID" in crypto
    ? crypto.randomUUID()
    : `pin-${Date.now()}-${Math.random().toString(36).slice(2)}`
}

/**
 * The user's saved pins, persisted per-browser. Newest are unshifted to the
 * front so the list reads most-recent-first without sorting on every render.
 * The map (voltedge-map.tsx) renders these on a dedicated layer; the pins
 * dialog (pins-dialog.tsx) manages them and flies to one via map-goto-store.
 */
export const useMapPinsStore = create<MapPinsState>()(
  persist(
    (set) => ({
      pins: [],
      addPin: ({ name, lng, lat }) =>
        set((s) => ({
          pins: [{ id: newId(), name: name.trim(), lng, lat, createdAt: Date.now() }, ...s.pins],
        })),
      removePin: (id) => set((s) => ({ pins: s.pins.filter((p) => p.id !== id) })),
      clearPins: () => set({ pins: [] }),
    }),
    { name: "voltedge-map-pins" },
  ),
)
