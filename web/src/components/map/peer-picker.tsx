import { useEffect, useRef } from "react"

import { useFeatureClasses } from "@/hooks/use-feature-classes"
import { useConnectStore } from "@/stores/connect-store"
import { useDiagramPlaceStore } from "@/stores/diagram-place-store"
import { useSelectionStore } from "@/stores/selection-store"
import { useTracePathStore } from "@/stores/trace-path-store"

/** Keeps the popup fully on-screen regardless of where in the viewport the click landed. */
const MARGIN = 8

function shortId(id: string): string {
  return id.length > 8 ? `${id.slice(0, 8)}…` : id
}

/**
 * A small floating list for the case where a click hit several
 * stacked/overlapping features at once -- src/components/map/voltedge-map.tsx
 * only auto-selects when exactly one feature is under the click; more
 * than one lands here instead of guessing which one the user meant.
 * Doubles as connect-mode's own target disambiguation (voltedge-map.tsx's
 * click handler populates the exact same candidates/candidatesPosition
 * state during a "+ New connection"/"+ New relationship" workflow) --
 * picking an entry routes to whichever action is actually live right
 * now: connect-store.ts's `pickTarget` while a workflow is waiting on a
 * target, selection-store's own `select` (populating FeaturePanel the
 * same as a direct single-feature click would) otherwise. Positioned
 * with `fixed` at the original click's viewport coordinates (not the
 * map's own pixel space, which is relative to the map container),
 * clamped so it never renders off-screen.
 */
export function PeerPicker() {
  const candidates = useSelectionStore((s) => s.candidates)
  const position = useSelectionStore((s) => s.candidatesPosition)
  const select = useSelectionStore((s) => s.select)
  const clearCandidates = useSelectionStore((s) => s.clearCandidates)
  const connectMode = useConnectStore((s) => s.mode)
  const connectTarget = useConnectStore((s) => s.target)
  const tracePathActive = useTracePathStore((s) => s.active)
  const tracePathTarget = useTracePathStore((s) => s.target)
  const diagramPlacePhase = useDiagramPlaceStore((s) => s.phase)
  const { data: featureClasses } = useFeatureClasses()
  const containerRef = useRef<HTMLDivElement>(null)

  // True for exactly the window voltedge-map.tsx's click handler uses
  // this same candidates state for connect-mode's own target pick
  // rather than a normal selection -- once target is set the confirm
  // panel takes over and no further picks from this list make sense.
  const pickingConnectTarget = Boolean(connectMode) && !connectTarget
  // Likewise for a "Trace path to…" gesture (trace-path-store.ts) waiting on a
  // destination feature: choosing one hands it to pickTarget, and feature-panel's
  // effect fires the SHORTEST_PATH trace.
  const pickingTracePathTarget = tracePathActive && !tracePathTarget
  // Likewise for a diagram "Add from map" pick (Phase 12): a stacked-
  // feature click during the pick lands here, and choosing one hands it
  // to diagram-place-store's `pick` (the owning canvas validates + arms).
  const pickingForDiagram = diagramPlacePhase === "picking"

  // Close on a click outside the popup or an Escape press -- the map's
  // own click handler already replaces `candidates` on its own when the
  // next click lands back on the map, but a click anywhere else in the
  // UI (the Layers button, the header, ...) never reaches that handler.
  // clearCandidates, not selection-store's `clear` -- dismissing this
  // list should never drop whichever feature is otherwise selected (or,
  // mid connect-workflow, the workflow's own source feature, still
  // shown in FeaturePanel throughout).
  useEffect(() => {
    if (!candidates) return

    const handlePointerDown = (evt: PointerEvent) => {
      if (!containerRef.current?.contains(evt.target as Node)) clearCandidates()
    }
    const handleKeyDown = (evt: KeyboardEvent) => {
      if (evt.key === "Escape") clearCandidates()
    }

    document.addEventListener("pointerdown", handlePointerDown)
    document.addEventListener("keydown", handleKeyDown)
    return () => {
      document.removeEventListener("pointerdown", handlePointerDown)
      document.removeEventListener("keydown", handleKeyDown)
    }
  }, [candidates, clearCandidates])

  if (!candidates || !position) return null

  const left = Math.min(Math.max(position.x, MARGIN), window.innerWidth - 220 - MARGIN)
  const top = Math.min(Math.max(position.y, MARGIN), window.innerHeight - candidates.length * 44 - 48 - MARGIN)

  function pick(candidate: { featureClassCode: string; featureId: string }) {
    if (pickingForDiagram) {
      useDiagramPlaceStore.getState().pick(candidate)
      clearCandidates()
    } else if (pickingConnectTarget) {
      useConnectStore.getState().pickTarget(candidate)
      clearCandidates()
    } else if (pickingTracePathTarget) {
      useTracePathStore.getState().pickTarget(candidate)
      clearCandidates()
    } else {
      select(candidate.featureClassCode, candidate.featureId)
    }
  }

  return (
    <div
      ref={containerRef}
      style={{ left, top }}
      className="bg-popover text-popover-foreground fixed z-50 w-56 overflow-hidden rounded-md border shadow-lg"
    >
      <p className="text-muted-foreground border-b px-3 py-1.5 text-xs">
        {candidates.length} features here — pick{" "}
        {pickingForDiagram
          ? "one to place"
          : pickingConnectTarget
            ? "a target"
            : pickingTracePathTarget
              ? "the destination"
              : "one"}
      </p>
      <div className="max-h-64 overflow-y-auto p-1">
        {candidates.map((candidate) => {
          const className =
            featureClasses?.feature_classes.find((fc) => fc.code === candidate.featureClassCode)?.name ??
            candidate.featureClassCode
          return (
            <button
              key={`${candidate.featureClassCode}:${candidate.featureId}`}
              type="button"
              onClick={() => pick(candidate)}
              className="hover:bg-accent hover:text-accent-foreground flex w-full flex-col items-start gap-0.5 rounded-sm px-2 py-1.5 text-left text-sm"
            >
              <span className="font-medium">{className}</span>
              <span className="text-muted-foreground font-mono text-xs">{shortId(candidate.featureId)}</span>
            </button>
          )
        })}
      </div>
    </div>
  )
}
