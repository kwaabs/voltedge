import { ShieldAlert } from "lucide-react"

import { cn } from "@/lib/utils"
import type { ServiceRollup } from "@/stores/trace-store"

/** Per-tier accent, brightest for the most critical restoration rank so a
 * glance at the bars reads P1->P4 as hot->cool. Keyed by the priority code the
 * backend emits (api/internal/trace/service.go's orderedPriorities). An
 * unrecognised code falls back to neutral. */
const TIER_BAR: Record<string, string> = {
  P1: "bg-red-500",
  P2: "bg-orange-500",
  P3: "bg-amber-400",
  P4: "bg-slate-400 dark:bg-slate-500",
}

/** Rounds kW/kVA to whole units with thousands separators -- distribution loads
 * are read at the kW level, so decimals are noise. */
function formatPower(v: number): string {
  return Math.round(v).toLocaleString()
}

/** Compact consumption summary shared by every trace/isolation result panel
 * (Phase 62). Reads the ServiceRollup that rides along on the response: a
 * headline (total customers + load, with the critical subset called out when
 * non-zero) over per-tier P1..P4 bars scaled to the busiest tier's customer
 * count. Renders nothing when the trace reached no load at all (every tier
 * zero) so a trace that touched no service points shows no empty widget.
 *
 * `variant="deenergized"` only reworks the copy for an isolation result (load
 * *dropped* rather than *served*); the figures are identical. */
export function ServiceRollupSummary({
  service,
  variant = "served",
}: {
  service: ServiceRollup | undefined
  variant?: "served" | "deenergized"
}) {
  if (!service || service.service_points === 0) return null

  const tiers = service.by_priority ?? []
  // Scale bars to the busiest tier so the tallest fills the track -- an absolute
  // customer scale would flatten every bar on a network with one dominant tier.
  const maxCustomers = Math.max(1, ...tiers.map((t) => t.customers))
  const verb = variant === "deenergized" ? "would lose power" : "served"

  return (
    <div className="flex flex-col gap-2 border-b px-3 py-2">
      <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1 text-xs">
        <span className="text-sm font-medium tabular-nums">
          {service.customers.toLocaleString()} customer{service.customers === 1 ? "" : "s"}
        </span>
        <span className="text-muted-foreground tabular-nums">
          {formatPower(service.demand_kw)} kW · {formatPower(service.demand_kva)} kVA {verb}
        </span>
        {service.critical.customers > 0 && (
          <span className="text-destructive flex items-center gap-1 tabular-nums" title="Restoration rank P1 or P2">
            <ShieldAlert className="size-3.5 shrink-0" />
            {service.critical.customers.toLocaleString()} critical
          </span>
        )}
      </div>

      <div className="flex flex-col gap-1">
        {tiers.map((tier) => {
          const pct = Math.round((tier.customers / maxCustomers) * 100)
          return (
            <div key={tier.priority} className="flex items-center gap-2 text-xs" title={tier.label}>
              <span className="text-muted-foreground w-6 shrink-0 font-medium">{tier.priority}</span>
              <div className="bg-muted relative h-3 flex-1 overflow-hidden rounded-sm">
                <div
                  className={cn("absolute inset-y-0 left-0 rounded-sm", TIER_BAR[tier.priority] ?? "bg-slate-400")}
                  style={{ width: `${tier.customers === 0 ? 0 : Math.max(3, pct)}%` }}
                />
              </div>
              <span className="text-muted-foreground w-28 shrink-0 text-right tabular-nums">
                {tier.customers.toLocaleString()} cust · {formatPower(tier.demand_kw)} kW
              </span>
            </div>
          )
        })}
      </div>
    </div>
  )
}
