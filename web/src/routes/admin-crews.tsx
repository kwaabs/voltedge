import { useEffect, useState } from "react"
import { Loader2, Plus, Truck, UserPlus, Users } from "lucide-react"

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
import {
  type Crew,
  useAddCrewMember,
  useCreateCrew,
  useCrewMembers,
  useCrews,
  useDeactivateCrew,
  useRemoveCrewMember,
  useSetCrewAvailability,
  useUpdateCrew,
} from "@/hooks/use-oms"
import { useAssignableUsers } from "@/hooks/use-wam"
import { useTerritories } from "@/hooks/use-territories"
import { ApiError } from "@/lib/api"
import { cn } from "@/lib/utils"
import { useToastStore } from "@/stores/toast-store"

const CREW_STATUS_TONE: Record<string, string> = {
  available: "bg-emerald-100 text-emerald-800 dark:bg-emerald-950 dark:text-emerald-300",
  dispatched: "bg-amber-100 text-amber-800 dark:bg-amber-950 dark:text-amber-300",
  on_site: "bg-indigo-100 text-indigo-800 dark:bg-indigo-950 dark:text-indigo-300",
  unavailable: "bg-muted text-muted-foreground",
}

function toastErr(err: unknown, fallback: string) {
  useToastStore.getState().pushError(err instanceof ApiError ? err.message : fallback)
}

function StatusChip({ status }: { status: string }) {
  return (
    <span
      className={cn(
        "rounded px-1.5 py-0.5 text-[11px] font-medium capitalize",
        CREW_STATUS_TONE[status] ?? "bg-muted text-muted-foreground",
      )}
    >
      {status.replace("_", " ")}
    </span>
  )
}

/** The crew roster: a compact table of crews the outage board dispatches. Create
 * is a dialog; clicking a row opens the crew's detail dialog (edit, availability,
 * retire, and membership). Gated at the route by oms:write. */
export function AdminCrewsPage() {
  const crews = useCrews({ includeInactive: true })

  const [creating, setCreating] = useState(false)
  const [selected, setSelected] = useState<Crew | null>(null)

  const rows = crews.data?.crews ?? []

  // Keep the open detail dialog in step with refetched data (status/member_count
  // change as the operator acts) by re-resolving the selected crew from the list.
  const selectedLive = selected ? (rows.find((c) => c.id === selected.id) ?? selected) : null

  return (
    <div className="h-full overflow-y-auto p-4">
      <div className="mx-auto flex max-w-4xl flex-col gap-4">
        <div className="flex items-start justify-between gap-2">
          <div>
            <h1 className="flex items-center gap-2 text-lg font-semibold">
              <Truck className="size-5" />
              Crews
            </h1>
            <p className="text-muted-foreground text-sm">
              Field crews the outage board dispatches. Click a crew to edit it, manage its members, or set it off shift.
            </p>
          </div>
          <Button size="sm" onClick={() => setCreating(true)}>
            <Plus className="size-4" />
            New crew
          </Button>
        </div>

        <Card>
          <CardHeader className="pb-2">
            <CardTitle className="text-sm">Roster</CardTitle>
            <CardDescription>
              {rows.length} crew{rows.length === 1 ? "" : "s"}
            </CardDescription>
          </CardHeader>
          <CardContent className="pt-0">
            {crews.isPending ? (
              <div className="text-muted-foreground flex items-center gap-2 text-sm">
                <Loader2 className="size-4 animate-spin" />
                Loading…
              </div>
            ) : rows.length === 0 ? (
              <p className="text-muted-foreground text-sm">No crews yet. Add one with “New crew”.</p>
            ) : (
              <div className="overflow-hidden rounded-md border">
                <table className="w-full text-sm">
                  <thead className="bg-muted/50 text-muted-foreground text-xs">
                    <tr className="[&>th]:px-2 [&>th]:py-1.5 [&>th]:text-left [&>th]:font-medium">
                      <th>Crew</th>
                      <th>Kind</th>
                      <th>Status</th>
                      <th>Members</th>
                      <th>Territory</th>
                      <th>Phone</th>
                    </tr>
                  </thead>
                  <tbody>
                    {rows.map((c) => (
                      <tr
                        key={c.id}
                        className={cn(
                          "border-border/60 hover:bg-muted/40 cursor-pointer border-t [&>td]:px-2 [&>td]:py-1.5",
                          !c.active && "opacity-60",
                        )}
                        onClick={() => setSelected(c)}
                      >
                        <td className="font-medium">
                          {c.name}
                          {!c.active && <span className="text-muted-foreground ml-1 text-xs">(retired)</span>}
                        </td>
                        <td className="text-muted-foreground">{c.kind ?? "—"}</td>
                        <td>
                          <StatusChip status={c.status} />
                        </td>
                        <td className="text-muted-foreground">
                          <span className="inline-flex items-center gap-1">
                            <Users className="size-3.5" />
                            {c.member_count}
                          </span>
                        </td>
                        <td className="text-muted-foreground">{c.territory_name ?? "—"}</td>
                        <td className="text-muted-foreground">{c.phone ?? "—"}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </CardContent>
        </Card>
      </div>

      {creating && <CreateCrewDialog onClose={() => setCreating(false)} />}
      {selectedLive && <CrewDetailDialog crew={selectedLive} onClose={() => setSelected(null)} />}
    </div>
  )
}

/** Create a crew in a dialog. Name is required; the rest are optional. */
function CreateCrewDialog({ onClose }: { onClose: () => void }) {
  const create = useCreateCrew()
  const territories = useTerritories()
  const territoryRows = territories.data?.territories ?? []

  const [name, setName] = useState("")
  const [kind, setKind] = useState("")
  const [phone, setPhone] = useState("")
  const [territoryId, setTerritoryId] = useState("")

  const canCreate = name.trim().length > 0 && !create.isPending

  function addCrew() {
    create.mutate(
      {
        name: name.trim(),
        kind: kind.trim() || undefined,
        phone: phone.trim() || undefined,
        territory_id: territoryId || undefined,
      },
      {
        onSuccess: onClose,
        onError: (err) => toastErr(err, "Could not create the crew."),
      },
    )
  }

  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>New crew</DialogTitle>
          <DialogDescription>Name is required; the rest are optional.</DialogDescription>
        </DialogHeader>
        <div className="flex flex-col gap-3">
          <div className="flex flex-col gap-1">
            <label className="text-xs font-medium">Name</label>
            <input className={inputClass} value={name} onChange={(e) => setName(e.target.value)} placeholder="Team A" autoFocus />
          </div>
          <div className="flex flex-wrap gap-3">
            <div className="flex flex-1 flex-col gap-1">
              <label className="text-xs font-medium">Kind</label>
              <input className={inputClass} value={kind} onChange={(e) => setKind(e.target.value)} placeholder="line / cable…" />
            </div>
            <div className="flex flex-1 flex-col gap-1">
              <label className="text-xs font-medium">Phone</label>
              <input className={inputClass} value={phone} onChange={(e) => setPhone(e.target.value)} placeholder="optional" />
            </div>
          </div>
          <div className="flex flex-col gap-1">
            <label className="text-xs font-medium">Home territory</label>
            <select className={inputClass} value={territoryId} onChange={(e) => setTerritoryId(e.target.value)}>
              <option value="">—</option>
              {territoryRows.map((t) => (
                <option key={t.id} value={t.id}>
                  {t.name} ({t.level_code})
                </option>
              ))}
            </select>
          </div>
        </div>
        <DialogFooter>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button disabled={!canCreate} onClick={addCrew}>
            {create.isPending ? <Loader2 className="size-4 animate-spin" /> : <Plus className="size-4" />}
            Add crew
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

/** A crew's detail dialog: edit its descriptive fields, toggle availability,
 * retire it, and manage its members. */
function CrewDetailDialog({ crew, onClose }: { crew: Crew; onClose: () => void }) {
  const update = useUpdateCrew()
  const setAvail = useSetCrewAvailability()
  const deactivate = useDeactivateCrew()
  const territories = useTerritories()
  const territoryRows = territories.data?.territories ?? []

  const [name, setName] = useState(crew.name)
  const [kind, setKind] = useState(crew.kind ?? "")
  const [phone, setPhone] = useState(crew.phone ?? "")
  const [territoryId, setTerritoryId] = useState(crew.territory_id ?? "")

  // Re-seed the form if a different crew is opened (same dialog instance reused).
  useEffect(() => {
    setName(crew.name)
    setKind(crew.kind ?? "")
    setPhone(crew.phone ?? "")
    setTerritoryId(crew.territory_id ?? "")
  }, [crew.id, crew.name, crew.kind, crew.phone, crew.territory_id])

  const dirty =
    name.trim() !== crew.name ||
    (kind.trim() || "") !== (crew.kind ?? "") ||
    (phone.trim() || "") !== (crew.phone ?? "") ||
    (territoryId || "") !== (crew.territory_id ?? "")
  const canSave = name.trim().length > 0 && dirty && !update.isPending

  function save() {
    update.mutate(
      {
        id: crew.id,
        body: {
          name: name.trim(),
          kind: kind.trim() || undefined,
          phone: phone.trim() || undefined,
          territory_id: territoryId || undefined,
        },
      },
      { onError: (err) => toastErr(err, "Could not save the crew.") },
    )
  }

  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-h-[85vh] max-w-2xl overflow-y-auto">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            {crew.name}
            <StatusChip status={crew.status} />
            {!crew.active && <span className="text-muted-foreground text-xs font-normal">(retired)</span>}
          </DialogTitle>
          <DialogDescription>Edit the crew, manage its members, or change its availability.</DialogDescription>
        </DialogHeader>

        <div className="flex flex-col gap-4">
          {/* Descriptive fields */}
          <fieldset disabled={!crew.active} className="flex flex-col gap-3 disabled:opacity-60">
            <div className="flex flex-col gap-1">
              <label className="text-xs font-medium">Name</label>
              <input className={inputClass} value={name} onChange={(e) => setName(e.target.value)} />
            </div>
            <div className="flex flex-wrap gap-3">
              <div className="flex flex-1 flex-col gap-1">
                <label className="text-xs font-medium">Kind</label>
                <input className={inputClass} value={kind} onChange={(e) => setKind(e.target.value)} placeholder="line / cable…" />
              </div>
              <div className="flex flex-1 flex-col gap-1">
                <label className="text-xs font-medium">Phone</label>
                <input className={inputClass} value={phone} onChange={(e) => setPhone(e.target.value)} placeholder="optional" />
              </div>
            </div>
            <div className="flex flex-col gap-1">
              <label className="text-xs font-medium">Home territory</label>
              <select className={inputClass} value={territoryId} onChange={(e) => setTerritoryId(e.target.value)}>
                <option value="">—</option>
                {territoryRows.map((t) => (
                  <option key={t.id} value={t.id}>
                    {t.name} ({t.level_code})
                  </option>
                ))}
              </select>
            </div>
            <div>
              <Button size="sm" disabled={!canSave} onClick={save}>
                {update.isPending ? <Loader2 className="size-4 animate-spin" /> : null}
                Save changes
              </Button>
            </div>
          </fieldset>

          {/* Members */}
          <div className="border-t pt-3">
            <CrewMembersSection crew={crew} />
          </div>
        </div>

        <DialogFooter className="border-t pt-3 sm:justify-between">
          <div className="flex items-center gap-1">
            {crew.active && (
              <>
                <Button
                  variant="ghost"
                  size="sm"
                  disabled={setAvail.isPending}
                  onClick={() =>
                    setAvail.mutate(
                      { id: crew.id, available: crew.status !== "available" },
                      { onError: (err) => toastErr(err, "Could not change availability.") },
                    )
                  }
                >
                  {crew.status === "available" ? "Set off shift" : "Set available"}
                </Button>
                <Button
                  variant="ghost"
                  size="sm"
                  className="text-destructive"
                  disabled={deactivate.isPending}
                  onClick={() =>
                    deactivate.mutate(
                      { id: crew.id },
                      {
                        onSuccess: onClose,
                        onError: (err) => toastErr(err, "Could not retire the crew."),
                      },
                    )
                  }
                >
                  Retire
                </Button>
              </>
            )}
          </div>
          <Button variant="outline" onClick={onClose}>
            Close
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

/** Edit a crew's membership: who is on the team. The member picker lists active
 * users who can act on work (wam:write) — the same pool the work-order assignee
 * picker uses — so a crew's members can be handed field work. */
function CrewMembersSection({ crew }: { crew: Crew }) {
  const members = useCrewMembers(crew.id)
  const assignable = useAssignableUsers()
  const add = useAddCrewMember()
  const remove = useRemoveCrewMember()

  const [userId, setUserId] = useState("")
  const [role, setRole] = useState("member")

  const memberRows = members.data?.members ?? []
  const memberIds = new Set(memberRows.map((m) => m.user_id))
  const candidates = (assignable.data?.users ?? []).filter((u) => !memberIds.has(u.id))
  const canAdd = userId.trim().length > 0 && !add.isPending

  function addMember() {
    add.mutate(
      { crewId: crew.id, user_id: userId, role: role || undefined },
      {
        onSuccess: () => {
          setUserId("")
          setRole("member")
        },
        onError: (err) => toastErr(err, "Could not add the member."),
      },
    )
  }

  function removeMember(memberUserId: string) {
    remove.mutate(
      { crewId: crew.id, userId: memberUserId },
      { onError: (err) => toastErr(err, "Could not remove the member.") },
    )
  }

  return (
    <div className="flex flex-col gap-3">
      <div className="flex items-center gap-2 text-sm font-medium">
        <Users className="size-4" />
        Members
        <span className="text-muted-foreground font-normal">
          — the users on this crew; they receive its field work
        </span>
      </div>

      {crew.active && (
        <div className="flex flex-wrap items-end gap-2">
          <div className="flex flex-col gap-1">
            <label className="text-xs font-medium">Add member</label>
            <select className={cn(inputClass, "w-56")} value={userId} onChange={(e) => setUserId(e.target.value)}>
              <option value="">Select a user…</option>
              {candidates.map((u) => (
                <option key={u.id} value={u.id}>
                  {u.display_name} ({u.email})
                </option>
              ))}
            </select>
          </div>
          <div className="flex flex-col gap-1">
            <label className="text-xs font-medium">Role</label>
            <select className={cn(inputClass, "w-32")} value={role} onChange={(e) => setRole(e.target.value)}>
              <option value="member">Member</option>
              <option value="lead">Lead</option>
            </select>
          </div>
          <Button size="sm" disabled={!canAdd} onClick={addMember}>
            {add.isPending ? <Loader2 className="size-4 animate-spin" /> : <UserPlus className="size-4" />}
            Add
          </Button>
        </div>
      )}

      {members.isPending ? (
        <div className="text-muted-foreground flex items-center gap-2 text-sm">
          <Loader2 className="size-4 animate-spin" />
          Loading…
        </div>
      ) : memberRows.length === 0 ? (
        <p className="text-muted-foreground text-sm">No members yet.</p>
      ) : (
        <div className="overflow-hidden rounded-md border">
          <table className="w-full text-sm">
            <thead className="bg-muted/50 text-muted-foreground text-xs">
              <tr className="[&>th]:px-2 [&>th]:py-1.5 [&>th]:text-left [&>th]:font-medium">
                <th>Name</th>
                <th>Email</th>
                <th>Role</th>
                <th></th>
              </tr>
            </thead>
            <tbody>
              {memberRows.map((m) => (
                <tr key={m.id} className="border-border/60 border-t [&>td]:px-2 [&>td]:py-1.5">
                  <td className="font-medium">{m.display_name ?? m.user_id}</td>
                  <td className="text-muted-foreground">{m.email ?? "—"}</td>
                  <td className="text-muted-foreground capitalize">{m.role ?? "—"}</td>
                  <td className="text-right">
                    {crew.active && (
                      <Button
                        variant="ghost"
                        size="sm"
                        className="text-destructive"
                        disabled={remove.isPending}
                        onClick={() => removeMember(m.user_id)}
                      >
                        Remove
                      </Button>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}
