import { useQuery } from "@tanstack/react-query"

import { apiFetch } from "@/lib/api"

/**
 * One addable cable-duct class (Phase 44 Slice 4): a class the backend fans out
 * DUCT_WAY children under. `duct_feature_class` is exactly what POST /ducts needs
 * (as duct_feature_class); the name drives the "Cable duct" menu. There's no config
 * axis (every way is identical -- the count comes off the duct's own way_count), so
 * one entry per class. Mirrors api/internal/duct DuctOption.
 */
export interface DuctOption {
  duct_feature_class: string
  duct_feature_class_name: string
}

/** A way fanned out by a duct create -- its class, the created way feature (with its
 * id), and its 1-based bore position. Mirrors api/internal/duct CreatedWay. */
export interface CreatedDuctWay {
  feature_class: string
  feature: { id: string; [key: string]: unknown }
  way_number: number
}

/** The POST /ducts response: the placed duct line and every way contained in it.
 * Mirrors api/internal/duct CreateDuctResult. */
export interface CreateDuctResult {
  duct: { id: string; [key: string]: unknown }
  ways: CreatedDuctWay[]
}

/**
 * The cable-duct classes that fan out ways -- GET /ducts/options. Empty until
 * seed/duct_way_example.sql is applied and DUCT_WAY is compiled (the backend returns
 * nothing without a `contains DUCT_WAY` containment class), so the "Cable duct"
 * fan-out affordance simply never shows until the catalog is present. Read-only.
 */
export function useDuctOptions() {
  return useQuery({
    queryKey: ["duct-options"],
    queryFn: () => apiFetch<DuctOption[]>("/ducts/options"),
  })
}
