import { create } from "zustand"

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
}

function paneOf(panes: Record<DiagramPane, string[]>, diagramId: string): DiagramPane | null {
  if (panes.top.includes(diagramId)) return "top"
  if (panes.bottom.includes(diagramId)) return "bottom"
  return null
}

export const useDiagramStore = create<DiagramState>((set, get) => ({
  panes: { top: [], bottom: [] },
  active: { top: null, bottom: null },

  open: (diagramId) => {
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
    set({ panes: { ...panes, [pane]: remaining }, active: { ...active, [pane]: nextActive } })
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
}))
