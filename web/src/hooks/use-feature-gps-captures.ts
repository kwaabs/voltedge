import { useQuery } from "@tanstack/react-query"

import { apiFetch } from "@/lib/api"

/** One GNSS survey-quality capture stamped onto a GPS-sourced placement/move by
 * the field tool (Phase gnss-quality Slice 3, api/internal/crud/gpscapture.go).
 * Every DOP/sat/accuracy figure is nullable: an iPhone's internal receiver
 * reports only accuracy (no NMEA => no DOP), so those come through null. source
 * is "internal" | "external"; fix_type is the mobile GnssFixType `.name`
 * (camelCase: none/gps/dgps/rtkFloat/rtkFixed/unknown) or null. captured_by_* are resolved
 * server-side (LEFT JOIN iam.users), null for an unknown/since-deleted user. */
export interface FeatureGpsCapture {
  id: number
  hdop: number | null
  pdop: number | null
  vdop: number | null
  h_accuracy_m: number | null
  v_accuracy_m: number | null
  altitude_m: number | null
  sats_used: number | null
  sats_in_view: number | null
  fix_type: string | null
  source: string
  captured_by: string | null
  captured_by_name: string | null
  captured_by_email: string | null
  captured_at: string
  recorded_at: string
}

interface FeatureGpsCapturesResponse {
  gps_captures: FeatureGpsCapture[]
}

/**
 * A feature's GNSS survey-quality captures, newest first (Phase gnss-quality
 * Slice 3). Backed by GET /features/{code}/{id}/gps-captures, which never 404s on
 * a purged feature -- the capture record outlives the row -- so an empty list is a
 * valid answer (and the normal one for any feature never placed by GPS). Kept off
 * the ["feature", ...] cache key so browsing this tab never disturbs the
 * single-feature read the rest of the panel shares.
 */
export function useFeatureGpsCaptures(featureClassCode: string | undefined, featureId: string | undefined) {
  return useQuery({
    queryKey: ["feature-gps-captures", featureClassCode, featureId],
    queryFn: () =>
      apiFetch<FeatureGpsCapturesResponse>(`/features/${featureClassCode}/${featureId}/gps-captures`),
    enabled: Boolean(featureClassCode && featureId),
  })
}
