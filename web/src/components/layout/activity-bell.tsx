import { useState } from "react"
import { Bell } from "lucide-react"
import { useNavigate } from "react-router"

import { Button } from "@/components/ui/button"
import { DropdownMenu, DropdownMenuContent, DropdownMenuTrigger } from "@/components/ui/dropdown-menu"
import { useMyOpenWorkOrders } from "@/hooks/use-activity"
import { usePermission } from "@/hooks/use-auth"
import { useExpiringEasements, useVegetationTrimDue } from "@/hooks/use-spatial"
import type { WorkOrder } from "@/hooks/use-wam"
import { cn } from "@/lib/utils"

// The header "activity centre": a bell + combined count of things needing the
// signed-in user's attention. The assigned work orders are bucketed into
// OVERDUE (past due_by), DATA-QUALITY FIXES (raised from the audit, source
// 'validation') and the rest (ASSIGNED TO YOU); plus impending network due-items
// (easements expiring, vegetation trims due). Clicking opens the grouped
// dropdown; the badge turns red when a new job has been assigned since the user
// last opened it. Extensible: each bucket/source is a section here, no new
// plumbing. Field crews are notified separately (a "new since last sync" badge in
// the offline field tool).

// The ids the user has already seen in their open-assigned set. A SET (not a
// "last opened" timestamp) so a RE-assigned order — created long ago, assigned to
// you just now — still reads as new: new = any id now in your set that wasn't in
// it last time you looked. Timestamps missed that (created_at never moved).
const SEEN_KEY = "voltedge-activity-seen-ids"

function loadSeenIds(): Set<string> {
  try {
    const raw = localStorage.getItem(SEEN_KEY)
    const arr = raw ? (JSON.parse(raw) as unknown) : []
    return new Set(Array.isArray(arr) ? arr.filter((x): x is string => typeof x === "string") : [])
  } catch {
    return new Set()
  }
}
const DUE_WITHIN_DAYS = 30
const MAX_ROWS = 6

const PRIORITY_DOT: Record<string, string> = {
  low: "bg-gray-400",
  medium: "bg-blue-500",
  high: "bg-orange-500",
  urgent: "bg-red-600",
}

function shortId(id: string): string {
  return id.length > 8 ? `${id.slice(0, 8)}…` : id
}

/** One bucketed section of work orders: a labelled header + up to MAX_ROWS rows
 * (priority dot + title + priority/asset/due), with a "+N more" link to the board.
 * Hidden when empty. Every row opens the Works board (the actionable surface). */
function WorkOrderSection({
  label,
  labelClass,
  items,
  onOpen,
}: {
  label: string
  labelClass?: string
  items: WorkOrder[]
  onOpen: () => void
}) {
  if (items.length === 0) return null
  const shown = items.slice(0, MAX_ROWS)
  return (
    <div>
      <div className={cn("bg-muted/40 px-3 py-1 text-[11px] font-medium", labelClass ?? "text-muted-foreground")}>
        {label} · {items.length}
      </div>
      <ul>
        {shown.map((wo) => (
          <li key={wo.id}>
            <button
              type="button"
              onClick={onOpen}
              className="hover:bg-muted/60 flex w-full items-start gap-2 px-3 py-1.5 text-left"
            >
              <span className={cn("mt-1 size-1.5 shrink-0 rounded-full", PRIORITY_DOT[wo.priority] ?? "bg-blue-500")} />
              <span className="min-w-0 flex-1">
                <span className="block truncate text-sm">{wo.title}</span>
                <span className="text-muted-foreground block truncate text-xs capitalize">
                  {wo.priority}
                  {wo.asset_class && ` · ${wo.asset_class} ${shortId(wo.asset_id ?? "")}`}
                  {wo.due_by && ` · due ${new Date(wo.due_by).toLocaleDateString()}`}
                </span>
              </span>
            </button>
          </li>
        ))}
      </ul>
      {items.length > shown.length && (
        <button
          type="button"
          onClick={onOpen}
          className="text-primary hover:bg-muted/60 w-full px-3 py-1.5 text-left text-xs"
        >
          + {items.length - shown.length} more — open Works board
        </button>
      )}
    </div>
  )
}

export function ActivityBell() {
  const can = usePermission()
  const canReadWam = can("wam:read")
  const canReadFeatures = can("features:read")
  const navigate = useNavigate()
  const [open, setOpen] = useState(false)

  const workOrders = useMyOpenWorkOrders(canReadWam).data?.work_orders ?? []
  const easements = useExpiringEasements({ withinDays: DUE_WITHIN_DAYS, enabled: canReadFeatures }).data?.count ?? 0
  const trims = useVegetationTrimDue({ withinDays: DUE_WITHIN_DAYS, enabled: canReadFeatures }).data?.count ?? 0
  const total = workOrders.length + easements + trims

  // Bucket the assigned work orders (from the one fetch): overdue first, then the
  // audit-raised data-quality fixes, then everything else.
  const now = Date.now()
  const isOverdue = (w: WorkOrder) => !!w.due_by && new Date(w.due_by).getTime() < now
  const overdue = workOrders.filter(isOverdue)
  const current = workOrders.filter((w) => !isOverdue(w))
  const dataQuality = current.filter((w) => w.source === "validation")
  const otherWork = current.filter((w) => w.source !== "validation")

  // "New" = orders now in your open-assigned set whose id you hadn't seen last
  // time you opened the bell (covers both brand-new and re-assigned orders).
  const [seenIds, setSeenIds] = useState<Set<string>>(loadSeenIds)
  const newCount = workOrders.filter((w) => !seenIds.has(w.id)).length

  // Nothing to notify about for this user's permissions -- no bell at all.
  if (!canReadWam && !canReadFeatures) return null

  const go = (to: string) => {
    setOpen(false)
    navigate(to)
  }

  return (
    <DropdownMenu
      open={open}
      onOpenChange={(next) => {
        setOpen(next)
        if (next) {
          // Opening is "I've seen these" -- remember the current ids so the new
          // badge clears and only a later arrival counts as new.
          const ids = workOrders.map((w) => w.id)
          localStorage.setItem(SEEN_KEY, JSON.stringify(ids))
          setSeenIds(new Set(ids))
        }
      }}
    >
      <DropdownMenuTrigger asChild>
        <Button
          variant="ghost"
          size="icon"
          className="relative text-slate-100 hover:bg-white/10 hover:text-white dark:text-foreground dark:hover:bg-accent"
          title="Activity — your assigned work and impending items"
        >
          <Bell className="size-4" />
          {total > 0 && (
            <span
              className={cn(
                "pointer-events-none absolute -top-0.5 -right-0.5 flex min-w-4 items-center justify-center rounded-full px-1 text-[10px] leading-none font-semibold text-white tabular-nums",
                newCount > 0 ? "bg-red-600" : "bg-slate-500 dark:bg-muted-foreground",
              )}
            >
              {total > 9 ? "9+" : total}
            </span>
          )}
          <span className="sr-only">Activity{newCount > 0 ? ` (${newCount} new)` : ""}</span>
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-80 p-0">
        <div className="border-b px-3 py-2 text-sm font-medium">Activity</div>
        <div className="max-h-96 overflow-auto">
          {total === 0 ? (
            <p className="text-muted-foreground p-4 text-center text-sm">You're all caught up.</p>
          ) : (
            <>
              <WorkOrderSection
                label="Overdue"
                labelClass="text-red-700 dark:text-red-400"
                items={overdue}
                onOpen={() => go("/works")}
              />
              <WorkOrderSection label="Data-quality fixes" items={dataQuality} onOpen={() => go("/works")} />
              <WorkOrderSection label="Assigned to you" items={otherWork} onOpen={() => go("/works")} />

              {(easements > 0 || trims > 0) && (
                <div>
                  <div className="text-muted-foreground bg-muted/40 px-3 py-1 text-[11px] font-medium">
                    Land &amp; vegetation
                  </div>
                  {easements > 0 && (
                    <button
                      type="button"
                      onClick={() => go("/")}
                      className="hover:bg-muted/60 block w-full px-3 py-1.5 text-left text-sm"
                    >
                      {easements} easement{easements === 1 ? "" : "s"} expiring soon
                    </button>
                  )}
                  {trims > 0 && (
                    <button
                      type="button"
                      onClick={() => go("/")}
                      className="hover:bg-muted/60 block w-full px-3 py-1.5 text-left text-sm"
                    >
                      {trims} vegetation trim{trims === 1 ? "" : "s"} due
                    </button>
                  )}
                </div>
              )}
            </>
          )}
        </div>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
