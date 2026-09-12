import { ArrowDown, ArrowUp, ChevronRight, Download, Search, SlidersHorizontal, Sparkles, Upload, X } from "lucide-react"
import { useState } from "react"

import { AmpacityPanel } from "@/components/map/ampacity-panel"
import { AnnotationEditPanel } from "@/components/map/annotation-edit-panel"
import { AssistantChat } from "@/components/map/assistant-chat"
import { AssistantSuggestions } from "@/components/map/assistant-suggestions"
import { BoxZoomButton } from "@/components/map/box-zoom-button"
import { ConnectConfirmPanel } from "@/components/map/connect-confirm-panel"
import { CoordinationPanel } from "@/components/map/coordination-panel"
import { DiagramCanvas } from "@/components/map/diagram-canvas"
import { ExportDialog } from "@/components/map/export-dialog"
import { ExportJobsPanel } from "@/components/map/export-jobs-panel"
import { FaultCurrentPanel } from "@/components/map/fault-current-panel"
import { FeaturePanel } from "@/components/map/feature-panel"
import { FeatureQueryPanel } from "@/components/map/feature-query-panel"
import { ImportDialog } from "@/components/map/import-dialog"
import { ImportedFeaturePopup } from "@/components/map/imported-feature-popup"
import { GeometryEditToolbar } from "@/components/map/geometry-edit-toolbar"
import { IsolationResultTable } from "@/components/map/isolation-result-table"
import { OutageHighlightBanner } from "@/components/map/outage-highlight-banner"
import { LayersPanel } from "@/components/map/layers-panel"
import { LoadFlowPanel } from "@/components/map/load-flow-panel"
import { VoltageProfilePanel } from "@/components/map/voltage-profile-panel"
import { ContingencyPanel } from "@/components/map/contingency-panel"
import { HostingPanel } from "@/components/map/hosting-panel"
import { MeasureToolbar } from "@/components/map/measure-toolbar"
import { NewAnnotationMenu } from "@/components/map/new-annotation-menu"
import { NewFeatureMenu } from "@/components/map/new-feature-menu"
import { PeerPicker } from "@/components/map/peer-picker"
import { StatusBar } from "@/components/map/status-bar"
import { TerritoryEditToolbar } from "@/components/map/territory-edit-toolbar"
import { TerritoryPanel } from "@/components/map/territory-panel"
import { TraceResultTable } from "@/components/map/trace-result-table"
import { VoltEdgeMap } from "@/components/map/voltedge-map"
import { Button } from "@/components/ui/button"
import { useAssistantStatus } from "@/hooks/use-assistant"
import { useDiagramInstance } from "@/hooks/use-diagrams"
import { useMapSettings } from "@/hooks/use-map-settings"
import { cn } from "@/lib/utils"
import { useAssistantChatStore } from "@/stores/assistant-chat-store"
import { type DiagramPane, useDiagramStore } from "@/stores/diagram-store"
import { useExportStore } from "@/stores/export-store"
import { useImportStore } from "@/stores/import-store"
import { useQueryStore } from "@/stores/query-store"

export function MapPage() {
  const topTabs = useDiagramStore((s) => s.panes.top)
  const bottomTabs = useDiagramStore((s) => s.panes.bottom)
  const diagramOpen = topTabs.length > 0 || bottomTabs.length > 0
  const split = topTabs.length > 0 && bottomTabs.length > 0
  // Full-page diagram: one diagram fills the width (map hidden), the rest
  // collapse into a carousel tab strip. Guard against a stale id whose diagram
  // has since closed.
  const maximizedId = useDiagramStore((s) => s.maximizedId)
  const maximized = maximizedId != null && (topTabs.includes(maximizedId) || bottomTabs.includes(maximizedId))
  const queryOpen = useQueryStore((s) => s.open)
  const chatOpen = useAssistantChatStore((s) => s.open)
  // The chat toolbar entry only appears when the server has a model configured;
  // the deterministic suggestions work regardless, so a model-less deployment
  // simply never shows a chat box (see use-assistant.ts).
  const chatEnabled = useAssistantStatus().data?.chat_enabled ?? false
  // The admin-configured default view. The map builds its View once at mount, so
  // hold the map back until this settles (success or error) rather than mounting
  // on the placeholder and re-centering -- that would flash and could race an
  // early user pan. On error we still mount, falling back to the client default.
  const mapSettings = useMapSettings()
  // The top-right control cluster grows with every feature (Query, Import,
  // Export, Assistant, Measure, New feature, New annotation, Layers ...) and
  // was crowding the map. Collapse tucks it behind a single button so the
  // canvas is unobstructed; local state -- it's a per-view toggle, not worth
  // persisting.
  const [controlsCollapsed, setControlsCollapsed] = useState(true)

  return (
    <div className="flex h-full w-full flex-col">
      <div className="flex min-h-0 flex-1">
        <div className={cn("relative min-h-0", maximized ? "hidden" : "flex-1")}>
          <div className="pointer-events-auto absolute top-3 right-3 z-10 flex items-center gap-2">
            {controlsCollapsed ? (
              <Button
                variant="secondary"
                size="sm"
                className="shadow-sm"
                title="Show controls"
                onClick={() => setControlsCollapsed(false)}
              >
                <SlidersHorizontal className="text-primary size-3.5" /> Controls
              </Button>
            ) : (
              <>
                <Button
                  variant={queryOpen ? "default" : "secondary"}
                  size="sm"
                  className="shadow-sm"
                  onClick={() => useQueryStore.getState().toggle()}
                >
                  <Search className={cn("size-3.5", !queryOpen && "text-sky-600")} /> Query
                </Button>
                <Button
                  variant="secondary"
                  size="sm"
                  className="shadow-sm"
                  onClick={() => useImportStore.getState().openDialog()}
                >
                  <Upload className="size-3.5 text-emerald-600" /> Import
                </Button>
                <Button
                  variant="secondary"
                  size="sm"
                  className="shadow-sm"
                  onClick={() => useExportStore.getState().openDialog()}
                >
                  <Download className="size-3.5 text-amber-600" /> Export
                </Button>
                {chatEnabled && (
                  <Button
                    variant={chatOpen ? "default" : "secondary"}
                    size="sm"
                    className="shadow-sm"
                    onClick={() => useAssistantChatStore.getState().toggle()}
                  >
                    <Sparkles className={cn("size-3.5", !chatOpen && "text-violet-600")} /> Assistant
                  </Button>
                )}
                <BoxZoomButton />
                <MeasureToolbar />
                <NewFeatureMenu />
                <NewAnnotationMenu />
                <LayersPanel />
                <Button
                  variant="ghost"
                  size="icon"
                  className="bg-background/90 size-8 shadow-sm"
                  title="Hide controls"
                  onClick={() => setControlsCollapsed(true)}
                >
                  <ChevronRight className="size-4" />
                  <span className="sr-only">Hide controls</span>
                </Button>
              </>
            )}
          </div>
          {!mapSettings.isPending && (
            <VoltEdgeMap
              initialCenter={
                mapSettings.data ? [mapSettings.data.center_lng, mapSettings.data.center_lat] : undefined
              }
              initialZoom={mapSettings.data?.zoom}
            />
          )}
        </div>
        {diagramOpen &&
          (maximized ? (
            <MaximizedDiagram />
          ) : (
            <div className="flex h-full w-2/5 min-w-80 shrink-0 flex-col divide-y">
              {topTabs.length > 0 && <DiagramColumn pane="top" heightClass={split ? "h-1/2" : "h-full"} />}
              {bottomTabs.length > 0 && <DiagramColumn pane="bottom" heightClass={split ? "h-1/2" : "h-full"} />}
            </div>
          ))}
      </div>
      {queryOpen && <FeatureQueryPanel />}
      <ExportDialog />
      <ExportJobsPanel />
      <ImportDialog />
      <ImportedFeaturePopup />
      <PeerPicker />
      <FeaturePanel />
      <TerritoryPanel />
      <ConnectConfirmPanel />
      <AssistantSuggestions />
      <AssistantChat />
      <GeometryEditToolbar />
      <TerritoryEditToolbar />
      <AnnotationEditPanel />
      <TraceResultTable />
      <IsolationResultTable />
      <OutageHighlightBanner />
      <LoadFlowPanel />
      <FaultCurrentPanel />
      <AmpacityPanel />
      <CoordinationPanel />
      <VoltageProfilePanel />
      <ContingencyPanel />
      <HostingPanel />
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

/**
 * Full-page ("maximized") diagram view -- one diagram fills the whole diagram
 * area while the map is hidden. Every other open diagram (across both panes)
 * becomes a tab in a single strip, so maximizing turns the split into a
 * carousel rather than stacking diagrams. Switching a tab just re-points
 * `maximizedId`; the pane layout underneath is untouched, so restoring
 * (the header's Exit-full-page button) returns to exactly the prior split.
 */
function MaximizedDiagram() {
  const topTabs = useDiagramStore((s) => s.panes.top)
  const bottomTabs = useDiagramStore((s) => s.panes.bottom)
  const maximizedId = useDiagramStore((s) => s.maximizedId)
  const ids = [...topTabs, ...bottomTabs]
  const activeId = maximizedId && ids.includes(maximizedId) ? maximizedId : (ids[0] ?? null)
  if (!activeId) return null

  return (
    <div className="flex h-full w-full min-w-0 flex-col">
      {ids.length > 1 && (
        <div className="bg-muted/30 flex shrink-0 items-center gap-0.5 overflow-x-auto border-b px-1">
          {ids.map((id) => (
            <MaximizedTab key={id} diagramId={id} active={id === activeId} />
          ))}
        </div>
      )}
      <div className="min-h-0 flex-1">
        <DiagramCanvas diagramId={activeId} onClose={() => useDiagramStore.getState().close(activeId)} />
      </div>
    </div>
  )
}

function MaximizedTab({ diagramId, active }: { diagramId: string; active: boolean }) {
  const { data: instance } = useDiagramInstance(diagramId)

  return (
    <div
      className={cn(
        "group flex shrink-0 items-center gap-1 border-b-2 px-2 py-1 text-xs",
        active ? "border-primary text-foreground" : "border-transparent text-muted-foreground hover:text-foreground",
      )}
    >
      <button onClick={() => useDiagramStore.getState().maximize(diagramId)} className="max-w-40 truncate">
        {instance?.name || instance?.diagram_type || "Diagram"}
      </button>
      <button
        onClick={() => useDiagramStore.getState().close(diagramId)}
        title="Close diagram"
        className="opacity-0 group-hover:opacity-100"
      >
        <X className="size-3" />
        <span className="sr-only">Close diagram</span>
      </button>
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
