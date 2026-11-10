import { create } from "zustand"

/** One attachment in a pole's fan-out. label is the display name (feature name,
 * else the class name). */
export interface SpiderAttachment {
  feature_class: string
  feature_id: string
  label: string
}

/** The currently fanned-out pole. lon/lat (EPSG:4326) anchor the fan on the map;
 * attachments are the geometry-less features mounted on the pole (all sharing its
 * point), splayed into a ring so each can be seen and picked individually. */
export interface PoleSpiderActive {
  poleClass: string
  poleId: string
  lon: number
  lat: number
  attachments: SpiderAttachment[]
}

/**
 * The "Show on map" fan-out ("spiderfy") for a pole's co-located attachments
 * (pole-attachments v2). The On-this-pole panel sets `active` with the pole's
 * coordinate + its attachments; voltedge-map.tsx anchors an ol/Overlay there and
 * renders the ring (PoleSpiderFan). Only one pole fans at a time -- opening a new
 * one (or closing) replaces it. Not persisted: it's a transient view aid, cleared
 * on close, on picking an attachment, or when the spidered pole is deselected.
 */
interface PoleSpiderState {
  active: PoleSpiderActive | null
  open: (active: PoleSpiderActive) => void
  close: () => void
}

export const usePoleSpiderStore = create<PoleSpiderState>((set) => ({
  active: null,
  open: (active) => set({ active }),
  close: () => set({ active: null }),
}))
