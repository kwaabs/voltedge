import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"

import { ApiError, apiFetch } from "@/lib/api"
import type { ServiceRollup } from "@/stores/trace-store"

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
  /** Cause certainty: unknown -> suspected -> confirmed (migration 0168). */
  cause_status: string
  /** How the utility learned of the outage (0168): customer | ami | scada | crew |
   * planned | upstream | analytics. Stamped at raise, correctable; null on a
   * pre-0168 row. */
  detection_source: string | null
  /** Optional free provenance note for the source (channel / external id). */
  detection_detail: string | null
  /** When the first signal arrived (0168) — distinct from started_at. */
  detected_at: string | null
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
  /** The user who last advanced this incident (confirm/dispatch/restore/cancel),
   * or null for an engine-predicted incident no one has touched yet. */
  last_actor_user_id: string | null
  /** Display name of last_actor_user_id, resolved server-side (no admin:users
   * needed to read it). Null when unattributed or the account was removed. */
  last_actor_name: string | null
  notes: string | null
  created_at: string
  updated_at: string
}

/** One affected service point in an incident's outage cone, with the WGS84
 * point the map overlay draws. `feature_class` + `customers` are stamped by the
 * /affected reader (omitted / 0 on the bare map-overlay payload) so the console
 * can show a supply-by-supply table, not just a headline sum. */
export interface AffectedServicePoint {
  id: string
  name: string
  longitude: number
  latitude: number
  feature_class?: string
  customers?: number
}

/** One territory's share of an incident's affected set at a hierarchy level. */
export interface AffectedTerritoryRow {
  territory_id: string
  territory_name: string
  service_points: number
  customers: number
}

/** The affected set grouped by the territories at one level (all regions, all
 * districts, …), biggest-first. */
export interface AffectedTerritoryLevel {
  level_code: string
  level_name: string
  level_rank: number
  rows: AffectedTerritoryRow[]
}

/** An incident's geographic impact: the blamed device (if any) plus the service
 * points in its outage cone. Frozen once confirmed, live-traced while predicted. */
export interface AffectedArea {
  incident_id: string
  device_class: string | null
  device_id: string | null
  service_points: AffectedServicePoint[]
  /** Phase 62 consumption rollup of the affected cone -- load + customers split
   * by restoration tier, so the console can show critical customers hit, not
   * just a raw count. See ServiceRollup (trace-store.ts). */
  service?: ServiceRollup
  /** Affected customers rolled up the geographic hierarchy (region / district /
   * …), one entry per level that has affected supplies. */
  by_territory: AffectedTerritoryLevel[]
  /** How many affected supplies no longer resolve to a map point (deleted /
   * geometry-less) — they count toward the total but can't be drawn. */
  missing_geometry: number
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
  /** Phase 62 consumption rollup of the live dead cone -- load + customers by
   * restoration tier (service.critical = the rank <= P2 subset). See
   * ServiceRollup (trace-store.ts). */
  service?: ServiceRollup
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

// --- Map overlay -----------------------------------------------------------
// The GIS-map overlay (oms-overlay-store.ts) draws every incident anchored to a
// resolvable predicted device, as a status-colored marker on the device geometry
// -- the twin of the fault overlay. GET /oms/incidents/map returns only the
// incidents that resolved to a point (longitude/latitude guaranteed non-null). It
// fetches every status once; the legend filters client-side, so revealing
// restored/cancelled incidents never re-hits the server. The two click-through
// cones reuse useAffectedArea (frozen) + useIncidentEnergization (live) above.

/** A GeoJSON geometry object (as OpenLayers' GeoJSON reader consumes it). Defined
 * locally rather than shared with use-faults so the OMS overlay carries no
 * dependency on the (optional) faults module. */
export interface GeoJsonGeometry {
  type: string
  coordinates: unknown
}

/** One incident projected onto the map (GET /oms/incidents/map). Extends the
 * incident with its predicted device's geometry: `geometry` is the device's full
 * shape (line / polygon / point) for the highlight, and longitude/latitude are
 * the marker point on that geometry. */
export interface IncidentMapMarker extends Incident {
  longitude: number
  latitude: number
  geometry: GeoJsonGeometry | null
}

interface IncidentMapResponse {
  incidents: IncidentMapMarker[]
}

/** Every incident with a placeable marker, for the GIS-map Outages overlay.
 * Disabled until `enabled` (the layer is toggled on) so a hidden overlay costs
 * nothing. */
export function useIncidentsForMap(enabled = true) {
  return useQuery({
    queryKey: [...OMS_KEY, "map"],
    queryFn: () => apiFetch<IncidentMapResponse>(`/oms/incidents/map`),
    enabled,
    staleTime: 15_000,
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

// --- FLISR restoration (back-feed suggestion) ------------------------------

/** A bare feature reference as the OMS restoration API returns it. */
export interface OmsFeatureRef {
  feature_class: string
  feature_id: string
}

export type CapacityVerdict = "ok" | "exceeds" | "unverified"

/** One suggested back-feed: close `tie` to re-energize the dead section from
 * `alternate_source`. Capacity verdict judges whether that feeder can carry the
 * picked-up load (unverified when no transformer nameplate was found). */
export interface RestorationPlan {
  tie: OmsFeatureRef
  tie_label: string
  alternate_source?: OmsFeatureRef
  alternate_source_label?: string
  restored_service_points: number
  restored_customers: number
  restored_load_kw: number
  restored_load_kva: number
  alternate_feeder_load_kva: number
  alternate_capacity_kva?: number
  spare_headroom_kva?: number
  capacity_verdict: CapacityVerdict
  /** How capacity_verdict was decided: a real donor-feeder load-flow sweep
   * ("load_flow", authoritative), the nameplate summed-demand estimate
   * ("nameplate"), or neither ("none"). */
  verdict_basis: "load_flow" | "nameplate" | "none"
  /** Load-flow check results (present when verdict_basis === "load_flow", or when
   * the sweep ran but was inconclusive). */
  load_flow_checked: boolean
  load_flow_feasible: boolean
  /** Post-transfer voltage violations and thermal overloads the sweep found. */
  violations: number
  overloaded: number
  /** Worst per-unit voltage reached on the donor feeder once the section transfers. */
  worst_voltage_pu?: number
  /** Engine note when the sweep was inconclusive (loop/mesh, no load gained) or
   * degraded (sweep error). */
  load_flow_note?: string
}

/** FLISR answer for one faulted device: the dead section's totals plus every
 * viable back-feed plan, best-first. `note` explains an empty plan list. */
export interface RestorationSuggestion {
  device: OmsFeatureRef
  de_energized_service_points: number
  de_energized_customers: number
  de_energized_load_kw: number
  de_energized_load_kva: number
  plans: RestorationPlan[]
  note?: string
}

/** One lifecycle event in a call/incident timeline (oms.events). actor_name is
 * resolved server-side; null for an engine/system step (prediction, SCADA, AMI). */
export interface TimelineEvent {
  id: string
  entity_kind: "call" | "incident"
  entity_id: string
  event_type: string
  actor_user_id: string | null
  actor_name: string | null
  note: string | null
  occurred_at: string
  created_at: string
}

interface TimelineResponse {
  events: TimelineEvent[]
}

/** A trouble call's timeline — its own events plus the incident it rolled into,
 * merged chronologically. Skipped until a callId is given (lazy, on expand). */
export function useCallTimeline(callId: string | null) {
  return useQuery({
    queryKey: [...OMS_KEY, "call-timeline", callId ?? ""],
    queryFn: () => apiFetch<TimelineResponse>(`/oms/trouble-calls/${callId}/timeline`),
    enabled: callId != null,
    staleTime: 15_000,
  })
}

/** An incident's timeline — its lifecycle plus the received events of its calls. */
export function useIncidentTimeline(incidentId: string | null) {
  return useQuery({
    queryKey: [...OMS_KEY, "incident-timeline", incidentId ?? ""],
    queryFn: () => apiFetch<TimelineResponse>(`/oms/incidents/${incidentId}/timeline`),
    enabled: incidentId != null,
    staleTime: 15_000,
  })
}

interface TroubleCallsResponse {
  trouble_calls: TroubleCall[]
  /** Count under the filter, ignoring limit/offset — for the page controls. */
  total?: number
}
interface IncidentsResponse {
  incidents: Incident[]
}

const OMS_KEY = ["oms"] as const

/** The trouble-call queue, newest first. `status` filters; `incidentId` scopes to
 * one incident's calls; `limit`/`offset` paginate (limit omitted = all, so the
 * incident-scoped and small-deployment reads are unchanged). Keeps the previous
 * page visible while the next loads. */
export function useTroubleCalls(
  opts: { status?: CallStatus | ""; incidentId?: string; limit?: number; offset?: number } = {},
) {
  const params = new URLSearchParams()
  if (opts.status) params.set("status", opts.status)
  if (opts.incidentId) params.set("incident_id", opts.incidentId)
  if (opts.limit != null) params.set("limit", String(opts.limit))
  if (opts.offset != null) params.set("offset", String(opts.offset))
  const query = params.toString() ? `?${params.toString()}` : ""
  return useQuery({
    queryKey: [
      ...OMS_KEY,
      "trouble-calls",
      opts.status ?? "all",
      opts.incidentId ?? "",
      opts.limit ?? 0,
      opts.offset ?? 0,
    ],
    queryFn: () => apiFetch<TroubleCallsResponse>(`/oms/trouble-calls${query}`),
    staleTime: 15_000,
    placeholderData: (prev) => prev,
  })
}

/** One coded outage cause (oms.outage_causes) — the coarse incident-level "why".
 * The detailed per-asset FMEA lives on the fault. */
export interface OutageCause {
  code: string
  label: string
  family: string
  sort_order: number
  active: boolean
}

interface OutageCausesResponse {
  outage_causes: OutageCause[]
}

/** The coded cause catalog for the incident cause picker. Rarely changes. */
export function useOutageCauses() {
  return useQuery({
    queryKey: [...OMS_KEY, "outage-causes"],
    queryFn: () => apiFetch<OutageCausesResponse>("/oms/outage-causes"),
    staleTime: 5 * 60_000,
  })
}

/** The incident board. `openOnly` narrows to unrestored incidents; `territoryId`
 * scopes to that territory's subtree (region catches its districts). */
export function useIncidents(opts: { status?: IncidentStatus; openOnly?: boolean; territoryId?: string } = {}) {
  const params = new URLSearchParams()
  if (opts.status) params.set("status", opts.status)
  if (opts.openOnly) params.set("open", "true")
  if (opts.territoryId) params.set("territory_id", opts.territoryId)
  const query = params.toString() ? `?${params.toString()}` : ""
  return useQuery({
    queryKey: [...OMS_KEY, "incidents", opts.status ?? "all", opts.openOnly ?? false, opts.territoryId ?? ""],
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

/** One detection-source bucket in the source/cause analytics (how we learned). */
export interface DetectionSourceStat {
  source: string
  incidents: number
  customers_affected: number
}

/** One cause-family bucket in the source/cause analytics (why it happened). */
export interface CauseFamilyStat {
  family: string
  incidents: number
  customers_affected: number
}

/** The "source & cause" payoff metrics over a window (GET /oms/detection-metrics):
 * the detection-source mix, the machine-detected (awareness) share, the
 * cause-family breakdown, and the mean detection->restore time. */
export interface DetectionMetrics {
  from: string
  to: string
  total_incidents: number
  restored_incidents: number
  by_source: DetectionSourceStat[]
  machine_detected_incidents: number
  machine_detected_share: number
  by_cause: CauseFamilyStat[]
  avg_detection_to_restore_minutes: number | null
}

/** Source/cause analytics over an optional RFC3339 window (default 12 months). */
export function useDetectionMetrics(opts: { from?: string; to?: string } = {}) {
  const params = new URLSearchParams()
  if (opts.from) params.set("from", opts.from)
  if (opts.to) params.set("to", opts.to)
  const query = params.toString() ? `?${params.toString()}` : ""
  return useQuery({
    queryKey: [...OMS_KEY, "detection-metrics", opts.from ?? "", opts.to ?? ""],
    queryFn: () => apiFetch<DetectionMetrics>(`/oms/detection-metrics${query}`),
    staleTime: 30_000,
  })
}

/** One territory's share of the live network-wide outage impact. */
export interface AreaImpactRow {
  territory_id: string
  territory_name: string
  customers: number
  service_points: number
  incidents: number
}

/** The impact grouped by the territories at one hierarchy level. */
export interface AreaImpactLevel {
  level_code: string
  level_name: string
  level_rank: number
  rows: AreaImpactRow[]
}

/** Network-wide outage impact across all open incidents (GET /oms/area-impact):
 * totals + the geographic breakdown + an unassigned bucket. */
export interface AreaImpact {
  open_incidents: number
  total_customers: number
  total_service_points: number
  unassigned: AreaImpactRow
  levels: AreaImpactLevel[]
}

/** One likely protective device to attribute an incident to (upstream of its
 * calls). `nearest` = the prediction's own first choice; `support` = callers
 * through it. The name is resolved client-side. */
export interface DeviceCandidate {
  feature_class: string
  feature_id: string
  support: number
  nearest: boolean
}

/** The protective devices to suggest in the re-attribution picker. */
export function useIncidentDeviceCandidates(incidentId: string | null) {
  return useQuery({
    queryKey: [...OMS_KEY, "device-candidates", incidentId ?? ""],
    queryFn: () => apiFetch<{ candidates: DeviceCandidate[] }>(`/oms/incidents/${incidentId}/device-candidates`),
    enabled: incidentId != null,
    staleTime: 30_000,
  })
}

/** Network-wide customers-out rolled by territory, across all open incidents. */
export function useAreaImpact() {
  return useQuery({
    queryKey: [...OMS_KEY, "area-impact"],
    queryFn: () => apiFetch<AreaImpact>("/oms/area-impact"),
    staleTime: 30_000,
  })
}

/** One geographic cluster of open trouble calls (GET /oms/trouble-spots) — a
 * place a crew can be dispatched to before/without prediction finding a device. */
export interface TroubleSpot {
  call_ids: string[]
  service_point_ids: string[]
  call_count: number
  centroid_lng: number
  centroid_lat: number
  radius_m: number
  territory_id: string | null
  territory_name: string | null
  dominant_call_type: CallType
  has_ami: boolean
  earliest_reported_at: string
  latest_reported_at: string
}

interface TroubleSpotsResponse {
  trouble_spots: TroubleSpot[]
}

/** The geographic lens: open calls clustered into trouble spots (`eps` = join
 * distance in metres, default 250). Short staleTime — it tracks the call queue. */
export function useTroubleSpots(opts: { eps?: number } = {}) {
  const query = opts.eps != null ? `?eps=${opts.eps}` : ""
  return useQuery({
    queryKey: [...OMS_KEY, "trouble-spots", opts.eps ?? ""],
    queryFn: () => apiFetch<TroubleSpotsResponse>(`/oms/trouble-spots${query}`),
    staleTime: 15_000,
  })
}

// --- crews (blocker #2) ----------------------------------------------------

/** A field crew / resource (GET /oms/crews). `status` is available / dispatched /
 * on_site / unavailable; `active_incident_id` is its current job (if any). */
export interface Crew {
  id: string
  name: string
  kind: string | null
  status: string
  territory_id: string | null
  territory_name: string | null
  phone: string | null
  active: boolean
  member_count: number
  active_incident_id: string | null
  active_assignment_id: string | null
  active_since: string | null
  created_at: string
  updated_at: string
}

/** A user on a crew (GET /oms/crews/{id}/members). `display_name`/`email` are
 * joined from iam.users; `role` is free text (lead / member). */
export interface CrewMember {
  id: string
  crew_id: string
  user_id: string
  role: string | null
  added_at: string
  display_name: string | null
  email: string | null
}

/** A crew's engagement on one incident (dispatched → on_site → cleared). */
export interface CrewAssignment {
  id: string
  crew_id: string
  incident_id: string
  status: string
  dispatched_at: string
  on_site_at: string | null
  cleared_at: string | null
  note: string | null
  assigned_by: string | null
  crew_name: string | null
  /** Set when dispatching also raised a restoration work order for the incident. */
  work_order_id?: string | null
  created_at: string
  updated_at: string
}

/** The crew roster. `includeInactive` also returns retired crews. */
export function useCrews(opts: { includeInactive?: boolean } = {}) {
  const query = opts.includeInactive ? "?include_inactive=true" : ""
  return useQuery({
    queryKey: [...OMS_KEY, "crews", opts.includeInactive ?? false],
    queryFn: () => apiFetch<{ crews: Crew[] }>(`/oms/crews${query}`),
    staleTime: 30_000,
  })
}

/** The crew assignments on one incident. */
export function useIncidentCrews(incidentId: string | null) {
  return useQuery({
    queryKey: [...OMS_KEY, "incident-crews", incidentId ?? ""],
    queryFn: () => apiFetch<{ assignments: CrewAssignment[] }>(`/oms/incidents/${incidentId}/crews`),
    enabled: incidentId != null,
    staleTime: 15_000,
  })
}

/** The members on a crew. */
export function useCrewMembers(crewId: string | null) {
  return useQuery({
    queryKey: [...OMS_KEY, "crew-members", crewId ?? ""],
    queryFn: () => apiFetch<{ members: CrewMember[] }>(`/oms/crews/${crewId}/members`),
    enabled: crewId != null,
    staleTime: 30_000,
  })
}

/** Add a user to a crew (idempotent; re-adding updates the role). */
export function useAddCrewMember() {
  return useOmsMutation<{ crewId: string; user_id: string; role?: string }>(({ crewId, user_id, role }) =>
    apiFetch(`/oms/crews/${crewId}/members`, { method: "POST", body: JSON.stringify({ user_id, role }) }),
  )
}

/** Remove a user from a crew. */
export function useRemoveCrewMember() {
  return useOmsMutation<{ crewId: string; userId: string }>(({ crewId, userId }) =>
    apiFetch(`/oms/crews/${crewId}/members/${userId}`, { method: "DELETE" }),
  )
}

export interface CrewBody {
  name: string
  kind?: string
  phone?: string
  territory_id?: string
}

export function useCreateCrew() {
  return useOmsMutation<CrewBody>((body) => apiFetch("/oms/crews", { method: "POST", body: JSON.stringify(body) }))
}

export function useUpdateCrew() {
  return useOmsMutation<{ id: string; body: CrewBody }>(({ id, body }) =>
    apiFetch(`/oms/crews/${id}`, { method: "PATCH", body: JSON.stringify(body) }),
  )
}

export function useSetCrewAvailability() {
  return useOmsMutation<{ id: string; available: boolean }>(({ id, available }) =>
    apiFetch(`/oms/crews/${id}/availability`, { method: "POST", body: JSON.stringify({ available }) }),
  )
}

export function useDeactivateCrew() {
  return useOmsMutation<{ id: string }>(({ id }) => apiFetch(`/oms/crews/${id}`, { method: "DELETE" }))
}

/** Dispatch a crew to an incident (creates a 'dispatched' assignment). Set
 * `raise_work_order` to also raise a crew-assigned restoration work order for the
 * incident (reaches every crew member's field pack). */
export function useDispatchCrew() {
  return useOmsMutation<{
    incidentId: string
    crew_id: string
    note?: string
    raise_work_order?: boolean
    work_order_title?: string
  }>(({ incidentId, crew_id, note, raise_work_order, work_order_title }) =>
    apiFetch(`/oms/incidents/${incidentId}/crews`, {
      method: "POST",
      body: JSON.stringify({ crew_id, note, raise_work_order, work_order_title }),
    }),
  )
}

/** Advance a crew assignment: on_site or cleared. */
export function useAdvanceAssignment() {
  return useOmsMutation<{ id: string; status: string }>(({ id, status }) =>
    apiFetch(`/oms/crew-assignments/${id}`, { method: "PATCH", body: JSON.stringify({ status }) }),
  )
}

/** Set or clear an incident's ETR (RFC3339, or null to clear). */
export function useSetIncidentETR() {
  return useOmsMutation<{ id: string; etr: string | null }>(({ id, etr }) =>
    apiFetch(`/oms/incidents/${id}/etr`, { method: "PATCH", body: JSON.stringify({ etr }) }),
  )
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

/** Dispatch a crew to a trouble spot: raise a confirmed, unattributed incident
 * from its open calls (geographic lens, no device needed). */
export function useRaiseIncidentFromSpot() {
  return useOmsMutation<{ call_ids: string[] }>((body) =>
    apiFetch("/oms/trouble-spots/raise", { method: "POST", body: JSON.stringify(body) }),
  )
}

/** Refine an incident's cause (code + status) and/or correct its detection
 * source — a partial update, only the supplied fields apply. `cause_code: ""`
 * clears the cause. */
export interface SetAttributionBody {
  cause_code?: string
  cause_status?: string
  detection_source?: string
  detection_detail?: string
}

export function useSetIncidentAttribution() {
  return useOmsMutation<{ id: string; body: SetAttributionBody }>(({ id, body }) =>
    apiFetch(`/oms/incidents/${id}/attribution`, { method: "PATCH", body: JSON.stringify(body) }),
  )
}

/** Set or change the protective device an incident is blamed on (operator
 * override): re-traces the cone, re-stamps affected + territory, keeps the calls. */
export function useAttributeIncidentDevice() {
  return useOmsMutation<{ id: string; device_class: string; device_id: string }>(({ id, device_class, device_id }) =>
    apiFetch(`/oms/incidents/${id}/device`, {
      method: "PATCH",
      body: JSON.stringify({ device_class, device_id }),
    }),
  )
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

/** FLISR: suggested back-feed restoration plans for an active incident (the
 * normally-open ties that could re-energize its dead section from an alternate
 * source). Skipped until an incidentId is supplied and `enabled`; short staleTime
 * so it tracks switching. */
export function useRestorationSuggestion(incidentId: string | null, enabled = true) {
  return useQuery({
    queryKey: [...OMS_KEY, "restoration", incidentId ?? ""],
    queryFn: () => apiFetch<RestorationSuggestion>(`/oms/incidents/${incidentId}/restoration`),
    enabled: incidentId != null && enabled,
    staleTime: 10_000,
  })
}

/** Build a draft close-tie restoration order from a chosen plan's tie. */
export function useGenerateRestorationOrder() {
  return useOmsMutation<{ incidentId: string; tieClass: string; tieId: string }>(
    ({ incidentId, tieClass, tieId }) =>
      apiFetch(`/oms/incidents/${incidentId}/restoration-orders`, {
        method: "POST",
        body: JSON.stringify({ tie_class: tieClass, tie_id: tieId }),
      }),
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

export type IncidentAction = "confirm" | "dispatch" | "restore" | "cancel"

/** Advance an incident's lifecycle (confirm / dispatch / restore / cancel). */
export function useIncidentAction() {
  return useOmsMutation<{ id: string; action: IncidentAction }>(({ id, action }) =>
    apiFetch(`/oms/incidents/${id}/${action}`, { method: "POST" }),
  )
}
