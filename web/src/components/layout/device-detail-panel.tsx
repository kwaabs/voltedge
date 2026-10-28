import { Fragment } from "react"
import { FileText, Loader2, MapPin, X } from "lucide-react"
import { useNavigate } from "react-router"

import { Button } from "@/components/ui/button"
import { type Attachment, fetchAttachmentUrl, useAttachments } from "@/hooks/use-attachments"
import { useFeature } from "@/hooks/use-feature"
import { useDeviceOperations } from "@/hooks/use-operations"
import { cn } from "@/lib/utils"
import { useSelectionStore } from "@/stores/selection-store"
import { useToastStore } from "@/stores/toast-store"

function humanize(s: string): string {
  return s.replace(/_/g, " ")
}
function formatWhen(iso: string): string {
  return new Date(iso).toLocaleString()
}
function fmtValue(v: unknown): string {
  if (v == null || v === "") return "—"
  if (typeof v === "object") return JSON.stringify(v)
  return String(v)
}
function fmtBytes(n: number): string {
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${Math.round(n / 1024)} KB`
  return `${(n / (1024 * 1024)).toFixed(1)} MB`
}

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

/**
 * The right-hand master-detail panel for one device, shared by the Operations and
 * Live state tabs. It shows a concise feature summary (device_state + attributes)
 * and the device's operation history, and carries the "View on map" button — so
 * jumping to the map is a deliberate action here, not a side effect of clicking a
 * table row. The host page renders it beside the (shrunk) table and owns the
 * open/close state.
 */
export function DeviceDetailPanel({
  featureClass,
  featureId,
  onClose,
}: {
  featureClass: string
  featureId: string
  onClose: () => void
}) {
  const navigate = useNavigate()
  const feature = useFeature(featureClass, featureId)
  const ops = useDeviceOperations(featureClass, featureId)
  // Attachments ride attachments:read; a user without it simply errors, so the
  // section only renders when there are ready files (never a dead header).
  const attachments = useAttachments(featureClass, featureId)

  function viewOnMap() {
    useSelectionStore.getState().select(featureClass, featureId, { focus: true })
    navigate("/")
  }

  async function openAttachment(a: Attachment) {
    try {
      const url = await fetchAttachmentUrl(featureClass, featureId, a.id)
      window.open(url, "_blank", "noopener,noreferrer")
    } catch {
      useToastStore.getState().pushError("Could not open the attachment.")
    }
  }

  const attrs = feature.data?.attributes ?? {}
  const deviceState = attrs.device_state != null ? String(attrs.device_state) : undefined
  const operations = ops.data?.operations ?? []
  const files = attachments.data?.attachments ?? []
  const imageFiles = files.filter((a) => a.kind === "image" && a.thumb_url)
  const otherFiles = files.filter((a) => !(a.kind === "image" && a.thumb_url))

  return (
    <aside className="bg-card flex w-[340px] shrink-0 flex-col gap-3 overflow-y-auto rounded-lg border p-3">
      <div className="flex items-start justify-between gap-2">
        <div className="min-w-0">
          <div className="text-sm font-semibold">{humanize(featureClass)}</div>
          <div className="text-muted-foreground font-mono text-xs break-all">{featureId}</div>
        </div>
        <Button variant="ghost" size="icon" className="size-7 shrink-0" onClick={onClose} title="Close">
          <X className="size-4" />
        </Button>
      </div>

      <Button variant="outline" size="sm" onClick={viewOnMap}>
        <MapPin className="size-4" />
        View on map
      </Button>

      <section className="flex flex-col gap-1.5">
        <h3 className="text-muted-foreground text-[11px] font-medium tracking-wide uppercase">Details</h3>
        {feature.isPending ? (
          <div className="text-muted-foreground flex items-center gap-2 text-xs">
            <Loader2 className="size-3.5 animate-spin" /> Loading…
          </div>
        ) : feature.isError || !feature.data ? (
          <p className="text-destructive text-xs">Could not load this feature.</p>
        ) : (
          <dl className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 text-xs">
            {deviceState && (
              <>
                <dt className="text-muted-foreground">Device state</dt>
                <dd className="font-medium">{deviceState}</dd>
              </>
            )}
            {feature.data.name && (
              <>
                <dt className="text-muted-foreground">Name</dt>
                <dd className="break-all">{feature.data.name}</dd>
              </>
            )}
            {feature.data.lifecycle_status && (
              <>
                <dt className="text-muted-foreground">Lifecycle</dt>
                <dd className="capitalize">{humanize(feature.data.lifecycle_status.toLowerCase())}</dd>
              </>
            )}
            {Object.entries(attrs)
              .filter(([k]) => k !== "device_state")
              .map(([k, v]) => (
                <Fragment key={k}>
                  <dt className="text-muted-foreground break-all">{humanize(k)}</dt>
                  <dd className="break-all">{fmtValue(v)}</dd>
                </Fragment>
              ))}
          </dl>
        )}
      </section>

      {files.length > 0 && (
        <section className="flex flex-col gap-1.5">
          <h3 className="text-muted-foreground text-[11px] font-medium tracking-wide uppercase">Attachments</h3>
          {imageFiles.length > 0 && (
            <div className="flex flex-wrap gap-2">
              {imageFiles.map((a) => (
                <button
                  key={a.id}
                  type="button"
                  onClick={() => openAttachment(a)}
                  title={a.filename}
                  className="transition hover:opacity-80"
                >
                  <img src={a.thumb_url} alt={a.filename} className="size-14 rounded border object-cover" />
                </button>
              ))}
            </div>
          )}
          {otherFiles.length > 0 && (
            <ul className="flex flex-col gap-1">
              {otherFiles.map((a) => (
                <li key={a.id}>
                  <button
                    type="button"
                    onClick={() => openAttachment(a)}
                    title={a.filename}
                    className="hover:bg-muted/60 flex w-full items-center gap-2 rounded border p-1.5 text-left text-xs"
                  >
                    <FileText className="text-muted-foreground size-4 shrink-0" />
                    <span className="min-w-0 flex-1 truncate">{a.filename}</span>
                    <span className="text-muted-foreground shrink-0">{fmtBytes(a.byte_size)}</span>
                  </button>
                </li>
              ))}
            </ul>
          )}
        </section>
      )}

      <section className="flex flex-col gap-1.5">
        <h3 className="text-muted-foreground text-[11px] font-medium tracking-wide uppercase">Operation history</h3>
        {ops.isPending ? (
          <div className="text-muted-foreground flex items-center gap-2 text-xs">
            <Loader2 className="size-3.5 animate-spin" /> Loading…
          </div>
        ) : ops.isError ? (
          <p className="text-destructive text-xs">Could not load operations.</p>
        ) : operations.length === 0 ? (
          <p className="text-muted-foreground text-xs">No operations recorded for this device.</p>
        ) : (
          <ul className="flex flex-col gap-1.5">
            {operations.map((op) => (
              <li key={op.id} className="border-border/60 rounded border p-1.5 text-xs">
                <div className="flex items-center justify-between gap-2">
                  <span
                    className={cn(
                      "rounded px-1.5 py-0.5 text-[11px] font-medium capitalize",
                      OP_BADGE[op.op_type] ?? "bg-muted text-muted-foreground",
                    )}
                  >
                    {humanize(op.op_type)}
                  </span>
                  <span className="text-muted-foreground">{formatWhen(op.occurred_at)}</span>
                </div>
                <div className="text-muted-foreground mt-0.5 capitalize">
                  {humanize(op.source)}
                  {op.cause ? ` · ${op.cause}` : ""}
                </div>
              </li>
            ))}
          </ul>
        )}
      </section>
    </aside>
  )
}
