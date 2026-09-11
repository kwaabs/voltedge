-- 0041_create_attachments.sql
-- Purpose: Phase 22 Slice 1 -- the attachments foundation. A feature of any class
-- can carry supporting files (field photos, and on the web PDFs/CAD/docs) stored
-- in RustFS; this table is the source of truth for what is attached to what,
-- while the bytes live under an attachments/{feature_id}/{id} key in the shared
-- export bucket (voltedge-exports). See docs/phase-22-attachments.md.
--
-- A feature has no single global table -- each class compiles to its own
-- gis.<code> physical table -- so a row is addressed by the (feature_class code,
-- feature_id uuid) pair, exactly how the generic CRUD engine and the field-sync
-- outbox reference features. A FK to the physical row is therefore impossible;
-- feature_class FKs the catalog by code so an attachment can never name a class
-- that does not exist (ON UPDATE CASCADE tracks a class rename).
--
-- id is client-mintable: a field device offline-creates the row with its own
-- UUID (matching the create-feature idempotency path in crud), so the API always
-- supplies the primary key -- there is no gen_random_uuid() default.
--
-- status models the two-step presigned upload. The client registers a row
-- ('pending') and gets a presigned PUT URL, uploads the bytes straight to RustFS,
-- then commits ('ready'). A 'pending' row whose upload never lands is reaped by a
-- later sweep. deleted_at is a soft delete mirroring the feature tables; the
-- RustFS objects are reaped separately.
--
-- Permissions attachments:read / attachments:write follow the 0025/0040 pattern
-- (a data-defined capability enforced by auth.RequirePermission in Go). The 0025
-- admin CROSS JOIN already ran, so admin/break-glass grants are added explicitly.

CREATE SCHEMA IF NOT EXISTS files;

COMMENT ON SCHEMA files IS 'Binary artifacts attached to domain data. Metadata rows here point at bytes in RustFS (the shared export bucket); no large blobs live in Postgres. See docs/phase-22-attachments.md.';

CREATE TABLE files.attachments (
    id            uuid PRIMARY KEY,
    feature_class text NOT NULL REFERENCES metadata.feature_classes(code) ON UPDATE CASCADE,
    feature_id    uuid NOT NULL,
    object_key    text NOT NULL,
    thumb_key     text,
    filename      text NOT NULL,
    content_type  text NOT NULL,
    byte_size     bigint NOT NULL DEFAULT 0,
    kind          text NOT NULL CHECK (kind IN ('image', 'document')),
    status        text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'ready')),
    uploaded_by   uuid REFERENCES iam.users(id) ON DELETE SET NULL,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    deleted_at    timestamptz
);

COMMENT ON TABLE files.attachments IS 'Files attached to features. Bytes live in RustFS under object_key (thumb_key for the image thumbnail); this row is the source of truth. Addressed by (feature_class, feature_id) since features have no shared table. See docs/phase-22-attachments.md.';
COMMENT ON COLUMN files.attachments.id IS 'Client-mintable uuid (a field device offline-creates it), so the API supplies the PK -- no gen_random_uuid() default. Matches the create-feature idempotency path.';
COMMENT ON COLUMN files.attachments.object_key IS 'RustFS key the full file lives under: attachments/{feature_id}/{id}.';
COMMENT ON COLUMN files.attachments.thumb_key IS 'RustFS key for the downscaled JPEG thumbnail (images only; NULL for documents). Bundled into field packs for offline display.';
COMMENT ON COLUMN files.attachments.status IS '''pending'' from register until the client commits the finished upload, then ''ready''. A pending row with no uploaded object is reaped by a sweep.';

-- Live rows for a feature, newest first: the panel's primary query.
CREATE INDEX attachments_feature_idx ON files.attachments (feature_class, feature_id)
    WHERE deleted_at IS NULL;

-- The permission catalog rows + Go constants in api/internal/auth/rbac.go.
INSERT INTO iam.permissions (key, description) VALUES
    ('attachments:read',  'View and download files attached to features.'),
    ('attachments:write', 'Upload, replace, and delete files attached to features.')
ON CONFLICT (key) DO NOTHING;

-- read: everyone who can see features (viewer/analyst/editor + admins).
INSERT INTO iam.role_permissions (role_id, permission_key)
SELECT r.id, 'attachments:read'
FROM iam.roles r
WHERE r.key IN ('viewer', 'analyst', 'editor', 'admin', 'break-glass-admin')
ON CONFLICT DO NOTHING;

-- write: whoever can edit features (editor + admins), mirroring features:write.
INSERT INTO iam.role_permissions (role_id, permission_key)
SELECT r.id, 'attachments:write'
FROM iam.roles r
WHERE r.key IN ('editor', 'admin', 'break-glass-admin')
ON CONFLICT DO NOTHING;
