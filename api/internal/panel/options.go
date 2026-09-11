package panel

import (
	"context"
	"fmt"

	"github.com/uptrace/bun"
)

// diagramTypeDomainCode is the coded domain the diagram_type of a diagram
// instance is drawn from (the same domain create-diagram-form.tsx offers). Its
// value ids key metadata.diagram_placement_rules; naming the domain by its
// stable code is the one structural constant this lookup needs (alongside the
// containment kind), not a feature-class code.
const diagramTypeDomainCode = "DIAGRAM_TYPE"

// PanelOption is one addable panel offered for an open diagram: which panel
// class to create and which role to create it as (its template selects the
// device fan-out). PanelFeatureClass + Role are exactly what POST /panels needs
// back as panel_feature_class + attributes.panel_role. Name/labels are for the
// menu.
type PanelOption struct {
	PanelFeatureClass     string `json:"panel_feature_class"`
	PanelFeatureClassName string `json:"panel_feature_class_name"`
	Role                  string `json:"role"`
	RoleLabel             string `json:"role_label"`
	TemplateName          string `json:"template_name"`
}

// ListPanelOptions returns the panels that may be added to a diagram of
// diagramType hosted by a container of class containerClass -- one entry per
// (panel class, role) that (a) has a device template (metadata.panel_templates),
// (b) is on the diagram's placement allow-list for this (container class,
// diagram type) pairing, and (c) is a legal containment child of the container
// class. All three conditions are metadata joins, so the result is empty for any
// diagram that isn't a real board schematic without this package naming a single
// feature-class code: an ordinary one-line (no placement-rule rows) yields
// nothing, so the frontend "Add panel" affordance simply doesn't appear there.
func ListPanelOptions(ctx context.Context, db bun.IDB, containerClass, diagramType string) ([]PanelOption, error) {
	if containerClass == "" || diagramType == "" {
		return nil, invalidf("container_feature_class and diagram_type are required")
	}
	// rv.sort_order is in the SELECT (not just the ORDER BY) because SELECT
	// DISTINCT requires every ORDER BY expression to appear in the select list --
	// it's scanned into a throwaway, only there to order roles by the domain's
	// intended sequence (INCOMER first, ...) rather than alphabetically.
	const query = `
SELECT DISTINCT pfc.code, pfc.name, rv.code, rv.label, rv.sort_order, pt.name
FROM metadata.panel_templates pt
JOIN metadata.feature_classes pfc ON pfc.id = pt.panel_feature_class_id
JOIN metadata.domain_values rv    ON rv.id = pt.role_value_id
-- the panel class must be on the placement allow-list for THIS diagram
-- (container class x diagram type); that pairing exists only for a board
-- schematic, so a plain one-line returns no rows here.
JOIN metadata.feature_classes cfc ON cfc.code = ?
JOIN metadata.domains dtd          ON dtd.code = ?
JOIN metadata.domain_values dtv    ON dtv.domain_id = dtd.id AND dtv.code = ?
JOIN metadata.diagram_placement_rules dpr
  ON dpr.container_feature_class_id = cfc.id
 AND dpr.diagram_type_value_id      = dtv.id
 AND dpr.feature_class_id           = pfc.id
-- and the container class must be allowed to contain the panel class.
JOIN metadata.relationship_classes rc
  ON rc.origin_feature_class_id      = cfc.id
 AND rc.destination_feature_class_id = pfc.id
 AND rc.kind = ?
ORDER BY pfc.name, rv.sort_order, rv.label`

	rows, err := db.QueryContext(ctx, query, containerClass, diagramTypeDomainCode, diagramType, containmentKind)
	if err != nil {
		return nil, fmt.Errorf("panel: listing options: %w", err)
	}
	defer rows.Close()

	var options []PanelOption
	for rows.Next() {
		var o PanelOption
		var sortOrder int // ordering only (see the SELECT DISTINCT note above)
		if err := rows.Scan(&o.PanelFeatureClass, &o.PanelFeatureClassName, &o.Role, &o.RoleLabel, &sortOrder, &o.TemplateName); err != nil {
			return nil, fmt.Errorf("panel: scanning option: %w", err)
		}
		options = append(options, o)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("panel: iterating options: %w", err)
	}
	return options, nil
}
