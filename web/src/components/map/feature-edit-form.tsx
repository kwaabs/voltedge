import { useEffect, useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { Loader2 } from "lucide-react"

import { FieldInput } from "@/components/map/feature-field-input"
import { formatFieldLabel, formatValue, inputClass } from "@/components/map/field-utils"
import { Button } from "@/components/ui/button"
import { useFeature } from "@/hooks/use-feature"
import { type FieldSchema, useFeatureClassSchema } from "@/hooks/use-feature-class-schema"
import { ApiError, apiFetch } from "@/lib/api"
import { useAssistantStore } from "@/stores/assistant-store"
import { useEditStore } from "@/stores/edit-store"
import { useMapRefreshStore } from "@/stores/map-refresh-store"
import { useErrorToast, useToastStore } from "@/stores/toast-store"

interface FeatureEditFormProps {
  featureClassCode: string
  featureId: string
}

/**
 * Renders in place of feature-panel.tsx's read-only Attributes tab
 * while edit-store.ts's `editing` matches the selected feature. Also
 * where a geometry edit actually gets committed: the map (see
 * voltedge-map.tsx's Modify/Translate interactions and
 * geometry-edit-toolbar.tsx) only ever produces a *candidate* new
 * geometry, written to edit-store.ts's `dirtyGeometry` -- this form's
 * Save is what turns it into a real `PATCH`, same commit-on-save
 * decision every write in this app already follows (see
 * docs/phase-3-edit-attributes.md's "Why this slice exists" for why
 * that was chosen over piling up edits server-side).
 *
 * Fetches the feature and its schema itself (useFeature/
 * useFeatureClassSchema) rather than taking them as props -- the same
 * queries feature-panel.tsx's AttributesTab and other panels already
 * run, so TanStack Query serves this from cache in the common case
 * instead of a second real fetch.
 */
export function FeatureEditForm({ featureClassCode, featureId }: FeatureEditFormProps) {
  const queryClient = useQueryClient()
  const {
    data: feature,
    isPending: featurePending,
    isError: featureIsError,
    error: featureError,
  } = useFeature(featureClassCode, featureId)
  const {
    data: schema,
    isPending: schemaPending,
    isError: schemaIsError,
    error: schemaError,
  } = useFeatureClassSchema(featureClassCode)
  useErrorToast(featureIsError, featureError, "Could not load this feature")
  useErrorToast(schemaIsError, schemaError, "Could not load the form")

  const dirtyGeometry = useEditStore((s) => s.dirtyGeometry)
  const [name, setName] = useState("")
  const [attributes, setAttributes] = useState<Record<string, unknown>>({})
  const [saving, setSaving] = useState(false)

  // Seeds the form from the feature's real, current values once loaded
  // -- re-seeds if a different feature is edited without this
  // component unmounting (edit-store.ts's `editing` can retarget while
  // feature-panel.tsx keeps this mounted across the same render).
  useEffect(() => {
    if (feature) {
      setName(feature.name ?? "")
      setAttributes({ ...feature.attributes })
    }
  }, [feature])

  if (featurePending || schemaPending) {
    return (
      <div className="text-muted-foreground flex items-center gap-2 text-sm">
        <Loader2 className="size-4 animate-spin" />
        Loading form...
      </div>
    )
  }
  if (featureIsError || schemaIsError || !feature || !schema) return null

  // A published feature locks any field with editable_after_publish
  // false -- mirrors api/internal/crud/validate.go's own rule exactly
  // (`field %q is locked once the feature is published`), so a locked
  // field is shown read-only here rather than as an input a Save would
  // just bounce off a 400 for.
  const isLocked = (field: FieldSchema) => feature.status === "published" && !field.editable_after_publish
  const visibleFields = schema.fields.filter((f) => f.visible_on_update)
  const isFieldRequired = (field: FieldSchema) =>
    feature.subtype !== undefined && (field.required_for_subtypes ?? []).includes(feature.subtype)
  const missingRequiredField = visibleFields.some((f) => {
    if (!isFieldRequired(f) || isLocked(f)) return false
    const v = attributes[f.name]
    return v === undefined || v === ""
  })
  const changedKeys = Object.keys(attributes).filter((k) => attributes[k] !== feature.attributes[k])
  const nameChanged = name !== (feature.name ?? "")
  // Any kind of change alone is enough to save -- the user may touch
  // just the name, just attributes, just geometry (via the map
  // toolbar), or any combination, per the user's own framing of this
  // feature.
  const canSave = !missingRequiredField && (changedKeys.length > 0 || nameChanged || dirtyGeometry !== null) && !saving

  function handleCancel() {
    useEditStore.getState().stop()
  }

  async function handleSave() {
    if (!canSave) return
    setSaving(true)
    // Captured before the await: only a geometry change moves a feature near
    // new candidates, so only then does the assistant ask for suggestions
    // (Phase 19). An attribute-only edit leaves connectivity/relationship
    // candidates unchanged, so it skips the round trip entirely.
    const geometryChanged = dirtyGeometry !== null
    try {
      const changed: Record<string, unknown> = {}
      for (const key of changedKeys) changed[key] = attributes[key]
      // Optimistic-concurrency token (Phase 40): echo the updated_at we loaded
      // so the server can reject the save if someone else changed this feature
      // in the meantime (409), rather than silently clobbering their edit.
      const body: Record<string, unknown> = { attributes: changed, expected_updated_at: feature.updated_at }
      if (dirtyGeometry) body.geometry = dirtyGeometry
      // An explicit empty string clears the name (crud's updateRequest
      // treats a *present* name as "set it to exactly this", same as
      // geometry's own presence-means-replace semantics) -- trimmed so
      // a user backspacing everything out reliably clears it rather
      // than leaving whitespace behind.
      if (nameChanged) body.name = name.trim()

      await apiFetch(`/features/${featureClassCode}/${featureId}`, {
        method: "PATCH",
        body: JSON.stringify(body),
      })
      queryClient.invalidateQueries({ queryKey: ["feature", featureClassCode, featureId] })
      // The real fix for the browser's own stale vector-tile cache (not
      // Martin's -- Martin has no cache with --cache-size 0, and its
      // tiles genuinely do reflect the update), same cache-busting
      // bump() the create-feature slice already established for exactly
      // this "the browser is still showing the old tile" problem. Not
      // just for dirtyGeometry: an attribute-only change (e.g.
      // device_state) can resolve to a different symbology_rules match
      // (map-layers.ts's createSymbologyStyle), so the map needs a fresh
      // tile after any successful save, not only a geometry change.
      useMapRefreshStore.getState().bump()
      useEditStore.getState().stop()
      // Fire-and-forget: proactively surface any connections/relationships the
      // feature's new geometry could now establish (assistant-suggestions.tsx
      // renders the chips). Deliberately not awaited -- the save is already
      // done and the assistant is advisory, so a slow or failing suggestion
      // fetch must never hold up the form closing.
      if (geometryChanged) {
        void useAssistantStore.getState().requestSuggestions({ featureClassCode, featureId })
      }
    } catch (err) {
      // A 409 is the concurrency guard (Phase 40): another user changed this
      // feature since it was opened. Surface the server's reload-and-reapply
      // message and refresh the underlying feature so the panel shows the
      // current values -- the form keeps the in-progress edits so they can be
      // reviewed and re-saved rather than lost.
      if (err instanceof ApiError && err.status === 409) {
        useToastStore.getState().pushError(err.message)
        queryClient.invalidateQueries({ queryKey: ["feature", featureClassCode, featureId] })
      } else {
        useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not save this feature.")
      }
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

      {visibleFields.map((field) => (
        <div key={field.name} className="flex flex-col gap-1">
          <label className="text-xs font-medium">
            {formatFieldLabel(field.name)}
            {isFieldRequired(field) && !isLocked(field) && <span className="text-destructive"> *</span>}
          </label>
          {isLocked(field) ? (
            <p className="text-muted-foreground text-sm">
              {formatValue(feature.attributes[field.name])}{" "}
              <span className="text-xs">(locked — published)</span>
            </p>
          ) : (
            <FieldInput
              field={field}
              value={attributes[field.name]}
              onChange={(value) => setAttributes((a) => ({ ...a, [field.name]: value }))}
              network={schema.network}
            />
          )}
        </div>
      ))}

      <div className="flex justify-end gap-2 pt-1">
        <Button variant="outline" size="sm" onClick={handleCancel} disabled={saving}>
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
