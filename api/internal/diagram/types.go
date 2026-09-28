// Package diagram is the explicit, CRUD-shaped engine for
// runtime.diagram_instances/diagram_placements (migrations/0019) --
// docs/metadata-core.md's "Diagram containers" design. Deliberately not
// shaped like api/internal/connectivity or api/internal/relationship:
// there's no rule table to match a placement against, no cardinality,
// no legality question beyond one static flag -- a diagram_instance's
// container_feature_class_id must have is_diagram_container = true, the
// only gate this package enforces before ever writing. Placing (or
// moving) a feature into a diagram is otherwise just recording where it
// sits in that diagram's own local x/y coordinate space, entirely
// independent of whatever real geom column the same feature might also
// have.
package diagram

import (
	"encoding/json"
	"time"
)

// featureRef identifies one specific feature row -- same shape as
// api/internal/connectivity and api/internal/relationship's own
// identical type, kept as its own copy for the same reason theirs are:
// sharing would mean exporting internals across packages for one small
// type.
type featureRef struct {
	FeatureClass string `json:"feature_class"`
	FeatureID    string `json:"feature_id"`
}

// createInstanceRequest is the POST /diagrams body. DiagramType is a
// domain value code (e.g. "ONE_LINE") under the DIAGRAM_TYPE domain,
// not a raw metadata.domain_values id -- same human-typed-code
// convention every other API request in this system uses (terminal
// codes, subtype codes, ...), never a UUID a caller would have to look
// up first.
type createInstanceRequest struct {
	Container   featureRef `json:"container"`
	DiagramType string     `json:"diagram_type"`
	Name        *string    `json:"name,omitempty"`
	// SourceTemplateID stamps the instance with the template it was built from
	// (migration 0122), set only by the assembly auto-diagram builder so the
	// diagram can later be regenerated. The REST create handler never sets it --
	// hand-built diagrams have no source template. Not a JSON field (internal).
	SourceTemplateID *string `json:"-"`
}

// instanceResponse is the shape returned for one diagram instance,
// whether from create, get, or list.
type instanceResponse struct {
	ID          string     `json:"id"`
	Container   featureRef `json:"container"`
	DiagramType string     `json:"diagram_type"`
	Name        *string    `json:"name,omitempty"`
	// Regenerable is true when this diagram was auto-generated from a template
	// (its source_template_id is set, migration 0122) and so can be rebuilt via
	// POST /{id}/regenerate. False for hand-built diagrams -- the canvas shows the
	// Regenerate control only when this is true.
	Regenerable bool      `json:"regenerable"`
	CreatedAt   time.Time `json:"created_at"`
}

// renameInstanceRequest is the PATCH /diagrams/{id} body -- Name is a
// pointer so an explicit null clears a diagram's name back to none,
// same convention createInstanceRequest.Name already uses.
type renameInstanceRequest struct {
	Name *string `json:"name"`
}

// placeRequest is the POST /diagrams/{id}/placements body. Placing the
// same feature into the same diagram a second time moves it (an
// idempotent upsert on the table's own unique constraint), not a second
// placement -- see migrations/0019's diagram_placements_one_per_diagram.
type placeRequest struct {
	FeatureClass string  `json:"feature_class"`
	FeatureID    string  `json:"feature_id"`
	X            float64 `json:"x"`
	Y            float64 `json:"y"`
	Rotation     float64 `json:"rotation"`
	// Path is an optional hand-drawn polyline for a line placement
	// (Phase 12, option B2): an ordered [[x, y], ...] array in the
	// diagram's local coordinate space. Null/absent for a point
	// placement, which keeps using X/Y/Rotation. When present the class
	// must be geometry_type = "line" (validated in placeFeature) and
	// X/Y still anchor the node's label/hit target.
	Path json.RawMessage `json:"path,omitempty"`
}

// placementResponse is the shape returned for one placement, whether
// from place or list.
type placementResponse struct {
	ID           string          `json:"id"`
	FeatureClass string          `json:"feature_class"`
	FeatureID    string          `json:"feature_id"`
	X            float64         `json:"x"`
	Y            float64         `json:"y"`
	Rotation     float64         `json:"rotation"`
	Path         json.RawMessage `json:"path,omitempty"`
	// LabelHidden is true when the canvas should not draw this placement's
	// name label (migration 0054). Always emitted so the client never has to
	// guess a default.
	LabelHidden bool `json:"label_hidden"`
	// Label transform (migration 0056): offset from the label's natural
	// anchor in the glyph-local frame, its own rotation in degrees, and a
	// font-size multiplier. Always emitted; 0/0/0/1 is the default pinned
	// position.
	LabelDX       float64 `json:"label_dx"`
	LabelDY       float64 `json:"label_dy"`
	LabelRotation float64 `json:"label_rotation"`
	LabelScale    float64 `json:"label_scale"`
}

// updatePlacementRequest is the PATCH /diagrams/{id}/placements/{placementID}
// body. It carries only geometry-free label state -- visibility (migration
// 0054) and the label transform (migration 0056). A placement's own geometry
// (x/y/rotation/path) is owned by the place/move upsert, not here. Every field
// is a pointer so an omitted field is a no-op rather than a silent reset.
type updatePlacementRequest struct {
	LabelHidden   *bool    `json:"label_hidden"`
	LabelDX       *float64 `json:"label_dx"`
	LabelDY       *float64 `json:"label_dy"`
	LabelRotation *float64 `json:"label_rotation"`
	LabelScale    *float64 `json:"label_scale"`
}

// elementKinds is the closed set of diagram_elements.kind values the API
// accepts, mirroring migrations 0046 + 0047 + 0050 + 0084's CHECK constraint so
// a bad kind is rejected with a clear message before it ever reaches the
// constraint. text/box/line (Slice 1), symbol (Slice 2, migration 0047), bus/tap
// (Slice 5, migration 0050), and control (Phase 46 Slice 1, migration 0084) are
// the line/box/glyph furniture; title_block/legend/slot are the template
// furniture -- all rendered by the canvas today.
var elementKinds = map[string]bool{
	"text":        true,
	"box":         true,
	"line":        true,
	"symbol":      true,
	"bus":         true,
	"tap":         true,
	"control":     true,
	"title_block": true,
	"legend":      true,
	"slot":        true,
}

// pathKinds is the subset of elementKinds whose geometry *is* a polyline: a
// create request for one of these requires a `path` of at least two points
// (there's nothing to draw otherwise). line is the original schematic-only
// line; bus/tap (Slice 5) are the heavy busbar and its right-angle taps;
// control (Phase 46) is the dashed, arrowheaded control/trip signal line -- all
// stored the same way, an ordered [[x,y],...] run in the diagram's local space.
// box/text/symbol/title_block/legend/slot anchor at x/y instead.
var pathKinds = map[string]bool{
	"line":    true,
	"bus":     true,
	"tap":     true,
	"control": true,
}

// elementRequest is the POST/PATCH body for a diagram element (Phase 24
// keystone). An element is diagram furniture that points at no real feature,
// so unlike placeRequest it carries no feature_class/feature_id -- just where
// it sits (x/y, optional width/height/rotation), what it is (kind), and how it
// looks (style). Pointer fields distinguish "absent" from "zero/null" on a
// PATCH: a nil field is left unchanged, a present one (including an explicit
// null Text/Path) is applied. On POST, X/Y/Kind are required and the rest fall
// back to their column defaults.
type elementRequest struct {
	Kind     string          `json:"kind"`
	X        *float64        `json:"x,omitempty"`
	Y        *float64        `json:"y,omitempty"`
	Width    *float64        `json:"width,omitempty"`
	Height   *float64        `json:"height,omitempty"`
	Rotation *float64        `json:"rotation,omitempty"`
	Path     json.RawMessage `json:"path,omitempty"`
	Text     *string         `json:"text,omitempty"`
	Style    json.RawMessage `json:"style,omitempty"`
	ZIndex   *int            `json:"z_index,omitempty"`
	// Slot expectation (kind = "slot"); ignored for other kinds. Binding
	// (BoundClass/BoundID) is set in a later slice, not by Slice 1 callers,
	// but the response carries it through once present.
	SlotClass   *string `json:"slot_class,omitempty"`
	SlotSubtype *int    `json:"slot_subtype,omitempty"`
	SlotRole    *string `json:"slot_role,omitempty"`
}

// elementResponse is the shape returned for one element, from create, list,
// or update. Style is always a JSON object (never null on the wire); Path is
// omitted when absent, same as a placement's.
type elementResponse struct {
	ID          string          `json:"id"`
	Kind        string          `json:"kind"`
	X           float64         `json:"x"`
	Y           float64         `json:"y"`
	Width       *float64        `json:"width,omitempty"`
	Height      *float64        `json:"height,omitempty"`
	Rotation    float64         `json:"rotation"`
	Path        json.RawMessage `json:"path,omitempty"`
	Text        *string         `json:"text,omitempty"`
	Style       json.RawMessage `json:"style"`
	ZIndex      int             `json:"z_index"`
	SlotClass   *string         `json:"slot_class,omitempty"`
	SlotSubtype *int            `json:"slot_subtype,omitempty"`
	SlotRole    *string         `json:"slot_role,omitempty"`
	BoundClass  *string         `json:"bound_class,omitempty"`
	BoundID     *string         `json:"bound_id,omitempty"`
}

// promoteRequest is the POST /diagrams/{id}/elements/{elementID}/promote body.
// It carries only what CreateFeature needs -- the new feature's subtype, name,
// and attributes, the very fields the create form already gathers. The target
// feature class is NOT in the request: it is resolved from the element itself
// (slot_class, else style.promoteClass, else the stencil's promote_class), so an
// admin can't aim a stencil at an arbitrary class. Same field names/tags as
// crud.CreateFeatureInput so the two describe an identical create.
type promoteRequest struct {
	// FeatureClass is the class the caller chose to promote this element into.
	// It is honoured ONLY as a fallback: a stencil that intrinsically maps to a
	// class (a typed slot, or a decoration tagged with a promoteClass/stencil
	// promote_class) always wins over the request, so a mapped stencil can never
	// be redirected. It exists for the picker path -- promoting a piece of
	// generic decoration (a plain line/box) that carries no mapping of its own.
	FeatureClass *string        `json:"feature_class,omitempty"`
	Subtype      *int           `json:"subtype,omitempty"`
	Name         *string        `json:"name,omitempty"`
	Attributes   map[string]any `json:"attributes"`
}

// promoteResponse is returned once an element is promoted: the new feature's
// class + id, plus the placement that now sits where the stencil was (so the
// canvas can select the feature and drop the vanished element in one refetch).
type promoteResponse struct {
	FeatureClass string            `json:"feature_class"`
	FeatureID    string            `json:"feature_id"`
	Placement    placementResponse `json:"placement"`
}

// replaceRequest is the POST /diagrams/{id}/placements/{placementID}/replace-from-map
// body: the real map feature that overwrites the stub the placement currently
// holds. The stub is identified by the placement id in the URL, not the body,
// so a caller only names what the placement should become. The class must match
// the stub's (enforced in replaceFromMap) -- the swap re-points by feature id.
type replaceRequest struct {
	FeatureClass string `json:"feature_class"`
	FeatureID    string `json:"feature_id"`
}

// replaceResponse is returned once a stub is overwritten by a map feature: the
// map feature's class + id, the placement that now holds it (re-pointed in
// place, so same id/coordinates as before), and how much wiring transferred off
// the retired stub -- so the canvas can refetch and confirm the topology held.
type replaceResponse struct {
	FeatureClass string            `json:"feature_class"`
	FeatureID    string            `json:"feature_id"`
	Placement    placementResponse `json:"placement"`
	// ConnectionsMoved / RelationshipsMoved count the edges carried from the
	// retired stub onto the map feature (rewrites plus dropped duplicates).
	ConnectionsMoved   int `json:"connections_moved"`
	RelationshipsMoved int `json:"relationships_moved"`
}

// apiError is the JSON body returned for any 4xx/5xx response.
type apiError struct {
	Error string `json:"error"`
}
