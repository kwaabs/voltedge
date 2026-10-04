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

---

## 0. The images you build (both platforms)

| Image | Dockerfile | Build context | Runs |
|---|---|---|---|
| `voltedge-api` | `api/Dockerfile` | `./api` | `server` / `exportworker` / `webhookworker` / `importworker` / `mcpserver` / `authadmin` (per `command`) |
| `voltedge-fieldsync` | `api/Dockerfile.fieldsync` | `./api` | `fieldsync` (bundles tippecanoe) |
| `voltedge-web` | `web/Dockerfile` | `./web` | nginx serving the built SPA |
| `voltedge-migrate` | `deploy/migrate/Dockerfile` | **repo root** | one-shot `apply.sh` (+ carries `pg_restore`) |

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
    volumes:
      # Only the config stays external now; the SPA is inside the image.
      - ./infra/nginx/nginx.conf:/etc/nginx/nginx.conf:ro
    # Drop the published host port -- Coolify's proxy fronts this service (A7).
    ports: !reset []
```

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
   `/api`).

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
  **`fieldsync` at 1** — it is the singleton pack builder.
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
  the **Deployments** history if a release misbehaves.

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
| SPA 404s / blank after deploy | `nginx` still using the host `dist` mount | ensure the `docker-compose.coolify.yml` override is applied (nginx builds `web/Dockerfile`) |
| TLS/cert not issued | DNS not pointing at the server, or domain set on the wrong service | A-record → server IP; set the domain on the `nginx` service only |
| Login fails, cookie missing | `AUTH_INSECURE_COOKIES` set, or site not HTTPS | unset it; confirm Coolify issued the cert and you're on `https://` |
| Presigned attachment URLs 404 | `storage.example.com` not routed, or `RUSTFS_PUBLIC_ENDPOINT` mismatch | give `rustfs` its domain (A8), match `RUSTFS_PUBLIC_ENDPOINT` |
| Volumes wiped after redeploy | storage not marked persistent | A7 — mark `postgres_data`/`rustfs_data` persistent; snapshot before destructive redeploys |
| `--scale`/replica error "name in use" | a `container_name` on a scaled service | only singletons carry one here; don't add to `api`/workers |
| "prepared statement does not exist" | pgdriver vs PgBouncer transaction pooling | set `POOL_MODE: session` on the pgbouncer service, or disable statement caching in the DSN |
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
```

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
        - CREATE EXTENSION IF NOT EXISTS pgcrypto;   # gen_random_uuid()
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

CNPG manages the pooler↔Postgres auth for you (an `auth_query`), so the manual
PgBouncer/SCRAM worry from the Compose stack does not apply here — the app just
presents the `app` credentials to the pooler. `sslmode=require` encrypts to the
pooler; if pgdriver rejects the CNPG CA, mount the cluster CA and use
`sslmode=verify-full`, or keep `require` (encrypt without CA verification).

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

## B7. Workers (export / webhook / import / mcp) and the fieldsync singleton

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
---
# importworker: identical to webhookworker with command ["importworker"] + the
# RUSTFS_* env (it stages uploaded files). mcpserver: command ["mcpserver"] + a
# Service on 8090 if you expose the MCP tool API.
---
# fieldsync -- SINGLETON. Never scale; Recreate so two pack builders never run.
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
  --image=REG/voltedge-migrate:TAG --env="DATABASE_URL=$DSN" -- \
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
      query: "SELECT count(*) FROM ops.export_jobs WHERE status = 'pending'"
      targetQueryValue: "5"
```

## B12. Upgrades (zero-downtime)

1. Build + push new `:TAG` images (§0).
2. **Run the migration Job** on the new tag first; wait for completion. Additive-
   first migrations keep the running pods compatible.
3. Roll the Deployments: `kubectl -n voltedge set image deploy/voltedge-api api=REG/voltedge-api:TAG`
   (repeat per Deployment). The rolling update + readiness probe + PDB keep ≥2 API
   pods serving throughout.
4. Breaking schema changes: expand → migrate → deploy → contract across two
   releases (scaling guide §10).

## B13. Verify

```
kubectl -n voltedge get pods                      # all Running; api ≥3; fieldsync =1
kubectl -n voltedge get cluster voltedge-db       # 3 instances, 1 primary healthy
kubectl -n voltedge exec deploy/voltedge-api -- wget -qO- http://127.0.0.1:8080/healthz
```

Then from a browser over HTTPS: sign in as the admin, confirm the map renders
(tiles via `/tiles`), create a feature, run a trace, upload an attachment
(confirms the storage Ingress + presign). `kubectl -n voltedge scale deploy/voltedge-api --replicas=6`
and confirm no re-login (stateless sessions) and load spreads.

## B14. Troubleshooting (k8s-specific)

| Symptom | Cause | Fix |
|---|---|---|
| api `CrashLoopBackOff`, DB errors | DSN wrong, or pooler not ready | check `voltedge-secret` DATABASE_URL host = `voltedge-db-pooler-rw`; `kubectl get pooler` ready |
| `pq: SSL required` / TLS errors | CNPG enforces TLS | keep `sslmode=require`; for verify, mount the cluster CA and use `verify-full` |
| migrate Job fails on re-run | Job object immutable | delete the old Job (or tag its name with the image TAG) before re-apply |
| presigned attachment URLs 404 | storage host not reachable | confirm the `storage.example.com` Ingress → `rustfs`, and `RUSTFS_PUBLIC_ENDPOINT` matches |
| login fails, cookie missing | not HTTPS, or insecure-cookies set | ensure Ingress TLS works and `AUTH_INSECURE_COOKIES` is absent |
| "prepared statement does not exist" | pgdriver vs transaction pooling | set the CNPG pooler `poolMode: session`, or disable statement caching in the DSN |
| map blank / classes missing | schema not seeded | restore the seeded dump (B10) |
| two field packs built / corrupt | fieldsync scaled >1 | it must be `replicas: 1`, `strategy: Recreate` |

## B15. Getting started fast

`kompose convert -f docker-compose.prod.yml` produces first-draft manifests — a
scaffold, **not** this. You still replace Postgres with CloudNativePG, add the
migration Job / Ingress / HPA / PDB, swap in the web image + ConfigMap, and pin
`fieldsync` to a singleton. Treat kompose output as a starting point, then apply
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
