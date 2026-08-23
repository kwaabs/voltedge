package crud

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/uptrace/bun"
)

// splitEndpointEpsilon guards against a split point that lands essentially on
// one of the line's own endpoints: ST_LineLocatePoint returns a fraction in
// [0, 1], and a fraction at (or a floating hair away from) 0 or 1 would leave
// one of the two halves degenerate. A split that near an endpoint isn't a split
// at all -- reject it with a clear message rather than writing a zero-length
// line. The threshold is a fraction of the whole line, not an absolute
// distance, which is all we need to keep both halves real two-point lines; the
// S2 UI snaps to the line interior anyway.
const splitEndpointEpsilon = 1e-6

// splitPoint is the map-space point the caller chose to cut the line at, in the
// same EPSG:4326 lng/lat the rest of the API speaks over the wire. Both fields
// are pointers so an omitted coordinate is a clean 400 ("split_point is
// required") rather than a silent (0, 0) off the coast of Africa.
type splitPoint struct {
	Lng *float64 `json:"lng"`
	Lat *float64 `json:"lat"`
}

// splitNewFeature is the create body for the second half -- the same
// subtype/name/attributes/territory a manual create takes, but deliberately no
// geometry: the far half's geometry is computed by PostGIS from the split, not
// supplied by the client. It routes through the shared CreateFeature, so the new
// part validates and assigns a territory exactly like any other create.
type splitNewFeature struct {
	Subtype     *int           `json:"subtype,omitempty"`
	Name        *string        `json:"name,omitempty"`
	TerritoryID *string        `json:"territory_id,omitempty"`
	Attributes  map[string]any `json:"attributes"`
}

// splitRequest is the POST /{code}/{id}/split body: where to cut, and how to
// fill out the new feature the far half becomes.
type splitRequest struct {
	SplitPoint splitPoint      `json:"split_point"`
	NewFeature splitNewFeature `json:"new_feature"`
}

// splitHandler cuts a line feature at a chosen point, shortening the original to
// the near half and creating a new feature from the far half -- both in one DB
// transaction, so a failure leaves the original untouched rather than truncating
// it with no replacement (docs/phase-20-split-copy.md's locked decision). It is
// a write (POST), so the /features mount's method-aware gate already requires
// features:write. Connections and relationships stay on the original; the
// frontend re-runs the assistant suggestion loop on both halves so re-connecting
// the new part is one click.
func splitHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		code := chi.URLParam(r, "code")
		id := chi.URLParam(r, "id")

		loaded, err := loadOrNotFound(r.Context(), db, code)
		if err != nil {
			writeError(w, err)
			return
		}

		// Splitting only makes sense for a line -- there's no "first/second part"
		// of a point or polygon. Reject anything else up front with a clear 400
		// rather than letting ST_LineLocatePoint fail cryptically downstream.
		if loaded.FeatureClass.GeometryType != "line" {
			writeError(w, invalid("feature class %q is not a line, so it can't be split", loaded.FeatureClass.Code))
			return
		}

		var req splitRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalid("request body is not valid JSON: %v", err))
			return
		}
		if req.SplitPoint.Lng == nil || req.SplitPoint.Lat == nil {
			writeError(w, invalid("split_point.lng and split_point.lat are required"))
			return
		}

		result, err := SplitLine(r.Context(), db, loaded, id, req)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, result)
	}
}

// SplitLine performs the whole transactional split for one line feature and
// returns the newly created (far-half) feature's envelope, the same shape a
// create returns. The caller has already resolved metadata and checked the class
// is a line; this owns the transaction, the PostGIS geometry math, and the two
// writes. Errors are this package's own validation/not-found errors.
//
// The two halves are computed with ST_LineSubstring around the fraction
// ST_LineLocatePoint returns for the chosen point -- the near half (0 -> frac)
// replaces the original's geometry, the far half (frac -> 1) becomes the new
// feature. The new feature is inserted through CreateFeature so it runs the exact
// same validation and territory assignment a manual create does; the original's
// geometry is updated in place (its connections, relationships, and territory are
// intentionally left as they were).
func SplitLine(ctx context.Context, db *bun.DB, loaded *metadata.Loaded, id string, req splitRequest) (map[string]any, error) {
	var created map[string]any
	err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		nearHalf, farHalf, err := computeSplitHalves(ctx, tx, loaded, id, *req.SplitPoint.Lng, *req.SplitPoint.Lat)
		if err != nil {
			return err
		}

		if err := updateGeometry(ctx, tx, loaded, id, nearHalf); err != nil {
			return err
		}

		created, err = CreateFeature(ctx, tx, loaded, CreateFeatureInput{
			Subtype:     req.NewFeature.Subtype,
			Name:        req.NewFeature.Name,
			Geometry:    json.RawMessage(farHalf),
			TerritoryID: req.NewFeature.TerritoryID,
			Attributes:  req.NewFeature.Attributes,
		})
		return err
	})
	if err != nil {
		return nil, err
	}
	return created, nil
}

// computeSplitHalves loads the original line inside the transaction, guards that
// it exists, isn't soft-deleted, is a simple LineString, and that the split
// point isn't on an endpoint, then returns the two halves as GeoJSON text ready
// to write back. The geometry type is checked before ST_LineLocatePoint is ever
// evaluated (a separate query), so a MultiLineString gets a clean 400 instead of
// a raw PostGIS error.
func computeSplitHalves(ctx context.Context, tx bun.Tx, loaded *metadata.Loaded, id string, lng, lat float64) (nearHalf, farHalf string, err error) {
	table, err := physicalTable(loaded.FeatureClass)
	if err != nil {
		return "", "", err
	}

	// Step 1: existence, deleted state, and geometry type -- separately, so the
	// LineString guard runs before ST_LineLocatePoint (which errors on a
	// MultiLineString) rather than after it fails.
	var deleted bool
	var geomType string
	// ST_GeometryType (not GeometryType): the ST_-prefixed form returns
	// "ST_LineString" / "ST_MultiLineString", which is what the check below and
	// the error message compare against. Bare GeometryType returns the
	// uppercased OGC name ("LINESTRING") and would reject every valid line.
	guard := fmt.Sprintf("SELECT deleted_at IS NOT NULL, ST_GeometryType(geom) FROM %s WHERE id = ?", table)
	switch err := tx.QueryRowContext(ctx, guard, id).Scan(&deleted, &geomType); {
	case err == sql.ErrNoRows:
		return "", "", notFound("feature %s not found", id)
	case err != nil:
		return "", "", fmt.Errorf("crud: split guard: %w", err)
	}
	if deleted {
		return "", "", invalid("feature %s is deleted -- restore it before splitting", id)
	}
	if geomType != "ST_LineString" {
		return "", "", invalid("feature %s is a %s, not a simple LineString, so it can't be split", id, geomType)
	}

	// Step 2: locate the point along the line, and slice both halves in one shot.
	// The split point is built at the class SRID with no reprojection, mirroring
	// how insertFeature/updateFeature treat client geometry -- geom's SRID is the
	// same coordinate space the wire lng/lat live in.
	srid := *loaded.FeatureClass.SRID
	var frac float64
	var near, far string
	query := fmt.Sprintf(`
WITH src AS (
  SELECT geom, ST_LineLocatePoint(geom, ST_SetSRID(ST_MakePoint(?, ?), %d)) AS frac
  FROM %s WHERE id = ?
)
SELECT frac,
       ST_AsGeoJSON(ST_LineSubstring(geom, 0, frac))::text,
       ST_AsGeoJSON(ST_LineSubstring(geom, frac, 1))::text
FROM src`, srid, table)
	if err := tx.QueryRowContext(ctx, query, lng, lat, id).Scan(&frac, &near, &far); err != nil {
		return "", "", fmt.Errorf("crud: split compute: %w", err)
	}

	if frac <= splitEndpointEpsilon || frac >= 1-splitEndpointEpsilon {
		return "", "", invalid("split point is on (or too close to) an endpoint of feature %s -- choose a point along the line", id)
	}
	return near, far, nil
}

// updateGeometry rewrites just the original's geometry column (plus the
// updated_at/updated_by stamp) inside the split transaction. It deliberately
// does not touch attributes or territory: a split only shortens the line from its
// far end, and the locked decision is to leave everything else on the original as
// it was. Same ST_SetSRID(ST_GeomFromGeoJSON(...)) shape insertFeature/
// updateFeature use.
func updateGeometry(ctx context.Context, tx bun.Tx, loaded *metadata.Loaded, id, geometry string) error {
	table, err := physicalTable(loaded.FeatureClass)
	if err != nil {
		return err
	}
	query := fmt.Sprintf(
		"UPDATE %s SET geom = ST_SetSRID(ST_GeomFromGeoJSON(?), %d), updated_at = now(), updated_by = ? WHERE id = ?",
		table, *loaded.FeatureClass.SRID,
	)
	res, err := tx.ExecContext(ctx, query, geometry, actorArg(ctx), id)
	if err != nil {
		return fmt.Errorf("crud: split update: %w", err)
	}
	// The row was read moments ago in the same transaction, so 0 rows here would
	// mean it vanished mid-transaction -- treat it as not-found rather than
	// silently reporting success for a write that changed nothing.
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return notFound("feature %s not found", id)
	}
	return nil
}
