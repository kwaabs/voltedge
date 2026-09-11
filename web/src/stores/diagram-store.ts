import { create } from "zustand"

import { useSelectionStore } from "@/stores/selection-store"

/**
 * Which diagram instance(s) are open in the map route's side-by-side
 * split view, and where -- two panes ("top"/"bottom") split the
 * diagram column evenly, each holding an ordered tab strip of open
 * diagrams. Not persisted, same reasoning as selection-store.ts.
 */
export type DiagramPane = "top" | "bottom"

interface DiagramState {
  panes: Record<DiagramPane, string[]>
  active: Record<DiagramPane, string | null>
  /** Full-page mode: the id of the single diagram shown at full width with
   * the geographic map hidden (kept mounted so its view survives), or null for
   * the normal side-by-side split. When 2+ diagrams are open, the others
   * collapse into a tab strip and this is the active tab. Cleared when the
   * maximized diagram closes (falls back to another open one, else null). */
  maximizedId: string | null
  /** Opens a diagram: if it's already open somewhere, just brings its
   * own tab to the front (no duplicate tabs); otherwise adds it as a
   * new tab in whichever pane currently has fewer tabs, so opening
   * diagrams one at a time naturally alternates top/bottom -- the
   * first ever opened lands in "top" (equal-empty ties favor top),
   * landing it in the single full-height pane a lone open diagram
   * renders as (see map.tsx's own "split only when both panes have a
   * tab" rule). A tie after that also favors top; which pane a given
   * diagram ends up in beyond the first two is never load-bearing
   * since `move` can always relocate it. */
  open: (diagramId: string) => void
  /** Closes one diagram's own tab, wherever it is. If it was the
   * active tab in its pane, the pane's own new active tab becomes
   * whichever tab is now last in that pane's list (arbitrary but
   * stable -- most-recently-opened-of-what's-left). A pane that drops
   * to zero tabs collapses map.tsx's split back to a single full-
   * height pane automatically, since that's driven by tab counts, not
   * a separate "is split" flag. */
  close: (diagramId: string) => void
  /** Switches which tab is active within one pane -- does not move
   * anything between panes. */
  activate: (pane: DiagramPane, diagramId: string) => void
  /** Relocates one diagram's own tab to the other pane and makes it
   * active there, keeping its origin pane's own active tab sane the
   * same way `close` does. The one way a tab actually "goes either
   * way (top or bottom)" once there are 3+ diagrams open across two
   * panes that can only hold one tab strip each. */
  move: (diagramId: string) => void
  /** Shows `diagramId` full-page (also makes it the active tab in its pane, so
   * restoring returns to a sane split). Doubles as the carousel tab switcher. */
  maximize: (diagramId: string) => void
  /** Leaves full-page mode, back to the side-by-side split. */
  restore: () => void
}

function paneOf(panes: Record<DiagramPane, string[]>, diagramId: string): DiagramPane | null {
  if (panes.top.includes(diagramId)) return "top"
  if (panes.bottom.includes(diagramId)) return "bottom"
  return null
}

export const useDiagramStore = create<DiagramState>((set, get) => ({
  panes: { top: [], bottom: [] },
  active: { top: null, bottom: null },
  maximizedId: null,

  open: (diagramId) => {
    // Opening (or creating -> create-diagram-form.tsx opens on success) a
    // diagram closes the feature/attribute panel: the two share the map's
    // right-hand space, and the diagram is now the focus. Clearing the
    // selection is what unmounts FeaturePanel (it renders null with no
    // `selected`). Covers every open path -- View diagram + the create
    // form -- so no call site has to remember to do it.
    useSelectionStore.getState().clear()

    const { panes, active } = get()
    const existing = paneOf(panes, diagramId)
    if (existing) {
      set({ active: { ...active, [existing]: diagramId } })
      return
    }
    const target: DiagramPane = panes.top.length <= panes.bottom.length ? "top" : "bottom"
    set({
      panes: { ...panes, [target]: [...panes[target], diagramId] },
      active: { ...active, [target]: diagramId },
    })
  },

  close: (diagramId) => {
    const { panes, active } = get()
    const pane = paneOf(panes, diagramId)
    if (!pane) return
    const remaining = panes[pane].filter((id) => id !== diagramId)
    const nextActive = active[pane] === diagramId ? (remaining.at(-1) ?? null) : active[pane]
    const nextPanes = { ...panes, [pane]: remaining }
    set({
      panes: nextPanes,
      active: { ...active, [pane]: nextActive },
      // If the full-page diagram is the one closing, fall back to another open
      // diagram (stay full-page, like closing a browser tab), else keep as-is.
      ...(get().maximizedId === diagramId
        ? { maximizedId: [...nextPanes.top, ...nextPanes.bottom][0] ?? null }
        : {}),
    })
  },

  activate: (pane, diagramId) => set((s) => ({ active: { ...s.active, [pane]: diagramId } })),

  move: (diagramId) => {
    const { panes, active } = get()
    const from = paneOf(panes, diagramId)
    if (!from) return
    const to: DiagramPane = from === "top" ? "bottom" : "top"
    const fromRemaining = panes[from].filter((id) => id !== diagramId)
    const fromNextActive = active[from] === diagramId ? (fromRemaining.at(-1) ?? null) : active[from]
    set({
      panes: { ...panes, [from]: fromRemaining, [to]: [...panes[to], diagramId] },
      active: { ...active, [from]: fromNextActive, [to]: diagramId },
    })
  },

  maximize: (diagramId) => {
    const pane = paneOf(get().panes, diagramId)
    if (!pane) return
    set((s) => ({ maximizedId: diagramId, active: { ...s.active, [pane]: diagramId } }))
  },

  restore: () => set({ maximizedId: null }),
}))
