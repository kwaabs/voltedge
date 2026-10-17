import { useRef, useState } from "react"
import type { CSSProperties, PointerEvent as ReactPointerEvent, ReactElement, RefObject } from "react"
import { GripHorizontal } from "lucide-react"

import { cn } from "@/lib/utils"

/**
 * Shared drag + resize mechanics for the map's floating analysis result
 * panels (load-flow, fault-current, ampacity, coordination, voltage-profile,
 * contingency, hosting, isolation) -- every one of them is the same fixed
 * bottom-centre overlay (`bottom-10 left-1/2 -translate-x-1/2`), so rather
 * than copy the pointer-capture drag/resize handlers into each, they share
 * this hook (the same mechanics feature-panel.tsx and trace-result-table.tsx
 * carry inline). A panel wires it in four places: `panelRef` + `shellStyle` +
 * `shellClassName` on its root div, `onHeaderPointerDown`/`headerClassName` +
 * `grip` on its header, `resized` to grow its scroll region, and
 * `resizeHandle` before the root closes.
 *
 * State stays null until the user acts: an untouched panel keeps its Tailwind
 * anchor + default width + `defaultMaxHeightClass` shrink-wrap. Dragging pins
 * an explicit `left/top` (and drops the translate anchor); resizing pins an
 * explicit `width`/`height` (and switches the shrink-wrap cap for a filled,
 * scrollable body). It survives re-renders and successive runs because the
 * panel component stays mounted (it returns null only when there's no
 * result/prompt), so a moved/resized panel keeps its geometry across reruns.
 */

const DEFAULT_POSITION_CLASS = "bottom-10 left-1/2 -translate-x-1/2"
const MIN_WIDTH = 320
const MIN_HEIGHT = 160

interface FloatingPanel {
  /** Attach to the panel's root div. */
  panelRef: RefObject<HTMLDivElement | null>
  /** Inline width (+left/top/height once moved/resized) for the root div. */
  shellStyle: CSSProperties
  /** Full className for the root div (positioning, flex column, shrink-wrap). */
  shellClassName: string
  /** true once the user has resized -- the body should fill + scroll. */
  resized: boolean
  /** Attach to the header's onPointerDown (starts a drag). */
  onHeaderPointerDown: (e: ReactPointerEvent) => void
  /** className for the draggable header (grab cursor, no text selection). */
  headerClassName: string
  /** The drag-affordance grip icon to render at the start of the header. */
  grip: ReactElement
  /** The bottom-right resize handle to render before the root div closes. */
  resizeHandle: ReactElement
}

export function useFloatingPanel(
  defaultWidth: number,
  defaultMaxHeightClass: string,
  // options.positionClass overrides the default bottom-centre anchor (e.g. the
  // network-audit panel anchors bottom-LEFT and stretches right with
  // "bottom-6 left-6 right-6"). options.stretch pairs with a left+right anchor:
  // the panel takes its width from those anchors (no explicit width) until it's
  // moved or resized, so it fills the span instead of a fixed 600px.
  options?: { positionClass?: string; stretch?: boolean },
): FloatingPanel {
  const [position, setPosition] = useState<{ x: number; y: number } | null>(null)
  const [size, setSize] = useState<{ width: number; height: number } | null>(null)
  const panelRef = useRef<HTMLDivElement>(null)
  const positionClass = options?.positionClass ?? DEFAULT_POSITION_CLASS
  const stretch = options?.stretch ?? false

  function onHeaderPointerDown(e: ReactPointerEvent) {
    // Left button only, and never when the press lands on an interactive
    // control in the header (e.g. the close button) -- those keep their click.
    if (e.button !== 0) return
    if ((e.target as HTMLElement).closest("button, a, input, select, textarea")) return
    const panel = panelRef.current
    if (!panel) return
    const rect = panel.getBoundingClientRect()
    const offsetX = e.clientX - rect.left
    const offsetY = e.clientY - rect.top
    // Pin the current on-screen position first so dropping the translate
    // anchor doesn't make the panel jump.
    setPosition({ x: rect.left, y: rect.top })
    // A stretched panel is left+right anchored with no explicit width; dropping the
    // right anchor on drag would collapse it to content width, so pin its current
    // size -- it becomes a fixed, scrollable floating panel at its stretched size.
    if (stretch && size == null) {
      setSize({ width: rect.width, height: rect.height })
    }
    e.currentTarget.setPointerCapture(e.pointerId)

    function onMove(ev: PointerEvent) {
      setPosition({ x: ev.clientX - offsetX, y: ev.clientY - offsetY })
    }
    function onUp() {
      window.removeEventListener("pointermove", onMove)
      window.removeEventListener("pointerup", onUp)
    }
    window.addEventListener("pointermove", onMove)
    window.addEventListener("pointerup", onUp)
  }

  function onResizePointerDown(e: ReactPointerEvent) {
    if (e.button !== 0) return
    e.preventDefault()
    e.stopPropagation()
    const panel = panelRef.current
    if (!panel) return
    const rect = panel.getBoundingClientRect()
    // Pin position before sizing so the panel resizes from where it sits
    // rather than staying anchored/translated.
    if (!position) setPosition({ x: rect.left, y: rect.top })
    const startX = e.clientX
    const startY = e.clientY
    const startW = rect.width
    const startH = rect.height
    e.currentTarget.setPointerCapture(e.pointerId)

    function onMove(ev: PointerEvent) {
      setSize({
        width: Math.max(MIN_WIDTH, startW + (ev.clientX - startX)),
        height: Math.max(MIN_HEIGHT, startH + (ev.clientY - startY)),
      })
    }
    function onUp() {
      window.removeEventListener("pointermove", onMove)
      window.removeEventListener("pointerup", onUp)
    }
    window.addEventListener("pointermove", onMove)
    window.addEventListener("pointerup", onUp)
  }

  const resized = size?.height != null
  // A stretched panel left in its default spot takes its width from the left+right
  // anchors in positionClass, so it carries no explicit width until moved/resized.
  const stretchedDefault = stretch && position == null && size == null
  const shellStyle: CSSProperties = {
    ...(stretchedDefault ? {} : { width: size?.width ?? defaultWidth }),
    ...(position ? { left: position.x, top: position.y } : {}),
    ...(resized ? { height: size?.height } : {}),
  }
  const shellClassName = cn(
    // Floating card chrome: a copper top-edge accent (the theme's --primary)
    // over the popover surface, a defined border, and a soft raised shadow so
    // the panel reads as a distinct floating object rather than a flat grey box.
    "bg-popover text-popover-foreground fixed z-40 flex flex-col max-w-[calc(100vw-2rem)] overflow-hidden rounded-lg border border-border/70 border-t-2 border-t-[oklch(0.45_0.10_252)] dark:border-t-primary shadow-xl ring-1 ring-black/5",
    !resized && defaultMaxHeightClass,
    !position && positionClass,
  )
  // Themed header band so the title/close row reads as a header, not another
  // sheet of white/grey. In LIGHT mode it echoes the app's blue chrome bars
  // (oklch(0.45 0.10 252)) as a faint cool wash + blue hairline; in DARK mode
  // the `dark:` overrides restore the warm copper/accent look. Matches
  // feature-panel.tsx and trace-result-table.tsx (kept in sync by hand).
  const headerClassName =
    "flex cursor-grab items-center justify-between gap-2 border-b border-[oklch(0.45_0.10_252)]/20 bg-gradient-to-b from-[oklch(0.45_0.10_252)]/15 to-[oklch(0.45_0.10_252)]/5 dark:border-primary/15 dark:from-accent/50 dark:to-accent/20 px-3 py-2 text-foreground select-none active:cursor-grabbing"
  const grip = <GripHorizontal className="text-primary/70 size-3.5 shrink-0" />
  const resizeHandle = (
    <div
      onPointerDown={onResizePointerDown}
      title="Drag to resize"
      style={{ touchAction: "none" }}
      className="absolute right-0.5 bottom-0.5 z-50 flex size-4 cursor-nwse-resize items-end justify-end"
    >
      <span className="border-muted-foreground/40 block size-2 border-r-2 border-b-2" />
    </div>
  )

  return { panelRef, shellStyle, shellClassName, resized, onHeaderPointerDown, headerClassName, grip, resizeHandle }
}
