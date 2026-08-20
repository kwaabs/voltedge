-- 0024_create_auth_schema.sql
-- Purpose: create the 'iam' schema and Phase 10 Slice A's identity,
-- session, settings, and audit tables -- the trust-boundary foundation
-- (see docs/phase-10-auth.md). Kept in its own schema, same
-- separate-schema-per-concern principle as ops/metadata/runtime/gis
-- (migrations 0001, 0003, 0015, 0016). This slice deliberately creates
-- only identity + sessions + the break-glass switch + the audit log;
-- the RBAC tables (roles/permissions/grants) land with Slice C so the
-- enforcement code and its data arrive together.
--
-- NOTE ON THE SCHEMA NAME: the supabase/postgres image ships a reserved
-- 'auth' schema (GoTrue's, owned by supabase_auth_admin, and it already
-- contains an auth.users table). We don't run GoTrue, but we cannot own
-- or safely reuse that schema, so all of VoltEdge's identity/access
-- state lives in 'iam' (identity & access management) instead. The Go
-- package is still named auth; only the physical schema is 'iam'.
--
-- No credentials are shipped here. The very first break-glass admin is
-- created out-of-band by api/cmd/authadmin against this schema, never as
-- a seed -- the design's "no shipped default credentials, ever" rule.

CREATE SCHEMA IF NOT EXISTS iam;
COMMENT ON SCHEMA iam IS 'Identity & access management: identities, sessions, the local-auth break-glass switch, and the security audit log (RBAC lands with Slice C). Enforced entirely in the Go API layer (the single trusted DB gatekeeper), not via Postgres RLS. Named iam rather than auth because the supabase/postgres image reserves the auth schema for GoTrue. See docs/phase-10-auth.md.';

-- citext gives case-insensitive email uniqueness without lower()-ing at
-- every call site; it ships with the supabase/postgres image.
CREATE EXTENSION IF NOT EXISTS citext;

-- iam.users -- the person. Deliberately holds NO password: credentials
-- live on iam.user_identities, so one person can carry a local password
-- and a linked SSO identity at once (or neither yet).
CREATE TABLE iam.users (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    email              citext NOT NULL UNIQUE,
    display_name       text NOT NULL,
    status             text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'disabled')),
    break_glass        boolean NOT NULL DEFAULT false,
    failed_login_count integer NOT NULL DEFAULT 0,
    locked_until       timestamptz,
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now()
);

COMMENT ON TABLE iam.users IS 'One person. No password column -- credentials live on iam.user_identities so local + SSO can coexist per user.';
COMMENT ON COLUMN iam.users.break_glass IS 'This account may log in with a local password even when iam.system_settings.local_auth_enabled is off -- the emergency floor when all SSO is down. See docs/phase-10-auth.md.';
COMMENT ON COLUMN iam.users.failed_login_count IS 'Consecutive failed local logins; reset to 0 on success. Drives the lockout in iam.users.locked_until.';
COMMENT ON COLUMN iam.users.locked_until IS 'If set and in the future, local login is refused regardless of password -- brute-force backoff.';

-- iam.user_identities -- one row per way this user can prove identity.
-- A local row carries an Argon2id password_hash and no provider_subject
-- (email is the subject); an SSO row carries the provider's stable
-- subject and no password.
CREATE TABLE iam.user_identities (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id             uuid NOT NULL REFERENCES iam.users(id) ON DELETE CASCADE,
    provider            text NOT NULL CHECK (provider IN ('local', 'azuread', 'google')),
    provider_subject    text,
    password_hash       text,
    password_changed_at timestamptz,
    created_at          timestamptz NOT NULL DEFAULT now(),
    UNIQUE (provider, provider_subject),
    UNIQUE (user_id, provider),
    CONSTRAINT local_identity_has_password CHECK (provider <> 'local' OR password_hash IS NOT NULL),
    CONSTRAINT sso_identity_has_subject    CHECK (provider = 'local' OR provider_subject IS NOT NULL)
);

COMMENT ON TABLE iam.user_identities IS 'One authentication method linked to a user. local = Argon2id password; azuread/google = OIDC (Slice B). A user may hold several at once.';
COMMENT ON COLUMN iam.user_identities.provider_subject IS 'The provider''s stable subject (OIDC sub/oid). NULL for local rows -- the email is the local subject. UNIQUE(provider, provider_subject) treats NULLs as distinct, so many local rows coexist; UNIQUE(user_id, provider) caps it at one local identity per user.';
COMMENT ON COLUMN iam.user_identities.password_hash IS 'Argon2id PHC-encoded string (algorithm, version, params, salt, hash all inline) -- never a bare digest. Only local rows have one.';

-- iam.sessions -- server-side, opaque, revocable. The cookie carries a
-- 256-bit random token; only its SHA-256 is stored here, so a DB leak
-- yields no usable cookie. Lookup hashes the presented token and compares
-- in constant time.
CREATE TABLE iam.sessions (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id             uuid NOT NULL REFERENCES iam.users(id) ON DELETE CASCADE,
    token_sha256        bytea NOT NULL UNIQUE,
    csrf_token          text NOT NULL,
    created_at          timestamptz NOT NULL DEFAULT now(),
    last_used_at        timestamptz NOT NULL DEFAULT now(),
    idle_expires_at     timestamptz NOT NULL,
    absolute_expires_at timestamptz NOT NULL,
    revoked_at          timestamptz,
    ip                  text,
    user_agent          text
);

CREATE INDEX sessions_user_idx ON iam.sessions (user_id);

COMMENT ON TABLE iam.sessions IS 'Opaque server-side sessions. token_sha256 is the SHA-256 of the random cookie token (the token itself is never stored). A row is valid only while revoked_at IS NULL and now() < least(idle_expires_at, absolute_expires_at).';
COMMENT ON COLUMN iam.sessions.csrf_token IS 'Per-session double-submit CSRF token. Delivered to the SPA in a readable companion cookie and echoed in the X-CSRF-Token header; the API compares that header against this value in constant time.';
COMMENT ON COLUMN iam.sessions.idle_expires_at IS 'Sliding idle timeout, pushed forward on each authenticated request; a refresh can never push it past absolute_expires_at.';

-- iam.system_settings -- small global key/value. Holds local_auth_enabled
-- (the break-glass switch) so a break-glass admin can flip it at runtime
-- with no redeploy. Seeded off by default here.
CREATE TABLE iam.system_settings (
    key        text PRIMARY KEY,
    value      text NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now(),
    updated_by uuid REFERENCES iam.users(id)
);

INSERT INTO iam.system_settings (key, value) VALUES ('local_auth_enabled', 'false')
ON CONFLICT (key) DO NOTHING;

COMMENT ON TABLE iam.system_settings IS 'Global runtime toggles. local_auth_enabled governs whether ordinary local accounts may log in; break-glass accounts bypass it. Every change is recorded in iam.auth_events.';

-- iam.auth_events -- append-only security audit. login success/failure,
-- logout, SSO link, switch toggle, break-glass login, role change (Slice
-- C). user_id is nullable + ON DELETE SET NULL so an event survives the
-- account it describes.
CREATE TABLE iam.auth_events (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    uuid REFERENCES iam.users(id) ON DELETE SET NULL,
    event_type text NOT NULL,
    detail     text,
    ip         text,
    user_agent text,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX auth_events_user_idx ON iam.auth_events (user_id, created_at);
CREATE INDEX auth_events_type_idx ON iam.auth_events (event_type, created_at);

COMMENT ON TABLE iam.auth_events IS 'Append-only audit of security-relevant events. Never updated or deleted by the application.';
