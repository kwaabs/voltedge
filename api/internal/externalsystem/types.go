// Package externalsystem is the admin CRUD for metadata.external_systems
// (Phase 32 Slice 2): the catalog of external systems that a feature's
// kind='api' external links will fetch live data from in Slice 3. It's
// config the same way domains and material specs are, governed by the
// metadata:read/metadata:write gate.
//
// Credentials are encrypted at rest via api/internal/secretbox and never
// returned to the browser -- a response says only whether a credential
// is set (has_credential), not what it is.
package externalsystem

import "time"

// systemResponse is the shape returned for one external system. It
// deliberately has no credential field: the sealed secret is
// write-only, surfaced only as has_credential.
type systemResponse struct {
	ID             string    `json:"id"`
	Code           string    `json:"code"`
	Name           string    `json:"name"`
	BaseURL        string    `json:"base_url"`
	AuthType       string    `json:"auth_type"`
	AuthHeader     string    `json:"auth_header,omitempty"`
	AuthUsername   string    `json:"auth_username,omitempty"`
	HasCredential  bool      `json:"has_credential"`
	ResponseFormat string    `json:"response_format"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// createRequest is the POST / body. Credential is the plaintext secret
// (api key / basic password / bearer token); it's sealed server-side and
// this is the only direction it ever travels.
type createRequest struct {
	Code           string `json:"code"`
	Name           string `json:"name"`
	BaseURL        string `json:"base_url"`
	AuthType       string `json:"auth_type"`
	AuthHeader     string `json:"auth_header"`
	AuthUsername   string `json:"auth_username"`
	Credential     string `json:"credential"`
	ResponseFormat string `json:"response_format"`
}

// updateRequest is the PATCH /{code} body. Every field is optional --
// only the ones present change. Credential is three-state: absent = leave
// the stored secret untouched; present and non-empty = re-seal and
// replace; present and empty = clear the stored secret.
type updateRequest struct {
	Name           *string `json:"name"`
	BaseURL        *string `json:"base_url"`
	AuthType       *string `json:"auth_type"`
	AuthHeader     *string `json:"auth_header"`
	AuthUsername   *string `json:"auth_username"`
	Credential     *string `json:"credential"`
	ResponseFormat *string `json:"response_format"`
}

// apiError is the JSON body returned for any 4xx/5xx response.
type apiError struct {
	Error string `json:"error"`
}
