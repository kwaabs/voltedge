# Phase 58 — Spatial access control (user ↔ territory)

## Problem

Authorization today is **permission-string only** (`iam.user_roles` → roles →
`iam.permissions`, gated by `auth.RequirePermission`). A permission is global:
`features:read` lets you read *every* feature, everywhere. There is no row-level,
tenant, or spatial scoping. Meanwhile a rich **territory** model already exists
(`gis.territories`, a self-referential admin-area tree with PostGIS boundaries,
Phase 11) and every geometric feature already carries a nullable `territory_id`
(migration 0028, auto-assigned on write by `territory.Assign`). Nothing links a
**user** to a **territory**, so that column is descriptive, not restrictive.

This phase makes territory membership an access boundary: a user sees and edits
only features inside the territories granted to them (and, up the org tree, to
the people who report to them).

## Locked decisions

1. **Two hierarchies.**
   - The **territory tree** cascades *down*: granting a node grants its whole
     subtree (reuse `territory.DescendantIDs`).
   - The **org/people tree** cascades *up*: a manager inherits the union of every
     grant held by their transitive reports (new `iam.users.manager_id`
     adjacency list).
2. **Grants gate read *and* write**, both spatially scoped.
3. **Revocation = delete a grant row.** No explicit-deny machinery.
4. **Fail closed, with an admin bypass.** With enforcement on, a user with no
   effective territories sees nothing; a holder of the new `territories:unscoped`
   permission bypasses scoping entirely (the predicate is dropped, so admin/
   break-glass reads and tiles stay fast).
5. **"Must not even see them" — enforced at the tile layer via a Go MVT proxy
   (Route A).** Tiles today are served by **Martin directly from Postgres**,
   bypassing the Go API, and Postgres RLS is deliberately unused (superuser
   connection). So feature tiles move behind a session-authenticated Go endpoint
   that runs `ST_AsMVT(… WHERE territory_id::text IN (<set>))`. The territory-boundary
   overlay is not a tile layer: it is fed by `GET /territories/boundaries` (phase 11, Slice H).
6. **Revocation is instant** — the per-user effective set is cached (60 s TTL), and every
   grant/manager write calls `access.InvalidateAll()`, flushing every user's cached set
   (a full flush, not per-user invalidation).

## Effective-territory set

For a user `U`, the allowed territory-id set is one recursive query:

1. Walk **down** the manager tree from `U` → `U` + all transitive reports.
2. Union the territory nodes those users are **directly granted**
   (`iam.user_territories`).
3. Expand that union **down** the territory tree to all descendants
   (`territory.DescendantIDs` semantics).

The feature predicate everywhere is then `territory_id = ANY(<set>)`. A
`territories:unscoped` holder skips the whole computation and matches everything.

Fail-closed detail: features with a **NULL** `territory_id` (created while
enforcement was off, or falling outside every drawn boundary) are invisible to
scoped users and visible only to unscoped users. Flipping enforcement on is
therefore preceded by a `territory.Assign` backfill sweep (S7) plus an admin
"unassigned features" review view.

## Rollout gate

All new read/write/tile scoping is gated on the **existing**
`territory_enforcement_enabled` flag (`iam.system_settings`, migration 0029).
OFF ⇒ today's behavior exactly (no predicate anywhere), so the phase can land
dark and be switched on per-deployment once territories are authored, grants
assigned, and features backfilled.

## Data model (migration 0094)

- **`iam.user_territories(user_id, territory_id, granted_by, granted_at)`**,
  PK `(user_id, territory_id)`. Mirrors `iam.user_roles`. Revoke = `DELETE`.
  FK `user_id` → `iam.users` (ON DELETE CASCADE), `territory_id` →
  `gis.territories` (ON DELETE CASCADE), `granted_by` → `iam.users`
  (ON DELETE SET NULL). Grants a node; descendants expand at read time.
- **`iam.users.manager_id uuid`** — nullable self-ref FK (ON DELETE SET NULL),
  adjacency list like `gis.territories.parent_id`. Cycle-guarded in Go
  (`access.wouldManagerCycle`, mirroring `territory.wouldCreateCycle`).
- **`iam.permissions` key `territories:unscoped`** — the admin bypass, granted
  to `admin` and `break-glass-admin`.

## Go: the `access` package

New `api/internal/access`, dependency-clean (imports neither `territory` nor `auth` --
it queries `gis.territories` directly -- so `auth` can call it without a cycle):

- `models.go` — `UserTerritory` bun model.
- `grants.go` — `Grant`, `Revoke`, `ListForUser`, `SetManager` (+ cycle guard),
  and `EffectiveIDs(ctx, db, userID) ([]string, error)`. The pure DB resolver; the
  unscoped bypass is decided in `auth.loadScopeFields`, not here.
- `cache.go` (S2) — process-local per-user cache of the effective set (and of the
  subordinate set), with `InvalidateAll()`; grant/manager writes call it after the write.

`auth.SessionUser` gains `AllowedTerritories []string`, `Unscoped bool` (and later `Subordinates []string`), loaded in `loadSession` beside `Permissions`. A helper
`(*SessionUser) TerritoryFilter()` returns `(ids []string, unscoped bool)` for
query builders.

## Enforcement points (all flag-gated)

- **Reads** — crud list/query/get, trace, export add
  `AND (<unscoped> OR territory_id = ANY($set))`, mirroring the `territoryIDs`
  filter `export.go` already uses.
- **Writes** — create/update/delete reject when the feature's resolved
  `territory_id` ∉ set (unless unscoped).
- **Tiles (Route A)** — new session-authed `GET /tiles/{code}/{z}/{x}/{y}`
  generic `ST_AsMVT` built from the metadata registry; Vite `/tiles` proxy flips
  Martin → API; compiler emits a btree on `territory_id` per `gis.*` table
  (+ index migration for existing tables).

## Slices

- **S1** — migration 0094 + `access` models + grant CRUD + manager cycle guard
  + `EffectiveIDs` resolver.
- **S2** — session cache (`AllowedTerritories`/`Unscoped` on `SessionUser`) +
  write-time invalidation.
- **S3** — grant/manager management API + web admin UI (assign territories to a
  user, set a manager).
- **S4** — read enforcement (crud list/query/get, trace, export).
- **S5** — write enforcement (create/update/delete membership check).
- **S6** — tile Route A (Go MVT endpoint, Vite proxy flip, `territory_id` index).
- **S7** — backfill sweep + "unassigned features" admin view + flip the flag.
