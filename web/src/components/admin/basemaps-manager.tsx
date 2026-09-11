import { useState } from "react"
import { Eye, EyeOff, Loader2, Pencil, Plus, Trash2, X } from "lucide-react"

import { ConfirmDeleteDialog } from "@/components/admin/confirm-delete-dialog"
import { FormDialog } from "@/components/admin/form-dialog"
import { inputClass } from "@/components/map/field-utils"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import {
  type AttributionLink,
  type BasemapRecord,
  type CreateBasemapInput,
  type UpdateBasemapInput,
  useAllBasemaps,
  useCreateBasemap,
  useDeleteBasemap,
  useUpdateBasemap,
} from "@/hooks/use-basemaps"
import { ApiError } from "@/lib/api"
import { useToastStore } from "@/stores/toast-store"

const labelClass = "text-xs font-medium"

function toastError(err: unknown, fallback: string) {
  useToastStore.getState().pushError(err instanceof ApiError ? err.message : fallback)
}

/** Parses an optional zoom input: blank -> null (no value/cap), else the
 * number (or null if unparseable, letting the server reject it). */
function parseZoom(raw: string): number | null {
  const t = raw.trim()
  if (t === "") return null
  const n = Number(t)
  return Number.isFinite(n) ? n : null
}

/** A small repeatable label/href editor for a basemap's required attribution.
 * Blank-label rows are dropped on save (see toInput); an empty list is valid
 * (a basemap with no required credit). */
function AttributionEditor({
  links,
  onChange,
}: {
  links: AttributionLink[]
  onChange: (next: AttributionLink[]) => void
}) {
  function setAt(i: number, patch: Partial<AttributionLink>) {
    onChange(links.map((l, idx) => (idx === i ? { ...l, ...patch } : l)))
  }
  return (
    <div className="flex flex-col gap-1">
      <label className={labelClass}>Attribution</label>
      <div className="flex flex-col gap-2">
        {links.map((link, i) => (
          <div key={i} className="flex items-center gap-1.5">
            <input
              className={inputClass}
              value={link.label}
              onChange={(e) => setAt(i, { label: e.target.value })}
              placeholder="© Provider"
            />
            <input
              className={inputClass}
              value={link.href}
              onChange={(e) => setAt(i, { href: e.target.value })}
              placeholder="https://…"
            />
            <Button
              type="button"
              variant="ghost"
              size="icon"
              className="text-muted-foreground size-7 shrink-0"
              title="Remove"
              onClick={() => onChange(links.filter((_, idx) => idx !== i))}
            >
              <X className="size-4" />
            </Button>
          </div>
        ))}
        <div>
          <Button
            type="button"
            variant="outline"
            size="sm"
            onClick={() => onChange([...links, { label: "", href: "" }])}
          >
            <Plus className="size-4" />
            Add credit
          </Button>
        </div>
      </div>
      <span className="text-muted-foreground text-xs">
        Required credit line(s) shown in the map's status bar. Leave empty only if the provider needs none.
      </span>
    </div>
  )
}

/** The shared field block for both create and edit -- everything except the
 * code (immutable, so create owns it). Controlled by the parent. */
interface FormState {
  label: string
  urlTemplate: string
  attribution: AttributionLink[]
  maxZoom: string
  minZoom: string
  tms: boolean
  sortOrder: string
  isEnabled: boolean
}

function FormFields({ state, set }: { state: FormState; set: (patch: Partial<FormState>) => void }) {
  return (
    <>
      <div className="flex flex-col gap-1">
        <label className={labelClass}>
          Label <span className="text-destructive">*</span>
        </label>
        <input
          className={inputClass}
          value={state.label}
          onChange={(e) => set({ label: e.target.value })}
          placeholder="e.g. Satellite (Esri)"
        />
      </div>

      <div className="flex flex-col gap-1">
        <label className={labelClass}>
          Tile URL template <span className="text-destructive">*</span>
        </label>
        <input
          className={inputClass}
          value={state.urlTemplate}
          onChange={(e) => set({ urlTemplate: e.target.value })}
          placeholder="https://…/{z}/{x}/{y}.png"
        />
        <span className="text-muted-foreground text-xs">
          Must contain {"{z}"}, {"{x}"} and {"{y}"}. Order follows the provider (Esri uses {"{z}/{y}/{x}"}).
        </span>
      </div>

      <AttributionEditor links={state.attribution} onChange={(attribution) => set({ attribution })} />

      <div className="grid grid-cols-2 gap-3">
        <div className="flex flex-col gap-1">
          <label className={labelClass}>Max zoom</label>
          <input
            className={inputClass}
            type="number"
            min="0"
            max="24"
            value={state.maxZoom}
            onChange={(e) => set({ maxZoom: e.target.value })}
            placeholder="none"
          />
          <span className="text-muted-foreground text-xs">Blank = no cap.</span>
        </div>
        <div className="flex flex-col gap-1">
          <label className={labelClass}>Min zoom</label>
          <input
            className={inputClass}
            type="number"
            min="0"
            max="24"
            value={state.minZoom}
            onChange={(e) => set({ minZoom: e.target.value })}
            placeholder="0"
          />
        </div>
      </div>

      <div className="grid grid-cols-2 gap-3">
        <div className="flex flex-col gap-1">
          <label className={labelClass}>Sort order</label>
          <input
            className={inputClass}
            type="number"
            value={state.sortOrder}
            onChange={(e) => set({ sortOrder: e.target.value })}
            placeholder="0"
          />
          <span className="text-muted-foreground text-xs">Lower shows first in the picker.</span>
        </div>
        <div className="flex flex-col gap-1">
          <label className={labelClass}>Options</label>
          <label className="flex items-center gap-2 text-sm">
            <input
              type="checkbox"
              checked={state.isEnabled}
              onChange={(e) => set({ isEnabled: e.target.checked })}
            />
            Enabled (shown on the map)
          </label>
          <label className="flex items-center gap-2 text-sm">
            <input type="checkbox" checked={state.tms} onChange={(e) => set({ tms: e.target.checked })} />
            TMS (flipped-Y tiles)
          </label>
        </div>
      </div>
    </>
  )
}

const EMPTY_FORM: FormState = {
  label: "",
  urlTemplate: "",
  attribution: [],
  maxZoom: "",
  minZoom: "0",
  tms: false,
  sortOrder: "0",
  isEnabled: true,
}

/** Turns the string-backed form into the shared numeric/attribution shape both
 * create and update bodies draw from. */
function toShared(state: FormState) {
  return {
    label: state.label.trim(),
    url_template: state.urlTemplate.trim(),
    attribution: state.attribution
      .map((l) => ({ label: l.label.trim(), href: l.href.trim() }))
      .filter((l) => l.label !== ""),
    max_zoom: parseZoom(state.maxZoom),
    min_zoom: parseZoom(state.minZoom) ?? 0,
    tms: state.tms,
    sort_order: Number(state.sortOrder.trim() || "0") || 0,
    is_enabled: state.isEnabled,
  }
}

function formValid(state: FormState): boolean {
  const t = state.urlTemplate
  const hasPlaceholders = t.includes("{z}") && t.includes("{x}") && t.includes("{y}")
  return state.label.trim() !== "" && t.trim() !== "" && hasPlaceholders
}

/** Create dialog -- owns the code (immutable after) plus the shared fields. */
function CreateBasemapDialog({ onClose }: { onClose: () => void }) {
  const [code, setCode] = useState("")
  const [state, setState] = useState<FormState>(EMPTY_FORM)
  const create = useCreateBasemap()
  const set = (patch: Partial<FormState>) => setState((s) => ({ ...s, ...patch }))

  const codeValid = /^[a-z0-9-]+$/.test(code) && code !== "blank"
  const canSave = codeValid && formValid(state) && !create.isPending

  function handleSave() {
    const body: CreateBasemapInput = { code: code.trim(), ...toShared(state) }
    create.mutate(body, {
      onSuccess: onClose,
      onError: (err) => toastError(err, "Could not create the basemap."),
    })
  }

  return (
    <FormDialog
      open
      onOpenChange={(next) => next || onClose()}
      title="New basemap"
      description="Adds a raster tile basemap the map can show."
      onSubmit={handleSave}
      submitLabel="Create"
      canSubmit={canSave}
      saving={create.isPending}
    >
      <div className="flex flex-col gap-1">
        <label className={labelClass}>
          Code <span className="text-destructive">*</span>
        </label>
        <input
          className={inputClass}
          value={code}
          onChange={(e) => setCode(e.target.value)}
          placeholder="e.g. company-tiles"
          autoFocus
        />
        <span className="text-muted-foreground text-xs">
          Stable id (lowercase letters, digits, hyphens). Fixed once created -- delete and recreate to change it.
        </span>
      </div>
      <FormFields state={state} set={set} />
    </FormDialog>
  )
}

/** Edit dialog -- the shared fields for an existing basemap. Code is shown but
 * not editable (it's the stable id the map persists). */
function EditBasemapDialog({ basemap, onClose }: { basemap: BasemapRecord; onClose: () => void }) {
  const [state, setState] = useState<FormState>({
    label: basemap.label,
    urlTemplate: basemap.url_template,
    attribution: basemap.attribution.map((l) => ({ ...l })),
    maxZoom: basemap.max_zoom == null ? "" : String(basemap.max_zoom),
    minZoom: String(basemap.min_zoom),
    tms: basemap.tms,
    sortOrder: String(basemap.sort_order),
    isEnabled: basemap.is_enabled,
  })
  const update = useUpdateBasemap()
  const set = (patch: Partial<FormState>) => setState((s) => ({ ...s, ...patch }))
  const canSave = formValid(state) && !update.isPending

  function handleSave() {
    const shared = toShared(state)
    // Send the full editable set; a blank max zoom clears the cap (distinct
    // from "leave it alone" on the wire).
    const body: UpdateBasemapInput = {
      label: shared.label,
      url_template: shared.url_template,
      attribution: shared.attribution,
      min_zoom: shared.min_zoom,
      tms: shared.tms,
      sort_order: shared.sort_order,
      is_enabled: shared.is_enabled,
    }
    if (shared.max_zoom == null) body.clear_max_zoom = true
    else body.max_zoom = shared.max_zoom

    update.mutate(
      { id: basemap.id, body },
      { onSuccess: onClose, onError: (err) => toastError(err, "Could not update the basemap.") },
    )
  }

  return (
    <FormDialog
      open
      onOpenChange={(next) => next || onClose()}
      title="Edit basemap"
      description={`Basemap "${basemap.code}"${basemap.is_builtin ? " (built-in)" : ""}. Code is fixed.`}
      onSubmit={handleSave}
      submitLabel="Save"
      canSubmit={canSave}
      saving={update.isPending}
    >
      <FormFields state={state} set={set} />
    </FormDialog>
  )
}

/** One basemap row: label + code, a built-in/disabled badge, and quick
 * enable-toggle / edit / delete actions. Built-ins can't be deleted (the delete
 * button is hidden for them) -- they're disabled instead. */
function BasemapRow({
  basemap,
  onEdit,
  onDelete,
}: {
  basemap: BasemapRecord
  onEdit: (b: BasemapRecord) => void
  onDelete: (b: BasemapRecord) => void
}) {
  const update = useUpdateBasemap()

  function toggleEnabled() {
    update.mutate(
      { id: basemap.id, body: { is_enabled: !basemap.is_enabled } },
      { onError: (err) => toastError(err, "Could not change the basemap.") },
    )
  }

  return (
    <div className="hover:bg-muted/50 group flex items-center gap-2 rounded-md px-2 py-1.5">
      <span className={`truncate text-sm font-medium ${basemap.is_enabled ? "" : "text-muted-foreground"}`}>
        {basemap.label}
      </span>
      <span className="bg-muted text-muted-foreground rounded px-1.5 py-0.5 font-mono text-xs">{basemap.code}</span>
      {basemap.is_builtin && (
        <span className="bg-muted text-muted-foreground rounded px-1.5 py-0.5 text-xs">built-in</span>
      )}
      {!basemap.is_enabled && (
        <span className="bg-muted text-muted-foreground rounded px-1.5 py-0.5 text-xs">disabled</span>
      )}
      <div className="ml-auto flex shrink-0 items-center gap-0.5 opacity-0 transition-opacity group-hover:opacity-100 focus-within:opacity-100">
        <Button
          variant="ghost"
          size="icon"
          className="size-7"
          title={basemap.is_enabled ? "Disable" : "Enable"}
          onClick={toggleEnabled}
          disabled={update.isPending}
        >
          {basemap.is_enabled ? <Eye className="size-4" /> : <EyeOff className="size-4" />}
        </Button>
        <Button variant="ghost" size="icon" className="size-7" title="Edit" onClick={() => onEdit(basemap)}>
          <Pencil className="size-4" />
        </Button>
        {!basemap.is_builtin && (
          <Button
            variant="ghost"
            size="icon"
            className="text-destructive size-7"
            title="Delete"
            onClick={() => onDelete(basemap)}
          >
            <Trash2 className="size-4" />
          </Button>
        )}
      </div>
    </div>
  )
}

/**
 * The basemap catalog manager, embedded in the Map settings page. Lists every
 * basemap (enabled and disabled) and lets an admin add custom ones, edit any
 * (including the built-ins' label/URL/order), toggle visibility, and delete the
 * custom ones -- all without a rebuild, since the map reads this list at
 * runtime (hooks/use-basemaps.ts).
 */
export function BasemapsManager() {
  const { data, isPending, isError } = useAllBasemaps()
  const [creating, setCreating] = useState(false)
  const [editing, setEditing] = useState<BasemapRecord | null>(null)
  const [deleting, setDeleting] = useState<BasemapRecord | null>(null)
  const del = useDeleteBasemap()

  return (
    <Card>
      <CardHeader className="flex-row items-center justify-between pb-2">
        <div>
          <CardTitle className="text-sm">Basemaps</CardTitle>
          <CardDescription>
            The raster tile layers the map can show, in the map's picker. Reorder with the sort value; disable to
            hide one without deleting it.
          </CardDescription>
        </div>
        <Button size="sm" onClick={() => setCreating(true)}>
          <Plus className="size-4" />
          New basemap
        </Button>
      </CardHeader>
      <CardContent className="pt-0">
        {isPending ? (
          <div className="text-muted-foreground flex items-center gap-2 text-sm">
            <Loader2 className="size-4 animate-spin" />
            Loading basemaps...
          </div>
        ) : isError ? (
          <p className="text-destructive text-sm">Could not load basemaps.</p>
        ) : data && data.length > 0 ? (
          <div className="flex flex-col">
            {data.map((b) => (
              <BasemapRow key={b.id} basemap={b} onEdit={setEditing} onDelete={setDeleting} />
            ))}
          </div>
        ) : (
          <p className="text-muted-foreground text-sm">No basemaps yet.</p>
        )}
      </CardContent>

      {creating && <CreateBasemapDialog onClose={() => setCreating(false)} />}
      {editing && <EditBasemapDialog key={editing.id} basemap={editing} onClose={() => setEditing(null)} />}

      <ConfirmDeleteDialog
        open={deleting !== null}
        title={`Delete ${deleting?.label ?? ""}?`}
        description="This can't be undone. Anyone whose saved choice was this basemap falls back to the first one."
        confirmLabel="Delete"
        onOpenChange={(open) => !open && setDeleting(null)}
        onConfirm={async () => {
          if (!deleting) return
          // A rejection (e.g. a built-in slipping through) is surfaced by the
          // dialog itself, which keeps the message visible.
          await del.mutateAsync(deleting.id)
        }}
      />
    </Card>
  )
}
