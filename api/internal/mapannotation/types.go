package mapannotation

import (
	"encoding/json"
	"time"
)

// annotationRequest is the body for both create (POST) and update (PATCH),
// mirroring diagram's elementRequest: pointer/omitempty fields so a PATCH can
// tell "field absent -> leave unchanged" from "field present -> apply". On
// POST, Kind and Geometry are required and the rest fall back to the column
// defaults ('{}' style, 0 z_index, spatial-assigned territory).
//
// Kind is immutable after create: a text is a Point, a box a Polygon, a line/
// arrow a LineString, so changing the kind would contradict the stored
// geometry. The update path ignores Kind and validates any new Geometry
// against the row's existing kind.
type annotationRequest struct {
	Kind     string          `json:"kind,omitempty"`
	Geometry json.RawMessage `json:"geometry,omitempty"`
	// Text is the note content. A nil pointer (absent or JSON null) means
	// "leave unchanged" on PATCH; a present string sets it. Required (non-empty)
	// for kind='text' on create, an optional caption otherwise.
	Text *string `json:"text,omitempty"`
	// Style is the per-annotation appearance blob (stroke/fill/dash/colour/
	// font/arrowheads). Absent/null on create -> the '{}' default; on update ->
	// left unchanged.
	Style       json.RawMessage `json:"style,omitempty"`
	ZIndex      *int            `json:"z_index,omitempty"`
	TerritoryID *string         `json:"territory_id,omitempty"`
}

// annotationResponse is the shape returned from create, list, and update.
// Geometry is always GeoJSON (never null on the wire -- geom is NOT NULL);
// Style is always an object. Text/TerritoryID/UpdatedAt are omitted when
// absent.
type annotationResponse struct {
	ID          string          `json:"id"`
	Kind        string          `json:"kind"`
	Geometry    json.RawMessage `json:"geometry"`
	Text        *string         `json:"text,omitempty"`
	Style       json.RawMessage `json:"style"`
	ZIndex      int             `json:"z_index"`
	TerritoryID *string         `json:"territory_id,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   *time.Time      `json:"updated_at,omitempty"`
}

// apiError is the JSON body returned for any 4xx/5xx response.
type apiError struct {
	Error string `json:"error"`
}
