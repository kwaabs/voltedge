import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"

import { ApiError, apiFetch } from "@/lib/api"

/** The two voltage namespaces a circuit lives in. An MV "Old Age 2" is a
 * different circuit from any LV one of the same name (api/internal/circuit). */
export type CircuitNetwork = "MV" | "LV"

/** One circuit -- the shape GET /circuits returns
 * (api/internal/circuit/http.go). `voltage_kv` and the source reference are
 * null until known; the source points at the feature a later slice's
 * assign-by-trace starts from. */
export interface Circuit {
  id: string
  name: string
  network: CircuitNetwork
  voltage_kv: number | null
  source_feature_class: string | null
  source_feature_id: string | null
  created_at: string
  updated_at: string
  /** When assign-by-trace last full-synced this circuit's membership (migration
   * 0130); null = never assigned. */
  members_synced_at: string | null
  /** Computed freshness flag: true when a member feature was edited since
   * members_synced_at, so the stamped membership may be stale and the circuit
   * should be re-assigned. */
  stale: boolean
}

interface CircuitsResponse {
  circuits: Circuit[]
}

/**
 * Server state, so Query -- same reasoning as useTerritories. The circuit
 * catalog is reference data read alongside features (a feature's circuit_id
 * resolves to a circuit name from this list), changing rarely, so it gets a
 * long staleTime rather than polling.
 */
export function useCircuits() {
  return useQuery({
    queryKey: ["circuits"],
    queryFn: () => apiFetch<CircuitsResponse>("/circuits"),
    staleTime: 5 * 60_000,
    retry: 1,
  })
}

/** Builds an id -> name lookup from the circuit list, for resolving a
 * feature's circuit_id to a human label. */
export function circuitNameMap(circuits: Circuit[] | undefined): Record<string, string> {
  if (!circuits) return {}
  return Object.fromEntries(circuits.map((c) => [c.id, c.name]))
}

// --- Admin mutations -------------------------------------------------------
// All gated by circuits:write on the server; the admin page only renders for
// callers who hold it. Each invalidates the circuit list so every consumer
// reflects the change.

function useCircuitMutation<TBody>(fn: (body: TBody) => Promise<unknown>) {
  const queryClient = useQueryClient()
  return useMutation<unknown, ApiError, TBody>({
    mutationFn: fn,
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["circuits"] })
    },
  })
}

export interface CreateCircuitBody {
  name: string
  network: CircuitNetwork
  voltage_kv?: number | null
  source_feature_class?: string | null
  source_feature_id?: string | null
}

/** Creates a circuit and returns it (with its new id), so a caller can chain a
 * source-set + assign off a freshly created circuit. */
export function useCreateCircuit() {
  const queryClient = useQueryClient()
  return useMutation<Circuit, ApiError, CreateCircuitBody>({
    mutationFn: (body) => apiFetch<Circuit>("/circuits", { method: "POST", body: JSON.stringify(body) }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["circuits"] })
    },
  })
}

/** The counts AssignCircuit returns (api/internal/circuit/assign.go): how many
 * features the downstream trace from the source reached, how many were stamped,
 * and how many prior members were cleared by the full sync. */
export interface AssignCircuitResult {
  circuit_id: string
  circuit_name: string
  network: string
  reached: number
  stamped: number
  cleared: number
  per_class: { feature_class: string; stamped: number }[]
  /** Reached features in this network that COULDN'T be stamped because their class
   * has no active circuit_id field (almost always: the class carries the field but
   * hasn't been recompiled). Nonzero skipped means "recompile these classes, then
   * re-assign". */
  skipped: number
  skipped_per_class?: { feature_class: string; stamped: number }[]
}

/** Runs assign-by-trace: POST /circuits/{id}/assign traces downstream from the
 * circuit's source feature and full-syncs circuit_id onto every reached member.
 * Requires the circuit to already have a source set. Assign is a bulk feature write
 * (it changes many rows' circuit_id), so it invalidates the circuit list AND the
 * feature caches so panels/browsers reflect the new membership. */
export function useAssignCircuit() {
  const queryClient = useQueryClient()
  return useMutation<AssignCircuitResult, ApiError, { id: string }>({
    mutationFn: ({ id }) => apiFetch<AssignCircuitResult>(`/circuits/${id}/assign`, { method: "POST" }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["circuits"] })
      queryClient.invalidateQueries({ queryKey: ["feature"] })
      queryClient.invalidateQueries({ queryKey: ["features"] })
      queryClient.invalidateQueries({ queryKey: ["feature-rows"] })
    },
  })
}

/** Rename (`name`), set/clear voltage (`voltage_kv`, or `clear_voltage`), or
 * set/clear the source reference. Network is immutable (delete + recreate to
 * change it). `clear_voltage`/`clear_source` distinguish "make it unknown"
 * from "leave alone". */
export interface UpdateCircuitBody {
  name?: string
  voltage_kv?: number | null
  clear_voltage?: boolean
  source_feature_class?: string | null
  source_feature_id?: string | null
  clear_source?: boolean
}

export function useUpdateCircuit() {
  return useCircuitMutation<{ id: string; body: UpdateCircuitBody }>(({ id, body }) =>
    apiFetch(`/circuits/${id}`, { method: "PATCH", body: JSON.stringify(body) }),
  )
}

export function useDeleteCircuit() {
  return useCircuitMutation<{ id: string }>(({ id }) =>
    apiFetch(`/circuits/${id}`, { method: "DELETE" }),
  )
}
