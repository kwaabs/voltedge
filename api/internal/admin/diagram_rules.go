package admin

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/uptrace/bun"
)

// diagramTypeDomainCode is the fixed domain every diagram type value
// belongs to -- the same constant api/internal/diagram uses (kept a
// private copy here rather than exported so neither package depends on
// the other for a string literal).
const diagramTypeDomainCode = "DIAGRAM_TYPE"

// diagramPlacementRuleRow mirrors metadata.diagram_placement_rules
// (migrations/0068): one row = one feature class allowed on a given
// (container class × diagram type) pairing. This is the authoring side of
// the read-only DiagramPlacementRule model api/internal/diagram enforces
// in placeFeature.
type diagramPlacementRuleRow struct {
	bun.BaseModel `bun:"table:metadata.diagram_placement_rules,alias:dpr"`

	ID                      string `bun:"id,pk,nullzero"`
	ContainerFeatureClassID string `bun:"container_feature_class_id"`
	DiagramTypeValueID      string `bun:"diagram_type_value_id"`
	FeatureClassID          string `bun:"feature_class_id"`
}

// diagramPlacementRuleRequest is the create body: everything by human
// code, never raw ids, same convention every other admin write uses.
type diagramPlacementRuleRequest struct {
	ContainerClass string `json:"container_class"`
	DiagramType    string `json:"diagram_type"`
	FeatureClass   string `json:"feature_class"`
}

// diagramPlacementRuleResponse carries codes AND display names/labels so
// the admin page renders each rule without a second lookup per row.
type diagramPlacementRuleResponse struct {
	ID               string `json:"id"`
	ContainerClass   string `json:"container_class"`
	ContainerName    string `json:"container_name"`
	DiagramType      string `json:"diagram_type"`
	DiagramTypeLabel string `json:"diagram_type_label"`
	FeatureClass     string `json:"feature_class"`
	FeatureClassName string `json:"feature_class_name"`
}

// listDiagramPlacementRules returns every placement rule, resolved to
// codes + names. The admin page groups them by (container_class,
// diagram_type) to show one allow-list per pairing; a pairing with no
// rows here simply doesn't appear, which is exactly the unconstrained
// "any placeable class" case the enforcement treats as opt-in.
func listDiagramPlacementRules(ctx context.Context, db *bun.DB) ([]diagramPlacementRuleResponse, error) {
	var rows []diagramPlacementRuleRow
	if err := db.NewSelect().Model(&rows).Order("created_at").Scan(ctx); err != nil {
		return nil, err
	}

	classes, err := metadata.ListAllFeatureClasses(ctx, db)
	if err != nil {
		return nil, err
	}
	classByID := make(map[string]metadata.FeatureClass, len(classes))
	for _, c := range classes {
		classByID[c.ID] = c
	}

	typeByID, err := diagramTypeValuesByID(ctx, db)
	if err != nil {
		return nil, err
	}

	out := make([]diagramPlacementRuleResponse, 0, len(rows))
	for _, row := range rows {
		container := classByID[row.ContainerFeatureClassID]
		feature := classByID[row.FeatureClassID]
		dtype := typeByID[row.DiagramTypeValueID]
		out = append(out, diagramPlacementRuleResponse{
			ID:               row.ID,
			ContainerClass:   container.Code,
			ContainerName:    container.Name,
			DiagramType:      dtype.Code,
			DiagramTypeLabel: dtype.Label,
			FeatureClass:     feature.Code,
			FeatureClassName: feature.Name,
		})
	}
	return out, nil
}

// createDiagramPlacementRule allows one feature class on one (container ×
// diagram type) pairing. Idempotent: re-allowing the same triple is a
// no-op that returns the existing rule (ON CONFLICT DO NOTHING on the
// unique index), so the UI's "add" never 500s on a double-click.
func createDiagramPlacementRule(ctx context.Context, db *bun.DB, req diagramPlacementRuleRequest) (*diagramPlacementRuleResponse, error) {
	if strings.TrimSpace(req.ContainerClass) == "" || strings.TrimSpace(req.DiagramType) == "" || strings.TrimSpace(req.FeatureClass) == "" {
		return nil, invalid("container_class, diagram_type, and feature_class are all required")
	}

	container, err := featureClassByCode(ctx, db, req.ContainerClass)
	if err != nil {
		return nil, err
	}
	// Only a real diagram container can host a diagram at all, so a rule
	// naming a non-container as the container is meaningless -- reject it
	// here rather than storing a row that could never match a real diagram.
	if !container.IsDiagramContainer {
		return nil, invalid("feature class %q is not a diagram container (is_diagram_container = false), so it can't host a diagram to restrict", container.Code)
	}

	dtype, err := metadata.ResolveDomainValueByCode(ctx, db, diagramTypeDomainCode, req.DiagramType)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, notFound("diagram type %q is not a value under the DIAGRAM_TYPE domain", req.DiagramType)
		}
		return nil, err
	}

	feature, err := featureClassByCode(ctx, db, req.FeatureClass)
	if err != nil {
		return nil, err
	}
	// A class that isn't globally placeable can never appear on any diagram,
	// so allow-listing it would be a dead rule the placement gate rejects
	// first anyway -- catch it here with a message that points at the fix.
	if !feature.IsDiagramPlaceable {
		return nil, invalid("feature class %q is not diagram-placeable (is_diagram_placeable = false); make it placeable before allow-listing it", feature.Code)
	}

	row := &diagramPlacementRuleRow{
		ContainerFeatureClassID: container.ID,
		DiagramTypeValueID:      dtype.ID,
		FeatureClassID:          feature.ID,
	}
	if _, err := db.NewInsert().Model(row).
		On("CONFLICT (container_feature_class_id, diagram_type_value_id, feature_class_id) DO NOTHING").
		Exec(ctx); err != nil {
		return nil, fmt.Errorf("admin: create diagram placement rule: %w", err)
	}
	// DO NOTHING leaves row.ID empty on a conflict, so read the row back by
	// its triple to return a stable id whether it was just inserted or
	// already there.
	stored := new(diagramPlacementRuleRow)
	if err := db.NewSelect().Model(stored).
		Where("container_feature_class_id = ? AND diagram_type_value_id = ? AND feature_class_id = ?",
			container.ID, dtype.ID, feature.ID).
		Scan(ctx); err != nil {
		return nil, fmt.Errorf("admin: create diagram placement rule: %w", err)
	}

	return &diagramPlacementRuleResponse{
		ID:               stored.ID,
		ContainerClass:   container.Code,
		ContainerName:    container.Name,
		DiagramType:      dtype.Code,
		DiagramTypeLabel: dtype.Label,
		FeatureClass:     feature.Code,
		FeatureClassName: feature.Name,
	}, nil
}

// deleteDiagramPlacementRule disallows one feature (removes one rule) by
// id. Deleting the LAST rule for a pairing returns it to the unconstrained
// "any placeable class" state -- there's nothing special to do for that, it
// falls out of the enforcement's "no rows = opt-in" rule for free.
func deleteDiagramPlacementRule(ctx context.Context, db *bun.DB, id string) error {
	res, err := db.NewDelete().Model((*diagramPlacementRuleRow)(nil)).Where("id = ?", id).Exec(ctx)
	if err != nil {
		return fmt.Errorf("admin: delete diagram placement rule: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("admin: delete diagram placement rule: %w", err)
	}
	if n == 0 {
		return notFound("diagram placement rule %s not found", id)
	}
	return nil
}

// featureClassByCode loads one feature class by its code, translating a
// missing row into this package's notFound. Used by the diagram-rules
// handlers to resolve the container + allowed class from the codes the
// request carries.
func featureClassByCode(ctx context.Context, db bun.IDB, code string) (*metadata.FeatureClass, error) {
	fc := new(metadata.FeatureClass)
	if err := db.NewSelect().Model(fc).Where("code = ?", code).Scan(ctx); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, notFound("feature class %q does not exist", code)
		}
		return nil, err
	}
	return fc, nil
}

// diagramTypeValuesByID returns every DIAGRAM_TYPE domain value keyed by
// its id -- the lookup listDiagramPlacementRules uses to label each rule's
// diagram type without a join.
func diagramTypeValuesByID(ctx context.Context, db bun.IDB) (map[string]metadata.DomainValue, error) {
	domains, err := metadata.ListDomains(ctx, db)
	if err != nil {
		return nil, err
	}
	var domainID string
	for _, d := range domains {
		if d.Code == diagramTypeDomainCode {
			domainID = d.ID
			break
		}
	}
	out := map[string]metadata.DomainValue{}
	if domainID == "" {
		// No DIAGRAM_TYPE domain at all -- no rules can exist either (FK), so
		// an empty map is correct, not an error.
		return out, nil
	}
	values, err := metadata.ListDomainValues(ctx, db)
	if err != nil {
		return nil, err
	}
	for _, v := range values {
		if v.DomainID == domainID {
			out[v.ID] = v
		}
	}
	return out, nil
}
