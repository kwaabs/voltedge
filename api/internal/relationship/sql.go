package relationship

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

// FeatureRelationship mirrors runtime.feature_relationships
// (migrations/0017). Fixed schema known at compile time, same as
// api/internal/connectivity's Connectivity model, so it's a plain bun
// model rather than hand-built SQL.
//
// alias:feature_relationships is not optional decoration -- without it
// bun derives the insert's SQL alias from the Go struct name
// ("feature_relationship", singular, lowercased) instead of the table's
// own name, which broke api/internal/connectivity's identical upsert
// pattern in exactly this way (see docs/phase-2-connectivity.md's bug
// writeup). Pinning it here avoids repeating that bug.
type FeatureRelationship struct {
	bun.BaseModel `bun:"table:runtime.feature_relationships,alias:feature_relationships"`

	ID                        string    `bun:"id,pk,nullzero"`
	RelationshipClassID       string    `bun:"relationship_class_id"`
	OriginFeatureClassID      string    `bun:"origin_feature_class_id"`
	OriginFeatureID           string    `bun:"origin_feature_id"`
	DestinationFeatureClassID string    `bun:"destination_feature_class_id"`
	DestinationFeatureID      string    `bun:"destination_feature_id"`
	EstablishedAt             time.Time `bun:"established_at,nullzero"`
	EstablishedBy             *string   `bun:"established_by"`
}

// resolvedFeature is one side of an establish request after its feature
// class and specific feature row have been resolved and validated.
// Simpler than connectivity's resolvedEndpoint -- relationship_classes
// has no terminal concept, so there's nothing to resolve beyond
// existence and deleted_at.
type resolvedFeature struct {
	ref    featureRef
	loaded *metadata.Loaded
}

// loadFeatureClass mirrors crud's loadOrNotFound and connectivity's copy
// of the same -- translating sql.ErrNoRows into this package's own
// notFoundError so writeError never has to know about another package's
// error types.
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

// resolveFeature validates one side of an establish request: the
// feature class must exist and be compiled, and the specific feature row
// must exist and not be soft-deleted -- relating a deleted feature makes
// no more sense here than connecting one does.
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
		return nil, invalid("feature %s (%s) is deleted -- restore it before relating", ref.FeatureID, ref.FeatureClass)
	}

	return &resolvedFeature{ref: ref, loaded: loaded}, nil
}

// establishRelationship is the only path that turns a candidate
// structural relationship into a recorded fact: resolve both features,
// require a matching relationship_classes row for their feature classes
// (zero trust -- matchClass rejects if none exists, or if the request's
// kind doesn't disambiguate an ambiguous match), require the matched
// class's cardinality not already be exhausted, then write. The insert
// is an idempotent upsert on the table's (relationship_class_id,
// origin_feature_id, destination_feature_id) unique constraint --
// establishing the same relationship twice is a safe no-op, matching
// this codebase's existing idempotency pattern.
func establishRelationship(ctx context.Context, db bun.IDB, req establishRequest) (*relationshipResponse, error) {
	origin, err := resolveFeature(ctx, db, req.Origin)
	if err != nil {
		return nil, err
	}
	destination, err := resolveFeature(ctx, db, req.Destination)
	if err != nil {
		return nil, err
	}

	classes, err := metadata.LoadRelationshipClasses(ctx, db, origin.loaded.FeatureClass.ID, destination.loaded.FeatureClass.ID)
	if err != nil {
		return nil, err
	}
	class, err := matchClass(classes, req.Kind)
	if err != nil {
		return nil, err
	}

	if err := checkCardinality(ctx, db, class, req.Origin.FeatureID, req.Destination.FeatureID); err != nil {
		return nil, err
	}

	establishedBy := "api"
	rel := &FeatureRelationship{
		RelationshipClassID:       class.ID,
		OriginFeatureClassID:      origin.loaded.FeatureClass.ID,
		OriginFeatureID:           req.Origin.FeatureID,
		DestinationFeatureClassID: destination.loaded.FeatureClass.ID,
		DestinationFeatureID:      req.Destination.FeatureID,
		EstablishedBy:             &establishedBy,
	}

	_, err = db.NewInsert().Model(rel).
		On("CONFLICT (relationship_class_id, origin_feature_id, destination_feature_id) DO UPDATE").
		Set("established_at = feature_relationships.established_at").
		Returning("*").
		Exec(ctx)
	if err != nil {
		return nil, fmt.Errorf("relationship: establish: %w", err)
	}

	return formatRelationship(rel, class, origin, destination), nil
}

// listRelationships returns every relationship involving one feature, on
// either side.
func listRelationships(ctx context.Context, db bun.IDB, featureClassCode, featureID string) ([]relationshipResponse, error) {
	loaded, err := loadFeatureClass(ctx, db, featureClassCode)
	if err != nil {
		return nil, err
	}
	fcID := loaded.FeatureClass.ID

	var rows []FeatureRelationship
	if err := db.NewSelect().Model(&rows).
		Where("(origin_feature_id = ? AND origin_feature_class_id = ?) OR (destination_feature_id = ? AND destination_feature_class_id = ?)",
			featureID, fcID, featureID, fcID).
		Order("established_at").
		Scan(ctx); err != nil {
		return nil, fmt.Errorf("relationship: list: %w", err)
	}

	return formatRelationships(ctx, db, rows)
}

// revokeRelationship permanently removes an established relationship.
// No soft-delete/restore pair, same reasoning as connectivity's revoke:
// a relationship is a binary fact, established or not, with no
// draft/published life cycle of its own to preserve.
func revokeRelationship(ctx context.Context, db bun.IDB, id string) error {
	res, err := db.NewDelete().Model((*FeatureRelationship)(nil)).Where("id = ?", id).Exec(ctx)
	if err != nil {
		return fmt.Errorf("relationship: revoke: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("relationship: revoke: %w", err)
	}
	if n == 0 {
		return notFound("relationship %s not found", id)
	}
	return nil
}

// formatRelationship builds the response for a relationship just
// established, reusing the already-validated request-side featureRefs
// verbatim.
func formatRelationship(rel *FeatureRelationship, class *metadata.RelationshipClass, origin, destination *resolvedFeature) *relationshipResponse {
	return &relationshipResponse{
		ID:                    rel.ID,
		RelationshipClassID:   class.ID,
		RelationshipClassName: class.Name,
		Kind:                  class.Kind,
		Cardinality:           class.Cardinality,
		Origin:                origin.ref,
		Destination:           destination.ref,
		EstablishedAt:         rel.EstablishedAt,
	}
}

// formatRelationships turns raw FeatureRelationship rows into the same
// human-readable shape formatRelationship produces for a fresh
// establish, via a handful of batched lookups rather than one query per
// row's referenced feature class/relationship class.
func formatRelationships(ctx context.Context, db bun.IDB, rows []FeatureRelationship) ([]relationshipResponse, error) {
	if len(rows) == 0 {
		return []relationshipResponse{}, nil
	}

	fcIDs := map[string]struct{}{}
	classIDs := map[string]struct{}{}
	for _, r := range rows {
		fcIDs[r.OriginFeatureClassID] = struct{}{}
		fcIDs[r.DestinationFeatureClassID] = struct{}{}
		classIDs[r.RelationshipClassID] = struct{}{}
	}

	fcCodes, err := loadFeatureClassCodes(ctx, db, keys(fcIDs))
	if err != nil {
		return nil, err
	}
	classes, err := loadRelationshipClassesByID(ctx, db, keys(classIDs))
	if err != nil {
		return nil, err
	}

	out := make([]relationshipResponse, 0, len(rows))
	for _, r := range rows {
		class := classes[r.RelationshipClassID]
		out = append(out, relationshipResponse{
			ID:                    r.ID,
			RelationshipClassID:   r.RelationshipClassID,
			RelationshipClassName: class.Name,
			Kind:                  class.Kind,
			Cardinality:           class.Cardinality,
			Origin: featureRef{
				FeatureClass: fcCodes[r.OriginFeatureClassID],
				FeatureID:    r.OriginFeatureID,
			},
			Destination: featureRef{
				FeatureClass: fcCodes[r.DestinationFeatureClassID],
				FeatureID:    r.DestinationFeatureID,
			},
			EstablishedAt: r.EstablishedAt,
		})
	}
	return out, nil
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
		return nil, fmt.Errorf("relationship: loading feature classes: %w", err)
	}
	out := make(map[string]string, len(fcs))
	for _, fc := range fcs {
		out[fc.ID] = fc.Code
	}
	return out, nil
}

func loadRelationshipClassesByID(ctx context.Context, db bun.IDB, ids []string) (map[string]metadata.RelationshipClass, error) {
	var classes []metadata.RelationshipClass
	if err := db.NewSelect().Model(&classes).Where("id IN (?)", bun.In(ids)).Scan(ctx); err != nil {
		return nil, fmt.Errorf("relationship: loading relationship classes: %w", err)
	}
	out := make(map[string]metadata.RelationshipClass, len(classes))
	for _, c := range classes {
		out[c.ID] = c
	}
	return out, nil
}
