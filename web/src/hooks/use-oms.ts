import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"

import { ApiError, apiFetch } from "@/lib/api"

// Client mirror of the Outage Management API (api/internal/oms). An outage is
// an operational transaction, not feature data, so it has its own /oms surface
// gated by oms:read / oms:write.

export type CallType = "no_supply" | "partial" | "flicker"
export type CallStatus = "open" | "associated" | "cleared" | "cancelled"
export type IncidentStatus =
  | "predicted"
  | "scheduled"
  | "confirmed"
  | "dispatched"
  | "restored"
  | "cancelled"

/** One customer report of no/partial supply (oms.trouble_calls). Longitude/
 * Latitude are the projected point (null when the call carries no location). */
export interface TroubleCall {
  id: string
  service_point_id: string | null
  address: string | null
  longitude: number | null
  latitude: number | null
  reported_at: string
  caller_ref: string | null
  call_type: CallType
  status: CallStatus
  incident_id: string | null
  notes: string | null
  created_at: string
  updated_at: string
}

/** One inferred (or manually raised) outage (oms.incidents). The predicted
 * device is the protective device the rollup blamed; both refs are null for an
 * unattributed cluster. Confidence is null when not predicted. */
export interface Incident {
  id: string
  predicted_device_class: string | null
  predicted_device_id: string | null
  circuit_id: string | null
  outage_type: string
  cause_code: string | null
  status: IncidentStatus
  confidence: number | null
  started_at: string
  planned_start: string | null
  planned_end: string | null
  etr: string | null
  restored_at: string | null
  affected_customers: number | null
  affected_service_points: number | null
  crew_ref: string | null
  notes: string | null
  created_at: string
  updated_at: string
}

/** One affected service point in an incident's outage cone, with the WGS84
 * point the map overlay draws. */
export interface AffectedServicePoint {
  id: string
  name: string
  longitude: number
  latitude: number
}

/** An incident's geographic impact: the blamed device (if any) plus the service
 * points in its outage cone. Frozen once confirmed, live-traced while predicted. */
export interface AffectedArea {
  incident_id: string
  device_class: string | null
  device_id: string | null
  service_points: AffectedServicePoint[]
}

/** System reliability indices (IEEE 1366) over the confirmed interruption
 * history, for the analysis window [from, to). SAIDI/CAIDI are in minutes;
 * SAIFI is interruptions per customer; ENS (kWh) is ESTIMATED — interrupted
 * customer-hours × avg_load_kw (no measured per-customer load yet). */
export interface ReliabilityIndices {
  from: string
  to: string
  /** Echoed only when the report was scoped to a single circuit. */
  circuit_id?: string
  total_customers: number
  customers_interrupted: number
  interruption_events: number
  customer_minutes: number
  ongoing_interruptions: number
  saifi: number
  saidi: number
  caidi: number
  ens_kwh: number
  ens_estimated: boolean
  avg_load_kw: number
  /** Per-fault-cause decomposition of SAIDI/SAIFI (buckets sum to the totals).
   * Omitted when the faults module is not installed; see fault_breakdown_available. */
  by_fault_cause?: FaultCauseContribution[]
  fault_breakdown_available: boolean
}

/** One fault-cause bucket's share of the window's unreliability, over the same
 * customer denominator as the system indices so the buckets sum to SAIDI/SAIFI.
 * `cause` is the linked fault's recorded cause; null when the fault has no cause
 * OR (fault_linked=false) when the incident had no fault event at all — the two
 * are told apart by fault_linked, not by cause. */
export interface FaultCauseContribution {
  cause: string | null
  fault_linked: boolean
  customers_interrupted: number
  interruption_events: number
  customer_minutes: number
  saifi: number
  saidi: number
}

/** An incident's LIVE de-energized cone: what is cut off from supply right now
 * given the network's current switch positions (as opposed to AffectedArea's
 * frozen "who was out at confirmation"). device_state is the blamed device's
 * live position; service_points are the currently-dead supplies (empty when the
 * device is closed / supply restored). Grows and shrinks as switching steps are
 * executed. */
export interface EnergizationState {
  incident_id: string
  device_class: string | null
  device_id: string | null
  device_state?: string
  service_points: AffectedServicePoint[]
  customers: number
}

/** An incident's live de-energized cone, for the console map + KPI. Skipped
 * until an incidentId is supplied. Short staleTime so it tracks switching. */
export function useIncidentEnergization(incidentId: string | null) {
  return useQuery({
    queryKey: [...OMS_KEY, "energization", incidentId ?? ""],
    queryFn: () => apiFetch<EnergizationState>(`/oms/incidents/${incidentId}/energization`),
    enabled: incidentId != null,
    staleTime: 10_000,
  })
}

/** An incident's frozen affected area (blamed device + outage-cone service
 * points), for the console map base layer. Skipped until an incidentId is
 * supplied. */
export function useAffectedArea(incidentId: string | null) {
  return useQuery({
    queryKey: [...OMS_KEY, "affected", incidentId ?? ""],
    queryFn: () => apiFetch<AffectedArea>(`/oms/incidents/${incidentId}/affected`),
    enabled: incidentId != null,
    staleTime: 30_000,
  })
}

export type SwitchingOrderStatus = "draft" | "issued" | "in_progress" | "completed" | "cancelled"
export type StepPhase = "isolate" | "restore"
export type StepAction = "open" | "close" | "earth" | "verify" | "note"

/** One operation in a switching order. device_class/device_id name the operated
 * device (null for a verify/note step); executed_at is set when ticked off. */
export interface SwitchingStep {
  id: string
  switching_order_id: string
  step_no: number
  phase: StepPhase
  action: StepAction
  device_class: string | null
  device_id: string | null
  instruction: string
  executed_at: string | null
  created_at: string
}

/** The ordered open/verify/close program for an incident, with its steps. */
export interface SwitchingOrder {
  id: string
  incident_id: string
  reference: string | null
  status: SwitchingOrderStatus
  notes: string | null
  created_at: string
  updated_at: string
  steps: SwitchingStep[]
}

interface SwitchingOrdersResponse {
  switching_orders: SwitchingOrder[]
}

interface TroubleCallsResponse {
  trouble_calls: TroubleCall[]
}
interface IncidentsResponse {
  incidents: Incident[]
}

const OMS_KEY = ["oms"] as const

/** The trouble-call queue, newest first, optionally filtered by status. */
export function useTroubleCalls(status?: CallStatus) {
  const query = status ? `?status=${status}` : ""
  return useQuery({
    queryKey: [...OMS_KEY, "trouble-calls", status ?? "all"],
    queryFn: () => apiFetch<TroubleCallsResponse>(`/oms/trouble-calls${query}`),
    staleTime: 15_000,
  })
}

/** The incident board. `openOnly` narrows to unrestored incidents. */
export function useIncidents(opts: { status?: IncidentStatus; openOnly?: boolean } = {}) {
  const params = new URLSearchParams()
  if (opts.status) params.set("status", opts.status)
  if (opts.openOnly) params.set("open", "true")
  const query = params.toString() ? `?${params.toString()}` : ""
  return useQuery({
    queryKey: [...OMS_KEY, "incidents", opts.status ?? "all", opts.openOnly ?? false],
    queryFn: () => apiFetch<IncidentsResponse>(`/oms/incidents${query}`),
    staleTime: 15_000,
  })
}

/** System reliability indices over an optional window (RFC3339 from/to) with an
 * optional per-customer load assumption for the estimated ENS. `circuitId`
 * scopes the whole report to one circuit's service points (per-feeder indices).
 * Undefined params fall back to the server defaults (trailing 12 months, 1 kW,
 * whole system). */
export function useReliabilityIndices(
  opts: { from?: string; to?: string; avgLoadKw?: number; circuitId?: string } = {},
) {
  const params = new URLSearchParams()
  if (opts.from) params.set("from", opts.from)
  if (opts.to) params.set("to", opts.to)
  if (opts.avgLoadKw != null) params.set("avg_load_kw", String(opts.avgLoadKw))
  if (opts.circuitId) params.set("circuit_id", opts.circuitId)
  const query = params.toString() ? `?${params.toString()}` : ""
  return useQuery({
    queryKey: [...OMS_KEY, "indices", opts.from ?? "", opts.to ?? "", opts.avgLoadKw ?? "", opts.circuitId ?? ""],
    queryFn: () => apiFetch<ReliabilityIndices>(`/oms/indices${query}`),
    staleTime: 30_000,
  })
}

// --- Mutations -------------------------------------------------------------
// All gated by oms:write on the server; the page self-gates the controls. Each
// invalidates the whole /oms cache, since a prediction/lifecycle step touches
// both the incident board and the call queue.

function useOmsMutation<TBody>(fn: (body: TBody) => Promise<unknown>) {
  const queryClient = useQueryClient()
  return useMutation<unknown, ApiError, TBody>({
    mutationFn: fn,
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: OMS_KEY })
    },
  })
}

/** SP-optional intake: at least one of service_point_id / address /
 * (longitude+latitude) must be present (the server enforces it). */
export interface CreateTroubleCallBody {
  service_point_id?: string
  address?: string
  longitude?: number
  latitude?: number
  caller_ref?: string
  call_type?: CallType
  notes?: string
}

export function useCreateTroubleCall() {
  return useOmsMutation<CreateTroubleCallBody>((body) =>
    apiFetch("/oms/trouble-calls", { method: "POST", body: JSON.stringify(body) }),
  )
}

/** Resolve a call's service point: pass service_point_id for a manual
 * association, or snap=true to bind the nearest SP to the call's location. */
export interface AssociateTroubleCallBody {
  service_point_id?: string
  snap?: boolean
}

export function useAssociateTroubleCall() {
  return useOmsMutation<{ id: string; body: AssociateTroubleCallBody }>(({ id, body }) =>
    apiFetch(`/oms/trouble-calls/${id}/associate`, { method: "POST", body: JSON.stringify(body) }),
  )
}

/** Roll open, SP-resolved calls up into predicted incidents. */
export function usePredictIncidents() {
  return useOmsMutation<void>(() => apiFetch("/oms/incidents/predict", { method: "POST" }))
}

/** Schedule a planned outage around operating one device. planned_start/_end are
 * RFC3339 timestamps; the device is required. */
export interface CreatePlannedOutageBody {
  device_class: string
  device_id: string
  planned_start?: string
  planned_end?: string
  cause_code?: string
  notes?: string
}

export function useCreatePlannedOutage() {
  return useOmsMutation<CreatePlannedOutageBody>((body) =>
    apiFetch("/oms/incidents/planned", { method: "POST", body: JSON.stringify(body) }),
  )
}

// --- Switching orders ------------------------------------------------------

/** An incident's switching orders (with steps), newest first. Skipped until an
 * incidentId is supplied (used lazily when a switching panel is expanded). */
export function useSwitchingOrders(incidentId: string | null) {
  return useQuery({
    queryKey: [...OMS_KEY, "switching-orders", incidentId ?? ""],
    queryFn: () => apiFetch<SwitchingOrdersResponse>(`/oms/incidents/${incidentId}/switching-orders`),
    enabled: incidentId != null,
    staleTime: 15_000,
  })
}

/** Auto-generate a draft switching order from the incident's device. */
export function useGenerateSwitchingOrder() {
  return useOmsMutation<{ incidentId: string }>(({ incidentId }) =>
    apiFetch(`/oms/incidents/${incidentId}/switching-orders`, { method: "POST" }),
  )
}

/** Mark a switching step done (executed=true) or outstanding again. */
export function useExecuteStep() {
  return useOmsMutation<{ stepId: string; executed: boolean }>(({ stepId, executed }) =>
    apiFetch(`/oms/switching-steps/${stepId}/execute`, {
      method: "POST",
      body: JSON.stringify({ executed }),
    }),
  )
}

/** Cancel a switching order (steps are kept for the record). */
export function useCancelSwitchingOrder() {
  return useOmsMutation<{ orderId: string }>(({ orderId }) =>
    apiFetch(`/oms/switching-orders/${orderId}/cancel`, { method: "POST" }),
  )
}

export type IncidentAction = "confirm" | "restore" | "cancel"

/** Advance an incident's lifecycle (confirm / restore / cancel). */
export function useIncidentAction() {
  return useOmsMutation<{ id: string; action: IncidentAction }>(({ id, action }) =>
    apiFetch(`/oms/incidents/${id}/${action}`, { method: "POST" }),
  )
}
