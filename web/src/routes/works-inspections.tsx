import { useEffect, useMemo, useState } from "react"
import { ChevronDown, ChevronRight, ClipboardCheck, ListChecks, Loader2, Plus, Trash2 } from "lucide-react"

import { FormDialog } from "@/components/admin/form-dialog"
import { inputClass } from "@/components/map/field-utils"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import {
  type InspectionResult,
  type ResponseType,
  type Severity,
  type WorkOrder,
  useCreateInspectionTemplate,
  useInspection,
  useInspectionTemplate,
  useInspectionTemplates,
  useInspections,
  useRecordInspection,
  useSetTemplateActive,
} from "@/hooks/use-wam"
import { ApiError } from "@/lib/api"
import { useToastStore } from "@/stores/toast-store"

// Phase 59 Slice 2 UI: inspection templates + execution, rendered as two extra
// cards at the bottom of the Works page. Kept self-contained (its own small
// helpers) so it only needs a single import + render into works.tsx.

const labelClass = "text-xs font-medium"
const RESPONSE_TYPES: ResponseType[] = ["pass_fail", "rating", "numeric", "text"]
const SEVERITIES: Severity[] = ["low", "medium", "high", "critical"]

function toastError(err: unknown, fallback: string) {
  useToastStore.getState().pushError(err instanceof ApiError ? err.message : fallback)
}

function humanize(s: string): string {
  return s.replace(/_/g, " ")
}

function shortId(id: string): string {
  return id.slice(0, 8)
}

function formatWhen(iso: string): string {
  return new Date(iso).toLocaleString()
}

const RESULT_BADGE: Record<InspectionResult, string> = {
  pass: "bg-emerald-100 text-emerald-800 dark:bg-emerald-950 dark:text-emerald-300",
  fail: "bg-rose-100 text-rose-800 dark:bg-rose-950 dark:text-rose-300",
  conditional: "bg-amber-100 text-amber-800 dark:bg-amber-950 dark:text-amber-300",
}

function Badge({ text, cls }: { text: string; cls: string }) {
  return <span className={`rounded px-1.5 py-0.5 text-xs font-medium capitalize ${cls}`}>{humanize(text)}</span>
}

// --- new-template dialog ---------------------------------------------------

interface ItemDraft {
  prompt: string
  response_type: ResponseType
  fail_severity: Severity | ""
  guidance: string
}

function emptyItem(): ItemDraft {
  return { prompt: "", response_type: "pass_fail", fail_severity: "medium", guidance: "" }
}

/** Define a reusable checklist. Code, name and at least one prompt are required
 * (server-enforced). An item's fail severity, when set, arms the auto-defect. */
function NewTemplateDialog({ onClose }: { onClose: () => void }) {
  const [code, setCode] = useState("")
  const [name, setName] = useState("")
  const [assetClass, setAssetClass] = useState("")
  const [description, setDescription] = useState("")
  const [items, setItems] = useState<ItemDraft[]>([emptyItem()])
  const create = useCreateInspectionTemplate()

  const validItems = items.filter((it) => it.prompt.trim() !== "")
  const canSave = code.trim() !== "" && name.trim() !== "" && validItems.length > 0 && !create.isPending

  function patchItem(idx: number, patch: Partial<ItemDraft>) {
    setItems((prev) => prev.map((it, i) => (i === idx ? { ...it, ...patch } : it)))
  }

  function handleSave() {
    create.mutate(
      {
        code: code.trim(),
        name: name.trim(),
        asset_class: assetClass.trim() || undefined,
        description: description.trim() || undefined,
        items: validItems.map((it) => ({
          prompt: it.prompt.trim(),
          response_type: it.response_type,
          fail_severity: it.fail_severity || undefined,
          guidance: it.guidance.trim() || undefined,
        })),
      },
      { onSuccess: onClose, onError: (err) => toastError(err, "Could not create the template.") },
    )
  }

  return (
    <FormDialog
      open
      onOpenChange={(next) => next || onClose()}
      title="New inspection template"
      description="A reusable checklist. A failing item with a severity auto-raises a defect when the inspection is recorded."
      onSubmit={handleSave}
      submitLabel="Create template"
      canSubmit={canSave}
      saving={create.isPending}
    >
      <div className="flex gap-2">
        <div className="flex flex-1 flex-col gap-1">
          <label className={labelClass}>Code</label>
          <input className={inputClass} value={code} onChange={(e) => setCode(e.target.value)} placeholder="e.g. MV_SWGR_ANNUAL" autoFocus />
        </div>
        <div className="flex flex-[2] flex-col gap-1">
          <label className={labelClass}>Name</label>
          <input className={inputClass} value={name} onChange={(e) => setName(e.target.value)} placeholder="e.g. MV switchgear annual inspection" />
        </div>
      </div>

      <div className="flex gap-2">
        <div className="flex flex-1 flex-col gap-1">
          <label className={labelClass}>Asset class</label>
          <input className={inputClass} value={assetClass} onChange={(e) => setAssetClass(e.target.value)} placeholder="Optional scope" />
        </div>
        <div className="flex flex-[2] flex-col gap-1">
          <label className={labelClass}>Description</label>
          <input className={inputClass} value={description} onChange={(e) => setDescription(e.target.value)} placeholder="Optional" />
        </div>
      </div>

      <div className="flex flex-col gap-2">
        <div className="flex items-center justify-between">
          <label className={labelClass}>Checklist items</label>
          <Button type="button" variant="ghost" size="sm" onClick={() => setItems((p) => [...p, emptyItem()])}>
            <Plus className="size-4" />
            Add item
          </Button>
        </div>
        {items.map((it, idx) => (
          <div key={idx} className="flex flex-col gap-1 rounded-md border p-2">
            <div className="flex items-center gap-2">
              <span className="text-muted-foreground w-5 text-xs">{idx + 1}.</span>
              <input
                className={inputClass}
                value={it.prompt}
                onChange={(e) => patchItem(idx, { prompt: e.target.value })}
                placeholder="e.g. Any oil leaks at bushings?"
              />
              {items.length > 1 && (
                <Button type="button" variant="ghost" size="sm" className="text-muted-foreground shrink-0" onClick={() => setItems((p) => p.filter((_, i) => i !== idx))}>
                  <Trash2 className="size-4" />
                </Button>
              )}
            </div>
            <div className="flex gap-2 pl-7">
              <div className="flex flex-1 flex-col gap-1">
                <label className="text-muted-foreground text-[11px]">Response</label>
                <select className={inputClass} value={it.response_type} onChange={(e) => patchItem(idx, { response_type: e.target.value as ResponseType })}>
                  {RESPONSE_TYPES.map((t) => (
                    <option key={t} value={t}>
                      {humanize(t)}
                    </option>
                  ))}
                </select>
              </div>
              <div className="flex flex-1 flex-col gap-1">
                <label className="text-muted-foreground text-[11px]">Fail severity</label>
                <select className={inputClass} value={it.fail_severity} onChange={(e) => patchItem(idx, { fail_severity: e.target.value as Severity | "" })}>
                  <option value="">None (informational)</option>
                  {SEVERITIES.map((s) => (
                    <option key={s} value={s}>
                      {s}
                    </option>
                  ))}
                </select>
              </div>
            </div>
          </div>
        ))}
      </div>
    </FormDialog>
  )
}

// --- record-inspection dialog ----------------------------------------------

interface ResultDraft {
  template_item_id?: string
  prompt: string
  response_type: ResponseType
  fail_severity?: Severity
  outcome: "pass" | "fail" | "na"
  value: string
  notes: string
}

/** Fill out a checklist against an asset. Picking a template loads its items;
 * picking an open inspection work order prefills the asset and offers to close
 * it out. Failing, severity-bearing items become defects server-side. */
function RecordInspectionDialog({ workOrders, onClose }: { workOrders: WorkOrder[]; onClose: () => void }) {
  const templates = useInspectionTemplates({ activeOnly: true })
  const [templateId, setTemplateId] = useState("")
  const templateDetail = useInspectionTemplate(templateId || null)

  const inspectionOrders = useMemo(
    () => workOrders.filter((o) => o.work_type === "inspection" && o.status !== "completed" && o.status !== "cancelled"),
    [workOrders],
  )
  const [workOrderId, setWorkOrderId] = useState("")
  const [completeWorkOrder, setCompleteWorkOrder] = useState(true)
  const [assetClass, setAssetClass] = useState("")
  const [assetId, setAssetId] = useState("")
  const [inspectedBy, setInspectedBy] = useState("")
  const [results, setResults] = useState<ResultDraft[]>([])
  const record = useRecordInspection()

  // When a template's items arrive, seed the editable result rows. Also default
  // the asset class from the template's scope when it has one.
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
    if (t.asset_class) setAssetClass((prev) => prev || t.asset_class || "")
  }, [templateDetail.data])

  function pickWorkOrder(id: string) {
    setWorkOrderId(id)
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
    >
      <div className="flex flex-col gap-1">
        <label className={labelClass}>Template</label>
        <select className={inputClass} value={templateId} onChange={(e) => setTemplateId(e.target.value)}>
          <option value="">Select a checklist…</option>
          {(templates.data?.templates ?? []).map((t) => (
            <option key={t.id} value={t.id}>
              {t.name}
            </option>
          ))}
        </select>
        {templates.data && templates.data.templates.length === 0 && (
          <span className="text-muted-foreground text-xs">No templates yet — create one first.</span>
        )}
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

      <div className="flex gap-2">
        <div className="flex flex-1 flex-col gap-1">
          <label className={labelClass}>Asset class</label>
          <input className={inputClass} value={assetClass} onChange={(e) => setAssetClass(e.target.value)} placeholder="e.g. MV_SWITCHGEAR_PANEL" />
        </div>
        <div className="flex flex-[2] flex-col gap-1">
          <label className={labelClass}>Asset ID</label>
          <input className={inputClass} value={assetId} onChange={(e) => setAssetId(e.target.value)} placeholder="UUID of the inspected asset" />
        </div>
      </div>

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
                  <span className="flex-1 text-sm">
                    {idx + 1}. {r.prompt}
                    {r.fail_severity && <span className="text-muted-foreground text-xs"> · {r.fail_severity}</span>}
                  </span>
                  <select
                    className={`${inputClass} w-24 shrink-0`}
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

// --- inspection row (expandable) -------------------------------------------

function InspectionRow({ id, summary }: { id: string; summary: string }) {
  const [open, setOpen] = useState(false)
  const detail = useInspection(open ? id : null)
  const items = detail.data?.items ?? []

  return (
    <div className="rounded-md">
      <button
        type="button"
        onClick={() => setOpen((o) => !o)}
        className="hover:bg-muted/40 flex w-full items-center gap-2 rounded-md px-2 py-2 text-left"
      >
        {open ? <ChevronDown className="size-4 shrink-0" /> : <ChevronRight className="size-4 shrink-0" />}
        <span className="text-sm">{summary}</span>
      </button>
      {open && (
        <div className="text-muted-foreground flex flex-col gap-1 px-8 pb-2 text-xs">
          {detail.isPending ? (
            <span className="flex items-center gap-2">
              <Loader2 className="size-3 animate-spin" /> Loading…
            </span>
          ) : items.length === 0 ? (
            <span>No recorded items.</span>
          ) : (
            items.map((it) => (
              <div key={it.id} className="flex items-center gap-2">
                <span className={it.passed === false ? "text-rose-600 dark:text-rose-400" : it.passed ? "text-emerald-600 dark:text-emerald-400" : ""}>
                  {it.passed === false ? "✗" : it.passed ? "✓" : "—"}
                </span>
                <span className="text-foreground">{it.prompt}</span>
                {it.value_numeric != null && <span>· {it.value_numeric}</span>}
                {it.value_text && <span>· {it.value_text}</span>}
                {it.defect_id && <span className="text-rose-600 dark:text-rose-400">· defect {shortId(it.defect_id)}</span>}
              </div>
            ))
          )}
        </div>
      )}
    </div>
  )
}

// --- panel -----------------------------------------------------------------

/** Inspection templates + execution, two cards appended to the Works page. */
export function InspectionsPanel({ canWrite, workOrders }: { canWrite: boolean; workOrders: WorkOrder[] }) {
  const templates = useInspectionTemplates()
  const inspections = useInspections()
  const setActive = useSetTemplateActive()

  const [creatingTemplate, setCreatingTemplate] = useState(false)
  const [recording, setRecording] = useState(false)

  const templateRows = templates.data?.templates ?? []
  const inspectionRows = inspections.data?.inspections ?? []

  return (
    <>
      <Card>
        <CardHeader className="pb-2">
          <div className="flex items-center justify-between gap-2">
            <div>
              <CardTitle className="flex items-center gap-2 text-sm">
                <ListChecks className="size-4" />
                Inspection templates
              </CardTitle>
              <CardDescription>
                {templateRows.length === 0 ? "No templates." : `${templateRows.length} checklist${templateRows.length === 1 ? "" : "s"}.`}
              </CardDescription>
            </div>
            {canWrite && (
              <Button variant="outline" size="sm" onClick={() => setCreatingTemplate(true)}>
                <Plus className="size-4" />
                New template
              </Button>
            )}
          </div>
        </CardHeader>
        <CardContent className="pt-0">
          {templates.isPending ? (
            <LoadingRow label="Loading templates…" />
          ) : templates.isError ? (
            <p className="text-destructive text-sm">Could not load templates.</p>
          ) : templateRows.length > 0 ? (
            <div className="flex flex-col">
              {templateRows.map((t) => (
                <div key={t.id} className="hover:bg-muted/40 flex flex-wrap items-center gap-x-3 gap-y-1 rounded-md px-2 py-2">
                  <span className="text-sm font-medium">{t.name}</span>
                  <span className="text-muted-foreground text-xs">
                    {t.code}
                    {t.asset_class && <> · {t.asset_class}</>}
                    {!t.active && <> · retired</>}
                  </span>
                  {canWrite && (
                    <Button
                      variant="ghost"
                      size="sm"
                      className="text-muted-foreground ml-auto"
                      disabled={setActive.isPending}
                      onClick={() =>
                        setActive.mutate(
                          { id: t.id, active: !t.active },
                          { onError: (err) => toastError(err, "Could not update the template.") },
                        )
                      }
                    >
                      {t.active ? "Retire" : "Restore"}
                    </Button>
                  )}
                </div>
              ))}
            </div>
          ) : (
            <p className="text-muted-foreground text-sm">No inspection templates yet.</p>
          )}
        </CardContent>
      </Card>

      <Card>
        <CardHeader className="pb-2">
          <div className="flex items-center justify-between gap-2">
            <div>
              <CardTitle className="flex items-center gap-2 text-sm">
                <ClipboardCheck className="size-4" />
                Inspections
              </CardTitle>
              <CardDescription>
                {inspectionRows.length === 0 ? "No inspections recorded." : `${inspectionRows.length} recorded.`}
              </CardDescription>
            </div>
            {canWrite && (
              <Button size="sm" onClick={() => setRecording(true)}>
                <ClipboardCheck className="size-4" />
                Record inspection
              </Button>
            )}
          </div>
        </CardHeader>
        <CardContent className="pt-0">
          {inspections.isPending ? (
            <LoadingRow label="Loading inspections…" />
          ) : inspections.isError ? (
            <p className="text-destructive text-sm">Could not load inspections.</p>
          ) : inspectionRows.length > 0 ? (
            <div className="flex flex-col">
              {inspectionRows.map((insp) => (
                <div key={insp.id} className="flex items-center gap-2">
                  <Badge text={insp.overall_result} cls={RESULT_BADGE[insp.overall_result]} />
                  <div className="min-w-0 flex-1">
                    <InspectionRow
                      id={insp.id}
                      summary={`${insp.asset_class} · ${shortId(insp.asset_id)}${insp.inspected_by ? ` · ${insp.inspected_by}` : ""} · ${formatWhen(insp.inspected_at)}`}
                    />
                  </div>
                </div>
              ))}
            </div>
          ) : (
            <p className="text-muted-foreground text-sm">No inspections recorded yet. Record one against an asset.</p>
          )}
        </CardContent>
      </Card>

      {creatingTemplate && <NewTemplateDialog onClose={() => setCreatingTemplate(false)} />}
      {recording && <RecordInspectionDialog workOrders={workOrders} onClose={() => setRecording(false)} />}
    </>
  )
}

function LoadingRow({ label }: { label: string }) {
  return (
    <div className="text-muted-foreground flex items-center gap-2 text-sm">
      <Loader2 className="size-4 animate-spin" />
      {label}
    </div>
  )
}
