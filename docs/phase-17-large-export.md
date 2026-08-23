# Phase 17 — Large / asynchronous export

Phase 14 made export **synchronous**: a `POST /export` reads every matching
feature into memory, an encoder buffers the whole document in a `strings.Builder`,
and the bytes stream back on the same HTTP request. That is fine for a map
viewport's worth of features; it falls over at scale. On a survey of ~10M
features a single CIM/Shapefile/DXF document is many GB — the buffer alone OOMs
the API, the full-dataset read OOMs it again, and a multi-minute request is a
timeout/retry liability that also pins API memory and CPU for the whole run,
degrading latency for every other user.

This phase makes large export **asynchronous and isolated**: the API only
*enqueues* a job and returns immediately; a **separate, long-running worker
process** generates the file with constant memory and uploads it to **RustFS**
(the S3-compatible object store already in the compose stack); the client
downloads it via a short-lived **presigned URL**. The API's hot path never runs
an export.

## Decisions (locked with the user)

- **Every export runs off the API's hot path.** The user's requirement is that
  triggering an export must not degrade the main API's performance. So *all*
  exports become jobs — the synchronous streaming path is retired, not kept as a
  "small export" fast lane (one code path, no size-guessing heuristic to get
  wrong).
- **Worker model: a persistent worker *service*, not a process-per-export.**
  Weighed against literally spawning an OS subprocess per export (maximum
  isolation, but pays fork/exec + a fresh DB pool + metadata-catalog load on
  *every* export, and multiplies Postgres connections under load) and an
  in-process goroutine pool (cheapest, but *no* memory isolation — a big export's
  GC/CPU pressure still hits API latency, the exact thing to avoid). A dedicated
  worker binary, separate process from the API, pulling jobs off a queue, gives
  the API isolation **and** avoids per-request spawn cost, with a shared bounded
  DB pool and natural backpressure. Ephemeral *tasks* per job; the worker process
  itself is long-lived.
- **Queue: Postgres, `FOR UPDATE SKIP LOCKED`.** No new infrastructure — we
  already run Postgres, and a `SELECT … FOR UPDATE SKIP LOCKED` claim is a
  well-worn, crash-safe job queue. `LISTEN/NOTIFY` is an optional latency
  optimization on top, not a dependency.
- **Delivery: RustFS + presigned GET URL.** The worker streams the output into
  RustFS via S3 multipart upload (constant worker memory, resumable); the job
  completes with a time-limited presigned URL the client downloads directly, so
  the API never relays export bytes. RustFS is already in the compose stack
  (`RUSTFS_*` in `.env.example`); this phase is the first thing to actually use
  it.
- **Streaming is a prerequisite, not an optimization.** The worker model alone
  fixes nothing if the encoder still buffers the whole document — it just moves
  the OOM to the worker. Two independent buffers to retire: the **output** side
  (each text encoder built the whole document in a `strings.Builder` and flushed
  once) and the **read** side (`gatherLayers` materializes every feature). The
  output side is the smaller, safer change and lands first (Slice 1). The read
  side is deferred to the worker slice on purpose: the synchronous path is
  already capped at `maxExportFeatures = 50000` per class (`crud/export.go`), so
  it cannot reach the read-side OOM today — the cap is only *lifted* in the
  worker, and that is exactly where a DB cursor becomes load-bearing. Splitting
  them keeps each change independently shippable and reviewable.
- **CIM topology stays a whole-graph step.** ConnectivityNode synthesis
  (`export/topology.go`) is union-find over the entire connectivity set and
  cannot be sharded across the feature set without breaking node identity and the
  deterministic-mRID contract. At 10M it moves off the in-RAM per-request path
  but stays a single global pass (persisted/incremental topology is a later
  slice, not v1).
- **Consistency: a snapshot read.** A multi-minute export reads inside one
  `REPEATABLE READ` transaction so concurrent edits can't tear the file.
- **Authorization is snapshotted at enqueue.** The job row records the requesting
  user and their *resolved* export scope (class codes, bbox, territory set) at
  `POST` time, evaluated under the same `features:read` gate the sync path used.
  The worker re-uses that snapshot; it never re-derives permissions later.

## Architecture

```
  client ──POST /export──▶ API (enqueue only)
                             │  writes ops.export_jobs row (status=queued)
                             ▼
                        Postgres  ◀──FOR UPDATE SKIP LOCKED claim──┐
                             │                                     │
                             ▼                                     │
   client ◀─poll GET /export/jobs/{id}─ API                  export-worker (separate process)
                             ▲                                     │  streams encoder → RustFS
                             │                                     ▼  (S3 multipart, snapshot read)
                             └────────── presigned URL ◀──── RustFS object store
```

The API gains three small endpoints and loses the streaming responsibility. The
worker is a new `cmd/exportworker` binary (sibling of `cmd/server`,
`cmd/compile`, `cmd/authadmin`) deployed as its own compose service, scaled
independently of the API.

## The job model (`ops.export_jobs`)

`ops` is the schema reserved from day one for "job/audit tables later"
(`migrations/0001`). One row per export request, carrying the request snapshot,
the lifecycle state, and the result pointer:

| Column | Purpose |
|---|---|
| `id` uuid PK | job id; the client polls by it |
| `status` text | `queued` → `running` → `succeeded` / `failed` (+ `cancelled`); CHECK-enforced |
| `format` text | `cim` / `kmz` / `shp` / `dxf` — the encoder to run |
| `requested_by` uuid | the authenticated user (FK to the auth user table); download RBAC + audit |
| `scope` jsonb | the *resolved* request: class codes, bbox, expanded territory ids, cim profile — the authz snapshot |
| `object_key` text | RustFS key once uploaded (`exports/{id}/voltedge-export.{ext}`) |
| `byte_size` bigint | final object size (nullable until done) |
| `feature_count` int | rows exported (nullable until done) |
| `error` text | failure message on `failed` (nullable) |
| `attempts` int | claim count; guards the crash-requeue loop |
| `worker_id` text | which worker holds the lease (observability) |
| `lease_expires_at` timestamptz | heartbeat lease; a stale `running` row is reclaimable |
| `created_at` / `started_at` / `finished_at` | timing |

### The claim (SKIP LOCKED)

A worker claims the oldest queued (or lease-expired) job atomically:

```sql
UPDATE ops.export_jobs SET
    status = 'running', worker_id = $1, started_at = now(),
    lease_expires_at = now() + $2::interval, attempts = attempts + 1
WHERE id = (
    SELECT id FROM ops.export_jobs
    WHERE status = 'queued'
       OR (status = 'running' AND lease_expires_at < now())   -- crashed worker
    ORDER BY created_at
    FOR UPDATE SKIP LOCKED
    LIMIT 1
)
RETURNING *;
```

`SKIP LOCKED` lets N workers (and N goroutines within a worker) claim distinct
jobs with no contention. A crashed worker's job is reclaimed once its lease
lapses; `attempts` caps retries so a job that reliably OOMs a worker eventually
lands in `failed` instead of thrashing forever. The worker heartbeats
`lease_expires_at` forward while running.

## Streaming rewrite (the prerequisite)

Two changes, both format-agnostic:

- **Writer → `io.Writer`.** Replace the `strings.Builder` + `.String()` tail
  (`cim.go`, `shapefile.go`, `dxf.go`, `kml.go`) with writing straight to a
  `bufio.Writer` over the destination (the S3 upload pipe). The CIM encoder's
  four passes stay, but each object is flushed as produced — O(part-size) memory,
  not O(document).
- **Read → cursor.** `gatherLayers` currently materializes every `Feature` into
  `[]Layer`. Switch the equipment pass to a streaming cursor (bun `Rows()` /
  keyset pagination) so features are encoded one at a time. The small
  aggregations (substation rings, distinct BaseVoltages) stay eager — they're
  bounded and cheap.

CIM's wrinkle: it is multi-pass (substations, base voltages, equipment,
topology), so the equipment pass streams while the bounded passes are precomputed
up front, and topology runs last over the connectivity set (see below).

## CIM topology at scale

`writeTopology` is union-find over `runtime.feature_connectivity`. At 10M
features (~2–3 endpoints each) the parent array is ~240MB — feasible in the
worker's isolated memory, but it is a genuine whole-graph pass that cannot be
streamed away. v1 keeps it in-worker. The scale escape hatch (a later slice) is
to **persist** endpoint→ConnectivityNode assignments in a `runtime` table,
maintained incrementally as connectivity is committed, so export (and traces)
read topology instead of recomputing it. Called out as deferred, not built now.

## API changes

`/export` stops streaming and becomes a job API (still behind `features:read`):

- `POST /export` — validate + resolve scope (same authz as today), insert a
  `queued` job, return `202` with `{ job_id }`. No bytes.
- `GET /export/jobs/{id}` — the requester's own job: `{ status, feature_count,
  byte_size, error, download_url? }`. `download_url` (a fresh presigned GET) is
  present only on `succeeded`. 404 for a job the caller didn't request (no
  cross-user peeking).
- `GET /export/jobs` — the caller's recent jobs (history panel).

Download RBAC: the presigned URL is capability-bearing, so it is minted only for
the owning user, short-lived (`RUSTFS_EXPORT_URL_TTL`, e.g. 15m), and re-minted
per poll rather than stored.

## Config (env, matching the `os.Getenv` convention)

Add to the API and worker (reusing the existing `RUSTFS_ACCESS_KEY` /
`RUSTFS_SECRET_KEY`):

- `RUSTFS_ENDPOINT` — S3 endpoint (compose-internal host:port).
- `RUSTFS_BUCKET` — e.g. `voltedge-exports` (worker ensures it exists on boot).
- `RUSTFS_USE_SSL` — `0` for local dev.
- `RUSTFS_EXPORT_URL_TTL` — presigned GET lifetime (default 15m).
- `EXPORT_WORKER_CONCURRENCY` — max concurrent jobs per worker (default e.g. 2).
- `EXPORT_JOB_LEASE` — heartbeat lease (default e.g. 2m).

S3 client: **`minio-go`** — the lightest S3-compatible client, first-class with
RustFS/MinIO-style stores, and its `PutObject` streams an unknown-size reader as
multipart automatically (the encoder writes one end of an `io.Pipe`, the uploader
reads the other), which is exactly the constant-memory upload we need.

## Frontend changes

The export dialog stops expecting a file download. On submit it `POST`s and gets
a `job_id`, then shows a jobs panel that polls `GET /export/jobs/{id}`; on
`succeeded` it surfaces the download link (the presigned URL). This is a modest
change to `export-dialog.tsx` plus a small `use-export-jobs` hook — the same
poll-a-job pattern the app can reuse for any future long task.

## Slices

- **Slice 1 — streaming writer.** ✅ *Code complete (2026-08-21, verification
  owed — Bash blocked).* Retire the whole-document `strings.Builder` in the three
  text encoders: `cim.go` now streams through a `bufio.Writer` (via a small
  `cimBuf` adapter so the `~40` `cw.b` emit sites across
  `cim/topology/electrical/phasing.go` were untouched), and `dxf.go` / `kml.go`
  swap their `*strings.Builder` helper params for `*bufio.Writer`, wrapping the
  destination and `Flush`-ing once. `shapefile.go` stays buffered by necessity —
  the .shp/.shx headers carry the file length, bbox, and per-record offsets, all
  unknowable until every record is encoded, and the format has a hard 2 GB
  ceiling — so it is the documented exception (and the natural candidate for the
  per-feeder split under Open items). No API/worker change; the synchronous
  endpoint behaves identically, now at constant *output* memory. Trade-off:
  streaming means a mid-document geometry-unmarshal error (DXF/KML) can surface
  after some bytes are on the wire, so it is no longer a clean pre-stream failure
  — acceptable, since DB geometry is well-formed and the handler already treats
  any post-header error as a truncated download.
- **Slice 1b — cursor read (now Slice 3d).** Replace `gatherLayers`' full
  materialization with a DB cursor. Deferred to the worker because the sync
  path's `maxExportFeatures` cap makes the read-side OOM unreachable there; see
  the streaming-prerequisite decision above. Originally bundled with the snapshot
  read as "Slice 3c"; split out once 3c's snapshot landed, since the cursor read
  is a much larger encoder rewrite (see Slice 3d).
- **Slice 2 — job model + queue + async API.** ✅ *Code complete (2026-08-21,
  verification owed — Bash blocked).* Migration `0036_create_export_jobs.sql`
  (`ops.export_jobs`: status/format CHECKs, `requested_by` FK → `iam.users`,
  `scope` jsonb authz snapshot, result pointer, lease/attempts, claim + owner
  indexes). `export/job.go`: the `ExportJob` bun model, `ExportScope`, and the
  worker-agnostic queue primitives — `enqueueExport`, `claimExport`
  (`FOR UPDATE SKIP LOCKED`, raw SQL, reclaims lease-expired `running` rows),
  `completeExport`/`failExport`, owner-scoped `getExportJob`/`listExportJobs`.
  `export/export.go` cut over: `POST /export` validates + resolves scope +
  enqueues → `202 {job_id}` (no bytes); `GET /export/jobs`, `GET /export/jobs/{id}`
  (owner-scoped, 404 on cross-user), `GET /export/jobs/{id}/download`. The
  synchronous streaming handler is gone; `runExport` (the gather+encode core)
  moved to `export/runner.go`. **`export/runner.go` is the throwaway in-process
  runner** (chosen cut-over path): a goroutine started from `cmd/server/main.go`
  that claims jobs, encodes to a local temp file (path parked in `object_key`),
  and marks succeeded/failed — so downloads work *today*, before the real worker.
  **Known break until Slice 4:** the Phase 14 export dialog still expects a file
  download and will choke on the `202 {job_id}`; the frontend jobs UX is Slice 4.
  Deferred to Slice 3: real worker, heartbeating a short lease (the runner parks
  a 1h lease since nothing competes), RustFS upload replacing the temp file, and
  a presigned URL replacing the `/download` passthrough.
- **Slice 3 — the worker binary + RustFS upload.** The phase's core, cut into
  three independently verifiable steps so no single unverifiable drop is large
  (Bash — hence `go build` — is unavailable here):
  - **Slice 3a — RustFS round-trip.** ✅ *Code complete (2026-08-21, verification
    owed).* Introduces `export/storage.go`: an `ObjectStore` over **minio-go**
    (`NewObjectStoreFromEnv` builds the client offline; `EnsureBucket` on boot;
    `Upload` streams an unknown-length multipart PutObject bounded by a 16 MiB
    part size; `PresignGet` mints a short-TTL GET URL with a download
    `content-disposition`). The **existing in-process runner keeps running** but
    its sink flips from a temp file to RustFS: `runClaimedJob` pipes the encoder
    into `store.Upload` via `io.Pipe` (constant memory, nothing on local disk),
    `object_key` now holds the RustFS key, and an encode failure closes the pipe
    with its error so the multipart upload aborts (no partial object). Error
    priority: the encode error wins over the induced upload error; an
    encode-clean-but-upload-failed job fails on the upload error. `GET
    /export/jobs/{id}/download` stops proxying `os.Open` bytes and instead
    **302-redirects to a freshly minted presigned URL** — minted at click time so
    the TTL is spent on the download, not on prior polling. `cmd/server/main.go`
    builds the store (fatal on missing `RUSTFS_ENDPOINT`; a bucket-ensure failure
    is a warning, not a boot-blocker — a RustFS outage can't take the API down).
    New env: `RUSTFS_ENDPOINT` / `RUSTFS_BUCKET` / `RUSTFS_USE_SSL` /
    `EXPORT_URL_TTL` (`.env.example`). **Endpoint caveat:** the presigned URL is
    signed for `RUSTFS_ENDPOINT`, so that host must be reachable by *both* the API
    (to upload) and the browser (to download) — fine in host-run dev
    (`localhost:3702`); once the API runs inside compose this must be the
    public-facing host (an internal-only endpoint would sign URLs the browser
    can't use). Adds the `github.com/minio/minio-go/v7` dependency (run `go get`).
  - **Slice 3b — the worker process.** ✅ *Code complete (2026-08-21,
    verification owed).* Extracted `cmd/exportworker` (sibling of
    server/compile/authadmin). The claim loop lives in the export package as
    `RunWorker` (`worker.go`) so it can drive the unexported queue/execution
    primitives directly; the command is just wiring (db + store + signal
    context). Bounded concurrency (`EXPORT_WORKER_CONCURRENCY`, default 2) via a
    slot semaphore acquired *before* each claim, so an unclaimable job stays
    `queued` for another worker rather than being locked idle. Each running job
    gets a heartbeat goroutine that extends its lease (`EXPORT_JOB_LEASE`, default
    60s) at lease/3; a heartbeat that finds the job no longer ours (0 rows —
    reclaimed or cancelled) cancels the job context so the encode/upload aborts.
    `WorkerConfigFromEnv` derives a `host-pid` worker id. Graceful drain:
    SIGINT/SIGTERM cancels the loop and `RunWorker` waits for in-flight jobs.
    Added `heartbeatExport` to `job.go`. **Deleted the throwaway
    `StartInProcessRunner`** (its file, `runner.go`, is repurposed as the shared
    execution core — `runExport`/`runClaimedJob`/`sleep`/etc. stay) and its
    `cmd/server/main.go` call (replaced by a comment: run the worker alongside).
    One image ships both binaries (`Dockerfile` builds `server` + `exportworker`);
    compose gains an `exportworker` service (same image, `command: [exportworker]`,
    `rustfs:9000` internal endpoint), and `task exportworker-dev` runs it on the
    host. **`REPEATABLE READ` snapshot read deferred to 3c** — it changes the same
    read-path signatures the cursor read does (threading a tx/`bun.IDB` through
    `gatherLayers` → `crud.ExportFeatures`), so the two land together as one
    coherent read-path change. Until then the worker reads exactly as the
    in-process runner did (no snapshot) — no regression.
  - **Slice 3c — snapshot read.** ✅ *Code complete (2026-08-21, verification
    owed — Bash blocked).* `runExport` now runs its entire read inside one
    `REPEATABLE READ`, read-only `db.RunInTx` (`database/sql` `TxOptions{Isolation:
    LevelRepeatableRead, ReadOnly: true}`), so a multi-class export can't tear
    against a concurrent edit — every class is read at one point in time. The
    plumbing was cheap: `metadata.Load`, `crud.ExportFeatures`, `loadConnectivity`,
    `loadMaterialSpecs`, and `LoadCIMMapping` already took `bun.IDB`, so only
    `gatherLayers` (→ `bun.IDB`) and `runExport`'s body changed; the `bun.Tx`
    threads straight through. CIM's extra inputs (mapping/connectivity/specs) read
    under the *same* snapshot as the features. Because everything is still
    materialized (the cursor read is Slice 3d), the tx is held only across the
    reads and released before the encode streams its bytes through the
    upload-bound pipe — the snapshot window is the read, not the whole upload.
  - **Slice 3d — cursor read + cap lift (was Slice 1b, split out of 3c).** ✅
    *Code complete (2026-08-21, verification owed — Bash blocked).* The
    streamable formats (KMZ/DXF) now read features through a DB cursor and are
    **uncapped**; the materialized formats (shapefile, CIM) keep the
    `maxExportFeatures = 50000` cap because they can't stream (shapefile's header
    holds offsets from a full pass + a hard 2 GB ceiling; CIM topology is a
    whole-graph union-find). The read path splits in two:
    - `crud/export.go`: extracted `exportQuery` (the shared SELECT/WHERE/ORDER BY,
      no LIMIT); `ExportFeatures` appends the cap'd `LIMIT` as before;
      **new `ExportFeaturesStream`** runs the same query with no LIMIT and scans
      row-by-row (generic per-row `Scan` + `formatRow`), invoking a callback per
      feature and returning the count — nothing is materialized.
    - `export/feature.go`: **new `StreamLayer`** (metadata + an `Each(emit)`
      cursor driver, no `Features` slice) with `FieldKeys()` (column set from
      metadata `Fields`, the streaming analog of `AttributeKeys`). An `asStream`
      adapter seeds a `StreamLayer`'s `Fields` from a materialized `Layer`'s
      `AttributeKeys`, so `FieldKeys` reproduces the exact data-union column set —
      letting the streaming encoders serve the materialized callers (the format
      tests) with byte-identical output.
    - `export/kml.go` + `export/dxf.go`: the encoders are now
      `writeKMZStream`/`writeKMLStream`/`writeDXFStream` over `[]StreamLayer`
      (pulling features through `Each`, returning the count); the original
      `writeKMZ`/`writeKML`/`writeDXF` (`[]Layer`) survive as thin adapters over
      them (`asStreams`), so **the existing encoder tests are untouched**.
    - `export/export.go`: `format` gains a `writeStream` field (kmz/dxf set it,
      shp keeps `write`, cim sets neither); **new `gatherStreamLayers`** loads
      each class's metadata up front (fail-fast on bad code) but defers the
      feature read into each `StreamLayer.Each` (a `crud.ExportFeaturesStream`
      cursor).
    - `export/runner.go`: `runExport` dispatches — `spec.writeStream != nil` runs
      `gatherStreamLayers` + `writeStream` **entirely inside** the `REPEATABLE
      READ` snapshot tx (the cursor stays live across the encode, so the snapshot
      is now held for the whole upload — the cost of a constant-memory, uncapped
      export); otherwise the materialized Slice-3c path (read under the snapshot,
      encode after) serves shapefile + CIM.
    Rationale for the data-union/metadata split: in production `formatRow`
    populates every metadata field on every row (nulls included), so the
    `FieldKeys` (metadata) column set equals the `AttributeKeys` (data-union) set
    — identical output. The two only diverge for a hand-built test `Layer` with
    attributes but no `Fields`, which is exactly why the materialized encoders
    (and their tests) keep the `AttributeKeys` path via the adapter.
- **Slice 4 — frontend jobs UX.** ✅ *Code complete (2026-08-21, verification
  owed — Bash blocked).* Unbreaks the dialog that Slice 2 knowingly broke. New
  `use-export-jobs.ts` hook: `useEnqueueExport` (`POST /export` → `{job_id}`,
  invalidates the jobs list on success) and `useExportJobs(enabled)` — a
  `GET /export/jobs` query that self-polls every 2s while any job is
  `queued`/`running` and stops once all are terminal (`refetchInterval` returns
  `false`), and only runs while the panel is open. `export-dialog.tsx` no longer
  calls `postForBlob`/`saveBlob`; `handleExport` enqueues via `mutateAsync`, then
  closes the dialog and opens the jobs panel (pending state is the mutation's
  `isPending`). New `export-jobs-panel.tsx` (a `FloatingPanel`) lists the
  caller's recent jobs with a status icon/badge, feature-count + byte-size +
  created-time, an inline error on `failed`, and a **Download** button on
  `succeeded` — a plain `<a href="/api{download_url}">` so the session cookie
  rides along and the route's 302→presigned-URL (with a download
  content-disposition) triggers the save without navigating the app away.
  `export-store.ts` gains a second, independent `jobsPanelOpen` bit (the panel
  outlives the dialog); the panel is mounted in `routes/map.tsx`. History falls
  out for free — the list is server-owned and per-user (newest 50), so the panel
  shows prior exports, not just the one just submitted. **Follow-up:** there is
  no toolbar affordance to *re-open* the panel after closing it (you reopen it by
  submitting another export); a small "Exports" toggle button could be added.
- **Slice 5 — persisted/incremental topology (designed, not built — trigger
  signal added instead).** ✅ *Closed as designed-not-needed (2026-08-22,
  verification owed — Bash blocked).* The optimization (a persisted, incrementally
  maintained topology table so the CIM export doesn't rebuild union-find from
  scratch each time) stays **unbuilt on purpose**: no export has hit the in-worker
  union-find as a bottleneck, the payoff is speculative, and the "incremental" part
  hides the dynamic-connectivity hard problem (maintaining connected components
  under edge *removal* is genuinely hard, unlike the append-only union-find the
  batch export gets away with). Building it now would be correctness risk
  (deterministic-mRID contract depends on ConnectivityNode identity; a denormalized
  topology table must stay consistent with `runtime.feature_connectivity`) for no
  observed gain. **Instead of building, we made the bottleneck observable:**
  `writeTopology` now returns a `topoStats` (edges consumed, terminals/union-find
  set size, ConnectivityNodes, and an estimated `parent_bytes` footprint), threaded
  up through `writeCIM` → `runExport`, which logs one line per CIM export:
  `export: CIM topology features=… conns=… terminals=… nodes=… parent_bytes=…`.
  The union-find `parent` map is the dominant structure (~240MB projected at 10M
  features); `parent_bytes` is an order-of-magnitude gauge (≈ `48·entries` plus the
  key bytes held as key+root). So the day a real export's `parent_bytes` climbs
  toward that ceiling, there is a concrete number in the logs (alertable) telling us
  Slice 5's time has actually come — rather than discovering it via an OOM. All in
  `export/topology.go` (`topoStats` + return), `export/cim.go` (`write`/`writeCIM`
  thread the value), `export/runner.go` (the `log.Printf`). No schema, API, or
  frontend change.
- **Slice 6 — retention / cleanup sweep.** ✅ *Code complete (2026-08-21,
  verification owed — Bash blocked).* Closes the object leak: every succeeded
  export left its RustFS artifact behind forever. The worker now runs a periodic
  **retention sweep** (`export/retention.go`) alongside its claim loop: it finds
  terminal jobs (`succeeded`/`failed`/`cancelled`) whose `finished_at` is older
  than `EXPORT_RETENTION` (default `168h` = 7 days; `0` disables), deletes each
  one's artifact from RustFS (new `ObjectStore.Delete`, an idempotent
  `RemoveObject`), then hard-deletes the reaped rows. Batched at 500/pass so a
  backlog drains over several passes; runs once at boot then every
  `EXPORT_SWEEP_INTERVAL` (default 1h). Ordering is object-first, row-second: an
  artifact whose delete fails keeps its row so the next pass retries, since the
  row is the only handle on the object — dropping it first would orphan the
  object permanently. **Orphan reaping folds in for free:** a `failed`/`cancelled`
  job records no `object_key`, but the sweep also tries the *deterministic* key
  (`objectKey(id, ext)`), so an object whose upload landed but whose
  `completeExport` write didn't (the one true orphan case) is reaped too — and
  because the key is deterministic, a *retried* job overwrites rather than
  orphans, so that's the only orphan the system can produce. Safe to run in
  multiple workers at once (idempotent deletes; row delete only touches
  already-terminal, already-aged rows). New env: `EXPORT_RETENTION`,
  `EXPORT_SWEEP_INTERVAL` (`.env.example`); wired through `WorkerConfig` /
  `WorkerConfigFromEnv`. No migration, no API change, no frontend change — a
  reaped job simply drops off the per-user history list on the next poll.
- **Slice 7 — job cancellation.** ✅ *Code complete (2026-08-21, verification
  owed — Bash blocked).* Wires the `cancelled` state that was already in the
  schema but had no trigger. New `POST /export/jobs/{id}/cancel` (owner-scoped)
  → `cancelExport`, an `UPDATE … SET status='cancelled' WHERE id=? AND
  requested_by=? AND status IN ('queued','running')`. A **queued** job is
  cancelled outright (no worker involved). A **running** job's flip to
  `cancelled` is noticed by its worker's next heartbeat — `heartbeatExport`
  already guards on `status='running'`, so it returns "not ours" and
  `runJobWithHeartbeat` cancels the job context, aborting the in-flight
  encode/upload (the existing lost-lease path, now also reached by a cancel). The
  handler re-reads the job to report the result: `404` if it isn't the caller's,
  `409` if it already finished (`succeeded`/`failed`) before the cancel landed,
  else the cancelled `jobView`. Two supporting guards: `completeExport` and
  `failExport` now carry a `status='running'` WHERE clause, so a worker that
  finishes (or errors on the cancelled context) *after* the cancel can't
  resurrect the job to `succeeded`/`failed` — it stays `cancelled`. **Synergy
  with Slice 6:** a running job cancelled mid-upload leaves its already-uploaded
  artifact behind, but the key is deterministic, so the retention sweep reaps it
  as an orphan — no extra cleanup needed here. Frontend: `useCancelExport`
  (`use-export-jobs.ts`) + a **Cancel** button on every pending job in
  `export-jobs-panel.tsx` (toasts a 404/409 via `pushError`), and the panel/hook
  learn the `cancelled` status (a muted `Ban` icon, terminal so polling stops).
  No migration — `cancelled` was already a valid status in the `0036` CHECK.

## Open items (deferred, tracked)

- **Per-feeder / per-substation export.** A single 10M-feature CIM file is itself
  a liability (consumers choke on multi-GB RDF/XML); exporting bounded sets per
  feeder is often the better *product* answer and makes topology naturally
  shardable. Orthogonal to this phase's plumbing; revisit as a filter option.
- ~~**Retention / cleanup.**~~ ✅ Done in **Slice 6** — the worker's retention
  sweep reaps aged terminal jobs' artifacts + rows (and the deterministic-key
  orphan) on a timer. See Slice 6 above.
- ~~**Internal-vs-public RustFS endpoint.**~~ ✅ Done (2026-08-22, alongside
  Phase 21). `storage.go` now signs download URLs with a second minio client bound
  to `RUSTFS_PUBLIC_ENDPOINT` (PUTs still go through `RUSTFS_ENDPOINT`); unset falls
  back to one endpoint, so host-run dev is unchanged. Wired into the in-compose
  `api` + `fieldsync` services (`rustfs:9000` internal / `localhost:3702` public).
- ~~**Job cancellation.**~~ ✅ Done in **Slice 7** — `POST
  /export/jobs/{id}/cancel` flips the `cancelled` state; a running job's worker
  aborts via the existing heartbeat lost-lease path. See Slice 7 above.
- **Progress reporting.** `feature_count` lands at completion; a live
  percent-complete would need the worker to update a `progress` column as it
  streams. Nice-to-have, not v1.
- ~~**Persisted topology**~~ (Slice 5 above) — closed as *designed, not needed
  yet*: no export has hit the union-find ceiling, so instead of building it we
  added the `parent_bytes` trigger signal to the CIM export log. Revisit **when**
  that number actually climbs toward the ~240MB projection, not before.

## Status

**Slices 1–2 + 3a + 3b + 3c + 3d + 4 + 6 + 7 code-complete; Slice 5 closed as designed-not-needed (trigger-signal logging added in its place).**
Decisions locked (persistent worker service, Postgres `SKIP LOCKED` queue,
RustFS + presigned URL, streaming prerequisite). Slice 1 retired the
whole-document output buffer in `cim.go`/`dxf.go`/`kml.go` (`shapefile.go` stays
buffered by format necessity). Slice 2 cut the API over to async jobs
(`ops.export_jobs` + `SKIP LOCKED` queue + `202 {job_id}` + poll/download), with
a throwaway in-process runner so downloads work before the real worker. Slice 3a
flipped that runner's sink to a real RustFS `io.Pipe` multipart upload and made
`/download` a 302 to a presigned URL. Slice 3b extracted the standalone
`cmd/exportworker` (concurrency + lease heartbeat + graceful drain), deleted the
in-process runner, and added the compose service — so exports now run fully off
the API process. Slice 4 rebuilt the frontend around the async API: the dialog
enqueues a job and hands off to a polling jobs panel that surfaces each export's
download link (unbreaking the UI Slice 2 knowingly left broken). Slice 3c wrapped
the worker's read in a `REPEATABLE READ` snapshot so a multi-class export is
point-in-time consistent. Slice 3d made KMZ/DXF stream their features through a
DB cursor (`ExportFeaturesStream` + `StreamLayer`), uncapped, encoding inside the
snapshot tx — shapefile/CIM stay materialized and capped since they can't stream.
Slice 6 added the worker's retention sweep (`export/retention.go`): aged terminal
jobs' RustFS artifacts + rows (and the deterministic-key orphan) are reaped on a
timer (`EXPORT_RETENTION` / `EXPORT_SWEEP_INTERVAL`), so the bucket stays bounded.
Slice 7 wired job cancellation: `POST /export/jobs/{id}/cancel` flips `queued`/
`running` → `cancelled`, a running job's worker aborts via the heartbeat's
lost-lease path, and `completeExport`/`failExport` gained a `status='running'`
guard so a late finish can't un-cancel it (a Cancel button lives on each pending
job in the panel).
**The API no longer drains the queue: `task exportworker-dev`
(or the compose `exportworker` service) must run alongside it, or jobs sit
`queued`.** Verification (Bash unavailable in this environment) is **owed** on
every slice:
- **Slice 1:** `go build ./...` (✅ done — `task api-dev` boots) / `go test ./...`;
  byte-for-byte diff of a CIM/DXF/KMZ export vs. pre-streaming output.
- **Slice 2:** `go build ./...` / `go test ./...`; apply migration `0036`
  (`task migrate`); end-to-end `POST /export` → poll `GET /export/jobs/{id}` →
  `GET .../download` for each format; confirm cross-user `GET /export/jobs/{id}`
  returns 404. **Frontend export is broken until Slice 4** (dialog expects a file,
  gets a job id) — expected, per the chosen cut-over.
- **Slice 3a:** `go get github.com/minio/minio-go/v7` then `go build ./...`; add
  `RUSTFS_ENDPOINT`/`RUSTFS_BUCKET` to `.env`. (Its RustFS round-trip is now
  driven by the worker, not the API, so 3a's smoke test folds into 3b's below.)
- **Slice 3b:** `go build ./...` (both `./cmd/server` and `./cmd/exportworker`);
  run `task api-dev` **and** `task exportworker-dev`; confirm the worker logs
  `export worker <host-pid> started` and ensures the bucket. Enqueue one export
  per format (cim/kmz/shp/dxf) and confirm: the object appears in RustFS under
  `exports/<job-id>/…`, the job goes `succeeded` with a `byte_size` +
  `feature_count`, and `GET /export/jobs/{id}/download` 302s to a presigned URL
  that downloads the file. Kill the worker mid-export and confirm the job's lease
  lapses and a restarted worker reclaims it (`attempts` increments). Confirm a
  cross-user `GET /export/jobs/{id}` still 404s. **Frontend export stays broken
  until Slice 4.**
- **Slice 4:** `pnpm -C web tsc --noEmit` / `pnpm -C web build` (type-check the
  new hook/panel). With `api-dev` + `exportworker-dev` up, open Export, submit a
  job of each format, and confirm the dialog closes, the jobs panel opens and
  polls, each job flips `queued`→`running`→`succeeded`, and its **Download**
  button fetches the file (302 → presigned URL). Confirm a `failed` job shows its
  error inline, and that reopening (via a fresh submit) shows prior jobs as
  history.
- **Slice 3c:** `go build ./...` / `go test ./...`; confirm an export still
  produces byte-identical output (the snapshot changes isolation, not results),
  and — ideally — that an edit committed mid-export doesn't appear in the file.
- **Slice 3d:** `go build ./...` / `go test ./...` (the encoder tests must still
  pass unchanged — the materialized adapters preserve byte-for-byte output).
  Confirm a KMZ/DXF export of a class with **more than 50k features** now
  completes (uncapped) while a shapefile/CIM export of the same class still stops
  at 50k. Confirm a KMZ/DXF export at 10M features holds constant worker memory
  (the cursor never materializes). Spot-check that a mid-export edit still doesn't
  appear (the cursor now runs inside the snapshot tx across the whole encode).
- **Slice 7:** `go build ./...` / `pnpm -C web tsc --noEmit`. Enqueue an export
  and click **Cancel** while it's `queued` (or `running`) — confirm it flips to
  `cancelled`, the worker (if it had started) logs the lost-lease abort, and no
  succeeded artifact is served. Confirm cancelling an already-`succeeded` job
  returns `409` (toasted), and a cross-user cancel returns `404`. If a running
  job is cancelled mid-upload, confirm its orphaned object is reaped by the next
  retention sweep (Slice 6).
- **Slice 6:** `go build ./...`. Set a tiny `EXPORT_RETENTION` (e.g. `1m`) and
  `EXPORT_SWEEP_INTERVAL` (e.g. `20s`), run one export to `succeeded`, then
  confirm that after the window the sweep logs `reaped 1 expired export`, the
  RustFS object is gone, and the job drops off `GET /export/jobs`. Set
  `EXPORT_RETENTION=0` and confirm the sweep never runs (no reap log, jobs
  persist). Optional orphan check: leave an object at `exports/<id>/…` with no
  succeeded row and confirm the next sweep removes it via the deterministic key.
