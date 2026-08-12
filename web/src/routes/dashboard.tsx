import { Database, Link2, Loader2, Waypoints, Zap } from "lucide-react"

import { Badge } from "@/components/ui/badge"
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import { useHealth } from "@/hooks/use-health"

function HealthBadge() {
  const { data, isPending, isError } = useHealth()

  if (isPending) {
    return (
      <Badge variant="secondary">
        <Loader2 className="animate-spin" />
        Checking API...
      </Badge>
    )
  }
  if (isError || data?.status !== "ok") {
    return <Badge variant="destructive">API unreachable</Badge>
  }
  return <Badge variant="success">API healthy</Badge>
}

const summaryCards = [
  {
    icon: Database,
    title: "Feature classes",
    description: "CRUD, publish/unpublish, delete lifecycle -- Phase 2",
  },
  {
    icon: Waypoints,
    title: "Connectivity",
    description: "Zero-trust, gated on network_rules -- Phase 2",
  },
  {
    icon: Link2,
    title: "Relationships",
    description: "Explicit-confirm, gated on relationship_classes -- Phase 2",
  },
]

export function DashboardPage() {
  return (
    <div className="flex flex-col gap-4">
      <Card>
        <CardHeader>
          <div className="flex items-center justify-between">
            <div className="flex items-center gap-2">
              <div className="bg-primary text-primary-foreground flex size-8 items-center justify-center rounded-md">
                <Zap className="size-4" />
              </div>
              <div>
                <CardTitle>VoltEdge</CardTitle>
                <CardDescription>Metadata-driven utility GIS</CardDescription>
              </div>
            </div>
            <HealthBadge />
          </div>
        </CardHeader>
        <CardContent>
          <p className="text-muted-foreground text-sm">
            Phase 3 (the map loop, this UI) is under way. This dashboard confirms the
            shell -- layout, theming, routing, and a live query against the Go API --
            works end to end before the map itself lands.
          </p>
        </CardContent>
      </Card>

      <div className="grid gap-4 sm:grid-cols-3">
        {summaryCards.map((card) => (
          <Card key={card.title}>
            <CardHeader>
              <div className="text-muted-foreground mb-2 flex size-9 items-center justify-center rounded-lg border">
                <card.icon className="size-4" />
              </div>
              <CardTitle className="text-base">{card.title}</CardTitle>
              <CardDescription>{card.description}</CardDescription>
            </CardHeader>
          </Card>
        ))}
      </div>
    </div>
  )
}
