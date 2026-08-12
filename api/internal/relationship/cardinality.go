package relationship

import (
	"context"
	"fmt"

	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/uptrace/bun"
)

// checkCardinality enforces a relationship_classes row's cardinality
// against what's already established under it, before a new
// origin/destination pair is allowed to write one more row. Something
// network_rules-based connectivity has no equivalent of -- network_rules
// carries no cardinality concept, so this check is unique to this
// package.
//
//   - many_to_many: no cap, always permitted.
//   - one_to_many: one origin may relate to many destinations, but a
//     given destination may only relate to one origin -- rejected if the
//     destination already has an established relationship of this class
//     with a *different* origin.
//   - one_to_one: both sides capped at one -- rejected if either the
//     origin or the destination already appears in any established
//     relationship of this class other than the exact pair being
//     established right now.
//
// Establishing the exact same origin/destination pair again is always
// permitted regardless of cardinality -- that's establishRelationship's
// idempotent upsert path, not a new relationship for this check to count
// against the cap, so every case here explicitly excludes the pair
// currently being established from its "already exists" check.
func checkCardinality(ctx context.Context, db bun.IDB, class *metadata.RelationshipClass, originFeatureID, destinationFeatureID string) error {
	switch class.Cardinality {
	case "many_to_many":
		return nil

	case "one_to_many":
		exists, err := db.NewSelect().Model((*FeatureRelationship)(nil)).
			Where("relationship_class_id = ?", class.ID).
			Where("destination_feature_id = ?", destinationFeatureID).
			Where("origin_feature_id != ?", originFeatureID).
			Exists(ctx)
		if err != nil {
			return fmt.Errorf("relationship: checking cardinality: %w", err)
		}
		if exists {
			return invalid("cardinality violation: %q is one_to_many and this destination already has a different origin under this relationship class", class.Name)
		}
		return nil

	case "one_to_one":
		exists, err := db.NewSelect().Model((*FeatureRelationship)(nil)).
			Where("relationship_class_id = ?", class.ID).
			Where("NOT (origin_feature_id = ? AND destination_feature_id = ?)", originFeatureID, destinationFeatureID).
			WhereGroup(" AND ", func(q *bun.SelectQuery) *bun.SelectQuery {
				return q.WhereOr("origin_feature_id = ?", originFeatureID).
					WhereOr("destination_feature_id = ?", destinationFeatureID)
			}).
			Exists(ctx)
		if err != nil {
			return fmt.Errorf("relationship: checking cardinality: %w", err)
		}
		if exists {
			return invalid("cardinality violation: %q is one_to_one and one side of this pair is already related elsewhere under this relationship class", class.Name)
		}
		return nil

	default:
		return fmt.Errorf("relationship: unknown cardinality %q", class.Cardinality)
	}
}
