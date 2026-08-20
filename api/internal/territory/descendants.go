package territory

import (
	"context"
	"fmt"
	"strings"

	"github.com/uptrace/bun"
)

// DescendantIDs returns the given territory ids plus every territory beneath
// them in the tree (their whole subtrees), deduplicated. It's the inverse
// direction of wouldCreateCycle's ancestor walk: this recurses downward
// (joining a child's parent_id back to the working set) instead of up.
//
// The geographic export uses it to make a "features in these territories"
// filter correct. A feature is stamped with the DEEPEST territory that
// contains it (see resolve.go), so a feature physically inside a district may
// carry the id of a drawn sub-area or plot; matching only the district id
// would silently drop it. Expanding each selected territory to its subtree and
// matching territory_id against the whole set restores the intuitive
// "everything within this territory".
//
// Ids that don't exist simply contribute nothing (the recursive base case
// selects only rows that are present), so an all-unknown input yields an empty
// slice -- which the caller treats as "match nothing", not "no filter".
func DescendantIDs(ctx context.Context, db bun.IDB, ids []string) ([]string, error) {
	if len(ids) == 0 {
		return nil, nil
	}

	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}
	query := fmt.Sprintf(`
WITH RECURSIVE subtree AS (
    SELECT id FROM gis.territories WHERE id IN (%s)
    UNION
    SELECT t.id FROM gis.territories t
    JOIN subtree s ON t.parent_id = s.id
)
SELECT id::text FROM subtree`, strings.Join(placeholders, ", "))

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("territory: expanding descendants: %w", err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("territory: scanning descendant id: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
