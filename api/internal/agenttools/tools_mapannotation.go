package agenttools

// The Map Annotation ("redline") module's tools -- the freehand map-markup
// surface (text notes, boxes, lines, arrows drawn over the map) exposed through
// the same single agenttools registry as every other activity. Each tool here
// is a thin adapter over an already-exported api/internal/mapannotation wrapper
// -- the same operations the REST /annotations routes and the map's annotation
// layer call -- and declares the RBAC permission that route requires: the read
// verbs (annotations:read), the mutating verbs (annotations:write). The read
// tools carry nil annotations (buildRegistry stamps readOnlyHint:true); the
// write tools carry explicit annotations so a client confirms with a human
// first (delete also DestructiveHint, update also IdempotentHint).

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/kwaabs/voltedge/api/internal/auth"
	"github.com/kwaabs/voltedge/api/internal/mapannotation"
	"github.com/uptrace/bun"
)

// annotationTools returns the Map Annotation tools in advertised order: the
// reads first (list / get), then the writes (create / update / delete).
func annotationTools() []Tool {
	return []Tool{
		{
			Name: "annotation_list",
			Description: "List map annotations (the redline layer -- text notes, boxes, lines, arrows drawn " +
				"over the map), ordered back-to-front by z_index -- the same read the REST GET /annotations " +
				"route returns. Both filters are optional: narrow to a single territory_id, and/or to a " +
				"lon/lat viewport bbox [minX, minY, maxX, maxY] in EPSG:4326. Each row carries its kind, " +
				"GeoJSON geometry, text, style, z_index, and resolved territory.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"territory_id":{"type":"string","description":"Filter to annotations assigned to this territory (uuid)."},
					"bbox":{"type":"array","items":{"type":"number"},"minItems":4,"maxItems":4,"description":"Optional lon/lat viewport filter [minX, minY, maxX, maxY] in EPSG:4326."}
				},
				"additionalProperties":false
			}`),
			RequiredPerm: auth.PermAnnotationsRead,
			Handler:      toolAnnotationList,
		},
		{
			Name: "annotation_get",
			Description: "Fetch a single map annotation by its id -- the same read the REST GET " +
				"/annotations/{id} route returns. Returns the annotation's kind, GeoJSON geometry, text, " +
				"style, z_index, resolved territory, and audit timestamps.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"id":{"type":"string","description":"The annotation's id (uuid)."}
				},
				"required":["id"],
				"additionalProperties":false
			}`),
			RequiredPerm: auth.PermAnnotationsRead,
			Handler:      toolAnnotationGet,
		},
		{
			Name: "annotation_create",
			Description: "Create a new map annotation (redline markup) -- the same validated write the REST " +
				"POST /annotations route performs. kind and geometry are required and must agree: a 'text' " +
				"note is a Point, a 'box' a Polygon, a 'line' or 'arrow' a LineString. A 'text' annotation " +
				"also requires non-empty text. style (appearance blob), z_index, and a territory_id " +
				"override are optional; the territory is otherwise assigned spatially from the geometry. " +
				"Returns the created annotation (including its new id). This WRITES to the database: " +
				"confirm with a human before calling.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"kind":{"type":"string","enum":["text","box","line","arrow"],"description":"The annotation kind; fixes the required geometry type (text=Point, box=Polygon, line/arrow=LineString)."},
					"geometry":{"type":"object","description":"GeoJSON geometry (EPSG:4326) whose type must match the kind."},
					"text":{"type":"string","description":"Note content; required (non-empty) for kind='text', an optional caption otherwise."},
					"style":{"type":"object","description":"Optional per-annotation appearance blob (stroke/fill/dash/colour/font/arrowheads); defaults to an empty object."},
					"z_index":{"type":"integer","description":"Optional stacking order; higher draws in front. Defaults to 0."},
					"territory_id":{"type":"string","description":"Optional territory (uuid) override; otherwise assigned spatially from the geometry."}
				},
				"required":["kind","geometry"],
				"additionalProperties":false
			}`),
			Annotations: &Annotations{
				Title:           "Create annotation",
				ReadOnlyHint:    &falseHint, // this tool writes
				DestructiveHint: &falseHint, // a create is additive, reversible by deleting the annotation
				IdempotentHint:  &falseHint, // repeating it creates another annotation
				OpenWorldHint:   &falseHint, // acts only on this system's own database
			},
			RequiredPerm: auth.PermAnnotationsWrite,
			Handler:      toolAnnotationCreate,
		},
		{
			Name: "annotation_update",
			Description: "Patch an existing map annotation -- the same sparse write the REST PATCH " +
				"/annotations/{id} route performs. kind is immutable; every other field is optional and " +
				"only what you send changes. A new geometry is validated against the row's existing kind " +
				"and re-runs spatial territory assignment. A 'text' annotation cannot be blanked. Returns " +
				"the updated annotation. This WRITES to the database: confirm with a human before calling.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"id":{"type":"string","description":"The annotation's id (uuid)."},
					"geometry":{"type":"object","description":"New GeoJSON geometry (EPSG:4326); must match the annotation's existing kind."},
					"text":{"type":"string","description":"New note content (cannot be blank for a 'text' annotation)."},
					"style":{"type":"object","description":"New appearance blob; replaces the stored style."},
					"z_index":{"type":"integer","description":"New stacking order."},
					"territory_id":{"type":"string","description":"Territory (uuid) override; only takes effect alongside a geometry change."}
				},
				"required":["id"],
				"additionalProperties":false
			}`),
			Annotations: &Annotations{
				Title:           "Update annotation",
				ReadOnlyHint:    &falseHint, // this tool writes
				DestructiveHint: &falseHint, // a field patch overwrites, it doesn't remove the annotation
				IdempotentHint:  &trueHint,  // re-applying the same patch yields the same state
				OpenWorldHint:   &falseHint, // acts only on this system's own database
			},
			RequiredPerm: auth.PermAnnotationsWrite,
			Handler:      toolAnnotationUpdate,
		},
		{
			Name: "annotation_delete",
			Description: "Delete a map annotation by its id -- the same write the REST DELETE " +
				"/annotations/{id} route performs. The removal is permanent (there is no restore). Returns " +
				"a confirmation of the deleted id. This WRITES to the database: confirm with a human " +
				"before calling.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"id":{"type":"string","description":"The annotation's id (uuid) to delete."}
				},
				"required":["id"],
				"additionalProperties":false
			}`),
			Annotations: &Annotations{
				Title:           "Delete annotation",
				ReadOnlyHint:    &falseHint, // this tool writes
				DestructiveHint: &trueHint,  // it removes the annotation outright, no restore
				IdempotentHint:  &falseHint, // a second delete of the same id is a not-found error
				OpenWorldHint:   &falseHint, // acts only on this system's own database
			},
			RequiredPerm: auth.PermAnnotationsWrite,
			Handler:      toolAnnotationDelete,
		},
	}
}

// --- handlers ---

func toolAnnotationList(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		TerritoryID *string   `json:"territory_id"`
		BBox        []float64 `json:"bbox"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	// mapannotation.List validates the bbox length (must be 4) and surfaces a bad
	// one in-band as the tool error.
	return mapannotation.List(ctx, db, in.TerritoryID, in.BBox)
}

func toolAnnotationGet(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.ID == "" {
		return nil, fmt.Errorf("id is required")
	}
	return mapannotation.Get(ctx, db, in.ID)
}

func toolAnnotationCreate(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var req mapannotation.AnnotationInput
	if err := json.Unmarshal(args, &req); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	// The engine validates the kind/geometry agreement, the non-empty-text rule
	// for text notes, and the style/territory; its 400s surface in-band.
	return mapannotation.Create(ctx, db, req)
}

func toolAnnotationUpdate(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	// Peel the id first, then unmarshal the same bytes into the request body (id
	// is not part of AnnotationInput).
	var idArg struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(args, &idArg); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if idArg.ID == "" {
		return nil, fmt.Errorf("id is required")
	}
	var req mapannotation.AnnotationInput
	if err := json.Unmarshal(args, &req); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	// Kind is immutable on update; the engine ignores any kind sent here and
	// validates a new geometry against the row's existing kind.
	return mapannotation.Update(ctx, db, idArg.ID, req)
}

func toolAnnotationDelete(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.ID == "" {
		return nil, fmt.Errorf("id is required")
	}
	if err := mapannotation.Delete(ctx, db, in.ID); err != nil {
		return nil, err
	}
	return map[string]any{"deleted": true, "id": in.ID}, nil
}
