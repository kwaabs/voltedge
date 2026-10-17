import { useMemo, useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { Eye, Loader2, Pencil, Plus, Power, PowerOff, Search, Trash2, X } from "lucide-react"
import { Link } from "react-router"

import { ConfirmDeleteDialog } from "@/components/admin/confirm-delete-dialog"
import { FormDialog } from "@/components/admin/form-dialog"
import { inputClass } from "@/components/map/field-utils"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { usePermission } from "@/hooks/use-auth"
import { type AdminFeatureClassSummary, useAdminFeatureClasses } from "@/hooks/use-admin"
import { ApiError, apiFetch } from "@/lib/api"
import { useToastStore } from "@/stores/toast-store"

const GEOMETRY_TYPES = ["point", "line", "polygon", "none"] as const
const NETWORK_ROLES = ["none", "edge", "junction", "container"] as const
// Every status the catalog can hold (api/internal/admin); "all" is the
// filter's no-filter sentinel, not a real status.
const STATUSES = ["active", "draft", "deprecated"] as const

const labelClass = "text-xs font-medium"

/**
 * Inline edit form for a single draft row's basic info -- name and
 * is_diagram_container are always safe to change (pure metadata,
 * api/internal/admin/update.go), geometry_type/srid/network_role are
 * included here too since this form only ever renders for a row whose
 * status is already confirmed draft (see the "Edit" button below,
 * which doesn't render otherwise) -- an already-compiled class edits
 * its two diagram flags via the detail page's "Diagram settings" section
 * (admin-feature-class-detail.tsx), the same PATCH endpoint, just without
 * this row ever being reachable for geometry_type/srid/network_role.
 */
function EditFeatureClassRow({
  fc,
  editingCode,
  onDone,
}: {
  fc: AdminFeatureClassSummary
  editingCode: string | null
  onDone: () => void
}) {
  const [name, setName] = useState(fc.name)
  const [isDiagramContainer, setIsDiagramContainer] = useState(fc.is_diagram_container)
  const [isDiagramPlaceable, setIsDiagramPlaceable] = useState(fc.is_diagram_placeable)
  const [geometryType, setGeometryType] = useState<(typeof GEOMETRY_TYPES)[number]>(
    fc.geometry_type as (typeof GEOMETRY_TYPES)[number],
  )
  const [srid, setSrid] = useState(fc.srid ?? 4326)
  const [networkRole, setNetworkRole] = useState<(typeof NETWORK_ROLES)[number]>(
    fc.network_role as (typeof NETWORK_ROLES)[number],
  )
  const [saving, setSaving] = useState(false)
  const queryClient = useQueryClient()

  function close() {
    setName(fc.name)
    setIsDiagramContainer(fc.is_diagram_container)
    setIsDiagramPlaceable(fc.is_diagram_placeable)
    setGeometryType(fc.geometry_type as (typeof GEOMETRY_TYPES)[number])
    setSrid(fc.srid ?? 4326)
    setNetworkRole(fc.network_role as (typeof NETWORK_ROLES)[number])
    onDone()
  }

  const canSave = name.trim().length > 0 && (geometryType === "none" || srid > 0)

  async function handleSave() {
    setSaving(true)
    try {
      await apiFetch(`/admin/feature-classes/${fc.code}`, {
        method: "PATCH",
        body: JSON.stringify({
          name: name.trim(),
          is_diagram_container: isDiagramContainer,
          is_diagram_placeable: isDiagramPlaceable,
          geometry_type: geometryType,
          srid: geometryType === "none" ? null : srid,
          network_role: networkRole,
        }),
      })
      queryClient.invalidateQueries({ queryKey: ["admin", "feature-classes"] })
      close()
    } catch (err) {
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not update this feature class.")
    } finally {
      setSaving(false)
    }
  }

  return (
    <FormDialog
      open={editingCode === fc.code}
      onOpenChange={(next) => {
        if (!next) close()
      }}
      title={`Edit ${fc.code}`}
      onSubmit={handleSave}
      canSubmit={canSave}
      saving={saving}
      className="max-w-2xl"
    >
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
      <label className="flex items-center gap-2 text-sm">
        <input type="checkbox" checked={isDiagramPlaceable} onChange={(e) => setIsDiagramPlaceable(e.target.checked)} />
        Can be placed on a diagram (is_diagram_placeable)
      </label>
    </FormDialog>
  )
}

/**
 * The admin catalog view -- every feature class regardless of status
 * (draft/active/deprecated), unlike the map/layers panel's own
 * useFeatureClasses hook (active only). "Compile" is offered inline for
 * a draft class -- the exact same api/internal/compiler.Compile the CLI
 * has always called, just reachable without a terminal now. "Edit" and
 * "Delete" are offered inline too, but only for a draft row -- an
 * already-compiled class can only have its two diagram flags touched
 * (via the detail page's "Diagram settings" section) and can never be
 * deleted through this UI at all, per api/internal/admin/update.go and
 * delete.go's own scope. Adding a
 * subtype or a field, and seeing a class's full field/domain picture,
 * both moved to the detail page (admin-feature-class-detail.tsx,
 * reached via the row's "View" button) -- this table only ever showed
 * a count, never the fields themselves.
 */
export function AdminFeatureClassesPage() {
  const { data, isPending, isError } = useAdminFeatureClasses()
  const queryClient = useQueryClient()
  // Every mutating control here hits an /admin route the server gates on
  // metadata:write (main.go), a permission only admin/break-glass hold.
  // metadata:read alone opens this page (app-sidebar.tsx), so without this
  // gate a viewer/analyst/editor would see Edit/Compile/etc. and only learn
  // they can't on a 403. Match the UI to what enforcement will actually
  // allow -- read-only roles get a clean view-only catalog.
  const canWrite = usePermission()("metadata:write")
  const [compiling, setCompiling] = useState<string | null>(null)
  const [editingCode, setEditingCode] = useState<string | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<AdminFeatureClassSummary | null>(null)
  const [activeTarget, setActiveTarget] = useState<{ code: string; action: "deactivate" | "reactivate" } | null>(null)

  // Client-side filtering -- the whole catalog is already loaded (one
  // query, ~60 rows), so there's nothing to fetch; narrowing is pure
  // in-memory work. `query` matches code OR name (case-insensitive); the
  // three selects each exact-match a column, with "all" meaning unset.
  const [query, setQuery] = useState("")
  const [statusFilter, setStatusFilter] = useState<"all" | (typeof STATUSES)[number]>("all")
  const [geometryFilter, setGeometryFilter] = useState<"all" | (typeof GEOMETRY_TYPES)[number]>("all")
  const [roleFilter, setRoleFilter] = useState<"all" | (typeof NETWORK_ROLES)[number]>("all")

  const allClasses = data?.feature_classes
  const filtered = useMemo(() => {
    const rows = allClasses ?? []
    const q = query.trim().toLowerCase()
    return rows.filter((fc) => {
      if (q && !fc.code.toLowerCase().includes(q) && !fc.name.toLowerCase().includes(q)) return false
      if (statusFilter !== "all" && fc.status !== statusFilter) return false
      if (geometryFilter !== "all" && fc.geometry_type !== geometryFilter) return false
      if (roleFilter !== "all" && fc.network_role !== roleFilter) return false
      return true
    })
  }, [allClasses, query, statusFilter, geometryFilter, roleFilter])

  const filtersActive =
    query.trim() !== "" || statusFilter !== "all" || geometryFilter !== "all" || roleFilter !== "all"
  function clearFilters() {
    setQuery("")
    setStatusFilter("all")
    setGeometryFilter("all")
    setRoleFilter("all")
  }

  // Each control fills its grid column (inputClass carries w-full), so the
  // grid -- not per-control widths -- decides the layout: one column stacked
  // on narrow, two on sm, all four side by side on lg.
  const selectClass = `${inputClass} h-9`

  async function handleCompile(code: string) {
    setCompiling(code)
    try {
      await apiFetch(`/admin/feature-classes/${code}/compile`, { method: "POST" })
      queryClient.invalidateQueries({ queryKey: ["admin", "feature-classes"] })
      queryClient.invalidateQueries({ queryKey: ["feature-classes"] })
      useToastStore.getState().pushSuccess(`Compiled ${code}.`)
    } catch (err) {
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not compile this class.")
    } finally {
      setCompiling(null)
    }
  }

  // Non-destructive global toggle: deactivate hides a live class from use
  // everywhere but keeps its table + data; reactivate restores it. Flips status
  // active <-> deprecated server-side (no recompile, unlike compile/uncompile).
  async function handleSetActive(code: string, active: boolean) {
    setCompiling(code)
    try {
      await apiFetch(`/admin/feature-classes/${code}/${active ? "reactivate" : "deactivate"}`, { method: "POST" })
      queryClient.invalidateQueries({ queryKey: ["admin", "feature-classes"] })
      queryClient.invalidateQueries({ queryKey: ["feature-classes"] })
      useToastStore
        .getState()
        .pushSuccess(active ? `Reactivated ${code}.` : `Deactivated ${code} — hidden from use, data kept.`)
    } catch (err) {
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not change this class.")
    } finally {
      setCompiling(null)
    }
  }

  return (
    <div className="h-full overflow-y-auto p-4">
      <div className="flex w-full flex-col gap-4">
        <div className="flex items-center justify-between">
          <div>
            <h1 className="text-lg font-semibold">Feature classes</h1>
            <p className="text-muted-foreground text-sm">
              Every feature class defined so far, draft or compiled -- create a new one end to end (subtypes,
              fields, terminals) without writing SQL.
            </p>
          </div>
          {canWrite && (
            <Button size="sm" asChild>
              <Link to="/admin/feature-classes/new">
                <Plus className="size-4" />
                New feature class
              </Link>
            </Button>
          )}
        </div>

        {editingCode && data && (
          <EditFeatureClassRow
            fc={data.feature_classes.find((f) => f.code === editingCode)!}
            editingCode={editingCode}
            onDone={() => setEditingCode(null)}
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
          <>
            <div className="flex flex-col gap-2">
              <div className="grid grid-cols-1 gap-2 sm:grid-cols-2 lg:grid-cols-4">
                <div className="relative">
                  <Search className="text-muted-foreground pointer-events-none absolute left-2.5 top-1/2 size-4 -translate-y-1/2" />
                  <input
                    className={`${inputClass} h-9 pl-8`}
                    placeholder="Filter by code or name..."
                    value={query}
                    onChange={(e) => setQuery(e.target.value)}
                  />
                </div>
                <select
                  className={selectClass}
                  value={statusFilter}
                  onChange={(e) => setStatusFilter(e.target.value as typeof statusFilter)}
                  aria-label="Filter by status"
                >
                  <option value="all">All statuses</option>
                  {STATUSES.map((s) => (
                    <option key={s} value={s}>
                      {s}
                    </option>
                  ))}
                </select>
                <select
                  className={selectClass}
                  value={geometryFilter}
                  onChange={(e) => setGeometryFilter(e.target.value as typeof geometryFilter)}
                  aria-label="Filter by geometry"
                >
                  <option value="all">All geometry</option>
                  {GEOMETRY_TYPES.map((g) => (
                    <option key={g} value={g}>
                      {g}
                    </option>
                  ))}
                </select>
                <select
                  className={selectClass}
                  value={roleFilter}
                  onChange={(e) => setRoleFilter(e.target.value as typeof roleFilter)}
                  aria-label="Filter by network role"
                >
                  <option value="all">All roles</option>
                  {NETWORK_ROLES.map((n) => (
                    <option key={n} value={n}>
                      {n}
                    </option>
                  ))}
                </select>
              </div>
              <div className="flex items-center gap-2">
                {filtersActive && (
                  <Button variant="ghost" size="sm" onClick={clearFilters}>
                    <X className="size-4" />
                    Clear
                  </Button>
                )}
                <span className="text-muted-foreground ml-auto text-xs">
                  {filtered.length} of {data.feature_classes.length}
                </span>
              </div>
            </div>

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
                {filtered.length === 0 ? (
                  <tr>
                    <td colSpan={8} className="text-muted-foreground px-3 py-8 text-center text-sm">
                      No feature classes match the current filter.
                    </td>
                  </tr>
                ) : (
                  filtered.map((fc) => (
                  <tr key={fc.code} className="border-t">
                    <td className="px-3 py-2 font-mono text-xs">{fc.code}</td>
                    <td className="px-3 py-2">
                      {fc.name}
                      {fc.is_diagram_container && (
                        <Badge variant="outline" className="ml-2">
                          diagram container
                        </Badge>
                      )}
                      {fc.is_diagram_placeable && (
                        <Badge variant="outline" className="ml-2">
                          diagram placeable
                        </Badge>
                      )}
                    </td>
                    <td className="px-3 py-2">{fc.geometry_type}</td>
                    <td className="px-3 py-2">{fc.network_role}</td>
                    <td className="px-3 py-2">
                      <Badge variant={fc.status === "active" ? "success" : fc.status === "draft" ? "secondary" : "outline"}>
                        {fc.status === "deprecated" ? "inactive" : fc.status}
                      </Badge>
                    </td>
                    <td className="px-3 py-2">{fc.subtype_count}</td>
                    <td className="px-3 py-2">{fc.field_count}</td>
                    <td className="px-3 py-2 text-right">
                      <div className="flex items-center justify-end gap-1">
                        <Button variant="ghost" size="icon" className="size-8" title="View" asChild>
                          <Link to={`/admin/feature-classes/${fc.code}`}>
                            <Eye className="size-4" />
                          </Link>
                        </Button>
                        {canWrite && fc.status === "draft" && (
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
                        {canWrite && fc.status === "active" && (
                          <Button
                            variant="ghost"
                            size="icon"
                            className="size-8"
                            title="Deactivate — hide from use everywhere (keeps the table + data)"
                            onClick={() => setActiveTarget({ code: fc.code, action: "deactivate" })}
                            disabled={compiling === fc.code}
                          >
                            <PowerOff className="size-4" />
                          </Button>
                        )}
                        {canWrite && fc.status === "deprecated" && (
                          <Button
                            variant="ghost"
                            size="icon"
                            className="size-8"
                            title="Reactivate — make this class available again"
                            onClick={() => setActiveTarget({ code: fc.code, action: "reactivate" })}
                            disabled={compiling === fc.code}
                          >
                            <Power className="size-4" />
                          </Button>
                        )}
                        {canWrite && (
                          <Button
                            variant="outline"
                            size="sm"
                            onClick={() => handleCompile(fc.code)}
                            disabled={compiling === fc.code}
                          >
                            {compiling === fc.code && <Loader2 className="size-4 animate-spin" />}
                            {fc.status === "draft" ? "Compile" : "Recompile"}
                          </Button>
                        )}
                      </div>
                    </td>
                  </tr>
                  ))
                )}
              </tbody>
            </table>
            </div>
          </>
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

      <ConfirmDeleteDialog
        open={activeTarget !== null}
        title={
          activeTarget?.action === "reactivate"
            ? `Reactivate ${activeTarget?.code}?`
            : `Deactivate ${activeTarget?.code ?? "this class"}?`
        }
        description={
          activeTarget?.action === "reactivate"
            ? "Makes this class available again everywhere — the map, the New-Feature menu and every form."
            : "Hides this class from use everywhere (the map, the New-Feature menu, the Features browser and every form) and stops it accepting new features. Its table and all data are kept, and you can reactivate it anytime."
        }
        confirmLabel={activeTarget?.action === "reactivate" ? "Reactivate" : "Deactivate"}
        onOpenChange={(open) => !open && setActiveTarget(null)}
        onConfirm={async () => {
          if (!activeTarget) return
          await handleSetActive(activeTarget.code, activeTarget.action === "reactivate")
        }}
      />
    </div>
  )
}
