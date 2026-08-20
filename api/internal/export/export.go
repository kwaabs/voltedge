package export

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/kwaabs/voltedge/api/internal/crud"
	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/kwaabs/voltedge/api/internal/territory"
	"github.com/uptrace/bun"
)

// exportRequest is the POST /export body. FeatureClasses are class codes (the
// same codes the CRUD endpoints key on). Scope is expressed by two optional,
// independent filters: Bbox is a [minX, minY, maxX, maxY] in EPSG:4326 that
// clips to the map's current viewport, and Territories is a set of territory
// ids that clips to those territories (and, expanded server-side, everything
// beneath them). With neither, the export is the whole dataset. The client
// sends at most one today, but both compose as an AND if given.
type exportRequest struct {
	Format         string    `json:"format"`
	FeatureClasses []string  `json:"feature_classes"`
	Bbox           []float64 `json:"bbox"`
	Territories    []string  `json:"territories"`
}

// format describes one supported output: its MIME type, file extension, and
// the encoder that streams it. Adding a format (Slice 2 shapefile, Slice 3
// DXF) is a new entry here plus its encoder file.
type format struct {
	contentType string
	ext         string
	write       func(w io.Writer, docName string, layers []Layer) error
}

var formats = map[string]format{
	"kmz": {contentType: "application/vnd.google-earth.kmz", ext: "kmz", write: writeKMZ},
	"shp": {contentType: "application/zip", ext: "zip", write: writeShapefiles},
	"dxf": {contentType: "application/dxf", ext: "dxf", write: writeDXF},
}

// Routes returns the export router, meant to be mounted at /export behind the
// features:read gate -- an export is a read, like trace/analysis, even though
// it's a POST (the filter/scope don't fit query params).
func Routes(db *bun.DB) chi.Router {
	r := chi.NewRouter()
	r.Post("/", exportHandler(db))
	return r
}

func exportHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req exportRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, fmt.Sprintf("request body is not valid JSON: %v", err))
			return
		}

		fmtSpec, ok := formats[req.Format]
		if !ok {
			writeErr(w, http.StatusBadRequest, fmt.Sprintf("unsupported export format %q", req.Format))
			return
		}
		if len(req.FeatureClasses) == 0 {
			writeErr(w, http.StatusBadRequest, "no feature classes selected for export")
			return
		}
		bbox, err := parseBBox(req.Bbox)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}

		// Expand the selected territories to their subtrees so the filter
		// matches features stamped to any descendant (see
		// territory.DescendantIDs). A nil result (no territories requested)
		// leaves the export unfiltered by territory; a non-nil but empty result
		// (requested ids that don't exist) filters to nothing.
		territoryIDs, err := resolveTerritories(r.Context(), db, req.Territories)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}

		// Read every class fully before writing a byte of the response: a read
		// failure mid-stream can't change an already-sent status/headers, so
		// all the fallible work happens first and the file is only streamed
		// once every layer is in hand.
		layers, status, msg := gatherLayers(r.Context(), db, req.FeatureClasses, bbox, territoryIDs)
		if status != 0 {
			writeErr(w, status, msg)
			return
		}

		filename := "voltedge-export." + fmtSpec.ext
		w.Header().Set("Content-Type", fmtSpec.contentType)
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename=%q`, filename))
		if err := fmtSpec.write(w, "VoltEdge export", layers); err != nil {
			// Headers are already sent, so this can't become a clean JSON error
			// -- log-worthy, but the connection just ends with a truncated file.
			return
		}
	}
}

// resolveTerritories turns the requested territory ids into the expanded
// descendant set the read path filters on. No requested territories yields a
// nil slice (no territory filter); requested-but-unresolvable ids yield a
// non-nil empty slice (filter to nothing), never a silent fall-back to the
// whole dataset.
func resolveTerritories(ctx context.Context, db *bun.DB, ids []string) ([]string, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	expanded, err := territory.DescendantIDs(ctx, db, ids)
	if err != nil {
		return nil, err
	}
	if expanded == nil {
		expanded = []string{}
	}
	return expanded, nil
}

// gatherLayers loads and reads every requested class. On success it returns
// the layers and a zero status; on failure it returns a nil slice plus the
// HTTP status and message the handler should send (before any file bytes).
func gatherLayers(ctx context.Context, db *bun.DB, codes []string, bbox *crud.ExportBBox, territoryIDs []string) ([]Layer, int, string) {
	layers := make([]Layer, 0, len(codes))
	for _, code := range codes {
		loaded, err := metadata.Load(ctx, db, code)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, http.StatusNotFound, fmt.Sprintf("feature class %q does not exist", code)
			}
			return nil, http.StatusInternalServerError, err.Error()
		}
		if loaded.FeatureClass.GeometryType == "none" {
			return nil, http.StatusBadRequest, fmt.Sprintf("feature class %q has no geometry, so it can't be exported", code)
		}

		rows, err := crud.ExportFeatures(ctx, db, loaded, bbox, territoryIDs)
		if err != nil {
			if crud.IsNotFoundError(err) {
				return nil, http.StatusNotFound, err.Error()
			}
			return nil, http.StatusInternalServerError, err.Error()
		}

		features := make([]Feature, 0, len(rows))
		for _, row := range rows {
			f, err := featureFrom(row)
			if err != nil {
				return nil, http.StatusInternalServerError, err.Error()
			}
			features = append(features, f)
		}

		fields := make([]AttributeField, len(loaded.Fields))
		for i, f := range loaded.Fields {
			fields[i] = AttributeField{Name: f.Name, DataType: f.DataType}
		}
		layers = append(layers, Layer{
			Code:         loaded.FeatureClass.Code,
			Name:         loaded.FeatureClass.Name,
			GeometryType: loaded.FeatureClass.GeometryType,
			Fields:       fields,
			Features:     features,
		})
	}
	return layers, 0, ""
}

// parseBBox validates the optional bbox array. Empty/absent yields nil (a
// whole-dataset export). A present bbox must have exactly four numbers; the
// min/max pairs are normalized so a client that sends them swapped still
// produces a non-empty envelope.
func parseBBox(b []float64) (*crud.ExportBBox, error) {
	if len(b) == 0 {
		return nil, nil
	}
	if len(b) != 4 {
		return nil, fmt.Errorf("bbox must be [minX, minY, maxX, maxY] (4 numbers), got %d", len(b))
	}
	for _, v := range b {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return nil, fmt.Errorf("bbox contains a non-finite number")
		}
	}
	return &crud.ExportBBox{
		MinX: math.Min(b[0], b[2]),
		MinY: math.Min(b[1], b[3]),
		MaxX: math.Max(b[0], b[2]),
		MaxY: math.Max(b[1], b[3]),
	}, nil
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
