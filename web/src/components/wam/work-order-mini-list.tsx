import { Loader2 } from "lucide-react"
import { useNavigate } from "react-router"

import { useWorkOrders } from "@/hooks/use-wam"

/** A compact list of work orders for a crew/subcontractor detail dialog: title,
 * status, priority, and a link to the outage when the order serves one. Pass a
 * scope (crewId or subcontractorId) to narrow it. */
export function WorkOrderMiniList({
  scope,
  empty,
}: {
  scope: { crewId?: string; subcontractorId?: string; incidentId?: string }
  empty: string
}) {
  const navigate = useNavigate()
  const orders = useWorkOrders(scope)
  const rows = orders.data?.work_orders ?? []
  if (orders.isPending) {
    return (
      <div className="text-muted-foreground flex items-center gap-2 text-sm">
        <Loader2 className="size-4 animate-spin" />
        Loading…
      </div>
    )
  }
  if (rows.length === 0) return <p className="text-muted-foreground text-sm">{empty}</p>
  return (
    <div className="flex flex-col divide-y rounded-md border">
      {rows.map((o) => (
        <div key={o.id} className="flex flex-wrap items-center gap-x-2 px-2 py-1.5 text-sm">
          <span className="font-medium">{o.title}</span>
          <span className="text-muted-foreground text-xs capitalize">{o.status.replace(/_/g, " ")}</span>
          {o.priority && <span className="text-muted-foreground text-xs">· {o.priority}</span>}
          {o.incident_id && (
            <button
              type="button"
              className="text-primary ml-auto text-xs hover:underline"
              onClick={() => navigate(`/outages/${o.incident_id}`)}
            >
              View outage
            </button>
          )}
        </div>
      ))}
    </div>
  )
}
