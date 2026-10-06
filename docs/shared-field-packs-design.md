# Design — Shared & Pre-Built Field Packs (grid-of-cells)

**Status:** DESIGN — not yet built. This is the spec to review and approve before
any code.

**Extends:** [phase-21-field-tool.md](phase-21-field-tool.md) (the field tool) and
[phase-23-sync-inbox.md](phase-23-sync-inbox.md) (the sync/apply engine). It reuses
the existing `ops.field_packs` build queue, the `cmd/fieldsync` build + sync
workers, the pack builder, and the `base_version`-tagged sync inbox — it does not
replace them.

---

## 1. Why

Today the field tool is **self-service**: each crew member requests their own pack
for an area, it builds on demand, and only they see it. That's right for
unpredictable work, but it has two costs at fleet scale:

- **Redundant CPU.** If 60 crews cover one depot, that's 60 near-identical builds —
  the heaviest, most CPU-intensive work (tippecanoe + the feature read) done 60
  times.
- **No shared, long-lived coverage.** A pack is personal and short-lived; there's no
  notion of "the North region, built once, used by everyone for weeks."

This design adds **shared, pre-built field packs** so an admin can build coverage
**overnight** (when CPU is cheap) that **many crews share and reuse for weeks**,
while keeping every crew's view safe and their personal work separate — and giving
supervisors an account of **who's holding which pack and whether they've synced.**

### Operating constraints that shaped it (decided with the product owner)

- **Crews are never bound to a zone.** A roaming crew must grab coverage freely and
  instantly as it moves; a fixed crew uses the same coverage every day. Both from
  the same mechanism.
- **Freshness isn't critical.** A pack may be used for a month; crews may sync
  weekly (e.g. every Friday). What matters is **accounting**: how many downloads, by
  whom, matched to their syncs.
- **Nothing personal or unpublished may enter a shared artifact** (the safety rule).
- **It's optional.** Zero shared packs is a valid configuration — a fully dynamic
  operation keeps self-serving, unchanged.

---

## 2. The model — three coexisting mechanisms

```
  1. ON-DEMAND PERSONAL PACK        (exists today — UNCHANGED)
       any crew draws any area → built now → one monolithic pack, owner-scoped
       for unpredictable / roaming-into-new-ground work (cost: a build wait)

  2. SHARED CELLS                   (new — the overnight win, OPTIONAL)
       admin defines cells (a grid, or hand-drawn areas) over a territory
       → rebuilt on a schedule → each build = a SHARED BASE (a "cell version")
       SPLIT: shared base (published, non-personal)  +  per-user OVERLAY
       any crew whose access covers a cell pulls its base instantly (pre-built)
       a crew is NOT bound to a cell — it pulls whatever cells it needs, as it moves

  3. ADMIN BUILD-ON-BEHALF          (new — small)
       admin pre-stages a personal pack (mechanism 1) for a named crew member
```

**A cell is a shared cache tile, not an assignment.** A roaming crew pulls the
cell(s) covering where it's going and more as it moves; a fixed crew pulls the same
cell(s). Where coverage is unpredictable, define no cells — those crews use
mechanism 1.

---

## 3. The base + overlay split

A shared cell is delivered to a crew in two layers the device composes:

```
   SHARED BASE  (one per cell version — built once, shared by all holders)
     • features (published only, WKB + attrs)
     • tiles (MBTiles)
     • connectivity + relationships
     • metadata / symbology / domains / icons
     • asset-tag index
     • team read-context: open faults/defects on the cell's assets
     → contains NOTHING personal or unpublished  ← the safety guarantee

   +  PER-USER OVERLAY  (one per (crew, cell) — thin, cheap, refreshed each sync)
     • the crew member's WAM work board (their assigned work orders)
     • the crew member's OWN private drafts in the cell area
     → everything that is person-specific or permission-sensitive
```

**Why the split is the safety model, not just tidiness:** a shared artifact can be
handed to anyone who may see the cell *only because* it holds nothing personal or
draft. Private drafts (Phase 61) and the personal work board move to the overlay,
which is built with the *individual's* identity.

**On the device:** three layers merge at read time — the **shared base** (read-only,
cached for weeks), the **overlay** (per-user, refreshed), and the crew's own
**edits** (`edits.sqlite`, unchanged). Overlay rows win over base; local edits win
over both. The existing read layer already composes "packed truth + local edits";
this adds the overlay as a middle layer.

---

## 4. Data model

### 4.1 Extend `ops.field_packs`

The existing build queue already carries a built pack (scope, object keys,
snapshot_at, counts, status). Add:

| Column | Meaning |
|--------|---------|
| `kind` | `'personal'` (default — today's packs) \| `'shared_base'` (a cell version) |
| `cell_id` | FK to `ops.field_pack_cells`, null for personal packs |
| `is_published_only` | true for a shared base (the builder's draft filter = published) |

A shared base is **built by the same worker** as a personal pack — it's an
`ops.field_packs` row with `kind='shared_base'`, a `cell_id`, and a scope that
excludes personal data. No second build pipeline.

### 4.2 New: `ops.field_pack_cells` — the cell definitions

The admin-defined shareable units (a grid, or hand-drawn areas).

| Column | Meaning |
|--------|---------|
| `id` | cell id |
| `name` | human label (e.g. "North · Zone 1") |
| `territory_id` | the access boundary — a crew must have access to this territory to pull the cell |
| `bbox` | the cell's area (EPSG:4326) |
| `feature_classes` | which classes the base carries |
| `include_connectivity / _relationships / _faults / _tags` | which extras the base carries (WAM/work board is never here — it's per-user overlay) |
| `schedule` | rebuild cadence (cron/interval), null = manual only |
| `retention` | how versions are reaped (see §9) |
| `active`, `created_by`, `created_at` |

A cell is a *definition*; each build produces a **cell version** (an
`ops.field_packs` row). The latest succeeded version for a cell is the one crews
pull.

### 4.3 New: `ops.field_pack_downloads` — the ledger

Every pickup, for the accounting the product owner asked for.

| Column | Meaning |
|--------|---------|
| `id`, `user_id`, `pack_id` (the field_packs version), `cell_id`, `device_id` | who pulled what |
| `downloaded_at` | when |

Reconciliation needs no new table beyond this: `ops.sync_inbox` already carries
**`base_version`** (the snapshot an edit was made against) and `submitted_by`, so a
crew's synced edits already name the pack version they came from.

---

## 5. Build & schedule flow

```
  admin defines a cell  ──►  scheduler (or manual)  ──►  enqueue a build
                                                           (ops.field_packs,
                                                            kind='shared_base',
                                                            cell_id, scope)
                                                                │
                              cmd/fieldsync build worker  ◄─────┘   (overnight)
                                 • published-only feature read
                                 • NO work board (personal → overlay)
                                 • base+extras per the cell's include flags
                                 • reuses the existing builder + #2 scoped graph scan
                                                                │
                                        succeeded cell version  ◄┘
```

- **Reuse the whole builder** (reader/builder, the scoped graph scan, the manifest
  extent, the size cap, the zoom range — all from the scaling work). The only build
  difference is the scope: published-only, no work board.
- **Scheduling:** a cell's `schedule` drives a periodic enqueue (a new goroutine/cron
  in `cmd/fieldsync`, or the platform's scheduler). Overnight is just a cron
  expression. "Zoning not needed" = define no cells, nothing schedules.
- A new cell version **supersedes** the previous one for new pickups; existing
  holders keep their copy (freshness isn't critical) until they choose to refresh.

---

## 6. Pickup & gating (the safety rule)

A crew browses the cells **their access covers** and pulls one (or several adjacent)
instantly — the base is already built.

**The enforced rule (decided: Option A):** a crew may download a cell only if their
own entitlements **cover** the cell's contents:

- they have access to the cell's **territory** (reuse Phase 58 territory scope), AND
- they hold the **read-permissions** for every extra the base carries
  (`connectivity:read`, `faults:read`, `tags:read`, …).

A crew missing a covering permission simply doesn't see that cell (or can't pull
it) — no reduced-content fallback, no leak. On pickup:

1. download the shared base (cached for weeks),
2. build + download the **per-user overlay** (cheap: the crew's work board + their
   private drafts clipped to the cell bbox — reuses `loadPackWorkOrders` and the
   draft-author read), and
3. **record the download** in `ops.field_pack_downloads`.

The overlay refreshes on each sync cycle; the base is re-pulled only when the crew
chooses to move to a newer version.

---

## 7. Sync & reconciliation — the accounting spine

Nothing changes in how edits sync (`POST /sync` → `sync_inbox` → the apply worker),
and edits still enforce the submitting crew's scope on apply. What's new is the
**reporting**:

- Every synced edit carries `base_version` → the cell version it was made against.
- The download ledger says who holds which cell version.
- Join the two for the supervisor view the product owner described:

```
  Cell "North · Zone 1"  (v3, built 3 weeks ago)
    ┌ Crew A — pulled 3w ago — last sync: this Fri        ✓ active
    ├ Crew B — pulled 2w ago — last sync: 2w ago          ⚠ holding, gone quiet
    └ Crew C — pulled 3w ago — never synced               ⚠ chase
```

This also drives retention: a cell version is a candidate for reaping once **no
active holder** references it.

---

## 8. Admin surfaces

Both behind the existing `admin:field-packs` gate (session + RBAC + CSRF), beside
the current `/field-packs` management page:

1. **Cell catalog** — CRUD the cells: define area (draw or a grid helper), classes,
   extras, schedule, retention; trigger an immediate rebuild; see each cell's latest
   version + build status.
2. **Usage dashboard** — the §7 view: per cell, who's holding each version, their
   last sync, and who's gone quiet; plus a "reap old versions" action gated by §9.

A **grid helper** (optional, S7) auto-generates a regular grid of cells over a
territory's bounds, so an admin covers a whole region in one action instead of
hand-drawing dozens of cells.

---

## 9. Retention (configurable — not forced)

A cell version's two RustFS artifacts + its row are reaped **only when you choose**
(decided: "sometimes"). Per-cell `retention` options:

- **manual** — never auto-reap; the admin deletes old versions.
- **keep N** — keep the newest N versions, reap older ones **once no active holder**
  references them (ledger + last-sync check).
- **pinned** — an admin can pin a version so it's never reaped even if idle.

The existing retention sweep / the admin delete path (`reapArtifacts`) is reused.

---

## 10. Device changes

- **Browse & pull cells** — a new "Shared coverage" list beside today's packs:
  the cells the crew's access covers, pullable instantly. Roaming crews pull more
  cells as they move; nothing binds them.
- **Compose base + overlay + edits** — the pack open/query layer gains the overlay
  as a middle layer (overlay > base for work board/drafts; local edits > both).
- **Everything else is unchanged** — view, edit, create, delete, connect, GPS,
  photos, scan, and sync all work exactly as on a personal pack, because after
  composition the device sees one merged dataset.

---

## 11. Permissions

- **Pickup gating** reuses existing perms: territory access (Phase 58) + the per-
  extra `*:read` perms (§6). No new crew permission.
- **Cell administration** reuses `admin:field-packs` (cells are pack management).
- A new `field-packs:usage` read-perm *may* be worth splitting out so a supervisor
  can see the usage dashboard without full pack-admin — open item (§13).

---

## 12. Slice plan (incremental, each shippable)

1. **S1 — cells + manual build.** `ops.field_pack_cells` + the `ops.field_packs`
   `kind`/`cell_id` columns; admin cell CRUD; "build now" → a `shared_base` build
   (published-only, no work board). Proves the shared base end to end.
2. **S2 — the split.** The per-user overlay build (work board + private drafts over
   a cell) + the device composing base + overlay + edits.
3. **S3 — pickup gating + ledger.** The "Shared coverage" browse gated by access
   coverage; `ops.field_pack_downloads` recorded on pull.
4. **S4 — scheduling.** Per-cell `schedule` → periodic rebuilds (overnight).
5. **S5 — reconciliation dashboard.** The §7 usage view (holders × last-sync).
6. **S6 — retention.** The §9 policies + reap action.
7. **S7 — grid helper.** Auto-generate a grid of cells over a territory.
8. **S8 — admin build-on-behalf** (mechanism 3) — a small add to the pack-admin
   surface: enqueue a personal pack *as* a named crew member (scope frozen from
   *their* RBAC — the safety rule).

S1–S3 deliver the core value (shared, pre-built, safely picked up). S4–S7 make it
operational at fleet scale. S8 is independent and can land any time.

---

## 13. Open items / risks

- **Overlay delivery.** Designed here as a cheap per-user *build* at pickup/refresh.
  A live *API fetch* (no artifact) is an alternative if the overlay stays tiny — to
  settle in S2.
- **Mixed-permission crews.** With gating Option A, a cell carrying an extra (e.g.
  faults) is invisible to crews lacking `faults:read`. If that splits a team
  awkwardly, the answer is to define a leaner cell (fewer extras) for that team —
  not to weaken the rule.
- **Freshness / delta.** Accepted as non-critical (month-long packs). If same-day
  accuracy is ever needed, **delta refresh** (features changed since the cell
  version's `snapshot_at`) layers on cleanly — the snapshot is already recorded.
- **Grid sizing.** The grid helper needs a sensible default cell size (feature-count
  aware, under `FIELDSYNC_MAX_FEATURES`); too-large cells hit the cap, too-small
  fragments coverage. Tune from the Phase-0 build-timing data.
- **A `field-packs:usage` perm split** for supervisors who should see the dashboard
  but not administer packs (§11).

---

## 14. What this does NOT change

- On-demand personal packs (mechanism 1) — **unchanged**; zero cells is valid.
- The sync/apply engine, conflict/review, and write-scope enforcement — unchanged.
- The crew experience *after* pickup — identical (one merged dataset).
