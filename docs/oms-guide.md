# OMS — Outage Management

_Last updated: 2026-10-28_

**Written for:** control-room operators who take trouble calls and drive outages to restoration, planners scheduling planned work, and engineers reading reliability performance.

**OMS (Outage Management System)** turns scattered **customer trouble calls** into **incidents** (an outage blamed on a device), predicts who's affected, drives the **switching order** that isolates and restores supply, and rolls the result up into **reliability indices**. It sits between two neighbours it references rather than repeats: a **fault** (see the **Faults** guide) *explains* an incident; and the **switchgear** it operates (see **Switching & Protection**) — including the fact that executing a switching step now flips a device's **live `device_state`**.

```
  trouble calls ─► PREDICT ─► INCIDENT ─► SWITCHING ORDER ─► RESTORE ─► reliability
   (no supply)     (blame a    (affected    (isolate · earth ·        (SAIDI/SAIFI)
                    device)     customers)    restore; flips state)
        ▲                                            │
     a FAULT explains the incident            a FAULT books a WAM defect
```

Two layers:
- **▶ Operator** — the console flow: take a call, predict, review who's out, generate and tick a switching order, restore.
- **∑ Engineer** — the record model, the prediction/energisation engines, the endpoints and the (sometimes surprising) coupling to the live network.

**The cast.** **Priya** (control-room operator) takes calls, runs prediction, confirms incidents and issues switching orders; **Dana** (field crew) executes the steps on the ground; **Tom** (analyst) reads the reliability indices; **Ada** (admin) holds the permissions. The running example is a **persistent fault on Feeder 7** that the mid-feeder recloser **REC-7** locks out on — the same feeder and devices as the Switching & Protection and Faults guides.

**Legend.** 🔒 a permission (named inline). 💡 a tip. ⚠️ a caution or a limitation stated honestly. **▶** the operate lane; **∑** the model lane. A boxed **How to** is a click-by-click procedure; a **Worked example** is reproducible.

> **Grounding note.** Drawn from the shipped code: schema in `migrations/0087_create_oms.sql` (+ `0088` permissions, `0089` planned outages & switching orders, `0119` interruption feature-class, `0167` event log, `0168` detection source & coded cause); engine in `api/internal/oms` (`predict.go`, `switching.go`, `energization.go`, `store.go`, `indices.go`, `causes.go`, `analytics.go`); UI in `web/src/routes/outages.tsx`, `web/src/components/map/outage-map.tsx`, `web/src/hooks/use-oms.ts`. OMS records are **not** compiled `gis.*` features. ⚠️ The `0087` migration comment calls OMS a decoupled overlay — that prose is **stale**: since 2026-09-24 switching steps write live state (§6). ⚠️ The FE `isScadaRaised` heuristic (device-backed + null confidence) is **retired** — detection source is now the stored `detection_source` (§4, migration 0168).

---

## 1. What OMS is — five records, two hinges

OMS is its own schema (`oms`), **not** map features. Five tables carry it:

| Table | What it holds |
|---|---|
| `oms.trouble_calls` | one customer report (`no_supply` / `partial` / `flicker`) |
| `oms.incidents` | an outage: the blamed device, cause, status, affected counts, ETR |
| `oms.customer_interruptions` | one per affected supply — the frozen reliability fact (SAIDI/SAIFI) |
| `oms.switching_orders` | the ordered program to isolate and restore an incident |
| `oms.switching_steps` | the individual open / close / earth / verify / note steps |

And **two hinges** to the rest of the system (both covered by their own guides):

- A **fault** → an incident: a fault record carries `incident_id` and *explains* the outage. OMS doesn't import the faults package; the operator **raises a fault from an incident** (see **Faults**). The only place OMS reads faults is the reliability report's "by cause" breakdown (§8), and even that is an optional, existence-checked read.
- A **defect / work order** → only *through* the fault: an incident has **no** WAM column. The repair is booked off the fault, not the incident (see **Works, Inspections & Maintenance**).

---

## 2. Who may do what

Two permissions (`migrations/0088_seed_oms_permissions.sql`; constants `auth.PermOMSRead`/`PermOMSWrite`), enforced method-aware on the `/oms` mount (`main.go:266` — `GET` = read, everything else = write):

| Action | Permission | viewer | analyst (Tom) | editor (Priya, Dana) | admin (Ada) |
|---|---|---|---|---|---|
| See the board, calls, affected area, energisation, switching orders, reliability, crews | `oms:read` | ✓ | ✓ | ✓ | ✓ |
| Log/associate calls, predict, confirm, dispatch, plan, restore, cancel, generate order, **execute step**, re-attribute, manage crews & dispatch, set ETR, task a subcontractor | `oms:write` | — | — | ✓ | ✓ |

`break-glass-admin` holds both permissions too, exactly like `admin` (`0088:25-33`). A viewer gets a **read-only board** (the write controls self-gate). A request without the needed permission is a `403`; without a session, `401` (`auth/rbac.go:66-85`). (The **subcontractor registry** itself — Admin → Subcontractors — is `wam:write`, since a subcontractor is a WAM entity; *tasking* one to an incident is `oms:write`, §12.) Two honest cautions:

> ⚠️ **OMS is not territory-scoped.** No `oms` table has a `territory_id`, and no incident/board query filters by territory (Phase 58 scoping lives *inside* the `trace` primitives OMS calls, e.g. back-feed paths, not in the OMS queries). The outage board shows **every** territory's incidents to any `oms:read` holder.

> **Incidents *do* record "who" now.** `last_actor_user_id` (migration 0165) stamps who last advanced the incident (confirm / dispatch / restore / cancel / re-attribute), resolved to a display name on read (§4) — and a real **crew** can be dispatched (§12), replacing the old free-text `crew_ref`. The switching-step `device_state` flips are still additionally logged to `gis.feature_history` (§6).

---

## 3. From trouble calls to incidents — prediction

**A trouble call** (`oms.trouble_calls`) is one customer report: a `call_type` (`no_supply` / `partial` / `flicker`), a locator (a service-point id, an address, or a map pin — at least one required), and a `status` (`open` → `associated` → `cleared` / `cancelled`; ⚠️ nothing in the code ever sets `cleared`, and only *Predict* sets `associated` — see below).

> **How to — take and place a call** 🔒 `oms:write`
> 1. **Outages** page → **Log call** → type + locator → save (`POST /oms/trouble-calls`, status `open`).
> 2. **Associate** it to a service point: give the SP id, or pass `snap = true` to resolve the nearest SP from the call's pin (`POST /oms/trouble-calls/{id}/associate`). An associated call now has a feature to walk upstream from. ⚠️ Associating only fills in `service_point_id`; the call's `status` **stays `open`** until *Predict* links it to an incident (`store.go:144-195`, `store.go:366`).

**Prediction** rolls the open, SP-resolved calls into incidents (`predict.go`):

- For each call's service point it walks **upstream** to the nearest **protective device** — `ProtectiveDeviceClasses = {MV_FUSE, MV_RECLOSER, MV_BREAKER}` (a plain `MV_SWITCH`/sectionaliser is *not* protective, so prediction walks past it to the real fault-clearer).
- Calls under the **same** device cluster into **one** incident; calls under different devices become separate incidents; calls with **no** upstream device form a "no-device" cluster. ⚠️ Nothing can attribute a device to an existing incident afterwards — no endpoint, no console action (`oms/http.go` route table; no `UPDATE … predicted_device_*` in `oms/store.go`). The generator's error text says "attribute it first", but the only ways forward are to fix the connectivity so the walk finds a device, then cancel the no-device incident (its calls return to `open`) and Predict again, or to plan an outage around the right device.
- It's a **single-fault hypothesis**: it blames the *nearest* protective device, one fault per cluster.
- **The upstream walk is junction-aware (but barrier-free).** Since the junction-awareness fix it delegates to the shared `bfs` (`trace/class_walk.go` `reachableWithDepth`), so it **crosses a busbar / RMU bus** and applies the cross-layer tier guard — but stays **barrier-free** (it ignores live switch state; prediction is about topology, not current positions). A service point fed through an RMU bus now reaches its upstream protective device instead of landing in the no-device cluster. A **no-device** cluster (null `confidence`) now means there is genuinely no protective device upstream. An `is_fused` `MV_SWITCH` (e.g. FSW-12) is never blamed — only the class codes `MV_FUSE`/`MV_RECLOSER`/`MV_BREAKER` count (`predict.go:38`).
- **Confidence** = distinct calling SPs ÷ affected SPs (capped at 1.0) — one call on a big feeder is low-confidence; half the street calling is high.

> **How to — predict incidents** 🔒 `oms:write`
> **Outages** → **Predict** (`POST /oms/incidents/predict`). Open calls roll into `predicted` incidents, each blamed on a device, with affected counts and a confidence. Calls are marked `associated` and linked.

---

## 4. The incident & who's affected

An **incident** (`oms.incidents`) is the outage record: the `predicted_device_class`/`_id` it's blamed on, a `circuit_id` reporting axis, an `outage_type` (`unplanned` / `planned`), a coded `cause_code` + `cause_status`, a `detection_source` + `detected_at`, `confidence`, timings (`started_at`, `etr`, `restored_at`), denormalised `affected_customers` / `affected_service_points`, `crew_ref` / `notes`, and `last_actor_user_id` — **who last advanced it** (confirm / dispatch / restore / cancel), resolved to a display name on read (migration 0165). A board row shows "restored by Ama" without the viewer needing `admin:users`.

> **∑ Two orthogonal axes — how we learned, and why (migration 0168).** An outage *is* the equipment event (a trip or a failure); calls, clusters and territory are how it is **detected** and **described**. So every incident carries two separate axes, both first-class:
> - **`detection_source`** — *how the utility learned*: `customer` / `ami` / `scada` / `crew` / `planned` / `upstream` / `analytics`, plus `detected_at` (when the first signal arrived — a SCADA trip's reported time, the earliest call) and an optional `detection_detail` (channel / external id). It is **stamped by the raising path** — predict ⇒ `customer` (or `ami` when the whole cluster is AMI last-gasp), SCADA webhook ⇒ `scada`, planned ⇒ `planned` — *not* inferred (this retires the old `isScadaRaised` heuristic: device-backed + null-confidence).
> - **`cause_code` + `cause_status`** — *why it happened*: a **coded** value from the `oms.outage_causes` catalog (coarse families — equipment failure, third-party dig-in, lightning, vegetation, …) that starts **`unknown`** and is refined **`suspected` → `confirmed`**. The detailed per-asset FMEA stays on the **fault** (`faults.fault_modes`); **raising a fault from an incident confirms the incident's cause** (`cause_status → confirmed` when a cause is named). Set or correct both via `PATCH /oms/incidents/{id}/attribution` (`oms:write`).
>
> A device alarm tells you a device *operated* (source), not *why* (cause) — keeping the two apart is what lets the console answer the best maturity question a utility has: **"how many outages did we know about before a customer called us?"** (the `machine_detected_share` in §8's detection metrics) while still slicing impact by root cause.

> **How to — open an incident's detail page** 🔒 `oms:read`
> The **Outages** board is a **list**; click any incident row (or a trouble call's **View incident** link) to open its own **detail page** at `/outages/{id}` (`web/src/routes/outages.tsx` `OutageDetailPage`) — a shareable, bookmarkable URL that gathers everything for that one outage:
> - an **Overview** card — type, blamed device (or *Unattributed*), cause, confidence, circuit, affected counts, started / planned window / ETR / restored, crew, last actor, notes, incident id;
> - the **outage-area map + affected-area rollup** — ⚠️ shown **only when there's a cone to draw**; an unattributed / zero-SP incident has nothing to map, so neither appears (no empty map tile);
> - the **switching program** + **Restoration (FLISR)** panel (§6);
> - the **timeline** (§model events) and the **trouble calls that fed this incident**.
>
> The lifecycle actions — **Confirm**/**Begin**, **Dispatch**, **Restore**, **Cancel incident**, plus **Raise fault** and **Show on map** — sit in the page header, each self-gated on `oms:write` (a viewer sees the detail read-only). **← Outage Management** returns to the board. (Before this, the detail lived in a side drawer beside the board; it's now a dedicated route.)

**Status machine** (`models.go`):

```
  unplanned:  predicted ──► confirmed ──► dispatched ──► restored   (or ──► cancelled)
  planned:    scheduled ──► confirmed ──► dispatched ──► restored   (confirmed = executed)
```

(`dispatched` (crew en route) is optional — **Restore** is accepted from `confirmed` or `dispatched`. The API also lets you **restore** a `predicted` or `scheduled` incident directly — only `restored`/`cancelled` are refused (`store.go`); with no frozen interruptions it simply contributes nothing to the indices.)

- **`predicted`** — just rolled up from calls; affected counts are **live** estimates.
- **`confirmed`** — the operator confirms the outage; this **freezes** the affected set into `customer_interruptions` rows (the reliability facts that won't move even as the network changes). For an unplanned incident each row's clock starts at the incident's `started_at` (the moment prediction created it), **not** at the confirm click; a planned outage's clock starts at confirm. An incident with no blamed device freezes nothing.
- **`dispatched`** — a crew has been sent (`POST /oms/incidents/{id}/dispatch`, from `confirmed` only). A board state, not a model change: it stamps `last_actor_user_id` and fires `oms.incident.updated`.
- **`restored`** — supply's back; `restored_at` stamped, interruption windows closed.
- **`cancelled`** — a false alarm; linked calls are released back to `open`.

**Who's affected (∑).** The affected rollup walks **downstream of the blamed device** and sums supplies — `trace.DownstreamLoadLeaves` → service points + customers, with a `Service` rollup splitting load (kW) and customers by **restoration tier** / `load_priority`, flagging **critical** facilities (rank ≤ P2). Two supply classes count: unmetered `SERVICE_POINT`s and `PRIMARY` `METER`s (a null customer count reads as 1). `GET /oms/incidents/{id}/affected` returns the **frozen** rows once confirmed, falling back to a **live** trace while still predicted.

> **∑ Affected is barrier-free — but junction-aware.** The affected/frozen rollup is **barrier-free and topological** — it counts the supplies *reached by a downstream walk* from the device regardless of live switch state (`trace.DownstreamLoadLeaves` → `reachableWithDepth`, `trace/class_walk.go`). Since the OMS junction-awareness fix, that walk **delegates to the shared junction-aware `bfs`**, so it crosses a busbar / RMU one-way bus and picks up the supplies behind it (the ring tail included) — the **same topology the live cone (§5) walks**. What still differs is **live switch state**: affected ignores it (barrier-free, so SAIDI/SAIFI don't shrink just because a tie happens to be closed), while the cone respects it. So the two now agree on a plain radial outage and diverge only when the live network differs from as-built (another open device below, or a back-feed).

---

## 5. The live de-energised cone

`GET /oms/incidents/{id}/energization` answers *"what is dark right now?"* (`energization.go`). It reads the blamed device's **live `device_state`**:

- If the device is **not** `OPEN` → the set is **empty** (supply is on).
- If it's `OPEN` → `trace.DeEnergizedCone(device)` — a **barrier-respecting**, directed-downstream walk — returns the dead service-point geometry plus a `Service` rollup.

The cone is **live**: open the device and it grows; close it and it collapses to empty; open a further device below it (a sectionalising or back-feed step) and it **shrinks** to just the still-isolated part (the customers beyond that further open device are not in *this* device's cone — they belong to the further-open device's own cone, even though they are still without supply). This is exactly what drives the red shading on the outage map (§6).

> **∑ Two "dead" numbers.** §4 "affected" (frozen, barrier-free) and §5 "energised" (live, barrier-respecting) now share the **same junction-aware topology** (both walk through a bus), so on a plain radial outage they **match**. They diverge when the live network differs from as-built: affected ignores an additional open device below it (it's barrier-free); and neither subtracts a **closed back-feed tie** — a region fed from a neighbouring feeder through a closed normally-open tie is still *inside* the open device's directed cone, so it can read as dead when it isn't. Read "affected" for reliability and "energised" for the live picture.

---

## 6. Switching orders — and the live-state coupling

A **switching order** (`oms.switching_orders`) is the ordered program a crew executes to isolate the incident and later restore it. Its **steps** (`oms.switching_steps`) each carry a `phase` (`isolate` / `restore`), an `action` (`open` / `close` / `earth` / `verify` / `note`), the target `device_class`/`_id`, an `instruction`, and an `executed_at` (null = outstanding).

> **How to — generate a switching order** 🔒 `oms:write`
> Open the incident → **Generate switching order** (`POST /oms/incidents/{id}/switching-orders`). It builds a **draft** 5-step program around the blamed device:
>
> ```
>   ISOLATE   1. Open    the blamed device
>             2. Verify  N supplies de-energised   (N from the downstream supply count)
>             3. Earth   at the upstream isolation boundary (or at the device if none)
>   RESTORE   4. Note    "remove earths"
>             5. Close   the blamed device
> ```
>
> The upstream isolation boundary is searched among `SwitchingDeviceClasses = {MV_SWITCH, MV_ISOLATOR, MV_SECTIONALIZER, MV_RECLOSER, MV_BREAKER}` — note the **sectionaliser is now included** as a valid isolation point. Requires an attributed device (not a no-device incident), an incident that is not `restored`/`cancelled` (`switching.go:67`), and no existing live order. The *Earth* step's boundary is the nearest upstream hit of the same directed walk, never the blamed device itself.

> **How to — execute a step** 🔒 `oms:write`
> Tick a step (`POST /oms/switching-steps/{id}/execute`, body `{executed?}` defaults true). The order's status rolls up automatically: all steps executed → `completed`, some → `in_progress`, none → `issued`.

> **⚠️ Executing an open/close step changes the real network.** This is the one thing people miss. Ticking an **`open`** or **`close`** step **flips the target device's live `device_state`** — through `crud.UpdateFeature`, so it's validated and logged to `gis.feature_history` — which means **every trace, the power-flow engine and the map immediately reflect it**. Un-ticking a step reverts the state (a *position* round-trip: un-ticking `open` writes `CLOSED` and un-ticking `close` writes `OPEN` — the opposite position, **not** the device's previous one, `switching.go:322-334`). ⚠️ So if a device was already `OPEN` (REC-7 after lock-out) ticking its `open` step is a no-op, but **un-ticking it closes the device in the model**. `verify` / `earth` / `note` steps only record `executed_at`; they touch nothing. (A device with no `device_state`, or one that's been deleted, is silently skipped — the step never fails.)

So as Dana ticks the isolate/restore steps, Priya watches the **red de-energised cone on the outage map shrink** in real time (§5) — because the steps are *actually operating the model*, not just checking boxes.

> ⚠️ **A safety tag can refuse a step.** Executing an open/close step now runs the **operational-safety interlock** first: if the device carries a blocking **danger**/**out-of-service** tag, the step is refused (nothing flips) with the tag's reason — remove the tag or surrender its clearance first. You can also **issue a clearance from a switching order** to danger-tag every device it opens in one action. See **[Operational Safety — Tags, Grounds & Clearances](safety-guide.md)**.

> 💡 **Restore by back-feed (FLISR).** The generated order restores by **re-closing the blamed device** — right once the fault clears. When it *won't* clear quickly, VoltEdge can instead suggest **back-feeding the dead section from a neighbouring feeder** through a **normally-open tie**, load-flow-checked, and draft the close-tie order for you — the **Restoration (FLISR)** panel on the incident. This also closes the "closed back-feed tie" gap §5 flags. See **[FLISR — Service Restoration by Back-Feed](flisr-restoration-guide.md)**.

---

## 7. Planned outages

Not every outage is a fault. A **planned outage** is scheduled around a device for maintenance.

> **How to — schedule a planned outage** 🔒 `oms:write`
> **Outages** → **Plan outage** (`POST /oms/incidents/planned`): pick the device, set `planned_start` / `planned_end`. It creates a `scheduled`, `outage_type = planned` incident with its affected set computed the same way. When the work starts, **confirm** it (`scheduled → confirmed` resets the clock to now and freezes interruptions); **restore** it when supply's back. Planned incidents appear on the board exactly like unplanned ones, flagged by `outage_type` — but ⚠️ they are **excluded from SAIDI/SAIFI/CAIDI** (`indices.go:213`, IEEE 1366 convention); their interruptions are recorded and nothing more. ENS is derived from the same customer-minutes, so it excludes them too.

---

## 8. Restore, and reliability indices

**Restore** (`POST /oms/incidents/{id}/restore`) stamps `restored_at` and closes the interruption windows. **Cancel** (`POST …/cancel`) voids a false incident and releases its calls.

**Reliability indices** (`GET /oms/indices?from=&to=&avg_load_kw=&circuit_id=`, `indices.go`) compute the standard outage metrics over a window (default: the trailing 12 months) from the frozen `customer_interruptions` of **unplanned, non-cancelled** incidents — optionally scoped to one circuit:

- **SAIFI** = customers interrupted ÷ N · **SAIDI** = customer-minutes ÷ N (minutes) · **CAIDI** = customer-minutes ÷ customers interrupted (= SAIDI/SAIFI; 0 when none) · **ENS** = customer-minutes ÷ 60 × `avg_load_kw` (kWh, flagged *estimated*; default 1 kW).
- N = customers served on the live system: Σ `customers_served` (null = 1) over service points **plus PRIMARY meters** (a per-circuit report stays service-point-only on both sides). An interruption is clipped to the window; a still-open one counts to `to`; it counts toward SAIFI only if it *began* in the window. `from` must be before `to`, `avg_load_kw` must not be negative (`400`). MAIFI is not computed. (`indices.go:12-45,133-290`)

> **∑ Source & cause analytics (migration 0168).** `GET /oms/detection-metrics?from=&to=` (`oms:read`, default trailing 12 months, unplanned-only) is the payoff of logging the two axes: the **detection-source mix** (`by_source`), the **`machine_detected_share`** — the fraction known via SCADA/AMI/analytics *without a customer call*, the single best situational-awareness measure — the **cause-family mix** (`by_cause`, via the coded catalog), and the **mean detection→restore time**. It surfaces on the board's **Situational awareness** panel. (This is counts + customer impact by source/cause; the customer-minute **SAIDI** decomposition stays in the indices' `by_fault_cause` below.)

> **∑ The one place OMS reads Faults.** The indices response carries a `by_fault_cause` breakdown — decomposing outages by the cause recorded on the **faults** that explain them. OMS reads `faults.fault_events` for this via an existence check (`to_regclass`), so it **degrades gracefully** (`fault_breakdown_available = false`) if the faults module isn't present. This is a *data* read, not a package import — the modules stay decoupled (see **Faults** for the other direction of the hinge).

---

## 9. Integration

- **Inbound field ground-truth** (`api/internal/webhook`, target **`outage`**). An outage can appear on the board from **telemetry**, not just phoned-in calls — the field counterpart to call-driven prediction. Two `kind`s on one inbound endpoint (`oms:write` service user; device/SP correlated by `device_external_id`/`service_point_external_id` through the `external_ref` index, or named directly):
  - **`device_trip`** — a SCADA/DMS trip or lockout → `oms.RaiseIncidentFromDevice` raises (or returns the already-open) incident blamed on that device, **confirmed**, with its downstream supplies frozen as interruptions and the SAIDI clock started at the reported time. Idempotent per open-incident-per-device, so a re-delivered trip never duplicates the outage. (The device's live `device_state` is left as-is — a paired `operation` feed or an operator sets it; this records the *outage*, not the switch position.)
  - **`last_gasp`** — an AMI meter's dying power-down frame → `oms.RecordLastGasp` logs a machine trouble call (`caller_ref` `AMI:last-gasp`) for that service point — ground truth that THIS supply is dark — feeding the same intake/prediction pipeline a human call does. Deduped on an open AMI call per SP so retransmits don't pile up.
- **Live state / telemetry** (`api/internal/telemetry`, migration 0172). The OMS cone and the switching panel read a device's **live `device_state`** — so the model has to track reality. The telemetry layer keeps it honest: a SCADA/DMS gateway feeds continuous device state into a **dedicated latest-value store** (`ops.device_telemetry`, one row per device, `POST /telemetry` `telemetry:ingest`) — NOT the durable webhook event engine, because telemetry is a high-rate, latest-value, loss-tolerant stream (a newer `measured_at` wins, an older one is dropped, a re-delivery is harmless). `GET /telemetry/reconciliation` (`telemetry:read`) then compares each telemetered device's reported state against its model `device_state` and its reading age against a threshold, flagging **disagreements** ("model CLOSED, field OPEN") and **stale** devices — the **Live State** page. An operator **applies** the field's reading to correct the model (`POST /telemetry/device/{class}/{id}/apply`, `oms:write`, via the same validated `crud.UpdateFeature` path switching uses + a telemetry-sourced device-operation; it bypasses the safety *operate* interlock because it records that the device *has* moved, not a command to move it). ⚠️ Honest scope: this is **telemetry ingestion + reconciliation**, NOT a state estimator, and the SCADA/DMS gateway (DNP3 / IEC 61850 / ICCP) that feeds the endpoint is external. Test it without a gateway via `task seed-telemetry-sim`. MCP: `telemetry_reconciliation`, `telemetry_apply`, `telemetry_ingest`.
- **Outbound webhooks.** `oms.incident.raised` fires on predict, planned-create **and a SCADA auto-raise**; `oms.incident.restored` on restore; `oms.incident.updated` now fires on **confirm**, **dispatch**, and **cancel** (each event's `actor_id` is the acting user). Wire these to a downstream CIS/OMS mirror.
- **Agent / MCP tools** (`api/internal/agenttools/tools_oms.go`) — the whole console is agent-callable (each tool carries its route permission; writes annotated "confirm with a human"): reads `oms_list_trouble_calls`, `oms_get_trouble_call`, `oms_list_incidents`, `oms_get_incident`, `oms_incident_affected_area`, `oms_incident_energization`, `oms_list_switching_orders`, `oms_get_switching_order`, `oms_reliability_indices`, **`oms_list_outage_causes`**, **`oms_detection_metrics`**; writes `oms_create_trouble_call`, `oms_associate_trouble_call`, `oms_run_prediction`, `oms_confirm_incident`, `oms_dispatch_incident`, `oms_create_planned_outage`, `oms_restore_incident`, `oms_cancel_incident`, `oms_generate_switching_order`, `oms_set_switching_step_executed`, `oms_cancel_switching_order`, **`oms_set_incident_attribution`**.
- **The fault & defect hinges** — raise a fault from an incident (→ **Faults**), which in turn books a WAM defect (→ **Works, Inspections & Maintenance**). OMS never touches WAM directly.

---

## 10. Worked example — a Feeder 7 outage, call to restoration

A persistent fault on the Feeder 7 main; **REC-7** does its reclose sequence and **locks out `OPEN`**. Everything below it — the FSW-12/TX-220 tee, RMU-5/TX-230, the tail — goes dark.

1. **Calls arrive.** Several customers below REC-7 report `no_supply`. Priya logs each (or they arrive via IVR) and **associates** them to their service points (`snap = true` resolves them from the pins).
2. **Predict.** Priya runs **Predict**. A call from SP-220 walks upstream (TX-220, C-3, then FSW-12 — a fused `MV_SWITCH`, skipped as not protective — C-2) to **REC-7**. The clinic SP-230, fed through RMU-5's tee way, walks upstream **across the RMU bus** — the junction-aware walk crosses it — and **also** reaches REC-7, so both calls roll into the **one** REC-7 incident (no spurious *no-device* incident at the bus). `confidence` = calling SPs ÷ affected SPs; with both SPs calling and 2 affected, it reads 1.0.
3. **Review who's out.** The **affected** panel lists **2 service points — SP-220 (85 customers) and the clinic SP-230 (`load_priority` P2, critical) — 155 customers, 570 kW**, because the downstream walk now crosses the RMU bus (§4). The **map** shows REC-7 `OPEN` with the whole tail shaded red and a translucent **outage-area polygon** over it; the live cone (§5) reports the **same** 2 service points / 155 customers. With no other open device below, affected and the cone **agree**.
4. **Confirm.** Priya **confirms** the incident — the affected set **freezes** into `customer_interruptions` (the reliability facts), and the SAIDI clock is running.
5. **Switching order.** She **generates** one: *isolate* [open REC-7 — already open; verify 1 supply dead; earth at the upstream boundary, CB-7] → *restore* [remove earths; close REC-7]. Dana executes the isolate steps on the ground and ticks them — each `open`/`close` tick **flips the live state**, and the red cone on Priya's map updates. To restore the **healthy** tail early, Priya closes the normal-open tie **T-79** and back-feeds it from Feeder 9 (a manual operation beyond the auto order — the Switching & Protection guide's restoration case).
6. **Repair.** From the incident Priya **raises a fault** (it inherits REC-7 as the device), the crew finds the cause, and **books a defect** off that fault → a WAM work order for the repair (see **Faults** / **WAM**).
7. **Restore & close.** Repair done, Dana closes REC-7 (ticking the restore step flips it `CLOSED`; do not un-tick the isolate *open* step afterwards — that would write `CLOSED` as well, see §6); the cone **collapses to empty**. Priya **restores** the incident — `restored_at` stamped, interruption windows closed.
8. **Reliability.** Tom pulls `GET /oms/indices` for the month: this outage's frozen interruptions contribute to **SAIDI/SAIFI**, and the `by_fault_cause` breakdown attributes it to the `dig_in` recorded on the fault.

---

## 11. At scale, and troubleshooting

### A storm — many incidents at once

There is **no special "storm mode"** — the loop just runs *N* times in parallel:

- **One Predict run makes many incidents.** Prediction clusters the open calls **per protective device**, so a storm's worth of calls becomes a *set* of incidents in a single run — each with its own blamed device, affected set, switching order and reliability contribution. You drive each the same way.
- **The uncategorised pile.** Calls with no upstream device all land in **one no-device incident** (§3). In a storm that can be large — **attribute it to a device** once you know it (`PATCH /oms/incidents/{id}/device`, §12), fix connectivity and re-predict, or work those calls off the map directly.
- ⚠️ **Storm days are *not* excluded from reliability.** VoltEdge implements **no IEEE 1366 major-event-day exclusion** — every confirmed unplanned interruption counts in full. If your regulator wants MED-normalised figures, apply that downstream of the raw indices (see **Reliability & Indices §8**).
- **Keep the board usable** with `?status=` / `?open=true`, and work highest-`confidence`, most-customers incidents first.

### Troubleshooting

| Symptom | Why | Fix |
|---|---|---|
| **Predict made a `no-device` incident** | the junction-aware upstream walk found **no** `MV_FUSE`/`MV_RECLOSER`/`MV_BREAKER` at all — none seeded on the feeder, broken connectivity upstream, or only an `is_fused` `MV_SWITCH` between the calls and the source (§3). (A one-way RMU bus is no longer a cause — the walk crosses it.) | fix connectivity / seed a protective device so the walk reaches one, **cancel** the no-device incident (its calls return to `open`), and **Predict** again |
| **Wrong / missing blamed device** | prediction found the wrong device, or none | **re-attribute** it — `PATCH /oms/incidents/{id}/device` (the **Attribute device** / **Change device** button) sets or changes the device and re-freezes the affected set (§4, §12) |
| **Affected count > the live cone** | both now walk through junctions, but affected is **barrier-free** (ignores an open device below), while the cone **stops** at it (§4–5) | expected — affected is the reliability set (who the fault cut off), the cone is what's dark right now |
| **Generate switching order refused** | the incident is `restored`/`cancelled`, has no device, or already has a live order (§6) | restore the state, attribute a device, or cancel the existing order |
| **Un-ticking an `open` step closed the device** | un-tick writes the *opposite* position, not the prior one (§6) | re-tick the step, or set `device_state` directly |
| **Planned outage missing from SAIDI/SAIFI** | the indices are **unplanned-only** (§7–8) | by design — planned interruptions are recorded, never summed |
| **A call stays `open` after I associated it** | associate only fills `service_point_id`; **Predict** sets `associated` (§3) | run Predict |
| **`"… does not exist"` is a `400`, not `404`** | every client-fixable error is a 400 `{"error":…}` | check the id |

---

## 12. Crews, field work & subcontractors

Who actually goes out, and the work they carry. Three layers sit behind a dispatch: an internal **crew** (system actors with logins), an external **subcontractor** (track-only, no login), and the WAM **work order** either of them can own. The thread is *outage → dispatch → field job → mobile → completion*.

### Crews and their members (`oms.crews`, `oms.crew_members`)

A **crew** is a dispatchable team: a name, an optional kind (line / cable / contractor), a phone, a home territory, a status (`available` / `dispatched` / `on_site` / `unavailable`), and a roster of **members**. Members are internal users (`iam.users`, joined through `oms.crew_members`, migration 0173) — the field staff who receive the crew's work. A member is **permanent** or **temporary** (`valid_until`, migration 0176): a seconded member auto-drops from routing the moment it lapses, no cleanup needed. A user may belong to **several crews** at once (and receives the union of their work). Manage all of this at **Admin → Crews** (`oms:write`): the roster row opens edit + members; a per-row **Work** button opens the crew's incidents and assigned work orders.

### Dispatch and the assignment lifecycle (`oms.crew_assignments`)

Dispatch a crew to an incident from the incident's **Crews & ETR** card (`POST /oms/incidents/{id}/crews`). It creates a `dispatched` assignment and — if the incident was `confirmed` — advances it to `dispatched`, so the board flag finally reflects a *real* crew (stamps `last_actor_user_id`, fires `oms.incident.updated`). A crew can run **several incidents at once** (storm work); only a duplicate assignment to the *same* incident is refused. The dispatch picker lists **every active crew**, each annotated with its live load (incidents · open orders), so you can see a crew's backlog before piling on more.

An assignment walks `dispatched → on_site → cleared` (`PATCH /oms/crew-assignments/{id}`), each transition timestamped; the crew's status follows and is freed on clear unless it's still busy elsewhere. Set the **ETR** with `PATCH /oms/incidents/{id}/etr`.

### The outage → field-work loop

Dispatching with **Raise restoration work order** ticked creates a **crew-assigned** corrective work order in the *same transaction* — `wam.work_orders` with `source = outage`, `incident_id` linked, pinned to the incident's blamed device so the crew can navigate to it. Because it carries a `crew_id`, it **fans out to every crew member's field pack** (the member-centric pack query also matches a member's active crews). When the crew marks **on-site**, its not-yet-started restoration orders advance to `in_progress` automatically — but **completion stays a deliberate field action** (clearing the assignment is *not* the same as finishing the job). The incident detail's **Field work** list shows the orders raised for that incident.

### Subcontractors — external, track-only (`wam.subcontractors`)

A **subcontractor** is an external firm the utility outsources field work to but which **does not operate in this system**. It is deliberately *not* a crew: no members, no status, no field pack, **no fan-out**. It's a registry row — a name and a contact — that records *who is responsible* (migration 0177; **Admin → Subcontractors**, `wam:write`). Task one to an outage with **Task a subcontractor** on the Crews & ETR card (`POST /oms/incidents/{id}/subcontractor`): it raises a subcontractor-tasked restoration work order linked to the incident, the external parallel to crew dispatch. Any work order can also be tasked to a firm via its `subcontractor_id`. The firm runs the job on their own tools; the utility's own coordinator tracks the order's status. The free-text `assigned_to` label stays for genuinely ad-hoc / unregistered parties.

> **∑ Three ways a job is owned.** `assigned_to_user_id` — one internal **person**. `crew_id` — an internal **crew** (fans out to every member's pack). `subcontractor_id` — an external **firm** (recorded responsibility, *no* fan-out). A job can also carry none (unassigned) or the free-text label.

---

## Appendix — reference

### Tables & key enums

| Table | Status / action values |
|---|---|
| `oms.incidents.status` | `predicted`, `scheduled`, `confirmed`, `dispatched`, `restored`, `cancelled` (`dispatched` set when a crew is sent — §12) |
| `oms.incidents.outage_type` | `unplanned`, `planned` |
| `oms.trouble_calls.call_type` | `no_supply`, `partial`, `flicker` |
| `oms.trouble_calls.status` | `open`, `associated`, `cleared`, `cancelled` |
| `oms.switching_orders.status` | `draft`, `issued`, `in_progress`, `completed`, `cancelled` |
| `oms.switching_steps.phase` / `action` | `isolate` / `restore` ; `open`, `close`, `earth`, `verify`, `note` |
| `oms.crews.status` | `available`, `dispatched`, `on_site`, `unavailable` (§12) |
| `oms.crew_assignments.status` | `dispatched`, `on_site`, `cleared` (§12) |
| `oms.crew_members.valid_until` | `NULL` = permanent member; a timestamp = temporary, drops from routing when past (§12) |
| `wam.subcontractors` | external track-only firms; work tasked via `wam.work_orders.subcontractor_id` (§12) |

### Endpoints (`/oms`, method-aware `oms:read` / `oms:write`)

| Method · Path | Does |
|---|---|
| `GET /oms/trouble-calls` | call queue (filter `status`) |
| `POST /oms/trouble-calls` · `/{id}/associate` | log a call · resolve it to an SP (`snap`) |
| `GET /oms/incidents` · `/map` · `/{id}` | board (filter `status`/`open`) · map markers · one incident |
| `GET /oms/outage-causes` | the coded cause catalog (the cause picker) |
| `PATCH /oms/incidents/{id}/attribution` | set/refine cause (`cause_code`/`cause_status`) + correct detection source |
| `GET /oms/detection-metrics` | source/cause analytics (`by_source`, `machine_detected_share`, `by_cause`, detect→restore) |
| `GET /oms/incidents/{id}/affected` · `/energization` | frozen-or-live affected set · **live** de-energised cone |
| `POST /oms/incidents/predict` · `/planned` | roll calls into incidents · schedule a planned outage |
| `POST /oms/incidents/{id}/confirm` · `/restore` · `/cancel` | confirm (freeze) · restore · cancel |
| `GET`/`POST /oms/incidents/{id}/switching-orders` | list · generate the 5-step order |
| `POST /oms/switching-orders/{id}/cancel` | cancel an order (keeps steps) |
| `POST /oms/switching-steps/{id}/execute` | tick a step (**open/close flips live `device_state`**) |
| `GET /oms/indices` | SAIDI / SAIFI / CAIDI / ENS + `by_fault_cause` (unplanned only) |
| `PATCH /oms/incidents/{id}/device` | **re-attribute** — set/change the blamed device (re-freezes interruptions) |
| `GET`/`POST`/`PATCH`/`DELETE /oms/crews` · `/{id}` · `/{id}/availability` | crew roster CRUD + off-shift/available (§12) |
| `GET`/`POST`/`DELETE /oms/crews/{id}/members` · `/{userId}` | crew membership (§12) |
| `GET /oms/crews/{id}/assignments` | a crew's incident history (§12) |
| `GET`/`POST /oms/incidents/{id}/crews` | an incident's crews · **dispatch** a crew (optionally raise a work order) (§12) |
| `PATCH /oms/crew-assignments/{id}` | advance an assignment `on_site`/`cleared` (on-site starts its restoration orders) (§12) |
| `PATCH /oms/incidents/{id}/etr` | set/clear the ETR (§12) |
| `POST /oms/incidents/{id}/subcontractor` | **task a subcontractor** — raises a tasked restoration work order (§12) |

**Status codes** (`http.go`, `errors.go`): `201` on log-call, planned-outage and generate-order; `200` otherwise. Every client-fixable problem — **including an unknown id** — is `400` `{"error":…}` (there is no `404`); anything unexpected is `500`. `401`/`403` come from the permission gate before the handler.

### Honest notes (what the module does *not* do)

- **A no-device incident *can now* be attributed** — `PATCH /oms/incidents/{id}/device` sets or changes the blamed device after the fact (re-stamping the affected set and re-freezing interruptions for a confirmed incident). This is the console action prediction can't do; it also re-homes an already-blamed incident onto the right device.
- **Affected counts, *verify N* and prediction now use a junction-aware class walk** — the shared `reachableWithDepth` delegates to `bfs`, so supplies behind an RMU's one-way bus **are** counted and a call from one **does** reach its upstream device. The walk stays barrier-free (ignores live switch state). Verified by code reading only; not yet run against a live database.
- **Not territory-scoped** — the board is organisation-wide for any `oms:read` holder (§2).
- **"Affected" (frozen, barrier-free) and "energised" (live, barrier-respecting) are different engines** and can differ; neither subtracts a closed back-feed tie (§4–5).
- **SAIDI/SAIFI/CAIDI/ENS are unplanned-only**; planned outages never enter them (§7–8).
- `cleared` (call status) is defined but never set; un-ticking an `open`/`close` step writes the *opposite* position, not the prior one (§6).
- **Crew ↔ WAM is one-directional** — a crew on-site starts its restoration orders, but completing a work order does **not** clear the crew assignment, and a subcontractor's status is tracked only through its work order (§12).
- The `0087` migration's "decoupled overlay" comment is **stale** — switching steps write live `device_state` (§6).
- OMS records are **not** GIS features.

### Related guides

- **Outage Operations** — the whole call → fault → incident → switch → restore → repair → reliability loop in one place, with this module's legs in context.
- **Faults** — a fault *explains* an incident (raise a fault from the incident) and *books* the repair; the source of the `by_fault_cause` reliability breakdown.
- **Reliability & Indices** — the exact SAIDI/SAIFI/CAIDI/ENS maths behind §8, the customer base, and the honest limits.
- **Switching & Protection** — the `device_state` a switching step flips, the protective devices prediction blames, the isolation-boundary classes (`SwitchingDeviceClasses`), and the de-energised cone.
- **Works, Inspections & Maintenance** — the work order a dispatch or a subcontractor tasking raises, how a crew-assigned order reaches the field pack, and the subcontractor registry (§12).
- **Engineering Studies** — the load/energisation model the affected-customer and cone rollups are built on.

### Corrections made to the first edition (with evidence)

| # | The guide said | The code says | Evidence |
|---|---|---|---|
| 1 | Planned incidents feed the reliability indices exactly like unplanned ones | SAIDI/SAIFI/CAIDI (and ENS) are unplanned-only | `api/internal/oms/indices.go:213,360` |
| 2 | Un-ticking a step is a clean round-trip | it writes the *opposite* position (open→`CLOSED`, close→`OPEN`), not the prior one | `oms/switching.go:322-334` |
| 3 | Associating a call makes it `associated` | associate only sets the service point; `associated` is set by Predict; `cleared` is never set | `oms/store.go:144-195,366`; `oms/models.go:31` |
| 4 | Affected = everything electrically below the device; Feeder 7 tail has a P1 facility and all calls cluster on REC-7 | **topologically true now** — the junction-aware walk crosses the RMU bus and counts the clinic SP-230 (P2): 2 SP / 155 customers, and its call reaches REC-7. Remaining caveat: it's **barrier-free**, so it ignores an open device below (the live cone doesn't) | `trace/service.go` `DownstreamLoadLeaves`; `trace/class_walk.go` `reachableWithDepth`; `trace/graph.go` `bfs`; `docs/switching-and-protection-guide.md` Appendix D #26 |
| 5 | Confirm starts the SAIDI clock | an unplanned incident's clock starts at `started_at` (prediction time) | `oms/store.go:424-429` |
| 6 | Restore follows confirm | restore is allowed from `predicted`/`scheduled` too | `oms/store.go:545-551` |
| 7 | Generate needs only a device and no live order | also refuses a `restored`/`cancelled` incident | `oms/switching.go:67` |
| 8 | Roles: viewer / analyst / editor / admin | `break-glass-admin` holds both permissions too | `migrations/0088_seed_oms_permissions.sql:25-33` |
| 9 | A no-device cluster is one "a human must attribute" | no endpoint or UI mutation sets `predicted_device_*` after creation | `oms/http.go:Routes`; `oms/store.go` (INSERTs only); `web/src/hooks/use-oms.ts` |
| 10 | The console is 18 agent tools | 19: nine reads and ten writes | `api/internal/agenttools/tools_oms.go:36-449` (19 `Name: "oms_…"` entries) |
| 11 | (omitted) indices window, formulas, denominator; error codes | default 12 months; N includes PRIMARY meters; unknown id is `400`, no `404` | `oms/indices.go:133-290`; `oms/http.go`; `oms/errors.go` |

