import { create } from "zustand"

import type { FeatureRef } from "./selection-store"

const keyOf = (r: FeatureRef) => `${r.featureClassCode}:${r.featureId}`

/**
 * The "Select" tool: a panel-less, highlight-only multi-selection, distinct
 * from selection-store.ts (whose single `selected` opens the FeaturePanel).
 * While `active`, voltedge-map.tsx's click handler toggles the top feature
 * under the cursor into `selected` (single-click `pick`), and a DragBox lets
 * the user rubber-band a rectangle to add every feature inside it at once
 * (`pickMany`, additive). Either way it just draws a halo for the whole set --
 * no attribute panel, no other side effect. Nothing is ever created or written;
 * this is purely a visual "highlight these to look at them" pick.
 *
 * Session-only (an armed tool / a highlight set has no meaning across a
 * reload), same as box-zoom-store.ts / measure-store.ts. Turning the tool off
 * clears the set, so no orphan highlight is left behind that a normal (panel-
 * opening) click can't clear.
 */
interface MapSelectState {
  active: boolean
  selected: FeatureRef[]
  toggleActive: () => void
  deactivate: () => void
  // Adds the feature to the set, or removes it if already in (click-to-toggle).
  pick: (ref: FeatureRef) => void
  // Adds many features at once, unioning them into the set (a marquee/box
  // selection is ADDITIVE -- it never deselects things already picked, unlike
  // single-click `pick` which toggles). Duplicates (already in, or repeated
  // within refs) are collapsed by `code:id` key.
  pickMany: (refs: FeatureRef[]) => void
  // Empties the set but stays in select mode.
  clear: () => void
}

export const useMapSelectStore = create<MapSelectState>((set) => ({
  active: false,
  selected: [],
  toggleActive: () => set((s) => (s.active ? { active: false, selected: [] } : { active: true })),
  deactivate: () => set({ active: false, selected: [] }),
  pick: (ref) =>
    set((s) => {
      const key = keyOf(ref)
      const exists = s.selected.some((r) => keyOf(r) === key)
      return { selected: exists ? s.selected.filter((r) => keyOf(r) !== key) : [...s.selected, ref] }
    }),
  pickMany: (refs) =>
    set((s) => {
      const have = new Set(s.selected.map(keyOf))
      const added: FeatureRef[] = []
      for (const ref of refs) {
        const key = keyOf(ref)
        if (have.has(key)) continue
        have.add(key)
        added.push(ref)
      }
      return added.length > 0 ? { selected: [...s.selected, ...added] } : s
    }),
  clear: () => set({ selected: [] }),
}))
