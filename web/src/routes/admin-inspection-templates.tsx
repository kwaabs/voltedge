import { useState } from "react"
import { ListChecks, Loader2, Plus, Trash2 } from "lucide-react"

import { FormDialog } from "@/components/admin/form-dialog"
import { inputClass } from "@/components/map/field-utils"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { usePermission } from "@/hooks/use-auth"
import { useFeatureClasses } from "@/hooks/use-feature-classes"
import { type ResponseType, type Severity, useCreateInspectionTemplate, useInspectionTemplates, useSetTemplateActive } from "@/hooks/use-wam"
import { ApiError } from "@/lib/api"
import { useToastStore } from "@/stores/toast-store"

// Admin → Inspection templates. Template authoring is configuration (like the
// fault-mode catalog and data-quality rules), so it lives in Admin and its writes
// need metadata:write; recording an inspection against a template stays on the
// Works page under wam:write. (Server split: GET /inspection-templates = wam:read,
// writes = metadata:write.)

const labelClass = "text-xs font-medium"
const RESPONSE_TYPES: ResponseType[] = ["pass_fail", "rating", "numeric", "text"]
const SEVERITIES: Severity[] = ["low", "medium", "high", "critical"]

function toastError(err: unknown, fallback: string) {
  useToastStore.getState().pushError(err instanceof ApiError ? err.message : fallback)
}

function humanize(s: string): string {
  return s.replace(/_/g, " ")
}

interface ItemDraft {
  prompt: string
  response_type: ResponseType
  fail_severity: Severity | ""
  guidance: string
}

function emptyItem(): ItemDraft {
  return { prompt: "", response_type: "pass_fail", fail_severity: "medium", guidance: "" }
}

export function AdminInspectionTemplatesPage() {
  const templates = useInspectionTemplates()
  const setActive = useSetTemplateActive()
  const canWrite = usePermission()("metadata:write")
  const [creating, setCreating] = useState(false)

  const rows = templates.data?.templates ?? []

  return (
    <div className="mx-auto flex max-w-4xl flex-col gap-4 p-4">
      <div>
        <h1 className="flex items-center gap-2 text-lg font-semibold">
          <ListChecks className="size-5" />
          Inspection templates
        </h1>
        <p className="text-muted-foreground text-sm">
          Reusable inspection checklists. A failing item with a severity auto-raises a defect when the inspection is
          recorded (on the Works page or from a feature on the map).
        </p>
      </div>

      <Card>
        <CardHeader className="pb-2">
          <div className="flex items-center justify-between gap-2">
            <div>
              <CardTitle className="text-sm">Templates</CardTitle>
              <CardDescription>
                {rows.length === 0 ? "No templates yet." : `${rows.length} checklist${rows.length === 1 ? "" : "s"}.`}
              </CardDescription>
            </div>
            {canWrite && (
              <Button variant="outline" size="sm" onClick={() => setCreating(true)}>
                <Plus className="size-4" />
                New template
              </Button>
            )}
          </div>
        </CardHeader>
        <CardContent className="pt-0">
          {templates.isPending ? (
            <div className="text-muted-foreground flex items-center gap-2 text-sm">
              <Loader2 className="size-4 animate-spin" />
              Loading templates…
            </div>
          ) : templates.isError ? (
            <p className="text-destructive text-sm">Could not load templates.</p>
          ) : rows.length > 0 ? (
            <div className="flex flex-col">
              {rows.map((t) => (
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

      {creating && <NewTemplateDialog onClose={() => setCreating(false)} />}
    </div>
  )
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
  const classes = useFeatureClasses()

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
          <label className={labelClass}>Applies to</label>
          <select className={inputClass} value={assetClass} onChange={(e) => setAssetClass(e.target.value)}>
            <option value="">Any class (universal)</option>
            {(classes.data?.feature_classes ?? []).map((c) => (
              <option key={c.code} value={c.code}>
                {c.name}
              </option>
            ))}
          </select>
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
