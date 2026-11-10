import { useEffect, useMemo, useRef, useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { Loader2, Pencil, Play, Plus, Trash2 } from "lucide-react"

import { ConfirmDeleteDialog } from "@/components/admin/confirm-delete-dialog"
import { FormDialog } from "@/components/admin/form-dialog"
import { inputClass } from "@/components/map/field-utils"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { usePermission } from "@/hooks/use-auth"
import { useAdminCimProfiles, useAdminFeatureClasses } from "@/hooks/use-admin"
import { type TerritorySummary, useTerritories } from "@/hooks/use-territories"
import {
  type CimSyncTarget,
  type CimSyncTargetInput,
  useCimSyncTargets,
  useDeleteCimSyncTarget,
  useRunCimSyncTarget,
  useSaveCimSyncTarget,
} from "@/hooks/use-cim-sync-targets"
import { useActiveExportCount } from "@/hooks/use-export-jobs"
import { ApiError } from "@/lib/api"
import { useToastStore } from "@/stores/toast-store"

const labelClass = "text-xs font-medium"

interface Bbox {
  minX: string
  minY: string
  maxX: string
  maxY: string
}

interface Draft {
  name: string
  description: string
  active: boolean
  feature_classes: string[]
  territory_ids: string[]
  bbox: Bbox
  cim_profile: string // "" = default profile
  target_srid: string // text; "" = no reprojection
  schedule: string // "" = manual
}

const EMPTY_BBOX: Bbox = { minX: "", minY: "", maxX: "", maxY: "" }

const BLANK: Draft = {
  name: "",
  description: "",
  active: true,
  feature_classes: [],
  territory_ids: [],
  bbox: { ...EMPTY_BBOX },
  cim_profile: "",
  target_srid: "",
  schedule: "",
}

function draftFromTarget(t: CimSyncTarget): Draft {
  const b = t.bbox && t.bbox.length === 4 ? t.bbox : null
  return {
    name: t.name,
    description: t.description ?? "",
    active: t.active,
    feature_classes: t.feature_classes ?? [],
    territory_ids: t.territory_ids ?? [],
    bbox: b ? { minX: String(b[0]), minY: String(b[1]), maxX: String(b[2]), maxY: String(b[3]) } : { ...EMPTY_BBOX },
    cim_profile: t.cim_profile ?? "",
    target_srid: t.target_srid != null ? String(t.target_srid) : "",
    schedule: t.schedule ?? "",
  }
}

// bboxArray returns the [minX,minY,maxX,maxY] numbers when all four are filled and
// numeric, else null (no bbox = whole scope). A partially-filled bbox is ignored.
function bboxArray(b: Bbox): number[] | null {
  const vals = [b.minX, b.minY, b.maxX, b.maxY].map((s) => s.trim())
  if (vals.some((v) => v === "")) return null
  const nums = vals.map(Number)
  if (nums.some((n) => !Number.isFinite(n))) return null
  return nums
}

function toInput(d: Draft): CimSyncTargetInput {
  const srid = d.target_srid.trim()
  return {
    name: d.name.trim(),
    description: d.description.trim() === "" ? null : d.description.trim(),
    active: d.active,
    feature_classes: d.feature_classes,
    territory_ids: d.territory_ids.length > 0 ? d.territory_ids : null,
    bbox: bboxArray(d.bbox),
    cim_profile: d.cim_profile === "" ? null : d.cim_profile,
    target_srid: srid === "" ? null : Number(srid),
    schedule: d.schedule === "" ? null : d.schedule,
  }
}

// flattenTerritories orders the tree depth-first (roots first, siblings by name)
// with an indent depth, so the picker reads as a hierarchy. An orphan (parent not
// in the list) is treated as a root.
function flattenTerritories(list: TerritorySummary[]): { t: TerritorySummary; depth: number }[] {
  const byParent = new Map<string | null, TerritorySummary[]>()
  const ids = new Set(list.map((x) => x.id))
  for (const t of list) {
    const p = t.parent_id && ids.has(t.parent_id) ? t.parent_id : null
    byParent.set(p, [...(byParent.get(p) ?? []), t])
  }
  const out: { t: TerritorySummary; depth: number }[] = []
  const walk = (parent: string | null, depth: number) => {
    for (const t of (byParent.get(parent) ?? []).sort((a, b) => a.name.localeCompare(b.name))) {
      out.push({ t, depth })
      walk(t.id, depth + 1)
    }
  }
  walk(null, 0)
  return out
}

/**
 * CIM sync targets admin: each row is a named external consumer fed INCREMENTAL
 * CIM. "Run" enqueues an export job -- the first run sends the full model, later
 * runs a difference model against the stored baseline -- whose status/download
 * live in the existing export jobs panel. metadata:write gated.
 */
export function AdminCimSyncTargetsPage() {
  const canWrite = usePermission()("metadata:write")
  const { data, isPending } = useCimSyncTargets()
  const classes = useAdminFeatureClasses()
  const profiles = useAdminCimProfiles()
  const territories = useTerritories()
  const save = useSaveCimSyncTarget()
  const del = useDeleteCimSyncTarget()
  const run = useRunCimSyncTarget()

  const [editing, setEditing] = useState<{ id?: string; draft: Draft } | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<CimSyncTarget | null>(null)
  const [lastQueued, setLastQueued] = useState<string | null>(null)

  // A target's baseline/last-run only advance when its background export job
  // finishes. The sync-targets list doesn't poll, so watch the shared export-jobs
  // activity count (it self-polls every 2s while a job is pending) and, when it
  // falls back to zero, refetch the list so the finished run's row updates live.
  const qc = useQueryClient()
  const activeExports = useActiveExportCount()
  const prevActiveExports = useRef(activeExports)
  useEffect(() => {
    if (prevActiveExports.current > 0 && activeExports === 0) {
      qc.invalidateQueries({ queryKey: ["cim-sync-targets"] })
    }
    prevActiveExports.current = activeExports
  }, [activeExports, qc])

  // CIM only emits geometric classes; a geometry-less class has nothing to export.
  const geometricClasses = useMemo(
    () => (classes.data?.feature_classes ?? []).filter((c) => c.geometry_type !== "none"),
    [classes.data],
  )
  const territoryRows = useMemo(() => flattenTerritories(territories.data?.territories ?? []), [territories.data])

  async function handleSave() {
    if (!editing) return
    try {
      await save.mutateAsync({ id: editing.id, input: toInput(editing.draft) })
      setEditing(null)
    } catch (err) {
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not save this sync target.")
    }
  }

  async function handleRun(t: CimSyncTarget) {
    try {
      const res = await run.mutateAsync(t.id)
      setLastQueued(res.job_id)
    } catch (err) {
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not start the export.")
    }
  }

  const draft = editing?.draft
  const canSubmit = !!draft && draft.name.trim() !== "" && draft.feature_classes.length > 0

  return (
    <div className="flex flex-col gap-4 p-4">
      <div className="flex items-start justify-between gap-4">
        <div>
          <h1 className="text-lg font-semibold">CIM sync targets</h1>
          <p className="text-muted-foreground text-sm">
            Feed an external system incremental CIM. The first run of a target sends a full model; every run
            after sends a difference model against the last baseline. Exports are published-only (no drafts).
          </p>
        </div>
        {canWrite && (
          <Button size="sm" onClick={() => setEditing({ draft: { ...BLANK } })}>
            <Plus className="size-4" /> New target
          </Button>
        )}
      </div>

      {lastQueued && (
        <div className="rounded-md border border-emerald-500/40 bg-emerald-500/10 px-3 py-2 text-sm">
          Export queued (job <span className="font-mono">{lastQueued.slice(0, 8)}</span>). Open the export jobs
          panel to download it when it finishes.
        </div>
      )}

      <Card>
        <CardHeader>
          <CardTitle>Targets</CardTitle>
          <CardDescription>Each target remembers the baseline it last sent.</CardDescription>
        </CardHeader>
        <CardContent className="flex flex-col gap-2">
          {isPending && <Loader2 className="text-muted-foreground size-4 animate-spin" />}
          {!isPending && (data?.sync_targets.length ?? 0) === 0 && (
            <p className="text-muted-foreground text-sm">No sync targets yet.</p>
          )}
          {(data?.sync_targets ?? []).map((t) => (
            <div key={t.id} className="flex items-center justify-between gap-3 rounded-md border px-3 py-2">
              <div className="min-w-0">
                <div className="flex items-center gap-2">
                  <span className="font-medium">{t.name}</span>
                  {!t.active && <span className="text-muted-foreground text-xs">(inactive)</span>}
                </div>
                <div className="text-muted-foreground text-xs">
                  {t.feature_classes.length} class{t.feature_classes.length === 1 ? "" : "es"}
                  {t.cim_profile ? ` · profile ${t.cim_profile}` : " · default profile"}
                  {t.target_srid ? ` · EPSG:${t.target_srid}` : ""}
                  {t.territory_ids && t.territory_ids.length > 0
                    ? ` · ${t.territory_ids.length} territor${t.territory_ids.length === 1 ? "y" : "ies"}`
                    : ""}
                  {t.bbox && t.bbox.length === 4 ? " · bbox" : ""}
                  {t.schedule ? ` · auto ${t.schedule}` : ""}
                  {" · "}
                  {t.has_baseline
                    ? `baseline set${t.last_exported_at ? ` (last run ${t.last_exported_at.slice(0, 10)})` : ""} — next run: difference`
                    : "no baseline yet — next run: full load"}
                </div>
              </div>
              <div className="flex shrink-0 items-center gap-1">
                <Button
                  size="sm"
                  variant="outline"
                  disabled={!t.active || run.isPending}
                  onClick={() => handleRun(t)}
                  title={t.active ? "Run an incremental export" : "Target is inactive"}
                >
                  <Play className="size-4" /> Run
                </Button>
                {canWrite && (
                  <>
                    <Button size="icon" variant="ghost" onClick={() => setEditing({ id: t.id, draft: draftFromTarget(t) })}>
                      <Pencil className="size-4" />
                    </Button>
                    <Button size="icon" variant="ghost" onClick={() => setDeleteTarget(t)}>
                      <Trash2 className="text-destructive size-4" />
                    </Button>
                  </>
                )}
              </div>
            </div>
          ))}
        </CardContent>
      </Card>

      {editing && draft && (
        <FormDialog
          open
          onOpenChange={(next) => next || setEditing(null)}
          title={editing.id ? "Edit sync target" : "New sync target"}
          onSubmit={handleSave}
          submitLabel={editing.id ? "Save" : "Create"}
          canSubmit={canSubmit}
          saving={save.isPending}
          className="max-w-2xl"
        >
          <div className="flex flex-col gap-1">
            <label className={labelClass}>
              Name <span className="text-destructive">*</span>
            </label>
            <input
              className={inputClass}
              value={draft.name}
              onChange={(e) => setEditing({ ...editing, draft: { ...draft, name: e.target.value } })}
              placeholder="e.g. Planning OpenDSS feed"
            />
          </div>

          <div className="flex flex-col gap-1">
            <label className={labelClass}>Description</label>
            <input
              className={inputClass}
              value={draft.description}
              onChange={(e) => setEditing({ ...editing, draft: { ...draft, description: e.target.value } })}
            />
          </div>

          <div className="grid grid-cols-3 gap-3">
            <div className="flex flex-col gap-1">
              <label className={labelClass}>CIM profile</label>
              <select
                className={inputClass}
                value={draft.cim_profile}
                onChange={(e) => setEditing({ ...editing, draft: { ...draft, cim_profile: e.target.value } })}
              >
                <option value="">(default)</option>
                {(profiles.data?.cim_profiles ?? []).map((p) => (
                  <option key={p.code} value={p.code}>
                    {p.name} ({p.code})
                  </option>
                ))}
              </select>
            </div>
            <div className="flex flex-col gap-1">
              <label className={labelClass}>Target SRID (optional)</label>
              <input
                className={inputClass}
                value={draft.target_srid}
                onChange={(e) => setEditing({ ...editing, draft: { ...draft, target_srid: e.target.value } })}
                placeholder="e.g. 32630"
                inputMode="numeric"
              />
            </div>
            <div className="flex flex-col gap-1">
              <label className={labelClass}>Auto-run</label>
              <select
                className={inputClass}
                value={draft.schedule}
                onChange={(e) => setEditing({ ...editing, draft: { ...draft, schedule: e.target.value } })}
              >
                <option value="">Manual</option>
                <option value="hourly">Hourly</option>
                <option value="daily">Daily</option>
                <option value="weekly">Weekly</option>
              </select>
            </div>
          </div>

          <label className="flex items-center gap-2 text-sm">
            <input
              type="checkbox"
              checked={draft.active}
              onChange={(e) => setEditing({ ...editing, draft: { ...draft, active: e.target.checked } })}
            />
            Active
          </label>

          <div className="flex flex-col gap-1">
            <div className="flex items-center justify-between">
              <label className={labelClass}>
                Feature classes <span className="text-destructive">*</span>
              </label>
              {geometricClasses.length > 0 &&
                (() => {
                  const allSelected = geometricClasses.every((c) =>
                    draft.feature_classes.includes(c.code),
                  )
                  return (
                    <button
                      type="button"
                      className="text-primary text-xs hover:underline"
                      onClick={() =>
                        setEditing({
                          ...editing,
                          draft: {
                            ...draft,
                            feature_classes: allSelected ? [] : geometricClasses.map((c) => c.code),
                          },
                        })
                      }
                    >
                      {allSelected ? "Deselect all" : "Select all"}
                    </button>
                  )
                })()}
            </div>
            <div className="max-h-60 overflow-y-auto rounded-md border p-2">
              {geometricClasses.map((c) => {
                const checked = draft.feature_classes.includes(c.code)
                return (
                  <label key={c.code} className="flex items-center gap-2 py-0.5 text-sm">
                    <input
                      type="checkbox"
                      checked={checked}
                      onChange={(e) =>
                        setEditing({
                          ...editing,
                          draft: {
                            ...draft,
                            feature_classes: e.target.checked
                              ? [...draft.feature_classes, c.code]
                              : draft.feature_classes.filter((x) => x !== c.code),
                          },
                        })
                      }
                    />
                    <span>{c.name}</span>
                    <span className="text-muted-foreground font-mono text-xs">{c.code}</span>
                  </label>
                )
              })}
            </div>
            <p className="text-muted-foreground text-xs">
              Changing the scope resets this target's baseline (the next run becomes a full load).
            </p>
          </div>

          <div className="flex flex-col gap-1">
            <label className={labelClass}>Territories (optional)</label>
            <div className="max-h-48 overflow-y-auto rounded-md border p-2">
              {territoryRows.length === 0 && <p className="text-muted-foreground text-xs">No territories defined.</p>}
              {territoryRows.map(({ t, depth }) => {
                const checked = draft.territory_ids.includes(t.id)
                return (
                  <label
                    key={t.id}
                    className="flex items-center gap-2 py-0.5 text-sm"
                    style={{ paddingLeft: depth * 14 }}
                  >
                    <input
                      type="checkbox"
                      checked={checked}
                      onChange={(e) =>
                        setEditing({
                          ...editing,
                          draft: {
                            ...draft,
                            territory_ids: e.target.checked
                              ? [...draft.territory_ids, t.id]
                              : draft.territory_ids.filter((x) => x !== t.id),
                          },
                        })
                      }
                    />
                    <span>{t.name}</span>
                    <span className="text-muted-foreground text-xs">{t.level_name}</span>
                  </label>
                )
              })}
            </div>
            <p className="text-muted-foreground text-xs">
              None selected = whole scope. Features are stamped to their deepest territory, so pick the exact
              territories you want (no descendant expansion yet).
            </p>
          </div>

          <div className="flex flex-col gap-1">
            <label className={labelClass}>Bounding box (optional, WGS84)</label>
            <div className="grid grid-cols-4 gap-2">
              {(["minX", "minY", "maxX", "maxY"] as const).map((k) => (
                <input
                  key={k}
                  className={inputClass}
                  value={draft.bbox[k]}
                  onChange={(e) =>
                    setEditing({ ...editing, draft: { ...draft, bbox: { ...draft.bbox, [k]: e.target.value } } })
                  }
                  placeholder={k}
                  inputMode="decimal"
                />
              ))}
            </div>
            <p className="text-muted-foreground text-xs">
              Clip to a rectangle: min lon, min lat, max lon, max lat. Leave all blank for the whole scope (a
              partly-filled box is ignored).
            </p>
          </div>
        </FormDialog>
      )}

      <ConfirmDeleteDialog
        open={!!deleteTarget}
        title="Delete sync target"
        description={`Delete "${deleteTarget?.name}"? Its baseline is dropped; a future target over the same scope would start from a full load.`}
        onOpenChange={(next) => next || setDeleteTarget(null)}
        onConfirm={async () => {
          if (!deleteTarget) return
          await del.mutateAsync(deleteTarget.id)
          setDeleteTarget(null)
        }}
      />
    </div>
  )
}
