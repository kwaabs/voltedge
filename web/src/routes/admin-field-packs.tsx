import { type ReactNode, useState } from "react"
import { BellOff, BellRing, ChevronDown, ChevronRight, Eye, EyeOff, Loader2, Package, Trash2 } from "lucide-react"

import { ConfirmDeleteDialog } from "@/components/admin/confirm-delete-dialog"
import { Button } from "@/components/ui/button"
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import { useFieldTrackingPolicy, useSetFieldTrackingPolicy } from "@/hooks/use-field-tracking-policy"
import {
  type AdminFieldPack,
  useAdminFieldPacks,
  useDeletePack,
  useSetPackDisabled,
} from "@/hooks/use-field-packs"
import { ApiError } from "@/lib/api"
import { useToastStore } from "@/stores/toast-store"

/** Org-wide field-tracking kill-switch (field_tracking_enabled, default ON). An
 * opt-out for jurisdictions/policies that forbid recording staff location; off
 * stops the movement trail AND the located edits, device-side and server-side.
 * All-or-nothing, org-wide (not per worker). The field app reads the same flag to
 * stop recording and to drive its in-app disclosure. */
function FieldTrackingPolicyCard() {
  const { data } = useFieldTrackingPolicy()
  const setPolicy = useSetFieldTrackingPolicy()
  const enabled = data?.enabled ?? true
  const disclosure = data?.disclosure_enabled ?? true

  function setTracking() {
    const next = !enabled
    setPolicy.mutate(
      { enabled: next },
      {
        onSuccess: () =>
          useToastStore.getState().pushSuccess(next ? "Field tracking is now ON." : "Field tracking is now OFF."),
        onError: (e) =>
          useToastStore.getState().pushError(e instanceof ApiError ? e.message : "Could not change the tracking policy."),
      },
    )
  }

  function setDisclosure() {
    const next = !disclosure
    setPolicy.mutate(
      { disclosure_enabled: next },
      {
        onSuccess: () =>
          useToastStore
            .getState()
            .pushSuccess(next ? "Crews will be told they're recorded." : "Crews will no longer be told they're recorded."),
        onError: (e) =>
          useToastStore.getState().pushError(e instanceof ApiError ? e.message : "Could not change the disclosure setting."),
      },
    )
  }

  return (
    <Card>
      <CardHeader className="pb-2">
        <CardTitle className="text-sm">Field location tracking</CardTitle>
        <CardDescription>
          When on, the field app records each crew's movement trail and the location of every edit (reviewed under
          Field trails). Turning it off stops all of it — the trail and the located edits — on the device and on the
          server. Org-wide, all or nothing.
        </CardDescription>
      </CardHeader>
      <CardContent className="grid items-start gap-3 pt-0 sm:grid-cols-2">
        <div className="flex items-center justify-between rounded-md border p-3">
          <div className="flex items-center gap-2">
            {enabled ? (
              <Eye className="size-4 shrink-0 text-emerald-600" />
            ) : (
              <EyeOff className="text-muted-foreground size-4 shrink-0" />
            )}
            <div>
              <p className="text-sm font-medium">Tracking {enabled ? "on" : "off"}</p>
              <p className="text-muted-foreground text-xs">
                {enabled ? "Crews' movements and edit locations are recorded." : "No field location is recorded anywhere."}
              </p>
            </div>
          </div>
          <Button
            size="sm"
            variant={enabled ? "outline" : "default"}
            onClick={setTracking}
            disabled={setPolicy.isPending}
            className="shrink-0"
          >
            {setPolicy.isPending && <Loader2 className="size-4 animate-spin" />}
            Turn {enabled ? "off" : "on"}
          </Button>
        </div>

        {/* Whether the field app TELLS crews they're recorded (the in-app disclosure
            notice). Independent of tracking: an org may record transparently (both on)
            or record without disclosing — a deliberate, auditable choice with its own
            legal weight, so it's a separate switch. */}
        <div className={`flex items-center justify-between rounded-md border p-3 ${enabled ? "" : "opacity-60"}`}>
          <div className="flex items-center gap-2">
            {disclosure ? (
              <BellRing className="size-4 shrink-0 text-emerald-600" />
            ) : (
              <BellOff className="text-muted-foreground size-4 shrink-0" />
            )}
            <div>
              <p className="text-sm font-medium">Inform crews {disclosure ? "on" : "off"}</p>
              <p className="text-muted-foreground text-xs">
                {!enabled
                  ? "Nothing to disclose while tracking is off — turn tracking on to use this."
                  : disclosure
                    ? "The field app shows crews a notice that their location is being recorded."
                    : "The field app does NOT tell crews they're being recorded — make sure this is lawful where you operate."}
              </p>
            </div>
          </div>
          <Button
            size="sm"
            variant={disclosure ? "outline" : "default"}
            onClick={setDisclosure}
            disabled={setPolicy.isPending || !enabled}
            className="shrink-0"
          >
            {setPolicy.isPending && <Loader2 className="size-4 animate-spin" />}
            Turn {disclosure ? "off" : "on"}
          </Button>
        </div>
      </CardContent>
    </Card>
  )
}

/**
 * Pack management (docs/phase-21-field-tool.md). Every field-tool region pack,
 * across every user, with the two administrative actions the field-pack lifecycle
 * was missing: DISABLE (hide a pack from its owner's tablet without destroying it
 * -- reversible) and DELETE (reap its RustFS artifacts and the row -- permanent).
 * The goal is keeping a field tech's device list uncluttered; the API
 * (admin:field-packs) is the real gate, this page just drives it.
 */
export function AdminFieldPacksPage() {
  const { data, isPending, isError } = useAdminFieldPacks()
  const setDisabled = useSetPackDisabled()
  const deletePack = useDeletePack()
  const [deleteTarget, setDeleteTarget] = useState<AdminFieldPack | null>(null)
  const [expandedId, setExpandedId] = useState<string | null>(null)

  async function toggleDisabled(pack: AdminFieldPack) {
    try {
      await setDisabled.mutateAsync({ id: pack.id, disabled: !pack.disabled })
    } catch (err) {
      useToastStore
        .getState()
        .pushError(err instanceof ApiError ? err.message : "Could not update this pack.")
    }
  }

  return (
    <div className="h-full overflow-y-auto p-4">
      <div className="flex w-full flex-col gap-4">
        <div>
          <h1 className="text-lg font-semibold">Field packs</h1>
          <p className="text-muted-foreground text-sm">
            Offline region packs built for the field tool, across every user. Disable a pack to
            hide it from its owner's tablet (reversible), or delete it to reap its storage for good.
          </p>
        </div>

        <FieldTrackingPolicyCard />

        {isPending ? (
          <div className="text-muted-foreground flex items-center gap-2 text-sm">
            <Loader2 className="size-4 animate-spin" />
            Loading packs...
          </div>
        ) : isError || !data ? (
          <p className="text-destructive text-sm">Could not load field packs.</p>
        ) : data.packs.length === 0 ? (
          <p className="text-muted-foreground text-sm">No field packs have been built yet.</p>
        ) : (
          <div className="flex flex-col gap-2">
            {data.packs.map((pack) => {
              const expanded = expandedId === pack.id
              return (
                <Card key={pack.id} className={pack.disabled ? "opacity-60" : undefined}>
                  <CardHeader
                    className="flex cursor-pointer flex-row items-start gap-3 py-3"
                    role="button"
                    aria-expanded={expanded}
                    onClick={() => setExpandedId(expanded ? null : pack.id)}
                  >
                    {expanded ? (
                      <ChevronDown className="text-muted-foreground mt-0.5 size-4 shrink-0" />
                    ) : (
                      <ChevronRight className="text-muted-foreground mt-0.5 size-4 shrink-0" />
                    )}
                    <Package className="text-muted-foreground mt-0.5 size-4 shrink-0" />
                    <div className="min-w-0 flex-1">
                      <CardTitle className="flex items-center gap-2 text-sm">
                        <span className="truncate">{pack.owner_name || pack.owner_email}</span>
                        <StatusChip status={pack.status} />
                        {pack.disabled && (
                          <span className="rounded bg-amber-100 px-1.5 py-0.5 text-xs font-medium text-amber-700 dark:bg-amber-950 dark:text-amber-400">
                            Hidden
                          </span>
                        )}
                      </CardTitle>
                      <CardDescription className="text-xs">{describePack(pack)}</CardDescription>
                      <CardDescription className="text-muted-foreground/70 font-mono text-[11px]">
                        {pack.id}
                      </CardDescription>
                    </div>
                    <div
                      className="flex shrink-0 items-center gap-1"
                      onClick={(e) => e.stopPropagation()}
                    >
                      <Button
                        variant="ghost"
                        size="icon"
                        className="size-7"
                        title={pack.disabled ? "Show on device" : "Hide from device"}
                        disabled={setDisabled.isPending}
                        onClick={() => toggleDisabled(pack)}
                      >
                        {pack.disabled ? <Eye className="size-4" /> : <EyeOff className="size-4" />}
                      </Button>
                      <Button
                        variant="ghost"
                        size="icon"
                        className="text-destructive size-7"
                        title="Delete pack"
                        onClick={() => setDeleteTarget(pack)}
                      >
                        <Trash2 className="size-4" />
                      </Button>
                    </div>
                  </CardHeader>
                  {expanded && <PackDetails pack={pack} />}
                </Card>
              )
            })}
          </div>
        )}
      </div>

      <ConfirmDeleteDialog
        open={deleteTarget !== null}
        title="Delete this pack?"
        description="Permanently deletes the pack and its offline artifacts (the tile set and data pack) from storage. The owner's device keeps any copy already downloaded until they remove it there. This can't be undone."
        onOpenChange={(open) => !open && setDeleteTarget(null)}
        onConfirm={async () => {
          if (!deleteTarget) return
          await deletePack.mutateAsync({ id: deleteTarget.id })
        }}
      />
    </div>
  )
}

/**
 * The expanded contents of a pack card: everything the one-line summary elides --
 * the owner's email, the full class list, exact sizes, the geographic extent, and
 * the build/snapshot timeline (plus the failure reason for a failed build). A
 * read-only inspector; every action lives in the header.
 */
function PackDetails({ pack }: { pack: AdminFieldPack }) {
  const classes = pack.feature_classes ?? []
  return (
    <CardContent className="border-t px-4 py-3">
      <dl className="grid grid-cols-1 gap-x-6 gap-y-2 text-xs sm:grid-cols-2">
        <DetailRow label="Owner">
          {pack.owner_name ? `${pack.owner_name} · ${pack.owner_email}` : pack.owner_email}
        </DetailRow>
        <DetailRow label="Status">
          {pack.status}
          {pack.disabled ? " · hidden from device" : ""}
        </DetailRow>
        <DetailRow label="Feature classes">
          {classes.length > 0 ? classes.join(", ") : "—"}
        </DetailRow>
        <DetailRow label="Features">
          {pack.feature_count != null ? pack.feature_count.toLocaleString() : "—"}
        </DetailRow>
        <DetailRow label="Attachments">
          {pack.attachment_count != null ? pack.attachment_count.toLocaleString() : "—"}
        </DetailRow>
        <DetailRow label="Tile set">
          {pack.tiles_bytes ? formatBytes(pack.tiles_bytes) : "—"}
        </DetailRow>
        <DetailRow label="Data pack">
          {pack.data_bytes ? formatBytes(pack.data_bytes) : "—"}
        </DetailRow>
        <DetailRow label="Extent (bbox)">{formatBbox(pack.bbox)}</DetailRow>
        <DetailRow label="Snapshot">{formatTime(pack.snapshot_at)}</DetailRow>
        <DetailRow label="Requested">{formatTime(pack.created_at)}</DetailRow>
        <DetailRow label="Finished">{formatTime(pack.finished_at)}</DetailRow>
        {pack.error && (
          <div className="sm:col-span-2">
            <dt className="text-muted-foreground">Error</dt>
            <dd className="text-destructive break-words">{pack.error}</dd>
          </div>
        )}
      </dl>
    </CardContent>
  )
}

function DetailRow({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="flex flex-col gap-0.5">
      <dt className="text-muted-foreground">{label}</dt>
      <dd className="break-words">{children}</dd>
    </div>
  )
}

const STATUS_STYLES: Record<AdminFieldPack["status"], string> = {
  queued: "bg-muted text-muted-foreground",
  running: "bg-blue-100 text-blue-700 dark:bg-blue-950 dark:text-blue-400",
  succeeded: "bg-emerald-100 text-emerald-700 dark:bg-emerald-950 dark:text-emerald-400",
  failed: "bg-red-100 text-red-700 dark:bg-red-950 dark:text-red-400",
  cancelled: "bg-muted text-muted-foreground",
}

function StatusChip({ status }: { status: AdminFieldPack["status"] }) {
  return (
    <span className={`rounded px-1.5 py-0.5 text-xs font-medium ${STATUS_STYLES[status]}`}>
      {status}
    </span>
  )
}

/** A one-line summary: what the pack covers and how big it is. */
function describePack(pack: AdminFieldPack): string {
  const parts: string[] = []
  const classes = pack.feature_classes ?? []
  if (classes.length > 0) {
    parts.push(classes.length <= 3 ? classes.join(", ") : `${classes.length} classes`)
  }
  if (pack.feature_count != null) parts.push(`${pack.feature_count.toLocaleString()} features`)
  const bytes = (pack.tiles_bytes ?? 0) + (pack.data_bytes ?? 0)
  if (bytes > 0) parts.push(formatBytes(bytes))
  parts.push(new Date(pack.created_at).toLocaleString())
  return parts.join(" · ")
}

/** A bbox as [minLon, minLat, maxLon, maxLat] → a compact lon/lat range, or a dash. */
function formatBbox(bbox?: number[]): string {
  if (!bbox || bbox.length < 4) return "—"
  const [minLon, minLat, maxLon, maxLat] = bbox
  return `${minLon.toFixed(4)}, ${minLat.toFixed(4)} → ${maxLon.toFixed(4)}, ${maxLat.toFixed(4)}`
}

function formatTime(value?: string): string {
  if (!value) return "—"
  const d = new Date(value)
  return Number.isNaN(d.getTime()) ? "—" : d.toLocaleString()
}

function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  const units = ["KB", "MB", "GB"]
  let value = bytes / 1024
  let i = 0
  while (value >= 1024 && i < units.length - 1) {
    value /= 1024
    i++
  }
  return `${value.toFixed(value < 10 ? 1 : 0)} ${units[i]}`
}
