import { ConnectConfirmPanel } from "@/components/map/connect-confirm-panel"
import { FeaturePanel } from "@/components/map/feature-panel"
import { GeometryEditToolbar } from "@/components/map/geometry-edit-toolbar"
import { LayersPanel } from "@/components/map/layers-panel"
import { NewFeatureMenu } from "@/components/map/new-feature-menu"
import { PeerPicker } from "@/components/map/peer-picker"
import { StatusBar } from "@/components/map/status-bar"
import { VoltEdgeMap } from "@/components/map/voltedge-map"
import { Toaster } from "@/components/ui/toaster"

export function MapPage() {
  return (
    <div className="flex h-full w-full flex-col">
      <div className="relative min-h-0 flex-1">
        <div className="pointer-events-auto absolute top-3 right-3 z-10 flex items-center gap-2">
          <NewFeatureMenu />
          <LayersPanel />
        </div>
        <VoltEdgeMap />
      </div>
      <PeerPicker />
      <FeaturePanel />
      <ConnectConfirmPanel />
      <GeometryEditToolbar />
      <Toaster />
      <StatusBar />
    </div>
  )
}
