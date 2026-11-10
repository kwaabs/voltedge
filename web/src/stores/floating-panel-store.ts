import { create } from "zustand"
import { persist } from "zustand/middleware"

/** One floating panel's remembered geometry. null = never moved/resized. */
export interface PanelGeometry {
  position: { x: number; y: number } | null
  size: { width: number; height: number } | null
}

/**
 * Remembers where the user dragged / how they sized each floating panel that opts in
 * (by passing a `storageKey` to useFloatingPanel). Persisted to localStorage so an
 * operator's arrangement survives closing/reopening a panel AND a full reload --
 * otherwise the shared useFloatingPanel keeps geometry in local component state that
 * resets every time the panel unmounts. Only panels that pass a key are stored.
 */
interface FloatingPanelStore {
  panels: Record<string, PanelGeometry>
  save: (key: string, geom: PanelGeometry) => void
}

export const useFloatingPanelStore = create<FloatingPanelStore>()(
  persist(
    (set) => ({
      panels: {},
      save: (key, geom) => set((s) => ({ panels: { ...s.panels, [key]: geom } })),
    }),
    { name: "voltedge-floating-panels" },
  ),
)
