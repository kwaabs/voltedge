import type TileLayer from "ol/layer/Tile"
import OSM from "ol/source/OSM"
import type TileSource from "ol/source/Tile"
import XYZ from "ol/source/XYZ"

import type { BasemapId } from "@/stores/basemap-store"

/**
 * Builds the OpenLayers tile source for a basemap id, or `null` for the
 * blank/no-basemap choice (nothing to render). Attribution isn't set on
 * the source here -- voltedge-map.tsx disables OL's Attribution control
 * and status-bar.tsx renders each basemap's required attribution itself
 * (see basemap-store.ts's BASEMAP_OPTIONS), so a source-level attribution
 * would never be shown anyway.
 */
export function createBasemapSource(id: BasemapId): TileSource | null {
  switch (id) {
    case "osm":
      return new OSM()
    case "carto-voyager":
      // Carto's non-subdomain raster host (their subdomained URLs are
      // deprecated; HTTP/2 makes the single host fine). Voyager raster
      // tiles top out around z20 -- past that OL over-zooms the last
      // level rather than 404ing, matching how OSM behaves above its own
      // max under the view's maxZoom: 22.
      return new XYZ({
        url: "https://basemaps.cartocdn.com/rastertiles/voyager/{z}/{x}/{y}.png",
        maxZoom: 20,
      })
    case "blank":
      return null
  }
}

/**
 * Applies a basemap choice to the map's single base tile layer in place:
 * a real source swap for osm/carto (keeping the same layer, so it stays
 * at the bottom of the z-order under every feature layer), or hiding the
 * layer entirely for the blank choice rather than juggling a null source
 * through OL's types. Switching back off blank re-shows it with the new
 * source. Idempotent -- safe to call on every basemap-change render.
 */
export function applyBasemap(layer: TileLayer<TileSource>, id: BasemapId): void {
  const source = createBasemapSource(id)
  if (source) {
    layer.setSource(source)
    layer.setVisible(true)
  } else {
    layer.setVisible(false)
  }
}
