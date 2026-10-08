import { useRef, useState } from "react"
import type { CSSProperties, JSX, PointerEvent as ReactPointerEvent, RefObject } from "react"
import { GripVertical } from "lucide-react"

/**
 * Drag-only mechanics for the map's compact edit toolbars (geometry-edit and
 * territory-edit) -- the lightweight sibling of useFloatingPanel. Those toolbars
 * are a single row of ghost buttons with no title bar and nothing to resize, and
 * they live `absolute` INSIDE the map column (so they clear the app header and
 * hide with the map), so the analysis-panel hook -- which forces `fixed`, a
 * `bottom-10` default anchor, a header band and a resize handle -- doesn't fit.
 *
 * A toolbar wires this in three places: `ref` on its root div, `style` +
 * `positioned` on that same div (spread the style, and drop the Tailwind anchor
 * classes once `positioned`), and `grip` as the first child (the drag handle).
 *
 * Position is stored relative to the toolbar's offsetParent (the map column), so
 * an explicit left/top lines up with the `absolute` box. It stays null -- the
 * toolbar keeps its Tailwind anchor -- until the first drag, and survives
 * re-renders because the toolbar component stays mounted while editing is active.
 */

interface DraggableToolbar {
  ref: RefObject<HTMLDivElement | null>
  /** Inline left/top once dragged; empty until then (Tailwind anchor holds). */
  style: CSSProperties
  /** true once the user has dragged -- drop the default anchor classes. */
  positioned: boolean
  /** The grip handle to render as the toolbar's first child. */
  grip: JSX.Element
}

export function useDraggableToolbar(): DraggableToolbar {
  const [pos, setPos] = useState<{ x: number; y: number } | null>(null)
  const ref = useRef<HTMLDivElement>(null)

  // The toolbar is positioned relative to its offsetParent (the map column), so
  // a drag translates viewport pointer coords into that parent's frame.
  function parentOrigin(): { left: number; top: number } {
    const parent = ref.current?.offsetParent as HTMLElement | null
    const rect = parent?.getBoundingClientRect()
    return { left: rect?.left ?? 0, top: rect?.top ?? 0 }
  }

  function onGripPointerDown(e: ReactPointerEvent) {
    if (e.button !== 0) return
    const el = ref.current
    if (!el) return
    const rect = el.getBoundingClientRect()
    const origin = parentOrigin()
    // Where inside the toolbar the grab landed, so the bar tracks the cursor
    // without snapping its corner to the pointer.
    const offsetX = e.clientX - rect.left
    const offsetY = e.clientY - rect.top
    // Pin the current on-screen spot first so dropping the -translate-x-1/2
    // anchor on the next render doesn't make the bar jump.
    setPos({ x: rect.left - origin.left, y: rect.top - origin.top })
    e.currentTarget.setPointerCapture(e.pointerId)

    function onMove(ev: PointerEvent) {
      const o = parentOrigin()
      setPos({ x: ev.clientX - offsetX - o.left, y: ev.clientY - offsetY - o.top })
    }
    function onUp() {
      window.removeEventListener("pointermove", onMove)
      window.removeEventListener("pointerup", onUp)
    }
    window.addEventListener("pointermove", onMove)
    window.addEventListener("pointerup", onUp)
  }

  const grip = (
    <button
      type="button"
      onPointerDown={onGripPointerDown}
      title="Drag to move"
      aria-label="Drag to move this toolbar"
      style={{ touchAction: "none" }}
      className="text-primary/70 hover:text-primary flex cursor-grab items-center self-stretch px-0.5 active:cursor-grabbing"
    >
      <GripVertical className="size-4" />
    </button>
  )

  return {
    ref,
    style: pos ? { left: pos.x, top: pos.y } : {},
    positioned: pos != null,
    grip,
  }
}
