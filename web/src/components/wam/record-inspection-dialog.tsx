import { useEffect, useMemo, useState } from "react"
import { Loader2, Lock, Search } from "lucide-react"

import { FormDialog } from "@/components/admin/form-dialog"
import { inputClass } from "@/components/map/field-utils"
import { useFeatureClasses } from "@/hooks/use-feature-classes"
import { useFeatureRegistrySearch } from "@/hooks/use-feature-registry"
import {
  type ResponseType,
  type Severity,
  type WorkOrder,
  useInspectionTemplate,
  useInspectionTemplates,
  useRecordInspection,
} from "@/hooks/use-wam"
import { ApiError } from "@/lib/api"
import { cn } from "@/lib/utils"
import { useToastStore } from "@/stores/toast-store"

// The shared "record an inspection" dialog: fill a checklist template out against
// an asset. Reused by the Works page (ad-hoc: pick the asset here) and the map
// feature panel's Works tab (the asset is already selected, so it's passed in and
// locked). Failing, severity-bearing items become defects server-side.

const labelClass = "text-xs font-medium"

function toastError(err: unknown, fallback: string) {
  useToastStore.getState().pushError(err instanceof ApiError ? err.message : fallback)
}

function shortId(id: string): string {
  return id.slice(0, 8)
}

interface ResultDraft {
  template_item_id?: string
  prompt: string
  response_type: ResponseType
  fail_severity?: Severity
  outcome: "pass" | "fail" | "na"
  value: string
  notes: string
}

/** The asset this inspection is about, when the caller already knows it (e.g. the
 * feature panel). When given, the picker is replaced by a locked summary row. */
export interface InitialAsset {
  assetClass: string
  assetId: string
  /** A human label for the locked header; falls back to class · short-id. */
  label?: string
}

export function RecordInspectionDialog({
  workOrders,
  initialAsset,
  onClose,
}: {
  workOrders: WorkOrder[]
  initialAsset?: InitialAsset
  onClose: () => void
}) {
  const locked = Boolean(initialAsset)
  const templates = useInspectionTemplates({ activeOnly: true })
  const [templateId, setTemplateId] = useState("")
  const templateDetail = useInspectionTemplate(templateId || null)

  const [assetClass, setAssetClass] = useState(initialAsset?.assetClass ?? "")
  const [assetId, setAssetId] = useState(initialAsset?.assetId ?? "")
  const [inspectedBy, setInspectedBy] = useState("")
  const [results, setResults] = useState<ResultDraft[]>([])
  const record = useRecordInspection()

  // Scope the template picker to the asset's class: a template with no asset_class
  // is universal (shows for everything); a scoped one only shows for its class. So a
  // fuse never sees a transformer checklist. With no class picked yet, show all.
  const scopedTemplates = useMemo(() => {
    const all = templates.data?.templates ?? []
    const cls = assetClass.trim()
    if (!cls) return all
    return all.filter((t) => !t.asset_class || t.asset_class === cls)
  }, [templates.data, assetClass])

  // Inspection work orders this inspection can fulfil. When locked to an asset,
  // only that asset's orders are relevant.
  const inspectionOrders = useMemo(
    () =>
      workOrders.filter(
        (o) =>
          o.work_type === "inspection" &&
          o.status !== "completed" &&
          o.status !== "cancelled" &&
          (!locked || (o.asset_class === assetClass && o.asset_id === assetId)),
      ),
    [workOrders, locked, assetClass, assetId],
  )
  const [workOrderId, setWorkOrderId] = useState("")
  const [completeWorkOrder, setCompleteWorkOrder] = useState(true)

  // When a template's items arrive, seed editable result rows. Default the asset
  // class from the template's scope only when not locked and not already set.
  useEffect(() => {
    const t = templateDetail.data
    if (!t) return
    setResults(
      (t.items ?? []).map((it) => ({
        template_item_id: it.id,
        prompt: it.prompt,
        response_type: it.response_type,
        fail_severity: it.fail_severity ?? undefined,
        outcome: "pass",
        value: "",
        notes: "",
      })),
    )
    if (!locked && t.asset_class) setAssetClass((prev) => prev || t.asset_class || "")
  }, [templateDetail.data, locked])

  // If the asset class changes to one the selected template isn't scoped to, drop
  // the selection — you can't record a transformer checklist against a fuse.
  useEffect(() => {
    if (!templateId) return
    const t = (templates.data?.templates ?? []).find((x) => x.id === templateId)
    if (t?.asset_class && assetClass.trim() && t.asset_class !== assetClass.trim()) {
      setTemplateId("")
      setResults([])
    }
  }, [assetClass, templateId, templates.data])

  function pickWorkOrder(id: string) {
    setWorkOrderId(id)
    if (locked) return
    const order = inspectionOrders.find((o) => o.id === id)
    if (order?.asset_class) setAssetClass(order.asset_class)
    if (order?.asset_id) setAssetId(order.asset_id)
  }

  function patchResult(idx: number, patch: Partial<ResultDraft>) {
    setResults((prev) => prev.map((r, i) => (i === idx ? { ...r, ...patch } : r)))
  }

  const canSave = assetClass.trim() !== "" && assetId.trim() !== "" && results.length > 0 && !record.isPending

  function handleSave() {
    record.mutate(
      {
        work_order_id: workOrderId || undefined,
        template_id: templateId || undefined,
        asset_class: assetClass.trim(),
        asset_id: assetId.trim(),
        inspected_by: inspectedBy.trim() || undefined,
        complete_work_order: !!workOrderId && completeWorkOrder,
        items: results.map((r, i) => {
          const numeric = r.response_type === "numeric" || r.response_type === "rating"
          const parsed = numeric && r.value.trim() !== "" ? Number(r.value) : NaN
          return {
            template_item_id: r.template_item_id,
            position: i + 1,
            prompt: r.prompt,
            response_type: r.response_type,
            passed: r.outcome === "na" ? undefined : r.outcome === "pass",
            value_text: r.response_type === "text" && r.value.trim() !== "" ? r.value.trim() : undefined,
            value_numeric: Number.isNaN(parsed) ? undefined : parsed,
            fail_severity: r.fail_severity,
            notes: r.notes.trim() || undefined,
          }
        }),
      },
      { onSuccess: onClose, onError: (err) => toastError(err, "Could not record the inspection.") },
    )
  }

  const failCount = results.filter((r) => r.outcome === "fail").length
  const willRaise = results.filter((r) => r.outcome === "fail" && r.fail_severity).length

  return (
    <FormDialog
      open
      onOpenChange={(next) => next || onClose()}
      title="Record inspection"
      description="Fill out a checklist against an asset. Failing items with a severity are logged as defects."
      onSubmit={handleSave}
      submitLabel="Record inspection"
      canSubmit={canSave}
      saving={record.isPending}
      className="max-w-3xl"
    >
      {/* Asset: locked summary when passed in, else a picker */}
      {locked ? (
        <div className="bg-muted/40 flex items-center gap-2 rounded-md border px-3 py-2 text-sm">
          <Lock className="text-muted-foreground size-3.5 shrink-0" />
          <span className="min-w-0 truncate">
            <span className="font-medium">{initialAsset?.label ?? assetClass}</span>
            <span className="text-muted-foreground"> · {shortId(assetId)}</span>
          </span>
        </div>
      ) : (
        <div className="flex flex-col gap-1">
          <label className={labelClass}>Asset</label>
          <AssetPicker
            assetClass={assetClass}
            assetId={assetId}
            onChange={(cls, id) => {
              setAssetClass(cls)
              setAssetId(id)
            }}
          />
        </div>
      )}

      <div className="flex flex-col gap-1">
        <label className={labelClass}>Template</label>
        <select className={inputClass} value={templateId} onChange={(e) => setTemplateId(e.target.value)}>
          <option value="">Select a checklist…</option>
          {scopedTemplates.map((t) => (
            <option key={t.id} value={t.id}>
              {t.name}
              {t.asset_class ? "" : " · any class"}
            </option>
          ))}
        </select>
        {templates.data &&
          (scopedTemplates.length === 0 ? (
            <span className="text-muted-foreground text-xs">
              {assetClass.trim()
                ? `No checklist is scoped to ${assetClass.trim()} (or universal). An admin creates them in Admin → Inspection templates.`
                : "No templates yet — an admin creates them in Admin → Inspection templates."}
            </span>
          ) : assetClass.trim() ? (
            <span className="text-muted-foreground text-xs">Checklists for {assetClass.trim()} and universal ones.</span>
          ) : null)}
      </div>

      {inspectionOrders.length > 0 && (
        <div className="flex flex-col gap-1">
          <label className={labelClass}>Fulfilling work order</label>
          <select className={inputClass} value={workOrderId} onChange={(e) => pickWorkOrder(e.target.value)}>
            <option value="">None (ad-hoc)</option>
            {inspectionOrders.map((o) => (
              <option key={o.id} value={o.id}>
                {o.title} · {shortId(o.id)}
              </option>
            ))}
          </select>
          {workOrderId && (
            <label className="text-muted-foreground flex items-center gap-2 text-xs">
              <input type="checkbox" checked={completeWorkOrder} onChange={(e) => setCompleteWorkOrder(e.target.checked)} />
              Mark this work order completed
            </label>
          )}
        </div>
      )}

      <div className="flex flex-col gap-1">
        <label className={labelClass}>Inspected by</label>
        <input className={inputClass} value={inspectedBy} onChange={(e) => setInspectedBy(e.target.value)} placeholder="Crew / person (optional)" />
      </div>

      {templateDetail.isPending && templateId ? (
        <div className="text-muted-foreground flex items-center gap-2 text-sm">
          <Loader2 className="size-4 animate-spin" />
          Loading checklist…
        </div>
      ) : results.length > 0 ? (
        <div className="flex flex-col gap-2">
          <label className={labelClass}>Checklist</label>
          {results.map((r, idx) => {
            const numeric = r.response_type === "numeric" || r.response_type === "rating"
            const text = r.response_type === "text"
            return (
              <div key={idx} className="flex flex-col gap-1 rounded-md border p-2">
                <div className="flex items-center gap-2">
                  <span className="min-w-0 flex-1 text-sm">
                    {idx + 1}. {r.prompt}
                    {r.fail_severity && <span className="text-muted-foreground text-xs"> · {r.fail_severity}</span>}
                  </span>
                  <select
                    className={cn(inputClass, "w-28 shrink-0")}
                    value={r.outcome}
                    onChange={(e) => patchResult(idx, { outcome: e.target.value as ResultDraft["outcome"] })}
                  >
                    <option value="pass">Pass</option>
                    <option value="fail">Fail</option>
                    <option value="na">N/A</option>
                  </select>
                </div>
                {(numeric || text) && (
                  <input
                    className={inputClass}
                    type={numeric ? "number" : "text"}
                    value={r.value}
                    onChange={(e) => patchResult(idx, { value: e.target.value })}
                    placeholder={numeric ? "Reading" : "Value"}
                  />
                )}
                <input
                  className={inputClass}
                  value={r.notes}
                  onChange={(e) => patchResult(idx, { notes: e.target.value })}
                  placeholder="Notes (optional)"
                />
              </div>
            )
          })}
          <p className="text-muted-foreground text-xs">
            {failCount === 0 ? "All passing." : `${failCount} failing`}
            {willRaise > 0 && ` · ${willRaise} will be logged as ${willRaise === 1 ? "a defect" : "defects"}`}
            {failCount > 0 && willRaise === 0 && " · none arm a defect"}.
          </p>
        </div>
      ) : (
        <p className="text-muted-foreground text-sm">Pick a template to load its checklist.</p>
      )}
    </FormDialog>
  )
}

/** Pick an asset for an ad-hoc inspection: choose a class, then its live features
 * (the feature-registry typeahead — by id, scoped to the class). */
function AssetPicker({
  assetClass,
  assetId,
  onChange,
}: {
  assetClass: string
  assetId: string
  onChange: (cls: string, id: string) => void
}) {
  const classes = useFeatureClasses()
  const [q, setQ] = useState("")
  const search = useFeatureRegistrySearch({ assetClass: assetClass || undefined, q: q.trim() || undefined, live: true })
  const entries = search.data?.entries ?? []

  return (
    <div className="flex flex-col gap-2">
      <select
        className={inputClass}
        value={assetClass}
        onChange={(e) => {
          onChange(e.target.value, "")
          setQ("")
        }}
      >
        <option value="">Select asset class…</option>
        {(classes.data?.feature_classes ?? []).map((c) => (
          <option key={c.code} value={c.code}>
            {c.name}
          </option>
        ))}
      </select>

      {assetClass && (
        <>
          <div className="relative">
            <Search className="text-muted-foreground pointer-events-none absolute left-2 top-1/2 size-3.5 -translate-y-1/2" />
            <input
              className={`${inputClass} pl-7`}
              value={q}
              onChange={(e) => setQ(e.target.value)}
              placeholder="Filter by id…"
            />
          </div>
          <div className="max-h-40 overflow-y-auto rounded-md border">
            {search.isPending ? (
              <div className="text-muted-foreground flex items-center gap-2 p-2 text-xs">
                <Loader2 className="size-3.5 animate-spin" /> Loading…
              </div>
            ) : entries.length === 0 ? (
              <div className="text-muted-foreground p-2 text-xs">No live assets match.</div>
            ) : (
              entries.map((en) => (
                <button
                  key={en.feature_id}
                  type="button"
                  onClick={() => onChange(assetClass, en.feature_id)}
                  className={cn(
                    "flex w-full items-center gap-2 px-2 py-1.5 text-left text-xs",
                    en.feature_id === assetId ? "bg-primary/10 text-primary" : "hover:bg-muted/50",
                  )}
                >
                  <span className="font-mono">{shortId(en.feature_id)}…</span>
                  {en.status && <span className="text-muted-foreground">· {en.status}</span>}
                </button>
              ))
            )}
          </div>
          {assetId && <p className="text-muted-foreground font-mono text-[11px]">Selected: {assetId}</p>}
        </>
      )}
    </div>
  )
}
