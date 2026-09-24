import { create } from "zustand"

/** The default radius (metres) a freshly dropped point starts at -- a
 * neighbourhood-scale circle that reads clearly at street zoom without swamping
 * the view. The user tunes it from the panel afterwards. */
export const DEFAULT_RADIUS_M = 250

/** The radius presets the panel offers as quick buttons, in metres. */
export const RADIUS_PRESETS_M = [100, 250, 500, 1000, 2000] as const

/**
 * The radius-scope picker's state. It backs the query-builder panel's Aggregate
 * "Radius of a point" scope (feature-query-panel.tsx): arming it makes the next
 * map click drop a center point (`armed` -> false, `center` set); voltedge-map.tsx
 * owns the crosshair cursor, the drawn circle, and the click-to-drop, all keyed
 * off this store, and the panel reads the center + radius back to build the
 * aggregate's radius scope and to re-draw the circle as the radius is tuned.
 *
 * Session-only, like the other ad-hoc map tools (measure/box-zoom/select): a
 * dropped point and its radius have no meaning across a reload. Distinct from
 * the query-store's feature highlight set -- this drives a count/aggregate, not a
 * selection, so nothing is ever highlighted on the map.
 */
interface RadiusQueryState {
  /** The tool is armed and waiting for a map click to drop the center. */
  armed: boolean
  /** The dropped center as [longitude, latitude] (WGS84), or null before a
   * drop. Its presence is what mounts the results panel. */
  center: [number, number] | null
  /** The current query radius in metres. */
  radiusM: number
  /** Arm the tool (the next map click drops the center). */
  arm: () => void
  /** Disarm without dropping (Escape / toggling the button off) -- leaves any
   * existing center/results in place. */
  disarm: () => void
  /** Drop the center at a clicked point; disarms (one drop per arm). */
  setCenter: (center: [number, number]) => void
  /** Tune the radius (the circle and the counts follow). */
  setRadius: (radiusM: number) => void
  /** Tear the whole tool down: no center, no circle, panel closes, disarmed. */
  clear: () => void
}

export const useRadiusQueryStore = create<RadiusQueryState>((set) => ({
  armed: false,
  center: null,
  radiusM: DEFAULT_RADIUS_M,
  arm: () => set({ armed: true }),
  disarm: () => set({ armed: false }),
  setCenter: (center) => set({ center, armed: false }),
  setRadius: (radiusM) => set({ radiusM: Math.max(1, radiusM) }),
  clear: () => set({ armed: false, center: null, radiusM: DEFAULT_RADIUS_M }),
}))
