package assembly

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/uptrace/bun"

	"github.com/kwaabs/voltedge/api/internal/diagram"
)

// KitDiagramPreviewElement is one positioned entry of a kit's single-line TEMPLATE,
// trimmed to what the editor preview renders (kind + position + size + a label).
// It is the stored template skeleton, NOT a generated per-component diagram -- a
// "which single-line does this kit produce" picture, bound to no real features.
type KitDiagramPreviewElement struct {
	Kind      string   `json:"kind"`
	X         float64  `json:"x"`
	Y         float64  `json:"y"`
	Width     *float64 `json:"width,omitempty"`
	Height    *float64 `json:"height,omitempty"`
	Text      *string  `json:"text,omitempty"`
	SlotClass *string  `json:"slot_class,omitempty"`
}

// KitDiagramPreview is the resolved single-line template for a kit: its name (empty
// when the kit generates no single-line) and the template's positioned elements.
type KitDiagramPreview struct {
	TemplateName string                     `json:"template_name"`
	Elements     []KitDiagramPreviewElement `json:"elements"`
}

// KitDiagramPreviewFor resolves the single-line template a kit maps to and returns
// its skeleton for the editor preview (#2-light). An explicit name (the editor's
// own diagram-template field, when set) wins; otherwise it resolves by (anchor
// class, config) exactly like buildAutoDiagram does on placement. A linked-but-
// missing template, or a kit with no template at all, returns an empty element list
// (the name alone still tells the author what will be generated).
func KitDiagramPreviewFor(ctx context.Context, db *bun.DB, anchorClass, config, explicitName string) (*KitDiagramPreview, error) {
	name := strings.TrimSpace(explicitName)
	if name == "" {
		resolved, err := kitDiagramTemplateName(ctx, db, anchorClass, config)
		if err != nil {
			return nil, err
		}
		name = resolved
	}
	out := &KitDiagramPreview{TemplateName: name, Elements: []KitDiagramPreviewElement{}}
	if name == "" {
		return out, nil
	}
	var payload []byte
	err := db.QueryRowContext(ctx,
		`SELECT payload FROM runtime.diagram_templates WHERE name = ? ORDER BY created_at DESC LIMIT 1`, name).Scan(&payload)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return out, nil // name linked but template row gone -- still report the name
	case err != nil:
		return nil, err
	}
	var p struct {
		Elements []KitDiagramPreviewElement `json:"elements"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return nil, err
	}
	if p.Elements != nil {
		out.Elements = p.Elements
	}
	return out, nil
}

// kitDiagramTemplateName resolves which runtime.diagram_templates row backs a kit's
// single-line. The link is DATA-DRIVEN (migration 0163): it reads
// metadata.assembly_templates.diagram_template_name for the (anchor class, config),
// so an in-app-authored kit (which the Go binary has never heard of) gets its
// generated/linked template too. Empty (no row, or a NULL link) means "no diagram"
// -- not an error. This replaced a hardcoded config->name Go map; the built-ins'
// links were backfilled into the column by migration 0163.
func kitDiagramTemplateName(ctx context.Context, db *bun.DB, anchorClass, config string) (string, error) {
	var name *string
	err := db.QueryRowContext(ctx, `
SELECT at.diagram_template_name
FROM metadata.assembly_templates at
JOIN metadata.feature_classes afc ON afc.id = at.anchor_feature_class_id
WHERE afc.code = ? AND at.code = ?`, anchorClass, config).Scan(&name)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return "", nil
	case err != nil:
		return "", err
	}
	if name == nil {
		return "", nil
	}
	return *name, nil
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
	templateName, err := kitDiagramTemplateName(ctx, db, in.AnchorFeatureClass, in.Config)
	if err != nil {
		return nil, err
	}
	if templateName == "" {
		return nil, nil // no template linked for this kit -- nothing to build, not an error
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
