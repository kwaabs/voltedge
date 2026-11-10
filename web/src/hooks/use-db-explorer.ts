import { useMutation, useQuery } from "@tanstack/react-query"

import { ApiError, apiFetch } from "@/lib/api"

// Hooks for the database explorer (admin:db — admin / break-glass only; see
// api/internal/dbexplorer). A schema browser (GET /db-explorer/objects, columns,
// routine definitions) plus the SQL runner (POST /db-explorer/query).

export interface DbRelation {
  schema: string
  name: string
  /** table / view / materialized_view / partitioned_table / foreign_table */
  kind: string
  row_estimate: number | null
}

export interface DbRoutine {
  schema: string
  name: string
  /** function / procedure / aggregate / window */
  kind: string
  args: string
}

export interface DbCatalog {
  schemas: string[]
  relations: DbRelation[]
  routines: DbRoutine[]
}

export interface DbColumn {
  name: string
  type: string
  not_null: boolean
  default: string | null
}

export interface QueryResult {
  columns: { name: string; type: string }[]
  rows: unknown[][]
  row_count: number
  truncated: boolean
  command: string
  duration_ms: number
}

export interface QueryInput {
  sql: string
  max_rows: number
}

/** The whole catalog of user (non-system) schema objects, for the browser tree. */
export function useDbObjects() {
  return useQuery({
    queryKey: ["db-explorer", "objects"],
    queryFn: () => apiFetch<DbCatalog>("/db-explorer/objects"),
    staleTime: 60_000,
  })
}

/** A relation's columns (enabled only when a relation is selected). */
export function useRelationColumns(schema: string | null, name: string | null) {
  return useQuery({
    queryKey: ["db-explorer", "columns", schema, name],
    queryFn: () => apiFetch<{ columns: DbColumn[] }>(`/db-explorer/relations/${schema}/${name}/columns`),
    enabled: Boolean(schema && name),
    staleTime: 60_000,
  })
}

/** A function/procedure's CREATE definition (enabled only when one is selected). */
export function useRoutineDefinition(schema: string | null, name: string | null) {
  return useQuery({
    queryKey: ["db-explorer", "routine", schema, name],
    queryFn: () => apiFetch<{ definition: string }>(`/db-explorer/routines/${schema}/${name}`),
    enabled: Boolean(schema && name),
    staleTime: 60_000,
  })
}

/** Run one SQL statement. Not cached — each run is an explicit action. */
export function useRunQuery() {
  return useMutation<QueryResult, ApiError, QueryInput>({
    mutationFn: (body) => apiFetch<QueryResult>("/db-explorer/query", { method: "POST", body: JSON.stringify(body) }),
  })
}
