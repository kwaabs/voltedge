import { useEffect, useRef, useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { Loader2, PenLine } from "lucide-react"

import { formatFieldLabel, inputClass } from "@/components/map/field-utils"
import { FieldInput } from "@/components/map/feature-field-input"
import { Button } from "@/components/ui/button"
import { type FeatureClassSummary } from "@/hooks/use-feature-classes"
import { type FieldSchema, useFeatureClassSchema } from "@/hooks/use-feature-class-schema"
import { ApiError, apiFetch } from "@/lib/api"
import { useToastStore } from "@/stores/toast-store"

export interface PendingDigitize {
  featureClass: string
  x: number
  y: number
}

/**
 * The header trigger for digitizing a brand-new, geometry_type = 'none'
 * feature straight onto a diagram -- the missing third path alongside
 * the map's own draw tools (real geometry, real feature classes) and
 * AddFeatureButton (an *existing* feature, any geometry_type, just
 * placed a second time). Deliberately scoped to `classes` the parent
 * has already filtered to geometry_type = 'none' only: a spatial class
 * can never be created from here, since there's no way to give it real
 * map geometry from inside the diagram canvas -- that's the one hard
 * rule this whole tool exists to respect (docs/phase-5-diagrams.md).
 */
export function DigitizeFeatureButton({
  classes,
  digitizingClass,
  onStart,
  onCancel,
}: {
  classes: FeatureClassSummary[]
  digitizingClass: string | null
  onStart: (code: string) => void
  onCancel: () => void
}) {
  const [open, setOpen] = useState(false)
  const containerRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (!open) return
    function handlePointerDown(e: PointerEvent) {
      if (containerRef.current && !containerRef.current.contains(e.target as Node)) setOpen(false)
    }
    window.addEventListener("pointerdown", handlePointerDown)
    return () => window.removeEventListener("pointerdown", handlePointerDown)
  }, [open])

  if (classes.length === 0) return null

  if (digitizingClass) {
    const className = classes.find((c) => c.code === digitizingClass)?.name ?? digitizingClass
    return (
      <Button variant="secondary" size="sm" className="h-7 shrink-0 gap-1.5 px-2 text-xs" onClick={onCancel}>
        Click the canvas to place {className}
        <span className="text-muted-foreground">Esc</span>
      </Button>
    )
  }

  return (
    <div ref={containerRef} className="relative shrink-0">
      <Button
        variant="ghost"
        size="icon"
        className="size-7"
        title="New feature (not on the map)"
        onClick={() => setOpen((o) => !o)}
      >
        <PenLine className="size-4" />
        <span className="sr-only">New feature (not on the map)</span>
      </Button>
      {open && (
        <div className="bg-popover text-popover-foreground absolute top-full right-0 z-10 mt-1 w-64 rounded-md border shadow-lg">
          <p className="text-muted-foreground border-b px-2 py-1.5 text-xs">
            Draw a feature that only ever exists on a diagram, never on the map
          </p>
          {classes.map((c) => (
            <button
              key={c.code}
              type="button"
              onClick={() => {
                onStart(c.code)
                setOpen(false)
              }}
              className="hover:bg-accent hover:text-accent-foreground block w-full border-b p-2 text-left text-sm transition-colors last:border-b-0"
            >
              {c.name}
            </button>
          ))}
        </div>
      )}
    </div>
  )
}

/**
 * The attribute-entry step, shown once digitizing mode has captured a
 * click on the canvas -- deliberately not a reuse of
 * feature-create-form.tsx: that component is wired to draw-store.ts's
 * map-draft flow (its save body always includes `geometry`, its
 * post-save side effects are bumpMapRefresh + map selection), neither
 * of which applies here. Reuses the same field-level pieces
 * (FieldInput, formatFieldLabel, inputClass, useFeatureClassSchema)
 * feature-create-form.tsx already uses, so the actual attribute inputs
 * behave identically either way.
 *
 * Saving is two real API calls in sequence for one user action:
 * POST /features/{class} with no geometry key at all (the CRUD engine
 * rejects one for a geometry_type = 'none' class), then
 * POST /diagrams/{id}/placements at the clicked point.
 */
export function DigitizeFeatureForm({
  diagramId,
  pending,
  onCancel,
  onDone,
}: {
  diagramId: string
  pending: PendingDigitize
  onCancel: () => void
  onDone: () => void
}) {
  const { data: schema, isPending, isError } = useFeatureClassSchema(pending.featureClass)
  const [name, setName] = useState("")
  const [subtypeCode, setSubtypeCode] = useState<number | "">("")
  const [attributes, setAttributes] = useState<Record<string, unknown>>({})
  const [saving, setSaving] = useState(false)
  const queryClient = useQueryClient()

  if (isPending) {
    return (
      <div className="bg-popover text-popover-foreground absolute inset-x-3 bottom-3 z-10 rounded-md border p-3 shadow-lg">
        <div className="text-muted-foreground flex items-center gap-2 text-sm">
          <Loader2 className="size-4 animate-spin" />
          Loading form…
        </div>
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
    setSaving(true)
    try {
      const body: Record<string, unknown> = { attributes }
      if (needsSubtype) body.subtype = subtypeCode
      if (name.trim()) body.name = name.trim()
      const created = await apiFetch<{ id: string }>(`/features/${pending.featureClass}`, {
        method: "POST",
        body: JSON.stringify(body),
      })
      await apiFetch(`/diagrams/${diagramId}/placements`, {
        method: "POST",
        body: JSON.stringify({
          feature_class: pending.featureClass,
          feature_id: created.id,
          x: pending.x,
          y: pending.y,
          rotation: 0,
        }),
      })
      queryClient.invalidateQueries({ queryKey: ["diagram-placements", diagramId] })
      onDone()
    } catch (err) {
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not create this feature.")
    } finally {
      setSaving(false)
    }
  }

  return (
    <div className="bg-popover text-popover-foreground absolute inset-x-3 bottom-3 z-10 flex max-h-[70%] flex-col gap-3 overflow-y-auto rounded-md border p-3 shadow-lg">
      <p className="text-sm font-medium">New {schema.name}</p>
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
        <Button variant="outline" size="sm" onClick={onCancel} disabled={saving}>
          Cancel
        </Button>
        <Button size="sm" onClick={handleSave} disabled={!canSave}>
          {saving && <Loader2 className="size-4 animate-spin" />}
          Place
        </Button>
      </div>
    </div>
  )
}
