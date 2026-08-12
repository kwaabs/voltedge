import TileLayer from "ol/layer/Tile"
import VectorTileLayer from "ol/layer/VectorTile"
import Map from "ol/Map"
import { fromLonLat } from "ol/proj"
import OSM from "ol/source/OSM"
import VectorTileSource from "ol/source/VectorTile"
import View from "ol/View"
import MVT from "ol/format/MVT"
import { useEffect, useRef } from "react"

import { useCatalog } from "@/hooks/use-catalog"
import { styleForFeature } from "@/lib/map-layers"
import { tileUrlTemplate } from "@/lib/martin"

import "ol/ol.css"

/**
 * Placeholder default view -- centered on the seed data used to
 * validate this slice (seed/pole_example.sql, seed/connectivity_example.sql),
 * not a real service-territory extent. Nothing in the metadata model
 * defines a default extent yet; revisit once/if that becomes a real
 * concept (e.g. derived from compiled feature classes' actual bounds).
 */
const DEFAULT_CENTER = fromLonLat([-0.127, 51.507])
const DEFAULT_ZOOM = 15

export function VoltEdgeMap() {
  const containerRef = useRef<HTMLDivElement>(null)
  const mapRef = useRef<Map | null>(null)
  const vectorLayersRef = useRef<VectorTileLayer[]>([])
  const { data: catalog } = useCatalog()

  // Base map + OL Map instance -- created once.
  useEffect(() => {
    if (!containerRef.current) return

    const map = new Map({
      target: containerRef.current,
      layers: [new TileLayer({ source: new OSM() })],
      view: new View({ center: DEFAULT_CENTER, zoom: DEFAULT_ZOOM, maxZoom: 22 }),
    })
    mapRef.current = map

    return () => {
      map.setTarget(undefined)
      mapRef.current = null
    }
  }, [])

  // One VectorTileLayer per Martin catalog entry -- added/replaced
  // whenever the catalog changes, independent of the base map's own
  // lifecycle above.
  useEffect(() => {
    const map = mapRef.current
    if (!map || !catalog) return

    for (const layer of vectorLayersRef.current) map.removeLayer(layer)
    vectorLayersRef.current = []

    for (const sourceId of Object.keys(catalog.tiles)) {
      const layer = new VectorTileLayer({
        source: new VectorTileSource({
          format: new MVT(),
          url: tileUrlTemplate(sourceId),
        }),
        style: styleForFeature,
        properties: { sourceId },
      })
      map.addLayer(layer)
      vectorLayersRef.current.push(layer)
    }
  }, [catalog])

  return <div ref={containerRef} className="h-full w-full" />
}
