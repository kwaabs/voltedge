import { useState } from "react"
import { Bell, Loader2, Network, Search, X } from "lucide-react"

import { OperationsOneLine } from "@/components/map/operations-oneline"
import { OperationsDetailDrawer } from "@/components/map/operations-detail-drawer"
import { FeaturePanel } from "@/components/map/feature-panel"
import { OperationsTabs } from "@/components/layout/operations-tabs"
import { AlarmAnnunciator } from "@/components/oms/alarm-annunciator"
import { Button } from "@/components/ui/button"
import { usePermission } from "@/hooks/use-auth"
import { useAlarmCounts } from "@/hooks/use-alarms"
import { useEventStream } from "@/hooks/use-event-stream"
import { useFeatureNameSearch } from "@/hooks/use-feature-name-search"
import { useFloatingPanel } from "@/hooks/use-floating-panel"
import { useIncidents } from "@/hooks/use-oms"
import { useNetworkOneLineLive } from "@/lib/one-line"
import { useSelectionStore } from "@/stores/selection-store"

/** Go-to search: find a feature by name and centre the schematic on it (via a
 * locate-with-focus the canvas consumes), without opening its panel. */
function SchematicSearch() {
  const [query, setQuery] = useState("")
  const { results, loading } = useFeatureNameSearch(query)
  const goTo = (code: string, id: string) => {
    useSelectionStore.getState().locate(code, id, { focus: true })
    setQuery("")
  }
  return (
    <div className="relative w-56">
      <Search className="text-muted-foreground pointer-events-none absolute left-2 top-1/2 size-3.5 -translate-y-1/2" />
      <input
        value={query}
        onChange={(e) => setQuery(e.target.value)}
        placeholder="Go to a device…"
        className="bg-background focus:ring-ring h-8 w-full rounded-md border py-1.5 pl-7 pr-2 text-sm focus:outline-none focus:ring-1"
      />
      {query.trim().length >= 2 && (
        <div className="bg-popover absolute left-0 right-0 top-full z-30 mt-1 max-h-72 overflow-y-auto rounded-md border shadow-md">
          {loading && results.length === 0 ? (
            <div className="text-muted-foreground px-2 py-1.5 text-xs">Searching…</div>
          ) : results.length === 0 ? (
            <div className="text-muted-foreground px-2 py-1.5 text-xs">No matches.</div>
          ) : (
            results.slice(0, 20).map((r) => (
              <button
                key={`${r.featureClassCode}:${r.featureId}`}
                type="button"
                onClick={() => goTo(r.featureClassCode, r.featureId)}
                className="hover:bg-accent flex w-full flex-col items-start px-2 py-1 text-left"
              >
                <span className="truncate text-sm">{r.name}</span>
                <span className="text-muted-foreground text-xs">{r.className}</span>
              </button>
            ))
          )}
        </div>
      )}
    </div>
  )
}

/** A compact open-outage roll-up for the schematic side panel -- the geographic
 * console (outages.tsx) remains the full board; this is the at-a-glance count the
 * operator keeps beside the one-line. */
function OpenIncidents() {
  const { data } = useIncidents({ openOnly: true })
  const incidents = data?.incidents ?? []
  return (
    <div className="space-y-1.5">
      <div className="text-muted-foreground flex items-center justify-between text-xs font-medium uppercase tracking-wide">
        <span>Open outages</span>
        <span className="text-foreground">{incidents.length}</span>
      </div>
      {incidents.length === 0 ? (
        <div className="text-muted-foreground text-xs">No open outages.</div>
      ) : (
        <div className="space-y-1">
          {incidents.slice(0, 8).map((i) => (
            <div key={i.id} className="flex items-center justify-between gap-2 text-xs">
              <span className="truncate">{i.territory_name ?? i.outage_type}</span>
              <span className="text-muted-foreground shrink-0">
                {i.affected_customers != null ? `${i.affected_customers} cust` : i.status}
              </span>
            </div>
          ))}
          {incidents.length > 8 && (
            <div className="text-muted-foreground text-xs">+{incidents.length - 8} more…</div>
          )}
        </div>
      )}
    </div>
  )
}

/** The movable, closeable "Operations" worklist: open outages + the alarm
 * annunciator, floating over the schematic with the same drag/resize chrome the
 * map's analysis panels use. Its alarm list scrolls inside the panel. */
function FloatingOperationsPanel({ canWrite, onClose }: { canWrite: boolean; onClose: () => void }) {
  const fp = useFloatingPanel(300, "max-h-[72vh]", { positionClass: "right-4 top-36", storageKey: "ops-worklist" })
  return (
    <div ref={fp.panelRef} style={fp.shellStyle} className={fp.shellClassName}>
      <div className={fp.headerClassName} onPointerDown={fp.onHeaderPointerDown}>
        {fp.grip}
        <span className="text-sm font-medium">Operations</span>
        <button type="button" onClick={onClose} className="hover:bg-foreground/10 ml-auto rounded p-0.5" title="Close">
          <X className="size-4" />
        </button>
      </div>
      <div className="min-h-0 flex-1 space-y-3 overflow-y-auto p-3">
        <OpenIncidents />
        <AlarmAnnunciator canWrite={canWrite} />
      </div>
      {fp.resizeHandle}
    </div>
  )
}


/**
 * The ADMS operations schematic: a full-screen, LIVE whole-network one-line that fills
 * a single non-scrolling page. The layout is computed from the current network on
 * demand (GET /schematic/network-one-line) and rendered by OperationsOneLine -- always
 * current, decoupled from GIS editing. Operational chrome (search, legend, the alarms
 * worklist) floats over the canvas, movable and closeable, so the one-line owns the
 * screen; a live SSE stream keeps energisation / alarms / incidents current.
 */
export function OperationsSchematicPage() {
  const can = usePermission()
  const canOms = can("oms:read")
  const canOmsWrite = can("oms:write")

  useEventStream()

  const { data: payload, isLoading, isError } = useNetworkOneLineLive()
  const featureCount = payload ? payload.nodes.length + payload.connectors.filter((c) => c.feature_id).length : 0
  const { data: alarmCounts } = useAlarmCounts(canOms)
  const unacked = alarmCounts?.unacknowledged ?? 0

  // The alarms/worklist panel starts CLOSED -- the operator opens it from the "Alarms"
  // button when they want it, so the one-line owns the screen by default.
  const [opsOpen, setOpsOpen] = useState(false)
  return (
    // AppLayout's shell is min-h-svh (growable), so `h-full` never resolves to a
    // definite height and the page sizes to the SVG's intrinsic height -> body
    // scroll. Pin an exact height (viewport minus the 56px app header) so the canvas
    // fills a fixed, non-scrolling screen.
    <div className="flex h-[calc(100svh-3.5rem)] flex-col overflow-hidden">
      <OperationsTabs />
      <div className="flex shrink-0 items-center justify-between gap-3 border-b px-3 py-2">
        <div className="flex min-w-0 items-center gap-2">
          <Network className="text-muted-foreground size-4 shrink-0" />
          <span className="text-sm font-medium">Operations schematic</span>
          <span className="text-muted-foreground truncate text-xs">
            {featureCount > 0 ? `${featureCount} features · live` : "whole-network one-line · live"}
          </span>
        </div>
        <div className="flex shrink-0 items-center gap-2">
          <SchematicSearch />
          {canOms && (
            <Button
              size="sm"
              variant={opsOpen ? "default" : "outline"}
              onClick={() => setOpsOpen((v) => !v)}
              title="Toggle the operations worklist (open outages + alarms)"
            >
              <Bell className="size-4" /> Alarms
              {unacked > 0 && (
                <span className="bg-destructive text-destructive-foreground ml-1 rounded-full px-1.5 text-[10px] font-semibold leading-4">
                  {unacked}
                </span>
              )}
            </Button>
          )}
        </div>
      </div>

      <div className="relative min-h-0 flex-1">
        {isLoading && !payload && (
          <div className="text-muted-foreground flex h-full items-center justify-center gap-2 text-sm">
            <Loader2 className="size-4 animate-spin" /> Loading schematic…
          </div>
        )}
        {isError && !payload && (
          <div className="text-muted-foreground flex h-full items-center justify-center text-sm">
            Could not load the operations schematic.
          </div>
        )}
        {payload && payload.nodes.length === 0 && (
          <div className="text-muted-foreground flex h-full items-center justify-center text-center text-sm">
            No connected network to show yet.
          </div>
        )}
        {payload && payload.nodes.length > 0 && <OperationsOneLine payload={payload} />}

        {/* The operate hint / selected-feature readout + the legend + print live inside
            OperationsOneLine (it owns the symbology + the canvas SVG). */}
        {canOms && opsOpen && <FloatingOperationsPanel canWrite={canOmsWrite} onClose={() => setOpsOpen(false)} />}
      </div>

      {/* Right-click → "See attributes" selects the device; this floating panel shows
          its attributes + terminals + operations + safety. Left-click only highlights. */}
      <FeaturePanel />
      {/* Right-click → Alarms / Safety / Incident opens this per-device slide-over. */}
      <OperationsDetailDrawer />
    </div>
  )
}
