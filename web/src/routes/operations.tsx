import { useMemo, useState } from "react"
import { Loader2, Plus } from "lucide-react"

import { LogOperationDialog, OP_SOURCES, OP_TYPES } from "@/components/map/log-operation-dialog"
import { DeviceDetailPanel } from "@/components/layout/device-detail-panel"
import { OperationsTabs } from "@/components/layout/operations-tabs"
import { inputClass } from "@/components/map/field-utils"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { usePermission } from "@/hooks/use-auth"
import { useAbnormalDevices, useDeviceInventory, useOperationCounts, useOperations } from "@/hooks/use-operations"
import { cn } from "@/lib/utils"
import { useDevicePanelStore } from "@/stores/device-panel-store"

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

/** The device identity cell, clickable to open the device's detail panel on the
 * right of the page (a feature summary + its operation history, with its own
 * "View on map" button). */
function DeviceCell({ cls, id, onOpen }: { cls: string; id: string; onOpen: (cls: string, id: string) => void }) {
  return (
    <button
      type="button"
      onClick={() => onOpen(cls, id)}
      className="hover:text-primary text-left hover:underline"
      title="View device details"
    >
      <div>{humanize(cls)}</div>
      <div className="text-muted-foreground font-mono text-xs">{shortId(id)}</div>
    </button>
  )
}

type View = "timeline" | "by-device" | "devices" | "abnormal"

const VIEW_LABEL: Record<View, string> = {
  timeline: "Timeline",
  "by-device": "By device",
  devices: "Devices",
  abnormal: "Abnormal",
}

/** The declared normal posture reads as a target position; show it against the
 * contradicting live state. */
function normalLabel(normalState: string): string {
  if (normalState === "NORMALLY_OPEN") return "Open"
  if (normalState === "NORMALLY_CLOSED") return "Closed"
  return normalState
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
  // The open device panel lives in a shared store so it survives switching to the
  // Live state tab and back (separate routes would each drop a local useState).
  const selected = useDevicePanelStore((s) => s.selected)

  const ops = useOperations({ op_type: opType, source, device_class: deviceClass, limit: 500 })
  const counts = useOperationCounts(opType || undefined)
  const inventory = useDeviceInventory(deviceClass || undefined, state || undefined)
  const abnormal = useAbnormalDevices(deviceClass || undefined)
  const abnormalRows = useMemo(() => abnormal.data?.devices ?? [], [abnormal.data])
  const devices = useMemo(() => inventory.data?.devices ?? [], [inventory.data])

  const rows = useMemo(() => ops.data?.operations ?? [], [ops.data])
  const countRows = useMemo(() => {
    const all = counts.data?.counts ?? []
    // The by-device view reuses the op_type filter (server-side) and the device
    // class filter (client-side, the counts endpoint has no class filter).
    return deviceClass ? all.filter((c) => c.device_class === deviceClass) : all
  }, [counts.data, deviceClass])

  return (
    <div className="flex h-full gap-4 p-4">
      <div className="flex min-w-0 flex-1 flex-col gap-4 overflow-y-auto">
        <OperationsTabs />
        <div className="flex items-start justify-between">
          <p className="text-muted-foreground text-sm">
            The device-operation log — trips, lockouts, recloses and open/close events across the network.
          </p>
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
                      : view === "devices"
                        ? `${devices.length} operable device${devices.length === 1 ? "" : "s"} — current state + last operation`
                        : `${abnormalRows.length} device${abnormalRows.length === 1 ? "" : "s"} off normal — live position contradicts the declared normal state`}
                </CardDescription>
              </div>
              {/* View toggle */}
              <div className="flex overflow-hidden rounded-md border">
                {(["timeline", "by-device", "devices", "abnormal"] as const).map((v) => (
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
              {(view === "timeline" || view === "by-device") && (
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
                          <DeviceCell cls={op.device_class} id={op.device_id} onOpen={(cls, id) => useDevicePanelStore.getState().open(cls, id)} />
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
                          <DeviceCell cls={c.device_class} id={c.device_id} onOpen={(cls, id) => useDevicePanelStore.getState().open(cls, id)} />
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
            ) : view === "devices" ? (
              inventory.isPending ? (
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
                        <DeviceCell cls={d.device_class} id={d.device_id} onOpen={(cls, id) => useDevicePanelStore.getState().open(cls, id)} />
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
            )
            ) : abnormal.isPending ? (
              <div className="text-muted-foreground flex items-center gap-2 text-sm">
                <Loader2 className="size-4 animate-spin" /> Checking configuration…
              </div>
            ) : abnormal.isError ? (
              <p className="text-destructive text-sm">Could not load the abnormal-configuration report.</p>
            ) : abnormalRows.length === 0 ? (
              <p className="text-muted-foreground text-sm">
                Every switching device is in its normal position — the network is in its normal configuration.
              </p>
            ) : (
              <table className="w-full text-sm">
                <thead className="text-muted-foreground border-b text-left text-xs">
                  <tr>
                    <th className="py-2 pr-3 font-medium">Device</th>
                    <th className="py-2 pr-3 font-medium">Normal</th>
                    <th className="py-2 pr-3 font-medium">Current</th>
                    <th className="py-2 pr-3 font-medium">Last operation</th>
                    <th className="py-2 font-medium">When</th>
                  </tr>
                </thead>
                <tbody>
                  {abnormalRows.map((d) => (
                    <tr key={`${d.device_class}:${d.device_id}`} className="border-b last:border-b-0">
                      <td className="py-1.5 pr-3">
                        <DeviceCell
                          cls={d.device_class}
                          id={d.device_id}
                          onOpen={(cls, id) => useDevicePanelStore.getState().open(cls, id)}
                        />
                      </td>
                      <td className="text-muted-foreground py-1.5 pr-3">{normalLabel(d.normal_state)}</td>
                      <td className="py-1.5 pr-3">
                        <span className="rounded bg-amber-100 px-1.5 py-0.5 text-xs font-medium text-amber-800 dark:bg-amber-950 dark:text-amber-300">
                          {d.device_state}
                        </span>
                      </td>
                      <td className="py-1.5 pr-3">
                        {d.last_op_type ? <OpBadge type={d.last_op_type} /> : <span className="text-muted-foreground">—</span>}
                      </td>
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

      {selected && (
        <DeviceDetailPanel
          featureClass={selected.cls}
          featureId={selected.id}
          onClose={() => useDevicePanelStore.getState().close()}
        />
      )}

      {logging && <LogOperationDialog onClose={() => setLogging(false)} />}
    </div>
  )
}
