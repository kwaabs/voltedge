import { type PointerEvent as RPointerEvent, type ReactNode, useEffect, useMemo, useRef, useState } from "react"
import { ArrowDown, ArrowUp, Loader2, Maximize2, Package, Plus, Trash2, ZoomIn, ZoomOut } from "lucide-react"

import { Button } from "@/components/ui/button"
import { ApiError } from "@/lib/api"
import { cn } from "@/lib/utils"
import { useFeatureClasses } from "@/hooks/use-feature-classes"
import { useToastStore } from "@/stores/toast-store"
import {
  type Kit,
  type KitComponent,
  type KitDiagramPreviewElement,
  type KitInput,
  type KitValidation,
  useAssemblyKit,
  useAssemblyKits,
  useDeleteAssemblyKit,
  useKitDiagramPreview,
  useSaveAssemblyKit,
  useValidateAssemblyKit,
} from "@/hooks/use-assemblies"

const inputClass = "border-input bg-background h-8 w-full rounded-md border px-2 text-sm outline-none focus-visible:ring-1 focus-visible:ring-ring"

/** A kit draft being edited: the API input plus the id when editing an existing
 * one (absent = a new kit). position is assigned from row order on save, so the
 * component rows here carry everything BUT position. */
type DraftComponent = Omit<KitComponent, "position">
interface Draft {
  id?: string
  anchor_feature_class: string
  code: string
  name: string
  anchor_subtype_code: string // kept as string for the input; parsed on save
  diagram_template_name: string
  components: DraftComponent[]
}

const emptyComponent: DraftComponent = {
  component_feature_class: "",
  subtype_code: null,
  attributes: undefined,
  support_kind: null,
  support_parent_position: null,
  wire_to_position: null,
  own_terminal: null,
  upstream_terminal: null,
  offset_x: 0,
  offset_y: 80,
  rotation: 0,
}

function draftFromKit(kit: Kit): Draft {
  return {
    id: kit.id,
    anchor_feature_class: kit.anchor_feature_class,
    code: kit.code,
    name: kit.name,
    anchor_subtype_code: kit.anchor_subtype_code != null ? String(kit.anchor_subtype_code) : "",
    diagram_template_name: kit.diagram_template_name ?? "",
    components: kit.components
      .slice()
      .sort((a, b) => a.position - b.position)
      .map(({ position: _position, ...rest }) => rest),
  }
}

function blankDraft(): Draft {
  return {
    anchor_feature_class: "",
    code: "",
    name: "",
    anchor_subtype_code: "",
    diagram_template_name: "",
    components: [{ ...emptyComponent }],
  }
}

/** Build the API body from a draft: reindex positions 1..N by row order, parse the
 * optional numbers, and drop the empty anchor-subtype/diagram-name to null. Throws
 * on a malformed attributes JSON (surfaced as a toast by the caller). */
function toInput(d: Draft): KitInput {
  return {
    anchor_feature_class: d.anchor_feature_class.trim(),
    code: d.code.trim(),
    name: d.name.trim(),
    anchor_subtype_code: d.anchor_subtype_code.trim() === "" ? null : Number(d.anchor_subtype_code),
    diagram_template_name: d.diagram_template_name.trim() === "" ? null : d.diagram_template_name.trim(),
    components: d.components.map((c, i) => ({
      ...c,
      component_feature_class: c.component_feature_class.trim(),
      position: i + 1,
    })),
  }
}

export function AdminAssembliesPage() {
  const list = useAssemblyKits()
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [draft, setDraft] = useState<Draft | null>(null)
  const [validation, setValidation] = useState<KitValidation | null>(null)

  const selected = useAssemblyKit(selectedId)
  const classes = useFeatureClasses()
  const save = useSaveAssemblyKit()
  const del = useDeleteAssemblyKit()
  const validate = useValidateAssemblyKit()

  // Seed the editor when a kit finishes loading (or stays null while creating).
  useEffect(() => {
    if (selectedId && selected.data) {
      setDraft(draftFromKit(selected.data))
      setValidation(null)
    }
  }, [selectedId, selected.data])

  const allClasses = classes.data?.feature_classes ?? []
  // Components are created WITHOUT map geometry, so only geometry-optional classes
  // can ride a kit -- steer the picker to those (the server enforces it too).
  const componentClasses = useMemo(
    () => allClasses.filter((c) => c.geometry_optional),
    [allClasses],
  )

  function startNew() {
    setSelectedId(null)
    setDraft(blankDraft())
    setValidation(null)
  }

  function patchDraft(p: Partial<Draft>) {
    setDraft((d) => (d ? { ...d, ...p } : d))
  }

  function patchComponent(i: number, p: Partial<DraftComponent>) {
    setDraft((d) => {
      if (!d) return d
      const components = d.components.map((c, j) => (j === i ? { ...c, ...p } : c))
      return { ...d, components }
    })
  }

  function moveComponent(i: number, dir: -1 | 1) {
    setDraft((d) => {
      if (!d) return d
      const j = i + dir
      if (j < 0 || j >= d.components.length) return d
      const components = d.components.slice()
      ;[components[i], components[j]] = [components[j], components[i]]
      return { ...d, components }
    })
  }

  async function onValidate() {
    if (!draft) return
    try {
      const result = await validate.mutateAsync(toInput(draft))
      setValidation(result)
    } catch (err) {
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not validate this kit.")
    }
  }

  async function onSave() {
    if (!draft) return
    try {
      const kit = await save.mutateAsync({ id: draft.id, input: toInput(draft) })
      useToastStore.getState().pushSuccess(draft.id ? "Kit updated." : "Kit created.")
      setSelectedId(kit.id)
      setValidation(null)
    } catch (err) {
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not save this kit.")
    }
  }

  async function onDelete() {
    if (!draft?.id) return
    if (!window.confirm(`Delete the "${draft.name}" kit? This can't be undone.`)) return
    try {
      await del.mutateAsync(draft.id)
      useToastStore.getState().pushSuccess("Kit deleted.")
      setSelectedId(null)
      setDraft(null)
    } catch (err) {
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not delete this kit.")
    }
  }

  return (
    <div className="flex h-[calc(100svh-3.5rem)] flex-col">
      <div className="flex min-h-0 flex-1">
        {/* Left: kit catalog */}
        <nav className="w-72 shrink-0 overflow-y-auto border-r p-3">
          <div className="mb-3 flex items-center justify-between px-1">
            <span className="flex items-center gap-2 font-semibold tracking-tight">
              <Package className="text-primary size-4" />
              Assembly kits
            </span>
            <Button size="sm" variant="outline" onClick={startNew}>
              <Plus className="size-4" /> New
            </Button>
          </div>
          {list.isPending ? (
            <div className="text-muted-foreground flex items-center gap-2 px-1 text-sm">
              <Loader2 className="size-4 animate-spin" /> Loading…
            </div>
          ) : list.isError ? (
            <p className="text-destructive px-1 text-sm">Couldn't load the kit catalog.</p>
          ) : (list.data ?? []).length === 0 ? (
            <p className="text-muted-foreground px-1 text-sm">No kits yet. Create one.</p>
          ) : (
            <div className="flex flex-col gap-1">
              {(list.data ?? []).map((k) => (
                <button
                  key={k.id}
                  type="button"
                  onClick={() => setSelectedId(k.id)}
                  className={cn(
                    "rounded-md px-2 py-1.5 text-left text-sm transition-colors",
                    k.id === selectedId ? "bg-accent text-accent-foreground font-medium" : "hover:bg-accent/50",
                  )}
                >
                  <div className="font-medium">{k.name}</div>
                  <div className="text-muted-foreground text-xs">
                    {k.anchor_feature_class_name} · {k.code} · {k.component_count} component
                    {k.component_count === 1 ? "" : "s"}
                  </div>
                </button>
              ))}
            </div>
          )}
        </nav>

        {/* Right: editor */}
        <div className="min-w-0 flex-1 overflow-y-auto p-5">
          {!draft ? (
            <div className="text-muted-foreground mx-auto max-w-lg p-10 text-center">
              <Package className="text-muted-foreground/60 mx-auto size-10" />
              <h1 className="text-foreground mt-4 text-lg font-semibold">Assembly kits</h1>
              <p className="mt-1 text-sm">
                A kit is a "place one anchor feature and get its whole pre-wired set of components"
                recipe. Pick a kit to edit, or create a new one — it appears in the map's Assemblies
                menu and gets an auto single-line.
              </p>
            </div>
          ) : selectedId && selected.isPending ? (
            <div className="text-muted-foreground flex items-center gap-2 text-sm">
              <Loader2 className="size-4 animate-spin" /> Loading…
            </div>
          ) : (
            <div className="mx-auto max-w-3xl">
              <KitEditor
                draft={draft}
                classes={allClasses}
                componentClasses={componentClasses}
                onPatch={patchDraft}
                onPatchComponent={patchComponent}
                onAddComponent={() =>
                  patchDraft({ components: [...draft.components, { ...emptyComponent }] })
                }
                onRemoveComponent={(i) =>
                  patchDraft({ components: draft.components.filter((_, j) => j !== i) })
                }
                onMoveComponent={moveComponent}
              />

              {validation && (
                <div
                  className={cn(
                    "mt-4 rounded-md border p-3 text-sm",
                    validation.valid ? "border-emerald-500/40 bg-emerald-500/10" : "border-destructive/40 bg-destructive/10",
                  )}
                >
                  {validation.valid ? (
                    <p className="font-medium text-emerald-600 dark:text-emerald-400">
                      Structure is valid.
                    </p>
                  ) : (
                    <ul className="text-destructive list-disc pl-5">
                      {validation.errors.map((e, i) => (
                        <li key={i}>{e}</li>
                      ))}
                    </ul>
                  )}
                  {validation.placement_check && (
                    <p className="text-muted-foreground mt-2 text-xs">
                      Placement dry-run flagged: {validation.placement_check}
                    </p>
                  )}
                </div>
              )}

              <div className="mt-5 flex items-center gap-2 border-t pt-4">
                <Button onClick={onSave} disabled={save.isPending}>
                  {save.isPending && <Loader2 className="size-4 animate-spin" />}
                  {draft.id ? "Save changes" : "Create kit"}
                </Button>
                <Button variant="outline" onClick={onValidate} disabled={validate.isPending}>
                  {validate.isPending && <Loader2 className="size-4 animate-spin" />}
                  Validate
                </Button>
                <div className="flex-1" />
                {draft.id && (
                  <Button variant="ghost" className="text-destructive" onClick={onDelete} disabled={del.isPending}>
                    <Trash2 className="size-4" /> Delete
                  </Button>
                )}
              </div>
            </div>
          )}
        </div>
      </div>
    </div>
  )
}

interface ClassOption {
  code: string
  name: string
  geometry_optional: boolean
}

function shortLabel(s: string): string {
  return s.length > 13 ? `${s.slice(0, 12)}…` : s
}

/**
 * A live schematic of the kit: the anchor (position 0) at the origin and each
 * component at its (offset_x, offset_y), joined by its "wire to" (solid) and
 * "support parent" (dashed) references. A pure client-side mental picture drawn
 * straight off the draft as you edit — not the real placed single-line (that's
 * still auto-generated on placement). +Y is up, so an offset_y of -2 sits BELOW
 * the anchor, matching what the field means.
 */
function AssemblyPreview({ draft }: { draft: Draft }) {
  const [view, setView] = useState({ zoom: 1, x: 0, y: 0 })
  const drag = useRef<{ x: number; y: number; px: number; py: number; kx: number; ky: number } | null>(null)

  // Index == position: node 0 is the anchor, node k is component k-1.
  const nodes = [
    { label: draft.anchor_feature_class || "anchor", x: 0, y: 0, isAnchor: true },
    ...draft.components.map((c, i) => ({
      label: c.component_feature_class || `#${i + 1}`,
      x: c.offset_x ?? 0,
      y: c.offset_y ?? 0,
      isAnchor: false,
    })),
  ]

  const xs = nodes.map((n) => n.x)
  const ys = nodes.map((n) => n.y)
  const minX = Math.min(...xs)
  const maxX = Math.max(...xs)
  const minY = Math.min(...ys)
  const maxY = Math.max(...ys)
  const spanX = maxX - minX || 1
  const spanY = maxY - minY || 1

  const W = 320
  const H = 240
  const pad = 46
  const availW = W - 2 * pad
  const availH = H - 2 * pad
  const scale = Math.min(availW / spanX, availH / spanY)
  const ox = pad + (availW - spanX * scale) / 2
  const oy = pad + (availH - spanY * scale) / 2
  const sx = (x: number) => ox + (x - minX) * scale
  const sy = (y: number) => oy + (maxY - y) * scale // flip so +y is up

  const edges: { x1: number; y1: number; x2: number; y2: number; kind: "wire" | "support" }[] = []
  draft.components.forEach((c, i) => {
    const from = i + 1
    const add = (target: number | null, kind: "wire" | "support") => {
      if (target == null || target < 0 || target >= nodes.length || target === from) return
      edges.push({ x1: sx(nodes[from].x), y1: sy(nodes[from].y), x2: sx(nodes[target].x), y2: sy(nodes[target].y), kind })
    }
    add(c.wire_to_position, "wire")
    add(c.support_parent_position, "support")
  })

  // Zoom keeps the anchor point fixed; the buttons anchor on the centre, so the
  // schematic grows/shrinks in place. Drag pans. Reset returns to the auto-fit.
  function zoomAt(factor: number, ax: number, ay: number) {
    setView((v) => {
      const z = Math.max(0.3, Math.min(6, v.zoom * factor))
      const k = z / v.zoom
      return { zoom: z, x: ax - k * (ax - v.x), y: ay - k * (ay - v.y) }
    })
  }
  function onDown(e: RPointerEvent<SVGSVGElement>) {
    const r = e.currentTarget.getBoundingClientRect()
    drag.current = { x: e.clientX, y: e.clientY, px: view.x, py: view.y, kx: W / r.width, ky: H / r.height }
    e.currentTarget.setPointerCapture(e.pointerId)
  }
  function onMove(e: RPointerEvent<SVGSVGElement>) {
    const d = drag.current
    if (!d) return
    setView((v) => ({ ...v, x: d.px + (e.clientX - d.x) * d.kx, y: d.py + (e.clientY - d.y) * d.ky }))
  }
  function onUp(e: RPointerEvent<SVGSVGElement>) {
    drag.current = null
    try {
      e.currentTarget.releasePointerCapture(e.pointerId)
    } catch {
      // the capture may already be released
    }
  }

  return (
    <div className="bg-card rounded-lg border p-3">
      <div className="mb-2 flex items-center justify-between gap-2">
        <h3 className="text-muted-foreground text-xs font-semibold tracking-wide uppercase">Preview</h3>
        <div className="flex items-center gap-3">
          <div className="text-muted-foreground flex items-center gap-3 text-[10px]">
            <span className="flex items-center gap-1">
              <span className="inline-block h-0.5 w-4 bg-blue-500" /> wire
            </span>
            <span className="flex items-center gap-1">
              <span className="border-muted-foreground inline-block w-4 border-t border-dashed" /> support
            </span>
          </div>
          <div className="text-muted-foreground flex items-center gap-0.5">
            <button
              type="button"
              onClick={() => zoomAt(1 / 1.25, W / 2, H / 2)}
              className="hover:bg-accent rounded p-1"
              title="Zoom out"
            >
              <ZoomOut className="size-3.5" />
            </button>
            <button
              type="button"
              onClick={() => zoomAt(1.25, W / 2, H / 2)}
              className="hover:bg-accent rounded p-1"
              title="Zoom in"
            >
              <ZoomIn className="size-3.5" />
            </button>
            <button
              type="button"
              onClick={() => setView({ zoom: 1, x: 0, y: 0 })}
              className="hover:bg-accent rounded p-1"
              title="Reset view"
            >
              <Maximize2 className="size-3.5" />
            </button>
          </div>
        </div>
      </div>
      <svg
        viewBox={`0 0 ${W} ${H}`}
        className="h-56 w-full cursor-grab touch-none select-none active:cursor-grabbing"
        preserveAspectRatio="xMidYMid meet"
        onPointerDown={onDown}
        onPointerMove={onMove}
        onPointerUp={onUp}
        onPointerLeave={onUp}
      >
        <g transform={`translate(${view.x} ${view.y}) scale(${view.zoom})`}>
        {edges.map((e, i) => (
          <line
            key={i}
            x1={e.x1}
            y1={e.y1}
            x2={e.x2}
            y2={e.y2}
            className={e.kind === "wire" ? "stroke-blue-500" : "stroke-muted-foreground"}
            strokeWidth={1.5}
            strokeDasharray={e.kind === "support" ? "4 3" : undefined}
            vectorEffect="non-scaling-stroke"
          />
        ))}
        {nodes.map((n, i) => {
          const cx = sx(n.x)
          const cy = sy(n.y)
          const w = 78
          const h = 26
          return (
            <g key={i}>
              <rect
                x={cx - w / 2}
                y={cy - h / 2}
                width={w}
                height={h}
                rx={5}
                className={n.isAnchor ? "fill-primary/15 stroke-primary" : "fill-muted stroke-border"}
                strokeWidth={1}
                vectorEffect="non-scaling-stroke"
              />
              <text x={cx} y={cy + 3.5} textAnchor="middle" className="fill-foreground text-[9px] font-medium">
                {shortLabel(n.label)}
              </text>
              <circle
                cx={cx - w / 2}
                cy={cy - h / 2}
                r={7}
                className={n.isAnchor ? "fill-primary" : "fill-muted-foreground"}
              />
              <text
                x={cx - w / 2}
                y={cy - h / 2 + 3}
                textAnchor="middle"
                className="fill-background text-[8px] font-bold"
              >
                {i}
              </text>
            </g>
          )
        })}
        </g>
      </svg>
    </div>
  )
}

/** A faithful render of the single-line TEMPLATE's skeleton (slots + annotations
 * at their stored positions). Diagram coords are y-down (unlike the kit-offset
 * schematic), so it's drawn as-is. Lines/buses (path-based) are omitted in this
 * light preview — it shows where the generated single-line puts each slot. */
function TemplateSkeleton({ elements }: { elements: KitDiagramPreviewElement[] }) {
  const pts: { x: number; y: number }[] = []
  elements.forEach((e) => {
    const w = e.width ?? 60
    const h = e.height ?? 24
    pts.push({ x: e.x, y: e.y }, { x: e.x + w, y: e.y + h })
  })
  const xs = pts.map((p) => p.x)
  const ys = pts.map((p) => p.y)
  const minX = Math.min(...xs)
  const maxX = Math.max(...xs)
  const minY = Math.min(...ys)
  const maxY = Math.max(...ys)
  const spanX = maxX - minX || 1
  const spanY = maxY - minY || 1
  const W = 320
  const H = 200
  const pad = 16
  const scale = Math.min((W - 2 * pad) / spanX, (H - 2 * pad) / spanY)
  const ox = pad + (W - 2 * pad - spanX * scale) / 2
  const oy = pad + (H - 2 * pad - spanY * scale) / 2
  const sx = (x: number) => ox + (x - minX) * scale
  const sy = (y: number) => oy + (y - minY) * scale

  return (
    <svg viewBox={`0 0 ${W} ${H}`} className="mt-2 h-48 w-full" preserveAspectRatio="xMidYMid meet">
      {elements.map((e, i) => {
        const x = sx(e.x)
        const y = sy(e.y)
        const w = (e.width ?? 60) * scale
        const h = (e.height ?? 24) * scale
        if (e.kind === "slot") {
          return (
            <g key={i}>
              <rect
                x={x}
                y={y}
                width={w}
                height={h}
                rx={3}
                className="fill-primary/10 stroke-primary"
                strokeWidth={1}
                strokeDasharray="3 2"
                vectorEffect="non-scaling-stroke"
              />
              <text x={x + w / 2} y={y + h / 2 + 3} textAnchor="middle" className="fill-foreground text-[8px]">
                {shortLabel(e.slot_class ?? "slot")}
              </text>
            </g>
          )
        }
        if (e.kind === "text" && e.text) {
          return (
            <text key={i} x={x} y={y} className="fill-muted-foreground text-[8px]">
              {e.text}
            </text>
          )
        }
        if (e.kind === "box") {
          return (
            <rect
              key={i}
              x={x}
              y={y}
              width={w}
              height={h}
              className="fill-none stroke-border"
              strokeWidth={1}
              vectorEffect="non-scaling-stroke"
            />
          )
        }
        return <circle key={i} cx={x} cy={y} r={2.5} className="fill-muted-foreground" />
      })}
    </svg>
  )
}

/** #2-light: which single-line this kit generates, resolved live from (anchor,
 * code) or the explicit template field, with a render of that template's layout.
 * Distinct from the kit-offset schematic above — this is the REAL template the
 * placement auto-diagram uses, not a per-component synthesis. */
function GeneratedSingleLinePreview({ draft }: { draft: Draft }) {
  const preview = useKitDiagramPreview(draft.anchor_feature_class, draft.code, draft.diagram_template_name)
  if (!draft.anchor_feature_class) return null

  const name = preview.data?.template_name ?? ""
  const elements = preview.data?.elements ?? []

  return (
    <div className="bg-card mt-3 rounded-lg border p-3">
      <div className="mb-2 flex items-center justify-between gap-2">
        <h3 className="text-muted-foreground text-xs font-semibold tracking-wide uppercase">Generated single-line</h3>
        {preview.isFetching && <Loader2 className="text-muted-foreground size-3.5 animate-spin" />}
      </div>
      {preview.isError ? (
        <p className="text-destructive text-xs">Couldn't resolve the single-line template.</p>
      ) : !name ? (
        <p className="text-muted-foreground text-xs">
          No single-line is generated for this anchor / code. Set a template above, or this kit places without a
          diagram.
        </p>
      ) : (
        <>
          <p className="text-xs">
            Auto-generates <span className="font-medium">{name}</span>
            {elements.length > 0 && <span className="text-muted-foreground"> · {elements.length} elements</span>}
          </p>
          {elements.length > 0 && <TemplateSkeleton elements={elements} />}
        </>
      )}
    </div>
  )
}

function KitEditor({
  draft,
  classes,
  componentClasses,
  onPatch,
  onPatchComponent,
  onAddComponent,
  onRemoveComponent,
  onMoveComponent,
}: {
  draft: Draft
  classes: ClassOption[]
  componentClasses: ClassOption[]
  onPatch: (p: Partial<Draft>) => void
  onPatchComponent: (i: number, p: Partial<DraftComponent>) => void
  onAddComponent: () => void
  onRemoveComponent: (i: number) => void
  onMoveComponent: (i: number, dir: -1 | 1) => void
}) {
  return (
    <>
      <h1 className="text-lg font-semibold">{draft.id ? "Edit kit" : "New kit"}</h1>

      <div className="mt-4 grid grid-cols-2 gap-3">
        <Field label="Anchor feature class">
          <select
            value={draft.anchor_feature_class}
            onChange={(e) => onPatch({ anchor_feature_class: e.target.value })}
            className={inputClass}
          >
            <option value="">— pick a class —</option>
            {classes.map((c) => (
              <option key={c.code} value={c.code}>
                {c.name} ({c.code})
              </option>
            ))}
          </select>
        </Field>
        <Field label="Anchor subtype code (optional)">
          <input
            type="number"
            value={draft.anchor_subtype_code}
            onChange={(e) => onPatch({ anchor_subtype_code: e.target.value })}
            placeholder="blank = user picks"
            className={inputClass}
          />
        </Field>
        <Field label="Code (selector, e.g. POLE_MOUNT_STD)">
          <input value={draft.code} onChange={(e) => onPatch({ code: e.target.value })} className={inputClass} />
        </Field>
        <Field label="Name (menu label)">
          <input value={draft.name} onChange={(e) => onPatch({ name: e.target.value })} className={inputClass} />
        </Field>
        <Field label="Single-line template (optional — blank auto-generates)" wide>
          <input
            value={draft.diagram_template_name}
            onChange={(e) => onPatch({ diagram_template_name: e.target.value })}
            placeholder="blank = auto-generate a stacked single-line"
            className={inputClass}
          />
        </Field>
      </div>

      <GeneratedSingleLinePreview draft={draft} />

      <div className="mt-6 flex items-center justify-between">
        <h2 className="text-sm font-semibold tracking-wide uppercase">Components (in creation order)</h2>
        <Button size="sm" variant="outline" onClick={onAddComponent}>
          <Plus className="size-4" /> Add component
        </Button>
      </div>
      <p className="text-muted-foreground mt-1 text-xs">
        Position is the row order (1…N); the anchor is position 0. “Wire to” / “Support parent” must
        reference the anchor (0) or an earlier row.
      </p>

      <div className="mt-3">
        <AssemblyPreview draft={draft} />
      </div>

      <div className="mt-3 flex flex-col gap-3">
        {draft.components.map((c, i) => (
          <div key={i} className="bg-muted/30 rounded-lg border p-3">
            <div className="mb-2 flex items-center gap-2">
              <span className="bg-primary/15 text-primary flex size-6 items-center justify-center rounded-full text-xs font-semibold">
                {i + 1}
              </span>
              <span className="flex-1" />
              <button type="button" onClick={() => onMoveComponent(i, -1)} className="hover:bg-accent rounded p-1" title="Move up">
                <ArrowUp className="size-4" />
              </button>
              <button type="button" onClick={() => onMoveComponent(i, 1)} className="hover:bg-accent rounded p-1" title="Move down">
                <ArrowDown className="size-4" />
              </button>
              <button type="button" onClick={() => onRemoveComponent(i)} className="hover:bg-destructive/10 text-destructive rounded p-1" title="Remove">
                <Trash2 className="size-4" />
              </button>
            </div>
            <div className="grid grid-cols-2 gap-2 md:grid-cols-3">
              <Field label="Component class">
                <select
                  value={c.component_feature_class}
                  onChange={(e) => onPatchComponent(i, { component_feature_class: e.target.value })}
                  className={inputClass}
                >
                  <option value="">— pick —</option>
                  {componentClasses.map((cc) => (
                    <option key={cc.code} value={cc.code}>
                      {cc.name} ({cc.code})
                    </option>
                  ))}
                </select>
              </Field>
              <Field label="Subtype code">
                <NumInput value={c.subtype_code} onChange={(v) => onPatchComponent(i, { subtype_code: v })} />
              </Field>
              <Field label="Wire to position (0=anchor)">
                <NumInput value={c.wire_to_position} onChange={(v) => onPatchComponent(i, { wire_to_position: v })} />
              </Field>
              <Field label="Upstream terminal (on target)">
                <TextInput value={c.upstream_terminal} onChange={(v) => onPatchComponent(i, { upstream_terminal: v })} />
              </Field>
              <Field label="Own terminal (on this)">
                <TextInput value={c.own_terminal} onChange={(v) => onPatchComponent(i, { own_terminal: v })} />
              </Field>
              <Field label="Support kind (e.g. spatial_support)">
                <TextInput value={c.support_kind} onChange={(v) => onPatchComponent(i, { support_kind: v })} />
              </Field>
              <Field label="Support parent position">
                <NumInput value={c.support_parent_position} onChange={(v) => onPatchComponent(i, { support_parent_position: v })} />
              </Field>
              <Field label="Offset X">
                <NumInput value={c.offset_x} onChange={(v) => onPatchComponent(i, { offset_x: v ?? 0 })} />
              </Field>
              <Field label="Offset Y">
                <NumInput value={c.offset_y} onChange={(v) => onPatchComponent(i, { offset_y: v ?? 0 })} />
              </Field>
            </div>
          </div>
        ))}
        {draft.components.length === 0 && (
          <p className="text-muted-foreground text-sm">No components yet — add at least one.</p>
        )}
      </div>
    </>
  )
}

function Field({ label, wide, children }: { label: string; wide?: boolean; children: ReactNode }) {
  return (
    <label className={cn("flex flex-col gap-1 text-xs font-medium", wide && "col-span-2")}>
      <span className="text-muted-foreground">{label}</span>
      {children}
    </label>
  )
}

function TextInput({ value, onChange }: { value?: string | null; onChange: (v: string | null) => void }) {
  return (
    <input
      value={value ?? ""}
      onChange={(e) => onChange(e.target.value === "" ? null : e.target.value)}
      className={inputClass}
    />
  )
}

function NumInput({ value, onChange }: { value?: number | null; onChange: (v: number | null) => void }) {
  return (
    <input
      type="number"
      value={value == null ? "" : value}
      onChange={(e) => onChange(e.target.value === "" ? null : Number(e.target.value))}
      className={inputClass}
    />
  )
}
