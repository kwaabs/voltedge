import { create } from "zustand"

import type { FeatureGeometry } from "@/hooks/use-feature"

/** One matched feature, as the map highlight layer and the results table
 * both need it. Mirrors the crud query endpoint's row envelope
 * (docs/phase-13-query-builder.md, formatRow) -- id/name/status at the top
 * level, geometry as a GeoJSON object, everything else under attributes. */
export interface QueryResultRow {
  id: string
  name: string | null
  status: string | null
  geometry?: FeatureGeometry
  attributes: Record<string, unknown>
  // The feature's primary (printable) asset-tag code, present only when the
  // query carried a tag filter (the engine surfaces it so a code search shows
  // which record a scan lands on). Absent otherwise, and absent for a feature
  // with no active primary tag.
  primary_tag_code?: string
}

/**
 * Not persisted -- a query result and which feature class it came from have
 * no meaning across a reload, same as trace-store.ts and every other
 * ephemeral map-workflow store. Three siblings read it: feature-query-panel.tsx
 * owns the builder form and writes results here on Run; voltedge-map.tsx reads
 * `results`/`resultClass` to highlight every match at once (the geometry is
 * already in each row, so no re-fetch); and the panel's own results table reads
 * the same rows to render, each with a per-row zoom button.
 *
 * `open` is the only piece the map toolbar toggles (map.tsx's search button);
 * the FloatingPanel owns its own position/size, so this store deliberately
 * doesn't -- a window's geometry is pure view state with no reason to outlive a
 * remount.
 */
/** A one-shot instruction to seed the builder when it next opens: focus a
 * feature class and narrow to a territory (aggregate mode, so the territory
 * scope applies -- filter mode has no spatial scope). Set by the Territory
 * Explorer's per-class "view" button; consumed and cleared by the panel on
 * open. Not a persisted preference -- it's a single hand-off, like a
 * navigation param. */
export interface QueryPreset {
  classCode: string
  territoryId: string
}

interface QueryState {
  open: boolean
  resultClass: string | null
  results: QueryResultRow[] | null
  preset: QueryPreset | null
  openPanel: () => void
  /** Open the panel seeded to a class + territory scope (Territory Explorer). */
  openWithPreset: (preset: QueryPreset) => void
  /** Consume the preset once the panel has applied it. */
  clearPreset: () => void
  closePanel: () => void
  toggle: () => void
  setResults: (resultClass: string, results: QueryResultRow[]) => void
  clearResults: () => void
}

export const useQueryStore = create<QueryState>((set) => ({
  open: false,
  resultClass: null,
  results: null,
  preset: null,
  openPanel: () => set({ open: true }),
  openWithPreset: (preset) => set({ open: true, preset }),
  clearPreset: () => set({ preset: null }),
  // Closing the panel also drops the highlighted result set -- the matches
  // are the panel's output, so leaving them lit on the map after it's gone
  // would strand a highlight the user can no longer clear from anywhere. A
  // pending preset is dropped too, so it can't leak into a later manual open.
  closePanel: () => set({ open: false, results: null, resultClass: null, preset: null }),
  toggle: () =>
    set((s) => (s.open ? { open: false, results: null, resultClass: null, preset: null } : { open: true })),
  setResults: (resultClass, results) => set({ resultClass, results }),
  clearResults: () => set({ results: null, resultClass: null }),
}))
