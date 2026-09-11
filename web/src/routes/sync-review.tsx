import { type ReactNode, useState } from "react"
import {
  AlertTriangle,
  ChevronDown,
  ChevronRight,
  Check,
  Loader2,
  Settings2,
  Trash2,
  X,
} from "lucide-react"

import { FormDialog } from "@/components/admin/form-dialog"
import { inputClass } from "@/components/map/field-utils"
import { Button } from "@/components/ui/button"
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import { usePermission } from "@/hooks/use-auth"
import {
  type ReviewOp,
  type ReviewStatus,
  type SyncPolicyRow,
  useConflictDetail,
  useDeletePolicy,
  usePostOp,
  useRejectOp,
  useResolveOp,
  useSetPolicy,
  useSyncPolicies,
  useSyncReview,
} from "@/hooks/use-sync-review"
import { ApiError } from "@/lib/api"
import { useToastStore } from "@/stores/toast-store"

const labelClass = "text-xs font-medium"

/**
 * The field-sync review queue (docs/phase-23-sync-inbox.md, Phase 23 Slice 3): the
 * QA / system-of-record face of ops.sync_inbox. A feature class set to 'review' mode
 * holds its device edits 'pending_review' instead of auto-applying; a supervisor
 * inspects the proposed change here and POSTs the ones to apply or REJECTs the rest.
 * Viewing needs sync:review (the route gate); posting/rejecting and setting the
 * per-class policy need sync:post (the API gate -- the action controls hide when the
 * viewer lacks it).
 */
export function SyncReviewPage() {
  const can = usePermission()
  const canPost = can("sync:post")
  const [status, setStatus] = useState<ReviewStatus>("pending_review")
  const { data, isPending, isError } = useSyncReview(status)

  return (
    <div className="h-full overflow-y-auto p-4">
      <div className="mx-auto flex max-w-3xl flex-col gap-4">
        <div>
          <h1 className="text-lg font-semibold">Sync review</h1>
          <p className="text-muted-foreground text-sm">
            Field edits held for review before they reach production. Post an edit to apply it, or
            reject it with a reason (the field tech sees it under "needs attention"). Conflicts are
            edits to a feature that changed in production after the field snapshot — apply the
            device's value anyway, or keep production's.
          </p>
        </div>

        <PolicySection canPost={canPost} />

        <div className="flex items-center gap-1">
          {STATUS_TABS.map((tab) => (
            <Button
              key={tab.value}
              variant={status === tab.value ? "default" : "outline"}
              size="sm"
              onClick={() => setStatus(tab.value)}
            >
              {tab.label}
            </Button>
          ))}
        </div>

        {isPending ? (
          <div className="text-muted-foreground flex items-center gap-2 text-sm">
            <Loader2 className="size-4 animate-spin" />
            Loading…
          </div>
        ) : isError || !data ? (
          <p className="text-destructive text-sm">Could not load the review queue.</p>
        ) : data.ops.length === 0 ? (
          <p className="text-muted-foreground text-sm">{emptyMessage(status)}</p>
        ) : (
          <div className="flex flex-col gap-2">
            {data.ops.map((op) => (
              <ReviewOpCard key={op.op_id} op={op} canPost={canPost} />
            ))}
          </div>
        )}
      </div>
    </div>
  )
}

const STATUS_TABS: { value: ReviewStatus; label: string }[] = [
  { value: "pending_review", label: "Pending review" },
  { value: "conflict", label: "Conflicts" },
  { value: "rejected", label: "Rejected" },
  { value: "applied", label: "Applied" },
]

function emptyMessage(status: ReviewStatus): string {
  switch (status) {
    case "pending_review":
      return "Nothing is waiting for review. Edits to classes in review mode will appear here."
    case "conflict":
      return "No conflicts. Edits to a feature that changed in production after the field snapshot land here."
    case "rejected":
      return "No rejected edits."
    case "applied":
      return "No reviewed edits have been applied yet."
    default:
      return "Nothing here."
  }
}

/** One held op: kind + target + owner in the header, the proposed change + actions on expand. */
function ReviewOpCard({ op, canPost }: { op: ReviewOp; canPost: boolean }) {
  const [expanded, setExpanded] = useState(false)
  const [dialog, setDialog] = useState<null | "reject" | "keep">(null)
  const post = usePostOp()
  const reject = useRejectOp()
  const resolve = useResolveOp()

  const isPending = op.status === "pending_review"
  const isConflict = op.status === "conflict"
  const showActions = canPost && (isPending || isConflict)
  const busy = post.isPending || resolve.isPending

  // Pending -> Post; conflict -> Apply anyway (force through the version check).
  async function doAccept() {
    try {
      if (isConflict) await resolve.mutateAsync({ opId: op.op_id, action: "apply" })
      else await post.mutateAsync({ opId: op.op_id })
    } catch (err) {
      toastError(err, isConflict ? "Could not apply this edit." : "Could not post this edit.")
    }
  }

  return (
    <Card>
      <CardHeader
        className="flex cursor-pointer flex-row items-start gap-3 py-3"
        role="button"
        aria-expanded={expanded}
        onClick={() => setExpanded(!expanded)}
      >
        {expanded ? (
          <ChevronDown className="text-muted-foreground mt-0.5 size-4 shrink-0" />
        ) : (
          <ChevronRight className="text-muted-foreground mt-0.5 size-4 shrink-0" />
        )}
        <div className="min-w-0 flex-1">
          <CardTitle className="flex items-center gap-2 text-sm">
            <span className="truncate">{KIND_LABELS[op.kind] ?? op.kind}</span>
            {op.feature_class && (
              <span className="text-muted-foreground font-mono text-xs">{op.feature_class}</span>
            )}
            <StatusChip status={op.status} />
          </CardTitle>
          <CardDescription className="text-xs">{describeOp(op)}</CardDescription>
        </div>
        {showActions && (
          <div className="flex shrink-0 items-center gap-1" onClick={(e) => e.stopPropagation()}>
            <Button
              variant="ghost"
              size="icon"
              className="size-7 text-emerald-600"
              title={isConflict ? "Apply the device's edit anyway" : "Post (apply this edit)"}
              disabled={busy}
              onClick={doAccept}
            >
              {busy ? (
                <Loader2 className="size-4 animate-spin" />
              ) : (
                <Check className="size-4" />
              )}
            </Button>
            <Button
              variant="ghost"
              size="icon"
              className="text-destructive size-7"
              title={isConflict ? "Keep the production value (discard the device edit)" : "Reject this edit"}
              disabled={busy}
              onClick={() => setDialog(isConflict ? "keep" : "reject")}
            >
              <X className="size-4" />
            </Button>
          </div>
        )}
      </CardHeader>
      {expanded && <OpDetails op={op} />}

      {dialog === "reject" && (
        <ReasonDialog
          title="Reject this edit?"
          description={`This ${KIND_LABELS[op.kind] ?? op.kind} won't be applied. The field tech sees your reason and can redo the edit.`}
          submitLabel="Reject"
          placeholder="e.g. Wrong pole class — should be LV, not HV"
          requireReason
          saving={reject.isPending}
          onClose={() => setDialog(null)}
          onSubmit={async (reason) => {
            try {
              await reject.mutateAsync({ opId: op.op_id, reason })
              setDialog(null)
            } catch (err) {
              toastError(err, "Could not reject this edit.")
            }
          }}
        />
      )}

      {dialog === "keep" && (
        <ReasonDialog
          title="Keep the production value?"
          description="The device's edit is discarded and the current production value stays. The field tech sees your reason under “needs attention” and can redo the edit on top of the newer data."
          submitLabel="Keep production"
          placeholder="Optional — e.g. Office already corrected this from the drawing"
          requireReason={false}
          saving={resolve.isPending}
          onClose={() => setDialog(null)}
          onSubmit={async (reason) => {
            try {
              await resolve.mutateAsync({ opId: op.op_id, action: "keep", reason })
              setDialog(null)
            } catch (err) {
              toastError(err, "Could not resolve this conflict.")
            }
          }}
        />
      )}
    </Card>
  )
}

/** The expanded op: who/what/when, the base version, and the raw proposed payload. */
function OpDetails({ op }: { op: ReviewOp }) {
  return (
    <CardContent className="border-t px-4 py-3">
      <dl className="grid grid-cols-1 gap-x-6 gap-y-2 text-xs sm:grid-cols-2">
        <DetailRow label="Submitted by">
          {op.owner_name ? `${op.owner_name} · ${op.owner_email}` : op.owner_email}
        </DetailRow>
        <DetailRow label="Received">{formatTime(op.received_at)}</DetailRow>
        <DetailRow label="Feature">
          {op.feature_id ? <span className="font-mono break-all">{op.feature_id}</span> : "—"}
        </DetailRow>
        <DetailRow label="Submission">
          {op.submission_id ? <span className="font-mono break-all">{op.submission_id}</span> : "—"}
        </DetailRow>
        <DetailRow label="Base version">{op.base_version || "—"}</DetailRow>
        {op.reviewed_at && (
          <DetailRow label="Reviewed">
            {op.reviewer_name ? `${op.reviewer_name} · ` : ""}
            {formatTime(op.reviewed_at)}
          </DetailRow>
        )}
      </dl>
      {op.error && (
        <div className="mt-2 text-xs">
          <dt className="text-muted-foreground">Reason</dt>
          <dd className="text-destructive break-words">{op.error}</dd>
        </div>
      )}
      {op.status === "conflict" && <ConflictDiff op={op} />}
      {op.payload != null && (
        <div className="mt-3">
          <div className="text-muted-foreground mb-1 text-xs">Proposed change</div>
          <pre className="bg-muted max-h-64 overflow-auto rounded p-2 text-[11px] leading-relaxed">
            {JSON.stringify(op.payload, null, 2)}
          </pre>
        </div>
      )}
    </CardContent>
  )
}

/**
 * A conflict card's field-level diff: the device's proposed value vs the CURRENT
 * production value (fetched on expand via useConflictDetail). We don't have the field
 * snapshot the device started from, so this is a 2-way "device wants X, prod now has Y"
 * comparison -- the actionable one for the reviewer choosing apply vs keep -- not a
 * 3-way merge. Only attributes/name are diffed field by field; geometry is flagged.
 */
function ConflictDiff({ op }: { op: ReviewOp }) {
  const { data, isPending, isError } = useConflictDetail(op.op_id, true)
  return (
    <div className="mt-3 rounded border border-orange-200 bg-orange-50/60 p-2 dark:border-orange-900 dark:bg-orange-950/30">
      <div className="mb-2 flex items-center gap-1.5 text-xs font-medium text-orange-700 dark:text-orange-400">
        <AlertTriangle className="size-3.5 shrink-0" />
        Production changed after the field snapshot
      </div>
      {isPending ? (
        <div className="text-muted-foreground flex items-center gap-2 text-xs">
          <Loader2 className="size-3.5 animate-spin" />
          Loading current value…
        </div>
      ) : isError || !data ? (
        <p className="text-muted-foreground text-xs">Could not load the current production value.</p>
      ) : op.kind === "delete" ? (
        <p className="text-muted-foreground text-xs">
          The device wants to delete this feature. It still exists in production
          {data.current ? "" : " no longer"}.
        </p>
      ) : (
        <DiffTable current={data.current} proposed={op.payload} />
      )}
    </div>
  )
}

function DiffTable({
  current,
  proposed,
}: {
  current: Record<string, unknown> | null
  proposed: unknown
}) {
  const rows = diffFields(current, proposed)
  if (rows === null) {
    return (
      <p className="text-muted-foreground text-xs">
        Can't field-diff this change — compare the raw payload below against production.
      </p>
    )
  }
  if (rows.length === 0) {
    return (
      <p className="text-muted-foreground text-xs">
        No differing attributes — the conflict is on geometry or metadata only.
      </p>
    )
  }
  return (
    <table className="w-full text-left text-[11px]">
      <thead className="text-muted-foreground">
        <tr>
          <th className="py-0.5 pr-3 font-medium">Field</th>
          <th className="py-0.5 pr-3 font-medium">Production now</th>
          <th className="py-0.5 font-medium">Device wants</th>
        </tr>
      </thead>
      <tbody>
        {rows.map((r) => (
          <tr key={r.field} className="border-t border-orange-200/60 dark:border-orange-900/60">
            <td className="py-0.5 pr-3 font-mono break-all">{r.field}</td>
            <td className="py-0.5 pr-3 break-all">{fmtVal(r.current)}</td>
            <td className="py-0.5 font-medium break-all">{fmtVal(r.proposed)}</td>
          </tr>
        ))}
      </tbody>
    </table>
  )
}

/**
 * Diff a proposed update_attrs payload against the current production row. Returns the
 * changed fields (name + each attribute the device would set to a different value, plus
 * a geometry flag), or null if the payload isn't a shape we can field-diff.
 */
function diffFields(
  current: Record<string, unknown> | null,
  proposed: unknown,
): { field: string; current: unknown; proposed: unknown }[] | null {
  if (!proposed || typeof proposed !== "object") return null
  const p = proposed as Record<string, unknown>
  const rows: { field: string; current: unknown; proposed: unknown }[] = []

  if ("name" in p && !eq(p.name, current?.name)) {
    rows.push({ field: "name", current: current?.name, proposed: p.name })
  }

  const pAttrs = p.attributes
  const cAttrs = (current?.attributes as Record<string, unknown> | undefined) ?? undefined
  if (pAttrs && typeof pAttrs === "object") {
    for (const [k, pv] of Object.entries(pAttrs as Record<string, unknown>)) {
      const cv = cAttrs?.[k]
      if (!eq(pv, cv)) rows.push({ field: k, current: cv, proposed: pv })
    }
  }

  if (p.geometry !== undefined) {
    rows.push({ field: "geometry", current: "(current)", proposed: "(changed)" })
  }
  return rows
}

function eq(a: unknown, b: unknown): boolean {
  return JSON.stringify(a ?? null) === JSON.stringify(b ?? null)
}

function fmtVal(v: unknown): string {
  if (v === null || v === undefined) return "—"
  if (typeof v === "object") return JSON.stringify(v)
  return String(v)
}

/**
 * Captures a reason the field tech will see under "needs attention", shared by reject
 * (pending review) and keep-production (conflict). requireReason gates the submit for
 * reject; keep lets the reason be blank.
 */
function ReasonDialog({
  title,
  description,
  submitLabel,
  placeholder,
  requireReason,
  saving,
  onClose,
  onSubmit,
}: {
  title: string
  description: string
  submitLabel: string
  placeholder: string
  requireReason: boolean
  saving: boolean
  onClose: () => void
  onSubmit: (reason: string) => void
}) {
  const [reason, setReason] = useState("")
  return (
    <FormDialog
      open
      onOpenChange={(next) => !next && onClose()}
      title={title}
      description={description}
      submitLabel={submitLabel}
      canSubmit={!requireReason || reason.trim().length > 0}
      saving={saving}
      onSubmit={() => onSubmit(reason.trim())}
    >
      <label className={labelClass} htmlFor="reason-input">
        Reason{requireReason ? "" : " (optional)"}
      </label>
      <textarea
        id="reason-input"
        className={inputClass}
        rows={3}
        value={reason}
        onChange={(e) => setReason(e.target.value)}
        placeholder={placeholder}
        autoFocus
      />
    </FormDialog>
  )
}

/**
 * The apply-policy editor: the global default plus per-class overrides. Setting a
 * class (or the global default) to 'review' turns on QA mode for it; 'auto' returns
 * it to immediate apply. Read-only for a sync:review user without sync:post.
 */
function PolicySection({ canPost }: { canPost: boolean }) {
  const [open, setOpen] = useState(false)
  const { data, isPending } = useSyncPolicies()
  const setPolicy = useSetPolicy()
  const deletePolicy = useDeletePolicy()
  const [adding, setAdding] = useState(false)
  const [newClass, setNewClass] = useState("")
  const [newMode, setNewMode] = useState<"auto" | "review">("review")
  const [newDetect, setNewDetect] = useState(false)

  const defaultMode = data?.default_mode ?? "auto"
  const defaultDetect = data?.default_detect_conflicts ?? false
  const overrides = (data?.policies ?? []).filter((p) => p.feature_class !== "*")

  async function run(fn: () => Promise<unknown>, fallback: string) {
    try {
      await fn()
    } catch (err) {
      toastError(err, fallback)
    }
  }

  return (
    <Card>
      <CardHeader
        className="flex cursor-pointer flex-row items-center gap-2 py-3"
        role="button"
        aria-expanded={open}
        onClick={() => setOpen(!open)}
      >
        {open ? (
          <ChevronDown className="text-muted-foreground size-4 shrink-0" />
        ) : (
          <ChevronRight className="text-muted-foreground size-4 shrink-0" />
        )}
        <Settings2 className="text-muted-foreground size-4 shrink-0" />
        <CardTitle className="flex-1 text-sm">Apply policy</CardTitle>
        <CardDescription className="text-xs">
          Default: <span className="font-medium">{defaultMode}</span>
          {defaultDetect ? " · conflict check on" : ""}
          {overrides.length > 0 ? ` · ${overrides.length} override${overrides.length === 1 ? "" : "s"}` : ""}
        </CardDescription>
      </CardHeader>

      {open && (
        <CardContent className="border-t px-4 py-3">
          {isPending ? (
            <div className="text-muted-foreground flex items-center gap-2 text-sm">
              <Loader2 className="size-4 animate-spin" />
              Loading…
            </div>
          ) : (
            <div className="flex flex-col gap-4">
              <div className="flex flex-wrap items-center gap-x-3 gap-y-2 text-sm">
                <span className="text-muted-foreground w-28 shrink-0">All other classes</span>
                <ModeToggle
                  mode={defaultMode}
                  disabled={!canPost || setPolicy.isPending}
                  onChange={(mode) =>
                    run(
                      () => setPolicy.mutateAsync({ feature_class: "", mode, detect_conflicts: defaultDetect }),
                      "Could not update the default policy.",
                    )
                  }
                />
                <DetectToggle
                  on={defaultDetect}
                  disabled={!canPost || setPolicy.isPending}
                  onChange={(on) =>
                    run(
                      () => setPolicy.mutateAsync({ feature_class: "", mode: defaultMode, detect_conflicts: on }),
                      "Could not update the default policy.",
                    )
                  }
                />
              </div>

              <div className="flex flex-col gap-2">
                <div className={labelClass}>Per-class overrides</div>
                {overrides.length === 0 ? (
                  <p className="text-muted-foreground text-xs">
                    No overrides — every class follows the default above.
                  </p>
                ) : (
                  overrides.map((p) => (
                    <PolicyRow
                      key={p.feature_class}
                      policy={p}
                      canPost={canPost}
                      busy={setPolicy.isPending || deletePolicy.isPending}
                      onChange={(mode, detect) =>
                        run(
                          () =>
                            setPolicy.mutateAsync({
                              feature_class: p.feature_class,
                              mode,
                              detect_conflicts: detect,
                            }),
                          "Could not update this override.",
                        )
                      }
                      onRemove={() =>
                        run(
                          () => deletePolicy.mutateAsync({ feature_class: p.feature_class }),
                          "Could not remove this override.",
                        )
                      }
                    />
                  ))
                )}

                {canPost &&
                  (adding ? (
                    <div className="flex flex-wrap items-center gap-2">
                      <input
                        className={inputClass}
                        value={newClass}
                        onChange={(e) => setNewClass(e.target.value)}
                        placeholder="feature class code"
                        autoFocus
                      />
                      <ModeToggle mode={newMode} disabled={false} onChange={setNewMode} />
                      <DetectToggle on={newDetect} disabled={false} onChange={setNewDetect} />
                      <Button
                        size="sm"
                        disabled={!newClass.trim() || setPolicy.isPending}
                        onClick={() =>
                          run(async () => {
                            await setPolicy.mutateAsync({
                              feature_class: newClass.trim(),
                              mode: newMode,
                              detect_conflicts: newDetect,
                            })
                            setNewClass("")
                            setNewDetect(false)
                            setAdding(false)
                          }, "Could not add this override.")
                        }
                      >
                        Add
                      </Button>
                      <Button variant="ghost" size="sm" onClick={() => setAdding(false)}>
                        Cancel
                      </Button>
                    </div>
                  ) : (
                    <div>
                      <Button variant="outline" size="sm" onClick={() => setAdding(true)}>
                        Add override
                      </Button>
                    </div>
                  ))}
              </div>
            </div>
          )}
        </CardContent>
      )}
    </Card>
  )
}

function PolicyRow({
  policy,
  canPost,
  busy,
  onChange,
  onRemove,
}: {
  policy: SyncPolicyRow
  canPost: boolean
  busy: boolean
  onChange: (mode: "auto" | "review", detect: boolean) => void
  onRemove: () => void
}) {
  return (
    <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-sm">
      <span className="w-28 shrink-0 truncate font-mono text-xs">{policy.feature_class}</span>
      <ModeToggle
        mode={policy.mode}
        disabled={!canPost || busy}
        onChange={(mode) => onChange(mode, policy.detect_conflicts)}
      />
      <DetectToggle
        on={policy.detect_conflicts}
        disabled={!canPost || busy}
        onChange={(detect) => onChange(policy.mode, detect)}
      />
      {canPost && (
        <Button
          variant="ghost"
          size="icon"
          className="text-destructive size-7"
          title="Remove override"
          disabled={busy}
          onClick={onRemove}
        >
          <Trash2 className="size-4" />
        </Button>
      )}
    </div>
  )
}

function ModeToggle({
  mode,
  disabled,
  onChange,
}: {
  mode: "auto" | "review"
  disabled: boolean
  onChange: (mode: "auto" | "review") => void
}) {
  return (
    <div className="flex items-center gap-1">
      {(["auto", "review"] as const).map((m) => (
        <Button
          key={m}
          variant={mode === m ? "default" : "outline"}
          size="sm"
          disabled={disabled}
          onClick={() => mode !== m && onChange(m)}
        >
          {m}
        </Button>
      ))}
    </div>
  )
}

/**
 * The conflict-detection toggle -- an independent dimension from mode (a class can
 * auto-apply AND be conflict-checked). "off" is the backward-compatible silent LWW;
 * "on" holds edits to a feature that changed in production after the field snapshot.
 */
function DetectToggle({
  on,
  disabled,
  onChange,
}: {
  on: boolean
  disabled: boolean
  onChange: (on: boolean) => void
}) {
  return (
    <div className="flex items-center gap-1">
      <span className="text-muted-foreground text-xs">conflict check</span>
      {([["off", false] as const, ["on", true] as const]).map(([label, val]) => (
        <Button
          key={label}
          variant={on === val ? "default" : "outline"}
          size="sm"
          disabled={disabled}
          onClick={() => on !== val && onChange(val)}
        >
          {label}
        </Button>
      ))}
    </div>
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

const KIND_LABELS: Record<string, string> = {
  create: "New feature",
  update_attrs: "Attribute edit",
  delete: "Delete",
  establish_connection: "New connection",
  establish_relationship: "New relationship",
}

const STATUS_STYLES: Record<string, string> = {
  pending_review: "bg-amber-100 text-amber-700 dark:bg-amber-950 dark:text-amber-400",
  applied: "bg-emerald-100 text-emerald-700 dark:bg-emerald-950 dark:text-emerald-400",
  rejected: "bg-red-100 text-red-700 dark:bg-red-950 dark:text-red-400",
  conflict: "bg-orange-100 text-orange-700 dark:bg-orange-950 dark:text-orange-400",
}

function StatusChip({ status }: { status: string }) {
  const cls = STATUS_STYLES[status] ?? "bg-muted text-muted-foreground"
  return (
    <span className={`rounded px-1.5 py-0.5 text-xs font-medium ${cls}`}>
      {status.replace("_", " ")}
    </span>
  )
}

function describeOp(op: ReviewOp): string {
  const parts: string[] = []
  parts.push(op.owner_name || op.owner_email)
  parts.push(new Date(op.received_at).toLocaleString())
  return parts.join(" · ")
}

function formatTime(value?: string): string {
  if (!value) return "—"
  const d = new Date(value)
  return Number.isNaN(d.getTime()) ? "—" : d.toLocaleString()
}

function toastError(err: unknown, fallback: string) {
  useToastStore.getState().pushError(err instanceof ApiError ? err.message : fallback)
}
