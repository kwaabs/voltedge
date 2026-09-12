package tie

import (
	"context"
	"fmt"

	"github.com/uptrace/bun"
)

// TieOption is one addable tie configuration for the map's add-feature menu: a
// switch class that can act as a normally-open tie (it carries the normal_state
// field with a NORMALLY_OPEN value, from Phase 48's field-pack) together with
// the conductor classes it may join. SwitchFeatureClass is exactly what the
// frontend sends back to POST /ties (as switch_feature_class); FeederFeatureClasses
// tells the frontend which classes to list features from for the two feeder
// picks. Splitting responsibility this way keeps this package from listing
// feature instances itself -- it names the eligible classes; the frontend reuses
// the existing feature-list surface to fill the pickers (the same split coupler
// uses).
type TieOption struct {
	SwitchFeatureClass     string   `json:"switch_feature_class"`
	SwitchFeatureClassName string   `json:"switch_feature_class_name"`
	FeederFeatureClasses   []string `json:"feeder_feature_classes"`
}

// ListTieOptions returns every active feature class that can be placed as a
// feeder-to-feeder tie: one that (a) carries a normal_state field whose domain
// offers the NORMALLY_OPEN value -- i.e. Phase 48's field-pack is applied -- AND
// (b) is authorized by BOTH switch rule 1 (`conductor -> switch.T1`) and rule 3
// (`switch.T2 -> conductor`) to sit between some conductor class, since without
// both a tie cannot actually be wired. For each such switch class it lists the
// conductor classes satisfying both rules. The result is empty until the
// field-pack seed + the switch wiring are present, so the "Create tie"
// affordance simply doesn't appear until the catalog supports it -- and this
// package never names a class code. Classes are only offered once compiled
// (status = 'active').
//
// The conductor side of both rules is a wildcard terminal (from_terminal on
// rule 1, to_terminal on rule 3 are null), so those terminals are NOT joined --
// only the pinned switch terminals T1 (rule 1 to) and T2 (rule 3 from) are.
func ListTieOptions(ctx context.Context, db bun.IDB) ([]TieOption, error) {
	const query = `
SELECT swc.code AS switch_code, swc.name AS switch_name, condc.code AS feeder_code
FROM metadata.feature_classes swc
JOIN metadata.fields f ON f.feature_class_id = swc.id AND f.name = 'normal_state'
JOIN metadata.domain_values dv ON dv.domain_id = f.domain_id AND dv.code = 'NORMALLY_OPEN'
-- rule 1: condc -> swc.T1
JOIN metadata.network_rules r1 ON r1.to_feature_class_id = swc.id AND r1.is_valid = true
JOIN metadata.terminals r1_to ON r1_to.id = r1.to_terminal_id AND r1_to.code = 'T1' AND r1_to.feature_class_id = swc.id
JOIN metadata.feature_classes condc ON condc.id = r1.from_feature_class_id
WHERE swc.status = 'active' AND condc.status = 'active'
  AND EXISTS (
    -- rule 3: swc.T2 -> condc
    SELECT 1 FROM metadata.network_rules r3
    JOIN metadata.terminals r3_from ON r3_from.id = r3.from_terminal_id AND r3_from.code = 'T2' AND r3_from.feature_class_id = swc.id
    WHERE r3.from_feature_class_id = swc.id AND r3.to_feature_class_id = condc.id AND r3.is_valid = true
  )
GROUP BY swc.code, swc.name, condc.code
ORDER BY switch_name, feeder_code`

	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("tie: listing options: %w", err)
	}
	defer rows.Close()

	// Aggregate the (switch, conductor) rows into one option per switch class,
	// preserving the query's stable order (switch name, then conductor code).
	var options []TieOption
	index := map[string]int{}
	for rows.Next() {
		var switchCode, switchName, feederCode string
		if err := rows.Scan(&switchCode, &switchName, &feederCode); err != nil {
			return nil, fmt.Errorf("tie: scanning option: %w", err)
		}
		i, ok := index[switchCode]
		if !ok {
			i = len(options)
			index[switchCode] = i
			options = append(options, TieOption{
				SwitchFeatureClass:     switchCode,
				SwitchFeatureClassName: switchName,
			})
		}
		options[i].FeederFeatureClasses = append(options[i].FeederFeatureClasses, feederCode)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("tie: iterating options: %w", err)
	}
	return options, nil
}
