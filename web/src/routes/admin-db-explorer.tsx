import { useMemo, useState } from "react"
import {
  Database,
  Eye,
  FunctionSquare,
  Loader2,
  Play,
  Search,
  ShieldAlert,
  Table2,
  TriangleAlert,
} from "lucide-react"

import { inputClass } from "@/components/map/field-utils"
import { Button } from "@/components/ui/button"
import {
  type DbRelation,
  type DbRoutine,
  useDbObjects,
  useRelationColumns,
  useRoutineDefinition,
  useRunQuery,
} from "@/hooks/use-db-explorer"
import { cn } from "@/lib/utils"

type Tab = "results" | "structure"

function relationIcon(kind: string) {
  if (kind === "view" || kind === "materialized_view") return Eye
  return Table2
}

/** The database explorer (admin:db — admin / break-glass only): browse schema
 * objects and run SQL. The server gates every call, defaults the runner to a
 * read-only transaction, caps rows, and audits each execution. */
export function AdminDbExplorerPage() {
  const objects = useDbObjects()
  const run = useRunQuery()

  const [sql, setSql] = useState("")
  const [limit, setLimit] = useState(1000)
  const [tab, setTab] = useState<Tab>("results")

  const [selRel, setSelRel] = useState<DbRelation | null>(null)
  const [selRoutine, setSelRoutine] = useState<DbRoutine | null>(null)
  const [search, setSearch] = useState("")

  function runSql(text: string) {
    const trimmed = text.trim()
    if (!trimmed) return
    setTab("results")
    run.mutate({ sql: trimmed, max_rows: limit })
  }

  // Opening a table/view shows its DATA (like any DB tool): run SELECT * and switch
  // to Results. The Structure tab stays populated (columns) for the same relation.
  function queryRelation(rel: DbRelation) {
    const q = `select * from "${rel.schema}"."${rel.name}" limit ${limit};`
    setSql(q)
    runSql(q)
  }
  function selectRelation(rel: DbRelation) {
    setSelRoutine(null)
    setSelRel(rel)
    queryRelation(rel)
  }
  function selectRoutine(ro: DbRoutine) {
    setSelRel(null)
    setSelRoutine(ro)
    setTab("structure")
  }

  return (
    // The app shell is min-h-svh (it grows with content), so h-full wouldn't bound
    // this IDE-style page — pin it to the viewport minus the app header (h-14) and
    // the admin back-bar (h-10) = 6rem, so the panels scroll internally instead of
    // the whole page.
    <div className="flex h-[calc(100svh-6rem)] flex-col gap-4 overflow-hidden p-4">
      <div className="shrink-0">
        <h1 className="flex items-center gap-2 text-lg font-semibold">
          <Database className="size-5" />
          Database explorer
        </h1>
        <p className="text-muted-foreground text-sm">
          Browse tables, views, functions and procedures, and run SQL directly against the database.
        </p>
        <div className="mt-2 flex items-start gap-2 rounded-md border border-amber-300 bg-amber-50 px-3 py-2 text-xs text-amber-900 dark:border-amber-900/50 dark:bg-amber-950/40 dark:text-amber-200">
          <ShieldAlert className="mt-0.5 size-4 shrink-0" />
          <span>
            Admin-only tool. Queries are <strong>read-only</strong> — every statement runs in a read-only transaction, so
            writes (<code>UPDATE</code>/<code>DELETE</code>/DDL) are rejected. Each run has a 15s timeout and a row cap, and
            is recorded in the server audit log.
          </span>
        </div>
      </div>

      <div className="grid min-h-0 min-w-0 flex-1 gap-4 lg:grid-cols-[18rem_minmax(0,1fr)] lg:grid-rows-[minmax(0,1fr)]">
        {/* Object browser */}
        <div className="flex min-h-0 flex-col rounded-lg border">
          <div className="flex items-center gap-2 border-b px-2">
            <Search className="text-muted-foreground size-3.5 shrink-0" />
            <input
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              placeholder="Search objects…"
              className="w-full bg-transparent py-2 text-sm outline-none"
            />
          </div>
          <div className="min-h-0 flex-1 overflow-y-auto p-1">
            {objects.isPending ? (
              <div className="text-muted-foreground flex items-center gap-2 p-2 text-sm">
                <Loader2 className="size-4 animate-spin" /> Loading…
              </div>
            ) : objects.isError ? (
              <div className="text-destructive p-2 text-sm">Failed to load objects.</div>
            ) : (
              <ObjectTree
                catalog={objects.data!}
                search={search}
                selected={selRel ? `${selRel.schema}.${selRel.name}` : selRoutine ? `${selRoutine.schema}.${selRoutine.name}` : null}
                onRelation={selectRelation}
                onRoutine={selectRoutine}
              />
            )}
          </div>
        </div>

        {/* Editor + results */}
        <div className="flex min-h-0 min-w-0 flex-col gap-3">
          <div className="flex shrink-0 flex-col gap-2 rounded-lg border p-3">
            <textarea
              value={sql}
              onChange={(e) => setSql(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) {
                  e.preventDefault()
                  runSql(sql)
                }
              }}
              spellCheck={false}
              placeholder="SELECT * FROM gis.pole LIMIT 100;"
              className="h-40 resize-y rounded-md border bg-transparent p-2 font-mono text-sm outline-none"
            />
            <div className="flex flex-wrap items-center gap-3">
              <Button onClick={() => runSql(sql)} disabled={run.isPending || !sql.trim()}>
                {run.isPending ? <Loader2 className="mr-1.5 size-4 animate-spin" /> : <Play className="mr-1.5 size-4" />}
                Run
              </Button>
              <label className="text-muted-foreground flex items-center gap-1.5 text-xs">
                Row limit
                <input
                  type="number"
                  min={1}
                  max={5000}
                  value={limit}
                  onChange={(e) => setLimit(Math.max(1, Math.min(5000, Number(e.target.value) || 1)))}
                  className={cn(inputClass, "w-20")}
                />
              </label>
              <span className="text-muted-foreground text-xs">⌘/Ctrl+Enter to run · read-only</span>
            </div>
          </div>

          {/* Tabs */}
          <div className="flex shrink-0 gap-1 border-b text-sm">
            <TabButton active={tab === "results"} onClick={() => setTab("results")}>
              Results
            </TabButton>
            <TabButton active={tab === "structure"} onClick={() => setTab("structure")}>
              Structure
            </TabButton>
          </div>

          <div className="min-h-0 flex-1 overflow-auto rounded-lg border">
            {tab === "results" ? (
              <ResultsPanel run={run} />
            ) : (
              <StructurePanel relation={selRel} routine={selRoutine} onQueryRelation={queryRelation} />
            )}
          </div>
        </div>
      </div>
    </div>
  )
}

function TabButton({ active, onClick, children }: { active: boolean; onClick: () => void; children: React.ReactNode }) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={cn(
        "-mb-px border-b-2 px-3 py-1.5 font-medium",
        active ? "border-primary text-foreground" : "text-muted-foreground border-transparent hover:text-foreground",
      )}
    >
      {children}
    </button>
  )
}

function ResultsPanel({ run }: { run: ReturnType<typeof useRunQuery> }) {
  if (run.isPending) {
    return (
      <div className="text-muted-foreground flex items-center gap-2 p-4 text-sm">
        <Loader2 className="size-4 animate-spin" /> Running…
      </div>
    )
  }
  if (run.isError) {
    return (
      <div className="m-3 flex items-start gap-2 rounded-md border border-red-300 bg-red-50 p-3 text-sm text-red-800 dark:border-red-900/50 dark:bg-red-950/40 dark:text-red-200">
        <TriangleAlert className="mt-0.5 size-4 shrink-0" />
        <pre className="whitespace-pre-wrap font-mono text-xs">{run.error.message}</pre>
      </div>
    )
  }
  const data = run.data
  if (!data) {
    return <div className="text-muted-foreground p-4 text-sm">Run a query to see results.</div>
  }

  return (
    <div className="flex h-full flex-col">
      <div className="text-muted-foreground flex flex-wrap items-center gap-x-3 gap-y-1 border-b px-3 py-1.5 text-xs">
        <span className="text-foreground font-medium">{data.command || "OK"}</span>
        {data.columns.length > 0 && (
          <span>
            {data.row_count} row{data.row_count === 1 ? "" : "s"}
          </span>
        )}
        <span>{data.duration_ms} ms</span>
        {data.truncated && <span className="font-medium text-amber-600">truncated (row cap reached)</span>}
      </div>

      {data.columns.length > 0 ? (
        <div className="min-h-0 flex-1 overflow-auto">
          <table className="w-full border-collapse text-xs">
            <thead className="bg-muted/50 sticky top-0">
              <tr>
                <th className="text-muted-foreground border-b px-2 py-1 text-right font-normal">#</th>
                {data.columns.map((c, i) => (
                  <th key={i} className="border-b px-2 py-1 text-left font-semibold">
                    {c.name}
                    <span className="text-muted-foreground ml-1 font-normal lowercase">{c.type}</span>
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {data.rows.map((row, ri) => (
                <tr key={ri} className="hover:bg-muted/30">
                  <td className="text-muted-foreground border-b px-2 py-1 text-right tabular-nums">{ri + 1}</td>
                  {row.map((cell, ci) => (
                    <td key={ci} className="max-w-[24rem] truncate border-b px-2 py-1 font-mono" title={cellText(cell)}>
                      {cell === null ? <span className="text-muted-foreground italic">NULL</span> : cellText(cell)}
                    </td>
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : (
        <div className="text-muted-foreground p-4 text-sm">Statement executed successfully. No result set.</div>
      )}
    </div>
  )
}

function cellText(v: unknown): string {
  if (v === null || v === undefined) return ""
  if (typeof v === "object") return JSON.stringify(v)
  return String(v)
}

function StructurePanel({
  relation,
  routine,
  onQueryRelation,
}: {
  relation: DbRelation | null
  routine: DbRoutine | null
  onQueryRelation: (rel: DbRelation) => void
}) {
  const columns = useRelationColumns(relation?.schema ?? null, relation?.name ?? null)
  const def = useRoutineDefinition(routine?.schema ?? null, routine?.name ?? null)

  if (relation) {
    return (
      <div className="flex h-full flex-col">
        <div className="flex items-center justify-between gap-2 border-b px-3 py-2">
          <div className="text-sm">
            <span className="font-semibold">
              {relation.schema}.{relation.name}
            </span>
            <span className="text-muted-foreground ml-2 text-xs">{relation.kind.replace(/_/g, " ")}</span>
          </div>
          <Button size="sm" variant="outline" onClick={() => onQueryRelation(relation)}>
            <Play className="mr-1.5 size-3.5" />
            SELECT *
          </Button>
        </div>
        <div className="min-h-0 flex-1 overflow-auto">
          {columns.isPending ? (
            <div className="text-muted-foreground flex items-center gap-2 p-3 text-sm">
              <Loader2 className="size-4 animate-spin" /> Loading columns…
            </div>
          ) : (
            <table className="w-full border-collapse text-xs">
              <thead className="bg-muted/50 sticky top-0">
                <tr>
                  <th className="border-b px-2 py-1 text-left font-semibold">Column</th>
                  <th className="border-b px-2 py-1 text-left font-semibold">Type</th>
                  <th className="border-b px-2 py-1 text-left font-semibold">Null</th>
                  <th className="border-b px-2 py-1 text-left font-semibold">Default</th>
                </tr>
              </thead>
              <tbody>
                {(columns.data?.columns ?? []).map((c) => (
                  <tr key={c.name} className="hover:bg-muted/30">
                    <td className="border-b px-2 py-1 font-mono font-medium">{c.name}</td>
                    <td className="border-b px-2 py-1 font-mono">{c.type}</td>
                    <td className="text-muted-foreground border-b px-2 py-1">{c.not_null ? "NOT NULL" : ""}</td>
                    <td className="text-muted-foreground border-b px-2 py-1 font-mono">{c.default ?? ""}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>
      </div>
    )
  }

  if (routine) {
    return (
      <div className="flex h-full flex-col">
        <div className="border-b px-3 py-2 text-sm">
          <span className="font-semibold">
            {routine.schema}.{routine.name}
          </span>
          <span className="text-muted-foreground ml-2 text-xs">{routine.kind}</span>
        </div>
        <div className="min-h-0 flex-1 overflow-auto p-3">
          {def.isPending ? (
            <div className="text-muted-foreground flex items-center gap-2 text-sm">
              <Loader2 className="size-4 animate-spin" /> Loading definition…
            </div>
          ) : (
            <pre className="whitespace-pre-wrap font-mono text-xs">{def.data?.definition ?? "—"}</pre>
          )}
        </div>
      </div>
    )
  }

  return <div className="text-muted-foreground p-4 text-sm">Select a table, view or function to inspect it.</div>
}

function ObjectTree({
  catalog,
  search,
  selected,
  onRelation,
  onRoutine,
}: {
  catalog: ReturnType<typeof useDbObjects>["data"] & object
  search: string
  selected: string | null
  onRelation: (rel: DbRelation) => void
  onRoutine: (ro: DbRoutine) => void
}) {
  const needle = search.trim().toLowerCase()
  const match = (name: string) => !needle || name.toLowerCase().includes(needle)

  const bySchema = useMemo(() => {
    const map = new Map<string, { relations: DbRelation[]; routines: DbRoutine[] }>()
    for (const s of catalog.schemas) map.set(s, { relations: [], routines: [] })
    for (const r of catalog.relations) {
      if (!match(r.name)) continue
      if (!map.has(r.schema)) map.set(r.schema, { relations: [], routines: [] })
      map.get(r.schema)!.relations.push(r)
    }
    for (const r of catalog.routines) {
      if (!match(r.name)) continue
      if (!map.has(r.schema)) map.set(r.schema, { relations: [], routines: [] })
      map.get(r.schema)!.routines.push(r)
    }
    return map
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [catalog, needle])

  const schemas = Array.from(bySchema.keys()).sort()

  return (
    <div className="flex flex-col">
      {schemas.map((schema) => {
        const group = bySchema.get(schema)!
        const total = group.relations.length + group.routines.length
        if (needle && total === 0) return null
        return (
          <details key={schema} open={Boolean(needle)} className="group">
            <summary className="hover:bg-muted/50 flex cursor-pointer items-center gap-1.5 rounded px-1.5 py-1 text-sm font-medium">
              <Database className="text-muted-foreground size-3.5" />
              {schema}
              <span className="text-muted-foreground ml-auto text-xs">{total}</span>
            </summary>
            <div className="ml-3 flex flex-col border-l pl-1">
              {group.relations.map((rel) => {
                const Icon = relationIcon(rel.kind)
                const id = `${rel.schema}.${rel.name}`
                return (
                  <button
                    key={id}
                    type="button"
                    onClick={() => onRelation(rel)}
                    className={cn(
                      "flex items-center gap-1.5 rounded px-1.5 py-1 text-left text-sm",
                      selected === id ? "bg-primary/10 text-primary" : "hover:bg-muted/50",
                    )}
                    title={rel.kind.replace(/_/g, " ")}
                  >
                    <Icon className="size-3.5 shrink-0 opacity-70" />
                    <span className="truncate">{rel.name}</span>
                  </button>
                )
              })}
              {group.routines.map((ro) => {
                const id = `${ro.schema}.${ro.name}`
                return (
                  <button
                    key={`${id}(${ro.args})`}
                    type="button"
                    onClick={() => onRoutine(ro)}
                    className={cn(
                      "flex items-center gap-1.5 rounded px-1.5 py-1 text-left text-sm",
                      selected === id ? "bg-primary/10 text-primary" : "hover:bg-muted/50",
                    )}
                    title={`${ro.kind}(${ro.args})`}
                  >
                    <FunctionSquare className="size-3.5 shrink-0 opacity-70" />
                    <span className="truncate">{ro.name}</span>
                  </button>
                )
              })}
            </div>
          </details>
        )
      })}
    </div>
  )
}
