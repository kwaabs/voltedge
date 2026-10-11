# OMS — Outage Management

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

> **Grounding note.** Drawn from the shipped code: schema in `migrations/0087_create_oms.sql` (+ `0088` permissions, `0089` planned outages & switching orders, `0119` interruption feature-class); engine in `api/internal/oms` (`predict.go`, `switching.go`, `energization.go`, `store.go`, `indices.go`); UI in `web/src/routes/outages.tsx`, `web/src/components/map/outage-map.tsx`, `web/src/hooks/use-oms.ts`. OMS records are **not** compiled `gis.*` features. ⚠️ The `0087` migration comment calls OMS a decoupled overlay — that prose is **stale**: since 2026-09-24 switching steps write live state (§6).

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
| See the board, calls, affected area, energisation, switching orders, reliability | `oms:read` | ✓ | ✓ | ✓ | ✓ |
| Log/associate calls, predict, confirm, plan, restore, cancel, generate order, **execute step** | `oms:write` | — | — | ✓ | ✓ |

`break-glass-admin` holds both permissions too, exactly like `admin` (`0088:25-33`). A viewer gets a **read-only board** (the write controls self-gate). A request without the needed permission is a `403`; without a session, `401` (`auth/rbac.go:66-85`). Two honest cautions:

> ⚠️ **OMS is not territory-scoped.** No `oms` table has a `territory_id`, and no incident/board query filters by territory (Phase 58 scoping lives *inside* the `trace` primitives OMS calls, e.g. back-feed paths, not in the OMS queries). The outage board shows **every** territory's incidents to any `oms:read` holder.

> ⚠️ **Incidents record no "who".** There is no actor/user column on an incident — `crew_ref` is free text only. The one real audit trail is indirect: when a switching step flips a device's state, *that* write is logged to `gis.feature_history` with the acting user (§6).

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
- ⚠️ **The upstream walk is strictly directed.** It follows connectivity rows from-to-the-source only (`trace/class_walk.go:175-200`, `neighborsOf` at `trace/graph.go:575-609`): no junction pass-through, no tier guard, no live-state barriers. A service point fed through an RMU bus (the tee way points *into* the bus, so the walk runs out of arrows) returns **no** protective device and lands in the "no-device" cluster, whose incident has a null `confidence`. An `is_fused` `MV_SWITCH` (e.g. FSW-12) is never blamed — only the class codes `MV_FUSE`/`MV_RECLOSER`/`MV_BREAKER` count (`predict.go:38`).
- **Confidence** = distinct calling SPs ÷ affected SPs (capped at 1.0) — one call on a big feeder is low-confidence; half the street calling is high.

> **How to — predict incidents** 🔒 `oms:write`
> **Outages** → **Predict** (`POST /oms/incidents/predict`). Open calls roll into `predicted` incidents, each blamed on a device, with affected counts and a confidence. Calls are marked `associated` and linked.

---

## 4. The incident & who's affected

An **incident** (`oms.incidents`) is the outage record: the `predicted_device_class`/`_id` it's blamed on, a `circuit_id` reporting axis, an `outage_type` (`unplanned` / `planned`), a free-text `cause_code`, `confidence`, timings (`started_at`, `etr`, `restored_at`), denormalised `affected_customers` / `affected_service_points`, and `crew_ref` / `notes`.

**Status machine** (`models.go`):

```
  unplanned:  predicted ──► confirmed ──► restored        (or ──► cancelled)
  planned:    scheduled ──► confirmed ──► restored         (confirmed = executed)
```

(`dispatched` is a defined status value but nothing transitions to it — stored, not wired. The API also lets you **restore** a `predicted` or `scheduled` incident directly — only `restored`/`cancelled` are refused (`store.go:545-551`); with no frozen interruptions it simply contributes nothing to the indices.)

- **`predicted`** — just rolled up from calls; affected counts are **live** estimates.
- **`confirmed`** — the operator confirms the outage; this **freezes** the affected set into `customer_interruptions` rows (the reliability facts that won't move even as the network changes). For an unplanned incident each row's clock starts at the incident's `started_at` (the moment prediction created it), **not** at the confirm click (`store.go:424-429`); a planned outage's clock starts at confirm. An incident with no blamed device freezes nothing.
- **`restored`** — supply's back; `restored_at` stamped, interruption windows closed.
- **`cancelled`** — a false alarm; linked calls are released back to `open`.

**Who's affected (∑).** The affected rollup walks **downstream of the blamed device** and sums supplies — `trace.DownstreamLoadLeaves` → service points + customers, with a `Service` rollup splitting load (kW) and customers by **restoration tier** / `load_priority`, flagging **critical** facilities (rank ≤ P2). Two supply classes count: unmetered `SERVICE_POINT`s and `PRIMARY` `METER`s (a null customer count reads as 1). `GET /oms/incidents/{id}/affected` returns the **frozen** rows once confirmed, falling back to a **live** trace while still predicted.

> **∑ Affected is deliberately conservative — and directed.** The affected/frozen rollup is **barrier-free and topological** — it counts the supplies *reached by a strictly directed downstream walk* from the device regardless of live switch state (`trace.DownstreamLoadLeaves` → `reachableWithDepth`, `trace/service.go:432-445`, `trace/class_walk.go:175`). That is *not* the same as "everything electrically below": the walk has no junction pass-through, so supplies behind an RMU's one-way bus (and the ring tail beyond Way B) are **missed**, and the switching order's *verify N* count inherits the same miss. That's correct for reliability (SAIDI/SAIFI shouldn't shrink because a tie happens to be closed), but it means the "affected customers" count can be larger than what's actually dark right now. For *what's dark right now*, use the live energisation cone (§5) — and expect the two to differ.

---

## 5. The live de-energised cone

`GET /oms/incidents/{id}/energization` answers *"what is dark right now?"* (`energization.go`). It reads the blamed device's **live `device_state`**:

- If the device is **not** `OPEN` → the set is **empty** (supply is on).
- If it's `OPEN` → `trace.DeEnergizedCone(device)` — a **barrier-respecting**, directed-downstream walk — returns the dead service-point geometry plus a `Service` rollup.

The cone is **live**: open the device and it grows; close it and it collapses to empty; open a further device below it (a sectionalising or back-feed step) and it **shrinks** to just the still-isolated part (the customers beyond that further open device are not in *this* device's cone — they belong to the further-open device's own cone, even though they are still without supply). This is exactly what drives the red shading on the outage map (§6).

> **∑ Two "dead" numbers, on purpose.** §4 "affected" (frozen, barrier-free) and §5 "energised" (live, barrier-respecting) are **different engines** and will report different sets. Neither subtracts a **closed back-feed tie** — a region fed from a neighbouring feeder through a closed normal-open tie is still *inside* the open device's directed cone, so it can read as dead when it isn't. Read "affected" for reliability and "energised" for the live picture, and know they're not meant to match.

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

> **∑ The one place OMS reads Faults.** The indices response carries a `by_fault_cause` breakdown — decomposing outages by the cause recorded on the **faults** that explain them. OMS reads `faults.fault_events` for this via an existence check (`to_regclass`), so it **degrades gracefully** (`fault_breakdown_available = false`) if the faults module isn't present. This is a *data* read, not a package import — the modules stay decoupled (see **Faults** for the other direction of the hinge).

---

## 9. Integration

- **Outbound webhooks.** `oms.incident.raised` fires on predict and planned-create; `oms.incident.restored` on restore. *(An `oms.incident.updated` event constant exists but isn't emitted — confirm/cancel/switching are silent.)* Wire these to a downstream CIS/OMS mirror.
- **Agent / MCP tools** (`api/internal/agenttools/tools_oms.go`) — the whole console is agent-callable (18 tools, each carrying its route permission; writes annotated "confirm with a human"): reads `oms_list_trouble_calls`, `oms_get_trouble_call`, `oms_list_incidents`, `oms_get_incident`, `oms_incident_affected_area`, `oms_incident_energization`, `oms_list_switching_orders`, `oms_get_switching_order`, `oms_reliability_indices`; writes `oms_create_trouble_call`, `oms_associate_trouble_call`, `oms_run_prediction`, `oms_confirm_incident`, `oms_create_planned_outage`, `oms_restore_incident`, `oms_cancel_incident`, `oms_generate_switching_order`, `oms_set_switching_step_executed`, `oms_cancel_switching_order`.
- **The fault & defect hinges** — raise a fault from an incident (→ **Faults**), which in turn books a WAM defect (→ **Works, Inspections & Maintenance**). OMS never touches WAM directly.

---

## 10. Worked example — a Feeder 7 outage, call to restoration

A persistent fault on the Feeder 7 main; **REC-7** does its reclose sequence and **locks out `OPEN`**. Everything below it — the FSW-12/TX-220 tee, RMU-5/TX-230, the tail — goes dark.

1. **Calls arrive.** Several customers below REC-7 report `no_supply`. Priya logs each (or they arrive via IVR) and **associates** them to their service points (`snap = true` resolves them from the pins).
2. **Predict.** Priya runs **Predict**. A call from SP-220 walks upstream (TX-220, C-3, then FSW-12 — a fused `MV_SWITCH`, skipped as not protective — C-2) to **REC-7**: an incident blamed on REC-7. ⚠️ A call from the clinic SP-230, fed through RMU-5's tee way, runs out of arrows at the RMU bus (strictly directed walk) and finds **no** device: it forms a separate *no-device* incident (null confidence) that cannot be attributed from the console (§3). `confidence` for the REC-7 incident = calling SPs ÷ affected SPs, and the affected count here is 1 service point (SP-220, 85 customers), so even one call reads 1.0.
3. **Review who's out.** The **affected** panel (barrier-free, directed) lists **SP-220 only — 1 service point, 85 customers**; it misses the clinic SP-230 (`load_priority` P2, critical) behind the RMU bus. The **map** shows REC-7 `OPEN` and the whole tail shaded red (the live cone — §5, which walks through junctions and *does* include SP-230: 2 service points, 155 customers, 570 kW). The two numbers disagree by design, and by this limitation.
4. **Confirm.** Priya **confirms** the incident — the affected set **freezes** into `customer_interruptions` (the reliability facts), and the SAIDI clock is running.
5. **Switching order.** She **generates** one: *isolate* [open REC-7 — already open; verify 1 supply dead; earth at the upstream boundary, CB-7] → *restore* [remove earths; close REC-7]. Dana executes the isolate steps on the ground and ticks them — each `open`/`close` tick **flips the live state**, and the red cone on Priya's map updates. To restore the **healthy** tail early, Priya closes the normal-open tie **T-79** and back-feeds it from Feeder 9 (a manual operation beyond the auto order — the Switching & Protection guide's restoration case).
6. **Repair.** From the incident Priya **raises a fault** (it inherits REC-7 as the device), the crew finds the cause, and **books a defect** off that fault → a WAM work order for the repair (see **Faults** / **WAM**).
7. **Restore & close.** Repair done, Dana closes REC-7 (ticking the restore step flips it `CLOSED`; do not un-tick the isolate *open* step afterwards — that would write `CLOSED` as well, see §6); the cone **collapses to empty**. Priya **restores** the incident — `restored_at` stamped, interruption windows closed.
8. **Reliability.** Tom pulls `GET /oms/indices` for the month: this outage's frozen interruptions contribute to **SAIDI/SAIFI**, and the `by_fault_cause` breakdown attributes it to the `dig_in` recorded on the fault.

---

## Appendix — reference

### Tables & key enums

| Table | Status / action values |
|---|---|
| `oms.incidents.status` | `predicted`, `scheduled`, `confirmed`, `restored`, `cancelled` (+ `dispatched`, defined but unused) |
| `oms.incidents.outage_type` | `unplanned`, `planned` |
| `oms.trouble_calls.call_type` | `no_supply`, `partial`, `flicker` |
| `oms.trouble_calls.status` | `open`, `associated`, `cleared`, `cancelled` |
| `oms.switching_orders.status` | `draft`, `issued`, `in_progress`, `completed`, `cancelled` |
| `oms.switching_steps.phase` / `action` | `isolate` / `restore` ; `open`, `close`, `earth`, `verify`, `note` |

### Endpoints (`/oms`, method-aware `oms:read` / `oms:write`)

| Method · Path | Does |
|---|---|
| `GET /oms/trouble-calls` | call queue (filter `status`) |
| `POST /oms/trouble-calls` · `/{id}/associate` | log a call · resolve it to an SP (`snap`) |
| `GET /oms/incidents` · `/map` · `/{id}` | board (filter `status`/`open`) · map markers · one incident |
| `GET /oms/incidents/{id}/affected` · `/energization` | frozen-or-live affected set · **live** de-energised cone |
| `POST /oms/incidents/predict` · `/planned` | roll calls into incidents · schedule a planned outage |
| `POST /oms/incidents/{id}/confirm` · `/restore` · `/cancel` | confirm (freeze) · restore · cancel |
| `GET`/`POST /oms/incidents/{id}/switching-orders` | list · generate the 5-step order |
| `POST /oms/switching-orders/{id}/cancel` | cancel an order (keeps steps) |
| `POST /oms/switching-steps/{id}/execute` | tick a step (**open/close flips live `device_state`**) |
| `GET /oms/indices` | SAIDI / SAIFI / CAIDI / ENS + `by_fault_cause` (unplanned only) |

**Status codes** (`http.go`, `errors.go`): `201` on log-call, planned-outage and generate-order; `200` otherwise. Every client-fixable problem — **including an unknown id** — is `400` `{"error":…}` (there is no `404`); anything unexpected is `500`. `401`/`403` come from the permission gate before the handler.

### Honest notes (what the module does *not* do)

- **A no-device incident cannot be attributed** — there is no way to set its device after the fact; it can be confirmed (freezing nothing), restored or cancelled (§3).
- **Affected counts, *verify N* and prediction use a strictly directed class walk** — supplies behind an RMU's one-way bus are not counted and a call from one can come back with no device (§3–4, §10). Read by code only; not run against a live database.
- **Not territory-scoped** — the board is organisation-wide for any `oms:read` holder (§2).
- **No actor column** on an incident — "who" is only in `gis.feature_history` for the switching-step state flips (§2).
- **"Affected" (frozen, barrier-free) and "energised" (live, barrier-respecting) are different engines** and can differ; neither subtracts a closed back-feed tie (§4–5).
- **SAIDI/SAIFI/CAIDI/ENS are unplanned-only**; planned outages never enter them (§7–8).
- `cleared` (call status) is defined but never set; un-ticking an `open`/`close` step writes the *opposite* position, not the prior one (§6).
- `cause_code` is **free text**, not a coded catalogue.
- `dispatched` status and the `oms.incident.updated` webhook are **defined but never fired**.
- An incident has **no WAM link** — the defect/work-order hinge is only through a fault.
- The `0087` migration's "decoupled overlay" comment is **stale** — switching steps write live `device_state` (§6).
- OMS records are **not** GIS features.

### Related guides

- **Faults** — a fault *explains* an incident (raise a fault from the incident) and *books* the repair; the source of the `by_fault_cause` reliability breakdown.
- **Switching & Protection** — the `device_state` a switching step flips, the protective devices prediction blames, the isolation-boundary classes (`SwitchingDeviceClasses`), and the de-energised cone.
- **Works, Inspections & Maintenance** — the defect/work-order the fault books.
- **Engineering Studies** — the load/energisation model the affected-customer and cone rollups are built on.

### Corrections made to the first edition (with evidence)

| # | The guide said | The code says | Evidence |
|---|---|---|---|
| 1 | Planned incidents feed the reliability indices exactly like unplanned ones | SAIDI/SAIFI/CAIDI (and ENS) are unplanned-only | `api/internal/oms/indices.go:213,360` |
| 2 | Un-ticking a step is a clean round-trip | it writes the *opposite* position (open→`CLOSED`, close→`OPEN`), not the prior one | `oms/switching.go:322-334` |
| 3 | Associating a call makes it `associated` | associate only sets the service point; `associated` is set by Predict; `cleared` is never set | `oms/store.go:144-195,366`; `oms/models.go:31` |
| 4 | Affected = everything electrically below the device; Feeder 7 tail has a P1 facility and all calls cluster on REC-7 | strictly directed class walk: the clinic SP-230 (P2) behind the RMU bus is missed (1 SP / 85 customers), its call has no device | `trace/service.go:432-445`; `trace/class_walk.go:175-200`; `trace/graph.go:575-609`; `docs/switching-and-protection-guide.md` Appendix D #26 |
| 5 | Confirm starts the SAIDI clock | an unplanned incident's clock starts at `started_at` (prediction time) | `oms/store.go:424-429` |
| 6 | Restore follows confirm | restore is allowed from `predicted`/`scheduled` too | `oms/store.go:545-551` |
| 7 | Generate needs only a device and no live order | also refuses a `restored`/`cancelled` incident | `oms/switching.go:67` |
| 8 | Roles: viewer / analyst / editor / admin | `break-glass-admin` holds both permissions too | `migrations/0088_seed_oms_permissions.sql:25-33` |
| 9 | A no-device cluster is one "a human must attribute" | no endpoint or UI mutation sets `predicted_device_*` after creation | `oms/http.go:Routes`; `oms/store.go` (INSERTs only); `web/src/hooks/use-oms.ts` |
| 10 | (omitted) indices window, formulas, denominator; error codes | default 12 months; N includes PRIMARY meters; unknown id is `400`, no `404` | `oms/indices.go:133-290`; `oms/http.go`; `oms/errors.go` |

