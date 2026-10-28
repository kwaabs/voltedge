import { useMemo, useState } from "react"
import { useNavigate } from "react-router"
import { Loader2, MapPin, Route as RouteIcon, Wrench } from "lucide-react"

import { inputClass } from "@/components/map/field-utils"
import { FieldTrackMap } from "@/components/map/field-track-map"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import {
  type TrackSession,
  type TrackWaypoint,
  useTrackSessions,
  useTrackWorkers,
  useTrail,
} from "@/hooks/use-field-tracks"
import { cn } from "@/lib/utils"
import { useSelectionStore } from "@/stores/selection-store"

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

/** Supervisor field-trail audit map (migration 0178): pick a field worker → one of
 * their sessions → see where they went (the breadcrumb trail) and where they made
 * each edit (the action pins). Gated at the route by field:tracks:read. */
export function FieldTracksPage() {
  const navigate = useNavigate()
  const workers = useTrackWorkers()

  const [userId, setUserId] = useState<string | null>(null)
  const [from, setFrom] = useState("")
  const [to, setTo] = useState("")
  const [sessionId, setSessionId] = useState<string | null>(null)

  const sessions = useTrackSessions({ userId, from: from || undefined, to: to || undefined })
  const trail = useTrail(sessionId)

  const workerRows = workers.data?.workers ?? []
  const sessionRows = sessions.data?.sessions ?? []
  const waypoints = useMemo(() => trail.data?.waypoints ?? [], [trail.data])

  function locate(w: TrackWaypoint) {
    if (!w.feature_class || !w.feature_id) return
    useSelectionStore.getState().select(w.feature_class, w.feature_id, { focus: true })
    navigate("/")
  }

  return (
    <div className="h-full overflow-y-auto p-4">
      <div className="mx-auto flex max-w-6xl flex-col gap-4">
        <div>
          <h1 className="flex items-center gap-2 text-lg font-semibold">
            <RouteIcon className="size-5" />
            Field trails
          </h1>
          <p className="text-muted-foreground text-sm">
            Where field workers went and where they made each edit — pick a worker and a session to map how the field
            work was done.
          </p>
        </div>

        {/* Worker + date filters */}
        <Card>
          <CardContent className="flex flex-wrap items-end gap-3 p-3">
            <div className="flex flex-col gap-1">
              <label className="text-xs font-medium">Worker</label>
              <select
                className={cn(inputClass, "w-64")}
                value={userId ?? ""}
                onChange={(e) => {
                  setUserId(e.target.value || null)
                  setSessionId(null)
                }}
              >
                <option value="">Select a worker…</option>
                {workerRows.map((w) => (
                  <option key={w.user_id} value={w.user_id}>
                    {w.display_name ?? w.email ?? w.user_id} · {w.session_days} day{w.session_days === 1 ? "" : "s"}
                  </option>
                ))}
              </select>
            </div>
            <div className="flex flex-col gap-1">
              <label className="text-xs font-medium">From</label>
              <input type="date" className={cn(inputClass, "w-40")} value={from} onChange={(e) => setFrom(e.target.value)} />
            </div>
            <div className="flex flex-col gap-1">
              <label className="text-xs font-medium">To</label>
              <input type="date" className={cn(inputClass, "w-40")} value={to} onChange={(e) => setTo(e.target.value)} />
            </div>
            {workers.isPending && <Loader2 className="text-muted-foreground mb-2 size-4 animate-spin" />}
          </CardContent>
        </Card>

        <div className="grid gap-4 lg:grid-cols-[20rem_1fr]">
          {/* Sessions list */}
          <Card>
            <CardHeader className="pb-2">
              <CardTitle className="text-sm">Sessions</CardTitle>
              <CardDescription>
                {userId == null
                  ? "Pick a worker."
                  : `${sessionRows.length} session${sessionRows.length === 1 ? "" : "s"}`}
              </CardDescription>
            </CardHeader>
            <CardContent className="max-h-[32rem] overflow-y-auto pt-0">
              {sessions.isPending && userId != null ? (
                <div className="text-muted-foreground flex items-center gap-2 text-sm">
                  <Loader2 className="size-4 animate-spin" />
                  Loading…
                </div>
              ) : userId != null && sessionRows.length === 0 ? (
                <p className="text-muted-foreground text-sm">No sessions in this window.</p>
              ) : (
                <div className="flex flex-col gap-1">
                  {sessionRows.map((s) => (
                    <SessionRow
                      key={s.session_id}
                      session={s}
                      selected={s.session_id === sessionId}
                      onClick={() => setSessionId(s.session_id)}
                    />
                  ))}
                </div>
              )}
            </CardContent>
          </Card>

          {/* The trail map + its located edits */}
          <div className="flex flex-col gap-3">
            <FieldTrackMap trail={trail.data ?? null} onWaypointClick={locate} />
            {sessionId != null && (
              <Card>
                <CardHeader className="pb-2">
                  <CardTitle className="flex items-center gap-2 text-sm">
                    <Wrench className="size-4" />
                    Edits on this trail
                  </CardTitle>
                  <CardDescription>
                    {trail.isPending
                      ? "Loading…"
                      : `${waypoints.length} located edit${waypoints.length === 1 ? "" : "s"}`}
                  </CardDescription>
                </CardHeader>
                {waypoints.length > 0 && (
                  <CardContent className="max-h-72 overflow-y-auto pt-0">
                    <div className="flex flex-col divide-y">
                      {waypoints.map((w) => (
                        <div key={w.op_id} className="flex flex-wrap items-center gap-x-2 py-1.5 text-sm">
                          <MapPin className={cn("size-3.5", w.kind === "delete" ? "text-red-600" : "text-amber-600")} />
                          <span className="font-medium capitalize">{w.kind.replace(/_/g, " ").replace("attrs", "edit")}</span>
                          {w.feature_class && (
                            <span className="text-muted-foreground text-xs">{w.feature_class}</span>
                          )}
                          <span className="text-muted-foreground text-xs tabular-nums">{fmtWhen(w.recorded_at)}</span>
                          {w.feature_class && w.feature_id && (
                            <Button
                              variant="ghost"
                              size="sm"
                              className="text-primary ml-auto h-6 px-1.5 text-xs"
                              onClick={() => locate(w)}
                            >
                              Locate on map
                            </Button>
                          )}
                        </div>
                      ))}
                    </div>
                  </CardContent>
                )}
              </Card>
            )}
          </div>
        </div>
      </div>
    </div>
  )
}

function SessionRow({
  session,
  selected,
  onClick,
}: {
  session: TrackSession
  selected: boolean
  onClick: () => void
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={cn(
        "flex flex-col gap-0.5 rounded-md border px-2 py-1.5 text-left text-sm",
        selected ? "border-primary bg-primary/5" : "border-border/60 hover:bg-muted/40",
      )}
    >
      <span className="font-medium tabular-nums">{fmtWhen(session.started_at)}</span>
      <span className="text-muted-foreground text-xs">
        {fmtDuration(session.started_at, session.ended_at)} · {session.point_count} points · {session.action_count} edit
        {session.action_count === 1 ? "" : "s"}
      </span>
    </button>
  )
}
