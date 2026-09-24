import { useEffect, useRef, useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { AlertTriangle, ArrowLeftRight, ArrowRight, CircleCheck, Loader2, X } from "lucide-react"

import { Button } from "@/components/ui/button"
import { useFeature } from "@/hooks/use-feature"
import { useFeatureClassRules } from "@/hooks/use-feature-class-rules"
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
  // The relationship_classes that could apply to this establish are the
  // source class's rules whose ordered pairing matches the picked target
  // (LoadRelationshipClasses matches origin AND destination, so the
  // establish is directional: origin=source, destination=target). We fetch
  // the source's rules and filter to the target below, turning the old
  // free-text Kind box into a picker of exactly the valid kinds. Enabled
  // only in relationship mode -- connectivity has no kind concept.
  const { data: sourceRules, isLoading: rulesLoading } = useFeatureClassRules(
    mode === "relationship" ? source?.featureClassCode : undefined,
  )
  const queryClient = useQueryClient()

  const [sourceTerminal, setSourceTerminal] = useState("")
  const [targetTerminal, setTargetTerminal] = useState("")
  const [kind, setKind] = useState("")
  const [saving, setSaving] = useState(false)
  // A just-established relationship that came back geometrically implausible
  // (checkPlausibility, api/internal/relationship/plausibility.go). The
  // warning is deliberately NOT persisted server-side (types.go) and never
  // reappears on read-back, so instead of flashing a toast that's gone before
  // it can be acted on, we hold it here and keep this card open with Undo/Keep.
  const [warned, setWarned] = useState<{ id: string; message: string } | null>(null)
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
    setWarned(null)
    setPosition(null)
  }, [mode, source?.featureClassCode, source?.featureId])

  // A loop check result only applies to the exact source/target pairing
  // it ran against -- picking a different target (or restarting the
  // whole workflow, already covered by the effect above) invalidates it.
  useEffect(() => {
    setLoopCheck({ checking: false, pathFound: null })
    // The valid Kind set is target-specific -- a kind chosen for one target
    // must not carry into a different one (whose kinds may differ or be a
    // single auto-picked one), or Confirm could send a now-invalid kind.
    setKind("")
  }, [target?.featureClassCode, target?.featureId])

  if (!mode || !source) return null

  const nameFor = (code: string) => featureClasses?.feature_classes.find((fc) => fc.code === code)?.name ?? code

  // sourceRules carries every relationship rule where the source class
  // appears on EITHER side, so from it alone we can read both directions of
  // the picked pairing. Kinds are deduped (the same kind can appear under
  // differently-named relationship_classes; matchClass keys on kind alone).
  const dedupeKinds = (pred: (r: { origin_class: string; destination_class: string }) => boolean) =>
    mode === "relationship" && target && sourceRules
      ? Array.from(new Set(sourceRules.relationship_rules.filter(pred).map((r) => r.kind)))
      : []
  // Forward = source -> target, exactly what the picker order asks for.
  const forwardKinds = dedupeKinds(
    (r) => r.origin_class === source.featureClassCode && target != null && r.destination_class === target.featureClassCode,
  )
  // Reverse = target -> source, only considered when there's no forward class
  // -- relationship_classes are one-way, but the pole<->transformer style
  // pairing "means the same thing" either way, so if only the reverse
  // direction is defined we establish it swapped rather than dead-ending.
  const reverseKinds =
    forwardKinds.length === 0
      ? dedupeKinds(
          (r) =>
            target != null && r.origin_class === target.featureClassCode && r.destination_class === source.featureClassCode,
        )
      : []
  // When we fall back to the reverse direction the POST must swap
  // origin/destination so the stored relationship stays semantically correct
  // (and its cardinality's "one" end stays on the right class).
  const relationshipReversed = reverseKinds.length > 0
  const applicableKinds = relationshipReversed ? reverseKinds : forwardKinds
  // Only reachable once the rules have loaded (sourceRules set): no
  // relationship_class permits this pairing in EITHER direction, so the
  // establish would 400 -- flag it up front instead.
  const noRelationshipClass =
    mode === "relationship" &&
    target != null &&
    !rulesLoading &&
    sourceRules != null &&
    forwardKinds.length === 0 &&
    reverseKinds.length === 0
  // >1 kind is the genuinely-ambiguous case matchClass needs disambiguated;
  // exactly 1 is auto-picked server-side with kind omitted, so no choice.
  const needsKindChoice = applicableKinds.length > 1
  const relationshipBlocked = mode === "relationship" && (noRelationshipClass || (needsKindChoice && kind === ""))

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
        // (api/internal/relationship/rules.go's matchClass). The Kind picker
        // above already constrains this to a valid kind (or omits it when
        // exactly one applies), so `kind || undefined` is either that chosen
        // kind or blank for the backend to auto-resolve. The free-text
        // fallback (rules fetch failed) can still 400, surfaced via toast.
        // relationshipReversed: only the target -> source direction has a
        // relationship_class, so swap the ends (see the forward/reverse kinds
        // computation) -- the pairing means the same thing either way, but the
        // stored row must carry the defined direction.
        const originEnd = relationshipReversed ? target : source
        const destEnd = relationshipReversed ? source : target
        const result = await apiFetch<{ id: string; plausibility_warning?: string }>("/relationships", {
          method: "POST",
          body: JSON.stringify({
            origin: { feature_class: originEnd.featureClassCode, feature_id: originEnd.featureId },
            destination: { feature_class: destEnd.featureClassCode, feature_id: destEnd.featureId },
            kind: kind || undefined,
          }),
        })
        queryClient.invalidateQueries({ queryKey: ["relationships", source.featureClassCode, source.featureId] })
        queryClient.invalidateQueries({ queryKey: ["relationships", target.featureClassCode, target.featureId] })
        // A cable_routing establish consumes a duct way, so the spare-ways
        // count (useDuctCapacity, Phase 44 Slice 3) needs to refetch. That
        // hook keys off ["duct-capacity", ...] independently of the
        // relationships list, so invalidate it for whichever side is the duct
        // (either could be, depending on which end started the relationship).
        queryClient.invalidateQueries({ queryKey: ["duct-capacity", source.featureClassCode, source.featureId] })
        queryClient.invalidateQueries({ queryKey: ["duct-capacity", target.featureClassCode, target.featureId] })
        // A soft geometric sanity check (plausibility.go) -- never blocks the
        // establish, which has already succeeded. But rather than a toast that
        // vanishes, hold it in the card: keep this panel open showing the
        // warning inline with Undo/Keep, so "double check this is the right
        // pairing" is actually actionable. No warning => clean close as before.
        if (result.plausibility_warning) {
          setWarned({ id: result.id, message: result.plausibility_warning })
          return
        }
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

  // "Undo" on the plausibility warning: the relationship was really created,
  // so backing out is a revoke (DELETE /relationships/{id}), same call and
  // broad invalidation as revoke-relationship-dialog.tsx.
  async function handleUndo() {
    if (!warned) return
    setSaving(true)
    try {
      await apiFetch(`/relationships/${warned.id}`, { method: "DELETE" })
      queryClient.invalidateQueries({ queryKey: ["relationships"] })
      setWarned(null)
      cancel()
    } catch (err) {
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not undo this relationship.")
    } finally {
      setSaving(false)
    }
  }

  return (
    <div
      ref={panelRef}
      style={position ? { left: position.x, top: position.y } : undefined}
      className={cn(
        "bg-popover text-popover-foreground fixed z-50 w-72 overflow-hidden rounded-lg border border-border/70 border-t-2 border-t-[oklch(0.45_0.10_252)] dark:border-t-primary shadow-xl ring-1 ring-black/5",
        !position && "top-20 right-4",
      )}
    >
      <div
        onPointerDown={handleHeaderPointerDown}
        className="border-[oklch(0.45_0.10_252)]/20 dark:border-primary/15 flex cursor-grab items-center justify-between border-b bg-gradient-to-b from-[oklch(0.45_0.10_252)]/15 to-[oklch(0.45_0.10_252)]/5 dark:from-accent/50 dark:to-accent/20 px-3 py-2 select-none active:cursor-grabbing"
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
            Click a second feature on the map or diagram to {mode === "relationship" ? "relate to" : "connect to"}.
          </p>
        )}

        {!warned && relationshipReversed && target && (
          // Relationship_classes are one-way; only target -> source is defined,
          // so we quietly store it that way -- same real-world link, correct
          // direction. Shown so the swap isn't a surprise.
          <p className="text-muted-foreground flex items-start gap-1.5 rounded-md border border-dashed border-input px-2 py-2 text-xs">
            <ArrowLeftRight className="size-3.5 shrink-0 translate-y-0.5" />
            <span>
              Stored as {nameFor(target.featureClassCode)} → {nameFor(source.featureClassCode)} (relationships are
              one-way; this pairing is only defined in that direction).
            </span>
          </p>
        )}

        {!warned && mode === "relationship" && target && (
          <div className="flex flex-col gap-1">
            <label className="text-xs font-medium">Kind</label>
            {rulesLoading ? (
              <p className="text-muted-foreground flex items-center gap-1.5 text-xs">
                <Loader2 className="size-3.5 animate-spin" /> Checking valid relationship kinds…
              </p>
            ) : !sourceRules ? (
              // Rules failed to load -- fall back to the old free-text box so
              // a transient fetch error can't block establishing; the backend
              // still validates whatever kind is typed.
              <input
                type="text"
                className={selectClass}
                value={kind}
                onChange={(e) => setKind(e.target.value)}
                placeholder="Only needed if the pairing is ambiguous"
              />
            ) : noRelationshipClass ? (
              <p className="text-destructive flex items-start gap-1.5 text-xs">
                <AlertTriangle className="size-3.5 shrink-0 translate-y-0.5" />
                No relationship class links {nameFor(source.featureClassCode)} and{" "}
                {nameFor(target.featureClassCode)} (in either direction).
              </p>
            ) : needsKindChoice ? (
              <select className={selectClass} value={kind} onChange={(e) => setKind(e.target.value)}>
                <option value="">Select kind…</option>
                {applicableKinds.map((k) => (
                  <option key={k} value={k}>
                    {k}
                  </option>
                ))}
              </select>
            ) : (
              // Exactly one kind applies -- nothing to choose; shown read-only
              // so it's clear what will be established (sent with kind omitted).
              <p className="text-muted-foreground text-xs">
                {applicableKinds[0]} <span className="opacity-70">(only kind for this pairing)</span>
              </p>
            )}
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

        {warned && (
          <div className="flex flex-col gap-1.5 rounded-md border border-amber-500/40 bg-amber-50 px-2.5 py-2 dark:bg-amber-950/30">
            <p className="flex items-center gap-1.5 text-xs font-medium text-amber-700 dark:text-amber-400">
              <AlertTriangle className="size-3.5 shrink-0" />
              Relationship created — worth a second look
            </p>
            <p className="text-xs text-amber-800 dark:text-amber-300">{warned.message}</p>
          </div>
        )}

        <div className="flex justify-end gap-2 pt-1">
          {warned ? (
            <>
              <Button
                variant="outline"
                size="sm"
                onClick={handleUndo}
                disabled={saving}
                className="text-destructive hover:text-destructive"
              >
                {saving && <Loader2 className="size-4 animate-spin" />}
                Undo
              </Button>
              <Button size="sm" onClick={cancel} disabled={saving}>
                Keep
              </Button>
            </>
          ) : (
            <>
              <Button variant="outline" size="sm" onClick={cancel} disabled={saving}>
                Cancel
              </Button>
              <Button size="sm" onClick={handleConfirm} disabled={saving || !target || relationshipBlocked}>
                {saving && <Loader2 className="size-4 animate-spin" />}
                Confirm
              </Button>
            </>
          )}
        </div>
      </div>
    </div>
  )
}
