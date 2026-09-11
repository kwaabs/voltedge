import { ArrowLeftRight, Bold, Italic, Move, RotateCw, Save, Spline, Trash2, X } from "lucide-react"
import { useEffect, useState } from "react"

import { Button } from "@/components/ui/button"
import {
  useDeleteAnnotation,
  useMapAnnotations,
  useUpdateAnnotation,
} from "@/hooks/use-map-annotations"
import { LINE_DASH_STYLES, type LineDashStyle } from "@/lib/line-dash"
import { ANNOTATION_DEFAULTS, type AnnotationStyle } from "@/lib/map-layers"
import { cn } from "@/lib/utils"
import { useAnnotationEditStore } from "@/stores/annotation-edit-store"
import { useMapAnnotationsStore } from "@/stores/map-annotations-store"
import { useToastStore } from "@/stores/toast-store"

const KIND_LABEL: Record<string, string> = {
  text: "Text note",
  box: "Box / area",
  line: "Line",
  arrow: "Arrow",
}

const DASH_LABEL: Record<LineDashStyle, string> = {
  solid: "Solid",
  dashed: "Dashed",
  dotted: "Dotted",
  dashdot: "Dash-dot",
}

/**
 * The map-annotation editor (Phase 36 Slice 4) -- a compact floating card shown
 * while annotation-edit-store.ts has an annotation selected. Slice 4a edits the
 * note text and stacking order and deletes; Slice 4b adds the per-instance style
 * controls (colour / stroke / dash / box fill / font / text rotation) and the
 * in-map geometry gestures (Move, Reshape). Only reachable through the
 * write-gated selection path, so it never needs its own permission check. Reads
 * the selected row straight from the already-fetched overlay data.
 *
 * All text/style edits are staged locally and applied on Save (one PATCH),
 * matching the geometry gesture that stages `dirtyGeometry` in the store -- the
 * whole card commits atomically rather than mutating per keystroke.
 */
export function AnnotationEditPanel() {
  const selectedId = useAnnotationEditStore((s) => s.selectedId)
  const deselect = useAnnotationEditStore((s) => s.deselect)
  const tool = useAnnotationEditStore((s) => s.tool)
  const dirtyGeometry = useAnnotationEditStore((s) => s.dirtyGeometry)
  const dirtyRotation = useAnnotationEditStore((s) => s.dirtyRotation)
  const setTool = useAnnotationEditStore((s) => s.setTool)
  const overlayVisible = useMapAnnotationsStore((s) => s.visible)
  // Selection only happens with the overlay on, so the list is already cached;
  // passing the live toggle keeps the query consistent with the map.
  const { data } = useMapAnnotations(overlayVisible)
  const update = useUpdateAnnotation()
  const del = useDeleteAnnotation()

  const annotation = data?.annotations.find((a) => a.id === selectedId) ?? null

  // Local form state, seeded from the row's style (falling back to the same
  // redline defaults the renderer uses, so an unset knob shows its real value).
  const [text, setText] = useState("")
  const [zIndex, setZIndex] = useState(0)
  const [color, setColor] = useState(ANNOTATION_DEFAULTS.color)
  const [strokeWidth, setStrokeWidth] = useState(ANNOTATION_DEFAULTS.strokeWidth)
  const [dash, setDash] = useState<LineDashStyle>("solid")
  const [fill, setFill] = useState(ANNOTATION_DEFAULTS.color)
  const [fillOpacity, setFillOpacity] = useState(ANNOTATION_DEFAULTS.boxFillOpacity)
  const [fontSize, setFontSize] = useState(ANNOTATION_DEFAULTS.fontSize)
  const [bold, setBold] = useState(false)
  const [italic, setItalic] = useState(false)
  const [rotation, setRotation] = useState(0)
  const [doubleHeaded, setDoubleHeaded] = useState(false)

  // Reseed whenever the selected row changes identity (a different annotation,
  // or the same one after a save round-trips fresh data). Keyed on id alone:
  // reseeding on every style field would fight the user's in-progress edits.
  useEffect(() => {
    if (!annotation) return
    const s = annotation.style ?? {}
    setText(annotation.text ?? "")
    setZIndex(annotation.z_index)
    setColor(s.color ?? ANNOTATION_DEFAULTS.color)
    setStrokeWidth(s.stroke_width ?? ANNOTATION_DEFAULTS.strokeWidth)
    setDash(s.dash ?? "solid")
    setFill(s.fill ?? s.color ?? ANNOTATION_DEFAULTS.color)
    setFillOpacity(s.fill_opacity ?? ANNOTATION_DEFAULTS.boxFillOpacity)
    setFontSize(s.font_size ?? ANNOTATION_DEFAULTS.fontSize)
    setBold(s.bold === true)
    setItalic(s.italic === true)
    setRotation(s.rotation ?? 0)
    setDoubleHeaded(s.double === true)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [annotation?.id])

  // Fold the in-map rotate gesture's live angle into the local style state so
  // buildStyle()/Save carry it (the gesture stages degrees in the store's
  // dirtyRotation as the user drags on the map -- see voltedge-map.tsx). Only
  // tracks non-null so it never overrides the id-keyed reseed above with 0.
  useEffect(() => {
    if (dirtyRotation !== null) setRotation(Math.round(dirtyRotation))
  }, [dirtyRotation])

  if (!selectedId || !annotation) return null

  const kind = annotation.kind
  const isText = kind === "text"
  const isBox = kind === "box"
  const isArrow = kind === "arrow"
  const hasStroke = kind === "line" || kind === "arrow" || isBox
  // A text note is a single Point -- no vertices to reshape, only move.
  const canReshape = !isText

  // Assemble the style jsonb for this kind -- only the knobs it actually uses,
  // so a box never carries font fields nor a note a fill.
  function buildStyle(): AnnotationStyle {
    const s: AnnotationStyle = {}
    if (hasStroke) {
      s.color = color
      s.stroke_width = strokeWidth
      s.dash = dash
    }
    if (isArrow) {
      s.double = doubleHeaded
    }
    if (isBox) {
      s.fill = fill
      s.fill_opacity = fillOpacity
    }
    if (isText) {
      s.color = color
      s.font_size = fontSize
      s.bold = bold
      s.italic = italic
      s.rotation = rotation
    }
    return s
  }

  // A text note must carry non-empty text (the server rejects an empty one);
  // a caption on the other kinds is optional.
  const textInvalid = isText && text.trim() === ""
  const canSave = !textInvalid && !update.isPending

  function handleSave() {
    if (!annotation || !canSave) return
    update.mutate(
      {
        id: annotation.id,
        // Send the raw text string, not null: the API's *string field can't tell
        // JSON null from an absent field (both mean "unchanged"), so clearing a
        // caption has to go through as "" -- which the non-text kinds accept and
        // canSave forbids for a text note. Include geometry only when an in-map
        // gesture staged one.
        body: {
          text,
          z_index: zIndex,
          style: buildStyle(),
          ...(dirtyGeometry ? { geometry: dirtyGeometry } : {}),
        },
      },
      {
        // Keep the annotation selected (panel stays open) but drop the staged
        // geometry + disarm the tool, which lets the overlay repopulate with the
        // saved shape (see the load effect's `annotationTool` guard).
        onSuccess: () => useAnnotationEditStore.getState().clearGeometry(),
        onError: (err) =>
          useToastStore.getState().pushError(err.message || "Could not save the annotation."),
      },
    )
  }

  function handleDelete() {
    if (!annotation) return
    del.mutate(
      { id: annotation.id },
      {
        onSuccess: () => useAnnotationEditStore.getState().deselect(),
        onError: (err) =>
          useToastStore.getState().pushError(err.message || "Could not delete the annotation."),
      },
    )
  }

  return (
    <div className="bg-popover text-popover-foreground fixed bottom-3 left-3 z-40 flex max-h-[calc(100vh-1.5rem)] w-72 flex-col gap-3 overflow-y-auto rounded-md border p-3 shadow-lg">
      <div className="flex items-center justify-between">
        <span className="text-sm font-medium">{KIND_LABEL[kind] ?? "Annotation"}</span>
        <Button variant="ghost" size="icon" className="size-6" onClick={deselect}>
          <X className="size-3.5" />
          <span className="sr-only">Close</span>
        </Button>
      </div>

      {/* Geometry gestures -- the map attaches an OL interaction to this
          annotation and stages the result for Save. Move (Translate) is offered
          for every kind; the other slot is Reshape (Modify) for shapes with
          vertices, or Rotate (a drag-to-spin gesture) for a lone text Point. */}
      <div className="flex items-center gap-1">
        <Button
          variant="ghost"
          size="sm"
          className={cn("flex-1", tool === "move" && "bg-accent text-accent-foreground")}
          onClick={() => setTool(tool === "move" ? null : "move")}
        >
          <Move className="size-4" />
          Move
        </Button>
        {isText ? (
          <Button
            variant="ghost"
            size="sm"
            className={cn("flex-1", tool === "rotate" && "bg-accent text-accent-foreground")}
            title="Drag on the map to spin the note; the angle updates live"
            onClick={() => setTool(tool === "rotate" ? null : "rotate")}
          >
            <RotateCw className="size-4" />
            Rotate
          </Button>
        ) : (
          <Button
            variant="ghost"
            size="sm"
            className={cn("flex-1", tool === "vertices" && "bg-accent text-accent-foreground")}
            disabled={!canReshape}
            onClick={() => setTool(tool === "vertices" ? null : "vertices")}
          >
            <Spline className="size-4" />
            Reshape
          </Button>
        )}
      </div>

      <label className="flex flex-col gap-1 text-xs">
        <span className="text-muted-foreground">{isText ? "Note text" : "Caption (optional)"}</span>
        <textarea
          value={text}
          onChange={(e) => setText(e.target.value)}
          rows={2}
          placeholder={isText ? "Type the note..." : "Optional label"}
          className="border-input bg-background focus-visible:ring-ring resize-none rounded-md border px-2 py-1 text-sm focus-visible:ring-1 focus-visible:outline-none"
        />
      </label>

      {/* Colour -- stroke/text for lines/arrows/text, outline for a box. */}
      <label className="flex items-center justify-between gap-2 text-xs">
        <span className="text-muted-foreground">{isBox ? "Outline colour" : "Colour"}</span>
        <input
          type="color"
          value={color}
          onChange={(e) => setColor(e.target.value)}
          className="border-input h-7 w-12 cursor-pointer rounded border bg-transparent p-0.5"
        />
      </label>

      {hasStroke && (
        <>
          <label className="flex items-center justify-between gap-2 text-xs">
            <span className="text-muted-foreground">Stroke width</span>
            <input
              type="number"
              min={1}
              max={20}
              value={strokeWidth}
              onChange={(e) => setStrokeWidth(Math.max(1, Number.parseInt(e.target.value, 10) || 1))}
              className="border-input bg-background focus-visible:ring-ring w-20 rounded-md border px-2 py-1 text-right text-sm focus-visible:ring-1 focus-visible:outline-none"
            />
          </label>
          <label className="flex items-center justify-between gap-2 text-xs">
            <span className="text-muted-foreground">Line style</span>
            <select
              value={dash}
              onChange={(e) => setDash(e.target.value as LineDashStyle)}
              className="border-input bg-background focus-visible:ring-ring rounded-md border px-2 py-1 text-sm focus-visible:ring-1 focus-visible:outline-none"
            >
              {LINE_DASH_STYLES.map((d) => (
                <option key={d} value={d}>
                  {DASH_LABEL[d]}
                </option>
              ))}
            </select>
          </label>
        </>
      )}

      {isArrow && (
        <Button
          variant="ghost"
          size="sm"
          className={cn("w-full justify-start", doubleHeaded && "bg-accent text-accent-foreground")}
          title="Add an arrowhead at the start too, so the arrow points both ways"
          onClick={() => setDoubleHeaded((d) => !d)}
        >
          <ArrowLeftRight className="size-4" />
          Double-headed
        </Button>
      )}

      {isBox && (
        <>
          <label className="flex items-center justify-between gap-2 text-xs">
            <span className="text-muted-foreground">Fill colour</span>
            <input
              type="color"
              value={fill}
              onChange={(e) => setFill(e.target.value)}
              className="border-input h-7 w-12 cursor-pointer rounded border bg-transparent p-0.5"
            />
          </label>
          <label className="flex items-center justify-between gap-2 text-xs">
            <span className="text-muted-foreground">Fill opacity</span>
            <span className="flex items-center gap-2">
              <input
                type="range"
                min={0}
                max={100}
                value={Math.round(fillOpacity * 100)}
                onChange={(e) => setFillOpacity((Number.parseInt(e.target.value, 10) || 0) / 100)}
              />
              <span className="text-muted-foreground w-8 text-right tabular-nums">
                {Math.round(fillOpacity * 100)}%
              </span>
            </span>
          </label>
        </>
      )}

      {isText && (
        <>
          <label className="flex items-center justify-between gap-2 text-xs">
            <span className="text-muted-foreground">Font size</span>
            <input
              type="number"
              min={8}
              max={72}
              value={fontSize}
              onChange={(e) => setFontSize(Math.max(8, Number.parseInt(e.target.value, 10) || 8))}
              className="border-input bg-background focus-visible:ring-ring w-20 rounded-md border px-2 py-1 text-right text-sm focus-visible:ring-1 focus-visible:outline-none"
            />
          </label>
          {/* Rotation is set by the in-map Rotate gesture (drag to spin), not a
              slider -- this is a read-only readout of the current angle. */}
          <div className="flex items-center justify-between gap-2 text-xs">
            <span className="text-muted-foreground">Rotation</span>
            <span className="text-muted-foreground tabular-nums">{Math.round(rotation)}°</span>
          </div>
          <div className="flex items-center gap-1">
            <Button
              variant="ghost"
              size="sm"
              className={cn("flex-1", bold && "bg-accent text-accent-foreground")}
              onClick={() => setBold((b) => !b)}
            >
              <Bold className="size-4" />
              Bold
            </Button>
            <Button
              variant="ghost"
              size="sm"
              className={cn("flex-1", italic && "bg-accent text-accent-foreground")}
              onClick={() => setItalic((i) => !i)}
            >
              <Italic className="size-4" />
              Italic
            </Button>
          </div>
        </>
      )}

      <label className="flex items-center justify-between gap-2 text-xs">
        <span className="text-muted-foreground">Stacking order</span>
        <input
          type="number"
          value={zIndex}
          onChange={(e) => setZIndex(Number.parseInt(e.target.value, 10) || 0)}
          className="border-input bg-background focus-visible:ring-ring w-20 rounded-md border px-2 py-1 text-right text-sm focus-visible:ring-1 focus-visible:outline-none"
        />
      </label>

      <div className="flex items-center justify-between gap-2 border-t pt-2">
        <Button variant="ghost" size="sm" className="text-destructive" disabled={del.isPending} onClick={handleDelete}>
          <Trash2 className="size-4" />
          Delete
        </Button>
        <Button size="sm" disabled={!canSave} onClick={handleSave}>
          <Save className="size-4" />
          Save
        </Button>
      </div>
    </div>
  )
}
