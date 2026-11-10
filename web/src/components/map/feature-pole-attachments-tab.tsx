import { useEffect, useMemo, useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { Box, Info, Lightbulb, Loader2, Plug, Plus, Share2, Unlink, X } from "lucide-react"

import { inputClass } from "@/components/map/field-utils"
import { RevokeRelationshipDialog } from "@/components/map/revoke-relationship-dialog"
import { useConnections } from "@/hooks/use-connections"
import { useEditing } from "@/hooks/use-editing"
import { useFeature } from "@/hooks/use-feature"
import { useFeatureClasses } from "@/hooks/use-feature-classes"
import { usePoleSpiderStore } from "@/stores/pole-spider-store"
import { type FieldSchema, useFeatureClassSchema } from "@/hooks/use-feature-class-schema"
import { type Relationship, type RelationshipEndpoint, useRelationships } from "@/hooks/use-relationships"
import { useAttachStreetLight } from "@/hooks/use-street-lights"
import { ApiError } from "@/lib/api"
import { cn } from "@/lib/utils"
import { useSelectionStore } from "@/stores/selection-store"
import { useToastStore } from "@/stores/toast-store"

// The map attribute panel's "On this pole" tab, shown on a POLE. A pole is a
// support structure: many things RIDE it, each geometry-less, sharing the pole's
// point -- a lamp, a pole-mounted transformer / recloser / capacitor (placed as
// assemblies), a meter. They're all linked by a `spatial_support` relationship
// (POLE supports X), so this one tab is the pole's equipment register: it lists
// everything on the pole regardless of how it was placed, and lets you open,
// trace or detach each. Street lights are the one kind with an inline attach form
// here (the other kinds are placed via the Assemblies menu, then appear in this
// list). Read-only without edit mode.

const STREET_LIGHT = "STREET_LIGHT"
const SUPPORT_KIND = "spatial_support"

// The required fields a pole-mounted lamp needs at create (the rest are nullable
// load fields a crew fills later). Rendered as domain dropdowns from the live
// STREET_LIGHT schema so the options always match the catalog.
const REQUIRED_FIELDS = ["phase", "luminaire_type", "condition_rating", "supply_mode"] as const

function fieldLabel(name: string): string {
  if (name === "luminaire_type") return "Luminaire type"
  if (name === "condition_rating") return "Condition"
  if (name === "phase") return "Phase"
  if (name === "supply_mode") return "Power source"
  return name
}

function shortId(id: string): string {
  return id.length > 8 ? `${id.slice(0, 8)}…` : id
}

/** The inline "attach a lamp" form (the former Street-lights dialog's body): pick
 * the three required fields and attach. `fields` is passed in so the parent's
 * single schema fetch is reused rather than re-fetched here. */
function AttachLampForm({
  poleClass,
  poleId,
  fields,
  onDone,
}: {
  poleClass: string
  poleId: string
  fields: FieldSchema[]
  onDone: () => void
}) {
  const attach = useAttachStreetLight()
  const queryClient = useQueryClient()
  const [values, setValues] = useState<Record<string, string>>({})
  // supply_mode defaults to GRID when unset, so only a solar lamp is a deliberate pick.
  const valueFor = (f: FieldSchema) => values[f.name] ?? f.default_value ?? (f.name === "supply_mode" ? "GRID" : "")
  const canSave = fields.length > 0 && fields.every((f) => valueFor(f) !== "") && !attach.isPending

  function handleSave() {
    const attributes: Record<string, string> = {}
    for (const f of fields) attributes[f.name] = valueFor(f)
    attach.mutate(
      { pole_feature_class: poleClass, pole_feature_id: poleId, attributes },
      {
        onSuccess: (res) => {
          // Refresh this tab's list (the pole's relationships), the map's
          // attachment-count badge, and -- if wired -- the lamp's connectivity chip.
          queryClient.invalidateQueries({ queryKey: ["relationships", poleClass, poleId] })
          queryClient.invalidateQueries({ queryKey: ["pole-attachment-counts"] })
          if (res.wired) queryClient.invalidateQueries({ queryKey: ["connections"] })
          const toast = useToastStore.getState()
          if (res.wired) toast.pushSuccess("Street light attached and wired to the pole's LV.")
          else toast.pushWarning(res.wire_note || "Street light attached (not wired).")
          onDone()
        },
        onError: (err) =>
          useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not attach the street light."),
      },
    )
  }

  return (
    <div className="flex flex-col gap-2 rounded-md border p-2">
      <div className="flex items-center justify-between text-xs font-medium">
        <span className="flex items-center gap-1.5">
          <Lightbulb className="size-3.5 text-amber-500" />
          New street light
        </span>
        <button type="button" onClick={onDone} className="text-muted-foreground hover:bg-muted/60 rounded p-0.5" title="Cancel">
          <X className="size-3.5" />
        </button>
      </div>
      {fields.map((f) => (
        <div key={f.name} className="flex flex-col gap-1">
          <label className="text-xs font-medium">{fieldLabel(f.name)}</label>
          <select
            className={inputClass}
            value={valueFor(f)}
            onChange={(e) => setValues((v) => ({ ...v, [f.name]: e.target.value }))}
          >
            <option value="">Select…</option>
            {(f.domain_values ?? []).map((code) => (
              <option key={code} value={code}>
                {f.domain_value_labels?.[code] ?? code}
              </option>
            ))}
          </select>
        </div>
      ))}
      <button
        type="button"
        onClick={handleSave}
        disabled={!canSave}
        className="bg-primary text-primary-foreground hover:bg-primary/90 inline-flex items-center justify-center gap-1.5 rounded-md px-3 py-1.5 text-sm font-medium disabled:opacity-50"
      >
        {attach.isPending && <Loader2 className="size-4 animate-spin" />}
        Attach
      </button>
      <p className="text-muted-foreground text-xs">
        Created without its own map geometry — it rides the pole — and auto-wired to the pole's LV when exactly one
        conductor is in range.
      </p>
    </div>
  )
}

/** One attached feature on the pole: its name + class, a live state badge
 * (device_state when the class has one — a pole recloser reads OPEN/CLOSED), a
 * connected/not chip. Clicking the row LOCATES it on the map (an emerald ring
 * marks the located row); the info button OPENS its panel; detach revokes the
 * support link. Same locate-vs-open pattern as the Terminals and Relationships
 * tabs. Generic over the attachment's class. */
function AttachmentRow({
  attachment,
  relationship,
  className,
  isLocated,
  isPoint,
  canEdit,
  onShowOnMap,
  onDetach,
}: {
  attachment: RelationshipEndpoint
  relationship: Relationship
  className: string
  isLocated: boolean
  isPoint: boolean
  canEdit: boolean
  onShowOnMap: (a: RelationshipEndpoint) => void
  onDetach: (r: Relationship) => void
}) {
  const feature = useFeature(attachment.feature_class, attachment.feature_id)
  const connections = useConnections(attachment.feature_class, attachment.feature_id)

  const attrs = feature.data?.attributes as Record<string, unknown> | undefined
  const name = feature.data?.name as string | undefined
  const deviceState = typeof attrs?.device_state === "string" ? (attrs.device_state as string) : null
  const connected = (connections.data?.connections?.length ?? 0) > 0
  const Icon = attachment.feature_class === STREET_LIGHT ? Lightbulb : Box

  return (
    <li
      className={cn(
        "hover:bg-accent/40 flex items-start gap-1 rounded-md border px-1 py-0.5 transition-colors",
        isLocated && "border-emerald-500 ring-1 ring-emerald-500",
      )}
    >
      {/* Clicking the row LOCATES (highlights on the map) the feature without
          opening its panel; the info button opens it. */}
      <button
        type="button"
        className="flex min-w-0 flex-1 flex-col rounded px-1.5 py-1 text-left"
        onClick={() => useSelectionStore.getState().locate(attachment.feature_class, attachment.feature_id)}
        title="Highlight this feature on the map"
      >
        <span className="flex items-center gap-1.5 truncate text-sm">
          <Icon className={cn("size-3.5 shrink-0", attachment.feature_class === STREET_LIGHT ? "text-amber-500" : "text-muted-foreground")} />
          <span className="truncate font-medium">{name || className}</span>
          <span className="text-muted-foreground shrink-0 text-xs">{shortId(attachment.feature_id)}</span>
        </span>
        <span className="text-muted-foreground flex items-center gap-1.5 pl-5 text-xs">
          <span className="truncate">{className}</span>
          {deviceState && (
            <span
              className={cn(
                "shrink-0 rounded px-1 py-0.5 text-[10px] font-medium",
                deviceState === "OPEN"
                  ? "bg-blue-500/10 text-blue-600 dark:text-blue-400"
                  : deviceState === "CLOSED"
                    ? "bg-green-600/10 text-green-700 dark:text-green-400"
                    : "bg-muted text-muted-foreground",
              )}
            >
              {deviceState}
            </span>
          )}
          {connected && (
            <span className="inline-flex shrink-0 items-center gap-0.5 text-green-700 dark:text-green-400">
              <Plug className="size-3" />
              connected
            </span>
          )}
        </span>
      </button>
      {/* Show on map is only meaningful for a POINT attachment: it rides the pole's
          own point (no geometry of its own), so it's revealed by fanning the pole's
          points out around it. A line attachment (a conductor) already draws itself
          on the map, so it gets no button — clicking its row locates it directly. */}
      {isPoint && (
        <button
          type="button"
          onClick={() => onShowOnMap(attachment)}
          className="text-muted-foreground hover:bg-accent hover:text-foreground mt-1 shrink-0 rounded p-1"
          title="Show this feature on the map (fans the pole's attachments out)"
        >
          <Share2 className="size-3.5" />
          <span className="sr-only">Show on map</span>
        </button>
      )}
      <button
        type="button"
        onClick={() => useSelectionStore.getState().select(attachment.feature_class, attachment.feature_id, { focus: true })}
        className="text-muted-foreground hover:bg-accent hover:text-foreground mt-1 shrink-0 rounded p-1"
        title="Open this feature's panel"
      >
        <Info className="size-3.5" />
        <span className="sr-only">Open feature panel</span>
      </button>
      {canEdit && (
        <button
          type="button"
          onClick={() => onDetach(relationship)}
          className="text-muted-foreground hover:bg-destructive/10 hover:text-destructive mt-1 shrink-0 rounded p-1"
          title="Detach this feature from the pole (the feature itself is kept)"
        >
          <Unlink className="size-3.5" />
        </button>
      )}
    </li>
  )
}

export function PoleAttachmentsTab() {
  const selected = useSelectionStore((s) => s.selected)
  const located = useSelectionStore((s) => s.located)
  const canEdit = useEditing()
  const poleClass = selected?.featureClassCode
  const poleId = selected?.featureId
  const { data, isPending, isError } = useRelationships(poleClass, poleId)
  // Clear the map's located highlight when this tab unmounts (switching tab or
  // feature), same as the Terminals / Relationships tabs.
  useEffect(() => () => useSelectionStore.getState().clearLocated(), [])
  const { data: featureClasses } = useFeatureClasses()
  const { data: schema } = useFeatureClassSchema(STREET_LIGHT)
  // The pole's own feature (for its map coordinate, to anchor the fan-out).
  const poleFeature = useFeature(poleClass, poleId)
  const spiderActive = usePoleSpiderStore((s) => s.active)
  const [adding, setAdding] = useState(false)
  const [toDetach, setToDetach] = useState<Relationship | null>(null)

  const nameFor = (code: string) => featureClasses?.feature_classes.find((fc) => fc.code === code)?.name ?? code

  // The lamp-attach form's required field schemas, from the one STREET_LIGHT fetch.
  const fields = useMemo(() => {
    const byName = new Map((schema?.fields ?? []).map((f) => [f.name, f]))
    return REQUIRED_FIELDS.map((name) => byName.get(name)).filter((f): f is FieldSchema => !!f)
  }, [schema])

  // Everything mounted on the pole: every spatial_support relationship, taking the
  // end that isn't the pole itself as the attachment (the pole is the origin in the
  // "POLE supports X" classes, but guard both directions).
  const attachments = useMemo(() => {
    const rels = data?.relationships ?? []
    return rels
      .filter((r) => r.kind === SUPPORT_KIND)
      .map((r) => {
        const other =
          r.origin.feature_class === poleClass && r.origin.feature_id === poleId ? r.destination : r.origin
        return { relationship: r, attachment: other }
      })
      // Defensive: never list the pole itself.
      .filter((x) => !(x.attachment.feature_class === poleClass && x.attachment.feature_id === poleId))
  }, [data, poleClass, poleId])

  // Only POINT attachments can "stack" invisibly on the pole's own point, so only
  // they fan out. A line/polygon attachment (an overhead conductor carried by the
  // pole) has its own visible geometry — fanning it as a chip would misplace it —
  // so it's excluded from the fan (but kept in the list above).
  const pointAttachments = useMemo(
    () =>
      attachments.filter(
        (a) => featureClasses?.feature_classes.find((fc) => fc.code === a.attachment.feature_class)?.geometry_type === "point",
      ),
    [attachments, featureClasses],
  )

  if (!selected || !poleClass || !poleId) return null

  const isFanned = spiderActive?.poleClass === poleClass && spiderActive?.poleId === poleId
  // Point attachments, keyed, so each row knows whether it's a point (and gets a
  // Show-on-map button) or a line (which draws itself — no button).
  const pointKeys = useMemo(
    () => new Set(pointAttachments.map((a) => `${a.attachment.feature_class}:${a.attachment.feature_id}`)),
    [pointAttachments],
  )

  // Open the pole's fan (splaying its geometry-less point attachments around it on
  // the map). Needs the pole's own point; no-op-with-note if it has none. Returns
  // whether the fan is now open, so the caller can then highlight one chip.
  function openFan(): boolean {
    if (isFanned) return true
    const geom = poleFeature.data?.geometry
    const coords = geom?.type === "Point" ? (geom.coordinates as [number, number]) : null
    if (!coords) {
      useToastStore.getState().pushError("This pole has no map location to show attachments on.")
      return false
    }
    usePoleSpiderStore.getState().open({
      poleClass: poleClass as string,
      poleId: poleId as string,
      lon: coords[0],
      lat: coords[1],
      attachments: pointAttachments.map((a) => ({
        feature_class: a.attachment.feature_class,
        feature_id: a.attachment.feature_id,
        label: nameFor(a.attachment.feature_class),
      })),
    })
    return true
  }

  // A row's "Show on map": fan the pole's points out (if not already), then
  // highlight this one.
  function showPointOnMap(a: RelationshipEndpoint) {
    if (openFan()) useSelectionStore.getState().locate(a.feature_class, a.feature_id)
  }

  return (
    <div className="flex flex-col gap-2">
      {canEdit &&
        (adding ? (
          <AttachLampForm poleClass={poleClass} poleId={poleId} fields={fields} onDone={() => setAdding(false)} />
        ) : (
          <button
            type="button"
            onClick={() => setAdding(true)}
            className="border-input text-muted-foreground hover:bg-muted/60 inline-flex items-center justify-center gap-1.5 rounded-md border border-dashed px-3 py-1.5 text-sm"
          >
            <Plus className="size-4" />
            Attach a street light
          </button>
        ))}

      {isPending ? (
        <div className="text-muted-foreground flex items-center gap-2 text-sm">
          <Loader2 className="size-4 animate-spin" /> Loading…
        </div>
      ) : isError ? (
        <p className="text-destructive text-sm">Could not load this pole's attachments.</p>
      ) : attachments.length === 0 ? (
        <p className="text-muted-foreground text-sm">
          Nothing is mounted on this pole yet.{canEdit ? " Attach a street light above, or place a pole-mounted assembly on the map." : ""}
        </p>
      ) : (
        <ul className="flex flex-col gap-1">
          {attachments.map(({ relationship, attachment }) => (
            <AttachmentRow
              key={`${attachment.feature_class}:${attachment.feature_id}`}
              attachment={attachment}
              relationship={relationship}
              className={nameFor(attachment.feature_class)}
              isLocated={
                located?.featureClassCode === attachment.feature_class && located?.featureId === attachment.feature_id
              }
              isPoint={pointKeys.has(`${attachment.feature_class}:${attachment.feature_id}`)}
              canEdit={canEdit}
              onShowOnMap={showPointOnMap}
              onDetach={setToDetach}
            />
          ))}
        </ul>
      )}

      <RevokeRelationshipDialog relationship={toDetach} onOpenChange={(open) => open || setToDetach(null)} />
    </div>
  )
}
