# FLISR — Service Restoration by Back-Feed

**Written for:** control-room operators restoring supply after a fault, planners checking a feeder's transfer capability, and engineers who want to know exactly how the suggestion and its capacity verdict are computed.

**FLISR** is *Fault Location, Isolation & Service Restoration*. VoltEdge already does the first two (a fault is **located** and **blamed** on a device, and an **isolate** trace / switching order bounds the work area — see **OMS** and **Switching & Protection**). This guide is the **restoration** half: when a protective device trips and leaves a section dark, VoltEdge **suggests how to bring it back** — which **normally-open tie** to close so the dead section is **back-fed from a neighbouring feeder** — and checks, with a real load-flow sweep, whether that feeder can actually carry the extra load.

It is a **suggestion engine**. It writes nothing and flips no switch; it hands the operator a ranked list of moves, and — on one click — drafts the close-tie **switching order** that performs the chosen move.

```
  tripped device ─► DEAD CONE ─► candidate TIES ─► energised ALT SOURCE ─► ranked PLANS
   (REC-7 locks     (live, what   (normally-open    (back-feed origin +     (capacity-checked,
    out Feeder 7)    is dark now)   points on its     its present load)       best first)
                                    boundary)                │
                                                             ▼
                                              CLOSE-TIE SWITCHING ORDER (one click)
```

Two layers, as elsewhere:
- **▶ Operator** — on the outage board: open the incident, read the **Restoration** panel, pick a plan, **Build switching order**, execute the close step.
- **∑ Engineer** — the five-step engine, the two capacity bases (nameplate vs. load-flow), the endpoints, the reused contingency sweep, and the honest limits.

**The cast** is the OMS guide's: **Priya** (control-room operator) restores supply; **Dana** (field crew) executes the close on the ground; **Ada** (admin) models the ties. The running example is the same **persistent fault on Feeder 7** that recloser **REC-7** locks out on, leaving a mid-feeder section dark that a **normally-open tie** to **Feeder 9** could back-feed.

**Legend.** 🔒 a permission. 💡 a tip. ⚠️ a caution or an honest limitation. **▶** operate lane; **∑** model lane. A boxed **How to** is click-by-click.

> **Grounding note.** Drawn from the shipped code: engine `api/internal/oms/restore.go` (`SuggestRestoration`, `GenerateRestorationOrder`); the load-flow check reuses the contingency engine `api/internal/powerflow/contingency.go` via the exported `api/internal/powerflow/restore_export.go` (`EvaluateRestorationTie`); endpoints in `api/internal/oms/http.go`; MCP tools in `api/internal/agenttools/tools_oms.go`; UI in `web/src/routes/outages.tsx` (the Restoration panel) and `web/src/hooks/use-oms.ts`. It composes existing primitives — the de-energised cone, the tie model, the service rollup and the contingency sweep — rather than adding a new graph engine.

---

## 1. Why this exists — the back-feed gap

The OMS guide flags a limitation twice: a region fed from a neighbouring feeder **through a closed normally-open tie** is still inside the tripped device's *directed* cone, so the live energisation read treats it as dark when it isn't. FLISR is the other side of that coin — it is the tool that **tells you which tie to close** to make that back-feed, and whether the donor feeder can take it.

Think of it as answering the control-room question after an isolate: *"The section past REC-7 is dead and the fault is isolated downstream — can I pick those customers up from somewhere else while the repair happens?"*

---

## 2. Who may do what

The same two OMS permissions (`auth.PermOMSRead` / `PermOMSWrite`), method-aware on the `/oms` mount:

| Action | Permission | viewer | editor (Priya) | admin (Ada) |
|---|---|---|---|---|
| See the suggested restoration plans | `oms:read` | ✓ | ✓ | ✓ |
| Build the close-tie switching order from a plan | `oms:write` | — | ✓ | ✓ |

The Restoration panel self-gates: a viewer sees the plans but no **Build** button. `break-glass-admin` holds both.

> ⚠️ **Not territory-scoped.** Like the rest of OMS, the restoration suggestion is not filtered by territory. The Phase-58 spatial scope lives *inside* the `trace` primitives it calls (the dead cone, the back-feed neighbours), not in the restoration query itself.

---

## 3. Prerequisites — model the ties

FLISR can only suggest a tie it can **see**. A tie is a switch that is **declared normally open**: its class carries the Phase-48 `normal_state` field (field-pack `seed/switch_normal_state_fields.sql`) and the specific feature's live `normal_state = NORMALLY_OPEN`. Place them with the **Add tie** flow (see **Switching & Protection** → ties/couplers), which wires a `NORMALLY_OPEN` switch between two feeders.

If no tie is modelled on the dead section's boundary, the panel says so plainly (*"no normally-open tie borders the de-energized section"*) rather than inventing a path. 💡 This is the single most common reason the panel is empty — model the tie first.

---

## 4. ▶ The operator flow

> **How to — restore a dead section by back-feed**
> 1. Open the outage board (**Outage Management**) and select the active incident (its device is open; the live cone shows customers dark).
> 2. In the right-hand **Switching program** drawer, below any isolation order, read the **Restoration (FLISR)** panel.
> 3. Each card is one plan, best first: **Close `<tie>`**, the feeder it back-feeds **from**, the **customers / supplies / kVA** it restores, and a **capacity badge**.
> 4. Pick a plan whose capacity badge is green (**Capacity OK**). Click **Build switching order**.
> 5. The new **Restoration of … via …** order appears in the switching list above, with its steps: *confirm the section is isolated → close the tie → verify restored*.
> 6. Dana executes the **Close** step; executing it flips the tie's live `device_state` to closed, the live cone collapses, and the map redraws with supply restored.

**Reading a plan card.** The capacity badge is the headline:

| Badge | Meaning |
|---|---|
| **Capacity OK · load-flow** | A real sweep says the donor feeder picks up the section with no voltage violation and no overload — a move you can make as-is. |
| **Capacity OK** (no suffix) | The nameplate estimate fits (no load-flow voltage was available to sweep). |
| **Overloads feeder** | A sweep found a post-transfer voltage violation or thermal overload — the back-feed is not clean. |
| **May overload** | The nameplate estimate says the restored load exceeds the donor transformer's spare rating. |
| **Capacity unverified** | Neither a load-flow voltage nor a transformer nameplate was available — the fit can't be judged here. |

A load-flow card also shows a **Load-flow:** line — *feasible*, or *N voltage violation(s), M overload(s)* and the **worst per-unit voltage**.

> ⚠️ Building the order from an **Overloads feeder** / **May overload** plan is allowed — the order simply carries a **WARNING** note step before the close, citing the violations/overloads (or the kVA excess). The operator, not the tool, decides.

---

## 5. ∑ The engine — five steps

`SuggestRestoration(device)` (or `SuggestRestorationForIncident(id)`, which resolves the incident's blamed device) runs:

1. **Dead cone.** `trace.DeEnergizedCone(device)` — the section dark *right now* (it reads live `device_state`, so it is the real present extent, and it includes the open tie at its boundary as a reached barrier node). Empty cone → nothing to restore.
2. **Candidate ties.** The cone features whose class is **tie-capable** (offers `normal_state`/`NORMALLY_OPEN`) **and** whose live `normal_state` is `NORMALLY_OPEN`.
3. **Live far side.** For each tie, its connected neighbours **not** in the dead cone (a direct read of `runtime.feature_connectivity`, both directions, live rows only). `trace.NetworkDeEnergized` then keeps only the genuinely **energised** ones — a far side that is itself dead (another outage, or the same cone round a ring) can restore nothing.
4. **Alternate source + load.** `trace.UpstreamFeaturesOfClass(liveSide, ProtectiveDeviceClasses)` finds the feeder heads upstream of the live far side — the **nearest** names the alternate source; the **farthest** is the feeder's grid source (used as the sweep's slack so backbone loading is included). Restored load is `trace.SummarizeService` over the dead cone's load leaves; the donor's present load is `trace.DownstreamLoadLeaves` of its head.
5. **Capacity verdict** (§6), then **rank**: capacity-OK before unverified before exceeds; within a tier, most customers restored, then largest load, then a stable id tiebreak.

The result is a `RestorationSuggestion` — the dead section's totals plus the ranked `plans[]` — each plan naming the tie, the alternate source, the restored figures, and the verdict.

---

## 6. ∑ The capacity verdict — nameplate, then load-flow

Two bases, in increasing authority. The field `verdict_basis` on each plan says which decided it.

**Nameplate (`verdict_basis: "nameplate"`).** A quick arithmetic fit: the nearest upstream transformer's nameplate (`rated_kva`, or `rated_mva × 1000`) minus the donor feeder's present load = its **spare headroom**. `restored_load_kva ≤ headroom` → OK, else → exceeds. When no transformer rating is found, the plan is **unverified** — the load numbers are still shown.

**Load-flow (`verdict_basis: "load_flow"`) — supersedes the nameplate when it reaches a verdict.** `applyLoadFlowVerdict` calls `powerflow.EvaluateRestorationTie(source, tie, nominalV)`, which drives the existing **contingency engine**: it sweeps the donor feeder as it stands, then **re-sweeps it with the tie hypothetically closed** (no connectivity is mutated), and reports the restored load plus any **post-transfer voltage violation** (outside ANSI C84.1 Range A, 0.95–1.05 pu by default) or **thermal overload** (branch/transformer loading > 100 %).

- **Feasible** (load restored, no violation, no overload) → **Capacity OK**.
- Any violation or overload → **Capacity exceeds**.
- An **inconclusive** sweep — the tie forms a loop/mesh rather than a radial transfer, or it energises no new load — or an **unresolvable sending-end voltage**, leaves the nameplate verdict standing (the engine's note explains).
- A genuine sweep failure **degrades** to the nameplate verdict with a `load_flow_note`, so one bad sweep never sinks the whole suggestion.

The sending-end voltage is read as `voltage_class` / `primary_voltage` off the donor head, then the tie's live-side conductor, then the tie itself (a conductor reliably carries `voltage_class`).

> ∑ **Why reuse contingency.** The N-1 / back-feed contingency study (**Engineering Studies**) already answered *"close this tie and re-sweep — what breaks?"* and ranks normally-open ties first. FLISR's capacity check is that study pointed at one chosen tie. One engine, one definition of a feasible transfer.

---

## 7. ∑ Building the order — `GenerateRestorationOrder`

`GenerateRestorationOrder(incidentID, tieClass, tieID)`:

- Re-runs the suggestion and **refuses a tie that isn't currently suggested** (so an order can't be built on a stale or bogus tie), and refuses a **restored/cancelled** incident.
- Drafts **RESTORE-phase** steps into the **existing** `oms.switching_orders` / `oms.switching_steps` tables: a **note** (*confirm the faulted section is isolated and safe*), an optional **WARNING** note when the capacity verdict is *exceeds* (citing the load-flow violations/overloads, or the kVA excess), a **close** step on the tie, and a **verify** step.
- Does **not** share the "one live order per incident" guard — a restoration order may coexist with the incident's **isolation** order (they are different programs); it guards on its own reference (`Restoration of <device> via <tie>`) instead.
- The **close** step is an ordinary switching step: executing it via the existing `SetStepExecuted` flips the tie's live `device_state` (and logs a device operation), exactly like any open/close step. No new execute machinery.

---

## 8. Endpoints & agent tools

Reads are `oms:read`, the generate is `oms:write`.

| Verb & path | What |
|---|---|
| `GET /oms/incidents/{id}/restoration` | Suggested back-feed plans for an incident's device |
| `GET /oms/restoration?device_class=&device_id=` | The same, for an ad-hoc device (no incident) |
| `POST /oms/incidents/{id}/restoration-orders` `{tie_class, tie_id}` | Build the close-tie order from a chosen plan |

MCP / agent tools mirror them: **`oms_suggest_restoration`** (by `incident_id`, or `device_class`+`device_id`) and **`oms_generate_restoration_order`** (`incident_id`, `tie_class`, `tie_id`).

---

## 9. Worked example — back-feeding Feeder 7

REC-7 locks out on a persistent fault; the section below it is dark. Priya opens the incident. The **Restoration** panel shows one plan:

- **Close `MV_SWITCH · a1b2…`** (the Feeder 7 ↔ Feeder 9 tie), **back-feed from `MV_BREAKER · f9…`** (Feeder 9's head).
- **Restores 412 customers · 38 supplies · 1 240 kVA.**
- **Capacity OK · load-flow** — *Load-flow: feasible · worst 0.961 pu.*

Priya clicks **Build switching order**. The new order reads: *confirm Feeder 7 below REC-7 is isolated → **Close MV_SWITCH a1b2…** to back-feed from MV_BREAKER f9… → verify 412 customers / 38 supplies restored via MV_BREAKER f9…*. Dana executes the close; the live cone collapses; the map shows Feeder 7's tail now green, fed from Feeder 9, while the faulted span stays isolated for the repair.

Had the sweep found an overload, the badge would read **Overloads feeder**, the card would show *2 voltage violation(s), 1 overload(s) · worst 0.912 pu*, and the generated order would carry a WARNING note — a transfer Priya could still make, with eyes open, or reject for a different tie.

---

## 10. Honest limitations

- ⚠️ **Only suggests modelled ties.** No `normal_state`/`NORMALLY_OPEN` tie on the dead section's boundary → no plan. Model the tie (§3).
- ⚠️ **The tie must border the live dead cone.** A tie reachable only past a *second* open device isn't offered (closing it wouldn't help the directly-dead section anyway).
- ⚠️ **Capacity may be unverified.** With no modelled feeder voltage *and* no transformer nameplate, the fit can't be judged — the plan still shows the load it would restore, flagged `unverified`.
- ⚠️ **Load-flow is planning-grade.** The contingency sweep is the same linearised, balanced, single-phase ladder the load-flow engine uses (see **Engineering Studies** / **Analysis Validation Report**) — validate against a balanced reference, not an unbalanced IEEE feeder.
- ⚠️ **Donor source = farthest upstream protective device.** For a simple radial donor this is the feeder breaker (correct); in an unusually meshed upstream it is a best-effort choice.
- ⚠️ **Suggesting re-runs the sweep.** Each plan runs a two-sweep contingency study; building an order re-runs the suggestion to validate the tie. Cheap for the handful of candidate ties a section has, but it is real work on every open.
- ⚠️ **No crew/ETR, no multi-step transfer.** A plan is a **single** tie close. It does not stage a multi-switch transfer, pick up only part of a section, or estimate restoration time.

---

## Related guides

- **[OMS — Outage Management](oms-guide.md)** — the incident the restoration hangs off, the live cone, and the switching-order model this reuses.
- **[Switching & Protection](switching-and-protection-guide.md)** — modelling ties/couplers, `device_state` vs `normal_state`, and how a close re-routes traces.
- **[Engineering Studies](engineering-studies.md)** — the contingency / back-feed study and the load-flow engine behind the capacity verdict.
- **[Outage Operations](outage-operations-guide.md)** — where restoration sits in the full call → fault → incident → switching → restore → reliability loop.
