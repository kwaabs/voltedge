import { create } from "zustand"
import { persist } from "zustand/middleware"

/** The identity + level of one territory, as carried in its boundary
 * feature's GeoJSON properties (GET /territories/boundaries) -- everything
 * the read-only click panel needs without a second fetch. */
export interface TerritoryOverlaySelection {
  id: string
  parent_id: string | null
  name: string
  level_code: string
  level_name: string
  level_rank: number
}

interface TerritoryOverlayState {
  /** Whether the boundary overlay layer is drawn at all -- the master
   * switch that also gates the (large) boundary fetch. Persisted, so the
   * choice survives reloads like the feature-layer visibility does. */
  visible: boolean
  /** Per-level visibility *within* the overlay, keyed by level_code (e.g.
   * "district"). A code present and true is hidden; absent/false is shown,
   * so a fresh overlay shows every level. Lets one level be peeled back
   * (hide districts, keep regions) instead of the master all-or-nothing.
   * Persisted alongside `visible`. */
  hiddenLevels: Record<string, boolean>
  /** Whether each boundary is labelled with its territory name. On by
   * default; some users want the coloured areas without the name clutter,
   * especially with several levels stacked. Persisted alongside `visible`. */
  showLabels: boolean
  /** The territory whose read-only panel is open (clicked on the overlay),
   * or null. Transient -- never persisted. */
  selected: TerritoryOverlaySelection | null
  toggle: () => void
  toggleLevel: (levelCode: string) => void
  toggleLabels: () => void
  selectTerritory: (t: TerritoryOverlaySelection) => void
  clearSelected: () => void
}

/**
 * The territory boundary overlay: a single toggleable map layer plus the
 * one territory currently inspected. Kept out of the feature-layer store
 * (layers-store.ts, keyed by Martin source id) because this is one fixed
 * overlay, not a per-feature-class layer. Turning the overlay off also
 * closes any open territory panel -- an inspect panel for a layer you can
 * no longer see would be stranded.
 */
export const useTerritoryOverlayStore = create<TerritoryOverlayState>()(
  persist(
    (set) => ({
      visible: false,
      hiddenLevels: {},
      showLabels: true,
      selected: null,
      toggle: () => set((s) => ({ visible: !s.visible, selected: s.visible ? null : s.selected })),
      // Flip one level's visibility. Hiding the level whose territory is
      // currently inspected also closes the panel -- same "don't strand an
      // inspector for something you can no longer see" rule as the master
      // toggle above.
      toggleLevel: (levelCode) =>
        set((s) => {
          const nowHidden = !s.hiddenLevels[levelCode]
          const hiddenLevels = { ...s.hiddenLevels, [levelCode]: nowHidden }
          const selected = nowHidden && s.selected?.level_code === levelCode ? null : s.selected
          return { hiddenLevels, selected }
        }),
      toggleLabels: () => set((s) => ({ showLabels: !s.showLabels })),
      selectTerritory: (t) => set({ selected: t }),
      clearSelected: () => set({ selected: null }),
    }),
    {
      name: "voltedge-territory-overlay",
      partialize: (s) => ({ visible: s.visible, hiddenLevels: s.hiddenLevels, showLabels: s.showLabels }),
    },
  ),
)
