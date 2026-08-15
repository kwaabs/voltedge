import { useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { Loader2, Plus, Rows3, Trash2, X } from "lucide-react"

import { ConfirmDeleteDialog } from "@/components/admin/confirm-delete-dialog"
import { inputClass } from "@/components/map/field-utils"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { type AdminFieldGroup, useAdminFieldGroups } from "@/hooks/use-admin"
import { ApiError, apiFetch } from "@/lib/api"
import { useToastStore } from "@/stores/toast-store"

/**
 * Field groups are the small, global set of form sections
 * (general/electrical/maintenance/asset/...) a field's group_code
 * points at in the feature-class wizard -- not redefined per class, see
 * migrations/0005's own comment. Cheap enough that this page is just a
 * flat list plus a one-shot create form, no editing yet.
 */
export function AdminFieldGroupsPage() {
  const { data, isPending, isError } = useAdminFieldGroups()
  const queryClient = useQueryClient()

  const [open, setOpen] = useState(false)
  const [code, setCode] = useState("")
  const [name, setName] = useState("")
  const [sortOrder, setSortOrder] = useState(0)
  const [saving, setSaving] = useState(false)
  const [deleteTarget, setDeleteTarget] = useState<AdminFieldGroup | null>(null)

  function reset() {
    setCode("")
    setName("")
    setSortOrder(0)
  }

  const canSave = code.trim().length > 0 && name.trim().length > 0

  async function handleSave() {
    setSaving(true)
    try {
      await apiFetch("/admin/field-groups", {
        method: "POST",
        body: JSON.stringify({ code: code.trim(), name: name.trim(), sort_order: sortOrder }),
      })
      queryClient.invalidateQueries({ queryKey: ["admin", "field-groups"] })
      setOpen(false)
      reset()
    } catch (err) {
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not create this field group.")
    } finally {
      setSaving(false)
    }
  }

  return (
    <div className="h-full overflow-y-auto p-4">
      <div className="mx-auto flex max-w-3xl flex-col gap-4">
        <div className="flex items-center justify-between">
          <div>
            <h1 className="text-lg font-semibold">Field groups</h1>
            <p className="text-muted-foreground text-sm">
              The form sections a field can belong to -- global, shared across every feature class.
            </p>
          </div>
          {!open && (
            <Button size="sm" onClick={() => setOpen(true)}>
              <Plus className="size-4" />
              New field group
            </Button>
          )}
        </div>

        {open && (
          <Card>
            <CardHeader className="flex flex-row items-start justify-between">
              <div>
                <CardTitle>New field group</CardTitle>
                <CardDescription>A stable code, a display label, and where it sorts.</CardDescription>
              </div>
              <Button
                variant="ghost"
                size="icon"
                className="size-7"
                onClick={() => {
                  setOpen(false)
                  reset()
                }}
                disabled={saving}
              >
                <X className="size-4" />
              </Button>
            </CardHeader>
            <CardContent className="flex flex-col gap-3">
              <div className="grid grid-cols-3 gap-3">
                <div className="flex flex-col gap-1">
                  <label className="text-xs font-medium">
                    Code <span className="text-destructive">*</span>
                  </label>
                  <input
                    className={inputClass}
                    value={code}
                    onChange={(e) => setCode(e.target.value.toLowerCase())}
                    placeholder="e.g. lighting"
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
                    placeholder="e.g. Lighting"
                  />
                </div>
                <div className="flex flex-col gap-1">
                  <label className="text-xs font-medium">Sort order</label>
                  <input
                    type="number"
                    className={inputClass}
                    value={sortOrder}
                    onChange={(e) => setSortOrder(Number(e.target.value))}
                  />
                </div>
              </div>
              <div className="flex justify-end gap-2 pt-1">
                <Button
                  variant="outline"
                  size="sm"
                  onClick={() => {
                    setOpen(false)
                    reset()
                  }}
                  disabled={saving}
                >
                  Cancel
                </Button>
                <Button size="sm" onClick={handleSave} disabled={!canSave || saving}>
                  {saving && <Loader2 className="size-4 animate-spin" />}
                  Create
                </Button>
              </div>
            </CardContent>
          </Card>
        )}

        {isPending ? (
          <div className="text-muted-foreground flex items-center gap-2 text-sm">
            <Loader2 className="size-4 animate-spin" />
            Loading field groups...
          </div>
        ) : isError || !data ? (
          <p className="text-destructive text-sm">Could not load field groups.</p>
        ) : data.field_groups.length === 0 ? (
          <p className="text-muted-foreground text-sm">No field groups yet.</p>
        ) : (
          <div className="flex flex-col gap-2">
            {data.field_groups.map((g) => (
              <Card key={g.code}>
                <CardHeader className="flex flex-row items-center gap-2 py-3">
                  <Rows3 className="text-muted-foreground size-4" />
                  <div className="min-w-0 flex-1">
                    <CardTitle className="text-sm">{g.name}</CardTitle>
                    <CardDescription className="font-mono text-xs">{g.code}</CardDescription>
                  </div>
                  <span className="text-muted-foreground text-xs">#{g.sort_order}</span>
                  <Button
                    variant="ghost"
                    size="icon"
                    className="text-destructive size-7"
                    onClick={() => setDeleteTarget(g)}
                  >
                    <Trash2 className="size-4" />
                  </Button>
                </CardHeader>
              </Card>
            ))}
          </div>
        )}
      </div>

      <ConfirmDeleteDialog
        open={deleteTarget !== null}
        title={`Delete ${deleteTarget?.code ?? "this field group"}?`}
        description="Only possible while no field belongs to this group -- if any field already references it, deletion is rejected with a clear error instead. This can't be undone."
        onOpenChange={(open) => !open && setDeleteTarget(null)}
        onConfirm={async () => {
          if (!deleteTarget) return
          await apiFetch(`/admin/field-groups/${deleteTarget.code}`, { method: "DELETE" })
          queryClient.invalidateQueries({ queryKey: ["admin", "field-groups"] })
        }}
      />
    </div>
  )
}
