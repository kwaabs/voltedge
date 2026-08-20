import { ArrowDown, ArrowUp } from "lucide-react"

import { AmpacityPanel } from "@/components/map/ampacity-panel"
import { ConnectConfirmPanel } from "@/components/map/connect-confirm-panel"
import { DiagramCanvas } from "@/components/map/diagram-canvas"
import { FaultCurrentPanel } from "@/components/map/fault-current-panel"
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
import { TerritoryEditToolbar } from "@/components/map/territory-edit-toolbar"
import { TerritoryPanel } from "@/components/map/territory-panel"
import { TraceResultTable } from "@/components/map/trace-result-table"
import { VoltEdgeMap } from "@/components/map/voltedge-map"
import { useDiagramInstance } from "@/hooks/use-diagrams"
import { cn } from "@/lib/utils"
import { type DiagramPane, useDiagramStore } from "@/stores/diagram-store"

export function MapPage() {
  const topTabs = useDiagramStore((s) => s.panes.top)
  const bottomTabs = useDiagramStore((s) => s.panes.bottom)
  const diagramOpen = topTabs.length > 0 || bottomTabs.length > 0
  const split = topTabs.length > 0 && bottomTabs.length > 0

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
          <div className="flex h-full w-2/5 min-w-80 shrink-0 flex-col divide-y">
            {topTabs.length > 0 && <DiagramColumn pane="top" heightClass={split ? "h-1/2" : "h-full"} />}
            {bottomTabs.length > 0 && <DiagramColumn pane="bottom" heightClass={split ? "h-1/2" : "h-full"} />}
          </div>
        )}
      </div>
      <PeerPicker />
      <FeaturePanel />
      <TerritoryPanel />
      <ConnectConfirmPanel />
      <GeometryEditToolbar />
      <TerritoryEditToolbar />
      <TraceResultTable />
      <IsolationResultTable />
      <LoadFlowPanel />
      <FaultCurrentPanel />
      <AmpacityPanel />
      <StatusBar />
    </div>
  )
}

/**
 * One diagram pane (top or bottom) -- its own tab strip plus the
 * active tab's own DiagramCanvas. The tab strip only renders when 2+
 * diagrams are open across *either* pane (not just this one): with
 * exactly one diagram open, there's nothing to switch or move between,
 * so the plain single-canvas view (its own header already has a close
 * button) is all that's shown, same as before this slice existed. Once
 * a second or third diagram opens, every occupied pane grows a strip
 * so any tab -- not just the newest one -- can be activated or moved,
 * per this feature's own "can go either way" framing.
 */
function DiagramColumn({ pane, heightClass }: { pane: DiagramPane; heightClass: string }) {
  const tabs = useDiagramStore((s) => s.panes[pane])
  const totalOpen = useDiagramStore((s) => s.panes.top.length + s.panes.bottom.length)
  const activeId = useDiagramStore((s) => s.active[pane])
  const showTabStrip = totalOpen > 1

  return (
    <div className={cn("flex min-h-0 min-w-0 flex-col", heightClass)}>
      {showTabStrip && (
        <div className="bg-muted/30 flex shrink-0 items-center gap-0.5 overflow-x-auto border-b px-1">
          {tabs.map((id) => (
            <DiagramTab key={id} diagramId={id} pane={pane} active={id === activeId} />
          ))}
        </div>
      )}
      <div className="min-h-0 flex-1">
        {activeId && <DiagramCanvas diagramId={activeId} onClose={() => useDiagramStore.getState().close(activeId)} />}
      </div>
    </div>
  )
}

function DiagramTab({ diagramId, pane, active }: { diagramId: string; pane: DiagramPane; active: boolean }) {
  const { data: instance } = useDiagramInstance(diagramId)

  return (
    <div
      className={cn(
        "group flex shrink-0 items-center gap-1 border-b-2 px-2 py-1 text-xs",
        active ? "border-primary text-foreground" : "border-transparent text-muted-foreground hover:text-foreground",
      )}
    >
      <button onClick={() => useDiagramStore.getState().activate(pane, diagramId)} className="max-w-32 truncate">
        {instance?.name || instance?.diagram_type || "Diagram"}
      </button>
      <button
        onClick={() => useDiagramStore.getState().move(diagramId)}
        title={`Move to ${pane === "top" ? "bottom" : "top"}`}
        className="opacity-0 group-hover:opacity-100"
      >
        {pane === "top" ? <ArrowDown className="size-3" /> : <ArrowUp className="size-3" />}
        <span className="sr-only">Move to {pane === "top" ? "bottom" : "top"}</span>
      </button>
    </div>
  )
}
