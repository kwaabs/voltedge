import { useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { ChevronDown, ChevronRight, Loader2, Plus, Sparkles, X } from "lucide-react"

import { Button } from "@/components/ui/button"
import { ApiError, apiFetch } from "@/lib/api"
import {
  type SuggestionAction,
  type SuggestionGroup,
  type SuggestionTargetFeature,
  useAssistantStore,
} from "@/stores/assistant-store"
import { useSelectionStore } from "@/stores/selection-store"
import { useToastStore } from "@/stores/toast-store"

/**
 * The in-app assistant's proactive-suggestion surface (Phase 19 Slice 1,
 * docs/phase-19-assistant.md): a small, compact, collapsible panel of the
 * connections/relationships the assistant backend proposed after the user
 * reshaped a feature (assistant-store.ts's `requestSuggestions`, fired from
 * feature-edit-form.tsx). Self-contained -- reads the store and renders null
 * when there's nothing to suggest, same pattern as connect-confirm-panel.tsx,
 * so map.tsx just drops it in as a sibling.
 *
 * The backend collapses every action against one candidate feature into a
 * single group (assistant/suggest.go's suggestionGroup), so the panel shows one
 * row per nearby feature that expands to the handful of things you can do with
 * it -- rather than the old wall of one tall card per action, which buried the
 * map. The panel is height-bounded and scrolls; its header collapses the whole
 * thing.
 *
 * Establishing an action doesn't hand anything to an LLM -- it POSTs the
 * backend-prebuilt `args` verbatim to the very establish route the manual
 * workflow uses (POST /connectivity or /relationships), so the write path, its
 * RBAC commit gate, and its actor attribution are exactly the manual ones. The
 * row only exists to skip the manual source/target/terminal picking; the human's
 * click is still the commit.
 */
export function AssistantSuggestions() {
  const source = useAssistantStore((s) => s.source)
  const suggestions = useAssistantStore((s) => s.suggestions)
  const remove = useAssistantStore((s) => s.remove)
  const removeAction = useAssistantStore((s) => s.removeAction)
  const clear = useAssistantStore((s) => s.clear)
  const queryClient = useQueryClient()
  // busyKey is `${groupIndex}:${actionIndex}` for the action mid-establish, so a
  // single spinner shows on the right row while every Establish is disabled.
  const [busyKey, setBusyKey] = useState<string | null>(null)
  const [collapsed, setCollapsed] = useState(false)
  const [expanded, setExpanded] = useState<Set<number>>(new Set())

  if (!source || suggestions.length === 0) return null

  // Highlight the candidate feature a suggestion is about: select it with
  // `focus` so the map draws the selection halo on it and pans to it if it's
  // off-screen -- the same thing feature-panel.tsx does for a connection/
  // relationship counterpart row. Purely a "show me which feature this is"
  // affordance; it doesn't establish anything and leaves the suggestion panel
  // up (its visibility keys off the assistant store's source, not selection).
  function highlight(target: SuggestionTargetFeature) {
    useSelectionStore.getState().select(target.feature_class, target.feature_id, { focus: true })
  }

  function toggleExpanded(groupIndex: number) {
    setExpanded((prev) => {
      const next = new Set(prev)
      if (next.has(groupIndex)) next.delete(groupIndex)
      else next.add(groupIndex)
      return next
    })
  }

  async function establish(action: SuggestionAction, groupIndex: number, actionIndex: number) {
    if (!source) return
    setBusyKey(`${groupIndex}:${actionIndex}`)
    try {
      const path = action.kind === "connection" ? "/connectivity" : "/relationships"
      // Both establish routes return JSON; only a relationship carries an
      // optional plausibility_warning (a soft geometric sanity check that
      // never blocks the write -- see api/internal/relationship/plausibility.go),
      // surfaced as a warning toast exactly like connect-confirm-panel.tsx does.
      const result = await apiFetch<{ plausibility_warning?: string }>(path, {
        method: "POST",
        body: JSON.stringify(action.args),
      })

      const keyRoot = action.kind === "connection" ? "connections" : "relationships"
      queryClient.invalidateQueries({ queryKey: [keyRoot, source.featureClassCode, source.featureId] })
      queryClient.invalidateQueries({ queryKey: [keyRoot, action.target.feature_class, action.target.feature_id] })

      if (action.kind === "relationship" && result.plausibility_warning) {
        useToastStore.getState().pushWarning(result.plausibility_warning)
      }
      useToastStore
        .getState()
        .pushSuccess(action.kind === "connection" ? "Connection established." : "Relationship established.")
      removeAction(groupIndex, actionIndex)
    } catch (err) {
      const fallback =
        action.kind === "connection" ? "Could not establish this connection." : "Could not establish this relationship."
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : fallback)
    } finally {
      setBusyKey(null)
    }
  }

  return (
    <div className="bg-popover text-popover-foreground fixed bottom-10 left-4 z-40 flex w-72 flex-col overflow-hidden rounded-md border shadow-lg">
      <div className="text-muted-foreground flex items-center justify-between gap-1 border-b px-2 py-1.5 text-xs font-medium">
        <button
          onClick={() => setCollapsed((c) => !c)}
          className="hover:text-foreground flex flex-1 items-center gap-1.5"
          title={collapsed ? "Expand suggestions" : "Collapse suggestions"}
        >
          {collapsed ? <ChevronRight className="size-3.5" /> : <ChevronDown className="size-3.5" />}
          <Sparkles className="size-3.5" />
          <span>
            Suggestions ({suggestions.length})
          </span>
        </button>
        <button onClick={clear} className="hover:text-foreground" title="Dismiss all">
          <X className="size-3.5" />
          <span className="sr-only">Dismiss all suggestions</span>
        </button>
      </div>

      {!collapsed && (
        <div className="max-h-64 divide-y overflow-y-auto">
          {suggestions.map((group, groupIndex) => (
            <SuggestionRow
              key={`${group.target.feature_class}-${group.target.feature_id}`}
              group={group}
              groupIndex={groupIndex}
              expanded={expanded.has(groupIndex)}
              busyKey={busyKey}
              onToggle={() => toggleExpanded(groupIndex)}
              onHighlight={() => highlight(group.target)}
              onDismiss={() => remove(groupIndex)}
              onEstablish={(action, actionIndex) => establish(action, groupIndex, actionIndex)}
            />
          ))}
        </div>
      )}
    </div>
  )
}

function SuggestionRow({
  group,
  groupIndex,
  expanded,
  busyKey,
  onToggle,
  onHighlight,
  onDismiss,
  onEstablish,
}: {
  group: SuggestionGroup
  groupIndex: number
  expanded: boolean
  busyKey: string | null
  onToggle: () => void
  onHighlight: () => void
  onDismiss: () => void
  onEstablish: (action: SuggestionAction, actionIndex: number) => void
}) {
  const single = group.actions.length === 1
  const busy = busyKey !== null

  return (
    <div className="flex flex-col">
      <div className="flex items-center gap-1 px-2 py-1.5">
        {/* Clicking the label highlights the candidate feature on the map
            (onHighlight) either way; the establish "+" (one-action group) and
            the expand chevron (multi-action group) stay separate affordances. */}
        {single ? (
          <button
            onClick={onHighlight}
            title="Show this feature on the map"
            className="hover:text-foreground flex min-w-0 flex-1 flex-col text-left"
          >
            <span className="truncate text-sm">{group.actions[0].title}</span>
            {group.distance_meters != null && (
              <span className="text-muted-foreground font-mono text-[11px]">{group.distance_meters.toFixed(0)} m away</span>
            )}
          </button>
        ) : (
          <button
            onClick={() => {
              onHighlight()
              onToggle()
            }}
            className="hover:text-foreground flex min-w-0 flex-1 items-center gap-1 text-left"
          >
            {expanded ? (
              <ChevronDown className="text-muted-foreground size-3.5 shrink-0" />
            ) : (
              <ChevronRight className="text-muted-foreground size-3.5 shrink-0" />
            )}
            <span className="flex min-w-0 flex-col">
              <span className="truncate text-sm font-medium">{group.label}</span>
              <span className="text-muted-foreground text-[11px]">
                {group.actions.length} options
                {group.distance_meters != null && ` · ${group.distance_meters.toFixed(0)} m`}
              </span>
            </span>
          </button>
        )}

        {single ? (
          <Button
            size="sm"
            className="h-7 shrink-0 px-2"
            title={group.actions[0].title}
            disabled={busy}
            onClick={() => onEstablish(group.actions[0], 0)}
          >
            {busyKey === `${groupIndex}:0` ? <Loader2 className="size-4 animate-spin" /> : <Plus className="size-4" />}
          </Button>
        ) : (
          <button
            onClick={onDismiss}
            className="text-muted-foreground hover:text-foreground shrink-0 p-1"
            title="Dismiss"
            disabled={busy}
          >
            <X className="size-3.5" />
            <span className="sr-only">Dismiss suggestions for this feature</span>
          </button>
        )}
      </div>

      {!single && expanded && (
        <div className="flex flex-col gap-1 pb-1.5 pl-6 pr-2">
          {group.actions.map((action, actionIndex) => (
            <div key={`${action.tool}-${actionIndex}`} className="flex items-center gap-1">
              <div className="flex min-w-0 flex-1 flex-col">
                <span className="truncate text-xs">{action.title}</span>
                {action.detail && <span className="text-muted-foreground truncate text-[11px]">{action.detail}</span>}
              </div>
              <Button
                size="sm"
                variant="secondary"
                className="h-6 shrink-0 px-2 text-xs"
                disabled={busy}
                onClick={() => onEstablish(action, actionIndex)}
              >
                {busyKey === `${groupIndex}:${actionIndex}` ? (
                  <Loader2 className="size-3.5 animate-spin" />
                ) : (
                  "Establish"
                )}
              </Button>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}
