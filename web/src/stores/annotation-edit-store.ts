import { create } from "zustand"

import type { AnnotationKind } from "@/lib/map-layers"

/** The in-map gesture the editor has armed (Slice 4b). `move` attaches an OL
 * Translate (drag the whole annotation); `vertices` attaches a Modify
 * (drag/insert/delete vertices -- box/line/arrow only, a text Point has none);
 * `rotate` attaches a drag-to-spin gesture (text notes only -- their rotation is
 * a style angle, not geometry). null = no gesture, just text/style editing. */
export type AnnotationGeometryTool = "move" | "vertices" | "rotate" | null

interface AnnotationEditState {
  /** The kind of annotation currently being drawn, or null when no draw
   * session is active. Set by new-annotation-menu.tsx when the user picks a
   * kind; voltedge-map.tsx keys its OL Draw interaction off it (text->Point,
   * box->Polygon, line/arrow->LineString). Cleared on drawend (the map creates
   * the annotation and selects it) or an explicit Cancel/Escape. */
  drawingKind: AnnotationKind | null
  /** The id of the existing annotation selected for editing, or null. Set by a
   * map click that hits the annotation layer (voltedge-map.tsx) or by the map
   * itself right after a fresh draw; drives annotation-edit-panel.tsx. */
  selectedId: string | null
  /** The armed in-map geometry gesture for the selected annotation (Slice 4b),
   * or null. voltedge-map.tsx attaches the matching OL interaction. */
  tool: AnnotationGeometryTool
  /** The reshaped/moved geometry staged by an in-map gesture, as EPSG:4326
   * GeoJSON, or null when geometry is untouched. Staged rather than PATCHed per
   * drag so a background refetch can't clobber the feature mid-edit; the panel's
   * Save sends it. */
  dirtyGeometry: unknown | null
  /** The angle (degrees clockwise) staged by the in-map rotate gesture, or null
   * when untouched. A text note's rotation is a style value, not geometry, so it
   * rides here rather than in dirtyGeometry; the panel folds it into the style
   * it PATCHes on Save. */
  dirtyRotation: number | null

  /** Arm a draw session for `kind`. Drawing and selecting are mutually
   * exclusive workflows, so this also clears any current selection. */
  startDrawing: (kind: AnnotationKind) => void
  /** End the draw session without drawing (Cancel button / Escape). */
  cancelDrawing: () => void
  /** Select an existing annotation for editing. Cancels any draw session and
   * resets the geometry-edit state (a fresh selection starts untouched). */
  select: (id: string) => void
  /** Close the edit panel and drop any staged geometry edit. */
  deselect: () => void
  /** Arm/disarm an in-map gesture. Toggling the active one off (null) leaves any
   * already-staged geometry/rotation intact for Save. */
  setTool: (tool: AnnotationGeometryTool) => void
  /** Stage the geometry an in-map gesture produced (its GeoJSON). */
  setDirtyGeometry: (geometry: unknown) => void
  /** Stage the angle (degrees) the in-map rotate gesture produced. */
  setDirtyRotation: (degrees: number) => void
  /** Clear the staged geometry/rotation edits (e.g. after a successful Save). */
  clearGeometry: () => void
}

/**
 * The map-annotation authoring session (Phase 36 Slice 4) -- which kind is
 * being drawn and which existing annotation is open for editing. The redline
 * sibling of draw-store.ts + edit-store.ts, but far thinner: an annotation has
 * no attribute form (only free-form text + a style blob), so a draw creates the
 * row immediately and hands it straight to the edit panel, rather than staging
 * a draft through a create form. Both fields are transient session state, so
 * this store is not persisted.
 */
export const useAnnotationEditStore = create<AnnotationEditState>((set) => ({
  drawingKind: null,
  selectedId: null,
  tool: null,
  dirtyGeometry: null,
  dirtyRotation: null,
  startDrawing: (kind) =>
    set({ drawingKind: kind, selectedId: null, tool: null, dirtyGeometry: null, dirtyRotation: null }),
  cancelDrawing: () => set({ drawingKind: null }),
  select: (id) =>
    set({ selectedId: id, drawingKind: null, tool: null, dirtyGeometry: null, dirtyRotation: null }),
  deselect: () => set({ selectedId: null, tool: null, dirtyGeometry: null, dirtyRotation: null }),
  setTool: (tool) => set({ tool }),
  setDirtyGeometry: (geometry) => set({ dirtyGeometry: geometry }),
  setDirtyRotation: (degrees) => set({ dirtyRotation: degrees }),
  clearGeometry: () => set({ tool: null, dirtyGeometry: null, dirtyRotation: null }),
}))
