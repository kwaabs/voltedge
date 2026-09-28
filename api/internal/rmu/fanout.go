// Package rmu is the composite "place one Ring Main Unit and get its whole
// assembly of switched ways + internal bus" endpoint (Phase RMU Slice 2). It
// owns no feature-class-specific logic and no new write primitive: it
// orchestrates the existing shared crud.CreateFeature (the RMU, its bus, each
// way), relationship.EstablishRelationship (RMU -> bus, RMU -> each way
// containment) and connectivity.EstablishConnection (each way's bus-side
// terminal -> the bus) inside a single transaction, driven entirely by the
// metadata.rmu_templates recipe (migration 0073) for the RMU's configuration.
//
// It is the panel fan-out (api/internal/panel) with ONE thing panels never
// needed: internal connectivity. A panel only wires containment; an RMU is an
// electrical assembly, so its ways must actually connect to the shared bus or
// isolation traces come out wrong (opening a tee way would isolate nothing).
// Nothing here hardcodes a class code, way set or terminal -- they all come
// from the template, the same way the generic CRUD engine reads metadata.
package rmu

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/kwaabs/voltedge/api/internal/connectivity"
	"github.com/kwaabs/voltedge/api/internal/crud"
	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/kwaabs/voltedge/api/internal/relationship"
	"github.com/uptrace/bun"
)

// rmuConfigField is the attribute a placed RMU carries its configuration in --
// the same field the Slice 1 catalog seeded on RMU (rmu_config, pointing at the
// RMU_CONFIG domain). Its coded value (2W_1T, 2W_2T, ...) selects the way
// template to fan out, exactly as a panel's panel_role selects its device set.
const rmuConfigField = "rmu_config"

// containmentKind pins the relationship kind for every link this endpoint
// writes (RMU -> bus, RMU -> way). Both pairings were seeded as containment in
// Slice 1; passing the kind explicitly disambiguates cleanly even if a second
// relationship class is ever added for the same pairing.
const containmentKind = "containment"

// InvalidError is a client-fixable problem (a missing config, a class with no
// template, missing geometry) -- distinct from a plain error so the HTTP layer
// maps it to 400 rather than 500, the same split circuit / crud / panel draw.
// Callers test with IsInvalid.
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

// CreateRMUInput is the composite request: which RMU class to create, where on
// the map (Geometry), and the RMU's own attributes. The RMU's configuration --
// the rmuConfigField value inside Attributes -- picks the way template.
// Subtype/Name/TerritoryID are the RMU's, exactly as a plain create takes them.
// The RMU class is named by code (not hardcoded) so a future second RMU class
// needs no Go change.
type CreateRMUInput struct {
	RMUFeatureClass string
	Subtype         *int
	Name            *string
	Geometry        json.RawMessage
	TerritoryID     *string
	Attributes      map[string]any
}

// CreatedWay is one fanned-out way in the result: its class, the created
// feature envelope (same shape a create returns), the id of the connection
// wiring its bus-side terminal to the internal bus, and where it sits on the
// future RMU mimic (Slice 3 draws it -- Slice 2 only creates + returns).
type CreatedWay struct {
	FeatureClass string         `json:"feature_class"`
	Feature      map[string]any `json:"feature"`
	ConnectionID string         `json:"connection_id"`
	OffsetX      float64        `json:"offset_x"`
	OffsetY      float64        `json:"offset_y"`
	Rotation     float64        `json:"rotation"`

	// Conductor is the external cable feature created on this way's cable side
	// (T1) when the template defines one (migration 0123); ConductorFeatureClass
	// is its class and CableConnectionID the id of the connection wiring the
	// cable to the way's cable terminal. All three are zero-valued when the
	// template item defines no cable, so a config without cables is unaffected.
	ConductorFeatureClass string         `json:"conductor_feature_class,omitempty"`
	Conductor             map[string]any `json:"conductor,omitempty"`
	CableConnectionID     string         `json:"cable_connection_id,omitempty"`
}

// CreateRMUResult reports the whole assembly: the placed RMU, its internal bus,
// and every way created, wired into the RMU by containment and to the bus by
// connectivity.
type CreateRMUResult struct {
	RMU    map[string]any `json:"rmu"`
	Busbar map[string]any `json:"busbar"`
	// BusbarFeatureClass is the internal bus's class code (from the template's
	// BusbarFeatureClass). Carried explicitly because the created-feature
	// envelope holds only column values (id, ...), never its own class -- the
	// auto-diagram builder needs the class to bind the bus into its slot.
	BusbarFeatureClass string       `json:"busbar_feature_class"`
	Ways               []CreatedWay `json:"ways"`
}

// CreateRMU creates one Ring Main Unit plus the internal bus and the set of
// switched ways its configuration template lists, wires all the containment
// (RMU -> bus, RMU -> each way) AND the internal connectivity (each way's
// bus-side terminal -> the bus) -- everything in one transaction, so a failure
// anywhere leaves no half-built RMU behind. The bus class, way set, subtypes,
// default attributes, mimic offsets and each way's bus terminal all come from
// metadata.rmu_templates for the RMU's (class, config); the actual writes reuse
// the shared crud.CreateFeature, relationship.EstablishRelationship and
// connectivity.EstablishConnection untouched. The acting user rides ctx (all
// three primitives read auth.ActorID themselves).
func CreateRMU(ctx context.Context, db *bun.DB, in CreateRMUInput) (*CreateRMUResult, error) {
	if in.RMUFeatureClass == "" {
		return nil, invalidf("rmu_feature_class is required")
	}
	if len(in.Geometry) == 0 {
		return nil, invalidf("geometry is required -- an RMU is a map-placed unit")
	}

	config, _ := in.Attributes[rmuConfigField].(string)
	if config == "" {
		return nil, invalidf("rmu attribute %q is required -- it selects the way template", rmuConfigField)
	}

	rmuLoaded, err := metadata.Load(ctx, db, in.RMUFeatureClass)
	if err != nil {
		return nil, invalidf("rmu feature class %q not found", in.RMUFeatureClass)
	}

	tmpl, err := metadata.LoadRMUTemplate(ctx, db, in.RMUFeatureClass, config)
	if err != nil {
		return nil, err
	}
	if tmpl == nil || len(tmpl.Ways) == 0 {
		return nil, invalidf("no rmu template defined for %s config %q", in.RMUFeatureClass, config)
	}

	busLoaded, err := metadata.Load(ctx, db, tmpl.BusbarFeatureClass)
	if err != nil {
		return nil, invalidf("busbar feature class %q in template not found", tmpl.BusbarFeatureClass)
	}

	// Pre-load each distinct way class's metadata once (the same class recurs
	// across items -- every way is a plain MV_SWITCH), before opening the
	// transaction: read-only lookups don't belong inside it.
	wayMeta := map[string]*metadata.Loaded{}
	// cableMeta holds the metadata for each distinct cable conductor class a way
	// defines (migration 0123). Loaded once up front, outside the tx, like the
	// way classes -- a way without a cable contributes nothing here.
	cableMeta := map[string]*metadata.Loaded{}
	for _, w := range tmpl.Ways {
		if _, ok := wayMeta[w.WayFeatureClass]; !ok {
			loaded, err := metadata.Load(ctx, db, w.WayFeatureClass)
			if err != nil {
				return nil, invalidf("way feature class %q in template not found", w.WayFeatureClass)
			}
			wayMeta[w.WayFeatureClass] = loaded
		}
		if w.CableFeatureClass == nil {
			continue
		}
		if _, ok := cableMeta[*w.CableFeatureClass]; ok {
			continue
		}
		loaded, err := metadata.Load(ctx, db, *w.CableFeatureClass)
		if err != nil {
			return nil, invalidf("cable feature class %q in template not found", *w.CableFeatureClass)
		}
		cableMeta[*w.CableFeatureClass] = loaded
	}

	result := &CreateRMUResult{}
	if err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		// 1. the RMU itself -- the one feature carrying map geometry (and hence a
		// territory); the bus and ways are geometry-optional and created without.
		rmuEnv, err := crud.CreateFeature(ctx, tx, rmuLoaded, crud.CreateFeatureInput{
			Subtype:     in.Subtype,
			Name:        in.Name,
			Geometry:    in.Geometry,
			TerritoryID: in.TerritoryID,
			Attributes:  in.Attributes,
		})
		if err != nil {
			return err
		}
		rmuID, err := featureID(rmuEnv)
		if err != nil {
			return err
		}
		result.RMU = rmuEnv

		// 2. the single internal bus, then RMU contains bus.
		busEnv, err := crud.CreateFeature(ctx, tx, busLoaded, crud.CreateFeatureInput{})
		if err != nil {
			return fmt.Errorf("creating rmu busbar: %w", err)
		}
		busID, err := featureID(busEnv)
		if err != nil {
			return err
		}
		result.Busbar = busEnv
		result.BusbarFeatureClass = tmpl.BusbarFeatureClass
		if _, err := relationship.EstablishRelationship(ctx, tx, containment(
			in.RMUFeatureClass, rmuID, tmpl.BusbarFeatureClass, busID,
		)); err != nil {
			return err
		}

		// 3. each templated way: create it, RMU contains way, and wire the way's
		// bus-side terminal to the bus so a trace can walk cable -> way -> bus ->
		// the other ways, and an OPEN way halts that walk at exactly one way.
		for _, wDef := range tmpl.Ways {
			env, err := crud.CreateFeature(ctx, tx, wayMeta[wDef.WayFeatureClass], crud.CreateFeatureInput{
				Subtype:    wDef.SubtypeCode,
				Attributes: wDef.Attributes,
			})
			if err != nil {
				return fmt.Errorf("creating templated %s: %w", wDef.WayFeatureClass, err)
			}
			wayID, err := featureID(env)
			if err != nil {
				return err
			}
			if _, err := relationship.EstablishRelationship(ctx, tx, containment(
				in.RMUFeatureClass, rmuID, wDef.WayFeatureClass, wayID,
			)); err != nil {
				return err
			}
			busTerminal := wDef.BusTerminal
			conn, err := connectivity.EstablishConnection(ctx, tx, connectivity.EstablishConnectionInput{
				From: connectivity.Endpoint{FeatureClass: wDef.WayFeatureClass, FeatureID: wayID, Terminal: &busTerminal},
				To:   connectivity.Endpoint{FeatureClass: tmpl.BusbarFeatureClass, FeatureID: busID},
			})
			if err != nil {
				return fmt.Errorf("wiring %s.%s to the bus: %w", wDef.WayFeatureClass, busTerminal, err)
			}
			created := CreatedWay{
				FeatureClass: wDef.WayFeatureClass,
				Feature:      env,
				ConnectionID: conn.ID,
				OffsetX:      wDef.OffsetX,
				OffsetY:      wDef.OffsetY,
				Rotation:     wDef.Rotation,
			}

			// 3b. the external cable, when the template defines one (migration
			// 0123): create the geometry-less conductor (its class is
			// geometry_optional, so no map geometry is required) and wire its
			// terminal-less "from" end to the way's cable terminal (T1 by default).
			// The rule MV_CONDUCTOR -> MV_SWITCH.T1 already exists; the connectivity
			// engine is geometry-agnostic, so an edge with no geometry is a valid
			// endpoint. This step is inside the tx: a cable failure rolls the whole
			// RMU back, same as any other way step.
			if wDef.CableFeatureClass != nil {
				condEnv, err := crud.CreateFeature(ctx, tx, cableMeta[*wDef.CableFeatureClass], crud.CreateFeatureInput{
					Subtype:    wDef.CableSubtypeCode,
					Attributes: wDef.CableAttributes,
				})
				if err != nil {
					return fmt.Errorf("creating %s cable for %s: %w", *wDef.CableFeatureClass, wDef.WayFeatureClass, err)
				}
				condID, err := featureID(condEnv)
				if err != nil {
					return err
				}
				cableTerminal := "T1"
				if wDef.CableTerminal != nil && *wDef.CableTerminal != "" {
					cableTerminal = *wDef.CableTerminal
				}
				cableConn, err := connectivity.EstablishConnection(ctx, tx, connectivity.EstablishConnectionInput{
					From: connectivity.Endpoint{FeatureClass: *wDef.CableFeatureClass, FeatureID: condID},
					To:   connectivity.Endpoint{FeatureClass: wDef.WayFeatureClass, FeatureID: wayID, Terminal: &cableTerminal},
				})
				if err != nil {
					return fmt.Errorf("wiring %s cable to %s.%s: %w", *wDef.CableFeatureClass, wDef.WayFeatureClass, cableTerminal, err)
				}
				created.ConductorFeatureClass = *wDef.CableFeatureClass
				created.Conductor = condEnv
				created.CableConnectionID = cableConn.ID
			}

			result.Ways = append(result.Ways, created)
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

// featureID pulls the created feature's id out of the crud envelope as a
// string. The id column comes back either already a string or as raw bytes
// depending on the driver's scan, so both are handled rather than assuming one.
func featureID(env map[string]any) (string, error) {
	switch v := env["id"].(type) {
	case string:
		return v, nil
	case []byte:
		return string(v), nil
	default:
		return "", fmt.Errorf("rmu: created feature envelope has no string id (got %T)", env["id"])
	}
}
