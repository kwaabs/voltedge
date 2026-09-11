import type TileLayer from "ol/layer/Tile"
import type TileSource from "ol/source/Tile"
import XYZ from "ol/source/XYZ"

import type { BasemapRecord } from "@/hooks/use-basemaps"
import { BLANK_BASEMAP } from "@/stores/basemap-store"

/**
 * Resolves a chosen basemap `code` against the fetched catalog to the record
 * whose tile source should render, or `null` for "no tiles" (the blank choice,
 * or an empty catalog). An unknown code -- e.g. a persisted choice an admin has
 * since deleted -- falls back to the first enabled basemap rather than going
 * blank, so a stale localStorage value never leaves the map empty.
 */
export function resolveBasemap(code: string, list: BasemapRecord[]): BasemapRecord | null {
  if (code === BLANK_BASEMAP) return null
  return list.find((b) => b.code === code) ?? list[0] ?? null
}

/**
 * Builds the OpenLayers tile source for a basemap record, or `null` for the
 * blank/no-basemap choice (nothing to render). Generic XYZ over the record's
 * url_template -- OL substitutes whichever {x}/{y}/{z} placeholders it carries,
 * so provider tile-URL orders (Esri's {z}/{y}/{x}, cartocdn/OSM's {z}/{x}/{y})
 * just work. Attribution isn't set on the source -- voltedge-map.tsx disables
 * OL's Attribution control and status-bar.tsx renders the record's required
 * attribution itself, so a source-level attribution would never be shown.
 */
export function createBasemapSource(record: BasemapRecord | null): TileSource | null {
  if (!record) return null
  // TMS servers number tile rows from the bottom; OpenLayers reads the {-y}
  // placeholder as that flipped row, so rewrite {y} when the record is TMS.
  const url = record.tms ? record.url_template.replaceAll("{y}", "{-y}") : record.url_template
  return new XYZ({
    url,
    // null max_zoom = no provider cap; OL over-zooms the last level (the view's
    // own maxZoom still bounds it) rather than requesting tiles that 404.
    maxZoom: record.max_zoom ?? undefined,
    minZoom: record.min_zoom || undefined,
  })
}

/**
 * Applies a basemap choice to the map's single base tile layer in place: a real
 * source swap for a tiled record (keeping the same layer, so it stays at the
 * bottom of the z-order under every feature layer), or hiding the layer for the
 * blank choice rather than juggling a null source through OL's types. Switching
 * back off blank re-shows it with the new source. Idempotent -- safe to call on
 * every basemap-change render.
 */
export function applyBasemap(layer: TileLayer<TileSource>, record: BasemapRecord | null): void {
  const source = createBasemapSource(record)
  if (source) {
    layer.setSource(source)
    layer.setVisible(true)
  } else {
    layer.setVisible(false)
  }
}
