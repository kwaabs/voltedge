import { create } from "zustand"

import type { FeatureRef } from "@/stores/selection-store"

export type ConnectMode = "connectivity" | "relationship"

/**
 * Not persisted -- an in-progress connect/relate workflow has no
 * meaning across a reload. `source` is locked in the moment the
 * workflow starts (the feature that was selected when "+ New
 * connection"/"+ New relationship" was clicked -- see
 * feature-panel.tsx); `target` is set once the user clicks a second
 * feature on the map (see voltedge-map.tsx's click handler). Once both
 * are set, connect-confirm-panel.tsx renders the floating confirm
 * panel -- the map click handler and the panel are siblings reading the
 * same store, same pattern as draw-store.ts / selection-store.ts
 * throughout this app.
 */
interface ConnectState {
  mode: ConnectMode | null
  source: FeatureRef | null
  target: FeatureRef | null
  start: (mode: ConnectMode, source: FeatureRef) => void
  pickTarget: (target: FeatureRef) => void
  cancel: () => void
}

export const useConnectStore = create<ConnectState>((set) => ({
  mode: null,
  source: null,
  target: null,
  start: (mode, source) => set({ mode, source, target: null }),
  pickTarget: (target) => set({ target }),
  cancel: () => set({ mode: null, source: null, target: null }),
}))
