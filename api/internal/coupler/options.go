package coupler

import (
	"context"
	"fmt"

	"github.com/uptrace/bun"
)

// CouplerOption is one addable coupler configuration for the map's add-feature
// menu: a breaker class that can act as a coupler (it carries the switch_function
// field with a BUS_COUPLER value, from Slice 1's field-pack) together with the
// bus classes it may bridge. BreakerFeatureClass is exactly what the frontend
// sends back to POST /couplers (as breaker_feature_class); BusFeatureClasses
// tells the frontend which classes to list features from for the two bus picks.
// Splitting responsibility this way keeps this package from listing feature
// instances itself -- it names the eligible classes; the frontend reuses the
// existing feature-list surface to fill the pickers.
type CouplerOption struct {
	BreakerFeatureClass     string   `json:"breaker_feature_class"`
	BreakerFeatureClassName string   `json:"breaker_feature_class_name"`
	BusFeatureClasses       []string `json:"bus_feature_classes"`
}

// ListCouplerOptions returns every active feature class that can be placed as a
// bus coupler: one that (a) carries a switch_function field whose domain offers
// the BUS_COUPLER value -- i.e. Slice 1's field-pack is applied -- AND (b) is
// authorized by BOTH busbar rule C (`bus.T2 -> breaker.T1`) and rule D
// (`breaker.T2 -> bus.T1`) to sit between some bus class, since without both a
// coupler cannot actually be wired. For each such breaker class it lists the bus
// classes satisfying both rules. The result is empty until the field-pack seed +
// the busbar wiring are present, so the "Bus coupler" affordance simply doesn't
// appear until the catalog supports it -- and this package never names a class
// code. Classes are only offered once compiled (status = 'active').
func ListCouplerOptions(ctx context.Context, db bun.IDB) ([]CouplerOption, error) {
	const query = `
SELECT bfc.code AS breaker_code, bfc.name AS breaker_name, busc.code AS bus_code
FROM metadata.feature_classes bfc
JOIN metadata.fields f ON f.feature_class_id = bfc.id AND f.name = 'switch_function'
JOIN metadata.domain_values dv ON dv.domain_id = f.domain_id AND dv.code = 'BUS_COUPLER'
-- rule C: busc.T2 -> bfc.T1
JOIN metadata.network_rules rc ON rc.to_feature_class_id = bfc.id AND rc.is_valid = true
JOIN metadata.terminals rc_to ON rc_to.id = rc.to_terminal_id AND rc_to.code = 'T1' AND rc_to.feature_class_id = bfc.id
JOIN metadata.feature_classes busc ON busc.id = rc.from_feature_class_id
JOIN metadata.terminals rc_from ON rc_from.id = rc.from_terminal_id AND rc_from.code = 'T2' AND rc_from.feature_class_id = busc.id
WHERE bfc.status = 'active' AND busc.status = 'active'
  AND EXISTS (
    -- rule D: bfc.T2 -> busc.T1
    SELECT 1 FROM metadata.network_rules rd
    JOIN metadata.terminals rd_from ON rd_from.id = rd.from_terminal_id AND rd_from.code = 'T2' AND rd_from.feature_class_id = bfc.id
    JOIN metadata.terminals rd_to ON rd_to.id = rd.to_terminal_id AND rd_to.code = 'T1' AND rd_to.feature_class_id = busc.id
    WHERE rd.from_feature_class_id = bfc.id AND rd.to_feature_class_id = busc.id AND rd.is_valid = true
  )
GROUP BY bfc.code, bfc.name, busc.code
ORDER BY breaker_name, bus_code`

	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("coupler: listing options: %w", err)
	}
	defer rows.Close()

	// Aggregate the (breaker, bus) rows into one option per breaker class,
	// preserving the query's stable order (breaker name, then bus code).
	var options []CouplerOption
	index := map[string]int{}
	for rows.Next() {
		var breakerCode, breakerName, busCode string
		if err := rows.Scan(&breakerCode, &breakerName, &busCode); err != nil {
			return nil, fmt.Errorf("coupler: scanning option: %w", err)
		}
		i, ok := index[breakerCode]
		if !ok {
			i = len(options)
			index[breakerCode] = i
			options = append(options, CouplerOption{
				BreakerFeatureClass:     breakerCode,
				BreakerFeatureClassName: breakerName,
			})
		}
		options[i].BusFeatureClasses = append(options[i].BusFeatureClasses, busCode)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("coupler: iterating options: %w", err)
	}
	return options, nil
}
