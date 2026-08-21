-- 0037_create_api_tokens.sql
-- Purpose: create iam.api_tokens -- opaque, revocable, per-user bearer
-- tokens that let a non-browser client (the Phase 18 MCP server, see
-- docs/phase-18-mcp.md) authenticate as a real VoltEdge user. This is the
-- data-plane agent's identity: an MCP tool call carries one of these in an
-- Authorization: Bearer header, the server resolves it to the owning user,
-- and every tool then runs under that user's exact RBAC permission set
-- (iam.user_roles -> iam.role_permissions) -- the same session -> user ->
-- roles -> permissions chain a cookie session flows through (Phase 10
-- Slice C). No new authorization surface: a token can never do more than
-- the user it belongs to.
--
-- Modeled on iam.sessions (migration 0024): the raw token is 256 bits of
-- crypto/rand entropy, and only its SHA-256 is stored here, so a database
-- leak yields no usable token. Lookup hashes the presented token and
-- compares against token_sha256. Unlike a session there is no idle/absolute
-- sliding window -- an API token is a long-lived credential minted
-- out-of-band, governed by an optional hard expires_at and by revocation.
--
-- Tokens are minted out-of-band by api/cmd/authadmin (the same "admin
-- pre-provisions, nothing shipped in a seed" path that bootstraps the first
-- break-glass admin), never as a seed and, in this slice, never by an
-- online endpoint.

CREATE TABLE iam.api_tokens (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id      uuid NOT NULL REFERENCES iam.users(id) ON DELETE CASCADE,
    name         text NOT NULL,
    token_prefix text NOT NULL,
    token_sha256 bytea NOT NULL UNIQUE,
    created_at   timestamptz NOT NULL DEFAULT now(),
    last_used_at timestamptz,
    expires_at   timestamptz,
    revoked_at   timestamptz,
    created_by   uuid REFERENCES iam.users(id) ON DELETE SET NULL
);

CREATE INDEX api_tokens_user_idx ON iam.api_tokens (user_id);

COMMENT ON TABLE iam.api_tokens IS 'Opaque, revocable, per-user bearer tokens for non-browser clients (the Phase 18 MCP data-plane server). Only the SHA-256 of the 256-bit random token is stored; the token itself is shown once at mint time and never persisted. A token authenticates as its user and inherits exactly that user''s RBAC permissions -- it grants no capability the user lacks. See docs/phase-18-mcp.md.';
COMMENT ON COLUMN iam.api_tokens.name IS 'Human label for the token (e.g. "claude-desktop", "ci-agent") so an operator can tell tokens apart in `authadmin list-tokens` and revoke the right one.';
COMMENT ON COLUMN iam.api_tokens.token_prefix IS 'The first few characters of the raw token (including its ve_mcp_ marker), stored in the clear purely for display -- it identifies a token in listings without revealing anything usable.';
COMMENT ON COLUMN iam.api_tokens.token_sha256 IS 'SHA-256 of the raw bearer token. The token is high-entropy random, so a fast hash is correct here (unlike a password); it exists so a DB leak cannot replay tokens.';
COMMENT ON COLUMN iam.api_tokens.expires_at IS 'Optional hard expiry. NULL means the token never expires and is governed only by revocation -- convenient for a long-lived agent, but an operator is encouraged to set a TTL.';
COMMENT ON COLUMN iam.api_tokens.revoked_at IS 'When set, the token is dead regardless of expires_at. Revocation is immediate -- the whole point of server-side opaque credentials over stateless ones.';
