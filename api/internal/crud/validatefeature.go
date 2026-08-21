package crud

import (
	"encoding/json"

	"github.com/kwaabs/voltedge/api/internal/metadata"
)

// ValidateFeatureInput describes a proposed feature to validate, mirroring the
// create request minus everything that only matters once you actually write:
// no name, no territory_id. It is deliberately create-shaped -- the dry-run
// answers "would creating this feature pass validation?".
type ValidateFeatureInput struct {
	// Subtype is the subtype code, required when the feature class defines
	// subtypes and forbidden when it doesn't (same rule as create).
	Subtype *int `json:"subtype"`
	// Geometry is the raw GeoJSON geometry object, required exactly when the
	// feature class has geometry.
	Geometry json.RawMessage `json:"geometry"`
	// Attributes are the field values keyed by field name.
	Attributes map[string]any `json:"attributes"`
}

// ValidateFeatureReport is the result of a dry-run validation. Valid is true
// only when every check the create path runs in memory passes; otherwise Error
// carries the first blocking problem -- the same first error the create
// endpoint would return, since both go through validateForCreate.
type ValidateFeatureReport struct {
	Valid bool   `json:"valid"`
	Error string `json:"error,omitempty"`
}

// ValidateFeature runs the create-time validation sequence against an already
// loaded metadata bundle without touching the database or writing anything. It
// is the exported, read-only entrypoint behind the Phase 18 MCP validate_feature
// tool (docs/phase-18-mcp.md): an agent can check whether a feature it intends
// to create is well-formed before committing to a write.
//
// It shares validateForCreate with the real create handler, so the two can
// never drift on what "valid" means. Being an in-memory pre-flight, it does NOT
// see anything only the database enforces on the actual insert -- foreign-key
// existence, uniqueness, PostGIS coordinate validity -- nor does it run the
// territory assignment (a spatial DB call). A Valid:true report therefore means
// "passes all metadata-level rules", not "guaranteed to insert".
func ValidateFeature(loaded *metadata.Loaded, in ValidateFeatureInput) ValidateFeatureReport {
	// Copy the attributes so default_override injection inside validateForCreate
	// (via validateRequired) never mutates the caller's map -- this is a
	// side-effect-free preview.
	attrs := make(map[string]any, len(in.Attributes))
	for k, v := range in.Attributes {
		attrs[k] = v
	}

	if _, _, err := validateForCreate(loaded, in.Subtype, in.Geometry, attrs); err != nil {
		return ValidateFeatureReport{Valid: false, Error: err.Error()}
	}
	return ValidateFeatureReport{Valid: true}
}
