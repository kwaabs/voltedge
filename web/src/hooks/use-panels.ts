import { useQuery } from "@tanstack/react-query"

import { apiFetch } from "@/lib/api"

/**
 * One panel addable to an open board schematic (Phase 42 Slice 3): a
 * (panel class, role) pairing the backend has both a device template and a
 * placement-rule allow-list entry for. `panel_feature_class` + `role` are
 * exactly what POST /panels needs (as panel_feature_class + attributes.panel_role);
 * the labels drive the add-panel menu.
 */
export interface PanelOption {
  panel_feature_class: string
  panel_feature_class_name: string
  role: string
  role_label: string
  template_name: string
}

interface PanelOptionsResponse {
  options: PanelOption[]
}

/** A device fanned out by a panel create -- its class, the created feature
 * (with its id), and where it sits on the board mimic relative to the panel's
 * own column. Mirrors api/internal/panel CreatedDevice. */
export interface CreatedPanelDevice {
  feature_class: string
  feature: { id: string; [key: string]: unknown }
  offset_x: number
  offset_y: number
  rotation: number
}

/** The POST /panels response: the created panel and every device wired into it.
 * Mirrors api/internal/panel CreatePanelResult. */
export interface CreatePanelResult {
  panel: { id: string; [key: string]: unknown }
  devices: CreatedPanelDevice[]
}

/**
 * The panels that may be added to the open diagram, by its container class and
 * diagram type -- GET /panels/options. Empty for any diagram that isn't a real
 * board schematic (the backend join returns nothing when the (container, type)
 * pairing has no placement-rule rows), so the add-panel affordance simply never
 * shows there. Read-only.
 */
export function usePanelOptions(containerFeatureClass: string | undefined, diagramType: string | undefined) {
  return useQuery({
    queryKey: ["panel-options", containerFeatureClass, diagramType],
    queryFn: () =>
      apiFetch<PanelOptionsResponse>(
        `/panels/options?container_feature_class=${encodeURIComponent(containerFeatureClass ?? "")}&diagram_type=${encodeURIComponent(diagramType ?? "")}`,
      ),
    enabled: Boolean(containerFeatureClass && diagramType),
  })
}
