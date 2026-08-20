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
}

// instanceResponse is the shape returned for one diagram instance,
// whether from create, get, or list.
type instanceResponse struct {
	ID          string     `json:"id"`
	Container   featureRef `json:"container"`
	DiagramType string     `json:"diagram_type"`
	Name        *string    `json:"name,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
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
}

// apiError is the JSON body returned for any 4xx/5xx response.
type apiError struct {
	Error string `json:"error"`
}
