# Asset-tagging module

An **asset tag** is a short, scannable code minted for **every feature** in the
network, printed on a label (QR + Code128 + human-readable text) and physically
affixed to the plant in the field. A scan resolves the code back to the GIS
record and drops the field worker onto that feature — to open it, attach an
inspection, book a defect, or raise a fault.

It is not a GIS feature and not an operational record (`docs/faults-module.md`,
`docs/phase-49-oms.md`); it is an **identity layer over the features that already
exist**. A feature may carry **many** codes — the one the system mints plus any
pre-existing legacy/external asset numbers registered against it — with exactly
**one** marked primary (the one that gets printed).

```
   FEATURE (a row in some gis.<class> table)
     ▲  resolve (scan) ── runtime.feature_registry ──► (class, id, scope, draft)
     │
   runtime.asset_tags   one feature ⇄ many codes, exactly one is_primary
     ├── VE-7Q3K…   generated, primary   ← printed on the label
     ├── OLD-4471    external            ← a legacy number someone keyed in
     └── VE-9F2P…   generated, superseded ← a reissued/retired code, still resolves
```

## Why it rides `runtime.feature_registry`, not its own schema

Faults, OMS and WAM each earn their own schema because each is a **new record
about** the assets. Asset tags are the opposite: they are a thin index **keyed
by** the assets, and there already is a table that sees every feature the instant
it is written by any path — `runtime.feature_registry` (migration 0108,
`docs/feature-registry.md`). The compiler attaches a per-class
`AFTER INSERT/UPDATE/DELETE` trigger to every `gis.<code>` table that upserts the
registry, so the registry catches the REST API, MCP tools, field-sync replay, and
(heavily) raw-SQL seeds that `INSERT` straight into `gis.*`.

That total coverage is exactly what a "mint a tag for every feature" requirement
needs. So the tags live in the same `runtime` schema and mint off a **single
trigger on the registry** rather than a per-class trigger the compiler would have
to attach and keep in step. A tag scan is "resolve this id back to its class and
scope" — which is what the registry already does — so ~80% of the resolution path
was already built.

**No FK to the gis row.** `feature_id` spans every `gis.<code>` table and
Postgres has no cross-table FK — the same polymorphic-reference stance as
`runtime.feature_registry` and `runtime.feature_connectivity`. A tag whose feature
is purged simply stops resolving (the resolve path joins through the registry,
which the purge removed), and the trigger's DELETE branch tidies it up.

## Data model (migration 0114)

### The code scheme

A tag code is `VE-<base32 body><check char>`:

- **body** — a global sequence value (`runtime.asset_tag_seq`) rendered in
  **Crockford base32** by `runtime.encode_base32_crockford(bigint)`. The alphabet
  is `0123456789ABCDEFGHJKMNPQRSTVWXYZ` — it drops `I`, `L`, `O`, `U` so a human
  reading a printed label can't confuse them with `1`/`0` etc.
- **check char** — `runtime.asset_tag_check_char(body)`: a position-weighted sum
  of the body's digit values mod 32, mapped back through the **same** 32-character
  alphabet. It catches a single mis-keyed/mis-read character before a typo can
  resolve to the wrong asset. Deliberately **not** Crockford's mod-37 check —
  those extra symbols (`* ~ $ = U`) would be ugly on a label and need escaping in
  the QR deep-link URL. Keeping every character in the clean base32 set makes the
  whole code URL- and label-safe by construction.
- **`VE-` prefix** — the default, applied by `runtime.encode_asset_tag(bigint)`
  (the one function the mint trigger and any reissue call). A later configurable
  label-template phase can supersede the prefix, but every code minted here
  carries it.

All three are `IMMUTABLE plpgsql` functions, so they compose in an `INSERT`
`SELECT` (the backfill uses exactly that).

### The store: `runtime.asset_tags`

One row per code:

| column | notes |
|---|---|
| `tag_code` **PK** | globally unique across all features **and statuses**; a voided code is never reused |
| `feature_class_code` | the `gis.<code>` class the feature lives in |
| `feature_id` uuid | the gis row id; **no FK** (polymorphic) |
| `source` | `generated` (system-minted) \| `external` (a registered legacy number) |
| `is_primary` | the one printed on the asset; **≤ 1 per feature** |
| `status` | `active` (live) \| `void` (retired/wrong — does **not** resolve) \| `superseded` (replaced by a reissue but **still** resolves — old labels linger in the field) |
| `print_count` | how many times a label was rendered (S3) |
| `created_by` | the acting user for a registered/reissued tag; `NULL` for a trigger-minted one |
| `created_at` / `updated_at` | |

Indexes: `asset_tags_feature_idx (feature_class_code, feature_id)` and
`asset_tags_feature_id_idx (feature_id)` for the list-for-feature and resolve
paths, plus the invariant

```sql
CREATE UNIQUE INDEX asset_tags_one_primary ON runtime.asset_tags (feature_id) WHERE is_primary;
```

a **partial** unique index so it constrains only the flagged rows — a feature with
a voided ex-primary **and** a new primary is fine.

### The mint/cleanup trigger

A single `AFTER INSERT OR DELETE` trigger `feature_registry_tag_sync` on
`runtime.feature_registry` runs `runtime.sync_asset_tag()`:

- **INSERT** (a new feature entered the registry) → mint one `generated`,
  `is_primary`, `active` tag. Guarded by an `EXISTS` check so a re-appearing id
  never double-mints, and `ON CONFLICT (tag_code) DO NOTHING`.
- **DELETE** (a feature was **purged** — a hard delete removes the registry row; a
  **soft** delete is an `UPDATE` that keeps it, so a soft-deleted asset **keeps**
  the label already on it in the field) → `DELETE` the now-dangling tags.

A registry **re-sync** is an `UPDATE`, which the trigger is **not** attached to, so
re-syncs never re-mint.

### Backfill

The migration ends with an `INSERT … SELECT` over the whole registry
(`WHERE NOT EXISTS` a tag already), so the module is populated the moment the
migration lands — every pre-existing feature gets its primary tag, not just
features created afterward.

## Permissions (migration 0115)

A new `tags:read` / `tags:write` capability pair (`auth.PermTagsRead` /
`auth.PermTagsWrite`), gated method-aware on the `/tags` mount:

- **`tags:read`** — viewer / field-technician tier. Any field tech scans and
  resolves.
- **`tags:write`** — editor tier. Register a legacy code, set-primary, void,
  reissue, and record a print.

Label-template configuration (a later phase) sits under `metadata:write` (admin).
Grants follow the module pattern — the two caps are seeded onto the relevant roles
plus admin and break-glass. Because a scan is scope- and draft-aware (below), a
`tags:read` holder still only resolves codes on features **they** can see.

## The Go package (`api/internal/assettag`)

Outside the metadata compiler, a small package owns the store and the HTTP
surface:

- **`models.go`** — `Tag` (with scan-only `print_count`/`created_at`/`updated_at`).
- **`errors.go`** — `Invalid` → 400, `NotFound` → 404.
- **`store.go`** — the CRUD and the one-primary invariant:
  - `LookupByCode` / `ListForFeature`
  - `Register` (a legacy `external` code, optional `make_primary`)
  - `SetPrimary` (idempotent; clears the old primary and sets the new one inside a
    `RunInTx`, honouring `asset_tags_one_primary`)
  - `Void` (retire a code — **cannot void the primary**, matching the print
    invariant)
  - `Reissue` (supersede the old code and mint a fresh one — a damaged label gets
    a new number while the old superseded code still resolves)
  - `MarkPrinted` (`print_count + 1`; 404 if missing, any status) and the batch
    `MarkManyPrinted` / `PrimaryForFeatures` used by bulk print (below)
- **`resolve.go`** — `Resolve` is **scope- and draft-aware**: it goes through
  `crud.GetFeatureScoped` (the exported door added to `crud/sql.go`:
  `getFeature` + `enforceReadScope` + `enforceDraftScope` + `overlayFeature`), so
  a code on a feature outside the caller's territory or private-draft visibility
  **404s** — hiding existence, exactly like `enforceReadScope`. A `void` code
  404s; a `superseded` code resolves.
- **`http.go`** — the REST handlers (next section).

> **Import direction:** `assettag` imports `crud` (for `GetFeatureScoped`), so
> `crud` **cannot** import `assettag`. Any tag SQL that has to live inside `crud`
> (the query-engine tag filter, below) is written as inline `bun` queries against
> `runtime.asset_tags`, never as a call into `assettag`.

## REST surface (`/tags`)

Mounted in `cmd/server/main.go` after `/feature-registry`, wrapped
`RequirePermission(PermTagsRead, PermTagsWrite)` (method-aware — GET needs read,
mutating verbs need write):

| method + path | purpose |
|---|---|
| `GET /tags/{code}` | resolve by code (path form) |
| `GET /tags?code=` | resolve by code (**query form** — reserved chars in a code resolve cleanly) |
| `GET /tags?feature_class_code=&feature_id=` | list a feature's tags |
| `GET /tags?feature_class_code=&feature_ids=a,b,c` | **batch** primary per feature (bulk print) |
| `POST /tags` | register a legacy/external code (`make_primary` optional) |
| `POST /tags/{code}/primary` | set primary |
| `POST /tags/{code}/void` | void |
| `POST /tags/{code}/reissue` | supersede + mint a replacement |
| `POST /tags/{code}/printed` | bump `print_count` |
| `POST /tags/printed` | **batch** bump (`{codes:[]}`) |

Minting stays trigger-only — there is deliberately **no** create-a-generated-tag
endpoint.

## Label rendering (S3, client-side)

The plan first sketched a server-side RustFS SVG/PDF worker (`skip2/go-qrcode`,
`boombuler/barcode`, an Avery-sheet PDF via the export-worker pattern). That is
**deferred** — those Go deps couldn't be added in this environment. What ships is
**client-side rendering + browser print-to-PDF**, dep-free on the server:

- **`web/src/components/map/tag-label.tsx`** — `TagLabel` renders one 54×30 mm
  label sized **in millimetres** so print is true-size: a QR encoding the deep
  link `${origin}/t/${code}` (via `QRCode.toString` SVG), a Code128 barcode of the
  **raw** code (via `JsBarcode` into a ref'd `<svg>`), and the human-readable mono
  code + subtitle (class / circuit).
- **`tag-print-sheet.tsx`** — `TagPrintSheet` is a full-screen overlay with a
  `print:hidden` toolbar (Print / Close / Esc) and a flex-wrap grid of `TagLabel`s;
  `window.print()` drives the PDF.
- **`index.css`** — an `@media print` block hides the app and reveals only
  `.print-sheet` (`print-color-adjust: exact`).

New npm deps: `qrcode` + `@types/qrcode`, `jsbarcode` + `@types/jsbarcode` (⇒
`npm install` before build). The tsconfig has `verbatimModuleSyntax` and no
`esModuleInterop`, so `qrcode` is imported `import * as QRCode` (named exports) and
`jsbarcode` as default `import JsBarcode` (`export =`).

The **Print** button (in `feature-tags-tab.tsx`) is **read-tier** — everyone can
print — while only the resulting `print_count` bump is write-gated (fired
best-effort `onPrinted` when the user holds `tags:write`).

## Resolution paths

### Web deep-link (S4)

QR codes point at `${origin}/t/<code>`. `web/src/routes/tag-resolve.tsx`
(`TagResolvePage`) reads the `:code` param, calls `useResolveTag` (GET
`/tags?code=` — query form so reserved-char codes resolve; `retry:false`,
`staleTime:0`), and on success hands off exactly like `/features` "Show on map":
`useSelectionStore.getState().select(class, id, {focus:true})` then
`navigate("/", {replace:true})`. Bare `/t` (and any miss) shows a manual
code-entry box. Both `/t` and `/t/:code` sit under `AppLayout` with
`RequirePermission perm="tags:read"`; the sidebar's "Resolve tag" item
(`app-sidebar.tsx`, `QrCode` icon) self-filters on the same perm.

### Field app (S5, offline-first)

The Flutter field app scans in the field, where there may be no network:

- **Pack** — `packSchemaVersion` bumps 9 → **10**. A new pack table
  `asset_tags (tag_code PK, feature_class, feature_id, is_primary, status)` +
  index is filled by `loadPackAssetTags`, which streams
  `runtime.asset_tags WHERE status IN ('active','superseded')` (void omitted,
  mirroring the online resolver) and keeps only codes on packed features. Gated on
  `PackScope.IncludeTags` (`user.Has("tags:read")`, frozen at enqueue). The pack
  manifest gains `asset_tag_count` + `include_tags`. `cmd/fieldsync` mounts the
  same `assettag.Routes(db)` under `/tags` (method-aware `tags:read`/`tags:write`),
  giving the field **online** resolve parity with zero new handler code.
- **Scan** — `mobile_scanner` (`ui/scan_tag_screen.dart`) reads QR **and** Code128
  with torch / camera-switch / manual-entry fallback. `extractTagCode` strips a QR
  deep link `<origin>/t/<code>` down to the bare code and passes a raw Code128
  value through.
- **Resolve** — `pack_map_page.dart` resolves **offline-first**:
  `PackDatabase.resolveTag` (index-backed, degrades to `null` on a SQLite error) →
  feature-by-id → select on the map; on a miss it falls back to the **online**
  `resolveTag` (GET `/tags?code=`). An in-network hit that isn't in the pack is
  named in a dialog but can't be mapped offline.

## MCP parity (S6)

`api/internal/agenttools/tools_assettag.go` `tagTools()` exposes the module to the
MCP server + in-app assistant (`docs`/memory `mcp-frontend-parity`) — thin
adapters over the exported `assettag` engine fns, each gated on the same perm as
its REST route:

- **reads** (`tags:read`): `resolve_asset_tag` (scope/draft-aware; accepts a bare
  code or a QR deep link), `list_feature_tags`, `primary_asset_tags` (batch, →
  `PrimaryForFeatures`).
- **writes** (`tags:write`, own annotations): `register_asset_tag`,
  `set_primary_asset_tag`, `void_asset_tag` (**destructive**), `reissue_asset_tag`,
  `mark_asset_tag_printed`.
- **tag search** (`find_features_by_tag`) — see below.

Write engine fns need a concrete `*bun.DB`, so their adapters use `asDB(db)`.
Minting stays trigger-only (no tool). `tagTools()` is wired into `moduleTools()`
in `registry.go`.

## Follow-ups

### Bulk print (from the `/features` browser)

Single-print was per-feature only; admins wanted to print a batch. The chosen
batch source is the **`/features` class browser** (over map-select / query
results). Backend: `PrimaryForFeatures(class, ids)` (a `DISTINCT ON (feature_id)`
over active tags, `is_primary DESC` then newest → one printable tag per feature)
+ `MarkManyPrinted(codes)` (one `UPDATE`), surfaced as the batch `GET`/`POST
/tags/printed` modes above. Frontend: `usePrimaryTagsBatch` (chunks 120 ids/req)
+ `useMarkManyPrinted`; a `tags:read`-gated selection bar on `routes/features.tsx`
(select-all with indeterminate + "Print tags (N)") feeds `TagPrintSheet`. The
fetch is read-tier; only the `print_count` bump is write-tier. The server-side
batch PDF worker remains deferred (browser print only).

### Tag querying (the structured query engine)

The `/query` engine (`crud/query.go`, `docs/phase-13-query-builder.md`) gained an
optional tag filter so a search can find features **by** their tags:

- `queryRequest` carries `Tag *queryTagFilter` (`types.go`: `Code` +
  `Match` `"exact"`/`"prefix"` + `Source` `"generated"`/`"external"` + `Present`
  `*bool`, default true).
- `buildTagFilter(loaded, table, f)` emits a correlated
  `EXISTS (SELECT 1 FROM runtime.asset_tags _at WHERE _at.feature_class_code = ? AND _at.feature_id = <physicalTable>.id AND _at.status <> 'void' [AND source = ?] [AND tag_code = ? | tag_code ILIKE ?%])`,
  with `Present:false` → `NOT EXISTS`. It correlates by the **trusted
  compiler-stamped physical table name** (`gis.<snake>`, validated
  `^gis\.[a-z0-9_]+$`, no alias); only bound params + the fixed `EXISTS`/`NOT
  EXISTS` tokens are spliced, so there is no injection surface. The clause is
  AND-ed on top of the existing scopes (read scope, draft visibility, spatial).
- When a tag filter is present, `enrichPrimaryTags` does **one** batched
  `SELECT feature_id::text, tag_code FROM runtime.asset_tags WHERE … is_primary AND status = 'active'`
  and stamps `primary_tag_code` on each row (inline SQL — the `crud → assettag`
  import cycle forbids a call into `assettag`), so a code search shows which record
  a scan would land on.
- Exported door `crud.FindFeaturesByTag(loaded, crud.TagQuery{Code, Match, Source,
  Present, Limit})` for the MCP tool `find_features_by_tag` (`RequiredPerm`
  `features:read` + `ExtraPerms [tags:read]` — the **only** agenttool with that
  combination, since it returns feature envelopes **and** reads tags; there is no
  general `query_features` tool, only `query_features_aggregate`, so this is the
  tag surface's own find tool).
- Frontend (`feature-query-panel.tsx`, Filter mode only — the aggregate request
  has no `Tag`): `TagFilterControls` (arm checkbox → presence have/missing +
  source any/generated/external + code box + exact/prefix select); `run()` adds
  `body.tag`; `QueryResultRow` gains optional `primary_tag_code`;
  `QueryResultsTable` shows a "Tag" column when any row carries one.

## Build slices

| slice | scope |
|---|---|
| **S1** core + backfill | migration 0114 (`asset_tags` + mint trigger on `feature_registry` + base32/check-digit SQL fns + backfill) + migration 0115 perms + `api/internal/assettag` (store/resolve/http) + `crud.GetFeatureScoped` + `/tags` mount |
| **S2** legacy registration + management | `use-asset-tags.ts` hook + feature-panel **Tags** tab (list / register legacy / set-primary / void / reissue) |
| **S3** rendering | client-side `TagLabel` (QR + Code128 + text) + `TagPrintSheet` + `@media print` + Print button + `MarkPrinted` |
| **S4** web deep-link | `/t` + `/t/:code` resolve route → select-on-map handoff + manual entry + sidebar item |
| **S5** field app | pack schema v10 tag index + `PackScope.IncludeTags` + `mobile_scanner` scan screen + offline-first `resolveTag` |
| **S6** MCP parity | `tools_assettag.go` `tagTools()` (7 tools) wired into `moduleTools()` |
| follow-up | bulk print (`/features`), tag querying (`/query` filter + `find_features_by_tag`) |

## Owed / deferred

- **Builds (blocked in this environment):** `cd api && go build ./...`;
  `cd web && npm install && npm run build`; `cd mobile && flutter pub get &&
  flutter analyze`; apply migrations 0114 / 0115. Nothing committed across the
  module yet.
- **Deferred (needs Go QR/PDF deps):** the server-side RustFS SVG-on-demand +
  batch Avery-PDF worker and a configurable **public base URL** for the QR deep
  link (`window.origin` covers the web-print case for now). Browser print-to-PDF
  stands in.
- **Open (undecided):** for a composite assembly (pole + transformer + fuse),
  whether it carries one tag or one-per-feature with the primary on the container;
  exact PDF label dimensions / stock.
