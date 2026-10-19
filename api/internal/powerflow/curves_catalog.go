package powerflow

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/uptrace/bun"
)

// The protection-curve CATALOG + its admin editor. Two concerns in one file:
//
//   - READ (features:read): a listing of the stored time-current curves
//     (metadata.protection_curves) so the feature form can offer a fuse's
//     curve_ref / a relay's curve as a picked value instead of a free-text code
//     that has to match a curve's code exactly. The coordination engine still
//     joins a device to its points by that code string (curves.go).
//   - WRITE (metadata:write): admin CRUD over the curves and their (band,
//     current, time) points, so an operator loads a manufacturer fuse/relay
//     curve through the UI instead of hand-writing a seed SQL file. This is the
//     only way to grow the catalog at run time.
//
// Mounted at /curves with a METHOD-AWARE gate (api/cmd/server/main.go):
// features:read for GET, metadata:write for the mutating verbs -- so the form
// (edited by feature authors who may not hold analysis:run or metadata:write)
// can still read the catalog, while only an admin can change it.
//
// The curve `code` is IMMUTABLE after create: features reference a curve by its
// code (curve_ref / curve_type), and the coordination engine joins on it, so
// renaming the code would orphan every device pointing at it. Edit the name
// (the display label) freely; to change a code, delete and recreate.

// CurveCatalogItem is one curve's header row (metadata.protection_curves): the
// code the engine joins on, a human name, the issuing standard, and whether the
// curve is indexed by a multiple of pickup (a relay curve) or absolute amperes
// (a fuse curve).
type CurveCatalogItem struct {
	bun.BaseModel `bun:"table:metadata.protection_curves,alias:c" json:"-"`

	Code     string `bun:"code" json:"code"`
	Name     string `bun:"name" json:"name"`
	Standard string `bun:"standard" json:"standard"`
	Quantity string `bun:"quantity" json:"quantity"`
}

// curvePointDTO is one (band, current, time) sample on the wire. current is
// absolute amperes for a fuse curve (quantity=ampere) or a multiple of pickup
// for a relay curve (quantity=multiple); time_s is seconds.
type curvePointDTO struct {
	Band    string  `json:"band"`
	Current float64 `json:"current"`
	TimeS   float64 `json:"time_s"`
}

// curveDetail is a curve header plus its full point set -- what the editor loads
// for one curve (GET /curves/{code}).
type curveDetail struct {
	CurveCatalogItem
	Points []curvePointDTO `json:"points"`
}

// curvePointInsert is the insert shape for metadata.protection_curve_points.
type curvePointInsert struct {
	bun.BaseModel `bun:"table:metadata.protection_curve_points"`

	CurveID string  `bun:"curve_id"`
	Band    string  `bun:"band"`
	Current float64 `bun:"current"`
	TimeS   float64 `bun:"time_s"`
}

// curveHeaderInsert is the insert shape for metadata.protection_curves (the id /
// created_at columns default server-side). id is Returning-scanned back.
type curveHeaderInsert struct {
	bun.BaseModel `bun:"table:metadata.protection_curves"`

	ID       string `bun:"id,scanonly"`
	Code     string `bun:"code"`
	Name     string `bun:"name"`
	Standard string `bun:"standard"`
	Quantity string `bun:"quantity"`
}

const (
	quantityMultiple = "multiple"
	quantityAmpere   = "ampere"
)

var validBands = map[string]bool{bandOperate: true, bandMinMelt: true, bandTotalClear: true}

// CatalogRoutes returns a router for the protection-curve catalog + editor,
// meant to be mounted at /curves:
//
//	GET    /curves               every stored curve (header only)
//	GET    /curves?quantity=ampere   only the ampere (fuse) curves; =multiple for relay
//	GET    /curves/{code}        one curve's header + its full point set
//	POST   /curves               create a curve (optionally with its points)
//	PATCH  /curves/{code}        update a curve's header (name/standard/quantity)
//	DELETE /curves/{code}        delete a curve (its points cascade)
//	PUT    /curves/{code}/points replace a curve's entire point set
//
// GET is features:read; every write is metadata:write (the mount gate is
// method-aware). The curve code is immutable (see file header).
func CatalogRoutes(db *bun.DB) chi.Router {
	r := chi.NewRouter()
	r.Get("/", listCurvesHandler(db))
	r.Post("/", createCurveHandler(db))
	r.Get("/{code}", getCurveHandler(db))
	r.Patch("/{code}", updateCurveHandler(db))
	r.Delete("/{code}", deleteCurveHandler(db))
	r.Put("/{code}/points", replacePointsHandler(db))
	return r
}

func listCurvesHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		quantity := r.URL.Query().Get("quantity")
		items, err := listCurves(r.Context(), db, quantity)
		if err != nil {
			writeError(w, err)
			return
		}
		// Always a JSON array (never null) so the client can map over it directly.
		writeJSON(w, http.StatusOK, map[string]any{"curves": items})
	}
}

// listCurves reads the curve catalog, optionally filtered to one quantity, in a
// stable display order (standard then name). An empty result is not an error --
// a deployment that hasn't loaded any curves yet simply has none to offer.
func listCurves(ctx context.Context, db bun.IDB, quantity string) ([]CurveCatalogItem, error) {
	items := []CurveCatalogItem{}
	q := db.NewSelect().
		Model(&items).
		Column("code", "name", "standard", "quantity").
		OrderExpr("c.standard ASC, c.name ASC")
	if quantity != "" {
		q = q.Where("c.quantity = ?", quantity)
	}
	if err := q.Scan(ctx); err != nil {
		return nil, err
	}
	return items, nil
}

func getCurveHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		code := chi.URLParam(r, "code")
		detail, err := getCurve(r.Context(), db, code)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, detail)
	}
}

// getCurve loads one curve's header + its points, ordered by band then current
// so the editor renders a tidy table. A missing code is a 404.
func getCurve(ctx context.Context, db bun.IDB, code string) (*curveDetail, error) {
	var header CurveCatalogItem
	err := db.NewSelect().Model(&header).
		Column("code", "name", "standard", "quantity").
		Where("c.code = ?", code).
		Scan(ctx)
	if err != nil {
		return nil, notFound("no protection curve with code %q", code)
	}

	var rows []struct {
		Band    string  `bun:"band"`
		Current float64 `bun:"current"`
		TimeS   float64 `bun:"time_s"`
	}
	if err := db.NewSelect().
		TableExpr("metadata.protection_curve_points AS p").
		ColumnExpr("p.band, p.current, p.time_s").
		Join("JOIN metadata.protection_curves c ON c.id = p.curve_id").
		Where("c.code = ?", code).
		OrderExpr("p.band ASC, p.current ASC").
		Scan(ctx, &rows); err != nil {
		return nil, err
	}

	points := make([]curvePointDTO, 0, len(rows))
	for _, r := range rows {
		points = append(points, curvePointDTO{Band: r.Band, Current: r.Current, TimeS: r.TimeS})
	}
	return &curveDetail{CurveCatalogItem: header, Points: points}, nil
}

type createCurveRequest struct {
	Code     string          `json:"code"`
	Name     string          `json:"name"`
	Standard string          `json:"standard"`
	Quantity string          `json:"quantity"`
	Points   []curvePointDTO `json:"points"`
}

func createCurveHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req createCurveRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalid("request body is not valid JSON: %v", err))
			return
		}
		detail, err := createCurve(r.Context(), db, req)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, detail)
	}
}

func createCurve(ctx context.Context, db *bun.DB, req createCurveRequest) (*curveDetail, error) {
	req.Code = strings.TrimSpace(req.Code)
	req.Name = strings.TrimSpace(req.Name)
	req.Standard = strings.TrimSpace(req.Standard)
	if req.Code == "" {
		return nil, invalid("code is required")
	}
	if req.Name == "" {
		return nil, invalid("name is required")
	}
	if req.Standard == "" {
		return nil, invalid("standard is required")
	}
	if req.Quantity != quantityMultiple && req.Quantity != quantityAmpere {
		return nil, invalid("quantity must be %q or %q", quantityMultiple, quantityAmpere)
	}
	if err := validatePoints(req.Points); err != nil {
		return nil, err
	}

	// Reject a duplicate code with a clear message rather than leaking the DB's
	// unique-violation string (the UNIQUE constraint still backstops a race).
	exists, err := db.NewSelect().Table("metadata.protection_curves").
		Where("code = ?", req.Code).Exists(ctx)
	if err != nil {
		return nil, err
	}
	if exists {
		return nil, invalid("a protection curve with code %q already exists", req.Code)
	}

	err = db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		header := curveHeaderInsert{Code: req.Code, Name: req.Name, Standard: req.Standard, Quantity: req.Quantity}
		if _, err := tx.NewInsert().Model(&header).Returning("id").Exec(ctx); err != nil {
			return err
		}
		return insertPoints(ctx, tx, header.ID, req.Points)
	})
	if err != nil {
		return nil, err
	}
	return getCurve(ctx, db, req.Code)
}

type updateCurveRequest struct {
	Name     string `json:"name"`
	Standard string `json:"standard"`
	Quantity string `json:"quantity"`
}

func updateCurveHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		code := chi.URLParam(r, "code")
		var req updateCurveRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalid("request body is not valid JSON: %v", err))
			return
		}
		detail, err := updateCurve(r.Context(), db, code, req)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, detail)
	}
}

// updateCurve edits a curve's header fields. The code is NOT updatable (features
// reference it) -- only name, standard and quantity. Note changing quantity
// reinterprets the existing points' current axis (multiple vs ampere), so the UI
// should warn; the data isn't rewritten, only its meaning.
func updateCurve(ctx context.Context, db *bun.DB, code string, req updateCurveRequest) (*curveDetail, error) {
	req.Name = strings.TrimSpace(req.Name)
	req.Standard = strings.TrimSpace(req.Standard)
	if req.Name == "" {
		return nil, invalid("name is required")
	}
	if req.Standard == "" {
		return nil, invalid("standard is required")
	}
	if req.Quantity != quantityMultiple && req.Quantity != quantityAmpere {
		return nil, invalid("quantity must be %q or %q", quantityMultiple, quantityAmpere)
	}

	res, err := db.NewUpdate().
		TableExpr("metadata.protection_curves").
		Set("name = ?", req.Name).
		Set("standard = ?", req.Standard).
		Set("quantity = ?", req.Quantity).
		Where("code = ?", code).
		Exec(ctx)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, notFound("no protection curve with code %q", code)
	}
	return getCurve(ctx, db, code)
}

func deleteCurveHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		code := chi.URLParam(r, "code")
		res, err := db.NewDelete().
			TableExpr("metadata.protection_curves").
			Where("code = ?", code).
			Exec(r.Context())
		if err != nil {
			writeError(w, err)
			return
		}
		if n, _ := res.RowsAffected(); n == 0 {
			writeError(w, notFound("no protection curve with code %q", code))
			return
		}
		// Points cascade (FK ON DELETE CASCADE, migration 0076).
		writeJSON(w, http.StatusOK, map[string]any{"deleted": code})
	}
}

type replacePointsRequest struct {
	Points []curvePointDTO `json:"points"`
}

func replacePointsHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		code := chi.URLParam(r, "code")
		var req replacePointsRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalid("request body is not valid JSON: %v", err))
			return
		}
		if err := validatePoints(req.Points); err != nil {
			writeError(w, err)
			return
		}
		detail, err := replacePoints(r.Context(), db, code, req.Points)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, detail)
	}
}

// replacePoints swaps a curve's entire point set atomically: resolve the curve,
// delete its points, insert the new set. "Replace all" fits the editor, which
// submits the whole table, and sidesteps per-point diffing.
func replacePoints(ctx context.Context, db *bun.DB, code string, points []curvePointDTO) (*curveDetail, error) {
	var curveID string
	if err := db.NewSelect().
		TableExpr("metadata.protection_curves").
		Column("id").
		Where("code = ?", code).
		Scan(ctx, &curveID); err != nil {
		return nil, notFound("no protection curve with code %q", code)
	}

	err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.NewDelete().
			TableExpr("metadata.protection_curve_points").
			Where("curve_id = ?", curveID).
			Exec(ctx); err != nil {
			return err
		}
		return insertPoints(ctx, tx, curveID, points)
	})
	if err != nil {
		return nil, err
	}
	return getCurve(ctx, db, code)
}

// insertPoints bulk-inserts a point set for a curve. An empty set is a no-op (a
// curve may legitimately have no points yet while it's being built).
func insertPoints(ctx context.Context, tx bun.Tx, curveID string, points []curvePointDTO) error {
	if len(points) == 0 {
		return nil
	}
	rows := make([]curvePointInsert, 0, len(points))
	for _, p := range points {
		rows = append(rows, curvePointInsert{CurveID: curveID, Band: p.Band, Current: p.Current, TimeS: p.TimeS})
	}
	_, err := tx.NewInsert().Model(&rows).Exec(ctx)
	return err
}

// validatePoints enforces the same shape the table's CHECKs do (band in the
// allowed set, current > 0, time_s >= 0) plus the (band, current) uniqueness the
// composite key requires -- caught here with a clear message rather than a raw
// constraint-violation string.
func validatePoints(points []curvePointDTO) error {
	seen := map[string]bool{}
	for i, p := range points {
		if !validBands[p.Band] {
			return invalid("point %d: band must be one of operate, minimum_melt, total_clear (got %q)", i+1, p.Band)
		}
		if p.Current <= 0 {
			return invalid("point %d: current must be greater than 0", i+1)
		}
		if p.TimeS < 0 {
			return invalid("point %d: time_s must be 0 or greater", i+1)
		}
		// Key by the current's numeric value (not its text) so 100 and 100.0 collide
		// the way the (band, current) unique constraint would.
		key := p.Band + "|" + strconv.FormatFloat(p.Current, 'f', 6, 64)
		if seen[key] {
			return invalid("duplicate point: band %q already has a value at current %g", p.Band, p.Current)
		}
		seen[key] = true
	}
	return nil
}
