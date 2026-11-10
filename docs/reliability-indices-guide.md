# Reliability & Indices

_Last updated: 2026-11-06_

**Written for:** engineers and analysts who report network reliability, and anyone who needs to know **exactly** what VoltEdge's SAIDI / SAIFI / CAIDI / ENS numbers mean — the formula as coded, the customer base behind them, and the things they deliberately do *not* do.

This is the deep-dive behind **OMS — Outage Management** §8. OMS turns outages into frozen **interruption facts**; this guide is what the `GET /oms/indices` endpoint does with them. It's written in two layers:

- **▶ Analyst** — what each index means, how to run a report, how to read it.
- **∑ Engineer** — the exact formula, denominator, window-clipping and filters as implemented in `api/internal/oms/indices.go`, and the honest list of simplifications.

**The cast.** **Tom** (reliability analyst) runs and reads the indices; **Priya** (operator) *confirms* incidents — which is what freezes the interruption facts the indices are built from; **Ada** (admin). The running example is a month's reporting that includes the Feeder 7 outage from the OMS and Outage Operations guides.

**Legend.** 💡 a tip. ⚠️ a limitation stated honestly. **▶** the analyst lane; **∑** the maths lane.

> **Grounding note.** Everything here is the shipped `api/internal/oms/indices.go` + `med.go` over `oms.customer_interruptions` (`migrations/0087`, widened by `0119`). Where the code simplifies (ENS, CAIDI population, the major-event-day reference window), this guide says so plainly — it does not describe methodology the code doesn't implement.

---

## 1. The four indices — and where the data comes from

| Index | Reads as | In one line |
|---|---|---|
| **SAIFI** | interruptions per customer | how *often* the average customer loses supply |
| **SAIDI** | customer-minutes per customer | how *long*, in total, the average customer is off per period |
| **CAIDI** | minutes per interruption | the average *duration* of one interruption |
| **ENS** | kWh | energy *not supplied* while customers were off |

**The data is frozen, not live.** Every index is computed from `oms.customer_interruptions` — one row per affected supply, written when an operator **confirms** an incident (OMS §4). That freeze is the reliability-critical moment: *an unconfirmed incident contributes nothing to the indices.* Each row carries a **frozen** `customers` count (a snapshot of the supply's `customers_served` at the time, a null counted as 1), an `interrupted_at`, and a `restored_at` (null = still out).

**One endpoint.** `GET /oms/indices?from=&to=&avg_load_kw=&circuit_id=` computes everything over a window. The window is **half-open `[from, to)`**; it defaults to **the last year** (`to` = now, `from` = one year back), and rejects `from ≥ to` with a 400.

---

## 2. The customer base — the denominator (read this first)

Every index divides by **N = total customers**, and *what N is* decides what the numbers mean. In VoltEdge:

> **∑ N is derived from the live network, not a configured constant.** There is **no "total customers" setting**. N is computed each time as the sum of `customers_served` over every **live service point** (`gis.service_point`, not deleted), with a null `customers_served` counted as 1 — plus, for a **whole-system** report, the `customers_served` on every **`PRIMARY` meter** (Phase 63), when the `meter_role` column exists.

This makes SAIDI/SAIFI **per-system-customer** — the standard utility convention ("the *average* customer"), not per-affected-customer. Two consequences:

- Add or remove service points on the map and your denominator moves — the indices are always relative to the network as currently modelled.
- ⚠️ **Per-circuit reports use a service-point-only denominator** (`?circuit_id=…`): `PRIMARY` meters are excluded from *both* the numerator-join and the denominator for a circuit report, because meters don't yet carry a `circuit_id`. A documented limitation — a circuit with metered supplies will read slightly low. Whole-system reports include them.

💡 If your SAIDI looks an order of magnitude off what you expect, check N first: it's the live service-point population, so a half-built network (few service points) inflates every index.

---

## 3. The formulas, exactly as coded

Two SQL reads feed a handful of Go divisions. The numerators come from the interruptions **clipped to the window** (§4); the denominator is **N** (§2).

```
  CustomersInterrupted = Σ customers   where the interruption BEGAN in the window
  CustomerMinutes      = Σ (customers × minutes-overlapping-the-window)   over ALL overlapping interruptions

  SAIFI = CustomersInterrupted / N            (0 if N = 0)
  SAIDI = CustomerMinutes      / N            (minutes; 0 if N = 0)
  CAIDI = CustomerMinutes / CustomersInterrupted   (0 if none interrupted)
  ENS   = (CustomerMinutes / 60) × avg_load_kw      (kWh)
```

> **∑ Two populations, one subtlety.** SAIFI's numerator counts only interruptions that **began inside** the window (a frequency should count *events*, not carry-overs). SAIDI's numerator counts the **overlapping minutes of every** interruption that touches the window, including one that **began before** `from` and runs into it. So for an outage straddling the window start, its minutes count toward SAIDI/CAIDI but its customers do *not* count toward SAIFI — a minor population mismatch to be aware of when a big outage crosses your reporting boundary. **CAIDI** is computed as `CustomerMinutes / CustomersInterrupted` directly (not as `SAIDI/SAIFI`) — algebraically the same when N is shared, but robust to the N-guard.

**Worked example — a month with the Feeder 7 outage.** Say the modelled network has **N = 12,000** customers, and in the month the only sustained unplanned outage is Feeder 7: **420** customers out for **95** minutes.

```
  CustomersInterrupted = 420
  CustomerMinutes      = 420 × 95 = 39,900

  SAIFI = 420 / 12,000      = 0.035  interruptions/customer
  SAIDI = 39,900 / 12,000   = 3.325  customer-minutes/customer
  CAIDI = 39,900 / 420      = 95     minutes            (= the single outage's duration, as expected)
  ENS   = (39,900 / 60) × 2 = 1,330  kWh                (at avg_load_kw = 2; = 665 kWh at the default 1.0)
```

With several outages in the window, each contributes its `customers` and `customer-minutes` and the totals sum — SAIFI and SAIDI rise, CAIDI becomes the customer-weighted average duration.

---

## 4. How one interruption contributes

Each `customer_interruptions` row is **clipped to the window** before it counts:

```
  eff_start = GREATEST(interrupted_at, from)
  eff_end   = LEAST(COALESCE(restored_at, to), to)        -- still-out → counts up to `to`
  minutes   = max(0, (eff_end − eff_start) in minutes)     -- only when eff_end > eff_start
```

- An outage **wholly inside** the window counts its full duration.
- One that **straddles** `from` or `to` counts only the overlapping minutes (its customers still count toward SAIFI *only if it began in-window*, §3).
- One **still out** at report time (`restored_at` null) counts up to `to` and is also surfaced separately as **`ongoing_interruptions`**, so you can see how much of the figure is still accruing.

The `customers` value is the **frozen** count from confirmation time — it does **not** re-read the network, so re-modelling an asset later never changes a historical index. (`feature_class` on the row distinguishes a `SERVICE_POINT` supply from a `PRIMARY` `METER` supply; the numerators sum both together.)

---

## 5. Scope & filters

- **Window** — `from`/`to`, half-open `[from, to)`, default last year (§1).
- **Circuit** — `?circuit_id=` narrows to one circuit's service points (with the meter caveat of §2). If the `gis.service_point.circuit_id` column is absent the request fails cleanly with a 400, not a raw DB error.
- **Unplanned only** — the indices count `outage_type = 'unplanned'` incidents. ⚠️ **Planned outages are excluded** (IEEE 1366 convention) — they're still recorded as interruptions, just never summed into the indices.
- **Cancelled excluded** — a `cancelled` incident contributes nothing.

---

## 6. ENS — an honest placeholder

**ENS (energy not supplied)** is `(CustomerMinutes / 60) × avg_load_kw` — interrupted customer-*hours* times an assumed average demand per customer.

> ⚠️ **ENS is a transparent estimate, always.** `avg_load_kw` is a **single flat per-customer figure** (query param, default `1.0`), *not* a per-customer or measured-load model. The response always carries **`ens_estimated = true`**. At the default 1.0, ENS is simply "interrupted customer-hours". Pass a realistic `avg_load_kw` (e.g. your system's average demand per customer) for a usable kWh figure, and treat it as an estimate. A negative value is rejected.

---

## 7. The by-fault-cause breakdown

The response can decompose the window's interruptions by **what caused them**, read from the **faults** that explain the incidents (the one place OMS touches the faults module):

- It picks **one representative fault per incident** (the earliest-detected), so a multi-fault incident isn't double-counted, and buckets each interruption into: a **specific cause** (the fault's `cause`), an **uncategorised fault** (a fault with no cause recorded), or **no fault linked** (an incident with no fault at all).
- Each bucket carries its own `customers_interrupted`, `customer_minutes`, and per-bucket **SAIFI/SAIDI over the same N** — so the buckets **sum back** to the system totals (a built-in consistency check).
- ⚠️ It's **fail-soft**: if the faults module isn't present, `fault_breakdown_available` comes back `false` and the breakdown is simply omitted — the headline indices are unaffected. (Grounded: an existence check on `faults.fault_events`, not a hard dependency.)

💡 This is how you answer "how much of our SAIDI was dig-ins vs. vegetation vs. equipment failure?" — but only to the fidelity of the **free-text `cause`** operators record on faults. Garbage causes in, garbage breakdown out.

---

## 8. Major-event-day normalisation (IEEE 1366 2.5β)

Raw SAIDI/SAIFI lump a catastrophic day (a major storm) in with ordinary operations, so a single event can dominate a year. **IEEE 1366** defines a statistical threshold above which a day is a *major event day* (MED) and is reported separately, leaving a **normalised** index that reflects day-to-day reliability — the figure most regulators compare year-on-year. VoltEdge computes this (`api/internal/oms/med.go`) and returns it alongside the raw indices:

- `med_threshold_saidi` — **T_MED**, the daily-SAIDI threshold. Over the window's daily SAIDI values (only days with SAIDI > 0): `α = mean(ln SAIDI_d)`, `β = sample std-dev(ln SAIDI_d)`, `T_MED = exp(α + 2.5β)`.
- `major_event_days` — how many days exceeded T_MED.
- `saidi_normalized` / `saifi_normalized` — the indices with those major event days **excluded**.
- `med_applicable` — `false` when the window holds too few outage days to define the threshold (fewer than two positive days); the normalised figures then **equal the raw** ones (nothing excluded), and `med_note` says so.

The daily rows use the **same** clip, circuit-scope and unplanned/non-cancelled filters as the headline aggregate, so they sum back to it — only the per-day split is new.

> ⚠️ **Two documented simplifications** (`med.go` HONEST LIMITS). (1) The strict standard uses a **five-year** reference period; VoltEdge computes T_MED from the daily SAIDI values **in the report window**, so a short window gives a less stable threshold (hence `med_applicable`). (2) A multi-day outage's customer-minutes are attributed to its **start day** rather than split across each calendar day it spans — so MED classification and exclusion operate on whole days keyed by outage start. Both are conservative for storm detection and keep the daily rows summing to the headline, but they are deviations from a strict per-calendar-day clip over a five-year reference. Quote `saidi_normalized` with that caveat.

---

## 9. MAIFI — momentary interruptions

SAIDI/SAIFI are about **sustained** outages — the ones an operator confirms into incidents. But a feeder that trips and recloses in four seconds never becomes an incident, so it leaves **no trace in `customer_interruptions`** at all. **MAIFI** (momentary average interruption frequency index) measures exactly those blinks, and VoltEdge computes it (`api/internal/oms/maifi.go`) from a **different source entirely**.

> **∑ Grounding: a different fact table.** MAIFI is read from the **device-operation log** (`ops.operations`, `migrations/0147`), *not* `customer_interruptions`. Momentaries never become incidents, so there is nothing to read on the SAIDI side — the operation log (every recorded trip / reclose / open / close on a device) is the only place a blink is recorded.

**What counts as one momentary.** A **momentary interruption event** is an interrupting device operation (a **trip/open**) that **returns to closed** (a **reclose/close**) within the sustained threshold, **with no lockout**. A rapid re-strike sequence — trip, reclose, trip, reclose — that all resolves within the threshold **collapses to one event**, not three, so a recloser's normal two- or three-shot sequence reads as a single customer-visible blink. If the device never comes back within the threshold (it locks out), it isn't a momentary — that's a sustained outage, and the SAIDI side owns it.

**The formula.**

```
  MAIFI   = Σ (momentary events × customers downstream of the device) / N
  MAIFI_E = Σ (each interrupting operation × customers downstream)     / N
```

- **MAIFI** groups a re-strike sequence into one event (the customer-experience figure).
- **MAIFI_E** (returned as `maifi_e`) counts **each interrupting operation** rather than grouping — the event-level figure for engineers watching device wear.
- **N** is the same system denominator as SAIDI/SAIFI (§2).
- The **downstream customer count reuses the same `AffectedCustomers` trace** OMS uses to populate the affected set for a real incident — so "customers below this recloser" means the same thing it does everywhere else in OMS.

**`momentary_mode` — don't let manual switching inflate it.** A query param with two settings:

- **`protective`** (**DEFAULT**) — a momentary only counts when the session includes a **trip/reclose auto-protection** operation. Routine **manual switching** (an operator opening and reclosing a switch for work) is *not* a protection event, so it doesn't inflate MAIFI.
- **`pure_duration`** — **any** interrupt → restore within the threshold counts, protection or not. Use this only when you deliberately want every open/close blink, manual included.

**`sustained_threshold_minutes` — the momentary/sustained cutoff.** Default **5 minutes** (the IEEE 1366 convention). An interrupt that restores inside this window is a momentary; one that takes longer is a sustained outage. Set it with the `sustained_threshold_min` query param.

> **▶ Run MAIFI** 🔒 `oms:read`
> It rides on the same `GET /oms/indices` call — pass `momentary_mode=protective` (or `pure_duration`) and `sustained_threshold_min=5`. Read `maifi` and `maifi_e` alongside the sustained indices.

**Honest limits — state these plainly:**

- ⚠️ **The customer cone is traced over *current* topology, not frozen at operation time.** Unlike a confirmed incident's frozen `customers` count, MAIFI re-walks "who's downstream" from the device as the network is modelled **now** — so re-modelling the feeder later moves a historical MAIFI figure. It is an estimate against today's graph, not a frozen fact.
- ⚠️ **MAIFI is only as complete as the operation log's feeds.** Today that's **crew entry**; SCADA / relay auto-population comes later. A window with no logged operations yields an empty MAIFI, and the response says so plainly via **`maifi_note`** rather than reporting a misleading zero.
- ⚠️ **Per-circuit MAIFI is not supported.** Reclosers carry no circuit membership, so a **circuit-scoped** report (`?circuit_id=…`) sets **`maifi_applicable = false`** and returns no MAIFI — unlike CEMI/CELID (§10) and the sustained indices, which *do* scope to a circuit.
- ⚠️ **MAIFI is not MED-normalised.** The major-event-day machinery (§8) applies to SAIDI/SAIFI only; a storm's blinks are *not* carved out of MAIFI.

---

## 10. CEMI / CELID — worst-served customers

SAIDI and SAIFI are **system averages**, and an average hides the customer who has been off **nine times this year** while everyone around them barely noticed. **CEMI** and **CELID** are the "who is actually suffering" indices — the ones a regulator asks about when a specific customer complains. VoltEdge computes both (`api/internal/oms/worstserved.go`) over the **same `oms.customer_interruptions` fact table, window, unplanned filter and denominator N** as SAIDI/SAIFI — grouped by **service point** instead of summed flat.

- **CEMI_n** (customers experiencing multiple interruptions) = (customers on service points with **more than `cemi_threshold`** sustained interruptions) / N. Default threshold **n = 4** (i.e. the fraction of the customer base that saw a 5th-or-more interruption in the window). Set it with the `cemi_threshold` query param.
- **CELID** (customers experiencing long interruption durations) = (customers whose **total** interruption time over the window **exceeds `celid_hours`**) / N. Default **24 hours**. Set it with the `celid_hours` query param.
- **`worst_served`** — the ranked **top-25 service points**, ordered by **most interruptions, then most total minutes**. Each entry carries `feature_class`, `feature_id`, `customers`, `interruptions`, `total_minutes` and `max_minutes`. This is the named list the system averages hide — the actual worst-served supplies, ready to hand to a planner.

💡 **Per-circuit scope works here** (unlike MAIFI, §9): because CEMI/CELID read the **scoped `customer_interruptions` fact table**, a `?circuit_id=` report simply narrows the facts and the denominator the same way SAIDI does.

> **▶ Run CEMI/CELID** 🔒 `oms:read`
> Same `GET /oms/indices` call — pass `cemi_threshold=4` and `celid_hours=24` (or your regulator's values). Read `cemi`, `celid` and the `worst_served` list.

---

## 11. What VoltEdge does *not* do (the honest limits)

Reliability reporting is a regulated area, so be precise about the boundaries:

- **IEEE 1366 major-event-day exclusion — now implemented** (§8), with two documented simplifications: the threshold uses the **report window** rather than a strict five-year reference, and multi-day outages are attributed to their **start day**. The raw indices are still reported; the normalised ones sit beside them.
- **MAIFI (momentary interruptions) — now implemented** (§9), computed from the device-operation log rather than the sustained interruption facts. See the new section for its own honest limits.
- **CAIDI population quirk** — its numerator (minutes, includes carried-over) and divisor (customers, began-in-window) use slightly different populations for a window-straddling outage (§3).
- **ENS is a flat-kW placeholder** (§6), always flagged estimated.
- **Per-circuit excludes `PRIMARY` meters** on both sides (§2).
- **Planned and cancelled** excluded (§5).

None of these are bugs — they're documented simplifications. The guide's job is to make sure you never quote a number as something it isn't.

---

## 12. How to run a report

> **▶ Run the indices** 🔒 `oms:read`
> `GET /oms/indices?from=2026-09-01T00:00:00Z&to=2026-10-01T00:00:00Z&avg_load_kw=2&circuit_id=…`
> - omit `from`/`to` for the **last year**; omit `circuit_id` for the **whole system** (which includes `PRIMARY` meters);
> - set `avg_load_kw` to your per-customer average for a meaningful ENS.
>
> Read the result: `saifi`, `saidi` (minutes), `caidi` (minutes), `ens_kwh` (with `ens_estimated: true`), the raw `total_customers` / `customers_interrupted` / `customer_minutes` / `ongoing_interruptions` behind them, and — when available — the `by_fault_cause` array.

---

## Appendix — reference

### Response fields (`GET /oms/indices`)

| Field | Meaning |
|---|---|
| `from`, `to` | the window actually used |
| `circuit_id` | present only when scoped to a circuit |
| `total_customers` | **N** — the denominator (live service points + `PRIMARY` meters) |
| `customers_interrupted` | SAIFI numerator (began-in-window) |
| `interruption_events` | count of interruptions that began in-window |
| `customer_minutes` | SAIDI numerator (overlapping minutes, all touching interruptions) |
| `ongoing_interruptions` | still out at `to` |
| `saifi`, `saidi`, `caidi`, `ens_kwh` | the four indices |
| `ens_estimated` | **always `true`** (§6) |
| `avg_load_kw` | the per-customer figure used for ENS |
| `by_fault_cause[]` | per-cause `{cause, fault_linked, customers_interrupted, interruption_events, customer_minutes, saifi, saidi}` |
| `fault_breakdown_available` | `false` when the faults module isn't present |
| `maifi` | momentary index — re-strike sequences grouped to one event (§9) |
| `maifi_e` | event-level momentary index — each interrupting operation counted |
| `momentary_events` | count of momentary interruption events in the window |
| `momentary_customer_interruptions` | Σ (momentary events × downstream customers), the MAIFI numerator |
| `momentary_devices` | count of devices that produced a momentary |
| `sustained_threshold_minutes` | the momentary/sustained cutoff used (default 5) |
| `momentary_mode` | `protective` (default) or `pure_duration` (§9) |
| `maifi_applicable` | `false` for a circuit-scoped report (reclosers carry no circuit) |
| `maifi_note` | why MAIFI is empty / not applicable (e.g. no logged operations) |
| `cemi_threshold` | the interruption-count cutoff n used for CEMI (default 4) |
| `cemi` | fraction of customers with more than `cemi_threshold` interruptions (§10) |
| `cemi_customers` | customer count behind `cemi` |
| `celid_hours` | the total-duration cutoff used for CELID (default 24) |
| `celid` | fraction of customers whose total interruption time exceeds `celid_hours` |
| `celid_customers` | customer count behind `celid` |
| `worst_served[]` | ranked top-25 service points `{feature_class, feature_id, customers, interruptions, total_minutes, max_minutes}` |

### Formula sheet

```
  N      = Σ service_point.customers_served (null→1)  [+ Σ PRIMARY meter.customers_served, whole-system]
  SAIFI  = customers_interrupted / N
  SAIDI  = customer_minutes / N                     (minutes)
  CAIDI  = customer_minutes / customers_interrupted  (minutes)
  ENS    = (customer_minutes / 60) × avg_load_kw     (kWh, always estimated)
  window = [from, to) ; unplanned, non-cancelled only ; clip each interruption to the window
```

### Related guides

- **OMS — Outage Management** — where interruptions come from: incidents, the confirm-freeze, `customer_interruptions`.
- **Outage Operations** — the end-to-end loop these indices sit at the end of.
- **Faults** — the `cause` behind the by-fault-cause breakdown.

