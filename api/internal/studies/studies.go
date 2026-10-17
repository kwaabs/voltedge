// Package studies is the single source of truth for which engineering / network
// analyses ("studies") apply to which feature classes.
//
// Two layers, mirroring the feature-class deactivation design:
//
//   - APPLICABILITY (code, here): a per-study predicate over a class's roles
//     (network_role / functional_role / boundary_role). This is the technical
//     FLOOR -- a study whose predicate is false for a class can never run there
//     (load flow on a pole, arc-flash on a meter), because the engine has no
//     model for it. Structural / non-electrical classes get no studies at all.
//   - ADMIN PRUNE (metadata.feature_class_study_disabled): within the applicable
//     set, an admin can turn a study OFF for a class they don't use it on. They
//     can only prune, never enable something the floor forbids.
//
// EFFECTIVE = applicable − disabled. The feature panel's analysis menu shows the
// effective set (carried on the schema endpoint), the admin screen edits the
// prune set, and the analysis endpoints enforce it.
package studies

import "github.com/kwaabs/voltedge/api/internal/metadata"

// Study is one analysis the UI can offer. ID is the stable key the menu, the
// prune table and the endpoints all agree on; Category groups the menu.
type Study struct {
	ID       string
	Name     string
	Category string // "loadflow" | "fault" | "capacity"
	// Applies reports whether this study is technically meaningful for a class
	// of these roles -- the floor. Kept here, in one place, so the applicability
	// rules are legible and tunable without hunting through the UI.
	Applies func(fc *metadata.FeatureClass) bool
}

// small predicate helpers over the (nullable) role fields.
func net(fc *metadata.FeatureClass, roles ...string) bool {
	for _, r := range roles {
		if fc.NetworkRole == r {
			return true
		}
	}
	return false
}
func fn(fc *metadata.FeatureClass, roles ...string) bool {
	if fc.FunctionalRole == nil {
		return false
	}
	for _, r := range roles {
		if *fc.FunctionalRole == r {
			return true
		}
	}
	return false
}
func bd(fc *metadata.FeatureClass, roles ...string) bool {
	if fc.BoundaryRole == nil {
		return false
	}
	for _, r := range roles {
		if *fc.BoundaryRole == r {
			return true
		}
	}
	return false
}

// isElectrical is the base floor: a structural / civil / furniture class (no
// electrical function and not part of the connectivity graph) gets no analysis
// at all -- a pole, a duct, a pit. Everything else is at least on the network.
func isElectrical(fc *metadata.FeatureClass) bool {
	return fc.FunctionalRole != nil || net(fc, "edge", "junction")
}

// canSourceLoadFlow: load flow is seeded at the top of what it traverses -- a
// source, a transformer, or a feeder-head device. Not a leaf, not mid-span.
func canSourceLoadFlow(fc *metadata.FeatureClass) bool {
	return fn(fc, "source", "transformer") || bd(fc, "source", "transformer", "protective")
}

// canSourceFeederStudy: feeder/source-level planning studies (contingency,
// load growth, Volt-VAR, storage) seed at a source or transformer boundary.
func canSourceFeederStudy(fc *metadata.FeatureClass) bool {
	return fn(fc, "source", "transformer") || bd(fc, "source", "transformer")
}

// Registry is the canonical study list, in menu order within each category.
// Every id here must match the feature panel's menu gate and the analysis
// endpoint that runs it.
var Registry = []Study{
	// --- Load flow family: sources, transformers and feeder-heads ---
	{"load_flow_auto", "Load flow (auto)", "loadflow", canSourceLoadFlow},
	{"load_flow", "Load flow", "loadflow", canSourceLoadFlow},
	{"voltage_profile", "Voltage profile & losses", "loadflow", canSourceLoadFlow},
	{"meshed_load_flow", "Meshed load flow", "loadflow", canSourceLoadFlow},
	{"nodal_load_flow", "Nodal load flow (Y-bus)", "loadflow", canSourceLoadFlow},
	{"unbalanced_load_flow", "Unbalanced load flow (3-phase)", "loadflow", canSourceLoadFlow},
	// LV network solve from a distribution transformer's secondary.
	{"lv_network", "Analyze LV network (from secondary)", "loadflow", func(fc *metadata.FeatureClass) bool { return bd(fc, "transformer") }},

	// --- Fault & protection ---
	// Fault level is computed at a node: any energised junction, plus transformers.
	{"fault_current", "Fault current", "fault", func(fc *metadata.FeatureClass) bool { return net(fc, "junction") || fn(fc, "transformer") }},
	// Coordination grades protective devices (relay/recloser/fuse/breaker).
	{"coordination", "Protection coordination", "fault", func(fc *metadata.FeatureClass) bool { return bd(fc, "protective") || fn(fc, "controller") }},
	// Ampacity is the thermal rating of a line -- edges only.
	{"ampacity", "Ampacity", "fault", func(fc *metadata.FeatureClass) bool { return net(fc, "edge") }},

	// --- Capacity & planning ---
	{"contingency", "Contingency & backfeed", "capacity", canSourceFeederStudy},
	// DER / EV connect at loads and feed off transformers.
	{"hosting", "DER hosting capacity", "capacity", func(fc *metadata.FeatureClass) bool { return fn(fc, "consumer", "transformer") || bd(fc, "transformer") }},
	{"ev_hosting", "EV feeder headroom", "capacity", func(fc *metadata.FeatureClass) bool { return fn(fc, "consumer", "transformer") || bd(fc, "transformer") }},
	{"load_growth", "Load-growth forecast", "capacity", canSourceFeederStudy},
	{"volt_var", "Volt-VAR (capacitor switching)", "capacity", canSourceFeederStudy},
	// Tap/regulator optimization -- transformers only.
	{"tap_optimization", "Tap optimization (voltage regulators)", "capacity", func(fc *metadata.FeatureClass) bool { return fn(fc, "transformer") || bd(fc, "transformer") }},
	{"storage_dispatch", "Storage dispatch (batteries)", "capacity", canSourceFeederStudy},
	{"vvo", "Volt-VAR Optimization (all controls)", "capacity", canSourceFeederStudy},
}

// byID indexes the registry for O(1) lookup.
var byID = func() map[string]Study {
	m := make(map[string]Study, len(Registry))
	for _, s := range Registry {
		m[s.ID] = s
	}
	return m
}()

// Known reports whether id is a registered study.
func Known(id string) bool { _, ok := byID[id]; return ok }

// AppliesTo reports whether study id is technically applicable to fc (the floor).
// An unknown id is false.
func AppliesTo(id string, fc *metadata.FeatureClass) bool {
	s, ok := byID[id]
	return ok && s.Applies(fc)
}

// Effective returns the study ids a class actually offers: applicable by role,
// minus the admin-disabled set. In Registry order (the UI's menu order).
func Effective(fc *metadata.FeatureClass, disabled map[string]bool) []string {
	out := []string{}
	for _, s := range Registry {
		if s.Applies(fc) && !disabled[s.ID] {
			out = append(out, s.ID)
		}
	}
	return out
}
