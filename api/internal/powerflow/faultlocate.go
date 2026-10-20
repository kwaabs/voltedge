package powerflow

// Fault Location Analysis (distance-to-fault) -- the operational INVERSE of the
// fault-current study (fault.go). The fault-current study answers "if a fault
// were at node N, what current flows?" by accumulating series impedance from the
// source and reporting I = V/|Z| at every node. Fault location runs that in
// reverse: a protective device tripped and the relay/SCADA measured a fault
// current; because the prospective current falls monotonically with
// impedance-from-source, the measured current pins a target impedance, and the
// fault lies on whichever conductor span bracket that target -- at a fraction
// along it we can interpolate into a distance and a map point. A feeder branches,
// so the same electrical distance recurs on several laterals: every bracketing
// span is returned as a candidate, the way a real fault-location tool presents
// "the fault is one of these".
//
// It reuses the fault study's whole substrate -- resolveNode, buildTree,
// resolveNodeStates, sourceFromStart, accumulateSeriesImpedance -- and adds only
// the per-node cumulative length and the bracket-and-interpolate.
//
// SCOPE (S1): three-phase symmetrical, and candidates on the FAULTED VOLTAGE
// LEVEL only (spans upstream of any transformer on their path, where the base
// voltage -- and so the monotonic |Z| -- is constant). SLG location (the common
// distribution fault) needs the zero-sequence path inverted and is the next
// increment.

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"

	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/uptrace/bun"
)

// RunFaultLocationJSON is the agent front door: it unmarshals a tool call's args
// into a fault-location request and runs the engine, so agenttools can expose the
// study without this package exporting its request/response shapes. Its signature
// matches agenttools.Handler, so it is wired as the tool handler directly.
func RunFaultLocationJSON(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var req faultLocationRequest
	if err := json.Unmarshal(args, &req); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	return runFaultLocation(ctx, db, req)
}

// faultLocationRequest is POST /analysis/fault-location's body. Start is the
// feeder SOURCE (the substation bus / transformer secondary feeding the faulted
// section), exactly as the fault-current study's start -- NOT the tripped device,
// whose measured current is compared against the per-node figures downstream of
// it. MeasuredCurrentAmps is what the relay/SCADA recorded.
type faultLocationRequest struct {
	Start               featureRef `json:"start"`
	NominalVoltageV     float64    `json:"nominal_voltage_v"`
	MeasuredCurrentAmps float64    `json:"measured_current_amps"`
	FaultType           string     `json:"fault_type"`    // "three_phase" (default); SLG is a follow-up
	VoltageBasis        string     `json:"voltage_basis"` // line_to_line (default) | line_to_neutral
	// VoltageFactorC scales the pre-fault driving voltage. Unlike the prospective
	// fault-current study (which defaults to the IEC c=1.10 maximum for a rating
	// duty), fault LOCATION back-calculates from a REAL measurement, so it defaults
	// to 1.0 -- the actual pre-fault voltage was ~nominal. Override if you know the
	// pre-fault voltage differed.
	VoltageFactorC float64 `json:"voltage_factor_c"`
}

// faultLocationCandidate is one span the measured current could place the fault
// on: the conductor segment, the feature feeding it (the upstream end), how far
// along it and from the source the fault sits, and -- when the geometry resolves
// -- an interpolated map point.
type faultLocationCandidate struct {
	Segment  featureRef `json:"segment"`  // the faulted conductor span
	Upstream featureRef `json:"upstream"` // the feature feeding that span (its upstream end)

	DistanceFromSourceM   float64 `json:"distance_from_source_m"`
	DistanceAlongSegmentM float64 `json:"distance_along_segment_m"`
	SegmentLengthM        float64 `json:"segment_length_m"`
	FractionAlongSegment  float64 `json:"fraction_along_segment"` // 0 = upstream end, 1 = downstream end

	Longitude *float64 `json:"longitude,omitempty"`
	Latitude  *float64 `json:"latitude,omitempty"`

	// Resolved is false when some impedance along the path to this span did not
	// resolve (a missing material %Z): the distance is then an optimistic estimate,
	// not a trustworthy figure -- the same flag the fault-current study raises.
	Resolved bool `json:"resolved"`
}

// faultLocationResponse is the ranked candidate set plus the diagnostics that
// explain an empty one.
type faultLocationResponse struct {
	Start               featureRef               `json:"start"`
	NominalVoltageV     float64                  `json:"nominal_voltage_v"`
	FaultType           string                   `json:"fault_type"`
	MeasuredCurrentAmps float64                  `json:"measured_current_amps"`
	TargetImpedanceOhm  float64                  `json:"target_impedance_ohm"`
	Candidates          []faultLocationCandidate `json:"candidates"`

	// SourceFaultCurrentAmps is the prospective current right at the source (the
	// ceiling); a measurement above it can't be bracketed. Note explains an empty
	// candidate set.
	SourceFaultCurrentAmps float64 `json:"source_fault_current_amps"`
	Note                   string  `json:"note,omitempty"`
}

// runFaultLocation is POST /analysis/fault-location's entry point.
func runFaultLocation(ctx context.Context, db bun.IDB, req faultLocationRequest) (*faultLocationResponse, error) {
	if req.Start.FeatureClass == "" || req.Start.FeatureID == "" {
		return nil, invalid("start.feature_class and start.feature_id are required")
	}
	if req.MeasuredCurrentAmps <= 0 {
		return nil, invalid("measured_current_amps must be greater than zero")
	}
	if req.NominalVoltageV < 0 {
		return nil, invalid("nominal_voltage_v must not be negative")
	}
	faultType := req.FaultType
	if faultType == "" {
		faultType = faultThreePhase
	}
	if faultType != faultThreePhase {
		return nil, invalid("fault location currently supports only %q (single-line-to-ground location is a follow-up)", faultThreePhase)
	}
	if req.VoltageBasis != "" && req.VoltageBasis != basisLineToLine && req.VoltageBasis != basisLineToNeutral {
		return nil, invalid("voltage_basis must be %q or %q", basisLineToLine, basisLineToNeutral)
	}
	if req.VoltageFactorC < 0 {
		return nil, invalid("voltage_factor_c must not be negative")
	}
	c := req.VoltageFactorC
	if c == 0 {
		c = 1.0 // a real measurement back-calculates at the actual (nominal) pre-fault voltage
	}
	phaseDiv := phaseDivisor(req.VoltageBasis)

	start, err := resolveNode(ctx, db, req.Start)
	if err != nil {
		return nil, err
	}

	barriers, err := loadFlowBarriers(ctx, db)
	if err != nil {
		return nil, err
	}
	loader := newFeatureClassLoader()

	tr, _, err := buildTree(ctx, db, start, barriers, loader, nil, nil)
	if err != nil {
		return nil, err
	}
	states, err := resolveNodeStates(ctx, db, tr, loader)
	if err != nil {
		return nil, err
	}

	sourceR, sourceX, startBaseV, _ := sourceFromStart(states[start], req.NominalVoltageV)
	if startBaseV <= 0 {
		return nil, errNominalRequired()
	}
	paths := accumulateSeriesImpedance(tr, states, start, startBaseV, sourceR, sourceX)
	cumLen := cumulativeLengths(tr, states, start)

	classCodes, err := nodeClassCodes(ctx, db, tr.order)
	if err != nil {
		return nil, err
	}

	// The line-to-neutral driving voltage, and the target impedance the measured
	// current implies: I = V_ln / |Z_total|, so |Z_target| = V_ln / I_measured.
	vLN := c * startBaseV / phaseDiv
	zTarget := vLN / req.MeasuredCurrentAmps

	resp := &faultLocationResponse{
		Start:               req.Start,
		NominalVoltageV:     startBaseV,
		FaultType:           faultType,
		MeasuredCurrentAmps: req.MeasuredCurrentAmps,
		TargetImpedanceOhm:  round4(zTarget),
		Candidates:          []faultLocationCandidate{},
	}

	zMagAt := func(n node) float64 { p := paths[n]; return math.Hypot(p.rOhm, p.xOhm) }
	zSource := zMagAt(start)
	if zSource > 0 {
		resp.SourceFaultCurrentAmps = round2(vLN / zSource)
	}

	// Walk every conductor span on the faulted voltage level and keep the ones
	// whose source-to-span impedance window contains the target.
	var zMaxLevel float64
	for _, n := range tr.order {
		if n == start {
			continue
		}
		st := states[n]
		if st == nil || st.loaded.FeatureClass.NetworkRole != "edge" {
			continue // only conductors carry a locatable length
		}
		p := paths[n]
		if p.baseV != startBaseV {
			continue // below a transformer -- a different base, different level
		}
		par := tr.parent[n]
		pp := paths[par]
		if pp.baseV != startBaseV {
			continue
		}
		zPar := math.Hypot(pp.rOhm, pp.xOhm)
		zChild := math.Hypot(p.rOhm, p.xOhm)
		if zChild > zMaxLevel {
			zMaxLevel = zChild
		}
		if zChild <= zPar {
			continue // zero-length / zero-impedance span: nothing to interpolate on
		}
		if zTarget < zPar || zTarget > zChild {
			continue // the fault isn't on this span
		}

		frac := (zTarget - zPar) / (zChild - zPar)
		segLen := st.impedance.lengthM
		cand := faultLocationCandidate{
			Segment:               featureRef{FeatureClass: classCodes[n.classID], FeatureID: n.featureID},
			Upstream:              featureRef{FeatureClass: classCodes[par.classID], FeatureID: par.featureID},
			DistanceFromSourceM:   round2(cumLen[par] + frac*segLen),
			DistanceAlongSegmentM: round2(frac * segLen),
			SegmentLengthM:        round2(segLen),
			FractionAlongSegment:  round4(frac),
			Resolved:              p.resolved,
		}
		if lon, lat, ok := interpolateFaultPoint(ctx, db, st.loaded, n.featureID, states[par], par.featureID, frac); ok {
			cand.Longitude = &lon
			cand.Latitude = &lat
		}
		resp.Candidates = append(resp.Candidates, cand)
	}

	sort.SliceStable(resp.Candidates, func(i, j int) bool {
		return resp.Candidates[i].DistanceFromSourceM < resp.Candidates[j].DistanceFromSourceM
	})

	if len(resp.Candidates) == 0 {
		switch {
		case zTarget < zMagAt(firstDownstream(tr, states, start)):
			resp.Note = "the measured current is higher than the model's fault level just downstream of the source -- the fault is at or very close to the source, or the source impedance is understated."
		case zTarget > zMaxLevel && zMaxLevel > 0:
			resp.Note = "the measured current is lower than the model's minimum fault level on this feeder -- the fault may be beyond the modeled network, through high impedance (an arcing or ground fault), or on a lateral the model doesn't carry."
		default:
			resp.Note = "no conductor span on the faulted voltage level brackets the measured current; check the source, nominal voltage and fault type."
		}
	}

	return resp, nil
}

// cumulativeLengths is each node's conductor length (metres) from the source,
// summed forward along the tree: a node inherits its parent's running total plus
// its own span length (zero for a non-conductor node). cumLen[parent] is thus the
// distance to a span's UPSTREAM end, the base a fractional position adds onto.
func cumulativeLengths(tr *tree, states map[node]*nodeState, start node) map[node]float64 {
	cum := make(map[node]float64, len(tr.order))
	for _, n := range tr.order {
		if n == start {
			cum[n] = 0
			continue
		}
		add := 0.0
		if st := states[n]; st != nil && st.loaded.FeatureClass.NetworkRole == "edge" {
			add = st.impedance.lengthM
		}
		cum[n] = cum[tr.parent[n]] + add
	}
	return cum
}

// firstDownstream returns the nearest conductor node below the source on the
// faulted level -- the span whose accumulated |Z| is the smallest a fault can
// have, used only to word the "current too high" diagnostic. Falls back to start
// when the source feeds no conductor.
func firstDownstream(tr *tree, states map[node]*nodeState, start node) node {
	best, bestLen := start, math.MaxFloat64
	for _, n := range tr.order {
		st := states[n]
		if n == start || st == nil || st.loaded.FeatureClass.NetworkRole != "edge" {
			continue
		}
		if l := st.impedance.lengthM; l >= 0 && l < bestLen {
			best, bestLen = n, l
		}
	}
	return best
}

// interpolateFaultPoint returns the lon/lat of the fault point `frac` along a
// conductor span, ORIENTED so frac=0 is the span's upstream (parent) end:
// conductor geometry has an arbitrary from/to direction, so the end nearer the
// upstream feature is treated as the start. When the upstream feature has no
// usable point (a geometry-less junction) it falls back to the raw geometry
// direction. Returns ok=false when the conductor has no line geometry.
func interpolateFaultPoint(ctx context.Context, db bun.IDB, condLoaded *metadata.Loaded, condID string, parentState *nodeState, parentID string, frac float64) (lon, lat float64, ok bool) {
	if condLoaded == nil || condLoaded.FeatureClass.PhysicalTable == nil {
		return 0, 0, false
	}
	condTable := *condLoaded.FeatureClass.PhysicalTable

	// Orient against the upstream feature's representative point when it has
	// geometry; otherwise interpolate along the raw geometry direction.
	hasParent := parentState != nil && parentState.loaded != nil &&
		parentState.loaded.FeatureClass.PhysicalTable != nil &&
		parentState.loaded.FeatureClass.GeometryType != "none"

	if hasParent {
		parentTable := *parentState.loaded.FeatureClass.PhysicalTable
		q := fmt.Sprintf(`
WITH ct AS (SELECT ST_Transform(geom, 4326) AS g FROM %s WHERE id = ?),
     pp AS (SELECT ST_Centroid(ST_Transform(geom, 4326)) AS pt FROM %s WHERE id = ?)
SELECT ST_X(ip), ST_Y(ip) FROM (
  SELECT ST_LineInterpolatePoint(ct.g,
    CASE
      WHEN pp.pt IS NULL THEN ?::float8
      WHEN ST_Distance(ST_StartPoint(ct.g), pp.pt) <= ST_Distance(ST_EndPoint(ct.g), pp.pt) THEN ?::float8
      ELSE 1 - ?::float8
    END) AS ip
  FROM ct LEFT JOIN pp ON true
) q WHERE ip IS NOT NULL`, condTable, parentTable)
		var x, y float64
		if err := db.QueryRowContext(ctx, q, condID, parentID, frac, frac, frac).Scan(&x, &y); err == nil {
			return x, y, true
		}
		return 0, 0, false
	}

	q := fmt.Sprintf(`SELECT ST_X(ip), ST_Y(ip) FROM (
  SELECT ST_LineInterpolatePoint(ST_Transform(geom, 4326), ?::float8) AS ip FROM %s WHERE id = ?
) q WHERE ip IS NOT NULL`, condTable)
	var x, y float64
	if err := db.QueryRowContext(ctx, q, frac, condID).Scan(&x, &y); err == nil {
		return x, y, true
	}
	return 0, 0, false
}

func round2(v float64) float64 { return math.Round(v*1e2) / 1e2 }
func round4(v float64) float64 { return math.Round(v*1e4) / 1e4 }
