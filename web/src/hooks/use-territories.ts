import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"

import { ApiError, apiFetch } from "@/lib/api"
import type { TerritoryGeometry } from "@/stores/territory-edit-store"

/** One territory in the tree, boundary-free -- the shape GET /territories
 * returns (api/internal/territory/http.go). `parent_id` is null at the
 * root; `level_rank` orders levels top-first (0 = utility). */
export interface TerritorySummary {
  id: string
  parent_id: string | null
  name: string
  level_code: string
  level_name: string
  level_rank: number
}

interface TerritoriesResponse {
  territories: TerritorySummary[]
}

/**
 * Server state, so Query -- same reasoning as useFeatureClasses. Territories
 * are fixed administrative reference data (loaded with the schema, no
 * in-app authoring), so like the feature-class catalog they change rarely
 * and get a long staleTime rather than polling. Backs the read-only
 * territory name shown on a feature: the feature carries a bare
 * territory_id uuid, resolved to a name from this list (see
 * territoryNameMap).
 */
export function useTerritories() {
  return useQuery({
    queryKey: ["territories"],
    queryFn: () => apiFetch<TerritoriesResponse>("/territories"),
    staleTime: 5 * 60_000,
    retry: 1,
  })
}

/** Builds an id -> name lookup from the territory list, for resolving a
 * feature's territory_id to a human label. */
export function territoryNameMap(territories: TerritorySummary[] | undefined): Record<string, string> {
  if (!territories) return {}
  return Object.fromEntries(territories.map((t) => [t.id, t.name]))
}

/** One level in the catalog -- the shape GET /territories/levels returns.
 * `rank` orders levels top-first (0 = utility); exactly one level carries
 * `is_min_feature_level` (the shallowest a feature must reach). */
export interface TerritoryLevel {
  id: string
  code: string
  name: string
  rank: number
  is_min_feature_level: boolean
}

/** Properties carried on each boundary feature from
 * GET /territories/boundaries -- identity + level, enough for the overlay's
 * styling and its read-only click panel without a second fetch. */
export interface TerritoryBoundaryProps {
  id: string
  parent_id: string | null
  name: string
  level_code: string
  level_name: string
  level_rank: number
}

/** A GeoJSON FeatureCollection of the drawn territory boundaries. Typed
 * loosely on geometry (OpenLayers' GeoJSON reader consumes the raw object);
 * only the properties are modeled, since that's what the app reads. */
export interface TerritoryBoundaryCollection {
  type: "FeatureCollection"
  features: {
    type: "Feature"
    id: string
    geometry: unknown
    properties: TerritoryBoundaryProps
  }[]
}

/**
 * The drawn territory boundaries for the map overlay. Only fetched when the
 * overlay is actually turned on (`enabled`) -- boundaries are large polygons
 * and most sessions never open the overlay. Invalidated by the admin
 * mutations (they touch the ["territory-boundaries"] key) so a re-drawn
 * boundary shows up without a reload.
 */
export function useTerritoryBoundaries(enabled: boolean) {
  return useQuery({
    queryKey: ["territory-boundaries"],
    queryFn: () => apiFetch<TerritoryBoundaryCollection>("/territories/boundaries"),
    enabled,
    staleTime: 5 * 60_000,
  })
}

/** The level catalog, for the create form's level picker. Reference data,
 * so a long staleTime like the territory list. */
export function useTerritoryLevels() {
  return useQuery({
    queryKey: ["territory-levels"],
    queryFn: () => apiFetch<{ levels: TerritoryLevel[] }>("/territories/levels"),
    staleTime: 5 * 60_000,
  })
}

// --- Admin mutations (Slice F/G) -------------------------------------------
// All gated by territories:write on the server; the admin page only renders
// for callers who hold it. Each mutation invalidates the territory list (and
// the boundary overlay, when present) so every consumer -- the tree, the
// feature label, the map overlay -- reflects the change.

function useTerritoryMutation<TBody>(fn: (body: TBody) => Promise<unknown>) {
  const queryClient = useQueryClient()
  return useMutation<unknown, ApiError, TBody>({
    mutationFn: fn,
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["territories"] })
      queryClient.invalidateQueries({ queryKey: ["territory-boundaries"] })
    },
  })
}

export interface CreateTerritoryBody {
  parent_id: string | null
  level_id: string
  name: string
}

export function useCreateTerritory() {
  return useTerritoryMutation<CreateTerritoryBody>((body) =>
    apiFetch("/territories", { method: "POST", body: JSON.stringify(body) }),
  )
}

/** Rename (`name`) and/or re-parent (`reparent: true` + `parent_id`, null =
 * make it a root). `reparent` must be set for `parent_id` to take effect --
 * the server can't otherwise tell "move to root" from "leave alone".
 * `geometry` replaces the boundary (a GeoJSON Polygon/MultiPolygon, Phase 11
 * Slice I's in-map authoring); `clear_geom` removes it (the two are mutually
 * exclusive -- the server lets clear_geom win). */
export interface UpdateTerritoryBody {
  name?: string
  reparent?: boolean
  parent_id?: string | null
  geometry?: TerritoryGeometry
  clear_geom?: boolean
}

export function useUpdateTerritory() {
  return useTerritoryMutation<{ id: string; body: UpdateTerritoryBody }>(({ id, body }) =>
    apiFetch(`/territories/${id}`, { method: "PATCH", body: JSON.stringify(body) }),
  )
}

export function useDeleteTerritory() {
  return useTerritoryMutation<{ id: string }>(({ id }) =>
    apiFetch(`/territories/${id}`, { method: "DELETE" }),
  )
}

// --- Phase 58 rollout: coverage, backfill, enforcement toggle --------------
// The staged steps an admin runs before turning territory scoping on: review
// which features are unassigned (coverage), assign the ones a boundary covers
// (backfill), then flip the flag (enforcement). All under territories:write on
// the mount, except the two GETs which ride features:read.

/** One class's territory-assignment status from GET /territories/coverage:
 * total live geometric features and how many still carry no territory_id
 * (invisible to scoped users once enforcement is on). */
export interface TerritoryCoverageRow {
  class_code: string
  total: number
  unassigned: number
}

interface TerritoryCoverageResponse {
  classes: TerritoryCoverageRow[]
  enforcement: boolean
}

/** The unassigned-features review view + current enforcement state. Not cached
 * long -- it reflects live feature data that a backfill changes, so it's
 * invalidated by useBackfillTerritories. */
export function useTerritoryCoverage() {
  return useQuery({
    queryKey: ["territory-coverage"],
    queryFn: () => apiFetch<TerritoryCoverageResponse>("/territories/coverage"),
    staleTime: 30_000,
  })
}

/** Per-class result of the backfill sweep from POST /territories/backfill:
 * how many features it newly assigned and how many remain uncovered. */
export interface TerritoryBackfillClass {
  class_code: string
  assigned: number
  remaining: number
}

export interface TerritoryBackfillResult {
  classes: TerritoryBackfillClass[]
  total_assigned: number
  total_remaining: number
}

/** Runs the spatial backfill sweep, then refreshes the coverage report so the
 * table shows the new (lower) unassigned counts. */
export function useBackfillTerritories() {
  const queryClient = useQueryClient()
  return useMutation<TerritoryBackfillResult, ApiError, void>({
    mutationFn: () => apiFetch<TerritoryBackfillResult>("/territories/backfill", { method: "POST" }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["territory-coverage"] })
    },
  })
}

/** Flips the territory_enforcement_enabled flag (PUT /territories/enforcement).
 * Invalidates coverage (which carries the enforcement state) so the toggle and
 * banner reflect the new value. */
export function useSetEnforcement() {
  const queryClient = useQueryClient()
  return useMutation<{ enforcement: boolean }, ApiError, boolean>({
    mutationFn: (enabled) =>
      apiFetch<{ enforcement: boolean }>("/territories/enforcement", {
        method: "PUT",
        body: JSON.stringify({ enabled }),
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["territory-coverage"] })
    },
  })
}
