import { useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { Loader2, Pencil, Plus, Trash2, Wrench } from "lucide-react"

import { ConfirmDeleteDialog } from "@/components/admin/confirm-delete-dialog"
import { FormDialog } from "@/components/admin/form-dialog"
import { inputClass } from "@/components/map/field-utils"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { usePermission } from "@/hooks/use-auth"
import { type AdminMaterialSpec, useAdminMaterialSpecs } from "@/hooks/use-admin"
import { ApiError, apiFetch } from "@/lib/api"
import { useToastStore } from "@/stores/toast-store"

const labelClass = "text-xs font-medium"

interface NumberFields {
  resistanceOhmPerKm: string
  reactanceOhmPerKm: string
  ampacityAmps: string
  diameterMm: string
  weightKgPerKm: string
  maxOperatingTempC: string
  emissivity: string
  absorptivity: string
  resistanceTempCoefficientPerC: string
  costPerKm: string
  unitCost: string
}

const EMPTY_NUMBERS: NumberFields = {
  resistanceOhmPerKm: "",
  reactanceOhmPerKm: "",
  ampacityAmps: "",
  diameterMm: "",
  weightKgPerKm: "",
  maxOperatingTempC: "",
  emissivity: "",
  absorptivity: "",
  resistanceTempCoefficientPerC: "",
  costPerKm: "",
  unitCost: "",
}

function numberFieldsFrom(spec: AdminMaterialSpec): NumberFields {
  return {
    resistanceOhmPerKm: spec.resistance_ohm_per_km !== undefined ? String(spec.resistance_ohm_per_km) : "",
    reactanceOhmPerKm: spec.reactance_ohm_per_km !== undefined ? String(spec.reactance_ohm_per_km) : "",
    ampacityAmps: spec.ampacity_amps !== undefined ? String(spec.ampacity_amps) : "",
    diameterMm: spec.diameter_mm !== undefined ? String(spec.diameter_mm) : "",
    weightKgPerKm: spec.weight_kg_per_km !== undefined ? String(spec.weight_kg_per_km) : "",
    maxOperatingTempC: spec.max_operating_temp_c !== undefined ? String(spec.max_operating_temp_c) : "",
    emissivity: spec.emissivity !== undefined ? String(spec.emissivity) : "",
    absorptivity: spec.absorptivity !== undefined ? String(spec.absorptivity) : "",
    resistanceTempCoefficientPerC:
      spec.resistance_temp_coefficient_per_c !== undefined ? String(spec.resistance_temp_coefficient_per_c) : "",
    costPerKm: spec.cost_per_km !== undefined ? String(spec.cost_per_km) : "",
    unitCost: spec.unit_cost !== undefined ? String(spec.unit_cost) : "",
  }
}

function toBody(n: NumberFields): Record<string, unknown> {
  return {
    resistance_ohm_per_km: n.resistanceOhmPerKm.trim() === "" ? undefined : Number(n.resistanceOhmPerKm),
    reactance_ohm_per_km: n.reactanceOhmPerKm.trim() === "" ? undefined : Number(n.reactanceOhmPerKm),
    ampacity_amps: n.ampacityAmps.trim() === "" ? undefined : Number(n.ampacityAmps),
    diameter_mm: n.diameterMm.trim() === "" ? undefined : Number(n.diameterMm),
    weight_kg_per_km: n.weightKgPerKm.trim() === "" ? undefined : Number(n.weightKgPerKm),
    max_operating_temp_c: n.maxOperatingTempC.trim() === "" ? undefined : Number(n.maxOperatingTempC),
    emissivity: n.emissivity.trim() === "" ? undefined : Number(n.emissivity),
    absorptivity: n.absorptivity.trim() === "" ? undefined : Number(n.absorptivity),
    resistance_temp_coefficient_per_c:
      n.resistanceTempCoefficientPerC.trim() === "" ? undefined : Number(n.resistanceTempCoefficientPerC),
    cost_per_km: n.costPerKm.trim() === "" ? undefined : Number(n.costPerKm),
    unit_cost: n.unitCost.trim() === "" ? undefined : Number(n.unitCost),
  }
}

function NumberFieldsGrid({ value, onChange }: { value: NumberFields; onChange: (v: NumberFields) => void }) {
  return (
    <div className="flex flex-col gap-3">
      <div className="grid grid-cols-2 gap-3 sm:grid-cols-3">
        <div className="flex flex-col gap-1">
          <label className={labelClass}>Resistance (Ω/km)</label>
          <input
            type="number"
            className={inputClass}
            value={value.resistanceOhmPerKm}
            onChange={(e) => onChange({ ...value, resistanceOhmPerKm: e.target.value })}
          />
        </div>
        <div className="flex flex-col gap-1">
          <label className={labelClass}>Reactance (Ω/km)</label>
          <input
            type="number"
            className={inputClass}
            value={value.reactanceOhmPerKm}
            onChange={(e) => onChange({ ...value, reactanceOhmPerKm: e.target.value })}
          />
        </div>
        <div className="flex flex-col gap-1">
          <label className={labelClass}>Ampacity (A)</label>
          <input
            type="number"
            className={inputClass}
            value={value.ampacityAmps}
            onChange={(e) => onChange({ ...value, ampacityAmps: e.target.value })}
          />
        </div>
        <div className="flex flex-col gap-1">
          <label className={labelClass}>Diameter (mm)</label>
          <input
            type="number"
            className={inputClass}
            value={value.diameterMm}
            onChange={(e) => onChange({ ...value, diameterMm: e.target.value })}
          />
        </div>
        <div className="flex flex-col gap-1">
          <label className={labelClass}>Weight (kg/km)</label>
          <input
            type="number"
            className={inputClass}
            value={value.weightKgPerKm}
            onChange={(e) => onChange({ ...value, weightKgPerKm: e.target.value })}
          />
        </div>
      </div>
      <div className="flex flex-col gap-2">
        <p className="text-muted-foreground text-xs font-semibold uppercase">
          Thermal properties (first-principles ampacity)
        </p>
        <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
          <div className="flex flex-col gap-1">
            <label className={labelClass}>Max operating temp (°C)</label>
            <input
              type="number"
              className={inputClass}
              value={value.maxOperatingTempC}
              onChange={(e) => onChange({ ...value, maxOperatingTempC: e.target.value })}
            />
          </div>
          <div className="flex flex-col gap-1">
            <label className={labelClass}>Emissivity (0-1)</label>
            <input
              type="number"
              className={inputClass}
              value={value.emissivity}
              onChange={(e) => onChange({ ...value, emissivity: e.target.value })}
            />
          </div>
          <div className="flex flex-col gap-1">
            <label className={labelClass}>Absorptivity (0-1)</label>
            <input
              type="number"
              className={inputClass}
              value={value.absorptivity}
              onChange={(e) => onChange({ ...value, absorptivity: e.target.value })}
            />
          </div>
          <div className="flex flex-col gap-1">
            <label className={labelClass}>Resistance temp. coeff. (/°C)</label>
            <input
              type="number"
              className={inputClass}
              value={value.resistanceTempCoefficientPerC}
              onChange={(e) => onChange({ ...value, resistanceTempCoefficientPerC: e.target.value })}
            />
          </div>
        </div>
      </div>
      <div className="flex flex-col gap-2">
        <p className="text-muted-foreground text-xs font-semibold uppercase">Cost</p>
        <div className="grid grid-cols-2 gap-3">
          <div className="flex flex-col gap-1">
            <label className={labelClass}>Cost per km</label>
            <input
              type="number"
              className={inputClass}
              value={value.costPerKm}
              onChange={(e) => onChange({ ...value, costPerKm: e.target.value })}
            />
          </div>
          <div className="flex flex-col gap-1">
            <label className={labelClass}>Unit cost (each)</label>
            <input
              type="number"
              className={inputClass}
              value={value.unitCost}
              onChange={(e) => onChange({ ...value, unitCost: e.target.value })}
            />
          </div>
        </div>
      </div>
    </div>
  )
}

/** Edit an existing spec's own name/category/engineering numbers --
 * api/internal/admin's PATCH .../material-specs/{code}. Always safe:
 * nothing physical reads these columns directly, a domain value just
 * carries an id pointer to whichever row is here now (see
 * metadata.MaterialSpec's own doc comment). */
function EditMaterialSpecRow({ spec, onDone }: { spec: AdminMaterialSpec; onDone: () => void }) {
  const [name, setName] = useState(spec.name)
  const [category, setCategory] = useState(spec.category)
  const [numbers, setNumbers] = useState<NumberFields>(numberFieldsFrom(spec))
  const [saving, setSaving] = useState(false)
  const queryClient = useQueryClient()

  const canSave = name.trim().length > 0 && category.trim().length > 0

  function close() {
    setName(spec.name)
    setCategory(spec.category)
    setNumbers(numberFieldsFrom(spec))
    onDone()
  }

  async function handleSave() {
    setSaving(true)
    try {
      await apiFetch(`/admin/material-specs/${spec.code}`, {
        method: "PATCH",
        body: JSON.stringify({ name: name.trim(), category: category.trim(), ...toBody(numbers) }),
      })
      queryClient.invalidateQueries({ queryKey: ["admin", "material-specs"] })
      close()
    } catch (err) {
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not update this material spec.")
    } finally {
      setSaving(false)
    }
  }

  return (
    <FormDialog
      open
      onOpenChange={(next) => next || close()}
      title={`Edit material spec ${spec.code}`}
      description="Update the display name, category, and engineering numbers."
      onSubmit={handleSave}
      submitLabel="Save"
      canSubmit={canSave}
      saving={saving}
      className="max-w-2xl"
    >
      <div className="grid grid-cols-2 gap-3">
        <div className="flex flex-col gap-1">
          <label className={labelClass}>Name</label>
          <input className={inputClass} value={name} onChange={(e) => setName(e.target.value)} />
        </div>
        <div className="flex flex-col gap-1">
          <label className={labelClass}>Category</label>
          <input className={inputClass} value={category} onChange={(e) => setCategory(e.target.value)} />
        </div>
      </div>
      <NumberFieldsGrid value={numbers} onChange={setNumbers} />
    </FormDialog>
  )
}

/**
 * Material specs are a real-world catalog (e.g. ACSR_50, see
 * metadata.MaterialSpec's own doc comment) -- physical/electrical
 * properties a coded domain value can optionally attach to (from the
 * Domains page's own value editor), so a field like MV_CONDUCTOR's
 * conductor_spec resolves to real numbers, not just a display label.
 * See docs/phase-8-materials.md.
 */
export function AdminMaterialsPage() {
  const { data, isPending, isError } = useAdminMaterialSpecs()
  const queryClient = useQueryClient()
  // Create/edit/delete here are metadata:write on the server; this page opens
  // on metadata:read, so gate the write controls to match.
  const canWrite = usePermission()("metadata:write")

  const [open, setOpen] = useState(false)
  const [code, setCode] = useState("")
  const [name, setName] = useState("")
  const [category, setCategory] = useState("conductor")
  const [numbers, setNumbers] = useState<NumberFields>(EMPTY_NUMBERS)
  const [saving, setSaving] = useState(false)
  const [editingCode, setEditingCode] = useState<string | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<AdminMaterialSpec | null>(null)

  function close() {
    setOpen(false)
    setCode("")
    setName("")
    setCategory("conductor")
    setNumbers(EMPTY_NUMBERS)
  }

  const canSave = code.trim().length > 0 && name.trim().length > 0 && category.trim().length > 0

  async function handleSave() {
    setSaving(true)
    try {
      await apiFetch("/admin/material-specs", {
        method: "POST",
        body: JSON.stringify({ code: code.trim(), name: name.trim(), category: category.trim(), ...toBody(numbers) }),
      })
      queryClient.invalidateQueries({ queryKey: ["admin", "material-specs"] })
      close()
    } catch (err) {
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not create this material spec.")
    } finally {
      setSaving(false)
    }
  }

  return (
    <div className="h-full overflow-y-auto p-4">
      <div className="mx-auto flex max-w-3xl flex-col gap-4">
        <div className="flex items-center justify-between">
          <div>
            <h1 className="text-lg font-semibold">Materials</h1>
            <p className="text-muted-foreground text-sm">
              A catalog of real physical/electrical properties (e.g. ACSR 50mm²) a coded domain value can link to, so
              a field resolves to real numbers instead of just a label.
            </p>
          </div>
          {canWrite && (
            <Button size="sm" onClick={() => setOpen(true)}>
              <Plus className="size-4" />
              New material spec
            </Button>
          )}
        </div>

        <FormDialog
          open={open}
          onOpenChange={(next) => (next ? setOpen(true) : close())}
          title="New material spec"
          description="A stable code, a display name, and its engineering numbers."
          onSubmit={handleSave}
          submitLabel="Create"
          canSubmit={canSave}
          saving={saving}
          className="max-w-2xl"
        >
          <div className="grid grid-cols-3 gap-3">
            <div className="flex flex-col gap-1">
              <label className={labelClass}>
                Code <span className="text-destructive">*</span>
              </label>
              <input
                className={inputClass}
                value={code}
                onChange={(e) => setCode(e.target.value.toUpperCase())}
                placeholder="e.g. ACSR_50"
              />
            </div>
            <div className="flex flex-col gap-1">
              <label className={labelClass}>
                Name <span className="text-destructive">*</span>
              </label>
              <input
                className={inputClass}
                value={name}
                onChange={(e) => setName(e.target.value)}
                placeholder="e.g. ACSR 50 mm²"
              />
            </div>
            <div className="flex flex-col gap-1">
              <label className={labelClass}>
                Category <span className="text-destructive">*</span>
              </label>
              <input className={inputClass} value={category} onChange={(e) => setCategory(e.target.value)} />
            </div>
          </div>
          <NumberFieldsGrid value={numbers} onChange={setNumbers} />
        </FormDialog>

        {isPending ? (
          <div className="text-muted-foreground flex items-center gap-2 text-sm">
            <Loader2 className="size-4 animate-spin" />
            Loading material specs...
          </div>
        ) : isError || !data ? (
          <p className="text-destructive text-sm">Could not load material specs.</p>
        ) : data.material_specs.length === 0 ? (
          <p className="text-muted-foreground text-sm">No material specs yet.</p>
        ) : (
          <div className="flex flex-col gap-2">
            {data.material_specs.map((m) =>
              editingCode === m.code ? (
                <EditMaterialSpecRow key={m.code} spec={m} onDone={() => setEditingCode(null)} />
              ) : (
                <Card key={m.code}>
                  <CardHeader className="flex flex-row items-center gap-2 pb-2">
                    <Wrench className="text-muted-foreground size-4" />
                    <div className="min-w-0 flex-1">
                      <CardTitle className="text-sm">{m.name}</CardTitle>
                      <CardDescription className="font-mono text-xs">
                        {m.code} · {m.category}
                      </CardDescription>
                    </div>
                    {canWrite && (
                      <>
                        <Button
                          variant="ghost"
                          size="icon"
                          className="size-7"
                          title="Edit"
                          onClick={() => setEditingCode(m.code)}
                        >
                          <Pencil className="size-4" />
                        </Button>
                        <Button
                          variant="ghost"
                          size="icon"
                          className="text-destructive size-7"
                          onClick={() => setDeleteTarget(m)}
                        >
                          <Trash2 className="size-4" />
                        </Button>
                      </>
                    )}
                  </CardHeader>
                  <CardContent className="flex flex-wrap gap-1 pt-0">
                    {m.resistance_ohm_per_km !== undefined && (
                      <span className="bg-muted text-muted-foreground rounded-md px-1.5 py-0.5 text-xs">
                        R {m.resistance_ohm_per_km} Ω/km
                      </span>
                    )}
                    {m.reactance_ohm_per_km !== undefined && (
                      <span className="bg-muted text-muted-foreground rounded-md px-1.5 py-0.5 text-xs">
                        X {m.reactance_ohm_per_km} Ω/km
                      </span>
                    )}
                    {m.ampacity_amps !== undefined && (
                      <span className="bg-muted text-muted-foreground rounded-md px-1.5 py-0.5 text-xs">
                        {m.ampacity_amps} A
                      </span>
                    )}
                    {m.diameter_mm !== undefined && (
                      <span className="bg-muted text-muted-foreground rounded-md px-1.5 py-0.5 text-xs">
                        ⌀ {m.diameter_mm} mm
                      </span>
                    )}
                    {m.weight_kg_per_km !== undefined && (
                      <span className="bg-muted text-muted-foreground rounded-md px-1.5 py-0.5 text-xs">
                        {m.weight_kg_per_km} kg/km
                      </span>
                    )}
                    {m.max_operating_temp_c !== undefined && (
                      <span className="bg-muted text-muted-foreground rounded-md px-1.5 py-0.5 text-xs">
                        max {m.max_operating_temp_c}°C
                      </span>
                    )}
                    {m.emissivity !== undefined && (
                      <span className="bg-muted text-muted-foreground rounded-md px-1.5 py-0.5 text-xs">
                        ε {m.emissivity}
                      </span>
                    )}
                    {m.absorptivity !== undefined && (
                      <span className="bg-muted text-muted-foreground rounded-md px-1.5 py-0.5 text-xs">
                        α {m.absorptivity}
                      </span>
                    )}
                    {m.resistance_temp_coefficient_per_c !== undefined && (
                      <span className="bg-muted text-muted-foreground rounded-md px-1.5 py-0.5 text-xs">
                        α_R {m.resistance_temp_coefficient_per_c}/°C
                      </span>
                    )}
                    {m.cost_per_km !== undefined && (
                      <span className="bg-muted text-muted-foreground rounded-md px-1.5 py-0.5 text-xs">
                        {m.cost_per_km}/km
                      </span>
                    )}
                    {m.unit_cost !== undefined && (
                      <span className="bg-muted text-muted-foreground rounded-md px-1.5 py-0.5 text-xs">
                        {m.unit_cost}/ea
                      </span>
                    )}
                  </CardContent>
                </Card>
              ),
            )}
          </div>
        )}
      </div>

      <ConfirmDeleteDialog
        open={deleteTarget !== null}
        title={`Delete ${deleteTarget?.code ?? "this material spec"}?`}
        description="Only possible while no domain value links to this spec -- if any already does, deletion is rejected with a clear error instead. This can't be undone."
        onOpenChange={(open) => !open && setDeleteTarget(null)}
        onConfirm={async () => {
          if (!deleteTarget) return
          await apiFetch(`/admin/material-specs/${deleteTarget.code}`, { method: "DELETE" })
          queryClient.invalidateQueries({ queryKey: ["admin", "material-specs"] })
        }}
      />
    </div>
  )
}
