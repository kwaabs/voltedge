import { useState } from "react"
import { Check, LandPlot, Pencil, X } from "lucide-react"

import { Button } from "@/components/ui/button"
import { usePermission } from "@/hooks/use-auth"
import { useEditModeStore } from "@/stores/edit-mode-store"
import { ApiError } from "@/lib/api"
import { useTerritories, useUpdateTerritory } from "@/hooks/use-territories"
import { useTerritoryEditStore } from "@/stores/territory-edit-store"
import {
  type TerritoryOverlaySelection,
  useTerritoryOverlayStore,
} from "@/stores/territory-overlay-store"
import { useToastStore } from "@/stores/toast-store"

/**
 * The inspector for a territory clicked on the boundary overlay (Phase 11
 * Slice H). Read-only by default -- it answers "what area did I click, and
 * what's under it" -- but an editor (territories:write) can flip it into an
 * inline edit mode to rename and re-parent the territory in place, the same
 * attribute edits the /admin/territories dialogs offer (useUpdateTerritory);
 * boundary geometry is still authored through the "Edit boundary" toolbar.
 * The territory's level is fixed at creation and cannot be changed here (the
 * PATCH endpoint has no level field), matching the admin page. Anchored
 * bottom-left so it never collides with the feature panel's bottom-right
 * corner. Read-only fields come from the boundary feature's own GeoJSON
 * properties (the store's `selected`) plus the territory list for the
 * parent's name and the immediate children -- no per-territory fetch.
 */
export function TerritoryPanel() {
  const selected = useTerritoryOverlayStore((s) => s.selected)
  const clearSelected = useTerritoryOverlayStore((s) => s.clearSelected)
  const selectTerritory = useTerritoryOverlayStore((s) => s.selectTerritory)
  const { data } = useTerritories()
  const can = usePermission()
  const update = useUpdateTerritory()
  const [editing, setEditing] = useState(false)

  if (!selected) return null

  // Only an editor (territories:write) gets the authoring entries (Phase 11
  // Slice I); the API enforces it regardless. Starting the boundary edit
  // closes this panel -- the authoring toolbar takes over.
  // Territory editing (details + boundary) is a write -- shown only in edit mode
  // (S4). Browsing territories stays open in view mode. Hook called
  // unconditionally, then combined (rules of hooks).
  const territoryEditMode = useEditModeStore((s) => s.editMode)
  const canEdit = can("territories:write") && territoryEditMode
  const startBoundaryEdit = () => {
    useTerritoryEditStore.getState().start(selected.id, selected.name)
    clearSelected()
  }

  const territories = data?.territories ?? []
  const parent = selected.parent_id
    ? territories.find((t) => t.id === selected.parent_id)
    : undefined
  // Immediate children only -- the panel maps one level down, not the
  // whole subtree; the overlay itself already shows the deeper nesting.
  const children = territories
    .filter((t) => t.parent_id === selected.id)
    .sort((a, b) => a.name.localeCompare(b.name))

  return (
    <div className="bg-popover text-popover-foreground fixed bottom-10 left-4 z-40 flex w-72 flex-col overflow-hidden rounded-lg border border-border/70 border-t-2 border-t-[oklch(0.45_0.10_252)] dark:border-t-primary shadow-xl ring-1 ring-black/5">
      <div className="border-[oklch(0.45_0.10_252)]/20 dark:border-primary/15 flex items-center gap-2 border-b bg-gradient-to-b from-[oklch(0.45_0.10_252)]/15 to-[oklch(0.45_0.10_252)]/5 dark:from-accent/50 dark:to-accent/20 px-3 py-2">
        <LandPlot className="text-primary size-4 shrink-0" />
        <div className="min-w-0 flex-1">
          <div className="truncate text-sm font-semibold">{selected.name}</div>
          <div className="text-muted-foreground truncate text-xs">{selected.level_name}</div>
        </div>
        <Button
          variant="ghost"
          size="icon"
          className="size-7 shrink-0"
          title="Close"
          onClick={clearSelected}
        >
          <X className="size-4" />
          <span className="sr-only">Close territory panel</span>
        </Button>
      </div>

      {editing ? (
        <EditDetailsForm
          key={selected.id}
          selected={selected}
          territories={territories}
          saving={update.isPending}
          onCancel={() => setEditing(false)}
          onSave={(body, next) =>
            update.mutate(
              { id: selected.id, body },
              {
                onSuccess: () => {
                  // Keep the still-open panel in sync with the edit -- the
                  // boundary overlay refetch (the mutation invalidates it)
                  // updates the map, but `selected` is a snapshot, so refresh
                  // it here so the header and read-only view reflect the change.
                  selectTerritory({ ...selected, ...next })
                  setEditing(false)
                },
                onError: (err) =>
                  useToastStore
                    .getState()
                    .pushError(err instanceof ApiError ? err.message : "Could not update the territory."),
              },
            )
          }
        />
      ) : (
        <div className="flex flex-col gap-3 px-3 py-2.5 text-xs">
          {parent && (
            <div className="flex items-baseline justify-between gap-2">
              <span className="text-muted-foreground">Parent</span>
              <span className="text-foreground truncate font-medium">{parent.name}</span>
            </div>
          )}

          <div>
            <div className="text-muted-foreground mb-1">
              {children.length === 0
                ? "No child territories"
                : `Children (${children.length})`}
            </div>
            {children.length > 0 && (
              <ul className="flex max-h-40 flex-col gap-0.5 overflow-y-auto">
                {children.map((child) => (
                  <li
                    key={child.id}
                    className="flex items-baseline justify-between gap-2 py-0.5"
                  >
                    <span className="text-foreground truncate">{child.name}</span>
                    <span className="text-muted-foreground shrink-0">{child.level_name}</span>
                  </li>
                ))}
              </ul>
            )}
          </div>

          {canEdit && (
            <div className="flex gap-2">
              <Button variant="outline" size="sm" className="flex-1" onClick={() => setEditing(true)}>
                <Pencil className="size-4" />
                Edit details
              </Button>
              <Button variant="outline" size="sm" className="flex-1" onClick={startBoundaryEdit}>
                <Pencil className="size-4" />
                Edit boundary
              </Button>
            </div>
          )}
        </div>
      )}
    </div>
  )
}

/**
 * Inline rename + re-parent form, mirroring the /admin/territories Rename and
 * Move dialogs (name text; a new-parent picker of the territories exactly one
 * rank above, which -- being shallower -- can never be this node's own
 * descendants, so no cycle is offered). The picker defaults to the current
 * parent, so leaving it unchanged is fine; Save sends only what actually
 * changed. A root territory (no level above it) shows rename only. The server
 * still rejects a boundary that falls outside the new parent's with a 400.
 */
function EditDetailsForm({
  selected,
  territories,
  saving,
  onCancel,
  onSave,
}: {
  selected: TerritoryOverlaySelection
  territories: { id: string; parent_id: string | null; name: string; level_code: string; level_name: string; level_rank: number }[]
  saving: boolean
  onCancel: () => void
  onSave: (
    body: { name?: string; reparent?: boolean; parent_id?: string | null },
    next: { name: string; parent_id: string | null },
  ) => void
}) {
  const [name, setName] = useState(selected.name)
  const [parentId, setParentId] = useState(selected.parent_id ?? "")

  // Legal new parents are the territories exactly one rank above this node.
  // The current parent is one of them, so it stays selectable as the default.
  const candidates = territories
    .filter((t) => t.level_rank === selected.level_rank - 1)
    .sort((a, b) => a.name.localeCompare(b.name))
  const canReparent = candidates.length > 0

  const trimmed = name.trim()
  const nameChanged = trimmed.length > 0 && trimmed !== selected.name
  const parentChanged = canReparent && parentId !== (selected.parent_id ?? "")
  const canSave = !saving && (nameChanged || parentChanged) && trimmed.length > 0

  function handleSave() {
    const body: { name?: string; reparent?: boolean; parent_id?: string | null } = {}
    const next = { name: selected.name, parent_id: selected.parent_id }
    if (nameChanged) {
      body.name = trimmed
      next.name = trimmed
    }
    if (parentChanged) {
      body.reparent = true
      body.parent_id = parentId || null
      next.parent_id = parentId || null
    }
    onSave(body, next)
  }

  return (
    <form
      className="flex flex-col gap-3 px-3 py-2.5 text-xs"
      onSubmit={(e) => {
        e.preventDefault()
        if (canSave) handleSave()
      }}
    >
      <div className="flex flex-col gap-1">
        <label className="text-muted-foreground font-medium">Name</label>
        <input
          className="border-input bg-background h-8 rounded-md border px-2 text-sm outline-none focus-visible:ring-2 focus-visible:ring-ring"
          value={name}
          onChange={(e) => setName(e.target.value)}
          autoFocus
        />
      </div>

      <div className="flex flex-col gap-1">
        <label className="text-muted-foreground font-medium">Parent</label>
        {canReparent ? (
          <select
            className="border-input bg-background h-8 rounded-md border px-2 text-sm outline-none focus-visible:ring-2 focus-visible:ring-ring"
            value={parentId}
            onChange={(e) => setParentId(e.target.value)}
          >
            {candidates.map((t) => (
              <option key={t.id} value={t.id}>
                {t.name} ({t.level_name})
              </option>
            ))}
          </select>
        ) : (
          <p className="text-muted-foreground">
            Top level -- no parent to change.
          </p>
        )}
      </div>

      <div className="flex gap-2">
        <Button type="button" variant="ghost" size="sm" className="flex-1" onClick={onCancel} disabled={saving}>
          Cancel
        </Button>
        <Button type="submit" size="sm" className="flex-1" disabled={!canSave}>
          <Check className="size-4" />
          Save
        </Button>
      </div>
    </form>
  )
}
