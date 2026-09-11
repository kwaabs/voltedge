# Phase 40 — Optimistic concurrency on feature updates

## Problem

Two users editing the same feature at the same time could silently clobber each
other. The interactive web update path (`UpdateFeature` → `updateFeature`) wrote
with `WHERE id = ?` and no version check — **last-write-wins, silently**. The
loser of the race got no warning that their change was overwritten.

Mitigating factor already in place: the web edit form sends a **diff, not the
whole form** ([feature-edit-form.tsx](../web/src/components/map/feature-edit-form.tsx)
computes `changedKeys`), and each attribute is its own column, so two users
editing *different* fields don't collide. The gap was **same-field** (and
name/geometry) concurrent edits, plus the total absence of any "this changed
under you" signal.

(Note: the *offline mobile sync* path already has conflict detection — Phase 23
sync-inbox. This phase brings the same safety to the interactive web/REST path.)

## Design — optimistic concurrency control (OCC)

The row's existing **`updated_at`** is the concurrency token. Every feature
table already has it and it's stamped `now()` on every write, so there is **no
schema change** and it works uniformly across every compiled feature class.

Flow:

1. The client reads a feature (the detail payload already includes `updated_at`)
   and holds that value.
2. On save, it echoes it back as `expected_updated_at`.
3. The write becomes conditional:
   `UPDATE ... WHERE id = ? AND updated_at = ?::timestamptz`.
4. If someone else wrote in between, `updated_at` no longer matches → **0 rows
   updated** → the server returns **409 Conflict** with a reload-and-reapply
   message, instead of overwriting.

The token is compared as **text cast to `timestamptz`**, so the client just
echoes the exact value it received — whatever precision Postgres stored
round-trips through the cast, sidestepping any JSON timestamp-format concern.

**Opt-in by design.** `expected_updated_at` is optional. Absent → the write is
unconditional (unchanged last-write-wins). So the **MCP `update_feature` tool**
and the **field-sync replay** (`POST /sync`), which call the shared
`UpdateFeature` without the token, are unaffected — the field path deliberately
keeps its own Phase 23 conflict handling rather than 409-ing on a stale offline
token. Only the web edit form sends the token today.

## Changes

### Backend (`api/internal/crud`)

- **validate.go** — new `conflictError` type + `conflict()` + `isConflictError()`
  (a well-formed request that lost a race — distinct from 400 and 404).
- **handler.go** — `writeError` maps `isConflictError` → `http.StatusConflict`;
  `updateHandler` passes `req.ExpectedUpdatedAt` through.
- **types.go** — `updateRequest.ExpectedUpdatedAt *string`
  (`json:"expected_updated_at,omitempty"`).
- **update.go** — `UpdateFeatureInput.ExpectedUpdatedAt` (same tag); `UpdateFeature`
  forwards it to `updateFeature`.
- **sql.go** — `updateFeature` takes `expectedUpdatedAt *string`; when present and
  non-empty it appends `AND updated_at = ?::timestamptz` to the WHERE (args after
  every SET arg, matching bun's placeholder order). On 0 rows **under a guard** it
  returns `conflict(...)` (the row existed moments earlier — the top-of-function
  fetch in `UpdateFeature` confirmed it), otherwise the existing `notFound`.

### Frontend

- **feature-edit-form.tsx** — the PATCH body now includes
  `expected_updated_at: feature.updated_at`. The catch special-cases
  `ApiError.status === 409`: shows the server's reload message **and** invalidates
  `["feature", code, id]` so the panel refreshes to current values, while the form
  keeps the in-progress edits so they can be reviewed and re-saved rather than
  lost.

## No migration / no dep / no MCP change

Uses the existing `updated_at` column; nothing to migrate. `ApiError` already
carried `.status`. MCP and field-sync unchanged (they don't send the token).

## Verification owed (Bash blocked here)

- `go build ./...` / `go vet ./...` (crud package: new error type, threaded param,
  WHERE change).
- Web `tsc` / lint / build.
- Manual, two browser sessions on the same feature:
  - Both edit the **same** field; first save wins, second save → toast "changed by
    someone else…", panel refreshes; re-save then succeeds.
  - Edit **different** fields → both save cleanly (diff + column-level write).
  - A normal single-user edit still saves (token matches).
  - MCP `update_feature` and a field-sync replay still write (no token → no guard).

## Deferred / not built

- **Extending the guard to publish/unpublish and delete.** Those are separate
  endpoints; publish is a near-idempotent toggle and lower-risk. Could carry the
  same token later if concurrent publish-vs-edit proves a problem.
- **A dedicated integer `version` column.** `updated_at` is sufficient and
  schema-free; a monotonic version would only matter if sub-microsecond writes
  ever needed disambiguating.
- **Presence ("Jane is editing this")** — a live indicator is a bigger,
  realtime-transport feature; OCC already turns silent loss into a visible,
  recoverable conflict, which was the actual risk.
