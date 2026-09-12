package powerflow

import (
	"context"
	"math"
	"sort"

	"github.com/kwaabs/voltedge/api/internal/crud"
	"github.com/uptrace/bun"
)

// Protection-coordination engine -- POST /analysis/coordination.
//
// Given a source and a fault location on the same radial feeder, it walks the
// series path between them, and for every time-graded protective device on
// that path evaluates how long that device would take to operate for a fault
// at the given location, then checks that each upstream (backup) device clears
// slower than the next downstream (primary) device by at least the
// coordination time interval (CTI). A pair that doesn't is flagged
// miscoordinated -- the backup would trip before (or too close to) the primary,
// tripping more of the network than the fault requires.
//
// It reuses the fault-current substrate wholesale: resolveNode -> buildTree ->
// resolveNodeStates -> accumulateSeriesImpedance give the prospective fault
// current at the fault node, and the same current (referred across any
// transformer between a device and the fault) is the through-current each
// upstream device sees. The only protection-specific inputs are the settings
// Slice B added and the tabulated curves Slice B/migration 0076 catalogued --
// this engine interpolates the stored (current, time) points, it never
// evaluates an IDMT formula.
//
// Device coverage: MV_BREAKER (graded via its PROTECTIVE_RELAY, found through
// the protection_trip relationship), MV_FUSE (inline, via its curve_ref), a
// fused MV_SWITCH / LV_SWITCH (via the same curve_ref when is_fused), and
// MV_RECLOSER (inline, via its slow_curve_ref -- the coordinating time curve --
// falling back to fast_curve_ref). Sectionalizers count fault passages during a
// recloser's dead time rather than time-grade, so they are not part of a
// time-current coordination check -- a natural follow-up.

const (
	classMVBreaker  = "MV_BREAKER"
	classMVFuse     = "MV_FUSE"
	classMVSwitch   = "MV_SWITCH"
	classLVSwitch   = "LV_SWITCH"
	classMVRecloser = "MV_RECLOSER"
)

// recloser curve field names (seed/mv_recloser_example.sql). A recloser carries
// a fast/slow curve pair; coordination grades on the SLOW (delayed) curve -- the
// one that must coordinate with downstream devices -- and falls back to the fast
// curve when only it is set.
const (
	recloserSlowCurveField = "slow_curve_ref"
	recloserFastCurveField = "fast_curve_ref"
)

// switchIsFusedField is the boolean flag on a generic MV_SWITCH / LV_SWITCH that
// marks it as carrying a fuse element. Only a fused switch is graded (via its
// curve_ref, the same field MV_FUSE uses); an unfused switch is a plain
// topological hop with no protection characteristic.
const switchIsFusedField = "is_fused"

// relay IDMT setting field names (seed/protection_curves_example.sql).
const (
	relayCurveTypeField  = "curve_type"
	relayPickupField     = "pickup_current_a"
	relayTimeDialField   = "time_dial"
	relayInstPickupField = "inst_pickup_a"
	relayDefTimeField    = "definite_time_s"
)

// fuse setting field names.
const (
	fuseCurveRefField = "curve_ref"
)

// coordVoltageClassFields are the attribute names a device may carry its own
// nominal operating voltage under, in resolution priority -- a conductor-style
// voltage_class first, then a transformer's primary_voltage (its feeder/high
// side). The same list and intent trace.enrichNode uses; both hold a coded
// VOLTAGE_CLASS value ("11KV"/"400V") parsed by parseVoltageClassCode. A device
// class carrying neither reports no nominal.
var coordVoltageClassFields = []string{"voltage_class", "primary_voltage"}

// enrichDeviceIdentity fills a device's own display extras -- nominal voltage
// (its coded voltage_class / primary_voltage) and circuit membership (circuit_id
// + circuit_ref) -- from the node state already loaded for the coordination
// walk. Mirrors trace.enrichNode so the same three fields surface here. A nil
// state (device row purged mid-run) or a class without these fields leaves them
// nil/empty.
func enrichDeviceIdentity(st *nodeState, dev *coordinationDevice) {
	if st == nil || st.attrs == nil {
		return
	}
	dev.CircuitID, dev.CircuitRef = circuitIdentity(st)
	for _, field := range coordVoltageClassFields {
		raw, _ := st.attrs[field].(string)
		if v, ok := parseVoltageClassCode(raw); ok {
			dev.NominalVoltageV = &v
			break
		}
	}
}

// circuitIdentity pulls a node's stored circuit membership (Phase 37 circuit_id
// + circuit_ref) straight off its already-loaded attributes -- the powerflow
// analog of what trace.enrichNode reads, shared by every analysis in this
// package that labels a node (coordination devices, load-flow and fault-current
// node results). Both are "" when the node's class carries no circuit field or
// the value is unset (SQL NULL -> nil). scanRows normalizes the circuit_id uuid
// to a string, so a set value reads cleanly.
func circuitIdentity(st *nodeState) (id, ref string) {
	if st == nil || st.attrs == nil {
		return "", ""
	}
	id, _ = st.attrs["circuit_id"].(string)
	ref, _ = st.attrs["circuit_ref"].(string)
	return id, ref
}

// curveTypeDefiniteTime is the one curve_type value with no stored points:
// the relay operates after a fixed definite_time_s once picked up.
const curveTypeDefiniteTime = "DEFINITE_TIME"

// defaultCTISeconds is the standard coordination time interval used when the
// request omits one -- 0.3 s is a common relay-relay margin (breaker time +
// relay overshoot + safety). The caller can override per study.
const defaultCTISeconds = 0.3

// coordinationRequest is POST /analysis/coordination's body. Start is the
// source (same radial substrate the fault-current walk uses); Fault is the
// feature a fault is postulated at -- it must lie downstream of Start.
// NominalVoltageV is the pre-fault sending-end voltage, as everywhere else in
// this package. CTISeconds overrides defaultCTISeconds when > 0.
type coordinationRequest struct {
	Start           featureRef `json:"start"`
	Fault           featureRef `json:"fault"`
	NominalVoltageV float64    `json:"nominal_voltage_v"`
	CTISeconds      float64    `json:"cti_seconds"`
}

// coordinationDevice is one time-graded protective device on the source->fault
// path. Role is "primary" for the device nearest the fault and "backup" for
// every device upstream of it (the ordering the pairwise check grades). Order
// is its position on the path, 0 = nearest the fault. OperateTimeS is how long
// it takes to clear a fault at the requested location given ThroughCurrentAmps
// (the fault current referred to this device's own voltage base); it is nil
// when the device has no resolved settings, or when the through-current is at
// or below its pickup so it never operates -- Resolved / Note say which.
type coordinationDevice struct {
	FeatureClass       string       `json:"feature_class"`
	FeatureID          string       `json:"feature_id"`
	Role               string       `json:"role"`
	Order              int          `json:"order"`
	ThroughCurrentAmps float64      `json:"through_current_amps"`
	NominalBaseV       float64      `json:"nominal_base_v"`

	// NominalVoltageV / CircuitID / CircuitRef are the device's own display
	// identity, read straight off its live row -- the same three extras an
	// enriched trace surfaces (trace.enrichNode), included here so a
	// coordination result can be labelled without a second round-trip. Unlike
	// NominalBaseV (the impedance-walk base, which reflects any transformer
	// between this device and the fault), NominalVoltageV is the feature's own
	// coded voltage_class / primary_voltage. All three are nil/empty when the
	// device's class carries no such field or the value is unset.
	NominalVoltageV    *float64     `json:"nominal_voltage_v,omitempty"`
	CircuitID          string       `json:"circuit_id,omitempty"`
	CircuitRef         string       `json:"circuit_ref,omitempty"`

	Curve              string       `json:"curve,omitempty"`
	OperateTimeS       *float64     `json:"operate_time_s,omitempty"`
	Resolved           bool         `json:"resolved"`
	Extrapolated       bool         `json:"extrapolated,omitempty"`
	Note               string       `json:"note,omitempty"`

	// Curves is the device's full time-current characteristic(s) for plotting,
	// sampled as (current, time) points and -- unlike the scalar fields above,
	// which stay on the device's own base -- referred to the run's common base
	// (CommonBaseV, the fault node's) so every device draws on one shared current
	// axis with a single vertical fault-current line. A relay carries one trace
	// (its operate curve, converted from multiples-of-pickup to absolute amps and
	// scaled by time_dial, truncated at any instantaneous pickup); a fuse carries
	// two (minimum-melt and total-clear). Empty when no curve resolved.
	Curves []curveTrace `json:"curves,omitempty"`
}

// curveSample is one plotted (current, time) point on the common base.
type curveSample struct {
	CurrentAmps float64 `json:"current_amps"`
	TimeS       float64 `json:"time_s"`
}

// curveTrace is one labelled polyline of a device's characteristic (e.g. a
// relay "operate" curve, or a fuse's "minimum melt" / "total clear" bounds).
type curveTrace struct {
	Label  string        `json:"label"`
	Points []curveSample `json:"points"`
}

// coordinationPair is one adjacent (downstream primary, upstream backup) pair
// and its grading result. MarginS is t_upstream - t_downstream, both evaluated
// for the same fault. Coordinated is true when MarginS >= the run's CTI. It is
// nil (result omitted) when either device's operate time is unresolved -- there
// is nothing to grade, and reporting false would wrongly imply a failure.
type coordinationPair struct {
	Downstream  featureRef `json:"downstream"`
	Upstream    featureRef `json:"upstream"`
	MarginS     *float64   `json:"margin_s,omitempty"`
	CTISeconds  float64    `json:"cti_seconds"`
	Coordinated *bool      `json:"coordinated,omitempty"`
}

// coordinationResponse is the full result. FaultCurrentAmps is the prospective
// three-phase bolted-fault current at the fault node (on CommonBaseV).
// CommonBaseV is the voltage base every device's Curves and the fault-current
// line are referred to (the fault node's base) -- so the whole chart shares one
// current axis and the fault current is a single vertical line at
// FaultCurrentAmps, which every device's curve crosses at its own operate time.
// Coordinated is the AND of every gradeable pair -- nil when no pair could be
// graded at all (fewer than two devices with resolved times).
type coordinationResponse struct {
	Start            featureRef           `json:"start"`
	Fault            featureRef           `json:"fault"`
	NominalVoltageV  float64              `json:"nominal_voltage_v"`
	CommonBaseV      float64              `json:"common_base_v"`
	CTISeconds       float64              `json:"cti_seconds"`
	FaultCurrentAmps *float64             `json:"fault_current_amps,omitempty"`
	Devices          []coordinationDevice `json:"devices"`
	Pairs            []coordinationPair   `json:"pairs"`
	Coordinated      *bool                `json:"coordinated,omitempty"`
}

// runCoordination is POST /analysis/coordination's single entry point.
func runCoordination(ctx context.Context, db bun.IDB, req coordinationRequest) (*coordinationResponse, error) {
	if req.Start.FeatureClass == "" || req.Start.FeatureID == "" {
		return nil, invalid("start.feature_class and start.feature_id are required")
	}
	if req.Fault.FeatureClass == "" || req.Fault.FeatureID == "" {
		return nil, invalid("fault.feature_class and fault.feature_id are required")
	}
	if req.NominalVoltageV <= 0 {
		return nil, invalid("nominal_voltage_v must be a positive number")
	}
	cti := req.CTISeconds
	if cti <= 0 {
		cti = defaultCTISeconds
	}

	start, err := resolveNode(ctx, db, req.Start)
	if err != nil {
		return nil, err
	}
	fault, err := resolveNode(ctx, db, req.Fault)
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

	// The fault must be a node the walk actually reached (downstream of start,
	// not behind an open device). buildTree only records reached nodes, so a
	// fault that isn't start and has no parent was never on the tree.
	if fault != start {
		if _, ok := tr.parent[fault]; !ok {
			return nil, invalid("fault feature is not downstream of start (it was not reached by a live path -- check the fault lies below start and no open device isolates it)")
		}
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

	allNodes := append(append([]node{}, tr.order...), barrierHits...)
	classCodes, err := nodeClassCodes(ctx, db, allNodes)
	if err != nil {
		return nil, err
	}

	// Prospective fault current at the fault node, on its own base.
	fp := paths[fault]
	faultZ := math.Hypot(fp.rOhm, fp.xOhm)
	var faultCurrent *float64
	if faultZ > 0 {
		i := fp.baseV / faultZ
		faultCurrent = &i
	}

	// The series path source -> fault, ordered fault-first (index 0 = nearest
	// the fault). Every device on it carries the same fault contribution, so
	// its through-current is the fault current referred to that device's base.
	pathNodes := pathToFault(tr, start, fault)

	curves := newCurveCache()
	devices := make([]coordinationDevice, 0)
	for i, n := range pathNodes {
		code := classCodes[n.classID]
		if code != classMVBreaker && code != classMVFuse && code != classMVSwitch && code != classLVSwitch && code != classMVRecloser {
			continue
		}
		dp := paths[n]
		through := 0.0
		if faultCurrent != nil && dp.baseV > 0 {
			// I_device * V_device = I_fault * V_fault (power through a series
			// path is conserved across an ideal transformer).
			through = *faultCurrent * fp.baseV / dp.baseV
		}

		dev := coordinationDevice{
			FeatureClass:       code,
			FeatureID:          n.featureID,
			Order:              i,
			ThroughCurrentAmps: through,
			NominalBaseV:       dp.baseV,
		}
		// Device display identity (own voltage + circuit membership), read from
		// the same node state the eval below uses -- one shared row read, no
		// extra query. states[n] is populated for every reached node, but guard
		// nil in case a device row was purged mid-run.
		enrichDeviceIdentity(states[n], &dev)
		switch code {
		case classMVBreaker:
			evalBreaker(ctx, db, n, through, loader, curves, classCodes, &dev)
		case classMVSwitch, classLVSwitch:
			evalFusedSwitch(ctx, db, n, through, states[n], curves, &dev)
		case classMVRecloser:
			evalRecloser(ctx, db, n, through, states[n], curves, &dev)
		default: // classMVFuse
			evalFuse(ctx, db, n, through, states[n], curves, &dev)
		}

		// eval* builds each device's curve on the device's own base; refer it to
		// the common (fault) base so the whole chart shares one current axis.
		// I_common = I_device · V_device / V_fault.
		if fp.baseV > 0 && dp.baseV > 0 {
			ref := dp.baseV / fp.baseV
			for ti := range dev.Curves {
				for pi := range dev.Curves[ti].Points {
					dev.Curves[ti].Points[pi].CurrentAmps *= ref
				}
			}
		}
		devices = append(devices, dev)
	}

	// Role: the first device that has a resolved operate time is the primary
	// (nearest the fault that actually clears it); everything upstream is
	// backup. Devices with no resolved time still appear but carry no role.
	primaryAssigned := false
	for i := range devices {
		if devices[i].OperateTimeS == nil {
			continue
		}
		if !primaryAssigned {
			devices[i].Role = "primary"
			primaryAssigned = true
		} else {
			devices[i].Role = "backup"
		}
	}

	pairs, overall := gradePairs(devices, cti)

	return &coordinationResponse{
		Start:            req.Start,
		Fault:            req.Fault,
		NominalVoltageV:  req.NominalVoltageV,
		CommonBaseV:      fp.baseV,
		CTISeconds:       cti,
		FaultCurrentAmps: faultCurrent,
		Devices:          devices,
		Pairs:            pairs,
		Coordinated:      overall,
	}, nil
}

// pathToFault returns the series path from start down to fault, ordered
// fault-first (index 0 = the fault node itself, last = start), by walking
// tr.parent up from the fault. A fault == start run returns just [start].
func pathToFault(tr *tree, start, fault node) []node {
	path := []node{fault}
	for cur := fault; cur != start; {
		p, ok := tr.parent[cur]
		if !ok {
			break
		}
		path = append(path, p)
		cur = p
	}
	return path
}

// maxDefiniteMultiple is how far above pickup a definite-time relay's flat
// display line is drawn (25x pickup) -- purely a plotting extent, since a
// definite-time curve has no stored points to bound it.
const maxDefiniteMultiple = 25.0

// evalBreaker fills a breaker device's operate time -- and its display
// characteristic -- from the relay that trips it (protection_trip
// relationship). No relay, or a relay with no usable settings, leaves
// OperateTimeS nil with an explanatory Note. The device-base curve it builds is
// referred to the common base by the caller.
func evalBreaker(ctx context.Context, db bun.IDB, n node, through float64, loader *featureClassLoader, curves *curveCache, classCodes map[string]string, dev *coordinationDevice) {
	relayAttrs, multi, err := relayForBreaker(ctx, db, n, loader)
	if err != nil || relayAttrs == nil {
		dev.Note = "no PROTECTIVE_RELAY linked via a protection_trip relationship"
		return
	}
	if multi {
		dev.Note = "multiple relays trip this breaker; graded on the first"
	}

	pickup, hasPickup := readFloatAttr(relayAttrs, relayPickupField)
	curveType, _ := relayAttrs[relayCurveTypeField].(string)
	inst, okInst := readFloatAttr(relayAttrs, relayInstPickupField)
	hasInst := okInst && inst > 0
	td, okTD := readFloatAttr(relayAttrs, relayTimeDialField)
	if !okTD || td <= 0 {
		td = 1 // graceful default, same spirit as x_r_ratio/power_factor
		if dev.Note == "" {
			dev.Note = "no time_dial set -- plotted/graded at time_dial = 1"
		}
	}

	if !hasPickup || pickup <= 0 || curveType == "" {
		dev.Note = "relay is missing pickup_current_a or curve_type"
		return
	}
	dev.Curve = curveType

	// --- display characteristic (device-base absolute amps/seconds) ---
	if curveType == curveTypeDefiniteTime {
		if dt, ok := readFloatAttr(relayAttrs, relayDefTimeField); ok {
			dev.Curves = []curveTrace{{Label: "definite time", Points: definiteTimeTrace(pickup, dt, inst, hasInst)}}
		}
	} else if pts, err := curves.points(ctx, db, curveType, bandOperate); err == nil && len(pts) > 0 {
		dev.Curves = []curveTrace{{Label: "operate", Points: relayTrace(pts, pickup, td, inst, hasInst)}}
	}

	// --- operate time for THIS fault ---
	// Instantaneous element first: if set and the through-current reaches it,
	// the relay operates with no intentional delay.
	if hasInst && through >= inst {
		t := 0.0
		dev.OperateTimeS = &t
		dev.Resolved = true
		return
	}

	if curveType == curveTypeDefiniteTime {
		dt, ok := readFloatAttr(relayAttrs, relayDefTimeField)
		if !ok {
			dev.Note = "definite-time curve but no definite_time_s set"
			return
		}
		if through <= pickup {
			dev.Note = "through-current at or below pickup -- relay does not operate"
			return
		}
		dev.OperateTimeS = &dt
		dev.Resolved = true
		return
	}

	// Inverse-time: operate time = time_dial * curve(I/pickup), the stored
	// multiple-quantity curve read at the current multiple.
	mult := through / pickup
	if mult <= 1 {
		dev.Note = "through-current at or below pickup -- relay does not operate"
		return
	}
	pts, err := curves.points(ctx, db, curveType, bandOperate)
	if err != nil || len(pts) == 0 {
		dev.Note = "no stored points for curve " + curveType
		return
	}
	base, extrap := interpLogLog(pts, mult)
	t := td * base
	dev.OperateTimeS = &t
	dev.Resolved = true
	dev.Extrapolated = extrap
}

// relayTrace converts a stored multiple-quantity relay curve into device-base
// absolute (amps, seconds): each point's current becomes multiple x pickup and
// its time is scaled by time_dial. When an instantaneous element is set, points
// at or above the instantaneous pickup are dropped -- the inst element takes
// over there, so the drawn inverse curve ends at that knee (its operating point,
// reported separately as operate_time_s = 0, shows the inst region).
func relayTrace(pts []curvePoint, pickup, td, inst float64, hasInst bool) []curveSample {
	out := make([]curveSample, 0, len(pts))
	for _, p := range pts {
		cur := p.current * pickup
		if hasInst && cur >= inst {
			continue
		}
		out = append(out, curveSample{CurrentAmps: cur, TimeS: td * p.timeS})
	}
	return out
}

// definiteTimeTrace draws a definite-time relay as a flat line at its set time,
// from just above pickup out to maxDefiniteMultiple x pickup (or the
// instantaneous pickup, if that is nearer -- the inst element takes over above
// it). time_dial does not scale a definite-time setting.
func definiteTimeTrace(pickup, dt, inst float64, hasInst bool) []curveSample {
	hi := pickup * maxDefiniteMultiple
	if hasInst && inst > pickup && inst < hi {
		hi = inst
	}
	return []curveSample{
		{CurrentAmps: pickup * 1.01, TimeS: dt},
		{CurrentAmps: hi, TimeS: dt},
	}
}

// evalFuse fills a fuse device's operate time from its curve_ref (an
// ampere-quantity curve), grading on the total_clear band -- the conservative
// "fully cleared" time -- and emitting both bounding bands (minimum melt +
// total clear) as display traces. The device-base curves it builds are referred
// to the common base by the caller.
func evalFuse(ctx context.Context, db bun.IDB, n node, through float64, st *nodeState, curves *curveCache, dev *coordinationDevice) {
	if st == nil {
		dev.Note = "fuse feature row could not be loaded"
		return
	}
	curveRef, _ := st.attrs[fuseCurveRefField].(string)
	if curveRef == "" {
		dev.Note = "fuse has no curve_ref set"
		return
	}
	gradeAmpereCurve(ctx, db, curveRef, through, curves, dev)
}

// evalRecloser grades an MV_RECLOSER from its curve pair. A recloser is a
// self-contained protective device: it carries its own ampere-quantity
// characteristic (no separate relay), so grading reuses the same total-clear
// interpolation a fuse uses. It is graded on the SLOW curve -- the delayed
// characteristic that must coordinate with downstream devices -- falling back to
// the FAST curve when only that is set. reclose_attempts / dead-times shape the
// reclosing sequence, not the single-shot operate time this check compares, so
// they don't enter the grading here. The device-base curve is referred to the
// common base by the caller.
func evalRecloser(ctx context.Context, db bun.IDB, n node, through float64, st *nodeState, curves *curveCache, dev *coordinationDevice) {
	if st == nil {
		dev.Note = "recloser feature row could not be loaded"
		return
	}
	curveRef, _ := st.attrs[recloserSlowCurveField].(string)
	if curveRef == "" {
		curveRef, _ = st.attrs[recloserFastCurveField].(string)
	}
	if curveRef == "" {
		dev.Note = "recloser has no slow_curve_ref or fast_curve_ref set"
		return
	}
	gradeAmpereCurve(ctx, db, curveRef, through, curves, dev)
}

// gradeAmpereCurve fills a device's operate time and display characteristic from
// an ampere-quantity curve (a fuse's curve_ref or a recloser's slow/fast curve),
// grading on the total_clear band -- the conservative "fully cleared" time --
// and emitting both bounding bands (minimum melt + total clear) as display
// traces. Shared by evalFuse and evalRecloser. The device-base curves it builds
// are referred to the common base by the caller.
func gradeAmpereCurve(ctx context.Context, db bun.IDB, curveRef string, through float64, curves *curveCache, dev *coordinationDevice) {
	dev.Curve = curveRef

	// Display: the minimum-melt lower bound (when present) and the total-clear
	// upper bound. total_clear is also what the operate time is graded on.
	var traces []curveTrace
	if mm, err := curves.points(ctx, db, curveRef, bandMinMelt); err == nil && len(mm) > 0 {
		traces = append(traces, curveTrace{Label: "minimum melt", Points: fuseTrace(mm)})
	}

	tc, err := curves.points(ctx, db, curveRef, bandTotalClear)
	if err != nil || len(tc) == 0 {
		dev.Note = "no total_clear points for curve " + curveRef
		dev.Curves = traces
		return
	}
	traces = append(traces, curveTrace{Label: "total clear", Points: fuseTrace(tc)})
	dev.Curves = traces

	t, extrap := interpLogLog(tc, through)
	dev.OperateTimeS = &t
	dev.Resolved = true
	dev.Extrapolated = extrap
}

// evalFusedSwitch grades a generic MV_SWITCH / LV_SWITCH. A switch only has a
// protection characteristic when its is_fused flag is set: then it carries a
// fuse element described by the same curve_ref field MV_FUSE uses, and grading
// is identical, so this defers straight to evalFuse. An unfused switch is a
// plain topological hop -- it appears on the path (so the through-current and,
// for an OPEN switch, the barrier still register elsewhere) but contributes no
// operate time to the coordination check.
func evalFusedSwitch(ctx context.Context, db bun.IDB, n node, through float64, st *nodeState, curves *curveCache, dev *coordinationDevice) {
	if st == nil {
		dev.Note = "switch feature row could not be loaded"
		return
	}
	fused, _ := st.attrs[switchIsFusedField].(bool)
	if !fused {
		dev.Note = "switch is not fused -- no protection element to grade"
		return
	}
	evalFuse(ctx, db, n, through, st, curves, dev)
}

// fuseTrace copies an ampere-quantity fuse band's points into display samples
// unchanged -- fuse curves are intrinsic absolute amps/seconds, not scaled by
// any setting.
func fuseTrace(pts []curvePoint) []curveSample {
	out := make([]curveSample, 0, len(pts))
	for _, p := range pts {
		out = append(out, curveSample{CurrentAmps: p.current, TimeS: p.timeS})
	}
	return out
}

// relayForBreaker finds the PROTECTIVE_RELAY that trips a given breaker via a
// protection_trip feature_relationship, and returns its attributes. multi is
// true when more than one relay is linked (main + backup) -- the first is used.
// A nil map (no error) means no relay is linked.
func relayForBreaker(ctx context.Context, db bun.IDB, breaker node, loader *featureClassLoader) (attrs map[string]any, multi bool, err error) {
	var rows []tripRelRow
	if err := db.NewSelect().Model(&rows).
		Join("JOIN metadata.relationship_classes rc ON rc.id = fr.relationship_class_id").
		Where("rc.kind = ?", "protection_trip").
		Where("fr.destination_feature_id = ?", breaker.featureID).
		Where("fr.destination_feature_class_id = ?", breaker.classID).
		OrderExpr("fr.established_at ASC").
		Scan(ctx); err != nil {
		return nil, false, err
	}
	if len(rows) == 0 {
		return nil, false, nil
	}

	relay := node{classID: rows[0].OriginFeatureClassID, featureID: rows[0].OriginFeatureID}
	loaded, err := loader.loadedFor(ctx, db, relay.classID)
	if err != nil {
		return nil, false, err
	}
	row, err := crud.GetFeature(ctx, db, loaded, relay.featureID)
	if err != nil {
		if crud.IsNotFoundError(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	a, _ := row["attributes"].(map[string]any)
	return a, len(rows) > 1, nil
}

// tripRelRow is a minimal read-only projection of runtime.feature_relationships
// (the origin, i.e. the relay, of a protection_trip instance).
type tripRelRow struct {
	bun.BaseModel `bun:"table:runtime.feature_relationships,alias:fr"`

	OriginFeatureClassID string `bun:"origin_feature_class_id"`
	OriginFeatureID      string `bun:"origin_feature_id"`
}

// gradePairs walks the devices in path order (fault-first) and grades each
// adjacent pair with two resolved operate times: the downstream (earlier in the
// slice) is the primary, the upstream (later) the backup, and the margin is
// t_backup - t_primary, which must be >= cti. Only devices that actually
// operate (OperateTimeS != nil) participate, so a non-operating device between
// two graders is skipped rather than breaking the chain. overall is the AND of
// every graded pair, nil when none could be graded.
func gradePairs(devices []coordinationDevice, cti float64) ([]coordinationPair, *bool) {
	// The subsequence of devices that actually operate, in path order.
	operating := make([]coordinationDevice, 0, len(devices))
	for _, d := range devices {
		if d.OperateTimeS != nil {
			operating = append(operating, d)
		}
	}
	// Defensive: operate times should already be non-decreasing away from the
	// fault, but grade on adjacency in path order regardless.
	sort.SliceStable(operating, func(i, j int) bool { return operating[i].Order < operating[j].Order })

	pairs := make([]coordinationPair, 0)
	var overall *bool
	for i := 0; i+1 < len(operating); i++ {
		down := operating[i]
		up := operating[i+1]
		margin := *up.OperateTimeS - *down.OperateTimeS
		coordinated := margin >= cti
		m := margin
		c := coordinated
		pairs = append(pairs, coordinationPair{
			Downstream:  featureRef{FeatureClass: down.FeatureClass, FeatureID: down.FeatureID},
			Upstream:    featureRef{FeatureClass: up.FeatureClass, FeatureID: up.FeatureID},
			MarginS:     &m,
			CTISeconds:  cti,
			Coordinated: &c,
		})
		if overall == nil {
			v := coordinated
			overall = &v
		} else if !coordinated {
			v := false
			overall = &v
		}
	}
	return pairs, overall
}
