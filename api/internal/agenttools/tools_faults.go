package agenttools

// The Faults module's tools -- the fault-event surface exposed through the same
// single agenttools registry as every other activity. A fault is an ELECTRICAL
// FAULT EVENT on the network (an earth fault, a phase-to-phase short, a
// protection operation), the causal hinge between an OMS outage and a WAM
// defect. Each tool here is a thin adapter over an already-exported
// api/internal/faults engine function -- the same code the REST /faults routes
// and the faults board / feature-faults-tab frontend call -- and declares the
// RBAC permission that route requires: the GET verbs read (faults:read), the
// mutating verbs write (faults:write). The read tools carry nil annotations
// (buildRegistry stamps readOnlyHint:true); the write tools carry explicit
// annotations (readOnlyHint:false) so a client confirms with a human first.

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/kwaabs/voltedge/api/internal/auth"
	"github.com/kwaabs/voltedge/api/internal/faults"
	"github.com/uptrace/bun"
)

// faultsTools returns the Faults tools in advertised order: the reads first
// (list / get / history / map / impact), then the writes (create / set-status /
// update / raise-from-incident / book-defect).
func faultsTools() []Tool {
	return []Tool{
		{
			Name: "faults_list",
			Description: "List logged fault events newest-first -- the fault board's data, the same " +
				"query the REST GET /faults route returns. Every filter is optional: narrow by " +
				"status, fault_type, the affected asset (asset_class + asset_id), or a linked OMS " +
				"incident_id, or set open=true for only faults still on the network (neither cleared " +
				"nor closed). Each row is the full fault event.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"status":{"type":"string","enum":["detected","investigating","cleared","closed"],"description":"Filter to a single lifecycle status."},
					"fault_type":{"type":"string","enum":["SLG","LL","LLG","LLL","LLLG","earth","unknown"],"description":"Filter to a single electrical classification."},
					"asset_class":{"type":"string","description":"Filter to faults on this feature class (pair with asset_id)."},
					"asset_id":{"type":"string","description":"Filter to faults on this asset's id (uuid; pair with asset_class)."},
					"incident_id":{"type":"string","description":"Filter to faults linked to this OMS incident (uuid)."},
					"open":{"type":"boolean","description":"When true, return only faults still on the network (not cleared or closed); overrides status."}
				},
				"additionalProperties":false
			}`),
			RequiredPerm: auth.PermFaultsRead,
			Handler:      toolFaultsList,
		},
		{
			Name: "faults_get",
			Description: "Fetch a single fault event by its id -- the same read the REST GET /faults/{id} " +
				"route returns. Returns the full fault event (classification, cause, protective device, " +
				"timings, status, the linked incident_id / defect_id, and its resolved location).",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"id":{"type":"string","description":"The fault event's id (uuid)."}
				},
				"required":["id"],
				"additionalProperties":false
			}`),
			RequiredPerm: auth.PermFaultsRead,
			Handler:      toolFaultsGet,
		},
		{
			Name: "faults_list_history",
			Description: "List a fault's change timeline newest-first -- its audit trail (who changed what, " +
				"when), the same read the REST GET /faults/{id}/history route returns. Each entry names " +
				"the action (created, status_changed, updated, defect_booked), the acting user, when it " +
				"happened, and the full post-change fault snapshot.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"fault_id":{"type":"string","description":"The fault event's id (uuid)."}
				},
				"required":["fault_id"],
				"additionalProperties":false
			}`),
			RequiredPerm: auth.PermFaultsRead,
			Handler:      toolFaultsListHistory,
		},
		{
			Name: "faults_list_map",
			Description: "List faults that have a placeable map point -- the fault map overlay's data, the " +
				"same query the REST GET /faults/map route returns. Takes the same filters as faults_list " +
				"but omits any fault with no resolvable point (an incident-only fault, or one whose asset " +
				"is uncompiled / deleted / geometry-less and carries no pin). Each marker carries its " +
				"resolved longitude/latitude and, for an asset-anchored fault, the asset's geometry.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"status":{"type":"string","enum":["detected","investigating","cleared","closed"],"description":"Filter to a single lifecycle status."},
					"fault_type":{"type":"string","enum":["SLG","LL","LLG","LLL","LLLG","earth","unknown"],"description":"Filter to a single electrical classification."},
					"asset_class":{"type":"string","description":"Filter to faults on this feature class (pair with asset_id)."},
					"asset_id":{"type":"string","description":"Filter to faults on this asset's id (uuid; pair with asset_class)."},
					"incident_id":{"type":"string","description":"Filter to faults linked to this OMS incident (uuid)."},
					"open":{"type":"boolean","description":"When true, return only faults still on the network (not cleared or closed); overrides status."}
				},
				"additionalProperties":false
			}`),
			RequiredPerm: auth.PermFaultsRead,
			Handler:      toolFaultsListMap,
		},
		{
			Name: "faults_impact_area",
			Description: "Compute a fault's de-energized downstream section -- every customer supply point " +
				"fed through the fault's protective device, the cone it blacked out when it operated, the " +
				"same read the REST GET /faults/{id}/impact route returns (and the map's impact overlay). " +
				"A fault with no protective device assigned yields an empty (but non-null) set. Read-only: " +
				"a barrier-free topological trace, it changes nothing.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"fault_id":{"type":"string","description":"The fault event's id (uuid)."}
				},
				"required":["fault_id"],
				"additionalProperties":false
			}`),
			RequiredPerm: auth.PermFaultsRead,
			Handler:      toolFaultsImpactArea,
		},
		{
			Name: "faults_create",
			Description: "Log a new fault event -- the same validated write the REST POST /faults route " +
				"performs (the fault board's Log-fault action). A title is required, and the fault must " +
				"be anchored by at least one of: an affected asset (asset_class + asset_id together), a " +
				"location (longitude + latitude together), or a linked OMS incident_id. It starts in the " +
				"'detected' status. Returns the created fault event (including its new id). This WRITES " +
				"to the database: confirm with a human before calling.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"title":{"type":"string","description":"Required short title for the fault."},
					"reference":{"type":"string","description":"Optional external reference."},
					"asset_class":{"type":"string","description":"The affected feature class code (pair with asset_id)."},
					"asset_id":{"type":"string","description":"The affected asset's id (uuid; pair with asset_class)."},
					"longitude":{"type":"number","description":"Location longitude (EPSG:4326; pair with latitude)."},
					"latitude":{"type":"number","description":"Location latitude (EPSG:4326; pair with longitude)."},
					"fault_type":{"type":"string","enum":["SLG","LL","LLG","LLL","LLLG","earth","unknown"],"description":"Electrical classification of the fault."},
					"cause":{"type":"string","description":"Free-text cause."},
					"fault_current_a":{"type":"number","description":"Measured or computed fault current, in amps."},
					"protective_device_class":{"type":"string","description":"Feature class of the device that operated (pair with protective_device_id)."},
					"protective_device_id":{"type":"string","description":"Id (uuid) of the device that operated (pair with protective_device_class)."},
					"clearing_time_ms":{"type":"number","description":"Time the device took to clear the fault, in milliseconds."},
					"detected_by":{"type":"string","description":"Who or what detected the fault."},
					"detected_at":{"type":"string","format":"date-time","description":"When the fault was detected (RFC3339); defaults to now."},
					"incident_id":{"type":"string","description":"Linked OMS incident id (uuid) this fault caused."},
					"defect_id":{"type":"string","description":"Linked WAM defect id (uuid) this fault booked."},
					"description":{"type":"string","description":"Longer description."},
					"notes":{"type":"string","description":"Free-text notes."}
				},
				"required":["title"],
				"additionalProperties":false
			}`),
			Annotations: &Annotations{
				Title:           "Log fault",
				ReadOnlyHint:    &falseHint, // this tool writes
				DestructiveHint: &falseHint, // a create is additive, reversible by closing/clearing the fault
				IdempotentHint:  &falseHint, // repeating it logs another fault
				OpenWorldHint:   &falseHint, // acts only on this system's own database
			},
			RequiredPerm: auth.PermFaultsWrite,
			Handler:      toolFaultsCreate,
		},
		{
			Name: "faults_set_status",
			Description: "Transition a fault to a new lifecycle status -- the same write the REST POST " +
				"/faults/{id}/status route performs (the board's status control). Statuses walk detected " +
				"-> investigating -> cleared (off the network) -> closed; moving to a terminal status " +
				"stamps cleared_at, and reopening clears it. Idempotent: setting the status it already " +
				"has re-applies the same state. Returns the updated fault event. This WRITES to the " +
				"database: confirm with a human before calling.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"id":{"type":"string","description":"The fault event's id (uuid)."},
					"status":{"type":"string","enum":["detected","investigating","cleared","closed"],"description":"The new lifecycle status."}
				},
				"required":["id","status"],
				"additionalProperties":false
			}`),
			Annotations: &Annotations{
				Title:           "Set fault status",
				ReadOnlyHint:    &falseHint, // this tool writes
				DestructiveHint: &falseHint, // a status change, reversible by transitioning again
				IdempotentHint:  &trueHint,  // setting the current status re-applies the same state
				OpenWorldHint:   &falseHint, // acts only on this system's own database
			},
			RequiredPerm: auth.PermFaultsWrite,
			Handler:      toolFaultsSetStatus,
		},
		{
			Name: "faults_update",
			Description: "Patch a fault's investigation findings -- the same sparse write the REST PATCH " +
				"/faults/{id} route performs. Every field is optional and only what you send is changed; " +
				"the status is not patched here (use faults_set_status, which keeps cleared_at " +
				"consistent). A protective device is all-or-nothing: send both class and id together, or " +
				"neither. Returns the updated fault event. This WRITES to the database: confirm with a " +
				"human before calling.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"id":{"type":"string","description":"The fault event's id (uuid)."},
					"reference":{"type":"string","description":"New external reference."},
					"fault_type":{"type":"string","enum":["SLG","LL","LLG","LLL","LLLG","earth","unknown"],"description":"New electrical classification; send an empty string to clear it."},
					"cause":{"type":"string","description":"New free-text cause."},
					"fault_current_a":{"type":"number","description":"New fault current, in amps."},
					"protective_device_class":{"type":"string","description":"New protective-device class (send with protective_device_id; empty strings clear both)."},
					"protective_device_id":{"type":"string","description":"New protective-device id (uuid; send with protective_device_class; empty strings clear both)."},
					"clearing_time_ms":{"type":"number","description":"New clearing time, in milliseconds."},
					"detected_by":{"type":"string","description":"New detected-by value."},
					"incident_id":{"type":"string","description":"New linked OMS incident id (uuid); send an empty string to unlink."},
					"title":{"type":"string","description":"New title (cannot be blank)."},
					"description":{"type":"string","description":"New description."},
					"notes":{"type":"string","description":"New free-text notes."}
				},
				"required":["id"],
				"additionalProperties":false
			}`),
			Annotations: &Annotations{
				Title:           "Update fault",
				ReadOnlyHint:    &falseHint, // this tool writes
				DestructiveHint: &falseHint, // a field patch overwrites, it doesn't remove the fault
				IdempotentHint:  &trueHint,  // re-applying the same patch yields the same state
				OpenWorldHint:   &falseHint, // acts only on this system's own database
			},
			RequiredPerm: auth.PermFaultsWrite,
			Handler:      toolFaultsUpdate,
		},
		{
			Name: "faults_raise_from_incident",
			Description: "Mint a fault event for an existing OMS incident and link the two, atomically -- " +
				"the same composite write the REST POST /faults/from-incident/{id} route performs (the " +
				"outages board's Raise-fault action). The fault inherits the incident's predicted " +
				"protective device and cause and is anchored by the incident_id; all overrides are " +
				"optional. Returns the created fault event. This WRITES to the database: confirm with a " +
				"human before calling.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"incident_id":{"type":"string","description":"The OMS incident's id (uuid) to raise the fault from."},
					"title":{"type":"string","description":"Override the fault title (defaults from the incident)."},
					"fault_type":{"type":"string","enum":["SLG","LL","LLG","LLL","LLLG","earth","unknown"],"description":"Electrical classification of the fault."},
					"cause":{"type":"string","description":"Override the cause (defaults to the incident's cause)."},
					"detected_by":{"type":"string","description":"Who or what detected the fault."},
					"notes":{"type":"string","description":"Free-text notes."}
				},
				"required":["incident_id"],
				"additionalProperties":false
			}`),
			Annotations: &Annotations{
				Title:           "Raise fault from incident",
				ReadOnlyHint:    &falseHint, // this tool writes
				DestructiveHint: &falseHint, // additive: it mints a fault and links it, removing nothing
				IdempotentHint:  &falseHint, // each call raises another fault for the incident
				OpenWorldHint:   &falseHint, // acts only on this system's own database
			},
			RequiredPerm: auth.PermFaultsWrite,
			Handler:      toolFaultsRaiseFromIncident,
		},
		{
			Name: "faults_book_defect",
			Description: "Book a WAM defect for the asset a fault hit and link it back onto the fault, " +
				"atomically -- the same composite write the REST POST /faults/{id}/defect route performs " +
				"(the fault board's Book-defect action). The fault must name an asset or a location, and " +
				"must not already have a booked defect; the defect is sourced 'outage' and carries the " +
				"fault's incident_id so it flows into WAM's work-order loop. All overrides are optional. " +
				"Returns the updated fault event (now carrying defect_id). This WRITES to the database: " +
				"confirm with a human before calling.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"fault_id":{"type":"string","description":"The fault event's id (uuid) to book a defect from."},
					"title":{"type":"string","description":"Override the defect title (defaults from the fault)."},
					"defect_type":{"type":"string","description":"The defect type."},
					"severity":{"type":"string","description":"The defect severity (defaults to WAM's medium)."},
					"description":{"type":"string","description":"Defect description."},
					"reported_by":{"type":"string","description":"Who reported the defect."}
				},
				"required":["fault_id"],
				"additionalProperties":false
			}`),
			Annotations: &Annotations{
				Title:           "Book defect from fault",
				ReadOnlyHint:    &falseHint, // this tool writes
				DestructiveHint: &falseHint, // additive: it mints a defect and links it, removing nothing
				IdempotentHint:  &falseHint, // a fault can only book one defect; a second call is rejected
				OpenWorldHint:   &falseHint, // acts only on this system's own database
			},
			RequiredPerm: auth.PermFaultsWrite,
			Handler:      toolFaultsBookDefect,
		},
	}
}

// --- handlers ---

// faultFilterIn is the shared filter body of faults_list / faults_list_map; its
// fields mirror the REST /faults query parameters and map straight onto
// faults.FaultFilter.
type faultFilterIn struct {
	Status     string `json:"status"`
	FaultType  string `json:"fault_type"`
	AssetClass string `json:"asset_class"`
	AssetID    string `json:"asset_id"`
	IncidentID string `json:"incident_id"`
	Open       bool   `json:"open"`
}

func (in faultFilterIn) filter() faults.FaultFilter {
	return faults.FaultFilter{
		Status:     in.Status,
		FaultType:  in.FaultType,
		AssetClass: in.AssetClass,
		AssetID:    in.AssetID,
		IncidentID: in.IncidentID,
		Open:       in.Open,
	}
}

func toolFaultsList(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in faultFilterIn
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	events, err := faults.ListFaults(ctx, db, in.filter())
	if err != nil {
		return nil, err
	}
	return map[string]any{"faults": events, "count": len(events)}, nil
}

func toolFaultsGet(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.ID == "" {
		return nil, fmt.Errorf("id is required")
	}
	return faults.GetFault(ctx, db, in.ID)
}

func toolFaultsListHistory(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		FaultID string `json:"fault_id"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.FaultID == "" {
		return nil, fmt.Errorf("fault_id is required")
	}
	entries, err := faults.ListFaultHistory(ctx, db, in.FaultID)
	if err != nil {
		return nil, err
	}
	return map[string]any{"history": entries, "count": len(entries)}, nil
}

func toolFaultsListMap(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in faultFilterIn
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	markers, err := faults.ListFaultsForMap(ctx, db, in.filter())
	if err != nil {
		return nil, err
	}
	return map[string]any{"faults": markers, "count": len(markers)}, nil
}

func toolFaultsImpactArea(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		FaultID string `json:"fault_id"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.FaultID == "" {
		return nil, fmt.Errorf("fault_id is required")
	}
	return faults.FaultImpactArea(ctx, db, in.FaultID)
}

func toolFaultsCreate(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		Title                 string   `json:"title"`
		Reference             *string  `json:"reference"`
		AssetClass            *string  `json:"asset_class"`
		AssetID               *string  `json:"asset_id"`
		Longitude             *float64 `json:"longitude"`
		Latitude              *float64 `json:"latitude"`
		FaultType             *string  `json:"fault_type"`
		Cause                 *string  `json:"cause"`
		FaultCurrentA         *float64 `json:"fault_current_a"`
		ProtectiveDeviceClass *string  `json:"protective_device_class"`
		ProtectiveDeviceID    *string  `json:"protective_device_id"`
		ClearingTimeMS        *float64 `json:"clearing_time_ms"`
		DetectedBy            *string  `json:"detected_by"`
		DetectedAt            *string  `json:"detected_at"`
		IncidentID            *string  `json:"incident_id"`
		DefectID              *string  `json:"defect_id"`
		Description           *string  `json:"description"`
		Notes                 *string  `json:"notes"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.Title == "" {
		return nil, fmt.Errorf("title is required")
	}

	p := faults.CreateFaultParams{
		Title:                 in.Title,
		Reference:             in.Reference,
		AssetClass:            in.AssetClass,
		AssetID:               in.AssetID,
		Longitude:             in.Longitude,
		Latitude:              in.Latitude,
		FaultType:             in.FaultType,
		Cause:                 in.Cause,
		FaultCurrentA:         in.FaultCurrentA,
		ProtectiveDeviceClass: in.ProtectiveDeviceClass,
		ProtectiveDeviceID:    in.ProtectiveDeviceID,
		ClearingTimeMS:        in.ClearingTimeMS,
		DetectedBy:            in.DetectedBy,
		IncidentID:            in.IncidentID,
		DefectID:              in.DefectID,
		Description:           in.Description,
		Notes:                 in.Notes,
	}
	// detected_at arrives as an RFC3339 string; parse it into the *time.Time the
	// engine wants, so a bad timestamp is a clear tool error, not a 500.
	if in.DetectedAt != nil && *in.DetectedAt != "" {
		t, err := time.Parse(time.RFC3339, *in.DetectedAt)
		if err != nil {
			return nil, fmt.Errorf("invalid detected_at (want RFC3339): %w", err)
		}
		p.DetectedAt = &t
	}

	// The engine re-validates the anchor rule (asset, location, or incident) and
	// the fault_type/all-or-nothing pairs; its 400s surface in-band as the tool
	// error. The acting user is on ctx for the history stamp.
	return faults.CreateFault(ctx, db, p)
}

func toolFaultsSetStatus(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
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
	// The engine validates the status value and keeps cleared_at consistent.
	return faults.SetFaultStatus(ctx, db, in.ID, in.Status)
}

func toolFaultsUpdate(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		ID                    string   `json:"id"`
		Reference             *string  `json:"reference"`
		FaultType             *string  `json:"fault_type"`
		Cause                 *string  `json:"cause"`
		FaultCurrentA         *float64 `json:"fault_current_a"`
		ProtectiveDeviceClass *string  `json:"protective_device_class"`
		ProtectiveDeviceID    *string  `json:"protective_device_id"`
		ClearingTimeMS        *float64 `json:"clearing_time_ms"`
		DetectedBy            *string  `json:"detected_by"`
		IncidentID            *string  `json:"incident_id"`
		Title                 *string  `json:"title"`
		Description           *string  `json:"description"`
		Notes                 *string  `json:"notes"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.ID == "" {
		return nil, fmt.Errorf("id is required")
	}
	// The engine applies only the non-nil fields and enforces the protective-device
	// pairing / non-blank-title rules; its 400s surface in-band as the tool error.
	return faults.UpdateFault(ctx, db, in.ID, faults.UpdateFaultParams{
		Reference:             in.Reference,
		FaultType:             in.FaultType,
		Cause:                 in.Cause,
		FaultCurrentA:         in.FaultCurrentA,
		ProtectiveDeviceClass: in.ProtectiveDeviceClass,
		ProtectiveDeviceID:    in.ProtectiveDeviceID,
		ClearingTimeMS:        in.ClearingTimeMS,
		DetectedBy:            in.DetectedBy,
		IncidentID:            in.IncidentID,
		Title:                 in.Title,
		Description:           in.Description,
		Notes:                 in.Notes,
	})
}

func toolFaultsRaiseFromIncident(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		IncidentID string  `json:"incident_id"`
		Title      *string `json:"title"`
		FaultType  *string `json:"fault_type"`
		Cause      *string `json:"cause"`
		DetectedBy *string `json:"detected_by"`
		Notes      *string `json:"notes"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.IncidentID == "" {
		return nil, fmt.Errorf("incident_id is required")
	}
	// RaiseFaultFromIncident owns its transaction, so it needs the concrete *bun.DB.
	sdb, err := asDB(db)
	if err != nil {
		return nil, err
	}
	// The engine reads the incident cross-schema and mints + links the fault
	// atomically; a missing incident surfaces in-band as the tool error.
	return faults.RaiseFaultFromIncident(ctx, sdb, in.IncidentID, faults.RaiseFaultFromIncidentParams{
		Title:      in.Title,
		FaultType:  in.FaultType,
		Cause:      in.Cause,
		DetectedBy: in.DetectedBy,
		Notes:      in.Notes,
	})
}

func toolFaultsBookDefect(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		FaultID     string  `json:"fault_id"`
		Title       *string `json:"title"`
		DefectType  *string `json:"defect_type"`
		Severity    *string `json:"severity"`
		Description *string `json:"description"`
		ReportedBy  *string `json:"reported_by"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.FaultID == "" {
		return nil, fmt.Errorf("fault_id is required")
	}
	// BookDefectFromFault owns its transaction, so it needs the concrete *bun.DB.
	sdb, err := asDB(db)
	if err != nil {
		return nil, err
	}
	// The engine mints a WAM defect for the fault's asset/location and links it
	// back atomically; an already-booked or unanchored fault surfaces in-band.
	return faults.BookDefectFromFault(ctx, sdb, in.FaultID, faults.BookDefectFromFaultParams{
		Title:       in.Title,
		DefectType:  in.DefectType,
		Severity:    in.Severity,
		Description: in.Description,
		ReportedBy:  in.ReportedBy,
	})
}
