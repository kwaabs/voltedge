import { useEffect, useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { Activity, Loader2, Pencil, Plus, Search, Trash2 } from "lucide-react"

import { ConfirmDeleteDialog } from "@/components/admin/confirm-delete-dialog"
import { FormDialog } from "@/components/admin/form-dialog"
import { inputClass } from "@/components/map/field-utils"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { usePermission } from "@/hooks/use-auth"
import {
  type CurveBand,
  type CurvePoint,
  type ProtectionCurve,
  useProtectionCurveDetail,
  useProtectionCurves,
} from "@/hooks/use-protection-curves"
import { ApiError, apiFetch } from "@/lib/api"
import { cn } from "@/lib/utils"
import { useToastStore } from "@/stores/toast-store"

const labelClass = "text-xs font-medium"

// The curve bands, with human labels. A relay carries a single `operate` curve;
// a fuse carries the two bounding bands. All three are offered regardless of
// quantity -- the server is the real validator -- but ordered fuse-first since
// loading a manufacturer fuse curve is the common case this page exists for.
const BAND_OPTIONS: { value: CurveBand; label: string }[] = [
  { value: "minimum_melt", label: "Minimum melt" },
  { value: "total_clear", label: "Total clear" },
  { value: "operate", label: "Operate (relay)" },
]

const QUANTITY_OPTIONS: { value: "ampere" | "multiple"; label: string }[] = [
  { value: "ampere", label: "Ampere (fuse — absolute amps)" },
  { value: "multiple", label: "Multiple (relay — × pickup)" },
]

function pushErr(err: unknown, fallback: string) {
  useToastStore.getState().pushError(err instanceof ApiError ? err.message : fallback)
}

/**
 * Admin editor for the protection-curve catalog (metadata.protection_curves +
 * its points). A curve's `code` is what a feature's curve_ref / curve_type
 * stores and what the coordination engine joins on, so it's set once at create
 * and immutable after. The point set IS the curve -- the engine interpolates
 * these stored (current, time) samples; it never evaluates an IDMT formula.
 *
 * This replaces the SQL-seed-only workflow (seed/fuse_curves_library.sql): an
 * admin loads a manufacturer fuse/relay curve here and it immediately appears in
 * the feature form's curve picker. Two-pane master-detail, mirroring the Domains
 * page: the curve list filters on the left, the selected curve's header + an
 * editable points table on the right.
 */
export function AdminProtectionCurvesPage() {
  const { data, isPending, isError } = useProtectionCurves()
  const queryClient = useQueryClient()
  const canWrite = usePermission()("metadata:write")

  const [query, setQuery] = useState("")
  const [selectedCode, setSelectedCode] = useState<string | null>(null)
  const [creating, setCreating] = useState(false)
  const [editing, setEditing] = useState<ProtectionCurve | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<ProtectionCurve | null>(null)

  function invalidate() {
    // The list (and every quantity-filtered picker, keyed ["protection-curves", …])
    // plus the open detail.
    queryClient.invalidateQueries({ queryKey: ["protection-curves"] })
    queryClient.invalidateQueries({ queryKey: ["protection-curve"] })
  }

  const q = query.trim().toLowerCase()
  const curves = data?.curves ?? []
  const filtered = curves.filter(
    (c) => !q || c.name.toLowerCase().includes(q) || c.code.toLowerCase().includes(q),
  )
  const selected = curves.find((c) => c.code === selectedCode) ?? filtered[0] ?? null

  return (
    <div className="flex h-full flex-col gap-4 p-4">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-lg font-semibold">Protection curves</h1>
          <p className="text-muted-foreground text-sm">
            The time-current curve catalog — fuse melt/clear and relay IDMT shapes the coordination engine
            interpolates. A curve loaded here is immediately pickable as a fuse's <span className="font-mono">curve_ref</span>.
          </p>
        </div>
        {canWrite && (
          <Button size="sm" onClick={() => setCreating(true)}>
            <Plus className="size-4" />
            New curve
          </Button>
        )}
      </div>

      {creating && (
        <CurveFormDialog
          mode="create"
          onDone={(code) => {
            setCreating(false)
            invalidate()
            if (code) setSelectedCode(code)
          }}
        />
      )}
      {editing && (
        <CurveFormDialog
          mode="edit"
          curve={editing}
          onDone={() => {
            setEditing(null)
            invalidate()
          }}
        />
      )}

      {isPending ? (
        <div className="text-muted-foreground flex items-center gap-2 text-sm">
          <Loader2 className="size-4 animate-spin" />
          Loading curves…
        </div>
      ) : isError || !data ? (
        <p className="text-destructive text-sm">Could not load protection curves.</p>
      ) : curves.length === 0 ? (
        <p className="text-muted-foreground text-sm">
          No curves yet{canWrite ? " — use New curve to add one." : "."}
        </p>
      ) : (
        <div className="flex min-h-0 flex-1 gap-4">
          {/* Left: the curve list, filtered and selectable. */}
          <div className="flex w-80 shrink-0 flex-col overflow-hidden rounded-lg border">
            <div className="border-b p-2">
              <div className="relative">
                <Search className="text-muted-foreground pointer-events-none absolute top-1/2 left-2 size-4 -translate-y-1/2" />
                <input
                  className={`${inputClass} h-8 pl-8`}
                  placeholder="Filter curves…"
                  value={query}
                  onChange={(e) => setQuery(e.target.value)}
                />
              </div>
            </div>
            <div className="max-h-[65vh] flex-1 overflow-y-auto">
              {filtered.length === 0 ? (
                <p className="text-muted-foreground p-3 text-sm">No curves match.</p>
              ) : (
                filtered.map((c) => (
                  <button
                    key={c.code}
                    type="button"
                    onClick={() => setSelectedCode(c.code)}
                    className={cn(
                      "hover:bg-muted/50 flex w-full flex-col items-start gap-0.5 border-b px-3 py-2 text-left last:border-b-0",
                      selected?.code === c.code && "bg-muted",
                    )}
                  >
                    <span className="flex w-full items-center gap-1.5">
                      <Activity className="text-muted-foreground size-3.5 shrink-0" />
                      <span className="truncate text-sm font-medium">{c.name}</span>
                      <span className="text-muted-foreground ml-auto shrink-0 text-[10px]">{c.quantity}</span>
                    </span>
                    <span className="text-muted-foreground font-mono text-[10px]">{c.code}</span>
                  </button>
                ))
              )}
            </div>
          </div>

          {/* Right: the selected curve's header + editable points table. */}
          <div className="min-w-0 flex-1 overflow-y-auto">
            {selected ? (
              <CurveDetailPane
                curve={selected}
                canWrite={canWrite}
                onEdit={() => setEditing(selected)}
                onDelete={() => setDeleteTarget(selected)}
                onPointsSaved={invalidate}
              />
            ) : (
              <div className="text-muted-foreground flex h-full items-center justify-center text-sm">
                Select a curve to view its points.
              </div>
            )}
          </div>
        </div>
      )}

      <ConfirmDeleteDialog
        open={deleteTarget !== null}
        title={`Delete ${deleteTarget?.code ?? "this curve"}?`}
        description="Its points are removed too. Any fuse or relay still pointing at this curve's code will no longer resolve an operate time until repointed. This can't be undone."
        onOpenChange={(open) => !open && setDeleteTarget(null)}
        onConfirm={async () => {
          if (!deleteTarget) return
          try {
            await apiFetch(`/curves/${deleteTarget.code}`, { method: "DELETE" })
            if (selectedCode === deleteTarget.code) setSelectedCode(null)
            invalidate()
          } catch (err) {
            pushErr(err, "Could not delete this curve.")
            throw err
          }
        }}
      />
    </div>
  )
}

/** Create a new curve or edit an existing curve's header. The code is only
 * settable on create (immutable after -- features reference it). */
function CurveFormDialog({
  mode,
  curve,
  onDone,
}: {
  mode: "create" | "edit"
  curve?: ProtectionCurve
  onDone: (code?: string) => void
}) {
  const [code, setCode] = useState(curve?.code ?? "")
  const [name, setName] = useState(curve?.name ?? "")
  const [standard, setStandard] = useState(curve?.standard ?? "FUSE")
  const [quantity, setQuantity] = useState<"ampere" | "multiple">(
    (curve?.quantity as "ampere" | "multiple") ?? "ampere",
  )
  const [saving, setSaving] = useState(false)
  const queryClient = useQueryClient()

  const canSave = (mode === "edit" || code.trim().length > 0) && name.trim().length > 0 && standard.trim().length > 0

  async function handleSave() {
    setSaving(true)
    try {
      if (mode === "create") {
        const created = await apiFetch<ProtectionCurve>("/curves", {
          method: "POST",
          body: JSON.stringify({ code: code.trim(), name: name.trim(), standard: standard.trim(), quantity }),
        })
        queryClient.invalidateQueries({ queryKey: ["protection-curves"] })
        onDone(created.code)
      } else {
        await apiFetch(`/curves/${curve!.code}`, {
          method: "PATCH",
          body: JSON.stringify({ name: name.trim(), standard: standard.trim(), quantity }),
        })
        onDone(curve!.code)
      }
    } catch (err) {
      pushErr(err, "Could not save this curve.")
    } finally {
      setSaving(false)
    }
  }

  return (
    <FormDialog
      open
      onOpenChange={(next) => next || onDone()}
      title={mode === "create" ? "New protection curve" : <>Edit curve <span className="font-mono">{curve?.code}</span></>}
      description={
        mode === "create"
          ? "A stable code (what features reference), a display name, the issuing standard, and how its current axis is read."
          : "The code is fixed (features reference it). Add or edit the points from the detail pane."
      }
      onSubmit={handleSave}
      submitLabel={mode === "create" ? "Create" : "Save"}
      canSubmit={canSave}
      saving={saving}
    >
      {mode === "create" && (
        <div className="flex flex-col gap-1">
          <label className={labelClass}>
            Code <span className="text-destructive">*</span>
          </label>
          <input
            className={inputClass}
            value={code}
            onChange={(e) => setCode(e.target.value.toUpperCase().replace(/\s+/g, "_"))}
            placeholder="e.g. FUSE_400A_CL"
          />
          <p className="text-muted-foreground text-[11px]">
            What a fuse's <span className="font-mono">curve_ref</span> stores. Immutable once created.
          </p>
        </div>
      )}
      <div className="flex flex-col gap-1">
        <label className={labelClass}>
          Name <span className="text-destructive">*</span>
        </label>
        <input
          className={inputClass}
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder="e.g. ABC 400 A current-limiting fuse"
        />
      </div>
      <div className="grid grid-cols-2 gap-3">
        <div className="flex flex-col gap-1">
          <label className={labelClass}>
            Standard <span className="text-destructive">*</span>
          </label>
          <input
            className={inputClass}
            value={standard}
            onChange={(e) => setStandard(e.target.value)}
            placeholder="e.g. FUSE, IEC, IEEE"
          />
        </div>
        <div className="flex flex-col gap-1">
          <label className={labelClass}>Quantity</label>
          <select className={inputClass} value={quantity} onChange={(e) => setQuantity(e.target.value as "ampere" | "multiple")}>
            {QUANTITY_OPTIONS.map((o) => (
              <option key={o.value} value={o.value}>
                {o.label}
              </option>
            ))}
          </select>
        </div>
      </div>
    </FormDialog>
  )
}

/** The right pane: the curve header actions + its editable points table. Loads
 * the full point set (header list carries no points) and holds an editable
 * draft, saved as a whole set (PUT replace-all). */
function CurveDetailPane({
  curve,
  canWrite,
  onEdit,
  onDelete,
  onPointsSaved,
}: {
  curve: ProtectionCurve
  canWrite: boolean
  onEdit: () => void
  onDelete: () => void
  onPointsSaved: () => void
}) {
  const { data, isPending, isError } = useProtectionCurveDetail(curve.code)

  return (
    <div className="rounded-lg border">
      <div className="flex items-start gap-2 border-b p-3">
        <Activity className="text-muted-foreground mt-0.5 size-4 shrink-0" />
        <div className="min-w-0 flex-1">
          <h2 className="text-sm font-semibold">{curve.name}</h2>
          <p className="text-muted-foreground font-mono text-xs">{curve.code}</p>
        </div>
        <Badge variant="outline" className="shrink-0">
          {curve.standard} · {curve.quantity}
        </Badge>
        {canWrite && (
          <div className="flex items-center gap-1">
            <Button variant="ghost" size="icon" className="size-7" title="Edit curve" onClick={onEdit}>
              <Pencil className="size-4" />
            </Button>
            <Button variant="ghost" size="icon" className="text-destructive size-7" title="Delete curve" onClick={onDelete}>
              <Trash2 className="size-4" />
            </Button>
          </div>
        )}
      </div>

      <div className="p-3">
        {isPending ? (
          <div className="text-muted-foreground flex items-center gap-2 text-sm">
            <Loader2 className="size-4 animate-spin" /> Loading points…
          </div>
        ) : isError || !data ? (
          <p className="text-destructive text-sm">Could not load this curve's points.</p>
        ) : (
          <PointsEditor
            key={curve.code}
            code={curve.code}
            quantity={curve.quantity}
            points={data.points}
            canWrite={canWrite}
            onSaved={onPointsSaved}
          />
        )}
      </div>
    </div>
  )
}

/** The editable (band, current, time) table. Seeded from the fetched points into
 * a local draft; Save PUTs the whole set (replace-all), Reset discards edits.
 * Rows are free-form so you paste a datasheet in top-to-bottom. */
function PointsEditor({
  code,
  quantity,
  points,
  canWrite,
  onSaved,
}: {
  code: string
  quantity: string
  points: CurvePoint[]
  canWrite: boolean
  onSaved: () => void
}) {
  // A draft row keeps current/time as strings so an in-progress "0." or "" is
  // editable; they're parsed to numbers only on save.
  type DraftRow = { band: CurveBand; current: string; time_s: string }
  const toDraft = (ps: CurvePoint[]): DraftRow[] =>
    ps.map((p) => ({ band: p.band, current: String(p.current), time_s: String(p.time_s) }))

  const [rows, setRows] = useState<DraftRow[]>(toDraft(points))
  const [saving, setSaving] = useState(false)

  // Re-seed when the fetched set changes (a different curve selected, or a save
  // round-trips fresh data).
  useEffect(() => {
    setRows(toDraft(points))
  }, [points])

  const dirty = JSON.stringify(rows) !== JSON.stringify(toDraft(points))

  function update(i: number, patch: Partial<DraftRow>) {
    setRows((rs) => rs.map((r, ri) => (ri === i ? { ...r, ...patch } : r)))
  }
  function addRow() {
    // Default a new row to the band of the last row, so entering one band's
    // points in a run doesn't mean re-picking the band every time.
    const band = rows.length > 0 ? rows[rows.length - 1].band : "minimum_melt"
    setRows((rs) => [...rs, { band, current: "", time_s: "" }])
  }
  function removeRow(i: number) {
    setRows((rs) => rs.filter((_, ri) => ri !== i))
  }

  async function handleSave() {
    // Validate client-side for a clear message before the round-trip; the server
    // re-checks (table CHECKs + unique band/current).
    const parsed: CurvePoint[] = []
    for (const [i, r] of rows.entries()) {
      const current = Number(r.current)
      const time_s = Number(r.time_s)
      if (r.current.trim() === "" || Number.isNaN(current) || current <= 0) {
        pushErr(null, `Row ${i + 1}: current must be a number greater than 0.`)
        return
      }
      if (r.time_s.trim() === "" || Number.isNaN(time_s) || time_s < 0) {
        pushErr(null, `Row ${i + 1}: time must be 0 or greater.`)
        return
      }
      parsed.push({ band: r.band, current, time_s })
    }
    setSaving(true)
    try {
      await apiFetch(`/curves/${code}/points`, { method: "PUT", body: JSON.stringify({ points: parsed }) })
      onSaved()
    } catch (err) {
      pushErr(err, "Could not save the points.")
    } finally {
      setSaving(false)
    }
  }

  return (
    <div className="flex flex-col gap-2">
      <div className="flex items-center justify-between">
        <span className="text-xs font-medium">
          Points <span className="text-muted-foreground">({rows.length})</span>
        </span>
        <span className="text-muted-foreground text-[11px]">
          current in {quantity === "ampere" ? "amperes" : "× pickup"} · time in seconds
        </span>
      </div>

      {rows.length === 0 ? (
        <p className="text-muted-foreground text-sm">
          No points yet{canWrite ? " — add rows below, then Save." : "."}
        </p>
      ) : (
        <div className="overflow-hidden rounded-md border">
          <div className="bg-muted text-muted-foreground grid grid-cols-[1fr_1fr_1fr_auto] gap-2 px-2 py-1 text-[11px] font-medium">
            <span>Band</span>
            <span>Current</span>
            <span>Time (s)</span>
            <span className="w-7" />
          </div>
          {rows.map((r, i) => (
            <div key={i} className="grid grid-cols-[1fr_1fr_1fr_auto] items-center gap-2 border-t px-2 py-1">
              <select
                className={`${inputClass} h-8`}
                value={r.band}
                disabled={!canWrite}
                onChange={(e) => update(i, { band: e.target.value as CurveBand })}
              >
                {BAND_OPTIONS.map((b) => (
                  <option key={b.value} value={b.value}>
                    {b.label}
                  </option>
                ))}
              </select>
              <input
                type="number"
                step="any"
                className={`${inputClass} h-8`}
                value={r.current}
                disabled={!canWrite}
                onChange={(e) => update(i, { current: e.target.value })}
                placeholder="e.g. 2000"
              />
              <input
                type="number"
                step="any"
                className={`${inputClass} h-8`}
                value={r.time_s}
                disabled={!canWrite}
                onChange={(e) => update(i, { time_s: e.target.value })}
                placeholder="e.g. 0.045"
              />
              {canWrite ? (
                <Button variant="ghost" size="icon" className="size-7" title="Remove row" onClick={() => removeRow(i)}>
                  <Trash2 className="size-4" />
                </Button>
              ) : (
                <span className="w-7" />
              )}
            </div>
          ))}
        </div>
      )}

      {canWrite && (
        <div className="flex items-center gap-2">
          <Button variant="outline" size="sm" onClick={addRow}>
            <Plus className="size-4" />
            Add point
          </Button>
          <div className="flex-1" />
          {dirty && (
            <Button variant="ghost" size="sm" onClick={() => setRows(toDraft(points))} disabled={saving}>
              Reset
            </Button>
          )}
          <Button size="sm" onClick={handleSave} disabled={!dirty || saving}>
            {saving && <Loader2 className="size-4 animate-spin" />}
            Save points
          </Button>
        </div>
      )}
    </div>
  )
}
