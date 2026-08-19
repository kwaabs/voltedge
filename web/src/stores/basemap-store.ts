import { create } from "zustand"
import { persist } from "zustand/middleware"

export type BasemapId = "osm" | "carto-voyager" | "blank"

export interface AttributionLink {
  label: string
  href: string
}

export interface BasemapOption {
  id: BasemapId
  label: string
  /** Required attribution for this basemap, rendered by the status bar
   * (OpenLayers' own Attribution control is disabled in voltedge-map.tsx,
   * so this is the only place attribution ever shows). Empty for the
   * blank/no-basemap choice, which has nothing to attribute. */
  attribution: AttributionLink[]
}

const OSM_ATTRIBUTION: AttributionLink = {
  label: "© OpenStreetMap contributors",
  href: "https://www.openstreetmap.org/copyright",
}

/** The single source of truth for which basemaps exist -- both the
 * picker (status-bar.tsx) and the source factory (lib/basemaps.ts) read
 * their ids from here, so adding a basemap is a one-place change plus a
 * new `case` in createBasemapSource. Deliberately no OpenLayers import
 * in this module: it's pure metadata the OL-free status bar can consume
 * without dragging tile-source classes into its bundle. */
export const BASEMAP_OPTIONS: BasemapOption[] = [
  { id: "osm", label: "OpenStreetMap", attribution: [OSM_ATTRIBUTION] },
  {
    id: "carto-voyager",
    label: "Carto Voyager",
    attribution: [OSM_ATTRIBUTION, { label: "© CARTO", href: "https://carto.com/attributions" }],
  },
  { id: "blank", label: "Blank (no basemap)", attribution: [] },
]

export function basemapOption(id: BasemapId): BasemapOption {
  return BASEMAP_OPTIONS.find((o) => o.id === id) ?? BASEMAP_OPTIONS[0]
}

interface BasemapState {
  basemap: BasemapId
  setBasemap: (basemap: BasemapId) => void
}

/**
 * Just the user's chosen basemap id -- swapping the actual OL tile
 * source on the map instance is a side effect kept out of the store and
 * done in voltedge-map.tsx (via lib/basemaps.ts's createBasemapSource),
 * exactly the split theme-store.ts uses for the theme. Persisted so the
 * choice survives a reload; `osm` is the default for a first visit,
 * matching what the map has always shown.
 */
export const useBasemapStore = create<BasemapState>()(
  persist(
    (set) => ({
      basemap: "osm",
      setBasemap: (basemap) => set({ basemap }),
    }),
    { name: "voltedge-basemap" },
  ),
)
