import { useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { ArrowLeft, Copy, ListPlus, Loader2, Pencil, Plus, Trash2, Wrench } from "lucide-react"
import { Link, useParams } from "react-router"

import { ConfirmDeleteDialog } from "@/components/admin/confirm-delete-dialog"
import { FormDialog } from "@/components/admin/form-dialog"
import { IconPicker } from "@/components/admin/icon-picker"
import { SymbologySwatch, type SwatchGeometry } from "@/components/admin/symbology-swatch"
import { inputClass } from "@/components/map/field-utils"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardHeader, CardTitle } from "@/components/ui/card"
import {
  type AdminSymbologyRule,
  useAdminDomains,
  useAdminFeatureClasses,
  useAdminFieldGroups,
  useSymbologyRules,
} from "@/hooks/use-admin"
import { useFeatureClassRules } from "@/hooks/use-feature-class-rules"
import { type FeatureClassSchema, type FieldSchema, useFeatureClassSchema } from "@/hooks/use-feature-class-schema"
import { useSymbologyIcons } from "@/hooks/use-symbology-icons"
import { ApiError, apiFetch } from "@/lib/api"
import { LINE_DASH_STYLES } from "@/lib/line-dash"
import { useToastStore } from "@/stores/toast-store"

const labelClass = "text-xs font-medium"

const DATA_TYPES = ["text", "numeric", "integer", "boolean", "date", "timestamp", "uuid", "jsonb"] as const

/**
 * One field's domain, rendered the same way regardless of which kind it
 * is -- a coded domain's own values as a pill list, a range domain's
 * bounds as a single "min – max" pill. Mirrors admin-domains.tsx's own
 * per-domain display, just inline in a table cell instead of a card.
 *
 * The schema endpoint's own domain_values is just bare codes (see
 * fieldSchema/api/cmd/server/main.go) -- it's shared with every other
 * schema consumer (create/edit forms), so it was never worth widening
 * just for this page's own display. Cross-references useAdminDomains
 * (already fetched elsewhere on this page, for the edit-field domain
 * picker) instead, purely client-side, to show each value's real label
 * and whether it's material-backed -- the same wrench indicator
 * admin-domains.tsx's own value pills already use.
 */
function DomainCell({ field }: { field: FieldSchema }) {
  const { data: domainsData } = useAdminDomains()
  const domain = domainsData?.domains.find((d) => d.code === field.domain_code)

  if (!field.domain_code) return <span className="text-muted-foreground">—</span>
  if (field.domain_values) {
    return (
      <div className="flex flex-wrap gap-1">
        <span className="font-mono text-xs">{field.domain_code}</span>
        {field.domain_values.map((v) => {
          const value = domain?.values?.find((dv) => dv.code === v)
          return (
            <span
              key={v}
              className="bg-muted text-muted-foreground flex items-center gap-1 rounded-md px-1.5 py-0.5 text-xs"
              title={value?.material_spec_code ? `${v} · material spec: ${value.material_spec_code}` : v}
            >
              {value?.material_spec_code && <Wrench className="size-3" />}
              {value?.label ?? v}
            </span>
          )
        })}
      </div>
    )
  }
  if (field.range_min !== undefined && field.range_max !== undefined) {
    return (
      <div className="flex items-center gap-1.5">
        <span className="font-mono text-xs">{field.domain_code}</span>
        <span className="bg-muted text-muted-foreground rounded-md px-1.5 py-0.5 text-xs">
          {field.range_min} – {field.range_max}
        </span>
      </div>
    )
  }
  return <span className="font-mono text-xs">{field.domain_code}</span>
}

/**
 * Add one new subtype to this class, any status -- moved here from the
 * admin-feature-classes.tsx list page (a bulkier trigger like this
 * belongs on the one-class-at-a-time detail view, not a row action on a
 * many-classes-at-once table). Same "insert now, recompile to actually
 * enforce it" split as AddFieldRow below and api/internal/compiler's
 * syncCheckConstraints.
 */
function AddSubtypeRow({ code, subtypes, onDone }: { code: string; subtypes: { code: number; name: string }[]; onDone: () => void }) {
  const [subtypeCode, setSubtypeCode] = useState((subtypes.length ?? 0) + 1)
  const [name, setName] = useState("")
  const [saving, setSaving] = useState(false)
  const queryClient = useQueryClient()

  const canSave = name.trim().length > 0 && !subtypes.some((s) => s.code === subtypeCode)

  async function handleAdd() {
    setSaving(true)
    try {
      await apiFetch(`/admin/feature-classes/${code}/subtypes`, {
        method: "POST",
        body: JSON.stringify({ code: subtypeCode, name: name.trim() }),
      })
      queryClient.invalidateQueries({ queryKey: ["feature-class-schema", code] })
      queryClient.invalidateQueries({ queryKey: ["admin", "feature-classes"] })
      onDone()
    } catch (err) {
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not add this subtype.")
    } finally {
      setSaving(false)
    }
  }

  return (
    <FormDialog
      open
      onOpenChange={(next) => next || onDone()}
      title="Add subtype"
      onSubmit={handleAdd}
      submitLabel="Add"
      canSubmit={canSave}
      saving={saving}
    >
      <div className="flex items-end gap-2">
        <div className="flex flex-col gap-1">
          <label className={labelClass}>Code</label>
          <input
            type="number"
            className={`${inputClass} w-20`}
            value={subtypeCode}
            onChange={(e) => setSubtypeCode(Number(e.target.value))}
          />
        </div>
        <div className="flex flex-1 flex-col gap-1">
          <label className={labelClass}>Name</label>
          <input className={inputClass} value={name} onChange={(e) => setName(e.target.value)} placeholder="e.g. recloser" />
        </div>
      </div>
    </FormDialog>
  )
}

/**
 * Add one new field to this class, any status -- api/internal/admin's
 * POST .../fields (add.go). Same additive-only, insert-now-recompile-
 * later split as subtypes/domain values: the metadata insert alone
 * doesn't make the field usable on real features, a recompile actually
 * ALTER TABLE ADD COLUMNs it (compiler.go's existing-table path already
 * did this before this endpoint existed, just never reachable through
 * the API).
 */
function AddFieldRow({
  code,
  subtypes,
  onDone,
}: {
  code: string
  subtypes: { code: number; name: string }[]
  onDone: () => void
}) {
  const { data: domainsData } = useAdminDomains()
  const { data: groupsData } = useAdminFieldGroups()
  const [name, setName] = useState("")
  const [dataType, setDataType] = useState<(typeof DATA_TYPES)[number]>("text")
  const [domainCode, setDomainCode] = useState("")
  const [groupCode, setGroupCode] = useState("")
  const [nullable, setNullable] = useState(true)
  const [visibleOnCreate, setVisibleOnCreate] = useState(true)
  const [visibleOnUpdate, setVisibleOnUpdate] = useState(true)
  const [editableAfterPublish, setEditableAfterPublish] = useState(false)
  const [requiredFor, setRequiredFor] = useState<Set<number>>(new Set())
  const [saving, setSaving] = useState(false)
  const queryClient = useQueryClient()

  const canSave = /^[a-z][a-z0-9_]*$/.test(name)

  function toggleRequired(subtypeCode: number) {
    setRequiredFor((prev) => {
      const next = new Set(prev)
      if (next.has(subtypeCode)) next.delete(subtypeCode)
      else next.add(subtypeCode)
      return next
    })
  }

  async function handleAdd() {
    setSaving(true)
    try {
      await apiFetch(`/admin/feature-classes/${code}/fields`, {
        method: "POST",
        body: JSON.stringify({
          name: name.trim(),
          data_type: dataType,
          domain_code: domainCode || undefined,
          group_code: groupCode || undefined,
          nullable,
          visible_on_create: visibleOnCreate,
          visible_on_update: visibleOnUpdate,
          editable_after_publish: editableAfterPublish,
          required_for_subtypes: [...requiredFor],
        }),
      })
      queryClient.invalidateQueries({ queryKey: ["feature-class-schema", code] })
      queryClient.invalidateQueries({ queryKey: ["admin", "feature-classes"] })
      onDone()
    } catch (err) {
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not add this field.")
    } finally {
      setSaving(false)
    }
  }

  return (
    <FormDialog
      open
      onOpenChange={(next) => next || onDone()}
      title="Add field"
      onSubmit={handleAdd}
      submitLabel="Add"
      canSubmit={canSave}
      saving={saving}
      className="max-w-2xl"
    >
      <div className="grid grid-cols-2 gap-3">
          <div className="flex flex-col gap-1">
            <label className={labelClass}>
              Name <span className="text-destructive">*</span>
            </label>
            <input className={inputClass} value={name} onChange={(e) => setName(e.target.value)} placeholder="e.g. voltage_class" />
          </div>
          <div className="flex flex-col gap-1">
            <label className={labelClass}>Data type</label>
            <select className={inputClass} value={dataType} onChange={(e) => setDataType(e.target.value as typeof dataType)}>
              {DATA_TYPES.map((t) => (
                <option key={t} value={t}>
                  {t}
                </option>
              ))}
            </select>
          </div>
          <div className="flex flex-col gap-1">
            <label className={labelClass}>Domain</label>
            <select className={inputClass} value={domainCode} onChange={(e) => setDomainCode(e.target.value)}>
              <option value="">none</option>
              {domainsData?.domains.map((d) => (
                <option key={d.code} value={d.code}>
                  {d.name} ({d.code})
                </option>
              ))}
            </select>
          </div>
          <div className="flex flex-col gap-1">
            <label className={labelClass}>Group</label>
            <select className={inputClass} value={groupCode} onChange={(e) => setGroupCode(e.target.value)}>
              <option value="">none</option>
              {groupsData?.field_groups.map((g) => (
                <option key={g.code} value={g.code}>
                  {g.name}
                </option>
              ))}
            </select>
          </div>
        </div>

        <div className="flex flex-wrap gap-4">
          <label className="flex items-center gap-2 text-sm">
            <input type="checkbox" checked={nullable} onChange={(e) => setNullable(e.target.checked)} />
            Nullable
          </label>
          <label className="flex items-center gap-2 text-sm">
            <input type="checkbox" checked={visibleOnCreate} onChange={(e) => setVisibleOnCreate(e.target.checked)} />
            Visible on create
          </label>
          <label className="flex items-center gap-2 text-sm">
            <input type="checkbox" checked={visibleOnUpdate} onChange={(e) => setVisibleOnUpdate(e.target.checked)} />
            Visible on update
          </label>
          <label className="flex items-center gap-2 text-sm">
            <input
              type="checkbox"
              checked={editableAfterPublish}
              onChange={(e) => setEditableAfterPublish(e.target.checked)}
            />
            Editable after publish
          </label>
        </div>

      {subtypes.length > 0 && (
        <div className="flex flex-col gap-1">
          <label className={labelClass}>Required for subtypes</label>
          <div className="flex flex-wrap gap-3">
            {subtypes.map((s) => (
              <label key={s.code} className="flex items-center gap-2 text-sm">
                <input type="checkbox" checked={requiredFor.has(s.code)} onChange={() => toggleRequired(s.code)} />
                {s.name}
              </label>
            ))}
          </div>
        </div>
      )}
    </FormDialog>
  )
}

/**
 * Edit an existing field's domain, nullable, and form-visibility flags --
 * api/internal/admin's PATCH .../fields/{name} (update.go). Name and
 * data_type aren't editable here because the endpoint itself doesn't
 * accept them (the compiler has no rename/retype path). group_code is
 * also left out of this form: the Fields table has never displayed which
 * group a field belongs to, so there's no current value to show as a
 * starting point -- editing it would mean guessing at a value the admin
 * can't see is being replaced. domain_code sends the empty string to
 * mean "clear the assignment", matching updateFieldRequest's own
 * sentinel convention (see its doc comment in api/internal/admin/types.go).
 */
function EditFieldRow({ code, field, onDone }: { code: string; field: FieldSchema; onDone: () => void }) {
  const { data: domainsData } = useAdminDomains()
  const [domainCode, setDomainCode] = useState(field.domain_code ?? "")
  const [nullable, setNullable] = useState(field.nullable)
  const [visibleOnCreate, setVisibleOnCreate] = useState(field.visible_on_create)
  const [visibleOnUpdate, setVisibleOnUpdate] = useState(field.visible_on_update)
  const [editableAfterPublish, setEditableAfterPublish] = useState(field.editable_after_publish)
  const [saving, setSaving] = useState(false)
  const queryClient = useQueryClient()

  async function handleSave() {
    setSaving(true)
    try {
      await apiFetch(`/admin/feature-classes/${code}/fields/${field.name}`, {
        method: "PATCH",
        body: JSON.stringify({
          domain_code: domainCode,
          nullable,
          visible_on_create: visibleOnCreate,
          visible_on_update: visibleOnUpdate,
          editable_after_publish: editableAfterPublish,
        }),
      })
      queryClient.invalidateQueries({ queryKey: ["feature-class-schema", code] })
      onDone()
    } catch (err) {
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not update this field.")
    } finally {
      setSaving(false)
    }
  }

  return (
    <FormDialog
      open
      onOpenChange={(next) => next || onDone()}
      title={
        <>
          Edit field <span className="font-mono">{field.name}</span>
        </>
      }
      onSubmit={handleSave}
      submitLabel="Save"
      canSubmit
      saving={saving}
    >
      <div className="flex flex-col gap-1">
        <label className={labelClass}>Domain</label>
        <select className={inputClass} value={domainCode} onChange={(e) => setDomainCode(e.target.value)}>
          <option value="">none</option>
          {domainsData?.domains.map((d) => (
            <option key={d.code} value={d.code}>
              {d.name} ({d.code})
            </option>
          ))}
        </select>
      </div>

      <div className="flex flex-wrap gap-4">
        <label className="flex items-center gap-2 text-sm">
          <input type="checkbox" checked={nullable} onChange={(e) => setNullable(e.target.checked)} />
          Nullable
        </label>
        <label className="flex items-center gap-2 text-sm">
          <input type="checkbox" checked={visibleOnCreate} onChange={(e) => setVisibleOnCreate(e.target.checked)} />
          Visible on create
        </label>
        <label className="flex items-center gap-2 text-sm">
          <input type="checkbox" checked={visibleOnUpdate} onChange={(e) => setVisibleOnUpdate(e.target.checked)} />
          Visible on update
        </label>
        <label className="flex items-center gap-2 text-sm">
          <input
            type="checkbox"
            checked={editableAfterPublish}
            onChange={(e) => setEditableAfterPublish(e.target.checked)}
          />
          Editable after publish
        </label>
      </div>
    </FormDialog>
  )
}

/**
 * Every network/relationship/snapping rule where this class appears on
 * either side -- reuses the exact same GET /feature-classes/{code}/rules
 * response the map's own feature-panel "Rules" tab already reads
 * (api/internal/rules), just rendered as tables here instead of cards,
 * matching this page's own Fields table above. Class-scoped like every
 * other section on this page -- unlike the feature-panel tab, there's no
 * specific feature instance in play here, which is exactly why this
 * endpoint fits a class-detail page as naturally as a feature panel: the
 * answer is the same either way.
 */
function RulesSection({ code }: { code: string }) {
  const { data, isPending, isError } = useFeatureClassRules(code)

  if (isPending) {
    return (
      <div className="text-muted-foreground flex items-center gap-2 text-sm">
        <Loader2 className="size-4 animate-spin" />
        Loading rules...
      </div>
    )
  }
  if (isError || !data) {
    return <p className="text-destructive text-sm">Could not load rules.</p>
  }

  const hasNothing =
    data.network_rules.length === 0 && data.relationship_rules.length === 0 && data.snapping_rules.length === 0
  if (hasNothing) {
    return <p className="text-muted-foreground text-sm">No rules configured for this class.</p>
  }

  return (
    <div className="flex flex-col gap-4">
      {data.network_rules.length > 0 && (
        <div className="flex flex-col gap-2">
          <h3 className="text-muted-foreground text-xs font-medium">Network</h3>
          <div className="overflow-hidden rounded-lg border">
            <table className="w-full text-sm">
              <thead className="bg-muted/50 text-muted-foreground text-left text-xs">
                <tr>
                  <th className="px-3 py-2 font-medium">Name</th>
                  <th className="px-3 py-2 font-medium">Direction</th>
                  <th className="px-3 py-2 font-medium">From</th>
                  <th className="px-3 py-2 font-medium">To</th>
                  <th className="px-3 py-2 font-medium">Phase rule</th>
                  <th className="px-3 py-2 font-medium">Valid</th>
                </tr>
              </thead>
              <tbody>
                {data.network_rules.map((r, i) => (
                  <tr key={i} className="border-t">
                    <td className="px-3 py-2">{r.name}</td>
                    <td className="px-3 py-2">{r.direction === "outgoing" ? "Outgoing" : "Incoming"}</td>
                    <td className="px-3 py-2 font-mono text-xs">
                      {r.from_class}
                      {r.from_terminal ? ` (${r.from_terminal})` : ""}
                    </td>
                    <td className="px-3 py-2 font-mono text-xs">
                      {r.to_class}
                      {r.to_terminal ? ` (${r.to_terminal})` : ""}
                    </td>
                    <td className="px-3 py-2 text-xs">{r.phase_rule ?? "—"}</td>
                    <td className="px-3 py-2">{r.is_valid ? "yes" : "no"}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}

      {data.relationship_rules.length > 0 && (
        <div className="flex flex-col gap-2">
          <h3 className="text-muted-foreground text-xs font-medium">Relationship</h3>
          <div className="overflow-hidden rounded-lg border">
            <table className="w-full text-sm">
              <thead className="bg-muted/50 text-muted-foreground text-left text-xs">
                <tr>
                  <th className="px-3 py-2 font-medium">Name</th>
                  <th className="px-3 py-2 font-medium">Direction</th>
                  <th className="px-3 py-2 font-medium">Origin</th>
                  <th className="px-3 py-2 font-medium">Destination</th>
                  <th className="px-3 py-2 font-medium">Kind</th>
                  <th className="px-3 py-2 font-medium">Cardinality</th>
                </tr>
              </thead>
              <tbody>
                {data.relationship_rules.map((r, i) => (
                  <tr key={i} className="border-t">
                    <td className="px-3 py-2">{r.name}</td>
                    <td className="px-3 py-2">{r.direction === "origin" ? "Origin" : "Destination"}</td>
                    <td className="px-3 py-2 font-mono text-xs">{r.origin_class}</td>
                    <td className="px-3 py-2 font-mono text-xs">{r.destination_class}</td>
                    <td className="px-3 py-2 text-xs">{r.kind}</td>
                    <td className="px-3 py-2 text-xs">{r.cardinality}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}

      {data.snapping_rules.length > 0 && (
        <div className="flex flex-col gap-2">
          <h3 className="text-muted-foreground text-xs font-medium">Snapping</h3>
          <div className="overflow-hidden rounded-lg border">
            <table className="w-full text-sm">
              <thead className="bg-muted/50 text-muted-foreground text-left text-xs">
                <tr>
                  <th className="px-3 py-2 font-medium">Direction</th>
                  <th className="px-3 py-2 font-medium">From</th>
                  <th className="px-3 py-2 font-medium">To</th>
                  <th className="px-3 py-2 font-medium">Target type</th>
                  <th className="px-3 py-2 font-medium">Tolerance</th>
                  <th className="px-3 py-2 font-medium">Priority</th>
                  <th className="px-3 py-2 font-medium">Required</th>
                </tr>
              </thead>
              <tbody>
                {data.snapping_rules.map((r, i) => (
                  <tr key={i} className="border-t">
                    <td className="px-3 py-2">{r.direction === "from" ? "As source" : "As target"}</td>
                    <td className="px-3 py-2 font-mono text-xs">{r.from_class}</td>
                    <td className="px-3 py-2 font-mono text-xs">
                      {r.to_class}
                      {r.to_terminal ? ` (${r.to_terminal})` : ""}
                    </td>
                    <td className="px-3 py-2 text-xs">{r.target_type}</td>
                    <td className="px-3 py-2 text-xs">{r.tolerance_m}m</td>
                    <td className="px-3 py-2 text-xs">{r.priority}</td>
                    <td className="px-3 py-2">{r.is_required ? "yes" : "no"}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}
    </div>
  )
}

const RENDER_CONTEXTS = ["map", "diagram"] as const

/** A rule's style_def in the small subset of state the editor form holds
 * -- strings for every numeric/color field so an empty input stays empty
 * rather than coercing to 0/NaN, converted to real numbers only when the
 * body is built (toStyleBody). */
interface StyleFields {
  iconRef: string
  color: string
  scale: string
  width: string
  dash: string
  fillColor: string
  strokeColor: string
}

const EMPTY_STYLE: StyleFields = {
  iconRef: "circle",
  color: "#2563eb",
  scale: "",
  width: "",
  dash: "solid",
  fillColor: "",
  strokeColor: "",
}

function styleFieldsFrom(rule: AdminSymbologyRule): StyleFields {
  const s = rule.style_def
  return {
    iconRef: s.icon_ref ?? "circle",
    color: s.color ?? "#2563eb",
    scale: s.scale !== undefined ? String(s.scale) : "",
    width: s.width !== undefined ? String(s.width) : "",
    dash: s.dash ?? "solid",
    fillColor: s.fill_color ?? "",
    strokeColor: s.stroke_color ?? "",
  }
}

/** Only the style keys actually filled become part of the body -- mirrors
 * api/internal/admin's buildStyleDef, which writes only present keys so a
 * point rule's style_def never carries line/polygon keys. */
function toStyleBody(s: StyleFields): Record<string, unknown> {
  return {
    icon_ref: s.iconRef.trim() === "" ? undefined : s.iconRef.trim(),
    color: s.color.trim() === "" ? undefined : s.color.trim(),
    scale: s.scale.trim() === "" ? undefined : Number(s.scale),
    width: s.width.trim() === "" ? undefined : Number(s.width),
    // "solid" is the default a rule simply omits -- only a real pattern is
    // written, keeping a solid line's style_def free of a redundant key
    // (same "only present keys" discipline as buildStyleDef).
    dash: s.dash === "solid" || s.dash.trim() === "" ? undefined : s.dash,
    fill_color: s.fillColor.trim() === "" ? undefined : s.fillColor.trim(),
    stroke_color: s.strokeColor.trim() === "" ? undefined : s.strokeColor.trim(),
  }
}

/** A plain-language summary of what a rule targets, for the list row --
 * "default" when it constrains nothing (the mandatory feature-class-only
 * rule), else the subtype and/or domain-value it narrows to, plus its zoom
 * band if any. Mirrors the most-specific-wins axes migrations/0012
 * documents. */
function targetingSummary(rule: AdminSymbologyRule): string {
  const parts: string[] = []
  if (rule.subtype_code !== undefined) parts.push(`subtype ${rule.subtype_code}`)
  if (rule.domain_field && rule.domain_value_code) parts.push(`${rule.domain_field} = ${rule.domain_value_code}`)
  let base = parts.length > 0 ? parts.join(" · ") : "default (whole class)"
  if (rule.min_zoom !== undefined || rule.max_zoom !== undefined) {
    base += ` · zoom ${rule.min_zoom ?? "–"}…${rule.max_zoom ?? "–"}`
  }
  return base
}

/** A one-line "what this rule draws" for the list row's subtitle, keyed
 * to geometry so it names the style axis that geometry actually uses -- a
 * point's icon, a line's width, a polygon's fill/stroke -- instead of
 * only ever showing icon_ref (blank for every non-point rule). */
function styleSummary(geometry: SwatchGeometry, style: AdminSymbologyRule["style_def"]): string {
  if (geometry === "line") {
    const parts = [style.dash ?? "solid"]
    if (style.width !== undefined) parts.push(`${style.width}px`)
    return parts.join(" · ")
  }
  if (geometry === "polygon") {
    const fill = style.fill_color ?? style.color
    return fill ? `fill ${fill}` : "polygon"
  }
  return style.icon_ref ?? "icon"
}

/**
 * Add or edit one symbology rule -- which icon/color satisfies which
 * subtype/domain-value, per render context and zoom band. This is the
 * "shouldn't the admin UI do this" answer: the same rows a user would
 * otherwise INSERT into metadata.symbology_rules by hand (the DB stays
 * directly editable), authored visually with a live icon preview instead.
 *
 * The targeting pickers reuse the exact schema this page already loads --
 * subtypes for the subtype picker, a coded field's own domain_values for
 * the value picker -- so no lookup endpoint is needed. A blank subtype /
 * field means "unconstrained" (matches anything), the same nil axis the
 * server resolves. A domain field and its value go together: the value
 * picker only appears once a field is chosen, and both are sent or
 * neither (api/internal/admin rejects a half-set pair).
 */
function RuleForm({
  code,
  schema,
  rule,
  duplicate,
  onDone,
}: {
  code: string
  schema: FeatureClassSchema
  rule?: AdminSymbologyRule
  /** When true, `rule` seeds the form's initial values but saving creates
   * a NEW row (POST) instead of updating the source (PATCH) -- the
   * "duplicate this rule, then tweak it" path. The source rule is left
   * untouched; the copy starts identical and the author narrows it (a
   * different subtype, a dashed instead of solid line, ...) before saving. */
  duplicate?: boolean
  onDone: () => void
}) {
  const [renderContext, setRenderContext] = useState<(typeof RENDER_CONTEXTS)[number]>(
    (rule?.render_context as (typeof RENDER_CONTEXTS)[number]) ?? "map",
  )
  const [subtypeCode, setSubtypeCode] = useState<string>(rule?.subtype_code !== undefined ? String(rule.subtype_code) : "")
  const [domainField, setDomainField] = useState<string>(rule?.domain_field ?? "")
  const [domainValueCode, setDomainValueCode] = useState<string>(rule?.domain_value_code ?? "")
  const [minZoom, setMinZoom] = useState<string>(rule?.min_zoom !== undefined ? String(rule.min_zoom) : "")
  const [maxZoom, setMaxZoom] = useState<string>(rule?.max_zoom !== undefined ? String(rule.max_zoom) : "")
  const [style, setStyle] = useState<StyleFields>(rule ? styleFieldsFrom(rule) : EMPTY_STYLE)
  const [saving, setSaving] = useState(false)
  const queryClient = useQueryClient()

  // Only coded fields (those with domain_values) can target a value.
  const codedFields = schema.fields.filter((f) => f.domain_values && f.domain_values.length > 0)
  const selectedFieldValues = codedFields.find((f) => f.name === domainField)?.domain_values ?? []

  // Backend requires domain_field and domain_value_code together -- guard
  // the same pairing here so save is disabled on a half-set target.
  const targetingValid = domainField === "" || domainValueCode !== ""
  const canSave = targetingValid

  async function handleSave() {
    setSaving(true)
    try {
      const body = {
        render_context: renderContext,
        subtype_code: subtypeCode === "" ? undefined : Number(subtypeCode),
        domain_field: domainField === "" ? undefined : domainField,
        domain_value_code: domainField === "" ? undefined : domainValueCode,
        min_zoom: minZoom.trim() === "" ? undefined : Number(minZoom),
        max_zoom: maxZoom.trim() === "" ? undefined : Number(maxZoom),
        style: toStyleBody(style),
      }
      if (rule && !duplicate) {
        await apiFetch(`/admin/symbology-rules/${rule.id}`, { method: "PATCH", body: JSON.stringify(body) })
      } else {
        await apiFetch(`/admin/feature-classes/${code}/symbology-rules`, { method: "POST", body: JSON.stringify(body) })
      }
      queryClient.invalidateQueries({ queryKey: ["admin", "symbology-rules", code] })
      queryClient.invalidateQueries({ queryKey: ["feature-class-schema", code] })
      onDone()
    } catch (err) {
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not save this rule.")
    } finally {
      setSaving(false)
    }
  }

  const isLine = schema.geometry_type === "line"
  const isPolygon = schema.geometry_type === "polygon"

  // A live preview of the line/polygon appearance, from the same string
  // state the inputs hold -- the icon path (points) already previews
  // through IconPicker, so this only feeds the stroke/fill swatch.
  const previewStyle = {
    color: style.color,
    width: style.width.trim() === "" ? undefined : Number(style.width),
    dash: style.dash,
    fill_color: style.fillColor || undefined,
    stroke_color: style.strokeColor || undefined,
  }

  return (
    <FormDialog
      open
      onOpenChange={(next) => next || onDone()}
      title={rule ? (duplicate ? "Duplicate rule" : "Edit rule") : "Add rule"}
      onSubmit={handleSave}
      submitLabel={rule && !duplicate ? "Save" : "Add rule"}
      canSubmit={canSave}
      saving={saving}
      className="max-w-2xl"
    >
      <div className="grid grid-cols-2 gap-3 sm:grid-cols-3">
          <div className="flex flex-col gap-1">
            <label className={labelClass}>Render context</label>
            <select
              className={inputClass}
              value={renderContext}
              onChange={(e) => setRenderContext(e.target.value as typeof renderContext)}
            >
              {RENDER_CONTEXTS.map((c) => (
                <option key={c} value={c}>
                  {c}
                </option>
              ))}
            </select>
          </div>
          <div className="flex flex-col gap-1">
            <label className={labelClass}>Subtype</label>
            <select className={inputClass} value={subtypeCode} onChange={(e) => setSubtypeCode(e.target.value)}>
              <option value="">any</option>
              {schema.subtypes.map((s) => (
                <option key={s.code} value={s.code}>
                  {s.code}: {s.name}
                </option>
              ))}
            </select>
          </div>
          <div className="flex flex-col gap-1">
            <label className={labelClass}>Field</label>
            <select
              className={inputClass}
              value={domainField}
              onChange={(e) => {
                setDomainField(e.target.value)
                setDomainValueCode("")
              }}
            >
              <option value="">any</option>
              {codedFields.map((f) => (
                <option key={f.name} value={f.name}>
                  {f.name}
                </option>
              ))}
            </select>
          </div>
          {domainField !== "" && (
            <div className="flex flex-col gap-1">
              <label className={labelClass}>
                Value <span className="text-destructive">*</span>
              </label>
              <select
                className={inputClass}
                value={domainValueCode}
                onChange={(e) => setDomainValueCode(e.target.value)}
              >
                <option value="">select…</option>
                {selectedFieldValues.map((v) => (
                  <option key={v} value={v}>
                    {v}
                  </option>
                ))}
              </select>
            </div>
          )}
          <div className="flex flex-col gap-1">
            <label className={labelClass}>Min zoom</label>
            <input
              type="number"
              className={inputClass}
              value={minZoom}
              onChange={(e) => setMinZoom(e.target.value)}
              placeholder="any"
            />
          </div>
          <div className="flex flex-col gap-1">
            <label className={labelClass}>Max zoom</label>
            <input
              type="number"
              className={inputClass}
              value={maxZoom}
              onChange={(e) => setMaxZoom(e.target.value)}
              placeholder="any"
            />
          </div>
        </div>

        <div className="flex flex-col gap-2 border-t pt-3">
          <div className="flex flex-wrap items-end gap-3">
            <div className="flex flex-col gap-1">
              <label className={labelClass}>Color</label>
              <input
                type="color"
                value={style.color}
                onChange={(e) => setStyle({ ...style, color: e.target.value })}
                className="h-8 w-12 rounded-md border"
              />
            </div>
            {isLine && (
              <>
                <div className="flex flex-col gap-1">
                  <label className={labelClass}>Width</label>
                  <input
                    type="number"
                    className={`${inputClass} w-20`}
                    value={style.width}
                    onChange={(e) => setStyle({ ...style, width: e.target.value })}
                  />
                </div>
                <div className="flex flex-col gap-1">
                  <label className={labelClass}>Dash</label>
                  <select
                    className={inputClass}
                    value={style.dash}
                    onChange={(e) => setStyle({ ...style, dash: e.target.value })}
                  >
                    {LINE_DASH_STYLES.map((d) => (
                      <option key={d} value={d}>
                        {d}
                      </option>
                    ))}
                  </select>
                </div>
              </>
            )}
            {isPolygon && (
              <>
                <div className="flex flex-col gap-1">
                  <label className={labelClass}>Fill color</label>
                  <input
                    type="color"
                    value={style.fillColor || "#2563eb"}
                    onChange={(e) => setStyle({ ...style, fillColor: e.target.value })}
                    className="h-8 w-12 rounded-md border"
                  />
                </div>
                <div className="flex flex-col gap-1">
                  <label className={labelClass}>Stroke color</label>
                  <input
                    type="color"
                    value={style.strokeColor || "#1e3a8a"}
                    onChange={(e) => setStyle({ ...style, strokeColor: e.target.value })}
                    className="h-8 w-12 rounded-md border"
                  />
                </div>
              </>
            )}
            {!isLine && !isPolygon && (
              <div className="flex flex-col gap-1">
                <label className={labelClass}>Scale</label>
                <input
                  type="number"
                  step="0.1"
                  className={`${inputClass} w-20`}
                  value={style.scale}
                  onChange={(e) => setStyle({ ...style, scale: e.target.value })}
                  placeholder="1"
                />
              </div>
            )}
            {(isLine || isPolygon) && (
              <div className="flex flex-col gap-1">
                <label className={labelClass}>Preview</label>
                <div className="bg-muted/40 flex size-10 items-center justify-center rounded-md border">
                  <SymbologySwatch
                    geometry={schema.geometry_type as SwatchGeometry}
                    style={previewStyle}
                    className="size-7"
                  />
                </div>
              </div>
            )}
          </div>
          {!isLine && !isPolygon && (
            <div className="flex flex-col gap-1">
              <label className={labelClass}>Icon</label>
              <IconPicker
                value={style.iconRef}
                color={style.color}
                onChange={(ref) => setStyle({ ...style, iconRef: ref })}
              />
            </div>
          )}
        </div>
    </FormDialog>
  )
}

/**
 * The per-class symbology rule editor -- lists every rule (with a live
 * swatch of exactly how it renders) and lets an author add, edit, or
 * delete one. Deleting has no guard, matching api/internal/admin's
 * deleteSymbologyRule: the "exactly one default rule" invariant is
 * enforced by the compiler at publish, not by blocking a delete, so a
 * class left without a default simply fails its next compile loudly.
 */
function SymbologySection({ code, schema }: { code: string; schema: FeatureClassSchema }) {
  const { data, isPending, isError } = useSymbologyRules(code)
  const icons = useSymbologyIcons()
  const queryClient = useQueryClient()
  const [adding, setAdding] = useState(false)
  const [editingId, setEditingId] = useState<string | null>(null)
  // The rule a "Duplicate" click seeds a new-rule form from -- distinct
  // from editingId (which edits in place) because a copy saves a NEW row.
  const [duplicateSource, setDuplicateSource] = useState<AdminSymbologyRule | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<AdminSymbologyRule | null>(null)

  return (
    <>
      <div className="flex items-center justify-between">
        <h2 className="text-sm font-semibold">Symbology</h2>
        <Button
          variant="ghost"
          size="sm"
          onClick={() => {
            setEditingId(null)
            setDuplicateSource(null)
            setAdding(true)
          }}
        >
          <Plus className="size-4" />
          Add rule
        </Button>
      </div>

      {adding && <RuleForm code={code} schema={schema} onDone={() => setAdding(false)} />}

      {duplicateSource && (
        <RuleForm
          key={duplicateSource.id}
          code={code}
          schema={schema}
          rule={duplicateSource}
          duplicate
          onDone={() => setDuplicateSource(null)}
        />
      )}

      {isPending ? (
        <div className="text-muted-foreground flex items-center gap-2 text-sm">
          <Loader2 className="size-4 animate-spin" />
          Loading rules...
        </div>
      ) : isError || !data ? (
        <p className="text-destructive text-sm">Could not load symbology rules.</p>
      ) : data.symbology_rules.length === 0 ? (
        <p className="text-muted-foreground text-sm">
          No symbology rules yet — this class renders with the built-in default until one is added.
        </p>
      ) : (
        <div className="flex flex-col gap-2">
          {data.symbology_rules.map((r) =>
            editingId === r.id ? (
              <RuleForm key={r.id} code={code} schema={schema} rule={r} onDone={() => setEditingId(null)} />
            ) : (
              <Card key={r.id}>
                <CardHeader className="flex flex-row items-center gap-3 py-3">
                  <div className="bg-muted/40 flex size-9 shrink-0 items-center justify-center rounded-md border">
                    <SymbologySwatch
                      geometry={schema.geometry_type as SwatchGeometry}
                      style={r.style_def}
                      icons={icons}
                      className="size-6"
                    />
                  </div>
                  <div className="min-w-0 flex-1">
                    <CardTitle className="text-sm">{targetingSummary(r)}</CardTitle>
                    <p className="text-muted-foreground font-mono text-xs">
                      {r.render_context} · {styleSummary(schema.geometry_type as SwatchGeometry, r.style_def)}
                    </p>
                  </div>
                  <Button
                    variant="ghost"
                    size="icon"
                    className="size-7"
                    title="Edit"
                    onClick={() => {
                      setAdding(false)
                      setDuplicateSource(null)
                      setEditingId(r.id)
                    }}
                  >
                    <Pencil className="size-4" />
                  </Button>
                  <Button
                    variant="ghost"
                    size="icon"
                    className="size-7"
                    title="Duplicate"
                    onClick={() => {
                      setAdding(false)
                      setEditingId(null)
                      setDuplicateSource(r)
                    }}
                  >
                    <Copy className="size-4" />
                  </Button>
                  <Button
                    variant="ghost"
                    size="icon"
                    className="text-destructive size-7"
                    onClick={() => setDeleteTarget(r)}
                  >
                    <Trash2 className="size-4" />
                  </Button>
                </CardHeader>
              </Card>
            ),
          )}
        </div>
      )}

      <ConfirmDeleteDialog
        open={deleteTarget !== null}
        title="Delete this symbology rule?"
        description="Removing a rule only reduces specificity. If it leaves the class without a default rule, the next compile will fail with a clear error rather than rendering nothing. This can't be undone."
        onOpenChange={(open) => !open && setDeleteTarget(null)}
        onConfirm={async () => {
          if (!deleteTarget) return
          await apiFetch(`/admin/symbology-rules/${deleteTarget.id}`, { method: "DELETE" })
          queryClient.invalidateQueries({ queryKey: ["admin", "symbology-rules", code] })
          queryClient.invalidateQueries({ queryKey: ["feature-class-schema", code] })
        }}
      />
    </>
  )
}

/**
 * A single feature class's full picture -- fields (with resolved
 * domain info), subtypes, terminals -- reusing the exact same
 * GET /feature-classes/{code}/schema response the map's own create/
 * edit forms already read, rather than a second admin-only endpoint.
 * The admin list page (admin-feature-classes.tsx) only ever showed a
 * subtype/field *count*; this is the page that answers "what does this
 * class actually have."
 */
export function AdminFeatureClassDetailPage() {
  const { code } = useParams<{ code: string }>()
  const { data: classesData } = useAdminFeatureClasses()
  const { data: schema, isPending, isError } = useFeatureClassSchema(code)
  const queryClient = useQueryClient()
  const [compiling, setCompiling] = useState(false)
  const [addingSubtype, setAddingSubtype] = useState(false)
  const [addingField, setAddingField] = useState(false)
  const [editingFieldName, setEditingFieldName] = useState<string | null>(null)

  const summary = classesData?.feature_classes.find((fc) => fc.code === code)

  async function handleCompile() {
    if (!code) return
    setCompiling(true)
    try {
      await apiFetch(`/admin/feature-classes/${code}/compile`, { method: "POST" })
      queryClient.invalidateQueries({ queryKey: ["admin", "feature-classes"] })
      queryClient.invalidateQueries({ queryKey: ["feature-classes"] })
    } catch (err) {
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not compile this class.")
    } finally {
      setCompiling(false)
    }
  }

  if (!code) return null

  return (
    <div className="h-full overflow-y-auto p-4">
      <div className="mx-auto flex max-w-4xl flex-col gap-4">
        <Link to="/admin/feature-classes" className="text-muted-foreground flex items-center gap-1 text-sm hover:underline">
          <ArrowLeft className="size-3.5" />
          Feature classes
        </Link>

        {isPending ? (
          <div className="text-muted-foreground flex items-center gap-2 text-sm">
            <Loader2 className="size-4 animate-spin" />
            Loading...
          </div>
        ) : isError || !schema ? (
          <p className="text-destructive text-sm">Could not load this feature class.</p>
        ) : (
          <>
            <div className="flex items-center justify-between">
              <div>
                <h1 className="text-lg font-semibold">
                  {schema.name} <span className="text-muted-foreground font-mono text-sm">{schema.code}</span>
                </h1>
                <p className="text-muted-foreground text-sm">
                  {schema.geometry_type}
                  {summary && ` · ${summary.network_role}`}
                  {summary && (
                    <>
                      {" · "}
                      <Badge variant={summary.status === "active" ? "success" : summary.status === "draft" ? "secondary" : "outline"}>
                        {summary.status}
                      </Badge>
                    </>
                  )}
                </p>
              </div>
              <Button size="sm" variant="outline" onClick={handleCompile} disabled={compiling}>
                {compiling && <Loader2 className="size-4 animate-spin" />}
                {summary?.status === "draft" ? "Compile" : "Recompile"}
              </Button>
            </div>

            <div className="flex items-center justify-between">
              <h2 className="text-sm font-semibold">Subtypes</h2>
              <Button variant="ghost" size="sm" onClick={() => setAddingSubtype(true)}>
                <Plus className="size-4" />
                Add subtype
              </Button>
            </div>
            {addingSubtype && (
              <AddSubtypeRow code={code} subtypes={schema.subtypes} onDone={() => setAddingSubtype(false)} />
            )}
            {schema.subtypes.length === 0 ? (
              <p className="text-muted-foreground text-sm">No subtypes.</p>
            ) : (
              <div className="flex flex-wrap gap-1">
                {schema.subtypes.map((s) => (
                  <span key={s.code} className="bg-muted text-muted-foreground rounded-md px-1.5 py-0.5 text-xs">
                    {s.code}: {s.name}
                  </span>
                ))}
              </div>
            )}

            <div className="flex items-center justify-between">
              <h2 className="text-sm font-semibold">Fields</h2>
              <Button
                variant="ghost"
                size="sm"
                onClick={() => {
                  setEditingFieldName(null)
                  setAddingField(true)
                }}
              >
                <ListPlus className="size-4" />
                Add field
              </Button>
            </div>
            {addingField && (
              <AddFieldRow code={code} subtypes={schema.subtypes} onDone={() => setAddingField(false)} />
            )}
            {editingFieldName &&
              (() => {
                const editingField = schema.fields.find((f) => f.name === editingFieldName)
                return editingField ? (
                  <EditFieldRow code={code} field={editingField} onDone={() => setEditingFieldName(null)} />
                ) : null
              })()}
            {schema.fields.length === 0 ? (
              <p className="text-muted-foreground text-sm">No fields.</p>
            ) : (
              <div className="overflow-hidden rounded-lg border">
                <table className="w-full text-sm">
                  <thead className="bg-muted/50 text-muted-foreground text-left text-xs">
                    <tr>
                      <th className="px-3 py-2 font-medium">Name</th>
                      <th className="px-3 py-2 font-medium">Type</th>
                      <th className="px-3 py-2 font-medium">Domain</th>
                      <th className="px-3 py-2 font-medium">Nullable</th>
                      <th className="px-3 py-2 font-medium">Create</th>
                      <th className="px-3 py-2 font-medium">Update</th>
                      <th className="px-3 py-2 font-medium">Required for</th>
                      <th className="px-3 py-2 font-medium" />
                    </tr>
                  </thead>
                  <tbody>
                    {schema.fields.map((f) => (
                      <tr key={f.name} className="border-t">
                        <td className="px-3 py-2 font-mono text-xs">{f.name}</td>
                        <td className="px-3 py-2">{f.data_type}</td>
                        <td className="px-3 py-2">
                          <DomainCell field={f} />
                        </td>
                        <td className="px-3 py-2">{f.nullable ? "yes" : "no"}</td>
                        <td className="px-3 py-2">{f.visible_on_create ? "yes" : "no"}</td>
                        <td className="px-3 py-2">{f.visible_on_update ? "yes" : "no"}</td>
                        <td className="px-3 py-2">
                          {f.required_for_subtypes && f.required_for_subtypes.length > 0
                            ? f.required_for_subtypes.join(", ")
                            : "—"}
                        </td>
                        <td className="px-3 py-2 text-right">
                          <Button
                            variant="ghost"
                            size="icon"
                            className="size-7"
                            title="Edit"
                            onClick={() => {
                              setAddingField(false)
                              setEditingFieldName(f.name)
                            }}
                          >
                            <Pencil className="size-3.5" />
                          </Button>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}

            {schema.terminals && schema.terminals.length > 0 && (
              <>
                <h2 className="text-sm font-semibold">Terminals</h2>
                <div className="flex flex-wrap gap-1">
                  {schema.terminals.map((t) => (
                    <span key={`${t.code}:${t.subtype_code ?? "any"}`} className="bg-muted text-muted-foreground rounded-md px-1.5 py-0.5 text-xs">
                      {t.code} ({t.role}
                      {t.subtype_code !== undefined ? `, subtype ${t.subtype_code}` : ""})
                    </span>
                  ))}
                </div>
              </>
            )}

            <SymbologySection code={code} schema={schema} />

            <h2 className="text-sm font-semibold">Rules</h2>
            <RulesSection code={code} />
          </>
        )}
      </div>
    </div>
  )
}
