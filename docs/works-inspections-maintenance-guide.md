# VoltEdge Works, Inspections & Maintenance Guide

_Last updated: 2026-10-28_

*A practical, task-oriented manual for the people who find what's wrong with the network, plan the work that fixes it, and keep it healthy on a cycle — using VoltEdge's Work & Asset Management (WAM) module.*

---

## About this manual

This manual is the how-to companion for the **Works** area of VoltEdge — everything reached from the **Works** page in the sidebar and from the **Works tab** on any asset's panel on the map. It covers three tightly-linked jobs:

- **Works** — logging **defects** (what is wrong with an asset) and running **work orders** (the jobs that put it right) from first report through to completion.
- **Inspections** — building reusable **checklists** and recording them against assets, so routine condition-checks are consistent, evidenced, and automatically turn findings into tracked defects.
- **Maintenance** — using the above on a cycle: preventive work and inspection rounds that keep assets healthy rather than only reacting to failures.

It is deliberately **how-to first**. Where an action needs a permission you may or may not hold, this manual says so plainly (for example, "this needs `wam:write`") rather than diving into internals. If you administer VoltEdge — configure roles, compile classes, run resets — the **[Admin & Break-Glass Guide](admin-break-glass-guide.md)** is your reference, and the broader **[User Handbook](user-handbook.md)** covers the map, tracing, and the rest of the product this module sits inside.

A note on scope: this manual documents **what has been built**. WAM is delivered in slices, and this guide is honest about the edges — most importantly, that **recurring, auto-generated maintenance plans are not built yet** (see Chapter 6). Where a capability is planned but absent, the manual says so rather than implying a button that isn't there.

### The cast

To keep the worked examples concrete, this manual reuses the recurring characters from the User Handbook and adds one field persona. Each holds a real set of permissions, and the examples respect them exactly:

- **Priya — viewer.** Read-only. Priya holds `wam:read`, so she can *open* the Works tab on an asset and read its defects, work orders, and inspection history — but she sees **no action buttons**. She is the manual's reminder of where the write boundary sits.
- **Dana — editor.** Our office-side **maintenance planner**. Dana holds `features:write` and, through the editor role, `wam:write`. She triages defects, authors inspection templates, and raises, schedules, and assigns work orders. Most planning examples are Dana's.
- **Femi — field technician.** Holds a **field role** that grants `wam:write` (the field roles seeded for offline crews). Femi executes work orders and records inspections, often on a phone with no signal, then syncs. Most execution examples are Femi's.
- **Tom — analyst.** Appears occasionally for context. Tom can trace and analyse the network but is read-only on assets; in WAM he behaves like Priya (sees the records, can't change them) unless separately granted `wam:write`.

> **Legend used throughout:** 🔒 marks an action that requires a permission (named inline). 🗺️ marks something that also depends on your **territory access**. 💡 is a tip. ⚠️ is a caution.

### Chapter roadmap

1. **[The WAM mental model](#chapter-1--the-wam-mental-model)** ← *this chapter* — the three record types, the closed loop they form, where they live, and who can touch them.
2. **Defects — recording what's wrong** — logging, severity & source, the triage lifecycle, resolving and dismissing.
3. **Work orders — the one typed engine** — the four work types, the shared lifecycle, priority, assignment, scheduling, and completing work.
4. **Inspection templates — reusable checklists** — building a checklist, item types, the fail-severity auto-defect arm, retiring and restoring.
5. **Recording inspections — executing a checklist** — running an inspection, fulfilling a work order, the auto-defect loop, reviewing results.
6. **Maintenance — running it on a cycle** — preventive work and inspection rounds, an honest look at what's automated and what isn't, an end-to-end regime.
7. **In the field (offline)** — taking Works into a field pack, working with no signal, and syncing back safely.
8. **Permissions, territory, and the agent (MCP)** — the full permission picture and doing all of this through the API / assistant.
9. **Appendix** — status, severity, and type reference tables, plus a one-page cheat sheet.

---

## Chapter 1 — The WAM mental model

This chapter builds the vocabulary the rest of the manual assumes. Read it once. If you take only three ideas away, take these: WAM records are **about** assets but are **not** map features; there are exactly **three record types**, and they form **one closed loop**; and everything you can do is gated by **two permissions** — `wam:read` to see, `wam:write` to act.

### 1.1 What WAM is — and what it is not

VoltEdge's core is a **map of the network**: every pole, transformer, switch, cable, and service is a *feature* with a geometry and electrical intelligence. Work & Asset Management (WAM) sits alongside that map and records the **operational life of those assets** — the problems found on them, the jobs done to them, and the routine checks run against them.

The distinction matters, so it's worth stating flatly:

- A **transformer** is a GIS feature. It has a location, terminals, a rated kVA, connections upstream and downstream. It lives in the network model and shows up in traces and analyses.
- A **defect on that transformer** ("oil weeping at the LV bushing") is **not** a feature. It is an operational *record about* the transformer. It has no independent place in the electrical model — it points at the transformer by reference and describes something true of it at a moment in time.

Because of that, WAM keeps its own store, separate from the compiled feature classes. You never "draw" a defect or a work order on the map; you **log** it against an asset. What you *do* get on the map is a **Works tab** on that asset's panel, and an optional pin for records that carry a location. The point of the separation: an asset's identity (what it is, where it is, what it connects to) stays clean and stable, while the messy, time-varying story of its health and the work done to it accumulates beside it.

> 💡 Rule of thumb: if it's a thing that exists in the world and connects to other things, it's a **feature**. If it's something that *happened to* or *needs to happen to* a feature, it's a **WAM record**.

### 1.2 The three record types

Everything in WAM is one of exactly three kinds of record. Learn these three and the whole module falls into place.

1. **Defect — "what is wrong."**
   A single health finding on one asset. A defect names the affected asset, carries a **severity** (how bad), records a **source** (how it was found), and walks a short **status** lifecycle from open to resolved or dismissed. A cracked insulator, a corroded earth strap, oil weeping from a bushing, a reading drifting out of range — each is a defect. Defects are the module's "inbox": the running list of things the network needs attention for. *(Full detail in Chapter 2.)*

2. **Work order — "the job that gets done."**
   One tracked unit of work. VoltEdge uses **one typed work-order engine**: an inspection round, a corrective repair, a preventive service, and a construction job are all *work orders*, distinguished by a **work type** but sharing a single lifecycle (draft → scheduled → assigned → in progress → completed, or cancelled). A work order carries a **priority**, can name a **primary asset**, can be **assigned** to a person or crew, and can be **scheduled** with a target date. *(Full detail in Chapter 3.)*

3. **Inspection — "the routine check, and its record."**
   Two halves. An **inspection template** is a reusable **checklist** — a named, ordered list of questions ("Any oil leaks at the bushings?", "Silica-gel colour?", "Earth resistance reading?"), optionally scoped to a class of asset. An **inspection** is one *filled-out* copy of that checklist against a specific asset at a point in time. The clever part is the join to defects: a checklist item can be armed so that **answering it "fail" automatically logs a defect** at a chosen severity. *(Templates in Chapter 4, recording in Chapter 5.)*

### 1.3 The closed loop

The three record types are not three separate lists — they're one cycle, and understanding the cycle is the whole game. Findings become tracked problems; problems become planned work; completed work (and fresh inspections) reveal the next findings.

```
                 ┌─────────────────────────────────────────────┐
                 │                                             │
                 ▼                                             │
   [ Inspection ] ──fail item──▶ [ Defect ] ──raise──▶ [ Work order ]
        ▲            (auto)          ▲                       │
        │                           │                       │
     (on a cycle)          logged by hand,           completed by a
                           from OMS, from the         crew; asset is
                           map, or from a scan        healthy again
                                                           │
                                                           ▼
                                                   next inspection…
```

Read it as a story:

- A **defect** can enter the loop three ways: someone **logs it by hand** (Dana spots corrosion; Femi reports a knock on a pole); an **OMS outage** throws one up (a fault on the network implicates an asset); or an **inspection** raises it **automatically** when a checklist item fails.
- An open defect is **triaged** and, when it needs a physical fix, a **corrective work order is raised from it** — one click that both creates the job and marks the defect as "work ordered," so nothing is worked twice or lost.
- The work order is **assigned, scheduled, and executed**. When the crew **completes** it (with notes), the defect it addressed can be **resolved**. The asset is healthy again.
- Meanwhile, **preventive work orders** and **inspection rounds** run on a cycle, catching the next generation of defects before they become outages — and the loop turns again.

> 💡 The single most important habit this manual teaches: **don't resolve a defect by fixing it quietly.** Raise a work order from it, do the work against that order, complete the order, then resolve the defect. That leaves a full, linked trail — finding → job → done — which is exactly what an audit, a reliability report, or the next engineer needs.

### 1.4 Where WAM lives — two front doors

There are two places to work with WAM, and they show the same underlying records from two angles. Knowing which to reach for is half of being efficient.

1. **The Works page (the board).** In the sidebar, **Works** opens the module's home. This is the **cross-asset** view — every defect and every work order across the whole network you can see, plus the inspection templates and the inspection history. It's where you go to *plan and triage*: "what's open across my territory this morning?", "which work orders are unassigned?", "how many defects are still awaiting a job?"

   The board is laid out for at-a-glance reading:
   - A **KPI strip** across the top — **Open defects**, **Active orders**, **In progress**, and **Unassigned** — so the state of the world is one glance, not a scroll.
   - A **defect → order flow board**: incoming **Defects** on the left (the inbox), **Work orders** in flight on the right. The left-to-right layout mirrors the loop in §1.3 — findings flow into jobs.
   - Below the board, **Inspection templates** and **Inspections** — the checklist library and the recorded history.
   - Each row collapses its actions into a compact **⋯ menu**, so a defect row offers *Raise work order / Resolve / Dismiss* and a work-order row offers *Start / Complete / Cancel* without crowding the list.

2. **The Works tab (on an asset).** Click any asset on the map and its panel opens; if you hold `wam:read`, one of the tabs is **Works**. This is the **single-asset** view — *only* the defects, work orders, and inspections for **this** transformer / pole / switch. It's where you go when you're already looking at a specific asset: "does this switch have any open defects?", "log a defect on *this* pole", "raise a job against *this* transformer." Logging from here is quicker than from the board because the asset is already filled in for you.

   The Works tab applies to **any** asset, not just electrically-connected ones — a pole, a chamber, or a building can all carry defects and work orders, so the tab appears on every feature's panel (unlike the Terminals tab, which only appears on things wired into the network).

> 💡 Board vs tab, in one line: use the **board** to see the whole picture and plan; use the **tab** to act on the asset in front of you. They read and write the same records — a defect Femi logs from a pole's Works tab appears instantly in Dana's board inbox.

### 1.5 Who can do what — the two-permission model

WAM's entire access story is **two permissions**, applied together as a single method-aware gate on the module. There is no finer split — no separate "manage templates" or "close work orders" permission — so it pays to know exactly what the two cover.

| Permission | Grants | Held by (seeded roles) |
|---|---|---|
| **`wam:read`** | See the Works board and the Works tab; read every defect, work order, template, and inspection. | viewer, analyst, editor, admin, break-glass-admin |
| **`wam:write`** | Everything write: log/triage/resolve defects; create, assign, schedule, complete, cancel work orders; author, retire, and restore inspection templates; record inspections. | editor, admin, break-glass-admin, **field roles** (field crews) |

A few consequences worth internalising:

- **Read is broad; write is narrower.** Anyone who can see the map (viewer and up) can see the Works records. Only editors, admins, and field crews can change anything. So **Priya (viewer)** can open a transformer's Works tab and read its whole history, but she sees no buttons — no *Log defect*, no *Raise work order*. **Tom (analyst)** is the same in WAM unless separately granted `wam:write`.
- **Write is flat.** Because there's no sub-split, the same `wam:write` that lets **Femi** close a work order also lets him **author an inspection template** the whole organisation then uses. That's convenient for small teams and worth flagging for larger ones — if you need template authoring restricted to planners while field crews keep defect/work-order write, that's a permission change an administrator would have to make deliberately; it isn't the default. *(This is the one place WAM's simplicity has a sharp edge; Chapter 8 revisits it.)*
- **Territory still applies. 🗺️** WAM records are *about* assets, and you only see the assets in your **territory access**. So even with `wam:read`, the board shows you defects and work orders on the network you're granted — not the whole utility. A field crew scoped to the North district plans and works the North district.
- **The tab hides itself, cleanly.** If you lack `wam:read`, the Works tab simply doesn't appear on the asset panel — you're never shown a tab whose every query would fail. It's absence, not an error.

### 1.6 A day in the loop — the whole module in one story

To close the mental model, here is the complete loop running once, with the cast in their real roles and permissions. Every step is something the following chapters teach in detail; this is the map of the territory before we walk it.

> *Monday, 08:10 — the inspection.* **Femi** (field technician, `wam:write`, offline in a rural feeder) opens the **MV switchgear annual** inspection checklist Dana authored, and records it against switch **SW-Feeder7-03**. Nine items pass. Item 6 — *"Any oil weeping at the tank seal?"* — he marks **Fail**. Because Dana armed that item with a **high** fail-severity, VoltEdge, the moment Femi's phone syncs, **auto-logs a defect**: *"Inspection: Any oil weeping at the tank seal?"*, severity **high**, source **inspection**, on SW-Feeder7-03 — linked back to the exact checklist item. Femi did not have to remember to raise it; the checklist did.
>
> *Monday, 09:30 — triage.* **Dana** (editor, planner) opens the **Works** board. Her **Open defects** KPI ticked up overnight; the new high-severity defect is at the top of her inbox. She reads it, agrees it needs a physical fix, and from the defect's **⋯ menu** chooses **Raise work order**. VoltEdge creates a **corrective** work order against SW-Feeder7-03, linked to the defect, and flips the defect's status to **work ordered** — so it drops out of her "needs a job" pile automatically.
>
> *Monday, 09:35 — plan.* Dana sets the new order's **priority** to **high**, **schedules** it for Thursday, and **assigns** it to Femi. It moves into the right-hand *Work orders in flight* column; her **Unassigned** KPI does not tick up, because she assigned it.
>
> *Thursday, 11:00 — execute.* Femi, on site, opens the order (from the board or from the switch's **Works tab**), taps **Start** (it moves to *in progress*, Dana's **In progress** KPI ticks up), reseals the tank, and taps **Complete**, adding a completion note. The order is now **completed**.
>
> *Thursday, 11:05 — close the loop.* Back on the defect, Dana (or Femi) marks it **Resolved**. The trail is now complete and linked: *inspection item failed → defect raised → corrective order → work done → defect resolved.* SW-Feeder7-03 is healthy, and every step is on the record for the next reliability report — and for whoever inspects it next year.
>
> Throughout, **Priya** (viewer) could open SW-Feeder7-03's Works tab and read this entire story unfold, without a single button to change it. That's the two-permission model doing its job.

That is WAM end to end. The rest of this manual is the detail of each step: how to log a defect well (Chapter 2), how to drive a work order through its lifecycle (Chapter 3), how to build a checklist that arms the right auto-defects (Chapter 4), how to record an inspection and fulfil a work order in one action (Chapter 5), how to run all of this as a maintenance regime (Chapter 6), how to do it offline (Chapter 7), and how permissions and the agent interface shape it (Chapter 8).

---

## Chapter 2 — Defects: recording what's wrong

A defect is the module's atom. Everything downstream — work orders, reliability figures, the maintenance backlog — is only as good as the defects feeding it. This chapter is about logging them well: getting them *born* the right way, describing them so the next person can act, triaging them without losing anything, and turning them into work. 🔒 Every write here needs `wam:write`; readers (`wam:read`) can follow along but see no buttons.

### 2.1 What a defect records

A defect is **one health finding on one asset at one moment**. Concretely it carries:

- **An anchor** — *what* it is about. Almost always this is an **asset**: a feature class code plus that feature's id (for example, `DIST_XFMR` + the transformer's id). A defect can instead be an **orphan** pinned only by a **map location** — more on that in §2.2. What it can never be is *both* half-set (an asset class with no id) or *neither* (no asset and no location); VoltEdge rejects those, because a finding that points at nothing is useless.
- **A title** — the required, human one-liner: *"Oil weeping at LV bushing."* This is what shows in every list, so make it specific.
- **A severity** — `low` / `medium` / `high` / `critical`. Drives triage order and, when you raise work from it, the resulting job's priority (§2.9). Defaults to **medium** if you don't choose.
- **A source** — how it was found: `inspection` / `outage` / `condition_monitoring` / `ad_hoc`. Defaults to **ad_hoc**. This is the provenance that later lets you ask "how many of our defects came from inspections versus outages?"
- **A status** — where it is in its life: `open` → `work_ordered` → `resolved` / `dismissed`. Starts at **open**. (§2.6.)
- **Optional colour** — a free-text **defect type** (your own classification, e.g. "corrosion"), a **description** (the detail behind the title), **notes**, **reported by**, and **reported at** (defaults to now). An outage-sourced defect also carries the **OMS incident id** it explains.

> 💡 The title is the single most-read field in the whole module. *"Corrosion"* is a bad title; *"Heavy corrosion on the HV earth strap, ~30% section loss"* is a good one. Put the actionable specifics in the title, the evidence in the description.

### 2.2 The four ways a defect is born

Defects enter the loop from four doors. Knowing which to use is mostly about *where you already are* when you notice the problem.

1. **By hand, from the Works board.** On the **Works** page, **Log defect** opens a dialog. You pick the asset (class + id), set severity/source/title, and save. Use this when you're planning at your desk and logging something reported to you second-hand. This is Dana's usual door.

2. **By hand, from an asset's Works tab (on the map).** Click the asset, open its **Works** tab, and **Log defect** there. The asset is **already filled in** — you're logging against the very feature you're looking at, so there's no id to copy. Use this whenever you're already on the map with the asset in front of you. Faster and less error-prone than the board for a single known asset.
   - This same tab is where a defect can be **orphaned to a location** rather than an asset: if the problem isn't tied to a modelled feature (a hazard on a road verge, a fly-tipped pile blocking access to a chamber), the defect can be pinned by its **map coordinates** alone. An orphan defect still triages and still raises work — the location becomes the crew's only anchor, so the work order inherits that pin.

3. **From an OMS outage.** When you're working an outage in the Outage console and an asset is implicated in the fault, a defect can be raised with **source = `outage`** and a link back to the **OMS incident**. This is how the operational event ("Feeder 7 tripped") leaves behind a durable asset-health record ("the pole-top fuse on TX-014 failed") once the lights are back on. *(The OMS console itself is covered in the User Handbook; here the point is simply that outage-sourced defects land in the same inbox as every other.)*

4. **Automatically, from an inspection.** This is the door that makes inspections worth doing. When a checklist item is armed with a **fail severity** and the inspector answers it **Fail**, VoltEdge logs a defect for you — **source = `inspection`**, severity as armed, titled from the item's prompt (*"Inspection: Any oil weeping at the tank seal?"*), on the inspected asset, linked back to the exact checklist item. Nobody has to remember to raise it. *(The full mechanism is Chapter 5; it's named here so the "four doors" picture is complete.)*

> ⚠️ However a defect is born, it lands in **one** inbox — the Works board's Defects column and the asset's Works tab. There is no separate "inspection defects" or "outage defects" list to check. Source is a *label on* the defect, not a *place it lives*.

### 2.3 Choosing severity well

Severity is the field people set carelessly and regret later, because it drives both triage order and the priority of any work raised from the defect (§2.9). Use this as a rule of thumb:

| Severity | Use it when… | Becomes work-order priority |
|---|---|---|
| **critical** | Imminent safety or supply risk — act now. Exposed live parts, a leaning pole over a road, a device one event from failing a customer. | **urgent** |
| **high** | Real degradation that will bite soon if left — oil weeping, ~30% section loss on an earth strap, a cracked but holding insulator. | **high** |
| **medium** | A genuine defect with headroom — surface corrosion, a loose but present cover, a reading drifting but in range. *(This is the default.)* | **medium** |
| **low** | Cosmetic or watch-list — faded labelling, minor rust bloom, "note for next visit." | **low** |

> 💡 If you find yourself wanting to mark everything **high** "so it gets done," you've moved the problem to the work-order queue instead of solving it. Severity is triage signal, not a plea — keep it honest so the KPI strip's **Open defects** count means something.

### 2.4 The status lifecycle

A defect walks a short, deliberate path. There are four states and a small set of legal moves.

```
        (born)
          │
          ▼
       ┌──────┐   raise work order    ┌──────────────┐
       │ open │ ────────────────────▶ │ work_ordered │
       └──────┘                       └──────────────┘
          │  │                              │
   resolve│  │dismiss                resolve│
          ▼  ▼                              ▼
     ┌──────────┐                     ┌──────────┐
     │ resolved │                     │ resolved │   (or dismissed)
     └──────────┘                     └──────────┘
          ▲                                 ▲
          └─────────── reopen ──────────────┘
                 (clears the resolved stamp)
```

- **open** — freshly logged, awaiting a decision. This is the triage queue.
- **work_ordered** — a corrective work order has been raised for it (§2.9). The defect steps out of your "needs a job" pile automatically, so you don't re-raise it.
- **resolved** — the problem is genuinely fixed (ideally after the work order that fixed it was completed). Moving here **stamps a resolved timestamp**.
- **dismissed** — a terminal "no action needed": a duplicate, a false alarm, a finding overtaken by events. Also stamped as resolved-time internally, but semantically distinct from *fixed*.

Two mechanics worth knowing:

- **Terminal states are timestamped, and reopening clears it.** VoltEdge keeps the resolved-timestamp consistent with the status: it's set when you move to resolved or dismissed, and **cleared if you reopen** the defect back to open. So "when was this resolved?" always has a truthful answer, and a mistaken resolve is fully reversible.
- **You can't raise work for a closed defect.** Once a defect is resolved or dismissed, VoltEdge refuses to raise a work order from it — a closed finding shouldn't sprout new jobs. If work really is needed, reopen it first (or log a fresh defect).

### 2.5 Logging a defect, step by step

*The map way (Femi, on site).* Femi is at a pole and sees a corroded stay wire.

1. He taps the pole on the map; its panel opens.
2. He opens the **Works** tab. (He holds `wam:write` through his field role, so the **Log defect** button is there. Priya, looking at the same pole, would see the tab and the existing records but no button.)
3. **Log defect.** The pole is already the asset. He types the title — *"Stay wire heavily corroded, near failure"* — sets **severity = high**, leaves **source = ad_hoc** (he found it eyes-on, not via a checklist), adds a note, and saves.
4. The defect is now **open** in Dana's board inbox and on this pole's Works tab. If Femi is offline, it's queued and will appear the moment he syncs (Chapter 7) — with the same identity, never duplicated.

*The board way (Dana, at her desk).* A member of the public reported a leaning pole by phone.

1. Dana opens **Works** → **Log defect**.
2. She picks the asset — the pole's class and id (she finds the pole on the map first if she needs the id, or logs it from the pole's Works tab instead, which is usually easier).
3. Title *"Pole leaning ~15° toward carriageway"*, **severity = critical** (over a road), **source = ad_hoc**, save.
4. It lands at the top of triage as an **open, critical** defect — and the board's **Open defects** KPI ticks up.

### 2.6 Triaging the inbox

Triage is the daily discipline of turning a pile of open defects into decisions. The board is built for it.

1. **Read the KPI strip.** **Open defects** is your workload signal. If it's climbing week over week, findings are outpacing fixes — a maintenance-planning conversation, not just a busier morning.
2. **Work the Defects column top-down.** It's newest-first, and severity tells you what to jump on. For each open defect, you make exactly one of three decisions:
   - **It needs a physical fix → raise a work order** (§2.9). Most defects.
   - **It's already fixed / not real → resolve or dismiss** (§2.7). 
   - **It needs watching, not acting → leave it open** with a note. Fine, deliberately.
3. **Filter when the pile is big.** The underlying list supports narrowing by **status**, **severity**, **asset**, and an **open-only** view — so "show me only the critical open defects on switchgear" is a question the board can answer rather than a scroll.

> 💡 A healthy inbox is one where every open defect has a *reason* it's still open — it's either newly logged (awaiting your next triage pass) or a deliberate watch-list item. Open defects with no decision behind them are how real problems get buried.

### 2.7 Raising a work order from a defect — the key move

This is the most important action in the chapter, because it's the hinge of the whole loop. From an open (or already-work-ordered) defect's **⋯ menu**, **Raise work order** does several things **atomically** — all or nothing:

- Creates a **corrective** work order (Chapter 3), **linked to the defect** (so the job and the finding are forever associated).
- **Titles it from the defect** — *"Repair: <defect title>"* — unless you override the title.
- **Derives the job's priority from the defect's severity**: critical → **urgent**, high → **high**, low → **low**, everything else → **medium**. You can override this too.
- **Carries the asset (and any location pin) onto the order**, so the crew knows exactly what and where. For an orphan defect, that inherited **location is the only anchor** the crew gets — which is exactly why orphan defects must be pinned.
- **Flips the defect to `work_ordered`** — but only from `open`. A defect that was already work-ordered stays so (you're allowed to raise a *second* order against it if the first didn't cover everything); a resolved or dismissed defect is refused outright (§2.4).

Because it's one transaction, you can never end up with a defect flipped to work-ordered but no job created, or a job created but the defect left dangling in triage. They move together or not at all.

> *Worked example (Dana, continuing §2.5).* Femi's *"Stay wire heavily corroded"* defect (high) is in Dana's inbox. She picks **Raise work order** from its ⋯ menu. VoltEdge creates a corrective order titled *"Repair: Stay wire heavily corroded, near failure"* at priority **high** (from the severity), on that pole, and flips the defect to **work ordered**. Dana didn't retype the asset, the title, or the priority — the defect carried all of it. She now only has to *schedule and assign* the job (Chapter 3).

### 2.8 Resolving and dismissing

The two terminal moves, and when each is right:

- **Resolve** — the problem is **actually fixed**. Best practice: resolve a defect *after* the work order that fixed it is completed, so the trail reads finding → job → done. Resolving stamps the resolved-time.
- **Dismiss** — the defect needs **no action**: it's a duplicate of another, a misread, a false alarm, or superseded (the asset was replaced entirely, say). Dismissing is also terminal and timestamped; it keeps the record for the audit trail rather than deleting it, but marks it as "closed without a fix."

Both are reversible: **reopen** a defect and its resolved-stamp clears, putting it back in triage. Use that when a "fixed" defect recurs, or a dismissal turns out to be wrong.

> ⚠️ Resist the temptation to **resolve a defect the moment work is done in the field without recording it through a work order**. It's quicker in the moment and it destroys the trail — a year later, a reliability report can't show *what* was done or *by whom*. The extra thirty seconds of raising and completing a work order is the difference between a record and a rumour. (Chapter 3 makes that path fast.)

### 2.9 A full defect life — worked example

> *Tuesday — born.* During his rounds, **Femi** records the **MV switchgear annual** inspection on switch **SW-Feeder7-03** (Chapter 5). He answers item 6, *"Any oil weeping at the tank seal?"*, as **Fail**. Dana armed that item at **high** severity, so on sync VoltEdge **auto-logs** defect D-207: *"Inspection: Any oil weeping at the tank seal?"*, severity **high**, source **inspection**, on SW-Feeder7-03, linked to the failed item. Femi never opened the defect form.
>
> *Wednesday — triaged.* **Dana** sees D-207 near the top of her inbox (high, and newest). She reads the linked inspection item and its note, agrees it's real, and from the ⋯ menu picks **Raise work order**. VoltEdge creates corrective order W-88, *"Repair: Inspection: Any oil weeping at the tank seal?"*, priority **high**, on SW-Feeder7-03, linked to D-207 — and flips **D-207 → work_ordered**. It leaves her "needs a job" pile.
>
> *Wednesday — planned.* Dana renames W-88 to something a crew will read at a glance — *"Reseal SW-Feeder7-03 tank, oil weep"* — schedules it for Friday, and assigns it to Femi (Chapter 3).
>
> *Friday — done.* Femi completes W-88 on site with a note (*"Reseated gasket, topped up oil, no weep after 30 min"*). W-88 → **completed**.
>
> *Friday — closed.* Dana (or Femi) marks **D-207 → resolved**. Its resolved-time is stamped. The full linked trail now exists: inspection item → D-207 → W-88 → completed → D-207 resolved.
>
> Had the reseal *not* held, Femi would have failed a re-inspection, a fresh defect would have been raised, and the loop would simply have turned again — nothing lost, nothing untracked. And throughout, **Priya** (viewer) could read every step on SW-Feeder7-03's Works tab, buttonless.

### 2.10 Pitfalls & good habits

- **Vague titles bury real problems.** Put the specific, actionable fact in the title (§2.1).
- **Severity inflation breaks triage.** If everything is high, nothing is. Keep it honest (§2.3).
- **Quiet resolves destroy the trail.** Route fixes through a work order, then resolve (§2.8).
- **Don't re-log a defect that's already work-ordered.** It left the "needs a job" pile on purpose; check before logging a duplicate. (Dismiss genuine duplicates rather than leaving two open.)
- **Orphan defects must be pinned.** A location-only defect with no pin can't be raised into locatable work — always drop the map point.
- **Log where you are.** On the map with the asset in view? Use its **Works tab**, not the board — the asset fills itself in.

---

## Chapter 3 — Work orders: the one typed engine

If a defect is "what's wrong," a work order is "the job that puts it right — or keeps it right." This chapter is the heart of the module. It explains VoltEdge's deliberate design choice — **one** work-order engine, not four separate systems — the lifecycle every order shares, and how to drive an order from creation to a properly-recorded completion. 🔒 All writing here needs `wam:write`.

### 3.1 One engine, four flavours — the design

A tempting way to build work management is to make inspections, repairs, servicing, and construction four separate things with four separate screens. VoltEdge deliberately does **not** do that. Every unit of work — a checklist round, a corrective repair, a preventive service, a build job — is **the same kind of record**, a *work order*, distinguished only by a **work type** field and otherwise sharing one lifecycle, one board, one set of actions.

Why this matters to you as a user:

- **One place to look.** "What work is open across my territory?" is a single list, not four. The board's *Work orders in flight* column shows every kind of job together, sorted and filterable.
- **One set of habits.** Once you know how to start, complete, assign, schedule, and cancel *one* work order, you know how to do it for *all* of them. A construction job and an inspection round behave identically; only their type label and their purpose differ.
- **Consistent reporting.** Because all work shares a lifecycle and timestamps, "how much work did we complete last month, by type?" is answerable across the board rather than stitched from separate systems.

### 3.2 The four work types

The **work type** is what the job *is*. It's set when the order is created and doesn't change the mechanics — only the meaning.

| Type | What it's for | Typically born from |
|---|---|---|
| **corrective** | Fixing a specific known problem on an asset. | **Raised from a defect** (§3.9) — this is the type that hinge produces. |
| **inspection** | Running a checklist round against an asset on a cycle. | Created by a planner; **fulfilled by recording an inspection** against it (Chapter 5). |
| **preventive** | Scheduled servicing to *prevent* failure — greasing, tightening, oil top-ups, vegetation clearance — not tied to a specific reported defect. | Created by a planner as part of a maintenance regime (Chapter 6). |
| **construction** | Building or extending network — a new pole, a new span, a reconductoring job. | Created by a planner; often larger and longer-lived. |

> 💡 The type is guidance, not a gate. Nothing stops you completing a "construction" order the same way as an "inspection" one. Choose the type that makes the work *findable and reportable* under the right heading — that's its whole job.

### 3.3 What a work order carries

- **A title** (required) — the one-liner a crew reads. *"Reseal SW-Feeder7-03 tank, oil weep."* Make it a clear instruction.
- **A work type** (required, §3.2) and a **priority** (`low` / `medium` / `high` / `urgent`, default **medium**, §3.5).
- **A status** — its place in the lifecycle (§3.4).
- **A primary asset** (optional) — the feature the work targets, class + id. Optional because some work (a survey, a general task) isn't about one modelled asset; but naming it is what makes the order show on that asset's **Works tab**.
- **An assignment** (optional) — an **internal worker** (a VoltEdge user), an internal **crew**, an external **subcontractor**, or a free-text label — see §3.6 for how they differ.
- **An outage link** (optional) — when the order was raised from an OMS incident (a crew dispatch or a subcontractor tasking), its `incident_id` ties it back to the outage it serves; the board/agent can filter by it (§3.6, OMS guide §12).
- **A schedule** (optional) — a **scheduled-for** date/time and, in the API, a **due-by** date (§3.7).
- **Lifecycle timestamps** — **started-at** and **completed-at**, stamped automatically as the order moves (§3.4).
- **A completion note** (optional) — what was actually done, recorded at completion (§3.10).
- **An optional reference** and **description**, and — when it was raised from a defect — the **defect link** that ties job to finding.

### 3.4 The shared lifecycle

Every work order walks the same six-state path:

```
   ┌───────┐   ┌───────────┐   ┌──────────┐   ┌─────────────┐   ┌───────────┐
   │ draft │──▶│ scheduled │──▶│ assigned │──▶│ in_progress │──▶│ completed │
   └───────┘   └───────────┘   └──────────┘   └─────────────┘   └───────────┘
       │             │              │                │
       └─────────────┴──────────────┴────────────────┴────────▶ ┌───────────┐
                          cancel, any time before done            │ cancelled │
                                                                  └───────────┘
```

- **draft** — created but not yet planned.
- **scheduled** — given a target date but not yet assigned to anyone.
- **assigned** — handed to a worker or crew.
- **in_progress** — work has started.
- **completed** — done. Terminal.
- **cancelled** — abandoned before completion (duplicate, no longer needed, superseded). Terminal.

Three mechanics make this smooth rather than bureaucratic:

1. **Creation auto-advances the status for you.** You rarely set "draft/scheduled/assigned" by hand. When you create an order:
   - name an **assignee** (internal *or* external) and it starts at **assigned**;
   - give it a **schedule** but no assignee and it starts at **scheduled**;
   - give it neither and it starts at **draft**.
   So a job you create already-assigned lands in the right place with no extra clicks.
2. **The timestamps keep themselves honest.** Moving an order to **in_progress** stamps its **started-at** (once — it won't be overwritten if you bounce back and forth). Moving to **completed** stamps **completed-at**. Moving *off* completed (a mistaken complete, reopened) **clears** completed-at. So "when did this start / finish?" always has a truthful answer, and completion is fully reversible.
3. **You don't have to walk every step.** The states are an *order*, not a checklist you must tick through. In practice the board gives you the three moves that matter — **Start**, **Complete**, **Cancel** — and creation/assignment handle the earlier states.

### 3.5 Priority

Priority (`low` / `medium` / `high` / `urgent`) is how the queue sorts when there's more work than crew. Two things to know:

- **A corrective order inherits its priority from the defect's severity** (critical → urgent, high → high, low → low, else medium) — see §3.9. You can override it at raise time.
- **A standalone order defaults to `medium`** and you set it on creation. There's no automatic escalation over time — priority is what you set it to, so revisit it if a job ages.

### 3.6 Assignment — worker, crew, or subcontractor

A work order can be owned **four** ways, which look similar in the dialog but behave very differently in the field. In order of strength:

- **Internal worker** (`assigned_to_user_id`) — a VoltEdge **user**, from the **assignee picker**. The order is tied to a real account: that person can filter the board to **their** work (the **"Mine"** toggle), and a worker's open orders ride into **their field pack** on the phone. The picker isn't a hardcoded list — it's **everyone who holds `wam:write`** (the people who can perform and close work), so adding a user to a field role makes them assignable automatically.

- **Internal crew** (`crew_id`) — a **crew**, chosen from the crew picker (which shows each crew's live load — *members · open orders · incidents* — so you can see a backlog before piling on). The payoff: a crew-assigned order **fans out to every crew member's field pack**, not just one person's. Crews are teams of users with membership (including temporary members), managed at **Admin → Crews** and dispatched by the Outage board — see **OMS guide §12**. Click a crew to see the work it owns.

- **External subcontractor** (`subcontractor_id`) — an **external firm** from the subcontractor picker. Unlike the two above, a subcontractor **does not operate in this system**: tasking one **records responsibility only** — there is **no field-pack fan-out**, because the firm runs the job on their own tools. You maintain the firms at **Admin → Subcontractors** (a registry of name + contact, `wam:write`); your own coordinator tracks the order's status on their behalf. Click a firm to see everything tasked to it.

- **Free-text label** (`assigned_to`) — an ad-hoc **"External crew"** string for a genuinely one-off or unregistered party (*"Voltaic Ltd"*). It's a label, not a link — no account, no "Mine", no fan-out. Prefer a registered **subcontractor** when it's a firm you'll task repeatedly; keep the label for one-offs.

Assigning *any* of these at creation advances the order to **assigned** (§3.4). The modes aren't exclusive at the column level, but the dialog treats them as a choice — pick the one that matches who's actually doing the work. To **unassign**, clear the field (the API treats an empty value as "set to nobody" / `NULL`).

> **∑ The field-pack rule.** Work reaches the phone only through an *internal* owner: `assigned_to_user_id` (that one person) or `crew_id` (every member of the crew). A `subcontractor_id` or a free-text label reaches **no** pack — those parties are tracked, not dispatched into VoltEdge. This is the one thing to get right when you choose an owner.

> 💡 **Raised from an outage.** When the Outage board dispatches a crew or tasks a subcontractor with the *"Raise restoration work order"* option, it creates one of these orders for you — crew- or subcontractor-owned, `source = outage`, pinned to the faulted device and linked to the incident (`incident_id`). A crew arriving on-site even flips its restoration orders to **in_progress** automatically (completion still stays your deliberate call). See **OMS guide §12**.

### 3.7 Scheduling and due dates

- **Scheduled-for** is the planned date/time the work should happen. Set it on creation (the *Scheduled for* field is a date-time picker) and the order starts at **scheduled** (if no assignee) so it's clearly planned rather than a loose draft. The board row shows the schedule when set, and the creation date otherwise.
- **Due-by** is a separate target/deadline the underlying engine supports (distinct from *scheduled-for*: "I plan to do it Thursday, but it's due by month-end"). It's available through the API and agent; the standard *New work order* dialog focuses on *scheduled-for* as the everyday field.

> 💡 Schedule vs assign are independent. A **scheduled** order (has a date, no owner) is a common and useful state — it says "this is planned for Thursday, we'll assign the crew nearer the time." Don't feel you must assign the moment you schedule.

### 3.8 Creating a standalone work order — step by step

Use this for preventive, inspection, and construction work that *isn't* born from a specific defect. (Corrective work should be **raised from its defect** instead — §3.9 — so the trail is linked.)

*Dana plans an inspection round.*

1. **Works** → **New work order.**
2. **Work type = inspection**, **priority = medium**.
3. **Title** — *"Q3 pole-top inspection, Feeder 7 north."*
4. **Asset** (optional) — she leaves it blank here because this round covers many poles, not one; she'll pick the specific asset when she *records* each inspection (Chapter 5). *(If the job were about one asset, she'd name it so it shows on that asset's Works tab.)*
5. **Assign to worker** — she picks Femi from the picker. (This alone advances the order to **assigned**.)
6. **Scheduled for** — next Monday.
7. **External crew / description** — left blank / a note. **Create.**

The order appears in the right-hand *Work orders in flight* column at status **assigned**, on Femi's plate, scheduled for Monday. Because she assigned it, the board's **Unassigned** KPI does *not* tick up.

### 3.9 Raising a corrective order from a defect — recap

This is the corrective path, covered in depth in §2.7. In short: from a defect's **⋯ menu → Raise work order**, VoltEdge atomically creates a **corrective** order (titled *"Repair: <defect title>"*, priority derived from the defect's severity, carrying the asset and any location pin, linked to the defect) and flips the defect to **work_ordered**. The **Raise corrective work order** dialog lets you optionally set the **assignee** and **schedule** at the same time — so in one action Dana can create *and* assign *and* schedule the fix. Prefer this over a standalone corrective order every time: the defect link is what makes the finding → job → done trail complete.

### 3.10 Driving an order: Start, Complete, Cancel

Once an order exists, the board (and the asset's Works tab) give a live order a compact **⋯ menu** with the moves that matter. They appear only on non-terminal orders, and only if you hold `wam:write`:

- **Start** — moves the order to **in_progress** and stamps **started-at**. The board's **In progress** KPI ticks up. (Offered whenever the order isn't already in progress and isn't finished.)
- **Complete** — moves an **in_progress** order to **completed** and stamps **completed-at**. This is the one-tap "done."
- **Cancel** — moves any non-terminal order to **cancelled**. Use it for a job that's a duplicate, no longer needed, or superseded — the record is kept, not deleted.

On **completion notes:** a work order can carry a **completion note** — *what was actually done* — and it's part of the permanent record (shown wherever the order is read). Notes are written automatically when an **inspection completes its work order** (Chapter 5 — the inspection's outcome becomes the order's completion note), and can be set through the API/agent. The board's one-tap **Complete** marks the order done immediately; where the *substance* of what was done matters for the record (most corrective and construction work), capture it — either by completing the order via the inspection that documents it, or by recording the detail in the linked defect's resolution and the order's note.

> 💡 The clean closing sequence for corrective work: crew **Starts** the order on arrival → does the work → **Completes** it with a note of what was done → the planner **Resolves** the linked defect (§2.8). Four taps, one unbroken trail.

### 3.11 Reading the board — KPIs and the flow

The Works board's top strip is calibrated for work-order health:

- **Active orders** — everything not yet completed or cancelled. Your live workload.
- **In progress** — orders a crew has *started*. If this is high and stays high, jobs are being started but not finished — a flag worth chasing.
- **Unassigned** — active orders with **no** owner (neither internal worker nor external crew). This is the number to drive to zero each planning pass; an unassigned active order is work nobody has picked up.

The right-hand column lists the orders themselves, newest-first, each showing status, type, priority, asset, assignee, and schedule at a glance. The **"Mine"** toggle narrows it to your own assigned work — the field crew's daily view.

### 3.12 A work order, end to end — worked example

> *Continuing D-207 from §2.9.* Dana has just raised corrective order **W-88** from Femi's oil-weep defect. In the same **Raise corrective work order** dialog she assigns it to **Femi** and schedules it for **Friday** — so W-88 is born at status **assigned**, priority **high** (inherited from the defect), on SW-Feeder7-03, linked to D-207.
>
> *Friday 10:40 — Femi arrives.* From the switch's **Works tab** on his phone, he opens W-88 and taps **Start**. Status → **in_progress**, started-at stamped 10:40. Dana's **In progress** KPI ticks up.
>
> *Friday 11:20 — done.* Femi reseals the tank, watches for 30 minutes, sees no weep, and taps **Complete**. Status → **completed**, completed-at stamped. He records what he did as the completion detail. W-88 leaves the *in flight* column.
>
> *Friday 11:25 — loop closed.* Dana marks **D-207 → resolved**. The trail is whole: inspection item → D-207 (defect) → W-88 (corrective, assigned→in_progress→completed, ~40 min on site) → D-207 resolved.
>
> A month later, asked "what corrective work did we do on Feeder 7 switchgear, and how long did it take?", the board answers from the timestamps — no separate log, no guesswork. That's the payoff of the one typed engine.

### 3.13 Pitfalls & good habits

- **Corrective work belongs to its defect.** Raise it *from* the defect (§3.9), don't create a loose corrective order — otherwise the finding and the fix aren't linked.
- **Drive Unassigned to zero.** An active order with no owner is work nobody's doing. Assign it or cancel it.
- **Assign to a *user*, not just a label, where you can.** Internal assignment powers the "Mine" view and is unambiguous; reserve the free-text crew label for genuinely external work.
- **Start when you start, complete when you finish.** The timestamps are only as truthful as the taps. A batch of orders all "completed" at 5 pm with no start times tells a reliability report nothing.
- **Cancel, don't delete-by-neglect.** A job that's no longer needed should be **cancelled** (it stays on the record) rather than left rotting as an open order that skews the KPIs.
- **Pick the type for findability.** The work type is how work is grouped and reported — choose the heading the job belongs under (§3.2).

---

## Chapter 4 — Inspection templates: reusable checklists

An inspection is only as good as the checklist behind it. A **template** is that checklist — a named, ordered set of questions you define once and run many times, against many assets, over many years. This chapter is about *authoring* templates well; Chapter 5 is about *running* them. The single most important idea here is the **fail-severity arm** (§4.4) — the setting that turns a failed checkbox into a tracked defect automatically. 🔒 Authoring needs `wam:write`.

### 4.1 Why checklists, not free-form notes

You *could* inspect an asset by writing a paragraph of notes. Templates exist because that doesn't scale:

- **Consistency.** Every inspector answers the *same* questions in the *same* order, so two people inspecting two switches produce comparable records. "Did anyone check the silica gel?" is never ambiguous.
- **Completeness.** A checklist is a memory aid — the twelve things that must be looked at are *listed*, not left to recall on a cold morning at the top of a pole.
- **Automation.** A structured answer ("item 6: fail") can *do something* — raise a defect, roll up an overall result — in a way a paragraph never can. This is the payoff, and §4.4 is where it lives.
- **Evidence.** A recorded checklist is an auditable artefact: *this* asset was checked against *these* criteria on *this* date with *these* results.

### 4.2 What a template is made of

A template has a small head and a list of items.

**The head:**

- **Code** (required) — a short, stable slug you'll recognise: `MV_SWGR_ANNUAL`, `POLE_TOP_VISUAL`, `DIST_XFMR_2YR`. Think of it as the checklist's permanent handle.
- **Name** (required) — the human title shown in the picker: *"MV switchgear annual inspection."*
- **Description** (optional) — a sentence of context or instruction for the inspector.
- **Asset class** (optional) — a **scope**: the feature class this checklist is *for* (§4.6). Optional, but setting it makes recording faster and less error-prone.

**The items** — at least one is required. Each checklist item carries:

- **A prompt** (required) — the question, phrased for a yes-is-good answer where possible: *"Any oil weeping at the tank seal?"*, *"Silica-gel colour acceptable?"*, *"Earth resistance reading (Ω)?"*
- **A response type** (§4.3) — how the answer is captured.
- **A fail severity** (§4.4) — optional; the arming that makes a failed answer raise a defect.
- **Guidance** (optional) — a note to the inspector on *how* to judge the item (*"Amber or pink gel = fail; blue = pass"*).

Items are **ordered** (by position), and that order is the order the inspector sees them — so put the checklist in the sequence a person actually walks the asset.

### 4.3 The four response types

The **response type** decides what kind of answer an item captures. There are four:

| Response type | The inspector records… | Use it for | Can it arm a defect? |
|---|---|---|---|
| **pass_fail** | A straight **Pass / Fail** (or N/A). | The bulk of a checklist — condition checks with a clear good/bad. *"Any corrosion on the tank?"* | **Yes** — fail is unambiguous. |
| **rating** | A **number on a scale** (plus pass/fail). | Graded condition — *"Paintwork condition 1–5."* | Yes (via its pass/fail outcome). |
| **numeric** | A **measured reading** (plus pass/fail). | Instrument values — *"Earth resistance (Ω)", "Oil temperature (°C)."* | Yes (via its pass/fail outcome). |
| **text** | A **free-text value** (plus pass/fail). | Notes and identifiers — *"Nameplate serial", "Observations."* | Yes (via its pass/fail outcome). |

Two things to understand about this:

- **Every type still has a pass/fail outcome.** Even a numeric or text item is answered *both* with its value *and* with a Pass / Fail / N/A judgement. That's deliberate: it's the **pass/fail outcome** — not the raw number — that drives the auto-defect and the overall result (§4.4, §5). So a numeric item *"Earth resistance (Ω)"* records the reading **and** whether that reading is acceptable; a bad reading is a **Fail** and, if armed, a defect.
- **The default is pass_fail.** If you don't choose, an item is a pass/fail question — the most common kind.

### 4.4 The fail-severity arm — the heart of it

This is the mechanism that makes inspections worth building. Each item has an optional **fail severity**. It has two states:

- **Unset — "None (informational)."** A failing answer is *recorded* (you'll see the ✗ in the inspection's history) but raises **no defect**. Use this for observational items where a "fail" is a note, not an action — *"Nameplate legible?"* on a survey checklist, say.
- **Set to a severity (`low` / `medium` / `high` / `critical`) — "armed."** Now a failing answer to this item **automatically logs a defect** at that severity when the inspection is recorded — on the inspected asset, sourced `inspection`, titled from the item's prompt, and linked back to the exact item. The inspector doesn't open a defect form; the checklist does it.

So authoring a template is largely a design exercise in **which items are armed, and at what severity**:

- Arm the items where a failure genuinely means "someone must act," at a severity that reflects *how urgently*. *"Any oil weeping at the tank seal?"* armed **high**; *"Silica-gel colour acceptable?"* armed **medium**; *"Fencing secure?"* armed **critical** (safety); *"Paint condition acceptable?"* left **informational** or armed **low**.
- Leave purely observational items **unset**, so recording a checklist doesn't spray low-value defects into the triage inbox.

> 💡 The severity you arm an item with flows all the way through the loop: a **critical**-armed item that fails raises a **critical** defect, which — if raised into work — produces an **urgent** work order (§2.3, §3.5). You are, in effect, pre-deciding the triage of a whole class of future failures at the moment you write the checklist. Arm thoughtfully.

> ⚠️ **In the field-app UI, an item defaults to a `medium` fail severity when you add it** — armed, not informational. That's a sensible default (most checklist items *should* arm), but it means you must **deliberately choose "None (informational)"** for the observational items, or every one of them will raise a defect on failure. Review each item's severity before saving.

### 4.5 Building a template — step by step

*Dana authors the MV switchgear annual.* From **Works**, in the **Inspection templates** card, she clicks **New template**.

1. **Code** — `MV_SWGR_ANNUAL`. **Name** — *"MV switchgear annual inspection."*
2. **Asset class** (optional) — `MV_SWITCHGEAR_PANEL`, so the checklist is scoped to switchgear and pre-fills the asset class at record time (§4.6).
3. **Description** — *"Annual condition inspection for ring-main and switchgear units."*
4. **Checklist items** — she adds them in walk order, choosing a response type and a fail severity for each:
   1. *"Enclosure/fencing secure and undamaged?"* — pass_fail, **critical** (safety).
   2. *"Any oil weeping at the tank seal?"* — pass_fail, **high**.
   3. *"Silica-gel breather colour acceptable?"* — pass_fail, **medium**, guidance *"Blue = pass; amber/pink = fail."*
   4. *"Earth resistance reading (Ω)?"* — **numeric**, **high** (a bad reading is a real problem).
   5. *"SF6 / oil pressure gauge in green band?"* — pass_fail, **high**.
   6. *"Nameplate serial number"* — **text**, **None (informational)** (she's capturing data, not judging it).
   7. *"General paintwork condition (1–5)"* — **rating**, **low**.
5. She reviews the severities — item 6 is correctly **informational**, the rest armed as intended — and clicks **Create template**.

`MV_SWGR_ANNUAL` now appears in the templates list and, because it's active, in the template picker inspectors choose from when recording (Chapter 5).

### 4.6 Scoping a template to an asset class

The optional **asset class** on the head is a *scope*, and it does two useful things:

- **It documents intent** — the list makes clear this checklist is *for* switchgear, not poles.
- **It pre-fills the asset class when recording** — when an inspector picks this template to record against, the asset class is filled in from the template's scope, so they only supply the specific asset id. Less typing, fewer wrong-class mistakes.

Leaving it blank makes a **general** checklist that can be run against any asset (a generic "site visit" checklist, say). Scope when the checklist is class-specific; leave open when it's not.

### 4.7 Templates are *versioned*, not edited

This is the most important operational fact about templates, and it's deliberate: **a template cannot be edited after it's created.** There is no "edit items" — you create a template, and from then on its items are fixed. You can only **retire** or **restore** it (§4.8).

Why it works this way, and why that's the right design:

- **History must not rewrite itself.** When an inspection is recorded, each answer **snapshots the prompt and response type off the template item** into the inspection's own record. So a completed inspection is a permanent, standalone artefact — *"on this date, this asset was checked against exactly these questions with these results."* If you could later edit the template's wording or arming, you'd risk changing what a past inspection *appears* to have asked. Freezing the template protects the record.
- **So you change a checklist by versioning it.** To revise `MV_SWGR_ANNUAL` — add an item, re-word a prompt, change an arming — you **create a new template** (a new version: keep the same code lineage in the name, e.g. code `MV_SWGR_ANNUAL_V2`, or a fresh descriptive code) and **retire the old one**. New inspections use the new checklist; the old inspections keep pointing at the criteria they were actually run against. Nothing breaks, nothing is rewritten.

> 💡 Because there's no edit, it's worth getting a new template's items *right* before you create it — sketch the checklist first. But a mistake isn't costly: retire the wrong one, create the corrected one. The only "cost" is that the retired version stays in the history (which is exactly what you want).

### 4.8 Retiring and restoring

A template is either **active** or **retired**:

- **Active** — appears in the record picker; inspectors can run it.
- **Retired** — hidden from the picker (nobody starts a new inspection with it) but **kept**, so every inspection ever recorded against it still reads correctly. Its row in the templates list shows a *retired* marker.

From the templates list, each row's button toggles this: **Retire** an active template, **Restore** a retired one. Retiring is the graceful way to take an old checklist out of circulation (superseded by a new version, or no longer needed) without deleting anything.

### 4.9 Who can author — the flat-write caveat, again

Authoring, retiring, and restoring templates all sit under the single **`wam:write`** permission — the *same* permission that lets someone log a defect or complete a work order. There is no separate "template author" role.

The practical consequence, flagged in Chapter 1 and worth repeating here where it bites: **a field technician who holds `wam:write` can create and retire the templates the whole organisation inspects against.** For a small team that's convenient. For a larger one where you'd want checklist design to sit with a few maintenance engineers while field crews only *run* the checklists, that separation isn't available out of the box — it would require an administrator to split the permission deliberately. If template governance matters to you, decide this consciously rather than discovering it later. *(Chapter 8 returns to the permission model in full.)*

### 4.10 Pitfalls & good habits

- **Arm deliberately; the UI defaults to armed.** New items start at `medium` severity in the field app — set the observational ones to **None (informational)** or they'll raise defects on failure (§4.4).
- **Phrase prompts so a "pass" is the good answer.** *"Any corrosion?"* → **Fail** means corrosion found. Consistent polarity keeps the ✗/✓ history readable at a glance.
- **Order items the way you walk the asset.** The item order is the inspector's order.
- **Use guidance to make judgements objective.** *"Amber/pink gel = fail"* removes the "well, it depends" from a checklist.
- **Scope class-specific checklists.** It pre-fills the asset class and prevents cross-class mistakes (§4.6).
- **Version, don't agonise over editing — because you can't edit.** Get it right up front; when it must change, new template + retire old (§4.7).
- **Don't over-arm.** A checklist where every item is `high` floods triage on any bad day. Arm what genuinely needs action; leave the rest informational.

---

## Chapter 5 — Recording inspections: executing a checklist

Chapter 4 built the checklist; this chapter *runs* it. Recording an inspection is where the module's automation pays off: in one action you capture a structured condition record, **auto-raise defects** for the items that failed, roll up an **overall result**, and — if the inspection fulfils a work order — **complete that order** too. This is the single most valuable action in WAM, and it's the one your field crews will do most. 🔒 It needs `wam:write`.

### 5.1 What "recording an inspection" actually does

When you record an inspection, VoltEdge does the following as **one atomic transaction** — all of it succeeds, or none of it does:

1. Writes the **inspection** (the header: which asset, which template, who, when) and **one result row per checklist item** (each snapshotting its prompt and response type, so the record is permanent — §4.7).
2. For every item answered **Fail** that carries a **fail severity**, **logs a defect** — on the inspected asset, source `inspection`, at the armed severity, titled from the item's prompt — and **links that defect back onto the item result**, so you can always see which check produced which defect.
3. Rolls up the **overall result** (§5.6).
4. If the inspection is **fulfilling a work order** and you asked to, marks that order **completed**, writing the inspection's outcome as the order's completion note.

Because it's one transaction, you never get a half-recorded inspection, an orphaned defect, or a work order completed against an inspection that didn't save. And because inspections carry a device-minted identity, recording one **offline** and syncing it later is safe — a re-pushed inspection is a no-op, so its defects are never raised twice (§5.7).

### 5.2 Opening the recorder

From **Works**, in the **Inspections** card, click **Record inspection**. (If you don't hold `wam:write`, the button isn't there — you can still read recorded inspections, §5.5.) The recorder is a single dialog that walks top to bottom: pick a template, optionally tie it to a work order, name the asset, fill the checklist, save.

### 5.3 Step 1 — pick the template

The **Template** dropdown lists the **active** checklists (retired ones are hidden — §4.8). Picking one **loads its items** into the dialog as an editable checklist, each defaulted to **Pass**. If the template is **scoped to an asset class** (§4.6), that class is pre-filled into the asset field for you.

> 💡 No template yet? The dialog tells you so — you (or a colleague with `wam:write`) must create one first (Chapter 4). Recording always runs *a* checklist; there's no free-form inspection.

### 5.4 Step 2 (optional) — fulfil a work order

If there are open **inspection**-type work orders (not completed or cancelled), a **Fulfilling work order** dropdown appears. This is the join between the *planning* side (Dana schedules an inspection round as a work order, §3.8) and the *doing* side (Femi runs the checklist). Picking the order here does two things:

- **Pre-fills the asset** from the work order (its asset class and id), so you're inspecting exactly what was planned.
- **Offers to complete the order** — a *"Mark this work order completed"* checkbox, ticked by default. Leave it ticked and, when you save the inspection, the work order is closed in the same breath, with the inspection's outcome as its completion note.

Leave the dropdown on **"None (ad-hoc)"** for an inspection that isn't fulfilling any planned order — an opportunistic check while you're on site, say.

### 5.5 Step 3 — name the asset

Every inspection is **against one asset**: a **class** and an **id**. Depending on the steps above, these may already be filled — from the template's scope, or from the fulfilled work order. If not, supply them (the id is the specific feature's identifier; recording from an asset's context on the map fills it for you). This is required — an inspection with no asset is rejected.

### 5.6 Step 4 — fill the checklist

Each loaded item shows its prompt (and its armed severity, if any) with an outcome selector and, for some types, a value field:

- **Outcome** — **Pass / Fail / N/A** for every item, whatever its type. This is the judgement that drives everything.
- **Value** — for **numeric** and **rating** items, a number field for the reading; for **text** items, a text field for the value. (Pass/fail items have no value field — just the outcome.)
- **Notes** — an optional free note on any item.

As you work, a **live preview line** at the bottom tells you exactly what will happen on save:

- *"All passing."* — nothing failed.
- *"3 failing · 2 will be logged as defects"* — three items marked Fail, of which two are armed (the third is informational, so it's recorded but raises no defect).
- *"1 failing · none arm a defect"* — a failure on an informational-only item.

That preview is your safety check: it shows the auto-defect consequences *before* you commit, so there are no surprises in the triage inbox.

**The overall result** is rolled up automatically: an inspection is **pass** unless **any** item was marked Fail, in which case it's **fail**. (The engine also supports a **conditional** result — "passed with reservations" — as a deliberate override where your process needs that middle ground; the everyday path is the automatic pass/fail.)

### 5.7 What happens when you save

Press **Record inspection** and, atomically:

- The inspection and all its item results are written.
- Each **armed Fail** becomes a **defect**, linked to its item. Those defects appear instantly in the triage inbox (Chapter 2) and on the asset's Works tab.
- If you were fulfilling a work order and left the box ticked, that order flips to **completed**, its completion note recording the inspection outcome (*"Inspection recorded: fail"*).
- The overall result is stamped on the inspection.

One action; the whole downstream cascade handled.

### 5.8 Reviewing recorded inspections

The **Inspections** card lists what's been recorded, newest activity to hand. Each row shows the **overall result** as a coloured badge (green **pass**, red **fail**, amber **conditional**), the asset, who inspected it, and when. **Click a row to expand it** and see the item-by-item detail:

- **✓** (green) — passed, **✗** (red) — failed, **—** — not answered / N/A.
- The item's **value** where it had one (the reading, the text).
- A **defect link** on any item that raised one — so from the inspection you can jump straight to the tracked problem it created.

This expandable history is the audit artefact: for any asset, you can read *exactly* what was checked, when, by whom, with what results, and what it triggered. Readers (`wam:read`) — including Priya — can browse all of this; they simply can't record new ones.

### 5.9 Offline and idempotency

Field crews record inspections on a phone, often with no signal. VoltEdge handles this cleanly (the mechanics are Chapter 7; the guarantee matters here): a recorded inspection carries a **device-minted id**, and recording is designed to be **replay-safe**. If a sync pushes the same inspection twice — a flaky connection, a retried upload — the second apply is a **no-op**: it does **not** re-write the inspection, and crucially does **not** re-raise its defects or re-complete its work order. So an inspection Femi records in a dead spot at 08:00 and syncs at noon lands exactly once, with exactly its defects, no matter how many times the upload is retried.

### 5.10 A recording, end to end — worked example

> *In the field.* **Femi** opens **Record inspection**. He picks the **MV switchgear annual** template; its seven items load, all defaulted to **Pass**, and because the template is scoped to `MV_SWITCHGEAR_PANEL`, that class pre-fills. The **Fulfilling work order** dropdown shows Dana's scheduled *"Q3 switchgear inspection — SW-Feeder7-03"* order; he picks it, which fills the **asset id** (SW-Feeder7-03) and leaves *"Mark this work order completed"* ticked.
>
> *He walks the switch.* Items 1, 3, 4, 5, 7 pass. Item 2 — *"Any oil weeping at the tank seal?"* (armed **high**) — he marks **Fail** and notes *"slow weep, ~1 drop/min at base gasket."* Item 6 — the informational *"Nameplate serial number"* (**text**) — he records the serial and marks it Pass.
>
> *The preview reassures him.* The line at the bottom reads *"1 failing · 1 will be logged as a defect."* Exactly right: the oil-weep fail is armed; nothing else failed.
>
> *He saves.* Atomically, VoltEdge: writes the inspection (overall **fail**, because one item failed); raises **defect D-207** (*"Inspection: Any oil weeping at the tank seal?"*, high, source inspection, on SW-Feeder7-03, linked to item 2); and completes Dana's work order with the note *"Inspection recorded: fail."*
>
> *Later.* Dana sees D-207 at the top of her inbox and raises corrective order W-88 from it (§2.9) — and the loop we followed in Chapters 2 and 3 begins. Anyone can open SW-Feeder7-03's Works tab, expand this inspection, see the red ✗ on item 2 with its note and reading, and click through to D-207.

### 5.11 Pitfalls & good habits

- **Read the preview before saving.** *"N will be logged as defects"* is your last check that the checklist is about to do the right thing — no more, no fewer defects than you intend.
- **Record N/A honestly.** If an item genuinely doesn't apply to this asset, mark it **N/A** — don't Pass it. N/A is recorded truthfully and never raises a defect.
- **Put the substance in item notes and readings.** *"slow weep, ~1 drop/min"* on the failing item is what makes the auto-raised defect actionable — the defect inherits the context.
- **Fulfil the work order when there is one.** Tying the inspection to its planned order (and letting it complete the order) is what keeps the plan → done trail intact; recording ad-hoc when a planned order exists leaves that order dangling open.
- **Don't hand-log a defect for a failing armed item.** The inspection already raised it — logging another by hand duplicates it. Trust the automation; check the inbox after syncing.
- **Retired templates are gone from the picker by design.** If a checklist you expect isn't listed, it's been retired (superseded by a newer version, §4.8) — use the current one.

---

## Chapter 6 — Maintenance: running it on a cycle

The first five chapters gave you the parts: defects, work orders, templates, inspections. **Maintenance** is what you get when you run those parts *proactively and on a cycle* — inspecting and servicing assets on a schedule so that problems are found and fixed before they become outages, rather than only reacting when something fails. This chapter is the synthesis. It is also the most honest chapter in the manual, because maintenance is the area where the module gives you strong *tools* but expects **you** to supply the *cadence* — VoltEdge does not yet generate recurring work for you. Read §6.3 carefully; it's the load-bearing truth of this chapter.

### 6.1 What "maintenance" means here

Maintenance in VoltEdge is not a fourth module bolted on — it's the **deliberate, scheduled use of the three record types** you already know, in three modes of work:

- **Inspection rounds** — running your checklists (Chapter 4) against assets on a cycle, so condition is *measured*, not guessed. These are **inspection**-type work orders, fulfilled by recording inspections (Chapter 5).
- **Preventive servicing** — time-based upkeep that isn't tied to a specific reported fault: greasing switches, tightening connections, topping up oil, clearing vegetation. These are **preventive**-type work orders (Chapter 3).
- **Corrective follow-through** — fixing what the rounds find. The defects your inspections raise become **corrective** work orders (Chapters 2–3), closing the loop.

Put together, these three turn the reactive "something broke, go fix it" posture into a proactive regime: *inspect on a cycle → catch degradation early → service and correct before failure → inspect again.*

### 6.2 The proactive cycle

Chapter 1's loop, run on a schedule, *is* a maintenance regime:

```
     ┌──────────── on a cadence you set ────────────┐
     │                                              │
     ▼                                              │
  [ inspection round ]                              │
     │                                              │
     │ armed item fails (auto)                      │
     ▼                                              │
  [ defect ] ──raise──▶ [ corrective order ] ──done──┤
                                                     │
  [ preventive order ] ──────────────────────done────┤
     ▲                                              │
     └──────── created each cycle (by you) ─────────┘
```

The **inside** of that loop — a failing checklist item becoming a defect, a defect becoming a corrective order in one click, orders self-timestamping as they progress — is automated and was the subject of Chapters 2–5. The **outside** arrow — "on a cadence you set," "created each cycle by you" — is the part you drive. That division is the whole point of §6.3.

### 6.3 What's automated today, and what you drive by hand

This is the section to internalise. VoltEdge automates the **mechanics of a single pass** through the loop, but it does **not** yet automate the **recurrence** — there is no scheduler that says "it's been a year, generate the switchgear inspections." Be clear-eyed about the line:

| Automated for you | You drive by hand (today) |
|---|---|
| A failing **armed** checklist item raises a **defect** (Chapter 5). | Deciding the **cadence** — annual, 2-yearly, quarterly — for each asset class. VoltEdge stores no maintenance interval. |
| **Raise work order** from a defect creates a linked corrective order and flips the defect (Chapter 2). | **Creating each cycle's inspection and preventive work orders.** They don't appear on their own; a planner makes them each period. |
| Work-order **status auto-advances** at creation (assignee→assigned, schedule→scheduled) and **timestamps** stamp themselves (Chapter 3). | **Remembering when a cycle is due.** There are no due-date reminders, alerts, or notifications — the `due-by` and `scheduled-for` dates are planning *labels*, not triggers. |
| Recording an inspection can **complete** its work order (Chapter 5). | **Re-running the round next period.** Completing this quarter's orders does *not* schedule next quarter's. |
| Offline records sync **idempotently** (Chapter 7). | Any **calendar / "what's due this month" view** — you assemble it from the board's filters (§6.5), there isn't a dedicated one. |

> ⚠️ **Recurring, auto-generated maintenance plans are a planned capability, not a built one.** The engine is explicitly designed to gain them in a later slice, but as of today nothing in VoltEdge will create a work order on a timer. If you treat the system as "set a plan and forget it," assets will silently fall out of cycle. Treat it instead as a superb *execution and record* system whose *cadence you own* — §6.4 shows how to run that reliably.

### 6.4 Building a maintenance regime with today's tools

Here's a practical recipe that works now, entirely within what's built:

1. **Author the checklists once (Chapter 4).** For each asset class you maintain, create a scoped inspection template with items armed at sensible severities — `MV_SWGR_ANNUAL`, `DIST_XFMR_2YR`, `POLE_TOP_VISUAL`. This is a one-time design job you revisit only when criteria change (by versioning, §4.7).

2. **Write down your cadences — outside VoltEdge, for now.** Because the system stores no interval, keep the schedule where your team already plans: a maintenance calendar, a spreadsheet, a recurring reminder. *"Switchgear: annual, each Q3. Transformers: 2-yearly, alternating districts. Pole-tops: quarterly on the storm-exposed feeders."* This document is your source of truth for *when*; VoltEdge is your source of truth for *what happened*.

3. **Each cycle, create the work orders (Chapter 3).** When the calendar says a round is due, the planner creates the **inspection** (and **preventive**) work orders for that round — one per asset, or a small set covering a batch — assigns them to crews, and sets a **scheduled-for** date. Assigning at creation lands them straight on the crews' plates (**assigned**), and the board's **Unassigned** KPI stays at zero if you've assigned them all.

4. **Crews execute (Chapter 5).** Field crews work their **"Mine"** view, record an inspection against each asset **fulfilling** its work order (which completes the order), and let the armed-fail automation raise defects for anything wrong. Preventive orders they simply **Start** and **Complete** with a note.

5. **Triage and correct (Chapters 2–3).** The planner works the fresh defects in the inbox, **raises corrective orders** for the ones needing a fix, schedules and assigns them, and **resolves** the defects once the corrective work completes. The cycle's findings are now tracked through to closure.

6. **Close the cycle and diarise the next.** When the round's orders are all completed and its defects resolved, the cycle is done — and, because nothing auto-recurs, the planner **sets the next reminder** (step 2's calendar) so the round runs again on cadence.

### 6.5 Tracking a cycle without a scheduler

You don't get a "maintenance dashboard," but the board's existing tools assemble one:

- **Filter by work type.** The underlying board can narrow to **inspection** or **preventive** orders, so "show me this quarter's inspection round" is a filtered view of the work-orders list.
- **Filter to open.** The **open** view (everything not completed or cancelled) is your "still outstanding this cycle" list — when it empties for the round's orders, the round is done.
- **Read the KPIs as progress.** During a round, **Active orders** is how much of the cycle remains, **In progress** is what crews are on right now, and **Unassigned** flags any round work nobody has picked up.
- **Crews use "Mine."** Each field worker's daily maintenance list is the board filtered to their own assigned orders.
- **Use scheduled-for / due-by as your dates.** Set **scheduled-for** to the planned visit date so the board rows read in date context; use **due-by** (via the API/agent, §3.7) for a cycle deadline. Remember they don't *alert* — they *inform*.

### 6.6 Where maintenance meets the rest of VoltEdge

Maintenance doesn't run in isolation — it plugs into three neighbouring capabilities:

- **Asset tags (scan-to-inspect).** If your assets carry VoltEdge's scannable **asset tags**, a field crew can scan the QR/barcode on the physical asset to resolve it directly to its record — then open its **Works tab** and record the inspection against exactly the right feature, no id-hunting. Scanning is the fastest front door to the map way of logging defects and recording inspections. *(Asset tags are covered in the User Handbook.)*
- **The offline field pack.** A maintenance round in an area with no signal is exactly what the **field pack** is for — build a pack of the round's area, take it out, record inspections and log defects offline, and sync back (Chapter 7). The idempotency guarantees (§5.9) mean a round recorded in dead spots lands cleanly.
- **OMS outages feed the same backlog.** Defects raised from outages (source `outage`, §2.2) land in the *same* inbox your inspection rounds feed. So your maintenance triage naturally folds in fault-driven findings — an asset that failed in a storm and one flagged by a checklist are both just defects to work through.

### 6.7 A maintenance cycle, end to end — worked example

> *July — plan.* The team's maintenance calendar (kept in a shared spreadsheet, §6.4 step 2) says **Q3 switchgear inspections** are due — eight MV switches on Feeder 7. **Dana** creates eight **inspection** work orders from the `MV_SWGR_ANNUAL` checklist's intent, one per switch, each scheduled for a July week and assigned across two crews. She also raises three **preventive** orders — *"Grease ring-main isolators, SW-Feeder7-01/03/05"* — for the same trip. The board's **Active orders** KPI jumps by eleven; **Unassigned** stays at zero.
>
> *July — execute.* **Femi** works his **"Mine"** view: at each switch he records the annual inspection **fulfilling** that switch's work order (which completes it). Seven switches pass clean. At **SW-Feeder7-03**, item 2 (*oil weep*, armed high) fails — the inspection auto-raises **defect D-207** and completes the order with a *fail* note. His three preventive greasing orders he Starts and Completes on the same visit, each with a note.
>
> *July — triage & correct.* Dana's inbox has one new defect from the round (D-207) plus a couple from the wider network. She **raises corrective order W-88** from D-207 (§2.9), assigns it to Femi, schedules the reseal for the following week; Femi completes it; Dana **resolves D-207**. The round's one real finding is now fixed and on the record.
>
> *August — close & diarise.* Every Q3 inspection and preventive order shows **completed**; the round's defects are resolved. Dana marks the cycle done in the spreadsheet and **sets next July's reminder** — because VoltEdge won't. The record left behind: eight dated inspections, three preventive services, one corrective repair, all linked, all timestamped — the exact evidence a regulator or a reliability review asks for.
>
> Notice what carried the cycle: VoltEdge automated every *pass* through the loop flawlessly, but **Dana's calendar** carried the *recurrence*. That's the division of labour until recurring plans are built.

### 6.8 What changes when recurring plans arrive

So you can plan around it: the module is deliberately shaped to gain **recurring maintenance plans** in a future slice. When they land, the expectation is that a plan will hold a **cadence** (this checklist, this asset class, every N months) and **auto-generate** the inspection/preventive work orders when they fall due — replacing steps 2, 3, and 6 of §6.4's hand-driven recipe. Everything *else* in this chapter — the checklists, the execution, the auto-defect loop, the corrective follow-through, the records — stays exactly as it is today. In other words, adopting the manual regime now is not throwaway work: you're building the checklists and habits the automated version will simply *schedule* for you later. Until then, **own the cadence.**

### 6.9 Pitfalls & good habits

- **The system won't remind you — so something must.** Keep an external cadence document and diarise the next cycle every time you close one (§6.4). This is the single failure mode that silently drops assets out of maintenance.
- **Don't confuse a completed round with a scheduled next one.** Completing this cycle's orders does nothing about next cycle. Re-create them when due.
- **Scope templates and assign at creation.** A round of pre-assigned, class-scoped inspection orders is a round crews can just *do*; unscoped, unassigned ones create friction at every asset.
- **Let the rounds feed triage; don't shadow-track findings.** Everything a round finds is a defect in the one inbox — work it there, don't keep a side-list.
- **Use preventive vs corrective honestly.** Time-based servicing is **preventive**; fixing a specific found problem is **corrective** (raised from its defect). The distinction is what lets you later report proactive vs reactive effort.
- **Treat due-by as a plan, not an alarm.** It won't chase you; your cadence document does.

---

## Chapter 7 — In the field, offline

Everything so far has assumed a browser and a live connection. Real maintenance doesn't happen at a desk — it happens up a pole on a rural feeder with one bar of signal, or in a basement substation with none. This chapter is about doing Works **offline**: taking your defects, work orders, and checklists into the field on a phone, capturing findings with no connection, and syncing them back cleanly when you resurface. It's the longest and most step-by-step chapter, because the field is where the details bite.

The golden rule to hold in your head the whole way through: **the field app is a capture tool, not a second control room.** It's built to let a technician *do the work in front of them and record what they found* — not to plan, assign, or administer. Once you internalise that, every "can I do X offline?" answers itself.

### 7.1 The mental model: a pack and an outbox

Two pieces make offline work, and they pull in opposite directions:

- **The field pack** — a *snapshot* of the network (and now the Works context) frozen at the moment you build it, downloaded to the device as a local database. It's **read context**: the assets, the open defects on them, your assigned work orders, and the inspection checklists. Think of it as *what you take out with you.*
- **The outbox** — a local queue of everything you *do* in the field: defects logged, inspections recorded, work orders started and completed. Each action drops an entry in the outbox. Think of it as *what you bring back.*

When you get signal again, you **sync**: the outbox flushes up to the server, the server applies each action, and your captures become real records. Crucially, Works captures ride the **same** outbox and the **same** sync button as map edits — one flush pushes everything you did, whether you moved a pole or logged a defect. There is no separate "Works sync" to remember.

```
  BUILD PACK                 IN THE FIELD              SYNC BACK
  (online, at depot)         (offline, on site)        (online again)

  server ──snapshot──▶ [ pack DB ]      you tap ──▶ [ outbox ]  ──flush──▶ server
   assets              open defects       log defect               applies each op,
   your WOs            your work orders    record inspection        raises defects,
   checklists          checklists          start/complete WO        completes WOs
```

### 7.2 What actually rides in the pack

You don't get *all* of Works on the device — you get the **slice you'll plausibly need on this trip**, filtered when the pack is built. Concretely, a Works-enabled pack carries:

- **Open work — your open and work-ordered defects on the packed assets.** Only defects that are still live (`open` or `work_ordered`) and only on assets inside the pack's area come down. Resolved and dismissed defects stay behind — you're taking out the *to-do list*, not the archive.
- **Your own active work orders — *and your crews'*.** The non-terminal work orders **assigned to you** ride along, **plus** any order assigned to a **crew you're a member of** (§3.6): a crew-owned job fans out to every current member's pack. (Temporary members only receive it while their membership is valid.) You don't get the whole org's board, and a **subcontractor**-tasked order reaches *no* pack — only internal owners fan out. (This is the "capture tool" rule: the field app is your worklist, not the dispatch desk.)
- **The active inspection checklists.** Every active template and its items comes down in full, so you can run any round the trip calls for. Retired templates don't.

Two practical consequences of *when* this is decided:

- **It's frozen at build time.** The pack reflects the network and the worklist *as they were when you tapped "build."* If a defect is raised on one of your assets after you've left the depot, it won't be in your pack. Build the pack close to when you leave, not the night before a week-long trip.
- **Your read permission is frozen in too.** Whether a pack *includes* Works at all is decided by your `wam:read` permission at the moment of building. If you can't see Works in the browser, your pack won't carry it. (The build worker runs detached and can't re-check your permissions later, so it snapshots the answer up front — the same reason connectivity context is frozen at build.)

### 7.3 Getting a Works-enabled pack onto the phone — step by step

1. **Confirm you can see Works.** Open VoltEdge in a browser as yourself. If the **Works** tab appears on a feature panel and `/works` loads, you hold `wam:read` and your packs will include Works. If not, you need the permission first (Chapter 8) — no pack setting can conjure it.
2. **Build a pack over the area you're visiting.** Create a field pack covering the feeder/district for the trip (draw the bbox on the map or use the current view — see the field-pack flow in the User Handbook). Nothing extra to tick for Works: if you hold `wam:read`, the Works context is included automatically.
3. **Wait for the build, then download to the phone.** The pack manifest will show Works counts — how many defects, work orders, and checklists came down — so you can sanity-check before you leave signal. Zero work orders when you expected a full round is your cue that something's off (wrong area, or the orders weren't assigned to you yet).
4. **On the phone, open the pack, then tap "Work & assets."** The pack detail screen shows a **Work & assets** button beside the map button. It appears only when the pack actually contains Works context — if it's missing, this pack was built without `wam:read`, and you'll need a new one.

That's it — you're now holding the map and the Works worklist offline, side by side.

### 7.4 The three offline surfaces, tap by tap

The **Work & assets** screen has three tabs. Here's what each is for and how you actually drive it.

#### Surface 1 — the Work board (your job list)

Your assigned, non-terminal work orders, sorted so the urgent, in-progress ones float to the top. Tap a card to open it, and you get the actions that matter on site:

- **Start** — flips the order to *in progress* and stamps the start time. Tap it when you begin the job, so the record reflects reality.
- **Run inspection** — for an inspection-type order, launches the checklist runner (Surface 3) already pointed at this order's asset, so recording it will *complete* the order. This is the normal way to work an inspection round: open the order, tap Run inspection, fill the checklist.
- **Complete** — flips the order to *completed*, stamps the finish time, and prompts for a completion note. Tap it when the job's done.
- **Raise defect** — found something wrong while doing this job that's *not* what the order was about? Log a defect against the same asset without leaving the flow.

> There is **no "create work order" button offline.** Field crews *work* assigned orders; they don't dispatch new ones. If a job needs a follow-up order (a corrective repair, say), you log the **defect** for it in the field, and a planner raises the order from it back at base (Chapter 2). This is the "capture tool, not control room" rule made concrete.

#### Surface 2 — Defects (log what you find)

The defects tab lists the live defects that came down on your assets, and — more importantly — lets you **log new ones**. Two ways in:

- **From an asset (the normal way).** Tap the **+** button, then pick the asset: choose its class, then the specific feature, walking the pack's catalogue. Fill the form — **severity** (low/medium/high/critical), a **title** (required — make it specific: *"cracked bushing, phase B"* not *"broken"*), type, description, notes — and save. It drops into the outbox as a defect against that asset.
- **From a work order.** The **Raise defect** action on a job (Surface 1) is the same form, pre-pointed at that order's asset.

You can also **update a defect's status** in the field from the defects list (e.g. work_ordered / resolved / dismissed) via the status sheet — useful when you've fixed something small on the spot and want to resolve it there and then.

#### Surface 3 — Inspections (run the checklist)

Pick an inspection template and run it against an asset. Each item renders by its response type:

- **Pass/fail** items show pass/fail chips — tap one.
- **Rating / numeric** items show a number field — type the reading.
- **Text** items show a text box — type the observation.

Add a per-item note where it helps, an overall note at the end, and — if you launched the runner from a work order — leave the **"complete work order"** switch on so recording the inspection closes the job. Save.

Here's the payoff of doing checklists offline: **the auto-defect machinery still fires.** When you fail a pass/fail item that was *armed* with a fail-severity (Chapter 4), the app carries that arming, and on sync the **server auto-raises the defect** for it — exactly as it would online. You inspect in a dead spot; the defect for the crack you found materialises the moment you sync, no extra tapping. The overall pass/fail rolls up the same way too (any fail ⇒ fail).

### 7.5 What you can and can't do offline — the honest list

| You **can**, offline | You **cannot**, offline |
|---|---|
| Work your assigned orders — start, complete, note. | Create a brand-new work order (log a defect instead; a planner raises the order). |
| Log defects against packed assets (or by GPS — §7.8). | See or act on defects that weren't packed (resolved/dismissed, or on assets outside your area). |
| Record inspections from any active checklist. | Author or edit a checklist template (that's an online planning job). |
| Let armed fails auto-raise defects on sync. | Assign work to other people, or manage anyone else's board. |
| Update a defect's status; complete a fulfilling order. | See work orders assigned to other crews. |
| Attach photos to an asset's defect (§7.8). | Attach a photo to an *orphan* (asset-less) defect. |

The pattern is consistent: **capture and execute — yes; plan, assign, and administer — no.**

### 7.6 Syncing back: one button, everything flushes

When you have signal again, tap **sync** (top of the Work & assets screen — or the map screen; it's the same outbox). Here's what happens, and why it's safe:

1. **The outbox flushes in order.** Each action you took goes up as one operation. Feature edits and Works captures go up **together** — one sync, everything.
2. **The server applies each op to its real record.** A logged defect becomes a `wam.defects` row; a recorded inspection writes its results *and* raises any armed-fail defects *and* completes its work order if you asked; a started/completed order stamps its timestamps.
3. **Anything the server couldn't cleanly apply surfaces for attention.** The sync flow carries an attention/retry channel so a genuinely problematic op isn't silently lost — you'll see it flagged rather than have it vanish.

One nuance worth knowing: on the **map** screen, an asset with a pending *edit* tints amber so you know it's unsynced. A pending **Works capture does not** tint its asset — logging a defect on a pole doesn't make the pole look edited. Your unsynced Works captures show up in the pending sheet as their own friendly rows ("defect queued for sync"), not as asset edits. If you want to discard a Works capture before syncing, you do it from the **Work & assets** screen, not the map.

### 7.7 Why sync is replay-safe (idempotency, in plain terms)

This is the single most important reason you can trust offline capture, so it's worth understanding rather than taking on faith.

Networks in the field are flaky. A sync can start, push half your outbox, and drop before the server confirms — so the app, not knowing what landed, **sends the batch again.** Naively, that would double everything: two identical defects, an inspection that raised its defects twice. VoltEdge prevents this with **device-minted ids**.

- **Every record you create in the field is stamped with a unique id on the device, before it ever syncs.** That id travels with the op.
- **When the server receives a create it's already seen, it recognises the id and does nothing new** — it returns the existing record instead of making a second one. A defect logged once stays one defect, no matter how many times a dropped connection makes the app resend it.
- **Inspections get extra care.** Because recording an inspection has *side effects* (it raises defects), a replay can't just re-run and re-raise — so the server checks "have I already recorded this inspection id?" up front, and if so returns the existing one **without raising its defects a second time.** Fail an armed item once; get exactly one defect, even if the op replays.
- **Status updates and start/complete are naturally safe** — setting a defect to *resolved* twice, or completing an order twice, lands in the same place. Last write wins, which is fine for capture.

The practical upshot: **you never have to worry about tapping sync twice, losing signal mid-sync, or a retry.** Capture freely; sync as often as you like; the records stay clean.

### 7.8 Two field realities: orphan defects and photos

The field throws two things at you that a tidy asset catalogue doesn't anticipate.

- **You find a problem on something that isn't in the catalogue.** An un-inventoried pole, a stray bit of kit, damage with no asset record. You can still log it — as an **orphan defect** anchored by **GPS** instead of an asset. In the defect flow, choose **"No asset — report by location"**; the app captures your current coordinates (it needs a location fix before it'll let you save) and files the defect pinned to that point. Back at base it shows as a located-but-asset-less defect a planner can investigate. *Limitation:* because it has no asset, an orphan defect won't be re-packed into future field packs — it lives on the server; capture-and-forward.
- **A photo is worth a paragraph of description.** On an asset-anchored defect you can attach photos — tap the camera or gallery buttons on the defect form; each photo shows a pending/synced badge and uploads on sync (after the defect row lands, so it attaches cleanly). *Limitation:* photos attach to the defect's **asset**, so an **orphan** defect — which has no asset — **can't carry photos.** If you need a picture of an un-catalogued problem, note that in the description and follow up.

### 7.9 A dead-spot round, end to end

> **Femi** has a Q3 switchgear round on rural Feeder 7 — no signal past the third pole.
>
> *At the depot (online).* He confirms the **Works** tab loads in his browser (he holds `wam:read`), builds a **field pack** over Feeder 7, and checks the manifest: **8 assets, 8 work orders (his), 4 checklists.** Good — his round is on the device. He drives out.
>
> *On site (offline).* At each switch he opens **Work & assets → Work board**, taps the order, taps **Start**, then **Run inspection** — the `MV_SWGR_ANNUAL` checklist opens pointed at that switch. Seven switches: all pass/fail items pass, overall **pass**, "complete work order" left on, save. At **SW-Feeder7-03**, the *oil weep* item (armed high) **fails** — he taps fail, notes *"weep at gasket, north face,"* snaps two **photos**, overall rolls up to **fail**, saves. He also spots a **leaning stay wire on an un-mapped pole** nearby — logs an **orphan defect** by **GPS**, severity medium. His outbox now holds 8 inspections, 8 completes, 1 armed fail waiting to raise its defect, and 1 orphan defect.
>
> *Back in range.* Femi taps **sync** once. Everything flushes: eight inspections recorded and their orders completed; the SW-03 fail **auto-raises a high-severity defect** on the server, its two photos attached to the switch; the orphan stay-wire defect lands pinned to its coordinates. A dropped bar mid-sync makes the app resend — the device ids mean **nothing doubles.**
>
> *At base (online).* **Dana** opens `/works`: the round's one real finding (SW-03's oil weep) is a fresh **high** defect with photos; the orphan stay-wire defect is there to investigate. She **raises a corrective order** from the SW-03 defect, assigns it back to Femi. The dead-spot round is now indistinguishable, on the record, from one done at a desk.

### 7.10 Pitfalls & good habits

- **Build the pack close to departure.** It's a snapshot — a stale pack means missing recent defects and a stale worklist. Build it the morning you leave, over the right area.
- **Check the manifest counts before you lose signal.** Zero work orders when you expected a round means they weren't assigned to you, or you packed the wrong area. Catch it at the depot, not up the pole.
- **Make titles and notes specific.** *"Cracked bushing, phase B, hairline"* helps the planner triage; *"broken"* doesn't. You're the eyes on site — write like it.
- **Sync early, sync often, sync without fear.** Idempotency means extra syncs cost nothing. Flush whenever you catch signal so nothing rides home only on the device.
- **Log the defect, don't wait to make the order.** You can't create work orders offline by design — capture the finding as a defect and let a planner raise the order. Trying to "remember to make an order later" loses findings; a logged defect doesn't.
- **Photograph asset-anchored problems; describe orphan ones.** Orphans can't hold photos — compensate with a precise description and GPS.
- **Don't hunt for other crews' work.** Your pack is *your* worklist by design. If you need someone else's job, that's a dispatch decision made online, not a gap in the app.

---

## Chapter 8 — Permissions, territory & the agent

Every action in the previous seven chapters was gated by *who you are.* This chapter lays out the access model in full: the two permissions that govern the whole module, which roles hold them, and — the parts most guides skip — two honest sharp edges you need to know about. The first is that write access is *flat*: there's no separate "template author" permission, so a field technician can author checklists the whole organisation runs. The second is subtler and more important: **Works records are not territory-scoped the way the map is.** Read §8.5 carefully; it's a genuine trust-boundary fact, not a footnote. The chapter closes with the agent surface — driving the entire module from MCP tools.

### 8.1 Two permissions, one method-aware gate

The whole Works module is governed by exactly **two** permissions:

- **`wam:read`** — see Works: the board, the defects, the work orders, the checklists, recorded inspections. Every GET.
- **`wam:write`** — act on Works: log defects, create and update work orders, author templates, record inspections. Every mutating verb.

They're enforced by a single **method-aware gate** at the module's mount: read verbs (GET) require `wam:read`, mutating verbs (POST/PATCH) require `wam:write`. There is no per-route quirk — the gate is uniform across the whole `/wam` surface, and the browser UI, the field app, and the agent all pass through the same check. If you can GET it, you hold `wam:read`; if you can change it, you hold `wam:write`.

One consequence worth stating: **`wam:write` does not imply `wam:read`** as a matter of design, but in practice every role that's granted write is also granted read (§8.2), so the distinction rarely bites. The gate checks the *right* permission for the *verb*, so a hypothetical write-only actor could POST but not GET — an arrangement you won't meet in the shipped role set.

### 8.2 Who holds what — the role table

Two migrations seed the grants. `wam:read` goes to the office roles; `wam:write` goes to the roles that actually change things, *plus* the field-worker roles (so technicians can capture in the field, Chapter 7):

| Role | `wam:read` | `wam:write` | What they can do in Works |
|---|:---:|:---:|---|
| **Viewer** | ✓ | — | See the board, defects, orders, inspections. Read-only. |
| **Analyst** | ✓ | — | Same as viewer — read, report, analyse; no changes. |
| **Editor** | ✓ | ✓ | Full Works operator: log, order, template, inspect, resolve. |
| **Field roles** (technician) | ✓ | ✓ | Capture in the field: log defects, record inspections, work assigned orders. |
| **Admin** | ✓ | ✓ | Everything an editor can, org-wide. |
| **Break-glass admin** | ✓ | ✓ | Everything, overriding all other bounds (§8.6). |

Map it to the cast:

- **Priya** (viewer) — reads the board, can't touch it.
- **Tom** (analyst) — reads and reports, can't change a thing.
- **Dana** (editor) — the full maintenance-planner role: authors templates, creates and assigns orders, triages defects, records inspections.
- **Femi** (field technician) — holds `wam:write` through his field role: logs defects and records inspections on site, works his assigned orders.

### 8.3 What each permission actually unlocks

Reading the table as capabilities rather than roles:

| Capability | Needs | Notes |
|---|---|---|
| See the `/works` board & Works tab | `wam:read` | The tab only renders when you hold read. |
| Log a defect (from asset, orphan, or agent) | `wam:write` | — |
| Set a defect's status (resolve/dismiss/reopen) | `wam:write` | — |
| Create / update / start / complete a work order | `wam:write` | Includes assign and reschedule. |
| Raise a corrective order from a defect | `wam:write` | The atomic composite (Chapter 2). |
| Author or retire an inspection template | `wam:write` | **Flat** — see §8.4. |
| Record an inspection (and auto-raise its defects) | `wam:write` | The composite write (Chapter 5). |
| Be *assignable* to a work order | `wam:write` | The assignee picker lists active `wam:write` holders only — you can only assign work to someone who can actually do and close it. |

That last row is a nice built-in guardrail: because the assignee list is exactly the set of `wam:write` holders, you can never assign a job to someone who lacks the permission to complete it.

### 8.4 The first sharp edge: write is flat (anyone who writes can author templates)

Here's the honest caveat we've flagged since Chapter 1. `wam:write` is a **single, flat permission.** There is no finer-grained "template author" or "maintenance planner" permission underneath it. That means:

> **Anyone who can write to Works can create — and retire — inspection templates that the entire organisation runs.** A field technician holding `wam:write` for the perfectly good reason that they log defects on site *also* has the technical ability to author an org-wide checklist, or to retire one in active use.

Why it's built this way: simplicity. WAM ships with a two-permission model rather than a sprawling matrix, on the bet that *who can act* is a cleaner line than *who can act on which sub-thing.* For most deployments that's the right trade — a small maintenance team where everyone with write access is trusted to curate checklists.

Why it's worth knowing anyway, and how to live with it safely:

- **The blast radius is bounded by design.** Templates can't be *deleted*, only **retired and restored** (Chapter 4). A mistaken or malicious template edit can't destroy history — worst case it clutters the picker until someone retires it. And templates are *immutable once used*: recorded inspections snapshot their items, so re-pointing a template never rewrites past inspections.
- **Manage it socially and by process, not by permission.** Agree who curates the template library (your Danas, not your Femis), and treat "don't author org-wide checklists" as a team norm. The system won't stop a field tech, so your process should.
- **Watch the picker.** Because retiring is the only cleanup, an unexpected template appearing in the list is your signal to check who added it and retire it if it's stray.
- **If your deployment genuinely needs template authorship locked down,** that's a permission-model change (a distinct `wam:templates:write`, say) — not something you can configure today. Flag it as a requirement rather than assuming a setting exists.

### 8.5 The second sharp edge: Works is *not* territory-scoped

This is the most important paragraph in the chapter, because it runs counter to what you'd reasonably assume from the rest of VoltEdge.

The map is **spatially access-controlled** (Phase 58): a user only sees and edits features inside the territories granted to them, fail-closed. It would be natural to assume Works inherits that — that a user only sees defects and orders in their patch. **It does not.** The Works module (the `wam` schema and its engine) carries **no territory enforcement of its own.** A user with `wam:read` sees the **entire** defect and work-order board, org-wide, regardless of which territories they're granted on the map. A user with `wam:write` can act on **any** record on that board, wherever the asset sits.

Territory bounds reach Works only **indirectly**, through two doors that happen to be spatial:

- **The feature-panel Works tab is as scoped as the feature.** You reach it *through* an asset on the map, and you can only open assets in your territory — so that door is spatially bounded by the asset, not by Works.
- **The field pack is area-filtered.** A pack only carries defects on the assets it packed, over the area you built (Chapter 7) — so the *offline* view is bounded by the pack's geography.

But the **`/works` board and the agent tools go straight at the `wam` schema** and are bounded by *neither.* Open `/works` and you see every defect in the system; call `wam_list_defects` and you get them all.

What this means in practice:

- **Treat `wam:read` as an organisation-wide grant.** Give it to someone and they can see the whole Works picture — every asset's defects and every crew's orders — not just their region's. That may be exactly what you want for a central maintenance team; just don't grant it assuming it's regionally fenced.
- **The board is a shared, global surface.** Two regional planners on the same system see the *same* board. Use `work_type`, asset, and assignee filters (and "Mine") to carve out your slice — the data isn't partitioned for you.
- **Don't lean on Works permissions as a spatial boundary.** If regional data isolation matters for your deployment, the map enforces it; Works does not. Design around that, and if Works-level territory scoping is a hard requirement, raise it as a gap rather than assuming it's there.

*(If you're wondering why: WAM records are* operational *records — a problem found, a job to do — not GIS features, so they deliberately don't run through the map's feature-scoping path. The trade-off is a simpler model at the cost of a global board. Knowing the trade-off is the point of this section.)*

### 8.6 Break-glass admin

The **break-glass admin** sits above all of it. It overrides every permission and every bound — read and write on all of Works, unconditionally. There's nothing Works-specific to configure for it: because Works has no territory scoping to bypass and its gate is the two flat permissions, break-glass simply satisfies both and sees everything. Its use is an audited, emergency-access affair covered in the Admin & Break-Glass guide; for Works, just know it exists and is total.

### 8.7 Driving Works from the agent (MCP)

Everything you can do in the browser, you can do through the **agent** — VoltEdge exposes the whole module as **17 MCP tools**, one thin adapter per engine function, hitting the *same* engine the `/works` board and REST routes use. Nothing about the agent is a side door: **it acts as you, with your permissions,** so the `wam:read`/`wam:write` gate (and everything in §8.4–8.5) applies identically. An agent running as Priya can list but not change; as Dana, it can do the lot.

The tools split into reads and writes:

**Reads (9, require `wam:read`, tagged read-only):**

| Tool | Does |
|---|---|
| `wam_list_defects` | The defect triage board, with optional status/severity/asset/open filters. |
| `wam_get_defect` | One defect in full (incl. its point if it's an orphan). |
| `wam_list_work_orders` | The work board, filterable by status/type/priority/asset/assignee/open. |
| `wam_get_work_order` | One order in full. |
| `wam_list_assignable_users` | The assignee picker — active `wam:write` holders. |
| `wam_list_templates` | Checklist templates (optionally active-only). |
| `wam_get_template` | One template with its ordered items. |
| `wam_list_inspections` | Recorded inspections (headers), filterable. |
| `wam_get_inspection` | One inspection with its item results and any auto-raised defect ids. |

**Writes (8, require `wam:write`, annotated so a client confirms with a human first):**

| Tool | Does |
|---|---|
| `wam_create_defect` | Log a defect (asset-anchored or orphan-by-GPS). |
| `wam_set_defect_status` | Resolve / dismiss / reopen a defect (idempotent). |
| `wam_create_work_order` | Create an order (auto-advances status from assignee/schedule). |
| `wam_update_work_order` | Sparse patch — start/complete/reassign/reschedule (timestamps stay consistent). |
| `wam_raise_work_order_from_defect` | The atomic corrective composite — order + flip defect. |
| `wam_create_template` | Author a checklist with its items (arm items via `fail_severity`). |
| `wam_set_template_active` | Retire / restore a template (idempotent). |
| `wam_record_inspection` | The composite — record results, auto-raise armed-fail defects, optionally complete the order. |

Two things make the agent surface trustworthy:

- **Reads are marked read-only; writes are marked as writes.** Every mutating tool carries annotations (a human-readable title, and hints that it writes, isn't idempotent where it isn't, and touches only this system) so a well-behaved MCP client **pauses for human confirmation before calling it.** The description of each write literally says "confirm with a human before calling."
- **The engine validates regardless of caller.** The same rules from Chapters 2–5 (title required, asset-or-orphan anchor, terminal-defect rejection, one-defect-per-armed-fail) are enforced in the engine, so an agent can't fast-talk its way past them — a bad call comes back as an error, not a corrupt record.

### 8.8 A board triage, run through the agent — worked example

> **Dana** asks the assistant: *"What high or critical defects are still open, and which have no work order yet?"* The agent calls `wam_list_defects` with `severity: high` and `open: true`, then again for `critical` — a **read**, so it runs without a confirmation prompt (Dana holds `wam:read`). It comes back with four defects, two of them (both `open`, not `work_ordered`) lacking an order.
>
> Dana says: *"Raise corrective orders for those two, assign them to Femi, due Friday."* The agent looks up Femi via `wam_list_assignable_users`, then proposes two `wam_raise_work_order_from_defect` calls — **writes**, so the client shows Dana each one to **confirm** before it fires. She approves; two corrective orders are raised, the two defects flip to `work_ordered`, both assigned to Femi with Friday due dates.
>
> Had **Priya** (viewer) asked the same second question, the agent would have *read* the board fine but hit a hard permission error the moment it tried `wam_raise_work_order_from_defect` — she lacks `wam:write`. The agent enforces exactly the boundary the UI does, because it's the same gate.

### 8.9 Pitfalls & good habits

- **Grant `wam:read` knowing it's global.** It shows the whole Works picture, not a regional slice. That's fine for a central team — just don't grant it expecting territory fencing.
- **Curate templates by convention.** Write is flat, so decide *socially* who authors checklists and keep field techs out of the library, even though the system would let them in.
- **Assign only to `wam:write` holders — the picker already ensures it.** If someone's missing from the assignee list, they lack write; fix the role, don't work around it.
- **Don't use Works as a data-isolation boundary.** The map isolates by territory; Works doesn't. If isolation matters, enforce it on the map and treat the Works board as shared.
- **Let the agent confirm writes.** The write tools are annotated to prompt for human approval — keep that on; it's the difference between "the agent drafted two orders for you to approve" and "the agent changed the board."
- **Break-glass is total and audited — reach for it only in emergencies**, and expect the trail.

---

## Chapter 9 — Appendix: reference tables & cheat sheet

The previous eight chapters taught the module in prose. This one is the lookup you keep open in another tab: every enumerated value, the lifecycles, the derivation rules the engine applies for you, the permission grid, and the full REST / offline / agent surfaces — followed by a one-page "how do I…" cheat sheet. Nothing new is introduced; everything here was explained earlier and is gathered so you don't have to hunt.

### 9.1 The three record types at a glance

| Record | What it is | Anchored by | Terminal states |
|---|---|---|---|
| **Defect** | A problem found on an asset. | An **asset** (class + id) **or** a location (orphan, by point) — never a partial of either. | `resolved`, `dismissed` |
| **Work order** | A job to be done. | Optionally an asset and/or a location and/or a defect — all optional. | `completed`, `cancelled` |
| **Inspection** | A recorded execution of a checklist. | An **asset** (class + id), required. | (none — an inspection is a point-in-time record) |

### 9.2 Enumerated values (every one)

**Defect — severity** (default `medium`):

| Value | Use for |
|---|---|
| `low` | Cosmetic / minor, no urgency. |
| `medium` | Standard defect (the default). |
| `high` | Serious, needs prompt attention. |
| `critical` | Safety / imminent-failure. |

**Defect — source** (default `ad_hoc`):

| Value | Meaning |
|---|---|
| `inspection` | Auto-raised by a failing armed checklist item. |
| `outage` | Raised from an OMS incident. |
| `condition_monitoring` | Flagged by monitoring/telemetry. |
| `ad_hoc` | Logged directly by a person (the default). |

**Defect — status:** `open` → `work_ordered` → `resolved` / `dismissed` (reopenable back to `open`). Moving to a terminal state stamps `resolved_at`; reopening clears it.

**Work order — work_type** (required, no default):

| Value | Use for |
|---|---|
| `inspection` | Running a checklist round. |
| `corrective` | Fixing a specific found problem (usually raised from a defect). |
| `preventive` | Time-based servicing not tied to a defect. |
| `construction` | New build / installation work. |

**Work order — status:** `draft` → `scheduled` → `assigned` → `in_progress` → `completed` / `cancelled`. (The order isn't a rigid march — creation can jump you straight to `assigned` or `scheduled`; see §9.4.)

**Work order — priority** (default `medium`): `low`, `medium`, `high`, `urgent`.

**Inspection — item response_type** (default `pass_fail`):

| Value | Answered with |
|---|---|
| `pass_fail` | A pass or a fail (the only type that can auto-raise a defect). |
| `rating` | A number on a scale. |
| `numeric` | A measured reading. |
| `text` | A free-text observation. |

**Inspection — overall_result:** `pass`, `fail`, `conditional` (rolls up from items — any fail ⇒ `fail` — unless overridden).

**Item — fail_severity** (optional): `low` / `medium` / `high` / `critical`. When set, a failing `pass_fail` answer **arms an auto-defect** at that severity. Omit it for an informational item that records but never raises.

### 9.3 Lifecycles

**Defect:**

```
        ┌─────────────────────────────────────┐
        │                                     reopen
        ▼                                       │
   ●─▶ open ──raise work order──▶ work_ordered ─┤
        │                             │         │
        ├──────────resolve────────────┴──▶ resolved ──┘
        └──────────dismiss───────────────▶ dismissed
```

**Work order:**

```
   ●─▶ draft ──▶ scheduled ──▶ assigned ──▶ in_progress ──▶ completed
        │            │            │                          
        └────────────┴────────────┴──────────────────────▶ cancelled
   (creation may enter at draft / scheduled / assigned — see §9.4)
```

**The closed loop (the whole module in one picture):**

```
   inspection round ──armed fail──▶ defect ──raise──▶ corrective order ──complete──▶ resolve defect
        ▲                             ▲                                                   
        │                             │                                                   
   preventive order            outage / ad-hoc / condition monitoring                     
```

### 9.4 What the engine derives for you (so you don't set it by hand)

| The engine does this | Rule |
|---|---|
| **Auto-advances a new order's status** | Naming an **assignee** at creation ⇒ `assigned`; naming a **schedule** (not an assignee) ⇒ `scheduled`; neither ⇒ `draft`. |
| **Stamps lifecycle timestamps** | Status → `in_progress` stamps `started_at`; → `completed` stamps `completed_at`. Set once, preserved thereafter (COALESCE). |
| **Derives corrective priority from defect severity** | On *raise-from-defect*: `critical` → `urgent`, `high` → `high`, `low` → `low`, anything else → `medium`. |
| **Titles a raised corrective order** | Defaults to `"Repair: <defect title>"` unless you override. |
| **Rolls up an inspection's overall result** | `fail` if any item failed, else `pass` (override to `conditional` if needed). |
| **Auto-raises defects from an inspection** | Every failing `pass_fail` item that carries a `fail_severity` → one linked defect (`source = inspection`) at that severity. |
| **Completes a work order from its inspection** | When `complete_work_order` is set, recording the inspection completes the linked order in the same transaction, note `"Inspection recorded: <overall>"`. |
| **Flips the defect on raise** | Raising a corrective order flips its defect `open` → `work_ordered`, atomically. |
| **Rejects a raise from a terminal defect** | A `resolved`/`dismissed` defect can't spawn an order — reopen it first. |

### 9.5 Permission grid

| Action | `wam:read` | `wam:write` |
|---|:---:|:---:|
| See board / defects / orders / inspections / templates | ✓ | ✓ |
| Log a defect · set defect status | — | ✓ |
| Create / update / start / complete / cancel a work order | — | ✓ |
| Raise a corrective order from a defect | — | ✓ |
| Author / retire / restore an inspection template | — | ✓ |
| Record an inspection | — | ✓ |
| Be listed as an assignable user | — | ✓ (the picker = active `wam:write` holders) |

**Roles:** `wam:read` → viewer, analyst, editor, admin, break-glass. `wam:write` → editor, field roles, admin, break-glass.
**Two things to remember:** write is **flat** (no separate template-author permission), and Works is **not territory-scoped** — `wam:read` sees the whole board org-wide (see Chapter 8).

### 9.6 REST surface

Mounted under `/wam`, gated `RequirePermission(wam:read, wam:write)` — GET reads, POST/PATCH writes.

| Method & path | Does |
|---|---|
| `GET /wam/defects` | List defects (filters: status, severity, asset, open). |
| `GET /wam/defects/{id}` | One defect. |
| `POST /wam/defects` | Log a defect. |
| `POST /wam/defects/{id}/status` | Transition a defect's status. |
| `POST /wam/defects/{id}/work-order` | Raise a corrective order + flip the defect (atomic). |
| `GET /wam/work-orders` | List orders (filters: status, type, priority, asset, assignee, **crew_id**, **subcontractor_id**, **incident_id**, open). |
| `GET /wam/work-orders/{id}` | One order. |
| `POST /wam/work-orders` | Create an order (owner: `assigned_to_user_id` / `crew_id` / `subcontractor_id` / `assigned_to`). |
| `PATCH /wam/work-orders/{id}` | Sparse-patch an order (start/complete/reassign/reschedule; empty owner = unassign). |
| `GET /wam/assignable-users` | Users an order can be assigned to (`wam:write` holders). |
| `GET`/`POST /wam/subcontractors` · `PATCH /{id}` · `POST /{id}/active` | The subcontractor registry — external firms work is tasked to (§3.6). |
| `GET /wam/inspection-templates` | List templates (optionally active-only). |
| `GET /wam/inspection-templates/{id}` | One template + items. |
| `POST /wam/inspection-templates` | Create a template + items. |
| `POST /wam/inspection-templates/{id}/active` | Retire / restore a template. |
| `GET /wam/inspections` | List recorded inspections. |
| `GET /wam/inspections/{id}` | One inspection + item results. |
| `POST /wam/inspections` | Record an inspection (composite). |

### 9.7 Offline sync operations (field app)

Works captures ride the **shared `/sync` outbox** (no separate Works sync). Four op kinds; create-ops carry a device-minted UUID for replay-safe idempotency (Chapter 7):

| Op kind | Captures | Idempotency |
|---|---|---|
| `wam_defect_create` | A logged defect. | Device UUID → `ON CONFLICT (id) DO NOTHING`. |
| `wam_defect_status` | A defect status change. | Deterministic write on an existing id (naturally safe). |
| `wam_workorder_update` | Start / complete / patch an order. | Deterministic write on an existing id. |
| `wam_inspection_record` | A recorded inspection (+ its auto-defects). | Device UUID → existence-check up front (so a replay doesn't re-raise defects). |

There is **no** offline work-order *create* op — field crews work assigned orders and log defects; planners raise orders online.

### 9.8 Agent (MCP) tools — 17 total

Reads (require `wam:read`, read-only): `wam_list_defects`, `wam_get_defect`, `wam_list_work_orders`, `wam_get_work_order`, `wam_list_assignable_users`, `wam_list_templates`, `wam_get_template`, `wam_list_inspections`, `wam_get_inspection`.

Writes (require `wam:write`, confirm-with-human annotated): `wam_create_defect`, `wam_set_defect_status`, `wam_create_work_order`, `wam_update_work_order`, `wam_raise_work_order_from_defect`, `wam_create_template`, `wam_set_template_active`, `wam_record_inspection`.

They hit the **same engine** as the UI and REST, act **as you** (your permissions apply), and validate identically — the agent is not a side door. (Full descriptions in Chapter 8.)

### 9.9 "How do I…" — one-page cheat sheet

| I want to… | Do this |
|---|---|
| Record a problem on an asset | Feature panel → **Works** tab → **Log defect**; or `/works` → Log defect; or the agent's `wam_create_defect`. |
| Record a problem on something not in the catalogue | Field app → defect → **"No asset — report by location"** (captures GPS). Orphan defects can't hold photos. |
| Turn a defect into a job | Defect's **⋯ → Raise work order** (or the quick **Raise** button when it's `open`). Priority derives from severity. |
| Create a job not tied to a defect | `/works` → create a **work order**; pick `preventive` (servicing) or `construction` (new build). |
| Assign a job | Set an **assignee** on create or via patch — the picker lists only people who can complete it. |
| Start / finish a job | Open the order → **Start** (stamps start) / **Complete** (stamps finish, prompts for a note). |
| Build a reusable checklist | `/works` → Inspections → **New template**; arm items with a `fail_severity` to auto-raise defects. |
| Fix a bad checklist | You can't edit — **retire** it and create a corrected one (immutable by design). |
| Run a checklist | `/works` → Inspections → **Record inspection**, or open an inspection order → **Run inspection** (completes the order). |
| Auto-log the failures | Just fail the armed items — the record raises one linked defect per armed fail on save/sync. |
| Work with no signal | Build a **field pack** (needs `wam:read`) → **Work & assets** → capture offline → **sync** back (one button, replay-safe). |
| Run a maintenance cycle | Create the round's orders each period (there's **no auto-recurrence** — you own the cadence), execute, triage, close, **diarise the next**. |
| See only my work | `/works` → **Mine**; or filter work orders by yourself as assignee. |
| Drive it from the assistant | Use the `wam_*` MCP tools — reads run free, writes ask you to confirm. |

### 9.10 Watch-outs, collected

- **Maintenance has no scheduler.** Recurring/auto-generated plans are not built — cadence is yours to drive (Chapter 6).
- **Templates are immutable and delete-proof.** Edit = retire + recreate; cleanup = retire, not delete (Chapter 4).
- **Write is flat.** Anyone with `wam:write` — field techs included — can author org-wide templates. Curate by convention (Chapter 8).
- **Works is not territory-scoped.** `wam:read` sees the whole board; don't use Works as a data-isolation boundary (Chapter 8).
- **Orphan defects carry no photos**, and won't re-pack into future field packs (Chapter 7).
- **No offline work-order create** — log the defect, raise the order online (Chapter 7).
- **A completed cycle doesn't schedule the next one** — re-create the round when due (Chapter 6).

---

*This is the final chapter. The manual covers the full Works / Inspections / Maintenance module end to end: the mental model (Ch 1), the three record types and their engines (Ch 2–3), checklists and their execution (Ch 4–5), running maintenance on a cycle (Ch 6), offline field work (Ch 7), permissions, territory and the agent (Ch 8), and this reference (Ch 9). For the neighbouring modules it touches — assets and the map, OMS outages, field packs, and break-glass admin — see the User Handbook, the Engineering Studies, and the Admin & Break-Glass guide.*
