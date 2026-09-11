package duct

import (
	"context"
	"fmt"

	"github.com/uptrace/bun"
)

// DuctOption is one addable cable-duct class offered for the map's add-feature menu.
// DuctFeatureClass is exactly what the frontend sends back to POST /ducts (as
// duct_feature_class); the name drives the menu label. There's no config axis (every
// way is identical, so the fan-out count comes off the duct's own way_count) -- so,
// unlike an RMU option, one entry per duct class, not per (class, config).
type DuctOption struct {
	DuctFeatureClass     string `json:"duct_feature_class"`
	DuctFeatureClassName string `json:"duct_feature_class_name"`
}

// ListDuctOptions returns every feature class that fans out DUCT_WAY children -- i.e.
// is the ORIGIN of a `contains DUCT_WAY` containment relationship class (seeded by
// seed/duct_way_example.sql). One entry per such duct class. The result is empty
// until that seed is applied, so the frontend "Cable duct" fan-out affordance simply
// doesn't appear until the catalog is present -- and this package never names a
// single class code. Classes are only offered once compiled (a draft class has no
// features to place ways under).
func ListDuctOptions(ctx context.Context, db bun.IDB) ([]DuctOption, error) {
	const query = `
SELECT dfc.code, dfc.name
FROM metadata.relationship_classes rc
JOIN metadata.feature_classes dfc ON dfc.id = rc.origin_feature_class_id
JOIN metadata.feature_classes wfc ON wfc.id = rc.destination_feature_class_id
WHERE rc.kind = 'containment'
  AND wfc.code = 'DUCT_WAY'
  AND dfc.status = 'active'
ORDER BY dfc.name`

	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("duct: listing options: %w", err)
	}
	defer rows.Close()

	var options []DuctOption
	for rows.Next() {
		var o DuctOption
		if err := rows.Scan(&o.DuctFeatureClass, &o.DuctFeatureClassName); err != nil {
			return nil, fmt.Errorf("duct: scanning option: %w", err)
		}
		options = append(options, o)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("duct: iterating options: %w", err)
	}
	return options, nil
}
