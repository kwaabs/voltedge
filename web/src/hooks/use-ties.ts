import { useQuery } from "@tanstack/react-query"

import { apiFetch } from "@/lib/api"

/**
 * One addable feeder-tie configuration (Phase 50 Slice 2): a switch class that
 * can act as a normally-open tie (it carries the normal_state field with a
 * NORMALLY_OPEN value, from Phase 48's field-pack) plus the conductor classes it
 * may join. `switch_feature_class` is exactly what POST /ties needs (as
 * switch_feature_class); `feeder_feature_classes` tells the create form which
 * classes to search for the two feeder picks. Mirrors api/internal/tie
 * TieOption.
 */
export interface TieOption {
  switch_feature_class: string
  switch_feature_class_name: string
  feeder_feature_classes: string[]
}

/** The POST /ties response: the placed switch and the ids of the two
 * connections wiring it to feeder A (rule 1) and feeder B (rule 3). Mirrors
 * api/internal/tie CreateTieResult. */
export interface CreateTieResult {
  switch: { id: string; [key: string]: unknown }
  connection_a: string
  connection_b: string
}

/**
 * The switch classes that can be placed as a feeder-to-feeder tie -- GET
 * /ties/options. Empty until seed/switch_normal_state_fields.sql is applied (the
 * normal_state field with a NORMALLY_OPEN value) AND the switch<->conductor
 * wiring rules exist, so the "Create tie" affordance simply never shows until
 * the catalog supports it. Read-only.
 */
export function useTieOptions() {
  return useQuery({
    queryKey: ["tie-options"],
    queryFn: () => apiFetch<TieOption[]>("/ties/options"),
  })
}
