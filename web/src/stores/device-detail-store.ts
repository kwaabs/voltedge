import { create } from "zustand"

/** Which per-device detail the operations slide-over is showing. */
export type DeviceDetailSection = "alarms" | "safety" | "incident"

/**
 * Drives the operations schematic's right-side device-detail slide-over. The
 * operate menu opens it to a section for a device; the drawer (mounted on the
 * operations route) reads it. Not persisted -- a transient inspection, same as the
 * selection store.
 */
interface DeviceDetailState {
  deviceClass: string | null
  deviceId: string | null
  section: DeviceDetailSection
  open: (deviceClass: string, deviceId: string, section: DeviceDetailSection) => void
  setSection: (section: DeviceDetailSection) => void
  close: () => void
}

export const useDeviceDetailStore = create<DeviceDetailState>((set) => ({
  deviceClass: null,
  deviceId: null,
  section: "alarms",
  open: (deviceClass, deviceId, section) => set({ deviceClass, deviceId, section }),
  setSection: (section) => set({ section }),
  close: () => set({ deviceClass: null, deviceId: null }),
}))
