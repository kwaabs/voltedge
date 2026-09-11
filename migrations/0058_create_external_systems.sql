-- 0058_create_external_systems.sql
-- Purpose: metadata.external_systems -- the admin-managed catalog of
-- external systems a feature's kind='api' links (Phase 32 Slice 3) fetch
-- live data from: a CIS/billing API, a work-order/EAM API, a document
-- service, ... Config, not instance data, so it sits in the metadata
-- schema alongside domains/material_specs and is governed by the same
-- metadata:read/metadata:write admin gate.
--
-- Credentials are encrypted at rest: the admin enters a secret once in
-- the UI, the server AES-256-GCM-seals it (api/internal/secretbox) into
-- credential_enc, and the plaintext is never stored or returned to the
-- browser. Only the Slice 3 fetch proxy decrypts it, server-side, to
-- build an authenticated outbound request. A response never carries the
-- secret -- only whether one is set.
--
-- Slice 2 (this migration + the catalog CRUD) ships no fetch yet; it
-- defines the systems. kind='api' links pointing here arrive in Slice 3.

CREATE TABLE metadata.external_systems (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    code             text NOT NULL UNIQUE,
    name             text NOT NULL,
    base_url         text NOT NULL,
    -- How the outbound request authenticates. 'none' needs no secret;
    -- 'api_key' puts the secret in a named header; 'basic' is
    -- username + secret-as-password; 'bearer' is Authorization: Bearer.
    auth_type        text NOT NULL DEFAULT 'none'
                        CHECK (auth_type IN ('none', 'api_key', 'basic', 'bearer')),
    -- Non-secret auth parameters: the header name for 'api_key', the
    -- username for 'basic'. Null for the kinds that don't use them.
    auth_header      text,
    auth_username    text,
    -- nonce||ciphertext||tag from secretbox.Encrypt. Null when auth_type
    -- is 'none' (or a credential hasn't been set yet). Never selected
    -- into an API response.
    credential_enc   bytea,
    -- Whether the system answers in JSON or XML -- the Slice 3 proxy
    -- normalises XML to JSON in Go so legacy systems work without CORS
    -- or browser-side XML parsing.
    response_format  text NOT NULL DEFAULT 'json'
                        CHECK (response_format IN ('json', 'xml')),
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now()
);

COMMENT ON TABLE metadata.external_systems IS 'Admin catalog of external systems that a feature''s kind=''api'' external links fetch live data from (Phase 32). Config, not instance data. Credentials are AES-256-GCM encrypted at rest in credential_enc; only the server-side fetch proxy decrypts them, never the browser.';
COMMENT ON COLUMN metadata.external_systems.credential_enc IS 'nonce||ciphertext||tag from api/internal/secretbox (AES-256-GCM). The API key / basic password / bearer token, sealed. Never returned in an API response -- responses expose only has_credential.';
COMMENT ON COLUMN metadata.external_systems.auth_header IS 'For auth_type=''api_key'': the request header name the secret is sent in (e.g. X-API-Key). Non-secret.';
COMMENT ON COLUMN metadata.external_systems.auth_username IS 'For auth_type=''basic'': the username paired with the sealed password. Non-secret.';
