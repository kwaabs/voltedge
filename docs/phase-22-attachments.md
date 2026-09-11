# Phase 22 — Attachments

Let a user attach supporting files to a feature — field photos, and on the web
documents (PDF/CAD/etc.) — stored in RustFS and available on both the web app and
the offline mobile field tool.

## Model

**An attachment = one file in RustFS + one metadata row.** The row
(`files.attachments`) is the source of truth for *what is attached to what*;
RustFS holds the bytes. Web and mobile both go through the API — they only ever
touch RustFS via short-lived presigned URLs.

A feature has no single global table (each class compiles to its own
`gis.<code>` physical table), so an attachment is addressed by the
**(feature_class code, feature_id uuid)** pair — the same way the generic CRUD
engine and the field-sync outbox reference features. The row's `id` is
client-mintable so an offline field device can create it with its own UUID and
replay idempotently, exactly like offline feature creates.

Bytes reuse the existing export bucket (`voltedge-exports`) under an
`attachments/{feature_id}/{id}` key (thumbnail at `…{id}.thumb.jpg`), alongside
the existing `exports/…` and `packs/…` namespaces.

## Decisions (locked)

- **Transfer:** presigned URLs. The client PUTs/GETs bytes straight to RustFS;
  the API only issues URLs and records metadata — large files never flow through
  the Go process. (Net-new `ObjectStore.PresignPut`, mirroring `PresignGet`.)
- **Upload is two-step:** `register → presigned PUT → commit`. Register inserts a
  `pending` row and returns a PUT URL; the client uploads; commit HEADs the object
  to confirm it landed (and record true size), generates the image thumbnail, and
  flips the row to `ready`. A pending row whose upload never lands is reaped.
- **Thumbnails are generated server-side on commit** (one code path, uniform
  output, no image work on the phone). Pure Go stdlib — no new dependency.
- **File scope:** mobile captures/views **images only**; web handles **images and
  documents**. `kind ∈ {image, document}`; documents get no thumbnail.
- **Offline reach:** thumbnails ride *inside* the field pack for offline browsing;
  full-res is fetched on-demand (presigned GET) when online.
- **Feature delete:** attachments soft-delete alongside the feature; a later
  retention sweep reaps the RustFS objects (consistent with export/pack reaping).

## Slices

- **S1 — Storage + schema foundation.** ✅ code-complete (verification owed)
- **S2 — Web API** (register/commit/list/delete/url) behind `attachments:read/write`. ✅ code-complete (verification owed)
- **S3 — Web UI**: attachments section in the feature panel (image grid + doc list). ✅ code-complete (verification owed)
- **S4 — fieldsync surface + pack thumbnails** (builder schema bump ships thumbs). ✅ code-complete (verification owed)
- **S5 — Mobile capture + offline outbox** (images; local outbox; upload on sync). ✅ code-complete (verification owed)
- **S6 — Polish/reconcile** (prune synced local files on fresh pack; delete sweep;
  admin attachment counts). ✅ code-complete (verification owed)

---

## Slice 1 — Storage + schema foundation (code-complete, verification owed)

The persistence + storage plumbing every later slice builds on. No HTTP yet.

**Migration [`0041_create_attachments.sql`](../migrations/0041_create_attachments.sql):**
- `CREATE SCHEMA files` (binary artifacts attached to domain data; no blobs in PG).
- `files.attachments` — `id` (uuid PK, client-supplied, no default), `feature_class`
  (FK → `metadata.feature_classes(code)` ON UPDATE CASCADE), `feature_id` uuid,
  `object_key`, `thumb_key?`, `filename`, `content_type`, `byte_size`, `kind`
  (`image|document`), `status` (`pending|ready`), `uploaded_by` (FK → `iam.users`,
  ON DELETE SET NULL), `created_at`/`updated_at`, `deleted_at` (soft delete).
  Partial index `(feature_class, feature_id) WHERE deleted_at IS NULL` — the
  panel's primary query.
- Permissions `attachments:read` / `attachments:write` seeded + granted (read →
  viewer/analyst/editor/admins; write → editor/admins, mirroring `features:write`).
  The 0025 admin CROSS JOIN already ran, so admin grants are explicit (0040 pattern).

**Go constants** — `PermAttachmentsRead` / `PermAttachmentsWrite` added to
[`auth/rbac.go`](../api/internal/auth/rbac.go) (the enforcement contract the route
gates reference).

**`ObjectStore`** ([`export/storage.go`](../api/internal/export/storage.go)) gains:
- `PresignPut(ctx, key) (string, error)` — time-limited PUT URL signed by the
  public-endpoint client, so browsers/devices can upload direct-to-RustFS.
- `Open(ctx, key) (io.ReadCloser, error)` — read an object back (commit reads the
  uploaded image for thumbnailing).
- `Stat(ctx, key) (size, contentType, err)` — confirm a presigned PUT landed and
  record the true size before marking a row ready.

**`internal/attachment` package** (new):
- [`attachment.go`](../api/internal/attachment/attachment.go) — the `Attachment`
  bun model (`table:files.attachments`), `Kind*`/`Status*` constants, and the key
  helpers `ObjectKey(featureID, id)` / `ThumbKey(featureID, id)`. The filename is
  *not* in the key (it rides the row + download content-disposition), so keys can
  never be broken by odd characters.
- [`thumbnail.go`](../api/internal/attachment/thumbnail.go) — `Thumbnail(r) ([]byte,
  error)`: `image.Decode` (JPEG/PNG/GIF), a box-filter downsample to ≤256px
  longest side, re-encoded as JPEG q80. **Pure standard library — no new
  dependency.** A non-image (a document) fails to decode and errors, which the
  commit path treats as "no thumbnail," not fatal.

**Verification owed** (Bash EPERMs here): `go build ./...`; apply migration 0041
and confirm the `files.attachments` table + the two permissions/grants exist.

---

## Slice 2 — Web API (code-complete, verification owed)

The cookie+CSRF+RBAC HTTP surface, mounted at `/attachments` in
[cmd/server/main.go](../api/cmd/server/main.go) behind a method-aware
`attachments:read`/`attachments:write` gate (GET → read, POST/DELETE → write),
with `RequireCSRF` on the mutating routes (the newer-package convention). Every
route is scoped to a feature by the `(code, id)` pair.

**Endpoints** ([attachment/routes.go](../api/internal/attachment/routes.go)):

| Method / path | Does |
|---|---|
| `GET /{code}/{id}` | List a feature's committed attachments; images carry an inline `thumb_url` |
| `POST /{code}/{id}` | Register (feature-exists checked) → insert a `pending` row, return a **presigned PUT URL** + the content-type to PUT with |
| `POST /{code}/{id}/{attachmentID}/commit` | `Stat` the object to confirm the upload landed + record true size; for images read it back and write a JPEG thumbnail beside it; flip to `ready` |
| `GET /{code}/{id}/{attachmentID}/url` | Presigned view/download URL — **inline** for images, **attachment-disposition** (original filename) for documents |
| `DELETE /{code}/{id}/{attachmentID}` | Soft-delete (RustFS objects left for the S6 sweep) |

**Storage** ([export/storage.go](../api/internal/export/storage.go)) gained
`PresignGetInline` (no content-disposition → object served inline under its stored
Content-Type) alongside S1's `PresignPut`/`Open`/`Stat`.

**Design notes:**
- Bytes never flow through the API — the one exception is the commit thumbnail,
  where the server `Open`s the just-uploaded image and `Upload`s a small JPEG. A
  non-decodable image (or a mislabeled one) commits *without* a thumb_url rather
  than failing — the UI shows a placeholder.
- `id` is client-mintable (offline field tool supplies its own uuid for idempotent
  replay); the web omits it and the server mints one. Invalid uuid → 400.
- `kind` is inferred from `content_type` (`image/*` → image) when not given.
- Only `ready` rows list — a registered-but-never-committed upload never shows.
- `google/uuid` promoted from indirect → direct in [go.mod](../api/go.mod).

**Verification owed:** `go build ./...`; exercise the flow end-to-end (register →
PUT to the presigned URL → commit → list shows a thumbnail → url opens the full
image; delete hides it).

---

## Slice 3 — Web UI (code-complete, verification owed)

A **Files** tab on the feature panel ([feature-panel.tsx](../web/src/components/map/feature-panel.tsx)),
sitting beside Attributes/Terminals/Relationships/Materials, rendering
[feature-attachments.tsx](../web/src/components/map/feature-attachments.tsx):

- **Images** as a 3-up thumbnail grid drawn straight from the server's inline
  `thumb_url` (a placeholder icon when a thumbnail is missing); click opens the
  full image in a new tab via a freshly-resolved presigned URL.
- **Documents** as a labelled list (filename + size); click downloads under the
  original filename (attachment-disposition).
- **Upload** (multi-file `<input type=file>`, uploaded sequentially) and **Delete**
  (per file) are gated on `attachments:write` via [use-auth.ts](../web/src/hooks/use-auth.ts)'s
  `usePermission()` — a viewer/analyst sees files read-only, matching enforcement.

The data layer is [use-attachments.ts](../web/src/hooks/use-attachments.ts):
`useAttachments` (list), `useUploadAttachment` (the register → **raw cross-origin
PUT to RustFS** → commit dance; the PUT is a bare `fetch`, no cookies/CSRF, since
the presigned URL self-authorizes), `useDeleteAttachment`, and the imperative
`fetchAttachmentUrl` (short-lived view/download URL, resolved on click rather than
prefetched). Every mutation invalidates the `["attachments", code, id]` query so
the grid refreshes.

Also fixed a Slice 2 compile miss caught during this slice: `routes.go`'s
`makeThumbnail`/`loadClass` take `context.Context` but the package didn't import
`context` — added.

**Verification owed:** `pnpm build` (web) + the end-to-end flow through the panel
(upload an image → thumbnail appears → click opens full → upload a PDF → downloads
→ delete removes both). Bash EPERMs here.

### Slice 3 addendum — guarded delete (Manage-files mode + confirmation)

Removing a file was too easy: with `attachments:write`, a per-file Delete showed
whenever the Files tab was open, so a stray click on a *selected* (not
even edited) feature could destroy an attachment with no confirmation. Two guards
now stand in front of it, in [feature-attachments.tsx](../web/src/components/map/feature-attachments.tsx):

- **Delete only in an opt-in manage mode.** Browsing is always read-only
  (open/download need no mode). A **Manage files** toggle — gated on
  `attachments:write`, so viewers/analysts never see it — flips the tab into a
  `managing` state that reveals **Upload** and per-file **Delete**; **Done** exits.
  This is the Files tab's own local "you're changing data on purpose" state,
  standing in for the feature's attribute-edit session: the Files tab can't join
  that session (it's one of the tabs locked during an edit, since switching tabs
  mid-edit would drop the edit form's local values —
  [feature-panel.tsx:1221](../web/src/components/map/feature-panel.tsx#L1221)), so
  it carries its own instead. `managing` resets on unmount, which the panel forces
  on any tab/feature change (it lands back on Attributes per selection), so Files
  always re-opens read-only.
- **Every delete confirms.** The trash button no longer calls the mutation; it
  opens the shared [ConfirmDeleteDialog](../web/src/components/admin/confirm-delete-dialog.tsx)
  (naming the file, "can't be undone"), whose own in-flight/close/toast handling the
  delete reuses — success closes it, a failed delete keeps it open with a toast.

Web-only; the mobile field tool's attachment model differs (its per-tile "discard"
only drops an *un-uploaded* local capture, a local, non-destructive action — not a
server delete), so it's untouched. Verification owed: `pnpm build`; as a viewer no
Manage/Delete appears; as an editor, Manage files → Delete → confirm removes the
file, Cancel keeps it.

---

## Slice 4 — fieldsync surface + pack thumbnails (code-complete, verification owed)

Two server-side pieces so the offline field tool can carry images: thumbnails ride
*inside* the pack for offline browsing, and a bearer-authenticated attachments
surface lets the device upload photos and pull full-res originals when online.

**Pack thumbnails — builder schema v5 → v6**
([fieldpack/builder.go](../api/internal/fieldpack/builder.go)):
- New `attachments` table in the pack SQLite: `id, feature_class, feature_id,
  filename, content_type, byte_size, thumb BLOB` + a `feature_id` index. `thumb`
  is the server-generated JPEG bytes; **NULL** when the image committed without a
  thumbnail (device shows a placeholder). **Full-res never rides the pack.**
- `loadPackAttachments` streams every ready image attachment under the pack
  snapshot and keeps those whose feature is in the packed set (the same
  stream-and-membership filter `loadConnectivity` uses); documents are excluded
  (field tool is images-only). `writePackAttachments` then reads each thumb's bytes
  from RustFS (`ObjectStore.Open`) *after* the DB tx closes — so a slow object read
  never holds the Postgres snapshot open — and inserts the rows into the still-open
  SQLite tx (atomic with the rest of the pack). A thumb that can't be read is
  written NULL rather than failing the build.
- `buildArtifacts` now takes the `*export.ObjectStore` (to `Open` thumbs); manifest
  gains `attachment_count`; build log line includes `attachments=N`.

**Fieldsync attachments API — bearer auth**
([fieldpack/routes.go](../api/internal/fieldpack/routes.go),
[attachment/routes.go](../api/internal/attachment/routes.go)):
- New `attachment.BearerRoutes(db, store)` — the *same* list/register/commit/url/
  delete handlers as the web `Routes`, minus the cookie CSRF guard (a bearer
  request has no cookies). Mounted at `/attachments` on the fieldsync server behind
  the existing `bearerAuth` gate + a new method-aware `requirePermMethod`
  (`attachments:read` for GET, `attachments:write` for POST/DELETE). So the device
  runs the identical register → presigned PUT → commit dance and gets the same
  presigned view URL the web does.

**Verification owed:** `go build ./...`; rebuild the fieldsync image and build a v6
pack over a feature that has a committed image — confirm `attachment_count > 0` in
the manifest and the `attachments` table holds the thumbnail bytes; hit
`GET/POST /attachments/{code}/{id}` on the fieldsync server with a bearer token.
Bash EPERMs here.

---

## Slice 5 — Mobile capture + offline outbox (code-complete, verification owed)

The field tool half of attachments: capture a photo of a feature offline, browse
what's attached (offline thumbnails + captured-but-unuploaded images), and upload
on the next sync. **Images only** — the mobile tool never handles documents.

**New dependency** ([pubspec.yaml](../mobile/pubspec.yaml)): `image_picker`
(flutter.dev-maintained; camera + gallery, no live-preview surface needed — the
heavier `camera` plugin was rejected). Camera/photo usage strings added to
[AndroidManifest.xml](../mobile/android/app/src/main/AndroidManifest.xml)
(`CAMERA`) and [Info.plist](../mobile/ios/Runner/Info.plist)
(`NSCameraUsageDescription`, `NSPhotoLibraryUsageDescription`). *(Confirmed via
AskUserQuestion — native-dep choice matters here given the AGP-9 / built-in-Kotlin
toolchain that already forced dropping `flutter_secure_storage`.)*

**Capture** ([attach/attachment_capture.dart](../mobile/lib/src/attach/attachment_capture.dart)):
`AttachmentCapture.capture(source, destDir)` picks an image (downscaled to
`maxWidth 2048`, quality 85), copies it OUT of image_picker's transient cache and
INTO the pack's own `<pack>/attachments/<uuid>.<ext>` dir (so it survives as the
on-device truth + offline thumbnail source), and returns filename / content-type
(from extension) / byte-size. A local `CaptureSource {camera, gallery}` enum keeps
image_picker's `ImageSource` from leaking into the UI.

**Offline outbox** — a **dedicated local table**, not a `/sync` outbox op: an
attachment is a three-step *byte* upload (register → presigned PUT → commit), which
doesn't fit the JSON-op replay model.
[edit_database.dart](../mobile/lib/src/edit/edit_database.dart) gains a
`local_attachments` table (`id` = client-minted attachment uuid PK, feature_class,
feature_id, filename, content_type, byte_size, `local_path`, created_at,
`synced_at`) + methods (put / all / pending / markSynced / delete).
[edit_store.dart](../mobile/lib/src/edit/edit_store.dart) mirrors the rows in
memory grouped by feature id and exposes `localAttachmentsFor` /
`pendingAttachments` / `pendingAttachmentCount` / `addLocalAttachment` /
`markAttachmentsSynced` / `discardLocalAttachment`; discarding a create also drops
its captured photos + files.

**Upload on sync** ([fieldsync_client.dart](../mobile/lib/src/api/fieldsync_client.dart)):
`uploadAttachment(LocalAttachment)` runs register → **raw PUT to the presigned URL**
(no bearer — the URL self-authorizes) → commit against the S4 bearer `/attachments`
surface; idempotent on the client uuid, so a partial-failure retry is safe.
`attachmentUrl(...)` fetches a presigned view URL for full-res (online only). The
map page's [pack_map_page.dart](../mobile/lib/src/ui/pack_map_page.dart) `Sync`
pushes feature ops **first** (so a create lands before its photos upload against
it), then the captured images; the sync badge and the pending-changes sheet now
fold in unuploaded photos (per-photo Discard / Upload + jump-to-feature).

**Viewing** ([feature_attachments_view.dart](../mobile/lib/src/ui/feature_attachments_view.dart)):
a **Photos** section in [FeatureDetailView](../mobile/lib/src/ui/feature_detail_screen.dart)
(via an optional `attachments` slot — the read-only browse page omits it). A
thumbnail grid combines captured-local images (rendered from disk, with a
not-uploaded/uploaded corner badge) and packed images (`PackDatabase.attachmentsFor`
reads the v6 `attachments` thumbnail bytes; swallows the missing-table error on a
pre-v6 pack). A local + its packed copy share an id → the local one wins and the
dupe is hidden. Tapping opens a dependency-free `AttachmentViewerPage`
(InteractiveViewer pinch-zoom): a local capture straight from `Image.file`, a
packed image's full-res via `Image.network` on a presigned URL (online only).

**Deferred to S6:** pruning a synced attachment's local row + file once a fresh
pack carries its server thumbnail (`PackStore.reconcileSyncedEdits` doesn't touch
`local_attachments` yet), and the RustFS delete sweep.

**Verification owed:** `flutter pub get` (pulls image_picker) + `flutter analyze` +
`flutter run`; capture a photo offline → it shows in the grid with the amber
badge → Sync → badge flips to uploaded and the server has the attachment; on a
re-downloaded v6 pack the photo shows from the packed thumbnail and tapping opens
full-res. Bash EPERMs here.

---

## Slice 6 — Reconcile + retention + admin counts (code-complete, verification owed)

The housekeeping S5 deferred, plus the pack-admin count. Three independent pieces.

**1. Prune synced captures on a fresh pack (mobile).** S5 keeps an uploaded photo's
local file + row rendering as on-device truth until a fresh snapshot bakes the
server thumbnail in — after that the local copy is pure redundancy. The fresh-pack
reconcile ([pack_store.dart](../mobile/lib/src/pack/pack_store.dart)) already
dropped redundant feature overlays/creates; it now also drops synced captures via
`_reconcileSyncedAttachments`: for each OTHER pack's `edits.sqlite`,
`EditDatabase.syncedAttachments()` (uploaded-only) is membership-checked against
the fresh pack's `attachments` table (`PackDatabase.existingAttachmentIds`, swallows
the missing-table error pre-v6) and each match has its **on-device file deleted**
(unlike a feature overlay, a capture owns a file) then its row dropped
(`dropSyncedAttachments`). Pending (un-uploaded) captures are never eligible; a
photo the fresh pack doesn't cover is kept. This closes S5's deferred item.

**2. Soft-deleted attachment retention sweep (server).** Deleting an attachment (or
its feature) only sets `deleted_at`; the RustFS objects were left "for the S6
sweep." NEW [attachment/sweep.go](../api/internal/attachment/sweep.go) mirrors
export's `runRetentionSweep`: `sweepDeletedAttachments` selects rows soft-deleted
longer ago than the retention window, deletes their `object_key` **and** `thumb_key`
from RustFS (idempotent; a failed object-delete keeps the row so the next pass
retries rather than orphaning bytes), then hard-deletes the reaped rows.
`RunDeletedSweep` loops it on an interval; `SweepConfigFromEnv` reads
`ATTACHMENT_RETENTION` (default 7d, `0` disables) / `ATTACHMENT_SWEEP_INTERVAL`
(default 1h). It rides the **export worker** ([exportworker/main.go](../api/cmd/exportworker/main.go))
as a goroutine sharing its ctx + store — attachments reuse the export bucket, so no
new process or store is needed.

**3. Admin attachment counts (web).** The count already rides the pack manifest
(`attachment_count`, S4) but the admin list reads `ops.field_packs` columns, so the
worker now persists it there too: migration
[0042](../migrations/0042_field_packs_attachment_count.sql) adds a nullable
`attachment_count`, `completePack` sets it (alongside `feature_count`) from
`res.attachmentCount`, and [admin.go](../api/internal/fieldpack/admin.go) selects +
exposes it. The admin pack-details card
([admin-field-packs.tsx](../web/src/routes/admin-field-packs.tsx)) shows an
**Attachments** row (NULL → "—", so pre-0042 packs read "unknown", not zero).

**Verification owed:** `go build ./...`; apply migration 0042; on the mobile side
`flutter analyze` + re-download a fresh v6 pack over a feature whose captured photo
already synced → confirm the local file + row are gone and the packed thumbnail
renders. Server: soft-delete an attachment, wait past `ATTACHMENT_RETENTION` (or set
it low), confirm the export worker log reports the reap and the RustFS objects are
gone. Web: `pnpm build`; a succeeded pack's details card shows the Attachments
count. Bash EPERMs here.

### Slice 6 addendum — web upload CORS

Symptom: the web upload's middle step failed —
`PUT http://localhost:3702/voltedge-exports/attachments/… net::ERR_FAILED` (no HTTP
status). The register and commit calls (to the API) succeeded; only the browser's
direct PUT to RustFS did.

Cause, not signature: the presigned URL is valid. The web upload
([use-attachments.ts](../web/src/hooks/use-attachments.ts)) PUTs with a
`Content-Type` header, which makes it a **non-simple, preflighted** cross-origin
request — the browser first sends `OPTIONS`, and RustFS answered without CORS
headers, so the browser blocked the PUT before it left. Downloads never hit this:
thumbnail `<img>` GETs are simple requests (not preflighted), and the mobile device
PUTs over native HTTP where CORS doesn't apply — which is why only web uploads broke.

Fix: **server-level CORS on RustFS, not a bucket policy.** Unlike AWS S3/MinIO,
RustFS has no `PutBucketCors` subresource (open request rustfs/rustfs#1386), so there
was nothing for the API's minio client to set — the real knob is RustFS's own
`RUSTFS_CORS_ALLOWED_ORIGINS` env, now wired on the `rustfs` service in
[docker-compose.yml](../docker-compose.yml) as `${RUSTFS_CORS_ORIGINS:-*}` (documented
in [.env.example](../.env.example)). The presigned PUT carries no cookies/credentials,
so wildcard `*` is safe for dev; set `RUSTFS_CORS_ORIGINS` to the web app origin(s) in
prod. Needs **RustFS ≥ 1.0.0-beta.2**, where an unset value means "no CORS headers"
(older builds reflected any origin back with credentials — GHSA-x5xv-223c-8vm7).
[storage.go](../api/internal/export/storage.go)'s `EnsureBucket` grew only a comment
explaining why there's nothing to set there (an earlier `SetBucketCors` attempt was
reverted — RustFS would reject the subresource and log a misleading boot warning on
every start).

**Verification owed:** `docker compose up -d rustfs` with the new env (recreate the
container so it picks up `RUSTFS_CORS_ALLOWED_ORIGINS`), then retry a web upload —
the PUT should return 200 and the attachment should commit + render. `go build ./...`
still clean after the storage.go revert.

