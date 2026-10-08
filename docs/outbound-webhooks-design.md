# Outbound webhooks / event emitter — design

**Status:** design (not built). A dev design doc, like `docs/phase-*.md`,
`docs/shared-field-packs-design.md`, and `docs/load-growth-forecast-design.md` —
deliberately **not** registered in the in-app docs viewer.

**One-line:** when something changes *in* VoltEdge (an outage raised, a work order
completed, a fault cleared), reliably **POST that event out** to one or more
configured external systems (CIS / work-management), using a **transactional outbox**
for at-least-once delivery and the engine's existing SSRF-hardened client + delivery
worker.

**Chosen approach (settled with the product owner):** **transactional outbox +
worker.** Each change writes an event row *in the same DB transaction* as the change,
so an event can never be lost once the change commits; a worker drains the outbox and
delivers with retries + dead-letter. The two rejected alternatives: *fire-and-forget*
(cheapest but best-effort — loses events on any crash/blip) and *DB-trigger capture*
(catches raw-SQL writes too, but a trigger per compiled feature table is heavy +
brittle + semantics-poor). CDC/Debezium was out of scope (operationally huge).

---

## 1. Purpose & scope

### What it is
The **outbound** counterpart to the existing inbound ingestion engine
(`api/internal/webhook`). Where that engine brings data *into* VoltEdge (pull or
receive), this emits VoltEdge's own changes *out* to subscribers, so an external CIS
or work-management system can react in near-real-time (e.g. close its ticket when a
defect is resolved here).

### What it is NOT
- **Not a sync of raw table writes.** It emits *semantic* events (`workorder.completed`,
  not "row N changed"), captured at the application write paths that know the meaning.
- **Not exactly-once.** It is **at-least-once** + an idempotency key, so a well-behaved
  receiver dedupes → effectively exactly-once for them.
- **Not a fan-in.** This is purely outbound; inbound stays the existing engine.

### The reuse thesis
Almost everything already exists. The delivery queue, the lease/attempts/retry worker,
the admin delivery log + replay, the sealed-secret handling, and the SSRF-hardened HTTP
client are all in the inbound engine (`webhook_deliveries`, `worker.go`,
`internal/secretbox`, `internal/outbound`). The genuinely new parts are the **outbox +
`emit()` hooks** and the **fan-out**; everything else is the inbound machinery inverted.

---

## 2. Architecture

```
  write path (oms / wam / faults / later crud)
     │  emit(tx, event) — SAME transaction as the change
     ▼
  ops.webhook_outbound_events        the OUTBOX: one row per real change
     │                               (seq, event_type, entity, payload, occurred_at)
     ▼  fan-out: event × matching subscriptions
  ops.webhook_outbound_deliveries    one row per (event × subscription)
     │                               (status, attempts, lease_*, idempotency_key)
     ▼  delivery worker  (FOR UPDATE SKIP LOCKED claim, lease, backoff, dead-letter)
  POST → subscriber URL              via internal/outbound.SafeClient (SSRF-hardened)
     │   X-VoltEdge-Signature (HMAC) + X-VoltEdge-Delivery (idempotency key)
     ▼
  external CIS / WMS                 verifies the signature, dedupes on the delivery id
```

Two stages after the outbox:
1. **Fan-out** — turn one outbox event into N per-subscription delivery rows (so each
   subscriber retries independently). A cheap step the worker runs before sending.
2. **Deliver** — the worker POSTs each delivery row, retries on failure, dead-letters
   after the cap.

---

## 3. Data model

**New: the outbox.**
```
ops.webhook_outbound_events
  id            uuid pk
  seq           bigserial         -- global order; per-entity ordering reads this
  event_type    text              -- 'oms.incident.raised', 'wam.workorder.completed', ...
  entity_kind   text              -- 'incident' | 'work_order' | 'fault' | 'feature' | ...
  entity_id     text              -- the entity's id (polymorphic, no FK)
  payload       jsonb             -- the event envelope's `data` (the changed record)
  actor_id      uuid              -- who caused it (audit)
  occurred_at   timestamptz default now()
  fanned_out_at timestamptz       -- null until the fan-out has created its deliveries
  index (fanned_out_at) where fanned_out_at is null   -- the claim index
```

**New: the deliveries** (a near-copy of `ops.webhook_deliveries`).
```
ops.webhook_outbound_deliveries
  id              uuid pk
  subscription_id uuid -> ops.webhook_endpoints(id)
  event_id        uuid -> ops.webhook_outbound_events(id)
  status          text   -- queued | delivering | delivered | failed | dead
  attempts        int
  worker_id       text
  lease_expires_at timestamptz
  next_attempt_at timestamptz     -- exponential backoff schedule
  response_code   int
  error           text
  idempotency_key text            -- == delivery id, sent so the receiver dedupes
  created_at / finished_at
  index (status, next_attempt_at)          -- the claim index
  index (subscription_id, created_at desc) -- the admin log
```

**Extend: subscriptions.** Reuse `ops.webhook_endpoints` with a new `direction =
'outbound'` (today: `pull` | `push`). The outbound slice of its `config` jsonb holds:
```
{ "target_url": "https://cis.example.com/hooks/voltedge",
  "event_types": ["oms.incident.raised", "wam.workorder.completed"],
  "class_filter": ["DIST_XFMR", ...],      // optional, for feature events
  "territory_id": "...",                    // optional scope
  "mapping": { ... } }                      // optional, reshape the envelope
```
The HMAC **signing secret reuses `signing_secret_enc`** (sealed via `secretbox`), same
as a receive endpoint's — here it signs our *outbound* body instead of verifying an
inbound one. One admin surface (`/webhook-endpoints`) now manages pull, receive, *and*
outbound.

---

## 4. Event capture — the `emit()` hook

A single helper, called inside the change's transaction:
```go
emit(ctx, tx, EventType, entityKind, entityID, payload)  // inserts one outbox row
```
Because it runs in the **same `tx`** as the `oms`/`wam`/`faults` write, the event and
the change commit or roll back together — an event is never emitted for a change that
didn't land, and a committed change always has its event. The write paths already know
the semantics, so they pick the precise `event_type` (a `publish` vs an `update`).

v1 hooks (the CIS/WMS case): the OMS incident lifecycle, WAM work-order + defect
lifecycle, and the Faults lifecycle. (Feature CRUD — `crud.CreateFeature`/`Update`/
`Delete`/publish — is the second wave; it's higher-volume and needs the class/territory
filters to be useful, so it's deliberately deferred.)

> **Coverage caveat (inherent to the chosen approach):** `emit()` only fires for
> writes that go *through* VoltEdge's engines. A direct SQL write or an out-of-band
> process won't emit. That is the accepted trade for precise semantics + a light build;
> the DB-trigger alternative would catch those but at a high maintenance cost. Documented
> so nobody is surprised.

---

## 5. Delivery pipeline

- **Fan-out:** claim an outbox row with `fanned_out_at IS NULL` (SKIP LOCKED), find the
  active `direction='outbound'` subscriptions whose `event_types` (and filters) match,
  insert one `webhook_outbound_deliveries` row each, stamp `fanned_out_at`. No match →
  just stamp it (recorded, nothing sent).
- **Deliver:** the worker claims `queued`/retry-due deliveries (SKIP LOCKED + lease,
  exactly the inbound worker's claim), builds the signed request, POSTs via
  `internal/outbound.SafeClient`, and records the outcome. A 2xx → `delivered`; a
  failure → `failed` with `next_attempt_at` set by **exponential backoff**; after the
  attempt cap → `dead` (dead-letter, surfaced for an admin to inspect/replay).
- **Ordering:** deliver a given entity's events in `seq` order (hold a later event for
  an entity until its earlier ones are delivered) — the per-entity ordering the inbound
  worker already does per device.
- **Idempotency:** the delivery id travels as `X-VoltEdge-Delivery`; a retry re-sends
  the same id, so the receiver dedupes.
- **Process:** run both stages in **`cmd/webhookworker`** (which already runs the
  inbound apply worker), or a sibling — same SKIP-LOCKED design, safe across replicas.

---

## 6. Payload & signing

```
POST <target_url>
  Content-Type: application/json
  X-VoltEdge-Event: wam.workorder.completed
  X-VoltEdge-Delivery: <delivery id>                 # idempotency key
  X-VoltEdge-Signature: sha256=<hmac(body, secret)>  # so the receiver trusts it

  { "event_id": "...", "event_type": "wam.workorder.completed",
    "occurred_at": "2026-10-08T12:00:00Z", "actor": "...",
    "entity": { "kind": "work_order", "id": "..." },
    "data": { ...the changed record... } }
```
An optional per-subscription **mapping** (the inverse of the inbound `mapping.go`)
reshapes this envelope into whatever the receiver expects, so an integration that can't
consume our shape still works.

---

## 7. Security

- **SSRF-hardened** outbound via `internal/outbound.SafeClient` — so a subscriber URL
  can't be loopback/private (the same localhost-is-blocked caveat as the external-
  systems proxy; a public URL / tunnel is required).
- **HMAC-signed** body with a per-subscription secret sealed via `secretbox`
  (`EXTERNAL_SYSTEMS_ENCRYPTION_KEY`-style key), so the receiver verifies authenticity
  and VoltEdge's secret never leaves the server.
- **Scope:** an optional `territory_id` filter on a subscription emits only events in
  that territory, so a regional partner hears only its own area.
- **Admin-gated:** managing outbound subscriptions is `metadata:write`, same as the
  rest of the webhook admin.

---

## 8. Admin surface

Extend the existing `/webhook-endpoints` admin API + page:
- Create/edit an endpoint with `direction='outbound'`, its `target_url`, `event_types`,
  filters, and signing secret.
- **Test-ping** (`POST /webhook-endpoints/{id}/ping`) — send a synthetic event to the
  URL and show the outcome: the outbound analogue of the inbound `/pull` test, and the
  "does the connection work" check.
- **Delivery log + replay** — reuse the inbound log UI against
  `webhook_outbound_deliveries`; replay re-enqueues a dead delivery.

---

## 9. Reuse vs new

| Piece | Reused | New |
|-------|--------|-----|
| Delivery queue + lease/attempts/retry worker | inbound `webhook_deliveries` + `worker.go` | outbound tables + fan-out stage |
| SSRF-hardened POST | `internal/outbound.SafeClient` | — |
| Sealed signing secret | `internal/secretbox` + `webhook_endpoints.signing_secret_enc` | — |
| Admin surface, delivery log, replay | `/webhook-endpoints` handler | `direction='outbound'` + subscription fields + `/ping` |
| Change capture | — | the outbox + `emit()` in the write paths |
| Payload mapping | the inbound `mapping.go` concept | its inverse (envelope → receiver shape) |

---

## 10. Slice plan

- **S1 — the outbox.** `ops.webhook_outbound_events` + the `emit()` helper, wired into
  the **OMS / WAM / Faults** write paths within their change transactions. Verify event
  rows appear atomically with the change (and roll back with it). No delivery yet — a
  pure, testable capture slice.
- **S2 — delivery. BUILT (2026-10-08).** `ops.webhook_outbound_deliveries` (migration
  0134) + outbound subscriptions (`direction='outbound'` on `webhook_endpoints`, accepted
  by the existing admin create/update) + the fan-out + the delivery worker (SKIP LOCKED,
  exponential backoff, lease heartbeat, stuck-delivery reaper, panic guard, dead-letter)
  + HMAC signing → real POSTs through `internal/outbound.SafeClient`. All in the new
  `internal/webhookout` files (`deliveries.go`, `subscriptions.go`, `signing.go`,
  `fanout.go`, `send.go`, `worker.go`, `errors.go`); both the inbound and outbound workers
  now run in `cmd/webhookworker`. Per-entity ordering is enforced two ways: fan-out is
  strictly sequential by `seq` (plain `FOR UPDATE`), and the delivery claim holds a row
  while any earlier-seq delivery for the same entity is still queued/running. This is the
  working end-to-end pipeline.
- **S3 — admin.** The outbound-subscription admin UI (pick event types, filters,
  secret), the **test-ping**, and the delivery log/replay reused against the outbound
  table.
- **S4 — feature events + mapping.** Add the `crud` feature-CRUD events + the
  class/territory filters that make them usable, and the optional per-subscription
  mapping.

S1–S2 is the whole capability (operational events delivered reliably); S3 makes it
self-service; S4 widens coverage.

---

## 11. Open defaults (confirm or adjust)

- **v1 events = operational** (OMS incidents, WAM work orders + defects, Faults). This
  is the default because it's the CIS/WMS reaction case and is low-volume. **Feature
  CRUD events are S4**, not v1 — flag if you want them sooner.
- **At-least-once + idempotency key** (not exactly-once) — the standard, and all a
  well-behaved receiver needs.
- **Per-entity ordering on** (hold a later event until earlier ones for that entity
  deliver). Turn it off if a receiver doesn't care about order and you want max
  throughput.

## 12. Non-goals / future

- **DB-trigger / CDC capture** of raw-SQL and out-of-band writes (the coverage gap
  noted in §4) — a bigger, separate effort if it's ever needed.
- **Delivery to message brokers** (Kafka/SNS) rather than HTTP — a transport add-on.
- **Replay-by-time / backfill** (re-emit all events since a timestamp) — useful for a
  subscriber recovering from downtime; a later enhancement.
