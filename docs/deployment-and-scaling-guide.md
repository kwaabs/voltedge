# Production Deployment & Scaling Guide

How to run VoltEdge in a serious production environment — many concurrent users, high availability, no single point of failure — **without losing the responsiveness that makes it useful.** The target this guide is sized against is the extreme case: **~1,000 concurrent users.** That is a very large number for a utility GIS (most have dozens of editors); sizing for it means everything smaller is comfortable.

This is an operations manual for whoever deploys and runs the system. It assumes you know Docker, a container orchestrator (Kubernetes or Nomad), Postgres replication, and load balancing. It does **not** teach those; it tells you how VoltEdge specifically wants to be wired.

> **Read the pre-flight checklist (§9) before you scale out.** A couple of current defaults (notably an unbounded DB connection pool) make scaling *out* actively worse until fixed. Scaling horizontally on top of them multiplies the problem instead of solving it.

> **This guide is the *why* and *how far*.** The step-by-step *how to stand it up* — bringing up the database, the API, and every dependent service, with real commands/manifests — lives in two companions:
> - **[Production Setup Runbook](production-setup-runbook.md)** — the single-host Docker Compose install (data tier → migrate → seed → app tier), from `docker-compose.prod.yml`.
> - **[Deploying on Kubernetes & Coolify](kubernetes-and-coolify-guide.md)** — the same, on a cluster (CloudNativePG Postgres + poolers, Deployments/HPA/Ingress) or on Coolify.
>
> This guide tells you *which knob to turn when load rises* — summarised in the **[scaling playbook (§13)](#13-scaling-playbook--what-to-scale-when-and-how-far)**.

---

## 1. The one fact that makes this easy: the API tier is stateless

VoltEdge's API holds **no authoritative in-memory state**:

- **Sessions are database-backed.** A login mints an opaque token; only its SHA-256 is stored in Postgres, and every request re-loads the session from the DB (`api/internal/auth/session.go`, `middleware.go`). There is no server-side session map.
- **Attachments and exports go to object storage** (RustFS via presigned URLs), not local disk — so no pod needs a shared volume.
- **Heavy background jobs already run as separate worker processes** draining Postgres queues with `SKIP LOCKED`.

**Consequence:** you can run *N* identical copies of the API behind a **plain, non-sticky load balancer.** No session affinity, no shared filesystem. This is the expensive property to retrofit into an app, and VoltEdge already has it. Horizontal scaling of the app tier is "run more replicas."

The ceilings are therefore **not** the Go tier. They are **Postgres** and the **tile path**. The rest of this guide is mostly about those two.

---

## 2. Target production topology

```
                         ┌─────────────────────────────┐
                         │  CDN / edge  (static assets, │
                         │   cacheable tiles later)     │
                         └──────────────┬──────────────┘
                                        │
                     ┌──────────────────▼──────────────────┐
                     │   Ingress / L7 load balancer         │
                     │   TLS term · non-sticky · WAF ·      │
                     │   edge rate-limit · health checks    │
                     └──────────────────┬──────────────────┘
                                        │ (round-robin)
        ┌───────────────────────────────┼───────────────────────────────┐
        │                               │                               │
   ┌────▼─────┐                   ┌─────▼────┐                    ┌──────▼─────┐
   │  API pod │   …  (N replicas, │  API pod │   HPA 3→30 …)      │   API pod  │
   │ (Go)     │                   │ (Go)     │                    │  (Go)      │
   └────┬─────┘                   └────┬─────┘                    └─────┬──────┘
        └──────────────┬───────────────┴───────────────┬───────────────┘
                       │ writes + hot reads             │ tile/analysis reads
                ┌──────▼───────┐                 ┌──────▼───────┐
                │   PgBouncer   │                │  PgBouncer    │  (read pool)
                │  (txn pool)   │                │  (read pool)  │
                └──────┬───────┘                 └──────┬───────┘
                       │                                │
                ┌──────▼───────┐   streaming      ┌─────▼──────┐  ┌──────────┐
                │  Postgres     │  replication →   │  Replica 1 │  │ Replica 2│
                │  PRIMARY      │ ───────────────▶ │  (reads)   │  │ (reads)  │
                │  (+PostGIS)   │                  └────────────┘  └──────────┘
                └───────────────┘
                       ▲
                       │ queues (SKIP LOCKED)
     ┌─────────────────┼───────────────────────────────────────┐
     │          │              │              │            │
┌────▼───┐ ┌────▼────┐ ┌───────▼───┐ ┌────────▼──┐ ┌───────▼────────┐
│export  │ │import   │ │webhook    │ │fieldsync  │ │ mcpserver      │
│worker  │ │worker   │ │worker     │ │★SINGLETON │ │ (AI tool API)  │
│(scale) │ │(scale)  │ │(scale)    │ │           │ │                │
└────────┘ └─────────┘ └───────────┘ └───────────┘ └────────────────┘

   ┌───────────────┐        ┌──────────────────────────────┐
   │ Object storage │        │ Observability: Vector→Loki→   │
   │ (RustFS/S3)    │        │ Grafana  (see logging guide)  │
   └───────────────┘        └──────────────────────────────┘
```

Seven moving tiers: **edge/LB**, **API replicas**, **worker fleet**, **PgBouncer**, **Postgres primary + replicas**, **object storage**, **observability**. Each scales independently; §§3–8 cover each.

---

## 3. Edge / load balancer

- **TLS termination** at the ingress (nginx-ingress, Traefik, HAProxy, or a cloud L7 LB).
- **Non-sticky, round-robin.** The API needs no session affinity — do not enable it (it defeats even load distribution).
- **Health checks.** The API already exposes `GET /healthz`, and it checks Postgres reachability — so it doubles as a readiness probe (a replica that can't reach the DB is pulled from rotation). That is what both the LB and the Compose/k8s probes point at. If you later want to distinguish *liveness* (process up) from *readiness* (DB reachable + migrations at expected version), split out a dedicated `/readyz`; for now `/healthz` serves both.
- **Edge rate-limiting.** VoltEdge's built-in rate limiter is **per-instance** (process-local token buckets, by design — `api/internal/ratelimit/ratelimit.go`). Across *N* replicas it no longer enforces a global limit. Put the real limit at the LB/WAF (per-IP, per-route for `/auth/login` and webhook ingress). Keep the in-app limiter as a second line.
- **Request timeouts** and **max body size** (the API caps bodies at 32 MB; mirror or lower at the edge).
- **CDN** in front for static SPA assets now, and for cacheable tiles later (§6).

---

## 4. API tier (stateless Go replicas)

The workhorse. Because it is stateless, this is the tier an autoscaler grows and shrinks.

**Resourcing per pod** (starting point — tune against load tests):
- CPU: request 500m, limit 2 cores. Analysis/trace is CPU-bound and runs in-process, so headroom matters.
- Memory: request 256Mi, limit 1Gi. Go + result sets; the engines are not memory-hungry, but large traces/exports spike.

**Probes:**
- Liveness → `/healthz` (process up).
- Readiness → `/readyz` (DB reachable, migrations at expected version). Pods failing readiness are removed from the Service without being killed.

**Autoscaling (HPA):** scale on CPU (target ~60%) and, better, on a custom metric (p95 request latency or in-flight heavy-analysis count). Suggested band: `minReplicas: 3`, `maxReplicas: 30`. Three is the floor for HA (survive a node loss and a rolling deploy).

**PodDisruptionBudget:** `minAvailable: 2` so a node drain can't take the whole API tier down.

**Config/secrets:** DB DSN, object-storage creds, OIDC config via `Secret`/`ConfigMap`. Every replica gets identical config.

Skeleton Deployment (trim to your cluster conventions):

```yaml
apiVersion: apps/v1
kind: Deployment
metadata: { name: voltedge-api }
spec:
  replicas: 3                     # HPA overrides
  selector: { matchLabels: { app: voltedge-api } }
  template:
    metadata: { labels: { app: voltedge-api } }
    spec:
      containers:
      - name: api
        image: registry/voltedge-api:TAG
        ports: [{ containerPort: 8080 }]
        env:
        - { name: DATABASE_URL, valueFrom: { secretKeyRef: { name: voltedge-db, key: dsn } } }
        - { name: DATABASE_MAX_OPEN_CONNS, value: "20" }   # see §7 — must be bounded
        - { name: DOCS_DIR, value: /app/docs }
        readinessProbe: { httpGet: { path: /readyz, port: 8080 }, periodSeconds: 5 }
        livenessProbe:  { httpGet: { path: /healthz, port: 8080 }, periodSeconds: 10 }
        resources:
          requests: { cpu: 500m, memory: 256Mi }
          limits:   { cpu: "2",  memory: 1Gi }
---
apiVersion: autoscaling/v2
kind: HorizontalPodAutoscaler
metadata: { name: voltedge-api }
spec:
  scaleTargetRef: { apiVersion: apps/v1, kind: Deployment, name: voltedge-api }
  minReplicas: 3
  maxReplicas: 30
  metrics:
  - type: Resource
    resource: { name: cpu, target: { type: Utilization, averageUtilization: 60 } }
```

---

## 5. Database tier — the real ceiling

Postgres single-primary is your ultimate **write** ceiling. Reads scale out; writes do not. The whole game is: keep writes on the primary, push the read flood (tiles, traces, feature reads) to replicas, and never exhaust connections.

### 5.1 Connection pooling — do this first

Today the server opens an **unbounded** `database/sql` pool (no `SetMaxOpenConns`) over `pgdriver` (`api/cmd/server/main.go`). With *N* replicas each opening unlimited connections, you will blow past Postgres `max_connections` long before you run out of CPU. Two things, both mandatory before scaling out:

1. **Bound the pool per pod.** Set `MaxOpenConns` to a modest number (10–25). A pod does not need hundreds; it needs enough to keep its cores busy. (Expose it via env — see `DATABASE_MAX_OPEN_CONNS` above — and call `db.SetMaxOpenConns`, `SetConnMaxIdleTime`, `SetConnMaxLifetime`.)
2. **Put PgBouncer in front, in transaction-pooling mode.** This is what lets thousands of client-side connections map onto a small server-side pool. Run PgBouncer as a sidecar or a dedicated Deployment; point the app's DSN at it.

**Connection math (worked example for ~1,000 users):**

| Layer | Count | Conns each | Total client conns |
|---|---|---|---|
| API pods | 20 | 20 (bounded) | 400 |
| Worker pods | ~8 | 2–4 | ~24 |
| **Into PgBouncer** | — | — | **~424** |
| PgBouncer → Postgres (txn pool) | — | — | **~50–100 server conns** |

PgBouncer collapses 424 client connections onto ~50–100 real Postgres backends. Size `max_connections` on the primary for **PgBouncer's server pool + replication slots + admin headroom** (e.g. 200), *not* for raw client count. Without PgBouncer you must keep `Σ(pods × MaxOpenConns)` under `max_connections` yourself — doable at small N, fragile at 20+.

> **Transaction pooling caveat:** it does not support session-level features (prepared statements held across transactions, `SET` that must persist, `LISTEN/NOTIFY`, advisory locks held across statements). Verify the ORM path (bun) tolerates it; if a specific path needs session pooling, give that path a second, session-mode PgBouncer. Test under load before trusting it.

### 5.2 Read replicas + read/write split

- **Run 2–3 streaming replicas.** Use a Postgres operator (**CloudNativePG** or the Zalando operator) to manage primary + replicas, automated failover, backups and PITR. Do **not** hand-roll replication for a production utility system.
- **Route reads to replicas.** This is the capacity multiplier — and it is **code work**: VoltEdge currently uses one pool/DSN for everything. To use replicas you need the app to send read-only queries (tiles above all, then traces, feature reads, metadata) to a **read-only pool** pointed at the replica service, and keep writes on the primary pool. The highest-value single step is giving the **tiles package its own read-only DB handle** against replicas (§6) — tiles are the dominant read load and are trivially read-only.
- **Mind replication lag.** Replicas are eventually consistent (usually <1s). Reads that must be read-your-own-write (e.g. immediately re-reading a feature you just edited) should stay on the primary or tolerate the lag. Tiles and analysis are lag-tolerant; the edit-then-reload path is not — keep it on the primary.

### 5.3 PostGIS specifics

- Ensure spatial indexes (GiST) exist on every `geom` column the tile and trace queries hit (the compiler should emit them — verify under `EXPLAIN` on a tile query).
- Replicas inherit indexes; no extra work.
- Tune `shared_buffers`, `work_mem`, `effective_cache_size` for the box; `ST_AsMVT` is memory-sensitive under concurrency.

---

## 6. The tile path — the hardest and highest-volume problem

Every map tile is a **live `ST_AsMVT` query against Postgres, rendered per-user**, scoped to the user's territory *and* their private-draft overlay, with **no caching of any kind** (`api/internal/tiles/tiles.go`). At 1,000 users panning, this is by far your largest DB load, and per-user scoping blocks the usual "slap a CDN in front" fix.

Order-of-magnitude (illustrative): if ~150–200 users are *actively* panning at once, that's ~300–600 `ST_AsMVT`/sec, each 10–40 ms of Postgres CPU — multiple cores on tiles alone. That does not hold on one primary. The fix is layered:

1. **Route tile queries to read replicas (do this first).** Pure reads, lag-tolerant. A dedicated read-only handle for the tiles package spreads the dominant load across replicas with no semantic change. This alone buys a lot.
2. **Make tiles cacheable by *scope class*, not per-user (the architectural fix).** The deciding question: **how many distinct territory-scopes do your users actually have?**
   - If access is **coarse** (most users see the whole network, or one of a handful of regions), change the cache key to `(scope_group, has_drafts, z, x, y)`. Now hundreds of users share tiles → a reverse-proxy/CDN cache becomes viable and the DB load collapses.
   - If every user has a **bespoke** territory set, shared caching is dead; you're on replicas + raw horsepower.
3. **Split base vs overlay.** Serve the **published network** from a shared tile cache (or pre-generated **pmtiles** refreshed on edit); render the **private-draft overlay** as a thin per-user layer composited client-side. Drafts are a tiny fraction of features, so ~95% of tile bytes come from the shared cache.
4. **Add HTTP cache headers** (`Cache-Control`, `ETag`) even on per-session tiles so browsers and any shared cache can revalidate instead of re-rendering on every pan.

Decide #2 early — it reshapes the whole read tier.

---

## 7. Keeping responsiveness under load ("don't lose its effect")

Three code-level changes preserve snappiness when the system is busy. None is large.

1. **Concurrency gate on analysis/trace.** Load-flow, fault, contingency, nodal, arc-flash and trace all run **synchronously in the HTTP handler with no limit** (`api/internal/powerflow/handler.go`, `trace/handler.go`). A few concurrent heavy solves can pin every core on a pod and starve the viewers sharing it. Add a **weighted semaphore** so each pod runs at most *k* heavy solves at once and returns `429 / queue` beyond that. This converts a silent collapse into graceful back-pressure. (For sustained analyst load, later move studies to an analysis **worker pool** using the same `SKIP LOCKED` pattern as exports, off the request tier entirely.)
2. **TTL cache for metadata.** `metadata.Load` fires ~6+ queries **every request** for data that changes rarely (feature classes, fields, subtypes, domains, symbology — `api/internal/metadata/load.go`). A 30–60 s in-process cache per pod removes a large slice of baseline DB load. Accept ≤60 s staleness after a metadata edit (same bound the access cache already uses).
3. **Mind the per-instance access cache.** The territory/subordinate-scope cache is in-memory per pod with a 60 s TTL (`api/internal/access/cache.go`). Under *N* pods, a grant/revoke or manager change is "instant" only on the pod that handled it; others serve stale scope for ≤60 s. If **instant revocation** is a security requirement, either drop the TTL or add cross-instance invalidation (Redis pub/sub, or a NOTIFY-driven flush). Otherwise accept the bound and document it.

---

## 8. Worker fleet

The compute-heavy / long-running work is already off the request path as separate processes draining Postgres queues with `SKIP LOCKED` — so they scale **horizontally by replica count** with no coordination:

| Worker | Scales how | Notes |
|---|---|---|
| `exportworker` | N replicas | `SKIP LOCKED`; `--scale` safe. |
| `importworker` | N replicas | ETL ingest; bursty — scale on queue depth. |
| `webhookworker` | N replicas | Inbound ingestion apply. |
| `mcpserver` | N replicas | AI tool API; stateless like the main API. |
| `fieldsync` | **SINGLETON — do not scale** | The field-pack **builder** must run single-instance (it shells out to tippecanoe over temp files). Run exactly one replica; use a `Deployment` with `replicas: 1` + `strategy: Recreate`, or a leader-elected singleton. The sync-**apply** half is queue-drained and safe, but keep the process to one to respect the builder constraint. |

Scale export/import/webhook workers on **queue depth** (a custom metric: rows pending in the job table) rather than CPU — they're I/O- and burst-shaped.

---

## 9. Pre-flight checklist — before you scale out

Ordered. The first two are not optional; scaling out without them makes things worse.

1. **Bound the DB pool** per pod (`SetMaxOpenConns` 10–25) — *mandatory*. Unbounded + N replicas = instant `max_connections` exhaustion.
2. **PgBouncer** (transaction mode) in front of Postgres — *mandatory at N > ~10 pods*.
3. **Health checks wired** — the API's `/healthz` already checks DB reachability; point LB/probes at it (add a separate `/readyz` only if you want liveness/readiness split).
4. **Concurrency gate** on analysis/trace handlers — prevents one pod's viewers being starved by a few big solves.
5. **Read replicas + route tile (and other read-only) queries to them** — the capacity multiplier; code work for the read/write split.
6. **Decide the tile caching story** (§6 #2) — coarse scope → shared cache; bespoke scope → replicas + horsepower.
7. **Metadata TTL cache** — removes constant baseline DB load.
8. **Edge rate-limiting** at the LB (the in-app limiter is per-pod).
9. **Object storage** (RustFS/S3) provisioned, replicated, backed up — attachments/exports live here.
10. **Observability wired** (Vector → Loki → Grafana — see the Logging & Observability guide) with the alerts in §11.
11. **Postgres operator** (CloudNativePG/Zalando) for replicas, failover, PITR backups.
12. **Load test** at target concurrency against a production-shaped dataset *before* go-live — synthetic 1k is cheap; a storm-day surprise is not.

---

## 10. Schema migrations & the compiler in a replicated world

VoltEdge compiles metadata → physical `gis.<class>` tables (`task compile`) and runs SQL migrations. With N live replicas you need **zero-downtime, backward-compatible** changes:

- **Additive, nullable first.** The compiler already follows the additive-NOT-NULL→nullable pattern (a new required column is added nullable, then backfilled, then constrained) — this is exactly what lets old and new API replicas run against the same schema during a rolling deploy. Keep every migration backward-compatible with the currently-running image.
- **Run migrations once**, as a pre-deploy `Job` (or init container with a leader lock), **not** from every pod on boot — concurrent migration runs race. Gate app readiness on "schema at expected version."
- **Deploy order for a breaking change:** expand (add new nullable column / table) → deploy code that writes both old and new → backfill → deploy code that reads new → contract (drop old) in a later release. Never expand and contract in the same release while replicas overlap.
- **A compile** that rewrites a physical table takes a lock — schedule it in a low-traffic window and verify it's additive. The data dictionary (`data-dictionary.md`) and the compiler notes describe which changes are additive vs rewriting.

---

## 11. What to watch (SLOs & alerts)

Define the SLOs that encode "didn't lose its effect," then alert on their leading indicators:

**SLOs (starting targets):**
- Tile p95 < 150 ms; feature-read p95 < 200 ms; map interactive.
- Analysis (trace/load-flow) p95 < 2 s for typical feeders; heavy studies queue, not block.
- Availability 99.9% on the read path.

**Alert on:**
- **DB connections in use / `max_connections`** > 80% → pool/PgBouncer sizing wrong (the classic scale-out failure).
- **Replication lag** > a few seconds → reads going stale; replica under-provisioned.
- **Tile query rate & p95** → the leading indicator of read-tier saturation; triggers "add replicas / turn on caching."
- **In-flight heavy-analysis count per pod** near the semaphore cap → analyst load rising; scale API or stand up the analysis worker pool.
- **Worker queue depth** (export/import/webhook) → backlog; scale that worker.
- **5xx rate, pod restarts, readiness flaps**, object-storage error rate.

---

## 12. The compose → Kubernetes path (if you're not on k8s yet)

You can stage this. Docker Compose already runs the workers as separate services and supports `--scale exportworker=N`, and the API tier being stateless means even plain Compose can run multiple API containers behind an nginx/HAProxy in front.

A pragmatic ladder:
1. **Compose, scaled:** this ships as **`docker-compose.prod.yml`** + **`infra/nginx/nginx.conf`** — nginx (non-sticky) in front of N `api` replicas, PgBouncer between the app tier and Postgres, workers scaled by replica count, `fieldsync` pinned singleton, and a replication-ready primary (with the streaming read replica left as a documented, commented opt-in). Run it with `docker compose -f docker-compose.prod.yml up -d --build --scale api=4`. Good to a few hundred users and it *proves* the multi-replica architecture on one host. **Note:** this file is authored but not yet run in CI — see its header "verify" steps (PgBouncer auth against SCRAM, and whether pgdriver's prepared statements tolerate transaction pooling) before relying on it.
2. **Managed Postgres + Kubernetes:** move Postgres to a managed service or an operator (replicas, failover, PITR handled for you), move the API/worker tiers to Deployments + HPA. This is the ~1,000-user shape in §2.
3. **Add the tile caching tier and the analysis worker pool** when the metrics in §11 say you need them — not before.

Don't skip to full k8s on day one if you're pre-production; prove the tiers under load on the cheaper rung first. But **do** the §9 pre-flight fixes regardless of which rung you're on — they're about the application, not the orchestrator.

---

## 13. Scaling playbook — what to scale, when, and how far

Every component, the signal that tells you to scale it, the action, and the ceiling past which scaling *that* component stops helping. This is the one-table consolidation of §§3–8 and §11; "set up in" points at where the component is first stood up.

| Component | Set up in | Scale **when** (signal) | **How** to scale | Ceiling / caveat |
|---|---|---|---|---|
| **Edge LB / Ingress** | runbook §8 · k8s B9 | LB CPU or active-connection count high | bigger LB, or more ingress-controller replicas | rarely the bottleneck; mostly pass-through |
| **Web (SPA nginx)** | k8s B8 · Coolify A4 | web pod CPU; slow static serves | more `web` replicas (stateless) | trivial; a CDN in front offloads it entirely |
| **API** | runbook §7 · k8s B6 | CPU > ~60%, request p95 rising, or in-flight heavy-analysis near the cap | HPA `3 → 30` (or `--scale api=N`) | **DB connections** — only safe once the pool is bounded + PgBouncer is in front (§5.1, pre-flight #1–2) |
| **PgBouncer / CNPG pooler** | runbook data-tier · k8s B3 | `cl_waiting` > 0, clients queueing for a server conn | raise `DEFAULT_POOL_SIZE`; add pooler instances | Postgres `max_connections` (size it for the pool, not raw clients) |
| **Postgres PRIMARY** | runbook data-tier · k8s B3 | **write** CPU/IO saturated, commit latency up | **vertical only** — bigger box, faster disk, tune `shared_buffers`/`work_mem` | writes do **not** scale out; a single primary is the ultimate write ceiling (sharding is a project, not a knob) |
| **Postgres READ replicas** | §5.2 · k8s B3 (`ro` pooler) | tile/feature-read p95 up, or replica CPU high | add replicas; **route read-only queries to them** (code: a read-only DB handle) | replication lag (~<1s) — keep read-your-own-write on the primary |
| **Tiles** | §6 | tile p95 or `ST_AsMVT`/sec climbing | 1) tiles → read replicas · 2) scope-class cache + CDN · 3) base/overlay split | per-user territory scoping blocks naive caching — decide §6 #2 early |
| **Analysis / trace** | §7 | analysis p95 up, or heavy solves pinning a pod | add API replicas + the concurrency gate (§7.1); then an **analysis worker pool** (off the request tier) | CPU-bound; the gate converts collapse into back-pressure |
| **exportworker / importworker / webhookworker** | runbook §7 · k8s B7 | **queue depth** (pending rows) rising | more replicas (KEDA on queue depth); `SKIP LOCKED` makes it safe | DB write throughput on the primary |
| **fieldsync** | runbook §7 · k8s B7 | pack-build backlog | **do NOT scale** — it is a singleton (tippecanoe over temp files); go **vertical** (more CPU on the one pod) | one builder only; `replicas: 1`, `strategy: Recreate` |
| **mcpserver** | runbook §7 · k8s B7 | MCP request CPU | more replicas (stateless like the API) | same DB-connection ceiling as the API |
| **Object storage (RustFS/S3)** | runbook data-tier · k8s B4 | storage IO or capacity; upload/download latency | move to real **S3** or a MinIO **cluster**; scale that out | single-node RustFS is a SPOF — externalise for HA |
| **Observability (Loki)** | logging guide | log-ingest volume | scale Loki ingesters / retention | separate plane; never let it share Postgres |

**How to read it in an incident:** the alert that fired (§11) points at a row; the row tells you the action and whether scaling that component even helps. The two rows where "scale it" is the *wrong* answer: **Postgres primary** (vertical/architectural, not horizontal) and **fieldsync** (never — it's a singleton). Everything else is "add replicas," gated on the §9 pre-flight being done.

---

## Bottom line

VoltEdge is **architecturally ready** to scale: the API tier is already stateless, workers are already separated, storage is already externalized. Reaching 1,000 concurrent users is a **tuning pass plus one design decision**, not a rewrite:

- **The must-fixes are small:** bound the pool, add PgBouncer, gate analysis concurrency, cache metadata, add health endpoints.
- **The capacity multipliers are infrastructure:** read replicas, non-sticky LB, autoscaled API replicas, edge rate-limiting.
- **The one genuine design decision is tile caching** — and its answer depends entirely on how coarse your territory-access model is.

Get §9 done, make the tile call in §6, and the system keeps its responsiveness at a scale most utility GIS never has to reach.
