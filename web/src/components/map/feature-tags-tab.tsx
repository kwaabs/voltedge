import { useState } from "react"
import { Check, Copy, Loader2, Plus, Printer, QrCode, RefreshCw, Star, XCircle } from "lucide-react"

import { Button } from "@/components/ui/button"
import { TagPrintSheet } from "@/components/map/tag-print-sheet"
import { usePermission } from "@/hooks/use-auth"
import {
  type AssetTag,
  type TagStatus,
  useFeatureTags,
  useMarkPrinted,
  useRegisterTag,
  useReissueTag,
  useSetPrimaryTag,
  useVoidTag,
} from "@/hooks/use-asset-tags"
import { ApiError } from "@/lib/api"
import { cn } from "@/lib/utils"
import { useSelectionStore } from "@/stores/selection-store"
import { useToastStore } from "@/stores/toast-store"

// The map attribute panel's "Tags" tab -- the asset-tagging module
// (api/internal/assettag) brought to the asset itself. Every feature is
// auto-minted a short scannable code; here an editor sees a feature's codes, can
// register a pre-existing legacy asset number, promote any code to the printed
// primary, void a wrong/damaged label, or reissue a fresh code when a physical
// tag is replaced. Reading is gated tags:read (the tab only shows when the
// panel allows it); the lifecycle controls self-gate on tags:write, mirroring
// the Faults tab. Minting itself is a DB trigger -- there is no "create" here.

const inputClass = "border-input bg-background w-full rounded-sm border px-2 py-1 text-sm"

function toastError(err: unknown, fallback: string) {
  useToastStore.getState().pushError(err instanceof ApiError ? err.message : fallback)
}

const TAG_STATUS_BADGE: Record<TagStatus, string> = {
  active: "bg-emerald-500/15 text-emerald-600 dark:text-emerald-400",
  void: "bg-red-500/15 text-red-600 dark:text-red-400",
  superseded: "bg-neutral-500/15 text-neutral-600 dark:text-neutral-400",
}

function Badge({ className, children }: { className?: string; children: React.ReactNode }) {
  return (
    <span
      className={cn("inline-flex shrink-0 items-center gap-1 rounded-md px-1.5 py-0.5 text-xs font-medium", className)}
    >
      {children}
    </span>
  )
}

function SectionHeader({ title, count, action }: { title: string; count: number; action?: React.ReactNode }) {
  return (
    <div className="flex items-center justify-between gap-2">
      <div className="flex items-center gap-1.5">
        <QrCode className="text-muted-foreground size-3.5" />
        <span className="text-xs font-semibold tracking-wide uppercase">{title}</span>
        <span className="text-muted-foreground text-xs">({count})</span>
      </div>
      {action}
    </div>
  )
}

// --- register a legacy code ------------------------------------------------

/** Record a pre-existing / legacy asset number a utility already paints on its
 * plant, optionally promoting it to the feature's printed primary. */
function RegisterTagForm({
  featureClassCode,
  featureId,
  onDone,
}: {
  featureClassCode: string
  featureId: string
  onDone: () => void
}) {
  const register = useRegisterTag()
  const [code, setCode] = useState("")
  const [makePrimary, setMakePrimary] = useState(false)

  const canSave = !register.isPending && code.trim() !== ""

  const submit = () => {
    if (!canSave) return
    register.mutate(
      {
        feature_class_code: featureClassCode,
        feature_id: featureId,
        tag_code: code.trim(),
        make_primary: makePrimary,
      },
      { onSuccess: onDone, onError: (err) => toastError(err, "Could not register this code.") },
    )
  }

  return (
    <div className="flex flex-col gap-2 rounded-md border p-2">
      <input
        autoFocus
        value={code}
        onChange={(e) => setCode(e.target.value)}
        onKeyDown={(e) => e.key === "Enter" && submit()}
        placeholder="Existing / legacy asset number"
        className={inputClass}
      />
      <label className="flex items-center gap-2 text-xs">
        <input type="checkbox" checked={makePrimary} onChange={(e) => setMakePrimary(e.target.checked)} />
        Make this the primary (printed) code
      </label>
      <div className="flex items-center gap-2">
        <Button size="sm" onClick={submit} disabled={!canSave}>
          {register.isPending ? <Loader2 className="size-4 animate-spin" /> : <Plus className="size-4" />}
          Register
        </Button>
        <Button variant="ghost" size="sm" onClick={onDone}>
          Cancel
        </Button>
      </div>
    </div>
  )
}

function CopyButton({ code }: { code: string }) {
  const [copied, setCopied] = useState(false)
  const copy = () => {
    navigator.clipboard?.writeText(code).then(
      () => {
        setCopied(true)
        setTimeout(() => setCopied(false), 1200)
      },
      () => toastError(null, "Could not copy the code."),
    )
  }
  return (
    <button
      type="button"
      onClick={copy}
      title="Copy code"
      className="text-muted-foreground hover:text-foreground shrink-0"
    >
      {copied ? <Check className="size-3.5 text-emerald-500" /> : <Copy className="size-3.5" />}
    </button>
  )
}

function TagRow({
  tag,
  canWrite,
  onPrint,
}: {
  tag: AssetTag
  canWrite: boolean
  onPrint: (tag: AssetTag) => void
}) {
  const setPrimary = useSetPrimaryTag()
  const voidTag = useVoidTag()
  const reissue = useReissueTag()
  const busy = setPrimary.isPending || voidTag.isPending || reissue.isPending

  const promote = () =>
    setPrimary.mutate({ code: tag.tag_code }, { onError: (err) => toastError(err, "Could not set this as primary.") })
  const retire = () =>
    voidTag.mutate({ code: tag.tag_code }, { onError: (err) => toastError(err, "Could not void this tag.") })
  const replace = () =>
    reissue.mutate({ code: tag.tag_code }, { onError: (err) => toastError(err, "Could not reissue this tag.") })

  const isActive = tag.status === "active"

  return (
    <div className="rounded-md border p-2 text-sm">
      <div className="flex items-start justify-between gap-2">
        <div className="min-w-0">
          <div className="flex items-center gap-1.5">
            <span className="truncate font-mono font-medium">{tag.tag_code}</span>
            <CopyButton code={tag.tag_code} />
          </div>
          <p className="text-muted-foreground text-xs">
            {tag.source === "generated" ? "Generated" : "Legacy"}
            {tag.print_count > 0 && <> · printed {tag.print_count}×</>}
          </p>
        </div>
        <div className="flex shrink-0 flex-col items-end gap-1">
          {tag.is_primary && (
            <Badge className="bg-amber-500/15 text-amber-600 dark:text-amber-400">
              <Star className="size-3 fill-current" />
              Primary
            </Badge>
          )}
          <Badge className={cn("capitalize", TAG_STATUS_BADGE[tag.status])}>{tag.status}</Badge>
        </div>
      </div>
      {isActive && (
        <div className="mt-2 flex flex-wrap gap-1">
          {/* Printing is a read-tier action -- a field tech can produce a label; only
              the print_count bump (in the tab) is gated on tags:write. */}
          <Button variant="outline" size="sm" className="h-6 px-2 text-xs" onClick={() => onPrint(tag)}>
            <Printer className="size-3" />
            Print
          </Button>
          {canWrite && !tag.is_primary && (
            <Button variant="outline" size="sm" className="h-6 px-2 text-xs" disabled={busy} onClick={promote}>
              <Star className="size-3" />
              Set primary
            </Button>
          )}
          {canWrite && (
            <Button variant="outline" size="sm" className="h-6 px-2 text-xs" disabled={busy} onClick={replace}>
              <RefreshCw className="size-3" />
              Reissue
            </Button>
          )}
          {canWrite && !tag.is_primary && (
            <Button
              variant="ghost"
              size="sm"
              className="text-muted-foreground h-6 px-2 text-xs"
              disabled={busy}
              onClick={retire}
            >
              <XCircle className="size-3" />
              Void
            </Button>
          )}
        </div>
      )}
    </div>
  )
}

// --- the tab ---------------------------------------------------------------

export function TagsTab() {
  const selected = useSelectionStore((s) => s.selected)
  const canWrite = usePermission()("tags:write")
  const markPrinted = useMarkPrinted()
  const [adding, setAdding] = useState(false)
  const [printing, setPrinting] = useState<AssetTag | null>(null)

  const featureClassCode = selected?.featureClassCode
  const featureId = selected?.featureId

  const tags = useFeatureTags(featureClassCode, featureId)

  if (!selected || !featureClassCode || !featureId) return null

  const rows = tags.data?.tags ?? []
  const subtitle = `${featureClassCode} · ${featureId.slice(0, 8)}`

  return (
    <div className="flex flex-col gap-2">
      <SectionHeader
        title="Tags"
        count={rows.length}
        action={
          canWrite && !adding ? (
            <Button variant="outline" size="sm" className="h-6 gap-1 px-2 text-xs" onClick={() => setAdding(true)}>
              <Plus className="size-3" />
              Legacy code
            </Button>
          ) : undefined
        }
      />
      {adding && (
        <RegisterTagForm featureClassCode={featureClassCode} featureId={featureId} onDone={() => setAdding(false)} />
      )}
      {tags.isPending ? (
        <div className="text-muted-foreground flex items-center gap-2 text-sm">
          <Loader2 className="size-4 animate-spin" />
          Loading…
        </div>
      ) : tags.isError ? (
        <p className="text-destructive text-sm">Could not load tags.</p>
      ) : rows.length === 0 ? (
        <p className="text-muted-foreground text-sm">No tags on this asset yet.</p>
      ) : (
        rows.map((tag) => (
          <TagRow key={tag.tag_code} tag={tag} canWrite={canWrite} onPrint={setPrinting} />
        ))
      )}
      {printing && (
        <TagPrintSheet
          tags={[{ code: printing.tag_code, subtitle }]}
          onClose={() => setPrinting(null)}
          onPrinted={() => {
            if (canWrite) markPrinted.mutate({ code: printing.tag_code })
          }}
        />
      )}
    </div>
  )
}
