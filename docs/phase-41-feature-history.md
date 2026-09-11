# Phase 41 — Per-feature history / audit trail

## Problem

A feature's tables carried only **latest-change** audit columns
(`created_at/by`, `updated_at/by`, migration 0026). An edit overwrote the prior
values in place — there was no way to answer "who changed this, when, and what
did it look like before?" beyond the single most recent write. This phase adds
an **append-only audit trail**: one immutable row per change, for every feature
class, recoverable as a timeline.

## Design decisions (confirmed with the user)

- **App-level capture, not DB triggers.** History is written from the CRUD
  write path (`api/internal/crud`), in the same `bun.IDB` the feature write
  used. When the caller supplies a transaction (the split path, MCP tools,
  field-sync replay), the history row commits atomically with the write; on the
  plain REST path it's a second statement on the same connection. A capture
  failure fails the enclosing write — a silent gap would defeat an audit trail.
- **One shared table**, not a per-class `*_history` table. `feature_class_code +
  feature_id` identify the feature; the full post-change state is stored as a
  **jsonb snapshot** (the same envelope a GET returns), so there's no per-class
  schema and no compiler change.
- **Full-row snapshot** per change (not a field-level diff). Simple, complete,
  and self-describing; the UI can diff two snapshots later if needed.

## Schema — migration 0067

`gis.feature_history`:

| column               | type          | notes                                            |
| -------------------- | ------------- | ------------------------------------------------ |
| `id`                 | bigint IDENTITY PK | append order                                |
| `feature_class_code` | text          | which class                                      |
| `feature_id`         | uuid          | which feature                                    |
| `action`             | text          | create/update/publish/unpublish/delete/restore/purge |
| `actor_id`           | uuid, null    | who; NULL for a system/CLI write                 |
| `at`                 | timestamptz   | default now()                                    |
| `snapshot`           | jsonb         | full feature state after the change              |

Index `feature_history_feature_idx (feature_class_code, feature_id, at DESC)` —
the one access pattern is a single feature's timeline, newest first.

**Deliberately no foreign keys:** `feature_id` spans every `gis.*` feature
table (can't FK to one); a **purge** must not cascade away the history of the
row it removes (the trail outlives the feature); `actor_id` is a soft reference
to `iam.users`, resolved by LEFT JOIN at read time so deleting a user never
erases their history.

## Changes

### Backend (`api/internal/crud`)

- **history.go** (new) — the `featureHistory` bun model; the action verb
  constants; `recordHistory(ctx, db, classCode, id, action, snapshot)` (marshals
  the snapshot, inserts one row, stamps `actor_id` via the same `actorArg(ctx)`
  the `updated_by` column uses); `recordFeatureHistory(ctx, db, loaded, action,
  snapshot)` (reads the id straight out of the formatRow-shaped snapshot);
  `historyEntry` + `listFeatureHistory` (the read query, LEFT JOIN `iam.users`
  for the actor's display name/email, newest first).
- **The five writers each record one row** right before returning their
  `formatRow` snapshot:
  - `insertFeature` → `create` (the idempotent ON CONFLICT DO NOTHING replay
    returns early, so a re-applied sync create records no phantom second create);
  - `updateFeature` → `update`;
  - `setStatus` → `publish` / `unpublish` (by the target status);
  - `setDeletedAt` → `delete` / `restore` (by the bool; the
    already-in-that-state short-circuits in soft/restore mean a repeat records
    nothing);
  - `hardDeleteFeature` → `purge`, snapshotting the **pre-delete** `current`
    (there's no post-write row left), after the DELETE is confirmed.
- **split.go** `updateGeometry` → records an `update` for the original half
  (a split shortens the original line); it writes without a RETURNING, so it
  re-reads the post-update row inside the same transaction for the snapshot. The
  far half's `CreateFeature` already records its own `create`.
- **handler.go** — `GET /{code}/{id}/history` → `historyHandler`, returns
  `{ "history": [...] }`. It never 404s on a purged feature (the history
  outlives the row); `loadOrNotFound` still guards an unknown class.

Because every write funnels through these shared writers, **REST, MCP, and
field-sync all produce audit rows automatically** — no per-caller wiring.

### Frontend

- **use-feature-history.ts** (new) — `useFeatureHistory(code, id)` query on key
  `["feature-history", code, id]` (kept off the `["feature", ...]` key so
  browsing history never disturbs the shared single-feature read). Types the
  `FeatureHistoryEntry` (action, actor_id/name/email, at, snapshot).
- **feature-panel.tsx** — a new **History** tab (clock icon). `HistoryTab`
  lists the timeline newest-first; each `HistoryRow` shows a coloured action
  badge, the actor (name → email → "System" for a CLI/sync/deleted-user write),
  and the timestamp, and expands to reveal the full stored snapshot as JSON.
  Read-only — history is a byproduct of the write path, never edited here.

## No new dependency / no permission change

The History tab rides the existing `features:read` gate on the `/features`
mount. `actor_id` reuses `actorArg(ctx)` exactly as the audit columns do.

## Verification owed (Bash blocked here)

- `go build ./...` / `go vet ./...` (crud package: new file, five writer hooks,
  split hook, new handler/route).
- Migration 0067 applies (`gis.feature_history` + index).
- Web `tsc` / lint / build.
- Manual: create → edit → publish → unpublish → delete → restore → purge a
  feature and confirm one correctly-labelled row appears per step, newest first,
  with the acting user resolved; an idempotent sync-create replay adds no second
  `create`; a split records a `create` (far half) + `update` (original); the
  History tab still loads after a purge (row gone, history remains).

## Deferred / not built

- **Snapshot diffing** ("what changed") — the UI shows the full snapshot; a
  field-level before/after view can diff two adjacent snapshots later.
- **Retention / pruning** of very old history rows — unbounded for now; revisit
  if `gis.feature_history` growth becomes a concern.
- **Restoring a feature to a past snapshot** — the data supports it (the
  snapshot is a full envelope), but a "revert to this version" action is its own
  feature.
