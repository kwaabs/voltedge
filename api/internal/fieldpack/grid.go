package fieldpack

// Shared-cell GRID helper (S7, docs/shared-field-packs-design.md).
//
// Defining a cell per sub-area of a big territory by hand is tedious. This generates
// a whole grid in one call: an outer bbox split into rows x cols sub-cells, each a
// real ops.field_pack_cells row sharing one template (classes, include flags,
// territory boundary, schedule, retention). The admin then builds them (manually, or
// the scheduler does it overnight). Sizing each sub-cell under FIELDSYNC_MAX_FEATURES
// is the admin's call -- a too-coarse cell whose base would exceed the cap fails the
// build loudly (maxPackFeatures), which is the signal to regrid finer.
//
// The territory boundary (if any) is applied to EVERY sub-cell, so a crew gated on
// the territory can pull any sub-cell, and each sub-cell's base carries only that
// territory's features within its own sub-bbox.

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"

	"github.com/kwaabs/voltedge/api/internal/auth"
	"github.com/uptrace/bun"
)

// maxGridCells caps one grid request so a fat-fingered tiny cell size (or huge bbox)
// can't spawn thousands of cells in a single call.
const maxGridCells = 500

// gridRequest is the POST /field-packs/cells/grid body: the outer bbox + the grid
// dimensions (explicit rows/cols, OR a cell size in degrees from which they're
// derived) + the shared template every sub-cell inherits.
type gridRequest struct {
	NamePrefix  string    `json:"name_prefix"`
	Bbox        []float64 `json:"bbox"`
	Rows        int       `json:"rows"`
	Cols        int       `json:"cols"`
	CellSizeDeg float64   `json:"cell_size_deg"`

	TerritoryID          *string  `json:"territory_id"`
	FeatureClasses       []string `json:"feature_classes"`
	IncludeConnectivity  bool     `json:"include_connectivity"`
	IncludeRelationships bool     `json:"include_relationships"`
	IncludeFaults        bool     `json:"include_faults"`
	IncludeTags          bool     `json:"include_tags"`
	Schedule             *string  `json:"schedule"`
	Retention            string   `json:"retention"`
	Active               *bool    `json:"active"`
}

// resolveDims returns the grid's (rows, cols), deriving them from CellSizeDeg when
// explicit rows/cols weren't given, or an error message when the request is unusable.
func (req gridRequest) resolveDims() (rows, cols int, msg string) {
	rows, cols = req.Rows, req.Cols
	if (rows <= 0 || cols <= 0) && req.CellSizeDeg > 0 {
		width := req.Bbox[2] - req.Bbox[0]
		height := req.Bbox[3] - req.Bbox[1]
		cols = int(math.Ceil(width / req.CellSizeDeg))
		rows = int(math.Ceil(height / req.CellSizeDeg))
	}
	if rows <= 0 || cols <= 0 {
		return 0, 0, "provide rows and cols (both >= 1), or a positive cell_size_deg"
	}
	if rows*cols > maxGridCells {
		return 0, 0, fmt.Sprintf("grid would be %d cells (max %d) -- use fewer rows/cols or a larger cell_size_deg", rows*cols, maxGridCells)
	}
	return rows, cols, ""
}

func (req gridRequest) validate() string {
	if req.NamePrefix == "" {
		return "name_prefix is required"
	}
	if len(req.Bbox) != 4 {
		return "bbox must be [minX, minY, maxX, maxY] (4 numbers)"
	}
	if req.Bbox[2] <= req.Bbox[0] || req.Bbox[3] <= req.Bbox[1] {
		return "bbox must have maxX > minX and maxY > minY"
	}
	if len(req.FeatureClasses) == 0 {
		return "at least one feature class is required"
	}
	return ""
}

// cellInputs expands the grid into one CellInput per sub-cell, row 1 at the bottom
// (minY). Each sub-cell gets an even slice of the outer bbox and the shared template.
func (req gridRequest) cellInputs(rows, cols int) []CellInput {
	minX, minY, maxX, maxY := req.Bbox[0], req.Bbox[1], req.Bbox[2], req.Bbox[3]
	cellW := (maxX - minX) / float64(cols)
	cellH := (maxY - minY) / float64(rows)
	active := true
	if req.Active != nil {
		active = *req.Active
	}
	out := make([]CellInput, 0, rows*cols)
	for r := 0; r < rows; r++ {
		for c := 0; c < cols; c++ {
			cMinX := minX + float64(c)*cellW
			cMinY := minY + float64(r)*cellH
			out = append(out, CellInput{
				Name: fmt.Sprintf("%s r%dc%d", req.NamePrefix, r+1, c+1),
				// The last row/col extends exactly to the outer max, avoiding float drift
				// leaving a sliver uncovered at the far edge.
				Bbox: []float64{
					cMinX, cMinY,
					lastEdge(c, cols, cMinX+cellW, maxX),
					lastEdge(r, rows, cMinY+cellH, maxY),
				},
				TerritoryID:          req.TerritoryID,
				FeatureClasses:       req.FeatureClasses,
				IncludeConnectivity:  req.IncludeConnectivity,
				IncludeRelationships: req.IncludeRelationships,
				IncludeFaults:        req.IncludeFaults,
				IncludeTags:          req.IncludeTags,
				Schedule:             req.Schedule,
				Retention:            req.Retention,
				Active:               active,
			})
		}
	}
	return out
}

// lastEdge snaps the final row/column's far edge to the exact outer max, so float
// accumulation never leaves a gap at the grid's top/right.
func lastEdge(i, count int, computed, outerMax float64) float64 {
	if i == count-1 {
		return outerMax
	}
	return computed
}

// adminCreateGridHandler creates the whole grid atomically: either every sub-cell is
// inserted or none is (one bad insert rolls the lot back, so the admin never ends up
// with a half-made grid). Returns the created cells.
func adminCreateGridHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := auth.UserFrom(r.Context())
		if !ok {
			writeErr(w, http.StatusUnauthorized, "no session")
			return
		}
		var req gridRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, "request body is not valid JSON")
			return
		}
		if msg := req.validate(); msg != "" {
			writeErr(w, http.StatusBadRequest, msg)
			return
		}
		rows, cols, msg := req.resolveDims()
		if msg != "" {
			writeErr(w, http.StatusBadRequest, msg)
			return
		}

		inputs := req.cellInputs(rows, cols)
		created := make([]cellView, 0, len(inputs))
		err := db.RunInTx(r.Context(), nil, func(ctx context.Context, tx bun.Tx) error {
			for _, in := range inputs {
				cell, err := createCell(ctx, tx, in, user.ID)
				if err != nil {
					return err
				}
				created = append(created, toCellView(cell, nil))
			}
			return nil
		})
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{
			"rows":  rows,
			"cols":  cols,
			"cells": created,
		})
	}
}
