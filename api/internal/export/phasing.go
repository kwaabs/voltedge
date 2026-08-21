package export

// CIM phasing -- Phase 16 Slice 4, docs/phase-16-cim-export.md.
//
// The Slice 1-3 model is phase-agnostic: an ACLineSegment or EnergyConsumer says
// nothing about which of L1/L2/L3/N it carries. CDPSM models phasing not as an
// attribute on the equipment but as separate per-phase objects -- an
// ACLineSegmentPhase (or EnergyConsumerPhase) per energised phase, each a sibling
// RDF resource that names its single phase (a SinglePhaseKind: A/B/C/N) and points
// back at its parent equipment.
//
// So this file expands one coded PHASE field value (e.g. "L1L2L3N") into that set
// of per-phase objects. The mapping is metadata-driven like the rest: a
// cim_attribute_mappings row with transform 'phase' whose cim_attribute names the
// per-phase class (e.g. "ACLineSegmentPhase.phase"). The coded-value -> phase-set
// expansion and the SinglePhaseKind naming are behavioral, so they live here.

import (
	"fmt"

	"github.com/kwaabs/voltedge/api/internal/metadata"
)

// phaseKinds maps a coded PHASE domain value (seed/phase_model_example.sql) to the
// CIM SinglePhaseKind letters it energises. L1/L2/L3 -> A/B/C, N -> N; the
// single-phase-plus-neutral and full four-wire composites expand to their members.
// A value absent here (or an empty field) yields no per-phase objects.
var phaseKinds = map[string][]string{
	"L1":      {"A"},
	"L2":      {"B"},
	"L3":      {"C"},
	"N":       {"N"},
	"L1N":     {"A", "N"},
	"L2N":     {"B", "N"},
	"L3N":     {"C", "N"},
	"L1L2L3N": {"A", "B", "C", "N"},
}

// writePhases emits a feature's per-phase CIM objects as siblings of the
// equipment (so it must be called after the equipment object is closed, like
// writeLocation). It is a no-op for a class with no transform='phase' mapping or
// a feature whose coded phase value is empty/unrecognised, so a phase-agnostic
// class (e.g. DIST_XFMR, BusbarSection) simply gets nothing.
func (cw *cimWriter) writePhases(cm metadata.CimClassMapping, f Feature, attrs []metadata.CimAttributeMapping) {
	for _, a := range attrs {
		if a.Transform != "phase" {
			continue
		}
		code, ok := f.Attributes[a.FieldName].(string)
		if !ok || code == "" {
			continue
		}
		kinds, ok := phaseKinds[code]
		if !ok {
			continue
		}
		// cim_attribute is "<PhaseClass>.phase" (e.g. ACLineSegmentPhase.phase);
		// the parent back-reference is "<PhaseClass>.<EquipmentClass>" (e.g.
		// ACLineSegmentPhase.ACLineSegment), and the equipment class is exactly the
		// feature's mapped CIM class.
		phaseClass := classOf(a.CimAttribute)
		if phaseClass == "" {
			continue
		}
		parentRef := phaseClass + "." + cm.CimClass
		for _, kind := range kinds {
			phaseID := uuidV5(fmt.Sprintf("phase:%s:%s:%s", phaseClass, f.ID, kind))
			cw.openObj(phaseClass, phaseID)
			cw.mRID(phaseID)
			cw.enumRef(a.CimAttribute, "SinglePhaseKind."+kind)
			cw.ref(parentRef, f.ID)
			cw.closeObj(phaseClass)
		}
	}
}

// classOf returns the class part of a dotted CIM attribute ("ACLineSegmentPhase"
// from "ACLineSegmentPhase.phase"), or "" if it isn't dotted.
func classOf(cimAttribute string) string {
	for i := 0; i < len(cimAttribute); i++ {
		if cimAttribute[i] == '.' {
			return cimAttribute[:i]
		}
	}
	return ""
}
