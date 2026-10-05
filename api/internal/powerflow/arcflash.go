package powerflow

// arcflash.go is Phase 53, Slice S1: the arc-flash hazard engine --
// POST /analysis/arc-flash. It is a DERIVED analysis: it computes almost nothing
// electrical itself, instead standing on the two engines already in this package.
//
//   - The 3-phase fault-current engine (runFaultCurrent) gives, per reached node,
//     the bolted fault current and that node's voltage base -- IEEE 1584's two
//     primary inputs.
//   - The protection-coordination engine's device evaluators (evalBreaker /
//     evalFuse / evalRecloser / evalFusedSwitch) give the upstream protective
//     device's operate time. Arc-flash drives them with the ARCING current
//     (< bolted), not the bolted current, to get the clearing time t.
//
// What is genuinely new here is the IEEE 1584-2002 empirical model (arcing
// current, incident energy, arc-flash boundary) and the equipment geometry it
// needs (electrode gap, enclosure, working distance). S1 supplied only IEEE
// typical values keyed by voltage (Table 3/4 of the standard) so a study runs on
// sparse data. S2 (this slice) adds the DECLARED per-feature geometry fields --
// arc_electrode_configuration / arc_conductor_gap_mm / arc_working_distance_mm /
// arc_system_grounded on the energized equipment classes -- resolved with the
// "Both" precedence used elsewhere in this package: a declared per-feature value
// wins over the study-level request override, which wins over the IEEE typical
// default. Every input that fell back to a typical value is flagged
// (geometry_assumed) rather than silently trusted.
//
// Model: IEEE 1584-2002 (the simpler empirical fit; the 2018 coefficient model is
// a later opt-in slice). Validity range is 0.208-15 kV and 0.7-106 kA of bolted
// fault current; outside it the result is flagged out_of_range. Conservative
// defaults (enclosed box, ungrounded) are chosen where a real input is unknown --
// both maximise the predicted energy, the safe direction for a hazard study.

import (
	"context"
	"math"
	"strings"

	"github.com/uptrace/bun"
)

// arcFlashStandard is the model this slice implements (echoed in the response so
// a later 2018 model can be an opt-in beside it).
const arcFlashStandard = "ieee-1584-2002"

// defaultMaxClearingTimeS caps the clearing time when the upstream device is slow
// or unresolved. IEEE 1584 guidance uses 2 s where a worker cannot self-evacuate.
const defaultMaxClearingTimeS = 2.0

// reducedArcingCurrentFactor is the "arcing current variation" sensitivity of
// IEEE 1584: incident energy is evaluated at the arcing current AND at 85% of it
// (a lower current can sit further out on the TCC and clear slower), and the worse
// of the two governs.
const reducedArcingCurrentFactor = 0.85

// The declared per-feature geometry field names (Slice S2). Each is nullable on
// the energized equipment classes (seed/arc_flash_geometry_fields.sql). The engine
// reads the NAMES off the faulted node's own attributes; an absent value simply
// falls back to the request override or the IEEE typical default, so a study never
// depends on any of these being populated.
const (
	// arc_electrode_configuration -- coded equipment class / electrode arrangement.
	// Determines enclosed-vs-open (the K terms) and the distance exponent x, plus
	// the class's typical gap & working distance. Values: switchgear (default) /
	// mcc_panelboard / cable / open_air (open synonyms accepted, see
	// normalizeElectrodeConfig).
	arcElectrodeConfigField = "arc_electrode_configuration"
	// arc_conductor_gap_mm -- electrode gap in mm; overrides the class typical.
	arcConductorGapField = "arc_conductor_gap_mm"
	// arc_working_distance_mm -- worker working distance in mm; the per-feature
	// override that wins over the request-level WorkingDistanceMM.
	arcWorkingDistanceField = "arc_working_distance_mm"
	// arc_system_grounded -- boolean; true selects K2 = -0.113 (grounded), the
	// LOWER-energy case. Default false (ungrounded) is conservative.
	arcGroundedField = "arc_system_grounded"
)

// arcGeometrySource records where each geometry input came from -- declared on the
// feature, supplied by the request, or defaulted to an IEEE typical value -- so the
// result can honestly flag which inputs were assumed (geometry_assumed) rather than
// engineered.
type arcGeometrySource struct {
	config            string // the resolved electrode configuration (declared or default)
	electrodeDeclared bool
	gapDeclared       bool
	wdSource          string // "declared" | "request" | "default"
	groundedDeclared  bool
}

// assumed is true when any geometry NUMERIC fell back to an IEEE typical value:
// the electrode configuration, the gap, or the working distance was not supplied
// by asset data or the request. (A request-level working distance counts as an
// explicit analyst input, not an assumption. Grounding defaults conservatively and
// is not treated as an "assumed geometry" trigger.)
func (s arcGeometrySource) assumed() bool {
	return !s.electrodeDeclared || !s.gapDeclared || s.wdSource == "default"
}

// arcFlashRequest is POST /analysis/arc-flash's body. Start + NominalVoltageV are
// the same radial substrate every analysis here uses. MaxClearingTimeS overrides
// the 2 s cap; WorkingDistanceMM overrides the per-voltage typical working
// distance for every node (S1's one geometry override -- S2 adds per-feature
// fields).
type arcFlashRequest struct {
	Start             featureRef `json:"start"`
	NominalVoltageV   float64    `json:"nominal_voltage_v"`
	MaxClearingTimeS  float64    `json:"max_clearing_time_s,omitempty"`
	WorkingDistanceMM float64    `json:"working_distance_mm,omitempty"`

	// VoltageBasis is threaded into the underlying fault-current run so the bolted
	// fault current this study consumes is the STANDARD per-phase figure, not a
	// √3-inflated one -- otherwise arcing current, incident energy and the PPE
	// category all inherit the error. Same values as faultCurrentRequest
	// (line_to_line default / line_to_neutral). Note the IEEE 1584 voltage term
	// itself is a line-to-line quantity, so arc-flash assumes NominalVoltageV is
	// line-to-line (the default); declaring line_to_neutral corrects the fault
	// current but NominalBaseV then feeds IEEE 1584 on a line-to-neutral basis.
	VoltageBasis string `json:"voltage_basis"`
}

// arcFlashNodeResult is the per-node hazard result. Currents are amps, energy is
// cal/cm2, distances are mm, time is seconds. The *Assumed / OutOfRange flags say
// which inputs were defaulted vs resolved so a study is never silently trusted.
type arcFlashNodeResult struct {
	FeatureClass string  `json:"feature_class"`
	FeatureID    string  `json:"feature_id"`
	CircuitID    string  `json:"circuit_id,omitempty"`
	CircuitRef   string  `json:"circuit_ref,omitempty"`
	NominalBaseV float64 `json:"nominal_base_v"`

	BoltedFaultCurrentAmps float64     `json:"bolted_fault_current_amps"`
	ArcingCurrentAmps      float64     `json:"arcing_current_amps"`
	ClearingTimeS          float64     `json:"clearing_time_s"`
	ClearingDevice         *featureRef `json:"clearing_device,omitempty"`

	IncidentEnergyCalCm2 float64 `json:"incident_energy_cal_cm2"`
	ArcFlashBoundaryMM   float64 `json:"arc_flash_boundary_mm"`
	PPECategory          int     `json:"ppe_category"` // 0 minimal, 1-4 NFPA 70E, -1 dangerous
	PPELabel             string  `json:"ppe_label"`

	// Geometry actually used (declared per-feature > request override > IEEE
	// typical default; see arcGeometryFor).
	ElectrodeConfiguration string  `json:"electrode_configuration"`
	WorkingDistanceMM      float64 `json:"working_distance_mm"`
	GapMM                  float64 `json:"gap_mm"`
	Enclosed               bool    `json:"enclosed"`
	Grounded               bool    `json:"grounded"`

	ClearingTimeAssumed   bool   `json:"clearing_time_assumed"`             // no device resolved -> capped
	GeometryAssumed       bool   `json:"geometry_assumed"`                  // >=1 geometry input fell back to an IEEE typical value
	ReducedCurrentGoverns bool   `json:"reduced_current_governs,omitempty"` // the 0.85*Ia case was worse
	OutOfRange            bool   `json:"out_of_range,omitempty"`            // V or Ibf outside 1584-2002 validity
	Note                  string `json:"note,omitempty"`
}

// arcFlashResponse is the full study. Nodes carries one hazard result per reached
// node with a resolved bolted fault current; Barriers are the open devices that
// stopped the walk (same as the fault engine reports).
type arcFlashResponse struct {
	Start            featureRef           `json:"start"`
	NominalVoltageV  float64              `json:"nominal_voltage_v"`
	Standard         string               `json:"standard"`
	MaxClearingTimeS float64              `json:"max_clearing_time_s"`
	Nodes            []arcFlashNodeResult `json:"nodes"`
	Barriers         []featureRef         `json:"barriers"`
}

// arcGeometry is the IEEE 1584 equipment geometry for one node: electrode gap,
// enclosed-vs-open, grounded, working distance, and the distance exponent x --
// all defaulted from the node's voltage in S1.
type arcGeometry struct {
	gapMM    float64
	wdMM     float64
	x        float64
	box      bool
	grounded bool
}

// runArcFlash is POST /analysis/arc-flash's single entry point.
func runArcFlash(ctx context.Context, db bun.IDB, req arcFlashRequest) (*arcFlashResponse, error) {
	if req.Start.FeatureClass == "" || req.Start.FeatureID == "" {
		return nil, invalid("start.feature_class and start.feature_id are required")
	}
	if req.NominalVoltageV <= 0 {
		return nil, invalid("nominal_voltage_v must be a positive number")
	}
	capS := req.MaxClearingTimeS
	if capS <= 0 {
		capS = defaultMaxClearingTimeS
	}

	// Bolted fault current + per-node voltage base -- authoritative (includes any
	// embedded-generation infeed), identical to what /analysis/fault-current
	// reports for the same node, so the two analyses never disagree.
	fault, err := runFaultCurrent(ctx, db, faultCurrentRequest{
		Start:           req.Start,
		NominalVoltageV: req.NominalVoltageV,
		FaultType:       faultThreePhase,
		VoltageBasis:    req.VoltageBasis,
	})
	if err != nil {
		return nil, err
	}

	// Independent walk of the same substrate for clearing-time evaluation (the
	// coordination engine's device evaluators need the tree/paths/states, which
	// the fault response doesn't carry). A deliberate second walk, cheap, keeps
	// the fault and coordination engines untouched -- the same trade the dispatch
	// router (dispatch.go) makes.
	start, err := resolveNode(ctx, db, req.Start)
	if err != nil {
		return nil, err
	}
	barriers, err := loadFlowBarriers(ctx, db)
	if err != nil {
		return nil, err
	}
	loader := newFeatureClassLoader()
	tr, barrierHits, err := buildTree(ctx, db, start, barriers, loader, nil, nil)
	if err != nil {
		return nil, err
	}
	states, err := resolveNodeStates(ctx, db, tr, loader)
	if err != nil {
		return nil, err
	}
	var sourceR, sourceX float64
	if startState := states[start]; startState != nil {
		sourceR, sourceX = resolveSourceImpedance(startState.attrs)
	}
	paths := accumulateSeriesImpedance(tr, states, start, req.NominalVoltageV, sourceR, sourceX)
	classCodes, err := nodeClassCodes(ctx, db, append(append([]node{}, tr.order...), barrierHits...))
	if err != nil {
		return nil, err
	}
	curves := newCurveCache()

	// Fault-result nodes are the reached tree nodes; index them so each hazard
	// point can find its own node for the upstream-device walk.
	nodeByID := make(map[string]node, len(tr.order))
	for _, n := range tr.order {
		nodeByID[n.featureID] = n
	}

	results := make([]arcFlashNodeResult, 0, len(fault.Nodes))
	for _, fn := range fault.Nodes {
		if fn.FaultCurrentAmps == nil || *fn.FaultCurrentAmps <= 0 {
			continue // no resolved fault current -> no arc-flash number
		}
		ibfA := *fn.FaultCurrentAmps
		v := fn.NominalBaseV

		fnode, ok := nodeByID[fn.FeatureID]
		if !ok {
			continue // defensive: fault node not on the coordination tree
		}

		// Geometry from the faulted node's OWN declared fields (S2), falling back
		// to the request override then the IEEE typical default. Absent fields are
		// harmless -- readFloatAttr / the string cast simply report "not set".
		var nodeAttrs map[string]any
		if st := states[fnode]; st != nil {
			nodeAttrs = st.attrs
		}
		geom, gsrc := arcGeometryFor(v, nodeAttrs, req.WorkingDistanceMM)

		// Arcing current, then clearing time at Ia and at 0.85*Ia; the worse
		// incident energy governs (IEEE 1584 arcing-current variation).
		ia := arcingCurrentAmps(ibfA, v, geom)
		t1, dev1, res1 := arcClearingTime(ctx, db, tr, states, paths, classCodes, loader, curves, start, fnode, ia, capS)
		e1, afb1 := incidentEnergyAndBoundary(ia, v, t1, geom)

		t2, dev2, res2 := arcClearingTime(ctx, db, tr, states, paths, classCodes, loader, curves, start, fnode, reducedArcingCurrentFactor*ia, capS)
		e2, afb2 := incidentEnergyAndBoundary(reducedArcingCurrentFactor*ia, v, t2, geom)

		// Governing case = worse (higher) incident energy.
		eCal, afb, t, dev, resolved := e1, afb1, t1, dev1, res1
		reducedGoverns := false
		if e2 > e1 {
			eCal, afb, t, dev, resolved = e2, afb2, t2, dev2, res2
			reducedGoverns = true
		}

		cat, label := ppeCategory(eCal)
		outOfRange, rangeNote := arcRangeCheck(v, ibfA)

		res := arcFlashNodeResult{
			FeatureClass:           fn.FeatureClass,
			FeatureID:              fn.FeatureID,
			CircuitID:              fn.CircuitID,
			CircuitRef:             fn.CircuitRef,
			NominalBaseV:           v,
			BoltedFaultCurrentAmps: ibfA,
			ArcingCurrentAmps:      ia,
			ClearingTimeS:          t,
			ClearingDevice:         dev,
			IncidentEnergyCalCm2:   eCal,
			ArcFlashBoundaryMM:     afb,
			PPECategory:            cat,
			PPELabel:               label,
			ElectrodeConfiguration: gsrc.config,
			WorkingDistanceMM:      geom.wdMM,
			GapMM:                  geom.gapMM,
			Enclosed:               geom.box,
			Grounded:               geom.grounded,
			ClearingTimeAssumed:    !resolved,
			GeometryAssumed:        gsrc.assumed(),
			ReducedCurrentGoverns:  reducedGoverns,
			OutOfRange:             outOfRange,
		}
		switch {
		case !resolved && outOfRange:
			res.Note = "no upstream protective device resolved (clearing time capped); " + rangeNote
		case !resolved:
			res.Note = "no upstream protective device resolved -- clearing time capped at the maximum"
		case outOfRange:
			res.Note = rangeNote
		}
		results = append(results, res)
	}

	return &arcFlashResponse{
		Start:            req.Start,
		NominalVoltageV:  req.NominalVoltageV,
		Standard:         arcFlashStandard,
		MaxClearingTimeS: capS,
		Nodes:            results,
		Barriers:         fault.Barriers,
	}, nil
}

// arcClearingTime returns the operate time of the upstream primary protective
// device for a fault at faultNode carrying faultNodeAmps at that node's base --
// the first device on the fault->source path (nearest the fault) that resolves an
// operate time, evaluated at the given current. It reuses the coordination
// engine's device evaluators unchanged, feeding them the ARCING current referred
// to each device's own base. When no device resolves, the clearing time is capped
// and resolved=false (the caller flags it). This mirrors runCoordination's
// primary-device selection but with the arcing current, and without grading.
func arcClearingTime(ctx context.Context, db bun.IDB, tr *tree, states map[node]*nodeState, paths map[node]pathImpedance, classCodes map[string]string, loader *featureClassLoader, curves *curveCache, start, faultNode node, faultNodeAmps, capS float64) (t float64, dev *featureRef, resolved bool) {
	fp := paths[faultNode]
	for _, n := range pathToFault(tr, start, faultNode) {
		code := classCodes[n.classID]
		if code != classMVBreaker && code != classMVFuse && code != classMVSwitch && code != classLVSwitch && code != classMVRecloser {
			continue
		}
		dp := paths[n]
		through := 0.0
		if fp.baseV > 0 && dp.baseV > 0 {
			// Refer the fault-point current to this device's base (power through a
			// series path is conserved across an ideal transformer).
			through = faultNodeAmps * fp.baseV / dp.baseV
		}

		var d coordinationDevice
		switch code {
		case classMVBreaker:
			evalBreaker(ctx, db, n, through, loader, curves, classCodes, &d)
		case classMVSwitch, classLVSwitch:
			evalFusedSwitch(ctx, db, n, through, states[n], curves, &d)
		case classMVRecloser:
			evalRecloser(ctx, db, n, through, states[n], curves, &d)
		default: // classMVFuse
			evalFuse(ctx, db, n, through, states[n], curves, &d)
		}
		if d.OperateTimeS != nil {
			ot := *d.OperateTimeS
			if ot > capS {
				ot = capS
			}
			return ot, &featureRef{FeatureClass: code, FeatureID: n.featureID}, true
		}
	}
	return capS, nil, false
}

// arcGeometryFor resolves the IEEE 1584 equipment geometry for one node from its
// own declared fields (S2), the study-level request override, and the IEEE typical
// defaults, in that precedence:
//
//	electrode configuration : arc_electrode_configuration field   -> switchgear
//	electrode gap           : arc_conductor_gap_mm field          -> class/voltage typical
//	working distance        : arc_working_distance_mm field       -> request WD -> class/voltage typical
//	grounding               : arc_system_grounded field           -> false (ungrounded, conservative)
//
// The typical defaults are conservative where a real input is unknown: an enclosed
// (box) switchgear enclosure and ungrounded system both maximise the predicted
// incident energy, the safe direction for a hazard study. arcGeometrySource records
// which inputs were declared vs defaulted so the result can flag geometry_assumed.
// A nil attrs map (node class carries none of these fields) is fine -- everything
// falls back to the request override / typical default.
func arcGeometryFor(vVolts float64, attrs map[string]any, requestWorkingDistanceMM float64) (arcGeometry, arcGeometrySource) {
	vkV := vVolts / 1000
	var src arcGeometrySource

	// Electrode configuration -> box, distance exponent x, and the class's typical
	// gap & working distance. Defaults to enclosed switchgear (conservative and the
	// most common MV/LV work location) when undeclared.
	config := "switchgear"
	if raw, ok := attrs[arcElectrodeConfigField].(string); ok && strings.TrimSpace(raw) != "" {
		config = normalizeElectrodeConfig(raw)
		src.electrodeDeclared = true
	}
	src.config = config
	g := typicalGeometryFor(config, vkV)

	// Grounding: conservative ungrounded (K2 = 0) unless declared.
	g.grounded = false
	if raw, ok := attrs[arcGroundedField].(bool); ok {
		g.grounded = raw
		src.groundedDeclared = true
	}

	// Gap: declared per-feature value wins over the class typical.
	if gap, ok := readFloatAttr(attrs, arcConductorGapField); ok && gap > 0 {
		g.gapMM = gap
		src.gapDeclared = true
	}

	// Working distance precedence: declared per-feature > request override >
	// class/voltage typical. Apply the request first, then let a declared value
	// override it, so the asset's own recorded geometry always wins.
	src.wdSource = "default"
	if requestWorkingDistanceMM > 0 {
		g.wdMM = requestWorkingDistanceMM
		src.wdSource = "request"
	}
	if wd, ok := readFloatAttr(attrs, arcWorkingDistanceField); ok && wd > 0 {
		g.wdMM = wd
		src.wdSource = "declared"
	}

	return g, src
}

// typicalGeometryFor returns the IEEE 1584-2002 Table 4 typical gap, distance
// exponent x, enclosed-vs-open, and working distance for an equipment class at
// voltage vkV. The "switchgear" branch reproduces S1's voltage-band defaults
// exactly, so an unpopulated network's results are unchanged from S1.
func typicalGeometryFor(config string, vkV float64) arcGeometry {
	var g arcGeometry
	switch config {
	case "open_air": // conductors in open air -- no enclosure focusing the arc
		g.box = false
		g.x = 2.000
		switch {
		case vkV <= 1.0:
			g.gapMM, g.wdMM = 40, 610
		case vkV <= 5.0:
			g.gapMM, g.wdMM = 104, 910
		default:
			g.gapMM, g.wdMM = 153, 910
		}
	case "cable": // cable termination / junction
		g.box = false
		g.x = 2.000
		g.gapMM, g.wdMM = 13, 455
	case "mcc_panelboard": // motor control centres & panelboards (LV)
		g.box = true
		g.x = 1.641
		g.gapMM, g.wdMM = 25, 455
	default: // "switchgear" -- also the conservative fallback (matches S1)
		g.box = true
		switch {
		case vkV <= 1.0: // LV switchgear
			g.gapMM, g.x, g.wdMM = 32, 1.473, 610
		case vkV <= 5.0: // 5 kV switchgear
			g.gapMM, g.x, g.wdMM = 104, 0.973, 910
		default: // 15 kV switchgear (and above -- flagged out_of_range at report time)
			g.gapMM, g.x, g.wdMM = 153, 0.973, 910
		}
	}
	return g
}

// normalizeElectrodeConfig folds a declared arc_electrode_configuration value (or a
// looser synonym) onto one of the four canonical equipment classes the typical
// table keys on. Unknown values fall back to switchgear (the conservative, enclosed
// default). The IEEE 1584-2018 five-electrode codes (VCB/VCBB/HCB/VOA/HOA) are
// accepted here and mapped to their 2002 open-vs-box equivalent so a field
// populated for a later 2018 run still reads sensibly under the 2002 model.
func normalizeElectrodeConfig(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "open_air", "open", "voa", "hoa", "voc", "hoc":
		return "open_air"
	case "cable":
		return "cable"
	case "mcc_panelboard", "mcc", "panelboard", "panel":
		return "mcc_panelboard"
	default: // switchgear, vcb, vcbb, hcb, box, enclosed, ...
		return "switchgear"
	}
}

// arcingCurrentAmps applies IEEE 1584-2002 equations 1 (V < 1 kV, electrode-config
// dependent) and 2 (V >= 1 kV) to the bolted fault current. Inputs are amps and
// volts; the equations work in kA and kV internally and the result is returned in
// amps.
func arcingCurrentAmps(ibfA, vVolts float64, g arcGeometry) float64 {
	ibfkA := ibfA / 1000
	if ibfkA <= 0 {
		return 0
	}
	vkV := vVolts / 1000
	lgIbf := math.Log10(ibfkA)
	var lgIa float64
	if vkV < 1.0 {
		k := -0.153 // open air
		if g.box {
			k = -0.097 // enclosed
		}
		lgIa = k + 0.662*lgIbf + 0.0966*vkV + 0.000526*g.gapMM +
			0.5588*vkV*lgIbf - 0.00304*g.gapMM*lgIbf
	} else {
		lgIa = 0.00402 + 0.983*lgIbf
	}
	return math.Pow(10, lgIa) * 1000 // kA -> A
}

// incidentEnergyAndBoundary applies IEEE 1584-2002 equations 3-5: the normalized
// incident energy (eq 3), the incident energy at the working distance (eq 4,
// reported in cal/cm2), and the arc-flash boundary (eq 5, the distance at which
// the energy falls to 1.2 cal/cm2). Verified against the standard's worked example
// (0.48 kV, 20 kA, box, grounded, G=32, D=457 -> ~7.29 cal/cm2, ~1555 mm).
func incidentEnergyAndBoundary(iaA, vVolts, tS float64, g arcGeometry) (eCal, afbMM float64) {
	iakA := iaA / 1000
	if iakA <= 0 || g.x <= 0 || g.wdMM <= 0 {
		return 0, 0
	}
	vkV := vVolts / 1000
	lgIa := math.Log10(iakA)
	k1 := -0.792 // open air
	if g.box {
		k1 = -0.555 // enclosed
	}
	k2 := 0.0 // ungrounded / high-resistance grounded
	if g.grounded {
		k2 = -0.113 // grounded
	}
	lgEn := k1 + k2 + 1.081*lgIa + 0.0011*g.gapMM
	en := math.Pow(10, lgEn) // normalized incident energy (0.2 s, 610 mm)

	cf := 1.0 // > 1 kV
	if vkV <= 1.0 {
		cf = 1.5
	}
	// The 4.184 J/cal factors of eq 4/5 cancel when reporting in cal/cm2, leaving
	// E = Cf * En * (t/0.2) * (610/D)^x.
	factor := cf * en * (tS / 0.2)
	eCal = factor * math.Pow(610.0/g.wdMM, g.x)
	if factor > 0 {
		afbMM = 610.0 * math.Pow(factor/1.2, 1.0/g.x)
	}
	return eCal, afbMM
}

// ppeCategory maps incident energy (cal/cm2) to an NFPA 70E arc-flash PPE
// category: 0 minimal (< 1.2), 1-4 by the standard thresholds, and -1 for
// "dangerous -- no live work" above 40 cal/cm2.
func ppeCategory(eCal float64) (int, string) {
	switch {
	case eCal < 1.2:
		return 0, "Minimal (< 1.2 cal/cm²)"
	case eCal <= 4:
		return 1, "Category 1"
	case eCal <= 8:
		return 2, "Category 2"
	case eCal <= 25:
		return 3, "Category 3"
	case eCal <= 40:
		return 4, "Category 4"
	default:
		return -1, "Dangerous — no live work (> 40 cal/cm²)"
	}
}

// arcRangeCheck flags a node whose voltage or bolted fault current falls outside
// the IEEE 1584-2002 validity envelope (0.208-15 kV, 0.7-106 kA); the result is
// still computed by extrapolation but marked.
func arcRangeCheck(vVolts, ibfA float64) (bool, string) {
	vkV := vVolts / 1000
	ibfkA := ibfA / 1000
	switch {
	case vkV < 0.208:
		return true, "voltage below IEEE 1584-2002 range (0.208-15 kV) -- result extrapolated"
	case vkV > 15:
		return true, "voltage above IEEE 1584-2002 range (0.208-15 kV) -- result extrapolated"
	case ibfkA < 0.7:
		return true, "bolted fault current below IEEE 1584-2002 range (0.7-106 kA) -- result extrapolated"
	case ibfkA > 106:
		return true, "bolted fault current above IEEE 1584-2002 range (0.7-106 kA) -- result extrapolated"
	}
	return false, ""
}
