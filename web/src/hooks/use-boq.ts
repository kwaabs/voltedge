import { useMutation } from "@tanstack/react-query"

import { ApiError, apiFetch } from "@/lib/api"

/**
 * The Bill-of-Quantities client (Slice 1). A BoQ prices a scope of the network:
 * POST /boq multiplies scoped quantities (discrete feature counts priced 'each',
 * linear geometry priced per_km from the selected material's cost_per_km) by
 * catalog rates and returns a grouped, totalled schedule in a chosen currency.
 * Read-only and synchronous, unlike an export job -- so a plain mutation, not a
 * polled queue (mirrors the analysis panels' one-shot POST).
 *
 * Shapes mirror api/internal/boq/types.go exactly.
 */

/** A spatial narrowing for a class scan (whole-network mode). Omit for no limit. */
export interface BoqScope {
  kind: "none" | "territory" | "radius" | "bbox"
  territory_id?: string
  center?: [number, number] // [lon, lat] for radius
  radius_m?: number
  bbox?: [number, number, number, number] // [minLon, minLat, maxLon, maxLat]
}

/** One feature to price by class + id -- how a trace result or selection scopes. */
export interface BoqFeatureRef {
  class: string
  id: string
}

export interface BoqRequest {
  feature_classes?: string[]
  scope?: BoqScope
  feature_refs?: BoqFeatureRef[]
  display_currency?: string
}

/** One priced row: a (class, subtype, material) bucket with its rate applied. */
export interface BoqLine {
  feature_class: string
  feature_class_name: string
  subtype?: number
  subtype_name?: string
  material_code?: string
  material_name?: string
  unit_basis: "each" | "per_km"
  quantity: number
  unit: "each" | "km"
  unit_rate: number
  line_total: number
}

/** A counted bucket that resolved no rate -- surfaced, never folded to zero. */
export interface BoqUnpricedLine {
  feature_class: string
  feature_class_name: string
  subtype?: number
  subtype_name?: string
  material_code?: string
  unit_basis: string
  quantity: number
  unit: string
  reason: string
}

export interface BoqCurrency {
  code: string
  name: string
  per_base: number
  is_base: boolean
}

export interface BoqResult {
  currency: string
  currency_name: string
  base_currency: string
  per_base: number
  lines: BoqLine[]
  unpriced: BoqUnpricedLine[]
  total: number
  total_base: number
  priced_count: number
  currencies: BoqCurrency[]
}

/**
 * Prices a scope on demand. Mirrors useEnqueueExport's mutation, but the result
 * is the schedule itself (returned inline) rather than a job id to poll.
 */
export function usePriceBoQ() {
  return useMutation<BoqResult, ApiError, BoqRequest>({
    mutationFn: (body) =>
      apiFetch<BoqResult>("/boq", { method: "POST", body: JSON.stringify(body) }),
  })
}

// --- Asset register (the per-feature sibling of the priced schedule) ---

/** The asset-register request: the same scope shapes as a BoQ, plus an optional
 * valuation. Mirrors api/internal/boq/types.go RegisterRequest. */
export interface AssetRegisterRequest {
  feature_classes?: string[]
  scope?: BoqScope
  feature_refs?: BoqFeatureRef[]
  include_valuation?: boolean
  display_currency?: string
}

/** One asset's itemised row. Valuation fields are present only when the request
 * asked for it. Mirrors api/internal/boq/types.go RegisterRow. */
export interface AssetRegisterRow {
  feature_class: string
  feature_class_name: string
  feature_id: string
  subtype?: number
  subtype_name?: string
  name?: string
  status?: string
  material_code?: string
  material_name?: string
  quantity: number
  unit: "each" | "km"
  // longitude/latitude = point location / line START endpoint / polygon
  // representative point; to_longitude/to_latitude = line END endpoint.
  longitude?: number
  latitude?: number
  to_longitude?: number
  to_latitude?: number
  territory_id?: string
  territory_name?: string
  circuit_id?: string
  ownership?: string
  external_ref?: string
  created_at?: string
  unit_rate?: number
  value?: number
  priced_source?: "catalog" | "override" | "unpriced"
}

/** The itemised asset register. Currency/total fields are meaningful only when
 * valuation was requested. Mirrors api/internal/boq/types.go RegisterResponse. */
export interface AssetRegisterResult {
  rows: AssetRegisterRow[]
  count: number
  truncated: boolean
  include_valuation: boolean
  currency?: string
  currency_name?: string
  base_currency?: string
  per_base?: number
  total_value?: number
  currencies: BoqCurrency[]
}

/** Builds the itemised asset register for a scope on demand (a one-shot POST,
 * like usePriceBoQ -- the register is returned inline, not polled). */
export function useAssetRegister() {
  return useMutation<AssetRegisterResult, ApiError, AssetRegisterRequest>({
    mutationFn: (body) =>
      apiFetch<AssetRegisterResult>("/boq/asset-register", {
        method: "POST",
        body: JSON.stringify(body),
      }),
  })
}
