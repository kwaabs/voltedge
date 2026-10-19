import { useMemo, useState } from "react"
import { Activity, Loader2, Plus } from "lucide-react"

import { LogOperationDialog, OP_SOURCES, OP_TYPES } from "@/components/map/log-operation-dialog"
import { inputClass } from "@/components/map/field-utils"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { usePermission } from "@/hooks/use-auth"
import { useDeviceInventory, useOperationCounts, useOperations } from "@/hooks/use-operations"
import { cn } from "@/lib/utils"

function shortId(id: string): string {
  return id.length > 8 ? `${id.slice(0, 8)}…` : id
}

function formatWhen(iso: string): string {
  return new Date(iso).toLocaleString()
}

function humanize(s: string): string {
  return s.replace(/_/g, " ")
}

/** A small coloured chip for an op_type, so a trip/lockout reads at a glance. */
const OP_BADGE: Record<string, string> = {
  trip: "bg-destructive/10 text-destructive",
  lockout: "bg-destructive/15 text-destructive",
  reclose: "bg-amber-500/15 text-amber-600 dark:text-amber-400",
  open: "bg-blue-500/10 text-blue-600 dark:text-blue-400",
  close: "bg-green-600/10 text-green-700 dark:text-green-400",
  earth: "bg-muted text-muted-foreground",
  alarm: "bg-amber-500/15 text-amber-600 dark:text-amber-400",
  other: "bg-muted text-muted-foreground",
}

function OpBadge({ type }: { type: string }) {
  return (
    <span className={cn("rounded px-1.5 py-0.5 text-xs font-medium capitalize", OP_BADGE[type] ?? "bg-muted text-muted-foreground")}>
      {humanize(type)}
    </span>
  )
}

type View = "timeline" | "by-device" | "devices"

const VIEW_LABEL: Record<View, string> = {
  timeline: "Timeline",
  "by-device": "By device",
  devices: "Devices",
}

/**
 * The device-operation log (api/internal/deviceops): a board of recent protective/
 * switching device operations (trip, lockout, reclose, open, close) and a per-
 * device tally. Distinct from the live device_state (OMS) -- this is the history
 * of operations, the trip record. Fed by crew entry, executed switching, booked
 * faults, and (later) SCADA.
 */
export function OperationsPage() {
  const canWrite = usePermission()("operations:write")
  const [view, setView] = useState<View>("timeline")
  const [opType, setOpType] = useState("")
  const [source, setSource] = useState("")
  const [deviceClass, setDeviceClass] = useState("")
  const [state, setState] = useState("")
  const [logging, setLogging] = useState(false)

  const ops = useOperations({ op_type: opType, source, device_class: deviceClass, limit: 500 })
  const counts = useOperationCounts(opType || undefined)
  const inventory = useDeviceInventory(deviceClass || undefined, state || undefined)
  const devices = useMemo(() => inventory.data?.devices ?? [], [inventory.data])

  const rows = useMemo(() => ops.data?.operations ?? [], [ops.data])
  const countRows = useMemo(() => {
    const all = counts.data?.counts ?? []
    // The by-device view reuses the op_type filter (server-side) and the device
    // class filter (client-side, the counts endpoint has no class filter).
    return deviceClass ? all.filter((c) => c.device_class === deviceClass) : all
  }, [counts.data, deviceClass])

  return (
    <div className="h-full overflow-y-auto p-4">
      <div className="flex flex-col gap-4">
        <div className="flex items-start justify-between">
          <div>
            <h1 className="flex items-center gap-2 text-lg font-semibold">
              <Activity className="text-primary size-5" />
              Operations
            </h1>
            <p className="text-muted-foreground text-sm">
              The device-operation log — trips, lockouts, recloses and open/close events across the network.
            </p>
          </div>
          {canWrite && (
            <Button size="sm" onClick={() => setLogging(true)}>
              <Plus className="size-4" />
              Log operation
            </Button>
          )}
        </div>

        <Card>
          <CardHeader className="gap-3">
            <div className="flex items-center justify-between">
              <div>
                <CardTitle className="text-base">{VIEW_LABEL[view]}</CardTitle>
                <CardDescription>
                  {view === "timeline"
                    ? `${rows.length} operation${rows.length === 1 ? "" : "s"}${rows.length >= 500 ? " (showing latest 500)" : ""}`
                    : view === "by-device"
                      ? `${countRows.length} device/operation tallies — busiest first`
                      : `${devices.length} operable device${devices.length === 1 ? "" : "s"} — current state + last operation`}
                </CardDescription>
              </div>
              {/* View toggle */}
              <div className="flex overflow-hidden rounded-md border">
                {(["timeline", "by-device", "devices"] as const).map((v) => (
                  <button
                    key={v}
                    type="button"
                    onClick={() => setView(v)}
                    className={cn(
                      "px-3 py-1 text-xs",
                      view === v ? "bg-accent text-accent-foreground font-medium" : "hover:bg-accent/50",
                    )}
                  >
                    {VIEW_LABEL[v]}
                  </button>
                ))}
              </div>
            </div>

            {/* Filters */}
            <div className="flex flex-wrap items-center gap-2">
              <input
                className={`${inputClass} h-8 w-auto flex-1`}
                placeholder="Device class (e.g. MV_RECLOSER)"
                value={deviceClass}
                onChange={(e) => setDeviceClass(e.target.value)}
              />
              {view !== "devices" && (
                <select className={`${inputClass} h-8 w-auto`} value={opType} onChange={(e) => setOpType(e.target.value)}>
                  <option value="">All operations</option>
                  {OP_TYPES.map((o) => (
                    <option key={o.value} value={o.value}>
                      {o.label}
                    </option>
                  ))}
                </select>
              )}
              {view === "timeline" && (
                <select className={`${inputClass} h-8 w-auto`} value={source} onChange={(e) => setSource(e.target.value)}>
                  <option value="">All sources</option>
                  {OP_SOURCES.map((o) => (
                    <option key={o.value} value={o.value}>
                      {o.label}
                    </option>
                  ))}
                </select>
              )}
              {view === "devices" && (
                <select className={`${inputClass} h-8 w-auto`} value={state} onChange={(e) => setState(e.target.value)}>
                  <option value="">Any state</option>
                  <option value="CLOSED">Closed</option>
                  <option value="OPEN">Open</option>
                </select>
              )}
            </div>
          </CardHeader>

          <CardContent>
            {view === "timeline" ? (
              ops.isPending ? (
                <div className="text-muted-foreground flex items-center gap-2 text-sm">
                  <Loader2 className="size-4 animate-spin" /> Loading operations…
                </div>
              ) : ops.isError ? (
                <p className="text-destructive text-sm">Could not load operations.</p>
              ) : rows.length === 0 ? (
                <p className="text-muted-foreground text-sm">No operations match.</p>
              ) : (
                <table className="w-full text-sm">
                  <thead className="text-muted-foreground border-b text-left text-xs">
                    <tr>
                      <th className="py-2 pr-3 font-medium">When</th>
                      <th className="py-2 pr-3 font-medium">Device</th>
                      <th className="py-2 pr-3 font-medium">Operation</th>
                      <th className="py-2 pr-3 font-medium">Source</th>
                      <th className="py-2 font-medium">Cause</th>
                    </tr>
                  </thead>
                  <tbody>
                    {rows.map((op) => (
                      <tr key={op.id} className="border-b last:border-b-0">
                        <td className="py-1.5 pr-3 whitespace-nowrap">{formatWhen(op.occurred_at)}</td>
                        <td className="py-1.5 pr-3">
                          <div>{humanize(op.device_class)}</div>
                          <div className="text-muted-foreground font-mono text-xs">{shortId(op.device_id)}</div>
                        </td>
                        <td className="py-1.5 pr-3">
                          <OpBadge type={op.op_type} />
                        </td>
                        <td className="text-muted-foreground py-1.5 pr-3 capitalize">{humanize(op.source)}</td>
                        <td className="text-muted-foreground py-1.5">{op.cause ?? "—"}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              )
            ) : view === "by-device" ? (
              counts.isPending ? (
                <div className="text-muted-foreground flex items-center gap-2 text-sm">
                  <Loader2 className="size-4 animate-spin" /> Loading tally…
                </div>
              ) : counts.isError ? (
                <p className="text-destructive text-sm">Could not load the tally.</p>
              ) : countRows.length === 0 ? (
                <p className="text-muted-foreground text-sm">No operations yet.</p>
              ) : (
                <table className="w-full text-sm">
                  <thead className="text-muted-foreground border-b text-left text-xs">
                    <tr>
                      <th className="py-2 pr-3 font-medium">Device</th>
                      <th className="py-2 pr-3 font-medium">Operation</th>
                      <th className="py-2 pr-3 font-medium text-right">Count</th>
                      <th className="py-2 font-medium">Last</th>
                    </tr>
                  </thead>
                  <tbody>
                    {countRows.map((c) => (
                      <tr key={`${c.device_class}:${c.device_id}:${c.op_type}`} className="border-b last:border-b-0">
                        <td className="py-1.5 pr-3">
                          <div>{humanize(c.device_class)}</div>
                          <div className="text-muted-foreground font-mono text-xs">{shortId(c.device_id)}</div>
                        </td>
                        <td className="py-1.5 pr-3">
                          <OpBadge type={c.op_type} />
                        </td>
                        <td className="py-1.5 pr-3 text-right tabular-nums font-medium">{c.count}</td>
                        <td className="text-muted-foreground py-1.5 whitespace-nowrap">{formatWhen(c.last_occurred_at)}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              )
            ) : inventory.isPending ? (
              <div className="text-muted-foreground flex items-center gap-2 text-sm">
                <Loader2 className="size-4 animate-spin" /> Loading devices…
              </div>
            ) : inventory.isError ? (
              <p className="text-destructive text-sm">Could not load the device inventory.</p>
            ) : devices.length === 0 ? (
              <p className="text-muted-foreground text-sm">No operable devices match.</p>
            ) : (
              <table className="w-full text-sm">
                <thead className="text-muted-foreground border-b text-left text-xs">
                  <tr>
                    <th className="py-2 pr-3 font-medium">Device</th>
                    <th className="py-2 pr-3 font-medium">State</th>
                    <th className="py-2 pr-3 font-medium">Last operation</th>
                    <th className="py-2 font-medium">When</th>
                  </tr>
                </thead>
                <tbody>
                  {devices.map((d) => (
                    <tr key={`${d.device_class}:${d.device_id}`} className="border-b last:border-b-0">
                      <td className="py-1.5 pr-3">
                        <div>{humanize(d.device_class)}</div>
                        <div className="text-muted-foreground font-mono text-xs">{shortId(d.device_id)}</div>
                      </td>
                      <td className="py-1.5 pr-3">
                        {d.device_state ? (
                          <span
                            className={cn(
                              "rounded px-1.5 py-0.5 text-xs font-medium",
                              d.device_state === "OPEN"
                                ? "bg-blue-500/10 text-blue-600 dark:text-blue-400"
                                : d.device_state === "CLOSED"
                                  ? "bg-green-600/10 text-green-700 dark:text-green-400"
                                  : "bg-muted text-muted-foreground",
                            )}
                          >
                            {d.device_state}
                          </span>
                        ) : (
                          <span className="text-muted-foreground">—</span>
                        )}
                      </td>
                      <td className="py-1.5 pr-3">{d.last_op_type ? <OpBadge type={d.last_op_type} /> : <span className="text-muted-foreground">never</span>}</td>
                      <td className="text-muted-foreground py-1.5 whitespace-nowrap">
                        {d.last_op_at ? formatWhen(d.last_op_at) : "—"}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </CardContent>
        </Card>
      </div>

      {logging && <LogOperationDialog onClose={() => setLogging(false)} />}
    </div>
  )
}
