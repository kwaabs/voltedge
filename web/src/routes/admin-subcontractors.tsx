import { useEffect, useState } from "react"
import { Building2, Loader2, Plus } from "lucide-react"

import { inputClass } from "@/components/map/field-utils"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { WorkOrderMiniList } from "@/components/wam/work-order-mini-list"
import {
  type Subcontractor,
  useCreateSubcontractor,
  useSetSubcontractorActive,
  useSubcontractors,
  useUpdateSubcontractor,
} from "@/hooks/use-wam"
import { ApiError } from "@/lib/api"
import { cn } from "@/lib/utils"
import { useToastStore } from "@/stores/toast-store"

function toastErr(err: unknown, fallback: string) {
  useToastStore.getState().pushError(err instanceof ApiError ? err.message : fallback)
}

/** The subcontractor registry: external firms the utility tasks field work to.
 * They do not operate in this system — this records who is tasked. Gated at the
 * route by wam:write. */
export function AdminSubcontractorsPage() {
  const subs = useSubcontractors({ includeInactive: true })
  const [creating, setCreating] = useState(false)
  const [editing, setEditing] = useState<Subcontractor | null>(null)

  const rows = subs.data?.subcontractors ?? []

  return (
    <div className="h-full overflow-y-auto p-4">
      <div className="mx-auto flex max-w-4xl flex-col gap-4">
        <div className="flex items-start justify-between gap-2">
          <div>
            <h1 className="flex items-center gap-2 text-lg font-semibold">
              <Building2 className="size-5" />
              Subcontractors
            </h1>
            <p className="text-muted-foreground text-sm">
              External firms the utility tasks field work to. They don't operate in this system — tasking records who is
              responsible; your coordinator tracks the work order's status.
            </p>
          </div>
          <Button size="sm" onClick={() => setCreating(true)}>
            <Plus className="size-4" />
            New subcontractor
          </Button>
        </div>

        <Card>
          <CardHeader className="pb-2">
            <CardTitle className="text-sm">Registry</CardTitle>
            <CardDescription>
              {rows.length} firm{rows.length === 1 ? "" : "s"}
            </CardDescription>
          </CardHeader>
          <CardContent className="pt-0">
            {subs.isPending ? (
              <div className="text-muted-foreground flex items-center gap-2 text-sm">
                <Loader2 className="size-4 animate-spin" />
                Loading…
              </div>
            ) : rows.length === 0 ? (
              <p className="text-muted-foreground text-sm">No subcontractors yet. Add one with “New subcontractor”.</p>
            ) : (
              <div className="overflow-hidden rounded-md border">
                <table className="w-full text-sm">
                  <thead className="bg-muted/50 text-muted-foreground text-xs">
                    <tr className="[&>th]:px-2 [&>th]:py-1.5 [&>th]:text-left [&>th]:font-medium">
                      <th>Firm</th>
                      <th>Contact</th>
                      <th>Phone</th>
                      <th>Email</th>
                      <th>Open orders</th>
                    </tr>
                  </thead>
                  <tbody>
                    {rows.map((s) => (
                      <tr
                        key={s.id}
                        className={cn(
                          "border-border/60 hover:bg-muted/40 cursor-pointer border-t [&>td]:px-2 [&>td]:py-1.5",
                          !s.active && "opacity-60",
                        )}
                        onClick={() => setEditing(s)}
                      >
                        <td className="font-medium">
                          {s.name}
                          {!s.active && <span className="text-muted-foreground ml-1 text-xs">(retired)</span>}
                        </td>
                        <td className="text-muted-foreground">{s.contact_name ?? "—"}</td>
                        <td className="text-muted-foreground">{s.phone ?? "—"}</td>
                        <td className="text-muted-foreground">{s.email ?? "—"}</td>
                        <td className="text-muted-foreground tabular-nums">{s.open_work_order_count}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </CardContent>
        </Card>
      </div>

      {creating && <SubcontractorDialog onClose={() => setCreating(false)} />}
      {editing && <SubcontractorDialog subcontractor={editing} onClose={() => setEditing(null)} />}
    </div>
  )
}

/** Create (no subcontractor) or edit (with one) a subcontractor. */
function SubcontractorDialog({ subcontractor, onClose }: { subcontractor?: Subcontractor; onClose: () => void }) {
  const create = useCreateSubcontractor()
  const update = useUpdateSubcontractor()
  const setActive = useSetSubcontractorActive()
  const editing = subcontractor != null

  const [name, setName] = useState(subcontractor?.name ?? "")
  const [contact, setContact] = useState(subcontractor?.contact_name ?? "")
  const [phone, setPhone] = useState(subcontractor?.phone ?? "")
  const [email, setEmail] = useState(subcontractor?.email ?? "")
  const [notes, setNotes] = useState(subcontractor?.notes ?? "")

  useEffect(() => {
    setName(subcontractor?.name ?? "")
    setContact(subcontractor?.contact_name ?? "")
    setPhone(subcontractor?.phone ?? "")
    setEmail(subcontractor?.email ?? "")
    setNotes(subcontractor?.notes ?? "")
  }, [subcontractor])

  const busy = create.isPending || update.isPending || setActive.isPending
  const canSave = name.trim().length > 0 && !busy

  function save() {
    const body = {
      name: name.trim(),
      contact_name: contact.trim() || undefined,
      phone: phone.trim() || undefined,
      email: email.trim() || undefined,
      notes: notes.trim() || undefined,
    }
    if (editing) {
      update.mutate(
        { id: subcontractor.id, body },
        { onSuccess: onClose, onError: (err) => toastErr(err, "Could not save the subcontractor.") },
      )
    } else {
      create.mutate(body, { onSuccess: onClose, onError: (err) => toastErr(err, "Could not create the subcontractor.") })
    }
  }

  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-h-[85vh] overflow-y-auto">
        <DialogHeader>
          <DialogTitle>{editing ? subcontractor.name : "New subcontractor"}</DialogTitle>
          <DialogDescription>An external firm you can task field work to. Name is required.</DialogDescription>
        </DialogHeader>
        <div className="flex flex-col gap-3">
          <div className="flex flex-col gap-1">
            <label className="text-xs font-medium">Name</label>
            <input className={inputClass} value={name} onChange={(e) => setName(e.target.value)} placeholder="Acme Contracting" autoFocus />
          </div>
          <div className="flex flex-wrap gap-3">
            <div className="flex flex-1 flex-col gap-1">
              <label className="text-xs font-medium">Contact name</label>
              <input className={inputClass} value={contact} onChange={(e) => setContact(e.target.value)} placeholder="optional" />
            </div>
            <div className="flex flex-1 flex-col gap-1">
              <label className="text-xs font-medium">Phone</label>
              <input className={inputClass} value={phone} onChange={(e) => setPhone(e.target.value)} placeholder="optional" />
            </div>
          </div>
          <div className="flex flex-col gap-1">
            <label className="text-xs font-medium">Email</label>
            <input className={inputClass} value={email} onChange={(e) => setEmail(e.target.value)} placeholder="optional" />
          </div>
          <div className="flex flex-col gap-1">
            <label className="text-xs font-medium">Notes</label>
            <input className={inputClass} value={notes} onChange={(e) => setNotes(e.target.value)} placeholder="optional" />
          </div>

          {editing && (
            <div className="flex flex-col gap-2 border-t pt-3">
              <span className="text-sm font-medium">Tasked work</span>
              <div className="max-h-64 overflow-y-auto">
                <WorkOrderMiniList
                  scope={{ subcontractorId: subcontractor.id }}
                  empty="No work orders tasked to this firm."
                />
              </div>
            </div>
          )}
        </div>
        <DialogFooter className="sm:justify-between">
          <div>
            {editing && (
              <Button
                variant="ghost"
                size="sm"
                className={subcontractor.active ? "text-destructive" : ""}
                disabled={busy}
                onClick={() =>
                  setActive.mutate(
                    { id: subcontractor.id, active: !subcontractor.active },
                    { onSuccess: onClose, onError: (err) => toastErr(err, "Could not change the firm.") },
                  )
                }
              >
                {subcontractor.active ? "Retire" : "Restore"}
              </Button>
            )}
          </div>
          <div className="flex items-center gap-2">
            <Button variant="ghost" onClick={onClose}>
              Cancel
            </Button>
            <Button disabled={!canSave} onClick={save}>
              {busy ? <Loader2 className="size-4 animate-spin" /> : null}
              {editing ? "Save" : "Add"}
            </Button>
          </div>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
