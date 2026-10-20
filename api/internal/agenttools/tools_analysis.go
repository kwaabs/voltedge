package agenttools

// The analysis engines' tools. The engineering STUDIES (api/internal/powerflow)
// are otherwise a REST + UI surface -- heavy, interactive, run from the map's
// analysis panels -- and deliberately not in MCP. Fault LOCATION is the
// exception: it is an operational tool (a dispatcher/agent locating a real fault
// from a measured current), so it is exposed here as a thin adapter over the
// already-exported powerflow.RunFaultLocationJSON, gated on the same analysis:run
// permission the REST /analysis mount requires.

import (
	"encoding/json"

	"github.com/kwaabs/voltedge/api/internal/auth"
	"github.com/kwaabs/voltedge/api/internal/powerflow"
)

func analysisTools() []Tool {
	return []Tool{
		{
			Name: "locate_fault",
			Description: "Estimate WHERE a fault is on a feeder from a measured fault current -- distance-to-fault, " +
				"the inverse of the fault-current study. A protective device tripped and the relay/SCADA " +
				"recorded a fault current; because the prospective current falls as impedance from the source " +
				"grows, the measured current pins a target impedance, and every conductor span whose " +
				"source-to-span impedance brackets it is a candidate location (a branching feeder yields " +
				"several, on different laterals). Each candidate carries the span, the feature feeding it, the " +
				"distance from the source and along the span, and an interpolated lon/lat. 'start' is the " +
				"feeder SOURCE (the substation bus / transformer secondary), NOT the tripped device. " +
				"Three-phase only, on the faulted voltage level (upstream of transformers). An empty " +
				"candidate set comes with a 'note' explaining why (current above the source ceiling, or below " +
				"the feeder's minimum level). Respects the analysis:run permission.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"start":{
						"type":"object",
						"description":"The feeder source feeding the faulted section (substation bus / transformer secondary).",
						"properties":{
							"feature_class":{"type":"string","description":"The source feature's class code."},
							"feature_id":{"type":"string","description":"The source feature's id (uuid)."}
						},
						"required":["feature_class","feature_id"],
						"additionalProperties":false
					},
					"measured_current_amps":{"type":"number","description":"The fault current the relay/SCADA measured, in amperes (must be > 0)."},
					"nominal_voltage_v":{"type":"number","description":"The source nominal voltage in volts. May be omitted (0) only when the start is a boundary transformer, whose secondary voltage is then used."},
					"fault_type":{"type":"string","enum":["three_phase"],"description":"Fault type. Only three_phase is supported today (SLG location is a follow-up)."},
					"voltage_basis":{"type":"string","enum":["line_to_line","line_to_neutral"],"description":"How nominal_voltage_v is expressed; default line_to_line."},
					"voltage_factor_c":{"type":"number","description":"Pre-fault voltage factor; defaults to 1.0 (a real measurement back-calculates at the actual pre-fault voltage), unlike the prospective study's IEC 1.10."}
				},
				"required":["start","measured_current_amps"],
				"additionalProperties":false
			}`),
			RequiredPerm: auth.PermAnalysisRun,
			Handler:      powerflow.RunFaultLocationJSON,
		},
	}
}
