import { useMemo, useState } from "react"
import { useNavigate } from "react-router"
import { FolderInput, Loader2, Map as MapIcon, Pencil, Plus, Trash2 } from "lucide-react"

import { ConfirmDeleteDialog } from "@/components/admin/confirm-delete-dialog"
import { FormDialog } from "@/components/admin/form-dialog"
import { inputClass } from "@/components/map/field-utils"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import {
  type TerritoryLevel,
  type TerritorySummary,
  useCreateTerritory,
  useDeleteTerritory,
  useTerritories,
  useTerritoryLevels,
  useUpdateTerritory,
} from "@/hooks/use-territories"
import { ApiError } from "@/lib/api"
import { useTerritoryEditStore } from "@/stores/territory-edit-store"
import { useToastStore } from "@/stores/toast-store"

const labelClass = "text-xs font-medium"

function toastError(err: unknown, fallback: string) {
  useToastStore.getState().pushError(err instanceof ApiError ? err.message : fallback)
}

/** Groups territories by parent_id (null = roots) so the flat list renders
 * as a tree. Children keep the list's ordering (level rank then name). */
function childrenByParent(territories: TerritorySummary[]): Map<string | null, TerritorySummary[]> {
  const byParent = new Map<string | null, TerritorySummary[]>()
  for (const t of territories) {
    const key = t.parent_id ?? null
    const bucket = byParent.get(key)
    if (bucket) bucket.push(t)
    else byParent.set(key, [t])
  }
  return byParent
}

/**
 * Create form. The level is *derived* from the chosen parent -- a child sits
 * exactly one rank below its parent (rank is unique per level), and a root
 * is rank 0 -- so there's no separate level picker to get wrong. A parent at
 * the deepest level can't take a child, and the form says so and blocks save.
 */
function CreateTerritoryDialog({
  initialParentId,
  territories,
  levels,
  onClose,
}: {
  initialParentId: string | null
  territories: TerritorySummary[]
  levels: TerritoryLevel[]
  onClose: () => void
}) {
  const [parentId, setParentId] = useState<string | null>(initialParentId)
  const [name, setName] = useState("")
  const create = useCreateTerritory()

  const parent = parentId ? territories.find((t) => t.id === parentId) : null
  const parentRank = parent ? parent.level_rank : -1
  const childLevel = levels.find((l) => l.rank === parentRank + 1)
  const canSave = name.trim().length > 0 && Boolean(childLevel) && !create.isPending

  function handleSave() {
    if (!childLevel) return
    create.mutate(
      { parent_id: parentId, level_id: childLevel.id, name: name.trim() },
      {
        onSuccess: onClose,
        onError: (err) => toastError(err, "Could not create the territory."),
      },
    )
  }

  return (
    <FormDialog
      open
      onOpenChange={(next) => next || onClose()}
      title="New territory"
      description={
        childLevel
          ? `Adds a ${childLevel.name} under ${parent ? parent.name : "the top level (a new root)"}.`
          : "This parent is at the deepest level, so it can't have children."
      }
      onSubmit={handleSave}
      submitLabel="Create"
      canSubmit={canSave}
      saving={create.isPending}
    >
      <div className="flex flex-col gap-1">
        <label className={labelClass}>Parent</label>
        <select
          className={inputClass}
          value={parentId ?? ""}
          onChange={(e) => setParentId(e.target.value || null)}
        >
          <option value="">— none (top-level root) —</option>
          {territories.map((t) => (
            <option key={t.id} value={t.id}>
              {t.name} ({t.level_name})
            </option>
          ))}
        </select>
      </div>

      <div className="flex flex-col gap-1">
        <label className={labelClass}>Level</label>
        <input
          className={inputClass}
          value={childLevel ? childLevel.name : "—"}
          disabled
          readOnly
        />
        <span className="text-muted-foreground text-xs">Determined by the parent -- one level below it.</span>
      </div>

      <div className="flex flex-col gap-1">
        <label className={labelClass}>
          Name <span className="text-destructive">*</span>
        </label>
        <input
          className={inputClass}
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder="e.g. Northern District"
        />
      </div>
    </FormDialog>
  )
}

/** Rename form -- name only. */
function RenameTerritoryDialog({ node, onClose }: { node: TerritorySummary; onClose: () => void }) {
  const [name, setName] = useState(node.name)
  const update = useUpdateTerritory()
  const canSave = name.trim().length > 0 && name.trim() !== node.name && !update.isPending

  function handleSave() {
    update.mutate(
      { id: node.id, body: { name: name.trim() } },
      {
        onSuccess: onClose,
        onError: (err) => toastError(err, "Could not rename the territory."),
      },
    )
  }

  return (
    <FormDialog
      open
      onOpenChange={(next) => next || onClose()}
      title="Rename territory"
      description={`Currently "${node.name}" (${node.level_name}).`}
      onSubmit={handleSave}
      submitLabel="Save"
      canSubmit={canSave}
      saving={update.isPending}
    >
      <div className="flex flex-col gap-1">
        <label className={labelClass}>Name</label>
        <input className={inputClass} value={name} onChange={(e) => setName(e.target.value)} autoFocus />
      </div>
    </FormDialog>
  )
}

/**
 * Re-parent form. A node's level is kept, so the only legal new parents are
 * those exactly one rank above it -- which, being shallower, can never be the
 * node's own descendants, so this list can't offer a cycle (the server
 * rejects one anyway). The moved node's boundary must still fall within the
 * new parent's; a boundary that doesn't is rejected with a 400 toast.
 */
function ReparentTerritoryDialog({
  node,
  territories,
  onClose,
}: {
  node: TerritorySummary
  territories: TerritorySummary[]
  onClose: () => void
}) {
  const update = useUpdateTerritory()
  const candidates = territories.filter(
    (t) => t.level_rank === node.level_rank - 1 && t.id !== node.parent_id,
  )
  const [target, setTarget] = useState("")
  const canSave = target.length > 0 && !update.isPending

  function handleSave() {
    update.mutate(
      { id: node.id, body: { reparent: true, parent_id: target } },
      {
        onSuccess: onClose,
        onError: (err) => toastError(err, "Could not move the territory."),
      },
    )
  }

  const currentParent = node.parent_id ? territories.find((t) => t.id === node.parent_id) : null

  return (
    <FormDialog
      open
      onOpenChange={(next) => next || onClose()}
      title={`Move "${node.name}"`}
      description={`Currently under ${currentParent ? currentParent.name : "no parent (a root)"}. Choose a new parent one level above.`}
      onSubmit={handleSave}
      submitLabel="Move"
      canSubmit={canSave}
      saving={update.isPending}
    >
      {candidates.length === 0 ? (
        <p className="text-muted-foreground text-sm">
          No eligible new parent -- there's no other territory one level above this one to move it under.
        </p>
      ) : (
        <div className="flex flex-col gap-1">
          <label className={labelClass}>New parent</label>
          <select className={inputClass} value={target} onChange={(e) => setTarget(e.target.value)}>
            <option value="">select a parent...</option>
            {candidates.map((t) => (
              <option key={t.id} value={t.id}>
                {t.name} ({t.level_name})
              </option>
            ))}
          </select>
        </div>
      )}
    </FormDialog>
  )
}

/** One row of the tree, indented by depth, with its actions. Children are
 * rendered by the caller (recursion lives in TerritoryTree). */
function TerritoryRow({
  node,
  depth,
  onAddChild,
  onRename,
  onReparent,
  onEditBoundary,
  onDelete,
}: {
  node: TerritorySummary
  depth: number
  onAddChild: (node: TerritorySummary) => void
  onRename: (node: TerritorySummary) => void
  onReparent: (node: TerritorySummary) => void
  onEditBoundary: (node: TerritorySummary) => void
  onDelete: (node: TerritorySummary) => void
}) {
  return (
    <div
      className="hover:bg-muted/50 group flex items-center gap-2 rounded-md py-1.5 pr-1"
      style={{ paddingLeft: depth * 20 + 8 }}
    >
      <span className="truncate text-sm font-medium">{node.name}</span>
      <span className="bg-muted text-muted-foreground rounded px-1.5 py-0.5 text-xs">{node.level_name}</span>
      <div className="ml-auto flex shrink-0 items-center gap-0.5 opacity-0 transition-opacity group-hover:opacity-100 focus-within:opacity-100">
        <Button variant="ghost" size="icon" className="size-7" title="Add child" onClick={() => onAddChild(node)}>
          <Plus className="size-4" />
        </Button>
        <Button variant="ghost" size="icon" className="size-7" title="Rename" onClick={() => onRename(node)}>
          <Pencil className="size-4" />
        </Button>
        <Button variant="ghost" size="icon" className="size-7" title="Move to a different parent" onClick={() => onReparent(node)}>
          <FolderInput className="size-4" />
        </Button>
        <Button
          variant="ghost"
          size="icon"
          className="size-7"
          title="Edit boundary on the map"
          onClick={() => onEditBoundary(node)}
        >
          <MapIcon className="size-4" />
        </Button>
        <Button
          variant="ghost"
          size="icon"
          className="text-destructive size-7"
          title="Delete"
          onClick={() => onDelete(node)}
        >
          <Trash2 className="size-4" />
        </Button>
      </div>
    </div>
  )
}

export function AdminTerritoriesPage() {
  const navigate = useNavigate()
  const { data, isPending, isError } = useTerritories()
  const { data: levelsData } = useTerritoryLevels()

  // "Edit boundary" hands off to the map: arm the authoring session, then
  // navigate to the map surface where TerritoryEditToolbar + voltedge-map
  // take over (Phase 11 Slice I). Boundary drawing needs the map's own
  // canvas, so it can't live on this list page.
  function handleEditBoundary(node: TerritorySummary) {
    useTerritoryEditStore.getState().start(node.id, node.name)
    navigate("/")
  }

  const [creating, setCreating] = useState<{ parentId: string | null } | null>(null)
  const [renaming, setRenaming] = useState<TerritorySummary | null>(null)
  const [reparenting, setReparenting] = useState<TerritorySummary | null>(null)
  const [deleting, setDeleting] = useState<TerritorySummary | null>(null)

  const territories = useMemo(() => data?.territories ?? [], [data])
  const levels = levelsData?.levels ?? []
  const byParent = useMemo(() => childrenByParent(territories), [territories])
  const del = useDeleteTerritory()

  // Depth-first flatten so the recursive tree renders as a simple ordered
  // list of rows, each carrying its own indentation depth.
  const rows = useMemo(() => {
    const out: { node: TerritorySummary; depth: number }[] = []
    const walk = (parentId: string | null, depth: number) => {
      for (const node of byParent.get(parentId) ?? []) {
        out.push({ node, depth })
        walk(node.id, depth + 1)
      }
    }
    walk(null, 0)
    return out
  }, [byParent])

  return (
    <div className="h-full overflow-y-auto p-4">
      <div className="mx-auto flex max-w-3xl flex-col gap-4">
        <div className="flex items-center justify-between">
          <div>
            <h1 className="text-lg font-semibold">Territories</h1>
            <p className="text-muted-foreground text-sm">
              The administrative-area hierarchy features are classified against. Editing the tree here never re-assigns
              already-placed features -- that stays automatic on each feature save.
            </p>
          </div>
          <Button size="sm" onClick={() => setCreating({ parentId: null })}>
            <Plus className="size-4" />
            New territory
          </Button>
        </div>

        <Card>
          <CardHeader className="pb-2">
            <CardTitle className="text-sm">Hierarchy</CardTitle>
            <CardDescription>Hover a row for its actions: add a child, rename, move, or delete.</CardDescription>
          </CardHeader>
          <CardContent className="pt-0">
            {isPending ? (
              <div className="text-muted-foreground flex items-center gap-2 text-sm">
                <Loader2 className="size-4 animate-spin" />
                Loading territories...
              </div>
            ) : isError ? (
              <p className="text-destructive text-sm">Could not load territories.</p>
            ) : rows.length === 0 ? (
              <p className="text-muted-foreground text-sm">No territories yet. Create a top-level root to start.</p>
            ) : (
              <div className="flex flex-col">
                {rows.map(({ node, depth }) => (
                  <TerritoryRow
                    key={node.id}
                    node={node}
                    depth={depth}
                    onAddChild={(n) => setCreating({ parentId: n.id })}
                    onRename={setRenaming}
                    onReparent={setReparenting}
                    onEditBoundary={handleEditBoundary}
                    onDelete={setDeleting}
                  />
                ))}
              </div>
            )}
          </CardContent>
        </Card>
      </div>

      {creating !== null && (
        <CreateTerritoryDialog
          key={creating.parentId ?? "root"}
          initialParentId={creating.parentId}
          territories={territories}
          levels={levels}
          onClose={() => setCreating(null)}
        />
      )}
      {renaming && <RenameTerritoryDialog key={renaming.id} node={renaming} onClose={() => setRenaming(null)} />}
      {reparenting && (
        <ReparentTerritoryDialog
          key={reparenting.id}
          node={reparenting}
          territories={territories}
          onClose={() => setReparenting(null)}
        />
      )}

      <ConfirmDeleteDialog
        open={deleting !== null}
        title={`Delete ${deleting?.name ?? ""}?`}
        description="This can't be undone. A territory with children can't be deleted -- move or delete them first. Features already classified against it keep their stored territory_id."
        confirmLabel="Delete"
        onOpenChange={(open) => !open && setDeleting(null)}
        onConfirm={async () => {
          if (!deleting) return
          await del.mutateAsync({ id: deleting.id })
        }}
      />
    </div>
  )
}
