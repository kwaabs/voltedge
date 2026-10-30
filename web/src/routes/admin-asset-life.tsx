import { useMemo, useState } from "react"
import { CalendarClock, Loader2, Pencil, Plus, Trash2 } from "lucide-react"

import { ConfirmDeleteDialog } from "@/components/admin/confirm-delete-dialog"
import { FormDialog } from "@/components/admin/form-dialog"
import { inputClass } from "@/components/map/field-utils"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import {
  type AgingItem,
  type LifeExpectancy,
  useAgingReport,
  useCreateLifeExpectancy,
  useDeleteLifeExpectancy,
  useLifecycleEvents,
  useLifeExpectancies,
  useUpdateLifeExpectancy,
} from "@/hooks/use-asset-life"
import { useFeatureClasses } from "@/hooks/use-feature-classes"
import { useFeatureClassSchema } from "@/hooks/use-feature-class-schema"
import { ApiError } from "@/lib/api"
import { cn } from "@/lib/utils"
import { useToastStore } from "@/stores/toast-store"

const labelClass = "text-xs font-medium"
const AGING_PAGE_SIZE = 15

function toastError(err: unknown, fallback: string) {
  useToastStore.getState().pushError(err instanceof ApiError ? err.message : fallback)
}

function BucketBadge({ bucket }: { bucket: AgingItem["bucket"] }) {
  if (bucket === "overdue") return <Badge variant="destructive">Overdue</Badge>
  if (bucket === "due_soon")
    return (
      <Badge variant="secondary" className="bg-amber-100 text-amber-900 dark:bg-amber-900/40 dark:text-amber-200">
        Due soon
      </Badge>
    )
  return <Badge variant="secondary">OK</Badge>
}

function AgingReportCard({ classNameByCode }: { classNameByCode: Record<string, string> }) {
  const [dueWithin, setDueWithin] = useState("2")
  const [page, setPage] = useState(0)
  const report = useAgingReport(undefined, Number(dueWithin) || 0)
  const data = report.data

  // Client-side pagination over the already worst-first (server-capped at 1000)
  // list, so the card stays a fixed, scannable height instead of a long table.
  const items = data?.items ?? []
  const pageCount = Math.max(1, Math.ceil(items.length / AGING_PAGE_SIZE))
  const safePage = Math.min(page, pageCount - 1)
  const pageItems = items.slice(safePage * AGING_PAGE_SIZE, safePage * AGING_PAGE_SIZE + AGING_PAGE_SIZE)

  return (
    <Card>
      <CardHeader className="flex flex-row items-end justify-between gap-2">
        <div className="flex flex-col gap-1">
          <CardTitle className="text-base">Aging report</CardTitle>
          <CardDescription>
            In-service assets ranked by remaining useful life. Only classes with a catalog entry (above) are assessed.
          </CardDescription>
        </div>
        <div className="flex flex-col gap-1">
          <label className={labelClass}>Due-soon window (years)</label>
          <input
            className={cn(inputClass, "w-28")}
            type="number"
            min={0}
            step="0.5"
            value={dueWithin}
            onChange={(e) => {
              setDueWithin(e.target.value)
              setPage(0)
            }}
          />
        </div>
      </CardHeader>
      <CardContent>
        {report.isPending ? (
          <div className="text-muted-foreground flex items-center gap-2 text-sm">
            <Loader2 className="size-4 animate-spin" /> Loading…
          </div>
        ) : !data || data.total === 0 ? (
          <p className="text-muted-foreground text-sm">
            Nothing to report yet. Add an expected-life entry below for a class, and make sure its features carry a
            lifecycle status (and ideally a commission date).
          </p>
        ) : (
          <div className="flex flex-col gap-4">
            <div className="flex flex-wrap gap-2 text-sm">
              <span className="rounded-md border px-2.5 py-1">
                <b>{data.total}</b> in service
              </span>
              <span className="rounded-md border border-destructive/40 bg-destructive/10 px-2.5 py-1">
                <b>{data.overdue}</b> overdue
              </span>
              <span className="rounded-md border border-amber-400/50 bg-amber-100/60 px-2.5 py-1 dark:bg-amber-900/30">
                <b>{data.due_soon}</b> due soon
              </span>
              <span className="rounded-md border px-2.5 py-1">
                <b>{data.ok}</b> within life
              </span>
            </div>

            <div className="overflow-hidden rounded-md border">
              <table className="w-full border-collapse text-sm">
                <thead className="bg-muted/50 text-muted-foreground text-xs">
                  <tr>
                    <th className="px-2 py-1.5 text-left font-medium">Asset</th>
                    <th className="px-2 py-1.5 text-left font-medium">In service since</th>
                    <th className="px-2 py-1.5 text-right font-medium">Age (yr)</th>
                    <th className="px-2 py-1.5 text-right font-medium">Expected</th>
                    <th className="px-2 py-1.5 text-right font-medium">Remaining</th>
                    <th className="px-2 py-1.5 text-left font-medium">Standing</th>
                  </tr>
                </thead>
                <tbody>
                  {pageItems.map((it) => (
                    <tr key={`${it.feature_class}:${it.feature_id}`} className="border-t">
                      <td className="px-2 py-1.5">
                        <div className="font-medium">{it.name ?? it.feature_id.slice(0, 8)}</div>
                        <div className="text-muted-foreground text-xs">
                          {classNameByCode[it.feature_class] ?? it.feature_class}
                        </div>
                      </td>
                      <td className="px-2 py-1.5">{it.in_service_since}</td>
                      <td className="px-2 py-1.5 text-right tabular-nums">{it.age_years}</td>
                      <td className="px-2 py-1.5 text-right tabular-nums">{it.expected_years}</td>
                      <td
                        className={cn(
                          "px-2 py-1.5 text-right tabular-nums",
                          it.remaining_years <= 0 && "text-destructive font-medium",
                        )}
                      >
                        {it.remaining_years}
                      </td>
                      <td className="px-2 py-1.5">
                        <BucketBadge bucket={it.bucket} />
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
            {items.length > AGING_PAGE_SIZE && (
              <div className="flex items-center justify-between text-xs">
                <span className="text-muted-foreground">
                  Showing {safePage * AGING_PAGE_SIZE + 1}–{Math.min((safePage + 1) * AGING_PAGE_SIZE, items.length)} of{" "}
                  {items.length}
                </span>
                <div className="flex items-center gap-2">
                  <Button variant="outline" size="sm" disabled={safePage === 0} onClick={() => setPage(safePage - 1)}>
                    Previous
                  </Button>
                  <span className="text-muted-foreground">
                    Page {safePage + 1} of {pageCount}
                  </span>
                  <Button
                    variant="outline"
                    size="sm"
                    disabled={safePage >= pageCount - 1}
                    onClick={() => setPage(safePage + 1)}
                  >
                    Next
                  </Button>
                </div>
              </div>
            )}
            {data.truncated && (
              <p className="text-muted-foreground text-xs">
                Showing the worst {data.items.length}; narrow by adding fewer classes to the catalog or tightening the
                window.
              </p>
            )}
          </div>
        )}
      </CardContent>
    </Card>
  )
}

function ExpectancyDialog({
  featureClass,
  existing,
  onClose,
}: {
  featureClass: string
  existing: LifeExpectancy | null
  onClose: () => void
}) {
  const isEdit = existing !== null
  const { data: schema } = useFeatureClassSchema(featureClass)
  const [subtypeCode, setSubtypeCode] = useState<string>(
    existing?.subtype_code != null ? String(existing.subtype_code) : "",
  )
  const [expectedYears, setExpectedYears] = useState(String(existing?.expected_years ?? ""))
  const [notes, setNotes] = useState(existing?.notes ?? "")

  const create = useCreateLifeExpectancy()
  const update = useUpdateLifeExpectancy()
  const saving = create.isPending || update.isPending
  const years = Number(expectedYears)
  const canSave = years > 0 && Number.isFinite(years) && !saving

  function handleSave() {
    const body = { expected_years: Math.round(years), notes: notes.trim() || null }
    if (isEdit) {
      update.mutate(
        { id: existing.id, body },
        { onSuccess: onClose, onError: (err) => toastError(err, "Could not update the entry.") },
      )
    } else {
      create.mutate(
        { ...body, feature_class_code: featureClass, subtype_code: subtypeCode === "" ? null : Number(subtypeCode) },
        { onSuccess: onClose, onError: (err) => toastError(err, "Could not create the entry.") },
      )
    }
  }

  return (
    <FormDialog
      open
      onOpenChange={(next) => next || onClose()}
      title={isEdit ? "Edit expected life" : "New expected-life entry"}
      description={`How long a ${featureClass}${isEdit ? "" : " (or one subtype of it)"} is expected to last in service. The aging report measures each feature's age against this.`}
      onSubmit={handleSave}
      submitLabel={isEdit ? "Save" : "Create"}
      canSubmit={canSave}
      saving={saving}
    >
      <div className="flex flex-col gap-1">
        <label className={labelClass}>Applies to</label>
        <select
          className={inputClass}
          value={subtypeCode}
          onChange={(e) => setSubtypeCode(e.target.value)}
          disabled={isEdit}
        >
          <option value="">Class default (every subtype)</option>
          {(schema?.subtypes ?? []).map((s) => (
            <option key={s.code} value={s.code}>
              Only subtype: {s.name}
            </option>
          ))}
        </select>
        {isEdit && <span className="text-muted-foreground text-xs">Fixed once created.</span>}
      </div>

      <div className="flex flex-col gap-1">
        <label className={labelClass}>
          Expected useful life (years) <span className="text-destructive">*</span>
        </label>
        <input
          className={cn(inputClass, "w-36")}
          type="number"
          min={1}
          value={expectedYears}
          onChange={(e) => setExpectedYears(e.target.value)}
          placeholder="e.g. 40"
        />
      </div>

      <div className="flex flex-col gap-1">
        <label className={labelClass}>Notes (optional)</label>
        <input className={inputClass} value={notes} onChange={(e) => setNotes(e.target.value)} />
      </div>
    </FormDialog>
  )
}

/**
 * Admin page for the asset-lifecycle aging engine (api/internal/assetlife): the
 * aging report (remaining useful life of in-service assets) on top, and the
 * expected-useful-life catalog it reads below. Editing the catalog needs
 * metadata:write; the report reads the lifecycle_status / commissioned_at system
 * columns (set from the feature panel's Lifecycle control).
 */
const LIFECYCLE_LABELS: Record<string, string> = {
  PLANNED: "Planned",
  UNDER_CONSTRUCTION: "Under construction",
  IN_SERVICE: "In service",
  DERATED: "Derated",
  RETIRED: "Retired",
  REMOVED: "Removed",
}
const LIFECYCLE_ORDER = ["PLANNED", "UNDER_CONSTRUCTION", "IN_SERVICE", "DERATED", "RETIRED", "REMOVED"]

function lifecycleLabel(s: string | null): string {
  if (!s) return "—"
  return LIFECYCLE_LABELS[s] ?? s
}

/** The network-wide lifecycle-transition ledger: every status change, newest first,
 * filterable by the resulting status — the "what got retired / commissioned this
 * quarter" view. Backed by ops.lifecycle_events (migration 0184). */
function LifecycleEventsCard({ classNameByCode }: { classNameByCode: Record<string, string> }) {
  const [toStatus, setToStatus] = useState("")
  const events = useLifecycleEvents({ toStatus: toStatus || undefined })
  const rows = events.data?.events ?? []

  return (
    <Card>
      <CardHeader className="flex flex-row items-end justify-between gap-2">
        <div className="flex flex-col gap-1">
          <CardTitle className="text-base">Lifecycle events</CardTitle>
          <CardDescription>
            Every lifecycle transition across the network, newest first — what got planned, commissioned, derated or
            retired, and by whom.
          </CardDescription>
        </div>
        <div className="flex flex-col gap-1">
          <label className={labelClass}>To status</label>
          <select className={cn(inputClass, "w-44")} value={toStatus} onChange={(e) => setToStatus(e.target.value)}>
            <option value="">Any status</option>
            {LIFECYCLE_ORDER.map((s) => (
              <option key={s} value={s}>
                {LIFECYCLE_LABELS[s]}
              </option>
            ))}
          </select>
        </div>
      </CardHeader>
      <CardContent>
        {events.isPending ? (
          <div className="text-muted-foreground flex items-center gap-2 text-sm">
            <Loader2 className="size-4 animate-spin" /> Loading…
          </div>
        ) : rows.length === 0 ? (
          <p className="text-muted-foreground text-sm">
            No lifecycle transitions recorded{toStatus ? ` to ${LIFECYCLE_LABELS[toStatus]}` : ""} yet. Change a
            feature's lifecycle from its panel to populate this.
          </p>
        ) : (
          <div className="max-h-96 overflow-auto rounded-md border">
            <table className="w-full border-collapse text-sm">
              <thead className="bg-muted/50 text-muted-foreground sticky top-0 z-10 text-xs">
                <tr>
                  <th className="px-2 py-1.5 text-left font-medium">When</th>
                  <th className="px-2 py-1.5 text-left font-medium">Asset</th>
                  <th className="px-2 py-1.5 text-left font-medium">Transition</th>
                  <th className="px-2 py-1.5 text-left font-medium">By</th>
                </tr>
              </thead>
              <tbody>
                {rows.map((e) => (
                  <tr key={e.id} className="border-t">
                    <td className="px-2 py-1.5 whitespace-nowrap">{new Date(e.occurred_at).toLocaleString()}</td>
                    <td className="px-2 py-1.5">
                      <span>{classNameByCode[e.feature_class_code] ?? e.feature_class_code}</span>{" "}
                      <span className="text-muted-foreground font-mono text-xs">{e.feature_id.slice(0, 8)}</span>
                    </td>
                    <td className="px-2 py-1.5">
                      <span className="text-muted-foreground">{lifecycleLabel(e.from_status)}</span>
                      {" → "}
                      <span className="font-medium">{lifecycleLabel(e.to_status)}</span>
                    </td>
                    <td className="text-muted-foreground px-2 py-1.5">{e.actor_name ?? "—"}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </CardContent>
    </Card>
  )
}

export function AdminAssetLifePage() {
  const { data: classesData } = useFeatureClasses()
  const classes = useMemo(
    () => [...(classesData?.feature_classes ?? [])].sort((a, b) => a.name.localeCompare(b.name)),
    [classesData],
  )
  const classNameByCode = useMemo(
    () => Object.fromEntries(classes.map((c) => [c.code, c.name])),
    [classes],
  )
  const [featureClass, setFeatureClass] = useState("")
  const catalog = useLifeExpectancies(featureClass || undefined)
  const { data: schema } = useFeatureClassSchema(featureClass || undefined)
  const [dialog, setDialog] = useState<LifeExpectancy | "new" | null>(null)
  const [deleting, setDeleting] = useState<LifeExpectancy | null>(null)
  const del = useDeleteLifeExpectancy()

  const rows = catalog.data?.expectancies ?? []
  const subtypeName = (code: number | null) =>
    code == null ? "Class default" : (schema?.subtypes.find((s) => s.code === code)?.name ?? `#${code}`)

  return (
    <div className="flex h-full w-full flex-col gap-4 overflow-y-auto p-4">
      <div>
        <h1 className="flex items-center gap-2 text-lg font-semibold">
          <CalendarClock className="size-5" /> Asset lifecycle
        </h1>
        <p className="text-muted-foreground text-sm">
          Track how close each in-service asset is to the end of its expected life, and plan replacements. The report
          reads each feature's lifecycle status and commission date; the catalog says how long each class should
          last.
        </p>
      </div>

      <Card>
        <CardHeader className="flex flex-row items-end justify-between gap-2">
          <div className="flex flex-col gap-1">
            <CardTitle className="text-base">Expected-life catalog</CardTitle>
            <CardDescription>Choose a class to set how many years it (or a subtype) lasts in service.</CardDescription>
          </div>
          <div className="flex items-end gap-2">
            <select
              className={cn(inputClass, "w-64")}
              value={featureClass}
              onChange={(e) => setFeatureClass(e.target.value)}
            >
              <option value="">Select a class…</option>
              {classes.map((c) => (
                <option key={c.code} value={c.code}>
                  {c.name}
                </option>
              ))}
            </select>
            {featureClass && (
              <Button size="sm" onClick={() => setDialog("new")}>
                <Plus className="size-4" /> Add entry
              </Button>
            )}
          </div>
        </CardHeader>
        {featureClass && (
          <CardContent>
            {catalog.isPending ? (
              <div className="text-muted-foreground flex items-center gap-2 text-sm">
                <Loader2 className="size-4 animate-spin" /> Loading…
              </div>
            ) : rows.length === 0 ? (
              <p className="text-muted-foreground text-sm">
                No expected-life entry for this class yet. Add a class default, and optionally per-subtype overrides.
              </p>
            ) : (
              <div className="overflow-hidden rounded-md border">
                <table className="w-full border-collapse text-sm">
                  <thead className="bg-muted/50 text-muted-foreground text-xs">
                    <tr>
                      <th className="px-2 py-1.5 text-left font-medium">Applies to</th>
                      <th className="px-2 py-1.5 text-right font-medium">Expected (yr)</th>
                      <th className="px-2 py-1.5 text-left font-medium">Notes</th>
                      <th className="px-2 py-1.5"></th>
                    </tr>
                  </thead>
                  <tbody>
                    {rows.map((row) => (
                      <tr key={row.id} className="border-t">
                        <td className="px-2 py-1.5">{subtypeName(row.subtype_code)}</td>
                        <td className="px-2 py-1.5 text-right tabular-nums">{row.expected_years}</td>
                        <td className="text-muted-foreground px-2 py-1.5 text-xs">{row.notes ?? "—"}</td>
                        <td className="px-2 py-1.5">
                          <div className="flex justify-end gap-1">
                            <Button variant="ghost" size="icon" className="size-7" onClick={() => setDialog(row)}>
                              <Pencil className="size-3.5" />
                            </Button>
                            <Button variant="ghost" size="icon" className="size-7" onClick={() => setDeleting(row)}>
                              <Trash2 className="size-3.5" />
                            </Button>
                          </div>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </CardContent>
        )}
      </Card>

      <AgingReportCard classNameByCode={classNameByCode} />

      <LifecycleEventsCard classNameByCode={classNameByCode} />

      {dialog && featureClass && (
        <ExpectancyDialog
          featureClass={featureClass}
          existing={dialog === "new" ? null : dialog}
          onClose={() => setDialog(null)}
        />
      )}

      <ConfirmDeleteDialog
        open={deleting !== null}
        title="Delete this expected-life entry?"
        description="The aging report stops assessing features covered only by this entry."
        confirmLabel="Delete"
        onOpenChange={(open) => !open && setDeleting(null)}
        onConfirm={async () => {
          if (!deleting) return
          try {
            await del.mutateAsync(deleting.id)
            setDeleting(null)
          } catch (err) {
            toastError(err, "Could not delete the entry.")
          }
        }}
      />
    </div>
  )
}
