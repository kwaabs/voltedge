import { useMemo, useState } from "react"
import { Loader2, MapPin, TreePine } from "lucide-react"

import { usePermission } from "@/hooks/use-auth"
import { useFeature } from "@/hooks/use-feature"
import {
  type SpatialMatch,
  type SpatialMode,
  useRaiseTrimOrder,
  useSpatialRelated,
  useVegetationEncroachments,
} from "@/hooks/use-spatial"
import { ApiError } from "@/lib/api"
import { cn } from "@/lib/utils"
import { useSelectionStore } from "@/stores/selection-store"
import { useToastStore } from "@/stores/toast-store"

// The map attribute panel's "Spatial" tab (api/internal/spatial): the features
// spatially related to the selected one, derived from the PostGIS geometry — what
// OVERLAPS it, what it sits INSIDE, what land COVERS it, and what's NEARBY. The
// companion to the land classes (easement, parcel, corridor, vegetation): select
// an easement → the assets inside it; select a pole → the easements/parcels over
// it. Read-only; nothing needs to be wired first.

const MODES: { value: SpatialMode; label: string; hint: string }[] = [
  { value: "intersects", label: "Overlapping", hint: "anything whose shape overlaps this feature" },
  { value: "contains", label: "Inside this", hint: "features this one fully contains (e.g. assets inside an easement)" },
  { value: "contained_by", label: "Covering this", hint: "polygons that contain this feature (e.g. the land over a pole)" },
  { value: "within", label: "Nearby", hint: "features within a distance of this one" },
]

// Short/known acronyms kept all-caps when humanizing a class code (MV_CONDUCTOR
// → "MV Conductor", not "Mv conductor").
const CLASS_ACRONYMS = new Set([
  "MV", "LV", "HV", "EHV", "UHV", "CT", "VT", "AC", "DC", "EV", "PV", "DER",
  "RMU", "URD", "BESS", "LVAC", "OLTC", "ANSI", "GPS", "CIM", "AMI", "SCADA",
])
function humanizeClass(code: string): string {
  return code
    .split("_")
    .map((w) => (w.length <= 2 || CLASS_ACRONYMS.has(w) ? w : w.charAt(0) + w.slice(1).toLowerCase()))
    .join(" ")
}

function shortId(id: string): string {
  return id.length > 8 ? `${id.slice(0, 8)}…` : id
}

/** One result row: open the feature on click; highlight it on the map on hover. */
function MatchRow({ match }: { match: SpatialMatch }) {
  const select = useSelectionStore((s) => s.select)
  const locate = useSelectionStore((s) => s.locate)
  const clearLocated = useSelectionStore((s) => s.clearLocated)
  return (
    <li>
      <button
        type="button"
        className="hover:bg-muted/60 flex w-full items-center justify-between gap-2 px-3 py-1.5 text-left text-sm"
        onClick={() => select(match.feature_class, match.feature_id)}
        onMouseEnter={() => locate(match.feature_class, match.feature_id)}
        onMouseLeave={clearLocated}
        title="Open this feature"
      >
        <span className="truncate">
          <span className="font-medium">{humanizeClass(match.feature_class)}</span>{" "}
          <span className="text-muted-foreground">{shortId(match.feature_id)}</span>
        </span>
        {match.distance_m != null && (
          <span className="text-muted-foreground shrink-0 text-xs tabular-nums">
            {match.distance_m < 1 ? "<1" : Math.round(match.distance_m)} m
          </span>
        )}
      </button>
    </li>
  )
}

/** Vegetation clearance breaches for a conductor — the trim list, worst first,
 * with a one-click "raise trim order" (closes detect→dispatch). Shown only for
 * conductor features (the per-voltage clearance rule assumes one). */
function VegetationClearance({ featureClass, featureId }: { featureClass: string; featureId: string }) {
  const select = useSelectionStore((s) => s.select)
  const locate = useSelectionStore((s) => s.locate)
  const clearLocated = useSelectionStore((s) => s.clearLocated)
  const canWam = usePermission()("wam:write")
  const raise = useRaiseTrimOrder()
  const [raised, setRaised] = useState<Set<string>>(new Set())
  // The conductor's feeder, so the scan can widen from one line to the whole
  // feeder (C3). useFeature is already cached by the panel, so this is free.
  const feature = useFeature(featureClass, featureId)
  const circuitId = typeof feature.data?.attributes?.circuit_id === "string" ? feature.data.attributes.circuit_id : undefined
  const [feederScope, setFeederScope] = useState(false)
  const useFeeder = feederScope && !!circuitId
  const { data, isPending } = useVegetationEncroachments(useFeeder ? { circuitId } : { featureClass, featureId })
  const breaches = data?.encroachments ?? []

  return (
    <div className="rounded-md border p-2">
      <div className="mb-1 flex items-center justify-between gap-1.5 text-xs font-medium">
        <span className="flex items-center gap-1.5">
          <TreePine className="size-3.5 text-green-700 dark:text-green-400" />
          Vegetation clearance
        </span>
        {circuitId && (
          <span className="flex gap-0.5">
            <button
              type="button"
              onClick={() => setFeederScope(false)}
              className={cn("rounded px-1.5 py-0.5 text-[11px]", !useFeeder ? "bg-primary/10 text-primary" : "text-muted-foreground hover:bg-muted/60")}
            >
              This conductor
            </button>
            <button
              type="button"
              onClick={() => setFeederScope(true)}
              className={cn("rounded px-1.5 py-0.5 text-[11px]", useFeeder ? "bg-primary/10 text-primary" : "text-muted-foreground hover:bg-muted/60")}
            >
              Whole feeder
            </button>
          </span>
        )}
      </div>
      {isPending ? (
        <div className="text-muted-foreground flex items-center gap-2 text-sm">
          <Loader2 className="size-4 animate-spin" /> Checking…
        </div>
      ) : breaches.length === 0 ? (
        <p className="text-muted-foreground text-xs">
          No vegetation breaching clearance on this {useFeeder ? "feeder" : "conductor"}.
        </p>
      ) : (
        <ul className="flex flex-col gap-1">
          {breaches.map((e) => {
            const key = `${e.vegetation_class}:${e.vegetation_id}`
            const done = raised.has(key)
            return (
              <li key={key} className="flex items-start gap-1">
                <button
                  type="button"
                  className="hover:bg-muted/60 flex min-w-0 flex-1 flex-col rounded px-1.5 py-1 text-left text-xs"
                  onClick={() => select(e.vegetation_class, e.vegetation_id)}
                  onMouseEnter={() => locate(e.vegetation_class, e.vegetation_id)}
                  onMouseLeave={clearLocated}
                  title="Open this vegetation zone"
                >
                  <span className="truncate">
                    <span className="font-medium">{humanizeClass(e.vegetation_class)}</span> {shortId(e.vegetation_id)}
                    {e.species ? ` · ${e.species}` : ""}
                  </span>
                  <span className="text-muted-foreground">
                    <span className="text-destructive font-medium">{e.distance_m.toFixed(1)} m</span> away — needs{" "}
                    {e.clearance_m} m{e.voltage_class ? ` (${e.voltage_class})` : ""} · breach {e.breach_m.toFixed(1)} m
                  </span>
                  {useFeeder && (
                    <span className="text-muted-foreground truncate">
                      on {humanizeClass(e.conductor_class)} {shortId(e.conductor_id)}
                    </span>
                  )}
                </button>
                {canWam &&
                  (done ? (
                    <span className="shrink-0 px-1 py-1 text-[11px] text-green-700 dark:text-green-400">✓ order raised</span>
                  ) : (
                    <button
                      type="button"
                      className="hover:bg-muted/60 text-muted-foreground shrink-0 rounded px-1.5 py-1 text-[11px]"
                      disabled={raise.isPending}
                      title="Raise a WAM trim work order for this breach"
                      onClick={() =>
                        raise.mutate(e, {
                          onSuccess: () => setRaised((prev) => new Set(prev).add(key)),
                          onError: (err) =>
                            useToastStore
                              .getState()
                              .pushError(err instanceof ApiError ? err.message : "Could not raise the trim order."),
                        })
                      }
                    >
                      {raise.isPending ? <Loader2 className="size-3.5 animate-spin" /> : "Raise trim order"}
                    </button>
                  ))}
              </li>
            )
          })}
        </ul>
      )}
    </div>
  )
}

function daysBetween(from: Date, to: Date): number {
  return Math.round((to.getTime() - from.getTime()) / 86_400_000)
}

/** A compact due/overdue badge for a land feature, computed from its own dates
 * (Slice D): an EASEMENT's expiry, a VEGETATION_ZONE's next trim. */
function LandDueBadge({ featureClass, featureId }: { featureClass: string; featureId: string }) {
  const feature = useFeature(featureClass, featureId)
  const attrs = feature.data?.attributes
  if (!attrs) return null
  const today = new Date()
  today.setHours(0, 0, 0, 0)

  const pill = (cls: string, text: string) => (
    <div className={cn("flex items-center gap-1.5 rounded-md px-2 py-1.5 text-xs font-medium", cls)}>{text}</div>
  )

  if (featureClass === "EASEMENT") {
    const exp = typeof attrs.expiry_date === "string" ? attrs.expiry_date : null
    if (!exp) return null
    const days = daysBetween(today, new Date(exp))
    if (days < 0) return pill("bg-destructive/10 text-destructive", `Easement expired ${-days} day${days === -1 ? "" : "s"} ago (${exp})`)
    if (days <= 90) return pill("bg-amber-500/15 text-amber-700 dark:text-amber-400", `Easement expires in ${days} day${days === 1 ? "" : "s"} (${exp})`)
    return pill("bg-muted text-muted-foreground", `Easement expires ${exp}`)
  }

  if (featureClass === "VEGETATION_ZONE") {
    const last = typeof attrs.last_trimmed === "string" ? attrs.last_trimmed : null
    const cycle = Number(attrs.trim_cycle_months)
    if (!last || !cycle || Number.isNaN(cycle)) return null
    const due = new Date(last)
    due.setMonth(due.getMonth() + cycle)
    due.setHours(0, 0, 0, 0)
    const overdue = daysBetween(due, today)
    const dueStr = due.toISOString().slice(0, 10)
    if (overdue > 0) return pill("bg-destructive/10 text-destructive", `Trim overdue by ${overdue} day${overdue === 1 ? "" : "s"} (was due ${dueStr})`)
    if (overdue >= -30) return pill("bg-amber-500/15 text-amber-700 dark:text-amber-400", `Trim due in ${-overdue} day${overdue === -1 ? "" : "s"} (${dueStr})`)
    return pill("bg-muted text-muted-foreground", `Next trim ${dueStr}`)
  }
  return null
}

export function SpatialTab() {
  const selected = useSelectionStore((s) => s.selected)
  const [mode, setMode] = useState<SpatialMode>("intersects")
  const [distance, setDistance] = useState("10")

  const featureClass = selected?.featureClassCode
  const featureId = selected?.featureId
  const dist = mode === "within" ? Number(distance) || 0 : undefined
  const { data, isPending, isError } = useSpatialRelated({ featureClass, featureId, mode, distance: dist })

  // Group matches by class so a long list reads as "N land parcels, M vegetation…".
  const groups = useMemo(() => {
    const byClass = new Map<string, SpatialMatch[]>()
    for (const m of data?.matches ?? []) {
      const arr = byClass.get(m.feature_class) ?? []
      arr.push(m)
      byClass.set(m.feature_class, arr)
    }
    return [...byClass.entries()].sort((a, b) => a[0].localeCompare(b[0]))
  }, [data])

  if (!selected || !featureClass || !featureId) return null

  const total = data?.matches.length ?? 0
  const isConductor = /_CONDUCTOR$/.test(featureClass)

  return (
    <div className="flex flex-col gap-2">
      {(featureClass === "EASEMENT" || featureClass === "VEGETATION_ZONE") && (
        <LandDueBadge featureClass={featureClass} featureId={featureId} />
      )}
      {isConductor && <VegetationClearance featureClass={featureClass} featureId={featureId} />}
      <div className="flex flex-wrap gap-1">
        {MODES.map((m) => (
          <button
            key={m.value}
            type="button"
            title={m.hint}
            onClick={() => setMode(m.value)}
            className={cn(
              "rounded-sm border px-2 py-1 text-xs",
              mode === m.value ? "border-primary bg-primary/10 text-primary font-medium" : "border-input text-muted-foreground hover:bg-muted/60",
            )}
          >
            {m.label}
          </button>
        ))}
      </div>

      {mode === "within" && (
        <label className="text-muted-foreground flex items-center gap-2 text-xs">
          Within
          <input
            type="number"
            min={1}
            value={distance}
            onChange={(e) => setDistance(e.target.value)}
            className="border-input bg-background w-20 rounded-sm border px-2 py-1 text-sm"
          />
          metres
        </label>
      )}

      {isPending ? (
        <div className="text-muted-foreground flex items-center gap-2 text-sm">
          <Loader2 className="size-4 animate-spin" /> Searching…
        </div>
      ) : isError ? (
        <p className="text-destructive text-sm">Could not run the spatial query.</p>
      ) : total === 0 ? (
        <p className="text-muted-foreground text-sm">
          {MODES.find((m) => m.value === mode)?.label}: nothing found
          {mode === "within" ? ` within ${dist} m.` : "."}
        </p>
      ) : (
        <div className="flex flex-col gap-2">
          <span className="text-muted-foreground flex items-center gap-1.5 text-xs">
            <MapPin className="size-3.5" />
            {total} feature{total === 1 ? "" : "s"}
          </span>
          {groups.map(([cls, rows]) => (
            <div key={cls} className="rounded-md border">
              <div className="text-muted-foreground bg-muted/40 px-3 py-1 text-xs font-medium">
                {humanizeClass(cls)} · {rows.length}
              </div>
              <ul className="divide-y">
                {rows.map((m) => (
                  <MatchRow key={`${m.feature_class}:${m.feature_id}`} match={m} />
                ))}
              </ul>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}
