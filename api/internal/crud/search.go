package crud

// SearchFeatures is the exported, read-only entry point another package
// needs to browse a feature class's rows -- specifically the Phase 18 MCP
// server's search_features tool (docs/phase-18-mcp.md), which has no way to
// reach the unexported listFeatures the HTTP list handler uses. It is a
// thin wrapper: the same soft-delete-excluding, name/status-filtered,
// sorted, paged read the /features/{code} GET endpoint already performs,
// exposed without duplicating any query logic.

import (
	"context"

	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/uptrace/bun"
)

// SearchOptions is the public mirror of listOptions -- the filters a caller
// can apply to a feature-class search. All fields are optional; the zero
// value returns the first page of every non-deleted feature, oldest first.
// A Limit of 0 is caller-corrected to a sane default so a search never
// accidentally streams an entire class.
type SearchOptions struct {
	// Limit caps the page size; Offset pages through results.
	Limit  int
	Offset int
	// NameQuery is a case-insensitive partial match against the feature's
	// name ("" = no name filter).
	NameQuery string
	// Status filters to a single status system column value, "draft" or
	// "published" ("" = both).
	Status string
	// Sort names a column to order by (a system column or one of this
	// class's fields); an unknown column falls back to created_at. Desc
	// flips the direction; false = ascending.
	Sort string
	Desc bool
}

// SearchFeatures reads a bounded, filtered page of a feature class's
// features (soft-deleted rows excluded), each in the same envelope shape a
// GET returns (top-level id/name/status/geometry/... plus a nested
// attributes map). loaded comes from metadata.Load(code). The caller is
// responsible for authorization (features:read) before calling.
func SearchFeatures(ctx context.Context, db bun.IDB, loaded *metadata.Loaded, opts SearchOptions) ([]map[string]any, error) {
	return listFeatures(ctx, db, loaded, listOptions{
		Limit:     opts.Limit,
		Offset:    opts.Offset,
		NameQuery: opts.NameQuery,
		Status:    opts.Status,
		Sort:      opts.Sort,
		Desc:      opts.Desc,
	})
}
