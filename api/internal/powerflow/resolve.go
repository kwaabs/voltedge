package powerflow

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

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

	// zero-sequence branch impedance (ground-fault Slice 3), from the spec's own
	// resistance_zero_ohm_per_km / reactance_zero_ohm_per_km. zeroSeqResolved =
	// false means the spec carried no measured zero-sequence data, so the SLG calc
	// falls back to 3x the positive-sequence r/x (zeroSeqLineFactor) and flags the
	// result estimated -- r0Ohm/x0Ohm are then left zero and unused.
	r0Ohm           float64
	x0Ohm           float64
	zeroSeqResolved bool
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

		// Zero-sequence branch impedance, when the spec records it. Scaled by the
		// same length; either column present turns on the real (non-estimated)
		// path -- a missing one contributes zero for that component rather than
		// disqualifying the whole zero-sequence figure.
		var r0, x0 float64
		zeroSeqResolved := spec.ResistanceZeroOhmPerKm != nil || spec.ReactanceZeroOhmPerKm != nil
		if spec.ResistanceZeroOhmPerKm != nil {
			r0 = *spec.ResistanceZeroOhmPerKm * lengthKm
		}
		if spec.ReactanceZeroOhmPerKm != nil {
			x0 = *spec.ReactanceZeroOhmPerKm * lengthKm
		}
		return conductorImpedance{
			rOhm: r, xOhm: x, r0Ohm: r0, x0Ohm: x0,
			zeroSeqResolved: zeroSeqResolved, ampacityAmps: spec.AmpacityAmps, resolved: true,
		}, nil
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

// sourceFieldR0/sourceFieldX0 are the zero-sequence counterparts of
// sourceFieldR/sourceFieldX (ground-fault Slice 3): the start node's own
// Thevenin *zero-sequence* source impedance, meaningful only for an SLG run
// started at a grounded source. Absent -> the SLG calc falls back to the
// positive-sequence source impedance (Z0 = Z1) and flags the result estimated,
// its Slice 1 behaviour.
const (
	sourceFieldR0 = "source_r0_ohm"
	sourceFieldX0 = "source_x0_ohm"
)

// resolveSourceImpedance reads the start node's own source impedance,
// if it has any -- zero (an ideal, stiff source) when the fields don't
// exist on this class or aren't set on this particular feature.
func resolveSourceImpedance(attrs map[string]any) (rOhm, xOhm float64) {
	r, _ := readFloatAttr(attrs, sourceFieldR)
	x, _ := readFloatAttr(attrs, sourceFieldX)
	return r, x
}

// resolveSourceZeroSeq reads the start node's own zero-sequence source
// impedance. ok = false when neither field is set, telling the SLG calc to fall
// back to the positive-sequence source impedance rather than an ideal zero
// (a source with no positive-sequence impedance either is genuinely ideal).
func resolveSourceZeroSeq(attrs map[string]any) (r0, x0 float64, ok bool) {
	r, okR := readFloatAttr(attrs, sourceFieldR0)
	x, okX := readFloatAttr(attrs, sourceFieldX0)
	if !okR && !okX {
		return 0, 0, false
	}
	return r, x, true
}

// ratedKVAField / ratedMVAField are the two field names a rated piece of
// equipment carries its own nameplate apparent-power rating under -- the
// junction-node analog of an edge's own resolved ampacity, so a transformer's
// loading % falls out of the same sweep an edge's already does, without
// hardcoding this package to a class by name (same generic-field-name approach
// resolveDemand and resolveSourceImpedance already use; any future rated
// junction class gets this for free by adding a field of one of these names).
// Distribution transformers (DIST_XFMR) are nameplated in kVA; power
// transformers (POWER_XFMR) in MVA -- two units for the same quantity, so the
// engine reads whichever the class actually carries.
const (
	ratedKVAField = "rated_kva"
	ratedMVAField = "rated_mva"
)

// resolveTransformerRating reads a junction node's own nameplate
// apparent-power rating, normalised to kVA. It accepts either rated_kva
// (distribution transformers) or rated_mva (power transformers, whose
// nameplates are quoted in MVA, converted ×1000 here); rated_kva wins when both
// happen to be set. ok = false when neither field exists on this class, neither
// is set on this particular feature, or the value isn't a usable positive
// number -- meaning this node reports no loading % (not "0% loaded").
func resolveTransformerRating(attrs map[string]any) (ratedKVA float64, ok bool) {
	if v, okKVA := readFloatAttr(attrs, ratedKVAField); okKVA && v > 0 {
		return v, true
	}
	if v, okMVA := readFloatAttr(attrs, ratedMVAField); okMVA && v > 0 {
		return v * 1000, true
	}
	return 0, false
}

// transformer model field names -- read by name off the DIST_XFMR row, same as
// every other electrical input this package resolves. primaryVoltageField is
// the existing coded VOLTAGE_CLASS field (parsed to volts by
// parseVoltageClassCode); the other three are the Phase 28 additions
// (seed/transformer_model_fields.sql), all nullable so an un-surveyed
// transformer degrades gracefully.
const (
	primaryVoltageField   = "primary_voltage"
	secondaryVoltageField = "secondary_voltage"
	impedancePctField     = "impedance_pct"
	xrRatioField          = "x_r_ratio"
)

// three-winding field names (POWER_XFMR, seed/power_transformer_example.sql),
// all nullable so a two-winding unit -- or an un-surveyed tertiary -- degrades
// to the two-winding path. tertiary_voltage is the TV nominal (its presence is
// what turns on the three-winding model); z_hv_tv_pct / z_lv_tv_pct are the
// other two nameplate pair impedances (the HV-LV pair is the existing
// impedance_pct), on the same rated_mva base, used to derive the star equivalent.
const (
	tertiaryVoltageField = "tertiary_voltage"
	zHVTVPctField        = "z_hv_tv_pct"
	zLVTVPctField        = "z_lv_tv_pct"
)

// tap-changer field names (POWER_XFMR, seed/power_transformer_example.sql). An
// on-load tap changer shifts the effective turns ratio to hold the output
// voltage as load swings: tap_position is the integer step offset from nominal
// (0 = nominal tap), tap_step_pct the per-step voltage change as a percent of
// nominal. tap_min/tap_max bound the position and are validation inputs (CRUD),
// not read by the engine -- the sweep applies whatever position is set.
const (
	tapPositionField = "tap_position"
	tapStepPctField  = "tap_step_pct"
)

// z0PctField is a transformer's nameplate zero-sequence impedance as a percent
// on its own rating base (ground-fault Slice 3), the zero-sequence counterpart
// of impedance_pct. Read only by the SLG calc; when absent (every un-surveyed
// unit) that calc falls back to taking the transformer's zero-sequence impedance
// equal to its positive-sequence %Z (Z0 = Z1) and flags the result estimated,
// its Slice 1 behaviour. Referred to the secondary base and split by the same
// x_r_ratio as the positive-sequence impedance -- a dedicated zero-sequence X/R
// is a refinement not worth its own field yet.
const z0PctField = "z0_pct"

// defaultTransformerXR is the X/R the series-impedance split falls back to when
// a transformer carries a nameplate impedance_pct but no x_r_ratio -- a
// representative distribution-transformer value, applied only as graceful
// degradation (the same spirit as power_factor defaulting to unity in
// resolveDemand), never overriding an x_r_ratio that is actually set.
const defaultTransformerXR = 2.0

// parseVoltageClassCode turns a coded VOLTAGE_CLASS value ("11KV", "33KV", or a
// plain "400V") into volts. Deliberately a parse, not a hardcoded 11KV/33KV
// lookup table, so any NkV / NV code resolves and the domain can grow without
// this package changing -- the same numeric intent the CIM export encodes as a
// domain_map (cim_mapping_example.sql). ok = false for an empty or
// non-numeric code, in which case the node simply doesn't transform voltage.
func parseVoltageClassCode(code string) (float64, bool) {
	s := strings.ToUpper(strings.TrimSpace(code))
	if s == "" {
		return 0, false
	}
	mult := 1.0
	switch {
	case strings.HasSuffix(s, "KV"):
		mult = 1000
		s = strings.TrimSuffix(s, "KV")
	case strings.HasSuffix(s, "V"):
		s = strings.TrimSuffix(s, "V")
	}
	n, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || n <= 0 {
		return 0, false
	}
	return n * mult, true
}

// windingConnection is how one transformer winding is connected -- the input a
// single-line-to-ground (ground-fault) calc needs that a balanced load flow
// never does, because zero-sequence current can only flow where a winding
// provides an earth path. delta blocks zero-sequence from passing through to the
// line (an open circuit to through zero-sequence, though its closed loop grounds
// the *other* winding when that one is wye-grounded); wyeGrounded passes
// zero-sequence to earth through its neutral; wyeUngrounded (no neutral brought
// out) offers no zero-sequence ground path at all.
type windingConnection int

const (
	connWyeUngrounded windingConnection = iota // Y / y   -- no ground path
	connWyeGrounded                            // YN / yn / Z / ZN (zig-zag is inherently earthed)
	connDelta                                  // D / d
)

// vectorGroupField is the coded VECTOR_GROUP attribute (POWER_XFMR,
// seed/power_transformer_example.sql); a value like "DYN11" / "YND11" encodes
// each winding's connection, which the ground-fault calc reads. DIST_XFMR
// carries no such field, and an un-surveyed POWER_XFMR may leave it blank --
// both default to Dyn (delta HV, wye-grounded LV), the near-universal
// distribution connection, so the common case needs no data entry.
const vectorGroupField = "vector_group"

// parseVectorGroup splits a vector-group code ("DYN11", "YNd11", "YNyn0", "Dd0",
// "Yy0", "Dzn11") into its HV and LV winding connections. The leading token is
// the HV winding, the next the LV; the trailing clock number (a phase shift,
// irrelevant to zero-sequence magnitude) is ignored. An empty or unrecognised
// code returns the Dyn default (delta HV, wye-grounded LV).
func parseVectorGroup(code string) (hv, lv windingConnection) {
	s := strings.ToUpper(strings.TrimSpace(code))
	s = strings.TrimRight(s, "0123456789") // drop the clock number
	hv, rest, okHV := consumeWindingToken(s)
	lv, _, okLV := consumeWindingToken(rest)
	if !okHV || !okLV {
		return connDelta, connWyeGrounded // Dyn default
	}
	return hv, lv
}

// consumeWindingToken reads one winding-connection token off the front of a
// vector-group string -- longest match first so "YN"/"ZN" win over "Y"/"Z" --
// returning the connection and the remainder. A zig-zag winding (Z/ZN) is a
// dedicated earthing connection, so it counts as a grounded zero-sequence path.
func consumeWindingToken(s string) (windingConnection, string, bool) {
	switch {
	case strings.HasPrefix(s, "YN"), strings.HasPrefix(s, "ZN"):
		return connWyeGrounded, s[2:], true
	case strings.HasPrefix(s, "Z"):
		return connWyeGrounded, s[1:], true
	case strings.HasPrefix(s, "Y"):
		return connWyeUngrounded, s[1:], true
	case strings.HasPrefix(s, "D"):
		return connDelta, s[1:], true
	}
	return connWyeUngrounded, s, false
}

// transformerModel is a transformer node's resolved behaviour for the sweep:
// the turns ratio(s) that rebase voltage across it and (optionally) the series
// impedance(s) that drop voltage through it. ok = false for any node that isn't
// a voltage-transforming transformer (no primary+secondary nominal resolved),
// in which case the sweep treats it as the electrical pass-through it was before
// Phase 28. rOhm/xOhm are zero (an ideal ratio, no through-drop) when the
// nameplate impedance_pct or rating needed to compute them isn't set.
//
// Two-winding (threeWinding = false) uses ratio/rOhm/xOhm/secondaryV only -- one
// output on the secondary base, exactly the Phase 28 model. Three-winding
// (POWER_XFMR with a tertiary_voltage) adds a second output (TV) and models the
// unit as its star equivalent: three arms (H, L, T) meeting at a common star
// point, so each output sees its own arm plus the shared H arm (which carries
// both windings' through-power). The H arm is pre-referred to each output's
// base (rHtoL/xHtoL to the LV base, rHtoT/xHtoT to the TV base) so the forward
// sweep composes the drops in volts without re-referring. secondaryV/ratio stay
// the LV winding in both models.
type transformerModel struct {
	ratio      float64 // secondary (LV) nominal / primary nominal (dimensionless)
	rOhm       float64 // two-winding: series R referred to the secondary, per-phase ohm
	xOhm       float64 // two-winding: series X referred to the secondary, per-phase ohm
	secondaryV float64 // nominal secondary (LV) line-to-line volts (the base below the transformer)
	ok         bool

	// winding connections (from the vector group; Dyn default) -- read only by
	// the ground-fault calc to gate the zero-sequence network, unused by the
	// balanced load flow. hvConn is the source-side winding, lvConn the
	// output(secondary)-side.
	hvConn windingConnection
	lvConn windingConnection

	// dedicated zero-sequence series impedance referred to the secondary, from a
	// nameplate z0_pct (ground-fault Slice 3). zeroSeqResolved = false means the
	// unit carried no z0_pct, so the SLG calc uses rOhm/xOhm (Z0 = Z1) as an
	// estimate instead -- r0Ohm/x0Ohm are then left zero and unused.
	r0Ohm           float64
	x0Ohm           float64
	zeroSeqResolved bool

	// three-winding extension (all zero/false for a two-winding unit)
	threeWinding  bool
	tertiaryRatio float64 // tertiary nominal / primary nominal (dimensionless)
	tertiaryV     float64 // nominal tertiary line-to-line volts (the base on the TV side)
	rL, xL        float64 // L arm referred to the LV base, per-phase ohm
	rT, xT        float64 // T arm referred to the TV base, per-phase ohm
	rHtoL, xHtoL  float64 // H arm referred to the LV base, per-phase ohm
	rHtoT, xHtoT  float64 // H arm referred to the TV base, per-phase ohm
}

// tapFactor is the multiplier an on-load tap changer applies to the nominal
// turns ratio: 1 + tap_position·tap_step_pct/100. A node with neither tap field
// set, or an explicit position 0, returns 1 (nominal tap) -- the tap is
// graceful degradation, the same spirit as x_r_ratio / power_factor defaulting.
// Applied to the LV (secondary) ratio only, the winding an OLTC regulates; a
// three-winding unit's tertiary ratio stays nominal (these fields don't record
// which winding physically carries the tap, so the sweep models the common
// "regulate the main LV output" intent rather than inventing an HV-side tap
// that would also shift the tertiary).
func tapFactor(attrs map[string]any) float64 {
	pos, okPos := readFloatAttr(attrs, tapPositionField)
	step, okStep := readFloatAttr(attrs, tapStepPctField)
	if !okPos || !okStep || step == 0 {
		return 1
	}
	return 1 + pos*step/100
}

// splitZ divides a series-impedance magnitude into R and X by an X/R ratio:
// R = |Z| / sqrt(1 + xr²), X = R·xr. A negative |Z| (a legitimate star arm when
// the three pair impedances are such that one arm goes negative) splits sign-
// consistently, which is exactly what the linearized drop wants.
func splitZ(zOhm, xr float64) (r, x float64) {
	r = zOhm / math.Sqrt(1+xr*xr)
	return r, r * xr
}

// resolveTransformerModel reads a junction node's two-winding behaviour. Both
// primary (coded VOLTAGE_CLASS -> volts) and secondary (numeric volts) nominal
// must resolve for the node to transform voltage at all; both are quoted on the
// same line-to-line basis, so the ratio is basis-independent (works whether the
// run supplied a line-to-line or phase-to-neutral nominal_voltage_v). The series
// impedance is added only when the node also carries a nameplate impedance_pct
// and a positive rated_kva, since Z_base = secondary^2 / S_rated needs the
// rating; %Z is split into R and X by x_r_ratio (defaultTransformerXR when
// unset). Z_ohm is the per-phase equivalent referred to the secondary -- the
// same per-phase / line-to-line convention resolveConductorImpedance uses -- so
// it composes with approxDropV unchanged.
func resolveTransformerModel(attrs map[string]any, ratedKVA float64, hasRating bool) transformerModel {
	code, _ := attrs[primaryVoltageField].(string)
	primaryV, okP := parseVoltageClassCode(code)
	secondaryV, okS := readFloatAttr(attrs, secondaryVoltageField)
	if !okP || !okS || primaryV <= 0 || secondaryV <= 0 {
		return transformerModel{}
	}

	m := transformerModel{ratio: secondaryV / primaryV, secondaryV: secondaryV, ok: true}

	// Winding connections for the ground-fault calc (Dyn default when the vector
	// group is absent/unparseable, e.g. every DIST_XFMR). Load flow ignores these.
	vg, _ := attrs[vectorGroupField].(string)
	m.hvConn, m.lvConn = parseVectorGroup(vg)

	// An on-load tap changer shifts the effective LV ratio off nominal. Applied
	// here so both the forward sweep's LV output (secondary/three-winding LV) and
	// fault.go's ratio-referred impedance reflect the live tap; the impedance
	// base stays the nameplate secondaryV (a few-percent tap doesn't rebase %Z).
	m.ratio *= tapFactor(attrs)

	// %Z (HV-LV) and X/R are shared inputs. xr defaults when unset, same as the
	// Phase 28 two-winding path.
	zHL, okZ := readFloatAttr(attrs, impedancePctField)
	xr, okXR := readFloatAttr(attrs, xrRatioField)
	if !okXR || xr < 0 {
		xr = defaultTransformerXR
	}

	// Dedicated zero-sequence series impedance from a nameplate z0_pct, referred
	// to the secondary on the same rating base and X/R split as the positive-
	// sequence %Z above (ground-fault Slice 3). Set here so both the two- and
	// three-winding paths carry it; when z0_pct is absent the ground-fault calc
	// falls back to the positive-sequence rOhm/xOhm as an estimate.
	if z0Pct, okZ0 := readFloatAttr(attrs, z0PctField); okZ0 && z0Pct > 0 && hasRating && ratedKVA > 0 {
		zBase := secondaryV * secondaryV / (ratedKVA * 1000)
		m.r0Ohm, m.x0Ohm = splitZ(z0Pct/100*zBase, xr)
		m.zeroSeqResolved = true
	}

	// Three-winding: a tertiary nominal is what turns it on. Adds the TV output
	// and, when the full nameplate set is present, the star-equivalent arms.
	if tertiaryV, okT := readFloatAttr(attrs, tertiaryVoltageField); okT && tertiaryV > 0 {
		m.threeWinding = true
		m.tertiaryV = tertiaryV
		m.tertiaryRatio = tertiaryV / primaryV

		zHT, okHT := readFloatAttr(attrs, zHVTVPctField)
		zLT, okLT := readFloatAttr(attrs, zLVTVPctField)
		switch {
		case okZ && zHL > 0 && okHT && zHT > 0 && okLT && zLT > 0 && hasRating && ratedKVA > 0:
			// Full star split from the three measured pair impedances (all
			// nameplate %Z on the rated_mva base): Z_H = ½(Z_HL+Z_HT−Z_LT),
			// Z_L = ½(Z_HL+Z_LT−Z_HT), Z_T = ½(Z_HT+Z_LT−Z_HL). Each arm is
			// converted to ohms on the base of the winding whose branch will see
			// it, so the forward sweep never re-refers.
			zHPct := 0.5 * (zHL + zHT - zLT)
			zLPct := 0.5 * (zHL + zLT - zHT)
			zTPct := 0.5 * (zHT + zLT - zHL)
			sVA := ratedKVA * 1000
			zBaseLV := secondaryV * secondaryV / sVA
			zBaseTV := tertiaryV * tertiaryV / sVA
			m.rL, m.xL = splitZ(zLPct/100*zBaseLV, xr)
			m.rT, m.xT = splitZ(zTPct/100*zBaseTV, xr)
			m.rHtoL, m.xHtoL = splitZ(zHPct/100*zBaseLV, xr)
			m.rHtoT, m.xHtoT = splitZ(zHPct/100*zBaseTV, xr)
		case okZ && zHL > 0 && hasRating && ratedKVA > 0:
			// Only the HV-LV nameplate impedance surveyed: put it entirely on the
			// LV arm (matching the two-winding drop) and leave the tertiary ideal
			// (ratio only, no through-drop) rather than inventing a split.
			zBaseLV := secondaryV * secondaryV / (ratedKVA * 1000)
			m.rL, m.xL = splitZ(zHL/100*zBaseLV, xr)
		}
		// fault.go reads the two-winding rOhm/xOhm/ratio to build an LV fault
		// level; the HV-LV equivalent seen from the LV winding is the H arm plus
		// the L arm (their %Z sum is the measured zHL), so populate it here from
		// whichever arms the switch above resolved -- an LV fault through a
		// three-winding unit then reflects its real %Z, and a TV fault (rarer,
		// and rebased on the LV ratio here) stays flagged as an upper bound.
		m.rOhm, m.xOhm = m.rHtoL+m.rL, m.xHtoL+m.xL
		return m
	}

	// Two-winding: single lumped series impedance referred to the secondary --
	// unchanged from Phase 28.
	if okZ && zHL > 0 && hasRating && ratedKVA > 0 {
		zBase := secondaryV * secondaryV / (ratedKVA * 1000) // per-phase ohm referred to secondary, 3-phase base
		m.rOhm, m.xOhm = splitZ(zHL/100*zBase, xr)
	}
	return m
}
