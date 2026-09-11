// Package panel is the composite "place one indoor switchgear panel and get its
// whole standard device set" endpoint (Phase 42 Slice 2 -- indoor MV switchgear).
// It owns no feature-class-specific logic and no new write primitive: it
// orchestrates the
// existing shared crud.CreateFeature (one feature) and
// relationship.EstablishRelationship (one containment link) inside a single
// transaction, driven entirely by the metadata.panel_templates recipe (migration
// 0072) for the panel's role. Nothing here hardcodes a device list or a class
// code -- the same reasoning the generic CRUD engine follows.
package panel

import (
	"context"
	"errors"
	"fmt"

	"github.com/kwaabs/voltedge/api/internal/crud"
	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/kwaabs/voltedge/api/internal/relationship"
	"github.com/uptrace/bun"
)

// panelRoleField is the attribute a placed panel carries its role in -- the same
// field the Slice 1 catalog seeded on MV_SWITCHGEAR_PANEL (named panel_role but
// pointing at the shared BAY_ROLE domain). Its coded value (INCOMER / FEEDER /
// ...) is what selects the panel template to fan out.
const panelRoleField = "panel_role"

// containmentKind pins the relationship kind for every link this endpoint writes
// (board -> panel, panel -> device). All three pairings were seeded as
// containment in Slice 1; passing the kind explicitly disambiguates cleanly even
// if a second relationship class is ever added for the same pairing.
const containmentKind = "containment"

// InvalidError is a client-fixable problem (a missing role, a class with no
// template, a board that doesn't exist) -- distinct from a plain error so the
// HTTP layer maps it to 400 rather than 500, the same split circuit / crud /
// territory draw. Callers test with IsInvalid.
type InvalidError struct{ msg string }

func (e *InvalidError) Error() string { return e.msg }

func invalidf(format string, args ...any) error {
	return &InvalidError{msg: fmt.Sprintf(format, args...)}
}

// IsInvalid reports whether err is (or wraps) an InvalidError.
func IsInvalid(err error) bool {
	var ie *InvalidError
	return errors.As(err, &ie)
}

// CreatePanelInput is the composite request: which board (container) to place the
// panel in, which panel class to create, and the panel's own attributes. The
// panel's role -- the panelRoleField value inside Attributes -- picks the device
// template. Subtype/Name are the panel's, exactly as a plain create takes them.
// Board and panel classes are named by code (not hardcoded) so the endpoint stays
// metadata-driven and a future second panel class needs no Go change.
type CreatePanelInput struct {
	BoardFeatureClass string
	BoardFeatureID    string
	PanelFeatureClass string
	Subtype           *int
	Name              *string
	Attributes        map[string]any
}

// CreatedDevice is one fanned-out device in the result: its class, the created
// feature envelope (same shape a create returns), and where it sits on the board
// mimic relative to the panel's column (Phase 42 Slice 3 places it -- Slice 2
// only creates the feature and returns the offset).
type CreatedDevice struct {
	FeatureClass string         `json:"feature_class"`
	Feature      map[string]any `json:"feature"`
	OffsetX      float64        `json:"offset_x"`
	OffsetY      float64        `json:"offset_y"`
	Rotation     float64        `json:"rotation"`
}

// CreatePanelResult reports the whole assembly: the panel and every device
// created and wired into it.
type CreatePanelResult struct {
	Panel   map[string]any  `json:"panel"`
	Devices []CreatedDevice `json:"devices"`
}

// CreatePanel creates one switchgear panel plus the standard device set its role
// template lists, and wires all the containment (board -> panel, panel -> each
// device) -- everything in one transaction, so a failure anywhere leaves no
// half-built panel behind. The device set, subtypes, default attribute values and
// mimic offsets all come from metadata.panel_templates for the panel's
// (class, role); the actual writes reuse the shared crud.CreateFeature and
// relationship.EstablishRelationship untouched. The acting user rides ctx (both
// primitives read auth.ActorID themselves).
func CreatePanel(ctx context.Context, db *bun.DB, in CreatePanelInput) (*CreatePanelResult, error) {
	if in.BoardFeatureClass == "" || in.BoardFeatureID == "" {
		return nil, invalidf("board_feature_class and board_feature_id are required")
	}
	if in.PanelFeatureClass == "" {
		return nil, invalidf("panel_feature_class is required")
	}

	role, _ := in.Attributes[panelRoleField].(string)
	if role == "" {
		return nil, invalidf("panel attribute %q is required -- it selects the device template", panelRoleField)
	}

	panelLoaded, err := metadata.Load(ctx, db, in.PanelFeatureClass)
	if err != nil {
		return nil, invalidf("panel feature class %q not found", in.PanelFeatureClass)
	}
	boardLoaded, err := metadata.Load(ctx, db, in.BoardFeatureClass)
	if err != nil {
		return nil, invalidf("board feature class %q not found", in.BoardFeatureClass)
	}

	// The board is supplied by the caller (the panel and devices are created here,
	// so they can't be missing). feature_relationships has no FK to a per-class
	// feature table, so verify the board exists up front -- otherwise a typo'd id
	// would produce a panel linked to nothing.
	if err := ensureFeatureExists(ctx, db, boardLoaded, in.BoardFeatureID); err != nil {
		return nil, err
	}

	devices, err := metadata.LoadPanelTemplate(ctx, db, in.PanelFeatureClass, role)
	if err != nil {
		return nil, err
	}
	if len(devices) == 0 {
		return nil, invalidf("no panel template defined for %s role %q", in.PanelFeatureClass, role)
	}

	// Pre-load each distinct device class's metadata once (the same class recurs
	// across items, e.g. two isolators), before opening the transaction -- these
	// are read-only lookups and don't belong inside it.
	deviceMeta := map[string]*metadata.Loaded{}
	for _, d := range devices {
		if _, ok := deviceMeta[d.DeviceFeatureClass]; ok {
			continue
		}
		loaded, err := metadata.Load(ctx, db, d.DeviceFeatureClass)
		if err != nil {
			return nil, invalidf("device feature class %q in template not found", d.DeviceFeatureClass)
		}
		deviceMeta[d.DeviceFeatureClass] = loaded
	}

	result := &CreatePanelResult{}
	if err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		// 1. the panel itself.
		panelEnv, err := crud.CreateFeature(ctx, tx, panelLoaded, crud.CreateFeatureInput{
			Subtype:    in.Subtype,
			Name:       in.Name,
			Attributes: in.Attributes,
		})
		if err != nil {
			return err
		}
		panelID, err := featureID(panelEnv)
		if err != nil {
			return err
		}
		result.Panel = panelEnv

		// 2. board contains panel.
		if _, err := relationship.EstablishRelationship(ctx, tx, containment(
			in.BoardFeatureClass, in.BoardFeatureID, in.PanelFeatureClass, panelID,
		)); err != nil {
			return err
		}

		// 3. each templated device: create it, then panel contains device.
		for _, d := range devices {
			env, err := crud.CreateFeature(ctx, tx, deviceMeta[d.DeviceFeatureClass], crud.CreateFeatureInput{
				Subtype:    d.SubtypeCode,
				Attributes: d.Attributes,
			})
			if err != nil {
				return fmt.Errorf("creating templated %s: %w", d.DeviceFeatureClass, err)
			}
			deviceID, err := featureID(env)
			if err != nil {
				return err
			}
			if _, err := relationship.EstablishRelationship(ctx, tx, containment(
				in.PanelFeatureClass, panelID, d.DeviceFeatureClass, deviceID,
			)); err != nil {
				return err
			}
			result.Devices = append(result.Devices, CreatedDevice{
				FeatureClass: d.DeviceFeatureClass,
				Feature:      env,
				OffsetX:      d.OffsetX,
				OffsetY:      d.OffsetY,
				Rotation:     d.Rotation,
			})
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return result, nil
}

// containment builds an EstablishRelationshipInput for a parent-contains-child
// link, pinning the kind so the pairing resolves unambiguously.
func containment(parentClass, parentID, childClass, childID string) relationship.EstablishRelationshipInput {
	kind := containmentKind
	return relationship.EstablishRelationshipInput{
		Origin:      relationship.FeatureRef{FeatureClass: parentClass, FeatureID: parentID},
		Destination: relationship.FeatureRef{FeatureClass: childClass, FeatureID: childID},
		Kind:        &kind,
	}
}

// ensureFeatureExists guards that a live (non-soft-deleted) feature row exists for
// the given class and id. Used only for the caller-supplied board -- a compiled
// class always has a physical table; a draft one (no table) can hold no features,
// so it's a clean client error either way.
func ensureFeatureExists(ctx context.Context, db bun.IDB, loaded *metadata.Loaded, id string) error {
	if loaded.FeatureClass.PhysicalTable == nil {
		return invalidf("feature class %q is not compiled, so it has no features to place a panel in", loaded.FeatureClass.Code)
	}
	var exists bool
	query := fmt.Sprintf("SELECT EXISTS (SELECT 1 FROM %s WHERE id = ? AND deleted_at IS NULL)", *loaded.FeatureClass.PhysicalTable)
	if err := db.QueryRowContext(ctx, query, id).Scan(&exists); err != nil {
		return fmt.Errorf("panel: checking board feature exists: %w", err)
	}
	if !exists {
		return invalidf("board feature %s of class %q not found", id, loaded.FeatureClass.Code)
	}
	return nil
}

// featureID pulls the created feature's id out of the crud envelope as a string.
// The id column comes back either already a string or as raw bytes depending on
// the driver's scan, so both are handled rather than assuming one.
func featureID(env map[string]any) (string, error) {
	switch v := env["id"].(type) {
	case string:
		return v, nil
	case []byte:
		return string(v), nil
	default:
		return "", fmt.Errorf("panel: created feature envelope has no string id (got %T)", env["id"])
	}
}
