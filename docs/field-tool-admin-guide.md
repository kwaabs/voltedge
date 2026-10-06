# VoltEdge Field Tool — Admin Guide

**Who this is for:** administrators and supervisors who **set up, manage, review,
and scale** the field tool — not the crews using it. If you're a crew member, read
the **Field Tool Guide** instead.

This guide assumes you work in the VoltEdge **web app** (cookie session + RBAC) and
can reach the server environment. It covers four jobs:

1. **Giving crews access** — logins and what a pack is allowed to contain.
2. **Managing packs** — listing, hiding, and deleting everyone's packs.
3. **Reviewing field work** — the QA queue: holding, posting, rejecting, and
   resolving conflicts on incoming edits.
4. **Tuning and scaling** — the build/sync knobs, and what to provision for a fleet.

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
   builds. (Pack reuse/dedup is a planned enhancement; until then, stagger download
   windows or pre-build common areas off-peak.)

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

---

## 6. Troubleshooting

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

---

## 7. Quick reference

**Permissions**

| Permission | For |
|------------|-----|
| `features:read` / `features:write` | Crew: download / sync |
| `connectivity:read`, `relationships:read`, `wam:read`, `faults:read`, `tags:read` | Crew: what a pack contains |
| `admin:field-packs` | Admin: manage packs |
| `sync:review` / `sync:post` | Admin: view / act on the review queue |

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

**Admin actions**

| To… | Do this |
|-----|---------|
| See everyone's packs | Field Packs page (`admin:field-packs`) |
| Declutter a crew's device | **Disable** the pack (reversible) |
| Reclaim storage | **Delete** the pack (permanent) |
| Gate a class for review | Sync Review → set that class to **review** (`sync:post`) |
| Check / release held edits | Sync Review → **Post** / **Reject** |
| Settle a conflict | Sync Review → conflict → **Apply anyway** / **Keep** |
| Build faster for a fleet | PgBouncer + raise concurrencies + add `cmd/fieldsync` replicas |

**See also:** the **Field Tool Guide** (what crews do) and the **Production
Deployment & Scaling** guide (PgBouncer, replicas, database topology).
