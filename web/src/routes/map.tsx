import { ConnectConfirmPanel } from "@/components/map/connect-confirm-panel"
import { DiagramCanvas } from "@/components/map/diagram-canvas"
import { FeaturePanel } from "@/components/map/feature-panel"
import { FeatureSearch } from "@/components/map/feature-search"
import { GeometryEditToolbar } from "@/components/map/geometry-edit-toolbar"
import { IsolationResultTable } from "@/components/map/isolation-result-table"
import { LayersPanel } from "@/components/map/layers-panel"
import { LoadFlowPanel } from "@/components/map/load-flow-panel"
import { MeasureToolbar } from "@/components/map/measure-toolbar"
import { NewFeatureMenu } from "@/components/map/new-feature-menu"
import { PeerPicker } from "@/components/map/peer-picker"
import { StatusBar } from "@/components/map/status-bar"
import { TraceResultTable } from "@/components/map/trace-result-table"
import { VoltEdgeMap } from "@/components/map/voltedge-map"
import { useDiagramStore } from "@/stores/diagram-store"

export function MapPage() {
  const diagramOpen = Boolean(useDiagramStore((s) => s.openDiagramId))

  return (
    <div className="flex h-full w-full flex-col">
      <div className="flex min-h-0 flex-1">
        <div className="relative min-h-0 flex-1">
          <div className="pointer-events-auto absolute top-3 right-3 z-10 flex items-center gap-2">
            <FeatureSearch />
            <MeasureToolbar />
            <NewFeatureMenu />
            <LayersPanel />
          </div>
          <VoltEdgeMap />
        </div>
        {diagramOpen && (
          <div className="h-full w-2/5 min-w-80 shrink-0">
            <DiagramCanvas />
          </div>
        )}
      </div>
      <PeerPicker />
      <FeaturePanel />
      <ConnectConfirmPanel />
      <GeometryEditToolbar />
      <TraceResultTable />
      <IsolationResultTable />
      <LoadFlowPanel />
      <StatusBar />
    </div>
  )
}
