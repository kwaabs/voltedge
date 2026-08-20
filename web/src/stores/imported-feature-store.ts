import { create } from "zustand"

/**
 * The imported-overlay feature a click landed on (Phase 15 Slice 5), shown in a
 * lightweight read-only attribute popup (see imported-feature-popup.tsx). These
 * features are never in the database, so they can't open the DB-coupled
 * FeaturePanel -- this store carries just what the popup needs: the source
 * overlay's name, the feature's own GeoJSON properties (its `geometry` and the
 * internal `__overlay*` tags already stripped), and the click's viewport
 * position so the popup can float there, clamped on-screen. Not persisted --
 * it's transient UI state, cleared on the next map click, an outside click, or
 * Escape.
 */
export interface ImportedFeatureSelection {
  overlayName: string
  properties: Record<string, unknown>
  /** Viewport (client) coordinates of the click, for positioning the popup. */
  position: { x: number; y: number }
}

interface ImportedFeatureState {
  selected: ImportedFeatureSelection | null
  show: (selection: ImportedFeatureSelection) => void
  clear: () => void
}

export const useImportedFeatureStore = create<ImportedFeatureState>((set) => ({
  selected: null,
  show: (selection) => set({ selected: selection }),
  clear: () => set({ selected: null }),
}))
