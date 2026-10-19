import { useQuery } from "@tanstack/react-query"

import { apiFetch } from "@/lib/api"

/** A curve's band: a relay's single operating curve, or a fuse's two bounding
 * curves (earliest-melt and fully-cleared). */
export type CurveBand = "operate" | "minimum_melt" | "total_clear"

/** One (band, current, time) sample on a curve. current is absolute amperes for
 * a fuse curve (quantity=ampere) or a multiple of pickup for a relay curve
 * (quantity=multiple); time_s is seconds. */
export interface CurvePoint {
  band: CurveBand
  current: number
  time_s: number
}

/** A curve header plus its full point set (GET /curves/{code}) -- what the admin
 * editor loads for one curve. */
export interface ProtectionCurveDetail extends ProtectionCurve {
  points: CurvePoint[]
}

/** One protection curve's header (api/internal/powerflow CurveCatalogItem): the
 * `code` the coordination engine joins a device to its points by, a human name,
 * the issuing standard, and whether it's indexed by a multiple of pickup (a
 * relay curve) or absolute amperes (a fuse curve). */
export interface ProtectionCurve {
  code: string
  name: string
  standard: string
  quantity: string
}

interface CurvesResponse {
  curves: ProtectionCurve[]
}

/**
 * The protection-curve catalog (GET /curves) -- reference data read so the
 * feature form can offer a fuse's curve_ref (or a relay's curve) as a PICKED
 * value instead of a free-text code that has to match a stored curve exactly.
 *
 * `quantity` filters the list to the curves that make sense for the field:
 * "ampere" for a fuse curve (curve_ref), "multiple" for a relay curve. The
 * catalog is small, rarely-changing reference data, so it gets a long staleTime
 * like useCircuits rather than polling.
 */
export function useProtectionCurves(quantity?: "ampere" | "multiple") {
  return useQuery({
    queryKey: ["protection-curves", quantity ?? "all"],
    queryFn: () =>
      apiFetch<CurvesResponse>(`/curves${quantity ? `?quantity=${quantity}` : ""}`),
    staleTime: 5 * 60_000,
    retry: 1,
  })
}

/**
 * One curve's header + full point set (GET /curves/{code}), for the admin
 * editor. Enabled only when a curve is selected; writes nothing. A mutation on
 * the curve should invalidate both this (`["protection-curve", code]`) and the
 * list (`["protection-curves"]`) so the editor and every picker refresh.
 */
export function useProtectionCurveDetail(code: string | null) {
  return useQuery({
    queryKey: ["protection-curve", code],
    queryFn: () => apiFetch<ProtectionCurveDetail>(`/curves/${code}`),
    enabled: Boolean(code),
    staleTime: 0,
    retry: 1,
  })
}
