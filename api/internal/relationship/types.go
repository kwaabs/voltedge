// Package relationship is the explicit-confirm structural/spatial
// relationship engine for Phase 2: the only path that turns a candidate
// structural relationship (e.g. a pole supporting a conductor span) into
// a real, recorded fact. An establish request only ever commits when a
// matching metadata.relationship_classes row exists for the origin/
// destination feature classes involved, and only when that class's
// cardinality isn't already exhausted -- see docs/phase-2-relationships.md.
//
// Deliberately parallel to api/internal/connectivity, not shared code
// with it: relationship_classes has no terminals and no is_valid flag
// (legality is just "does a row exist for this pairing"), but it does
// have a cardinality rule connectivity has no equivalent of, so the two
// packages' matching logic isn't actually the same shape underneath the
// similar-looking surface.
package relationship

import "time"

// featureRef identifies one specific feature row.
type featureRef struct {
	FeatureClass string `json:"feature_class"`
	FeatureID    string `json:"feature_id"`
}

// establishRequest is the POST /relationships body. Kind is optional --
// only needed to disambiguate when more than one relationship_classes
// row exists for the same origin/destination feature-class pairing
// (different kind per pairing is legal; see migrations/0010).
type establishRequest struct {
	Origin      featureRef `json:"origin"`
	Destination featureRef `json:"destination"`
	Kind        *string    `json:"kind,omitempty"`
}

// relationshipResponse is the shape returned for one established
// relationship, whether from establish or list.
type relationshipResponse struct {
	ID                    string     `json:"id"`
	RelationshipClassID   string     `json:"relationship_class_id"`
	RelationshipClassName string     `json:"relationship_class_name"`
	Kind                  string     `json:"kind"`
	Cardinality           string     `json:"cardinality"`
	Origin                featureRef `json:"origin"`
	Destination           featureRef `json:"destination"`
	EstablishedAt         time.Time  `json:"established_at"`
	// PlausibilityWarning is set only on the response to a fresh
	// establish, and only when checkPlausibility (plausibility.go)
	// found both features' real geometries further apart than
	// plausibilityThresholdMeters -- never re-checked on list, so an
	// old relationship's warning doesn't recompute (or silently
	// disappear) every time it's read back.
	PlausibilityWarning *string `json:"plausibility_warning,omitempty"`
}

// apiError is the JSON body returned for any 4xx/5xx response.
type apiError struct {
	Error string `json:"error"`
}
