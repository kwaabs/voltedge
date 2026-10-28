# Device Operations — Logging & Reporting Device Trips

_Last updated: 2026-10-28_

The **Device Operations** log is the record of *what the network's protective and
switching devices actually did* — every trip, lockout, reclose, manual open and
close — with a timestamp, a cause, and where the report came from.

It answers two questions the rest of VoltEdge could not:

- **"What has this device done?"** — a breaker's or recloser's operation history,
  and how many times it has operated (a maintenance and reliability signal).
- **"What's out there, and where does it stand?"** — every operable device with
  its current open/closed state and its last operation.

> **Operations vs. state vs. outages.** A device's `device_state` (OPEN/CLOSED)
> is only its *current* position. The operations log is the *history* of how it
> got there. And an outage (OMS) is the *customer* consequence — a device
> operation is the *electrical event* that often causes one. They're linked (see
> [Relationship to Outage Management](#relationship-to-outage-management)), but
> each is its own record.

---

## Where to find it

- **Operations menu** (left sidebar) — opens the device-operations surface. It
  has two tabs: **Operations** (this log — the network-wide board and reports)
  and **Live state** (device telemetry reconciled against the model — see
  [Live state](#live-state--field-vs-model)).
- **Click any device** in a board row to open its **detail panel** on the right:
  a summary of the feature (its `device_state`, lifecycle and attributes), its
  **operation history**, any **attachments**, and a **View on map** button. The
  table shrinks to make room, and the panel stays open as you switch between the
  Operations and Live state tabs.
- **A device's Operations tab** — open any breaker, recloser, fuse or switch on
  the map; its attribute panel has an **Operations** tab showing that device's
  own timeline, with a **Log operation** button.

Viewing the log requires **`operations:read`** and logging requires
**`operations:write`**; the Live state tab requires **`telemetry:read`** and its
Apply action **`oms:write`** (see [Permissions](#permissions-at-a-glance)).

---

## What a device operation records

| Field | Meaning |
|---|---|
| **Device** | The feature that operated (its class + id — e.g. an `MV_RECLOSER`). |
| **Operation** | `trip`, `lockout`, `reclose`, `open`, `close`, `earth`, `alarm`, or `other`. |
| **Source** | Where the record came from: `crew`, `scada`, `relay`, `switching_order`, `derived`, `telemetry`, or `other`. |
| **When** | The moment the operation *happened* (not when it was logged). |
| **Cause** | Free text — "overcurrent", "earth fault", "manual", "test"… |
| **Notes** | Anything else worth keeping. |
| **Correlations** | Optional links to the related **fault**, **OMS incident**, or **switching step**. |

The log is **append-only**: an operation is an event that occurred, so it is
never edited or deleted — the record of a trip stands.

---

## The three views

Open the **Operations** page and use the view toggle (top-right of the card):

### 1. Timeline
Recent operations across the whole network, newest first — *When · Device ·
Operation · Source · Cause*. Filter by **device class**, **operation type**, and
**source**. This is the live "what just happened" board.

### 2. By device
The **trip-count rollup**: for each device and operation type, how many times it
has done it and when it last did — busiest first. This is the reliability /
maintenance view: *which recloser operates most?* Filter by operation type.

### 3. Devices
The **device inventory**: every operable device, its **current state** (OPEN /
CLOSED), and its **last operation** (or "never"). This includes devices that have
never operated. Filter by device class and by state (e.g. show every device
that's currently OPEN). The device list is discovered automatically — any class
that models a `device_state` is treated as an operable device, so a device class
you add later appears here for free.

---

## Logging an operation

Click **Log operation** (on the board, or on a device's Operations tab). On a
device's tab the device is pre-filled; from the board you enter it.

- **Operation** and the **device** are required.
- **When** defaults to *now* — leave it blank for "it just operated", or set it
  for a historical entry.
- **Source** defaults to **crew** (a field log).

That's it — the operation is logged and appears immediately in all three views.

---

## How the log fills itself

You will rarely log everything by hand. The operations log captures activity from
across VoltEdge automatically:

- **Planned switching** — when you mark an **open/close switching step executed**
  (OMS switching order), the operation is logged automatically (source
  `switching_order`), linked back to the step. Only a fresh execution logs — a
  re-tick or revert does not.
- **Faults** — when a **fault** names the protective device that cleared it, a
  **trip** is logged (source `relay`/`scada`/`derived`, inferred from how the
  fault was detected), linked to the fault. This happens whether the device is
  identified when the fault is first logged or filled in later — and only once
  per fault.
- **Telemetry (Live state)** — when an operator **applies** a device's telemetry
  reading to correct the model (Live state → **Apply field**), the resulting
  open/close is logged automatically (source `telemetry`).
- **Crew** — the manual **Log operation** action above.
- **SCADA / external systems** — see below.
- **Agents** — the in-app assistant and any MCP client (see
  [For agents](#for-agents-mcp-tools)).

---

## Live state — field vs. model

The **Live state** tab answers a different question from the log: *does the model
match reality right now?* It reconciles the **latest device telemetry** (what the
field — SCADA / AMI — reports) against each device's **model `device_state`**, and
flags where they disagree or where a device has gone quiet.

Four tiles summarise the picture:

- **Telemetered** — devices currently reporting telemetry.
- **Agreeing** — telemetry matches the model and is fresh.
- **Disagreeing** — the field and the model say different things (e.g. the model
  says CLOSED, telemetry says OPEN). This is the one that bites a control room: a
  picture out of sync with the copper.
- **Stale** — no reading for longer than the staleness window (default 5 minutes):
  a quiet or dead device (lost comms, a dead RTU).

The **Needs attention** table lists the disagreeing and stale devices. Search it
by device, state or status, and click a device to open its [detail panel](#where-to-find-it).
The page refreshes itself — it's a live picture, not a snapshot.

### Apply field

When a device **disagrees**, an operator with **`oms:write`** can press **Apply
field** to write the telemetry state onto the model, so the model catches up to
what the field reports. It is deliberate — nothing auto-applies — and is **logged
as a `telemetry`-sourced operation**, so the correction shows up in the operations
log. A *stale* row has no Apply: a quiet device isn't disagreeing, it's just
silent.

> Applying records that the device **has** moved in the field; it does **not**
> command it to move, so it is not subject to the switching safety interlock.

Viewing Live state needs **`telemetry:read`**; telemetry itself is pushed in by a
SCADA / AMI gateway as batches to the telemetry ingest endpoint
(**`telemetry:ingest`**) — a separate feed from the device-operation ingestion
described next.

---

## Feeding it from SCADA / external systems

An external system (SCADA, a relay master, a DMS) can push operations straight
into the log through VoltEdge's **webhook ingestion** engine — the same hardened
pipeline (signature + replay protection, queueing, retries, an admin field
mapping) that feeds features, readings, work orders and faults.

**Set it up (admin):** create a webhook endpoint with **Target = "Device
operation"**. Give it a `system_code` in its config so VoltEdge knows which
external system the ids belong to.

**Device correlation.** The external system names a device by *its own* id. Two
options:

- Send VoltEdge's own `device_class` + `device_id` directly, or
- Send a **`device_external_id`** — VoltEdge resolves it to the right feature
  through the device's **external reference** (the `external_ref` you set when you
  tagged the feature with its SCADA id). The external side never needs VoltEdge's
  internal ids. (See the Integrations guide for `external_ref`.)

**Idempotency.** Each operation carries the sender's own event id
(`external_id`). A re-delivered batch — SCADA loves to re-send — records nothing
the second time, so a trip is never double-logged.

A minimal change in the payload:

```json
{
  "external_id": "SCADA-EVT-90432",
  "op_type": "trip",
  "device_external_id": "RTU-42-OC",
  "occurred_at": "2026-10-18T14:03:11Z",
  "cause": "overcurrent"
}
```

---

## Relationship to Outage Management

Device operations sit in the same operational family as **Outage Management
(OMS)** and **Faults**, and the three cross-link:

```
   a device operates (TRIP)        ──►  Device Operations log   (the electrical event)
        │                                        │ fault_id
        ▼                                        ▼
   it clears a FAULT               ──►  Faults                  (the investigation)
        │                                        │ incident_id
        ▼                                        ▼
   customers lose supply           ──►  OMS incident / outage   (the customer impact)
        │
        ▼
   a SWITCHING ORDER restores them ──►  executing an open/close step
                                         logs an operation (source switching_order)
```

So an operation can carry a `fault_id` and an `incident_id`, and an executed
switching step logs its own operation. The operations log is the **electrical
ground truth** beneath the outage story: the outage board tells you *who is off
supply*; the operations log tells you *what the devices did* to get there and to
restore it.

---

## For agents (MCP tools)

Every view and action is available to the in-app assistant and any MCP client,
gated by the same permissions:

| Tool | Does |
|---|---|
| `operations_list` | the Timeline board (filterable) |
| `operations_device_timeline` | one device's history |
| `operations_counts` | the per-device trip-count rollup |
| `operations_inventory` | the device inventory (state + last op) |
| `operations_record` | log an operation (write) |

---

## Permissions at a glance

| Capability | Who holds it by default |
|---|---|
| `operations:read` | viewer, analyst, editor, field-technician, admin, break-glass |
| `operations:write` | editor, field-technician, admin, break-glass |
| `telemetry:read` (view Live state) | operational roles, per your configuration |
| `oms:write` (Apply field) | operators who run the network, per your configuration |
