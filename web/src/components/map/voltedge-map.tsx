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
import { useLayersStore } from "@/stores/layers-store"

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
  const hiddenLayers = useLayersStore((s) => s.hidden)
  // Lets the layer-creation effect below read the *current* hidden set
  // without depending on it -- depending on it directly would rebuild
  // every VectorTileLayer (and re-request every tile) on each single
  // checkbox toggle, when only that one layer's visible flag actually
  // needs to change (the second effect below already does that in
  // place). Kept in sync on every render, not inside an effect --
  // assigning a ref during render is fine, it's only reads during
  // render that render fresh data is not safe for.
  const hiddenLayersRef = useRef(hiddenLayers)
  hiddenLayersRef.current = hiddenLayers

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
        visible: !hiddenLayersRef.current[sourceId],
        properties: { sourceId },
      })
      map.addLayer(layer)
      vectorLayersRef.current.push(layer)
    }
  }, [catalog])

  // Reacts to the layers store -- src/components/map/layers-panel.tsx
  // -- toggling an existing layer's visibility in place, independent of
  // the catalog-driven effect above that only runs when the set of
  // layers itself changes.
  useEffect(() => {
    for (const layer of vectorLayersRef.current) {
      const sourceId = layer.get("sourceId") as string
      layer.setVisible(!hiddenLayers[sourceId])
    }
  }, [hiddenLayers])

  return <div ref={containerRef} className="h-full w-full" />
}
