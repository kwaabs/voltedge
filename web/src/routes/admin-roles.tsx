import { useEffect, useMemo, useState } from "react"
import { Copy, Loader2, Lock, Plus, ShieldCheck, Trash2 } from "lucide-react"

import { FormDialog } from "@/components/admin/form-dialog"
import { inputClass } from "@/components/map/field-utils"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { usePermission } from "@/hooks/use-auth"
import {
  type AdminRoleFull,
  useCloneRole,
  useCreateRole,
  useDeleteRole,
  usePermissionCatalog,
  useRolesAdmin,
  useSetRolePermissions,
  useUpdateRole,
} from "@/hooks/use-role-admin"
import { ApiError } from "@/lib/api"
import { cn } from "@/lib/utils"
import { useToastStore } from "@/stores/toast-store"

const labelClass = "text-xs font-medium"

function toastError(err: unknown, fallback: string) {
  useToastStore.getState().pushError(err instanceof ApiError ? err.message : fallback)
}

// Group permissions by domain so a 40-permission editor reads legibly. Derived
// from the key prefix (features:…, oms:…) — no server-side category needed.
const PREFIX_CATEGORY: Record<string, string> = {
  features: "Network data",
  connectivity: "Network data",
  relationships: "Network data",
  circuits: "Network data",
  trace: "Engineering & studies",
  analysis: "Engineering & studies",
  diagrams: "Diagrams & symbology",
  annotations: "Diagrams & symbology",
  assemblies: "Diagrams & symbology",
  oms: "Operations",
  operations: "Operations",
  safety: "Operations",
  faults: "Operations",
  telemetry: "Operations",
  wam: "Asset & maintenance",
  tags: "Asset & maintenance",
  metadata: "Data & GIS admin",
  attachments: "Files & attachments",
  field: "Field",
  sync: "Field",
  admin: "Platform & security",
  territories: "Platform & security",
}
const CATEGORY_ORDER = [
  "Network data",
  "Engineering & studies",
  "Diagrams & symbology",
  "Operations",
  "Asset & maintenance",
  "Data & GIS admin",
  "Files & attachments",
  "Field",
  "Platform & security",
  "Other",
]
function categoryFor(key: string): string {
  return PREFIX_CATEGORY[key.split(":")[0]] ?? "Other"
}

export function AdminRolesPage() {
  const roles = useRolesAdmin()
  const catalog = usePermissionCatalog()
  const canWrite = usePermission()("admin:users")

  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [creating, setCreating] = useState(false)
  const [cloning, setCloning] = useState<AdminRoleFull | null>(null)

  const rows = roles.data?.roles ?? []
  const selected = rows.find((r) => r.id === selectedId) ?? null

  return (
    <div className="mx-auto flex max-w-6xl flex-col gap-4 p-4">
      <div className="flex items-start justify-between gap-2">
        <div>
          <h1 className="flex items-center gap-2 text-lg font-semibold">
            <ShieldCheck className="size-5" />
            Roles &amp; permissions
          </h1>
          <p className="text-muted-foreground text-sm">
            Job-family roles bundle permissions. The system roles (greyed) are locked — clone one to tailor it. Assign
            roles to people on the Users page; a user's access is the union of their roles.
          </p>
        </div>
        {canWrite && (
          <Button size="sm" className="shrink-0" onClick={() => setCreating(true)}>
            <Plus className="size-4" />
            New role
          </Button>
        )}
      </div>

      <div className="grid gap-4 lg:grid-cols-[18rem_1fr]">
        {/* Role list */}
        <Card className="min-h-0">
          <CardHeader className="pb-2">
            <CardTitle className="text-sm">Roles</CardTitle>
          </CardHeader>
          <CardContent className="pt-0">
            {roles.isPending ? (
              <div className="text-muted-foreground flex items-center gap-2 text-sm">
                <Loader2 className="size-4 animate-spin" /> Loading…
              </div>
            ) : (
              <div className="flex flex-col gap-1">
                {rows.map((r) => (
                  <button
                    key={r.id}
                    type="button"
                    onClick={() => setSelectedId(r.id)}
                    className={cn(
                      "flex flex-col gap-0.5 rounded-md border px-2 py-1.5 text-left",
                      r.id === selectedId ? "border-primary bg-primary/5" : "border-border/60 hover:bg-muted/40",
                    )}
                  >
                    <span className="flex items-center gap-1.5 text-sm font-medium">
                      {r.is_system && <Lock className="text-muted-foreground size-3" />}
                      {r.name}
                    </span>
                    <span className="text-muted-foreground text-xs">
                      {r.key} · {r.permissions.length} permission{r.permissions.length === 1 ? "" : "s"}
                    </span>
                  </button>
                ))}
              </div>
            )}
          </CardContent>
        </Card>

        {/* Editor */}
        {selected ? (
          <RoleEditor key={selected.id} role={selected} canWrite={canWrite} permissions={catalog.data?.permissions ?? []} onClone={() => setCloning(selected)} onDeleted={() => setSelectedId(null)} />
        ) : (
          <Card>
            <CardContent className="text-muted-foreground p-6 text-sm">Select a role to view or edit its permissions.</CardContent>
          </Card>
        )}
      </div>

      {creating && <RoleFormDialog mode="create" onClose={() => setCreating(false)} onCreated={(r) => setSelectedId(r.id)} />}
      {cloning && <RoleFormDialog mode="clone" source={cloning} onClose={() => setCloning(null)} onCreated={(r) => setSelectedId(r.id)} />}
    </div>
  )
}

function RoleEditor({
  role,
  canWrite,
  permissions,
  onClone,
  onDeleted,
}: {
  role: AdminRoleFull
  canWrite: boolean
  permissions: { key: string; description: string }[]
  onClone: () => void
  onDeleted: () => void
}) {
  const editable = canWrite && !role.is_system
  const update = useUpdateRole()
  const setPerms = useSetRolePermissions()
  const del = useDeleteRole()

  const [name, setName] = useState(role.name)
  const [description, setDescription] = useState(role.description)
  const [perms, setPermsState] = useState<Set<string>>(new Set(role.permissions))
  useEffect(() => {
    setName(role.name)
    setDescription(role.description)
    setPermsState(new Set(role.permissions))
  }, [role])

  const grouped = useMemo(() => {
    const byCat = new Map<string, { key: string; description: string }[]>()
    for (const p of permissions) {
      const c = categoryFor(p.key)
      if (!byCat.has(c)) byCat.set(c, [])
      byCat.get(c)!.push(p)
    }
    return CATEGORY_ORDER.filter((c) => byCat.has(c)).map((c) => ({ category: c, items: byCat.get(c)! }))
  }, [permissions])

  const detailsDirty = name.trim() !== role.name || description.trim() !== role.description
  const permsDirty =
    perms.size !== role.permissions.length || role.permissions.some((k) => !perms.has(k))

  function togglePerm(key: string) {
    setPermsState((prev) => {
      const next = new Set(prev)
      if (next.has(key)) next.delete(key)
      else next.add(key)
      return next
    })
  }

  function saveDetails() {
    update.mutate({ id: role.id, name: name.trim(), description: description.trim() }, { onError: (e) => toastError(e, "Could not save role details.") })
  }
  function savePerms() {
    setPerms.mutate({ id: role.id, permissions: [...perms] }, { onError: (e) => toastError(e, "Could not save permissions.") })
  }
  function remove() {
    del.mutate({ id: role.id }, { onSuccess: onDeleted, onError: (e) => toastError(e, "Could not delete the role.") })
  }

  return (
    <Card className="flex min-h-0 flex-col">
      <CardHeader className="pb-2">
        <div className="flex items-start justify-between gap-2">
          <div className="min-w-0">
            <CardTitle className="flex items-center gap-2 text-sm">
              {role.is_system && <Lock className="text-muted-foreground size-3.5" />}
              {role.name}
              <span className="text-muted-foreground font-mono text-xs font-normal">{role.key}</span>
            </CardTitle>
            {role.is_system && <p className="text-muted-foreground mt-0.5 text-xs">System role — read-only. Clone it to customise.</p>}
          </div>
          <div className="flex shrink-0 gap-1">
            {canWrite && (
              <Button variant="outline" size="sm" onClick={onClone}>
                <Copy className="mr-1 size-3.5" />
                Clone
              </Button>
            )}
            {editable && (
              <Button variant="ghost" size="sm" className="text-muted-foreground hover:text-destructive" disabled={del.isPending} onClick={remove}>
                <Trash2 className="size-4" />
              </Button>
            )}
          </div>
        </div>
      </CardHeader>
      <CardContent className="space-y-4 pt-0">
        {/* Details */}
        {editable && (
          <div className="flex flex-col gap-2 border-b pb-4">
            <div className="flex gap-2">
              <div className="flex flex-1 flex-col gap-1">
                <label className={labelClass}>Name</label>
                <input className={inputClass} value={name} onChange={(e) => setName(e.target.value)} />
              </div>
              <div className="flex flex-[2] flex-col gap-1">
                <label className={labelClass}>Description</label>
                <input className={inputClass} value={description} onChange={(e) => setDescription(e.target.value)} />
              </div>
            </div>
            {detailsDirty && (
              <div>
                <Button size="sm" disabled={update.isPending || name.trim() === ""} onClick={saveDetails}>
                  {update.isPending && <Loader2 className="mr-1 size-4 animate-spin" />}
                  Save details
                </Button>
              </div>
            )}
          </div>
        )}

        {/* Permissions */}
        <div className="flex items-center justify-between gap-2">
          <span className="text-sm font-medium">Permissions ({perms.size})</span>
          {editable && permsDirty && (
            <Button size="sm" disabled={setPerms.isPending} onClick={savePerms}>
              {setPerms.isPending && <Loader2 className="mr-1 size-4 animate-spin" />}
              Save permissions
            </Button>
          )}
        </div>
        <div className="flex flex-col gap-4">
          {grouped.map(({ category, items }) => (
            <div key={category}>
              <div className="text-muted-foreground mb-1 text-[11px] font-semibold uppercase tracking-wide">{category}</div>
              <div className="grid grid-cols-1 gap-x-4 gap-y-1 sm:grid-cols-2">
                {items.map((p) => (
                  <label
                    key={p.key}
                    className={cn("flex items-start gap-2 rounded px-1 py-0.5 text-sm", editable && "hover:bg-muted/40 cursor-pointer")}
                    title={p.description}
                  >
                    <input
                      type="checkbox"
                      className="mt-0.5"
                      checked={perms.has(p.key)}
                      disabled={!editable}
                      onChange={() => togglePerm(p.key)}
                    />
                    <span className="min-w-0">
                      <span className="font-mono text-xs">{p.key}</span>
                      <span className="text-muted-foreground block text-xs">{p.description}</span>
                    </span>
                  </label>
                ))}
              </div>
            </div>
          ))}
        </div>
      </CardContent>
    </Card>
  )
}

/** Create a new role, or clone an existing one into a new editable role. */
function RoleFormDialog({
  mode,
  source,
  onClose,
  onCreated,
}: {
  mode: "create" | "clone"
  source?: AdminRoleFull
  onClose: () => void
  onCreated: (role: AdminRoleFull) => void
}) {
  const create = useCreateRole()
  const clone = useCloneRole()
  const [key, setKey] = useState("")
  const [name, setName] = useState(source ? `${source.name} (copy)` : "")
  const [description, setDescription] = useState("")
  const pending = create.isPending || clone.isPending
  const canSave = /^[a-z][a-z0-9-]{1,48}$/.test(key.trim()) && name.trim() !== "" && !pending

  function handleSave() {
    if (mode === "clone" && source) {
      clone.mutate(
        { id: source.id, key: key.trim(), name: name.trim() },
        { onSuccess: (r) => { onCreated(r); onClose() }, onError: (e) => toastError(e, "Could not clone the role.") },
      )
    } else {
      create.mutate(
        { key: key.trim(), name: name.trim(), description: description.trim() },
        { onSuccess: (r) => { onCreated(r); onClose() }, onError: (e) => toastError(e, "Could not create the role.") },
      )
    }
  }

  return (
    <FormDialog
      open
      onOpenChange={(next) => next || onClose()}
      title={mode === "clone" ? `Clone "${source?.name}"` : "New role"}
      description={mode === "clone" ? "Creates a new editable role with the same permissions." : "A new, empty role you then grant permissions to."}
      onSubmit={handleSave}
      submitLabel={mode === "clone" ? "Clone role" : "Create role"}
      canSubmit={canSave}
      saving={pending}
    >
      <div className="flex flex-col gap-1">
        <label className={labelClass}>Key</label>
        <input className={inputClass} value={key} onChange={(e) => setKey(e.target.value)} placeholder="e.g. senior-operator" autoFocus />
        <span className="text-muted-foreground text-xs">Lowercase letters, digits and hyphens. Immutable once set.</span>
      </div>
      <div className="flex flex-col gap-1">
        <label className={labelClass}>Name</label>
        <input className={inputClass} value={name} onChange={(e) => setName(e.target.value)} placeholder="e.g. Senior Operator" />
      </div>
      {mode === "create" && (
        <div className="flex flex-col gap-1">
          <label className={labelClass}>Description</label>
          <input className={inputClass} value={description} onChange={(e) => setDescription(e.target.value)} placeholder="Optional" />
        </div>
      )}
    </FormDialog>
  )
}
