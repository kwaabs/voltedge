import { Loader2 } from "lucide-react"

import { VoltEdgeMap } from "@/components/map/voltedge-map"
import { Badge } from "@/components/ui/badge"
import { useCatalog } from "@/hooks/use-catalog"
import { useHealth } from "@/hooks/use-health"

function StatusOverlay() {
  const health = useHealth()
  const catalog = useCatalog()

  return (
    <div className="pointer-events-none absolute top-3 left-3 z-10 flex flex-col gap-1.5">
      {health.isPending ? (
        <Badge variant="secondary" className="shadow-sm">
          <Loader2 className="animate-spin" />
          API
        </Badge>
      ) : health.isError || health.data?.status !== "ok" ? (
        <Badge variant="destructive" className="shadow-sm">
          API unreachable
        </Badge>
      ) : (
        <Badge variant="success" className="shadow-sm">
          API healthy
        </Badge>
      )}
      {catalog.data && (
        <Badge variant="outline" className="bg-background/90 shadow-sm">
          {Object.keys(catalog.data.tiles).length} layer
          {Object.keys(catalog.data.tiles).length === 1 ? "" : "s"}
        </Badge>
      )}
    </div>
  )
}

export function MapPage() {
  return (
    <div className="relative h-full w-full">
      <StatusOverlay />
      <VoltEdgeMap />
    </div>
  )
}
