package auth

// Field-app session model (migration 0191). A field device no longer holds one
// non-expiring bearer; it holds a SHORT-LIVED access token (an ordinary
// iam.api_tokens row, 24h) plus a LONG-LIVED refresh token it swaps for a fresh
// access token when the access token lapses -- without re-authenticating. This
// backs both sign-in paths: password (FieldLogin) and SSO (FieldSSOExchange), which
// funnel through issueFieldSession so the two stay identical.
//
// Why these lifetimes: the access token is what every call presents, so a short TTL
// bounds the damage of a leaked bearer. The refresh token lives long (60d) and is
// NON-ROTATING in v1 -- a field device's connectivity is flaky, and rotating the
// refresh token on every use would strand a device whose rotation reply was lost,
// forcing a needless re-login. Offline is unaffected: no calls go out offline, so the
// access token only matters online, where the device refreshes before/at expiry.
//
// SSO can't hand a bearer straight to the app (the Azure-registered redirect lands on
// cmd/server's browser callback). So a field SSO login mints a single-use handoff code
// (iam.field_sso_handoff) in the shared DB; the device redeems it on cmd/fieldsync for
// the session. The handoff stores a USER reference, never a token.

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/uptrace/bun"
)

const (
	// fieldAccessTTL caps the field access token (the presented bearer). Short so a
	// leaked token self-heals; the device refreshes transparently before it lapses.
	fieldAccessTTL = 24 * time.Hour
	// fieldSSOHandoffTTL is the tight window the device has to redeem an SSO handoff
	// code after the browser returns it -- it should be seconds in practice.
	fieldSSOHandoffTTL = 2 * time.Minute
	// defaultFieldRefreshTTLDays is how long a device can go between
	// re-authentications when FIELD_REFRESH_TTL_DAYS is unset. See fieldRefreshTTL.
	defaultFieldRefreshTTLDays = 21

	fieldRefreshMarker   = "ve_frt_" // raw refresh token prefix (field refresh token)
	fieldSSOHandoffMarker = "ve_fho_" // raw SSO handoff code prefix (field handoff)
)

// fieldRefreshToken mirrors iam.field_refresh_tokens. The raw token is returned once
// and never stored (only its sha256), exactly like api_tokens.
type fieldRefreshToken struct {
	bun.BaseModel `bun:"table:iam.field_refresh_tokens"`

	ID          string     `bun:"id,pk,nullzero"`
	UserID      string     `bun:"user_id"`
	TokenPrefix string     `bun:"token_prefix"`
	TokenSHA256 []byte     `bun:"token_sha256"`
	DeviceName  *string    `bun:"device_name"`
	CreatedAt   time.Time  `bun:"created_at,nullzero"`
	LastUsedAt  *time.Time `bun:"last_used_at"`
	ExpiresAt   time.Time  `bun:"expires_at"`
	RevokedAt   *time.Time `bun:"revoked_at"`
}

// fieldSSOHandoff mirrors iam.field_sso_handoff: a one-time, single-use code that
// bridges the server-mediated SSO callback to the device.
type fieldSSOHandoff struct {
	bun.BaseModel `bun:"table:iam.field_sso_handoff"`

	CodeSHA256 []byte     `bun:"code_sha256,pk"`
	UserID     string     `bun:"user_id"`
	DeviceName *string    `bun:"device_name"`
	CreatedAt  time.Time  `bun:"created_at,nullzero"`
	ExpiresAt  time.Time  `bun:"expires_at"`
	UsedAt     *time.Time `bun:"used_at"`
}

// issueFieldSession is the shared tail of every field sign-in: it enforces the
// field:login capability, mints a short-lived access token and a long-lived refresh
// token, and returns the device payload. Used by FieldLogin (password) and
// FieldSSOExchange (SSO) so both yield an identical session.
func issueFieldSession(ctx context.Context, db *bun.DB, userID, email, displayName, deviceName string) (*FieldLoginResult, error) {
	su, err := LoadSessionUser(ctx, db, userID)
	if err != nil {
		return nil, err
	}
	// Field-app access is an explicit capability (migration 0164): only a user granted
	// field:login (the field roles, admin, break-glass) may obtain a field session.
	if !su.BreakGlass && !su.Has(PermFieldLogin) {
		return nil, &FieldLoginError{
			Status:  http.StatusForbidden,
			Message: "this account does not have field-app access (field:login)",
		}
	}

	name := strings.TrimSpace(deviceName)
	if name == "" {
		name = "field-app"
	}
	access, _, err := MintAPIToken(ctx, db, email, name, fieldAccessTTL, userID)
	if err != nil {
		return nil, err
	}
	refresh, err := mintFieldRefreshToken(ctx, db, userID, name)
	if err != nil {
		return nil, err
	}
	return &FieldLoginResult{
		Token:           access,
		AccessExpiresAt: time.Now().Add(fieldAccessTTL),
		RefreshToken:    refresh,
		UserID:          userID,
		Email:           email,
		DisplayName:     displayName,
		Permissions:     permissionList(su),
	}, nil
}

// fieldRefreshTTL is how long a device can go between re-authentications, from
// FIELD_REFRESH_TTL_DAYS (positive integer days) else defaultFieldRefreshTTLDays.
// SSO orgs that rotate passwords (e.g. every 28 days) should set this <= that period
// so a device must re-SSO within one cycle, re-delegating to the IdP's current policy.
// A shorter TTL CAPS staleness; it does not re-validate against the IdP per refresh,
// so it is not instant revocation (disabling the VoltEdge user still cuts access at
// the next access-token refresh, ~fieldAccessTTL).
func fieldRefreshTTL() time.Duration {
	days := defaultFieldRefreshTTLDays
	if v := strings.TrimSpace(os.Getenv("FIELD_REFRESH_TTL_DAYS")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			days = n
		}
	}
	return time.Duration(days) * 24 * time.Hour
}

// mintFieldRefreshToken creates and stores a long-lived refresh token, returning the
// raw value once (hashed at rest).
func mintFieldRefreshToken(ctx context.Context, db *bun.DB, userID, deviceName string) (string, error) {
	random, err := newRandomToken(sessionTokenBytes)
	if err != nil {
		return "", err
	}
	raw := fieldRefreshMarker + random
	rt := &fieldRefreshToken{
		UserID:      userID,
		TokenPrefix: raw[:apiTokenPrefixLen],
		TokenSHA256: hashToken(raw),
		ExpiresAt:   time.Now().Add(fieldRefreshTTL()),
	}
	if deviceName != "" {
		rt.DeviceName = &deviceName
	}
	// Exclude the uuid pk + created_at so their DB defaults apply (a zero uuid pk would
	// otherwise fail 22P02 -- see the insert-zero-uuid-pk convention).
	if _, err := db.NewInsert().Model(rt).ExcludeColumn("id", "created_at").Exec(ctx); err != nil {
		return "", err
	}
	return raw, nil
}

// FieldRefresh swaps a valid refresh token for a FRESH access token, re-checking the
// account is active and still field-enabled. The refresh token itself is unchanged
// (non-rotating v1); only last_used_at is stamped. A revoked/expired/unknown token is
// a 401 the device treats as "re-authenticate".
func FieldRefresh(ctx context.Context, db *bun.DB, rawRefresh, deviceName string) (*FieldLoginResult, error) {
	rawRefresh = strings.TrimSpace(rawRefresh)
	if rawRefresh == "" {
		return nil, &FieldLoginError{Status: http.StatusUnauthorized, Message: "missing refresh token"}
	}
	var rt fieldRefreshToken
	err := db.NewSelect().Model(&rt).Where("token_sha256 = ?", hashToken(rawRefresh)).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, &FieldLoginError{Status: http.StatusUnauthorized, Message: "invalid refresh token"}
	}
	if err != nil {
		return nil, err
	}
	now := time.Now()
	if rt.RevokedAt != nil || now.After(rt.ExpiresAt) {
		return nil, &FieldLoginError{Status: http.StatusUnauthorized, Message: "refresh token expired or revoked"}
	}

	var user User
	err = db.NewSelect().Model(&user).Where("id = ?", rt.UserID).Where("status = ?", "active").Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, &FieldLoginError{Status: http.StatusUnauthorized, Message: "account is no longer active"}
	}
	if err != nil {
		return nil, err
	}
	su, err := LoadSessionUser(ctx, db, user.ID)
	if err != nil {
		return nil, err
	}
	if !su.BreakGlass && !su.Has(PermFieldLogin) {
		return nil, &FieldLoginError{Status: http.StatusForbidden, Message: "this account does not have field-app access (field:login)"}
	}

	name := "field-app"
	if rt.DeviceName != nil && *rt.DeviceName != "" {
		name = *rt.DeviceName
	}
	access, _, err := MintAPIToken(ctx, db, user.Email, name, fieldAccessTTL, user.ID)
	if err != nil {
		return nil, err
	}
	_, _ = db.NewUpdate().Model((*fieldRefreshToken)(nil)).
		Set("last_used_at = ?", now).
		Where("token_sha256 = ?", rt.TokenSHA256).
		Exec(ctx)

	return &FieldLoginResult{
		Token:           access,
		AccessExpiresAt: now.Add(fieldAccessTTL),
		RefreshToken:    rawRefresh, // unchanged -- non-rotating
		UserID:          user.ID,
		Email:           user.Email,
		DisplayName:     user.DisplayName,
		Permissions:     permissionList(su),
	}, nil
}

// RevokeFieldRefreshToken invalidates a refresh token (device sign-out). Idempotent;
// a missing/already-revoked token is a no-op. The matching access token still lapses
// on its own short TTL.
func RevokeFieldRefreshToken(ctx context.Context, db *bun.DB, rawRefresh string) error {
	rawRefresh = strings.TrimSpace(rawRefresh)
	if rawRefresh == "" {
		return nil
	}
	_, err := db.NewUpdate().Model((*fieldRefreshToken)(nil)).
		Set("revoked_at = ?", time.Now()).
		Where("token_sha256 = ?", hashToken(rawRefresh)).
		Where("revoked_at IS NULL").
		Exec(ctx)
	return err
}

// createFieldSSOHandoff stores a single-use code bound to userID and returns the raw
// code once. Called by the OIDC callback after a successful FIELD SSO login. Takes
// bun.IDB (not *bun.DB) so the auth service -- which holds the interface -- can call it.
func createFieldSSOHandoff(ctx context.Context, db bun.IDB, userID, deviceName string) (string, error) {
	random, err := newRandomToken(sessionTokenBytes)
	if err != nil {
		return "", err
	}
	raw := fieldSSOHandoffMarker + random
	ho := &fieldSSOHandoff{
		CodeSHA256: hashToken(raw),
		UserID:     userID,
		ExpiresAt:  time.Now().Add(fieldSSOHandoffTTL),
	}
	if deviceName != "" {
		ho.DeviceName = &deviceName
	}
	if _, err := db.NewInsert().Model(ho).ExcludeColumn("created_at").Exec(ctx); err != nil {
		return "", err
	}
	return raw, nil
}

// FieldSSOExchange redeems a one-time SSO handoff code for a field session. The code is
// consumed atomically (single-use, guarded against a double redeem), then the session
// is issued via the same path as a password login. deviceName from the request body is
// a fallback label if the handoff carried none.
func FieldSSOExchange(ctx context.Context, db *bun.DB, code, deviceName string) (*FieldLoginResult, error) {
	code = strings.TrimSpace(code)
	if code == "" {
		return nil, &FieldLoginError{Status: http.StatusBadRequest, Message: "missing code"}
	}
	hash := hashToken(code)
	var ho fieldSSOHandoff
	err := db.NewSelect().Model(&ho).Where("code_sha256 = ?", hash).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, &FieldLoginError{Status: http.StatusUnauthorized, Message: "invalid or expired code"}
	}
	if err != nil {
		return nil, err
	}
	now := time.Now()
	if ho.UsedAt != nil || now.After(ho.ExpiresAt) {
		return nil, &FieldLoginError{Status: http.StatusUnauthorized, Message: "invalid or expired code"}
	}
	// Consume atomically -- only the first redeem of an unused code wins.
	res, err := db.NewUpdate().Model((*fieldSSOHandoff)(nil)).
		Set("used_at = ?", now).
		Where("code_sha256 = ?", hash).
		Where("used_at IS NULL").
		Exec(ctx)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, &FieldLoginError{Status: http.StatusUnauthorized, Message: "code already used"}
	}

	var user User
	err = db.NewSelect().Model(&user).Where("id = ?", ho.UserID).Where("status = ?", "active").Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, &FieldLoginError{Status: http.StatusUnauthorized, Message: "account is no longer active"}
	}
	if err != nil {
		return nil, err
	}
	name := deviceName
	if ho.DeviceName != nil && *ho.DeviceName != "" {
		name = *ho.DeviceName
	}
	return issueFieldSession(ctx, db, user.ID, user.Email, user.DisplayName, name)
}
