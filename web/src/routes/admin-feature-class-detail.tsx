import { useEffect, useMemo, useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { AlertTriangle, ArrowLeft, Copy, FlaskConical, LandPlot, ListPlus, Loader2, Pencil, Plus, Power, PowerOff, Trash2, Wrench } from "lucide-react"
import { Link, useNavigate, useParams } from "react-router"

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
import {
  useClassAnalysisStudies,
  useSetClassAnalysisStudies,
} from "@/hooks/use-class-analysis-studies"
import {
  useClassTerritoryAvailability,
  useSetClassTerritoryAvailability,
} from "@/hooks/use-class-territory-availability"
import { useFeatureClassRules } from "@/hooks/use-feature-class-rules"
import { useTerritories } from "@/hooks/use-territories"
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
// by 0075 for protection, 0080 for metering_sensing, 0082 for cable_routing,
// 0083 for panel_bay and 0121 for metering_accounting) and the API's
// validRelationshipKinds in
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
  "metering_accounting",
] as const
const CARDINALITIES = ["one_to_one", "one_to_many", "many_to_many"] as const
// The SNAP_TARGET_TYPE domain values (seed/snapping_rules_example.sql):
// point_feature (snap to a whole point feature), endpoint (a line's end),
// edge (anywhere along a line), vertex (an interior shape vertex).
const SNAP_TARGET_TYPES = ["point_feature", "endpoint", "edge", "vertex"] as const

type RuleKind = "network" | "relationship" | "snapping"

// A RuleSeed pre-fills the matching add form with an existing rule's values so an
// admin can save a COPY under a new name. The rule APIs are create-only (no edit),
// so a copy is authored as a new row, then tweaked. Named kinds (network,
// relationship) seed the name as "<name> copy"; snapping rules have no name column,
// so a copy is the same pairing to then adjust (tolerance/priority/class/terminal) --
// an unchanged copy would just be a duplicate the dup-detector flags.
type NetworkRuleSeed = {
  name: string
  fromClass: string
  fromTerminal: string
  toClass: string
  toTerminal: string
  phaseRule: string
  isValid: boolean
}
type RelationshipRuleSeed = {
  name: string
  originClass: string
  destinationClass: string
  kind: string
  cardinality: string
}
type SnappingRuleSeed = {
  fromClass: string
  toClass: string
  targetType: string
  toleranceM: string
  priority: string
  isRequired: boolean
}
type RuleSeed =
  | ({ section: "network" } & NetworkRuleSeed)
  | ({ section: "relationship" } & RelationshipRuleSeed)
  | ({ section: "snapping" } & SnappingRuleSeed)

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
  // Which section's "remove duplicates" confirm is open, and which is mid-delete.
  // Per-kind (not a single boolean) so Network / Relationship / Snapping each get
  // their own badge, button, and confirm.
  const [confirmCleanKind, setConfirmCleanKind] = useState<RuleKind | null>(null)
  const [cleaningKind, setCleaningKind] = useState<RuleKind | null>(null)
  // "Duplicate" opens the add form pre-filled from an existing rule (seed); "Add"
  // opens it blank. formNonce bumps on every open so the form REMOUNTS and re-reads
  // the seed even when it was already open (its field state is initialized once, at
  // mount, from the seed).
  const [seed, setSeed] = useState<RuleSeed | null>(null)
  const [formNonce, setFormNonce] = useState(0)
  const openAdd = (kind: RuleKind) => {
    setSeed(null)
    setFormNonce((n) => n + 1)
    setAdding(kind)
  }
  const openDuplicate = (s: RuleSeed) => {
    setSeed(s)
    setFormNonce((n) => n + 1)
    setAdding(s.section)
  }
  const closeForm = () => {
    setAdding(null)
    setSeed(null)
  }

  const classes = classesData?.feature_classes ?? []

  // Flag redundant rules in each section so an admin can prune them. Two rules
  // are "the same" when they authorize the identical thing; we build a canonical
  // key per rule, group by it, and in every group of 2+ keep the first and mark
  // the rest for removal. `flaggedIds` are all members of a 2+ group (highlighted);
  // `removalIds` are the redundant extras -- what a cleanup would delete.
  //
  //   - Network: matching runs in EITHER direction (matchRuleEitherDirection), so
  //     "A T2 -> B T1" and "B T1 -> A T2 (incoming)" are the same rule -- the two
  //     endpoints (each = class:subtype:terminal) are sorted into an ORDER-
  //     INDEPENDENT key, plus phase_rule + is_valid.
  //   - Relationship: origin and destination are DISTINCT roles (a duct carries a
  //     conductor, not vice-versa), so the key is order-DEPENDENT on
  //     origin|destination|kind|cardinality.
  //   - Snapping: from (source) and to (target) are DISTINCT roles, so the key is
  //     the full ordered tuple (direction, from, to+terminal, target_type,
  //     tolerance, priority, required). Only rows identical on all of it collapse.
  const dups = useMemo(() => {
    const group = (rows: { id: string; key: string }[]) => {
      const byKey = new Map<string, string[]>()
      for (const { id, key } of rows) byKey.set(key, [...(byKey.get(key) ?? []), id])
      const flaggedIds = new Set<string>()
      const removalIds = new Set<string>()
      for (const ids of byKey.values()) {
        if (ids.length > 1) {
          ids.forEach((id) => flaggedIds.add(id))
          ids.slice(1).forEach((id) => removalIds.add(id)) // keep the first, drop the rest
        }
      }
      return { flaggedIds, removalIds }
    }

    const network = group(
      (data?.network_rules ?? []).map((r) => {
        const epA = `${r.from_class}:${r.from_subtype ?? ""}:${r.from_terminal ?? ""}`
        const epB = `${r.to_class}:${r.to_subtype ?? ""}:${r.to_terminal ?? ""}`
        return { id: r.id, key: `${[epA, epB].sort().join("|")}|${r.phase_rule ?? ""}|${r.is_valid}` }
      }),
    )
    const relationship = group(
      (data?.relationship_rules ?? []).map((r) => ({
        id: r.id,
        key: `${r.origin_class}|${r.destination_class}|${r.kind}|${r.cardinality}`,
      })),
    )
    const snapping = group(
      (data?.snapping_rules ?? []).map((r) => ({
        id: r.id,
        key: [
          r.direction,
          r.from_class,
          r.from_subtype ?? "",
          r.to_class,
          r.to_subtype ?? "",
          r.to_terminal ?? "",
          r.target_type,
          r.tolerance_m,
          r.priority,
          r.is_required,
        ].join("|"),
      })),
    )
    return { network, relationship, snapping }
  }, [data?.network_rules, data?.relationship_rules, data?.snapping_rules])

  // Per-kind REST collection for the batched delete, and the noun used in copy.
  const dupEndpoint: Record<RuleKind, string> = {
    network: "network-rules",
    relationship: "relationship-classes",
    snapping: "snapping-rules",
  }
  const dupNoun: Record<RuleKind, string> = {
    network: "network rule",
    relationship: "relationship rule",
    snapping: "snapping rule",
  }

  // Delete every redundant rule of one kind via the same per-rule DELETE the
  // trash button uses, throttled to a few in flight so a large prune doesn't
  // fire as one burst. Failures are counted, not fatal -- report how many went,
  // then refetch to show the truth.
  async function runCleanup(kind: RuleKind) {
    setCleaningKind(kind)
    const ids = [...dups[kind].removalIds]
    let failed = 0
    const batchSize = 6
    for (let i = 0; i < ids.length; i += batchSize) {
      const batch = ids.slice(i, i + batchSize)
      const results = await Promise.allSettled(
        batch.map((id) => apiFetch(`/admin/${dupEndpoint[kind]}/${id}`, { method: "DELETE" })),
      )
      failed += results.filter((res) => res.status === "rejected").length
    }
    queryClient.invalidateQueries({ queryKey: ["feature-class-rules", code] })
    setCleaningKind(null)
    const removed = ids.length - failed
    if (failed > 0) {
      useToastStore
        .getState()
        .pushWarning(`Removed ${removed} duplicate${removed === 1 ? "" : "s"}; ${failed} could not be deleted.`)
    } else {
      useToastStore.getState().pushSuccess(`Removed ${removed} duplicate ${dupNoun[kind]}${removed === 1 ? "" : "s"}.`)
    }
  }

  // The badge + "Remove N duplicates" button shared by all three section headers.
  const dupBadge = (kind: RuleKind) =>
    dups[kind].removalIds.size > 0 ? (
      <span className="inline-flex items-center gap-1 rounded-full bg-amber-500/15 px-2 py-0.5 text-xs font-medium text-amber-600 dark:text-amber-400">
        <AlertTriangle className="size-3" />
        {dups[kind].removalIds.size} duplicate{dups[kind].removalIds.size === 1 ? "" : "s"}
      </span>
    ) : null

  const dupRemoveButton = (kind: RuleKind) =>
    canWrite && dups[kind].removalIds.size > 0 ? (
      <Button
        variant="outline"
        size="sm"
        className="h-7 gap-1 border-amber-500/40 text-xs text-amber-600 hover:bg-amber-500/10 dark:text-amber-400"
        disabled={cleaningKind !== null}
        onClick={() => setConfirmCleanKind(kind)}
      >
        {cleaningKind === kind ? <Loader2 className="size-3.5 animate-spin" /> : <Trash2 className="size-3.5" />}
        Remove {dups[kind].removalIds.size} duplicate{dups[kind].removalIds.size === 1 ? "" : "s"}
      </Button>
    ) : null

  // Count backing the open cleanup confirm's copy (0 while none is open).
  const cleanCount = confirmCleanKind ? dups[confirmCleanKind].removalIds.size : 0

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
          <div className="flex items-center gap-2">
            <h3 className="text-muted-foreground text-xs font-medium">Network</h3>
            {dupBadge("network")}
          </div>
          <div className="flex items-center gap-2">
            {dupRemoveButton("network")}
            {canWrite && (
              <Button variant="outline" size="sm" className="h-7 gap-1 text-xs" onClick={() => openAdd("network")}>
                <Plus className="size-3.5" />
                Add network rule
              </Button>
            )}
          </div>
        </div>
        {data.network_rules.length > 0 ? (
          <div className="max-h-80 overflow-y-auto rounded-lg border">
            <table className="w-full text-sm">
              <thead className="bg-muted text-muted-foreground sticky top-0 z-10 text-left text-xs">
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
                  <tr
                    key={r.id}
                    className={`border-t ${dups.network.flaggedIds.has(r.id) ? "bg-amber-500/10" : ""}`}
                  >
                    <td className="px-3 py-2">
                      <span className="flex items-center gap-1.5">
                        {dups.network.flaggedIds.has(r.id) && (
                          <AlertTriangle
                            className="size-3.5 shrink-0 text-amber-500"
                            aria-label="Duplicate rule"
                          />
                        )}
                        {r.name}
                      </span>
                    </td>
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
                        <div className="flex items-center justify-end gap-0.5">
                          <Button
                            variant="ghost"
                            size="icon"
                            className="size-7"
                            title="Duplicate"
                            onClick={() =>
                              openDuplicate({
                                section: "network",
                                name: `${r.name} copy`,
                                fromClass: r.from_class,
                                fromTerminal: r.from_terminal ?? "",
                                toClass: r.to_class,
                                toTerminal: r.to_terminal ?? "",
                                phaseRule: r.phase_rule ?? "",
                                isValid: r.is_valid,
                              })
                            }
                          >
                            <Copy className="size-4" />
                          </Button>
                          <Button
                            variant="ghost"
                            size="icon"
                            className="text-destructive size-7"
                            title="Delete"
                            onClick={() => setDeleteTarget({ kind: "network", id: r.id, name: r.name })}
                          >
                            <Trash2 className="size-4" />
                          </Button>
                        </div>
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
          <div className="flex items-center gap-2">
            <h3 className="text-muted-foreground text-xs font-medium">Relationship</h3>
            {dupBadge("relationship")}
          </div>
          <div className="flex items-center gap-2">
            {dupRemoveButton("relationship")}
            {canWrite && (
              <Button
                variant="outline"
                size="sm"
                className="h-7 gap-1 text-xs"
                onClick={() => openAdd("relationship")}
              >
                <Plus className="size-3.5" />
                Add relationship
              </Button>
            )}
          </div>
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
                  <tr
                    key={r.id}
                    className={`border-t ${dups.relationship.flaggedIds.has(r.id) ? "bg-amber-500/10" : ""}`}
                  >
                    <td className="px-3 py-2">
                      <span className="flex items-center gap-1.5">
                        {dups.relationship.flaggedIds.has(r.id) && (
                          <AlertTriangle className="size-3.5 shrink-0 text-amber-500" aria-label="Duplicate rule" />
                        )}
                        {r.name}
                      </span>
                    </td>
                    <td className="px-3 py-2">{r.direction === "origin" ? "Origin" : "Destination"}</td>
                    <td className="px-3 py-2 font-mono text-xs">{r.origin_class}</td>
                    <td className="px-3 py-2 font-mono text-xs">{r.destination_class}</td>
                    <td className="px-3 py-2 text-xs">{r.kind}</td>
                    <td className="px-3 py-2 text-xs">{r.cardinality}</td>
                    {canWrite && (
                      <td className="px-3 py-2 text-right">
                        <div className="flex items-center justify-end gap-0.5">
                          <Button
                            variant="ghost"
                            size="icon"
                            className="size-7"
                            title="Duplicate"
                            onClick={() =>
                              openDuplicate({
                                section: "relationship",
                                name: `${r.name} copy`,
                                originClass: r.origin_class,
                                destinationClass: r.destination_class,
                                kind: r.kind,
                                cardinality: r.cardinality,
                              })
                            }
                          >
                            <Copy className="size-4" />
                          </Button>
                          <Button
                            variant="ghost"
                            size="icon"
                            className="text-destructive size-7"
                            title="Delete"
                            onClick={() => setDeleteTarget({ kind: "relationship", id: r.id, name: r.name })}
                          >
                            <Trash2 className="size-4" />
                          </Button>
                        </div>
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
          <div className="flex items-center gap-2">
            <h3 className="text-muted-foreground text-xs font-medium">Snapping</h3>
            {dupBadge("snapping")}
          </div>
          <div className="flex items-center gap-2">
            {dupRemoveButton("snapping")}
            {canWrite && (
              <Button variant="outline" size="sm" className="h-7 gap-1 text-xs" onClick={() => openAdd("snapping")}>
                <Plus className="size-3.5" />
                Add snapping rule
              </Button>
            )}
          </div>
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
                  <tr
                    key={r.id}
                    className={`border-t ${dups.snapping.flaggedIds.has(r.id) ? "bg-amber-500/10" : ""}`}
                  >
                    <td className="px-3 py-2">
                      <span className="flex items-center gap-1.5">
                        {dups.snapping.flaggedIds.has(r.id) && (
                          <AlertTriangle className="size-3.5 shrink-0 text-amber-500" aria-label="Duplicate rule" />
                        )}
                        {r.direction === "from" ? "As source" : "As target"}
                      </span>
                    </td>
                    <td className="px-3 py-2 font-mono text-xs">{r.from_class}</td>
                    <td className="px-3 py-2 font-mono text-xs">{r.to_class}</td>
                    <td className="px-3 py-2 text-xs">{r.target_type}</td>
                    <td className="px-3 py-2 text-xs">{r.tolerance_m}m</td>
                    <td className="px-3 py-2 text-xs">{r.priority}</td>
                    <td className="px-3 py-2">{r.is_required ? "yes" : "no"}</td>
                    {canWrite && (
                      <td className="px-3 py-2 text-right">
                        <div className="flex items-center justify-end gap-0.5">
                          <Button
                            variant="ghost"
                            size="icon"
                            className="size-7"
                            title="Duplicate (snapping rules have no name — adjust before saving)"
                            onClick={() =>
                              openDuplicate({
                                section: "snapping",
                                fromClass: r.from_class,
                                toClass: r.to_class,
                                targetType: r.target_type,
                                toleranceM: String(r.tolerance_m),
                                priority: String(r.priority),
                                isRequired: r.is_required,
                              })
                            }
                          >
                            <Copy className="size-4" />
                          </Button>
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
                        </div>
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

      {adding === "network" && (
        <AddNetworkRuleForm
          key={formNonce}
          code={code}
          classes={classes}
          seed={seed?.section === "network" ? seed : undefined}
          onDone={closeForm}
        />
      )}
      {adding === "relationship" && (
        <AddRelationshipForm
          key={formNonce}
          code={code}
          classes={classes}
          seed={seed?.section === "relationship" ? seed : undefined}
          onDone={closeForm}
        />
      )}
      {adding === "snapping" && (
        <AddSnappingRuleForm
          key={formNonce}
          code={code}
          classes={classes}
          seed={seed?.section === "snapping" ? seed : undefined}
          onDone={closeForm}
        />
      )}

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

      <ConfirmDeleteDialog
        open={confirmCleanKind !== null}
        title={`Remove ${cleanCount} duplicate ${confirmCleanKind ? dupNoun[confirmCleanKind] : "rule"}${
          cleanCount === 1 ? "" : "s"
        }?`}
        description={
          confirmCleanKind === "relationship"
            ? "Each set of identical rules keeps one copy; the redundant duplicates are deleted. No legal pairing is lost -- the surviving copy still authorizes it, and relationships already established stay. This can't be undone."
            : confirmCleanKind === "snapping"
              ? "Each set of identical rules keeps one copy; the redundant duplicates are deleted. No snap target is lost -- the surviving copy still offers it, and nothing already drawn changes. This can't be undone."
              : "Each set of identical rules keeps one copy; the redundant duplicates are deleted. No legal connection is lost -- the surviving copy still authorizes each pairing, and connections already established stay. This can't be undone."
        }
        confirmLabel={`Remove ${cleanCount} duplicate${cleanCount === 1 ? "" : "s"}`}
        onOpenChange={(open) => !open && setConfirmCleanKind(null)}
        onConfirm={async () => {
          if (!confirmCleanKind) return
          await runCleanup(confirmCleanKind)
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
  seed,
  onDone,
}: {
  code: string
  classes: AdminFeatureClassSummary[]
  seed?: NetworkRuleSeed
  onDone: () => void
}) {
  const [name, setName] = useState(seed?.name ?? "")
  const [fromClass, setFromClass] = useState(seed?.fromClass ?? code)
  const [fromTerminal, setFromTerminal] = useState(seed?.fromTerminal ?? "")
  const [toClass, setToClass] = useState(seed?.toClass ?? "")
  const [toTerminal, setToTerminal] = useState(seed?.toTerminal ?? "")
  const [phaseRule, setPhaseRule] = useState(seed?.phaseRule ?? "")
  const [isValid, setIsValid] = useState(seed?.isValid ?? true)
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
  seed,
  onDone,
}: {
  code: string
  classes: AdminFeatureClassSummary[]
  seed?: RelationshipRuleSeed
  onDone: () => void
}) {
  const [name, setName] = useState(seed?.name ?? "")
  const [originClass, setOriginClass] = useState(seed?.originClass ?? code)
  const [destinationClass, setDestinationClass] = useState(seed?.destinationClass ?? "")
  const [kind, setKind] = useState<(typeof RELATIONSHIP_KINDS)[number]>(
    (seed?.kind as (typeof RELATIONSHIP_KINDS)[number]) ?? "containment",
  )
  const [cardinality, setCardinality] = useState<(typeof CARDINALITIES)[number]>(
    (seed?.cardinality as (typeof CARDINALITIES)[number]) ?? "one_to_many",
  )
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
  seed,
  onDone,
}: {
  code: string
  classes: AdminFeatureClassSummary[]
  seed?: SnappingRuleSeed
  onDone: () => void
}) {
  const [fromClass, setFromClass] = useState(seed?.fromClass ?? code)
  const [toClass, setToClass] = useState(seed?.toClass ?? "")
  const [targetType, setTargetType] = useState<(typeof SNAP_TARGET_TYPES)[number]>(
    (seed?.targetType as (typeof SNAP_TARGET_TYPES)[number]) ?? "point_feature",
  )
  const [toleranceM, setToleranceM] = useState(seed?.toleranceM ?? "0.5")
  const [priority, setPriority] = useState(seed?.priority ?? "1")
  const [isRequired, setIsRequired] = useState(seed?.isRequired ?? false)
  const [saving, setSaving] = useState(false)
  const queryClient = useQueryClient()

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
            <select className={inputClass} value={toClass} onChange={(e) => setToClass(e.target.value)}>
              <option value="">Select a class…</option>
              {classes.map((c) => (
                <option key={c.code} value={c.code}>
                  {c.code}
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
            <label className={labelClass}>Apply rule from zoom</label>
            <input
              type="number"
              className={inputClass}
              value={minZoom}
              onChange={(e) => setMinZoom(e.target.value)}
              placeholder="any"
            />
          </div>
          <div className="flex flex-col gap-1">
            <label className={labelClass}>Apply rule to zoom</label>
            <input
              type="number"
              className={inputClass}
              value={maxZoom}
              onChange={(e) => setMaxZoom(e.target.value)}
              placeholder="any"
            />
          </div>
          <p className="text-muted-foreground col-span-full text-xs">
            Limits the zoom range this <em>style</em> applies over — outside it another rule (or the
            default) still paints the feature. This does not hide anything; to hide the class at some
            zooms use “Display zoom” instead.
          </p>
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

/**
 * The per-class map display-zoom band (migration 0090) -- a min and/or max
 * zoom outside which the whole feature is hidden on the geographic map (glyph
 * and label alike), not merely restyled. One config per class, stored as two
 * columns on the feature class and edited here like Labelling. Map only; the
 * diagram one-line always shows. Distinct from a symbology rule's own zoom band
 * (which picks a style), so it lives in its own section rather than the rule
 * editor.
 */
function DisplayZoomSection({ code, schema }: { code: string; schema: FeatureClassSchema }) {
  const canWrite = usePermission()("metadata:write")
  const [editing, setEditing] = useState(false)
  const min = schema.min_display_zoom
  const max = schema.max_display_zoom
  const hasBand = min !== undefined || max !== undefined
  const summary = hasBand ? `Visible zoom ${min ?? "0"}…${max ?? "max"}` : "Visible at every zoom"

  return (
    <>
      <div className="flex items-center justify-between">
        <h2 className="text-sm font-semibold">Display zoom</h2>
        {canWrite && (
          <Button variant="ghost" size="sm" onClick={() => setEditing(true)}>
            <Pencil className="size-4" />
            {hasBand ? "Edit range" : "Set range"}
          </Button>
        )}
      </div>

      <p className={hasBand ? "text-sm" : "text-muted-foreground text-sm"}>{summary}</p>

      {editing && <DisplayZoomForm code={code} schema={schema} onDone={() => setEditing(false)} />}
    </>
  )
}

function DisplayZoomForm({ code, schema, onDone }: { code: string; schema: FeatureClassSchema; onDone: () => void }) {
  const queryClient = useQueryClient()
  const [minZoom, setMinZoom] = useState(schema.min_display_zoom !== undefined ? String(schema.min_display_zoom) : "")
  const [maxZoom, setMaxZoom] = useState(schema.max_display_zoom !== undefined ? String(schema.max_display_zoom) : "")
  const [saving, setSaving] = useState(false)

  const minNum = minZoom.trim() === "" ? undefined : Number(minZoom)
  const maxNum = maxZoom.trim() === "" ? undefined : Number(maxZoom)
  const minValid = minNum === undefined || (Number.isInteger(minNum) && minNum >= 0 && minNum <= 24)
  const maxValid = maxNum === undefined || (Number.isInteger(maxNum) && maxNum >= 0 && maxNum <= 24)
  const bandValid = minNum === undefined || maxNum === undefined || minNum <= maxNum
  const canSave = minValid && maxValid && bandValid

  async function handleSave() {
    setSaving(true)
    try {
      // Sentinel-encoded PATCH (see updateFeatureClassRequest): both sides are
      // always sent so either can be cleared; an empty input becomes -1, which
      // the server writes as SQL NULL (no bound), any 0..24 sets the bound.
      const body = { min_display_zoom: minNum ?? -1, max_display_zoom: maxNum ?? -1 }
      await apiFetch(`/admin/feature-classes/${code}`, { method: "PATCH", body: JSON.stringify(body) })
      queryClient.invalidateQueries({ queryKey: ["feature-class-schema", code] })
      onDone()
    } catch (err) {
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not save the display zoom.")
    } finally {
      setSaving(false)
    }
  }

  return (
    <FormDialog
      open
      onOpenChange={(next) => next || onDone()}
      title="Map display zoom"
      description="Hide this class's features on the geographic map outside a zoom range. Leave a bound empty for no limit; clear both to show at every zoom. This hides the whole feature (glyph and label), unlike a symbology rule's own zoom band."
      onSubmit={handleSave}
      submitLabel="Save"
      canSubmit={canSave}
      saving={saving}
    >
      <div className="flex flex-wrap items-end gap-3">
        <div className="flex flex-col gap-1">
          <label className={labelClass}>Min zoom</label>
          <input
            type="number"
            min="0"
            max="24"
            step="1"
            className={`${inputClass} w-24`}
            value={minZoom}
            onChange={(e) => setMinZoom(e.target.value)}
            placeholder="any"
          />
        </div>
        <div className="flex flex-col gap-1">
          <label className={labelClass}>Max zoom</label>
          <input
            type="number"
            min="0"
            max="24"
            step="1"
            className={`${inputClass} w-24`}
            value={maxZoom}
            onChange={(e) => setMaxZoom(e.target.value)}
            placeholder="any"
          />
        </div>
      </div>
      {!bandValid && <p className="text-destructive text-xs">Min zoom must not exceed max zoom.</p>}
      {(!minValid || !maxValid) && <p className="text-destructive text-xs">Zoom levels must be whole numbers from 0 to 24.</p>}
    </FormDialog>
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

      {/* Placement -- shifts/spins the label for every feature of the class. Offset X
          and rotation apply to point/polygon labels only; a line label rides the
          stroke but now honours Offset Y to lift off it (negative = above the line,
          positive = below). Offset Y blank = the point default (14px drop below the
          glyph). */}
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
        <span className="text-muted-foreground text-xs">
          Offset X &amp; rotation apply to point/polygon labels. Offset Y also lifts a line label off the stroke —
          negative sits it above the line, positive below.
        </span>
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
 * The functional_role vocabulary (migration 0091) offered in the class-settings
 * editor. Each is an ELECTRICAL function on the traced network, orthogonal to
 * the structural network_role. "source" is intentionally offered even though no
 * class carries it today -- it's reserved for a future generation/grid-infeed
 * class (today's source is an instance-level slack transformer, not a class).
 */
const FUNCTIONAL_ROLE_OPTIONS: { value: string; label: string }[] = [
  { value: "source", label: "Source — injects power (generation / grid infeed)" },
  { value: "consumer", label: "Consumer — load / sink (service point, meter)" },
  { value: "passthrough", label: "Passthrough — carries flow, no state (conductor)" },
  { value: "transformer", label: "Transformer — voltage/circuit boundary" },
  { value: "controller", label: "Controller — open/closed state gates flow (switch, breaker)" },
  { value: "node", label: "Node — many-way meeting point (busbar)" },
  { value: "sensor", label: "Sensor — observes, off-path (CT/VT, relay)" },
  { value: "ground", label: "Ground — return/reference path (NER, earthing transformer)" },
]

/**
 * The trace_tier scale (migration 0126) offered in the class-settings editor --
 * a class's rank in the vertical source-to-load hierarchy, in hundreds with gaps
 * of 100 so a new layer can slot between two existing ones later. A directional
 * trace steps within or deeper into the hierarchy but never back toward the end
 * it came from; leaving a class untiered makes it transparent to that guard.
 */
const TRACE_TIER_OPTIONS: { value: number; label: string }[] = [
  { value: 0, label: "0 — Source / grid infeed" },
  { value: 200, label: "200 — Sub-transmission / POWER_XFMR (HV→MV)" },
  { value: 300, label: "300 — MV (busbar, switchgear, conductor, switch)" },
  { value: 400, label: "400 — DIST_XFMR (MV→LV boundary)" },
  { value: 500, label: "500 — LV (busbar, pillar, conductor, fuse)" },
  { value: 600, label: "600 — Service point / meter" },
]

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
  // Migration 0091: the class's electrical FUNCTION on the traced network,
  // orthogonal to network_role. "" clears it to unclassified (non-electrical);
  // a value stamps the role the supply-path / section / isolate traces read.
  const [functionalRole, setFunctionalRole] = useState<string>(summary.functional_role ?? "")
  // Migration 0126: the class's tier in the source-to-load hierarchy. "" =
  // untiered (transparent to the trace cross-layer guard); a value stamps the
  // rank. Sent sentinel-encoded on save (-1 clears back to NULL).
  const [traceTier, setTraceTier] = useState<string>(
    summary.trace_tier !== undefined && summary.trace_tier !== null ? String(summary.trace_tier) : "",
  )
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
          // Always send it: "" clears functional_role to NULL (unclassified),
          // a value stamps the electrical role. Pure metadata -- no recompile.
          functional_role: functionalRole,
          // Sentinel-encoded like the display-zoom band: -1 clears trace_tier
          // back to NULL (untiered), a non-negative value sets the rank.
          trace_tier: traceTier === "" ? -1 : Number(traceTier),
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
      <label className="flex flex-col gap-1 text-sm">
        <span>Electrical role (functional_role)</span>
        <select
          className={inputClass}
          value={functionalRole}
          onChange={(e) => setFunctionalRole(e.target.value)}
        >
          <option value="">Unclassified (non-electrical)</option>
          {FUNCTIONAL_ROLE_OPTIONS.map((r) => (
            <option key={r.value} value={r.value}>
              {r.label}
            </option>
          ))}
        </select>
        <span className="text-xs text-muted-foreground">
          The class's electrical FUNCTION on the traced network — what the supply-path, section and
          isolation traces read to treat it as a transformer boundary, a switching device, an
          off-path sensor, etc. Orthogonal to the structural network role ({summary.network_role}).
          Leave unclassified for non-electrical classes (containers, structures, civil). No recompile.
        </span>
      </label>
      <label className="flex flex-col gap-1 text-sm">
        <span>Network tier (trace_tier)</span>
        <select className={inputClass} value={traceTier} onChange={(e) => setTraceTier(e.target.value)}>
          <option value="">Untiered (transparent to the trace guard)</option>
          {TRACE_TIER_OPTIONS.map((t) => (
            <option key={t.value} value={String(t.value)}>
              {t.label}
            </option>
          ))}
        </select>
        <span className="text-xs text-muted-foreground">
          The class's rank in the source-to-load hierarchy (lower = closer to the grid infeed). A
          downstream trace may step to an equal-or-higher tier but never a strictly-lower one (that's
          climbing back toward the source); upstream mirrors it. Leaving a class untiered makes it
          transparent — never tier-blocked. No recompile.
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
        <Badge variant={summary.functional_role ? "success" : "outline"}>
          {summary.functional_role ? `Role: ${summary.functional_role}` : "No electrical role"}
        </Badge>
        <Badge variant={summary.trace_tier !== undefined && summary.trace_tier !== null ? "success" : "outline"}>
          {summary.trace_tier !== undefined && summary.trace_tier !== null
            ? `Tier: ${summary.trace_tier}`
            : "Untiered"}
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
/**
 * Per-territory availability (Slice 2 of class deactivation): tick a territory to
 * turn this class OFF for new features there and everywhere beneath it (the
 * disable cascades down the tree server-side -- crud.classDisabledForTerritory).
 * Untick = available; an empty set means available in every territory. Enforced
 * at create time (crud.enforceClassAvailability); existing features are grandfathered.
 */
function TerritoryAvailabilitySection({ code, canWrite }: { code: string; canWrite: boolean }) {
  const [open, setOpen] = useState(false)
  const { data: territoriesData } = useTerritories()
  const { data } = useClassTerritoryAvailability(code)
  const setAvail = useSetClassTerritoryAvailability(code)
  const [disabled, setDisabled] = useState<Set<string> | null>(null)
  // Re-seed each time the dialog opens (or the saved set changes) so it always
  // starts from the stored state and a Cancel discards cleanly.
  useEffect(() => {
    if (open && data) setDisabled(new Set(data.disabled_territory_ids))
  }, [open, data])

  const territories = useMemo(
    () =>
      [...(territoriesData?.territories ?? [])].sort(
        (a, b) => a.level_rank - b.level_rank || a.name.localeCompare(b.name),
      ),
    [territoriesData],
  )
  const current = disabled ?? new Set<string>()
  const serverSet = useMemo(() => new Set(data?.disabled_territory_ids ?? []), [data])
  const dirty =
    disabled != null && (disabled.size !== serverSet.size || [...disabled].some((id) => !serverSet.has(id)))
  const offCount = serverSet.size

  function toggle(id: string, checked: boolean) {
    setDisabled((prev) => {
      const next = new Set(prev ?? [])
      if (checked) next.add(id)
      else next.delete(id)
      return next
    })
  }
  function save() {
    setAvail.mutate([...current], {
      onSuccess: () => {
        useToastStore.getState().pushSuccess("Territory availability updated.")
        setOpen(false)
      },
      onError: (err) =>
        useToastStore
          .getState()
          .pushError(err instanceof ApiError ? err.message : "Could not save territory availability."),
    })
  }

  return (
    <>
      <Button variant="outline" className="w-full justify-between" onClick={() => setOpen(true)}>
        <span className="flex items-center gap-2">
          <LandPlot className="size-4" />
          Territory availability
        </span>
        {offCount > 0 ? (
          <Badge variant="secondary">{offCount} off</Badge>
        ) : (
          <span className="text-muted-foreground text-xs font-normal">available everywhere</span>
        )}
      </Button>

      <FormDialog
        open={open}
        onOpenChange={setOpen}
        title="Territory availability"
        description={
          <>
            Tick a territory to turn <span className="font-mono">{code}</span> <b>off</b> there — new features
            can't be created in it (or any territory beneath it). Unticked = available; none ticked = available
            everywhere. Existing features are kept.
          </>
        }
        onSubmit={save}
        submitLabel="Save"
        canSubmit={canWrite && dirty}
        saving={setAvail.isPending}
      >
        {territories.length === 0 ? (
          <p className="text-muted-foreground text-sm">No territories defined.</p>
        ) : (
          <div className="flex flex-col gap-0.5">
            {territories.map((t) => (
              <label
                key={t.id}
                className="hover:bg-muted/50 flex items-center gap-2 rounded px-1.5 py-1 text-sm"
                style={{ paddingLeft: `${0.375 + t.level_rank}rem` }}
              >
                <input
                  type="checkbox"
                  checked={current.has(t.id)}
                  disabled={!canWrite}
                  onChange={(e) => toggle(t.id, e.target.checked)}
                />
                <span>{t.name}</span>
                <span className="text-muted-foreground text-xs">{t.level_name}</span>
              </label>
            ))}
          </div>
        )}
      </FormDialog>
    </>
  )
}

/**
 * Which engineering / network analysis studies a class offers. Applicability is
 * code-defined by role (api/internal/studies) -- only studies the role makes
 * applicable can be toggled; the rest never appear. Unticking one prunes it from
 * the feature panel's analysis menu for every feature of this class.
 */
const STUDY_CATEGORIES: { key: string; label: string }[] = [
  { key: "loadflow", label: "Load flow" },
  { key: "fault", label: "Fault & protection" },
  { key: "capacity", label: "Capacity & planning" },
]

function AnalysisStudiesSection({ code, canWrite }: { code: string; canWrite: boolean }) {
  const [open, setOpen] = useState(false)
  const { data } = useClassAnalysisStudies(code)
  const setStudies = useSetClassAnalysisStudies(code)
  const [disabled, setDisabled] = useState<Set<string> | null>(null)
  useEffect(() => {
    if (open && data) setDisabled(new Set(data.studies.filter((s) => s.disabled).map((s) => s.id)))
  }, [open, data])

  const applicable = useMemo(() => (data?.studies ?? []).filter((s) => s.applicable), [data])
  const current = disabled ?? new Set<string>()
  const serverDisabled = useMemo(
    () => new Set((data?.studies ?? []).filter((s) => s.disabled).map((s) => s.id)),
    [data],
  )
  const dirty =
    disabled != null && (disabled.size !== serverDisabled.size || [...disabled].some((id) => !serverDisabled.has(id)))
  const serverOn = applicable.filter((s) => !s.disabled).length

  function toggle(id: string, enabled: boolean) {
    setDisabled((prev) => {
      const next = new Set(prev ?? [])
      if (enabled) next.delete(id)
      else next.add(id)
      return next
    })
  }
  function save() {
    setStudies.mutate([...current], {
      onSuccess: () => {
        useToastStore.getState().pushSuccess("Analysis studies updated.")
        setOpen(false)
      },
      onError: (err) =>
        useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not save analysis studies."),
    })
  }

  return (
    <>
      <Button variant="outline" className="w-full justify-between" onClick={() => setOpen(true)}>
        <span className="flex items-center gap-2">
          <FlaskConical className="size-4" />
          Analysis studies
        </span>
        {applicable.length === 0 ? (
          <span className="text-muted-foreground text-xs font-normal">none apply to this role</span>
        ) : (
          <Badge variant="secondary">
            {serverOn} of {applicable.length} on
          </Badge>
        )}
      </Button>

      <FormDialog
        open={open}
        onOpenChange={setOpen}
        title="Analysis studies"
        description={
          <>
            Which engineering / network studies this class offers. Only studies its <b>role</b> makes applicable
            can be enabled; untick one to hide it from the analysis menu on every feature of this class.
          </>
        }
        onSubmit={save}
        submitLabel="Save"
        canSubmit={canWrite && dirty}
        saving={setStudies.isPending}
      >
        {applicable.length === 0 ? (
          <p className="text-muted-foreground text-sm">
            No analysis studies apply to this class — its role carries no electrical model to analyse.
          </p>
        ) : (
          <div className="flex flex-col gap-3">
            {STUDY_CATEGORIES.map((cat) => {
              const rows = applicable.filter((s) => s.category === cat.key)
              if (rows.length === 0) return null
              return (
                <div key={cat.key}>
                  <div className="text-muted-foreground mb-1 text-xs font-semibold uppercase">{cat.label}</div>
                  <div className="flex flex-col gap-0.5">
                    {rows.map((s) => (
                      <label
                        key={s.id}
                        className="hover:bg-muted/50 flex items-center gap-2 rounded px-1.5 py-1 text-sm"
                      >
                        <input
                          type="checkbox"
                          checked={!current.has(s.id)}
                          disabled={!canWrite}
                          onChange={(e) => toggle(s.id, e.target.checked)}
                        />
                        {s.name}
                      </label>
                    ))}
                  </div>
                </div>
              )
            })}
          </div>
        )}
      </FormDialog>
    </>
  )
}

export function AdminFeatureClassDetailPage() {
  const { code } = useParams<{ code: string }>()
  const navigate = useNavigate()
  const { data: classesData } = useAdminFeatureClasses()
  const { data: schema, isPending, isError } = useFeatureClassSchema(code)
  const queryClient = useQueryClient()
  // Compile and every subtype/field add/edit here are metadata:write on the
  // server; this page opens on metadata:read, so gate the write controls.
  const canWrite = usePermission()("metadata:write")
  const [compiling, setCompiling] = useState(false)
  const [uncompileOpen, setUncompileOpen] = useState(false)
  const [deleteOpen, setDeleteOpen] = useState(false)
  const [activeConfirm, setActiveConfirm] = useState<null | "deactivate" | "reactivate">(null)
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

  // Delete permanently removes the whole class definition (subtypes, fields,
  // terminals, rules -- all ON DELETE CASCADE, see api/internal/admin/delete.go).
  // The server allows it only while the class is still draft: a compiled class
  // has a real gis.<code> table that must be dropped first (Uncompile), so the
  // button only renders for a draft. On success there's nothing left to view,
  // so we return to the catalog.
  async function handleDelete() {
    if (!code) return
    await apiFetch(`/admin/feature-classes/${code}`, { method: "DELETE" })
    queryClient.invalidateQueries({ queryKey: ["admin", "feature-classes"] })
    queryClient.invalidateQueries({ queryKey: ["feature-classes"] })
    useToastStore.getState().pushSuccess(`Deleted ${code}.`)
    navigate("/admin/feature-classes")
  }

  // Global activation toggle: deactivate takes a live class out of use
  // everywhere (map, New-Feature menu, forms) while KEEPING its table + data,
  // reactivate puts it back -- the non-destructive opposite of Uncompile. Flips
  // status active <-> deprecated server-side (no recompile). Invalidate both the
  // admin catalog and the map's active-only list so the class appears/vanishes.
  async function handleSetActive(active: boolean) {
    if (!code) return
    await apiFetch(`/admin/feature-classes/${code}/${active ? "reactivate" : "deactivate"}`, { method: "POST" })
    queryClient.invalidateQueries({ queryKey: ["admin", "feature-classes"] })
    queryClient.invalidateQueries({ queryKey: ["feature-classes"] })
    useToastStore.getState().pushSuccess(active ? `Reactivated ${code}.` : `Deactivated ${code} — hidden from use, data kept.`)
  }

  if (!code) return null

  return (
    <div className="h-full overflow-y-auto p-4">
      <div className="flex flex-col gap-4">
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
                        {summary.status === "deprecated" ? "inactive" : summary.status}
                      </Badge>
                    </>
                  )}
                </p>
              </div>
              {canWrite && (
                <div className="flex items-center gap-2">
                  {/* Delete removes the whole draft class. Only a draft can be
                      deleted (a compiled class has a real table -- Uncompile it
                      first), so this and Uncompile are mutually exclusive. */}
                  {summary && summary.status === "draft" && (
                    <Button
                      size="sm"
                      variant="outline"
                      className="text-destructive"
                      onClick={() => setDeleteOpen(true)}
                      disabled={compiling}
                    >
                      <Trash2 className="size-4" />
                      Delete
                    </Button>
                  )}
                  {/* Deactivate (non-destructive): take a live class out of use
                      everywhere but keep its table + data. Reactivate restores it. */}
                  {summary && summary.status === "active" && (
                    <Button size="sm" variant="outline" onClick={() => setActiveConfirm("deactivate")} disabled={compiling}>
                      <PowerOff className="size-4" />
                      Deactivate
                    </Button>
                  )}
                  {summary && summary.status === "deprecated" && (
                    <Button size="sm" variant="outline" onClick={() => setActiveConfirm("reactivate")} disabled={compiling}>
                      <Power className="size-4" />
                      Reactivate
                    </Button>
                  )}
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

            <ConfirmDeleteDialog
              open={deleteOpen}
              title={`Delete ${code}?`}
              description="Permanently removes this draft class and everything defined on it so far (subtypes, fields, terminals, symbology and connectivity rules) — nothing physical exists yet, so there's nothing else to undo. This can't be undone."
              onOpenChange={setDeleteOpen}
              onConfirm={handleDelete}
            />

            <ConfirmDeleteDialog
              open={activeConfirm !== null}
              title={activeConfirm === "reactivate" ? `Reactivate ${code}?` : `Deactivate ${code}?`}
              description={
                activeConfirm === "reactivate"
                  ? "Makes this class available again everywhere — the map, the New-Feature menu and every form."
                  : "Hides this class from use everywhere (the map, the New-Feature menu, the Features browser and every form) and stops it accepting new features. Its table and all data are kept, and you can reactivate it anytime. Any per-territory settings are paused while it's off (global off wins)."
              }
              confirmLabel={activeConfirm === "reactivate" ? "Reactivate" : "Deactivate"}
              onOpenChange={(open) => !open && setActiveConfirm(null)}
              onConfirm={() => handleSetActive(activeConfirm === "reactivate")}
            />

            {summary && <DiagramSettingsSection summary={summary} canWrite={canWrite} />}

            {/* Per-territory availability -- only while the class is globally
                active. A globally-deactivated class is off everywhere, so the
                per-territory overlay is moot and the control is hidden. */}
            {summary && summary.status === "active" && (
              <TerritoryAvailabilitySection code={code} canWrite={canWrite} />
            )}

            {/* Which analysis studies this class offers (applicable by role,
                admin-prunable). Only meaningful once compiled. */}
            {summary && summary.status === "active" && (
              <AnalysisStudiesSection code={code} canWrite={canWrite} />
            )}

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

            <DisplayZoomSection code={code} schema={schema} />

            <h2 className="text-sm font-semibold">Rules</h2>
            <RulesSection code={code} />
          </>
        )}
      </div>
    </div>
  )
}
