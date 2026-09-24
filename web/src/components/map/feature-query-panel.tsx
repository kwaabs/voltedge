import {
  ArrowLeft,
  BarChart3,
  ChevronRight,
  Crosshair,
  Download,
  ListFilter,
  Loader2,
  MapPin,
  Play,
  Plus,
  Sigma,
  Tag,
  X,
} from "lucide-react"
import { useEffect, useMemo, useRef, useState } from "react"

import { formatFieldLabel, formatValue, inputClass } from "@/components/map/field-utils"
import { Button } from "@/components/ui/button"
import { FloatingPanel } from "@/components/ui/floating-panel"
import { type FieldSchema, useFeatureClassSchema } from "@/hooks/use-feature-class-schema"
import { useFeatureClasses } from "@/hooks/use-feature-classes"
import { useTerritories } from "@/hooks/use-territories"
import { ApiError, apiFetch } from "@/lib/api"
import { cn } from "@/lib/utils"
import { type QueryResultRow, useQueryStore } from "@/stores/query-store"
import { RADIUS_PRESETS_M, useRadiusQueryStore } from "@/stores/radius-query-store"
import { useSelectionStore } from "@/stores/selection-store"
import { useToastStore } from "@/stores/toast-store"

type DataType = FieldSchema["data_type"]

// The sentinel the feature-class picker uses for "aggregate across every class"
// rather than one class. Chosen so it can never collide with a real class code
// (which are uppercase identifiers). Selecting it switches the panel into the
// multi-class count breakdown -- the "what's on the map within this scope"
// question -- where the per-class filter/group-by/roll-up don't apply.
const ALL_CLASSES = "__all__"

// Matches api/internal/crud query.go's buildGroup: the maximum nesting depth
// the backend accepts, so the UI never offers an "Add group" the server would
// reject. Root is depth 0.
const MAX_DEPTH = 5

// The operator keys mirror api/internal/crud/query.go's buildCondition
// exactly -- the panel never sends an op the backend doesn't know.
type OpKey =
  | "eq"
  | "ne"
  | "lt"
  | "lte"
  | "gt"
  | "gte"
  | "contains"
  | "starts_with"
  | "in"
  | "between"
  | "is_null"
  | "not_null"

const OP_LABELS: Record<OpKey, string> = {
  eq: "=",
  ne: "≠",
  lt: "<",
  lte: "≤",
  gt: ">",
  gte: "≥",
  contains: "contains",
  starts_with: "starts with",
  in: "in (comma-separated)",
  between: "between",
  is_null: "is empty",
  not_null: "is not empty",
}

const NO_VALUE_OPS = new Set<OpKey>(["is_null", "not_null"])

/** A column the builder lets you filter on, normalized across the class's
 * own fields and the system columns (name/status/subtype). `options`, when
 * present, drives a value dropdown instead of a free-text input -- a coded
 * (domain) field, status, or subtype. */
interface QueryField {
  name: string
  label: string
  dataType: DataType
  options?: { value: string; label: string }[]
}

/** Which operators are offered for a field, matching buildCondition's own
 * data_type gating so an offered op can never come back a 400: ordered
 * comparisons only for number/date types, contains/starts_with only for
 * text, in for text/uuid/number, boolean and uuid pared down to equality. */
function operatorsFor(field: QueryField): OpKey[] {
  switch (field.dataType) {
    case "boolean":
      return ["eq", "ne", "is_null", "not_null"]
    case "numeric":
    case "integer":
      return ["eq", "ne", "lt", "lte", "gt", "gte", "between", "in", "is_null", "not_null"]
    case "date":
    case "timestamp":
      return ["eq", "ne", "lt", "lte", "gt", "gte", "between", "is_null", "not_null"]
    case "uuid":
      return ["eq", "ne", "in", "is_null", "not_null"]
    default:
      // text (including status and coded/domain fields)
      return ["eq", "ne", "contains", "starts_with", "in", "is_null", "not_null"]
  }
}

/** Turns a raw string from an input/select into the JSON type the backend
 * binds: a number for numeric/integer, a real boolean for boolean, the
 * string as-is otherwise (dates go over as "YYYY-MM-DD" strings, which
 * Postgres casts against a date/timestamp column). */
function convert(field: QueryField, raw: string): unknown {
  if (field.dataType === "numeric" || field.dataType === "integer") {
    const n = Number(raw)
    return Number.isFinite(n) ? n : raw
  }
  if (field.dataType === "boolean") return raw === "true"
  return raw
}

interface Condition {
  id: number
  field: string
  op: OpKey
  value: string
  value2: string // the upper bound for `between`
}

/** One node of the condition tree (Slice 4): a match combinator over its own
 * leaf conditions and any nested sub-groups. The root is a Group too; a flat
 * query is just a root with conditions and no sub-groups. Mirrors the
 * backend's queryGroup. */
interface Group {
  id: number
  match: "all" | "any"
  conditions: Condition[]
  groups: Group[]
}

// --- pure tree helpers -------------------------------------------------------

/** Returns a new tree with the group of the given id replaced by fn(group). */
function updateGroupById(g: Group, id: number, fn: (g: Group) => Group): Group {
  if (g.id === id) return fn(g)
  if (g.groups.length === 0) return g
  return { ...g, groups: g.groups.map((sub) => updateGroupById(sub, id, fn)) }
}

/** Returns a new tree with the sub-group of the given id removed wherever it
 * sits (the root itself is never removable). */
function removeGroupById(g: Group, id: number): Group {
  return { ...g, groups: g.groups.filter((sub) => sub.id !== id).map((sub) => removeGroupById(sub, id)) }
}

/** Applies a condition patch, snapping the operator back to the field's first
 * valid one (and clearing stale values) when the field itself changed. */
function applyConditionPatch(c: Condition, patch: Partial<Condition>, fieldByName: Map<string, QueryField>): Condition {
  const next = { ...c, ...patch }
  if (patch.field && patch.field !== c.field) {
    const field = fieldByName.get(patch.field)
    next.op = field ? operatorsFor(field)[0] : "eq"
    next.value = ""
    next.value2 = ""
  }
  return next
}

interface SerializedCondition {
  field: string
  op: OpKey
  value?: unknown
  values?: unknown[]
}

interface SerializedGroup {
  match: "all" | "any"
  conditions: SerializedCondition[]
  groups: SerializedGroup[]
}

/** One condition -> the backend's wire shape, or null when it's incomplete
 * (a value-taking op with no value): incomplete rows are dropped rather than
 * sent, so a half-filled row never 400s the whole query. */
function conditionToJSON(c: Condition, field: QueryField): SerializedCondition | null {
  if (NO_VALUE_OPS.has(c.op)) return { field: c.field, op: c.op }
  if (c.op === "between") {
    if (c.value === "" || c.value2 === "") return null
    return { field: c.field, op: c.op, values: [convert(field, c.value), convert(field, c.value2)] }
  }
  if (c.op === "in") {
    const values = c.value
      .split(",")
      .map((v) => v.trim())
      .filter((v) => v !== "")
      .map((v) => convert(field, v))
    if (values.length === 0) return null
    return { field: c.field, op: c.op, values }
  }
  if (c.value === "") return null
  return { field: c.field, op: c.op, value: convert(field, c.value) }
}

/** Recursively serializes a group into the request body, dropping incomplete
 * conditions and pruning any sub-group that ends up empty. */
function serializeGroup(g: Group, fieldByName: Map<string, QueryField>): SerializedGroup {
  const conditions = g.conditions
    .map((c) => {
      const field = fieldByName.get(c.field)
      return field ? conditionToJSON(c, field) : null
    })
    .filter((c): c is SerializedCondition => c !== null)
  const groups = g.groups
    .map((sub) => serializeGroup(sub, fieldByName))
    .filter((sg) => sg.conditions.length > 0 || sg.groups.length > 0)
  return { match: g.match, conditions, groups }
}

/** The equality that pins one GROUP BY bucket when drilling into it: `field = the
 * clicked value`, or `field IS empty` for the unset-value bucket (its aggregate
 * value comes back null). ANDed with the user's filter to reproduce exactly the
 * rows that bucket counted. */
function groupCondition(fieldName: string, value: unknown): SerializedCondition {
  if (value === null || value === undefined) return { field: fieldName, op: "is_null" }
  return { field: fieldName, op: "eq", value }
}

// --- asset-tag filter --------------------------------------------------------

/** The asset-tag predicate the query panel can carry (Filter mode). Mirrors the
 * backend's queryTagFilter: presence (have / missing a matching tag), an
 * optional specific code matched exact or as a case-insensitive prefix, and an
 * optional source narrowing (system-minted vs a legacy/external number). `on`
 * gates the whole thing -- off means the query sends no tag predicate at all. */
interface TagFilterState {
  on: boolean
  presence: "have" | "missing"
  code: string
  match: "exact" | "prefix"
  source: "" | "generated" | "external"
}

const TAG_FILTER_OFF: TagFilterState = { on: false, presence: "have", code: "", match: "exact", source: "" }

/** The tag filter as the query body's `tag` object, or null when it's off. A
 * blank code is a pure presence test (any matching tag); "missing" flips it to
 * NOT EXISTS. Only sent in Filter mode. */
function tagFilterToJSON(t: TagFilterState): Record<string, unknown> | null {
  if (!t.on) return null
  const tag: Record<string, unknown> = { present: t.presence === "have" }
  const code = t.code.trim()
  if (code) {
    tag.code = code
    tag.match = t.match
  }
  if (t.source) tag.source = t.source
  return tag
}

// --- aggregate mode ----------------------------------------------------------

/** Which of the two things the panel does: filter and list matching features
 * (the original builder), or aggregate them into counts (Slice 2). Both share
 * the same feature-class picker and condition builder -- aggregate just folds
 * what the filter narrows to instead of listing it. */
type PanelMode = "filter" | "aggregate"

/** The secondary roll-up beyond the always-present count. "count" means "no
 * secondary aggregate" -- the group count is enough. sum/avg/min/max mirror
 * aggSQL in api/internal/crud/aggregate.go and are numeric-field only. */
type AggFn = "count" | "sum" | "avg" | "min" | "max"

const AGG_FN_LABELS: Record<AggFn, string> = {
  count: "Count",
  sum: "Sum",
  avg: "Average",
  min: "Minimum",
  max: "Maximum",
}

/** The spatial narrowing an aggregate can carry, all driven from this form:
 * "none" (everywhere), "territory" (a chosen territory and its subtree), or
 * "radius" (a metric circle around a point picked on the map -- the picker,
 * crosshair, drawn circle, and click-to-drop live in voltedge-map.tsx, keyed off
 * radius-query-store, and this panel reads the dropped center + radius back from
 * that same store). */
type ScopeKind = "none" | "territory" | "radius"

/** A metre radius as a short label: metres under 1 km, else kilometres. */
function formatRadius(m: number): string {
  return m < 1000 ? `${Math.round(m)} m` : `${(m / 1000).toLocaleString(undefined, { maximumFractionDigits: 2 })} km`
}

/** One GROUP BY bucket from POST /{code}/aggregate -- mirrors aggregateGroup. */
interface AggregateGroup {
  value: unknown
  count: number
  agg?: number
}

/** The aggregate response envelope -- mirrors aggregateResponse. */
interface AggregateResult {
  total: number
  agg?: number
  groups: AggregateGroup[]
}

/** One class's feature count within the scope, for the All-feature-classes
 * breakdown (each class's group-less /aggregate total). */
interface ClassCount {
  code: string
  name: string
  count: number
}

/** A drill-down from a count into the actual rows behind it: the class the rows
 * belong to, a human label for what was drilled (a class name, a group value, or
 * "all matches"), and the fetched feature rows. Set while the results area shows
 * the feature list instead of the counts; cleared by "Back to counts". */
interface DrillResult {
  classCode: string
  label: string
  rows: QueryResultRow[]
}

/** The number the aggregate function reports, trimmed to a sensible precision
 * (avg can be long; sum/min/max are usually already round). */
function formatAgg(n: number): string {
  if (!Number.isFinite(n)) return "—"
  return Number.isInteger(n) ? String(n) : n.toLocaleString(undefined, { maximumFractionDigits: 3 })
}

/**
 * The map's structured query builder (docs/phase-13-query-builder.md): pick a
 * feature class, its attributes load, build typed `field op value` conditions
 * combined with match-all (AND) / match-any (OR) toggles -- flat, or nested
 * into groups for `(A AND B) OR C` (Slice 4) -- and Run posts to
 * POST /features/{code}/query. Matches come back in a table where every row
 * has a zoom button (select(...,{focus:true}), same as the trace table) and
 * are all highlighted at once on the map via query-store.ts; the result set
 * exports to CSV.
 *
 * Lives inside the reusable FloatingPanel shell (draggable/resizable/closeable)
 * and is only mounted while the store's `open` is true (map.tsx), so its schema
 * fetch and result set don't exist until the user actually opens it.
 */
export function FeatureQueryPanel() {
  const { data: classes } = useFeatureClasses()
  const [code, setCode] = useState("")
  const isAllClasses = code === ALL_CLASSES
  // No single-class schema to fetch when aggregating across every class.
  const { data: schema, isLoading: schemaLoading } = useFeatureClassSchema(isAllClasses ? undefined : code || undefined)

  const nextId = useRef(1)
  const [root, setRoot] = useState<Group>(() => ({ id: 0, match: "all", conditions: [], groups: [] }))
  const [running, setRunning] = useState(false)
  const [mode, setMode] = useState<PanelMode>("filter")

  // Asset-tag filter (Filter mode only -- /query carries it as an EXISTS
  // subquery against runtime.asset_tags; /aggregate has no tag predicate). Off
  // by default. "have" keeps features that carry a matching tag; "missing"
  // flips to those that carry none. code + match narrow to a specific code
  // (exact / case-insensitive prefix); source narrows to generated / external.
  const [tagFilter, setTagFilter] = useState<TagFilterState>(() => ({ ...TAG_FILTER_OFF }))

  // Aggregate-mode form state (Slice 2). groupBy "" = one overall total; aggFn
  // "count" = no secondary roll-up; scope "none"/territory narrows spatially.
  const [groupBy, setGroupBy] = useState("")
  const [aggFn, setAggFn] = useState<AggFn>("count")
  const [aggField, setAggField] = useState("")
  const [scopeKind, setScopeKind] = useState<ScopeKind>("none")
  const [scopeTerritory, setScopeTerritory] = useState("")
  const [aggResult, setAggResult] = useState<AggregateResult | null>(null)
  // The All-feature-classes breakdown (per-class counts within the scope).
  const [multiResult, setMultiResult] = useState<ClassCount[] | null>(null)
  // The drill-down feature list (the rows behind a clicked count), or null when
  // the results area is showing counts. Its rows are also pushed to the
  // query-store so they light up on the map while the list is open.
  const [drill, setDrill] = useState<DrillResult | null>(null)
  const { data: territories } = useTerritories()

  // Radius scope shares the map's radius-query-store: arming it turns the next
  // map click into a dropped center (voltedge-map.tsx owns the crosshair, circle,
  // and click-to-drop), and the center + radius are read back here to build the
  // scope. The circle stays on the map only while this scope is active (the
  // effect below tears it down on leaving radius mode or closing the panel).
  const radiusCenter = useRadiusQueryStore((s) => s.center)
  const radiusArmed = useRadiusQueryStore((s) => s.armed)
  const radiusM = useRadiusQueryStore((s) => s.radiusM)

  const results = useQueryStore((s) => s.results)
  const resultClass = useQueryStore((s) => s.resultClass)

  // Only geometric classes are worth querying from the map -- a geometry-less
  // class has nothing to highlight or zoom to. Sorted by display name.
  const queryableClasses = useMemo(
    () =>
      (classes?.feature_classes ?? [])
        .filter((c) => c.geometry_type !== "none")
        .slice()
        .sort((a, b) => a.name.localeCompare(b.name)),
    [classes],
  )

  // The queryable columns for the picked class: system columns first, then
  // its own non-jsonb fields (jsonb has no sensible flat operator here).
  const fields = useMemo<QueryField[]>(() => {
    if (!schema) return []
    const out: QueryField[] = [
      { name: "name", label: "Name", dataType: "text" },
      {
        name: "status",
        label: "Status",
        dataType: "text",
        options: [
          { value: "draft", label: "Draft" },
          { value: "published", label: "Published" },
        ],
      },
    ]
    if (schema.subtypes.length > 0) {
      out.push({
        name: "subtype",
        label: "Subtype",
        dataType: "integer",
        options: schema.subtypes.map((s) => ({ value: String(s.code), label: s.name })),
      })
    }
    for (const f of schema.fields) {
      if (f.data_type === "jsonb") continue
      out.push({
        name: f.name,
        label: formatFieldLabel(f.name),
        dataType: f.data_type,
        options: f.domain_values?.map((v) => ({ value: v, label: v })),
      })
    }
    return out
  }, [schema])

  const fieldByName = useMemo(() => new Map(fields.map((f) => [f.name, f])), [fields])

  // The fields a sum/avg/min/max can run over -- numeric only, matching the
  // backend's aggregateExpr gate so an offered field can never come back a 400.
  const numericFields = useMemo(
    () => fields.filter((f) => f.dataType === "numeric" || f.dataType === "integer"),
    [fields],
  )

  // Territories for the scope dropdown, ordered top level first then by name so
  // the tree reads utility -> region -> district the way the admin tree does.
  const scopeTerritories = useMemo(
    () =>
      (territories?.territories ?? [])
        .slice()
        .sort((a, b) => a.level_rank - b.level_rank || a.name.localeCompare(b.name)),
    [territories],
  )

  const makeCondition = (field: string): Condition => {
    const f = fieldByName.get(field)
    return { id: nextId.current++, field, op: f ? operatorsFor(f)[0] : "eq", value: "", value2: "" }
  }
  const makeGroup = (): Group => ({ id: nextId.current++, match: "all", conditions: [], groups: [] })

  // Reset the whole builder whenever the class changes -- the old class's
  // fields, conditions, and any results (feature list or aggregate) are
  // meaningless against a new one.
  useEffect(() => {
    setRoot({ id: nextId.current++, match: "all", conditions: [], groups: [] })
    useQueryStore.getState().clearResults()
    setGroupBy("")
    setAggFn("count")
    setAggField("")
    setAggResult(null)
    setMultiResult(null)
    setDrill(null)
    setTagFilter({ ...TAG_FILTER_OFF })
  }, [code])

  // The radius circle/picker only makes sense while a radius scope is actually on
  // screen -- that's whenever the radius scope is selected AND the scope controls
  // are visible (the All-classes breakdown, or single-class Aggregate mode). The
  // moment none of that holds (Filter mode single-class, a non-radius scope) or
  // the panel unmounts, clear it so no orphan circle is left on the map.
  const radiusScopeActive = scopeKind === "radius" && (isAllClasses || mode === "aggregate")
  useEffect(() => {
    if (!radiusScopeActive) useRadiusQueryStore.getState().clear()
    return () => useRadiusQueryStore.getState().clear()
  }, [radiusScopeActive])

  // A drill-down belongs to the aggregate that opened it -- toggling filter/
  // aggregate mode drops back to counts and clears the drill's map highlight so
  // it never lingers in a mode that didn't produce it.
  useEffect(() => {
    setDrill(null)
    useQueryStore.getState().clearResults()
  }, [mode])

  // Keep the aggregate field valid: when a roll-up is selected but the picked
  // field isn't (still) a numeric column on this class, snap to the first one.
  useEffect(() => {
    if (aggFn === "count") return
    if (!numericFields.some((f) => f.name === aggField)) {
      setAggField(numericFields[0]?.name ?? "")
    }
  }, [aggFn, aggField, numericFields])

  // Once the picked class's attributes have loaded, seed one empty condition
  // if the root is still bare, so the user lands on a ready-to-fill row rather
  // than an empty panel.
  useEffect(() => {
    if (fields.length === 0) return
    setRoot((r) => (r.conditions.length === 0 && r.groups.length === 0 ? { ...r, conditions: [makeCondition(fields[0].name)] } : r))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [fields])

  const actions: GroupActions = {
    fields,
    fieldByName,
    setMatch: (id, match) => setRoot((r) => updateGroupById(r, id, (g) => ({ ...g, match }))),
    addCondition: (id) =>
      setRoot((r) => updateGroupById(r, id, (g) => ({ ...g, conditions: [...g.conditions, makeCondition(fields[0]?.name ?? "")] }))),
    addGroup: (id) => setRoot((r) => updateGroupById(r, id, (g) => ({ ...g, groups: [...g.groups, makeGroup()] }))),
    updateCondition: (gid, cid, patch) =>
      setRoot((r) =>
        updateGroupById(r, gid, (g) => ({
          ...g,
          conditions: g.conditions.map((c) => (c.id === cid ? applyConditionPatch(c, patch, fieldByName) : c)),
        })),
      ),
    removeCondition: (gid, cid) =>
      setRoot((r) => updateGroupById(r, gid, (g) => ({ ...g, conditions: g.conditions.filter((c) => c.id !== cid) }))),
    removeGroup: (id) => setRoot((r) => removeGroupById(r, id)),
  }

  async function run() {
    const body: Record<string, unknown> = { ...serializeGroup(root, fieldByName), limit: 200 }
    const tag = tagFilterToJSON(tagFilter)
    if (tag) body.tag = tag
    setRunning(true)
    try {
      const res = await apiFetch<{ features: QueryResultRow[] }>(`/features/${code}/query`, {
        method: "POST",
        body: JSON.stringify(body),
      })
      useQueryStore.getState().setResults(code, res.features)
      if (res.features.length === 0) {
        useToastStore.getState().pushWarning("No features match this query.")
      }
    } catch (err) {
      const message = err instanceof ApiError ? err.message : "Query failed."
      useToastStore.getState().pushError(message)
    } finally {
      setRunning(false)
    }
  }

  async function runAggregate() {
    const filter = serializeGroup(root, fieldByName)
    const body: Record<string, unknown> = { ...filter, limit: 500 }
    if (groupBy) body.group_by = groupBy
    if (aggFn !== "count" && aggField) body.aggregate = { fn: aggFn, field: aggField }
    if (scopeKind === "territory" && scopeTerritory) {
      body.scope = { kind: "territory", territory_id: scopeTerritory }
    }
    if (scopeKind === "radius" && radiusCenter) {
      body.scope = { kind: "radius", center: radiusCenter, radius_m: radiusM }
    }
    closeDrill()
    setRunning(true)
    try {
      const res = await apiFetch<AggregateResult>(`/features/${code}/aggregate`, {
        method: "POST",
        body: JSON.stringify(body),
      })
      setAggResult(res)
    } catch (err) {
      const message = err instanceof ApiError ? err.message : "Aggregate failed."
      useToastStore.getState().pushError(message)
    } finally {
      setRunning(false)
    }
  }

  // The chosen spatial scope as the request's scope object, or null for
  // "everywhere". Shared by the single-class aggregate and the All-classes
  // breakdown so both narrow identically.
  function buildScope(): Record<string, unknown> | null {
    if (scopeKind === "territory" && scopeTerritory) return { kind: "territory", territory_id: scopeTerritory }
    if (scopeKind === "radius" && radiusCenter) return { kind: "radius", center: radiusCenter, radius_m: radiusM }
    return null
  }

  // Drill from a count into the rows behind it: run the SAME narrowing the count
  // used -- the user's filter (single-class) plus the spatial scope -- against
  // /query, optionally ANDed with one extra equality that pins a grouped bucket
  // (group_by field = the clicked value). The rows land in local `drill` state
  // (the list) AND the query-store (so they highlight on the map, the spatial
  // half of "show me what's here"). limit 500 mirrors the aggregate's group cap
  // enough for a drill; a bigger bucket is better narrowed than paged.
  async function runDrill(classCode: string, label: string, filter: SerializedGroup | null, extra: SerializedCondition | null) {
    const scope = buildScope()
    const body: Record<string, unknown> = { limit: 500 }
    if (scope) body.scope = scope
    if (extra && filter) {
      // AND the bucket's equality with the user's whole filter (kept as a nested
      // group so its own match/OR semantics survive intact).
      body.match = "all"
      body.conditions = [extra]
      body.groups = [filter]
    } else if (extra) {
      body.match = "all"
      body.conditions = [extra]
    } else if (filter) {
      body.match = filter.match
      body.conditions = filter.conditions
      body.groups = filter.groups
    }
    setRunning(true)
    try {
      const res = await apiFetch<{ features: QueryResultRow[] }>(`/features/${classCode}/query`, {
        method: "POST",
        body: JSON.stringify(body),
      })
      setDrill({ classCode, label, rows: res.features })
      useQueryStore.getState().setResults(classCode, res.features)
      if (res.features.length === 0) {
        useToastStore.getState().pushWarning("No features to show for this count.")
      }
    } catch (err) {
      const message = err instanceof ApiError ? err.message : "Drill-down failed."
      useToastStore.getState().pushError(message)
    } finally {
      setRunning(false)
    }
  }

  // Leave the drill list and return to the counts, dropping the map highlight
  // (the rows were the drill's output, so they shouldn't outlive it).
  function closeDrill() {
    setDrill(null)
    useQueryStore.getState().clearResults()
  }

  // The All-feature-classes breakdown: fan a group-less /aggregate out across
  // every geometric class under the same scope, so a dropped radius (or a chosen
  // territory) answers "what -- and how many of each -- is on the map here." A
  // class the user can't read contributes a zero rather than failing the sweep.
  async function runMultiClass() {
    const scope = buildScope()
    closeDrill()
    setRunning(true)
    try {
      const rows = await Promise.all(
        queryableClasses.map((c) =>
          apiFetch<AggregateResult>(`/features/${c.code}/aggregate`, {
            method: "POST",
            body: JSON.stringify(scope ? { scope } : {}),
          })
            .then((res) => ({ code: c.code, name: c.name, count: res.total }))
            .catch(() => ({ code: c.code, name: c.name, count: 0 })),
        ),
      )
      const nonEmpty = rows.filter((r) => r.count > 0).sort((a, b) => b.count - a.count)
      setMultiResult(nonEmpty)
      if (nonEmpty.length === 0) {
        useToastStore.getState().pushWarning("No features found within this scope.")
      }
    } catch (err) {
      const message = err instanceof ApiError ? err.message : "Count failed."
      useToastStore.getState().pushError(message)
    } finally {
      setRunning(false)
    }
  }

  // The group-by field resolved back to a QueryField, for labelling raw group
  // values (a subtype code / domain code -> its human name) in the results.
  const groupByField = groupBy ? fieldByName.get(groupBy) : undefined

  return (
    <FloatingPanel
      title="Query builder"
      onClose={() => useQueryStore.getState().closePanel()}
      initialRect={{ x: 16, y: 60, width: 480, height: 500 }}
      minWidth={400}
    >
      <div className="flex h-full flex-col">
        <div className="flex flex-col gap-3 border-b p-3">
          {!isAllClasses && <ModeToggle mode={mode} onChange={setMode} />}

          <div className="flex flex-col gap-1">
            <label className="text-xs font-medium">Feature class</label>
            <select className={inputClass} value={code} onChange={(e) => setCode(e.target.value)}>
              <option value="">Select a feature class…</option>
              <option value={ALL_CLASSES}>All feature classes (count by class)</option>
              {queryableClasses.map((c) => (
                <option key={c.code} value={c.code}>
                  {c.name}
                </option>
              ))}
            </select>
          </div>

          {isAllClasses && (
            <>
              <p className="text-muted-foreground text-xs">
                Counts every feature class within the scope below and lists how many of each are inside it.
              </p>
              <ScopeControls
                territories={scopeTerritories}
                scopeKind={scopeKind}
                onScopeKind={setScopeKind}
                scopeTerritory={scopeTerritory}
                onScopeTerritory={setScopeTerritory}
                radiusCenter={radiusCenter}
                radiusArmed={radiusArmed}
                radiusM={radiusM}
              />
              <div className="flex justify-end">
                <Button
                  size="sm"
                  onClick={runMultiClass}
                  disabled={
                    running ||
                    (scopeKind === "radius" && !radiusCenter) ||
                    (scopeKind === "territory" && !scopeTerritory)
                  }
                >
                  {running ? <Loader2 className="size-3.5 animate-spin" /> : <Play className="size-3.5" />}
                  Count
                </Button>
              </div>
            </>
          )}

          {!isAllClasses && code && schemaLoading && (
            <p className="text-muted-foreground flex items-center gap-2 text-sm">
              <Loader2 className="size-4 animate-spin" /> Loading attributes…
            </p>
          )}

          {!isAllClasses && code && !schemaLoading && fields.length > 0 && (
            <>
              {mode === "aggregate" && (
                <span className="text-muted-foreground text-xs font-medium">Filter (optional)</span>
              )}
              <GroupEditor group={root} depth={0} isRoot actions={actions} />

              {mode === "aggregate" && (
                <AggregateControls
                  fields={fields}
                  numericFields={numericFields}
                  territories={scopeTerritories}
                  groupBy={groupBy}
                  onGroupBy={setGroupBy}
                  aggFn={aggFn}
                  onAggFn={setAggFn}
                  aggField={aggField}
                  onAggField={setAggField}
                  scopeKind={scopeKind}
                  onScopeKind={setScopeKind}
                  scopeTerritory={scopeTerritory}
                  onScopeTerritory={setScopeTerritory}
                  radiusCenter={radiusCenter}
                  radiusArmed={radiusArmed}
                  radiusM={radiusM}
                />
              )}

              {mode === "filter" && <TagFilterControls value={tagFilter} onChange={setTagFilter} />}

              <div className="flex justify-end">
                <Button
                  size="sm"
                  onClick={mode === "filter" ? run : runAggregate}
                  disabled={
                    running ||
                    (mode === "aggregate" &&
                      ((aggFn !== "count" && !aggField) ||
                        (scopeKind === "radius" && !radiusCenter) ||
                        (scopeKind === "territory" && !scopeTerritory)))
                  }
                >
                  {running ? <Loader2 className="size-3.5 animate-spin" /> : <Play className="size-3.5" />}
                  Run
                </Button>
              </div>
            </>
          )}
        </div>

        <div className="flex min-h-0 flex-1 flex-col">
          {drill ? (
            <DrillResults drill={drill} onBack={closeDrill} />
          ) : isAllClasses ? (
            multiResult !== null && (
              <MultiClassResults rows={multiResult} busy={running} onDrill={(c, n) => runDrill(c, n, null, null)} />
            )
          ) : mode === "filter" ? (
            results !== null && resultClass === code && <QueryResults rows={results} resultClass={code} />
          ) : (
            aggResult !== null && (
              <AggregateResults
                result={aggResult}
                resultClass={code}
                groupByField={groupByField}
                aggFn={aggFn}
                busy={running}
                onDrillAll={() => runDrill(code, "All matches", serializeGroup(root, fieldByName), null)}
                onDrillGroup={(value, label) =>
                  runDrill(
                    code,
                    `${groupByField?.label ?? "Value"}: ${label}`,
                    serializeGroup(root, fieldByName),
                    groupCondition(groupBy, value),
                  )
                }
              />
            )
          )}
        </div>
      </div>
    </FloatingPanel>
  )
}

interface GroupActions {
  fields: QueryField[]
  fieldByName: Map<string, QueryField>
  setMatch: (groupId: number, match: "all" | "any") => void
  addCondition: (groupId: number) => void
  addGroup: (groupId: number) => void
  updateCondition: (groupId: number, condId: number, patch: Partial<Condition>) => void
  removeCondition: (groupId: number, condId: number) => void
  removeGroup: (groupId: number) => void
}

/** One group: its match toggle, its own condition rows, then its nested
 * sub-groups (rendered by recursing into this same component), with Add
 * condition / Add group buttons. The root has no border or remove button;
 * every nested group is indented with a left rule and can be removed. */
function GroupEditor({
  group,
  depth,
  isRoot,
  actions,
}: {
  group: Group
  depth: number
  isRoot: boolean
  actions: GroupActions
}) {
  const itemCount = group.conditions.length + group.groups.length

  return (
    <div className={cn("flex flex-col gap-2", !isRoot && "border-muted-foreground/30 rounded-r border-l-2 pl-2")}>
      <div className="flex items-center justify-between gap-2">
        {itemCount >= 2 ? (
          <MatchToggle match={group.match} onChange={(m) => actions.setMatch(group.id, m)} />
        ) : (
          <span className="text-muted-foreground text-xs">{isRoot ? "Conditions" : "Group"}</span>
        )}
        {!isRoot && (
          <Button variant="ghost" size="icon" className="size-6 shrink-0" onClick={() => actions.removeGroup(group.id)}>
            <X className="size-3.5" />
            <span className="sr-only">Remove group</span>
          </Button>
        )}
      </div>

      {group.conditions.map((c) => (
        <ConditionRowEditor
          key={c.id}
          row={c}
          fields={actions.fields}
          field={actions.fieldByName.get(c.field)}
          onChange={(patch) => actions.updateCondition(group.id, c.id, patch)}
          onRemove={() => actions.removeCondition(group.id, c.id)}
        />
      ))}

      {group.groups.map((g) => (
        <GroupEditor key={g.id} group={g} depth={depth + 1} isRoot={false} actions={actions} />
      ))}

      <div className="flex gap-2">
        <Button variant="outline" size="sm" onClick={() => actions.addCondition(group.id)}>
          <Plus className="size-3.5" /> Add condition
        </Button>
        {depth < MAX_DEPTH && (
          <Button variant="outline" size="sm" onClick={() => actions.addGroup(group.id)}>
            <Plus className="size-3.5" /> Add group
          </Button>
        )}
      </div>
    </div>
  )
}

/** The match-all (AND) / match-any (OR) segmented control for one group. */
function MatchToggle({ match, onChange }: { match: "all" | "any"; onChange: (m: "all" | "any") => void }) {
  return (
    <div className="flex items-center gap-2 text-xs">
      <span className="text-muted-foreground">Match</span>
      <div className="flex overflow-hidden rounded-md border">
        {(["all", "any"] as const).map((m) => (
          <button
            key={m}
            onClick={() => onChange(m)}
            className={cn("px-2 py-1", match === m ? "bg-primary text-primary-foreground" : "hover:bg-accent")}
          >
            {m === "all" ? "all (AND)" : "any (OR)"}
          </button>
        ))}
      </div>
    </div>
  )
}

/** The Filter / Aggregate mode switch at the top of the panel. Filter lists
 * matching features (and highlights them on the map); Aggregate folds them into
 * counts. Same segmented-control look as MatchToggle. */
function ModeToggle({ mode, onChange }: { mode: PanelMode; onChange: (m: PanelMode) => void }) {
  return (
    <div className="flex overflow-hidden rounded-md border text-xs">
      {(
        [
          { key: "filter", label: "Filter", Icon: ListFilter },
          { key: "aggregate", label: "Aggregate", Icon: Sigma },
        ] as const
      ).map(({ key, label, Icon }) => (
        <button
          key={key}
          onClick={() => onChange(key)}
          className={cn(
            "flex flex-1 items-center justify-center gap-1.5 px-2 py-1.5",
            mode === key ? "bg-primary text-primary-foreground" : "hover:bg-accent",
          )}
        >
          <Icon className="size-3.5" /> {label}
        </button>
      ))}
    </div>
  )
}

/** The aggregate-mode controls below the filter: what to group by, an optional
 * numeric roll-up (sum/avg/min/max), and a spatial scope (everywhere / a
 * territory subtree / a radius around a point picked on the map). */
function AggregateControls({
  fields,
  numericFields,
  territories,
  groupBy,
  onGroupBy,
  aggFn,
  onAggFn,
  aggField,
  onAggField,
  scopeKind,
  onScopeKind,
  scopeTerritory,
  onScopeTerritory,
  radiusCenter,
  radiusArmed,
  radiusM,
}: {
  fields: QueryField[]
  numericFields: QueryField[]
  territories: { id: string; name: string; level_name: string }[]
  groupBy: string
  onGroupBy: (v: string) => void
  aggFn: AggFn
  onAggFn: (v: AggFn) => void
  aggField: string
  onAggField: (v: string) => void
  scopeKind: ScopeKind
  onScopeKind: (v: ScopeKind) => void
  scopeTerritory: string
  onScopeTerritory: (v: string) => void
  radiusCenter: [number, number] | null
  radiusArmed: boolean
  radiusM: number
}) {
  return (
    <div className="border-muted-foreground/20 flex flex-col gap-2 rounded-md border border-dashed p-2">
      <div className="flex flex-col gap-1">
        <label className="text-muted-foreground text-xs font-medium">Group by</label>
        <select className={inputClass} value={groupBy} onChange={(e) => onGroupBy(e.target.value)}>
          <option value="">— overall total —</option>
          {fields.map((f) => (
            <option key={f.name} value={f.name}>
              {f.label}
            </option>
          ))}
        </select>
      </div>

      <div className="flex flex-col gap-1">
        <label className="text-muted-foreground text-xs font-medium">Aggregate</label>
        <div className="flex gap-1.5">
          <select
            className={cn(inputClass, aggFn === "count" ? "flex-1" : "w-28 shrink-0")}
            value={aggFn}
            onChange={(e) => onAggFn(e.target.value as AggFn)}
          >
            {(Object.keys(AGG_FN_LABELS) as AggFn[]).map((fn) => (
              <option key={fn} value={fn}>
                {AGG_FN_LABELS[fn]}
              </option>
            ))}
          </select>
          {aggFn !== "count" &&
            (numericFields.length > 0 ? (
              <select className={cn(inputClass, "flex-1")} value={aggField} onChange={(e) => onAggField(e.target.value)}>
                {numericFields.map((f) => (
                  <option key={f.name} value={f.name}>
                    {f.label}
                  </option>
                ))}
              </select>
            ) : (
              <span className="text-muted-foreground flex flex-1 items-center text-xs">
                No numeric fields on this class
              </span>
            ))}
        </div>
      </div>

      <ScopeControls
        territories={territories}
        scopeKind={scopeKind}
        onScopeKind={onScopeKind}
        scopeTerritory={scopeTerritory}
        onScopeTerritory={onScopeTerritory}
        radiusCenter={radiusCenter}
        radiusArmed={radiusArmed}
        radiusM={radiusM}
      />
    </div>
  )
}

/** The asset-tag filter (Filter mode): a checkbox arms it, then a presence
 * toggle (has / missing a tag), a source narrowing (any / generated / external),
 * and an optional specific code matched exact or as a prefix. Sent to /query as
 * the `tag` predicate. Off contributes nothing to the query. */
function TagFilterControls({ value, onChange }: { value: TagFilterState; onChange: (v: TagFilterState) => void }) {
  const set = (patch: Partial<TagFilterState>) => onChange({ ...value, ...patch })

  return (
    <div className="border-muted-foreground/20 flex flex-col gap-2 rounded-md border border-dashed p-2">
      <label className="flex items-center gap-2 text-xs font-medium">
        <input type="checkbox" className="size-3.5" checked={value.on} onChange={(e) => set({ on: e.target.checked })} />
        <Tag className="size-3.5" /> Asset tag
      </label>

      {value.on && (
        <div className="flex flex-col gap-2">
          <div className="flex gap-1.5">
            <select
              className={cn(inputClass, "flex-1")}
              value={value.presence}
              onChange={(e) => set({ presence: e.target.value as TagFilterState["presence"] })}
            >
              <option value="have">Has a matching tag</option>
              <option value="missing">Has no matching tag</option>
            </select>
            <select
              className={cn(inputClass, "w-32 shrink-0")}
              value={value.source}
              onChange={(e) => set({ source: e.target.value as TagFilterState["source"] })}
            >
              <option value="">Any source</option>
              <option value="generated">Generated</option>
              <option value="external">External</option>
            </select>
          </div>
          <div className="flex gap-1.5">
            <input
              className={cn(inputClass, "flex-1")}
              value={value.code}
              onChange={(e) => set({ code: e.target.value })}
              placeholder="Tag code (optional)"
            />
            <select
              className={cn(inputClass, "w-28 shrink-0")}
              value={value.match}
              onChange={(e) => set({ match: e.target.value as TagFilterState["match"] })}
              disabled={value.code.trim() === ""}
            >
              <option value="exact">exact</option>
              <option value="prefix">prefix</option>
            </select>
          </div>
          <p className="text-muted-foreground text-xs">
            {value.presence === "missing"
              ? "Finds features that carry no such tag (e.g. plant still missing an external number)."
              : "Finds features by the code on their label; leave the code blank to match any tag."}
          </p>
        </div>
      )}
    </div>
  )
}

/** The spatial scope picker shared by single-class aggregate mode and the
 * All-feature-classes breakdown: everywhere / a territory subtree / a radius
 * around a point picked on the map. Kept separate so both entry points narrow
 * identically. */
function ScopeControls({
  territories,
  scopeKind,
  onScopeKind,
  scopeTerritory,
  onScopeTerritory,
  radiusCenter,
  radiusArmed,
  radiusM,
}: {
  territories: { id: string; name: string; level_name: string }[]
  scopeKind: ScopeKind
  onScopeKind: (v: ScopeKind) => void
  scopeTerritory: string
  onScopeTerritory: (v: string) => void
  radiusCenter: [number, number] | null
  radiusArmed: boolean
  radiusM: number
}) {
  return (
    <div className="flex flex-col gap-1.5">
      <label className="text-muted-foreground text-xs font-medium">Scope</label>
      <div className="flex gap-1.5">
        <select
          className={cn(inputClass, scopeKind === "territory" ? "w-28 shrink-0" : "flex-1")}
          value={scopeKind}
          onChange={(e) => onScopeKind(e.target.value as ScopeKind)}
        >
          <option value="none">Everywhere</option>
          <option value="territory">Territory</option>
          <option value="radius">Radius of a point</option>
        </select>
        {scopeKind === "territory" && (
          <select
            className={cn(inputClass, "flex-1")}
            value={scopeTerritory}
            onChange={(e) => onScopeTerritory(e.target.value)}
          >
            <option value="">Select a territory…</option>
            {territories.map((t) => (
              <option key={t.id} value={t.id}>
                {t.name} ({t.level_name})
              </option>
            ))}
          </select>
        )}
      </div>
      {scopeKind === "radius" && (
        <RadiusScopeControl center={radiusCenter} armed={radiusArmed} radiusM={radiusM} />
      )}
    </div>
  )
}

/** The radius scope's picker + radius control (aggregate mode). "Pick point on
 * map" arms the shared radius-query-store, after which the next map click drops
 * the center (voltedge-map.tsx draws the circle and disarms); the dropped
 * coordinates show here and the radius is tunable by preset or metre input. All
 * state lives in radius-query-store so the map and this panel stay in lock-step. */
function RadiusScopeControl({
  center,
  armed,
  radiusM,
}: {
  center: [number, number] | null
  armed: boolean
  radiusM: number
}) {
  const [radiusInput, setRadiusInput] = useState(String(radiusM))
  useEffect(() => setRadiusInput(String(radiusM)), [radiusM])

  const applyRadiusInput = () => {
    const n = Number(radiusInput)
    if (Number.isFinite(n) && n > 0) useRadiusQueryStore.getState().setRadius(n)
    else setRadiusInput(String(radiusM))
  }

  return (
    <div className="border-muted-foreground/20 mt-1 flex flex-col gap-2 rounded-md border border-dashed p-2">
      <div className="flex items-center gap-2">
        <Button
          variant={armed ? "default" : "outline"}
          size="sm"
          onClick={() => (armed ? useRadiusQueryStore.getState().disarm() : useRadiusQueryStore.getState().arm())}
        >
          <MapPin className="size-3.5" />
          {center ? "Re-pick center" : "Pick point on map"}
        </Button>
        {armed ? (
          <span className="text-muted-foreground text-xs">Click a point on the map…</span>
        ) : center ? (
          <span className="text-muted-foreground font-mono text-xs">
            {center[1].toFixed(5)}, {center[0].toFixed(5)}
          </span>
        ) : (
          <span className="text-muted-foreground text-xs">No point set</span>
        )}
      </div>

      <div className="flex flex-col gap-1.5">
        <div className="flex flex-wrap gap-1.5">
          {RADIUS_PRESETS_M.map((m) => (
            <button
              key={m}
              onClick={() => useRadiusQueryStore.getState().setRadius(m)}
              className={cn(
                "rounded-md border px-2 py-1 text-xs",
                radiusM === m ? "bg-primary text-primary-foreground border-primary" : "hover:bg-accent",
              )}
            >
              {formatRadius(m)}
            </button>
          ))}
        </div>
        <div className="flex items-center gap-1.5">
          <input
            type="number"
            min={1}
            className={cn(inputClass, "w-24")}
            value={radiusInput}
            onChange={(e) => setRadiusInput(e.target.value)}
            onBlur={applyRadiusInput}
            onKeyDown={(e) => e.key === "Enter" && applyRadiusInput()}
          />
          <span className="text-muted-foreground text-xs">metres radius</span>
        </div>
      </div>
    </div>
  )
}

/** One `field op value` row. The value control adapts to the field: a
 * dropdown for coded/status/subtype/boolean, a date/number/text input by
 * data_type, two inputs for `between`, and nothing for is_null/not_null. */
function ConditionRowEditor({
  row,
  fields,
  field,
  onChange,
  onRemove,
}: {
  row: Condition
  fields: QueryField[]
  field: QueryField | undefined
  onChange: (patch: Partial<Condition>) => void
  onRemove: () => void
}) {
  const ops = field ? operatorsFor(field) : []
  const needsValue = !NO_VALUE_OPS.has(row.op)

  return (
    <div className="flex items-start gap-1.5">
      <div className="flex flex-1 flex-col gap-1.5">
        <div className="flex gap-1.5">
          <select
            className={cn(inputClass, "flex-1")}
            value={row.field}
            onChange={(e) => onChange({ field: e.target.value })}
          >
            {fields.map((f) => (
              <option key={f.name} value={f.name}>
                {f.label}
              </option>
            ))}
          </select>
          <select
            className={cn(inputClass, "w-32 shrink-0")}
            value={row.op}
            onChange={(e) => onChange({ op: e.target.value as OpKey })}
          >
            {ops.map((op) => (
              <option key={op} value={op}>
                {OP_LABELS[op]}
              </option>
            ))}
          </select>
        </div>
        {needsValue && field && (
          <div className="flex gap-1.5">
            {row.op === "in" ? (
              // Always free text (comma-separated) -- a list operand doesn't
              // fit a single-select even when the field is coded.
              <input
                className={inputClass}
                value={row.value}
                onChange={(e) => onChange({ value: e.target.value })}
                placeholder="value, value, …"
              />
            ) : (
              <>
                <ValueInput field={field} value={row.value} onChange={(v) => onChange({ value: v })} />
                {row.op === "between" && (
                  <ValueInput field={field} value={row.value2} onChange={(v) => onChange({ value2: v })} />
                )}
              </>
            )}
          </div>
        )}
      </div>
      <Button variant="ghost" size="icon" className="size-8 shrink-0" onClick={onRemove}>
        <X className="size-3.5" />
        <span className="sr-only">Remove condition</span>
      </Button>
    </div>
  )
}

/** The typed operand editor -- a select for a coded field/status/subtype, a
 * true/false select for a boolean, a date/number/text input otherwise. */
function ValueInput({
  field,
  value,
  onChange,
}: {
  field: QueryField
  value: string
  onChange: (value: string) => void
}) {
  if (field.dataType === "boolean") {
    return (
      <select className={inputClass} value={value} onChange={(e) => onChange(e.target.value)}>
        <option value="">—</option>
        <option value="true">Yes</option>
        <option value="false">No</option>
      </select>
    )
  }
  if (field.options) {
    return (
      <select className={inputClass} value={value} onChange={(e) => onChange(e.target.value)}>
        <option value="">—</option>
        {field.options.map((o) => (
          <option key={o.value} value={o.value}>
            {o.label}
          </option>
        ))}
      </select>
    )
  }
  const type =
    field.dataType === "date" || field.dataType === "timestamp"
      ? "date"
      : field.dataType === "numeric" || field.dataType === "integer"
        ? "number"
        : "text"
  return (
    <input
      type={type}
      className={inputClass}
      value={value}
      onChange={(e) => onChange(e.target.value)}
      placeholder="value"
    />
  )
}

function shortId(id: string): string {
  return id.length > 8 ? `${id.slice(0, 8)}…` : id
}

/** A CSV cell -- quoted only when it contains a comma, quote, or newline, with
 * inner quotes doubled (RFC 4180). */
function csvEscape(v: unknown): string {
  const s = v === null || v === undefined ? "" : String(v)
  return /[",\n]/.test(s) ? `"${s.replace(/"/g, '""')}"` : s
}

/** The current result set as CSV: id/name/status then the union of every
 * row's attribute keys (sorted), so a class with optional fields still gets a
 * complete, aligned header. */
function resultsToCsv(rows: QueryResultRow[]): string {
  const attrKeys = Array.from(new Set(rows.flatMap((r) => Object.keys(r.attributes)))).sort()
  const header = ["id", "name", "status", ...attrKeys]
  const lines = [header.map(csvEscape).join(",")]
  for (const r of rows) {
    lines.push([r.id, r.name, r.status, ...attrKeys.map((k) => r.attributes[k])].map(csvEscape).join(","))
  }
  return lines.join("\n")
}

function downloadCsv(filename: string, csv: string) {
  const blob = new Blob([csv], { type: "text/csv;charset=utf-8" })
  const url = URL.createObjectURL(blob)
  const a = document.createElement("a")
  a.href = url
  a.download = filename
  document.body.appendChild(a)
  a.click()
  document.body.removeChild(a)
  URL.revokeObjectURL(url)
}

/** A raw group value -> its human label: a coded/status/subtype value mapped
 * through the group-by field's options, an empty/null value shown as "(none)",
 * everything else stringified. */
function groupLabel(field: QueryField | undefined, value: unknown): string {
  if (value === null || value === undefined || value === "") return "(none)"
  const asStr = typeof value === "boolean" ? (value ? "true" : "false") : String(value)
  const opt = field?.options?.find((o) => o.value === asStr)
  if (opt) return opt.label
  if (typeof value === "boolean") return value ? "Yes" : "No"
  return asStr
}

/** The aggregate result as CSV: the group column (or a single "total" row when
 * there was no group-by), the count, and the roll-up when one was requested. */
function aggregateToCsv(result: AggregateResult, groupByField: QueryField | undefined, aggFn: AggFn): string {
  const header = [groupByField ? groupByField.label : "total", "count"]
  if (aggFn !== "count") header.push(AGG_FN_LABELS[aggFn])
  const lines = [header.map(csvEscape).join(",")]
  if (result.groups.length === 0) {
    const row = ["Total", result.total]
    if (aggFn !== "count") row.push(result.agg ?? "")
    lines.push(row.map(csvEscape).join(","))
  } else {
    for (const g of result.groups) {
      const row: unknown[] = [groupLabel(groupByField, g.value), g.count]
      if (aggFn !== "count") row.push(g.agg ?? "")
      lines.push(row.map(csvEscape).join(","))
    }
  }
  return lines.join("\n")
}

/** Aggregate results: a total header with CSV export, then either a single
 * overall figure (no group-by) or one bar-charted row per group, biggest first.
 * Every count is a drill target -- the overall figure and each group row open
 * the actual features behind it (onDrillAll / onDrillGroup), which then list in
 * place and highlight on the map. */
function AggregateResults({
  result,
  resultClass,
  groupByField,
  aggFn,
  busy,
  onDrillAll,
  onDrillGroup,
}: {
  result: AggregateResult
  resultClass: string
  groupByField: QueryField | undefined
  aggFn: AggFn
  busy: boolean
  onDrillAll: () => void
  onDrillGroup: (value: unknown, label: string) => void
}) {
  const maxCount = result.groups.reduce((m, g) => Math.max(m, g.count), 0)
  const showAgg = aggFn !== "count"

  return (
    <>
      <div className="flex shrink-0 items-center justify-between border-b px-3 py-1.5">
        <span className="text-muted-foreground flex items-center gap-1.5 text-xs">
          <BarChart3 className="size-3.5" />
          {result.total.toLocaleString()} feature{result.total === 1 ? "" : "s"}
          {showAgg && result.groups.length === 0 && result.agg !== undefined && (
            <span>
              · {AGG_FN_LABELS[aggFn]} {formatAgg(result.agg)}
            </span>
          )}
        </span>
        <Button
          variant="ghost"
          size="sm"
          onClick={() => downloadCsv(`${resultClass}-aggregate.csv`, aggregateToCsv(result, groupByField, aggFn))}
        >
          <Download className="size-3.5" /> Export CSV
        </Button>
      </div>

      {result.groups.length === 0 ? (
        <div className="min-h-0 flex-1 overflow-auto p-4">
          <div className="text-3xl font-semibold">{result.total.toLocaleString()}</div>
          <div className="text-muted-foreground text-sm">matching features</div>
          {showAgg && result.agg !== undefined && (
            <div className="mt-3 text-sm">
              {AGG_FN_LABELS[aggFn]}: <span className="font-medium">{formatAgg(result.agg)}</span>
            </div>
          )}
          {result.total > 0 && (
            <Button variant="outline" size="sm" className="mt-4" disabled={busy} onClick={onDrillAll}>
              <ListFilter className="size-3.5" /> Show these features
            </Button>
          )}
        </div>
      ) : (
        <div className="min-h-0 flex-1 overflow-auto">
          <table className="w-full text-sm">
            <thead className="text-muted-foreground bg-popover sticky top-0 border-b text-left text-xs">
              <tr>
                <th className="px-3 py-1.5 font-medium">{groupByField?.label ?? "Value"}</th>
                <th className="w-20 px-3 py-1.5 text-right font-medium">Count</th>
                {showAgg && <th className="w-24 px-3 py-1.5 text-right font-medium">{AGG_FN_LABELS[aggFn]}</th>}
                <th className="w-9 px-2 py-1.5" />
              </tr>
            </thead>
            <tbody>
              {result.groups.map((g, i) => {
                const label = groupLabel(groupByField, g.value)
                return (
                  <tr
                    key={i}
                    className="hover:bg-accent/50 group cursor-pointer border-b last:border-b-0"
                    onClick={() => !busy && onDrillGroup(g.value, label)}
                    title="Show the features in this group"
                  >
                    <td className="px-3 py-1.5">
                      <div className="relative">
                        <div
                          className="bg-primary/10 absolute inset-y-0 left-0 -my-1.5 rounded-r"
                          style={{ width: maxCount > 0 ? `${(g.count / maxCount) * 100}%` : "0%" }}
                        />
                        <span className="relative">{label}</span>
                      </div>
                    </td>
                    <td className="px-3 py-1.5 text-right tabular-nums">{g.count.toLocaleString()}</td>
                    {showAgg && (
                      <td className="text-muted-foreground px-3 py-1.5 text-right tabular-nums">
                        {g.agg !== undefined ? formatAgg(g.agg) : "—"}
                      </td>
                    )}
                    <td className="px-2 py-1.5">
                      <ChevronRight className="text-muted-foreground size-4 opacity-0 group-hover:opacity-100" />
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        </div>
      )}
    </>
  )
}

/** The All-feature-classes breakdown: one row per class that has at least one
 * feature inside the scope, biggest first, with a grand total header. Answers
 * "what -- and how many of each -- is on the map here." Each class row is a drill
 * target -- clicking it opens that class's features within the same scope. */
function MultiClassResults({
  rows,
  busy,
  onDrill,
}: {
  rows: ClassCount[]
  busy: boolean
  onDrill: (classCode: string, className: string) => void
}) {
  const total = rows.reduce((sum, r) => sum + r.count, 0)
  const maxCount = rows.reduce((m, r) => Math.max(m, r.count), 0)

  return (
    <>
      <div className="flex shrink-0 items-center justify-between border-b px-3 py-1.5">
        <span className="text-muted-foreground flex items-center gap-1.5 text-xs">
          <BarChart3 className="size-3.5" />
          {total.toLocaleString()} feature{total === 1 ? "" : "s"} across {rows.length} class
          {rows.length === 1 ? "" : "es"}
        </span>
        <Button
          variant="ghost"
          size="sm"
          onClick={() =>
            downloadCsv(
              "feature-classes-in-scope.csv",
              [
                ["Feature class", "Count"].map(csvEscape).join(","),
                ...rows.map((r) => [r.name, r.count].map(csvEscape).join(",")),
              ].join("\n"),
            )
          }
        >
          <Download className="size-3.5" /> Export CSV
        </Button>
      </div>

      {rows.length === 0 ? (
        <div className="text-muted-foreground min-h-0 flex-1 overflow-auto p-4 text-sm">
          No features found within this scope.
        </div>
      ) : (
        <div className="min-h-0 flex-1 overflow-auto">
          <table className="w-full text-sm">
            <thead className="text-muted-foreground bg-popover sticky top-0 border-b text-left text-xs">
              <tr>
                <th className="px-3 py-1.5 font-medium">Feature class</th>
                <th className="w-20 px-3 py-1.5 text-right font-medium">Count</th>
                <th className="w-9 px-2 py-1.5" />
              </tr>
            </thead>
            <tbody>
              {rows.map((r) => (
                <tr
                  key={r.code}
                  className="hover:bg-accent/50 group cursor-pointer border-b last:border-b-0"
                  onClick={() => !busy && onDrill(r.code, r.name)}
                  title="Show these features"
                >
                  <td className="px-3 py-1.5">
                    <div className="relative">
                      <div
                        className="bg-primary/10 absolute inset-y-0 left-0 -my-1.5 rounded-r"
                        style={{ width: maxCount > 0 ? `${(r.count / maxCount) * 100}%` : "0%" }}
                      />
                      <span className="relative">{r.name}</span>
                    </div>
                  </td>
                  <td className="px-3 py-1.5 text-right tabular-nums">{r.count.toLocaleString()}</td>
                  <td className="px-2 py-1.5">
                    <ChevronRight className="text-muted-foreground size-4 opacity-0 group-hover:opacity-100" />
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </>
  )
}

/** The drill-down view: the actual feature rows behind a clicked count, listed
 * with the same per-row zoom-to-feature button the filter results use -- and,
 * because the rows were pushed to the query-store, all highlighted on the map at
 * once. A "Back to counts" button returns to the aggregate that opened it. */
function DrillResults({ drill, onBack }: { drill: DrillResult; onBack: () => void }) {
  return (
    <>
      <div className="flex shrink-0 items-center justify-between gap-2 border-b px-3 py-1.5">
        <button
          onClick={onBack}
          className="text-muted-foreground hover:text-foreground flex min-w-0 items-center gap-1.5 text-xs"
        >
          <ArrowLeft className="size-3.5 shrink-0" />
          <span className="shrink-0">Back to counts</span>
        </button>
        <div className="flex items-center gap-2">
          <span className="text-muted-foreground shrink-0 text-xs">
            {drill.rows.length} feature{drill.rows.length === 1 ? "" : "s"}
          </span>
          <Button
            variant="ghost"
            size="sm"
            disabled={drill.rows.length === 0}
            onClick={() => downloadCsv(`${drill.classCode}-features.csv`, resultsToCsv(drill.rows))}
          >
            <Download className="size-3.5" /> Export CSV
          </Button>
        </div>
      </div>
      <div className="text-muted-foreground truncate border-b px-3 py-1 text-xs" title={drill.label}>
        {drill.label}
      </div>
      <div className="min-h-0 flex-1 overflow-auto">
        <QueryResultsTable rows={drill.rows} resultClass={drill.classCode} />
      </div>
    </>
  )
}

/** Result count + CSV export header over the results table. */
function QueryResults({ rows, resultClass }: { rows: QueryResultRow[]; resultClass: string }) {
  return (
    <>
      <div className="flex shrink-0 items-center justify-between border-b px-3 py-1.5">
        <span className="text-muted-foreground text-xs">
          {rows.length} result{rows.length === 1 ? "" : "s"}
        </span>
        <Button
          variant="ghost"
          size="sm"
          disabled={rows.length === 0}
          onClick={() => downloadCsv(`${resultClass}-query.csv`, resultsToCsv(rows))}
        >
          <Download className="size-3.5" /> Export CSV
        </Button>
      </div>
      <div className="min-h-0 flex-1 overflow-auto">
        <QueryResultsTable rows={rows} resultClass={resultClass} />
      </div>
    </>
  )
}

/** One row per match, with the same zoom-to-feature button the trace result
 * table uses (select(...,{focus:true})). Every match is already highlighted on
 * the map (voltedge-map.tsx reads the same store), so this table's job is just
 * to list them and let the user fly to any one. */
function QueryResultsTable({ rows, resultClass }: { rows: QueryResultRow[]; resultClass: string }) {
  if (rows.length === 0) {
    return <p className="text-muted-foreground p-3 text-sm">No features matched.</p>
  }
  // Show the tag column only when the query surfaced primary codes (a tag
  // filter was applied) -- an ordinary field query never carries them.
  const showTag = rows.some((r) => r.primary_tag_code)
  return (
    <table className="w-full text-sm">
      <thead className="text-muted-foreground bg-popover sticky top-0 border-b text-left text-xs">
        <tr>
          <th className="px-3 py-1.5 font-medium">Name</th>
          <th className="px-3 py-1.5 font-medium">Status</th>
          {showTag && <th className="px-3 py-1.5 font-medium">Tag</th>}
          <th className="px-3 py-1.5 font-medium">ID</th>
          <th className="w-10 px-2 py-1.5" />
        </tr>
      </thead>
      <tbody>
        {rows.map((row) => (
          <tr key={row.id} className="border-b last:border-b-0">
            <td className="px-3 py-1.5">{row.name || "—"}</td>
            <td className="text-muted-foreground px-3 py-1.5">{formatValue(row.status)}</td>
            {showTag && (
              <td className="text-muted-foreground px-3 py-1.5 font-mono">{row.primary_tag_code || "—"}</td>
            )}
            <td className="text-muted-foreground px-3 py-1.5 font-mono">{shortId(row.id)}</td>
            <td className="px-2 py-1.5">
              <Button
                variant="ghost"
                size="icon"
                className="size-7"
                title="Zoom to feature"
                onClick={() => useSelectionStore.getState().select(resultClass, row.id, { focus: true })}
              >
                <Crosshair className="size-3.5" />
                <span className="sr-only">Zoom to feature</span>
              </Button>
            </td>
          </tr>
        ))}
      </tbody>
    </table>
  )
}
