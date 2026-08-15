import { Fragment, useEffect, useRef, useState } from "react"
import {
  ChevronDown,
  GripHorizontal,
  Info,
  Link2,
  ListChecks,
  Loader2,
  Pencil,
  Plug,
  Plus,
  Route,
  Trash2,
  Unlink2,
  Workflow,
  X,
  Zap,
} from "lucide-react"

import { CreateDiagramForm } from "@/components/map/create-diagram-form"
import { DeleteFeatureDialog } from "@/components/map/delete-feature-dialog"
import { FeatureCreateForm } from "@/components/map/feature-create-form"
import { FeatureEditForm } from "@/components/map/feature-edit-form"
import { formatFieldLabel, formatValue } from "@/components/map/field-utils"
import { RevokeConnectionDialog } from "@/components/map/revoke-connection-dialog"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { type Connection, useConnections } from "@/hooks/use-connections"
import { useDiagramInstances } from "@/hooks/use-diagrams"
import { useFeature } from "@/hooks/use-feature"
import { useFeatureClassRules } from "@/hooks/use-feature-class-rules"
import { useFeatureClasses } from "@/hooks/use-feature-classes"
import { useRelationships } from "@/hooks/use-relationships"
import { useTraceConfigurations } from "@/hooks/use-trace-configurations"
import { ApiError, apiFetch } from "@/lib/api"
import { cn } from "@/lib/utils"
import { useConnectStore } from "@/stores/connect-store"
import { useDiagramStore } from "@/stores/diagram-store"
import { useDrawStore } from "@/stores/draw-store"
import { useEditStore } from "@/stores/edit-store"
import { type IsolationResult, useIsolateStore } from "@/stores/isolate-store"
import { useSelectionStore } from "@/stores/selection-store"
import { useErrorToast, useToastStore } from "@/stores/toast-store"
import { type TraceFeatureRef, type TraceResult, useTraceStore } from "@/stores/trace-store"

type TabId = "attributes" | "terminals" | "relationships" | "rules"

const TABS: { id: TabId; label: string; icon: typeof Info }[] = [
  { id: "attributes", label: "Attributes", icon: Info },
  { id: "terminals", label: "Terminals", icon: Plug },
  { id: "relationships", label: "Relationships", icon: Link2 },
  { id: "rules", label: "Rules", icon: ListChecks },
]

function shortId(id: string): string {
  return id.length > 8 ? `${id.slice(0, 8)}…` : id
}

function AttributesTab() {
  const selected = useSelectionStore((s) => s.selected)
  const editing = useEditStore((s) => s.editing)
  const { data: feature, isPending, isError, error } = useFeature(
    selected?.featureClassCode,
    selected?.featureId,
  )
  useErrorToast(isError, error, "Could not load this feature")

  const isEditingThis =
    selected && editing?.featureClassCode === selected.featureClassCode && editing?.featureId === selected.featureId
  if (isEditingThis) {
    return <FeatureEditForm featureClassCode={selected.featureClassCode} featureId={selected.featureId} />
  }

  if (isPending) {
    return (
      <div className="text-muted-foreground flex items-center gap-2 text-sm">
        <Loader2 className="size-4 animate-spin" />
        Loading feature...
      </div>
    )
  }
  if (isError || !feature) return null

  return (
    <dl className="grid grid-cols-[max-content_1fr] gap-x-6 gap-y-1.5 text-sm">
      <dt className="text-muted-foreground">Status</dt>
      <dd className="capitalize">{feature.status}</dd>

      {feature.deleted_at && (
        <>
          <dt className="text-muted-foreground">Deleted</dt>
          <dd>{new Date(feature.deleted_at).toLocaleString()}</dd>
        </>
      )}

      {feature.subtype !== undefined && (
        <>
          <dt className="text-muted-foreground">Subtype</dt>
          <dd>{feature.subtype}</dd>
        </>
      )}

      {Object.entries(feature.attributes).map(([key, value]) => (
        <Fragment key={key}>
          <dt className="text-muted-foreground">{formatFieldLabel(key)}</dt>
          <dd>{formatValue(value)}</dd>
        </Fragment>
      ))}

      <dt className="text-muted-foreground">Updated</dt>
      <dd>{new Date(feature.updated_at).toLocaleString()}</dd>
    </dl>
  )
}

/**
 * Resolves each counterpart's per-feature name for TerminalsTab and
 * RelationshipsTab's row lists -- same independent-fetch-per-row
 * pattern trace-result-table.tsx uses for its own table, since both
 * tabs render a variable-length list (so useFeature, a single hook
 * call, can't be used in a loop) and neither wants to share a cache key
 * with the single selected feature's own useFeature call. Keyed by
 * `feature_class:feature_id`; refetches whenever the row list itself
 * changes (a new connection/relationship established, or a different
 * feature selected).
 */
function useCounterpartNames(refs: { feature_class: string; feature_id: string }[] | undefined) {
  const [names, setNames] = useState<Record<string, string | null>>({})
  const key = refs?.map((r) => `${r.feature_class}:${r.feature_id}`).join(",") ?? ""

  useEffect(() => {
    if (!refs || refs.length === 0) {
      setNames({})
      return
    }
    let cancelled = false
    // Reassigned into a typed local -- same reason
    // trace-result-table.tsx does this for its own nested async
    // function: TS's narrowing from the `if (!refs)` check above
    // doesn't propagate into loadNames below.
    const resolved: NonNullable<typeof refs> = refs

    async function loadNames() {
      const entries = await Promise.all(
        resolved.map(async (ref) => {
          try {
            const feature = await apiFetch<{ name: string | null }>(`/features/${ref.feature_class}/${ref.feature_id}`)
            return [`${ref.feature_class}:${ref.feature_id}`, feature.name] as const
          } catch {
            return [`${ref.feature_class}:${ref.feature_id}`, null] as const
          }
        }),
      )
      if (!cancelled) setNames(Object.fromEntries(entries))
    }
    loadNames()
    return () => {
      cancelled = true
    }
    // Deliberately keyed on `key` (a stable string derived from refs),
    // not `refs` itself -- refs is a fresh array/object identity on
    // every render (each Tab recomputes it from query data inline), so
    // depending on it directly would refetch every render.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key])

  return names
}

function TerminalsTab() {
  const selected = useSelectionStore((s) => s.selected)
  const { data: featureClasses } = useFeatureClasses()
  const { data, isPending, isError, error } = useConnections(
    selected?.featureClassCode,
    selected?.featureId,
  )
  useErrorToast(isError, error, "Could not load connections")
  const [revokingConnection, setRevokingConnection] = useState<Connection | null>(null)
  const counterpartNames = useCounterpartNames(
    data?.connections.map((conn) => {
      const isFrom =
        conn.from.feature_class === selected?.featureClassCode && conn.from.feature_id === selected?.featureId
      return isFrom ? conn.to : conn.from
    }),
  )

  const newConnectionButton = selected && (
    <Button
      variant="outline"
      size="sm"
      className="self-start"
      onClick={() => useConnectStore.getState().start("connectivity", selected)}
    >
      <Plus className="size-4" />
      New connection
    </Button>
  )

  // The dialog needs to stay mounted across every branch below -- a
  // successful revoke can flip data.connections from non-empty to
  // empty (switching which branch renders) while the dialog is still
  // closing itself, so it can't live only inside the "has connections"
  // return.
  const revokeDialog = (
    <RevokeConnectionDialog
      connection={revokingConnection}
      onOpenChange={(open) => !open && setRevokingConnection(null)}
    />
  )

  if (isPending) {
    return (
      <div className="text-muted-foreground flex items-center gap-2 text-sm">
        <Loader2 className="size-4 animate-spin" />
        Loading connections...
      </div>
    )
  }
  if (isError) return null
  if (!selected || !data || data.connections.length === 0) {
    return (
      <div className="flex flex-col items-start gap-2">
        {newConnectionButton}
        <p className="text-muted-foreground text-sm">No terminal connections.</p>
        {revokeDialog}
      </div>
    )
  }

  return (
    <div className="flex flex-col items-start gap-2">
      {newConnectionButton}
      <div className="flex w-full flex-col gap-2">
        {data.connections.map((conn) => {
          const isFrom =
            conn.from.feature_class === selected.featureClassCode && conn.from.feature_id === selected.featureId
          const counterpart = isFrom ? conn.to : conn.from
          const thisTerminal = isFrom ? conn.from.terminal : conn.to.terminal
          const counterpartClassName =
            featureClasses?.feature_classes.find((fc) => fc.code === counterpart.feature_class)?.name ??
            counterpart.feature_class
          const counterpartFeatureName = counterpartNames[`${counterpart.feature_class}:${counterpart.feature_id}`]
          const counterpartPrimary = counterpartFeatureName || counterpartClassName
          const counterpartSecondary = counterpartFeatureName
            ? `${counterpartClassName} · ${shortId(counterpart.feature_id)}`
            : shortId(counterpart.feature_id)

          return (
            <div
              key={conn.id}
              className="hover:bg-accent hover:text-accent-foreground group flex items-start gap-1 rounded-md border p-2 text-sm transition-colors"
            >
              <button
                type="button"
                onClick={() =>
                  useSelectionStore.getState().select(counterpart.feature_class, counterpart.feature_id, {
                    focus: true,
                  })
                }
                className="min-w-0 flex-1 text-left"
              >
                <div className="flex items-center justify-between gap-2">
                  <span className="font-medium">{counterpartPrimary}</span>
                  <span className="text-muted-foreground shrink-0 text-xs">{isFrom ? "Outgoing" : "Incoming"}</span>
                </div>
                <p className="text-muted-foreground font-mono text-xs">{counterpartSecondary}</p>
                <p className="text-muted-foreground mt-1 text-xs">
                  {thisTerminal && <>via {thisTerminal} · </>}
                  {conn.network_rule_name}
                </p>
              </button>
              <button
                type="button"
                onClick={() => setRevokingConnection(conn)}
                className="text-muted-foreground hover:bg-destructive/10 hover:text-destructive shrink-0 rounded p-1"
              >
                <Unlink2 className="size-3.5" />
                <span className="sr-only">Revoke connection</span>
              </button>
            </div>
          )
        })}
      </div>
      {revokeDialog}
    </div>
  )
}

function RelationshipsTab() {
  const selected = useSelectionStore((s) => s.selected)
  const { data: featureClasses } = useFeatureClasses()
  const { data, isPending, isError, error } = useRelationships(
    selected?.featureClassCode,
    selected?.featureId,
  )
  useErrorToast(isError, error, "Could not load relationships")
  const counterpartNames = useCounterpartNames(
    data?.relationships.map((rel) => {
      const isOrigin =
        rel.origin.feature_class === selected?.featureClassCode && rel.origin.feature_id === selected?.featureId
      return isOrigin ? rel.destination : rel.origin
    }),
  )

  const newRelationshipButton = selected && (
    <Button
      variant="outline"
      size="sm"
      className="self-start"
      onClick={() => useConnectStore.getState().start("relationship", selected)}
    >
      <Plus className="size-4" />
      New relationship
    </Button>
  )

  if (isPending) {
    return (
      <div className="text-muted-foreground flex items-center gap-2 text-sm">
        <Loader2 className="size-4 animate-spin" />
        Loading relationships...
      </div>
    )
  }
  if (isError) return null
  if (!selected || !data || data.relationships.length === 0) {
    return (
      <div className="flex flex-col items-start gap-2">
        {newRelationshipButton}
        <p className="text-muted-foreground text-sm">No relationships.</p>
      </div>
    )
  }

  return (
    <div className="flex flex-col items-start gap-2">
      {newRelationshipButton}
      <div className="flex w-full flex-col gap-2">
        {data.relationships.map((rel) => {
          const isOrigin =
            rel.origin.feature_class === selected.featureClassCode && rel.origin.feature_id === selected.featureId
          const counterpart = isOrigin ? rel.destination : rel.origin
          const counterpartClassName =
            featureClasses?.feature_classes.find((fc) => fc.code === counterpart.feature_class)?.name ??
            counterpart.feature_class
          const counterpartFeatureName = counterpartNames[`${counterpart.feature_class}:${counterpart.feature_id}`]
          const counterpartPrimary = counterpartFeatureName || counterpartClassName
          const counterpartSecondary = counterpartFeatureName
            ? `${counterpartClassName} · ${shortId(counterpart.feature_id)}`
            : shortId(counterpart.feature_id)

          return (
            <button
              key={rel.id}
              type="button"
              onClick={() => useSelectionStore.getState().select(counterpart.feature_class, counterpart.feature_id, { focus: true })}
              className="hover:bg-accent hover:text-accent-foreground w-full rounded-md border p-2 text-left text-sm transition-colors"
            >
              <div className="flex items-center justify-between gap-2">
                <span className="font-medium">{counterpartPrimary}</span>
                <span className="text-muted-foreground shrink-0 text-xs">{isOrigin ? "Origin" : "Destination"}</span>
              </div>
              <p className="text-muted-foreground font-mono text-xs">{counterpartSecondary}</p>
              <p className="text-muted-foreground mt-1 text-xs">
                {rel.relationship_class_name} · {rel.kind} · {rel.cardinality}
              </p>
            </button>
          )
        })}
      </div>
    </div>
  )
}

/**
 * Class-scoped, not instance-scoped, unlike TerminalsTab/RelationshipsTab
 * above -- every feature of the same class has the same answer to "what
 * rules apply," so this reads from useFeatureClassRules keyed on
 * featureClassCode rather than a per-feature endpoint. Answers what
 * Terminals/Relationships never could on their own: those two only ever
 * show what's actually been established for *this* feature; this shows
 * what's *allowed* for the whole class -- network rules
 * (api/internal/connectivity), relationship rules
 * (api/internal/relationship), and snapping rules (never surfaced
 * anywhere before api/internal/rules).
 */
function RulesTab() {
  const selected = useSelectionStore((s) => s.selected)
  const { data, isPending, isError, error } = useFeatureClassRules(selected?.featureClassCode)
  useErrorToast(isError, error, "Could not load rules")

  if (isPending) {
    return (
      <div className="text-muted-foreground flex items-center gap-2 text-sm">
        <Loader2 className="size-4 animate-spin" />
        Loading rules...
      </div>
    )
  }
  if (isError || !data) return null

  const hasNothing =
    data.network_rules.length === 0 && data.relationship_rules.length === 0 && data.snapping_rules.length === 0
  if (hasNothing) {
    return <p className="text-muted-foreground text-sm">No rules configured for this feature class.</p>
  }

  return (
    <div className="flex flex-col gap-4">
      {data.network_rules.length > 0 && (
        <div className="flex flex-col gap-2">
          <h3 className="text-muted-foreground text-xs font-semibold uppercase">Network rules</h3>
          {data.network_rules.map((r, i) => (
            <div key={i} className="rounded-md border p-2 text-sm">
              <div className="flex items-center justify-between gap-2">
                <span className="font-medium">
                  {r.from_class}
                  {r.from_terminal && ` (${r.from_terminal})`} → {r.to_class}
                  {r.to_terminal && ` (${r.to_terminal})`}
                </span>
                <span className="text-muted-foreground shrink-0 text-xs">
                  {r.direction === "outgoing" ? "Outgoing" : "Incoming"}
                </span>
              </div>
              <p className="text-muted-foreground mt-1 text-xs">{r.name}</p>
              {r.phase_rule && <p className="text-muted-foreground text-xs">Phase: {r.phase_rule}</p>}
              {!r.is_valid && <p className="text-destructive text-xs">Disabled (is_valid = false)</p>}
            </div>
          ))}
        </div>
      )}

      {data.relationship_rules.length > 0 && (
        <div className="flex flex-col gap-2">
          <h3 className="text-muted-foreground text-xs font-semibold uppercase">Relationship rules</h3>
          {data.relationship_rules.map((r, i) => (
            <div key={i} className="rounded-md border p-2 text-sm">
              <div className="flex items-center justify-between gap-2">
                <span className="font-medium">
                  {r.origin_class} → {r.destination_class}
                </span>
                <span className="text-muted-foreground shrink-0 text-xs">
                  {r.direction === "origin" ? "Origin" : "Destination"}
                </span>
              </div>
              <p className="text-muted-foreground mt-1 text-xs">
                {r.kind} · {r.cardinality}
              </p>
            </div>
          ))}
        </div>
      )}

      {data.snapping_rules.length > 0 && (
        <div className="flex flex-col gap-2">
          <h3 className="text-muted-foreground text-xs font-semibold uppercase">Snapping rules</h3>
          {data.snapping_rules.map((r, i) => (
            <div key={i} className="rounded-md border p-2 text-sm">
              <div className="flex items-center justify-between gap-2">
                <span className="font-medium">
                  {r.from_class} → {r.to_class}
                  {r.to_terminal && ` (${r.to_terminal})`}
                </span>
                <span className="text-muted-foreground shrink-0 text-xs">
                  {r.direction === "from" ? "As source" : "As target"}
                </span>
              </div>
              <p className="text-muted-foreground mt-1 text-xs">
                {r.target_type} · {r.tolerance_m}m tolerance · priority {r.priority}
                {r.is_required && " · required"}
              </p>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}

/** Default position (bottom-right, clear of the h-7 status bar below
 * it) used until the user drags the panel somewhere else -- matches
 * `!position` in connect-confirm-panel.tsx's own className branch. */
const DEFAULT_POSITION_CLASS = "bottom-10 right-4"

/**
 * Floating and draggable (grab the header, same mechanics as
 * connect-confirm-panel.tsx's own `handleHeaderPointerDown`/`position`
 * state), not docked -- so it can be moved clear of whatever part of
 * the map the user is actually working on, the same way the
 * connect/relationship confirm panel already can be. `position` lives
 * in this component's own state, which React preserves across
 * re-renders even while this returns null (nothing selected, no
 * draft) -- so a window the user has already dragged somewhere stays
 * there the next time a feature is selected, rather than snapping back
 * to the default corner on every click. Resizing/minimizing remain out
 * of scope -- undocking was the specifically requested piece; a fixed
 * `w-96` and internal `overflow-y-auto` (as before) are enough for now.
 *
 * Side tabs, not top or bottom ones -- a vertical rail spends the
 * panel's plentiful horizontal space instead of costing height a
 * bottom tab strip would take directly out of the already-short
 * content area.
 */
export function FeaturePanel() {
  const selected = useSelectionStore((s) => s.selected)
  const clear = useSelectionStore((s) => s.clear)
  const draft = useDrawStore((s) => s.draft)
  const clearDraft = useDrawStore((s) => s.clearDraft)
  const editing = useEditStore((s) => s.editing)
  const { data: featureClasses } = useFeatureClasses()
  const { data: feature } = useFeature(selected?.featureClassCode, selected?.featureId)
  const [activeTab, setActiveTab] = useState<TabId>("attributes")
  const [deleteDialogOpen, setDeleteDialogOpen] = useState(false)
  const [creatingDiagram, setCreatingDiagram] = useState(false)
  const [position, setPosition] = useState<{ x: number; y: number } | null>(null)
  const panelRef = useRef<HTMLDivElement>(null)
  const tracing = useTraceStore((s) => s.loading)
  const isolating = useIsolateStore((s) => s.loading)
  const { data: traceConfigurations } = useTraceConfigurations()

  const isEditingSelected =
    Boolean(selected) &&
    editing?.featureClassCode === selected?.featureClassCode &&
    editing?.featureId === selected?.featureId

  // Land back on Attributes for every newly selected feature -- staying
  // on, say, Relationships while the id underneath changes would show
  // the new feature's relationships under a tab the user didn't
  // deliberately choose for it. Also ends whatever edit session was in
  // progress -- edit-store.ts's `editing` would otherwise keep pointing
  // at a feature that's no longer selected, and voltedge-map.tsx's
  // click guard already stops a click from changing `selected` while
  // editing anyway, so the only way this effect fires mid-edit is
  // Escape, which should end the edit too. Deliberately doesn't touch
  // `position` -- unlike connect-confirm-panel.tsx's per-workflow reset,
  // a window the user already moved should stay put across selections.
  useEffect(() => {
    setActiveTab("attributes")
    setDeleteDialogOpen(false)
    setCreatingDiagram(false)
    useEditStore.getState().stop()
  }, [selected?.featureClassCode, selected?.featureId])

  function handleHeaderPointerDown(e: React.PointerEvent) {
    const rect = panelRef.current?.getBoundingClientRect()
    if (!rect) return
    const startX = e.clientX
    const startY = e.clientY
    const origX = position?.x ?? rect.left
    const origY = position?.y ?? rect.top

    const handleMove = (moveEvt: PointerEvent) => {
      setPosition({ x: origX + (moveEvt.clientX - startX), y: origY + (moveEvt.clientY - startY) })
    }
    const handleUp = () => {
      window.removeEventListener("pointermove", handleMove)
      window.removeEventListener("pointerup", handleUp)
    }
    window.addEventListener("pointermove", handleMove)
    window.addEventListener("pointerup", handleUp)
  }

  // Runs a real trace (api/internal/trace, see
  // docs/phase-4-trace-engine.md and docs/phase-4-utility-traces.md)
  // starting from the selected feature, by trace_configurations *code*
  // -- the dropdown below is built directly off GET /trace/configurations,
  // so there's nothing left to resolve by direction the way this used
  // to when there were only ever two hardcoded buttons. SHORTEST_PATH
  // configs are filtered out of that dropdown (no single, obvious
  // target to trace to from a one-click panel action), so every code
  // this can be called with is a real, runnable one-shot trace. Clears
  // any isolation result first -- the two workflows share the same map
  // highlight/result-panel real estate and are mutually exclusive, not
  // simultaneous. The result lands in trace-store.ts, which
  // voltedge-map.tsx and trace-result-table.tsx both read -- this
  // function's only job is the request itself.
  async function runTrace(configCode: string) {
    if (!selected) return
    useIsolateStore.getState().clear()
    useTraceStore.getState().setLoading(true)
    try {
      const result = await apiFetch<TraceResult>("/trace", {
        method: "POST",
        body: JSON.stringify({
          trace_configuration: configCode,
          start: { feature_class: selected.featureClassCode, feature_id: selected.featureId },
        }),
      })
      useTraceStore.getState().setResult(result)
    } catch (err) {
      useTraceStore.getState().setLoading(false)
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not run this trace.")
    }
  }

  // Isolation (api/internal/trace/isolate.go) needs a DOWNSTREAM
  // config for its barrier settings -- resolved by direction, same
  // reasoning the old hardcoded trace buttons used, since there's no
  // separate "isolation configuration" of its own to pick from a list.
  const isolationConfig = traceConfigurations?.trace_configurations.find((c) => c.direction === "DOWNSTREAM")

  async function runIsolate() {
    if (!selected || !isolationConfig) return
    useTraceStore.getState().clear()
    useIsolateStore.getState().setLoading(true)
    try {
      const result = await apiFetch<IsolationResult>("/trace/isolate", {
        method: "POST",
        body: JSON.stringify({
          trace_configuration: isolationConfig.code,
          device: { feature_class: selected.featureClassCode, feature_id: selected.featureId },
        }),
      })
      useIsolateStore.getState().setResult(result)
    } catch (err) {
      useIsolateStore.getState().setLoading(false)
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not isolate from this device.")
    }
  }

  // Feeder trace (api/internal/trace/feeder.go) and section trace
  // (api/internal/trace/section.go) each hit their own dedicated
  // endpoint, not a named trace_configurations row -- both still need
  // isolationConfig's DOWNSTREAM barrier settings, same reasoning
  // isolation itself uses one. Their differently-shaped responses
  // (heads / upstream_boundary+downstream_boundary instead of
  // barriers/targets) are normalized into the same TraceResult shape
  // trace-result-table.tsx already renders, rather than building two
  // more result tables from scratch -- heads/boundaries become
  // `targets` so they get the same visually-distinct treatment a
  // target_class hit already gets.
  async function runFeederTrace() {
    if (!selected || !isolationConfig) return
    useIsolateStore.getState().clear()
    useTraceStore.getState().setLoading(true)
    try {
      const result = await apiFetch<{ start: TraceFeatureRef; heads: TraceFeatureRef[]; features: TraceFeatureRef[] }>(
        "/trace/feeder",
        {
          method: "POST",
          body: JSON.stringify({
            trace_configuration: isolationConfig.code,
            start: { feature_class: selected.featureClassCode, feature_id: selected.featureId },
          }),
        },
      )
      useTraceStore.getState().setResult({
        trace_configuration: "FEEDER",
        direction: "FEEDER",
        start: result.start,
        features: result.features,
        barriers: [],
        targets: result.heads,
        targets_found: result.heads.length > 0,
      })
    } catch (err) {
      useTraceStore.getState().setLoading(false)
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not trace this feeder.")
    }
  }

  async function runSectionTrace() {
    if (!selected || !isolationConfig) return
    useIsolateStore.getState().clear()
    useTraceStore.getState().setLoading(true)
    try {
      const result = await apiFetch<{
        start: TraceFeatureRef
        features: TraceFeatureRef[]
        upstream_boundary?: TraceFeatureRef
        downstream_boundary?: TraceFeatureRef
      }>("/trace/section", {
        method: "POST",
        body: JSON.stringify({
          trace_configuration: isolationConfig.code,
          start: { feature_class: selected.featureClassCode, feature_id: selected.featureId },
        }),
      })
      const boundaries = [result.upstream_boundary, result.downstream_boundary].filter(
        (b): b is TraceFeatureRef => Boolean(b),
      )
      useTraceStore.getState().setResult({
        trace_configuration: "SECTION",
        direction: "SECTION",
        start: result.start,
        features: result.features,
        barriers: [],
        targets: boundaries,
        targets_found: boundaries.length > 0,
      })
    } catch (err) {
      useTraceStore.getState().setLoading(false)
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not trace this section.")
    }
  }

  // Runnable trace_configurations for the dropdown -- SHORTEST_PATH
  // excluded (needs a target, no single one-click starting point).
  const runnableConfigs = traceConfigurations?.trace_configurations.filter((c) => c.direction !== "SHORTEST_PATH") ?? []
  // Isolation only makes sense from a junction -- an edge or a
  // non-network feature has nothing to "open."
  const selectedClass = featureClasses?.feature_classes.find((fc) => fc.code === selected?.featureClassCode)
  const selectedNetworkRole = selectedClass?.network_role
  const canIsolate = selectedNetworkRole === "junction" && Boolean(isolationConfig)

  // Diagrams: only fetched at all once the selected feature's own class
  // is a real container (is_diagram_container) -- most feature classes
  // never host one, no point issuing a query that always comes back
  // empty. A container can host more than one diagram instance
  // (docs/phase-5-diagrams.md), so the trigger below is a small menu
  // once there's more than one to choose from, not a single button.
  const isDiagramContainer = Boolean(selectedClass?.is_diagram_container)
  const { data: diagramsData } = useDiagramInstances(
    isDiagramContainer ? selected?.featureClassCode : undefined,
    isDiagramContainer ? selected?.featureId : undefined,
  )
  const diagrams = diagramsData?.diagrams ?? []

  const positionStyle = position ? { left: position.x, top: position.y } : undefined
  const positionClass = cn(
    "bg-background fixed z-40 flex h-64 w-96 flex-col rounded-md border shadow-lg",
    !position && DEFAULT_POSITION_CLASS,
  )

  // An unsaved draft (see draw-store.ts) takes over the whole panel,
  // no tabs -- a feature with no id yet has no terminals or
  // relationships to show. Checked before `selected` so starting a new
  // draw while another feature is already selected still shows the
  // form, not that older feature's tabs.
  if (draft) {
    const className =
      featureClasses?.feature_classes.find((fc) => fc.code === draft.featureClassCode)?.name ??
      draft.featureClassCode

    return (
      <div ref={panelRef} style={positionStyle} className={positionClass}>
        <div
          onPointerDown={handleHeaderPointerDown}
          className="flex cursor-grab items-center justify-between border-b px-3 py-2 select-none active:cursor-grabbing"
        >
          <div className="flex min-w-0 items-center gap-1.5">
            <GripHorizontal className="text-muted-foreground size-3.5 shrink-0" />
            <div className="min-w-0">
              <p className="truncate text-sm font-medium">New {className}</p>
              <p className="text-muted-foreground truncate text-xs">Unsaved</p>
            </div>
          </div>
          <Button variant="ghost" size="icon" className="size-7 shrink-0" onClick={clearDraft}>
            <X className="size-4" />
            <span className="sr-only">Cancel</span>
          </Button>
        </div>
        <div className="flex-1 overflow-y-auto p-3">
          <FeatureCreateForm />
        </div>
      </div>
    )
  }

  if (!selected) return null

  const className =
    featureClasses?.feature_classes.find((fc) => fc.code === selected.featureClassCode)?.name ??
    selected.featureClassCode
  // A per-feature name, once set, is the more human-identifiable label
  // -- promoted to the primary line, with the class name and id (the
  // system guid, less meaningful on its own) demoted to the secondary
  // line together. No name yet: unchanged from before this field
  // existed, class name primary, id secondary.
  const primaryLabel = feature?.name || className
  const secondaryLabel = isEditingSelected
    ? "Editing…"
    : feature?.name
      ? `${className} · ${shortId(selected.featureId)}`
      : shortId(selected.featureId)

  return (
    <div ref={panelRef} style={positionStyle} className={positionClass}>
      <div
        onPointerDown={handleHeaderPointerDown}
        className="flex cursor-grab items-center justify-between border-b px-3 py-2 select-none active:cursor-grabbing"
      >
        <div className="flex min-w-0 items-center gap-1.5">
          <GripHorizontal className="text-muted-foreground size-3.5 shrink-0" />
          <div className="min-w-0">
            <p className="truncate text-sm font-medium">{primaryLabel}</p>
            <p className="text-muted-foreground truncate font-mono text-xs">{secondaryLabel}</p>
          </div>
        </div>
        {isEditingSelected || creatingDiagram ? null : (
          <div className="flex shrink-0 items-center gap-1">
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <Button
                  variant="ghost"
                  size="icon"
                  className="size-7"
                  disabled={
                    (tracing || isolating) ||
                    (runnableConfigs.length === 0 && !canIsolate && !isolationConfig)
                  }
                  title="Run trace"
                >
                  <Route className="size-4" />
                  <ChevronDown className="size-3" />
                  <span className="sr-only">Run trace</span>
                </Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end" className="w-52">
                {runnableConfigs.length === 0 && !canIsolate && !isolationConfig ? (
                  <p className="text-muted-foreground px-2 py-1.5 text-sm">No trace configurations exist</p>
                ) : (
                  <>
                    {runnableConfigs.map((config) => (
                      <DropdownMenuItem key={config.code} onSelect={() => runTrace(config.code)}>
                        {config.name}
                      </DropdownMenuItem>
                    ))}
                    {isolationConfig && (
                      <>
                        {runnableConfigs.length > 0 && <DropdownMenuSeparator />}
                        <DropdownMenuItem onSelect={runFeederTrace}>Feeder trace</DropdownMenuItem>
                        <DropdownMenuItem onSelect={runSectionTrace}>Section trace</DropdownMenuItem>
                      </>
                    )}
                    {canIsolate && (
                      <>
                        <DropdownMenuSeparator />
                        <DropdownMenuItem onSelect={runIsolate}>
                          <Zap className="size-4" />
                          Isolate from here
                        </DropdownMenuItem>
                      </>
                    )}
                  </>
                )}
              </DropdownMenuContent>
            </DropdownMenu>
            {isDiagramContainer && diagrams.length === 0 && (
              <Button
                variant="ghost"
                size="icon"
                className="size-7"
                title="Create diagram"
                onClick={() => setCreatingDiagram(true)}
              >
                <Workflow className="size-4" />
                <Plus className="size-3" />
                <span className="sr-only">Create diagram</span>
              </Button>
            )}
            {diagrams.length === 1 && (
              <Button
                variant="ghost"
                size="icon"
                className="size-7"
                title="View diagram"
                onClick={() => useDiagramStore.getState().open(diagrams[0].id)}
              >
                <Workflow className="size-4" />
                <span className="sr-only">View diagram</span>
              </Button>
            )}
            {diagrams.length > 1 && (
              <DropdownMenu>
                <DropdownMenuTrigger asChild>
                  <Button variant="ghost" size="icon" className="size-7" title="View diagram">
                    <Workflow className="size-4" />
                    <ChevronDown className="size-3" />
                    <span className="sr-only">View diagram</span>
                  </Button>
                </DropdownMenuTrigger>
                <DropdownMenuContent align="end" className="w-52">
                  {diagrams.map((d) => (
                    <DropdownMenuItem key={d.id} onSelect={() => useDiagramStore.getState().open(d.id)}>
                      {d.name || d.diagram_type}
                    </DropdownMenuItem>
                  ))}
                </DropdownMenuContent>
              </DropdownMenu>
            )}
            <div className="bg-border mx-0.5 h-4 w-px" />
            <Button
              variant="ghost"
              size="icon"
              className="size-7"
              disabled={Boolean(feature?.deleted_at)}
              onClick={() => useEditStore.getState().start(selected.featureClassCode, selected.featureId)}
            >
              <Pencil className="size-4" />
              <span className="sr-only">Edit</span>
            </Button>
            <Button
              variant="ghost"
              size="icon"
              className="size-7"
              disabled={Boolean(feature?.deleted_at)}
              onClick={() => setDeleteDialogOpen(true)}
            >
              <Trash2 className="size-4" />
              <span className="sr-only">Delete</span>
            </Button>
            <Button variant="ghost" size="icon" className="size-7" onClick={clear}>
              <X className="size-4" />
              <span className="sr-only">Close</span>
            </Button>
          </div>
        )}
      </div>

      <DeleteFeatureDialog
        featureClassCode={selected.featureClassCode}
        featureId={selected.featureId}
        className={className}
        open={deleteDialogOpen}
        onOpenChange={setDeleteDialogOpen}
      />

      <div className="flex min-h-0 flex-1">
        <div className="flex w-28 shrink-0 flex-col gap-0.5 border-r p-1">
          {TABS.map((tab) => {
            // Terminals/Relationships unmount when the active tab
            // changes (see below) -- switching away mid-edit would
            // silently drop whatever's typed into the Attributes tab's
            // edit form, so those two are locked until Save or Cancel
            // ends the edit session. Every tab locks while
            // creatingDiagram too -- CreateDiagramForm takes over the
            // whole content pane below, same reasoning.
            const locked = (isEditingSelected && tab.id !== "attributes") || creatingDiagram
            return (
              <button
                key={tab.id}
                type="button"
                disabled={locked}
                onClick={() => setActiveTab(tab.id)}
                className={cn(
                  "flex items-center gap-2 rounded-sm px-2 py-1.5 text-left text-xs font-medium",
                  locked && "cursor-not-allowed opacity-40",
                  !locked && activeTab === tab.id && "bg-accent text-accent-foreground",
                  !locked && activeTab !== tab.id && "text-muted-foreground hover:bg-accent/50",
                )}
              >
                <tab.icon className="size-3.5 shrink-0" />
                {tab.label}
              </button>
            )
          })}
        </div>
        <div className="flex-1 overflow-y-auto p-3">
          {creatingDiagram ? (
            <CreateDiagramForm
              featureClassCode={selected.featureClassCode}
              featureId={selected.featureId}
              onDone={() => setCreatingDiagram(false)}
            />
          ) : (
            <>
              {activeTab === "attributes" && <AttributesTab />}
              {activeTab === "terminals" && <TerminalsTab />}
              {activeTab === "relationships" && <RelationshipsTab />}
              {activeTab === "rules" && <RulesTab />}
            </>
          )}
        </div>
      </div>
    </div>
  )
}
