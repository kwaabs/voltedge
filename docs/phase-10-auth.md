# Phase 10 — authentication & authorization

**Goal:** tie a real, authenticated identity and an explicit permission
to every action in VoltEdge. Today every route in `api/cmd/server/main.go`
is open and the API talks to Postgres as the `postgres` superuser over a
single shared `DATABASE_URL` -- so the Go API is the *only* trusted
gatekeeper there is, and all enforcement has to live in the Go layer
(Postgres RLS is not a lever we hold without re-architecting the DB
connection model, which this phase deliberately does not do). This phase
gives the app: local (password) login and Azure AD / Google SSO, a
single revocable session, role-based authorization checked on every
route, and a real actor stamped onto every write -- finally turning the
`applied_by` / `established_by` / `published_by` placeholder columns
(`migrations/0002`, `0006`, `0016`, `0017`) into real identities.

**Status:** all five slices are built -- Slice A (identity foundation),
Slice B (OIDC federation), Slice C (RBAC enforcement), Slice D (frontend),
and Slice E (actor threading). Verification of B against real Azure AD /
Google tenants is owed, as is the full RBAC 403/200 matrix, migration
`0026`, and a frontend Playwright pass once the DB is up. Every
architectural fork below is locked (see "Decisions locked before
building").

**Security posture:** this is the app's trust boundary, so the bar is
"hardened, only well-audited libraries, smallest sensible dependency
surface." The concrete library choices and *why each one* are in
"Hardened library choices" below; the short version is that we lean on
the Go standard library (`crypto/rand`, `crypto/subtle`,
`golang.org/x/crypto/argon2`) and two de-facto-standard, actively
maintained packages (`coreos/go-oidc`, `golang.org/x/oauth2`) rather
than a large auth framework, and we issue **opaque, server-side,
revocable sessions** rather than self-signed JWTs -- no token parsing in
the browser, no JWT algorithm-confusion surface, instant revocation.

## Decisions locked before building

These were settled in discussion before any code; recorded here so the
build doesn't re-litigate them.

- **Both local and SSO.** Local password login *and* Azure AD + Google
  SSO, all converging on one session. Authentication is pluggable at the
  edge; everything behind the session is identical regardless of how you
  got in.
- **Admin pre-provisions; SSO never self-creates.** An admin creates
  every account (email + role) first. A successful SSO login for an email
  with no matching, active VoltEdge user is **rejected**, not
  auto-provisioned. Azure AD is locked to a single configured tenant,
  Google to a configured hosted-domain / address allowlist. This is an
  internal utility tool -- access is a deliberate grant, not a
  side-effect of owning a Google account.
- **Local auth is the break-glass floor, behind a switch.** A global
  `local_auth_enabled` setting (default **off**) plus a per-user
  `break_glass` flag. See "Local auth: the break-glass floor" -- the
  truth table is the heart of this phase's operational safety.
- **RBAC as data, single org.** Permissions are a catalog, roles bundle
  permissions, both are rows -- extensible the same way every other part
  of this metadata-driven app is, without a code change per new
  distinction. One organization, one user pool; no tenant column threaded
  through the schema.
- **Opaque sessions, not JWTs.** Covered under security posture above.

## Design: many ways in, one session out

Local password, Azure AD, and Google are three different ways to *prove
identity*. They all terminate in the **same** thing: a server-side
session, referenced by one `httpOnly` cookie. Authorization never knows
or cares which path a request's user authenticated through -- it only
ever reads `session → user → roles → permissions`. That keeps all the
provider-specific messiness at the very edge (three thin login handlers)
and makes the entire rest of the system -- middleware, RBAC, audit --
uniform.

```
  local login  ─┐
  Azure AD SSO ─┼─►  resolve/verify identity  ─►  mint session  ─►  Set-Cookie: ve_session=<opaque>
  Google SSO   ─┘         (per-provider)            (uniform)

  every subsequent request:
    cookie ─► session middleware ─► user in request context ─► RBAC middleware ─► handler
```

### Sessions

- The cookie carries a **256-bit opaque token** from `crypto/rand`, not a
  JWT and not anything the browser can read into meaning. It is
  `httpOnly` (JS can never touch it -- removes the XSS token-theft
  surface entirely), `Secure`, `SameSite=Strict`, host-scoped, with a
  fixed path.
- The server stores only a **SHA-256 hash** of the token in the
  `sessions` table, never the token itself -- a database leak yields no
  usable session cookies. Lookup hashes the presented token and compares
  in constant time (`crypto/subtle`).
- Lifetimes: sliding **idle timeout** (default 12h) refreshed on use, plus
  a hard **absolute cap** (default 30d) that a refresh can't extend.
  Logout, an admin disabling the user, a password change, and a
  break-glass event all **revoke server-side immediately** -- the whole
  reason we don't use stateless JWTs.
- CSRF: because state-changing requests ride a cookie, we defend with
  `SameSite=Strict` **and** a double-submit CSRF token (a non-`httpOnly`
  companion cookie the SPA echoes in an `X-CSRF-Token` header; the server
  compares in constant time) **and** an `Origin` / `Sec-Fetch-Site`
  check on every mutating request. Defense in depth, since each control
  has a known individual gap.

### Local login

`POST /auth/login` with email + password. Passwords are verified against
an **Argon2id** hash (`golang.org/x/crypto/argon2`, OWASP's first-choice
KDF) with tuned parameters (see library section). Failures are
constant-time and generic ("invalid email or password" -- never
disclosing which), rate-limited per-IP and per-account, with escalating
backoff / temporary lockout on repeated failure. Whether a given local
login is even *allowed* is the break-glass logic below.

### SSO login (Azure AD + Google, OIDC)

Standard **OpenID Connect Authorization Code flow with PKCE (S256)**:

1. `GET /auth/oidc/{provider}/start` -- the server generates `state`,
   `nonce`, and a PKCE verifier (all `crypto/rand`), stashes them in a
   short-lived signed, `httpOnly` cookie, and 302s to the provider.
2. Provider authenticates the human and redirects back to
   `GET /auth/oidc/{provider}/callback`.
3. The server exchanges the code (with the PKCE verifier) for tokens via
   `golang.org/x/oauth2`, then **verifies the ID token** against the
   provider's published JWKS using `coreos/go-oidc/v3` -- signature,
   `iss`, `aud`, `exp`, and the `nonce` we planted. Azure AD's `tid`
   (tenant) is checked against the configured tenant; Google's `hd` /
   verified email against the configured allowlist.
4. We match the verified email / provider subject to an **existing,
   active** user. No match → reject (per the locked provisioning
   decision). Match → link the `user_identities` row on first use and
   mint the uniform session.

The browser never handles a provider token -- no MSAL.js, no gapi, no
client-side SDK. The frontend's "Sign in with Microsoft / Google" button
is a plain link to our own `/auth/oidc/{provider}/start`. Keeping the
whole flow server-side is both simpler and materially more secure
(nothing sensitive in JS, no token storage in the browser at all).

## Local auth: the break-glass floor

Local password auth exists mainly as the floor we can always fall back on
when *all* SSO fails. A single global switch plus a per-user exemption
governs it:

- `system_settings.local_auth_enabled` -- boolean, **default off**.
- `users.break_glass` -- boolean, per account.

A password login is permitted iff the account has a local identity with a
password **and**:

| | Break-glass admin | Regular local account |
|---|---|---|
| **Switch OFF** (normal operation) | ✅ can log in | ❌ rejected |
| **Switch ON** | ✅ can log in | ✅ can log in |

Break-glass admins **bypass the switch entirely** -- they are the "only
thing we can fall on" when SSO is down. The intended runbook: SSO breaks
→ a break-glass admin logs in locally (switch still off) → flips
`local_auth_enabled` **on** so ordinary local accounts can work through
the outage → flips it back **off** once SSO recovers. Every toggle of
that switch is an audited event, and break-glass logins are
alert-worthy.

Two safeguards this model demands, both built in this phase:

- **A last resort beneath the floor.** If even the break-glass password
  is lost, the UI cannot help you (you can't log in to fix login). So a
  small server-side CLI, `api/cmd/authadmin`, can create or reset a
  break-glass admin **directly against the database** -- usable by anyone
  with server/DB access, independent of the running API and independent
  of the switch. This is also how the very first admin is bootstrapped.
- **A higher bar for break-glass credentials.** Strong-password policy
  enforced on these accounts, and they are few by convention.

## Design: authorization as data (RBAC)

Enforcement is one middleware, but *what* it enforces is rows, not Go
constants -- on brand with the rest of this metadata-driven system, and
so a new distinction is a seed row rather than a recompile.

- `permissions` -- the catalog. Each is a stable string keyed to a real
  action verb the app already has, e.g. `features:read`,
  `features:write`, `connectivity:commit`, `relationships:commit`,
  `metadata:read`, `metadata:compile`, `symbology:write`, `analysis:run`,
  `admin:users`, `admin:auth-settings`. The seed enumerates them by
  auditing the existing routes in `main.go` and each mounted package.
- `roles` -- named bundles (e.g. `admin`, `editor`, `analyst`, `viewer`,
  `break-glass-admin`).
- `role_permissions` -- many-to-many.
- `user_roles` -- many-to-many; a user's effective permission set is the
  union.

A route declares the permission it requires; the middleware
(`auth.RequirePermission`) reads the authenticated user's permission set
from context and returns `403` if the permission is absent, `401` if
there's no valid session at all. Routes are gated by wrapping each mount in
`main.go`. The gate is **method-aware** on the mixed read/write mounts --
`GET`/`HEAD`/`OPTIONS` need the `:read` permission, mutating methods the
`:write` / `:commit` one -- so one wrapper covers a whole sub-router
without the sub-package knowing anything about auth. The pure-action
engines (`/trace`, `/analysis`), whose `POST`s are queries not writes, take
a single permission for every method. The effective permission set is
loaded once with the session (a `user_roles ⋈ role_permissions` union) and
carried on the request context, so a gate is a map lookup, not a query.

**As built** (migration `0025`), the catalog is `features:{read,write}`,
`connectivity:{read,commit}`, `relationships:{read,commit}`,
`diagrams:{read,write}`, `trace:run`, `analysis:run`, and
`metadata:{read,write}` -- where `metadata:write` covers catalog editing
*and* compiling *and* symbology, one cohesive admin capability gated at the
`/admin` mount (so the separate `metadata:compile` / `symbology:write` the
sketch above imagined were folded in rather than split by path) -- plus
`admin:auth-settings` (the break-glass switch) and `admin:users` (enforced
by the Slice D user-admin API at `/auth/admin/...`). The baseline roles are
`viewer` (reads), `analyst` (+ `trace`/`analysis`), `editor` (+ feature /
connectivity / relationship / diagram writes), and `admin` /
`break-glass-admin` (every permission). The switch handler accepts
`admin:auth-settings` **or** a break-glass account -- the bypass is
load-bearing, since a freshly bootstrapped break-glass admin must be able
to flip local auth on during an SSO outage before anyone has assigned it a
role. `api/cmd/authadmin` assigns the `admin` role on `create` and gained
`grant-role` / `revoke-role` / `roles` so the out-of-band path can manage
assignments until the Slice D UI exists.

**Deferred, deliberately:** per-feature-row and per-feeder / spatial
scoping (a genuinely different, heavier model -- "this editor may only
touch features in substation X's service area"). The `permissions`
catalog is designed so this becomes an added *dimension* later, not a
rewrite.

## Design: a real actor on every write

With an authenticated user in request context, the placeholder audit
columns become real. As built (see Slice E above for the final shape):

- `metadata.metadata_versions.published_by`,
  `runtime.feature_connectivity.established_by`,
  `runtime.feature_relationships.established_by` -- today `text`
  placeholders -- keep their `text` type and now hold the acting user's
  uuid going forward. The chosen representation is the id stored as-is
  (text), **not** a new `user_id` FK: no enforced join, and already-written
  historical rows (`"api"`, `"compiler-cli"`) are **not** retroactively
  rewritten, per those columns' own comments.
- `ops.migration_log.applied_by` stays as-is -- it's stamped by
  `migrations/apply.sh` as the Postgres role running the SQL, which has no
  HTTP session and so no application actor to thread.
- The generic CRUD engine (`api/internal/crud`) stamps `created_by` /
  `updated_by` (plain `uuid`, not FKs) on every feature write -- insert,
  update, publish/unpublish, and soft-delete/restore.
- This is the foundation for a real changeset log (who did what, when) --
  the thing the README has flagged as blocked on auth since Phase 6.

## Schema (migration `0024`)

New `iam` schema (identity & access management) -- kept out of
`metadata` / `runtime` / `gis`, since it's neither user-defined metadata
nor domain data. **Not** named `auth`: the `supabase/postgres` image
reserves an `auth` schema for GoTrue (owned by `supabase_auth_admin`, and
it already contains an `auth.users` table), which we can neither own nor
safely reuse. The Go package stays `auth`; only the physical schema is
`iam`.

- `iam.users` -- `id`, `email` (unique, citext), `display_name`,
  `status` (`active` / `disabled`), `break_glass` bool, timestamps.
  **No password column here** -- credentials live on identities.
- `iam.user_identities` -- `id`, `user_id` FK, `provider`
  (`local` / `azuread` / `google`), `provider_subject`, `password_hash`
  (Argon2id-encoded, nullable -- only local rows have one),
  `password_changed_at`, unique on `(provider, provider_subject)` and one
  `local` identity per user. A user may hold both a `local` and an SSO
  identity at once.
- `iam.sessions` -- `id`, `user_id` FK, `token_sha256` (bytea, unique),
  `created_at`, `last_used_at`, `absolute_expires_at`,
  `revoked_at` (nullable), plus `ip` / `user_agent` for the audit trail.
- `iam.permissions`, `iam.roles`, `iam.role_permissions`,
  `iam.user_roles` -- the RBAC tables above. These land in migration
  `0025` (Slice C), not `0024`, so the enforcement code and its seed data
  arrive together; `0024` created only the identity/session/audit
  foundation.
- `iam.system_settings` -- a small key/value (or single typed row)
  holding `local_auth_enabled` and future global toggles, with an
  `updated_by` / `updated_at` for the audit requirement.
- `iam.auth_events` -- append-only audit of security-relevant events
  (login success/failure, logout, SSO link, switch toggle, break-glass
  login, role change), each with actor, ip, and timestamp.

Seeds: the permission catalog, a baseline set of roles, and the RBAC
grants. The **first** break-glass admin is created out-of-band by
`api/cmd/authadmin`, not committed as a seed (no shipped default
credentials, ever).

## Hardened library choices

The explicit ask was "the most secure libraries you can use." The
governing principle here is *minimal, audited surface* -- prefer the Go
standard library, and where we can't, prefer one small package that is
the de-facto community standard and actively maintained, over a large
framework we'd have to trust wholesale.

- **Password hashing -- `golang.org/x/crypto/argon2` (Argon2id).**
  OWASP's first-choice password KDF; memory-hard, tunable. Parameters
  start at the OWASP baseline (memory 19 MiB, time 2, parallelism 1) and
  are tuned to ~the target hardware, stored in the encoded hash so they
  can be raised later without invalidating old hashes. Salts from
  `crypto/rand`. (Rationale for not using bcrypt: Argon2id is the
  current recommendation and resists GPU/ASIC cracking better; bcrypt is
  an acceptable fallback if a pure-Go, zero-cgo constraint ever bites,
  which `x/crypto/argon2` does not.)
- **OIDC verification -- `github.com/coreos/go-oidc/v3`.** The standard
  Go OIDC library (CoreOS / Red Hat); does JWKS fetching + caching and
  full ID-token verification (signature, `iss`, `aud`, `exp`, `nonce`).
  We never hand-parse a JWT.
- **OAuth2 flow -- `golang.org/x/oauth2`.** The canonical Go OAuth2
  client (auth-code exchange, provider endpoints). Used only for the
  code-for-token exchange; token *trust* is established by go-oidc.
- **Randomness -- `crypto/rand` (stdlib).** Session tokens, CSRF tokens,
  OIDC `state` / `nonce`, PKCE verifiers. Never `math/rand`.
- **Constant-time comparison -- `crypto/subtle` (stdlib).** Session-token
  hash comparison, CSRF-token comparison -- no early-exit timing leaks.
- **Session-token hashing -- `crypto/sha256` (stdlib).** Tokens are high
  entropy (256-bit random), so a fast hash is correct here (unlike
  passwords -- those get Argon2id); it exists so a DB leak can't replay
  sessions.
- **Rate limiting / lockout -- `golang.org/x/time/rate` (stdlib-adjacent,
  Go team).** Per-IP and per-account throttling on the local-login path,
  plus DB-tracked failed-attempt lockout.
- **CSRF & security headers.** We implement double-submit CSRF and the
  header set (HSTS, `X-Content-Type-Options: nosniff`, a strict CSP,
  `Referrer-Policy`, `X-Frame-Options: DENY`) as small, reviewable
  middleware rather than pulling a framework; the primitives
  (`crypto/rand`, `crypto/subtle`) are stdlib.
- **MFA (deferred) -- `github.com/pquerna/otp` when we add TOTP.** Noted
  so the choice is pre-made; not built this phase.

**Supply-chain hardening:** every dependency is version-pinned with
`go.sum` verification; `govulncheck` runs in CI against the module;
dependency additions are deliberate and few (the whole phase adds
essentially `go-oidc`, `x/oauth2`, and `x/time` on top of stdlib +
`x/crypto` we'd add for Argon2id). No shipped default credentials; secrets
(OIDC client IDs/secrets, cookie/CSRF signing keys, Argon2id pepper if
used) come from env, never the repo.

## Configuration surface (env)

- `SESSION_COOKIE_NAME`, `SESSION_IDLE_TIMEOUT`, `SESSION_ABSOLUTE_MAX`.
- `LOCAL_AUTH_ENABLED` is **not** env -- it's the DB switch, so a
  break-glass admin can toggle it at runtime without a redeploy. Env only
  seeds its initial value on an empty DB.
- `OIDC_AZURE_TENANT_ID`, `OIDC_AZURE_CLIENT_ID`,
  `OIDC_AZURE_CLIENT_SECRET`, `OIDC_AZURE_REDIRECT_URL`.
- `OIDC_GOOGLE_CLIENT_ID`, `OIDC_GOOGLE_CLIENT_SECRET`,
  `OIDC_GOOGLE_REDIRECT_URL`, `OIDC_GOOGLE_ALLOWED_DOMAIN`.
- A provider absent from config is simply not offered on the login screen
  -- local-only, or one SSO provider, are all valid deployments.

## Build slices

- **A — Identity foundation. (built)** Migration `0024` (in the `iam`
  schema: users, user_identities, sessions, system_settings, auth_events);
  Argon2id local login/logout; session middleware injecting the user into
  request context; the break-glass truth table; `api/cmd/authadmin`
  (bootstrap + reset); double-submit CSRF + security-header middleware. No
  SSO, no RBAC enforcement yet -- but the trust boundary starts existing.
- **B — OIDC federation. (built)** Azure AD + Google via auth-code + PKCE
  (`oidc.go`, `oidc_handlers.go`). `GET /auth/providers` tells the login
  screen which providers are live; `/auth/oidc/{provider}/login` starts the
  flow (state + nonce + PKCE in a SameSite=Lax flow cookie so it survives
  the IdP round-trip); the callback verifies the ID token (signature/iss/
  aud via go-oidc, then nonce), applies provider trust rules (Google
  email-verified + optional hosted-domain; Azure tenant-scoped issuer),
  then **match-or-reject**: an already-linked subject logs in; otherwise a
  pre-provisioned, active account is matched by email and the identity is
  linked on first login; an unknown email is rejected. Never auto-creates a
  user. Terminates in the same session as local login.
- **C — RBAC. (built)** Permission catalog + roles + grants (migration
  `0025`); `auth.RequirePermission` enforcement middleware (method-aware
  read/write, single-permission for the pure-action engines); the effective
  permission set loaded with the session and carried on context; **every**
  route in `main.go` gated (only `/healthz` and `/auth`'s own
  login/SSO/self-gated endpoints stay open); the break-glass switch moved to
  `admin:auth-settings`-or-break-glass; `authadmin` extended to assign the
  bootstrap `admin` role and manage grants (`grant-role` / `revoke-role` /
  `roles`); `/auth/me` + login now return the caller's permission list for
  the SPA to shape its UI (Slice D).
- **D — Frontend. (built)** Login screen (`routes/login.tsx`: SSO buttons
  from `GET /auth/providers` as plain server-side links to
  `/auth/oidc/{provider}/login`, plus the local email/password form →
  `POST /auth/login`); a session-aware app shell (`RequireSession` resolves
  `GET /auth/me` once and bounces an unauthenticated browser to `/login`,
  remembering where it was; `RequirePermission` renders a 403 page for a
  signed-in user who lacks a route's permission; the sidebar and header
  hide controls the caller's permission set doesn't cover, mirroring the
  server gate); and a user-management page (`routes/admin-users.tsx`,
  behind `admin:users`) that creates accounts, assigns/revokes roles,
  sets/replaces the local password, unlinks SSO identities, disables/enables
  accounts, and — for a caller who also holds `admin:auth-settings` —
  toggles the local-auth switch. This slice added the online user-admin API
  (`api/internal/auth/useradmin.go`, mounted at `/auth/admin/...`), the
  in-app counterpart to the `authadmin` CLI. `apiFetch` now echoes the
  readable `ve_csrf` cookie in `X-CSRF-Token` on every mutating request.
  SSO *linking* stays automatic (bound by email on first login), so the UI
  only unlinks, never pre-sets, an SSO identity.
- **E — Actor threading. (built)** Every mutating API path now stamps the
  acting user, read from the request context via `auth.ActorID` (empty on a
  session-less CLI/system path, which then writes NULL or keeps the system
  literal). The write paths: the CRUD engine's insert/update/publish/
  unpublish/soft-delete/restore (`api/internal/crud`), connectivity and
  relationship establish (`established_by`), and the metadata compiler's
  publish (`published_by`, still `"compiler-cli"` on the `cmd/compile`
  path). **Representation decision:** the actor's id is stored as-is, not
  behind a new FK -- the three pre-auth placeholder columns
  (`feature_connectivity` / `feature_relationships.established_by`,
  `metadata_versions.published_by`) keep their `text` type and now hold the
  user's uuid going forward; the feature tables gain plain `uuid`
  `created_by`/`updated_by` columns (migration `0026` backfills existing
  tables, `compiler.buildCreateTable` adds them to new ones, and the
  compiler's recompile path backfills like it does `deleted_at`/`name`).
  Deliberately **not** FKs to `iam.users` -- no enforced join, keeping the
  `gis`/`runtime` schemas free of a hard dependency on `iam`, and pre-auth
  placeholder rows (`"api"`, `"compiler-cli"`) are never rewritten.
  `ops.migration_log.applied_by` is left untouched: it's written by
  `migrations/apply.sh` as the Postgres role running raw SQL, a path with
  no HTTP session to read an actor from. The changeset-log foundation.

## Explicitly out of scope (this phase)

- MFA / TOTP (library pre-chosen; not built).
- SCIM / automated deprovisioning from the IdP.
- Per-feature-row and per-feeder / spatial authorization scoping.
- Multi-tenancy (single org, per the locked decision).
- Postgres RLS (the API stays the single Go gatekeeper; RLS would require
  per-user DB roles, a separate, larger re-architecture).

## Verification plan

Because the local shell cannot run builds/migrations here, everything
below is owed as copy-paste verification once landed, per slice:
`task migrate` (0024), `go build ./... && go vet ./... && govulncheck
./...`, `curl` the local-login / logout / whoami flow, a scripted OIDC
callback against each provider's test tenant, an RBAC matrix
(`403`/`200` per role per route), a break-glass truth-table check (switch
on/off × break-glass/regular), and a frontend Playwright pass over
login, SSO redirect, forbidden handling, and user management.
