package agenttools

// WAM (Work & Asset Management, Phase 59) tools -- the assistant/MCP surface of
// the wam module, one thin adapter per already-exported engine function in
// api/internal/wam. Each mirrors a REST route under /wam (the /works board and
// the feature panel's Works tab drive the same engine): the defect triage board,
// the one typed work-order engine, inspection templates + their execution, and
// the assignee picker. Reads carry wam:read and get the auto-stamped
// readOnlyHint; writes carry wam:write and their own annotations so a client
// confirms with a human first. WAM records are operational (a problem found on an
// asset, a job to fix it) -- not GIS features -- so nothing here touches the
// feature CRUD path.

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/kwaabs/voltedge/api/internal/auth"
	"github.com/kwaabs/voltedge/api/internal/wam"
	"github.com/uptrace/bun"
)

// wamTools returns the WAM tools -- reads first, then the mutating surface.
func wamTools() []Tool {
	return []Tool{
		{
			Name: "wam_list_defects",
			Description: "List asset-health defects newest-first -- the defect triage board (REST GET " +
				"/wam/defects, the /works board's Defects list and the feature panel's Works tab). " +
				"All filters are optional: status, severity, an asset (asset_class + asset_id), or " +
				"open=true to limit to still-open defects. Each defect carries its id (pass it to " +
				"wam_set_defect_status or wam_raise_work_order_from_defect), asset, severity, source, " +
				"and status.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"status":{"type":"string","enum":["open","work_ordered","resolved","dismissed"],"description":"Filter to one defect status."},
					"severity":{"type":"string","enum":["low","medium","high","critical"],"description":"Filter to one severity."},
					"asset_class":{"type":"string","description":"Feature class code of the affected asset; pair with asset_id."},
					"asset_id":{"type":"string","description":"The affected asset's feature id (uuid); pair with asset_class."},
					"open":{"type":"boolean","description":"Limit to still-open defects (status=open); overrides the status filter when true."}
				},
				"required":[],
				"additionalProperties":false
			}`),
			RequiredPerm: auth.PermWAMRead,
			Handler:      toolWAMListDefects,
		},
		{
			Name: "wam_get_defect",
			Description: "Fetch a single defect by its id -- the same read as REST GET /wam/defects/{id}. " +
				"Returns the full defect record (asset, severity, source, status, title/description, " +
				"reporter, timestamps, and its location when it's an orphan defect pinned by a point).",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"id":{"type":"string","description":"The defect's id (uuid)."}
				},
				"required":["id"],
				"additionalProperties":false
			}`),
			RequiredPerm: auth.PermWAMRead,
			Handler:      toolWAMGetDefect,
		},
		{
			Name: "wam_create_defect",
			Description: "Log an asset-health defect -- the same write as REST POST /wam/defects (the " +
				"/works board's Log-defect action and the feature panel's Works tab). A defect must " +
				"name an asset (both asset_class and asset_id) or be an orphan pinned by a location " +
				"(both longitude and latitude), but never a partial of either. Title is required; " +
				"severity defaults to medium and source to ad_hoc. Returns the created defect (with " +
				"its new id). This WRITES to the database: confirm with a human before calling.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"asset_class":{"type":"string","description":"Feature class code of the affected asset; required together with asset_id unless this is an orphan defect."},
					"asset_id":{"type":"string","description":"The affected asset's feature id (uuid); required together with asset_class."},
					"defect_type":{"type":"string","description":"Optional free-text classification of the defect."},
					"severity":{"type":"string","enum":["low","medium","high","critical"],"description":"Defect severity; defaults to medium."},
					"source":{"type":"string","enum":["inspection","outage","condition_monitoring","ad_hoc"],"description":"How the defect was found; defaults to ad_hoc."},
					"incident_id":{"type":"string","description":"Optional OMS incident id this defect explains (an outage-sourced defect)."},
					"title":{"type":"string","description":"Short summary of what is wrong (required)."},
					"description":{"type":"string","description":"Optional longer description."},
					"longitude":{"type":"number","description":"Longitude (EPSG:4326) for an orphan defect; must be paired with latitude."},
					"latitude":{"type":"number","description":"Latitude (EPSG:4326) for an orphan defect; must be paired with longitude."},
					"reported_by":{"type":"string","description":"Optional label of who reported the defect."},
					"reported_at":{"type":"string","format":"date-time","description":"Optional RFC3339 timestamp of when it was reported; defaults to now."},
					"notes":{"type":"string","description":"Optional free-text notes."}
				},
				"required":["title"],
				"additionalProperties":false
			}`),
			Annotations: &Annotations{
				Title:           "Log defect",
				ReadOnlyHint:    &falseHint, // this tool writes
				DestructiveHint: &falseHint, // a create is additive
				IdempotentHint:  &falseHint, // repeating it logs another defect
				OpenWorldHint:   &falseHint, // acts only on this system's own database
			},
			RequiredPerm: auth.PermWAMWrite,
			Handler:      toolWAMCreateDefect,
		},
		{
			Name: "wam_set_defect_status",
			Description: "Transition a defect to a new status -- the same write as REST POST " +
				"/wam/defects/{id}/status (the Works tab's resolve/dismiss/reopen actions). Valid " +
				"statuses are open, work_ordered, resolved, and dismissed; moving to a terminal state " +
				"stamps resolved_at, reopening clears it. Idempotent: setting the current status " +
				"returns the same state. Returns the updated defect. This WRITES to the database: " +
				"confirm with a human before calling.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"id":{"type":"string","description":"The defect's id (uuid)."},
					"status":{"type":"string","enum":["open","work_ordered","resolved","dismissed"],"description":"The status to transition the defect to."}
				},
				"required":["id","status"],
				"additionalProperties":false
			}`),
			Annotations: &Annotations{
				Title:           "Set defect status",
				ReadOnlyHint:    &falseHint, // this tool writes
				DestructiveHint: &falseHint, // a status change is reversible
				IdempotentHint:  &trueHint,  // re-applying the same status is a no-op
				OpenWorldHint:   &falseHint, // acts only on this system's own database
			},
			RequiredPerm: auth.PermWAMWrite,
			Handler:      toolWAMSetDefectStatus,
		},
		{
			Name: "wam_list_work_orders",
			Description: "List work orders newest-first -- the work board (REST GET /wam/work-orders, " +
				"the /works board and the feature panel's Works tab). All filters are optional: status, " +
				"work_type, priority, an asset (asset_class + asset_id), an assignee (assigned_to " +
				"free-text or assigned_to_user_id), or open=true to limit to live (not completed or " +
				"cancelled) orders. Each order carries its id (pass it to wam_update_work_order).",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"status":{"type":"string","enum":["draft","scheduled","assigned","in_progress","completed","cancelled"],"description":"Filter to one work-order status."},
					"work_type":{"type":"string","enum":["inspection","corrective","preventive","construction"],"description":"Filter to one work type."},
					"priority":{"type":"string","enum":["low","medium","high","urgent"],"description":"Filter to one priority."},
					"asset_class":{"type":"string","description":"Feature class code of the targeted asset; pair with asset_id."},
					"asset_id":{"type":"string","description":"The targeted asset's feature id (uuid); pair with asset_class."},
					"assigned_to":{"type":"string","description":"Free-text assignee label (external crews) to filter by."},
					"assigned_to_user_id":{"type":"string","description":"Internal user id (uuid) the order is assigned to (the 'my work orders' filter)."},
					"open":{"type":"boolean","description":"Limit to live orders (not completed or cancelled); overrides the status filter when true."}
				},
				"required":[],
				"additionalProperties":false
			}`),
			RequiredPerm: auth.PermWAMRead,
			Handler:      toolWAMListWorkOrders,
		},
		{
			Name: "wam_get_work_order",
			Description: "Fetch a single work order by its id -- the same read as REST GET " +
				"/wam/work-orders/{id}. Returns the full order (type, status, priority, asset, defect " +
				"link, assignee, schedule, and lifecycle timestamps).",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"id":{"type":"string","description":"The work order's id (uuid)."}
				},
				"required":["id"],
				"additionalProperties":false
			}`),
			RequiredPerm: auth.PermWAMRead,
			Handler:      toolWAMGetWorkOrder,
		},
		{
			Name: "wam_create_work_order",
			Description: "Create a work order -- the same write as REST POST /wam/work-orders (the " +
				"/works board's create action and the feature panel's Works tab). work_type and title " +
				"are required; priority defaults to medium and status to draft (naming an assignee or a " +
				"schedule up front advances it to assigned/scheduled). Everything else is optional (an " +
				"asset, a location, a defect link, an assignee, a schedule). Returns the created order " +
				"(with its new id). This WRITES to the database: confirm with a human before calling.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"reference":{"type":"string","description":"Optional external reference/number for the order."},
					"work_type":{"type":"string","enum":["inspection","corrective","preventive","construction"],"description":"The kind of work (required)."},
					"priority":{"type":"string","enum":["low","medium","high","urgent"],"description":"Priority; defaults to medium."},
					"title":{"type":"string","description":"Short summary of the job (required)."},
					"description":{"type":"string","description":"Optional longer description."},
					"asset_class":{"type":"string","description":"Feature class code of the targeted asset (optional); pair with asset_id."},
					"asset_id":{"type":"string","description":"The targeted asset's feature id (uuid); pair with asset_class."},
					"longitude":{"type":"number","description":"Longitude (EPSG:4326) to anchor the order; pair with latitude."},
					"latitude":{"type":"number","description":"Latitude (EPSG:4326) to anchor the order; pair with longitude."},
					"defect_id":{"type":"string","description":"Optional defect id (uuid) this order addresses."},
					"assigned_to":{"type":"string","description":"Optional free-text assignee label (external crews)."},
					"assigned_to_user_id":{"type":"string","description":"Optional internal user id (uuid) to assign the order to; see wam_list_assignable_users."},
					"scheduled_for":{"type":"string","format":"date-time","description":"Optional RFC3339 scheduled start."},
					"due_by":{"type":"string","format":"date-time","description":"Optional RFC3339 due date."}
				},
				"required":["work_type","title"],
				"additionalProperties":false
			}`),
			Annotations: &Annotations{
				Title:           "Create work order",
				ReadOnlyHint:    &falseHint, // this tool writes
				DestructiveHint: &falseHint, // a create is additive
				IdempotentHint:  &falseHint, // repeating it creates another order
				OpenWorldHint:   &falseHint, // acts only on this system's own database
			},
			RequiredPerm: auth.PermWAMWrite,
			Handler:      toolWAMCreateWorkOrder,
		},
		{
			Name: "wam_update_work_order",
			Description: "Apply a sparse patch to a work order -- the same write as REST PATCH " +
				"/wam/work-orders/{id} (the Works tab's start/complete/cancel/reassign/reschedule " +
				"actions). Every field is optional and only what you send is changed; changing status " +
				"keeps the lifecycle timestamps consistent (in_progress stamps started_at, completed " +
				"stamps completed_at). Sending an empty assigned_to_user_id unassigns the internal " +
				"user. Returns the updated order. This WRITES to the database: confirm with a human " +
				"before calling. Use wam_get_work_order to see current values first.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"id":{"type":"string","description":"The work order's id (uuid)."},
					"status":{"type":"string","enum":["draft","scheduled","assigned","in_progress","completed","cancelled"],"description":"New status; omit to keep it."},
					"priority":{"type":"string","enum":["low","medium","high","urgent"],"description":"New priority; omit to keep it."},
					"title":{"type":"string","description":"New title; omit to keep it (must not be empty)."},
					"description":{"type":"string","description":"New description; omit to keep it."},
					"assigned_to":{"type":"string","description":"New free-text assignee label; omit to keep it."},
					"assigned_to_user_id":{"type":"string","description":"New internal assignee user id (uuid); send an empty string to unassign; omit to keep it."},
					"scheduled_for":{"type":"string","format":"date-time","description":"New RFC3339 scheduled start; omit to keep it."},
					"due_by":{"type":"string","format":"date-time","description":"New RFC3339 due date; omit to keep it."},
					"completion_notes":{"type":"string","description":"Completion notes; omit to keep them."}
				},
				"required":["id"],
				"additionalProperties":false
			}`),
			Annotations: &Annotations{
				Title:           "Update work order",
				ReadOnlyHint:    &falseHint, // this tool writes
				DestructiveHint: &falseHint, // a field/status update overwrites, it doesn't delete
				IdempotentHint:  &trueHint,  // re-applying the same patch yields the same state
				OpenWorldHint:   &falseHint, // acts only on this system's own database
			},
			RequiredPerm: auth.PermWAMWrite,
			Handler:      toolWAMUpdateWorkOrder,
		},
		{
			Name: "wam_raise_work_order_from_defect",
			Description: "Raise a corrective work order for a defect and flip the defect to " +
				"'work_ordered', atomically -- the same composite write as REST POST " +
				"/wam/defects/{id}/work-order (the Works tab's Raise-work-order action). The order " +
				"inherits the defect's asset and location and (unless overridden) its title and a " +
				"priority derived from the defect severity. A resolved or dismissed defect is rejected. " +
				"All overrides are optional. Returns the created order. This WRITES to the database: " +
				"confirm with a human before calling.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"defect_id":{"type":"string","description":"The defect's id (uuid) to raise a corrective order for."},
					"title":{"type":"string","description":"Optional title override; defaults to 'Repair: <defect title>'."},
					"priority":{"type":"string","enum":["low","medium","high","urgent"],"description":"Optional priority override; defaults from the defect severity."},
					"description":{"type":"string","description":"Optional description for the order."},
					"assigned_to":{"type":"string","description":"Optional free-text assignee label (external crews)."},
					"assigned_to_user_id":{"type":"string","description":"Optional internal user id (uuid) to assign the order to."},
					"scheduled_for":{"type":"string","format":"date-time","description":"Optional RFC3339 scheduled start."},
					"due_by":{"type":"string","format":"date-time","description":"Optional RFC3339 due date."}
				},
				"required":["defect_id"],
				"additionalProperties":false
			}`),
			Annotations: &Annotations{
				Title:           "Raise work order from defect",
				ReadOnlyHint:    &falseHint, // this tool writes
				DestructiveHint: &falseHint, // it creates an order + advances the defect, additive
				IdempotentHint:  &falseHint, // each call raises another order
				OpenWorldHint:   &falseHint, // acts only on this system's own database
			},
			RequiredPerm: auth.PermWAMWrite,
			Handler:      toolWAMRaiseWorkOrderFromDefect,
		},
		{
			Name: "wam_list_assignable_users",
			Description: "List the internal users a work order can be assigned to -- the assignee " +
				"picker (REST GET /wam/assignable-users). These are active users whose roles grant " +
				"wam:write (so they can perform and close work), each with id, display name, and email. " +
				"Use the id as assigned_to_user_id on wam_create_work_order / wam_update_work_order. " +
				"Takes no arguments.",
			InputSchema:  json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
			RequiredPerm: auth.PermWAMRead,
			Handler:      toolWAMListAssignableUsers,
		},
		{
			Name: "wam_list_templates",
			Description: "List inspection checklist templates ordered by name -- the template picker " +
				"(REST GET /wam/inspection-templates, the /works Inspections panel). Set active_only to " +
				"hide retired templates. Each template carries its id and code (pass the id to " +
				"wam_get_template or use it as template_id when recording an inspection).",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"active_only":{"type":"boolean","description":"Hide retired (inactive) templates when true."}
				},
				"required":[],
				"additionalProperties":false
			}`),
			RequiredPerm: auth.PermWAMRead,
			Handler:      toolWAMListTemplates,
		},
		{
			Name: "wam_get_template",
			Description: "Fetch a single inspection template by its id, with its ordered checklist items " +
				"-- the same read as REST GET /wam/inspection-templates/{id}. Each item carries its " +
				"prompt, response type, and any fail severity that arms an auto-defect on failure. Use " +
				"this to learn a template's items before recording an inspection against it.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"id":{"type":"string","description":"The inspection template's id (uuid)."}
				},
				"required":["id"],
				"additionalProperties":false
			}`),
			RequiredPerm: auth.PermWAMRead,
			Handler:      toolWAMGetTemplate,
		},
		{
			Name: "wam_create_template",
			Description: "Create an inspection checklist template with its ordered items -- the same " +
				"write as REST POST /wam/inspection-templates (the /works Inspections panel). code, " +
				"name, and at least one item are required; each item needs a prompt and defaults to a " +
				"pass_fail response type. An item's fail_severity, when set, makes a failing answer " +
				"auto-raise a defect at that severity when the template is executed. Returns the " +
				"created template with its items. This WRITES to the database: confirm with a human " +
				"before calling.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"code":{"type":"string","description":"Stable slug for the template (required)."},
					"name":{"type":"string","description":"Display name for the template (required)."},
					"description":{"type":"string","description":"Optional description."},
					"asset_class":{"type":"string","description":"Optional feature class code the checklist is scoped to."},
					"items":{
						"type":"array",
						"description":"The ordered checklist items (at least one).",
						"items":{
							"type":"object",
							"properties":{
								"position":{"type":"integer","description":"1-based order; defaults to the array index when 0."},
								"prompt":{"type":"string","description":"The checklist question (required)."},
								"response_type":{"type":"string","enum":["pass_fail","rating","numeric","text"],"description":"How the item is answered; defaults to pass_fail."},
								"fail_severity":{"type":"string","enum":["low","medium","high","critical"],"description":"Arms an auto-defect at this severity when the item fails; omit for an informational item."},
								"guidance":{"type":"string","description":"Optional inspector guidance for the item."}
							},
							"required":["prompt"],
							"additionalProperties":false
						}
					}
				},
				"required":["code","name","items"],
				"additionalProperties":false
			}`),
			Annotations: &Annotations{
				Title:           "Create inspection template",
				ReadOnlyHint:    &falseHint, // this tool writes
				DestructiveHint: &falseHint, // a create is additive
				IdempotentHint:  &falseHint, // repeating it creates another template
				OpenWorldHint:   &falseHint, // acts only on this system's own database
			},
			RequiredPerm: auth.PermWAMWrite,
			Handler:      toolWAMCreateTemplate,
		},
		{
			Name: "wam_set_template_active",
			Description: "Retire or restore an inspection template -- the same write as REST POST " +
				"/wam/inspection-templates/{id}/active. A retired (inactive) template is kept for " +
				"history but hidden from the record picker; restoring makes it selectable again. " +
				"Idempotent: setting the current active flag returns the same state. Returns the " +
				"updated template. This WRITES to the database: confirm with a human before calling.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"id":{"type":"string","description":"The inspection template's id (uuid)."},
					"active":{"type":"boolean","description":"true to restore the template, false to retire it."}
				},
				"required":["id","active"],
				"additionalProperties":false
			}`),
			Annotations: &Annotations{
				Title:           "Set template active",
				ReadOnlyHint:    &falseHint, // this tool writes
				DestructiveHint: &falseHint, // retire/restore is reversible, keeps history
				IdempotentHint:  &trueHint,  // re-applying the same flag is a no-op
				OpenWorldHint:   &falseHint, // acts only on this system's own database
			},
			RequiredPerm: auth.PermWAMWrite,
			Handler:      toolWAMSetTemplateActive,
		},
		{
			Name: "wam_record_inspection",
			Description: "Record an executed inspection against an asset with its answered checklist " +
				"items -- the same composite write as REST POST /wam/inspections (the /works " +
				"Inspections panel). asset_class, asset_id, and at least one item are required; the " +
				"overall result rolls up from the items unless you override it. Every failing item that " +
				"carries a fail_severity auto-raises a linked defect (source=inspection), and " +
				"complete_work_order closes the named work order in the same transaction. Returns the " +
				"recorded inspection with its item results. This WRITES to the database: confirm with a " +
				"human before calling.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"work_order_id":{"type":"string","description":"Optional inspection work order (uuid) this execution belongs to."},
					"template_id":{"type":"string","description":"Optional template (uuid) this inspection was run from."},
					"asset_class":{"type":"string","description":"Feature class code of the inspected asset (required)."},
					"asset_id":{"type":"string","description":"The inspected asset's feature id (uuid, required)."},
					"inspected_by":{"type":"string","description":"Optional label of who performed the inspection."},
					"inspected_at":{"type":"string","format":"date-time","description":"Optional RFC3339 time the inspection was performed; defaults to now."},
					"notes":{"type":"string","description":"Optional overall notes."},
					"longitude":{"type":"number","description":"Longitude (EPSG:4326) of the inspection; pair with latitude."},
					"latitude":{"type":"number","description":"Latitude (EPSG:4326) of the inspection; pair with longitude."},
					"overall_result":{"type":"string","enum":["pass","fail","conditional"],"description":"Overall outcome; defaults to a roll-up of the items (fail if any item failed)."},
					"complete_work_order":{"type":"boolean","description":"Complete the linked work_order_id in the same transaction when true."},
					"items":{
						"type":"array",
						"description":"The answered checklist items (at least one).",
						"items":{
							"type":"object",
							"properties":{
								"template_item_id":{"type":"string","description":"Optional source template item id (uuid)."},
								"position":{"type":"integer","description":"1-based order; defaults to the array index when 0."},
								"prompt":{"type":"string","description":"The item's question, snapshotted onto the record (required)."},
								"response_type":{"type":"string","enum":["pass_fail","rating","numeric","text"],"description":"How the item was answered; defaults to pass_fail."},
								"passed":{"type":"boolean","description":"The pass/fail outcome; omit for not-answered / N/A."},
								"value_text":{"type":"string","description":"Optional free-text answer."},
								"value_numeric":{"type":"number","description":"Optional numeric reading."},
								"fail_severity":{"type":"string","enum":["low","medium","high","critical"],"description":"When the item failed, arms an auto-defect at this severity."},
								"notes":{"type":"string","description":"Optional per-item notes."}
							},
							"required":["prompt"],
							"additionalProperties":false
						}
					}
				},
				"required":["asset_class","asset_id","items"],
				"additionalProperties":false
			}`),
			Annotations: &Annotations{
				Title:           "Record inspection",
				ReadOnlyHint:    &falseHint, // this tool writes
				DestructiveHint: &falseHint, // it records + may raise defects/complete an order, additive
				IdempotentHint:  &falseHint, // each call records another inspection
				OpenWorldHint:   &falseHint, // acts only on this system's own database
			},
			RequiredPerm: auth.PermWAMWrite,
			Handler:      toolWAMRecordInspection,
		},
		{
			Name: "wam_list_inspections",
			Description: "List recorded inspections newest-first (without their item results) -- the " +
				"same read as REST GET /wam/inspections. All filters are optional: an asset " +
				"(asset_class + asset_id), a work_order_id, or a template_id. Each carries its id (pass " +
				"it to wam_get_inspection for the item results).",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"asset_class":{"type":"string","description":"Feature class code of the inspected asset; pair with asset_id."},
					"asset_id":{"type":"string","description":"The inspected asset's feature id (uuid); pair with asset_class."},
					"work_order_id":{"type":"string","description":"Filter to inspections under this work order (uuid)."},
					"template_id":{"type":"string","description":"Filter to inspections run from this template (uuid)."}
				},
				"required":[],
				"additionalProperties":false
			}`),
			RequiredPerm: auth.PermWAMRead,
			Handler:      toolWAMListInspections,
		},
		{
			Name: "wam_get_inspection",
			Description: "Fetch a single recorded inspection by its id, with its ordered item results -- " +
				"the same read as REST GET /wam/inspections/{id}. Each item result carries the answered " +
				"prompt, the pass/fail outcome, any numeric/text value, and the id of any defect a " +
				"failing item auto-raised.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"id":{"type":"string","description":"The inspection's id (uuid)."}
				},
				"required":["id"],
				"additionalProperties":false
			}`),
			RequiredPerm: auth.PermWAMRead,
			Handler:      toolWAMGetInspection,
		},
	}
}

// --- handlers ---
//
// Each unmarshals into a local struct with explicit json tags (the wam engine's
// param structs carry no tags of their own, so the mapping is spelled out) and
// hands off to the exported engine. The acting user is already on ctx via
// auth.WithUser in Execute, matching the REST path. The transaction-owning
// engines (RaiseWorkOrderFromDefect, CreateTemplate, RecordInspection) take a
// concrete *bun.DB, narrowed via asDB.

func toolWAMListDefects(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		Status     string `json:"status"`
		Severity   string `json:"severity"`
		AssetClass string `json:"asset_class"`
		AssetID    string `json:"asset_id"`
		Open       bool   `json:"open"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	defects, err := wam.ListDefects(ctx, db, wam.DefectFilter{
		Status:     in.Status,
		Severity:   in.Severity,
		AssetClass: in.AssetClass,
		AssetID:    in.AssetID,
		Open:       in.Open,
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"defects": defects, "count": len(defects)}, nil
}

func toolWAMGetDefect(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.ID == "" {
		return nil, fmt.Errorf("id is required")
	}
	return wam.GetDefect(ctx, db, in.ID)
}

func toolWAMCreateDefect(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		AssetClass  string     `json:"asset_class"`
		AssetID     string     `json:"asset_id"`
		DefectType  *string    `json:"defect_type"`
		Severity    string     `json:"severity"`
		Source      string     `json:"source"`
		IncidentID  *string    `json:"incident_id"`
		Title       string     `json:"title"`
		Description *string    `json:"description"`
		Longitude   *float64   `json:"longitude"`
		Latitude    *float64   `json:"latitude"`
		ReportedBy  *string    `json:"reported_by"`
		ReportedAt  *time.Time `json:"reported_at"`
		Notes       *string    `json:"notes"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.Title == "" {
		return nil, fmt.Errorf("title is required")
	}
	// The engine re-validates the asset/location anchor, severity, and source;
	// those surface in-band as the tool error.
	return wam.CreateDefect(ctx, db, wam.CreateDefectParams{
		AssetClass:  in.AssetClass,
		AssetID:     in.AssetID,
		DefectType:  in.DefectType,
		Severity:    in.Severity,
		Source:      in.Source,
		IncidentID:  in.IncidentID,
		Title:       in.Title,
		Description: in.Description,
		Longitude:   in.Longitude,
		Latitude:    in.Latitude,
		ReportedBy:  in.ReportedBy,
		ReportedAt:  in.ReportedAt,
		Notes:       in.Notes,
	})
}

func toolWAMSetDefectStatus(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
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
	return wam.SetDefectStatus(ctx, db, in.ID, in.Status)
}

func toolWAMListWorkOrders(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		Status           string `json:"status"`
		WorkType         string `json:"work_type"`
		Priority         string `json:"priority"`
		AssetClass       string `json:"asset_class"`
		AssetID          string `json:"asset_id"`
		AssignedTo       string `json:"assigned_to"`
		AssignedToUserID string `json:"assigned_to_user_id"`
		Open             bool   `json:"open"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	orders, err := wam.ListWorkOrders(ctx, db, wam.WorkOrderFilter{
		Status:           in.Status,
		WorkType:         in.WorkType,
		Priority:         in.Priority,
		AssetClass:       in.AssetClass,
		AssetID:          in.AssetID,
		AssignedTo:       in.AssignedTo,
		AssignedToUserID: in.AssignedToUserID,
		Open:             in.Open,
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"work_orders": orders, "count": len(orders)}, nil
}

func toolWAMGetWorkOrder(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.ID == "" {
		return nil, fmt.Errorf("id is required")
	}
	return wam.GetWorkOrder(ctx, db, in.ID)
}

func toolWAMCreateWorkOrder(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		Reference        *string    `json:"reference"`
		WorkType         string     `json:"work_type"`
		Priority         string     `json:"priority"`
		Title            string     `json:"title"`
		Description      *string    `json:"description"`
		AssetClass       *string    `json:"asset_class"`
		AssetID          *string    `json:"asset_id"`
		Longitude        *float64   `json:"longitude"`
		Latitude         *float64   `json:"latitude"`
		DefectID         *string    `json:"defect_id"`
		AssignedTo       *string    `json:"assigned_to"`
		AssignedToUserID *string    `json:"assigned_to_user_id"`
		ScheduledFor     *time.Time `json:"scheduled_for"`
		DueBy            *time.Time `json:"due_by"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.WorkType == "" || in.Title == "" {
		return nil, fmt.Errorf("work_type and title are required")
	}
	return wam.CreateWorkOrder(ctx, db, wam.CreateWorkOrderParams{
		Reference:        in.Reference,
		WorkType:         in.WorkType,
		Priority:         in.Priority,
		Title:            in.Title,
		Description:      in.Description,
		AssetClass:       in.AssetClass,
		AssetID:          in.AssetID,
		Longitude:        in.Longitude,
		Latitude:         in.Latitude,
		DefectID:         in.DefectID,
		AssignedTo:       in.AssignedTo,
		AssignedToUserID: in.AssignedToUserID,
		ScheduledFor:     in.ScheduledFor,
		DueBy:            in.DueBy,
	})
}

func toolWAMUpdateWorkOrder(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		ID               string     `json:"id"`
		Status           *string    `json:"status"`
		Priority         *string    `json:"priority"`
		Title            *string    `json:"title"`
		Description      *string    `json:"description"`
		AssignedTo       *string    `json:"assigned_to"`
		AssignedToUserID *string    `json:"assigned_to_user_id"`
		ScheduledFor     *time.Time `json:"scheduled_for"`
		DueBy            *time.Time `json:"due_by"`
		CompletionNotes  *string    `json:"completion_notes"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.ID == "" {
		return nil, fmt.Errorf("id is required")
	}
	// The engine rejects an empty patch and re-validates status/priority; those
	// surface in-band as the tool error.
	return wam.UpdateWorkOrder(ctx, db, in.ID, wam.UpdateWorkOrderParams{
		Status:           in.Status,
		Priority:         in.Priority,
		Title:            in.Title,
		Description:      in.Description,
		AssignedTo:       in.AssignedTo,
		AssignedToUserID: in.AssignedToUserID,
		ScheduledFor:     in.ScheduledFor,
		DueBy:            in.DueBy,
		CompletionNotes:  in.CompletionNotes,
	})
}

func toolWAMRaiseWorkOrderFromDefect(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		DefectID         string     `json:"defect_id"`
		Title            *string    `json:"title"`
		Priority         *string    `json:"priority"`
		Description      *string    `json:"description"`
		AssignedTo       *string    `json:"assigned_to"`
		AssignedToUserID *string    `json:"assigned_to_user_id"`
		ScheduledFor     *time.Time `json:"scheduled_for"`
		DueBy            *time.Time `json:"due_by"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.DefectID == "" {
		return nil, fmt.Errorf("defect_id is required")
	}
	sdb, err := asDB(db)
	if err != nil {
		return nil, err
	}
	// RaiseWorkOrderFromDefect owns the transaction (create order + flip defect);
	// a terminal defect is rejected in-band as the tool error.
	return wam.RaiseWorkOrderFromDefect(ctx, sdb, in.DefectID, wam.RaiseWorkOrderFromDefectParams{
		Title:            in.Title,
		Priority:         in.Priority,
		Description:      in.Description,
		AssignedTo:       in.AssignedTo,
		AssignedToUserID: in.AssignedToUserID,
		ScheduledFor:     in.ScheduledFor,
		DueBy:            in.DueBy,
	})
}

func toolWAMListAssignableUsers(ctx context.Context, db bun.IDB, _ json.RawMessage) (any, error) {
	users, err := wam.ListAssignableUsers(ctx, db)
	if err != nil {
		return nil, err
	}
	return map[string]any{"users": users, "count": len(users)}, nil
}

func toolWAMListTemplates(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		ActiveOnly bool `json:"active_only"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	templates, err := wam.ListTemplates(ctx, db, in.ActiveOnly)
	if err != nil {
		return nil, err
	}
	return map[string]any{"templates": templates, "count": len(templates)}, nil
}

func toolWAMGetTemplate(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.ID == "" {
		return nil, fmt.Errorf("id is required")
	}
	return wam.GetTemplate(ctx, db, in.ID)
}

func toolWAMCreateTemplate(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		Code        string  `json:"code"`
		Name        string  `json:"name"`
		Description *string `json:"description"`
		AssetClass  *string `json:"asset_class"`
		Items       []struct {
			Position     int     `json:"position"`
			Prompt       string  `json:"prompt"`
			ResponseType string  `json:"response_type"`
			FailSeverity *string `json:"fail_severity"`
			Guidance     *string `json:"guidance"`
		} `json:"items"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.Code == "" || in.Name == "" {
		return nil, fmt.Errorf("code and name are required")
	}
	if len(in.Items) == 0 {
		return nil, fmt.Errorf("at least one checklist item is required")
	}
	items := make([]wam.TemplateItemInput, len(in.Items))
	for i, it := range in.Items {
		items[i] = wam.TemplateItemInput{
			Position:     it.Position,
			Prompt:       it.Prompt,
			ResponseType: it.ResponseType,
			FailSeverity: it.FailSeverity,
			Guidance:     it.Guidance,
		}
	}
	sdb, err := asDB(db)
	if err != nil {
		return nil, err
	}
	// CreateTemplate owns the transaction (template + its items); item response
	// types and fail severities are re-validated in-band.
	return wam.CreateTemplate(ctx, sdb, wam.CreateTemplateParams{
		Code:        in.Code,
		Name:        in.Name,
		Description: in.Description,
		AssetClass:  in.AssetClass,
		Items:       items,
	})
}

func toolWAMSetTemplateActive(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		ID     string `json:"id"`
		Active bool   `json:"active"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.ID == "" {
		return nil, fmt.Errorf("id is required")
	}
	return wam.SetTemplateActive(ctx, db, in.ID, in.Active)
}

func toolWAMRecordInspection(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		WorkOrderID       *string    `json:"work_order_id"`
		TemplateID        *string    `json:"template_id"`
		AssetClass        string     `json:"asset_class"`
		AssetID           string     `json:"asset_id"`
		InspectedBy       *string    `json:"inspected_by"`
		InspectedAt       *time.Time `json:"inspected_at"`
		Notes             *string    `json:"notes"`
		Longitude         *float64   `json:"longitude"`
		Latitude          *float64   `json:"latitude"`
		OverallResult     *string    `json:"overall_result"`
		CompleteWorkOrder bool       `json:"complete_work_order"`
		Items             []struct {
			TemplateItemID *string  `json:"template_item_id"`
			Position       int      `json:"position"`
			Prompt         string   `json:"prompt"`
			ResponseType   string   `json:"response_type"`
			Passed         *bool    `json:"passed"`
			ValueText      *string  `json:"value_text"`
			ValueNumeric   *float64 `json:"value_numeric"`
			FailSeverity   *string  `json:"fail_severity"`
			Notes          *string  `json:"notes"`
		} `json:"items"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.AssetClass == "" || in.AssetID == "" {
		return nil, fmt.Errorf("asset_class and asset_id are required")
	}
	if len(in.Items) == 0 {
		return nil, fmt.Errorf("at least one recorded item is required")
	}
	items := make([]wam.RecordItemInput, len(in.Items))
	for i, it := range in.Items {
		items[i] = wam.RecordItemInput{
			TemplateItemID: it.TemplateItemID,
			Position:       it.Position,
			Prompt:         it.Prompt,
			ResponseType:   it.ResponseType,
			Passed:         it.Passed,
			ValueText:      it.ValueText,
			ValueNumeric:   it.ValueNumeric,
			FailSeverity:   it.FailSeverity,
			Notes:          it.Notes,
		}
	}
	sdb, err := asDB(db)
	if err != nil {
		return nil, err
	}
	// RecordInspection owns the transaction: it writes the inspection + item
	// results, auto-raises a defect for every failing severity-bearing item, and
	// optionally completes the linked work order.
	return wam.RecordInspection(ctx, sdb, wam.RecordInspectionParams{
		WorkOrderID:       in.WorkOrderID,
		TemplateID:        in.TemplateID,
		AssetClass:        in.AssetClass,
		AssetID:           in.AssetID,
		InspectedBy:       in.InspectedBy,
		InspectedAt:       in.InspectedAt,
		Notes:             in.Notes,
		Longitude:         in.Longitude,
		Latitude:          in.Latitude,
		OverallResult:     in.OverallResult,
		CompleteWorkOrder: in.CompleteWorkOrder,
		Items:             items,
	})
}

func toolWAMListInspections(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		AssetClass  string `json:"asset_class"`
		AssetID     string `json:"asset_id"`
		WorkOrderID string `json:"work_order_id"`
		TemplateID  string `json:"template_id"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	inspections, err := wam.ListInspections(ctx, db, wam.InspectionFilter{
		AssetClass:  in.AssetClass,
		AssetID:     in.AssetID,
		WorkOrderID: in.WorkOrderID,
		TemplateID:  in.TemplateID,
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"inspections": inspections, "count": len(inspections)}, nil
}

func toolWAMGetInspection(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.ID == "" {
		return nil, fmt.Errorf("id is required")
	}
	return wam.GetInspection(ctx, db, in.ID)
}
