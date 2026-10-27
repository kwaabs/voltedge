import { useState } from "react"
import { Loader2, Unplug, Zap } from "lucide-react"

import { ConfirmDeleteDialog } from "@/components/admin/confirm-delete-dialog"
import { Button } from "@/components/ui/button"
import { ApiError } from "@/lib/api"
import { cn } from "@/lib/utils"
import { usePermission } from "@/hooks/use-auth"
import {
  type LoopBreakRing,
  useLoopBreakOptions,
  useOperateDevice,
  useRevokeConnection,
  useRingConnections,
} from "@/hooks/use-loop-fix"
import { useToastStore } from "@/stores/toast-store"

/**
 * Fix a detected loop -- rendered as the collapsible right-hand column of the trace
 * result panel (trace-result-table.tsx) when the result is a loop check, so the
 * loop and its fix share ONE window. Two human-driven paths (nothing auto-edits
 * topology):
 *  - OPEN a switchable device on the ring (operational fix) -- the ring stays
 *    as-built but is operated radially; logged as a device operation. Needs oms:write.
 *  - REVOKE a wrong connection (data-error fix) -- review the ring's edges and
 *    remove the mistaken one. Needs connectivity:commit.
 * `start` (the loop check's start feature) seeds POST /trace/loops/break-options.
 */
export function LoopFixContent({ start }: { start: { feature_class: string; feature_id: string } }) {
  const options = useLoopBreakOptions(start)
  const canOperate = usePermission()("oms:write")
  const canRevoke = usePermission()("connectivity:commit")
  const rings = options.data?.rings ?? []

  return (
    <div className="p-3 text-sm">
      {options.isPending ? (
        <div className="text-muted-foreground flex items-center gap-2">
          <Loader2 className="size-4 animate-spin" /> Finding break points…
        </div>
      ) : options.isError ? (
        <p className="text-destructive">Couldn't load break options.</p>
      ) : rings.length === 0 ? (
        <p className="text-muted-foreground">No closed loop here to fix (open points already break it).</p>
      ) : (
        rings.map((ring, i) => (
          <RingFix key={i} ring={ring} index={i} total={rings.length} canOperate={canOperate} canRevoke={canRevoke} />
        ))
      )}
    </div>
  )
}

function RingFix({
  ring,
  index,
  total,
  canOperate,
  canRevoke,
}: {
  ring: LoopBreakRing
  index: number
  total: number
  canOperate: boolean
  canRevoke: boolean
}) {
  const operate = useOperateDevice()
  const [reviewing, setReviewing] = useState(false)
  // Go marshals an empty slice as null, so coalesce both lists before use.
  const candidates = ring.candidates ?? []

  async function open(featureClass: string, featureId: string) {
    try {
      await operate.mutateAsync({ featureClass, featureId, state: "OPEN" })
      useToastStore.getState().pushSuccess(`Opened ${featureClass} — the loop is broken (operated radially).`)
    } catch (err) {
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not operate this device.")
    }
  }

  return (
    <div className={cn("py-2", index > 0 && "mt-2 border-t pt-3")}>
      {total > 1 && <div className="text-muted-foreground mb-1 text-xs font-medium">Loop {index + 1}</div>}

      <div className="text-muted-foreground mb-1 text-xs">Open a point to break it:</div>
      {candidates.length === 0 ? (
        <p className="text-muted-foreground text-xs">
          No switchable device on this ring — it can only be fixed by removing a connection.
        </p>
      ) : (
        <div className="flex flex-col gap-1">
          {candidates.map((c) => (
            <div key={`${c.feature_class}:${c.feature_id}`} className="flex items-center gap-2">
              <Zap className={cn("size-3.5", c.device_state === "OPEN" ? "text-muted-foreground" : "text-amber-500")} />
              <span className="flex-1 truncate font-mono text-xs" title={`${c.feature_class} ${c.feature_id}`}>
                {c.feature_class}
                {c.recommended && <span className="text-primary ml-1 text-[10px]">· recommended</span>}
              </span>
              <Button
                size="sm"
                variant={c.recommended ? "default" : "outline"}
                disabled={!canOperate || c.device_state === "OPEN" || operate.isPending}
                onClick={() => open(c.feature_class, c.feature_id)}
                title={!canOperate ? "Requires oms:write" : c.device_state === "OPEN" ? "Already open" : "Open this device"}
              >
                {c.device_state === "OPEN" ? "Open" : "Open"}
              </Button>
            </div>
          ))}
        </div>
      )}

      <button
        type="button"
        onClick={() => setReviewing((v) => !v)}
        className="text-muted-foreground hover:text-foreground mt-2 flex items-center gap-1 text-xs"
      >
        <Unplug className="size-3.5" />
        {reviewing ? "Hide connections" : "Review connections (data error)"}
      </button>
      {reviewing && <RingConnections ring={ring} canRevoke={canRevoke} />}
    </div>
  )
}

function RingConnections({
  ring,
  canRevoke,
}: {
  ring: LoopBreakRing
  canRevoke: boolean
}) {
  const conns = useRingConnections(ring.features)
  const revoke = useRevokeConnection()
  const [confirmId, setConfirmId] = useState<string | null>(null)

  if (conns.isPending) {
    return (
      <div className="text-muted-foreground mt-1 flex items-center gap-2 text-xs">
        <Loader2 className="size-3.5 animate-spin" /> Loading edges…
      </div>
    )
  }
  const edges = conns.data ?? []
  if (edges.length === 0) return <p className="text-muted-foreground mt-1 text-xs">No edges found on this ring.</p>

  return (
    <div className="mt-1 flex flex-col gap-1">
      <p className="text-muted-foreground text-[11px]">Remove the WRONG edge only — this is a permanent topology edit.</p>
      {edges.map((e) => (
        <div key={e.id} className="flex items-center gap-2">
          <span className="flex-1 truncate font-mono text-[11px]" title={`${e.from.feature_class} → ${e.to.feature_class}`}>
            {e.from.feature_class} → {e.to.feature_class}
          </span>
          <Button
            size="sm"
            variant="ghost"
            className="text-destructive h-6 px-2 text-xs"
            disabled={!canRevoke || revoke.isPending}
            onClick={() => setConfirmId(e.id)}
            title={!canRevoke ? "Requires connectivity:commit" : "Revoke this connection"}
          >
            Revoke
          </Button>
        </div>
      ))}
      <ConfirmDeleteDialog
        open={confirmId !== null}
        title="Remove this connection?"
        description="This permanently edits the as-built topology — remove the WRONG edge only. (To operate the network radially instead, open a device above.)"
        confirmLabel="Remove"
        onOpenChange={(next) => {
          if (!next) setConfirmId(null)
        }}
        onConfirm={async () => {
          if (!confirmId) return
          await revoke.mutateAsync(confirmId)
          useToastStore.getState().pushSuccess("Connection removed.")
        }}
      />
    </div>
  )
}
