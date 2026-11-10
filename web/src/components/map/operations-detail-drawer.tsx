import { Bell, Flame, ShieldAlert, X } from "lucide-react"

import { useAlarms } from "@/hooks/use-alarms"
import { useFloatingPanel } from "@/hooks/use-floating-panel"
import { useIncidents } from "@/hooks/use-oms"
import { useDeviceTags, useFeatureGrounds } from "@/hooks/use-safety"
import { cn } from "@/lib/utils"
import { type DeviceDetailSection, useDeviceDetailStore } from "@/stores/device-detail-store"

function humanize(s: string): string {
  return s.replace(/_/g, " ")
}
function shortId(id: string): string {
  return id.length > 10 ? `${id.slice(0, 10)}…` : id
}
function when(iso?: string | null): string {
  return iso ? new Date(iso).toLocaleString() : "—"
}

const SEVERITY_COLOR: Record<string, string> = {
  critical: "#dc2626",
  high: "#ef4444",
  medium: "#f59e0b",
  low: "#eab308",
}

const TAG_COLOR: Record<string, string> = {
  danger: "#ef4444",
  out_of_service: "#f59e0b",
  caution: "#eab308",
  information: "#64748b",
}

/**
 * The operations schematic's per-device detail slide-over. Opened from the operate
 * menu (Alarms / Safety / On incident) for a right-clicked device; shows that
 * device's live context without leaving the one-line. Attributes live in the
 * FeaturePanel instead (richer), reached via the menu's "See attributes".
 */
export function OperationsDetailDrawer() {
  const deviceClass = useDeviceDetailStore((s) => s.deviceClass)
  const deviceId = useDeviceDetailStore((s) => s.deviceId)
  const section = useDeviceDetailStore((s) => s.section)
  const setSection = useDeviceDetailStore((s) => s.setSection)
  const close = useDeviceDetailStore((s) => s.close)
  const open = deviceClass != null && deviceId != null
  const fp = useFloatingPanel(300, "max-h-[72vh]", { positionClass: "right-4 top-44", storageKey: "ops-device-detail" })

  const { data: alarmData } = useAlarms({ status: "live" }, open)
  const { data: tagsData } = useDeviceTags(open ? (deviceClass ?? undefined) : undefined, open ? (deviceId ?? undefined) : undefined)
  const { data: groundsData } = useFeatureGrounds(open ? (deviceClass ?? undefined) : undefined, open ? (deviceId ?? undefined) : undefined)
  const { data: incidentData } = useIncidents({ openOnly: true, enabled: open })

  if (!open) return null

  const alarms = (alarmData?.alarms ?? []).filter((a) => a.device_class === deviceClass && a.device_id === deviceId)
  const tags = tagsData?.tags ?? []
  const grounds = groundsData?.grounds ?? []
  const incident = (incidentData?.incidents ?? []).find(
    (i) => i.predicted_device_class === deviceClass && i.predicted_device_id === deviceId,
  )

  const tabs: { id: DeviceDetailSection; label: string; icon: React.ReactNode; count: number }[] = [
    { id: "alarms", label: "Alarms", icon: <Bell className="size-3.5" />, count: alarms.length },
    { id: "safety", label: "Safety", icon: <ShieldAlert className="size-3.5" />, count: tags.length + grounds.length },
    { id: "incident", label: "Incident", icon: <Flame className="size-3.5" />, count: incident ? 1 : 0 },
  ]

  return (
    <div ref={fp.panelRef} style={fp.shellStyle} className={fp.shellClassName}>
      <div className={fp.headerClassName} onPointerDown={fp.onHeaderPointerDown}>
        {fp.grip}
        <div className="min-w-0">
          <div className="truncate text-sm font-medium">{humanize(deviceClass!)}</div>
          <div className="text-muted-foreground font-mono text-xs">{shortId(deviceId!)}</div>
        </div>
        <button type="button" onClick={close} className="hover:bg-foreground/10 ml-auto rounded p-0.5" title="Close">
          <X className="size-4" />
        </button>
      </div>

      <div className="flex shrink-0 gap-1 border-b px-1">
        {tabs.map((t) => (
          <button
            key={t.id}
            type="button"
            onClick={() => setSection(t.id)}
            className={cn(
              "-mb-px flex items-center gap-1.5 border-b-2 px-2.5 py-1.5 text-xs font-medium",
              section === t.id
                ? "border-primary text-foreground"
                : "text-muted-foreground hover:text-foreground border-transparent",
            )}
          >
            {t.icon}
            {t.label}
            {t.count > 0 && <span className="bg-muted rounded px-1 text-[10px]">{t.count}</span>}
          </button>
        ))}
      </div>

      <div className="min-h-0 flex-1 overflow-y-auto p-3">
        {section === "alarms" &&
          (alarms.length === 0 ? (
            <Empty>No live alarms on this device.</Empty>
          ) : (
            <div className="space-y-2">
              {alarms.map((a) => (
                <div key={a.id} className="rounded-md border p-2">
                  <div className="flex items-center gap-2">
                    <span className="size-2 shrink-0 rounded-full" style={{ backgroundColor: SEVERITY_COLOR[a.severity] ?? "#64748b" }} />
                    <span className="text-xs font-medium capitalize">{a.severity}</span>
                    <span className="text-muted-foreground ml-auto text-[10px]">{a.acknowledged_at ? "ack'd" : "unack'd"}</span>
                  </div>
                  <div className="mt-1 text-sm">{a.message}</div>
                  <div className="text-muted-foreground mt-0.5 text-[10px]">{when(a.last_raised_at)}</div>
                </div>
              ))}
            </div>
          ))}

        {section === "safety" &&
          (tags.length === 0 && grounds.length === 0 ? (
            <Empty>No safety tags or grounds on this device.</Empty>
          ) : (
            <div className="space-y-2">
              {tags.map((t) => (
                <div key={t.id} className="rounded-md border p-2">
                  <div className="flex items-center gap-2">
                    <span className="size-2 shrink-0 rounded-full" style={{ backgroundColor: TAG_COLOR[t.tag_type] ?? "#64748b" }} />
                    <span className="text-xs font-medium">{humanize(t.tag_type)}</span>
                    {t.blocks_operation && <span className="text-destructive text-[10px]">blocks operation</span>}
                  </div>
                  {t.reason && <div className="mt-1 text-sm">{t.reason}</div>}
                  <div className="text-muted-foreground mt-0.5 text-[10px]">placed {when(t.placed_at)}</div>
                </div>
              ))}
              {grounds.map((g) => (
                <div key={g.id} className="rounded-md border p-2">
                  <div className="text-xs font-medium">Applied ground</div>
                  {g.reason && <div className="mt-1 text-sm">{g.reason}</div>}
                  <div className="text-muted-foreground mt-0.5 text-[10px]">applied {when(g.applied_at)}</div>
                </div>
              ))}
            </div>
          ))}

        {section === "incident" &&
          (!incident ? (
            <Empty>This device isn’t the blamed device of an open incident.</Empty>
          ) : (
            <div className="space-y-1.5 rounded-md border p-2 text-sm">
              <Row label="Status" value={humanize(incident.status)} />
              <Row label="Type" value={humanize(incident.outage_type)} />
              {incident.territory_name && <Row label="Territory" value={incident.territory_name} />}
              <Row label="Customers" value={incident.affected_customers != null ? String(incident.affected_customers) : "—"} />
              <Row label="Started" value={when(incident.started_at)} />
              {incident.etr && <Row label="ETR" value={when(incident.etr)} />}
            </div>
          ))}
      </div>
      {fp.resizeHandle}
    </div>
  )
}

function Empty({ children }: { children: React.ReactNode }) {
  return <div className="text-muted-foreground py-6 text-center text-xs">{children}</div>
}

function Row({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex items-center justify-between gap-2">
      <span className="text-muted-foreground text-xs">{label}</span>
      <span className="text-right">{value}</span>
    </div>
  )
}
