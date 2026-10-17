import { Route, X } from "lucide-react"

import { Button } from "@/components/ui/button"
import { useTracePathStore } from "@/stores/trace-path-store"

/**
 * Floating banner shown while a "Trace path to…" gesture (trace-path-store.ts) is
 * waiting on a destination feature. It makes the picking MODE explicit -- otherwise
 * the only cue is a one-off toast -- and gives it a visible Cancel (Escape also
 * cancels, handled in voltedge-map.tsx). Self-contained and reads the store
 * directly, same shape as outage-highlight-banner.tsx; returns null once a target is
 * picked (feature-panel.tsx's effect fires the trace immediately) or when idle.
 */
export function TracePathBanner() {
  const active = useTracePathStore((s) => s.active)
  const target = useTracePathStore((s) => s.target)

  if (!active || target) return null

  return (
    <div className="bg-popover text-popover-foreground fixed top-4 left-1/2 z-40 flex max-w-[calc(100vw-2rem)] -translate-x-1/2 items-center gap-3 rounded-md border px-3 py-2 shadow-lg">
      <Route className="text-primary size-4 shrink-0" />
      <div className="min-w-0 text-sm">
        <p className="font-medium">Trace path: pick a destination</p>
        <p className="text-muted-foreground truncate text-xs">Click the target feature on the map · Esc to cancel</p>
      </div>
      <Button
        variant="ghost"
        size="icon"
        className="size-6 shrink-0"
        onClick={() => useTracePathStore.getState().cancel()}
      >
        <X className="size-3.5" />
        <span className="sr-only">Cancel trace path</span>
      </Button>
    </div>
  )
}
