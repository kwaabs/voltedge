# VoltEdge Administration & Break-Glass Guide

A reference-complete guide for the people who **run** VoltEdge rather than use it day to day: provisioning identities, authoring the metadata that shapes the whole application, standing up an environment, and holding — and safely using — the break-glass account of last resort.

This is the third companion document. Its siblings are the [user handbook](user-handbook.md) (how-to voice for everyday users) and the [engineering studies](engineering-studies.md) reference (the `/analysis` deep-dive). Where those teach *using* VoltEdge, this one teaches *administering* it.

**A note on scope.** Chapter 2 covers standing up a working system in enough detail to bootstrap one from nothing — but this is **not** an installation manual. Setup is one chapter of an administration reference; the document's spine is the administration and break-glass model an operator lives in *after* the system is up. Read Chapter 2 when you need to build an environment; read the rest whenever you administer one.

**The recurring cast.** To keep examples concrete, this guide reuses the handbook's people — **Priya** (dispatcher), **Tom** (analyst), **Dana** (editor/field supervisor) — as ordinary users whose access an administrator manages, and introduces two new roles: **Ada**, the everyday **system administrator** (provisions users, authors metadata, runs imports), and **Owen**, the **break-glass administrator** — the emergency account that exists for the day normal access has failed. Recurring assets carry over too: Feeder 7 (11 kV, Oakwood Zone) and the `MV_CONDUCTOR` / `DIST_XFMR` feature classes.

---

## Chapter 1 — Orientation: the administration model

Before any specific admin task, you need the shape of the system and the two ideas that govern *who can do what*: **RBAC** (role-based access control — which *actions* a person may take) and **spatial access** (which *territories* those actions may touch). Everything else in this guide — user provisioning, metadata authoring, setup, break-glass — hangs off this model. This chapter builds it from the ground up.

### 1.1 Who administers VoltEdge, and this guide's two personas

VoltEdge draws a deliberate line between *operating* the network data (what the [user handbook](user-handbook.md) covers) and *administering the platform*. Administration is itself two distinct jobs, and this guide personifies them so the difference stays visible:

- **Ada — the system administrator.** Ada holds the `admin` role. She provisions users and assigns their roles, authors the metadata catalog (feature classes, fields, domains, symbology), configures map settings and external service layers, runs ETL imports, and manages territories and circuits. Ada is a *normal account* with a powerful role: she signs in the ordinary way, and every action she takes is gated by the same permission checks as everyone else — she simply holds a role that grants (almost) all of them.

- **Owen — the break-glass administrator.** Owen exists for the day the normal path fails: the SSO provider is down, local auth was switched off and no one can sign in, or the system needs a destructive out-of-band operation (a factory reset). Owen's account is **flagged** in a way Ada's is not, and that flag buys three specific emergency powers (§1.5). Owen is "the fire-axe behind glass" — rarely used, deliberately conspicuous, and audited when it is.

⚠️ **The distinction is not "more permissions."** As you'll see, the `admin` and `break-glass-admin` *roles* both grant every permission. What separates Owen from Ada is a **database column** that changes how Owen can *authenticate* and what survives a reset — not a bigger permission set. Keeping that straight is the single most important idea in this guide, and §1.5 is devoted to it.

Throughout, Priya, Tom, and Dana appear as the users whose access Ada manages: Priya needs read-only dispatch access, Tom needs analysis, Dana needs to edit and commit field data.

### 1.2 The system in one page

VoltEdge is a **metadata-driven electrical-network GIS**. The parts an administrator touches:

- **The Go API** (`api/`) — a `chi` router over the `bun` ORM against **PostgreSQL/PostGIS**. One crucial fact for security reasoning: **the API connects as a single Postgres superuser over one `DATABASE_URL`, and all access enforcement lives in Go — there is no Postgres row-level security.** Every rule in this guide is a Go check on a request, not a database grant. If a route isn't wrapped in a permission check, the database won't stop it — so the middleware *is* the security boundary.
- **The React frontend** (`web/`) — TanStack Router + React Query + OpenLayers; the admin screens live at `web/src/routes/admin-*.tsx`.
- **The Flutter field app** — offline-capable, authenticates with the same identity system via a bearer token.
- **The `cmd/` binaries** (`api/cmd/`) — the API server plus out-of-band tools: `authadmin` (create accounts), `compile` (build feature-class tables), `resetdata` (factory reset), `mcpserver`, `exportworker`, `importworker`, `fieldsync`, `uncompile`.
- **Migrations** (repo-root `migrations/`) — sequentially numbered SQL (`0001`…), applied by `migrations/apply.sh`, tracked in `ops.migration_log`.
- **The compiler** — feature classes are *authored as metadata* and then **compiled** into physical `gis.<code>` tables. This is why administration is central rather than peripheral: an admin authoring a feature class is, in effect, defining a database table and its API surface.

The `iam` schema (not `auth` — that name is reserved by the base Postgres image) holds all identity and access data: `iam.users`, `iam.sessions`, `iam.roles`, `iam.permissions`, `iam.role_permissions`, `iam.user_roles`, `iam.user_territories`, `iam.api_tokens`, `iam.system_settings`, and the `iam.auth_events` audit log.

### 1.3 RBAC: how a permission check actually works ∑

Access control is the trust boundary, and it's small enough to understand completely. The package is [api/internal/auth](api/internal/auth/); the design is documented in `docs/phase-10-auth.md`.

**The request identity.** When a request arrives, middleware resolves the caller into a **`SessionUser`** — a snapshot attached to the request context ([context.go](api/internal/auth/context.go)):

```go
type SessionUser struct {
    ID                 string
    Email              string
    DisplayName        string
    BreakGlass         bool                // the break-glass column, carried through
    Permissions        map[string]struct{} // every permission this user's roles grant
    AllowedTerritories []string            // resolved spatial scope (§1.4)
    Unscoped           bool                // holds territories:unscoped → skip spatial filter
    Subordinates       []string
    SessionID          string
    CSRFToken          string
}
```

**The check.** A permission test is exactly one method — a map lookup, nothing more:

```go
func (u *SessionUser) Has(perm string) bool {
    _, ok := u.Permissions[perm]
    return ok
}
```

💡 **There is no break-glass short-circuit in `Has()`.** A break-glass user isn't "allowed everything by fiat" — they pass permission checks because their *role* put every permission string into their `Permissions` map, exactly like the `admin` role does. This matters: if you somehow had a `break_glass = true` account with **no roles**, `Has("features:write")` would return `false`. Break-glass power over *actions* is entirely a function of the role you also assign; the column governs *authentication*, not authorization (§1.5).

**Where the permissions come from.** On session load, [session.go](api/internal/auth/session.go)'s `loadPermissions` unions every permission across the user's roles — `iam.user_roles ⋈ iam.role_permissions` — into the `Permissions` map. The same resolution runs on the API-token path (`ResolveAPIToken`), so a bearer token is scoped exactly like the user who owns it.

**The gate.** Routes are wrapped in **`RequirePermission(readPerm, writePerm)`** middleware ([rbac.go](api/internal/auth/rbac.go)):

1. No session → **401 Unauthorized**.
2. Pick the required permission by method: `readPerm` for `GET`/`HEAD`/`OPTIONS`, otherwise `writePerm`.
3. `user.Has(required)` is false → **403 Forbidden**.

So a single mount like `RequirePermission(PermMetadataRead, PermMetadataWrite)` makes reads need `metadata:read` and writes need `metadata:write` in one line. Write routes additionally require CSRF (`RequireCSRF`, a double-submit token plus a `Sec-Fetch-Site` check) on the browser path -- applied on the administrative, attachment and assistant surfaces listed in §3.6, not on every mount.

**The permission strings** are Go constants in [rbac.go](api/internal/auth/rbac.go), mirroring the `iam.permissions` catalog — `PermFeaturesRead = "features:read"`, `PermFeaturesWrite`, `PermMetadataRead/Write`, `PermUsersManage = "admin:users"`, `PermAuthSettings = "admin:auth-settings"`, `PermTerritoriesWrite`, `PermCircuitsWrite`, `PermOMS*`, `PermWAM*`, `PermFaults*`, and so on. The `:read`/`:write` convention pairs with the middleware's method split.

### 1.4 Spatial access: the second axis ∑

RBAC answers *"may this user perform this kind of action?"* Spatial access answers *"…on features in which territories?"* The two are **AND-ed**: a user needs both the permission *and* the territory to touch a feature.

The resolver is [api/internal/access](api/internal/access/) (Phase 58; `docs/phase-58-spatial-access-control.md`). It deliberately never imports `auth` — `auth` depends on it, keeping the dependency one-way:

- **`EffectiveIDs(ctx, db, userID)`** ([grants.go](api/internal/access/grants.go)) resolves a user's effective territory set with one recursive CTE: the territories granted **to the user**, *plus* those granted to **anyone who reports up to them** (down the `iam.users.manager_id` org tree), each expanded **down** the `gis.territories` subtree. A manager inherits their reports' scope; a grant on a parent territory covers its children.
- **Fail-closed.** An empty result means *"match nothing"* — never *"no filter."* A user with no grants sees no scoped data, rather than accidentally seeing everything.
- **The bypass.** Holding the permission **`territories:unscoped`** (constant `access.PermUnscoped`) sets `SessionUser.Unscoped = true`, and the spatial predicate is skipped entirely. Both `admin` and `break-glass-admin` roles hold it (seeded in migration 0094), which is *why* Ada and Owen see the whole network — not a special case in the code, just a permission.
- **Instant revocation.** The resolver's results are cached (`CachedEffectiveIDs`), and every territory/manager mutation calls `access.InvalidateAll()` after commit, so a revoked grant takes effect on the very next request.

⚠️ **Spatial enforcement ships OFF.** It's gated by the `territory_enforcement_enabled` flag (migration 0029, default off). Until an admin turns it on, RBAC is the only axis in force and every authorized user is effectively unscoped. Chapter 4 covers the rollout. `SessionUser.TerritoryFilter()` returns `(ids, unscoped)` that the crud/trace/export/tiles layers turn into a `territory_id = ANY(...)` predicate — but only when that flag is on.

### 1.5 The break-glass admin, precisely ▶∑

Now the central distinction. A **break-glass admin** is a `User` whose **`iam.users.break_glass` column is `true`**, a boolean carried through to `SessionUser.BreakGlass`. That column does **not** grant any permission. Its power is three specific authentication/lifecycle bypasses — and by convention such an account is *also* assigned the `break-glass-admin` role (every permission), so it can actually *do* things once it's in.

The three powers the **column** buys:

1. **Login bypass** ([login.go](api/internal/auth/login.go)). Normal local (password) login is gated by the `local_auth_enabled` system setting — an admin can switch it off to force everyone onto SSO. But the login handler computes `allowed := user.BreakGlass` and only consults that setting when the user is *not* break-glass. **So a break-glass account can always sign in with its local password, even when local auth is globally disabled.** This is the SSO-outage escape hatch: if Azure AD is down and everyone's locked out, Owen still gets in. Break-glass logins are audited as `eventBreakGlassLogin`.

2. **Auth-settings toggle bypass** (`setLocalAuthHandler`, [login.go](api/internal/auth/login.go)). Flipping the `local_auth_enabled` switch requires `user.BreakGlass || user.Has(PermAuthSettings)`. The break-glass leg means a **freshly bootstrapped** break-glass account — one with the column set but *no roles yet* — can still turn local auth back on for everyone. This closes the "someone disabled local auth and now no one with `admin:auth-settings` can log in to re-enable it" trap.

3. **Factory-reset survivor** (`cmd/resetdata`, Chapter 6). The destructive reset authenticates via `VerifyBreakGlassAdmin` and makes that same break-glass account the **sole surviving user** — every other user row is deleted. Only a break-glass account can authorize a reset, and only it lives through one.

**`VerifyBreakGlassAdmin(ctx, db, email, password)`** ([provision.go](api/internal/auth/provision.go)) is the authorization gate for those out-of-band tools. It looks up the user, verifies the Argon2id password on their `local` identity, and requires `break_glass == true` **and** `status == "active"`. It fails closed with a single opaque error (`"break-glass authentication failed"`) no matter which check failed — no oracle telling an attacker which part was wrong.

💡 **There is no `BREAK_GLASS_*` environment variable.** The whole concept is the `break_glass` DB column plus the `local_auth_enabled` runtime switch in `iam.system_settings`. The only break-glass-adjacent env inputs are the *passwords* the out-of-band tools read — `AUTHADMIN_PASSWORD` (account tool) and `RESETDATA_PASSWORD` (factory reset) — never flags, never the flag itself. Keeping break-glass state in the database, not the environment, is deliberate: it's runtime-toggleable and audited, not a deploy-time constant.

**Worked distinction — Ada vs Owen.** Both can create a feature class, because both roles hold `metadata:write`. But:
- If Ada disables local auth to enforce SSO and *then* SSO breaks, **Ada is locked out** (she's a normal account) — **Owen is not** (login bypass), and Owen can re-enable local auth (toggle bypass) to let Ada back in.
- If the network data is corrupted beyond repair and must be wiped to a clean slate, **only Owen** can authorize `resetdata`, and only Owen's account remains afterward to rebuild from.

That's the entire difference: **same authority to act, different authority to authenticate and to survive.** Ada is the everyday hands; Owen is the emergency key.

### 1.6 The roles, as seeded ∑

Roles map to permissions as **data**, seeded in [migrations/0025_create_rbac.sql](migrations/0025_create_rbac.sql). Five roles ship in 0025 (migration 0078 later adds `supervisor` and 0099 the three field roles `field-surveyor`, `field-enumerator`, `field-technician` -- nine in all; see the Access Control Guide, chapter 3):

| Role | Grants | Persona |
|---|---|---|
| `viewer` | read permissions (features, connectivity, diagrams, …) | Priya |
| `analyst` | viewer + trace + `analysis:run` | Tom |
| `editor` | analyst + data writes/commits — **but not** metadata or admin | Dana |
| `admin` | **every** permission (via `CROSS JOIN` over the catalog as of 0025, then explicit grants in each later migration) | Ada |
| `break-glass-admin` | **every** permission (via `CROSS JOIN`) | Owen |

Two things to internalise:

- **`editor` deliberately stops short of metadata and admin.** Dana can create and commit *features* all day, but she cannot author a feature *class*, manage users, or change auth settings. Authoring the schema is an `admin` act, separated from editing the data that lives in it.
- **`admin` and `break-glass-admin` grant the identical permission set** — both `CROSS JOIN` every permission that existed when 0025 ran; permissions added later (e.g. 0094's `territories:unscoped`, 0088/0097/0107's OMS/WAM/faults perms) reach both only because each of those migrations grants them explicitly to `admin` and `break-glass-admin`. Again: the roles are identical; the `break_glass` column is the only real difference.

**No `user_roles` are seeded.** A fresh database has the roles and permissions defined but *no one assigned to them* — the first account is created out-of-band by the `authadmin` tool (Chapter 2), which bootstraps a break-glass admin and assigns it the `admin` role. That first account is how Owen (and then Ada) come to exist at all.

### 1.7 How the rest of this guide is organised ▶

With the model in hand, the remaining chapters are:

- **Chapter 2 — System setup & bootstrap.** Standing up an environment: prerequisites, config/env, migrations, the compiler, `task seed-all`, and creating that first break-glass admin. Detailed, but a chapter — not the whole book.
- **Chapter 3 — Identity, authentication & RBAC.** Users, roles, API tokens, local/SSO/field login, and the user-admin API surface Ada uses.
- **Chapter 4 — Spatial access control.** Territories, manager chains, grants, the enforcement rollout, and `territories:unscoped`.
- **Chapter 5 — The break-glass admin.** A full treatment of the powers §1.5 introduced, plus the safety and audit model.
- **Chapter 6 — Break-glass operations.** The factory reset (`resetdata`) and the other destructive out-of-band flows, step by step.
- **Chapter 7 — Metadata & catalog administration.** Feature classes, fields, domains, material specs, symbology, CIM mappings, the admin landing page.
- **Chapter 8 — Data lifecycle administration.** ETL import, exports, private-draft/pending-edit visibility, audit trails.
- **Chapter 9 — Appendix.** Permission reference, env-var reference, admin-endpoint index, and a break-glass runbook checklist.

---

---

## Chapter 2 — System setup & bootstrap

This chapter stands up a working VoltEdge from an empty database. Read it once, when you build an environment; you will rarely return to it, because everything after this happens *inside* a running system.

That framing matters. This is **not** an installation manual — it does not chase every operating system, container runtime, or reverse-proxy topology. It is the one chapter of an administration reference that explains the state an administrator inherits: what a healthy install is *made of*, so that when something is wrong at three in the morning you know which of the five moving parts has stopped moving. We follow **Ada** as she brings up a fresh staging environment — call it *Oakwood staging* — from nothing to a login prompt she can sign into.

Bootstrap is five ordered steps, and the order is not negotiable:

1. **Configure** the environment (§2.2, §2.3) — point every process at one database and set the secrets.
2. **Migrate** (§2.4) — build the schema: `iam`, `metadata`, `gis`, `ops`, `runtime`, and the rest.
3. **Compile** (§2.5) — turn each *class definition* in `metadata` into a real physical `gis.<code>` table.
4. **Seed** (§2.6) — load the metadata catalog and (in dev) example network data.
5. **Create the first account** (§2.7) — mint the break-glass admin, because migrations seed *roles* but never *users*.

Then §2.8 verifies the result and §2.9 hands off to the rest of the guide. Skip a step or reorder it and the failure is legible once you know the model: seed before compile and the instance seeds have no table to insert into; start the server before creating an account and the login screen works but nobody can pass it.

### 2.1 What a deployment is made of

Chapter 1 established the security model — Go is the boundary, one Postgres superuser, no RLS. Setup makes that concrete: a deployment is **one database** and **a small fleet of Go processes that all open it from the same `DATABASE_URL`.**

The processes are the `cmd/` binaries. They fall into two groups:

- **Long-running services** — the ones that stay up:
  - **`server`** — the HTTP API the web app and field app talk to. Reads `DATABASE_URL` (required) and `PORT` (default `8080`; the compose stack publishes it on `API_PORT=3704`). This is the process that enforces every permission check in Chapter 1.
  - **`mcpserver`** — the Model Context Protocol server for AI agents (`MCP_PORT`, default `8090`). Optional.
  - **`exportworker`** — drains the export job queue and writes artifacts to object storage. Needed only if users export.
  - **`importworker`** — drains the ETL import queue (Chapter 8). Needed only if admins import.
  - **`fieldsync`** — builds and serves offline region packs to the Flutter field app (`FIELDSYNC_PORT`, default `8091`).
- **One-shot tools** — run to completion and exit:
  - **`compile`** — materialises `gis.<code>` tables from class metadata (§2.5).
  - **`uncompile`** — the destructive inverse; drops a class's table (Chapter 6).
  - **`authadmin`** — the out-of-band account tool that creates the first admin (§2.7). It is *deliberately* a separate binary from the server: it must be able to mint a break-glass credential before any account exists, so it can never depend on being logged in.
  - **`resetdata`** — the break-glass factory reset (Chapter 6).

Every one of these opens its own connection pool from `DATABASE_URL`. There is no privileged "admin connection" and no unprivileged one — they are all the same superuser, and the *only* thing that makes `authadmin` more powerful than `server` is that `authadmin` is a command you run from a shell with database credentials, while `server` is a daemon that answers strangers over HTTP and therefore runs everything behind the RBAC middleware. **Guarding who can run the `cmd/` tools is guarding the database itself.** Anyone who can execute `authadmin` against the production DSN can mint themselves a break-glass admin; treat that shell access with the same care as the Postgres password.

The supporting infrastructure around the database:

| Component | Role | Default port (compose) | Required for bootstrap? |
|---|---|---|---|
| **PostgreSQL/PostGIS** | The one database. Everything lives here. | `3700` | **Yes** — nothing works without it |
| **Martin** | Vector-tile server (reads geometry straight from `gis.*`) | `3701` | No — the API also has a Go MVT path |
| **RustFS** | S3-compatible object store (exports, attachments, field packs) | `3702` / `3703` | No — only exports/attachments/packs need it |
| **gitbash sidecar** | A container with `psql` on `PATH`, used by the Taskfile to run migrations and seeds against the dockerized Postgres | — | Only if you drive setup through `task` |

For a *minimal* bring-up — enough to log in and edit features on a map — Ada needs Postgres and the `server`. The object store and workers can come later; the system boots without them and simply refuses the features that need them.

> **A word on the gitbash sidecar.** The Taskfile targets a dev stack where Postgres runs in Docker and the developer is on Windows. Rather than assume `psql` on the host, `task migrate` / `task seed-*` shell into a small `gitbash` container (`docker compose exec -T gitbash psql …`) that has the client tools. If you run Postgres some other way — a managed cloud instance, a native install — you don't need the sidecar; you need *a* `psql` that can reach the database, and you run `migrations/apply.sh` against it directly (§2.4). The sidecar is a convenience of this repo's dev topology, not a requirement of VoltEdge.

### 2.2 Prerequisites

Ada needs, on the machine doing the bootstrap:

- **Go** (to run the `cmd/` tools and build the server) and **Node** (to build the web frontend) — versions per `go.mod` and the web `package.json`.
- **A PostgreSQL with PostGIS.** VoltEdge is built against the `supabase/postgres` image, which is why the auth schema is `iam` and not `auth` — `auth` is reserved by that base image (Chapter 1, §1.2). Any Postgres with the PostGIS extension available works; the schema name choice is baked into the migrations regardless.
- **`psql`** reachable from wherever migrations run (host, or the gitbash sidecar).
- **`task`** (go-task) if she wants the Taskfile wrappers. Everything the Taskfile does can be done by hand — it is a convenience layer over `go run`, `psql`, and `apply.sh` — but the wrappers encode the correct order and the correct DSN, so use them where you can.
- For object storage / field packs later: **RustFS** (or any S3-compatible store) and, for building field packs, **tippecanoe** (bundled in the fieldsync compose image).

The single most important prerequisite is conceptual, not a package: **a database URL that every process will share.** Get that right and the rest is mechanical.

### 2.3 Configuring the environment

VoltEdge is configured entirely through environment variables — there is no config file the server reads at runtime. The repository ships **`.env.example`** at its root as the authoritative catalog: copy it to `.env`, adjust, and `docker compose up` (or your process manager) feeds those values to the binaries. `.env` is gitignored; **never commit real secrets**, including the dev defaults once they stop being throwaway.

`.env.example` is heavily commented and worth reading top to bottom the first time. Here is the map an administrator needs, grouped by what actually matters at bootstrap.

#### The one variable that is never optional

```
DATABASE_URL   postgres://<user>:<pass>@<host>:<port>/<db>
```

Every `cmd/` binary opens its pool from `DATABASE_URL`. The dev DSN in this repo is `postgres://postgres:voltedge_dev@postgres:5432/postgres` *inside compose* (host `postgres`, the container name) or `…@localhost:3700/postgres` from the host (the published `POSTGRES_PORT`). Note the split: **processes inside compose reach Postgres by service name and internal port; processes on the host reach it by `localhost` and the published port.** Getting this wrong is the single most common bootstrap failure — the symptom is a process that can't connect, and the fix is always "which side of the compose boundary is this process on?"

The Taskfile encodes both sides as `LOCAL_DATABASE_URL` (host) and `CONTAINER_DATABASE_URL` (sidecar) so you don't have to think about it when using `task`.

#### The API's own knobs

```
PORT=8080            # the server's HTTP listener (compose publishes as API_PORT=3704)
```

That is the whole required surface for the API: `DATABASE_URL` + `PORT`. Everything else below is a *feature* the server switches on when its variables are present and stays quiet about when they're absent.

#### Authentication & sessions (Chapter 3 covers the behaviour; here are the knobs)

```
SESSION_COOKIE_NAME=ve_session     # rarely changed
CSRF_COOKIE_NAME=ve_csrf           # rarely changed
SESSION_IDLE_TIMEOUT=12h           # sliding window; session dies this long after last use
SESSION_ABSOLUTE_MAX=720h          # hard ceiling (30 days) regardless of activity
AUTH_INSECURE_COOKIES=1            # DROPS the Secure flag — local http:// dev ONLY
```

**`AUTH_INSECURE_COOKIES` is the one to be careful with.** It exists so cookies survive plain `http://` in local development. Set anywhere reachable over a network it silently disables the single defence that keeps a session cookie off the wire in clear text. In `.env.example` it defaults to `1` because that file *is* the dev template; in any real deployment it must be unset and the app served over TLS.

```
AUTHADMIN_PASSWORD=   # read ONLY by the authadmin CLI, NEVER by the server
```

This is not a server variable at all — it is how the `authadmin` tool (§2.7) receives the password for the account it is creating, so the password never appears as a shell-history-visible flag. The comment in `.env.example` is emphatic that you set it *just-in-time* in the invoking shell rather than leaving it in `.env`, so a break-glass credential never lingers on disk:

```
AUTHADMIN_PASSWORD='…' task authadmin-create EMAIL=… NAME='…'
```

There is, deliberately, **no `BREAK_GLASS_*` environment variable.** Break-glass status is a column on `iam.users` and a switch in `iam.system_settings`, never an env toggle (Chapter 1, §1.5; Chapter 5). The only two password-bearing env vars in the whole system are `AUTHADMIN_PASSWORD` and `RESETDATA_PASSWORD` (Chapter 6), and both are consumed by out-of-band CLIs, never by the daemon.

#### Single sign-on (optional)

Azure AD and Google OIDC live behind `OIDC_AZURE_*` and `OIDC_GOOGLE_*`. A provider is enabled **only when its variables are set and discovery succeeds at startup**; otherwise it is silently absent from the login screen, so "local-only," "one provider," and "both" are all valid postures with no separate feature flag. The redirect URL must be registered verbatim with the provider and point at `<public-base-url>/api/auth/oidc/<provider>/callback`. Crucially, **SSO never creates accounts** — an admin pre-provisions the user with the same email and the first SSO login links the identity; unknown emails are rejected. That is Chapter 3's territory; note here only that SSO is optional and off by default.

#### Object storage, workers, and the assistant (optional at bootstrap)

- **`RUSTFS_*` / `EXPORT_*`** — the S3-compatible store and the export worker's tuning (`RUSTFS_ENDPOINT` is the host:port a *process* uploads to; `RUSTFS_PUBLIC_ENDPOINT` is the host:port a *downloader* is handed in presigned URLs — they differ inside compose). Needed only for exports, attachments, and field packs.
- **`EXTERNAL_SYSTEMS_ENCRYPTION_KEY`** — a base64 32-byte AES-256 key that encrypts stored credentials for `kind='api'` external-system links. Unset means credential encryption is disabled and systems that need auth simply can't be saved; `auth_type=none` systems still work. Rotating the key makes existing stored credentials undecryptable — re-enter them after a rotation.
- **`ASSISTANT_LLM_*`** — the in-app assistant's optional language-model router. Leave `ASSISTANT_LLM_PROVIDER` and `ASSISTANT_LLM_BASE_URL` unset and the API boots with chat disabled; the deterministic snap→suggest→confirm assistance still works. The model only *routes* (parse intent, pick a tool, fill args); the deterministic engines do the domain work and every write still needs a human confirm click.

Ada, standing up *Oakwood staging* for people to test map editing, sets `DATABASE_URL`, `PORT`, leaves `AUTH_INSECURE_COOKIES=1` only because staging is behind a VPN on http (she notes to flip it before any public exposure), and defers RustFS, OIDC, and the assistant. That is a legitimate minimal configuration.

### 2.4 Building the schema: migrations

VoltEdge's schema is a sequence of plain SQL files in the repo-root **`migrations/`** directory, numbered `0001…` through the hundreds (`0113` at the time of writing). They are applied **in filename order**, and each file is applied **exactly once**. Two pieces make that work:

- **`ops.migration_log`** — the ledger of what has been applied. It is bootstrapped by `0002_create_migration_log.sql` (the runner tolerates it not existing yet on the very first pass).
- **`migrations/apply.sh`** — the runner. For every `.sql` file in the directory, in filename order, it checks whether that filename is already in `ops.migration_log`; if not, it applies the file with `psql -v ON_ERROR_STOP=1 -f` (so any error aborts the whole file — migrations are transactional per file), then records the filename, the `-- Purpose:` comment it reads from the file's header as a human description, and a `sha256sum` checksum.

Ada runs it. With the Taskfile against the dockerized Postgres:

```
task migrate
```

which is exactly `docker compose exec -T -e DATABASE_URL="…container DSN…" gitbash bash /workspace/migrations/apply.sh`. Against a Postgres she reaches directly, it is just:

```
DATABASE_URL='postgres://…' ./migrations/apply.sh
```

The properties that matter to an administrator:

- **Idempotent by ledger.** Run `apply.sh` twice and the second run applies nothing — every file is already logged. This is safe to re-run after adding new migrations; only the new files execute.
- **Ordered and gap-tolerant.** Files apply in filename order regardless of when they were authored; the numeric prefix *is* the order. A migration that was skipped (never in the log) will apply on the next run whenever the runner reaches it.
- **Fails loud.** `ON_ERROR_STOP=1` means a broken migration stops that file cold rather than leaving it half-applied. If `apply.sh` exits non-zero, read the last file it named and fix forward with a new migration — never hand-edit a file already in the ledger, because the checksum will then disagree with what actually ran.

To see what has been applied:

```
task migrate-status
```

which selects `filename, status, applied_at FROM ops.migration_log ORDER BY id` — the audit trail of the schema's construction.

At the end of this step Ada has all the schemas — `iam`, `metadata`, `ops`, `runtime`, `gis` (as a schema, but *empty of feature tables*), plus the RBAC rows: migration `0025` has seeded the five roles (`viewer`, `analyst`, `editor`, `admin`, `break-glass-admin`) and their permissions, and later migrations have appended newer permissions to `admin` and `break-glass-admin` (Chapter 1, §1.6). What she does **not** have yet is a single feature table or a single user.

### 2.5 Materialising feature tables: compile

This is the step that surprises people coming from an ordinary application, and it is the heart of VoltEdge's metadata-driven design. Migrations build the *fixed* schema — `iam`, `metadata`, `ops`. But the **feature tables themselves are not written by migrations.** A feature class like `MV_CONDUCTOR` or `DIST_XFMR` exists first as *metadata* — rows in `metadata.feature_classes`, `metadata.feature_fields`, and friends that describe its fields, subtypes, geometry, and symbology. The physical table `gis.mv_conductor` — with a real typed column per field — is generated from that metadata by the **`compile`** tool.

You run `compile` in one of two modes:

```
go run ./cmd/compile MV_CONDUCTOR      # compile one class by code
go run ./cmd/compile --all             # compile EVERY registered class
```

`--all` enumerates every row in `metadata.feature_classes` and compiles each into its `gis.<code>` table. It is **idempotent and additive** — re-running it brings tables into line with current metadata without dropping data — and it **continues past a class that can't compile**, naming the offender loudly rather than aborting the batch, so one broken class doesn't block the other fifty. The Taskfile wraps it:

```
task compile-all
```

Two consequences an administrator lives with:

- **Compile happens *after* the metadata that defines a class is loaded, and *before* any instance data for it.** That is precisely why bootstrap order is migrate → compile → seed, and why `task seed-all` (§2.6) runs `compile-all` *between* its metadata seeds and its instance seeds.
- **Adding a field to a class already holding data is a compile operation, not a migration.** For a field that can be added non-destructively, re-running `compile` for that class alters the table in place. For a change that can't be done in place, `uncompile` drops and `compile` rebuilds — which discards that class's rows (Chapter 6). This is the mechanism behind, e.g., "made `circuit_ref` a derived field" or "subtype now editable" — the class metadata changed, then the class was recompiled.

After this step Ada has real, empty `gis.*` tables ready to receive rows.

### 2.6 Seeding: metadata catalog and example data

A freshly-migrated, freshly-compiled database is *structurally* complete but *empty of content*: no feature classes described beyond whatever migrations inlined, no domains, no symbology, no example network. Seeds fill that in. In this repo the whole reseed is one task:

```
task seed-all
```

Its own description says it plainly: *reseed a freshly-migrated (empty) dev DB end to end.* Run `task migrate` first; then `seed-all` runs in **three phases**, and the phase boundaries are the same migrate→compile→seed logic in miniature:

1. **Phase 1 — metadata / config.** Load every class definition, subtype, field, domain, symbology rule, diagram stencil, placement/network/relationship rule, and CIM mapping. These write only to `metadata` (and config tables); **no compiled `gis` table is needed yet.** The order within the phase is the historical/dependency order the seeds were authored in (poles, towers, connectivity, substation yards, bays, MV switchgear, circuit refs, operational-state, and on down).
2. **Phase 2 — compile.** A single `task: compile-all`. Now that *all* class metadata is loaded, materialise every `gis.<code>` table.
3. **Phase 3 — instance / network data.** The seeds that `INSERT`/`UPDATE` actual `gis.<code>` rows — the example feeders, transformers, switches, the LV ring network, the Newton-parity network, and so on. These need the tables Phase 2 built, which is why they come last.

`seed-all` is **idempotent** — every seed is `ON CONFLICT`-guarded and `compile` is additive — so re-running it is safe. It deliberately **excludes the destructive one-off migrations** (things like `collapse-rmu-*` or `migrate-dist-switch-to-mv-switch`) and diagnostics: those exist to migrate an *existing* install forward, not to build a fresh one, and running them against a clean DB would be at best a no-op and at worst harmful.

**A boundary worth drawing for production.** Phases 1 and 2 give you a working, *empty* application — the catalog of what a feature *can be*, with real tables to hold them, but no example assets. Phase 3 is example/demo network data. On a real deployment an administrator typically wants Phases 1–2 (or a curated subset of the metadata seeds that matches the utility's asset model) and then loads *real* asset data through the ETL import engine (Chapter 8), not the bundled examples. `task seed-all` as written is the *dev* path — the fastest way to get a fully populated system to click around in. Know which parts you actually want before you point it at anything that isn't a scratch database.

Ada, on staging, wants a populated system to demo, so she runs the full `task seed-all` and gets Feeder 7, TX-Feeder7-014, the Oakwood zone, and the rest of the familiar cast — the very assets this documentation set uses in its examples.

### 2.7 Creating the first account

Here is the gap the whole chapter has been pointing at: **migrations seed roles, but never users.** `iam.user_roles` is empty after migrate. There is no default `admin@voltedge` with a known password — by design, because a shipped default credential is a shipped vulnerability. A freshly bootstrapped system has a working login screen that **nobody can pass.** Closing that gap is the job of the out-of-band **`authadmin`** tool.

`authadmin create` bootstraps the first account, and it is opinionated about what that account should be:

```
AUTHADMIN_PASSWORD='<strong password>' \
  go run ./cmd/authadmin create \
    --email owen@oakwood.example \
    --name  "Owen — Break-glass Admin"
```

or via the Taskfile wrapper:

```
AUTHADMIN_PASSWORD='<strong password>' \
  task authadmin-create EMAIL=owen@oakwood.example NAME="Owen — Break-glass Admin"
```

What `create` does, and why each choice matters:

- **It defaults to a break-glass admin.** `create` sets `--break-glass=true` and `--role admin` by default. It calls `auth.CreateUser(ctx, db, email, name, password, breakGlass)` and then `auth.AssignRole(ctx, db, email, "admin", "")`. So the very first account is both *omnipotent over actions* (the `admin` role carries every permission — Chapter 1, §1.6) and *holds the break-glass column* (the three bypasses — Chapter 1, §1.5). That is exactly the account you want as your first: it can do anything, and it can get back in even if a later misconfiguration disables normal login. This first account is **Owen** in our cast — the break-glass admin — created by whoever is doing the install.
- **The password comes from the environment, never a flag.** `authadmin` reads `AUTHADMIN_PASSWORD` from the environment so the credential never appears in shell history or process listings as a flag value. Set it just-in-time on the invoking line, as above; don't leave it in `.env`.
- **`DATABASE_URL` is required** — `authadmin` opens its own connection to the same database the server will use. It needs no running server. This is the property that makes it a *bootstrap* tool: it works when nothing else is up.
- **A role is not optional in practice.** The tool's own source carries the warning that *"an account with no role can authenticate but is authorized for nothing"* — a userwith the break-glass column but no role could log in (even bypass a disabled local-auth setting) yet `Has()` every permission-checked action as false. `create`'s default `--role admin` avoids that trap for the first account; when you later create *ordinary* users, remember that granting the account and granting its role are two steps.

`authadmin` has a wider repertoire Chapter 3 covers in full — `set-password` (reset a lost password, same `AUTHADMIN_PASSWORD` mechanism), `grant-role` / `revoke-role`, `roles`, `list`, and `mint-token` (issue a `ve_mcp_`-prefixed API bearer token for the MCP server or a field device, printed once). At bootstrap you need exactly one invocation: `create`.

With that one command, *Oakwood staging* has an account that can sign in.

> **Bootstrap vs. steady state.** `authadmin create` is how the *first* account comes to exist, out of band, from a shell. Once that account can log in, **every subsequent user is provisioned through the running system** — the `/admin/users` API behind `RequirePermission("admin:users")`, or the admin UI (Chapter 3). You should need `authadmin` again only to reset a locked-out password or to mint a token — the ordinary provisioning path is inside the app, under RBAC and audit, not on the command line.

### 2.8 Verifying a healthy install

Ada now confirms the five steps actually took. A quick, ordered smoke test — each check proves one bootstrap step and, if it fails, tells her which step to revisit:

1. **Schema exists (migrate).** `task migrate-status` lists rows through the latest migration; the highest `filename` matches the highest-numbered file in `migrations/`. If a migration is missing from the log, re-run `task migrate`.
2. **Tables exist (compile).** In `psql`, `\dt gis.*` shows a table per compiled class — `gis.mv_conductor`, `gis.dist_xfmr`, and the rest. If `gis` is empty of feature tables, `compile-all` didn't run or failed; re-run `task compile-all` and read what it names as uncompilable.
3. **Content exists (seed).** A `SELECT count(*)` against a Phase-1 metadata table (e.g. `metadata.feature_classes`) is non-zero, and — if she ran the full dev seed — a Phase-3 table like `gis.mv_conductor` has example rows.
4. **The server is up.** Start `server` (`task server-dev` or the compose service) and hit its health endpoint (`/healthz`) — it should return healthy once it has connected to the database. A server that starts but reports unhealthy is almost always a `DATABASE_URL` pointed at the wrong side of the compose boundary (§2.3).
5. **The account works (identity).** Sign in through the web app with the break-glass admin's email and the `AUTHADMIN_PASSWORD` she set, then confirm the session resolves — `GET /auth/me` (what the app calls after login) returns the user with the `admin` permissions and the break-glass flag. A login that *succeeds* but leaves the user able to do nothing means the account has no role — the "authenticate but authorized for nothing" trap from §2.7; grant the role with `authadmin grant-role` or re-run `create`.

If all five pass, the system is not merely *installed* — it is in exactly the state the rest of this guide assumes: schema built, tables compiled, catalog loaded, server answering, and one omnipotent break-glass account able to sign in and provision everyone else.

### 2.9 From bootstrap to operation

Notice what Ada has *not* done: she has not created Priya, Tom, or Dana; not granted anyone a territory; not authored a new feature class; not run an import. She has built the floor those activities stand on. Everything from here happens **inside the running system, under RBAC and audit** — which is the whole point of not skewing this document toward installation. Setup is a door you walk through once; administration is the room on the other side.

The rest of the guide is that room. The first account Ada created — Owen, the break-glass admin — is the subject of Chapter 5 and the wielder of Chapter 6's factory reset. The *ordinary* administration Ada now begins — provisioning Priya/Tom/Dana, assigning roles, granting territories — is Chapters 3 and 4. And the metadata catalog that `compile` and `seed` populated is what Chapter 7 teaches her to *author* rather than merely load.

---

---

## Chapter 3 — Identity, authentication & RBAC

Chapter 1 sketched the *model*; this chapter is the *operating manual*. It is where Ada spends most of her time: creating the accounts Priya, Tom, and Dana sign in with, giving each exactly the reach their job needs, and understanding — precisely enough to reason about a security question — how the system decides, on every single request, whether the person behind it may do the thing they asked.

Two ideas run through everything here, and it pays to hold them apart:

- **Authentication** — *who are you?* Proving identity: a password, a single-sign-on token, a bearer credential. VoltEdge has three front doors (local, SSO, field/bearer) and they all lead to the same room.
- **Authorization** — *what may you do?* Deciding, given a proven identity, whether a specific action is permitted. This is RBAC (which *actions*) and — Chapter 4 — spatial access (which *territories*).

The elegant part of VoltEdge's design, and the thing to internalise first, is that **authorization never learns how you authenticated.** A password login, an Azure AD token, and a field device's bearer token all resolve to the *same* `SessionUser` snapshot carrying the *same* effective permission set, and every gate downstream reads only that snapshot. Change how someone signs in and nothing about what they can do changes. We will see the exact code that guarantees this in §3.4.

### 3.1 The identity model: users, identities, roles, permissions

Everything lives in the `iam` schema (Chapter 1: named `iam`, not `auth`, because the base Postgres image reserves `auth`). Six tables carry the whole model, and the shape of the relationships is the whole game:

| Table | Holds | Key relationships |
|---|---|---|
| `iam.users` | one row per **account** — email, display name, `status` (`active`/`disabled`), `break_glass` bool, `manager_id` (Chapter 4) | the identity everything hangs off |
| `iam.user_identities` | one row per **way to prove you are that user** — `provider` ∈ `local`/`azuread`/`google`, and for `local`, the Argon2id `password_hash` | many-to-one → `users` |
| `iam.roles` | the **role catalog** — `key` (`viewer`…`break-glass-admin`), name, description | seeded by migration 0025 |
| `iam.permissions` | the **permission catalog** — every capability string (`features:read`, `metadata:write`, `admin:users`, …) | seeded by 0025 + appended by later migrations |
| `iam.role_permissions` | which permissions a role **grants** | many-to-many: roles ⋈ permissions |
| `iam.user_roles` | which roles a user **holds** | many-to-many: users ⋈ roles |

The design decision worth naming: **a user has no permissions of their own.** There is no `user_permissions` table, no per-user grant, no exception list. A user's power is *entirely* the union of the permissions of the roles they hold — nothing more, nothing less. That is what `loadPermissions` computes on every request (§3.4), and it is why the whole authorization surface is auditable from six tables: to know what Priya can do, you read her roles and the permissions those roles grant, and there is no seventh place a stray capability could hide.

Two corollaries an administrator lives with:

- **A user with no roles can authenticate but is authorized for nothing.** The `Permissions` map is empty; `Has(anything)` is false; every `RequirePermission` gate returns 403. This is not a bug or a locked state — it is the honest default. The freshly-bootstrapped break-glass admin from Chapter 2 avoided it only because `authadmin create` assigned the `admin` role in the same breath.
- **`identities` and `users` are distinct on purpose.** One account can have several identities — a `local` password *and* an `azuread` link — and they are separate rows. This is what lets an account be SSO-normally-but-break-glass-locally (Chapter 5), and what makes "unlink an SSO identity" a distinct admin action from "disable the account."

### 3.2 The five roles in practice

Migration 0025 seeds five roles (0078 and 0099 later add `supervisor` and three field roles), and later migrations append permissions to them (as new modules — OMS, WAM, faults — arrive). Here is what each role means in terms of the people who hold it, from least to most:

| Role | Grants, in words | Who holds it | Stops short of |
|---|---|---|---|
| **`viewer`** | Read the map, features, diagrams, connectivity, annotations — everything, but changes nothing | **Priya** the dispatcher | any write, trace, analysis |
| **`analyst`** | Everything a viewer can, **plus** `trace:run` and `analysis:run` | **Tom** the planner | any data write or commit |
| **`editor`** | Everything an analyst can, **plus** the data-writing permissions — create/edit/split features, commit connectivity and relationships, write diagrams and annotations, attachments | **Dana** the field supervisor | `metadata:*` and `admin:*` — cannot author feature classes or manage users |
| **`admin`** | **Every permission**, granted by a `CROSS JOIN` against the 0025 catalog, then by explicit grants in each later permission migration | **Ada** the system administrator | nothing, within RBAC |
| **`break-glass-admin`** | **Every permission** — an identical permission set to `admin` | **Owen** the emergency account | nothing, within RBAC |

Three things this table is quietly telling you, each of which matters:

- **The ladder is cumulative up to `editor`, then jumps.** viewer ⊂ analyst ⊂ editor is a clean nesting of *data* capability. But `editor` deliberately **stops before metadata and administration** — Dana can edit *the data* all day and cannot touch *the schema that defines the data* or *the accounts that access it*. That boundary is the single most important line in the role model, because it is the difference between "operates the system" and "shapes and governs the system." Authoring a feature class (Chapter 7) or provisioning a user (§3.3) requires crossing it.
- **`admin` and `break-glass-admin` hold the *same permissions*.** This surprises people, and it is the crux of Chapter 1's point: the two roles are permission-identical. Everything that makes Owen *different* from Ada comes not from his role but from the `break_glass` **column** on his user row — the three bypasses (login, auth-settings, reset-survivor) of Chapter 1 §1.5 and Chapter 5. As a *role*, "break-glass-admin" is just "admin"; as an *account*, Owen additionally holds the column. You can hold the column without the role (authorized for nothing but able to log in and re-enable local auth — Chapter 5) and the role without the column (Ada: omnipotent over actions, but no login/reset bypass).
- **No `user_roles` rows are seeded.** The roles *exist* after migration; nobody *holds* one until an administrator (or the bootstrap `authadmin`) grants it. A clean install has five roles and, until Chapter 2's `authadmin create`, zero role-holders.

> **Choosing a role for a real person.** The question is never "how senior are they?" but "what is the *most limited* role that still lets them do their job?" Priya dispatches — she needs to *see* everything and change nothing, so `viewer`. Tom plans — he needs to run studies but must not accidentally edit the network, so `analyst`. Dana maintains data in the field — `editor`. Reserve `admin` for the handful of people who genuinely author metadata and manage accounts, and reserve break-glass (the column) for the even smaller set who must be able to get in when everything else has failed (Chapter 5). Granting `admin` "to be safe" is the opposite of safe — it is the one role with no ceiling.

### 3.3 Provisioning users inside the running system

Chapter 2 created the *first* account out of band, because nothing existed yet. Every account after that is created **inside the running system**, through the user-administration API, under RBAC and CSRF and with an audit trail — which is exactly where account management belongs once there is an administrator to do it.

#### ▶ The operator's view: Ada onboards the team

Ada, holding `admin`, opens the user-management page (or calls the API directly) and creates Priya:

1. **Create the account** — email `priya@oakwood.example`, display name, *not* break-glass, and (because Priya will sign in locally) an initial password. 
2. **Assign the role** — `viewer`.

Two steps, and the order and separateness matter: creating an account and giving it a role are distinct actions (§3.1 — a user is nothing but their roles), and the API lets you do them together on create or separately later. Ada repeats this for Tom (`analyst`) and Dana (`editor`).

When Priya later joins the SSO rollout, Ada does *not* create a second account. She created Priya's account with the same email her identity provider knows; the **first successful SSO login links the Azure/Google identity to the existing account automatically** (§3.5). SSO never creates accounts — an unknown email is simply rejected. Pre-provisioning is the whole workflow.

#### ∑ The mechanics: the `/auth/admin` API

The surface is a chi subrouter mounted at **`/auth/admin`**, and the very first thing it does is gate the *entire* tree:

```go
func (s *service) adminRoutes() chi.Router {
    r := chi.NewRouter()
    r.Use(RequirePermission(PermUsersManage, PermUsersManage))   // "admin:users", read AND write
    r.Get("/users", …); r.Get("/roles", …)
    r.Group(func(r chi.Router) {
        r.Use(RequireCSRF)                    // every mutating route additionally
        r.Post("/users", …)                   // create
        r.Patch("/users/{id}", …)             // edit display name (email is immutable)
        r.Post("/users/{id}/roles", …)        // assign a role
        r.Delete("/users/{id}/roles/{role}", …)
        r.Post("/users/{id}/status", …)       // enable/disable
        r.Post("/users/{id}/password", …)     // set/replace local password
        r.Delete("/users/{id}/identities/{provider}", …)  // unlink SSO
        r.Post("/users/{id}/territories", …)  // Chapter 4
        …
    })
}
```

Note that `RequirePermission(PermUsersManage, PermUsersManage)` passes the *same* permission for both read and write — this is a uniformly-privileged subtree, so a `GET` list and a `POST` create both require `admin:users`. Dana (`editor`) cannot even *list* users. The gate is the real boundary; the frontend merely mirrors it by hiding the page from anyone without the permission.

The behaviours worth an administrator's attention, each grounded in the handler code:

- **Create is permissive about credentials, with one hard rule.** `POST /users` takes `email`, `display_name`, `break_glass`, an optional `role`, and an *optional* `password`. A password is optional because an **SSO-only account** is a legitimate thing to create — no local identity, links its provider on first login. The one hard rule: **`break_glass` requires a password** (`"a break-glass account requires a local password"`), because being break-glass is meaningless without a local credential to sign in with when SSO is down. Passwords set through this path must clear the 12-character floor.
- **Email is immutable through the API.** `PATCH /users/{id}` edits only the display name. Email is both the login identity *and* the SSO-link key, so changing it would orphan a linked provider — it is deliberately not editable here.
- **Disabling is immediate, and you cannot disable yourself.** `POST /users/{id}/status` with `disabled` sets the status *and revokes every live session for that user in the same handler*, so the lockout takes effect on the user's very next request, not whenever their session happens to expire. And an admin **cannot disable their own account** (`"you cannot disable your own account"`) — the guard against the classic "last admin locks themselves out" foot-gun.
- **Only SSO identities can be unlinked.** `DELETE /users/{id}/identities/{provider}` accepts only `azuread` or `google`; the local credential is managed through the password endpoint, never "unlinked." SSO *linking* is absent entirely — it happens automatically on first login, never by admin action.
- **Every mutation is audited and returns the fresh row.** Each handler writes an `iam.auth_events` entry (Chapter 8) and re-reads the account so the SPA updates in place.

### 3.4 How authorization actually happens, on every request

This is the heart of the security model, and it is worth understanding exactly, because everything else — every gate in the whole API — is just this mechanism applied at a mount point.

#### The snapshot: `SessionUser`

When the session middleware validates a request's credential, it builds one small, read-only struct and attaches it to the request context:

```go
type SessionUser struct {
    ID, Email, DisplayName string
    BreakGlass             bool
    Permissions            map[string]struct{}  // the effective set — union of all roles' permissions
    AllowedTerritories     []string             // Chapter 4
    Unscoped               bool                 // Chapter 4
    Subordinates           []string             // Chapter 4 / private drafts
    SessionID, CSRFToken   string
}
```

It "deliberately carries no password or token material." Handlers and middleware read it via `UserFrom(ctx)`; there is one unexported context key so nothing outside the `auth` package can forge it.

#### The check: `Has` is a map lookup — nothing more

```go
func (u *SessionUser) Has(perm string) bool {
    _, ok := u.Permissions[perm]
    return ok
}
```

This is the single most important five lines in the guide to read literally. **`Has` does not special-case break-glass.** There is no `if u.BreakGlass { return true }`. Owen the break-glass admin passes permission checks for the same reason Ada does: his `admin`/`break-glass-admin` **role** put every permission string into his `Permissions` map, so the map lookup succeeds. Chapter 1's memory-note "GAP: Has() doesn't short-circuit on BreakGlass yet" was reading this as an oversight; it is not — it is the design. Omnipotence over *actions* comes from the role's permissions being in the map, not from a bypass in the check. The `break_glass` column's powers (Chapter 5) are elsewhere entirely and are about *authentication and lifecycle*, not about `Has`.

#### The gate: `RequirePermission`

```go
func RequirePermission(readPerm, writePerm string) func(http.Handler) http.Handler {
    // per request:
    user, ok := UserFrom(r.Context())
    if !ok { → 401 "authentication required" }         // anonymous
    required := writePerm
    switch r.Method { case GET, HEAD, OPTIONS: required = readPerm }  // method picks read vs write
    if !user.Has(required) { → 403 "…required permission (X)" }
    next()
}
```

Three properties define the whole enforcement style:

- **401 vs 403 are distinct and meaningful.** No session at all → **401** (authenticate first). A session that lacks the permission → **403** (you are known, and refused). Priya hitting a write endpoint gets 403; a logged-out browser gets 401.
- **One gate covers a mixed read/write mount.** The HTTP *method* selects which permission is required — safe methods (`GET`/`HEAD`/`OPTIONS`) need `readPerm`, everything else needs `writePerm`. So `features` mounts with `RequirePermission(PermFeaturesRead, PermFeaturesWrite)` and a single line correctly lets viewers read and requires editors to write. A *uniform* subtree (a pure query engine, or a pure action endpoint like `analysis`) passes the *same* permission for both.
- **The permission strings are Go constants, not literals.** `rbac.go` defines `PermFeaturesRead = "features:read"`, `PermMetadataWrite = "metadata:write"`, `PermUsersManage = "admin:users"`, and the rest, so a gate in `main.go` references a symbol and can never drift from the seeded permission by a typo.

#### Where the snapshot comes from: `loadPermissions`

On each request, `loadSession` (cookie) or `ResolveAPIToken` (bearer) calls the *same* query to build the `Permissions` map — the union of every permission from every role the user holds:

```sql
SELECT DISTINCT rp.permission_key
FROM iam.user_roles ur
JOIN iam.role_permissions rp ON rp.role_id = ur.role_id
WHERE ur.user_id = ?
```

Two consequences: **a role change takes effect on the next request** (the set is recomputed every time, never cached in the session row — revoke Dana's `editor` and her next write is 403), and **a permission-load failure fails the request closed** (`"a read failure here … the request fails closed rather than silently authorizing nothing"`). The identical query backs the bearer path (`loadPermissionsFor`), which is the mechanism guaranteeing a token can never do more than its owner.

### 3.5 The three front doors

All three authentication paths converge on the same `SessionUser`; they differ only in how identity is proven.

#### Local password

`authenticateLocal` (in `login.go`) is `POST /auth/login`. Its design is uniformly defensive:

- **Every rejection looks identical.** Unknown email, disabled account, no local password, wrong password, and locked-out all return the *same* generic `"invalid email or password"` — none becomes an oracle for the others. Even the unknown-email path spends the hash cost (`verifyPassword` against a dummy hash) so a missing account can't be *timed* apart from a present one.
- **Lockout is conservative.** Five consecutive failures locks the account for 15 minutes (`maxFailedLogins = 5`, `lockoutDuration = 15m`); a success clears the counter and the lock. Suited to a small internal user base, not a consumer sign-in.
- **The local-auth switch, and the break-glass bypass.** Before checking the password, the code consults the org-wide `local_auth_enabled` setting — *unless* the account is break-glass:
  ```go
  allowed := user.BreakGlass
  if !allowed { allowed = localAuthEnabled() }
  if !allowed { → 403 "local password sign-in is currently disabled…" }
  ```
  This is Chapter 5's first break-glass power in situ: when an administrator has switched local auth *off* (forcing everyone to SSO), **a break-glass account still signs in locally**, which is exactly what makes it the SSO-outage escape hatch. A break-glass login is audited under its own event (`eventBreakGlassLogin`), distinct from an ordinary one.

#### Single sign-on (Azure AD / Google)

SSO is optional and governed entirely by the `OIDC_*` environment variables (Chapter 2 §2.3). Its rules, which an administrator must understand to provision correctly:

- **A provider exists only when configured *and* discovery succeeds at startup** — otherwise it is silently absent from the login screen. "Local only," "one provider," "both" are all valid.
- **SSO never creates accounts.** An admin pre-provisions the user with the matching email (§3.3); the **first successful SSO login links the identity** to that account; an unknown email is rejected outright. This is why onboarding is always "create the account first."
- **Each provider narrows *who* can get a valid token.** Azure AD is scoped to a single tenant by `OIDC_AZURE_TENANT_ID` (the issuer), so only that tenant's users can obtain a token at all. Google can be restricted to a Workspace domain via `OIDC_GOOGLE_ALLOWED_DOMAIN`, refusing any account outside it.
- **Authorization is unchanged.** However Priya proves her identity to Azure, she arrives as the *same* pre-provisioned account with the *same* `viewer` role. SSO changes the front door, never the room.

#### Field / bearer credentials

Non-browser clients — the MCP server for AI agents, and the Flutter field app — authenticate with a **long-lived opaque bearer token** from `iam.api_tokens` instead of a cookie, presented as `Authorization: Bearer <token>`. The model (`apitoken.go`) mirrors sessions deliberately:

- **Recognisable and hashed.** Every token is prefixed **`ve_mcp_`** (so secret scanners and operators spot it at a glance) and stored only as its SHA-256; the raw token is shown **exactly once** at mint and never persisted. A 12-character prefix is kept in the clear for identification in listings.
- **Resolves to the same snapshot.** `ResolveAPIToken` validates the token (exists, not revoked, unexpired, user still active) and returns *the same `SessionUser`* a cookie yields — same `loadPermissionsFor`, same territory scope. **A token can never do more than the user it belongs to.** It carries no `SessionID`/`CSRFToken` because a bearer client has no cookie and thus no CSRF surface.
- **Two ways to obtain one.** An admin mints one out of band with `authadmin mint-token --email <e> --name <label>` (§3.7), printed once. Or the field app exchanges an email+password for one: `FieldLogin` runs the *exact same* `authenticateLocal` policy as the browser (lockout, break-glass bypass, local-auth switch, same audit events), then mints a bearer and hands it back once — "username/password is only the FRONT of the door; a token is still what walks through it." The device keystores the token and presents it on every `/packs` and `/sync` call. Field tokens typically mint non-expiring (governed by revocation only), matching a device that must work offline for weeks.

### 3.6 Sessions, CSRF, and the response-header baseline

The browser session machinery is small, opaque, and defensive by construction:

- **Opaque, server-side sessions.** A login mints 256 bits of random token, stores only its SHA-256, and hands the raw token to the browser in a cookie once. There is no JWT, nothing self-describing — validity is a database lookup, so **revocation is instant** (logout, or disabling the account, marks the row revoked and the next request is anonymous).
- **Two expiry windows.** A **sliding idle window** (`SESSION_IDLE_TIMEOUT`, default 12h) that every valid use pushes forward, and a **hard absolute cap** (`SESSION_ABSOLUTE_MAX`, default 720h/30d) that no activity can extend. A session dies at whichever comes first.
- **Cookie hardening.** The session cookie is `HttpOnly` (JavaScript can never read it), `SameSite=Strict`, host-scoped, and `Secure` — *unless* `AUTH_INSECURE_COOKIES=1` drops the Secure flag for local http dev (Chapter 2's warning: never on a networked host). A companion CSRF cookie is intentionally *not* `HttpOnly` — the SPA reads it to echo in a header — but it is useless without the session cookie.
- **Defense-in-depth CSRF.** `RequireCSRF` guards state-changing methods with two independent checks on top of `SameSite=Strict`: the request's `Sec-Fetch-Site` header, when present, must be `same-origin` or `none` (the `Origin` header is not inspected), **and** the `X-CSRF-Token` header must equal the session's CSRF token, compared in *constant time*. Safe methods pass through. This is why every mutating admin route in §3.3 additionally carries `RequireCSRF`; the middleware is applied to a specific set of surfaces (logout and the auth switch, `/auth/admin`, attachments, assistant chat/execute, field-pack admin, sync-review) and **not** to the core data mounts such as `/features`, which rely on `SameSite=Strict` alone.
- **A baseline of security headers on every response.** `SecurityHeaders` sets `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`, `Referrer-Policy: no-referrer`, `Cross-Origin-Opener-Policy: same-origin`, and — only over TLS — HSTS. Small, reviewable middleware rather than a framework, in keeping with the "minimal audited surface" posture.

### 3.7 The `authadmin` CLI: the out-of-band twin

Everything in §3.3 has an out-of-band counterpart in the `authadmin` binary (Chapter 2 §2.7), and understanding the pairing clarifies *when* to reach for the CLI. The `/auth/admin` API is the everyday path — gated, audited, driven from the UI. `authadmin` is the **break-in-case-of-emergency path**: it works when the server is down or when *no account exists yet*, because it opens its own database connection and depends on nothing being logged in.

Its commands map onto the same `auth`-package primitives the API uses:

| `authadmin` command | Primitive | API equivalent | When you need the CLI |
|---|---|---|---|
| `create` | `CreateUser` + `AssignRole` | `POST /auth/admin/users` | Bootstrap (no account exists) |
| `set-password` | `SetLocalPassword` | `POST /users/{id}/password` | A locked-out admin can't log in to fix it |
| `grant-role` / `revoke-role` | `AssignRole` / `RemoveRole` | `POST`/`DELETE …/roles` | Rescuing a mis-roled admin |
| `list` / `roles` | `ListUsers` / `ListRoles` | `GET /users` / `/roles` | Inspecting from a shell |
| `mint-token` / `list-tokens` / `revoke-token` | `MintAPIToken` / `ListAPITokens` / `RevokeAPIToken` | — (no UI) | Issuing agent/device bearers |

Password-bearing commands read `AUTHADMIN_PASSWORD` from the environment, never a flag (Chapter 2 §2.3), so a credential never lands in shell history. `mint-token` prints the raw `ve_mcp_…` token exactly once. In steady state Ada should rarely touch `authadmin` — the reasons to are "the normal path is unavailable" (a lockout, a role mistake that removed her own access) and "there is no UI for it" (minting tokens). If you find yourself using it routinely for ordinary provisioning, that provisioning belongs in the app instead, where it is gated and audited.

### 3.8 Putting it together: a request's life

Trace one request end to end and the chapter's pieces lock into place. Tom, an `analyst`, clicks "Run fault current" — a `POST /analysis/fault-current`:

1. **`SessionLoader`** reads his session cookie, validates it (not revoked, within both windows, user still active), runs `loadPermissions` → the union of `analyst`'s permissions, resolves his territory scope (Chapter 4), and attaches the `SessionUser` to the context. A missing/invalid cookie would simply flow on as anonymous — the loader never rejects.
2. **`RequireCSRF`** (mutating method) confirms same-origin and a matching `X-CSRF-Token`.
3. **`RequirePermission(PermAnalysisRun, PermAnalysisRun)`** — the `/analysis` mount is a uniform action subtree — calls `Has("analysis:run")`. Tom's `analyst` role granted it, so the map lookup succeeds.
4. The handler runs, and when it writes, `ActorID(ctx)` stamps Tom's id into the audit columns.

Now vary the actor and watch the model do its work, with *no other code changing*:

- **Priya** (`viewer`) makes the same request → step 3's `Has("analysis:run")` is false → **403**. She authenticated fine; she is simply not authorized.
- **A logged-out browser** → step 1 attaches no user → `RequirePermission` finds none → **401**.
- **An AI agent** with Tom's `ve_mcp_` token → the API server itself reads only the session cookie, so a bearer token sent straight to `POST /analysis/...` is anonymous (**401**). The agent talks to the MCP server instead, whose step 1 is `ResolveAPIToken` rather than `loadSession`, yielding the *same* `SessionUser` with the *same* permissions; CSRF is skipped (no cookie, no CSRF surface); the tool for that action is gated on `analysis:run` exactly as the route would be → it runs, exactly as Tom, never more than Tom.
- **Owen** (break-glass, `admin` role) → `Has("analysis:run")` is true because his role put it in the map — *not* because he is break-glass. His break-glass column would matter if local auth were switched off at step 1's front door, or if he were performing a factory reset — never here.

That invariance — one snapshot, one `Has`, one gate, regardless of who you are or how you got in — is the entire security model, and it is why the rest of this guide can describe *capabilities* without re-explaining *enforcement* each time.

---

---

## Chapter 4 — Spatial access control

Chapter 3 answered *what may you do?* — the action axis. This chapter answers the second, orthogonal question: *where may you do it?* A utility does not want every `editor` able to edit every asset across the whole service area. Dana maintains the Oakwood district; she should be able to edit Oakwood's features and see Oakwood's map, and neither edit nor even *see* a feeder three regions away that another crew owns. That is **spatial access control** — territory scoping — and it is a completely separate mechanism from RBAC, ANDed on top of it.

Hold the two axes apart, because the distinction is the whole chapter:

- **RBAC (Chapter 3)** decides *the verb*: may you write features at all?
- **Spatial access (this chapter)** decides *the place*: of the features you may write, which ones are in territory you hold?

A write succeeds only when **both** pass. Dana's `editor` role says "yes, you may edit features"; her territory grants say "…but only these features." Take either away and the write is refused — a 403 from RBAC if she lacks `features:write`, a 403 (or an invisible feature) from spatial access if the feature sits outside her territories.

One framing decision governs everything that follows, and it is unusual enough to state up front: **spatial enforcement ships OFF.** A fresh VoltEdge install (Chapter 2) applies no territory predicate anywhere — every read and write behaves exactly as if this chapter's machinery did not exist. Scoping is a capability an administrator *rolls out* deliberately, after authoring territories and backfilling existing data (§4.7). So this chapter describes a system Ada *switches on*, not one that is on by default — and the "off" default is itself a load-bearing design choice, not an oversight.

### 4.1 The two trees

Spatial access is computed from two independent hierarchies that meet at the user. Internalise this diagram and the rest is detail:

```
   TERRITORY TREE (gis.territories)          ORG TREE (iam.users.manager_id)
   cascades DOWN ▼                            cascades UP ▲

   Region  ──────────────┐                    Ada (region manager)
     └─ Zone             │  a grant here            ▲
         └─ District  ◄──┘  covers the         Tom (zone lead)
             └─ (features)   whole subtree           ▲
                                               Dana (district crew)
```

- **The territory tree (`gis.territories`) cascades *down*.** Territories nest — a `Region` contains `Zone`s, a `Zone` contains `District`s, and so on, each row pointing at its `parent_id`, each sitting at a `level` (from `gis.territory_levels`, which are *ranked* — shallower to deeper). A grant on a node covers that node **and its entire subtree**. Grant Dana the *Oakwood District* and she gets Oakwood; grant her the *Oakwood Zone* and she gets every district beneath it. You never grant a subtree explicitly — you grant the node and the subtree is expanded at resolve time.
- **The org tree (`iam.users.manager_id`) cascades *up*.** Each user optionally has a manager. A manager **inherits the union of every territory grant held by everyone who reports up to them, transitively.** Grant Dana the Oakwood District, make Tom her manager, and Tom sees Oakwood *without a grant of his own* — he sees it because Dana does and Dana reports to him. Make Ada Tom's manager and Ada sees everything Tom's whole org sees.

The genius of composing these two trees is that an administrator rarely has to grant territory to managers at all. You grant the people at the edge — the crews who actually own ground — and the management chain inherits the union automatically by virtue of the org chart. Reorganise the org chart (change a `manager_id`) and everyone's effective reach re-derives on the next request, with no re-granting.

> **These are genuinely independent.** A user can hold territory grants and no reports (a lone crew member), reports and no grants of their own (a pure manager who sees only what their team sees), both, or neither (scoped to nothing — see fail-closed, §4.6). The territory tree is geography; the org tree is people; the user is where they intersect.

### 4.2 How a feature acquires a territory

Before scoping can filter by territory, every feature needs a `territory_id` — and that is assigned **at write time**, automatically, by point-in-polygon. `territory.Assign` is the single entry point the CRUD engine calls on every create and every geometry-changing update:

- **Automatic (the normal path).** With no override, `Assign` resolves the **deepest territory whose boundary contains the feature's centroid** (`ResolveContaining`) and stores that node's id. When you draw Feeder 7 inside the Oakwood District boundary, it is stamped with Oakwood District automatically. A qualifying match is stored **even when enforcement is off** — that is precisely how coverage gets populated *before* the rollout flip (§4.7).
- **Explicit override.** A caller may name a territory directly. An override is validated to exist and to be at least the minimum feature level, **always — independent of the enforcement flag** (a too-shallow assignment is a user error worth rejecting regardless of rollout). Notably, an override is **not** required to spatially contain the feature — that is the whole point: it exists for when the boundary is missing or imperfect and a human asserts membership.
- **The minimum-level rule.** A feature must belong to at least a configured minimum level (e.g. "at least a District," never merely a whole Region). `MinFeatureLevel` sets that floor.

The enforcement flag governs only the *failure case* of the automatic path. With enforcement **off**, "no territory covers this location" or "only a too-shallow one does" simply leaves the feature **unassigned** (`territory_id = NULL`) — the write still saves. With enforcement **on**, the same situation **rejects the write with a 400** (`"this location isn't within any \"district\" — no territory covers it"`, quoting the level *code*). This asymmetry is what lets an install accumulate correct territory stamps quietly during normal editing, then have Ada turn the invariant into a hard requirement once coverage is complete.

### 4.3 Granting access: the operator's view

#### ▶ Ada scopes the Oakwood crew

Everything spatial is managed through the same `/auth/admin` subrouter as roles (Chapter 3 §3.3), behind `admin:users` + CSRF. Three endpoints:

| Endpoint | Effect |
|---|---|
| `POST /auth/admin/users/{id}/territories` `{territory_id}` | Grant a user a territory node (and its subtree) |
| `DELETE /auth/admin/users/{id}/territories/{territoryId}` | Revoke a grant (idempotent — revoking one not held is a no-op success) |
| `POST /auth/admin/users/{id}/manager` `{manager_id}` | Set (or clear, with `null`) the user's manager — the org-tree edge |

So Ada, onboarding the Oakwood crew:

1. **Grants Dana the Oakwood District.** One row in `iam.user_territories`. Dana can now see and edit every feature in Oakwood District and any subtree beneath it — but nothing else.
2. **Sets Tom as Dana's manager.** Now Tom inherits Oakwood through Dana, plus whatever every other report of his holds — a zone-wide view assembled from his crews' grants, with no grant of his own.
3. **Leaves Priya (dispatcher) ungranted but sets her manager appropriately** — or grants her the zone directly if she dispatches zone-wide. A `viewer` with a zone grant sees the whole zone read-only.

Each of these three mutations calls `access.InvalidateAll()` **after the write commits**, so the change takes effect on the grantee's (and every affected manager's) *very next request* — the "instant revocation" the design requires (§4.7).

The guards worth knowing: a grant validates that both the user and the territory exist (a typo is a **400**, not a raw foreign-key 500); `SetManager` rejects a self-reference and any assignment that would **close a cycle** in the org tree (`"it would make the user manage themselves through the org tree"`) — you cannot make A report to B if B already reports up to A.

### 4.4 Resolving the effective set: `EffectiveIDs`

#### ∑ One recursive query does all three steps

When a request arrives, the session layer needs the *flat set* of territory ids this user is scoped to — the union of the two trees, fully expanded. That is `access.EffectiveIDs`, and it is a single `WITH RECURSIVE` query with three CTEs that correspond exactly to the model:

```sql
WITH RECURSIVE reports AS (               -- ① walk the ORG tree DOWN from the user
    SELECT id FROM iam.users WHERE id = ?     --    (the user themselves, plus…)
    UNION
    SELECT u.id FROM iam.users u JOIN reports r ON u.manager_id = r.id  -- …every transitive report
),
granted AS (                              -- ② the territory nodes any of those users hold
    SELECT DISTINCT ut.territory_id AS id
    FROM iam.user_territories ut JOIN reports r ON ut.user_id = r.id
),
scope AS (                                -- ③ those nodes PLUS their whole subtrees
    SELECT id FROM granted                    --    (walk the TERRITORY tree DOWN via parent_id)
    UNION
    SELECT t.id FROM gis.territories t JOIN scope s ON t.parent_id = s.id
)
SELECT id::text FROM scope
```

Read it as three steps:

1. **`reports`** — the user *and* everyone below them in the org chart. (Seeded with the user's own id because a user's own grants are of course in scope; this is the *up*-cascade of §4.1, expressed as a *down* walk from the manager's perspective.)
2. **`granted`** — every territory node any of those users was directly granted.
3. **`scope`** — those nodes plus every descendant, walking the territory tree down through `parent_id`. This is the subtree expansion of §4.1.

**Worked expansion.** Tom manages Dana. Dana holds *Oakwood District*; another of Tom's reports, Carlos, holds *Riverside District*. Tom holds nothing directly. Resolving Tom:

- `reports` = {Tom, Dana, Carlos} (Tom + his transitive reports).
- `granted` = {Oakwood District, Riverside District} (the nodes his reports hold).
- `scope` = {Oakwood District (+ its sub-nodes), Riverside District (+ its sub-nodes)}.

Tom's effective set is both districts and everything under them — assembled entirely from his crews' grants. Resolving Dana instead: `reports` = {Dana} (she manages no one), `granted` = {Oakwood District}, `scope` = Oakwood District + subtree. Exactly her patch, nothing more.

**The empty result is fail-closed.** A user with no grants and no reports who have any yields an **empty slice** — and the enforcement layer treats empty as **"match nothing,"** *never* "no filter" (§4.6). This is the single most important safety property: the absence of a grant denies access, it does not accidentally grant it.

**Caching.** Tiles arrive in bursts (many per pan/zoom), and each one resolves the session and so needs this set. `CachedEffectiveIDs` wraps `EffectiveIDs` in a process-local, 60-second TTL cache keyed by user, so a burst shares one resolver run. The TTL is only a backstop — grant and manager mutations call `InvalidateAll` for true instant revocation, so the cache can be generous without ever serving stale scope for long (§4.7).

> **A sibling primitive: `Subordinates`.** The same manager tree also feeds private-draft visibility (Chapter 8): `access.Subordinates` walks the org tree *down* from a user — their transitive reports, **excluding** themselves — so a manager can see private drafts authored by anyone beneath them. It shares the cache and the `InvalidateAll` flush. Mentioned here because it is the same tree; its use belongs to the data-lifecycle chapter.

### 4.5 The bypass: `territories:unscoped`

Some accounts must see and edit *everything*, regardless of grants — an administrator authoring across the whole network, a break-glass operator in an emergency. That is the `territories:unscoped` permission (`access.PermUnscoped = "territories:unscoped"`), seeded by migration 0094 and granted to the **`admin`** and **`break-glass-admin`** roles.

Its handling is deliberately clean, in `loadScopeFields` (package `auth`):

```go
if _, ok := perms[access.PermUnscoped]; ok {
    return nil, true, nil, nil   // Unscoped=true; skip the resolver entirely
}
allowed, err = access.CachedEffectiveIDs(...)   // scoped user: resolve the set
```

An unscoped holder **skips the resolver entirely** — no recursive query, `AllowedTerritories` left nil, `Unscoped` true. This is both correct (they are confined to nothing) and fast (admin reads and tiles never pay the resolver cost). Ada, holding `admin`, is unscoped: she sees every feature on the map. Note the layering that keeps the packages decoupled — the unscoped *check* lives in `auth` (which already loads the permission set), so package `access` never imports `auth` and never needs to know the bypass exists.

Because `admin`/`break-glass-admin` are unscoped, **Owen the break-glass admin inherently sees every private draft and every out-of-territory feature** — not through any special-casing, but because scoping is simply switched off for him at the resolver. This is the spatial half of Chapter 1's "break-glass sees everything"; it falls out of the model rather than being bolted on.

### 4.6 Enforcement at request time

Resolving the set is half the story; *applying* it is the other half. Enforcement lives not in the `access` package but at each read/write site, and it is uniformly gated so that **it is a complete no-op unless every condition holds.** The gate function is `crud.readScope`, and it returns "don't apply any predicate" when **any** of these is true:

- **enforcement is switched off** (`territory_enforcement_enabled` — the rollout gate, §4.7);
- **the class is geometry-less** (`GeometryType == "none"`) — no `territory_id` column to filter on;
- **the user is unscoped** (`admin`/`break-glass`);
- **there is no session on the context** — an internal, non-HTTP caller (the engine re-reading a row it just wrote) is trusted and unscoped. Every real HTTP read arrives behind the session middleware, so this spares only server-internal reads, never a user request.

When it *does* apply, the effective ids become a SQL predicate, and the exact shapes encode the safety rules:

- **List / query reads** append `scopeWhere`'s fragment: `territory_id::text IN (?, ?, …)` for a non-empty set, or — critically — **`1 = 0`** for an empty set. An empty effective set matches *no rows*: fail-closed, in SQL.
- **GET-by-id** uses `enforceReadScope`: a feature outside the user's set — **including a feature with no `territory_id` at all** — returns **`notFound`**, not `forbidden`. Hiding an out-of-scope feature as "not found" rather than "forbidden" avoids leaking that the id even exists. Under fail-closed, an unassigned feature is invisible to a scoped user.
- **Writes** are two checks. First, an update, delete, publish or unpublish of an *existing* feature outside the user's set (or with no territory) is refused by `enforceReadScope` as **`notFound` (404)** — the same answer a GET gives. Second, `enforceWriteScope` guards the *destination*: a **create**, or an update that supplies new geometry or a `territory_id`, must resolve to a territory in the user's set, else **`forbidden` (403)** — `"you don't have access to the territory this feature belongs to"`. A 403 is right here because the user is deliberately placing a feature somewhere they hold no grant.

And enforcement is not only in CRUD — the same effective set drives the predicate in **trace**, **export**, and **map tiles**, so a scoped user cannot route around the filter by tracing a network, exporting a region, or reading vector tiles. Every path that surfaces feature geometry consults the same scope.

### 4.7 Rolling it out (and instant revocation)

Because scoping ships **OFF**, turning it on is a deliberate, ordered rollout — and getting the order right is what keeps it from rejecting legitimate writes before the data is ready.

**The flag.** `EnforcementEnabled` reads `territory_enforcement_enabled` from `iam.system_settings` and treats **missing or unparseable as OFF** — only an explicit `"true"` turns it on. It reuses the runtime key/value settings table, so the invariant flips **at runtime with no redeploy**, the same pattern as the local-auth break-glass switch. Migration 0029 seeds the row as `'false'`, so a fresh install is off.

**The ordered rollout** (the admin surface lives on the `/territories` routes and the admin Territories page's rollout card):

1. **Author the territory tree** — create the `Region`/`Zone`/`District` nodes and their boundary polygons (Chapter 7 metadata/geodata work).
2. **Backfill existing features** — run `territory.Backfill`, which stamps a `territory_id` onto already-existing rows by the same point-in-polygon logic `Assign` uses on new writes. A greenfield install skips this; an install that accumulated features while enforcement was off needs it.
3. **Review coverage** — the coverage report shows how many features are still unassigned (no boundary covers them, or only a too-shallow one does). This is where Ada finds the stragglers *before* flipping the switch.
4. **Flip the flag** — `SetEnforcement(true)` (behind `territories:write`). From the next request, every read applies a predicate and every write must land in-territory. Notably the flip **does not** guard against remaining unassigned features — an admin may knowingly turn it on with a few stragglers pending manual review; the coverage report (step 3) is how they see that, and an unassigned feature simply becomes invisible to scoped users until it is assigned.

**Instant revocation.** Once enforcement is on, changes to *who has access* must be immediate — revoking a grant should lock someone out now, not in an hour. That is why every grant/revoke/manager mutation calls `access.InvalidateAll()` **after the transaction commits**. `InvalidateAll` flushes the *entire* effective-set cache (and the subordinate cache in lockstep):

```go
func InvalidateAll() {
    effectiveCache = map[string]effectiveCacheEntry{}     // drop everything
    subordinateCache = map[string]effectiveCacheEntry{}
}
```

A full flush rather than per-user invalidation is deliberate: a grant or manager change ripples to the grantee *and everyone who manages them up the tree*, and recomputing every active user's set once is far cheaper and safer than getting partial invalidation of the manager tree subtly wrong. It must run *after* commit, or a concurrent request could repopulate the cache from pre-commit state. The result: revoke Dana's Oakwood grant and her next request — and Tom's, since he inherited through her — resolves the new, smaller set. The 60-second TTL is only a backstop for the case where nothing was explicitly invalidated.

### 4.8 Putting it together: two axes, one decision

Return to Dana editing Feeder 7 in Oakwood, with enforcement **on**, and watch both axes participate:

1. **Authentication** (Chapter 3) — Dana's cookie resolves to her `SessionUser`. `loadScopeFields` sees she is *not* unscoped, so it resolves `CachedEffectiveIDs(Dana)` = {Oakwood District + subtree} into `AllowedTerritories`.
2. **RBAC** (Chapter 3) — her `editor` role grants `features:write`; `RequirePermission` passes. *The verb is allowed.*
3. **Spatial write guard** (this chapter) — `enforceWriteScope` checks Feeder 7's `territory_id` (Oakwood District) against her set. It is in the set. *The place is allowed.* The write commits.

Now vary the case, changing nothing else:

- Dana edits an existing feeder in **Riverside** (not her patch) → RBAC still passes (she *may* write features), but the read-scope check on the existing row finds Riverside outside her set → **404**; drawing a *new* feeder in Riverside (or dragging hers into it) reaches `enforceWriteScope` and gets **403**. The action axis said yes; the place axis said no.
- Dana *reads* the Riverside feeder by id → `enforceReadScope` → **404 not found** (existence hidden, not "forbidden").
- **Ada** (unscoped `admin`) does either → `loadScopeFields` returned `Unscoped=true`, `readScope` short-circuits, no predicate → she edits and sees everything.
- **Owen** (break-glass, unscoped) in an emergency → identical to Ada spatially: every territory, every private draft, by virtue of the bypass, no special-casing.
- The utility **hasn't rolled out scoping yet** → `EnforcementEnabled` is false → `readScope` short-circuits for *everyone*, and Dana edits Riverside freely. Spatial control is dark until Ada flips the flag.

Two orthogonal axes — *what* and *where* — resolved independently and ANDed, with a fail-closed default and an off-by-default rollout. That is the complete authorization picture; Chapter 5 turns from the everyday administrator who manages both axes to the account that exists precisely for when they have failed.

---

---

## Chapter 5 — The break-glass administrator

Every prior chapter described the system as it runs when things work: Ada provisions accounts, roles gate actions, territories gate places, SSO carries logins. This chapter is about the day things *don't* work — the SSO provider is down and no one can sign in, a configuration change has locked the administrators out, the account catalog itself is corrupt. For those days there is Owen, and the account he holds: the **break-glass administrator**.

The name is the metaphor: a fire-alarm behind glass you break only in an emergency, whose use is loud, logged, and never routine. Owen's account is not "a more powerful admin." It is a deliberately narrow set of **bypasses** that let a trusted human get back *in* and turn the lights back *on* when the normal doors are all shut — nothing more. The discipline of this chapter is to say exactly what those bypasses are, exactly what they are *not*, and how to hold such an account responsibly.

The single most important sentence in the chapter, which the rest elaborates: **break-glass is a property of an *account* (the `break_glass` column), not a set of permissions and not a role.** Owen's *ability to do things* comes from his `admin`/`break-glass-admin` role exactly like Ada's (Chapters 3–4). What the `break_glass` column adds is three things that are about *getting in and recovering*, never about *what you may do once in*.

### 5.1 What the `break_glass` column is — and is not

Mechanically, break-glass is one boolean column on `iam.users`, surfaced onto the session snapshot as `SessionUser.BreakGlass` (Chapter 3 §3.4). That is the entire storage footprint. Everything else is how three specific code paths *read* that boolean.

It is worth stating the negatives as sharply as the positives, because almost every misconception about break-glass is a confusion of the column with something else:

- **It is *not* permissions.** The column puts nothing into the `Permissions` map. A break-glass account with no role `Has()` nothing and is refused by every `RequirePermission` gate. Recall Chapter 3 §3.4: `Has` is a bare map lookup with **no break-glass short-circuit**. Owen can run analyses and edit metadata because his *role* granted those permissions, precisely as Ada's does — not because he is break-glass.
- **It is *not* a role.** `break-glass-admin` (the role) and `break_glass` (the column) are different things that happen to share a word. The role is permission-identical to `admin` (Chapter 3 §3.2). The column is the three bypasses below. You can hold either without the other (§5.5).
- **It is *not* spatial omniscience.** Owen sees every territory and every private draft — but that comes from the `territories:unscoped` permission his *role* carries (Chapter 4 §4.5), not from the column. Strip his role and give him only the column and he is scoped like anyone else (and, having no permissions, can do nothing anyway).
- **It is *not* an environment variable.** There is deliberately **no `BREAK_GLASS_*` env var** (Chapter 2 §2.3). Break-glass status is data — a column value — set through the provisioning tools, auditable in the database, and revocable by clearing the column. The only password-bearing env vars in the whole system are `AUTHADMIN_PASSWORD` and `RESETDATA_PASSWORD`, both consumed by out-of-band CLIs.

So the mental model is a **two-part identity**. A fully-functional break-glass admin like Owen is *column + role*: the role makes him omnipotent over actions and unscoped over territory (the same powers Ada has), and the column adds the three recovery bypasses (which Ada does *not* have). Keep the two halves separate in your head and every behaviour below is predictable.

### 5.2 Power 1 — the local-login bypass

**The problem it solves.** A security-conscious deployment often switches *off* local password login entirely and forces everyone onto SSO (§5.3). That is good posture — until the SSO provider has an outage. Now *no one can sign in*, including every administrator, because the only door is the one that's broken. Someone has to be able to get in and turn local login back on. That someone is a break-glass account.

#### ∑ How the bypass works

Inside `authenticateLocal` (Chapter 3 §3.5), before the password is even checked:

```go
allowed := user.BreakGlass
if !allowed {
    on, _ := s.localAuthEnabled(ctx)   // consult the org-wide switch
    allowed = on
}
if !allowed {
    _, _ = verifyPassword(password, dummyArgon2idHash)   // spend the time anyway
    → 403 "local password sign-in is currently disabled; use SSO or contact an administrator"
}
```

Read it precisely. For an **ordinary** account, local login is permitted only if the `local_auth_enabled` switch is on. For a **break-glass** account, `allowed` is `true` from the first line — it **never consults the switch at all**. So when Ada (or a prior incident) has turned local auth off and SSO then fails, Owen can *still* sign in with his email and password, because his account bypasses the switch. That is the escape hatch, and it is the whole reason the column's login power exists.

Three details that matter:

- **The check is before the password, and timing-flat.** The bypass decision happens *before* verifying the password, so a disabled-local policy never becomes a password oracle — but the code still spends the hash time (`verifyPassword` against a dummy hash) on the reject path, so you can't time the branches apart. This is the same oracle-proofing as ordinary login (Chapter 3 §3.5).
- **The switch is fail-closed.** `localAuthEnabled` treats a missing or unparseable `local_auth_enabled` row as **off** — ordinary local login stays refused unless something explicitly turned it on. Only the break-glass bypass is immune to that default.
- **It is audited under its own event.** A successful break-glass login writes `break_glass_login` to `iam.auth_events`, *distinct* from the ordinary `login_success`. So the audit trail always distinguishes "someone used the emergency account" from "someone logged in normally" — a break-glass login should be a rare, noticed line (§5.7).

### 5.3 Power 2 — the auth-settings bypass

**The problem it solves.** Once Owen is *in* during an SSO outage, he needs to actually fix the situation — turn local login back on for everyone so the ordinary admins can sign in too, do the recovery, then turn it off again. Toggling that switch normally requires the `admin:auth-settings` permission. But the whole point of a break-glass account is that it must work *even when it holds no role* — a freshly bootstrapped or deliberately minimal break-glass account with an empty permission set must still be able to flip the switch. So the toggle has its own bypass.

#### ∑ How the bypass works

`setLocalAuthHandler` (`POST /auth/settings/local-auth`):

```go
if !user.BreakGlass && !user.Has(PermAuthSettings) {
    → 403 "you do not have permission to change this setting"
}
// …then flip local_auth_enabled and record who did it
```

The authorization is `BreakGlass **OR** Has("admin:auth-settings")`. Ada flips it via her permission; **Owen flips it via the column, needing no permission at all.** The code comment names this exactly: *"during an SSO outage a break-glass admin signs in locally (bypassing the switch), turns local auth on for everyone, then off again after, and must be able to do so even on a freshly bootstrapped account that holds no role yet."*

This is the second bypass composing with the first to make a complete runbook: Power 1 gets Owen *through the door* when local login is off; Power 2 lets him *change the setting* even with no role. Together they are the SSO-outage recovery lever, and — crucially — every toggle is recorded: `setLocalAuthEnabled` stamps `updated_by` with Owen's id, and the handler writes a `local_auth_toggled` audit event with the direction of the change.

### 5.4 Power 3 — the factory-reset survivor

The third power is the heaviest, and it belongs in full to Chapter 6 (the destructive-operations chapter); here is the shape of it. The out-of-band `resetdata` tool performs a **factory reset** — wiping operational data back to a clean slate while preserving configuration. Two roles the break-glass account plays there:

- **It is the authorization gate.** `resetdata` calls `auth.VerifyBreakGlassAdmin(email, password)` and refuses to run unless it succeeds — so a factory reset can *only* be triggered by proving you hold a break-glass credential. An ordinary admin, however privileged, cannot invoke it.
- **It is the sole survivor.** The same account that authorizes the wipe becomes the *one account left standing* afterward — the reset reduces the user table to that single break-glass admin, so the system comes out of a reset with exactly one way back in: the account that ordered it.

`VerifyBreakGlassAdmin` (in `provision.go`) is the gate: it Argon2id-verifies the password *and* requires `break_glass == true` **and** `status == active`, and it **fails closed and opaque** — a missing user, a non-break-glass account, a disabled account, a missing local identity, and a wrong password all return the identical `"break-glass authentication failed"`, so it leaks nothing about which condition failed. The mechanics of the wipe itself — what is truncated, what survives, `RESETDATA_PASSWORD`, dry-run — are Chapter 6.

### 5.5 The column and the role, pulled apart

Because "break-glass admin" almost always means *both* the column and the role together, it helps to look at the four combinations explicitly — this table *is* the mental model:

| Holds column? | Holds admin role? | Who | Can do (actions) | Sees (territory) | Recovery bypasses |
|---|---|---|---|---|---|
| No | No | Priya/Tom/Dana | only their role's verbs | only their grants | none |
| **No** | **Yes** | **Ada** | everything | everything (unscoped) | **none** |
| **Yes** | **No** | a bare break-glass acct | **nothing** (empty perm set) | scoped like anyone | **all three** |
| **Yes** | **Yes** | **Owen** | everything | everything (unscoped) | **all three** |

The two middle rows are the instructive ones:

- **Ada (column No, role Yes)** is *omnipotent but not break-glass*. She can do anything the system permits, but if local auth is off and SSO fails, she is locked out exactly like everyone else, and she cannot trigger a factory reset. Power without recovery.
- **The bare break-glass account (column Yes, role No)** can *get in and turn local auth back on* during an outage — Powers 1 and 2 — but `Has()` nothing, so it is authorized for no ordinary action. Recovery without power. This is a legitimate, even prudent, configuration for a "cold" emergency account: it can restore access for the real admins and do nothing else, so its blast radius if compromised is minimal.

Owen is the bottom row — both — because in a real emergency you want one account that can get in *and* fix things. But understanding the middle rows is what lets you reason about break-glass correctly.

### 5.6 Creating and provisioning a break-glass account

A break-glass account is created the same two ways any account is (Chapters 2–3), with one hard rule enforced on both paths:

- **Out of band — `authadmin create`** (Chapter 2 §2.7). This is how the *first* one is born, before any account exists. `create` defaults `--break-glass=true` and `--role admin`, so the bootstrap account is Owen-shaped from the start. Password comes from `AUTHADMIN_PASSWORD`, never a flag.
- **Inside the system — `POST /auth/admin/users`** with `break_glass: true` (Chapter 3 §3.3), by an existing `admin`.

**The hard rule, on both paths: a break-glass account must have a local password.** The API rejects `break_glass` without a password (`"a break-glass account requires a local password"`), and `authadmin create` sets one. The reason is definitional — being break-glass *is* the ability to log in locally when SSO is down (Power 1); an account with no local credential could never exercise it, so break-glass without a password is a contradiction. And because these credentials bypass the switch, they are held to a higher bar: the provisioning path enforces a **12-character minimum** (`minLocalPasswordLen`), with the design note that "break-glass credentials are held to a higher bar than the switch-gated accounts precisely because they bypass the switch."

To **revoke** break-glass status, clear the column (disable the account, or remove the column via the provisioning path) — there is no env var to unset, no secret to rotate at the infrastructure level. It is data.

### 5.7 The audit trail

Break-glass use is meant to be *seen*. Every relevant action lands in `iam.auth_events`, the append-only security audit log, written by `logEvent`:

- **`break_glass_login`** — a distinct event from `login_success`, so an emergency login is never buried among ordinary ones. This is the line a security review looks for: *who used the break-glass account, when, from what IP?*
- **`local_auth_toggled`** — every flip of the local-auth switch, with the direction and the actor (`updated_by`), so the "turned local auth on … then off again" of a recovery is a legible pair of entries.
- **`login_denied`** — the correct-password-but-local-auth-off-and-not-break-glass case, distinguishing a policy refusal from a wrong password.

Two properties of the log to rely on: it is **append-only** (events are inserted, never updated or deleted through the app), and audit writes are **best-effort** — a failure to write an audit row must never change the outcome of the action being audited, so `logEvent` ignores its own error's effect on control flow. That trade-off is deliberate: you would rather complete an emergency login and miss one audit row than have a flaky audit table *block* the emergency login. It does mean the audit log is a strong-but-not-cryptographic record — appropriate for an internal system where the database itself is the trust boundary (Chapter 1).

### 5.8 Operational discipline

The technical bypasses are only half of break-glass; the other half is how you *hold* the account. The design gives you the mechanism; these are the practices that keep it an emergency tool rather than a liability:

- **Keep the holders few, and separate from daily admin.** Break-glass is for the handful of people trusted to recover the system. Where possible, Owen's break-glass account should be *distinct from* his everyday `admin` account — he administers day to day as ordinary-admin-Owen, and the break-glass credential is a separate, rarely-touched identity. This keeps routine work out of the `break_glass_login` audit stream and shrinks the window in which the emergency credential is in active use.
- **Guard the password like an infrastructure secret.** A break-glass password bypasses the org-wide auth switch, so it is as sensitive as the database password (Chapter 2 §2.1: shell access to the DSN is already equivalent to minting one of these). Store it where infrastructure secrets live — a sealed vault, an offline record — not in a password manager alongside routine logins, and never in `.env`.
- **Consider the "cold" configuration.** As §5.5 showed, a break-glass account *without* the admin role can restore access (Powers 1 & 2) while being authorized for nothing else. For an account that sits unused for months, that smaller blast radius can be the right trade — it can let the real admins back in and do no damage if it leaks. Weigh it against needing a *second* step (granting the role, or using a second account) to actually fix things once in.
- **Treat every `break_glass_login` as an event to explain.** Because the login is distinctly audited, a break-glass login with no corresponding incident is a red flag. Wire that event into whatever alerting the deployment has; the whole value of the distinct event type is that it can be *noticed*.
- **Rehearse the runbook before you need it.** The SSO-outage recovery — sign in locally (Power 1), turn local auth on (Power 2), let the ordinary admins back in, do the fix, turn local auth off again — should be a written, tested procedure, not something worked out under pressure during an actual outage.

### 5.9 Worked scenario: the SSO outage

Tie it together. The utility runs SSO-only: `local_auth_enabled` is **off**, and everyone signs in through Azure AD. One morning Azure AD has a tenant-wide outage. No one can log in — not Priya, not Tom, not Dana, and *not Ada*, whose omnipotent `admin` account has no recovery bypass (§5.5, middle row). The system is running fine; nobody can get into it.

Owen breaks the glass:

1. **He signs in locally.** His browser posts email + password to `/auth/login`. `authenticateLocal` sets `allowed := user.BreakGlass` — `true` — and **never consults the disabled switch** (Power 1). His password verifies; a `break_glass_login` event is written; he is in. Every other account's local login on this same endpoint would have hit the `local_auth disabled … not break-glass` 403.
2. **He turns local auth on for everyone.** He posts to `/auth/settings/local-auth` with `enabled: true`. `setLocalAuthHandler` checks `BreakGlass || Has("admin:auth-settings")` — his column satisfies it (Power 2, and his `admin` role would too). The switch flips; `local_auth_toggled → true` is audited with his id.
3. **The ordinary admins recover.** Ada and the team can now sign in with their local passwords (the switch is on), diagnose, and — if the outage is prolonged — keep operating on local auth. Owen has handed control back to the normal administrators.
4. **He restores posture afterward.** When Azure AD is healthy again, someone (Owen, or now Ada) flips `local_auth_enabled` back to `false` — another audited `local_auth_toggled` entry — returning the deployment to SSO-only. The glass is repaired.

Notice what Owen's *column* did and did not do. It did not give him any permission — every action he took, an `admin` could have taken *if they could have gotten in*. What the column uniquely provided was the ability to **get in when the front door was locked** and to **flip the one switch that unlocks it for everyone else**. That, and being the survivor of a factory reset, is the entire meaning of break-glass — a minimal, audited, account-scoped recovery capability, held in reserve for the days the rest of the system's careful access control has, by design, locked everyone out.

Chapter 6 takes up the heaviest thing a break-glass account can authorize: the factory reset itself, in full mechanical detail.

---

## Chapter 6 — Break-glass operations

Chapter 5 was about *getting in* when the doors are locked. This chapter is about the two things a break-glass account can do once it is in that are genuinely irreversible — the two commands in the codebase that discard data with no undo. Both are `cmd/` binaries, run out-of-band against `DATABASE_URL`, independent of the running API:

- **`resetdata`** — the factory reset. It wipes every operational row in the deployment (features, outages, works, attachments, the audit trail) and reduces the account set to the single break-glass admin who authorised it, while *preserving* the entire configuration: the metadata catalog, rules, formulas, templates, roles, territories. The "clean slate, same system" tool.
- **`uncompile`** — the surgical drop. It removes *one* feature class's physical table (and everything the compiler built for it) and returns that class to draft, leaving all other metadata untouched. The narrow, per-class recovery tool.

They differ by an order of magnitude in blast radius, but they share a design temperament that is worth naming up front, because it is the temperament of the whole break-glass surface: **destructive, but never *silent*.** Each one authenticates or counts before it acts, each one prints exactly what it did (how many tables, how many rows, how many objects), and each one is wrapped so that the irreversible step cannot be reached by accident. We take `resetdata` in full first — it is the heavier tool and the one a break-glass account uniquely gates — then `uncompile`.

### 6.1 `resetdata`: the operator's view ▶

There are only a few moments in a deployment's life when a factory reset is the right instrument, and all of them share a shape: *the configuration is good, but the data is not, and you want to start the data over without rebuilding the system.* Three concrete cases:

- **Pilot → production cutover.** Owen's utility ran a three-week pilot on the real production deployment: real feature classes, real symbology, real territories — but the *features* placed during the pilot were throwaway test assets, and a dozen throwaway accounts were handed out to evaluators. On go-live morning the catalog is exactly what they want to keep; the pilot's features, outages, attachments, and evaluator accounts are exactly what they want gone. A migrate-and-reseed would destroy the tuned configuration too. `resetdata` keeps the configuration and clears only the data.
- **Recovering a corrupted or contaminated dataset.** A bad bulk import, a mis-scoped ETL run, or a test that leaked into the live tables has left the operational data untrustworthy, and there is a clean external source to reload from. Rather than surgically un-pick the damage, reset to an empty-but-configured system and reload.
- **Decommissioning-in-place.** A deployment is being handed to a different team or repurposed; the configuration (which encodes months of modelling work) stays, but the operational data and everyone's accounts must go, leaving one controlled survivor to re-provision from.

What all three are *not* is routine. `resetdata` is not a "clear my test data" convenience — it takes the whole deployment down to one account and empties the object store. It sits behind the break-glass account precisely because the set of people who should be able to authorise it is the same tiny set who hold the fire-axe.

From Owen's chair the operation is deliberately two-step. He **never** runs the destructive command first. He runs the *plan*:

```
RESETDATA_PASSWORD=… go run ./cmd/resetdata --admin-email owen@grid-ops.example
```

and reads back, in full, the list of every table that *would* be truncated and the promise that every account except his own *would* be deleted. Only when that plan matches his intent does he add `--yes`. The tool is built so that the safe thing is the default and the destructive thing is an explicit, extra act.

### 6.2 The contract: what it clears, what it preserves

The single most important thing to understand about `resetdata` is the line it draws between **data** and **configuration**. The file's own header states the contract, and the code enforces it table by table. ([resetdata/main.go:6](api/cmd/resetdata/main.go#L6))

**Cleared** (truncated, identity counters restarted, then accounts reduced):

| Domain | What goes |
| --- | --- |
| `gis` | every compiled `gis.<code>` feature table — *enumerated at runtime from `metadata.feature_classes`, never hardcoded*, so admin-created classes are wiped too — plus `gis.feature_history`, `gis.feature_gps_captures`, `gis.circuits` |
| `runtime` | connectivity, relationships, external links, all diagram instances/placements/elements/templates, map annotations |
| `oms` | incidents, trouble calls, customer interruptions, switching orders and steps |
| `wam` | defects, work orders, inspections, inspection item results |
| `ops` | export jobs, field packs, the sync inbox |
| `files` | attachment metadata rows — *and* the object-store blobs behind them |
| `iam` | sessions, the `auth_events` audit trail, and **every account except the surviving break-glass admin** |

**Preserved** (the "clean slate, same system" half of the contract):

| Kept | Why it is configuration, not data |
| --- | --- |
| the entire `metadata` schema | feature-class definitions, subtypes/fields, symbology, network/trace/snapping rules, CIM mappings, calc-formula tables (`material_specs`, `protection_curves`), assembly/RMU/panel templates — months of modelling |
| `wam` inspection **templates** (`+ template_items`) | the *forms*, not the filled-in inspections |
| `gis.territories` + `territory_levels` | the territory tree is the access model's config, not operational data |
| `ops.migration_log` + `sync_policies` | the schema-version ledger and sync configuration |
| `iam` roles / permissions / basemaps / `system_settings` | the RBAC catalog and deployment settings |
| the one surviving break-glass admin | with its identity, roles, tokens, and territory grants intact |

The dividing principle, stated in the source: everything in the cleared set is *instance/operational data or an audit trail* — never a config table. That is not a comment that hopes to be true; §6.4 shows how the `TRUNCATE` is structurally *bounded* to the data set so the cascade cannot reach configuration.

Two entries on the "cleared" side deserve emphasis because operators are sometimes surprised by them:

- **The audit trail (`iam.auth_events`) is cleared.** A factory reset erases the record of the logins and role changes that preceded it. This is deliberate — a reset is a clean slate, and a slate that kept the old audit trail would not be clean — but it means *the reset itself is the last thing the old audit trail could have recorded, and it won't be there afterward.* If you need forensic history across a reset, export `auth_events` **before** you run it. (§6.9 and Chapter 8 cover the audit trail proper.)
- **Every non-survivor account is deleted, not disabled.** This is not "revoke their sessions"; the `iam.users` rows are gone, along with their identities, tokens, and territory grants (via `CASCADE`). After a reset, the deployment has exactly one account. Re-provisioning the team is a post-reset step (Chapter 3's `/auth/admin` flow, or `authadmin` out-of-band).

### 6.3 The authorization gate ∑

Before `resetdata` reads a single table name, it authenticates. ([resetdata/main.go:142](api/cmd/resetdata/main.go#L142))

```go
survivor, err := auth.VerifyBreakGlassAdmin(ctx, db, *adminEmail, pw)
```

`VerifyBreakGlassAdmin` ([provision.go:127](api/internal/auth/provision.go#L127)) is the same gate Chapter 5 introduced as "Power 3", and it is worth reading its exact checks, because they are the whole authorization story for a factory reset:

1. Look up the user by (lowercased, trimmed) email. No row → fail.
2. Find that user's **local** identity (`provider = 'local'`). No local identity, or a `NULL` password hash → fail. *(A pure-SSO account cannot authorise a reset — there is no password to verify, and the reset path is exactly the path you take when SSO may be unavailable.)*
3. Verify the supplied password against the stored Argon2id hash.
4. Require **all** of: password OK **and** `user.BreakGlass` **and** `user.Status == "active"`.

Every failure returns the same opaque string — `"break-glass authentication failed"` — so the tool never reveals *which* check failed (unknown email vs. wrong password vs. not-break-glass all look identical to a caller). And the function returns the surviving user's **id**, which the rest of the command then uses as the sole-survivor identity. This is the elegant part of the design: **the account that authorises the reset is, by construction, the account that survives it.** There is no separate "who should survive?" flag to get wrong — the authenticated authoriser *is* the survivor.

The password comes from the environment, never a flag:

```go
pw := os.Getenv("RESETDATA_PASSWORD")
if pw == "" {
    log.Fatal("resetdata: RESETDATA_PASSWORD is not set (the password is read from the environment, not a flag)")
}
```

This mirrors `AUTHADMIN_PASSWORD` (Chapter 2): a password on a command line lands in shell history, `ps` output, and process listings; a just-in-time environment variable does not. As with `authadmin`, there is **no `BREAK_GLASS_*` env var** anywhere in the system — break-glass is the `break_glass` *column* plus these two password variables (`AUTHADMIN_PASSWORD`, `RESETDATA_PASSWORD`), nothing more.

### 6.4 Dry run vs `--yes` ∑

The `--yes` flag is the entire difference between "tell me what you would do" and "do it". Without it, the command authenticates, prints the plan, and exits having changed nothing: ([resetdata/main.go:155](api/cmd/resetdata/main.go#L155))

```
DRY RUN -- nothing will be changed. Pass --yes to execute.
authenticated break-glass survivor: owen@grid-ops.example (0f3c…-…-a91b)
would TRUNCATE 47 data tables (23 compiled feature tables):
  gis.feature_history
  gis.feature_gps_captures
  gis.circuits
  runtime.feature_connectivity
  …
  gis.mv_conductor
  gis.dist_xfmr
  …
would DELETE every account except the survivor, then PURGE the object store.
```

Note what the dry run has *already* done by the time it prints: it has **authenticated** (a wrong password fails the dry run too — you cannot even preview the plan without being the break-glass admin), and it has **enumerated the live compiled feature tables** from `metadata.feature_classes`, so the count and the list reflect *this* deployment's actual classes, including any an admin created after install. What it has *not* done is open a write transaction. The dry run is a genuine no-op against the data.

This two-phase shape is the tool's primary safety mechanism, and it is why the operator discipline in §6.1 is "plan first, always". The plan is not decoration; it is the thing you diff against your intent before authorising destruction.

### 6.5 The mechanics of the wipe ∑

When `--yes` is present, the work happens in a strict order designed so that every partial-failure outcome is *safe* (recoverable by re-running) rather than *corrupt* (dangling references). There are three moving parts: assembling the target set, the transactional wipe, and the post-commit object-store purge.

**Assembling the targets.** The target list is the fixed `staticDataTables` slice ([resetdata/main.go:79](api/cmd/resetdata/main.go#L79)) — the ~24 hardcoded operational/audit tables listed in §6.2 — plus the *dynamically discovered* compiled feature tables:

```go
compiled, err := compiledFeatureTables(ctx, db)
…
targets := append(append([]string{}, staticDataTables...), compiled...)
```

`compiledFeatureTables` ([resetdata/main.go:203](api/cmd/resetdata/main.go#L203)) reads every non-empty `physical_table` from `metadata.feature_classes`, and here is the safety detail that makes runtime enumeration trustworthy: each value is checked against a strict regex before it can reach a `TRUNCATE`:

```go
var gisTableRe = regexp.MustCompile(`^gis\.[a-z0-9_]+$`)
…
if !gisTableRe.MatchString(t) {
    return nil, fmt.Errorf("refusing to truncate suspicious physical_table %q", r.PhysicalTable)
}
```

Anything that is not the compiler's exact `gis.<snake_code>` shape aborts the whole run rather than being truncated — a defence against a tampered or malformed `physical_table` value being used to point the `TRUNCATE` at something it shouldn't. And even within the `gis.` schema, the territory tree is explicitly skipped:

```go
if t == "gis.territories" || t == "gis.territory_levels" {
    continue
}
```

— belt-and-braces, because those are config even though they live in `gis`.

**The transactional wipe.** Everything destructive to the database happens inside a single `RunInTx` ([resetdata/main.go:236](api/cmd/resetdata/main.go#L236)): *either the database ends up wiped-and-reduced, or nothing changes.* There is no half-reset state. Three statements, in order:

1. **Truncate the data set.**
   ```sql
   TRUNCATE TABLE <targets…> RESTART IDENTITY CASCADE
   ```
   `RESTART IDENTITY` resets the `bigserial` counters so the fresh system starts numbering from 1 again. `CASCADE` matters here and is *bounded*: the header documents that no KEEP (config) table has a foreign key referencing any table in the data set, so the cascade can only reach FK-children that are *themselves already in the target set*. The cascade physically cannot climb into configuration — that is the structural guarantee behind the §6.2 contract, not merely a convention.

2. **Release the one blocking FK.**
   ```sql
   UPDATE iam.system_settings SET updated_by = NULL
     WHERE updated_by IS NOT NULL AND updated_by <> <survivor>
   ```
   `iam.system_settings.updated_by` is the single foreign key into `iam.users` that lives on a *kept* table and has no `ON DELETE` rule (`NO ACTION`). If a now-doomed account was the last one to touch a system setting, that reference would block the account deletion below. So it is nulled first — for everyone except the survivor, whose reference is allowed to stand. (Every other user-FK on a kept table is `CASCADE` or `SET NULL` and needs no help; the `RESTRICT` FKs on `export_jobs`/`field_packs`/`sync_inbox` all live on tables that were truncated in step 1, so they are already empty and cannot block anything.)

3. **Reduce the accounts.**
   ```sql
   DELETE FROM iam.users WHERE id <> <survivor>
   ```
   One statement removes every account but the survivor. `CASCADE` clears each deleted user's identities, roles, tokens, and territory grants; `SET NULL` detaches audit/manager references. The survivor keeps everything.

**The object-store purge, deliberately last.** Only *after* the DB transaction commits does the tool purge the blob store ([resetdata/main.go:174](api/cmd/resetdata/main.go#L174)):

```go
store, err := export.NewObjectStoreFromEnv()
…
n, err := store.Purge(ctx)
```

`Purge` ([storage.go:190](api/internal/export/storage.go#L190)) lists the bucket recursively and removes every object, returning the count. The **ordering is a correctness argument**, spelled out in the source: if the DB commit succeeds but the purge fails, the worst case is *orphaned blobs* — objects with no surviving row pointing at them (the `files.attachments` rows were already truncated) — which a re-run of `resetdata` clears. The reverse order could leave `files.attachments` rows pointing at deleted blobs. The tool chooses the recoverable failure mode. Its error messages say exactly this, e.g. "*data wiped, but object-store purge failed … (blobs may remain, re-run to finish)*", and even the "object store not configured" path tells you to set `RUSTFS_*` and re-run to purge. Nothing is left ambiguous.

On success the tool prints the tally — tables truncated, accounts reduced, objects purged — and `factory reset complete -- configuration preserved, data cleared.`

### 6.6 Running it: production vs. dev

In **production there is no Go toolchain** to `go run` from, and this is the operationally important part: the `resetdata` binary is baked into the api image (`api/Dockerfile`) and run against the live deployment with the api service's own environment — the same `DATABASE_URL` and `RUSTFS_*` the API uses:

```
docker compose exec -e RESETDATA_PASSWORD=… api resetdata --admin-email owen@grid-ops.example --yes
```

Run the plan first (drop the `--yes`), read it, then re-run with `--yes`. Whoever can `docker compose exec` into the api service and knows a break-glass admin's password can factory-reset the deployment — which is the same "guarding shell access is guarding the database" boundary from Chapter 2, now with teeth.

In **development**, `task reset-data-plan` and `task reset-data` are thin sugar over the same binary ([Taskfile.yml:312](Taskfile.yml#L312)):

- `task reset-data-plan EMAIL=…` → `go run ./cmd/resetdata --admin-email {{.EMAIL}}` (dry run).
- `task reset-data EMAIL=…` → adds an **interactive confirmation** before `--yes`: a PowerShell prompt that spells out "*This ERASES all features, outages, works, attachments, and every account except the break-glass admin. Type YES to continue*" and aborts on anything but a literal `YES`, *then* runs `--admin-email … --yes`.

So in dev there are *two* gates in front of destruction (type `YES`, and hold the break-glass password) and in prod there is the explicit `--yes` plus the password. Set `RESETDATA_PASSWORD` in your environment first either way; it is never a task variable.

### 6.7 Worked scenario: Owen resets the pilot for go-live

Pilot's over. The catalog is final, the pilot features and evaluator accounts must go. Owen is `owen@grid-ops.example`, a `break_glass = true` admin with the `admin` role.

1. **He exports what he needs to keep across the reset.** The audit trail will be erased, so if compliance wants the pilot's login history, he exports `iam.auth_events` now. (There is nothing else to save — the catalog is preserved by design.)
2. **He runs the plan.** `RESETDATA_PASSWORD=… docker compose exec -e RESETDATA_PASSWORD api resetdata --admin-email owen@grid-ops.example`. It authenticates him, then prints "*would TRUNCATE 47 data tables (23 compiled feature tables)*" and lists them — including `gis.mv_conductor` and `gis.dist_xfmr`, the classes he wants emptied but *kept as classes*. He confirms the list is data, not config. Critically, he sees his catalog tables are **absent** from the list.
3. **He authorises.** He re-runs with `--yes`. In one transaction the 47 tables truncate, `iam.system_settings.updated_by` is released for the departing accounts, and every account but Owen's is deleted. The DB commits.
4. **The blobs go.** The object store purge runs, removing the pilot's attachment photos and export artifacts; it prints "*purged 1,284 object(s)*".
5. **He rebuilds the team.** The deployment now has exactly one account — his. He signs in and re-provisions Ada and the operational users via `/auth/admin` (Chapter 3), re-grants territories (Chapter 4), and go-live begins on an empty-but-fully-configured system.

What Owen's break-glass column bought here was, again, narrow: it *authorised* the reset (the `VerifyBreakGlassAdmin` gate) and made him the survivor. Every other capability he used afterward — provisioning, granting — came from his `admin` **role**, exactly as Chapter 5 insisted.

### 6.8 `uncompile`: the surgical drop ▶∑

`uncompile` is the far smaller sibling: it drops **one** feature class's physical table and returns that class to draft. Where `resetdata` clears all data and keeps all config, `uncompile` clears one class's data *and* its compiled table, keeping the class's *definition* so it can be rebuilt.

**Why it exists ▶.** The compiler is **additive-only**: it can `ALTER TABLE ADD COLUMN` a new field onto an existing class, but it never rewrites the table. That leaves one blind spot — you cannot add a *required* (`NOT NULL`) field to a class that already has rows, because Postgres rejects `ADD COLUMN … NOT NULL` on a populated table (there is no value for the existing rows, SQLSTATE 23502). The recovery is: drop the table, then recompile it fresh — an empty table accepts the `NOT NULL` column cleanly. `uncompile` is the "drop the table, keep the class" step, so an operator never has to hand-write `DROP TABLE`, remember the Martin tile function, and reset the metadata columns by hand. ([uncompile.go:22](api/internal/compiler/uncompile.go#L22))

Concretely, Dana the modeller realises `MV_FUSE` needs a mandatory `rated_current` field, but there are already 40 `MV_FUSE` rows in the pilot. She (or an admin) exports those 40 rows, runs:

```
task uncompile CODE=MV_FUSE          # → go run ./cmd/uncompile MV_FUSE
```

then adds the required field in the metadata admin and recompiles:

```
task compile CODE=MV_FUSE
```

and re-imports the 40 rows with the new field populated.

**What it drops, what it keeps ∑.** `compiler.Uncompile` ([uncompile.go:45](api/internal/compiler/uncompile.go#L45)) does all of this in one transaction:

- **Counts the rows first**, inside the same transaction, so the number it reports is exactly what the drop removes — a destructive drop is *never silent about how much it discarded*.
- **Drops the Martin tile function** `gis.<table>(z,x,y)` first (only for a geometric class; `IF EXISTS` makes it a no-op otherwise), *before* the table, so a failure rolls back cleanly rather than leaving a tile function pointing at a gone table.
- **Drops the table `CASCADE`**, which also removes the spatial index and the registry-sync trigger (migration 0108).
- **Clears this class's rows from `runtime.feature_registry`** (a separate table, not an FK-dependent of the `gis` table), so the cross-class id index doesn't advertise ids whose backing table is gone; a later compile backfills them.
- **Resets the class metadata** to the un-compiled state — `physical_table = NULL`, `status = 'draft'` — exactly what a never-compiled class looks like.

Everything *else* about the class is left untouched: fields, subtypes, terminals, domain links, and every rule that references it (symbology, CIM, connectivity, snapping). That is the whole point — a subsequent `compile` rebuilds the table from scratch with the *current* field set.

The CLI ([uncompile/main.go:52](api/cmd/uncompile/main.go#L52)) reports the outcome honestly, including the row count and the next step:

```
uncompiled MV_FUSE -> dropped gis.mv_fuse (40 row(s) discarded); metadata reset to draft. Run `compile MV_FUSE` to rebuild.
```

and if there was no physical table to begin with it says so and re-asserts the draft state — `uncompile` is **idempotent**. Note the two differences from `resetdata` in temperament: `uncompile` has **no break-glass gate** (it is a metadata-admin recovery tool, run against `DATABASE_URL` like `compile`, not a break-glass-only operation) and **no dry run** — its safety comes from being surgical (one named class) and from printing the row count it discarded. Back the data up first if it matters; there is no undo. The same operation is also exposed inside the running system at `POST /admin/feature-classes/{code}/uncompile` behind the metadata-write permission ([handler.go:474](api/internal/admin/handler.go#L474)), for admins who prefer the UI to the CLI — same `compiler.Uncompile`, same guarantees.

### 6.9 Discipline for both tools

Three habits close the chapter:

- **Plan or count before you commit.** For `resetdata`, always run the dry run and diff the table list against your intent — confirm your *config* tables are absent from it. For `uncompile`, read the row count in the output; if it surprises you, stop and back up.
- **Export anything that must survive the operation.** `resetdata` erases the audit trail and every non-survivor account; `uncompile` discards a class's rows. Neither has an undo. The backup is your responsibility, taken *before* the command.
- **Treat access to these tools as access to destruction.** Anyone who can reach a shell with `DATABASE_URL` (and, for `resetdata`, a break-glass password) can run them. That is the same boundary as Chapter 2's — and it is why break-glass passwords are held by few, rotated, and monitored (Chapter 5, §5.8).

---

## Chapter 7 — Metadata & catalog admin

Chapter 6 spent a lot of care describing what `resetdata` *preserves*. This chapter is about that preserved thing: the **metadata catalog** — the definitions that make VoltEdge *this* utility's GIS rather than a generic one. Feature classes and their fields, the coded domains that constrain those fields, the materials catalog the engineering studies draw from, the symbology that decides how the map looks, the rules that govern connectivity and diagrams, and the CIM mappings that shape exports. All of it is authored, day to day, by an administrator like Ada through one API surface and its matching admin screens.

This is the largest single admin surface in the system — [handler.go](api/internal/admin/handler.go) mounts on the order of fifty endpoints — so this chapter does not walk every one. It teaches the **one doctrine** that governs the whole surface (why some edits are safe and some are forbidden), then tours the object types through that lens, with the endpoints named so the appendix (Chapter 9) can index them.

### 7.1 The one rule: additive, then compile ▶

Everything in this chapter obeys a single principle, and if you hold it, the rest follows. The metadata admin API is **additive**. You can *add* a subtype to a class, *add* a field, *add* a value to a domain, *add* a rule — and each addition is real and safe even on a class that is already compiled and holding thousands of rows. What you (mostly) cannot do is *destructively rewrite*: you cannot rename a field, change its data type, or edit/remove a subtype, because those would silently invalidate data already sitting in the physical table.

The package's own header comment states the doctrine precisely ([handler.go:1](api/internal/admin/handler.go#L1)):

> Adding a subtype to an already-compiled class, a value to an already-referenced coded domain, or a new field to an already-existing class, is real and safe. Editing or deleting a subtype is still fully unsupported (no endpoint at all)… What's still genuinely unsupported: renaming or retyping a field, and editing/removing a subtype.

The reason is the compiler's shape, which Chapter 2 (§2.5) and Chapter 6 (§6.8) established: the compiler is **additive-only DDL**. It `ADD COLUMN`s a new field; it never `ALTER`s or drops one. So the admin API only exposes edits the compiler can actually carry out. This produces the **two-step rhythm** that defines catalog work:

1. **Change the metadata** (add the field / subtype / domain value). This is instant, transactional, and touches only `metadata.*` — the physical `gis.<code>` table does not change yet. The new field exists in the *definition* but not in the *table*.
2. **Recompile the class** (`POST /admin/feature-classes/{code}/compile`) to materialise the change — `ADD COLUMN` the new field, widen the domain `CHECK`, and so on.

Between the two steps the addition is authored but not enforced. This is not a bug; it is the seam that lets an administrator stage several changes and compile once. The mental model to carry through the whole chapter: **metadata is the blueprint, the compiled table is the building; the admin API edits the blueprint, `compile` renovates the building.** And when an edit is *not* additive — a required field onto a populated class, a rename — the recovery is the `uncompile` → edit → `compile` cycle from Chapter 6, which discards the class's rows to rebuild the table fresh.

**Who may do this.** The entire `/admin` metadata surface sits behind one gate ([main.go:306](api/cmd/server/main.go#L306)):

```go
r.With(auth.RequirePermission(auth.PermMetadataRead, auth.PermMetadataWrite)).Mount("/admin", admin.Routes(db, credCipher))
```

`GET`s need `metadata:read`, every mutating verb needs `metadata:write` — the method-aware split from Chapter 3. Per the role table (Chapter 3, §3.2), **editor stops short of this**: `metadata:write` belongs only to `admin` and `break-glass-admin`. This is Ada's domain, not Dana's. Nothing here is break-glass-gated — catalog authoring is ordinary (if powerful) administration.

### 7.2 The objects, mapped

The catalog is a small set of object types that reference each other. Before the detail, the map:

| Object | What it is | Key endpoints (under `/admin`) |
| --- | --- | --- |
| **Feature class** | the definition of an asset type (`MV_CONDUCTOR`, `DIST_XFMR`) — geometry, network role, status | `GET/POST /feature-classes`, `PATCH/DELETE /feature-classes/{code}`, `.../compile`, `.../uncompile` |
| **Subtype** | a variant of a class (e.g. overhead vs. underground conductor) | `POST /feature-classes/{code}/subtypes` |
| **Field** | one attribute column on a class | `POST /feature-classes/{code}/fields`, `PATCH .../fields/{name}` |
| **Domain** | a coded-value list or numeric range that constrains a field | `GET/POST /domains`, `PATCH/DELETE /domains/{code}`, `.../values` |
| **Field group** | a section heading that organises fields on the attribute panel | `GET/POST /field-groups`, `DELETE /field-groups/{code}` |
| **Material spec** | an engineering data sheet (`ACSR_50`: resistance, ampacity…) a coded value can point at | `GET/POST /material-specs`, `PATCH/DELETE /material-specs/{code}` |
| **Symbology** | icons, diagram stencils, and per-class styling rules | `/symbology-icons`, `/symbology-stencils`, `/feature-classes/{code}/symbology-rules` |
| **Network / relationship / snapping / placement rules** | the graph and editing constraints | `/network-rules`, `/relationship-classes`, `/snapping-rules`, `/diagram-placement-rules` |
| **CIM mapping** | how classes/fields translate to the CIM export | `/cim-profiles`, `/cim-class-mappings`, `/cim-attribute-mappings`, `/cim-coverage`, `/cim-preview/{code}` |
| **Import profile** | ETL source→class mapping (Chapter 8's subject) | `/import-profiles`, `/import-field-mappings` |

Every one of these tables is on the **preserved** side of the `resetdata` contract (Chapter 6, §6.2). That is the payoff of keeping the catalog and the data in separate schemas: a factory reset can empty every asset without touching a single line of this modelling work.

### 7.3 Authoring a new feature class, end to end ▶∑

The heaviest single operation is creating a class from nothing. `POST /admin/feature-classes` does it **end to end in one transaction** ([create.go:104](api/internal/admin/create.go#L104)): the class row, its subtypes, its fields (with domain and group links resolved by code), its terminals, and one default symbology rule — all or nothing.

**▶ Ada adds a new asset type.** The network team has started modelling pole-mounted reclosers and wants a first-class `MV_RECLOSER`. Ada opens *Admin → Feature classes → New*, and describes the class: a **point** geometry, network role `device`, an initial subtype set (e.g. *electronic* vs. *hydraulic*), and a handful of fields — `rated_current` (integer), `control_type` (coded, pointing at a `RECLOSER_CONTROL` domain), `install_date`. She saves.

**∑ What the create does.** ([create.go:104](api/internal/admin/create.go#L104))

- It **validates** the request first (`validateCreateFeatureClass`), then checks the code is unique inside the transaction — returning a friendly `400 "a feature class with code … already exists"` rather than letting a raw Postgres unique-violation bubble up as a 500.
- It resolves the field's `domain_code` / `group_code` references to ids up front, so a typo'd domain code fails cleanly before anything is written.
- It inserts the `FeatureClass` with **`Status: "draft"` and no `physical_table`** — the same un-compiled state Chapter 6's `uncompile` resets a class *to*. A newly authored class has a definition but no table yet.
- It inserts each subtype and each field (carrying `nullable`, the `visible_on_create` / `visible_on_update` / `editable_after_publish` form-visibility flags, and per-subtype required-field links).

At this point `MV_RECLOSER` **exists in metadata but has no `gis.mv_recloser` table**. Ada's last step is to compile it — *Admin → Feature classes → MV_RECLOSER → Compile*, i.e. `POST /admin/feature-classes/MV_RECLOSER/compile`, which wraps `compiler.Compile` directly and materialises the physical table, its spatial index, and its Martin tile function. Only now can a feature of the class be placed on the map. This is the same `configure → compile → use` sequence as the whole-system bootstrap in Chapter 2, applied to a single class.

### 7.4 Evolving a compiled class ∑

Once `MV_RECLOSER` is live and holds rows, changes obey the additive doctrine. The three real, safe evolutions:

- **Add a subtype** — `POST /feature-classes/{code}/subtypes`. Real on any status; **recompile to actually enforce it** (the subtype `CHECK` widens on compile).
- **Add a field** — `POST /feature-classes/{code}/fields`. Real on any status; **recompile to actually add the column**. If the new field is *required* (`NOT NULL`) and the class already has rows, this is the one case the additive compiler cannot do — that is the `uncompile`/`compile` path from Chapter 6, §6.8.
- **Edit a field in place** — `PATCH /feature-classes/{code}/fields/{name}`. This is the subtle one, and worth stating exactly what is and isn't editable.

**∑ Which field edits are legal.** `updateField` ([update.go:347](api/internal/admin/update.go#L347)) accepts changes only to properties that are **read live from `metadata.fields`** by whatever consumes them (the CRUD engine, a following recompile) and are *never baked into the physical column* at `ADD COLUMN` time: `domain_code`, `group_code`, `nullable`, the three form-visibility flags, and `required_for_subtypes`. Because those are consulted at runtime rather than frozen into DDL, editing them in place is real and safe — and `required_for_subtypes` in particular **takes effect live, no recompile needed** (the CRUD engine reads it on the next write). What is *absent from `updateFieldRequest` entirely* is `name` and `data_type`: **you cannot rename or retype a field.** Those *are* the physical column; changing them would orphan the data. The endpoint doesn't offer them at all — the impossibility is expressed as a missing capability, not a runtime error. If you truly must rename or retype, the class-level recovery is `uncompile` → re-author the field → `compile`.

The header comment even documents the one benign inconsistency: if you flip a field to `nullable` while the physical column still enforces `NOT NULL`, the two disagree until a recompile — and it **fails safe**: a genuinely-null write passes the Go-side check but Postgres rejects it, yielding a raw `500` rather than silent data loss. Never silent, again.

**What has no endpoint at all:** editing or deleting a subtype. That is deliberate — a subtype is woven into per-row data and per-field required-sets, and no safe in-place rewrite exists. The absence of the endpoint *is* the safety guarantee.

### 7.5 Domains and coded values ∑

A **domain** is a reusable constraint — a coded list (`OH_CONDUCTOR_MATERIAL = {ACSR, AAAC, COPPER}`) or a numeric range — that any field on any class can point at. Domains are where a lot of day-to-day catalog work happens, because the real world keeps adding coded values.

- **Add a value** — `POST /domains/{code}/values`. Safe even on a domain already referenced by compiled classes; **recompile any class using it to actually enforce the widened `CHECK`.** Until then the value is authored but the physical `CHECK` still rejects it.
- **Edit a value's display** — `PATCH /domains/{code}/values/{value_code}`. Editing `label` / `sort_order` (and the `material_spec_code` link) is **always safe with no recompile** — these are pure display/link metadata, never part of the `CHECK` constraint.
- **Delete a value** — `DELETE /domains/{code}/values/{value_code}`. Safe to author the same way adding was; **recompile any class using it to actually narrow the `CHECK`.** The header comment flags the honest edge: a later recompile that would narrow the `CHECK` under existing rows using the deleted value **fails loudly** (the recompile errors) rather than silently deleting real data out from under itself. That loud failure is the feature — it forces you to deal with the affected rows before the constraint tightens.

Domains themselves: `GET/POST /domains`, `PATCH /domains/{code}` (name, or a range domain's min/max), `DELETE /domains/{code}` — the delete **guarded**, refused if any field still references the domain. The guard-on-delete pattern (refuse if referenced) recurs across the catalog: field groups, material specs, and symbology icons all refuse deletion while something points at them, so you can never orphan a reference by deleting its target.

### 7.6 Field groups and material specs ∑

Two smaller catalogs that shape, respectively, how attributes are *presented* and what engineering numbers they *carry*.

- **Field groups** (`/field-groups`) are the section headings on the feature attribute panel — "Electrical", "Construction", "Inspection". A field's `group_code` (editable live via `updateField`) files it under one. Purely organisational; deleting a group is guarded against fields that still reference it.
- **Material specs** (`/material-specs`) are the engineering data sheets — `ACSR_50` with its resistance-per-km, ampacity, reactance. A **coded domain value** can carry a `material_spec_code`, so selecting "ACSR" on a conductor pulls in a whole sheet of numbers. This is the bridge between the *catalog* (Chapter 7) and the *engineering studies* (the second deliverable, `engineering-studies.md`): the power-flow and fault engines read these specs to turn a coded material into ohms. `PATCH /material-specs/{code}` edits a spec's own numbers in place (safe — the numbers are read live by the calc engines, not compiled into anything); `DELETE` is guarded against domain values that reference it.

The chain worth internalising: **field → domain → coded value → material spec → engineering number.** Editing a material spec's ampacity silently improves every future study on every conductor that resolves to it, with no recompile — because nothing about a material spec is ever baked into a physical column. That is the same "read live" property that makes field-flag edits safe, applied to the numeric catalog.

### 7.7 Symbology: how the map and diagrams look ∑

Symbology is the catalog of *appearance*, in three parts:

- **The icon library** (`/symbology-icons`) — named `icon_ref → svg` rows. `POST` adds an icon (name + SVG markup), `PATCH` replaces one's markup in place, `DELETE` is guarded against any symbology rule that references it. This is the raw vocabulary of symbols.
- **Diagram stencils** (`/symbology-stencils`) — each stencil's `promote_class` mapping: the feature class a stamped schematic symbol *promotes into* when a diagram symbol is turned into a real network feature. `PATCH /symbology-stencils/{icon_ref}` sets or clears that mapping.
- **Per-class symbology rules** (`/feature-classes/{code}/symbology-rules`) — the actual styling logic: a rule *targets* (by attribute/subtype/zoom) and *styles* (icon, colour, width). `GET`/`POST`/`PATCH` (replace wholesale)/`DELETE`. Note the delete carries **no guard** — because the compiler *re-validates* that a class still has a usable default symbology rule at **publish** time, so a mistaken deletion surfaces at publish rather than being silently permitted. (The zoom fields on a rule are the "apply rule from/to zoom" band — distinct from a class's display-zoom min/max; the guide's earlier chapters and the handbook cover the several zoom levers.)

Every new class gets **one default symbology rule** created automatically by `createFeatureClass` (§7.3), so a freshly compiled class is never invisible — it draws with a sane default until Ada refines it.

### 7.8 The rules that govern behaviour ∑

Four smaller rule catalogs, each a thin `POST` (add one) / `DELETE` (remove one) surface, that constrain how features connect and how the editor behaves:

- **Network rules** (`/network-rules`) — the connectivity grammar: which class may connect to which (optionally terminal-to-terminal), the phase rule, and whether the pairing is valid. This is what the trace and power-flow engines walk. Adding a rule is how you teach the system that, say, an `MV_RECLOSER` terminal may join an `MV_CONDUCTOR`.
- **Relationship classes** (`/relationship-classes`) — non-electrical associations (origin/destination class + kind + cardinality), e.g. "this pole *supports* that transformer". These drive the relationship dropdown the handbook describes.
- **Snapping rules** (`/snapping-rules`) — editor snap targets (from/to class, tolerance, priority, required flag). Display-oriented until the snap UI consumes them.
- **Diagram placement rules** (`/diagram-placement-rules`) — the allow-list of which feature class may be placed on which (container × diagram type) pairing. Emptying a pairing reverts it to unconstrained.

These are the least "recompile-y" of the catalog: they are consumed live by the trace/editor/diagram engines, so adding or removing a rule takes effect on the next operation, no compile step.

### 7.9 CIM export mapping ∑

The CIM (Common Information Model) mapping catalog decides how VoltEdge's classes and fields translate into a standards-compliant CIM/XML export — the interchange format utilities exchange with planning tools. It is three mapping tables plus two read-only diagnostics:

- **Profiles** (`/cim-profiles`) — a named export target (all columns `NOT NULL`, set-only edits).
- **Class mappings** (`/cim-class-mappings`) — feature class (optionally one subtype) → CIM class + kind.
- **Attribute mappings** (`/cim-attribute-mappings`) — feature field → CIM attribute, with a transform, scale factor, unit, and value-map (so `rated_mva` can be scaled by `1e6`, a coded value remapped, etc.).
- **Coverage** (`GET /cim-coverage`) — a diagnostic anti-join: the feature classes with **no** CIM class mapping, which are therefore *silently dropped* from an export. This backs the amber "unmapped classes" warning card, so a gap in the mapping is surfaced before an export quietly omits assets.
- **Preview** (`GET /cim-preview/{code}`) — takes **one real feature** of a class and runs it through the actual CIM encoder, returning the **byte-exact** CIM/XML that an export would produce. This is the "show me what this class actually becomes" tool: Ada maps a class, previews a real feature, and sees the exact XML before trusting a full export.

The coverage/preview pair is the important administrative pattern here: the mapping is only trustworthy if you can *see* both what's missing (coverage) and what's produced (preview) without running a full export. Chapter 8 covers exports themselves.

### 7.10 The admin landing page ▶

All of the above is reached through one screen. The sidebar has a single **Admin** entry that opens `/admin`, a landing page of grouped cards — *Metadata & schema*, *Symbology & map*, *Access & operations* — driven by a single shared source, [admin-nav.tsx](web/src/components/layout/admin-nav.tsx). Two properties make it trustworthy:

- **The UI mirrors the API gate, it doesn't replace it.** Each nav item carries the `perm` the API enforces, and both the sidebar and the landing page **hide** any item (and any group with no reachable item) the caller can't use — via `filterAdminNav`. So an `editor` who wanders to `/admin` sees only what their permissions allow, and a `viewer` sees an empty or absent admin area. But this is *cosmetic*: the real gate is `RequirePermission` on the server (§7.1). Hiding a dead-end link is a courtesy; the 403 behind it is the guarantee.
- **One source of truth.** Because the sidebar and the landing page both read `adminNavGroups`, they can't drift. Adding a new admin destination is a one-line edit to `admin-nav.tsx`.

For Ada this means the catalog is navigable as a coherent whole rather than fifteen stacked menu items; for the system it means the visible admin surface always matches the caller's real permissions.

### 7.11 Discipline for catalog work

Three habits close the chapter:

- **Remember the seam: add, then compile.** An added field/subtype/domain-value is authored the instant you save it, but not *enforced* until you recompile the affected class. If a new field isn't showing up on features, you almost certainly haven't recompiled. (Live-consumed edits — field flags, `required_for_subtypes`, material-spec numbers, rules — are the exceptions that need no recompile.)
- **Let the guards and the loud failures work for you.** Deletes are guarded against references; a narrowing recompile fails loudly rather than dropping data; a mistaken symbology-rule delete surfaces at publish. Don't fight these — they are the catalog telling you there is data to deal with first.
- **When additive isn't enough, reach for `uncompile` deliberately.** Renaming or retyping a field, or removing a subtype, means the `uncompile` → re-author → `compile` cycle from Chapter 6 — which discards the class's rows. Export first. That is the boundary between everyday catalog admin (this chapter) and the destructive tools (Chapter 6).

---

## Chapter 8 — Data lifecycle admin

Chapter 7 was about the catalog — the definitions. This chapter is about the **data that flows through those definitions**: how features get *in* (the ETL import engine), how they get *out* (exports), how in-progress and private work is *hidden or shown* to the right people (the private-drafts and pending-edit visibility model), and how the security-relevant events are *recorded* (the `iam.auth_events` audit trail). These are the four operational-data subsystems an administrator supervises once the catalog is built and the system is live.

A theme from Chapter 7 carries straight in and is worth restating, because it draws the line between the two chapters: **authoring is configuration; running is operation.** Authoring an import *profile* (the mapping from a source file to a feature class) is `metadata:write` work under `/admin` (Chapter 7, §7.9's sibling). *Running* an import against that profile — staging a file, validating it, enqueuing the load — is `features:write` work under `/imports`. The same split governs the whole chapter: an admin sets the machinery up in the catalog, and an operator (often the same person, wearing a different permission) drives data through it.

### 8.1 The four subsystems, and how data moves

Before the detail, the shape of the data lifecycle:

| Subsystem | Direction | Gate | Executed by |
| --- | --- | --- | --- |
| **ETL import** | data **in** (bulk) | `features:read`/`features:write` (`/imports`) | `cmd/importworker` |
| **Export** | data **out** | `features:read` (`/export`) | `cmd/exportworker` |
| **Private drafts / pending edits** | visibility *of* data | rollout switch + per-request | the read surfaces themselves |
| **Audit trail** | record *of* access | (write-only, app-internal) | every auth path |

The first two are **queue-and-worker** designs: the API endpoint stages the work and enqueues a job row; a standalone long-running binary claims and executes it. This is the same fleet model as Chapter 2 — the API process stays responsive while minutes-long imports and exports run in a separate worker you deploy alongside it. The third is a *read-time* concern woven through every feature-read surface. The fourth is a write-only ledger.

### 8.2 The ETL import engine ▶∑

Bulk data entry is the import engine: turn a CSV / GeoJSON / Shapefile / KML / DXF — or a whole GeoPackage / SpatiaLite database — into features of a class, matched-and-upserted against what's already there.

**▶ Dana imports a batch of new poles.** The survey team has delivered 900 new poles as a shapefile. The `POLE` class and an import *profile* (source→`POLE`, match on `asset_tag`, upsert) already exist — Ada authored the profile in the catalog (Chapter 7). Dana opens the import wizard, picks the profile, uploads the file, previews a dry run, and — once the sample validates — enqueues the load. Minutes later the history panel shows *900 rows, 897 inserted, 3 updated, 0 failed*.

**∑ The wizard, endpoint by endpoint.** The operational surface is `/imports` ([http.go:42](api/internal/importer/http.go#L42)), and its steps map one-to-one onto the wizard:

- `GET /imports/profiles` — the **active** profiles this operator may run. Deliberately gated on `features:write`, *not* `metadata:read`, so running an import never requires the admin authoring permission — an operator picks from profiles an admin has published.
- `POST /imports/uploads` — mints an **object key + presigned PUT URL** for the source artifact. The key is **server-minted, never client-controlled**, so one operator's upload cannot address another's key. The `artifact` kind picks the key's extension (`ndjson` default, or `geopackage`/`spatialite` for raw SQLite-family files) — cosmetic; what actually parses the object is the profile's `source_format`.
- `POST /imports/layers` — for a staged GeoPackage, lists its feature layers (server-side parse) so the operator can pick which layer to load.
- `POST /imports/dry-run` — validates a **sample** of rows against the profile, **writing nothing**. This is the pre-flight: it runs the same `ValidateFeature` the CRUD engine uses, so what passes the dry run will pass the load.
- `POST /imports/jobs` — enqueues the load run over the uploaded artifact. Returns immediately with a job id.
- `GET /imports/jobs` / `GET /imports/jobs/{id}` — the caller's run history and one run's **status + tallies + per-row error report**, the last capped at `maxJobErrorReport = 500` so a pathological all-rows-failed import doesn't ship a million-line report.

**Two ingest shapes.** The v1 file formats (CSV/GeoJSON/Shapefile/KML/DXF) are **normalised in the browser to NDJSON** before upload — the server ingests a uniform stream. GeoPackage and SpatiaLite are different: the browser uploads the **raw SQLite file verbatim**, and the *server* parses it (`geopackage.go` / `spatialite.go`), because a full spatial database is not something to normalise client-side. Either way, the enqueued job is claimed and executed by the standalone **`cmd/importworker`**, which streams the staged artifact and applies every row via the profile's match-on-field upsert policy — reusing the same validation and write path as an interactive edit, so an imported feature is indistinguishable from a hand-placed one.

The key administrative point: **an import is a bulk CRUD write, not a back door.** It runs through `ValidateFeature`, respects the class's domains and required fields (Chapter 7), and is attributed to the operator. A malformed row fails validation and lands in the error report; it does not slip past the catalog's constraints.

### 8.3 Exports ▶∑

The mirror image is `/export` — read features and stream them back as a downloadable KMZ, Shapefile, DXF, or CIM/XML.

**∑ Why it's a read, even as a `POST`.** The mount is telling ([main.go:171](api/cmd/server/main.go#L171)):

```go
r.With(auth.RequirePermission(auth.PermFeaturesRead, auth.PermFeaturesRead)).Mount("/export", export.Routes(db, exportStore))
```

Both the read-perm *and* the write-perm slot are `features:read` — so **every method, including the `POST` that starts an export, needs only `features:read`.** It is `POST` purely because the filter and scope (which classes, clipped to which viewport) don't fit in query params, not because it mutates anything. An export is a read, like trace or analysis. It also respects the same read scoping as every other read: territory scope (Chapter 4) and private-drafts visibility (§8.4) both apply, so an export can never leak features the operator couldn't see on the map.

**∑ The worker, the queue, and retention.** A large export is queued to `ops.export_jobs` and executed by **`cmd/exportworker`** ([worker.go:1](api/internal/export/worker.go#L1)):

- It claims jobs with `FOR UPDATE SKIP LOCKED`, runs up to `Concurrency` at once (default 2, `EXPORT_WORKER_CONCURRENCY`), and **heartbeats each job's lease** (default 60 s, `EXPORT_JOB_LEASE`) at `lease/3` so a genuinely long export is never mistaken for a dead worker and reclaimed underneath itself. A crashed worker's jobs become reclaimable one lease after its last heartbeat.
- A periodic **retention sweep** ([retention.go:1](api/internal/export/retention.go#L1)) reaps finished exports' artifacts *and* their job rows once they're older than `EXPORT_RETENTION` (default **7 days**; set `0` to keep everything), every `EXPORT_SWEEP_INTERVAL` (default 1 h), in batches of 500. Without it, every download would leave an object in RustFS forever and the bucket would grow without bound. The sweep also reaps the rare orphan — an object whose upload succeeded but whose `completeExport` write didn't.

The administrative takeaways: exports are **ephemeral by design** (a downloaded export is gone in a week unless you re-run it — this is not a document store), and the export worker is a **deploy-alongside** binary with its own `EXPORT_*` tuning, exactly like the import worker. The CIM/XML format is where Chapter 7's CIM mappings (§7.9) are finally consumed — the coverage and preview tools there exist precisely so an admin can trust what a CIM export will contain before running one.

### 8.4 Private drafts and pending edits ▶∑

The subtlest data-lifecycle concern is not moving data but **deciding who can see which version of it**. VoltEdge has a two-phase model for private, in-progress work, and an administrator needs to understand it well enough to answer the two questions it raises: *why can't this user see that feature?* and *should I turn this on?*

**The model, in two phases** ([drafts.go:1](api/internal/drafts/drafts.go#L1), [pending.go:1](api/internal/drafts/pending.go#L1)):

- **Phase A — never-published drafts.** A feature that has *never* been public is private to its **author and the author's management chain**: a viewer sees it only when its `created_by` is the viewer or one of the viewer's subordinates. "Never been public" is `status = 'draft' AND published_at IS NULL`; the public test is `status = 'published' OR published_at IS NOT NULL` (belt-and-suspenders — a row created already-published counts as public even if `published_at` was never stamped). This is a **filter**: a `WHERE` predicate that hides rows.
- **Phase B — pending edits over published features.** A scoped user's private *edit or delete of an already-published feature* is different in kind. The base `gis.<code>` row **stays public and untouched**; the author sees a *different version* of it, their pending change overlaid. It is an **actor-dependent value substitution, not a filter** — the overlay is display-only (v1): it shows in the author's own reads (GET / list / query / their tiles) but does **not** participate in `WHERE`/`ORDER BY`. It's stored in one generic table, `runtime.feature_pending_edits`, keyed uniquely by `(feature_class_code, feature_id, author_id)`, carrying a `patch` envelope, an optional changed geometry, an `is_delete` flag, and a `base_version` conflict token — the apply path blocks if someone else published a change to the base row first.

A crucial scoping difference between the phases: Phase A visibility includes **subordinates** (a manager can see a report's orphaned draft — recovery matters), whereas Phase B overlays the **viewer's own edits only** ([pending.go:72](api/internal/drafts/pending.go#L72)) — a pending edit is a private workspace over a row a manager already sees fine as its public version, so there's no recovery need, and viewer-own-only sidesteps the "two authors edited the same feature, whose overlay wins?" problem entirely.

**▶ What the administrator actually controls.** Both phases compose with — never replace — the Chapter 4 territory predicate: a private draft is visible only to an authorised author **AND** within the viewer's territory scope, all AND-ed into one read. And both are gated by a single **rollout switch**, `private_drafts_enabled` in `iam.system_settings` ([drafts.go:40](api/internal/drafts/drafts.go#L40)), which behaves exactly like the territory-enforcement flag from Chapter 4:

- **Off by default.** Missing or unparseable = off; only an explicit `"true"` turns it on. An install that hasn't enabled it reads exactly as before.
- **Retroactive when flipped.** Turning it on **privatises every never-published feature to its author chain** at once — a deliberate admin action (mirroring `territory.SetEnforcement`), not a deploy-time surprise. The switch lives at `/private-drafts`, gated `features:read` to view / `metadata:write` to flip ([main.go:320](api/cmd/server/main.go#L320)) — the admin gate.

**The break-glass thread, closed.** Here is where Chapters 4 and 5 pay off. `OverlayAuthor` and the Phase A visibility set both **short-circuit for `Unscoped` or `BreakGlass` users** ([pending.go:84](api/internal/drafts/pending.go#L84)): an admin (`territories:unscoped`) or the break-glass account reads and writes the **public base data directly, never through a private overlay**. This is the same "the omnipotent account falls out of the access model" property from Chapter 5, §5.1 — and it means an admin diagnosing "why can't Priya see feature X?" sees the *true, unfiltered* state themselves, which is exactly what you want from the person debugging a visibility complaint. The answer to that complaint is always one of: it's a never-published draft not in her chain, it's a pending edit that's someone else's private workspace, or it's outside her territory — and the admin can see all three because they're outside the filter.

### 8.5 The audit trail ∑

Every security-relevant authentication event lands in **`iam.auth_events`**, the append-only audit log ([events.go:8](api/internal/auth/events.go#L8), [models.go:133](api/internal/auth/models.go#L133)). Two design properties matter to an administrator:

- **It's best-effort and never blocks the audited action.** `logEvent` inserts one row and *ignores the write's effect on control flow* — "an audit-write failure must never change the outcome of the action being audited." So a login succeeds even if the audit insert fails. This is the right trade-off (you never want the audit log to be able to deny a legitimate login), but it means the log is a *very good* record, not a *cryptographically guaranteed* one.
- **The event vocabulary is a fixed constant set**, kept in one place so the log stays queryable rather than accreting free-form strings:

| Event | Meaning |
| --- | --- |
| `login_success` | a normal successful sign-in |
| `login_failure` | wrong credentials (the generic-failure path from Chapter 3) |
| `login_denied` | **correct** password, but local auth is off and the account is not break-glass |
| `break_glass_login` | a break-glass account signed in (Power 1, Chapter 5) — the loudest line in the log |
| `logout` | an explicit sign-out |
| `local_auth_toggled` | the local-auth switch was flipped (Power 2, Chapter 5) |
| `sso_linked` | a first SSO login linked a provider identity to a pre-provisioned user |
| `role_changed` | a role was granted to or revoked from a user |

Each row carries `user_id` (nullable — a login attempt for an unknown email has no subject), `event_type`, a free-text `detail`, the client `ip` and `user_agent`, and `created_at`.

**How an admin reads it — and the catch.** There is **no read/list endpoint** for `auth_events` anywhere in the API; the application only ever *writes* it. Reviewing the audit trail is therefore an **out-of-band SQL query** against `DATABASE_URL` — the same operator-with-shell-access boundary as the rest of this guide's out-of-band tools. An admin investigating, say, a suspected credential-stuffing run queries directly:

```sql
SELECT created_at, event_type, ip, detail
FROM iam.auth_events
WHERE event_type IN ('login_failure','login_denied')
  AND created_at > now() - interval '1 hour'
ORDER BY created_at DESC;
```

and the `break_glass_login` rows are the ones to alert on and reconcile against a known incident (Chapter 5, §5.7–5.8). **The catch, tying back to Chapter 6:** `resetdata` **truncates `iam.auth_events`** (§6.2). A factory reset erases the audit history. If that history has compliance or forensic value across a reset, **export it first** — there is no other copy.

### 8.6 One more inbound path: the field-sync review queue

For completeness, bulk import is not the only way data arrives. The Flutter field app syncs offline edits into `ops.sync_inbox`, and when a class is configured for review, those edits wait in a **review queue** an admin works before they become system-of-record. That surface is `/sync-review` ([main.go:357](api/cmd/server/main.go#L357)), gated `sync:review` to view the queue and policy / `sync:post` to post, reject, or set a class's policy — a distinct permission pair so reviewing and posting can be separated. It's the QA face of field data; the field app and its sync model are the user-handbook's and field-app's territory, so this chapter only flags it as the third way (after interactive edits and ETL import) that features enter the system.

### 8.7 Discipline for the data lifecycle

- **Deploy the workers, and tune their env.** Imports and exports are inert without `cmd/importworker` and `cmd/exportworker` running alongside the API. Set `EXPORT_RETENTION` deliberately — the default reaps downloads after a week, which surprises anyone treating exports as durable storage.
- **Dry-run every import; read the error report.** The dry run writes nothing and runs the real validation. If a load reports failures, the capped per-row report (≤500) tells you which rows and why — fix the source, re-stage, re-run.
- **Treat the rollout switches as retroactive, one-way-feeling events.** Flipping `private_drafts_enabled` (or Chapter 4's territory enforcement) instantly changes what everyone sees. Announce it, do it deliberately, and remember that admins/break-glass read *past* both filters — so test visibility as a scoped user, not as yourself.
- **Export the audit trail before a factory reset.** `resetdata` erases `iam.auth_events`. It is the one piece of history a reset destroys that you cannot reconstruct.

---

## Chapter 9 — Appendix: reference & runbooks

The preceding eight chapters explained the system; this one is for looking things up while you operate it. It gathers the flat reference material — every permission, every environment variable, the admin and out-of-band surfaces, and a set of break-glass runbook checklists — that the narrative chapters referenced but did not tabulate. Nothing here is new; it is the earlier chapters distilled into tables you can scan under pressure. Where a value is grounded in a specific source file, the citation is kept so you can confirm it hasn't drifted.

### 9.1 Permission reference

Every permission is a string constant in [rbac.go:21](api/internal/auth/rbac.go#L21) (plus `territories:unscoped` in the `access` package). A permission is held only via a **role** — there are no per-user permissions (Chapter 3). The "lowest role" column reflects the base RBAC seed, migration [0025_create_rbac.sql](migrations/0025_create_rbac.sql); domain permissions added in later migrations (OMS/WAM/faults, asset-tags, territories, circuits, attachments, sync, annotations, diagram-delete, field-packs) are seeded to their relevant roles and **always** to `admin`/`break-glass-admin`.

| Permission | Grants | Lowest role (base seed) |
| --- | --- | --- |
| `features:read` | View features: map layers, attributes, class schema | viewer |
| `connectivity:read` | View committed electrical connectivity | viewer |
| `relationships:read` | View committed structural/spatial relationships | viewer |
| `diagrams:read` | View schematic diagrams and placements | viewer |
| `metadata:read` | View the metadata catalog | viewer |
| `annotations:read` | View map annotations | viewer¹ |
| `trace:run` | Run network traces (isolation/feeder/section) | analyst |
| `analysis:run` | Run power-flow, fault-current, ampacity analysis | analyst |
| `features:write` | Create/edit/publish/unpublish/delete/restore features | editor |
| `connectivity:commit` | Establish or revoke electrical connectivity | editor |
| `relationships:commit` | Establish or revoke structural/spatial relationships | editor |
| `diagrams:write` | Create/rename diagrams, place/remove features (deleting a diagram needs `diagrams:delete`) | editor |
| `diagrams:delete` | Delete diagrams¹ | supervisor¹ (not editor) |
| `annotations:write` | Create/edit map annotations¹ | editor¹ |
| `attachments:read` / `attachments:write` | View / manage feature attachments¹ | viewer¹ (read) · editor¹ (write) |
| `oms:read` / `oms:write` | View / manage outages & switching¹ | viewer¹ (read) · editor¹ (write) |
| `wam:read` / `wam:write` | View / manage defects, works, inspections¹ | viewer¹ (read) · editor¹ (write) |
| `faults:read` / `faults:write` | View / manage fault events¹ | viewer¹ (read) · editor¹ (write) |
| `tags:read` / `tags:write` | Resolve/scan asset tags & view a feature's tags / register-void-reissue-set-primary¹ | viewer¹ (read, +field-technician) · editor¹ (write) |
| `metadata:write` | **Edit and compile the metadata catalog** | **admin** |
| `territories:write` | Create/edit the territory tree, boundaries, backfill and the enforcement flip (granting a territory to a user is `admin:users`) | **admin** |
| `circuits:write` | Assign/edit circuits¹ | **admin** |
| `sync:review` / `sync:post` | Review / post the field-sync inbox | **admin** |
| `admin:field-packs` | Manage field-tool region packs | **admin** |
| `admin:auth-settings` | Change auth settings incl. the local-auth switch | **admin** |
| `admin:users` | Manage accounts & role assignments | **admin** |
| `territories:unscoped` | **Bypass** spatial scope entirely (see all territories) | **admin** |

¹ Seeded/extended in a migration after 0025; the base-seed boundary shown is the intended tier (data-writes → editor; catalog/admin/scope → admin). Confirm the exact grants for a given deployment against its applied migrations.

**The two boundaries that matter most:**
- **editor stops before `metadata:write` and every `admin:*`.** An editor edits *data*; they never touch the *schema*, *users*, or *auth settings*. That line is the difference between Dana (editor) and Ada (admin) throughout this guide.
- **`admin` and `break-glass-admin` have identical permission sets** — every permission (`CROSS JOIN` in [0025:130](migrations/0025_create_rbac.sql#L130) for the original 14; explicit grants in each later permission migration). The difference between them is **not** a permission; it is the `break_glass` *column* (Chapter 5).

### 9.2 The five baseline roles at a glance (plus `supervisor` and three field roles added by 0078/0099)

| Role | Permissions (cumulative) | Who |
| --- | --- | --- |
| **viewer** | pure reads (features/connectivity/relationships/diagrams/metadata) | Priya (dispatcher) |
| **analyst** | viewer + `trace:run` + `analysis:run` | Tom (planner) |
| **editor** | analyst + all domain writes/commits — **not** metadata/admin | Dana (field supervisor) |
| **admin** | every permission | Ada (system administrator) |
| **break-glass-admin** | every permission — *same set as admin* | Owen (emergency) — **plus `break_glass = true`** |

Roles seeded by migration 0025; **no `user_roles` are seeded** — the first account is created out-of-band with `authadmin` (§9.5). Permission is recomputed from the user's roles on **every request** ([session.go](api/internal/auth/session.go)), so a role change takes effect on the next request and always fails closed.

### 9.3 Environment-variable catalog

From [.env.example](.env.example) plus the two out-of-band password variables. All ports default into the reserved **3700–3720** range to avoid collisions.

**Core / database**
| Var | Meaning |
| --- | --- |
| `DATABASE_URL` | The one Postgres DSN every binary opens. In compose it points at the `db` service; host-run it points at `localhost:<POSTGRES_PORT>`. **The single most common bootstrap failure is a compose-vs-host mismatch here** (Chapter 2). |
| `POSTGRES_PASSWORD` / `POSTGRES_DB` / `POSTGRES_PORT` | Postgres image config (published port `3700`). |
| `API_PORT` | The Go API listener (`3704`). |
| `MARTIN_PORT` | Martin tileserver (`3701`). |

**Object store (RustFS / S3)** — shared by API, export worker, import worker, fieldsync
| Var | Meaning |
| --- | --- |
| `RUSTFS_ACCESS_KEY` / `RUSTFS_SECRET_KEY` | S3 credentials. |
| `RUSTFS_ENDPOINT` | host:port the **processes** reach RustFS at (upload). |
| `RUSTFS_PUBLIC_ENDPOINT` | host:port a **downloader** (browser/device) reaches (presign target); unset = same as `RUSTFS_ENDPOINT`. |
| `RUSTFS_BUCKET` | Artifact bucket (auto-created on boot). |
| `RUSTFS_S3_PORT` / `RUSTFS_CONSOLE_PORT` / `RUSTFS_REGION` / `RUSTFS_USE_SSL` / `RUSTFS_PUBLIC_USE_SSL` / `RUSTFS_CORS_ORIGINS` | Ports, region, TLS, and the browser-PUT CORS allow-list. |
| `EXPORT_URL_TTL` | Presigned download URL lifetime (default `15m`). |

**Export worker** (Chapter 8)
| Var | Default | Meaning |
| --- | --- | --- |
| `EXPORT_WORKER_CONCURRENCY` | `2` | Parallel exports per worker. |
| `EXPORT_JOB_LEASE` | `60s` | Job lease; heartbeats at lease/3. |
| `EXPORT_RETENTION` | `168h` (7d) | How long finished exports are kept; `0` = keep all. *(Go durations have no day unit — use `168h`.)* |
| `EXPORT_SWEEP_INTERVAL` | `1h` | Retention sweep cadence. |

**Auth / sessions** (Chapter 3)
| Var | Default | Meaning |
| --- | --- | --- |
| `SESSION_IDLE_TIMEOUT` | `12h` | Sliding idle window. |
| `SESSION_ABSOLUTE_MAX` | `720h` (30d) | Hard session ceiling. |
| `SESSION_COOKIE_NAME` / `CSRF_COOKIE_NAME` | `ve_session` / `ve_csrf` | Rarely changed. |
| `AUTH_INSECURE_COOKIES` | — | `1` drops the `Secure` flag for **local http dev only**. Never set on a networked deployment. |

**Out-of-band passwords** — read *only* by their CLIs, never the server, never left in `.env`
| Var | Read by | Purpose |
| --- | --- | --- |
| `AUTHADMIN_PASSWORD` | `authadmin` | Password for the account being created/reset (Chapter 2, §9.5). |
| `RESETDATA_PASSWORD` | `resetdata` | Break-glass admin's password authorising a factory reset (Chapter 6). |

**SSO / OIDC (optional)** — a provider is enabled only when its vars are set *and* discovery succeeds at startup
| Var group | Meaning |
| --- | --- |
| `OIDC_AZURE_TENANT_ID` / `_CLIENT_ID` / `_CLIENT_SECRET` / `_REDIRECT_URL` | Azure AD, single-tenant. |
| `OIDC_GOOGLE_CLIENT_ID` / `_CLIENT_SECRET` / `_REDIRECT_URL` / `_ALLOWED_DOMAIN` | Google; `ALLOWED_DOMAIN` restricts to a Workspace domain. |

**Other optional subsystems**
| Var | Subsystem |
| --- | --- |
| `EXTERNAL_SYSTEMS_ENCRYPTION_KEY` | AES-256 (base64 32 bytes) sealing external-system & service-layer credentials at rest; unset = credential-bearing systems can't be saved. Rotating it makes stored creds undecryptable. |
| `MCP_PORT` | MCP server (`3705`); agents present an `iam.api_tokens` bearer minted by `authadmin mint-token`. |
| `ASSISTANT_LLM_PROVIDER` / `_BASE_URL` / `_MODEL` / `_API_KEY` / `_TIMEOUT` / `_MAX_TOKENS` | In-app assistant chat; both provider+base-url unset = chat disabled (deterministic assistance still works). |
| `FIELDSYNC_PORT` / `FIELDSYNC_WORKER_CONCURRENCY` / `FIELDSYNC_JOB_LEASE` / `FIELDPACK_MIN_ZOOM` / `FIELDPACK_MAX_ZOOM` / `TIPPECANOE_BIN` | Field-tool pack builder. |

**There is no `BREAK_GLASS_*` environment variable.** Break-glass is the `break_glass` column plus the two password variables above — nothing else (Chapters 5–6).

### 9.4 Admin & operational endpoint index

Every mount and its gate, from [main.go](api/cmd/server/main.go). Gates are `RequirePermission(read, write)` with the method picking which slot applies (Chapter 3).

| Mount | Read gate | Write gate | Chapter |
| --- | --- | --- | --- |
| `/admin` (metadata catalog: classes, fields, domains, symbology, CIM, import-profiles) | `metadata:write` | `metadata:write` | 7 |
| `/map-settings`, `/basemaps`, `/service-layers` | `features:read` | `metadata:write` | 7 |
| `/private-drafts` (rollout switch) | `features:read` | `metadata:write` | 8 |
| `/external-systems` | `metadata:write` | `metadata:write` | 7 |
| `/imports` (run an import) | `features:read` | `features:write` | 8 |
| `/export` | `features:read` | `features:read` | 8 |
| `/field-packs` (admin) | `admin:field-packs` | `admin:field-packs` | 8 |
| `/sync-review` | `sync:review` | `sync:post` | 8 |
| `/auth/admin` (users, roles, territories, manager) | `admin:users` | `admin:users` (+ CSRF) | 3–4 |
| `/auth/settings/local-auth` | — | `admin:auth-settings` **or** `break_glass` | 3, 5 |
| `/territories` (`/enforcement` toggle) | `features:read` | `territories:write` | 4 |
| `/circuits` | `features:read` | `circuits:write` (assign also `features:write`) | — |

### 9.5 Out-of-band CLI reference

The one-shot binaries and their Task wrappers. All open `DATABASE_URL` directly and run independent of the API — the "last resort beneath the floor" surface.

| Command | Task wrapper | Purpose | Chapter |
| --- | --- | --- | --- |
| `authadmin create --email --name` | `task authadmin-create EMAIL= NAME=` | Bootstrap/create an account (defaults `--break-glass=true --role admin`); password from `AUTHADMIN_PASSWORD` | 2–3 |
| `authadmin set-password` / `grant-role` / `roles` / `list` / `mint-token` / `revoke-token` | `task authadmin-*` | Out-of-band account/role/token management | 3 |
| `compile <CODE>` / `--all` | `task compile CODE=` / `task compile-all` | Materialise metadata → `gis.<code>` (idempotent, additive) | 2, 7 |
| `uncompile <CODE>` | `task uncompile CODE=` | **Destructive**: drop one class's table (discards its rows), reset to draft | 6, 7 |
| `resetdata --admin-email` | `task reset-data-plan EMAIL=` | **Dry run** of the factory reset — authenticate, print the plan, change nothing | 6 |
| `resetdata --admin-email --yes` | `task reset-data EMAIL=` | **Destructive**: factory reset — wipe data, reduce to one admin, purge object store; `RESETDATA_PASSWORD` | 6 |
| `migrations/apply.sh` | `task migrate` / `task migrate-status` | Apply pending migrations in filename order; query `ops.migration_log` | 2 |
| `task seed-all` | — | 3-phase reseed: metadata seeds → `compile-all` → instance seeds | 2 |

Long-running binaries deployed alongside the API: `server`, `mcpserver`, `exportworker`, `importworker`, `fieldsync` (Chapters 2, 8).

### 9.6 Break-glass runbook checklists

Four emergencies, each with the deliberate sequence. These assume the reader is Owen (or whoever holds a `break_glass = true` local account and its password).

**Runbook A — SSO is down, no one can sign in.**
1. Confirm SSO is the cause (provider status page; `login` failing at the redirect, not the password).
2. Sign in as the break-glass account with its **local password** — this works even though local auth may be off (Power 1). Audited as `break_glass_login`.
3. `POST /auth/settings/local-auth {enabled: true}` — the column satisfies the gate (Power 2). Now the team can sign in locally.
4. Diagnose/operate on local auth until SSO recovers.
5. When SSO is healthy, set `local_auth_enabled` back to `false`. Reconcile the `break_glass_login` and `local_auth_toggled` rows against this incident.

**Runbook B — the administrators are locked out (bad config, not SSO).**
1. If the running system can't let an admin in at all, drop to the out-of-band surface: `authadmin` against `DATABASE_URL`.
2. Reset an admin's password (`authadmin set-password`) or grant the admin role (`authadmin grant-role`) directly. No server required.
3. Sign in through the now-fixed path and correct the configuration that caused the lockout.

**Runbook C — factory reset (pilot→prod, or contaminated data).**
1. **Export anything that must survive**: `iam.auth_events` (the reset erases it) and any operational data you can't re-source.
2. Run the **plan**: `resetdata --admin-email <you>` (no `--yes`). Read the full table list; confirm your *config* tables are **absent** and only data tables are listed.
3. Authorise: re-run with `--yes` (prod: `docker compose exec -e RESETDATA_PASSWORD=… api resetdata --admin-email <you> --yes`).
4. Verify the tally (tables truncated, accounts reduced, objects purged).
5. Re-provision the team via `/auth/admin` and re-grant territories. You are the sole surviving account until you do.

**Runbook D — the sole break-glass admin is lost (person left / credential gone).**
1. Using any *other* admin account, create a new break-glass account: `/auth/admin` with `break_glass = true` and a strong local password (12-char minimum), **or** `authadmin create` out-of-band.
2. If **no** working admin remains at all, use `authadmin` against `DATABASE_URL` to create or repair one — the bootstrap path never depends on an existing session.
3. Rotate: disable the lost account, and confirm at least one *other* person holds a working break-glass credential (the standing rule from Chapter 5, §5.8 — never a single point of recovery).

### 9.7 Closing

That completes the **VoltEdge Administrator & Break-Glass Guide** — from the two personas and the security model (Chapter 1), through whole-system setup (2), identity and RBAC (3), spatial access (4), the break-glass administrator (5) and its destructive operations (6), the metadata catalog (7), the data lifecycle (8), and this reference (9).

It is also the last of the four VoltEdge documents: the **User Handbook** (how to use the system), the **Engineering Studies** deep-dive (how the analyses work), and this guide's two halves — **how to run the system** and **how to recover it when the normal path fails**. Read together they cover VoltEdge from the map a dispatcher clicks to the fire-axe behind the glass.

The single thread through all of it: **the middleware is the security boundary.** There is no database-level enforcement to fall back on — every guarantee in this guide is a Go check on a request, a column on a row, or a deliberate out-of-band tool. Understanding *where* those checks are, and *why* break-glass is a narrow set of audited bypasses rather than an omnipotent mode, is the whole job of administering VoltEdge safely.
