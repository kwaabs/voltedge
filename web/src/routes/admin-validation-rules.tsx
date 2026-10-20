import { useMemo, useState } from "react"
import { Loader2, Pencil, Plus, ShieldCheck, Trash2 } from "lucide-react"

import { ConfirmDeleteDialog } from "@/components/admin/confirm-delete-dialog"
import { FormDialog } from "@/components/admin/form-dialog"
import { inputClass } from "@/components/map/field-utils"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { useFeatureClasses } from "@/hooks/use-feature-classes"
import { useFeatureClassSchema } from "@/hooks/use-feature-class-schema"
import {
  type ValidationRule,
  useCreateValidationRule,
  useDeleteValidationRule,
  useUpdateValidationRule,
  useValidationRules,
} from "@/hooks/use-validation-rules"
import { ApiError } from "@/lib/api"
import { cn } from "@/lib/utils"
import { useToastStore } from "@/stores/toast-store"

const labelClass = "text-xs font-medium"

function toastError(err: unknown, fallback: string) {
  useToastStore.getState().pushError(err instanceof ApiError ? err.message : fallback)
}

function RuleDialog({
  featureClass,
  existing,
  onClose,
}: {
  featureClass: string
  existing: ValidationRule | null
  onClose: () => void
}) {
  const isEdit = existing !== null
  const { data: schema } = useFeatureClassSchema(featureClass)
  const [fieldName, setFieldName] = useState(existing?.field_name ?? "")
  const [subtypeCode, setSubtypeCode] = useState<string>(
    existing?.subtype_code != null ? String(existing.subtype_code) : "",
  )
  const [severity, setSeverity] = useState<"error" | "warning">(existing?.severity ?? "warning")
  const [message, setMessage] = useState(existing?.message ?? "")
  const [sortOrder, setSortOrder] = useState(String(existing?.sort_order ?? 0))
  const [active, setActive] = useState(existing?.active ?? true)

  const create = useCreateValidationRule()
  const update = useUpdateValidationRule()
  const saving = create.isPending || update.isPending
  const canSave = fieldName.trim().length > 0 && !saving

  function handleSave() {
    const body = {
      severity,
      message: message.trim() || null,
      sort_order: Number(sortOrder) || 0,
      active,
    }
    if (isEdit) {
      update.mutate(
        { id: existing.id, body },
        { onSuccess: onClose, onError: (err) => toastError(err, "Could not update the rule.") },
      )
    } else {
      create.mutate(
        {
          ...body,
          feature_class_code: featureClass,
          field_name: fieldName,
          subtype_code: subtypeCode === "" ? null : Number(subtypeCode),
        },
        { onSuccess: onClose, onError: (err) => toastError(err, "Could not create the rule.") },
      )
    }
  }

  return (
    <FormDialog
      open
      onOpenChange={(next) => next || onClose()}
      title={isEdit ? "Edit rule" : "New completeness rule"}
      description={`A field expected to be populated on ${featureClass}${isEdit ? "" : " (optionally only for one subtype)"}. Violations surface in the validation-issue queue.`}
      onSubmit={handleSave}
      submitLabel={isEdit ? "Save" : "Create"}
      canSubmit={canSave}
      saving={saving}
    >
      <div className="flex flex-col gap-1">
        <label className={labelClass}>
          Field <span className="text-destructive">*</span>
        </label>
        <select
          className={inputClass}
          value={fieldName}
          onChange={(e) => setFieldName(e.target.value)}
          disabled={isEdit}
        >
          <option value="">Select a field…</option>
          {(schema?.fields ?? []).map((f) => (
            <option key={f.name} value={f.name}>
              {f.name}
              {!f.nullable ? " (schema-required)" : ""}
            </option>
          ))}
        </select>
        {isEdit && <span className="text-muted-foreground text-xs">Fixed once created.</span>}
      </div>

      <div className="flex flex-col gap-1">
        <label className={labelClass}>Applies to</label>
        <select
          className={inputClass}
          value={subtypeCode}
          onChange={(e) => setSubtypeCode(e.target.value)}
          disabled={isEdit}
        >
          <option value="">Every feature of the class</option>
          {(schema?.subtypes ?? []).map((s) => (
            <option key={s.code} value={s.code}>
              Only subtype: {s.name}
            </option>
          ))}
        </select>
      </div>

      <div className="flex flex-col gap-1">
        <label className={labelClass}>Severity</label>
        <select className={inputClass} value={severity} onChange={(e) => setSeverity(e.target.value as "error" | "warning")}>
          <option value="warning">Warning</option>
          <option value="error">Error</option>
        </select>
      </div>

      <div className="flex flex-col gap-1">
        <label className={labelClass}>Message (optional)</label>
        <input
          className={inputClass}
          value={message}
          onChange={(e) => setMessage(e.target.value)}
          placeholder="Custom message, else auto-phrased"
        />
      </div>

      <div className="flex items-end gap-3">
        <div className="flex flex-col gap-1">
          <label className={labelClass}>Sort order</label>
          <input
            className={cn(inputClass, "w-24")}
            type="number"
            value={sortOrder}
            onChange={(e) => setSortOrder(e.target.value)}
          />
        </div>
        <label className="flex items-center gap-2 pb-1.5 text-sm">
          <input type="checkbox" checked={active} onChange={(e) => setActive(e.target.checked)} />
          Active
        </label>
      </div>
    </FormDialog>
  )
}

/**
 * Admin editor for the completeness / business-rule catalog behind the attribute
 * audit: the fields EXPECTED to be populated on each class (beyond the schema's
 * required/domain/range), optionally per subtype. Pick a class, add/edit/retire
 * rules; the audit scans existing rows against the active ones and surfaces
 * violations in the validation-issue queue. Gated on metadata:write.
 */
export function AdminValidationRulesPage() {
  const { data: classesData } = useFeatureClasses()
  const classes = useMemo(
    () => [...(classesData?.feature_classes ?? [])].sort((a, b) => a.name.localeCompare(b.name)),
    [classesData],
  )
  const [featureClass, setFeatureClass] = useState("")
  const rules = useValidationRules(featureClass || undefined, true)
  const { data: schema } = useFeatureClassSchema(featureClass || undefined)
  const [dialog, setDialog] = useState<ValidationRule | "new" | null>(null)
  const [deleting, setDeleting] = useState<ValidationRule | null>(null)
  const del = useDeleteValidationRule()

  const rows = rules.data?.rules ?? []
  const subtypeName = (code: number | null) =>
    code == null ? "—" : (schema?.subtypes.find((s) => s.code === code)?.name ?? `#${code}`)

  return (
    <div className="mx-auto flex w-full max-w-4xl flex-col gap-4 p-4">
      <div>
        <h1 className="flex items-center gap-2 text-lg font-semibold">
          <ShieldCheck className="size-5" /> Data-quality rules
        </h1>
        <p className="text-muted-foreground text-sm">
          Fields that are <b>expected</b> to be populated on each asset class — beyond the schema's own
          required/domain/range. The attribute audit scans existing features against these and surfaces gaps in the
          validation-issue queue (Network audit → Issue queue).
        </p>
      </div>

      <Card>
        <CardHeader className="flex flex-row items-end justify-between gap-2">
          <div className="flex flex-col gap-1">
            <CardTitle className="text-base">Feature class</CardTitle>
            <CardDescription>Choose a class to view and edit its completeness rules.</CardDescription>
          </div>
          <div className="flex items-end gap-2">
            <select
              className={cn(inputClass, "w-64")}
              value={featureClass}
              onChange={(e) => setFeatureClass(e.target.value)}
            >
              <option value="">Select a class…</option>
              {classes.map((c) => (
                <option key={c.code} value={c.code}>
                  {c.name}
                </option>
              ))}
            </select>
            {featureClass && (
              <Button size="sm" onClick={() => setDialog("new")}>
                <Plus className="size-4" /> Add rule
              </Button>
            )}
          </div>
        </CardHeader>
        {featureClass && (
          <CardContent>
            {rules.isPending ? (
              <div className="text-muted-foreground flex items-center gap-2 text-sm">
                <Loader2 className="size-4 animate-spin" /> Loading…
              </div>
            ) : rows.length === 0 ? (
              <p className="text-muted-foreground text-sm">
                No completeness rules for this class yet. Add one to flag an expected-but-empty field.
              </p>
            ) : (
              <div className="overflow-hidden rounded-md border">
                <table className="w-full border-collapse text-sm">
                  <thead className="bg-muted/50 text-muted-foreground text-xs">
                    <tr>
                      <th className="px-2 py-1.5 text-left font-medium">Field</th>
                      <th className="px-2 py-1.5 text-left font-medium">Applies to</th>
                      <th className="px-2 py-1.5 text-left font-medium">Severity</th>
                      <th className="px-2 py-1.5 text-left font-medium">Message</th>
                      <th className="px-2 py-1.5"></th>
                    </tr>
                  </thead>
                  <tbody>
                    {rows.map((rule) => (
                      <tr key={rule.id} className={cn("border-t", !rule.active && "opacity-50")}>
                        <td className="px-2 py-1.5 font-mono text-xs">{rule.field_name}</td>
                        <td className="px-2 py-1.5">{subtypeName(rule.subtype_code)}</td>
                        <td className="px-2 py-1.5 capitalize">{rule.severity}</td>
                        <td className="text-muted-foreground px-2 py-1.5 text-xs">{rule.message ?? "(auto)"}</td>
                        <td className="px-2 py-1.5">
                          <div className="flex justify-end gap-1">
                            <Button variant="ghost" size="icon" className="size-7" onClick={() => setDialog(rule)}>
                              <Pencil className="size-3.5" />
                            </Button>
                            <Button variant="ghost" size="icon" className="size-7" onClick={() => setDeleting(rule)}>
                              <Trash2 className="size-3.5" />
                            </Button>
                          </div>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </CardContent>
        )}
      </Card>

      {dialog && featureClass && (
        <RuleDialog
          featureClass={featureClass}
          existing={dialog === "new" ? null : dialog}
          onClose={() => setDialog(null)}
        />
      )}

      <ConfirmDeleteDialog
        open={deleting !== null}
        title={`Delete this rule?`}
        description="The expected-field check stops running. Any open issues it raised will auto-resolve on the next audit."
        confirmLabel="Delete"
        onOpenChange={(open) => !open && setDeleting(null)}
        onConfirm={async () => {
          if (!deleting) return
          try {
            await del.mutateAsync(deleting.id)
            setDeleting(null)
          } catch (err) {
            toastError(err, "Could not delete the rule.")
          }
        }}
      />
    </div>
  )
}
