# Phase 23 — Sync inbox: durable receive, gated apply, review/QA

**Status: Phase 23 CODE-COMPLETE — Slices 1–3 + Slice 4a (server) + 4b (web
conflict-resolution UI) + 4c (mobile `superseded` handling) all done (4a+4b+4c
2026-08-28; verification owed — Bash blocked in-session, build/device runs owed to
the user).** This doc is both the plan and the running record. Supersedes the "should
sync be staged?" discussion.

## Why

VoltEdge needs to be two things at once, and today's sync path only comfortably
supports one:

1. A **reliable field-capture tool** — an edit made offline must never be lost, on
   any device, over any network.
2. An **authoritative system of record** — edits that land in production may need
   **review / QA / conflict resolution** before they're trusted, and every submitted
   change needs an **audit trail**.

The current `POST /sync` ([api/internal/fieldpack/sync.go](../api/internal/fieldpack/sync.go))
does **receive** and **apply** in one step: it replays each op straight into the
production tables via the shared `crud` / `connectivity` / `relationship` engines and
returns a per-op `applied | error` result. That's simple and idempotent, but it
couples three concerns that should be separable:

- **Durability is coupled to business logic.** The device only marks an op synced when
  the server says *"applied to production."* So the no-loss guarantee depends on
  metadata load + geometry validation + topology succeeding inside the request. A slow
  or failing apply on flaky cellular can time out mid-batch.
- **Rejected ops vanish server-side.** A validation failure returns an error string and
  leaves **no server record**. The op retries from the device forever (a *poison op*),
  visible only in a snackbar.
- **There is no place to hold, review, or reconcile.** Conflicts are silent
  last-writer-wins; there's no queue a supervisor can post from.

The fix is one architectural move: **decouple receive from apply** with a durable
server-side inbox.

## The model: a chain of custody

```
Device outbox  ──►  ops.sync_inbox  ──►  Production tables
(edits.sqlite,      (durable receive,     (idempotent apply via the
 durable until       ack = "captured")     existing crud/graph engines,
 server acks)                              optionally gated by review)
```

Every hop keeps its copy until the **next** hop confirms durability — nothing is
released on a promise:

- The **device outbox** already persists an op until the server acks it (`synced_at`
  stays NULL until then). Unchanged.
- The **server inbox** becomes the new durable landing zone. `POST /sync`'s *first and
  only synchronous job* is to persist the raw ops and ack. The ack now means
  **"durably captured,"** not "applied."
- **Production** is written by a worker draining the inbox — immediately (trusted mode)
  or after review (system-of-record mode).

This one split is what serves all four concerns (data loss / efficiency / reliability /
performance) and unlocks review/QA without changing the apply engines.

## New table: `ops.sync_inbox`

Lives in the `ops` schema alongside `ops.export_jobs` — the schema reserved from day one
(migration 0001) for exactly these queue/job tables. Migration **0043** (0042 is the
latest today).

```sql
CREATE TABLE ops.sync_inbox (
    -- op_id is the device-minted idempotency key. It is the PK, so a re-pushed op
    -- (retry after an ambiguous ack) is absorbed by ON CONFLICT (op_id) DO NOTHING.
    -- TEXT (not uuid): the device mints these and we never want a malformed id to
    -- fail the whole capture -- idempotency only needs equality, not a uuid cast.
    op_id            text PRIMARY KEY,

    -- recv_seq is the server receive order, assigned at insert. It is the causal
    -- ordering key the worker applies by: the device pushes its outbox oldest-first
    -- (ORDER BY created_at) and a single bulk insert assigns recv_seq in array order,
    -- so recv_seq preserves each device's op order across pushes without the device
    -- having to send an explicit sequence. (device_seq stays reserved for S-future.)
    recv_seq         bigint GENERATED ALWAYS AS IDENTITY,

    -- Who/what submitted it (audit + per-device causal grouping + review grouping).
    submitted_by     uuid NOT NULL REFERENCES iam.users(id) ON DELETE RESTRICT,
    device_id        text,                 -- optional in S1 (client sends none yet); groups a device's ops for serialized apply
    submission_id    text,                 -- groups a batch/survey for review/posting
    device_seq       bigint,               -- reserved: per-device monotonic hint (recv_seq carries order in S1)

    -- The op itself, verbatim from the device outbox. Same vocabulary as syncOp.
    kind             text NOT NULL
                         CHECK (kind IN ('create','update_attrs','delete',
                                         'establish_connection','establish_relationship')),
    feature_class    text,
    feature_id       text,
    payload          jsonb,                -- the crud/graph input, as today (empty for delete)
    base_version     text,                 -- pack snapshot_at / row version for LWW + conflict

    -- Lifecycle. received -> applying -> applied | rejected | conflict | pending_review | superseded.
    --   received       : durably captured, not yet applied (the queued state)
    --   applying        : a worker has claimed it (the running state; lease-guarded)
    --   pending_review : QA mode, awaiting a supervisor's post (Slice 3)
    --   applied        : committed to production (terminal, success)
    --   rejected       : failed apply -- quarantined, NOT retried (terminal; poison-op fix)
    --   conflict       : base_version diverged from production -- awaiting resolution (Slice 4)
    --   superseded     : a later op for the same feature replaced this one before apply (Slice 4)
    status           text NOT NULL DEFAULT 'received'
                         CHECK (status IN ('received','applying','pending_review','applied',
                                           'rejected','conflict','superseded')),
    error            text,                 -- human message on rejected/conflict
    result_feature   jsonb,               -- server row echoed back on applied

    -- Review/QA trail (Slice 3).
    reviewed_by      uuid REFERENCES iam.users(id) ON DELETE RESTRICT,
    reviewed_at      timestamptz,

    -- Queue bookkeeping, mirrored from ops.export_jobs: attempts caps the
    -- crash-requeue loop; worker_id + lease_expires_at implement the heartbeat lease
    -- that reclaims an op whose worker died mid-apply (status='applying' AND lease lapsed).
    attempts         integer NOT NULL DEFAULT 0,
    worker_id        text,
    lease_expires_at timestamptz,

    received_at      timestamptz NOT NULL DEFAULT now(),
    started_at       timestamptz,          -- when a worker last claimed it
    finished_at      timestamptz           -- when it reached a terminal status
);

-- Claim path: the worker scans the oldest applyable op in recv_seq order.
CREATE INDEX sync_inbox_claim_idx  ON ops.sync_inbox (status, recv_seq);
-- Per-device serialization + status pull-back: the "is an earlier op still active?"
-- guard and a device's own op lookups.
CREATE INDEX sync_inbox_device_idx ON ops.sync_inbox (submitted_by, device_id, recv_seq);
-- Review queue: a submission's ops, and pending-review triage (Slice 3).
CREATE INDEX sync_inbox_review_idx ON ops.sync_inbox (status, submission_id, received_at);
```

## Ingestion path (write path 1) — receive and ack

`POST /sync` is rewritten to do **only** a durable, cheap capture:

1. Validate the request *shape* only (JSON parses, op kinds known, required fields
   present). No metadata load, no geometry work, no engine calls.
2. One bulk `INSERT ... ON CONFLICT (op_id) DO NOTHING` of the batch into
   `ops.sync_inbox` with `status = 'received'` (or `'pending_review'` if policy — see
   below).
3. Return `200` with a per-op result of **`received`** (plus `duplicate` for ops the
   ON CONFLICT absorbed — already captured, still a success from the device's view).

The device's ack contract changes only in meaning: it marks an op synced on
**`received`/`duplicate`**, because the op is now durably server-side. Everything about
the outbox, batching (≤200), ordering, and idempotent retry stays as-is.

**Why this is the load-bearing change:** a single idempotent insert almost never fails,
so the no-loss guarantee no longer rides on apply-time business logic. Apply can fail,
retry, or wait for review **without any risk to the data** — it's already safe.

## Apply path (write path 2) — drain the inbox to production

A worker drains `received` inbox rows and commits them to production. **Reuse the
Phase 17 worker skeleton** (`export.RunWorker` /
[cmd/exportworker](../api/cmd/exportworker/main.go)): the same
`FOR UPDATE SKIP LOCKED` claim + heartbeat lease (`worker_id`, `lease_expires_at`) +
`attempts` cap + graceful drain-on-shutdown, pointed at `ops.sync_inbox` instead of
`ops.export_jobs`.

Per claimed op, the worker runs **the existing `applySyncOp` body almost unchanged** —
it already applies one op per transaction, idempotently, through the shared engines:

- **create / update_attrs / delete** → `crud.CreateFeature` / `UpdateFeature` /
  `DeleteFeature` (with the per-drain `metaCache` deduping metadata loads, exactly as
  the request-time cache does today).
- **establish_connection / establish_relationship** → the zero-trust
  `connectivity` / `relationship` engines, which authorize against
  `network_rules` / `relationship_classes` themselves.

On success → `status = 'applied'`, `applied_at = now()`, `result_feature` set. On a
**permanent** validation/authz failure → `status = 'rejected'`, `error` set — the op is
**quarantined, not retried** (this is the poison-op fix). On a **transient** failure
(DB hiccup, deadlock) → leave `received`, bump `attempts`, retry with backoff; a cap
sends chronic failures to `rejected` with a diagnostic.

**Causal ordering.** The worker applies a device's ops in **`recv_seq`** order so a
`connect` never lands before the `create` it references. The device already emits ops
oldest-first (`ORDER BY created_at`) and a single bulk insert assigns `recv_seq` in that
same order, so no explicit client sequence is needed in S1. The claim query enforces
**single-flight per device**: it will not hand out an op while an *earlier* op from the
same `(submitted_by, device_id)` is still active (`received`, or `applying` with a live
lease), so two workers can't apply a device's create and connect out of order. An earlier
op that is terminal (`rejected`/`applied`) does **not** block — one poison op can't stall
the whole device; the dependent op simply fails and is quarantined too. Cross-device ops
are independent and parallelize freely (SKIP LOCKED across devices). Because all apply
paths are idempotent, even a lease-expiry double-claim is harmless.

## Status protocol back to the device

Because apply is now asynchronous, the device must distinguish **received** from
**applied/rejected/conflict**. Two small additions:

- The device records each op's server status: `received` (synced, awaiting apply) →
  `applied` (done) / `rejected` (needs attention) / `conflict` (needs resolution).
- **Both** pull paths ship server-side in S1 (owner-scoped, same `getInboxStatuses`):
  the next `POST /sync` piggy-backs statuses for a device-supplied `poll` list of op_ids
  in a `statuses` array, and **`GET /sync/status?op_ids=a,b,c`** serves a device with
  nothing new to push. The device's consumption — a "needs attention" list for
  `rejected`/`conflict` instead of silent retry — lands in S2.

In **trusted mode** the apply worker runs near-inline (sub-second), so the
received→applied lag is negligible and the field UX feels identical to today — just with
a durable receive stage and an audit trail in front of it.

## One pipeline, two personalities (the policy switch)

A **policy** — per dataset / per feature-class / per role (exact granularity is an open
decision, see below) — decides what happens after receive:

- **Trusted field-capture mode.** Ops land `received` and the worker auto-applies
  immediately. This is today's behavior plus durability + audit. Fast, edits live on
  arrival.
- **System-of-record / QA mode.** Ops land `pending_review`; the worker does **not**
  auto-apply. A supervisor reviews and **posts** a reviewed set, which flips those ops
  to `received` for the worker (or applies them inline under the reviewer's authz). This
  is the classic "post to default" / versioned-edit workflow utilities expect.

Same ingestion, same durability, same apply engine — the gate is the only difference.

## Review / QA workflow (system-of-record mode)

- **Review queue (web).** List `pending_review` ops grouped by `submission_id` / device
  / crew, with the decoded change (attribute diff, new geometry, proposed edge). RBAC-gated
  (a new `sync:review` / `sync:post` permission, mirroring the export authz model).
- **Conflict detection.** At review (or at apply) compare `op.base_version` against the
  current production row version. If diverged → `status = 'conflict'`, surface both the
  field edit and the current server value. Today this is silent LWW; this makes it
  explicit and resolvable (accept field / keep server / merge).
- **Post.** Applying a reviewed set stamps `reviewed_by` / `reviewed_at` and promotes
  the ops. `superseded` handles the case where a later op for the same feature obsoletes
  an earlier queued one.

## How this answers the four concerns

- **Data loss.** Chain of custody: device outbox (durable) → inbox (durable, ack =
  captured) → production (idempotent). The ack no longer depends on apply-time logic, so
  the no-loss guarantee is independent of validation/topology/network stalls. Rejected
  and conflicting ops are **retained** for triage, not dropped (today they have no
  server record at all).
- **Efficiency.** Receive is a single bulk idempotent insert — cheap, bounded, fast to
  ack. The heavy per-op work (metadata, geometry, topology) moves off the request path
  into the worker; the `metaCache` N+1 fix carries over to the drain loop.
- **Reliability.** Idempotent at both hops. Explicit per-op status. Poison ops are
  quarantined, not retried forever. Transient failures retry with backoff under the
  lease; a dead worker's op is reclaimed via the heartbeat.
- **Performance.** The device gets fast acks (receive, not apply) → snappier field UX
  and a shorter connection hold on bad cellular. Apply parallelizes across workers via
  SKIP LOCKED; review queries are indexed status scans.

## Rollout & backward compatibility

- Additive migration (0043) — no change to production feature tables.
- The `POST /sync` contract stays the same wire shape; only the result `status` gains
  `received` / `duplicate` alongside the existing values. An old client that treats any
  non-error result as "synced" keeps working (it just marks on `received`).
- Default policy = **trusted / auto-apply**, so behavior is unchanged until QA mode is
  turned on for a dataset. Ship the durable-receive + worker-apply plumbing first
  (Slice 1), review/QA later (Slices 3–4).

## Slices

- **Slice 1 — durable receive + worker apply (auto-apply mode). CODE-COMPLETE
  (2026-08-27, verification owed.)** Highest-leverage slice for
  data-loss/reliability/performance; stands alone; backward-compatible. Shipped:
  - **Server (1a):** migration [`0043_create_sync_inbox.sql`](../migrations/0043_create_sync_inbox.sql)
    (`ops.sync_inbox`, `recv_seq` IDENTITY ordering, per-device claim index);
    [`inbox.go`](../api/internal/fieldpack/inbox.go) (the `SyncInboxRow` model +
    queue primitives: `receiveInboxOps` bulk idempotent capture, `claimInboxOp` with
    the per-device single-flight guard, `heartbeatInboxOp`, `markInboxApplied` /
    `markInboxRejected`, `getInboxStatuses`);
    [`syncworker.go`](../api/internal/fieldpack/syncworker.go) (`RunSyncWorker`
    mirroring `export.RunWorker` — claim + heartbeat-lease + drain, applying via the
    unchanged `applySyncOp`; any apply error → `rejected` quarantine);
    [`sync.go`](../api/internal/fieldpack/sync.go) rewritten so `POST /sync` captures
    + acks `received`/`duplicate` and piggy-backs a `poll` status list, plus a new
    `statusHandler` for `GET /sync/status?op_ids=…`; the route + a second
    `RunSyncWorker` goroutine wired in
    [routes.go](../api/internal/fieldpack/routes.go) / [cmd/fieldsync](../api/cmd/fieldsync/main.go).
  - **Mobile (1b, ships WITH 1a — required, not optional):** the server now acks
    `received`, so `SyncOpResult.isApplied` → **`isAccepted`** (received | duplicate |
    applied) in [models.dart](../mobile/lib/src/api/models.dart), and `_pushOps` in
    [pack_map_page.dart](../mobile/lib/src/ui/pack_map_page.dart) marks synced on
    accepted. Backward-compatible with an old inline-apply server (`applied` still
    counts as accepted).
  - **Deferred from S1 as scoped:** `device_seq` on the wire (`recv_seq` carries order;
    the client sends no `device_id`/`device_seq` yet — a user's ops serialize under a
    NULL device group, which is correct if conservative); the device *consuming* the
    status pull (the "needs attention" list) is S2; per-class policy is S3.
- **Slice 2 — quarantine + device status surfacing. CODE-COMPLETE (2026-08-27,
  verification owed.)** The device *consumes* the status pull and surfaces
  quarantined ops, closing the poison-op gap end-to-end (the server already
  rejects + records; before this the device treated a captured op as done and never
  learned it was quarantined). Entirely device-side — no server change. Shipped:
  - **Outbox schema** ([edit_database.dart](../mobile/lib/src/edit/edit_database.dart)):
    three in-place `ALTER TABLE outbox` columns (guarded by `_hasColumn`, like
    `geom_json`) — `apply_status` (the worker's outcome; `synced_at` still only means
    *captured*), `apply_error`, `dismissed_at`. `markSynced` seeds
    `apply_status='received'` (the op is now *in flight*). New accessors:
    `inFlightOpIds()` (captured but non-terminal — the poll set), `recordApplyStatus`,
    `attentionOps()` / `attentionCount()` (rejected|conflict, un-dismissed), `dismissOp`.
    `OutboxOp` gains `applyStatus`/`applyError` + `needsAttention`.
  - **Wire + client:** [models.dart](../mobile/lib/src/api/models.dart) adds
    `SyncOpStatus` (the apply outcome, vs `SyncOpResult` the capture ack) + a
    `SyncResponse{results, statuses}` envelope; [fieldsync_client.dart](../mobile/lib/src/api/fieldsync_client.dart)
    `sync()` now takes an optional `poll:` list + returns `SyncResponse`, and a new
    `syncStatus()` hits `GET /sync/status` (chunked ≤500/req, the server cap).
  - **Store + UI:** [edit_store.dart](../mobile/lib/src/edit/edit_store.dart) mirrors a
    cached `attentionCount` + exposes `inFlightOpIds`/`attentionOps`/`recordApplyStatuses`/
    `dismissAttention`; [pack_map_page.dart](../mobile/lib/src/ui/pack_map_page.dart)
    `_pushOps` piggy-backs the in-flight poll on the first batch + records outcomes
    (snackbar notes rejections), a new `_pollStatuses()` reconciles when there's nothing
    to push (called from `_sync` and, if in-flight ops exist, on pack open); the pending
    sheet gains a red "needs attention" section (per-op error + Acknowledge + tap-to-select)
    and the app bar a red attention badge; [map_settings_screen.dart](../mobile/lib/src/ui/map_settings_screen.dart)
    adds a "Rejected — needs attention" tally (shown only when non-zero).
  - **Dismiss semantics:** acknowledging a rejected op sets `dismissed_at` (drops it off
    the list) but keeps the row as history; the user re-does the edit to submit a fresh
    op (new `op_id`). Fresh-pack reconcile eventually drops the stale row.
  - **Deferred:** actual *retry* of a rejected op (the server doesn't re-apply a terminal
    inbox row) — re-submission is a fresh op today; a create whose op was rejected still
    renders green until reconciled (surfacing-only scope).
- **Slice 3 — review queue + posting (QA mode). CODE-COMPLETE (2026-08-27,
  verification owed.)** Turns on the "system-of-record / QA" personality: the policy
  switch, `pending_review`, the web review queue, `sync:review` / `sync:post` RBAC, and
  the post/reject actions. Split like S1 into a server half (3a) and a web half (3b);
  both ship together (the web queue drives the new API). Backward compatible — an empty
  `ops.sync_policies` table means every op auto-applies, so behaviour is unchanged until
  a class (or the global default) is set to `review`. Shipped:
  - **Server (3a):** migration [`0044_sync_review.sql`](../migrations/0044_sync_review.sql)
    — the `sync:review` / `sync:post` permissions (granted to admin + break-glass-admin,
    per 0040's precedent), the `ops.sync_policies` table (per-class-or-global apply mode;
    empty = built-in `auto`), and a widened `sync_inbox_device_idx` covering
    `pending_review`. [`policy.go`](../api/internal/fieldpack/policy.go) — the
    `SyncPolicy` model + `SyncPolicySet` resolver (`ModeFor` / `InitialStatus`, sentinel
    `'*'` = global default, built-in fallback `auto`) + `load`/`list`/`set`/`delete`
    helpers. [`inbox.go`](../api/internal/fieldpack/inbox.go) — `receiveInboxOps` now
    takes the policy and lands each op `received` **or** `pending_review` by class; the
    `claimInboxOp` per-device single-flight guard now also blocks a later op while an
    **earlier** op of the same device is held `pending_review` (so a connect can't apply
    before the create it references is posted); new `postInboxOp` (pending_review →
    received, stamps reviewer) and `rejectInboxReview` (pending_review → rejected, same
    terminal state the poison-op path uses, so the device surfaces it through the Slice 2
    "needs attention" channel). [`review.go`](../api/internal/fieldpack/review.go) — the
    session-authed `AdminReviewRoutes`: `GET /sync-review` (queue by status), `POST
    /{id}/post`, `POST /{id}/reject`, and `GET`/`PUT`/`DELETE /sync-review/policy`.
    [`sync.go`](../api/internal/fieldpack/sync.go) resolves the policy once per batch and
    passes it to `receiveInboxOps`; the mount lives in
    [cmd/server](../api/cmd/server/main.go) behind a method-aware `sync:review` (GET) /
    `sync:post` (mutations) gate + CSRF, mirroring the field-packs admin surface.
  - **Web (3b):** [`use-sync-review.ts`](../web/src/hooks/use-sync-review.ts) — the
    review + policy queries/mutations (the queue self-polls every 4s while anything is
    pending). [`sync-review.tsx`](../web/src/routes/sync-review.tsx) — the `SyncReviewPage`:
    a status filter (pending / rejected / applied), per-op cards (kind + class + owner,
    expandable to the raw proposed payload) with Post / Reject (a reason dialog) actions,
    and a collapsible "Apply policy" editor (global default + per-class overrides). Wired
    into [app.tsx](../web/src/app.tsx) behind a route-level `sync:review` gate (the action
    controls self-gate on `sync:post`) and the [sidebar](../web/src/components/layout/app-sidebar.tsx).
  - **Deferred:** batch post by `submission_id` (per-op post/reject for now); diffing the
    payload against the current production row (shown raw — full diff is Slice 4's
    conflict work); validating a policy override's class code against real classes (a
    typo is harmless dead config).
- **Slice 4 — conflict detection + resolution.**
  - **Server (4a) — CODE-COMPLETE (2026-08-28, verification owed).** Turns today's
    silent LWW into explicit, resolvable conflicts. Opt-in and backward-compatible
    (default off = Slices 1–3 behaviour). Shipped:
    - Migration [`0045_sync_conflict.sql`](../migrations/0045_sync_conflict.sql):
      `ops.sync_policies.detect_conflicts` (bool, default false — the per-class/global
      opt-in switch, **independent of** `mode`); `ops.sync_inbox.conflict_override`
      (bool — set when a supervisor force-applies a conflict); widen
      `sync_inbox_device_idx` to include `'conflict'` (a held conflict now blocks the
      device's later ops).
    - [`policy.go`](../api/internal/fieldpack/policy.go): `SyncPolicy.DetectConflicts`;
      `SyncPolicySet` gains `detectDef`/`detectByClass` + `DetectFor(class) bool`;
      `loadSyncPolicy`/`listSyncPolicies`/`setSyncPolicy` carry the flag.
    - [`crud/sql.go`](../api/internal/crud/sql.go) `FeatureVersions(ctx,db,loaded,id)`:
      one cheap `created_at,updated_at` read for the version compare.
    - [`syncworker.go`](../api/internal/fieldpack/syncworker.go) `detectSyncConflict`:
      runs before `applySyncOp` for **update_attrs/delete** with detection on and a
      `base_version` set (skipped when `conflict_override`). Conflict test =
      `updated_at > base_version` **and** `created_at <= base_version` (the second
      clause kills the false positive where the device's *own* earlier create bumps
      `updated_at` past the snapshot). A missing row is not a conflict. On divergence:
      if a **later** active op for the same feature is queued → `superseded` (silent);
      else → `conflict` (held). A detection error is non-fatal (log + apply as before).
      Go's `time.Parse(RFC3339,...)` accepts the mobile's fractional-second
      `snapshot_at`, so a millis timestamp still compares.
    - [`inbox.go`](../api/internal/fieldpack/inbox.go): `claimInboxOp` guard adds
      `'conflict'` to the per-device "active/blocking" set (so resolving can't regress a
      newer op that jumped ahead); `markInboxConflict` (applying→conflict, non-terminal,
      awaits resolve), `markInboxSuperseded` (applying→superseded, terminal, silent),
      `hasLaterActiveOpForFeature`, `resolveConflictApply` (conflict→received +
      `conflict_override`, worker re-runs skipping the check), `resolveConflictKeep`
      (conflict→**rejected** — reuses the Slice 2 "needs attention" channel), `getInboxRow`.
    - [`review.go`](../api/internal/fieldpack/review.go): `GET /sync-review/{id}`
      (op + current production row, for the diff), `POST /sync-review/{id}/resolve`
      (`{action:"apply"|"keep", reason?}`, 409 if not in conflict); policy GET/PUT carry
      `detect_conflicts` (+ `default_detect_conflicts`).
  - **Conflict already surfaces on-device with no mobile change** — Slice 2's
    `attentionOps` already covers `conflict`. `superseded` is silent (not needs-attention).
  - **Web (4b) — CODE-COMPLETE (2026-08-28, verification owed).** The reviewer-facing
    conflict-resolution UI, on the existing Sync review page. Shipped:
    - [`use-sync-review.ts`](../web/src/hooks/use-sync-review.ts): `useConflictDetail(opId,
      enabled)` (lazy GET `/sync-review/{id}` — op + current production row, fetched only
      when a conflict card is expanded); `useResolveOp()` (POST `/sync-review/{id}/resolve`
      with `{action:"apply"|"keep", reason}`); `SyncPolicyRow.detect_conflicts`,
      `PolicyResponse.default_detect_conflicts`, and `useSetPolicy` now requires
      `detect_conflicts` (both dimensions sent on every upsert).
    - [`sync-review.tsx`](../web/src/routes/sync-review.tsx): a **Conflicts** status tab;
      per-card Apply-anyway (resolve `apply`) / Keep-production (resolve `keep`, optional
      reason) actions replacing Post/Reject when `status === "conflict"`; a `ConflictDiff`
      in the expanded details (`DiffTable`/`diffFields` — a 2-way "production now vs device
      wants" field diff of name + attributes, geometry flagged; we lack the field snapshot
      for a true 3-way merge); a `DetectToggle` (off/on) added to the policy editor
      (default + per-class rows + add form), independent of the auto/review `ModeToggle`.
      `RejectDialog` generalised to a shared `ReasonDialog` (reject requires a reason;
      keep lets it be blank).
  - **Mobile (4c) — CODE-COMPLETE (2026-08-28, verification owed).** Teaches the
    device that `superseded` is a **terminal, silent** outcome (stop polling, never
    surface). The DB layer already excluded it defensively in Slice 2 — 4c makes the
    contract explicit and drift-proof:
    - [`models.dart`](../mobile/lib/src/api/models.dart) `SyncOpStatus`: new
      `isSuperseded` + `isTerminal` (= applied | rejected/conflict | superseded)
      getters, so poll/attention decisions have a semantic hook instead of scattered
      string checks; doc notes superseded's silence.
    - [`edit_database.dart`](../mobile/lib/src/edit/edit_database.dart): the poll
      whitelist is now a single `static const inFlightApplyStatuses`
      (`received`/`applying`/`pending_review`) that `inFlightOpIds` builds its SQL
      `IN (...)` from — so `applied`/`rejected`/`conflict`/`superseded` **and any
      future/unknown status** fall out of the poll set by construction (no drift).
      `attentionOps`/`needsAttention` docs call out that `superseded` is deliberately
      excluded (a newer edit to the same feature won; the shared overlay already shows
      that newer intent, so there's nothing to act on).
    - [`sync_op_status_test.dart`](../mobile/test/sync_op_status_test.dart): new unit
      test pinning the lifecycle — in-flight stays polled, applied/rejected/conflict/
      superseded are terminal, superseded is terminal-but-silent, an unknown status
      keeps polling (defensive).
  - **Not planned:** field-level merge (apply vs keep only); classifying
    transient-vs-permanent apply failures.

## Decisions (locked before Slice 1)

- **Auto-apply timing in trusted mode — WORKER-IMMEDIATE.** `POST /sync` only receives +
  acks; the drain worker applies moments later (sub-second). Chosen over inline apply for
  the short connection hold on bad cellular and one uniform apply path; the status
  pull-back covers the received→applied gap.
- **Status pull-back shape — BOTH.** The next `POST /sync` piggy-backs current statuses
  for a device-supplied `poll` list of op_ids, **and** a dedicated
  `GET /sync/status?op_ids=…` serves a device with nothing new to push. Both are
  owner-scoped and resolve through the same `getInboxStatuses`. (Server side ships in
  S1; the device's *consumption* of these — the "needs attention" list — is S2.)
- **Policy granularity — SERVER DEFAULT + PER-CLASS OVERRIDE.** One global default
  (trusted / auto-apply) overridable per `feature_class`. In S1 everything is auto-apply,
  so the policy column/lookup is deferred to **Slice 3** (QA mode) where it first has an
  effect; S1 hard-codes auto.

Still open (not blocking S1):

- **Inbox retention.** How long to keep `applied` rows (audit value vs. table growth) —
  a retention sweep mirroring the export-jobs sweep, added in a later slice.

## Related

`docs/phase-21-field-tool.md` (the device outbox + `POST /sync` this builds on),
`docs/phase-17-large-export.md` (the worker + `SKIP LOCKED` queue pattern reused here),
migration `0036_create_export_jobs.sql` (the `ops`-schema queue template).
