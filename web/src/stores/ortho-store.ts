import { create } from "zustand"

/**
 * Right-angle (orthogonal) drawing toggle for the new-feature draw
 * banner (new-feature-menu.tsx), read live by voltedge-map.tsx's Draw
 * geometryFunction. When on, each in-progress segment snaps to the
 * nearest 45° direction off the previous vertex -- the clean along-the-
 * street runs a utility network wants, rather than free-angle segments.
 * Only meaningful for line/polygon draws (a point has no segment to
 * constrain), so the toggle button hides for point classes. Off by
 * default and session-only, same as snap-store.ts/draw-store.ts: a
 * transient digitizing mode, not a durable preference.
 */
interface OrthoState {
  enabled: boolean
  toggle: () => void
}

export const useOrthoStore = create<OrthoState>((set) => ({
  enabled: false,
  toggle: () => set((s) => ({ enabled: !s.enabled })),
}))
