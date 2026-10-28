# VoltEdge Field Tool — Admin Guide

_Last updated: 2026-10-28_

**Who this is for:** administrators and supervisors who **set up, manage, review,
and scale** the field tool — not the crews using it. If you're a crew member, read
the **Field Tool Guide** instead.

This guide assumes you work in the VoltEdge **web app** (cookie session + RBAC) and
can reach the server environment. It covers five jobs:

1. **Giving crews access** — logins and what a pack is allowed to contain.
2. **Managing packs** — listing, hiding, and deleting everyone's packs.
3. **Reviewing field work** — the QA queue: holding, posting, rejecting, and
   resolving conflicts on incoming edits.
4. **Tuning and scaling** — the build/sync knobs, and what to provision for a fleet.
5. **Pre-building shared packs** — defining reusable coverage *cells* so many crews
   share one pre-built area, with scheduling, retention, and usage accounting.

---

## 1. How the pieces fit together

```
   FIELD DEVICES                 SERVERS                              STORAGE
  ┌──────────────┐    download  ┌──────────────────────────┐        ┌──────────┐
  │  Field app   │ ◄──────────► │  cmd/fieldsync            │ ◄────► │  RustFS   │
  │  (crews)     │    + sync    │  • pack RECEIVER (HTTP)   │        │ (pack     │
  └──────────────┘              │  • pack BUILD worker      │        │  files)   │
                                │  • sync-APPLY worker      │        └──────────┘
   ADMIN / SUPERVISOR           └──────────────┬───────────┘             ▲
  ┌──────────────┐    manage &                 │                         │
  │  Web app     │ ◄─ review ──►┌──────────────┴───────────┐             │
  │  (you)       │              │  cmd/server (main API)    │ ◄───────────┘
  └──────────────┘              │  • /field-packs admin     │
                                │  • /sync-review queue     │        ┌──────────┐
                                └──────────────┬───────────┘        │ Postgres  │
                                               └───────────────────►│ (the      │
                                                                     │  truth)   │
                                                                     └──────────┘
```

Two server processes matter:

- **`cmd/fieldsync`** — the field engine. One process does three things: receives a
  device's edits, **builds packs** (tippecanoe + SQLite), and **applies** synced
  edits to the live database. This is the process you **scale** (§4).
- **`cmd/server`** — the main web API. It hosts the **admin pack-management** and
  **sync-review** surfaces you use (both behind your web session + RBAC).

Everything a crew does flows through `cmd/fieldsync`; everything *you* do flows
through `cmd/server`.

---

## 2. Giving crews access

### 2.1 Signing crews in

Crews sign in with **email + password** on the app's **Connection** screen (which
exchanges them for a long-lived device token behind the scenes — they never handle
a token). For this to work a crew member needs:

- a **user account** (created the normal way in the web app), and
- the permissions below.

> There is also a lower-level path — minting an API token directly
> (`task authadmin-mint-token EMAIL=… NAME=field-tablet`) and pasting it — but the
> email/password login is the normal route.

### 2.2 The permissions that gate the field tool

A crew member's RBAC decides both *whether* they can use the field tool and *what
their packs contain*:

| Permission | Effect |
|------------|--------|
| `features:read` | Required to **download packs** and read features offline |
| `features:write` | Required to **sync edits** back (a read-only crew can pull but not push) |
| `connectivity:read` | The pack ships the **connectivity graph** (offline links) |
| `relationships:read` | The pack ships **relationships** |
| `wam:read` | The pack ships the crew's **work board** (work orders, defects, inspection checklists) |
| `faults:read` | The pack ships the open **fault board** |
| `tags:read` | The pack ships the **asset-tag index** (offline tag scanning) |
| `attachments:read/write` | View / capture **photos** |

These are **frozen into the pack when it's built**, from the requester's RBAC at
that moment. So if a crew's pack is missing a work board or won't let them connect
features, check their permissions — then they must **rebuild** the pack to pick up
the change.

### 2.3 Territory scope

If you use spatial access control (territories), a scoped crew member's packs and
their synced edits are confined to their territories, exactly as in the web app.
Their offline edits re-enforce that scope on the server when applied — an edit
outside their territory is refused on sync.

### 2.4 The admin permissions you need

To do the admin jobs in this guide:

| Permission | Lets you |
|------------|----------|
| `admin:field-packs` | Manage **everyone's** packs (list / disable / delete) |
| `sync:review` | **View** the sync-review queue |
| `sync:post` | **Act** on held edits (post / reject / resolve conflicts, set policy) |
| `field:tracks:read` | **View** field workers' movement trails and located edits (the Field trails map, §5.6) |

---

## 3. Managing packs — the Field Packs admin page

Open **Field Packs** in the web app (the admin surface at `/field-packs`, gated on
`admin:field-packs`). Unlike the crew view — which only shows a crew their *own*
packs — this lists **every user's** packs, newest first, with the owner, the area
(bbox + classes), status, feature count, and artifact sizes.

Three actions:

| Action | What it does | Reversible? |
|--------|--------------|-------------|
| **Disable** | Hides the pack from its owner's device list (sets a flag). The files stay in storage. Use it to stop a stale pack crowding a tablet. | **Yes** — Enable brings it back |
| **Enable** | Un-hides a disabled pack | — |
| **Delete** | Permanently removes both pack files from storage **and** the record | **No** — permanent |

> **Disable vs. Delete.** Disable is the everyday tool — it declutters a crew's
> device without destroying anything. Delete is for reclaiming storage from packs
> you're certain are finished with; it reaps the (potentially tens-of-MB) files.

Deleting a pack that's still **building** just removes the record; if the build
later finishes, its upload is left for the storage retention sweep to clean up.

---

## 4. Tuning and scaling

This is the operational heart of running the field tool for more than a handful of
crews. There are two workloads — **building packs** (download surge, usually
shift-start) and **applying synced edits** (upload surge, usually shift-end) — and
both run in `cmd/fieldsync`.

### 4.1 The knobs

All are environment variables on the `cmd/fieldsync` process.

**Pack build (download side)**

| Variable | Default | What it controls |
|----------|---------|------------------|
| `FIELDSYNC_MAX_FEATURES` | `200000` | Hard cap on features per pack. A bigger request **fails loudly** ("narrow the bbox or class set") rather than building a giant pack that's slow to build, huge to download, and risky to open. |
| `FIELDPACK_MIN_ZOOM` | `8` | Lowest map zoom in the pack. Keep it low — below it, the crew's map goes **blank** when zoomed out to navigate. |
| `FIELDPACK_MAX_ZOOM` | `16` | Highest tile zoom. **16 (~2.4 m/pixel)** separates typical utility features; the app lets crews zoom *past* it (the tiles scale up) and taps still hit full-precision data. Each level higher ~4×'s build cost and pack size. Only raise to `17` for dense urban LV where service connections are 2–5 m apart. **Do not lower it** — that stacks/hides dense features. |
| `FIELDSYNC_WORKER_CONCURRENCY` | `2` | How many packs build at once in this process. tippecanoe is CPU-heavy, so size it to available cores. |
| `FIELDSYNC_JOB_LEASE` | `60s` | How long a build can run before a crashed worker's job becomes reclaimable. |
| `TIPPECANOE_BIN` | `tippecanoe` | Path to the tile builder (the compose image bundles it). |

**Sync apply (upload side)**

| Variable | Default | What it controls |
|----------|---------|------------------|
| `SYNC_WORKER_CONCURRENCY` | `2` | How many synced edits apply at once. Edits from **one device** always apply in order; different devices parallelize up to this number. |
| `SYNC_JOB_LEASE` | `60s` | Lease before a stuck apply is reclaimed. |

### 4.2 Scaling for a fleet

The shipped defaults (one `cmd/fieldsync`, concurrency 2 each) are fine for a small
team but **not** for a large fleet syncing at once. For a fleet (e.g. hundreds of
crews, many concurrent at shift change):

1. **Put PgBouncer in front of Postgres.** At high concurrency the single biggest
   limit is database connections; transaction pooling is effectively mandatory.
   (See the Production Deployment & Scaling guide.)
2. **Raise the concurrencies and run replicas.** Both workers claim work with
   `FOR UPDATE SKIP LOCKED`, so you can run **several `cmd/fieldsync` replicas**
   safely; they share the build and sync queues. Size build concurrency to CPU
   cores (tippecanoe) and sync concurrency to your PgBouncer pool.
3. **If download and upload surges overlap** (staggered or round-the-clock shifts),
   consider a **read replica for pack builds** — the build is read-only and the
   heaviest DB consumer, so moving it off the primary protects the sync/edit path.
4. **For a geographically clustered fleet**, the biggest lever is **not** building
   the same area many times. If 60 crews cover one depot, that's 60 identical
   builds. **Shared packs (§6) solve this directly:** pre-build a depot's area once
   as a *cell* (overnight, off-peak), and every crew covering it **picks up the same
   build** instead of each triggering their own. Pair it with the review/usage tools
   to see who's holding each build and who's synced.

### 4.2a Worked sizing example (do the arithmetic before shift start)

Build capacity is simple division, so you can predict the shift-start queue before it
happens. Let *N* = packs requested in the surge, *t* = seconds one build takes (read it
from the `elapsed_ms` the worker logs — the 40 s below is only an example), *c* =
`FIELDSYNC_WORKER_CONCURRENCY`, and *r* = number of `cmd/fieldsync` replicas.

```
   queue drains in  ≈  N × t / (c × r)
   120 crews × 40 s / (2 × 1)  =  2 400 s  ≈ 40 min   ← the last crew waits 40 minutes
   120 crews × 40 s / (4 × 3)  =    400 s  ≈  7 min   ← 3 replicas, concurrency 4
```

Two things cap how far you can push this. **CPU:** tippecanoe is CPU-bound, so
`c × r` beyond your total core count only makes each build slower. **Database
connections:** every in-flight build and apply holds a connection, so `c × r` must fit
inside the PgBouncer pool. And the cheapest lever of all is to **reduce *N***: if 60 of
those 120 crews cover one depot, a pre-built shared cell (§6) turns 60 builds into 1.

Pack size has its own multiplier: each extra zoom level in `FIELDPACK_MAX_ZOOM` costs
roughly 4× the build time and pack size, which is why the guide tells you not to
raise it casually.

### 4.3 Reading the signals

- Every successful build logs its duration (`elapsed_ms`) and counts. Watch these
  to size build concurrency and spot packs that are too large.
- A build queue that's draining slowly at shift-start → raise build concurrency /
  add a replica / trim `FIELDPACK_MAX_ZOOM` or the class set.
- Synced edits taking minutes to show up in the office → raise sync concurrency /
  add a replica.

> **The one rule:** don't raise the concurrencies without PgBouncer in front of the
> database. More workers hammering an unpooled Postgres makes things worse, not
> better.

---

## 5. Reviewing field work — QA mode

By default every synced edit **applies automatically** moments after it's captured.
For classes where you want a human to check field edits before they hit the live
network, switch that class (or everything) to **review mode**. Then those edits are
**held**, and a supervisor posts or rejects each one.

This is the **Sync Review** surface in the web app (`/sync-review`), gated on
`sync:review` to view and `sync:post` to act.

### 5.1 Setting the apply policy

The **apply policy** decides, per feature class, whether synced edits auto-apply or
wait for review:

| Mode | Behaviour |
|------|-----------|
| **auto** | Edits apply automatically (the default) |
| **review** | Edits are **held** until a supervisor posts them |

You can set a **global default** and **per-class overrides**. For example: global
`auto`, but `MV_CONDUCTOR` set to `review` so conductor edits get a second pair of
eyes. Clearing an override reverts that class to the global default.

There's a companion setting, **conflict detection**, per class: with it on, an edit
whose feature was changed in the office *after* the crew's pack was built is held as
a **conflict** rather than silently overwriting (see §5.3).

### 5.2 The review queue — post or reject

Open **Sync Review**. The default view is the **pending review** queue, oldest
first (that's also the order edits will apply in). For each held edit you see who
submitted it, the feature and class, and the proposed change. For each one:

```
   held edit ──► review it ──► ┌─ POST   → released to apply (enters the normal queue)
                               └─ REJECT → quarantined; the crew sees it in
                                           "needs attention" with your reason
```

- **Post** releases the edit — the worker applies it in order like any auto edit.
- **Reject** quarantines it with a reason you provide; the crew sees it on their
  device's **needs attention** list and can re-do the edit.

You can also filter the queue to **applied**, **rejected**, or **conflict** to
audit what's already been decided.

### 5.3 Resolving conflicts

A **conflict** means: the crew edited a feature offline, but the office changed the
*same* feature after the crew's pack was built. Applying the crew's edit would
overwrite the office change — so it's held. Open the conflict to see the crew's
proposed change **against the current live value**, then:

| Resolve as | Result |
|------------|--------|
| **Apply anyway** | The crew's edit is applied, overwriting the current value |
| **Keep** | The crew's edit is discarded; the live value stays. The crew sees it rejected in "needs attention" with your reason. |

> Conflicts only happen for classes with **conflict detection** turned on. Leave it
> off and the system is last-write-wins (the crew's edit silently overwrites) — fine
> for capture-oriented fields, risky for contended ones.

### 5.4 How held edits queue — one device, one lane

Edits from one device always apply **in the order the device captured them**. The
worker enforces this with a per-device single-flight rule: it will not hand out an edit
while an **earlier** edit from the **same device** is still *active*. Active means:
`received` (waiting), `applying` (a worker holds a live lease), **`pending_review`**
(waiting for a post) or **`conflict`** (waiting for a resolution).

The consequence catches administrators out: **a held edit blocks every later edit from
that device — even edits to classes that are set to `auto`.** If a crew's first edit is
on a `review` class and nobody posts it, their next ten edits (on any class) sit
`received` behind it, and nothing from that tablet reaches the office. The same is true
of an unresolved conflict. This is deliberate — it stops a later edit overtaking an
earlier one and being regressed when the earlier one is finally posted or force-applied.

| Edit status | Meaning | Blocks the device's later edits? |
|-------------|---------|-----------------------------------|
| `received` | Captured, waiting for a worker | Yes (it is earlier and not yet applied) |
| `applying` | A worker holds it; the lease is live | Yes — until the lease lapses (then it is reclaimable) |
| `pending_review` | Held by a `review` policy; awaits Post/Reject | **Yes** |
| `conflict` | Held because the office changed the feature; awaits Apply anyway/Keep | **Yes** |
| `applied` | Done | No |
| `rejected` | A reviewer (or an apply failure) refused it; the crew sees it in *needs attention* | No — one rejected edit never stalls the device |
| `superseded` | A newer edit to the same feature stepped in front of it | No |

So the practical rule for a supervisor is: **clear a crew's oldest held edit first** —
the queue shows oldest first for exactly this reason — and treat "a crew says none of
their edits arrived" as "find their oldest held edit".

### 5.5 What a conflict check actually compares

Conflict detection is more specific than "the office changed it". When it is turned on
for a class, the worker checks each **update** or **delete** edit just before applying
it:

- It compares the feature's **current live version** against the **snapshot of the pack
  the crew made the edit against** (`base_version`, stamped on every edit by the
  device). If the live row was modified *after* that snapshot, the edit is held as a
  `conflict`, with a message naming both times.
- **Creates are never checked** — a create mints a new row, so there is nothing to
  conflict with.
- **An edit that carries no `base_version` is not checked** (older edits made before the
  stamp existed).
- If the crew edited the same feature several times offline, only the **newest** edit
  surfaces as the conflict; the older ones are marked `superseded` and step aside
  silently, so you resolve one conflict per feature, not one per keystroke.
- **Apply anyway** re-queues the edit with the check waived (the human has decided);
  **Keep** discards it and tells the crew why.
- If the check itself errors (a database hiccup), the worker logs it and **applies the
  edit anyway** — conflict detection fails open, so do not treat it as a hard guarantee
  on a contended class.

### 5.6 Field trails — where the work was done

§5.1–5.5 review **what** a crew changed. The **field trail** reviews **where** they
were while they did it — so you can answer the questions a desk review can't: *were
they actually at the site? how did they get there? where did they pass?*

**What the device records.** While a crew has a pack open, the field app quietly
records two things off the GPS, with **no action needed from the crew**:

- a **movement trail** — a breadcrumb every ~20 m (or every 30 s), the path they walked
  or drove through the session; and
- a **located edit** on **every** save / update / delete / connect / relate — the exact
  spot the worker was standing when they made that change.

The located edits ride the **same durable sync channel as the edits themselves**, so a
change made deep offline keeps its location and never loses it; the trail uploads on the
next sync. Recording is **foreground-only** — it runs while the app is open, and does
**not** use a background-location permission — so a phone pocketed with the app
backgrounded leaves a gap in the trail (the edits are still located).

**Seeing it — the Field trails map.** Open **Field trails** in the web app
(`/field-tracks`), gated on **`field:tracks:read`**. Pick a **worker**, narrow by
**date**, and choose a **session**: the map draws the trail as a line (green start, blue
end) with the located edits as pins — **amber** for a create/update, **red** for a
delete, labelled by action. Each edit lists below the map; **Locate on map** jumps to
that feature so you can cross-check the change against where it was made.

**Access & privacy.** `field:tracks:read` is a **supervisor** capability — granted to
`admin` and `break-glass-admin` out of the box; grant it to your supervisor roles in
**Admin → Users / roles**. A crew **records its own trail** under its ordinary
`features:write` (no extra grant), but **seeing another worker's trail is the privileged
act** this permission gates. The app shows crews **no in-app notice** that they're
recorded, so **tell your crews** that fieldwork location is logged — some jurisdictions
require you to disclose staff location tracking.

**Where it lives.** The trail is `ops.field_track_points`; the located edits are
`ops.field_action_waypoints` (one row per edit, keyed by its sync op). Both are append-
only audit data, kept independent of the edits they describe.

> **Known gaps (today).** Offline **WAM** (defect/work-order/inspection) and **fault**
> captures are not yet location-stamped, so those specific actions won't appear as pins
> (the GIS edits — create/update/delete/connect/relate — do). And the trail is
> foreground-only, so expect gaps when the phone is pocketed mid-walk.

---

## 6. Pre-building shared packs

For a clustered fleet, the biggest waste is many crews each building the **same**
area. Shared packs fix that: you pre-build an area **once** as a reusable **cell**,
and every crew whose access covers it **picks up that single build** — instantly, no
build wait — and reuses it for as long as the job runs.

This is optional. Where coverage is unpredictable (roaming crews into new ground),
define no cells and crews keep self-serving their own packs (§3) exactly as before.
The two mechanisms coexist; use shared packs where coverage is predictable enough to
pre-build.

> **How it's managed today.** Cells and their usage are driven through the Field
> Packs **admin API** (the `/field-packs/cells…` endpoints under cmd/server, gated on
> `admin:field-packs` + CSRF, same as the pack-management page). A web UI for cells is
> planned; until then these are operator/API actions. The endpoint for each action is
> noted below.

### Creating a pack, as an admin — which route?

Unlike a crew (who builds their own pack on the device — see the Field Tool Guide,
*Requesting a new pack*), an admin doesn't build ad-hoc personal packs from a button.
There are three routes, depending on **who** the pack is for:

| You want to… | Use | How |
|--------------|-----|-----|
| Give **many crews** one reusable area | A **shared cell** (§6.2) | Define a cell → **build a version** → crews **pick it up** |
| Cover a **big territory** in sub-areas | The **grid helper** (§6.3) | One call makes a whole grid of cells |
| Stage a **personal** pack for **one crew member** | **Build-on-behalf** (§6.7) | Build a pack owned by them, scoped from their RBAC |

**The common case — build a shared pack (3 steps):**

1. **Define the cell** — `POST /field-packs/cells` with the area (bbox), the feature
   classes, the extras to include, and (optionally) the territory, schedule, and
   retention (§6.2).
2. **Build a version** — `POST /field-packs/cells/{id}/build` (or let a **schedule**
   build it overnight, §6.4). It runs through the normal pack queue; watch the cell's
   latest version go `queued → running → succeeded`.
3. **Crews pick it up** — anyone whose access covers the cell now sees it under
   **Shared packs** on their device and downloads it instantly (§6.1).

> **Just want a pack for yourself?** You're a user too — build one from the **mobile
> app** exactly as a crew does, or use **build-on-behalf** (§6.7) with your own user
> id. There's no separate "admin builds a personal pack" surface, by design.

### 6.1 How shared packs work — base + overlay

A shared pack is deliberately split so it can be shared **safely**:

```
   SHARED BASE (one build, many crews)        PER-USER OVERLAY (each crew, live)
  ┌────────────────────────────────┐         ┌────────────────────────────────┐
  │  Published features + map tiles │    +    │  This crew's work board         │
  │  Connectivity / relationships   │         │  This crew's OWN draft features │
  │  Open faults, asset tags        │         │                                 │
  │  NOTHING personal/unpublished   │         │  (fetched at pickup + each sync) │
  └────────────────────────────────┘         └────────────────────────────────┘
                     └──────────────┬──────────────────┘
                        the device composes BASE + OVERLAY + the crew's own edits
```

- **The base** is **published-only and crew-neutral** — it contains no one's
  unpublished drafts and no personal work board. That's the safety guarantee that
  lets one build be handed to many crews.
- **The overlay** is each crew's thin personal layer — their assigned work board and
  their own drafts over the area — delivered when they pick up and refreshed each
  sync.

**Pickup gating.** A crew may pick up a cell **only if their entitlements cover it**:
their **territory access** must include the cell's territory, **and** they must hold
the `*:read` permission for every extra the base carries (connectivity, relationships,
faults, tags). A crew that doesn't fully cover a cell never sees it in their list and
can't pull it — there's no reduced fallback. If a crew has mixed permissions, define a
leaner cell rather than weakening the rule.

**The gate, check by check.** Pickup runs these in order and reports the *first*
missing entitlement (so the device can say why a cell is unavailable):

1. `connectivity:read`, if the base includes connectivity
2. `relationships:read`, if it includes relationships
3. `faults:read`, if it includes faults
4. `tags:read`, if it includes tags
5. **Territory** — checked **only when territory enforcement is switched on *and* the
   crew member is scoped**. If enforcement is off, or they are *unscoped*
   (`territories:unscoped`), the territory test is skipped. Otherwise the cell's
   territory must be inside the crew member's accessible set (which already includes
   sub-territories), and a cell with **no** territory is refused to every scoped crew
   (fail closed).

What the crew sees: the browse list shows only **active** cells they cover. A cell that
has **never produced a succeeded build** still lists, marked *not yet available*, so the
crew knows it is coming; a direct pickup attempt on a cell they do not cover is refused
`403`. The device may send a `device_id` with each pickup so one person's two tablets are
told apart in the usage view.

### 6.2 Defining a cell

A **cell** is a definition (create with `POST /field-packs/cells`). Its settings:

| Setting | What it does |
|---------|--------------|
| **Name** | A label crews see in their Shared packs list. |
| **Territory** | The pickup access boundary **and** a content filter: the base carries only this territory's features (even where the bbox spills past it), so a crew gated on the territory never receives out-of-area data. Omit it only for a truly public baseline (then only unscoped crews can pull it). |
| **Area (bbox)** | The geographic box the base covers. |
| **Feature classes** | Which layers the base ships (same catalogue as a normal pack). |
| **Include** connectivity / relationships / faults / tags | The extras the base carries. Each one also **raises the bar for pickup** — a crew must hold the matching `*:read`. |
| **Schedule** | Automatic rebuild cadence — see §6.4. |
| **Retention** | How old versions are reaped — see §6.5. |
| **Active** | Inactive cells are hidden from crews and skipped by scheduling/retention. |

**Building a version.** Each build of a cell is a **version** (an ordinary pack row,
built by the same worker). Trigger one with `POST /field-packs/cells/{id}/build`, or
let the schedule do it. Crews always pick up the **newest succeeded** version.

> The base is built **published-only**, so it never carries anyone's drafts or work
> board — those ride the per-user overlay instead. You don't configure that; it's how
> a cell build differs from a personal pack.

**A cell, as JSON.** Required: `name`, a four-number `bbox` (`[minX, minY, maxX, maxY]`,
longitude/latitude order) and at least one entry in `feature_classes`. Everything else
is optional (`active` defaults to true; `retention` defaults to `manual`):

```json
POST /field-packs/cells
{
  "name": "Oakwood depot",
  "territory_id": "7b0c…",                 // omit only for a public baseline
  "bbox": [-0.2410, 5.5520, -0.1880, 5.6010],
  "feature_classes": ["MV_CONDUCTOR", "DIST_XFMR", "RMU", "SWITCH"],
  "include_connectivity": true,
  "include_relationships": false,
  "include_faults": true,
  "include_tags": false,
  "schedule": "daily",                       // hourly | daily | weekly | omit
  "retention": "keep_n"                       // manual | pinned | keep_n
}
```

The reply (`201`) echoes the cell with its `id`. `GET /field-packs/cells` lists every
cell with its **latest version** (pack id, status, feature count, data and tile bytes,
snapshot time) — that is where you read how big a build came out. A bad body returns
`400` with the first problem named (for example *"bbox must be [minX, minY, maxX, maxY]
(4 numbers)"*).

### 6.3 Covering a big area — the grid helper

Defining a cell per sub-area of a large territory by hand is tedious. The **grid
helper** (`POST /field-packs/cells/grid`) splits an outer bbox into a `rows × cols`
set of cells in one call, each a real cell sharing one template (classes, includes,
territory, schedule, retention). You give either explicit `rows`/`cols` or a
`cell_size_deg` it derives them from; it caps a single request at 500 cells and
creates them **atomically** (all or none).

> **Size each sub-cell under the feature cap.** A sub-cell whose base would exceed
> `FIELDSYNC_MAX_FEATURES` fails its build loudly — that's your signal to regrid
> finer (smaller `cell_size_deg` / more rows+cols).

**Worked grid example.** Outer bbox `[-0.30, 5.50, -0.10, 5.70]` is 0.20° × 0.20°.

```
cell_size_deg 0.05  →  cols = ceil(0.20/0.05) = 4,  rows = ceil(0.20/0.05) = 4  →  16 cells
cell_size_deg 0.01  →  20 × 20 = 400 cells   (allowed)
cell_size_deg 0.005 →  40 × 40 = 1 600 cells (refused: "grid would be 1600 cells (max 500)")
```

Cells are named `<name_prefix> r<row>c<col>`, with **row 1 at the bottom** (minimum Y)
and column 1 at the left. The grid is split into equal slices, and the last row and
column are snapped to the outer edge so floating-point drift never leaves a sliver
uncovered. If a territory is supplied it is applied to **every** sub-cell; each
sub-cell's base then carries only that territory's features inside its own box. Because
`derived rows/cols = ceil(size ÷ cell_size_deg)`, a cell size that does not divide the
box evenly still covers it fully (the cells are just slightly smaller than asked).

### 6.4 Scheduling overnight rebuilds

A cell's **schedule** is a cadence token — `hourly`, `daily`, or `weekly` (anything
else, or empty, means no schedule). A scheduled cell is automatically rebuilt when its
newest version is older than the cadence, so crews keep getting a fresh base without
anyone clicking build.

To keep the heavy builds off peak hours, confine them to a **build window**:

| Variable (`cmd/fieldsync`) | Default | What it controls |
|----------------------------|---------|------------------|
| `FIELDSYNC_BUILD_WINDOW` | *(unset)* | UTC hour window `start-end` (e.g. `0-5`) scheduled builds are allowed in. Wraps midnight (`22-5` works). Unset = any hour. |
| `FIELDSYNC_MAINTENANCE_INTERVAL` | `15m` | How often the maintenance loop checks for due rebuilds and runs retention. |

A maintenance loop inside `cmd/fieldsync` does this on a timer; it's self-skipping (a
cell with a build already in flight isn't re-enqueued), so it's safe across replicas.

**Exactly when does a scheduled rebuild fire?** The maintenance loop ticks every
`FIELDSYNC_MAINTENANCE_INTERVAL` (and once at start-up). On a tick it considers each
**active, scheduled** cell and enqueues a build only if **both** hold: no build is
already `queued` or `running` for it (so a slow build never stacks duplicates), **and**
its newest **succeeded** version is older than the cadence (or it has never built). Notes:

- The window is **hours `[start, end)` in UTC**: `0-5` allows ticks at 00:xx–04:xx, not
  05:xx. `22-5` wraps midnight. A malformed value is ignored and treated as *no window*
  (any hour) — check the log if your builds are not respecting it.
- Because the loop only wakes every interval, a cell can be up to one interval late;
  with a 15-minute interval and an `hourly` cadence that is normal.
- A scheduled build is **attributed to the cell's creator** (the admin who defined it),
  which is who you will see as the requester on those packs.
- **Retention runs on every tick, in or out of the window** — only *scheduling* is
  window-bound.
- Cadence compares against the last **succeeded** build. A cell whose builds keep
  failing is therefore *due* on every tick: fix the cause (usually the feature cap)
  rather than waiting.

### 6.5 Retention — keeping storage in check

Rebuilding a cell over time piles up versions. **Retention** reaps the old ones —
but **never one a crew still holds**: a version is a reap candidate only once it's no
crew's current download. The per-cell policy:

| Policy | Behaviour |
|--------|-----------|
| **manual** | Never auto-reaped — you delete versions by hand (the per-pack Delete, §3). |
| **pinned** | Never auto-reaped — explicitly protected. |
| **keep_n** | Keep the newest **N** succeeded versions; reap older ones that **no crew holds**. |

| Variable (`cmd/fieldsync`) | Default | What it controls |
|----------------------------|---------|------------------|
| `FIELDSYNC_KEEP_VERSIONS` | `3` | N for `keep_n` cells. |

The maintenance loop runs retention each tick; you can also apply a cell's policy
on demand with `POST /field-packs/cells/{id}/reap` (it reports how many were reaped).

**Precisely what "held" means.** A version is *held* while it is some crew member's
**latest pickup** of that cell. When a crew member re-pulls a newer version they stop
holding the old one; a crew member still on an old version keeps it alive however old it
is (they may be weeks into a job against it). Retention therefore reaps a version only if
it is **both** outside the newest *N* **and** held by nobody — the newest *N* are kept
even with no holders yet, so a just-built version is never reaped before its first
pickup. Reaping deletes both stored files and the pack row; the ledger rows for that
version cascade away with it, which is correct because a reaped version has no holder
left to account for. `manual` and `pinned` cells are never auto-reaped, and the on-demand
reap endpoint does nothing for them either.

### 6.6 Who's holding what — the usage dashboard

Because a shared base lives for weeks and crews sync on their own cadence (e.g. every
Friday), the real operational question is **accounting**, not freshness: per cell, who
is holding which version, who has synced work back, and who has **gone quiet**.

`GET /field-packs/cells/{id}/usage` answers it by reconciling the **download ledger**
(every pickup is recorded) against the **sync inbox** (every synced edit is stamped
with the version it was made against). It returns:

- **Versions** — each built version with how many crews pulled it (so you can see
  which versions are still out in the field — useful before reaping).
- **Holders** — one row per crew member: the version they hold, when they picked it
  up, when they **last synced** against it, how many ops they've sent, and a **quiet**
  flag (picked up but **never synced** — a crew possibly sitting on un-pushed field
  work).

This is the supervisor's "who's holding / who's synced / who's gone quiet" view.

> **How "quiet" is worked out.** Each synced edit carries the snapshot time of the pack it
> was made against. The report matches a crew member's edits to the **exact version they
> currently hold**. So a crew member who just re-pulled a newer version shows as *quiet*
> until they sync something made against **that** version — even if they synced plenty
> against the previous one. Read "quiet" as *nothing synced against the version they hold
> yet*, then look at the pickup date to judge whether that is a problem.

### 6.7 Staging a pack for a crew — build-on-behalf

Separately from cells, you can pre-stage a **personal** pack **as** a named crew
member (`POST /field-packs/on-behalf`), so a tech finds a ready pack waiting on their
first connection instead of building one. Crucially, it's scoped from **their** RBAC,
not yours: the include flags come from the target's permissions, their drafts from
their own visibility, and the territory filter is bounded by **their** scope. You can
never stage a pack that gives a crew member more than they could pull themselves.

### 6.8 Permissions

All cell administration is gated on the same **`admin:field-packs`** permission as the
pack-management page (plus CSRF). Pickup, by contrast, uses the **crew's own** RBAC —
their territory access and `*:read` permissions decide which cells they may pull
(§6.1). So defining cells is an admin act; pulling them is self-service within each
crew's existing entitlements.

---

## 7. Troubleshooting

| Symptom | Likely cause / action |
|---------|----------------------|
| **A crew's pack has no work board / fault board / connections** | They lacked the matching `*:read` permission when the pack was built. Grant it; they **rebuild** the pack. |
| **Build fails with "exceeds the N-feature limit"** | The requested area/classes are over `FIELDSYNC_MAX_FEATURES`. Have them draw a smaller box or pick fewer classes (or raise the cap if your devices can handle it). |
| **Build fails otherwise** | Read the error on the Field Packs page. A "no features in scope" means an empty area; a tippecanoe error points at the tile build. |
| **Builds queue up at shift-start** | Raise `FIELDSYNC_WORKER_CONCURRENCY` (to CPU cores) and/or add a `cmd/fieldsync` replica; consider trimming `FIELDPACK_MAX_ZOOM` or the class set. |
| **Synced edits are slow to appear in the office** | Raise `SYNC_WORKER_CONCURRENCY` and/or add a replica; confirm PgBouncer is in place. |
| **A crew reports a rejected edit they don't understand** | It's in your review queue's **rejected** filter (or was an apply failure) — the reason is on the record. Usually a data/rule problem; the crew re-does the edit. |
| **Held edits piling up in review** | A class is in **review mode** and nobody's posting them. Either post/reject them, or switch the class back to **auto** if review isn't needed. |
| **Database connection errors under load** | You're running without (or past) PgBouncer. Put transaction pooling in front of Postgres and size the pool. |
| **Dense urban features look stacked on the map** | `FIELDPACK_MAX_ZOOM` may have been lowered below 16; set it back to 16 (or 17 for very dense LV) and have affected packs rebuilt. |
| **A crew can't see a shared pack you made** | Pickup is gated on *their* access. Check the crew's **territory** covers the cell's territory **and** they hold the `*:read` for every extra the base carries (connectivity/relationships/faults/tags). A crew that doesn't fully cover a cell never sees it (§6.1). |
| **A shared cell has no build to pick up** | It's never been built (or every build failed). Trigger `POST /field-packs/cells/{id}/build`, or set a **schedule**; check the build didn't fail on the feature cap (regrid finer). |
| **Old cell versions aren't being cleaned up** | Retention only reaps `keep_n` cells, and never a version a crew still holds. Set the policy to `keep_n`, and check the usage view for lingering holders (§6.6). |
| **A crew says none of their edits are arriving, but their classes are all `auto`** | Head-of-line blocking (§5.4): an **earlier** edit from that device is held (`pending_review` or `conflict`). Filter the queue to that user, clear the **oldest** held edit, and the rest flow. |
| **A conflict keeps coming back for the same feature** | Check whether the office is still editing it (live row newer than the crew's snapshot each time). After **Apply anyway**, the crew should rebuild/re-pull so their next edit is made against a fresh snapshot. |
| **Conflicts are not being detected on a class** | Detection is a per-class setting and only checks update/delete edits that carry a snapshot stamp (§5.5). It also fails open if the check errors — don't rely on it as a hard lock. |
| **A cell is listed on the device as "not yet available"** | It is active and covered, but has no succeeded version yet. Build it (`…/build`) or wait for the schedule; see why a build failed on the cell's latest version. |
| **A crew shows as "quiet" right after re-pulling** | "Quiet" is per exact version (§6.6). They have not yet synced anything made against the version they just picked up. |
| **A scheduled cell isn't rebuilding** | Its `schedule` must be `hourly`/`daily`/`weekly`, it must be **active**, and if `FIELDSYNC_BUILD_WINDOW` is set the rebuild only fires within that UTC window. |

---

## 8. Quick reference

**Permissions**

| Permission | For |
|------------|-----|
| `features:read` / `features:write` | Crew: download / sync |
| `connectivity:read`, `relationships:read`, `wam:read`, `faults:read`, `tags:read` | Crew: what a pack contains |
| `admin:field-packs` | Admin: manage packs |
| `sync:review` / `sync:post` | Admin: view / act on the review queue |
| `field:tracks:read` | Supervisor: view the Field trails map (§5.6) |

**Environment (`cmd/fieldsync`)**

| Variable | Default | Side |
|----------|---------|------|
| `FIELDSYNC_MAX_FEATURES` | 200000 | build (size cap) |
| `FIELDPACK_MIN_ZOOM` / `FIELDPACK_MAX_ZOOM` | 8 / 16 | build (tiles) |
| `FIELDSYNC_WORKER_CONCURRENCY` | 2 | build (parallel builds) |
| `FIELDSYNC_JOB_LEASE` | 60s | build (lease) |
| `SYNC_WORKER_CONCURRENCY` | 2 | sync (parallel applies) |
| `SYNC_JOB_LEASE` | 60s | sync (lease) |
| `TIPPECANOE_BIN` | tippecanoe | build (tile binary) |
| `FIELDSYNC_PORT` | 8091 | HTTP receiver port |
| `FIELDSYNC_BUILD_WINDOW` | *(unset)* | shared packs — off-peak UTC hours for scheduled rebuilds |
| `FIELDSYNC_MAINTENANCE_INTERVAL` | 15m | shared packs — schedule/retention check cadence |
| `FIELDSYNC_KEEP_VERSIONS` | 3 | shared packs — N kept by `keep_n` retention |

**Admin actions**

| To… | Do this |
|-----|---------|
| See everyone's packs | Field Packs page (`admin:field-packs`) |
| Declutter a crew's device | **Disable** the pack (reversible) |
| Reclaim storage | **Delete** the pack (permanent) |
| Gate a class for review | Sync Review → set that class to **review** (`sync:post`) |
| Check / release held edits | Sync Review → **Post** / **Reject** |
| See where a crew worked | **Field trails** (`/field-tracks`, `field:tracks:read`) → worker → session (§5.6) |
| Settle a conflict | Sync Review → conflict → **Apply anyway** / **Keep** |
| Build faster for a fleet | PgBouncer + raise concurrencies + add `cmd/fieldsync` replicas |
| Pre-build a shared area | Define a **cell** (`POST /field-packs/cells`) → build it (`…/build`) or schedule it |
| Grid a big territory | `POST /field-packs/cells/grid` (rows×cols or `cell_size_deg`) |
| See who holds / synced a cell | `GET /field-packs/cells/{id}/usage` (§6.6) |
| Reap old cell versions | Set `keep_n` retention; `POST /field-packs/cells/{id}/reap` |
| Stage a pack for a crew | `POST /field-packs/on-behalf` (scoped from *their* RBAC) |

**See also:** the **Field Tool Guide** (what crews do) and the **Production
Deployment & Scaling** guide (PgBouncer, replicas, database topology).
