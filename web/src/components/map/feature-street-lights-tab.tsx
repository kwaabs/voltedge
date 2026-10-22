import { useMemo, useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { Lightbulb, Loader2, Plug, Plus, Unlink, X } from "lucide-react"

import { inputClass } from "@/components/map/field-utils"
import { RevokeRelationshipDialog } from "@/components/map/revoke-relationship-dialog"
import { useConnections } from "@/hooks/use-connections"
import { useEditing } from "@/hooks/use-editing"
import { type FieldSchema, useFeatureClassSchema } from "@/hooks/use-feature-class-schema"
import { useFeature } from "@/hooks/use-feature"
import { type Relationship, type RelationshipEndpoint, useRelationships } from "@/hooks/use-relationships"
import { useAttachStreetLight } from "@/hooks/use-street-lights"
import { ApiError } from "@/lib/api"
import { cn } from "@/lib/utils"
import { useSelectionStore } from "@/stores/selection-store"
import { useToastStore } from "@/stores/toast-store"

// The map attribute panel's "Street lights" tab, shown on a POLE. A pole-mounted
// street light is just a luminaire riding the pole (geometry-less); a pole can
// carry several (POLE supports STREET_LIGHT is one_to_many), so this lists the
// lamps already on the pole and lets you attach another inline -- the list view
// the one-shot dialog never was. Attaching calls POST /street-lights
// (api/internal/streetlight): it creates the lamp, links it to the pole, and
// best-effort auto-wires it to the pole's nearby LV. Read-only without edit mode.

// The required fields a pole-mounted lamp needs at create (the rest are nullable
// load fields a crew fills later). Rendered as domain dropdowns from the live
// STREET_LIGHT schema so the options always match the catalog.
const REQUIRED_FIELDS = ["phase", "luminaire_type", "condition_rating"] as const

function fieldLabel(name: string): string {
  if (name === "luminaire_type") return "Luminaire type"
  if (name === "condition_rating") return "Condition"
  if (name === "phase") return "Phase"
  return name
}

function shortId(id: string): string {
  return id.length > 8 ? `${id.slice(0, 8)}…` : id
}

/** The inline "attach a lamp" form (the former dialog's body): pick the three
 * required fields and attach. `fields` is passed in so the parent's single schema
 * fetch is reused rather than re-fetched here. */
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
  const valueFor = (f: FieldSchema) => values[f.name] ?? f.default_value ?? ""
  const canSave = fields.length > 0 && fields.every((f) => valueFor(f) !== "") && !attach.isPending

  function handleSave() {
    const attributes: Record<string, string> = {}
    for (const f of fields) attributes[f.name] = valueFor(f)
    attach.mutate(
      { pole_feature_class: poleClass, pole_feature_id: poleId, attributes },
      {
        onSuccess: (res) => {
          // Refresh this tab's list (the pole's relationships) and, if wired, the
          // lamp's connectivity chip.
          queryClient.invalidateQueries({ queryKey: ["relationships", poleClass, poleId] })
          if (res.wired) queryClient.invalidateQueries({ queryKey: ["connections"] })
          const toast = useToastStore.getState()
          if (res.wired) toast.pushSuccess("Street light attached and wired to the pole's LV.")
          else toast.pushWarning(res.wire_note || "Street light attached (not wired).")
          onDone()
        },
        onError: (err) =>
          useToastStore
            .getState()
            .pushError(err instanceof ApiError ? err.message : "Could not attach the street light."),
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
        <button
          type="button"
          onClick={onDone}
          className="text-muted-foreground hover:bg-muted/60 rounded p-0.5"
          title="Cancel"
        >
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
        Created without its own map geometry — it rides the pole — and auto-wired to the pole's LV when
        exactly one conductor is in range.
      </p>
    </div>
  )
}

/** One attached lamp: its luminaire type/phase/condition, a wired/not-wired chip
 * (from its connectivity), click to open, and detach (revoke the link). */
function LampRow({
  lamp,
  relationship,
  labelFor,
  canEdit,
  onDetach,
}: {
  lamp: RelationshipEndpoint
  relationship: Relationship
  labelFor: (name: string, code: unknown) => string
  canEdit: boolean
  onDetach: (r: Relationship) => void
}) {
  const select = useSelectionStore((s) => s.select)
  const locate = useSelectionStore((s) => s.locate)
  const clearLocated = useSelectionStore((s) => s.clearLocated)
  const feature = useFeature(lamp.feature_class, lamp.feature_id)
  const connections = useConnections(lamp.feature_class, lamp.feature_id)

  const attrs = feature.data?.attributes
  const wired = (connections.data?.connections?.length ?? 0) > 0
  const name = feature.data?.name
  const secondary = attrs
    ? [labelFor("luminaire_type", attrs.luminaire_type), labelFor("phase", attrs.phase), labelFor("condition_rating", attrs.condition_rating)]
        .filter(Boolean)
        .join(" · ")
    : ""

  return (
    <li className="flex items-start gap-1">
      <button
        type="button"
        className="hover:bg-muted/60 flex min-w-0 flex-1 flex-col rounded px-2 py-1.5 text-left"
        onClick={() => select(lamp.feature_class, lamp.feature_id)}
        onMouseEnter={() => locate(lamp.feature_class, lamp.feature_id)}
        onMouseLeave={clearLocated}
        title="Open this street light"
      >
        <span className="flex items-center gap-1.5 truncate text-sm">
          <Lightbulb className="size-3.5 shrink-0 text-amber-500" />
          <span className="truncate font-medium">{name || "Street light"}</span>
          <span className="text-muted-foreground shrink-0 text-xs">{shortId(lamp.feature_id)}</span>
        </span>
        <span className="text-muted-foreground flex items-center gap-1.5 pl-5 text-xs">
          {secondary && <span className="truncate">{secondary}</span>}
          <span
            className={cn(
              "inline-flex shrink-0 items-center gap-0.5",
              wired ? "text-green-700 dark:text-green-400" : "text-amber-700 dark:text-amber-400",
            )}
          >
            <Plug className="size-3" />
            {wired ? "wired" : "not wired"}
          </span>
        </span>
      </button>
      {canEdit && (
        <button
          type="button"
          onClick={() => onDetach(relationship)}
          className="text-muted-foreground hover:bg-muted/60 hover:text-destructive shrink-0 rounded p-1.5"
          title="Detach this lamp from the pole (the lamp itself is kept)"
        >
          <Unlink className="size-3.5" />
        </button>
      )}
    </li>
  )
}

export function StreetLightsTab() {
  const selected = useSelectionStore((s) => s.selected)
  const canEdit = useEditing()
  const poleClass = selected?.featureClassCode
  const poleId = selected?.featureId
  const { data, isPending, isError } = useRelationships(poleClass, poleId)
  const { data: schema } = useFeatureClassSchema("STREET_LIGHT")
  const [adding, setAdding] = useState(false)
  const [toDetach, setToDetach] = useState<Relationship | null>(null)

  // The required field schemas (for the attach form) and a code→label resolver
  // (for the list rows), both from the one STREET_LIGHT schema fetch.
  const fields = useMemo(() => {
    const byName = new Map((schema?.fields ?? []).map((f) => [f.name, f]))
    return REQUIRED_FIELDS.map((name) => byName.get(name)).filter((f): f is FieldSchema => !!f)
  }, [schema])
  const labelFor = useMemo(() => {
    const byName = new Map((schema?.fields ?? []).map((f) => [f.name, f]))
    return (name: string, code: unknown): string => {
      if (code == null || code === "") return ""
      const f = byName.get(name)
      return f?.domain_value_labels?.[String(code)] ?? String(code)
    }
  }, [schema])

  // The pole's attached lamps: any relationship whose other end is a STREET_LIGHT.
  const lamps = useMemo(() => {
    const rels = data?.relationships ?? []
    return rels
      .map((r) => {
        const lamp =
          r.destination.feature_class === "STREET_LIGHT"
            ? r.destination
            : r.origin.feature_class === "STREET_LIGHT"
              ? r.origin
              : null
        return lamp ? { relationship: r, lamp } : null
      })
      .filter((x): x is { relationship: Relationship; lamp: RelationshipEndpoint } => x !== null)
  }, [data])

  if (!selected || !poleClass || !poleId) return null

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
        <p className="text-destructive text-sm">Could not load this pole's street lights.</p>
      ) : lamps.length === 0 ? (
        <p className="text-muted-foreground text-sm">
          No street lights on this pole yet.{canEdit ? " Attach one above." : ""}
        </p>
      ) : (
        <ul className="divide-y rounded-md border">
          {lamps.map(({ relationship, lamp }) => (
            <LampRow
              key={lamp.feature_id}
              lamp={lamp}
              relationship={relationship}
              labelFor={labelFor}
              canEdit={canEdit}
              onDetach={setToDetach}
            />
          ))}
        </ul>
      )}

      <RevokeRelationshipDialog relationship={toDetach} onOpenChange={(open) => open || setToDetach(null)} />
    </div>
  )
}
