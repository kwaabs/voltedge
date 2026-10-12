# Outage Operations — from the first call to the reliability report

**Written for:** anyone who runs — or needs the big picture of — the whole outage loop: control-room operators, field crew, maintenance planners and reliability engineers. This is the **overview that ties three modules together**; each leg has a detailed guide of its own, named inline.

VoltEdge splits outage work across three record modules, on purpose — but to an operator they're **one continuous loop**:

```
  trouble call ─► PREDICT ─► INCIDENT ─► SWITCHING ─► RESTORE ─► reliability
   (customer)     (blame a    (outage)    (isolate ·    (supply    (SAIDI/SAIFI)
                   device)                  restore)      back)
                      ▲                                      │
                a FAULT explains the incident        a FAULT books a DEFECT
                      │                                      │
                 [Faults guide]                     [WAM guide] ─► work order ─► repair ─► inspection
```

- **OMS** owns the **outage** — calls, prediction, affected customers, switching orders, restoration, reliability. → **OMS — Outage Management**.
- **Faults** owns the **fault event** — the electrical cause that *explains* the outage and *books* the repair. → **Faults — Logging & Managing Fault Events**.
- **WAM** owns the **repair** — the defect, the work order, the inspection that follows. → **Works, Inspections & Maintenance**.

And the **switchgear** the loop operates (the `device_state` a switching step flips, the protective device prediction blames) is **Switching & Protection**; the **reliability maths** at the end is **Reliability & Indices**.

Two layers:
- **▶ Operator** — the flow across the loop, who does what, in what order.
- **∑ Engineer** — how the modules connect (the hinges, the dependency directions) and the cross-cutting facts that bite.

**The cast.** **Priya** (control-room operator) takes calls and drives incidents and switching; **Dana** (maintenance planner) triages the defect and raises the work order; **Femi** (field technician) executes switching steps and the repair; **Tom** (analyst) reads reliability; **Ada** (admin) holds the permissions. The running example — carried the whole way down — is a **persistent cable fault on Feeder 7** that the recloser **REC-7** locks out on (the same feeder as the Switching & Protection, Faults and OMS guides).

**Legend.** 🔒 a permission (named inline). 💡 a tip. ⚠️ a caution. **▶** the operate lane; **∑** the model lane.

---

## 1. The loop in one page

| Leg | Who | Module (guide) | The one action |
|---|---|---|---|
| A customer reports no supply | public → Priya | **OMS** | log a **trouble call** |
| Roll calls into an outage | Priya | **OMS** | **predict** — blame the nearest protective device |
| Record the electrical cause | Priya | **Faults** | **raise a fault** from the incident |
| Freeze who's affected | Priya | **OMS** | **confirm** the incident |
| Isolate and restore supply | Priya → Femi | **OMS** + **Switching & Protection** | **switching order** (its open/close steps flip `device_state`) |
| Book the repair | Dana | **Faults** → **WAM** | **book a defect** → raise a **work order** |
| Fix the asset | Femi | **WAM** | execute + complete the work order, resolve the defect |
| Close the outage | Priya | **OMS** | **restore** the incident |
| Report performance | Tom | **OMS** + **Reliability & Indices** | **SAIDI / SAIFI / CAIDI / ENS** |

Every leg is a record with an audit trail; together they are one linked story from "my power's off" to "this is what it cost our reliability."

---

## 2. How the three modules connect

Three records, four links — and the links are deliberately **loose** (bare `class`/`id` references, not foreign keys) so a record survives the asset being re-modelled. The *code* dependencies are one-directional and cycle-free:

```
   OMS incident ◄──incident_id──  FAULT  ──defect_id──►  WAM defect ──► work order
       ▲                            │
       └── reads faults for the ────┘   (faults imports wam; neither imports oms)
           "by cause" reliability
```

- **A fault *explains* an incident** — the fault holds `incident_id`. You **raise the fault from the incident** on the OMS side; OMS never imports the faults package. *(The one exception: the reliability report soft-reads `faults.fault_events` for its by-cause breakdown — an existence-checked read that degrades gracefully.)*
- **A fault *books* a defect** — `BookDefectFromFault` calls into WAM (`faults → wam`, the only import between the three). The defect then becomes a **work order** inside WAM.
- **An incident has no WAM column** — the repair hinge runs *only* through the fault. There is no incident → work order shortcut.

> **∑ Three cross-cutting facts that bite — know them once, here.**
> 1. **Executing a switching step changes the real network.** An `open`/`close` step flips the target's **live `device_state`** (through `crud.UpdateFeature`, logged to `gis.feature_history`), so every trace, the power-flow engine and the map move with it. This is the one surprise operators hit. *(Details: OMS §6 / Switching & Protection.)*
> 2. **None of the three modules is territory-scoped.** Faults, OMS and WAM boards are **organisation-wide** for anyone with the read permission — unlike the map's features (Phase 58). If you rely on territory scoping to partition what a user sees, it does **not** apply here.
> 3. **"Affected" and "de-energised" are different numbers.** OMS's frozen *affected* count (barrier-free, for SAIDI) and the live *de-energised* cone (barrier-respecting, for the map) are computed by different engines and will differ — on purpose. Neither subtracts a closed back-feed tie. *(OMS §4–5.)*

---

## 3. The end-to-end story — one fault, all the way down

Follow the **Feeder 7** fault through every module. Each step names the guide and endpoint that owns it, so this is both the story *and* a map into the detail.

**0 · It happens.** A contractor's excavator cuts the 11 kV main. The fault is persistent; **REC-7** runs its reclose sequence and **locks out `OPEN`**. Everything below it — the FSW-12/TX-220 tee, RMU-5/TX-230, the tail — goes dark. *(Why REC-7 and not the fuse: Switching & Protection, coordination.)*

**1 · Calls come in** 🔒 `oms:write` *(Priya — OMS §3)*. Customers below REC-7 report `no_supply`. Priya logs each (`POST /oms/trouble-calls`) and **associates** them to their service points (`snap=true` resolves them from the pins).

**2 · Predict** 🔒 `oms:write` *(Priya — OMS §3)*. Priya runs **Predict** (`POST /oms/incidents/predict`). Each dead SP walks upstream to the nearest **protective** device — past the un-protective fused switch and sectionaliser — to **REC-7**. The calls cluster into **one incident**, blamed on REC-7, with a rising **confidence** and an **affected** list of every supply below it (flagging a **critical** P1 facility on the tail).

**3 · Record the cause — raise a fault** 🔒 `faults:write` *(Priya — Faults §6)*. From the incident, **Raise fault** (`POST /faults/from-incident/{id}`). The fault is born anchored to `incident_id`, inheriting REC-7 as the operating device. Priya fills in what she knows: `fault_type = earth`, `detected_by = scada`.

**4 · Confirm — freeze who's out** 🔒 `oms:write` *(Priya — OMS §4)*. She **confirms** the incident (`POST /oms/incidents/{id}/confirm`) — the affected set **freezes** into `customer_interruptions` (the reliability facts), and the SAIDI clock starts.

**5 · Switch — isolate and restore** 🔒 `oms:write` *(Priya → Femi — OMS §6 / Switching & Protection)*. Priya **generates a switching order** (`POST …/switching-orders`): *isolate* [open REC-7; verify supplies dead; earth at the upstream boundary] → *restore* [remove earths; close REC-7]. Femi executes the isolate steps on the ground and **ticks** them (`POST /oms/switching-steps/{id}/execute`) — each `open`/`close` tick **flips the live `device_state`**, and Priya watches the red cone on the outage map update. To get the **healthy tail** back early, Priya closes the normal-open tie **T-79** and back-feeds it from Feeder 9 (a manual operation beyond the auto order).

**6 · Investigate** 🔒 `faults:write` *(Priya — Faults §4)*. The patrol finds the dig-in; Priya `PATCH`es the fault: `cause = "dig_in"`, `fault_current_a = 3900`. *(Tom later checks that 3,900 A against the coordination study's 3,913 A — a good match.)*

**7 · Book the repair** 🔒 `faults:write` + `wam:write` *(Dana — Faults §6 → WAM)*. From the fault, **Book defect** (`POST /faults/{id}/defect`) → a WAM **defect** (source *outage*, carrying `incident_id`). Dana triages it and **raises a corrective work order** from it (one click: creates the job, marks the defect "work ordered"). *(WAM guide, the defect → order loop.)*

**8 · Fix it** 🔒 `wam:write` *(Femi — WAM)*. Femi executes the work order (splice the cable), **completes** it with notes, and **resolves** the defect. The asset is healthy again. *(The linked trail finding → job → done is exactly what the reliability report and the next engineer need.)*

**9 · Restore & close** 🔒 `oms:write` *(Priya — OMS §8)*. Femi closes REC-7 (ticking the restore step flips it `CLOSED`); the live cone **collapses to empty**. Priya **restores** the incident (`POST …/restore`) — `restored_at` stamped, interruption windows closed. She then **closes the fault** (`cleared → closed`).

**10 · Report** 🔒 `oms:read` *(Tom — OMS §8 / Reliability & Indices)*. At month-end Tom pulls `GET /oms/indices`: this outage's frozen interruptions feed **SAIDI/SAIFI**, and the `by_fault_cause` breakdown attributes it to the `dig_in` on the fault.

```
  CALL ─► PREDICT ─► [raise FAULT] ─► CONFIRM ─► SWITCHING(flip state) ─► RESTORE
   §1       §2           §3            §4            §5                     §9
                          └──► investigate(§6) ─► [book DEFECT ─► WORK ORDER ─► repair] ─► resolve(§7–8)
                                                                                              │
                                                                                     RELIABILITY(§10)
```

That's the whole loop: eight records across three modules, each a link in one auditable chain from "my power's off" to "here's what it cost."

---

## 4. Roles & permissions across the loop

The loop crosses three permission pairs. One matrix for the whole thing:

| Capability | Permission | viewer | analyst | editor | field role | admin |
|---|---|---|---|---|---|---|
| See outages, faults, works boards | `oms:read` / `faults:read` / `wam:read` | ✓ | ✓ | ✓ | ✓ | ✓ |
| Log calls, predict, confirm, switch, restore | `oms:write` | — | — | ✓ | — | ✓ |
| Log / clear faults, book defect | `faults:write` | — | — | ✓ | — | ✓ |
| Triage defects, raise / complete work orders, record inspections | `wam:write` | — | — | ✓ | ✓ (field) | ✓ |

(`break-glass-admin` holds everything.) Note the **field roles** grant `wam:write` for offline crews but **not** `oms:write` — Femi executes the repair and records inspections on a phone, but driving the incident and the switching order stays with an `editor` (Priya). ⚠️ Remember none of these three boards is territory-scoped (§2).

---

## 5. Variations on the loop

The spine above is the big unplanned fault. Four common variants:

- **A minor fault, no outage.** A fuse clears a single tee; only one transformer drops. There may be no trouble-call cluster worth an incident — just **log the fault** directly against the asset (Faults §5) and book a defect if the asset needs a fix. The loop runs **Faults → WAM**, skipping OMS.
- **A planned outage.** No fault at all. Dana **schedules** a planned outage around a device (OMS §7: `scheduled → confirmed → restored`), generates the switching order, the crew executes it, supply restores. It feeds the reliability indices flagged as `planned`. The loop runs **OMS → (switching)**, no Faults, no WAM — unless the planned work *is* a repair booked earlier off a defect.
- **A storm — many incidents at once.** Prediction clusters calls **per device**, so a storm becomes **many incidents**, each with its own switching order and reliability contribution. There's no special "storm mode" — you run the same loop N times; the reliability window later aggregates them. ⚠️ VoltEdge implements **no IEEE 1366 major-event-day exclusion** — storm days are *not* automatically carved out of SAIDI (see Reliability & Indices).
- **A fault found by inspection (reverse flow).** Femi's inspection checklist fails an item → WAM **auto-raises a defect** (WAM §5) → work order → repair. Here the loop starts in **WAM** and may never touch OMS or Faults at all — a defect doesn't need an outage to exist.

---

## 6. What's automated vs. what you drive

Being clear about this prevents both over-trust and needless clicking:

| Automated | You drive (manual) |
|---|---|
| **Prediction** clusters calls and blames the nearest protective device | **Associating** a call to its service point (or `snap`) |
| **Affected / critical** rollups from the network graph | **Confirming** an incident (the freeze is a decision) |
| A switching step **flips the live `device_state`** when ticked | **Generating** the order, and **ticking** each step as the crew does it |
| The de-energised **cone redraws** as steps are ticked | **Raising the fault** from the incident |
| **Raise work order from defect** is one linked click | **Booking the defect** from the fault; **resolving** it after the fix |
| **Reliability indices** compute from frozen interruptions | Reading them, and attributing the `cause` on the fault |

> 💡 The habit that makes the whole loop worth it: **keep the chain linked.** Raise the fault from the incident (don't log a loose one), book the defect from the fault (don't log a loose one), raise the work order from the defect, resolve the defect after completing the order. Every shortcut you skip is a gap in the audit trail the reliability report and the next engineer depend on.

---

## Appendix — cross-module reference

### The four links (and their directions)

| Link | Column | Direction | Set by |
|---|---|---|---|
| Fault → incident | `faults.incident_id` | fault *explains* incident | **Raise fault from incident** (Faults) |
| Fault → defect | `faults.defect_id` | fault *books* defect | **Book defect** (Faults → WAM) |
| Defect → work order | WAM internal | defect *raises* order | **Raise work order** (WAM) |
| Incident ← reliability | soft-read of `faults.fault_events` | OMS reads faults *by cause* | `GET /oms/indices` |

### The five gotchas (all stated honestly in the detail guides)

1. Switching steps **flip live `device_state`** (OMS §6).
2. None of OMS / Faults / WAM is **territory-scoped**.
3. "Affected" (frozen) ≠ "de-energised" (live) — different engines (OMS §4–5).
4. No **IEEE 1366** major-event exclusion in the reliability indices.
5. Prediction blames the **nearest protective device** only — a single-fault hypothesis a human may need to correct.

### The guides this one ties together

- **OMS — Outage Management** — calls, prediction, affected customers, the live cone, switching orders, restoration, reliability.
- **Faults — Logging & Managing Fault Events** — the fault record, its lifecycle, and its links to the outage and the repair.
- **Works, Inspections & Maintenance** — the defect → work order → inspection repair loop.
- **Switching & Protection** — the devices the loop operates, the `device_state` it flips, the protective device prediction blames.
- **Reliability & Indices** — the SAIDI / SAIFI / CAIDI / ENS maths at the end of the loop.


