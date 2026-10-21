# Asset Lifecycle & Aging

Every feature in VoltEdge carries a **lifecycle axis** — where that physical asset
sits in its life, from a line on a plan to a decommissioned relic. It drives three
things: what the live network *is* (planned and retired assets don't conduct), how
the map reads (proposed and gone assets are de-emphasised), and when things need
replacing (the aging report).

This is a different question from the two axes it's easy to confuse it with:

| Axis | Column | Answers | Set by |
|---|---|---|---|
| **Editorial** | `status` | Is this a draft or published? | Publish / unpublish |
| **Availability** | class `status` | Is this *class* still offered? | Admin (class deactivation) |
| **Lifecycle** | `lifecycle_status` | Where is this *asset* in its life? | The Lifecycle control (this guide) |

---

## 1. The lifecycle states

`lifecycle_status` is a system column on every feature table, one of six coded
states, in life order:

| State | Meaning | Operational? |
|---|---|---|
| `PLANNED` | On a plan, not yet built. | No |
| `UNDER_CONSTRUCTION` | Being built. | No |
| `IN_SERVICE` | Live and carrying load. | **Yes** |
| `DERATED` | In service at a reduced rating (still energised). | **Yes** |
| `RETIRED` | Taken out of service, still physically present. | No |
| `REMOVED` | Physically gone (kept as a record). | No |

A new feature defaults to **`IN_SERVICE`** — an asset you've added to the system
is assumed to be in service unless you say otherwise.

Two nullable dates accompany the status:

- **`commissioned_at`** — when it entered service (generalises the old per-class
  `install_date`).
- **`retired_at`** — when it left service.

Moving a feature **into** service auto-stamps `commissioned_at` if it's still
unset; moving it to **retired/removed** auto-stamps `retired_at`. So the common
path is a single status pick — the dates look after themselves.

---

## 2. Changing an asset's lifecycle

On the feature panel, the **Lifecycle** row shows the current state (a green badge
when operational, muted otherwise) with a **Change** menu. Pick the new state and
it's applied immediately.

Lifecycle changes go through a **dedicated path** (`PATCH
/features/{code}/{id}/lifecycle`), not the generic attribute editor — a status
change is a deliberate operational act, so it's kept separate, recorded in the
feature's history, and attributed to you. Agents can do the same through the
`set_feature_lifecycle` tool.

> **It's not the same as deleting.** A `REMOVED` asset is still in the system as a
> record (and can be re-energised by setting it back to `IN_SERVICE`); a deleted
> feature is gone. Use lifecycle for "this asset's life changed", delete for "this
> record shouldn't exist."

---

## 3. The network gate — what conducts

**Only operational assets conduct.** Trace and power flow walk the connectivity
graph, and an edge is only followed when **both** of its endpoints are live
*and* in an operational state (`IN_SERVICE` or `DERATED`). A `PLANNED`,
under-construction, `RETIRED` or `REMOVED` asset is walked neither *through* nor
*into* — exactly the way a soft-deleted feature is already skipped.

This means:

- A planned extension you've drawn and wired doesn't appear in a trace or a load
  flow until you commission it — you model the future without polluting the
  present.
- Retiring a feeder section instantly removes it from analysis; the downstream
  that used to depend on it reads as unfed (which is the point).

The gate is **unconditional** today — there's no "trace the planned network as if
it were built" scenario mode yet. (You can still *select* a non-operational
feature and inspect it; it just doesn't carry a trace onward.)

---

## 4. The map — reading lifecycle at a glance

Non-operational features are **de-emphasised** on the map so the live network
reads clearly:

- **Planned / under construction** → *ghosted*: the normal symbol colour, but
  dashed and semi-transparent ("coming").
- **Retired / removed** → *greyed out* ("gone").

Operational features draw at full strength, exactly as before.

---

## 5. Aging & replacement planning

Admin → **Asset lifecycle** hosts two linked things: the **aging report** and the
**expected-life catalog** it reads.

### 5.1 The expected-life catalog

One entry says how many years a class — or one subtype of it — is expected to last
in service. A wood pole and a steel tower age very differently, so a `subtype_code`
of **NULL** is the class default and a set value overrides it for that subtype.
This is deployment-specific policy (`ops.asset_life_expectancy`), authored by an
admin; nothing ships pre-filled.

### 5.2 The aging report

For every class that has a catalog entry, the report scans the live, operational
features, derives each one's age from `commissioned_at` (falling back to when the
record was created), and buckets it against the expected life:

- **Overdue** — past its expected end of life.
- **Due soon** — within the window (default 2 years) of end of life.
- **OK** — comfortably within life.

The result is ranked **worst-first** — a replacement work-list — with overall and
per-class counts. Agents can pull the same figures through the `asset_life_report`
tool.

> Only assets actually *in service* age toward replacement. Planned ones aren't
> built yet; retired/removed ones are already handled — all are excluded, the same
> operational set the network gate uses.

---

## 6. Permissions reference

| Action | Permission | Where |
|---|---|---|
| Change a feature's lifecycle | `features:write` | Feature panel · `PATCH /features/{code}/{id}/lifecycle` |
| View the aging report / catalog | `features:read` | Admin → Asset lifecycle · `GET /asset-life/report`, `GET /asset-life/catalog` |
| Edit the expected-life catalog | `metadata:write` | Admin → Asset lifecycle · `POST`/`PATCH`/`DELETE /asset-life/catalog` |

---

## 7. Under the hood — endpoints, migrations, files

**Endpoints**
- `PATCH /features/{code}/{id}/lifecycle` — `{lifecycle_status?, commissioned_at?, retired_at?, clear_commissioned_at?, clear_retired_at?}`.
- `GET /asset-life/report?feature_class=&due_within_years=&max_items=` — the aging report.
- `GET/POST /asset-life/catalog`, `PATCH/DELETE /asset-life/catalog/{id}` — the expected-life catalog.

**Migrations**
- the `lifecycle_status` / `commissioned_at` / `retired_at` system columns come from the **compiler** (added on recompile), not a migration.
- `0156_feature_registry_lifecycle.sql` — mirrors `lifecycle_status` into the cross-class registry so the network gate is one index probe.
- `0157_create_asset_life_expectancy.sql` — the expected-life catalog table.

**Code**
- `api/internal/crud/lifecycle.go` — `SetLifecycle` (the transition write) and `LifecycleOperationalClause` (the network-gate predicate).
- `api/internal/compiler/ddl.go`, `compiler.go` — the three system columns and their backfill.
- `api/internal/trace/graph.go`, `api/internal/powerflow/graph.go`, `nodal.go` — the conduction queries that enforce the gate.
- `api/internal/assetlife/` — the expected-life catalog and the aging report engine.
- `web/src/lib/map-layers.ts` — the lifecycle de-emphasis styling.
- `web/src/routes/admin-asset-life.tsx`, `web/src/hooks/use-asset-life.ts` — the admin page and its client.

*Companion reading:* **Connectivity & the Network Audit** (what conducts), **Engineering Studies** (trace & power flow), and **Bill of Quantities** (asset valuation, the other per-asset rollup).
