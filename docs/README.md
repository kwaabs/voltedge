# VoltEdge Documentation — Start Here

*How to use the VoltEdge documentation set: what each document is, who it's for,
and the order to read them in.*

---

## What this page is

VoltEdge ships a lot of writing. This page is the **front door** to it — a short
guide to the guides. It exists so that you never have to open six files to find
out which one answers your question. Read the two-minute router below, jump to
the one manual you need, and come back here only when your question changes.

The documentation falls into **four tiers**, and knowing which tier you're in
saves the most time:

| Tier | What it is | Read it when… |
| --- | --- | --- |
| **1. The manuals** | Seven polished, comprehensive guides written to be *read* — how-to and reference. | You want to *use, administer, or model* the product. **This is where almost everyone should start.** |
| **2. Module deep-dives** | Design/architecture notes for one subsystem (asset tags, faults, the feature registry, import engine…). | You are extending or debugging that specific subsystem and want its rationale. |
| **3. Phase build-notes** | The sequential engineering record — `phase-0` … `phase-58` — one file per build slice. | You are a developer tracing *why* or *when* something was built the way it is. |
| **4. Operational references** | Short, practical lookups: the command cheatsheet and the open-items log. | You need to run the stack, or check what's deliberately unfinished. |

If you only remember one thing: **the seven manuals in Tier 1 are the
documentation**; Tiers 2–4 are the workshop behind them.

---

## The two-minute router — "I want to…"

Find your task on the left; open the document on the right.

| I want to… | Go to |
| --- | --- |
| Sign in, find assets, edit the network, run a trace (the office web app) | **[User Handbook](user-handbook.md)** |
| Work offline in the field — download a work-area pack, view and edit the network, capture GPS/photos, run inspections, sync back | **[Field Tool Guide](field-tool-guide.md)** |
| Run the field tool: manage packs, review and resolve field edits, tune build/sync for a fleet | **[Field Tool Admin Guide](field-tool-admin-guide.md)** |
| Get the *step-by-step recipe* for one map task — create, edit, measure, snap, connect, annotate | **[Everyday Operations](everyday-operations.md)** |
| Understand what a load-flow / fault / arc-flash study actually computes | **[Engineering Studies](engineering-studies.md)** |
| Model an EV charging site and size what a feeder can absorb (managed charging, headroom) | **[EV Charging & Feeder Headroom](ev-charging-guide.md)** |
| Provision users, author feature classes, stand up an environment, use break-glass | **[Admin & Break-Glass Guide](admin-break-glass-guide.md)** |
| Log a defect, raise a work order, build an inspection checklist | **[Works, Inspections & Maintenance Guide](works-inspections-maintenance-guide.md)** |
| Capture and wire up a substation, end to end | **[Substation Guide](substation-guide.md)** |
| Look up one feature class — its fields, terminals, network role, symbology | **[Feature Catalog](feature-catalog.md)** |
| Look up what a field *means* before entering data — every class, every column, in a line | **[Data Dictionary](data-dictionary.md)** |
| Model the LV network and metering — conductors, pillars, service points, meters, consumption | **[LV Network & Metering](lv-and-metering-guide.md)** |
| Understand feeders, protection zones, switching sections and how VoltEdge does subnetworks | **[Feeders, Sections & Subnetworks](network-sectioning-guide.md)** |
| Model switchgear and protection — switches, breakers, reclosers, fuses, RMUs, relays and coordination | **[Switching & Protection](switching-and-protection-guide.md)** |
| Draw an SVG icon, attach it to a class, and style features by their field values | **[Symbology & Map Styling Guide](symbology-guide.md)** |
| Price a slice of the network as a Bill of Quantities, or maintain the cost catalog | **[Bill of Quantities & Cost Catalog](bill-of-quantities-guide.md)** |
| Configure logging, correlate a request by its id, or ship logs off the box | **[Logging & Observability Guide](logging-and-observability-guide.md)** |
| Deploy to production at scale — replicas, load balancers, DB replicas, Kubernetes | **[Production Deployment & Scaling](deployment-and-scaling-guide.md)** |
| Actually stand up the production stack step by step — env, images, migrate, TLS | **[Production Setup Runbook](production-setup-runbook.md)** |
| Run the stack on Coolify or Kubernetes — PaaS wrapper or native manifests | **[Deploying on Kubernetes & Coolify](kubernetes-and-coolify-guide.md)** |
| Understand how the asset-tag / faults / registry / import subsystem is built | The matching **module deep-dive** (see Tier 2 below) |
| Run the database, compiler, or server by hand | **[Command cheatsheet](commands-cheatsheet.md)** |
| See what's still outstanding across the project | **[Open-items log](open-items.md)** |

---

## Tier 1 — The seven manuals

These are the documents to read. Each is self-contained, chaptered, and written
in the same house voice (see [Conventions](#conventions-shared-across-the-manuals)
below). They cross-reference each other, so a link out of one lands you in the
right place in another.

### 1. [User Handbook](user-handbook.md) — *start here if you're new*

The everyday, **how-to-first** guide for the people who use VoltEdge day to day:
viewers, analysts, editors, and field crews. It builds the core mental model
(feature classes, terminals, territory, permissions) and then walks every
workflow: signing in, browsing and querying the network, creating and editing
features, connectivity, tracing, the operations modules (OMS / WAM / Faults),
diagrams, the offline field app, external map layers, and asset tags. Thirteen
chapters plus a permission-reference appendix.

**Read it if:** you *use* the product at all. It's the trunk the other manuals
branch off. **Who's in it:** Priya (viewer/dispatcher), Tom (analyst), Dana
(editor/field supervisor).

### 2. [Engineering Studies & Network Analysis](engineering-studies.md)

The companion to Handbook Chapter 8. Where the handbook shows you *which button
to press*, this explains *what each study does* — the model it builds, the
assumptions it makes, and the maths it runs — for all twelve `/analysis` studies
(radial/meshed/nodal load flow, fault current, protection coordination, ampacity,
arc-flash, contingency, hosting capacity, EV headroom). Every section is written
in **two layers**: a plain-language **▶ operator** part and a **∑ method & math**
part you can skip or dive into independently. Nine chapters plus a formula-sheet
appendix.

**Read it if:** you run studies and need to *trust and defend the number*.
**Grounded in:** the real solver code under `api/internal/powerflow`, not a
textbook ideal.

### 3. [Administration & Break-Glass Guide](admin-break-glass-guide.md)

The reference for the people who **run** VoltEdge rather than use it: provisioning
identities, authoring the metadata that shapes the whole application (feature
classes, fields, domains, symbology), standing up an environment, and holding —
and safely using — the break-glass account of last resort. Its spine is the RBAC
+ spatial-access model and the crucial fact that **all enforcement lives in Go,
not in the database**. Nine chapters.

**Read it if:** you administer the platform. **Who's in it:** Ada (system admin)
and Owen (break-glass admin), alongside the users whose access they manage.

### 4. [Works, Inspections & Maintenance Guide](works-inspections-maintenance-guide.md)

The how-to manual for the **Works** area — the Work & Asset Management (WAM)
module. Covers the three linked record types (defects, work orders, inspections),
the closed loop they form, running them on a maintenance cycle, and taking Works
into the field offline. It's honest about the edges (e.g. recurring auto-generated
maintenance plans aren't built yet). Nine chapters.

**Read it if:** you find, plan, or execute work on assets. **Who's in it:** Dana
(office planner), Femi (field technician), Priya (read-only), Tom (analyst).

### 5. [Substation Guide](substation-guide.md)

A single narrative that **braids two books**: a step-by-step **▶ operator lane**
for capturing a real station in the app, and a **∑ model lane** explaining why
each class, terminal, and relationship is shaped the way it is. One running
example — *Adenta Primary*, a 33/11 kV primary — is built from an empty map to a
fully traceable, load-flowable, CIM-exportable station. Nine chapters.

**Read it if:** you model substations, or you extend the substation data model.
**Who's in it:** Kwame (primary-plant design engineer), with the usual cast.

### 6. [Feature Catalog](feature-catalog.md)

The **reference you keep open on a second monitor while you model.** One entry
per feature class (~59 classes across ten chapters, organised by electrical
layer), each under the same seven headings: what it is, what it does, how it's
specified (subtypes/fields), its network role, its terminals, what it relates to,
and where it's drawn. Not a tutorial — a lookup.

**Read it if:** you need the exact fields, terminals, or symbology of one class.
**Pairs with:** the Substation Guide (narrative) and Engineering Studies (the
engine that reads these classes).

### 6b. [Data Dictionary](data-dictionary.md)

The **key you hold while loading data.** Every feature class and every field
with a one-line meaning, plus the system columns that sit on *every* table and
the shared coded domains (`PHASE`, `VOLTAGE_CLASS`, …) that fields draw from.
Terser than the Feature Catalog — no narrative, just "what does this column
mean and what may I put in it."

**Read it if:** you're about to enter or import data and want to get the fields
right the first time.
**Pairs with:** the Feature Catalog (the fuller per-class reference).

### 7. [Everyday Operations](everyday-operations.md) — *the task cookbook*

The **hands-on companion to the User Handbook.** Where the handbook explains
*what VoltEdge is* and *why* it behaves as it does, this book is organised around
**what you actually do** — one self-contained recipe per task: creating features
of every geometry, editing attributes and geometry, measuring, snapping,
connecting and relating, and annotating the map. Each recipe pairs a **terse ▶
quick-steps** block with a **comprehensive worked example** by the recurring cast,
then a *how it works* and *tips & gotchas*. Ten chapters.

**Read it if:** you know roughly what you want to do and just need the exact
sequence. **Who's in it:** Dana (editor), Priya (viewer), Tom (field), Ada (admin).

### Also in Tier 1 — focused how-to & administration guides

Added to the library as the product grew, in the same house voice:

- **[Symbology & Map Styling Guide](symbology-guide.md)** *(Administration — needs `metadata:read`)* — drawing SVG icons, attaching them to classes, and styling features by subtype and field value.
- **[Bill of Quantities & Cost Catalog](bill-of-quantities-guide.md)** *(How-to)* — pricing any scope of the network as a costed schedule (needs only `features:read`), and administering the price book behind it: currencies, unit rates and per-feature overrides (`metadata:write`).
- **[Logging & Observability Guide](logging-and-observability-guide.md)** *(Administration — needs `admin:users`)* — what the services log and how: configuring format and level, correlating a request by its `X-Request-Id`, the PII policy, and shipping logs to an aggregator (Vector/Promtail → Loki → Grafana).
- **[Feeders, Sections & Subnetworks](network-sectioning-guide.md)** *(Manual)* — VoltEdge's lean subnetwork model: feeders (= circuits), protection zones vs switching sections, the `boundary_role` device backbone, on-demand section highlighting, sections in trace/load-flow results, and feeder freshness — with a comparison to the heavyweight utility-network approach and what's deliberately left out.
- **[Switching & Protection](switching-and-protection-guide.md)** *(Manual)* — the switching and protection devices end to end: `MV_SWITCH`/`LV_SWITCH`, `MV_BREAKER`, `MV_ISOLATOR`, `MV_RECLOSER`, `MV_SECTIONALIZER`, `MV_FUSE` and the `RMU` (plus the bus-coupler and feeder-tie *fields*, not classes); the `device_state` vs `operational_state` axes; how an open device becomes a trace barrier, de-energises a cone, and bounds a switching section vs a protection zone; ties/couplers/RMU fan-out; and the `PROTECTIVE_RELAY` → TCC curve → coordination chain with CTI grading. Cross-references Engineering Studies for the fault/arc-flash math.
- **[LV Network & Metering](lv-and-metering-guide.md)** *(Manual)* — modelling the low-voltage network and metering end to end: `LV_CONDUCTOR`, cut-outs, pillars and the `LV_PILLAR` assembly, `SERVICE_POINT`, the inline revenue `METER` (and the SP-owns-METER model), MV metering (`MV_METERING_POINT`/`MV_METER`), energy-accounting `NETWORK_METER`, the seven supply chains, and the consumption rollup. Worked from the seeded test + ring networks.

---

## Tier 2 — Module deep-dives

These document **one subsystem's design and rationale** — closer to architecture
notes than to a user manual. Reach for them when you're extending or debugging
that subsystem. The main ones:

- **[asset-tagging-module.md](asset-tagging-module.md)** — the scannable-code
  identity layer over every feature (QR + Code128), why it rides
  `runtime.feature_registry` instead of its own schema, and the tag lifecycle.
- **[feature-registry.md](feature-registry.md)** — the cross-class `id → class`
  index kept in sync by a compiler-attached DB trigger; the backbone several
  other subsystems build on.
- **[faults-module.md](faults-module.md)** — the first-class fault-event record
  that sits between OMS (outage) and WAM (defect).
- **[etl-import-engine.md](etl-import-engine.md)** — the admin import engine
  (CSV / GeoJSON / Shapefile / KML / DXF, match-on-field upsert).
- **[embedded-generation.md](embedded-generation.md)** — how generators are
  modelled as negative demand and as a fault-current infeed.
- **[security-input-hardening.md](security-input-hardening.md)** — the
  input-validation and SSRF/injection posture across the API.
- **[metadata-core.md](metadata-core.md)** — the metadata model everything else
  compiles from.

### Tier 3 — Phase build-notes (`phase-*.md`)

The **sequential engineering record**: one file per build slice, `phase-0-skeleton`
through `phase-58-spatial-access-control` (plus a few named slices like
`phase-rmu` and `phase-bus-coupler`). Each captures what a slice built and why,
including its "what's still open" notes. This is a developer archive — you read a
*specific* phase doc to understand the history of one area, not the set front to
back. When a phase's decisions later get promoted into a manual, the manual is the
current truth and the phase doc is the provenance.

### Tier 4 — Operational references

- **[commands-cheatsheet.md](commands-cheatsheet.md)** — every operational
  command as a literal one-liner (Docker / Go / compiler / migrations), for
  environments where running saved scripts isn't allowed.
- **[open-items.md](open-items.md)** — the single index of what's deliberately
  left undone (and what's since been closed) across every phase, with a `Status`
  column so "what's outstanding" is answerable at a glance.
- **[test-network.md](test-network.md)** — the seeded example network the docs
  and tests lean on.

---

## Conventions shared across the manuals

Once you've read one manual, the others feel familiar. They deliberately share:

- **A recurring cast.** The same people appear throughout so examples stay
  concrete: **Priya** (read-only dispatcher/viewer), **Tom** (network analyst),
  **Dana** (editor / field supervisor), **Femi** (field technician), plus
  role-specific additions — **Ada**/**Owen** (system & break-glass admins) in the
  admin guide, **Kwame** (design engineer) in the substation guide. A shared
  example feeder (**Feeder 7**, 11 kV) and station (**Adenta Primary**, 33/11 kV)
  run through several.
- **The ▶ / ∑ two-lane pattern.** In the Engineering Studies and Substation
  guides, a **▶** marker introduces the plain-language "how to operate it" lane
  and a **∑** marker introduces the deeper "why / the maths" lane. The ▶ parts
  stand alone; skip or dive into the ∑ parts as you like.
- **A permission & territory legend.** In the how-to manuals, **🔒** marks an
  action that needs a specific permission (named inline), **🗺️** marks something
  that also depends on your **territory access**, **💡** is a tip, and **⚠️** is a
  caution. If a screen or button in a manual isn't visible to you, the usual
  reasons are: you don't hold the permission, it's outside your territory, or
  your build predates it.
- **"What's built" honesty.** Every manual documents what actually exists in the
  code and is explicit about capabilities that are planned but not yet built,
  rather than implying a button that isn't there.

---

## A suggested reading path

You don't need to read everything, or in this order — but if you're onboarding:

1. **Everyone:** [User Handbook](user-handbook.md), Chapters 1–3 (concepts,
   getting started, permissions). This is the shared vocabulary the rest assumes.
2. **Then, by role:**
   - *Analyst / planner* → the rest of the Handbook, then
     [Engineering Studies](engineering-studies.md).
   - *Editor / modeller* → the rest of the Handbook, then the
     [Substation Guide](substation-guide.md), with the
     [Feature Catalog](feature-catalog.md) open alongside.
   - *Operations / maintenance* → the
     [Works, Inspections & Maintenance Guide](works-inspections-maintenance-guide.md).
   - *Administrator* → the
     [Admin & Break-Glass Guide](admin-break-glass-guide.md) (and keep the
     [command cheatsheet](commands-cheatsheet.md) handy).
3. **Developers:** skim the module deep-dive for the area you'll touch, and use
   the phase build-notes as the history when you need the "why".
