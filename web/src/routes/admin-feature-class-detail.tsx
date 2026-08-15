import { useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { ArrowLeft, ListPlus, Loader2, Pencil, Plus, X } from "lucide-react"
import { Link, useParams } from "react-router"

import { inputClass } from "@/components/map/field-utils"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { useAdminDomains, useAdminFeatureClasses, useAdminFieldGroups } from "@/hooks/use-admin"
import { type FieldSchema, useFeatureClassSchema } from "@/hooks/use-feature-class-schema"
import { ApiError, apiFetch } from "@/lib/api"
import { useToastStore } from "@/stores/toast-store"

const labelClass = "text-xs font-medium"

const DATA_TYPES = ["text", "numeric", "integer", "boolean", "date", "timestamp", "uuid", "jsonb"] as const

/**
 * One field's domain, rendered the same way regardless of which kind it
 * is -- a coded domain's own values as a pill list, a range domain's
 * bounds as a single "min – max" pill. Mirrors admin-domains.tsx's own
 * per-domain display, just inline in a table cell instead of a card.
 */
function DomainCell({ field }: { field: FieldSchema }) {
  if (!field.domain_code) return <span className="text-muted-foreground">—</span>
  if (field.domain_values) {
    return (
      <div className="flex flex-wrap gap-1">
        <span className="font-mono text-xs">{field.domain_code}</span>
        {field.domain_values.map((v) => (
          <span key={v} className="bg-muted text-muted-foreground rounded-md px-1.5 py-0.5 text-xs">
            {v}
          </span>
        ))}
      </div>
    )
  }
  if (field.range_min !== undefined && field.range_max !== undefined) {
    return (
      <div className="flex items-center gap-1.5">
        <span className="font-mono text-xs">{field.domain_code}</span>
        <span className="bg-muted text-muted-foreground rounded-md px-1.5 py-0.5 text-xs">
          {field.range_min} – {field.range_max}
        </span>
      </div>
    )
  }
  return <span className="font-mono text-xs">{field.domain_code}</span>
}

/**
 * Add one new subtype to this class, any status -- moved here from the
 * admin-feature-classes.tsx list page (a bulkier trigger like this
 * belongs on the one-class-at-a-time detail view, not a row action on a
 * many-classes-at-once table). Same "insert now, recompile to actually
 * enforce it" split as AddFieldRow below and api/internal/compiler's
 * syncCheckConstraints.
 */
function AddSubtypeRow({ code, subtypes, onDone }: { code: string; subtypes: { code: number; name: string }[]; onDone: () => void }) {
  const [subtypeCode, setSubtypeCode] = useState((subtypes.length ?? 0) + 1)
  const [name, setName] = useState("")
  const [saving, setSaving] = useState(false)
  const queryClient = useQueryClient()

  const canSave = name.trim().length > 0 && !subtypes.some((s) => s.code === subtypeCode)

  async function handleAdd() {
    setSaving(true)
    try {
      await apiFetch(`/admin/feature-classes/${code}/subtypes`, {
        method: "POST",
        body: JSON.stringify({ code: subtypeCode, name: name.trim() }),
      })
      queryClient.invalidateQueries({ queryKey: ["feature-class-schema", code] })
      queryClient.invalidateQueries({ queryKey: ["admin", "feature-classes"] })
      onDone()
    } catch (err) {
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not add this subtype.")
    } finally {
      setSaving(false)
    }
  }

  return (
    <Card>
      <CardHeader className="flex flex-row items-start justify-between py-3">
        <CardTitle className="text-sm">Add subtype</CardTitle>
        <Button variant="ghost" size="icon" className="size-7" onClick={onDone} disabled={saving}>
          <X className="size-4" />
        </Button>
      </CardHeader>
      <CardContent className="flex items-end gap-2">
        <div className="flex flex-col gap-1">
          <label className={labelClass}>Code</label>
          <input
            type="number"
            className={`${inputClass} w-20`}
            value={subtypeCode}
            onChange={(e) => setSubtypeCode(Number(e.target.value))}
          />
        </div>
        <div className="flex flex-1 flex-col gap-1">
          <label className={labelClass}>Name</label>
          <input className={inputClass} value={name} onChange={(e) => setName(e.target.value)} placeholder="e.g. recloser" />
        </div>
        <Button size="sm" onClick={handleAdd} disabled={!canSave || saving}>
          {saving && <Loader2 className="size-4 animate-spin" />}
          Add
        </Button>
      </CardContent>
    </Card>
  )
}

/**
 * Add one new field to this class, any status -- api/internal/admin's
 * POST .../fields (add.go). Same additive-only, insert-now-recompile-
 * later split as subtypes/domain values: the metadata insert alone
 * doesn't make the field usable on real features, a recompile actually
 * ALTER TABLE ADD COLUMNs it (compiler.go's existing-table path already
 * did this before this endpoint existed, just never reachable through
 * the API).
 */
function AddFieldRow({
  code,
  subtypes,
  onDone,
}: {
  code: string
  subtypes: { code: number; name: string }[]
  onDone: () => void
}) {
  const { data: domainsData } = useAdminDomains()
  const { data: groupsData } = useAdminFieldGroups()
  const [name, setName] = useState("")
  const [dataType, setDataType] = useState<(typeof DATA_TYPES)[number]>("text")
  const [domainCode, setDomainCode] = useState("")
  const [groupCode, setGroupCode] = useState("")
  const [nullable, setNullable] = useState(true)
  const [visibleOnCreate, setVisibleOnCreate] = useState(true)
  const [visibleOnUpdate, setVisibleOnUpdate] = useState(true)
  const [editableAfterPublish, setEditableAfterPublish] = useState(false)
  const [requiredFor, setRequiredFor] = useState<Set<number>>(new Set())
  const [saving, setSaving] = useState(false)
  const queryClient = useQueryClient()

  const canSave = /^[a-z][a-z0-9_]*$/.test(name)

  function toggleRequired(subtypeCode: number) {
    setRequiredFor((prev) => {
      const next = new Set(prev)
      if (next.has(subtypeCode)) next.delete(subtypeCode)
      else next.add(subtypeCode)
      return next
    })
  }

  async function handleAdd() {
    setSaving(true)
    try {
      await apiFetch(`/admin/feature-classes/${code}/fields`, {
        method: "POST",
        body: JSON.stringify({
          name: name.trim(),
          data_type: dataType,
          domain_code: domainCode || undefined,
          group_code: groupCode || undefined,
          nullable,
          visible_on_create: visibleOnCreate,
          visible_on_update: visibleOnUpdate,
          editable_after_publish: editableAfterPublish,
          required_for_subtypes: [...requiredFor],
        }),
      })
      queryClient.invalidateQueries({ queryKey: ["feature-class-schema", code] })
      queryClient.invalidateQueries({ queryKey: ["admin", "feature-classes"] })
      onDone()
    } catch (err) {
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not add this field.")
    } finally {
      setSaving(false)
    }
  }

  return (
    <Card>
      <CardHeader className="flex flex-row items-start justify-between py-3">
        <CardTitle className="text-sm">Add field</CardTitle>
        <Button variant="ghost" size="icon" className="size-7" onClick={onDone} disabled={saving}>
          <X className="size-4" />
        </Button>
      </CardHeader>
      <CardContent className="flex flex-col gap-3">
        <div className="grid grid-cols-2 gap-3">
          <div className="flex flex-col gap-1">
            <label className={labelClass}>
              Name <span className="text-destructive">*</span>
            </label>
            <input className={inputClass} value={name} onChange={(e) => setName(e.target.value)} placeholder="e.g. voltage_class" />
          </div>
          <div className="flex flex-col gap-1">
            <label className={labelClass}>Data type</label>
            <select className={inputClass} value={dataType} onChange={(e) => setDataType(e.target.value as typeof dataType)}>
              {DATA_TYPES.map((t) => (
                <option key={t} value={t}>
                  {t}
                </option>
              ))}
            </select>
          </div>
          <div className="flex flex-col gap-1">
            <label className={labelClass}>Domain</label>
            <select className={inputClass} value={domainCode} onChange={(e) => setDomainCode(e.target.value)}>
              <option value="">none</option>
              {domainsData?.domains.map((d) => (
                <option key={d.code} value={d.code}>
                  {d.name} ({d.code})
                </option>
              ))}
            </select>
          </div>
          <div className="flex flex-col gap-1">
            <label className={labelClass}>Group</label>
            <select className={inputClass} value={groupCode} onChange={(e) => setGroupCode(e.target.value)}>
              <option value="">none</option>
              {groupsData?.field_groups.map((g) => (
                <option key={g.code} value={g.code}>
                  {g.name}
                </option>
              ))}
            </select>
          </div>
        </div>

        <div className="flex flex-wrap gap-4">
          <label className="flex items-center gap-2 text-sm">
            <input type="checkbox" checked={nullable} onChange={(e) => setNullable(e.target.checked)} />
            Nullable
          </label>
          <label className="flex items-center gap-2 text-sm">
            <input type="checkbox" checked={visibleOnCreate} onChange={(e) => setVisibleOnCreate(e.target.checked)} />
            Visible on create
          </label>
          <label className="flex items-center gap-2 text-sm">
            <input type="checkbox" checked={visibleOnUpdate} onChange={(e) => setVisibleOnUpdate(e.target.checked)} />
            Visible on update
          </label>
          <label className="flex items-center gap-2 text-sm">
            <input
              type="checkbox"
              checked={editableAfterPublish}
              onChange={(e) => setEditableAfterPublish(e.target.checked)}
            />
            Editable after publish
          </label>
        </div>

        {subtypes.length > 0 && (
          <div className="flex flex-col gap-1">
            <label className={labelClass}>Required for subtypes</label>
            <div className="flex flex-wrap gap-3">
              {subtypes.map((s) => (
                <label key={s.code} className="flex items-center gap-2 text-sm">
                  <input type="checkbox" checked={requiredFor.has(s.code)} onChange={() => toggleRequired(s.code)} />
                  {s.name}
                </label>
              ))}
            </div>
          </div>
        )}

        <div className="flex justify-end gap-2 pt-1">
          <Button variant="outline" size="sm" onClick={onDone} disabled={saving}>
            Cancel
          </Button>
          <Button size="sm" onClick={handleAdd} disabled={!canSave || saving}>
            {saving && <Loader2 className="size-4 animate-spin" />}
            Add
          </Button>
        </div>
      </CardContent>
    </Card>
  )
}

/**
 * Edit an existing field's domain, nullable, and form-visibility flags --
 * api/internal/admin's PATCH .../fields/{name} (update.go). Name and
 * data_type aren't editable here because the endpoint itself doesn't
 * accept them (the compiler has no rename/retype path). group_code is
 * also left out of this form: the Fields table has never displayed which
 * group a field belongs to, so there's no current value to show as a
 * starting point -- editing it would mean guessing at a value the admin
 * can't see is being replaced. domain_code sends the empty string to
 * mean "clear the assignment", matching updateFieldRequest's own
 * sentinel convention (see its doc comment in api/internal/admin/types.go).
 */
function EditFieldRow({ code, field, onDone }: { code: string; field: FieldSchema; onDone: () => void }) {
  const { data: domainsData } = useAdminDomains()
  const [domainCode, setDomainCode] = useState(field.domain_code ?? "")
  const [nullable, setNullable] = useState(field.nullable)
  const [visibleOnCreate, setVisibleOnCreate] = useState(field.visible_on_create)
  const [visibleOnUpdate, setVisibleOnUpdate] = useState(field.visible_on_update)
  const [editableAfterPublish, setEditableAfterPublish] = useState(field.editable_after_publish)
  const [saving, setSaving] = useState(false)
  const queryClient = useQueryClient()

  async function handleSave() {
    setSaving(true)
    try {
      await apiFetch(`/admin/feature-classes/${code}/fields/${field.name}`, {
        method: "PATCH",
        body: JSON.stringify({
          domain_code: domainCode,
          nullable,
          visible_on_create: visibleOnCreate,
          visible_on_update: visibleOnUpdate,
          editable_after_publish: editableAfterPublish,
        }),
      })
      queryClient.invalidateQueries({ queryKey: ["feature-class-schema", code] })
      onDone()
    } catch (err) {
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not update this field.")
    } finally {
      setSaving(false)
    }
  }

  return (
    <Card>
      <CardHeader className="flex flex-row items-start justify-between py-3">
        <CardTitle className="text-sm">
          Edit field <span className="font-mono">{field.name}</span>
        </CardTitle>
        <Button variant="ghost" size="icon" className="size-7" onClick={onDone} disabled={saving}>
          <X className="size-4" />
        </Button>
      </CardHeader>
      <CardContent className="flex flex-col gap-3">
        <div className="flex flex-col gap-1">
          <label className={labelClass}>Domain</label>
          <select className={inputClass} value={domainCode} onChange={(e) => setDomainCode(e.target.value)}>
            <option value="">none</option>
            {domainsData?.domains.map((d) => (
              <option key={d.code} value={d.code}>
                {d.name} ({d.code})
              </option>
            ))}
          </select>
        </div>

        <div className="flex flex-wrap gap-4">
          <label className="flex items-center gap-2 text-sm">
            <input type="checkbox" checked={nullable} onChange={(e) => setNullable(e.target.checked)} />
            Nullable
          </label>
          <label className="flex items-center gap-2 text-sm">
            <input type="checkbox" checked={visibleOnCreate} onChange={(e) => setVisibleOnCreate(e.target.checked)} />
            Visible on create
          </label>
          <label className="flex items-center gap-2 text-sm">
            <input type="checkbox" checked={visibleOnUpdate} onChange={(e) => setVisibleOnUpdate(e.target.checked)} />
            Visible on update
          </label>
          <label className="flex items-center gap-2 text-sm">
            <input
              type="checkbox"
              checked={editableAfterPublish}
              onChange={(e) => setEditableAfterPublish(e.target.checked)}
            />
            Editable after publish
          </label>
        </div>

        <div className="flex justify-end gap-2 pt-1">
          <Button variant="outline" size="sm" onClick={onDone} disabled={saving}>
            Cancel
          </Button>
          <Button size="sm" onClick={handleSave} disabled={saving}>
            {saving && <Loader2 className="size-4 animate-spin" />}
            Save
          </Button>
        </div>
      </CardContent>
    </Card>
  )
}

/**
 * A single feature class's full picture -- fields (with resolved
 * domain info), subtypes, terminals -- reusing the exact same
 * GET /feature-classes/{code}/schema response the map's own create/
 * edit forms already read, rather than a second admin-only endpoint.
 * The admin list page (admin-feature-classes.tsx) only ever showed a
 * subtype/field *count*; this is the page that answers "what does this
 * class actually have."
 */
export function AdminFeatureClassDetailPage() {
  const { code } = useParams<{ code: string }>()
  const { data: classesData } = useAdminFeatureClasses()
  const { data: schema, isPending, isError } = useFeatureClassSchema(code)
  const queryClient = useQueryClient()
  const [compiling, setCompiling] = useState(false)
  const [addingSubtype, setAddingSubtype] = useState(false)
  const [addingField, setAddingField] = useState(false)
  const [editingFieldName, setEditingFieldName] = useState<string | null>(null)

  const summary = classesData?.feature_classes.find((fc) => fc.code === code)

  async function handleCompile() {
    if (!code) return
    setCompiling(true)
    try {
      await apiFetch(`/admin/feature-classes/${code}/compile`, { method: "POST" })
      queryClient.invalidateQueries({ queryKey: ["admin", "feature-classes"] })
      queryClient.invalidateQueries({ queryKey: ["feature-classes"] })
    } catch (err) {
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not compile this class.")
    } finally {
      setCompiling(false)
    }
  }

  if (!code) return null

  return (
    <div className="h-full overflow-y-auto p-4">
      <div className="mx-auto flex max-w-4xl flex-col gap-4">
        <Link to="/admin/feature-classes" className="text-muted-foreground flex items-center gap-1 text-sm hover:underline">
          <ArrowLeft className="size-3.5" />
          Feature classes
        </Link>

        {isPending ? (
          <div className="text-muted-foreground flex items-center gap-2 text-sm">
            <Loader2 className="size-4 animate-spin" />
            Loading...
          </div>
        ) : isError || !schema ? (
          <p className="text-destructive text-sm">Could not load this feature class.</p>
        ) : (
          <>
            <div className="flex items-center justify-between">
              <div>
                <h1 className="text-lg font-semibold">
                  {schema.name} <span className="text-muted-foreground font-mono text-sm">{schema.code}</span>
                </h1>
                <p className="text-muted-foreground text-sm">
                  {schema.geometry_type}
                  {summary && ` · ${summary.network_role}`}
                  {summary && (
                    <>
                      {" · "}
                      <Badge variant={summary.status === "active" ? "success" : summary.status === "draft" ? "secondary" : "outline"}>
                        {summary.status}
                      </Badge>
                    </>
                  )}
                </p>
              </div>
              <Button size="sm" variant="outline" onClick={handleCompile} disabled={compiling}>
                {compiling && <Loader2 className="size-4 animate-spin" />}
                {summary?.status === "draft" ? "Compile" : "Recompile"}
              </Button>
            </div>

            <div className="flex items-center justify-between">
              <h2 className="text-sm font-semibold">Subtypes</h2>
              {!addingSubtype && (
                <Button variant="ghost" size="sm" onClick={() => setAddingSubtype(true)}>
                  <Plus className="size-4" />
                  Add subtype
                </Button>
              )}
            </div>
            {addingSubtype && (
              <AddSubtypeRow code={code} subtypes={schema.subtypes} onDone={() => setAddingSubtype(false)} />
            )}
            {schema.subtypes.length === 0 ? (
              <p className="text-muted-foreground text-sm">No subtypes.</p>
            ) : (
              <div className="flex flex-wrap gap-1">
                {schema.subtypes.map((s) => (
                  <span key={s.code} className="bg-muted text-muted-foreground rounded-md px-1.5 py-0.5 text-xs">
                    {s.code}: {s.name}
                  </span>
                ))}
              </div>
            )}

            <div className="flex items-center justify-between">
              <h2 className="text-sm font-semibold">Fields</h2>
              {!addingField && (
                <Button
                  variant="ghost"
                  size="sm"
                  onClick={() => {
                    setEditingFieldName(null)
                    setAddingField(true)
                  }}
                >
                  <ListPlus className="size-4" />
                  Add field
                </Button>
              )}
            </div>
            {addingField && (
              <AddFieldRow code={code} subtypes={schema.subtypes} onDone={() => setAddingField(false)} />
            )}
            {editingFieldName &&
              (() => {
                const editingField = schema.fields.find((f) => f.name === editingFieldName)
                return editingField ? (
                  <EditFieldRow code={code} field={editingField} onDone={() => setEditingFieldName(null)} />
                ) : null
              })()}
            {schema.fields.length === 0 ? (
              <p className="text-muted-foreground text-sm">No fields.</p>
            ) : (
              <div className="overflow-hidden rounded-lg border">
                <table className="w-full text-sm">
                  <thead className="bg-muted/50 text-muted-foreground text-left text-xs">
                    <tr>
                      <th className="px-3 py-2 font-medium">Name</th>
                      <th className="px-3 py-2 font-medium">Type</th>
                      <th className="px-3 py-2 font-medium">Domain</th>
                      <th className="px-3 py-2 font-medium">Nullable</th>
                      <th className="px-3 py-2 font-medium">Create</th>
                      <th className="px-3 py-2 font-medium">Update</th>
                      <th className="px-3 py-2 font-medium">Required for</th>
                      <th className="px-3 py-2 font-medium" />
                    </tr>
                  </thead>
                  <tbody>
                    {schema.fields.map((f) => (
                      <tr key={f.name} className="border-t">
                        <td className="px-3 py-2 font-mono text-xs">{f.name}</td>
                        <td className="px-3 py-2">{f.data_type}</td>
                        <td className="px-3 py-2">
                          <DomainCell field={f} />
                        </td>
                        <td className="px-3 py-2">{f.nullable ? "yes" : "no"}</td>
                        <td className="px-3 py-2">{f.visible_on_create ? "yes" : "no"}</td>
                        <td className="px-3 py-2">{f.visible_on_update ? "yes" : "no"}</td>
                        <td className="px-3 py-2">
                          {f.required_for_subtypes && f.required_for_subtypes.length > 0
                            ? f.required_for_subtypes.join(", ")
                            : "—"}
                        </td>
                        <td className="px-3 py-2 text-right">
                          <Button
                            variant="ghost"
                            size="icon"
                            className="size-7"
                            title="Edit"
                            onClick={() => {
                              setAddingField(false)
                              setEditingFieldName(f.name)
                            }}
                          >
                            <Pencil className="size-3.5" />
                          </Button>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}

            {schema.terminals && schema.terminals.length > 0 && (
              <>
                <h2 className="text-sm font-semibold">Terminals</h2>
                <div className="flex flex-wrap gap-1">
                  {schema.terminals.map((t) => (
                    <span key={`${t.code}:${t.subtype_code ?? "any"}`} className="bg-muted text-muted-foreground rounded-md px-1.5 py-0.5 text-xs">
                      {t.code} ({t.role}
                      {t.subtype_code !== undefined ? `, subtype ${t.subtype_code}` : ""})
                    </span>
                  ))}
                </div>
              </>
            )}
          </>
        )}
      </div>
    </div>
  )
}
