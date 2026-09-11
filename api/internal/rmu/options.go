package rmu

import (
	"context"
	"fmt"

	"github.com/uptrace/bun"
)

// RMUOption is one addable RMU offered for the map's add-feature menu: which
// RMU class to create and which configuration to build it as (its template
// selects the way fan-out). RMUFeatureClass + Config are exactly what the
// frontend sends back to POST /rmus (as rmu_feature_class + attributes.
// rmu_config). Name/labels drive the menu.
type RMUOption struct {
	RMUFeatureClass     string `json:"rmu_feature_class"`
	RMUFeatureClassName string `json:"rmu_feature_class_name"`
	Config              string `json:"config"`
	ConfigLabel         string `json:"config_label"`
	TemplateName        string `json:"template_name"`
}

// ListRMUOptions returns every (RMU class, config) that has a fan-out template
// (metadata.rmu_templates) -- one entry per addable RMU. Unlike panels this is
// not diagram-scoped: an RMU is placed straight on the map, so the only gate is
// "does a template exist." The result is empty until seed/rmu_templates.sql is
// applied, so the frontend "Add RMU" affordance simply doesn't appear until the
// catalog is present -- without this package naming a single config code.
func ListRMUOptions(ctx context.Context, db bun.IDB) ([]RMUOption, error) {
	const query = `
SELECT rfc.code, rfc.name, cv.code, cv.label, rt.name
FROM metadata.rmu_templates rt
JOIN metadata.feature_classes rfc ON rfc.id = rt.rmu_feature_class_id
JOIN metadata.domain_values cv    ON cv.id = rt.config_value_id
ORDER BY rfc.name, cv.sort_order, cv.label`

	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("rmu: listing options: %w", err)
	}
	defer rows.Close()

	var options []RMUOption
	for rows.Next() {
		var o RMUOption
		if err := rows.Scan(&o.RMUFeatureClass, &o.RMUFeatureClassName, &o.Config, &o.ConfigLabel, &o.TemplateName); err != nil {
			return nil, fmt.Errorf("rmu: scanning option: %w", err)
		}
		options = append(options, o)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rmu: iterating options: %w", err)
	}
	return options, nil
}
