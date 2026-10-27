import { useMemo, useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { Gauge, Loader2, Plug, Plus, Unlink, X } from "lucide-react"

import { inputClass } from "@/components/map/field-utils"
import { RevokeRelationshipDialog } from "@/components/map/revoke-relationship-dialog"
import { useConnections } from "@/hooks/use-connections"
import { useEditing } from "@/hooks/use-editing"
import { type FieldSchema, useFeatureClassSchema } from "@/hooks/use-feature-class-schema"
import { useFeature } from "@/hooks/use-feature"
import { useAttachMeter } from "@/hooks/use-meter-mounts"
import { type Relationship, type RelationshipEndpoint, useRelationships } from "@/hooks/use-relationships"
import { ApiError } from "@/lib/api"
import { cn } from "@/lib/utils"
import { useSelectionStore } from "@/stores/selection-store"
import { useToastStore } from "@/stores/toast-store"

// The map attribute panel's "Meters" tab, shown on a SERVICE_POINT. A meter
// installed at a delivery point can ride the service point (geometry-less); a SP
// can host more than one (its primary billing meter + a check meter -- SERVICE_POINT
// supports METER is one_to_many), so this lists the meters already on the SP and
// lets you attach another inline. Attaching calls POST /meter-mounts
// (api/internal/metermount): it creates the geometry-less meter, links it to the
// SP, and wires it (METER T2 -> SERVICE_POINT T1). Read-only without edit mode.
//
// A meter wired to its SP is a DEVICE OF it -- the SP stays the counted supply --
// so its role (PRIMARY/CHECK) is a device record, not a second load leaf
// (trace/service.go skips an SP-attached meter in the rollup).

// The fields the attach form collects: meter_serial + phase are the meter's two
// required (NOT NULL) fields; meter_role (PRIMARY/CHECK) is the optional device
// role. account_ref and the load fields a crew fills later.
const FORM_FIELDS: { name: string; label: string; required: boolean }[] = [
  { name: "meter_serial", label: "Meter serial", required: true },
  { name: "phase", label: "Phase", required: true },
  { name: "meter_role", label: "Role", required: false },
]

function shortId(id: string): string {
  return id.length > 8 ? `${id.slice(0, 8)}…` : id
}

/** The inline "attach a meter" form. `fields` are the live METER field schemas
 * (reused from the parent's one schema fetch) so a domain field renders as a
 * dropdown with the catalogue options and a plain field as a text input. */
function AttachMeterForm({
  spClass,
  spId,
  fields,
  onDone,
}: {
  spClass: string
  spId: string
  fields: Map<string, FieldSchema>
  onDone: () => void
}) {
  const attach = useAttachMeter()
  const queryClient = useQueryClient()
  const [values, setValues] = useState<Record<string, string>>({})
  const valueFor = (name: string) => values[name] ?? fields.get(name)?.default_value ?? ""
  const canSave = FORM_FIELDS.every((f) => !f.required || valueFor(f.name).trim() !== "") && !attach.isPending

  function handleSave() {
    const attributes: Record<string, string> = {}
    for (const f of FORM_FIELDS) {
      const v = valueFor(f.name).trim()
      if (v !== "") attributes[f.name] = v
    }
    attach.mutate(
      { service_point_feature_class: spClass, service_point_feature_id: spId, attributes },
      {
        onSuccess: (res) => {
          queryClient.invalidateQueries({ queryKey: ["relationships", spClass, spId] })
          if (res.wired) queryClient.invalidateQueries({ queryKey: ["connections"] })
          const toast = useToastStore.getState()
          if (res.wired) toast.pushSuccess("Meter attached and wired to the service point.")
          else toast.pushWarning(res.wire_note || "Meter attached (not wired).")
          onDone()
        },
        onError: (err) =>
          useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not attach the meter."),
      },
    )
  }

  return (
    <div className="flex flex-col gap-2 rounded-md border p-2">
      <div className="flex items-center justify-between text-xs font-medium">
        <span className="flex items-center gap-1.5">
          <Gauge className="text-primary size-3.5" />
          New meter
        </span>
        <button type="button" onClick={onDone} className="text-muted-foreground hover:bg-muted/60 rounded p-0.5" title="Cancel">
          <X className="size-3.5" />
        </button>
      </div>
      {FORM_FIELDS.map((f) => {
        const schema = fields.get(f.name)
        const domain = schema?.domain_values
        return (
          <div key={f.name} className="flex flex-col gap-1">
            <label className="text-xs font-medium">
              {f.label}
              {!f.required && <span className="text-muted-foreground"> (optional)</span>}
            </label>
            {domain && domain.length > 0 ? (
              <select
                className={inputClass}
                value={valueFor(f.name)}
                onChange={(e) => setValues((v) => ({ ...v, [f.name]: e.target.value }))}
              >
                <option value="">{f.required ? "Select…" : "— none —"}</option>
                {domain.map((code) => (
                  <option key={code} value={code}>
                    {schema?.domain_value_labels?.[code] ?? code}
                  </option>
                ))}
              </select>
            ) : (
              <input
                className={inputClass}
                value={valueFor(f.name)}
                onChange={(e) => setValues((v) => ({ ...v, [f.name]: e.target.value }))}
                placeholder={f.name === "meter_serial" ? "device serial number" : ""}
              />
            )}
          </div>
        )
      })}
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
        Created without its own map geometry — it rides the service point — and wired to it. A meter on an SP is a
        device of it: the service point stays the counted supply, metered or not.
      </p>
    </div>
  )
}

/** One attached meter: its serial/role, a wired/not-wired chip, click to open,
 * and detach (revoke the link; the meter itself is kept). */
function MeterRow({
  meter,
  relationship,
  labelFor,
  canEdit,
  onDetach,
}: {
  meter: RelationshipEndpoint
  relationship: Relationship
  labelFor: (name: string, code: unknown) => string
  canEdit: boolean
  onDetach: (r: Relationship) => void
}) {
  const select = useSelectionStore((s) => s.select)
  const locate = useSelectionStore((s) => s.locate)
  const clearLocated = useSelectionStore((s) => s.clearLocated)
  const feature = useFeature(meter.feature_class, meter.feature_id)
  const connections = useConnections(meter.feature_class, meter.feature_id)

  const attrs = feature.data?.attributes
  const wired = (connections.data?.connections?.length ?? 0) > 0
  const name = feature.data?.name
  const serial = attrs?.meter_serial ? String(attrs.meter_serial) : ""
  const secondary = attrs
    ? [serial, labelFor("meter_role", attrs.meter_role), labelFor("phase", attrs.phase)].filter(Boolean).join(" · ")
    : ""

  return (
    <li className="flex items-start gap-1">
      <button
        type="button"
        className="hover:bg-muted/60 flex min-w-0 flex-1 flex-col rounded px-2 py-1.5 text-left"
        onClick={() => select(meter.feature_class, meter.feature_id)}
        onMouseEnter={() => locate(meter.feature_class, meter.feature_id)}
        onMouseLeave={clearLocated}
        title="Open this meter"
      >
        <span className="flex items-center gap-1.5 truncate text-sm">
          <Gauge className="text-primary size-3.5 shrink-0" />
          <span className="truncate font-medium">{name || serial || "Meter"}</span>
          <span className="text-muted-foreground shrink-0 text-xs">{shortId(meter.feature_id)}</span>
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
          title="Detach this meter from the service point (the meter itself is kept)"
        >
          <Unlink className="size-3.5" />
        </button>
      )}
    </li>
  )
}

/** A small "rides service point X" link shown at the top of a METER's panel so
 * the child shows its host at a glance -- the full list + attach lives on the
 * SP's Meters tab, so this is just the one-to-one reverse pointer, not a tab.
 * Renders nothing for a non-meter or an unattached meter. */
export function MeterHostChip() {
  const selected = useSelectionStore((s) => s.selected)
  const select = useSelectionStore((s) => s.select)
  const locate = useSelectionStore((s) => s.locate)
  const clearLocated = useSelectionStore((s) => s.clearLocated)
  const isMeter = selected?.featureClassCode === "METER"
  const { data } = useRelationships(
    isMeter ? selected?.featureClassCode : undefined,
    isMeter ? selected?.featureId : undefined,
  )
  const host = useMemo(() => {
    if (!isMeter) return null
    for (const r of data?.relationships ?? []) {
      if (r.origin.feature_class === "SERVICE_POINT") return r.origin
      if (r.destination.feature_class === "SERVICE_POINT") return r.destination
    }
    return null
  }, [data, isMeter])
  // Called unconditionally (hooks rules); disabled until there's a host.
  const hostFeature = useFeature(host?.feature_class, host?.feature_id)
  if (!isMeter || !host) return null
  const name = hostFeature.data?.name || shortId(host.feature_id)
  return (
    <button
      type="button"
      onClick={() => select(host.feature_class, host.feature_id)}
      onMouseEnter={() => locate(host.feature_class, host.feature_id)}
      onMouseLeave={clearLocated}
      className="hover:bg-muted/60 mb-3 inline-flex max-w-full items-center gap-1.5 rounded-md border px-2 py-1 text-xs"
      title="Open the service point this meter rides"
    >
      <Gauge className="text-primary size-3.5 shrink-0" />
      <span className="text-muted-foreground shrink-0">Rides service point</span>
      <span className="truncate font-medium">{name}</span>
    </button>
  )
}

export function ServicePointMetersTab() {
  const selected = useSelectionStore((s) => s.selected)
  const canEdit = useEditing()
  const spClass = selected?.featureClassCode
  const spId = selected?.featureId
  const { data, isPending, isError } = useRelationships(spClass, spId)
  const { data: schema } = useFeatureClassSchema("METER")
  const [adding, setAdding] = useState(false)
  const [toDetach, setToDetach] = useState<Relationship | null>(null)

  // The METER field schemas (for the attach form) and a code→label resolver (for
  // the list rows), both from the one METER schema fetch.
  const fields = useMemo(() => new Map((schema?.fields ?? []).map((f) => [f.name, f])), [schema])
  const labelFor = useMemo(() => {
    return (name: string, code: unknown): string => {
      if (code == null || code === "") return ""
      return fields.get(name)?.domain_value_labels?.[String(code)] ?? String(code)
    }
  }, [fields])

  // The SP's attached meters: any relationship whose other end is a METER.
  const meters = useMemo(() => {
    const rels = data?.relationships ?? []
    return rels
      .map((r) => {
        const meter =
          r.destination.feature_class === "METER"
            ? r.destination
            : r.origin.feature_class === "METER"
              ? r.origin
              : null
        return meter ? { relationship: r, meter } : null
      })
      .filter((x): x is { relationship: Relationship; meter: RelationshipEndpoint } => x !== null)
  }, [data])

  if (!selected || !spClass || !spId) return null

  return (
    <div className="flex flex-col gap-2">
      {canEdit &&
        (adding ? (
          <AttachMeterForm spClass={spClass} spId={spId} fields={fields} onDone={() => setAdding(false)} />
        ) : (
          <button
            type="button"
            onClick={() => setAdding(true)}
            className="border-input text-muted-foreground hover:bg-muted/60 inline-flex items-center justify-center gap-1.5 rounded-md border border-dashed px-3 py-1.5 text-sm"
          >
            <Plus className="size-4" />
            Attach a meter
          </button>
        ))}

      {isPending ? (
        <div className="text-muted-foreground flex items-center gap-2 text-sm">
          <Loader2 className="size-4 animate-spin" /> Loading…
        </div>
      ) : isError ? (
        <p className="text-destructive text-sm">Could not load this service point's meters.</p>
      ) : meters.length === 0 ? (
        <p className="text-muted-foreground text-sm">
          No meters on this service point yet.{canEdit ? " Attach one above." : ""}
        </p>
      ) : (
        <ul className="divide-y rounded-md border">
          {meters.map(({ relationship, meter }) => (
            <MeterRow
              key={meter.feature_id}
              meter={meter}
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
