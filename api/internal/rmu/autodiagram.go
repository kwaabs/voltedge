package rmu

import (
	"context"

	"github.com/uptrace/bun"

	"github.com/kwaabs/voltedge/api/internal/diagram"
)

// rmuDiagramTemplates maps an RMU config code to the built-in single-line
// template (seed/assembly_diagram_templates.sql) whose typed slots match that
// config's fanned-out components.
var rmuDiagramTemplates = map[string]string{
	"2W_1T": "RMU — 2 ring + 1 transformer",
	"2W_2T": "RMU — 2 ring + 2 transformer",
}

// buildAutoDiagram stands up the RMU's single-line right after a successful
// fan-out: it roots a ONE_LINE diagram on the RMU (already a diagram container)
// and binds the internal bus + every way into the matching template's slots.
// Called best-effort by the handler -- a diagram hiccup must never fail the
// create, so any error is returned only for the caller to log.
func buildAutoDiagram(ctx context.Context, db *bun.DB, in CreateRMUInput, result *CreateRMUResult) (*diagram.GenerateAssemblyDiagramResult, error) {
	config, _ := in.Attributes[rmuConfigField].(string)
	templateName := rmuDiagramTemplates[config]
	if templateName == "" {
		return nil, nil // no template for this config -- nothing to build, not an error
	}

	rmuID, err := featureID(result.RMU)
	if err != nil {
		return nil, err
	}

	components := make([]diagram.AssemblyComponent, 0, len(result.Ways)+1)
	if busID, err := featureID(result.Busbar); err == nil {
		components = append(components, diagram.AssemblyComponent{
			FeatureClass: result.BusbarFeatureClass,
			FeatureID:    busID,
		})
	}
	for _, w := range result.Ways {
		id, err := featureID(w.Feature)
		if err != nil {
			return nil, err
		}
		components = append(components, diagram.AssemblyComponent{
			FeatureClass: w.FeatureClass,
			FeatureID:    id,
		})
	}

	return diagram.GenerateAssemblyDiagram(ctx, db, diagram.GenerateAssemblyDiagramInput{
		ContainerClass: in.RMUFeatureClass,
		ContainerID:    rmuID,
		DiagramType:    diagram.AssemblyDiagramType,
		Name:           in.Name,
		TemplateName:   templateName,
		Components:     components,
	})
}
