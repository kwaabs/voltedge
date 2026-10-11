# Deploying on Kubernetes & Coolify

Two concrete, detailed ways to run the production stack beyond single-host Docker
Compose:

- **Coolify** — a self-hosted PaaS that *wraps* Docker Compose. The easiest jump
  from `docker-compose.prod.yml`: a UI, Git-push deploys, automatic TLS and secret
  management, on your own server(s). Right for one strong server up to a few
  hundred users.
- **Kubernetes** — the stack *decomposed* into native objects with autoscaling,
  self-healing and operator-managed Postgres. The path to the ~1,000-user,
  high-availability shape the [scaling guide](deployment-and-scaling-guide.md)
  sizes for.

Both **build images** (neither can bind-mount a host-built `web/dist`), so both
use the portable **web image** (`web/Dockerfile`, bakes the SPA) and the
**migrate image** (`deploy/migrate/Dockerfile`, bakes the migrations). The nginx
*config* stays external (a mount on Coolify, a ConfigMap on k8s) because the load
-balancing strategy differs between the two.

Read the [Production Setup Runbook](production-setup-runbook.md) first — the
migrate / seed / first-admin *logic* is identical here; only the mechanics change.

> **Status of the files this guide names.** `docker-compose.prod.yml`,
> `infra/nginx/nginx.conf`, `web/Dockerfile`, `deploy/migrate/Dockerfile`,
> `api/Dockerfile` and `api/Dockerfile.fieldsync` exist in the repo.
> `docker-compose.coolify.yml` and everything under `deploy/k8s/` are **proposed**
> — they do not exist yet; you create them from the listings below. Claims that
> could be checked against the repo (compose, Dockerfiles, nginx.conf, migrations,
> `api/cmd/*`) are checked; Kubernetes / CloudNativePG / KEDA / Coolify behaviour
> that the repo cannot confirm is marked *not verified*. A sibling
> [interactive edition](kubernetes-and-coolify-guide-interactive.html) runs the
> arithmetic (connection budgets, HPA and rolling-update timelines, probe timing,
> queue-depth scaling, bootstrap order) in the browser. A list of the corrections
> made to earlier text is at the end (**Appendix — corrections and verification**).

---

## 0. The images you build (both platforms)

| Image | Dockerfile | Build context | Runs |
|---|---|---|---|
| `voltedge-api` | `api/Dockerfile` | `./api` | `server` / `exportworker` / `webhookworker` / `importworker` / `mcpserver` / `authadmin` (per `command`; the image also carries `resetdata`, the break-glass factory reset — never a long-running service) |
| `voltedge-fieldsync` | `api/Dockerfile.fieldsync` | `./api` | `fieldsync` (bundles tippecanoe) |
| `voltedge-web` | `web/Dockerfile` | `./web` | nginx serving the built SPA |
| `voltedge-migrate` | `deploy/migrate/Dockerfile` | **repo root** | one-shot `apply.sh` (+ carries `pg_restore`) |

The api image's default `CMD` is `server`; on Kubernetes a container's `command:`
replaces it, on Compose the service's `command:` does. `fieldsync` is **not** in the
api image (it needs tippecanoe, built from a pinned Felt tag), so it must use the
`voltedge-fieldsync` image. The migrate image's `ENTRYPOINT` is
`bash /migrations/apply.sh` (it needs `DATABASE_URL` at runtime and skips anything
already in `ops.migration_log`); build it from the **repo root**.

Build and push all four to your registry (replace `REG` with yours, pick a real
`TAG` — a git SHA is ideal):

```
REG=registry.example.com/voltedge ; TAG=$(git rev-parse --short HEAD)

docker build -t $REG/voltedge-api:$TAG        ./api
docker build -t $REG/voltedge-fieldsync:$TAG  -f api/Dockerfile.fieldsync ./api
docker build -t $REG/voltedge-web:$TAG        ./web
docker build -t $REG/voltedge-migrate:$TAG    -f deploy/migrate/Dockerfile .

for i in api fieldsync web migrate; do docker push $REG/voltedge-$i:$TAG; done
```

---

# Part A — Coolify

Coolify (v4) is a self-hosted PaaS that runs Docker Compose under the hood, so
`docker-compose.prod.yml` is most of the work. This walks the whole deployment
end to end.

## A1. How Coolify is organised

Four nested concepts, top to bottom:

- **Server** — a host Coolify manages over SSH (the one Coolify runs on, and any
  you add). Docker runs here.
- **Project → Environment** — a grouping (e.g. project *VoltEdge*, environment
  *production*).
- **Resource** — the deployable unit. Yours is a **Docker Compose** resource built
  from your Git repo. (Databases can also be their own managed resources — A3.)
- **The proxy** — Coolify runs a **Traefik** (or Caddy) proxy on each server that
  terminates TLS, provisions Let's Encrypt certs, and routes a domain to a
  service. You never touch cert files.

The flow you'll follow: install Coolify → (optionally) create a managed Postgres
resource → create the Compose resource from Git → set env + domains → deploy →
bootstrap the DB → turn on backups and auto-deploy.

## A2. Prerequisites & installing Coolify

- A server for Coolify + the app: **8 vCPU / 16 GB / SSD** is a sane start for a
  few hundred users (give Postgres the bulk). A fresh Ubuntu 22.04/24.04 works.
- Two DNS A-records at the server's IP: the app host `gis.example.com` and a
  storage host `storage.example.com` (for presigned URLs — A7).
- Install Coolify on the server:
  ```
  curl -fsSL https://cdn.coollabs.io/coolify/install.sh | bash
  ```
  Then open `http://<server-ip>:8000`, create the admin account, and you land on
  the dashboard. The install registers this server as Coolify's first managed
  server (`localhost`). To deploy onto other machines, **Servers → Add Server**
  (Coolify connects over SSH).

## A3. Decide your Postgres topology

Two valid shapes — pick one before creating the app resource:

- **Option 1 — Postgres inside the compose (simplest, matches the file).** Deploy
  `docker-compose.prod.yml` as-is; its `postgres` + `pgbouncer` services come up
  with the app. Backups are your job via a Scheduled Task (A10). Start here if you
  want the fewest moving parts.
- **Option 2 — Postgres as a managed Coolify resource (better ops).** **New
  Resource → Database → PostgreSQL**, and set its image to a PostGIS one (e.g.
  `postgis/postgis:15-3.4`) so the spatial extension is available. You get
  Coolify's **automated backups** (local or S3) and a managed lifecycle. Then
  **remove** the `postgres` service from the compose you deploy (keep `pgbouncer`
  pointed at the managed DB, or drop it too and point the app straight at the
  managed DB), and set `DATABASE_URL` to the connection string Coolify shows on
  the database resource. This mirrors the operator-managed Postgres you'd run on
  Kubernetes.

The rest of Part A assumes **Option 1** (self-contained compose); the deltas for
Option 2 are only "don't deploy the bundled `postgres`, point `DATABASE_URL` at
the managed one."

Option 2 details the compose file forces on you: `pgbouncer`, `bootstrap` and
`martin` all declare `depends_on: postgres`, so those entries must be removed or
re-pointed when you remove the service; and every service builds its own
`DATABASE_URL` from `POSTGRES_PASSWORD`, so overriding `DATABASE_URL` in the UI is
not enough — override those `environment:` entries too. The compose file runs
`supabase/postgres:15.8.1.049`; the migrations only `CREATE EXTENSION` `postgis`
(0015) and `citext` (0024), so a PostGIS image should be sufficient, but that has
not been run here.

## A4. Adapt the compose for Coolify (web image, no host mounts)

Coolify builds images; it will **not** run `npm run build` for a bind-mount, so
the `./web/dist` mount would be empty. Commit a Coolify override that builds the
web image and drops the host-path mounts. Create **`docker-compose.coolify.yml`**
in the repo — it is `docker-compose.prod.yml` with only the `nginx` service
changed:

```yaml
# docker-compose.coolify.yml -- overrides for Coolify (web image, Coolify proxy).
# Give Coolify BOTH files (base + this), or merge manually.
services:
  nginx:
    build:
      context: ./web          # bakes the SPA (web/Dockerfile)
      dockerfile: Dockerfile
    volumes: !override
      # Only the config stays external now; the SPA is inside the image.
      # !override REPLACES the base list. A plain list MERGES by mount target, so
      # the base file's ./web/dist:/usr/share/nginx/html mount would survive.
      - ./infra/nginx/nginx.conf:/etc/nginx/nginx.conf:ro
    # Drop the published host port -- Coolify's proxy fronts this service (A8).
    ports: !reset []
```

> **Correction.** The earlier version of this override listed only the nginx.conf
> mount, without `!override`. Compose merges `volumes:` lists across files by
> container path, so the base `./web/dist` bind mount stayed and shadowed the baked
> SPA with an empty directory (blank page / 404 on Coolify). `!override` and `!reset`
> need a recent Compose v2 (2.24+). This follows the Compose merge rules; it has not
> been run here. Also note the base keeps `image: nginx:1.27-alpine` on this service,
> so with `build:` added Compose tags the built image with that name on the build
> host; add `image: voltedge-web:coolify` if that is unwanted. *(not verified in the repo)*

The `infra/nginx/nginx.conf` resolver trick uses Docker's `127.0.0.11` DNS, which
Coolify honours (it *is* Docker underneath), so that config works unchanged — the
same `/api` strip, `/tiles` keep, SPA fallback and internal replica
load-balancing as on raw Compose.

## A5. Create the Compose resource

1. Coolify → **Project → production → + New → Docker Compose (Git Based)**. Pick
   your Git source (connect GitHub/GitLab, or a public/deploy-key repo URL) and
   the branch.
2. **Base directory** `/`, **Compose file** `docker-compose.prod.yml`. Add the
   override: in the resource's settings there's a field for additional compose
   files — add `docker-compose.coolify.yml` (or maintain a single merged file if
   your Coolify version takes only one).
3. Coolify parses the services and shows them. It builds the images defined by
   `build:` (api, fieldsync, web, bootstrap) and pulls the rest.

## A6. Environment variables & secrets

Coolify → the resource → **Environment Variables**. These are stored encrypted;
do **not** commit a `.env`. Paste, with production values:

```
# Postgres (Option 1: in-compose)
POSTGRES_PASSWORD=<openssl rand -base64 24>
POSTGRES_DB=postgres

# Object storage (RustFS)
RUSTFS_ACCESS_KEY=<strong>
RUSTFS_SECRET_KEY=<strong>
RUSTFS_BUCKET=voltedge-exports
RUSTFS_CORS_ORIGINS=https://gis.example.com
RUSTFS_PUBLIC_ENDPOINT=storage.example.com      # browser-reachable storage host (A7)
RUSTFS_PUBLIC_USE_SSL=1

# Auth / sessions  --  THESE MATTER
# Do NOT set AUTH_INSECURE_COOKIES (must be 0/unset in prod; Coolify gives HTTPS).
SESSION_IDLE_TIMEOUT=12h
SESSION_ABSOLUTE_MAX=720h

# Encryption for stored external-system credentials (only if you use external links)
EXTERNAL_SYSTEMS_ENCRYPTION_KEY=<openssl rand -base64 32>

# Logging
LOG_FORMAT=json
LOG_LEVEL=info

# PgBouncer sizing (the pgbouncer service in the base compose)
PGBOUNCER_POOL_SIZE=25
PGBOUNCER_MAX_CLIENT_CONN=1000

# Replica counts
API_REPLICAS=3
EXPORTWORKER_REPLICAS=1
WEBHOOKWORKER_REPLICAS=1
IMPORTWORKER_REPLICAS=1

# SSO (optional) -- redirect https://gis.example.com/api/auth/oidc/<provider>/callback
#OIDC_AZURE_TENANT_ID=
#OIDC_AZURE_CLIENT_ID=
#OIDC_AZURE_CLIENT_SECRET=
```

Notes:
- **A variable only reaches a container if the compose file references it.**
  `docker-compose.prod.yml` wires `POSTGRES_PASSWORD`/`POSTGRES_DB`,
  `RUSTFS_ACCESS_KEY`/`RUSTFS_SECRET_KEY`/`RUSTFS_BUCKET`/`RUSTFS_PUBLIC_ENDPOINT`,
  `RUSTFS_CORS_ORIGINS` (into rustfs as `RUSTFS_CORS_ALLOWED_ORIGINS`),
  `PGBOUNCER_*`, the `*_REPLICAS`, and the worker/fieldsync tuning knobs. It does
  **not** wire `RUSTFS_PUBLIC_USE_SSL`, `SESSION_IDLE_TIMEOUT`, `SESSION_ABSOLUTE_MAX`,
  `EXTERNAL_SYSTEMS_ENCRYPTION_KEY`, `LOG_FORMAT`, `LOG_LEVEL` or `OIDC_*`. Add them to
  the `environment:` blocks in `docker-compose.coolify.yml` (a map merges by key).
  The one that bites: without `RUSTFS_PUBLIC_USE_SSL=1` inside the container
  (`api/internal/export/storage.go` reads `== "1"`), presigned URLs are signed for
  `http://` and the browser blocks them as mixed content. Safe to omit: the session
  timeouts (code defaults 12h / 720h), `LOG_*` (JSON, info by default) and
  `AUTH_INSECURE_COOKIES` (Secure by default). The webhook and import workers also
  need `EXTERNAL_SYSTEMS_ENCRYPTION_KEY` (see B7).
- SSO: the Azure provider's key is `azuread` (callback
  `/api/auth/oidc/azuread/callback`) and its redirect URL comes from
  `OIDC_AZURE_REDIRECT_URL`; Google uses `OIDC_GOOGLE_CLIENT_ID`,
  `OIDC_GOOGLE_CLIENT_SECRET`, `OIDC_GOOGLE_REDIRECT_URL`.
- Toggle **Build Variable** on only the vars needed at *image build* time
  (none of ours are — they're all runtime), leave the rest as runtime secrets.
- For **Option 2**, drop `POSTGRES_PASSWORD`/`POSTGRES_DB` and instead set
  `DATABASE_URL` to the managed DB's connection string (Coolify shows it on the
  database resource; point it at the pooler/DB host it provides).

## A7. Persistent storage

In **Option 1**, the `postgres_data` and `rustfs_data` named volumes from the
compose carry your database and object store. Coolify preserves named volumes
across redeploys, but confirm them under the resource's **Storage** tab and mark
them persistent so a rebuild never wipes them. (If you ever "Delete + redeploy"
the resource, volumes can be removed — snapshot first.)

## A8. Domains & TLS

Coolify assigns domains per **service**. On the resource's service list:

1. On the **`nginx`** service, set the domain to `https://gis.example.com`.
   Coolify writes the Traefik labels, routes the host to nginx:80, and issues a
   Let's Encrypt cert automatically. (Some Coolify versions expose this as a
   `SERVICE_FQDN_NGINX` magic variable instead of a UI field — either way the
   target is the `nginx` service.)
2. On the **`rustfs`** service, set the domain to `https://storage.example.com`
   routing to its port 9000 — this is what presigned upload/download URLs hit
   (A6 set `RUSTFS_PUBLIC_ENDPOINT` to match).
3. Leave every other service **without** a public domain (api, workers, pgbouncer,
   postgres, bootstrap are internal-only; the SPA reaches the API through nginx's
   `/api`). The base file publishes host ports for `mcpserver` (3705), `fieldsync`
   (3706), postgres (3700), rustfs (3702/3703) and martin (3701): close those at the
   firewall unless something outside needs them.

Result: two proxies, by design — Coolify's Traefik (TLS + domain) in front of our
nginx (SPA + `/api`//`/tiles` split + replica load-balancing). Because Coolify
serves HTTPS, secure session cookies work (keep `AUTH_INSECURE_COOKIES` unset).

## A9. Deploy, then bootstrap the database

1. **Deploy** — hit **Deploy**. Watch the build/deploy log; wait until `postgres`
   is healthy and the stack is up (the api healthcheck is `/healthz`).
2. **Migrate** — open Coolify's **Terminal** into the `bootstrap` service (or use
   **Execute Command**) and run:
   ```
   bash /workspace/migrations/apply.sh
   ```
   (The `bootstrap` service has psql + the repo mounted and connects directly to
   Postgres — it exists for exactly this.)
3. **Seed** — restore a validated dump (runbook §6b), not 100 hand-run seeds. Get
   the dump into the container (upload, or pull from S3), then from the bootstrap
   terminal:
   ```
   pg_restore --no-owner -d "$DATABASE_URL" /workspace/seeded.dump
   ```
   Ordering caveat: the runbook (§6b) restores the dump and then says to re-run the
   migrations to roll newer ones forward. A dump carries the schema, so restoring
   after a full migrate on an empty database is likely to report "already exists";
   for a fresh install from a dump, restore first, then migrate. *(not verified)*
4. **First break-glass admin** — Terminal into an **`api`** service (authadmin is
   baked into the image):
   ```
   AUTHADMIN_PASSWORD='<strong>' authadmin create --email admin@example.com --name "Break-glass Admin"
   ```
   Then `authadmin list`, `authadmin grant-role --email … --role …` as needed.

## A10. Backups

- **Option 2 (managed DB):** enable Coolify's **Scheduled Backups** on the
  PostgreSQL resource — pick a cadence and an S3 destination. One click; done.
- **Option 1 (in-compose Postgres):** Coolify's one-click DB backup targets
  *managed* database resources, not a Postgres inside a compose stack, so add a
  **Scheduled Task** on the resource (Coolify → the resource → **Scheduled
  Tasks**) that runs, in the `bootstrap` container, on a cron:
  ```
  pg_dump -Fc "$DATABASE_URL" -f /workspace/backups/voltedge-$(date +%F).dump
  ```
  and ship `/workspace/backups` (or dump straight) to object storage. Either way,
  **back up the `rustfs_data` volume separately** (attachments/exports live only
  there) and **test a restore**.

## A11. Scaling & resource limits

- **Replicas:** set `deploy.replicas` on `api`/workers via the `*_REPLICAS` env
  (A6); Coolify honours them and our nginx load-balances across them. Keep
  **`fieldsync` at 1 by default** — the compose file runs one instance *by choice*
  (it publishes a fixed device port and carries a `container_name`, both of which
  block `--scale`). It is **not** a correctness singleton: both of its workers claim
  jobs with `FOR UPDATE SKIP LOCKED` and each build uses its own temp dir (compose
  header comment, Scaling Guide §8). To scale it, drop the port and `container_name`
  and front it with nginx; the shared-pack maintenance loop then runs in every
  replica and may harmlessly double-enqueue a scheduled rebuild.
- **Limits:** set per-service CPU/memory limits in the service settings so one
  heavy analysis pod can't starve the host.
- **Multi-node reality:** a single Coolify server scales *up* (bigger box, more
  replicas) but not *out* across nodes with autoscaling. Coolify can deploy to
  multiple servers and has **Docker Swarm** support for multi-node, but if you
  genuinely need autoscaling + managed replica failover at the ~1,000-user tier,
  that's the Kubernetes path (Part B). Coolify's sweet spot is one strong server.

## A12. Auto-deploy & rollbacks

- **Auto-deploy:** connect the Git source so Coolify deploys on push to the branch
  (it installs a webhook). Gate it to a `production` branch/tag.
- **Deploy order on upgrade:** a push rebuilds images and redeploys. Run the
  migration step (A9.2) **before** the new app code serves traffic — the
  additive-first schema keeps old containers working during the swap. For a
  breaking change, use expand → migrate → deploy → contract across two releases
  (scaling guide §10).
- **Rollback:** Coolify keeps previous deployments — redeploy a prior build from
  the **Deployments** history if a release misbehaves. This rolls the *code* back,
  not the schema: migrations are not reversed, which is why they must stay
  backward-compatible with the previous image.

## A13. Verify

1. Resource shows every service healthy; `api` at your replica count; `fieldsync`
   at 1.
2. Browse `https://gis.example.com/` — sign in as the break-glass admin.
3. Map renders (confirms the `/tiles` proxy + territory-scoped MVT).
4. Create a feature, run a trace (write path + synchronous analysis path).
5. Upload an attachment (confirms RustFS presign + the `storage.example.com`
   domain).

## A14. Troubleshooting (Coolify-specific)

| Symptom | Cause | Fix |
|---|---|---|
| SPA 404s / blank after deploy | `nginx` still using the host `dist` mount (override list merged instead of replaced) | ensure the `docker-compose.coolify.yml` override is applied and uses `volumes: !override` (nginx builds `web/Dockerfile`) |
| TLS/cert not issued | DNS not pointing at the server, or domain set on the wrong service | A-record → server IP; set the domain on the `nginx` service only |
| Login fails, cookie missing | `AUTH_INSECURE_COOKIES` set, or site not HTTPS | unset it; confirm Coolify issued the cert and you're on `https://` |
| Presigned attachment URLs 404 or blocked | `storage.example.com` not routed, `RUSTFS_PUBLIC_ENDPOINT` mismatch, or `RUSTFS_PUBLIC_USE_SSL` never reached the container | give `rustfs` its domain (A8), match `RUSTFS_PUBLIC_ENDPOINT`, pass `RUSTFS_PUBLIC_USE_SSL=1` through the override (A6) |
| Volumes wiped after redeploy | storage not marked persistent | A7 — mark `postgres_data`/`rustfs_data` persistent; snapshot before destructive redeploys |
| `--scale`/replica error "name in use" | a `container_name` on a scaled service | only singletons carry one here; don't add to `api`/workers |
| "prepared statement does not exist" | pgdriver vs PgBouncer transaction pooling (the app uses no LISTEN/NOTIFY or advisory locks; prepared-statement behaviour is *not verified*) | set `POOL_MODE: session` on the pgbouncer service, or disable statement caching in the DSN |
| Map blank, classes missing | schema not seeded | restore the seeded dump (A9.3) |

**When Coolify is right:** one or two servers, a few hundred users, you want
push-to-deploy and automatic TLS without running a cluster. It gives ~80% of the
operational niceties for ~5% of the effort of Kubernetes.

---

# Part B — Kubernetes

A complete, applyable deployment. Save each manifest below under `deploy/k8s/`
and `kubectl apply` in the order given, or assemble them into a kustomization.

## B1. Cluster prerequisites

Install these once per cluster:

```
# Ingress controller (TLS + host routing)
helm upgrade --install ingress-nginx ingress-nginx/ingress-nginx -n ingress-nginx --create-namespace

# cert-manager (Let's Encrypt)
helm upgrade --install cert-manager jetstack/cert-manager -n cert-manager --create-namespace --set crds.enabled=true

# CloudNativePG operator (managed Postgres: replicas, failover, PITR, pooler)
helm upgrade --install cnpg cnpg/cloudnative-pg -n cnpg-system --create-namespace

# KEDA (optional: autoscale workers on queue depth)
helm upgrade --install keda kedacore/keda -n keda --create-namespace
```

Create a cert-manager issuer (`deploy/k8s/00-issuer.yaml`):

```yaml
apiVersion: cert-manager.io/v1
kind: ClusterIssuer
metadata: { name: letsencrypt-prod }
spec:
  acme:
    server: https://acme-v02.api.letsencrypt.org/directory
    email: ops@example.com
    privateKeySecretRef: { name: letsencrypt-prod }
    solvers:
    - http01: { ingress: { class: nginx } }
```

## B2. Namespace, config, secrets

`deploy/k8s/10-namespace-config.yaml`:

```yaml
apiVersion: v1
kind: Namespace
metadata: { name: voltedge }
---
apiVersion: v1
kind: ConfigMap
metadata: { name: voltedge-env, namespace: voltedge }
data:
  PORT: "8080"
  MCP_PORT: "8090"
  FIELDSYNC_PORT: "8091"
  LOG_FORMAT: "json"
  LOG_LEVEL: "info"
  RUSTFS_ENDPOINT: "rustfs:9000"
  RUSTFS_BUCKET: "voltedge-exports"
  RUSTFS_PUBLIC_ENDPOINT: "storage.example.com"
  RUSTFS_PUBLIC_USE_SSL: "1"
  # AUTH_INSECURE_COOKIES is deliberately absent -> secure cookies (HTTPS only).
```

Every key above is read by the code (`PORT` defaults to 8080, `MCP_PORT` to 8090,
`FIELDSYNC_PORT` to 8091). Unlike Compose, `envFrom` delivers `RUSTFS_PUBLIC_USE_SSL`
to every container, so the Coolify gap in A6 does not exist here.

`deploy/k8s/11-secret.yaml` (fill in real values; consider Sealed Secrets / SOPS
rather than committing this):

```yaml
apiVersion: v1
kind: Secret
metadata: { name: voltedge-db-app, namespace: voltedge }
type: kubernetes.io/basic-auth
stringData:
  username: app
  password: "<CHOOSE-A-STRONG-DB-PASSWORD>"        # CNPG uses this for the app role (B3)
---
apiVersion: v1
kind: Secret
metadata: { name: voltedge-secret, namespace: voltedge }
type: Opaque
stringData:
  # Routed through the CNPG transaction pooler (B3). Same password as above.
  DATABASE_URL: "postgres://app:<SAME-STRONG-DB-PASSWORD>@voltedge-db-pooler-rw:5432/voltedge?sslmode=require"
  RUSTFS_ACCESS_KEY: "<strong>"
  RUSTFS_SECRET_KEY: "<strong>"
  EXTERNAL_SYSTEMS_ENCRYPTION_KEY: "<openssl rand -base64 32>"
  # SSO (optional): OIDC_AZURE_TENANT_ID / _CLIENT_ID / _CLIENT_SECRET / _REDIRECT_URL
```

Deliver the keys to the right workloads: `server` builds the object store at boot
(`RUSTFS_*` required) and reads the encryption key; `exportworker`, `importworker`
and `fieldsync` need `RUSTFS_*`; `webhookworker` and `importworker` need
`EXTERNAL_SYSTEMS_ENCRYPTION_KEY`; `mcpserver` needs only `DATABASE_URL`.

## B3. Postgres via CloudNativePG (primary + replicas + pooler)

`deploy/k8s/20-postgres.yaml`:

```yaml
apiVersion: postgresql.cnpg.io/v1
kind: Cluster
metadata: { name: voltedge-db, namespace: voltedge }
spec:
  instances: 3                                   # 1 primary + 2 streaming replicas
  imageName: ghcr.io/cloudnative-pg/postgis:15   # PostGIS-enabled Postgres 15
  storage: { size: 100Gi }
  bootstrap:
    initdb:
      database: voltedge
      owner: app
      secret: { name: voltedge-db-app }          # the app role credentials (B2)
      postInitApplicationSQL:
        - CREATE EXTENSION IF NOT EXISTS postgis;
        - CREATE EXTENSION IF NOT EXISTS citext;     # migration 0024 needs it
        - CREATE EXTENSION IF NOT EXISTS pgcrypto;   # optional: gen_random_uuid() is core since PG13
  # Optional but recommended: continuous backup / PITR to object storage.
  # backup:
  #   barmanObjectStore:
  #     destinationPath: "s3://voltedge-backups/pg"
  #     s3Credentials: { ... }
---
# Transaction-pooling pooler for WRITES + hot reads (the app's DATABASE_URL).
apiVersion: postgresql.cnpg.io/v1
kind: Pooler
metadata: { name: voltedge-db-pooler-rw, namespace: voltedge }
spec:
  cluster: { name: voltedge-db }
  instances: 2
  type: rw
  pgbouncer:
    poolMode: transaction
    parameters: { max_client_conn: "1000", default_pool_size: "25" }
---
# Read-only pooler against the REPLICAS -- route tile/read queries here once the
# app grows a read-only DB handle (scaling guide §5-6). Not yet wired by default.
apiVersion: postgresql.cnpg.io/v1
kind: Pooler
metadata: { name: voltedge-db-pooler-ro, namespace: voltedge }
spec:
  cluster: { name: voltedge-db }
  instances: 2
  type: ro
  pgbouncer: { poolMode: transaction }
```

> **Correction.** The migrations `CREATE EXTENSION` exactly two extensions:
> `postgis` (0015) and `citext` (0024). The earlier text listed `pgcrypto`
> ("`gen_random_uuid()`" — built in since Postgres 13; 55 migrations use it) and
> omitted `citext`. `citext` is normally a "trusted" extension the owner may create,
> but pre-creating it as superuser is safest. The CNPG image name and CNPG defaults
> are *not verified* here.

CNPG manages the pooler↔Postgres auth for you (an `auth_query`), so the manual
PgBouncer/SCRAM worry from the Compose stack does not apply here — the app just
presents the `app` credentials to the pooler. `sslmode=require` encrypts to the
pooler; if pgdriver rejects the CNPG CA, mount the cluster CA and use
`sslmode=verify-full`, or keep `require` (encrypt without CA verification).

**Direct vs pooled, and sizing.** CNPG also creates a read-write Service straight to
the primary, `<cluster>-rw` (`voltedge-db-rw`) *(CNPG convention, not verified here)*.
Compose runs migrations over a **direct** connection because DDL wants a stable
session (see the `bootstrap` comment in `docker-compose.prod.yml` and runbook §6a);
use the direct Service for the migrate Job and `pg_restore` (B5, B10). Size the
pooler so that `instances × default_pool_size` plus ~13 reserved/operator slots fits
under Postgres `max_connections` (Compose sets 200; the CNPG default is a generic
figure, not verified), and so that clients per pooler instance stay under
`max_client_conn`. Today the API does **not** cap its own pool: `DATABASE_MAX_OPEN_CONNS`
is not read anywhere (no `SetMaxOpenConns` on the main pool), so at the HPA maximum
(30 pods) the client-side count follows request concurrency. The interactive edition's
connection-budget widget does the sums.

## B4. Object storage (RustFS)

`deploy/k8s/25-rustfs.yaml` (single-instance to mirror the stack; for real
durability use an S3 service or the MinIO operator and just point `RUSTFS_*` at it):

```yaml
apiVersion: apps/v1
kind: StatefulSet
metadata: { name: rustfs, namespace: voltedge }
spec:
  serviceName: rustfs
  replicas: 1
  selector: { matchLabels: { app: rustfs } }
  template:
    metadata: { labels: { app: rustfs } }
    spec:
      containers:
      - name: rustfs
        image: rustfs/rustfs:latest
        args: []
        env:
        - { name: RUSTFS_VOLUMES, value: /data }
        - { name: RUSTFS_ADDRESS, value: "0.0.0.0:9000" }
        - { name: RUSTFS_ACCESS_KEY, valueFrom: { secretKeyRef: { name: voltedge-secret, key: RUSTFS_ACCESS_KEY } } }
        - { name: RUSTFS_SECRET_KEY, valueFrom: { secretKeyRef: { name: voltedge-secret, key: RUSTFS_SECRET_KEY } } }
        - { name: RUSTFS_CORS_ALLOWED_ORIGINS, value: "https://gis.example.com" }
        ports: [{ containerPort: 9000 }]
        volumeMounts: [{ name: data, mountPath: /data }]
  volumeClaimTemplates:
  - metadata: { name: data }
    spec: { accessModes: ["ReadWriteOnce"], resources: { requests: { storage: 100Gi } } }
---
apiVersion: v1
kind: Service
metadata: { name: rustfs, namespace: voltedge }
spec:
  selector: { app: rustfs }
  ports: [{ port: 9000, targetPort: 9000 }]
```

The env names match the compose service (`RUSTFS_VOLUMES`, `RUSTFS_ADDRESS`,
`RUSTFS_ACCESS_KEY`, `RUSTFS_SECRET_KEY`, `RUSTFS_CORS_ALLOWED_ORIGINS`). The compose
file also enables a console on 9001 (`RUSTFS_CONSOLE_*`); this StatefulSet does not.
`rustfs/rustfs:latest` is unpinned — pin a tag for production. The API creates the
bucket at boot (`EnsureBucket`); an unreachable RustFS only logs a warning there.

## B5. Migrations as a Job (run once, before app rollout)

`deploy/k8s/30-migrate-job.yaml`:

```yaml
apiVersion: batch/v1
kind: Job
metadata: { name: voltedge-migrate, namespace: voltedge }
spec:
  backoffLimit: 2
  template:
    spec:
      restartPolicy: Never
      containers:
      - name: migrate
        image: REG/voltedge-migrate:TAG
        env:
        - { name: DATABASE_URL, valueFrom: { secretKeyRef: { name: voltedge-secret, key: DATABASE_URL } } }
```

`kubectl apply -f 30-migrate-job.yaml && kubectl -n voltedge wait --for=condition=complete job/voltedge-migrate --timeout=300s`.
Re-apply it on every upgrade (delete the old Job first, or template the name with
the image tag). In Helm/Argo, make it a `pre-upgrade` hook / `PreSync`.

> **Correction.** The Job used the pooled `DATABASE_URL` (transaction-mode pooler).
> The Compose stack deliberately migrates over a direct connection. Give the Job a
> separate secret key (e.g. `DATABASE_URL_DIRECT`, pointing at `voltedge-db-rw:5432`
> with `sslmode=require`) and use it for the Job and for `pg_restore`. Also wait for
> the pooler (B10); the Cluster wait does not cover it.

## B6. The API — Deployment, Service, HPA, PDB

`deploy/k8s/40-api.yaml`:

```yaml
apiVersion: apps/v1
kind: Deployment
metadata: { name: voltedge-api, namespace: voltedge }
spec:
  replicas: 3
  selector: { matchLabels: { app: voltedge-api } }
  template:
    metadata: { labels: { app: voltedge-api } }
    spec:
      containers:
      - name: api
        image: REG/voltedge-api:TAG
        command: ["server"]
        ports: [{ containerPort: 8080 }]
        envFrom:
        - configMapRef: { name: voltedge-env }
        env:
        - { name: DATABASE_URL, valueFrom: { secretKeyRef: { name: voltedge-secret, key: DATABASE_URL } } }
        - { name: RUSTFS_ACCESS_KEY, valueFrom: { secretKeyRef: { name: voltedge-secret, key: RUSTFS_ACCESS_KEY } } }
        - { name: RUSTFS_SECRET_KEY, valueFrom: { secretKeyRef: { name: voltedge-secret, key: RUSTFS_SECRET_KEY } } }
        - { name: EXTERNAL_SYSTEMS_ENCRYPTION_KEY, valueFrom: { secretKeyRef: { name: voltedge-secret, key: EXTERNAL_SYSTEMS_ENCRYPTION_KEY } } }
        # Once the app honours it (scaling guide pre-flight #1); the CNPG pooler
        # caps real backends regardless.
        - { name: DATABASE_MAX_OPEN_CONNS, value: "20" }
        readinessProbe: { httpGet: { path: /healthz, port: 8080 }, periodSeconds: 5, failureThreshold: 3 }
        livenessProbe:  { httpGet: { path: /healthz, port: 8080 }, periodSeconds: 10, failureThreshold: 3 }
        resources:
          requests: { cpu: 500m, memory: 256Mi }
          limits:   { cpu: "2",  memory: 1Gi }
---
apiVersion: v1
kind: Service
metadata: { name: api, namespace: voltedge }
spec:
  selector: { app: voltedge-api }
  ports: [{ port: 8080, targetPort: 8080 }]
---
apiVersion: autoscaling/v2
kind: HorizontalPodAutoscaler
metadata: { name: voltedge-api, namespace: voltedge }
spec:
  scaleTargetRef: { apiVersion: apps/v1, kind: Deployment, name: voltedge-api }
  minReplicas: 3
  maxReplicas: 30
  metrics:
  - type: Resource
    resource: { name: cpu, target: { type: Utilization, averageUtilization: 60 } }
---
apiVersion: policy/v1
kind: PodDisruptionBudget
metadata: { name: voltedge-api, namespace: voltedge }
spec:
  minAvailable: 2
  selector: { matchLabels: { app: voltedge-api } }
```

**What the code actually does** (`api/cmd/server/main.go`): `/healthz` is public and
pings Postgres with a **3-second** context, returning 200 `{"status":"ok"}` or 503
`{"status":"db unreachable: …"}`. There is **no `/readyz`** (the Scaling Guide only
suggests one as an option). The server uses plain `http.ListenAndServe` with no
SIGTERM handling, so a terminating pod exits at once and drops in-flight requests —
consider a `preStop` sleep and `maxUnavailable: 0`. Kubernetes' default probe
`timeoutSeconds` is 1, shorter than the handler's own 3 s, so set `timeoutSeconds: 4`.
Because `/healthz` depends on the database, a liveness probe on it will restart every
pod during a DB outage longer than `failureThreshold × periodSeconds`; a liveness check
that does not touch Postgres is safer. *(probe-tuning advice is analysis, not
verified on a cluster)*. `DATABASE_MAX_OPEN_CONNS` is not read by the code (the env
line is a placeholder for the future change). The workers have **no HTTP server**:
never give them an `httpGet` probe. HPA utilisation is measured against the CPU
*request* (500m), so 60% is ~300m per pod.

## B7. Workers (export / webhook / import / mcp) and fieldsync

`deploy/k8s/50-workers.yaml` — same shape per worker, differing `command`. The
export/import workers also need the RustFS keys:

```yaml
apiVersion: apps/v1
kind: Deployment
metadata: { name: voltedge-exportworker, namespace: voltedge }
spec:
  replicas: 1
  selector: { matchLabels: { app: voltedge-exportworker } }
  template:
    metadata: { labels: { app: voltedge-exportworker } }
    spec:
      containers:
      - name: exportworker
        image: REG/voltedge-api:TAG
        command: ["exportworker"]
        envFrom: [{ configMapRef: { name: voltedge-env } }]
        env:
        - { name: DATABASE_URL, valueFrom: { secretKeyRef: { name: voltedge-secret, key: DATABASE_URL } } }
        - { name: RUSTFS_ACCESS_KEY, valueFrom: { secretKeyRef: { name: voltedge-secret, key: RUSTFS_ACCESS_KEY } } }
        - { name: RUSTFS_SECRET_KEY, valueFrom: { secretKeyRef: { name: voltedge-secret, key: RUSTFS_SECRET_KEY } } }
        resources: { requests: { cpu: 100m, memory: 128Mi }, limits: { cpu: "1", memory: 512Mi } }
---
apiVersion: apps/v1
kind: Deployment
metadata: { name: voltedge-webhookworker, namespace: voltedge }
spec:
  replicas: 1
  selector: { matchLabels: { app: voltedge-webhookworker } }
  template:
    metadata: { labels: { app: voltedge-webhookworker } }
    spec:
      containers:
      - name: webhookworker
        image: REG/voltedge-api:TAG
        command: ["webhookworker"]
        envFrom: [{ configMapRef: { name: voltedge-env } }]
        env:
        - { name: DATABASE_URL, valueFrom: { secretKeyRef: { name: voltedge-secret, key: DATABASE_URL } } }
        - { name: EXTERNAL_SYSTEMS_ENCRYPTION_KEY, valueFrom: { secretKeyRef: { name: voltedge-secret, key: EXTERNAL_SYSTEMS_ENCRYPTION_KEY } } }   # outbound HMAC secrets
---
# importworker: identical to webhookworker with command ["importworker"] + the
# RUSTFS_* env (it stages uploaded files). mcpserver: command ["mcpserver"] + a
# Service on 8090 if you expose the MCP tool API.
---
# fieldsync -- one replica to start; Recreate avoids overlapping builders during a
# rollout. NOT a correctness singleton: replicas are safe (see the note below).
apiVersion: apps/v1
kind: Deployment
metadata: { name: voltedge-fieldsync, namespace: voltedge }
spec:
  replicas: 1
  strategy: { type: Recreate }
  selector: { matchLabels: { app: voltedge-fieldsync } }
  template:
    metadata: { labels: { app: voltedge-fieldsync } }
    spec:
      containers:
      - name: fieldsync
        image: REG/voltedge-fieldsync:TAG
        command: ["fieldsync"]
        envFrom: [{ configMapRef: { name: voltedge-env } }]
        env:
        - { name: DATABASE_URL, valueFrom: { secretKeyRef: { name: voltedge-secret, key: DATABASE_URL } } }
        - { name: RUSTFS_ACCESS_KEY, valueFrom: { secretKeyRef: { name: voltedge-secret, key: RUSTFS_ACCESS_KEY } } }
        - { name: RUSTFS_SECRET_KEY, valueFrom: { secretKeyRef: { name: voltedge-secret, key: RUSTFS_SECRET_KEY } } }
        ports: [{ containerPort: 8091 }]
---
apiVersion: v1
kind: Service
metadata: { name: fieldsync, namespace: voltedge }
spec:
  selector: { app: voltedge-fieldsync }
  ports: [{ port: 8091, targetPort: 8091 }]
```

> **Corrections.** (1) *fieldsync is not a singleton.* The earlier text said
> "SINGLETON. Never scale" and blamed corrupt packs on scaling above 1. The compose
> header, Scaling Guide §8 and the code (`FOR UPDATE SKIP LOCKED` claims, a temp dir per job, and sync-inbox claims ordered and serialized per device — `api/internal/fieldpack/inbox.go`)
> say replicas are safe; the only caveat is that the maintenance loop runs in every
> replica and a scheduled rebuild may be double-enqueued (retention reaps the extra).
> For a build backlog, add CPU and replicas. (2) *`webhookworker` and `importworker`
> need `EXTERNAL_SYSTEMS_ENCRYPTION_KEY`* (header of `api/cmd/webhookworker/main.go`;
> both call `secretbox.NewFromEnv`): without it outbound deliveries dead-letter. The
> earlier manifest gave them only `DATABASE_URL`. (3) The three queue workers drain
> in-flight jobs on SIGTERM; `fieldsync` serves `/healthz` on 8091 but returns a bare
> `ok` without a DB check.

## B8. Web (SPA + proxy) and its k8s nginx config

In k8s the `api` Service load-balances across pods, so the nginx config drops the
resolver/variable trick and uses a **literal** upstream. `deploy/k8s/60-web.yaml`:

```yaml
apiVersion: v1
kind: ConfigMap
metadata: { name: web-nginx, namespace: voltedge }
data:
  nginx.conf: |
    worker_processes auto;
    events { worker_connections 4096; }
    http {
      include /etc/nginx/mime.types;
      limit_req_zone $binary_remote_addr zone=login:10m rate=5r/s;
      client_max_body_size 32m;
      proxy_read_timeout 60s;
      gzip on; gzip_types text/css application/javascript application/json image/svg+xml;
      server {
        listen 80;
        root /usr/share/nginx/html;
        location = /api/auth/login { limit_req zone=login burst=10 nodelay; rewrite ^/api/(.*)$ /$1 break; proxy_pass http://api:8080; }
        location /api/   { rewrite ^/api/(.*)$ /$1 break; proxy_pass http://api:8080; }
        location /tiles/ { proxy_pass http://api:8080; }
        location /assets/ { expires 1y; add_header Cache-Control "public, immutable"; try_files $uri =404; }
        location /       { try_files $uri /index.html; }
      }
    }
---
apiVersion: apps/v1
kind: Deployment
metadata: { name: voltedge-web, namespace: voltedge }
spec:
  replicas: 2
  selector: { matchLabels: { app: voltedge-web } }
  template:
    metadata: { labels: { app: voltedge-web } }
    spec:
      containers:
      - name: web
        image: REG/voltedge-web:TAG         # bakes the SPA
        ports: [{ containerPort: 80 }]
        volumeMounts: [{ name: conf, mountPath: /etc/nginx/nginx.conf, subPath: nginx.conf }]
        readinessProbe: { httpGet: { path: /, port: 80 }, periodSeconds: 10 }
      volumes:
      - name: conf
        configMap: { name: web-nginx }
---
apiVersion: v1
kind: Service
metadata: { name: web, namespace: voltedge }
spec:
  selector: { app: voltedge-web }
  ports: [{ port: 80, targetPort: 80 }]
```

> **Corrections.** (1) Behind the Ingress, `$remote_addr` in these pods is the ingress
> controller's IP, so `limit_req_zone $binary_remote_addr` puts every user in one
> 5 r/s login bucket. Restore real client addresses (nginx realip:
> `set_real_ip_from <ingress CIDR>; real_ip_header X-Forwarded-For;` — the CIDR is
> cluster-specific, *not verified*) or rate-limit at the Ingress. (2) `infra/nginx/nginx.conf`
> also sets `Host`, `X-Real-IP`, `X-Forwarded-For`, `X-Forwarded-Proto`,
> `proxy_http_version 1.1`, `proxy_connect_timeout 5s` and `proxy_send_timeout 60s`
> on each proxied location; the k8s config dropped them. The API's audit code prefers
> the first `X-Forwarded-For` hop (`api/internal/auth/service.go`), so copy them over.
> (3) The app's own per-IP limiters (`api/internal/ratelimit`) key on the TCP peer and
> ignore `X-Forwarded-For`, so behind a proxy the edge limit is the real brake.

## B9. Ingress + TLS

`deploy/k8s/70-ingress.yaml` — the app host routes to `web`; the storage host
routes to `rustfs` (for presigned URLs):

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: voltedge
  namespace: voltedge
  annotations:
    cert-manager.io/cluster-issuer: letsencrypt-prod
    nginx.ingress.kubernetes.io/proxy-body-size: "32m"
spec:
  ingressClassName: nginx
  tls:
  - hosts: [gis.example.com, storage.example.com]
    secretName: voltedge-tls
  rules:
  - host: gis.example.com
    http:
      paths:
      - path: /
        pathType: Prefix
        backend: { service: { name: web, port: { number: 80 } } }
  - host: storage.example.com
    http:
      paths:
      - path: /
        pathType: Prefix
        backend: { service: { name: rustfs, port: { number: 9000 } } }
```

The 32m matches the API's global body cap (`httpx.MaxBytes(32 << 20)`) and nginx's
`client_max_body_size 32m`; bulk bytes ride presigned PUTs straight to RustFS, which is
why the storage host needs its own route and certificate.

## B10. Apply order & bootstrap

```
kubectl apply -f deploy/k8s/00-issuer.yaml
kubectl apply -f deploy/k8s/10-namespace-config.yaml
kubectl apply -f deploy/k8s/11-secret.yaml
kubectl apply -f deploy/k8s/20-postgres.yaml
kubectl -n voltedge wait --for=condition=Ready cluster/voltedge-db --timeout=600s
kubectl apply -f deploy/k8s/25-rustfs.yaml

# migrate once, gate on completion
kubectl apply -f deploy/k8s/30-migrate-job.yaml
kubectl -n voltedge wait --for=condition=complete job/voltedge-migrate --timeout=300s

# seed: restore a validated dump with the migrate image (carries pg_restore)
kubectl -n voltedge run restore --rm -it --restart=Never \
  --image=REG/voltedge-migrate:TAG --env="DATABASE_URL=$DSN" --command -- \
  sh -c 'pg_restore --no-owner -d "$DATABASE_URL" /path/to/seeded.dump'   # mount/stream the dump in

# app tier
kubectl apply -f deploy/k8s/40-api.yaml
kubectl apply -f deploy/k8s/50-workers.yaml
kubectl apply -f deploy/k8s/60-web.yaml
kubectl apply -f deploy/k8s/70-ingress.yaml

# first break-glass admin (authadmin is baked into the api image)
kubectl -n voltedge exec deploy/voltedge-api -- \
  sh -c 'AUTHADMIN_PASSWORD=<strong> authadmin create --email admin@example.com --name "Break-glass Admin"'
```

> **Corrections.** (1) The migrate image's `ENTRYPOINT` is `bash /migrations/apply.sh`;
> `kubectl run … -- sh -c …` only replaces CMD, so without `--command` the args are
> handed to `apply.sh` and the pod re-runs migrations instead of restoring. The dump
> path in the example does not exist in the image: bake it into a derived image, mount
> it, or stream it in. (2) The Cluster wait does not cover the pooler; before the Job add
> `kubectl -n voltedge wait --for=condition=Available deploy/voltedge-db-pooler-rw --timeout=300s`
> (Deployment name per CNPG convention, *not verified*) and use the direct DSN for the Job
> and the restore (B5). (3) Order: see the A9 ordering caveat — dump first, then migrate,
> for a fresh install. *(not verified)*

## B11. Autoscaling workers on queue depth (KEDA, optional)

CPU isn't the right signal for queue workers; scale on pending-job count.
`deploy/k8s/80-keda-exportworker.yaml` (adjust the table/column to the real export
queue):

```yaml
apiVersion: keda.sh/v1alpha1
kind: ScaledObject
metadata: { name: exportworker, namespace: voltedge }
spec:
  scaleTargetRef: { name: voltedge-exportworker }
  minReplicaCount: 1
  maxReplicaCount: 8
  triggers:
  - type: postgresql
    metadata:
      connectionFromEnv: DATABASE_URL
      query: "SELECT count(*) FROM ops.export_jobs WHERE status = 'queued'"
      targetQueryValue: "5"
```

> **Correction.** The query used `status = 'pending'`. Migration 0036 constrains
> `ops.export_jobs.status` to `('queued','running','succeeded','failed','cancelled')`
> (migration 0102 gives `ops.import_jobs` the same set), so `'pending'` matched nothing
> and the scaler never left `minReplicaCount`. A job whose worker died stays `running`
> until its lease expires; count `lease_expires_at < now()` rows too if you want those
> re-claimable jobs to drive scale-out. The same pattern works for `ops.import_jobs`
> (`importworker`). KEDA's own behaviour is *not verified* here.

## B12. Upgrades (zero-downtime)

1. Build + push new `:TAG` images (§0).
2. **Run the migration Job** on the new tag first; wait for completion. Additive-
   first migrations keep the running pods compatible.
3. Roll the Deployments: `kubectl -n voltedge set image deploy/voltedge-api api=REG/voltedge-api:TAG`
   (repeat per Deployment; container names are `api`, `exportworker`, `webhookworker`,
   `fieldsync`, `web`). The rolling update plus the readiness probe keep capacity up:
   with 3 replicas and the default 25% / 25%, surge rounds up to 1 and unavailable
   rounds down to 0. The PDB does **not** govern a rolling update — it only limits
   voluntary evictions such as node drains (correction to the earlier wording).
4. Breaking schema changes: expand → migrate → deploy → contract across two
   releases (scaling guide §10).
5. Roll code back with `kubectl -n voltedge rollout undo deploy/voltedge-api`;
   migrations are not reversed.

## B13. Verify

```
kubectl -n voltedge get pods                      # all Running; api ≥3; fieldsync ≥1
kubectl -n voltedge get cluster voltedge-db       # 3 instances, 1 primary healthy
kubectl -n voltedge exec deploy/voltedge-api -- wget -qO- http://127.0.0.1:8080/healthz
```

A healthy answer is `{"status":"ok"}`; a broken database gives HTTP 503 and
`{"status":"db unreachable: …"}` (the api image is Alpine, so BusyBox `wget` is there).
Green probes do not prove the schema exists — `/healthz` only checks the DB answers.

Then from a browser over HTTPS: sign in as the admin, confirm the map renders
(tiles via `/tiles`), create a feature, run a trace, upload an attachment
(confirms the storage Ingress + presign). `kubectl -n voltedge scale deploy/voltedge-api --replicas=6`
and confirm no re-login (stateless sessions) and load spreads.

## B14. Troubleshooting (k8s-specific)

| Symptom | Cause | Fix |
|---|---|---|
| api `CrashLoopBackOff`, DB errors | DSN wrong, or pooler not ready | check `voltedge-secret` DATABASE_URL host = `voltedge-db-pooler-rw`; `kubectl get pooler` ready |
| api pods Ready but every page errors | schema missing (`/healthz` only pings the DB) | restore the dump and/or run the migrate Job |
| `pq: SSL required` / TLS errors | CNPG enforces TLS | keep `sslmode=require`; for verify, mount the cluster CA and use `verify-full` |
| migrate Job fails on re-run | Job object immutable | delete the old Job (or tag its name with the image TAG) before re-apply |
| presigned attachment URLs 404 | storage host not reachable | confirm the `storage.example.com` Ingress → `rustfs`, and `RUSTFS_PUBLIC_ENDPOINT` matches |
| login fails, cookie missing | not HTTPS, or insecure-cookies set | ensure Ingress TLS works and `AUTH_INSECURE_COOKIES` is absent |
| "prepared statement does not exist" | pgdriver vs transaction pooling | set the CNPG pooler `poolMode: session`, or disable statement caching in the DSN |
| map blank / classes missing | schema not seeded | restore the seeded dump (B10) |
| field-pack build backlog | one fieldsync replica; builds are CPU-heavy | raise `FIELDSYNC_WORKER_CONCURRENCY`, add CPU, then add replicas (**corrected**: scaling above 1 is safe — the earlier row blamed it for corrupt packs) |
| export jobs never finish; KEDA stuck at min | scaler query counts `'pending'` | count `'queued'` (B11) |
| outbound webhooks dead-letter | `webhookworker` lacks `EXTERNAL_SYSTEMS_ENCRYPTION_KEY` | add the secretKeyRef (B7) |
| api `CrashLoopBackOff` with "RUSTFS_ENDPOINT is not set" | ConfigMap not wired (`envFrom`) | restore `envFrom: configMapRef: voltedge-env` — a missing endpoint is a fatal boot error, an unreachable RustFS only warns |
| pods flip Ready/NotReady | probe `timeoutSeconds` (default 1) shorter than the 3 s DB ping in `/healthz` | `timeoutSeconds: 4`; look at pooler saturation |
| login rate-limits everyone at once | `$binary_remote_addr` is the ingress pod IP | restore client IPs (B8 correction) |

## B15. Getting started fast

`kompose convert -f docker-compose.prod.yml` produces first-draft manifests — a
scaffold, **not** this. You still replace Postgres with CloudNativePG, add the
migration Job / Ingress / HPA / PDB, swap in the web image + ConfigMap, and decide the
`fieldsync` replica count (the earlier text pinned it to a singleton; it is replica-safe). Treat kompose output as a starting point, then apply
B2–B11.

---

## Which should you pick?

- **Coolify** if you want the stack running this week on one or two servers, with
  push-to-deploy and automatic TLS, and a few-hundred-user ceiling. Lowest burden.
- **Kubernetes** if you're aiming at the ~1,000-user / HA / autoscaling target,
  have (or will build) the platform skills, and want operator-managed Postgres
  replicas and failover. Highest ceiling, highest effort.

A common path is Coolify first (prove the app and data model under real load),
then Kubernetes when scale or availability demands it — the **images and bootstrap
steps are identical**, only the orchestration changes.

> These manifests and the Coolify flow are authored from the known architecture
> but have **not** been run in CI here. Validate against your own cluster/Coolify
> instance — especially the CNPG pooler TLS/auth and the transaction-pooling
> prepared-statement caveat (scaling guide §5) — before production.

---

## Appendix — corrections and verification

**Corrections made to earlier text** (each checked against the repo):

| # | Where | Was | Now | Evidence |
|---|---|---|---|---|
| 1 | A4 override | listed only the nginx.conf mount | `volumes: !override` (base `./web/dist` mount otherwise survives the merge) | `docker-compose.prod.yml` nginx `volumes:`; Compose merge rules |
| 2 | A6 env | all listed vars assumed to reach containers | only vars the compose references do; `RUSTFS_PUBLIC_USE_SSL`, `SESSION_*`, `EXTERNAL_SYSTEMS_ENCRYPTION_KEY`, `LOG_*`, `OIDC_*` need override wiring | `docker-compose.prod.yml` api/worker `environment:`; `api/internal/export/storage.go` |
| 3 | A6 SSO | `<provider>` | Azure key is `azuread`; `OIDC_AZURE_REDIRECT_URL`; Google vars | `api/internal/auth/oidc_handlers.go` |
| 4 | A11, B7, B14 | fieldsync is a singleton, never scale | replica-safe; 1 is a starting choice | compose fieldsync comment; Scaling Guide §8; SKIP LOCKED |
| 5 | B3 extensions | `postgis`, `pgcrypto` | `postgis`, `citext` (pgcrypto optional) | `migrations/0015`, `0024`; no other `CREATE EXTENSION` |
| 6 | B5 migrate Job | pooled DSN | direct DSN | compose `bootstrap` comment; runbook §6a |
| 7 | B7 webhook/import workers | `DATABASE_URL` only | add `EXTERNAL_SYSTEMS_ENCRYPTION_KEY` | `api/cmd/webhookworker/main.go` header; `importworker` calls `secretbox.NewFromEnv` |
| 8 | B8 web nginx | login limit and proxy headers as given | realip / ingress limit; restore proxy_set_header | `infra/nginx/nginx.conf`; `api/internal/auth/service.go`; `api/internal/ratelimit` |
| 9 | B10 restore | `kubectl run … -- sh -c` | add `--command` | `deploy/migrate/Dockerfile` ENTRYPOINT |
| 10 | B11 KEDA | `status = 'pending'` | `status = 'queued'` | `migrations/0036` (and 0102) CHECK |
| 11 | B12 | "rolling update + readiness + PDB keep ≥2 serving" | PDB governs evictions, not rollouts | Kubernetes semantics *(not verified in repo)* |
| 12 | B6 notes | (absent) | `/healthz` = 3 s DB ping, no `/readyz`, no SIGTERM handling, `DATABASE_MAX_OPEN_CONNS` unread | `api/cmd/server/main.go` |

**Not verified in the repo** (needs a real Coolify / cluster): Coolify UI steps and
the additional-compose-files field; whether `.:/workspace` resolves on a Coolify build
host; the CNPG image name, `-rw` Service and pooler Deployment names, CNPG defaults;
the realip CIDR; KEDA/HPA defaults used in the simulations; pgdriver prepared-statement
behaviour under transaction pooling; Helm chart names/flags; the dump-then-migrate
ordering on a fresh install.
