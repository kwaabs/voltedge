import { useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { ListPlus, Loader2, Pencil, Plus, Trash2, X } from "lucide-react"
import { Link } from "react-router"

import { ConfirmDeleteDialog } from "@/components/admin/confirm-delete-dialog"
import { inputClass } from "@/components/map/field-utils"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { type AdminFeatureClassSummary, useAdminFeatureClasses } from "@/hooks/use-admin"
import { useFeatureClassSchema } from "@/hooks/use-feature-class-schema"
import { ApiError, apiFetch } from "@/lib/api"
import { useToastStore } from "@/stores/toast-store"

const GEOMETRY_TYPES = ["point", "line", "polygon", "none"] as const
const NETWORK_ROLES = ["none", "edge", "junction", "container"] as const

const labelClass = "text-xs font-medium"

/**
 * Inline edit form for a single draft row's basic info -- name and
 * is_diagram_container are always safe to change (pure metadata,
 * api/internal/admin/update.go), geometry_type/srid/network_role are
 * included here too since this form only ever renders for a row whose
 * status is already confirmed draft (see the "Edit" button below,
 * which doesn't render otherwise) -- an already-compiled class edits
 * name/is_diagram_container only, via the same PATCH endpoint, just
 * without this row ever being reachable for the other three.
 */
function EditFeatureClassRow({ fc, onDone }: { fc: AdminFeatureClassSummary; onDone: () => void }) {
  const [name, setName] = useState(fc.name)
  const [isDiagramContainer, setIsDiagramContainer] = useState(fc.is_diagram_container)
  const [geometryType, setGeometryType] = useState<(typeof GEOMETRY_TYPES)[number]>(
    fc.geometry_type as (typeof GEOMETRY_TYPES)[number],
  )
  const [srid, setSrid] = useState(fc.srid ?? 4326)
  const [networkRole, setNetworkRole] = useState<(typeof NETWORK_ROLES)[number]>(
    fc.network_role as (typeof NETWORK_ROLES)[number],
  )
  const [saving, setSaving] = useState(false)
  const queryClient = useQueryClient()

  const canSave = name.trim().length > 0 && (geometryType === "none" || srid > 0)

  async function handleSave() {
    setSaving(true)
    try {
      await apiFetch(`/admin/feature-classes/${fc.code}`, {
        method: "PATCH",
        body: JSON.stringify({
          name: name.trim(),
          is_diagram_container: isDiagramContainer,
          geometry_type: geometryType,
          srid: geometryType === "none" ? null : srid,
          network_role: networkRole,
        }),
      })
      queryClient.invalidateQueries({ queryKey: ["admin", "feature-classes"] })
      onDone()
    } catch (err) {
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not update this feature class.")
    } finally {
      setSaving(false)
    }
  }

  return (
    <Card>
      <CardHeader className="flex flex-row items-start justify-between py-3">
        <CardTitle className="text-sm">Edit {fc.code}</CardTitle>
        <Button variant="ghost" size="icon" className="size-7" onClick={onDone} disabled={saving}>
          <X className="size-4" />
        </Button>
      </CardHeader>
      <CardContent className="flex flex-col gap-3">
        <div className="flex flex-col gap-1">
          <label className={labelClass}>Name</label>
          <input className={inputClass} value={name} onChange={(e) => setName(e.target.value)} />
        </div>
        <div className="grid grid-cols-3 gap-3">
          <div className="flex flex-col gap-1">
            <label className={labelClass}>Geometry type</label>
            <select className={inputClass} value={geometryType} onChange={(e) => setGeometryType(e.target.value as typeof geometryType)}>
              {GEOMETRY_TYPES.map((g) => (
                <option key={g} value={g}>
                  {g}
                </option>
              ))}
            </select>
          </div>
          {geometryType !== "none" && (
            <div className="flex flex-col gap-1">
              <label className={labelClass}>SRID</label>
              <input type="number" className={inputClass} value={srid} onChange={(e) => setSrid(Number(e.target.value))} />
            </div>
          )}
          <div className="flex flex-col gap-1">
            <label className={labelClass}>Network role</label>
            <select className={inputClass} value={networkRole} onChange={(e) => setNetworkRole(e.target.value as typeof networkRole)}>
              {NETWORK_ROLES.map((n) => (
                <option key={n} value={n}>
                  {n}
                </option>
              ))}
            </select>
          </div>
        </div>
        <label className="flex items-center gap-2 text-sm">
          <input type="checkbox" checked={isDiagramContainer} onChange={(e) => setIsDiagramContainer(e.target.checked)} />
          Can host a one-line/schematic diagram (is_diagram_container)
        </label>
        <div className="flex justify-end gap-2 pt-1">
          <Button variant="outline" size="sm" onClick={onDone} disabled={saving}>
            Cancel
          </Button>
          <Button size="sm" onClick={handleSave} disabled={!canSave || saving}>
            {saving && <Loader2 className="size-4 animate-spin" />}
            Save
          </Button>
        </div>
      </CardContent>
    </Card>
  )
}

/**
 * Add one new subtype to an existing class, any status -- unlike
 * EditFeatureClassRow, deliberately not draft-gated: adding a subtype
 * to an already-compiled class is exactly the point of this control
 * (api/internal/admin/add.go, api/internal/compiler's
 * syncCheckConstraints). The metadata insert alone doesn't make it
 * usable, though -- shows a clear "recompile to apply" note, and for
 * an already-compiled class, an inline Recompile button right here so
 * the whole add-then-apply flow never has to leave this row.
 */
function AddSubtypeRow({ fc, onDone }: { fc: AdminFeatureClassSummary; onDone: () => void }) {
  const { data: schema } = useFeatureClassSchema(fc.code)
  const [code, setCode] = useState((schema?.subtypes.length ?? 0) + 1)
  const [name, setName] = useState("")
  const [saving, setSaving] = useState(false)
  const [recompiling, setRecompiling] = useState(false)
  const [added, setAdded] = useState(false)
  const queryClient = useQueryClient()

  const canSave = name.trim().length > 0 && !schema?.subtypes.some((s) => s.code === code)

  async function handleAdd() {
    setSaving(true)
    try {
      await apiFetch(`/admin/feature-classes/${fc.code}/subtypes`, {
        method: "POST",
        body: JSON.stringify({ code, name: name.trim() }),
      })
      queryClient.invalidateQueries({ queryKey: ["feature-class-schema", fc.code] })
      queryClient.invalidateQueries({ queryKey: ["admin", "feature-classes"] })
      setName("")
      setCode((c) => c + 1)
      setAdded(true)
    } catch (err) {
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not add this subtype.")
    } finally {
      setSaving(false)
    }
  }

  async function handleRecompile() {
    setRecompiling(true)
    try {
      await apiFetch(`/admin/feature-classes/${fc.code}/compile`, { method: "POST" })
      queryClient.invalidateQueries({ queryKey: ["admin", "feature-classes"] })
      queryClient.invalidateQueries({ queryKey: ["feature-classes"] })
    } catch (err) {
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not recompile this class.")
    } finally {
      setRecompiling(false)
    }
  }

  return (
    <Card>
      <CardHeader className="flex flex-row items-start justify-between py-3">
        <CardTitle className="text-sm">Add subtype to {fc.code}</CardTitle>
        <Button variant="ghost" size="icon" className="size-7" onClick={onDone} disabled={saving}>
          <X className="size-4" />
        </Button>
      </CardHeader>
      <CardContent className="flex flex-col gap-3">
        {schema && schema.subtypes.length > 0 && (
          <div className="flex flex-wrap gap-1">
            {schema.subtypes.map((s) => (
              <span key={s.code} className="bg-muted text-muted-foreground rounded-md px-1.5 py-0.5 text-xs">
                {s.code}: {s.name}
              </span>
            ))}
          </div>
        )}
        <div className="flex items-end gap-2">
          <div className="flex flex-col gap-1">
            <label className={labelClass}>Code</label>
            <input
              type="number"
              className={`${inputClass} w-20`}
              value={code}
              onChange={(e) => setCode(Number(e.target.value))}
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
        </div>
        {added && fc.status !== "draft" && (
          <div className="bg-muted flex items-center justify-between rounded-md px-3 py-2 text-xs">
            <span>Added to metadata -- recompile to actually allow it on real features.</span>
            <Button variant="outline" size="sm" onClick={handleRecompile} disabled={recompiling}>
              {recompiling && <Loader2 className="size-4 animate-spin" />}
              Recompile
            </Button>
          </div>
        )}
      </CardContent>
    </Card>
  )
}

/**
 * The admin catalog view -- every feature class regardless of status
 * (draft/active/deprecated), unlike the map/layers panel's own
 * useFeatureClasses hook (active only). "Compile" is offered inline for
 * a draft class -- the exact same api/internal/compiler.Compile the CLI
 * has always called, just reachable without a terminal now. "Edit" and
 * "Delete" are offered inline too, but only for a draft row -- an
 * already-compiled class can only have its name/is_diagram_container
 * touched (via the same edit form reached a different way, not built
 * here yet) and can never be deleted through this UI at all, per
 * api/internal/admin/update.go and delete.go's own scope.
 */
export function AdminFeatureClassesPage() {
  const { data, isPending, isError } = useAdminFeatureClasses()
  const queryClient = useQueryClient()
  const [compiling, setCompiling] = useState<string | null>(null)
  const [editingCode, setEditingCode] = useState<string | null>(null)
  const [addingSubtypeCode, setAddingSubtypeCode] = useState<string | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<AdminFeatureClassSummary | null>(null)

  async function handleCompile(code: string) {
    setCompiling(code)
    try {
      await apiFetch(`/admin/feature-classes/${code}/compile`, { method: "POST" })
      queryClient.invalidateQueries({ queryKey: ["admin", "feature-classes"] })
      queryClient.invalidateQueries({ queryKey: ["feature-classes"] })
    } catch (err) {
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not compile this class.")
    } finally {
      setCompiling(null)
    }
  }

  return (
    <div className="h-full overflow-y-auto p-4">
      <div className="mx-auto flex max-w-5xl flex-col gap-4">
        <div className="flex items-center justify-between">
          <div>
            <h1 className="text-lg font-semibold">Feature classes</h1>
            <p className="text-muted-foreground text-sm">
              Every feature class defined so far, draft or compiled -- create a new one end to end (subtypes,
              fields, terminals) without writing SQL.
            </p>
          </div>
          <Button size="sm" asChild>
            <Link to="/admin/feature-classes/new">
              <Plus className="size-4" />
              New feature class
            </Link>
          </Button>
        </div>

        {editingCode && data && (
          <EditFeatureClassRow
            fc={data.feature_classes.find((f) => f.code === editingCode)!}
            onDone={() => setEditingCode(null)}
          />
        )}

        {addingSubtypeCode && data && (
          <AddSubtypeRow
            fc={data.feature_classes.find((f) => f.code === addingSubtypeCode)!}
            onDone={() => setAddingSubtypeCode(null)}
          />
        )}

        {isPending ? (
          <div className="text-muted-foreground flex items-center gap-2 text-sm">
            <Loader2 className="size-4 animate-spin" />
            Loading feature classes...
          </div>
        ) : isError || !data ? (
          <p className="text-destructive text-sm">Could not load feature classes.</p>
        ) : (
          <div className="overflow-hidden rounded-lg border">
            <table className="w-full text-sm">
              <thead className="bg-muted/50 text-muted-foreground text-left text-xs">
                <tr>
                  <th className="px-3 py-2 font-medium">Code</th>
                  <th className="px-3 py-2 font-medium">Name</th>
                  <th className="px-3 py-2 font-medium">Geometry</th>
                  <th className="px-3 py-2 font-medium">Network role</th>
                  <th className="px-3 py-2 font-medium">Status</th>
                  <th className="px-3 py-2 font-medium">Subtypes</th>
                  <th className="px-3 py-2 font-medium">Fields</th>
                  <th className="px-3 py-2 font-medium"></th>
                </tr>
              </thead>
              <tbody>
                {data.feature_classes.map((fc) => (
                  <tr key={fc.code} className="border-t">
                    <td className="px-3 py-2 font-mono text-xs">{fc.code}</td>
                    <td className="px-3 py-2">
                      {fc.name}
                      {fc.is_diagram_container && (
                        <Badge variant="outline" className="ml-2">
                          diagram container
                        </Badge>
                      )}
                    </td>
                    <td className="px-3 py-2">{fc.geometry_type}</td>
                    <td className="px-3 py-2">{fc.network_role}</td>
                    <td className="px-3 py-2">
                      <Badge variant={fc.status === "active" ? "success" : fc.status === "draft" ? "secondary" : "outline"}>
                        {fc.status}
                      </Badge>
                    </td>
                    <td className="px-3 py-2">{fc.subtype_count}</td>
                    <td className="px-3 py-2">{fc.field_count}</td>
                    <td className="px-3 py-2 text-right">
                      <div className="flex items-center justify-end gap-1">
                        <Button
                          variant="ghost"
                          size="icon"
                          className="size-8"
                          title="Add subtype"
                          onClick={() => setAddingSubtypeCode(fc.code)}
                        >
                          <ListPlus className="size-4" />
                        </Button>
                        {fc.status === "draft" && (
                          <>
                            <Button variant="ghost" size="icon" className="size-8" onClick={() => setEditingCode(fc.code)}>
                              <Pencil className="size-4" />
                            </Button>
                            <Button
                              variant="ghost"
                              size="icon"
                              className="text-destructive size-8"
                              onClick={() => setDeleteTarget(fc)}
                            >
                              <Trash2 className="size-4" />
                            </Button>
                          </>
                        )}
                        <Button
                          variant="outline"
                          size="sm"
                          onClick={() => handleCompile(fc.code)}
                          disabled={compiling === fc.code}
                        >
                          {compiling === fc.code && <Loader2 className="size-4 animate-spin" />}
                          {fc.status === "draft" ? "Compile" : "Recompile"}
                        </Button>
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>

      <ConfirmDeleteDialog
        open={deleteTarget !== null}
        title={`Delete ${deleteTarget?.code ?? "this feature class"}?`}
        description="Permanently removes this draft class and everything defined on it so far (subtypes, fields, terminals) -- nothing physical exists yet, so there's nothing else to undo. This can't be undone."
        onOpenChange={(open) => !open && setDeleteTarget(null)}
        onConfirm={async () => {
          if (!deleteTarget) return
          await apiFetch(`/admin/feature-classes/${deleteTarget.code}`, { method: "DELETE" })
          queryClient.invalidateQueries({ queryKey: ["admin", "feature-classes"] })
        }}
      />
    </div>
  )
}
