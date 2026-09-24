import { create } from "zustand"
import { persist } from "zustand/middleware"

/**
 * Per-layer visibility and opacity for the external service-layer overlays
 * (WMS/WMTS/XYZ/Esri), keyed by the layer's catalog id. The catalog itself
 * (labels, urls, auth, default opacity) lives server-side and is fetched by
 * use-service-layers.ts; this store holds only the viewer's local, persisted
 * choices about which overlays are showing and how strong.
 *
 * Both maps store exceptions only:
 *  - `visible` absent  -> hidden. Service layers are opt-in overlays (unlike
 *    feature classes, which default on), so the default is off until the user
 *    turns one on in the layers panel.
 *  - `opacity` absent  -> fall back to the layer's own default_opacity from the
 *    catalog. Only a user-dragged value is stored here, so an admin re-tuning a
 *    layer's default still takes effect for anyone who never touched the slider.
 *
 * Persisted (localStorage) like layers-store / fault-overlay-store so a chosen
 * overlay set survives a reload. Keyed by catalog id, so a deleted-and-recreated
 * layer (new id) correctly starts from defaults.
 */
interface ServiceLayersState {
  visible: Record<string, boolean>
  opacity: Record<string, number>
  /** Flip one layer's visibility (default-off, so first toggle turns it on). */
  toggle: (id: string) => void
  /** Set one layer's visibility explicitly (used by the panel checkbox). */
  setVisible: (id: string, visible: boolean) => void
  /** Store a user-chosen opacity (0..1) for one layer. */
  setOpacity: (id: string, opacity: number) => void
}

export const useServiceLayersStore = create<ServiceLayersState>()(
  persist(
    (set) => ({
      visible: {},
      opacity: {},
      toggle: (id) => set((s) => ({ visible: { ...s.visible, [id]: !s.visible[id] } })),
      setVisible: (id, visible) => set((s) => ({ visible: { ...s.visible, [id]: visible } })),
      setOpacity: (id, opacity) =>
        set((s) => ({ opacity: { ...s.opacity, [id]: Math.min(1, Math.max(0, opacity)) } })),
    }),
    { name: "voltedge-service-layers" },
  ),
)
