import { create } from "zustand"

import { apiFetch } from "@/lib/api"
import type { FeatureRef } from "@/stores/selection-store"

/**
 * The in-app AI assistant's proactive-suggestion state (Phase 19,
 * docs/phase-19-assistant.md). Slice 1 is deterministic and LLM-free: when the
 * user finishes reshaping a feature's geometry (typically snapping it to
 * another feature -- see feature-edit-form.tsx's Save), that form fires
 * `requestSuggestions`, which asks the assistant backend for the ranked,
 * rule-legal connections/relationships that feature could now establish. Each
 * comes back as a ready-to-execute action -- the establish route to call plus
 * the exact body -- so assistant-suggestions.tsx can render a confirmation chip
 * that, on click, just POSTs the prebuilt args to the same /connectivity or
 * /relationships route the manual connect-confirm-panel.tsx already uses.
 * Nothing writes until the user clicks; the assistant only proposes.
 *
 * Not persisted -- a suggestion has no meaning across a reload, same reasoning
 * as connect-store.ts / selection-store.ts. A fresh `requestSuggestions`
 * replaces the whole set (the previous feature's chips are stale the moment a
 * new feature is edited), and both `remove` (one acted-on/dismissed chip) and
 * `clear` (all of them) prune it.
 */
export interface SuggestionTargetFeature {
  feature_class: string
  feature_id: string
  name?: string
  subtype?: number
}

export interface SuggestionAction {
  kind: "connection" | "relationship" | "split"
  tool: "establish_connection" | "establish_relationship" | "split_line"
  title: string
  detail?: string
  target: SuggestionTargetFeature
  distance_meters?: number
  // For connection/relationship: the prebuilt establish request body, POSTed
  // verbatim to /connectivity or /relationships -- opaque here on purpose, the
  // frontend never reshapes it. For a `split` action it's NOT a ready-to-POST
  // body but the input the frontend hands to the manual-split create form
  // (assistant-suggestions.tsx opens draw-store.startSplitDraft from it):
  // { feature_class, feature_id, split_point: { lng, lat } } -- the line to cut
  // and where. The split commit needs the far half's own attributes, so it
  // routes through that form rather than a one-click POST.
  args: unknown
}

/**
 * A suggestion group is every action the backend proposed against one candidate
 * feature, collapsed into a single row (see api/internal/assistant/suggest.go's
 * suggestionGroup). The engines legitimately return several distinct actions per
 * target -- an electrical connection plus one or more relationship kinds -- so
 * the surface shows one row per nearby feature that expands to the handful of
 * things you can do with it, rather than a duplicate-looking chip per action.
 */
export interface SuggestionGroup {
  target: SuggestionTargetFeature
  label: string
  distance_meters?: number
  actions: SuggestionAction[]
}

interface SuggestResponse {
  suggestions: SuggestionGroup[]
}

interface AssistantState {
  source: FeatureRef | null
  suggestions: SuggestionGroup[]
  requestSuggestions: (source: FeatureRef) => Promise<void>
  // remove drops a whole group (the user dismissed that candidate feature);
  // removeAction drops one action within a group after it's been established,
  // pruning the group entirely once its last action is gone.
  remove: (groupIndex: number) => void
  removeAction: (groupIndex: number, actionIndex: number) => void
  clear: () => void
}

export const useAssistantStore = create<AssistantState>((set) => ({
  source: null,
  suggestions: [],
  requestSuggestions: async (source) => {
    try {
      const res = await apiFetch<SuggestResponse>("/assistant/suggestions", {
        method: "POST",
        body: JSON.stringify({ feature_class: source.featureClassCode, feature_id: source.featureId }),
      })
      set({ source, suggestions: res.suggestions })
    } catch {
      // Proactive help must never interrupt the user's own action. A failed
      // fetch just means no chips this time -- swallow it (no toast) and drop
      // any stale suggestions from a previously edited feature. A user who
      // lacks the commit permissions isn't an error at all: the endpoint
      // answers 200 with an empty list, so they simply see nothing here.
      set({ source: null, suggestions: [] })
    }
  },
  remove: (groupIndex) =>
    set((state) => ({ suggestions: state.suggestions.filter((_, i) => i !== groupIndex) })),
  removeAction: (groupIndex, actionIndex) =>
    set((state) => ({
      suggestions: state.suggestions
        .map((group, i) =>
          i === groupIndex
            ? { ...group, actions: group.actions.filter((_, j) => j !== actionIndex) }
            : group,
        )
        // A group with no actions left has nothing to offer -- drop it so the
        // panel count stays honest and empty rows never render.
        .filter((group) => group.actions.length > 0),
    })),
  clear: () => set({ source: null, suggestions: [] }),
}))
