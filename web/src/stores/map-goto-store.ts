import { create } from "zustand"

/**
 * A one-shot "pan the map here" command, decoupled from the map instance the
 * same way selection-store.ts's focusToken is: the status bar's go-to search
 * writes a target + bumps a token, and voltedge-map.tsx watches the token and
 * animates its OL view. A monotonic token (not just the target) is what makes
 * it fire every time -- re-entering the *same* coordinates still jumps, which a
 * value-equality effect would swallow.
 */
export interface GoToTarget {
  lng: number
  lat: number
  /** Optional target zoom; when omitted the map keeps (or gently raises) its
   * current zoom so a recenter doesn't yank the scale. */
  zoom?: number
}

interface MapGotoState {
  target: GoToTarget | null
  token: number
  goTo: (target: GoToTarget) => void
}

export const useMapGotoStore = create<MapGotoState>((set) => ({
  target: null,
  token: 0,
  goTo: (target) => set((s) => ({ target, token: s.token + 1 })),
}))
