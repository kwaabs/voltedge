package admin

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/uptrace/bun"
)

// updateFeatureClass is the safe subset of "edit a feature class after
// creation" -- Name and IsDiagramContainer are pure metadata with no
// physical-table footprint, so they're always changeable. GeometryType/
// SRID/NetworkRole are exactly the three columns compiler/ddl.go reads
// to build the physical table's own shape (the geom column, its type,
// its SRID); changing any of them on a class that's already been
// compiled would leave the physical table and the metadata describing
// it disagreeing with each other, the same class of problem that kept
// editing an already-compiled class's subtypes/domain values out of
// this admin package's original scope. So: those three are only
// accepted while the class is still draft (PhysicalTable is nil) --
// nothing physical exists yet to disagree with.
func updateFeatureClass(ctx context.Context, db *bun.DB, code string, req updateFeatureClassRequest) (*updateFeatureClassResponse, error) {
	if err := validateUpdateFeatureClass(req); err != nil {
		return nil, err
	}

	var resp updateFeatureClassResponse
	err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		fc := new(metadata.FeatureClass)
		if err := tx.NewSelect().Model(fc).Where("code = ?", code).Scan(ctx); err != nil {
			if err == sql.ErrNoRows {
				return notFound("feature class %q does not exist", code)
			}
			return fmt.Errorf("loading feature class %q: %w", code, err)
		}

		cols := make([]string, 0, 5)
		if req.Name != nil {
			fc.Name = *req.Name
			cols = append(cols, "name")
		}
		if req.IsDiagramContainer != nil {
			fc.IsDiagramContainer = *req.IsDiagramContainer
			cols = append(cols, "is_diagram_container")
		}
		if req.GeometryType != nil {
			if fc.Status != "draft" {
				return invalid("geometry_type/srid/network_role can only be changed while %q is still draft (not yet compiled) -- it's %s", code, fc.Status)
			}
			fc.GeometryType = *req.GeometryType
			fc.NetworkRole = *req.NetworkRole
			fc.SRID = req.SRID
			cols = append(cols, "geometry_type", "srid", "network_role")
		}
		if len(cols) == 0 {
			return invalid("request must set at least one of name, is_diagram_container, geometry_type/srid/network_role")
		}

		if _, err := tx.NewUpdate().Model(fc).Column(cols...).Where("code = ?", code).Exec(ctx); err != nil {
			return fmt.Errorf("updating feature class %q: %w", code, err)
		}

		resp = updateFeatureClassResponse{
			Code:               fc.Code,
			Name:               fc.Name,
			GeometryType:       fc.GeometryType,
			SRID:               fc.SRID,
			NetworkRole:        fc.NetworkRole,
			IsDiagramContainer: fc.IsDiagramContainer,
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &resp, nil
}
