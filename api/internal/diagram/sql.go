package diagram

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/kwaabs/voltedge/api/internal/crud"
	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/uptrace/bun"
)

// diagramTypeDomain is the fixed domain code every diagram_type value
// belongs to -- "just another row in domains, not a new lookup table"
// per docs/metadata-core.md, same pattern trace direction and snapping
// target_type already use.
const diagramTypeDomain = "DIAGRAM_TYPE"

// DiagramInstance mirrors runtime.diagram_instances (migrations/0019).
// Fixed schema known at compile time, same as api/internal/connectivity's
// Connectivity model, so it's a plain bun model rather than hand-built
// SQL.
type DiagramInstance struct {
	bun.BaseModel `bun:"table:runtime.diagram_instances,alias:diagram_instances"`

	ID                      string    `bun:"id,pk,nullzero"`
	ContainerFeatureClassID string    `bun:"container_feature_class_id"`
	ContainerFeatureID      string    `bun:"container_feature_id"`
	DiagramTypeValueID      string    `bun:"diagram_type_value_id"`
	Name                    *string   `bun:"name"`
	CreatedAt               time.Time `bun:"created_at,nullzero"`
}

// DiagramPlacement mirrors runtime.diagram_placements (migrations/0019).
type DiagramPlacement struct {
	bun.BaseModel `bun:"table:runtime.diagram_placements,alias:diagram_placements"`

	ID                string    `bun:"id,pk,nullzero"`
	DiagramInstanceID string    `bun:"diagram_instance_id"`
	FeatureClassID    string    `bun:"feature_class_id"`
	FeatureID         string    `bun:"feature_id"`
	X                 float64   `bun:"x"`
	Y                 float64   `bun:"y"`
	Rotation          float64   `bun:"rotation"`
	CreatedAt         time.Time `bun:"created_at,nullzero"`
}

// resolvedFeature is one side of a request after its feature class and
// specific feature row have been resolved and validated -- same shape
// api/internal/relationship's identical helper uses.
type resolvedFeature struct {
	ref    featureRef
	loaded *metadata.Loaded
}

// loadFeatureClass mirrors connectivity/relationship's identical copy --
// translating sql.ErrNoRows into this package's own notFoundError so
// writeError never has to know about another package's error types.
func loadFeatureClass(ctx context.Context, db bun.IDB, code string) (*metadata.Loaded, error) {
	loaded, err := metadata.Load(ctx, db, code)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, notFound("feature class %q does not exist", code)
		}
		return nil, err
	}
	return loaded, nil
}

// resolveFeature validates one side of a request: the feature class
// must exist and be compiled, and the specific feature row must exist
// and not be soft-deleted -- placing a deleted feature on a diagram
// makes no more sense than connecting or relating one does.
func resolveFeature(ctx context.Context, db bun.IDB, ref featureRef) (*resolvedFeature, error) {
	loaded, err := loadFeatureClass(ctx, db, ref.FeatureClass)
	if err != nil {
		return nil, err
	}

	row, err := crud.GetFeature(ctx, db, loaded, ref.FeatureID)
	if err != nil {
		if crud.IsNotFoundError(err) {
			return nil, notFound("feature %s (%s) not found", ref.FeatureID, ref.FeatureClass)
		}
		return nil, err
	}
	if row["deleted_at"] != nil {
		return nil, invalid("feature %s (%s) is deleted -- restore it before placing on a diagram", ref.FeatureID, ref.FeatureClass)
	}

	return &resolvedFeature{ref: ref, loaded: loaded}, nil
}

// createInstance is the only path that turns a container feature into a
// real diagram: resolve the container, require its feature class have
// is_diagram_container = true (zero trust by omission, same discipline
// network_rules/relationship_classes enforce for their own kind of
// legality), resolve the requested diagram type against the
// DIAGRAM_TYPE domain, then write. No idempotency/upsert here unlike
// connectivity and relationship's establish -- a container can
// plausibly host more than one diagram (docs/metadata-core.md: "e.g. a
// one-line and a control-scheme diagram for the same substation"), so
// there's no natural uniqueness to upsert against.
func createInstance(ctx context.Context, db bun.IDB, req createInstanceRequest) (*instanceResponse, error) {
	container, err := resolveFeature(ctx, db, req.Container)
	if err != nil {
		return nil, err
	}
	if !container.loaded.FeatureClass.IsDiagramContainer {
		return nil, invalid("feature class %q is not a diagram container (is_diagram_container = false)", req.Container.FeatureClass)
	}

	diagramType, err := metadata.ResolveDomainValueByCode(ctx, db, diagramTypeDomain, req.DiagramType)
	if err != nil {
		return nil, invalid("diagram_type %q is not a valid DIAGRAM_TYPE value: %v", req.DiagramType, err)
	}

	inst := &DiagramInstance{
		ContainerFeatureClassID: container.loaded.FeatureClass.ID,
		ContainerFeatureID:      req.Container.FeatureID,
		DiagramTypeValueID:      diagramType.ID,
		Name:                    req.Name,
	}
	if _, err := db.NewInsert().Model(inst).Returning("*").Exec(ctx); err != nil {
		return nil, fmt.Errorf("diagram: create instance: %w", err)
	}

	return &instanceResponse{
		ID:          inst.ID,
		Container:   req.Container,
		DiagramType: req.DiagramType,
		Name:        inst.Name,
		CreatedAt:   inst.CreatedAt,
	}, nil
}

// getInstance fetches one diagram instance by id.
func getInstance(ctx context.Context, db bun.IDB, id string) (*instanceResponse, error) {
	inst := new(DiagramInstance)
	if err := db.NewSelect().Model(inst).Where("id = ?", id).Scan(ctx); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, notFound("diagram instance %s not found", id)
		}
		return nil, fmt.Errorf("diagram: get instance: %w", err)
	}
	return formatInstance(ctx, db, inst)
}

// listInstances returns every diagram instance hosted by one container
// feature.
func listInstances(ctx context.Context, db bun.IDB, containerFeatureClassCode, containerFeatureID string) ([]instanceResponse, error) {
	loaded, err := loadFeatureClass(ctx, db, containerFeatureClassCode)
	if err != nil {
		return nil, err
	}

	var rows []DiagramInstance
	if err := db.NewSelect().Model(&rows).
		Where("container_feature_class_id = ? AND container_feature_id = ?", loaded.FeatureClass.ID, containerFeatureID).
		Order("created_at").
		Scan(ctx); err != nil {
		return nil, fmt.Errorf("diagram: list instances: %w", err)
	}

	out := make([]instanceResponse, 0, len(rows))
	for i := range rows {
		formatted, err := formatInstance(ctx, db, &rows[i])
		if err != nil {
			return nil, err
		}
		out = append(out, *formatted)
	}
	return out, nil
}

// deleteInstance removes a diagram instance and, via ON DELETE CASCADE
// (migrations/0019), every placement on it. No soft-delete/restore pair
// -- same reasoning as connectivity/relationship's own revoke: this
// isn't a feature with a draft/published life cycle of its own.
func deleteInstance(ctx context.Context, db bun.IDB, id string) error {
	res, err := db.NewDelete().Model((*DiagramInstance)(nil)).Where("id = ?", id).Exec(ctx)
	if err != nil {
		return fmt.Errorf("diagram: delete instance: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("diagram: delete instance: %w", err)
	}
	if n == 0 {
		return notFound("diagram instance %s not found", id)
	}
	return nil
}

// renameInstance updates a diagram instance's own display name --
// there's no admin UI or metadata concept behind it, just a plain
// column set once at creation (createInstance) that a diagram canvas
// now also needs to be able to change after the fact.
func renameInstance(ctx context.Context, db bun.IDB, id string, name *string) (*instanceResponse, error) {
	inst := new(DiagramInstance)
	res, err := db.NewUpdate().Model(inst).
		Set("name = ?", name).
		Where("id = ?", id).
		Returning("*").
		Exec(ctx)
	if err != nil {
		return nil, fmt.Errorf("diagram: rename instance: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("diagram: rename instance: %w", err)
	}
	if n == 0 {
		return nil, notFound("diagram instance %s not found", id)
	}
	return formatInstance(ctx, db, inst)
}

// placeFeature puts (or moves) one feature on one diagram: resolve the
// diagram instance and the feature, then upsert on the table's own
// (diagram_instance_id, feature_class_id, feature_id) unique constraint
// -- placing the same feature again just updates its x/y/rotation,
// never creates a second placement.
func placeFeature(ctx context.Context, db bun.IDB, diagramInstanceID string, req placeRequest) (*placementResponse, error) {
	inst := new(DiagramInstance)
	if err := db.NewSelect().Model(inst).Where("id = ?", diagramInstanceID).Scan(ctx); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, notFound("diagram instance %s not found", diagramInstanceID)
		}
		return nil, fmt.Errorf("diagram: place feature: %w", err)
	}

	feature, err := resolveFeature(ctx, db, featureRef{FeatureClass: req.FeatureClass, FeatureID: req.FeatureID})
	if err != nil {
		return nil, err
	}

	placement := &DiagramPlacement{
		DiagramInstanceID: diagramInstanceID,
		FeatureClassID:    feature.loaded.FeatureClass.ID,
		FeatureID:         req.FeatureID,
		X:                 req.X,
		Y:                 req.Y,
		Rotation:          req.Rotation,
	}
	_, err = db.NewInsert().Model(placement).
		On("CONFLICT (diagram_instance_id, feature_class_id, feature_id) DO UPDATE").
		Set("x = EXCLUDED.x, y = EXCLUDED.y, rotation = EXCLUDED.rotation").
		Returning("*").
		Exec(ctx)
	if err != nil {
		return nil, fmt.Errorf("diagram: place feature: %w", err)
	}

	return &placementResponse{
		ID:           placement.ID,
		FeatureClass: req.FeatureClass,
		FeatureID:    req.FeatureID,
		X:            placement.X,
		Y:            placement.Y,
		Rotation:     placement.Rotation,
	}, nil
}

// listPlacements returns every feature placed on one diagram.
func listPlacements(ctx context.Context, db bun.IDB, diagramInstanceID string) ([]placementResponse, error) {
	var rows []DiagramPlacement
	if err := db.NewSelect().Model(&rows).
		Where("diagram_instance_id = ?", diagramInstanceID).
		Order("created_at").
		Scan(ctx); err != nil {
		return nil, fmt.Errorf("diagram: list placements: %w", err)
	}
	if len(rows) == 0 {
		return []placementResponse{}, nil
	}

	fcIDs := make(map[string]struct{}, len(rows))
	for _, r := range rows {
		fcIDs[r.FeatureClassID] = struct{}{}
	}
	fcCodes, err := loadFeatureClassCodes(ctx, db, keys(fcIDs))
	if err != nil {
		return nil, err
	}

	out := make([]placementResponse, 0, len(rows))
	for _, r := range rows {
		out = append(out, placementResponse{
			ID:           r.ID,
			FeatureClass: fcCodes[r.FeatureClassID],
			FeatureID:    r.FeatureID,
			X:            r.X,
			Y:            r.Y,
			Rotation:     r.Rotation,
		})
	}
	return out, nil
}

// removePlacement un-places one feature from one diagram -- a real
// delete, not a soft one, same as everywhere else in this package.
func removePlacement(ctx context.Context, db bun.IDB, diagramInstanceID, placementID string) error {
	res, err := db.NewDelete().Model((*DiagramPlacement)(nil)).
		Where("id = ? AND diagram_instance_id = ?", placementID, diagramInstanceID).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("diagram: remove placement: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("diagram: remove placement: %w", err)
	}
	if n == 0 {
		return notFound("placement %s not found on diagram %s", placementID, diagramInstanceID)
	}
	return nil
}

// formatInstance resolves a raw DiagramInstance row's feature-class ids
// and diagram-type value id into the human-readable codes
// instanceResponse exposes.
func formatInstance(ctx context.Context, db bun.IDB, inst *DiagramInstance) (*instanceResponse, error) {
	fcCodes, err := loadFeatureClassCodes(ctx, db, []string{inst.ContainerFeatureClassID})
	if err != nil {
		return nil, err
	}
	diagramTypeCode, err := metadata.DomainValueCode(ctx, db, inst.DiagramTypeValueID)
	if err != nil {
		return nil, fmt.Errorf("diagram: resolving diagram_type: %w", err)
	}

	return &instanceResponse{
		ID: inst.ID,
		Container: featureRef{
			FeatureClass: fcCodes[inst.ContainerFeatureClassID],
			FeatureID:    inst.ContainerFeatureID,
		},
		DiagramType: diagramTypeCode,
		Name:        inst.Name,
		CreatedAt:   inst.CreatedAt,
	}, nil
}

func keys(set map[string]struct{}) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	return out
}

func loadFeatureClassCodes(ctx context.Context, db bun.IDB, ids []string) (map[string]string, error) {
	var fcs []metadata.FeatureClass
	if err := db.NewSelect().Model(&fcs).Where("id IN (?)", bun.In(ids)).Scan(ctx); err != nil {
		return nil, fmt.Errorf("diagram: loading feature classes: %w", err)
	}
	out := make(map[string]string, len(fcs))
	for _, fc := range fcs {
		out[fc.ID] = fc.Code
	}
	return out, nil
}
