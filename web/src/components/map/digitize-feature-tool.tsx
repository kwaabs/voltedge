import { useEffect, useRef, useState } from "react"
import { PenLine } from "lucide-react"

import { Button } from "@/components/ui/button"
import { type FeatureClassSummary } from "@/hooks/use-feature-classes"

/**
 * The header trigger for digitizing a brand-new, geometry_type = 'none'
 * feature straight onto a diagram -- the missing third path alongside
 * the map's own draw tools (real geometry, real feature classes) and
 * AddFeatureButton (an *existing* feature, any geometry_type, just
 * placed a second time). Deliberately scoped to `classes` the parent
 * has already filtered to geometry_type = 'none' only: a spatial class
 * can never be created from here, since there's no way to give it real
 * map geometry from inside the diagram canvas -- that's the one hard
 * rule this whole tool exists to respect (docs/phase-5-diagrams.md).
 *
 * Only arms digitizing mode -- diagram-canvas.tsx's own canvas click
 * handler captures the point and hands off to draw-store.ts's
 * `startDiagramDraft`, which feature-panel.tsx/feature-create-form.tsx
 * pick up from there. This component used to also own the follow-up
 * attribute form (a bespoke `DigitizeFeatureForm`), but that duplicated
 * the exact same form feature-create-form.tsx already renders for the
 * map's own draw flow -- reusing it, via draw-store.ts's draft, is what
 * actually shows the attribute form now.
 */
export function DigitizeFeatureButton({
  classes,
  digitizingClass,
  onStart,
  onCancel,
}: {
  classes: FeatureClassSummary[]
  digitizingClass: string | null
  onStart: (code: string) => void
  onCancel: () => void
}) {
  const [open, setOpen] = useState(false)
  const containerRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (!open) return
    function handlePointerDown(e: PointerEvent) {
      if (containerRef.current && !containerRef.current.contains(e.target as Node)) setOpen(false)
    }
    window.addEventListener("pointerdown", handlePointerDown)
    return () => window.removeEventListener("pointerdown", handlePointerDown)
  }, [open])

  if (classes.length === 0) return null

  if (digitizingClass) {
    const className = classes.find((c) => c.code === digitizingClass)?.name ?? digitizingClass
    return (
      <Button variant="secondary" size="sm" className="h-7 shrink-0 gap-1.5 px-2 text-xs" onClick={onCancel}>
        Click the canvas to place {className}
        <span className="text-muted-foreground">Esc</span>
      </Button>
    )
  }

  return (
    <div ref={containerRef} className="relative shrink-0">
      <Button
        variant="ghost"
        size="icon"
        className="size-7"
        title="New feature (not on the map)"
        onClick={() => setOpen((o) => !o)}
      >
        <PenLine className="size-4" />
        <span className="sr-only">New feature (not on the map)</span>
      </Button>
      {open && (
        <div className="bg-popover text-popover-foreground absolute top-full right-0 z-10 mt-1 w-64 rounded-md border shadow-lg">
          <p className="text-muted-foreground border-b px-2 py-1.5 text-xs">
            Draw a feature that only ever exists on a diagram, never on the map
          </p>
          {classes.map((c) => (
            <button
              key={c.code}
              type="button"
              onClick={() => {
                onStart(c.code)
                setOpen(false)
              }}
              className="hover:bg-accent hover:text-accent-foreground block w-full border-b p-2 text-left text-sm transition-colors last:border-b-0"
            >
              {c.name}
            </button>
          ))}
        </div>
      )}
    </div>
  )
}
