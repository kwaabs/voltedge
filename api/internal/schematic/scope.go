package schematic

import (
	"context"
	"strings"

	"github.com/kwaabs/voltedge/api/internal/auth"
	"github.com/kwaabs/voltedge/api/internal/drafts"
	"github.com/kwaabs/voltedge/api/internal/territory"
	"github.com/uptrace/bun"
)

// readScope is the acting request's Phase 58 territory filter plus the Phase 61
// private-draft visibility filter, resolved once and applied to EVERY table read
// the schematic makes -- the same gates the map's tiles apply (tiles.tileScope +
// drafts.VisibleAuthors). Without this a scoped user could read, via the
// schematic, the position of a feature outside their territories (or an
// unpublished draft authored by someone else) that the map itself withholds --
// a Phase 58/61 bypass. An unscoped user (or a session-less internal caller)
// gets no predicate, exactly as on the map.
type readScope struct {
	territoryIDs   []string
	territoryApply bool
	draftAuthors   []string
	draftApply     bool
}

// resolveScope reads the acting request's effective territory + draft filters.
func resolveScope(ctx context.Context, db bun.IDB) (readScope, error) {
	var s readScope

	enforced, err := territory.EnforcementEnabled(ctx, db)
	if err != nil {
		return s, err
	}
	if enforced {
		if user, ok := auth.UserFrom(ctx); ok {
			allowed, unscoped := user.TerritoryFilter()
			if !unscoped {
				if allowed == nil {
					allowed = []string{}
				}
				s.territoryIDs = allowed
				s.territoryApply = true
			}
		}
	}

	authors, apply, err := drafts.VisibleAuthors(ctx, db)
	if err != nil {
		return s, err
	}
	s.draftAuthors = authors
	s.draftApply = apply
	return s, nil
}

// rawClause returns the " AND ..." SQL to append to a bare single-table read,
// pushing its bind args onto args in placeholder order (territory first, then
// drafts), matching how the clause is assembled.
// rawClause builds the scope predicate. hasTerritory says whether the table has a
// territory_id column -- geometry-less feature tables don't (it's added only to
// geometric classes, migration 0011), so the territory filter is skipped for them
// (they aren't spatially scoped anyway). The draft filter still applies -- every
// feature table carries status/published_at/created_by.
func (s readScope) rawClause(args *[]any, hasTerritory bool) string {
	var sb strings.Builder
	if s.territoryApply && hasTerritory {
		if len(s.territoryIDs) == 0 {
			sb.WriteString(" AND 1 = 0") // scoped to nothing: fail closed
		} else {
			ph := make([]string, len(s.territoryIDs))
			for i := range s.territoryIDs {
				ph[i] = "?"
				*args = append(*args, s.territoryIDs[i])
			}
			sb.WriteString(" AND territory_id::text IN (")
			sb.WriteString(strings.Join(ph, ", "))
			sb.WriteString(")")
		}
	}
	if clause, dargs := drafts.WhereClause(s.draftAuthors, s.draftApply, ""); clause != "" {
		sb.WriteString(" AND ")
		sb.WriteString(clause)
		*args = append(*args, dargs...)
	}
	return sb.String()
}

// apply AND-s the same predicates onto a bun query-builder select.
func (s readScope) apply(q *bun.SelectQuery) *bun.SelectQuery {
	if s.territoryApply {
		if len(s.territoryIDs) == 0 {
			q = q.Where("1 = 0")
		} else {
			q = q.Where("territory_id::text IN (?)", bun.In(s.territoryIDs))
		}
	}
	if clause, dargs := drafts.WhereClause(s.draftAuthors, s.draftApply, ""); clause != "" {
		q = q.Where(clause, dargs...)
	}
	return q
}
