# The VoltEdge CIM Export Guide

*What a CIM/XML export is, how to produce one, and how VoltEdge turns your
network into a standards-compliant model file.*

---

## How to read this guide

This is the tenth VoltEdge manual, and like the
[Substation Guide](substation-guide.md) it is deliberately two books braided
into one. Readers arrive with very different questions.

- A **GIS operator** asks *"I've been asked to send our network to the planning
  team as a CIM file — which button, which options, and how do I know it
  worked?"*
- An **administrator** asks *"a whole class came out empty — where do I fix the
  mapping, and how do I see what's covered before I export?"*
- An **integration engineer** asks *"I have to load this into OpenDSS / CYME /
  PowerFactory — what exactly is in the file, how is the network topology
  encoded, and what are the known gaps I need to work around?"*

Rather than split these into three documents that drift out of sync, this guide
answers all three at once, chapter by chapter, in two lanes:

- **The ▶ operator lane** — the step-by-step "click here, set that, read this
  warning" path for producing and sanity-checking an export, and for editing
  the mappings that drive it. If you only need to *use* VoltEdge to emit CIM,
  you can read just these.
- **The ∑ model lane** — the "why the file looks like this" reference: what CIM
  is, how each feature becomes a typed CIM object, how connectivity becomes a
  topology graph, and where the fidelity limits are. Grounded in the actual code
  under `api/internal/export` and the seed mappings under `/seed`. If you consume
  the file downstream, or extend the mapping, these are for you.

Both lanes share one running example — the **Oakwood Zone** feeder we have used
throughout the engineering docs, centred on transformer **TX-Feeder7-014** (a
315 kVA 11 kV/415 V `DIST_XFMR`) and the 11 kV line feeding it. Every abstract
point about the file format lands on a concrete row of Oakwood's export.

This guide assumes you have met the core VoltEdge concepts — feature classes,
subtypes, terminals, connectivity, containment — in the
[user handbook](user-handbook.md) and, for the network side, the
[Substation Guide](substation-guide.md). The original build-notes for this
subsystem live in [phase-16-cim-export.md](phase-16-cim-export.md); where that
doc and this one disagree, **this one is current** (Phase 16 predates several
mapping additions and still uses the old `SUBSTATION_YARD` class name).

> **A note on honesty.** VoltEdge's CIM export is real and used, but it is not a
> perfect, loss-free CIM implementation — no practical one is. This guide names
> what it does faithfully, what it approximates, and what it drops, in a
> dedicated **Coverage & gaps** chapter and inline wherever a limit bites. If you
> are about to hand the file to a solver, read that chapter.

### The cast

| Who | Role | Shows up when |
| --- | --- | --- |
| **Priya** | Read-only viewer / GIS operator | Running an export, reading the result and its warnings |
| **Ada** | Administrator | Editing the mapping tables, reading coverage, previewing a class |
| **Tom** | Network analyst / integration engineer | Consuming the file in a downstream power-system tool |
| **Dana** | Network editor & planner | Owns the connectivity and containment that topology is built from |

### Legend

The same symbols used across the VoltEdge manuals:

- 🔒 **Permission** — the RBAC permission a step needs.
- 🗺️ **Territory** — where spatial access scoping changes what you get.
- 💡 **Tip** — a shortcut or a good habit.
- ⚠️ **Caution** — a place the model can mislead you if you're not careful; most
  of these are cross-referenced to the **Coverage & gaps** chapter.

---

## What this guide covers

> **Reading path.** The guide is written to be read in order — each chapter
> assumes the ones before it. If you are here for one thing, the titles are the
> index; the operator ▶ sections stand alone if all you need is to *run* an
> export.

1. **What CIM *is*, and why export it** — the standard, the CDPSM profile, and
   the crucial idea that a CIM file is a *network model*, not a map. *(this
   chapter)*
2. **The mental model** — features → typed CIM objects → terminals meeting at
   connectivity nodes. The three graphs (geometry, containment, connectivity)
   and which one CIM cares about.
3. **The declarative mapping** — the three `cim_*` tables, `cim_kind`, and the
   transforms, walked through with a worked `ACLineSegment` and
   `PowerTransformer`.
4. **Topology** — how connectivity becomes `ConnectivityNode`s, `Terminal`s and
   `PowerTransformerEnd`s; the union-find; the single-feature preview.
5. **Running an export & reading the file** — the export dialog, scope, the
   admin mapping editor, coverage warnings, and the per-feature CIM preview.
6. **Coverage, gaps & consuming the file** — what's mapped, what's skipped, what
   drops silently, the known fidelity limits, and how a downstream tool ingests
   the result. Plus an appendix reference of classes, kinds and transforms.

---

## Chapter 1 — What CIM is, and why export it

Priya has an email from the regional planning team: *"Please send us your Oakwood
Zone network in CIM."* She has exported KML for Google Earth before, and a
Shapefile for a consultant, and she assumes CIM is just another file format in
the same list. It is in the list — but it is a different *kind* of thing, and
understanding why is the whole of this chapter.

### ▶ The one-paragraph version

CIM export produces a single `.xml` file that describes your electrical network
as a **connected model** a power-system analysis tool can solve — not a picture
of it. Where a KML or Shapefile export says *"here is a line, here are its
coordinates and attributes,"* a CIM file additionally says *"this line's near end
is electrically the same node as that transformer's high-voltage terminal, which
sits in this substation, at this base voltage."* You produce it from the same
**Export** dialog as any other format; the difference is entirely in what comes
out. To use it, you (or Tom) load it into a tool that speaks CIM. That is the
whole operator story — the rest of this chapter is *why*, and you can skip to
Chapter 5 if you just need to run one.

### ∑ What "CIM" actually stands for

**CIM** is the **Common Information Model** — an international standard
(IEC 61970 for transmission, IEC 61968 for distribution, IEC 62325 for markets)
for describing electrical power systems. It is maintained by the IEC and is the
*lingua franca* between utility software: EMS/DMS/ADMS platforms, planning and
analysis tools, and asset systems all speak dialects of it, which is why a
planning team can ask for "CIM" and expect any competent GIS to be able to
produce something they can read.

Two properties of CIM matter for understanding VoltEdge's export:

1. **It is an object model, not a file format.** CIM defines *classes* —
   `ACLineSegment`, `PowerTransformer`, `Breaker`, `EnergyConsumer`,
   `ConnectivityNode`, `Terminal`, `BaseVoltage` — with attributes and
   associations between them. A "CIM file" is a *serialization* of instances of
   those classes. VoltEdge serializes them as **RDF/XML** (the traditional CIM
   wire format): each object is an XML element, each association an
   `rdf:resource` pointer to another object's id.

2. **A subset is agreed for a purpose — a *profile*.** The full CIM is enormous;
   nobody exchanges all of it. Instead, communities agree on a **profile**: a
   named subset of classes and attributes sufficient for a task. VoltEdge targets
   **CDPSM** — the *Common Distribution Power System Model* — the profile built
   for exchanging distribution networks for power-flow-style analysis. Its
   schema namespace is CIM16 (`http://iec.ch/TC57/2013/CIM-schema-cim16#`). When
   VoltEdge writes `cim:ACLineSegment`, the `cim:` prefix is that namespace.

> 💡 **The mental shortcut.** *CIM = the object vocabulary. CDPSM = the agreed
> subset we speak. RDF/XML = how we write it down.* VoltEdge's default profile,
> seeded as `CDPSM_CIM16`, bundles all three choices into one row you can inspect
> and edit (Chapter 5).

### ∑ A model, not a map — why this is the key idea

This is the distinction Priya's instinct missed, and it is worth making concrete.

When VoltEdge exports **KML** or a **Shapefile**, each feature is emitted
independently: a geometry plus its attribute columns. The Oakwood 11 kV line and
transformer TX-Feeder7-014 come out as a `LineString` and a `Point` that happen
to touch on screen. Nothing in the file states that they are electrically
connected — the *connection is implied by the coordinates* and left for a human
(or a spatial join) to infer. That is fine for a map: a map's job is to show
where things are.

A **CIM/CDPSM** file has a different job: to be **solvable**. A load-flow or
fault engine cannot run on "two shapes that touch." It needs an explicit graph —
*which terminals are the same electrical node*. So the CIM export must state,
explicitly:

- TX-Feeder7-014 is a `PowerTransformer` with two **windings**
  (`PowerTransformerEnd`s), a high-voltage end at 11 kV and a low-voltage end.
- Its HV winding has a `Terminal`, and that terminal sits on a
  **`ConnectivityNode`**.
- The near end of the Oakwood 11 kV `ACLineSegment` has a `Terminal` on the
  **same `ConnectivityNode`** — *that* is what says "these are electrically
  joined."
- Both pieces of equipment reference a shared `BaseVoltage` object with
  `nominalVoltage = 11000`.
- Both sit inside an `EquipmentContainer` (a substation, or the catch-all
  "Unassigned equipment" line container).

None of that structure exists in the KML. Producing it is the entire reason CIM
export is a distinct subsystem in VoltEdge rather than one more entry in a format
switch statement — and it is why the export has to read not just your features
but your **connectivity** (`runtime.feature_connectivity`) and **containment**
(`runtime.feature_relationships`) as well.

> ⚠️ **Corollary — CIM quality is connectivity quality.** Because the file's
> value *is* the topology, an export is only as good as the connectivity Dana
> captured. Two conductors that look joined on the map but were never wired
> together produce two *separate* connectivity nodes and an open circuit in the
> model. Chapter 4 explains exactly how the graph is built; Chapter 6 explains
> how to spot the gap before Tom does. The corollary in one line: **a beautiful
> map can export a broken model.**

### ∑ What VoltEdge's CIM export is — and is not

To set expectations honestly before the detail chapters:

**It is:**

- **Export-only.** VoltEdge writes CIM; it does not import it. The mapping runs
  one direction: VoltEdge features → CIM objects.
- **Declaratively mapped, behaviorally encoded.** *Which* CIM class a feature
  becomes, and *how* each field is transformed, live in three editable database
  tables (Chapter 3) — an administrator can remap without a code change. *How*
  the topology graph, geometry, transformer windings and per-phase objects are
  synthesized is fixed Go code (Chapter 4).
- **Scoped like every other export.** 🗺️ It respects your territory access and
  the private-draft rules — you export the network *you can see*, not the whole
  database. (Chapter 5.)
- **Streamed.** The job runs asynchronously and streams straight to object
  storage, so a large network exports in constant memory.

**It is not:**

- **A full, loss-free CIM implementation.** It targets the CDPSM distribution
  profile and maps the classes and attributes that profile needs. Fields with no
  mapping are dropped (Chapter 6).
- **A round-trippable interchange.** Because it is export-only and lossy, you
  cannot export a network, edit the CIM, and re-import it into VoltEdge.
- **A guarantee of solver-readiness.** The file is *structured* for analysis, but
  whether a given consumer solves cleanly depends on data completeness —
  transformer secondary voltages, conductor impedances, and full connectivity
  all have known limits documented in Chapter 6. Treat the first export to a new
  downstream tool as something to *validate*, not to trust blind.

### ▶ Where it fits among the other exports

For the operator, this is the practical placement:

| You want to… | Use |
| --- | --- |
| See the network in Google Earth | **KML/KMZ** |
| Hand geometry + attributes to a GIS consultant | **Shapefile** |
| Give a CAD/drafting team the linework | **DXF** |
| Give a **planning/analysis** team a **solvable network model** | **CIM/XML** |

CIM is the one you reach for when the recipient is going to *compute* on the
network — power flow, fault levels, hosting capacity — rather than *look at* it.
That is the answer to Priya's email: the planning team asked for CIM precisely
because they intend to run studies, and only the model-carrying format will do.

### Where this leaves us

Priya now knows *what* she is being asked to produce and *why* it is a different
animal from the exports she has run before: a connected, solvable model, written
in the CDPSM dialect of CIM as RDF/XML, built from her features *plus* their
connectivity and containment.

The next chapter opens the model itself: the three graphs VoltEdge holds
(geometry, containment, connectivity), which one CIM is built from, and the exact
chain **feature → typed CIM object → terminal → connectivity node** that Chapter
1 sketched with TX-Feeder7-014. From there, Chapter 3 makes the mapping concrete
and editable, and Chapter 4 shows the topology being assembled line by line.

---
