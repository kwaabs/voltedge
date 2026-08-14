import { useEffect, useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { Loader2 } from "lucide-react"

import { FieldInput } from "@/components/map/feature-field-input"
import { formatFieldLabel, inputClass } from "@/components/map/field-utils"
import { Button } from "@/components/ui/button"
import { type FieldSchema, useFeatureClassSchema } from "@/hooks/use-feature-class-schema"
import { ApiError, apiFetch } from "@/lib/api"
import { useDrawStore } from "@/stores/draw-store"
import { useMapRefreshStore } from "@/stores/map-refresh-store"
import { useSelectionStore } from "@/stores/selection-store"
import { useErrorToast, useToastStore } from "@/stores/toast-store"

/**
 * Renders in place of feature-panel.tsx's tabs while a draft exists
 * (see draw-store.ts) -- a not-yet-saved feature has no id, so
 * Terminals/Relationships don't apply yet. Two distinct draft origins,
 * same form either way: the map's own draw flow (`draft.geometry` set,
 * point/line/polygon depending on the feature class) and a diagram's
 * digitizing tool (`draft.diagramPlacement` set instead, no geometry at
 * all -- diagram-canvas.tsx only ever starts one of these for a
 * `geometry_type = 'none'` class). Deliberately one shared form rather
 * than a second bespoke one for the diagram case: the actual attribute
 * inputs, subtype picker, and save/cancel affordances have no reason to
 * differ, and a user filling either one should see the exact same
 * panel they already know from the feature-panel.tsx attribute view.
 */
export function FeatureCreateForm() {
  const draft = useDrawStore((s) => s.draft)
  const clearDraft = useDrawStore((s) => s.clearDraft)
  const bumpMapRefresh = useMapRefreshStore((s) => s.bump)
  const select = useSelectionStore((s) => s.select)
  const queryClient = useQueryClient()
  const { data: schema, isPending, isError, error } = useFeatureClassSchema(draft?.featureClassCode)
  useErrorToast(isError, error, "Could not load the form")

  const [name, setName] = useState("")
  const [subtypeCode, setSubtypeCode] = useState<number | "">("")
  const [attributes, setAttributes] = useState<Record<string, unknown>>({})
  const [saving, setSaving] = useState(false)

  // Fresh form state for every new draft -- a leftover subtype pick or
  // attribute value from a just-canceled or just-saved draft has no
  // business surviving into the next one.
  useEffect(() => {
    setName("")
    setSubtypeCode("")
    setAttributes({})
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
  if (isError || !schema) return null

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
    try {
      const body: Record<string, unknown> = { attributes }
      if (draft.geometry) body.geometry = draft.geometry
      if (needsSubtype) body.subtype = subtypeCode
      if (name.trim()) body.name = name.trim()
      const created = await apiFetch<{ id: string }>(`/features/${draft.featureClassCode}`, {
        method: "POST",
        body: JSON.stringify(body),
      })
      if (draft.diagramPlacement) {
        const { diagramId, x, y } = draft.diagramPlacement
        await apiFetch(`/diagrams/${diagramId}/placements`, {
          method: "POST",
          body: JSON.stringify({ feature_class: draft.featureClassCode, feature_id: created.id, x, y, rotation: 0 }),
        })
        queryClient.invalidateQueries({ queryKey: ["diagram-placements", diagramId] })
      } else {
        bumpMapRefresh()
      }
      select(draft.featureClassCode, created.id)
      clearDraft()
    } catch (err) {
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not save this feature.")
    } finally {
      setSaving(false)
    }
  }

  return (
    <div className="flex flex-col gap-3">
      <div className="flex flex-col gap-1">
        <label className="text-xs font-medium">Name</label>
        <input
          type="text"
          className={inputClass}
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder="Optional -- a human-readable label"
        />
      </div>

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
