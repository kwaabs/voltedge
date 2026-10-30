import { Activity, ListChecks, Radio } from "lucide-react"
import { Link, useLocation } from "react-router"

import { usePermission } from "@/hooks/use-auth"
import { cn } from "@/lib/utils"

// The Operations log (what devices DID — the trip/switch history), Live state
// (what the field reports NOW vs the model) and Switching (the planned open/close
// programs an operator authors and executes) are all views of the same device-
// operations surface, so they share one page as tabs. This strip is rendered at
// the top of each route page; the active tab tracks the URL, and a tab the caller
// lacks permission for is hidden. When only one tab is visible there's nothing to
// switch between, so the strip omits itself entirely.
const OPS_TABS = [
  { to: "/operations", label: "Operations", icon: Activity, perm: "operations:read" },
  { to: "/live-state", label: "Live state", icon: Radio, perm: "telemetry:read" },
  { to: "/switching", label: "Switching", icon: ListChecks, perm: "oms:read" },
]

export function OperationsTabs() {
  const can = usePermission()
  const { pathname } = useLocation()
  const tabs = OPS_TABS.filter((t) => can(t.perm))
  if (tabs.length < 2) return null

  return (
    <div className="bg-background sticky top-0 z-10 flex shrink-0 gap-1 border-b">
      {tabs.map((t) => {
        const active = pathname === t.to || pathname.startsWith(`${t.to}/`)
        const Icon = t.icon
        return (
          <Link
            key={t.to}
            to={t.to}
            className={cn(
              "-mb-px flex items-center gap-1.5 border-b-2 px-3 py-2 text-sm font-medium",
              active
                ? "border-primary text-foreground"
                : "text-muted-foreground hover:text-foreground border-transparent",
            )}
          >
            <Icon className="size-4" />
            {t.label}
          </Link>
        )
      })}
    </div>
  )
}
