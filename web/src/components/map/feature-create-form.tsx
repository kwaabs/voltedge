import { useEffect, useState } from "react"
import { Loader2 } from "lucide-react"

import { Button } from "@/components/ui/button"
import { type FieldSchema, useFeatureClassSchema } from "@/hooks/use-feature-class-schema"
import { ApiError, apiFetch } from "@/lib/api"
import { useDrawStore } from "@/stores/draw-store"
import { useMapRefreshStore } from "@/stores/map-refresh-store"
import { useSelectionStore } from "@/stores/selection-store"

/** "condition_rating" -> "Condition rating" -- same cheap label
 * derivation feature-panel.tsx already uses; no real per-field display
 * label modeled in metadata.fields yet. */
function formatFieldLabel(key: string): string {
  const spaced = key.replace(/_/g, " ")
  return spaced.charAt(0).toUpperCase() + spaced.slice(1)
}

const inputClass =
  "border-input bg-background h-8 w-full rounded-md border px-2 text-sm outline-none focus-visible:border-ring focus-visible:ring-ring/50 focus-visible:ring-[3px]"

interface FieldInputProps {
  field: FieldSchema
  value: unknown
  onChange: (value: unknown) => void
}

/** One form control per field, picked from its metadata data_type --
 * the same contract api/internal/crud/validate.go's validateValue
 * enforces server-side, so a value that passes here should never bounce
 * off a 400 for its type alone. A coded domain always wins into a
 * select, regardless of the underlying data_type. */
function FieldInput({ field, value, onChange }: FieldInputProps) {
  if (field.domain_values) {
    return (
      <select
        className={inputClass}
        value={typeof value === "string" ? value : ""}
        onChange={(e) => onChange(e.target.value || undefined)}
      >
        <option value="">—</option>
        {field.domain_values.map((v) => (
          <option key={v} value={v}>
            {v}
          </option>
        ))}
      </select>
    )
  }

  switch (field.data_type) {
    case "boolean":
      return (
        <input
          type="checkbox"
          className="size-4"
          checked={Boolean(value)}
          onChange={(e) => onChange(e.target.checked)}
        />
      )
    case "integer":
    case "numeric":
      return (
        <input
          type="number"
          className={inputClass}
          value={typeof value === "number" ? value : ""}
          onChange={(e) => onChange(e.target.value === "" ? undefined : Number(e.target.value))}
        />
      )
    case "date":
      return (
        <input
          type="date"
          className={inputClass}
          value={typeof value === "string" ? value : ""}
          onChange={(e) => onChange(e.target.value || undefined)}
        />
      )
    case "timestamp":
      return (
        <input
          type="datetime-local"
          className={inputClass}
          value={typeof value === "string" ? value : ""}
          onChange={(e) => onChange(e.target.value ? new Date(e.target.value).toISOString() : undefined)}
        />
      )
    default:
      return (
        <input
          type="text"
          className={inputClass}
          value={typeof value === "string" ? value : ""}
          onChange={(e) => onChange(e.target.value || undefined)}
        />
      )
  }
}

/**
 * Renders in place of feature-panel.tsx's tabs while a draft exists
 * (see draw-store.ts) -- a not-yet-saved feature has no id, so
 * Terminals/Relationships don't apply yet. Point geometry only this
 * slice, since that's all voltedge-map.tsx's Draw interaction produces
 * so far; line/polygon drawing is future work.
 */
export function FeatureCreateForm() {
  const draft = useDrawStore((s) => s.draft)
  const clearDraft = useDrawStore((s) => s.clearDraft)
  const bumpMapRefresh = useMapRefreshStore((s) => s.bump)
  const select = useSelectionStore((s) => s.select)
  const { data: schema, isPending, isError, error } = useFeatureClassSchema(draft?.featureClassCode)

  const [subtypeCode, setSubtypeCode] = useState<number | "">("")
  const [attributes, setAttributes] = useState<Record<string, unknown>>({})
  const [saving, setSaving] = useState(false)
  const [saveError, setSaveError] = useState<string | null>(null)

  // Fresh form state for every new draft -- a leftover subtype pick or
  // attribute value from a just-canceled or just-saved draft has no
  // business surviving into the next one.
  useEffect(() => {
    setSubtypeCode("")
    setAttributes({})
    setSaveError(null)
  }, [draft?.featureClassCode])

  if (!draft) return null

  if (isPending) {
    return (
      <div className="text-muted-foreground flex items-center gap-2 text-sm">
        <Loader2 className="size-4 animate-spin" />
        Loading form...
      </div>
    )
  }
  if (isError || !schema) {
    return (
      <p className="text-destructive text-sm">
        Could not load the form{error instanceof Error ? `: ${error.message}` : "."}
      </p>
    )
  }

  const visibleFields = schema.fields.filter((f) => f.visible_on_create)
  const isFieldRequired = (field: FieldSchema) =>
    subtypeCode !== "" && (field.required_for_subtypes ?? []).includes(subtypeCode)
  const needsSubtype = schema.subtypes.length > 0
  const missingSubtype = needsSubtype && subtypeCode === ""
  const missingRequiredField = visibleFields.some((f) => {
    if (!isFieldRequired(f)) return false
    const v = attributes[f.name]
    return v === undefined || v === ""
  })
  const canSave = !missingSubtype && !missingRequiredField && !saving

  async function handleSave() {
    if (!draft || !schema) return
    setSaving(true)
    setSaveError(null)
    try {
      const body: Record<string, unknown> = { geometry: draft.geometry, attributes }
      if (needsSubtype) body.subtype = subtypeCode
      const created = await apiFetch<{ id: string }>(`/features/${draft.featureClassCode}`, {
        method: "POST",
        body: JSON.stringify(body),
      })
      bumpMapRefresh()
      select(draft.featureClassCode, created.id)
      clearDraft()
    } catch (err) {
      setSaveError(err instanceof ApiError ? err.message : "Could not save this feature.")
    } finally {
      setSaving(false)
    }
  }

  return (
    <div className="flex flex-col gap-3">
      {needsSubtype && (
        <div className="flex flex-col gap-1">
          <label className="text-xs font-medium">
            Subtype <span className="text-destructive">*</span>
          </label>
          <select
            className={inputClass}
            value={subtypeCode}
            onChange={(e) => setSubtypeCode(e.target.value === "" ? "" : Number(e.target.value))}
          >
            <option value="">Select…</option>
            {schema.subtypes.map((st) => (
              <option key={st.code} value={st.code}>
                {st.name}
              </option>
            ))}
          </select>
        </div>
      )}

      {visibleFields.map((field) => (
        <div key={field.name} className="flex flex-col gap-1">
          <label className="text-xs font-medium">
            {formatFieldLabel(field.name)}
            {isFieldRequired(field) && <span className="text-destructive"> *</span>}
          </label>
          <FieldInput
            field={field}
            value={attributes[field.name]}
            onChange={(value) => setAttributes((a) => ({ ...a, [field.name]: value }))}
          />
        </div>
      ))}

      {saveError && <p className="text-destructive text-sm">{saveError}</p>}

      <div className="flex justify-end gap-2 pt-1">
        <Button variant="outline" size="sm" onClick={clearDraft} disabled={saving}>
          Cancel
        </Button>
        <Button size="sm" onClick={handleSave} disabled={!canSave}>
          {saving && <Loader2 className="size-4 animate-spin" />}
          Save
        </Button>
      </div>
    </div>
  )
}
