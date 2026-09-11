package export

// CIM container hierarchy -- Phase 47 Slice 4, docs/phase-47-substation-site-model.md.
//
// The Slice-1 CIM export placed equipment into a Substation purely by geometry
// (a point-in-polygon test against the substation's drawn boundary), which is all
// it could do: the intermediate CIM container tier -- Bay -- maps to VoltEdge's BAY
// feature class, a none-geometry logical grouping with no footprint to test a point
// against. Its membership lives instead in runtime.feature_relationships as
// 'containment' edges (SUBSTATION contains BAY, BAY contains a breaker, ...). This
// loads that graph so the encoder can emit the real modeled
// Substation -> Bay -> equipment hierarchy.
//
// (The Phase 47 minimal-model correction, migration 0086, retired the VOLTAGE_LEVEL
// tier: voltage level is derivable, so it is carried per equipment via BaseVoltage
// references rather than modeled as its own container. BAY is the one modeled tier.)

import (
	"context"
	"fmt"

	"github.com/uptrace/bun"
)

// contRow is a minimal read-only projection of one containment relationship
// instance: which container (origin) holds which child (destination). The
// relationship kind is filtered in SQL via a join to metadata.relationship_classes
// (kind lives on the class, not the instance row).
type contRow struct {
	bun.BaseModel `bun:"table:runtime.feature_relationships,alias:fr"`

	OriginFeatureID      string `bun:"origin_feature_id"`
	DestinationFeatureID string `bun:"destination_feature_id"`
}

// loadContainment returns a child-feature-id -> container-feature-id map for
// every 'containment' relationship whose BOTH endpoints are inside the exported
// feature set (so the graph is closed: no child points at a container that isn't
// in the file). A child is contained by at most one parent (containment is
// one_to_many -- the destination side is singular), so a flat map is exact, and
// because containers can themselves be contained (a BAY inside a VOLTAGE_LEVEL
// inside a SUBSTATION) the one map expresses the whole tree.
func loadContainment(ctx context.Context, db bun.IDB, layers []Layer) (map[string]string, error) {
	inSet := map[string]bool{}
	for _, l := range layers {
		for _, f := range l.Features {
			inSet[f.ID] = true
		}
	}
	if len(inSet) == 0 {
		return nil, nil
	}

	var rows []contRow
	err := db.NewSelect().
		Model(&rows).
		Join("JOIN metadata.relationship_classes rc ON rc.id = fr.relationship_class_id").
		Where("rc.kind = ?", "containment").
		Scan(ctx)
	if err != nil {
		return nil, fmt.Errorf("export: loading containment: %w", err)
	}

	out := make(map[string]string, len(rows))
	for _, r := range rows {
		if inSet[r.OriginFeatureID] && inSet[r.DestinationFeatureID] {
			out[r.DestinationFeatureID] = r.OriginFeatureID
		}
	}
	return out, nil
}
