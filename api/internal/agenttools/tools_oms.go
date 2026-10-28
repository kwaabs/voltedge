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
					"open_only":{"type":"boolean","description":"Return only incidents that are not yet restored (the active board); defaults to false."},
					"territory_id":{"type":"string","description":"Scope to incidents in this territory's subtree (the territory and every descendant); omit for all territories."}
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
		{
			Name: "oms_list_outage_causes",
			Description: "List the coded outage-cause catalog (oms.outage_causes) -- the coarse, " +
				"incident-level 'why' an operator picks from (equipment failure, third-party dig-in, " +
				"lightning, vegetation, ...), each with a code, a human label and a family. The same " +
				"read the REST GET /oms/outage-causes route returns. Use it to resolve a valid " +
				"cause_code before oms_set_incident_attribution. The detailed per-asset failure mode " +
				"is a separate catalog on the fault (not here). No arguments.",
			InputSchema:  json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
			RequiredPerm: auth.PermOMSRead,
			Handler:      toolOMSListOutageCauses,
		},
		{
			Name: "oms_detection_metrics",
			Description: "Summarise HOW the utility learned of its unplanned outages and WHY they " +
				"happened, over a window -- the same read the REST GET /oms/detection-metrics route " +
				"returns. Returns the detection-source mix (by_source: customer / ami / scada / crew / " +
				"upstream / analytics), the machine_detected_share (the fraction known via telemetry " +
				"WITHOUT a customer call -- the situational-awareness measure), the cause-family mix " +
				"(by_cause), and the mean detection-to-restore time. from/to (RFC3339) are optional " +
				"(default the trailing 12 months).",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"from":{"type":"string","description":"Window start (RFC3339 timestamp); defaults to 12 months before 'to'."},
					"to":{"type":"string","description":"Window end (RFC3339 timestamp); defaults to now."}
				},
				"additionalProperties":false
			}`),
			RequiredPerm: auth.PermOMSRead,
			Handler:      toolOMSDetectionMetrics,
		},
		{
			Name: "oms_list_trouble_spots",
			Description: "Cluster the OPEN, located trouble calls into geographic TROUBLE SPOTS -- " +
				"the same read the REST GET /oms/trouble-spots route returns. This is the geographic " +
				"lens: it groups nearby open calls (single-linkage within `eps` metres, default 250) " +
				"into places a crew can be dispatched to BEFORE/without prediction finding a device. " +
				"Each spot carries its member call_ids + service_point_ids, call_count, centroid " +
				"lng/lat + radius, dominant territory, dominant call_type, an AMI flag, and the " +
				"earliest/latest report time. Feed a spot's call_ids to oms_raise_incident_from_spot " +
				"to dispatch. Address-only calls with no point are excluded.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"eps":{"type":"number","description":"Cluster join distance in metres (calls within this of each other are one spot); defaults to 250."}
				},
				"additionalProperties":false
			}`),
			RequiredPerm: auth.PermOMSRead,
			Handler:      toolOMSListTroubleSpots,
		},
		{
			Name: "oms_area_impact",
			Description: "The network-wide AREA IMPACT -- how many customers are off supply right now, " +
				"rolled up the geographic hierarchy (region → district → …) across ALL open incidents at " +
				"once -- the same read the REST GET /oms/area-impact route returns. Returns open_incidents, " +
				"total_customers/total_service_points, an unassigned bucket (impact on incidents with no " +
				"territory), and levels[]: per hierarchy level, the territories with customers/service_points " +
				"off and how many incidents touch each. Use it to answer 'which areas are worst hit now'. " +
				"No arguments.",
			InputSchema:  json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
			RequiredPerm: auth.PermOMSRead,
			Handler:      toolOMSAreaImpact,
		},
		{
			Name: "oms_list_crews",
			Description: "List the field crews / resources (the same read the REST GET /oms/crews route " +
				"returns): each crew's name, kind, status (available / dispatched / on_site / unavailable), " +
				"home territory, and its current active incident (if any). Set include_inactive to also " +
				"return retired crews. Use it to find an available crew before oms_dispatch_crew.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"include_inactive":{"type":"boolean","description":"Also return retired (active=false) crews; defaults to false."}
				},
				"additionalProperties":false
			}`),
			RequiredPerm: auth.PermOMSRead,
			Handler:      toolOMSListCrews,
		},
		{
			Name: "oms_list_callbacks",
			Description: "List the customer callback queue (the same read the REST GET /oms/callbacks route " +
				"returns) -- the 'notify this supply that power is back' list enqueued per affected service " +
				"point when an outage is restored (blocker #5, the CIS/IVR integration). Each row carries the " +
				"service point, the caller's premise id / phone when one was left, the reason and the status " +
				"(pending / notified / cancelled). Filter by status (pending = the work list) and/or incident_id. " +
				"The external CIS normally pulls this to call customers; mark one done with oms_mark_callback_notified.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"status":{"type":"string","enum":["pending","notified","cancelled"],"description":"Filter by status; pending = the outstanding work list."},
					"incident_id":{"type":"string","description":"Filter to one incident's callbacks (uuid)."},
					"limit":{"type":"integer","description":"Max rows (0 = all)."},
					"offset":{"type":"integer","description":"Rows to skip (pagination)."}
				},
				"additionalProperties":false
			}`),
			RequiredPerm: auth.PermOMSRead,
			Handler:      toolOMSListCallbacks,
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
			Name: "oms_dispatch_incident",
			Description: "Advance a confirmed incident to 'dispatched' -- a crew has been sent to " +
				"the field -- the same write the REST POST /incidents/{id}/dispatch route performs " +
				"(the outage board's dispatch action). Only a confirmed incident can be dispatched. " +
				"Returns the updated incident. This WRITES to the database: confirm with a human " +
				"before calling.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"id":{"type":"string","description":"The incident's id (uuid); must be confirmed."}
				},
				"required":["id"],
				"additionalProperties":false
			}`),
			Annotations: &Annotations{
				Title:           "Dispatch incident",
				ReadOnlyHint:    &falseHint, // this tool writes
				DestructiveHint: &falseHint, // a status advance, not a removal
				IdempotentHint:  &falseHint, // an already-dispatched incident can't be dispatched again
				OpenWorldHint:   &falseHint, // acts only on this system's own database
			},
			RequiredPerm: auth.PermOMSWrite,
			Handler:      toolOMSDispatchIncident,
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
		{
			Name: "oms_set_incident_attribution",
			Description: "Refine an incident's CAUSE (why it happened) and/or correct its DETECTION " +
				"SOURCE (how the utility learned of it) -- the same write the REST PATCH " +
				"/oms/incidents/{id}/attribution route performs. A partial update: only the supplied " +
				"fields change. cause_code is a code from oms_list_outage_causes (an empty string " +
				"clears the cause); cause_status is unknown / suspected / confirmed; detection_source " +
				"is customer / ami / scada / crew / planned / upstream / analytics; detection_detail is " +
				"a free provenance note. Assigning a cause with no explicit status implies 'suspected'. " +
				"Returns the updated incident. This WRITES to the database: confirm with a human before calling.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"id":{"type":"string","description":"The incident's id (uuid) to attribute."},
					"cause_code":{"type":"string","description":"A code from oms_list_outage_causes (e.g. LIGHTNING); an empty string clears the cause."},
					"cause_status":{"type":"string","description":"Cause certainty: unknown, suspected or confirmed."},
					"detection_source":{"type":"string","description":"How the outage was detected: customer, ami, scada, crew, planned, upstream or analytics."},
					"detection_detail":{"type":"string","description":"Optional free provenance note (channel / external id); an empty string clears it."}
				},
				"required":["id"],
				"additionalProperties":false
			}`),
			Annotations: &Annotations{
				Title:           "Set incident cause / source",
				ReadOnlyHint:    &falseHint, // this tool writes
				DestructiveHint: &falseHint, // it refines attribution, it doesn't remove the incident
				IdempotentHint:  &trueHint,  // re-applying the same attribution yields the same state
				OpenWorldHint:   &falseHint, // acts only on this system's own database
			},
			RequiredPerm: auth.PermOMSWrite,
			Handler:      toolOMSSetIncidentAttribution,
		},
		{
			Name: "oms_raise_incident_from_spot",
			Description: "Dispatch a crew to a geographic trouble spot by raising a CONFIRMED, " +
				"UNATTRIBUTED incident from its open calls -- the same write the REST POST " +
				"/oms/trouble-spots/raise route performs. Pass the call_ids of one trouble spot (from " +
				"oms_list_trouble_spots). It makes ONE incident with no device yet (attribute one " +
				"later, or raise a fault), links the calls, and stamps the detection source, " +
				"detected_at, dominant territory and an affected estimate from them. Use this (not " +
				"oms_run_prediction) for no-device calls, so separate neighbourhoods become separate " +
				"incidents instead of one lump. Returns the created incident. This WRITES: confirm with a human before calling.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"call_ids":{"type":"array","items":{"type":"string"},"description":"The open trouble call ids of the spot to dispatch to (from a oms_list_trouble_spots spot)."}
				},
				"required":["call_ids"],
				"additionalProperties":false
			}`),
			Annotations: &Annotations{
				Title:           "Dispatch crew to trouble spot",
				ReadOnlyHint:    &falseHint, // this tool writes
				DestructiveHint: &falseHint, // a create is additive
				IdempotentHint:  &falseHint, // repeating it raises another incident from the same calls (now associated)
				OpenWorldHint:   &falseHint, // acts only on this system's own database
			},
			RequiredPerm: auth.PermOMSWrite,
			Handler:      toolOMSRaiseIncidentFromSpot,
		},
		{
			Name: "oms_attribute_incident_device",
			Description: "Set or CHANGE the protective device an incident is blamed on -- the operator " +
				"override prediction can't do (the same write the REST PATCH /oms/incidents/{id}/device " +
				"route performs). Use it to attribute a device to an UNATTRIBUTED incident (so it becomes " +
				"switchable), or to correct a mis-blamed one. It re-traces the new device's downstream " +
				"cone and re-stamps the incident's affected counts + dominant territory, keeps its linked " +
				"calls, clears the confidence (a human attribution is not a prediction), and re-freezes a " +
				"confirmed incident's interruptions from the new cone. A restored/cancelled incident can't " +
				"be re-attributed. Returns the updated incident. This WRITES: confirm with a human before calling.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"id":{"type":"string","description":"The incident's id (uuid) to attribute a device to."},
					"device_class":{"type":"string","description":"The protective/switchable device's feature class (e.g. MV_RECLOSER)."},
					"device_id":{"type":"string","description":"The device's feature id (uuid)."}
				},
				"required":["id","device_class","device_id"],
				"additionalProperties":false
			}`),
			Annotations: &Annotations{
				Title:           "Attribute device to incident",
				ReadOnlyHint:    &falseHint, // this tool writes
				DestructiveHint: &falseHint, // it corrects attribution; re-freezes interruptions
				IdempotentHint:  &trueHint,  // attributing the same device again yields the same state
				OpenWorldHint:   &falseHint, // acts only on this system's own database
			},
			RequiredPerm: auth.PermOMSWrite,
			Handler:      toolOMSAttributeIncidentDevice,
		},
		{
			Name: "oms_mark_callback_notified",
			Description: "Mark a customer callback done once the customer has been reached (the same write the " +
				"REST POST /oms/callbacks/{id}/notified route performs, blocker #5). Stamps who and when; an " +
				"optional note records the outcome (e.g. 'left voicemail'). A cancelled callback is refused. " +
				"This WRITES: confirm with a human before calling.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"id":{"type":"string","description":"The callback's id (uuid)."},
					"note":{"type":"string","description":"Optional outcome note."}
				},
				"required":["id"],
				"additionalProperties":false
			}`),
			Annotations:  &Annotations{Title: "Mark callback notified", ReadOnlyHint: &falseHint, DestructiveHint: &falseHint, IdempotentHint: &trueHint, OpenWorldHint: &falseHint},
			RequiredPerm: auth.PermOMSWrite,
			Handler:      toolOMSMarkCallbackNotified,
		},
		{
			Name: "oms_dispatch_crew",
			Description: "Send a crew to an incident (the same write the REST POST /oms/incidents/{id}/crews " +
				"route performs): creates a 'dispatched' assignment, marks the crew dispatched, and -- if the " +
				"incident is confirmed -- advances it to 'dispatched'. Optionally raises a crew-assigned " +
				"restoration work order for the incident (raise_work_order), pinned to the blamed device, which " +
				"reaches every crew member's field pack. A crew already on an active assignment, or a closed " +
				"incident, is refused. Returns the assignment (with work_order_id when one was raised). This " +
				"WRITES: confirm with a human before calling.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"incident_id":{"type":"string","description":"The incident (uuid) to dispatch the crew to."},
					"crew_id":{"type":"string","description":"The crew (uuid) to dispatch (from oms_list_crews; must be available)."},
					"note":{"type":"string","description":"Optional dispatch note."},
					"raise_work_order":{"type":"boolean","description":"Also raise a crew-assigned restoration work order for the incident; defaults to false."},
					"work_order_title":{"type":"string","description":"Optional title for the raised work order; defaults to 'Restore outage …'."}
				},
				"required":["incident_id","crew_id"],
				"additionalProperties":false
			}`),
			Annotations:  &Annotations{Title: "Dispatch crew", ReadOnlyHint: &falseHint, DestructiveHint: &falseHint, IdempotentHint: &falseHint, OpenWorldHint: &falseHint},
			RequiredPerm: auth.PermOMSWrite,
			Handler:      toolOMSDispatchCrew,
		},
		{
			Name: "oms_advance_crew_assignment",
			Description: "Move a crew assignment forward (the same write the REST PATCH /oms/crew-assignments/{id} " +
				"route performs): dispatched → on_site → cleared. Stamps the transition time and frees the crew when " +
				"cleared (if it has no other active assignment). Backwards moves are refused. Returns the assignment. " +
				"This WRITES: confirm with a human before calling.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"id":{"type":"string","description":"The crew assignment (uuid) to advance (from oms_list_incident_crews via the incident, or a dispatch result)."},
					"status":{"type":"string","description":"The new status: on_site or cleared."}
				},
				"required":["id","status"],
				"additionalProperties":false
			}`),
			Annotations:  &Annotations{Title: "Advance crew assignment", ReadOnlyHint: &falseHint, DestructiveHint: &falseHint, IdempotentHint: &falseHint, OpenWorldHint: &falseHint},
			RequiredPerm: auth.PermOMSWrite,
			Handler:      toolOMSAdvanceCrewAssignment,
		},
		{
			Name: "oms_set_incident_etr",
			Description: "Set or clear an incident's estimated time of restoration (the same write the REST " +
				"PATCH /oms/incidents/{id}/etr route performs). Pass etr as an RFC3339 timestamp to set it, or " +
				"omit it (null) to clear. A closed incident is refused. Returns the updated incident. This WRITES: " +
				"confirm with a human before calling.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"id":{"type":"string","description":"The incident (uuid) to set the ETR on."},
					"etr":{"type":"string","description":"Estimated restoration time (RFC3339); omit to clear the ETR."}
				},
				"required":["id"],
				"additionalProperties":false
			}`),
			Annotations:  &Annotations{Title: "Set incident ETR", ReadOnlyHint: &falseHint, DestructiveHint: &falseHint, IdempotentHint: &trueHint, OpenWorldHint: &falseHint},
			RequiredPerm: auth.PermOMSWrite,
			Handler:      toolOMSSetIncidentETR,
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
	calls, total, err := oms.ListTroubleCalls(ctx, db, oms.TroubleCallFilter{Status: in.Status})
	if err != nil {
		return nil, err
	}
	return map[string]any{"trouble_calls": calls, "count": len(calls), "total": total}, nil
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
		Status      string `json:"status"`
		OpenOnly    bool   `json:"open_only"`
		TerritoryID string `json:"territory_id"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	incidents, err := oms.ListIncidents(ctx, db, in.Status, in.OpenOnly, in.TerritoryID)
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

func toolOMSListOutageCauses(ctx context.Context, db bun.IDB, _ json.RawMessage) (any, error) {
	causes, err := oms.ListOutageCauses(ctx, db)
	if err != nil {
		return nil, err
	}
	return map[string]any{"outage_causes": causes}, nil
}

func toolOMSDetectionMetrics(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		From *time.Time `json:"from"`
		To   *time.Time `json:"to"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	return oms.ComputeDetectionMetrics(ctx, db, in.From, in.To)
}

func toolOMSListTroubleSpots(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		Eps float64 `json:"eps"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	spots, err := oms.ClusterTroubleSpots(ctx, db, in.Eps)
	if err != nil {
		return nil, err
	}
	return map[string]any{"trouble_spots": spots}, nil
}

func toolOMSAreaImpact(ctx context.Context, db bun.IDB, _ json.RawMessage) (any, error) {
	return oms.ComputeAreaImpact(ctx, db)
}

func toolOMSListCrews(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		IncludeInactive bool `json:"include_inactive"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	crews, err := oms.ListCrews(ctx, db, in.IncludeInactive)
	if err != nil {
		return nil, err
	}
	return map[string]any{"crews": crews}, nil
}

func toolOMSListCallbacks(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		Status     string `json:"status"`
		IncidentID string `json:"incident_id"`
		Limit      int    `json:"limit"`
		Offset     int    `json:"offset"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	callbacks, err := oms.ListCallbacks(ctx, db, oms.CallbackFilter{
		Status:     in.Status,
		IncidentID: in.IncidentID,
		Limit:      in.Limit,
		Offset:     in.Offset,
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"callbacks": callbacks}, nil
}

func toolOMSMarkCallbackNotified(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		ID   string  `json:"id"`
		Note *string `json:"note"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.ID == "" {
		return nil, fmt.Errorf("id is required")
	}
	return oms.MarkCallbackNotified(ctx, db, in.ID, in.Note)
}

func toolOMSDispatchCrew(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		IncidentID     string  `json:"incident_id"`
		CrewID         string  `json:"crew_id"`
		Note           *string `json:"note"`
		RaiseWorkOrder bool    `json:"raise_work_order"`
		WorkOrderTitle *string `json:"work_order_title"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.IncidentID == "" || in.CrewID == "" {
		return nil, fmt.Errorf("incident_id and crew_id are required")
	}
	sdb, err := asDB(db)
	if err != nil {
		return nil, err
	}
	return oms.DispatchCrewToIncident(ctx, sdb, in.CrewID, in.IncidentID, in.Note,
		oms.DispatchOptions{RaiseWorkOrder: in.RaiseWorkOrder, WorkOrderTitle: in.WorkOrderTitle})
}

func toolOMSAdvanceCrewAssignment(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.ID == "" || in.Status == "" {
		return nil, fmt.Errorf("id and status are required")
	}
	sdb, err := asDB(db)
	if err != nil {
		return nil, err
	}
	return oms.AdvanceCrewAssignment(ctx, sdb, in.ID, in.Status)
}

func toolOMSSetIncidentETR(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		ID  string     `json:"id"`
		ETR *time.Time `json:"etr"`
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
	return oms.SetIncidentETR(ctx, sdb, in.ID, in.ETR)
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

func toolOMSDispatchIncident(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
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
	return oms.DispatchIncident(ctx, sdb, in.ID)
}

func toolOMSSetIncidentAttribution(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		ID              string  `json:"id"`
		CauseCode       *string `json:"cause_code"`
		CauseStatus     *string `json:"cause_status"`
		DetectionSource *string `json:"detection_source"`
		DetectionDetail *string `json:"detection_detail"`
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
	return oms.SetIncidentAttribution(ctx, sdb, in.ID, oms.SetAttributionParams{
		CauseCode:       in.CauseCode,
		CauseStatus:     in.CauseStatus,
		DetectionSource: in.DetectionSource,
		DetectionDetail: in.DetectionDetail,
	})
}

func toolOMSRaiseIncidentFromSpot(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		CallIDs []string `json:"call_ids"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if len(in.CallIDs) == 0 {
		return nil, fmt.Errorf("call_ids is required")
	}
	sdb, err := asDB(db)
	if err != nil {
		return nil, err
	}
	return oms.RaiseIncidentFromCalls(ctx, sdb, in.CallIDs)
}

func toolOMSAttributeIncidentDevice(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		ID          string `json:"id"`
		DeviceClass string `json:"device_class"`
		DeviceID    string `json:"device_id"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.ID == "" || in.DeviceClass == "" || in.DeviceID == "" {
		return nil, fmt.Errorf("id, device_class and device_id are required")
	}
	sdb, err := asDB(db)
	if err != nil {
		return nil, err
	}
	return oms.AttributeIncidentDevice(ctx, sdb, in.ID, in.DeviceClass, in.DeviceID)
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
