import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"

import { ApiError, apiFetch } from "@/lib/api"

// Mirrors api/internal/deviceops. The device-operation log is append-only: one
// row per time a protective/switching device operated (trip, lockout, reclose,
// open, close, earth). It's distinct from the CURRENT device_state (a feature
// column) -- this is the history of operations, with typed cause/source/time.

/** op_type values (migration 0147 CHECK). */
export type OpType = "trip" | "lockout" | "reclose" | "open" | "close" | "earth" | "alarm" | "other"

/** source values (migration 0147 CHECK). */
export type OpSource = "crew" | "scada" | "relay" | "switching_order" | "derived" | "other"

/** One device operation (api/internal/deviceops Operation). */
export interface Operation {
  id: string
  device_class: string
  device_id: string
  op_type: OpType
  source: OpSource
  occurred_at: string
  cause?: string
  notes?: string
  fault_id?: string
  incident_id?: string
  switching_step_id?: string
  source_system?: string
  source_event_id?: string
  actor_id?: string
  created_at: string
}

/** One device's tally for an op_type (api/internal/deviceops DeviceOpCount). */
export interface DeviceOpCount {
  device_class: string
  device_id: string
  op_type: OpType
  count: number
  last_occurred_at: string
}

/** One operable device in the inventory report (api/internal/deviceops
 * InventoryDevice): current live state + its most recent operation (absent when
 * it has never operated). */
export interface InventoryDevice {
  device_class: string
  device_id: string
  device_state?: string
  territory_id?: string
  last_op_type?: OpType
  last_op_at?: string
}

/** Filters for the operations board. Empty fields are omitted. */
export interface OperationsFilter {
  device_class?: string
  device_id?: string
  op_type?: string
  source?: string
  since?: string
  until?: string
  limit?: number
}

function toQuery(f: OperationsFilter): string {
  const p = new URLSearchParams()
  if (f.device_class) p.set("device_class", f.device_class)
  if (f.device_id) p.set("device_id", f.device_id)
  if (f.op_type) p.set("op_type", f.op_type)
  if (f.source) p.set("source", f.source)
  if (f.since) p.set("since", f.since)
  if (f.until) p.set("until", f.until)
  if (f.limit) p.set("limit", String(f.limit))
  const s = p.toString()
  return s ? `?${s}` : ""
}

/** The operations board (GET /operations), newest first, for the given filter. */
export function useOperations(filter: OperationsFilter = {}) {
  return useQuery({
    queryKey: ["operations", filter],
    queryFn: () => apiFetch<{ operations: Operation[] }>(`/operations${toQuery(filter)}`),
    staleTime: 30_000,
    retry: 1,
  })
}

/** One device's operation timeline (GET /operations/device/{class}/{id}). Enabled
 * only when both parts are present (a device panel section). */
export function useDeviceOperations(deviceClass?: string, deviceId?: string) {
  return useQuery({
    queryKey: ["operations", "device", deviceClass, deviceId],
    queryFn: () =>
      apiFetch<{ operations: Operation[] }>(`/operations/device/${deviceClass}/${deviceId}`),
    enabled: Boolean(deviceClass && deviceId),
    staleTime: 30_000,
    retry: 1,
  })
}

/** The per-device operation tally (GET /operations/counts), busiest first. */
export function useOperationCounts(opType?: string, since?: string) {
  const p = new URLSearchParams()
  if (opType) p.set("op_type", opType)
  if (since) p.set("since", since)
  const qs = p.toString()
  return useQuery({
    queryKey: ["operations", "counts", opType ?? "", since ?? ""],
    queryFn: () => apiFetch<{ counts: DeviceOpCount[] }>(`/operations/counts${qs ? `?${qs}` : ""}`),
    staleTime: 30_000,
    retry: 1,
  })
}

/** One switching device currently off its normal position (api/internal/deviceops
 * AbnormalDevice): its declared normal_state and the contradicting device_state. */
export interface AbnormalDevice {
  device_class: string
  device_id: string
  device_state: string
  normal_state: string
  territory_id?: string
  last_op_type?: OpType
  last_op_at?: string
}

/** The abnormal-configuration report (GET /operations/abnormal): every switching
 * device whose live device_state contradicts its declared normal_state — the
 * "what's off normal right now" control-room view. */
export function useAbnormalDevices(deviceClass?: string) {
  const p = new URLSearchParams()
  if (deviceClass) p.set("device_class", deviceClass)
  const qs = p.toString()
  return useQuery({
    queryKey: ["operations", "abnormal", deviceClass ?? ""],
    queryFn: () => apiFetch<{ devices: AbnormalDevice[] }>(`/operations/abnormal${qs ? `?${qs}` : ""}`),
    staleTime: 30_000,
    retry: 1,
  })
}

/** The device inventory (GET /operations/inventory): every operable device with
 * its current device_state and last operation. Device-centric, the complement of
 * the operation-centric board/counts. */
export function useDeviceInventory(deviceClass?: string, state?: string) {
  const p = new URLSearchParams()
  if (deviceClass) p.set("device_class", deviceClass)
  if (state) p.set("state", state)
  const qs = p.toString()
  return useQuery({
    queryKey: ["operations", "inventory", deviceClass ?? "", state ?? ""],
    queryFn: () => apiFetch<{ devices: InventoryDevice[] }>(`/operations/inventory${qs ? `?${qs}` : ""}`),
    staleTime: 30_000,
    retry: 1,
  })
}

/** Body for recording one operation (POST /operations). occurred_at omitted
 * defaults to now server-side; source omitted defaults to 'crew'. */
export interface RecordOperationBody {
  device_class: string
  device_id: string
  op_type: OpType
  source?: OpSource
  occurred_at?: string
  cause?: string
  notes?: string
  fault_id?: string
  incident_id?: string
  switching_step_id?: string
}

/** Records one device operation, invalidating every operations query so the
 * board, a device's timeline and the counts all refresh. */
export function useRecordOperation() {
  const queryClient = useQueryClient()
  return useMutation<Operation, ApiError, RecordOperationBody>({
    mutationFn: (body) => apiFetch<Operation>("/operations", { method: "POST", body: JSON.stringify(body) }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["operations"] })
    },
  })
}
