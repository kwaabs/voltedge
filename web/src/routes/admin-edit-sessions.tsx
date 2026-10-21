import { useMemo, useState } from "react"
import { Clock, Loader2 } from "lucide-react"

import { Button } from "@/components/ui/button"
import { type EditSessionFilter, useEditSessions } from "@/hooks/use-edit-sessions"
import { useFeatureClasses } from "@/hooks/use-feature-classes"
import { useAdminUsers } from "@/hooks/use-users"

/** Narrow a datetime-local value to an RFC3339 string the API parses, or
 * undefined when blank. Mirrors admin-activity.tsx. */
function toRFC3339(local: string): string | undefined {
  if (!local) return undefined
  const d = new Date(local)
  return Number.isNaN(d.getTime()) ? undefined : d.toISOString()
}

/** Human "Xh Ym" / "Ym" / "Ys" for a session's span. */
function formatDuration(startISO: string, endISO: string): string {
  const ms = Math.max(0, new Date(endISO).getTime() - new Date(startISO).getTime())
  const sec = Math.round(ms / 1000)
  if (sec < 60) return `${sec}s`
  const min = Math.round(sec / 60)
  if (min < 60) return `${min}m`
  const h = Math.floor(min / 60)
  return `${h}h ${min % 60}m`
}

export function AdminEditSessionsPage() {
  const [actorId, setActorId] = useState("")
  const [from, setFrom] = useState("")
  const [to, setTo] = useState("")

  const filter: EditSessionFilter = useMemo(
    () => ({ actorId: actorId || undefined, from: toRFC3339(from), to: toRFC3339(to), limit: 500 }),
    [actorId, from, to],
  )

  const sessions = useEditSessions(filter)
  const users = useAdminUsers()
  const classes = useFeatureClasses()

  const rows = sessions.data?.edit_sessions ?? []
  const classNameByCode = useMemo(() => {
    const m = new Map<string, string>()
    for (const c of classes.data?.feature_classes ?? []) m.set(c.code, c.name)
    return m
  }, [classes.data])

  const classLabels = (csv: string) =>
    csv
      .split(",")
      .filter(Boolean)
      .map((code) => classNameByCode.get(code) ?? code)
      .join(", ")

  const anyFilter = actorId || from || to

  return (
    <div className="h-full overflow-y-auto p-4">
      <div className="flex w-full max-w-5xl flex-col gap-4">
        <div>
          <h1 className="flex items-center gap-2 text-lg font-semibold">
            <Clock className="size-5" />
            Edit sessions
          </h1>
          <p className="text-muted-foreground text-sm">
            Each row is one edit-mode session — the batch of changes a person made between turning Edit on
            and off — with when it started and ended, how many changes it made, and which classes it
            touched. Sessions where nothing changed never appear. Admins see everyone; a supervisor sees
            their own sessions and their reports'. For the individual changes, see the{" "}
            <b>Activity audit</b>. Newest first, capped at 500; narrow with the filters.
          </p>
        </div>

        {/* Filters */}
        <div className="flex flex-wrap items-end gap-3 rounded-md border p-3">
          {/* The Person picker needs the user list, which only an admin can load;
              a non-admin supervisor just sees their own + reports' sessions
              unfiltered (and the Person column tells them who). */}
          {(users.data?.users?.length ?? 0) > 0 && (
            <label className="flex flex-col gap-1 text-xs font-medium">
              Person
              <select
                value={actorId}
                onChange={(e) => setActorId(e.target.value)}
                className="border-input bg-background h-8 min-w-44 rounded-md border px-2 text-sm"
              >
                <option value="">Anyone</option>
                {(users.data?.users ?? []).map((u) => (
                  <option key={u.id} value={u.id}>
                    {u.display_name || u.email}
                  </option>
                ))}
              </select>
            </label>
          )}

          <label className="flex flex-col gap-1 text-xs font-medium">
            From
            <input
              type="datetime-local"
              value={from}
              onChange={(e) => setFrom(e.target.value)}
              className="border-input bg-background h-8 rounded-md border px-2 text-sm"
            />
          </label>

          <label className="flex flex-col gap-1 text-xs font-medium">
            To
            <input
              type="datetime-local"
              value={to}
              onChange={(e) => setTo(e.target.value)}
              className="border-input bg-background h-8 rounded-md border px-2 text-sm"
            />
          </label>

          {anyFilter && (
            <Button
              variant="ghost"
              size="sm"
              onClick={() => {
                setActorId("")
                setFrom("")
                setTo("")
              }}
            >
              Clear
            </Button>
          )}
        </div>

        {/* Table */}
        <div className="rounded-md border">
          <table className="w-full text-sm">
            <thead>
              <tr className="text-muted-foreground border-b text-left text-xs">
                <th className="px-3 py-2 font-medium">Person</th>
                <th className="px-3 py-2 font-medium">Started</th>
                <th className="px-3 py-2 font-medium">Ended</th>
                <th className="px-3 py-2 font-medium">Duration</th>
                <th className="px-3 py-2 text-right font-medium">Changes</th>
                <th className="px-3 py-2 font-medium">Classes</th>
              </tr>
            </thead>
            <tbody>
              {sessions.isPending ? (
                <tr>
                  <td colSpan={6} className="text-muted-foreground px-3 py-6 text-center">
                    <Loader2 className="mr-2 inline size-4 animate-spin" />
                    Loading…
                  </td>
                </tr>
              ) : sessions.isError ? (
                <tr>
                  <td colSpan={6} className="text-destructive px-3 py-6 text-center">
                    Could not load the edit sessions.
                  </td>
                </tr>
              ) : rows.length === 0 ? (
                <tr>
                  <td colSpan={6} className="text-muted-foreground px-3 py-6 text-center">
                    No edit sessions match these filters.
                  </td>
                </tr>
              ) : (
                rows.map((s) => (
                  <tr key={s.session_id} className="hover:bg-muted/40 border-b last:border-0">
                    <td className="px-3 py-2">
                      {s.actor_name || s.actor_email || <span className="text-muted-foreground italic">system</span>}
                    </td>
                    <td className="text-muted-foreground px-3 py-2 whitespace-nowrap tabular-nums">
                      {new Date(s.started_at).toLocaleString()}
                    </td>
                    <td className="text-muted-foreground px-3 py-2 whitespace-nowrap tabular-nums">
                      {new Date(s.ended_at).toLocaleString()}
                    </td>
                    <td className="px-3 py-2 whitespace-nowrap tabular-nums">
                      {formatDuration(s.started_at, s.ended_at)}
                    </td>
                    <td className="px-3 py-2 text-right tabular-nums">{s.change_count}</td>
                    <td className="text-muted-foreground max-w-80 truncate px-3 py-2" title={classLabels(s.classes)}>
                      {classLabels(s.classes)}
                    </td>
                  </tr>
                ))
              )}
            </tbody>
          </table>
        </div>
      </div>
    </div>
  )
}
