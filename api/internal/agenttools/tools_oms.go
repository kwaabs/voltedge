package agenttools

// The Outage Management (OMS) module's tool surface -- the same thin-adapter
// pattern as tools.go/handlers.go, one tool per already-exported oms engine
// function, each mirroring the REST route the outage board's frontend calls and
// declaring the RBAC permission that route requires (oms:read for the reads,
// oms:write for the writes). No query or lifecycle logic is reimplemented here;
// a handler unmarshals its arguments, validates the presence of the required
// ids, and returns the engine's result or error verbatim. The acting user is
// already on ctx (Execute calls auth.WithUser), so writes are attributed exactly
// as the REST path attributes them.
//
// The oms param structs (CreateCallParams, PlannedOutageParams, IndicesParams)
// carry no json tags, so each handler unmarshals into a local snake_case struct
// and maps the fields onto the params value explicitly.

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/kwaabs/voltedge/api/internal/auth"
	"github.com/kwaabs/voltedge/api/internal/oms"
	"github.com/kwaabs/voltedge/api/internal/trace"
	"github.com/uptrace/bun"
)

// omsTools returns the OMS tools in advertised order: the reads first
// (oms:read, Annotations left nil so the registry stamps readOnlyHint:true),
// then the writes (oms:write, each carrying its own annotations so a client
// confirms with a human before calling).
func omsTools() []Tool {
	return []Tool{
		// --- reads (oms:read) ---
		{
			Name: "oms_list_trouble_calls",
			Description: "List customer trouble calls newest-first, optionally filtered to a " +
				"single status -- the same list the REST GET /trouble-calls route returns " +
				"(the outage board's call queue). Each call carries its id, locator " +
				"(service point / address / lat-lng), call type, status, and any linked " +
				"incident. Omit status for every call.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"status":{"type":"string","description":"Filter to a single call status (e.g. open, associated, cleared, cancelled); omit for all."}
				},
				"additionalProperties":false
			}`),
			RequiredPerm: auth.PermOMSRead,
			Handler:      toolOMSListTroubleCalls,
		},
		{
			Name: "oms_get_trouble_call",
			Description: "Fetch one customer trouble call by its id -- the same read the REST " +
				"GET side of the trouble-calls surface performs. Returns the call's full " +
				"record (locator, call type, status, linked incident, and its location " +
				"projected to longitude/latitude).",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"id":{"type":"string","description":"The trouble call's id (uuid)."}
				},
				"required":["id"],
				"additionalProperties":false
			}`),
			RequiredPerm: auth.PermOMSRead,
			Handler:      toolOMSGetTroubleCall,
		},
		{
			Name: "oms_list_incidents",
			Description: "List outage incidents newest-first -- the same list the REST GET " +
				"/incidents route returns (the outage board). Optionally filter to a single " +
				"status, and set open_only to return only unrestored incidents (the active " +
				"board). Each incident carries its blamed device, status, outage type, and " +
				"affected-customer counts.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"status":{"type":"string","description":"Filter to a single incident status (e.g. predicted, confirmed, scheduled, restored, cancelled); omit for all."},
					"open_only":{"type":"boolean","description":"Return only incidents that are not yet restored (the active board); defaults to false."}
				},
				"additionalProperties":false
			}`),
			RequiredPerm: auth.PermOMSRead,
			Handler:      toolOMSListIncidents,
		},
		{
			Name: "oms_get_incident",
			Description: "Fetch one outage incident by its id -- the same read the REST GET " +
				"/incidents/{id} route performs. Returns the incident's full record " +
				"(blamed device, status, outage type, planned window, affected counts, and " +
				"timestamps).",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"id":{"type":"string","description":"The incident's id (uuid)."}
				},
				"required":["id"],
				"additionalProperties":false
			}`),
			RequiredPerm: auth.PermOMSRead,
			Handler:      toolOMSGetIncident,
		},
		{
			Name: "oms_incident_affected_area",
			Description: "Resolve an incident's affected service points -- the same read the " +
				"REST GET /incidents/{id}/affected route returns (what the map overlay draws " +
				"when an incident is selected). Returns the blamed device and each affected " +
				"service point's point geometry: the FROZEN interruption set once the incident " +
				"is confirmed, otherwise a live downstream trace from the blamed device.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"id":{"type":"string","description":"The incident's id (uuid)."}
				},
				"required":["id"],
				"additionalProperties":false
			}`),
			RequiredPerm: auth.PermOMSRead,
			Handler:      toolOMSIncidentAffectedArea,
		},
		{
			Name: "oms_incident_energization",
			Description: "Resolve an incident's LIVE de-energized cone -- what is cut off from " +
				"supply RIGHT NOW given the network's CURRENT switch positions, the same read the " +
				"REST GET /incidents/{id}/energization route returns. Unlike oms_incident_affected_area " +
				"(the frozen interruption set), this tracks the crew's switching in real time: it is " +
				"non-empty only while the blamed device is actually OPEN, and shrinks to nothing as " +
				"open/close steps are executed (oms_set_switching_step_executed flips device_state). " +
				"Returns the device's live state, each currently-dead service point's geometry, and " +
				"the total customers off supply.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"id":{"type":"string","description":"The incident's id (uuid)."}
				},
				"required":["id"],
				"additionalProperties":false
			}`),
			RequiredPerm: auth.PermOMSRead,
			Handler:      toolOMSIncidentEnergization,
		},
		{
			Name: "oms_suggest_restoration",
			Description: "FLISR -- suggest how to RESTORE the de-energized section below a faulted/open " +
				"device by back-feeding it from an alternate source through a normally-open tie, the " +
				"same read the REST GET /incidents/{id}/restoration (and /restoration) routes return. " +
				"Name the device by incident_id (uses the incident's blamed device) OR directly by " +
				"device_class + device_id. Returns the dead section's totals and, best-first, each " +
				"viable plan: the tie to close, the alternate source feeder, the customers/load it " +
				"would restore, and a capacity verdict (ok / exceeds / unverified) against the " +
				"alternate transformer's nameplate. Read-only -- it suggests switching, it flips nothing.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"incident_id":{"type":"string","description":"An incident (uuid) whose blamed device to restore around; provide this OR device_class+device_id."},
					"device_class":{"type":"string","description":"The faulted/open device's feature class; provide with device_id instead of incident_id."},
					"device_id":{"type":"string","description":"The faulted/open device's id (uuid); provide with device_class."}
				},
				"additionalProperties":false
			}`),
			RequiredPerm: auth.PermOMSRead,
			Handler:      toolOMSSuggestRestoration,
		},
		{
			Name: "oms_list_switching_orders",
			Description: "List every switching order for an incident, newest-first, each with " +
				"its ordered steps -- the same read the REST GET " +
				"/incidents/{id}/switching-orders route returns (the outage board's switching " +
				"panel). Each step carries its id (pass it to oms_set_switching_step_executed), " +
				"phase, action, instruction, and executed state.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"incident_id":{"type":"string","description":"The incident's id (uuid) whose switching orders to list."}
				},
				"required":["incident_id"],
				"additionalProperties":false
			}`),
			RequiredPerm: auth.PermOMSRead,
			Handler:      toolOMSListSwitchingOrders,
		},
		{
			Name: "oms_get_switching_order",
			Description: "Fetch one switching order by its id, with its ordered steps -- the " +
				"same read the REST GET /switching-orders/{id} route performs. Each step " +
				"carries its id, phase, action, instruction, and executed state.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"id":{"type":"string","description":"The switching order's id (uuid)."}
				},
				"required":["id"],
				"additionalProperties":false
			}`),
			RequiredPerm: auth.PermOMSRead,
			Handler:      toolOMSGetSwitchingOrder,
		},
		{
			Name: "oms_reliability_indices",
			Description: "Compute the IEEE 1366 reliability indices (SAIDI/SAIFI/CAIDI and " +
				"estimated ENS) over the confirmed interruption history -- the same read the " +
				"REST GET /indices route returns (the reliability report). All arguments are " +
				"optional: from/to (RFC3339) narrow the analysis window (default the trailing " +
				"12 months), avg_load_kw overrides the per-customer demand assumption behind " +
				"the estimated ENS, and circuit_id scopes the whole report to one circuit's " +
				"service points (per-feeder indices). The report also includes by_fault_cause: " +
				"a decomposition of SAIDI/SAIFI into per-fault-cause contributions (equipment " +
				"failure, lightning, vegetation, ...) whose buckets sum back to the system " +
				"figures -- the fault-caused reliability rollup; absent when the faults module " +
				"is not installed (fault_breakdown_available=false).",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"from":{"type":"string","description":"Window start (RFC3339 timestamp); defaults to 12 months before 'to'."},
					"to":{"type":"string","description":"Window end (RFC3339 timestamp); defaults to now."},
					"avg_load_kw":{"type":"number","description":"Assumed average per-customer demand (kW) behind the estimated ENS; defaults to 1.0."},
					"circuit_id":{"type":"string","description":"Scope the report to one circuit's service points (uuid); omit for a whole-system report."}
				},
				"additionalProperties":false
			}`),
			RequiredPerm: auth.PermOMSRead,
			Handler:      toolOMSReliabilityIndices,
		},

		// --- writes (oms:write) ---
		{
			Name: "oms_create_trouble_call",
			Description: "Log one customer trouble call -- the same write the REST POST " +
				"/trouble-calls route performs (the outage board's 'log a call' action). At " +
				"least one locator is required: a service_point_id, an address, or a " +
				"longitude/latitude pair (both or neither). The call is created at status " +
				"'open' and does not roll up into an incident until oms_run_prediction. Returns " +
				"the created call. This WRITES to the database: confirm with a human before calling.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"service_point_id":{"type":"string","description":"The reporting service point's id (uuid); one of service_point_id/address/lat-lng is required."},
					"address":{"type":"string","description":"A free-text address locator; one of service_point_id/address/lat-lng is required."},
					"longitude":{"type":"number","description":"Longitude (EPSG:4326) of the call; must be paired with latitude."},
					"latitude":{"type":"number","description":"Latitude (EPSG:4326) of the call; must be paired with longitude."},
					"reported_at":{"type":"string","description":"When the call was reported (RFC3339 timestamp); defaults to now."},
					"caller_ref":{"type":"string","description":"Optional caller reference / ticket number."},
					"call_type":{"type":"string","description":"Call type: no_supply, partial, or flicker; defaults to no_supply."},
					"notes":{"type":"string","description":"Optional free-text notes."}
				},
				"additionalProperties":false
			}`),
			Annotations: &Annotations{
				Title:           "Log trouble call",
				ReadOnlyHint:    &falseHint, // this tool writes
				DestructiveHint: &falseHint, // a create is additive, not destructive
				IdempotentHint:  &falseHint, // repeating it logs another call
				OpenWorldHint:   &falseHint, // acts only on this system's own database
			},
			RequiredPerm: auth.PermOMSWrite,
			Handler:      toolOMSCreateTroubleCall,
		},
		{
			Name: "oms_associate_trouble_call",
			Description: "Resolve a trouble call's service point so it can feed the prediction " +
				"rollup -- the same write the REST POST /trouble-calls/{id}/associate route " +
				"performs. Pass a service_point_id for a manual association, or snap=true to " +
				"bind the nearest service point to the call's own location. It does not change " +
				"the call's status (rolling into an incident is oms_run_prediction's job). " +
				"Returns the updated call. This WRITES to the database: confirm with a human before calling.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"id":{"type":"string","description":"The trouble call's id (uuid) to associate."},
					"service_point_id":{"type":"string","description":"The service point (uuid) to associate manually; omit when using snap."},
					"snap":{"type":"boolean","description":"Bind the nearest service point to the call's own location instead of naming one; requires the call to have a location."}
				},
				"required":["id"],
				"additionalProperties":false
			}`),
			Annotations: &Annotations{
				Title:           "Associate trouble call",
				ReadOnlyHint:    &falseHint, // this tool writes
				DestructiveHint: &falseHint, // it sets the call's service point, it doesn't remove anything
				IdempotentHint:  &trueHint,  // re-associating to the same service point yields the same state
				OpenWorldHint:   &falseHint, // acts only on this system's own database
			},
			RequiredPerm: auth.PermOMSWrite,
			Handler:      toolOMSAssociateTroubleCall,
		},
		{
			Name: "oms_run_prediction",
			Description: "Roll every open, service-point-resolved trouble call up into predicted " +
				"incidents and persist them in one transaction -- the same write the REST POST " +
				"/incidents/predict route performs (the outage board's 'predict' action). It " +
				"operates only on open calls, so re-running it picks up newly logged calls " +
				"without duplicating incidents. Touches nothing in the live network and " +
				"materialises no interruptions (that happens on confirm). Takes no arguments. " +
				"Returns the incidents it created. This WRITES to the database: confirm with a human before calling.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
			Annotations: &Annotations{
				Title:           "Run outage prediction",
				ReadOnlyHint:    &falseHint, // this tool writes
				DestructiveHint: &falseHint, // it creates predicted incidents, it doesn't remove anything
				IdempotentHint:  &falseHint, // re-running with new open calls creates further incidents
				OpenWorldHint:   &falseHint, // acts only on this system's own database
			},
			RequiredPerm: auth.PermOMSWrite,
			Handler:      toolOMSRunPrediction,
		},
		{
			Name: "oms_confirm_incident",
			Description: "Confirm an incident and materialise its per-service-point interruptions " +
				"(the frozen fact rows the reliability indices sum over) -- the same write the " +
				"REST POST /incidents/{id}/confirm route performs (the outage board's confirm " +
				"action). Confirms a predicted unplanned incident or executes a scheduled " +
				"planned outage (which resets its clock to now); no other status can be " +
				"confirmed. Returns the updated incident. This WRITES to the database: confirm " +
				"with a human before calling.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"id":{"type":"string","description":"The incident's id (uuid); must be predicted or scheduled."}
				},
				"required":["id"],
				"additionalProperties":false
			}`),
			Annotations: &Annotations{
				Title:           "Confirm incident",
				ReadOnlyHint:    &falseHint, // this tool writes
				DestructiveHint: &falseHint, // a status advance that freezes interruptions, not a removal
				IdempotentHint:  &falseHint, // an already-confirmed incident can't be confirmed again
				OpenWorldHint:   &falseHint, // acts only on this system's own database
			},
			RequiredPerm: auth.PermOMSWrite,
			Handler:      toolOMSConfirmIncident,
		},
		{
			Name: "oms_create_planned_outage",
			Description: "Schedule a planned outage around operating one device -- the same write " +
				"the REST POST /incidents/planned route performs (the outage board's 'schedule " +
				"planned outage' action). The device (device_class and device_id) is required; " +
				"the booked window and metadata are optional. It starts at status 'scheduled' " +
				"and materialises no interruptions until confirmed (executed). Returns the " +
				"created incident. This WRITES to the database: confirm with a human before calling.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"device_class":{"type":"string","description":"The feature class of the device whose operation causes the outage."},
					"device_id":{"type":"string","description":"The device's id (uuid)."},
					"planned_start":{"type":"string","description":"Booked outage start (RFC3339 timestamp); must be before planned_end when both are given."},
					"planned_end":{"type":"string","description":"Booked outage end (RFC3339 timestamp)."},
					"cause_code":{"type":"string","description":"Optional cause/reason code for the planned work."},
					"circuit_id":{"type":"string","description":"Optional circuit (uuid) the outage is booked against."},
					"notes":{"type":"string","description":"Optional free-text notes."}
				},
				"required":["device_class","device_id"],
				"additionalProperties":false
			}`),
			Annotations: &Annotations{
				Title:           "Schedule planned outage",
				ReadOnlyHint:    &falseHint, // this tool writes
				DestructiveHint: &falseHint, // a create is additive, not destructive
				IdempotentHint:  &falseHint, // repeating it schedules another outage
				OpenWorldHint:   &falseHint, // acts only on this system's own database
			},
			RequiredPerm: auth.PermOMSWrite,
			Handler:      toolOMSCreatePlannedOutage,
		},
		{
			Name: "oms_restore_incident",
			Description: "Mark an incident restored (now), closing every open interruption window " +
				"it owns -- the same write the REST POST /incidents/{id}/restore route performs " +
				"(the outage board's restore action). A restored or cancelled incident cannot be " +
				"restored again. Returns the updated incident. This WRITES to the database: " +
				"confirm with a human before calling.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"id":{"type":"string","description":"The incident's id (uuid) to restore."}
				},
				"required":["id"],
				"additionalProperties":false
			}`),
			Annotations: &Annotations{
				Title:           "Restore incident",
				ReadOnlyHint:    &falseHint, // this tool writes
				DestructiveHint: &falseHint, // it ends the outage, it doesn't destroy the record
				IdempotentHint:  &falseHint, // an already-restored incident can't be restored again
				OpenWorldHint:   &falseHint, // acts only on this system's own database
			},
			RequiredPerm: auth.PermOMSWrite,
			Handler:      toolOMSRestoreIncident,
		},
		{
			Name: "oms_cancel_incident",
			Description: "Cancel a (mis-predicted) incident and release its linked calls back to " +
				"'open' so they can re-roll into a corrected incident -- the same write the REST " +
				"POST /incidents/{id}/cancel route performs (the outage board's cancel action). A " +
				"restored incident cannot be cancelled. Returns the updated incident. This WRITES " +
				"to the database: confirm with a human before calling.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"id":{"type":"string","description":"The incident's id (uuid) to cancel."}
				},
				"required":["id"],
				"additionalProperties":false
			}`),
			Annotations: &Annotations{
				Title:           "Cancel incident",
				ReadOnlyHint:    &falseHint, // this tool writes
				DestructiveHint: &trueHint,  // it cancels the incident and releases its calls
				IdempotentHint:  &falseHint, // an already-cancelled incident can't be cancelled again
				OpenWorldHint:   &falseHint, // acts only on this system's own database
			},
			RequiredPerm: auth.PermOMSWrite,
			Handler:      toolOMSCancelIncident,
		},
		{
			Name: "oms_generate_switching_order",
			Description: "Build a draft switching order for an incident from its operated device " +
				"-- an isolate half (open/verify/earth) and a restore half (remove earths/close) " +
				"-- the same write the REST POST /incidents/{id}/switching-orders route performs " +
				"(the outage board's 'generate switching order' action). It refuses an incident " +
				"with no device, a terminal incident, or one that already has a live order. " +
				"Returns the generated order with its steps. This WRITES to the database: confirm " +
				"with a human before calling.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"incident_id":{"type":"string","description":"The incident's id (uuid) to build a switching order for; must have an attributed device."}
				},
				"required":["incident_id"],
				"additionalProperties":false
			}`),
			Annotations: &Annotations{
				Title:           "Generate switching order",
				ReadOnlyHint:    &falseHint, // this tool writes
				DestructiveHint: &falseHint, // it creates a draft order, it doesn't remove anything
				IdempotentHint:  &falseHint, // a second generate is rejected while a live order exists
				OpenWorldHint:   &falseHint, // acts only on this system's own database
			},
			RequiredPerm: auth.PermOMSWrite,
			Handler:      toolOMSGenerateSwitchingOrder,
		},
		{
			Name: "oms_generate_restoration_order",
			Description: "FLISR -- build a draft RESTORATION switching order for an incident that " +
				"back-feeds its dead section by closing a normally-open tie, the same write the REST " +
				"POST /incidents/{id}/restoration-orders route performs. The tie (tie_class + tie_id) " +
				"must be one oms_suggest_restoration currently suggests for this incident -- run that " +
				"first to choose a tie. The order's steps confirm the faulted section is isolated, warn " +
				"when the alternate feeder may be overloaded, close the tie, and verify supply restored; " +
				"executing the close step later flips the tie's live state (oms_set_switching_step_executed). " +
				"A restoration order may coexist with the incident's isolation order. Returns the " +
				"generated order with its steps. This WRITES to the database: confirm with a human before calling.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"incident_id":{"type":"string","description":"The incident's id (uuid); must have an attributed device and not be restored/cancelled."},
					"tie_class":{"type":"string","description":"The feature class of the normally-open tie to close (from oms_suggest_restoration)."},
					"tie_id":{"type":"string","description":"The tie's id (uuid), from a suggested restoration plan."}
				},
				"required":["incident_id","tie_class","tie_id"],
				"additionalProperties":false
			}`),
			Annotations: &Annotations{
				Title:           "Generate restoration order",
				ReadOnlyHint:    &falseHint, // this tool writes
				DestructiveHint: &falseHint, // it drafts a close-tie order, it doesn't remove anything
				IdempotentHint:  &falseHint, // a second generate for the same tie is rejected while a live one exists
				OpenWorldHint:   &falseHint, // acts only on this system's own database
			},
			RequiredPerm: auth.PermOMSWrite,
			Handler:      toolOMSGenerateRestorationOrder,
		},
		{
			Name: "oms_set_switching_step_executed",
			Description: "Mark a switching step done (executed=true, default) or outstanding again " +
				"(executed=false) -- the same write the REST POST /switching-steps/{id}/execute " +
				"route performs (ticking a step off in the outage board's switching panel). It " +
				"nudges the parent order's status to reflect progress (first step -> in_progress, " +
				"last step -> completed). Returns the parent order with its steps. This WRITES to " +
				"the database: confirm with a human before calling.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"step_id":{"type":"string","description":"The switching step's id (uuid), e.g. from oms_get_switching_order."},
					"executed":{"type":"boolean","description":"Mark the step done (true, the default) or outstanding again (false)."}
				},
				"required":["step_id"],
				"additionalProperties":false
			}`),
			Annotations: &Annotations{
				Title:           "Set switching step executed",
				ReadOnlyHint:    &falseHint, // this tool writes
				DestructiveHint: &falseHint, // it toggles a step's executed flag, it doesn't remove anything
				IdempotentHint:  &trueHint,  // re-setting the same executed value yields the same state
				OpenWorldHint:   &falseHint, // acts only on this system's own database
			},
			RequiredPerm: auth.PermOMSWrite,
			Handler:      toolOMSSetSwitchingStepExecuted,
		},
		{
			Name: "oms_cancel_switching_order",
			Description: "Cancel a switching order (its steps are kept for the record) -- the same " +
				"write the REST POST /switching-orders/{id}/cancel route performs (the outage " +
				"board's switching panel). An already-cancelled or completed order cannot be " +
				"cancelled. Returns the updated order. This WRITES to the database: confirm with a " +
				"human before calling.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"id":{"type":"string","description":"The switching order's id (uuid) to cancel."}
				},
				"required":["id"],
				"additionalProperties":false
			}`),
			Annotations: &Annotations{
				Title:           "Cancel switching order",
				ReadOnlyHint:    &falseHint, // this tool writes
				DestructiveHint: &trueHint,  // it cancels the order
				IdempotentHint:  &falseHint, // an already-cancelled order can't be cancelled again
				OpenWorldHint:   &falseHint, // acts only on this system's own database
			},
			RequiredPerm: auth.PermOMSWrite,
			Handler:      toolOMSCancelSwitchingOrder,
		},
	}
}

// --- read handlers ---

func toolOMSListTroubleCalls(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	calls, err := oms.ListTroubleCalls(ctx, db, in.Status)
	if err != nil {
		return nil, err
	}
	return map[string]any{"trouble_calls": calls, "count": len(calls)}, nil
}

func toolOMSGetTroubleCall(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.ID == "" {
		return nil, fmt.Errorf("id is required")
	}
	return oms.GetTroubleCall(ctx, db, in.ID)
}

func toolOMSListIncidents(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		Status   string `json:"status"`
		OpenOnly bool   `json:"open_only"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	incidents, err := oms.ListIncidents(ctx, db, in.Status, in.OpenOnly)
	if err != nil {
		return nil, err
	}
	return map[string]any{"incidents": incidents, "count": len(incidents)}, nil
}

func toolOMSGetIncident(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.ID == "" {
		return nil, fmt.Errorf("id is required")
	}
	return oms.GetIncident(ctx, db, in.ID)
}

func toolOMSIncidentAffectedArea(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.ID == "" {
		return nil, fmt.Errorf("id is required")
	}
	return oms.IncidentAffectedArea(ctx, db, in.ID)
}

func toolOMSIncidentEnergization(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.ID == "" {
		return nil, fmt.Errorf("id is required")
	}
	return oms.IncidentEnergization(ctx, db, in.ID)
}

func toolOMSSuggestRestoration(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		IncidentID  string `json:"incident_id"`
		DeviceClass string `json:"device_class"`
		DeviceID    string `json:"device_id"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	// incident_id wins when given (resolves to the incident's blamed device);
	// otherwise the device must be named directly.
	if in.IncidentID != "" {
		return oms.SuggestRestorationForIncident(ctx, db, in.IncidentID)
	}
	if in.DeviceClass == "" || in.DeviceID == "" {
		return nil, fmt.Errorf("provide incident_id, or both device_class and device_id")
	}
	return oms.SuggestRestoration(ctx, db, trace.FeatureRef{FeatureClass: in.DeviceClass, FeatureID: in.DeviceID})
}

func toolOMSListSwitchingOrders(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		IncidentID string `json:"incident_id"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.IncidentID == "" {
		return nil, fmt.Errorf("incident_id is required")
	}
	orders, err := oms.ListSwitchingOrders(ctx, db, in.IncidentID)
	if err != nil {
		return nil, err
	}
	return map[string]any{"switching_orders": orders, "count": len(orders)}, nil
}

func toolOMSGetSwitchingOrder(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.ID == "" {
		return nil, fmt.Errorf("id is required")
	}
	return oms.GetSwitchingOrder(ctx, db, in.ID)
}

func toolOMSReliabilityIndices(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		From      *time.Time `json:"from"`
		To        *time.Time `json:"to"`
		AvgLoadKW *float64   `json:"avg_load_kw"`
		CircuitID *string    `json:"circuit_id"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	// Params carry no json tags; map the parsed request onto them explicitly.
	// The engine fills defaults for an omitted window and load figure, so all
	// fields are optional here.
	return oms.ReliabilityIndicesReport(ctx, db, oms.IndicesParams{
		From:      in.From,
		To:        in.To,
		AvgLoadKW: in.AvgLoadKW,
		CircuitID: in.CircuitID,
	})
}

// --- write handlers ---

func toolOMSCreateTroubleCall(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		ServicePointID *string    `json:"service_point_id"`
		Address        *string    `json:"address"`
		Longitude      *float64   `json:"longitude"`
		Latitude       *float64   `json:"latitude"`
		ReportedAt     *time.Time `json:"reported_at"`
		CallerRef      *string    `json:"caller_ref"`
		CallType       string     `json:"call_type"`
		Notes          *string    `json:"notes"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	// The engine enforces the SP-optional locator rule (at least one of service
	// point / address / lat-lng) and normalises the call type; its validation
	// errors surface in-band as the tool error. Params carry no json tags, so map
	// the request onto them explicitly.
	return oms.CreateTroubleCall(ctx, db, oms.CreateCallParams{
		ServicePointID: in.ServicePointID,
		Address:        in.Address,
		Longitude:      in.Longitude,
		Latitude:       in.Latitude,
		ReportedAt:     in.ReportedAt,
		CallerRef:      in.CallerRef,
		CallType:       in.CallType,
		Notes:          in.Notes,
	})
}

func toolOMSAssociateTroubleCall(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		ID             string  `json:"id"`
		ServicePointID *string `json:"service_point_id"`
		Snap           bool    `json:"snap"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.ID == "" {
		return nil, fmt.Errorf("id is required")
	}
	// The engine requires either a service_point_id or snap=true and re-checks the
	// call's state; those errors surface in-band as the tool error.
	return oms.AssociateTroubleCall(ctx, db, in.ID, in.ServicePointID, in.Snap)
}

func toolOMSRunPrediction(ctx context.Context, db bun.IDB, _ json.RawMessage) (any, error) {
	sdb, err := asDB(db)
	if err != nil {
		return nil, err
	}
	// PredictAndPersist owns its own transaction (rolls open calls up into
	// predicted incidents and links them). Returns the incidents it created.
	created, err := oms.PredictAndPersist(ctx, sdb)
	if err != nil {
		return nil, err
	}
	return map[string]any{"incidents": created, "count": len(created)}, nil
}

func toolOMSConfirmIncident(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.ID == "" {
		return nil, fmt.Errorf("id is required")
	}
	sdb, err := asDB(db)
	if err != nil {
		return nil, err
	}
	return oms.ConfirmIncident(ctx, sdb, in.ID)
}

func toolOMSCreatePlannedOutage(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		DeviceClass  string     `json:"device_class"`
		DeviceID     string     `json:"device_id"`
		PlannedStart *time.Time `json:"planned_start"`
		PlannedEnd   *time.Time `json:"planned_end"`
		CauseCode    *string    `json:"cause_code"`
		CircuitID    *string    `json:"circuit_id"`
		Notes        *string    `json:"notes"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.DeviceClass == "" || in.DeviceID == "" {
		return nil, fmt.Errorf("device_class and device_id are required")
	}
	sdb, err := asDB(db)
	if err != nil {
		return nil, err
	}
	// Params carry no json tags; map the request onto them explicitly. The engine
	// validates the window ordering and traces the affected cone.
	return oms.CreatePlannedOutage(ctx, sdb, oms.PlannedOutageParams{
		DeviceClass:  in.DeviceClass,
		DeviceID:     in.DeviceID,
		PlannedStart: in.PlannedStart,
		PlannedEnd:   in.PlannedEnd,
		CauseCode:    in.CauseCode,
		CircuitID:    in.CircuitID,
		Notes:        in.Notes,
	})
}

func toolOMSRestoreIncident(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.ID == "" {
		return nil, fmt.Errorf("id is required")
	}
	sdb, err := asDB(db)
	if err != nil {
		return nil, err
	}
	return oms.RestoreIncident(ctx, sdb, in.ID)
}

func toolOMSCancelIncident(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.ID == "" {
		return nil, fmt.Errorf("id is required")
	}
	sdb, err := asDB(db)
	if err != nil {
		return nil, err
	}
	return oms.CancelIncident(ctx, sdb, in.ID)
}

func toolOMSGenerateSwitchingOrder(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		IncidentID string `json:"incident_id"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.IncidentID == "" {
		return nil, fmt.Errorf("incident_id is required")
	}
	sdb, err := asDB(db)
	if err != nil {
		return nil, err
	}
	return oms.GenerateSwitchingOrder(ctx, sdb, in.IncidentID)
}

func toolOMSGenerateRestorationOrder(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		IncidentID string `json:"incident_id"`
		TieClass   string `json:"tie_class"`
		TieID      string `json:"tie_id"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.IncidentID == "" || in.TieClass == "" || in.TieID == "" {
		return nil, fmt.Errorf("incident_id, tie_class and tie_id are required")
	}
	sdb, err := asDB(db)
	if err != nil {
		return nil, err
	}
	return oms.GenerateRestorationOrder(ctx, sdb, in.IncidentID, in.TieClass, in.TieID)
}

func toolOMSSetSwitchingStepExecuted(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		StepID   string `json:"step_id"`
		Executed *bool  `json:"executed"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.StepID == "" {
		return nil, fmt.Errorf("step_id is required")
	}
	// executed defaults to true (the common "tick it off" case), matching the REST
	// execute handler's absent-body default.
	executed := true
	if in.Executed != nil {
		executed = *in.Executed
	}
	sdb, err := asDB(db)
	if err != nil {
		return nil, err
	}
	return oms.SetStepExecuted(ctx, sdb, in.StepID, executed)
}

func toolOMSCancelSwitchingOrder(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.ID == "" {
		return nil, fmt.Errorf("id is required")
	}
	sdb, err := asDB(db)
	if err != nil {
		return nil, err
	}
	return oms.CancelSwitchingOrder(ctx, sdb, in.ID)
}
