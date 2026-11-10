# VoltEdge Field App — Authentication &amp; SSO

**How a field tablet signs in — with a password or with Microsoft/Azure SSO — and the token, enrolment, setup, dev and security model behind it.**

_Last updated: 2026-11-03_

> **Companion interactive edition:** `field-sso-guide-interactive.html` runs the rules in this guide — a topology toggle, a hop-by-hop flow stepper, the two-gates simulator, a token-lifecycle timeline, a config resolver, and a troubleshooting diagnoser. Everything runs in the browser; nothing talks to a server.

> **Who this is for.** Administrators enrolling crews, operators deploying the field SSO config, and anyone debugging a sign-in that won't complete. It assumes **web** SSO already works (Azure AD configured on the API); field SSO rides on top of it. For the web/provisioning internals see the **Access Control Guide** (chapters 8–9); for the admin UI and a short enrolment recap see the **Field Tool Admin Guide** (§2.1a).

## Contents

1. [Why field auth is different](#1-why-field-auth-is-different)
2. [The full sign-in flow, hop by hop](#2-the-full-sign-in-flow-hop-by-hop)
3. [Enrolment and the two gates](#3-enrolment-and-the-two-gates)
4. [The token model](#4-the-token-model)
5. [Server and Azure setup](#5-server-and-azure-setup)
6. [Local dev and device testing](#6-local-dev-and-device-testing)
7. [Troubleshooting](#7-troubleshooting)
8. [Security model](#8-security-model)

---

## 1. Why field auth is different

Web and field SSO share everything that matters for **identity**: the same Azure provider, the same user accounts, the same match-or-reject logic (`authenticateOIDC`). What differs is the **credential the surface can hold**, and that one difference shapes the whole flow.

| | Web | Field app |
|---|---|---|
| Credential | Cookie session (the browser auto-sends it) | Bearer **token** in the device keystore |
| SSO ends by | setting the session cookie, redirect to `/` | handing the device a one-time **code** via `voltedge://`, which it swaps for tokens |
| Runs on | `cmd/server` | OIDC on `cmd/server`; redemption + sync on `cmd/fieldsync` |
| Token life | idle + absolute session windows | short **access token** + long **refresh token** (ch. 4) |

Two server processes matter, and they **share one database** — which is what makes the hand-off possible:

- **`cmd/server`** hosts the OIDC endpoints (`/auth/oidc/…`, `/auth/providers`) and holds the **Azure-registered redirect**.
- **`cmd/fieldsync`** is the field engine the device talks to for packs, sync, and redeeming the SSO code.

SSO runs on the first, drops a one-time code in the shared Postgres, and the device redeems it on the second. **Everything a crew does flows through `cmd/fieldsync`; the SSO handshake is the one thing that must touch `cmd/server`, because that's where Azure sends the code back.**

---

## 2. The full sign-in flow, hop by hop

The field app can't hand a bearer straight to itself — Azure only redirects to a pre-registered URL, and that URL lives on the server. So the flow is **server-mediated**: the app runs the real OIDC flow in the **system browser** (Azure blocks embedded webviews), and the server bridges the end of it back to the app with a single-use code.

1. **Field app — tap “Sign in with Microsoft”.** The app resolves where to start (`GET /auth/sso-config` → `login_base`), then opens the system browser at `${login_base}/auth/oidc/azuread/login?field=1&device_name=…`.
2. **cmd/server — start the OIDC flow.** Mints `state`, `nonce` and a PKCE verifier, stores them in the short-lived `ve_oidc_flow` cookie, tags the flow **field**, **overrides `redirect_uri`** to the origin the device reached it on, and redirects to Azure.
3. **Azure AD — the user signs in.** Microsoft authenticates the person — **password and MFA happen here, never in VoltEdge.** On success Azure redirects the browser to the registered redirect URI (`${api-origin}/auth/oidc/azuread/callback?code=…&state=…`).
4. **cmd/server — the callback verifies everything.** Reads the flow cookie, checks `state`, exchanges the code (with the same overridden `redirect_uri` + PKCE verifier), verifies the id_token (signature / issuer / audience) and `nonce`.
5. **cmd/server — match or reject the account.** `authenticateOIDC`: a known identity logs in; otherwise it matches a **pre-provisioned** account by email and links it. No account → `voltedge://field-sso?error=no_account`.
6. **cmd/server — gate `field:login`, mint a code.** The field branch checks `field:login`. Pass → insert a **single-use code** (`iam.field_sso_handoff`, 2-minute TTL) into the shared DB and redirect to `voltedge://field-sso?code=…`. Fail → `?error=no_field_access`.
7. **Field app — the browser hands back the code.** The system browser returns to the app via the `voltedge://` scheme (registered in the **app manifest**, not with Azure). The app reads the one-time `code`.
8. **cmd/fieldsync — redeem the code for tokens.** The app posts the code to `${api-origin}/auth/field-sso/exchange`; the service consumes it (atomically, single-use), re-checks the account + `field:login`, and returns an **access token** + **refresh token**. Signed in.

> **Key idea.** The `voltedge://` scheme is the app's own, registered in the app manifest. Azure never sees it — Azure only ever redirects to the server callback. That's why no mobile-platform entry is needed in the Azure app registration.

---

## 3. Enrolment and the two gates

SSO **never auto-creates accounts**. A Microsoft sign-in only gets a crew member into the field app if an administrator has already provisioned them. Two steps:

1. **Create the user** (Admin → Users) with **email = their Microsoft account email**. The emails must match — that's how VoltEdge links the Microsoft identity to the account on first login. No VoltEdge password is needed.
2. **Grant a role with `field:login`** (any field role, or admin). Without it, Microsoft accepts them but the field app refuses.

So a field SSO login passes **two gates, in order** — **Microsoft** *authenticates*, then **VoltEdge** *authorises*:

| Gate | Checks | Failure you'll see |
|------|--------|--------------------|
| **Microsoft** | the password / MFA (Microsoft's own) | “password incorrect” on the Microsoft page |
| **VoltEdge** | an **active** user with that email **and** `field:login` | `voltedge://…?error=no_account` (not provisioned) or `…?error=no_field_access` (missing `field:login`) |

The password only ever reaches Microsoft, so a “password incorrect” message is a Microsoft/account matter (reset it at `myaccount.microsoft.com`), never a VoltEdge one. You set **no VoltEdge password** for an SSO user; the **first** Microsoft sign-in auto-links the identity — there's no separate “register for Azure” step.

---

## 4. The token model

A password *or* SSO login (migration 0191) mints a **pair**:

- **Access token (~24h)** — an `iam.api_tokens` row; the bearer presented on **every** call. Short-lived so a leaked token self-heals quickly.
- **Refresh token (default 21 days)** — an `iam.field_refresh_tokens` row in the device keystore. The device swaps it for a fresh access token **without re-authenticating**. The TTL is set by `FIELD_REFRESH_TTL_DAYS` (default 21) — see "Aligning with SSO password rotation" below.

| Endpoint | Does |
|----------|------|
| `POST /auth/field-login` · `/auth/field-sso/exchange` | Mint the pair (password / SSO) |
| `POST /auth/field-refresh` | Swap the refresh token for a fresh access token (refresh token unchanged — **non-rotating**) |
| `POST /auth/field-logout` | Revoke the refresh token (device sign-out) |

**Offline is unaffected.** No API calls go out offline, so a lapsed access token doesn't matter until the device is back online — at which point it silently refreshes from the (still-valid) refresh token. A device offline longer than the refresh TTL re-authenticates.

### Aligning with SSO password rotation

The refresh token is **independent of Azure**: `field-refresh` re-checks the **VoltEdge** account (active + `field:login`) but does **not** re-validate against Microsoft. So two things behave differently:

- **VoltEdge-side disable** (you disable the user) → cut within **~24h** (the next access-token refresh re-checks `active`).
- **Azure-side change** (password rotated, account disabled in Entra, conditional access tightened) → **not reflected until the refresh token expires**.

If your org rotates passwords on a schedule (say every 28 days), set the refresh TTL **≤ that period** so a device must **re-SSO within one cycle**. Re-SSO re-opens the Microsoft flow and **delegates to Azure's current policy** — a rotated password or an invalidated session is enforced by Azure then.

```
# .env — applies to all field logins (password and SSO)
FIELD_REFRESH_TTL_DAYS=21        # default; set <= your IdP rotation period
```

> A shorter TTL **caps staleness**, it does not make revocation instant. For near-instant alignment you'd re-validate with the IdP on each refresh (store Azure's refresh token via `offline_access` and call Entra on `field-refresh`) — a larger change, not in v1.

**Why non-rotating?** Rotating the refresh token on every use is more secure in theory, but a field device's connectivity is flaky — a dropped rotation reply would strand it and force a needless re-login. v1 keeps the refresh token stable until expiry or revocation. Admin revocation of the user or their tokens still cuts off **both** web and field immediately.

---

## 5. Server and Azure setup

Field SSO reuses your existing Azure app and web OIDC config. Three one-time changes:

1. **Azure:** add a redirect URI for the **API origin the device reaches**, i.e. `<api-origin>/auth/oidc/azuread/callback`, **alongside** your web redirect (keep both). The field flow derives this from the request — it just needs to be registered. Add it under the **Web** platform, same as your web redirect.
2. **Env:** set `FIELD_SSO_WEB_ORIGIN` (on the API + fieldsync services) to that API origin, so `GET /auth/sso-config` tells the device where to start. If unset it is derived from `OIDC_AZURE_REDIRECT_URL`, else the request origin.
3. **Migrate:** apply migration **0191** (the hand-off + refresh-token tables).

**Which origin?** It depends on how your services are fronted:

| Topology | `FIELD_SSO_WEB_ORIGIN` | Azure redirect to add |
|----------|------------------------|-----------------------|
| **Single gateway (prod)** — one public origin proxies both services | `https://app.example.com` | `https://app.example.com/auth/oidc/azuread/callback` |
| **Split origins** — API and fieldsync on different hosts | the **API** origin, e.g. `https://api.example.com` | `https://api.example.com/auth/oidc/azuread/callback` |
| **Local dev (docker + adb reverse)** | `http://localhost:3704` (the API container's host port) | `http://localhost:3704/auth/oidc/azuread/callback` |

> **Azure platform note.** Azure's **Web** platform accepts `https://…` and `http://localhost` — but **not** `http://127.0.0.1`. So for local dev keep the host as `localhost` everywhere the *browser* and Azure see it.

Verify from the device's network: `curl <fieldsync-origin>/auth/sso-config` should return `{"login_base":"<api-origin>"}`, and `<api-origin>/auth/providers` should list `azuread` / Microsoft.

---

## 6. Local dev and device testing

In production there's one origin and none of this applies. Testing on a physical tablet against Docker containers has sharp edges — here they are so nobody re-walks them.

### 6.1 The port map (docker-compose defaults)

| Service | Host port | Serves |
|---------|-----------|--------|
| `cmd/server` (api) | **3704** (`API_PORT`; internal 8080) | OIDC, `/auth/providers`, the field callback |
| `cmd/fieldsync` | **3706** (`FIELDSYNC_PORT`) | packs, sync, `/auth/sso-config`, `/auth/field-sso/exchange` |
| web (Vite dev) | **5173** | the web app; proxies `/api` → `:3704` |

### 6.2 Reaching the host from the tablet

Use `adb reverse` so the tablet's `localhost` maps to your host — then the same `localhost` URLs work on the device and in Azure:

```
task adb-reverse        # reverses 3702/3704/3706/5173, then --list
adb reverse --list      # confirm after every replug / adb restart
```

> **adb reverse drops silently.** A container recreate, a device replug, or an `adb`/`flutter run` restart clears the reverses. The symptom is `Connection refused … localhost:3706` in the app while your PC `curl` to the same port works. Re-run `task adb-reverse`. Check this **first**.

### 6.3 Trap 1 — don't route field SSO through Vite

The web dev server (`:5173`) proxies `/api` to the API. It's tempting to point `FIELD_SSO_WEB_ORIGIN` at `localhost:5173/api` to reuse the web redirect — **don't**. The Dart HTTP client gets `Connection reset by peer` from Vite's proxy (a Dart-vs-Node-proxy quirk), even though the device's Chrome reaches the same URL fine. Point the field app straight at the API container (`:3704`) — a Go server, which the Dart client reaches cleanly (just like `:3706`). This is also the production shape: no Vite in prod.

### 6.4 Trap 2 — `localhost` vs `127.0.0.1`

If the app's own HTTP calls intermittently `Connection refused` while the browser works, suspect IPv6: Android can resolve `localhost` to `::1`, but `adb reverse` only listens on IPv4 `127.0.0.1`. The fix is **not** to change Azure (its Web platform rejects `127.0.0.1`). Keep the browser/Azure on `localhost`; only the device's own socket would need IPv4.

> **Ordering.** To make the button *appear*, `cmd/fieldsync` must be rebuilt (it serves `sso-config`). To *complete* a sign-in you also need `cmd/server` rebuilt (the field callback) and migration 0191 applied.

---

## 7. Troubleshooting

The login screen's SSO diagnostic shows the raw error — match it here.

| Symptom | Cause | Fix |
|---------|-------|-----|
| **No “Sign in with Microsoft” button** | Providers discovered at runtime; none found | Read the diagnostic. `Connection refused :3706` → adb reverse / fieldsync down. Wrong `login_base` → set `FIELD_SSO_WEB_ORIGIN` + restart fieldsync. `/auth/providers` empty → the API has no `OIDC_AZURE_*` env |
| **`Connection refused · localhost:3706`** | Device can't reach fieldsync (tunnel/container down) | `task adb-reverse`; `adb reverse --list`; `curl localhost:3706/healthz` from the PC. PC works but device doesn't → purely adb reverse |
| **`Connection reset by peer · :5173`** | Field SSO routed through the Vite proxy; Dart client reset there | Point `FIELD_SSO_WEB_ORIGIN` at the API directly (`http://localhost:3704`), not `:5173/api`; rebuild + restart fieldsync (§6.3) |
| **Microsoft: “password incorrect”** | Microsoft rejected the credential; VoltEdge never saw it | Verify at `myaccount.microsoft.com`; ensure the account is in the tenant `OIDC_AZURE_TENANT_ID` points to; don't confuse MFA with a password error |
| **“no VoltEdge account exists…”** | Microsoft accepted, but no VoltEdge user matches the email | Create the user with the **same email** Microsoft asserts (§3) |
| **“no field-app access”** | Account exists but lacks `field:login` | Grant a role with `field:login` |
| **Browser never returns to the app** | The `voltedge://` hand-off didn't reach the app | Confirm the `voltedge` scheme is in the Android manifest (`CallbackActivity` intent-filter) and the app was rebuilt. If the last browser URL stuck before `/callback`, the redirect isn't registered/reachable; after it, the scheme hand-off is the issue |
| **Redirect URI mismatch (AADSTS50011)** | Azure lacks the API-origin callback | Add `<api-origin>/auth/oidc/azuread/callback` to the Azure app's redirect URIs (§5) |

---

## 8. Security model

- **VoltEdge never sees the password.** It goes to Microsoft; VoltEdge only receives a verified id_token. A “password incorrect” is always Microsoft-side.
- **The hand-off code is single-use and short-lived.** `iam.field_sso_handoff` rows are consumed atomically (only the first redeem of an unused code wins) and expire in 2 minutes. They store a *user reference*, never a token.
- **Request-origin redirect is safe against Host spoofing.** The field flow derives `redirect_uri` from the request, but Azure only ever redirects to a **pre-registered** URI — a spoofed origin is rejected by Azure regardless.
- **PKCE + state + nonce** protect the auth-code flow exactly as the web flow does; the field variant reuses the same verification.
- **Linking by email on first login is a trust decision.** Whoever controls a Microsoft account asserting a verified email can claim the pre-provisioned VoltEdge account with that email — bounded by your Azure tenant restriction.
- **Revocation cuts both surfaces.** Disabling the user, or revoking their tokens, stops web and field. Device sign-out revokes the refresh token; the short access token lapses on its own.
- **Trade-off noted:** the refresh token is non-rotating in v1 (ch. 4) — a deliberate choice for field connectivity, revisitable with rotation + a grace window if policy requires.

**See also:** enrolment walk-through and the admin UI — **Field Tool Admin Guide** §2.1a. Web SSO + provisioning internals — **Access Control Guide** chapters 8–9. Code: `api/internal/auth` (oidc, fieldsession), `api/internal/fieldpack` (fieldauth, routes), migration `0191`.
