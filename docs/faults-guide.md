# Faults — Logging & Managing Fault Events

**Written for:** control-room operators logging and tracking electrical fault events, network engineers investigating them, and field crew who clear a fault and book the follow-up repair.

A **fault** here is an **operational record of an electrical fault event** that happened on the network — an earth fault, a phase-to-phase short, a protection operation — *about* the assets on the map, **not** a feature drawn on it. It's the causal hinge between an **outage** (the OMS incident the fault explains) and a **defect** (the WAM repair the fault triggers):

```
   OMS incident  ──explains──►  FAULT EVENT  ──triggers──►  WAM defect ──► work order
   (the outage)                 (this guide)                (the repair)
```

This guide has two companions it **references rather than repeats**: the **OMS guide** for the outage/incident side, and **Works, Inspections & Maintenance** for the defect/work-order side. For the *protective devices* that clear a fault (and the de-energised cone it leaves) see **Switching & Protection**; for the *fault-current calculation* (a different thing entirely — see §1) see **Engineering Studies**. It's written in two layers:

- **▶ Operator** — what to record, how to drive a fault through its lifecycle, what to do about it.
- **∑ Engineer** — the record model, its columns and constraints, the endpoints and the module's links, with their source in the migration and Go code.

**The cast.** **Priya** (control-room operator) logs faults as they come in off SCADA and customer calls and drives their status; **Dana** (field supervisor) clears a fault on the ground and books the repair; **Tom** (analyst) compares a recorded fault current against what the engine predicted; **Ada** (admin) holds the permissions. The running example is an earth fault on **Feeder 7** (the same feeder as the Switching & Protection guide): a cable dig-in on the tee below the fused switch **FSW-12** that feeds distribution transformer **TX-220**.

**Legend.** 🔒 a permission (named inline). 💡 a tip. ⚠️ a caution or a limitation stated honestly. **▶** the operate lane; **∑** the model lane. A boxed **How to** is a click-by-click procedure; a **Worked example** is reproducible.

> **Grounding note.** Everything here is drawn from the shipped code: schema in `migrations/0106_create_faults.sql` (+ `0107` permissions, `0109` history); engine in `api/internal/faults` (`store.go`, `http.go`, `geometry.go`, `history.go`); UI in `web/src/routes/faults.tsx`, `web/src/hooks/use-faults.ts`, `web/src/components/map/feature-faults-tab.tsx`. A fault record is **not** a compiled `gis.*` class.

---

## 1. What a fault record is — and what it is *not*

VoltEdge has **two** completely separate things with "fault" in the name. Confusing them is the first trap:

| | **Faults module** (this guide) | **Fault-current engine** (Engineering Studies) |
|---|---|---|
| What | a persistent **record** of a fault event that *happened* | a live **calculation** of the bolted fault current that *would* flow |
| Lives in | schema `faults`, package `api/internal/faults` | stateless, `api/internal/powerflow` |
| You call | `POST /faults` (log it) | `POST /analysis/fault-current` (compute it) |
| Permission | `faults:read` / `faults:write` | `analysis:run` |
| Produces | a row with cause, device, clearing time, a status lifecycle, links to the outage and the repair | a number per node — the symmetrical / SLG fault current |

They share only vocabulary: a fault **record** carries a `fault_type` (`SLG`, `LL`, …) and a `fault_current_a` drawn from the same classification the engine speaks, so you can compare *what was measured* against *what the model predicted* — but there is **no code dependency** between them. Neither reads the other.

**A fault is a record, not a map feature.** It is a row in `faults.fault_events`, not a `gis.<class>` the compiler builds. It *points at* the faulted asset by a loose reference (§6), and it can carry an optional location pin, but it is never something you draw or trace through.

---

## 2. Who may do what

Two permissions gate the whole module (`migrations/0107_seed_faults_permissions.sql`; constants `auth/rbac.go`), enforced method-aware on the `/faults` mount (`main.go:279` — `GET` needs read, everything mutating needs write):

| Action | Permission | viewer | analyst (Tom) | editor (Priya, Dana) | admin (Ada) |
|---|---|---|---|---|---|
| See the fault board, a fault, its history, its impact, the map overlay | `faults:read` | ✓ | ✓ | ✓ | ✓ |
| Log a fault, change its status, patch findings, book a defect, raise from an incident | `faults:write` | — | — | ✓ | ✓ |

(`break-glass-admin` holds both.) Note the personas are *people*, not roles — there is no "operator" role; Priya and Dana are both **editors**.

> ⚠️ **Faults are not territory-scoped.** Unlike the map's features (Phase 58 spatial access control), the faults module applies **no** territory filter — `faults.fault_events` has no `territory_id`, and no route narrows by territory. Every `faults:read` holder sees **every** fault on the system, regardless of which districts they're scoped to on the map. If you rely on territory scoping to partition what a user sees, know that the fault board is organisation-wide.

---

## 3. The fault record

One table, `faults.fault_events` (`migrations/0106_create_faults.sql`; Go mirror `faults/models.go`). The fields, grouped:

**Identity & description**
- `id` (uuid) — the fault's key. A device can **mint its own** for offline capture through the sync `fault_create` op (§5) — not through `POST /faults`, whose body has no `id` field.
- `reference` — an optional human control-room fault number (your own scheme).
- `title` *(required)* — a one-line summary.
- `description`, `notes` — free text.

**What & where**
- `asset_class` + `asset_id` — the **faulted asset** (a loose `(class, id)` reference, §6). All-or-nothing.
- `location` — an optional `Point` pin (the only geometry on the record).
- `fault_type` — a **closed enum**: `SLG`, `LL`, `LLG`, `LLL`, `LLLG`, `earth`, `unknown` (or null). Validated in `store.go`.
- `cause` — **free text**, not a coded catalogue (e.g. `vegetation`, `dig_in`, `lightning`, `third_party`, `animal`, `equipment_failure`).

**Protection & measurement**
- `protective_device_class` + `protective_device_id` — the device that **operated** to clear it (loose ref; all-or-nothing).
- `fault_current_a` — measured/estimated fault current (≥ 0).
- `clearing_time_ms` — how long it took to clear (≥ 0).

**Detection & timing**
- `detected_by` — free text (`scada`, `protection_relay`, `field_crew`, `customer_call`, `model`).
- `detected_at` — when (defaults to now).
- `cleared_at` — when it came off the network (null = still on).

**Links & status**
- `incident_id` — the **OMS incident** this fault caused/explains (§6).
- `defect_id` — the **WAM defect** this fault booked (§6).
- `status` — the lifecycle (§4), defaults to `detected`.
- `created_at` / `updated_at`.

> **∑ The anchor rule.** A fault must be *about* something: a DB CHECK (`fault_events_anchor_ck`) requires at least one of `asset_id`, `location`, or `incident_id` — you can't log a fault floating in nowhere. Two more CHECKs keep the pairs honest (asset class+id together, device class+id together), and a fourth (`fault_events_cleared_ck`) ties `cleared_at` to the terminal statuses. **There is no `severity`/`priority` on a fault** — severity lives on the *defect* it books, not here.

---

## 4. The lifecycle

A fault moves through four statuses (`store.go` constants; DB CHECK on the column):

```
  detected ──► investigating ──► cleared ──► closed
     ▲               │              │           │
     └───────────────┴──────────────┴───reopen──┘  (set back to investigating)
```

- **`detected`** — just logged; still on the network.
- **`investigating`** — being worked (patrol dispatched, cause being established).
- **`cleared`** — **off the network** (isolated or repaired enough to restore). Reaching `cleared` or `closed` stamps `cleared_at` automatically (`SetFaultStatus` COALESCEs it to now); dropping back to a non-terminal status clears it again — so the `cleared_at`/status invariant always holds.
- **`closed`** — the follow-up (the outage and the booked defect) is wrapped up.

> **∑ Engine vs. UI on ordering.** `SetFaultStatus` doesn't *enforce* a forward-only path — any valid value can be set, which is how a **reopen** works (set `cleared`/`closed` back to `investigating`). The **UI** offers the ordered transitions (Investigate → Mark cleared → Close → Reopen, `routes/faults.tsx`), so operators get the sane path while the API stays flexible.

**Every change is audited.** `faults.fault_event_history` (`migration 0109`) is an **append-only** trail — one immutable row per change, each with an `action` (`created`, `status_changed`, `updated`, `defect_booked`), the `actor_id` (who, from the session; null for a system/CLI write), the timestamp, and a full **`snapshot`** of the fault state after the change. The fault record itself has no `created_by`; "who did it" lives only in this history. Read it at `GET /faults/{id}/history` (newest-first), or the expandable **History** panel on each board row.

---

## 5. How to log a fault

> **How to — log a fault from the board** 🔒 `faults:write`
> 1. Sidebar → **Faults** (the ⚡-off icon) → the board lists faults newest first (the **Open only** box is ticked by default, so cleared/closed faults are hidden until you untick it — `routes/faults.tsx`).
> 2. Click **Log fault**.
> 3. Give it a **title**, and anchor it: either pick the **faulted asset** (class + feature — the picker resolves names through the feature registry) **or** enter a **location** (longitude + latitude). (The API accepts asset / location / incident as the anchor, but this dialog only enables **Log fault** for an asset or a location; an incident-anchored fault comes from the Outages page, below.)
> 4. Optionally record `fault_type` (the dialog pre-selects `unknown` and always sends it), `cause`, `detected_by`, `fault_current_a`, `clearing_time_ms` and a description. **Save** → it starts at `detected` (`POST /faults`, returns the new row 201). The dialog has **no** field for the protective device, `reference`, `notes` or `detected_at`; set those through the API (`POST /faults` / `PATCH /faults/{id}`), by raising from an incident (which inherits the device), or via an offline/webhook capture.

> **How to — log a fault against an asset from the map** 🔒 `faults:write`
> Select the faulted feature → in the attribute panel open the **Faults** tab (`feature-faults-tab.tsx`) → it shows that asset's fault history and lets you **log / clear / book** without leaving the map (the asset anchor is filled in for you).

> **∑ Offline-safe logging.** A field device mints the fault's `id` itself and pushes a `fault_create` sync op (the id rides in the op's `feature_id`) when it reconnects; the create is `ON CONFLICT (id) DO NOTHING`, so replaying a queued capture never double-logs (and records no second history row or event). This is the same offline-idempotency pattern the field tool uses elsewhere. ⚠️ The REST `POST /faults` body has **no `id` field** (`faults/http.go` `createFaultRequest`) — an `id` sent there is silently ignored, so it is *not* replay-safe; a second POST makes a second fault.

**Updating findings vs. changing status — two different writes.**
- `PATCH /faults/{id}` is a **sparse patch of investigation findings** (cause, fault_type, device, currents, notes) — it does **not** touch status. ⚠️ The web app has a hook for it (`useUpdateFault`) but **no screen calls it**: today findings are patched through the API, the agent tool `faults_update`, or an inbound webhook, not from the board. It cannot change the asset, the location pin or `detected_at`.
- `POST /faults/{id}/status` is the **only** way to move the lifecycle. Keeping them separate means "I learned the cause" and "I'm marking it cleared" are distinct, separately-audited actions.

---

## 6. The connections — asset, device, incident, defect

A fault sits at the centre of four links. Three are **loose references** (a bare `class`/`id` pair, deliberately *not* a foreign key, so a fault survives the asset being re-modelled); one is a real cross-module call.

```
        protective_device_class/id            incident_id
        (the device that operated)      (the OMS outage it explains)
                    │                            │
                    ▼                            ▼
   asset_class/id ──►   ●  FAULT EVENT  ●   ◄── defect_id
   (the faulted asset)                         (the WAM repair it booked)
```

**① The faulted asset — `asset_class` + `asset_id`.** A loose polymorphic reference to any network feature. The map overlay and impact tools resolve the asset's geometry from its compiled `gis.<class>` table at read time — there's no FK, so a fault can point at a feature that's later edited or even removed.

**② The protective device — `protective_device_class` + `protective_device_id`.** Which breaker / recloser / fuse operated. Used by the **impact** query (§7) to walk the de-energised cone below it. (See **Switching & Protection** for what those devices are.)

**③ The OMS incident — `incident_id`.** The direction is **fault attaches to an existing incident**: the outage is already on the OMS board, and the fault *explains* it.

> **How to — raise a fault from an outage** 🔒 `faults:write`
> On the **Outages** page, open the incident → the **⚡ Raise fault** icon button (icon only, tooltip "Raise a fault event for this outage"; hidden for cancelled incidents). This calls `POST /faults/from-incident/{id}`: the new fault **inherits the incident's predicted device** (as the operating protective device) and its cause (`cause_code`), anchored by `incident_id`. The button sends an empty body, so the fault is titled **"Fault behind incident"** until renamed (`PATCH` `title`); the API also accepts `title`, `fault_type`, `cause`, `detected_by`, `notes` overrides. *(Grounded: `RaiseFaultFromIncident` reads `oms.incidents` cross-schema — the only coupling is at the DB level, no Go import.)* See the **OMS guide** for the incident side.

**④ The WAM defect — `defect_id`.** The direction is **a fault books a defect**: handling a fault creates the repair record that flows into WAM's work-order loop.

> **How to — book the repair from a fault** 🔒 `faults:write`
> On the fault's board row → **Book defect**. This calls `POST /faults/{id}/defect` → `wam.CreateDefect` (source = *outage*, carrying the fault's `incident_id`), then writes `defect_id` back onto the fault. Requires the fault to have an asset or location and to not already have a defect. The defect then becomes a **work order** inside WAM — the fault does **not** create the work order directly. See **Works, Inspections & Maintenance**.

> **∑ One-directional dependency.** `faults` imports `wam` (to book a defect) but **not** `oms` (the incident link is read by raw cross-schema SQL). So the dependency graph is `faults → wam`, and `faults ↔ oms` is DB-only — no import cycle.

---

## 7. Impact & the map

**The map overlay.** `GET /faults/map` returns every fault that has a resolvable point (its own pin, or its asset's geometry) for a map layer (`fault-overlay-store.ts`), filterable the same way as the board. An asset-anchored fault is drawn on its asset (an on-geometry marker plus the asset's shape); the asset geometry is preferred over a pin when both exist. Incident-only faults, and faults whose asset class is uncompiled/deleted/geometry-less with no pin, are omitted. The layer is **off by default** (`visible:false`), shows only `detected` + `investigating` until you tick `cleared`/`closed` in the legend, and live faults pulse unless the OS asks for reduced motion. Clicking a marker draws its impact area.

**The impact area.** `GET /faults/{id}/impact` answers *"what did this fault take out?"* — it walks **downstream of the fault's protective device** and returns the **de-energised service points** below it (`FaultImpactArea`, `geometry.go`). This is the same energisation idea the Switching & Protection guide covers (a cone below an open/operated device), scoped to one fault's protective device. It counts only `SERVICE_POINT` features, uses the barrier-free topological trace (conservative: it does not model open switches), and a fault with no protective device returns an empty list.

**Filters (board & map).** `?status=`, `?open=true` (anything not yet `closed`/`cleared`), `?fault_type=`, `?asset_class=&asset_id=` (one asset's history), `?incident_id=` (all faults on one outage).

---

## 8. Automation & integration

- **Outbound webhooks.** `CreateFault` emits `faults.event.raised`; a status change emits `faults.event.cleared` (on `cleared`/`closed`) or `faults.event.updated` (`webhookout/outbox.go`). A findings `PATCH` and a defect booking emit **no** event. Wire these to a downstream CIS/WMS to mirror fault state. *(See the outbound-webhooks design.)*
- **Inbound webhooks.** An ingestion endpoint with target `fault` (`webhook/adapter_ops.go`, `faultAdapter`) creates or updates faults from a sender's own `external_id`: the first sight creates (same validation and anchor rule as a hand edit), a repeat patches findings and/or sets `status`. `op` must be `upsert` (a fault is never webhook-deleted), and the endpoint's identity needs `faults:write`.
- **Agent / MCP tools** (`api/internal/agenttools/tools_faults.go`) — the whole module is agent-callable, each tool carrying its route's permission: reads `faults_list`, `faults_get`, `faults_list_history`, `faults_list_map`, `faults_impact_area`; writes `faults_create`, `faults_set_status`, `faults_update`, `faults_raise_from_incident`, `faults_book_defect` (writes are annotated "confirm with a human").
- **Offline sync** — the device-minted-`id` idempotency (§5) lets the field tool queue fault captures and replay them safely. The sync ops are `fault_create`, `fault_status` and `fault_book_defect` (`fieldpack/sync.go`); there is **no** offline findings-update op. The Flutter field app has a Faults screen (raise fault, update status, book defect — `mobile/lib/src/ui/faults_screen.dart`).

---

## 9. Worked example — an earth fault on Feeder 7, end to end

A contractor's excavator strikes the 11 kV cable on the tee below **FSW-12** (the fused switch feeding transformer **TX-220**). Here's the whole record lifecycle:

1. **It happens.** The 100 T fuse in FSW-12 clears the fault; **TX-220 and its LV go dark**. SCADA flags loss of supply and an outage is already on the OMS board as an incident.
2. **Priya logs it** (`detected`). Because step 5 needs an asset or a location to book the defect, the fault is logged **anchored to the cable asset *and* the incident** — `POST /faults` with `asset_class`/`asset_id` = the cable tee, `incident_id`, `protective_device` = FSW-12, `title = "Cable fault, Feeder 7 tee to TX-220"`, `fault_type = earth`, `detected_by = scada`. (The Outages-page **Raise fault** button alone would anchor it by `incident_id` only, inheriting the incident's predicted device and cause; such a fault **cannot book a defect** — `POST /faults/{id}/defect` answers 400 *"fault … has no asset or location to book a defect against"* — and a PATCH cannot add an asset or pin afterwards. Raise it from the incident only when there is no asset to name yet.) The Log-fault dialog has no device or incident field, so this combination is an API/agent call.
   - *History row:* `created`, actor = Priya.
3. **Investigation** (`investigating`). A patrol is dispatched; Priya moves it to *Investigating*. The crew finds the dig-in; the findings are `PATCH`ed in (API/agent tool — see §5): `cause = "dig_in"`, `fault_current_a = 3900`, `clearing_time_ms = 55`.
   - Tom opens the fault and compares `fault_current_a = 3900` against the coordination study's prospective 3,913 A — a good match, confirming the model. *(Two different "fault" worlds meeting at one number — §1.)*
   - *History rows:* `status_changed`, `updated`.
4. **Cleared** (`cleared`). The cable is isolated and TX-220 back-fed via the tie; Dana marks it **Mark cleared** → `cleared_at` is stamped automatically. The outage restoration is driven on the OMS side (see the OMS guide).
5. **Book the repair.** Dana clicks **Book defect** → a WAM defect is raised (source *outage*, carrying the `incident_id`), and `defect_id` is written back. The cable splice becomes a work order inside WAM.
   - *History row:* `defect_booked`.
6. **Closed** (`closed`). Once the repair's done and the outage wrapped, Priya **Close**s the fault.

Every step is on `GET /faults/{id}/history` with who and when — a complete, auditable story of one fault from detection to repair.

---

## 10. At scale, and troubleshooting

### Many faults at once — storm operations

There is **no "storm mode"** — faults scale by staying simple and independent:

- **Clustering is OMS's job, not Faults'.** You log each fault event; rolling scattered customer calls into *incidents* happens in OMS (see **OMS §3**). One **incident can carry several faults** (multiple assets failing in one outage) — filter the board by `?incident_id=` to see them all, or by `?asset_class=&asset_id=` for one asset's history.
- **For reliability, record the cause on the *first* fault of an incident.** The `by_fault_cause` breakdown (see **Reliability & Indices**) picks the **earliest-detected** fault per incident as representative, so the primary cause should sit on the fault you raise first.
- **Many crews, offline, at once** replay safely — each `fault_create` sync op is idempotent on its device-minted `id` (§5). The REST `POST /faults` is **not** idempotent, so a retried POST makes a second fault; prefer the sync op for field capture under load.

### Troubleshooting

| Symptom | Why | Fix |
|---|---|---|
| **Book defect → `400` "no asset or location"** | the fault was raised from an incident only (§6 ③) — no asset or pin | log a fresh fault anchored to the **asset** (a `PATCH` can't add an asset or pin afterwards), then book from that |
| **Book defect → `500`** | a bad `severity` reaches WAM, whose error type `writeError` doesn't map to 400 | send a valid `severity` on `POST /faults/{id}/defect` |
| **Book defect refused "already has a defect"** | `defect_id` is already set | the repair is already booked — open it in WAM |
| **Fault not on the map** | the overlay is **off by default**; or the fault is incident-only; or its asset is uncompiled / geometry-less with no pin | tick the layer in the legend; give the fault a **location pin** or anchor it to an asset that has geometry |
| **Can't edit findings from the board** | no web screen calls `PATCH /faults/{id}` (§5) | use the API, the `faults_update` agent tool, or an inbound webhook |
| **Duplicate fault after a retry** | `POST /faults` has no `id` and isn't idempotent (§5) | use the offline `fault_create` sync op (device-minted `id`) for replay-safe capture |
| **`"fault … does not exist"` is a `400`, not `404`** | every client-fixable error is a 400 with `{"error":…}` | check the id |
| **A cleared fault vanished from the board** | the board defaults to **Open only** | untick *Open only* to see `cleared` / `closed` |

---

## Appendix — reference

### Status & type enums

| Field | Values |
|---|---|
| `status` | `detected`, `investigating`, `cleared`, `closed` |
| `fault_type` | `SLG`, `LL`, `LLG`, `LLL`, `LLLG`, `earth`, `unknown` (or null) |
| `cause` | **free text** (not an enum) — e.g. `dig_in`, `vegetation`, `lightning`, `animal`, `third_party`, `equipment_failure` |
| `detected_by` | **free text** — e.g. `scada`, `protection_relay`, `field_crew`, `customer_call`, `model` |
| history `action` | `created`, `status_changed`, `updated`, `defect_booked` |

### Endpoints (`/faults`, method-aware `faults:read` / `faults:write`)

| Method · Path | Perm | Does |
|---|---|---|
| `GET /faults` | read | Board list, newest-first; filters `status` / `open` / `fault_type` / `asset_class`+`asset_id` / `incident_id` |
| `GET /faults/map` | read | Faults with a point, for the map overlay |
| `GET /faults/{id}` | read | One fault |
| `GET /faults/{id}/history` | read | Audit timeline, newest-first |
| `GET /faults/{id}/impact` | read | De-energised service points below the protective device |
| `POST /faults` | write | Log a fault (title + anchor required); starts `detected` |
| `PATCH /faults/{id}` | write | Sparse patch of findings (not status) |
| `POST /faults/{id}/status` | write | Transition the lifecycle |
| `POST /faults/{id}/defect` | write | Book a WAM defect, link `defect_id` |
| `POST /faults/from-incident/{id}` | write | Raise a fault from an OMS incident, link `incident_id` |

### Honest notes (what the module does *not* do)

- **Not territory-scoped** (§2) — organisation-wide visibility for any `faults:read` holder.
- **No fault-level severity/priority** — severity lives on the booked WAM defect.
- `cause` and `detected_by` are **free text**, not coded catalogues.
- An unknown fault id is a **400** (`fault "…" does not exist`), not a 404; every client-fixable validation error is a 400 with `{"error": …}`.
- Asset / device / incident / defect links are **loose refs, not FKs** — a fault outlives its asset being re-modelled.
- A fault books a **defect**, not a work order directly (the work order is created inside WAM).
- A fault is **not a GIS feature** — it's a record in `faults.fault_events`, never a compiled `gis.*` class.
- The record module is **not** the fault-current engine (§1).

### Related guides

- **Outage Operations** — the whole call → fault → incident → switch → restore → repair → reliability loop in one place; where this guide's fault sits in the bigger picture.
- **OMS — Outage Management** — the incident side: predicting the outage, the device-state console, switching orders and restoration. A fault *explains* an incident.
- **Works, Inspections & Maintenance** — the defect/work-order side. A fault *books* a defect.
- **Switching & Protection** — the protective devices that clear a fault and the de-energised cone one leaves.
- **Reliability & Indices** — how a fault's `cause` feeds the `by_fault_cause` breakdown of SAIDI/SAIFI.
- **Engineering Studies** — the *fault-current* calculation (`/analysis/fault-current`), the model a recorded `fault_current_a` can be checked against.

---

## Appendix — Corrections (verified against the code)

This edition corrected the following against the shipped code: outbound webhook event names are `faults.event.raised|cleared|updated` (not `fault.*`) and only create and status change emit them; the Log-fault dialog has no protective-device, reference, notes or incident field, and the board defaults to *Open only*; no web screen edits findings (the `PATCH` is API/agent/webhook only); the Outages button sends an empty body (default title "Fault behind incident"); and the inbound-webhook (`fault` target), offline-sync ops, mobile Faults screen, map-overlay defaults and impact-area limits were added. The §9 worked example was also corrected: a fault raised only from an incident has no asset or location, so `BookDefectFromFault` refuses it (`faults/store.go` `has no asset or location to book a defect against`) — the example now logs the fault against the cable asset *and* the incident. Also corrected: the idempotent device-minted `id` applies to the sync `fault_create` op, not to `POST /faults` (`faults/http.go:92-110` has no `id`; `fieldpack/sync.go` `applyFaultsOp` passes `ID: op.FeatureID`). Note (code reading, not run): a bad `severity` on `POST /faults/{id}/defect` surfaces as HTTP 500, because `wam`'s validation error type is not the `faults` one that `writeError` maps to 400.
