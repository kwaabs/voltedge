import { useMemo, useState } from "react"
import { History, Loader2 } from "lucide-react"

import { Button } from "@/components/ui/button"
import { useActivity, type ActivityFilter } from "@/hooks/use-activity"
import { useFeatureClasses } from "@/hooks/use-feature-classes"
import { useAdminUsers } from "@/hooks/use-users"
import { cn } from "@/lib/utils"

/** The write verbs gis.feature_history records. Kept here (the API returns the
 * raw code) so the filter can offer a fixed, readable list rather than a free
 * text box, and so a badge can colour by weight. */
const ACTIONS = [
  "create",
  "update",
  "publish",
  "unpublish",
  "delete",
  "restore",
  "purge",
  "stage",
  "switch",
  "relate",
  "unrelate",
] as const

/** How "loud" an action is, driving the badge tint -- a purge/delete reads
 * differently from an ordinary update at a glance. */
function actionTone(action: string): string {
  if (action === "purge" || action === "delete") return "bg-destructive/15 text-destructive"
  if (action === "create" || action === "restore") return "bg-emerald-500/15 text-emerald-600 dark:text-emerald-400"
  if (action === "publish" || action === "switch") return "bg-sky-500/15 text-sky-600 dark:text-sky-400"
  if (action === "relate") return "bg-indigo-500/15 text-indigo-600 dark:text-indigo-400"
  return "bg-muted text-muted-foreground"
}

/** Narrow a datetime-local value ("2026-10-12T14:30") to an RFC3339 string the
 * API parses, or undefined when blank. The browser gives local time; toISOString
 * normalises to UTC with an offset the server's time.Parse accepts. */
function toRFC3339(local: string): string | undefined {
  if (!local) return undefined
  const d = new Date(local)
  return Number.isNaN(d.getTime()) ? undefined : d.toISOString()
}

export function AdminActivityPage() {
  const [actorId, setActorId] = useState("")
  const [featureClass, setFeatureClass] = useState("")
  const [action, setAction] = useState("")
  const [from, setFrom] = useState("")
  const [to, setTo] = useState("")

  const filter: ActivityFilter = useMemo(
    () => ({
      actorId: actorId || undefined,
      featureClass: featureClass || undefined,
      action: action || undefined,
      from: toRFC3339(from),
      to: toRFC3339(to),
      limit: 500,
    }),
    [actorId, featureClass, action, from, to],
  )

  const activity = useActivity(filter)
  const users = useAdminUsers()
  const classes = useFeatureClasses()

  const rows = activity.data?.activity ?? []
  const classNameByCode = useMemo(() => {
    const m = new Map<string, string>()
    for (const c of classes.data?.feature_classes ?? []) m.set(c.code, c.name)
    return m
  }, [classes.data])

  const anyFilter = actorId || featureClass || action || from || to

  return (
    <div className="h-full overflow-y-auto p-4">
      <div className="flex w-full max-w-5xl flex-col gap-4">
        <div>
          <h1 className="flex items-center gap-2 text-lg font-semibold">
            <History className="size-5" />
            Activity audit
          </h1>
          <p className="text-muted-foreground text-sm">
            Who changed what, and when — the append-only feature change log sliced by person and time
            rather than by feature. Use it for accountability and review ("every delete last week", "what
            did this user do this afternoon"). Drill into a single feature's full timeline from its own
            panel. Newest first, capped at 500 rows; narrow with the filters.
          </p>
        </div>

        {/* Filters */}
        <div className="flex flex-wrap items-end gap-3 rounded-md border p-3">
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

          <label className="flex flex-col gap-1 text-xs font-medium">
            Feature class
            <select
              value={featureClass}
              onChange={(e) => setFeatureClass(e.target.value)}
              className="border-input bg-background h-8 min-w-44 rounded-md border px-2 text-sm"
            >
              <option value="">Any class</option>
              {(classes.data?.feature_classes ?? []).map((c) => (
                <option key={c.code} value={c.code}>
                  {c.name}
                </option>
              ))}
            </select>
          </label>

          <label className="flex flex-col gap-1 text-xs font-medium">
            Action
            <select
              value={action}
              onChange={(e) => setAction(e.target.value)}
              className="border-input bg-background h-8 min-w-32 rounded-md border px-2 text-sm"
            >
              <option value="">Any action</option>
              {ACTIONS.map((a) => (
                <option key={a} value={a}>
                  {a}
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
                setActorId("")
                setFeatureClass("")
                setAction("")
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
                <th className="px-3 py-2 font-medium">When</th>
                <th className="px-3 py-2 font-medium">Person</th>
                <th className="px-3 py-2 font-medium">Action</th>
                <th className="px-3 py-2 font-medium">Feature class</th>
                <th className="px-3 py-2 font-medium">Feature</th>
              </tr>
            </thead>
            <tbody>
              {activity.isPending ? (
                <tr>
                  <td colSpan={5} className="text-muted-foreground px-3 py-6 text-center">
                    <Loader2 className="mr-2 inline size-4 animate-spin" />
                    Loading…
                  </td>
                </tr>
              ) : activity.isError ? (
                <tr>
                  <td colSpan={5} className="text-destructive px-3 py-6 text-center">
                    Could not load the activity log.
                  </td>
                </tr>
              ) : rows.length === 0 ? (
                <tr>
                  <td colSpan={5} className="text-muted-foreground px-3 py-6 text-center">
                    No changes match these filters.
                  </td>
                </tr>
              ) : (
                rows.map((row) => (
                  <tr key={row.id} className="hover:bg-muted/40 border-b last:border-0">
                    <td className="text-muted-foreground px-3 py-2 whitespace-nowrap tabular-nums">
                      {new Date(row.at).toLocaleString()}
                    </td>
                    <td className="px-3 py-2">
                      {row.actor_name || row.actor_email || (
                        <span className="text-muted-foreground italic">system</span>
                      )}
                    </td>
                    <td className="px-3 py-2">
                      <span className={cn("rounded px-1.5 py-0.5 text-xs font-medium", actionTone(row.action))}>
                        {row.action}
                      </span>
                    </td>
                    <td className="px-3 py-2">{classNameByCode.get(row.feature_class_code) ?? row.feature_class_code}</td>
                    <td
                      className="text-muted-foreground max-w-56 truncate px-3 py-2 font-mono text-xs"
                      title={row.feature_id}
                    >
                      {row.feature_id}
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
