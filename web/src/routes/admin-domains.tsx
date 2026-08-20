import { Fragment, useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { ListPlus, Loader2, Pencil, Plus, Tags, Trash2, Wrench, X } from "lucide-react"

import { ConfirmDeleteDialog } from "@/components/admin/confirm-delete-dialog"
import { FormDialog } from "@/components/admin/form-dialog"
import { inputClass } from "@/components/map/field-utils"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { usePermission } from "@/hooks/use-auth"
import { type AdminDomain, type AdminDomainValue, useAdminDomains, useAdminMaterialSpecs } from "@/hooks/use-admin"
import { ApiError, apiFetch } from "@/lib/api"
import { useToastStore } from "@/stores/toast-store"

const labelClass = "text-xs font-medium"

interface DraftValue {
  code: string
  label: string
}

/**
 * Shared by AddDomainValueRow/EditDomainValueRow below -- optional, so
 * most domain values (OPEN/CLOSED, phase codes, ...) leave it at "none."
 * Only meaningful for a coded domain whose values represent a real
 * material/size catalog entry (e.g. a CONDUCTOR_SPEC domain's ACSR_50),
 * see docs/phase-7-materials.md.
 */
function MaterialSpecPicker({ value, onChange }: { value: string; onChange: (v: string) => void }) {
  const { data } = useAdminMaterialSpecs()
  return (
    <div className="flex flex-col gap-1">
      <label className={labelClass}>Material spec</label>
      <select className={inputClass} value={value} onChange={(e) => onChange(e.target.value)}>
        <option value="">none</option>
        {data?.material_specs.map((m) => (
          <option key={m.code} value={m.code}>
            {m.name} ({m.code})
          </option>
        ))}
      </select>
    </div>
  )
}

/**
 * Add one new coded value to an existing domain -- mirrors
 * AddSubtypeRow in admin-feature-classes.tsx. Unlike that row, this
 * page doesn't know which feature classes (if any) reference this
 * domain through a compiled field, so the "recompile to apply" note
 * here can only point at the feature-classes page rather than offer a
 * targeted recompile button.
 */
function AddDomainValueRow({ domain, onDone }: { domain: AdminDomain; onDone: () => void }) {
  const [code, setCode] = useState("")
  const [label, setLabel] = useState("")
  const [materialSpecCode, setMaterialSpecCode] = useState("")
  const [saving, setSaving] = useState(false)
  const [added, setAdded] = useState(false)
  const queryClient = useQueryClient()

  const existingCodes = new Set((domain.values ?? []).map((v) => v.code))
  const canSave = code.trim().length > 0 && label.trim().length > 0 && !existingCodes.has(code.trim())

  async function handleAdd() {
    setSaving(true)
    try {
      const nextSortOrder = (domain.values?.length ?? 0) + 1
      await apiFetch(`/admin/domains/${domain.code}/values`, {
        method: "POST",
        body: JSON.stringify({
          code: code.trim(),
          label: label.trim(),
          sort_order: nextSortOrder,
          material_spec_code: materialSpecCode || undefined,
        }),
      })
      queryClient.invalidateQueries({ queryKey: ["admin", "domains"] })
      setCode("")
      setLabel("")
      setMaterialSpecCode("")
      setAdded(true)
    } catch (err) {
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not add this value.")
    } finally {
      setSaving(false)
    }
  }

  return (
    <FormDialog
      open
      onOpenChange={(next) => next || onDone()}
      title={`Add value to ${domain.code}`}
      description="Add one coded value at a time -- the dialog stays open so you can add several in a row."
      onSubmit={handleAdd}
      submitLabel="Add"
      canSubmit={canSave}
      saving={saving}
    >
      {domain.values && domain.values.length > 0 && (
        <div className="flex flex-wrap gap-1">
          {domain.values.map((v) => (
            <span key={v.code} className="bg-muted text-muted-foreground rounded-md px-1.5 py-0.5 text-xs">
              {v.label}
            </span>
          ))}
        </div>
      )}
      <div className="grid grid-cols-2 gap-2">
        <div className="flex flex-col gap-1">
          <label className={labelClass}>Code</label>
          <input className={inputClass} value={code} onChange={(e) => setCode(e.target.value)} placeholder="e.g. LED" />
        </div>
        <div className="flex flex-col gap-1">
          <label className={labelClass}>Label</label>
          <input className={inputClass} value={label} onChange={(e) => setLabel(e.target.value)} placeholder="e.g. LED" />
        </div>
      </div>
      <MaterialSpecPicker value={materialSpecCode} onChange={setMaterialSpecCode} />
      {added && (
        <p className="text-muted-foreground text-xs">
          Added to metadata -- recompile any feature class using this domain to actually allow it on real features.
        </p>
      )}
    </FormDialog>
  )
}

/**
 * Edit a domain's own name, or (range domain only) its bounds --
 * api/internal/admin's PATCH /admin/domains/{code}. Safe the same way
 * widening a coded domain's values already is: a recompile of any class
 * using this domain rebuilds the field's BETWEEN CHECK from whatever's
 * here now, and Postgres itself rejects a narrowing that doesn't fit
 * real data rather than silently applying it.
 */
function EditDomainRow({ domain, onDone }: { domain: AdminDomain; onDone: () => void }) {
  const [name, setName] = useState(domain.name)
  const [rangeMin, setRangeMin] = useState(domain.range_min !== undefined ? String(domain.range_min) : "")
  const [rangeMax, setRangeMax] = useState(domain.range_max !== undefined ? String(domain.range_max) : "")
  const [saving, setSaving] = useState(false)
  const queryClient = useQueryClient()

  const canSave =
    name.trim().length > 0 &&
    (domain.domain_type !== "range" || (rangeMin.trim().length > 0 && rangeMax.trim().length > 0 && Number(rangeMin) <= Number(rangeMax)))

  async function handleSave() {
    setSaving(true)
    try {
      const body: Record<string, unknown> = { name: name.trim() }
      if (domain.domain_type === "range") {
        body.range_min = Number(rangeMin)
        body.range_max = Number(rangeMax)
      }
      await apiFetch(`/admin/domains/${domain.code}`, { method: "PATCH", body: JSON.stringify(body) })
      queryClient.invalidateQueries({ queryKey: ["admin", "domains"] })
      onDone()
    } catch (err) {
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not update this domain.")
    } finally {
      setSaving(false)
    }
  }

  return (
    <FormDialog
      open
      onOpenChange={(next) => next || onDone()}
      title={
        <>
          Edit domain <span className="font-mono">{domain.code}</span>
        </>
      }
      onSubmit={handleSave}
      submitLabel="Save"
      canSubmit={canSave}
      saving={saving}
    >
      <div className="flex flex-col gap-1">
        <label className={labelClass}>Name</label>
        <input className={inputClass} value={name} onChange={(e) => setName(e.target.value)} />
      </div>
      {domain.domain_type === "range" && (
        <div className="grid grid-cols-2 gap-3">
          <div className="flex flex-col gap-1">
            <label className={labelClass}>Minimum</label>
            <input type="number" className={inputClass} value={rangeMin} onChange={(e) => setRangeMin(e.target.value)} />
          </div>
          <div className="flex flex-col gap-1">
            <label className={labelClass}>Maximum</label>
            <input type="number" className={inputClass} value={rangeMax} onChange={(e) => setRangeMax(e.target.value)} />
          </div>
        </div>
      )}
    </FormDialog>
  )
}

/**
 * Edit an existing coded value's label/sort_order -- api/internal/admin's
 * PATCH /admin/domains/{code}/values/{value_code}. Always safe with no
 * recompile involved at all: a value's code is what a CHECK constraint
 * is built from, never its label.
 */
function EditDomainValueRow({
  domainCode,
  value,
  onDone,
}: {
  domainCode: string
  value: AdminDomainValue
  onDone: () => void
}) {
  const [label, setLabel] = useState(value.label)
  const [sortOrder, setSortOrder] = useState(String(value.sort_order))
  const [materialSpecCode, setMaterialSpecCode] = useState(value.material_spec_code ?? "")
  const [saving, setSaving] = useState(false)
  const queryClient = useQueryClient()

  const canSave = label.trim().length > 0

  async function handleSave() {
    setSaving(true)
    try {
      await apiFetch(`/admin/domains/${domainCode}/values/${value.code}`, {
        method: "PATCH",
        body: JSON.stringify({ label: label.trim(), sort_order: Number(sortOrder), material_spec_code: materialSpecCode }),
      })
      queryClient.invalidateQueries({ queryKey: ["admin", "domains"] })
      onDone()
    } catch (err) {
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not update this value.")
    } finally {
      setSaving(false)
    }
  }

  return (
    <FormDialog
      open
      onOpenChange={(next) => next || onDone()}
      title={
        <>
          Edit value <span className="font-mono">{value.code}</span>
        </>
      }
      onSubmit={handleSave}
      submitLabel="Save"
      canSubmit={canSave}
      saving={saving}
    >
      <div className="grid grid-cols-[1fr_5rem] gap-2">
        <div className="flex flex-col gap-1">
          <label className={labelClass}>Label</label>
          <input className={inputClass} value={label} onChange={(e) => setLabel(e.target.value)} />
        </div>
        <div className="flex flex-col gap-1">
          <label className={labelClass}>Sort</label>
          <input type="number" className={inputClass} value={sortOrder} onChange={(e) => setSortOrder(e.target.value)} />
        </div>
      </div>
      <MaterialSpecPicker value={materialSpecCode} onChange={setMaterialSpecCode} />
    </FormDialog>
  )
}

/**
 * A domain is shared reference data -- fields across many feature
 * classes point at the same one (see docs/metadata-core.md) -- so it
 * gets its own page rather than being authored inline inside the
 * feature-class wizard. Either domain_type is real here: coded builds
 * the values list, range sets range_min/range_max (a plain numeric
 * bound, enforced by api/internal/compiler as a BETWEEN CHECK on any
 * field that uses it -- see migrations/0020).
 */
export function AdminDomainsPage() {
  const { data, isPending, isError } = useAdminDomains()
  const queryClient = useQueryClient()
  // Every mutation here (POST/PATCH/DELETE on /admin/domains) is gated on
  // metadata:write server-side; this page opens on metadata:read, so gate
  // the write controls to match (see admin-feature-classes.tsx).
  const canWrite = usePermission()("metadata:write")

  const [open, setOpen] = useState(false)
  const [code, setCode] = useState("")
  const [name, setName] = useState("")
  const [domainType, setDomainType] = useState<"coded" | "range">("coded")
  const [values, setValues] = useState<DraftValue[]>([{ code: "", label: "" }])
  const [rangeMin, setRangeMin] = useState("")
  const [rangeMax, setRangeMax] = useState("")
  const [saving, setSaving] = useState(false)
  const [deleteTarget, setDeleteTarget] = useState<AdminDomain | null>(null)
  const [addingValueCode, setAddingValueCode] = useState<string | null>(null)
  const [editingDomainCode, setEditingDomainCode] = useState<string | null>(null)
  const [editingValue, setEditingValue] = useState<{ domainCode: string; value: AdminDomainValue } | null>(null)
  const [deleteValueTarget, setDeleteValueTarget] = useState<{ domainCode: string; value: AdminDomainValue } | null>(null)

  function close() {
    setOpen(false)
    setCode("")
    setName("")
    setDomainType("coded")
    setValues([{ code: "", label: "" }])
    setRangeMin("")
    setRangeMax("")
  }

  const canSave =
    code.trim().length > 0 &&
    name.trim().length > 0 &&
    (domainType === "coded"
      ? values.length > 0 && values.every((v) => v.code.trim().length > 0 && v.label.trim().length > 0)
      : rangeMin.trim().length > 0 && rangeMax.trim().length > 0 && Number(rangeMin) <= Number(rangeMax))

  async function handleSave() {
    setSaving(true)
    try {
      const body: Record<string, unknown> = { code: code.trim(), name: name.trim(), domain_type: domainType }
      if (domainType === "coded") {
        body.values = values.map((v, i) => ({ code: v.code.trim(), label: v.label.trim(), sort_order: i + 1 }))
      } else {
        body.range_min = Number(rangeMin)
        body.range_max = Number(rangeMax)
      }
      await apiFetch("/admin/domains", { method: "POST", body: JSON.stringify(body) })
      queryClient.invalidateQueries({ queryKey: ["admin", "domains"] })
      close()
    } catch (err) {
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not create this domain.")
    } finally {
      setSaving(false)
    }
  }

  return (
    <div className="h-full overflow-y-auto p-4">
      <div className="mx-auto flex max-w-3xl flex-col gap-4">
        <div className="flex items-center justify-between">
          <div>
            <h1 className="text-lg font-semibold">Domains</h1>
            <p className="text-muted-foreground text-sm">
              Reusable coded-value vocabularies -- fields across any feature class can constrain their values to one
              of these.
            </p>
          </div>
          {canWrite && (
            <Button size="sm" onClick={() => setOpen(true)}>
              <Plus className="size-4" />
              New domain
            </Button>
          )}
        </div>

        <FormDialog
          open={open}
          onOpenChange={(next) => (next ? setOpen(true) : close())}
          title="New domain"
          description="A stable code, a display name, and its legal values."
          onSubmit={handleSave}
          submitLabel="Create"
          canSubmit={canSave}
          saving={saving}
          className="max-w-2xl"
        >
          <div className="grid grid-cols-2 gap-3">
                <div className="flex flex-col gap-1">
                  <label className="text-xs font-medium">
                    Code <span className="text-destructive">*</span>
                  </label>
                  <input
                    className={inputClass}
                    value={code}
                    onChange={(e) => setCode(e.target.value.toUpperCase())}
                    placeholder="e.g. LAMP_TYPE"
                  />
                </div>
                <div className="flex flex-col gap-1">
                  <label className="text-xs font-medium">
                    Name <span className="text-destructive">*</span>
                  </label>
                  <input
                    className={inputClass}
                    value={name}
                    onChange={(e) => setName(e.target.value)}
                    placeholder="e.g. Street light lamp type"
                  />
                </div>
              </div>

              <div className="flex flex-col gap-1">
                <label className="text-xs font-medium">Type</label>
                <div className="flex gap-2">
                  <Button
                    type="button"
                    variant={domainType === "coded" ? "default" : "outline"}
                    size="sm"
                    onClick={() => setDomainType("coded")}
                  >
                    Coded values
                  </Button>
                  <Button
                    type="button"
                    variant={domainType === "range" ? "default" : "outline"}
                    size="sm"
                    onClick={() => setDomainType("range")}
                  >
                    Numeric range
                  </Button>
                </div>
              </div>

              {domainType === "coded" ? (
                <div className="flex flex-col gap-2">
                  <label className="text-xs font-medium">
                    Values <span className="text-destructive">*</span>
                  </label>
                  {values.map((v, i) => (
                    <div key={i} className="flex items-center gap-2">
                      <input
                        className={inputClass}
                        value={v.code}
                        onChange={(e) => {
                          const next = [...values]
                          next[i] = { ...next[i], code: e.target.value }
                          setValues(next)
                        }}
                        placeholder="code, e.g. LED"
                      />
                      <input
                        className={inputClass}
                        value={v.label}
                        onChange={(e) => {
                          const next = [...values]
                          next[i] = { ...next[i], label: e.target.value }
                          setValues(next)
                        }}
                        placeholder="label, e.g. LED"
                      />
                      <Button
                        variant="ghost"
                        size="icon"
                        className="size-8 shrink-0"
                        onClick={() => setValues(values.filter((_, vi) => vi !== i))}
                        disabled={values.length === 1}
                      >
                        <Trash2 className="size-4" />
                      </Button>
                    </div>
                  ))}
                  <Button
                    variant="outline"
                    size="sm"
                    className="self-start"
                    onClick={() => setValues([...values, { code: "", label: "" }])}
                  >
                    <Plus className="size-4" />
                    Add value
                  </Button>
                </div>
              ) : (
                <div className="grid grid-cols-2 gap-3">
                  <div className="flex flex-col gap-1">
                    <label className="text-xs font-medium">
                      Minimum <span className="text-destructive">*</span>
                    </label>
                    <input
                      type="number"
                      className={inputClass}
                      value={rangeMin}
                      onChange={(e) => setRangeMin(e.target.value)}
                      placeholder="e.g. 0"
                    />
                  </div>
                  <div className="flex flex-col gap-1">
                    <label className="text-xs font-medium">
                      Maximum <span className="text-destructive">*</span>
                    </label>
                    <input
                      type="number"
                      className={inputClass}
                      value={rangeMax}
                      onChange={(e) => setRangeMax(e.target.value)}
                      placeholder="e.g. 100"
                    />
                  </div>
                </div>
          )}
        </FormDialog>

        {isPending ? (
          <div className="text-muted-foreground flex items-center gap-2 text-sm">
            <Loader2 className="size-4 animate-spin" />
            Loading domains...
          </div>
        ) : isError || !data ? (
          <p className="text-destructive text-sm">Could not load domains.</p>
        ) : data.domains.length === 0 ? (
          <p className="text-muted-foreground text-sm">No domains yet.</p>
        ) : (
          <div className="flex flex-col gap-2">
            {data.domains.map((d) => (
              <Fragment key={d.code}>
                {editingDomainCode === d.code && (
                  <EditDomainRow domain={d} onDone={() => setEditingDomainCode(null)} />
                )}
                {addingValueCode === d.code && (
                  <AddDomainValueRow domain={d} onDone={() => setAddingValueCode(null)} />
                )}
                <Card>
                  <CardHeader className="flex flex-row items-center gap-2 pb-2">
                    <Tags className="text-muted-foreground size-4" />
                    <div className="min-w-0 flex-1">
                      <CardTitle className="text-sm">{d.name}</CardTitle>
                      <CardDescription className="font-mono text-xs">{d.code}</CardDescription>
                    </div>
                    <span className="text-muted-foreground text-xs">{d.domain_type}</span>
                    {canWrite && (
                      <>
                        <Button
                          variant="ghost"
                          size="icon"
                          className="size-7"
                          title="Edit"
                          onClick={() => setEditingDomainCode(d.code)}
                        >
                          <Pencil className="size-4" />
                        </Button>
                        {d.domain_type === "coded" && (
                          <Button
                            variant="ghost"
                            size="icon"
                            className="size-7"
                            title="Add value"
                            onClick={() => setAddingValueCode(d.code)}
                          >
                            <ListPlus className="size-4" />
                          </Button>
                        )}
                        <Button
                          variant="ghost"
                          size="icon"
                          className="text-destructive size-7"
                          onClick={() => setDeleteTarget(d)}
                        >
                          <Trash2 className="size-4" />
                        </Button>
                      </>
                    )}
                  </CardHeader>
                  {d.values && d.values.length > 0 && (
                    <CardContent className="flex flex-col gap-2 pt-0">
                      <div className="flex flex-wrap gap-1">
                        {d.values.map((v) => (
                          <span
                            key={v.code}
                            className="bg-muted text-muted-foreground group flex items-center gap-1 rounded-md py-0.5 pr-0.5 pl-1.5 text-xs"
                            title={v.material_spec_code ? `${v.code} · material spec: ${v.material_spec_code}` : v.code}
                          >
                            {v.material_spec_code && <Wrench className="size-3" />}
                            {v.label}
                            {canWrite && (
                              <>
                                <button
                                  type="button"
                                  className="hover:text-foreground rounded p-0.5"
                                  title="Edit value"
                                  onClick={() => setEditingValue({ domainCode: d.code, value: v })}
                                >
                                  <Pencil className="size-3" />
                                </button>
                                <button
                                  type="button"
                                  className="hover:text-destructive rounded p-0.5"
                                  title="Delete value"
                                  onClick={() => setDeleteValueTarget({ domainCode: d.code, value: v })}
                                >
                                  <X className="size-3" />
                                </button>
                              </>
                            )}
                          </span>
                        ))}
                      </div>
                      {editingValue?.domainCode === d.code && (
                        <EditDomainValueRow
                          domainCode={d.code}
                          value={editingValue.value}
                          onDone={() => setEditingValue(null)}
                        />
                      )}
                    </CardContent>
                  )}
                  {d.domain_type === "range" && d.range_min !== undefined && d.range_max !== undefined && (
                    <CardContent className="pt-0">
                      <span className="bg-muted text-muted-foreground rounded-md px-1.5 py-0.5 text-xs">
                        {d.range_min} – {d.range_max}
                      </span>
                    </CardContent>
                  )}
                </Card>
              </Fragment>
            ))}
          </div>
        )}
      </div>

      <ConfirmDeleteDialog
        open={deleteTarget !== null}
        title={`Delete ${deleteTarget?.code ?? "this domain"}?`}
        description="Only possible while no field constrains its values to this domain -- if any field already references it, deletion is rejected with a clear error instead. This can't be undone."
        onOpenChange={(open) => !open && setDeleteTarget(null)}
        onConfirm={async () => {
          if (!deleteTarget) return
          await apiFetch(`/admin/domains/${deleteTarget.code}`, { method: "DELETE" })
          queryClient.invalidateQueries({ queryKey: ["admin", "domains"] })
        }}
      />

      <ConfirmDeleteDialog
        open={deleteValueTarget !== null}
        title={`Delete value ${deleteValueTarget?.value.code ?? ""}?`}
        description="Safe to remove from metadata now -- if any feature class using this domain is later recompiled while real data still holds this value, the compiler rejects the narrower constraint outright rather than silently applying it. This can't be undone."
        onOpenChange={(open) => !open && setDeleteValueTarget(null)}
        onConfirm={async () => {
          if (!deleteValueTarget) return
          await apiFetch(`/admin/domains/${deleteValueTarget.domainCode}/values/${deleteValueTarget.value.code}`, {
            method: "DELETE",
          })
          queryClient.invalidateQueries({ queryKey: ["admin", "domains"] })
        }}
      />
    </div>
  )
}
