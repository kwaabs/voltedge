import { Loader2 } from "lucide-react"

import { LayersPanel } from "@/components/map/layers-panel"
import { StatusBar } from "@/components/map/status-bar"
import { VoltEdgeMap } from "@/components/map/voltedge-map"
import { Badge } from "@/components/ui/badge"
import { useHealth } from "@/hooks/use-health"

function HealthBadge() {
  const { data, isPending, isError } = useHealth()

  if (isPending) {
    return (
      <Badge variant="secondary" className="shadow-sm">
        <Loader2 className="animate-spin" />
        API
      </Badge>
    )
  }
  if (isError || data?.status !== "ok") {
    return (
      <Badge variant="destructive" className="shadow-sm">
        API unreachable
      </Badge>
    )
  }
  return (
    <Badge variant="success" className="shadow-sm">
      API healthy
    </Badge>
  )
}

export function MapPage() {
  return (
    <div className="flex h-full w-full flex-col">
      <div className="relative min-h-0 flex-1">
        <div className="pointer-events-none absolute top-3 left-3 z-10">
          <HealthBadge />
        </div>
        <div className="pointer-events-auto absolute top-3 right-3 z-10">
          <LayersPanel />
        </div>
        <VoltEdgeMap />
      </div>
      <StatusBar />
    </div>
  )
}
