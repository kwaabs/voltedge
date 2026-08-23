import { useEffect, useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { Loader2 } from "lucide-react"

import { FieldInput } from "@/components/map/feature-field-input"
import { formatFieldLabel, inputClass } from "@/components/map/field-utils"
import { Button } from "@/components/ui/button"
import { type FieldSchema, useFeatureClassSchema } from "@/hooks/use-feature-class-schema"
import { ApiError, apiFetch } from "@/lib/api"
import { useAssistantStore } from "@/stores/assistant-store"
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
  const draftToken = useDrawStore((s) => s.draftToken)
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
  // business surviving into the next one. Keyed on draftToken (bumped once
  // per new draft), NOT on the draft object: a Duplicate draft dragged on
  // the map re-writes draft.geometry every move, and re-running this then
  // would wipe what's been typed. A seeded draft (Duplicate) pre-fills from
  // its `seed`; a blank draw starts empty. Read live from the store so this
  // needs no dependency on `draft` (whose identity churns on every drag).
  useEffect(() => {
    const seed = useDrawStore.getState().draft?.seed
    setName(seed?.name ?? "")
    setSubtypeCode(seed?.subtype ?? "")
    setAttributes(seed?.attributes ? { ...seed.attributes } : {})
  }, [draftToken])

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
      // Only send fields the class actually allows at create time. For a
      // blank draw this is exactly what's in `attributes` already (the form
      // only ever wrote visible fields), so it's a no-op; for a seeded
      // Duplicate draft it drops any computed/invisible attributes copied
      // off the source that the create validator (api/internal/crud) would
      // reject outright.
      const createAttributes: Record<string, unknown> = {}
      for (const field of visibleFields) {
        if (field.name in attributes) createAttributes[field.name] = attributes[field.name]
      }

      // A split draft doesn't create a plain feature -- it cuts an existing
      // line in one transaction (api/internal/crud/split.go): the original
      // keeps the near half, and this new_feature becomes the far half, whose
      // geometry PostGIS computes from split_point (so no geometry is sent).
      // Everything else -- the visible-only attribute filter, the subtype and
      // name -- is exactly the create form's, since the far half is a create.
      if (draft.split) {
        const { sourceFeatureId, splitPoint } = draft.split
        const newFeature: Record<string, unknown> = { attributes: createAttributes }
        if (needsSubtype) newFeature.subtype = subtypeCode
        if (name.trim()) newFeature.name = name.trim()
        const created = await apiFetch<{ id: string }>(
          `/features/${draft.featureClassCode}/${sourceFeatureId}/split`,
          { method: "POST", body: JSON.stringify({ split_point: splitPoint, new_feature: newFeature }) },
        )
        bumpMapRefresh()
        // The original was shortened server-side -- drop its cached row so the
        // panel/map re-read the near half rather than the pre-split geometry.
        queryClient.invalidateQueries({ queryKey: ["feature", draft.featureClassCode, sourceFeatureId] })
        select(draft.featureClassCode, created.id)
        // Re-suggest on the new far half only: it's the brand-new, disconnected
        // part that needs re-connecting (the original kept all its connections
        // and relationships, per docs/phase-20-split-copy.md). requestSuggestions
        // replaces the whole set, so firing it for both halves would just race --
        // the new part is the one that matters.
        void useAssistantStore.getState().requestSuggestions({
          featureClassCode: draft.featureClassCode,
          featureId: created.id,
        })
        clearDraft()
        return
      }

      const body: Record<string, unknown> = { attributes: createAttributes }
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
      // Fire-and-forget: a just-drawn feature with real geometry (e.g. an LV
      // conductor snapped to a pole) is exactly the moment the assistant should
      // surface the connections/relationships it could now establish -- the same
      // trigger feature-edit-form.tsx runs after a geometry edit, but for the
      // create path, which is how a feature drawn on the map first lands.
      // Skipped for a diagram-only draft: it has no map geometry to rank against.
      if (draft.geometry) {
        void useAssistantStore.getState().requestSuggestions({
          featureClassCode: draft.featureClassCode,
          featureId: created.id,
        })
      }
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
