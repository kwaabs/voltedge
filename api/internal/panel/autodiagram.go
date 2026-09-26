package panel

import (
	"context"

	"github.com/uptrace/bun"

	"github.com/kwaabs/voltedge/api/internal/diagram"
)

// panelDiagramTemplates maps a panel role code (the panel_role attribute driving
// the fan-out) to the built-in single-line template
// (seed/assembly_diagram_templates.sql) whose typed slots match that role's
// device set. INCOMER has its own layout; every other role (FEEDER / TRANSFORMER
// / BUS_SECTION / BUS_COUPLER) shares the generic feeder-bay single-line.
var panelDiagramTemplates = map[string]string{
	"INCOMER":     "MV switchgear panel — Incomer",
	"FEEDER":      "MV switchgear panel — Feeder",
	"TRANSFORMER": "MV switchgear panel — Feeder",
	"BUS_SECTION": "MV switchgear panel — Feeder",
	"BUS_COUPLER": "MV switchgear panel — Feeder",
}

// buildAutoDiagram stands up the panel's single-line right after a successful
// fan-out: it roots a ONE_LINE diagram on the panel (flipped to a diagram
// container by seed/assembly_diagram_templates.sql for MV_SWITCHGEAR_PANEL) and
// binds every fanned-out device into the matching role template's slots.
// Best-effort -- the handler logs any error, never fails the create over a
// diagram.
func buildAutoDiagram(ctx context.Context, db *bun.DB, in CreatePanelInput, result *CreatePanelResult) (*diagram.GenerateAssemblyDiagramResult, error) {
	role, _ := in.Attributes[panelRoleField].(string)
	templateName := panelDiagramTemplates[role]
	if templateName == "" {
		return nil, nil // no template for this role -- nothing to build, not an error
	}

	panelID, err := featureID(result.Panel)
	if err != nil {
		return nil, err
	}

	components := make([]diagram.AssemblyComponent, 0, len(result.Devices))
	for _, d := range result.Devices {
		id, err := featureID(d.Feature)
		if err != nil {
			return nil, err
		}
		components = append(components, diagram.AssemblyComponent{
			FeatureClass: d.FeatureClass,
			FeatureID:    id,
		})
	}

	return diagram.GenerateAssemblyDiagram(ctx, db, diagram.GenerateAssemblyDiagramInput{
		ContainerClass: in.PanelFeatureClass,
		ContainerID:    panelID,
		DiagramType:    diagram.AssemblyDiagramType,
		Name:           in.Name,
		TemplateName:   templateName,
		Components:     components,
	})
}
