package relationship

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/uptrace/bun"
)

// plausibilityThresholdMeters is a deliberately generous "same
// neighborhood" sanity bound, not a precision check -- it exists to
// catch a clearly-wrong pairing (a pole related to a conductor on the
// other side of the demo dataset), not to flag every relationship whose
// two features aren't survey-exact. Fixed for now; nothing in
// relationship_classes carries a per-class override.
const plausibilityThresholdMeters = 500.0

// checkPlausibility is the soft geometric sanity check
// docs/metadata-core.md's POLE sample describes ("relates to a
// conductor only via feature_relationships ... ST_DWithin only
// validating plausibility"): unlike matchClass/checkCardinality, it
// never blocks establishRelationship. A nil return covers "close
// enough," "too far apart to compare meaningfully" being the caller's
// call to make, not this function's, and "can't be compared at all"
// (either side is geometry_type = none, e.g. a diagram-only
// PROTECTIVE_RELAY, or one side's geometry happens to be null) alike --
// there's nothing implausible about relating two features that were
// never going to have comparable positions.
//
// Distance is computed server-side via a single cross-table query
// (ST_Distance on both geometries transformed to EPSG:4326 and cast to
// geography, so it comes back in real meters regardless of either
// feature class's own SRID) rather than fetching and parsing GeoJSON in
// Go -- PostGIS already does this correctly and it's one round trip
// either way.
func checkPlausibility(ctx context.Context, db bun.IDB, origin, destination *resolvedFeature) (*string, error) {
	if origin.loaded.FeatureClass.GeometryType == "none" || destination.loaded.FeatureClass.GeometryType == "none" {
		return nil, nil
	}

	originTable := *origin.loaded.FeatureClass.PhysicalTable
	destTable := *destination.loaded.FeatureClass.PhysicalTable

	query := fmt.Sprintf(
		`SELECT ST_Distance(ST_Transform(a.geom, 4326)::geography, ST_Transform(b.geom, 4326)::geography)
		 FROM %s a, %s b
		 WHERE a.id = ? AND b.id = ? AND a.geom IS NOT NULL AND b.geom IS NOT NULL`,
		originTable, destTable,
	)

	var distanceMeters float64
	err := db.QueryRowContext(ctx, query, origin.ref.FeatureID, destination.ref.FeatureID).Scan(&distanceMeters)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("relationship: plausibility check: %w", err)
	}

	if distanceMeters <= plausibilityThresholdMeters {
		return nil, nil
	}
	warning := fmt.Sprintf(
		"these features are %.0fm apart, further than the usual %.0fm plausibility bound for a relationship -- double check this is the right pairing",
		distanceMeters, plausibilityThresholdMeters,
	)
	return &warning, nil
}
