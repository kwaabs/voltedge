import { ArrowUpRight, Minus, PencilLine, Square, Type, X } from "lucide-react"

import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { usePermission } from "@/hooks/use-auth"
import type { AnnotationKind } from "@/lib/map-layers"
import { useAnnotationEditStore } from "@/stores/annotation-edit-store"
import { useEditStore } from "@/stores/edit-store"
import { useToolbarMenuStore } from "@/stores/toolbar-menu-store"

/** The four redline kinds, in the order they appear in the menu, each with its
 * icon, label, and the one-line instruction shown while drawing. */
const KINDS: { kind: AnnotationKind; label: string; icon: typeof Type; banner: string }[] = [
  { kind: "text", label: "Text note", icon: Type, banner: "Click the map to place the note" },
  { kind: "box", label: "Box / area", icon: Square, banner: "Click to add corners, double-click to finish the box" },
  { kind: "line", label: "Line", icon: Minus, banner: "Click to add points, double-click to finish the line" },
  { kind: "arrow", label: "Arrow", icon: ArrowUpRight, banner: "Click the start then the end of the arrow, double-click to finish" },
]

/**
 * Entry point for drawing a map annotation (Phase 36 Slice 4) -- the redline
 * sibling of new-feature-menu.tsx. Picking a kind arms a draw session
 * (annotation-edit-store.ts's `drawingKind`); voltedge-map.tsx owns the actual
 * OL Draw interaction, keyed off it, and on drawend creates the annotation and
 * opens it for editing. Gated on `annotations:write` -- a caller without it
 * can still see the overlay (the Layers toggle) but is never offered the
 * authoring controls the server would refuse.
 */
export function NewAnnotationMenu() {
  const canWrite = usePermission()("annotations:write")
  const drawingKind = useAnnotationEditStore((s) => s.drawingKind)
  const startDrawing = useAnnotationEditStore((s) => s.startDrawing)
  const cancelDrawing = useAnnotationEditStore((s) => s.cancelDrawing)
  // Drawing a redline arms its own OL Draw interaction, so it's blocked while an
  // existing feature's geometry is under edit -- same reason as new-feature-menu.tsx.
  const editing = useEditStore((s) => s.editing)
  // Only one toolbar dropdown open at a time -- see toolbar-menu-store.ts.
  const menuOpen = useToolbarMenuStore((s) => s.open === "annotation")
  const setMenuOpen = useToolbarMenuStore((s) => s.setOpen)

  if (!canWrite) return null

  if (drawingKind) {
    const banner = KINDS.find((k) => k.kind === drawingKind)?.banner ?? ""
    return (
      <div className="bg-background/90 flex items-center gap-2 rounded-md border py-1.5 pr-1.5 pl-3 text-sm shadow-sm">
        <span className="size-2 shrink-0 animate-pulse rounded-full" style={{ backgroundColor: "#dc2626" }} />
        {banner}
        <Button variant="ghost" size="icon" className="size-6 shrink-0" onClick={cancelDrawing}>
          <X className="size-3.5" />
          <span className="sr-only">Cancel</span>
        </Button>
      </div>
    )
  }

  return (
    <DropdownMenu open={menuOpen && !editing} onOpenChange={(o) => setMenuOpen("annotation", o)}>
      <DropdownMenuTrigger asChild>
        <Button
          variant="outline"
          size="sm"
          className="bg-background/90 gap-1.5 shadow-sm"
          disabled={Boolean(editing)}
          title={editing ? "Finish or cancel the current geometry edit first" : undefined}
        >
          <PencilLine className="size-4" style={{ color: "#dc2626" }} />
          Annotate
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-48">
        <DropdownMenuLabel>Annotation</DropdownMenuLabel>
        <DropdownMenuSeparator />
        {KINDS.map(({ kind, label, icon: Icon }) => (
          <DropdownMenuItem key={kind} onSelect={() => startDrawing(kind)}>
            <Icon className="size-4" />
            {label}
          </DropdownMenuItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
