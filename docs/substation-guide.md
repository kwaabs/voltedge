# The VoltEdge Substation Guide

*How a substation is modelled in VoltEdge, and how to model one yourself.*

---

## How to read this guide

This is the sixth VoltEdge manual, and it is deliberately two books braided into
one. Some readers arrive holding a survey sheet and a question — *"I have a
33/11 kV primary to capture; where do I start?"* Others arrive holding the seed
files and a different question — *"why is the busbar a junction and the station
a polygon, and what happens if I add a new class?"* Rather than split those into
a how-to and a reference that drift out of sync, this guide answers both at once,
chapter by chapter:

- **The ▶ operator lane** — the step-by-step, "click here, place that, wire this"
  path for building a real station in the app. If you only want to *use*
  VoltEdge to capture and operate substations, you can read just these.
- **The ∑ model lane** — the "why it is shaped this way" reference for each class,
  terminal, rule and relationship, grounded in the actual seed files under
  `/seed`. If you extend the metadata model, or you need to justify a modelling
  choice to a colleague, these are for you.

Both lanes share one running example — **Adenta Primary**, a 33/11 kV primary
substation we build up from an empty map to a fully traceable, load-flowable,
CIM-exportable station over the course of the guide. Every abstract point lands
on a concrete piece of Adenta Primary.

This guide assumes you have met the core VoltEdge concepts (feature classes,
subtypes, terminals, the map, the feature panel) in the
[user handbook](user-handbook.md). Where a topic has its own deep manual — the
engineering analyses, break-glass administration, works & maintenance — this
guide points to it rather than repeating it.

### The cast

The recurring VoltEdge characters continue, and one joins for this guide:

| Who | Role | Shows up when |
| --- | --- | --- |
| **Kwame** | *Substation / primary-plant design engineer* (**new**) | Designing and capturing the station itself — the driver of most chapters |
| **Dana** | Network editor & planner | Wiring connectivity, containment, one-line diagrams |
| **Tom** | Network analyst | Tracing, load flow, fault, energization through the station |
| **Priya** | Read-only viewer / control-room operator | Reading the finished station, operating switches |
| **Femi** | Field technician | Capturing plant on site, offline, on the field app |

---

## What this guide covers

> **Reading path.** The guide is written to be read in order — each chapter builds
> the station a little further and assumes the ones before it. If you are here for
> one specific thing, the chapter titles below are the index.

1. **What a substation *is* in VoltEdge** — the mental model: the `SUBSTATION`
   container, the two orthogonal graphs (containment vs. connectivity), why the
   model is metadata-driven, and the Adenta Primary example we build throughout.
2. **The yard** — the primary plant classes: `POWER_XFMR`, `MV_BUSBAR`, the
   switching devices, `EARTHING_XFMR`. Their terminals, fields and subtypes.
3. **The control room** — `SUBSTATION_PANEL` and the protection-&-control
   secondary layer: `STATION_BATTERY`, `BATTERY_CHARGER`, `CONTROL_IED`,
   `ANNUNCIATOR`. Why they are geometry-less and network-less.
4. **The LVAC layer** — the station's own AC services: `AUX_TRANSFORMER` (the
   station-service transformer) and `LVAC_BOARD`. The most recent piece of the
   model, and the one that makes "what feeds the chargers?" answerable.
5. **The connectivity graph** — terminals, `network_rules`, and wiring a station
   end to end so a trace and a load flow can walk it.
6. **The containment graph** — `SUBSTATION contains …`, why it is deliberately
   independent of connectivity, and who actually consumes it (CIM export).
7. **The one-line diagram** — turning the geographic station into a schematic:
   diagram containers, placeable classes, and the per-diagram allow-list.
8. **Making it live** — trace, load flow, fault infeed and
   energization/de-energization *through* a station, including the station-service
   transformer and LVAC board.
9. **Building Adenta Primary end to end** — a single uninterrupted walkthrough,
   then checklists and a reference appendix (class / terminal / rule / CIM tables).

Chapters are delivered one per turn. Say **next** when you want the following one.

---

## Chapter 1 — What a substation *is* in VoltEdge

### 1.1 The trap of the word "substation"

Kwame has designed substations for fifteen years. When he opens VoltEdge for the
first time and types "substation" into the class list, he expects to find *the*
substation object — one thing, with a rating, a voltage, a fence, a list of bays,
a schematic. He finds a class called `SUBSTATION`, places one, and is briefly
disappointed: it is just a **polygon**. It has no transformers inside it, no
switchgear, no voltage. It is, apparently, a coloured boundary on the map.

This is the single most important thing to understand before you model a station
in VoltEdge, so we will say it plainly:

> **In VoltEdge, a substation is not one object. It is a *place* plus a
> *population* of equipment, tied together by relationships — and the equipment,
> not the boundary, is where the electrical reality lives.**

The `SUBSTATION` polygon is the *place*: a fenced site with a name, a footprint,
and an operator. Everything that makes it a working substation — the transformers
that step voltage down, the busbars that distribute it, the switches that isolate
it, the panels that protect it, the batteries that outlive a grid failure — are
**separate features**, each its own class, each with its own identity, that
happen to *live inside* that polygon and *wire together* electrically.

Kwame's instinct — one fat "substation" record with a hundred attributes — is
exactly the model VoltEdge refuses. The reasons are worth internalising, because
they explain almost every design decision in the rest of this guide.

#### ∑ Why not one fat object?

- **A transformer is a network node; a fence is not.** A power flow needs to walk
  *through* the transformer (HV in, LV out, impedance drop, tap ratio). It never
  walks through a site boundary. If the transformer were an attribute of the
  station, the analysis engine would have nothing to traverse. Making each piece
  of plant a first-class *feature with terminals* is what lets trace, load flow
  and fault studies treat the station as what it physically is: a small network.
- **Equipment has independent lifecycles.** A breaker is replaced; a transformer
  is uprated; a battery bank is swapped. Each is inspected, defect-logged, and
  maintained on its own schedule (see the
  [works & maintenance guide](works-inspections-maintenance-guide.md)). A single
  monolithic record cannot carry twenty independent condition ratings and
  histories cleanly.
- **The same equipment classes appear outside substations.** A `POWER_XFMR`, an
  `MV_BUSBAR`, a switch — these also exist at pole tops, in packaged substations,
  in customer yards. Modelling them as reusable classes rather than
  substation-internal fields means one definition, one symbology, one set of
  network rules, everywhere.
- **Metadata, not code.** VoltEdge is *metadata-driven*: a feature class is rows
  in `metadata.feature_classes` / `fields` / `terminals` / `network_rules`, not a
  hand-written Go type. Adding "station-service transformer" to the model was a
  seed file (Chapter 4), not a schema migration and a redeploy. A fat object
  would push all that variety into application code.

So when this guide says "model a substation," it means: **place the boundary,
populate it with equipment, and connect that equipment two different ways.** The
next section is about those two ways, because they are the spine of everything.

### 1.2 The two graphs — the idea that unlocks the model

Every piece of equipment in a station participates in **two completely separate
graphs**, and VoltEdge keeps them separate on purpose. Confusing them is the
number-one modelling mistake, so we introduce them side by side.

#### The containment graph — *"what is inside what"*

This is the **spatial / ownership** hierarchy. The station *contains* its
transformers, its busbars, its panels. A panel *contains* the battery and the
IEDs bolted into it. It answers questions like *"list everything at Adenta
Primary"* and *"which station does this breaker belong to?"*

- It is expressed with **`relationship_classes` of kind `containment`** and
  concrete edges in `runtime.feature_relationships`.
- It is a **tree**: since the Phase 47 "minimal-model correction," each piece of
  equipment has a **single parent**. (The old `VOLTAGE_LEVEL` tier was retired in
  migration 0086; `BAY` remains the one real sub-container.)
- Crucially, **it carries no electricity.** Containment says the breaker sits
  inside the station's fence. It says nothing about what the breaker is wired to.

#### The connectivity graph — *"what feeds what"*

This is the **electrical** network. It is what a trace walks, what a load flow
solves, what a fault study energises. It answers *"if I open this breaker, what
goes dark?"* and *"what is the voltage at the 11 kV busbar under load?"*

- It is expressed with **`terminals`** on each class, **`network_rules`** that say
  which terminal may connect to which, and concrete **`runtime.feature_connectivity`**
  edges made when you wire two features together.
- It is a **graph**, not a tree — busbars fan out, ties close loops, meshes exist.
- Crucially, **it ignores the fence.** The connectivity graph does not care that
  the transformer and the busbar are "in the same station." It only cares that
  the transformer's LV terminal is wired to the busbar's incoming terminal.

#### ∑ Why deliberately independent? (the Phase 47 tenet)

It is tempting to think containment and connectivity should imply each other —
*"if the transformer is inside the station, surely it's connected to the
station's bus."* VoltEdge rejects that coupling, and the discipline pays off:

- **Connectivity crosses station boundaries; containment does not.** A feeder
  leaves Adenta Primary, runs three kilometres, and enters a distribution
  substation. The *conductor* is electrically continuous across both; neither
  station "contains" the other. Only an independent connectivity graph can
  express a network that spans many containers.
- **You can be inside a station and electrically dead.** A spare transformer sits
  in the yard (contained by the station) but is wired to nothing (absent from the
  connectivity graph). A newly-built bay is contained before it is commissioned
  and energised. If containment implied connectivity, you could not model the
  ordinary in-between states.
- **Different consumers, different shapes.** The analysis engines
  (`api/internal/powerflow`, `trace`) read *only* the connectivity graph. The CIM
  export (Chapter 6) is essentially the *only* consumer of the containment graph,
  which it turns into CIM `EquipmentContainer` nesting. Keeping them separate lets
  each stay the shape its consumer needs.

Hold onto this picture — a **tree of containment** and a **graph of
connectivity**, over the same equipment, never conflated. Chapters 5 and 6 build
each one for Adenta Primary in full.

> ▶ **Rule of thumb at the keyboard.** When you drag a feature *into* a station on
> the map, VoltEdge does **not** automatically wire it to anything. Placement sets
> geometry; containment is a relationship you (or an assembly) create; connectivity
> is wiring you do at the terminals. Three separate acts. If you place a
> transformer and expect a trace to find it, and it doesn't — you have geometry
> without connectivity. That is not a bug; it is the model asking you to wire it.

### 1.3 The layers of a real station — the map of the guide

A HV/MV primary substation, seen through VoltEdge's classes, has four layers.
Kwame's mental sketch of Adenta Primary maps one-to-one onto the chapters ahead:

| Layer | What it is, physically | VoltEdge classes | Graph role | Chapter |
| --- | --- | --- | --- | --- |
| **The place** | The fenced site, the footprint | `SUBSTATION` (polygon) | Containment root; **not** a network node | this chapter |
| **The yard** | HV/MV plant in the open air: transformers, busbars, breakers, isolators, earthing | `POWER_XFMR`, `MV_BUSBAR`, switching devices, `EARTHING_XFMR` | Live **connectivity** nodes (junctions) | 2 |
| **The control room** | Secondary systems: protection & control panels, station DC, IEDs, annunciators | `SUBSTATION_PANEL` (container) + `STATION_BATTERY` / `BATTERY_CHARGER` / `CONTROL_IED` / `ANNUNCIATOR` | Containment only; **network-less** (`network_role 'none'`) | 3 |
| **Station AC services (LVAC)** | The station's own low-voltage supply: station-service transformer → LVAC board → chargers, fans, tap-changer motors, lighting | `AUX_TRANSFORMER`, `LVAC_BOARD` | Live **connectivity** nodes, fed from the MV yard | 4 |

Two things about this table are worth flagging now, because they surprise people:

- **The control-room DC layer is on the containment graph but *not* the
  connectivity graph.** A `CONTROL_IED` or a `STATION_BATTERY` has
  `network_role 'none'`: it has no terminals, so a trace cannot walk to it. It is
  *contained by* a panel (so "list what's in this panel" works), but it is not
  *wired* in the electrical-network sense. Chapter 3 explains why that is the
  right call, and where the boundary sits.
- **The LVAC layer *is* on the connectivity graph.** This is recent and
  deliberate. The station-service transformer taps the MV bus and steps it down to
  400/230 V; the LVAC board fans that out to the station's auxiliary circuits. By
  making both real *junctions* with terminals and network rules (Chapter 4), a
  trace and a load flow can walk **MV bus → aux transformer → LVAC board →
  auxiliary feeders** — so "what feeds the battery charger?" and "does the
  station's own supply go dark if I open this MV isolator?" become answerable from
  the network, not from a spreadsheet.

That asymmetry — DC secondary contained-but-not-connected, AC auxiliary
contained-*and*-connected — is not an accident of history. It reflects a real
boundary in what VoltEdge's analyses can reason about today, and Chapters 3 and 4
each defend their side of it.

### 1.4 Meet Adenta Primary — the station we build

Throughout this guide we build one station, so the abstractions never float free.
**Adenta Primary** is a 33/11 kV primary substation:

- **Two 33/11 kV power transformers** (`POWER_XFMR`, two-winding), T1A and T1B,
  each fed from a 33 kV incomer.
- **A 33 kV side** — incoming feeders onto a 33 kV busbar (`MV_BUSBAR`), through
  circuit breakers and isolators (switching devices) into the transformer HV
  windings.
- **An 11 kV side** — the transformer LV windings onto an 11 kV busbar
  (`MV_BUSBAR`), which fans out to the outgoing 11 kV feeders through their bay
  breakers.
- **A control building** housing protection, control and metering **panels**
  (`SUBSTATION_PANEL`), the **station battery** and **charger**
  (`STATION_BATTERY` / `BATTERY_CHARGER`), the **bay controllers / RTU / SCADA
  gateway** (`CONTROL_IED`), and an **annunciator**.
- **A station-service supply** — an **auxiliary transformer** (`AUX_TRANSFORMER`)
  tapped off the 11 kV bus, stepping down to a **400/230 V LVAC board**
  (`LVAC_BOARD`) that feeds the charger, the transformer cooling fans, the
  tap-changer motors and the building's small power and lighting.

By Chapter 9 we will have placed every one of these, wired the connectivity graph
so a trace runs from a 33 kV incomer to an 11 kV feeder, built the containment
tree so `SUBSTATION contains …` lists them all, drawn the one-line, and run a
load flow that reports the 11 kV bus voltage and the loading on each transformer —
including the station's own auxiliary load through `AUX_TRANSFORMER`.

> ▶ **Your first act.** Modelling Adenta Primary starts with the *place*: on the
> map, choose the `SUBSTATION` class, draw the site polygon around the yard and
> control building, and name it. That polygon is `network_role 'none'` — it will
> never be a trace node — but it is the **containment root** every other feature in
> this guide will hang beneath. Everything else is population and wiring, and that
> is Chapter 2.

### 1.5 What you now know

- A substation in VoltEdge is a **place** (`SUBSTATION` polygon) plus a
  **population** of independently-modelled equipment features — not one fat object.
- That equipment lives in **two independent graphs**: a **containment tree**
  ("what's inside what") and a **connectivity graph** ("what feeds what"). They are
  kept separate on purpose; the analyses read connectivity, CIM export reads
  containment.
- A real station has **four layers** — the place, the yard, the control room, and
  the LVAC services — and they differ in *which* graphs they join: the DC secondary
  layer is contained but network-less, while the LVAC layer is both contained and
  connected.
- Everything is **metadata-driven**: each class is seed rows, so the model grows by
  seeding, not recompiling.

Next, we open the yard and meet the primary plant — the transformers, busbars and
switches that carry the actual power — class by class, terminal by terminal. Say
**next** for Chapter 2.

---

## Chapter 2 — The yard

The yard is the open-air part of the station: the plant that actually carries
power. Everything here is a **live connectivity node** — a junction with
terminals that a trace walks and a load flow solves. Kwame's job in this chapter
is to place the primary plant of Adenta Primary and understand, for each class,
*what terminals it has and why*, because the terminals are what he will wire
together in Chapter 5.

Before the classes, one orientation point that applies to every one of them.

### 2.1 What "a junction with terminals" means

Every yard class shares a shape:

- `geometry_type 'point'` — it is placed as a single point on the map (a
  transformer, a breaker, a busbar all resolve to a location).
- `network_role 'junction'` — it is an **inline node** on the electrical network.
  Power flows *through* it. This is the property that distinguishes yard plant
  from the `SUBSTATION` polygon (`network_role 'none'`) and from the control-room
  DC classes of Chapter 3 (also `'none'`).
- **`terminals`** — named connection points (`T1`, `T2`, `HV`, `LV`, `N`, …), each
  with a `role` and a `phase_capable` flag. A terminal is *where a wire lands*. Two
  features are electrically connected by joining a terminal on one to a terminal on
  the other, and the join is only allowed if a **`network_rule`** permits that
  terminal-to-terminal pairing (Chapter 5).
- `geometry_optional true` and `is_diagram_placeable true` — substation plant may
  be captured **geographically** (a real coordinate in the yard), **only on a
  one-line diagram** (a bay breaker whose exact GPS nobody surveyed), or **both**.
  `geometry_optional` (migration 0052) lets a feature exist with a `NULL` geometry;
  `is_diagram_placeable` (migration 0032) lets it be dropped onto a schematic. Both
  flags are set *explicitly* in the seeds, because migration 0032's backfill only
  touched classes that existed when it ran — a class seeded later takes the column
  default `false` unless the seed says otherwise.

> ∑ **The shape is copied on purpose.** The MV switching classes are, in the seed
> author's own words, "byte-for-byte the DIST_SWITCH pattern." This is the
> metadata-driven model paying off: a new junction class is *new rows in the same
> tables*, not new engine code. The trace and power-flow engines are
> **field-name-** and **role-driven**, not class-driven — they look for a terminal
> called `HV`, a field called `primary_voltage`, a `device_state` of `OPEN`. Seed
> those names and the engines already know what to do.

### 2.2 The power transformer — `POWER_XFMR`

The transformer is the heart of the yard: it is what makes a 33/11 kV station a
33/11 kV station. Adenta Primary has two, T1A and T1B.

#### ▶ Placing a power transformer

1. On the map, inside the `SUBSTATION` polygon, choose the **Power Transformer**
   class and place a point where T1A stands in the yard.
2. In the feature panel, choose the **subtype** — `two_winding` for a plain
   33/11 kV unit (Adenta's are two-winding). A `three_winding` unit would have a
   tertiary winding brought out.
3. Fill the nameplate. The panel requires **`primary_voltage`** (the HV class,
   `33KV`) and a **`condition_rating`**. Everything else is optional so an
   un-surveyed unit degrades gracefully.

#### ∑ Why `POWER_XFMR` is its own class (not a `DIST_XFMR` subtype)

VoltEdge already has `DIST_XFMR` — the pole/pad distribution transformer that
steps MV down to LV service. A substation power transformer is *not* modelled as a
subtype of it, for two structural reasons a subtype cannot express:

- **The winding count changes the terminals.** A three-winding unit has a *real
  third connection point* — the tertiary. Terminals are therefore
  **subtype-scoped** on `POWER_XFMR` (this is the one class in the model where
  `terminals.subtype_id` is non-`NULL`):
  - `two_winding` → `HV` (primary), `LV` (secondary), `N` (ground)
  - `three_winding` → `HV`, `LV`, `TV` (tertiary), `N`
  The `TV` role required widening a `CHECK` constraint (migration 0070) before it
  could be seeded.
- **The nameplate is quoted in MVA, not kVA.** `POWER_XFMR` carries `rated_mva`;
  the engine reads it and multiplies by 1000 to get kVA.

#### ∑ How the transformer behaves in a load flow — for free

This is the pay-off of the field-name-driven engine. `POWER_XFMR` carries the
*same field names* the power-flow solver already reads off any junction, so it is
swept as a real transforming node with **zero engine change**:

| Field | What the sweep does with it |
| --- | --- |
| `primary_voltage` (coded, e.g. `33KV`) | HV nominal — the "from" voltage |
| `secondary_voltage` (numeric, e.g. `11000`) | LV nominal — the "to" voltage. *Both* present ⇒ treat as a voltage-transforming node; absent `secondary_voltage` ⇒ pass-through |
| `impedance_pct` + `x_r_ratio` | the nameplate %Z and its X/R — the series impedance drop |
| `rated_mva` | nameplate rating → the loading % is reported against it |
| `tap_position` + `tap_step_pct` | shift the effective LV turns ratio in the sweep (`1 + position·step/100`), so the reported LV voltage tracks the live tap |
| `vector_group` | **record-only** — a positive-sequence radial load flow has no phase shift to act on until a meshed/parallel study exists |

The three-winding tertiary fields (`tertiary_voltage`, `z_hv_tv_pct`, …) are
recorded now and staged for a later slice; the sweep models the HV↔LV path today.

> ∑ **"Record the field first, key an engine off it later."** Several `POWER_XFMR`
> fields (the tap changer, the tertiary) landed in the survey schema a pass *before*
> the engine used them. This is a recurring VoltEdge discipline — get the data
> captured, wire the analysis to it when ready — and it is why the earthing
> transformer (§2.5) and the LVAC layer (Chapter 4) needed no engine change at all.

Symbology: a **teal square** on the map — deliberately a different colour from the
**blue square** of the distribution `DIST_XFMR`, so a bulk transformer reads
differently from a service one at a glance.

### 2.3 The busbar — `MV_BUSBAR`

If the transformer is the heart, the busbar is the spine. Adenta Primary has two:
a **33 kV bus** the incomers land on, and an **11 kV bus** the transformer LV
windings feed and the outgoing feeders hang off.

#### ∑ The one-incoming, one-equipotential-tap shape

`MV_BUSBAR` is a point junction with exactly **two terminals**:

- **`T1` (primary)** — the incoming way.
- **`T2` (other)** — the equipotential bar itself, the side that **fans out**.

The bar is a *solid equipotential conductor*, not a set of individually switchable
ways — so it needs no twenty-terminal schema to feed twenty circuits. Every
outgoing feeder is a **distinct connectivity edge on the same `T2`**. One rule,
`MV_BUSBAR T2 → …`, scales to any number of feeders. (This is the same shape the
LV distribution board `LV_BUSBAR` uses; if you know one you know the other.)

The bar participates in four network-rule patterns (the full wiring vocabulary is
Chapter 5, but here is the shape):

- **A.** `MV_CONDUCTOR → MV_BUSBAR T1` — a feeder or a transformer tail lands *onto*
  the bar's incoming way.
- **B.** `MV_BUSBAR T2 → MV_CONDUCTOR` — the bar fans out to a bay feeder.
- **C.** `MV_BUSBAR T2 → {switch} T1` — the bar feeds a bay switch's line side
  directly.
- **D.** `{switch} T2 → MV_BUSBAR T1` — a switch's load side lands back onto a bus
  (an incomer breaker onto the bar).

> ∑ **A diagram detail worth knowing.** `MV_BUSBAR` sets
> `diagram_geometry_type 'line'`: on the map it is a point, but on a one-line
> diagram it is drawn as a **bar** (a line), which is how an engineer expects a bus
> to look on a schematic. Geometry on the map and geometry on the diagram are
> allowed to differ — Chapter 7 returns to this.

#### ▶ Placing Adenta's two busbars

1. Place an `MV_BUSBAR` point for the 33 kV bus; set its voltage on the panel.
2. Place a second `MV_BUSBAR` for the 11 kV bus.
   These two bars are *not* connected to each other directly — the transformers
   sit between them. Keeping them as two features (not one) is what lets the model
   say "the 33 kV bus is live but the 11 kV bus is dead" during a transformer
   outage.

### 2.4 The switching devices — breakers, isolators, sectionalizers

Switches are what make a station *operable*: they isolate, they protect, they
reconfigure. VoltEdge models the MV yard switches as a family of junction classes
that all share the `DIST_SWITCH` shape:

| Class | Physical role | Interrupts fault current? |
| --- | --- | --- |
| `MV_BREAKER` | The bay / feeder **circuit breaker** — the workhorse protective switch | Yes |
| `MV_ISOLATOR` | The **off-load isolator / disconnector** — a visible isolating gap | No (off-load) |
| `MV_SECTIONALIZER` | The **auto-sectionalizer** — counts fault passages, opens on a dead line | No (opens dead) |
| `DIST_SWITCH` | The general MV load-break switch; its `automatic_recloser` subtype is the **recloser** | Load-break / recloser |

*(The recloser is deliberately **not** a fourth new class — it already lives as the
`automatic_recloser` subtype of `DIST_SWITCH`, and maps to a CIM `Recloser`. The
pole-mounted `MV_RECLOSER` assembly kit reuses the same idea.)*

#### ∑ The common shape — `device_state` and the OPEN barrier

Every switch is a two-terminal junction: **`T1` line-side, `T2` load-side**, with
three network rules (`MV_CONDUCTOR → T1`, `MV_CONDUCTOR → T2`, and `Tn →
MV_CONDUCTOR`) so a downstream trace can walk *through* it. What makes it a switch
rather than a plain node is one field:

- **`device_state`** — a coded `CLOSED` / `OPEN`. When a trace is configured to
  respect barriers, an **`OPEN` device_state is a barrier**: the trace stops there,
  and everything beyond it is de-energised. This single field is the hinge of
  isolation tracing, switching plans and the outage console.

> ∑ **`device_state` is *live*, not just survey data.** The outage-management module
> flips `device_state` in the same transaction as a switching step, so opening a
> breaker in a switching plan immediately changes what every trace, power flow and
> map query sees (see the OMS control-room console). There is also a separate
> `normal_state` field recording the switch's *normal* (steady-state) position, so
> the system can tell "abnormally open for switching" from "normally open tie."

The subtypes on `MV_BREAKER` name the **interrupting medium** (e.g. vacuum, SF₆) —
recorded now, not yet acted on by the engine, present so a later slice can key off
a real field rather than inventing one.

#### ▶ Placing Adenta's bay switches

For each 33 kV incomer and each transformer HV connection, Kwame places an
`MV_BREAKER` and, where there is a maintenance isolation point, an `MV_ISOLATOR`.
On the 11 kV side, each outgoing feeder gets its bay `MV_BREAKER`. He leaves them
`device_state = CLOSED` for the normal running arrangement; Tom will open them in
Chapter 8 to see the network react.

### 2.5 The earthing transformer — `EARTHING_XFMR`

The last piece of yard plant is easy to overlook and instructive to model: the
earthing (grounding) transformer, which gives an otherwise-ungrounded (delta) MV
system a neutral/earth reference so ground-fault current has a path.

#### ∑ Why it is a *pass-through leaf*, not a transforming node

`EARTHING_XFMR` sits "in the realm of power transformers" but plays a completely
different electrical role — it transforms no voltage and carries no through-load in
balanced operation; it is a **shunt** that only conducts during a ground fault. The
field-name-driven engine handles this correctly *by what fields are present*:

- It carries **`primary_voltage`** (the system voltage it earths) but **no
  `secondary_voltage`**. Because `resolveTransformerModel` needs *both* to treat a
  node as voltage-transforming, the sweep resolves `EARTHING_XFMR` as a plain
  **pass-through leaf** — exactly right: a dead-end shunt off the bus with no
  downstream network and no through-power. It has no `rated_kva`/`rated_mva`, so it
  reports no loading %.
- Its ground-fault data *does* drive an analysis: **`z0_ohm`** is read as the
  unit's zero-sequence shunt to earth (with `3 ×` any integral
  `neutral_resistance_ohm` folded in) by the single-line-to-ground model
  (`groundfault.go`). So an earthing transformer on an otherwise-ungrounded bus
  *turns that bus grounded* and bounds the SLG current there — identified by the
  presence of a positive `z0_ohm`, not by class.

#### ∑ Why it has *no subtypes* (unlike `POWER_XFMR`)

The two grounding constructions (zig-zag / interconnected-star, and
grounded-wye-delta) present the **same connection points** to the network — a
three-phase line terminal plus a neutral/earth terminal (the wye-delta bank's
delta is closed internally). No terminal or behavioural difference means a subtype
would be over-modelling, so the construction is a plain **coded attribute**
(`GROUNDING_TYPE`) and the required fields are enforced by `nullable = false`
directly.

This is the general lesson of the yard, stated one more way: **a subtype is for
differences the network can see** (different terminals, different engine
behaviour). Everything else is a field.

### 2.6 The yard so far

Kwame has now placed, inside the Adenta Primary polygon:

- two `POWER_XFMR` (T1A, T1B) — teal squares, swept by the load flow from their
  nameplate fields;
- two `MV_BUSBAR` — the 33 kV and 11 kV bars, each a one-in/one-fan-out junction;
- the bay `MV_BREAKER`s and `MV_ISOLATOR`s, each a two-terminal switch whose
  `device_state` gates isolation traces;
- optionally an `EARTHING_XFMR` on the 11 kV bus — a grounding shunt that shapes
  the ground-fault study without touching the load flow.

Every one of these is a **junction with terminals**, geometry-optional and
diagram-placeable, sharing one copied shape so the engines need no per-class code.
They are placed but **not yet wired** — geometry without connectivity, exactly the
Chapter 1 warning. Wiring is Chapter 5.

But first, the other half of the station: the control room, where the protection,
control and DC-auxiliary systems live — and where, for the first time, we meet
classes that are **contained but deliberately not connected**. Say **next** for
Chapter 3.

---

## Chapter 3 — The control room

Everything in Chapter 2 carried AC power and lived on the connectivity graph. The
control room carries almost none. It is where the station is *protected*,
*controlled* and *kept alive during a grid failure* — the protection relays, the
bay controllers and RTU, the DC battery that actually trips a breaker, the charger
that floats it, the alarm annunciator. In VoltEdge these are real feature classes,
but they join a **different graph** from the yard, and understanding *why* is the
lesson of this chapter.

We build the control room in two moves: first the **enclosures** (the panels),
then the **secondary equipment inside them**.

### 3.1 The enclosure — `SUBSTATION_PANEL`

A control building is, physically, a room full of cubicles: a protection panel, a
control panel, a metering panel, an RTU/SCADA cabinet, a DC distribution board, a
marshalling kiosk. VoltEdge models each cubicle as a **`SUBSTATION_PANEL`** — the
physical enclosure as a first-class, inventoried asset.

#### ∑ Why the panel is geometry-less

`SUBSTATION_PANEL` has **`geometry_type 'none'`**, exactly like `BAY`. A panel is
not a point or a line you survey on the map — its meaningful visual form is a
**schematic** (a panel-front layout, or its place on the station one-line), not a
GPS coordinate. The compiler skips the geometry column entirely for a geometry-none
class, and CRUD rejects any attempt to create one carrying geometry.

Two coded fields carry the panel's identity:

- **`panel_type`** — what kind of cubicle it is:
  `PROTECTION` / `CONTROL` / `METERING` / `RTU_SCADA` / `AUXILIARY_DC` /
  `MARSHALLING`. One class, six roles, distinguished by a *field*, not six classes —
  the same economy the yard used (§2.5): the enclosure is the same asset in
  different functions.
- **`control_mode`** — `LOCAL` / `REMOTE` / `LOCAL_REMOTE`, the local/remote
  selector state that governs whether the bay can be operated from SCADA or only at
  the panel.

> ▶ **Placing Adenta's panels.** In the control building, Kwame creates one
> `SUBSTATION_PANEL` per cubicle: a `PROTECTION` panel and a `CONTROL` panel per
> transformer bay, a `METERING` panel, an `RTU_SCADA` cabinet, and an
> `AUXILIARY_DC` board. Because they are geometry-none, he does not place them on
> the map — he creates them as records (and later stamps them on a diagram,
> Chapter 7).

#### ∑ The panel sits in *two* relationships — and this is the point

`SUBSTATION_PANEL` is where the containment graph starts to earn its keep:

- **`SUBSTATION contains SUBSTATION_PANEL`** (containment, one-to-many) — the panel
  is physically inside the station. This is what makes "list everything at Adenta
  Primary" reach the control room.
- **`SUBSTATION_PANEL contains PROTECTIVE_RELAY` / `METER`** (and, as we will see,
  the DC-auxiliary classes) — the equipment bolted into the cubicle.
- **`SUBSTATION_PANEL serves BAY`** — a **`many_to_many`** association (kind
  `panel_bay`, needing migration 0083), *not* a containment edge.

That last one is deliberate and worth dwelling on. A `BAY` is the **logical
circuit** — the electrical bay (an incomer, a transformer, a feeder). A
`SUBSTATION_PANEL` is the **physical cubicle**. They are not the same thing and do
not nest cleanly: one protection panel may serve two bays; one bay's protection,
control and metering may be spread across three panels. Forcing a containment tree
("the bay contains the panel" or vice versa) would lie about the wiring. So
VoltEdge decouples them with a **many-to-many association** — the panel *serves* the
bay without either owning the other. This is the same discipline as the two graphs
in Chapter 1, applied one level down: **model the real relationship, not a
convenient tree.**

### 3.2 The secondary equipment — the P&C DC-auxiliary layer

Inside those panels live the classes that were, until recently, the biggest gap in
the substation model: the **protection-and-control secondary layer**. VoltEdge
registers four:

| Class | What it is | Identity field (coded) |
| --- | --- | --- |
| `STATION_BATTERY` | The DC battery bank — the trip/close/control supply that actually operates a breaker's trip coil when a relay picks up | `battery_technology` |
| `BATTERY_CHARGER` | The rectifier/charger feeding the DC bus and float-charging the battery | `charger_type` |
| `CONTROL_IED` | The control/automation intelligent electronic device | `ied_role` |
| `ANNUNCIATOR` | The alarm annunciator / facia-window panel | — |

`CONTROL_IED` deserves a note: one class covers the **bay controller**, **RTU**,
**SCADA gateway**, **station HMI** and **protocol converter** — carried by a coded
**`ied_role`** field, not one class each, because these are the same kind of asset
in different roles (exactly as `panel_type` does for panels and the ANSI device
number does for relays). Its comms are captured by a `COMMS_PROTOCOL` field
(`iec61850` / `dnp3` / `iec60870_5_104` / `modbus`).

#### ∑ The defining choice: `geometry_type 'none'` **and** `network_role 'none'`

All four classes mirror `PROTECTIVE_RELAY` deliberately, and the two `'none'`s are
the whole story:

- **`geometry_type 'none'`** — same as the panels; their only visual form is a
  diagram placement. (Because they have *no map symbology to fall back to*, each
  gets a `render_context 'diagram'` symbology rule explicitly, or it would render
  as no symbol at all.)
- **`network_role 'none'`** — **they are not in the electrical connectivity graph.**
  A battery, a charger, an IED, an annunciator carries no AC power path and has **no
  terminals**. You cannot wire them, and a trace cannot walk to them.

This is the "contained but not connected" idea promised in Chapter 1, made
concrete. The DC layer joins the **containment** graph — `SUBSTATION_PANEL contains
STATION_BATTERY`, and so on, all `one_to_many` — so "what's in the DC board?"
works. It does **not** join the connectivity graph.

> ∑ **Why not wire the trip/control circuits?** Because the link from a relay to a
> breaker's trip coil, or from the battery to the DC bus, is *control/trip wiring* —
> a second kind of "line" with its own semantics (DC, signal, protection logic),
> distinct from the AC power path `feature_connectivity` models. VoltEdge records
> that association today as **schematic annotation** on the panel diagram, not as a
> connectivity edge. Bringing control wiring into a first-class second connectivity
> layer is a known, larger open item ("dual line semantics"). Until then, the honest
> model is: the DC layer is inventoried and contained, and its wiring is drawn, not
> traced. Do not expect an isolation trace to tell you a charger lost supply — that
> reasoning arrives through the LVAC layer in Chapter 4, on the *AC* side, which
> *is* connected.

#### ∑ Coded roles, not subtypes; app-enforced requiredness

Each class has **one `standard` subtype** and carries its variant in a coded field
(`battery_technology`, `charger_type`, `ied_role`) — the §2.5 rule again: no
terminal or behavioural difference, so no subtype. And every field is **nullable at
the database level**, with requiredness enforced at the app layer through
`subtype_field_rules` (the identifying coded field is required, the rest optional).

> ∑ **Why nullable-in-DB, required-in-app?** The compiler emits a `NOT NULL`
> column for a `nullable = false` field — which would fail if the class already had
> rows when a later re-seed added the field. Keeping the DB column nullable and
> enforcing "required" in the application keeps every re-compile **additive** and
> safe on a populated table. This is a general VoltEdge compiler rule, not specific
> to the control room — but the DC layer is a clean place to see it.

### 3.3 Where protection and instrumentation sit

Two neighbours of the DC layer are worth placing on the map of the station, though
they predate this guide's classes:

- **`PROTECTIVE_RELAY`** — the protection relay itself, also `geometry_type 'none'`,
  identified by its ANSI device number, contained by a `PROTECTION` panel, with its
  own protection curves. It is the thing that *decides* to trip; the
  `STATION_BATTERY` is the thing that *provides the energy* to trip; the
  `MV_BREAKER` (Chapter 2) is the thing that *does* the tripping. Three classes,
  three graphs' worth of relationships, one physical action.
- **`MV_CT` / `MV_VT`** — the instrument transformers that feed the relays and
  meters their current and voltage signals.

You do not need to master these to model Adenta Primary's structure, but knowing
they exist explains why the DC/control layer is *complete* rather than a stub: the
station's protection scheme has a home for every real component.

### 3.4 The control room so far

Kwame has now built the secondary side of Adenta Primary:

- one `SUBSTATION_PANEL` per cubicle (`PROTECTION` / `CONTROL` / `METERING` /
  `RTU_SCADA` / `AUXILIARY_DC`), each **contained by the station** and each
  **serving** one or more bays through the many-to-many `panel_bay` association;
- inside them, the **DC-auxiliary layer** — `STATION_BATTERY`, `BATTERY_CHARGER`,
  `CONTROL_IED` (with its `ied_role` and comms protocol), `ANNUNCIATOR` — all
  `geometry 'none'`, all `network_role 'none'`, all **contained but not
  connected**, their identity in coded fields rather than subtypes.

The whole control room lives on the **containment graph only**. That is the right
model for DC and signals — but it leaves one honest question hanging: *what actually
feeds the charger, the cooling fans, the tap-changer motors?* That is an **AC**
question, and AC *is* connectable. Answering it is the job of the LVAC layer — the
station's own low-voltage supply — which is where the connectivity graph re-enters
the control room. Say **next** for Chapter 4.

---

## Chapter 4 — The LVAC layer

Chapter 3 ended on a real question. The control room is full of equipment that
*consumes* power — the battery charger, the transformer cooling fans, the
tap-changer motors, the building lighting and small power. None of it appeared on
the connectivity graph, because it is DC or signals. But every one of those loads
is ultimately fed by an **AC** supply — the station's own low-voltage services —
and AC *is* something VoltEdge can trace and solve. That supply is the **LVAC
layer**, and it is the most recently added piece of the substation model. This
chapter is both a tour of it and an honest account of why it was built the way it
was.

### 4.1 The gap this fills

A HV/MV station does not draw its auxiliary supply from the 230 V mains next door.
It makes its own: a **station-service (auxiliary) transformer** taps the MV bus and
steps it down to **400/230 V**, feeding a **station LVAC board** that distributes
to all the auxiliary circuits. This is the source of supply for the very
control-room equipment Chapter 3 inventoried.

Before this layer, VoltEdge modelled the yard (Chapter 2) and the control-room
secondary (Chapter 3) but had **no representation of the station's own AC
services**. The consequence was a blind spot: you could see the charger and the
fans as contained assets, but you could not answer *"what feeds them?"* or *"if I
open this 11 kV isolator, does the station's own supply go dark?"* from the
network. A substation-modelling review surfaced exactly this — yard ✅, control-room
DC ✅, but station AC ❌ — and the LVAC layer was built to close it.

> ∑ **The design choice: a *traceable subsystem*, not inventory rows.** There were
> two ways to fill the gap. **(A)** Add a couple of geometry-less inventory records
> — "there is an aux transformer, there is an LVAC board" — the way the DC layer is
> modelled. **(B)** Model them as **real junctions with terminals**, wired into the
> network, so a trace and a load flow actually walk MV bus → aux transformer → LVAC
> board → auxiliary feeders. Option **B** was chosen. The whole point of the layer
> is *traceability*; inventory rows would have reproduced the very blind spot they
> were meant to remove. So — unlike the DC layer of Chapter 3 — the LVAC classes are
> `network_role 'junction'`, and they are **contained *and* connected.**

### 4.2 `AUX_TRANSFORMER` — the station-service transformer

#### ∑ The shape

`AUX_TRANSFORMER` is a point junction, `geometry_optional` and
`is_diagram_placeable` (the yard shape from §2.1), with a single `standard`
subtype and two subtype-agnostic terminals:

- **`T1` (primary)** — the MV incoming side (tapped from the 11 kV bus at Adenta).
- **`T2` (secondary)** — the LV outgoing side (400/230 V, to the board).

One subtype, because — the §2.5 rule yet again — a station typically has one kind of
aux transformer and the variety is captured in fields, not in a terminal-changing
subtype. Its fields deliberately reuse the **same names** the power-flow engine
already reads:

| Field | Role |
| --- | --- |
| `primary_voltage` (coded `VOLTAGE_CLASS`, **required**) | MV nominal in (e.g. `11KV`) |
| `secondary_voltage` (numeric) | LV nominal out (e.g. `400`) |
| `impedance_pct` (numeric) | nameplate %Z |
| `rated_kva` (numeric) | nameplate rating → loading % |
| `condition_rating` (coded, **required**) | asset condition |

#### ∑ Why it is swept by the load flow with zero engine change

This is the same trick `POWER_XFMR` uses (§2.2), and it is why Option B cost almost
nothing to build. The power-flow engine is **field-name-driven**:
`resolveTransformerModel` looks for `primary_voltage` **and** `secondary_voltage`
on *any* junction and, finding both, treats the node as a voltage-transforming
transformer; `resolveTransformerRating` reads the nameplate. `AUX_TRANSFORMER`
carries those exact names, so the solver sweeps it as a **real step-down node with a
loading %** — the station's own auxiliary load shows up in the load flow — without a
single line of new engine code. A station aux transformer *is* a small power
transformer, so it also maps to CIM **`PowerTransformer`** on export (§4.5).

On the map it is an **amber square** — a third distinct transformer colour, so the
station-service unit reads differently from the teal bulk `POWER_XFMR` and the blue
distribution `DIST_XFMR`.

### 4.3 `LVAC_BOARD` — the station AC low-voltage board

#### ∑ The shape

`LVAC_BOARD` is a point junction, single `standard` subtype, with the **same
one-in/one-fan-out shape as `LV_BUSBAR`** (§2.3):

- **`T1` (primary)** — the incoming way (from the aux transformer's LV side).
- **`T2` (other)** — the equipotential fan-out side that supplies the station's AC
  auxiliary circuits. One `T2`, any number of feeders, each a distinct connectivity
  edge.

Its fields describe a board rather than a transformer:

| Field | Role |
| --- | --- |
| `board_ref` (text) | the asset tag (nullable — may be unlabelled) |
| `phase` (coded `PHASE`, **required**) | 1-phase or 3-phase configuration |
| `incoming_rating_a` (numeric) | incomer rating |
| `num_ways` (numeric) | number of outgoing ways |
| `condition_rating` (coded, **required**) | asset condition |

On the map it is an **olive square**. On export it maps to CIM **`BusbarSection`** —
the same CIM class the LV distribution board `LV_BUSBAR` uses, because that is
electrically what it is.

### 4.4 Wiring the layer — the four rules

The whole layer is defined by four `network_rules` that give it a directed shape a
downstream trace and a load flow can walk. Two feed the transformer's primary; one
carries it to the board; one fans the board out:

| Rule | Meaning | `phase_rule` |
| --- | --- | --- |
| `MV_BUSBAR T2 → AUX_TRANSFORMER T1` | the **station-service tap** off the MV bus (the usual arrangement — the bar's fan-out side into the aux transformer's MV winding) | `NULL` (device hop) |
| `MV_CONDUCTOR → AUX_TRANSFORMER T1` | a **direct feeder tap** off an MV conductor | `from.voltage_class = to.primary_voltage` (voltage-matched, like `POWER_XFMR`'s HV-in) |
| `AUX_TRANSFORMER T2 → LVAC_BOARD T1` | the transformer's LV winding into the board's incomer — **this is what makes the board's supply traceable** | `NULL` (device hop) |
| `LVAC_BOARD T2 → LV_CONDUCTOR` | the board fans out to the station's AC **auxiliary wiring runs** | `NULL` (device hop) |

> ▶ **Wiring Adenta's LVAC.** Kwame places an `AUX_TRANSFORMER` tapping the 11 kV
> `MV_BUSBAR`, wires its `T1` to the bar's `T2`; places the `LVAC_BOARD` and wires
> its `T1` to the transformer's `T2`; then runs `LV_CONDUCTOR` from the board's `T2`
> to each auxiliary circuit. Now a **downstream trace from the 11 kV bus reaches the
> LVAC board**, and a **de-energization of the 11 kV isolator shows the station's
> own AC supply going dark** — the blind spot is closed.

And containment, in parallel (the two-graphs discipline): **`SUBSTATION contains
AUX_TRANSFORMER`** and **`SUBSTATION contains LVAC_BOARD`**, both `one_to_many`, so
the station roster now includes its own AC services.

### 4.5 The honest boundary — where the trace stops

Option B closes the AC blind spot, but it does not pretend to model more than it
does. The layer **stops at the board's `T2` fan-out**, and there is a specific
reason:

- The charger, the cooling fans and the tap-changer motors are the *loads* on the
  LVAC board. The **`BATTERY_CHARGER`** is a Chapter-3 class with **`network_role
  'none'`** — it has no terminals, so it *cannot yet be a trace node*. Wiring the
  charger's AC-in edge to the LVAC board is therefore a **deliberately deferred**
  edge: you cannot connect a junction to a network-less feature.
- So the LVAC layer gives you AC traceability **down to the auxiliary wiring runs**
  (`LV_CONDUCTOR` off the board). Naming and wiring the individual loads
  (charger-in, fan-in, motor-in) is future work that waits on those loads gaining
  terminals.

This is the precise seam between Chapters 3 and 4, and it is worth stating as a
model fact rather than a limitation to apologise for:

> ∑ **Two supplies, two graph memberships.** The station's **DC** supply
> (battery → DC bus → trip coils) is modelled as *contained inventory*, its wiring
> drawn as annotation (Chapter 3). The station's **AC** supply (MV bus → aux
> transformer → LVAC board → auxiliary runs) is modelled as *live connectivity*,
> its wiring traceable and solvable (this chapter). The boundary between them is the
> charger — the one device that bridges AC-in to DC-out — and it currently sits on
> the DC (contained) side. When a future slice gives the charger terminals, that one
> edge closes the loop and a trace will run clean from the MV bus to the DC bus.

### 4.6 The station is now complete — structurally

With the LVAC layer in place, every layer of Adenta Primary from Chapter 1's table
now has a home:

- **the place** — the `SUBSTATION` polygon;
- **the yard** — `POWER_XFMR`, `MV_BUSBAR`, switching, `EARTHING_XFMR` (live
  junctions);
- **the control room** — `SUBSTATION_PANEL` + the DC-auxiliary layer (contained,
  network-less);
- **the LVAC services** — `AUX_TRANSFORMER` + `LVAC_BOARD` (contained *and*
  connected).

Every class exists; every field is understood. What remains is to *connect* them —
to build the two graphs in full so the station stops being a pile of placed
features and becomes a network you can trace, solve and export. That is the next
two chapters: connectivity first (Chapter 5), then containment (Chapter 6). Say
**next** for Chapter 5.

---

## Chapter 5 — The connectivity graph

Adenta Primary is a yard full of placed features that are wired to nothing. This
chapter turns it into a **network** — the graph a trace walks, a load flow solves,
a fault study energises. By the end, a downstream trace launched at a 33 kV incomer
will run through the transformer, down the 11 kV bus, out a feeder — and into the
LVAC board. We build up the vocabulary first, then wire the whole station.

### 5.1 Nodes and edges — the two shapes of the graph

The connectivity graph has exactly two kinds of member, and everything you place is
one or the other:

- **Nodes** are **junctions** (`network_role 'junction'`): the transformers,
  busbars, switches, meters — and the LVAC classes. A node has **terminals** —
  named connection points (`HV`, `LV`, `T1`, `T2`, …). Power flows *through* a node,
  in one terminal and out another.
- **Edges** are **conductors** (`network_role 'edge'`): `MV_CONDUCTOR`,
  `LV_CONDUCTOR`. An edge is a *line* geometry and — this is the crucial mechanical
  fact — **has no terminals at all**. It connects at its two endpoints, and it is
  *never itself the "to" side of a rule*. An edge is the wire; a node is the thing
  the wire lands on.

A real electrical path therefore **alternates**: node → edge → node → edge → node.
The 33 kV supply (edge) lands on a breaker (node), whose load side runs a conductor
(edge) to the transformer HV (node), whose LV (node terminal) runs a conductor
(edge) to the 11 kV bus (node), and so on.

> ∑ **Why conductors have no terminals.** A junction is a discrete device with
> countable connection points — a two-winding transformer has an HV and an LV and
> nothing else. A conductor is a continuous span; "which terminal" is meaningless
> for a wire. So VoltEdge models conductors as terminal-less edges that attach at
> their geometry endpoints, and models the *rules* about what a conductor may touch
> from the **node** side (`… → junction.Terminal`). This is why almost every rule
> you have seen names a terminal on the junction and leaves the conductor side
> terminal-free.

Each concrete wire you make is a row in **`runtime.feature_connectivity`** — the
edge instances, as opposed to the `metadata.network_rules` that say which edges are
*allowed*.

### 5.2 `network_rules` — the grammar of legal connections

You cannot wire two features together arbitrarily. Every connection must be
permitted by a **`network_rule`**: a metadata row that says *"a feature of class X
(at terminal a) may connect to a feature of class Y at terminal b."* Its columns:

| Column | Meaning |
| --- | --- |
| `from_feature_class_id` / `from_terminal_id` | the source class and (for a junction) its terminal; a conductor source leaves `from_terminal_id` `NULL` |
| `to_feature_class_id` / `to_terminal_id` | the destination class and terminal |
| `phase_rule` | an optional guard string the connection must satisfy (§5.3) |
| `is_valid` | whether this pairing is allowed |

Three properties of the rule grammar matter:

- **Rules are directed.** `MV_CONDUCTOR → POWER_XFMR HV` and `POWER_XFMR LV →
  MV_CONDUCTOR` are two different rules. Direction is what lets a **downstream**
  trace know which way current flows — it walks *from* the source *to* the
  destination. When the seeds want a device to be walkable in the downstream
  direction, they add a rule with the device on the **`from`** side (that is why
  §2.4's switches have a `Tn → MV_CONDUCTOR` rule as well as the two feeding them).
- **There is no unique constraint on the table.** A "rule set" is intentionally a
  collection of rows, so every seed guards its inserts with `WHERE NOT EXISTS`
  rather than `ON CONFLICT`. Re-running a seed never duplicates a rule.
- **A missing rule is a silent "no."** If you try to connect two features and no
  `is_valid` rule covers the pairing, VoltEdge refuses the wire. This is the model's
  main guardrail against nonsense connectivity (an LV meter onto a 33 kV bus, say) —
  the rules *are* the schematic grammar of your network.

> ∑ **This is why placement ≠ wiring (Chapter 1, restated mechanically).** Placing
> a transformer writes a row in its `gis` table with a geometry. Wiring it writes a
> row in `runtime.feature_connectivity`, and only if a `network_rule` allows it.
> Two different tables, two different acts, gated by two different pieces of
> metadata. A feature can have geometry and no connectivity (placed, unwired), or —
> for a geometry-optional class — connectivity and no geometry (wired on a diagram,
> never surveyed).

### 5.3 `phase_rule` — the voltage-and-phase guard

Some connections are legal only if the electrical characteristics line up. That is
the `phase_rule`: a small predicate evaluated against the two features' fields when
you wire them. You have already met the canonical one:

- **`from.voltage_class = to.primary_voltage`** — on `MV_CONDUCTOR → POWER_XFMR HV`
  (and the direct-tap `MV_CONDUCTOR → AUX_TRANSFORMER T1`). It refuses to land a
  33 kV conductor on a transformer whose HV nameplate says 11 kV. The wire is only
  made if the feeder's voltage matches the winding it feeds.

Where a connection is a plain **device hop** — a busbar's fan-out into a switch, a
transformer's LV winding into a board — the `phase_rule` is left **`NULL`**: no
electrical predicate, just a permitted topological hop. (Look back at §4.4: the
station-service *tap* off the bus is `NULL`, but the direct *feeder* tap carries the
voltage-match rule, because a conductor has a voltage class to check and a busbar
fan-out does not.)

### 5.4 Connectivity is *not* relationships — keep them apart

This is the most common point of confusion, so we state it flatly. VoltEdge has
**two** ways to "link" two features, and they are unrelated systems:

| | **Connectivity** | **Relationships** |
| --- | --- | --- |
| Governed by | `terminals` + `network_rules` | `relationship_classes` |
| Instances in | `runtime.feature_connectivity` | `runtime.feature_relationships` |
| Means | *electrically wired* (power flows) | *contained / owns / supports* (Chapter 6) |
| Read by | trace, load flow, fault | CIM export, "what's inside" queries |
| At the keyboard | the **Connect** tool / terminals tab | the **Relationships** tab |

A `SUBSTATION contains AUX_TRANSFORMER` edge (Chapter 4) is a **relationship**, not
connectivity — the station does not *conduct* to the transformer. Conversely,
`AUX_TRANSFORMER T2 → LVAC_BOARD T1` is **connectivity**, not a relationship — it is
a wire, and it is what a trace follows. Both can exist between the same pair of
features and mean different things; neither implies the other. This is the
Chapter 1 two-graphs tenet at the level of the actual tables.

### 5.5 ▶ Wiring Adenta Primary end to end

Now Kwame wires the station. Working terminal to terminal, and letting the
`network_rules` accept or refuse each wire:

1. **33 kV incomer → bay breaker.** The incoming 33 kV `MV_CONDUCTOR` lands on the
   33 kV `MV_BUSBAR` (`MV_CONDUCTOR → MV_BUSBAR T1`), or via the incomer
   `MV_BREAKER` (`MV_BREAKER T2 → MV_BUSBAR T1`).
2. **33 kV bus → transformer HV.** From the bar's fan-out (`MV_BUSBAR T2`) through
   the transformer bay breaker (`MV_BUSBAR T2 → MV_BREAKER T1`), then a short
   `MV_CONDUCTOR` tail into `POWER_XFMR` T1A's **HV** terminal
   (`MV_CONDUCTOR → POWER_XFMR HV`, and here the **`phase_rule` checks 33 kV = HV
   nameplate**).
3. **Through the transformer.** The engine walks HV → LV internally; Kwame's job is
   the external wire: `POWER_XFMR LV → MV_CONDUCTOR` down to the 11 kV bus
   (`MV_CONDUCTOR → MV_BUSBAR T1`).
4. **11 kV bus → outgoing feeders.** Each outgoing feeder hangs off the 11 kV bar's
   `T2` through its bay breaker (`MV_BUSBAR T2 → MV_BREAKER T1`, then `MV_BREAKER T2
   → MV_CONDUCTOR`) and leaves the station.
5. **The station-service tap.** Off the same 11 kV `MV_BUSBAR T2`, a wire into
   `AUX_TRANSFORMER T1`; its `T2` into `LVAC_BOARD T1`; the board's `T2` out to the
   auxiliary `LV_CONDUCTOR` runs (Chapter 4's four rules).

> ▶ **Now trace it.** Tom launches a **downstream trace** from the 33 kV incomer.
> It walks incomer → breaker → 33 kV bus → transformer HV → (through) → LV → 11 kV
> bus → every feeder breaker → out — *and* down the station-service tap to the aux
> transformer and the LVAC board. Adenta Primary is, at last, a connected network:
> one trace lights up the whole station, primary and auxiliary.

### 5.6 What the wired graph now gives you

Once connectivity exists, the analyses and derived data that read it come alive —
none of which was possible from placement alone:

- **Isolation traces respect the switches.** Open the 11 kV transformer-bay
  `MV_BREAKER` (`device_state = OPEN`) and re-trace: the 11 kV bus, its feeders and
  the LVAC board all fall dark, because the `OPEN` device is a barrier (§2.4). This
  is the outage console's core move.
- **`circuit_ref` is derived by trace.** A feature's circuit is *computed* by
  walking connectivity back to a source, not typed in — `circuit_ref` is a
  derive-only field written only by assign-by-trace, so the map label and
  trace display stay consistent and no dual-writer clobbers it.
- **The load flow has a network to solve.** With voltages, impedances and ratings
  on the nodes (Chapter 2) and a connected graph to walk, the solver can report the
  11 kV bus voltage and each transformer's loading — including the auxiliary load
  drawn through `AUX_TRANSFORMER`. That is Chapter 8.

### 5.7 The connectivity graph in one picture

- The graph is **nodes** (junctions, with terminals) joined by **edges**
  (conductors, terminal-less), alternating node→edge→node.
- Every wire is a `runtime.feature_connectivity` row, allowed only if a **directed
  `network_rule`** permits that terminal-to-terminal pairing; a **`phase_rule`** can
  further gate it on voltage/phase; a **missing rule is a refusal**.
- Connectivity is a **separate system from relationships** — different tables,
  different meaning, different consumers. Wiring is not containment.
- With Adenta wired, one downstream trace runs the whole station, switches gate
  isolation, `circuit_ref` derives itself, and the load flow has something to solve.

That is the electrical graph. Its quiet twin — the **containment** graph, "what is
inside what," and the one consumer that actually depends on it — is Chapter 6. Say
**next**.

---

## Chapter 6 — The containment graph

The connectivity graph answers *"what feeds what."* The containment graph answers a
different question — *"what is inside what"* — and it is quieter, simpler, and
easier to get wrong precisely because it looks like it should imply the wiring. It
does not (Chapter 1). This chapter builds Adenta's containment tree and then does
something the previous chapters deferred: it names the **one consumer** that
actually depends on containment, and shows why the graph exists at all.

### 6.1 Relationships — the family, and where containment sits

Containment is one *kind* of **relationship**. A relationship is a metadata-defined
link between two feature classes, declared as a **`relationship_class`** and
instantiated as a row in **`runtime.feature_relationships`**. VoltEdge ships a small
vocabulary of kinds, and a substation uses several:

| Kind | Meaning | Substation example |
| --- | --- | --- |
| `containment` | **spatial** "is physically inside" — a strict, single-parent tree | `SUBSTATION contains POWER_XFMR` |
| `composite` | **ownership** "owns / is made of" | `SERVICE_POINT owns METER` (outside the station) |
| `spatial_support` | a `many_to_many` support/association | double-circuit tower carrying two circuits |
| `panel_bay` | the `many_to_many` "panel serves bay" association (§3.1) | `SUBSTATION_PANEL serves BAY` |

The one this chapter is about is **`containment`**. Two properties define it:

- **It is a tree — one parent per feature.** Since the Phase 47 "minimal-model
  correction," a piece of equipment is contained by exactly one thing. You will not
  find a breaker that is "inside" two stations.
- **Cardinality is enforced.** `SUBSTATION contains POWER_XFMR` is `one_to_many`; the
  relationship engine (`api/internal/relationship/cardinality.go`) caps the
  *destination* side, so a transformer belongs to at most one station while a
  station holds many. Try to give a feature a second parent and the API refuses.

> ∑ **`containment` vs `composite` — kind, not cardinality.** Both can be
> `one_to_many`. The difference is *meaning*: `containment` is spatial ("the meter
> is physically in the chamber"), `composite` is ownership ("the service point owns
> the meter, wherever it sits"). A station uses `containment` throughout; the
> distinction matters when you extend the model and must pick a kind (Chapter 9).

### 6.2 ▶ Building Adenta's containment tree

Kwame now records what lives inside the station. On the map or in the feature
panel's **Relationships** tab, he creates `containment` edges — none of which is
wiring; all of which are "is inside":

```
SUBSTATION  "Adenta Primary"
├── POWER_XFMR         T1A, T1B
├── MV_BUSBAR          33 kV bus, 11 kV bus
├── MV_BREAKER         each bay breaker
├── MV_ISOLATOR        each isolator
├── EARTHING_XFMR      (if fitted)
├── AUX_TRANSFORMER    the station-service transformer      ← Chapter 4
├── LVAC_BOARD         the station AC board                 ← Chapter 4
└── SUBSTATION_PANEL   PROTECTION / CONTROL / METERING / RTU_SCADA / AUXILIARY_DC
        ├── PROTECTIVE_RELAY   (in the PROTECTION panel)
        ├── METER             (in the METERING panel)
        ├── STATION_BATTERY    ┐
        ├── BATTERY_CHARGER    │ in the AUXILIARY_DC panel  ← Chapter 3
        ├── CONTROL_IED        │
        └── ANNUNCIATOR        ┘
```

Two things to notice about this tree:

- **The `SUBSTATION contains AUX_TRANSFORMER` / `LVAC_BOARD` edges are the ones we
  seeded in Chapter 4.** Those two classes are *both* connected (Chapter 5) and
  contained (here) — the tree and the graph agree about where they live, but they
  are recorded separately.
- **The DC-auxiliary layer appears here and *only* here.** `STATION_BATTERY`,
  `BATTERY_CHARGER`, `CONTROL_IED` and `ANNUNCIATOR` are `network_role 'none'`, so
  they are absent from Chapter 5's connectivity graph entirely. The containment tree
  is the *only* graph they belong to — which is exactly why "list what's in the DC
  board" works while "trace to the charger" does not.

The tree is what powers **"list everything at Adenta Primary"**, the station roster,
and the roll-ups. It says nothing about volts or amps. It is an inventory hierarchy,
and that is all it needs to be — because it has essentially **one demanding
consumer**.

### 6.3 The one consumer — CIM export

Here is the fact that explains why the containment graph exists in the shape it
does: **the CIM/CDPSM export is essentially the only thing that depends on
containment.** The trace and the load flow read connectivity; the map reads geometry
and symbology; but turning VoltEdge into an IEC 61968-13 **CDPSM** model — the
interchange format grid operators swap network data in — needs the *nesting*, and
nesting is containment.

#### ∑ How classes become CIM

The export is driven by three metadata tables (migration 0034), authored in
`seed/cim_mapping_example.sql` and editable in the admin CIM-mappings screen:

- **`cim_profiles`** — the RDF/XML header config (the default is `CDPSM_CIM16`).
- **`cim_class_mappings`** — for each feature class, *which CIM class it becomes* and
  its **`cim_kind`**, the axis the encoder switches on:

  | `cim_kind` | For | CIM shape |
  | --- | --- | --- |
  | `conducting_edge` | line equipment (`MV_CONDUCTOR`) | `ACLineSegment`, 2 synthesized terminals |
  | `conducting_junction` | point equipment (transformers, busbars, switches) | e.g. `PowerTransformer`, `BusbarSection`, `LoadBreakSwitch` |
  | `container` | the nesting tiers | `Substation`, `Bay`, `EquipmentContainer` |
  | `asset` | non-conducting physical (`POLE`, `METER`) | `Asset` / `UsagePoint` |
  | `protection` | protection equipment (no map geometry) | `ProtectionEquipment` |

- **`cim_attribute_mappings`** — the handful of fields that feed CIM attributes (e.g.
  `POWER_XFMR.rated_mva` scaled to VA).

The station's own class, **`SUBSTATION`, maps to CIM `Substation` with
`cim_kind 'container'`** — and *this is where containment earns its keep*: the export
reads the `containment` tree to nest each piece of equipment inside its
`Substation` (and, where present, inside a `Bay`). Without the tree, the exporter
would have a flat bag of equipment and no `EquipmentContainer` hierarchy to place it
in. `BAY` is the one real sub-container tier; the old `VOLTAGE_LEVEL` tier was
**retired** (migration 0086) because voltage is derivable and no longer a modelled
CIM level — it rides on each equipment's `BaseVoltage` instead.

#### ∑ Unmapped ≠ error — it is a *skip*

If a class has no `cim_class_mapping`, the exporter does not fail — it **silently
skips** that class and tallies it as "unmapped." This is deliberate (a partial model
should still export what it can) but it is a real trap: equipment you forgot to map
just *vanishes* from the CIM file with no error. VoltEdge guards against the trap in
three ways you should know:

- an admin **coverage** card (an anti-join) lists the classes with no mapping and
  offers a prefilled "Map…" button;
- the export logs a **CIM WARNING** with the count of dropped/unmapped features;
- a per-class **CIM preview** runs one real feature through the whole encoder and
  shows the byte-exact CIM/XML it produces, so you can confirm a class maps the way
  you expect before a full export.

### 6.4 How the LVAC layer exports

Because we are honest about the newest classes: the LVAC seed (Chapter 4) **includes
its own `cim_class_mappings`**, so `AUX_TRANSFORMER` and `LVAC_BOARD` do *not* fall
into the unmapped trap:

- **`AUX_TRANSFORMER` → `PowerTransformer`** (`conducting_junction`) — a
  station-service transformer *is* a small power transformer in CIM terms.
- **`LVAC_BOARD` → `BusbarSection`** (`conducting_junction`) — the same CIM class the
  LV distribution board `LV_BUSBAR` uses, because that is electrically what it is.

And because both are contained by `SUBSTATION` (§6.2), the exporter nests them
inside the Adenta Primary `Substation` container automatically — the tree and the
mapping working together. (The mapping rows depend on the CIM tables existing; on a
database without migration 0034 they are simply skipped.)

> ∑ **This is the whole design closing on itself.** The LVAC classes were built to
> be *traceable* (connectivity, Chapter 5) **and** *exportable* (containment +
> CIM, this chapter) — the two graphs, both populated, for one subsystem. That is
> what "model it properly" means in VoltEdge: not one relationship, but the right
> membership in each independent graph.

### 6.5 The containment graph in one picture

- Containment is a **relationship** of kind `containment`: a **single-parent tree**
  ("what's inside what"), instantiated in `runtime.feature_relationships`, with
  **cardinality enforced** on the destination side.
- It is **not** connectivity — it carries no power and is read by none of the
  analyses. The DC-auxiliary layer lives *only* here.
- Its **one demanding consumer is CIM export**, which reads the tree to build the
  `Substation` → `Bay` → equipment `EquipmentContainer` nesting; the `VOLTAGE_LEVEL`
  tier was retired, `BAY` remains.
- **Unmapped classes silently skip** the CIM export — guarded by a coverage card, a
  WARNING log, and a per-class preview. The LVAC classes ship their own mappings
  (`PowerTransformer` / `BusbarSection`) and nest under `SUBSTATION`.

Both graphs of Adenta Primary are now built. The station is a network you can trace
and a hierarchy you can export. Two things remain: making it *legible* as a
schematic (the one-line diagram, Chapter 7) and making it *live* under the analyses
(trace, load flow, fault, energization — Chapter 8). Say **next** for Chapter 7.

---

## Chapter 7 — The one-line diagram

A geographic map is the wrong picture of a substation. On the map, Adenta Primary is
a cluster of squares inside a polygon — accurate, but unreadable as a *circuit*. An
engineer reads a station as a **one-line diagram**: a schematic where the busbars are
bars, the transformers are stacked circles, the breakers are boxes, and the topology
— not the geography — is what the layout shows. VoltEdge lets a station own its own
one-line, and this chapter builds Adenta's.

There is also a harder reason the diagram matters. Two whole layers of the station —
the `SUBSTATION_PANEL` enclosures and the entire DC-auxiliary layer (Chapter 3) —
are **`geometry_type 'none'`**. They have no place on the map at all. **The diagram
is their only visual home.** Until you draw the one-line, half the control room is
invisible.

### 7.1 Three flags and one table — the diagram model

Whether a feature can appear on a diagram, and which diagram, is governed by a small
set of metadata knobs. They are worth separating cleanly because they answer
different questions:

| Knob | Question it answers | Where |
| --- | --- | --- |
| `is_diagram_container` | *May this class **own** a diagram?* | `feature_classes` |
| `is_diagram_placeable` | *May this class be **dropped onto** a diagram?* | `feature_classes` |
| `DIAGRAM_TYPE` (domain) | *What **kind** of diagram is this?* (`ONE_LINE`, `CHAMBER_SCHEMATIC`, …) | `domains` / `domain_values` |
| `diagram_placement_rules` | *On **this** container's **this** diagram type, which classes are allowed?* | table (migration 0068) |

- **`SUBSTATION` is a diagram container.** Its class flag `is_diagram_container` is
  flipped `true` (in `seed/diagram_example.sql`), and the `ONE_LINE` diagram type is
  seeded there too. So a station can host a one-line — the schematic *of itself*.
- **The yard and LVAC classes are `is_diagram_placeable`.** Every junction we placed
  in Chapters 2 and 4 set this flag explicitly (§2.1), so they can be stamped onto
  the one-line. So can the geometry-none panels and DC classes (§3.2) — placeability
  is independent of having map geometry.

> ∑ **Container vs. placeable are orthogonal.** A `METER_CHAMBER` is a *container*
> (it owns a `CHAMBER_SCHEMATIC`) and also has map geometry; a `SUBSTATION_PANEL` is
> placeable-but-geometry-less; a busbar is placeable and mapped. "Owns a diagram,"
> "appears on a diagram," and "has map geometry" are three separate properties, and
> a class can hold any combination.

### 7.2 The allow-list — `diagram_placement_rules`, opt-in by absence

By default, *any* `is_diagram_placeable` class may be dropped onto *any* diagram.
That is usually what you want for a station one-line — you might legitimately place a
transformer, a bus, a switch, a panel, an aux transformer, all on the same sheet.

But sometimes a diagram should be **restricted**. The meter-chamber schematic
(§ the LV metering model) should only ever show the busbar and the meters inside
that chamber — not a 33 kV breaker. VoltEdge expresses that with
**`diagram_placement_rules`**: rows that pin a `(container class × diagram type)`
pairing to an explicit set of allowed classes.

The rule is **opt-in by absence**, and this is the elegant part:

- A `(container, type)` pairing **with** rows is *restricted* — `placeFeature`
  rejects any class not on the list, even a globally-placeable one.
- A `(container, type)` pairing **with no rows** is *unconstrained* — open to any
  placeable class.

So the `(METER_CHAMBER, CHAMBER_SCHEMATIC)` pairing has two allow-list rows
(`LV_BUSBAR`, `METER`) and admits nothing else, while the `(SUBSTATION, ONE_LINE)`
pairing has **no** rows and therefore stays open — a station one-line can hold the
whole station. You get tight control where you want it and freedom where you don't,
from the *presence or absence* of rows, with no "unrestricted" flag to maintain.

### 7.3 Map geometry vs. diagram geometry — the busbar draws as a bar

A feature's shape on the map and its shape on a diagram are allowed to differ, and
the busbar is the canonical case (§2.3): `MV_BUSBAR` is a **point** on the map but
sets **`diagram_geometry_type 'line'`**, so on the one-line it is drawn as a **bar**
— the way an engineer expects a bus to look. The diagram is not obligated to echo the
map's geometry; it renders the feature the way the *schematic* convention wants.

Symbology follows the same context-awareness:

- Diagram rendering uses **`render_context 'diagram'`** symbology, falling back to
  the `'map'` rule if there is no diagram-specific one.
- A **geometry-none class has no `'map'` rule to fall back to**, so the DC-auxiliary
  classes each carry an explicit `'diagram'` symbology rule (§3.2) — otherwise they
  would render as no symbol at all on the one-line.
- Richer schematic glyphs — the ANSI one-line **stencils** (a two-/three-winding
  transformer symbol, an open-switch symbol) — are authored as diagram symbology and
  are a natural follow-up for a class that ships with just a default node
  (`POWER_XFMR`, the LVAC classes). Until a stencil exists, the class uses the plain
  diagram node.

### 7.4 ▶ Building Adenta's one-line

1. **Create the diagram.** On the `SUBSTATION` "Adenta Primary," Dana chooses *Create
   diagram* → `ONE_LINE`. (The option is offered because `SUBSTATION` is a diagram
   container.) A blank schematic canvas opens.
2. **Place the yard, top to bottom.** She stamps the 33 kV `MV_BUSBAR` as a bar
   across the top, the two `POWER_XFMR` below it, the 11 kV `MV_BUSBAR` as a second
   bar, and the bay `MV_BREAKER`s as boxes on the connections. Because
   `(SUBSTATION, ONE_LINE)` has no allow-list, nothing is refused.
3. **Place the control room — its first appearance.** She stamps the
   `SUBSTATION_PANEL`s and, on a panel-schematic detail or the same sheet, the
   `STATION_BATTERY` / `BATTERY_CHARGER` / `CONTROL_IED` / `ANNUNCIATOR`. This is the
   **only** view in which those geometry-none features are visible at all.
4. **Place the LVAC tap.** The `AUX_TRANSFORMER` (amber) off the 11 kV bar and the
   `LVAC_BOARD` (olive) below it — the station-service branch, now legible as a
   schematic.

> ∑ **The diagram is depiction, not truth.** A one-line placement records *where a
> feature sits on the schematic* — it is a view. The **connectivity graph**
> (Chapter 5) remains the source of electrical truth; the trace and load flow read
> connectivity, never the diagram. Drawing a line between two symbols on the canvas
> does not wire them; wiring is the terminals-and-`network_rules` act of Chapter 5.
> Keep the two straight: the diagram *shows* the network, it does not *define* it.

### 7.5 A note on the canvas

Each open diagram is its own coordinate space, and the canvas reflects that: a
**per-diagram status bar** shows the cursor's X/Y in that diagram's space, updated
live as you move (independently for each open diagram, since two diagrams have
independent view-boxes). It is a small thing, but it signals the larger point — a
diagram is a genuine authored drawing surface, not a fixed auto-layout of the map.

### 7.6 The one-line in one picture

- A **`SUBSTATION` owns a `ONE_LINE` diagram** (`is_diagram_container`); the yard,
  LVAC, panel and DC classes are **`is_diagram_placeable`** and can be stamped on it.
- **`diagram_placement_rules` restrict a `(container × type)` pairing by *presence*;**
  no rows = open (so the station one-line holds anything), rows = a strict allow-list
  (so the chamber schematic holds only its busbar and meters).
- **Diagram geometry and symbology are context-aware** — the busbar draws as a bar,
  geometry-none classes carry an explicit `'diagram'` rule, ANSI stencils are the
  richer follow-up.
- The diagram is the **only** home of the geometry-none control room, and it is
  **depiction, not the connectivity truth**.

Adenta Primary is now placed, wired, contained, exportable and legible. The last
step is to watch it *behave* — to run the trace, the load flow, the fault study and
the energization analysis *through* the station, station-service transformer and
all. Say **next** for Chapter 8.

---

## Chapter 8 — Making it live

Everything so far has been *building*. This chapter is about *behaviour*: what the
analyses see when they walk into Adenta Primary. The point worth holding onto is that
**not one of the analyses knows what a "substation" is.** There is no substation
solver. The trace, the load flow, the fault study and the energization model all walk
the same connectivity graph you wired in Chapter 5, reading the same fields you set in
Chapters 2–4. The station "works" because it is made of the primitives the engine
already understands — junctions with terminals, edges between them, and a handful of
field names the solver recognises. This is the pay-off of the whole model, and it is
why the LVAC layer swept for free.

We will run the four analyses through Adenta in turn, and watch each one touch the
station-service branch as it goes.

### 8.1 ▶ Trace — the reachability walk

Tom right-clicks the 33 kV incomer and runs a **downstream trace**. The engine walks
`runtime.feature_connectivity` node→edge→node from the start terminal outward:

1. 33 kV busbar → incomer breaker → HV terminal of `POWER_XFMR` #1.
2. Through the transformer to its LV terminal → 11 kV busbar.
3. Out every 11 kV feeder breaker to the distribution network beyond the fence.
4. **And down the station-service tap:** 11 kV busbar → `AUX_TRANSFORMER` T1 →
   through to T2 → `LVAC_BOARD` T1 → T2 fan-out → the LV auxiliary wiring.

The trace reaches the LVAC board with **zero special handling** — it is a junction
with terminals, wired by the four rules of Chapter 4, so the walk simply flows into
it like any other node. That is the whole reason Option B (a traceable subsystem)
was chosen over inventory rows: the station service *appears on the trace*.

> ∑ **A trace is a graph reachability walk, gated by two things.** (1) **`device_state
> OPEN` is a barrier** — the walk does not cross an open switch, so opening the incomer
> breaker prunes the entire station and its service tap from a downstream trace, and
> opening a single feeder breaker prunes only that feeder. (2) **Edges are never the
> "to" side of a rule** — the path always alternates node→edge→node, so a conductor
> cannot be a dead-end the walk originates from. The trace reads *only* connectivity;
> it never consults the containment tree or the diagram.

Because the trace is the thing that assigns `circuit_ref` (Chapter 5, derive-by-trace),
running it is also what stamps the station's equipment — including the aux transformer
and board — with the circuit they belong to.

### 8.2 ∑ Load flow — why the aux transformer is a real step-down node

Now Tom runs the **load-flow** analysis. Here the *field names* you chose in Chapter 4
earn their keep. The engine's `resolveTransformerModel` scans every junction on the
path and asks a single structural question:

> *Does this node carry **both** a `primary_voltage` **and** a `secondary_voltage`
> field?*

If yes, it is treated as a **transforming node** — the sweep steps the voltage base
down across it and applies its impedance. The engine does not check the class name. It
does not care that one node is a `POWER_XFMR` and the other an `AUX_TRANSFORMER`. Both
carry those two field names (that was the deliberate design in Chapter 4), so **both
are swept as step-down transformers, with no engine change whatsoever.**

The rating is read the same field-driven way by `resolveTransformerRating`:

- a `rated_mva` value is taken and multiplied by 1000 to get kVA, **or**
- a `rated_kva` value is taken directly.

`POWER_XFMR` carries its nameplate in MVA; `AUX_TRANSFORMER` carries a small
`rated_kva` (a station-service transformer is a few hundred kVA, not tens of MVA). The
engine reads whichever field is present. So the load flow sees Adenta as: grid source
→ 33 kV base → **step down at the main transformer** → 11 kV base → **step down again
at the aux transformer** → LV base at the board, with I²R losses and a per-node voltage
in per-unit against the ANSI 0.95–1.05 band, exactly as the voltage-profile analysis
reports for any feeder.

> ∑ **This is the model's central bet, stated plainly.** The powerflow engine is
> *field-name-driven, not class-driven*. A device behaves like a transformer because it
> **has the fields a transformer has**, not because someone wrote a `case
> "AUX_TRANSFORMER"` branch. Get the field names right and new equipment participates in
> every numeric analysis the day it is seeded. Get them wrong — name it
> `hv_voltage`/`lv_voltage` — and the same device is invisible to the solver despite
> being perfectly traceable. **Field names are load-bearing API.**

### 8.3 ▶ Fault study — the station as a source and a sink

Tom runs a **fault-current** study at the 11 kV busbar. Two things about a substation
make it the interesting case for fault analysis:

- **It is a fault *sink*.** The study computes the prospective short-circuit current at
  the node by accumulating source impedance back to the grid infeed — through the LV
  and HV windings of `POWER_XFMR` (its `impedance_pct` is exactly why it is on the
  nameplate), through the busbars, to the slack source. The transformer impedance is the
  dominant term; the station is where fault level is *set*.
- **It can be a fault *source*.** If Adenta hosts embedded generation, the generator is
  a second infeed, and the study superimposes its contribution (a three-phase
  superposition on the network's Thévenin impedances) on top of the grid's — so the
  busbar fault level is higher than the grid alone would give. A single-line-to-ground
  fault stays grid-only; the `EARTHING_XFMR`'s `z0_ohm` (Chapter 2) is what sets the
  zero-sequence path for that case.

The clearing device the study names is whichever protective element sits upstream on
the connectivity path — the bay breaker — which is *why* the switching devices are
first-class junctions and not attributes: the fault study has to be able to *point at*
the device that clears the fault.

> ∑ The aux transformer participates here too — its `impedance_pct` adds series
> impedance on the station-service branch — but its rating is small, so it is a modest
> contributor. The honest picture: it is swept, it is accounted, and it is not where the
> station's fault level lives.

### 8.4 ▶ Energization & outage — de-energizing a station

Finally, Priya (operator) works the station under **OMS**. When an incident opens a
switch, OMS does not merely *record* the open — it **flips the live `device_state`** on
that device in the same transaction (Phase 62 / OMS operational-state). Every analysis
that reads `device_state` therefore reacts at once: the trace prunes, the load flow
re-sweeps, the map re-symbolises the open switch in red.

Two OMS reads are the substation-shaped ones:

- **The de-energized cone** (`trace.DeEnergizedCone`): given an open device, what falls
  dark downstream. Open Adenta's incomer and the cone is the *entire station plus its
  service tap plus everything the 11 kV feeders supply* — the trace walk of §8.1, run
  from the open point and inverted into "what lost supply."
- **Incident energization** (`oms.IncidentEnergization`, `GET …/energization`): for an
  outage, which affected sections are still energized from an alternate source (a
  back-feed through a normally-open tie) versus truly dead. This is the contingency
  question of the advanced analyses, asked live against the incident.

> ∑ **The service-consumption dimension rides on top of all of this.** Because the trace
> reaches the meter (Phase 62, "the network ends at the meter"), every one of these
> walks — downstream trace, de-energized cone, incident energization — carries a
> **`ServiceRollup`**: how many customers, how much connected kW/kVA, and how many
> *critical* loads (graded `load_priority` P1–P4, critical = rank ≤ P2) sit in the
> affected set. So "de-energize Adenta Primary" does not just return a list of dark
> equipment; it returns *"N customers, X kW, K critical services affected."* The
> substation stops being plant and becomes **supply** — which is the whole reason it
> exists.

### 8.5 The station, alive, in one picture

- **No analysis knows what a substation is.** Trace, load flow, fault and energization
  all walk the **connectivity graph** and read **fields** — the station behaves because
  it is built from primitives the engine already understands.
- **The trace** is a reachability walk gated by `device_state OPEN`; it reaches the LVAC
  board like any node and stamps `circuit_ref`.
- **The load flow** is **field-name-driven** — any node with `primary_voltage` +
  `secondary_voltage` is a step-down transformer, so `AUX_TRANSFORMER` sweeps with zero
  engine change. *Field names are load-bearing API.*
- **The fault study** sees the station as both a **sink** (transformer `impedance_pct`
  sets fault level) and a **source** (embedded generation superimposes); the clearing
  device is a real junction it can point at.
- **OMS** flips **live `device_state`**, so opening a switch instantly prunes traces and
  re-sweeps flow; the **de-energized cone** and **incident energization** answer "what
  went dark / what is back-fed," each carrying a **`ServiceRollup`** — the station
  measured as customers and critical load, not just equipment.

Adenta Primary is now built, wired, contained, exportable, legible **and** live under
every analysis. One thing remains: to put it all together as a repeatable procedure —
the end-to-end build of a station from an empty map, with the checklists and the
appendix reference tables you will actually reach for. Say **next** for Chapter 9.

---

## Chapter 9 — Building Adenta end-to-end

The previous eight chapters explained the model one layer at a time. This one runs the
whole thing forward as a **single procedure**: Kwame sits down with an empty map and a
commissioning drawing for Adenta Primary, and builds it to the point where Tom can
trace it and Priya can operate it. Then come the checklists and the reference tables —
the part of the guide you will keep open on a second monitor.

### 9.1 ▶ The build, in order

The order matters, because connectivity is directional and a wire cannot land on a node
that does not exist yet. Build **outside-in and top-down**: the place, then the yard
from the incomer down, then the control room, then the LVAC tap, then wire, then draw.

1. **The place.** Create the `SUBSTATION` polygon "Adenta Primary" around the site
   boundary. Everything else will be *contained* by it. (Nothing is connected yet — the
   polygon is `network_role 'none'`.)
2. **The yard, HV to LV.** Place, in electrical order:
   - the 33 kV `MV_BUSBAR` (a point that will draw as a bar on the one-line);
   - the incomer and bay `MV_BREAKER`s;
   - the two `POWER_XFMR` (subtype `33/11`, nameplate `rated_mva`, `impedance_pct`);
   - the 11 kV `MV_BUSBAR`;
   - the feeder `MV_BREAKER`s;
   - the `EARTHING_XFMR` on the transformer neutral (`z0_ohm`).
3. **The control room.** Place the `SUBSTATION_PANEL`s (geometry-none) and, contained
   under them, the P&C DC layer — `STATION_BATTERY`, `BATTERY_CHARGER`, `CONTROL_IED`,
   `ANNUNCIATOR` — plus the `PROTECTIVE_RELAY` / `MV_CT` / `MV_VT` that measure the bays.
   None of these are on the connectivity graph; they are *contained*, and they will
   first become visible on the one-line in step 7.
4. **The LVAC tap.** Place the `AUX_TRANSFORMER` (small `rated_kva`,
   `primary_voltage`/`secondary_voltage`) off the 11 kV bus, and the `LVAC_BOARD` below
   it.
5. **Wire the connectivity graph.** Now that every node exists, connect terminals in
   path order — 33 kV bus → breaker → `POWER_XFMR` HV, LV → 11 kV bus → feeder breakers,
   and the service tap 11 kV bus → `AUX_TRANSFORMER` T1, T2 → `LVAC_BOARD` T1, T2 → LV
   aux. Each hop must have a matching `network_rule`, or the connect is silently refused
   (Chapter 5).
6. **Establish containment.** Nest the equipment under the `SUBSTATION` (and the DC
   devices under their `SUBSTATION_PANEL`s). This is what CIM export will read.
7. **Draw the one-line.** Create the `ONE_LINE` diagram on the `SUBSTATION`, stamp the
   yard as bars/boxes, and place the geometry-none control room — its first and only
   visual appearance.
8. **Run it.** Trace downstream from the incomer (this stamps `circuit_ref`), run the
   load flow (the aux transformer sweeps as a step-down), run a fault study at the 11 kV
   bus, and hand it to OMS.

> ∑ **Why this order is not arbitrary.** Steps 1–4 create *nodes*; step 5 creates
> *edges between them*. You cannot reverse them — a `network_rule` connect needs both
> endpoints to exist. Containment (step 6) and the diagram (step 7) are *views* layered
> on top and can be done anytime after the features exist. The analyses (step 8) read
> the graph from step 5. If you remember one thing about sequencing: **nodes before
> wires, wires before views, views before analyses.**

### 9.2 The seed author's path (for a new class)

Kwame placed *instances* above. When the gap is a whole *class* that does not exist yet
— as the LVAC layer was before this project — the path is the one the
`lvac_auxiliary_example.sql` seed followed, and it is worth stating as its own checklist
because it is the deeper skill:

1. **Decide the shape first** — point/line/polygon, and `network_role` junction / edge /
   none. (Junction = traceable node with terminals; edge = conductor; none =
   contained-but-not-connected.) This one decision determines everything downstream.
2. **Reuse field names that the engine already understands.** If it transforms voltage,
   name the fields `primary_voltage`/`secondary_voltage` and `rated_kva`/`rated_mva` —
   then it sweeps in the load flow for free (§8.2). This is the single highest-leverage
   choice in the whole seed.
3. **Reuse existing domains and field groups** — `VOLTAGE_CLASS`, `PHASE`,
   `CONDITION_RATING`, the `electrical`/`maintenance` groups — so you add no new domain
   values and inherit validation.
4. **Add the terminals** (subtype-agnostic unless the network must see a difference).
5. **Add the `network_rules`** — every hop in and out, each guarded `WHERE NOT EXISTS`,
   `phase_rule` where a voltage match matters and `NULL` for a plain device hop.
6. **Add containment + CIM mappings + symbology** (map and, for geometry-none classes, a
   `'diagram'` rule).
7. **Apply and compile:** `task seed-<name>`, then `task compile CODE=<CLASS>` to build
   the physical `gis.<code>` table. Place `seed-<name>` in `seed-all` *after* any seed
   whose class its rules reference (the LVAC seed must run after `seed-mv-busbar`, or the
   busbar-tap rule finds no busbar to join and is silently skipped).
8. **Verify by behaviour:** place two instances, connect them, trace across them, and —
   for a transforming class — confirm the load flow steps voltage across the node. A
   class is not "done" when it compiles; it is done when it *traces and sweeps*.

> ▶ This is exactly what remains owed for the LVAC layer built earlier in this project:
> the seed is written and reviewed, but `task seed-lvac-auxiliary` +
> `task compile CODE=AUX_TRANSFORMER` + `task compile CODE=LVAC_BOARD` + a downstream
> trace and load-flow re-check still need a live database to run against.

### 9.3 Commissioning checklists

Reach for these when a station "looks built" but you want to know it is *actually*
complete.

**Yard**
- [ ] Every transformer has `primary_voltage`, `secondary_voltage`, and a rating
  (`rated_mva` **or** `rated_kva`) — or it will not sweep.
- [ ] Every busbar's `T2` fan-out reaches the devices it feeds.
- [ ] Every switching device has a `device_state` and a `normal_state`.
- [ ] The earthing transformer carries `z0_ohm` (else SLG faults have no zero-sequence
  path).

**Control room**
- [ ] Each `SUBSTATION_PANEL` is contained by the `SUBSTATION`.
- [ ] Each DC device (`STATION_BATTERY`/`BATTERY_CHARGER`/`CONTROL_IED`/`ANNUNCIATOR`)
  is contained by a panel and has a `'diagram'` symbology rule (it has no map form).
- [ ] Panels that serve bays have their `panel_bay` relationships established.

**LVAC**
- [ ] The `AUX_TRANSFORMER` is tapped off an energised MV source (busbar or conductor).
- [ ] Its field names match the transformer contract (so it sweeps).
- [ ] The `LVAC_BOARD` fans out from `AUX_TRANSFORMER` T2.

**Graphs & views**
- [ ] A downstream trace from the incomer reaches the LVAC board *and* the feeders.
- [ ] The containment tree nests everything under the `SUBSTATION` (check the CIM
  preview for silent drops — an unmapped class exports nothing).
- [ ] The `ONE_LINE` diagram shows the geometry-none control room.
- [ ] The load flow reports a sane per-unit voltage at the 11 kV bus and at the board.

### 9.4 Appendix A — the substation classes

| Class | Geometry | `network_role` | Role in the station |
| --- | --- | --- | --- |
| `SUBSTATION` | polygon | none | The place; containment root |
| `POWER_XFMR` | point | junction | Main step-down transformer (MVA) |
| `MV_BUSBAR` | point (bar on diagram) | junction | Bus; T1 in, T2 fan-out |
| `MV_BREAKER` / `MV_ISOLATOR` / `MV_SECTIONALIZER` | point | junction | Switching; `device_state` |
| `EARTHING_XFMR` | point | junction | Neutral earthing; `z0_ohm` leaf |
| `SUBSTATION_PANEL` | none | none | Control cubicle; contains DC + relays |
| `STATION_BATTERY` / `BATTERY_CHARGER` / `CONTROL_IED` / `ANNUNCIATOR` | none | none | P&C DC secondary; contained, not connected |
| `PROTECTIVE_RELAY` / `MV_CT` / `MV_VT` | point/none | none | Protection & instrument |
| `AUX_TRANSFORMER` | point | junction | Station-service transformer (kVA); sweeps |
| `LVAC_BOARD` | point | junction | Station LVAC board; T2 fan-out |

### 9.5 Appendix B — the LVAC connectivity rules

| From (class · terminal) | To (class · terminal) | `phase_rule` |
| --- | --- | --- |
| `MV_BUSBAR` · T2 | `AUX_TRANSFORMER` · T1 | `NULL` (station-service tap) |
| `MV_CONDUCTOR` | `AUX_TRANSFORMER` · T1 | `from.voltage_class = to.primary_voltage` |
| `AUX_TRANSFORMER` · T2 | `LVAC_BOARD` · T1 | `NULL` |
| `LVAC_BOARD` · T2 | `LV_CONDUCTOR` | `NULL` (aux fan-out) |

### 9.6 Appendix C — the field-name contract (why zero-engine-change works)

| If a node carries… | …the engine treats it as | Read by |
| --- | --- | --- |
| `primary_voltage` **and** `secondary_voltage` | a step-down transformer | `resolveTransformerModel` |
| `rated_mva` | rating = value × 1000 kVA | `resolveTransformerRating` |
| `rated_kva` | rating = value kVA | `resolveTransformerRating` |
| `impedance_pct` | series impedance (load flow + fault) | sweep / fault study |
| `z0_ohm` | zero-sequence path (SLG fault) | fault study |
| `device_state = OPEN` | a trace barrier | every graph walk |
| `load_priority` (P1–P4) | service criticality (critical ≤ P2) | `ServiceRollup` |

> ∑ This table *is* the thesis of the guide. A substation is not a special object the
> software knows about; it is an arrangement of ordinary junctions and edges whose
> **field names** happen to be ones the engine reads. Model a new piece of station plant
> with the names in this table and it participates — traces, sweeps, faults, energizes —
> the day you seed it.

### 9.7 The whole guide, in one breath

A substation in VoltEdge is **a place plus a population**, over **two independent
graphs** — a **containment tree** (what is inside what; read almost only by CIM export)
and a **connectivity graph** (what feeds what; walked by every analysis). You build it
**outside-in and top-down** — place, yard, control room, LVAC, then wire, then contain,
then draw, then run. The control room's DC layer is **contained but not connected**; the
LVAC layer is **contained *and* connected**, which is why it traces and sweeps. And the
reason any of it behaves is that **the analyses do not know what a substation is** — they
read a connectivity graph and a handful of load-bearing field names. Get the shape and
the names right, and Adenta Primary is not a diagram of a station. It *is* one.

---

*End of the Substation Guide. You have the mental model (Ch 1), the three layers
(Ch 2–4), both graphs (Ch 5–6), the schematic (Ch 7), the live behaviour (Ch 8), and
the build procedure with reference tables (Ch 9). Keep §9.4–§9.6 open when you model;
re-read §8.2 and §9.6 when a new device refuses to sweep.*
