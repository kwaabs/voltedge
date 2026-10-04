# VoltEdge Logging & Observability Guide

A reference for the people who **run** VoltEdge: what the services log, how to configure it, how to read a request's trail, and how to get logs off the box and into a place you can search. This is a companion to the [Administration & Break-Glass Guide](admin-break-glass-guide.md) — where that covers *who can do what*, this covers *what the system tells you while it does it*.

**Who this is for.** Operators and administrators running the stack (Docker/Kubernetes/systemd), and anyone diagnosing a production incident. You don't need to read Go, but every claim here is grounded in the code and linked, so a developer can follow it down.

**The cast.** **Ada** (system administrator) configures the logging and stands up the shipper; **Owen** (break-glass admin) appears when an incident needs the trail. The running example is a single user request that misbehaves, and how its **correlation id** ties the whole story together.

**Legend.** 🔒 an action gated by a permission or environment access, 💡 a tip, ⚠️ a caution.

---

## Chapter 1 — The model: one seam, structured output

Every VoltEdge binary logs through a single seam — the **`logx`** package ([api/internal/logx](api/internal/logx/)). The point of routing all logging through one place is that **format, level, and destination are decided once**, not re-invented per package. A binary calls `logx.Init()` as the first thing in `main()`; that builds the one logger, installs it as the process default, and every log line from then on — whether written through `logx` or a stray standard-library `slog` call — lands in the same stream, in the same shape.

Three design decisions define the system, and they are worth internalising before you configure anything:

1. **Structured, not prose.** Lines are **key–value records** (JSON in production), not free-form sentences. `"http request" method=GET status=200 duration_ms=14` is a set of fields you can filter and aggregate, not a string you grep and hope. This is the difference between "find every request slower than 500 ms" being a query versus a prayer.

2. **The app streams; it does not store.** VoltEdge writes logs to **stdout** and stops caring. It never opens a log file, rotates one, or offers an endpoint to read logs back. *Where logs reside* is the platform's job — Docker, Kubernetes, or systemd captures the stream and routes it. This is the **twelve-factor** rule, and Chapter 5 is the whole of "so where do they actually go?".

3. **OpenTelemetry-compatible field names.** Field names follow **OpenTelemetry semantic conventions** where one exists — `http.request.method`, `url.path`, `http.response.status_code`, `user.id`. Today these are emitted as plain JSON with no OTel SDK involved. But because the *names* already match the standard, a future switch to an OTLP exporter (shipping to an OpenTelemetry collector) is a **one-file change** in `logx.Init()` — no call site anywhere else moves. You get the portability of the standard without paying for its machinery yet.

---

## Chapter 2 — Configuring it 🔒

🔒 *These are process environment variables, set wherever you define the services' environment (compose file, k8s manifest, systemd unit) — the same place `DATABASE_URL` lives.*

There are exactly **two** knobs, read by `logx.Init()` at startup:

| Variable | Values | Default | Effect |
| --- | --- | --- | --- |
| `LOG_FORMAT` | `json`, `text` | `json` | `json` = one JSON object per line (machine-parseable, for a shipper). `text` = human-readable, for a developer at a terminal. |
| `LOG_LEVEL` | `debug`, `info`, `warn`, `error` | `info` | The minimum severity emitted. Anything below the threshold is dropped. |

That's the whole surface. Some guidance:

- **Production:** leave `LOG_FORMAT` at its `json` default (a log shipper wants JSON) and keep `LOG_LEVEL=info`. `info` carries the request-summary lines and worker lifecycle; `warn`/`error` carry the problems.
- **Local development:** set `LOG_FORMAT=text` for readable output, and `LOG_LEVEL=debug` when you want everything.
- ⚠️ **`debug` in production is loud and can be sensitive.** Raise to `debug` deliberately and briefly to chase a specific problem, then put it back. The default `info` is the right standing level.

💡 There is no per-package or per-logger level — the threshold is process-wide, by design. If you need "just this subsystem at debug", that's a filter in your log backend (Chapter 5), not an app setting.

💡 There is also no "off". The quietest level is `error`; the logger itself can't be disabled. An audit-grade trail you can silently switch off wouldn't be much of a trail.

### 2.1 Changing the level live, from the UI 🔒

`LOG_FORMAT` and `LOG_LEVEL` are read once, at boot — so changing the **format** (or anything else) means editing the environment and restarting. The **level**, though, you can also change at **runtime, with no restart**, from **Admin → Logging** (`/admin/logging`).

🔒 *Gated on `admin:users` — a platform-operations control, not a per-user preference.*

Pick a level, press **Apply**, and it takes effect immediately for the running API server. The choice is **persisted** (in `iam.system_settings`, key `log_level`) and **re-applied at boot**, so it survives a restart — a level set here overrides the `LOG_LEVEL` env default from then on. Under the hood the shared log handler reads an atomic level variable that the screen updates live ([logx.go](api/internal/logx/logx.go) `SetLevel`; the endpoint is [internal/logsettings](api/internal/logsettings/handler.go)).

⚠️ **Scope: this governs the API server process only.** The out-of-process workers (`exportworker`, `importworker`, `fieldsync`, `webhookworker`) honour their own `LOG_LEVEL` environment setting and are not changed by this screen — raise their verbosity the env-var way (and restart them) if you need worker debug output.

So the division is: **format** and the worker levels are deploy-time env; the **API server's level** is a live admin control that also persists.

---

## Chapter 3 — The request log line

The heart of the API's observability is **one summary line per HTTP request**, written by the request-logging middleware ([api/internal/httpx/logger.go](api/internal/httpx/logger.go)) that replaces chi's plain-text logger. It is emitted when the request **finishes**, carrying:

| Field | Meaning |
| --- | --- |
| `request_id` | The request's correlation id (also returned to the client — §3.1) |
| `http.request.method` | `GET`, `POST`, … |
| `url.path` | The path **only** — never the query string (§4) |
| `http.response.status_code` | The status actually written |
| `http.response.body.size` | Response bytes |
| `duration_ms` | Wall-clock time to serve |
| `client.address` | The caller's IP (§3.2) |
| `user.id` | The authenticated user's opaque id, when the request was authenticated (§3.3) |

The line's **severity tracks the status**: a `5xx` logs at **error**, a `4xx` at **warn**, everything else at **info**. This is deliberate — your log backend can alert on error-level lines without having to parse and compare the status field.

### 3.1 The correlation id — the single most useful field

Every request is minted a random id and that id travels two ways:

- **Into the logs** as `request_id`, stamped on the summary line *and* on every handler or engine line written while serving that request (the request-scoped logger carries it down the call stack).
- **Back to the client** in the **`X-Request-Id`** response header.

So when Owen is handed a screenshot of a user's error, the response's `X-Request-Id` is the thread that pulls the entire server-side story out of the logs: filter on that one id and you have every line that request produced, in order. 💡 Surface `X-Request-Id` in client-side error messages (or capture it in your frontend error reporting) so this link is always available.

### 3.2 `client.address` behind a proxy

`client.address` is the **transport peer** (`r.RemoteAddr`) with the port stripped — the machine that actually opened the connection. It is deliberately **not** read from the `X-Forwarded-For` header, which any client can forge.

⚠️ **If VoltEdge runs behind a reverse proxy or load balancer, `client.address` will be the proxy's IP, not the user's.** To log the real client, mount a trusted-proxy middleware (chi's `middleware.RealIP`) **ahead** of the logger — and only do so when you actually trust the forwarded header, i.e. the proxy is yours and strips inbound `X-Forwarded-For`. Trusting that header on an untrusted edge lets callers spoof their address in your logs.

### 3.3 `user.id` and nothing more

When a request is authenticated, the session stack adds **only the user's opaque id** to the line — resolved just after the session middleware identifies the caller, and attached so the outermost logger sees it when it writes the end-of-request summary. The user's **email and display name are deliberately kept out** (§4). The id is enough to answer "who did this?" against the `iam.users` table without spilling personal data into every log line.

---

## Chapter 4 — What is deliberately *not* logged

VoltEdge treats logs as **audit-grade but privacy-aware**. The following never appear in a log line, by design:

- **The query string.** Only `url.path` is logged, not the raw query — query parameters can carry tokens, search terms, or other sensitive values.
- **Request bodies and response bodies.** Never logged.
- **Authorization headers, cookies, CSRF tokens, API tokens.** Never logged.
- **User PII.** Email and display name stay out; only the opaque `user.id` identifies the actor.

⚠️ This is a contract to **preserve** when you add logging. If you're extending the code, log the user *id*, not their email; log the path, not the full URL; and never dump a request body to help debugging and forget to remove it. The whole value of "audit-grade" collapses the first time a credential lands in a log aggregator that a wider audience can read.

---

## Chapter 5 — Where the logs go, and how to read them 🔒

The app streams to stdout; **the platform decides the rest.** How you read logs, and how durable they are, depends entirely on how you run the services.

### 5.1 Out of the box: Docker's json-file driver

Under the default `docker compose` setup, no logging driver is configured, so Docker uses its **json-file** driver. That means:

- Logs are readable with **`docker logs <service>`** (and `docker logs -f` to follow). See the [command cheatsheet](commands-cheatsheet.md) for the literal invocations.
- ⚠️ They are **ephemeral and local**: tied to the container, lost when it's removed, not centralised, not searchable across services, and growing on the host's disk until something rotates them. This is fine for development and a single box; it is **not** an observability solution for production.

💡 Because the output is JSON, you can pipe it through `jq` to read it:

```
docker logs api | jq 'select(.level=="ERROR")'
docker logs api | jq 'select(.request_id=="a1b2c3d4e5f6a7b8")'
docker logs api | jq 'select(.duration_ms > 500) | {url.path, duration_ms, status: ."http.response.status_code"}'
```

### 5.2 For real: ship to an aggregator

For anything beyond one box, run a **log shipper** that tails each service's stdout and forwards it to a central store you can search, dashboard, and alert on. The recommended shape:

- A collector — **Vector** — as a sidecar reading container stdout,
- forwarding to **Loki** (log store),
- queried and dashboarded in **Grafana**.

Because the lines are already structured JSON with stable field names, the shipper needs almost no parsing — it forwards the JSON body and a tiny label set, and `request_id`, `user.id`, `status`, `duration_ms` become first-class things you filter and graph with LogQL's `| json`.

**This is wired and ready — opt in with one overlay.** The repo ships a [`docker-compose.observability.yml`](docker-compose.observability.yml) overlay (Loki + Grafana + a Vector collector + a bearer-token proxy, configs under [`infra/`](infra/)). The base stack stays lean; you turn log aggregation on when you want it. Set a read token first — the proxy refuses to start without one:

```
LOKI_READ_TOKEN=$(openssl rand -hex 32) \
  docker compose -f docker-compose.yml -f docker-compose.observability.yml up -d
```

Then open Grafana (default `http://localhost:3708`) — the Loki data source is pre-wired — and Explore:

```
{service="voltedge-api"} | json | ."http.response.status_code" >= 500
{service="voltedge-api"} | json | request_id="a1b2c3d4e5f6a7b8"
{project="voltedge"}     | json | level="ERROR"
```

Vector reads every VoltEdge container's stdout off the Docker socket, so it **doesn't change the log driver** — `docker logs` keeps working alongside. It attaches two low-cardinality labels (`service` = container name, `project`) and ships the original JSON line as the body; everything else is parsed at query time with `| json`, which keeps Loki's index small.

**Authenticating external consumers.** Loki has **no authentication of its own** (`auth_enabled` toggles multi-tenancy, not a login), so the overlay does **not** publish Loki's port. Other systems read through the **bearer-token proxy** ([`infra/logproxy/Caddyfile`](infra/logproxy/Caddyfile)) on `LOKI_PROXY_PORT` (default `3709`), presenting the shared `LOKI_READ_TOKEN`:

```
curl -H "Authorization: Bearer $LOKI_READ_TOKEN" \
  "http://localhost:3709/loki/api/v1/query_range?query={service=\"voltedge-api\"}"
```

A request without the token (or with a wrong one) gets `401`. This is the §5.3 rule in practice: access control lives at the aggregator's front door (here, the proxy), never in a VoltEdge route. ⚠️ A single shared token has no per-consumer revocation — for several independent consumers, issue a token each (one matcher per token in the Caddyfile) or front Loki with a real auth service. And note Grafana's own anonymous-viewer default is a *second* unauthenticated read path on `:3708` — fine for local dev, but turn it off (`GF_AUTH_ANONYMOUS_ENABLED=false`) for anything shared. For production, also move Loki's chunks to object storage; the collector, proxy, and Grafana wiring is unchanged.

### 5.3 The one rule: don't build a log endpoint 🔒

⚠️ **External access to logs is the log aggregator's job, governed by *its* access control — never an application API.** It can be tempting to add a "GET /admin/logs" route so an admin can read logs in-app. Don't. Logs contain operational detail across every user and tenant; exposing them through the app means re-implementing a log store, its retention, and its RBAC inside VoltEdge, badly, and widening the blast radius of any auth bug. The correct boundary is: the app streams, the platform ships, and your Loki/Grafana (or equivalent) enforces who may read — with its own authentication, separate from the application's.

---

## Chapter 6 — Worker and background-job logs

The HTTP request line is the API's story; the **workers** (the out-of-band `cmd/` processes) tell theirs the same structured way. Each long-running worker builds a logger tagged with its identity and threads it through the job, so a line can always be traced to *which worker* and *which job* produced it:

- `worker` — the worker family (`webhook`, `import`, `export`, `fieldsync`, `sync`, `attachment-sweep`).
- `worker_id` — the specific process (host + pid), so two workers on one host are distinguishable.
- A **per-job id** — `delivery_id` (webhook), `job_id` (import/export), `pack_id` / `op_id` (field sync). Every line a job emits carries its id, so one job's trail filters out cleanly, exactly like `request_id` does for a request.

A worker logs `worker started` at boot (with its concurrency and lease), an `error` on a failed claim or a lost lease, and per-job outcome lines. The binaries that install the structured logger at startup are: the **API server**, `webhookworker`, `importworker`, `exportworker`, `fieldsync`, and `mcpserver`. (Boot-time fatal errors — a missing `DATABASE_URL` — still abort via the standard library before the logger's useful life begins; that's intentional.)

💡 To watch one webhook delivery end-to-end, filter the webhook worker's logs on its `delivery_id`; to audit a field-sync apply, filter on `op_id`. The pattern is identical to request correlation — pick the id, get the whole trail.

---

## Chapter 7 — Diagnosing an incident, end to end

Putting it together — Owen gets "the export failed for user X at about 14:05 with a strange error."

1. **If there's a client error with `X-Request-Id`,** filter the API logs on that `request_id`. Every line that request wrote appears, in order, including the `user.id`, the `url.path`, the final `http.response.status_code`, and any handler/engine warnings along the way. Usually the story ends here.
2. **If it's a background job** (an export, a webhook), move to that worker's logs and filter on the job id (`job_id`, `delivery_id`, …) from the request that enqueued it — or, lacking that, on the `user.id` and time window.
3. **Correlate across services** in your aggregator: the API line that enqueued the job and the worker line that ran it share the `user.id` and are close in time; Loki/Grafana lets you line them up.
4. **If the trail is too thin,** raise `LOG_LEVEL` to `debug` on the relevant service (Chapter 2), reproduce, read, and **put it back to `info`**.

The whole method rests on two ids — `request_id` for the synchronous path and the per-job id for the asynchronous one — plus the structured fields that make "filter to this, within that window" a query rather than a scroll.

---

## Appendix — Quick reference

**Configuration.** `LOG_FORMAT` = `json` (default) | `text`. `LOG_LEVEL` = `info` (default) | `debug` | `warn` | `error`. Set them in the service environment. The API server's level can also be changed **live** at **Admin → Logging** (`admin:users`), which persists and survives a restart (§2.1); format and the workers' levels stay env-only.

**Key fields.** `request_id` (also the `X-Request-Id` response header) · `http.request.method` · `url.path` · `http.response.status_code` · `http.response.body.size` · `duration_ms` · `client.address` · `user.id`. Workers add `worker` · `worker_id` · `job_id`/`delivery_id`/`op_id`/`pack_id`.

**Severity by HTTP status.** `5xx` → error · `4xx` → warn · else info.

**Never logged.** Query strings · request/response bodies · auth headers, cookies, tokens · user email/display name. Only the opaque `user.id`.

**Reading logs.** Dev/single box: `docker logs <service>` (JSON; pipe through `jq`). Production: ship stdout via Vector/Promtail → Loki → Grafana. Never expose logs through an application endpoint — access control belongs to the aggregator.

**Behind a proxy.** `client.address` is the connection peer, not `X-Forwarded-For`. Mount chi `middleware.RealIP` ahead of the logger **only** behind a proxy whose forwarded header you trust.

**Grounded in the code.** The seam: [api/internal/logx/logx.go](api/internal/logx/logx.go). The request middleware: [api/internal/httpx/logger.go](api/internal/httpx/logger.go). Worker tagging: the `cmd/*worker` mains and their engine packages (`internal/webhook`, `internal/importer`, `internal/export`, `internal/fieldpack`, `internal/attachment`).
