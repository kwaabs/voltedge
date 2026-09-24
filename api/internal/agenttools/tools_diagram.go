package agenttools

// The Diagram module's tools -- the schematic surface (one-line diagrams,
// control schemes, ...) exposed through the same single agenttools registry as
// every other activity. A diagram is a container feature's own local x/y drawing
// space: features are *placed* on it, non-feature *elements* (boxes, text,
// schematic lines, stencil symbols) are drawn on it, reusable *templates*
// capture and re-stamp a layout, and a drawn busbar can be *sectionalized* into
// two feature-backed halves. Each tool here is a thin adapter over an
// already-exported api/internal/diagram operation -- the same code the REST
// /diagrams routes and the diagram canvas frontend call -- and declares the RBAC
// permission that route requires: the GET verbs read (diagrams:read), the
// mutating verbs write (diagrams:write), deleting a whole diagram escalates to
// diagrams:delete, and sectionalizing (which mints a feature and commits
// connectivity) additionally requires features:write + connectivity:commit. The
// read tools carry nil annotations (buildRegistry stamps readOnlyHint:true); the
// write tools carry explicit annotations so a client confirms with a human first.

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/kwaabs/voltedge/api/internal/auth"
	"github.com/kwaabs/voltedge/api/internal/diagram"
	"github.com/uptrace/bun"
)

// diagramTools returns the Diagram tools in advertised order: the reads first
// (get / list instances / list placements / list elements / list templates),
// then the writes (instances, placements, elements, templates, and the
// sectionalize composite).
func diagramTools() []Tool {
	return []Tool{
		{
			Name: "diagram_get_instance",
			Description: "Fetch a single diagram instance by its id -- the same read the REST GET " +
				"/diagrams/{id} route returns. Returns the instance's container feature, its diagram_type " +
				"code, its optional name, and when it was created.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"id":{"type":"string","description":"The diagram instance's id (uuid)."}
				},
				"required":["id"],
				"additionalProperties":false
			}`),
			RequiredPerm: auth.PermDiagramsRead,
			Handler:      toolDiagramGetInstance,
		},
		{
			Name: "diagram_list_instances",
			Description: "List every diagram instance hosted by one container feature -- the same query the " +
				"REST GET /diagrams route returns. A container may host more than one diagram (e.g. a " +
				"one-line and a control-scheme diagram for the same substation), so this returns them all, " +
				"oldest first.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"container_feature_class":{"type":"string","description":"The container feature's class code (e.g. SUBSTATION)."},
					"container_feature_id":{"type":"string","description":"The container feature's id (uuid)."}
				},
				"required":["container_feature_class","container_feature_id"],
				"additionalProperties":false
			}`),
			RequiredPerm: auth.PermDiagramsRead,
			Handler:      toolDiagramListInstances,
		},
		{
			Name: "diagram_list_placements",
			Description: "List every feature placed on one diagram -- the same read the REST GET " +
				"/diagrams/{id}/placements route returns. Each placement carries its feature ref, its " +
				"position (x/y/rotation), its optional drawn line path, and its label state. A placement " +
				"whose underlying feature has been soft-deleted is filtered out.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"diagram_id":{"type":"string","description":"The diagram instance's id (uuid)."}
				},
				"required":["diagram_id"],
				"additionalProperties":false
			}`),
			RequiredPerm: auth.PermDiagramsRead,
			Handler:      toolDiagramListPlacements,
		},
		{
			Name: "diagram_list_elements",
			Description: "List every non-feature element on one diagram (boxes, text, schematic lines, " +
				"stencil symbols, title blocks, legends, slots) -- the same read the REST GET " +
				"/diagrams/{id}/elements route returns, drawn back-to-front (z-index ascending).",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"diagram_id":{"type":"string","description":"The diagram instance's id (uuid)."}
				},
				"required":["diagram_id"],
				"additionalProperties":false
			}`),
			RequiredPerm: auth.PermDiagramsRead,
			Handler:      toolDiagramListElements,
		},
		{
			Name: "diagram_list_templates",
			Description: "List the reusable diagram-template library newest-first -- the same read the REST " +
				"GET /diagrams/templates route returns. Each summary carries the template's name, optional " +
				"description, and its element / slot counts (not the full payload).",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{},
				"additionalProperties":false
			}`),
			RequiredPerm: auth.PermDiagramsRead,
			Handler:      toolDiagramListTemplates,
		},
		{
			Name: "diagram_create_instance",
			Description: "Create a new diagram instance for a container feature -- the same validated write " +
				"the REST POST /diagrams route performs. The container feature's class must be flagged " +
				"is_diagram_container, and diagram_type must be a valid DIAGRAM_TYPE domain value code (e.g. " +
				"ONE_LINE). Returns the created instance (including its new id). This WRITES to the " +
				"database: confirm with a human before calling.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"container":{
						"type":"object",
						"properties":{
							"feature_class":{"type":"string","description":"The container feature's class code."},
							"feature_id":{"type":"string","description":"The container feature's id (uuid)."}
						},
						"required":["feature_class","feature_id"],
						"additionalProperties":false,
						"description":"The feature that hosts this diagram."
					},
					"diagram_type":{"type":"string","description":"A DIAGRAM_TYPE domain value code (e.g. ONE_LINE)."},
					"name":{"type":"string","description":"Optional display name for the diagram."}
				},
				"required":["container","diagram_type"],
				"additionalProperties":false
			}`),
			Annotations: &Annotations{
				Title:           "Create diagram",
				ReadOnlyHint:    &falseHint, // this tool writes
				DestructiveHint: &falseHint, // additive: it creates a diagram, removing nothing
				IdempotentHint:  &falseHint, // a container may host many diagrams; each call adds one
				OpenWorldHint:   &falseHint, // acts only on this system's own database
			},
			RequiredPerm: auth.PermDiagramsWrite,
			Handler:      toolDiagramCreateInstance,
		},
		{
			Name: "diagram_rename_instance",
			Description: "Rename one diagram instance -- the same write the REST PATCH /diagrams/{id} route " +
				"performs. Send a name to set it, or an explicit null to clear the diagram's name back to " +
				"none. Idempotent: setting the name it already has re-applies the same state. Returns the " +
				"updated instance. This WRITES to the database: confirm with a human before calling.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"id":{"type":"string","description":"The diagram instance's id (uuid)."},
					"name":{"type":["string","null"],"description":"The new name; send null to clear it."}
				},
				"required":["id"],
				"additionalProperties":false
			}`),
			Annotations: &Annotations{
				Title:           "Rename diagram",
				ReadOnlyHint:    &falseHint, // this tool writes
				DestructiveHint: &falseHint, // a rename overwrites a label, it removes nothing
				IdempotentHint:  &trueHint,  // re-applying the same name yields the same state
				OpenWorldHint:   &falseHint, // acts only on this system's own database
			},
			RequiredPerm: auth.PermDiagramsWrite,
			Handler:      toolDiagramRenameInstance,
		},
		{
			Name: "diagram_delete_instance",
			Description: "Delete one diagram instance -- the same write the REST DELETE /diagrams/{id} route " +
				"performs. This cascades to every placement on the diagram (but not to the real features " +
				"they pointed at, nor to any template previously saved from it). Requires the escalated " +
				"diagrams:delete permission. Returns a small confirmation. This is a DESTRUCTIVE write: " +
				"confirm with a human before calling.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"id":{"type":"string","description":"The diagram instance's id (uuid)."}
				},
				"required":["id"],
				"additionalProperties":false
			}`),
			Annotations: &Annotations{
				Title:           "Delete diagram",
				ReadOnlyHint:    &falseHint, // this tool writes
				DestructiveHint: &trueHint,  // deletes the diagram and cascades to its placements
				IdempotentHint:  &falseHint, // a second delete of the same id reports not-found
				OpenWorldHint:   &falseHint, // acts only on this system's own database
			},
			RequiredPerm: auth.PermDiagramsDelete,
			Handler:      toolDiagramDeleteInstance,
		},
		{
			Name: "diagram_place_feature",
			Description: "Place (or move) one feature on one diagram -- the same write the REST POST " +
				"/diagrams/{id}/placements route performs. The feature's class must be flagged " +
				"is_diagram_placeable and pass the diagram type's optional placement allow-list. Placing " +
				"the same feature again just moves it (an idempotent upsert), never a second placement. An " +
				"optional path draws the feature as a polyline (only legal when its diagram shape is line). " +
				"Returns the placement. This WRITES to the database: confirm with a human before calling.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"diagram_id":{"type":"string","description":"The diagram instance's id (uuid) to place onto."},
					"feature_class":{"type":"string","description":"The feature's class code."},
					"feature_id":{"type":"string","description":"The feature's id (uuid)."},
					"x":{"type":"number","description":"The node's x anchor in the diagram's local space."},
					"y":{"type":"number","description":"The node's y anchor in the diagram's local space."},
					"rotation":{"type":"number","description":"Rotation in degrees (defaults to 0)."},
					"path":{"type":"array","items":{"type":"array","items":{"type":"number"}},"description":"Optional drawn polyline as an ordered array of [x, y] pairs (at least two); only valid when the feature's diagram shape is line."}
				},
				"required":["diagram_id","feature_class","feature_id"],
				"additionalProperties":false
			}`),
			Annotations: &Annotations{
				Title:           "Place feature on diagram",
				ReadOnlyHint:    &falseHint, // this tool writes
				DestructiveHint: &falseHint, // an upsert of one placement; it removes nothing
				IdempotentHint:  &trueHint,  // re-placing the same feature at the same spot yields the same state
				OpenWorldHint:   &falseHint, // acts only on this system's own database
			},
			RequiredPerm: auth.PermDiagramsWrite,
			Handler:      toolDiagramPlaceFeature,
		},
		{
			Name: "diagram_update_placement",
			Description: "Apply a geometry-free label change to one placement -- the same write the REST " +
				"PATCH /diagrams/{id}/placements/{placementID} route performs. Only label state is touched " +
				"(hide the label, and its offset / rotation / font-size transform); a placement's own " +
				"geometry (x/y/rotation/path) is owned by diagram_place_feature. Every field is optional; " +
				"an omitted field is left unchanged. Idempotent. Returns the updated placement. This " +
				"WRITES to the database: confirm with a human before calling.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"diagram_id":{"type":"string","description":"The diagram instance's id (uuid)."},
					"placement_id":{"type":"string","description":"The placement's id (uuid) on that diagram."},
					"label_hidden":{"type":"boolean","description":"Hide (true) or show (false) this placement's name label."},
					"label_dx":{"type":"number","description":"Label x offset from its natural anchor (glyph-local frame)."},
					"label_dy":{"type":"number","description":"Label y offset from its natural anchor (glyph-local frame)."},
					"label_rotation":{"type":"number","description":"Label rotation in degrees."},
					"label_scale":{"type":"number","description":"Label font-size multiplier (1 is default)."}
				},
				"required":["diagram_id","placement_id"],
				"additionalProperties":false
			}`),
			Annotations: &Annotations{
				Title:           "Update placement label",
				ReadOnlyHint:    &falseHint, // this tool writes
				DestructiveHint: &falseHint, // a label tweak overwrites, it removes nothing
				IdempotentHint:  &trueHint,  // re-applying the same label state yields the same state
				OpenWorldHint:   &falseHint, // acts only on this system's own database
			},
			RequiredPerm: auth.PermDiagramsWrite,
			Handler:      toolDiagramUpdatePlacement,
		},
		{
			Name: "diagram_remove_placement",
			Description: "Un-place one feature from one diagram -- the same write the REST DELETE " +
				"/diagrams/{id}/placements/{placementID} route performs. This removes only the placement " +
				"(where the feature sits on this diagram); the real feature itself is untouched. Returns a " +
				"small confirmation. This is a DESTRUCTIVE write: confirm with a human before calling.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"diagram_id":{"type":"string","description":"The diagram instance's id (uuid)."},
					"placement_id":{"type":"string","description":"The placement's id (uuid) on that diagram."}
				},
				"required":["diagram_id","placement_id"],
				"additionalProperties":false
			}`),
			Annotations: &Annotations{
				Title:           "Remove placement",
				ReadOnlyHint:    &falseHint, // this tool writes
				DestructiveHint: &trueHint,  // deletes the placement row
				IdempotentHint:  &falseHint, // a second remove of the same id reports not-found
				OpenWorldHint:   &falseHint, // acts only on this system's own database
			},
			RequiredPerm: auth.PermDiagramsWrite,
			Handler:      toolDiagramRemovePlacement,
		},
		{
			Name: "diagram_create_element",
			Description: "Add one non-feature element to a diagram -- the same write the REST POST " +
				"/diagrams/{id}/elements route performs. An element points at no real feature: it is diagram " +
				"furniture (a box, text, a schematic line, a stencil symbol, a title block, a legend, a " +
				"slot). kind and an x/y anchor are required; a line/bus/tap/control kind additionally needs " +
				"a path of at least two points, and a symbol needs style.iconRef. Returns the created " +
				"element. This WRITES to the database: confirm with a human before calling.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"diagram_id":{"type":"string","description":"The diagram instance's id (uuid) to add to."},
					"kind":{"type":"string","enum":["text","box","line","symbol","bus","tap","control","title_block","legend","slot"],"description":"The element kind."},
					"x":{"type":"number","description":"The element's x anchor in the diagram's local space."},
					"y":{"type":"number","description":"The element's y anchor in the diagram's local space."},
					"width":{"type":"number","description":"Width (box-like kinds only)."},
					"height":{"type":"number","description":"Height (box-like kinds only)."},
					"rotation":{"type":"number","description":"Rotation in degrees (defaults to 0)."},
					"path":{"type":"array","items":{"type":"array","items":{"type":"number"}},"description":"Polyline as an ordered array of [x, y] pairs (required, at least two, for line/bus/tap/control kinds)."},
					"text":{"type":"string","description":"Text content (text-like kinds)."},
					"style":{"type":"object","description":"Style object; a symbol element requires style.iconRef (the stencil glyph to render)."},
					"z_index":{"type":"integer","description":"Draw order (higher draws in front)."},
					"slot_class":{"type":"string","description":"For a slot element: the feature class it expects."},
					"slot_subtype":{"type":"integer","description":"For a slot element: the expected subtype."},
					"slot_role":{"type":"string","description":"For a slot element: the expected role."}
				},
				"required":["diagram_id","kind","x","y"],
				"additionalProperties":false
			}`),
			Annotations: &Annotations{
				Title:           "Add diagram element",
				ReadOnlyHint:    &falseHint, // this tool writes
				DestructiveHint: &falseHint, // additive: it creates one element, removing nothing
				IdempotentHint:  &falseHint, // each call adds another element
				OpenWorldHint:   &falseHint, // acts only on this system's own database
			},
			RequiredPerm: auth.PermDiagramsWrite,
			Handler:      toolDiagramCreateElement,
		},
		{
			Name: "diagram_update_element",
			Description: "Apply a partial change to one element (move, resize, restyle, relabel, reshape) -- " +
				"the same write the REST PATCH /diagrams/{id}/elements/{elementID} route performs. Fields " +
				"absent from the request are left unchanged; an explicit null path clears the polyline. " +
				"Idempotent. Returns the updated element. This WRITES to the database: confirm with a human " +
				"before calling.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"diagram_id":{"type":"string","description":"The diagram instance's id (uuid)."},
					"element_id":{"type":"string","description":"The element's id (uuid) on that diagram."},
					"kind":{"type":"string","enum":["text","box","line","symbol","bus","tap","control","title_block","legend","slot"],"description":"New element kind."},
					"x":{"type":"number","description":"New x anchor."},
					"y":{"type":"number","description":"New y anchor."},
					"width":{"type":"number","description":"New width."},
					"height":{"type":"number","description":"New height."},
					"rotation":{"type":"number","description":"New rotation in degrees."},
					"path":{"type":["array","null"],"items":{"type":"array","items":{"type":"number"}},"description":"New polyline ([x, y] pairs); send null to clear it."},
					"text":{"type":"string","description":"New text content."},
					"style":{"type":"object","description":"New style object."},
					"z_index":{"type":"integer","description":"New draw order."},
					"slot_class":{"type":"string","description":"New slot expected feature class."},
					"slot_subtype":{"type":"integer","description":"New slot expected subtype."},
					"slot_role":{"type":"string","description":"New slot expected role."}
				},
				"required":["diagram_id","element_id"],
				"additionalProperties":false
			}`),
			Annotations: &Annotations{
				Title:           "Update diagram element",
				ReadOnlyHint:    &falseHint, // this tool writes
				DestructiveHint: &falseHint, // a field patch overwrites, it doesn't remove the element
				IdempotentHint:  &trueHint,  // re-applying the same patch yields the same state
				OpenWorldHint:   &falseHint, // acts only on this system's own database
			},
			RequiredPerm: auth.PermDiagramsWrite,
			Handler:      toolDiagramUpdateElement,
		},
		{
			Name: "diagram_remove_element",
			Description: "Delete one element from one diagram -- the same write the REST DELETE " +
				"/diagrams/{id}/elements/{elementID} route performs. Returns a small confirmation. This is " +
				"a DESTRUCTIVE write: confirm with a human before calling.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"diagram_id":{"type":"string","description":"The diagram instance's id (uuid)."},
					"element_id":{"type":"string","description":"The element's id (uuid) on that diagram."}
				},
				"required":["diagram_id","element_id"],
				"additionalProperties":false
			}`),
			Annotations: &Annotations{
				Title:           "Remove diagram element",
				ReadOnlyHint:    &falseHint, // this tool writes
				DestructiveHint: &trueHint,  // deletes the element row
				IdempotentHint:  &falseHint, // a second remove of the same id reports not-found
				OpenWorldHint:   &falseHint, // acts only on this system's own database
			},
			RequiredPerm: auth.PermDiagramsWrite,
			Handler:      toolDiagramRemoveElement,
		},
		{
			Name: "diagram_save_as_template",
			Description: "Capture one diagram as a reusable template skeleton -- the same write the REST " +
				"POST /diagrams/{id}/save-as-template route performs. Every placed feature becomes an empty, " +
				"class-tagged slot (its feature id dropped -- the template binds to different real features " +
				"each time), and every non-feature element is copied verbatim into an opaque payload. A " +
				"name is required. Returns the template summary. This WRITES to the database: confirm with " +
				"a human before calling.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"diagram_id":{"type":"string","description":"The diagram instance's id (uuid) to capture."},
					"name":{"type":"string","description":"Required name for the new template."},
					"description":{"type":"string","description":"Optional description."}
				},
				"required":["diagram_id","name"],
				"additionalProperties":false
			}`),
			Annotations: &Annotations{
				Title:           "Save diagram as template",
				ReadOnlyHint:    &falseHint, // this tool writes
				DestructiveHint: &falseHint, // additive: it creates a template, removing nothing
				IdempotentHint:  &falseHint, // each call saves another template
				OpenWorldHint:   &falseHint, // acts only on this system's own database
			},
			RequiredPerm: auth.PermDiagramsWrite,
			Handler:      toolDiagramSaveAsTemplate,
		},
		{
			Name: "diagram_apply_template",
			Description: "Instantiate one template's slots and furniture onto one diagram -- the same write " +
				"the REST POST /diagrams/{id}/apply-template route performs. Every payload entry becomes a " +
				"new element on the diagram (slots left unbound, furniture as-is). Existing content is left " +
				"untouched -- a template is additive scaffolding, not a replacement. Returns the created " +
				"elements. This WRITES to the database: confirm with a human before calling.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"diagram_id":{"type":"string","description":"The diagram instance's id (uuid) to stamp onto."},
					"template_id":{"type":"string","description":"The template's id (uuid) to apply."}
				},
				"required":["diagram_id","template_id"],
				"additionalProperties":false
			}`),
			Annotations: &Annotations{
				Title:           "Apply diagram template",
				ReadOnlyHint:    &falseHint, // this tool writes
				DestructiveHint: &falseHint, // additive scaffolding: it adds elements, removing nothing
				IdempotentHint:  &falseHint, // each call stamps another copy of the template
				OpenWorldHint:   &falseHint, // acts only on this system's own database
			},
			RequiredPerm: auth.PermDiagramsWrite,
			Handler:      toolDiagramApplyTemplate,
		},
		{
			Name: "diagram_delete_template",
			Description: "Delete one template from the reusable library -- the same write the REST DELETE " +
				"/diagrams/templates/{templateID} route performs. Copies already applied to diagrams live as " +
				"their own element rows, so deleting the template never touches any diagram. Returns a small " +
				"confirmation. This is a DESTRUCTIVE write: confirm with a human before calling.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"template_id":{"type":"string","description":"The template's id (uuid)."}
				},
				"required":["template_id"],
				"additionalProperties":false
			}`),
			Annotations: &Annotations{
				Title:           "Delete diagram template",
				ReadOnlyHint:    &falseHint, // this tool writes
				DestructiveHint: &trueHint,  // deletes the template row
				IdempotentHint:  &falseHint, // a second delete of the same id reports not-found
				OpenWorldHint:   &falseHint, // acts only on this system's own database
			},
			RequiredPerm: auth.PermDiagramsWrite,
			Handler:      toolDiagramDeleteTemplate,
		},
		{
			Name: "diagram_sectionalize_bus",
			Description: "Cut a drawn busbar placement in two, dividing its connectivity -- the same " +
				"composite write the REST POST /diagrams/{id}/placements/{placementID}/sectionalize route " +
				"performs. The placement must be a bar (a line-on-diagram placement carrying a hand-drawn " +
				"path); the bar is cut at the given diagram-space point into a near half (the original " +
				"feature, shortened) and a far half (a newly minted bus feature with its own placement), " +
				"and each original connection re-homes onto whichever half its other end sits nearer. An " +
				"optional sectionalizer feature is wired to both halves best-effort. Because it mints a " +
				"feature and commits connectivity, this needs diagrams:write PLUS features:write PLUS " +
				"connectivity:commit. Returns the two bus halves, the far placement id, and how many " +
				"connections moved. This WRITES to the database: confirm with a human before calling.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"diagram_id":{"type":"string","description":"The diagram instance's id (uuid)."},
					"placement_id":{"type":"string","description":"The busbar placement's id (uuid) to cut."},
					"at":{
						"type":"object",
						"properties":{
							"x":{"type":"number","description":"Cut-point x in the diagram's local space."},
							"y":{"type":"number","description":"Cut-point y in the diagram's local space."}
						},
						"required":["x","y"],
						"additionalProperties":false,
						"description":"Where along the bar to cut (diagram-space point, not lng/lat)."
					},
					"new_bus":{
						"type":"object",
						"properties":{
							"subtype":{"type":"integer","description":"Subtype for the new far-half bus feature."},
							"name":{"type":"string","description":"Name for the new far-half bus feature."},
							"attributes":{"type":"object","description":"Attribute values for the new far-half bus feature."}
						},
						"additionalProperties":false,
						"description":"How to fill out the newly minted far-half bus feature (geometry is inherited, never supplied)."
					},
					"sectionalizer":{
						"type":"object",
						"properties":{
							"feature_class":{"type":"string","description":"The sectionalizer feature's class code."},
							"feature_id":{"type":"string","description":"The sectionalizer feature's id (uuid)."},
							"near_terminal":{"type":"string","description":"The sectionalizer terminal facing the near (original) bus half."},
							"far_terminal":{"type":"string","description":"The sectionalizer terminal facing the far (new) bus half."}
						},
						"required":["feature_class","feature_id"],
						"additionalProperties":false,
						"description":"Optional bus-tie / sectionalizer switch to wire to both halves."
					}
				},
				"required":["diagram_id","placement_id","at","new_bus"],
				"additionalProperties":false
			}`),
			Annotations: &Annotations{
				Title:           "Sectionalize busbar",
				ReadOnlyHint:    &falseHint, // this tool writes
				DestructiveHint: &falseHint, // it splits a bus into two live halves, deleting nothing
				IdempotentHint:  &falseHint, // each call cuts again, minting another far-half bus
				OpenWorldHint:   &falseHint, // acts only on this system's own database
			},
			RequiredPerm: auth.PermDiagramsWrite,
			ExtraPerms:   []string{auth.PermFeaturesWrite, auth.PermConnectivityCommit},
			Handler:      toolDiagramSectionalizeBus,
		},
	}
}

// --- handlers ---

func toolDiagramGetInstance(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.ID == "" {
		return nil, fmt.Errorf("id is required")
	}
	return diagram.GetInstance(ctx, db, in.ID)
}

func toolDiagramListInstances(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		ContainerFeatureClass string `json:"container_feature_class"`
		ContainerFeatureID    string `json:"container_feature_id"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.ContainerFeatureClass == "" || in.ContainerFeatureID == "" {
		return nil, fmt.Errorf("container_feature_class and container_feature_id are required")
	}
	return diagram.ListInstances(ctx, db, in.ContainerFeatureClass, in.ContainerFeatureID)
}

func toolDiagramListPlacements(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		DiagramID string `json:"diagram_id"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.DiagramID == "" {
		return nil, fmt.Errorf("diagram_id is required")
	}
	return diagram.ListPlacements(ctx, db, in.DiagramID)
}

func toolDiagramListElements(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		DiagramID string `json:"diagram_id"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.DiagramID == "" {
		return nil, fmt.Errorf("diagram_id is required")
	}
	return diagram.ListElements(ctx, db, in.DiagramID)
}

func toolDiagramListTemplates(ctx context.Context, db bun.IDB, _ json.RawMessage) (any, error) {
	return diagram.ListTemplates(ctx, db)
}

func toolDiagramCreateInstance(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in diagram.CreateInstanceInput
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.Container.FeatureClass == "" || in.Container.FeatureID == "" {
		return nil, fmt.Errorf("container.feature_class and container.feature_id are required")
	}
	if in.DiagramType == "" {
		return nil, fmt.Errorf("diagram_type is required")
	}
	return diagram.CreateInstance(ctx, db, in)
}

func toolDiagramRenameInstance(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		ID   string  `json:"id"`
		Name *string `json:"name"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.ID == "" {
		return nil, fmt.Errorf("id is required")
	}
	return diagram.RenameInstance(ctx, db, in.ID, in.Name)
}

func toolDiagramDeleteInstance(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.ID == "" {
		return nil, fmt.Errorf("id is required")
	}
	if err := diagram.DeleteInstance(ctx, db, in.ID); err != nil {
		return nil, err
	}
	return map[string]any{"deleted": true, "id": in.ID}, nil
}

func toolDiagramPlaceFeature(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var path struct {
		DiagramID string `json:"diagram_id"`
	}
	if err := json.Unmarshal(args, &path); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if path.DiagramID == "" {
		return nil, fmt.Errorf("diagram_id is required")
	}
	var in diagram.PlaceFeatureInput
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.FeatureClass == "" || in.FeatureID == "" {
		return nil, fmt.Errorf("feature_class and feature_id are required")
	}
	return diagram.PlaceFeature(ctx, db, path.DiagramID, in)
}

func toolDiagramUpdatePlacement(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var path struct {
		DiagramID   string `json:"diagram_id"`
		PlacementID string `json:"placement_id"`
	}
	if err := json.Unmarshal(args, &path); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if path.DiagramID == "" || path.PlacementID == "" {
		return nil, fmt.Errorf("diagram_id and placement_id are required")
	}
	var in diagram.UpdatePlacementInput
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	return diagram.UpdatePlacement(ctx, db, path.DiagramID, path.PlacementID, in)
}

func toolDiagramRemovePlacement(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		DiagramID   string `json:"diagram_id"`
		PlacementID string `json:"placement_id"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.DiagramID == "" || in.PlacementID == "" {
		return nil, fmt.Errorf("diagram_id and placement_id are required")
	}
	if err := diagram.RemovePlacement(ctx, db, in.DiagramID, in.PlacementID); err != nil {
		return nil, err
	}
	return map[string]any{"deleted": true, "diagram_id": in.DiagramID, "placement_id": in.PlacementID}, nil
}

func toolDiagramCreateElement(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var path struct {
		DiagramID string `json:"diagram_id"`
	}
	if err := json.Unmarshal(args, &path); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if path.DiagramID == "" {
		return nil, fmt.Errorf("diagram_id is required")
	}
	var in diagram.ElementInput
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.Kind == "" {
		return nil, fmt.Errorf("kind is required")
	}
	return diagram.CreateElement(ctx, db, path.DiagramID, in)
}

func toolDiagramUpdateElement(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var path struct {
		DiagramID string `json:"diagram_id"`
		ElementID string `json:"element_id"`
	}
	if err := json.Unmarshal(args, &path); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if path.DiagramID == "" || path.ElementID == "" {
		return nil, fmt.Errorf("diagram_id and element_id are required")
	}
	var in diagram.ElementInput
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	return diagram.UpdateElement(ctx, db, path.DiagramID, path.ElementID, in)
}

func toolDiagramRemoveElement(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		DiagramID string `json:"diagram_id"`
		ElementID string `json:"element_id"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.DiagramID == "" || in.ElementID == "" {
		return nil, fmt.Errorf("diagram_id and element_id are required")
	}
	if err := diagram.RemoveElement(ctx, db, in.DiagramID, in.ElementID); err != nil {
		return nil, err
	}
	return map[string]any{"deleted": true, "diagram_id": in.DiagramID, "element_id": in.ElementID}, nil
}

func toolDiagramSaveAsTemplate(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var path struct {
		DiagramID string `json:"diagram_id"`
	}
	if err := json.Unmarshal(args, &path); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if path.DiagramID == "" {
		return nil, fmt.Errorf("diagram_id is required")
	}
	var in diagram.SaveAsTemplateInput
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.Name == "" {
		return nil, fmt.Errorf("name is required")
	}
	return diagram.SaveAsTemplate(ctx, db, path.DiagramID, in)
}

func toolDiagramApplyTemplate(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		DiagramID  string `json:"diagram_id"`
		TemplateID string `json:"template_id"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.DiagramID == "" || in.TemplateID == "" {
		return nil, fmt.Errorf("diagram_id and template_id are required")
	}
	return diagram.ApplyTemplate(ctx, db, in.DiagramID, in.TemplateID)
}

func toolDiagramDeleteTemplate(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		TemplateID string `json:"template_id"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.TemplateID == "" {
		return nil, fmt.Errorf("template_id is required")
	}
	if err := diagram.DeleteTemplate(ctx, db, in.TemplateID); err != nil {
		return nil, err
	}
	return map[string]any{"deleted": true, "template_id": in.TemplateID}, nil
}

func toolDiagramSectionalizeBus(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var path struct {
		DiagramID   string `json:"diagram_id"`
		PlacementID string `json:"placement_id"`
	}
	if err := json.Unmarshal(args, &path); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if path.DiagramID == "" || path.PlacementID == "" {
		return nil, fmt.Errorf("diagram_id and placement_id are required")
	}
	var in diagram.SectionalizeInput
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.At.X == nil || in.At.Y == nil {
		return nil, fmt.Errorf("at.x and at.y are required (the diagram-space point to cut the bar at)")
	}
	// SectionalizeBus owns its own transaction, so it needs the concrete *bun.DB.
	sdb, err := asDB(db)
	if err != nil {
		return nil, err
	}
	return diagram.SectionalizeBus(ctx, sdb, path.DiagramID, path.PlacementID, in)
}
