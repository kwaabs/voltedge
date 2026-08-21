package auth

// API tokens (Phase 18, docs/phase-18-mcp.md) are opaque, revocable,
// per-user bearer credentials for non-browser clients -- specifically the
// MCP data-plane server, whose agent presents one in an
// `Authorization: Bearer <token>` header instead of a cookie. Everything
// behind the credential is identical to a cookie session: a token resolves
// to its owning user and that user's effective RBAC permission set, so a
// tool call is authorized by the exact same session -> user -> roles ->
// permissions chain (session.go / rbac.go). A token can never do more than
// the user it belongs to.
//
// This mirrors the session model deliberately (token.go / session.go): 256
// bits of crypto/rand entropy, stored only as its SHA-256, compared by
// hashing the presented token. The difference is lifetime -- an API token
// is a long-lived credential minted out-of-band (api/cmd/authadmin, the
// same "nothing shipped in a seed" path that bootstraps the first admin),
// governed by an optional hard expiry and by revocation rather than a
// sliding idle window.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/uptrace/bun"
)

// apiTokenMarker prefixes every raw token. It makes a VoltEdge token
// recognizable at a glance (and to secret scanners), and its leading
// characters are what token_prefix stores for display. The marker is part
// of the token: it is included in the bytes that get hashed and must be
// presented verbatim.
const apiTokenMarker = "ve_mcp_"

// apiTokenPrefixLen is how many leading characters of the raw token are
// kept in the clear (token_prefix) so an operator can identify a token in a
// listing without it revealing anything usable.
const apiTokenPrefixLen = 12

// APIToken mirrors iam.api_tokens (migration 0037). RawToken is never a
// column -- it is returned once by MintAPIToken and never persisted.
type APIToken struct {
	bun.BaseModel `bun:"table:iam.api_tokens"`

	ID          string     `bun:"id,pk,nullzero"`
	UserID      string     `bun:"user_id"`
	Name        string     `bun:"name"`
	TokenPrefix string     `bun:"token_prefix"`
	TokenSHA256 []byte     `bun:"token_sha256"`
	CreatedAt   time.Time  `bun:"created_at,nullzero"`
	LastUsedAt  *time.Time `bun:"last_used_at"`
	ExpiresAt   *time.Time `bun:"expires_at"`
	RevokedAt   *time.Time `bun:"revoked_at"`
	CreatedBy   *string    `bun:"created_by"`
}

// MintAPIToken creates a new token for the account with the given email and
// returns the raw bearer token -- shown to the operator exactly once, never
// stored. name is a human label (e.g. "claude-desktop"). ttl <= 0 means the
// token never expires (governed only by revocation); a positive ttl sets a
// hard expiry. createdBy is the acting user id, or "" for an out-of-band
// mint with no actor (the authadmin CLI path).
func MintAPIToken(ctx context.Context, db *bun.DB, email, name string, ttl time.Duration, createdBy string) (rawToken string, tok *APIToken, err error) {
	email = strings.ToLower(strings.TrimSpace(email))
	name = strings.TrimSpace(name)
	if email == "" || name == "" {
		return "", nil, errors.New("email and token name are required")
	}

	var user User
	err = db.NewSelect().Model(&user).Where("email = ?", email).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil, fmt.Errorf("no user with email %q", email)
	}
	if err != nil {
		return "", nil, err
	}
	if user.Status != "active" {
		return "", nil, fmt.Errorf("user %q is not active", email)
	}

	random, err := newRandomToken(sessionTokenBytes)
	if err != nil {
		return "", nil, err
	}
	rawToken = apiTokenMarker + random

	tok = &APIToken{
		UserID:      user.ID,
		Name:        name,
		TokenPrefix: rawToken[:apiTokenPrefixLen],
		TokenSHA256: hashToken(rawToken),
	}
	if ttl > 0 {
		exp := time.Now().Add(ttl)
		tok.ExpiresAt = &exp
	}
	if createdBy != "" {
		tok.CreatedBy = &createdBy
	}
	if _, err := db.NewInsert().Model(tok).Returning("id, created_at").Exec(ctx); err != nil {
		return "", nil, err
	}
	return rawToken, tok, nil
}

// ResolveAPIToken validates a presented raw bearer token and, if it is
// live, returns the authenticated user snapshot -- the same *SessionUser a
// cookie session yields, so downstream RBAC (SessionUser.Has) and actor
// stamping (ActorID) work identically. "Live" means: the token exists, is
// not revoked, is unexpired, and its user is still active. Any invalidity
// returns (nil, nil) -- an unauthenticated caller, not an error -- so the
// transport can answer 401 rather than 500. The returned SessionUser
// carries no SessionID/CSRFToken: those are cookie-session concerns a
// bearer client has no use for (a token isn't auto-sent by a browser, so
// there is no CSRF surface to defend).
func ResolveAPIToken(ctx context.Context, db bun.IDB, rawToken string) (*SessionUser, error) {
	rawToken = strings.TrimSpace(rawToken)
	if rawToken == "" {
		return nil, nil
	}

	var tok APIToken
	err := db.NewSelect().Model(&tok).
		Where("token_sha256 = ?", hashToken(rawToken)).
		Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	now := time.Now()
	if tok.RevokedAt != nil || (tok.ExpiresAt != nil && now.After(*tok.ExpiresAt)) {
		return nil, nil
	}

	var user User
	err = db.NewSelect().Model(&user).
		Where("id = ?", tok.UserID).
		Where("status = ?", "active").
		Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		// user was disabled or deleted out from under a live token
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	// The effective permission set -- exactly as the session path loads it
	// (session.go). A read failure for an otherwise-valid token is a real
	// server fault, so it propagates and the request fails closed.
	perms, permErr := loadPermissionsFor(ctx, db, tok.UserID)
	if permErr != nil {
		return nil, permErr
	}

	// Record use, best effort -- a failed stamp must not fail the request.
	_, _ = db.NewUpdate().Model((*APIToken)(nil)).
		Set("last_used_at = ?", now).
		Where("id = ?", tok.ID).
		Exec(ctx)

	return &SessionUser{
		ID:          user.ID,
		Email:       user.Email,
		DisplayName: user.DisplayName,
		BreakGlass:  user.BreakGlass,
		Permissions: perms,
	}, nil
}

// RevokeAPIToken revokes one of a user's tokens by its id-prefix or name,
// scoped to the owning email so a typo can't kill another user's token.
// Idempotent: revoking an already-revoked token is a no-op. Returns the
// number of tokens revoked (0 = no live match).
func RevokeAPIToken(ctx context.Context, db *bun.DB, email, tokenID string) (int, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	var user User
	err := db.NewSelect().Model(&user).Where("email = ?", email).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("no user with email %q", email)
	}
	if err != nil {
		return 0, err
	}
	res, err := db.NewUpdate().Model((*APIToken)(nil)).
		Set("revoked_at = ?", time.Now()).
		Where("user_id = ?", user.ID).
		Where("id = ?", tokenID).
		Where("revoked_at IS NULL").
		Exec(ctx)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
}

// APITokenSummary is the read shape for CLI listing -- never any token
// material beyond the display prefix.
type APITokenSummary struct {
	ID          string
	Name        string
	TokenPrefix string
	CreatedAt   time.Time
	LastUsedAt  *time.Time
	ExpiresAt   *time.Time
	RevokedAt   *time.Time
}

// ListAPITokens returns every token for the account with the given email,
// newest first, for the CLI `list-tokens` command.
func ListAPITokens(ctx context.Context, db *bun.DB, email string) ([]APITokenSummary, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	var user User
	err := db.NewSelect().Model(&user).Where("email = ?", email).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("no user with email %q", email)
	}
	if err != nil {
		return nil, err
	}
	var toks []APIToken
	if err := db.NewSelect().Model(&toks).
		Where("user_id = ?", user.ID).
		OrderExpr("created_at DESC").
		Scan(ctx); err != nil {
		return nil, err
	}
	out := make([]APITokenSummary, len(toks))
	for i, t := range toks {
		out[i] = APITokenSummary{
			ID: t.ID, Name: t.Name, TokenPrefix: t.TokenPrefix,
			CreatedAt: t.CreatedAt, LastUsedAt: t.LastUsedAt,
			ExpiresAt: t.ExpiresAt, RevokedAt: t.RevokedAt,
		}
	}
	return out, nil
}
