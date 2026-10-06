package crud

import (
	"context"
	"fmt"

	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/uptrace/bun"
)

// ductWayClass is the per-way child of a CABLE_DUCT (Phase 44 Slice 4). A duct that
// has been fanned out owns one of these per bore; capacity is then answered per-way
// rather than by the coarse field-minus-count fallback.
const ductWayClass = "DUCT_WAY"

// ductWayDetail is one way's occupancy in a per-way capacity answer: its feature id,
// bore position, and whether a cable is routed through it.
type ductWayDetail struct {
	FeatureID string `bun:"feature_id" json:"feature_id"`
	WayNumber int    `bun:"way_number" json:"way_number"`
	Occupied  bool   `bun:"occupied" json:"occupied"`
}

// ductCapacityResult is the spare-way answer for a CABLE_DUCT (Phase 44 Slices 3-4,
// docs/phase-44-cable-management.md).
//
//   - PerWay=false (Slice 3, COARSE): the duct has no DUCT_WAY children. Capacity is
//     the duct's own way_count field; occupancy is the number of cable_routing rows
//     whose ORIGIN is this duct (every such row is a duct->conductor "cable pulled
//     through" link -- the CABLE_TRENCH->CABLE_DUCT link has the duct as DESTINATION,
//     so filtering on origin_feature_id naturally excludes it).
//   - PerWay=true (Slice 4): the duct has been fanned out into DUCT_WAY children.
//     Capacity is the COUNT of those ways; a way is occupied when a cable_routing row
//     originates from IT; Ways carries the per-bore breakdown.
//
// spare = way_count - occupied, clamped at zero, in both modes.
type ductCapacityResult struct {
	WayCount int             `json:"way_count"`
	Occupied int             `json:"occupied"`
	Spare    int             `json:"spare"`
	PerWay   bool            `json:"per_way"`
	Ways     []ductWayDetail `json:"ways,omitempty"`
}

// ductCapacity computes the spare-way capacity of a single feature that carries a
// way_count field (CABLE_DUCT today). It 404s (via getFeature) on a missing row and
// 400s on a class that has no way_count field, so the generic
// /features/{code}/{id}/capacity route stays honest for a non-duct class. It answers
// per-way when the duct owns DUCT_WAY children and falls back to the coarse count
// when it doesn't.
func ductCapacity(ctx context.Context, db *bun.DB, loaded *metadata.Loaded, id string) (*ductCapacityResult, error) {
	row, err := getFeature(ctx, db, loaded, id)
	if err != nil {
		return nil, err
	}
	raw, ok := row["way_count"]
	if !ok {
		return nil, invalid("capacity is only defined for a class with a way_count field (e.g. CABLE_DUCT); %s has none", loaded.FeatureClass.Code)
	}
	wayCount := coerceInt(raw)

	// Per-way first: the DUCT_WAY children contained under this duct. If any exist,
	// the duct has been fanned out (Slice 4) and capacity is answered per bore.
	var wayIDs []string
	if err := db.NewSelect().
		TableExpr("runtime.feature_relationships AS fr").
		Join("JOIN metadata.relationship_classes AS rc ON rc.id = fr.relationship_class_id").
		Join("JOIN metadata.feature_classes AS dc ON dc.id = rc.destination_feature_class_id").
		Where("fr.origin_feature_id = ?", id).
		Where("rc.kind = ?", "containment").
		Where("dc.code = ?", ductWayClass).
		ColumnExpr("fr.destination_feature_id").
		Scan(ctx, &wayIDs); err != nil {
		return nil, fmt.Errorf("crud: duct way children: %w", err)
	}
	if len(wayIDs) > 0 {
		return perWayCapacity(ctx, db, wayIDs)
	}

	// Coarse fallback (Slice 3): cable_routing rows originating from the duct itself.
	// Joined to relationship_classes for the kind filter; no import of the
	// relationship package (it imports crud, so the reverse would be an import cycle)
	// -- a direct table expression is used instead.
	var occupied int
	if err := db.NewSelect().
		TableExpr("runtime.feature_relationships AS fr").
		Join("JOIN metadata.relationship_classes AS rc ON rc.id = fr.relationship_class_id").
		Where("fr.origin_feature_id = ?", id).
		Where("rc.kind = ?", "cable_routing").
		ColumnExpr("count(*)").
		Scan(ctx, &occupied); err != nil {
		return nil, fmt.Errorf("crud: duct capacity count: %w", err)
	}

	spare := wayCount - occupied
	if spare < 0 {
		spare = 0
	}
	return &ductCapacityResult{WayCount: wayCount, Occupied: occupied, Spare: spare, PerWay: false}, nil
}

// perWayCapacity answers capacity from the duct's DUCT_WAY children (Slice 4): each
// way is occupied when a cable_routing row originates from it, and the per-bore
// breakdown is returned so the UI can show which ways are free. wayIDs are the
// containment destinations gathered by the caller.
func perWayCapacity(ctx context.Context, db *bun.DB, wayIDs []string) (*ductCapacityResult, error) {
	wayLoaded, err := metadata.Load(ctx, db, ductWayClass)
	if err != nil {
		return nil, fmt.Errorf("crud: loading %s metadata: %w", ductWayClass, err)
	}
	if wayLoaded.FeatureClass.PhysicalTable == nil {
		// Ways can only exist as feature rows if the class is compiled, so this
		// should be unreachable when wayIDs is non-empty; guard anyway.
		return nil, fmt.Errorf("crud: %s is not compiled", ductWayClass)
	}

	// PhysicalTable is server-controlled metadata (e.g. "gis.duct_way"), interpolated
	// directly the same way panel/fanout.go and getFeature build their table refs.
	var ways []ductWayDetail
	if err := db.NewSelect().
		TableExpr(*wayLoaded.FeatureClass.PhysicalTable+" AS w").
		ColumnExpr("w.id AS feature_id").
		ColumnExpr("w.way_number AS way_number").
		ColumnExpr("EXISTS (SELECT 1 FROM runtime.feature_relationships fr "+
			"JOIN metadata.relationship_classes rc ON rc.id = fr.relationship_class_id "+
			"WHERE fr.origin_feature_id = w.id AND rc.kind = 'cable_routing') AS occupied").
		Where("w.id IN (?)", bun.In(wayIDs)).
		OrderExpr("w.way_number").
		Scan(ctx, &ways); err != nil {
		return nil, fmt.Errorf("crud: per-way capacity: %w", err)
	}

	occupied := 0
	for _, w := range ways {
		if w.Occupied {
			occupied++
		}
	}
	spare := len(ways) - occupied
	if spare < 0 {
		spare = 0
	}
	return &ductCapacityResult{
		WayCount: len(ways),
		Occupied: occupied,
		Spare:    spare,
		PerWay:   true,
		Ways:     ways,
	}, nil
}

// coerceInt narrows the driver's numeric type for a generic-scanned column value to
// an int. way_count is an integer NOT NULL column, but a generic map[string]any scan
// can surface it as int64/int32/float64 depending on the driver path; a nil (should
// not happen for a NOT NULL column) reads as 0.
func coerceInt(v any) int {
	switch n := v.(type) {
	case int64:
		return int(n)
	case int32:
		return int(n)
	case int:
		return n
	case float64:
		return int(n)
	default:
		return 0
	}
}
