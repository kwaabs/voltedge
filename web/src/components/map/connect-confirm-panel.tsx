import { useEffect, useRef, useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { ArrowRight, Loader2, X } from "lucide-react"

import { Button } from "@/components/ui/button"
import { useFeature } from "@/hooks/use-feature"
import { useFeatureClasses } from "@/hooks/use-feature-classes"
import { terminalsForSubtype, useFeatureClassSchema } from "@/hooks/use-feature-class-schema"
import { ApiError, apiFetch } from "@/lib/api"
import { cn } from "@/lib/utils"
import { useConnectStore } from "@/stores/connect-store"
import { useToastStore } from "@/stores/toast-store"

const selectClass =
  "border-input bg-background h-8 w-full rounded-md border px-2 text-sm outline-none focus-visible:border-ring focus-visible:ring-ring/50 focus-visible:ring-[3px]"

function shortId(id: string): string {
  return id.length > 8 ? `${id.slice(0, 8)}…` : id
}

interface EndpointPickerProps {
  featureClassCode: string
  featureId: string
  name: string
  terminal: string
  onTerminalChange: (terminal: string) => void
}

/** One side of the connection: the feature's real display name/id, and
 * a terminal dropdown scoped to its actual subtype -- only rendered
 * when that subtype has any terminals at all (an edge like
 * MV_CONDUCTOR never will). */
function EndpointPicker({ featureClassCode, featureId, name, terminal, onTerminalChange }: EndpointPickerProps) {
  const { data: schema } = useFeatureClassSchema(featureClassCode)
  const { data: feature } = useFeature(featureClassCode, featureId)
  const terminals = schema ? terminalsForSubtype(schema, feature?.subtype) : []

  return (
    <div className="flex flex-col gap-1">
      <p className="truncate text-sm font-medium">{name}</p>
      <p className="text-muted-foreground truncate font-mono text-xs">{shortId(featureId)}</p>
      {terminals.length > 0 && (
        <select className={selectClass} value={terminal} onChange={(e) => onTerminalChange(e.target.value)}>
          <option value="">No terminal</option>
          {terminals.map((t) => (
            <option key={t.code} value={t.code}>
              {t.code} ({t.role})
            </option>
          ))}
        </select>
      )}
    </div>
  )
}

/**
 * A small floating, movable (drag the header) panel for confirming a
 * connectivity link between two features -- source locked in from
 * whichever feature was selected when "+ New connection" was clicked
 * (feature-panel.tsx), target picked by clicking a second feature on
 * the map (voltedge-map.tsx's click handler, gated on connect-store.ts).
 * Renders as soon as a source is set, with a placeholder in the
 * target's spot until one is picked, so starting the workflow always
 * gives visible feedback (the map cursor also switches to crosshair --
 * see voltedge-map.tsx) rather than silently waiting on a second click.
 * Deliberately not resizable/minimizable -- that's the pinned
 * floating-attribute-panel work, a separate and later concern; this is
 * just draggable so it can be moved out of the way of the two features
 * it's connecting.
 */
export function ConnectConfirmPanel() {
  const mode = useConnectStore((s) => s.mode)
  const source = useConnectStore((s) => s.source)
  const target = useConnectStore((s) => s.target)
  const cancel = useConnectStore((s) => s.cancel)
  const { data: featureClasses } = useFeatureClasses()
  const queryClient = useQueryClient()

  const [sourceTerminal, setSourceTerminal] = useState("")
  const [targetTerminal, setTargetTerminal] = useState("")
  const [saving, setSaving] = useState(false)
  const [position, setPosition] = useState<{ x: number; y: number } | null>(null)
  const panelRef = useRef<HTMLDivElement>(null)

  // A fresh "+ New connection"/"+ New relationship" click starts a new
  // session with a (possibly different) source and no target yet --
  // this component never unmounts (it renders null instead), so without
  // this its terminal picks would silently carry over into the new one.
  useEffect(() => {
    if (!mode) return
    setSourceTerminal("")
    setTargetTerminal("")
    setPosition(null)
  }, [mode, source?.featureClassCode, source?.featureId])

  if (!mode || !source) return null

  const nameFor = (code: string) => featureClasses?.feature_classes.find((fc) => fc.code === code)?.name ?? code

  function handleHeaderPointerDown(e: React.PointerEvent) {
    const rect = panelRef.current?.getBoundingClientRect()
    if (!rect) return
    const startX = e.clientX
    const startY = e.clientY
    const origX = position?.x ?? rect.left
    const origY = position?.y ?? rect.top

    const handleMove = (moveEvt: PointerEvent) => {
      setPosition({ x: origX + (moveEvt.clientX - startX), y: origY + (moveEvt.clientY - startY) })
    }
    const handleUp = () => {
      window.removeEventListener("pointermove", handleMove)
      window.removeEventListener("pointerup", handleUp)
    }
    window.addEventListener("pointermove", handleMove)
    window.addEventListener("pointerup", handleUp)
  }

  async function handleConfirm() {
    if (!source || !target) return
    setSaving(true)
    try {
      await apiFetch("/connectivity", {
        method: "POST",
        body: JSON.stringify({
          from: { feature_class: source.featureClassCode, feature_id: source.featureId, terminal: sourceTerminal || undefined },
          to: { feature_class: target.featureClassCode, feature_id: target.featureId, terminal: targetTerminal || undefined },
        }),
      })
      queryClient.invalidateQueries({ queryKey: ["connections", source.featureClassCode, source.featureId] })
      queryClient.invalidateQueries({ queryKey: ["connections", target.featureClassCode, target.featureId] })
      cancel()
    } catch (err) {
      // Panel stays open (unlike a successful Confirm, which calls
      // cancel() above) so a rejected pairing can be retried with a
      // different terminal or target without losing the workflow's
      // place -- only where the message shows moved to a toast.
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not establish this connection.")
    } finally {
      setSaving(false)
    }
  }

  return (
    <div
      ref={panelRef}
      style={position ? { left: position.x, top: position.y } : undefined}
      className={cn(
        "bg-popover text-popover-foreground fixed z-50 w-72 rounded-md border shadow-lg",
        !position && "top-20 right-4",
      )}
    >
      <div
        onPointerDown={handleHeaderPointerDown}
        className="flex cursor-grab items-center justify-between border-b px-3 py-2 select-none active:cursor-grabbing"
      >
        <p className="text-sm font-medium">{mode === "connectivity" ? "New connection" : "New relationship"}</p>
        <Button variant="ghost" size="icon" className="size-6 shrink-0" onClick={cancel}>
          <X className="size-3.5" />
          <span className="sr-only">Cancel</span>
        </Button>
      </div>

      <div className="flex flex-col gap-3 p-3">
        <EndpointPicker
          featureClassCode={source.featureClassCode}
          featureId={source.featureId}
          name={nameFor(source.featureClassCode)}
          terminal={sourceTerminal}
          onTerminalChange={setSourceTerminal}
        />
        <ArrowRight className="text-muted-foreground size-4 self-center" />
        {target ? (
          <EndpointPicker
            featureClassCode={target.featureClassCode}
            featureId={target.featureId}
            name={nameFor(target.featureClassCode)}
            terminal={targetTerminal}
            onTerminalChange={setTargetTerminal}
          />
        ) : (
          <p className="text-muted-foreground border-input rounded-md border border-dashed px-2 py-3 text-center text-sm">
            Click a second feature on the map to connect to.
          </p>
        )}

        <div className="flex justify-end gap-2 pt-1">
          <Button variant="outline" size="sm" onClick={cancel} disabled={saving}>
            Cancel
          </Button>
          <Button size="sm" onClick={handleConfirm} disabled={saving || !target}>
            {saving && <Loader2 className="size-4 animate-spin" />}
            Confirm
          </Button>
        </div>
      </div>
    </div>
  )
}
