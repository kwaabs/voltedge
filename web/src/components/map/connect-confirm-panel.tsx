import { useEffect, useRef, useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { AlertTriangle, ArrowRight, CircleCheck, Loader2, X } from "lucide-react"

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
  className: string
  terminal: string
  onTerminalChange: (terminal: string) => void
  showTerminal: boolean
}

/** One side of the connection: the feature's real display name/id, and
 * -- for a connectivity workflow only -- a terminal dropdown scoped to
 * its actual subtype, rendered only when that subtype has any terminals
 * at all (an edge like MV_CONDUCTOR never will). A relationship has no
 * terminal concept (`relationship_classes` doesn't reference terminals
 * the way `network_rules` does), so `showTerminal` is false for that
 * workflow rather than this component trying to infer it from whether
 * terminals happen to exist.
 *
 * Same primary/secondary label promotion as feature-panel.tsx's header:
 * a per-feature name, once set, is more human-identifiable than the
 * class name and id, so it takes the primary line when present. */
function EndpointPicker({ featureClassCode, featureId, className, terminal, onTerminalChange, showTerminal }: EndpointPickerProps) {
  const { data: schema } = useFeatureClassSchema(featureClassCode)
  const { data: feature } = useFeature(featureClassCode, featureId)
  const terminals = showTerminal && schema ? terminalsForSubtype(schema, feature?.subtype) : []
  const primaryLabel = feature?.name || className
  const secondaryLabel = feature?.name ? `${className} · ${shortId(featureId)}` : shortId(featureId)

  return (
    <div className="flex flex-col gap-1">
      <p className="truncate text-sm font-medium">{primaryLabel}</p>
      <p className="text-muted-foreground truncate font-mono text-xs">{secondaryLabel}</p>
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
 * A small floating, movable (drag the header) panel for confirming
 * either an electrical connectivity link or a structural relationship
 * between two features -- same panel, same drag/target-picking
 * mechanics, branching on connect-store.ts's `mode` for the one part
 * that actually differs (terminals + POST /connectivity vs. an
 * optional kind + POST /relationships, see handleConfirm below).
 * Source locked in from whichever feature was selected when "+ New
 * connection"/"+ New relationship" was clicked (feature-panel.tsx),
 * target picked by clicking a second feature on the map
 * (voltedge-map.tsx's click handler, gated on connect-store.ts).
 * Renders as soon as a source is set, with a placeholder in the
 * target's spot until one is picked, so starting the workflow always
 * gives visible feedback (the map cursor also switches to crosshair --
 * see voltedge-map.tsx) rather than silently waiting on a second click.
 * Deliberately not resizable/minimizable -- that's the pinned
 * floating-attribute-panel work, a separate and later concern; this is
 * just draggable so it can be moved out of the way of the two features
 * it's linking.
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
  const [kind, setKind] = useState("")
  const [saving, setSaving] = useState(false)
  const [position, setPosition] = useState<{ x: number; y: number } | null>(null)
  const [loopCheck, setLoopCheck] = useState<{ checking: boolean; pathFound: boolean | null }>({
    checking: false,
    pathFound: null,
  })
  const panelRef = useRef<HTMLDivElement>(null)

  // A fresh "+ New connection"/"+ New relationship" click starts a new
  // session with a (possibly different) source and no target yet --
  // this component never unmounts (it renders null instead), so without
  // this its terminal picks (or, for a relationship, its kind) would
  // silently carry over into the new one.
  useEffect(() => {
    if (!mode) return
    setSourceTerminal("")
    setTargetTerminal("")
    setKind("")
    setPosition(null)
  }, [mode, source?.featureClassCode, source?.featureId])

  // A loop check result only applies to the exact source/target pairing
  // it ran against -- picking a different target (or restarting the
  // whole workflow, already covered by the effect above) invalidates it.
  useEffect(() => {
    setLoopCheck({ checking: false, pathFound: null })
  }, [target?.featureClassCode, target?.featureId])

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

  // Whether closing/establishing this connectivity link would create a
  // loop -- a live path (LOOP_CHECK_TRACE, unlike SHORTEST_PATH_TRACE,
  // respects real switch state: a topological path through a
  // currently-open device wouldn't actually loop anything right now,
  // see seed/loop_check_example.sql) already connecting source and
  // target means this new link would be a second, parallel path.
  async function checkLoop() {
    if (!source || !target) return
    setLoopCheck({ checking: true, pathFound: null })
    try {
      const result = await apiFetch<{ path_found?: boolean }>("/trace", {
        method: "POST",
        body: JSON.stringify({
          trace_configuration: "LOOP_CHECK_TRACE",
          start: { feature_class: source.featureClassCode, feature_id: source.featureId },
          target: { feature_class: target.featureClassCode, feature_id: target.featureId },
        }),
      })
      setLoopCheck({ checking: false, pathFound: result.path_found ?? false })
    } catch (err) {
      setLoopCheck({ checking: false, pathFound: null })
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not check for a loop.")
    }
  }

  async function handleConfirm() {
    if (!source || !target) return
    setSaving(true)
    try {
      if (mode === "connectivity") {
        await apiFetch("/connectivity", {
          method: "POST",
          body: JSON.stringify({
            from: { feature_class: source.featureClassCode, feature_id: source.featureId, terminal: sourceTerminal || undefined },
            to: { feature_class: target.featureClassCode, feature_id: target.featureId, terminal: targetTerminal || undefined },
          }),
        })
        queryClient.invalidateQueries({ queryKey: ["connections", source.featureClassCode, source.featureId] })
        queryClient.invalidateQueries({ queryKey: ["connections", target.featureClassCode, target.featureId] })
      } else {
        // relationship_classes has no terminal concept -- just origin,
        // destination, and an optional kind, only needed when more than
        // one relationship_classes row matches the same pairing
        // (api/internal/relationship/rules.go's matchClass). Left blank,
        // an ambiguous pairing comes back as a real 400 naming the valid
        // kinds -- surfaced through the same toast as any other
        // rejection, no separate UI for listing them out.
        await apiFetch("/relationships", {
          method: "POST",
          body: JSON.stringify({
            origin: { feature_class: source.featureClassCode, feature_id: source.featureId },
            destination: { feature_class: target.featureClassCode, feature_id: target.featureId },
            kind: kind || undefined,
          }),
        })
        queryClient.invalidateQueries({ queryKey: ["relationships", source.featureClassCode, source.featureId] })
        queryClient.invalidateQueries({ queryKey: ["relationships", target.featureClassCode, target.featureId] })
      }
      cancel()
    } catch (err) {
      // Panel stays open (unlike a successful Confirm, which calls
      // cancel() above) so a rejected pairing can be retried with a
      // different terminal/kind or target without losing the workflow's
      // place -- only where the message shows moved to a toast.
      const fallback = mode === "connectivity" ? "Could not establish this connection." : "Could not establish this relationship."
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : fallback)
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
          className={nameFor(source.featureClassCode)}
          terminal={sourceTerminal}
          onTerminalChange={setSourceTerminal}
          showTerminal={mode === "connectivity"}
        />
        <ArrowRight className="text-muted-foreground size-4 self-center" />
        {target ? (
          <EndpointPicker
            featureClassCode={target.featureClassCode}
            featureId={target.featureId}
            className={nameFor(target.featureClassCode)}
            terminal={targetTerminal}
            onTerminalChange={setTargetTerminal}
            showTerminal={mode === "connectivity"}
          />
        ) : (
          <p className="text-muted-foreground border-input rounded-md border border-dashed px-2 py-3 text-center text-sm">
            Click a second feature on the map to connect to.
          </p>
        )}

        {mode === "relationship" && (
          <div className="flex flex-col gap-1">
            <label className="text-xs font-medium">Kind</label>
            <input
              type="text"
              className={selectClass}
              value={kind}
              onChange={(e) => setKind(e.target.value)}
              placeholder="Only needed if the pairing is ambiguous"
            />
          </div>
        )}

        {mode === "connectivity" && target && (
          <div className="flex flex-col gap-1.5">
            <Button variant="outline" size="sm" onClick={checkLoop} disabled={loopCheck.checking}>
              {loopCheck.checking && <Loader2 className="size-4 animate-spin" />}
              Check for loop
            </Button>
            {loopCheck.pathFound === true && (
              <p className="text-destructive flex items-start gap-1.5 text-xs">
                <AlertTriangle className="size-3.5 shrink-0 translate-y-0.5" />
                A live path already connects these -- this would create a loop.
              </p>
            )}
            {loopCheck.pathFound === false && (
              <p className="flex items-start gap-1.5 text-xs text-emerald-600 dark:text-emerald-400">
                <CircleCheck className="size-3.5 shrink-0 translate-y-0.5" />
                No existing live path between these.
              </p>
            )}
          </div>
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
