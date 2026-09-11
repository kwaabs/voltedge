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
  type AdminFeatureClassSummary,
  type AdminSymbologyRule,
  useAdminDomains,
  useAdminFeatureClasses,
  useAdminFieldGroups,
  useSymbologyRules,
} from "@/hooks/use-admin"
import { usePermission } from "@/hooks/use-auth"
import { useFeatureClassRules } from "@/hooks/use-feature-class-rules"
import { type FeatureClassSchema, type FieldSchema, useFeatureClassSchema } from "@/hooks/use-feature-class-schema"
import { useSymbologyIcons } from "@/hooks/use-symbology-icons"
import { ApiError, apiFetch } from "@/lib/api"
import { LINE_DASH_STYLES } from "@/lib/line-dash"
import type { LabelDef } from "@/lib/symbology"
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
 * Edit an existing field's domain, nullable, form-visibility flags, and
 * per-subtype required rules -- api/internal/admin's PATCH .../fields/{name}
 * (update.go). required_for_subtypes reconciles subtype_field_rules to
 * exactly the checked set (is_required is read live by the CRUD engine, so no
 * recompile is needed to make a field required, unlike a nullable flip). Name
 * and data_type aren't editable here because the endpoint itself doesn't
 * data_type aren't editable here because the endpoint itself doesn't
 * accept them (the compiler has no rename/retype path). group_code is
 * also left out of this form: the Fields table has never displayed which
 * group a field belongs to, so there's no current value to show as a
 * starting point -- editing it would mean guessing at a value the admin
 * can't see is being replaced. domain_code sends the empty string to
 * mean "clear the assignment", matching updateFieldRequest's own
 * sentinel convention (see its doc comment in api/internal/admin/types.go).
 */
function EditFieldRow({
  code,
  field,
  subtypes,
  onDone,
}: {
  code: string
  field: FieldSchema
  subtypes: { code: number; name: string }[]
  onDone: () => void
}) {
  const { data: domainsData } = useAdminDomains()
  const [domainCode, setDomainCode] = useState(field.domain_code ?? "")
  const [nullable, setNullable] = useState(field.nullable)
  const [visibleOnCreate, setVisibleOnCreate] = useState(field.visible_on_create)
  const [visibleOnUpdate, setVisibleOnUpdate] = useState(field.visible_on_update)
  const [editableAfterPublish, setEditableAfterPublish] = useState(field.editable_after_publish)
  const [requiredFor, setRequiredFor] = useState<Set<number>>(new Set(field.required_for_subtypes ?? []))
  const [saving, setSaving] = useState(false)
  const queryClient = useQueryClient()

  function toggleRequired(subtypeCode: number) {
    setRequiredFor((prev) => {
      const next = new Set(prev)
      if (next.has(subtypeCode)) next.delete(subtypeCode)
      else next.add(subtypeCode)
      return next
    })
  }

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
          required_for_subtypes: [...requiredFor],
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
// Mirrors metadata.relationship_classes' kind CHECK (migrations/0010, widened
// by 0075 for protection, 0080 for metering_sensing, 0082 for cable_routing and
// 0083 for panel_bay) and the API's validRelationshipKinds in
// api/internal/admin/rules.go -- keep all three in sync when a kind is added, or
// the authoring dropdown lags what the schema and seeds already allow.
const RELATIONSHIP_KINDS = [
  "spatial_support",
  "containment",
  "composite",
  "protection_trip",
  "protection_sensing",
  "metering_sensing",
  "cable_routing",
  "panel_bay",
] as const
const CARDINALITIES = ["one_to_one", "one_to_many", "many_to_many"] as const
// The SNAP_TARGET_TYPE domain values (seed/snapping_rules_example.sql):
// point_feature (snap to a whole point feature), endpoint (a line's end),
// edge (anywhere along a line), vertex (an interior shape vertex).
const SNAP_TARGET_TYPES = ["point_feature", "endpoint", "edge", "vertex"] as const

type RuleKind = "network" | "relationship" | "snapping"

/**
 * The three kinds of rule metadata for a class -- network (legal
 * electrical connectivity), relationship (structural/spatial pairings),
 * and snapping (digitizing coincidence). All three are now authorable in
 * place (add + delete) via api/internal/admin's /network-rules,
 * /relationship-classes, and /snapping-rules. Every write invalidates the
 * class-scoped feature-class-rules query so the tables refresh. All three
 * are pure metadata -- no recompile follows, the connectivity/relationship
 * validators read the rows live. Caveat: snapping rules are display-only
 * for now (the map's Snap interaction doesn't yet consume tolerance/target
 * type -- docs/open-items.md), so authoring one shows here but doesn't
 * change snap behavior until that wiring slice lands.
 */
function RulesSection({ code }: { code: string }) {
  const { data, isPending, isError } = useFeatureClassRules(code)
  const { data: classesData } = useAdminFeatureClasses()
  const canWrite = usePermission()("metadata:write")
  const queryClient = useQueryClient()
  const [adding, setAdding] = useState<RuleKind | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<{ kind: RuleKind; id: string; name: string } | null>(null)

  const classes = classesData?.feature_classes ?? []

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

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-col gap-2">
        <div className="flex items-center justify-between">
          <h3 className="text-muted-foreground text-xs font-medium">Network</h3>
          {canWrite && (
            <Button variant="outline" size="sm" className="h-7 gap-1 text-xs" onClick={() => setAdding("network")}>
              <Plus className="size-3.5" />
              Add network rule
            </Button>
          )}
        </div>
        {data.network_rules.length > 0 ? (
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
                  {canWrite && <th className="px-3 py-2" />}
                </tr>
              </thead>
              <tbody>
                {data.network_rules.map((r) => (
                  <tr key={r.id} className="border-t">
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
                    {canWrite && (
                      <td className="px-3 py-2 text-right">
                        <Button
                          variant="ghost"
                          size="icon"
                          className="text-destructive size-7"
                          title="Delete"
                          onClick={() => setDeleteTarget({ kind: "network", id: r.id, name: r.name })}
                        >
                          <Trash2 className="size-4" />
                        </Button>
                      </td>
                    )}
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        ) : (
          <p className="text-muted-foreground text-sm">No network rules.</p>
        )}
      </div>

      <div className="flex flex-col gap-2">
        <div className="flex items-center justify-between">
          <h3 className="text-muted-foreground text-xs font-medium">Relationship</h3>
          {canWrite && (
            <Button variant="outline" size="sm" className="h-7 gap-1 text-xs" onClick={() => setAdding("relationship")}>
              <Plus className="size-3.5" />
              Add relationship
            </Button>
          )}
        </div>
        {data.relationship_rules.length > 0 ? (
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
                  {canWrite && <th className="px-3 py-2" />}
                </tr>
              </thead>
              <tbody>
                {data.relationship_rules.map((r) => (
                  <tr key={r.id} className="border-t">
                    <td className="px-3 py-2">{r.name}</td>
                    <td className="px-3 py-2">{r.direction === "origin" ? "Origin" : "Destination"}</td>
                    <td className="px-3 py-2 font-mono text-xs">{r.origin_class}</td>
                    <td className="px-3 py-2 font-mono text-xs">{r.destination_class}</td>
                    <td className="px-3 py-2 text-xs">{r.kind}</td>
                    <td className="px-3 py-2 text-xs">{r.cardinality}</td>
                    {canWrite && (
                      <td className="px-3 py-2 text-right">
                        <Button
                          variant="ghost"
                          size="icon"
                          className="text-destructive size-7"
                          title="Delete"
                          onClick={() => setDeleteTarget({ kind: "relationship", id: r.id, name: r.name })}
                        >
                          <Trash2 className="size-4" />
                        </Button>
                      </td>
                    )}
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        ) : (
          <p className="text-muted-foreground text-sm">No relationship rules.</p>
        )}
      </div>

      <div className="flex flex-col gap-2">
        <div className="flex items-center justify-between">
          <h3 className="text-muted-foreground text-xs font-medium">Snapping</h3>
          {canWrite && (
            <Button variant="outline" size="sm" className="h-7 gap-1 text-xs" onClick={() => setAdding("snapping")}>
              <Plus className="size-3.5" />
              Add snapping rule
            </Button>
          )}
        </div>
        {data.snapping_rules.length > 0 ? (
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
                  {canWrite && <th className="px-3 py-2" />}
                </tr>
              </thead>
              <tbody>
                {data.snapping_rules.map((r) => (
                  <tr key={r.id} className="border-t">
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
                    {canWrite && (
                      <td className="px-3 py-2 text-right">
                        <Button
                          variant="ghost"
                          size="icon"
                          className="text-destructive size-7"
                          title="Delete"
                          onClick={() =>
                            setDeleteTarget({ kind: "snapping", id: r.id, name: `${r.from_class} → ${r.to_class}` })
                          }
                        >
                          <Trash2 className="size-4" />
                        </Button>
                      </td>
                    )}
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        ) : (
          <p className="text-muted-foreground text-sm">No snapping rules.</p>
        )}
      </div>

      {adding === "network" && <AddNetworkRuleForm code={code} classes={classes} onDone={() => setAdding(null)} />}
      {adding === "relationship" && (
        <AddRelationshipForm code={code} classes={classes} onDone={() => setAdding(null)} />
      )}
      {adding === "snapping" && <AddSnappingRuleForm code={code} classes={classes} onDone={() => setAdding(null)} />}

      <ConfirmDeleteDialog
        open={deleteTarget !== null}
        title={
          deleteTarget?.kind === "network"
            ? "Delete this network rule?"
            : deleteTarget?.kind === "relationship"
              ? "Delete this relationship rule?"
              : "Delete this snapping rule?"
        }
        description={
          deleteTarget?.kind === "network"
            ? "This removes a legal connectivity pattern. Connections already established stay, but this pairing can no longer be validated as legal until re-added. This can't be undone."
            : deleteTarget?.kind === "relationship"
              ? "This removes a legal structural/spatial pairing. Relationships already established stay, but this pairing can no longer be established until re-added. This can't be undone."
              : "This removes a digitizing snap target. Nothing already drawn changes; this pairing just won't be offered as a snap target once the map consumes these rules. This can't be undone."
        }
        onOpenChange={(open) => !open && setDeleteTarget(null)}
        onConfirm={async () => {
          if (!deleteTarget) return
          const path =
            deleteTarget.kind === "network"
              ? "network-rules"
              : deleteTarget.kind === "relationship"
                ? "relationship-classes"
                : "snapping-rules"
          await apiFetch(`/admin/${path}/${deleteTarget.id}`, { method: "DELETE" })
          queryClient.invalidateQueries({ queryKey: ["feature-class-rules", code] })
        }}
      />
    </div>
  )
}

/** Distinct terminal codes for a class, so the network-rule form's
 * terminal pickers list each code once even when the class carries
 * duplicate/subtype-scoped terminal rows sharing a code. */
function terminalCodeOptions(schema: FeatureClassSchema | undefined): string[] {
  const seen = new Set<string>()
  for (const t of schema?.terminals ?? []) seen.add(t.code)
  return [...seen].sort()
}

/**
 * Add one network (connectivity) rule -- api/internal/admin's POST
 * /network-rules. A rule names a legal from-class -> to-class electrical
 * connection, optionally narrowed to specific terminals (e.g.
 * SERVICE_CUTOUT T2 -> METER T1). Terminals are optional: an edge (a
 * conductor) has none. is_valid defaults on -- a rule is authored to
 * permit a connection.
 */
function AddNetworkRuleForm({
  code,
  classes,
  onDone,
}: {
  code: string
  classes: AdminFeatureClassSummary[]
  onDone: () => void
}) {
  const [name, setName] = useState("")
  const [fromClass, setFromClass] = useState(code)
  const [fromTerminal, setFromTerminal] = useState("")
  const [toClass, setToClass] = useState("")
  const [toTerminal, setToTerminal] = useState("")
  const [phaseRule, setPhaseRule] = useState("")
  const [isValid, setIsValid] = useState(true)
  const [saving, setSaving] = useState(false)
  const queryClient = useQueryClient()

  const { data: fromSchema } = useFeatureClassSchema(fromClass || undefined)
  const { data: toSchema } = useFeatureClassSchema(toClass || undefined)
  const fromTerminals = terminalCodeOptions(fromSchema)
  const toTerminals = terminalCodeOptions(toSchema)

  const canSave = name.trim().length > 0 && fromClass !== "" && toClass !== ""

  async function handleAdd() {
    setSaving(true)
    try {
      await apiFetch("/admin/network-rules", {
        method: "POST",
        body: JSON.stringify({
          name: name.trim(),
          from_class: fromClass,
          from_terminal: fromTerminal || undefined,
          to_class: toClass,
          to_terminal: toTerminal || undefined,
          phase_rule: phaseRule.trim() || undefined,
          is_valid: isValid,
        }),
      })
      queryClient.invalidateQueries({ queryKey: ["feature-class-rules", code] })
      onDone()
    } catch (err) {
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not add this network rule.")
    } finally {
      setSaving(false)
    }
  }

  return (
    <FormDialog
      open
      onOpenChange={(next) => next || onDone()}
      title="Add network rule"
      onSubmit={handleAdd}
      submitLabel="Add"
      canSubmit={canSave}
      saving={saving}
      className="max-w-2xl"
    >
      <div className="flex flex-col gap-3">
        <div className="flex flex-col gap-1">
          <label className={labelClass}>
            Name <span className="text-destructive">*</span>
          </label>
          <input
            className={inputClass}
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="e.g. SERVICE_CUTOUT T2 -> METER T1"
          />
        </div>
        <div className="grid grid-cols-2 gap-3">
          <div className="flex flex-col gap-1">
            <label className={labelClass}>
              From class <span className="text-destructive">*</span>
            </label>
            <select
              className={inputClass}
              value={fromClass}
              onChange={(e) => {
                setFromClass(e.target.value)
                setFromTerminal("")
              }}
            >
              <option value="">Select a class…</option>
              {classes.map((c) => (
                <option key={c.code} value={c.code}>
                  {c.code}
                </option>
              ))}
            </select>
          </div>
          <div className="flex flex-col gap-1">
            <label className={labelClass}>From terminal</label>
            <select
              className={inputClass}
              value={fromTerminal}
              onChange={(e) => setFromTerminal(e.target.value)}
              disabled={fromTerminals.length === 0}
            >
              <option value="">{fromTerminals.length === 0 ? "(no terminals)" : "(none)"}</option>
              {fromTerminals.map((t) => (
                <option key={t} value={t}>
                  {t}
                </option>
              ))}
            </select>
          </div>
          <div className="flex flex-col gap-1">
            <label className={labelClass}>
              To class <span className="text-destructive">*</span>
            </label>
            <select
              className={inputClass}
              value={toClass}
              onChange={(e) => {
                setToClass(e.target.value)
                setToTerminal("")
              }}
            >
              <option value="">Select a class…</option>
              {classes.map((c) => (
                <option key={c.code} value={c.code}>
                  {c.code}
                </option>
              ))}
            </select>
          </div>
          <div className="flex flex-col gap-1">
            <label className={labelClass}>To terminal</label>
            <select
              className={inputClass}
              value={toTerminal}
              onChange={(e) => setToTerminal(e.target.value)}
              disabled={toTerminals.length === 0}
            >
              <option value="">{toTerminals.length === 0 ? "(no terminals)" : "(none)"}</option>
              {toTerminals.map((t) => (
                <option key={t} value={t}>
                  {t}
                </option>
              ))}
            </select>
          </div>
        </div>
        <div className="flex flex-col gap-1">
          <label className={labelClass}>Phase rule</label>
          <input
            className={inputClass}
            value={phaseRule}
            onChange={(e) => setPhaseRule(e.target.value)}
            placeholder="optional, free text (e.g. from.voltage_class = to.primary_voltage)"
          />
        </div>
        <label className="flex items-center gap-2 text-sm">
          <input type="checkbox" checked={isValid} onChange={(e) => setIsValid(e.target.checked)} />
          Valid (permits the connection)
        </label>
      </div>
    </FormDialog>
  )
}

/**
 * Add one relationship class -- api/internal/admin's POST
 * /relationship-classes. Defines a legal structural/spatial pairing
 * (e.g. STRUCTURE contains METER_CHAMBER) with a kind and cardinality;
 * unlike a network rule, mere row-presence is the legality (no valid
 * flag). name is unique across all relationship classes.
 */
function AddRelationshipForm({
  code,
  classes,
  onDone,
}: {
  code: string
  classes: AdminFeatureClassSummary[]
  onDone: () => void
}) {
  const [name, setName] = useState("")
  const [originClass, setOriginClass] = useState(code)
  const [destinationClass, setDestinationClass] = useState("")
  const [kind, setKind] = useState<(typeof RELATIONSHIP_KINDS)[number]>("containment")
  const [cardinality, setCardinality] = useState<(typeof CARDINALITIES)[number]>("one_to_many")
  const [saving, setSaving] = useState(false)
  const queryClient = useQueryClient()

  const canSave = name.trim().length > 0 && originClass !== "" && destinationClass !== ""

  async function handleAdd() {
    setSaving(true)
    try {
      await apiFetch("/admin/relationship-classes", {
        method: "POST",
        body: JSON.stringify({
          name: name.trim(),
          origin_class: originClass,
          destination_class: destinationClass,
          kind,
          cardinality,
        }),
      })
      queryClient.invalidateQueries({ queryKey: ["feature-class-rules", code] })
      onDone()
    } catch (err) {
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not add this relationship.")
    } finally {
      setSaving(false)
    }
  }

  return (
    <FormDialog
      open
      onOpenChange={(next) => next || onDone()}
      title="Add relationship"
      onSubmit={handleAdd}
      submitLabel="Add"
      canSubmit={canSave}
      saving={saving}
      className="max-w-2xl"
    >
      <div className="flex flex-col gap-3">
        <div className="flex flex-col gap-1">
          <label className={labelClass}>
            Name <span className="text-destructive">*</span>
          </label>
          <input
            className={inputClass}
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="e.g. STRUCTURE contains METER_CHAMBER"
          />
        </div>
        <div className="grid grid-cols-2 gap-3">
          <div className="flex flex-col gap-1">
            <label className={labelClass}>
              Origin class <span className="text-destructive">*</span>
            </label>
            <select className={inputClass} value={originClass} onChange={(e) => setOriginClass(e.target.value)}>
              <option value="">Select a class…</option>
              {classes.map((c) => (
                <option key={c.code} value={c.code}>
                  {c.code}
                </option>
              ))}
            </select>
          </div>
          <div className="flex flex-col gap-1">
            <label className={labelClass}>
              Destination class <span className="text-destructive">*</span>
            </label>
            <select
              className={inputClass}
              value={destinationClass}
              onChange={(e) => setDestinationClass(e.target.value)}
            >
              <option value="">Select a class…</option>
              {classes.map((c) => (
                <option key={c.code} value={c.code}>
                  {c.code}
                </option>
              ))}
            </select>
          </div>
          <div className="flex flex-col gap-1">
            <label className={labelClass}>Kind</label>
            <select className={inputClass} value={kind} onChange={(e) => setKind(e.target.value as typeof kind)}>
              {RELATIONSHIP_KINDS.map((k) => (
                <option key={k} value={k}>
                  {k}
                </option>
              ))}
            </select>
          </div>
          <div className="flex flex-col gap-1">
            <label className={labelClass}>Cardinality</label>
            <select
              className={inputClass}
              value={cardinality}
              onChange={(e) => setCardinality(e.target.value as typeof cardinality)}
            >
              {CARDINALITIES.map((c) => (
                <option key={c} value={c}>
                  {c}
                </option>
              ))}
            </select>
          </div>
        </div>
      </div>
    </FormDialog>
  )
}

/**
 * Add one snapping rule -- api/internal/admin's POST /snapping-rules. A
 * rule says: while digitizing a from-class feature, its geometry should
 * snap to a to-class target (optionally a specific terminal) of the given
 * target type, within tolerance (meters). priority breaks ties when
 * several targets are in range (lower wins); is_required flags a snap the
 * digitizer shouldn't skip. Unlike the other two rule kinds this has no
 * name and no valid flag -- a snap rule is its from/to/target pairing.
 * Display-only for now: the map's Snap interaction doesn't yet consume
 * these (docs/open-items.md), so a new rule shows in the table but won't
 * change snap behavior until that wiring lands.
 */
function AddSnappingRuleForm({
  code,
  classes,
  onDone,
}: {
  code: string
  classes: AdminFeatureClassSummary[]
  onDone: () => void
}) {
  const [fromClass, setFromClass] = useState(code)
  const [toClass, setToClass] = useState("")
  const [toTerminal, setToTerminal] = useState("")
  const [targetType, setTargetType] = useState<(typeof SNAP_TARGET_TYPES)[number]>("point_feature")
  const [toleranceM, setToleranceM] = useState("0.5")
  const [priority, setPriority] = useState("1")
  const [isRequired, setIsRequired] = useState(false)
  const [saving, setSaving] = useState(false)
  const queryClient = useQueryClient()

  const { data: toSchema } = useFeatureClassSchema(toClass || undefined)
  const toTerminals = terminalCodeOptions(toSchema)

  const tolNum = Number(toleranceM)
  const canSave = fromClass !== "" && toClass !== "" && Number.isFinite(tolNum) && tolNum > 0

  async function handleAdd() {
    setSaving(true)
    try {
      await apiFetch("/admin/snapping-rules", {
        method: "POST",
        body: JSON.stringify({
          from_class: fromClass,
          to_class: toClass,
          to_terminal: toTerminal || undefined,
          target_type: targetType,
          tolerance_m: tolNum,
          priority: priority.trim() === "" ? undefined : Number(priority),
          is_required: isRequired,
        }),
      })
      queryClient.invalidateQueries({ queryKey: ["feature-class-rules", code] })
      onDone()
    } catch (err) {
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not add this snapping rule.")
    } finally {
      setSaving(false)
    }
  }

  return (
    <FormDialog
      open
      onOpenChange={(next) => next || onDone()}
      title="Add snapping rule"
      onSubmit={handleAdd}
      submitLabel="Add"
      canSubmit={canSave}
      saving={saving}
      className="max-w-2xl"
    >
      <div className="flex flex-col gap-3">
        <p className="text-muted-foreground text-xs">
          Display-only for now — the map's snap interaction doesn't consume these yet, so this shows in the rules table
          but won't change snapping behavior until that wiring lands.
        </p>
        <div className="grid grid-cols-2 gap-3">
          <div className="flex flex-col gap-1">
            <label className={labelClass}>
              From class <span className="text-destructive">*</span>
            </label>
            <select className={inputClass} value={fromClass} onChange={(e) => setFromClass(e.target.value)}>
              <option value="">Select a class…</option>
              {classes.map((c) => (
                <option key={c.code} value={c.code}>
                  {c.code}
                </option>
              ))}
            </select>
          </div>
          <div className="flex flex-col gap-1">
            <label className={labelClass}>
              To class <span className="text-destructive">*</span>
            </label>
            <select
              className={inputClass}
              value={toClass}
              onChange={(e) => {
                setToClass(e.target.value)
                setToTerminal("")
              }}
            >
              <option value="">Select a class…</option>
              {classes.map((c) => (
                <option key={c.code} value={c.code}>
                  {c.code}
                </option>
              ))}
            </select>
          </div>
          <div className="flex flex-col gap-1">
            <label className={labelClass}>To terminal</label>
            <select
              className={inputClass}
              value={toTerminal}
              onChange={(e) => setToTerminal(e.target.value)}
              disabled={toTerminals.length === 0}
            >
              <option value="">{toTerminals.length === 0 ? "(no terminals)" : "(none)"}</option>
              {toTerminals.map((t) => (
                <option key={t} value={t}>
                  {t}
                </option>
              ))}
            </select>
          </div>
          <div className="flex flex-col gap-1">
            <label className={labelClass}>Target type</label>
            <select
              className={inputClass}
              value={targetType}
              onChange={(e) => setTargetType(e.target.value as typeof targetType)}
            >
              {SNAP_TARGET_TYPES.map((t) => (
                <option key={t} value={t}>
                  {t}
                </option>
              ))}
            </select>
          </div>
          <div className="flex flex-col gap-1">
            <label className={labelClass}>
              Tolerance (m) <span className="text-destructive">*</span>
            </label>
            <input
              className={inputClass}
              type="number"
              step="0.1"
              min="0"
              value={toleranceM}
              onChange={(e) => setToleranceM(e.target.value)}
            />
          </div>
          <div className="flex flex-col gap-1">
            <label className={labelClass}>Priority</label>
            <input
              className={inputClass}
              type="number"
              step="1"
              value={priority}
              onChange={(e) => setPriority(e.target.value)}
              placeholder="1 (lower wins)"
            />
          </div>
        </div>
        <label className="flex items-center gap-2 text-sm">
          <input type="checkbox" checked={isRequired} onChange={(e) => setIsRequired(e.target.checked)} />
          Required (the digitizer shouldn't leave this snap unmade)
        </label>
      </div>
    </FormDialog>
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
const LABEL_DEFAULT_COLOR = "#1f2937"
const LABEL_DEFAULT_HALO_COLOR = "#ffffff"
const LABEL_DEFAULT_HALO_WIDTH = 3

/** The label-editor form state -- strings for the numeric fields (empty
 * stays empty, coerced only on save), mirroring StyleFields' discipline. */
interface LabelFields {
  template: string
  size: string
  color: string
  bold: boolean
  italic: boolean
  halo: boolean
  haloColor: string
  haloWidth: string
  minZoom: string
  offsetX: string
  offsetY: string
  rotation: string
}

function labelFieldsFrom(def: LabelDef | undefined): LabelFields {
  return {
    template: def?.template ?? "",
    size: def?.size !== undefined ? String(def.size) : "",
    color: def?.color ?? LABEL_DEFAULT_COLOR,
    bold: def?.bold ?? false,
    italic: def?.italic ?? false,
    // Halo defaults ON (map-layers.ts does the same) -- a never-configured
    // class still gets a legible outline once labelling is turned on.
    halo: def?.halo ?? true,
    haloColor: def?.halo_color ?? LABEL_DEFAULT_HALO_COLOR,
    haloWidth: def?.halo_width !== undefined ? String(def.halo_width) : "",
    minZoom: def?.min_zoom !== undefined ? String(def.min_zoom) : "",
    offsetX: def?.offset_x !== undefined ? String(def.offset_x) : "",
    offsetY: def?.offset_y !== undefined ? String(def.offset_y) : "",
    rotation: def?.rotation !== undefined ? String(def.rotation) : "",
  }
}

/** Builds the label_def body -- only keys that differ from the map's own
 * defaults are written, keeping the jsonb tight (same "only present keys"
 * discipline as the symbology toStyleBody). template is the on-switch and
 * always written when non-empty. */
function toLabelDef(f: LabelFields): LabelDef {
  return {
    template: f.template.trim(),
    size: f.size.trim() === "" ? undefined : Number(f.size),
    color: f.color.trim() === "" || f.color === LABEL_DEFAULT_COLOR ? undefined : f.color.trim(),
    bold: f.bold ? true : undefined,
    italic: f.italic ? true : undefined,
    // Only the non-default (off) needs recording; on is the default.
    halo: f.halo ? undefined : false,
    halo_color:
      !f.halo || f.haloColor.trim() === "" || f.haloColor === LABEL_DEFAULT_HALO_COLOR ? undefined : f.haloColor.trim(),
    halo_width: !f.halo || f.haloWidth.trim() === "" ? undefined : Number(f.haloWidth),
    min_zoom: f.minZoom.trim() === "" ? undefined : Number(f.minZoom),
    // Placement (point/polygon labels): only record a non-zero nudge/spin.
    offset_x: f.offsetX.trim() === "" || Number(f.offsetX) === 0 ? undefined : Number(f.offsetX),
    offset_y: f.offsetY.trim() === "" ? undefined : Number(f.offsetY),
    rotation: f.rotation.trim() === "" || Number(f.rotation) === 0 ? undefined : Number(f.rotation),
  }
}

/** A one-line summary of a class's current label config for the section
 * header -- the template, or a muted "No label" when unset/empty. */
function labelSummary(def: LabelDef | undefined): string {
  return def?.template && def.template.trim() !== "" ? def.template : "No label"
}

/**
 * The per-class map label editor. Labelling is one config per feature
 * class (not per symbology rule), so it lives in its own section with a
 * single Edit dialog rather than a list. Stored as label_def jsonb on the
 * feature class (PATCH /admin/feature-classes/{code}); an empty template
 * clears it (sent as label_def: null). Map only -- the diagram toggles
 * labels per placement.
 */
function LabelSection({ code, schema }: { code: string; schema: FeatureClassSchema }) {
  const canWrite = usePermission()("metadata:write")
  const [editing, setEditing] = useState(false)
  const def = schema.label_def
  const hasLabel = !!def?.template && def.template.trim() !== ""

  return (
    <>
      <div className="flex items-center justify-between">
        <h2 className="text-sm font-semibold">Labelling</h2>
        {canWrite && (
          <Button variant="ghost" size="sm" onClick={() => setEditing(true)}>
            <Pencil className="size-4" />
            {hasLabel ? "Edit label" : "Add label"}
          </Button>
        )}
      </div>

      <p className={hasLabel ? "font-mono text-sm" : "text-muted-foreground text-sm"}>{labelSummary(def)}</p>

      {editing && <LabelForm code={code} schema={schema} onDone={() => setEditing(false)} />}
    </>
  )
}

function LabelForm({ code, schema, onDone }: { code: string; schema: FeatureClassSchema; onDone: () => void }) {
  const queryClient = useQueryClient()
  const [fields, setFields] = useState<LabelFields>(() => labelFieldsFrom(schema.label_def))
  const [insertField, setInsertField] = useState("")
  const [saving, setSaving] = useState(false)

  const hasTemplate = fields.template.trim() !== ""

  // Append a {field} token -- a straightforward append (not cursor-aware)
  // keeps this robust; the author can rearrange the template text freely.
  function appendField(name: string) {
    if (name === "") return
    setFields((f) => ({ ...f, template: `${f.template}{${name}}` }))
    setInsertField("")
  }

  const previewFont = `${fields.italic ? "italic " : ""}${fields.bold ? "bold " : ""}${
    fields.size.trim() === "" ? 12 : Number(fields.size)
  }px ui-sans-serif, system-ui, sans-serif`

  async function handleSave() {
    setSaving(true)
    try {
      // Empty template clears the config to SQL/JSON null; otherwise the
      // built label_def rides the shared PATCH /admin/feature-classes.
      const body = { label_def: hasTemplate ? toLabelDef(fields) : null }
      await apiFetch(`/admin/feature-classes/${code}`, { method: "PATCH", body: JSON.stringify(body) })
      queryClient.invalidateQueries({ queryKey: ["feature-class-schema", code] })
      onDone()
    } catch (err) {
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not save the label.")
    } finally {
      setSaving(false)
    }
  }

  return (
    <FormDialog
      open
      onOpenChange={(next) => next || onDone()}
      title="Map label"
      description="One label per feature class, drawn on the geographic map. Use {field} placeholders to insert attribute values."
      onSubmit={handleSave}
      submitLabel="Save"
      canSubmit
      saving={saving}
      className="max-w-2xl"
    >
      <div className="flex flex-col gap-1">
        <label className={labelClass}>Template</label>
        <input
          className={inputClass}
          value={fields.template}
          onChange={(e) => setFields({ ...fields, template: e.target.value })}
          placeholder="{name} ({voltage} kV)"
        />
        <div className="flex items-center gap-2">
          <select className={`${inputClass} w-auto`} value={insertField} onChange={(e) => appendField(e.target.value)}>
            <option value="">Insert field…</option>
            {/* Built-in gis columns (published as tile props, not in schema.fields) */}
            <optgroup label="Built-in">
              <option value="name">name</option>
            </optgroup>
            {schema.fields.length > 0 && (
              <optgroup label="Fields">
                {schema.fields.map((f) => (
                  <option key={f.name} value={f.name}>
                    {f.name}
                  </option>
                ))}
              </optgroup>
            )}
          </select>
          <span className="text-muted-foreground text-xs">
            Empty template removes the label. Missing values collapse out.
          </span>
        </div>
      </div>

      {/* Live styling preview -- shows font size/weight/slant/color over a
          representative background; placeholders show literally since no
          real feature values are available here. */}
      <div className="flex flex-col gap-1">
        <label className={labelClass}>Preview</label>
        <div className="bg-muted/40 flex min-h-10 items-center rounded-md border px-3 py-2">
          {hasTemplate ? (
            <span
              style={{
                font: previewFont,
                color: fields.color,
                // Preview the rotation (a positive angle reads clockwise, like
                // the map); offsets aren't meaningful without a real glyph anchor.
                display: "inline-block",
                transform: fields.rotation.trim() === "" ? undefined : `rotate(${Number(fields.rotation)}deg)`,
              }}
            >
              {fields.template}
            </span>
          ) : (
            <span className="text-muted-foreground text-sm">No label</span>
          )}
        </div>
      </div>

      <div className="flex flex-wrap items-end gap-3 border-t pt-3">
        <div className="flex flex-col gap-1">
          <label className={labelClass}>Size</label>
          <input
            type="number"
            step="1"
            className={`${inputClass} w-20`}
            value={fields.size}
            onChange={(e) => setFields({ ...fields, size: e.target.value })}
            placeholder="12"
          />
        </div>
        <div className="flex flex-col gap-1">
          <label className={labelClass}>Color</label>
          <input
            type="color"
            value={fields.color || LABEL_DEFAULT_COLOR}
            onChange={(e) => setFields({ ...fields, color: e.target.value })}
            className="h-8 w-12 rounded-md border"
          />
        </div>
        <label className="flex items-center gap-1.5 text-sm">
          <input
            type="checkbox"
            checked={fields.bold}
            onChange={(e) => setFields({ ...fields, bold: e.target.checked })}
          />
          Bold
        </label>
        <label className="flex items-center gap-1.5 text-sm">
          <input
            type="checkbox"
            checked={fields.italic}
            onChange={(e) => setFields({ ...fields, italic: e.target.checked })}
          />
          Italic
        </label>
        <div className="flex flex-col gap-1">
          <label className={labelClass}>Min zoom</label>
          <input
            type="number"
            className={`${inputClass} w-24`}
            value={fields.minZoom}
            onChange={(e) => setFields({ ...fields, minZoom: e.target.value })}
            placeholder="any"
          />
        </div>
      </div>

      <div className="flex flex-wrap items-end gap-3 border-t pt-3">
        <label className="flex items-center gap-1.5 text-sm">
          <input
            type="checkbox"
            checked={fields.halo}
            onChange={(e) => setFields({ ...fields, halo: e.target.checked })}
          />
          Halo (outline)
        </label>
        {fields.halo && (
          <>
            <div className="flex flex-col gap-1">
              <label className={labelClass}>Halo color</label>
              <input
                type="color"
                value={fields.haloColor || LABEL_DEFAULT_HALO_COLOR}
                onChange={(e) => setFields({ ...fields, haloColor: e.target.value })}
                className="h-8 w-12 rounded-md border"
              />
            </div>
            <div className="flex flex-col gap-1">
              <label className={labelClass}>Halo width</label>
              <input
                type="number"
                step="1"
                className={`${inputClass} w-20`}
                value={fields.haloWidth}
                onChange={(e) => setFields({ ...fields, haloWidth: e.target.value })}
                placeholder="3"
              />
            </div>
          </>
        )}
      </div>

      {/* Placement -- shifts/spins the label for every feature of the class.
          Point/polygon labels only; a line label rides the line and ignores
          these. Offset Y blank = the default 14px drop below the glyph. */}
      <div className="flex flex-wrap items-end gap-3 border-t pt-3">
        <div className="flex flex-col gap-1">
          <label className={labelClass}>Offset X</label>
          <input
            type="number"
            step="1"
            className={`${inputClass} w-20`}
            value={fields.offsetX}
            onChange={(e) => setFields({ ...fields, offsetX: e.target.value })}
            placeholder="0"
          />
        </div>
        <div className="flex flex-col gap-1">
          <label className={labelClass}>Offset Y</label>
          <input
            type="number"
            step="1"
            className={`${inputClass} w-20`}
            value={fields.offsetY}
            onChange={(e) => setFields({ ...fields, offsetY: e.target.value })}
            placeholder="14"
          />
        </div>
        <div className="flex flex-col gap-1">
          <label className={labelClass}>Rotation°</label>
          <input
            type="number"
            step="5"
            className={`${inputClass} w-24`}
            value={fields.rotation}
            onChange={(e) => setFields({ ...fields, rotation: e.target.value })}
            placeholder="0"
          />
        </div>
        <span className="text-muted-foreground text-xs">Point/polygon labels only — lines ride the line.</span>
      </div>
    </FormDialog>
  )
}

function SymbologySection({ code, schema }: { code: string; schema: FeatureClassSchema }) {
  const { data, isPending, isError } = useSymbologyRules(code)
  const icons = useSymbologyIcons()
  const queryClient = useQueryClient()
  // Add/edit/duplicate/delete a rule are all metadata:write on the server.
  const canWrite = usePermission()("metadata:write")
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
        {canWrite && (
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
        )}
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
                  {canWrite && (
                    <>
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
                    </>
                  )}
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
 * Edit this class's two pure-metadata diagram flags -- whether it can
 * host a one-line/schematic diagram (is_diagram_container) and whether
 * its features can be placed onto one (is_diagram_placeable). Both are
 * safe to change at ANY status: api/internal/admin/update.go treats them
 * as metadata with no physical-table footprint (only geometry/srid/
 * network_role are draft-gated there). That's exactly why this lives on
 * the always-available detail page rather than only in the draft-only
 * inline edit form on the list page -- an already-compiled class had no
 * other UI path to toggle them. PATCHes the same
 * /admin/feature-classes/{code} endpoint, sending only the two flags.
 */
function DiagramSettingsForm({ summary, onDone }: { summary: AdminFeatureClassSummary; onDone: () => void }) {
  const [isDiagramContainer, setIsDiagramContainer] = useState(summary.is_diagram_container)
  const [isDiagramPlaceable, setIsDiagramPlaceable] = useState(summary.is_diagram_placeable)
  const [geometryOptional, setGeometryOptional] = useState(summary.geometry_optional)
  // "" = inherit geometry_type (the map shape); "point"/"line" = override
  // just the diagram draw shape (migration 0053, the busbar case).
  const [diagramGeometryType, setDiagramGeometryType] = useState<string>(summary.diagram_geometry_type ?? "")
  // Admin-defined "New feature" menu grouping (migration 0074). "" clears it
  // back to uncategorised; any other string sets the section it lands under.
  const [category, setCategory] = useState<string>(summary.category ?? "")
  const [saving, setSaving] = useState(false)
  const queryClient = useQueryClient()

  // Offer the categories other classes already use as autocomplete, so an
  // admin reuses "MV plant" rather than inventing "MV Plant" and splitting
  // the menu section. Distinct, sorted, non-blank.
  const { data: classesData } = useAdminFeatureClasses()
  const categorySuggestions = Array.from(
    new Set((classesData?.feature_classes ?? []).map((c) => c.category?.trim()).filter((c): c is string => !!c)),
  ).sort((a, b) => a.localeCompare(b))

  // geometry_optional only has meaning for a class that HAS a geom column;
  // a geometry_type = 'none' class is already geometry-less by definition,
  // so the flag is hidden there (the compiler ignores it for those classes).
  const supportsGeometryOptional = summary.geometry_type !== "none"

  async function handleSave() {
    setSaving(true)
    try {
      await apiFetch(`/admin/feature-classes/${summary.code}`, {
        method: "PATCH",
        body: JSON.stringify({
          is_diagram_container: isDiagramContainer,
          is_diagram_placeable: isDiagramPlaceable,
          ...(supportsGeometryOptional ? { geometry_optional: geometryOptional } : {}),
          // Always send it: "" clears the override back to inherit, a value sets it.
          diagram_geometry_type: diagramGeometryType,
          // Always send it: "" clears the category (uncategorised), a value sets it.
          category: category.trim(),
        }),
      })
      // Refresh both the admin catalog (this page's own summary/badges) and
      // the map's active-only list, since placement eligibility changed.
      queryClient.invalidateQueries({ queryKey: ["admin", "feature-classes"] })
      queryClient.invalidateQueries({ queryKey: ["feature-classes"] })
      onDone()
    } catch (err) {
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not update diagram settings.")
    } finally {
      setSaving(false)
    }
  }

  return (
    <FormDialog
      open
      onOpenChange={(next) => next || onDone()}
      title={`Class settings — ${summary.code}`}
      onSubmit={handleSave}
      submitLabel="Save"
      canSubmit
      saving={saving}
    >
      <label className="flex items-center gap-2 text-sm">
        <input type="checkbox" checked={isDiagramContainer} onChange={(e) => setIsDiagramContainer(e.target.checked)} />
        Can host a one-line/schematic diagram (is_diagram_container)
      </label>
      <label className="flex items-center gap-2 text-sm">
        <input type="checkbox" checked={isDiagramPlaceable} onChange={(e) => setIsDiagramPlaceable(e.target.checked)} />
        Can be placed on a diagram (is_diagram_placeable)
      </label>
      {supportsGeometryOptional && (
        <label className="flex items-start gap-2 text-sm">
          <input
            type="checkbox"
            className="mt-1"
            checked={geometryOptional}
            onChange={(e) => setGeometryOptional(e.target.checked)}
          />
          <span>
            Features may exist without map geometry (geometry_optional)
            <span className="block text-xs text-muted-foreground">
              Lets a feature be diagram-only or both map + diagram. Takes effect on the next compile,
              which relaxes the geom column to nullable.
            </span>
          </span>
        </label>
      )}
      <label className="flex flex-col gap-1 text-sm">
        <span>Diagram draw shape (diagram_geometry_type)</span>
        <select
          className={inputClass}
          value={diagramGeometryType}
          onChange={(e) => setDiagramGeometryType(e.target.value)}
        >
          <option value="">Inherit map shape ({summary.geometry_type})</option>
          <option value="point">Point</option>
          <option value="line">Line (draw as a bar/wire on diagrams)</option>
        </select>
        <span className="text-xs text-muted-foreground">
          Overrides how this class is drawn on a one-line/schematic only — the geographic map is
          unchanged. Set "Line" for a busbar so a point junction reads as a spanning bar. No recompile
          needed.
        </span>
      </label>
      <label className="flex flex-col gap-1 text-sm">
        <span>Menu category</span>
        <input
          className={inputClass}
          value={category}
          onChange={(e) => setCategory(e.target.value)}
          placeholder="e.g. MV plant, LV service, Structures"
          list="feature-class-category-suggestions"
        />
        <datalist id="feature-class-category-suggestions">
          {categorySuggestions.map((c) => (
            <option key={c} value={c} />
          ))}
        </datalist>
        <span className="text-xs text-muted-foreground">
          Groups this class in the map's "New feature" menu. Classes sharing a category are listed
          together under it; leave blank to file the class under "Other". Presentation only — no
          recompile, no effect on topology.
        </span>
      </label>
    </FormDialog>
  )
}

/**
 * The always-available home for a class's diagram flags -- shows their
 * current state as badges and, for a writer, an Edit affordance that
 * works regardless of compile status (unlike the list page's inline
 * edit, which only ever renders for a draft row). See DiagramSettingsForm.
 */
function DiagramSettingsSection({ summary, canWrite }: { summary: AdminFeatureClassSummary; canWrite: boolean }) {
  const [editing, setEditing] = useState(false)

  return (
    <>
      <div className="flex items-center justify-between">
        <h2 className="text-sm font-semibold">Class settings</h2>
        {canWrite && (
          <Button variant="ghost" size="sm" onClick={() => setEditing(true)}>
            <Pencil className="size-4" />
            Edit
          </Button>
        )}
      </div>
      <div className="flex flex-wrap gap-2">
        <Badge variant={summary.category ? "success" : "outline"}>
          {summary.category ? `Menu: ${summary.category}` : "Uncategorised"}
        </Badge>
        <Badge variant={summary.is_diagram_container ? "success" : "outline"}>
          {summary.is_diagram_container ? "Hosts diagrams" : "Not a diagram container"}
        </Badge>
        <Badge variant={summary.is_diagram_placeable ? "success" : "outline"}>
          {summary.is_diagram_placeable ? "Placeable on diagrams" : "Not diagram-placeable"}
        </Badge>
        {summary.geometry_type !== "none" && (
          <Badge variant={summary.geometry_optional ? "success" : "outline"}>
            {summary.geometry_optional ? "Geometry optional" : "Geometry required"}
          </Badge>
        )}
        {summary.diagram_geometry_type && (
          <Badge variant="success">Diagram shape: {summary.diagram_geometry_type}</Badge>
        )}
      </div>
      {editing && <DiagramSettingsForm summary={summary} onDone={() => setEditing(false)} />}
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
  // Compile and every subtype/field add/edit here are metadata:write on the
  // server; this page opens on metadata:read, so gate the write controls.
  const canWrite = usePermission()("metadata:write")
  const [compiling, setCompiling] = useState(false)
  const [uncompileOpen, setUncompileOpen] = useState(false)
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
      useToastStore.getState().pushSuccess(`Compiled ${code}.`)
    } catch (err) {
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not compile this class.")
    } finally {
      setCompiling(false)
    }
  }

  // Uncompile is the destructive inverse of compile: it DROPs the physical
  // table (and its tile function) and resets the class to draft. Its reason
  // to exist is the additive compiler's blind spot -- it can't ADD a
  // required (NOT NULL) field to a class that already has rows, so the
  // table must be dropped and recompiled fresh. Confirmed first (row count
  // discarded is irreversible); on success we recompile-by-hand is the
  // user's next step, so the toast points at it.
  async function handleUncompile() {
    if (!code) return
    const res = await apiFetch<{ rows_dropped: number; table_existed: boolean }>(
      `/admin/feature-classes/${code}/uncompile`,
      { method: "POST" },
    )
    queryClient.invalidateQueries({ queryKey: ["admin", "feature-classes"] })
    queryClient.invalidateQueries({ queryKey: ["feature-classes"] })
    queryClient.invalidateQueries({ queryKey: ["feature-class-schema", code] })
    useToastStore
      .getState()
      .pushSuccess(
        res.table_existed
          ? `Uncompiled ${code} — dropped its table (${res.rows_dropped} row(s) discarded). Recompile to rebuild.`
          : `Reset ${code} to draft (no table to drop). Compile to build.`,
      )
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
              {canWrite && (
                <div className="flex items-center gap-2">
                  {/* Uncompile only makes sense once there's a physical table
                      to drop -- a draft class has none, so hide it there. */}
                  {summary && summary.status !== "draft" && (
                    <Button
                      size="sm"
                      variant="outline"
                      className="text-destructive"
                      onClick={() => setUncompileOpen(true)}
                      disabled={compiling}
                    >
                      Uncompile
                    </Button>
                  )}
                  <Button size="sm" variant="outline" onClick={handleCompile} disabled={compiling}>
                    {compiling && <Loader2 className="size-4 animate-spin" />}
                    {summary?.status === "draft" ? "Compile" : "Recompile"}
                  </Button>
                </div>
              )}
            </div>

            <ConfirmDeleteDialog
              open={uncompileOpen}
              title={`Uncompile ${code}?`}
              description="This DROPS the physical table for this class (and its map tile function) and resets it to draft. Every feature row in the table is permanently discarded — this cannot be undone. All other metadata (fields, subtypes, symbology, connectivity rules) is kept, so you can Recompile to rebuild an empty table. Use this to add a required field to a class that already has data."
              confirmLabel="Uncompile"
              onOpenChange={setUncompileOpen}
              onConfirm={handleUncompile}
            />

            {summary && <DiagramSettingsSection summary={summary} canWrite={canWrite} />}

            <div className="flex items-center justify-between">
              <h2 className="text-sm font-semibold">Subtypes</h2>
              {canWrite && (
                <Button variant="ghost" size="sm" onClick={() => setAddingSubtype(true)}>
                  <Plus className="size-4" />
                  Add subtype
                </Button>
              )}
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
              {canWrite && (
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
              )}
            </div>
            {addingField && (
              <AddFieldRow code={code} subtypes={schema.subtypes} onDone={() => setAddingField(false)} />
            )}
            {editingFieldName &&
              (() => {
                const editingField = schema.fields.find((f) => f.name === editingFieldName)
                return editingField ? (
                  <EditFieldRow
                    code={code}
                    field={editingField}
                    subtypes={schema.subtypes}
                    onDone={() => setEditingFieldName(null)}
                  />
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
                          {canWrite && (
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
                          )}
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
                  {schema.terminals.map((t, i) => (
                    <span key={`${t.code}:${t.subtype_code ?? "any"}:${i}`} className="bg-muted text-muted-foreground rounded-md px-1.5 py-0.5 text-xs">
                      {t.code} ({t.role}
                      {t.subtype_code !== undefined ? `, subtype ${t.subtype_code}` : ""})
                    </span>
                  ))}
                </div>
              </>
            )}

            <SymbologySection code={code} schema={schema} />

            <LabelSection code={code} schema={schema} />

            <h2 className="text-sm font-semibold">Rules</h2>
            <RulesSection code={code} />
          </>
        )}
      </div>
    </div>
  )
}
