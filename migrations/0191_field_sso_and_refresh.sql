-- 0191_field_sso_and_refresh.sql
-- Purpose: two small tables that let a field device authenticate with SSO and hold
-- a short-lived access token refreshed from a long-lived refresh token.
--
-- 1. iam.field_sso_handoff -- the one-time bridge from the server-mediated SSO flow
--    (cmd/server /auth/oidc/.../callback, where the Azure-registered redirect lands)
--    to the field app. The web callback can't hand a bearer straight to the app, so
--    on a successful field SSO login it stores a single-use, short-TTL code keyed to
--    the matched user; the device redeems it at POST /auth/field-sso/exchange on
--    cmd/fieldsync (same DB) for a real field session. We store a reference to the
--    USER, never a token -- the token is minted only at redemption. Code is hashed at
--    rest (sha256), like api_tokens.
--
-- 2. iam.field_refresh_tokens -- the long-lived credential a field device keeps in
--    its keystore. The access token it presents on every call is a short-TTL
--    iam.api_tokens row (24h); when that lapses the device swaps this refresh token
--    for a fresh access token (POST /auth/field-refresh) without re-authenticating.
--    Non-rotating in v1 (stable until expiry/revocation): rotating on every use breaks
--    on the flaky connectivity a field device lives with. Hashed at rest; revocation
--    (admin, or device sign-out) and expiry are the controls. Backward compatible:
--    devices already holding a non-expiring api_token keep working until revoked.

CREATE TABLE IF NOT EXISTS iam.field_sso_handoff (
    code_sha256 bytea PRIMARY KEY,
    user_id     uuid NOT NULL REFERENCES iam.users (id) ON DELETE CASCADE,
    device_name text,
    created_at  timestamptz NOT NULL DEFAULT now(),
    expires_at  timestamptz NOT NULL,
    used_at     timestamptz
);
CREATE INDEX IF NOT EXISTS field_sso_handoff_expires_idx ON iam.field_sso_handoff (expires_at);

CREATE TABLE IF NOT EXISTS iam.field_refresh_tokens (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id      uuid NOT NULL REFERENCES iam.users (id) ON DELETE CASCADE,
    token_prefix text NOT NULL,
    token_sha256 bytea NOT NULL UNIQUE,
    device_name  text,
    created_at   timestamptz NOT NULL DEFAULT now(),
    last_used_at timestamptz,
    expires_at   timestamptz NOT NULL,
    revoked_at   timestamptz
);
CREATE INDEX IF NOT EXISTS field_refresh_tokens_user_idx ON iam.field_refresh_tokens (user_id);

COMMENT ON TABLE iam.field_sso_handoff IS 'One-time, single-use codes bridging the server-mediated field SSO callback to the device, which redeems the code for a field session. Stores a user reference, not a token.';
COMMENT ON TABLE iam.field_refresh_tokens IS 'Long-lived field-device refresh tokens: the device swaps one for a fresh short-lived api_tokens access token. Non-rotating in v1; governed by expiry + revocation.';
