package diagram

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/kwaabs/voltedge/api/internal/auth"
	"github.com/kwaabs/voltedge/api/internal/connectivity"
	"github.com/kwaabs/voltedge/api/internal/crud"
	"github.com/uptrace/bun"
)

// This file is Phase 29 Slice 3 (docs/phase-29-point-on-line-split.md): the
// diagram counterpart of the map line-split. A busbar drawn on a one-line
// diagram is a *point* feature (MV_BUSBAR is geometry_type = 'point') that draws
// as a bar because it sets diagram_geometry_type = 'line' and carries a hand-
// drawn `path` polyline on its placement (migration 0033/0053). Splitting it is
// therefore nothing like the map /split endpoint -- there is no PostGIS line to
// ST_LineSubstring and a diagram click has no lng/lat -- so this is its own
// operation: *sectionalize* the bar in the diagram's own local x/y space into
// two independent busbar features, and divide the original bus's connectivity
// between the two halves by where each connection sits along the bar.
//
// The whole thing is one transaction (mirroring crud.SplitLine): cut the drawn
// path, shorten the original placement to the near half, mint a second MV_BUSBAR
// feature for the far half (geometry-optional, so it needs no map geometry --
// see the "inherit" decision below), give it its own placement carrying the far-
// half path, re-home the far-side connections onto it, and optionally wire the
// placed sectionalizer to both halves. A failure rolls the lot back rather than
// leaving a half-cut bus.

// sectionalizeEndpointEpsilon rejects a cut that lands essentially on one end of
// the bar -- the same guard, in fraction-of-length terms, that split.go's
// splitEndpointEpsilon enforces for a map line. A cut at an endpoint isn't a
// sectionalize, it's a no-op that would leave one half a zero-length bar.
const sectionalizeEndpointEpsilon = 1e-6

// sectionalizeOffsetMeters is how far (in EPSG:3857 metres) the far-half bus's
// inherited map point is nudged from the original's, so two coincident points
// don't stack invisibly on the geographic map. Only used when the original bus
// actually has map geometry (the "inherit" decision); a diagram-only bus mints a
// diagram-only far half with no map point at all. A few metres of visual
// separation, dragged into place later like a Phase 20 duplicate.
const sectionalizeOffsetMeters = 5.0

// diagramPoint is the cut location in the diagram's own local coordinate space
// (the same x/y a placement's path is drawn in), not lng/lat. Pointers so an
// omitted coordinate is a clean 400 rather than a silent (0, 0).
type diagramPoint struct {
	X *float64 `json:"x"`
	Y *float64 `json:"y"`
}

// sectionalizeNewFeature is how the far-half busbar feature is filled out -- the
// same subtype/name/attributes a manual create takes, and deliberately no
// geometry: whether the far half gets a map point is decided by the original
// (inherited, offset-copied), never supplied by the client. It routes through
// the shared crud.CreateFeature so the new bus validates exactly like any create.
type sectionalizeNewFeature struct {
	Subtype    *int           `json:"subtype,omitempty"`
	Name       *string        `json:"name,omitempty"`
	Attributes map[string]any `json:"attributes"`
}

// sectionalizerRef is the optional point feature the user placed on the bar (a
// bus-tie / sectionalizer switch) to be wired to both halves once the bar is
// cut. NearTerminal/FarTerminal name the sectionalizer's own terminals facing
// the original (near) and new (far) bus respectively -- pinning them avoids the
// ambiguity of both halves landing on the same switch terminal. Absent terminals
// fall back to rule-driven resolution, best-effort with a warning.
type sectionalizerRef struct {
	FeatureClass string  `json:"feature_class"`
	FeatureID    string  `json:"feature_id"`
	NearTerminal *string `json:"near_terminal,omitempty"`
	FarTerminal  *string `json:"far_terminal,omitempty"`
}

// sectionalizeRequest is the POST /diagrams/{id}/placements/{placementID}/
// sectionalize body: where to cut the bar, how to fill out the new far-half bus,
// and (optionally) the sectionalizer to bridge the two halves.
type sectionalizeRequest struct {
	At            diagramPoint           `json:"at"`
	NewBus        sectionalizeNewFeature `json:"new_bus"`
	Sectionalizer *sectionalizerRef      `json:"sectionalizer,omitempty"`
}

// sectionalizeResponse reports the outcome: the two bus halves (the original,
// unchanged id as near; the freshly minted far), the far half's new placement
// id, how many connections were re-homed onto the far half, and any soft
// warnings (e.g. the sectionalizer couldn't be wired -- the cut still stands).
type sectionalizeResponse struct {
	NearBus          featureRef `json:"near_bus"`
	FarBus           featureRef `json:"far_bus"`
	FarPlacementID   string     `json:"far_placement_id"`
	ConnectionsMoved int        `json:"connections_moved"`
	Warnings         []string   `json:"warnings,omitempty"`
}

// sectionalizeHandler cuts a drawn busbar placement in two. It sits under the
// /diagrams mount, so the method-aware gate already required diagrams:write; but
// the operation also *creates a feature* and *commits connectivity*, so it
// independently requires features:write (and, when a sectionalizer is given,
// connectivity:commit) -- otherwise a diagrams-only user would escalate into the
// feature and topology write surfaces through this one route. Same "gate on what
// the action actually does" discipline the assistant's splitActions uses.
func sectionalizeHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		placementID := chi.URLParam(r, "placementID")

		user, ok := auth.UserFrom(r.Context())
		if !ok {
			// Belt-and-suspenders: the mount sits behind RequireAuth.
			writeJSON(w, http.StatusUnauthorized, apiError{Error: "authentication required"})
			return
		}
		if !user.Has(auth.PermFeaturesWrite) {
			writeJSON(w, http.StatusForbidden, apiError{Error: "sectionalizing a bus creates a feature and requires " + auth.PermFeaturesWrite})
			return
		}

		var req sectionalizeRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalid("request body is not valid JSON: %v", err))
			return
		}
		if req.At.X == nil || req.At.Y == nil {
			writeError(w, invalid("at.x and at.y are required (the diagram-space point to cut the bar at)"))
			return
		}
		if req.Sectionalizer != nil {
			if req.Sectionalizer.FeatureClass == "" || req.Sectionalizer.FeatureID == "" {
				writeError(w, invalid("sectionalizer.feature_class and sectionalizer.feature_id are required when a sectionalizer is given"))
				return
			}
			if !user.Has(auth.PermConnectivityCommit) {
				writeJSON(w, http.StatusForbidden, apiError{Error: "wiring a sectionalizer commits connectivity and requires " + auth.PermConnectivityCommit})
				return
			}
		}

		result, err := sectionalizeBus(r.Context(), db, id, placementID, req)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

// sectionalizeBus owns the whole transactional sectionalize. Every read and
// write runs against the tx (passed as bun.IDB), so metadata.Load,
// crud.CreateFeature and connectivity.EstablishConnection all compose inside it
// and a failure anywhere rolls the entire cut back.
func sectionalizeBus(ctx context.Context, db *bun.DB, diagramInstanceID, placementID string, req sectionalizeRequest) (*sectionalizeResponse, error) {
	var resp sectionalizeResponse
	err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		// 1. The placement being cut, scoped by its diagram so a placement can't
		//    be reached through the wrong one (same discipline removePlacement uses).
		placement := new(DiagramPlacement)
		if err := tx.NewSelect().Model(placement).
			Where("id = ? AND diagram_instance_id = ?", placementID, diagramInstanceID).
			Scan(ctx); err != nil {
			if err == sql.ErrNoRows {
				return notFound("placement %s not found on diagram %s", placementID, diagramInstanceID)
			}
			return fmt.Errorf("diagram: sectionalize: loading placement: %w", err)
		}

		// 2. Resolve the bus's feature class (code + compiled metadata) from the
		//    placement's stored class id.
		fcCodes, err := loadFeatureClassCodes(ctx, tx, []string{placement.FeatureClassID})
		if err != nil {
			return err
		}
		busCode := fcCodes[placement.FeatureClassID]
		loaded, err := loadFeatureClass(ctx, tx, busCode)
		if err != nil {
			return err
		}

		// 3. Only a bar (a line-on-diagram placement that actually carries a drawn
		//    path) can be sectionalized. A point placement or a line placement with
		//    no hand-drawn polyline has no bar to cut.
		if loaded.FeatureClass.DiagramGeom() != "line" {
			return invalid("feature class %q is not drawn as a line on the diagram, so it has no bar to sectionalize", busCode)
		}
		coords, err := parsePathCoords(placement.Path)
		if err != nil {
			return err
		}
		if coords == nil {
			return invalid("placement %s has no drawn path -- only a hand-drawn bar can be sectionalized", placementID)
		}

		// 4. Cut the drawn path in diagram space. cutFrac is where along the whole
		//    bar the cut landed (by cumulative length), which is what divides the
		//    connectivity below.
		nearPath, farPath, cutFrac := splitPolyline(coords, []float64{*req.At.X, *req.At.Y})
		if cutFrac <= sectionalizeEndpointEpsilon || cutFrac >= 1-sectionalizeEndpointEpsilon {
			return invalid("cut point is on (or too close to) an end of the bar -- choose a point along it")
		}

		// 5. Decide the far half's MAP geometry by *inheriting* the original's: if
		//    the original bus carries a map point, the far half gets an offset copy
		//    (two near-coincident points, dragged apart later); if the original is
		//    diagram-only (MV_BUSBAR is geometry_optional), so is the far half.
		farGeom, err := inheritedOffsetPoint(ctx, tx, loaded.FeatureClass.PhysicalTable, loaded.FeatureClass.SRID, placement.FeatureID)
		if err != nil {
			return err
		}

		// 6. Mint the far-half bus feature through the shared create path (validates
		//    + assigns a territory exactly like any create; a nil geometry is a
		//    legitimate diagram-only feature on this geometry_optional class).
		created, err := crud.CreateFeature(ctx, tx, loaded, crud.CreateFeatureInput{
			Subtype:    req.NewBus.Subtype,
			Name:       req.NewBus.Name,
			Geometry:   farGeom,
			Attributes: req.NewBus.Attributes,
		})
		if err != nil {
			return err
		}
		newBusID, _ := created["id"].(string)
		if newBusID == "" {
			return fmt.Errorf("diagram: sectionalize: created far-half bus has no id")
		}

		// 7. Shorten the original placement to the near half.
		nearJSON, err := json.Marshal(nearPath)
		if err != nil {
			return fmt.Errorf("diagram: sectionalize: encoding near path: %w", err)
		}
		if _, err := tx.NewUpdate().Model((*DiagramPlacement)(nil)).
			Set("path = ?", json.RawMessage(nearJSON)).
			Where("id = ?", placementID).
			Exec(ctx); err != nil {
			return fmt.Errorf("diagram: sectionalize: updating near placement: %w", err)
		}

		// 8. Give the far-half feature its own placement on the same diagram,
		//    carrying the far-half path. Anchor it at the far path's midpoint so its
		//    label doesn't sit on top of the original's.
		farJSON, err := json.Marshal(farPath)
		if err != nil {
			return fmt.Errorf("diagram: sectionalize: encoding far path: %w", err)
		}
		ax, ay := pathMidpoint(farPath)
		farPlacement := &DiagramPlacement{
			DiagramInstanceID: diagramInstanceID,
			FeatureClassID:    placement.FeatureClassID,
			FeatureID:         newBusID,
			X:                 ax,
			Y:                 ay,
			Rotation:          placement.Rotation,
			Path:              json.RawMessage(farJSON),
		}
		if _, err := tx.NewInsert().Model(farPlacement).Returning("*").Exec(ctx); err != nil {
			return fmt.Errorf("diagram: sectionalize: inserting far placement: %w", err)
		}

		// 9. Divide the original bus's connectivity: any connection whose other end
		//    projects past the cut re-homes onto the far half.
		moved, err := divideConnectivity(ctx, tx, diagramInstanceID, placement.FeatureClassID, placement.FeatureID, newBusID, coords, cutFrac)
		if err != nil {
			return err
		}

		// 10. Optionally bridge the two halves with the placed sectionalizer. Best-
		//     effort: a class with no matching bus<->switch network_rule can't be
		//     wired, but that shouldn't undo a valid geometric cut -- surface it as
		//     a warning. (These failures are rule/validation errors that return
		//     before any SQL runs, so they don't poison the transaction.)
		if req.Sectionalizer != nil {
			resp.Warnings = wireSectionalizer(ctx, tx, busCode, placement.FeatureID, newBusID, req.Sectionalizer)
		}

		resp.NearBus = featureRef{FeatureClass: busCode, FeatureID: placement.FeatureID}
		resp.FarBus = featureRef{FeatureClass: busCode, FeatureID: newBusID}
		resp.FarPlacementID = farPlacement.ID
		resp.ConnectionsMoved = moved
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// inheritedOffsetPoint returns the far-half bus's inherited map geometry: an
// offset copy of the original bus's point when it has one, or nil when the
// original is diagram-only (no map geom, a legitimate geometry_optional state).
// The offset is done in EPSG:3857 metres then transformed back to the class's
// own SRID, so it lands in the same coordinate space insertFeature expects. A
// class with no physical table or SRID (never compiled / non-geometric) yields
// nil without erroring. physicalTable is *string and SRID is *int, exactly as
// metadata.FeatureClass carries them.
func inheritedOffsetPoint(ctx context.Context, tx bun.Tx, physicalTable *string, srid *int, featureID string) (json.RawMessage, error) {
	if physicalTable == nil || srid == nil {
		return nil, nil
	}
	// ST_Translate in 3857 gives a real-metre nudge regardless of the stored SRID;
	// the WHERE geom IS NOT NULL means a diagram-only bus simply returns no row.
	q := fmt.Sprintf(
		`SELECT ST_AsGeoJSON(ST_Transform(ST_Translate(ST_Transform(geom, 3857), ?, ?), %d))::text FROM %s WHERE id = ? AND geom IS NOT NULL`,
		*srid, *physicalTable,
	)
	var gj sql.NullString
	switch err := tx.QueryRowContext(ctx, q, sectionalizeOffsetMeters, sectionalizeOffsetMeters, featureID).Scan(&gj); {
	case err == sql.ErrNoRows:
		return nil, nil
	case err != nil:
		return nil, fmt.Errorf("diagram: sectionalize: reading original bus geometry: %w", err)
	}
	if !gj.Valid || gj.String == "" {
		return nil, nil
	}
	return json.RawMessage(gj.String), nil
}

// divideConnectivity re-homes the far-side connections of a sectionalized bus.
// For every connection where the original bus is an endpoint, it projects the
// *other* feature's diagram anchor onto the original bar and, when that lands
// past the cut fraction, flips that connection's bus side to the new far-half
// feature id (class and terminal unchanged -- both halves are the same bus
// class, and re-homing keeps the same terminal). A connection whose other end
// isn't placed on this diagram has no position to project, so it stays with the
// original (near) half by design. Returns how many connections moved.
func divideConnectivity(ctx context.Context, tx bun.Tx, diagramInstanceID, busClassID, origBusID, newBusID string, coords [][]float64, cutFrac float64) (int, error) {
	// Collect first, then mutate: iterating a live rows cursor while issuing the
	// UPDATE/SELECTs below on the same tx would hold the connection open.
	type conn struct {
		id      string
		busIsTo bool
		otherFC string
		otherID string
	}
	rows, err := tx.QueryContext(ctx, `
SELECT id, from_feature_class_id, from_feature_id, to_feature_class_id, to_feature_id
FROM runtime.feature_connectivity
WHERE (from_feature_class_id = ? AND from_feature_id = ?)
   OR (to_feature_class_id = ? AND to_feature_id = ?)`,
		busClassID, origBusID, busClassID, origBusID)
	if err != nil {
		return 0, fmt.Errorf("diagram: sectionalize: reading connectivity: %w", err)
	}
	var conns []conn
	for rows.Next() {
		var id, fromFC, fromID, toFC, toID string
		if err := rows.Scan(&id, &fromFC, &fromID, &toFC, &toID); err != nil {
			rows.Close()
			return 0, fmt.Errorf("diagram: sectionalize: scanning connectivity: %w", err)
		}
		c := conn{id: id}
		if fromFC == busClassID && fromID == origBusID {
			c.busIsTo = false
			c.otherFC, c.otherID = toFC, toID
		} else {
			c.busIsTo = true
			c.otherFC, c.otherID = fromFC, fromID
		}
		conns = append(conns, c)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, fmt.Errorf("diagram: sectionalize: iterating connectivity: %w", err)
	}
	rows.Close()

	moved := 0
	for _, c := range conns {
		// Where does the other feature sit on this diagram? No placement -> it's
		// off-diagram, so keep it with the original half.
		var ox, oy float64
		switch err := tx.QueryRowContext(ctx,
			`SELECT x, y FROM runtime.diagram_placements WHERE diagram_instance_id = ? AND feature_class_id = ? AND feature_id = ?`,
			diagramInstanceID, c.otherFC, c.otherID).Scan(&ox, &oy); {
		case err == sql.ErrNoRows:
			continue
		case err != nil:
			return moved, fmt.Errorf("diagram: sectionalize: locating connected placement: %w", err)
		}

		if locateFraction(coords, []float64{ox, oy}) <= cutFrac {
			continue // near side -- stays on the original bus
		}

		col := "from_feature_id"
		if c.busIsTo {
			col = "to_feature_id"
		}
		// Re-home to the far half. The new bus id is brand-new, so this can never
		// collide with the UNIQUE (from_feature_id, from_terminal_id, to_feature_id,
		// to_terminal_id) key.
		if _, err := tx.ExecContext(ctx,
			fmt.Sprintf("UPDATE runtime.feature_connectivity SET %s = ? WHERE id = ?", col),
			newBusID, c.id); err != nil {
			return moved, fmt.Errorf("diagram: sectionalize: re-homing connection: %w", err)
		}
		moved++
	}
	return moved, nil
}

// wireSectionalizer best-effort connects the placed sectionalizer to both bus
// halves, returning any warnings (never an error): a cut that geometrically
// succeeded shouldn't be undone because the bridging switch has no matching
// network_rule. The bus-side terminal is left to rule resolution; the
// sectionalizer's own near/far terminals are pinned when supplied so the two
// halves don't both land on the same switch terminal.
func wireSectionalizer(ctx context.Context, tx bun.Tx, busCode, nearBusID, farBusID string, s *sectionalizerRef) []string {
	var warnings []string
	targets := []struct {
		busID    string
		terminal *string
		label    string
	}{
		{nearBusID, s.NearTerminal, "near"},
		{farBusID, s.FarTerminal, "far"},
	}
	for _, t := range targets {
		_, err := connectivity.EstablishConnection(ctx, tx, connectivity.EstablishConnectionInput{
			From: connectivity.Endpoint{FeatureClass: busCode, FeatureID: t.busID},
			To:   connectivity.Endpoint{FeatureClass: s.FeatureClass, FeatureID: s.FeatureID, Terminal: t.terminal},
		})
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("could not connect the %s bus half to the sectionalizer: %v", t.label, err))
		}
	}
	return warnings
}

// parsePathCoords decodes a placement's stored path into a coordinate list. A
// nil/empty/JSON-null path (a point placement, or a line drawn without a
// polyline) returns nil coords with no error -- the caller decides that's not
// sectionalizable. A malformed path is a 400.
func parsePathCoords(raw json.RawMessage) ([][]float64, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var pts [][]float64
	if err := json.Unmarshal(raw, &pts); err != nil {
		return nil, invalid("stored path is not an array of [x, y] pairs: %v", err)
	}
	if len(pts) < 2 {
		return nil, invalid("stored path has fewer than two points, nothing to sectionalize")
	}
	return pts, nil
}

// splitPolyline cuts a diagram-space polyline at the point on it nearest pt,
// returning the near half (start -> cut), the far half (cut -> end, sharing the
// cut coordinate so they meet), and the cut's position as a fraction of the
// whole polyline's length. It is the plain planar port of the frontend's
// web/src/lib/geometry.ts splitCoordinates -- diagram coordinates are abstract
// x/y, so there's no projection to reconcile and no PostGIS to defer to.
func splitPolyline(coords [][]float64, pt []float64) (near, far [][]float64, frac float64) {
	if len(coords) < 2 {
		return coords, coords, 0
	}
	bestSeg := 0
	bestSplit := []float64{coords[0][0], coords[0][1]}
	bestDist2 := math.Inf(1)
	for i := 0; i < len(coords)-1; i++ {
		a, b := coords[i], coords[i+1]
		dx, dy := b[0]-a[0], b[1]-a[1]
		len2 := dx*dx + dy*dy
		t := 0.0
		if len2 != 0 {
			t = ((pt[0]-a[0])*dx + (pt[1]-a[1])*dy) / len2
			t = math.Max(0, math.Min(1, t))
		}
		sx, sy := a[0]+t*dx, a[1]+t*dy
		d2 := (pt[0]-sx)*(pt[0]-sx) + (pt[1]-sy)*(pt[1]-sy)
		if d2 < bestDist2 {
			bestDist2 = d2
			bestSeg = i
			bestSplit = []float64{sx, sy}
		}
	}
	near = make([][]float64, 0, bestSeg+2)
	near = append(near, coords[:bestSeg+1]...)
	near = append(near, bestSplit)
	far = make([][]float64, 0, len(coords)-bestSeg)
	far = append(far, bestSplit)
	far = append(far, coords[bestSeg+1:]...)
	frac = fractionAlong(coords, bestSeg, bestSplit)
	return near, far, frac
}

// locateFraction is splitPolyline's fraction alone, for projecting a connected
// feature's anchor onto the bar without allocating the two halves.
func locateFraction(coords [][]float64, pt []float64) float64 {
	_, _, frac := splitPolyline(coords, pt)
	return frac
}

// fractionAlong returns the cumulative-length position of a split point (on
// segment seg) as a fraction of the polyline's total length. A zero-length
// polyline is fraction 0.
func fractionAlong(coords [][]float64, seg int, split []float64) float64 {
	total := polylineLength(coords)
	if total == 0 {
		return 0
	}
	acc := 0.0
	for i := 0; i < seg; i++ {
		acc += dist2D(coords[i], coords[i+1])
	}
	acc += dist2D(coords[seg], split)
	return acc / total
}

func polylineLength(coords [][]float64) float64 {
	total := 0.0
	for i := 0; i < len(coords)-1; i++ {
		total += dist2D(coords[i], coords[i+1])
	}
	return total
}

func dist2D(a, b []float64) float64 {
	dx, dy := b[0]-a[0], b[1]-a[1]
	return math.Hypot(dx, dy)
}

// pathMidpoint anchors the far-half placement's label at the mean of its path's
// first and last points -- cheap and good enough to keep it off the original's
// anchor; the canvas (Slice 3b) lets the user drag it anyway.
func pathMidpoint(coords [][]float64) (x, y float64) {
	if len(coords) == 0 {
		return 0, 0
	}
	first, last := coords[0], coords[len(coords)-1]
	return (first[0] + last[0]) / 2, (first[1] + last[1]) / 2
}
