import { create } from "zustand"
import { createJSONStorage, persist } from "zustand/middleware"

/** The source formats a user can import as a map overlay (Phase 15). Only
 * "geojson" is wired in Slice 1; the rest arrive with their parsers. */
export type ImportFormat = "geojson" | "kmz" | "shapefile" | "dxf"

/** A GeoJSON FeatureCollection, typed loosely on features since OpenLayers'
 * GeoJSON reader consumes the raw object and we only ever pass it through. */
export interface GeoJSONFeatureCollection {
  type: "FeatureCollection"
  features: unknown[]
}

/**
 * One imported overlay: a foreign GIS/CAD file the user dropped on the map,
 * normalized to a canonical EPSG:4326 GeoJSON FeatureCollection. Reprojection
 * to the view CRS (EPSG:3857) happens at render time via OpenLayers'
 * featureProjection, so the stored geometry stays projection-neutral (and, at
 * 4326, compact). This is display data only -- never written to the database.
 */
export interface ImportedOverlay {
  id: string
  name: string
  format: ImportFormat
  featureCount: number
  /** Canonical EPSG:4326 GeoJSON. */
  geojson: GeoJSONFeatureCollection
  visible: boolean
  importedAt: number
  /** True when the overlay is too large to persist across reloads (see the
   * size guard in the import dialog): it lives for the session only and is
   * filtered out of what gets written to localStorage. */
  ephemeral?: boolean
}

interface ImportedOverlaysState {
  overlays: ImportedOverlay[]
  /** Adds a parsed overlay, assigning its id/importedAt and defaulting it
   * visible. Returns the new id. */
  add: (overlay: Omit<ImportedOverlay, "id" | "importedAt" | "visible">) => string
  remove: (id: string) => void
  toggle: (id: string) => void
  rename: (id: string, name: string) => void
}

/**
 * localStorage-backed, but wrapped so a quota overflow (the ~5 MB ceiling)
 * degrades to session-only rather than throwing: the in-memory overlays keep
 * working, they just aren't persisted. Per-overlay `ephemeral` handles the
 * known-large case up front (see the dialog); this catch is the backstop for
 * the aggregate case where several medium overlays together exceed the quota.
 */
const safeStorage = createJSONStorage<Pick<ImportedOverlaysState, "overlays">>(() => ({
  getItem: (name) => localStorage.getItem(name),
  setItem: (name, value) => {
    try {
      localStorage.setItem(name, value)
    } catch {
      // Quota exceeded -- keep the session state, skip persistence.
    }
  },
  removeItem: (name) => localStorage.removeItem(name),
}))

/**
 * The imported map overlays (Phase 15 data import). Kept out of the
 * feature-layer store (layers-store.ts, keyed by Martin source id) and the
 * territory overlay store: these are ad-hoc, user-supplied reference layers,
 * each its own toggleable/removable entry. Persisted so an overlay survives a
 * reload until the user removes it.
 */
export const useImportedOverlaysStore = create<ImportedOverlaysState>()(
  persist(
    (set) => ({
      overlays: [],
      add: (overlay) => {
        const id = crypto.randomUUID()
        set((s) => ({
          overlays: [...s.overlays, { ...overlay, id, importedAt: Date.now(), visible: true }],
        }))
        return id
      },
      remove: (id) => set((s) => ({ overlays: s.overlays.filter((o) => o.id !== id) })),
      toggle: (id) =>
        set((s) => ({
          overlays: s.overlays.map((o) => (o.id === id ? { ...o, visible: !o.visible } : o)),
        })),
      rename: (id, name) =>
        set((s) => ({
          overlays: s.overlays.map((o) => (o.id === id ? { ...o, name } : o)),
        })),
    }),
    {
      name: "voltedge-imported-overlays",
      storage: safeStorage,
      // Session-only (ephemeral) overlays are never persisted -- they're the
      // ones too large for the quota (see ImportedOverlay.ephemeral).
      partialize: (s) => ({ overlays: s.overlays.filter((o) => !o.ephemeral) }),
    },
  ),
)
