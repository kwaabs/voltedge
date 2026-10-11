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
- `id` (uuid) — the fault's key. A device can **mint its own** for offline capture (§5).
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
> 1. Sidebar → **Faults** (the ⚡-off icon) → the board lists every fault, newest first.
> 2. Click **Log fault**.
> 3. Give it a **title**, and anchor it: either pick the **faulted asset** (class + feature — the picker resolves names through the feature registry) **or** drop a **location** pin. (At least one of asset / location / incident is required.)
> 4. Optionally record `fault_type`, `cause`, `detected_by`, the protective device that operated, `fault_current_a`, `clearing_time_ms`. **Save** → it starts at `detected` (`POST /faults`, returns the new row).

> **How to — log a fault against an asset from the map** 🔒 `faults:write`
> Select the faulted feature → in the attribute panel open the **Faults** tab (`feature-faults-tab.tsx`) → it shows that asset's fault history and lets you **log / clear / book** without leaving the map (the asset anchor is filled in for you).

> **∑ Offline-safe logging.** A field device can mint the fault's `id` itself and `POST /faults` when it reconnects; the create is `ON CONFLICT (id) DO NOTHING`, so replaying a queued capture never double-logs. This is the same offline-idempotency pattern the field tool uses elsewhere.

**Updating findings vs. changing status — two different writes.**
- `PATCH /faults/{id}` is a **sparse patch of investigation findings** (cause, fault_type, device, currents, notes) — it does **not** touch status.
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
> On the **Outages** page, open the incident → **Raise fault**. This calls `POST /faults/from-incident/{id}`: the new fault **inherits the incident's predicted device** (as the operating protective device) and its cause, anchored by `incident_id`. *(Grounded: `RaiseFaultFromIncident` reads `oms.incidents` cross-schema — the only coupling is at the DB level, no Go import.)* See the **OMS guide** for the incident side.

**④ The WAM defect — `defect_id`.** The direction is **a fault books a defect**: handling a fault creates the repair record that flows into WAM's work-order loop.

> **How to — book the repair from a fault** 🔒 `faults:write`
> On the fault's board row → **Book defect**. This calls `POST /faults/{id}/defect` → `wam.CreateDefect` (source = *outage*, carrying the fault's `incident_id`), then writes `defect_id` back onto the fault. Requires the fault to have an asset or location and to not already have a defect. The defect then becomes a **work order** inside WAM — the fault does **not** create the work order directly. See **Works, Inspections & Maintenance**.

> **∑ One-directional dependency.** `faults` imports `wam` (to book a defect) but **not** `oms` (the incident link is read by raw cross-schema SQL). So the dependency graph is `faults → wam`, and `faults ↔ oms` is DB-only — no import cycle.

---

## 7. Impact & the map

**The map overlay.** `GET /faults/map` returns every fault that has a resolvable point (its own pin, or its asset's geometry) for a map layer (`fault-overlay-store.ts`), filterable the same way as the board. Markers + impact cones draw over the network; the legend filters client-side.

**The impact area.** `GET /faults/{id}/impact` answers *"what did this fault take out?"* — it walks **downstream of the fault's protective device** and returns the **de-energised service points** below it (`FaultImpactArea`, `geometry.go`). This is the same energisation idea the Switching & Protection guide covers (a cone below an open/operated device), scoped to one fault's protective device.

**Filters (board & map).** `?status=`, `?open=true` (anything not yet `closed`/`cleared`), `?fault_type=`, `?asset_class=&asset_id=` (one asset's history), `?incident_id=` (all faults on one outage).

---

## 8. Automation & integration

- **Outbound webhooks.** `CreateFault` emits `fault.raised`; a status change emits `fault.cleared` (on a terminal status) or `fault.updated`. Wire these to a downstream CIS/WMS to mirror fault state. *(See the outbound-webhooks design.)*
- **Agent / MCP tools** (`api/internal/agenttools/tools_faults.go`) — the whole module is agent-callable, each tool carrying its route's permission: reads `faults_list`, `faults_get`, `faults_list_history`, `faults_list_map`, `faults_impact_area`; writes `faults_create`, `faults_set_status`, `faults_update`, `faults_raise_from_incident`, `faults_book_defect` (writes are annotated "confirm with a human").
- **Offline sync** — the device-minted-`id` idempotency (§5) lets the field tool queue fault captures and replay them safely.

---

## 9. Worked example — an earth fault on Feeder 7, end to end

A contractor's excavator strikes the 11 kV cable on the tee below **FSW-12** (the fused switch feeding transformer **TX-220**). Here's the whole record lifecycle:

1. **It happens.** The 100 T fuse in FSW-12 clears the fault; **TX-220 and its LV go dark**. SCADA flags loss of supply and an outage is already on the OMS board as an incident.
2. **Priya logs it** (`detected`). On the Outages page she opens the incident → **Raise fault**. The fault is born anchored to `incident_id`, inheriting the predicted device. She fills in what she knows: `title = "Cable fault, Feeder 7 tee to TX-220"`, `fault_type = earth`, `detected_by = scada`, `protective_device = FSW-12`.
   - *History row:* `created`, actor = Priya.
3. **Investigation** (`investigating`). A patrol is dispatched; Priya moves it to *Investigating*. The crew finds the dig-in; she `PATCH`es the findings: `cause = "dig_in"`, `fault_current_a = 3900`, `clearing_time_ms = 55`.
   - Tom opens the fault and compares `fault_current_a = 3900` against the coordination study's prospective 3,913 A — a good match, confirming the model. *(Two different "fault" worlds meeting at one number — §1.)*
   - *History rows:* `status_changed`, `updated`.
4. **Cleared** (`cleared`). The cable is isolated and TX-220 back-fed via the tie; Dana marks it **Mark cleared** → `cleared_at` is stamped automatically. The outage restoration is driven on the OMS side (see the OMS guide).
5. **Book the repair.** Dana clicks **Book defect** → a WAM defect is raised (source *outage*, carrying the `incident_id`), and `defect_id` is written back. The cable splice becomes a work order inside WAM.
   - *History row:* `defect_booked`.
6. **Closed** (`closed`). Once the repair's done and the outage wrapped, Priya **Close**s the fault.

Every step is on `GET /faults/{id}/history` with who and when — a complete, auditable story of one fault from detection to repair.

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
- Asset / device / incident / defect links are **loose refs, not FKs** — a fault outlives its asset being re-modelled.
- A fault books a **defect**, not a work order directly (the work order is created inside WAM).
- A fault is **not a GIS feature** — it's a record in `faults.fault_events`, never a compiled `gis.*` class.
- The record module is **not** the fault-current engine (§1).

### Related guides

- **OMS — Outage Management** — the incident side: predicting the outage, the device-state console, switching orders and restoration. A fault *explains* an incident.
- **Works, Inspections & Maintenance** — the defect/work-order side. A fault *books* a defect.
- **Switching & Protection** — the protective devices that clear a fault and the de-energised cone one leaves.
- **Engineering Studies** — the *fault-current* calculation (`/analysis/fault-current`), the model a recorded `fault_current_a` can be checked against.


