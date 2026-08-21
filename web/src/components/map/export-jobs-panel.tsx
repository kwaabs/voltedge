import { AlertCircle, Ban, CheckCircle2, Download, Loader2 } from "lucide-react"

import { Button } from "@/components/ui/button"
import { FloatingPanel } from "@/components/ui/floating-panel"
import {
  type ExportJobView,
  isJobPending,
  useCancelExport,
  useExportJobs,
} from "@/hooks/use-export-jobs"
import { ApiError } from "@/lib/api"
import { cn } from "@/lib/utils"
import { useExportStore } from "@/stores/export-store"
import { useToastStore } from "@/stores/toast-store"

/** Human labels per format code (matches the export dialog's FORMATS). */
const FORMAT_LABEL: Record<string, string> = {
  kmz: "KMZ",
  shp: "Shapefile",
  dxf: "DXF",
  cim: "CIM/XML",
}

/**
 * The floating panel that watches this user's export jobs (Phase 17). An export
 * is asynchronous now: the dialog enqueues a job and this panel -- opened in the
 * same handoff -- polls GET /export/jobs (see useExportJobs, which stops polling
 * once every job is terminal) and surfaces each one's status, then a download
 * link once the worker finishes. The list is server-owned and per-user, so it
 * shows recent history too, not just the job just submitted. Only mounted (and
 * only fetching) while open, so an unopened panel costs nothing.
 */
export function ExportJobsPanel() {
  const open = useExportStore((s) => s.jobsPanelOpen)
  const { data, isLoading, isError } = useExportJobs(open)

  if (!open) return null

  const jobs = data?.jobs ?? []

  return (
    <FloatingPanel
      title="Exports"
      onClose={() => useExportStore.getState().closeJobsPanel()}
      initialRect={{ x: window.innerWidth - 460, y: 72, width: 420, height: 380 }}
      minWidth={320}
      minHeight={200}
    >
      <div className="p-2">
        {isLoading ? (
          <p className="text-muted-foreground p-3 text-sm">Loading exports…</p>
        ) : isError ? (
          <p className="text-destructive p-3 text-sm">Could not load exports.</p>
        ) : jobs.length === 0 ? (
          <p className="text-muted-foreground p-3 text-sm">No exports yet.</p>
        ) : (
          <ul className="space-y-1.5">
            {jobs.map((job) => (
              <ExportJobRow key={job.id} job={job} />
            ))}
          </ul>
        )}
      </div>
    </FloatingPanel>
  )
}

function ExportJobRow({ job }: { job: ExportJobView }) {
  const pending = isJobPending(job.status)
  const label = FORMAT_LABEL[job.format] ?? job.format.toUpperCase()
  const cancel = useCancelExport()

  const onCancel = () =>
    cancel.mutate(job.id, {
      onError: (err) =>
        useToastStore
          .getState()
          .pushError(err instanceof ApiError ? err.message : "Could not cancel export."),
    })

  return (
    <li className="rounded-md border p-2.5 text-sm">
      <div className="flex items-center gap-2">
        <StatusIcon status={job.status} />
        <span className="font-medium">{label}</span>
        <span
          className={cn(
            "ml-auto text-xs capitalize",
            job.status === "failed" ? "text-destructive" : "text-muted-foreground",
          )}
        >
          {job.status}
        </span>
      </div>

      <div className="text-muted-foreground mt-1 flex flex-wrap gap-x-3 gap-y-0.5 text-xs">
        {job.feature_count != null && (
          <span>
            {job.feature_count.toLocaleString()} feature{job.feature_count === 1 ? "" : "s"}
          </span>
        )}
        {job.byte_size != null && <span>{formatBytes(job.byte_size)}</span>}
        <span>{formatTime(job.created_at)}</span>
      </div>

      {job.status === "failed" && job.error && (
        <p className="text-destructive mt-1.5 text-xs break-words">{job.error}</p>
      )}

      {job.status === "succeeded" && job.download_url && (
        <div className="mt-2">
          <Button asChild size="sm" variant="secondary">
            {/* Prefixed with the API base; a plain navigation carries the
             * session cookie, and the route 302-redirects to a presigned
             * RustFS URL whose content-disposition triggers the download
             * rather than navigating the app away. */}
            <a href={`/api${job.download_url}`}>
              <Download className="size-3.5" /> Download
            </a>
          </Button>
        </div>
      )}

      {pending && (
        <div className="mt-1.5 flex items-center justify-between gap-2">
          <p className="text-muted-foreground text-xs">
            {job.status === "queued" ? "Waiting for a worker…" : "Building the file…"}
          </p>
          <Button
            size="sm"
            variant="ghost"
            className="text-muted-foreground hover:text-destructive h-6 px-2 text-xs"
            onClick={onCancel}
            disabled={cancel.isPending}
          >
            Cancel
          </Button>
        </div>
      )}
    </li>
  )
}

function StatusIcon({ status }: { status: ExportJobView["status"] }) {
  if (status === "succeeded") return <CheckCircle2 className="size-4 shrink-0 text-emerald-600" />
  if (status === "failed") return <AlertCircle className="text-destructive size-4 shrink-0" />
  if (status === "cancelled") return <Ban className="text-muted-foreground size-4 shrink-0" />
  return <Loader2 className="text-muted-foreground size-4 shrink-0 animate-spin" />
}

/** Bytes to a short human string (1024-based, matching a file manager). */
function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  const units = ["KB", "MB", "GB", "TB"]
  let value = bytes / 1024
  let i = 0
  while (value >= 1024 && i < units.length - 1) {
    value /= 1024
    i++
  }
  return `${value.toFixed(value < 10 ? 1 : 0)} ${units[i]}`
}

/** A created_at ISO timestamp to a compact local time-of-day + date. */
function formatTime(iso: string): string {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return ""
  return d.toLocaleString(undefined, {
    month: "short",
    day: "numeric",
    hour: "numeric",
    minute: "2-digit",
  })
}
