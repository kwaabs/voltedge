# Phase 32 — Feature external links

A feature carries **many outbound links** to external systems: a service
point → its CIS/billing account, a transformer → its work-order/EAM record,
anything → a document in a doc store. Two link *kinds* share one table and
one panel section, delivered in phases so later slices add behaviour, not a
schema reshape:

- **`hyperlink`** (Slice 1) — a label + an `http`/`https` URL. Opens in a new
  tab. Nothing is fetched; it's a per-feature bookmark.
- **`api`** (Slice 3, later) — a *live* link that fetches data through a
  server-side proxy from a catalogued external system, rendered as a data
  card in the panel.

Numbered 32 because Phase 31 is diagram label transform
([docs/phase-31-diagram-label-transform.md](phase-31-diagram-label-transform.md)).
The stub at [docs/phase-31-external-links.md](phase-31-external-links.md) is a
tombstone from an initial mis-numbering and can be `git rm`'d.

## Why the relationship precedent, not the field system

A feature's links are a **variable-length, per-instance list** — different
features have different numbers of them — so they can't be a per-class scalar
field. This is exactly the shape of feature *relationships*, so external
links are modeled on that precedent:

- polymorphic key `(feature_class_id, feature_id)`;
- **no DB foreign key** on `feature_id` (features live in per-class GIS
  tables, so a single FK can't span them) — existence is validated in Go via
  `crud.GetFeature` before any write, rejecting a missing or soft-deleted
  feature with a 400;
- Go package layout mirrors `api/internal/relationship`
  (`types.go` / `errors.go` / `sql.go` / `handler.go`), with the bun model's
  `alias:` pinned (bun otherwise derives a wrong singular SQL alias — see
  docs/phase-2-connectivity.md).

---

## Slice 1 — Hyperlinks (code-complete, verification owed)

### Schema — migration 0057

`runtime.feature_external_links`:

| column | notes |
| --- | --- |
| `id` | uuid PK |
| `feature_class_id` | uuid, FK → `metadata.feature_classes(id)` |
| `feature_id` | uuid, **no FK** (polymorphic; Go-validated) |
| `kind` | text, default `'hyperlink'`, CHECK in (`hyperlink`,`api`) |
| `label` | text NOT NULL |
| `url` | text, nullable (an `api` link resolves via its system, not a raw URL) |
| `created_at` | timestamptz default now() |
| `created_by` | text (`auth.ActorID`, empty on session-less) |

Index on `(feature_class_id, feature_id)` — the panel's only query shape.
`kind` and the nullable `url` are in the schema from day one so Slice 3 adds
behaviour + a catalog, never an ALTER of this table.

### API — `api/internal/externallink`, mounted `/feature-links`

Mounted top-level (`/features` is taken) behind
`features:read`/`features:write`. Routes:

- `POST /` — validates the feature exists and isn't soft-deleted
  (`crud.GetFeature`), a non-empty label, and `validateHyperlinkURL`
  (absolute `http`/`https` only; rejects `javascript:`/`data:`).
- `GET /?feature_class=&feature_id=` — the panel's list query.
- `PATCH /{id}` — `label?`/`url?` (no frontend caller yet).
- `DELETE /{id}` — hard delete.

### Frontend

- `web/src/hooks/use-external-links.ts` — list/create/delete, query key
  `["external-links", class, id]`.
- A **Links** tab in
  [feature-panel.tsx](web/src/components/map/feature-panel.tsx)
  (`LinksTab`): an inline add row (label + url) and anchor rows
  (`target="_blank" rel="noopener noreferrer"`) with a hover-reveal remove
  button — same add/list/remove shape as the Files tab.

---

## Slice 2 — External-systems catalog + encrypted credentials (code-complete, verification owed)

Slice 3's `api` links need somewhere to name and authenticate the systems
they fetch from. Slice 2 builds that catalog and its admin UI now, so Slice 3
is behaviour over an existing table. It's config in the same shape as
Materials/Domains — gated `metadata:read` to view, `metadata:write` to
change.

### Credential storage — encrypted at rest (user decision)

An admin types a system's secret (API key / password / bearer token) once in
the UI; the server **AES-256-GCM-encrypts** it with an app key from the
environment before storing, decrypts it only at fetch time (Slice 3), and
**never returns it to the browser** — the API exposes only a
`has_credential` boolean.

- `api/internal/secretbox` — `Cipher` with `Encrypt`/`Decrypt` sealing
  `nonce||ciphertext||tag` (a fresh random nonce per call makes one key safe
  across many secrets). `NewFromEnv()` reads a base64 32-byte key from
  `EXTERNAL_SYSTEMS_ENCRYPTION_KEY` and returns `(nil, nil)` when the key is
  **unset** (the API still boots; systems with `auth_type='none'` work,
  storing a secret is refused with a clear error) but a hard error when the
  key is **set but malformed** — the optional-dependency pattern from
  `assistant`'s `llm.NewFromEnv`.
- `.env.example` documents the key (`openssl rand -base64 32`, with the
  rotation caveat that changing it strands existing ciphertext).

### Schema — migration 0058

`metadata.external_systems`:

| column | notes |
| --- | --- |
| `id` | uuid PK |
| `code` | text NOT NULL UNIQUE (stable handle, e.g. `CIS`) |
| `name` | text NOT NULL |
| `base_url` | text NOT NULL |
| `auth_type` | text default `'none'`, CHECK in (`none`,`api_key`,`basic`,`bearer`) |
| `auth_header` | text (the header name for `api_key`) |
| `auth_username` | text (for `basic`) |
| `credential_enc` | bytea (the sealed secret; never returned) |
| `response_format` | text default `'json'`, CHECK in (`json`,`xml`) |
| `created_at` / `updated_at` | timestamptz |

### API — `api/internal/externalsystem`, mounted `/external-systems`

Mounted top-level behind `metadata:read`/`metadata:write` (mirrors how
field-packs' admin API sits top-level while its page lives under `/admin`).
`Routes(db, cipher)` builds a `store{db, cipher}`:

- `GET /` — list every system (never the secrets); `formatSystem` sets
  `has_credential = len(credential_enc) > 0`.
- `POST /` — validate, pre-check unique `code` via `.Exists(ctx)` (the admin
  convention, not catching a driver 23505), seal the credential, insert.
- `PATCH /{code}` — load, merge present fields; switching `auth_type` to
  `none` drops the auth params + secret; the credential is **three-state**:
  absent = keep, present+empty = clear, present+non-empty = re-seal;
  `updated_at = now()`.
- `DELETE /{code}` — 404 if no row.

Validation: `validateBaseURL` (http/https + host), `validateAuthShape`
(`api_key` needs `auth_header`, `basic` needs `auth_username`),
`sealCredential` refuses when `cipher == nil` with a
"set EXTERNAL_SYSTEMS_ENCRYPTION_KEY" error.

`server/main.go` builds `credCipher, err := secretbox.NewFromEnv()`
(log.Fatalf on a malformed key, log.Printf when nil) and mounts the router.

### Frontend

- `web/src/hooks/use-external-systems.ts` — `useExternalSystems()`, query key
  `["external-systems"]`, `staleTime: 30_000`. `ExternalSystem` type has
  `has_credential` and no credential field.
- [web/src/routes/admin-external-systems.tsx](../web/src/routes/admin-external-systems.tsx)
  — the admin page, in the Materials mould (`FormDialog` for create + edit,
  `ConfirmDeleteDialog`, `usePermission()("metadata:write")` gating the write
  controls, `apiFetch`, invalidate `["external-systems"]`). A shared
  `SystemFormFields` renders base URL, auth-type select (conditionally
  showing header name / username / secret), and response-format select. The
  secret input is write-only; on edit it shows "Leave blank to keep the
  current secret" and the body omits a blank credential. Each system renders
  as a card (name, `code · auth_type · FORMAT`, base URL, a
  credential-set/no-credential chip).
- Route `admin/external-systems` in
  [web/src/app.tsx](../web/src/app.tsx); nav entry (Cloud icon,
  `metadata:read`) in
  [app-sidebar.tsx](../web/src/components/layout/app-sidebar.tsx) `adminItems`.

---

## Slice 3 — Live API links (code-complete, verification owed)

An `api` link points at a catalogued external system and fetches live data
through a server-side proxy, rendered as an expandable data card in the panel.

### Schema — migration 0059

Adds two api-only columns to `runtime.feature_external_links`:

- `system_id` uuid, FK → `metadata.external_systems(id)` **ON DELETE SET
  NULL** — deleting a system orphans its api links (fetch then reports the
  system is gone) rather than blocking the delete;
- `resource_path` text — the path (relative) appended to the system's
  `base_url`.

No CHECK forcing `api ⇒ system_id NOT NULL`, precisely because SET NULL must
be allowed to leave that state.

### API — the fetch proxy

`GET /feature-links/{id}/data` (still under `features:read`, so any feature
viewer can read an existing api link's data). `Routes(db, cipher)` now takes
the same credential cipher as the catalog. `externallink/fetch.go`:

1. load the link; reject non-`api` or an orphaned (`system_id` NULL) link;
2. load its system; build the target with `resolveTarget` —
   `base.ResolveReference(relativePath)`, which cannot change origin because
   `validateResourcePath` already rejected any scheme/host on the path;
3. `applyAuth` decrypts the credential **only here** and attaches it:
   `api_key` → configured header, `basic` → `SetBasicAuth`, `bearer` →
   `Authorization: Bearer`. A nil cipher (no key) or missing secret is a clear
   error, never a silent unauthenticated call;
4. fetch through `safeClient()` — **SSRF hardening**: a `net.Dialer.Control`
   hook runs after DNS resolution on the initial connection *and every
   redirect hop* and refuses any loopback/private/link-local/multicast/
   unspecified IP (`isBlockedIP`), defeating DNS-rebinding; plus a 10s
   timeout, a 2 MiB response cap, and a 5-redirect limit;
5. normalize the body (`externallink/xml.go`): JSON as-is, or XML decoded into
   a nested map (attributes → `@name`, repeated elements → arrays, mixed text
   → `#text`, leaf → its string) so both render through one card.

Upstream failures (unreachable, non-2xx, unparseable) return a new
`upstreamError` → **502 Bad Gateway**, so a broken downstream isn't
misreported as our 500 or the caller's 400.

`createLink` now branches on `kind`: a hyperlink still needs an http/https
url; an `api` link needs an existing `system_id` and a validated relative
`resource_path`. `listLinks` enriches api rows with their system's
code/name in one batched lookup (`systemNames`).

### Frontend

- `use-external-links.ts` — `ExternalLink` gains the api fields;
  `CreateLinkInput` is a discriminated union (hyperlink `url` vs api
  `system_id`+`resource_path`); `useExternalLinkData(id, enabled)` fetches the
  proxy on demand (no retry, so a 502 surfaces at once).
- `useExternalSystems(enabled)` gained an `enabled` flag so the panel's
  api-link picker skips the request for users without `metadata:read`.
- `feature-panel.tsx` `LinksTab` — the add form gets a Hyperlink / Live-data
  toggle (the api option shown only to `metadata:read` users with ≥1 system);
  api rows render as `ApiLinkRow`, a header (label · system · path) with a
  Load/Refresh button that expands a pretty-printed JSON card.

### SSRF note

The base URL is admin-set (`metadata:write`) and the resource path is
set by feature editors (`features:write`); the path is the less-trusted
input and is constrained to a relative path, while the dialer-level IP
block is the backstop against a hostile or rebinding host.

---

## Verification owed (Bash blocked here — user runs locally)

- Apply migrations **0057**, **0058**, **0059**.
- API: `go build ./...`, `go vet ./...`, `gofmt`.
- Web: `tsc`, lint, build.
- Set `EXTERNAL_SYSTEMS_ENCRYPTION_KEY` (base64 32 bytes).
- Manual — Slice 1: hyperlink add → opens new tab; `javascript:` URL rejected;
  per-feature scoping.
- Manual — Slice 2: catalog CRUD; store a credential and confirm the API never
  returns the secret (only `has_credential`); PATCH with a blank secret keeps
  it; switching `auth_type` to `none` clears it; storing a secret with the key
  unset returns a clear 400.
- Manual — Slice 3: add an api link (system + resource path), Load → data card
  renders; Refresh re-fetches; a bad path/host is rejected at create; deleting
  a system orphans its links (Load then reports it gone); an unreachable/non-2xx
  system returns 502; confirm the dialer blocks a system whose host resolves to
  a private/loopback IP.
