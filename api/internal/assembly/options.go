package assembly

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/uptrace/bun"
)

// AssemblyOption is one addable kit offered for the map's Assemblies menu: which
// anchor class to place, which recipe Config to build (its template drives the
// fan-out) and, when the recipe forces one, the anchor subtype to stamp.
// AnchorFeatureClass + Config are exactly what the frontend sends back to POST
// /assemblies. AnchorSubtype is non-nil only when the template fixes it (e.g. a URD
// unit is pad_mounted), so the create form can pre-select and lock the subtype;
// nil means the placing user picks (e.g. a pole's material).
type AssemblyOption struct {
	AnchorFeatureClass     string `json:"anchor_feature_class"`
	AnchorFeatureClassName string `json:"anchor_feature_class_name"`
	Config                 string `json:"config"`
	Name                   string `json:"name"`
	AnchorSubtype          *int   `json:"anchor_subtype,omitempty"`
	// AnchorAttributes are the template's default anchor attributes (migration 0194)
	// -- the create form seeds them so a kit's required config field (package_config
	// / rmu_config) arrives pre-selected instead of being re-entered. Omitted when
	// the kit declares none.
	AnchorAttributes map[string]any `json:"anchor_attributes,omitempty"`
}

// ListAssemblyOptions returns every (anchor class, config) that has a kit template
// -- one entry per addable assembly. Like rmu/packagedsub this is not diagram-
// scoped: the anchor is placed straight on the map, so the only gate is "does a
// template exist." The result is empty until the Phase 57 seeds are applied, so
// the frontend affordance simply doesn't appear until the catalog is present.
func ListAssemblyOptions(ctx context.Context, db bun.IDB) ([]AssemblyOption, error) {
	const query = `
SELECT afc.code, afc.name, at.code, at.name, at.anchor_subtype_code, at.anchor_attributes
FROM metadata.assembly_templates at
JOIN metadata.feature_classes afc ON afc.id = at.anchor_feature_class_id
ORDER BY afc.name, at.name`

	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("assembly: listing options: %w", err)
	}
	defer rows.Close()

	var options []AssemblyOption
	for rows.Next() {
		var (
			o        AssemblyOption
			rawAttrs []byte
		)
		if err := rows.Scan(&o.AnchorFeatureClass, &o.AnchorFeatureClassName, &o.Config, &o.Name, &o.AnchorSubtype, &rawAttrs); err != nil {
			return nil, fmt.Errorf("assembly: scanning option: %w", err)
		}
		if len(rawAttrs) > 0 {
			if err := json.Unmarshal(rawAttrs, &o.AnchorAttributes); err != nil {
				return nil, fmt.Errorf("assembly: decoding option anchor_attributes: %w", err)
			}
		}
		options = append(options, o)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("assembly: iterating options: %w", err)
	}
	return options, nil
}
