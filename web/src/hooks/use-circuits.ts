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
}

export function useCreateCircuit() {
  return useCircuitMutation<CreateCircuitBody>((body) =>
    apiFetch("/circuits", { method: "POST", body: JSON.stringify(body) }),
  )
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
