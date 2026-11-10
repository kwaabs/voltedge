import { useEffect, useRef } from "react"
import { Bell, BellOff, Check, RefreshCw } from "lucide-react"

import {
  type Alarm,
  useAcknowledgeAlarm,
  useAlarmStream,
  useAlarms,
  useReconcileAlarms,
  useShelveAlarm,
} from "@/hooks/use-alarms"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { useToastStore } from "@/stores/toast-store"

const SEVERITY_DOT: Record<Alarm["severity"], string> = {
  critical: "bg-red-500",
  high: "bg-orange-500",
  medium: "bg-amber-400",
  low: "bg-muted-foreground/50",
}

const SEVERITY_RANK: Record<Alarm["severity"], string> = {
  critical: "Critical",
  high: "High",
  medium: "Medium",
  low: "Low",
}

function fmtTime(iso: string): string {
  const d = new Date(iso)
  return d.toLocaleString(undefined, { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" })
}

/** The operator alarm annunciator: a severity-ranked, acknowledgeable worklist of
 * conditions needing attention (protective operations + off-normal configuration),
 * live over SSE. Reconciles the standing sources on mount so off-normal devices
 * surface without an operation. oms:read to see, oms:write to act. */
export function AlarmAnnunciator({ canWrite }: { canWrite: boolean }) {
  const alarms = useAlarms()
  const reconcile = useReconcileAlarms()
  const ack = useAcknowledgeAlarm()
  const shelve = useShelveAlarm()
  useAlarmStream(true)

  // Rescan the standing (off-normal) sources once on mount so their alarms are current
  // — only for a writer (reconcile needs oms:write); a read-only operator sees the last
  // reconciled set via the poll/stream.
  const didReconcile = useRef(false)
  useEffect(() => {
    if (!canWrite || didReconcile.current) return
    didReconcile.current = true
    reconcile.mutate()
  }, [canWrite, reconcile])

  const rows = alarms.data?.alarms ?? []
  const unacked = rows.filter((a) => !a.acknowledged_at).length
  const critical = rows.filter((a) => a.severity === "critical").length

  function doAck(a: Alarm) {
    ack.mutate(a.id, { onError: () => useToastStore.getState().pushError("Could not acknowledge the alarm.") })
  }
  function doShelve(a: Alarm) {
    const until = new Date(Date.now() + 60 * 60_000).toISOString()
    shelve.mutate(
      { id: a.id, until, reason: "shelved 1h from the annunciator" },
      { onError: () => useToastStore.getState().pushError("Could not shelve the alarm.") },
    )
  }

  return (
    <Card>
      <CardHeader className="flex flex-row flex-wrap items-center justify-between gap-2 pb-3">
        <CardTitle className="flex items-center gap-2 text-sm">
          <Bell className="size-4" />
          Alarms
          {rows.length > 0 && (
            <span className="flex items-center gap-1.5 text-xs font-normal">
              <span className="bg-muted rounded px-1.5 py-0.5 tabular-nums">{rows.length}</span>
              {unacked > 0 && (
                <span className="rounded bg-orange-500/15 px-1.5 py-0.5 tabular-nums text-orange-600 dark:text-orange-400">
                  {unacked} unack'd
                </span>
              )}
              {critical > 0 && (
                <span className="bg-destructive/15 text-destructive rounded px-1.5 py-0.5 tabular-nums">
                  {critical} critical
                </span>
              )}
            </span>
          )}
        </CardTitle>
        {canWrite && (
          <Button variant="ghost" size="sm" disabled={reconcile.isPending} onClick={() => reconcile.mutate()}>
            <RefreshCw className={`size-3.5 ${reconcile.isPending ? "animate-spin" : ""}`} /> Rescan
          </Button>
        )}
      </CardHeader>
      <CardContent className="pt-0">
        {alarms.isPending ? (
          <p className="text-muted-foreground text-sm">Loading alarms…</p>
        ) : rows.length === 0 ? (
          <p className="text-muted-foreground text-sm">No active alarms — the network is in its normal state.</p>
        ) : (
          <ul className="flex flex-col divide-y">
            {rows.map((a) => (
              <li key={a.id} className="flex flex-wrap items-center gap-x-3 gap-y-1 py-2 text-sm">
                <span
                  className={`size-2.5 shrink-0 rounded-full ${SEVERITY_DOT[a.severity]}`}
                  title={SEVERITY_RANK[a.severity]}
                />
                <span className="min-w-0 flex-1">
                  <span className={a.acknowledged_at ? "text-muted-foreground" : "font-medium"}>{a.message}</span>
                  <span className="text-muted-foreground ml-2 text-[11px]">
                    {a.category}
                    {a.occurrence_count > 1 && ` ·×${a.occurrence_count}`}
                    {" · "}
                    {fmtTime(a.last_raised_at)}
                    {!a.active && a.acknowledged_at == null && " · cleared"}
                    {a.acknowledged_at && ` · ack'd${a.acknowledged_by_name ? ` by ${a.acknowledged_by_name}` : ""}`}
                  </span>
                </span>
                {canWrite && (
                  <span className="flex shrink-0 items-center gap-1">
                    {!a.acknowledged_at && (
                      <Button variant="outline" size="sm" className="h-7 px-2" disabled={ack.isPending} onClick={() => doAck(a)}>
                        <Check className="size-3.5" /> Ack
                      </Button>
                    )}
                    <Button
                      variant="ghost"
                      size="sm"
                      className="text-muted-foreground h-7 px-2"
                      disabled={shelve.isPending}
                      onClick={() => doShelve(a)}
                      title="Shelve for 1 hour"
                    >
                      <BellOff className="size-3.5" />
                    </Button>
                  </span>
                )}
              </li>
            ))}
          </ul>
        )}
      </CardContent>
    </Card>
  )
}
