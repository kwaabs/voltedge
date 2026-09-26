package packagedsub

import (
	"context"

	"github.com/uptrace/bun"

	"github.com/kwaabs/voltedge/api/internal/diagram"
)

// packagedSubDiagramTemplates maps a packaged-substation config code to the
// built-in single-line template (seed/assembly_diagram_templates.sql) whose
// typed slots match that config's fanned-out components.
var packagedSubDiagramTemplates = map[string]string{
	"COMPACT": "Packaged substation (compact)",
}

// buildAutoDiagram stands up the packaged substation's single-line right after a
// successful fan-out: it roots a ONE_LINE diagram on the enclosure (already a
// diagram container) and binds the chain of components into the matching
// template's slots. Best-effort -- the handler logs any error, never fails the
// create over a diagram.
func buildAutoDiagram(ctx context.Context, db *bun.DB, in CreatePackagedSubInput, result *CreatePackagedSubResult) (*diagram.GenerateAssemblyDiagramResult, error) {
	config, _ := in.Attributes[packageConfigField].(string)
	templateName := packagedSubDiagramTemplates[config]
	if templateName == "" {
		return nil, nil
	}

	enclosureID, err := featureID(result.PackagedSubstation)
	if err != nil {
		return nil, err
	}

	components := make([]diagram.AssemblyComponent, 0, len(result.Components))
	for _, c := range result.Components {
		id, err := featureID(c.Feature)
		if err != nil {
			return nil, err
		}
		components = append(components, diagram.AssemblyComponent{
			FeatureClass: c.FeatureClass,
			FeatureID:    id,
		})
	}

	return diagram.GenerateAssemblyDiagram(ctx, db, diagram.GenerateAssemblyDiagramInput{
		ContainerClass: in.PackagedFeatureClass,
		ContainerID:    enclosureID,
		DiagramType:    diagram.AssemblyDiagramType,
		Name:           in.Name,
		TemplateName:   templateName,
		Components:     components,
	})
}
