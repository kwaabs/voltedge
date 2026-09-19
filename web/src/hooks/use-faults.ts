import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"

import { ApiError, apiFetch } from "@/lib/api"

// Client mirror of the Faults API (api/internal/faults). A fault is the
// electrical fault EVENT on the network -- the causal hinge that explains an OMS
// outage and triggers a WAM defect. It has its own /faults surface gated by
// faults:read / faults:write.

export type FaultType = "SLG" | "LL" | "LLG" | "LLL" | "LLLG" | "earth" | "unknown"
export type FaultStatus = "detected" | "investigating" | "cleared" | "closed"

/** One electrical fault event (faults.fault_events). asset_class/asset_id name
 * the affected feature (both null when the fault is anchored only by a location
 * pin or the incident it caused). incident_id links the OMS outage it caused;
 * defect_id the WAM defect it booked. Longitude/Latitude are the projected pin
 * (null when none). */
export interface FaultEvent {
  id: string
  reference: string | null
  asset_class: string | null
  asset_id: string | null
  longitude: number | null
  latitude: number | null
  fault_type: FaultType | null
  cause: string | null
  fault_current_a: number | null
  protective_device_class: string | null
  protective_device_id: string | null
  clearing_time_ms: number | null
  status: FaultStatus
  detected_by: string | null
  detected_at: string
  cleared_at: string | null
  incident_id: string | null
  defect_id: string | null
  title: string
  description: string | null
  notes: string | null
  created_at: string
  updated_at: string
}

interface FaultsResponse {
  faults: FaultEvent[]
}

const FAULTS_KEY = ["faults"] as const

/** The fault board, newest first. `openOnly` narrows to faults still on the
 * network (neither cleared nor closed); type/asset/incident filters are optional.
 * assetClass+assetId scope the board to a single feature (the map's Faults tab
 * passes both to show that asset's fault history). */
export function useFaults(
  opts: {
    status?: FaultStatus
    faultType?: FaultType
    assetClass?: string
    assetId?: string
    incidentId?: string
    openOnly?: boolean
  } = {},
) {
  const params = new URLSearchParams()
  if (opts.openOnly) params.set("open", "true")
  else if (opts.status) params.set("status", opts.status)
  if (opts.faultType) params.set("fault_type", opts.faultType)
  if (opts.assetClass) params.set("asset_class", opts.assetClass)
  if (opts.assetId) params.set("asset_id", opts.assetId)
  if (opts.incidentId) params.set("incident_id", opts.incidentId)
  const query = params.toString() ? `?${params.toString()}` : ""
  return useQuery({
    queryKey: [
      ...FAULTS_KEY,
      "list",
      opts.status ?? "all",
      opts.faultType ?? "",
      opts.assetClass ?? "",
      opts.assetId ?? "",
      opts.incidentId ?? "",
      opts.openOnly ?? false,
    ],
    queryFn: () => apiFetch<FaultsResponse>(`/faults${query}`),
    staleTime: 15_000,
  })
}

/** One row of a fault's audit trail (faults.fault_event_history). action is
 * created / status_changed / updated / defect_booked; actor_name/email resolve
 * the acting user (both null for a system write or a since-deleted user); at is
 * the timestamp; snapshot is the full fault state after that change. */
export interface FaultHistoryEntry {
  id: number
  action: string
  actor_id: string | null
  actor_name: string | null
  actor_email: string | null
  at: string
  snapshot: FaultEvent
}

/** A fault's change timeline, newest first. Disabled until `enabled` (the row's
 * history panel is opened) so the board doesn't fetch every fault's history up
 * front. */
export function useFaultHistory(faultId: string | undefined, enabled = true) {
  return useQuery({
    queryKey: [...FAULTS_KEY, "history", faultId ?? ""],
    queryFn: () => apiFetch<{ history: FaultHistoryEntry[] }>(`/faults/${faultId}/history`),
    enabled: enabled && !!faultId,
    staleTime: 15_000,
  })
}

// --- Map overlay -----------------------------------------------------------
// The overlay (fault-overlay-store.ts) draws every fault that has a placeable
// point. An asset-anchored fault is drawn ON its asset: an on-geometry marker
// point PLUS the asset's full geometry, so a fault on a line/polygon highlights
// the whole faulted asset instead of a triangle floating at its centroid. A
// location-anchored fault keeps its own pin (no asset geometry). GET /faults/map
// returns only the faults that resolved to a point (longitude/latitude guaranteed
// non-null). It fetches every status once; the legend filters client-side, so
// revealing cleared/closed faults never re-hits the server.

/** A GeoJSON geometry object (as OpenLayers' GeoJSON reader consumes it). */
export interface GeoJsonGeometry {
  type: string
  coordinates: unknown
}

/** One fault projected onto the map (GET /faults/map). Extends the fault event
 * with the asset's geometry: `geometry` is the asset's full shape (line / polygon
 * / point) for an asset-anchored fault, and null for a location-only fault.
 * longitude/latitude are the marker point -- on the asset geometry for an
 * asset-anchored fault, or the location pin otherwise. */
export interface FaultMapMarker extends FaultEvent {
  geometry: GeoJsonGeometry | null
}

interface FaultMapResponse {
  faults: FaultMapMarker[]
}

export function useFaultMapMarkers(enabled = true) {
  return useQuery({
    queryKey: [...FAULTS_KEY, "map"],
    queryFn: () => apiFetch<FaultMapResponse>(`/faults/map`),
    enabled,
    staleTime: 15_000,
  })
}

/** One customer supply point in a fault's downstream impact, with the point the
 * overlay draws. Shape mirrors the OMS affected-area response. */
export interface FaultImpactPoint {
  id: string
  name: string
  longitude: number
  latitude: number
}

/** A fault's de-energized downstream section: the protective device that
 * operated plus every service point below it (empty when the fault has no
 * protective device). Drawn with the same rose halos as the OMS outage cone. */
export interface FaultImpact {
  fault_id: string
  device_class: string | null
  device_id: string | null
  service_points: FaultImpactPoint[]
}

/** A fault's impact area, fetched when its marker is selected on the map. */
export function useFaultImpact(faultId: string | undefined, enabled = true) {
  return useQuery({
    queryKey: [...FAULTS_KEY, "impact", faultId ?? ""],
    queryFn: () => apiFetch<FaultImpact>(`/faults/${faultId}/impact`),
    enabled: enabled && !!faultId,
    staleTime: 15_000,
  })
}

// --- Mutations -------------------------------------------------------------
// All gated by faults:write on the server; the page self-gates the controls.
// Booking a defect from a fault also touches the WAM boards, so that mutation
// additionally invalidates the /wam cache.

function useFaultsMutation<TBody>(fn: (body: TBody) => Promise<unknown>, alsoInvalidate: readonly string[][] = []) {
  const queryClient = useQueryClient()
  return useMutation<unknown, ApiError, TBody>({
    mutationFn: fn,
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: FAULTS_KEY })
      for (const key of alsoInvalidate) queryClient.invalidateQueries({ queryKey: key })
    },
  })
}

/** Log a fault event. A title is required, plus an anchor: an asset (class + id),
 * a location, or an incident_id (server-enforced). */
export interface CreateFaultBody {
  reference?: string
  asset_class?: string
  asset_id?: string
  longitude?: number
  latitude?: number
  fault_type?: FaultType
  cause?: string
  fault_current_a?: number
  protective_device_class?: string
  protective_device_id?: string
  clearing_time_ms?: number
  detected_by?: string
  incident_id?: string
  title: string
  description?: string
  notes?: string
}

export function useCreateFault() {
  return useFaultsMutation<CreateFaultBody>((body) =>
    apiFetch("/faults", { method: "POST", body: JSON.stringify(body) }),
  )
}

/** Patch investigation findings on a fault (classification, cause, the device
 * that cleared it, timings). Only the given fields are written. */
export interface UpdateFaultBody {
  reference?: string
  fault_type?: FaultType
  cause?: string
  fault_current_a?: number
  protective_device_class?: string
  protective_device_id?: string
  clearing_time_ms?: number
  detected_by?: string
  incident_id?: string
  title?: string
  description?: string
  notes?: string
}

export function useUpdateFault() {
  return useFaultsMutation<{ id: string; body: UpdateFaultBody }>(({ id, body }) =>
    apiFetch(`/faults/${id}`, { method: "PATCH", body: JSON.stringify(body) }),
  )
}

/** Transition a fault (detected / investigating / cleared / closed). cleared_at
 * is kept consistent server-side. */
export function useSetFaultStatus() {
  return useFaultsMutation<{ id: string; status: FaultStatus }>(({ id, status }) =>
    apiFetch(`/faults/${id}/status`, { method: "POST", body: JSON.stringify({ status }) }),
  )
}

/** Book a WAM defect from a fault (links defect_id, sourced 'outage'). All
 * overrides default from the fault; needs the fault to carry an asset or a
 * location. Touches the WAM boards too. */
export interface BookDefectBody {
  title?: string
  defect_type?: string
  severity?: string
  description?: string
  reported_by?: string
}

export function useBookDefectFromFault() {
  return useFaultsMutation<{ faultId: string; body?: BookDefectBody }>(
    ({ faultId, body }) => apiFetch(`/faults/${faultId}/defect`, { method: "POST", body: JSON.stringify(body ?? {}) }),
    [["wam"]],
  )
}

/** Raise a fault from an OMS incident (links incident_id, inherits the incident's
 * predicted device + cause). All overrides default from the incident. */
export interface RaiseFaultFromIncidentBody {
  title?: string
  fault_type?: FaultType
  cause?: string
  detected_by?: string
  notes?: string
}

export function useRaiseFaultFromIncident() {
  return useFaultsMutation<{ incidentId: string; body?: RaiseFaultFromIncidentBody }>(({ incidentId, body }) =>
    apiFetch(`/faults/from-incident/${incidentId}`, { method: "POST", body: JSON.stringify(body ?? {}) }),
  )
}
