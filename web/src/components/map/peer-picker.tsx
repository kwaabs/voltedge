import { useEffect, useRef } from "react"

import { useFeatureClasses } from "@/hooks/use-feature-classes"
import { useSelectionStore } from "@/stores/selection-store"

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
 * Picking an entry selects it, populating FeaturePanel the same as a
 * direct single-feature click would. Positioned with `fixed` at the
 * original click's viewport coordinates (not the map's own pixel space,
 * which is relative to the map container), clamped so it never renders
 * off-screen.
 */
export function PeerPicker() {
  const candidates = useSelectionStore((s) => s.candidates)
  const position = useSelectionStore((s) => s.candidatesPosition)
  const select = useSelectionStore((s) => s.select)
  const clear = useSelectionStore((s) => s.clear)
  const { data: featureClasses } = useFeatureClasses()
  const containerRef = useRef<HTMLDivElement>(null)

  // Close on a click outside the popup or an Escape press -- the map's
  // own click handler already replaces `candidates` on its own when the
  // next click lands back on the map, but a click anywhere else in the
  // UI (the Layers button, the header, ...) never reaches that handler.
  useEffect(() => {
    if (!candidates) return

    const handlePointerDown = (evt: PointerEvent) => {
      if (!containerRef.current?.contains(evt.target as Node)) clear()
    }
    const handleKeyDown = (evt: KeyboardEvent) => {
      if (evt.key === "Escape") clear()
    }

    document.addEventListener("pointerdown", handlePointerDown)
    document.addEventListener("keydown", handleKeyDown)
    return () => {
      document.removeEventListener("pointerdown", handlePointerDown)
      document.removeEventListener("keydown", handleKeyDown)
    }
  }, [candidates, clear])

  if (!candidates || !position) return null

  const left = Math.min(Math.max(position.x, MARGIN), window.innerWidth - 220 - MARGIN)
  const top = Math.min(Math.max(position.y, MARGIN), window.innerHeight - candidates.length * 44 - 48 - MARGIN)

  return (
    <div
      ref={containerRef}
      style={{ left, top }}
      className="bg-popover text-popover-foreground fixed z-50 w-56 overflow-hidden rounded-md border shadow-lg"
    >
      <p className="text-muted-foreground border-b px-3 py-1.5 text-xs">
        {candidates.length} features here — pick one
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
              onClick={() => select(candidate.featureClassCode, candidate.featureId)}
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
