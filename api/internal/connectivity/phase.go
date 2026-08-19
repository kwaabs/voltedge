package connectivity

import (
	"strings"

	"github.com/kwaabs/voltedge/api/internal/metadata"
)

// phaseRuleIntersect is the one recognized token in a network_rules
// phase_rule column that turns on Phase 9 Slice 0's phase gate. A rule
// *opts in* by setting phase_rule to exactly this value; any other
// content (including the legacy free-text notes like
// "from.voltage_class = to.primary_voltage" the connectivity seed
// carries, and NULL) is treated as descriptive and enforces nothing.
// This keeps every pre-existing rule's behavior unchanged while letting
// new LV rules demand phase compatibility -- the "rule opts in via
// phase_rule" decision recorded in docs/phase-9-lv-service-delivery.md.
const phaseRuleIntersect = "phases_intersect"

// checkPhaseCompatibility gates an already-matched network rule on phase.
// It is a no-op unless the rule opted in (phase_rule == phaseRuleIntersect).
// When it has opted in, both endpoints must carry a phase value and their
// line phases (L1/L2/L3) must intersect -- a single-phase L1 service
// cannot be fed from an L2 supply even though a subtype/terminal rule
// authorizes the shape of the connection. A missing phase on either side
// is a hard error rather than a silent pass: a rule explicitly asked for
// phase matching, so unset phase data is a gap the user must fill, not a
// reason to wave the connection through.
func checkPhaseCompatibility(rule *metadata.NetworkRule, from, to *resolvedEndpoint) error {
	if rule.PhaseRule == nil || strings.TrimSpace(*rule.PhaseRule) != phaseRuleIntersect {
		return nil
	}
	if from.phase == nil || to.phase == nil {
		return invalid(
			"network rule %q requires matching phases, but a phase value is missing (%s=%s, %s=%s) -- set the phase field on both features before connecting",
			rule.Name,
			from.ref.FeatureClass, phaseOrUnset(from.phase),
			to.ref.FeatureClass, phaseOrUnset(to.phase),
		)
	}
	if !phasesIntersect(*from.phase, *to.phase) {
		return invalid(
			"phase mismatch under rule %q: %s carries %s and %s carries %s -- they share no common line phase (L1/L2/L3) and cannot be connected",
			rule.Name,
			from.ref.FeatureClass, *from.phase,
			to.ref.FeatureClass, *to.phase,
		)
	}
	return nil
}

// phasesIntersect reports whether two PHASE codes share at least one live
// line conductor. Neutral is deliberately ignored for compatibility: two
// services that share only N (e.g. L1N and L2N) are on different phases
// and must not be treated as connectable, even though both include a
// neutral.
func phasesIntersect(a, b string) bool {
	pa := lineConductors(a)
	for p := range lineConductors(b) {
		if pa[p] {
			return true
		}
	}
	return false
}

// lineConductors extracts the set of live line phases (L1/L2/L3) named in
// a PHASE code. It is intentionally forgiving about exact formatting --
// "L1N", "L1L2L3N", and a bare "L1" all resolve by substring -- so the
// gate keeps working if the PHASE domain later gains values like "L1L2"
// without a code change here. A neutral-only "N" resolves to the empty
// set, which correctly never intersects anything.
func lineConductors(code string) map[string]bool {
	out := map[string]bool{}
	u := strings.ToUpper(strings.TrimSpace(code))
	for _, p := range []string{"L1", "L2", "L3"} {
		if strings.Contains(u, p) {
			out[p] = true
		}
	}
	return out
}

// phaseOrUnset renders a phase pointer for an error message, turning nil
// into a readable "(unset)" rather than a Go pointer.
func phaseOrUnset(p *string) string {
	if p == nil {
		return "(unset)"
	}
	return *p
}
