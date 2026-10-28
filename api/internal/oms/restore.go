package oms

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/kwaabs/voltedge/api/internal/crud"
	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/kwaabs/voltedge/api/internal/powerflow"
	"github.com/kwaabs/voltedge/api/internal/trace"
	"github.com/uptrace/bun"
)

// FLISR -- Fault Location, Isolation & Service Restoration (the restoration half).
// Given an open/faulted protective device, this suggests how to RESTORE the
// section it de-energises by back-feeding it from an alternate source through a
// normally-open tie. It is the Oracle-NMS-style "suggested switching to restore"
// capability, composed entirely from primitives this app already has:
//
//   - the LIVE de-energised cone below the device (trace.DeEnergizedCone), which
//     reads real device_state so it is the section that is actually dark now;
//   - the normally-open TIES bordering that dead cone -- a switch whose class
//     carries a normal_state field offering NORMALLY_OPEN and whose own live
//     normal_state is NORMALLY_OPEN. Closing one bridges the dead section to
//     whatever is on its other side;
//   - a liveness check on that other side (trace.NetworkDeEnergized) so a tie is
//     only suggested when its far side is genuinely energised, not dead from the
//     same (or another) outage;
//   - the alternate SOURCE the far side traces up to (trace.UpstreamFeaturesOfClass
//     against the protective-device classes) and that feeder's present load
//     (trace.DownstreamLoadLeaves);
//   - the load to be picked up (trace.SummarizeService over the dead cone) and a
//     best-effort capacity check against the alternate transformer's nameplate.
//
// This slice SUGGESTS only -- it writes nothing and flips no switch. Turning a
// chosen plan into an executable close-tie switching order is a separate step
// (Slice 2, GenerateSwitchingOrder's restore half).

// restorationCapacityClasses are the classes whose nameplate rating bounds an
// alternate feeder's spare capacity -- the supplying transformer. A package var
// (like ProtectiveDeviceClasses) so a deployment can override it. The nearest one
// upstream of the tie's live side gives the kVA the back-feed must fit within.
var restorationCapacityClasses = []string{"POWER_XFMR", "DIST_XFMR"}

// Capacity verdicts for a restoration plan.
const (
	// CapacityOK -- the alternate feeder has the spare capacity to pick up the dead
	// cone's load (restored + present load fits the supplying transformer's rating).
	CapacityOK = "ok"
	// CapacityExceeds -- the back-feed would overload the alternate feeder.
	CapacityExceeds = "exceeds"
	// CapacityUnverified -- no nameplate rating was found on the alternate feeder, so
	// the fit can't be judged here. The load numbers are still reported; a later
	// slice runs a real load-flow thermal/voltage check (Slice 4).
	CapacityUnverified = "unverified"
)

// RestorationPlan is one suggested back-feed: close Tie to re-energise the dead
// cone from AlternateSource. Every figure is read-only analytics over the current
// network -- nothing is switched.
type RestorationPlan struct {
	// Tie is the normally-open switch to close. TieLabel is its short human tag.
	Tie      trace.FeatureRef `json:"tie"`
	TieLabel string           `json:"tie_label"`
	// AlternateSource is the protective device (feeder head) the tie's live side is
	// fed from -- the feeder that would carry the restored load. Nil when the live
	// side reaches no modelled source (the back-feed origin is then unknown and the
	// plan is reported capacity-unverified).
	AlternateSource      *trace.FeatureRef `json:"alternate_source,omitempty"`
	AlternateSourceLabel string            `json:"alternate_source_label,omitempty"`

	// Restored* is the dead-cone load this plan would bring back.
	RestoredServicePoints int     `json:"restored_service_points"`
	RestoredCustomers     int     `json:"restored_customers"`
	RestoredLoadKW        float64 `json:"restored_load_kw"`
	RestoredLoadKVA       float64 `json:"restored_load_kva"`

	// AlternateFeederLoadKVA is the alternate feeder's present (topological) load --
	// what it already carries before picking up the restored section.
	AlternateFeederLoadKVA float64 `json:"alternate_feeder_load_kva"`
	// AlternateCapacityKVA is the supplying transformer's nameplate, when found.
	AlternateCapacityKVA *float64 `json:"alternate_capacity_kva,omitempty"`
	// SpareHeadroomKVA is capacity - present load (the room before the back-feed).
	SpareHeadroomKVA *float64 `json:"spare_headroom_kva,omitempty"`
	// CapacityVerdict is one of CapacityOK / CapacityExceeds / CapacityUnverified.
	CapacityVerdict string `json:"capacity_verdict"`
	// VerdictBasis says how CapacityVerdict was decided: "load_flow" (a real sweep
	// of the donor feeder with the tie closed -- authoritative), "nameplate" (the
	// summed-demand-vs-transformer-rating estimate), or "none" (neither available).
	VerdictBasis string `json:"verdict_basis"`

	// --- load-flow check (Slice 4), present when VerdictBasis == "load_flow" ---
	// LoadFlowChecked is true when the contingency sweep ran for this tie.
	LoadFlowChecked bool `json:"load_flow_checked"`
	// LoadFlowFeasible is true when the sweep restored load with no voltage
	// violation and no thermal overload -- a transfer the operator can make as-is.
	LoadFlowFeasible bool `json:"load_flow_feasible"`
	// Violations / Overloaded count the post-transfer undervoltage/overvoltage nodes
	// and the overloaded branches/transformers the sweep found.
	Violations int `json:"violations"`
	Overloaded int `json:"overloaded"`
	// WorstVoltagePu is the lowest per-unit voltage reached on the donor feeder once
	// the section transfers (nil when the sweep found no judgeable node).
	WorstVoltagePu *float64 `json:"worst_voltage_pu,omitempty"`
	// LoadFlowNote carries the engine's explanation when the sweep was inconclusive
	// (a loop/mesh tie, no load gained, or a sweep error degraded to nameplate).
	LoadFlowNote string `json:"load_flow_note,omitempty"`
}

// Verdict basis values (how a plan's CapacityVerdict was decided).
const (
	VerdictBasisLoadFlow  = "load_flow"
	VerdictBasisNameplate = "nameplate"
	VerdictBasisNone      = "none"
)

// RestorationSuggestion is the whole answer for one faulted device: the dead
// section's totals plus every viable back-feed plan, best first.
type RestorationSuggestion struct {
	Device trace.FeatureRef `json:"device"`

	DeEnergizedServicePoints int     `json:"de_energized_service_points"`
	DeEnergizedCustomers     int     `json:"de_energized_customers"`
	DeEnergizedLoadKW        float64 `json:"de_energized_load_kw"`
	DeEnergizedLoadKVA       float64 `json:"de_energized_load_kva"`

	Plans []RestorationPlan `json:"plans"`
	// Note explains an empty Plans list (no dead load, or no viable tie), so the
	// caller can tell "nothing to restore" from "no restoration path found".
	Note string `json:"note,omitempty"`
}

// SuggestRestorationForIncident resolves an incident to its blamed device and
// suggests restoration for it. An incident with no attributed device is a clear
// 400 (attribute it first), matching GenerateSwitchingOrder.
func SuggestRestorationForIncident(ctx context.Context, db bun.IDB, incidentID string) (*RestorationSuggestion, error) {
	inc, err := GetIncident(ctx, db, incidentID)
	if err != nil {
		return nil, err
	}
	if inc.PredictedDeviceClass == nil || inc.PredictedDeviceID == nil {
		return nil, invalidf("incident %q has no device to restore around; attribute it first", incidentID)
	}
	return SuggestRestoration(ctx, db, trace.FeatureRef{
		FeatureClass: *inc.PredictedDeviceClass,
		FeatureID:    *inc.PredictedDeviceID,
	})
}

// SuggestRestoration is the FLISR engine entry point: the ranked back-feed plans
// for the section de-energised below device. Read-only.
func SuggestRestoration(ctx context.Context, db bun.IDB, device trace.FeatureRef) (*RestorationSuggestion, error) {
	out := &RestorationSuggestion{Device: device, Plans: []RestorationPlan{}}

	// 1. The section that is dark right now, below the device. DeEnergizedCone reads
	//    live device_state, so it is the real present extent -- and it includes the
	//    open ties at its boundary (an open device is a barrier the walk reaches but
	//    does not cross), which is exactly where a back-feed can enter.
	cone, err := trace.DeEnergizedCone(ctx, db, device)
	if err != nil {
		return nil, err
	}
	if len(cone) == 0 {
		out.Note = "nothing is de-energized below this device"
		return out, nil
	}

	coneIDs := make(map[string]bool, len(cone))
	for _, f := range cone {
		coneIDs[f.FeatureID] = true
	}

	// Dead-cone load -- the consumption this restoration would bring back.
	deadSPs := loadLeafRefs(cone)
	deadRollup, err := trace.SummarizeService(ctx, db, deadSPs)
	if err != nil {
		return nil, fmt.Errorf("oms: rolling up de-energized load: %w", err)
	}
	out.DeEnergizedServicePoints = deadRollup.ServicePoints
	out.DeEnergizedCustomers = deadRollup.Customers
	out.DeEnergizedLoadKW = deadRollup.DemandKW
	out.DeEnergizedLoadKVA = deadRollup.DemandKVA

	// 2. The normally-open ties sitting in the dead cone -- restoration candidates.
	tieClasses, err := tieCapableClassCodes(ctx, db)
	if err != nil {
		return nil, err
	}
	ties := make([]trace.FeatureRef, 0, 4)
	seenTie := map[string]bool{}
	for _, f := range cone {
		if !tieClasses[f.FeatureClass] || seenTie[f.FeatureID] {
			continue
		}
		isTie, err := isNormallyOpenTie(ctx, db, f.FeatureClass, f.FeatureID)
		if err != nil {
			return nil, err
		}
		if isTie {
			seenTie[f.FeatureID] = true
			ties = append(ties, f)
		}
	}
	if len(ties) == 0 {
		out.Note = "no normally-open tie borders the de-energized section, so there is no alternate-source restoration path"
		return out, nil
	}

	// 3-5. For each tie, confirm its far side is live and build a plan.
	for _, tie := range ties {
		plan, ok, err := planForTie(ctx, db, tie, coneIDs, deadRollup)
		if err != nil {
			return nil, err
		}
		if ok {
			out.Plans = append(out.Plans, *plan)
		}
	}

	if len(out.Plans) == 0 {
		out.Note = "ties border the de-energized section, but none has an energized alternate source to back-feed from"
		return out, nil
	}

	rankRestorationPlans(out.Plans)
	return out, nil
}

// planForTie builds a restoration plan for one tie, or reports ok=false when the
// tie has no energized far side to back-feed from.
func planForTie(ctx context.Context, db bun.IDB, tie trace.FeatureRef, coneIDs map[string]bool, deadRollup *trace.ServiceRollup) (*RestorationPlan, bool, error) {
	// The tie's neighbours that are NOT in the dead cone -- its live side.
	neighbors, err := connectedNeighbors(ctx, db, tie.FeatureID)
	if err != nil {
		return nil, false, err
	}
	liveSide := make([]trace.FeatureRef, 0, len(neighbors))
	for _, nb := range neighbors {
		if !coneIDs[nb.FeatureID] {
			liveSide = append(liveSide, nb)
		}
	}
	if len(liveSide) == 0 {
		return nil, false, nil
	}

	// Keep only the genuinely energised ones -- a far side that is itself dead
	// (another outage, or the same cone reached around a ring) can't restore anything.
	dead, err := trace.NetworkDeEnergized(ctx, db, liveSide)
	if err != nil {
		return nil, false, err
	}
	deadSet := make(map[string]bool, len(dead))
	for _, d := range dead {
		deadSet[d.FeatureID] = true
	}
	energized := liveSide[:0]
	for _, l := range liveSide {
		if !deadSet[l.FeatureID] {
			energized = append(energized, l)
		}
	}
	if len(energized) == 0 {
		return nil, false, nil
	}

	plan := &RestorationPlan{
		Tie:                   tie,
		TieLabel:              deviceRefLabel(tie.FeatureClass, tie.FeatureID),
		RestoredServicePoints: deadRollup.ServicePoints,
		RestoredCustomers:     deadRollup.Customers,
		RestoredLoadKW:        deadRollup.DemandKW,
		RestoredLoadKVA:       deadRollup.DemandKVA,
		CapacityVerdict:       CapacityUnverified,
	}

	// The alternate source: the protective devices upstream of the energised far
	// side. The nearest is the feeder head that would protect the restored section
	// (the plan's named source); the farthest is the feeder's grid source -- the
	// slack a donor sweep must start from so backbone loading is included. The first
	// far side that reaches any names the feeder.
	plan.VerdictBasis = VerdictBasisNone
	for _, l := range energized {
		ups, uerr := trace.UpstreamFeaturesOfClass(ctx, db, l, ProtectiveDeviceClasses)
		if uerr != nil {
			return nil, false, uerr
		}
		if len(ups) == 0 {
			continue
		}
		src := ups[0]
		donorHead := ups[len(ups)-1]
		plan.AlternateSource = &src
		plan.AlternateSourceLabel = deviceRefLabel(src.FeatureClass, src.FeatureID)

		// Nameplate estimate: present load the alternate feeder already carries
		// (topological downstream of its head) vs the supplying transformer's rating.
		if _, altRollup, lerr := trace.DownstreamLoadLeaves(ctx, db, src); lerr != nil {
			return nil, false, lerr
		} else if altRollup != nil {
			plan.AlternateFeederLoadKVA = altRollup.DemandKVA
		}
		cap, cerr := nearestCapacityKVA(ctx, db, l)
		if cerr != nil {
			return nil, false, cerr
		}
		if cap != nil {
			plan.AlternateCapacityKVA = cap
			headroom := *cap - plan.AlternateFeederLoadKVA
			plan.SpareHeadroomKVA = &headroom
			if plan.RestoredLoadKVA <= headroom {
				plan.CapacityVerdict = CapacityOK
			} else {
				plan.CapacityVerdict = CapacityExceeds
			}
			plan.VerdictBasis = VerdictBasisNameplate
		}

		// Slice 4: supersede the nameplate estimate with a real load-flow sweep of
		// the donor feeder with this tie closed, when a sending-end voltage resolves.
		if err := applyLoadFlowVerdict(ctx, db, plan, donorHead, l); err != nil {
			return nil, false, err
		}
		break
	}

	return plan, true, nil
}

// applyLoadFlowVerdict runs the powerflow contingency sweep for this tie and, when
// it reaches a verdict, supersedes the nameplate capacity estimate with it: a
// feasible transfer (load restored, no voltage violation, no overload) is OK; any
// violation or overload is "exceeds". An inconclusive sweep (a loop/mesh tie or no
// load gained) or an unresolvable sending-end voltage leaves the nameplate verdict
// standing. A sweep failure degrades to the nameplate verdict with an explanatory
// note rather than sinking the whole suggestion.
func applyLoadFlowVerdict(ctx context.Context, db bun.IDB, plan *RestorationPlan, donorHead, liveSide trace.FeatureRef) error {
	// The donor feeder's sending-end voltage: read it off the head, else the tie's
	// live-side conductor (which carries voltage_class), else the tie itself.
	nomV, err := nominalVoltageV(ctx, db, donorHead, liveSide, plan.Tie)
	if err != nil {
		return err
	}
	if nomV <= 0 {
		return nil // no modelled voltage -> can't sweep; keep the nameplate verdict
	}

	check, err := powerflow.EvaluateRestorationTie(ctx, db,
		powerflow.FeatureRef{FeatureClass: donorHead.FeatureClass, FeatureID: donorHead.FeatureID},
		powerflow.FeatureRef{FeatureClass: plan.Tie.FeatureClass, FeatureID: plan.Tie.FeatureID},
		nomV)
	if err != nil {
		plan.LoadFlowNote = fmt.Sprintf("load-flow check unavailable: %v", err)
		return nil
	}

	plan.LoadFlowChecked = true
	plan.LoadFlowFeasible = check.Feasible
	plan.Violations = len(check.Violations)
	plan.Overloaded = len(check.Overloaded)
	if check.MinVoltagePu > 0 {
		v := check.MinVoltagePu
		plan.WorstVoltagePu = &v
	}
	plan.LoadFlowNote = check.Note

	switch {
	case check.Feasible:
		plan.CapacityVerdict = CapacityOK
		plan.VerdictBasis = VerdictBasisLoadFlow
	case plan.Violations > 0 || plan.Overloaded > 0:
		plan.CapacityVerdict = CapacityExceeds
		plan.VerdictBasis = VerdictBasisLoadFlow
	}
	return nil
}

// nominalVoltageV returns the first resolvable nominal operating voltage (volts)
// among the given features, read from a voltage_class / primary_voltage attribute.
// 0 (no error) when none carries one -- the caller then skips the load-flow sweep.
func nominalVoltageV(ctx context.Context, db bun.IDB, refs ...trace.FeatureRef) (float64, error) {
	for _, r := range refs {
		attrs, ok, err := featureAttrs(ctx, db, r.FeatureClass, r.FeatureID)
		if err != nil {
			return 0, err
		}
		if !ok {
			continue
		}
		for _, field := range []string{"voltage_class", "primary_voltage"} {
			if s, _ := attrs[field].(string); s != "" {
				if v, ok := parseVoltageCode(s); ok {
					return v, nil
				}
			}
		}
	}
	return 0, nil
}

// parseVoltageCode turns a coded VOLTAGE_CLASS value ("11KV", "400V") into volts --
// this package's small copy of the same parser trace and powerflow each keep, to
// avoid importing either just for it. ok=false for an empty or non-numeric code.
func parseVoltageCode(code string) (float64, bool) {
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

// rankRestorationPlans orders plans best-first: a capacity-OK plan beats an
// unverified one beats an overloading one; within a verdict, more customers
// restored wins, then the larger restored load, then a stable tie-id tiebreak.
func rankRestorationPlans(plans []RestorationPlan) {
	rank := func(v string) int {
		switch v {
		case CapacityOK:
			return 0
		case CapacityUnverified:
			return 1
		default: // CapacityExceeds
			return 2
		}
	}
	sort.SliceStable(plans, func(i, j int) bool {
		if r := rank(plans[i].CapacityVerdict); r != rank(plans[j].CapacityVerdict) {
			return r < rank(plans[j].CapacityVerdict)
		}
		if plans[i].RestoredCustomers != plans[j].RestoredCustomers {
			return plans[i].RestoredCustomers > plans[j].RestoredCustomers
		}
		if plans[i].RestoredLoadKVA != plans[j].RestoredLoadKVA {
			return plans[i].RestoredLoadKVA > plans[j].RestoredLoadKVA
		}
		return plans[i].Tie.FeatureID < plans[j].Tie.FeatureID
	})
}

// loadLeafRefs picks the load-leaf features (SERVICE_POINT / METER) out of a trace
// result, the only ones SummarizeService counts. Mirrors the filter OMS
// energization uses so the rollup can't over-count.
func loadLeafRefs(refs []trace.FeatureRef) []trace.FeatureRef {
	out := make([]trace.FeatureRef, 0, len(refs))
	for _, f := range refs {
		if f.FeatureClass == ServicePointClass || f.FeatureClass == MeterClass {
			out = append(out, f)
		}
	}
	return out
}

// tieCapableClassCodes returns the set of active feature-class codes that can act
// as a normally-open tie: those carrying a normal_state field whose domain offers
// the NORMALLY_OPEN value (Phase 48's field-pack). Mirrors tie.ListTieOptions's
// eligibility test, but by class code alone -- membership is all the cone scan
// needs. Empty until the field-pack is seeded, so FLISR simply finds no ties.
func tieCapableClassCodes(ctx context.Context, db bun.IDB) (map[string]bool, error) {
	var rows []struct {
		Code string `bun:"code"`
	}
	err := db.NewRaw(`
SELECT DISTINCT fc.code AS code
FROM metadata.feature_classes fc
JOIN metadata.fields f ON f.feature_class_id = fc.id AND f.name = ?
JOIN metadata.domain_values dv ON dv.domain_id = f.domain_id AND dv.code = ?
WHERE fc.status = 'active'`, normalStateField, normalStateOpen).Scan(ctx, &rows)
	if err != nil {
		return nil, fmt.Errorf("oms: loading tie-capable classes: %w", err)
	}
	out := make(map[string]bool, len(rows))
	for _, r := range rows {
		out[r.Code] = true
	}
	return out, nil
}

// Field + value names for the normally-open tie flag (Phase 48 field-pack),
// matching api/internal/tie.
const (
	normalStateField = "normal_state"
	normalStateOpen  = "NORMALLY_OPEN"
)

// isNormallyOpenTie reports whether a specific feature's live normal_state is
// NORMALLY_OPEN -- i.e. it is actually configured as a tie, not merely of a
// tie-capable class. A since-purged feature or an unset value reads false.
func isNormallyOpenTie(ctx context.Context, db bun.IDB, class, id string) (bool, error) {
	attrs, ok, err := featureAttrs(ctx, db, class, id)
	if err != nil || !ok {
		return false, err
	}
	s, _ := attrs[normalStateField].(string)
	return s == normalStateOpen, nil
}

// connectedNeighbors returns every live feature directly connected to featureID in
// runtime.feature_connectivity, either direction (a tie doesn't announce which way
// its edge was authored), each as a class-code + id ref. Soft-deleted neighbours
// are dropped (they conduct nothing), matching the trace engine.
func connectedNeighbors(ctx context.Context, db bun.IDB, featureID string) ([]trace.FeatureRef, error) {
	var rows []struct {
		ID   string `bun:"id"`
		Code string `bun:"code"`
	}
	err := db.NewRaw(`
SELECT DISTINCT nb.other_id AS id, mc.code AS code
FROM (
  SELECT to_feature_id AS other_id, to_feature_class_id AS other_class
  FROM runtime.feature_connectivity WHERE from_feature_id = ?
  UNION
  SELECT from_feature_id AS other_id, from_feature_class_id AS other_class
  FROM runtime.feature_connectivity WHERE to_feature_id = ?
) nb
JOIN metadata.feature_classes mc ON mc.id = nb.other_class
JOIN runtime.feature_registry r ON r.feature_id = nb.other_id AND r.deleted_at IS NULL`,
		featureID, featureID).Scan(ctx, &rows)
	if err != nil {
		return nil, fmt.Errorf("oms: loading neighbours of %s: %w", featureID, err)
	}
	out := make([]trace.FeatureRef, 0, len(rows))
	for _, r := range rows {
		out = append(out, trace.FeatureRef{FeatureClass: r.Code, FeatureID: r.ID})
	}
	return out, nil
}

// nearestUpstream returns the nearest feature of any of classes upstream of start,
// or nil when the walk reaches none. Reuses the barrier-free class walk the
// prediction engine uses, so it needs no trace_configuration.
func nearestUpstream(ctx context.Context, db bun.IDB, start trace.FeatureRef, classes []string) (*trace.FeatureRef, error) {
	hits, err := trace.UpstreamFeaturesOfClass(ctx, db, start, classes)
	if err != nil {
		return nil, err
	}
	if len(hits) == 0 {
		return nil, nil
	}
	nearest := hits[0]
	return &nearest, nil
}

// nearestCapacityKVA finds the supplying transformer upstream of start and reads
// its nameplate as kVA, so a restoration plan can judge whether the alternate
// feeder has the headroom to pick up the dead cone. nil (not an error) when no
// capacity-class feature is upstream or none carries a readable rating -- the plan
// is then reported capacity-unverified rather than blocked.
func nearestCapacityKVA(ctx context.Context, db bun.IDB, start trace.FeatureRef) (*float64, error) {
	xfmr, err := nearestUpstream(ctx, db, start, restorationCapacityClasses)
	if err != nil || xfmr == nil {
		return nil, err
	}
	attrs, ok, err := featureAttrs(ctx, db, xfmr.FeatureClass, xfmr.FeatureID)
	if err != nil || !ok {
		return nil, err
	}
	// Nameplate field varies by class: DIST_XFMR carries rated_kva, POWER_XFMR
	// rated_mva. Try kVA first, then MVA scaled up.
	if v, ok := numAttr(attrs, "rated_kva"); ok {
		return &v, nil
	}
	if v, ok := numAttr(attrs, "rated_mva"); ok {
		kva := v * 1000
		return &kva, nil
	}
	return nil, nil
}

// featureAttrs reads one feature's custom attributes map (crud nests them under
// "attributes"). ok is false -- no error -- for a class that doesn't exist or a
// feature that has been purged, the same "nothing to read" treatment
// deviceLiveState gives.
func featureAttrs(ctx context.Context, db bun.IDB, class, id string) (map[string]any, bool, error) {
	loaded, err := metadata.Load(ctx, db, class)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, false, nil
		}
		return nil, false, err
	}
	row, err := crud.GetFeature(ctx, db, loaded, id)
	if err != nil {
		if crud.IsNotFoundError(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	attrs, _ := row["attributes"].(map[string]any)
	return attrs, true, nil
}

// GenerateRestorationOrder turns a chosen restoration plan into a draft switching
// order: the RESTORE-phase steps that back-feed the incident's dead section by
// closing a normally-open tie. The tie is named by class+id; it must be one of the
// ties SuggestRestoration currently suggests for this incident's device (so the
// order can never be built on a stale or bogus tie), and the plan's figures fill
// the step instructions. Persisted in the same oms.switching_orders/_steps tables
// GenerateSwitchingOrder uses, so it shows up in the incident's switching panel and
// its close-tie step flips the tie's live device_state when executed (Slice 3's
// execute path is the existing SetStepExecuted). It refuses a terminal incident and
// a duplicate (a non-cancelled order already built for this same device+tie).
//
// This is Slice 2 of FLISR: it still only DRAFTS a program -- no switch moves until
// a crew executes the steps. Unlike the isolate order, a restoration order may
// coexist with an incident's isolation order (they are different programs), so it
// does not share the "one live order per incident" guard; it guards on its own
// reference instead.
func GenerateRestorationOrder(ctx context.Context, db *bun.DB, incidentID, tieClass, tieID string) (*SwitchingOrder, error) {
	inc, err := GetIncident(ctx, db, incidentID)
	if err != nil {
		return nil, err
	}
	if err := assertLocalIncident(inc); err != nil {
		return nil, err
	}
	if inc.PredictedDeviceClass == nil || inc.PredictedDeviceID == nil {
		return nil, invalidf("incident %q has no device to restore around; attribute it first", incidentID)
	}
	if inc.Status == StatusRestored || inc.Status == StatusCancelled {
		return nil, invalidf("incident %q is %s; cannot build a restoration order for it", incidentID, inc.Status)
	}
	device := trace.FeatureRef{FeatureClass: *inc.PredictedDeviceClass, FeatureID: *inc.PredictedDeviceID}

	// Re-run the suggestion so the order is built from the CURRENT network, and
	// locate the chosen tie's plan -- a tie that isn't a live suggestion is refused.
	suggestion, err := SuggestRestoration(ctx, db, device)
	if err != nil {
		return nil, err
	}
	var plan *RestorationPlan
	for i := range suggestion.Plans {
		if suggestion.Plans[i].Tie.FeatureClass == tieClass && suggestion.Plans[i].Tie.FeatureID == tieID {
			plan = &suggestion.Plans[i]
			break
		}
	}
	if plan == nil {
		return nil, invalidf("%s is not a current restoration tie for incident %q; re-run the restoration suggestion", deviceRefLabel(tieClass, tieID), incidentID)
	}

	deviceLabel := deviceRefLabel(device.FeatureClass, device.FeatureID)
	sourceLabel := plan.AlternateSourceLabel
	if sourceLabel == "" {
		sourceLabel = "the alternate feeder"
	}
	restored := fmt.Sprintf("%d supply(ies)", plan.RestoredServicePoints)
	if plan.RestoredCustomers > 0 {
		restored += fmt.Sprintf(" / %d customer(s)", plan.RestoredCustomers)
	}
	reference := fmt.Sprintf("Restoration of %s via %s", deviceLabel, plan.TieLabel)

	type stepDraft struct {
		phase, action, instruction string
		deviceClass, deviceID      *string
	}
	var drafts []stepDraft

	// Prerequisite: the faulted section must be isolated before back-energizing.
	drafts = append(drafts, stepDraft{StepPhaseRestore, StepActionNote,
		fmt.Sprintf("Confirm the faulted section below %s is isolated and safe to back-energize (fault cleared, earths removed as required).", deviceLabel), nil, nil})

	// Capacity warning, when the alternate feeder may not hold the picked-up load.
	if plan.CapacityVerdict == CapacityExceeds {
		var warn string
		if plan.VerdictBasis == VerdictBasisLoadFlow {
			warn = fmt.Sprintf("WARNING: a load-flow sweep predicts %d voltage violation(s) and %d overload(s) on %s once this section transfers", plan.Violations, plan.Overloaded, sourceLabel)
			if plan.WorstVoltagePu != nil {
				warn += fmt.Sprintf(" (worst %.3f pu)", *plan.WorstVoltagePu)
			}
			warn += " -- confirm before closing."
		} else {
			warn = fmt.Sprintf("WARNING: %s may be overloaded by this back-feed (restoring %.0f kVA on top of %.0f kVA present load).", sourceLabel, plan.RestoredLoadKVA, plan.AlternateFeederLoadKVA)
			if plan.AlternateCapacityKVA != nil {
				warn += fmt.Sprintf(" Transformer rating %.0f kVA -- confirm loading before closing.", *plan.AlternateCapacityKVA)
			}
		}
		drafts = append(drafts, stepDraft{StepPhaseRestore, StepActionNote, warn, nil, nil})
	}

	// Close the tie to back-feed, then verify supply is restored.
	tc, ti := plan.Tie.FeatureClass, plan.Tie.FeatureID
	drafts = append(drafts, stepDraft{StepPhaseRestore, StepActionClose,
		fmt.Sprintf("Close %s to back-feed the de-energized section from %s.", plan.TieLabel, sourceLabel), &tc, &ti})
	drafts = append(drafts, stepDraft{StepPhaseRestore, StepActionVerify,
		fmt.Sprintf("Verify %s restored via %s.", restored, sourceLabel), nil, nil})

	var out *SwitchingOrder
	err = db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		var existing int
		if err := tx.NewSelect().
			Model((*SwitchingOrder)(nil)).
			ColumnExpr("count(*)").
			Where("incident_id = ?", incidentID).
			Where("reference = ?", reference).
			Where("status <> ?", SwitchingStatusCancelled).
			Scan(ctx, &existing); err != nil {
			return fmt.Errorf("oms: checking existing restoration orders: %w", err)
		}
		if existing > 0 {
			return invalidf("a restoration order for %s via %s already exists; cancel it before regenerating", deviceLabel, plan.TieLabel)
		}

		var orderID string
		if err := tx.QueryRowContext(ctx,
			`INSERT INTO oms.switching_orders (incident_id, reference, status) VALUES (?, ?, 'draft') RETURNING id`,
			incidentID, reference,
		).Scan(&orderID); err != nil {
			return fmt.Errorf("oms: creating restoration order: %w", err)
		}

		steps := make([]SwitchingStep, len(drafts))
		for i, d := range drafts {
			steps[i] = SwitchingStep{
				SwitchingOrderID: orderID,
				StepNo:           i + 1,
				Phase:            d.phase,
				Action:           d.action,
				DeviceClass:      d.deviceClass,
				DeviceID:         d.deviceID,
				Instruction:      d.instruction,
			}
		}
		if _, err := tx.NewInsert().Model(&steps).ExcludeColumn("id", "created_at").Exec(ctx); err != nil {
			return fmt.Errorf("oms: inserting restoration steps: %w", err)
		}

		out, err = getSwitchingOrder(ctx, tx, orderID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// numAttr reads a numeric attribute, tolerating the several Go types bun/pgdriver
// may hand back a NUMERIC column as (float64, the integer types, or a string).
func numAttr(attrs map[string]any, name string) (float64, bool) {
	switch v := attrs[name].(type) {
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case string:
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f, true
		}
	}
	return 0, false
}
