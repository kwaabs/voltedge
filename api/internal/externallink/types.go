// Package externallink records outbound links from a feature to
// external systems (CIS/billing, maintenance/work-order, document
// stores, ...). A feature can have arbitrarily many links -- this is the
// "many outbound links per feature" surface, which a per-class field
// could never model.
//
// Slice 1 handles plain hyperlinks: kind='hyperlink' with an http/https
// url the panel opens in a new tab. Slice 3 adds kind='api' links, which
// carry a catalogued external system (metadata.external_systems) + a
// resource path instead of a url, and whose live data is fetched
// server-side by fetch.go's proxy and rendered as a data card.
//
// Deliberately parallel to api/internal/relationship: the same
// polymorphic (feature_class_id, feature_id) pattern with no DB FK on
// feature_id, existence validated in Go against the right physical table
// before writing -- see docs/phase-32-external-links.md.
package externallink

import "time"

// createRequest is the POST / body. kind defaults to 'hyperlink'. For a
// hyperlink, url is required and must be http/https (see
// validateHyperlinkURL). For kind='api', system_id and resource_path
// carry the target instead, and url is ignored.
type createRequest struct {
	FeatureClass string `json:"feature_class"`
	FeatureID    string `json:"feature_id"`
	Kind         string `json:"kind"`
	Label        string `json:"label"`
	URL          string `json:"url"`
	SystemID     string `json:"system_id"`
	ResourcePath string `json:"resource_path"`
}

// updateRequest is the PATCH /{id} body. Every field is optional -- only
// the ones present are changed, so a caller can rename a link without
// resending its target. url applies to hyperlinks, resource_path to api
// links; the kind of a link is fixed at creation.
type updateRequest struct {
	Label        *string `json:"label"`
	URL          *string `json:"url"`
	ResourcePath *string `json:"resource_path"`
}

// linkResponse is the shape returned for one link, from create, list or
// update alike. The api-only fields are omitted for hyperlinks (and vice
// versa) so the panel can branch on kind without null-checking.
type linkResponse struct {
	ID           string    `json:"id"`
	Kind         string    `json:"kind"`
	Label        string    `json:"label"`
	URL          string    `json:"url,omitempty"`
	SystemID     string    `json:"system_id,omitempty"`
	SystemCode   string    `json:"system_code,omitempty"`
	SystemName   string    `json:"system_name,omitempty"`
	ResourcePath string    `json:"resource_path,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

// apiError is the JSON body returned for any 4xx/5xx response.
type apiError struct {
	Error string `json:"error"`
}
