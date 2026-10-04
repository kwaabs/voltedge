import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"

import { ApiError, apiFetch } from "@/lib/api"

/**
 * The Bill-of-Quantities cost catalog (api/internal/admin, migrations
 * 0127/0128): presentation currencies, the unit-rate price book, and
 * per-feature cost overrides. All three live under the /admin mount, so every
 * read and write is gated on metadata:write -- the admin-only tightening cost
 * data was given (security-admin-view-gate). The BoQ engine prices against
 * exactly these rows.
 */

// --- Currencies ------------------------------------------------------------

/** One presentation currency -- admin's currencyResponse. per_base is how many
 * units of this currency equal one base unit (the base row is always 1); is_base
 * marks the single house currency every stored cost is denominated in. */
export interface Currency {
  code: string
  name: string
  per_base: number
  is_base: boolean
}

interface CurrenciesResponse {
  currencies: Currency[]
}

export function useCurrencies() {
  return useQuery({
    queryKey: ["admin", "currencies"],
    queryFn: () => apiFetch<CurrenciesResponse>("/admin/currencies"),
    staleTime: 5 * 60_000,
    retry: 1,
  })
}

// --- Cost rates ------------------------------------------------------------

/** One unit rate (in the base currency) for a class, optionally narrowed to a
 * subtype -- admin's costRateResponse. subtype_code absent = the class-wide
 * default. unit_basis is 'each' (rate x count) or 'per_km' (rate x length km). */
export interface CostRate {
  id: string
  feature_class_code: string
  subtype_code?: number
  unit_basis: "each" | "per_km"
  unit_rate: number
  notes?: string
}

interface CostRatesResponse {
  cost_rates: CostRate[]
}

export function useCostRates() {
  return useQuery({
    queryKey: ["admin", "cost-rates"],
    queryFn: () => apiFetch<CostRatesResponse>("/admin/cost-rates"),
    staleTime: 5 * 60_000,
    retry: 1,
  })
}

// --- Feature cost overrides -------------------------------------------------

/** One per-feature override -- admin's featureCostOverrideResponse. unit_cost is
 * in the base currency, in the feature's natural unit (per_km for a line, each
 * for a discrete feature), and supersedes the catalog rate. */
export interface FeatureCostOverride {
  id: string
  feature_class_code: string
  feature_id: string
  unit_cost: number
  notes?: string
}

interface FeatureCostOverridesResponse {
  feature_cost_overrides: FeatureCostOverride[]
}

/** Lists overrides, optionally narrowed to one class. Passing a class keeps the
 * feature panel's single-feature lookup small; omitting it lists them all. */
export function useFeatureCostOverrides(classCode?: string) {
  const qs = classCode ? `?class=${encodeURIComponent(classCode)}` : ""
  return useQuery({
    queryKey: ["admin", "feature-cost-overrides", classCode ?? null],
    queryFn: () => apiFetch<FeatureCostOverridesResponse>(`/admin/feature-cost-overrides${qs}`),
    staleTime: 60_000,
    retry: 1,
  })
}

// --- Mutations -------------------------------------------------------------
// All gated by metadata:write on the server; the pages/controls only render for
// callers who hold it. Each invalidates the catalog list it touches.

function useCatalogMutation<TBody>(fn: (body: TBody) => Promise<unknown>, invalidate: unknown[][]) {
  const queryClient = useQueryClient()
  return useMutation<unknown, ApiError, TBody>({
    mutationFn: fn,
    onSuccess: () => {
      for (const key of invalidate) queryClient.invalidateQueries({ queryKey: key })
    },
  })
}

export interface CreateCurrencyBody {
  code: string
  name: string
  per_base: number
  is_base: boolean
}

export function useCreateCurrency() {
  return useCatalogMutation<CreateCurrencyBody>(
    (body) => apiFetch("/admin/currencies", { method: "POST", body: JSON.stringify(body) }),
    [["admin", "currencies"]],
  )
}

export interface UpdateCurrencyBody {
  name?: string
  per_base?: number
  is_base?: boolean
}

export function useUpdateCurrency() {
  return useCatalogMutation<{ code: string; body: UpdateCurrencyBody }>(
    ({ code, body }) => apiFetch(`/admin/currencies/${code}`, { method: "PATCH", body: JSON.stringify(body) }),
    [["admin", "currencies"]],
  )
}

export function useDeleteCurrency() {
  return useCatalogMutation<{ code: string }>(
    ({ code }) => apiFetch(`/admin/currencies/${code}`, { method: "DELETE" }),
    [["admin", "currencies"]],
  )
}

export interface CreateCostRateBody {
  feature_class_code: string
  subtype_code?: number | null
  unit_basis: "each" | "per_km"
  unit_rate: number
  notes?: string | null
}

export function useCreateCostRate() {
  return useCatalogMutation<CreateCostRateBody>(
    (body) => apiFetch("/admin/cost-rates", { method: "POST", body: JSON.stringify(body) }),
    [["admin", "cost-rates"]],
  )
}

/** `clear_subtype` resets the rate back to the class-wide default -- a null
 * `subtype_code` means "leave it", so the explicit flag distinguishes them. */
export interface UpdateCostRateBody {
  subtype_code?: number | null
  clear_subtype?: boolean
  unit_basis?: "each" | "per_km"
  unit_rate?: number
  notes?: string | null
}

export function useUpdateCostRate() {
  return useCatalogMutation<{ id: string; body: UpdateCostRateBody }>(
    ({ id, body }) => apiFetch(`/admin/cost-rates/${id}`, { method: "PATCH", body: JSON.stringify(body) }),
    [["admin", "cost-rates"]],
  )
}

export function useDeleteCostRate() {
  return useCatalogMutation<{ id: string }>(
    ({ id }) => apiFetch(`/admin/cost-rates/${id}`, { method: "DELETE" }),
    [["admin", "cost-rates"]],
  )
}

export interface UpsertFeatureCostOverrideBody {
  unit_cost: number
  notes?: string | null
}

/** Sets (or replaces) the single override for a feature -- idempotent PUT keyed
 * by (class, feature_id). */
export function useUpsertFeatureCostOverride() {
  return useCatalogMutation<{ classCode: string; featureId: string; body: UpsertFeatureCostOverrideBody }>(
    ({ classCode, featureId, body }) =>
      apiFetch(`/admin/feature-cost-overrides/${classCode}/${featureId}`, {
        method: "PUT",
        body: JSON.stringify(body),
      }),
    [["admin", "feature-cost-overrides"]],
  )
}

export function useDeleteFeatureCostOverride() {
  return useCatalogMutation<{ classCode: string; featureId: string }>(
    ({ classCode, featureId }) =>
      apiFetch(`/admin/feature-cost-overrides/${classCode}/${featureId}`, { method: "DELETE" }),
    [["admin", "feature-cost-overrides"]],
  )
}
