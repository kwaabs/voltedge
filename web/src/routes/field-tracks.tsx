import { useEffect, useMemo, useRef, useState } from "react"
import { useNavigate } from "react-router"
import {
  Check,
  ChevronsUpDown,
  Eye,
  EyeOff,
  ExternalLink,
  Loader2,
  MapPin,
  Route as RouteIcon,
  Search,
  Trash2,
  X,
} from "lucide-react"

import { inputClass } from "@/components/map/field-utils"
import { FieldTrackMap, kindLabel, TRAIL_COLORS } from "@/components/map/field-track-map"
import { FieldTrackTimeline, type TimelineTick } from "@/components/map/field-track-timeline"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { useFeature } from "@/hooks/use-feature"
import {
  type TrackSession,
  type TrackWaypoint,
  type TrackWorker,
  useTrackSessions,
  useTrackWorkers,
  useTrails,
} from "@/hooks/use-field-tracks"
import { usePermission } from "@/hooks/use-auth"
import { useFieldTrackingPolicy } from "@/hooks/use-field-tracking-policy"
import { buildTrailTimeModel } from "@/lib/field-trail-timeline"
import { cn } from "@/lib/utils"
import { useSelectionStore } from "@/stores/selection-store"

/** The most trails the map will overlay at once — beyond this the picture turns to
 * spaghetti and the colour palette runs out. */
const MAX_TRAILS = 7

/** A trail the supervisor has chosen to overlay (a session, plus enough to label it
 * in the legend without re-reading the worker list). */
interface ShownTrail {
  sessionId: string
  userId: string
  workerLabel: string
  startedAt: string
}

function fmtWhen(iso: string): string {
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? "—" : d.toLocaleString()
}

function fmtDuration(from: string, to: string): string {
  const ms = new Date(to).getTime() - new Date(from).getTime()
  if (!Number.isFinite(ms) || ms < 0) return "—"
  const mins = Math.round(ms / 60000)
  if (mins < 60) return `${mins} min`
  const h = Math.floor(mins / 60)
  return `${h}h ${mins % 60}m`
}


function workerLabel(w: TrackWorker): string {
  return w.display_name ?? w.email ?? w.user_id
}

/** Supervisor field-trail audit map (migration 0178): pick field workers → overlay
 * one or more of their sessions (up to 7, each its own colour) and see where they
 * went and where they made each edit. Clicking a pin opens that edit's details here
 * (the feature it changed, with its attributes); jumping to the main geographic map
 * is a separate action. Gated by field:tracks:read. */
/** Read-only state of the org-wide field-tracking switch, shown where trails are
 * reviewed so a supervisor knows whether what they're looking at reflects current
 * recording. The switch itself is set by a field admin on Admin → Field packs. Off
 * stops the trail AND the located edits, device-side and as a server backstop. */
function TrackingPolicyBanner() {
  const { data } = useFieldTrackingPolicy()
  const canManage = usePermission()("admin:field-packs")
  const enabled = data?.enabled ?? true

  return (
    <Card className="shrink-0">
      <CardContent className="flex items-center justify-between gap-3 p-3">
        <div className="flex items-center gap-2">
          {enabled ? (
            <Eye className="size-4 shrink-0 text-emerald-600" />
          ) : (
            <EyeOff className="text-muted-foreground size-4 shrink-0" />
          )}
          <div>
            <p className="text-sm font-medium">Field location tracking {enabled ? "on" : "off"}</p>
            <p className="text-muted-foreground text-xs">
              {enabled
                ? "Crews' movement trails and the location of each edit are recorded; the field app discloses this to crews on their device."
                : "No field location is recorded — the movement trail and located edits are both off, device-side and on the server."}
            </p>
          </div>
        </div>
        {canManage && (
          <span className="text-muted-foreground shrink-0 text-xs">Manage in Admin → Field packs</span>
        )}
      </CardContent>
    </Card>
  )
}

export function FieldTracksPage() {
  const navigate = useNavigate()
  const workers = useTrackWorkers()

  const [activeWorkerId, setActiveWorkerId] = useState<string | null>(null)
  const [from, setFrom] = useState("")
  const [to, setTo] = useState("")
  const [shown, setShown] = useState<ShownTrail[]>([])
  const [activeOp, setActiveOp] = useState<string | null>(null)

  const workerRows = useMemo(() => workers.data?.workers ?? [], [workers.data])
  const sessions = useTrackSessions({ userId: activeWorkerId, from: from || undefined, to: to || undefined })
  const sessionRows = sessions.data?.sessions ?? []

  // Load every shown trail; results are index-aligned to `shown`.
  const trailResults = useTrails(shown.map((s) => s.sessionId))
  const trails = useMemo(
    () =>
      shown.map((s, i) => ({
        sessionId: s.sessionId,
        color: TRAIL_COLORS[i % TRAIL_COLORS.length],
        trail: trailResults[i]?.data ?? null,
      })),
    [shown, trailResults],
  )
  const anyTrailLoading = trailResults.some((r) => r.isPending)

  const allWaypoints = useMemo(() => trailResults.flatMap((r) => r.data?.waypoints ?? []), [trailResults])
  const activeWaypoint = useMemo(() => allWaypoints.find((w) => w.op_id === activeOp) ?? null, [allWaypoints, activeOp])

  // Drop the open edit if its trail was removed from the overlay.
  useEffect(() => {
    if (activeOp && !allWaypoints.some((w) => w.op_id === activeOp)) setActiveOp(null)
  }, [allWaypoints, activeOp])

  const atCap = shown.length >= MAX_TRAILS

  // --- Playback timeline (real-time clock with idle gaps collapsed) ---
  const [playing, setPlaying] = useState(false)
  const [speed, setSpeed] = useState(60)
  const [playheadComp, setPlayheadComp] = useState(0)

  const instants = useMemo(() => {
    const out: number[] = []
    for (const r of trailResults) {
      if (!r.data) continue
      for (const p of r.data.points) out.push(Date.parse(p.recorded_at))
      for (const w of r.data.waypoints) out.push(Date.parse(w.recorded_at))
    }
    return out
  }, [trailResults])
  const timeModel = useMemo(() => buildTrailTimeModel(instants), [instants])
  const spanKey = timeModel ? `${timeModel.realStart}-${timeModel.realEnd}-${timeModel.totalComp}` : "none"

  // When the shown trails change, reset to "fully revealed" (playhead at the end),
  // so the default view is the whole trail, not a paused mid-playback frame.
  useEffect(() => {
    setPlaying(false)
    setPlayheadComp(timeModel ? timeModel.totalComp : 0)
  }, [spanKey]) // eslint-disable-line react-hooks/exhaustive-deps

  // The animation loop: advance the compressed playhead by speed× real time.
  useEffect(() => {
    if (!playing || !timeModel) return
    let raf = 0
    let last = performance.now()
    const tick = (now: number) => {
      const dt = (now - last) / 1000
      last = now
      setPlayheadComp((prev) => Math.min(timeModel.totalComp, prev + dt * speed * 1000))
      raf = requestAnimationFrame(tick)
    }
    raf = requestAnimationFrame(tick)
    return () => cancelAnimationFrame(raf)
  }, [playing, speed, timeModel])

  // Stop at the end.
  useEffect(() => {
    if (playing && timeModel && playheadComp >= timeModel.totalComp) setPlaying(false)
  }, [playheadComp, playing, timeModel])

  function handlePlayPause() {
    if (!timeModel) return
    if (playing) {
      setPlaying(false)
      return
    }
    if (playheadComp >= timeModel.totalComp) setPlayheadComp(0) // restart from the top
    setPlaying(true)
  }

  const playheadRealMs = timeModel ? timeModel.compToReal(playheadComp) : null

  const ticks = useMemo<TimelineTick[]>(() => {
    if (!timeModel || timeModel.totalComp <= 0) return []
    const out: TimelineTick[] = []
    for (const tr of trails) {
      for (const w of tr.trail?.waypoints ?? []) {
        out.push({ position: timeModel.realToComp(Date.parse(w.recorded_at)) / timeModel.totalComp, color: tr.color })
      }
    }
    return out
  }, [trails, timeModel])

  function toggleTrail(session: TrackSession) {
    setShown((prev) => {
      if (prev.some((t) => t.sessionId === session.session_id)) {
        return prev.filter((t) => t.sessionId !== session.session_id)
      }
      if (prev.length >= MAX_TRAILS) return prev
      const w = workerRows.find((x) => x.user_id === session.user_id)
      return [
        ...prev,
        {
          sessionId: session.session_id,
          userId: session.user_id,
          workerLabel: w ? workerLabel(w) : session.user_id,
          startedAt: session.started_at,
        },
      ]
    })
  }

  function removeTrail(sessionId: string) {
    setShown((prev) => prev.filter((t) => t.sessionId !== sessionId))
  }

  // Jump to the main geographic map focused on the edited feature — the explicit
  // opt-in, NOT what a plain pin click does.
  function showOnMainMap(w: TrackWaypoint) {
    if (!w.feature_class || !w.feature_id) return
    useSelectionStore.getState().select(w.feature_class, w.feature_id, { focus: true })
    navigate("/")
  }

  const today = new Date().toISOString().slice(0, 10)

  return (
    <div className="flex h-full flex-col gap-4 overflow-y-auto p-4 lg:overflow-hidden">
      <div className="shrink-0">
        <h1 className="flex items-center gap-2 text-lg font-semibold">
          <RouteIcon className="size-5" />
          Field trails
        </h1>
        <p className="text-muted-foreground text-sm">
          Where field workers went and where they made each edit — pick workers and sessions to overlay how the field
          work was done (up to {MAX_TRAILS} trails at once).
        </p>
      </div>

      <TrackingPolicyBanner />

      {/* Worker search + date filters */}
      <Card className="shrink-0">
        <CardContent className="flex flex-wrap items-end gap-3 p-3">
          <div className="flex flex-col gap-1">
            <label className="text-xs font-medium">Worker</label>
            <WorkerPicker
              workers={workerRows}
              loading={workers.isPending}
              activeWorkerId={activeWorkerId}
              onPick={setActiveWorkerId}
            />
          </div>
          <div className="flex flex-col gap-1">
            <label className="text-xs font-medium">From</label>
            <input
              type="date"
              className={cn(inputClass, "w-40")}
              value={from}
              max={to || today}
              onChange={(e) => setFrom(e.target.value)}
            />
          </div>
          <div className="flex flex-col gap-1">
            <label className="text-xs font-medium">To</label>
            <input
              type="date"
              className={cn(inputClass, "w-40")}
              value={to}
              min={from || undefined}
              max={today}
              onChange={(e) => setTo(e.target.value)}
            />
          </div>
        </CardContent>
      </Card>

      {/* Sessions + legend · map [· detail] */}
      <div
        className={cn(
          "grid gap-4 lg:min-h-0 lg:flex-1",
          activeWaypoint ? "lg:grid-cols-[16rem_1fr_23rem]" : "lg:grid-cols-[16rem_1fr]",
        )}
      >
        {/* Left: this worker's sessions (toggle to overlay) + the shown-trails legend */}
        <div className="flex min-h-0 flex-col gap-4">
          <Card className="flex min-h-0 flex-1 flex-col">
            <CardHeader className="pb-2">
              <CardTitle className="text-sm">Sessions</CardTitle>
              <CardDescription>
                {activeWorkerId == null
                  ? "Pick a worker."
                  : `${sessionRows.length} session${sessionRows.length === 1 ? "" : "s"} — click to overlay`}
              </CardDescription>
            </CardHeader>
            <CardContent className="min-h-0 flex-1 overflow-y-auto pt-0">
              {atCap && (
                <p className="text-muted-foreground mb-2 text-xs">
                  Showing {MAX_TRAILS}/{MAX_TRAILS} — remove one to add another.
                </p>
              )}
              {sessions.isPending && activeWorkerId != null ? (
                <div className="text-muted-foreground flex items-center gap-2 text-sm">
                  <Loader2 className="size-4 animate-spin" />
                  Loading…
                </div>
              ) : activeWorkerId != null && sessionRows.length === 0 ? (
                <p className="text-muted-foreground text-sm">No sessions in this window.</p>
              ) : (
                <div className="flex flex-col gap-1">
                  {sessionRows.map((s) => {
                    const idx = shown.findIndex((t) => t.sessionId === s.session_id)
                    return (
                      <SessionRow
                        key={s.session_id}
                        session={s}
                        color={idx >= 0 ? TRAIL_COLORS[idx % TRAIL_COLORS.length] : null}
                        disabled={idx < 0 && atCap}
                        onClick={() => toggleTrail(s)}
                      />
                    )
                  })}
                </div>
              )}
            </CardContent>
          </Card>

          {shown.length > 0 && (
            <Card className="shrink-0">
              <CardHeader className="flex-row items-center justify-between gap-2 space-y-0 pb-2">
                <CardTitle className="text-sm">
                  Shown trails{" "}
                  <span className="text-muted-foreground font-normal">
                    ({shown.length}/{MAX_TRAILS})
                  </span>
                </CardTitle>
                <button
                  type="button"
                  onClick={() => setShown([])}
                  className="text-muted-foreground hover:text-foreground flex items-center gap-1 text-xs"
                >
                  <Trash2 className="size-3.5" />
                  Clear
                </button>
              </CardHeader>
              <CardContent className="max-h-48 overflow-y-auto pt-0">
                <div className="flex flex-col gap-1">
                  {shown.map((t, i) => (
                    <div key={t.sessionId} className="flex items-center gap-2 text-xs">
                      <span
                        className="size-3 shrink-0 rounded-full"
                        style={{ backgroundColor: TRAIL_COLORS[i % TRAIL_COLORS.length] }}
                      />
                      <span className="min-w-0 flex-1 truncate">
                        <span className="font-medium">{t.workerLabel}</span>
                        <span className="text-muted-foreground"> · {fmtWhen(t.startedAt)}</span>
                      </span>
                      <button
                        type="button"
                        onClick={() => removeTrail(t.sessionId)}
                        className="text-muted-foreground hover:text-foreground shrink-0"
                        title="Remove trail"
                      >
                        <X className="size-3.5" />
                      </button>
                    </div>
                  ))}
                </div>
              </CardContent>
            </Card>
          )}
        </div>

        {/* The trail map (hero) with the playback timeline beneath it; spans the
            detail column until an edit is clicked. */}
        <div className="flex h-[34rem] flex-col gap-3 lg:h-auto">
          <div className="relative min-h-0 flex-1">
            <FieldTrackMap
              trails={trails}
              selectedOpId={activeOp}
              playheadRealMs={playheadRealMs}
              onWaypointClick={(w) => setActiveOp(w.op_id)}
            />
            {anyTrailLoading && (
              <div className="bg-background/80 absolute left-2 top-2 z-10 flex items-center gap-1.5 rounded-md border px-2 py-1 text-xs shadow-sm">
                <Loader2 className="size-3.5 animate-spin" />
                Loading trails…
              </div>
            )}
          </div>
          {timeModel && (
            <div className="shrink-0">
              <FieldTrackTimeline
                model={timeModel}
                playheadComp={playheadComp}
                playing={playing}
                speed={speed}
                ticks={ticks}
                onScrub={(c) => {
                  setPlaying(false)
                  setPlayheadComp(c)
                }}
                onPlayPause={handlePlayPause}
                onRestart={() => {
                  setPlaying(false)
                  setPlayheadComp(0)
                }}
                onSpeed={setSpeed}
              />
            </div>
          )}
        </div>

        {/* Detail pane — only mounts once an edit pin is clicked on the map */}
        {activeWaypoint && (
          <Card className="flex min-h-0 flex-col">
            <WaypointDetail waypoint={activeWaypoint} onClose={() => setActiveOp(null)} onShowOnMainMap={showOnMainMap} />
          </Card>
        )}
      </div>
    </div>
  )
}

/** A searchable worker picker — a plain <select> doesn't scale to hundreds of field
 * workers, so this filters as you type. */
function WorkerPicker({
  workers,
  loading,
  activeWorkerId,
  onPick,
}: {
  workers: TrackWorker[]
  loading: boolean
  activeWorkerId: string | null
  onPick: (id: string) => void
}) {
  const [open, setOpen] = useState(false)
  const [q, setQ] = useState("")
  const inputRef = useRef<HTMLInputElement | null>(null)

  const active = workers.find((w) => w.user_id === activeWorkerId)
  const filtered = useMemo(() => {
    const needle = q.trim().toLowerCase()
    if (!needle) return workers
    return workers.filter((w) => workerLabel(w).toLowerCase().includes(needle) || (w.email ?? "").toLowerCase().includes(needle))
  }, [workers, q])

  useEffect(() => {
    if (open) inputRef.current?.focus()
  }, [open])

  return (
    <div className="relative">
      <button
        type="button"
        onClick={() => setOpen((o) => !o)}
        className={cn(inputClass, "flex w-64 items-center justify-between gap-2")}
      >
        <span className={cn("truncate", !active && "text-muted-foreground")}>
          {active ? `${workerLabel(active)} · ${active.session_days} day${active.session_days === 1 ? "" : "s"}` : "Select a worker…"}
        </span>
        {loading ? <Loader2 className="size-4 shrink-0 animate-spin" /> : <ChevronsUpDown className="text-muted-foreground size-4 shrink-0" />}
      </button>

      {open && (
        <>
          <div className="fixed inset-0 z-10" onClick={() => setOpen(false)} />
          <div className="bg-popover absolute z-20 mt-1 w-64 overflow-hidden rounded-md border shadow-md">
            <div className="flex items-center gap-2 border-b px-2">
              <Search className="text-muted-foreground size-3.5 shrink-0" />
              <input
                ref={inputRef}
                value={q}
                onChange={(e) => setQ(e.target.value)}
                placeholder="Search workers…"
                className="w-full bg-transparent py-2 text-sm outline-none"
              />
            </div>
            <div className="max-h-64 overflow-y-auto py-1">
              {filtered.length === 0 ? (
                <div className="text-muted-foreground px-3 py-2 text-sm">No matching worker.</div>
              ) : (
                filtered.map((w) => (
                  <button
                    key={w.user_id}
                    type="button"
                    onClick={() => {
                      onPick(w.user_id)
                      setOpen(false)
                      setQ("")
                    }}
                    className="hover:bg-muted/60 flex w-full items-center gap-2 px-3 py-1.5 text-left text-sm"
                  >
                    <Check className={cn("size-3.5 shrink-0", w.user_id === activeWorkerId ? "opacity-100" : "opacity-0")} />
                    <span className="min-w-0 flex-1 truncate">
                      {workerLabel(w)}
                      <span className="text-muted-foreground"> · {w.session_days}d</span>
                    </span>
                  </button>
                ))
              )}
            </div>
          </div>
        </>
      )}
    </div>
  )
}

/** The selected edit's details: when/where it happened and, if it changed a
 * feature, that feature's live attributes — with the explicit "Show on main map"
 * action. This is what a pin click opens, in place of navigating away. */
function WaypointDetail({
  waypoint,
  onClose,
  onShowOnMainMap,
}: {
  waypoint: TrackWaypoint
  onClose: () => void
  onShowOnMainMap: (w: TrackWaypoint) => void
}) {
  const feature = useFeature(waypoint.feature_class ?? undefined, waypoint.feature_id ?? undefined)
  const hasFeature = Boolean(waypoint.feature_class && waypoint.feature_id)

  const attrRows = useMemo(() => {
    const a = feature.data?.attributes ?? {}
    return Object.entries(a).filter(([, v]) => v !== null && v !== undefined && v !== "")
  }, [feature.data])

  return (
    <>
      <CardHeader className="pb-2">
        <button
          type="button"
          onClick={onClose}
          className="text-muted-foreground hover:text-foreground -ml-1 mb-1 flex items-center gap-1 text-xs"
        >
          <X className="size-3.5" />
          Close
        </button>
        <CardTitle className="flex items-center gap-2 text-sm">
          <MapPin className={cn("size-4", waypoint.kind === "delete" ? "text-red-600" : "text-amber-600")} />
          <span>{kindLabel(waypoint.kind)}</span>
        </CardTitle>
        <CardDescription>{fmtWhen(waypoint.recorded_at)}</CardDescription>
      </CardHeader>
      <CardContent className="min-h-0 flex-1 space-y-4 overflow-y-auto pt-0 text-sm">
        {/* Where the worker was when the edit was made */}
        <div className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 text-xs">
          <span className="text-muted-foreground">Location</span>
          <span className="tabular-nums">
            {waypoint.latitude.toFixed(6)}, {waypoint.longitude.toFixed(6)}
          </span>
          {waypoint.accuracy_m != null && (
            <>
              <span className="text-muted-foreground">GPS accuracy</span>
              <span className="tabular-nums">±{waypoint.accuracy_m.toFixed(1)} m</span>
            </>
          )}
        </div>

        {!hasFeature ? (
          <p className="text-muted-foreground text-xs">A graph operation — no single feature is attached to this edit.</p>
        ) : feature.isPending ? (
          <div className="text-muted-foreground flex items-center gap-2 text-xs">
            <Loader2 className="size-3.5 animate-spin" />
            Loading feature…
          </div>
        ) : feature.isError || !feature.data ? (
          <p className="text-muted-foreground text-xs">
            The edited feature ({waypoint.feature_class} {waypoint.feature_id}) could not be loaded — it may have been
            deleted since.
          </p>
        ) : (
          <div className="space-y-3">
            <div>
              <div className="font-medium">{feature.data.name ?? "(unnamed)"}</div>
              <div className="text-muted-foreground text-xs">
                {waypoint.feature_class} · {feature.data.status}
                {feature.data.lifecycle_status ? ` · ${feature.data.lifecycle_status}` : ""}
              </div>
            </div>

            {attrRows.length > 0 && (
              <div>
                <div className="text-muted-foreground mb-1 text-[11px] font-semibold uppercase tracking-wide">
                  Attributes
                </div>
                <div className="divide-y rounded-md border">
                  {attrRows.map(([k, v]) => (
                    <div key={k} className="grid grid-cols-[1fr_1.2fr] gap-2 px-2 py-1 text-xs">
                      <span className="text-muted-foreground capitalize">{k.replace(/_/g, " ")}</span>
                      <span className="break-words">{typeof v === "object" ? JSON.stringify(v) : String(v)}</span>
                    </div>
                  ))}
                </div>
              </div>
            )}

            <Button variant="outline" size="sm" className="w-full" onClick={() => onShowOnMainMap(waypoint)}>
              <ExternalLink className="mr-1.5 size-3.5" />
              Show on main map
            </Button>
          </div>
        )}
      </CardContent>
    </>
  )
}

function SessionRow({
  session,
  color,
  disabled,
  onClick,
}: {
  session: TrackSession
  /** The trail colour when this session is overlaid, else null. */
  color: string | null
  disabled: boolean
  onClick: () => void
}) {
  const selected = color != null
  return (
    <button
      type="button"
      onClick={onClick}
      disabled={disabled}
      className={cn(
        "flex items-center gap-2 rounded-md border px-2 py-1.5 text-left text-sm",
        selected ? "border-primary/60 bg-primary/5" : "border-border/60 hover:bg-muted/40",
        disabled && "cursor-not-allowed opacity-40",
      )}
    >
      <span
        className="flex size-4 shrink-0 items-center justify-center rounded-sm border"
        style={selected ? { backgroundColor: color!, borderColor: color! } : undefined}
      >
        {selected && <Check className="size-3 text-white" />}
      </span>
      <span className="min-w-0 flex-1">
        <span className="block font-medium tabular-nums">{fmtWhen(session.started_at)}</span>
        <span className="text-muted-foreground text-xs">
          {fmtDuration(session.started_at, session.ended_at)} · {session.point_count} points · {session.action_count} edit
          {session.action_count === 1 ? "" : "s"}
        </span>
      </span>
    </button>
  )
}
