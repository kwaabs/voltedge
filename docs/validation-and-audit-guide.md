# Data Validation & Audit — keeping a 200,000-change network trustworthy

**What this is.** Two capabilities that together answer *"is the data still good, and who changed it?"* without asking anyone to review a mountain of edits by hand:

1. **Continuous validation** — a background worker that re-checks every feature the moment it changes and keeps a small, **self-pruning queue of open data-quality issues** (`ops.validation_issues`). You work the queue, not the whole database. Fixed problems leave the queue on their own.
2. **The activity audit** — a reactive, read-only view over the append-only change log (`gis.feature_history`), sliced **by person and time** instead of by feature: *"every delete last week", "what did Dana change this afternoon."*

They are deliberately **orthogonal**. The queue tells you *what is wrong now*; the activity audit tells you *who did what, when*. One is about correctness, the other about accountability.

> **Two gates.** Working the **issue queue** (view, reconcile, triage) uses the connectivity capability — **`connectivity:read`** to look, **`connectivity:commit`** to reconcile or triage. The **activity audit** is a monitoring view and is gated on **`admin:users`**, the same capability behind user management. Running the worker is an operations task.

---

## 1. The problem: why batch auditing doesn't scale

VoltEdge has always had a whole-database **connectivity audit** — a read-only sweep that finds orphaned connections, orphaned relationships and features that sit on top of each other but aren't wired (`coincident_unconnected`). On a small estate you run it, read the list, fix things. Fine.

Now do the arithmetic of a real deployment:

> **200 users × ~10 edits a day × 10 working days ≈ 20,000 changes a fortnight** — and that climbs with every crew, every import, every sync from the field.

A whole-database audit over that hands back an **unreviewable pile**. Nobody can — or will — read twenty thousand findings. Worse, a static report is **stale the moment it's produced**: half the "problems" are already fixed, and new ones have appeared since. "Audit the whole database and review every change" is not a workflow; it's a wish.

The fix is to stop thinking in **batches** and start thinking in **flow**:

- Don't audit *everything on a schedule*. Validate **each change as it lands**, scoped to just that feature and its neighbours — cheap and immediate.
- Don't accumulate a report. Keep a **living queue of currently-open issues** that **shrinks itself** when problems are fixed, so what a human sees is always small and always current.
- Keep the accountability question **separate and on-demand** — you don't trawl a change log continuously; you *ask* it when something needs explaining.

---

## 2. Three lines of defence (plus the audit)

Data quality isn't one mechanism. It's layered, and most bad data never reaches the queue at all:

| Line | When | What it does | Where |
|---|---|---|---|
| **1 · Prevent** | At write time | Network rules, cardinality, domains and **territory write-scope** reject an invalid write *before it is saved*. | The CRUD write path — always on. |
| **2 · Validate** | Just after a write | The worker re-runs the **scoped** connectivity audit on the changed feature + its neighbourhood and reconciles the result into the queue. | `validationworker` → `ReconcileIssues`. |
| **3 · Triage** | Human, when ready | A person works the **small open queue** — acknowledge, resolve by fixing, or suppress a known-OK finding. | The **Issue queue** tab (Network audit panel). |
| **Audit** | On demand | *Separate axis:* who changed what, when — for accountability, not correctness. | **Admin → Activity audit** (`/admin/activity`). |

Prevention does the heavy lifting: a feature that breaks a network rule or lands outside your territory never gets written, so it never becomes an issue. Validation catches the **relational** problems a single-row write can't see on its own — an orphaned link, two un-wired features sharing a point. Triage is where a human spends their scarce attention, and only on what's genuinely open.

---

## 3. What gets validated

The worker runs the **same connectivity audit** the whole-DB sweep runs, only **scoped to a set of feature ids**. Three finding kinds:

- **`orphaned_connection`** — a row in `runtime.feature_connectivity` whose `from` or `to` feature no longer exists (deleted/purged out from under the link). *Severity: error.*
- **`orphaned_relationship`** — the same, for `runtime.feature_relationships` (origin/destination gone). *Severity: error.*
- **`coincident_unconnected`** — two features whose geometries sit within a small tolerance of each other but are **not** connected — almost always a capture slip (someone drew the second feature *near* the first but never wired them). *Severity: warning.*

### Scoping — the key change

The audit grew a `feature_ids` parameter. Empty = the old whole-database behaviour. Non-empty = **limit every check to connections, relationships and coincident pairs that touch one of those features** (either endpoint qualifies). That one change is what makes per-change validation cheap: when feature *X* is edited, the worker audits *only X's neighbourhood*, not the planet.

```
GET /connectivity/audit?feature_ids=<uuid>,<uuid>
```

returns only findings touching those features, plus `scoped_to_features` in the summary (0 = whole-DB).

> **Why not just validate the one changed feature?** Because the problems are **relational** — a coincident pair or an orphaned link involves *two* features. Scoping to the changed feature **and its connectivity/relationship neighbours** is what lets a scoped run see the whole of each problem it's responsible for.

---

## 4. The self-pruning issue queue

The heart of the system is `ops.validation_issues` (migration **0137**) and the `ReconcileIssues` routine that keeps it honest.

### 4.1 The fingerprint — one issue, one row

Every finding is reduced to a stable **fingerprint**:

- `coincident_unconnected` → `cu:` + the two feature ids **sorted** (so the pair A|B and B|A are the *same* issue, found from either end).
- everything else → `kind` + `:` + the connection/relationship id.

The fingerprint is a **UNIQUE** column. Reconcile **upserts** on it (`ON CONFLICT (fingerprint) DO UPDATE`). So:

- The **first** time a problem is seen, a row is created (`status = open`, `first_seen_at` stamped).
- **Every subsequent** sight of the same problem updates the *existing* row (refreshes `last_seen_at`) — it never duplicates. Run the audit a hundred times; the queue doesn't grow.

### 4.2 Status and severity

| Status | Meaning |
|---|---|
| `open` | Live, unhandled. The default. |
| `acknowledged` | A human has seen it and parked it (still counts as live). |
| `resolved` | Gone — either fixed, or auto-resolved because the finding disappeared. |
| `suppressed` | A human declared it a non-issue; stays out of the live queue even if re-found. |

Severity is `error` or `warning`. The **live queue** is `open + acknowledged`, **errors first**, newest-seen first — so a steward opens the panel and sees the worst, freshest, still-unhandled things at the top.

### 4.3 Auto-resolve — the self-pruning

This is what keeps the queue small **without** anyone clearing stale entries. In one transaction, `ReconcileIssues`:

1. **Upserts** every current finding (re-opening a row that was `resolved` if the problem came back; leaving `acknowledged`/`suppressed` rows in their human-set state; refreshing `last_seen_at`).
2. **Auto-resolves** any `open`/`acknowledged` issue **within the scope** whose fingerprint is **not** in the current findings — i.e. a problem that *used to* be there and is now gone. It gets stamped `resolved`, with **zero human action**.

So the normal lifecycle of an issue is: worker finds it → it appears in the queue → someone fixes the feature → the next scoped sweep doesn't find it → it auto-resolves and leaves the queue. The human only ever touched the *fix*, never the *bookkeeping*.

### 4.4 The truncation guard — why a capped run won't lie

Auto-resolve rests on one assumption: *"I audited this scope completely, so a fingerprint I didn't find is genuinely gone."* If a run hit a findings **cap** and was **truncated**, that assumption breaks — a missing fingerprint might mean *fixed*, or it might mean *the run stopped before reaching it*. Auto-resolving on a truncated run could **falsely close a real, still-open issue**.

> **Guard:** when `report.Summary.Truncated` is true, `ReconcileIssues` **skips auto-resolve entirely** (it still upserts what it found). Scoped per-change runs are tiny and never truncate, so the incremental path always prunes correctly; only a whole-database reconcile that hits the cap declines to prune — and that's the safe failure.

---

## 5. The worker — making it automatic

The clever part of Step 3 is that **no write-path change was needed**. `gis.feature_history` (migration 0067) *already* records exactly one append-only, auto-incrementing row per feature write — create, update, publish, delete, restore, purge, stage, and OMS switching. That log **is** the dirty-feature feed. The worker just **tails** it.

### 5.1 The cursor and the sweep

A one-row `ops.validation_cursor` (migration **0138**) holds `last_history_id`. Each **sweep**:

1. Reads the cursor.
2. `SELECT id, feature_id FROM gis.feature_history WHERE id > cursor ORDER BY id LIMIT batch`.
3. Collects the **distinct** feature ids and the **max** id seen.
4. Runs the **scoped** audit over those ids and `ReconcileIssues` on the same scope.
5. **Advances the cursor forward-only** (`WHERE singleton AND last_history_id < maxID`).

`RunWorker` is a ticker loop: on each tick it **drains the backlog** (sweeps until a sweep returns zero new rows), then sleeps. Each sweep is wrapped in `recover()` — a single pathological history row can't crash the worker.

### 5.2 Operating it

| Knob (env) | Default | What it does |
|---|---|---|
| `VALIDATION_SWEEP_INTERVAL` | `15s` | How often the worker wakes to drain the backlog. |
| `VALIDATION_SWEEP_BATCH` | `500` | History rows processed per sweep. |
| `DATABASE_URL` | — | Standard; same as the other workers. |

Run it like the other workers:

```
DATABASE_URL=… go run ./cmd/validationworker     # or the built binary
```

> **Run exactly one instance.** The sweep is **idempotent** (re-processing a feature is harmless — reconcile just re-affirms), so it needs **no lease and no SKIP LOCKED**; but two instances racing the single cursor row would waste work. One is correct and sufficient.

### 5.3 The one honest gap, and its backstop

`feature_history` ids can **commit slightly out of order** under concurrency (a transaction that got id *N* can commit *after* one that got *N+1*). If the worker's cursor jumps past *N* while *N* was still uncommitted, that one row can be **tail-skipped**. Two things make this a non-problem in practice:

- The **next edit** to that feature re-validates it (a new, higher history row).
- A **periodic whole-database reconcile** — `POST /connectivity/audit/reconcile` with no body — is the backstop that catches anything the tail missed. Run it nightly.

---

## 6. Triage in the app — the Issue queue

The **Network audit** floating panel (on the map) now opens on an **Issue queue** tab, with the old on-demand report moved to a **Run audit** tab beside it. The queue is the primary view because the worker keeps it fresh; the report is the secondary, manual tool.

In the queue tab:

- **Filters** — status (Live = open+acknowledged, or a specific state) and severity.
- **Reconcile now** — a manual whole-database kick (`POST /connectivity/audit/reconcile`) for when you don't want to wait for the worker. Needs **`connectivity:commit`**.
- **Issue rows** — each shows the severity badge, the message, and **two locatable feature chips** (click to zoom the map to the feature), plus triage buttons.

Triage actions (all **`connectivity:commit`**):

- **Acknowledge** — "seen, parked." Stays live.
- **Resolve** — "handled." (Usually you'd fix the feature and let auto-resolve do this; the manual action is for findings you've dispositioned another way.)
- **Suppress** — "this is fine, stop showing me." Survives re-finding.
- **Re-open** — pull a resolved/suppressed issue back to open.

---

## 7. The activity audit — who changed what, when

A completely separate, **reactive** view over the same `gis.feature_history`, this time sliced **by actor and time** rather than by feature. Where a single feature's timeline (its panel's history tab) answers *"what happened to this feature,"* the activity audit answers *"what did this person do,"* across every feature at once.

**Where:** Admin → **Activity audit** (`/admin/activity`). **Gate:** `admin:users`.

> **Why admin:users, not features:read?** The endpoint lives under `/features` (so it inherits `features:read`), but it **escalates in-handler to `admin:users`**. An "aggregate everything a named person did" view is a **monitoring** capability — proper to whoever runs user accounts, not something every map viewer should be able to compile about their colleagues.

### 7.1 What you get

Filter by **person**, **feature class**, **action**, and a **from/to time window**; the result is newest-first, capped at 500 rows. Each row is **who / action / class / feature / when**.

- **The snapshot is deliberately omitted.** An activity log is who-what-when; to see the actual *state* a change produced, drill into that feature's own history (its panel). Keeping the snapshot out keeps the view a fast, scannable ledger.
- **Private-drafts are respected.** The same visibility gate the per-feature timeline uses applies here: a peer's **staging** rows stay inside their own draft chain and never leak into your cross-feature slice. The accountability view can't be used to peek at unpublished private work.

---

## 8. The honest edges

Know these before you lean on the system:

- **Only connectivity/relationship/coincidence is validated.** The queue is about *topology* data-quality. It does **not** validate attribute values, schema conformance, or electrical correctness — those are other mechanisms (domains at write time, the analysis engines).
- **The activity audit covers feature writes only.** `gis.feature_history` records feature changes. It does **not** cover connectivity edits, relationship edits, metadata/schema changes, admin config (basemaps, service layers, import profiles), or user/role/territory changes. Faults, OMS and WAM keep their own histories; sign-in events are in `auth_events`.
- **Out-of-order tail-skip** (§5.3) — mitigated by re-validation + the nightly whole-DB reconcile backstop, not eliminated.
- **The worker is single-instance** by design — don't scale it horizontally.
- **Deployment is owed a compose service.** Add a `validationworker` service to your production compose (mirror `importworker`/`webhookworker`) so it runs alongside the API.

---

## 9. Permissions reference

| Action | Permission | Where |
|---|---|---|
| View the issue queue | `connectivity:read` | Issue queue tab · `GET /connectivity/issues` |
| Reconcile (manual kick) | `connectivity:commit` | Reconcile now · `POST /connectivity/audit/reconcile` |
| Triage an issue (ack/resolve/suppress/re-open) | `connectivity:commit` | `PATCH /connectivity/issues/{id}` |
| Run the on-demand audit | `connectivity:read` | Run audit tab · `GET /connectivity/audit` |
| View the activity audit | `admin:users` | Admin → Activity audit · `GET /features/activity` |

---

## 10. Under the hood — endpoints, migrations, files

**Endpoints**
- `GET /connectivity/audit?feature_ids=<csv>` — scoped (or whole-DB) on-demand audit.
- `POST /connectivity/audit/reconcile` — audit + reconcile into the queue; body `{feature_ids?, tolerance_m?, max_findings?}`; empty body = whole-DB.
- `GET /connectivity/issues?status=&severity=&kind=&limit=` — the queue (default = live, errors-first).
- `PATCH /connectivity/issues/{id}` — `{status}` triage.
- `GET /features/activity?actor_id=&feature_class=&action=&from=&to=&limit=` — the activity audit (RFC3339 times; `admin:users`).

**Migrations**
- `0137_create_validation_issues.sql` — `ops.validation_issues`.
- `0138_create_validation_cursor.sql` — `ops.validation_cursor`.

**Code**
- `api/internal/connectivity/audit.go` — the scoped audit (`featureScopeClause`).
- `api/internal/connectivity/issues.go` — `fingerprintFor`, `ReconcileIssues`, `ListIssues`, `UpdateIssueStatus`.
- `api/internal/connectivity/sweep.go` — `ValidationSweep`, `RunWorker`, config.
- `api/cmd/validationworker/main.go` — the worker entrypoint.
- `api/internal/crud/activity.go` — `listActivity` + the activity log model.
- `web/src/hooks/use-validation-issues.ts`, `web/src/hooks/use-activity.ts` — the client hooks.
- `web/src/components/map/connectivity-audit-panel.tsx` — the Issue queue + Run audit tabs.
- `web/src/routes/admin-activity.tsx` — the Activity audit page.

---

*Companion reading:* **Connectivity & the Network Audit** (the finding kinds and the on-demand sweep), **Access Control** (the permission model and the audit trail), **Logging & Observability** (shipping the worker's logs), and **Production Deployment & Scaling** (wiring the worker into the stack).
