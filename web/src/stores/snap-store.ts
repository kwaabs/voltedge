import { create } from "zustand"

/**
 * One toggle, shared by both places snapping applies: drawing a brand
 * new feature (new-feature-menu.tsx's draw banner) and reshaping an
 * existing one (geometry-edit-toolbar.tsx) -- both read/write the same
 * store rather than each keeping its own flag, so turning snapping off
 * from either toolbar turns it off everywhere, matching the mental
 * model of one setting, not a per-workflow one. Defaults to on: snapping
 * to an existing feature is the more common intent while digitizing a
 * utility network (that's the whole point of connectivity), so off is
 * the exception a user opts into, not the default they'd have to find
 * first. Not persisted, same as draw-store.ts/edit-store.ts -- a session
 * preference, not a durable one.
 */
interface SnapState {
  enabled: boolean
  toggle: () => void
}

export const useSnapStore = create<SnapState>((set) => ({
  enabled: true,
  toggle: () => set((s) => ({ enabled: !s.enabled })),
}))
