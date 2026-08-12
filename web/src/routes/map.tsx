import { FeaturePanel } from "@/components/map/feature-panel"
import { LayersPanel } from "@/components/map/layers-panel"
import { PeerPicker } from "@/components/map/peer-picker"
import { StatusBar } from "@/components/map/status-bar"
import { VoltEdgeMap } from "@/components/map/voltedge-map"

export function MapPage() {
  return (
    <div className="flex h-full w-full flex-col">
      <div className="relative min-h-0 flex-1">
        <div className="pointer-events-auto absolute top-3 right-3 z-10">
          <LayersPanel />
        </div>
        <VoltEdgeMap />
      </div>
      <PeerPicker />
      <FeaturePanel />
      <StatusBar />
    </div>
  )
}
