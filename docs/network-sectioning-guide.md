# VoltEdge Feeders, Sections & Subnetworks Guide

How VoltEdge divides the network into named, meaningful pieces — **feeders**, **protection zones** and **switching sections** — and how to work with them. This is VoltEdge's deliberately **lean** take on the "subnetwork" idea: the useful 80% of what a heavyweight utility-network model gives you, without the ceremony.

**Who this is for.** Analysts and dispatchers who ask "what's on this feeder / in this section," editors who curate feeders, and admins who classify the devices that bound them.

**The cast.** **Dana** (editor) curates feeders; **Tom** (analyst) reads sections off traces and load flows; **Priya** (dispatcher) cares about isolation sections during an outage; **Ada** (admin) classifies the boundary devices. The running example is the seeded test network — transformer **TX-101** feeding **SP-A**/**SP-B** — plus a feeder off a substation circuit breaker.

**Legend.** 🔒 needs a permission (named inline). 💡 a tip, ⚠️ a caution. **▶** the "how to use it" lane, **∑** the "why / the model" lane.

---

## Chapter 1 — The one idea

A **subnetwork is just a named section of the larger network, defined by where it starts.** That's the whole concept. Everything below is which *kind* of section, and what marks its start and end.

VoltEdge builds sections the same way the network itself is built — by its **devices**. A section is the span between **boundary devices**, and the *kind* of device decides the *kind* of section. There's no separate "subnetwork" object to create and maintain; a section falls out of the topology plus one attribute on each class.

---

## Chapter 2 — The three kinds of section (∑ the grounded model)

A distribution feeder isn't one undifferentiated blob — it's carved up by its own devices, and **the kind of section depends on the kind of device**:

1. **The feeder** — everything on the load side of a **substation circuit breaker** (off the station bus). The top-level subnetwork.
2. **A protection zone** — the span a **protective device** (breaker, recloser, fuse) clears on a fault, bounded by the *next downstream protective device*. Answers *"what de-energises when this device trips?"*
3. **A switching / isolation section** — the span between **switching devices** (disconnectors, load-break switches, sectionalisers, ties), bounded by the next switch *in any state*. Answers *"what can I isolate to work on it?"*

These are **nested**: feeder ⊃ protection zones ⊃ switching sections. And — the elegant part — **LV mirrors MV exactly**:

| Role in the (sub)network | MV | LV |
| --- | --- | --- |
| Originates the (sub)network | station **circuit breaker** off the bus | **distribution transformer** secondary |
| Protective section boundary | recloser, line fuse, breaker | **feeder-pillar fuse**, service cut-out |
| Switching section boundary | load-break switch, isolator, sectionaliser, tie | LV switch, link box |

So the transformer *is* the LV's "station breaker," and the pillar fuses *are* the LV's downstream protective devices. Same grammar, two voltages.

---

## Chapter 3 — The backbone: `boundary_role` 🔒

The whole model rests on one class-level attribute, **`boundary_role`** (migration 0129), that classifies what each device *does* to the network:

- **`source`** — originates a (sub)network at its load side (a generator; a grid infeed).
- **`protective`** — interrupts fault current, carving a **protection zone** (breaker, recloser, fuse, cut-out).
- **`switching`** — isolates or transfers without being primarily protective, carving a **switching section** (disconnector, isolator, load-break switch, sectionaliser, tie, link box).
- **`transformer`** — a voltage boundary that originates the next-voltage network at its secondary.
- *(unset)* — not a boundary (conductors, busbars, meters, poles).

💡 This is the one piece of structure worth adding, and it's electrically honest: it's finer than lumping every switch together, because a fault-interrupting recloser and a manual isolator create *different kinds* of section. 🔒 Ada sets it per class on the admin feature-class screens (it's metadata, like `functional_role`); most deployments inherit the seeded defaults (Appendix A) and never touch it.

⚠️ **The feeder head is per-instance, not per-class.** Whether a given breaker is *the* station feeder breaker is a placement fact (it sits off the substation bus), not a property of the breaker class — a mid-feeder breaker is still a `protective` boundary. VoltEdge models the feeder head as a **circuit's source** (Chapter 4), not via `boundary_role`.

---

## Chapter 4 — Feeders are circuits ▶ 🔒

A **feeder is a circuit** — the named, source-bounded subnetwork VoltEdge already has (migration 0062). You don't build a separate "feeder" thing.

**▶ Create and populate one** (🔒 `circuits:write` + `features:write`):
1. **Admin → Circuits** → create a circuit (name, MV/LV network, optional voltage).
2. Set its **source feature** — the station circuit breaker (or the transformer secondary for an LV feeder). That's the feeder head / subnetwork controller.
3. **Assign by trace** — VoltEdge traces downstream from the source and **stamps the circuit onto every feature it reaches** (the `circuit_ref` label). Every conductor, switch and device on the feeder now knows which feeder it belongs to — shown on the map and in every feature's panel.

**Freshness (the one bit of "is it stale?").** After you assign, the network keeps changing — a cable is edited, a device moved. VoltEdge flags when a circuit's stamped membership may be out of date: the **Circuits** list shows a **"stale — re-assign"** badge when a member feature has been edited since the last assign (migration 0130). Re-assign to refresh.

⚠️ The stale check catches member **edits, moves and deletes**; it does *not* catch a pure connect/disconnect that touches no existing member (e.g. closing a tie that pulls brand-new features onto the feeder). Re-assigning is always safe, so when in doubt, re-assign.

---

## Chapter 5 — A feature's section, in results ▶

You don't have to label sections manually — VoltEdge **derives each feature's section on the fly** from the result you're already looking at. Run any trace, or a load flow, and each row knows its section:

- **In a trace result**, each feature carries a small **§ section** tag — its nearest upstream boundary device (the section head). So you read *"MV-2 · § past SW-101"* at a glance.
- **In a load flow**, the results table gains a **Section** column showing the same section head per node.

**∑ How it's derived (and why it's free).** Every trace and load-flow result already carries a parent tree (each feature's one-hop-upstream neighbour). To find a feature's section, VoltEdge climbs that tree to the nearest device whose class has a `boundary_role` — no extra queries, nothing stored on the feature, and always correct for the current switching state. A boundary device heads its own section.

💡 Because it's derived, there's no "section membership" to go stale and no re-assign step — unlike feeders (circuits), which *are* stamped. Sections are a read-time concept; feeders are a stored one.

---

## Chapter 6 — Highlighting a section on the map ▶

To *see* a section on the map, select a feature and open its **actions menu** (the ⌄ in the panel header). Two choices, because the two section kinds answer different questions:

- **Switching section** — the span bounded by any isolating device (protective + switching) and the transformer. *"What would I isolate to work on this?"*
- **Protection zone** — the span bounded only by fault-interrupting protective devices (and the transformer). *"What de-energises if a fault here trips the upstream device?"* This stops at *fewer* devices — a plain switch doesn't bound a protection zone.

Either one highlights the section on the map, with its upstream and downstream bounding devices marked. 💡 Priya uses **Switching section** to scope a planned outage ("who's between these two switches"); Tom uses **Protection zone** when reasoning about coordination.

---

## Chapter 7 — From an agent

The section trace is on the MCP surface too: **`run_section_trace`** returns a feature's section (its features + the bounding devices), and a **`protection_zone`** flag switches it between the switching section (default) and the protection zone. So an assistant can answer "what's in this section?" the same way the UI does.

---

## Chapter 8 — How this compares to a full utility-network model

VoltEdge intentionally does **not** clone the heavyweight apparatus (ArcGIS Utility Network's tiers, terminal-precise controllers, per-tier subnetwork-name fields, a derived subnetwork-line, a clean/dirty/invalid state machine, network-wide name-uniqueness). Here's the lean mapping:

| Heavyweight concept | VoltEdge's lean version |
| --- | --- |
| Domain network / tier definition | the `network` column (MV/LV) + `trace_tier` |
| Subnetwork / feeder | a **circuit** (source feature = the feeder head) |
| Subnetwork controller | the circuit's source feature (no terminal-level controllers) |
| Subnetwork **name** stamped on features | `circuit_ref` |
| Update Subnetwork | assign-by-trace |
| Section *kinds* | **`boundary_role`** (protective / switching / transformer / source) |
| Clean / dirty / invalid | one **timestamp** (`members_synced_at`) → "stale — re-assign" |
| Section membership | **derived at read time** (no stored section id) |

**What's deliberately out of scope** (so you know the edges):
- **No map tiles coloured by section.** Sections are shown on demand (a highlight) and in results, not as a persistent per-feature colour. (Addable later if a section-coloured overview is ever needed.)
- **No multi-tier labels on one feature** — a feature carries one feeder (`circuit_ref`), not simultaneously a feeder *and* an LV-network name.
- **No tiers table, subnetwork-line, terminal controllers, or validity state machine.**

The payoff: feeders, protection zones and switching sections are all usable today, with one new attribute, one timestamp, and the trace/circuit machinery that already existed.

---

## Appendix A — The `boundary_role` taxonomy

Seeded defaults (`seed/boundary_role_backfill.sql`); adjust per class on the admin feature-class screens.

| `boundary_role` | Bounds… | Default classes |
| --- | --- | --- |
| `source` | originates a (sub)network | `GENERATOR` |
| `protective` | protection zone (fault interruption) | `MV_BREAKER`, `MV_RECLOSER`, `MV_FUSE`, `SERVICE_CUTOUT`, `LV_FUSE` |
| `switching` | switching / isolation section | `MV_SWITCH`, `MV_ISOLATOR`, `LV_SWITCH`, `LV_LINK_BOX` |
| `transformer` | voltage boundary (next-voltage origin) | `DIST_XFMR`, `POWER_XFMR` |
| *(unset)* | not a boundary | conductors, busbars, meters, poles, service points, … |

Which role set each walk uses:
- **Isolation** (switches to open): `protective` + `switching` — *not* the transformer (you can't "open" a transformer).
- **Switching section**: `protective` + `switching` + `transformer` + `source`.
- **Protection zone**: `protective` + `transformer` + `source` (no plain switches).

## Appendix B — Where this is grounded

Backbone: `migrations/0129_feature_class_boundary_role.sql`, `seed/boundary_role_backfill.sql`. Feeders/freshness: `api/internal/circuit` (+ `migrations/0130_circuit_members_synced_at.sql`). Section walk: `api/internal/trace/section.go`, `api/internal/trace/class_walk.go` (the `boundary_role` sets). Result display: `web/src/lib/network-section.ts` (client-side derivation), `web/src/components/map/trace-result-table.tsx`, `web/src/components/map/load-flow-panel.tsx`. Pairs with the [LV Network & Metering](lv-and-metering-guide.md) guide (which the sections run through) and the [Feature Catalog](feature-catalog.md).
