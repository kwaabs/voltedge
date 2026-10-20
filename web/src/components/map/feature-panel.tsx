import { Fragment, useEffect, useRef, useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
import {
  Activity,
  BatteryCharging,
  BatteryFull,
  ChartSpline,
  CheckCircle2,
  ClipboardList,
  ChevronDown,
  ChevronsDownUp,
  ChevronsUpDown,
  Cloud,
  Copy,
  ExternalLink as ExternalLinkIcon,
  FileClock,
  Gauge,
  GripHorizontal,
  History,
  Info,
  LandPlot,
  Link2,
  Loader2,
  Lock,
  MoreHorizontal,
  Network,
  Paperclip,
  Pencil,
  Plug,
  Plus,
  QrCode,
  RefreshCw,
  Route,
  Satellite,
  ShieldAlert,
  Shuffle,
  SlidersHorizontal,
  Spline,
  Sun,
  Crosshair,
  Thermometer,
  Trash2,
  TrendingDown,
  TriangleAlert,
  Undo2,
  Unlink2,
  Wand2,
  Waves,
  Waypoints,
  TrendingUp,
  Workflow,
  Wrench,
  X,
  Zap,
  ZapOff,
} from "lucide-react"

import { CreateDiagramForm } from "@/components/map/create-diagram-form"
import { DeleteFeatureDialog } from "@/components/map/delete-feature-dialog"
import { FeatureAttachments } from "@/components/map/feature-attachments"
import { FeatureCreateForm } from "@/components/map/feature-create-form"
import { FeatureEditForm } from "@/components/map/feature-edit-form"
import { FaultsTab } from "@/components/map/feature-faults-tab"
import { OperationsTab } from "@/components/map/feature-operations-tab"
import { SafetyTab } from "@/components/map/feature-safety-tab"
import { SpatialTab } from "@/components/map/feature-spatial-tab"
import { TagsTab } from "@/components/map/feature-tags-tab"
import { WorksTab } from "@/components/map/feature-works-tab"
import { formatFieldLabel, formatValue, inputClass } from "@/components/map/field-utils"
import { SetCircuitSourceDialog } from "@/components/map/set-circuit-source-dialog"
import { RevokeConnectionDialog } from "@/components/map/revoke-connection-dialog"
import { RevokeRelationshipDialog } from "@/components/map/revoke-relationship-dialog"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuCheckboxItem,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuSub,
  DropdownMenuSubContent,
  DropdownMenuSubTrigger,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { circuitNameMap, useCircuits } from "@/hooks/use-circuits"
import {
  useDeleteFeatureCostOverride,
  useFeatureCostOverrides,
  useUpsertFeatureCostOverride,
} from "@/hooks/use-cost-catalog"
import { type Connection, useConnections } from "@/hooks/use-connections"
import { useDiagramInstances } from "@/hooks/use-diagrams"
import { useDuctCapacity } from "@/hooks/use-duct-capacity"
import { useFeature } from "@/hooks/use-feature"
import { type FeatureGpsCapture, useFeatureGpsCaptures } from "@/hooks/use-feature-gps-captures"
import { type FeatureHistoryEntry, useFeatureHistory } from "@/hooks/use-feature-history"
import { useFeatureClassSchema } from "@/hooks/use-feature-class-schema"
import { useFeatureClasses } from "@/hooks/use-feature-classes"
import { usePermission } from "@/hooks/use-auth"
import { useMarkDownstreamPrivate, useResyncOwnership } from "@/hooks/use-ownership"
import {
  type ExternalLink,
  type ExternalRefEntry,
  useCreateExternalLink,
  useDeleteExternalLink,
  useExternalLinkData,
  useExternalLinks,
  useFeatureExternalData,
  useFeatureExternalRefs,
  useSetFeatureExternalRefs,
} from "@/hooks/use-external-links"
import { useExternalSystems } from "@/hooks/use-external-systems"
import { useApplyPendingEdit, useDiscardPendingEdit } from "@/hooks/use-pending-edits"
import { useRelationships, type Relationship } from "@/hooks/use-relationships"
import { useTerritories, territoryNameMap } from "@/hooks/use-territories"
import { useTraceConfigurations } from "@/hooks/use-trace-configurations"
import { ApiError, apiFetch } from "@/lib/api"
import { offsetGeometry } from "@/lib/geometry"
import { formatArea, formatLength, geoJSONAreaMeters, geoJSONLengthMeters } from "@/lib/measure"
import { cn } from "@/lib/utils"
import { useAmpacityStore } from "@/stores/ampacity-store"
import { useConnectStore } from "@/stores/connect-store"
import { useCoordinationStore } from "@/stores/coordination-store"
import { useDiagramStore } from "@/stores/diagram-store"
import { useDrawStore } from "@/stores/draw-store"
import { useEditStore } from "@/stores/edit-store"
import { useOneLineStore } from "@/stores/one-line-store"
import { useFaultCurrentStore } from "@/stores/fault-current-store"
import { useFaultLocationStore } from "@/stores/fault-location-store"
import { type IsolationResult, useIsolateStore } from "@/stores/isolate-store"
import { useLoadFlowStore } from "@/stores/load-flow-store"
import { useSelectionStore } from "@/stores/selection-store"
import { useTracePathStore } from "@/stores/trace-path-store"
import { useErrorToast, useToastStore } from "@/stores/toast-store"
import {
  type ServiceTraceResult,
  type TraceFeatureRef,
  type TracedFeature,
  type TraceResult,
  useTraceStore,
} from "@/stores/trace-store"
import { useUnitsStore } from "@/stores/units-store"
import { useVoltageProfileStore } from "@/stores/voltage-profile-store"
import { useContingencyStore } from "@/stores/contingency-store"
import { useHostingStore } from "@/stores/hosting-store"
import { useEvHostingStore } from "@/stores/ev-hosting-store"
import { useLoadGrowthStore } from "@/stores/load-growth-store"
import { useMeshedLoadFlowStore } from "@/stores/meshed-load-flow-store"
import { useNodalLoadFlowStore } from "@/stores/nodal-load-flow-store"
import { useAutoLoadFlowStore } from "@/stores/auto-load-flow-store"
import { useStorageDispatchStore } from "@/stores/storage-dispatch-store"
import { useVvoStore } from "@/stores/vvo-store"
import { useTapOptimizationStore } from "@/stores/tap-optimization-store"
import { useUnbalancedLoadFlowStore } from "@/stores/unbalanced-load-flow-store"
import { useVoltVarStore } from "@/stores/volt-var-store"

type TabId =
  | "attributes"
  | "terminals"
  | "relationships"
  | "materials"
  | "works"
  | "faults"
  | "operations"
  | "safety"
  | "spatial"
  | "tags"
  | "files"
  | "links"
  | "history"
  | "gps"

const TABS: { id: TabId; label: string; icon: typeof Info }[] = [
  { id: "attributes", label: "Attributes", icon: Info },
  { id: "terminals", label: "Terminals", icon: Plug },
  { id: "relationships", label: "Relationships", icon: Link2 },
  { id: "materials", label: "Materials", icon: Wrench },
  { id: "works", label: "Works", icon: ClipboardList },
  { id: "faults", label: "Faults", icon: ZapOff },
  { id: "operations", label: "Operations", icon: Activity },
  { id: "safety", label: "Safety", icon: ShieldAlert },
  { id: "spatial", label: "Land use", icon: LandPlot },
  { id: "tags", label: "Tags", icon: QrCode },
  { id: "files", label: "Files", icon: Paperclip },
  { id: "links", label: "Links", icon: ExternalLinkIcon },
  { id: "history", label: "History", icon: History },
  { id: "gps", label: "GPS", icon: Satellite },
]

function shortId(id: string): string {
  return id.length > 8 ? `${id.slice(0, 8)}…` : id
}

/**
 * Snapshots the current load-flow result's per-node voltages, keyed
 * `feature_class:feature_id`, but only when that result was run from the same
 * start a trace is about to run from -- so the trace table can overlay a
 * *computed* voltage next to each reached feature's nominal one. Returns null
 * when no load-flow result exists, or one exists for a different start (its
 * voltages wouldn't describe this trace's tree). Read at trace time and handed
 * to trace-store because running a trace clears the load-flow store right after
 * -- the two workflows share the same map/panel real estate and stay mutually
 * exclusive, so the snapshot carries the computed figures forward rather than
 * keeping the load-flow result live. The trace *engine* never computes these;
 * this client-side join is the only place computed voltages meet a trace.
 */
function snapshotComputedVoltages(start: TraceFeatureRef): Record<string, number> | null {
  const lf = useLoadFlowStore.getState().result
  if (!lf || lf.start.feature_class !== start.feature_class || lf.start.feature_id !== start.feature_id) {
    return null
  }
  return Object.fromEntries(lf.nodes.map((n) => [`${n.feature_class}:${n.feature_id}`, n.voltage_v]))
}

function AttributesTab() {
  const selected = useSelectionStore((s) => s.selected)
  const editing = useEditStore((s) => s.editing)
  const units = useUnitsStore((s) => s.units)
  const queryClient = useQueryClient()
  // features:write is the same permission the /publish + /unpublish POSTs
  // require (the method-aware /features mount gate) -- an editor who lacks it
  // never sees the control, rather than being offered an action the server
  // would reject. Publishing is the review/sign-off step of the draft ->
  // published axis (docs/phase-38-operational-state.md, "Follow-on").
  const canPublish = usePermission()("features:write")
  const [publishing, setPublishing] = useState(false)
  const { data: feature, isPending, isError, error } = useFeature(
    selected?.featureClassCode,
    selected?.featureId,
  )
  useErrorToast(isError, error, "Could not load this feature")

  // Private-drafts Phase B: apply/discard the acting user's private, unpublished
  // edit of this already-published feature. Only rendered when the server's read
  // overlay marks the feature pending_private_edit (see the banner below); the
  // mutations refresh the feature, the row list and the map tiles.
  const applyPending = useApplyPendingEdit()
  const discardPending = useDiscardPendingEdit()
  const pendingBusy = applyPending.isPending || discardPending.isPending
  function handleApplyPending() {
    if (!selected || pendingBusy) return
    applyPending.mutate(
      { featureClassCode: selected.featureClassCode, featureId: selected.featureId },
      {
        // A 409 is the stale-base conflict: someone else published a change since
        // this edit was staged. Surface the server's discard-and-redo message.
        onError: (err) =>
          useToastStore.getState().pushError(err.message || "Could not apply your private edit."),
      },
    )
  }
  function handleDiscardPending() {
    if (!selected || pendingBusy) return
    discardPending.mutate(
      { featureClassCode: selected.featureClassCode, featureId: selected.featureId },
      {
        onError: (err) =>
          useToastStore.getState().pushError(err.message || "Could not discard your private edit."),
      },
    )
  }
  // Resolve the feature's auto-assigned territory (Phase 11) to a name.
  // Fixed reference data, cached long -- no picker, read-only.
  const { data: territoriesData } = useTerritories()
  const territoryNames = territoryNameMap(territoriesData?.territories)
  // The class schema resolves the raw subtype code (e.g. 2) to its display
  // name for the read-only row below -- the same list the edit form's subtype
  // picker uses. Already cached from opening the form; the panel just reads it.
  const { data: subtypeSchema } = useFeatureClassSchema(selected?.featureClassCode)
  // Resolve a circuit_id attribute (a gis.circuits uuid, Phase 37) to the
  // circuit's name -- same reference-data resolution as territory above, so the
  // panel shows "Old Age 2" rather than a raw uuid. circuit_ref, when set, is
  // already the plain name and needs no lookup.
  const { data: circuitsData } = useCircuits()
  const circuitNames = circuitNameMap(circuitsData?.circuits)

  // Coarse spare-way capacity for a duct-like class (Phase 44 Slice 3). Enabled
  // ONLY when the loaded feature actually carries a way_count field, so the
  // generic /capacity endpoint (which 400s for a class without one) is never hit
  // for anything but a CABLE_DUCT. Rendered as its own read-only row below.
  const hasWayCount = Boolean(feature && feature.attributes && "way_count" in feature.attributes)
  const { data: ductCapacity } = useDuctCapacity(
    selected?.featureClassCode,
    selected?.featureId,
    hasWayCount,
  )

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

  // A hairline under every cell turns the flat key/value grid into
  // legible, table-like rows -- both cells of a row share the same grid
  // line, so their borders align into one continuous rule. `py-1.5`
  // replaces the old `gap-y` so those rules butt up against each other
  // with no gap breaking them; the last row keeps its border too (reads
  // as the list's closing rule, and a flat Fragment grid has no clean
  // "last row" to special-case anyway).
  const dtClass = "text-muted-foreground border-primary/15 border-b py-1.5 font-medium"
  const ddClass = "text-foreground border-primary/15 border-b py-1.5"

  // Length/area computed live from the feature's own geometry (no stored
  // column, no measure tool to arm) -- a line gets a Length row, a
  // polygon an Area + Perimeter pair. Geodesic (see lib/measure.ts), so
  // correct regardless of the class's SRID; re-formats instantly when the
  // shared units preference flips.
  const geometry = feature.geometry
  const lengthMeters = geometry ? geoJSONLengthMeters(geometry) : null
  const areaMeters = geometry ? geoJSONAreaMeters(geometry) : null

  // Raw subtype code -> display name (feature is defined past the guard above);
  // falls back to the code if the schema is still loading or has no match.
  const subtypeName =
    feature.subtype !== undefined
      ? subtypeSchema?.subtypes.find((s) => s.code === feature.subtype)?.name
      : undefined

  // Resolve a coded field's stored code to its human domain label (e.g.
  // height_class "CLASS_1" -> "Class 1 (9 m)"), so the read-only panel shows
  // the admin-set label rather than the raw code. Returns undefined for a
  // non-coded field or an unknown code, so the caller falls back to formatValue.
  const domainLabel = (fieldName: string, value: unknown): string | undefined =>
    typeof value === "string"
      ? subtypeSchema?.fields.find((f) => f.name === fieldName)?.domain_value_labels?.[value]
      : undefined

  // Show attributes in the admin-set field order (the schema's fields come back
  // in sort_order -- migration 0146), not the raw attribute-object key order, so
  // the read-only panel matches the create/edit forms. A field not in the schema
  // (loading, or a stray attribute) sorts after the known ones, keeping its
  // original relative position.
  const fieldOrder = new Map((subtypeSchema?.fields ?? []).map((f, i) => [f.name, i]))
  const orderedAttributes = Object.entries(feature.attributes).sort(([a], [b]) => {
    const ia = fieldOrder.get(a) ?? Number.MAX_SAFE_INTEGER
    const ib = fieldOrder.get(b) ?? Number.MAX_SAFE_INTEGER
    return ia - ib
  })

  // Move the feature between draft and published -- the review/sign-off axis.
  // status is the publish system column, not a metadata attribute, so it's
  // deliberately absent from the edit form; this inline control is its home.
  // Publish is gated server-side on completeness (a draft missing a
  // required-by-subtype field 400s, surfaced verbatim as the toast below);
  // unpublish is ungated. On success we re-read the feature so this Status row
  // and the edit form's post-publish field locks both refresh off the same
  // useFeature cache. No map refresh -- the vector tiles don't style by status.
  async function handleSetPublished(publish: boolean) {
    if (!selected || publishing) return
    setPublishing(true)
    try {
      await apiFetch(
        `/features/${selected.featureClassCode}/${selected.featureId}/${publish ? "publish" : "unpublish"}`,
        { method: "POST" },
      )
      queryClient.invalidateQueries({ queryKey: ["feature", selected.featureClassCode, selected.featureId] })
    } catch (err) {
      useToastStore.getState().pushError(
        err instanceof ApiError
          ? err.message
          : publish
            ? "Could not publish this feature."
            : "Could not unpublish this feature.",
      )
    } finally {
      setPublishing(false)
    }
  }

  const isPublished = feature.status === "published"
  // The control is hidden (not just disabled) without features:write or on a
  // soft-deleted feature -- the same guard Duplicate/Delete use in the header.
  const showPublishControl = canPublish && !feature.deleted_at

  return (
    <>
      {/* Private-drafts Phase B: this feature is public to everyone else, but
          the values shown here are the acting user's own private, unpublished
          edit (or a pending private retirement). Apply publishes it for
          everyone; Discard throws it away and reveals the public row again. */}
      {feature.pending_private_edit && (
        <div className="border-primary/30 bg-accent/40 mb-3 rounded-md border px-3 py-2 text-xs">
          <div className="flex items-center gap-1.5 font-medium">
            <FileClock className="size-3.5" />
            {feature.pending_private_delete ? "Private deletion pending" : "Private edit pending"}
          </div>
          <p className="text-muted-foreground mt-1">
            {feature.pending_private_delete
              ? "You've privately retired this feature. It's still live for everyone else until you apply the deletion."
              : "These are your private, unpublished changes. Only you can see them until you apply them."}
          </p>
          {canPublish && (
            <div className="mt-2 flex gap-2">
              <Button
                size="sm"
                className="h-6 gap-1 px-2 text-xs"
                disabled={pendingBusy}
                onClick={handleApplyPending}
                title="Publish your private change onto the public feature"
              >
                {applyPending.isPending ? (
                  <Loader2 className="size-3 animate-spin" />
                ) : (
                  <CheckCircle2 className="size-3" />
                )}
                Apply
              </Button>
              <Button
                variant="outline"
                size="sm"
                className="h-6 gap-1 px-2 text-xs"
                disabled={pendingBusy}
                onClick={handleDiscardPending}
                title="Throw away your private change and show the public feature"
              >
                {discardPending.isPending ? (
                  <Loader2 className="size-3 animate-spin" />
                ) : (
                  <Undo2 className="size-3" />
                )}
                Discard
              </Button>
            </div>
          )}
        </div>
      )}
      <dl className="grid grid-cols-[max-content_1fr] gap-x-6 text-sm">
      <dt className={dtClass}>Status</dt>
      <dd className={ddClass}>
        <div className="flex items-center justify-between gap-2">
          <Badge variant={isPublished ? "success" : "secondary"} className="capitalize">
            {feature.status}
          </Badge>
          {showPublishControl && (
            <Button
              variant="outline"
              size="sm"
              className="h-6 gap-1 px-2 text-xs"
              disabled={publishing}
              title={isPublished ? "Return this feature to draft" : "Publish this feature (requires all required fields)"}
              onClick={() => handleSetPublished(!isPublished)}
            >
              {publishing ? (
                <Loader2 className="size-3 animate-spin" />
              ) : isPublished ? (
                <Undo2 className="size-3" />
              ) : (
                <CheckCircle2 className="size-3" />
              )}
              {isPublished ? "Unpublish" : "Publish"}
            </Button>
          )}
        </div>
      </dd>

      {feature.deleted_at && (
        <>
          <dt className={dtClass}>Deleted</dt>
          <dd className={ddClass}>{new Date(feature.deleted_at).toLocaleString()}</dd>
        </>
      )}

      {feature.subtype !== undefined && (
        <>
          <dt className={dtClass}>Subtype</dt>
          {/* Show the resolved subtype name; fall back to the raw code while
              the schema loads or if the code has no matching subtype. */}
          <dd className={ddClass}>{subtypeName ?? feature.subtype}</dd>
        </>
      )}

      {orderedAttributes.map(([key, value]) => (
        <Fragment key={key}>
          <dt className={dtClass}>{formatFieldLabel(key)}</dt>
          <dd className={ddClass}>
            {/* circuit_id is a uuid reference -- show the circuit's name, not
                the raw id (falling back to the id if the catalog hasn't
                loaded or the circuit is gone). Every other attribute formats
                by its own value. */}
            {key === "circuit_id" && typeof value === "string"
              ? (circuitNames[value] ?? formatValue(value))
              : (domainLabel(key, value) ?? formatValue(value))}
          </dd>
        </Fragment>
      ))}

      {ductCapacity && (
        <>
          <dt className={dtClass}>Spare ways</dt>
          <dd className={ddClass}>
            {ductCapacity.spare} of {ductCapacity.way_count} free
            <span className="text-muted-foreground ml-1.5 text-xs">
              ({ductCapacity.occupied} cable{ductCapacity.occupied === 1 ? "" : "s"} routed
              {ductCapacity.per_way ? ", per way" : ""})
            </span>
            {/* Per-bore breakdown (Phase 44 Slice 4): once a duct is fanned out
                into DUCT_WAY children the endpoint returns each way's occupancy,
                so show a chip per bore -- filled = a cable is routed through it,
                outlined = free. Absent for a coarse (un-fanned-out) duct. */}
            {ductCapacity.per_way && ductCapacity.ways && ductCapacity.ways.length > 0 && (
              <div className="mt-1.5 flex flex-wrap gap-1">
                {ductCapacity.ways.map((way) => (
                  <span
                    key={way.feature_id}
                    title={way.occupied ? `Way ${way.way_number}: cable routed` : `Way ${way.way_number}: free`}
                    className={
                      way.occupied
                        ? "bg-primary/80 text-primary-foreground inline-flex size-5 items-center justify-center rounded text-[10px] font-medium"
                        : "border-muted-foreground/40 text-muted-foreground inline-flex size-5 items-center justify-center rounded border text-[10px] font-medium"
                    }
                  >
                    {way.way_number}
                  </span>
                ))}
              </div>
            )}
          </dd>
        </>
      )}

      {geometry?.type === "LineString" && lengthMeters !== null && (
        <>
          <dt className={dtClass}>Length</dt>
          <dd className={ddClass}>
            {formatLength(lengthMeters, units)}
            <span className="text-muted-foreground ml-1.5 text-xs">(calculated)</span>
          </dd>
        </>
      )}

      {geometry?.type === "Polygon" && (
        <>
          {areaMeters !== null && (
            <>
              <dt className={dtClass}>Area</dt>
              <dd className={ddClass}>
                {formatArea(areaMeters, units)}
                <span className="text-muted-foreground ml-1.5 text-xs">(calculated)</span>
              </dd>
            </>
          )}
          {lengthMeters !== null && (
            <>
              <dt className={dtClass}>Perimeter</dt>
              <dd className={ddClass}>
                {formatLength(lengthMeters, units)}
                <span className="text-muted-foreground ml-1.5 text-xs">(calculated)</span>
              </dd>
            </>
          )}
        </>
      )}

      {feature.territory_id !== undefined && (
        <>
          <dt className={dtClass}>Territory</dt>
          <dd className={ddClass}>
            {feature.territory_id ? (
              territoryNames[feature.territory_id] || shortId(feature.territory_id)
            ) : (
              <span className="text-muted-foreground">Unassigned</span>
            )}
          </dd>
        </>
      )}

      <dt className={dtClass}>Updated</dt>
      <dd className={ddClass}>{new Date(feature.updated_at).toLocaleString()}</dd>
      </dl>
      <OwnershipSection feature={feature} />
    </>
  )
}

/**
 * Asset-ownership STATUS chip (api/internal/ownership). Ownership is utility by
 * default, so this shows a quiet "Privately owned" badge ONLY on a feature that
 * is actually private -- nothing on the utility 99%. The ACTIONS that set it
 * (mark downstream private / re-sync) live in the feature's actions dropdown,
 * shown only on a transformer (the boundary where ownership is declared).
 */
function OwnershipSection({ feature }: { feature: { attributes?: Record<string, unknown> | null } }) {
  if (!feature.attributes || feature.attributes.ownership !== "private") return null
  return (
    <div className="mt-3 flex items-center gap-2 text-sm">
      <span className="text-muted-foreground">Ownership</span>
      <Badge variant="secondary" title="Part of a privately-owned network (declared at its transformer)">
        Privately owned
      </Badge>
    </div>
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
  // The peeked counterpart (a row clicked to highlight it on the map without
  // opening its panel -- the row's info button does that). Drives the active
  // row styling below; cleared when this tab unmounts so no halo lingers.
  const located = useSelectionStore((s) => s.located)
  useEffect(() => () => useSelectionStore.getState().clearLocated(), [])
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
          // The terminal on the viewed feature's side. The dedicated
          // terminal field is authoritative, but the seeded network rules
          // also bake it onto the end of the rule name (e.g. "... T1"), so
          // fall back to parsing that suffix and always strip it from the
          // displayed rule line -- the badge below is the single place it shows.
          const ruleTerminalMatch = conn.network_rule_name.match(/\s+(T\d+)\s*$/i)
          const thisTerminal = (isFrom ? conn.from.terminal : conn.to.terminal) || ruleTerminalMatch?.[1] || null
          const ruleLabel = ruleTerminalMatch
            ? conn.network_rule_name.slice(0, ruleTerminalMatch.index).trimEnd()
            : conn.network_rule_name
          const counterpartClassName =
            featureClasses?.feature_classes.find((fc) => fc.code === counterpart.feature_class)?.name ??
            counterpart.feature_class
          const counterpartFeatureName = counterpartNames[`${counterpart.feature_class}:${counterpart.feature_id}`]
          const counterpartPrimary = counterpartFeatureName || counterpartClassName
          const counterpartSecondary = counterpartFeatureName
            ? `${counterpartClassName} · ${shortId(counterpart.feature_id)}`
            : shortId(counterpart.feature_id)

          const isLocated =
            located?.featureClassCode === counterpart.feature_class &&
            located?.featureId === counterpart.feature_id

          return (
            <div
              key={conn.id}
              className={cn(
                "hover:bg-accent hover:text-accent-foreground group flex items-start gap-1 rounded-md border p-2 text-sm transition-colors",
                isLocated && "border-emerald-500 ring-1 ring-emerald-500",
              )}
            >
              {/* Clicking the row LOCATES (highlights on the map) the connected
                  feature without opening its panel; the info button below opens
                  the panel. */}
              <button
                type="button"
                onClick={() =>
                  useSelectionStore.getState().locate(counterpart.feature_class, counterpart.feature_id)
                }
                className="min-w-0 flex-1 text-left"
              >
                <div className="flex items-center justify-between gap-2">
                  <span className="font-medium">{counterpartPrimary}</span>
                  <span className="text-muted-foreground shrink-0 text-xs">{isFrom ? "Outgoing" : "Incoming"}</span>
                </div>
                <p className="text-muted-foreground font-mono text-xs">{counterpartSecondary}</p>
                <p className="text-muted-foreground mt-1 text-xs">{ruleLabel}</p>
              </button>
              <button
                type="button"
                onClick={() =>
                  useSelectionStore.getState().select(counterpart.feature_class, counterpart.feature_id, {
                    focus: true,
                  })
                }
                className="text-muted-foreground hover:bg-accent hover:text-foreground shrink-0 rounded p-1"
                title="Open this feature's panel"
              >
                <Info className="size-3.5" />
                <span className="sr-only">Open feature panel</span>
              </button>
              <button
                type="button"
                onClick={() => setRevokingConnection(conn)}
                className="text-muted-foreground hover:bg-destructive/10 hover:text-destructive shrink-0 rounded p-1"
              >
                <Unlink2 className="size-3.5" />
                <span className="sr-only">Revoke connection</span>
              </button>
              {/* The terminal this connection lands on (T1/T2 on the viewed
                  feature) -- pinned to the card's extreme right edge, after the
                  action buttons, in a big badge so it reads at a glance. */}
              {thisTerminal && (
                <span
                  className="text-foreground shrink-0 self-center pl-1 text-lg font-semibold tabular-nums"
                  title={`Terminal ${thisTerminal}`}
                >
                  {thisTerminal}
                </span>
              )}
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
  // Same peek-vs-open split as TerminalsTab -- clicking a row locates the
  // counterpart on the map, the info button opens its panel.
  const located = useSelectionStore((s) => s.located)
  useEffect(() => () => useSelectionStore.getState().clearLocated(), [])
  const { data: featureClasses } = useFeatureClasses()
  const { data, isPending, isError, error } = useRelationships(
    selected?.featureClassCode,
    selected?.featureId,
  )
  useErrorToast(isError, error, "Could not load relationships")
  const [revokingRelationship, setRevokingRelationship] = useState<Relationship | null>(null)
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

  // Kept mounted across every branch below -- a successful revoke can flip
  // data.relationships from non-empty to empty (switching which branch renders)
  // while the dialog is still closing itself. Same reasoning as TerminalsTab.
  const revokeDialog = (
    <RevokeRelationshipDialog
      relationship={revokingRelationship}
      onOpenChange={(open) => !open && setRevokingRelationship(null)}
    />
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
        {revokeDialog}
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

          const isLocated =
            located?.featureClassCode === counterpart.feature_class &&
            located?.featureId === counterpart.feature_id

          return (
            <div
              key={rel.id}
              className={cn(
                "hover:bg-accent hover:text-accent-foreground group flex items-start gap-1 rounded-md border p-2 text-sm transition-colors",
                isLocated && "border-emerald-500 ring-1 ring-emerald-500",
              )}
            >
              {/* Row locates on the map; info button opens the panel -- see TerminalsTab. */}
              <button
                type="button"
                onClick={() => useSelectionStore.getState().locate(counterpart.feature_class, counterpart.feature_id)}
                className="min-w-0 flex-1 text-left"
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
              <button
                type="button"
                onClick={() =>
                  useSelectionStore.getState().select(counterpart.feature_class, counterpart.feature_id, { focus: true })
                }
                className="text-muted-foreground hover:bg-accent hover:text-foreground shrink-0 rounded p-1"
                title="Open this feature's panel"
              >
                <Info className="size-3.5" />
                <span className="sr-only">Open feature panel</span>
              </button>
              <button
                type="button"
                onClick={() => setRevokingRelationship(rel)}
                className="text-muted-foreground hover:bg-destructive/10 hover:text-destructive shrink-0 rounded p-1"
              >
                <Unlink2 className="size-3.5" />
                <span className="sr-only">Revoke relationship</span>
              </button>
            </div>
          )
        })}
      </div>
      {revokeDialog}
    </div>
  )
}

/**
 * Outbound links from this feature to external systems (Phase 32). Slice
 * 1 is plain hyperlinks: a label + an http/https url the row opens in a
 * new tab (rel="noopener noreferrer" -- the external page never gets a
 * handle back to this window). A feature can hold arbitrarily many, so
 * this is a plain list with an inline add row and a per-row remove, the
 * same add/list/remove shape the Files tab uses.
 */
function LinksTab({ featureClassCode, featureId }: { featureClassCode: string; featureId: string }) {
  const { data, isPending, isError, error } = useExternalLinks(featureClassCode, featureId)
  useErrorToast(isError, error, "Could not load links")
  const createLink = useCreateExternalLink(featureClassCode, featureId)
  const deleteLink = useDeleteExternalLink(featureClassCode, featureId)

  // Building an api link means picking a catalogued external system, which
  // lives behind metadata:read. A feature editor who lacks it can still
  // add hyperlinks and view existing api links' data (that rides
  // features:read) -- they just can't create new api links, so the option
  // is hidden rather than offered and rejected.
  const canReadSystems = usePermission()("metadata:read")
  const systemsQuery = useExternalSystems(canReadSystems)
  const systems = systemsQuery.data?.external_systems ?? []
  const canAddApi = canReadSystems && systems.length > 0

  const [adding, setAdding] = useState(false)
  const [kind, setKind] = useState<"hyperlink" | "api">("hyperlink")
  const [label, setLabel] = useState("")
  const [url, setUrl] = useState("")
  const [systemId, setSystemId] = useState("")
  const [resourcePath, setResourcePath] = useState("")

  const reset = () => {
    setAdding(false)
    setKind("hyperlink")
    setLabel("")
    setUrl("")
    setSystemId("")
    setResourcePath("")
  }

  const canSave =
    !createLink.isPending &&
    label.trim() !== "" &&
    (kind === "hyperlink" ? url.trim() !== "" : systemId !== "")

  const handleAdd = () => {
    if (!canSave) return
    const input =
      kind === "api"
        ? { kind: "api" as const, label: label.trim(), system_id: systemId, resource_path: resourcePath.trim() }
        : { label: label.trim(), url: url.trim() }
    createLink.mutate(input, {
      onSuccess: reset,
      onError: (err) =>
        useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not add this link."),
    })
  }

  const removeLink = (id: string) =>
    deleteLink.mutate(id, {
      onError: (err) =>
        useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not remove this link."),
    })

  const inputClass = "border-input bg-background w-full rounded-sm border px-2 py-1 text-sm"

  return (
    <div className="flex flex-col items-start gap-2">
      {adding ? (
        <div className="flex w-full flex-col gap-2 rounded-md border p-2">
          {canAddApi && (
            <div className="flex gap-1">
              <Button
                variant={kind === "hyperlink" ? "default" : "outline"}
                size="sm"
                className="h-7 flex-1"
                onClick={() => setKind("hyperlink")}
              >
                <ExternalLinkIcon className="size-3.5" />
                Hyperlink
              </Button>
              <Button
                variant={kind === "api" ? "default" : "outline"}
                size="sm"
                className="h-7 flex-1"
                onClick={() => setKind("api")}
              >
                <Cloud className="size-3.5" />
                Live data
              </Button>
            </div>
          )}
          <input
            autoFocus
            value={label}
            onChange={(e) => setLabel(e.target.value)}
            placeholder="Label (e.g. Billing account)"
            className={inputClass}
          />
          {kind === "hyperlink" ? (
            <input
              value={url}
              onChange={(e) => setUrl(e.target.value)}
              onKeyDown={(e) => e.key === "Enter" && handleAdd()}
              placeholder="https://..."
              className={cn(inputClass, "font-mono text-xs")}
            />
          ) : (
            <>
              <select value={systemId} onChange={(e) => setSystemId(e.target.value)} className={inputClass}>
                <option value="">Select a system...</option>
                {systems.map((sys) => (
                  <option key={sys.id} value={sys.id}>
                    {sys.name} ({sys.code})
                  </option>
                ))}
              </select>
              <input
                value={resourcePath}
                onChange={(e) => setResourcePath(e.target.value)}
                onKeyDown={(e) => e.key === "Enter" && handleAdd()}
                placeholder="Resource path (e.g. accounts/123)"
                className={cn(inputClass, "font-mono text-xs")}
              />
            </>
          )}
          <div className="flex items-center gap-2">
            <Button size="sm" onClick={handleAdd} disabled={!canSave}>
              {createLink.isPending ? <Loader2 className="size-4 animate-spin" /> : <Plus className="size-4" />}
              Add link
            </Button>
            <Button variant="ghost" size="sm" onClick={reset}>
              Cancel
            </Button>
          </div>
        </div>
      ) : (
        <Button variant="outline" size="sm" className="self-start" onClick={() => setAdding(true)}>
          <Plus className="size-4" />
          Add link
        </Button>
      )}

      {isPending ? (
        <div className="text-muted-foreground flex items-center gap-2 text-sm">
          <Loader2 className="size-4 animate-spin" />
          Loading links...
        </div>
      ) : isError ? null : !data || data.links.length === 0 ? (
        <p className="text-muted-foreground text-sm">No links.</p>
      ) : (
        <div className="flex w-full flex-col gap-2">
          {data.links.map((link) =>
            link.kind === "api" ? (
              <ApiLinkRow key={link.id} link={link} onRemove={() => removeLink(link.id)} />
            ) : (
              <div
                key={link.id}
                className="group hover:bg-accent/50 flex w-full items-center gap-2 rounded-md border p-2 text-sm transition-colors"
              >
                <a
                  href={link.url}
                  target="_blank"
                  rel="noopener noreferrer"
                  className="flex min-w-0 flex-1 flex-col"
                  title={link.url}
                >
                  <span className="text-primary flex items-center gap-1 font-medium hover:underline">
                    <span className="truncate">{link.label}</span>
                    <ExternalLinkIcon className="size-3 shrink-0" />
                  </span>
                  <span className="text-muted-foreground truncate font-mono text-xs">{link.url}</span>
                </a>
                <button
                  type="button"
                  onClick={() => removeLink(link.id)}
                  title="Remove link"
                  className="text-muted-foreground hover:text-destructive shrink-0 opacity-0 group-hover:opacity-100"
                >
                  <Trash2 className="size-3.5" />
                  <span className="sr-only">Remove link</span>
                </button>
              </div>
            ),
          )}
        </div>
      )}

      <ExternalRefsSection featureClassCode={featureClassCode} featureId={featureId} />
    </div>
  )
}

/**
 * The feature's external_ref map (migration 0145): the ids it carries in other
 * systems (SCADA / AMI / CIS), keyed by system code. Distinct from the links
 * above -- a link is a stored pointer the panel opens/fetches, while external_ref
 * is the correlation SOURCE OF TRUTH (what the reverse index resolves inbound
 * pushes against, and what the live-fetch builds its URL from). Shown as a small
 * list with an inline add (pick a catalogued system + type the id) and a per-row
 * remove; a row whose system has a fetch template also offers inline Live data.
 * Editing rides features:write -- a read-only viewer sees the refs but no controls.
 */
function ExternalRefsSection({ featureClassCode, featureId }: { featureClassCode: string; featureId: string }) {
  const { data, isPending, isError } = useFeatureExternalRefs(featureClassCode, featureId)
  const setRefs = useSetFeatureExternalRefs(featureClassCode, featureId)
  const canWrite = usePermission()("features:write")

  // The system catalog gives the add-picker its options and tells each ref row
  // whether its system can be fetched (has a path_template). Behind metadata:read
  // like the links picker; a feature editor who lacks it can still add a ref by
  // typing the code, and existing rows still render (their code is enough).
  const canReadSystems = usePermission()("metadata:read")
  const systemsQuery = useExternalSystems(canReadSystems)
  const systems = systemsQuery.data?.external_systems ?? []
  const templateByCode = new Map(systems.map((s) => [s.code, s.path_template ?? ""]))
  const nameByCode = new Map(systems.map((s) => [s.code, s.name]))

  const [adding, setAdding] = useState(false)
  const [sysCode, setSysCode] = useState("")
  const [extId, setExtId] = useState("")

  const refs = data?.refs ?? []

  const reset = () => {
    setAdding(false)
    setSysCode("")
    setExtId("")
  }

  // Write the next map = current entries (raw values, preserved) + the edit.
  const buildMap = (mutate: (m: Record<string, unknown>) => void) => {
    const next: Record<string, unknown> = {}
    for (const r of refs) next[r.system] = r.value
    mutate(next)
    return next
  }

  const handleAdd = () => {
    const code = sysCode.trim()
    const id = extId.trim()
    if (code === "" || id === "") return
    const next = buildMap((m) => {
      m[code] = id
    })
    setRefs.mutate(next, {
      onSuccess: reset,
      onError: (err) =>
        useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not save this reference."),
    })
  }

  const removeRef = (system: string) => {
    const next = buildMap((m) => {
      delete m[system]
    })
    setRefs.mutate(next, {
      onError: (err) =>
        useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not remove this reference."),
    })
  }

  const inputClass = "border-input bg-background w-full rounded-sm border px-2 py-1 text-sm"

  // Systems not already referenced -- the picker only offers unused ones (one ref
  // per system). Typing a brand-new code is still allowed via the free-text option.
  const available = systems.filter((s) => !refs.some((r) => r.system === s.code))

  return (
    <div className="mt-2 w-full border-t pt-3">
      <div className="mb-2 flex items-center justify-between">
        <span className="text-muted-foreground text-xs font-medium uppercase tracking-wide">External references</span>
        {canWrite && !adding && (
          <Button variant="ghost" size="sm" className="h-6 px-2" onClick={() => setAdding(true)}>
            <Plus className="size-3.5" />
            Add
          </Button>
        )}
      </div>

      {adding && (
        <div className="mb-2 flex w-full flex-col gap-2 rounded-md border p-2">
          {available.length > 0 && (
            <select value={sysCode} onChange={(e) => setSysCode(e.target.value)} className={inputClass}>
              <option value="">Select a system...</option>
              {available.map((s) => (
                <option key={s.code} value={s.code}>
                  {s.name} ({s.code})
                </option>
              ))}
            </select>
          )}
          {available.length === 0 && (
            <input
              value={sysCode}
              onChange={(e) => setSysCode(e.target.value.toUpperCase())}
              placeholder="System code (e.g. SCADA)"
              className={cn(inputClass, "font-mono text-xs")}
            />
          )}
          <input
            autoFocus
            value={extId}
            onChange={(e) => setExtId(e.target.value)}
            onKeyDown={(e) => e.key === "Enter" && handleAdd()}
            placeholder="This system's id (e.g. 48213)"
            className={cn(inputClass, "font-mono text-xs")}
          />
          <div className="flex items-center gap-2">
            <Button size="sm" onClick={handleAdd} disabled={setRefs.isPending || sysCode.trim() === "" || extId.trim() === ""}>
              {setRefs.isPending ? <Loader2 className="size-4 animate-spin" /> : <Plus className="size-4" />}
              Save
            </Button>
            <Button variant="ghost" size="sm" onClick={reset}>
              Cancel
            </Button>
          </div>
        </div>
      )}

      {isPending ? (
        <div className="text-muted-foreground flex items-center gap-2 text-sm">
          <Loader2 className="size-4 animate-spin" />
          Loading references...
        </div>
      ) : isError ? null : refs.length === 0 ? (
        <p className="text-muted-foreground text-sm">No external references.</p>
      ) : (
        <div className="flex w-full flex-col gap-2">
          {refs.map((ref) => (
            <ExternalRefRow
              key={ref.system}
              featureClassCode={featureClassCode}
              featureId={featureId}
              entry={ref}
              systemName={nameByCode.get(ref.system)}
              canFetch={(templateByCode.get(ref.system) ?? "") !== ""}
              canRemove={canWrite}
              onRemove={() => removeRef(ref.system)}
            />
          ))}
        </div>
      )}
    </div>
  )
}

/**
 * One external_ref entry: the system + the id this feature has there. When the
 * system carries a fetch template, a Live data toggle pulls that id's data through
 * the server proxy (GET /feature-links/external) and shows it as an expandable
 * card, exactly like a stored api link's data row. An object ref (no single id)
 * shows its raw JSON instead of a scalar id.
 */
function ExternalRefRow({
  featureClassCode,
  featureId,
  entry,
  systemName,
  canFetch,
  canRemove,
  onRemove,
}: {
  featureClassCode: string
  featureId: string
  entry: ExternalRefEntry
  systemName?: string
  canFetch: boolean
  canRemove: boolean
  onRemove: () => void
}) {
  const [open, setOpen] = useState(false)
  const query = useFeatureExternalData(featureClassCode, featureId, entry.system, open && canFetch)

  const label = systemName ?? entry.system
  const value =
    entry.has_id && entry.id !== "" ? entry.id : typeof entry.value === "string" ? entry.value : JSON.stringify(entry.value)

  return (
    <div className="rounded-md border">
      <div className="group hover:bg-accent/50 flex w-full items-center gap-2 rounded-t-md p-2 text-sm transition-colors">
        <Cloud className="text-muted-foreground size-4 shrink-0" />
        <div className="flex min-w-0 flex-1 flex-col">
          <span className="truncate font-medium">{label}</span>
          <span className="text-muted-foreground truncate font-mono text-xs" title={value}>
            {entry.system} · {value}
          </span>
        </div>
        {canFetch && (
          <Button
            variant="ghost"
            size="sm"
            className="h-7 px-2"
            disabled={query.isFetching}
            onClick={() => (open ? query.refetch() : setOpen(true))}
          >
            {query.isFetching ? <Loader2 className="size-3.5 animate-spin" /> : <RefreshCw className="size-3.5" />}
            {open ? "Refresh" : "Live data"}
          </Button>
        )}
        {canRemove && (
          <button
            type="button"
            onClick={onRemove}
            title="Remove reference"
            className="text-muted-foreground hover:text-destructive shrink-0 opacity-0 group-hover:opacity-100"
          >
            <Trash2 className="size-3.5" />
            <span className="sr-only">Remove reference</span>
          </button>
        )}
      </div>
      {open && canFetch && (
        <div className="border-t p-2">
          {query.isPending ? (
            <div className="text-muted-foreground flex items-center gap-2 text-xs">
              <Loader2 className="size-3.5 animate-spin" />
              Fetching...
            </div>
          ) : query.isError ? (
            <p className="text-destructive text-xs">{query.error.message}</p>
          ) : query.data ? (
            <>
              <div className="text-muted-foreground mb-1 flex items-center justify-between text-xs">
                <span>
                  {query.data.system.name} · {query.data.format.toUpperCase()}
                </span>
                <span>fetched {new Date(query.data.fetched_at).toLocaleTimeString()}</span>
              </div>
              <pre className="bg-muted max-h-64 overflow-auto rounded-sm p-2 font-mono text-xs">
                {JSON.stringify(query.data.data, null, 2)}
              </pre>
            </>
          ) : null}
        </div>
      )}
    </div>
  )
}

/**
 * One kind='api' link: a header row that fetches its live data through
 * the server-side proxy (GET /feature-links/{id}/data) on demand and
 * shows the normalized payload as an expandable card. The data isn't
 * loaded until the first "Load"; after that the same button re-fetches.
 */
function ApiLinkRow({ link, onRemove }: { link: ExternalLink; onRemove: () => void }) {
  const [open, setOpen] = useState(false)
  const query = useExternalLinkData(link.id, open)

  const source = link.system_name ?? link.system_code ?? "system removed"

  return (
    <div className="rounded-md border">
      <div className="group hover:bg-accent/50 flex w-full items-center gap-2 rounded-t-md p-2 text-sm transition-colors">
        <Cloud className="text-muted-foreground size-4 shrink-0" />
        <div className="flex min-w-0 flex-1 flex-col">
          <span className="truncate font-medium">{link.label}</span>
          <span className="text-muted-foreground truncate font-mono text-xs">
            {source}
            {link.resource_path ? ` · ${link.resource_path}` : ""}
          </span>
        </div>
        <Button
          variant="ghost"
          size="sm"
          className="h-7 px-2"
          disabled={query.isFetching}
          onClick={() => (open ? query.refetch() : setOpen(true))}
        >
          {query.isFetching ? <Loader2 className="size-3.5 animate-spin" /> : <RefreshCw className="size-3.5" />}
          {open ? "Refresh" : "Load"}
        </Button>
        <button
          type="button"
          onClick={onRemove}
          title="Remove link"
          className="text-muted-foreground hover:text-destructive shrink-0 opacity-0 group-hover:opacity-100"
        >
          <Trash2 className="size-3.5" />
          <span className="sr-only">Remove link</span>
        </button>
      </div>
      {open && (
        <div className="border-t p-2">
          {query.isPending ? (
            <div className="text-muted-foreground flex items-center gap-2 text-xs">
              <Loader2 className="size-3.5 animate-spin" />
              Fetching...
            </div>
          ) : query.isError ? (
            <p className="text-destructive text-xs">{query.error.message}</p>
          ) : query.data ? (
            <>
              <div className="text-muted-foreground mb-1 flex items-center justify-between text-xs">
                <span>
                  {query.data.system.name} · {query.data.format.toUpperCase()}
                </span>
                <span>fetched {new Date(query.data.fetched_at).toLocaleTimeString()}</span>
              </div>
              <pre className="bg-muted max-h-64 overflow-auto rounded-sm p-2 font-mono text-xs">
                {JSON.stringify(query.data.data, null, 2)}
              </pre>
            </>
          ) : null}
        </div>
      )}
    </div>
  )
}

/**
 * A feature's append-only change timeline (Phase 41), newest first -- who
 * changed it, when, and what it looked like after each change. Backed by
 * useFeatureHistory (GET /features/{code}/{id}/history), which never 404s on a
 * purged feature, so an empty list is a valid answer, not an error. Read-only:
 * history is a byproduct of the write path, never edited here. Each row shows
 * the action, the actor (resolved to a name/email server-side; "System" for a
 * CLI/sync write or a since-deleted user), and the timestamp; expanding a row
 * reveals the full stored snapshot for that change.
 */
function historyActionLabel(action: string): { label: string; className: string } {
  switch (action) {
    case "create":
      return { label: "Created", className: "bg-emerald-500/15 text-emerald-600 dark:text-emerald-400" }
    case "update":
      return { label: "Edited", className: "bg-blue-500/15 text-blue-600 dark:text-blue-400" }
    case "publish":
      return { label: "Published", className: "bg-emerald-500/15 text-emerald-600 dark:text-emerald-400" }
    case "unpublish":
      return { label: "Unpublished", className: "bg-amber-500/15 text-amber-600 dark:text-amber-400" }
    case "delete":
      return { label: "Deleted", className: "bg-destructive/15 text-destructive" }
    case "restore":
      return { label: "Restored", className: "bg-blue-500/15 text-blue-600 dark:text-blue-400" }
    case "purge":
      return { label: "Purged", className: "bg-destructive/15 text-destructive" }
    // Private-drafts Phase B: staging verbs, shown only in the author's own
    // timeline (peers never receive these rows). A muted amber marks them as
    // pending/private work, distinct from the emerald/blue of a public change.
    case "stage_edit":
      return { label: "Private edit", className: "bg-amber-500/15 text-amber-600 dark:text-amber-400" }
    case "stage_delete":
      return { label: "Private delete", className: "bg-amber-500/15 text-amber-600 dark:text-amber-400" }
    case "discard":
      return { label: "Discarded", className: "bg-muted text-muted-foreground" }
    default:
      // An unrecognized verb (a newer backend action this build doesn't know)
      // still renders -- shown verbatim rather than dropped.
      return { label: action, className: "bg-muted text-muted-foreground" }
  }
}

function HistoryRow({ entry }: { entry: FeatureHistoryEntry }) {
  const [expanded, setExpanded] = useState(false)
  const { label, className } = historyActionLabel(entry.action)
  // Prefer a display name, fall back to the email, then to "System" -- a
  // null actor is a CLI/field-sync write or a user deleted since the change.
  const actor = entry.actor_name || entry.actor_email || "System"

  return (
    <div className="rounded-md border text-sm">
      <button
        type="button"
        onClick={() => setExpanded((e) => !e)}
        className="hover:bg-accent/50 flex w-full items-start gap-2 rounded-t-md p-2 text-left transition-colors"
      >
        <span className={cn("shrink-0 rounded-md px-1.5 py-0.5 text-xs font-medium", className)}>{label}</span>
        <div className="flex min-w-0 flex-1 flex-col">
          <span className="truncate font-medium">{actor}</span>
          <span className="text-muted-foreground text-xs">{new Date(entry.at).toLocaleString()}</span>
        </div>
        <ChevronDown className={cn("text-muted-foreground mt-0.5 size-3.5 shrink-0 transition-transform", expanded && "rotate-180")} />
      </button>
      {expanded && (
        <div className="border-t p-2">
          <pre className="bg-muted max-h-64 overflow-auto rounded-sm p-2 font-mono text-xs">
            {JSON.stringify(entry.snapshot, null, 2)}
          </pre>
        </div>
      )}
    </div>
  )
}

function HistoryTab({ featureClassCode, featureId }: { featureClassCode: string; featureId: string }) {
  const { data, isPending, isError, error } = useFeatureHistory(featureClassCode, featureId)
  useErrorToast(isError, error, "Could not load this feature's history")

  if (isPending) {
    return (
      <div className="text-muted-foreground flex items-center gap-2 text-sm">
        <Loader2 className="size-4 animate-spin" />
        Loading history...
      </div>
    )
  }
  if (isError || !data) return null
  if (data.history.length === 0) {
    return <p className="text-muted-foreground text-sm">No history recorded for this feature.</p>
  }

  return (
    <div className="flex flex-col gap-2">
      {data.history.map((entry) => (
        <HistoryRow key={entry.id} entry={entry} />
      ))}
    </div>
  )
}

/** A friendly label + colour for the mobile GnssFixType name. The stored value is
 * the Dart enum `.name` (camelCase -- see gnss_sample.dart's toCaptureJson), so
 * `rtkFixed` is the survey-grade best; a low/no fix reads red. An unknown value
 * renders verbatim. */
function fixTypeLabel(fix: string | null): { label: string; className: string } {
  switch (fix) {
    case "rtkFixed":
      return { label: "RTK fixed", className: "bg-emerald-500/15 text-emerald-600 dark:text-emerald-400" }
    case "rtkFloat":
      return { label: "RTK float", className: "bg-emerald-500/15 text-emerald-600 dark:text-emerald-400" }
    case "dgps":
      return { label: "DGPS", className: "bg-blue-500/15 text-blue-600 dark:text-blue-400" }
    case "gps":
      return { label: "GPS fix", className: "bg-blue-500/15 text-blue-600 dark:text-blue-400" }
    case "none":
      return { label: "No fix", className: "bg-destructive/15 text-destructive" }
    case "unknown":
    case null:
      return { label: "GPS fix", className: "bg-muted text-muted-foreground" }
    default:
      return { label: fix, className: "bg-muted text-muted-foreground" }
  }
}

function fmtDop(v: number | null): string {
  return v == null ? "—" : v.toFixed(1)
}

function GpsCaptureRow({ capture }: { capture: FeatureGpsCapture }) {
  const { label, className } = fixTypeLabel(capture.fix_type)
  // Prefer a display name, fall back to email, then "Field device" -- a null
  // captured_by is a device whose submitting user we couldn't resolve.
  const actor = capture.captured_by_name || capture.captured_by_email || "Field device"
  const hasDop = capture.hdop != null || capture.pdop != null || capture.vdop != null
  const sats =
    capture.sats_used != null && capture.sats_in_view != null
      ? `${capture.sats_used} / ${capture.sats_in_view}`
      : capture.sats_used != null
        ? `${capture.sats_used} used`
        : capture.sats_in_view != null
          ? `${capture.sats_in_view} in view`
          : null

  return (
    <div className="rounded-md border p-2 text-sm">
      <div className="flex items-start gap-2">
        <span className={cn("shrink-0 rounded-md px-1.5 py-0.5 text-xs font-medium", className)}>{label}</span>
        <div className="flex min-w-0 flex-1 flex-col">
          <span className="truncate font-medium">{actor}</span>
          <span className="text-muted-foreground text-xs">{new Date(capture.captured_at).toLocaleString()}</span>
        </div>
        <span className="text-muted-foreground shrink-0 text-xs capitalize">{capture.source}</span>
      </div>
      <dl className="mt-2 grid grid-cols-2 gap-x-3 gap-y-1 text-xs">
        {hasDop ? (
          <>
            <div className="flex justify-between gap-2">
              <dt className="text-muted-foreground">PDOP</dt>
              <dd className="font-mono">{fmtDop(capture.pdop)}</dd>
            </div>
            <div className="flex justify-between gap-2">
              <dt className="text-muted-foreground">HDOP</dt>
              <dd className="font-mono">{fmtDop(capture.hdop)}</dd>
            </div>
            <div className="flex justify-between gap-2">
              <dt className="text-muted-foreground">VDOP</dt>
              <dd className="font-mono">{fmtDop(capture.vdop)}</dd>
            </div>
          </>
        ) : (
          <div className="text-muted-foreground col-span-2">DOP not reported by this receiver</div>
        )}
        <div className="flex justify-between gap-2">
          <dt className="text-muted-foreground">Accuracy</dt>
          <dd className="font-mono">{capture.h_accuracy_m == null ? "—" : `±${capture.h_accuracy_m.toFixed(1)} m`}</dd>
        </div>
        {sats != null && (
          <div className="flex justify-between gap-2">
            <dt className="text-muted-foreground">Satellites</dt>
            <dd className="font-mono">{sats}</dd>
          </div>
        )}
        {capture.altitude_m != null && (
          <div className="flex justify-between gap-2">
            <dt className="text-muted-foreground">Altitude</dt>
            <dd className="font-mono">{capture.altitude_m.toFixed(0)} m</dd>
          </div>
        )}
      </dl>
    </div>
  )
}

/**
 * A feature's GNSS survey-quality captures (Phase gnss-quality Slice 3), newest
 * first -- the fix DOP/accuracy/satellites recorded each time a field crew placed
 * or moved it at their device's position. Backed by useFeatureGpsCaptures (GET
 * /features/{code}/{id}/gps-captures), which never 404s, so an empty list is a
 * valid answer -- and the normal one for any map-drawn or REST-created feature.
 * Read-only: a capture is a byproduct of a field placement, never edited here.
 */
function GpsTab({ featureClassCode, featureId }: { featureClassCode: string; featureId: string }) {
  const { data, isPending, isError, error } = useFeatureGpsCaptures(featureClassCode, featureId)
  useErrorToast(isError, error, "Could not load this feature's GPS captures")

  if (isPending) {
    return (
      <div className="text-muted-foreground flex items-center gap-2 text-sm">
        <Loader2 className="size-4 animate-spin" />
        Loading GPS captures...
      </div>
    )
  }
  if (isError || !data) return null
  if (data.gps_captures.length === 0) {
    return (
      <p className="text-muted-foreground text-sm">
        No GPS captures for this feature. Survey quality is recorded only when a feature is placed at the device&apos;s
        position from the field tool.
      </p>
    )
  }

  return (
    <div className="flex flex-col gap-2">
      {data.gps_captures.map((capture) => (
        <GpsCaptureRow key={capture.id} capture={capture} />
      ))}
    </div>
  )
}

/**
 * Read-only, deliberately -- a feature's material isn't a separate
 * thing to "add" here, it's whatever value the feature already has on
 * one of its own domain-linked fields (e.g. MV_CONDUCTOR's own
 * conductor_spec), the same attribute the Attributes tab's edit form
 * already lets you set. This tab exists purely to surface what's
 * otherwise invisible once that value is picked: the real engineering
 * numbers (resistance, ampacity, ...) behind whichever domain value the
 * field currently holds, resolved server-side onto
 * useFeatureClassSchema's own fieldSchema.material_specs
 * (api/cmd/server/main.go's resolveMaterialSpecs) rather than the
 * admin-only /admin/domains this map-facing panel has no business
 * calling. Changing which material a feature uses still happens on
 * Attributes -- the Edit button below just jumps there, it doesn't
 * duplicate that form.
 */
function MaterialsTab({ onEditClick }: { onEditClick: () => void }) {
  const selected = useSelectionStore((s) => s.selected)
  const {
    data: schema,
    isPending: schemaPending,
    isError: schemaIsError,
    error: schemaError,
  } = useFeatureClassSchema(selected?.featureClassCode)
  const {
    data: feature,
    isPending: featurePending,
    isError: featureIsError,
    error: featureError,
  } = useFeature(selected?.featureClassCode, selected?.featureId)
  useErrorToast(schemaIsError, schemaError, "Could not load this feature class's schema")
  useErrorToast(featureIsError, featureError, "Could not load this feature")

  if (schemaPending || featurePending) {
    return (
      <div className="text-muted-foreground flex items-center gap-2 text-sm">
        <Loader2 className="size-4 animate-spin" />
        Loading materials...
      </div>
    )
  }
  if (schemaIsError || featureIsError || !schema || !feature || !selected) return null

  const materialFields = schema.fields.filter((f) => f.material_specs && Object.keys(f.material_specs).length > 0)

  return (
    <div className="flex flex-col gap-3">
      <CostOverrideSection
        featureClassCode={selected.featureClassCode}
        featureId={selected.featureId}
        linear={schema.geometry_type === "line"}
      />
      {materialFields.length === 0 ? (
        <p className="text-muted-foreground text-sm">This feature class has no material-linked fields.</p>
      ) : (
        <>
          <Button
            variant="outline"
            size="sm"
            className="self-start"
            disabled={Boolean(feature.deleted_at)}
            onClick={() => {
              useEditStore.getState().start(selected.featureClassCode, selected.featureId)
              onEditClick()
            }}
          >
            <Pencil className="size-4" />
            Edit in Attributes
          </Button>
          {materialFields.map((f) => {
        const currentValue = feature.attributes[f.name]
        const spec = typeof currentValue === "string" ? f.material_specs?.[currentValue] : undefined
        return (
          <div key={f.name} className="rounded-md border p-2 text-sm">
            <div className="flex items-center justify-between gap-2">
              <span className="font-medium">{formatFieldLabel(f.name)}</span>
              <span className="text-muted-foreground text-xs">{currentValue ? String(currentValue) : "Not set"}</span>
            </div>
            {spec ? (
              <div className="mt-1.5 flex flex-wrap gap-1">
                <span className="bg-muted text-muted-foreground rounded-md px-1.5 py-0.5 text-xs">{spec.name}</span>
                {spec.resistance_ohm_per_km !== undefined && (
                  <span className="bg-muted text-muted-foreground rounded-md px-1.5 py-0.5 text-xs">
                    R {spec.resistance_ohm_per_km} Ω/km
                  </span>
                )}
                {spec.reactance_ohm_per_km !== undefined && (
                  <span className="bg-muted text-muted-foreground rounded-md px-1.5 py-0.5 text-xs">
                    X {spec.reactance_ohm_per_km} Ω/km
                  </span>
                )}
                {spec.ampacity_amps !== undefined && (
                  <span className="bg-muted text-muted-foreground rounded-md px-1.5 py-0.5 text-xs">{spec.ampacity_amps} A</span>
                )}
                {spec.diameter_mm !== undefined && (
                  <span className="bg-muted text-muted-foreground rounded-md px-1.5 py-0.5 text-xs">⌀ {spec.diameter_mm} mm</span>
                )}
                {spec.weight_kg_per_km !== undefined && (
                  <span className="bg-muted text-muted-foreground rounded-md px-1.5 py-0.5 text-xs">
                    {spec.weight_kg_per_km} kg/km
                  </span>
                )}
              </div>
            ) : (
              <p className="text-muted-foreground mt-1 text-xs">
                {currentValue ? "No material data for this value." : "Set this field on Attributes to add a material."}
              </p>
            )}
          </div>
            )
          })}
        </>
      )}
    </div>
  )
}

/**
 * Per-feature cost override (metadata.feature_cost_overrides) -- a one-off unit
 * cost, in the base currency, that supersedes the catalog rate the Bill of
 * Quantities would otherwise resolve for this feature. Admin-only: the whole
 * cost catalog is metadata:write-gated (security-admin-view-gate), so the
 * section renders nothing for callers without it. `linear` picks the unit
 * wording (per km for a line, each for a discrete feature).
 */
function CostOverrideSection({
  featureClassCode,
  featureId,
  linear,
}: {
  featureClassCode: string
  featureId: string
  linear: boolean
}) {
  const canEdit = usePermission()("metadata:write")
  // Query only for admins (the endpoint 403s otherwise); the undefined arg
  // disables the query for everyone else.
  const { data } = useFeatureCostOverrides(canEdit ? featureClassCode : undefined)
  const upsert = useUpsertFeatureCostOverride()
  const del = useDeleteFeatureCostOverride()
  const [value, setValue] = useState("")
  const [editing, setEditing] = useState(false)

  // Hooks above stay unconditional; bail out only after them.
  if (!canEdit) return null

  const existing = data?.feature_cost_overrides.find((o) => o.feature_id === featureId)
  const unit = linear ? "per km" : "each"

  function startEdit() {
    setValue(existing ? String(existing.unit_cost) : "")
    setEditing(true)
  }

  function save() {
    const n = Number(value.trim())
    if (!Number.isFinite(n) || n < 0) {
      useToastStore.getState().pushError("Enter a cost of zero or more.")
      return
    }
    upsert.mutate(
      { classCode: featureClassCode, featureId, body: { unit_cost: n } },
      {
        onSuccess: () => setEditing(false),
        onError: (err) =>
          useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not save the override."),
      },
    )
  }

  function clear() {
    del.mutate(
      { classCode: featureClassCode, featureId },
      {
        onSuccess: () => setEditing(false),
        onError: (err) =>
          useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not clear the override."),
      },
    )
  }

  return (
    <div className="rounded-md border p-2 text-sm">
      <div className="flex items-center justify-between gap-2">
        <span className="font-medium">Cost override</span>
        {existing && !editing && (
          <span className="rounded-md bg-amber-500/15 px-1.5 py-0.5 text-xs text-amber-600 dark:text-amber-400">
            {existing.unit_cost} {unit}
          </span>
        )}
      </div>
      {editing ? (
        <div className="mt-2 flex flex-col gap-2">
          <input
            className={inputClass}
            type="number"
            min="0"
            step="any"
            value={value}
            onChange={(e) => setValue(e.target.value)}
            placeholder={`unit cost (base currency), ${unit}`}
            autoFocus
          />
          <div className="flex items-center gap-2">
            <Button size="sm" onClick={save} disabled={upsert.isPending}>
              Save
            </Button>
            <Button size="sm" variant="ghost" onClick={() => setEditing(false)}>
              Cancel
            </Button>
          </div>
        </div>
      ) : (
        <div className="mt-2 flex items-center gap-2">
          <Button size="sm" variant="outline" onClick={startEdit}>
            {existing ? "Change" : "Set override"}
          </Button>
          {existing && (
            <Button
              size="sm"
              variant="ghost"
              className="text-destructive"
              onClick={clear}
              disabled={del.isPending}
            >
              Reset to catalog
            </Button>
          )}
        </div>
      )}
      <p className="text-muted-foreground mt-1.5 text-xs">
        Supersedes the catalog rate for this feature only, in the base currency ({unit}).
      </p>
    </div>
  )
}

/** Default position (bottom-right, clear of the h-7 status bar below
 * it) used until the user drags the panel somewhere else -- matches
 * `!position` in connect-confirm-panel.tsx's own className branch. */
const DEFAULT_POSITION_CLASS = "bottom-10 right-4"

/** Fixed defaults the panel opens at (a touch wider and notably taller
 * than the original `w-96 h-64`, so more of the attribute list is visible
 * before scrolling), and the floors a manual resize can't shrink past --
 * small enough to still show a header row plus a couple of lines of
 * content, not so small the tab rail collapses. The panel is anchored
 * bottom-right (DEFAULT_POSITION_CLASS), so a larger default simply grows
 * it left and up out of that corner. */
const DEFAULT_WIDTH = 560
const DEFAULT_HEIGHT = 400
const MIN_WIDTH = 288
const MIN_HEIGHT = 180

/** How far off the original a duplicated feature lands (metres, EPSG:3857),
 * far enough to be visible and separately clickable, near enough to drag into
 * place -- see the Duplicate action below and docs/phase-20-split-copy.md.
 * Applied on both axes, so the copy appears up-and-to-the-right of the
 * original. */
const COPY_OFFSET_METERS = 5

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
 * to the default corner on every click. The window can also be resized
 * (drag the bottom-right corner grip -- `size` state, floored at
 * MIN_WIDTH/MIN_HEIGHT) and minimized (the header's collapse toggle --
 * `minimized` state hides the body so only the drag header remains),
 * both living in this component's own state alongside `position`. Size
 * persists across selections the same way position does; `minimized`
 * resets to false on a new selection so a freshly-selected feature is
 * never hidden.
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
  // The selected class's schema carries its effective analysis-study set
  // (applicable by role, minus the admin prune) -- the analysis menu gates each
  // study on it. Same query key AttributesTab fetches, so react-query dedups it.
  const { data: selectedSchema } = useFeatureClassSchema(selected?.featureClassCode)
  const [activeTab, setActiveTab] = useState<TabId>("attributes")
  const [deleteDialogOpen, setDeleteDialogOpen] = useState(false)
  const [creatingDiagram, setCreatingDiagram] = useState(false)
  const [position, setPosition] = useState<{ x: number; y: number } | null>(null)
  const [size, setSize] = useState<{ width: number; height: number } | null>(null)
  const [minimized, setMinimized] = useState(false)
  const panelRef = useRef<HTMLDivElement>(null)
  const tracing = useTraceStore((s) => s.loading)
  const isolating = useIsolateStore((s) => s.loading)
  const loadFlowLoading = useLoadFlowStore((s) => s.loading)
  const faultCurrentLoading = useFaultCurrentStore((s) => s.loading)
  const ampacityLoading = useAmpacityStore((s) => s.loading)
  const { data: traceConfigurations } = useTraceConfigurations()
  // Asset-ownership actions live in the actions dropdown, shown only on a
  // transformer (the declare boundary, boundary_role='transformer').
  const markDownstreamPrivate = useMarkDownstreamPrivate()
  const resyncOwnership = useResyncOwnership()

  const isEditingSelected =
    Boolean(selected) &&
    editing?.featureClassCode === selected?.featureClassCode &&
    editing?.featureId === selected?.featureId

  const isTransformerBoundary =
    featureClasses?.feature_classes.find((fc) => fc.code === selected?.featureClassCode)?.boundary_role ===
    "transformer"
  const ownershipIsPrivate =
    (feature?.attributes as Record<string, unknown> | undefined)?.ownership === "private"

  function handleMarkOwnershipPrivate() {
    if (!selected) return
    markDownstreamPrivate.mutate(
      { featureClass: selected.featureClassCode, featureId: selected.featureId },
      {
        onSuccess: (res) =>
          useToastStore
            .getState()
            .pushSuccess(`Marked private: this feature + ${res.reached} downstream (${res.stamped} stamped).`),
        onError: (err) =>
          useToastStore
            .getState()
            .pushError(err instanceof ApiError ? err.message : "Could not mark downstream private."),
      },
    )
  }

  function handleResyncOwnership() {
    resyncOwnership.mutate(undefined, {
      onSuccess: (res) =>
        useToastStore
          .getState()
          .pushSuccess(
            `Ownership re-synced from ${res.boundaries} private boundary(ies): ${res.stamped} private, ${res.cleared} cleared.`,
          ),
      onError: (err) =>
        useToastStore
          .getState()
          .pushError(err instanceof ApiError ? err.message : "Could not re-sync ownership."),
    })
  }

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
    setMinimized(false)
    useEditStore.getState().stop()
  }, [selected?.featureClassCode, selected?.featureId])

  // Duplicate: open the create form pre-seeded from this feature (its
  // attributes, subtype, and name), with a copy of its geometry nudged a few
  // metres off the original so it's visible and draggable into place (Phase
  // 20, docs/phase-20-split-copy.md). Reuses the ordinary draft/create path --
  // Save is a plain POST /features, so RBAC, validation, and territory
  // assignment are exactly the manual create's. Connections/relationships are
  // deliberately NOT copied (they're separate facts about the original); the
  // assistant's suggestion loop fires on the new feature's geometry to help
  // re-establish any that belong on the copy. Only offered for a feature that
  // actually has geometry -- a geometry-less class has nothing to place.
  function handleDuplicate() {
    if (!selected || !feature?.geometry) return
    const geometry = offsetGeometry(feature.geometry, COPY_OFFSET_METERS, COPY_OFFSET_METERS)
    useDrawStore.getState().startCopyDraft(selected.featureClassCode, geometry, {
      name: feature.name ?? undefined,
      subtype: feature.subtype,
      attributes: { ...feature.attributes },
    })
  }

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

  // Same pointer-capture mechanics as the header drag above, but writes
  // `size` instead of `position`. If the panel is still at its default
  // corner anchor (`position` null, pinned bottom-right via
  // DEFAULT_POSITION_CLASS), pin it to its current top-left first so a
  // bottom-right resize grows down/right intuitively rather than the
  // panel appearing to grow up/left off its fixed corner. Floors at
  // MIN_WIDTH/MIN_HEIGHT so it can't be shrunk into an unusable sliver.
  function handleResizePointerDown(e: React.PointerEvent) {
    e.stopPropagation()
    const rect = panelRef.current?.getBoundingClientRect()
    if (!rect) return
    if (!position) setPosition({ x: rect.left, y: rect.top })
    const startX = e.clientX
    const startY = e.clientY
    const startWidth = size?.width ?? rect.width
    const startHeight = size?.height ?? rect.height

    const handleMove = (moveEvt: PointerEvent) => {
      setSize({
        width: Math.max(MIN_WIDTH, startWidth + (moveEvt.clientX - startX)),
        height: Math.max(MIN_HEIGHT, startHeight + (moveEvt.clientY - startY)),
      })
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
  async function runTrace(configCode: string, startOverride?: TraceFeatureRef, target?: TraceFeatureRef) {
    if (!startOverride && !selected) return
    const start =
      startOverride ?? { feature_class: selected!.featureClassCode, feature_id: selected!.featureId }
    // Snapshot before clearing the load-flow store below -- if its result
    // was run from this same start, its per-node voltages carry forward as
    // the table's computed-voltage overlay.
    const computed = snapshotComputedVoltages(start)
    useIsolateStore.getState().clear()
    useLoadFlowStore.getState().clear()
    useFaultCurrentStore.getState().clear()
    useAmpacityStore.getState().clear()
    useCoordinationStore.getState().clear()
    useVoltageProfileStore.getState().clear()
    useContingencyStore.getState().clear()
    useHostingStore.getState().clear()
    useEvHostingStore.getState().clear()
    useLoadGrowthStore.getState().clear()
    useMeshedLoadFlowStore.getState().clear()
    useNodalLoadFlowStore.getState().clear()
    useAutoLoadFlowStore.getState().clear()
    useUnbalancedLoadFlowStore.getState().clear()
    useVoltVarStore.getState().clear()
    useTapOptimizationStore.getState().clear()
    useStorageDispatchStore.getState().clear()
    useVvoStore.getState().clear()
    useTraceStore.getState().setLoading(true)
    try {
      const result = await apiFetch<TraceResult>("/trace", {
        method: "POST",
        body: JSON.stringify({
          trace_configuration: configCode,
          start,
          // A downstream trace stops at the transformer boundary by default; the
          // "Continue past transformers" toggle in the result table re-runs it
          // across the boundary. The engine ignores this on non-downstream
          // configs, so sending the default here is always safe.
          cross_transformers: false,
          // target is only read by a SHORTEST_PATH config (the "Trace path to…"
          // gesture); the engine ignores it on every other config, so it's safe to
          // send only when present.
          ...(target ? { target } : {}),
        }),
      })
      // A SHORTEST_PATH trace that found no connected route comes back with
      // path_found=false (undefined for every other config). Clear the panel and
      // warn rather than leaving an empty "Shortest path" result that reads like a
      // failure.
      if (target && result.path_found === false) {
        useTraceStore.getState().clear()
        useToastStore
          .getState()
          .pushWarning("No connected path between these two features (they may be on separate networks).")
        return
      }
      useTraceStore.getState().setResult(result, computed, () => runTrace(configCode, start, target))
    } catch (err) {
      useTraceStore.getState().setLoading(false)
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not run this trace.")
    }
  }

  // "Trace path to…" gesture (trace-path-store.ts): once the user picks a
  // destination feature on the map, fire the SHORTEST_PATH trace from source to
  // target and end the gesture. SHORTEST_PATH is the one config the dropdown hides
  // (it needs a second feature, which this two-click flow supplies).
  const tracePathActive = useTracePathStore((s) => s.active)
  const tracePathSource = useTracePathStore((s) => s.source)
  const tracePathTarget = useTracePathStore((s) => s.target)
  const shortestPathConfig = traceConfigurations?.trace_configurations.find(
    (c) => c.direction === "SHORTEST_PATH",
  )
  useEffect(() => {
    if (!tracePathActive || !tracePathSource || !tracePathTarget) return
    if (!shortestPathConfig) {
      useToastStore.getState().pushError("Shortest-path trace is not configured on this deployment.")
      useTracePathStore.getState().cancel()
      return
    }
    runTrace(
      shortestPathConfig.code,
      { feature_class: tracePathSource.featureClassCode, feature_id: tracePathSource.featureId },
      { feature_class: tracePathTarget.featureClassCode, feature_id: tracePathTarget.featureId },
    )
    useTracePathStore.getState().cancel()
    // Keyed only on the gesture reaching a complete source+target state; runTrace is
    // a hoisted closure over component state and intentionally not a dep.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [tracePathActive, tracePathSource, tracePathTarget, shortestPathConfig])

  // Safety net: end an in-progress "Trace path to…" gesture if the user moves to a
  // different feature by any other means (peer picker, Show-on-map, etc.). The
  // gesture is bound to the feature it started from -- a map click during picking
  // doesn't change `selected` (it's intercepted as the target), so this only fires
  // on a genuine navigation away, never mid-pick.
  useEffect(() => {
    if (!tracePathActive) return
    if (!selected || selected.featureId !== tracePathSource?.featureId) {
      useTracePathStore.getState().cancel()
    }
  }, [selected, tracePathActive, tracePathSource])

  // Isolation (api/internal/trace/isolate.go) needs a DOWNSTREAM
  // config for its barrier settings -- resolved by direction, same
  // reasoning the old hardcoded trace buttons used, since there's no
  // separate "isolation configuration" of its own to pick from a list.
  const isolationConfig = traceConfigurations?.trace_configurations.find((c) => c.direction === "DOWNSTREAM")

  async function runIsolate() {
    if (!selected || !isolationConfig) return
    useTraceStore.getState().clear()
    useLoadFlowStore.getState().clear()
    useFaultCurrentStore.getState().clear()
    useAmpacityStore.getState().clear()
    useCoordinationStore.getState().clear()
    useVoltageProfileStore.getState().clear()
    useContingencyStore.getState().clear()
    useHostingStore.getState().clear()
    useEvHostingStore.getState().clear()
    useLoadGrowthStore.getState().clear()
    useMeshedLoadFlowStore.getState().clear()
    useNodalLoadFlowStore.getState().clear()
    useAutoLoadFlowStore.getState().clear()
    useUnbalancedLoadFlowStore.getState().clear()
    useVoltVarStore.getState().clear()
    useTapOptimizationStore.getState().clear()
    useStorageDispatchStore.getState().clear()
    useVvoStore.getState().clear()
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
  async function runFeederTrace(startOverride?: TraceFeatureRef) {
    if ((!startOverride && !selected) || !isolationConfig) return
    const start =
      startOverride ?? { feature_class: selected!.featureClassCode, feature_id: selected!.featureId }
    const computed = snapshotComputedVoltages(start)
    useIsolateStore.getState().clear()
    useLoadFlowStore.getState().clear()
    useFaultCurrentStore.getState().clear()
    useAmpacityStore.getState().clear()
    useCoordinationStore.getState().clear()
    useVoltageProfileStore.getState().clear()
    useContingencyStore.getState().clear()
    useHostingStore.getState().clear()
    useEvHostingStore.getState().clear()
    useLoadGrowthStore.getState().clear()
    useMeshedLoadFlowStore.getState().clear()
    useNodalLoadFlowStore.getState().clear()
    useAutoLoadFlowStore.getState().clear()
    useUnbalancedLoadFlowStore.getState().clear()
    useVoltVarStore.getState().clear()
    useTapOptimizationStore.getState().clear()
    useStorageDispatchStore.getState().clear()
    useVvoStore.getState().clear()
    useTraceStore.getState().setLoading(true)
    try {
      const result = await apiFetch<{ start: TraceFeatureRef; heads: TraceFeatureRef[]; features: TracedFeature[]; truncated?: boolean }>(
        "/trace/feeder",
        {
          method: "POST",
          body: JSON.stringify({
            trace_configuration: isolationConfig.code,
            start,
          }),
        },
      )
      useTraceStore.getState().setResult(
        {
          trace_configuration: "FEEDER",
          direction: "FEEDER",
          start: result.start,
          features: result.features,
          barriers: [],
          targets: result.heads,
          targets_found: result.heads.length > 0,
          truncated: result.truncated,
        },
        computed,
        () => runFeederTrace(start),
      )
    } catch (err) {
      useTraceStore.getState().setLoading(false)
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not trace this feeder.")
    }
  }

  async function runSectionTrace(startOverride?: TraceFeatureRef, protectionZone = false) {
    if ((!startOverride && !selected) || !isolationConfig) return
    const start =
      startOverride ?? { feature_class: selected!.featureClassCode, feature_id: selected!.featureId }
    const computed = snapshotComputedVoltages(start)
    useIsolateStore.getState().clear()
    useLoadFlowStore.getState().clear()
    useFaultCurrentStore.getState().clear()
    useAmpacityStore.getState().clear()
    useCoordinationStore.getState().clear()
    useVoltageProfileStore.getState().clear()
    useContingencyStore.getState().clear()
    useHostingStore.getState().clear()
    useEvHostingStore.getState().clear()
    useLoadGrowthStore.getState().clear()
    useMeshedLoadFlowStore.getState().clear()
    useNodalLoadFlowStore.getState().clear()
    useAutoLoadFlowStore.getState().clear()
    useUnbalancedLoadFlowStore.getState().clear()
    useVoltVarStore.getState().clear()
    useTapOptimizationStore.getState().clear()
    useStorageDispatchStore.getState().clear()
    useVvoStore.getState().clear()
    useTraceStore.getState().setLoading(true)
    try {
      const result = await apiFetch<{
        start: TraceFeatureRef
        features: TracedFeature[]
        upstream_boundary?: TraceFeatureRef
        downstream_boundary?: TraceFeatureRef
        truncated?: boolean
      }>("/trace/section", {
        method: "POST",
        body: JSON.stringify({
          trace_configuration: isolationConfig.code,
          start,
          protection_zone: protectionZone,
        }),
      })
      const boundaries = [result.upstream_boundary, result.downstream_boundary].filter(
        (b): b is TraceFeatureRef => Boolean(b),
      )
      const label = protectionZone ? "PROTECTION ZONE" : "SECTION"
      useTraceStore.getState().setResult(
        {
          trace_configuration: label,
          direction: label,
          start: result.start,
          features: result.features,
          barriers: [],
          targets: boundaries,
          targets_found: boundaries.length > 0,
          truncated: result.truncated,
        },
        computed,
        () => runSectionTrace(start, protectionZone),
      )
    } catch (err) {
      useTraceStore.getState().setLoading(false)
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not trace this section.")
    }
  }

  // Service trace (api/internal/trace/service.go) -- a downstream walk
  // (reusing the same DOWNSTREAM config isolation does) that collects the
  // reached SERVICE_POINTs and their consumption. Normalized into the same
  // TraceResult display every other trace uses: the reached service points
  // become `features`, the critical ones (restoration rank <= P2) become
  // `targets` so they get the distinct highlight, and the consumption rollup
  // rides along on `service` where ServiceRollupSummary surfaces it. Answers
  // "who/what does this feed, and how much of it is critical?".
  async function runServiceTrace(startOverride?: TraceFeatureRef) {
    if ((!startOverride && !selected) || !isolationConfig) return
    const start =
      startOverride ?? { feature_class: selected!.featureClassCode, feature_id: selected!.featureId }
    const computed = snapshotComputedVoltages(start)
    useIsolateStore.getState().clear()
    useLoadFlowStore.getState().clear()
    useFaultCurrentStore.getState().clear()
    useAmpacityStore.getState().clear()
    useCoordinationStore.getState().clear()
    useVoltageProfileStore.getState().clear()
    useContingencyStore.getState().clear()
    useHostingStore.getState().clear()
    useEvHostingStore.getState().clear()
    useLoadGrowthStore.getState().clear()
    useMeshedLoadFlowStore.getState().clear()
    useNodalLoadFlowStore.getState().clear()
    useAutoLoadFlowStore.getState().clear()
    useUnbalancedLoadFlowStore.getState().clear()
    useVoltVarStore.getState().clear()
    useTapOptimizationStore.getState().clear()
    useStorageDispatchStore.getState().clear()
    useVvoStore.getState().clear()
    useTraceStore.getState().setLoading(true)
    try {
      const result = await apiFetch<ServiceTraceResult>("/trace/service", {
        method: "POST",
        body: JSON.stringify({
          trace_configuration: isolationConfig.code,
          start,
        }),
      })
      const criticals = result.service_points
        .filter((sp) => sp.critical)
        .map((sp) => ({ feature_class: sp.feature_class, feature_id: sp.feature_id }))
      useTraceStore.getState().setResult(
        {
          trace_configuration: "SERVICE",
          direction: "SERVICE",
          start: result.start,
          features: result.service_points.map((sp) => ({
            feature_class: sp.feature_class,
            feature_id: sp.feature_id,
          })),
          barriers: [],
          targets: criticals,
          targets_found: criticals.length > 0,
          service: result.service,
          truncated: result.truncated,
        },
        computed,
        () => runServiceTrace(start),
      )
      if (result.service_points.length === 0) {
        useToastStore.getState().pushWarning("No service points downstream of this feature.")
      }
    } catch (err) {
      useTraceStore.getState().setLoading(false)
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not run the service trace.")
    }
  }

  // Supply-path trace (api/internal/trace/supply.go) -- from this
  // feature (a service point / meter, but any feature is accepted) walk
  // UPSTREAM barrier-free to report the supply chain: the LV switching
  // devices in the way, the serving distribution transformer, then on to
  // the MV devices, power transformer, and source. Needs no
  // trace_configuration (it's a fixed topological walk, not a
  // config-driven one), so unlike feeder/section it's available even
  // without isolationConfig. Its response is normalized into the same
  // TraceResult shape trace-result-table.tsx renders: features come back
  // nearest-first so the table reads consumption-point -> ... -> source,
  // and the milestones (transformer/switches/source) become `targets` so
  // they get the same visually-distinct highlight a target_class hit does.
  async function runSupplyPathTrace(
    startOverride?: TraceFeatureRef,
    opts?: { respectSwitchState?: boolean; toPrimarySource?: boolean },
  ) {
    if (!startOverride && !selected) return
    const start =
      startOverride ?? { feature_class: selected!.featureClassCode, feature_id: selected!.featureId }
    const respectSwitchState = opts?.respectSwitchState ?? false
    const toPrimarySource = opts?.toPrimarySource ?? false
    const computed = snapshotComputedVoltages(start)
    useIsolateStore.getState().clear()
    useLoadFlowStore.getState().clear()
    useFaultCurrentStore.getState().clear()
    useAmpacityStore.getState().clear()
    useCoordinationStore.getState().clear()
    useVoltageProfileStore.getState().clear()
    useContingencyStore.getState().clear()
    useHostingStore.getState().clear()
    useEvHostingStore.getState().clear()
    useLoadGrowthStore.getState().clear()
    useMeshedLoadFlowStore.getState().clear()
    useNodalLoadFlowStore.getState().clear()
    useAutoLoadFlowStore.getState().clear()
    useUnbalancedLoadFlowStore.getState().clear()
    useVoltVarStore.getState().clear()
    useTapOptimizationStore.getState().clear()
    useStorageDispatchStore.getState().clear()
    useVvoStore.getState().clear()
    useTraceStore.getState().setLoading(true)
    try {
      const result = await apiFetch<{
        start: TraceFeatureRef
        features: TracedFeature[]
        // Each milestone carries the functional_role (migration 0091) that made
        // it significant -- "transformer" | "controller" | "source" -- so the
        // chain can be labelled/grouped; a plain TraceFeatureRef for the map
        // highlight, plus role. The engine derives these from class role, not a
        // hardcoded class list (api/internal/trace/supply.go).
        milestones: (TraceFeatureRef & { role: string })[]
        serving_transformer?: TraceFeatureRef
        source?: TraceFeatureRef
        // What the reported source IS (api/internal/trace/supply.go): an LV
        // feature stops at its distribution transformer ("serving_transformer");
        // an MV feature stops at the breaker a circuit designates as its source
        // ("feeder_head"), or, when no circuit source is designated, the nearest
        // upstream protective device ("section_head"); "none" when nothing
        // upstream was reachable.
        source_kind?: string
        reached_transformer: boolean
        // Echoes the request: true = energized view (walk halted at open switches).
        respect_switch_state?: boolean
        truncated?: boolean
      }>("/trace/supply-path", {
        method: "POST",
        // respect_switch_state true = energized view (stop at open switches, so a
        // ring's normally-open point bounds the trace to the live supply direction);
        // false (default) = the full barrier-free wired view.
        body: JSON.stringify({
          start,
          respect_switch_state: respectSwitchState,
          to_primary_source: toPrimarySource,
        }),
      })
      useTraceStore.getState().setResult(
        {
          trace_configuration: "SUPPLY_PATH",
          direction: "SUPPLY_PATH",
          start: result.start,
          features: result.features,
          barriers: [],
          targets: result.milestones,
          targets_found: result.milestones.length > 0,
          truncated: result.truncated,
        },
        computed,
        () => runSupplyPathTrace(start, { respectSwitchState, toPrimarySource }),
      )
      // The supply path stops at the head of the feature's own section, which is
      // level-dependent -- an LV feature's transformer, an MV feature's feeder
      // breaker -- so "no transformer upstream" is only noteworthy when NOTHING
      // upstream was reachable. A section_head terminus means the circuit has no
      // designated source yet, so we stopped at the nearest protective device.
      if (!result.source) {
        useToastStore
          .getState()
          .pushWarning(
            respectSwitchState
              ? "No live source — every supply route to this feature passes through an open switch, so it may be de-energized."
              : "No upstream source found for this feature — upstream connectivity may be incomplete.",
          )
      } else if (result.source_kind === "section_head") {
        useToastStore
          .getState()
          .pushWarning(
            "Stopped at the nearest protective device: this circuit has no designated source feature, so the true feeder breaker couldn't be resolved. Assign the circuit a source to complete the feeder head.",
          )
      }
    } catch (err) {
      useTraceStore.getState().setLoading(false)
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not trace the supply path.")
    }
  }

  // Arms the load-flow panel (load-flow-panel.tsx) for the selected
  // feature -- doesn't run anything itself, since the engine needs
  // nominal_voltage_v first and there's no honest default across MV/LV
  // to guess (see docs/phase-8-load-flow.md). Clears trace/isolate the
  // same way every other trigger here does; the panel's own Run button
  // makes the actual POST /analysis/load-flow call.
  function armLoadFlow() {
    if (!selected) return
    useTraceStore.getState().clear()
    useIsolateStore.getState().clear()
    useFaultCurrentStore.getState().clear()
    useAmpacityStore.getState().clear()
    useCoordinationStore.getState().clear()
    useVoltageProfileStore.getState().clear()
    useContingencyStore.getState().clear()
    useHostingStore.getState().clear()
    useEvHostingStore.getState().clear()
    useLoadGrowthStore.getState().clear()
    useMeshedLoadFlowStore.getState().clear()
    useNodalLoadFlowStore.getState().clear()
    useAutoLoadFlowStore.getState().clear()
    useUnbalancedLoadFlowStore.getState().clear()
    useVoltVarStore.getState().clear()
    useTapOptimizationStore.getState().clear()
    useStorageDispatchStore.getState().clear()
    useVvoStore.getState().clear()
    useLoadFlowStore.getState().arm({ feature_class: selected.featureClassCode, feature_id: selected.featureId })
  }

  // Arms the fault-current panel (fault-current-panel.tsx) for the
  // selected feature -- like armLoadFlow, it only arms (the engine needs
  // nominal_voltage_v first, no honest MV/LV default), clearing the other
  // three mutually-exclusive workflows; the panel's own Run button makes
  // the POST /analysis/fault-current call.
  // Opens the Fault Location panel (fault-location-panel.tsx) for the selected
  // feature as the feeder SOURCE. Unlike the planning studies above it is an
  // independent, operational tool, so it does NOT clear the mutually-exclusive
  // workflows -- a located fault can sit open beside a fault-current or trace
  // result. The panel's own Locate button makes the POST /analysis/fault-location.
  function armFaultLocation() {
    if (!selected) return
    useFaultLocationStore.getState().arm({ feature_class: selected.featureClassCode, feature_id: selected.featureId })
  }

  function armFaultCurrent() {
    if (!selected) return
    useTraceStore.getState().clear()
    useIsolateStore.getState().clear()
    useLoadFlowStore.getState().clear()
    useAmpacityStore.getState().clear()
    useCoordinationStore.getState().clear()
    useVoltageProfileStore.getState().clear()
    useContingencyStore.getState().clear()
    useHostingStore.getState().clear()
    useEvHostingStore.getState().clear()
    useLoadGrowthStore.getState().clear()
    useMeshedLoadFlowStore.getState().clear()
    useNodalLoadFlowStore.getState().clear()
    useAutoLoadFlowStore.getState().clear()
    useUnbalancedLoadFlowStore.getState().clear()
    useVoltVarStore.getState().clear()
    useTapOptimizationStore.getState().clear()
    useStorageDispatchStore.getState().clear()
    useVvoStore.getState().clear()
    useFaultCurrentStore.getState().arm({ feature_class: selected.featureClassCode, feature_id: selected.featureId })
  }

  // Arms the ampacity panel (ampacity-panel.tsx) for the selected
  // feature -- like the other two, it only arms (the engine needs
  // environmental inputs first, no honest default for ambient temp);
  // clears the other four mutually-exclusive workflows. Only ever
  // called for an edge (see the dropdown item's own network_role gate
  // below), mirroring the same guard runAmpacity enforces server-side.
  function armAmpacity() {
    if (!selected) return
    useTraceStore.getState().clear()
    useIsolateStore.getState().clear()
    useLoadFlowStore.getState().clear()
    useFaultCurrentStore.getState().clear()
    useCoordinationStore.getState().clear()
    useVoltageProfileStore.getState().clear()
    useContingencyStore.getState().clear()
    useHostingStore.getState().clear()
    useEvHostingStore.getState().clear()
    useLoadGrowthStore.getState().clear()
    useMeshedLoadFlowStore.getState().clear()
    useNodalLoadFlowStore.getState().clear()
    useAutoLoadFlowStore.getState().clear()
    useUnbalancedLoadFlowStore.getState().clear()
    useVoltVarStore.getState().clear()
    useTapOptimizationStore.getState().clear()
    useStorageDispatchStore.getState().clear()
    useVvoStore.getState().clear()
    useAmpacityStore.getState().arm({ feature_class: selected.featureClassCode, feature_id: selected.featureId })
  }

  // Arms the protection-coordination panel (coordination-panel.tsx) with
  // the selected feature as the *source*. Unlike the other five analysis
  // triggers, coordination needs a second node -- the fault location --
  // which the panel captures from a follow-up map selection ("Set fault"),
  // so this only arms the start; the panel's own Run button makes the
  // POST /analysis/coordination call. Clears the other five
  // mutually-exclusive workflows the same way they clear each other.
  function armCoordination() {
    if (!selected) return
    useTraceStore.getState().clear()
    useIsolateStore.getState().clear()
    useLoadFlowStore.getState().clear()
    useFaultCurrentStore.getState().clear()
    useAmpacityStore.getState().clear()
    useVoltageProfileStore.getState().clear()
    useContingencyStore.getState().clear()
    useHostingStore.getState().clear()
    useEvHostingStore.getState().clear()
    useLoadGrowthStore.getState().clear()
    useMeshedLoadFlowStore.getState().clear()
    useNodalLoadFlowStore.getState().clear()
    useAutoLoadFlowStore.getState().clear()
    useUnbalancedLoadFlowStore.getState().clear()
    useVoltVarStore.getState().clear()
    useTapOptimizationStore.getState().clear()
    useStorageDispatchStore.getState().clear()
    useVvoStore.getState().clear()
    useCoordinationStore.getState().arm({ feature_class: selected.featureClassCode, feature_id: selected.featureId })
  }

  // Arms the voltage-profile panel (voltage-profile-panel.tsx) for the
  // selected feature as the sweep's source. Like load-flow it only arms
  // (the report needs nominal_voltage_v first, no honest MV/LV default);
  // the panel's own Run button makes the POST /analysis/voltage-profile
  // call. Clears the other six mutually-exclusive workflows.
  function armVoltageProfile() {
    if (!selected) return
    useTraceStore.getState().clear()
    useIsolateStore.getState().clear()
    useLoadFlowStore.getState().clear()
    useFaultCurrentStore.getState().clear()
    useAmpacityStore.getState().clear()
    useCoordinationStore.getState().clear()
    useContingencyStore.getState().clear()
    useHostingStore.getState().clear()
    useEvHostingStore.getState().clear()
    useLoadGrowthStore.getState().clear()
    useMeshedLoadFlowStore.getState().clear()
    useNodalLoadFlowStore.getState().clear()
    useAutoLoadFlowStore.getState().clear()
    useUnbalancedLoadFlowStore.getState().clear()
    useVoltVarStore.getState().clear()
    useTapOptimizationStore.getState().clear()
    useStorageDispatchStore.getState().clear()
    useVvoStore.getState().clear()
    useVoltageProfileStore.getState().arm({ feature_class: selected.featureClassCode, feature_id: selected.featureId })
  }

  // Arms the contingency panel (contingency-panel.tsx) with the selected
  // feature as the donor feeder's source. Like load-flow it only arms (the
  // study needs nominal_voltage_v first, no honest MV/LV default); the
  // panel's own Run button makes the POST /analysis/contingency call, which
  // sweeps the feeder and, for every normally-open tie it halts at,
  // simulates closing it to report the restorable outaged load. Clears the
  // other seven mutually-exclusive workflows.
  function armContingency() {
    if (!selected) return
    useTraceStore.getState().clear()
    useIsolateStore.getState().clear()
    useLoadFlowStore.getState().clear()
    useFaultCurrentStore.getState().clear()
    useAmpacityStore.getState().clear()
    useCoordinationStore.getState().clear()
    useVoltageProfileStore.getState().clear()
    useHostingStore.getState().clear()
    useEvHostingStore.getState().clear()
    useLoadGrowthStore.getState().clear()
    useMeshedLoadFlowStore.getState().clear()
    useNodalLoadFlowStore.getState().clear()
    useAutoLoadFlowStore.getState().clear()
    useUnbalancedLoadFlowStore.getState().clear()
    useVoltVarStore.getState().clear()
    useTapOptimizationStore.getState().clear()
    useStorageDispatchStore.getState().clear()
    useVvoStore.getState().clear()
    useContingencyStore.getState().arm({ feature_class: selected.featureClassCode, feature_id: selected.featureId })
  }

  // Arms the DER hosting-capacity panel (hosting-panel.tsx) with the selected
  // feature as the feeder's source. Like load-flow it only arms (the study
  // needs nominal_voltage_v first, no honest MV/LV default); the panel's own
  // Run button makes the POST /analysis/hosting-capacity call, which
  // binary-searches the largest DER injection each interconnection node can
  // accept before over-voltage or thermal overload binds. Clears the other
  // eight mutually-exclusive workflows.
  function armHosting() {
    if (!selected) return
    useTraceStore.getState().clear()
    useIsolateStore.getState().clear()
    useLoadFlowStore.getState().clear()
    useFaultCurrentStore.getState().clear()
    useAmpacityStore.getState().clear()
    useCoordinationStore.getState().clear()
    useVoltageProfileStore.getState().clear()
    useContingencyStore.getState().clear()
    useMeshedLoadFlowStore.getState().clear()
    useNodalLoadFlowStore.getState().clear()
    useAutoLoadFlowStore.getState().clear()
    useUnbalancedLoadFlowStore.getState().clear()
    useVoltVarStore.getState().clear()
    useTapOptimizationStore.getState().clear()
    useStorageDispatchStore.getState().clear()
    useVvoStore.getState().clear()
    useEvHostingStore.getState().clear()
    useLoadGrowthStore.getState().clear()
    useHostingStore.getState().arm({ feature_class: selected.featureClassCode, feature_id: selected.featureId })
  }

  // Arms the EV feeder-headroom panel (ev-hosting-panel.tsx) with the selected
  // feature as the feeder's source. The under-voltage mirror of DER hosting: it
  // binary-searches the largest ADDITIONAL charging load each connection node
  // can absorb before under-voltage (0.95 pu floor) or thermal overload binds.
  // Like load-flow it only arms (needs nominal_voltage_v first); the panel's own
  // Run button makes the POST /analysis/ev-hosting call. Clears the other
  // mutually-exclusive workflows the same way they clear each other.
  function armEvHosting() {
    if (!selected) return
    useTraceStore.getState().clear()
    useIsolateStore.getState().clear()
    useLoadFlowStore.getState().clear()
    useFaultCurrentStore.getState().clear()
    useAmpacityStore.getState().clear()
    useCoordinationStore.getState().clear()
    useVoltageProfileStore.getState().clear()
    useContingencyStore.getState().clear()
    useHostingStore.getState().clear()
    useMeshedLoadFlowStore.getState().clear()
    useNodalLoadFlowStore.getState().clear()
    useAutoLoadFlowStore.getState().clear()
    useUnbalancedLoadFlowStore.getState().clear()
    useVoltVarStore.getState().clear()
    useTapOptimizationStore.getState().clear()
    useStorageDispatchStore.getState().clear()
    useVvoStore.getState().clear()
    useLoadGrowthStore.getState().clear()
    useEvHostingStore.getState().arm({ feature_class: selected.featureClassCode, feature_id: selected.featureId })
  }

  // Arms the load-growth forecast panel (load-growth-panel.tsx) with the selected
  // feature as the feeder's start. Like load-flow it only arms (the study needs
  // nominal_voltage_v + a horizon + growth rate first); the panel's own Run button
  // makes the POST /analysis/load-growth call, which projects the load-flow across a
  // horizon under a growth assumption and reports which assets overload and when (plus
  // optional DER headroom). Clears the other mutually-exclusive workflows.
  function armLoadGrowth() {
    if (!selected) return
    useTraceStore.getState().clear()
    useIsolateStore.getState().clear()
    useLoadFlowStore.getState().clear()
    useFaultCurrentStore.getState().clear()
    useAmpacityStore.getState().clear()
    useCoordinationStore.getState().clear()
    useVoltageProfileStore.getState().clear()
    useContingencyStore.getState().clear()
    useMeshedLoadFlowStore.getState().clear()
    useNodalLoadFlowStore.getState().clear()
    useAutoLoadFlowStore.getState().clear()
    useUnbalancedLoadFlowStore.getState().clear()
    useVoltVarStore.getState().clear()
    useTapOptimizationStore.getState().clear()
    useStorageDispatchStore.getState().clear()
    useVvoStore.getState().clear()
    useHostingStore.getState().clear()
    useEvHostingStore.getState().clear()
    useLoadGrowthStore.getState().arm({ feature_class: selected.featureClassCode, feature_id: selected.featureId })
  }

  // Arms the meshed load-flow panel (meshed-load-flow-panel.tsx) with the
  // selected feature as the source. Like load-flow it only arms (the engine
  // needs nominal_voltage_v first, no honest MV/LV default); the panel's own
  // Run button makes the POST /analysis/meshed-load-flow call, which solves a
  // radial spanning tree then closes each ring/loop by compensation. Clears the
  // other mutually-exclusive workflows the same way they clear each other.
  function armMeshedLoadFlow() {
    if (!selected) return
    useTraceStore.getState().clear()
    useIsolateStore.getState().clear()
    useLoadFlowStore.getState().clear()
    useFaultCurrentStore.getState().clear()
    useAmpacityStore.getState().clear()
    useCoordinationStore.getState().clear()
    useVoltageProfileStore.getState().clear()
    useContingencyStore.getState().clear()
    useHostingStore.getState().clear()
    useEvHostingStore.getState().clear()
    useLoadGrowthStore.getState().clear()
    useNodalLoadFlowStore.getState().clear()
    useAutoLoadFlowStore.getState().clear()
    useUnbalancedLoadFlowStore.getState().clear()
    useVoltVarStore.getState().clear()
    useTapOptimizationStore.getState().clear()
    useStorageDispatchStore.getState().clear()
    useVvoStore.getState().clear()
    useMeshedLoadFlowStore.getState().arm({ feature_class: selected.featureClassCode, feature_id: selected.featureId })
  }

  // Arms the nodal (Y-bus) load-flow panel (nodal-load-flow-panel.tsx) with the
  // selected feature as the slack source. This is the Phase 52 Tier 2 solver --
  // a distinct engine that assembles a per-unit admittance matrix over the
  // general graph and solves it by Gauss-Seidel, so a densely meshed LV network
  // is solved directly rather than refused. Like load-flow it only arms (needs
  // nominal_voltage_v first); the panel's own Run button makes the POST
  // /analysis/nodal-load-flow call. Clears the other mutually-exclusive
  // workflows the same way they clear each other.
  function armNodalLoadFlow() {
    if (!selected) return
    useTraceStore.getState().clear()
    useIsolateStore.getState().clear()
    useLoadFlowStore.getState().clear()
    useFaultCurrentStore.getState().clear()
    useAmpacityStore.getState().clear()
    useCoordinationStore.getState().clear()
    useVoltageProfileStore.getState().clear()
    useContingencyStore.getState().clear()
    useHostingStore.getState().clear()
    useEvHostingStore.getState().clear()
    useLoadGrowthStore.getState().clear()
    useMeshedLoadFlowStore.getState().clear()
    useAutoLoadFlowStore.getState().clear()
    useUnbalancedLoadFlowStore.getState().clear()
    useVoltVarStore.getState().clear()
    useTapOptimizationStore.getState().clear()
    useStorageDispatchStore.getState().clear()
    useVvoStore.getState().clear()
    useNodalLoadFlowStore.getState().arm({ feature_class: selected.featureClassCode, feature_id: selected.featureId })
  }

  // The dispatch router (Phase 52, S4): the engine-agnostic entry. Classifies the
  // network's topology once and auto-selects the engine (radial ladder / Tier 1
  // mesh compensation / Tier 2 nodal Y-bus), so the caller need not know the
  // shape. Like the others it only arms (the panel's Run makes the POST
  // /analysis/load-flow-auto call); clears every other mutually-exclusive
  // workflow, including the three individual engines.
  function armAutoLoadFlow() {
    if (!selected) return
    useTraceStore.getState().clear()
    useIsolateStore.getState().clear()
    useLoadFlowStore.getState().clear()
    useFaultCurrentStore.getState().clear()
    useAmpacityStore.getState().clear()
    useCoordinationStore.getState().clear()
    useVoltageProfileStore.getState().clear()
    useContingencyStore.getState().clear()
    useHostingStore.getState().clear()
    useEvHostingStore.getState().clear()
    useLoadGrowthStore.getState().clear()
    useMeshedLoadFlowStore.getState().clear()
    useNodalLoadFlowStore.getState().clear()
    useUnbalancedLoadFlowStore.getState().clear()
    useVoltVarStore.getState().clear()
    useTapOptimizationStore.getState().clear()
    useStorageDispatchStore.getState().clear()
    useVvoStore.getState().clear()
    useAutoLoadFlowStore.getState().arm({ feature_class: selected.featureClassCode, feature_id: selected.featureId })
  }

  // Arms the unbalanced (three-phase) load-flow panel
  // (unbalanced-load-flow-panel.tsx) with the selected feature as the feeder's
  // source. Like load-flow it only arms (needs nominal_voltage_v first); the
  // panel's own Run makes the POST /analysis/unbalanced-load-flow call, which
  // solves phases A/B/C as three decoupled ladders. Clears every other
  // mutually-exclusive workflow.
  function armUnbalancedLoadFlow() {
    if (!selected) return
    useTraceStore.getState().clear()
    useIsolateStore.getState().clear()
    useLoadFlowStore.getState().clear()
    useFaultCurrentStore.getState().clear()
    useAmpacityStore.getState().clear()
    useCoordinationStore.getState().clear()
    useVoltageProfileStore.getState().clear()
    useContingencyStore.getState().clear()
    useHostingStore.getState().clear()
    useEvHostingStore.getState().clear()
    useLoadGrowthStore.getState().clear()
    useMeshedLoadFlowStore.getState().clear()
    useNodalLoadFlowStore.getState().clear()
    useAutoLoadFlowStore.getState().clear()
    useVoltVarStore.getState().clear()
    useTapOptimizationStore.getState().clear()
    useStorageDispatchStore.getState().clear()
    useVvoStore.getState().clear()
    useUnbalancedLoadFlowStore.getState().arm({ feature_class: selected.featureClassCode, feature_id: selected.featureId })
  }

  // Arms the volt-VAR panel (volt-var-panel.tsx) with the selected feature as
  // the feeder's source. The reactive twin of DER hosting: it sweeps the base
  // case then re-sweeps each switched capacitor bank currently out of circuit as
  // though switched in, reporting the voltage lift / loss reduction switching it
  // on would buy. Like load-flow it only arms (needs nominal_voltage_v first);
  // the panel's own Run button makes the POST /analysis/volt-var call. Clears the
  // other mutually-exclusive workflows the same way they clear each other.
  function armVoltVar() {
    if (!selected) return
    useTraceStore.getState().clear()
    useIsolateStore.getState().clear()
    useLoadFlowStore.getState().clear()
    useFaultCurrentStore.getState().clear()
    useAmpacityStore.getState().clear()
    useCoordinationStore.getState().clear()
    useVoltageProfileStore.getState().clear()
    useContingencyStore.getState().clear()
    useHostingStore.getState().clear()
    useEvHostingStore.getState().clear()
    useLoadGrowthStore.getState().clear()
    useMeshedLoadFlowStore.getState().clear()
    useNodalLoadFlowStore.getState().clear()
    useAutoLoadFlowStore.getState().clear()
    useUnbalancedLoadFlowStore.getState().clear()
    useTapOptimizationStore.getState().clear()
    useStorageDispatchStore.getState().clear()
    useVvoStore.getState().clear()
    useVoltVarStore.getState().arm({ feature_class: selected.featureClassCode, feature_id: selected.featureId })
  }

  // Arms the tap-optimization panel (tap-optimization-panel.tsx) with the
  // selected feature as the feeder's source. The series-voltage twin of volt-VAR:
  // it sweeps the base case then, for each step voltage regulator, tries every
  // tap in ±max_tap_steps and reports the tap that best holds the feeder in band.
  // Like load-flow it only arms (needs nominal_voltage_v first); the panel's own
  // Run button makes the POST /analysis/tap-optimization call. Clears the other
  // mutually-exclusive workflows the same way they clear each other.
  function armTapOptimization() {
    if (!selected) return
    useTraceStore.getState().clear()
    useIsolateStore.getState().clear()
    useLoadFlowStore.getState().clear()
    useFaultCurrentStore.getState().clear()
    useAmpacityStore.getState().clear()
    useCoordinationStore.getState().clear()
    useVoltageProfileStore.getState().clear()
    useContingencyStore.getState().clear()
    useHostingStore.getState().clear()
    useEvHostingStore.getState().clear()
    useLoadGrowthStore.getState().clear()
    useMeshedLoadFlowStore.getState().clear()
    useNodalLoadFlowStore.getState().clear()
    useAutoLoadFlowStore.getState().clear()
    useUnbalancedLoadFlowStore.getState().clear()
    useVoltVarStore.getState().clear()
    useStorageDispatchStore.getState().clear()
    useVvoStore.getState().clear()
    useTapOptimizationStore.getState().arm({ feature_class: selected.featureClassCode, feature_id: selected.featureId })
  }

  // Arms the storage-dispatch panel (storage-dispatch-panel.tsx) with the
  // selected feature as the feeder's source. The bidirectional-injection twin of
  // volt-VAR / tap-optimization: it sweeps the base case then, for each
  // battery-storage unit, tries every set-point from full charge to full
  // discharge and reports the one that best holds the feeder in band and shaves
  // the source draw. Like load-flow it only arms (needs nominal_voltage_v first);
  // the panel's own Run button makes the POST /analysis/storage-dispatch call.
  // Clears the other mutually-exclusive workflows the same way they clear each
  // other.
  function armStorageDispatch() {
    if (!selected) return
    useTraceStore.getState().clear()
    useIsolateStore.getState().clear()
    useLoadFlowStore.getState().clear()
    useFaultCurrentStore.getState().clear()
    useAmpacityStore.getState().clear()
    useCoordinationStore.getState().clear()
    useVoltageProfileStore.getState().clear()
    useContingencyStore.getState().clear()
    useHostingStore.getState().clear()
    useEvHostingStore.getState().clear()
    useLoadGrowthStore.getState().clear()
    useMeshedLoadFlowStore.getState().clear()
    useNodalLoadFlowStore.getState().clear()
    useAutoLoadFlowStore.getState().clear()
    useUnbalancedLoadFlowStore.getState().clear()
    useVoltVarStore.getState().clear()
    useTapOptimizationStore.getState().clear()
    useVvoStore.getState().clear()
    useStorageDispatchStore.getState().arm({ feature_class: selected.featureClassCode, feature_id: selected.featureId })
  }

  // Arms the combined Volt-VAR Optimization panel (vvo-panel.tsx) with the
  // selected feature as the feeder's source. Where volt-VAR / tap-optimization /
  // storage-dispatch each tune one class of control, VVO co-optimizes every
  // capacitor, regulator and battery on the feeder together by coordinate
  // descent -- band first, then loss. Like load-flow it only arms (needs
  // nominal_voltage_v first); the panel's own Run button makes the
  // POST /analysis/vvo call. Clears the other mutually-exclusive workflows the
  // same way they clear each other.
  function armVvo() {
    if (!selected) return
    useTraceStore.getState().clear()
    useIsolateStore.getState().clear()
    useLoadFlowStore.getState().clear()
    useFaultCurrentStore.getState().clear()
    useAmpacityStore.getState().clear()
    useCoordinationStore.getState().clear()
    useVoltageProfileStore.getState().clear()
    useContingencyStore.getState().clear()
    useHostingStore.getState().clear()
    useEvHostingStore.getState().clear()
    useLoadGrowthStore.getState().clear()
    useMeshedLoadFlowStore.getState().clear()
    useNodalLoadFlowStore.getState().clear()
    useAutoLoadFlowStore.getState().clear()
    useUnbalancedLoadFlowStore.getState().clear()
    useVoltVarStore.getState().clear()
    useTapOptimizationStore.getState().clear()
    useStorageDispatchStore.getState().clear()
    useVvoStore.getState().arm({ feature_class: selected.featureClassCode, feature_id: selected.featureId })
  }

  // Isolation only makes sense from a junction -- an edge or a
  // non-network feature has nothing to "open."
  const selectedClass = featureClasses?.feature_classes.find((fc) => fc.code === selected?.featureClassCode)
  const selectedNetworkRole = selectedClass?.network_role
  // A boundary transformer (DIST_XFMR / POWER_XFMR) is the source of its own
  // secondary network -- offer a one-click LV-network study that derives the
  // run voltage from the transformer's secondary (no voltage input needed).
  const selectedIsTransformerBoundary = selectedClass?.boundary_role === "transformer"
  // A leaf endpoint (functional_role consumer/sensor -- SERVICE_POINT, METER):
  // it sits at the end of the network with nothing below it, so downstream-
  // direction traces, sectioning (section/zone), isolate and the load-served
  // trace are pointless from here and are hidden. Upstream / connected /
  // supply-path / feeder still apply (they walk UP toward the source).
  const isLeaf = selectedClass?.functional_role === "consumer" || selectedClass?.functional_role === "sensor"
  // Runnable trace_configurations for the dropdown -- SHORTEST_PATH excluded
  // (needs a target), and DOWNSTREAM-direction configs dropped on a leaf.
  const runnableConfigs =
    traceConfigurations?.trace_configurations.filter(
      (c) => c.direction !== "SHORTEST_PATH" && !(isLeaf && c.direction === "DOWNSTREAM"),
    ) ?? []
  // Split the configs for the cascading trace menu: a circuit-scoped config is a
  // network SPAN (grouped with feeder/section/zone), everything else is a
  // DIRECTION trace (connected / downstream / upstream / ownership…). Its
  // direction is CONNECTED, so scope_to_circuit is the only thing that tells it
  // apart (surfaced on the /trace/configurations summary for exactly this).
  const directionConfigs = runnableConfigs.filter((c) => !c.scope_to_circuit)
  const circuitConfigs = runnableConfigs.filter((c) => c.scope_to_circuit)
  // Is a given analysis study offered for this feature's class? Drives every
  // item in the analysis menu, so a study that doesn't apply to this class (by
  // role) or was pruned by an admin never appears. See api/internal/studies.
  const studyOK = (id: string) => selectedSchema?.analysis_studies?.includes(id) ?? false
  // Whether a whole analysis submenu has any applicable study -- hide the
  // submenu entirely (not just its items) when none apply to this class.
  const anyLoadFlow = ["load_flow_auto", "load_flow", "voltage_profile", "meshed_load_flow", "nodal_load_flow", "unbalanced_load_flow"].some(studyOK)
  const anyFaultProtection = ["fault_current", "coordination", "ampacity"].some(studyOK)
  const anyCapacity = ["contingency", "hosting", "ev_hosting", "load_growth", "volt_var", "tap_optimization", "storage_dispatch", "vvo"].some(studyOK)
  const canIsolate = selectedNetworkRole === "junction" && Boolean(isolationConfig) && !isLeaf

  // Terminal connections only exist for features inside the network model:
  // an `edge` connects to junctions and a `junction` carries the terminals
  // (metadata.terminals rows live only on junction classes). A `container`
  // hosts a diagram and `none` is fully outside the network (e.g. a POLE) --
  // neither can hold a connection, so the Terminals tab is hidden for them
  // rather than offering a connect flow the server rules would reject.
  const isConnectable = selectedNetworkRole === "edge" || selectedNetworkRole === "junction"
  // The Works tab surfaces this asset's WAM records (defects / work orders /
  // inspections, api/internal/wam) and reads them over /wam, gated by wam:read
  // -- a user without it never sees the tab rather than being shown one whose
  // every query 403s. Unlike Terminals, Works applies to any feature (any asset
  // can carry a defect or work order), so it's not restricted by network role.
  const canReadWorks = usePermission()("wam:read")
  const canReadFaults = usePermission()("faults:read")
  const canReadOperations = usePermission()("operations:read")
  const canReadSafety = usePermission()("safety:read")
  const canReadFeatures = usePermission()("features:read")
  const canReadTags = usePermission()("tags:read")
  const canWriteCircuits = usePermission()("circuits:write")
  const [circuitSourceOpen, setCircuitSourceOpen] = useState(false)
  // Circuit catalog (small, cached) — used below to badge the feature when it's a
  // circuit's designated source. A separate useCircuits() lives in the attribute
  // field renderer above; this one is scoped to the FeaturePanel component itself.
  const { data: circuitsData } = useCircuits()
  const visibleTabs = TABS.filter(
    (tab) =>
      (tab.id !== "terminals" || isConnectable) &&
      (tab.id !== "works" || canReadWorks) &&
      (tab.id !== "faults" || canReadFaults) &&
      // Operations (the device-operation log) only makes sense for an operable
      // device -- a protective/switching apparatus, which is a `junction` in the
      // network model (a breaker/recloser/fuse/switch). Hidden for edges,
      // containers and non-network features, and for a user without operations:read.
      (tab.id !== "operations" || (canReadOperations && selectedNetworkRole === "junction")) &&
      // Safety (tags/grounds) applies to any network feature a crew works on -- a
      // device to tag (junction) or a conductor to earth (edge). Hidden for
      // non-network features and a user without safety:read.
      (tab.id !== "safety" ||
        (canReadSafety && (selectedNetworkRole === "junction" || selectedNetworkRole === "edge"))) &&
      // Spatial (geometry-derived overlap/proximity) applies to any feature with a
      // shape -- including the land polygons (easement/parcel/corridor/vegetation),
      // which are network_role 'none', so it is NOT gated on a network role.
      (tab.id !== "spatial" || canReadFeatures) &&
      (tab.id !== "tags" || canReadTags),
  )

  // Trace + every network analysis behind that same dropdown (isolate,
  // feeder/section trace, load flow, fault current, ampacity) only mean
  // something from a feature that's actually a node/edge in the graph. A
  // `container` (METER_CHAMBER, SUBSTATION) or `none` (POLE) has no
  // terminals and never appears in a trace, so hide the whole trace button
  // for them rather than offering runs the engine returns nothing for --
  // same "inside the network model" set the Terminals tab uses.
  const isNetworkFeature = isConnectable

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

  // Width/height come from `size` (or the fixed defaults) as inline
  // styles now, not the former `w-96 h-64` classes -- a manual resize
  // needs a live numeric value the classes can't carry. Minimized drops
  // the height entirely so the shell shrink-wraps its header row.
  const shellStyle: React.CSSProperties = {
    ...(position ? { left: position.x, top: position.y } : {}),
    width: size?.width ?? DEFAULT_WIDTH,
    height: minimized ? undefined : (size?.height ?? DEFAULT_HEIGHT),
  }
  const positionClass = cn(
    // bg-popover (not bg-background) so the translucent accent header gradient
    // tints over the SAME base as the trace-result/analysis panels -- otherwise
    // the header reads a different shade here (popover is pure white in light
    // mode, background a cooler off-white, and the warm accent bleeds the
    // difference through). All floating map panels share the popover base.
    "bg-popover text-popover-foreground fixed z-40 flex flex-col overflow-hidden rounded-lg border border-border/70 border-t-2 border-t-[oklch(0.45_0.10_252)] dark:border-t-primary shadow-xl ring-1 ring-black/5",
    !position && DEFAULT_POSITION_CLASS,
  )

  const minimizeButton = (
    <Button
      variant="ghost"
      size="icon"
      className="size-7 shrink-0"
      title={minimized ? "Restore" : "Minimize"}
      onClick={() => setMinimized((m) => !m)}
    >
      {minimized ? <ChevronsUpDown className="size-4" /> : <ChevronsDownUp className="size-4" />}
      <span className="sr-only">{minimized ? "Restore panel" : "Minimize panel"}</span>
    </Button>
  )

  const resizeHandle = minimized ? null : (
    <div
      onPointerDown={handleResizePointerDown}
      title="Resize"
      style={{ touchAction: "none" }}
      className="absolute right-0 bottom-0 z-50 flex size-4 cursor-nwse-resize items-end justify-end p-0.5"
    >
      <span className="border-muted-foreground/40 size-2 border-r-2 border-b-2" />
    </div>
  )

  // Header layout splits into feature actions (trace, edit, and an
  // overflow menu holding the less-frequent/destructive diagram + delete
  // items) and window controls (minimize + close, pinned far right).
  // Feature actions hide entirely while editing, mid-create-diagram, or
  // minimized -- a minimized panel is just its title bar plus window
  // controls. Close stays available whenever the panel isn't mid-edit,
  // including while minimized, so a collapsed panel can still be
  // dismissed; the edit session has its own Save/Cancel instead.
  const showHeaderActions = !isEditingSelected && !creatingDiagram && !minimized
  const showCloseButton = !isEditingSelected && !creatingDiagram
  const hasDiagramActions =
    (isDiagramContainer && diagrams.length === 0) || diagrams.length >= 1
  // While the ephemeral one-line is open, the panel is opened from clicking a
  // schematic symbol/conductor purely to READ what it is before printing -- so the
  // mutating header controls (Edit, Duplicate, Delete) are hidden. They never affect
  // the print and would clutter a view-only context.
  const oneLineOpen = useOneLineStore((s) => s.open)

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
      <div ref={panelRef} style={shellStyle} className={positionClass}>
        <div
          onPointerDown={handleHeaderPointerDown}
          className="flex cursor-grab items-center justify-between border-b border-[oklch(0.45_0.10_252)]/20 bg-gradient-to-b from-[oklch(0.45_0.10_252)]/15 to-[oklch(0.45_0.10_252)]/5 dark:border-primary/15 dark:from-accent/50 dark:to-accent/20 px-3 py-2 select-none active:cursor-grabbing"
        >
          <div className="flex min-w-0 items-center gap-1.5">
            <GripHorizontal className="text-primary/70 size-3.5 shrink-0" />
            <div className="min-w-0">
              <p className="truncate text-sm font-medium">New {className}</p>
              <p className="text-muted-foreground truncate text-xs">Unsaved</p>
            </div>
          </div>
          <div className="flex shrink-0 items-center gap-1">
            {minimizeButton}
            <Button variant="ghost" size="icon" className="size-7 shrink-0" onClick={clearDraft}>
              <X className="size-4" />
              <span className="sr-only">Cancel</span>
            </Button>
          </div>
        </div>
        {!minimized && (
          <div className="flex-1 overflow-y-auto p-3">
            <FeatureCreateForm />
          </div>
        )}
        {resizeHandle}
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

  // Circuits this feature is the designated SOURCE of (gis.circuits.source_feature_*).
  // Stored on the circuit, not the feature, so the attribute panel surfaces it here.
  const sourceCircuits = (circuitsData?.circuits ?? []).filter(
    (c) => c.source_feature_class === selected.featureClassCode && c.source_feature_id === selected.featureId,
  )

  // A SOURCE feature has no supply path -- it IS where supply originates, so the
  // "Supply path" trace (walk UP to your source) is meaningless and hidden on it.
  // A feature is a (primary) source when it's generation / grid infeed
  // (functional_role 'source', mirroring boundary_role 'source', migration 0129),
  // OR it's the designated source of a circuit (the station feeder head -- an
  // instance fact) AND it is NOT a transformer. A transformer ORIGINATES the
  // next-voltage network at its secondary but is itself supplied from upstream (its
  // primary), so it is a SECONDARY source and keeps its supply path: DIST_XFMR
  // climbs its MV supply, POWER_XFMR its HV. Everything downstream keeps it too.
  const isSourceFeature =
    selectedClass?.functional_role === "source" ||
    (sourceCircuits.length > 0 && selectedClass?.functional_role !== "transformer")

  return (
    <div ref={panelRef} style={shellStyle} className={positionClass}>
      <div
        onPointerDown={handleHeaderPointerDown}
        className="flex cursor-grab items-center justify-between border-b border-[oklch(0.45_0.10_252)]/20 bg-gradient-to-b from-[oklch(0.45_0.10_252)]/15 to-[oklch(0.45_0.10_252)]/5 dark:border-primary/15 dark:from-accent/50 dark:to-accent/20 px-3 py-2 select-none active:cursor-grabbing"
      >
        <div className="flex min-w-0 items-center gap-1.5">
          <GripHorizontal className="text-primary/70 size-3.5 shrink-0" />
          <div className="min-w-0">
            <div className="flex min-w-0 items-center gap-1.5">
              <p className="truncate text-sm font-medium">{primaryLabel}</p>
              {sourceCircuits.length > 0 && (
                <span
                  className="inline-flex shrink-0 items-center gap-1 rounded-full bg-amber-500/15 px-2 py-0.5 text-[11px] font-medium text-amber-700 dark:text-amber-400"
                  title={`Designated source of circuit${sourceCircuits.length === 1 ? "" : "s"}: ${sourceCircuits.map((c) => c.name).join(", ")}`}
                >
                  <Network className="size-3 shrink-0" />
                  {sourceCircuits.length === 1
                    ? `Source of ${sourceCircuits[0].name}`
                    : `Source of ${sourceCircuits.length} circuits`}
                </span>
              )}
            </div>
            <p className="text-muted-foreground truncate font-mono text-xs">{secondaryLabel}</p>
          </div>
        </div>
        <div className="flex shrink-0 items-center gap-1">
          {showHeaderActions && (
            <>
              {isNetworkFeature && (
              <DropdownMenu>
                <DropdownMenuTrigger asChild>
                  <Button
                    variant="ghost"
                    // Not size="icon" (a fixed square) -- this trigger holds an
                    // icon AND a dropdown chevron, so it needs auto width to fit
                    // both, else the content overflows the square and the ghost
                    // hover fill stops short of the icons. h-7 keeps it level with
                    // the sibling icon buttons; tight gap + slim px keep it compact.
                    className="h-7 w-auto gap-0.5 px-1.5"
                    // Disabled mid-"Trace path to…" pick so a second trace/analysis
                    // can't be launched while the map is waiting on a destination.
                    disabled={
                      tracing ||
                      isolating ||
                      loadFlowLoading ||
                      faultCurrentLoading ||
                      ampacityLoading ||
                      tracePathActive
                    }
                    title={tracePathActive ? "Click a destination feature on the map (Esc to cancel)" : "Run trace"}
                  >
                    <Route className="size-4" />
                    <ChevronDown className="size-3 opacity-70" />
                    <span className="sr-only">Run trace</span>
                  </Button>
                </DropdownMenuTrigger>
                <DropdownMenuContent align="end" className="max-h-[min(24rem,var(--radix-dropdown-menu-content-available-height))] w-64 overflow-y-auto">
                  {runnableConfigs.length === 0 && !canIsolate && !isolationConfig && (
                    <p className="text-muted-foreground px-2 py-1.5 text-sm">No trace configurations exist</p>
                  )}
                  {/* The "Continue past transformers" toggle used to live here, but a
                      downstream trace stops at the transformer boundary by default and
                      crossing it only makes sense AFTER you see it stopped there -- so
                      the toggle now lives in the result table (trace-result-table.tsx),
                      where it re-runs the same trace across the boundary. */}
                  {/* The trace variants are grouped into three cascading submenus so the
                      list stays short and scannable as configs are added:
                        Direction  -- the configured topology/direction traces,
                        Spans      -- the bounded partitions this device sits in/bounds,
                        Operational-- what it serves and how to kill it. */}
                  {directionConfigs.length > 0 && (
                    <DropdownMenuSub>
                      <DropdownMenuSubTrigger>
                        <Route className="size-4" />
                        Direction
                      </DropdownMenuSubTrigger>
                      <DropdownMenuSubContent className="w-56">
                        {directionConfigs.map((config) => (
                          <DropdownMenuItem key={config.code} onSelect={() => runTrace(config.code)}>
                            {config.name}
                          </DropdownMenuItem>
                        ))}
                      </DropdownMenuSubContent>
                    </DropdownMenuSub>
                  )}
                  {(circuitConfigs.length > 0 || isolationConfig) && (
                    <DropdownMenuSub>
                      <DropdownMenuSubTrigger>
                        <Network className="size-4" />
                        Spans
                      </DropdownMenuSubTrigger>
                      <DropdownMenuSubContent className="w-56">
                        {circuitConfigs.map((config) => (
                          <DropdownMenuItem key={config.code} onSelect={() => runTrace(config.code)}>
                            {config.name}
                          </DropdownMenuItem>
                        ))}
                        {isolationConfig && (
                          <>
                            {/* Feeder trace walks UP to the feeder, so it's useful even
                                from a leaf; section/zone describe structure around a
                                node, so they're hidden on a leaf endpoint. */}
                            <DropdownMenuItem onSelect={() => runFeederTrace()}>Feeder trace</DropdownMenuItem>
                            {!isLeaf && (
                              <>
                                <DropdownMenuItem onSelect={() => runSectionTrace()}>
                                  Switching section
                                </DropdownMenuItem>
                                <DropdownMenuItem onSelect={() => runSectionTrace(undefined, true)}>
                                  Protection zone
                                </DropdownMenuItem>
                              </>
                            )}
                          </>
                        )}
                      </DropdownMenuSubContent>
                    </DropdownMenuSub>
                  )}
                  {((isolationConfig && !isLeaf) || canIsolate) && (
                    <DropdownMenuSub>
                      <DropdownMenuSubTrigger>
                        <Zap className="size-4" />
                        Operational
                      </DropdownMenuSubTrigger>
                      <DropdownMenuSubContent className="w-60">
                        {isolationConfig && !isLeaf && (
                          <DropdownMenuItem onSelect={() => runServiceTrace()}>
                            <Gauge className="size-4" />
                            Service trace (load served)
                          </DropdownMenuItem>
                        )}
                        {canIsolate && (
                          <DropdownMenuItem onSelect={runIsolate}>
                            <Zap className="size-4" />
                            Isolate from here
                          </DropdownMenuItem>
                        )}
                      </DropdownMenuSubContent>
                    </DropdownMenuSub>
                  )}
                  <DropdownMenuSeparator />
                  {/* Supply path options are direct-run submenu items -- pick a
                      variant and it runs immediately (no check-then-run). The two
                      axes are the switch view (energized vs full wired) and how far
                      up to climb (section head vs the primary source). Hidden on a
                      SOURCE feature (isSourceFeature): a source IS where supply
                      originates, so "trace up to your source" is meaningless there.
                      Transformers are secondary sources and keep it. */}
                  {!isSourceFeature && (
                  <DropdownMenuSub>
                    <DropdownMenuSubTrigger>
                      <Waypoints className="size-4" />
                      Supply path…
                    </DropdownMenuSubTrigger>
                    <DropdownMenuSubContent className="w-72">
                      <DropdownMenuItem
                        onSelect={() =>
                          runSupplyPathTrace(undefined, { respectSwitchState: true, toPrimarySource: false })
                        }
                      >
                        <BatteryCharging className="size-4" />
                        <span className="flex flex-col">
                          Energized route
                          <span className="text-muted-foreground text-xs">Live path, stops at open switches</span>
                        </span>
                      </DropdownMenuItem>
                      <DropdownMenuItem
                        onSelect={() =>
                          runSupplyPathTrace(undefined, { respectSwitchState: false, toPrimarySource: false })
                        }
                      >
                        <Waypoints className="size-4" />
                        <span className="flex flex-col">
                          Full wired route
                          <span className="text-muted-foreground text-xs">Ignores open switches (as-built)</span>
                        </span>
                      </DropdownMenuItem>
                      <DropdownMenuItem
                        onSelect={() =>
                          runSupplyPathTrace(undefined, { respectSwitchState: true, toPrimarySource: true })
                        }
                      >
                        <Network className="size-4" />
                        <span className="flex flex-col">
                          To primary source
                          <span className="text-muted-foreground text-xs">
                            Climbs past the section head to the substation breaker
                          </span>
                        </span>
                      </DropdownMenuItem>
                    </DropdownMenuSubContent>
                  </DropdownMenuSub>
                  )}
                  <DropdownMenuItem
                    onSelect={() => {
                      if (!selected) return
                      useTracePathStore
                        .getState()
                        .begin({ featureClassCode: selected.featureClassCode, featureId: selected.featureId })
                      useToastStore
                        .getState()
                        .pushWarning("Trace path: click the destination feature on the map (Esc to cancel).")
                    }}
                  >
                    <Route className="size-4" />
                    Trace path to…
                  </DropdownMenuItem>
                  {canWriteCircuits && (
                    <DropdownMenuItem onSelect={() => setCircuitSourceOpen(true)}>
                      <Network className="size-4" />
                      Set as circuit source…
                    </DropdownMenuItem>
                  )}
                  <DropdownMenuSeparator />
                  {studyOK("lv_network") && (
                    <>
                      <DropdownMenuItem onSelect={armLoadFlow}>
                        <Activity className="size-4" />
                        Analyze LV network (from secondary)…
                      </DropdownMenuItem>
                      <DropdownMenuSeparator />
                    </>
                  )}
                  {/* Each study is gated on the class's effective set (studyOK) so a
                      study that doesn't apply to this feature's class, or was pruned
                      by an admin, never appears; an empty submenu is hidden whole. */}
                  {anyLoadFlow && (
                    <DropdownMenuSub>
                      <DropdownMenuSubTrigger>
                        <Activity className="size-4" />
                        Load flow
                      </DropdownMenuSubTrigger>
                      <DropdownMenuSubContent>
                        {studyOK("load_flow_auto") && (
                          <DropdownMenuItem onSelect={armAutoLoadFlow}>
                            <Workflow className="size-4" />
                            Load flow (auto)…
                          </DropdownMenuItem>
                        )}
                        {studyOK("load_flow") && (
                          <DropdownMenuItem onSelect={armLoadFlow}>
                            <Activity className="size-4" />
                            Run load flow…
                          </DropdownMenuItem>
                        )}
                        {studyOK("voltage_profile") && (
                          <DropdownMenuItem onSelect={armVoltageProfile}>
                            <TrendingDown className="size-4" />
                            Voltage profile &amp; losses…
                          </DropdownMenuItem>
                        )}
                        {studyOK("meshed_load_flow") && (
                          <DropdownMenuItem onSelect={armMeshedLoadFlow}>
                            <Spline className="size-4" />
                            Meshed load flow…
                          </DropdownMenuItem>
                        )}
                        {studyOK("nodal_load_flow") && (
                          <DropdownMenuItem onSelect={armNodalLoadFlow}>
                            <Network className="size-4" />
                            Nodal load flow (Y-bus)…
                          </DropdownMenuItem>
                        )}
                        {studyOK("unbalanced_load_flow") && (
                          <DropdownMenuItem onSelect={armUnbalancedLoadFlow}>
                            <Waves className="size-4" />
                            Unbalanced load flow (3-phase)…
                          </DropdownMenuItem>
                        )}
                      </DropdownMenuSubContent>
                    </DropdownMenuSub>
                  )}
                  {anyFaultProtection && (
                    <DropdownMenuSub>
                      <DropdownMenuSubTrigger>
                        <TriangleAlert className="size-4" />
                        Fault &amp; protection
                      </DropdownMenuSubTrigger>
                      <DropdownMenuSubContent>
                        {studyOK("fault_current") && (
                          <DropdownMenuItem onSelect={armFaultCurrent}>
                            <TriangleAlert className="size-4" />
                            Fault current…
                          </DropdownMenuItem>
                        )}
                        <DropdownMenuItem onSelect={armFaultLocation}>
                          <Crosshair className="size-4" />
                          Locate fault…
                        </DropdownMenuItem>
                        {studyOK("coordination") && (
                          <DropdownMenuItem onSelect={armCoordination}>
                            <ChartSpline className="size-4" />
                            Protection coordination…
                          </DropdownMenuItem>
                        )}
                        {studyOK("ampacity") && (
                          <DropdownMenuItem onSelect={armAmpacity}>
                            <Thermometer className="size-4" />
                            Run ampacity…
                          </DropdownMenuItem>
                        )}
                      </DropdownMenuSubContent>
                    </DropdownMenuSub>
                  )}
                  {anyCapacity && (
                    <DropdownMenuSub>
                      <DropdownMenuSubTrigger>
                        <Sun className="size-4" />
                        Capacity &amp; planning
                      </DropdownMenuSubTrigger>
                      <DropdownMenuSubContent>
                        {studyOK("contingency") && (
                          <DropdownMenuItem onSelect={armContingency}>
                            <Shuffle className="size-4" />
                            Contingency &amp; backfeed…
                          </DropdownMenuItem>
                        )}
                        {studyOK("hosting") && (
                          <DropdownMenuItem onSelect={armHosting}>
                            <Sun className="size-4" />
                            DER hosting capacity…
                          </DropdownMenuItem>
                        )}
                        {studyOK("ev_hosting") && (
                          <DropdownMenuItem onSelect={armEvHosting}>
                            <BatteryCharging className="size-4" />
                            EV feeder headroom…
                          </DropdownMenuItem>
                        )}
                        {studyOK("load_growth") && (
                          <DropdownMenuItem onSelect={armLoadGrowth}>
                            <TrendingUp className="size-4" />
                            Load-growth forecast…
                          </DropdownMenuItem>
                        )}
                        {studyOK("volt_var") && (
                          <DropdownMenuItem onSelect={armVoltVar}>
                            <Gauge className="size-4" />
                            Volt-VAR (capacitor switching)…
                          </DropdownMenuItem>
                        )}
                        {studyOK("tap_optimization") && (
                          <DropdownMenuItem onSelect={armTapOptimization}>
                            <SlidersHorizontal className="size-4" />
                            Tap optimization (voltage regulators)…
                          </DropdownMenuItem>
                        )}
                        {studyOK("storage_dispatch") && (
                          <DropdownMenuItem onSelect={armStorageDispatch}>
                            <BatteryFull className="size-4" />
                            Storage dispatch (batteries)…
                          </DropdownMenuItem>
                        )}
                        {studyOK("vvo") && (
                          <DropdownMenuItem onSelect={armVvo}>
                            <Wand2 className="size-4" />
                            Volt-VAR Optimization (all controls)…
                          </DropdownMenuItem>
                        )}
                      </DropdownMenuSubContent>
                    </DropdownMenuSub>
                  )}
                  {/* Asset ownership (migration 0129/ownership axis): only on a
                      transformer, the boundary where a private network is declared.
                      Utility is the default, so a utility transformer still shows
                      the action (to mark it) but nothing stays marked unless set. */}
                  {isTransformerBoundary && (
                    <>
                      <DropdownMenuSeparator />
                      <DropdownMenuItem
                        disabled={markDownstreamPrivate.isPending}
                        onSelect={handleMarkOwnershipPrivate}
                      >
                        <Lock className="size-4" />
                        {ownershipIsPrivate ? "Re-mark downstream private" : "Mark downstream private"}
                      </DropdownMenuItem>
                      <DropdownMenuItem disabled={resyncOwnership.isPending} onSelect={handleResyncOwnership}>
                        <RefreshCw className="size-4" />
                        Re-sync ownership
                      </DropdownMenuItem>
                    </>
                  )}
                </DropdownMenuContent>
              </DropdownMenu>
              )}
              {!oneLineOpen && (
                <Button
                  variant="ghost"
                  size="icon"
                  className="size-7"
                  title="Edit"
                  disabled={Boolean(feature?.deleted_at)}
                  onClick={() => useEditStore.getState().start(selected.featureClassCode, selected.featureId)}
                >
                  <Pencil className="size-4" />
                  <span className="sr-only">Edit</span>
                </Button>
              )}
              {/* Hidden entirely in the schematic-view context unless it still has a
                  non-mutating item (a diagram action) to show. */}
              {(!oneLineOpen || hasDiagramActions) && (
                <DropdownMenu>
                  <DropdownMenuTrigger asChild>
                    <Button variant="ghost" size="icon" className="size-7" title="More actions">
                      <MoreHorizontal className="size-4" />
                      <span className="sr-only">More actions</span>
                    </Button>
                  </DropdownMenuTrigger>
                  <DropdownMenuContent align="end" className="w-52">
                    {!oneLineOpen && (
                      <>
                        <DropdownMenuItem
                          disabled={!feature?.geometry || Boolean(feature?.deleted_at)}
                          onSelect={handleDuplicate}
                        >
                          <Copy className="size-4" />
                          Duplicate
                        </DropdownMenuItem>
                        <DropdownMenuSeparator />
                      </>
                    )}
                    {isDiagramContainer && diagrams.length === 0 && (
                      <DropdownMenuItem onSelect={() => setCreatingDiagram(true)}>
                        <Workflow className="size-4" />
                        Create diagram
                      </DropdownMenuItem>
                    )}
                    {diagrams.length === 1 && (
                      <DropdownMenuItem onSelect={() => useDiagramStore.getState().open(diagrams[0].id)}>
                        <Workflow className="size-4" />
                        View diagram
                      </DropdownMenuItem>
                    )}
                    {diagrams.length > 1 &&
                      diagrams.map((d) => (
                        <DropdownMenuItem key={d.id} onSelect={() => useDiagramStore.getState().open(d.id)}>
                          <Workflow className="size-4" />
                          View {d.name || d.diagram_type}
                        </DropdownMenuItem>
                      ))}
                    {!oneLineOpen && (
                      <>
                        {hasDiagramActions && <DropdownMenuSeparator />}
                        <DropdownMenuItem
                          disabled={Boolean(feature?.deleted_at)}
                          onSelect={() => setDeleteDialogOpen(true)}
                          className="text-destructive focus:text-destructive"
                        >
                          <Trash2 className="size-4" />
                          Delete feature
                        </DropdownMenuItem>
                      </>
                    )}
                  </DropdownMenuContent>
                </DropdownMenu>
              )}
              <div className="bg-border mx-0.5 h-4 w-px" />
            </>
          )}
          {minimizeButton}
          {showCloseButton && (
            <Button variant="ghost" size="icon" className="size-7" title="Close" onClick={clear}>
              <X className="size-4" />
              <span className="sr-only">Close</span>
            </Button>
          )}
        </div>
      </div>

      <DeleteFeatureDialog
        featureClassCode={selected.featureClassCode}
        featureId={selected.featureId}
        className={className}
        open={deleteDialogOpen}
        onOpenChange={setDeleteDialogOpen}
      />


      {circuitSourceOpen && (
        <SetCircuitSourceDialog
          feature={{ featureClassCode: selected.featureClassCode, featureId: selected.featureId }}
          onClose={() => setCircuitSourceOpen(false)}
        />
      )}

      {!minimized && (
      <div className="flex min-h-0 flex-1">
        <div className="scrollbar-thin bg-muted/40 flex w-32 shrink-0 flex-col gap-0.5 overflow-y-auto border-r border-[oklch(0.45_0.10_252)]/10 dark:border-primary/10 p-1">
          {visibleTabs.map((tab) => {
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
                  // Selected/hover tints echo the light-mode blue chrome (the
                  // panel header + app bars); dark: restores the warm accent.
                  !locked &&
                    activeTab === tab.id &&
                    "bg-[oklch(0.45_0.10_252)]/15 text-[oklch(0.45_0.10_252)] dark:bg-accent dark:text-accent-foreground",
                  !locked &&
                    activeTab !== tab.id &&
                    "text-muted-foreground hover:bg-[oklch(0.45_0.10_252)]/10 dark:hover:bg-accent/50",
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
              {activeTab === "terminals" && isConnectable && <TerminalsTab />}
              {activeTab === "relationships" && <RelationshipsTab />}
              {activeTab === "materials" && <MaterialsTab onEditClick={() => setActiveTab("attributes")} />}
              {activeTab === "works" && <WorksTab />}
              {activeTab === "faults" && <FaultsTab />}
              {activeTab === "operations" && <OperationsTab />}
              {activeTab === "safety" && <SafetyTab />}
              {activeTab === "spatial" && <SpatialTab />}
              {activeTab === "tags" && <TagsTab />}
              {activeTab === "files" && (
                <FeatureAttachments
                  featureClassCode={selected.featureClassCode}
                  featureId={selected.featureId}
                />
              )}
              {activeTab === "links" && (
                <LinksTab
                  featureClassCode={selected.featureClassCode}
                  featureId={selected.featureId}
                />
              )}
              {activeTab === "history" && (
                <HistoryTab
                  featureClassCode={selected.featureClassCode}
                  featureId={selected.featureId}
                />
              )}
              {activeTab === "gps" && (
                <GpsTab
                  featureClassCode={selected.featureClassCode}
                  featureId={selected.featureId}
                />
              )}
            </>
          )}
        </div>
      </div>
      )}
      {resizeHandle}
    </div>
  )
}
