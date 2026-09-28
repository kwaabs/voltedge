package assembly

import (
	"context"

	"github.com/uptrace/bun"

	"github.com/kwaabs/voltedge/api/internal/diagram"
)

// assemblyDiagramTemplates maps a kit config code (the Config named in the create
// request -- e.g. POLE_MOUNT_STD) to the built-in single-line template
// (seed/assembly_diagram_templates.sql) whose typed slots match that kit's
// fanned-out components. A config with no entry gets no diagram (not an error).
var assemblyDiagramTemplates = map[string]string{
	"POLE_MOUNT_STD":       "Pole-mount transformer (overhead)",
	"POLE_MOUNT_RECLOSER":  "Pole-mount recloser",
	"POLE_MOUNT_CAP_BANK":  "Pole-mount capacitor bank",
	"POLE_MOUNT_REGULATOR": "Pole-mount voltage regulator",
	"POLE_MOUNT_BESS":      "Pole-mount battery storage",
	"PAD_MOUNT_URD":        "Pad-mount transformer + feeder pillar (URD)",
	"WAYS_4":               "Feeder pillar — 4-way",
	"WAYS_6":               "Feeder pillar — 6-way",
}

// buildAutoDiagram stands up the kit's single-line right after a successful
// fan-out: it roots a ONE_LINE diagram on the anchor (flipped to a diagram
// container by seed/assembly_diagram_templates.sql for POLE / DIST_XFMR) and
// binds every component into the matching template's slots. The anchor itself is
// offered as a component so a template with an anchor-class slot binds it (the
// URD's DIST_XFMR pad-mount transformer fills that template's DIST_XFMR slot); a
// pole anchor matches no slot and is harmlessly ignored. Best-effort -- the
// handler logs any error, never fails the create over a diagram.
func buildAutoDiagram(ctx context.Context, db *bun.DB, in CreateAssemblyInput, result *CreateAssemblyResult) (*diagram.GenerateAssemblyDiagramResult, error) {
	templateName := assemblyDiagramTemplates[in.Config]
	if templateName == "" {
		return nil, nil // no template for this config -- nothing to build, not an error
	}

	anchorID, err := featureID(result.Anchor)
	if err != nil {
		return nil, err
	}

	components := make([]diagram.AssemblyComponent, 0, len(result.Components)+1)
	// Offer the anchor first so a same-class slot binds it before a component.
	components = append(components, diagram.AssemblyComponent{
		FeatureClass: in.AnchorFeatureClass,
		FeatureID:    anchorID,
	})
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
		ContainerClass: in.AnchorFeatureClass,
		ContainerID:    anchorID,
		DiagramType:    diagram.AssemblyDiagramType,
		Name:           in.Name,
		TemplateName:   templateName,
		Components:     components,
	})
}
