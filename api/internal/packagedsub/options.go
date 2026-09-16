package packagedsub

import (
	"context"
	"fmt"

	"github.com/uptrace/bun"
)

// PackagedSubOption is one addable packaged substation offered for the map's
// Assemblies menu: which class to create, which configuration to build it as (its
// template selects the internal-kit fan-out) and which tenure subtype (permanent
// vs mobile) to stamp. PackagedFeatureClass + Config + Subtype are exactly what
// the frontend sends back to POST /packaged-substations (as packaged_feature_
// class + attributes.package_config + subtype). Names/labels drive the menu.
//
// One option is emitted per (config x tenure subtype): a template says "these
// components make a COMPACT unit", and the two subtypes say "as a permanent one"
// and "as a mobile one" -- the same kit either way, so the cross product is the
// addable set.
type PackagedSubOption struct {
	PackagedFeatureClass     string `json:"packaged_feature_class"`
	PackagedFeatureClassName string `json:"packaged_feature_class_name"`
	Config                   string `json:"config"`
	ConfigLabel              string `json:"config_label"`
	Subtype                  int    `json:"subtype"`
	SubtypeLabel             string `json:"subtype_label"`
	TemplateName             string `json:"template_name"`
}

// ListPackagedSubOptions returns every (packaged class, config, tenure subtype)
// that has a fan-out template (metadata.packaged_substation_templates) crossed
// with the class's subtypes -- one entry per addable packaged substation. Like
// RMU (and unlike panels) this is not diagram-scoped: the unit is placed straight
// on the map, so the only gate is "does a template exist." The result is empty
// until seed/packaged_substation_templates.sql is applied, so the frontend
// affordance simply doesn't appear until the catalog is present.
func ListPackagedSubOptions(ctx context.Context, db bun.IDB) ([]PackagedSubOption, error) {
	const query = `
SELECT pfc.code, pfc.name, cv.code, cv.label, st.code, st.name, pt.name
FROM metadata.packaged_substation_templates pt
JOIN metadata.feature_classes pfc ON pfc.id = pt.packaged_feature_class_id
JOIN metadata.domain_values cv    ON cv.id = pt.config_value_id
JOIN metadata.subtypes st         ON st.feature_class_id = pfc.id
ORDER BY pfc.name, cv.sort_order, cv.label, st.code`

	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("packagedsub: listing options: %w", err)
	}
	defer rows.Close()

	var options []PackagedSubOption
	for rows.Next() {
		var o PackagedSubOption
		if err := rows.Scan(&o.PackagedFeatureClass, &o.PackagedFeatureClassName, &o.Config, &o.ConfigLabel, &o.Subtype, &o.SubtypeLabel, &o.TemplateName); err != nil {
			return nil, fmt.Errorf("packagedsub: scanning option: %w", err)
		}
		options = append(options, o)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("packagedsub: iterating options: %w", err)
	}
	return options, nil
}
