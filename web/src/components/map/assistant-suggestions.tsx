import { useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { ChevronDown, ChevronRight, Loader2, Plus, Scissors, Sparkles, X } from "lucide-react"

import { Button } from "@/components/ui/button"
import { type FeatureRecord, useFeature } from "@/hooks/use-feature"
import {
  terminalsForSubtype,
  type TerminalSchema,
  useFeatureClassSchema,
} from "@/hooks/use-feature-class-schema"
import { ApiError, apiFetch } from "@/lib/api"
import {
  type SuggestionAction,
  type SuggestionGroup,
  type SuggestionTargetFeature,
  useAssistantStore,
} from "@/stores/assistant-store"
import { useDrawStore } from "@/stores/draw-store"
import { useSelectionStore } from "@/stores/selection-store"
import { useToastStore } from "@/stores/toast-store"

/** The shape the backend packs into a `split` action's `args`
 * (api/internal/assistant/suggest.go's splitActions): the line to cut and the
 * on-line point to cut it at, in EPSG:4326 lng/lat. Not a ready-to-POST body --
 * see `establish` below, which hands it to the manual-split create form. */
interface SplitArgs {
  feature_class: string
  feature_id: string
  split_point: { lng: number; lat: number }
}

/** One side of a `connection` action's prebuilt `args` -- the exact from/to shape
 * POST /connectivity takes (api/internal/assistant/suggest.go's endpointBody).
 * `terminal` is the rule-suggested terminal the backend pre-picked (omitted when
 * the engine wildcarded it); the inline picker below lets the user override it
 * before the '+' commits, exactly as the manual connect-confirm-panel.tsx does. */
interface ConnectionEndpoint {
  feature_class: string
  feature_id: string
  terminal?: string
}
interface ConnectionArgs {
  from: ConnectionEndpoint
  to: ConnectionEndpoint
}

const terminalSelectClass =
  "border-input bg-background h-6 w-full rounded border px-1.5 text-xs outline-none focus-visible:border-ring focus-visible:ring-ring/50 focus-visible:ring-[2px]"

/** One endpoint's terminal dropdown on a connection suggestion row: the terminals
 * that endpoint's subtype can connect through (terminalsForSubtype), defaulting to
 * the rule-suggested one. `stopPropagation` keeps a click inside the select from
 * highlighting/expanding the row. Mirrors EndpointPicker's "No terminal" + `{code}
 * ({role})` options so the two connectivity surfaces read the same. */
function TerminalRow({
  label,
  terminals,
  value,
  onChange,
  disabled,
}: {
  label: string
  terminals: TerminalSchema[]
  value: string
  onChange: (terminal: string) => void
  disabled: boolean
}) {
  return (
    <label className="flex items-center gap-1.5">
      <span className="text-muted-foreground w-16 shrink-0 truncate text-[11px]" title={label}>
        {label}
      </span>
      <select
        className={terminalSelectClass}
        value={value}
        disabled={disabled}
        onClick={(e) => e.stopPropagation()}
        onChange={(e) => onChange(e.target.value)}
      >
        <option value="">No terminal</option>
        {terminals.map((t) => (
          <option key={t.code} value={t.code}>
            {t.code} ({t.role})
          </option>
        ))}
      </select>
    </label>
  )
}

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
 * Acting on a suggestion doesn't hand anything to an LLM, and (for the two
 * establish kinds) POSTs the backend-prebuilt `args` verbatim to the very route
 * the manual workflow uses (POST /connectivity or /relationships), so the write
 * path, its RBAC commit gate, and its actor attribution are exactly the manual
 * ones. The row only exists to skip the manual source/target/terminal picking;
 * the human's click is still the commit.
 *
 * The one exception is a `split` action (Phase 29,
 * docs/phase-29-point-on-line-split.md), proposed when the just-created point
 * landed on a line: it writes nothing here. Instead it opens the manual-split
 * create form (draw-store.startSplitDraft) pre-filled from the source line, and
 * that form's own Save is what POSTs to /features/{code}/{id}/split -- so the
 * far half's attributes get a human's eyes before the cut commits.
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

  // argsOverride, when present, is the connection body the row rebuilt from the
  // user's inline terminal picks (a clone of action.args with from/to.terminal
  // swapped); a split or an untouched connection passes action.args verbatim, so
  // the write path and its RBAC gate stay identical to the manual one.
  async function establish(
    action: SuggestionAction,
    groupIndex: number,
    actionIndex: number,
    argsOverride?: unknown,
  ) {
    if (!source) return
    setBusyKey(`${groupIndex}:${actionIndex}`)
    try {
      // A split doesn't write here -- it opens the same manual-split create form
      // (Phase 20) pre-filled from the source line, and the human confirms the
      // far half's attributes before Save POSTs to /split. So this branch fetches
      // the line for its seed (the action's target only carries name/subtype, not
      // attributes) and starts the draft; the chip is dismissed as the form takes
      // over. Nothing is committed until that form's own Save.
      if (action.kind === "split") {
        const { feature_class, feature_id, split_point } = action.args as SplitArgs
        const line = await apiFetch<FeatureRecord>(`/features/${feature_class}/${feature_id}`)
        useDrawStore.getState().startSplitDraft(feature_class, feature_id, split_point, {
          name: line.name ?? undefined,
          subtype: line.subtype,
          attributes: { ...line.attributes },
        })
        removeAction(groupIndex, actionIndex)
        return
      }

      const path = action.kind === "connection" ? "/connectivity" : "/relationships"
      // Both establish routes return JSON; only a relationship carries an
      // optional plausibility_warning (a soft geometric sanity check that
      // never blocks the write -- see api/internal/relationship/plausibility.go),
      // surfaced as a warning toast exactly like connect-confirm-panel.tsx does.
      const result = await apiFetch<{ plausibility_warning?: string }>(path, {
        method: "POST",
        body: JSON.stringify(argsOverride ?? action.args),
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
        action.kind === "split"
          ? "Could not open the split for this line."
          : action.kind === "connection"
            ? "Could not establish this connection."
            : "Could not establish this relationship."
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : fallback)
    } finally {
      setBusyKey(null)
    }
  }

  return (
    <div className="bg-popover text-popover-foreground fixed bottom-10 left-4 z-40 flex w-72 flex-col overflow-hidden rounded-lg border border-border/70 border-t-2 border-t-primary shadow-xl ring-1 ring-black/5">
      <div className="text-muted-foreground border-primary/15 flex items-center justify-between gap-1 border-b bg-gradient-to-b from-accent/50 to-accent/20 px-2 py-1.5 text-xs font-medium">
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
              onEstablish={(action, actionIndex, argsOverride) =>
                establish(action, groupIndex, actionIndex, argsOverride)
              }
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
  onEstablish: (action: SuggestionAction, actionIndex: number, argsOverride?: unknown) => void
}) {
  const single = group.actions.length === 1
  const busy = busyKey !== null

  // The two endpoints of any connection in this group are always the same pair:
  // the assistant's single source feature (the just-edited one) on the `from`
  // side and this row's candidate (group.target) on the `to` side. So resolve
  // each side's terminals once here -- its subtype's terminals from the class
  // schema (terminalsForSubtype), same as the manual EndpointPicker. Source
  // subtype needs a fetch (the store only holds a bare ref); the candidate's
  // rides along on the action. Queries are cached, so the extra reads are cheap
  // and shared across every row.
  const source = useAssistantStore((s) => s.source)
  const { data: sourceFeature } = useFeature(source?.featureClassCode, source?.featureId)
  const { data: sourceSchema } = useFeatureClassSchema(source?.featureClassCode)
  const { data: targetSchema } = useFeatureClassSchema(group.target.feature_class)
  const fromTerminals = sourceSchema ? terminalsForSubtype(sourceSchema, sourceFeature?.subtype) : []
  const toTerminals = targetSchema ? terminalsForSubtype(targetSchema, group.target.subtype) : []
  const hasTerminals = fromTerminals.length > 0 || toTerminals.length > 0

  // Explicit per-endpoint terminal picks, keyed `${actionIndex}:${side}`. Absent
  // means "use the backend's rule-suggested terminal" (args.<side>.terminal), so
  // an untouched row commits exactly what the manual auto-pick would.
  const [overrides, setOverrides] = useState<Record<string, string>>({})
  const sourceLabel = sourceFeature?.name || source?.featureClassCode || "This feature"
  const targetLabel = group.target.name || group.target.feature_class

  function resolvedTerminal(actionIndex: number, side: "from" | "to", suggested: string | undefined): string {
    const key = `${actionIndex}:${side}`
    return key in overrides ? overrides[key] : (suggested ?? "")
  }

  // Commit a connection with the user's terminal picks folded into a clone of the
  // prebuilt body (an empty pick means "no terminal", so it's dropped rather than
  // sent as ""); every other action (split/relationship, or a connection whose
  // endpoints have no terminals) passes through untouched.
  function handleEstablish(action: SuggestionAction, actionIndex: number) {
    if (action.kind !== "connection" || !hasTerminals) {
      onEstablish(action, actionIndex)
      return
    }
    const args = action.args as ConnectionArgs
    const next: ConnectionArgs = { from: { ...args.from }, to: { ...args.to } }
    const fromT = resolvedTerminal(actionIndex, "from", args.from?.terminal)
    const toT = resolvedTerminal(actionIndex, "to", args.to?.terminal)
    if (fromT) next.from.terminal = fromT
    else delete next.from.terminal
    if (toT) next.to.terminal = toT
    else delete next.to.terminal
    onEstablish(action, actionIndex, next)
  }

  // The inline terminal picker(s) for one connection action -- one row per
  // endpoint that actually has terminals, so a bare edge (MV_CONDUCTOR) shows
  // nothing and a multi-terminal device (a switch's T1/T2) shows a dropdown the
  // user picks before establishing.
  function terminalPickers(action: SuggestionAction, actionIndex: number) {
    if (action.kind !== "connection" || !hasTerminals) return null
    const args = action.args as ConnectionArgs
    return (
      <div className="mt-1 flex flex-col gap-1">
        {fromTerminals.length > 0 && (
          <TerminalRow
            label={sourceLabel}
            terminals={fromTerminals}
            value={resolvedTerminal(actionIndex, "from", args.from?.terminal)}
            onChange={(t) => setOverrides((prev) => ({ ...prev, [`${actionIndex}:from`]: t }))}
            disabled={busy}
          />
        )}
        {toTerminals.length > 0 && (
          <TerminalRow
            label={targetLabel}
            terminals={toTerminals}
            value={resolvedTerminal(actionIndex, "to", args.to?.terminal)}
            onChange={(t) => setOverrides((prev) => ({ ...prev, [`${actionIndex}:to`]: t }))}
            disabled={busy}
          />
        )}
      </div>
    )
  }

  const singlePickers = single ? terminalPickers(group.actions[0], 0) : null

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
            onClick={() => handleEstablish(group.actions[0], 0)}
          >
            {busyKey === `${groupIndex}:0` ? (
              <Loader2 className="size-4 animate-spin" />
            ) : group.actions[0].kind === "split" ? (
              <Scissors className="size-4" />
            ) : (
              <Plus className="size-4" />
            )}
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

      {/* A single-action connection's terminal picker(s) sit under the row,
          above nothing else -- pick the terminal, then the '+' establishes. */}
      {singlePickers && <div className="px-2 pb-1.5">{singlePickers}</div>}

      {!single && expanded && (
        <div className="flex flex-col gap-1 pb-1.5 pl-6 pr-2">
          {group.actions.map((action, actionIndex) => (
            <div key={`${action.tool}-${actionIndex}`} className="flex flex-col gap-1">
              <div className="flex items-center gap-1">
                <div className="flex min-w-0 flex-1 flex-col">
                  <span className="truncate text-xs">{action.title}</span>
                  {action.detail && <span className="text-muted-foreground truncate text-[11px]">{action.detail}</span>}
                </div>
                <Button
                  size="sm"
                  variant="secondary"
                  className="h-6 shrink-0 px-2 text-xs"
                  disabled={busy}
                  onClick={() => handleEstablish(action, actionIndex)}
                >
                  {busyKey === `${groupIndex}:${actionIndex}` ? (
                    <Loader2 className="size-3.5 animate-spin" />
                  ) : action.kind === "split" ? (
                    "Split"
                  ) : (
                    "Establish"
                  )}
                </Button>
              </div>
              {terminalPickers(action, actionIndex)}
            </div>
          ))}
        </div>
      )}
    </div>
  )
}
