package powerflow

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strconv"

	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/uptrace/bun"
)

// readFloatAttr reads a numeric attribute out of crud.GetFeature's own
// attributes map defensively -- a "numeric" column can come back
// either as a Go float64 or (depending on the driver's own decoding)
// as a numeric-looking string, and this package has no reason to care
// which; a value that's neither, or absent, or explicitly null, simply
// isn't set.
func readFloatAttr(attrs map[string]any, name string) (float64, bool) {
	v, ok := attrs[name]
	if !ok || v == nil {
		return 0, false
	}
	switch t := v.(type) {
	case float64:
		return t, true
	case string:
		f, err := strconv.ParseFloat(t, 64)
		if err != nil {
			return 0, false
		}
		return f, true
	default:
		return 0, false
	}
}

// resolveMaterialSpecByCode looks up the material spec (if any) one
// specific domain value code resolves to -- the same domain_values.
// material_spec_id link api/cmd/server/main.go's resolveMaterialSpecs
// reads for a whole domain at once, narrowed here to the single live
// code a specific feature's field currently holds.
func resolveMaterialSpecByCode(ctx context.Context, db bun.IDB, domainID, code string) (*metadata.MaterialSpec, error) {
	var dv metadata.DomainValue
	err := db.NewSelect().Model(&dv).Where("domain_id = ? AND code = ?", domainID, code).Scan(ctx)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("powerflow: resolving domain value %q: %w", code, err)
	}
	if dv.MaterialSpecID == nil {
		return nil, nil
	}

	var spec metadata.MaterialSpec
	if err := db.NewSelect().Model(&spec).Where("id = ?", *dv.MaterialSpecID).Scan(ctx); err != nil {
		return nil, fmt.Errorf("powerflow: loading material spec: %w", err)
	}
	return &spec, nil
}

// conductorImpedance is one edge-class node's own resolved branch
// impedance -- zero value (Resolved = false) means "contributes no
// drop," not an error; see resolveConductorImpedance's own doc
// comment for when that happens.
type conductorImpedance struct {
	rOhm         float64
	xOhm         float64
	ampacityAmps *float64
	resolved     bool
}

// resolveConductorImpedance computes one edge-class node's own R+jX:
// its real geometry length (ST_Length(geom::geography), real meters
// regardless of the class's own SRID -- same technique
// api/internal/relationship's plausibility check already uses for
// ST_Distance) times whichever of the class's own fields currently
// holds a material-linked domain value. Deliberately not hardcoded to
// a field literally named "conductor_spec": this scans every field on
// the class so LV_CONDUCTOR and MV_CONDUCTOR (which don't have to,
// and today don't both, name their material-linked field the same
// way) are each resolved correctly, and a future edge class inherits
// this for free. A class with no material-linked field at all, a field
// left unset on this particular feature, or a linked spec with neither
// resistance nor reactance recorded all resolve to Resolved = false --
// a load flow over an incompletely-cataloged network should degrade to
// "this branch drops nothing" rather than fail outright.
func resolveConductorImpedance(ctx context.Context, db bun.IDB, loaded *metadata.Loaded, n node, attrs map[string]any) (conductorImpedance, error) {
	if loaded.FeatureClass.GeometryType == "none" || loaded.FeatureClass.PhysicalTable == nil {
		return conductorImpedance{}, nil
	}

	var lengthM float64
	query := fmt.Sprintf(`SELECT ST_Length(geom::geography) FROM %s WHERE id = ? AND geom IS NOT NULL`, *loaded.FeatureClass.PhysicalTable)
	if err := db.QueryRowContext(ctx, query, n.featureID).Scan(&lengthM); err != nil {
		return conductorImpedance{}, nil
	}
	if lengthM <= 0 {
		return conductorImpedance{}, nil
	}

	for _, f := range loaded.Fields {
		if f.DomainID == nil {
			continue
		}
		code, ok := attrs[f.Name].(string)
		if !ok || code == "" {
			continue
		}
		spec, err := resolveMaterialSpecByCode(ctx, db, *f.DomainID, code)
		if err != nil {
			return conductorImpedance{}, err
		}
		if spec == nil || (spec.ResistanceOhmPerKm == nil && spec.ReactanceOhmPerKm == nil) {
			continue
		}

		lengthKm := lengthM / 1000
		var r, x float64
		if spec.ResistanceOhmPerKm != nil {
			r = *spec.ResistanceOhmPerKm * lengthKm
		}
		if spec.ReactanceOhmPerKm != nil {
			x = *spec.ReactanceOhmPerKm * lengthKm
		}
		return conductorImpedance{rOhm: r, xOhm: x, ampacityAmps: spec.AmpacityAmps, resolved: true}, nil
	}
	return conductorImpedance{}, nil
}

// demandFieldKW/demandFieldPF are the two field names a SERVICE_POINT
// (or any future load-bearing junction) carries its own real/reactive
// demand under -- kW + power factor rather than kW + kVAR, since power
// factor is the figure a utility planner actually has on hand (see
// docs/open-items.md's Phase 8 section, "load representation" decision).
const (
	demandFieldKW = "demand_kw"
	demandFieldPF = "power_factor"
)

// resolveDemand reads a node's own (P, Q) contribution to the sweep --
// zero for any node that doesn't carry demand_kw (every junction/edge
// class except whichever ones an admin has added the field to), or
// that carries the field but hasn't had a value set on this particular
// feature. Q is derived from P and power factor
// (Q = P * tan(acos(pf))); a demand_kw with no power_factor set
// defaults to unity (pf = 1, Q = 0) rather than treating the whole
// demand as unusable.
func resolveDemand(attrs map[string]any) (kw, kvar float64) {
	kw, ok := readFloatAttr(attrs, demandFieldKW)
	if !ok || kw == 0 {
		return 0, 0
	}
	pf, ok := readFloatAttr(attrs, demandFieldPF)
	if !ok || pf <= 0 || pf > 1 {
		pf = 1
	}
	kvar = kw * math.Tan(math.Acos(pf))
	return kw, kvar
}

// sourceFieldR/sourceFieldX are the two field names the sweep's own
// start feature may carry its Thevenin source impedance under --
// meaningful only on the node a load flow is actually started from;
// harmless and unread on every other node.
const (
	sourceFieldR = "source_r_ohm"
	sourceFieldX = "source_x_ohm"
)

// resolveSourceImpedance reads the start node's own source impedance,
// if it has any -- zero (an ideal, stiff source) when the fields don't
// exist on this class or aren't set on this particular feature.
func resolveSourceImpedance(attrs map[string]any) (rOhm, xOhm float64) {
	r, _ := readFloatAttr(attrs, sourceFieldR)
	x, _ := readFloatAttr(attrs, sourceFieldX)
	return r, x
}

// ratedKVAField is the field name a rated piece of equipment (today
// only DIST_XFMR) carries its own nameplate apparent-power rating
// under -- the junction-node analog of an edge's own resolved
// ampacity, so a transformer's loading % falls out of the same sweep
// an edge's already does, without hardcoding this package to
// DIST_XFMR by name (same generic-field-name approach resolveDemand
// and resolveSourceImpedance already use; any future rated junction
// class gets this for free by adding a field of this name).
const ratedKVAField = "rated_kva"

// resolveTransformerRating reads a junction node's own rated_kva, if
// it has any -- ok = false when the field doesn't exist on this class,
// isn't set on this particular feature, or isn't a usable positive
// value, meaning this node reports no loading % (not "0% loaded").
func resolveTransformerRating(attrs map[string]any) (ratedKVA float64, ok bool) {
	v, ok := readFloatAttr(attrs, ratedKVAField)
	if !ok || v <= 0 {
		return 0, false
	}
	return v, true
}
