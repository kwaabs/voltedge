import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"

import { apiFetch } from "@/lib/api"

/** One device's latest telemetry (GET /telemetry/device/{class}/{id}). */
export interface DeviceTelemetry {
  feature_class_code: string
  feature_id: string
  state: string | null
  measurements?: unknown
  quality: string
  source: string | null
  measured_at: string
  ingested_at: string
}

/** One device needing attention in the field-vs-model reconciliation. */
export interface ReconItem {
  feature_class_code: string
  feature_id: string
  telemetry_state: string | null
  model_state: string | null
  agree: boolean
  stale: boolean
  age_seconds: number
  quality: string
  source: string | null
  measured_at: string
}

/** The field-vs-model reconciliation summary + the items needing attention. */
export interface Reconciliation {
  stale_seconds: number
  total: number
  agreeing: number
  disagreeing: number
  stale: number
  no_model_state: number
  items: ReconItem[]
}

const TELEMETRY_KEY = ["telemetry"] as const

/** Field-vs-model reconciliation: which devices disagree with the model, or are
 * stale. Short staleTime — it's a live picture. */
export function useTelemetryReconciliation(opts: { staleSeconds?: number } = {}) {
  const query = opts.staleSeconds != null ? `?stale_seconds=${opts.staleSeconds}` : ""
  return useQuery({
    queryKey: [...TELEMETRY_KEY, "reconciliation", opts.staleSeconds ?? 0],
    queryFn: () => apiFetch<Reconciliation>(`/telemetry/reconciliation${query}`),
    staleTime: 10_000,
    // It's a LIVE picture — poll so a new disagreement or a device going quiet
    // surfaces on its own. (react-query pauses the interval on a hidden tab.)
    refetchInterval: 15_000,
  })
}

/** One device's latest telemetry (null when none reported). */
export function useDeviceTelemetry(featureClass: string | undefined, featureId: string | undefined) {
  return useQuery({
    queryKey: [...TELEMETRY_KEY, "device", featureClass ?? "", featureId ?? ""],
    queryFn: () => apiFetch<{ telemetry: DeviceTelemetry | null }>(`/telemetry/device/${featureClass}/${featureId}`),
    enabled: !!featureClass && !!featureId,
    staleTime: 10_000,
  })
}

/** Write a device's telemetry state onto the model device_state (operator action,
 * oms:write). Invalidates the telemetry views. */
export function useApplyTelemetry() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ featureClass, featureId }: { featureClass: string; featureId: string }) =>
      apiFetch(`/telemetry/device/${featureClass}/${featureId}/apply`, { method: "POST" }),
    onSuccess: () => qc.invalidateQueries({ queryKey: TELEMETRY_KEY }),
  })
}
