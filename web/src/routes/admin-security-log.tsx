import { useMemo, useState } from "react"
import { KeyRound, Loader2 } from "lucide-react"

import { Button } from "@/components/ui/button"
import { useAuthEvents, type AuthEventFilter } from "@/hooks/use-auth-events"
import { useAdminUsers } from "@/hooks/use-users"
import { cn } from "@/lib/utils"

// The security audit log (iam.auth_events): who signed in — or failed, or was
// denied — when, from what IP, with which browser, plus SSO links, logouts, the
// break-glass switch and role changes. Read-only; gated on admin:users (and
// break-glass) with the rest of the user-management API. The complement to the
// Activity audit, which tracks DATA changes rather than account entry.

/** The event verbs iam.auth_events records -- a fixed list so the filter offers
 * a readable dropdown and a badge can colour by weight. */
const EVENT_TYPES = [
  "login_success",
  "login_failure",
  "login_denied",
  "break_glass_login",
  "logout",
  "sso_linked",
  "role_changed",
  "local_auth_toggled",
] as const

function humanizeEvent(type: string): string {
  const s = type.replace(/_/g, " ")
  return s.charAt(0).toUpperCase() + s.slice(1)
}

/** Badge tint by how notable the event is: a denied/failed sign-in or a
 * break-glass entry reads differently from an ordinary success at a glance. */
function eventTone(type: string): string {
  if (type === "login_failure" || type === "login_denied") return "bg-destructive/15 text-destructive"
  if (type === "break_glass_login" || type === "local_auth_toggled")
    return "bg-amber-500/15 text-amber-700 dark:text-amber-400"
  if (type === "login_success") return "bg-emerald-500/15 text-emerald-600 dark:text-emerald-400"
  return "bg-sky-500/15 text-sky-600 dark:text-sky-400"
}

/** A datetime-local value → the RFC3339 string the API parses, or undefined. */
function toRFC3339(local: string): string | undefined {
  if (!local) return undefined
  const d = new Date(local)
  return Number.isNaN(d.getTime()) ? undefined : d.toISOString()
}

export function AdminSecurityLogPage() {
  const [userId, setUserId] = useState("")
  const [eventType, setEventType] = useState("")
  const [from, setFrom] = useState("")
  const [to, setTo] = useState("")

  const filter: AuthEventFilter = useMemo(
    () => ({
      userId: userId || undefined,
      eventType: eventType || undefined,
      from: toRFC3339(from),
      to: toRFC3339(to),
      limit: 500,
    }),
    [userId, eventType, from, to],
  )

  const events = useAuthEvents(filter)
  const users = useAdminUsers()
  const rows = events.data?.events ?? []
  const anyFilter = userId || eventType || from || to

  return (
    <div className="h-full overflow-y-auto p-4">
      <div className="flex w-full max-w-6xl flex-col gap-4">
        <div>
          <h1 className="flex items-center gap-2 text-lg font-semibold">
            <KeyRound className="size-5" />
            Sign-in &amp; security log
          </h1>
          <p className="text-muted-foreground text-sm">
            The append-only authentication audit trail — who signed in or failed, when, from what IP and
            browser, plus SSO links, logouts, break-glass entries and role changes. Newest first, capped at
            500 rows; narrow with the filters. For data changes (who edited what), see the Activity audit.
          </p>
        </div>

        {/* Filters */}
        <div className="flex flex-wrap items-end gap-3 rounded-md border p-3">
          <label className="flex flex-col gap-1 text-xs font-medium">
            Person
            <select
              value={userId}
              onChange={(e) => setUserId(e.target.value)}
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

          <label className="flex flex-col gap-1 text-xs font-medium">
            Event
            <select
              value={eventType}
              onChange={(e) => setEventType(e.target.value)}
              className="border-input bg-background h-8 min-w-40 rounded-md border px-2 text-sm"
            >
              <option value="">Any event</option>
              {EVENT_TYPES.map((t) => (
                <option key={t} value={t}>
                  {humanizeEvent(t)}
                </option>
              ))}
            </select>
          </label>

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
                setUserId("")
                setEventType("")
                setFrom("")
                setTo("")
              }}
            >
              Clear
            </Button>
          )}
        </div>

        {/* Table (on a card) */}
        <div className="bg-card overflow-hidden rounded-xl border shadow-sm">
          <table className="w-full text-sm">
            <thead>
              <tr className="bg-muted/40 text-muted-foreground border-b text-left text-xs">
                <th className="px-3 py-2 font-medium">When</th>
                <th className="px-3 py-2 font-medium">Person</th>
                <th className="px-3 py-2 font-medium">Event</th>
                <th className="px-3 py-2 font-medium">IP</th>
                <th className="px-3 py-2 font-medium">Browser</th>
                <th className="px-3 py-2 font-medium">Detail</th>
              </tr>
            </thead>
            <tbody>
              {events.isPending ? (
                <tr>
                  <td colSpan={6} className="text-muted-foreground px-3 py-6 text-center">
                    <Loader2 className="mr-2 inline size-4 animate-spin" />
                    Loading…
                  </td>
                </tr>
              ) : events.isError ? (
                <tr>
                  <td colSpan={6} className="text-destructive px-3 py-6 text-center">
                    Could not load the security log.
                  </td>
                </tr>
              ) : rows.length === 0 ? (
                <tr>
                  <td colSpan={6} className="text-muted-foreground px-3 py-6 text-center">
                    No events match these filters.
                  </td>
                </tr>
              ) : (
                rows.map((row) => (
                  <tr key={row.id} className="hover:bg-muted/40 border-b align-top last:border-0">
                    <td className="text-muted-foreground px-3 py-2 whitespace-nowrap tabular-nums">
                      {new Date(row.created_at).toLocaleString()}
                    </td>
                    <td className="px-3 py-2">
                      {row.user_name || row.user_email ? (
                        <span title={row.user_email ?? undefined}>{row.user_name || row.user_email}</span>
                      ) : (
                        <span className="text-muted-foreground italic">unknown</span>
                      )}
                    </td>
                    <td className="px-3 py-2">
                      <span className={cn("rounded px-1.5 py-0.5 text-xs font-medium", eventTone(row.event_type))}>
                        {humanizeEvent(row.event_type)}
                      </span>
                    </td>
                    <td className="text-muted-foreground px-3 py-2 font-mono text-xs whitespace-nowrap">
                      {row.ip ?? "—"}
                    </td>
                    <td className="text-muted-foreground max-w-56 truncate px-3 py-2 text-xs" title={row.user_agent ?? undefined}>
                      {row.user_agent ?? "—"}
                    </td>
                    <td className="text-muted-foreground max-w-72 px-3 py-2 text-xs">{row.detail ?? "—"}</td>
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
