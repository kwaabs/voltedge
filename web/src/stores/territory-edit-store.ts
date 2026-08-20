import { create } from "zustand"

/** A GeoJSON MultiPolygon (or bare Polygon) geometry object -- what the
 * boundary authoring pipeline produces and PATCH /territories/{id}
 * accepts (the server's ST_Multi coerces a Polygon into the
 * MultiPolygon the column stores). Coordinates are left loosely typed;
 * ol/format/GeoJSON produces and consumes them, nothing here inspects
 * their shape. */
export interface TerritoryGeometry {
  type: "Polygon" | "MultiPolygon"
  coordinates: unknown
}

/** Which map interaction, if any, is attached while authoring a
 * boundary. "draw" is a Draw interaction (each completed polygon is
 * appended to the territory's MultiPolygon); "vertices" is a Modify
 * interaction over the existing rings (drag/insert/delete a vertex).
 * Only ever one at a time -- both listen for the same drag gestures and
 * would fight, exactly like edit-store.ts's GeometryTool. */
export type TerritoryEditTool = "draw" | "vertices" | null

interface EditingTerritory {
  id: string
  name: string
}

/**
 * In-map boundary authoring (Phase 11 Slice I). Not persisted -- a
 * half-drawn boundary has no meaning across a reload. Started from the
 * admin tree ("Edit boundary" row action) or the read-only territory
 * panel; both just set `editing` and let the map take over.
 *
 * `dirtyGeometry`/`cleared` are the map->toolbar signal, same
 * cross-sibling-component pattern as edit-store.ts: voltedge-map.tsx's
 * Draw/Modify interactions produce the geometry, and
 * territory-edit-toolbar.tsx's Save reads it to PATCH. A distinct
 * `cleared` flag separates "boundary removed" (send clear_geom) from
 * "boundary untouched" (`dirtyGeometry` null, nothing to send) --
 * clearing a boundary and never having edited one both leave
 * `dirtyGeometry` null, but only the former should write.
 */
interface TerritoryEditState {
  editing: EditingTerritory | null
  tool: TerritoryEditTool
  dirtyGeometry: TerritoryGeometry | null
  cleared: boolean
  start: (id: string, name: string) => void
  setTool: (tool: TerritoryEditTool) => void
  setDirtyGeometry: (geometry: TerritoryGeometry) => void
  clearGeometry: () => void
  stop: () => void
}

export const useTerritoryEditStore = create<TerritoryEditState>((set) => ({
  editing: null,
  tool: null,
  dirtyGeometry: null,
  cleared: false,
  start: (id, name) => set({ editing: { id, name }, tool: null, dirtyGeometry: null, cleared: false }),
  setTool: (tool) => set({ tool }),
  // Drawing/modifying after a clear resurrects the boundary, so a new
  // geometry always cancels the cleared flag.
  setDirtyGeometry: (geometry) => set({ dirtyGeometry: geometry, cleared: false }),
  clearGeometry: () => set({ dirtyGeometry: null, cleared: true, tool: null }),
  stop: () => set({ editing: null, tool: null, dirtyGeometry: null, cleared: false }),
}))
