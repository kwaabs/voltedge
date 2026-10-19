package agenttools

// The device-operation log's tools -- the operation surface exposed through the
// same single agenttools registry as every other activity. A device operation is
// one time a protective/switching device operated on the network (a trip,
// lockout, reclose, manual open/close). Each tool is a thin adapter over an
// already-exported api/internal/deviceops function -- the same code the REST
// /operations routes and the operations board / per-device panel frontend call --
// gated by exactly the RBAC permission that route needs: the GET verbs read
// (operations:read), the record verb writes (operations:write).

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/kwaabs/voltedge/api/internal/auth"
	"github.com/kwaabs/voltedge/api/internal/deviceops"
	"github.com/uptrace/bun"
)

func operationsTools() []Tool {
	return []Tool{
		{
			Name: "operations_list",
			Description: "List device operations newest-first -- the operations board's data, the same " +
				"query the REST GET /operations route returns. Every filter is optional: narrow by the " +
				"device (device_class + device_id), the op_type (trip/lockout/reclose/open/close/earth/" +
				"alarm/other), or the source (crew/scada/relay/switching_order/derived/other). Each row " +
				"is one logged operation.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"device_class":{"type":"string","description":"Filter to operations on this feature class (pair with device_id)."},
					"device_id":{"type":"string","description":"Filter to operations on this device's id (uuid)."},
					"op_type":{"type":"string","enum":["trip","lockout","reclose","open","close","earth","alarm","other"],"description":"Filter to a single operation type."},
					"source":{"type":"string","enum":["crew","scada","relay","switching_order","derived","other"],"description":"Filter to a single source."},
					"limit":{"type":"integer","description":"Max rows (default 200, capped at 1000)."}
				},
				"additionalProperties":false
			}`),
			RequiredPerm: auth.PermOperationsRead,
			Handler:      toolOperationsList,
		},
		{
			Name: "operations_device_timeline",
			Description: "List one device's operation timeline newest-first -- the per-device Operations " +
				"panel's data, the same read the REST GET /operations/device/{class}/{id} route returns.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"device_class":{"type":"string","description":"The device's feature class code."},
					"device_id":{"type":"string","description":"The device's id (uuid)."}
				},
				"required":["device_class","device_id"],
				"additionalProperties":false
			}`),
			RequiredPerm: auth.PermOperationsRead,
			Handler:      toolOperationsDeviceTimeline,
		},
		{
			Name: "operations_counts",
			Description: "Roll the operation log up per device and op_type -- how many times each device did " +
				"each thing and when it last did it, busiest first (the board's By-device view, the REST " +
				"GET /operations/counts route). The reliability/maintenance view (which recloser operates " +
				"most). Optional filters: a single op_type, and a window start (since).",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"op_type":{"type":"string","enum":["trip","lockout","reclose","open","close","earth","alarm","other"],"description":"Count only this operation type."},
					"since":{"type":"string","format":"date-time","description":"Only count operations at or after this RFC3339 time."}
				},
				"additionalProperties":false
			}`),
			RequiredPerm: auth.PermOperationsRead,
			Handler:      toolOperationsCounts,
		},
		{
			Name: "operations_inventory",
			Description: "List every operable device with its current state (device_state) and its most " +
				"recent operation -- the device inventory (the board's Devices view, the REST GET " +
				"/operations/inventory route). Device-centric, and includes devices that have never " +
				"operated. Optional filters: a single device_class, and a device state (e.g. OPEN).",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"device_class":{"type":"string","description":"Only devices of this feature class."},
					"state":{"type":"string","description":"Only devices at this device_state (e.g. OPEN, CLOSED)."}
				},
				"additionalProperties":false
			}`),
			RequiredPerm: auth.PermOperationsRead,
			Handler:      toolOperationsInventory,
		},
		{
			Name: "operations_record",
			Description: "Record one device operation -- the same validated write the REST POST /operations " +
				"route performs (the board's and panel's Log-operation action). The device " +
				"(device_class + device_id) and op_type are required; source defaults to 'crew' and " +
				"occurred_at to now. Append-only: returns the logged operation. This WRITES to the " +
				"database: confirm with a human before calling.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"device_class":{"type":"string","description":"The operated device's feature class code."},
					"device_id":{"type":"string","description":"The operated device's id (uuid)."},
					"op_type":{"type":"string","enum":["trip","lockout","reclose","open","close","earth","alarm","other"],"description":"What the device did."},
					"source":{"type":"string","enum":["crew","scada","relay","switching_order","derived","other"],"description":"Where the record came from (default crew)."},
					"occurred_at":{"type":"string","format":"date-time","description":"When the operation happened (RFC3339); defaults to now."},
					"cause":{"type":"string","description":"Free-text cause (overcurrent, earth fault, manual...)."},
					"notes":{"type":"string","description":"Free-text notes."},
					"fault_id":{"type":"string","description":"Correlated fault event id (uuid)."},
					"incident_id":{"type":"string","description":"Correlated OMS incident id (uuid)."},
					"switching_step_id":{"type":"string","description":"Correlated switching step id (uuid)."}
				},
				"required":["device_class","device_id","op_type"],
				"additionalProperties":false
			}`),
			Annotations: &Annotations{
				Title:           "Record device operation",
				ReadOnlyHint:    &falseHint, // this tool writes
				DestructiveHint: &falseHint, // append-only: it logs an event, removing nothing
				IdempotentHint:  &falseHint, // each call logs another operation
				OpenWorldHint:   &falseHint, // acts only on this system's own database
			},
			RequiredPerm: auth.PermOperationsWrite,
			Handler:      toolOperationsRecord,
		},
	}
}

// --- handlers ---

func toolOperationsList(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		DeviceClass string `json:"device_class"`
		DeviceID    string `json:"device_id"`
		OpType      string `json:"op_type"`
		Source      string `json:"source"`
		Limit       int    `json:"limit"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	ops, err := deviceops.List(ctx, db, deviceops.Filter{
		DeviceClass: in.DeviceClass,
		DeviceID:    in.DeviceID,
		OpType:      in.OpType,
		Source:      in.Source,
		Limit:       in.Limit,
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"operations": ops, "count": len(ops)}, nil
}

func toolOperationsDeviceTimeline(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		DeviceClass string `json:"device_class"`
		DeviceID    string `json:"device_id"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.DeviceClass == "" || in.DeviceID == "" {
		return nil, fmt.Errorf("device_class and device_id are required")
	}
	ops, err := deviceops.List(ctx, db, deviceops.Filter{DeviceClass: in.DeviceClass, DeviceID: in.DeviceID})
	if err != nil {
		return nil, err
	}
	return map[string]any{"operations": ops, "count": len(ops)}, nil
}

func toolOperationsCounts(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		OpType string `json:"op_type"`
		Since  string `json:"since"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	f := deviceops.CountsFilter{OpType: in.OpType}
	if in.Since != "" {
		t, err := time.Parse(time.RFC3339, in.Since)
		if err != nil {
			return nil, fmt.Errorf("invalid since (want RFC3339): %w", err)
		}
		f.Since = &t
	}
	rows, err := deviceops.Counts(ctx, db, f)
	if err != nil {
		return nil, err
	}
	return map[string]any{"counts": rows, "count": len(rows)}, nil
}

func toolOperationsInventory(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		DeviceClass string `json:"device_class"`
		State       string `json:"state"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	devices, err := deviceops.Inventory(ctx, db, deviceops.InventoryFilter{DeviceClass: in.DeviceClass, State: in.State})
	if err != nil {
		return nil, err
	}
	return map[string]any{"devices": devices, "count": len(devices)}, nil
}

func toolOperationsRecord(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		DeviceClass     string  `json:"device_class"`
		DeviceID        string  `json:"device_id"`
		OpType          string  `json:"op_type"`
		Source          string  `json:"source"`
		OccurredAt      string  `json:"occurred_at"`
		Cause           *string `json:"cause"`
		Notes           *string `json:"notes"`
		FaultID         *string `json:"fault_id"`
		IncidentID      *string `json:"incident_id"`
		SwitchingStepID *string `json:"switching_step_id"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.DeviceClass == "" || in.DeviceID == "" || in.OpType == "" {
		return nil, fmt.Errorf("device_class, device_id and op_type are required")
	}
	source := in.Source
	if source == "" {
		source = "crew"
	}
	occurred := time.Now()
	if in.OccurredAt != "" {
		t, err := time.Parse(time.RFC3339, in.OccurredAt)
		if err != nil {
			return nil, fmt.Errorf("invalid occurred_at (want RFC3339): %w", err)
		}
		occurred = t
	}
	// The engine validates op_type/source and stamps the acting user from ctx.
	return deviceops.Record(ctx, db, deviceops.RecordInput{
		DeviceClass:     in.DeviceClass,
		DeviceID:        in.DeviceID,
		OpType:          in.OpType,
		Source:          source,
		OccurredAt:      occurred,
		Cause:           in.Cause,
		Notes:           in.Notes,
		FaultID:         in.FaultID,
		IncidentID:      in.IncidentID,
		SwitchingStepID: in.SwitchingStepID,
	})
}
