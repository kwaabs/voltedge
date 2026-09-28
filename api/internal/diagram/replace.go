package diagram

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/kwaabs/voltedge/api/internal/connectivity"
	"github.com/kwaabs/voltedge/api/internal/crud"
	"github.com/kwaabs/voltedge/api/internal/relationship"
	"github.com/uptrace/bun"
)

// replaceFromMap overwrites one already-placed diagram feature (a "stub" -- a
// geometry-less feature drawn onto the diagram by a template + promotion, e.g.
// an RMU cable stub) with a real feature picked on the geographic map, in one
// atomic step. It is the mirror image of "add from map": there the map pick
// creates a fresh placement; here it takes over an existing one.
//
// The whole swap runs in a single transaction so the diagram (and the live
// network) can never end up half-swapped:
//
//  1. load the stub placement (scoped to this diagram),
//  2. resolve the map feature and require it be the SAME feature class -- the
//     transfer moves wiring and re-points the placement by feature id alone,
//     which is only meaningful within one class,
//  3. refuse if that map feature is already on this diagram (the placement
//     table is unique per diagram/class/feature),
//  4. transfer the stub's connectivity + relationships onto the map feature so
//     the topology it was standing in for (the RMU's MV_SWITCH.T1 link) now
//     points at the real feature instead of being orphaned,
//  5. re-point the placement in place -- same id, coordinates, path, rotation
//     and label transform, so the map feature draws exactly where the stub sat,
//  6. soft-delete the now-orphaned stub feature.
//
// The metadata:write gate is enforced by the route mount (handler.go), not
// here -- same discipline promoteElement follows: turning template scaffolding
// into (and swapping it for) real shared data is an authoring act.
func replaceFromMap(ctx context.Context, db *bun.DB, diagramInstanceID, placementID string, req replaceRequest) (*replaceResponse, error) {
	var resp *replaceResponse
	err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		// 1. The stub placement being overwritten -- scoped to this diagram so a
		//    placement id from another diagram can't be touched (same guard
		//    removePlacement / updatePlacement use).
		stub := new(DiagramPlacement)
		if err := tx.NewSelect().Model(stub).
			Where("id = ? AND diagram_instance_id = ?", placementID, diagramInstanceID).
			Scan(ctx); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return notFound("placement %s not found on diagram %s", placementID, diagramInstanceID)
			}
			return fmt.Errorf("diagram: replace from map: %w", err)
		}
		stubFeatureID := stub.FeatureID

		// 2. The real map feature that will take the stub's place. resolveFeature
		//    checks the class is compiled and the row exists and isn't deleted.
		repl, err := resolveFeature(ctx, tx, featureRef{FeatureClass: req.FeatureClass, FeatureID: req.FeatureID})
		if err != nil {
			return err
		}
		classID := repl.loaded.FeatureClass.ID

		// 3. Same class only. A cross-class swap would orphan terminal-typed
		//    connections and dodge the placement allow-list the stub already
		//    cleared, so it is rejected rather than half-honoured.
		if stub.FeatureClassID != classID {
			return invalid("replace needs the same feature class -- the picked map feature (%s) doesn't match this diagram feature", req.FeatureClass)
		}
		if stubFeatureID == req.FeatureID {
			return invalid("the picked map feature is already this diagram feature")
		}

		// 4. The replacement must not already sit on this diagram -- the placement
		//    table is unique per (diagram, class, feature); a clear message beats a
		//    raw constraint violation.
		dup, err := tx.NewSelect().Model((*DiagramPlacement)(nil)).
			Where("diagram_instance_id = ? AND feature_class_id = ? AND feature_id = ?",
				diagramInstanceID, classID, req.FeatureID).
			Where("id <> ?", placementID).
			Exists(ctx)
		if err != nil {
			return fmt.Errorf("diagram: replace from map: %w", err)
		}
		if dup {
			return invalid("that map feature is already placed on this diagram")
		}

		// 5. Transfer the stub's wiring onto the replacement (same class, so only
		//    the feature id moves). An RMU cable stub carries exactly one
		//    connectivity edge (-> MV_SWITCH.T1); a hand-promoted stub may carry
		//    more, and either way both edges and relationships come along.
		connMoved, err := connectivity.RepointFeature(ctx, tx, classID, stubFeatureID, req.FeatureID)
		if err != nil {
			return err
		}
		relMoved, err := relationship.RepointFeature(ctx, tx, classID, stubFeatureID, req.FeatureID)
		if err != nil {
			return err
		}

		// 6. Re-point the placement in place: it keeps its id, coordinates, path,
		//    rotation and label transform, so the map feature draws exactly where
		//    the stub sat. The class is unchanged, so the allow-list the stub
		//    already cleared still holds -- no re-check needed.
		stub.FeatureID = req.FeatureID
		if _, err := tx.NewUpdate().Model(stub).Column("feature_id").WherePK().Exec(ctx); err != nil {
			return fmt.Errorf("diagram: replace from map: re-point placement: %w", err)
		}

		// 7. Retire the orphaned stub feature. Its wiring and placement now belong
		//    to the map feature, so a soft delete leaves nothing dangling (reads
		//    already hide a deleted feature's stray rows). Same class, so the
		//    replacement's loaded metadata addresses the stub's row too.
		if _, err := crud.DeleteFeature(ctx, tx, repl.loaded, stubFeatureID); err != nil {
			return err
		}

		resp = &replaceResponse{
			FeatureClass:       req.FeatureClass,
			FeatureID:          req.FeatureID,
			Placement:          toPlacementResponse(stub, req.FeatureClass),
			ConnectionsMoved:   connMoved,
			RelationshipsMoved: relMoved,
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return resp, nil
}
