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

> **Interactive edition:** [production-setup-runbook-interactive.html](production-setup-runbook-interactive.html)
> lets you reorder the steps and see what breaks, size the PgBouncer pool, check a
> draft `.env` against what the compose file really passes to the containers, replay the
> real migrations and probe the health check. Statements this runbook could not confirm
> from the repository are marked *not verified*; corrections to earlier revisions are
> listed at the end ("Corrections in this revision").

### The order at a glance

1. Prerequisites and sizing (§1) -> hardened `.env` (§2) -> build images (§3) -> build the SPA (§4)
2. Data tier up (§5) -> migrate (§6a) -> schema/seed or restore a dump (§6b) -> first admin (§6c)
3. App tier (§7) -> TLS (§8) -> observability (§9, optional) -> verify (§12)
4. Later: upgrades (§10), backups (§11), troubleshooting (§13)

The order is load-bearing. Starting the app tier before migrating leaves every replica
*healthy* (`/healthz` only pings Postgres) while every feature call fails on missing
tables; creating the admin before migrating fails because `iam.users` does not exist;
starting nginx before `web/dist` exists makes Docker create an empty directory and the
site serves 404 for everything; and logging in before TLS is in place makes the
browser discard the Secure session cookie.

---

## 0. What you are standing up

```
nginx (published as ${WEB_PORT:-3710} -> :80 by default; :443 only once you add TLS; serves the SPA + proxies /api and /tiles)
  -> N api replicas (stateless)
       -> PgBouncer (transaction pool)
            -> Postgres (PostGIS)      [+ optional read replicas]
  workers: exportworker, webhookworker, importworker, fieldsync (scale by replicas),
           mcpserver
  rustfs (S3-compatible object storage for attachments/exports)
  bootstrap (one-shot sidecar: psql + migrations; does nothing until exec'd)
  martin (legacy tile server; superseded as the map origin, but `up -d` still starts it)
```

Which services publish host ports, and which can be scaled with `--scale`
(verified against `docker-compose.prod.yml`):

| Service | Host port (default) | `container_name` | `--scale` works? |
|---|---|---|---|
| `nginx` | `${WEB_PORT:-3710}` -> 80 | yes | no (singleton front door) |
| `api` | none | no | **yes** (`API_REPLICAS`, default 3) |
| `exportworker` / `webhookworker` / `importworker` | none | no | **yes** (`*_REPLICAS`, default 1) |
| `mcpserver` | `${MCP_PORT:-3705}` -> 8090 | yes | no |
| `fieldsync` | `${FIELDSYNC_PORT:-3706}` -> 8091 | yes | no as shipped (replica-safe in principle) |
| `postgres` | `${POSTGRES_PORT:-3700}` -> 5432 | yes | no |
| `rustfs` | `${RUSTFS_S3_PORT:-3702}` -> 9000, `${RUSTFS_CONSOLE_PORT:-3703}` -> 9001 | yes | no |
| `martin` | `${MARTIN_PORT:-3701}` -> 3000 | yes | no |
| `pgbouncer`, `bootstrap` | none | yes | no |

Seven services publish host ports. Decide at the host firewall what each is allowed
to see: **never** expose `postgres` (3700) or the RustFS console (3703) to the
internet; `martin` (3701) is not needed by the SPA and can be blocked; `rustfs` S3
(3702) and `fieldsync` (3706) must be reachable by browsers / field devices (behind
TLS); `mcpserver` (3705) only if you use AI clients.


The API image (`api/Dockerfile`) bakes every binary you need — `server`,
`exportworker`, `webhookworker`, `importworker`, `mcpserver`, and the ad-hoc
`authadmin` / `resetdata` admin tools — and the compose service picks which to
run via its `command`. So `$PC exec api authadmin …` runs the admin CLI with the
API's exact environment. (`fieldsync` is the exception: it has its own image,
`api/Dockerfile.fieldsync`, which bundles tippecanoe. The `compile`, `uncompile`,
`rmubackfill` and `cimdict` tools under `api/cmd/` are **not** built into the API image.)

---

## 1. Prerequisites & host sizing

- **Docker Engine + Compose v2**, a host (or VM) with enough headroom. A sane
  single-host starting point for a few hundred users: **8 vCPU / 16 GB RAM / SSD**.
  Postgres and the `api` replicas are the CPU/RAM consumers; give Postgres the
  bulk. (For ~1,000 users you move Postgres and the replicas off one host — see
  the scaling guide.) Postgres is started with `max_connections=200`, a figure fixed in
  the compose file and sized for PgBouncer's server pool plus replication plus
  headroom, not raw clients.
- **The repository on the host** (the compose files, `infra/`, `migrations/`,
  `seed/`, and `web/` all come from it).
- **Node.js** on the host (or a CI step) to build the SPA in §4.
- A **DNS name + TLS certificate** for the public URL (§8). Secure session
  cookies require HTTPS — this is not optional in production.

---

## 2. Secrets & environment (`.env`)

Compose reads `.env` from the repo root. Copy the template and harden every
value — **do not ship the dev defaults**:

```
cp .env.example .env
```

> **Read this before editing `.env`.** Compose uses `.env` only to substitute `${...}`
> placeholders *inside the compose file*. `docker-compose.prod.yml` has no `env_file:`,
> and the `api` service passes through only `DATABASE_URL`, `PORT` and the `RUSTFS_*`
> block. So `AUTH_INSECURE_COOKIES`, `SESSION_*`, `OIDC_*`, `EXTERNAL_SYSTEMS_ENCRYPTION_KEY`,
> `LOG_FORMAT` / `LOG_LEVEL`, `RUSTFS_PUBLIC_USE_SSL`, `RUSTFS_REGION`, `EXPORT_URL_TTL`
> and similar written to `.env` **never reach the container** until you add them to the
> service's `environment:` block (rows marked *needs pass-through* below).

Set, at minimum:

| Variable | Set to | Why |
|---|---|---|
| `POSTGRES_PASSWORD` | a long random secret | DB superuser. `openssl rand -base64 24` |
| `RUSTFS_ACCESS_KEY` / `RUSTFS_SECRET_KEY` | strong random | object-storage creds |
| `RUSTFS_CORS_ORIGINS` | your web origin (e.g. `https://gis.utility.com`) | **not** `*` in prod — browser-direct attachment PUTs |
| `RUSTFS_PUBLIC_ENDPOINT` | the public **host[:port]** (no scheme, no path) the **browser** reaches RustFS at | presigned attachment/export URLs are signed for it (see §7). Compose defaults it to `localhost:3702`, which only works in a browser on the Docker host; passed to `api` and `fieldsync` only |
| `RUSTFS_PUBLIC_USE_SSL` | `1` | if that endpoint is HTTPS — *needs pass-through* (not wired in the compose file) |
| `EXTERNAL_SYSTEMS_ENCRYPTION_KEY` | `openssl rand -base64 32` | AES key for stored external-system credentials (only if you use external API links) — *needs pass-through*. Must be base64 of exactly 32 bytes: a set-but-malformed key stops the API booting; unset just disables credential encryption. Rotating it makes stored credentials undecryptable |
| `AUTH_INSECURE_COOKIES` | **unset / `0`** | the dev template sets `1`; in prod cookies MUST carry the Secure flag → requires HTTPS. The Go default is Secure (`api/internal/auth/config.go`); as shipped the compose file never passes this variable, so it is unset in the container unless you add it |
| `SESSION_IDLE_TIMEOUT` / `SESSION_ABSOLUTE_MAX` | your policy (defaults 12h / 720h) | session lifetimes — *needs pass-through*. Go durations (`720h`, not `30d`); an unparsable value is silently replaced by the default |
| `LOG_FORMAT` / `LOG_LEVEL` | `json` / `info` | structured logs for a shipper (already the defaults; *needs pass-through* to change them) |
| `LOKI_READ_TOKEN` | `openssl rand -hex 32` | only if you run the observability overlay (§9); the overlay refuses to start without it |
| `PGBOUNCER_POOL_SIZE` / `PGBOUNCER_MAX_CLIENT_CONN` | per §5 of the scaling guide | pooler sizing |
| `API_REPLICAS`, `EXPORTWORKER_REPLICAS`, … | your scale | replica counts (also `WEBHOOKWORKER_REPLICAS`, `IMPORTWORKER_REPLICAS`; defaults 3 / 1 / 1 / 1) |
| `WEB_PORT` | `80` if nginx is your edge | host port mapped to nginx :80 (default `3710`) |

**SSO (optional):** to enable Azure AD / Google, set the `OIDC_*` block. The
redirect URL registered with the provider must be
`https://<your-host>/api/auth/oidc/<provider>/callback` (note the `/api` prefix —
nginx strips it to the backend's `/auth/oidc/...`). SSO never creates accounts;
pre-provision the user's email first (§6c). A provider is enabled only when its variables
are set *and* OIDC discovery succeeds at startup; otherwise it is silently absent from the
login screen. As with the other auth variables, the `OIDC_*` block must be passed through
to `api`.

#### Pass-through lines for the variables the compose file does not wire

Add the ones you set under `api:` -> `environment:` in `docker-compose.prod.yml`
(then recreate the service with `$PC up -d --no-deps api`):

```
      RUSTFS_PUBLIC_USE_SSL: ${RUSTFS_PUBLIC_USE_SSL:-}
      EXTERNAL_SYSTEMS_ENCRYPTION_KEY: ${EXTERNAL_SYSTEMS_ENCRYPTION_KEY:-}
      SESSION_IDLE_TIMEOUT: ${SESSION_IDLE_TIMEOUT:-}
      SESSION_ABSOLUTE_MAX: ${SESSION_ABSOLUTE_MAX:-}
      OIDC_GOOGLE_CLIENT_ID: ${OIDC_GOOGLE_CLIENT_ID:-}      # ...and the rest of the OIDC_* block
```

An empty value is treated as "unset" by the Go readers (they test for `""`), so the
`:-` form is safe. `fieldsync` and the export/import workers read `RUSTFS_*` themselves
(`RUSTFS_PUBLIC_USE_SSL` matters wherever a presigned URL is signed: `api` and `fieldsync`).

#### Worked example: sizing the PgBouncer pool

The app's own database pool is unbounded, so every in-flight request holds a client
connection. With 3 `api` replicas peaking at 20 in-flight requests each, 3 workers with
3 connections each and ~6 for `mcpserver` + `fieldsync`: 60 + 9 + 6 = **75 client
connections**, far below `PGBOUNCER_MAX_CLIENT_CONN` (1000). If 60 % are inside a
transaction at once (45) and `DEFAULT_POOL_SIZE` is 25 with `RESERVE_POOL_SIZE` 5, up to
30 Postgres backends serve them and ~15 wait. With 20 ms transactions the pool sustains
about 25 / 0.020 = 1,250 transactions/s. Postgres keeps `200 - 30` connections for
everything that bypasses the pooler (`martin`, `bootstrap`, ad-hoc `psql`; Postgres also
reserves a few for superusers by default — *not verified* for this image). The connections
per replica and transaction time are your measurements to supply; the limits above are the
compose defaults.

> Keep `.env` out of version control (it already is, via `.gitignore`) and off
> shared disks. For `AUTHADMIN_PASSWORD` / `RESETDATA_PASSWORD`, pass them
> just-in-time on the command line (§8), never leave them in `.env`.

---

## 3. Build the images

```
$PC build
```

This builds the API image (all binaries), the `fieldsync` image (bundles
tippecanoe) and the `bootstrap` sidecar image (`infra/gitbash`: alpine with bash, git,
curl and the PostgreSQL 15 client). The `nginx`, `postgres`, `pgbouncer`, `rustfs`
and `martin` images are pulled. `postgres` is pinned (`supabase/postgres:15.8.1.049`);
`pgbouncer` and `rustfs` use `:latest`, so pin them once you have a tested set.

Where each tool lives (so you reach for the right container):

| Command | Where it exists |
|---|---|
| `server`, `exportworker`, `webhookworker`, `importworker`, `mcpserver`, `authadmin`, `resetdata` | API image: `$PC exec api <tool>` |
| `fieldsync` | the `fieldsync` image |
| `migrations/apply.sh`, `psql`, `pg_dump`, `pg_restore` | `bootstrap` sidecar: `$PC exec -T bootstrap …` |
| `compile`, `uncompile`, `rmubackfill`, `cimdict`, `task …` | **not in any production image** — dev checkout with the Go toolchain; the Taskfile targets also use the *dev* compose `gitbash` service |

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
Build the SPA **before** the first `up`: if `./web/dist` does not exist when nginx starts,
Docker creates an empty directory for the bind mount and every request 404s. The build script is
`tsc -b && vite build`. Rebuild and `$PC restart nginx` whenever the frontend changes
(files come from a bind mount, so the restart may not be strictly required — *not verified*). (If you prefer a
CDN/separate static host, drop that mount and point the host's `/api` + `/tiles`
at the API instead.)

---

## 5. Start the data tier first

Bring up Postgres, the pooler, object storage, and the bootstrap sidecar — but
not yet the app — so you can migrate before any API replica connects:

```
$PC up -d postgres pgbouncer rustfs bootstrap
```

Wait for Postgres healthy: `$PC ps` should show `voltedge-postgres` healthy (`pg_isready`
every 5 s, up to 10 retries). The `voltedge-exports` bucket (`RUSTFS_BUCKET`) is
created on boot by `api`, `exportworker`, `importworker` and `fieldsync` if it is
missing — no manual bucket step. If RustFS is not reachable at that moment the API only
logs a warning and exports fail until it is. Note `rustfs` has no healthcheck, `api` waits for
it only to be *started*, and `bootstrap` waits for Postgres to be *healthy* and then just
sleeps (`restart: "no"`).

---

## 6. Bootstrap the database

### 6a. Apply migrations

The `bootstrap` sidecar has `psql` + the repo mounted and connects directly to
Postgres (not through PgBouncer — DDL wants a stable session):

```
$PC exec -T bootstrap bash /workspace/migrations/apply.sh
```

Verify (the single quotes matter: `$DATABASE_URL` must expand *inside* the container, where
the sidecar's direct-to-Postgres DSN is defined; typed bare it expands on your host to an
empty string):

```
$PC exec -T bootstrap bash -c 'psql "$DATABASE_URL" -c "SELECT filename, status FROM ops.migration_log ORDER BY id;"'
```

How `apply.sh` behaves (read from the script): it walks every `*.sql` in `migrations/` in
filename order (135 files today; two share the `0060` prefix and both run), skips any
filename already in `ops.migration_log`, runs the rest with `psql -v ON_ERROR_STOP=1 -f`,
and then records the filename, the file's `-- Purpose:` header and a SHA-256. Consequences:

- A failing file stops the run (`set -e`). The file is **not** logged, but statements
  before the error are **not rolled back** (`psql` is not run with `--single-transaction`,
  and only 14 migration files contain their own `BEGIN`). After fixing the cause, re-running
  skips the logged files and retries the failed one from its first line.
- Checksums are recorded but never compared: editing an already-applied file is invisible.
- Worked example: `apply.sh` stops at file 60 of 135. `SELECT count(*) FROM ops.migration_log`
  returns 59; the 60th file may have left partial DDL behind. Inspect, repair by hand if
  needed, then re-run.

### 6b. Load the schema + seed data

Two honest paths — pick by whether this is your first environment or a
promotion:

- **Greenfield (first environment ever):** the full metadata + compiled schema is
  produced by the authoring flow `task migrate` + `task seed-all` (114 ordered steps today:
  100 idempotent seeds that load class metadata, then `compile-all`, which **compiles every
  class** into its `gis.<code>` table, then 13 network seeds). The Taskfile targets run
  through the *dev* compose `gitbash` service and `go run`, so they target a dev/staging
  stack, not `docker-compose.prod.yml`. This is a one-time authoring
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

  Then re-run **6a** so any migrations newer than the dump are applied forward. (If
  the dump contains 100 of 135 migrations in `ops.migration_log`, `apply.sh` skips those
  and applies the remaining 35.) The bootstrap container mounts the repo at `/workspace`, so
  a dump written there lands in your checkout; keep it out of version control. That
  `pg_restore --no-owner` is clean against the supabase role/extension layout was *not
  verified*; rehearse it on a scratch database. Restore **before** starting the app tier: sessions
  live in Postgres, and restoring under live replicas replaces tables beneath them.

This mirrors how utilities actually provision: build + validate the model once,
restore it, then roll migrations forward on every upgrade (§10).

### 6c. Create the first break-glass admin

`authadmin` is baked into the API image; run it with the password passed
just-in-time (never stored):

```
$PC exec -e AUTHADMIN_PASSWORD='<a-strong-password>' api \
  authadmin create --email admin@utility.com --name "Break-glass Admin"
```

Defaults (from `api/cmd/authadmin`): `--break-glass` is **true** and `--role` is `admin`
(pass `--role ""` to assign none); the password comes **only** from `AUTHADMIN_PASSWORD` (there
is no password flag). The first account should be break-glass: migration 0024 seeds
`iam.system_settings.local_auth_enabled` as `false`, and with it off only break-glass
accounts may sign in with a password (the gate is checked before the password, so a
refusal never reveals whether the password was right). Five consecutive failed sign-ins lock
an account for 15 minutes. Roles seeded by the migrations: `viewer`, `analyst`, `editor`,
`supervisor`, `admin`, `break-glass-admin`, `field-surveyor`, `field-enumerator`,
`field-technician`.

Grant roles / list accounts the same way: `$PC exec api authadmin list`,
`$PC exec api authadmin grant-role --email <e> --role <role>`
(`authadmin roles` lists them). SSO users are pre-provisioned here by email
before their first login.

For an AI/MCP integration token: `$PC exec api authadmin mint-token --email <e>
--name <label>` (printed once; optional `--ttl 720h`, default no expiry). Other subcommands:
`set-password`, `revoke-role`, `list-tokens`, `revoke-token --email <e> --id <id>`.

---

## 7. Start the application tier

```
$PC up -d                      # brings up api (×API_REPLICAS), workers, nginx, mcpserver, fieldsync (and martin)
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
browser must be able to resolve and reach `RUSTFS_PUBLIC_ENDPOINT`. The API talks to
`rustfs:9000` internally but signs URLs for the public endpoint offline (no round trip).
Publish RustFS on a public host with TLS (and set `RUSTFS_PUBLIC_ENDPOINT` +
`RUSTFS_PUBLIC_USE_SSL=1`, the latter via the pass-through in §2), or put a TLS proxy
for RustFS on a **dedicated host name** (its own nginx `server` block forwarding the
original `Host`) and point the public endpoint at that. If presigned downloads 404 in the
browser, this is almost always why.

> A path-based `location /s3/` on the app's host cannot work as the endpoint setting is
> **host[:port] only** (it is handed to the S3 client as an endpoint; a path is rejected
> and the API treats the failure as fatal at boot), and a presigned URL is signed for a
> specific `Host`. This corrects the earlier advice; the dedicated-host route was not run
> end to end here (*not verified*).

Checklist for a presigned upload to work from the browser: (1) endpoint is `host[:port]`;
(2) the browser can resolve it (not `localhost`, not `rustfs`); (3) the signed scheme matches the
page (`RUSTFS_PUBLIC_USE_SSL=1` for an https page, else mixed-content blocking); (4) the
port is reachable; (5) `RUSTFS_CORS_ORIGINS` lists your web origin (the PUT is preflighted);
(6) whatever sits in front preserves the signed `Host`.

---

## 8. TLS

Secure cookies (and therefore login) require HTTPS to the browser. Two options:

- **Terminate at an external LB / proxy** (cloud L7 LB, Caddy, Cloudflare) in
  front of nginx:80. Simplest; the LB holds the cert.
- **Terminate at this nginx:** uncomment the `443` port (`- "443:443"` in the compose file; as
  shipped nginx is published only as `${WEB_PORT:-3710}:80`), mount your cert/key, and
  add a TLS `server` block (and redirect `:80` → `:443`). Keep the `/api`,
  `/tiles`, and SPA locations identical. The shipped `nginx.conf` has only a plain
  `listen 80` server.

Either way, confirm `AUTH_INSECURE_COOKIES` is unset/`0` (§2) and that the
browser reaches the app over `https://`.

Two consequences of terminating at an external LB, both read from `nginx.conf`:
nginx sets `X-Forwarded-Proto` to its own `$scheme` (i.e. `http`), overwriting the LB's value, so the API
will **not** emit its HSTS header (add HSTS at the LB); and nginx's `$binary_remote_addr`
is then the LB's address, so the edge login limiter below is shared by every user (no
`real_ip` module is configured).

### 8a. How nginx routes, and the login brakes

Routing (exact match first, then longest prefix):

| Request | Location | Goes to |
|---|---|---|
| `/api/auth/login` (exact) | `= /api/auth/login` | `api:8080/auth/login`, **rate-limited** |
| `/api/…` | `/api/` | `api:8080/…` (prefix stripped) |
| `/tiles/…` | `/tiles/` | `api:8080/tiles/…` (prefix kept) |
| `/assets/…` | `/assets/` | static, `expires 1y`, immutable; missing file = 404 |
| anything else (also `/api` with no slash, `/healthz`, `/s3/…`) | `/` | the SPA's `index.html` with 200 |

Note: `/api/auth/login/` (trailing slash) is **not** the exact location and is not
rate-limited at the edge, and `/healthz` through nginx returns the SPA, not the API: probe
replicas directly.

Three independent brakes sit in front of a password guess: **(1)** nginx `limit_req`:
`rate=5r/s`, `burst=10 nodelay`, 503 on excess; **(2)** the app's per-IP limiter: 10-request
burst then 10/min per replica, answering 429 with `Retry-After: 60`, keyed on the TCP
peer (it deliberately ignores `X-Forwarded-For`, so behind nginx every user shares each
replica's bucket); **(3)** the account lockout: 5 consecutive failures lock the account
for 15 minutes. Together they mean a determined flood against one email can lock the real
user out; watch the `upstream=` and status fields in nginx's access log.

---

## 9. Observability (optional but recommended)

Layer the opt-in Loki + Grafana + Vector overlay on top (see the
[Logging & Observability guide](logging-and-observability-guide.md)):

```
LOKI_READ_TOKEN=$(openssl rand -hex 32) \
  $PC -f docker-compose.observability.yml up -d
```

Grafana comes up on `GRAFANA_PORT` (default 3708); external log consumers go through the
bearer-token proxy (Caddy) on `LOKI_PROXY_PORT` (default 3709); Loki itself is not
published. `LOKI_READ_TOKEN` is mandatory: an empty value aborts the whole command
(`set LOKI_READ_TOKEN to a strong secret before starting the observability overlay`).
Mind the defaults: Grafana's admin login is `admin`/`admin` unless you set
`GRAFANA_ADMIN_PASSWORD`, anonymous access is enabled as **Viewer**, and Vector reads every
container's logs through the Docker socket — firewall port 3708. Set the alerts from §11 of the scaling
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
   image. With `API_REPLICAS ≥ 3`, drop one at a time
   (or use a rolling strategy) so there's always a healthy replica. (`nginx.conf` defines no
   health check of its own; it re-resolves `api` every 10 s through Docker DNS, so a removed
   replica can still be tried for up to that long. The order in which Compose recreates
   replicas was *not verified*.)
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
  A drill that costs nothing: dump (`$PC exec -T bootstrap bash -c 'pg_dump "$DATABASE_URL" -Fc -f /workspace/drill.dump'`),
  restore into a *scratch* database (never over production), run `apply.sh` against it and
  compare the `ops.migration_log` counts. Time it; your measured number replaces any estimate.
  For example, a 50 GB database restoring at 30 MB/s takes about 28 minutes, and a nightly dump
  means up to 24 h of data loss unless you add PITR.
- **What lives where:** database state is in the `postgres_data` volume; attachments, exports and
  field packs in `rustfs_data`; secrets in `.env`; the SPA (`web/dist`) is rebuildable.
  `$PC down` keeps volumes, `down -v` deletes them. A database restored to time T with a bucket at
  a different time leaves orphaned objects or dangling attachment rows.
- Nothing in the compose file configures WAL archiving: it is `wal_level=replica`,
  10 senders, 10 slots (replication-ready only).
- The break-glass **factory reset** (`resetdata`) is a destructive maintenance
  tool, not a backup; keep `RESETDATA_PASSWORD` out of `.env`.

---

## 12. Verify the deployment

1. `$PC ps` — every service up; `api` shows your replica count; `fieldsync` is 1.
2. **Health:** each `api` replica's `/healthz` is green (it pings Postgres through
   PgBouncer with a 3 s timeout) — `$PC exec api wget -qO- http://127.0.0.1:8080/healthz`
   returns `{"status":"ok"}` (503 `db unreachable` otherwise). `exec` reaches one replica
   only, and nginx does not proxy `/healthz`. It does **not** prove migrations ran:
   also check `SELECT count(*) FROM ops.migration_log` (135 files today).
   The container healthcheck runs every 10 s, times out at 5 s, needs 3 failures and
   ignores failures during a 20 s start period; an unhealthy container is **not**
   restarted (the restart policy reacts to exits only).
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
| Every replica healthy, every feature call fails | migrations never applied (`/healthz` only pings Postgres) | check `ops.migration_log`, run `apply.sh` — §6a |
| `apply.sh` stopped partway | a file failed; earlier statements in it are not rolled back | fix the cause, re-run (it resumes at that file) — §6a |
| "local password sign-in is currently disabled" | `local_auth_enabled` is seeded `false` and the account is not break-glass | sign in as the break-glass admin, or create accounts with the default `--break-glass` — §6c |
| Account locked | 5 consecutive failures lock it 15 min; a flood can lock the real user | wait it out; check the nginx log for the source — §8a |
| 503 / 429 on every login | nginx edge limit (5 r/s, burst 10) / app limiter (10 burst, 10 per min per replica) | find the noisy source; behind a shared address everyone shares the bucket — §8a |
| An `.env` change had no effect | the variable is not passed through by the compose file | add it to the service `environment:` and recreate — §2 |
| SSO button missing | `OIDC_*` not passed through, or discovery failed at startup | pass the block through; check the redirect URL — §2 |
| Observability `up` aborts at once | `LOKI_READ_TOKEN` unset | set it — §9 |
| A replica is `unhealthy` and nothing restarts | no component acts on health | investigate/restart manually; alert on health — §12 |
| 502s for ~10 s during an upgrade | nginx has no health check and caches DNS for 10 s | roll one replica at a time with ≥ 3 replicas — §10 |

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

# additions
$PC exec -T bootstrap bash -c 'psql "$DATABASE_URL" -c "SELECT count(*) FROM ops.migration_log;"'   # migration count
$PC exec api authadmin roles                # list role keys
$PC exec api authadmin mint-token --email … --name "…" --ttl 720h   # MCP token
$PC up -d --no-deps api                     # roll the api tier
LOKI_READ_TOKEN=$(openssl rand -hex 32) $PC -f docker-compose.observability.yml up -d   # logs overlay
```

---

## Corrections in this revision

Statements in earlier revisions that the repository contradicts, now fixed in place:

| Earlier statement | What the code says | Evidence |
|---|---|---|
| `.env` values such as `AUTH_INSECURE_COOKIES`, `SESSION_*`, `LOG_*`, `EXTERNAL_SYSTEMS_ENCRYPTION_KEY`, `OIDC_*`, `RUSTFS_PUBLIC_USE_SSL` configure the stack | the `api` service passes only `DATABASE_URL`, `PORT`, `RUSTFS_*`; no `env_file` | `docker-compose.prod.yml:151-159` |
| nginx on :80/:443 | published as `${WEB_PORT:-3710}:80`; 443 is a commented line | `docker-compose.prod.yml:126-127` |
| proxy RustFS via nginx `/s3/` and point the endpoint at it | endpoint is host[:port] given to the S3 client; URLs are signed for a Host | `api/internal/export/storage.go:88,99-100,114-127` |
| `psql "$DATABASE_URL"` via `exec -T bootstrap` | expands on the host; must expand in the container | `docker-compose.prod.yml:332` |
| `$PC up -d` list omits martin; `build` lists two images | martin also starts; `bootstrap` is built too | `docker-compose.prod.yml:299-318, 320-336` |
| "the nginx health check" keeps a replica | `nginx.conf` has none; it re-resolves every 10 s | `infra/nginx/nginx.conf:34` |
| "100+ seeds" run by `task seed-all` against the prod stack | 114 steps; dev `gitbash` service + `go run`; `compile` is not in the prod image | `Taskfile.yml:1359`, `api/Dockerfile:23-29` |
| cross-references: TLS "(§6)", pre-provision SSO "(§8)" | TLS is §8, first admin is §6c | this document |
