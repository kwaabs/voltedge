import { X } from "lucide-react"

import { Button } from "@/components/ui/button"
import { useTraceStore } from "@/stores/trace-store"

const DIRECTION_LABEL: Record<string, string> = {
  CONNECTED: "Connected",
  DOWNSTREAM: "Downstream",
  UPSTREAM: "Upstream",
  SHORTEST_PATH: "Shortest path",
}

/**
 * Floating summary of the active trace result -- fixed bottom-center
 * (clear of the floating attribute panel's own default bottom-right
 * corner and geometry-edit-toolbar.tsx's top-center), shown for as long
 * as trace-store.ts's `result` is set. Self-contained, same pattern as
 * every other floating overlay in this app (connect-confirm-panel.tsx,
 * geometry-edit-toolbar.tsx) -- reads the store directly and returns
 * null rather than being conditionally rendered by a parent.
 *
 * The actual highlight (every reached feature in trace's own color,
 * every barrier in a distinct one) is voltedge-map.tsx's job, driven by
 * the same store -- this component is only the summary + Clear
 * affordance, matching how new-feature-menu.tsx's draw banner and this
 * app's other ephemeral-workflow banners work: a small text summary,
 * never the visualization itself.
 */
export function TraceResultBanner() {
  const result = useTraceStore((s) => s.result)
  if (!result) return null

  const directionLabel = DIRECTION_LABEL[result.direction] ?? result.direction
  const barrierCount = result.barriers.length
  const summary =
    result.direction === "SHORTEST_PATH"
      ? result.path_found
        ? `Path found -- ${result.features.length} features`
        : "No path found"
      : `${result.features.length} feature${result.features.length === 1 ? "" : "s"} reached` +
        (barrierCount > 0 ? `, stopped at ${barrierCount} open device${barrierCount === 1 ? "" : "s"}` : "")

  return (
    <div className="bg-popover text-popover-foreground fixed bottom-10 left-1/2 z-40 flex -translate-x-1/2 items-center gap-2 rounded-md border px-3 py-1.5 text-sm shadow-lg">
      <span className="font-medium">{directionLabel} trace</span>
      <span className="text-muted-foreground">{summary}</span>
      <Button
        variant="ghost"
        size="icon"
        className="size-6 shrink-0"
        onClick={() => useTraceStore.getState().clear()}
      >
        <X className="size-3.5" />
        <span className="sr-only">Clear trace</span>
      </Button>
    </div>
  )
}
