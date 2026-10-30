import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"

import { ApiError, apiFetch } from "@/lib/api"

/**
 * Client for the protection-settings register (api/internal/protection, migration
 * 0185): the as-built relay / overcurrent settings per protective device that a TCC
 * curve does not capture. Read is features:read; edit is features:write.
 */
export interface ProtectionSettings {
  id: string
  feature_class_code: string
  feature_id: string
  relay_model: string | null
  ct_ratio: string | null
  phase_pickup_a: number | null
  phase_time_dial: number | null
  phase_curve: string | null
  phase_instantaneous_a: number | null
  ground_pickup_a: number | null
  ground_time_dial: number | null
  ground_curve: string | null
  ground_instantaneous_a: number | null
  reclose_operations: number | null
  reclose_intervals: string | null
  /** Provenance of the last write: "manual" (register UI) or an external system's
   * code (webhook-ingested). Null on rows written before provenance tracking. */
  source: string | null
  notes: string | null
  updated_by?: string | null
  created_at: string
  updated_at: string
}

/** The write body — every setting optional (null clears it). */
export interface ProtectionSettingsBody {
  relay_model?: string | null
  ct_ratio?: string | null
  phase_pickup_a?: number | null
  phase_time_dial?: number | null
  phase_curve?: string | null
  phase_instantaneous_a?: number | null
  ground_pickup_a?: number | null
  ground_time_dial?: number | null
  ground_curve?: string | null
  ground_instantaneous_a?: number | null
  reclose_operations?: number | null
  reclose_intervals?: string | null
  notes?: string | null
}

/** The whole register — every device with recorded settings, newest-edited first. */
export function useProtectionSettingsRegister() {
  return useQuery({
    queryKey: ["protection-settings"],
    queryFn: () => apiFetch<{ settings: ProtectionSettings[] }>("/protection/settings"),
    staleTime: 30_000,
  })
}

/** Create or replace one device's settings (PUT). */
export function useUpsertProtectionSettings() {
  const qc = useQueryClient()
  return useMutation<ProtectionSettings, ApiError, { featureClass: string; featureId: string; body: ProtectionSettingsBody }>({
    mutationFn: ({ featureClass, featureId, body }) =>
      apiFetch(`/protection/settings/${featureClass}/${featureId}`, { method: "PUT", body: JSON.stringify(body) }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["protection-settings"] }),
  })
}

/** The feature classes a protection-settings record may attach to (mirrors
 * api/internal/protection's ProtectionDeviceClasses). Used to scope the device
 * name-search so a transformer or conductor never shows up as a candidate. */
export const PROTECTION_DEVICE_CLASSES = [
  "MV_BREAKER",
  "MV_RECLOSER",
  "MV_FUSE",
  "MV_SWITCH",
  "MV_ISOLATOR",
  "MV_SECTIONALIZER",
  "LV_SWITCH",
  "PROTECTIVE_RELAY",
]

/** A protective device located by its bare id (GUID) — the class + name the
 * register keys on. */
export interface ResolvedProtectionDevice {
  feature_class_code: string
  feature_id: string
  name?: string
}

/** Resolve a pasted feature id (GUID) to its protective/switching device class.
 * Rejects (ApiError) an id that belongs to a non-protection feature (a
 * transformer, a line) or doesn't exist. Called on demand from the picker. */
export function resolveProtectionDevice(featureId: string): Promise<ResolvedProtectionDevice> {
  return apiFetch<ResolvedProtectionDevice>(`/protection/resolve?feature_id=${encodeURIComponent(featureId.trim())}`)
}

/** Remove one device's settings. */
export function useDeleteProtectionSettings() {
  const qc = useQueryClient()
  return useMutation<void, ApiError, { featureClass: string; featureId: string }>({
    mutationFn: ({ featureClass, featureId }) =>
      apiFetch(`/protection/settings/${featureClass}/${featureId}`, { method: "DELETE" }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["protection-settings"] }),
  })
}
