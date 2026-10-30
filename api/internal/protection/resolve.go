package protection

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/uptrace/bun"

	"github.com/kwaabs/voltedge/api/internal/crud"
	"github.com/kwaabs/voltedge/api/internal/metadata"
)

// ResolvedDevice is a protective / switching device located by its bare feature
// id -- the class the register keys on, plus its name for display.
type ResolvedDevice struct {
	FeatureClassCode string `json:"feature_class_code"`
	FeatureID        string `json:"feature_id"`
	Name             string `json:"name,omitempty"`
}

// ResolveDevice finds which PROTECTION device class a bare feature id belongs to,
// scanning only ProtectionDeviceClasses. This lets the picker accept a pasted
// GUID while still refusing one that belongs to the wrong kind of feature (a
// transformer, a line) -- it is reported as "not a protective/switching device"
// rather than silently attached. The first matching class wins (feature ids are
// globally unique). A class not modelled in this deployment is skipped.
func ResolveDevice(ctx context.Context, db bun.IDB, featureID string) (*ResolvedDevice, error) {
	id := strings.TrimSpace(featureID)
	if id == "" {
		return nil, invalid("feature_id is required")
	}
	for _, code := range ProtectionDeviceClasses {
		loaded, err := metadata.Load(ctx, db, code)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				continue // this class isn't modelled here
			}
			return nil, err
		}
		row, err := crud.GetFeature(ctx, db, loaded, id)
		if err != nil {
			if crud.IsNotFoundError(err) {
				continue
			}
			return nil, err
		}
		name, _ := row["name"].(string)
		return &ResolvedDevice{FeatureClassCode: code, FeatureID: id, Name: name}, nil
	}
	return nil, invalid("no protective/switching device has id %s -- protection settings attach only to a breaker, recloser, fuse, switch, isolator, sectionalizer or relay (not a transformer, line, or other feature)", id)
}
