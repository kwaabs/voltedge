# Production Setup Runbook

A step-by-step procedure for standing up VoltEdge in production with the
multi-replica stack (`docker-compose.prod.yml` + `infra/nginx/nginx.conf`). This
is the **how-to-install**; its companion [Production Deployment & Scaling](deployment-and-scaling-guide.md)
is the **why/architecture** (replicas, read replicas, the scaling ceilings).
Read that one for the reasoning; follow this one to bring the system up.

This is written for the operator doing the deployment. It assumes Docker +
Docker Compose v2, the repository checked out on the host, and (for building the
SPA) Node.js. The commands are the production equivalents of the dev
`task migrate` / `task seed-all` / `task authadmin-*` flows, pointed at the
production stack.

> Throughout, `PC` is shorthand for `docker compose -f docker-compose.prod.yml`.
> Set it once: `PC="docker compose -f docker-compose.prod.yml"`.

---

## 0. What you are standing up

```
nginx (:80/:443, serves the SPA + proxies /api and /tiles)
  -> N api replicas (stateless)
       -> PgBouncer (transaction pool)
            -> Postgres (PostGIS)      [+ optional read replicas]
  workers: exportworker, webhookworker, importworker, fieldsync (scale by replicas),
           mcpserver
  rustfs (S3-compatible object storage for attachments/exports)
  bootstrap (one-shot sidecar: psql + migrations; does nothing until exec'd)
```

The API image (`api/Dockerfile`) bakes every binary you need — `server`,
`exportworker`, `webhookworker`, `importworker`, `mcpserver`, and the ad-hoc
`authadmin` / `resetdata` admin tools — and the compose service picks which to
run via its `command`. So `$PC exec api authadmin …` runs the admin CLI with the
API's exact environment.

---

## 1. Prerequisites & host sizing

- **Docker Engine + Compose v2**, a host (or VM) with enough headroom. A sane
  single-host starting point for a few hundred users: **8 vCPU / 16 GB RAM / SSD**.
  Postgres and the `api` replicas are the CPU/RAM consumers; give Postgres the
  bulk. (For ~1,000 users you move Postgres and the replicas off one host — see
  the scaling guide.)
- **The repository on the host** (the compose files, `infra/`, `migrations/`,
  `seed/`, and `web/` all come from it).
- **Node.js** on the host (or a CI step) to build the SPA in §4.
- A **DNS name + TLS certificate** for the public URL (§6). Secure session
  cookies require HTTPS — this is not optional in production.

---

## 2. Secrets & environment (`.env`)

Compose reads `.env` from the repo root. Copy the template and harden every
value — **do not ship the dev defaults**:

```
cp .env.example .env
```

Set, at minimum:

| Variable | Set to | Why |
|---|---|---|
| `POSTGRES_PASSWORD` | a long random secret | DB superuser. `openssl rand -base64 24` |
| `RUSTFS_ACCESS_KEY` / `RUSTFS_SECRET_KEY` | strong random | object-storage creds |
| `RUSTFS_CORS_ORIGINS` | your web origin (e.g. `https://gis.utility.com`) | **not** `*` in prod — browser-direct attachment PUTs |
| `RUSTFS_PUBLIC_ENDPOINT` | the public host the **browser** reaches RustFS at | presigned attachment/export URLs are signed for it (see §7) |
| `RUSTFS_PUBLIC_USE_SSL` | `1` | if that endpoint is HTTPS |
| `EXTERNAL_SYSTEMS_ENCRYPTION_KEY` | `openssl rand -base64 32` | AES key for stored external-system credentials (only if you use external API links) |
| `AUTH_INSECURE_COOKIES` | **unset / `0`** | the dev template sets `1`; in prod cookies MUST carry the Secure flag → requires HTTPS |
| `SESSION_IDLE_TIMEOUT` / `SESSION_ABSOLUTE_MAX` | your policy (defaults 12h / 720h) | session lifetimes |
| `LOG_FORMAT` / `LOG_LEVEL` | `json` / `info` | structured logs for a shipper |
| `LOKI_READ_TOKEN` | `openssl rand -hex 32` | only if you run the observability overlay (§9) |
| `PGBOUNCER_POOL_SIZE` / `PGBOUNCER_MAX_CLIENT_CONN` | per §5 of the scaling guide | pooler sizing |
| `API_REPLICAS`, `EXPORTWORKER_REPLICAS`, … | your scale | replica counts |

**SSO (optional):** to enable Azure AD / Google, set the `OIDC_*` block. The
redirect URL registered with the provider must be
`https://<your-host>/api/auth/oidc/<provider>/callback` (note the `/api` prefix —
nginx strips it to the backend's `/auth/oidc/...`). SSO never creates accounts;
pre-provision the user's email first (§8).

> Keep `.env` out of version control (it already is, via `.gitignore`) and off
> shared disks. For `AUTHADMIN_PASSWORD` / `RESETDATA_PASSWORD`, pass them
> just-in-time on the command line (§8), never leave them in `.env`.

---

## 3. Build the images

```
$PC build
```

This builds the API image (all binaries) and the `fieldsync` image (bundles
tippecanoe). The `nginx`, `postgres`, `pgbouncer`, `rustfs` images are pulled.

---

## 4. Build the web SPA

The React app is a static bundle nginx serves; the backend does not serve it.
Its API base is hard-wired to `/api` (`web/src/lib/api.ts`), so no build-time env
is needed — nginx maps `/api` and `/tiles` to the backend.

```
cd web
npm ci
npm run build        # produces web/dist
cd ..
```

`docker-compose.prod.yml` mounts `./web/dist` into the nginx container read-only.
Rebuild and `$PC restart nginx` whenever the frontend changes. (If you prefer a
CDN/separate static host, drop that mount and point the host's `/api` + `/tiles`
at the API instead.)

---

## 5. Start the data tier first

Bring up Postgres, the pooler, object storage, and the bootstrap sidecar — but
not yet the app — so you can migrate before any API replica connects:

```
$PC up -d postgres pgbouncer rustfs bootstrap
```

Wait for Postgres healthy: `$PC ps` should show `voltedge-postgres` healthy. The
`voltedge-exports` bucket is auto-created by the services on first use — no
manual bucket step.

---

## 6. Bootstrap the database

### 6a. Apply migrations

The `bootstrap` sidecar has `psql` + the repo mounted and connects directly to
Postgres (not through PgBouncer — DDL wants a stable session):

```
$PC exec -T bootstrap bash /workspace/migrations/apply.sh
```

Verify: `$PC exec -T bootstrap psql "$DATABASE_URL" -c "SELECT filename, status FROM ops.migration_log ORDER BY id;"`

### 6b. Load the schema + seed data

Two honest paths — pick by whether this is your first environment or a
promotion:

- **Greenfield (first environment ever):** the full metadata + compiled schema is
  produced by the authoring flow `task migrate` + `task seed-all` (100+ ordered,
  idempotent seeds that load class metadata, **compile every class** into its
  `gis.<code>` table, then load the network seeds). This is a one-time authoring
  activity. Run it against a **staging** stack, verify, then dump it (below) — do
  not hand-run 100 seeds against production.
- **Production (the normal path):** **restore a validated dump** of the
  staging/reference database (schema + metadata, and whatever reference data you
  want pre-loaded):

  ```
  # on staging, after task seed-all + verification:
  pg_dump "<staging-dsn>" -Fc -f voltedge-seeded.dump

  # into production (through the bootstrap sidecar or any psql host):
  pg_restore --no-owner -d "<prod-dsn>" voltedge-seeded.dump
  ```

  Then re-run **6a** so any migrations newer than the dump are applied forward.

This mirrors how utilities actually provision: build + validate the model once,
restore it, then roll migrations forward on every upgrade (§10).

### 6c. Create the first break-glass admin

`authadmin` is baked into the API image; run it with the password passed
just-in-time (never stored):

```
$PC exec -e AUTHADMIN_PASSWORD='<a-strong-password>' api \
  authadmin create --email admin@utility.com --name "Break-glass Admin"
```

Grant roles / list accounts the same way: `$PC exec api authadmin list`,
`$PC exec api authadmin grant-role --email <e> --role <role>`
(`authadmin roles` lists them). SSO users are pre-provisioned here by email
before their first login.

For an AI/MCP integration token: `$PC exec api authadmin mint-token --email <e>
--name <label>` (printed once).

---

## 7. Start the application tier

```
$PC up -d                      # brings up api (×API_REPLICAS), workers, nginx, mcpserver, fieldsync
```

Scale on demand:

```
$PC up -d --scale api=6 --scale exportworker=3
```

(`fieldsync` can scale by replicas too — its build and sync workers both claim work
with `SKIP LOCKED` and build in isolated temp dirs. Scale it on pack-build backlog,
with CPU-heavy pods for tippecanoe. One caveat: its shared-pack maintenance loop runs
in every replica, so a scheduled cell rebuild can be harmlessly double-enqueued —
retention reaps the extra, or leader-elect that loop to avoid it.)

**RustFS public reachability (don't skip):** attachments and exports are uploaded
and downloaded **by the browser directly to RustFS** via presigned URLs. The
browser must be able to resolve and reach `RUSTFS_PUBLIC_ENDPOINT`. Either
publish RustFS on a public host with TLS (and set `RUSTFS_PUBLIC_ENDPOINT` +
`RUSTFS_PUBLIC_USE_SSL=1`), or add an nginx `location /s3/` that proxies to the
`rustfs` service and point the public endpoint at that. If presigned downloads
404 in the browser, this is almost always why.

---

## 8. TLS

Secure cookies (and therefore login) require HTTPS to the browser. Two options:

- **Terminate at an external LB / proxy** (cloud L7 LB, Caddy, Cloudflare) in
  front of nginx:80. Simplest; the LB holds the cert.
- **Terminate at this nginx:** uncomment the `443` port, mount your cert/key, and
  add a TLS `server` block (and redirect `:80` → `:443`). Keep the `/api`,
  `/tiles`, and SPA locations identical.

Either way, confirm `AUTH_INSECURE_COOKIES` is unset/`0` (§2) and that the
browser reaches the app over `https://`.

---

## 9. Observability (optional but recommended)

Layer the opt-in Loki + Grafana + Vector overlay on top (see the
[Logging & Observability guide](logging-and-observability-guide.md)):

```
LOKI_READ_TOKEN=$(openssl rand -hex 32) \
  $PC -f docker-compose.observability.yml up -d
```

Grafana comes up on `GRAFANA_PORT`; external log consumers go through the
bearer-token proxy on `LOKI_PROXY_PORT`. Set the alerts from §11 of the scaling
guide (DB connection saturation, replication lag, tile p95, worker queue depth).

---

## 10. Upgrades (zero-downtime)

Because the API tier is stateless and the schema changes are additive-first, you
can roll forward without downtime:

1. **Build the new image + SPA** (§3, §4).
2. **Apply migrations first**, once, via the bootstrap sidecar (§6a). They are
   backward-compatible with the currently-running image (the compiler's
   additive-nullable pattern guarantees old replicas keep working).
3. **Roll the API tier:** `$PC up -d --no-deps api` recreates replicas on the new
   image. With `API_REPLICAS ≥ 3` and the nginx health check, drop one at a time
   (or use a rolling strategy) so there's always a healthy replica.
4. **Roll the workers:** `$PC up -d --no-deps exportworker webhookworker importworker mcpserver fieldsync`.
5. For a **breaking** schema change, use expand → migrate → deploy → contract
   across two releases (never expand and contract while old replicas overlap) —
   see §10 of the scaling guide.

---

## 11. Backups & DR

- **Postgres:** scheduled `pg_dump -Fc` at minimum; for real RPO use continuous
  archiving / PITR (a Postgres operator — CloudNativePG/Zalando — gives you this
  plus replica failover; strongly recommended once you add read replicas).
- **RustFS/object storage:** back up or replicate the bucket — attachments and
  exports live only there, not in Postgres.
- **Test a restore.** A backup you have never restored is a hope, not a backup.
- The break-glass **factory reset** (`resetdata`) is a destructive maintenance
  tool, not a backup; keep `RESETDATA_PASSWORD` out of `.env`.

---

## 12. Verify the deployment

1. `$PC ps` — every service up; `api` shows your replica count; `fieldsync` is 1.
2. **Health:** each `api` replica's `/healthz` is green (it checks Postgres through
   PgBouncer) — `$PC exec api wget -qO- http://127.0.0.1:8080/healthz`.
3. **Login:** open `https://<host>/`, sign in as the break-glass admin.
4. **Map/tiles:** the map renders (confirms the `/tiles` proxy + territory-scoped
   MVT path).
5. **A write + a read:** create a feature, run a trace (confirms the DB write path
   and the synchronous analysis path).
6. **An attachment:** upload one (confirms RustFS presign + public endpoint).
7. **Scale test:** `$PC up -d --scale api=4`; confirm nginx spreads load (the
   access log's `upstream=` varies) without re-login (confirms stateless
   sessions).

---

## 13. Troubleshooting

| Symptom | Likely cause | Fix |
|---|---|---|
| API can't connect / "too many clients" | DB pool unbounded × many replicas, or PgBouncer pool too small | confirm app points at `pgbouncer`; raise `PGBOUNCER_POOL_SIZE` / Postgres `max_connections` (§5 scaling guide) |
| "prepared statement does not exist" under load | pgdriver prepared statements vs PgBouncer **transaction** pooling | set `POOL_MODE: session` on the pgbouncer service, or disable statement caching in the DSN |
| PgBouncer refuses app logins | client auth vs supabase SCRAM | adjust `AUTH_TYPE` on the pgbouncer service (plain within the private net, or scram + auth_query) |
| Presigned attachment/export URLs 404 in browser | `RUSTFS_PUBLIC_ENDPOINT` not browser-reachable | point it at a public host (or proxy RustFS through nginx) — §7 |
| Login fails silently / cookie not set | `AUTH_INSECURE_COOKIES` still `1`, or site not HTTPS | unset it and serve over HTTPS — §8 |
| `--scale` errors "container name already in use" | a `container_name` on a scaled service | only singletons carry `container_name` here; don't add one to `api`/workers |
| SPA loads but every call 404s | `web/dist` not built/mounted, or `/api` proxy wrong | rebuild SPA (§4), confirm the nginx `/api` + `/tiles` locations |
| Map blank, features missing | classes not compiled into `gis.<code>` | re-run the seed/compile step or restore a seeded dump — §6b |

---

## Appendix — command quick reference

```
PC="docker compose -f docker-compose.prod.yml"

$PC build                                   # build images
$PC up -d postgres pgbouncer rustfs bootstrap   # data tier
$PC exec -T bootstrap bash /workspace/migrations/apply.sh   # migrate
$PC exec -e AUTHADMIN_PASSWORD='…' api authadmin create --email … --name "…"   # first admin
$PC up -d                                   # app tier + workers + nginx
$PC up -d --scale api=6 --scale exportworker=3   # scale
$PC logs -f api                             # tail an api replica
$PC exec api authadmin list                 # list accounts
$PC down                                     # stop (keeps volumes)
```
