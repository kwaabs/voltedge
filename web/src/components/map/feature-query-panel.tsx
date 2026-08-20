import { Crosshair, Download, Loader2, Play, Plus, X } from "lucide-react"
import { useEffect, useMemo, useRef, useState } from "react"

import { formatFieldLabel, formatValue, inputClass } from "@/components/map/field-utils"
import { Button } from "@/components/ui/button"
import { FloatingPanel } from "@/components/ui/floating-panel"
import { type FieldSchema, useFeatureClassSchema } from "@/hooks/use-feature-class-schema"
import { useFeatureClasses } from "@/hooks/use-feature-classes"
import { ApiError, apiFetch } from "@/lib/api"
import { cn } from "@/lib/utils"
import { type QueryResultRow, useQueryStore } from "@/stores/query-store"
import { useSelectionStore } from "@/stores/selection-store"
import { useToastStore } from "@/stores/toast-store"

type DataType = FieldSchema["data_type"]

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
  const { data: schema, isLoading: schemaLoading } = useFeatureClassSchema(code || undefined)

  const nextId = useRef(1)
  const [root, setRoot] = useState<Group>(() => ({ id: 0, match: "all", conditions: [], groups: [] }))
  const [running, setRunning] = useState(false)

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

  const makeCondition = (field: string): Condition => {
    const f = fieldByName.get(field)
    return { id: nextId.current++, field, op: f ? operatorsFor(f)[0] : "eq", value: "", value2: "" }
  }
  const makeGroup = (): Group => ({ id: nextId.current++, match: "all", conditions: [], groups: [] })

  // Reset the whole builder whenever the class changes -- the old class's
  // fields, conditions, and any results are meaningless against a new one.
  useEffect(() => {
    setRoot({ id: nextId.current++, match: "all", conditions: [], groups: [] })
    useQueryStore.getState().clearResults()
  }, [code])

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
    const body = { ...serializeGroup(root, fieldByName), limit: 200 }
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

  return (
    <FloatingPanel
      title="Query builder"
      onClose={() => useQueryStore.getState().closePanel()}
      initialRect={{ x: 16, y: 60, width: 480, height: 500 }}
      minWidth={400}
    >
      <div className="flex h-full flex-col">
        <div className="flex flex-col gap-3 border-b p-3">
          <div className="flex flex-col gap-1">
            <label className="text-xs font-medium">Feature class</label>
            <select className={inputClass} value={code} onChange={(e) => setCode(e.target.value)}>
              <option value="">Select a feature class…</option>
              {queryableClasses.map((c) => (
                <option key={c.code} value={c.code}>
                  {c.name}
                </option>
              ))}
            </select>
          </div>

          {code && schemaLoading && (
            <p className="text-muted-foreground flex items-center gap-2 text-sm">
              <Loader2 className="size-4 animate-spin" /> Loading attributes…
            </p>
          )}

          {code && !schemaLoading && fields.length > 0 && (
            <>
              <GroupEditor group={root} depth={0} isRoot actions={actions} />
              <div className="flex justify-end">
                <Button size="sm" onClick={run} disabled={running}>
                  {running ? <Loader2 className="size-3.5 animate-spin" /> : <Play className="size-3.5" />}
                  Run
                </Button>
              </div>
            </>
          )}
        </div>

        <div className="flex min-h-0 flex-1 flex-col">
          {results !== null && resultClass === code && <QueryResults rows={results} resultClass={code} />}
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
  return (
    <table className="w-full text-sm">
      <thead className="text-muted-foreground bg-popover sticky top-0 border-b text-left text-xs">
        <tr>
          <th className="px-3 py-1.5 font-medium">Name</th>
          <th className="px-3 py-1.5 font-medium">Status</th>
          <th className="px-3 py-1.5 font-medium">ID</th>
          <th className="w-10 px-2 py-1.5" />
        </tr>
      </thead>
      <tbody>
        {rows.map((row) => (
          <tr key={row.id} className="border-b last:border-b-0">
            <td className="px-3 py-1.5">{row.name || "—"}</td>
            <td className="text-muted-foreground px-3 py-1.5">{formatValue(row.status)}</td>
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
