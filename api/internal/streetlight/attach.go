// Package streetlight is the small composite action behind "attach a lamp to a
// pole": it creates a geometry-less pole-mounted STREET_LIGHT, links it to the
// pole structurally, and (best-effort) wires it to the single LV conductor the
// pole carries nearby. It orchestrates the same three shared primitives the kit
// fan-out engines use -- crud.CreateFeature, relationship.EstablishRelationship,
// connectivity.EstablishConnection -- for one targeted attachment rather than a
// whole templated kit.
//
// Data model (seed/street_light_pole_mounted.sql): STREET_LIGHT subtype 3
// pole_mounted + geometry_optional (the lamp rides its pole, no own geometry) +
// a POLE supports STREET_LIGHT relationship class. The LV feed rule
// (LV_CONDUCTOR -> STREET_LIGHT T1, phase-matched) already ships.
package streetlight

import (
	"context"
	"fmt"

	"github.com/uptrace/bun"

	"github.com/kwaabs/voltedge/api/internal/connectivity"
	"github.com/kwaabs/voltedge/api/internal/crud"
	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/kwaabs/voltedge/api/internal/relationship"
)

const (
	streetLightClass   = "STREET_LIGHT"
	lvConductorClass   = "LV_CONDUCTOR"
	poleMountedSubtype = 3
	supportKind        = "spatial_support"
	lampTerminal       = "T1"
	// defaultRadiusMeters: how close an LV conductor must pass to the pole to be
	// considered "on" it for the auto-wire. ~10 m covers the span sag / capture
	// slop without reaching an adjacent street's line.
	defaultRadiusMeters = 10.0
)

// AttachInput identifies the pole and the lamp to put on it.
type AttachInput struct {
	PoleFeatureClass string         // the pole class (normally POLE)
	PoleFeatureID    string         // the existing pole to attach to
	SubtypeCode      *int           // the lamp subtype; nil -> pole_mounted (3)
	Attributes       map[string]any // the lamp's required fields (phase, luminaire_type, condition_rating) + optional load
	RadiusMeters     float64        // auto-wire search radius; <=0 -> default
	// CanWire gates the best-effort auto-wire on the caller holding
	// connectivity:commit. False -> attach only (create + the support link), never
	// wire -- EstablishConnection has no RBAC gate of its own, so the handler
	// enforces it here.
	CanWire bool
}

// AttachResult is the outcome: the created lamp, its support link, and whether
// (and why not) it was auto-wired to the pole's LV.
type AttachResult struct {
	StreetLight  map[string]any                   `json:"street_light"`
	Relationship *relationship.RelationshipResult `json:"relationship"`
	Wired        bool                             `json:"wired"`
	Connection   *connectivity.ConnectionResult   `json:"connection,omitempty"`
	WireNote     string                           `json:"wire_note,omitempty"`
}

// AttachStreetLight creates the geometry-less pole lamp and links it to the pole
// (atomically), then best-effort wires it to the one LV conductor within range.
//
// The create + the support relationship commit together in one transaction -- a
// lamp that couldn't be linked to its pole must not exist. The auto-wire runs
// AFTER that commit, outside the transaction: it is a convenience, so a wiring
// failure (no conductor in range, several in range, a phase mismatch, or the
// caller lacking connectivity:commit) leaves the lamp attached-but-unwired with a
// note, never undoes the attachment.
func AttachStreetLight(ctx context.Context, db *bun.DB, in AttachInput) (*AttachResult, error) {
	if in.PoleFeatureClass == "" || in.PoleFeatureID == "" {
		return nil, invalidf("pole_feature_class and pole_feature_id are required")
	}
	lampLoaded, err := metadata.Load(ctx, db, streetLightClass)
	if err != nil {
		return nil, fmt.Errorf("streetlight: loading %s: %w", streetLightClass, err)
	}
	subtype := poleMountedSubtype
	if in.SubtypeCode != nil {
		subtype = *in.SubtypeCode
	}

	var lamp map[string]any
	var lampID string
	var rel *relationship.RelationshipResult

	// 1. create the geometry-less lamp + the support link, atomically.
	if err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		env, err := crud.CreateFeature(ctx, tx, lampLoaded, crud.CreateFeatureInput{
			Subtype:    &subtype,
			Attributes: in.Attributes,
			// No Geometry -> geometry-less (STREET_LIGHT is geometry_optional). The
			// lamp has no independent location; it rides its pole.
		})
		if err != nil {
			return err
		}
		lamp = env
		id, ok := env["id"].(string)
		if !ok {
			return fmt.Errorf("streetlight: created lamp has no id")
		}
		lampID = id

		r, err := relationship.EstablishRelationship(ctx, tx, relationship.EstablishRelationshipInput{
			Origin:      relationship.FeatureRef{FeatureClass: in.PoleFeatureClass, FeatureID: in.PoleFeatureID},
			Destination: relationship.FeatureRef{FeatureClass: streetLightClass, FeatureID: lampID},
			Kind:        strptr(supportKind),
		})
		if err != nil {
			return err
		}
		rel = r
		return nil
	}); err != nil {
		return nil, err
	}

	result := &AttachResult{StreetLight: lamp, Relationship: rel}

	// 2. best-effort auto-wire (never fatal past here -- the lamp is attached).
	if !in.CanWire {
		result.WireNote = "attached but not wired -- needs connectivity:commit to auto-wire"
		return result, nil
	}
	radius := in.RadiusMeters
	if radius <= 0 {
		radius = defaultRadiusMeters
	}
	condIDs, err := nearbyLVConductors(ctx, db, in.PoleFeatureClass, in.PoleFeatureID, radius)
	if err != nil {
		result.WireNote = "attached; could not search for a nearby LV conductor to auto-wire"
		return result, nil
	}
	switch len(condIDs) {
	case 0:
		result.WireNote = "attached; no LV conductor within range -- wire it to the LV manually"
		return result, nil
	case 1:
		conn, err := connectivity.EstablishConnection(ctx, db, connectivity.EstablishConnectionInput{
			From: connectivity.Endpoint{FeatureClass: lvConductorClass, FeatureID: condIDs[0]},
			To:   connectivity.Endpoint{FeatureClass: streetLightClass, FeatureID: lampID, Terminal: strptr(lampTerminal)},
		})
		if err != nil {
			// A phase mismatch (or any rule rejection) on the one candidate -- attach
			// stands, the crew wires it deliberately.
			result.WireNote = "attached; found one LV conductor but couldn't auto-wire it (" + err.Error() + ") -- wire it manually"
			return result, nil
		}
		result.Wired = true
		result.Connection = conn
		return result, nil
	default:
		result.WireNote = fmt.Sprintf("attached; %d LV conductors within range -- ambiguous, wire it manually", len(condIDs))
		return result, nil
	}
}

// nearbyLVConductors returns the live LV_CONDUCTOR ids whose geometry passes
// within radiusM of the pole's point. physical tables come from metadata (the
// compiler-generated, schema-qualified names), so they're interpolated like the
// other spatial scans; the pole id and radius are bound. Geography casts give a
// true metres distance regardless of SRID.
func nearbyLVConductors(ctx context.Context, db *bun.DB, poleClass, poleID string, radiusM float64) ([]string, error) {
	poleLoaded, err := metadata.Load(ctx, db, poleClass)
	if err != nil {
		return nil, fmt.Errorf("streetlight: loading %s: %w", poleClass, err)
	}
	lvLoaded, err := metadata.Load(ctx, db, lvConductorClass)
	if err != nil {
		return nil, fmt.Errorf("streetlight: loading %s: %w", lvConductorClass, err)
	}
	if poleLoaded.FeatureClass.PhysicalTable == nil || lvLoaded.FeatureClass.PhysicalTable == nil {
		return nil, fmt.Errorf("streetlight: pole or LV conductor class is not compiled")
	}

	query := fmt.Sprintf(`
		SELECT lc.id::text AS id
		FROM %s lc, %s p
		WHERE p.id = ?
		  AND p.deleted_at IS NULL AND lc.deleted_at IS NULL
		  AND p.geom IS NOT NULL AND lc.geom IS NOT NULL
		  AND ST_DWithin(lc.geom::geography, p.geom::geography, ?)`,
		*lvLoaded.FeatureClass.PhysicalTable, *poleLoaded.FeatureClass.PhysicalTable)

	var rows []struct {
		ID string `bun:"id"`
	}
	if err := db.NewRaw(query, poleID, radiusM).Scan(ctx, &rows); err != nil {
		return nil, fmt.Errorf("streetlight: searching for nearby LV conductors: %w", err)
	}
	ids := make([]string, len(rows))
	for i := range rows {
		ids[i] = rows[i].ID
	}
	return ids, nil
}

func strptr(s string) *string { return &s }
