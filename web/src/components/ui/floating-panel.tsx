import { GripVertical, X } from "lucide-react"
import { useCallback, useEffect, useRef, useState, type PointerEvent as ReactPointerEvent, type ReactNode } from "react"

import { Button } from "@/components/ui/button"
import { cn } from "@/lib/utils"

export interface PanelRect {
  x: number
  y: number
  width: number
  height: number
}

interface FloatingPanelProps {
  title: ReactNode
  onClose: () => void
  children: ReactNode
  /** Starting rectangle the first time the panel mounts. Position/size are
   * owned internally from there on (drag/resize mutate local state), so a
   * caller only needs to decide where it first appears. */
  initialRect?: Partial<PanelRect>
  minWidth?: number
  minHeight?: number
  /** Extra classes for the outer window (e.g. a z-index override). */
  className?: string
  /** When set, the panel's position and size are remembered under this key in
   * localStorage, so it reopens where it was last left (and survives a reload).
   * Absent = the default behaviour: geometry is ephemeral, seeded from
   * initialRect on every mount. The stored rect is re-clamped to the viewport on
   * load, so a window saved on a larger screen can't open off a smaller one. */
  persistKey?: string
}

const DEFAULT_RECT: PanelRect = { x: 120, y: 96, width: 480, height: 420 }
const MARGIN = 8

/** Keeps a rect fully inside the viewport, shrinking it if the window is
 * smaller than the panel and pinning it to the top-left margin at minimum --
 * so a panel can never end up dragged entirely off-screen or opened larger
 * than the window on a small display. */
function clampRect(rect: PanelRect, minWidth: number, minHeight: number): PanelRect {
  const maxWidth = Math.max(minWidth, window.innerWidth - MARGIN * 2)
  const maxHeight = Math.max(minHeight, window.innerHeight - MARGIN * 2)
  const width = Math.min(Math.max(rect.width, minWidth), maxWidth)
  const height = Math.min(Math.max(rect.height, minHeight), maxHeight)
  const x = Math.min(Math.max(rect.x, MARGIN), window.innerWidth - width - MARGIN)
  const y = Math.min(Math.max(rect.y, MARGIN), window.innerHeight - height - MARGIN)
  return { x, y, width, height }
}

/**
 * A draggable, resizable, closeable floating window -- the reusable shell the
 * query builder (docs/phase-13-query-builder.md) lives in, kept generic so any
 * later floating tool can reuse it. Drag by the header, resize from the
 * bottom-right grip, close with the X. Position and size are clamped to the
 * viewport on every drag/resize and re-clamped on window resize, so the window
 * can't be lost off-screen.
 *
 * Self-contained: it owns its own rect state seeded from `initialRect`, rather
 * than threading position through a store -- a floating window's geometry is
 * pure view state with no reason to survive a remount, and keeping it local
 * means a caller only wires up `onClose`.
 */
export function FloatingPanel({
  title,
  onClose,
  children,
  initialRect,
  minWidth = 320,
  minHeight = 200,
  className,
  persistKey,
}: FloatingPanelProps) {
  const storageKey = persistKey ? `voltedge-panel-rect:${persistKey}` : null
  const [rect, setRect] = useState<PanelRect>(() => {
    if (storageKey) {
      try {
        const saved = JSON.parse(localStorage.getItem(storageKey) ?? "null")
        if (
          saved &&
          typeof saved.x === "number" &&
          typeof saved.y === "number" &&
          typeof saved.width === "number" &&
          typeof saved.height === "number"
        ) {
          return clampRect(saved, minWidth, minHeight)
        }
      } catch {
        // Corrupt/blocked storage: fall through to the seeded default.
      }
    }
    return clampRect({ ...DEFAULT_RECT, ...initialRect }, minWidth, minHeight)
  })

  // Remember the rect under persistKey, so a reopen (or reload) restores it. Writes
  // on every drag frame are cheap (a tiny JSON) and always reflect the latest shape.
  useEffect(() => {
    if (!storageKey) return
    try {
      localStorage.setItem(storageKey, JSON.stringify(rect))
    } catch {
      // Storage full/blocked -- persistence is best-effort, never fatal.
    }
  }, [storageKey, rect])

  // Active drag/resize gesture. Null when idle. Held in a ref so the pointer
  // handlers don't need to be re-bound as the pointer moves.
  const gesture = useRef<
    | { kind: "drag"; pointerX: number; pointerY: number; startX: number; startY: number }
    | { kind: "resize"; pointerX: number; pointerY: number; startW: number; startH: number }
    | null
  >(null)

  const onPointerMove = useCallback(
    (e: PointerEvent) => {
      const g = gesture.current
      if (!g) return
      if (g.kind === "drag") {
        setRect((r) =>
          clampRect(
            { ...r, x: g.startX + (e.clientX - g.pointerX), y: g.startY + (e.clientY - g.pointerY) },
            minWidth,
            minHeight,
          ),
        )
      } else {
        setRect((r) =>
          clampRect(
            { ...r, width: g.startW + (e.clientX - g.pointerX), height: g.startH + (e.clientY - g.pointerY) },
            minWidth,
            minHeight,
          ),
        )
      }
    },
    [minWidth, minHeight],
  )

  const endGesture = useCallback(() => {
    gesture.current = null
  }, [])

  // Bind move/up on the window for the gesture's lifetime -- listening on the
  // window (not the header) means a fast drag that outruns the pointer still
  // tracks, and the gesture ends wherever the pointer is released.
  useEffect(() => {
    window.addEventListener("pointermove", onPointerMove)
    window.addEventListener("pointerup", endGesture)
    return () => {
      window.removeEventListener("pointermove", onPointerMove)
      window.removeEventListener("pointerup", endGesture)
    }
  }, [onPointerMove, endGesture])

  // Re-clamp if the viewport shrinks under the panel (e.g. window resize).
  useEffect(() => {
    const onResize = () => setRect((r) => clampRect(r, minWidth, minHeight))
    window.addEventListener("resize", onResize)
    return () => window.removeEventListener("resize", onResize)
  }, [minWidth, minHeight])

  function startDrag(e: ReactPointerEvent) {
    gesture.current = { kind: "drag", pointerX: e.clientX, pointerY: e.clientY, startX: rect.x, startY: rect.y }
  }

  function startResize(e: ReactPointerEvent) {
    e.stopPropagation()
    gesture.current = {
      kind: "resize",
      pointerX: e.clientX,
      pointerY: e.clientY,
      startW: rect.width,
      startH: rect.height,
    }
  }

  return (
    <div
      className={cn(
        // Copper top-edge accent + soft raised shadow + ring so the window
        // reads as a distinct floating object, matching the analysis panels'
        // shared chrome (use-floating-panel.tsx) rather than a flat grey box.
        "bg-popover text-popover-foreground pointer-events-auto fixed z-40 flex flex-col overflow-hidden rounded-lg border border-border/70 border-t-2 border-t-[oklch(0.45_0.10_252)] dark:border-t-primary shadow-xl ring-1 ring-black/5",
        className,
      )}
      style={{ left: rect.x, top: rect.y, width: rect.width, height: rect.height }}
    >
      <div
        onPointerDown={startDrag}
        className="border-[oklch(0.45_0.10_252)]/20 dark:border-primary/15 flex cursor-move items-center justify-between gap-2 border-b bg-gradient-to-b from-[oklch(0.45_0.10_252)]/15 to-[oklch(0.45_0.10_252)]/5 dark:from-accent/50 dark:to-accent/20 px-3 py-2 select-none"
      >
        <GripVertical className="text-primary/70 size-3.5 shrink-0" />
        <div className="min-w-0 flex-1 truncate text-sm font-medium">{title}</div>
        <Button variant="ghost" size="icon" className="size-6 shrink-0" onClick={onClose}>
          <X className="size-3.5" />
          <span className="sr-only">Close</span>
        </Button>
      </div>

      <div className="min-h-0 flex-1 overflow-auto">{children}</div>

      <div
        onPointerDown={startResize}
        className="text-muted-foreground/60 hover:text-muted-foreground absolute right-0 bottom-0 flex size-4 cursor-nwse-resize items-end justify-end"
        aria-hidden
      >
        <GripVertical className="size-3 rotate-45" />
      </div>
    </div>
  )
}
