# VoltEdge Integrations & External Systems Guide

_Last updated: 2026-10-28_

A how-to manual for connecting VoltEdge to the **other systems that run your utility** — SCADA, AMI/metering, a CIS/billing platform, a work-order system, a document store — so a feature on the map knows what it is called everywhere else, can pull live data on demand, and stays in step when another system pushes a change.

This guide has two audiences that meet in the middle. **Administrators** stand up the plumbing — they register the external systems and configure the inbound webhooks, which is system configuration and needs `metadata:write`. **Operators** (editors and analysts) use that plumbing from the map — they link a feature outward and tag it with its id in another system, which is ordinary feature editing and needs `features:write` (reading rides `features:read`). Both jobs are covered here; the administrative sections are marked so you can skip them if the integration wiring is someone else's responsibility.

**The recurring cast.** To keep examples concrete this guide reuses the house cast: **Ada** (system administrator) registers external systems and webhook endpoints; **Tom** (network analyst) tags features with their ids in other systems and pulls live data; **Dana** (editor) attaches the occasional reference link. The running assets are the shared **Feeder 7** (11 kV, Oakwood Zone): a `DIST_XFMR` monitored by **SCADA**, a `SERVICE_POINT` whose consumption comes from **AMI** and whose customer sits in **CIS**.

**Legend.** 🔒 marks an action that needs a specific permission (named inline). 🗺️ marks something that also depends on your **territory access**. 💡 is a tip, ⚠️ a caution. **▶** introduces a "how to operate it" workflow; **∑** the "how it actually works" explanation you can read or skip.

---

## Chapter 1 — The integration surface

VoltEdge is the system of record for the *network*, but it is never the only system that knows about an asset. The same transformer is a tag in SCADA, the same service point is a meter in AMI and an account in CIS, the same pole is a job in a work-management system. Integration is about keeping those identities and that data joined up without copying everything into one database.

VoltEdge gives you **four building blocks**, which you'll usually set up in this order:

| # | Block | Direction | Who sets it up | What it does |
| --- | --- | --- | --- | --- |
| 1 | **External systems catalog** | — | Administrator (`metadata:write`) | Registers a system once: its base URL, how to authenticate, and a live-fetch path template. The thing every other block points at. |
| 2 | **External links** | Outbound | Operator (`features:write`) | Per-feature pointers — a plain hyperlink, or a "live data" link that fetches a stored address through VoltEdge's proxy. |
| 3 | **External references** (`external_ref`) | Both | Operator (`features:write`) | Records *this feature's id in that system* on the feature itself. The correlation source of truth — it drives both the live fetch and inbound matching. |
| 4 | **Webhooks** | Inbound (+ pull & outbound) | Administrator (`metadata:write`) | Lets another system **push** changes into VoltEdge (or VoltEdge pull from it), correlated back to the right feature. |

Two directions run through all of this:

- **Outbound / pull** — *VoltEdge asks another system for data.* You stored an address or an id; VoltEdge fetches through a hardened proxy and shows you the answer. Nothing is persisted; it's a live read.
- **Inbound / push** — *another system tells VoltEdge something changed.* A webhook delivery arrives, is authenticated, and is applied to the matching feature through the ordinary write path.

The connective tissue between the two is the **external reference**: the id a feature carries in another system. Set it once and it serves both directions — it builds the outbound fetch URL, and it lets an inbound push find the feature it belongs to instead of creating a duplicate.

💡 Everything that reaches outside VoltEdge — a live fetch, a pull — goes through a **server-side proxy that is hardened against SSRF** (it refuses private/loopback/link-local targets and keeps requests on the registered origin). The browser never talks to the external system directly, and a credential never leaves the server. More in Chapter 7.

---

## Chapter 2 — Registering an external system (the catalog)

🔒 *Administrative — needs `metadata:write`. Managed at **Admin → External systems**.*

Everything else points at a catalog entry, so this is the first thing Ada sets up. A catalog entry is the stable description of one external system: where it lives, how to authenticate to it, and — optionally — how to turn a feature's id into a live-fetch URL.

**▶ Create a system.** On **Admin → External systems**, press **New system** and fill in:

| Field | Meaning |
| --- | --- |
| **Code** | A short, stable identifier, uppercased (e.g. `SCADA`, `AMI`, `CIS`). This is the key a feature uses to reference the system, so pick it carefully — it's awkward to change later. |
| **Name** | A human label (e.g. *Realtime SCADA*). |
| **Base URL** | The absolute `http(s)://host/...` root every request is built on (e.g. `https://cis.example.com/api`). Must include a host; validated on save. |
| **Authentication** | `None`, `API key (header)`, `Basic (username + password)`, or `Bearer token`. |
| **Header name** | For API-key auth: the header the key is sent in (e.g. `X-API-Key`). |
| **Username** | For Basic auth. |
| **Secret** | The API key / password / bearer token. **Write-only** — see below. |
| **Response format** | `JSON` or `XML (legacy)`. XML responses are decoded into a nested object so the viewer renders them the same way. |
| **Live-data path template** | *Optional.* The path (appended to the base URL) a live fetch uses, with `{placeholder}` fields filled from a feature's reference — e.g. `/meters/{id}/consumption`. Leave blank for a system that only receives pushes or is used only for plain hyperlinks. |

**∑ How the credential is stored.** The secret is **sealed with AES-256-GCM** (`api/internal/secretbox`) using a single key the server reads from the `EXTERNAL_SYSTEMS_ENCRYPTION_KEY` environment variable, and written to a `credential_enc` column. It is **never returned** to the browser — a system card shows only *credential set* or *no credential*. On edit, leaving the secret blank keeps the stored one; typing a new one re-seals it. ⚠️ If the server has no encryption key configured, storing a credential is refused outright with a clear error rather than saved in the clear — set the key before registering an authenticated system.

**∑ What the path template is for.** A template turns *a feature's id in this system* into a request. `{id}` (and `{ref}`) are filled from a scalar reference; named placeholders like `{point}` are filled from an object reference's fields. The filled path is re-validated to be **relative** before the fetch, so a malformed id can't smuggle in a different host (Chapter 4 and Chapter 7). A push-only system needs no template.

💡 You can register a system with `Authentication: None` and no template purely to **name an inbound correlation source** — e.g. a `SCADA` entry whose only job is to be the system code an inbound webhook correlates against (Chapter 5). The catalog is the registry of *who* a feature can be joined to, regardless of whether VoltEdge ever calls out to it.

---

## Chapter 3 — Linking a feature outward

🔒 *Open to any map user with `features:read`; adding or removing a link needs `features:write`. On the feature panel's **Links** tab.*

The simplest join is a **link**: a per-feature pointer to something in another system. A feature can carry as many as you like. Open a feature, go to the **Links** tab, press **Add link**, and choose a kind.

**Hyperlink.** A label and an absolute `http(s)://` URL. The row opens it in a new tab (with `rel="noopener noreferrer"`, so the external page never gets a handle back to the VoltEdge window). This is Dana's go-to for "here's the GIS-adjacent document / dashboard for this asset" — no catalog entry needed.

**Live data** *(api link).* A label, a catalogued **system**, and a **resource path**. Instead of opening a page, the row has a **Load** button that fetches the data **server-side** through the proxy and shows the normalized payload as an expandable card; **Refresh** re-fetches.

- The **resource path must be relative** — no scheme or host. It is resolved against the system's base URL (e.g. system `CIS` + path `accounts/8842` → `https://cis.example.com/api/accounts/8842`). An absolute URL is rejected, which is a deliberate SSRF guard.
- The fetch rides `features:read`, so anyone who can open the feature can read its live data; only **creating** an api link needs the catalog to be visible (`metadata:read`), so a feature editor who can't see the catalog can still add plain hyperlinks and load existing api links.

**∑ Link vs reference — when to use which.** A link is a *stored pointer you chose per feature* ("fetch exactly this path"). A **reference** (next chapter) records *the feature's identity* in a system and lets VoltEdge build the path for you from a template — and, unlike a link, it also works **inbound**. Rule of thumb: a one-off document or a bespoke endpoint → **link**; "this feature is meter 48213 in AMI" → **reference**.

---

## Chapter 4 — External references (`external_ref`)

🔒 *Viewing rides `features:read`; adding or removing a reference needs `features:write` (plus 🗺️ territory write access to the feature). On the feature panel's **External references** section, under the **Links** tab.*

A reference answers one question for one feature: ***"what is this called in that other system?"*** Tom opens the Feeder 7 `SERVICE_POINT`, scrolls to **External references**, presses **Add**, picks **AMI** from the system list, types the meter number `48213`, and saves. The service point now carries its AMI identity.

**∑ What's actually stored.** Each feature has a single `external_ref` JSONB **system column** (added to every class by the compiler, like `deleted_at` or `territory_id`), shaped:

```json
{ "AMI": "48213", "CIS": "8842" }
```

The key is a catalog **system code**; the value is either a **scalar string** (the id) or an **object** whose `id` field is the correlation id and whose other fields fill a richer URL template:

```json
{ "SCADA": { "id": "FDR7.TX1", "point": "MW" } }
```

`external_ref` is the **source of truth**. It's what the live fetch reads, and what inbound correlation matches against — set it in one place and both directions follow.

**▶ Pull live data.** If the reference's system has a **path template**, the reference row shows a **Live data** button. Tom presses it; VoltEdge fills the template from the reference value, fetches through the proxy, and shows the payload inline — exactly like a live-data link, but with the address built from the id rather than typed. With AMI's template `/meters/{id}/consumption` and the scalar ref `48213`, the request path is `/meters/48213/consumption`. For an object ref, `{id}` comes from the `id` field and named placeholders from the sibling fields (so SCADA's `/points/{id}/{point}` becomes `/points/FDR7.TX1/MW`). A referenced placeholder the value doesn't provide is a clear error, never a silently wrong URL; every substituted value is path-escaped.

**∑ The reverse index.** Besides living on the feature, every reference is mirrored into a small trigger-maintained table, `runtime.external_feature_refs`, keyed by `(system_code, external_id)`. That's what lets an inbound push ("device 48213 changed") resolve to the right feature in one hit across all 60-odd classes without scanning. You never write it by hand — a database trigger derives it from `external_ref` on every write, and a **soft-deleted feature drops out of the index** (so a deleted feature can't be matched inbound; restoring it re-indexes). This is the same pattern as the feature registry.

💡 Editing is a **whole-map replace** under the hood: the panel shows the current set, and add/remove sends the next set. An empty map clears every reference. Each value must be a non-empty id string or an object; the server validates the shape before writing.

⚠️ One reference per system per feature — the add picker only offers systems the feature doesn't already reference (type a brand-new code if the system isn't catalogued yet). If two features legitimately share an external id *in different systems* that's fine; the same id reused *within one system* across classes stays separate (inbound correlation only adopts a feature of the matching class — Chapter 5).

---

## Chapter 5 — Receiving inbound pushes (webhooks)

🔒 *Administrative — needs `metadata:write`. Managed at **Admin → Webhooks**.*

The blocks so far are VoltEdge reaching out. A **webhook endpoint** is the reverse: another system **pushes** a change and VoltEdge applies it. Ada configures one endpoint per sender.

### 5.1 The shape of an endpoint

**▶ Create an endpoint.** On **Admin → Webhooks**, press **New** and set:

| Field | Meaning |
| --- | --- |
| **Name** | A label for the sender (e.g. *CIS account sync*). |
| **Direction** | `push` (another system calls us — this chapter), `pull` (we fetch from it — Chapter 6), or `outbound` (we emit to it — Chapter 7). |
| **Target** | What the delivery drives: `feature`, `reading`, `work`, `fault`, `operation`, `outage`, or a read-only **mirror** (`incident`, `workorder`) — hidden for outbound. See the table below, and Chapter 7½ for the mirror targets. |
| **Default feature class** | For `feature`/`reading`: the class used when a change doesn't name one (e.g. `SERVICE_POINT`). |
| **Service user** | *Required.* Ingested writes run **as this user**, so they inherit that user's RBAC and territory write-scope, and history attributes the change to them. |
| **Auth mode** | `hmac`, `bearer`, or `both` — how a delivery proves it's genuine (§5.3). |
| **Signing secret** | For HMAC: the shared secret. Write-only and sealed, exactly like a catalog credential. |
| **System code** | *Optional but important.* The catalog system this sender's ids belong to (e.g. `CIS`) — this is what ties inbound deliveries to `external_ref` (§5.5). |
| **Field mapping** | *Optional, advanced.* A spec that transforms a non-native sender's JSON into VoltEdge's change shape (§5.6). |
| **Active** | Uncheck to park an endpoint without deleting it. |

Each endpoint has its own **receive URL**: `POST /webhooks/{id}` (the admin page shows the full URL to hand to the sender). The receiver is **not behind the normal login** — each endpoint authenticates itself with its own credential — and it's rate-limited with a 1 MiB body cap. A missing, inactive, or non-push endpoint all answer the same `404`, so the URL isn't an existence oracle.

| Target | Drives | Notes |
| --- | --- | --- |
| `feature` | `CreateFeature` / `UpdateFeature` / `DeleteFeature` | The exact hand-edit path — subtype rules, required-attribute checks, territory write-scope and history all apply. |
| `reading` | the same path | A reading is modelled as a feature row; a thin alias of `feature`, declared separately so intent is explicit. |
| `work` | the WAM engine | Creates/updates VoltEdge-**owned** work orders from the feed. Correlated via the work/fault entity-ref table. |
| `fault` | the Faults engine | Same correlation table. |
| `operation` | the device-operations log | Records a switching/operation event against a device. |
| `outage` | the OMS | **Raises VoltEdge's own incident** from a device signal (SCADA trip / AMI last-gasp) — VoltEdge then runs its own OMS. |
| `incident` | the OMS (read-only **mirror**) | Reflects a **full external incident** into a display-only shadow VoltEdge never edits or re-emits. The external OMS owns its lifecycle. *(Chapter 7½.)* |
| `workorder` | the WAM (read-only **mirror**) | The WAM sibling of `incident`: reflects a full external work order into a read-only shadow. The external WAM owns its lifecycle. *(Chapter 7½.)* |

💡 `outage` vs `incident` is the key distinction: `outage` ingests a **signal** and VoltEdge becomes the system of record for the incident it raises; `incident` ingests a **finished record** and the external system stays the system of record — VoltEdge only shows it. Likewise `work` (VoltEdge owns the order) vs `workorder` (the external WAM owns it).

### 5.2 What a delivery looks like

A delivery is one or more **changes**. The native shape of a change:

```json
{
  "op": "upsert",
  "external_id": "8842",
  "feature_class": "SERVICE_POINT",
  "name": "12 Oakwood Rise",
  "attributes": { "tariff": "D1" }
}
```

- **`external_id`** *(required)* — the sender's own stable id for the thing. This is the correlation key: a repeat delivery with the same `external_id` is an **update**, not a duplicate.
- **`op`** — `upsert` (default; create-or-update, last-writer-wins with prior values kept in history) or `delete` (retire the feature and drop the mapping; a no-op if already gone, so deletes are idempotent). Anything else is a `400`.
- **`feature_class`** — falls back to the endpoint's **Default feature class** when omitted.
- **`subtype`, `name`, `territory_id`, `geometry`, `attributes`** — optional, same meanings as a hand edit.

Send one bare change object, or a batch under an envelope: `{"changes":[ ... ]}`.

### 5.3 Proving a delivery is genuine

**HMAC** (recommended). The sender signs each request:

```
X-VoltEdge-Signature:  hex( HMAC-SHA256( secret, timestamp + "." + body ) )
X-VoltEdge-Timestamp:  <unix seconds>
```

VoltEdge recomputes the MAC over `timestamp + "." + rawBody` and compares in constant time. A `sha256=` prefix on the signature is tolerated. The **timestamp must be within 5 minutes** of now — an out-of-window request is rejected even with a valid signature, which defeats replay. Every HMAC failure returns the same vague `401 invalid signature` (it never tells an attacker *why*).

**Bearer.** `Authorization: Bearer <token>`, where the token must resolve to the endpoint's configured **service user**. **Both** accepts either, routing on whether the signature header is present.

💡 Add an **`Idempotency-Key`** header and a replayed delivery is de-duplicated: the receiver returns the original delivery id at `200` with `{"deduped":true}` instead of applying it twice. A fresh delivery returns `202 Accepted` — the receiver only authenticates and enqueues; an out-of-process worker does the apply.

⚠️ The receiver stores an audit row for every delivery but **never stores the `Authorization` header or the raw signature** — only non-secret metadata (content type, user agent, timestamp, idempotency key, and a `signature_present` flag).

### 5.4 Operating an endpoint

The endpoint's detail view gives Ada the tools to run it:

- **Deliveries** — the inbound log (newest 100): status (`queued`/`running`/`succeeded`/`failed`/`dead`), source, attempts, and the per-item result or error.
- **Test** — a paste-and-apply tester: paste a payload and it records a `manual` delivery and applies it **inline**, returning the per-item result so you can validate mapping and auth before going live. (A failing test dead-letters rather than retrying.)
- **Replay** — re-enqueue a stored delivery's payload as a new delivery (the original row stays as the audit trail; idempotency keys are dropped so it isn't deduped against the original).

### 5.5 Correlation — adopt, don't duplicate

This is where webhooks meet `external_ref`. Set the endpoint's **System code** (e.g. `CIS`) and inbound deliveries correlate against the reverse index:

- **First sight of an `external_id`** — VoltEdge checks `runtime.external_feature_refs` for a feature of this endpoint's class already claiming that id in its `external_ref`. If one exists, it **adopts** it (updates it in place) instead of creating a duplicate, and records the mapping so next time is a direct hit.
- **A genuine new feature** — VoltEdge creates it and **stamps its `external_ref`** with `{system_code: external_id}`, so the created feature immediately becomes the correlation source of truth and is live-fetchable.

So if Tom has already tagged the Feeder 7 service point as `CIS` account `8842` (Chapter 4), the first CIS push for `8842` updates *that* service point — the two systems join up with no duplicate. Leave System code blank and the endpoint falls back to its own per-endpoint correlation only (the historic behaviour).

### 5.6 Senders that don't speak VoltEdge

Most systems won't emit VoltEdge's native change shape. The **field mapping** spec bridges them — stored on the endpoint, applied *before* the adapter:

```json
{
  "items_path": "data.records",
  "fields": {
    "external_id": { "path": "meterNo" },
    "op":          { "const": "upsert" },
    "name":        { "path": "address" },
    "attributes":  { "fields": { "tariff": { "path": "rateCode", "map": { "R": "D1" } } } }
  }
}
```

- **`items_path`** — dot-path to the array of records (empty = the whole body is one record).
- **`fields`** — each native field is a `const` (literal), a `path` (dot-path into the sender's JSON), or nested `fields` (for `attributes`), with an optional `map` value-translation table and a `default`.

The spec is decoded strictly (unknown keys are rejected at configure time), so a typo is caught when you save the endpoint, not at 2 a.m. during a delivery.

---

## Chapter 6 — Pull endpoints (VoltEdge fetches on a schedule)

🔒 *Administrative — `metadata:write`.*

A **pull** endpoint inverts a push: instead of waiting to be called, VoltEdge **fetches** from the sender. Set **Direction: pull** and provide a **pull URL** and its auth (none / bearer / api-key / basic; the secret rides the same sealed column). Press **Pull now** on the endpoint to fetch the URL through the SSRF-hardened outbound client and apply the result inline — handy for a system that can expose a feed but can't call out. The payload, mapping, targets and correlation are all identical to a push endpoint; only the trigger differs.

---

## Chapter 7 — Outbound events (VoltEdge notifies others)

🔒 *Administrative — `metadata:write`.*

The fourth direction is VoltEdge **emitting** to another system when something changes. Set **Direction: outbound** and configure a **target URL**, the **event types** and **feature-class / territory filters** that select what to send, a **signing secret**, and an **envelope mapping** to shape the payload the receiver expects. VoltEdge signs each outbound delivery with `X-VoltEdge-Signature` (and tags it with `X-VoltEdge-Delivery`), mirroring the inbound HMAC scheme so the receiver can verify it the same way. The endpoint view adds a **Ping** button (send a synthetic event), an **outbound delivery log**, and per-delivery **replay** — the same operability as the inbound side.

💡 Outbound is the counterpart to inbound: inbound keeps VoltEdge in step with the world; outbound keeps the world in step with VoltEdge. A full round-trip integration usually runs both — an inbound endpoint from CIS and an outbound one back to it — joined by the `external_ref` on each feature.

---

## Chapter 7½ — Mirroring an external OMS or WAM (read-only)

🔒 *Administrative — `metadata:write`. Target `incident` or `workorder`.*

Chapters 5–7 assume VoltEdge is a **participant**: it ingests signals, runs its own OMS/WAM, and may own the record. Sometimes the opposite is true — **another system is the authoritative record** and VoltEdge's job is only to *show* it on the network, beside its own incidents and work orders. That's a **read-only mirror**.

A mirror endpoint reflects a **full external record** (its own id, status, and fields) into a display-only shadow that VoltEdge renders but **never edits and never pushes back out**. Two targets:

| Target | Mirrors | Migration |
| --- | --- | --- |
| `incident` | an external **OMS** incident → `oms.incidents` | 0181 |
| `workorder` | an external **WAM** work order → `wam.work_orders` | 0182 |

### 7½.1 What a mirror guarantees

Three properties hold **by construction** — they are not toggles you can get wrong:

- **Read-only.** Every local lifecycle action refuses a mirrored record. A mirrored incident can't be confirmed, dispatched, attributed, restored, cancelled, given an ETR, or have a crew/subcontractor/switching order raised on it; a mirrored work order can't be started, completed, cancelled, or edited. The map and board show it, greyed to view-only, with a **"read-only mirror · `<system>`"** badge.
- **No echo.** A mirrored record is never re-emitted by the outbound engine (Chapter 7), so it can't loop back to the system that owns it.
- **Reliability- and crew-safe.** A mirrored incident **does not enter VoltEdge's SAIDI/SAIFI** (its own OMS computes reliability; VoltEdge freezes interruptions only from *its* topology). A mirrored work order **touches no crews or field packs** — you can't dispatch work you don't own. VoltEdge shows the *external* system's numbers.

### 7½.2 Configuring a mirror endpoint

On **Admin → Webhooks → New**, pick **Direction: push** (or `pull`) and **Target: Incident** or **Work order**. Two mirror-specific settings appear:

| Field | Meaning |
| --- | --- |
| **External system code** *(required)* | The owning system's code (e.g. `acme-oms`). Stamped on every mirrored record as its `origin_system`, used to namespace its `external_id`, and used to resolve the referenced device/asset through `external_ref` (Chapter 4). |
| **Status map** | Rows of `external state → VoltEdge status`. Translate the vendor's lifecycle into VoltEdge's. An external state you don't map is accepted **only if it is already a VoltEdge status**; anything else fails that item loudly (so a surprise state is caught, never silently dropped). |

VoltEdge statuses to map onto:

- **Incident:** `predicted`, `scheduled`, `confirmed`, `dispatched`, `restored`, `cancelled`.
- **Work order:** `draft`, `scheduled`, `assigned`, `in_progress`, `completed`, `cancelled`.

### 7½.3 What a mirror delivery looks like

A mirror change carries the external record's **identity, status, and fields** — not a feature edit. An incident example:

```json
{
  "op": "upsert",
  "external_id": "INC-4471",
  "status": "DISPATCHED",
  "outage_type": "unplanned",
  "device_external_id": "FDR7-RECLOSER-2",
  "customers_affected": 318
}
```

- **`external_id`** *(required)* — the external system's own id for the record. A repeat delivery with the same id is an **update** of the same shadow (keyed on `origin_system + external_id`), never a duplicate.
- **`op`** — `upsert` only. A close is an upsert carrying `status` = `restored`/`cancelled` (incident) or `completed`/`cancelled` (work order); there is no hard delete.
- **`status`** — run through the endpoint's **status map** (here `DISPATCHED` → `dispatched`).
- **`device_external_id` / `asset_external_id`** — resolved to a VoltEdge feature via `external_ref` so the mirrored record draws on the map; optional (a record can be location-less).

As with any endpoint, a sender that doesn't speak this shape can be bridged with a **field mapping** (§5.6), applied before the adapter.

💡 **Participant or mirror?** Mirror when the other system is the source of truth and VoltEdge is a viewer (don't double-count outages, don't task crews twice). Use the participant targets (`outage`, `work`) when VoltEdge should own and run the incident or order itself.

---

## Chapter 8 — A round trip, end to end

Putting the blocks together on Feeder 7:

1. **Ada registers `CIS`** in the catalog (base URL, bearer token, template `/accounts/{id}`), and **registers `AMI`** (api-key auth, template `/meters/{id}/consumption`). *(Chapter 2.)*
2. **Tom tags the service point** — `external_ref = { "CIS": "8842", "AMI": "48213" }` — from the External references section. *(Chapter 4.)*
3. **Tom pulls live data** — the AMI reference's **Live data** button fetches `/meters/48213/consumption` through the proxy and shows today's kWh; the CIS reference fetches the account. *(Chapter 4.)*
4. **Ada stands up a CIS webhook** — target `feature`, default class `SERVICE_POINT`, system code `CIS`, HMAC auth. *(Chapter 5.)*
5. **CIS pushes an update** for account `8842` (a tariff change). VoltEdge authenticates it, finds the service point already claiming `CIS:8842` in its reverse index, and **updates that feature** — no duplicate, full history, attributed to the webhook's service user. *(Chapter 5.5.)*
6. *(Optional)* **An outbound endpoint back to CIS** notifies it whenever the service point's address changes in VoltEdge. *(Chapter 7.)*

The service point is now a single asset that reads correctly in all three systems, pulls fresh data on demand, and absorbs changes from CIS without drifting.

---

## Chapter 9 — Automating with the assistant / MCP

Every one of these feature-level actions has an agent tool (the same functions the REST routes and the panel call), so the in-app assistant or an external MCP client can drive them under the caller's own permissions:

| Tool | Does | Needs |
| --- | --- | --- |
| `feature_link_list` | List a feature's links | `features:read` |
| `feature_link_create` / `_update` / `_delete` | Manage links | `features:write` |
| `feature_external_refs_get` | Read a feature's `external_ref` map | `features:read` |
| `feature_external_refs_set` | Replace a feature's `external_ref` map (whole-map replace) | `features:write` |

⚠️ The **live-data fetch is deliberately not an agent tool** — it needs the encryption key and makes an outbound call, neither of which the stateless tool registry supplies, so the agent surface stays database-only (read/set the ids; a human or the panel does the live fetch). This mirrors the same decision for stored api links.

---

## Chapter 10 — Security & operations notes

- **SSRF hardening.** Every outbound call (live-data link, external-ref fetch, pull) goes through a proxy that keeps the request relative to the registered base URL and refuses private/loopback/link-local/CGNAT targets at the network layer. A filled path template is re-validated to be relative, so a hostile id can't repoint the request.
- **Credentials at rest.** System credentials and webhook signing secrets are sealed with AES-256-GCM (`EXTERNAL_SYSTEMS_ENCRYPTION_KEY`), write-only, surfaced only as a boolean. With no key configured, the app refuses to store a secret rather than storing it in the clear.
- **Replay & idempotency.** Inbound HMAC carries a 5-minute timestamp window; `Idempotency-Key` de-duplicates retried deliveries.
- **Territory & draft scope.** Reading or writing a feature's links/refs is gated exactly like reading or writing the feature: 🗺️ an out-of-territory or soft-deleted feature's references are not listable, and tagging a feature you can't write to is refused. Inbound writes run as the endpoint's service user and inherit that user's scope.
- **Deleted features leave the index.** A soft-deleted feature drops out of `runtime.external_feature_refs`, so it can never be adopted by an inbound push; restoring it re-indexes it.

---

## Appendix — Quick reference

**Permissions.** Catalog & webhooks → `metadata:write`. Feature links & references → `features:write` (view: `features:read`).

**HTTP surface.**

| Route | Method | Purpose | Perm |
| --- | --- | --- | --- |
| `/external-systems` | GET / POST / PATCH `/{code}` / DELETE `/{code}` | The catalog | `metadata:write` |
| `/feature-links` | GET `?feature_class=&feature_id=` | List a feature's links | `features:read` |
| `/feature-links` | POST | Create a link | `features:write` |
| `/feature-links/{id}/data` | GET | Fetch an api link's live data | `features:read` |
| `/feature-links/{id}` | PATCH / DELETE | Rename-repoint / remove a link | `features:write` |
| `/feature-links/external` | GET `?feature_class=&feature_id=&system=` | Live data from a reference | `features:read` |
| `/feature-links/external-refs` | GET `?feature_class=&feature_id=` | A feature's `external_ref` map | `features:read` |
| `/feature-links/external-refs` | PUT | Replace a feature's `external_ref` map | `features:write` |
| `/webhook-endpoints` | GET / POST / PATCH / DELETE `/{id}` + `/{id}/deliveries`, `/test`, `/pull`, `/replay`, `/ping` | Manage endpoints | `metadata:write` |
| `/webhooks/{id}` | POST | **Public receiver** (per-endpoint credential) | per-endpoint auth |

**Inbound webhook headers.** `X-VoltEdge-Signature` = `hex(HMAC-SHA256(secret, timestamp + "." + body))`, `X-VoltEdge-Timestamp` (unix seconds, ±5 min), `Idempotency-Key` (optional, de-dup), or `Authorization: Bearer <token>` (must be the service user).

**`external_ref` shape.** `{ "<SYSTEM_CODE>": "<id>" }` or `{ "<SYSTEM_CODE>": { "id": "<id>", "<field>": "<value>" } }`.
