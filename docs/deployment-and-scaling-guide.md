# Production Deployment & Scaling Guide

How to run VoltEdge in a serious production environment — many concurrent users, high availability, no single point of failure — **without losing the responsiveness that makes it useful.** The target this guide is sized against is the extreme case: **~1,000 concurrent users.** That is a very large number for a utility GIS (most have dozens of editors); sizing for it means everything smaller is comfortable.

This is an operations manual for whoever deploys and runs the system. It assumes you know Docker, a container orchestrator (Kubernetes or Nomad), Postgres replication, and load balancing. It does **not** teach those; it tells you how VoltEdge specifically wants to be wired.

> **Read the pre-flight checklist (§9) before you scale out.** A couple of current defaults (notably an unbounded DB connection pool) make scaling *out* actively worse until fixed. Scaling horizontally on top of them multiplies the problem instead of solving it.

> **This guide is the *why* and *how far*.** The step-by-step *how to stand it up* — bringing up the database, the API, and every dependent service, with real commands/manifests — lives in two companions:
> - **[Production Setup Runbook](production-setup-runbook.md)** — the single-host Docker Compose install (data tier → migrate → seed → app tier), from `docker-compose.prod.yml`.
> - **[Deploying on Kubernetes & Coolify](kubernetes-and-coolify-guide.md)** — the same, on a cluster (CloudNativePG Postgres + poolers, Deployments/HPA/Ingress) or on Coolify.
>
> This guide tells you *which knob to turn when load rises* — summarised in the **[scaling playbook (§13)](#13-scaling-playbook--what-to-scale-when-and-how-far)**.

### Reality check — what the repo does today vs what this guide recommends

This guide mixes **things that already exist** with **things you must build or configure**. Verified against the repo (evidence in the right-hand column):

| Item | Today | Evidence |
|---|---|---|
| Stateless API (DB-backed sessions, object-storage attachments) | **Yes** | `api/internal/auth/session.go` (`token_sha256` lookup), `api/internal/auth/middleware.go` (`SessionLoader`) |
| Bounded DB pool per pod | **No — unbounded.** `sql.OpenDB(pgdriver.NewConnector(...))` with no `SetMaxOpenConns`/`SetConnMaxLifetime` | `api/cmd/server/main.go:93`; the only `SetMaxOpenConns` calls in `api/` are in importers and the field-pack SQLite builder |
| `DATABASE_MAX_OPEN_CONNS` env var | **Not implemented** — no Go code reads it (it appears only in this guide and in the Kubernetes guide). It is a *proposed* knob | `grep -rn MAX_OPEN api/` finds nothing |
| `/healthz` (checks Postgres, 3 s timeout, `503` on failure) | **Yes** | `api/cmd/server/main.go:186,488-503`; also on `mcpserver` and `fieldsync` |
| `/readyz` | **No** — does not exist; `/healthz` serves both liveness and readiness | `grep -rn readyz api/` finds nothing |
| PgBouncer in transaction mode | **Yes in `docker-compose.prod.yml`** (not run in CI — see its header) | `docker-compose.prod.yml` `pgbouncer` service |
| nginx non-sticky LB + login edge rate-limit | **Yes in `docker-compose.prod.yml`** | `infra/nginx/nginx.conf` |
| Read/write split (read-only DB handle) | **No** — one pool/DSN for everything | `api/cmd/server/main.go:93-94` |
| Tile caching / `Cache-Control` / `ETag` | **None** — only `Content-Type` is set | `api/internal/tiles/tiles.go:129` |
| Concurrency gate on analysis/trace | **None** — no semaphore in `powerflow`/`trace` | `grep -rn semaphore api/internal/powerflow api/internal/trace` finds nothing |
| Metadata TTL cache | **None** — `metadata.Load` queries every call | `api/internal/metadata/load.go:61` |
| Access (territory) cache | **Yes**, per-pod, 60 s TTL | `api/internal/access/cache.go:18` |
| Worker queues with `FOR UPDATE SKIP LOCKED` and lease reclaim | **Yes** (export, import, inbound/outbound webhook, field-pack builds, sync inbox) | `api/internal/export/job.go:110-127`, `api/internal/webhook/deliveries.go:70-90`, `api/internal/fieldpack/inbox.go:175-185` |

Everything marked **No** is the "code work" this guide talks about. Until it is done, §9 items 1, 4, 5 and 7 are *recommendations*, not behaviours you can switch on.

---

## 1. The one fact that makes this easy: the API tier is stateless

VoltEdge's API holds **no authoritative in-memory state**:

- **Sessions are database-backed.** A login mints an opaque token; only its SHA-256 is stored in Postgres, and every request re-loads the session from the DB (`api/internal/auth/session.go`, `middleware.go`). There is no server-side session map.
- **Attachments and exports go to object storage** (RustFS via presigned URLs), not local disk — so no pod needs a shared volume.
- **Heavy background jobs already run as separate worker processes** draining Postgres queues with `SKIP LOCKED`.

**Consequence:** you can run *N* identical copies of the API behind a **plain, non-sticky load balancer.** No session affinity, no shared filesystem. This is the expensive property to retrofit into an app, and VoltEdge already has it. Horizontal scaling of the app tier is "run more replicas."

The ceilings are therefore **not** the Go tier. They are **Postgres** and the **tile path**. The rest of this guide is mostly about those two.

**"Stateless" has three footnotes.** The API holds no *authoritative* state, but it does hold three process-local things that behave differently once you run N copies:

| In-process state | What it is | What changes with N pods |
|---|---|---|
| Rate-limiter buckets | token buckets per client IP (`api/internal/ratelimit/ratelimit.go`) | each pod limits only the traffic it sees — no global limit (§3) |
| Territory/subordinate scope cache | 60 s TTL per user (`api/internal/access/cache.go`) | a grant/revoke is instant only on the pod that handled it (§7.3) |
| Nothing else authoritative | sessions are re-loaded from Postgres on every request (`SessionLoader`) | a *session* revoke is instant everywhere — only *scope* changes lag |

**Prove it on your own deployment** (Compose, four replicas — the nginx log format in `infra/nginx/nginx.conf` prints `upstream=<addr>` per request):

```bash
docker compose -f docker-compose.prod.yml up -d --build --scale api=4
# log in once, then hit an authenticated route repeatedly with the same cookie
for i in $(seq 1 20); do curl -s -o /dev/null -b cookies.txt http://localhost:3710/api/auth/me; done
docker compose -f docker-compose.prod.yml logs nginx | grep 'auth/me' | grep -o 'upstream=[^ ]*' | sort | uniq -c
```

You should see the requests spread across several upstream addresses **and every one succeed with the same session cookie** — that is statelessness in practice. (Port `3710` is `WEB_PORT`'s default; `/auth/me` is the session-scoped endpoint mounted by `auth.Routes` — if your build names it differently, any authenticated GET works.)

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
│worker  │ │worker   │ │worker     │ │build+sync │ │ (AI tool API)  │
│(scale) │ │(scale)  │ │(scale)    │ │(scale)    │ │                │
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

### 3.1 The in-app limiter, with its real numbers

| Limiter | Rate / burst | Where | Evidence |
|---|---|---|---|
| Login (`POST /auth/login`) | 10 per minute, burst 10, **per client IP, per pod** | `auth.Routes` | `api/internal/auth/handler.go:48` |
| Field-app login | 10 per minute, burst 10 | `fieldpack` routes | `api/internal/fieldpack/routes.go:74` |
| Webhook receiver `/webhooks` | 300 per minute, burst 100 | `cmd/server` | `api/cmd/server/main.go:198` |
| nginx edge (Compose) | `limit_req_zone … rate=5r/s`, `burst=10 nodelay`, on `= /api/auth/login` only | `infra/nginx/nginx.conf` | |

> **Watch-out — the in-app key is the TCP peer, not the client.** `ratelimit.clientIP` keys on `r.RemoteAddr` and deliberately ignores `X-Forwarded-For` (`ratelimit.go:115`), and the server does not mount chi's `RealIP` middleware (the `httpx` logger comment says a trusted-proxy middleware must be mounted for forwarded addresses to be trusted, `api/internal/httpx/logger.go:99`). Behind nginx or a cloud LB, **every user therefore shares one bucket per pod** — the LB's address. With 10 logins/minute (burst 10) per pod, a shift-start login wave of hundreds of users across 4 pods is throttled to roughly 40 logins/minute after the first 40. If your users log in in a wave, either (a) raise the numbers, (b) mount a trusted-proxy `RealIP` middleware that only trusts your LB, or (c) accept that the edge limiter — which *does* see real client IPs — is the real control and size the in-app limiter generously. This is a configuration decision, not a bug in the limiter's intent (it is built for a single instance or sticky routing, per its package comment).

### 3.2 Health-check timing

`GET /healthz` pings Postgres with a **3-second** context timeout and returns `200 {"status":"ok"}` or `503 {"status":"db unreachable: …"}` (`api/cmd/server/main.go:488`). Probe settings that exist in the repo:

| Where | interval | timeout | failures to mark unhealthy | start grace |
|---|---|---|---|---|
| `docker-compose.prod.yml` `api` | 10 s | 5 s | 3 retries | 20 s |
| Kubernetes guide B6 liveness / readiness | 10 s / 5 s | (default) | 3 / 3 | — |

Worked example: the DB goes away at *t = 0*. Compose pulls a replica after about `retries × interval` = **~30 s** of failed checks (each check may itself wait up to the 3 s ping timeout); a Kubernetes readiness probe at `periodSeconds: 5` and `failureThreshold: 3` removes the pod from the Service in **~15 s**. Both pull *every* replica when the database is the thing that is down — which is correct (they cannot serve) but means a total DB outage reads as a total API outage. That is why the LB should serve a friendly 503 page, and why alerts key on the DB, not on pod health.

---

## 4. API tier (stateless Go replicas)

The workhorse. Because it is stateless, this is the tier an autoscaler grows and shrinks.

**Resourcing per pod** (starting point — tune against load tests):
- CPU: request 500m, limit 2 cores. Analysis/trace is CPU-bound and runs in-process, so headroom matters.
- Memory: request 256Mi, limit 1Gi. Go + result sets; the engines are not memory-hungry, but large traces/exports spike.

**Probes:**
- Liveness **and** readiness → `/healthz`. Today it is the only health endpoint (it pings Postgres, 3 s timeout, `503` when unreachable). A failing readiness probe removes the pod from the Service without killing it; a failing liveness probe restarts it — so point liveness at a threshold slow enough (`failureThreshold: 3`, `periodSeconds: 10`) that a short DB blip does not restart every pod at once.
- *Proposed, not implemented:* a separate `/readyz` (DB reachable **and** migrations at the expected version) and a liveness endpoint that does **not** touch the DB. The manual previously showed `/readyz` in the skeleton below; that path does not exist in the code, so the skeleton now uses `/healthz` for both. See §3.2 for probe timing.

**Autoscaling (HPA):** scale on CPU (target ~60%) and, better, on a custom metric (p95 request latency or in-flight heavy-analysis count — the latter does not exist until the concurrency gate in §7.1 does). Suggested band: `minReplicas: 3`, `maxReplicas: 30`. Three is the floor for HA (survive a node loss and a rolling deploy).

**The HPA rule, worked.** Kubernetes computes `desired = ceil(current × observed / target)` and clamps it to `[min, max]`. With 6 pods at 90 % average CPU against a 60 % target: `ceil(6 × 90 / 60)` = **9** pods. At 12 % CPU with 9 pods: `ceil(9 × 12 / 60)` = 2, clamped up to the floor of **3**. Note the **request** of 500m is what "Utilization" is measured against (60 % of 500m = 300m per pod), not the 2-core limit — so a pod can burst well above 100 % of its request during a heavy solve before the autoscaler has even reacted. The *connection* ceiling follows the max: 30 pods × 20 conns = 600 client connections into PgBouncer (§5.1).

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
        - { name: DATABASE_MAX_OPEN_CONNS, value: "20" }   # PROPOSED — not read by the code today; see §5.1 (the pool is unbounded until you add it)
        - { name: DOCS_DIR, value: /app/docs }
        readinessProbe: { httpGet: { path: /healthz, port: 8080 }, periodSeconds: 5, failureThreshold: 3 }
        livenessProbe:  { httpGet: { path: /healthz, port: 8080 }, periodSeconds: 10, failureThreshold: 3 }
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

Today the server opens an **unbounded** `database/sql` pool (no `SetMaxOpenConns`) over `pgdriver` (`api/cmd/server/main.go:93`: `sql.OpenDB(pgdriver.NewConnector(pgdriver.WithDSN(databaseURL)))`). *Verified:* nothing in `api/cmd/server`, `exportworker`, `importworker`, `webhookworker`, `mcpserver` or `fieldsync` ever calls `SetMaxOpenConns`, `SetMaxIdleConns` or `SetConnMaxLifetime` on the main pool. Go's `database/sql` defaults then apply: **unlimited open connections, but only 2 idle ones kept** — so under a burst a pod opens as many connections as it has in-flight queries, then closes all but two, and re-opens them on the next burst (connection churn, which PgBouncer also absorbs). With *N* replicas each opening unlimited connections, you will blow past Postgres `max_connections` long before you run out of CPU. Two things, both mandatory before scaling out:

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

**What the repo's Compose file actually configures** (`docker-compose.prod.yml`, `pgbouncer` + `postgres` services):

| Setting | Value | Notes |
|---|---|---|
| `POOL_MODE` | `transaction` | |
| `DEFAULT_POOL_SIZE` (`PGBOUNCER_POOL_SIZE`) | **25** | real backends per (user, database) pair — all VoltEdge processes connect as `postgres` to one database, so this is **one** pool of 25 |
| `RESERVE_POOL_SIZE` | 5 | extra backends PgBouncer may add when clients wait |
| `MIN_POOL_SIZE` | 5 | |
| `MAX_CLIENT_CONN` (`PGBOUNCER_MAX_CLIENT_CONN`) | 1000 | client-side ceiling; above it new clients are refused |
| Postgres `max_connections` | 200 | sized for pool + replication + headroom |
| `AUTH_TYPE` | `plain` | the file's own header flags SCRAM auth as the thing to verify first |

So the shipped default is **25 (+5 reserve) real backends** — smaller than the "~50–100" in the worked example above, which is the *1,000-user* shape. 25 is comfortable for a few hundred users; the sizing tool in the interactive edition shows when to raise it. Remember `DEFAULT_POOL_SIZE` is **per pooler instance**: two PgBouncers each with 25 can open 50 real connections.

**How big should the server pool be?** Two rules of thumb, both approximate: (1) Postgres does not gain throughput past a few connections per CPU core — beyond roughly `2 × cores + spindles` extra backends only add contention; (2) by Little's law the number of busy backends ≈ *queries per second × mean query time*. 600 queries/s at 8 ms average is ~5 busy backends on average, so a pool of 25 has headroom for bursts and slow queries; the same 600 q/s at 80 ms average (a tile storm) needs ~48 and the pool becomes the queue. When clients wait, PgBouncer's `cl_waiting` rises (see §13).

**Check it on a live system** (plain SQL, run against the primary): 

```sql
SHOW max_connections;
SELECT count(*) AS in_use, count(*) FILTER (WHERE state = 'active') AS active
  FROM pg_stat_activity WHERE datname = current_database();
```

PgBouncer's own `SHOW POOLS;` / `SHOW STATS;` admin console is the standard way to read `cl_active`, `cl_waiting`, `sv_active`; whether the `edoburu/pgbouncer` image used here exposes the admin console with the credentials in the Compose file is **not verified** — check before relying on it.

> **Transaction pooling caveat:** it does not support session-level features (prepared statements held across transactions, `SET` that must persist, `LISTEN/NOTIFY`, advisory locks held across statements). Verify the ORM path (bun) tolerates it; if a specific path needs session pooling, give that path a second, session-mode PgBouncer. Test under load before trusting it.
>
> *What the code review found (not a substitute for a load test):* `pgdriver` v1.2.18 formats query arguments **client-side** (`formatQueryArgs`) and sends them as text queries, so ordinary `db.Query`/`NewSelect` paths do not create server-side prepared statements; a grep of `api/` finds **no** `LISTEN`/`NOTIFY`/`pg_notify` and no advisory locks. The risk spots are explicit transactions (`RunInTx` is fine — a transaction pins one backend for its duration) and anything that issues `SET` outside a transaction. Migrations deliberately bypass the pooler: the Compose `bootstrap` sidecar connects straight to Postgres "because DDL/migrations want a stable session".

### 5.2 Read replicas + read/write split

- **Run 2–3 streaming replicas.** Use a Postgres operator (**CloudNativePG** or the Zalando operator) to manage primary + replicas, automated failover, backups and PITR. Do **not** hand-roll replication for a production utility system.
- **Route reads to replicas.** This is the capacity multiplier — and it is **code work**: VoltEdge currently uses one pool/DSN for everything. To use replicas you need the app to send read-only queries (tiles above all, then traces, feature reads, metadata) to a **read-only pool** pointed at the replica service, and keep writes on the primary pool. The highest-value single step is giving the **tiles package its own read-only DB handle** against replicas (§6) — tiles are the dominant read load and are trivially read-only.
- **Mind replication lag.** Replicas are eventually consistent (usually <1s). Reads that must be read-your-own-write (e.g. immediately re-reading a feature you just edited) should stay on the primary or tolerate the lag. Tiles and analysis are lag-tolerant; the edit-then-reload path is not — keep it on the primary.

**A routing table to design the read-only handle against** (the `Route to` column is a recommendation; *every* row is on the primary today):

| Workload | Route to | Why |
|---|---|---|
| Tile render (`/tiles/{code}/{z}/{x}/{y}`) | **replica** | pure read, dominant load, lag-tolerant. **Caveat:** a user's private-draft overlay reads `runtime.feature_pending_edits`; an author who just staged an edit may not see it on a lagging replica — either keep overlay-active users on the primary or accept ≤ lag |
| Trace / load-flow / fault / contingency reads | replica | pure reads of the network graph; lag-tolerant |
| Feature list / get | replica, **except** immediately after a write by the same user | read-your-own-write |
| Metadata reads (`metadata.Load` …) | replica (or cache — §7.2) | rarely change |
| Session lookup (`token_sha256`) | **primary** | a revoked/expired session must take effect immediately; a lagging replica could still honour it |
| Territory enablement flag (`iam.system_settings`) | primary | security setting; keep consistent |
| Every INSERT/UPDATE/DELETE, queue claims (`SKIP LOCKED`), migrations | **primary** | writes; `FOR UPDATE` cannot run on a standby |
| Worker `UPDATE … RETURNING` claims | primary | same |

Lag is measurable: on the primary, `SELECT application_name, state, replay_lag FROM pg_stat_replication;` (PostgreSQL 10+). Alert when `replay_lag` exceeds the staleness you promised in the table above.

### 5.3 PostGIS specifics

- Ensure spatial indexes (GiST) exist on every `geom` column the tile and trace queries hit. *Verified:* the compiler emits `CREATE INDEX … USING GIST (geom)` when it creates a table (`api/internal/compiler/compiler.go:65`) and a btree index on `territory_id` for every geometric table (the scoped tile predicate, `compiler.go:~242`). Still confirm under `EXPLAIN (ANALYZE, BUFFERS)` on a real tile query that the bbox `&&` filter uses the GiST index and the `territory_id IN (…)` filter does not force a sequential scan.
- Replicas inherit indexes; no extra work.
- Tune `shared_buffers`, `work_mem`, `effective_cache_size` for the box; `ST_AsMVT` is memory-sensitive under concurrency.

---

## 6. The tile path — the hardest and highest-volume problem

Every map tile is a **live `ST_AsMVT` query against Postgres, rendered per-user**, scoped to the user's territory *and* their private-draft overlay, with **no caching of any kind** (`api/internal/tiles/tiles.go`). At 1,000 users panning, this is by far your largest DB load, and per-user scoping blocks the usual "slap a CDN in front" fix.

**What one tile request actually costs the database** (read from `tileHandler`/`renderTile`, `tiles.go:95-300`; counts are by reading the code, not a profile):

| # | Round trip | Where |
|---|---|---|
| 1–2 | session row + user row (`SessionLoader`) | `auth/session.go` |
| 3 | `loadTileClass` — feature-class lookup by `lower(code)` | `tiles.go:148` |
| 4 | `territory.EnforcementEnabled` — reads `iam.system_settings` | `tiles.go:175`, `territory/assign.go:26` |
| 5 | `propertyColumns` — an `information_schema.columns` introspection, **per tile** | `tiles.go:317` |
| 6 | `drafts.OverlayAuthor` — is the private-draft overlay active for this actor | `tiles.go:~217` |
| 7 | the `ST_AsMVT` query itself | `tiles.go:~300` |

So a single tile is **~7 short round trips, only one of which is the expensive spatial query** — which matters twice: through PgBouncer each is a transaction checked out of the pool, and any one of them (columns introspection, class lookup, the settings flag) is a candidate for a cheap in-process TTL cache (§7.2) before you invest in the architectural options below. Responses carry only `Content-Type: application/x-protobuf` — **no `Cache-Control` or `ETag`** — and the nginx `/tiles/` location proxies straight through with no `proxy_cache`. (`martin` is still in the Compose file, started with `--cache-size 0`, but is marked superseded as the map origin.)

Order-of-magnitude (illustrative): if ~150–200 users are *actively* panning at once, that's ~300–600 `ST_AsMVT`/sec, each 10–40 ms of Postgres CPU — multiple cores on tiles alone. That does not hold on one primary. The fix is layered:

1. **Route tile queries to read replicas (do this first).** Pure reads, lag-tolerant. A dedicated read-only handle for the tiles package spreads the dominant load across replicas with no semantic change. This alone buys a lot.
2. **Make tiles cacheable by *scope class*, not per-user (the architectural fix).** The deciding question: **how many distinct territory-scopes do your users actually have?**
   - If access is **coarse** (most users see the whole network, or one of a handful of regions), change the cache key to `(scope_group, has_drafts, z, x, y)`. Now hundreds of users share tiles → a reverse-proxy/CDN cache becomes viable and the DB load collapses.
   - If every user has a **bespoke** territory set, shared caching is dead; you're on replicas + raw horsepower.
3. **Split base vs overlay.** Serve the **published network** from a shared tile cache (or pre-generated **pmtiles** refreshed on edit); render the **private-draft overlay** as a thin per-user layer composited client-side. Drafts are a tiny fraction of features, so ~95% of tile bytes come from the shared cache.
4. **Add HTTP cache headers** (`Cache-Control`, `ETag`) even on per-session tiles so browsers and any shared cache can revalidate instead of re-rendering on every pan.

**Worked example — how many cores do tiles cost?** Take the manual's own range: 175 users actively panning, each panning user requesting ~2.5 tiles/s → ~440 `ST_AsMVT`/s at 25 ms each = **11 cores** of Postgres CPU on tile queries alone, before the six other round trips per tile. Split across 2 replicas: ~5.5 cores each. Now apply a scope-class cache with 8 distinct scope classes (×2 for `has_drafts` = 16 keys) and 175 users: roughly 91 % of tile requests share a key with someone else; if the cache serves that 91 % (and a CDN absorbs repeated viewport requests), the database sees ~40 tiles/s and **~1 core**. The calculator in the interactive edition runs this arithmetic with your numbers. The cache-hit model is deliberately simple (it ignores cold starts and tile-update invalidation) — treat it as an order of magnitude.

**How to count your scope classes.** The decision in #2 is a count of *distinct effective territory sets* among your users. The tile path asks `user.TerritoryFilter()` for an `allowed` set, or "unscoped" (the `territories:unscoped` permission), or "enforcement off" (no predicate at all). Users with the same allowed set — or all unscoped users — can share a cache key; one user with a bespoke set needs their own. The interactive edition has a small tool for grouping users this way.

Decide #2 early — it reshapes the whole read tier.

---

## 7. Keeping responsiveness under load ("don't lose its effect")

Three code-level changes preserve snappiness when the system is busy. None is large.

1. **Concurrency gate on analysis/trace** *(not implemented — no semaphore exists in `powerflow` or `trace`)*. Load-flow, fault, contingency, nodal, arc-flash and trace all run **synchronously in the HTTP handler with no limit** (`api/internal/powerflow/handler.go`, `trace/handler.go`). A few concurrent heavy solves can pin every core on a pod and starve the viewers sharing it. Add a **weighted semaphore** so each pod runs at most *k* heavy solves at once and returns `429 / queue` beyond that. This converts a silent collapse into graceful back-pressure. (For sustained analyst load, later move studies to an analysis **worker pool** using the same `SKIP LOCKED` pattern as exports, off the request tier entirely.)
2. **TTL cache for metadata** *(not implemented)*. `metadata.Load` runs **five queries per call** — class, active fields, subtypes, terminals — plus one for subtype field-rules when subtypes exist and the domain/coded-value lookups for every field that has a domain (`api/internal/metadata/load.go:61-120`) — and is called from ~66 sites, so a request that loads a class pays that on every call. Feature classes, fields, subtypes, domains and symbology change rarely. A 30–60 s in-process cache per pod removes a large slice of baseline DB load. Accept ≤60 s staleness after a metadata edit (same bound the access cache already uses).
3. **Mind the per-instance access cache** *(exists)*. The territory/subordinate-scope cache (`CachedEffectiveIDs`, `CachedSubordinates`) is in-memory per pod with a 60 s TTL (`effectiveCacheTTL`, `api/internal/access/cache.go:18`). Under *N* pods, a grant/revoke or manager change is "instant" only on the pod that handled it; others serve stale scope for ≤60 s. If **instant revocation** is a security requirement, either drop the TTL or add cross-instance invalidation (Redis pub/sub, or a NOTIFY-driven flush). Otherwise accept the bound and document it. Worked timeline: you revoke a territory from a user at *t = 0* and the request lands on pod B. Pods A and C each cached that user's scope at some moment in the previous 60 s, so A may keep serving the old scope until `t ≈ 0–60 s` and C likewise; the **worst case is 60 s** and the average over randomly aged entries is ~30 s. Sessions are different: they are reloaded from Postgres on every request, so logging a user out or revoking their session is effective on every pod immediately.

*Sketch of the concurrency gate (illustrative, not in the repo):*

```go
// k heavy solves per pod; beyond that, shed load instead of queueing forever.
var heavy = semaphore.NewWeighted(k) // golang.org/x/sync/semaphore
func gate(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if !heavy.TryAcquire(1) { http.Error(w, "busy, retry shortly", 429); return }
        defer heavy.Release(1)
        next.ServeHTTP(w, r)
    })
}
```

Pick *k* near the pod's CPU limit in cores (2 per the starting resources above): the point is to keep the viewers' tile and read requests from competing with more solves than there are cores.

---

## 8. Worker fleet

The compute-heavy / long-running work is already off the request path as separate processes draining Postgres queues with `SKIP LOCKED` — so they scale **horizontally by replica count** with no coordination:

| Worker | Scales how | Notes |
|---|---|---|
| `exportworker` | N replicas | `SKIP LOCKED`; `--scale` safe. |
| `importworker` | N replicas | ETL ingest; bursty — scale on queue depth. |
| `webhookworker` | N replicas | Inbound ingestion apply. |
| `mcpserver` | N replicas | AI tool API; stateless like the main API. |
| `fieldsync` | N replicas | Both halves claim work with `FOR UPDATE SKIP LOCKED`, and each build runs in its **own** temp dir (`os.MkdirTemp` per job) — so replicas share the build + sync queues with no coordination (the process already runs concurrent builds in isolated temp dirs via `FIELDSYNC_WORKER_CONCURRENCY`). Builds are CPU-heavy (tippecanoe), so give each pod CPU **and** add replicas on backlog. **One caveat:** the maintenance loop (shared-pack scheduling + retention, §8 note) runs in every replica, so a scheduled rebuild can be double-enqueued on a tick race — harmless (retention reaps the extra). Leader-elect just that loop if you want to avoid it entirely. |

Scale export/import/webhook workers on **queue depth** (a custom metric: rows pending in the job table) rather than CPU — they're I/O- and burst-shaped.

**How the claim loop works, and why replicas need no coordination.** Every queue uses the same pattern (shown for exports, `api/internal/export/job.go:110-127`): one `UPDATE … WHERE id = (SELECT id … WHERE status = 'queued' OR (status = 'running' AND lease_expires_at < now() AND attempts < N) ORDER BY created_at FOR UPDATE SKIP LOCKED LIMIT 1) RETURNING *`. `SKIP LOCKED` makes two workers claim *different* rows; the **lease** (`lease_expires_at`, 60 s by default, extended by a heartbeat while the job runs) lets a surviving worker reclaim a job whose worker died, and `attempts` caps the requeue loop (3 for exports, `maxExportAttempts`; `WEBHOOK_MAX_ATTEMPTS` / `WEBHOOK_OUT_MAX_ATTEMPTS` for webhooks, after which a delivery goes `dead`). A worker acquires a concurrency slot **before** claiming, so it never holds a claimed job it cannot start.

| Process | Concurrency env (default) | Lease env (default) | Queue table |
|---|---|---|---|
| `exportworker` | `EXPORT_WORKER_CONCURRENCY` (2) | `EXPORT_JOB_LEASE` (60 s) | `ops.export_jobs` |
| `importworker` | `IMPORT_WORKER_CONCURRENCY` (2) | `IMPORT_JOB_LEASE` (60 s) | `ops.import_jobs` |
| `webhookworker` — inbound | `WEBHOOK_WORKER_CONCURRENCY` (2) | `WEBHOOK_DELIVERY_LEASE` (60 s) | `ops.webhook_deliveries` |
| `webhookworker` — outbound (same process) | `WEBHOOK_OUT_CONCURRENCY` (2) | `WEBHOOK_OUT_LEASE` (60 s) | outbound deliveries (`webhookout`) |
| `fieldsync` — pack builds | `FIELDSYNC_WORKER_CONCURRENCY` (2) | `FIELDSYNC_JOB_LEASE` (60 s) | `ops.field_packs` |
| `fieldsync` — sync apply | `SYNC_WORKER_CONCURRENCY` (2) | `SYNC_JOB_LEASE` (60 s) | sync inbox |

(The poll interval default is 2 s in every worker — a new job waits up to ~2 s to be noticed.) **Throughput** is `replicas × concurrency ÷ job duration`; **drain time** for a backlog of *B* jobs is `B × duration ÷ (replicas × concurrency)`. Example: 120 pack builds of 40 s with 2 slots on 1 replica = `120 × 40 / 2` = 2,400 s (40 min); on 4 replicas = 600 s (10 min) — *if* Postgres and the CPU can absorb it. Each running job holds database connections, so `replicas × concurrency` is also a connection multiplier (§5.1). Check the depth with `SELECT status, count(*) FROM ops.export_jobs GROUP BY status;` (the same query shape works for `ops.import_jobs`, `ops.webhook_deliveries` and `ops.field_packs`). Two operational notes: `webhookworker` runs **both** the inbound and outbound loops in one process, and in `docker-compose.prod.yml` the `mcpserver` service keeps a fixed `container_name` and published port, so both must be removed (and the service added to nginx) before it can be scaled.

**Reducing the field-pack build load (shared packs).** Because the builder is the **most expensive** component to scale out (CPU-heavy `tippecanoe`, so each replica needs real cores — although replicas *are* safe, see the table), the highest-leverage move for a large fleet is to **build less**. The heaviest input is many crews building the *same* area — 60 crews on one depot is 60 identical builds. **Shared packs** remove that duplication: pre-build a common area **once** as a reusable *cell* (overnight, in an off-peak window), and every crew whose access covers it picks up that single build instead of queuing their own. It's the pack-side analogue of the "coarse scope → shared cache" tile strategy (§6), and for a geographically clustered fleet it's the single biggest reduction in build-queue depth. The scheduling + retention knobs (`FIELDSYNC_BUILD_WINDOW`, `FIELDSYNC_MAINTENANCE_INTERVAL`, `FIELDSYNC_KEEP_VERSIONS`) live on the `fieldsync` process; see the **Field Tool Admin Guide §6** for defining cells, scheduling, retention, and the usage dashboard.

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

**Where each item stands in the repo today** (so you know which are configuration and which are code):

| # | Item | Status today | Kind of work |
|---|---|---|---|
| 1 | Bound the pool | **Not done** (`main.go:93`); `DATABASE_MAX_OPEN_CONNS` is proposed only | code (3 lines + env parse) |
| 2 | PgBouncer | **Done in `docker-compose.prod.yml`** (auth & prepared-statement behaviour unverified); CNPG pooler in the k8s guide | config |
| 3 | Health checks | **Done** for `/healthz`; `/readyz` does not exist | none / optional code |
| 4 | Concurrency gate | **Not done** | code |
| 5 | Replicas + read routing | Replica is a commented opt-in; routing is **not done** | infra + code |
| 6 | Tile caching decision | **Open decision**; no caching exists | decision, then code |
| 7 | Metadata TTL cache | **Not done** | code |
| 8 | Edge rate-limit | **Done for login** in `nginx.conf`; webhook ingress has only the in-app limiter; see §3.1 for the shared-bucket caveat | config |
| 9 | Object storage | RustFS single node in Compose — a SPOF | infra |
| 10 | Observability | `docker-compose.observability.yml` ships Loki, Vector, Grafana; the §11 alerts are **yours to define** | config |
| 11 | Postgres operator | In the k8s guide (CloudNativePG); not in Compose | infra |
| 12 | Load test | n/a — no load-test harness is in the repo (not verified) | you |

**Gating rule used by the interactive scorer:** items 1 and 2 are hard gates (no scale-out without both, or Σ(pods × pool) strictly under `max_connections`); items 3–5 are strongly recommended before ~10 pods; 6–12 are staged — do them as the §11 metrics ask for them.

---

## 10. Schema migrations & the compiler in a replicated world

VoltEdge compiles metadata → physical `gis.<class>` tables (`task compile`) and runs SQL migrations. With N live replicas you need **zero-downtime, backward-compatible** changes:

- **Additive, nullable first.** The compiler is *additive-only*: it creates tables and runs `ALTER TABLE … ADD COLUMN`, and **never drops or alters an existing column** (`api/internal/compiler/ddl.go` package comment) — a "disabled" field is a metadata status flip, not a `DROP COLUMN`. That is what lets old and new API replicas run against the same schema during a rolling deploy. **Correction to the earlier text:** the compiler does *not* automatically add a new required field as nullable and backfill it. A field defined with `nullable = false` is added as `ADD COLUMN … NOT NULL` in one step (`compiler.go:~200`, `ddl.go:78`), which Postgres rejects on a table that already has rows. The safe sequence is therefore a *metadata-author* discipline: add the field **nullable**, backfill, then tighten it in a later release. Keep every migration backward-compatible with the currently-running image.
- **Run migrations once**, as a pre-deploy `Job` (or init container with a leader lock), **not** from every pod on boot — concurrent migration runs race. *Verified:* `migrations/apply.sh` takes **no lock** — it checks `ops.migration_log` and then applies, so two concurrent runs can both decide a file is unapplied. Run exactly one (Compose: the `bootstrap` sidecar, `docker compose -f docker-compose.prod.yml exec bootstrap bash /workspace/migrations/apply.sh`; Kubernetes: the `deploy/migrate` image as a Job, which carries the baked SQL and is idempotent per file). Gate app readiness on "schema at expected version."
- **Deploy order for a breaking change:** expand (add new nullable column / table) → deploy code that writes both old and new → backfill → deploy code that reads new → contract (drop old) in a later release. Never expand and contract in the same release while replicas overlap.
- **A compile** that rewrites a physical table takes a lock — schedule it in a low-traffic window and verify it's additive. The data dictionary (`data-dictionary.md`) and the compiler notes describe which changes are additive vs rewriting. Compile one class with `task compile CODE=<class>` or all with `task compile-all` (idempotent, both in `Taskfile.yml`). Each compile also refreshes the class's Martin tile function (`gis.sync_tile_function`), rebuilds subtype/domain `CHECK` constraints, and keeps the territory index — so a compile is not free even when it adds nothing.

**Which changes are safe during a rolling deploy** (old and new pods overlap for minutes):

| Change | Safe to ship with overlapping replicas? | How |
|---|---|---|
| Add a nullable column / new table | **Yes** | old code ignores it |
| Add a NOT NULL column to a populated table | **No** | add nullable → backfill → constrain in a *later* release |
| Add an index (`CREATE INDEX CONCURRENTLY`) | Yes | plain `CREATE INDEX` blocks writes |
| Rename a column | **No** (old pods break instantly) | add new, dual-write, backfill, switch reads, drop old later |
| Drop a column | Only in the **contract** release, after no running image reads it | the compiler will not do it for you |
| Change a column type | **No** | add new column + migrate, as with a rename |
| Add a subtype / coded-domain value | Yes | `CHECK` constraints are rebuilt on recompile; narrowing can reject existing rows |

Worked example (rename `phase` → `phase_code`): release 1 adds `phase_code` and deploys code that writes both; a backfill copies old rows; release 2 reads only `phase_code`; release 3 (only now) drops `phase`. At no point do two live versions disagree about which column exists.

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
1. **Compose, scaled:** this ships as **`docker-compose.prod.yml`** + **`infra/nginx/nginx.conf`** — nginx (non-sticky) in front of N `api` replicas, PgBouncer between the app tier and Postgres, workers (including `fieldsync`) scaled by replica count, and a replication-ready primary (with the streaming read replica left as a documented, commented opt-in). Run it with `docker compose -f docker-compose.prod.yml up -d --build --scale api=4`. Good to a few hundred users and it *proves* the multi-replica architecture on one host. **Note:** this file is authored but not yet run in CI — see its header "verify" steps (PgBouncer auth against SCRAM, and whether pgdriver's prepared statements tolerate transaction pooling) before relying on it.
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
| **fieldsync** | runbook §7 · k8s B7 | pack-build backlog | more replicas (`SKIP LOCKED`, isolated temp dirs) **and** more CPU per pod (tippecanoe) | builds are CPU-heavy; the maintenance loop may double-enqueue a scheduled rebuild across replicas (benign — retention reaps it) |
| **mcpserver** | runbook §7 · k8s B7 | MCP request CPU | more replicas (stateless like the API) | same DB-connection ceiling as the API |
| **Object storage (RustFS/S3)** | runbook data-tier · k8s B4 | storage IO or capacity; upload/download latency | move to real **S3** or a MinIO **cluster**; scale that out | single-node RustFS is a SPOF — externalise for HA |
| **Observability (Loki)** | logging guide | log-ingest volume | scale Loki ingesters / retention | separate plane; never let it share Postgres |

**How to read it in an incident:** the alert that fired (§11) points at a row; the row tells you the action and whether scaling that component even helps. The one row where "scale it out" is the *wrong* answer is **Postgres primary** (vertical/architectural, not horizontal — writes don't scale out). Everything else — `fieldsync` included — is "add replicas" (CPU-heavy ones for `fieldsync`), gated on the §9 pre-flight being done.

---

## Bottom line

VoltEdge is **architecturally ready** to scale: the API tier is already stateless, workers are already separated, storage is already externalized. Reaching 1,000 concurrent users is a **tuning pass plus one design decision**, not a rewrite:

- **The must-fixes are small:** bound the pool, add PgBouncer, gate analysis concurrency, cache metadata, add health endpoints.
- **The capacity multipliers are infrastructure:** read replicas, non-sticky LB, autoscaled API replicas, edge rate-limiting.
- **The one genuine design decision is tile caching** — and its answer depends entirely on how coarse your territory-access model is.

Get §9 done, make the tile call in §6, and the system keeps its responsiveness at a scale most utility GIS never has to reach.
