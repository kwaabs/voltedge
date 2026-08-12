import { create } from "zustand"

/**
 * Not persisted -- a monotonic counter, not real data. voltedge-map.tsx
 * doesn't know about creates/edits/deletes; a save handler anywhere
 * (feature-create-form.tsx today, an edit form later) just bumps this,
 * and a dedicated effect in voltedge-map.tsx reacts by calling
 * `.refresh()` on every vector tile source. Deliberately not folded
 * into refetching the catalog query -- the catalog (which sources
 * exist) doesn't change when a feature is added, so nothing there would
 * signal "the tiles themselves are stale now."
 */
interface MapRefreshState {
  version: number
  bump: () => void
}

export const useMapRefreshStore = create<MapRefreshState>((set) => ({
  version: 0,
  bump: () => set((s) => ({ version: s.version + 1 })),
}))
