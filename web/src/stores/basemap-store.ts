import { create } from "zustand"
import { persist } from "zustand/middleware"

/**
 * The client-side "no basemap" choice. It is deliberately NOT a server record
 * (it has no tile source); the picker appends it as a built-in option and the
 * map hides its base tile layer when it's selected. Kept as a stable sentinel
 * code so it can be persisted and compared like any real basemap's code.
 */
export const BLANK_BASEMAP = "blank"

/**
 * The default a first visit opens on, matching what the map has always shown.
 * It is the `code` of the OSM row migration 0077 seeds; if an admin later
 * disables or renames it, the map falls back to the first enabled basemap
 * (resolved in voltedge-map.tsx), so this constant never needs to change.
 */
const DEFAULT_BASEMAP = "osm"

interface BasemapState {
  /** The `code` of the user's chosen basemap (a server record's code, or
   * BLANK_BASEMAP). Resolving it to an actual tile source is a side effect kept
   * out of the store and done in voltedge-map.tsx against the fetched list,
   * exactly the split theme-store.ts uses for the theme. */
  basemap: string
  setBasemap: (basemap: string) => void
}

/**
 * Just the user's chosen basemap code -- persisted so the choice survives a
 * reload. Since the set of basemaps now lives on the server (see
 * hooks/use-basemaps.ts), this store no longer carries the catalog; it only
 * remembers which one is selected.
 */
export const useBasemapStore = create<BasemapState>()(
  persist(
    (set) => ({
      basemap: DEFAULT_BASEMAP,
      setBasemap: (basemap) => set({ basemap }),
    }),
    { name: "voltedge-basemap" },
  ),
)
