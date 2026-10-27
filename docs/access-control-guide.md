# VoltEdge Access Control Guide

**Users, roles, permissions, territories and the audit trail — for administrators and auditors.**

This is the reference for *who may do what, and where* in VoltEdge. It is written from the code and the migrations, not from earlier design notes: every permission, role, grant and route table below was rebuilt by applying `migrations/*.sql` to a scratch PostgreSQL and reading `api/cmd/server/main.go` and the handler packages. Where an older document disagrees with the code, this guide follows the code and the disagreement is listed in the [Corrections appendix](#appendix-d-corrections-to-existing-documents).

> **Who should read what.**
> *Administrators* (people who create accounts, assign roles, scope crews, run the break-glass procedures): chapters 1–3, 6–11, 14 and the runbooks in chapter 15.
> *Auditors and security reviewers* (people who must be able to answer "who could do this, how do we know, and where is the evidence?"): chapters 1, 4, 5, 6–9, 13, 16 and the appendices.
> *Developers adding a feature*: chapter 5 (how a route is gated) and chapter 3.4 (adding a permission or a role).
>
> The companion interactive edition (`access-control-guide-interactive.html`) lets you run the rules in this guide: a role designer, a request tester that walks the real middleware order, a territory resolver, a lockout lab, an SSO decision simulator and a "why can't Priya do X" diagnoser.

## Contents

1. [The access model on one page](#1-the-access-model-on-one-page)
2. [Users](#2-users)
3. [Roles](#3-roles)
4. [The permission catalog](#4-the-permission-catalog)
5. [From a request to a permission: the route map](#5-from-a-request-to-a-permission-the-route-map)
6. [Authentication: sessions, cookies and CSRF](#6-authentication-sessions-cookies-and-csrf)
7. [Passwords, lockout and rate limits](#7-passwords-lockout-and-rate-limits)
8. [Single sign-on (OIDC) and provisioning](#8-single-sign-on-oidc-and-provisioning)
9. [API tokens, field logins and service identities](#9-api-tokens-field-logins-and-service-identities)
10. [The break-glass account](#10-the-break-glass-account)
11. [Spatial access control (territories)](#11-spatial-access-control-territories)
12. [Private drafts and the pending-edit overlay](#12-private-drafts-and-the-pending-edit-overlay)
13. [The audit trail](#13-the-audit-trail)
14. [Putting the layers together](#14-putting-the-layers-together)
15. [Runbooks](#15-runbooks)
16. [Auditor's checklist and queries](#16-auditors-checklist-and-queries)
17. [Troubleshooting](#17-troubleshooting)
- [Appendix A. Role and permission matrix](#appendix-a-role-and-permission-matrix)
- [Appendix B. SQL cookbook](#appendix-b-sql-cookbook)
- [Appendix C. Source index](#appendix-c-source-index)
- [Appendix D. Corrections to existing documents](#appendix-d-corrections-to-existing-documents)
- [Appendix E. Not verified, and known limits](#appendix-e-not-verified-and-known-limits)

**The cast.** To stay concrete the guide reuses the people from the other manuals: **Priya** (dispatcher, `viewer`), **Tom** (planner, `analyst`), **Dana** (field supervisor, `editor`), **Ada** (system administrator, `admin`) and **Owen** (the emergency break-glass account). Dana's crew works the **Oakwood District**.

---

## 1. The access model on one page

VoltEdge makes three independent decisions about every request. They are evaluated by different code, stored in different tables, and an action succeeds only when all of them say yes.

| Question | Mechanism | Stored in | Enforced by |
| --- | --- | --- | --- |
| **Who are you?** (authentication) | Session cookie, API bearer token, or webhook credential | `iam.users`, `iam.user_identities`, `iam.sessions`, `iam.api_tokens` | `auth.SessionLoader` (cookies); `auth.ResolveAPIToken` (bearer) |
| **What verbs may you perform?** (RBAC) | A set of `resource:action` permission strings | `iam.permissions`, `iam.roles`, `iam.role_permissions`, `iam.user_roles` | `auth.RequirePermission` on each mount, plus a few in-handler checks |
| **Where may you perform them?** (spatial access) | Territory grants, the territory tree and the manager tree | `iam.user_territories`, `iam.users.manager_id`, `gis.territories`, the `territories:unscoped` permission | `territory_id` predicates in `crud`, `tiles`, `export`, `trace`, `powerflow` (start feature only), `importer`, `fieldpack`, `featureregistry` — **not** `oms`/`faults`/`wam` or graph-structure endpoints (§11.5) |

A fourth, smaller gate sits on top for feature data: **private drafts** (chapter 12) hide never-published features from everyone except their author and the author's management chain, and let a scoped user stage private edits of published features.

### 1.1 Three facts that explain most surprises

1. **A user has no permissions of their own.** There is no per-user grant table. A user's effective permission set is the *union* of the permissions of the roles they hold, recomputed on every request (`SELECT DISTINCT rp.permission_key FROM iam.user_roles ur JOIN iam.role_permissions rp ...`). A user with no role can sign in and is authorised for nothing.
2. **Authorization never learns how you authenticated.** A password login, an SSO login and a bearer token all produce the same `SessionUser` snapshot (id, permissions, territory scope, subordinates). The break-glass *column* changes how you may *sign in*; it does not appear in `Has()`.
3. **Roles are data, but there is no role editor.** The only API for roles is a read-only list (`GET /auth/admin/roles`). Roles and their permission bundles change through SQL (a migration or an operator's `INSERT`). Chapter 3.4 explains how.

### 1.2 The request pipeline, in order

The order matters because it decides which error you see. For a browser request to `POST /features/pole`:

```
 1. httpx.Logger / Recoverer / MaxBytes(32 MiB)      request id, panic guard, global body cap
 2. auth.SecurityHeaders                               nosniff, X-Frame-Options DENY, no-referrer, COOP; HSTS only over TLS
 3. auth.SessionLoader                                 cookie -> session -> user -> permissions -> territory scope (never rejects; anonymous flows on)
 4. (per mount) auth.RequirePermission(read, write)    401 no session; 403 permission missing (GET/HEAD/OPTIONS pick read, others pick write)
 5. (per route, some) RequireCSRF                      403 cross-site / bad X-CSRF-Token (mutations only, and only on the surfaces listed in 6.4)
 6. (per route, some) inner gates                      extra permissions, e.g. diagrams:delete, features:wipe, relationships:commit
 7. handler: territory scope                           404 existing feature outside your territories; 400 no covering territory; 403 create/move into one you do not hold
 8. handler: private-drafts staging / visibility       hide other people's drafts; stage your own edits of published features
 9. handler writes created_by / updated_by             ActorID(ctx) stamps the acting user
```

Two ordering consequences worth remembering:

- **401 versus 403 versus 404.** No session -> 401. Session without the permission -> 403. Session with the permission but the feature is outside your territory or is someone else's private draft -> **404** (existence is hidden) for reads *and* for updates/deletes of an existing feature; **403** only when you try to create a feature in, or move one into, a territory you do not hold.
- **Spatial checks happen after RBAC.** A viewer posting a feature is refused at step 4 and never reaches step 7.

### 1.3 The tables

| Table | What it holds |
| --- | --- |
| `iam.users` | One row per person: `email` (citext, unique), `display_name`, `status` (`active`/`disabled`), `break_glass`, `failed_login_count`, `locked_until`, `manager_id` (org tree). **No password column.** |
| `iam.user_identities` | One row per way to prove identity: `provider` is `local`, `azuread` or `google`. Local rows carry the Argon2id `password_hash`; SSO rows carry the provider's `provider_subject`. Unique `(provider, provider_subject)` and unique `(user_id, provider)`. |
| `iam.sessions` | Server-side sessions: SHA-256 of the cookie token, per-session CSRF token, `idle_expires_at`, `absolute_expires_at`, `revoked_at`, `ip`, `user_agent`. |
| `iam.api_tokens` | Long-lived bearer tokens: `token_prefix` (first 12 chars, for display), `token_sha256`, `expires_at` (nullable), `revoked_at`, `last_used_at`, `created_by`. |
| `iam.permissions` | The catalog: `key` (primary key), `description`. 37 rows. |
| `iam.roles` | `key`, `name`, `description`, `is_system`. 9 seeded rows. |
| `iam.role_permissions` | Which permissions a role grants. 163 seeded rows. |
| `iam.user_roles` | Which roles a user holds (`granted_by`, `granted_at`). **No rows are seeded.** |
| `iam.user_territories` | Direct user -> territory grants. |
| `iam.system_settings` | Runtime switches: `local_auth_enabled`, `territory_enforcement_enabled`, and (when set) `private_drafts_enabled`, plus map defaults and the log level. |
| `iam.auth_events` | The append-only security audit. |

> **Schema name.** The physical schema is `iam`, not `auth`: the supabase/postgres image reserves `auth` for GoTrue (migration 0024). The Go package is still `auth`.

---

## 2. Users

### 2.1 Anatomy of an account

A user row carries *state*, not power:

| Column | Meaning | Who changes it |
| --- | --- | --- |
| `email` | Login name **and** the SSO match key. Lower-cased on create. Immutable through the API. | nobody (SQL only) |
| `display_name` | Shown in the UI and logs. | `PATCH /auth/admin/users/{id}` |
| `status` | `active` or `disabled`. A disabled user's sessions are revoked, local login is refused, and bearer tokens stop resolving. | `POST /auth/admin/users/{id}/status` |
| `break_glass` | Exempts the account from the local-auth switch and lets it survive a factory reset. **Grants no permission.** See chapter 10. | create-time only through the API (`break_glass` in the create body); SQL otherwise |
| `failed_login_count`, `locked_until` | Brute-force bookkeeping (chapter 7). | the login code; clears on success |
| `manager_id` | The org tree edge that spatial scope cascades up (chapter 11). | `POST /auth/admin/users/{id}/manager` |

An account is *usable* only if it is `active`, has at least one way to authenticate (a local password identity or a linked/linkable SSO identity), and holds at least one role.

### 2.2 Creating accounts

There are three paths, and they differ in who may use them and what they leave behind.

| Path | Who | Creates | Notes |
| --- | --- | --- | --- |
| `POST /auth/admin/users` | holder of `admin:users` (+ CSRF) | user, optional local identity, optional role | The everyday path. Audited (`role_changed` when a role is given). |
| `authadmin create` | anyone with `DATABASE_URL` and shell access | user, local identity, default role `admin`, **default `--break-glass=true`** | The bootstrap path. Password comes from `AUTHADMIN_PASSWORD`, never a flag. Not audited in `iam.auth_events`. |
| SSO first login | nobody | **nothing** | SSO never creates an account (chapter 8). It only links to a pre-provisioned one. |

The create body is `{email, display_name, break_glass, role, password}`. The rules enforced in code:

- `email` and `display_name` are required; the email must contain `@`; duplicates are refused (400).
- A `password`, if given, must be at least **12 characters** (`minLocalPasswordLen`). It is optional because an **SSO-only account** is legitimate.
- `break_glass: true` **requires a password** ("a break-glass account requires a local password").
- `role`, if given, must be an existing role key (400 otherwise). It is assigned in the same request.

### 2.3 Changing and ending accounts

| Action | Endpoint | Effect | Guards |
| --- | --- | --- | --- |
| Rename | `PATCH /auth/admin/users/{id}` | Display name only. | Email is deliberately not editable: it would orphan a linked SSO identity. |
| Grant role | `POST .../roles` `{role}` | Inserts a `user_roles` row (idempotent). | Role must exist. |
| Revoke role | `DELETE .../roles/{role}` | Deletes the row. Takes effect on the user's **next request**. | None — you *can* remove your own admin role; see 2.5. |
| Disable / enable | `POST .../status` `{status}` | Sets status; on disable, **revokes every live session** of that user. | You cannot disable yourself (403). |
| Set / replace password | `POST .../password` | Creates or replaces the local identity's hash. | Minimum 12 characters. Does **not** revoke existing sessions and writes **no audit event**. |
| Unlink SSO | `DELETE .../identities/{provider}` | Deletes the `azuread`/`google` identity row. | Local identity cannot be unlinked here. The next SSO login re-links by email. |
| Grant / revoke territory | `POST/DELETE .../territories` | Adds/removes an `iam.user_territories` row. | Both ids must exist (400). Cache flushed after write. |
| Set manager | `POST .../manager` `{manager_id}` | Sets or clears (`null`) the org edge. | No self-manager; no cycles. |

There is **no delete-user endpoint**. Offboarding is *disable*, which keeps the audit trail and `created_by` references intact. (`ON DELETE CASCADE` on sessions, identities, role and territory grants only matters for an operator's manual SQL or a factory reset.)

### 2.4 Account lifecycle

```
 (nothing) --create--> active --disable--> disabled --enable--> active
                          |  \                  |
                          |   \--lockout 5x-->  | (locked_until = now+15m; status stays active)
                          |
                          +-- roles: none / some --> authorised for nothing / for the union
```

A *locked* account is not a *disabled* one: lock is temporary and per-password-failure; disable is administrative and immediate. SSO sign-in ignores the lock (the lock only guards local password verification) but respects `disabled`.

### 2.5 Foot-guns an administrator should know

- **Removing your own `admin` role** is allowed by the API. If you were the only holder you are now unable to administer anyone. Recover with `authadmin grant-role --email <you> --role admin` (chapter 15.7).
- **A user with no role** authenticates fine and every gate answers 403. This is the usual "I can log in but see nothing" report.
- **Email case.** Emails are stored lower-cased; SSO matching lower-cases the provider's address too.
- **Two admins, one is a break-glass.** If every `admin:users` holder is disabled or locked, no one can use the user-admin API. That is exactly what the `authadmin` CLI is for.

---

## 3. Roles

### 3.1 The nine seeded roles

All nine have `is_system = true`. They come from three migrations: **0025** (five baseline roles), **0078** (`supervisor`) and **0099** (three field roles).

| Role | Name | Migration | Grants | Stored description | Typical holder |
| --- | --- | --- | --- | --- | --- |
| `viewer` | Viewer | 0025 | 11 | Read-only: features, connectivity, relationships, diagrams, and the metadata catalog. | Priya, dispatcher: sees everything, changes nothing |
| `analyst` | Analyst | 0025 | 13 | Everything a viewer can do, plus running traces and network analysis. | Tom, planner: runs traces and studies |
| `editor` | Editor | 0025 | 25 | Everything an analyst can do, plus editing features, connectivity, relationships, and diagrams. | Dana, field supervisor: edits data |
| `supervisor` | Supervisor | 0078 | 18 | Everything an editor can do, plus deleting diagrams. | An editor who may also delete diagrams (see 3.3 for the drift) |
| `admin` | Administrator | 0025 | 37 | Full access, including the metadata catalog, user management, and auth settings. | Ada, system administrator |
| `break-glass-admin` | Break-glass admin | 0025 | 37 | Full access; the role given to emergency break-glass accounts. | Owen, emergency account (pair with the break_glass flag) |
| `field-surveyor` | Field surveyor | 0099 | 5 | Read-only field capture: view features and connectivity, take photos and attachments. Cannot edit features. | Field tool: read-only capture with photos |
| `field-enumerator` | Field enumerator | 0099 | 6 | Field capture with editing: everything a surveyor can do, plus creating and editing features in the field. | Field tool: surveyor plus creating/editing features |
| `field-technician` | Field technician | 0099 | 11 | Field works: everything an enumerator can do, plus the work board -- log defects, perform and close work orders, record inspections. | Field tool: enumerator plus work board, faults and tags |

Grant counts are from the scratch database built from the migrations: viewer 11, analyst 13, editor 25, supervisor 18, admin 37, break-glass-admin 37, field-surveyor 5, field-enumerator 6, field-technician 11 (163 rows in all).

### 3.2 How the roles relate

```
 viewer (11)        reads everything a map user needs, incl. oms/wam/faults/tags/annotations/attachments reads
   |  + trace:run, analysis:run
 analyst (13)
   |  + features:write, connectivity:commit, relationships:commit, diagrams:write,
   |    annotations:write, attachments:write, oms:write, wam:write, faults:write, tags:write,
   |    assemblies:read/write
 editor (25)        edits DATA; no metadata:*, no admin:*, no territory powers
   |
   +-- supervisor (18): copy of editor *as of migration 0078*, plus diagrams:delete  (see 3.3)
 admin (37) == break-glass-admin (37): every permission

 field-surveyor (5)  < field-enumerator (6) < field-technician (11)    (a separate, narrower ladder for the field tool;
                                                                        each holds field:login -- the gate to sign in to it)
```

Things the ladder deliberately does **not** give an editor: `metadata:write` (edit the schema), `admin:users` (manage people), `admin:auth-settings`, `territories:write`, `territories:unscoped`, `circuits:write`, `sync:review`/`sync:post`, `admin:field-packs`, `features:wipe`, `diagrams:delete`.

### 3.3 A drift you should know about: `supervisor` is not "editor plus"

The role's stored description reads *"Everything an editor can do, plus deleting diagrams."* That was true when migration 0078 ran, because it copied the editor's grants at that moment. Later permission migrations (0088 OMS, 0097 WAM, 0107 faults, 0115 tags) granted their new permissions to a hard-coded list of roles that does **not** include `supervisor`. In the migrated database today:

| editor has, supervisor lacks | supervisor has, editor lacks |
| --- | --- |
| `oms:read`, `oms:write`, `wam:read`, `wam:write`, `faults:read`, `faults:write`, `tags:read`, `tags:write` | `diagrams:delete` |

So a supervisor can delete diagrams but cannot open the outage board, work board, fault board or resolve a scanned tag. If that is not intended, grant the missing permissions (SQL recipe in appendix B.7). This guide reports it rather than silently correcting it because the grant is a data decision.

### 3.4 Role editing rules

What the code allows and forbids:

| Operation | Possible? | How |
| --- | --- | --- |
| List roles | yes | `GET /auth/admin/roles` (key, name, description only — **not** the permissions), `authadmin roles` |
| See a role's permissions | via SQL only | appendix B.1 |
| Assign / revoke a role to a user | yes | API (`admin:users`) or `authadmin grant-role` / `revoke-role` |
| Create a role | **SQL only** | `INSERT INTO iam.roles ...` then `INSERT INTO iam.role_permissions ...` |
| Change the permissions of a role | **SQL only** | `INSERT` / `DELETE` on `iam.role_permissions` |
| Delete a role | SQL only | `ON DELETE CASCADE` removes its `user_roles` and `role_permissions` rows, silently stripping holders |
| Protect the seeded roles | **not enforced** | `is_system` is stored and exposed in the model, but no Go code or trigger reads it to refuse an edit or delete |
| Per-user exceptions | impossible by design | there is no `user_permissions` table |

Because role changes are SQL, they should travel as **migrations** (numbered files in `migrations/`, recorded in `ops.migration_log`). That gives you review, an audit record and repeatability across environments. The established pattern, from 0061/0088/0097/0107/0115/0116:

```sql
INSERT INTO iam.permissions (key, description) VALUES
    ('widgets:read',  'View widgets.'),
    ('widgets:write', 'Create and edit widgets.')
ON CONFLICT (key) DO NOTHING;

INSERT INTO iam.role_permissions (role_id, permission_key)
SELECT r.id, 'widgets:read' FROM iam.roles r
WHERE r.key IN ('viewer', 'analyst', 'editor', 'admin', 'break-glass-admin')
ON CONFLICT DO NOTHING;
```

> **The `CROSS JOIN` trap.** Migration 0025 gave `admin` and `break-glass-admin` "every permission" with a `CROSS JOIN` over `iam.permissions`. That ran *once*, against the 14 permissions that existed then. Every later permission was granted to them by an explicit `INSERT`. So "admin has every permission" is true today only because each later migration remembered to add the grant. A permission added without an explicit admin grant is *not* held by admin.

**Adding a permission end to end** (developers):

1. Add the constant to `api/internal/auth/rbac.go` and use it in the route gate (`main.go` or the package).
2. Add a numbered migration that inserts the catalog row and the grants (pattern above). Include `admin` and `break-glass-admin`.
3. Mirror the check in the SPA only to hide controls (`usePermission()` in `web/src/hooks/use-auth.ts`, `RequirePermission` in `web/src/app.tsx`). The server is the boundary; the SPA is cosmetic.
4. Add the permission to this guide's catalog, matrix and route map.

**Adding a role** (administrators): write the migration, then `authadmin roles` to confirm it is visible, then assign it. Remember the new role appears in the user-admin UI role picker only because the picker reads `GET /auth/admin/roles`.

### 3.5 Choosing a role for a person

Ask "what is the *most limited* role that still lets this person do their job?", not "how senior are they?".

| Person | Needs | Role |
| --- | --- | --- |
| Priya, dispatcher | See everything, change nothing | `viewer` |
| Tom, planner | Run studies, must not edit the network | `analyst` |
| Dana, field supervisor | Maintain data | `editor` |
| A crew lead who also manages diagrams but not work orders | Diagram deletes | `supervisor` — but read 3.3 first |
| A surveyor on a tablet | Capture photos, read features | `field-surveyor` |
| An enumerator creating assets in the field | Create/edit features and attachments | `field-enumerator` |
| A technician closing work orders | Work board, faults, tags, plus enumerator abilities | `field-technician` |
| Ada, administrator | Author metadata, manage accounts | `admin` |
| Owen, emergency account | Sign in when SSO is down | `break-glass-admin` **and** the `break_glass` flag |

Granting `admin` "to be safe" is the opposite of safe: it is the one role with no ceiling (and it is `territories:unscoped`, so it also ignores territory scoping).

### 3.6 Multiple roles

Roles are additive. A user holding `viewer` and `field-technician` holds the union of both. There is no deny and no precedence. To *remove* a capability you must remove every role that grants it — use the diagnoser in the interactive edition or appendix B.3 to find which role contributes a permission.

---

## 4. The permission catalog

Thirty-seven permission keys exist. Each is a stable `resource:action` string that `auth.RequirePermission` (or an in-handler `Has()`) compares verbatim; the Go constants live in `api/internal/auth/rbac.go` (plus `access.PermUnscoped` for `territories:unscoped`).

| Permission | Area | Since | What it allows | Held by | Gates (R read, W write/other, all, + extra check) |
| --- | --- | --- | --- | --- | --- |
| `admin:auth-settings` | Administration | 0025 | Change auth settings, including the local-auth break-glass switch. | Ad, BG | `POST /auth/settings/local-auth` (or break_glass) |
| `admin:field-packs` | Administration | 0040 | List every user's field-tool region packs, and disable (hide from the device) or delete them. | Ad, BG | `/field-packs` (all); UI: `/admin/field-packs` |
| `admin:users` | Administration | 0025 | Manage user accounts and role assignments. | Ad, BG | `/log-settings` (all); `/auth/admin/*` (all); UI: `/admin/users`, `/admin/logging` |
| `analysis:run` | Trace & analysis | 0025 | Run power-flow, fault-current, and ampacity analysis. | A, E, S, Ad, BG | `/analysis` (all) |
| `annotations:read` | Annotations | 0061 | View map annotations (the redlining/markup layer). | V, A, E, S, Ad, BG | `/map-annotations` (R) |
| `annotations:write` | Annotations | 0061 | Create, edit, and delete map annotations (redlining/markup). | E, S, Ad, BG | `/map-annotations` (W) |
| `assemblies:read` | Assemblies | 0162 | View the assembly-kit catalog (place-one-get-a-wired-set recipes). | E, S, Ad, BG | `/assemblies/templates` (R) |
| `assemblies:write` | Assemblies | 0162 | Author assembly kits: define a place-one-get-a-wired-set recipe and its components. A kit arranges existing classes, so it sits below `metadata:write`. | E, S, Ad, BG | `/assemblies/templates` (W); UI: `/admin/assemblies` |
| `attachments:read` | Attachments | 0041 | View and download files attached to features. | V, A, E, S, Ad, BG, FS, FE, FT | `/attachments` (R) |
| `attachments:write` | Attachments | 0041 | Upload, replace, and delete files attached to features. | E, S, Ad, BG, FS, FE, FT | `/attachments` (W) |
| `circuits:write` | Circuits | 0063 | Create, rename, and delete circuits, and edit their voltage and source feature. | Ad, BG | `/circuits` (W); UI: `/admin/circuits` |
| `connectivity:commit` | Connectivity | 0025 | Establish or revoke electrical connectivity. | E, S, Ad, BG | `/connectivity` (W); `POST /couplers` (+); `POST /rmus` (+) |
| `connectivity:read` | Connectivity | 0025 | View committed electrical connectivity. | V, A, E, S, Ad, BG, FS, FE, FT | `/connectivity` (R) |
| `diagrams:delete` | Diagrams | 0078 | Delete an entire schematic diagram, including all of its placements. | S, Ad, BG | `DELETE /diagrams/{id}` (+) |
| `diagrams:read` | Diagrams | 0025 | View schematic diagrams and their placements. | V, A, E, S, Ad, BG | `/diagrams` (R) |
| `diagrams:write` | Diagrams | 0025 | Create and rename diagrams and place or remove features on them. | E, S, Ad, BG | `/diagrams` (W) |
| `faults:read` | Faults | 0107 | View the fault board: electrical fault events and their outage/defect links. | V, A, E, Ad, BG, FT | `/faults` (R); UI: `/faults` |
| `faults:write` | Faults | 0107 | Log fault events, link them to outages, and raise defects from them. | E, Ad, BG, FT | `/faults` (W) |
| `features:read` | Features | 0025 | View feature data: map layers, attributes, and feature-class schema. | V, A, E, S, Ad, BG, FS, FE, FT | `/feature-classes` (all); `/features` (R); `/imports` (R); `/export` (all); `/tiles` (all); `/territories` (R); `/circuits` (R); `/feature-registry` (all); `/panels` (R); `/rmus` (R); `/packaged-substations` (R); `/assemblies` (R); `/ducts` (R); `/couplers` (R); `/ties` (R); `/feature-links` (R); `/boq` (all); `/map-settings` (R); `/private-drafts` (R); `/basemaps` (R); `/service-layers` (R) |
| `features:wipe` | Features | 0116 | Bulk soft-delete and permanently purge every feature in a class (admin). | Ad, BG | `POST /features/{code}/wipe`, `.../counts`, `.../purge-deleted` (+) |
| `features:write` | Features | 0025 | Create, edit, publish, unpublish, delete, and restore features. | E, S, Ad, BG, FE, FT | `/features` (W); `/imports` (W); `/ownership` (all); `/panels` (W); `/rmus` (W); `/packaged-substations` (W); `/assemblies` (W); `/ducts` (W); `/couplers` (W); `/ties` (W); `/feature-links` (W); `POST /circuits/{id}/assign` (+); UI: `/imports` |
| `field:login` | Field tool | 0164 | Sign in to the offline field app (obtain a field-app bearer token). Without it, a valid credential is refused 403 at field login -- so field-app access is an explicit grant, not implied by having an account. | Ad, BG, FS, FE, FT | `POST /auth/field-login` (+, on the field-sync service) |
| `metadata:read` | Metadata catalog | 0025 | View the metadata catalog: classes, domains, field groups, symbology, materials. | V, A, E, S, Ad, BG | (no API mount; two manuals; three webhook assistant tools) |
| `metadata:write` | Metadata catalog | 0025 | Edit and compile the metadata catalog and symbology. | Ad, BG | `/admin` (all); `/map-settings` (W); `/private-drafts` (W); `/basemaps` (W); `/service-layers` (W); `/external-systems` (all); `/webhook-endpoints` (all); `POST /diagrams/{id}/elements/{eid}/promote` (+); UI: `/admin/feature-classes`, `/admin/domains`, `/admin/field-groups`, `/admin/materials`, `/admin/cost-catalog`, `/admin/cim-mappings`, `/admin/imports`, `/admin/diagrams`, `/admin/stencils`, `/admin/symbology-icons`, `/admin/external-systems`, `/admin/webhooks`, `/admin/map-settings` |
| `oms:read` | Outages (OMS) | 0088 | View the outage board: trouble calls, predicted and active incidents. | V, A, E, Ad, BG | `/oms` (R); UI: `/outages` |
| `oms:write` | Outages (OMS) | 0088 | Log trouble calls, run prediction, and confirm/restore/cancel incidents. | E, Ad, BG | `/oms` (W) |
| `relationships:commit` | Relationships | 0025 | Establish or revoke structural/spatial relationships. | E, S, Ad, BG | `/relationships` (W); `POST /panels` (+); `POST /rmus` (+) |
| `relationships:read` | Relationships | 0025 | View committed structural/spatial relationships. | V, A, E, S, Ad, BG | `/relationships` (R) |
| `sync:post` | Field sync | 0044 | Post (apply) or reject a reviewed field-sync op, and set the per-class apply policy. | Ad, BG | `/sync-review` (W) |
| `sync:review` | Field sync | 0044 | View the field-sync review queue -- device ops held pending a supervisor's post (QA mode). | Ad, BG | `/sync-review` (R); UI: `/admin/sync-review` |
| `tags:read` | Asset tags | 0115 | Resolve scanned asset tags and view the tags on a feature. | V, A, E, Ad, BG, FT | `/tags` (R); UI: `/t`, `/admin/asset-tags` |
| `tags:write` | Asset tags | 0115 | Register legacy tags, set the primary, void, and reissue asset tags. | E, Ad, BG | `/tags` (W) |
| `territories:unscoped` | Territories | 0094 | Bypass territory scoping -- see and edit every feature regardless of territory grants. | Ad, BG | (no route: removes the territory predicate) |
| `territories:write` | Territories | 0031 | Create, rename, re-parent, and delete territories, and edit their boundaries. | Ad, BG | `/territories` (W); `PUT /territories/enforcement`; `POST /territories/backfill`; UI: `/admin/territories` |
| `trace:run` | Trace & analysis | 0025 | Run network traces (isolation, feeder, section). | A, E, S, Ad, BG | `/trace` (all) |
| `wam:read` | Work & assets (WAM) | 0097 | View the work board: defects, work orders, inspections and maintenance. | V, A, E, Ad, BG, FT | `/wam` (R); UI: `/works` |
| `wam:write` | Work & assets (WAM) | 0097 | Log defects, raise/schedule/assign work orders, and record completion. | E, Ad, BG, FT | `/wam` (W) |

Holder codes: V viewer, A analyst, E editor, S supervisor, Ad admin, BG break-glass-admin, FS field-surveyor, FE field-enumerator, FT field-technician.

### 4.1 Reading the catalog

- **`:read` / `:write` / `:commit` pairs.** Mixed mounts use a single method-aware gate: GET/HEAD/OPTIONS need the read key, every other method needs the write (or commit) key.
- **`:run`** (`trace:run`, `analysis:run`) gates "pure action" engines whose POSTs are queries. The same key serves every method.
- **`metadata:write` covers a lot.** It is the gate for the entire `/admin` tree (feature classes, domains, field groups, materials, symbology, CIM mappings, import profiles, stencils...) for **both read and write**, and for map defaults, basemaps, service layers, external systems and webhook endpoints. `metadata:read` is held by viewer/analyst/editor but gates no API mount today; it gates the documentation entries for two manuals (CIM export and Symbology) and three read-only webhook tools in the assistant/MCP tool registry.
- **`admin:users` is wider than "users".** It also gates the runtime log-level control (`/log-settings`), the Admin > Logging page, and the operations manuals in the docs viewer (admin/break-glass, logging, deployment and scaling, production setup, Kubernetes/Coolify, and this guide).
- **`territories:write` vs `admin:users`.** Territory *authoring* (the tree, boundaries, backfill, the enforcement flip) is `territories:write`. *Granting a territory to a user* and *setting a manager* are `admin:users`. They are different people's jobs on purpose.
- **`territories:unscoped` is a bypass, not a grant.** Holding it removes the territory predicate entirely (admin and break-glass-admin only).
- **`features:wipe`** is an in-handler check on top of `features:write` for the bulk class wipe and purge. Editors are refused.

### 4.2 Permission to capability, in plain words

| If you hold... | ...you can | ...but not |
| --- | --- | --- |
| `features:read` | See the map, open attributes, list classes, export, request tiles, run BOQ, read territories and circuits | change anything; run a trace |
| `features:write` | Create/edit/delete/publish features, run imports, place panels/RMUs/assemblies, manage feature links, mark ownership | commit connectivity; wipe a class |
| `connectivity:commit` + `relationships:commit` | Establish/revoke topology and structural relationships | nothing else — pair with `features:write` to place composite assemblies (RMU needs all three) |
| `trace:run` / `analysis:run` | Run traces / power-flow, fault-current, ampacity... | write data |
| `metadata:write` | Open every Admin metadata page; author classes, domains, symbology; promote a diagram stencil; flip the private-drafts switch | manage users |
| `admin:users` | Everything under `/auth/admin` (users, roles list, territory grants, manager), log level, the operations manuals | edit the metadata catalog |
| `admin:auth-settings` | Show the local-auth switch card in the UI and flip it | (a break-glass account can flip it without this) |

---
## 5. From a request to a permission: the route map

Every mount in `api/cmd/server/main.go` is wrapped in one of four gates. This chapter lists them all, then the sub-routes that add something, then the web pages.

### 5.1 The four gate shapes

| Shape | Written as | Behaviour |
| --- | --- | --- |
| **Method-aware** | `RequirePermission(readPerm, writePerm)` | GET/HEAD/OPTIONS -> `readPerm`; every other method -> `writePerm`. No session -> 401; missing permission -> 403 with the message `you do not have the required permission (<key>)`. |
| **Single** | `RequirePermission(p, p)` | The same permission for every method. Used for query engines (`/trace`, `/analysis`, `/export`, `/tiles`, `/boq`) and wholly-admin trees (`/admin`, `/external-systems`, `/webhook-endpoints`, `/field-packs`, `/log-settings`). |
| **Authenticated only** | `RequireAuth` | Any valid session; finer checks happen inside (`/assistant`, `/docs`). |
| **Public** | none | `/healthz`, `/webhooks` (own credential), and `/auth` login/SSO/provider endpoints. |

### 5.2 Every mount

| Mount | Shape | Read (GET/HEAD/OPTIONS) | Write (other methods) | Notes |
| --- | --- | --- | --- | --- |
| `/healthz` | public | none | none | DB reachability only; probes use it |
| `/webhooks` | public | none | none | Public receiver: per-endpoint HMAC or bearer, then runs as the endpoint service user; per-IP limiter 300/min burst 100, 1 MiB body cap |
| `/feature-classes` | single | `features:read` | `features:read` | also /symbology/icons and /symbology/stencils (same gate) |
| `/features` | method-aware | `features:read` | `features:write` | POST /query and /aggregate are POSTs, so they need features:write |
| `/imports` | method-aware | `features:read` | `features:write` | profile authoring is separate (metadata:write under /admin) |
| `/export` | single | `features:read` | `features:read` | POST, but a read; scope + draft filtered |
| `/tiles` | single | `features:read` | `features:read` | in-process MVT; territory + draft overlay aware |
| `/territories` | method-aware | `features:read` | `territories:write` | /enforcement, /backfill also territories:write on PUT/POST |
| `/circuits` | method-aware | `features:read` | `circuits:write` | POST /{id}/assign additionally needs features:write |
| `/ownership` | single | `features:write` | `features:write` | both verbs |
| `/oms` | method-aware | `oms:read` | `oms:write` |  |
| `/wam` | method-aware | `wam:read` | `wam:write` |  |
| `/faults` | method-aware | `faults:read` | `faults:write` |  |
| `/feature-registry` | single | `features:read` | `features:read` | scope + draft filtered id typeahead |
| `/tags` | method-aware | `tags:read` | `tags:write` | resolve is scope/draft-aware (404 if hidden) |
| `/attachments` | method-aware | `attachments:read` | `attachments:write` | writes also RequireCSRF; feature must be readable (scope+draft) |
| `/connectivity` | method-aware | `connectivity:read` | `connectivity:commit` |  |
| `/relationships` | method-aware | `relationships:read` | `relationships:commit` |  |
| `/panels` | method-aware | `features:read` | `features:write` | inner relationships:commit |
| `/rmus` | method-aware | `features:read` | `features:write` | inner relationships:commit + connectivity:commit |
| `/packaged-substations` | method-aware | `features:read` | `features:write` | inner relationships:commit + connectivity:commit |
| `/assemblies` | method-aware | `features:read` | `features:write` | inner relationships:commit + connectivity:commit |
| `/ducts` | method-aware | `features:read` | `features:write` | inner relationships:commit |
| `/couplers` | method-aware | `features:read` | `features:write` | inner connectivity:commit |
| `/ties` | method-aware | `features:read` | `features:write` | inner connectivity:commit |
| `/feature-links` | method-aware | `features:read` | `features:write` |  |
| `/diagrams` | method-aware | `diagrams:read` | `diagrams:write` | DELETE /{id} also diagrams:delete; promote + replace-from-map also metadata:write |
| `/map-annotations` | method-aware | `annotations:read` | `annotations:write` |  |
| `/trace` | single | `trace:run` | `trace:run` | POSTs are queries; scope filtered |
| `/analysis` | single | `analysis:run` | `analysis:run` | scope + draft filtered |
| `/boq` | single | `features:read` | `features:read` | read-only |
| `/assistant` | RequireAuth | session only | session only | RequireAuth; each action re-checks the commit permissions in-handler; /chat and /execute add RequireCSRF |
| `/docs` | RequireAuth | session only | session only | RequireAuth; per-document RequiredPerm decides list + fetch (BreakGlass column also passes canSee) |
| `/admin` | single | `metadata:write` | `metadata:write` | read AND write: the whole metadata catalog is admin-only |
| `/map-settings` | method-aware | `features:read` | `metadata:write` |  |
| `/log-settings` | single | `admin:users` | `admin:users` | both verbs |
| `/private-drafts` | method-aware | `features:read` | `metadata:write` | PUT /enforcement flips the draft switch |
| `/basemaps` | method-aware | `features:read` | `metadata:write` |  |
| `/service-layers` | method-aware | `features:read` | `metadata:write` |  |
| `/external-systems` | single | `metadata:write` | `metadata:write` | both verbs |
| `/webhook-endpoints` | single | `metadata:write` | `metadata:write` | both verbs |
| `/field-packs` | single | `admin:field-packs` | `admin:field-packs` | both verbs + RequireCSRF |
| `/sync-review` | method-aware | `sync:review` | `sync:post` | RequireCSRF on mutations |
| `/auth/*` | custom | see 5.5 | see 5.5 | login, providers, OIDC are public; logout/me need a session; `/auth/admin/*` is `admin:users` (+CSRF on mutations) |

### 5.3 The method rule has consequences

Because the gate picks the permission from the HTTP **method**, not from the *intent* of the call:

- **Read-only POSTs on a method-aware mount require the write permission.** In the features mount, `POST /features/{code}/query` and `POST /features/{code}/aggregate` are searches, but by code reading they require `features:write`. A pure `viewer` therefore receives 403 on them. (This is derived from reading `RequirePermission` and `crud.Routes`; it has not been exercised against a running server in this review. If viewers must run the query panel, a dedicated gate or an exemption is a code change, not a data change.)
- **`POST /oms/incidents/predict`** runs a prediction but needs `oms:write` for the same reason.
- **Pure engines are the exception on purpose.** `/trace`, `/analysis`, `/export`, `/tiles`, `/boq` use a single permission so POSTs that are queries need only the read-level capability.
- **`OPTIONS` and `HEAD`** are treated as reads.

### 5.4 Routes that add a second requirement

These routes pass the mount gate and then demand more. The second check is either another `RequirePermission` on a sub-route or an in-handler `Has()`.

| Route | Mount gate | Additional requirement | Where |
| --- | --- | --- | --- |
| `POST /circuits/{id}/assign` | `circuits:write` (POST) | `features:write` | `circuit/http.go` |
| `DELETE /diagrams/{id}` | `diagrams:write` | `diagrams:delete` | `diagram/handler.go` |
| `POST /diagrams/{id}/elements/{eid}/promote` | `diagrams:write` | `metadata:write` | `diagram/handler.go` |
| `POST /diagrams/{id}/placements/{pid}/replace-from-map` | `diagrams:write` | `metadata:write` | `diagram/handler.go` |
| `POST /diagrams/{id}/placements/{pid}/sectionalize` | `diagrams:write` | `features:write`, and `connectivity:commit` when wiring a sectionalizer | `diagram/sectionalize.go` |
| `POST /panels` | `features:write` | `relationships:commit` | `panel/http.go` |
| `POST /rmus`, `/packaged-substations`, `/assemblies` | `features:write` | `relationships:commit` **and** `connectivity:commit` | `rmu/`, `packagedsub/`, `assembly/` |
| `POST /ducts` | `features:write` | `relationships:commit` | `duct/http.go` |
| `POST /couplers`, `POST /ties` | `features:write` | `connectivity:commit` | `coupler/`, `tie/` |
| `GET/POST /features/{code}/counts|wipe|purge-deleted` | `features:read` / `features:write` | `features:wipe` (in-handler) | `crud/handler.go` |
| `POST /auth/settings/local-auth` | session + CSRF | `admin:auth-settings` **or** `users.break_glass` | `auth/login.go` |
| `POST /sync-review/...` | `sync:review` (GET) | `sync:post` for post/reject/resolve/policy | `fieldpack/review.go` |
| `POST /webhooks/...` (ingestion) | public | the **service user's** `wam:write` / `faults:write` for those adapters; crud territory scope for features | `webhook/util.go` |

The assistant (`/assistant`) is `RequireAuth` only; each *tool* it can call declares a `RequiredPerm` (and `ExtraPerms` for composite ones) that `agenttools.Execute` checks against the acting user, so an assistant action can never exceed the user's own permissions. The MCP server uses the same registry with a bearer-token user.

### 5.5 The route tester's reference table

The table below is the data behind the "request tester" widget of the interactive edition. `Kind` is `M` (method-aware), `S` (single), `AUTH` (session only) or `PUB` (public). `Scope` shows which territory/draft logic applies after RBAC: `r` read predicate, `w` write guard, `x` export intersection, `t` trace (start gate and result filter), `a` analysis (start gate only), `tile` tile predicate, `i` import snapshot.

| Method | Path | Requires (all of) | Scope | CSRF | Note |
| --- | --- | --- | --- | --- | --- |
| GET | `/features/{code}` | `features:read` | r |  | list; scopeWhere adds territory_id IN (...) or 1=0; draft predicate |
| GET | `/features/{code}/{id}` | `features:read` | r |  | GET by id; out-of-scope = 404, not 403 |
| POST | `/features/{code}/query` | `features:write` | r |  | read-only query but POST: needs features:write (code reading; not runtime-tested) |
| POST | `/features/{code}/aggregate` | `features:write` | r |  | read-only aggregate but POST: needs features:write |
| GET | `/features/{code}/{id}/history` | `features:read` | r |  | sub-resource: loads parent and applies scope + draft checks |
| POST | `/features/{code}` | `features:write` | w |  | create; territory.Assign decides territory_id |
| PATCH | `/features/{code}/{id}` | `features:write` | w |  | update; may be staged as a private pending edit |
| DELETE | `/features/{code}/{id}` | `features:write` | w |  | soft delete |
| POST | `/features/{code}/{id}/publish` | `features:write` | w |  | draft -> published |
| POST | `/features/{code}/{id}/apply-pending` | `features:write` | w |  | apply the actor's own staged edit |
| GET | `/features/{code}/counts` | `features:read` + `features:wipe` |  |  | bulk-wipe confirm counts |
| POST | `/features/{code}/wipe` | `features:write` + `features:wipe` |  |  | soft-delete every in-scope row |
| DELETE | `/features/{code}/{id}/purge` | `features:write` | w |  | permanent delete of one row |
| POST | `/features/{code}/purge-deleted` | `features:write` + `features:wipe` |  |  | permanently remove soft-deleted rows |
| GET | `/feature-classes` | `features:read` |  |  | class catalog (every map user) |
| GET | `/feature-registry` | `features:read` | r |  | id typeahead; scope + draft filtered |
| POST | `/imports/dry-run` | `features:write` | i |  | pre-flight; scope snapshotted at enqueue |
| POST | `/imports/jobs` | `features:write` | i |  | runs through the validated crud path with the caller's snapshot |
| POST | `/export` | `features:read` | x |  | clipped to the caller's territories (intersected) |
| GET | `/export/jobs/{id}/download` | `features:read` |  |  | presigned download |
| GET | `/tiles/{code}/{z}/{x}/{y}` | `features:read` | tile |  | MVT per request; own pending edits overlaid for the author |
| GET | `/tiles/catalog` | `features:read` |  |  |  |
| POST | `/trace` | `trace:run` | t |  | start node must be in scope or 404; results filtered |
| POST | `/trace/feeder` | `trace:run` | t |  | synthetic traces use the same start gate |
| POST | `/analysis/load-flow` | `analysis:run` | a |  | start feature must be in scope (404); results are NOT filtered |
| POST | `/analysis/fault-current` | `analysis:run` | a |  |  |
| POST | `/boq` | `features:read` | r |  | priced schedule; read-only |
| GET | `/connectivity` | `connectivity:read` |  |  |  |
| POST | `/connectivity` | `connectivity:commit` |  |  | establish a connection |
| DELETE | `/connectivity/{id}` | `connectivity:commit` |  |  |  |
| GET | `/relationships` | `relationships:read` |  |  |  |
| POST | `/relationships` | `relationships:commit` |  |  |  |
| POST | `/rmus` | `features:write` + `relationships:commit` + `connectivity:commit` |  |  | place one RMU + wiring (3 capabilities) |
| POST | `/panels` | `features:write` + `relationships:commit` |  |  | place a switchgear panel |
| POST | `/couplers` | `features:write` + `connectivity:commit` |  |  | bus coupler |
| GET | `/diagrams` | `diagrams:read` |  |  |  |
| POST | `/diagrams` | `diagrams:write` |  |  |  |
| DELETE | `/diagrams/{id}` | `diagrams:write` + `diagrams:delete` |  |  | escalated: supervisor/admin only |
| POST | `/diagrams/{id}/elements/{eid}/promote` | `diagrams:write` + `metadata:write` |  |  | stencil -> real feature |
| GET | `/map-annotations` | `annotations:read` |  |  |  |
| POST | `/map-annotations` | `annotations:write` |  |  |  |
| GET | `/territories` | `features:read` |  |  | whole tree (unscoped list; any features:read) |
| GET | `/territories/mine` | `features:read` |  |  | filtered to caller scope only when enforcement is on |
| GET | `/territories/boundaries` | `features:read` |  |  |  |
| POST | `/territories` | `territories:write` |  |  |  |
| PATCH | `/territories/{id}` | `territories:write` |  |  |  |
| DELETE | `/territories/{id}` | `territories:write` |  |  | leaf only |
| PUT | `/territories/enforcement` | `territories:write` |  |  | the rollout flip |
| POST | `/territories/backfill` | `territories:write` |  |  |  |
| GET | `/private-drafts/enforcement` | `features:read` |  |  |  |
| PUT | `/private-drafts/enforcement` | `metadata:write` |  |  | retroactively privatises never-published features |
| GET | `/circuits` | `features:read` |  |  |  |
| POST | `/circuits` | `circuits:write` |  |  |  |
| POST | `/circuits/{id}/assign` | `circuits:write` + `features:write` |  |  | needs circuits:write AND features:write |
| GET | `/oms/incidents` | `oms:read` |  |  |  |
| POST | `/oms/trouble-calls` | `oms:write` |  |  |  |
| POST | `/oms/incidents/predict` | `oms:write` |  |  | a POST: needs oms:write |
| GET | `/wam/work-orders` | `wam:read` |  |  |  |
| POST | `/wam/defects` | `wam:write` |  |  |  |
| GET | `/faults` | `faults:read` |  |  |  |
| POST | `/faults` | `faults:write` |  |  |  |
| GET | `/tags/{code}` | `tags:read` | r |  | resolve a scanned code; hidden = 404 |
| POST | `/tags/{code}/void` | `tags:write` |  |  |  |
| GET | `/attachments/{code}/{id}` | `attachments:read` | r |  | feature must be readable by the caller |
| POST | `/attachments/{code}/{id}` | `attachments:write` | r | yes |  |
| GET | `/admin/feature-classes` | `metadata:write` |  |  | even reads need metadata:write |
| POST | `/admin/feature-classes` | `metadata:write` |  |  |  |
| PATCH | `/admin/domains/{id}` | `metadata:write` |  |  |  |
| GET | `/map-settings` | `features:read` |  |  |  |
| PUT | `/map-settings` | `metadata:write` |  |  |  |
| GET | `/basemaps` | `features:read` |  |  |  |
| POST | `/basemaps` | `metadata:write` |  |  |  |
| GET | `/external-systems` | `metadata:write` |  |  |  |
| GET | `/webhook-endpoints` | `metadata:write` |  |  |  |
| GET | `/log-settings` | `admin:users` |  |  |  |
| PUT | `/log-settings` | `admin:users` |  |  |  |
| GET | `/field-packs` | `admin:field-packs` |  |  | admin list of every user's packs |
| POST | `/field-packs/{id}/disable` | `admin:field-packs` |  | yes |  |
| GET | `/sync-review` | `sync:review` |  |  |  |
| POST | `/sync-review/{id}/post` | `sync:post` |  | yes |  |
| POST | `/assistant/chat` | session |  | yes | RequireAuth only; tools gated per RequiredPerm inside |
| GET | `/docs` | session |  |  | catalog filtered by each doc's RequiredPerm |
| GET | `/healthz` | none (public) |  |  |  |
| GET | `/auth/providers` | none (public) |  |  | login screen: SSO buttons + local_auth_enabled |
| POST | `/auth/login` | none (public) |  |  | per-IP limiter 10 burst / 10 per min; 64 KiB body cap; lockout per account |
| GET | `/auth/oidc/{provider}/login` | none (public) |  |  |  |
| GET | `/auth/oidc/{provider}/callback` | none (public) |  |  | protected by state + nonce + PKCE, not RequireCSRF |
| GET | `/auth/me` | session |  |  |  |
| POST | `/auth/logout` | session |  | yes |  |
| POST | `/auth/settings/local-auth` | session + (`admin:auth-settings` or break_glass) |  | yes | admin:auth-settings OR users.break_glass (checked in the handler) |
| GET | `/auth/admin/users` | `admin:users` |  |  |  |
| GET | `/auth/admin/roles` | `admin:users` |  |  | read-only list: there is no role create/edit endpoint |
| POST | `/auth/admin/users` | `admin:users` |  | yes |  |
| PATCH | `/auth/admin/users/{id}` | `admin:users` |  | yes | display name only; email immutable |
| POST | `/auth/admin/users/{id}/roles` | `admin:users` |  | yes |  |
| DELETE | `/auth/admin/users/{id}/roles/{role}` | `admin:users` |  | yes |  |
| POST | `/auth/admin/users/{id}/status` | `admin:users` |  | yes | disable revokes sessions; cannot disable yourself |
| POST | `/auth/admin/users/{id}/password` | `admin:users` |  | yes | min 12 chars; does not revoke sessions; not audited |
| DELETE | `/auth/admin/users/{id}/identities/{provider}` | `admin:users` |  | yes |  |
| POST | `/auth/admin/users/{id}/territories` | `admin:users` |  | yes | InvalidateAll after write |
| DELETE | `/auth/admin/users/{id}/territories/{tid}` | `admin:users` |  | yes |  |
| POST | `/auth/admin/users/{id}/manager` | `admin:users` |  | yes | cycle guard |

### 5.6 Web pages and the permission they need

The SPA mirrors the server gates to hide what you cannot use. It is a convenience: a user who bypasses the UI still meets the server gate.

| Page | Needs | Note |
| --- | --- | --- |
| `/login` | anyone |  |
| `/map` | signed-in session |  |
| `/features` | signed-in session | (sidebar item shows with features:read) |
| `/docs` | signed-in session | per-document gates |
| `/imports` | `features:write` |  |
| `/outages` | `oms:read` |  |
| `/works` | `wam:read` |  |
| `/faults` | `faults:read` |  |
| `/t` | `tags:read` | resolve tag |
| `/admin` | signed-in session | landing page shows only the cards the caller may use |
| `/admin/feature-classes` | `metadata:write` |  |
| `/admin/domains` | `metadata:write` |  |
| `/admin/field-groups` | `metadata:write` |  |
| `/admin/materials` | `metadata:write` |  |
| `/admin/cost-catalog` | `metadata:write` |  |
| `/admin/cim-mappings` | `metadata:write` |  |
| `/admin/imports` | `metadata:write` | import profiles |
| `/admin/diagrams` | `metadata:write` |  |
| `/admin/stencils` | `metadata:write` |  |
| `/admin/symbology-icons` | `metadata:write` |  |
| `/admin/external-systems` | `metadata:write` |  |
| `/admin/webhooks` | `metadata:write` |  |
| `/admin/map-settings` | `metadata:write` | default view, basemaps, service layers |
| `/admin/asset-tags` | `tags:read` |  |
| `/admin/territories` | `territories:write` |  |
| `/admin/circuits` | `circuits:write` |  |
| `/admin/users` | `admin:users` |  |
| `/admin/logging` | `admin:users` |  |
| `/admin/field-packs` | `admin:field-packs` |  |
| `/admin/sync-review` | `sync:review` |  |

Two UI details worth knowing: the **local-auth switch card** on Admin > Users appears only when the caller holds `admin:auth-settings` (a break-glass account *without* that permission can flip the switch through the API but will not see the card), and the **territory panel's "Edit boundary"** button needs `territories:write`.

---

## 6. Authentication: sessions, cookies and CSRF

### 6.1 What a login produces

A successful local login (or SSO callback) mints a server-side **session**:

- 256 bits from `crypto/rand`, URL-safe base64, as the cookie value. Only its **SHA-256** is stored (`token_sha256`); a database leak yields no usable cookie.
- A second random value, the per-session **CSRF token**, stored on the row and mirrored into a readable cookie.
- Two clocks: `idle_expires_at` (now + `SESSION_IDLE_TIMEOUT`, default **12h**) and `absolute_expires_at` (now + `SESSION_ABSOLUTE_MAX`, default **720h = 30 days**).
- The client's `ip` and `user_agent` (the IP is the first `X-Forwarded-For` value when present, else the peer address — see appendix E).

### 6.2 Cookies

| Cookie | Default name (env) | HttpOnly | SameSite | Secure | Path | MaxAge |
| --- | --- | --- | --- | --- | --- | --- |
| Session | `ve_session` (`SESSION_COOKIE_NAME`) | yes | Strict | yes, unless `AUTH_INSECURE_COOKIES=1` | `/` | `SESSION_ABSOLUTE_MAX` |
| CSRF | `ve_csrf` (`CSRF_COOKIE_NAME`) | **no** (the SPA reads it) | Strict | same | `/` | same |
| SSO flow | `ve_oidc_flow` | yes | **Lax** (must survive the IdP redirect) | same | `/auth/oidc` | 10 minutes |

`Secure` defaults to **on**; only `AUTH_INSECURE_COOKIES=1`/`true` removes it. The repository's `.env.example` ships it set to `1` for local development — never carry that to a networked host.

### 6.3 Validity and expiry

On every request `loadSession` hashes the presented cookie and accepts it only if **all** of these hold:

1. a row with that hash exists;
2. `revoked_at IS NULL`;
3. `now <= idle_expires_at` **and** `now <= absolute_expires_at`;
4. the user row exists and `status = 'active'`.

Any failure yields an *anonymous* request, not an error (so an expired cookie produces 401s on gated routes, never a 500). A valid use slides the idle window forward to `now + idle`, **capped at the absolute expiry**, as a best-effort update. A permission-load or scope-load database error, by contrast, *fails the request* rather than silently authorising nothing (fail closed).

Ways a session ends: logout (revokes that session), the administrator disables the user (revokes all of the user's sessions), idle expiry, absolute expiry. **Not** ways a session ends: a password change (no revocation), a role change (not needed — permissions are recomputed on each request), and a territory change (cache flushed, scope recomputed on the next request).

### 6.4 CSRF: where it is actually enforced

`RequireCSRF` runs two checks on non-safe methods for an authenticated request: if `Sec-Fetch-Site` is present it must be `same-origin` or `none`; and the `X-CSRF-Token` header must equal the session's token (constant-time compare). Anonymous requests pass through (nothing to forge). Note what it does **not** check: the `Origin` header is not inspected by this middleware.

It is applied to a **specific set of surfaces**, not to every mutating route:

| Surface | CSRF middleware |
| --- | --- |
| `POST /auth/logout`, `POST /auth/settings/local-auth` | yes |
| `/auth/admin/*` mutations | yes |
| `/attachments` POST/DELETE | yes |
| `/assistant/chat`, `/assistant/execute` | yes |
| `/field-packs` (admin) and `/sync-review` mutations | yes |
| `/features`, `/connectivity`, `/relationships`, `/territories`, `/admin`, `/imports`, `/diagrams`, `/circuits`, ... | **no** — protected by the `SameSite=Strict` session cookie alone |

The SPA echoes `X-CSRF-Token` on every mutating call regardless (`apiFetch`), so enabling the middleware on more mounts needs no client change. For an auditor the point is: **cross-site request forgery resistance for the core data mounts rests on `SameSite=Strict`**, with the double-submit token as defence in depth on the administrative and attachment/assistant surfaces.

### 6.5 Response headers

`SecurityHeaders` sets on every response: `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`, `Referrer-Policy: no-referrer`, `Cross-Origin-Opener-Policy: same-origin`, and `Strict-Transport-Security: max-age=63072000; includeSubDomains` **only** when the request arrived over TLS (`r.TLS` or `X-Forwarded-Proto: https`). The API does not set a `Content-Security-Policy`; if you need one, set it at the reverse proxy.

---

## 7. Passwords, lockout and rate limits

### 7.1 Passwords

- Hash: **Argon2id**, OWASP baseline parameters m=19456 KiB, t=2, p=1, 16-byte salt, 32-byte key, PHC-encoded so parameters can be raised later without invalidating old hashes.
- Policy: **minimum 12 characters** for any password set through the admin API or `authadmin`. There is no complexity rule and no rotation or history.
- Verification is constant-ish time: an unknown email, a disabled account, an account with no local identity, a locked account and a switched-off local login all spend an Argon2 verification against a dummy hash before failing, so timing does not reveal which case applied.

### 7.2 The local-login decision, in order

`POST /auth/login` (`authenticateLocal`):

1. Require email and password (400 otherwise).
2. Look up an **active** user by email. Missing/disabled -> dummy hash, `login_failure` event, generic 401 `invalid email or password`.
3. If `locked_until` is in the future -> dummy hash, `login_failure` ("attempt while locked out"), the **same generic 401**. (A locked account does not say it is locked.)
4. Find the `local` identity. None, or no hash -> generic 401.
5. **The switch.** `allowed = user.break_glass`; if not break-glass, `allowed = local_auth_enabled`. If not allowed -> dummy hash, `login_denied`, **403** `local password sign-in is currently disabled; use SSO or contact an administrator`. This check happens *before* the password is verified, so the switch is never a password oracle.
6. Verify the password. Wrong -> `recordFailedLogin` (below) and the generic 401.
7. Success -> zero the failure counter, clear `locked_until`, log `login_success` or `break_glass_login`, mint the session.

### 7.3 Account lockout

`failed_login_count` increments on each wrong password. At the **fifth** consecutive failure (`maxFailedLogins = 5`) the account is locked for **15 minutes** (`lockoutDuration`) and the counter resets to zero; a success clears both. Observations an administrator should keep in mind:

- It is a *fixed* 15-minute lock, not escalating backoff (the design note in phase 10 describes escalation; the code does not do it).
- Lockout applies to *local password* sign-in only. An SSO sign-in or a bearer token for the same user is unaffected.
- Because the lock is per account, an attacker who knows an email can lock that person out (denial of service). The per-IP rate limit below blunts a flood.
- A break-glass account is subject to lockout like any other.
- To unlock early: `UPDATE iam.users SET failed_login_count = 0, locked_until = NULL WHERE email = '...';`

### 7.4 Rate limits

Process-local, in-memory token buckets keyed by the **transport peer address** (`RemoteAddr`), never a client-settable header:

| Limiter | Applies to | Burst | Sustained | Response |
| --- | --- | --- | --- | --- |
| Login | `POST /auth/login` (plus 64 KiB body cap) | 10 | 10 per minute | 429, `Retry-After: 60`, `{"error":"too many attempts; please wait and try again"}` |
| Field login | `POST /auth/field-login` on the field-sync service (same numbers, own instance) | 10 | 10 per minute | 429 |
| Webhooks | `/webhooks/*` (plus 1 MiB body cap) | 100 | 300 per minute | 429 |

Characteristics to plan around: buckets are per process (N replicas allow N times the rate unless the load balancer also limits); idle buckets are swept after 10 minutes; and **behind a reverse proxy every client shares the proxy's address**, so a login burst from one office can exhaust the bucket for all of them. Front the API with a proxy-level limiter that sees the real client address and treat this limiter as a backstop.

Everything else — `GET /auth/providers`, the OIDC endpoints, authenticated routes — has no application rate limit. The global request-body cap is 32 MiB.

### 7.5 What a failed login looks like to each party

| Situation | Client sees | `auth_events` row |
| --- | --- | --- |
| Unknown or disabled email | 401 `invalid email or password` | `login_failure` "unknown or disabled email: ..." (no user id) |
| Wrong password (1st-4th) | same 401 | `login_failure` "wrong password" |
| Wrong password (5th) | same 401 | `login_failure` "wrong password -- account locked" |
| Correct password while locked | same 401 | `login_failure` "attempt while locked out" |
| SSO-only user tries a password | same 401 | `login_failure` "no local password on this account" |
| Local auth off, not break-glass | 403 (disabled message) | `login_denied` |
| Burst over the limiter | 429 | none (rejected before the handler) |

---

## 8. Single sign-on (OIDC) and provisioning

### 8.1 What SSO is and is not

Two providers can be configured by environment: **Azure AD** (`azuread`, single tenant) and **Google** (`google`). Each is optional and independent; a provider that is unconfigured or whose discovery fails at API start simply does not exist (not shown on the login screen). Local-only, one provider, or both are all valid deployments.

SSO **authenticates** the person. It does **not** decide their permissions: there is **no claim-to-role mapping** anywhere in the code, no group sync, and no automatic deprovisioning (SCIM is out of scope). Roles, territories and manager are always assigned by an administrator inside VoltEdge. Likewise SSO **never creates** an account.

### 8.2 The flow

1. `GET /auth/oidc/{provider}/login` generates `state`, `nonce` and a PKCE verifier (S256), stores them in the short-lived `ve_oidc_flow` cookie (base64 JSON; httpOnly; SameSite=Lax; 10 minutes) and redirects to the provider.
2. The provider redirects to `GET /auth/oidc/{provider}/callback`. The handler clears the flow cookie, then: rejects a provider `error`; compares `state` (constant time); exchanges the code with the PKCE verifier; requires an `id_token`; verifies signature, issuer and audience with `go-oidc`; compares the `nonce`.
3. **Provider trust rules** (`validateClaims`): Google requires `email_verified`; and if `OIDC_GOOGLE_ALLOWED_DOMAIN` is set the `hd` claim **or** the email's domain must equal it. Azure applies no extra claim rule — the tenant-scoped issuer URL (`.../{OIDC_AZURE_TENANT_ID}/v2.0`) already bounds who can mint a token. (There is no separate `tid` comparison in the code.)
4. **Match or reject** (`authenticateOIDC`), never create:
   1. If `(provider, subject)` is already linked: log that user in — unless the account is disabled (403 -> `login_denied`).
   2. Otherwise take the lower-cased `email` claim, or for Azure the `preferred_username` if it contains `@`. No email -> 403.
   3. Find an **active** user with that email. None -> 403, `login_denied` "SSO rejected: no pre-provisioned account for ...".
   4. Insert the identity row (link) and log `sso_linked` then `login_success` ("first login").
5. Success mints the same session and redirects to `/`. Failures redirect to `/login?error=<reason>` with reasons `unknown_provider`, `expired`, `sso`, `state`, `denied`, `no_account`.

### 8.3 Provisioning checklist

1. Create the account with the **same email** the identity provider will assert (`POST /auth/admin/users`, no password needed for SSO-only).
2. Assign a role (and territories/manager if the crew is scoped).
3. Tell the person to sign in with the provider button. The first success links the identity.
4. To re-bind a person to a different provider account, unlink (`DELETE .../identities/{provider}`) and have them sign in again.

### 8.4 Behaviours to note

- **Link by email on first login is a trust decision.** Whoever controls an IdP account asserting a given verified email can claim the pre-provisioned VoltEdge account with that email. Restrict the Azure tenant and the Google domain accordingly.
- **Disabling a user in the IdP does nothing here.** VoltEdge only learns on the next sign-in attempt. Disable the VoltEdge account to cut access immediately (sessions are revoked on disable).
- **A user can hold both a password and an SSO identity** (one of each provider, at most one `local`). That is how a break-glass account stays SSO-normally and local-in-an-emergency.
- **SSO sign-in is not subject to the local-auth switch**, the lockout, or the login rate limiter.
- **Required environment** (from `.env.example` and `oidc.go`): `OIDC_AZURE_TENANT_ID`, `OIDC_AZURE_CLIENT_ID`, `OIDC_AZURE_CLIENT_SECRET`, `OIDC_AZURE_REDIRECT_URL`; `OIDC_GOOGLE_CLIENT_ID`, `OIDC_GOOGLE_CLIENT_SECRET`, `OIDC_GOOGLE_REDIRECT_URL`, optional `OIDC_GOOGLE_ALLOWED_DOMAIN`. A provider needs a client secret and a redirect URL or it is skipped.

---

## 9. API tokens, field logins and service identities

Browsers use cookie sessions. Everything else — AI agents, the field tool, webhook sources, background workers — authenticates differently, but always resolves to **a real user** and therefore to **that user's permissions and territory scope**. There is no ambient superuser.

### 9.1 The bearer token

- Format: `ve_mcp_` + 43 URL-safe base64 characters (256 bits). Stored as SHA-256; the first 12 characters (`token_prefix`) are kept in the clear for listings. The raw token is shown **once** at mint.
- Lifetime: optional `expires_at` (NULL = never expires); revocation sets `revoked_at` and is immediate. There is no sliding window.
- Validity (`ResolveAPIToken`): hash found, not revoked, not expired, user `active`. Anything else returns "no user" (401 at the transport).
- Resolves through the **same** permission load and scope resolver as a cookie session, so a token can never exceed its owner. It carries no session id or CSRF token (nothing to forge: it is not sent automatically by a browser).
- Disabling the owner kills the token's effect at once (the active check); it does not delete the row.

### 9.2 Who accepts a bearer token

This is the most commonly misunderstood point: **the main API server does not read `Authorization: Bearer`.** Its `SessionLoader` looks only at the session cookie. Tokens are accepted by specific services:

| Service | How it uses a token | Authorisation after |
| --- | --- | --- |
| MCP server (`cmd/mcpserver`) | `Authorization: Bearer` on each JSON-RPC call | `agenttools` `RequiredPerm` / `ExtraPerms` per tool, plus crud scope |
| Field-sync service (`cmd/fieldsync`) | `bearerAuth` middleware on `/packs`, `/sync`, `/cells`, `/attachments`, `/tags` | `features:read` for pack reads, `features:write` for `/sync`, `attachments:*` and `tags:*` method-aware |
| Webhook receiver (`/webhooks`) | One of its credential modes (9.4) | the endpoint's **service user** |
| Webhook / import / export workers | no token: they construct the user from an id (`LoadSessionUser`) | the same permission + scope chain |

An example that does not work, to avoid a wrong mental model: an agent holding Tom's token **cannot** call `POST /analysis/load-flow` on the API server directly. It can ask the MCP server, whose tool for that action is gated on `analysis:run`.

### 9.3 Minting and field logins

| Method | Command / endpoint | TTL | Audit |
| --- | --- | --- | --- |
| Operator | `authadmin mint-token --email <e> --name <label> [--ttl 720h]` | omitted or 0 = never expires | `created_by` empty (out of band); no `auth_events` row |
| Field app | `POST /auth/field-login` on the field-sync service `{email, password, device_name}` (token stored as `field-app · <device_name>`) | **0 = non-expiring** (a device must work offline for weeks) | same `authenticateLocal` events as a browser login; the token's `created_by` is the user |
| List / revoke | `authadmin list-tokens --email`, `authadmin revoke-token --email --id` | | `revoked_at` stamped |

A field login runs the *exact* local-login policy: lockout, the local-auth switch (so with local auth off only break-glass users can log in from a device), and the same events. **Then, since migration 0164, it additionally requires the `field:login` permission** — a valid credential held by a user who lacks it is refused **403** (`this account does not have field-app access`). So signing into the field app is an explicit grant held only by the three field roles plus admin/break-glass; an office-only account (viewer/analyst/editor) with perfectly good credentials cannot obtain a field-app token. A person who does both office and field work holds an office role *and* a field role (roles stack). On success the response also returns the user's effective permission set, which the device uses to show/hide its modules (the server still enforces every call independently). Because field tokens never expire by default, **revocation is the control**: when a device is lost, run `revoke-token`, and disable the user if the credential itself is suspect.

### 9.4 Webhook ingestion credentials

Each inbound endpoint (`ops.webhook_endpoints`, managed under Admin > Webhooks with `metadata:write`) has an `auth_mode`:

| Mode | Check |
| --- | --- |
| `hmac` | `X-VoltEdge-Signature` = hex(HMAC-SHA256(secret, timestamp + "." + body)); `X-VoltEdge-Timestamp` within a **5 minute** replay window. All failures return the same vague 401. |
| `bearer` | An API token whose **owner is exactly the endpoint's configured service user**; a valid token for anyone else is refused. |
| `both` | A signature header routes to HMAC, otherwise bearer. |

Every applied write runs as the endpoint's `service_user_id`, so it inherits that user's permissions **and** territory scope. The WAM and faults adapters check `wam:write` / `faults:write` explicitly because those engines do not self-check; the feature adapter relies on crud's territory enforcement. Give a service user the *narrowest* role and territories its feed needs.

### 9.5 Service identities for background work

`auth.LoadSessionUser(ctx, db, userID)` builds the same snapshot for a user id with no cookie or token. Import and export jobs capture the requester's **scope snapshot at enqueue time** (`ops.import_jobs.scope`, `ops.export_jobs.scope`) and the worker applies exactly that — it never re-derives permissions. A consequence for auditors: a job queued before a revocation still runs with the permissions and territories the requester had when they pressed the button.

---

## 10. The break-glass account

### 10.1 What the flag is

`iam.users.break_glass = true` marks an account as the emergency floor. It is **not a role and not a permission**. `Has()` does not special-case it. The flag buys exactly these behaviours:

| # | Power | Where | Effect |
| --- | --- | --- | --- |
| 1 | **Local-login bypass** | `login.go` | Ignores `local_auth_enabled`; can sign in with a password when everyone else is forced onto SSO. Logged as `break_glass_login`. |
| 2 | **Auth-settings bypass** | `setLocalAuthHandler` | May flip `local_auth_enabled` without `admin:auth-settings`, even with no role at all. |
| 3 | **Factory-reset survivor** | `cmd/resetdata`, `VerifyBreakGlassAdmin` | The only kind of account that can authorise a reset, and the sole user left afterwards. |
| 4 | **Doc catalog** | `docs.canSee` | Passes every documentation `RequiredPerm` check. |
| 5 | **Draft visibility** | `drafts.VisibleAuthors`, `OverlayAuthor` | Sees all private drafts and is never given a private pending-edit overlay (it edits public data directly) — same as `territories:unscoped` holders, but independent of the permission. |

What it does **not** buy: any route permission. An account with the flag and no role can sign in and flip the local-auth switch and read the manuals — and gets 403 on everything else. By convention such an account **also** holds the `break-glass-admin` role (permission-identical to `admin`) so it can act once in.

### 10.2 The four combinations

| `break_glass` | Role | Meaning |
| --- | --- | --- |
| no | `admin` | Ada: omnipotent over actions, but cannot sign in with a password when local auth is off, and cannot authorise a factory reset |
| yes | `break-glass-admin` | Owen: both |
| yes | none | Can get in and re-enable local auth; authorised for nothing else |
| no | none | A new account awaiting a role |

### 10.3 The local-auth switch

`iam.system_settings.local_auth_enabled` (seeded `false` by migration 0024, **not** an environment variable). A missing or unparseable value is treated as **off** (fail closed). Flip it with `POST /auth/settings/local-auth` `{enabled: bool}` (CSRF required): the handler requires `admin:auth-settings` **or** a break-glass user. Every flip logs `local_auth_toggled` with the user id and `local_auth_enabled set to true|false`.

Decision table for a password sign-in (the heart of the safety model):

| | Break-glass account | Ordinary local account |
| --- | --- | --- |
| Switch **off** | allowed | 403 disabled |
| Switch **on** | allowed | allowed |

(Subject to: active account, not locked, a local password identity exists.)

### 10.4 Creating and protecting break-glass accounts

- Bootstrap: `AUTHADMIN_PASSWORD=... authadmin create --email owen@... --name "Owen"` — **defaults** to `--break-glass=true` and `--role admin`. Pass `--break-glass=false` for an ordinary account, `--role` to choose another role, `--role=""` for none.
- In the API: `POST /auth/admin/users` with `break_glass: true` **and** a password (12+ characters).
- Keep them few, give each a unique long password held offline, alert on every `break_glass_login` event, and review who holds the flag (appendix B.5).
- There is no endpoint to turn the flag on or off for an existing user; that is SQL by design, so that granting the emergency floor is a deliberate, reviewable act.
- Lost password: `AUTHADMIN_PASSWORD=... authadmin set-password --email owen@...` against the database; it works with the API down.

### 10.5 Worked scenario: the SSO outage

1. Azure AD is down; no one can sign in. Local auth is off.
2. Owen signs in with his password (bypass #1). Audit: `break_glass_login`.
3. Owen flips local auth **on** (bypass #2 or `admin:auth-settings`). Audit: `local_auth_toggled ... true`.
4. Ordinary accounts that *have* a local password (Priya, Tom, Dana — only if they were created with one) can now sign in. SSO-only accounts cannot; they have no password. Ada can set one: `POST .../password`.
5. SSO recovers; Owen flips local auth **off**. Audit: `local_auth_toggled ... false`.
6. Reconcile: the `break_glass_login` and `local_auth_toggled` rows against the incident ticket.

### 10.6 The destructive tools

`cmd/resetdata` authenticates a break-glass account with `VerifyBreakGlassAdmin` (all failure reasons produce one opaque error), wipes operational data, **truncates `iam.auth_events`**, leaves that single break-glass user, and has a dry-run default (`--yes` to execute). **Export the audit trail first** (appendix B.9). `uncompile` drops one class's table. Both are covered in the Administration & Break-Glass Guide, chapter 6.

---
## 11. Spatial access control (territories)

RBAC answers *which verbs*. Spatial access answers *which places*. A utility does not want every `editor` able to change every asset across the service area: Dana maintains Oakwood and should neither edit nor even see a feeder three districts away.

### 11.1 The two trees

```
  TERRITORY TREE  gis.territories (cascades DOWN)        ORG TREE  iam.users.manager_id (cascades UP)

  Utility (rank 0)                                       Ada
   └─ Central London Region (1)                           └─ Tom        (manager of Dana, Carlos)
       ├─ Westminster District (2, min level)                  ├─ Dana  holds: Westminster District
       │   └─ Whitehall Area (3)                               └─ Carlos holds: City of London District
       └─ City of London District (2, min level)
```

- **Territories** form a tree of levels (`gis.territory_levels`, seeded `utility`, `region`, `district`, `area`, `plot` at ranks 0-4; **`district` is the minimum feature level**). A *grant* names one node; the node's **whole subtree** is included at resolve time, never stored. Re-parent or extend the tree and everyone's reach re-derives with no re-granting.
- **The org tree** is people: each user optionally has a manager. A manager's effective territories are the **union of every grant held by all their transitive reports**. Grant the crews at the edge; their managers inherit by virtue of the org chart.
- The two meet at the user. A user can hold grants and no reports, reports and no grants (a pure manager), both, or neither.

The seeded tree (migrations 0027 and 0030) is a **placeholder**: rectangles around central London (`Westminster District` lon -0.16..-0.11, lat 51.48..51.54; `Whitehall Area` lon -0.135..-0.12, lat 51.50..51.51, nested inside). Replace the geometry with the authoritative boundaries before turning enforcement on in anger.

### 11.2 The effective set

For a user `U`, `access.EffectiveIDs` runs one recursive query:

1. `reports`: `U` plus everyone who reports up to `U`, transitively.
2. `granted`: the distinct territory nodes any of those users were directly granted.
3. `scope`: those nodes plus all their descendants (walking `parent_id`).

`auth.loadScopeFields` calls it (through a 60-second per-user cache) for every request, **except** for holders of `territories:unscoped`, which skip the resolver and get `Unscoped = true`. It also resolves `Subordinates` (reports excluding self) for the draft predicate.

**Worked example.** Tom manages Dana and Carlos. Dana holds *Westminster District*; Carlos holds *City of London District*; Tom holds nothing.

- Resolving **Dana**: `reports` = {Dana}; `granted` = {Westminster}; `scope` = Westminster + Whitehall.
- Resolving **Tom**: `reports` = {Tom, Dana, Carlos}; `granted` = {Westminster, City of London}; `scope` = both districts + Whitehall.
- Resolving **a user with no grants and no reports**: the empty set — and *empty means "match nothing"*, never "no filter".

**Cache.** TTL 60 seconds as a backstop; every grant, revoke and manager change calls `access.InvalidateAll()` after the write, flushing both the territory cache and the subordinate cache for **all** users (a manager change ripples through whole subtrees, so partial invalidation is deliberately avoided). The cache is per process: with several API replicas a replica other than the one that served the admin call can serve a stale set for up to 60 seconds. See appendix E.

### 11.3 How a feature acquires a territory

Every geometric feature has a nullable `territory_id` (a plain uuid, not a foreign key). It is assigned at write time by `territory.Assign`:

- **Automatic**: the deepest territory whose boundary **contains the feature's centroid** (`ST_Contains(boundary, ST_Centroid(...))`, geometry transformed to 4326). A line or polygon straddling a boundary therefore resolves to exactly one territory — whichever contains its centroid. A qualifying match (rank >= the minimum level) is stored **even when enforcement is off**: that is how coverage fills up before the flip.
- **Override**: a caller may supply `territory_id`. It must exist and be at least the minimum level, **always** (independent of the flag), but it is **not** required to contain the feature (the escape hatch for a missing or imperfect boundary).
- **The failure case**: no territory covers the centroid, or only a too-shallow one does. With enforcement **on** the write is rejected (400, for example `this location isn't within any "district" -- no territory covers it`); with enforcement **off** the feature saves with `territory_id = NULL`.
- Geometry-less classes have no `territory_id` column and are never scoped.

Editing a territory boundary never silently relabels placed features; reassignment is an explicit act (backfill, or editing the feature).

### 11.4 The master switch and what it gates

`iam.system_settings.territory_enforcement_enabled` (seeded `false` by migration 0029; missing or unparseable is also off). **Every** read/write/tile/trace/export predicate below is a no-op unless it is `true`. So a fresh install behaves exactly as if this chapter did not exist.

Even with the switch on, the predicate is skipped when: the user holds **`territories:unscoped`**; the class is geometry-less; or there is no session on the context (an internal, non-HTTP caller — a trusted code path, never a user request).

### 11.5 Enforcement, surface by surface

| Surface | Behaviour when enforced and the user is scoped | Source |
| --- | --- | --- |
| **List / query / aggregate** (`GET /features/{code}`, `POST .../query`, `.../aggregate`, BOQ) | Adds `territory_id::text IN (<set>)`; an **empty set becomes `1 = 0`** (match nothing). Unassigned (`NULL`) features are invisible. | `crud/scope.go` `scopeWhere` |
| **Get by id** and publish/unpublish/restore | A feature outside the set, or with no territory, answers **404** (existence hidden). | `enforceReadScope` |
| **Sub-resources** (history, GPS captures, capacity, attachments, tag resolve, feature registry) | Load the parent feature and apply the same territory + draft checks. A *purged* feature's history still returns (documented residual). | `enforceSubResourceReadScope` |
| **Update / delete / publish / unpublish of an existing feature** | Same check as a read: outside the set (or unassigned) answers **404** before anything else runs. | `enforceReadScope` (update.go, delete.go, publish.go) |
| **Create, and any update that sets geometry or `territory_id`** | The *destination* territory (`Assign` result) must be in the set, else **403** `you don't have access to the territory this feature belongs to`. A geometry-less feature (no territory) passes. | `enforceWriteScope` |
| **Map tiles** | `/tiles/{code}/{z}/{x}/{y}` appends the same predicate; empty set -> empty tile. Rendered per request behind the session (never a shared cache), so a scoped user is never sent geometry outside their territories. | `tiles/tiles.go` |
| **Export** | A requested territory filter is **intersected** with the user's set (silently dropping others); no filter means *the user's whole set*, never everything. Empty -> nothing exported. | `export/export.go` `enforceExportScope` |
| **Trace** | The start node must be in scope (else 404); traversal may pass through, but returned nodes (features, barriers, targets) are **filtered** to in-scope ones. Geometry-less devices are always permitted. | `trace/scope.go` |
| **Analysis** (load flow, fault current...) | **Start gate only**: an out-of-scope start feature is 404; results are **not** filtered, because filtering would corrupt the physics (understated demand). By product decision. | `powerflow/scope.go` |
| **Imports** | The caller's scope is captured in the job snapshot at enqueue; the worker writes through the validated crud path as that scope. | `importer/http.go`, `importer/load.go` |
| **Field packs** | Pack contents are scoped to the owner; shared-cell pickup needs the cell's territory to lie inside the crew member's set (a cell with no territory is for unscoped members only while enforcement is on). | `fieldpack/cellfetch.go` |
| **Webhook ingestion** | Writes run as the endpoint's service user and so under that user's scope. | `webhook/` |
| **`GET /territories`** | **Not scoped**: any `features:read` user can list the whole tree (names and levels). `GET /territories/mine` filters to the caller's set when enforcement is on. | `territory/http.go` |
| **Operational records** (`/oms`, `/faults`, `/wam`) | **Not enforced — ever.** These schemas have **no `territory_id`** and apply no territory predicate; `territories:unscoped` changes nothing for them. Every `oms:read` / `faults:read` / `wam:read` holder sees **all** outages, faults and works records, organisation-wide. | `oms/`, `faults/`, `wam/` (no territory refs) |
| **Graph structure** (`/connectivity`, `/relationships`, `/diagrams`, `/annotations`) | **Not enforced.** Connectivity and diagrams carry no territory gate; `GET /relationships` explicitly **defers** scoping (it returns only id/class/name — a documented existence leak); map annotations *stamp* a `territory_id` via `Assign` but don't restrict reads or writes to your grants. | `connectivity/`, `relationship/sql.go`, `diagram/`, `mapannotation/` |

> **What scoping covers — and what it doesn't.** Territory scoping is a property of the **feature layer** and the things that read it: list/query/aggregate/BOQ, get-by-id and sub-resources, writes, **tiles, trace, export, imports and field packs** — plus the **start feature** of an analysis (the walk itself isn't filtered, §11.5 *Analysis* row). It is **not** applied to the **operational-record modules** (OMS outages, Faults, WAM works) or the pure **graph-structure** endpoints. That's deliberate, not an oversight: an outage, a fault or a defect is an operational *record about* assets, not a spatial feature — it has no `territory_id` to filter on, and a control room / maintenance desk is expected to see the whole organisation's queue. If you need those boards partitioned by area, that's an application-level concern today, not something Phase 58 does. *(The Faults, OMS and Works guides each flag this in their own ⚠️; this row is the single source of truth.)*

### 11.6 Rolling it out safely

Scoping ships off, and the order matters:

1. **Author the tree and boundaries** (Admin > Territories; `territories:write`). Check each child lies within its parent and has the right rank.
2. **Backfill** existing features (`POST /territories/backfill`, `territories:write`): stamps `territory_id` on still-unassigned live features by the same point-in-polygon rule. Safe to re-run. A greenfield install skips it.
3. **Review coverage** (`GET /territories/coverage`): per-class counts of features still unassigned. Fix boundaries or assign manually.
4. **Grant** crews (`admin:users`): territories to the people at the edge, managers via the org tree.
5. **Flip** (`PUT /territories/enforcement` `{enabled:true}`, `territories:write`). It does **not** check coverage: flipping with stragglers makes them invisible to scoped users until assigned. The flip is *not* audited in `auth_events` (appendix E).
6. **Verify** as a scoped test user: list, get a known out-of-territory id (expect 404), write outside (expect 403), request a tile outside (expect empty).

To roll back: `PUT /territories/enforcement {enabled:false}` — immediate (no cache on the flag itself).

### 11.7 The bypass

`territories:unscoped` (migration 0094) is held by `admin` and `break-glass-admin`. It is a **permission**, so it can be granted to a custom role — for example a read-only auditor role that sees everything but changes nothing:

```sql
-- illustrative; ship as a migration
INSERT INTO iam.roles (key, name, description, is_system)
VALUES ('auditor','Auditor','Read everything everywhere; change nothing.', false);
INSERT INTO iam.role_permissions (role_id, permission_key)
SELECT r.id, p.key FROM iam.roles r, iam.permissions p
WHERE r.key = 'auditor' AND p.key IN
 ('features:read','connectivity:read','relationships:read','diagrams:read','annotations:read',
  'metadata:read','oms:read','wam:read','faults:read','tags:read','attachments:read','territories:unscoped');
```

(That role would still hit the read-only-POST caveat of chapter 5.3 for query/aggregate.)

### 11.8 Pitfalls

- **Unassigned features vanish for scoped users** once enforcement is on. Always check coverage first.
- **A scoped user with no grants sees an empty map**, not an error. That is fail-closed working as designed.
- **`GET /territories` leaks names.** The tree of names/levels is visible to every map user.
- **A manager sees what their reports see — and nothing is granted to the manager.** Removing a person from a manager's reports (changing their `manager_id`) shrinks the manager's scope on the next request.
- **Cycles** (A manages B manages A) are refused by `SetManager`.
- **A disabled report still counts.** `reports` does not filter on status, so a disabled user's grants keep flowing up to their manager until the user's `manager_id` is changed or the grant is revoked.
- **Revocation is instant on the serving process** (flush after write), within 60 seconds elsewhere.

### 11.9 Per-territory feature-class availability (distinct from user scope)

Orthogonal to *who* may work *where* (above) is *which feature classes exist where*. A feature class can be turned off for specific territories, so a utility that doesn't use a class in one region can make it unavailable there — independently of any user's grants.

- **Global off** — a class's `feature_classes.status` set to `deprecated` (the admin **Deactivate** action) removes it from the map, the New-Feature menu, the Features browser and every form, *everywhere*. The table and all rows are kept; **Reactivate** restores it. This is enforced at write time (a deactivated class accepts no new features), not just hidden in the UI.
- **Per-territory off** — a `metadata.feature_class_territory_disabled` row turns a still-active class off for one territory and, by cascade, **every territory beneath it**. On create, the feature's territory (resolved by the same point-in-polygon as §11.3) is checked, and a feature landing in a disabled territory is **refused**. Existing features are **grandfathered** (never removed). Global off always wins over any per-territory setting.

This is a *class*-level availability gate, authored on the feature class (Admin → Feature classes → the class → **Territory availability**), and it composes with — but is independent of — the user-level territory scope this chapter otherwise describes.

---

## 12. Private drafts and the pending-edit overlay

Feature data has a lifecycle: a new feature is a **draft** (`status = 'draft'`, `published_at` NULL) until someone publishes it (`status = 'published'`, `published_at` stamped and kept — a sticky marker). Phase 61 added a visibility rule on top of territories.

### 12.1 The switch

`iam.system_settings.private_drafts_enabled`. **No migration seeds this key**; a missing or unparseable value means **off**. Flip it with `PUT /private-drafts/enforcement {enabled:true}` (`metadata:write`; GET needs `features:read`). Turning it on is retroactive: every never-published feature immediately becomes private to its author chain. It is independent of territory enforcement.

### 12.2 Phase A: never-published features are private

When the switch is on, a feature is **visible** to a viewer if it is *public* (`status = 'published' OR published_at IS NOT NULL`) **or** its `created_by` is the viewer or one of the viewer's **subordinates** (transitive reports). The predicate is ANDed with the territory predicate — a draft is visible only to an eligible author **and** inside the viewer's territories.

It does not apply to: holders of `territories:unscoped`, accounts with the `break_glass` flag, or session-less internal callers. It is applied in crud (list/get/sub-resources/publish/delete), tiles, export, the feature registry, attachments and field packs.

Consequences:

- **A peer cannot see, publish or delete your draft.** Anything they request answers 404.
- **Your manager can**, because they are an ancestor of the author. That is how an orphaned draft is recovered without the break-glass account: the author's manager publishes it. (Publishing still requires the feature to be complete: required fields for its subtype must be filled.)
- **Unpublishing a published feature does not make it private again** — `published_at` stays set, so it remains public.

### 12.3 Phase B: private edits of published features

Editing a published feature is normally visible to everyone at once. With the switch on, a user who is **not** `territories:unscoped` and **not** break-glass has their edit *staged* instead (`runtime.feature_pending_edits`, migration 0112), keyed by `(class, feature, author)`. The public row is untouched.

- **Overlay is the author's own only.** The author's reads (get/list/query/tiles) show their staged values merged over the public row (attributes merged field by field; a privately moved geometry; a private delete hides the feature from them). Everyone else, including the author's manager, sees the public version. Subordinate-visible overlays are an explicit non-goal of v1.
- **Display only.** Staged values show but do not participate in `WHERE`/`ORDER BY`; a staged attribute does not re-sort or re-filter a list. Tiles overlay position and presence, not attributes.
- **Apply** (`POST /features/{code}/{id}/apply-pending`) writes the staged change to the public row inside a transaction after re-checking scope and draft rules, with a **conflict check**: if the live row's `updated_at` is newer than the `base_version` captured at first staging, the apply is refused with a conflict and the author must discard and redo. **Discard**: `DELETE /features/{code}/{id}/pending`.
- A never-published draft is edited in place (phase A already keeps it private).
- Deleting the author deletes their pending edits (`ON DELETE CASCADE`).

### 12.4 Who sees what, for auditors

| Viewer | Own draft | Subordinate's draft | Peer's draft | Published feature | Own staged edit |
| --- | --- | --- | --- | --- | --- |
| Ordinary user (switch on) | yes | yes | **no (404)** | yes (in territory) | overlaid |
| Manager of the author | yes | yes | no | yes | their own only |
| `territories:unscoped` or `break_glass` | yes | yes | **yes** | yes | no overlay (writes go direct) |
| Anyone, switch off | yes | yes | yes | yes | no overlay |

> Chapter 8.4 of the Administration & Break-Glass Guide covers day-to-day handling of drafts; this chapter is the access-control view.

---

## 13. The audit trail

### 13.1 `iam.auth_events`

Append-only: the application only ever inserts. `user_id` is nullable with `ON DELETE SET NULL` so an event outlives the account it describes. Columns: `id`, `user_id`, `event_type`, `detail`, `ip`, `user_agent`, `created_at`; indexed by `(user_id, created_at)` and `(event_type, created_at)`.

| Event | Written when | Source |
| --- | --- | --- |
| `login_success` | A normal successful sign-in (local or SSO) | `login.go authenticateLocal; oidc.go authenticateOIDC` |
| `login_failure` | Wrong password / unknown or disabled email / no local password / attempt while locked / SSO state, nonce, token or code failure | `login.go, oidc_handlers.go` |
| `login_denied` | Correct credentials but local auth off and not break-glass; SSO claim validation failed; SSO for a disabled or unprovisioned account | `login.go, oidc.go, oidc_handlers.go` |
| `break_glass_login` | A break-glass account signed in locally | `login.go` |
| `logout` | Explicit sign-out | `login.go logoutHandler` |
| `local_auth_toggled` | local_auth_enabled flipped | `login.go setLocalAuthHandler` |
| `sso_linked` | First SSO login linked a provider identity, OR an admin unlinked one (detail says which) | `oidc.go; useradmin.go` |
| `role_changed` | Despite the name: role granted/revoked, account status changed, display name edited, territory granted/revoked, manager set/cleared | `useradmin.go, useradmin_territories.go` |

Writing is **best-effort**: an audit insert failure never changes the outcome of the action being audited (a login still succeeds). Treat the log as a very good record, not a cryptographic guarantee.

### 13.2 What is not in the log

- Admin password set/replace (POST /auth/admin/users/{id}/password) writes no auth_events row.
- Territory enforcement flips (PUT /territories/enforcement) and the private-drafts switch (PUT /private-drafts/enforcement) update iam.system_settings.updated_at only; no auth_events row and no updated_by.
- API token mint / revoke (authadmin CLI) write no auth_events row; they stamp iam.api_tokens.created_by / revoked_at.
- Token use stamps last_used_at only (no event per request).
- Bulk role/permission definition changes are SQL migrations; the migration log (ops.migration_log) is the record.
- Permission denials (401/403 from RequirePermission) are not audited; they appear only in the request log (user.id attribute).

### 13.3 Reading it

There is **no API or UI to read `auth_events`**. Review is an out-of-band SQL query with `DATABASE_URL` (appendix B). The `IP` column stores the first `X-Forwarded-For` value when present — a header any client can set unless your proxy overwrites it — so treat it as advisory unless the proxy is configured to strip client-supplied values.

### 13.4 Other evidence an auditor can use

| Evidence | Where | Answers |
| --- | --- | --- |
| Who created / last changed a feature | `created_by`, `updated_by` (uuid) on every `gis.*` table, stamped from `ActorID(ctx)`; `gis.feature_history` | who edited this asset |
| Who granted a role / territory | `iam.user_roles.granted_by`, `granted_at`; `iam.user_territories.granted_by`, `granted_at` | who gave Dana this access, and when |
| Who connected / related features | `established_by` on `runtime.feature_connectivity` / `feature_relationships` | topology provenance |
| Who changed a runtime setting | `iam.system_settings.updated_at` (and `updated_by` for the local-auth switch only) | when a switch last moved |
| Token inventory | `iam.api_tokens` (`created_by`, `last_used_at`, `expires_at`, `revoked_at`) | which credentials exist, are stale, or were revoked |
| Schema / role changes | `ops.migration_log` (applied files, checksum, who) | when the catalog or a role changed |
| Per-request trail | structured request log, tagged with `user.id` and `X-Request-Id` | what a user called, and what was refused (see the Logging & Observability Guide) |
| Webhook deliveries | `ops.webhook_*` delivery tables | what an external feed asked for and the result |

### 13.5 Retention and loss

Nothing in the application prunes `auth_events`. `cmd/resetdata` **truncates it**. Export before any reset (appendix B.9) and ship rows to your SIEM if the history has compliance value.

---

## 14. Putting the layers together

### 14.1 Decision order for one request

```
  Is there a valid session (cookie) ............................ no -> 401
  Does the mount gate's permission (by method) exist in your set no -> 403 (names the permission)
  Is it a CSRF-protected surface and does the token/Sec-Fetch-Site pass?  no -> 403
  Does an inner gate apply (diagrams:delete, features:wipe, ...)?         no -> 403
  Feature route and territory enforcement on and you are not unscoped:
      read:  feature outside your set (or unassigned) ........... -> 404
      update/delete/publish of an existing feature outside your set -> 404 (read check runs first)
      create / move into a territory you do not hold ............ -> 403
      create: no covering territory ............................. -> 400
  Private drafts on and feature is a peer's never-published draft ... -> 404
  Private drafts on, you are not unscoped, feature is public and you edit -> staged, public row untouched
  else -> performed, stamped with your user id
```

### 14.2 Worked cases

| Who | Request | Outcome | Why |
| --- | --- | --- | --- |
| Priya (`viewer`) | `GET /features/mv_conductor` | 200 | `features:read`; (enforcement on) only features in her set |
| Priya | `POST /features/mv_conductor` | 403 `required permission (features:write)` | mount gate, step 2 |
| Dana (`editor`, Oakwood) | `PATCH` an existing Riverside feature, enforcement on | 404 | RBAC passes; `enforceReadScope` on the existing row hides it |
| Dana | `POST` a new feature drawn inside Riverside | 403 `you don't have access to the territory...` | RBAC passes; `enforceWriteScope` rejects the destination |
| Dana | `GET` that Riverside feature | 404 | `enforceReadScope` hides existence |
| Dana | `GET /admin/feature-classes` | 403 `metadata:write` | even admin *reads* need `metadata:write` |
| Tom (`analyst`) | `POST /trace` with a start node in Oakwood | 200, results limited to his set | `trace:run`; trace filter |
| Tom | `POST /analysis/load-flow` starting out of scope | 404 | start gate only |
| Ada (`admin`) | any of the above | 200 | all permissions and `territories:unscoped` |
| Owen (flag only, no role) | `POST /auth/settings/local-auth` | 200 | handler allows `break_glass` |
| Owen (flag only, no role) | `GET /features/...` | 403 | the flag grants no permission |
| Disabled user with a live cookie | any | 401 | session loader requires `status='active'` |
| Token holder calling the API server directly | `Authorization: Bearer ve_mcp_...` | 401 | the API server reads cookies only |

---

## 15. Runbooks

### 15.1 Onboard a person (cookie user)

1. `POST /auth/admin/users` `{email, display_name, role, password?}` (or Admin > Users). For SSO-only, omit the password.
2. If crews are scoped: `POST .../territories` for the crew's node; set `manager` if they report to someone who should inherit scope.
3. Tell the person how to sign in. SSO: the first success links.
4. Record the ticket id alongside the change; `role_changed` events carry the user id but not your ticket.

### 15.2 Change someone's job

Grant the new role first, then revoke the old one. Both take effect on the person's next request; there is no session to reset. If territory changes too, grant before revoke to avoid a window with no access.

### 15.3 Offboard

1. `POST .../status {status:"disabled"}` — revokes all sessions, blocks local login, SSO login (403 `this account is disabled`), and bearer tokens.
2. Revoke any API tokens explicitly if you want the inventory clean: `authadmin list-tokens` / `revoke-token`.
3. If they managed anyone, re-point those reports' manager (`POST .../manager`). A disabled manager remains in the org tree and keeps inheriting their reports' territories, which is harmless only because a disabled user cannot sign in.
4. Do **not** delete the row: `created_by` references and audit rows should keep meaning.

### 15.4 Scope a crew to its district

Grant the district node to each crew member (or to their lead and set managers), confirm with `GET /auth/admin/users` (the `territories` array), then — if enforcement is already on — have a crew member verify with `GET /territories/mine`. If enforcement is off the grants do nothing yet.

### 15.5 Turn on territory enforcement

Chapter 11.6, in order. Take a screenshot of the coverage report as evidence.

### 15.6 SSO outage

Chapter 10.5. Afterwards revert the switch and reconcile the events.

### 15.7 Recover a locked-out administrator

- Account locked (5 failures): wait 15 minutes, or `UPDATE iam.users SET failed_login_count = 0, locked_until = NULL WHERE email = ...`.
- Lost password: `AUTHADMIN_PASSWORD=... authadmin set-password --email ...`.
- Lost the admin role (removed your own): `authadmin grant-role --email ... --role admin`.
- Everyone disabled: `authadmin create ...` makes a fresh break-glass admin.
All of these work with the API down and are performed by a person with `DATABASE_URL`.

### 15.8 Lost or stolen field device

`authadmin list-tokens --email <user>`, find the `field-app · <device_name>` token and `revoke-token --id`. If the password may be known, set a new one and consider disabling the user until reissued. Existing sessions on a laptop are separate (disable revokes them).

### 15.9 Quarterly access review

Run appendix B.2 (who holds what), B.5 (flags), B.6 (stale tokens), B.4 (territory grants) and compare with HR/org data; attach the output as evidence; revoke the excess.

### 15.10 Add a custom role

Chapter 3.4: write the migration, apply, `authadmin roles`, assign, test with a throwaway account (the interactive request tester shows the expected result).

---

## 16. Auditor's checklist and queries

| # | Control | How to verify | Expected |
| --- | --- | --- | --- |
| 1 | No shipped default credentials | `SELECT count(*) FROM iam.users` on a fresh DB | 0 until `authadmin create` |
| 2 | Passwords hashed with Argon2id, no plaintext | `SELECT left(password_hash,10) FROM iam.user_identities WHERE provider='local'` | `$argon2id$` |
| 3 | Sessions opaque, hashed, revocable | `iam.sessions` has `token_sha256`, `revoked_at` | yes |
| 4 | Every API mount gated | read `main.go` (or chapter 5.2) | only `/healthz`, `/webhooks`, `/auth` public |
| 5 | Admin set small and known | B.2 filtered to `admin`/`break-glass-admin`/`admin:users` holders | matches approved list |
| 6 | Break-glass accounts few and alerted | B.5, plus alert on `break_glass_login` | approved list |
| 7 | Local auth switch off in normal operation | B.8 | `false` |
| 8 | No unexplained territory bypass | B.2 for `territories:unscoped` | admins only |
| 9 | Territory enforcement state | `territory_enforcement_enabled` | `true` once rolled out |
| 10 | Tokens have owners, are used, and are bounded | B.6 | no stale never-expiring tokens |
| 11 | Lockout and rate limits active | 5 wrong passwords -> 15-minute lock; 11th rapid login -> 429 | yes |
| 12 | CSRF on administrative mutations | chapter 6.4 | yes; core data mounts rely on SameSite=Strict |
| 13 | Audit retention | B.9 export exists before any reset | yes |
| 14 | Role definitions under change control | migrations folder + `ops.migration_log` | no ad-hoc SQL edits |

---

## 17. Troubleshooting

| Symptom | Likely cause | Check / fix |
| --- | --- | --- |
| I can sign in but every page says forbidden | The account holds no role | B.2; `POST .../roles` |
| 401 on everything after a while | Session expired (12h idle / 30d absolute), or the account was disabled | Sign in again; check `status` |
| 403 "local password sign-in is currently disabled" | `local_auth_enabled` is false and the user is not break-glass | Use SSO, or have a break-glass admin enable local auth temporarily |
| "invalid email or password" with the right password | Account locked (5 failures, 15 min), disabled, or SSO-only (no local password) | B.7 lock check; set a password via the admin API |
| 429 on sign-in | The per-IP limiter (10 burst, 10/min) — often a shared proxy address | Wait 60s; rate-limit at the proxy using real client IPs |
| SSO redirects back with `?error=no_account` | No active VoltEdge user with the IdP email | Create the user with the same email (chapter 8.3) |
| SSO `?error=denied` | Google email not verified, or outside `OIDC_GOOGLE_ALLOWED_DOMAIN` | Check the domain setting |
| SSO provider button missing | Provider env not set or discovery failed at API start | Check API logs at startup; restart after fixing env |
| Viewer gets 403 on the map query panel | Query/aggregate are POSTs and need `features:write` (chapter 5.3) | Grant a role with `features:write`, or change the gate in code |
| Editor cannot open Admin pages | They lack `metadata:write` (all `/admin` reads need it) | By design |
| Supervisor cannot open Outages/Works/Faults | Chapter 3.3 drift | Grant those permissions to `supervisor` |
| Scoped user sees an empty map | No effective territories (no grants, no reports), or every feature unassigned | `GET /territories/mine`; grants; coverage report |
| Feature exists but a user gets 404 | Out of their territory, or a peer's private draft | Chapter 14.1 order |
| Grant had no effect | Enforcement is off; or another API replica still has the cached set (<= 60s) | `territory_enforcement_enabled`; wait or restart |
| 400 "this location isn't within any ..." on create | Enforcement on and no district covers the point | Draw/extend a boundary, or pass an explicit `territory_id` override |
| Editor cannot wipe a class | `features:wipe` is admin-only | By design |
| Diagram delete 403 for an editor | Needs `diagrams:delete` (supervisor/admin) | Assign `supervisor` or grant the permission |
| `circuits` assign 403 for an admin-lite role | Needs `circuits:write` **and** `features:write` | Grant both |
| Agent's Bearer token gets 401 from the API | The API server does not read bearer tokens | Point the agent at the MCP server |
| Token still works after the person left | Account not disabled | Disable (kills effect) and revoke the token |
| Pending edit "changed by someone else" | Live row newer than the staged base | Discard and redo |
| Role change did not appear | Cached UI permission list | `/auth/me` is loaded at sign-in; reload the page. Server checks are always current |

---

## Appendix A. Role and permission matrix

Built from the database produced by applying every migration. `x` = granted. Columns: V viewer, A analyst, E editor, S supervisor, Ad admin, BG break-glass-admin, FS field-surveyor, FE field-enumerator, FT field-technician.

| Permission | V | A | E | S | Ad | BG | FS | FE | FT |
| --- | :-: | :-: | :-: | :-: | :-: | :-: | :-: | :-: | :-: |
| `admin:auth-settings` |  |  |  |  | x | x |  |  |  |
| `admin:field-packs` |  |  |  |  | x | x |  |  |  |
| `admin:users` |  |  |  |  | x | x |  |  |  |
| `analysis:run` |  | x | x | x | x | x |  |  |  |
| `annotations:read` | x | x | x | x | x | x |  |  |  |
| `annotations:write` |  |  | x | x | x | x |  |  |  |
| `attachments:read` | x | x | x | x | x | x | x | x | x |
| `attachments:write` |  |  | x | x | x | x | x | x | x |
| `circuits:write` |  |  |  |  | x | x |  |  |  |
| `connectivity:commit` |  |  | x | x | x | x |  |  |  |
| `connectivity:read` | x | x | x | x | x | x | x | x | x |
| `diagrams:delete` |  |  |  | x | x | x |  |  |  |
| `diagrams:read` | x | x | x | x | x | x |  |  |  |
| `diagrams:write` |  |  | x | x | x | x |  |  |  |
| `faults:read` | x | x | x |  | x | x |  |  | x |
| `faults:write` |  |  | x |  | x | x |  |  | x |
| `features:read` | x | x | x | x | x | x | x | x | x |
| `features:wipe` |  |  |  |  | x | x |  |  |  |
| `features:write` |  |  | x | x | x | x |  | x | x |
| `metadata:read` | x | x | x | x | x | x |  |  |  |
| `metadata:write` |  |  |  |  | x | x |  |  |  |
| `oms:read` | x | x | x |  | x | x |  |  |  |
| `oms:write` |  |  | x |  | x | x |  |  |  |
| `relationships:commit` |  |  | x | x | x | x |  |  |  |
| `relationships:read` | x | x | x | x | x | x |  |  |  |
| `sync:post` |  |  |  |  | x | x |  |  |  |
| `sync:review` |  |  |  |  | x | x |  |  |  |
| `tags:read` | x | x | x |  | x | x |  |  | x |
| `tags:write` |  |  | x |  | x | x |  |  |  |
| `territories:unscoped` |  |  |  |  | x | x |  |  |  |
| `territories:write` |  |  |  |  | x | x |  |  |  |
| `trace:run` |  | x | x | x | x | x |  |  |  |
| `wam:read` | x | x | x |  | x | x |  |  | x |
| `wam:write` |  |  | x |  | x | x |  |  | x |
| **total** | **11** | **13** | **23** | **16** | **34** | **34** | **4** | **5** | **10** |

---

## Appendix B. SQL cookbook

All queries are read-only unless marked. Run with `psql "$DATABASE_URL"`. (B.1-B.3 and B.5-B.10, except the territory join in B.4, were executed against a scratch database built from the migrations; the territory queries need the PostGIS-backed `gis.territories` table.)

**B.1 A role's permissions**
```sql
SELECT rp.permission_key
FROM iam.role_permissions rp JOIN iam.roles r ON r.id = rp.role_id
WHERE r.key = 'editor' ORDER BY 1;
```

**B.2 Who holds what (the access register)**
```sql
SELECT u.email, u.status, u.break_glass,
       string_agg(DISTINCT r.key, ', ' ORDER BY r.key) AS roles
FROM iam.users u
LEFT JOIN iam.user_roles ur ON ur.user_id = u.id
LEFT JOIN iam.roles r ON r.id = ur.role_id
GROUP BY u.id ORDER BY u.email;
```

**B.3 Which roles give a user a permission** (the "why can Dana do X" query)
```sql
SELECT u.email, r.key AS via_role, rp.permission_key
FROM iam.users u
JOIN iam.user_roles ur ON ur.user_id = u.id
JOIN iam.roles r ON r.id = ur.role_id
JOIN iam.role_permissions rp ON rp.role_id = r.id
WHERE u.email = 'dana@oakwood.example' AND rp.permission_key = 'features:write';
```

**B.4 Territory grants and managers**
```sql
SELECT u.email, t.name AS territory, lv.code AS level, ut.granted_at, g.email AS granted_by
FROM iam.user_territories ut
JOIN iam.users u ON u.id = ut.user_id
JOIN gis.territories t ON t.id = ut.territory_id
JOIN gis.territory_levels lv ON lv.id = t.level_id
LEFT JOIN iam.users g ON g.id = ut.granted_by
ORDER BY u.email, lv.rank;

SELECT u.email AS user, m.email AS manager FROM iam.users u LEFT JOIN iam.users m ON m.id = u.manager_id ORDER BY 1;
```

**B.5 Break-glass flags and unscoped holders**
```sql
SELECT email, status FROM iam.users WHERE break_glass;
SELECT DISTINCT u.email FROM iam.users u
JOIN iam.user_roles ur ON ur.user_id = u.id
JOIN iam.role_permissions rp ON rp.role_id = ur.role_id
WHERE rp.permission_key = 'territories:unscoped';
```

**B.6 Token inventory and stale tokens**
```sql
SELECT u.email, t.name, t.token_prefix, t.created_at, t.last_used_at, t.expires_at, t.revoked_at
FROM iam.api_tokens t JOIN iam.users u ON u.id = t.user_id
ORDER BY t.last_used_at NULLS FIRST;

-- never expiring and unused for 90 days
SELECT * FROM iam.api_tokens
WHERE revoked_at IS NULL AND expires_at IS NULL AND coalesce(last_used_at, created_at) < now() - interval '90 days';
```

**B.7 Lockouts, sessions, and the supervisor fix**
```sql
SELECT email, failed_login_count, locked_until FROM iam.users WHERE locked_until > now() OR failed_login_count > 0;

SELECT u.email, count(*) AS live_sessions
FROM iam.sessions s JOIN iam.users u ON u.id = s.user_id
WHERE s.revoked_at IS NULL AND s.idle_expires_at > now() AND s.absolute_expires_at > now() GROUP BY 1;

-- MUTATING: give supervisor the permissions editor has and it lacks (chapter 3.3) -- ship as a migration
INSERT INTO iam.role_permissions (role_id, permission_key)
SELECT s.id, ep.permission_key
FROM iam.roles s
JOIN iam.roles e ON e.key = 'editor'
JOIN iam.role_permissions ep ON ep.role_id = e.id
WHERE s.key = 'supervisor'
ON CONFLICT DO NOTHING;
```

**B.8 Runtime switches**
```sql
SELECT key, value, updated_at FROM iam.system_settings
WHERE key IN ('local_auth_enabled','territory_enforcement_enabled','private_drafts_enabled');
```
An absent row means off for all three.

**B.9 The audit trail**
```sql
-- credential attacks in the last hour
SELECT created_at, event_type, ip, detail FROM iam.auth_events
WHERE event_type IN ('login_failure','login_denied') AND created_at > now() - interval '1 hour'
ORDER BY created_at DESC;

-- every break-glass sign-in and every switch flip
SELECT created_at, event_type, user_id, ip, detail FROM iam.auth_events
WHERE event_type IN ('break_glass_login','local_auth_toggled') ORDER BY created_at DESC;

-- who changed access (role/status/territory/manager) for one user
SELECT created_at, detail FROM iam.auth_events
WHERE event_type = 'role_changed' AND user_id = (SELECT id FROM iam.users WHERE email = 'dana@oakwood.example')
ORDER BY created_at;

-- export before a factory reset
\copy (SELECT * FROM iam.auth_events ORDER BY created_at) TO 'auth_events.csv' CSV HEADER
```

**B.10 Catalog sanity checks**
```sql
-- permissions nobody holds
SELECT p.key FROM iam.permissions p
WHERE NOT EXISTS (SELECT 1 FROM iam.role_permissions rp WHERE rp.permission_key = p.key);

-- admin should equal the whole catalog
SELECT p.key FROM iam.permissions p
WHERE NOT EXISTS (SELECT 1 FROM iam.role_permissions rp JOIN iam.roles r ON r.id = rp.role_id
                  WHERE r.key = 'admin' AND rp.permission_key = p.key);
```

---

## Appendix C. Source index

| Topic | File |
| --- | --- |
| Route gates | `api/cmd/server/main.go` |
| Permission constants, `RequirePermission` | `api/internal/auth/rbac.go` |
| Session loader, CSRF, headers | `api/internal/auth/middleware.go` |
| Session lifecycle, permission load | `api/internal/auth/session.go` |
| Local login, lockout, switch | `api/internal/auth/login.go`, `settings.go` |
| Passwords | `api/internal/auth/password.go`, `provision.go` |
| SSO | `api/internal/auth/oidc.go`, `oidc_handlers.go` |
| Bearer tokens, field login, service identity | `apitoken.go`, `fieldlogin.go`, `serviceidentity.go` |
| User administration | `useradmin.go`, `useradmin_territories.go` |
| Audit events | `events.go`, `models.go` |
| Rate limiter | `api/internal/ratelimit/` |
| Territory resolution and grants | `api/internal/access/`, `api/internal/territory/` |
| Read/write scope | `api/internal/crud/scope.go`, `tiles/tiles.go`, `export/export.go`, `trace/scope.go`, `powerflow/scope.go` |
| Private drafts | `api/internal/drafts/`, `crud/pending.go` |
| Schema and seeds | `migrations/0024`, `0025`, `0027`-`0031`, `0037`, `0040`, `0041`, `0044`, `0061`, `0063`, `0078`, `0088`, `0094`, `0097`, `0099`, `0107`, `0110`, `0112`, `0115`, `0116`, `0118`, `0124` |
| CLI | `api/cmd/authadmin/`, `api/cmd/resetdata/` |
| SPA gates | `web/src/app.tsx`, `web/src/hooks/use-auth.ts`, `web/src/components/layout/app-sidebar.tsx` |

---

## Appendix D. Corrections to existing documents

While building this guide, four existing documents were checked against the code. The following clear errors were corrected **in place, minimally**; nothing else in those documents was rewritten.

Line numbers refer to the documents **before** the correction (`git show HEAD:<file>`); evidence is the code or migration that contradicts the old text.

**`docs/admin-break-glass-guide.md`**

| # | Where (old line) | Was | Now | Evidence |
| --- | --- | --- | --- | --- |
| 1 | §3.1 table (430) | table named `iam.identities` | `iam.user_identities` | `migrations/0024_create_auth_schema.sql` (`CREATE TABLE iam.user_identities`) |
| 2 | §1.6 (123), §3.2 (445), §9.2 (1561) | "Five roles ship" / "seeds exactly five roles" | five baseline roles from 0025, plus `supervisor` (0078) and three field roles (0099): nine | `migrations/0078`, `0099`; scratch DB: 9 rows in `iam.roles` |
| 3 | §1.6 (130, 136), §3.2 (452), §9.1 (1559) | later permissions "automatically flow" to admin/break-glass via `CROSS JOIN` | the `CROSS JOIN` ran once in 0025 over 14 permissions; each later migration grants explicitly | `0031`, `0040`, `0044`, `0061`, `0063`, `0078`, `0088`, `0094`, `0097`, `0107`, `0115`, `0116`, `0162`, `0164` each `INSERT INTO iam.role_permissions ... WHERE r.key IN ('admin','break-glass-admin')` |
| 4 | §1.3 (82), §3.6 (620) | CSRF check includes `Origin`; every mutating route carries it | only `Sec-Fetch-Site` is inspected; middleware is applied to a specific set of surfaces | `api/internal/auth/middleware.go` `RequireCSRF`; `grep RequireCSRF` shows `/auth` logout + switch, `/auth/admin`, attachments, assistant chat/execute, field-packs, sync-review only |
| 5 | §3.8 (652) | an AI agent's bearer token drives `POST /analysis/...` through the API's step 1 | the API server reads cookies only; the agent uses the MCP server | `middleware.go` `SessionLoader` (cookie only); bearer users: `mcpserver/server.go:256`, `fieldpack/routes.go`, `webhook/receiver.go:173` |
| 6 | §4.2 (704) | quoted message with `"District"` | level **code**, `"district"` | `territory/assign.go` (`min.Code`) |
| 7 | §4.7 (810) | "A fresh install has no such row" | migration 0029 seeds `'false'` | `migrations/0029_seed_territory_enforcement_flag.sql` |
| 8 | §9.1 (1538-1547) | `diagrams:delete` lowest role editor; `diagrams:write` "delete diagrams"; read/write pairs for attachments, OMS, WAM, faults listed as "editor"; `territories:write` "& grant scope" | `diagrams:delete` is supervisor/admin/break-glass only; reads are viewer-level; granting a territory is `admin:users` | `migrations/0078` (editor not granted); `0041`, `0088`, `0097`, `0107` (reads to viewer); `useradmin.go` territory grant under `admin:users` |
| 9b | §4.6 (802), §4.8 (840) | writes of an existing out-of-scope feature answer 403 via `enforceWriteScope` | 404 via `enforceReadScope` first; 403 is for create / move destination | `crud/update.go:79-84, 213`, `crud/delete.go:60`, `crud/create.go:83` |
| 9 | §9.4 (1639, 1642, 1650) | `/admin` and `/external-systems` read gate `metadata:read`; `/circuits` read gate "—" | `metadata:write` for both verbs; `/circuits` GET is `features:read` | `api/cmd/server/main.go` mounts of `/admin`, `/external-systems`, `/circuits` |

**`docs/phase-10-auth.md`**

| # | Where (old line) | Was | Now | Evidence |
| --- | --- | --- | --- | --- |
| 10 | 95 | a password change and a break-glass event revoke sessions | only logout and an admin disable revoke | `useradmin.go` `adminSetPasswordHandler` (no revoke), `adminSetStatusHandler` |
| 11 | 101 | `Origin`/`Sec-Fetch-Site` check on every mutating request | `Sec-Fetch-Site` only, on specific routes | `middleware.go`, see #4 |
| 12 | 111 | per-account rate limit with escalating backoff | per-IP token bucket (10/10) + fixed 5-failure / 15-minute lockout | `ratelimit/ratelimit.go`, `login.go` constants |
| 13 | 119, 137 | `GET /auth/oidc/{provider}/start` | `/login` | `handler.go` routes |
| 14 | 121 | flow cookie "signed" | unsigned base64 JSON | `oidc_handlers.go` `setFlowCookie` |
| 15 | 48, 127 | Azure `tid` checked; Google "address allowlist" | tenant-scoped issuer; Google email verified + optional domain | `oidc.go` `validateClaims` |
| 16 | 326, 330 | `golang.org/x/time/rate`; strict CSP | in-house limiter; no CSP, HSTS only over TLS | `api/go.mod` (no `x/time`), `middleware.go` `SecurityHeaders` |
| 17 | 349 | env seeds `local_auth_enabled` | migration 0024 seeds `false`; no env var | repo grep: no `LOCAL_AUTH_ENABLED` outside the docs |
| 18 | 381 | only `/healthz` and `/auth` open | also `/webhooks` | `main.go` |
| 19 | 275 | `iam.sessions` columns omitted `csrf_token`, `idle_expires_at` | added | `migrations/0024` |

**`docs/phase-11-territories.md`**

| # | Where (old line) | Was | Now | Evidence |
| --- | --- | --- | --- | --- |
| 20 | 517 (Slice I verify) | "As an editor" authors boundaries | an admin (`territories:write`); editors lack it | `migrations/0031` grants to admin/break-glass only |

**`docs/phase-58-spatial-access-control.md`**

| # | Where (old line) | Was | Now | Evidence |
| --- | --- | --- | --- | --- |
| 21 | 36 | Martin keeps serving the territory overlay | overlay is fed by `GET /territories/boundaries` | `territory/http.go`, phase 11 Slice H |
| 22 | 39, 92 | cache busts the affected user and managers above | `InvalidateAll()` flushes every user | `access/cache.go` |
| 23 | 84 | `access` imports `territory` | imports neither `territory` nor `auth` | `access/grants.go` imports |
| 24 | 89 | `EffectiveIDs(...) ([]string, bool, error)` | `([]string, error)`; unscoped decided in `auth.loadScopeFields` | `access/grants.go`, `auth/scope.go` |
| 25 | 95 | `AllowedTerritories map[string]struct{}` | `[]string` (plus `Subordinates`) | `auth/context.go` |

**Findings reported but not changed** (data decisions or behaviour, listed in appendix E): `supervisor` description drift, read-only POSTs needing `features:write`, `is_system` not enforced.

---

## Appendix E. Not verified, and known limits

**Not verified at runtime.** This review read code and applied migrations to a scratch PostgreSQL (PostGIS stubbed: the `gis.territories` table and everything depending on geometry were read from source, not executed). The following were **not exercised against a running API**: the 403 for a pure `viewer` on `POST /features/{code}/query`; HTTP status codes quoted for each branch; the 429 limiter timing; SSO against a live Azure AD or Google tenant (code read only); the behaviour of Martin versus the in-process tile endpoint in your deployment.

**Observed limits and gaps** (facts from the code, not recommendations to change them):

1. `supervisor`'s description does not match its grants (chapter 3.3).
2. `is_system` is not enforced anywhere; any role can be edited or deleted by SQL.
3. Read-only POSTs on method-aware mounts require the write permission (chapter 5.3).
4. CSRF middleware is applied to a subset of mutating routes (chapter 6.4).
5. Lockout is fixed (5 attempts / 15 minutes) and per account; the rate limiter is per process and keyed by peer address (shared proxy address problem).
6. The audit IP column trusts the first `X-Forwarded-For` value.
7. Admin password changes, territory-enforcement flips and private-draft switch flips are not written to `iam.auth_events`; `role_changed` is overloaded for status, name, territory and manager changes.
8. Territory caches are per process: with multiple API replicas, a stale scope can be served for up to 60 seconds on replicas that did not handle the admin call.
9. `GET /territories` is not scoped (names are visible to every map user).
10. A session-less internal caller is trusted and unscoped by design (workers run with a constructed user, not session-less, but any new code path that omits the user is unscoped).
11. API server does not accept bearer tokens; only the MCP, field-sync and webhook services do.
12. The seeded territory boundaries are illustrative rectangles around central London.
13. `private_drafts_enabled` has no seeded row; absent means off.

---

*End of the Access Control Guide.*
