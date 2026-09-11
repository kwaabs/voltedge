package externalsystem

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/kwaabs/voltedge/api/internal/secretbox"
	"github.com/uptrace/bun"
)

// ExternalSystem mirrors metadata.external_systems (migration 0058).
// alias pinned for the same reason the other engines pin theirs.
type ExternalSystem struct {
	bun.BaseModel `bun:"table:metadata.external_systems,alias:external_systems"`

	ID             string    `bun:"id,pk,nullzero"`
	Code           string    `bun:"code"`
	Name           string    `bun:"name"`
	BaseURL        string    `bun:"base_url"`
	AuthType       string    `bun:"auth_type"`
	AuthHeader     *string   `bun:"auth_header"`
	AuthUsername   *string   `bun:"auth_username"`
	CredentialEnc  []byte    `bun:"credential_enc"`
	ResponseFormat string    `bun:"response_format"`
	CreatedAt      time.Time `bun:"created_at,nullzero"`
	UpdatedAt      time.Time `bun:"updated_at,nullzero"`
}

// store bundles the db with the optional at-rest cipher so every handler
// has both. A nil cipher means EXTERNAL_SYSTEMS_ENCRYPTION_KEY is unset:
// systems still work, but storing a credential is refused with a clear
// error rather than silently dropped.
type store struct {
	db     *bun.DB
	cipher *secretbox.Cipher
}

var (
	validAuthTypes       = map[string]bool{"none": true, "api_key": true, "basic": true, "bearer": true}
	validResponseFormats = map[string]bool{"json": true, "xml": true}
)

// validateBaseURL keeps a system's base_url to an absolute http/https
// URL with a host -- the same guard the hyperlink path uses, and the
// first line of defence for the Slice 3 fetch proxy (SSRF hardening
// proper lands there, but a garbage or non-http base is rejected now).
func validateBaseURL(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", invalid("base_url is required")
	}
	u, err := url.Parse(trimmed)
	if err != nil {
		return "", invalid("base_url is not a valid URL: %v", err)
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", invalid("base_url must start with http:// or https://")
	}
	if u.Host == "" {
		return "", invalid("base_url must include a host")
	}
	return trimmed, nil
}

// validateAuthShape checks that the non-secret auth parameters an
// auth_type needs are present on the (already-merged) row. It does NOT
// check the credential itself -- that's handled separately because
// update can leave an existing sealed secret in place.
func validateAuthShape(sys *ExternalSystem) error {
	if !validAuthTypes[sys.AuthType] {
		return invalid("auth_type must be one of none, api_key, basic, bearer")
	}
	if !validResponseFormats[sys.ResponseFormat] {
		return invalid("response_format must be json or xml")
	}
	switch sys.AuthType {
	case "api_key":
		if sys.AuthHeader == nil || strings.TrimSpace(*sys.AuthHeader) == "" {
			return invalid("auth_header is required for auth_type api_key")
		}
	case "basic":
		if sys.AuthUsername == nil || strings.TrimSpace(*sys.AuthUsername) == "" {
			return invalid("auth_username is required for auth_type basic")
		}
	}
	return nil
}

// sealCredential turns a plaintext secret into the stored ciphertext,
// refusing when no encryption key is configured -- we never fall back to
// storing a secret in the clear.
func (s *store) sealCredential(plaintext string) ([]byte, error) {
	if s.cipher == nil {
		return nil, invalid("cannot store a credential: the server has no encryption key configured (set %s)", secretbox.EnvKey)
	}
	sealed, err := s.cipher.Encrypt([]byte(plaintext))
	if err != nil {
		return nil, fmt.Errorf("externalsystem: seal credential: %w", err)
	}
	return sealed, nil
}

func ptrIfSet(v string) *string {
	trimmed := strings.TrimSpace(v)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func createSystem(ctx context.Context, s *store, req createRequest) (*systemResponse, error) {
	code := strings.TrimSpace(req.Code)
	if code == "" {
		return nil, invalid("code is required")
	}
	if strings.TrimSpace(req.Name) == "" {
		return nil, invalid("name is required")
	}
	baseURL, err := validateBaseURL(req.BaseURL)
	if err != nil {
		return nil, err
	}

	authType := strings.TrimSpace(req.AuthType)
	if authType == "" {
		authType = "none"
	}
	responseFormat := strings.TrimSpace(req.ResponseFormat)
	if responseFormat == "" {
		responseFormat = "json"
	}

	sys := &ExternalSystem{
		Code:           code,
		Name:           strings.TrimSpace(req.Name),
		BaseURL:        baseURL,
		AuthType:       authType,
		ResponseFormat: responseFormat,
	}
	// 'none' carries no auth params even if the caller sent stray ones.
	if authType != "none" {
		sys.AuthHeader = ptrIfSet(req.AuthHeader)
		sys.AuthUsername = ptrIfSet(req.AuthUsername)
	}
	if err := validateAuthShape(sys); err != nil {
		return nil, err
	}

	cred := strings.TrimSpace(req.Credential)
	switch {
	case authType == "none":
		// no secret regardless of what was sent
	case cred == "":
		return nil, invalid("credential is required for auth_type %s", authType)
	default:
		sealed, err := s.sealCredential(cred)
		if err != nil {
			return nil, err
		}
		sys.CredentialEnc = sealed
	}

	// Pre-check the unique code, same pattern as api/internal/admin's
	// creates -- a clean 400 rather than surfacing a raw 23505.
	exists, err := s.db.NewSelect().Model((*ExternalSystem)(nil)).Where("code = ?", code).Exists(ctx)
	if err != nil {
		return nil, fmt.Errorf("externalsystem: create: check code: %w", err)
	}
	if exists {
		return nil, invalid("an external system with code %q already exists", code)
	}

	if _, err := s.db.NewInsert().Model(sys).Returning("*").Exec(ctx); err != nil {
		return nil, fmt.Errorf("externalsystem: create: %w", err)
	}
	return formatSystem(sys), nil
}

func listSystems(ctx context.Context, s *store) ([]systemResponse, error) {
	var rows []ExternalSystem
	if err := s.db.NewSelect().Model(&rows).Order("code").Scan(ctx); err != nil {
		return nil, fmt.Errorf("externalsystem: list: %w", err)
	}
	out := make([]systemResponse, 0, len(rows))
	for i := range rows {
		out = append(out, *formatSystem(&rows[i]))
	}
	return out, nil
}

func updateSystem(ctx context.Context, s *store, code string, req updateRequest) (*systemResponse, error) {
	var sys ExternalSystem
	if err := s.db.NewSelect().Model(&sys).Where("code = ?", code).Scan(ctx); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, notFound("external system %q not found", code)
		}
		return nil, fmt.Errorf("externalsystem: update: load: %w", err)
	}

	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" {
			return nil, invalid("name cannot be empty")
		}
		sys.Name = name
	}
	if req.BaseURL != nil {
		baseURL, err := validateBaseURL(*req.BaseURL)
		if err != nil {
			return nil, err
		}
		sys.BaseURL = baseURL
	}
	if req.AuthType != nil {
		sys.AuthType = strings.TrimSpace(*req.AuthType)
	}
	if req.AuthHeader != nil {
		sys.AuthHeader = ptrIfSet(*req.AuthHeader)
	}
	if req.AuthUsername != nil {
		sys.AuthUsername = ptrIfSet(*req.AuthUsername)
	}
	if req.ResponseFormat != nil {
		sys.ResponseFormat = strings.TrimSpace(*req.ResponseFormat)
	}
	// Switching to 'none' drops any auth params + stored secret.
	if sys.AuthType == "none" {
		sys.AuthHeader = nil
		sys.AuthUsername = nil
		sys.CredentialEnc = nil
	}
	if err := validateAuthShape(&sys); err != nil {
		return nil, err
	}

	// Credential is three-state (see updateRequest doc): absent = keep,
	// present+empty = clear, present+non-empty = re-seal.
	if req.Credential != nil && sys.AuthType != "none" {
		cred := strings.TrimSpace(*req.Credential)
		if cred == "" {
			sys.CredentialEnc = nil
		} else {
			sealed, err := s.sealCredential(cred)
			if err != nil {
				return nil, err
			}
			sys.CredentialEnc = sealed
		}
	}
	// A non-'none' system must end up with a secret set (either kept from
	// before or supplied now) -- otherwise the Slice 3 proxy has nothing
	// to authenticate with.
	if sys.AuthType != "none" && len(sys.CredentialEnc) == 0 {
		return nil, invalid("credential is required for auth_type %s", sys.AuthType)
	}

	sys.UpdatedAt = time.Now()
	if _, err := s.db.NewUpdate().Model(&sys).
		Column("name", "base_url", "auth_type", "auth_header", "auth_username", "credential_enc", "response_format", "updated_at").
		WherePK().
		Exec(ctx); err != nil {
		return nil, fmt.Errorf("externalsystem: update: %w", err)
	}
	return formatSystem(&sys), nil
}

func deleteSystem(ctx context.Context, s *store, code string) error {
	res, err := s.db.NewDelete().Model((*ExternalSystem)(nil)).Where("code = ?", code).Exec(ctx)
	if err != nil {
		return fmt.Errorf("externalsystem: delete: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("externalsystem: delete: %w", err)
	}
	if n == 0 {
		return notFound("external system %q not found", code)
	}
	return nil
}

func formatSystem(sys *ExternalSystem) *systemResponse {
	resp := &systemResponse{
		ID:             sys.ID,
		Code:           sys.Code,
		Name:           sys.Name,
		BaseURL:        sys.BaseURL,
		AuthType:       sys.AuthType,
		HasCredential:  len(sys.CredentialEnc) > 0,
		ResponseFormat: sys.ResponseFormat,
		CreatedAt:      sys.CreatedAt,
		UpdatedAt:      sys.UpdatedAt,
	}
	if sys.AuthHeader != nil {
		resp.AuthHeader = *sys.AuthHeader
	}
	if sys.AuthUsername != nil {
		resp.AuthUsername = *sys.AuthUsername
	}
	return resp
}
