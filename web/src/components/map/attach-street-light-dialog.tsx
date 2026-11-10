import { useMemo, useState } from "react"
import { useQueryClient } from "@tanstack/react-query"

import { FormDialog } from "@/components/admin/form-dialog"
import { inputClass } from "@/components/map/field-utils"
import { type FieldSchema, useFeatureClassSchema } from "@/hooks/use-feature-class-schema"
import { useAttachStreetLight } from "@/hooks/use-street-lights"
import { ApiError } from "@/lib/api"
import { useToastStore } from "@/stores/toast-store"

const labelClass = "text-xs font-medium"

// The required fields a pole-mounted street light needs at create (the rest are
// nullable load fields the crew fills later). Rendered as domain dropdowns from
// the STREET_LIGHT schema so the values always match the live catalog.
const REQUIRED_FIELDS = ["phase", "luminaire_type", "condition_rating", "supply_mode"] as const

function fieldLabel(name: string): string {
  if (name === "luminaire_type") return "Luminaire type"
  if (name === "condition_rating") return "Condition"
  if (name === "phase") return "Phase"
  if (name === "supply_mode") return "Power source"
  return name
}

/**
 * The "Attach street light" picker shown from a POLE's panel: pick the lamp's
 * phase, luminaire type and condition, then attach. The backend creates a
 * geometry-less pole-mounted STREET_LIGHT, links it to the pole, and best-effort
 * auto-wires it to the pole's nearby LV (reporting the outcome in wire_note).
 */
export function AttachStreetLightDialog({
  poleFeatureClass,
  poleFeatureId,
  onClose,
}: {
  poleFeatureClass: string
  poleFeatureId: string
  onClose: () => void
}) {
  const { data: schema } = useFeatureClassSchema("STREET_LIGHT")
  const attach = useAttachStreetLight()
  const queryClient = useQueryClient()

  const fields = useMemo(() => {
    const byName = new Map((schema?.fields ?? []).map((f) => [f.name, f]))
    return REQUIRED_FIELDS.map((name) => byName.get(name)).filter((f): f is FieldSchema => !!f)
  }, [schema])

  // One value per required field, seeded from the field's default_value.
  const [values, setValues] = useState<Record<string, string>>({})
  // supply_mode defaults to GRID (a grid-fed lamp) when unset, so the common case
  // needs no choice and only a solar lamp is a deliberate pick.
  const valueFor = (f: FieldSchema) => values[f.name] ?? f.default_value ?? (f.name === "supply_mode" ? "GRID" : "")

  const canSave = fields.length > 0 && fields.every((f) => valueFor(f) !== "") && !attach.isPending

  function handleSave() {
    const attributes: Record<string, string> = {}
    for (const f of fields) attributes[f.name] = valueFor(f)
    attach.mutate(
      { pole_feature_class: poleFeatureClass, pole_feature_id: poleFeatureId, attributes },
      {
        onSuccess: (res) => {
          // Refresh the pole's Relationships tab (and its connectivity, if wired).
          queryClient.invalidateQueries({ queryKey: ["relationships", poleFeatureClass, poleFeatureId] })
          if (res.wired) queryClient.invalidateQueries({ queryKey: ["connectivity"] })
          const toast = useToastStore.getState()
          if (res.wired) toast.pushSuccess("Street light attached and wired to the pole's LV.")
          else toast.pushWarning(res.wire_note || "Street light attached (not wired).")
          onClose()
        },
        onError: (err) =>
          useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not attach the street light."),
      },
    )
  }

  return (
    <FormDialog
      open
      onOpenChange={(next) => next || onClose()}
      title="Attach street light"
      description="Add a pole-mounted lamp to this pole. It's created without its own map geometry (it rides the pole) and auto-wired to the pole's LV when exactly one conductor is in range."
      onSubmit={handleSave}
      submitLabel="Attach"
      canSubmit={canSave}
      saving={attach.isPending}
    >
      {fields.map((f) => (
        <div key={f.name} className="flex flex-col gap-1">
          <label className={labelClass}>{fieldLabel(f.name)}</label>
          <select className={inputClass} value={valueFor(f)} onChange={(e) => setValues((v) => ({ ...v, [f.name]: e.target.value }))}>
            <option value="">Select…</option>
            {(f.domain_values ?? []).map((code) => (
              <option key={code} value={code}>
                {f.domain_value_labels?.[code] ?? code}
              </option>
            ))}
          </select>
        </div>
      ))}
    </FormDialog>
  )
}
