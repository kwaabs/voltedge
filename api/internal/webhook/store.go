package webhook

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/kwaabs/voltedge/api/internal/secretbox"
	"github.com/uptrace/bun"
)

// receivePathPrefix is where the public receiver is mounted (main.go). Used only
// to build a human-facing receive_url in the endpoint response.
const receivePathPrefix = "/webhooks/"

// store bundles the db with the optional at-rest cipher, exactly like
// api/internal/externalsystem. A nil cipher means the encryption key is unset:
// endpoints still work for bearer auth, but an HMAC signing secret can't be
// stored (verifying a signature needs the plaintext secret back).
type store struct {
	db     *bun.DB
	cipher *secretbox.Cipher
}

var (
	validDirections = map[string]bool{"push": true, "pull": true, "outbound": true}
	validTargets    = map[string]bool{"feature": true, "reading": true, "work": true, "fault": true, "operation": true, "outage": true, "incident": true, "workorder": true, "protection": true}
	validAuthModes  = map[string]bool{"hmac": true, "bearer": true, "both": true}
)

func (s *store) sealSecret(plaintext string) ([]byte, error) {
	if s.cipher == nil {
		return nil, invalid("cannot store a signing secret: the server has no encryption key configured (set %s)", secretbox.EnvKey)
	}
	sealed, err := s.cipher.Encrypt([]byte(plaintext))
	if err != nil {
		return nil, fmt.Errorf("webhook: seal signing secret: %w", err)
	}
	return sealed, nil
}

// unsealSecret returns an endpoint's stored secret in plaintext, or "" when none
// is stored. It is the inverse of sealSecret, used by the pull path to recover the
// outbound credential a pull endpoint keeps in signing_secret_enc. A nil cipher
// (encryption key unset, or the stateless agent registry) can't decrypt, so a
// present-but-unreadable secret is a clean 400 rather than a panic.
func (s *store) unsealSecret(ep *Endpoint) (string, error) {
	if len(ep.SigningSecretEnc) == 0 {
		return "", nil
	}
	if s.cipher == nil {
		return "", invalid("cannot read the stored credential: the server has no encryption key configured (set %s)", secretbox.EnvKey)
	}
	plain, err := s.cipher.Decrypt(ep.SigningSecretEnc)
	if err != nil {
		return "", fmt.Errorf("webhook: unseal credential: %w", err)
	}
	return string(plain), nil
}

// serviceUserExists guards the service_user_id FK with a clean 400 (a live,
// active user) rather than surfacing a raw 23503 or a later apply-time failure.
func (s *store) serviceUserExists(ctx context.Context, userID string) (bool, error) {
	return s.db.NewSelect().
		Table("iam.users").
		Where("id = ?", userID).
		Where("status = ?", "active").
		Exists(ctx)
}

// needsSecret reports whether an auth mode requires an HMAC signing secret.
func needsSecret(authMode string) bool { return authMode == "hmac" || authMode == "both" }

func createEndpoint(ctx context.Context, s *store, req createEndpointRequest) (*endpointResponse, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, invalid("name is required")
	}

	direction := defaulted(req.Direction, "push")
	if !validDirections[direction] {
		return nil, invalid("direction must be one of push, pull, outbound")
	}
	target := defaulted(req.Target, "feature")
	if !validTargets[target] {
		return nil, invalid("target must be one of feature, reading, work, fault")
	}
	authMode := defaulted(req.AuthMode, "hmac")
	if !validAuthModes[authMode] {
		return nil, invalid("auth_mode must be one of hmac, bearer, both")
	}

	serviceUserID := strings.TrimSpace(req.ServiceUserID)
	if serviceUserID == "" {
		return nil, invalid("service_user_id is required -- every ingested write acts as this user")
	}
	ok, err := s.serviceUserExists(ctx, serviceUserID)
	if err != nil {
		return nil, fmt.Errorf("webhook: create: check service user: %w", err)
	}
	if !ok {
		return nil, invalid("service_user_id %q is not an active user", serviceUserID)
	}

	ep := &Endpoint{
		Name:          name,
		Active:        boolOr(req.Active, true),
		Direction:     direction,
		Target:        target,
		AuthMode:      authMode,
		ServiceUserID: serviceUserID,
		Mapping:       nilIfEmptyJSON(req.Mapping),
		Config:        orEmptyObject(req.Config),
	}
	if len(ep.Mapping) > 0 {
		if err := validateMappingSpec(ep.Mapping); err != nil {
			return nil, err
		}
	}

	secret := strings.TrimSpace(req.SigningSecret)
	switch {
	case direction == "push" && needsSecret(authMode):
		// Inbound HMAC: the receiver needs the plaintext back to verify a signature.
		if secret == "" {
			return nil, invalid("signing_secret is required for auth_mode %s", authMode)
		}
		sealed, err := s.sealSecret(secret)
		if err != nil {
			return nil, err
		}
		ep.SigningSecretEnc = sealed
	case direction == "outbound":
		// An outbound subscription signs every delivery with this secret (HMAC),
		// so the subscriber can verify it came from us; without it the delivery
		// worker dead-letters, so require it up front.
		if secret == "" {
			return nil, invalid("signing_secret is required for an outbound subscription (it signs each delivery)")
		}
		sealed, err := s.sealSecret(secret)
		if err != nil {
			return nil, err
		}
		ep.SigningSecretEnc = sealed
	case secret != "":
		// A pull endpoint repurposes the sealed secret as its OUTBOUND credential
		// (there is no inbound sender to authenticate, so auth_mode is irrelevant);
		// a push endpoint on bearer auth has no use for a signing secret.
		if direction != "pull" {
			return nil, invalid("signing_secret is only used by auth_mode hmac or both, or as the credential on a pull endpoint")
		}
		sealed, err := s.sealSecret(secret)
		if err != nil {
			return nil, err
		}
		ep.SigningSecretEnc = sealed
	}

	if actor := actorID(ctx); actor != "" {
		ep.CreatedBy = &actor
	}
	if _, err := s.db.NewInsert().Model(ep).Returning("*").Exec(ctx); err != nil {
		return nil, fmt.Errorf("webhook: create endpoint: %w", err)
	}
	return formatEndpoint(ep), nil
}

func listEndpoints(ctx context.Context, s *store) ([]endpointResponse, error) {
	var rows []Endpoint
	if err := s.db.NewSelect().Model(&rows).Order("created_at DESC").Scan(ctx); err != nil {
		return nil, fmt.Errorf("webhook: list endpoints: %w", err)
	}
	out := make([]endpointResponse, 0, len(rows))
	for i := range rows {
		out = append(out, *formatEndpoint(&rows[i]))
	}
	return out, nil
}

func getEndpoint(ctx context.Context, s *store, id string) (*Endpoint, error) {
	ep := new(Endpoint)
	if err := s.db.NewSelect().Model(ep).Where("id = ?", id).Scan(ctx); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, notFound("webhook endpoint %q not found", id)
		}
		return nil, fmt.Errorf("webhook: load endpoint: %w", err)
	}
	return ep, nil
}

func updateEndpoint(ctx context.Context, s *store, id string, req updateEndpointRequest) (*endpointResponse, error) {
	ep, err := getEndpoint(ctx, s, id)
	if err != nil {
		return nil, err
	}

	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" {
			return nil, invalid("name cannot be empty")
		}
		ep.Name = name
	}
	if req.Active != nil {
		ep.Active = *req.Active
	}
	if req.Direction != nil {
		d := strings.TrimSpace(*req.Direction)
		if !validDirections[d] {
			return nil, invalid("direction must be one of push, pull, outbound")
		}
		ep.Direction = d
	}
	if req.Target != nil {
		t := strings.TrimSpace(*req.Target)
		if !validTargets[t] {
			return nil, invalid("target must be one of feature, reading, work, fault, operation, outage, incident, workorder")
		}
		ep.Target = t
	}
	if req.AuthMode != nil {
		m := strings.TrimSpace(*req.AuthMode)
		if !validAuthModes[m] {
			return nil, invalid("auth_mode must be one of hmac, bearer, both")
		}
		ep.AuthMode = m
	}
	if req.ServiceUserID != nil {
		u := strings.TrimSpace(*req.ServiceUserID)
		ok, err := s.serviceUserExists(ctx, u)
		if err != nil {
			return nil, fmt.Errorf("webhook: update: check service user: %w", err)
		}
		if !ok {
			return nil, invalid("service_user_id %q is not an active user", u)
		}
		ep.ServiceUserID = u
	}
	if req.Mapping != nil {
		ep.Mapping = nilIfEmptyJSON(req.Mapping)
		if len(ep.Mapping) > 0 {
			if err := validateMappingSpec(ep.Mapping); err != nil {
				return nil, err
			}
		}
	}
	if req.Config != nil {
		ep.Config = orEmptyObject(req.Config)
	}

	// Signing secret is three-state: absent = keep, present+empty = clear,
	// present+non-empty = re-seal.
	if req.SigningSecret != nil {
		secret := strings.TrimSpace(*req.SigningSecret)
		if secret == "" {
			ep.SigningSecretEnc = nil
		} else {
			sealed, err := s.sealSecret(secret)
			if err != nil {
				return nil, err
			}
			ep.SigningSecretEnc = sealed
		}
	}
	// An HMAC-capable PUSH endpoint must end up with a secret (kept or supplied),
	// else the receiver has nothing to verify a signature against. A pull endpoint
	// never signs an inbound request, so its secret (the outbound credential) stays
	// optional regardless of auth_mode.
	if ep.Direction == "push" && needsSecret(ep.AuthMode) && len(ep.SigningSecretEnc) == 0 {
		return nil, invalid("signing_secret is required for auth_mode %s", ep.AuthMode)
	}

	ep.UpdatedAt = time.Now()
	if _, err := s.db.NewUpdate().Model(ep).
		Column("name", "active", "direction", "target", "auth_mode", "signing_secret_enc", "service_user_id", "mapping", "config", "updated_at").
		WherePK().
		Exec(ctx); err != nil {
		return nil, fmt.Errorf("webhook: update endpoint: %w", err)
	}
	return formatEndpoint(ep), nil
}

func deleteEndpoint(ctx context.Context, s *store, id string) error {
	res, err := s.db.NewDelete().Model((*Endpoint)(nil)).Where("id = ?", id).Exec(ctx)
	if err != nil {
		return fmt.Errorf("webhook: delete endpoint: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return notFound("webhook endpoint %q not found", id)
	}
	return nil
}

func formatEndpoint(ep *Endpoint) *endpointResponse {
	resp := &endpointResponse{
		ID:               ep.ID,
		Name:             ep.Name,
		Active:           ep.Active,
		Direction:        ep.Direction,
		Target:           ep.Target,
		AuthMode:         ep.AuthMode,
		HasSigningSecret: len(ep.SigningSecretEnc) > 0,
		ServiceUserID:    ep.ServiceUserID,
		Mapping:          ep.Mapping,
		Config:           orEmptyObject(ep.Config),
		CreatedAt:        ep.CreatedAt,
		UpdatedAt:        ep.UpdatedAt,
	}
	if ep.Direction == "push" {
		resp.ReceiveURL = receivePathPrefix + ep.ID
	}
	return resp
}
