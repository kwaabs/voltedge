# The VoltEdge CIM Export Guide

*What a CIM/XML export is, how to produce one, and how VoltEdge turns your
network into a standards-compliant model file.*

_Last updated: 2026-11-06_

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
   connectivity nodes; the three graphs (geometry, containment, connectivity);
   identity and determinism; the Oakwood example end to end.
3. **The declarative mapping** — the three `cim_*` tables, `cim_kind`, the seven
   transforms, the declaring-class rule, worked `ACLineSegment` and
   `PowerTransformer` examples, and what the shipped seeds map.
4. **Topology** — how connectivity becomes `ConnectivityNode`s, `Terminal`s and
   `PowerTransformerEnd`s; union-find; conductor-end disambiguation; phasing.
5. **Running an export & reading the file** — the dialog, the API, the job
   lifecycle, scope and reprojection, a pre-hand-off checklist, and an annotated
   tour of a real file.
6. **Coverage, gaps & fidelity** — the honest ledger: what is carried, what is
   approximated, what is dropped.
7. **The standard, in practice** — the IEC 61970/61968 family, CIM versions,
   CIMXML rules, CDPSM vs CGMES, node-breaker vs bus-branch, validator rules, and
   where VoltEdge follows or departs from the standard.
8. **Taking the file into other tools** — a ten-question interview, the
   first-import protocol, adapter tools and recipes for each family of consumer.
9. **Validate, diff and repair** — the validator, command-line recipes, tracing an
   open end to the data, regression diffs.
10. **Cookbook, troubleshooting and reference** — mapping recipes, a symptom
    table, and appendices (element reference, API/env reference, glossary, change
    log, tools, and the complete sample file).
11. **Incremental exchange** — named sync targets, difference models
    (`forwardDifferences`/`reverseDifferences`) diffed against a stored baseline,
    scheduling, and the `export.ready` webhook push.

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

For a fuller account of the standards themselves — the IEC family, the CIM versions
and how a CIM file is meant to be read — see **Chapter 7**.

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
> model. Chapter 4 explains exactly how the graph is built; Chapter 9 shows
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
- **Asynchronous and bounded.** The job runs on a worker and **streams the XML**
  straight to object storage, but it must read each requested class fully (up to
  **50 000 features per class**) and hold the topology graph in memory — CIM cannot
  stream the way KMZ or DXF can, because connected components need the whole graph
  (Chapter 5).

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

## Chapter 2 — The mental model

Chapter 1 said a CIM file is a *model*, not a map. This chapter opens that model:
the three graphs VoltEdge holds, the one chain that turns a feature into CIM
objects, and — using a real file the encoder produced — exactly what comes out.
Everything in later chapters hangs off the picture drawn here.

### ∑ Three graphs, one of which CIM cares about most

Every VoltEdge feature participates in three different graphs at once. They are
easy to blur together, and the blur is the source of most "why is my export
wrong?" questions.

| Graph | Stored in | What an edge means | Used by CIM for |
| --- | --- | --- | --- |
| **Geometry** | the feature's own geometry column | "this shape is at these coordinates" | `Location` + `PositionPoint`s (the GL part), `Conductor.length`, picking which end of a conductor a connection touches, substation point-in-polygon |
| **Containment** | `runtime.feature_relationships` (relationship classes of kind `containment`) | "this container holds that child" (substation → equipment) | `Equipment.EquipmentContainer` |
| **Connectivity** | `runtime.feature_connectivity` | "terminal A of feature X is electrically joined to terminal B of feature Y" | `ConnectivityNode`s and `Terminal`s — the heart of the model |

A fourth input is not a graph but a catalogue: **material specs**
(`metadata.material_specs`), which supply per-kilometre resistance and reactance
so a conductor's length can become a series impedance.

> 💡 **Which graph wins when they disagree?** For *electrical* meaning, only
> connectivity counts. Two conductors that overlap on the map but have no
> connectivity row are **not** connected in the CIM file. Conversely, a
> connectivity row between two features that are kilometres apart *is* honoured —
> the encoder trusts the row and uses geometry only to decide *which end* of a
> conductor it touches (Chapter 4).

### ∑ The chain: feature → CIM object → terminal → connectivity node

Every conducting feature goes through the same four steps. Follow the Oakwood
transformer **TX-Feeder7-014** through them:

```
  1. CLASS MAPPING          2. EQUIPMENT OBJECT        3. TERMINALS            4. CONNECTIVITY NODE
  ─────────────────         ──────────────────         ─────────────────       ────────────────────
  DIST_XFMR                 <cim:PowerTransformer      T1 (HV)  seq 1   ──►    node A  (also holds the
   → PowerTransformer        rdf:ID="_93185a15…">      T2 (LV)  seq 2   ──►            11 kV span's end 2)
   cim_kind =               name, mRID, container,                             node B  (also holds the
   conducting_junction      Location, no voltage                                       LV span's end 1)
                                    │
                                    └─ + one PowerTransformerEnd (winding) per terminal:
                                       end 1: ratedU 11000, ratedS 315000, BaseVoltage 11 kV
                                       end 2: ratedU   415, ratedS 315000, BaseVoltage 415 V
```

1. **Class mapping.** A row in `metadata.cim_class_mappings` says which CIM class
   the feature's class becomes and its `cim_kind` (how the encoder treats it).
2. **Equipment object.** The encoder writes one RDF element whose `rdf:ID` is
   `_` + the feature's own UUID, with its name, container, voltage reference,
   location, and any mapped literal attributes.
3. **Terminals.** The encoder synthesizes the equipment's complete terminal set —
   two for a conductor, one per non-ground terminal definition for a junction.
4. **Connectivity nodes.** Terminals that connectivity says are joined are grouped
   into one `ConnectivityNode`, and each terminal points at it.

A feature that is *not* conducting (a pole, a meter, a protection relay) stops
after step 2: it becomes an object but has no terminals and is not in the
topology graph.

### ∑ The Oakwood example, end to end

The running example in this guide is a real encoder run on the Oakwood Zone
fixture. The same network is rebuilt by an automated test
(`api/internal/export/cim_test.go`), so the file shown in Appendix F is not
hand-edited — it is what the encoder writes. Its shape:

```
 Oakwood Primary (Substation polygon)
 ┌──────────────────────────────┐
 │  SW-F7-03 (LoadBreakSwitch)  │
 │   T1 ● dangling      T2 ●────┼─── C-F7-01 ─── end1 ──┐   (11 kV overhead span, 284.6 m)
 └──────────────────────────────┘                       │
                                         node-A: SW.T2 + C-F7-01.end1
                                         node-B: C-F7-01.end2 + TX.T1
                                         TX-Feeder7-014 (315 kVA, 11 kV / 415 V)
                                         node-C: TX.T2 + LV-014-01.end1
                                         LV-014-01 (95 mm² ABC, 62.8 m, r 0.02 Ω)
                                         node-D: LV-014-01.end2 + SP-0457.T1
                                         SP-0457 (EnergyConsumer) ── M-0457 (UsagePoint)
```

The encoder's own bookkeeping for this fixture is a handy sanity check, because
the same numbers appear in the server log for a real export (Chapter 5):

| Quantity | Value | Why |
| --- | --- | --- |
| Equipment objects | 5 conducting (switch, 2 spans, transformer, consumer) + 1 asset (meter) + 1 container (substation) | one per mapped feature |
| Terminals | **9** | switch 2 + MV span 2 + transformer 2 (its ground terminal is excluded) + LV span 2 + consumer 1 |
| ConnectivityNodes | **5** | four joins (A–D above) plus the switch's unconnected source-side terminal on its own isolated node |
| PowerTransformerEnds | **2** | one per transformer terminal |
| BaseVoltages | **2** | 11 000 V and 415 V — one object per *distinct* voltage in the export, shared by reference |
| Containers | Substation "Oakwood Primary", plus the catch-all `Line` "Unassigned equipment" | the switch falls inside the substation polygon; everything else lands in the default `Line` |

Notice what the five nodes teach: **an unconnected end is not an error.** The
switch's source-side terminal has no connectivity row, so it gets a node of its
own with exactly one terminal. The file stays structurally valid (every
`ACLineSegment` still has two terminals, every transformer two windings) and the
open end is *visible* — a one-terminal node is the signature of a gap in your
connectivity data (Chapter 9 shows a query that finds them all).

### ∑ How the file is laid out

The encoder writes the document in passes, and the order is visible in the file:

1. **Header** — `md:FullModel` (what this file is, when it was made, which
   profile and authority set).
2. **Scaffold** — one `GeographicalRegion`, one `SubGeographicalRegion`, one
   `CoordinateSystem` (the CRS the positions are in) and the default `Line`
   container ("Unassigned equipment").
3. **Pass 1 — substations**: each `Substation` feature, with its region link and
   its boundary polygon as a GL `Location`.
4. **Pass 1b — container tiers** (`Bay`; see the note in Chapter 6 on why this
   tier is dormant today).
5. **Pass 2 — base voltages**, one `BaseVoltage` per distinct nominal voltage.
6. **Pass 3 — equipment, assets, protection**, each followed by its `Location`
   and `PositionPoint`s, and (for phased classes) its per-phase objects.
7. **Pass 4 — topology**: for every connected component a `ConnectivityNode`,
   then each of its `Terminal`s, and after any transformer terminal the
   `PowerTransformerEnd` that terminal belongs to.

RDF does not care about order — references can point forward — so consumers read
the file as an unordered set of statements. The ordering is for human readers and
for byte-stable diffs.

### ∑ Identity: how the file keeps stable ids

A model exchange is only useful if ids survive from one export to the next, so
that a downstream system can say "the same transformer as last week". VoltEdge's
identity rules are deliberately simple:

| Object | `rdf:ID` / `mRID` | Stable across re-exports? |
| --- | --- | --- |
| Equipment, assets, containers (every *feature*) | the feature's own UUID | **Yes** — it is the database id |
| `BaseVoltage` | UUIDv5 of `basevoltage:<volts>` | Yes — same voltage, same id, in every file |
| `Location`, `PositionPoint` | UUIDv5 of `location:<featureId>` / `positionpoint:<locationId>:<n>` | Yes |
| `Terminal` | UUIDv5 of `terminal:<endpoint key>` | Yes, while the feature's terminal ids are unchanged |
| `PowerTransformerEnd` | UUIDv5 of `ptend:<endpoint key>` | Yes |
| Per-phase objects | UUIDv5 of `phase:<class>:<featureId>:<kind>` | Yes |
| **`ConnectivityNode`** | UUIDv5 of *all member endpoint keys, sorted and joined* | **No — it changes whenever the set of terminals on the node changes** |
| Scaffold (region, CRS, default `Line`) | UUIDv5 of a fixed string | Yes |

Two consequences worth knowing:

- **Equipment identity is dependable; node identity is not.** If a downstream
  tool tracks `ConnectivityNode` mRIDs (a bus list, say), a single new connection
  on a node re-ids that node. Track *terminals and equipment*, and treat node ids
  as derived.
- **Output is deterministic.** Given the same data, two exports are byte-identical
  except for the header's creation time and model id (the automated test asserts
  this). That makes `diff` a genuine regression tool — Chapter 9 uses it.

UUIDv5 is the RFC 4122 name-based UUID (SHA-1 over a fixed VoltEdge namespace
plus the name); the namespace constant is arbitrary but frozen forever, so ids
never drift between releases.

Each object is written with its id twice: as the RDF identifier
(`rdf:ID="_93185a15-…"`, with the conventional leading underscore) and again as an
`IdentifiedObject.mRID` literal (`93185a15-…`, without it). References use the
underscore form (`rdf:resource="#_93185a15-…"`). Chapter 7 explains how that
convention relates to the standard and to newer `urn:uuid:` styles.

### ∑ Scope: the graph is closed over what you export

CIM export reads each requested class, then loads the connectivity and
containment rows **only where both ends are inside the exported set** — after the
bounding-box, territory and private-draft filters have been applied. The
guarantee is that no `Terminal` ever points at a feature that is not in the file.
The price is that **what you select decides what is connected**:

- Export only `MV_CONDUCTOR` and you get spans and nothing for them to join to:
  every end sits alone on its own node, and the file is a pile of disconnected
  lines.
- Export a bounding box that cuts a feeder in half and the cut ends become
  unconnected one-terminal nodes — the file describes an island, not the feeder.
- Export as a territory-scoped user and you get exactly your territories' share,
  with the boundary treated the same way as a bounding-box cut.

> ⚠️ **Rule of thumb.** For a solvable model, export *every class that forms the
> connected network* (conductors, transformers, switches, busbars, loads) for a
> region that contains the whole connected sub-network you care about — whole
> feeders, whole substations. A convenient default is the full set of classes in
> the Admin → CIM mappings list that are *mapped and conducting*.

### ▶ Practical takeaways

1. A CIM export is **a set of statements about equipment and how it connects**,
   not a layer.
2. Check the **topology counts** (terminals, nodes) before you hand the file over:
   a node count close to the terminal count means little is connected.
3. **Whole feeders, all conducting classes** for a solvable model.
4. Trust **equipment and terminal ids** across exports; do not rely on
   `ConnectivityNode` ids.

*Next: Chapter 3 shows the declarative mapping that decides which CIM class each
feature class becomes — and that you can edit.*

---

## Chapter 3 — The declarative mapping

The encoder has two halves. The **behavioral** half — topology, geometry, windings,
ids, XML — is Go code (Chapters 4 and 5). The **declarative** half answers
questions that are really *data*: which CIM class does a `DIST_XFMR` become? Which
field is the nominal voltage? How is `rated_kva` turned into volt-amperes? That
half lives in three metadata tables an administrator can edit, with no deploy.

### ▶ Where to edit it

🔒 **Admin → CIM mappings** (`/admin/cim-mappings`). Needs `metadata:read` to look
and `metadata:write` to change — the same gate as every other `/admin` metadata
screen. The page has four parts:

| Part | What it is |
| --- | --- |
| **Unmapped classes** (amber card) | every feature class with *no* class-mapping row — the classes silently dropped from export today. A **Map…** button opens the class-mapping form pre-filled with that class. |
| **Profiles** | the export target(s): namespace, profile URI, authority, CRS, unmapped-attribute policy |
| **Mappings → Class mappings** | feature class (and optional subtype) → CIM class + kind. A **CIM/XML preview** icon on each row renders one real feature through the real encoder. |
| **Mappings → Attribute mappings** | feature field → CIM attribute + transform |
| **Browse CIM vocabulary** | the CIM reference dictionary: every CIM class and attribute, flagged *in profile* where VoltEdge already targets it |

### ∑ Table 1 — `metadata.cim_profiles`: the export target

One row is one *thing the export can target*. VoltEdge seeds one, `CDPSM_CIM16`,
flagged `is_default` (the export uses it when a request names none).

| Column | Meaning | Seeded value |
| --- | --- | --- |
| `code` | the name an export request selects (`cim_profile`) | `CDPSM_CIM16` |
| `cim_namespace` | the namespace every `cim:` element is qualified by | `http://iec.ch/TC57/2013/CIM-schema-cim16#` |
| `profile_uri` | written to `md:Model.profile` — *which profile the file claims to follow* | `http://iec.ch/TC57/61968-13/CDPSM/2013#` |
| `model_authority` | `md:Model.modelingAuthoritySet` — who produced the model | `https://voltedge.local/ModelAuthoritySet` |
| `coordinate_crs` | the CRS URN declared on the `CoordinateSystem` | `urn:ogc:def:crs:EPSG::4326` |
| `unmapped_attr_policy` | `drop` or `carry` — what to do with fields that have no mapping | `drop` |
| `is_default` | the profile used when none is named | `true` |

> ⚠️ **Change the authority URI for production.** `https://voltedge.local/…` is a
> placeholder. The *modelling authority set* is how a recipient tells *whose*
> model this is and, in multi-source exchanges, whose ids to trust. Set it to a
> URI you own (your utility's domain) before the first file leaves the building.

**The unmapped-attribute policy in detail.** `drop` (the default) is a *contract*:
only mapped fields appear in the file, nothing leaks. `carry` writes each feature's
**unmapped scalar fields** as extension elements in a separate namespace
(`xmlns:ve="https://voltedge.local/CIM-extension/1#"`, declared on the root only in
carry mode), for example `<ve:serial_no>T-77-0412</ve:serial_no>` inside the
transformer. A consumer that only understands CDPSM ignores unknown namespaces; one
that wants the extra fields can read them. Composite (JSON object/array) values are
skipped because CIM literals are scalar, and field names are sanitised into valid
XML names. A `drop` export is byte-for-byte unchanged by the existence of `carry`.
Use `carry` for hand-offs to people and ad-hoc tools; use `drop` for strict
consumers and validators.

### ∑ Table 2 — `metadata.cim_class_mappings`: feature class → CIM class

| Column | Meaning |
| --- | --- |
| `feature_class_id` | which VoltEdge class |
| `subtype_id` | `NULL` = the class default; set = an **override for one subtype** |
| `cim_class` | the CIM class written, unqualified (`ACLineSegment`) |
| `cim_kind` | how the encoder *treats* the feature (below) |

Uniqueness is one row per (class, subtype). When both exist, **the subtype row
wins**: a `DIST_SWITCH` is a `LoadBreakSwitch` by default, but its
`automatic_recloser` subtype maps to `Recloser`.

**`cim_kind` is the axis the encoder switches on.** The CIM class tells consumers
what the object *is*; the kind tells VoltEdge what to *do* with it:

| `cim_kind` | What the encoder emits | Terminals / topology | Typical CIM classes |
| --- | --- | --- | --- |
| `conducting_edge` | an equipment element with container, `BaseVoltage`, `Location`, literals, length/impedance, per-phase objects | **two** synthesized terminals (the line's two ends); conductor ends are told apart by geometry | `ACLineSegment` |
| `conducting_junction` | the same equipment element | **one terminal per non-ground terminal definition** of the class (or one implicit terminal if none are defined); a `PowerTransformer` also gets one `PowerTransformerEnd` per terminal and is padded to at least two | `PowerTransformer`, `LoadBreakSwitch`, `Breaker`, `Disconnector`, `Fuse`, `Recloser`, `BusbarSection`, `EnergyConsumer`, `Junction`, `SynchronousMachine`, `LinearShuntCompensator` |
| `container` | an element with name, mRID and (if it has geometry) a `Location`; a `Substation` also gets `Substation.Region` | none — but its id can be a *parent* in the containment tree | `Substation`, `Bay`, `EquipmentContainer` |
| `asset` | name, mRID and `Location` only | none; kept out of the topology graph | `Pole`, `Tower`, `UsagePoint`, `RatioTapChanger` |
| `protection` | name and mRID only (it has no map geometry) | none | `ProtectionEquipment` |
| `skip` | **nothing** — a deliberate omission | — | (sentinel class name `(not exported)`) |

Two things to hold on to:

- **`skip` is not the same as "unmapped".** A class with a `skip` row has been
  *decided* — the export drops it knowingly and does not warn. A class with *no*
  row is dropped **silently by the encoder** and **loudly by the tooling**: the
  export logs `CIM unmapped classes dropped from output … CLASS=count`, and the
  admin page lists it in the amber **Unmapped classes** card (Chapter 5).
- **The kind is a promise about electrical behaviour.** Mapping a point class as
  `conducting_junction` makes it participate in the connectivity graph; mapping it
  as `asset` takes it out. Choose by whether the thing carries current, not by how
  it looks on the map.

### ∑ Table 3 — `metadata.cim_attribute_mappings`: field → CIM attribute

| Column | Meaning |
| --- | --- |
| `field_id` | which VoltEdge field |
| `cim_attribute` | dotted `ClassName.attribute` — the *exact* element name written |
| `transform` | how the value is coerced (below) |
| `scale_factor` | multiplier for `scale` |
| `value_map` | JSON lookup for `domain_map` / `boolean`, keyed by the field's coded value |
| `unit` | informational only (`m`, `V`, `ohm`) — **not written to the file** |

Uniqueness is one row per (field, CIM attribute), so one field can feed several
attributes (the LV conductor's `conductor_spec` feeds length, `r` and `x`).

**The seven transforms** — what each does, and exactly when it emits *nothing*
(the encoder never writes a half-resolved value; a failed transform is silent):

| Transform | Does | Example | Emits nothing when… |
| --- | --- | --- | --- |
| `direct` | copies the value; booleans become `true`/`false`, numbers keep their digits | `secondary_voltage` 415 → `415` | the field is null/absent |
| `scale` | value × `scale_factor` | `rated_kva` 315 × 1000 → `315000` | field null, value not numeric, or `scale_factor` is NULL |
| `domain_map` | looks the coded value up in `value_map` | `primary_voltage` `11KV` → `11000` | the code isn't a key in `value_map` |
| `boolean` | looks up in `value_map`; the mapped value must be a JSON boolean | `device_state` `CLOSED` → `false` for `Switch.normalOpen` | the code isn't in the map, or maps to a non-boolean |
| `from_geometry` | derives the value from the geometry — today only `Conductor.length` (geodesic metres, to the millimetre) | any LineString | the feature has under two vertices; the *field* is just an anchor and its value is ignored |
| `material_spec` | resolves the field's coded value to a `metadata.material_specs` row and multiplies per-km resistance (`ACLineSegment.r`) or reactance (`.x`) by length in km | `conductor_spec` `ABC_95` → r, x | the value has no spec, the spec lacks that property, or the length is zero |
| `phase` | expands a coded phase value into per-phase objects (Chapter 4) | `L1L2L3N` → four `ACLineSegmentPhase` objects | the code isn't one of the eight recognised phase codes |

**Three target names are special** — they do not produce a literal on the
equipment:

| Target | Where it goes |
| --- | --- |
| `BaseVoltage.nominalVoltage` | feeds a shared `BaseVoltage` object the equipment references (`ConductingEquipment.BaseVoltage`). A `PowerTransformer` carries voltage on its windings instead. |
| `BaseVoltage.nominalVoltage.secondary` | **a VoltEdge convention, not a CIM attribute.** Names the transformer's second winding's voltage so winding 2 can carry `ratedU` and a `BaseVoltage`. |
| `PowerTransformerEnd.*` / `TransformerEnd.*` | applied to the `PowerTransformerEnd` objects (every winding gets e.g. `ratedS`), never to the `PowerTransformer` element |
| `<PhaseClass>.phase` (with `transform = phase`) | creates sibling per-phase objects |

Everything else is written verbatim as `<cim:Class.attribute>value</…>` inside the
equipment element. That brings a rule you must respect when you author mappings:

> **The declaring-class rule.** CIM serializes a property under the class that
> *declares* it. `Switch.normalOpen` on a `LoadBreakSwitch` is right (`LoadBreakSwitch`
> is a `Switch`); `Conductor.length` on an `ACLineSegment` is right (`Conductor`
> declares it); `cim:RatioTapChanger.step` is **wrong**, because `step` is declared on
> `TapChanger` — a strict consumer silently drops the former spelling. Because the
> encoder emits `cim_attribute` verbatim, **the class half of the name must be the
> mapped CIM class or one of its ancestors.** The CIM vocabulary browser shows each
> attribute's *declaring class* and flags inherited ones so you pick the canonical
> spelling; `seed/cim_mapping_target_fixes.sql` re-targeted four non-canonical
> attributes (`RatioTapChanger.step` → `TapChanger.step`,
> `TapChangerControl.targetValue` → `RegulatingControl.targetValue`, likewise
> `targetDeadband`, and `ShuntCompensator.nomQ` → `ShuntCompensatorInfo.ratedReactivePower`).
> A property that belongs to a *different object* than the one you're writing (a
> tap changer's step on a transformer element) still lands as a literal on that
> element — that is a mapping limit, listed in Chapter 6.

### ∑ Worked example 1 — an `ACLineSegment`

`MV_CONDUCTOR` is a line class (`network_role = edge`). Its rows:

| Table | Row | Effect |
| --- | --- | --- |
| class | `MV_CONDUCTOR` → `ACLineSegment`, `conducting_edge` | each span becomes an `ACLineSegment` with two terminals |
| attribute | `voltage_class` → `BaseVoltage.nominalVoltage`, `domain_map` `{"11KV":11000,"33KV":33000}` | `ConductingEquipment.BaseVoltage` → the 11 000 V object |
| attribute | `voltage_class` (anchor) → `Conductor.length`, `from_geometry` | `<cim:Conductor.length>284.603</…>` |
| attribute | `conductor_spec` → `ACLineSegment.r` / `.x`, `material_spec` *(seeded by `mv_conductor_specs.sql`)* | series ohms = spec per km × length in km |
| attribute | `phase` → `ACLineSegmentPhase.phase`, `phase` | four per-phase objects for `L1L2L3N` |

Resulting element (from the real Oakwood export):

```xml
<cim:ACLineSegment rdf:ID="_9341c867-aae5-52b9-859e-147a13a6b332">
  <cim:IdentifiedObject.name>C-F7-01</cim:IdentifiedObject.name>
  <cim:IdentifiedObject.mRID>9341c867-aae5-52b9-859e-147a13a6b332</cim:IdentifiedObject.mRID>
  <cim:Equipment.EquipmentContainer rdf:resource="#_a15e7a22-11b7-56f5-bfe4-190610adf0b0"/>
  <cim:ConductingEquipment.BaseVoltage rdf:resource="#_6890e289-f3f8-5807-9e5b-f077ac483ef1"/>
  <cim:PowerSystemResource.Location rdf:resource="#_ae2d0879-baa8-515f-bf49-8c037b229bf5"/>
  <cim:Conductor.length>284.603</cim:Conductor.length>
</cim:ACLineSegment>
```

The length is **metres**. Note what is *absent*: no `ACLineSegment.r`/`.x` here,
because this fixture's MV span has no `conductor_spec` mapping — the LV span next
to it, which does, carries `r` 0.02 Ω and `x` 0.005 Ω.

> ⚠️ **Impedance precision floor.** Impedance is rounded to the milliohm
> (`round3`). A 62.751 m run of 0.32 Ω/km cable is 0.02008 Ω and is written as
> `0.02`. For short LV service cables that is a 1–3 % error at the 3rd decimal and
> larger for very short runs. It does not matter on a feeder, but if you study
> voltage drop on service drops, ask for more precision (a one-line change to the
> rounding) or compute impedance downstream from `Conductor.length` and the
> catalogue.

### ∑ Worked example 2 — a `PowerTransformer`

`DIST_XFMR` is a junction class with terminals `T1` (in), `T2` (out) and `T3`
(**ground**). Its rows:

| Table | Row | Effect |
| --- | --- | --- |
| class | `DIST_XFMR` → `PowerTransformer`, `conducting_junction` | one `PowerTransformer` element |
| attribute | `primary_voltage` → `BaseVoltage.nominalVoltage`, `domain_map` | winding 1 `ratedU` + `BaseVoltage` |
| attribute | `secondary_voltage` → `BaseVoltage.nominalVoltage.secondary`, `direct` | winding 2 `ratedU` + `BaseVoltage` (415 V) |
| attribute | `rated_kva` → `PowerTransformerEnd.ratedS`, `scale` × 1000 | `ratedS` on **every** winding |

The element itself is deliberately bare — a transformer has no voltage of its own,
only its windings do:

```xml
<cim:PowerTransformer rdf:ID="_93185a15-0963-58e3-b744-28d63c30d899">
  <cim:IdentifiedObject.name>TX-Feeder7-014</cim:IdentifiedObject.name>
  <cim:IdentifiedObject.mRID>93185a15-0963-58e3-b744-28d63c30d899</cim:IdentifiedObject.mRID>
  <cim:Equipment.EquipmentContainer rdf:resource="#_8c11e9ef-cb53-51b8-9110-817746f020b7"/>
  <cim:PowerSystemResource.Location rdf:resource="#_3ddaa7fa-3fd1-500e-a143-50d6387447b3"/>
</cim:PowerTransformer>
```

and the windings (one per terminal, `T3` ground excluded) carry the electrical data:

```xml
<cim:PowerTransformerEnd rdf:ID="_bfd7c027-a32c-5888-839c-af04a6f32d79">
  <cim:PowerTransformerEnd.PowerTransformer rdf:resource="#_93185a15-0963-58e3-b744-28d63c30d899"/>
  <cim:TransformerEnd.endNumber>1</cim:TransformerEnd.endNumber>
  <cim:TransformerEnd.Terminal rdf:resource="#_341b525b-a44b-5bf2-bf40-20b37a888c95"/>
  <cim:PowerTransformerEnd.ratedS>315000</cim:PowerTransformerEnd.ratedS>
  <cim:PowerTransformerEnd.ratedU>11000</cim:PowerTransformerEnd.ratedU>
  <cim:TransformerEnd.BaseVoltage rdf:resource="#_6890e289-f3f8-5807-9e5b-f077ac483ef1"/>
</cim:PowerTransformerEnd>
```

> **A bug this guide fixed.** Earlier builds *also* wrote
> `<cim:PowerTransformerEnd.ratedS>` as a stray literal directly on the
> `PowerTransformer` element — a property CIM does not allow there. The encoder now
> routes winding attributes only to the windings, and a regression test
> (`TestCIMOakwoodSample`) guards it. If you diff an old export against a new one
> and see that element vanish, this is why.

Not carried (and flagged in Chapter 6): winding resistance/reactance/`connectionKind`
(vector group), tap changers, short-circuit data. A consumer that needs transformer
impedance must supply it from another source.

### ∑ Worked example 3 — a subtype override

`DIST_SWITCH` carries two class-mapping rows:

| Subtype | `cim_class` | Why |
| --- | --- | --- |
| *(class default)* | `LoadBreakSwitch` | the common case |
| `automatic_recloser` (code 2) | `Recloser` | a recloser is a `ProtectedSwitch`, a different CIM class with a different consumer behaviour |

`ClassFor(class, subtype)` checks the subtype row first, then falls back to the
default — so adding a new subtype later needs one new row, not a code change.
(Installs that have run `seed/migrate_dist_switch_to_mv_switch.sql` have split the
legacy `DIST_SWITCH` into `MV_SWITCH` / `LV_SWITCH` / `MV_RECLOSER`, each with its
own class row — the same mechanism with the override promoted to a class.)

### ∑ What the shipped seeds map

The default content is split across seed files so each area owns its rows. The
table lists the **class-level** result; attribute rows (voltage domain maps,
`Switch.normalOpen`, impedance, ratings, phase) follow the patterns above.

| Area | Seed file | Feature class → CIM class (kind) |
| --- | --- | --- |
| Distribution core | `cim_mapping_example.sql` | `MV_CONDUCTOR`, `LV_CONDUCTOR` → `ACLineSegment` (edge) · `DIST_XFMR` → `PowerTransformer` · `DIST_SWITCH` → `LoadBreakSwitch` (subtype `Recloser`) · `SERVICE_CUTOUT` → `Fuse` · `LV_LINK_BOX` → `Junction` · `LV_BUSBAR` → `BusbarSection` · `SERVICE_POINT`, `STREET_LIGHT` → `EnergyConsumer` (all junction) · `METER` → `UsagePoint` (asset) · `METER_CHAMBER` → `EquipmentContainer`, `SUBSTATION` → `Substation`, `BAY` → `Bay` (container) · `POLE` → `Pole` (asset) · `PROTECTIVE_RELAY` → `ProtectionEquipment` |
| MV plant | `mv_cim_mapping_example.sql` | `MV_BREAKER` → `Breaker` · `MV_ISOLATOR` → `Disconnector` · `MV_SECTIONALIZER` → `Sectionaliser` · `MV_FUSE` → `Fuse` · `MV_ARRESTER` → `SurgeArrester` · `MV_CT` → `CurrentTransformer` · `MV_VT` → `PotentialTransformer` · `MV_BUSBAR` → `BusbarSection` |
| Switch parity | `mv_lv_switch_parity.sql`, `mv_recloser_example.sql` | `MV_SWITCH`, `LV_SWITCH` → `LoadBreakSwitch` · `MV_RECLOSER` → `Recloser` |
| Regulation & storage | `mv_voltage_regulator_example.sql`, `mv_capacitor_bank_example.sql`, `oltc_tap_changer_example.sql`, `mv_bess_example.sql` | `MV_VREG` → `PowerTransformer` · `MV_CAP_BANK` → `LinearShuntCompensator` · `TAP_CHANGER` → `RatioTapChanger` (asset) · `MV_BESS` → `BatteryUnit` |
| Coverage fill (Phase 55) | `cim_mapping_coverage.sql` | `POWER_XFMR`, `EARTHING_XFMR` → `PowerTransformer` · `GENERATOR` → `SynchronousMachine` · `EV_CHARGING_STATION` → `EnergyConsumer` · `MV_EARTH_SWITCH` → `GroundDisconnector` · `MV_CABLE_JOINT`, `LV_CABLE_JOINT` → `Junction` · `RMU`, `MV_METERING_CUBICLE`, `MV_SWITCHGEAR_PANEL`, `MV_SWITCHBOARD`, `SUBSTATION_PANEL` → `EquipmentContainer` · `TOWER` → `Tower` (asset) |
| Deliberately skipped | `cim_mapping_coverage.sql` | `EV_CHARGER`, `EV_CONNECTOR`, `STATION_BATTERY`, `BATTERY_CHARGER`, `CONTROL_IED`, `ANNUNCIATOR`, `CABLE_TRENCH`, `CABLE_DUCT`, `CABLE_CHAMBER`, `DUCT_WAY`, `CONTROL_BUILDING` → `skip` |

This table describes what the **seed files ship**. Your live database is the
truth — open **Admin → CIM mappings** to see it, and read the **Unmapped classes**
card for anything added since.

### ▶ Recipes in the editor

**Map a new class (the 90 % case).** *Admin → CIM mappings → Unmapped classes →
Map…* for your class. Pick the **CIM class** from the vocabulary browser (it
defaults to the in-profile subset and warns on an abstract or unknown name — it
never *blocks* free text), choose the **kind** by whether it carries current, save.
Click the row's **CIM/XML preview** to see one real feature rendered.

**Add a voltage.** Add an attribute mapping: field = the class's voltage field,
CIM attribute `BaseVoltage.nominalVoltage`, transform `domain_map`, value map
`{"11KV": 11000, "33KV": 33000}`. A numeric field already in volts uses `direct`.

**Add an impedance.** Make sure the class's conductor field has coded values that
link to material specs, then add `ACLineSegment.r` and `.x` mappings with transform
`material_spec` (and a `Conductor.length` / `from_geometry` mapping if the class
doesn't have one — impedance needs the length).

**Treat one subtype differently.** Add a second *class* mapping for the same class
with the **Subtype** set. Leave the default row alone.

**Take a class out on purpose.** Add a class mapping with kind `skip` (CIM class
`(not exported)`): the warning disappears because the decision is recorded.

*Next: Chapter 4 follows the topology pass line by line — union-find, conductor
ends, terminals, windings and phases.*

---

## Chapter 4 — Topology

This is the chapter the whole export exists for. VoltEdge stores connectivity as
**pairs**; CIM needs **shared nodes**. Turning one into the other is the job of
`api/internal/export/topology.go`, and it is worth understanding in detail because
every topology question — "why are these two joined?", "why is this end floating?",
"why does the file have N nodes?" — is answered here.

### ∑ The input: pairwise connectivity

`runtime.feature_connectivity` holds one row per connection:

```
   (from_feature, from_terminal)  ──────  (to_feature, to_terminal)
```

Rows are undirected, and a terminal can be **NULL**:

- a **junction** feature (transformer, switch, busbar…) has named terminals
  (`T1`, `T2`, …) defined in `metadata.terminals`, so its end of a row names one;
- an **edge** feature (a conductor) has **no terminals at all** — it is a line with
  two physical ends but no names for them — so its end of every row is `NULL`;
- a junction class that defines no terminals also produces `NULL`.

So a conductor between a switch and a transformer yields two rows, both naming the
*same* conductor with a `NULL` terminal. By id alone, end 1 and end 2 of the
conductor are **indistinguishable**. CIM, however, requires an `ACLineSegment` to
have two distinct `Terminal`s on (usually) two distinct nodes. The encoder has to
decide, for each connection, *which end* of the line it touches. It uses geometry.

### ∑ Step 1 — endpoints and union-find

Every equipment endpoint becomes a vertex in a graph, identified by a key:

| Key | Meaning |
| --- | --- |
| `T\|<feature>\|<terminalId>` | a named terminal of a junction |
| `T\|<feature>\|` | the single *implicit* terminal of a junction whose class defines none |
| `E\|<feature>\|<0 or 1>` | the first (0) or last (1) vertex-end of a conductor |
| `T\|<feature>\|@w2` | a **synthesized** second winding of a transformer (below) |

Every connectivity row becomes an edge between two such vertices, and the encoder
runs **union-find** (disjoint-set union) over them. Each resulting **connected
component** is one `ConnectivityNode`; every endpoint in the component is a `Terminal`
pointing at it.

Union-find is the right tool because it merges groups incrementally in near-linear
time and needs no recursion. The implementation is **iterative with path
compression** on purpose: a feeder wired end-to-end chains its endpoints into one
very deep component, and a recursive `find` would risk blowing the stack at exactly
the scale this export is meant for.

> **What a ConnectivityNode really is.** In CIM a connectivity node is the *point*
> where terminals meet with zero impedance — a busbar, a joint, a T-off, a pole-top
> bolt. Here it is literally "the set of endpoints that connectivity says touch each
> other, transitively." If A touches B and B touches C, all three share one node.

### ∑ Step 2 — which end of a conductor? (geometry disambiguation)

For a connection landing on a conductor with a `NULL` terminal the encoder picks the
**nearer end of the conductor's LineString**:

1. Take the *other* feature in the row and its **representative point** — simply its
   first vertex.
2. Compare that point to the conductor's first and last vertex using squared
   degrees (a *comparison*, never a real length).
3. The nearer vertex decides: end 0 (first vertex) or end 1 (last).

For the Oakwood span `C-F7-01` the switch sits at the span's first vertex and the
transformer at its last, so the switch connection becomes end 1 (`sequenceNumber 1`)
and the transformer connection end 2:

```
  switch (-0.1990, 5.6006) ──nearer to──► first vertex   → Terminal 1 of the span
  transformer (-0.1965, 5.6012) ──nearer to──► last vertex → Terminal 2 of the span
```

**Conductor-to-conductor** connections (both sides `NULL`) cannot be anchored on a
point, so the encoder tries all four end pairings and picks the pair of ends that are
**closest to each other** — which is exactly the pair that physically meet.

> ⚠️ **Where geometry can mislead the model.** The disambiguation is a heuristic over
> data you maintain. It fails *silently* in three ways:
>
> - **Both neighbours nearer the same end.** A conductor drawn very short, or with
>   both neighbours' first vertices closer to one end, ends up with *both*
>   connections on one end; the other end sits alone on an isolated node. The model
>   shows a stub with a floating end, and nothing complains.
> - **A neighbour with no geometry** has no anchor, so the conductor falls back to
>   end 0.
> - **A neighbour that is long** (a busbar drawn as a line) is anchored on its *first
>   vertex only*, which may be far from where the conductor actually lands.
>
> The practical rule: **draw conductors end-to-end with their neighbours** — the first
> vertex at one device, the last at the other — and keep connections consistent with
> that drawing. Chapter 9 gives a query that flags conductors with an unconnected end,
> the signature of all three failures.

### ∑ Step 3 — a complete terminal set for everything

After processing the connectivity rows, the encoder makes sure **every conducting
feature has its full expected set of endpoints**, creating isolated ones where
connectivity is silent:

- a **conductor** always gets both ends (`E|…|0` and `E|…|1`);
- a **junction** gets one terminal per non-ground terminal definition, **resolved for
  its subtype** — a subtype-scoped terminal row overrides a class-wide row of the same
  code — **ordered by terminal code** (`T1`, `T2`, `T3`…), so sequence numbers are
  stable across exports;
- a junction whose class defines **no** terminals gets **one implicit terminal**.

`Terminal.sequenceNumber` is that 1-based position (1/2 for a conductor's ends, the
terminal's ordinal for a junction). Starting at 1 and counting up is exactly what CIM
validators expect.

The point of synthesis is that **a sparse network still exports valid CIM**: an
unconnected `ACLineSegment` end becomes a `Terminal` on its own one-terminal node
instead of an invalid one-terminal line. You can *see* the gaps — they are the nodes
with a single terminal.

**Ground terminals are excluded.** A terminal whose role is `ground` (the `T3` on the
distribution transformer, for example) is not exported: a grounded winding is modelled
differently in CIM (a winding-level `grounded` attribute and earthing equipment). A
connectivity row that *lands* on a ground terminal is **dropped entirely** — so an
earthing connection made through a ground terminal does not appear in the file.

### ∑ Step 4 — transformers: windings, not just terminals

A `PowerTransformer` in CIM is a container for **windings** (`PowerTransformerEnd`),
and each winding is attached to a `Terminal`. So every terminal of a transformer also
yields a `PowerTransformerEnd`:

```
 Terminal T1 (seq 1) ◄── TransformerEnd.Terminal ── PowerTransformerEnd endNumber 1
                                                     ratedU 11000, ratedS 315000
                                                     TransformerEnd.BaseVoltage → 11 kV
 Terminal T2 (seq 2) ◄── TransformerEnd.Terminal ── PowerTransformerEnd endNumber 2
                                                     ratedU   415, ratedS 315000
                                                     TransformerEnd.BaseVoltage → 415 V
```

- `TransformerEnd.endNumber` equals the terminal's sequence number.
- `ratedS` comes from the end-targeted attribute mapping and is written on **every**
  winding.
- **Winding 1** takes `ratedU` and its `BaseVoltage` from the primary voltage;
  **winding 2** from the secondary voltage channel (the
  `BaseVoltage.nominalVoltage.secondary` mapping). Windings beyond two have no
  configured voltage and carry none.
- **Padding.** A distribution transformer modelled as one junction often defines
  fewer terminals than it has windings (frequently none). A valid CIM transformer
  needs at least two windings, so the encoder **synthesizes missing winding endpoints**
  under sentinel ids (`@w2`) that can never collide with a real terminal; unconnected,
  each sits on its own isolated node. The result is never a malformed one-winding
  transformer.

### ∑ Step 5 — which container does a node (and a piece of equipment) belong to?

Two different mechanisms, deliberately:

| Thing | Container rule |
| --- | --- |
| **Equipment** (`Equipment.EquipmentContainer`) | **1.** the *modelled* containment parent (a `containment` relationship from `runtime.feature_relationships`) if that parent is in the export and is a container class · **2.** else the **substation whose polygon contains the equipment's first vertex** (ray-cast point-in-polygon) · **3.** else the default `Line` "Unassigned equipment" |
| **ConnectivityNode** (`ConnectivityNodeContainer`) | the substation containing **any member's** representative point (members are tried in sorted-key order); else the default `Line` |

Consequences: modelled containment **beats** geometry for equipment, so a breaker
modelled as inside a substation panel is placed there even if its drawn point is
outside the yard. But a *node* always uses geometry, so it can land in a different
container than the equipment that touches it — acceptable in CIM, which allows a
connectivity node's container to be flexible, but worth knowing when a consumer
groups by container. Containment loads one flat map (a child has at most one
parent), which expresses the whole tree because containers can themselves be
contained.

### ∑ Phasing: one coded value becomes a set of objects

CDPSM models phases not as an attribute but as **separate per-phase objects** — an
`ACLineSegmentPhase` (or `EnergyConsumerPhase`) per energised phase, each naming a
`SinglePhaseKind` and pointing back at its parent. One coded `PHASE` value therefore
expands into a set:

| `PHASE` value | `SinglePhaseKind` set | Objects written |
| --- | --- | --- |
| `L1` / `L2` / `L3` | `A` / `B` / `C` | 1 |
| `N` | `N` | 1 |
| `L1N` / `L2N` / `L3N` | `A,N` / `B,N` / `C,N` | 2 |
| `L1L2L3N` | `A,B,C,N` | 4 |

Each per-phase object has its own deterministic id and **references the enumeration
literal by absolute URI** (`rdf:resource="http://iec.ch/TC57/2013/CIM-schema-cim16#SinglePhaseKind.A"`),
not a local `#_id`, because an enumeration literal is not an object in the file.
It is emitted *after* the equipment element closes, as a sibling.

Only classes with a `phase` field and a clean CDPSM per-phase object are mapped:
`MV_CONDUCTOR`, `LV_CONDUCTOR` → `ACLineSegmentPhase`; `STREET_LIGHT` →
`EnergyConsumerPhase`. **Transformers, busbars and meters are intentionally not
phased** (a transformer is inherently multi-phase in this profile; `BusbarSection`
has no per-phase class in CDPSM; a meter is non-conducting), and `SERVICE_POINT` has
no `phase` field. A conductor with a null or unrecognised phase simply gets no
per-phase objects.

> ⚠️ **Phases are inferred, not stated.** CIM can also state phases on the terminal
> (`Terminal.phases`); VoltEdge does not write it. A consumer that needs phases for
> an *unphased* class (a transformer, a busbar) must assume them — typically
> three-phase. Hand-offs to an unbalanced solver should be checked for this.

### ∑ The single-feature preview and the stats

Two practical by-products of the design:

- **Preview** (Admin → CIM mappings → the row's *CIM/XML preview*, or
  `GET /admin/cim-preview/{class}`) runs the **entire** encoder on exactly one real
  feature of the class, so the output is a complete, reference-closed document —
  header, scaffold, `BaseVoltage`, the equipment, its synthesized terminals and
  isolated nodes. It is byte-for-byte what the real export writes for that feature,
  minus cross-feature node merging, which is irrelevant to a per-class check.
- **Topology stats.** Every CIM job logs one `CIM topology` line with `features`
  (the exported feature count), `conns` (connectivity edges consumed), `terminals`,
  `nodes` and `parent_bytes` — a rough footprint of the union-find map (about 48
  bytes plus the keys per endpoint). The numbers are the trigger for a future
  persisted-topology design: the day a real export's footprint approaches the
  ~240 MB projected at ten million features, the logs say so.

### ▶ Practical takeaways

1. Connectivity is **pairs in, nodes out**; check the terminal and node counts.
2. **Draw conductors end-to-end** with their neighbours so the nearer-end rule picks
   the right end.
3. A **single-terminal node** is an unconnected end — a data gap, not an encoder error.
4. **Windings, not voltage on the transformer**: voltage and rating live on the
   `PowerTransformerEnd`s.
5. Ground terminals and the connections landing on them **don't appear**.
6. Phases are **expanded objects** for conductors and street lights only.

*Next: Chapter 5 runs an export and reads the file.*

---

## Chapter 5 — Running an export & reading the file

The operator chapter. If you only need to *produce* a CIM file and be confident it
is sound, read this one and Chapter 9 (validation).

### ▶ Step by step — producing the file

🔒 Needs `features:read` (the export route's gate). 🗺️ Your territory access and the
private-draft rules decide what you receive.

1. **Open the export dialog** from the map toolbar (*Export*).
2. **Format → `CIM/XML`** — "CDPSM RDF/XML, for power-system tools".
3. **Pick the classes.** The list shows only classes **with geometry** (a
   geometry-less class can't be exported — see below), seeded from what is currently
   visible on the map. For a solvable model tick **every conducting class**
   (conductors, transformers, switches, busbars, loads) — Chapter 2's scope rule.
4. **Pick the scope**:
   - *Current view* — features inside the map viewport (a bounding box in EPSG:4326);
   - *Whole dataset* — every feature of each selected class;
   - *Territory* — chosen territories **and everything beneath them**.
5. **Optionally pick a coordinate system.** CIM accepts reprojection (below).
6. **Press Export.** The server answers immediately with a job id; the dialog hands
   off to the **jobs panel**, which polls. When the job reads *succeeded* a download
   link appears; the file is `voltedge-export.xml`.

> 💡 **The dialog has no profile picker.** The CIM profile (`cim_profile`) is an API
> parameter; the dialog always uses the default profile. If you maintain more than
> one profile (say a production and a test authority set), request the export through
> the API.

### ▶ The job, from queued to downloaded

Every export — CIM, KMZ, Shapefile, DXF — is a row in `ops.export_jobs` handled
asynchronously, so a big export never ties up a web request.

| State | Meaning |
| --- | --- |
| `queued` | accepted; waiting for a worker (workers claim jobs with `FOR UPDATE SKIP LOCKED`) |
| `running` | a worker holds a lease (default **60 s**, renewed by heartbeat); the job reads under one **repeatable-read** snapshot so the file is internally consistent even while editors keep working |
| `succeeded` | the file is in object storage (RustFS); a download link is available |
| `failed` | an error (the message is on the job); also the end of a *poison* job — one claimed **3 times** without finishing is failed rather than retried forever |
| `cancelled` | you cancelled it (queued or running); a job cancelled mid-run is never resurrected to *succeeded* by a late-finishing encode |

Jobs are drained by the **standalone export worker** (`cmd/exportworker`; the
`exportworker` service in the compose file, scalable with `--scale exportworker=N`) —
not by the web server. **If no worker is running, jobs sit in `queued` forever.** That is
the first thing to check when a job never starts.

Operational facts worth keeping in your head:

| Fact | Value | Knob |
| --- | --- | --- |
| Download link lifetime | **15 minutes**, minted when you *click* (not when the job finished) | `EXPORT_URL_TTL` |
| How long a finished export is kept | **7 days**, then the file and its job row are reaped (`0` disables the sweep) | `EXPORT_RETENTION`, `EXPORT_SWEEP_INTERVAL` (default hourly) |
| Parallel export jobs per worker | **2** | `EXPORT_WORKER_CONCURRENCY` |
| Lease before a crashed worker's job is reclaimed | **60 s** | `EXPORT_JOB_LEASE` |
| Your visible history | your **50** most recent jobs | — |
| Who can download | the job's **owner** only — a job id someone else owns reads as *not found* | — |

### ∑ The API behind the dialog

```
POST /export                      → 202 {"job_id": "…"}   (or a 4xx — see below)
GET  /export/jobs                 → your 50 most recent jobs
GET  /export/jobs/{id}            → one job: status, byte_size, feature_count, error…
POST /export/jobs/{id}/cancel
GET  /export/jobs/{id}/download   → 302 to a 15-minute presigned URL
```

A CIM request:

```json
POST /export
{
  "format": "cim",
  "feature_classes": ["MV_CONDUCTOR", "LV_CONDUCTOR", "DIST_XFMR", "MV_SWITCH",
                      "SERVICE_POINT", "SUBSTATION"],
  "bbox": [-0.2410, 5.5520, -0.1880, 5.6010],
  "territories": ["<territory-id>"],
  "cim_profile": "CDPSM_CIM16",
  "target_srid": 32630
}
```

`bbox` is `[minX, minY, maxX, maxY]` in EPSG:4326; `territories` expands to each
territory's subtree on the server; **both compose as AND** when given. `cim_profile`
empty selects the default profile; `target_srid` empty keeps the stored 4326.

Everything that can fail cleanly fails **synchronously**, before a job exists:

| Status | Message | Cause |
| --- | --- | --- |
| 400 | `unsupported export format "…"` | bad `format` |
| 400 | `no feature classes selected for export` | empty `feature_classes` |
| 404 | `feature class "X" does not exist` | unknown code |
| 400 | `feature class "X" has no geometry, so it can't be exported` | a geometry-less class (see Chapter 6) |
| 400 | `unknown target SRID N (not present in PostGIS spatial_ref_sys)` | bad CRS |
| 400 | `CIM export is not configured: …` | no default profile seeded, or `cim_profile` names none |
| 400 | `bbox must be [minX, minY, maxX, maxY] (4 numbers), got N` / `bbox contains a non-finite number` | malformed `bbox` |
| 401 | `authentication required` | no session |

Failures that only appear at encode time surface later as a `failed` job. The most
common one for CIM is the **per-class cap**:

> `feature class "X" has more than 50000 features, the per-class limit for a
> shapefile/CIM export. Narrow the scope with a bbox or territory filter, or export as
> KMZ or DXF (which stream and are uncapped).`

### ∑ Why CIM is capped and KMZ/DXF are not

KMZ and DXF **stream** — features flow from a database cursor straight to the file,
uncapped, never held in memory. **CIM cannot**: building connected components needs
the *whole* graph, so each requested class is read fully into memory under the
snapshot (capped at **50 000 features per class**), the union-find runs, and only the
*writing* of the XML streams to object storage through a buffered writer. In short:
**constant memory for output, linear memory for topology.** The topology job logs its
size (Chapter 4).

For a network bigger than the cap in one class, split it **along natural seams** —
by territory, or by a bounding box that does not cut a feeder — and deliver several
files. Remember the scope rule from Chapter 2: an export cut mid-feeder produces
floating ends at the cut.

### ∑ Reprojection

`target_srid` reprojects **every output coordinate** with PostGIS `ST_Transform`
before the encoder sees it. Effects specific to CIM:

- The `CoordinateSystem` declares the new CRS (`urn:ogc:def:crs:EPSG::32630`) instead
  of the profile's stored default; the `PositionPoint` x/y are the projected
  easting/northing, written verbatim.
- **Conductor length becomes a planar sum**, because on projected metres the
  Euclidean distance *is* the length; with no reprojection it is the geodesic sum of
  great-circle segments (haversine on a 6 371 km sphere, within about 0.3 % of the
  ellipsoid). Impedance, derived from length, follows.
- ⚠️ **Only reproject to a metre-based CRS** (a UTM zone, a national metric grid).
  A foot- or degree-based target would make the planar length — and everything
  derived from it — wrong, and the encoder does not check units. Confirm the CRS's
  unit before you choose it.
- `PositionPoint.xPosition` is **x** — longitude (or easting) — and `yPosition` is
  **y** — latitude (or northing). The EPSG registry formally defines 4326 with a
  latitude-first axis order, so tell any GIS importer that x is longitude.

### ▶ Before you hand the file over — a 5-minute checklist

1. **Server log** (ask whoever runs the platform, or open the job log): find the
   `CIM topology` line and note `terminals` and `nodes`. A healthy network has
   *noticeably fewer nodes than terminals* (roughly two terminals per node plus a few
   open ends). `nodes ≈ terminals` means almost nothing is connected.
2. **Unmapped warning.** Search the same log for `CIM unmapped classes dropped from
   output`. Every class listed is a class whose features are **missing from your
   file**. Map it or skip it deliberately (Chapter 3).
3. **Per-class preview.** *Admin → CIM mappings →* the row's preview icon on two or
   three key classes (a conductor, a transformer, a switch): does the XML look the way
   you expect?
4. **Validate the structure** with the scripts in Chapter 9 (well-formed XML, no
   dangling references, every line has two terminals, every transformer two ends).
5. **Say what's in it.** Send the recipient the *coverage and gaps* summary
   (Chapter 6) with the file — units (SI base units), the profile, what is not
   carried — so they don't discover it by failing to solve.

### ∑ Reading the file — an annotated tour

The complete Oakwood sample is in Appendix F. This tour walks it top to bottom with
what each part is for. Sizes are from the real file: **23.4 KB for 7 features** —
about **3.3 KB per feature** with topology, in 58 RDF objects plus the header.

**1. The header.** One `md:FullModel` — the model-description record every CIM/XML
file begins with:

```xml
<md:FullModel rdf:about="urn:uuid:8c050932-1884-5017-9ea9-8e3a895e7b66">
  <md:Model.created>2026-10-10T06:00:00Z</md:Model.created>
  <md:Model.description>Oakwood Zone</md:Model.description>
  <md:Model.modelingAuthoritySet>https://voltedge.local/ModelAuthoritySet</md:Model.modelingAuthoritySet>
  <md:Model.profile>http://iec.ch/TC57/61968-13/CDPSM/2013#</md:Model.profile>
</md:FullModel>
```

`FullModel` means "this file is a complete model, not a delta" (the other kind,
`DifferenceModel`, carries changes — **Chapter 11**). The model id is a **content
hash**: re-export an unchanged network and the `rdf:about` mRID is byte-for-byte the
same, so a consumer can tell "same model version" without diffing; it changes only
when the exported content does. `Model.created` is the job's timestamp (so two
separate runs differ there, honestly), but it no longer feeds the id. Combined with
the deterministic element order, two exports of an unchanged model differ **only** in
that one `created` line — which is what makes the difference models of Chapter 11
possible.

**2. The scaffold.** `GeographicalRegion` → `SubGeographicalRegion` (both named
"VoltEdge"), a `CoordinateSystem` whose `crsUrn` states the CRS, and the catch-all
`Line` "Unassigned equipment" that holds any equipment not in a substation.

**3. A substation** — name, region link, and its boundary as a `Location` of
`PositionPoint`s (the polygon's ring, sequence-numbered).

**4. Base voltages** — one per distinct voltage: here `415 V` and `11000 V`, sorted
ascending, each named "`<volts> V`".

**5. Equipment**, one element each, followed by that feature's `Location` and
`PositionPoint`s (a `LineString` span has one position point per vertex), and any
per-phase objects:

```xml
<cim:LoadBreakSwitch rdf:ID="_0663338e-a187-537b-ae65-fdac19774c6c">
  <cim:IdentifiedObject.name>SW-F7-03</cim:IdentifiedObject.name>
  <cim:IdentifiedObject.mRID>0663338e-a187-537b-ae65-fdac19774c6c</cim:IdentifiedObject.mRID>
  <cim:Equipment.EquipmentContainer rdf:resource="#_a15e7a22-11b7-56f5-bfe4-190610adf0b0"/>
  <cim:PowerSystemResource.Location rdf:resource="#_91058252-e603-534f-91e6-28c1165749ca"/>
  <cim:Switch.normalOpen>false</cim:Switch.normalOpen>
</cim:LoadBreakSwitch>
```

`false` here means *normally closed* (`CLOSED` → `false` via the boolean transform).
Note the container is the **substation** — the switch's point is inside its polygon.

**6. Topology** — the part a power-flow tool actually reads. Each node, then its
terminals, then (after a transformer terminal) the winding:

```xml
<cim:ConnectivityNode rdf:ID="_45a2ecbf-ff58-572e-9983-1002455369e2">
  <cim:IdentifiedObject.mRID>45a2ecbf-ff58-572e-9983-1002455369e2</cim:IdentifiedObject.mRID>
  <cim:ConnectivityNode.ConnectivityNodeContainer rdf:resource="#_a15e7a22-…"/>
</cim:ConnectivityNode>
<cim:Terminal rdf:ID="_735779b1-5cb0-5c90-9f9d-fcf1df3e5f45">           <!-- the MV span, end 2 -->
  <cim:Terminal.ConductingEquipment rdf:resource="#_9341c867-…"/>
  <cim:Terminal.ConnectivityNode rdf:resource="#_45a2ecbf-…"/>
  <cim:Terminal.sequenceNumber>2</cim:Terminal.sequenceNumber>
</cim:Terminal>
<cim:Terminal rdf:ID="_341b525b-a44b-5bf2-bf40-20b37a888c95">           <!-- the transformer, T1 -->
  <cim:Terminal.ConductingEquipment rdf:resource="#_93185a15-…"/>
  <cim:Terminal.ConnectivityNode rdf:resource="#_45a2ecbf-…"/>
  <cim:Terminal.sequenceNumber>1</cim:Terminal.sequenceNumber>
</cim:Terminal>
```

Two terminals, one node: **that is the electrical connection.** Everything else in
the file exists to describe the things at either end of it.

**Object census** (what a consumer will count):

| Element | Count | Element | Count |
| --- | --- | --- | --- |
| `PositionPoint` | 14 | `ConnectivityNode` | 5 |
| `Terminal` | 9 | `PowerTransformerEnd` | 2 |
| `ACLineSegmentPhase` | 8 | `BaseVoltage` | 2 |
| `Location` | 7 | `ACLineSegment` | 2 |
| remaining 1 each: `UsagePoint`, `Substation`, `SubGeographicalRegion`, `PowerTransformer`, `LoadBreakSwitch`, `Line`, `GeographicalRegion`, `EnergyConsumer`, `CoordinateSystem` | | | |

**Sizing a real export.** A usable estimate from the sample: roughly **400 bytes per
vertex** (a `PositionPoint`), **700–750 bytes per terminal-with-its-share-of-a-node**,
and **600–900 bytes per equipment element**. A feeder with 30 000 spans of ten
vertices each is therefore on the order of 120 MB — which is why the per-class cap and
the streaming writer both matter.

### ▶ Quick checks from a shell

```bash
# Is it well-formed?
xmllint --noout voltedge-export.xml && echo OK

# What did we get?
grep -o '<cim:[A-Za-z]* rdf:ID' voltedge-export.xml | sort | uniq -c | sort -rn

# Topology size (terminals vs nodes)
grep -c '<cim:Terminal rdf:ID'         voltedge-export.xml
grep -c '<cim:ConnectivityNode rdf:ID' voltedge-export.xml

# Find a piece of equipment by name
grep -n -B1 -A6 'TX-Feeder7-014' voltedge-export.xml
```

*Next: Chapter 6 states honestly what the file covers, approximates and leaves out.*

---

## Chapter 6 — Coverage, gaps & fidelity

VoltEdge's CIM export is real and used, and it is **not** a loss-free CIM
implementation. This chapter is the honest ledger: what is carried faithfully, what
is approximated, what is dropped, and what to do about each. Read it before handing
a file to a solver — and send the relevant parts to whoever receives it.

### ∑ The scorecard

| Aspect | Status | In one line |
| --- | --- | --- |
| **Object identity** (equipment mRIDs) | ✅ faithful | the feature's own UUID, stable across exports |
| **Connectivity** (terminals, nodes) | ✅ faithful, with a heuristic | exact where connectivity rows are right; conductor *ends* are inferred from geometry |
| **Equipment class** (what each thing *is*) | ✅ faithful | declarative mapping, editable, with subtype overrides |
| **Nominal voltage** | ⚠️ partial | shared `BaseVoltage`s for MV classes and transformer windings; **LV classes carry none** in the shipped mapping (G18) |
| **Conductor length** | ✅ approximated | geodesic on a sphere (≈0.3 % of the ellipsoid) or planar if reprojected |
| **Line impedance r, x** | ⚠️ partial | only where a material spec exists; positive sequence only; rounded to 1 mΩ |
| **Transformer rating** (`ratedS`, `ratedU`) | ✅ carried | per winding |
| **Transformer impedance, vector group, taps** | ❌ not carried | no `r`/`x`/`connectionKind`/tap changer |
| **Switch normal state** | ✅ carried | `Switch.normalOpen` only |
| **Switch current state, loads, set-points** | ❌ not carried | no steady-state-hypothesis data (`Switch.open`, `EnergyConsumer.p/q`…) |
| **Phases** | ⚠️ partial | per-phase objects for conductors and street lights; none for transformers/busbars; `Terminal.phases` not written |
| **Geometry** (GL) | ✅ carried | `Location` + `PositionPoint` per vertex, CRS declared |
| **Containment** | ⚠️ partial | substations and modelled containers yes; the `Bay` tier is dormant |
| **Assets & metering** | ⚠️ partial | `Pole`/`Tower`/`UsagePoint` emitted; no link from a meter to its service point |
| **Ratings / thermal limits** | ❌ not carried | `ampacity_amps` in the material catalogue is not exported |
| **Other attributes** | per policy | dropped (default) or carried in the `ve:` namespace |

### ∑ Mapped, skipped, unmapped — and a trap in between

Every feature class falls in one of four buckets:

| Bucket | How it happens | In the file? | Warning? |
| --- | --- | --- | --- |
| **Mapped** | a class-mapping row with a real `cim_kind` | yes | — |
| **Skipped** | a class-mapping row with `cim_kind = skip` | no | no — a recorded decision |
| **Unmapped** | no class-mapping row at all | no | yes — export log + the amber card |
| **Mapped but unreachable** | mapped, but the class has **no geometry** | **never** | no |

The last bucket is the trap. **The export refuses to select a class with no
geometry** (`feature class "X" has no geometry, so it can't be exported`; the dialog
doesn't even list them). So these mapped classes **can never appear in a CIM file**,
whatever the mapping says: `BAY`, `PROTECTIVE_RELAY`, `MV_SWITCHBOARD`,
`MV_SWITCHGEAR_PANEL`, `SUBSTATION_PANEL`. Consequences:

- The **`Bay` container tier** and the modelled Substation → Bay → equipment tree
  cannot be produced today. Equipment is placed in its **substation** (by modelled
  containment, else by polygon) or the default `Line`; the dormant tier code (and a
  `VoltageLevel` arm kept for a future mapping) is in the encoder awaiting a path
  that lets geometry-less containers ride along with the equipment inside them.
- **`ProtectionEquipment`** objects (relays) are not exported.

> 💡 **Workaround while this stands.** Containers that *do* have geometry
> (`SUBSTATION`, `RMU`, `METER_CHAMBER`, `MV_METERING_CUBICLE`) work as parents:
> equipment modelled as contained by an `RMU` is emitted under the RMU's
> `EquipmentContainer`. If you need a bay-like grouping now, model it as a point
> container class with geometry and map it as `container`.

### ∑ Known fidelity limits — the ledger

Each limit says what you see, why, and the workaround. They are numbered so a hand-off
note can cite them.

**G1 — Units are SI base units.** Voltages are **volts** (`11000`), apparent power
**volt-amperes** (`315000`), lengths **metres** (`284.603`), impedance **ohms**. This is
the convention of the CIM *distribution* toolchain (the CIMHub/GridAPPS-D family
states that it works in SI throughout, not the CIM unit-multiplier system). CGMES, the
ENTSO-E transmission profile, conventionally carries **kV / MW / MVA / km**. A consumer
that assumes CGMES units will read 11 000 as 11 000 kV. *Workaround:* tell the
recipient the units; Chapter 8 has a converter. *(Verify the unit of each attribute
against the profile of the tool you target.)*

**G2 — No transformer impedance, vector group or taps.** A `PowerTransformerEnd` has
rating and voltage but no `r`, `x`, `connectionKind` (Dyn11…) or tap changer. The
`DIST_XFMR.source_r_ohm/source_x_ohm` fields are a *source* impedance used by
VoltEdge's own load flow, deliberately not mapped. *Workaround:* supply transformer
impedance from the nameplate or a library downstream; or add mappings for
`PowerTransformerEnd.r`/`.x` if the data exists (Chapter 10).

**G3 — Line impedance is partial.** Resistance and reactance exist **only** where the
conductor's coded value links to a material spec carrying them; they are positive
sequence only (the catalogue's zero-sequence columns and ampacity are not exported),
there is no shunt `bch`/`gch`, and values round to **0.001 Ω**, which truncates short
LV cables (Chapter 3). A span with no resolvable spec has a length and *no* impedance —
the encoder omits rather than guesses. *Workaround:* compute from `Conductor.length`
and your own catalogue downstream, or enrich the specs and mapping.

**G4 — No steady-state data.** CIM separates the *equipment model* from the *steady
state hypothesis* (SSH) — switch positions, loads, set-points — and the *state
variables* (SV) of a solved case. VoltEdge writes the equipment model with
`Switch.normalOpen` as the only state. There is no `Switch.open`, no
`EnergyConsumer.p`/`.q`, no generator output, no tap position. A power-flow run needs
loads: the recipient must attach demand from billing, AMI or an assumption.

**G5 — Connectivity nodes only; no TopologicalNodes.** CIM can describe a network in
*node-breaker* detail (connectivity nodes plus switches) or pre-reduced to
*bus-branch* (topological nodes, switches already resolved). VoltEdge writes the
detailed form and leaves topology processing to the consumer — which is what CDPSM
tools expect, but it means **open switches matter**: the consumer decides which nodes
merge based on switch state (Chapter 7).

**G6 — Phases are partial.** See Chapter 4: per-phase objects only for conductors
(`ACLineSegmentPhase`) and street lights (`EnergyConsumerPhase`); transformers,
busbars and meters are unphased; `Terminal.phases` is never written.

**G7 — Asset location and metering links.** Assets (`Pole`, `Tower`, `UsagePoint`,
`RatioTapChanger`) reference their `Location` through `PowerSystemResource.Location`,
though in the 61968 asset model these classes relate to a location through their own
associations (a known imprecision). More usefully, **a `UsagePoint` (meter) is not
linked to the `EnergyConsumer` (service point) it measures** — the meter appears in the
file but is orphaned. *Workaround:* join on name or position downstream, or extend the
encoder to emit `UsagePoint.Equipment`.

**G8 — One combined file; not CGMES profile-split.** The export writes a single
document in the CDPSM style rather than separate `EQ`/`TP`/`SSH`/`SV`/`GL` files. A
strict CGMES importer expects the split; a CDPSM tool accepts the combined file. Also:
`ConnectivityNode`s and `Terminal`s carry no `IdentifiedObject.name`, and every object
carries an `IdentifiedObject.mRID` literal that strict CGMES profiles don't include
(Chapter 7).

**G9 — Containers.** Described above (`Bay` dormant). Also: cubicle-style containers
map to the generic `EquipmentContainer`, the superclass — strict consumers expect a
concrete subclass (`Substation`, `VoltageLevel`, `Bay`, `Line`) — and the catch-all `Line`
named "Unassigned equipment" holds everything outside a substation. A `ConnectivityNode`
takes its container from geometry, so it may differ from its equipment's container.

**G10 — The graph is closed over your selection.** Cut a feeder with a bounding box or
a territory edge, or leave out a conducting class, and the cut ends float (Chapter 2).
There is also the **50 000-features-per-class** cap.

**G11 — Conductor ends are a geometry heuristic** (Chapter 4). Wrong or ambiguous
geometry silently yields a floating end.

**G12 — Ground terminals vanish.** Terminals with role `ground` and the connections
that land on them are omitted (Chapter 4).

**G13 — Attributes from another class land on the equipment.** A mapping such as
`TapChanger.step` or `RegulatingControl.targetValue` on a regulator class is written
as a literal *inside the transformer element*, though CIM models a tap changer and a
regulating control as separate objects. Strict consumers drop or reject these. They
are in the shipped regulator/tap-changer seeds as the nearest available carriage; treat
them as informational.

**G14 — No voltage-consistency check.** The encoder writes each conductor's own
`BaseVoltage`. CIM profiles require both ends of a line to share a base voltage; if a
33 kV span is wired to an 11 kV transformer terminal in the data, the file says so
without objecting. Chapter 9's validator flags it.

**G15 — Lengths are geodesic on a sphere** (haversine, 6 371 km), within roughly 0.3 %
of ellipsoidal length — fine for planning, not for cadastral work. Reprojected exports
use planar length, correct only in a metre-based CRS.

**G16 — Unplaced features.** A feature with no geometry in a geometry class is still
emitted (and still gets terminals), but with no `Location`; an unplaced conductor has
no length (so no impedance) and its ends fall back to end 0 for any connection.

**G18 — LV equipment has no `BaseVoltage`.** The shipped mapping assigns a voltage
only to `MV_CONDUCTOR`/`MV_BUSBAR` (from their voltage class) and to the transformer
windings. `LV_CONDUCTOR`, `SERVICE_POINT`, `LV_BUSBAR` and other LV classes are written
with **no `ConductingEquipment.BaseVoltage`** — in the Oakwood sample the LV span has
none, and the validator's per-voltage totals list its length under "(no BaseVoltage)".
A consumer must infer LV voltage from the transformer's secondary winding. *Workaround:*
map an LV voltage onto those classes (a `direct`/`domain_map` mapping on a voltage field,
or a constant-valued field) — Chapter 10.

**G17 — The profile label.** The default profile writes the **CIM16** namespace and
the profile identifier `http://iec.ch/TC57/61968-13/CDPSM/2013#`. IEC 61968-13's first
edition (2008) was withdrawn and replaced by a second edition in 2021; the `2013` in
the label is VoltEdge's historical identifier, not a claim about an edition. If a
recipient requires a specific profile identifier, add a profile row (Chapter 10).

### ▶ What to tell the recipient

Copy this into the cover email and edit:

> *Attached is a CIM/XML network model of the Oakwood Zone from VoltEdge, in the
> CDPSM style using the CIM16 namespace, as a single combined RDF/XML file. It is a
> connected equipment model: conductors, transformers (with winding ratings and
> voltages), switches (with their normal state), busbars and loads, joined through
> terminals and connectivity nodes, with positions. Units are SI base units (volts,
> volt-amperes, metres, ohms). Not included: transformer impedance and vector group,
> loads and other steady-state data, thermal limits, topological nodes. Line impedance
> is present only where a conductor specification exists. Equipment ids are stable
> across exports; connectivity-node ids are not.*

*Next: Chapter 7 puts the file in the context of the standard itself.*

---

## Chapter 7 — The standard, in practice

Chapters 1–6 described VoltEdge's export from the inside. This chapter puts it in
context: **what the CIM standards actually are, how a CIM file is meant to be
read, where VoltEdge follows the standard to the letter, and where it makes a
deliberate local choice.** If a recipient asks "is this CGMES?" or "which edition of
the profile?", the answers are here.

> **Sources and certainty.** This chapter draws on the public documentation listed
> in *Further reading* (the EPRI CIM primer, the ENTSO-E and open-CGMES material, the
> OpenDSS/CIMHub and powsybl documentation, the IEC catalogue entries). The IEC
> standards themselves are paywalled and were not read in full; where a statement
> rests on convention or on a tool's documentation rather than on the standard's own
> text, it says so. **Treat unit and profile details as "verify against the profile
> of the tool you are targeting".**

### ∑ The family of standards

"CIM" is not one document. It is a family maintained by IEC Technical Committee 57
and the CIM User Group (UCAIug):

| Standard | What it defines | Why you meet it |
| --- | --- | --- |
| **IEC 61970-301** | the **CIM base**: the UML object model for the *transmission/energy-management* side — Core, Wires (lines, transformers, switches, loads), Topology, Measurement… | the class and attribute names (`ACLineSegment`, `Terminal`, `Switch.normalOpen`) |
| **IEC 61968-11** | the **distribution extensions** to the CIM — assets, metering, work, customers | `Pole`, `Tower`, `UsagePoint` |
| **IEC 61970-501** | how the UML becomes an **RDF Schema** (the `…cim16#` vocabulary) | the namespace URI your file uses |
| **IEC 61970-552** | the **CIMXML model exchange format** — how instances are written as RDF/XML, plus the model-description header | `rdf:ID`/`rdf:resource`, `md:FullModel` |
| **IEC 61970-452 / 453 / 456** | **CPSM profiles** — which classes and attributes a *transmission* exchange uses (equipment, diagram layout, topology & solved state) | the profile idea |
| **IEC 61968-13** | **CDPSM** — *Common Distribution Power System Model* profiles for exchanging distribution network models for power-flow-style analysis (2021 second edition: balanced and unbalanced networks) | **the profile VoltEdge targets** |
| **IEC 61970-600-1 / -2** | **CGMES** — the *Common Grid Model Exchange Standard*, ENTSO-E's regional transmission profile set built on the above | the other big CIM dialect; what European TSO tools speak |
| IEC 62325 | market-communications extensions | not used here |

Three **versions of the CIM schema** circulate, identified by their namespace:

| Name | Namespace | Notes |
| --- | --- | --- |
| **CIM16** | `http://iec.ch/TC57/2013/CIM-schema-cim16#` | the schema CGMES 2.4.15 uses — **VoltEdge writes this** |
| **CIM17** | (CIM16's successor) | the base CIM100 extends |
| **CIM100** | `http://iec.ch/TC57/CIM100#` | used by CGMES 3.0; OpenDSS's CIM export is described as a candidate CIM100 extending CIM17 (standardised 2020) |
| CIM18 | `https://cim.ucaiug.io/ns#` | no matching CGMES release yet |

### ∑ Three layers: schema, profile, serialization

The single most useful idea for reading any CIM file is that there are **three
separate layers**, and people say "CIM" about all of them:

```
  UML object model  ─►  RDF Schema (cim16#…)  ─►  PROFILE (a constrained subset)  ─►  CIMXML instance file
  (what exists)         (names + types)           (what a given exchange uses,        (your .xml)
                                                   mandatory/optional, constraints)
   IEC 61970-301        IEC 61970-501             CDPSM / CGMES / CPSM                IEC 61970-552
```

- The **schema** says an `ACLineSegment` *can* have an `r`. It is enormous, and no
  exchange uses all of it.
- A **profile** says for *this* exchange which classes appear, which attributes are
  mandatory, and which constraints must hold (for example "a two-terminal device's two
  terminals must not share a connectivity node"). Profiles are why a file can be
  *valid CIM* yet rejected by a consumer: valid against the schema, wrong for *its*
  profile.
- The **serialization** is just how you write instances down.

VoltEdge's `profile_uri` is the claim "this file follows CDPSM"; the encoder's mapping
is the *implementation* of that claim — a subset that is deliberately smaller than the
full profile (Chapter 6).

### ∑ CIMXML — the serialization rules (IEC 61970-552)

IEC 61970-552 uses only a **subset of RDF** so that the UML maps directly to RDF
Schema. The rules you can see in any VoltEdge file:

| Rule | In a VoltEdge file |
| --- | --- |
| One XML element per object, named by its **class** in the CIM namespace | `<cim:ACLineSegment …>` |
| Object identity is carried by `rdf:ID` (a fragment identifier within the document) or `rdf:about` (an absolute IRI) | `rdf:ID="_9341c867-…"` (underscore + UUID) |
| An **attribute** is a child element named `Class.attribute` with a literal | `<cim:Conductor.length>284.603</…>` |
| An **association** is a child element whose value is an `rdf:resource` pointing at the target's id | `<cim:Terminal.ConnectivityNode rdf:resource="#_45a2…"/>` |
| An **enumeration value** is also an `rdf:resource`, to the enumeration literal's *absolute* IRI | `…#SinglePhaseKind.A` |
| A **header** `md:FullModel` identifies the document and the profile(s) it conforms to | `Model.profile`, `Model.modelingAuthoritySet`, `Model.created` |
| A model is either a **FullModel** (complete) or a **DifferenceModel** (changes only) | VoltEdge writes `FullModel` |

**Identifier style is the one place the world differs.** Older CIM/CGMES 2.4.15 files
write `rdf:ID="_<uuid>"` and reference it as `rdf:resource="#_<uuid>"` — the form
VoltEdge uses. The 2016 edition of 61970-552 added `urn:uuid:<uuid>` identifiers
(written with `rdf:about` and referenced as `rdf:resource="urn:uuid:<uuid>"`), the form
CGMES 3.0 uses. Parsers cope with both — one open CIMXML parser, for example,
normalises underscore-prefixed and dashed forms into canonical `urn:uuid:` IRIs — but a
**validator or importer written for one style may reject the other**. VoltEdge's header
`md:FullModel` is already written `rdf:about="urn:uuid:…"`; the objects use the
traditional underscore form. Chapter 8 shows the one-line transform to `urn:uuid:` if a
recipient insists.

The **`IdentifiedObject.mRID` literal** VoltEdge also writes is a CIM attribute that
older CIM (and CDPSM) files carry; the CGMES profiles instead let the `rdf:ID`/`rdf:about`
*be* the mRID and do not include the attribute, so a strict CGMES validator reports the
extra element. It is harmless to a CDPSM consumer and trivially stripped (Chapter 8).

### ∑ The classes VoltEdge writes, and where they sit in the CIM

A CIM class inherits everything from its ancestors, which is why `Conductor.length`
appears on an `ACLineSegment`. The inheritance chains for the classes in an export:

```
 IdentifiedObject  (mRID, name)
   └─ PowerSystemResource  (Location)
        ├─ Equipment  (EquipmentContainer)
        │    └─ ConductingEquipment  (BaseVoltage, Terminals)
        │         ├─ Conductor  (length)  └─ ACLineSegment  (r, x, …)
        │         ├─ Switch  (normalOpen, open)  ├─ LoadBreakSwitch  ├─ Breaker  ├─ Disconnector  ├─ Fuse  └─ …
        │         ├─ PowerTransformer  (its windings: PowerTransformerEnd)
        │         ├─ EnergyConsumer  (+ EnergyConsumerPhase)
        │         └─ BusbarSection · Junction · …
        └─ ConnectivityNodeContainer
             └─ EquipmentContainer  ├─ Substation  ├─ Line  ├─ VoltageLevel  └─ Bay
```

Alongside the equipment tree, a few classes tie it together:

| Class | Role | What VoltEdge sets |
| --- | --- | --- |
| **`Terminal`** | a connection point of one piece of conducting equipment | `Terminal.ConductingEquipment`, `Terminal.ConnectivityNode`, `Terminal.sequenceNumber` |
| **`ConnectivityNode`** | the zero-impedance point where terminals meet | `ConnectivityNode.ConnectivityNodeContainer` |
| **`BaseVoltage`** | a system voltage level, shared by reference | `BaseVoltage.nominalVoltage` (volts) |
| **`PowerTransformerEnd`** | a winding | `PowerTransformer`, `endNumber`, `Terminal`, `ratedS`, `ratedU`, `BaseVoltage` |
| **`Location`, `PositionPoint`, `CoordinateSystem`** | the *geographical location* (GL) part | `crsUrn`; per-vertex `xPosition`/`yPosition`/`sequenceNumber` |
| **`GeographicalRegion`, `SubGeographicalRegion`** | the root of the container hierarchy | one of each, named "VoltEdge" |

**Terminal ↔ node is the connection.** In CIM, equipment never points at other
equipment. Two lines are connected because each has a terminal *and both terminals
refer to the same `ConnectivityNode`*. This indirection is what lets one node hold
three, four or ten terminals (a T-off, a busbar) — something a pairwise "A touches B"
record cannot express.

### ∑ Node-breaker or bus-branch? — and the CGMES profile split

CIM can describe the same network at two levels of reduction:

- **Node-breaker** (detailed): `ConnectivityNode`s, plus every switch as equipment. Which
  nodes are electrically one bus depends on the *current switch states*. Needed for
  operations.
- **Bus-branch** (reduced): `TopologicalNode`s — groups of connectivity nodes joined
  through closed switches in the current state — with switches already resolved. For
  planning models, switch states are often *not* used to form topological nodes.

**VoltEdge writes the node-breaker form** — connectivity nodes and switches with their
normal state — and leaves topology processing to the consumer.

CGMES splits one model across several files, each declaring its **profile** in its
header (as described in the powsybl documentation):

| Profile | Carries |
| --- | --- |
| **EQ** — Equipment | the physical equipment and its connections |
| **TP** — Topology | connectivity as it resolves into power-flow buses |
| **SSH** — Steady State Hypothesis | the inputs to a power flow (loads, set-points, switch positions) |
| **SV** — State Variables | the solved result |
| **GL** — Geographical Location | positions |

VoltEdge's single file is, in these terms, **EQ + GL in one document, with no TP, SSH
or SV**. A consumer reads the connectivity nodes, builds its own buses from the
terminals, and supplies its own steady-state data.

How consumers treat a connectivity node: the powsybl importer, for example, makes each
`ConnectivityNode` a node of a node-breaker voltage level, and — because it allows one
terminal per node — inserts internal connections where a CIM node holds several
terminals. A node with *several* terminals is therefore normal, not an error.

### ∑ CDPSM — what the distribution profile contains

CDPSM exchanges distribution networks for analysis. The OpenDSS/CIMHub tooling —
the most widely used open implementation — exports **six** sub-profiles, either
**fragments** (one file each) or **one combined file**:

| Sub-profile | Contents (per the OpenDSS notes) |
| --- | --- |
| **FUN** — Functional | nearly all components with names, phasing, grounding, base voltage, feeder containment, terminals, operational limits |
| **EP** — Electrical Properties | voltage and power ratings, control settings, impedances |
| **TOPO** — Topological | the `ConnectivityNode`s (the buses) |
| **GEO** — Geographical | locations |
| **CAT** — Asset Catalog | catalogue data (wire/transformer information) |
| **SSH** — Steady State Hypothesis | operating state |

Mapped loosely onto VoltEdge's single file: equipment, names, containers and
terminals correspond to **FUN**; ratings and impedances to **EP**; the connectivity
nodes to **TOPO**; the positions to **GEO**. VoltEdge carries **no CAT and no SSH**
content. (This is why Chapter 6 lists missing impedance data and operating state as
gaps: they live in the profiles VoltEdge does not populate.)

### ∑ Rules real validators enforce

Profiles come with constraints, and consumers (and the grid operators that publish
modelling guidelines) enforce them. Three that matter for a distribution export, all
checked by Chapter 9's validator:

| Rule | Why it exists | VoltEdge status |
| --- | --- | --- |
| **Terminal sequence numbers start at 1** and count up; the first terminal is the start of a two-terminal branch | consumers use sequence to orient a branch | ✅ always — conductor ends 1/2, junctions ordinal |
| **The two terminals of a two-terminal device must not share one `ConnectivityNode`** (CGMES constraint 452) | otherwise the device is a self-loop | ✅ normally — but a conductor whose ends are tied together by connectivity violates it (a data error) |
| **Both ends of a line share the same base voltage** | a line cannot step voltage | ⚠️ not enforced — the encoder writes whatever each feature says |

### ∑ Where VoltEdge follows the standard, and where it chooses

| # | Topic | VoltEdge | The standard / convention | Effect |
| --- | --- | --- | --- | --- |
| D1 | Identifier style | `rdf:ID="_uuid"` objects; `urn:uuid:` header | CIMXML 2016 adds `urn:uuid:`; CGMES 3.0 uses it | strict importers may want one style |
| D2 | `IdentifiedObject.mRID` literal | written on every object | CGMES profiles omit it (id *is* the mRID) | extra element for CGMES validators |
| D3 | Units | SI base (V, VA, m, Ω) | CGMES conventionally kV, MVA, MW, km; CIMHub uses SI | scale at the consumer boundary |
| D4 | File layout | one combined document | CGMES: EQ/TP/SSH/SV/GL files | CGMES importers need a splitter |
| D5 | Profile identifier | CDPSM (`…61968-13/CDPSM/2013#`) on the CIM16 namespace | CDPSM ed. 2 (2021); CGMES 2.4.15 shares CIM16 | label is historical |
| D6 | Container classes | generic `EquipmentContainer` for cubicles | concrete subclasses | strict validators object |
| D7 | Asset location | `PowerSystemResource.Location` on assets | asset classes have their own location associations | minor imprecision |
| D8 | Winding voltage channel | `BaseVoltage.nominalVoltage.secondary` mapping target | *not CIM* — a VoltEdge mapping convention | invisible in the output |
| D9 | Extension fields | `ve:` namespace in `carry` mode | CIM permits extensions in their own namespace | ignorable by CDPSM readers |
| D10 | Unmapped attribute policy | drop (default) | n/a | the mapping is the contract |

### ▶ What this means in practice

1. **"Is it CIM?"** Yes — RDF/XML in the CIM16 namespace, CDPSM-style, one combined
   document. **"Is it CGMES?"** No — it shares CGMES 2.4.15's schema namespace but is not
   split into CGMES profiles and uses different units.
2. **Distribution tools (OpenDSS/CIMHub family)** are the natural fit: they speak SI
   units, expect connectivity nodes, and process topology themselves.
3. **Transmission/CGMES tools** can read the equipment model after an adapter step
   (units, id style, optional profile split) — Chapter 8.
4. Always **state the profile, units and gaps** to the recipient (Chapter 6).

### Further reading

- IEC 61970-552 *CIMXML Model Exchange Format* — [IEC webstore preview](https://webstore.iec.ch/p-preview/info_iec61970-552%7Bed1.0%7Db.pdf) · [2016 ed. 2 summary](https://online.standard.no/en/iec-61970-552-2016-ed2)
- [EPRI *Common Information Model Primer*, ch. 7: CIM, RDF and XML](https://msites.epri.com/rd/research/062333/common-information-model-primer/chapter-7:-cim-rdf-and-xml)
- [OpenCGMES — CIMXML overview](https://opencgmes.soptim.de/cimxml/overview) and [CIM version/namespace compliance](https://opencgmes.soptim.de/cimxml/compliance); [CGMES background](https://opencgmes.soptim.de/reference/cgmes-background)
- IEC 61968-13:2021 *Common distribution power system model profiles* — [AFNOR catalogue entry](https://www.boutique.afnor.org/en-gb/standard/iec-61968132021/application-integration-at-electric-utilities-system-interfaces-for-distrib/xs138835/249215) · [ANSI preview of the 2008 edition](https://webstore.ansi.org/preview-pages/IEC/preview_iec61968-13%7Bed1.0%7Den.pdf)
- [OpenDSS — Common Information Model (CIM100)](https://opendss.epri.com/CommonInformationModelCIM100.html) and [Export](https://opendss.epri.com/Export.html); [CIMHub documentation](https://cimhub.readthedocs.io/en/latest/CDPSM.html); [GridAPPS-D CIMHub](https://github.com/GRIDAPPSD/CIMHub)
- ENTSO-E [CGMES library](https://www.entsoe.eu/digital/cim/cim-for-grid-models-exchange/) · [CGMES v2.4.15](https://www.entsoe.eu/Documents/CIM_documents/Grid_Model_CIM/140807_ENTSOE_CGMES_v2.4.15.pdf) · [Implementation guide 2.4.15](https://eepublicdownloads.entsoe.eu/clean-documents/CIM_documents/Grid_Model_CIM/160315_ImplementationGuide_CGMES_2_4_15.pdf)
- [powsybl — CGMES import](https://powsybl.readthedocs.io/projects/powsybl-core/en/stable/grid_exchange_formats/cgmes/import.html) and [overview](https://powsybl.readthedocs.io/projects/powsybl-core/en/stable/grid_exchange_formats/cgmes/index.html)
- [Netbeheer Nederland — connectivity modelling guideline](https://modellen.netbeheernederland.nl/modeling-guidelines/draft-connectivity/connectivity.html) · [Ofgem LTDS CGMES constraints (Appendix 7)](https://www.ofgem.gov.uk/sites/default/files/2024-04/Grid%20Modelling%20Appendix%207%20-%20LTDS%20Subset%20of%20CGMES%20Constraints%20Descriptions.pdf)
- [cimpy — CGMES 2.4.15 class docs](https://sogno.energy/cimpy/cimpy.cgmes_v2_4_15.BaseVoltage.html) (`ConnectivityNode`, `TopologicalNode`, `Terminal`)

*Next: Chapter 8 — taking the file into the tools people actually use.*

---

## Chapter 8 — Taking the file into other tools

A CIM file is only worth what the recipient can do with it. This chapter is the
practical hand-off guide: how to decide what your recipient needs, a protocol for the
first import, the adapter tools that ship in the repository, and recipes for the
common families of consumer.

> **What this chapter can and cannot promise.** VoltEdge emits **one canonical,
> standards-clean file**; byte-perfect import into every tool is not promised, because
> each importer has its own quirks and partial profile support (the Phase 16 decision
> record says the same). What follows is built from the standard, from public tool
> documentation and from the shipped, tested adapters. For any *commercial* tool —
> CYME, PowerFactory, Synergi and the like — the vendor's importer documentation is the
> authority: use the interview in the next section to get the facts.

### ▶ Step 0 — Find out what the recipient actually needs

Ask these ten questions *before* you export. Their answers tell you which recipe below
applies and what to say in the cover note.

| # | Question | Why it matters | VoltEdge answer / action |
| --- | --- | --- | --- |
| 1 | Which **profile and version**? (CDPSM / CGMES 2.4.15 / CGMES 3.0 / CIM100; CIM16 vs 17 vs 100 namespace) | decides the namespace, class names and id style | CIM16 namespace, CDPSM style — see Ch. 7 |
| 2 | **Units**: SI base units or kV/MVA/km? | a unit mismatch silently scales every number | SI by default; `cim_adapt.py --units cgmes` |
| 3 | **Identifier style**: `rdf:ID="_uuid"` or `rdf:about="urn:uuid:…"`? | strict importers reject the other | underscore by default; `--id-style urn` |
| 4 | **One file, or split by profile** (EQ/TP/SSH/SV/GL)? | CGMES importers read profile-tagged files | one file by default; `--split` |
| 5 | Does it need **TopologicalNodes**, or will it process topology itself? | some tools want a pre-reduced TP | VoltEdge writes none — consumer reduces |
| 6 | Does it need **steady-state data** (loads, set-points)? | a power flow with no demand is a no-op | not exported; `cim_add_loads.py` |
| 7 | Which **attributes are mandatory** for its equipment (e.g. winding `r`/`x`, line `bch`)? | missing ones are reported as errors | see Ch. 6 gaps G2/G3 |
| 8 | Does it use **per-phase objects** and `Terminal.phases`? | unbalanced studies need phases | partial — Ch. 4 |
| 9 | Does it honour **GL** positions, and in which CRS/axis order? | maps vs schematics | x = lon/easting, y = lat/northing |
| 10 | How does it handle **unknown namespaces** (`ve:`)? | `carry` mode adds extension elements | default `drop` has none |

### ▶ The first-import protocol — never skip this

The first export to any new tool is a **validation exercise, not a delivery**.

1. **Export a small, known area** — one feeder, one substation, 20–200 features — with
   all its conducting classes. Choose something you can check by eye.
2. **Validate the structure**: `python3 scripts/cim/cim_validate.py export.xml --summary`
   (Chapter 9). Fix errors at the source; read every warning.
3. **Write down the reconciliation totals** the summary prints: object counts by class,
   line length by voltage (km), transformer capacity (kVA).
4. **Import into the tool** and compare **its** totals with yours: number of lines,
   transformers, consumers; total line length; total installed transformer kVA; number of
   buses/nodes. Differences are the whole point — each one is a lesson.
5. **Walk one feeder end to end** in the tool: is the path from the substation to a
   service point continuous? An open end in the tool that is a one-terminal node in your
   validator is a connectivity data gap, not an import bug.
6. **Only then** export the full network.

Expected, benign differences: line lengths differ from VoltEdge's own Bill of Quantities
by a fraction of a percent (CIM uses a spherical great-circle sum, the BoQ an
ellipsoidal one); node counts differ if the tool merges nodes through closed switches.

### The adapter tools

Three small, standard-library-only Python scripts live in `scripts/cim/`, with a
self-test (`python3 scripts/cim/test_cim_tools.py`) that runs them against a real
encoder output (`scripts/cim/samples/oakwood.xml`). They never invent data — they
re-spell, rescale, remove, split and (for demand) accept your numbers.

| Script | Does | Example |
| --- | --- | --- |
| `cim_validate.py` | structural validation + reconciliation totals | `cim_validate.py export.xml --summary` |
| `cim_adapt.py` | units to kV/MVA/km; `urn:uuid:` ids; strip `mRID`/`ve:`; split EQ/GL | `cim_adapt.py in.xml out.xml --units cgmes --id-style urn --strip-mrid` |
| `cim_add_loads.py` | attach `EnergyConsumer.p`/`.q` from a CSV | `cim_add_loads.py in.xml loads.csv out.xml` |

**What `--units cgmes` does, exactly:** `BaseVoltage.nominalVoltage` and
`PowerTransformerEnd.ratedU` ÷ 1 000 (V → kV); `PowerTransformerEnd.ratedS` ÷ 1 000 000
(VA → MVA); `Conductor.length` ÷ 1 000 (m → km). Ohms are unchanged. Applied to the Oakwood
sample: `11000` → `11`, `415` → `0.415`, `315000` → `0.315`, `284.603` → `0.284603`.

**`--split`** writes the three GL classes (`Location`, `PositionPoint`,
`CoordinateSystem`) to one document and everything else to another, each with a copy of the
header whose `Model.profile` you supply — the adapter **refuses to guess profile URIs**
(`--eq-profile` and `--gl-profile` are required), because the right values are specific to
the recipient's CGMES version (ENTSO-E profile URIs encode a profile name and a
major/minor version).

A complete adapted pipeline for a CGMES-style recipient:

```bash
python3 scripts/cim/cim_validate.py export.xml --summary
python3 scripts/cim/cim_add_loads.py export.xml loads.csv with_loads.xml --units cgmes
python3 scripts/cim/cim_adapt.py with_loads.xml cgmes.xml --units cgmes --id-style urn --strip-mrid
python3 scripts/cim/cim_adapt.py cgmes.xml - --split eq.xml gl.xml \
        --eq-profile "<the EQ profile URI the recipient names>" \
        --gl-profile "<the GL profile URI the recipient names>"
python3 scripts/cim/cim_validate.py eq.xml gl.xml        # a split model validates as one; expect W-UNIT — it is kV now, on purpose
```

Two details. `cim_add_loads.py --units cgmes` writes megawatts and megavars, and
`cim_adapt.py --units cgmes` never touches `EnergyConsumer.p`/`.q`, so the order of those
two steps does not matter. And **validate the split files together**: each half refers to
objects in the other (every piece of equipment points at its `Location` in the GL file), so
`eq.xml` alone reports dangling references by design.

### ▶ Recipe A — a distribution-analysis tool (OpenDSS / CIMHub / GridAPPS-D family)

*Best fit.* These tools are built for the CDPSM family: they work in SI units, expect
equipment, connectivity nodes and terminals, and do their own topology processing.

1. Export the whole feeder, **all conducting classes**.
2. Validate; fix open ends.
3. **Add demand** with `cim_add_loads.py` (the model carries none) — or supply it through
   the tool's own load-allocation facility.
4. Add anything the tool needs that VoltEdge does not carry (transformer impedance,
   catalogue data) in the tool.
5. Import; run the reconciliation.

Things to check against the tool's current documentation: the **CIM version/namespace**
it expects (OpenDSS's CIM export is described as a candidate CIM100 extending CIM17; its
loaders target that, while VoltEdge writes the CIM16 namespace — most classes VoltEdge
writes are common to both, but confirm class and attribute spellings), whether it needs the
**asset catalogue (CAT)** profile for wires and transformers, and how it expects **phases**
to be stated.

### ▶ Recipe B — a CGMES-based tool (powsybl, pandapower's CGMES converter, European grid tools)

*Possible with an adapter step.* CGMES is the transmission-oriented profile family; its
tools expect profile-split files and kV/MVA/km.

1. Run the adapted pipeline above (units, `urn:uuid` ids, strip `mRID`, split EQ/GL).
2. Expect the importer to **complain about missing mandatory attributes** — in particular
   the per-winding impedance (`PowerTransformerEnd.r`/`.x`) and anything the EQ profile
   marks mandatory that CDPSM does not. Add them to the file from nameplate data, or
   configure the importer to tolerate them.
3. EQ-only is a supported starting point for at least one open importer (powsybl imports
   the equipment profile first and applies operational data afterwards), so you may not
   need TP/SSH/SV — confirm for your tool.
4. Supply **loads** (SSH) separately, in the tool or via `cim_add_loads.py`.
5. Remember D6 (`EquipmentContainer` for cubicles): a strict validator may want those
   mapped to `Bay`/`Line`/`VoltageLevel`.

### ▶ Recipe C — a commercial distribution suite (CYME, PowerFactory, Synergi …)

*Use the interview.* Each of these has a CIM import of its own vintage and profile support.
Do not assume which profile it reads: ask the vendor Step 0's questions 1, 2, 5, 6 and 7,
then follow Recipe A (CDPSM-style, SI units) or Recipe B (CGMES-style) accordingly. Insist on
the first-import protocol with a small feeder before committing to the full network, and keep
the reconciliation table — it is the artefact that proves, to your planning team and to the
vendor, that the model arrived intact.

### ▶ Recipe D — people who just want to *look* at it

A CIM file is not a map. For a GIS user, export **KMZ or Shapefile** instead; for CAD,
**DXF**. If someone insists on seeing the CIM, any XML viewer shows the structure, and the
`xmllint`/`grep` snippets in Chapter 5 find a named piece of equipment in seconds.

### ∑ Reading import errors

| Symptom in the tool | Likely cause | Fix |
| --- | --- | --- |
| "unknown element/attribute", "not in profile" for `IdentifiedObject.mRID` | the profile omits the mRID attribute (D2) | `--strip-mrid` |
| Voltages 1 000× too large, or lengths 1 000× too long | SI vs kV/km (D3) | `--units cgmes` (or tell the tool the units) |
| Every object "missing" / reference errors | id style (D1) | `--id-style urn` or the reverse |
| "Missing mandatory attribute `PowerTransformerEnd.r`" | gap G2 | supply impedance |
| Network imports as islands | export scope cut the feeder (Ch. 2) or conductor ends wrong (Ch. 4) | re-export a whole feeder; fix open ends |
| Loads/flows are all zero | no steady-state data (G4) | add demand |
| LV buses have no voltage | gap G18 | map an LV voltage (Ch. 10) |
| Namespace not recognised | tool wants CIM100/CIM17 | confirm class compatibility, convert namespace deliberately (not a blind find-and-replace) |
| `ve:` elements rejected | `carry` policy export | use the default `drop` for strict tools |

*Next: Chapter 9 — validating and repairing the model.*

---

## Chapter 9 — Validate, diff and repair

A CIM export can be perfectly well-formed XML and still be a broken network. This
chapter gives you the checks to run *before* the recipient does, the tools that ship
for it, and how to trace each finding back to the data that caused it.

### ▶ The three levels of "is it right?"

| Level | Question | How |
| --- | --- | --- |
| **1. Well-formed** | is it XML at all? | `xmllint --noout file.xml` |
| **2. Structurally sound** | do references resolve; does each line have two terminals; does each transformer have two windings; do node and terminal agree? | `scripts/cim/cim_validate.py` |
| **3. Electrically plausible** | right totals, no floating ends, voltages consistent, units as agreed | the validator's warnings + `--summary` + your reconciliation (Chapter 8) |

Level 2 is where real problems show up. Level 3 is where *data* problems show up.

### ▶ The validator

```bash
python3 scripts/cim/cim_validate.py export.xml --summary
```

Standard library only; exit status `0` clean, `1` errors (or warnings under `--strict`),
`2` unreadable. Real output on the Oakwood sample:

```text
info    58 objects; 9 terminals on 5 nodes; 2 lines; 1 transformers; 2 windings; 0 dangling reference(s)
info    1 of 2 ACLineSegment(s) carry no impedance (no material spec)
WARN   W-OPEN: 1 open end(s) on single-terminal nodes: SW-F7-03 [LoadBreakSwitch] (terminal 1)

0 error(s), 1 warning(s)

counts by class:
  ACLineSegment           2
  ...
line length by base voltage (km):
       11000 V  0.285
  (no BaseVoltage)  0.063
transformer capacity (kVA, sum of each transformer's largest winding rating): 315.0
```

**What it checks** (codes are stable so you can grep for them in CI):

| Code | Level | Meaning | Usual cause |
| --- | --- | --- | --- |
| `E-ID` | error | an object with no `rdf:ID`/`rdf:about` | corrupted or hand-edited file |
| `E-DUP` | error | two objects share an id | a merge of two exports |
| `E-REF` | error | an association points at an object that is not in the file | a **split** file validated half at a time; or a hand edit; *a fresh VoltEdge export never produces this* |
| `E-TERM` | error | a `Terminal` missing its equipment or its node | hand edit |
| `E-SEQDUP` | error | duplicate `sequenceNumber` on one piece of equipment | hand edit |
| `E-2TERM` | error | an `ACLineSegment`/switch/etc. without exactly two terminals | a class mapped `conducting_junction` whose terminal definitions are not two (Chapter 10) |
| `E-452` | error | both terminals of a two-terminal device on the **same node** (CGMES constraint 452) | connectivity ties a conductor's two ends together, or both neighbours map to one end (Chapter 4) |
| `E-END` | error | a winding bound to a terminal of a different transformer | hand edit |
| `E-WIND` | error | a `PowerTransformer` with fewer than two `PowerTransformerEnd`s | a hand edit — the encoder always pads to two |
| `E-NODE` | error | a `ConnectivityNode` with no terminals | hand edit |
| `E-BV` | error | a `BaseVoltage` without a numeric `nominalVoltage` | hand edit |
| `W-OPEN` | warn | **open ends**: nodes with a single terminal | genuine gaps in connectivity (below) |
| `W-VOLT` | warn | one node joins different voltages | a 33 kV conductor wired to an 11 kV terminal; wrong voltage class |
| `W-UNIT` | warn | the largest `BaseVoltage` is under 1000 — the file looks like kV | you converted on purpose (`--units cgmes`), or the voltage mapping is wrong |
| `W-LEN` | warn | `ACLineSegment`s with no length | **unplaced** conductors |
| `W-MRID` | warn | an `IdentifiedObject.mRID` that differs from its `rdf:ID` | hand edit |
| `W-SEQ`, `W-SEQ1` | warn | missing sequence number; numbering not starting at 1 | hand edit |

A fresh export from the current encoder should produce **no errors**; the realistic
findings are `W-OPEN`, `W-LEN`, `W-VOLT` and `W-UNIT`. (An *error* on an untouched export
is a bug — save the file and report it.)

The checks are exercised by a self-test that mutates the real sample (dangling
reference, shared node, missing winding, voltage mismatch, empty node, truncated XML,
kV units) and asserts each is caught: `python3 scripts/cim/test_cim_tools.py`.

### ▶ Tracing an open end back to the data

An open end — `W-OPEN` — means a terminal sits alone on its node. It is **not** a
defect in the file; it is a defect (or an honest dead end) in the *network data*. Some are
legitimate: the end of a feeder, a spare switch way. The rest are gaps. The validator tells
you the equipment and the terminal number; to find the same thing in the database:

```sql
-- Conductors (edges) that have fewer than two connectivity rows: an end is unconnected.
-- Adjust gis.mv_conductor to the physical table of your class (gis.<code>, lower-case).
SELECT c.id, c.name, count(fc.*) AS connections
FROM gis.mv_conductor c
LEFT JOIN runtime.feature_connectivity fc
       ON fc.from_feature_id = c.id OR fc.to_feature_id = c.id
WHERE c.deleted_at IS NULL
GROUP BY c.id, c.name
HAVING count(fc.*) < 2
ORDER BY connections, c.name;
```

For junctions (switches, transformers) compare the terminals the class defines with the
connections present: a transformer should normally have a connection on its HV and LV
terminals.

```sql
-- Which terminals of this class are used at all?
SELECT t.code, t.role, count(fc.*) AS connections
FROM metadata.terminals t
JOIN metadata.feature_classes k ON k.id = t.feature_class_id AND k.code = 'DIST_XFMR'
LEFT JOIN runtime.feature_connectivity fc
       ON fc.from_terminal_id = t.id OR fc.to_terminal_id = t.id
GROUP BY t.code, t.role ORDER BY t.code;
```

Repair at the source — in VoltEdge, connect the features properly (Connect tool; Chapter
"Everyday Operations" and the Substation guide) — then re-export. Do not edit the XML.

> ⚠️ **A conductor with both neighbours on the same end** (Chapter 4) shows up as `W-OPEN`
> on the *other* end of a conductor that clearly looks connected on the map. If the
> connectivity query above returns two rows for it, the culprit is geometry: redraw the
> span so its first vertex is at one device and its last at the other.

### ▶ Command-line recipes (`xmllint` + XPath)

The file is plain XML, so ordinary tools work. The `local-name()` form below avoids
namespace prefixes:

```bash
# well-formedness
xmllint --noout export.xml

# how many terminals / nodes / lines?
xmllint --xpath "count(//*[local-name()='Terminal'])"         export.xml
xmllint --xpath "count(//*[local-name()='ConnectivityNode'])" export.xml
xmllint --xpath "count(//*[local-name()='ACLineSegment'])"    export.xml

# every line's name
xmllint --xpath "//*[local-name()='ACLineSegment']/*[local-name()='IdentifiedObject.name']" export.xml

# the terminal sequence numbers of one piece of equipment (paste its rdf:ID after the #)
xmllint --xpath "//*[local-name()='Terminal'][*[local-name()='Terminal.ConductingEquipment']/@*[local-name()='resource']='#_9341c867-aae5-52b9-859e-147a13a6b332']/*[local-name()='Terminal.sequenceNumber']" export.xml

# both windings' rated voltages
xmllint --xpath "//*[local-name()='PowerTransformerEnd']/*[local-name()='PowerTransformerEnd.ratedU']" export.xml
```

### ▶ Diffing two exports — the regression tool

Because the output is deterministic (Chapter 2), two exports of the *same data* differ
only in the header's `created` time and model id. That makes `diff` a genuine regression
instrument:

```bash
# strip the volatile header lines, then diff
for f in before.xml after.xml; do
  grep -v -e 'md:Model.created' -e '<md:FullModel' $f > ${f%.xml}.stable.xml
done
diff before.stable.xml after.stable.xml | head -50
```

What the diff tells you after you change something:

| You changed… | Expect the diff to show… |
| --- | --- |
| a feature's voltage class | its `BaseVoltage` reference, and the new `BaseVoltage` object |
| a connection (join two features) | **two `ConnectivityNode`s replaced by one**, new node id, the moved `Terminal` — *node ids change*, equipment ids do not |
| a conductor's geometry | its `PositionPoint`s and `Conductor.length` (and `r`/`x`) |
| a mapping (new attribute) | the new literal on every feature of the class — and nothing else |
| nothing | **an empty diff** — if it is not empty, something non-deterministic crept in; investigate |

To get a **before/after for a mapping change without a full export**, use the per-class
**CIM/XML preview** in the admin editor: it renders one real feature through the real
encoder, so you can see the effect of the change on a representative element in seconds.

### ▶ A pre-flight pipeline for a regular hand-off

If you export on a schedule (a weekly model to planning), automate the checks:

```bash
#!/usr/bin/env bash
set -euo pipefail
f=${1:?usage: preflight.sh export.xml}
xmllint --noout "$f"
python3 scripts/cim/cim_validate.py "$f" --summary | tee "${f%.xml}.report.txt"
# fail the run on structural errors; tolerate the expected warnings
python3 scripts/cim/cim_validate.py "$f" > /dev/null
echo "OK to send: $f"
```

The `.report.txt` doubles as the reconciliation sheet you attach to the delivery.

### ∑ What the encoder itself guarantees

Worth knowing because it tells you what *not* to check:

- **Closed references** — every `Terminal` points at equipment in the file, every `PowerTransformerEnd` at a terminal in the file, every container reference at an emitted container (the connectivity load drops rows whose far end isn't exported).
- **Two terminals per line and two windings per transformer**, always, even with no connectivity at all.
- **Sequence numbers start at 1**, are unique per equipment and stable across exports.
- **Determinism** — byte-identical output for identical data (minus the header timestamp).
- **Well-formed output** — all text is escaped; a regression test parses the sample as XML.

And what it does **not** guarantee: that connectivity is *complete*, that two ends of a
line sit on different nodes (a data problem can break this — `E-452`), that voltages
agree across a node (`W-VOLT`), or that the units are the recipient's.

*Next: Chapter 10 — extending the mapping, and the troubleshooting table.*

---

## Chapter 10 — Cookbook, troubleshooting and reference

The practical close: recipes for the changes administrators actually make, the
troubleshooting table, and the reference appendices.

### ▶ Recipes — extending the mapping

Everything here can be done in **Admin → CIM mappings**; the SQL is the same change
expressed as a seed, for repeatability (the shipped seeds use exactly this idiom —
class rows guarded with `NOT EXISTS` because `UNIQUE(feature_class_id, subtype_id)`
treats `NULL` subtypes as distinct; attribute rows with `ON CONFLICT … DO NOTHING`).

**R1 — Map a new conducting class.**

```sql
INSERT INTO metadata.cim_class_mappings (feature_class_id, subtype_id, cim_class, cim_kind)
SELECT fc.id, NULL, 'Breaker', 'conducting_junction'
FROM metadata.feature_classes fc
WHERE fc.code = 'MV_NEW_BREAKER'
  AND NOT EXISTS (SELECT 1 FROM metadata.cim_class_mappings m
                  WHERE m.feature_class_id = fc.id AND m.subtype_id IS NULL);

INSERT INTO metadata.cim_attribute_mappings (field_id, cim_attribute, transform, value_map, unit)
SELECT f.id, 'Switch.normalOpen', 'boolean', '{"OPEN":true,"CLOSED":false}'::jsonb, NULL
FROM metadata.fields f
JOIN metadata.feature_classes fc ON fc.id = f.feature_class_id
WHERE fc.code = 'MV_NEW_BREAKER' AND f.name = 'device_state'
ON CONFLICT (field_id, cim_attribute) DO NOTHING;
```

Then click the row's **CIM/XML preview**. For a junction class, make sure the class has
terminal definitions (`metadata.terminals`) or it will get a single implicit terminal —
a switch needs two.

**R2 — Give LV equipment a voltage (closes gap G18).** A voltage mapping needs a field
that carries the voltage. If the LV class has none, add a coded field (for example
`voltage_class` with a domain containing `LV`, defaulting to `LV`) to the class in the
metadata editor, recompile, then map it:

```sql
INSERT INTO metadata.cim_attribute_mappings (field_id, cim_attribute, transform, value_map, unit)
SELECT f.id, 'BaseVoltage.nominalVoltage', 'domain_map', '{"LV":415}'::jsonb, 'V'
FROM metadata.fields f JOIN metadata.feature_classes fc ON fc.id = f.feature_class_id
WHERE fc.code IN ('LV_CONDUCTOR','SERVICE_POINT','LV_BUSBAR') AND f.name = 'voltage_class'
ON CONFLICT (field_id, cim_attribute) DO NOTHING;
```

The shared `BaseVoltage` object for 415 V already exists (the transformer's LV winding
uses it) and is *reused by reference* — no duplicate object is created.

**R3 — Line impedance for a conductor class.** Ensure the conductor field's coded values
link to material specs that carry per-km resistance/reactance, then:

```sql
INSERT INTO metadata.cim_attribute_mappings (field_id, cim_attribute, transform, unit)
SELECT f.id, v.attr, 'material_spec', 'ohm'
FROM metadata.fields f JOIN metadata.feature_classes fc ON fc.id = f.feature_class_id
CROSS JOIN (VALUES ('ACLineSegment.r'), ('ACLineSegment.x')) AS v(attr)
WHERE fc.code = 'MV_CONDUCTOR' AND f.name = 'conductor_spec'
ON CONFLICT (field_id, cim_attribute) DO NOTHING;
```

(`mv_conductor_specs.sql` ships this for `MV_CONDUCTOR`.) The class must also have the
`Conductor.length` / `from_geometry` mapping — impedance is per-km × length.

**R4 — A transformer attribute on the windings — and its trap.** Anything targeted at
`PowerTransformerEnd.*` or `TransformerEnd.*` is written on **every winding with the same
value**. That is right for `ratedS` (a transformer's apparent-power rating is common to its
windings) and **wrong for per-winding electrical values**: a single `PowerTransformerEnd.r`
mapping would write the same resistance on the HV and the LV winding, double-counting the
impedance. The shipped mapping mechanism cannot express "winding 1 gets X, winding 2 gets Y"
except for voltage. Until per-winding channels exist, supply transformer impedance
downstream (G2).

**R5 — Treat one subtype differently.**

```sql
INSERT INTO metadata.cim_class_mappings (feature_class_id, subtype_id, cim_class, cim_kind)
SELECT fc.id, st.id, 'Recloser', 'conducting_junction'
FROM metadata.feature_classes fc
JOIN metadata.subtypes st ON st.feature_class_id = fc.id AND st.code = 2
WHERE fc.code = 'DIST_SWITCH'
  AND NOT EXISTS (SELECT 1 FROM metadata.cim_class_mappings m
                  WHERE m.feature_class_id = fc.id AND m.subtype_id = st.id);
```

**R6 — Exclude a class on purpose** (a recorded decision, no warning):

```sql
INSERT INTO metadata.cim_class_mappings (feature_class_id, subtype_id, cim_class, cim_kind)
SELECT fc.id, NULL, '(not exported)', 'skip'
FROM metadata.feature_classes fc
WHERE fc.code = 'CABLE_DUCT'
  AND NOT EXISTS (SELECT 1 FROM metadata.cim_class_mappings m
                  WHERE m.feature_class_id = fc.id AND m.subtype_id IS NULL);
```

**R7 — See what is unmapped (the SQL behind the amber card).**

```sql
SELECT fc.code, fc.name, fc.geometry_type
FROM metadata.feature_classes fc
WHERE NOT EXISTS (SELECT 1 FROM metadata.cim_class_mappings m WHERE m.feature_class_id = fc.id)
ORDER BY fc.code;
```

Remember that a class with geometry type `none` is *mapped but unreachable* (Chapter 6),
so "unmapped" and "will appear" are different questions.

**R8 — A production authority set and a test profile.** Change the placeholder authority on
the default profile, and add a second profile for non-production exports:

```sql
UPDATE metadata.cim_profiles
   SET model_authority = 'https://assets.example-utility.com/cim/ModelAuthoritySet'
 WHERE code = 'CDPSM_CIM16';

INSERT INTO metadata.cim_profiles
  (code, name, cim_namespace, profile_uri, model_authority, unmapped_attr_policy, is_default)
VALUES ('CDPSM_CIM16_CARRY', 'CDPSM CIM16 + extension fields',
        'http://iec.ch/TC57/2013/CIM-schema-cim16#',
        'http://iec.ch/TC57/61968-13/CDPSM/2013#',
        'https://assets.example-utility.com/cim/ModelAuthoritySet', 'carry', false);
```

Request the second one with `"cim_profile": "CDPSM_CIM16_CARRY"`. Mappings are **global** —
profiles differ in namespace, identity and the unmapped policy, not in which classes map
(a profile can't carry its own class list today).

**R9 — Re-running seeds after editing them.** The shipped seeds use `ON CONFLICT DO
NOTHING`, so re-running a seed whose *target* you changed **adds a second mapping and
leaves the old one** — the export would then emit *both* attributes. To re-target an
existing row, `UPDATE` it (as `seed/cim_mapping_target_fixes.sql` does, dropping any
canonical duplicate first). After changing mappings, re-run
`seed/cim_reference_profile_members.sql` so the vocabulary browser's *in profile* flags
match what the export now targets.

### ▶ Troubleshooting

| Symptom | Likely cause | What to do |
| --- | --- | --- |
| Job stays `queued` | no export worker running | start `cmd/exportworker` (the `exportworker` compose service) |
| Job `failed`: *more than 50000 features* | per-class cap for materialised formats | narrow with a bbox/territory, or export in parts |
| Job `failed` after a long wait, error mentions attempts | a poison job (claimed 3 times, never finished) — the encode crashes or runs out of memory | export a smaller area; check the worker's memory and logs |
| `400 CIM export is not configured` | no default profile (or the named one doesn't exist) | seed/restore `metadata.cim_profiles`; check `cim_profile` spelling |
| `400 … has no geometry, so it can't be exported` | a geometry-less class was requested | remove it; it cannot be exported (Chapter 6) |
| Download says *not ready* (`409`) | the job hasn't `succeeded` | wait; check the job's status |
| Download link no longer works | the **15-minute** link expired | click Download again (a new link is minted per click) |
| File/job vanished | **7-day** retention swept it | re-export |
| A class is missing from the file | unmapped (log warning), or `skip`, or geometry-less, or not selected, or outside your territory/bbox | Chapter 6 buckets; check the log's `CIM unmapped classes dropped …` line |
| Features missing that you can see on the map | territory scope, **private drafts** (someone else's unpublished feature), or the bbox | export as a user who can see them; publish drafts; widen the box |
| Equipment present but **no terminals** | the class is mapped `asset`/`container`/`protection`, not `conducting_*` | change the kind if it carries current |
| Everything is on its own node; `nodes ≈ terminals` | classes missing from the selection, or connectivity not captured | tick all conducting classes; fix connectivity |
| A feeder looks cut off | the export scope cut it | export whole feeders (Chapter 2) |
| A conductor's far end is floating though the map looks joined | both neighbours nearer the same end, or a neighbour with no geometry | redraw end-to-end; Chapter 4 |
| Transformer has no voltage/rating | `primary_voltage`/`secondary_voltage`/`rated_kva` not mapped or null; domain code not in the `value_map` | add the mapping / fix the value_map keys |
| LV conductor/load has no voltage | gap G18 | recipe R2 |
| No `r`/`x` on a line | the conductor's coded value has no material spec (or none carries resistance/reactance) | add specs (R3) |
| `r` seems too small on short cables | rounding to 1 mΩ | Chapter 3 precision note |
| `ve:` elements in the file | the profile's policy is `carry` | use the default profile for strict consumers |
| Tool says coordinates are swapped | x = lon/easting, y = lat/northing vs a lat-first CRS reading | tell the importer the axis order (Chapter 5) |
| Lengths 1000× or voltages 1000× off | units mismatch (D3) | `cim_adapt.py --units` |
| `E-452` from the validator | both ends of a conductor on one node | find it by name; fix connectivity/geometry |
| `W-VOLT` from the validator | a node joins different voltages | fix the voltage class on the wrong feature, or the connection |

## Chapter 11 — Incremental exchange: difference models & sync targets

Everything so far produces a **full model** — the whole network in one file. That is
right for an *initial* load into another system. But once that system holds your
model, re-sending the whole thing every time is wasteful and makes "what actually
changed?" the consumer's problem. CIM's answer is the **difference model**
(`dm:DifferenceModel`): a file that carries only the changes — statements to add
(`forwardDifferences`) and statements to remove (`reverseDifferences`). This chapter
is how VoltEdge produces those, on a schedule, and pushes a "ready" signal to the
consumer. Tom (integration engineer) owns the far end; Ada (admin) sets it up here.

### ∑ The approach — diff two full models, don't track changes

VoltEdge computes a difference by **encoding the current full model and diffing it,
at the RDF-statement level, against a stored baseline** (the last full model this
consumer received). It does *not* watch edits and accumulate a running delta.

That choice is deliberate, and it is why Chapters 5's determinism work matters:

- The encoder is **reused unchanged** — the current model is produced exactly as a
  normal export, then parsed back to statements and compared.
- It is **self-healing**: because every run compares two *complete* models, nothing
  can silently drift. A hard-deleted connection, a missed edit — all are caught on
  the next run by construction. A change-tracking (CDC) engine would re-implement the
  mapping in delta form and has to get every cross-feature effect right (a rewired
  switch moves `ConnectivityNode`s); one bug there and the consumer's model diverges
  with no signal. For a system-of-record feed, correctness-by-construction wins.
- It only works because **object mRIDs are stable** (Chapter 2) and **output is
  deterministic** (the content-hashed model id + ordered elements from Chapter 5). A
  changed transformer rating becomes "remove the old `ratedS` statement, add the new
  one" on the *same* subject — not delete-and-recreate.

### ▶ Sync targets — the named consumers (Admin → CIM sync targets)

A **sync target** is one external consumer with its own baseline. 🔒 `metadata:write`.

1. **Admin → CIM sync targets → New target.** Give it a name (the consumer),
   pick its **feature classes**, optionally a **bounding box** and **territories**
   (none = whole scope), a **CIM profile** (Chapter 3; blank = default) and a
   **target SRID** (Chapter 5's reprojection, optional).
2. ⚠️ A sync target always exports **published-only** — private, never-published
   drafts are excluded, so the consumer gets a stable, authoritative model rather
   than anyone's in-progress edits. This is independent of your personal draft
   visibility.
3. **Run** it. The run is an ordinary export job (same queue, same jobs panel,
   same download) — only the artifact differs.

### ∑ The baseline — full on the first run, a difference after

Each target remembers the **baseline**: the object key + identity of the full model
it last received, stored in a *separate* object-store bucket so the export retention
sweep (Chapter 5) never deletes it.

- **First run (no baseline):** the artifact is a **full model** — the initial load.
  The baseline is then recorded.
- **Every run after:** VoltEdge builds the current full model, fetches the baseline,
  diffs them, and the artifact is a **difference model**. On success — *only after
  the artifact is confirmed delivered* — the baseline advances to the new model. If
  delivery fails, the old baseline stays, so the next run re-emits the same delta
  (safe to re-apply) rather than skipping it.
- Editing a target's **scope** clears its baseline: a diff against a model built over
  a different scope would be nonsense, so the next run is a fresh full load.

### ∑ Reading a difference model

A difference model is small — it quotes only what moved. Say Dana re-rates
TX-Feeder7-014 from 315 kVA to 500 kVA and Ada runs the target. The artifact:

```xml
<rdf:RDF xmlns:rdf="…" xmlns:cim="…" xmlns:md="…"
         xmlns:dm="http://iec.ch/TC57/61970-552/DifferenceModel/1#">
  <dm:DifferenceModel rdf:about="urn:uuid:…">        <!-- id = hash of the delta -->
    <md:Model.created>2026-11-06T…Z</md:Model.created>
    <dm:reverseDifferences rdf:parseType="Statements">
      <rdf:Description rdf:about="#_<ptend-mRID>">
        <cim:PowerTransformerEnd.ratedS>315000</cim:PowerTransformerEnd.ratedS>
      </rdf:Description>
    </dm:reverseDifferences>
    <dm:forwardDifferences rdf:parseType="Statements">
      <rdf:Description rdf:about="#_<ptend-mRID>">
        <cim:PowerTransformerEnd.ratedS>500000</cim:PowerTransformerEnd.ratedS>
      </rdf:Description>
    </dm:forwardDifferences>
  </dm:DifferenceModel>
</rdf:RDF>
```

- Statements are grouped by subject under `rdf:Description rdf:about="#_<mRID>"`.
- The **same mRID** carries the old and new value — that is the stable-identity payoff.
- A **new** object appears only under `forwardDifferences` (all its statements), a
  **deleted** one only under `reverseDifferences`. An **unchanged** object — and the
  whole scaffold, which has content-derived ids — appears in neither.
- The `DifferenceModel`'s own `rdf:about` is a hash of the delta, so the same change
  produces the same difference identity.

**▶ To consume it:** apply `reverseDifferences` (remove those statements), then
`forwardDifferences` (add these), matching objects by mRID. Run two unchanged exports
and both difference sets are **empty** — nothing to apply, and proof the pipeline is
sound.

### ▶ Scheduling — run it without clicking

A target's **Auto-run** can be `Hourly` / `Daily` / `Weekly` (or `Manual`). The
export worker's maintenance loop enqueues a run when a scheduled target's last
*successful* run is older than its cadence and nothing is already in flight (so a
slow run never stacks). A scheduled run is attributed to the target's creator.
Tune the check interval with `EXPORT_SYNC_SCHEDULE_INTERVAL` (default 5 min; `0`
disables scheduling).

💡 Pick the cadence the consumer actually needs. There is deliberately **no
"skip when nothing changed" optimisation**: a safe skip would need a complete
"did anything in scope change?" signal, and one does not exist — a *revoked*
connection is a hard delete with no timestamp, yet it changes the topology, so a
naive watermark would silently drift the consumer. The self-healing full diff is
cheap for the bounded scopes a target covers, so **cadence is the cost lever**, not a
gate.

### ▶ Push delivery — the `export.ready` webhook

Rather than have the consumer poll, VoltEdge can **push a notification** when a sync
export finishes, over the outbound-webhooks pipeline (see the Integrations Guide).
Subscribe an endpoint to the `export.ready` event type (**Admin → Webhooks**), and
each completed sync export POSTs a signed envelope whose `data` carries the job id,
the target, the kind (`full`/`difference`), the add/remove counts, the byte size, and
a **time-limited presigned `download_url`** the consumer fetches with no VoltEdge
session. ⚠️ That URL expires (`EXPORT_URL_TTL`); the `job_id` is the stable reference,
so a consumer should pull promptly.

### ∑ What this does and doesn't change

| | |
| --- | --- |
| Engine | the *same* encoder + mapping (Chapters 3–4); a difference is a post-pass |
| Profile / scope | a target's own (published-only); same `cim_profile`/`target_srid` |
| Identity | object mRIDs and the model-id hash from Chapter 5 are the whole basis |
| Delivery | file (download) + optional webhook push; same jobs/queue/retention |
| Not built | a CDC/running-delta engine; a change-skip gate (see above) |

*Next: the reference appendices.*

### Appendix A — Element reference: what the encoder writes

Every element the encoder emits, with its predicates. "Literal" = text child;
"ref" = `rdf:resource` to another object; "enum" = absolute URI to an enumeration literal.

| Element | Predicates written |
| --- | --- |
| `md:FullModel` (`rdf:about="urn:uuid:…"`) | `Model.created`, `Model.description`, `Model.modelingAuthoritySet`, `Model.profile` |
| `GeographicalRegion` | `IdentifiedObject.name` ("VoltEdge"), `.mRID` |
| `SubGeographicalRegion` | name, mRID, `SubGeographicalRegion.Region` (ref) |
| `CoordinateSystem` | name (= the CRS URN), mRID, `CoordinateSystem.crsUrn` |
| `Line` (the default "Unassigned equipment") | name, mRID, `Line.Region` (ref) |
| `Substation` | name, mRID, `Substation.Region` (ref), `PowerSystemResource.Location` (ref, if it has geometry) |
| `EquipmentContainer` (other containers) | name, mRID, `PowerSystemResource.Location` (ref, if geometry) |
| `Bay` / `VoltageLevel` *(dormant tiers)* | name, mRID, `Bay.VoltageLevel` or `Bay.Substation` (ref); `VoltageLevel.Substation`, `VoltageLevel.BaseVoltage` |
| `BaseVoltage` | name ("`<volts> V`"), mRID, `BaseVoltage.nominalVoltage` (volts) |
| **Conducting equipment** (e.g. `ACLineSegment`, `PowerTransformer`, `LoadBreakSwitch`, `EnergyConsumer`) | name, mRID, `Equipment.EquipmentContainer` (ref), `ConductingEquipment.BaseVoltage` (ref — *not* for `PowerTransformer`; only when a voltage resolves), `PowerSystemResource.Location` (ref, if geometry), mapped literals (sorted by name), `ve:*` (carry mode only), and for conductors `Conductor.length` (m), `ACLineSegment.r`, `.x` (Ω) |
| **Asset** (`Pole`, `Tower`, `UsagePoint`, `RatioTapChanger`) | name, mRID, `PowerSystemResource.Location` (ref, if geometry), `ve:*` (carry) |
| **Protection** (`ProtectionEquipment`) | name, mRID |
| `Location` | mRID, `Location.CoordinateSystem` (ref) |
| `PositionPoint` (one per vertex) | `PositionPoint.Location` (ref), `.sequenceNumber`, `.xPosition`, `.yPosition` |
| `ACLineSegmentPhase`, `EnergyConsumerPhase` | mRID, `<Class>.phase` (**enum** `…#SinglePhaseKind.A/B/C/N`), `<Class>.<EquipmentClass>` (ref) |
| `ConnectivityNode` | mRID, `ConnectivityNode.ConnectivityNodeContainer` (ref) |
| `Terminal` | mRID, `Terminal.ConductingEquipment` (ref), `Terminal.ConnectivityNode` (ref), `Terminal.sequenceNumber` |
| `PowerTransformerEnd` | mRID, `PowerTransformerEnd.PowerTransformer` (ref), `TransformerEnd.endNumber`, `TransformerEnd.Terminal` (ref), end-targeted mapped literals (e.g. `ratedS`), `PowerTransformerEnd.ratedU`, `TransformerEnd.BaseVoltage` (ref) |

Not written anywhere: `Terminal.phases`, `Switch.open`, `Switch.retained`,
`EnergyConsumer.p/q`, `PowerTransformerEnd.r/x/connectionKind`, `TopologicalNode`, any
`Measurement`, `CurrentLimit`, or `IdentifiedObject.name` on nodes and terminals.

### Appendix B — API and environment reference

| Item | Value |
| --- | --- |
| Permission | `features:read` |
| Routes | `POST /export`; `GET /export/jobs`, `GET /export/jobs/{id}`, `POST /export/jobs/{id}/cancel`, `GET /export/jobs/{id}/download` |
| Request fields | `format` (`cim`), `feature_classes[]`, `bbox[4]`, `territories[]`, `cim_profile`, `target_srid` |
| Admin routes (`metadata:read`/`write`) | `/admin/cim-profiles`, `/cim-class-mappings`, `/cim-attribute-mappings`, `/cim-coverage`, `/cim-preview/{class}`, `/cim-dictionary/classes[/{name}/attributes]` |
| Per-class cap | 50 000 features |
| Worker | `cmd/exportworker`; `EXPORT_WORKER_CONCURRENCY` (2), `EXPORT_JOB_LEASE` (60 s) |
| Sync targets (Ch 11) | `GET/POST /export/sync-targets`, `GET/PATCH/DELETE /export/sync-targets/{id}`, `POST /export/sync-targets/{id}/run` (`metadata:read`/`write`) |
| Sync scheduler | `EXPORT_SYNC_SCHEDULE_INTERVAL` (5 min; `0` disables); baselines in bucket `voltedge-cim-baselines` |
| Download | `EXPORT_URL_TTL` (15 min); file `voltedge-export.xml`; owner-only |
| Retention | `EXPORT_RETENTION` (7 days; `0` = keep forever), `EXPORT_SWEEP_INTERVAL` (1 h) |
| Content type / extension | `application/rdf+xml` / `.xml` |
| Object store | RustFS bucket `voltedge-exports` (`RUSTFS_BUCKET`) |

### Appendix C — Glossary

| Term | Meaning |
| --- | --- |
| **CIM** | Common Information Model — the IEC object model for power systems |
| **CIMXML** | the RDF/XML serialization of CIM instances (IEC 61970-552) |
| **CDPSM** | Common Distribution Power System Model — the distribution profile (IEC 61968-13) |
| **CGMES** | Common Grid Model Exchange Standard — ENTSO-E's transmission profile set |
| **Profile** | a constrained subset of the CIM for one exchange |
| **mRID** | master resource identifier — a CIM object's UUID |
| **Terminal** | a connection point of one piece of conducting equipment |
| **ConnectivityNode** | the point where terminals meet, zero impedance |
| **TopologicalNode** | a group of connectivity nodes joined through closed switches (bus-branch) |
| **Node-breaker / bus-branch** | detailed vs reduced topology levels |
| **EQ / TP / SSH / SV / GL** | CGMES profiles: equipment, topology, steady-state hypothesis, state variables, geographical location |
| **BaseVoltage** | a shared system voltage level objects reference |
| **PowerTransformerEnd** | a transformer winding |
| **SinglePhaseKind** | CIM enumeration of single phases (A, B, C, N …) |
| **Declaring class** | the class that defines a property; CIM writes the property under it |
| **UUIDv5** | RFC 4122 name-based UUID; VoltEdge's deterministic id for synthesized objects |
| **Union-find** | the disjoint-set algorithm used to group endpoints into nodes |

### Appendix D — What changed since the earlier build notes

`phase-16-cim-export.md` and `phase-55-cim-mapping-admin.md` are the build records; where
they differ from this guide, **this guide is current**. The principal differences:

| Earlier note says | Now |
| --- | --- |
| mapping is SQL/seed-only; no admin UI | a full admin editor exists (**Admin → CIM mappings**), with coverage, per-class preview and a CIM vocabulary browser |
| the `carry` policy is "not implemented" | implemented: unmapped scalar fields ride as `ve:*` elements |
| MV conductors have no impedance (no MV specs) | `mv_conductor_specs.sql` maps `MV_CONDUCTOR.conductor_spec` → `r`/`x` where specs exist |
| class is `SUBSTATION_YARD` | renamed `SUBSTATION` (migration 0085) |
| transformer LV winding has no voltage | the secondary voltage channel gives winding 2 `ratedU` + `BaseVoltage` |
| containment is substation-only, by polygon | modelled containment (relationships) wins; polygon is the fallback; the `Bay` tier is dormant (geometry-less) |
| output streams in constant memory | **output** streams; each class is read fully (≤ 50 000) and topology is held in memory |
| CIM does not support reprojection | `target_srid` is accepted for CIM (the `CoordinateSystem` declares it; length becomes planar) |
| `PowerTransformerEnd.ratedS` as a literal on the transformer too | **fixed** — written on the windings only |
| unmapped classes drop silently | logged as a warning and listed in the admin coverage card |
| `DIST_SWITCH` | split into `MV_SWITCH`/`LV_SWITCH`/`MV_RECLOSER` on migrated installs |
| the model id + creation time change on every export | the `FullModel` id is a **content hash** (stable when the network is unchanged); `created` is the job timestamp and no longer feeds the id; element order is deterministic, so two unchanged exports differ only in `created` |
| full models only; `DifferenceModel` not produced | **incremental difference models** via named **sync targets** (Chapter 11), diffed against a stored baseline, with `Hourly`/`Daily`/`Weekly` scheduling and an `export.ready` webhook push |

### Appendix E — The shipped tools

| File | Purpose |
| --- | --- |
| `scripts/cim/cim_validate.py` | structural validator + reconciliation summary |
| `scripts/cim/cim_adapt.py` | units, id style, strip, split |
| `scripts/cim/cim_add_loads.py` | attach `EnergyConsumer.p`/`.q` from CSV |
| `scripts/cim/test_cim_tools.py` | self-test against the real sample |
| `scripts/cim/samples/oakwood.xml` | the real encoder output the guide quotes |
| `api/internal/export/cim_test.go` | Go test that builds the Oakwood fixture, regenerates the sample (`CIM_SAMPLE_OUT=…`) and asserts the encoder's guarantees |

To regenerate the sample after a change to the encoder:

```bash
cd api && CIM_SAMPLE_OUT=../scripts/cim/samples/oakwood.xml \
  go test ./internal/export/ -run TestCIMOakwoodSample -count=1
python3 ../scripts/cim/test_cim_tools.py
```

*End of the CIM Export Guide's main text. Appendix F follows with the complete sample file.*

---

### Appendix F — The complete Oakwood sample file

This is the **unedited output of the encoder** for the Oakwood fixture
(`scripts/cim/samples/oakwood.xml`, generated by `TestCIMOakwoodSample`): seven
features, 58 RDF objects, 5 connectivity nodes, 9 terminals. Search it for
`TX-Feeder7-014` and follow the `rdf:resource` references — they are the whole
model. (Header time and model id differ in a real export; everything else is
deterministic.)

```xml
<?xml version="1.0" encoding="UTF-8"?>
<rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#" xmlns:cim="http://iec.ch/TC57/2013/CIM-schema-cim16#" xmlns:md="http://iec.ch/TC57/61970-552/ModelDescription/1#">
  <md:FullModel rdf:about="urn:uuid:8c050932-1884-5017-9ea9-8e3a895e7b66">
    <md:Model.created>2026-10-10T06:00:00Z</md:Model.created>
    <md:Model.description>Oakwood Zone</md:Model.description>
    <md:Model.modelingAuthoritySet>https://voltedge.local/ModelAuthoritySet</md:Model.modelingAuthoritySet>
    <md:Model.profile>http://iec.ch/TC57/61968-13/CDPSM/2013#</md:Model.profile>
  </md:FullModel>
  <cim:GeographicalRegion rdf:ID="_e61e35f7-3138-5114-ba29-086becbec618">
    <cim:IdentifiedObject.name>VoltEdge</cim:IdentifiedObject.name>
    <cim:IdentifiedObject.mRID>e61e35f7-3138-5114-ba29-086becbec618</cim:IdentifiedObject.mRID>
  </cim:GeographicalRegion>
  <cim:SubGeographicalRegion rdf:ID="_c6f369f6-260e-5723-bc04-5365252cd3d3">
    <cim:IdentifiedObject.name>VoltEdge</cim:IdentifiedObject.name>
    <cim:IdentifiedObject.mRID>c6f369f6-260e-5723-bc04-5365252cd3d3</cim:IdentifiedObject.mRID>
    <cim:SubGeographicalRegion.Region rdf:resource="#_e61e35f7-3138-5114-ba29-086becbec618"/>
  </cim:SubGeographicalRegion>
  <cim:CoordinateSystem rdf:ID="_0382134e-be43-586a-984d-0584f775a4af">
    <cim:IdentifiedObject.name>urn:ogc:def:crs:EPSG::4326</cim:IdentifiedObject.name>
    <cim:IdentifiedObject.mRID>0382134e-be43-586a-984d-0584f775a4af</cim:IdentifiedObject.mRID>
    <cim:CoordinateSystem.crsUrn>urn:ogc:def:crs:EPSG::4326</cim:CoordinateSystem.crsUrn>
  </cim:CoordinateSystem>
  <cim:Line rdf:ID="_8c11e9ef-cb53-51b8-9110-817746f020b7">
    <cim:IdentifiedObject.name>Unassigned equipment</cim:IdentifiedObject.name>
    <cim:IdentifiedObject.mRID>8c11e9ef-cb53-51b8-9110-817746f020b7</cim:IdentifiedObject.mRID>
    <cim:Line.Region rdf:resource="#_c6f369f6-260e-5723-bc04-5365252cd3d3"/>
  </cim:Line>
  <cim:Substation rdf:ID="_a15e7a22-11b7-56f5-bfe4-190610adf0b0">
    <cim:IdentifiedObject.name>Oakwood Primary</cim:IdentifiedObject.name>
    <cim:IdentifiedObject.mRID>a15e7a22-11b7-56f5-bfe4-190610adf0b0</cim:IdentifiedObject.mRID>
    <cim:Substation.Region rdf:resource="#_c6f369f6-260e-5723-bc04-5365252cd3d3"/>
    <cim:PowerSystemResource.Location rdf:resource="#_1ae1eb39-d087-5638-a12b-6592c1f2617f"/>
  </cim:Substation>
  <cim:Location rdf:ID="_1ae1eb39-d087-5638-a12b-6592c1f2617f">
    <cim:IdentifiedObject.mRID>1ae1eb39-d087-5638-a12b-6592c1f2617f</cim:IdentifiedObject.mRID>
    <cim:Location.CoordinateSystem rdf:resource="#_0382134e-be43-586a-984d-0584f775a4af"/>
  </cim:Location>
  <cim:PositionPoint rdf:ID="_8d77c862-d85e-58ef-a171-137049690835">
    <cim:PositionPoint.Location rdf:resource="#_1ae1eb39-d087-5638-a12b-6592c1f2617f"/>
    <cim:PositionPoint.sequenceNumber>1</cim:PositionPoint.sequenceNumber>
    <cim:PositionPoint.xPosition>-0.201</cim:PositionPoint.xPosition>
    <cim:PositionPoint.yPosition>5.5995</cim:PositionPoint.yPosition>
  </cim:PositionPoint>
  <cim:PositionPoint rdf:ID="_9c8559cf-0def-5f37-acf1-eaea052317f0">
    <cim:PositionPoint.Location rdf:resource="#_1ae1eb39-d087-5638-a12b-6592c1f2617f"/>
    <cim:PositionPoint.sequenceNumber>2</cim:PositionPoint.sequenceNumber>
    <cim:PositionPoint.xPosition>-0.198</cim:PositionPoint.xPosition>
    <cim:PositionPoint.yPosition>5.5995</cim:PositionPoint.yPosition>
  </cim:PositionPoint>
  <cim:PositionPoint rdf:ID="_1caceb10-a360-559b-a8a1-53abc01e5371">
    <cim:PositionPoint.Location rdf:resource="#_1ae1eb39-d087-5638-a12b-6592c1f2617f"/>
    <cim:PositionPoint.sequenceNumber>3</cim:PositionPoint.sequenceNumber>
    <cim:PositionPoint.xPosition>-0.198</cim:PositionPoint.xPosition>
    <cim:PositionPoint.yPosition>5.602</cim:PositionPoint.yPosition>
  </cim:PositionPoint>
  <cim:PositionPoint rdf:ID="_e0338373-4185-5615-ba9b-07abff051291">
    <cim:PositionPoint.Location rdf:resource="#_1ae1eb39-d087-5638-a12b-6592c1f2617f"/>
    <cim:PositionPoint.sequenceNumber>4</cim:PositionPoint.sequenceNumber>
    <cim:PositionPoint.xPosition>-0.201</cim:PositionPoint.xPosition>
    <cim:PositionPoint.yPosition>5.602</cim:PositionPoint.yPosition>
  </cim:PositionPoint>
  <cim:PositionPoint rdf:ID="_826464b2-650c-55a4-b13d-2b4206585617">
    <cim:PositionPoint.Location rdf:resource="#_1ae1eb39-d087-5638-a12b-6592c1f2617f"/>
    <cim:PositionPoint.sequenceNumber>5</cim:PositionPoint.sequenceNumber>
    <cim:PositionPoint.xPosition>-0.201</cim:PositionPoint.xPosition>
    <cim:PositionPoint.yPosition>5.5995</cim:PositionPoint.yPosition>
  </cim:PositionPoint>
  <cim:BaseVoltage rdf:ID="_a1a477c4-6c96-5796-9312-afc7ef5b20f4">
    <cim:IdentifiedObject.name>415 V</cim:IdentifiedObject.name>
    <cim:IdentifiedObject.mRID>a1a477c4-6c96-5796-9312-afc7ef5b20f4</cim:IdentifiedObject.mRID>
    <cim:BaseVoltage.nominalVoltage>415</cim:BaseVoltage.nominalVoltage>
  </cim:BaseVoltage>
  <cim:BaseVoltage rdf:ID="_6890e289-f3f8-5807-9e5b-f077ac483ef1">
    <cim:IdentifiedObject.name>11000 V</cim:IdentifiedObject.name>
    <cim:IdentifiedObject.mRID>6890e289-f3f8-5807-9e5b-f077ac483ef1</cim:IdentifiedObject.mRID>
    <cim:BaseVoltage.nominalVoltage>11000</cim:BaseVoltage.nominalVoltage>
  </cim:BaseVoltage>
  <cim:LoadBreakSwitch rdf:ID="_0663338e-a187-537b-ae65-fdac19774c6c">
    <cim:IdentifiedObject.name>SW-F7-03</cim:IdentifiedObject.name>
    <cim:IdentifiedObject.mRID>0663338e-a187-537b-ae65-fdac19774c6c</cim:IdentifiedObject.mRID>
    <cim:Equipment.EquipmentContainer rdf:resource="#_a15e7a22-11b7-56f5-bfe4-190610adf0b0"/>
    <cim:PowerSystemResource.Location rdf:resource="#_91058252-e603-534f-91e6-28c1165749ca"/>
    <cim:Switch.normalOpen>false</cim:Switch.normalOpen>
  </cim:LoadBreakSwitch>
  <cim:Location rdf:ID="_91058252-e603-534f-91e6-28c1165749ca">
    <cim:IdentifiedObject.mRID>91058252-e603-534f-91e6-28c1165749ca</cim:IdentifiedObject.mRID>
    <cim:Location.CoordinateSystem rdf:resource="#_0382134e-be43-586a-984d-0584f775a4af"/>
  </cim:Location>
  <cim:PositionPoint rdf:ID="_3b03f73e-4ae7-509c-8d15-a7106e5e6686">
    <cim:PositionPoint.Location rdf:resource="#_91058252-e603-534f-91e6-28c1165749ca"/>
    <cim:PositionPoint.sequenceNumber>1</cim:PositionPoint.sequenceNumber>
    <cim:PositionPoint.xPosition>-0.199</cim:PositionPoint.xPosition>
    <cim:PositionPoint.yPosition>5.6006</cim:PositionPoint.yPosition>
  </cim:PositionPoint>
  <cim:ACLineSegment rdf:ID="_9341c867-aae5-52b9-859e-147a13a6b332">
    <cim:IdentifiedObject.name>C-F7-01</cim:IdentifiedObject.name>
    <cim:IdentifiedObject.mRID>9341c867-aae5-52b9-859e-147a13a6b332</cim:IdentifiedObject.mRID>
    <cim:Equipment.EquipmentContainer rdf:resource="#_a15e7a22-11b7-56f5-bfe4-190610adf0b0"/>
    <cim:ConductingEquipment.BaseVoltage rdf:resource="#_6890e289-f3f8-5807-9e5b-f077ac483ef1"/>
    <cim:PowerSystemResource.Location rdf:resource="#_ae2d0879-baa8-515f-bf49-8c037b229bf5"/>
    <cim:Conductor.length>284.603</cim:Conductor.length>
  </cim:ACLineSegment>
  <cim:Location rdf:ID="_ae2d0879-baa8-515f-bf49-8c037b229bf5">
    <cim:IdentifiedObject.mRID>ae2d0879-baa8-515f-bf49-8c037b229bf5</cim:IdentifiedObject.mRID>
    <cim:Location.CoordinateSystem rdf:resource="#_0382134e-be43-586a-984d-0584f775a4af"/>
  </cim:Location>
  <cim:PositionPoint rdf:ID="_7b0788d9-dad4-5a58-8c80-7f2de0c37650">
    <cim:PositionPoint.Location rdf:resource="#_ae2d0879-baa8-515f-bf49-8c037b229bf5"/>
    <cim:PositionPoint.sequenceNumber>1</cim:PositionPoint.sequenceNumber>
    <cim:PositionPoint.xPosition>-0.199</cim:PositionPoint.xPosition>
    <cim:PositionPoint.yPosition>5.6006</cim:PositionPoint.yPosition>
  </cim:PositionPoint>
  <cim:PositionPoint rdf:ID="_81307da1-f34a-5a95-bf83-8b02dc16d137">
    <cim:PositionPoint.Location rdf:resource="#_ae2d0879-baa8-515f-bf49-8c037b229bf5"/>
    <cim:PositionPoint.sequenceNumber>2</cim:PositionPoint.sequenceNumber>
    <cim:PositionPoint.xPosition>-0.1978</cim:PositionPoint.xPosition>
    <cim:PositionPoint.yPosition>5.6009</cim:PositionPoint.yPosition>
  </cim:PositionPoint>
  <cim:PositionPoint rdf:ID="_988aee05-55df-54cc-82cc-7b468cb18cf9">
    <cim:PositionPoint.Location rdf:resource="#_ae2d0879-baa8-515f-bf49-8c037b229bf5"/>
    <cim:PositionPoint.sequenceNumber>3</cim:PositionPoint.sequenceNumber>
    <cim:PositionPoint.xPosition>-0.1965</cim:PositionPoint.xPosition>
    <cim:PositionPoint.yPosition>5.6012</cim:PositionPoint.yPosition>
  </cim:PositionPoint>
  <cim:ACLineSegmentPhase rdf:ID="_8f3310a5-39f2-55ab-b811-e6ab5f060fcc">
    <cim:IdentifiedObject.mRID>8f3310a5-39f2-55ab-b811-e6ab5f060fcc</cim:IdentifiedObject.mRID>
    <cim:ACLineSegmentPhase.phase rdf:resource="http://iec.ch/TC57/2013/CIM-schema-cim16#SinglePhaseKind.A"/>
    <cim:ACLineSegmentPhase.ACLineSegment rdf:resource="#_9341c867-aae5-52b9-859e-147a13a6b332"/>
  </cim:ACLineSegmentPhase>
  <cim:ACLineSegmentPhase rdf:ID="_47c4a6ef-68f0-5f98-91fd-bf86e879b6d6">
    <cim:IdentifiedObject.mRID>47c4a6ef-68f0-5f98-91fd-bf86e879b6d6</cim:IdentifiedObject.mRID>
    <cim:ACLineSegmentPhase.phase rdf:resource="http://iec.ch/TC57/2013/CIM-schema-cim16#SinglePhaseKind.B"/>
    <cim:ACLineSegmentPhase.ACLineSegment rdf:resource="#_9341c867-aae5-52b9-859e-147a13a6b332"/>
  </cim:ACLineSegmentPhase>
  <cim:ACLineSegmentPhase rdf:ID="_4d7614ae-5a5c-5ddd-825c-103fc28c9c09">
    <cim:IdentifiedObject.mRID>4d7614ae-5a5c-5ddd-825c-103fc28c9c09</cim:IdentifiedObject.mRID>
    <cim:ACLineSegmentPhase.phase rdf:resource="http://iec.ch/TC57/2013/CIM-schema-cim16#SinglePhaseKind.C"/>
    <cim:ACLineSegmentPhase.ACLineSegment rdf:resource="#_9341c867-aae5-52b9-859e-147a13a6b332"/>
  </cim:ACLineSegmentPhase>
  <cim:ACLineSegmentPhase rdf:ID="_5f14fff2-1b50-5d68-9fce-28e96db2fb94">
    <cim:IdentifiedObject.mRID>5f14fff2-1b50-5d68-9fce-28e96db2fb94</cim:IdentifiedObject.mRID>
    <cim:ACLineSegmentPhase.phase rdf:resource="http://iec.ch/TC57/2013/CIM-schema-cim16#SinglePhaseKind.N"/>
    <cim:ACLineSegmentPhase.ACLineSegment rdf:resource="#_9341c867-aae5-52b9-859e-147a13a6b332"/>
  </cim:ACLineSegmentPhase>
  <cim:PowerTransformer rdf:ID="_93185a15-0963-58e3-b744-28d63c30d899">
    <cim:IdentifiedObject.name>TX-Feeder7-014</cim:IdentifiedObject.name>
    <cim:IdentifiedObject.mRID>93185a15-0963-58e3-b744-28d63c30d899</cim:IdentifiedObject.mRID>
    <cim:Equipment.EquipmentContainer rdf:resource="#_8c11e9ef-cb53-51b8-9110-817746f020b7"/>
    <cim:PowerSystemResource.Location rdf:resource="#_3ddaa7fa-3fd1-500e-a143-50d6387447b3"/>
  </cim:PowerTransformer>
  <cim:Location rdf:ID="_3ddaa7fa-3fd1-500e-a143-50d6387447b3">
    <cim:IdentifiedObject.mRID>3ddaa7fa-3fd1-500e-a143-50d6387447b3</cim:IdentifiedObject.mRID>
    <cim:Location.CoordinateSystem rdf:resource="#_0382134e-be43-586a-984d-0584f775a4af"/>
  </cim:Location>
  <cim:PositionPoint rdf:ID="_5955d075-7759-51a9-81ba-6dc58f8078c9">
    <cim:PositionPoint.Location rdf:resource="#_3ddaa7fa-3fd1-500e-a143-50d6387447b3"/>
    <cim:PositionPoint.sequenceNumber>1</cim:PositionPoint.sequenceNumber>
    <cim:PositionPoint.xPosition>-0.1965</cim:PositionPoint.xPosition>
    <cim:PositionPoint.yPosition>5.6012</cim:PositionPoint.yPosition>
  </cim:PositionPoint>
  <cim:ACLineSegment rdf:ID="_d35d2d90-dbe6-5181-98d8-16345cf64746">
    <cim:IdentifiedObject.name>LV-014-01</cim:IdentifiedObject.name>
    <cim:IdentifiedObject.mRID>d35d2d90-dbe6-5181-98d8-16345cf64746</cim:IdentifiedObject.mRID>
    <cim:Equipment.EquipmentContainer rdf:resource="#_8c11e9ef-cb53-51b8-9110-817746f020b7"/>
    <cim:PowerSystemResource.Location rdf:resource="#_ab75d365-3710-5682-bb79-57c98166d176"/>
    <cim:Conductor.length>62.751</cim:Conductor.length>
    <cim:ACLineSegment.r>0.02</cim:ACLineSegment.r>
    <cim:ACLineSegment.x>0.005</cim:ACLineSegment.x>
  </cim:ACLineSegment>
  <cim:Location rdf:ID="_ab75d365-3710-5682-bb79-57c98166d176">
    <cim:IdentifiedObject.mRID>ab75d365-3710-5682-bb79-57c98166d176</cim:IdentifiedObject.mRID>
    <cim:Location.CoordinateSystem rdf:resource="#_0382134e-be43-586a-984d-0584f775a4af"/>
  </cim:Location>
  <cim:PositionPoint rdf:ID="_8ab6d68d-fabd-5e85-abd5-8c4805098a1c">
    <cim:PositionPoint.Location rdf:resource="#_ab75d365-3710-5682-bb79-57c98166d176"/>
    <cim:PositionPoint.sequenceNumber>1</cim:PositionPoint.sequenceNumber>
    <cim:PositionPoint.xPosition>-0.1965</cim:PositionPoint.xPosition>
    <cim:PositionPoint.yPosition>5.6012</cim:PositionPoint.yPosition>
  </cim:PositionPoint>
  <cim:PositionPoint rdf:ID="_b413fac4-d127-5784-b7c8-e9e09ce4d082">
    <cim:PositionPoint.Location rdf:resource="#_ab75d365-3710-5682-bb79-57c98166d176"/>
    <cim:PositionPoint.sequenceNumber>2</cim:PositionPoint.sequenceNumber>
    <cim:PositionPoint.xPosition>-0.1961</cim:PositionPoint.xPosition>
    <cim:PositionPoint.yPosition>5.6016</cim:PositionPoint.yPosition>
  </cim:PositionPoint>
  <cim:ACLineSegmentPhase rdf:ID="_5b25a939-6eb9-562b-a2ca-81e7e0f49218">
    <cim:IdentifiedObject.mRID>5b25a939-6eb9-562b-a2ca-81e7e0f49218</cim:IdentifiedObject.mRID>
    <cim:ACLineSegmentPhase.phase rdf:resource="http://iec.ch/TC57/2013/CIM-schema-cim16#SinglePhaseKind.A"/>
    <cim:ACLineSegmentPhase.ACLineSegment rdf:resource="#_d35d2d90-dbe6-5181-98d8-16345cf64746"/>
  </cim:ACLineSegmentPhase>
  <cim:ACLineSegmentPhase rdf:ID="_aaf64737-e4e1-58bf-ad9d-5cef706fcfd2">
    <cim:IdentifiedObject.mRID>aaf64737-e4e1-58bf-ad9d-5cef706fcfd2</cim:IdentifiedObject.mRID>
    <cim:ACLineSegmentPhase.phase rdf:resource="http://iec.ch/TC57/2013/CIM-schema-cim16#SinglePhaseKind.B"/>
    <cim:ACLineSegmentPhase.ACLineSegment rdf:resource="#_d35d2d90-dbe6-5181-98d8-16345cf64746"/>
  </cim:ACLineSegmentPhase>
  <cim:ACLineSegmentPhase rdf:ID="_ca341201-31de-57a8-9900-a933894c1089">
    <cim:IdentifiedObject.mRID>ca341201-31de-57a8-9900-a933894c1089</cim:IdentifiedObject.mRID>
    <cim:ACLineSegmentPhase.phase rdf:resource="http://iec.ch/TC57/2013/CIM-schema-cim16#SinglePhaseKind.C"/>
    <cim:ACLineSegmentPhase.ACLineSegment rdf:resource="#_d35d2d90-dbe6-5181-98d8-16345cf64746"/>
  </cim:ACLineSegmentPhase>
  <cim:ACLineSegmentPhase rdf:ID="_191f0d9d-5d6c-52e7-ba0b-77c1b88936a9">
    <cim:IdentifiedObject.mRID>191f0d9d-5d6c-52e7-ba0b-77c1b88936a9</cim:IdentifiedObject.mRID>
    <cim:ACLineSegmentPhase.phase rdf:resource="http://iec.ch/TC57/2013/CIM-schema-cim16#SinglePhaseKind.N"/>
    <cim:ACLineSegmentPhase.ACLineSegment rdf:resource="#_d35d2d90-dbe6-5181-98d8-16345cf64746"/>
  </cim:ACLineSegmentPhase>
  <cim:EnergyConsumer rdf:ID="_c7e7ab3a-d814-5614-8258-8c197ff29391">
    <cim:IdentifiedObject.name>SP-0457</cim:IdentifiedObject.name>
    <cim:IdentifiedObject.mRID>c7e7ab3a-d814-5614-8258-8c197ff29391</cim:IdentifiedObject.mRID>
    <cim:Equipment.EquipmentContainer rdf:resource="#_8c11e9ef-cb53-51b8-9110-817746f020b7"/>
    <cim:PowerSystemResource.Location rdf:resource="#_12aa17c1-c167-547e-a114-31d24dfa96c3"/>
  </cim:EnergyConsumer>
  <cim:Location rdf:ID="_12aa17c1-c167-547e-a114-31d24dfa96c3">
    <cim:IdentifiedObject.mRID>12aa17c1-c167-547e-a114-31d24dfa96c3</cim:IdentifiedObject.mRID>
    <cim:Location.CoordinateSystem rdf:resource="#_0382134e-be43-586a-984d-0584f775a4af"/>
  </cim:Location>
  <cim:PositionPoint rdf:ID="_a79364a4-1531-5204-b216-59ec0180da99">
    <cim:PositionPoint.Location rdf:resource="#_12aa17c1-c167-547e-a114-31d24dfa96c3"/>
    <cim:PositionPoint.sequenceNumber>1</cim:PositionPoint.sequenceNumber>
    <cim:PositionPoint.xPosition>-0.1961</cim:PositionPoint.xPosition>
    <cim:PositionPoint.yPosition>5.6016</cim:PositionPoint.yPosition>
  </cim:PositionPoint>
  <cim:UsagePoint rdf:ID="_234144a6-ae38-5002-8dff-fa06b73c4d6a">
    <cim:IdentifiedObject.name>M-0457</cim:IdentifiedObject.name>
    <cim:IdentifiedObject.mRID>234144a6-ae38-5002-8dff-fa06b73c4d6a</cim:IdentifiedObject.mRID>
    <cim:PowerSystemResource.Location rdf:resource="#_e0ac9d32-36e8-5128-b124-6b904f69d660"/>
  </cim:UsagePoint>
  <cim:Location rdf:ID="_e0ac9d32-36e8-5128-b124-6b904f69d660">
    <cim:IdentifiedObject.mRID>e0ac9d32-36e8-5128-b124-6b904f69d660</cim:IdentifiedObject.mRID>
    <cim:Location.CoordinateSystem rdf:resource="#_0382134e-be43-586a-984d-0584f775a4af"/>
  </cim:Location>
  <cim:PositionPoint rdf:ID="_9a3813da-a5da-5fa5-a220-59f2fc6bbd42">
    <cim:PositionPoint.Location rdf:resource="#_e0ac9d32-36e8-5128-b124-6b904f69d660"/>
    <cim:PositionPoint.sequenceNumber>1</cim:PositionPoint.sequenceNumber>
    <cim:PositionPoint.xPosition>-0.1961</cim:PositionPoint.xPosition>
    <cim:PositionPoint.yPosition>5.6016</cim:PositionPoint.yPosition>
  </cim:PositionPoint>
  <cim:ConnectivityNode rdf:ID="_dd9b94ac-9a46-589f-8455-d98cba48c075">
    <cim:IdentifiedObject.mRID>dd9b94ac-9a46-589f-8455-d98cba48c075</cim:IdentifiedObject.mRID>
    <cim:ConnectivityNode.ConnectivityNodeContainer rdf:resource="#_a15e7a22-11b7-56f5-bfe4-190610adf0b0"/>
  </cim:ConnectivityNode>
  <cim:Terminal rdf:ID="_dac4795c-1ab9-5e35-8581-5846fc3979ec">
    <cim:IdentifiedObject.mRID>dac4795c-1ab9-5e35-8581-5846fc3979ec</cim:IdentifiedObject.mRID>
    <cim:Terminal.ConductingEquipment rdf:resource="#_9341c867-aae5-52b9-859e-147a13a6b332"/>
    <cim:Terminal.ConnectivityNode rdf:resource="#_dd9b94ac-9a46-589f-8455-d98cba48c075"/>
    <cim:Terminal.sequenceNumber>1</cim:Terminal.sequenceNumber>
  </cim:Terminal>
  <cim:Terminal rdf:ID="_59755608-ec0b-55aa-b6fb-f7dcf5297548">
    <cim:IdentifiedObject.mRID>59755608-ec0b-55aa-b6fb-f7dcf5297548</cim:IdentifiedObject.mRID>
    <cim:Terminal.ConductingEquipment rdf:resource="#_0663338e-a187-537b-ae65-fdac19774c6c"/>
    <cim:Terminal.ConnectivityNode rdf:resource="#_dd9b94ac-9a46-589f-8455-d98cba48c075"/>
    <cim:Terminal.sequenceNumber>2</cim:Terminal.sequenceNumber>
  </cim:Terminal>
  <cim:ConnectivityNode rdf:ID="_67fbca05-8a2c-51f9-9ad3-cb1539a073f5">
    <cim:IdentifiedObject.mRID>67fbca05-8a2c-51f9-9ad3-cb1539a073f5</cim:IdentifiedObject.mRID>
    <cim:ConnectivityNode.ConnectivityNodeContainer rdf:resource="#_8c11e9ef-cb53-51b8-9110-817746f020b7"/>
  </cim:ConnectivityNode>
  <cim:Terminal rdf:ID="_3cdb9414-acfc-5e00-b6fb-b9cb756f9bfa">
    <cim:IdentifiedObject.mRID>3cdb9414-acfc-5e00-b6fb-b9cb756f9bfa</cim:IdentifiedObject.mRID>
    <cim:Terminal.ConductingEquipment rdf:resource="#_d35d2d90-dbe6-5181-98d8-16345cf64746"/>
    <cim:Terminal.ConnectivityNode rdf:resource="#_67fbca05-8a2c-51f9-9ad3-cb1539a073f5"/>
    <cim:Terminal.sequenceNumber>1</cim:Terminal.sequenceNumber>
  </cim:Terminal>
  <cim:Terminal rdf:ID="_d6245f27-98f3-5f2c-9cee-3da1028d98f3">
    <cim:IdentifiedObject.mRID>d6245f27-98f3-5f2c-9cee-3da1028d98f3</cim:IdentifiedObject.mRID>
    <cim:Terminal.ConductingEquipment rdf:resource="#_93185a15-0963-58e3-b744-28d63c30d899"/>
    <cim:Terminal.ConnectivityNode rdf:resource="#_67fbca05-8a2c-51f9-9ad3-cb1539a073f5"/>
    <cim:Terminal.sequenceNumber>2</cim:Terminal.sequenceNumber>
  </cim:Terminal>
  <cim:PowerTransformerEnd rdf:ID="_2c7b3b77-c025-5028-86f1-6eebfca5f814">
    <cim:IdentifiedObject.mRID>2c7b3b77-c025-5028-86f1-6eebfca5f814</cim:IdentifiedObject.mRID>
    <cim:PowerTransformerEnd.PowerTransformer rdf:resource="#_93185a15-0963-58e3-b744-28d63c30d899"/>
    <cim:TransformerEnd.endNumber>2</cim:TransformerEnd.endNumber>
    <cim:TransformerEnd.Terminal rdf:resource="#_d6245f27-98f3-5f2c-9cee-3da1028d98f3"/>
    <cim:PowerTransformerEnd.ratedS>315000</cim:PowerTransformerEnd.ratedS>
    <cim:PowerTransformerEnd.ratedU>415</cim:PowerTransformerEnd.ratedU>
    <cim:TransformerEnd.BaseVoltage rdf:resource="#_a1a477c4-6c96-5796-9312-afc7ef5b20f4"/>
  </cim:PowerTransformerEnd>
  <cim:ConnectivityNode rdf:ID="_ae159ac1-a13e-5128-8fb7-3afaec477d00">
    <cim:IdentifiedObject.mRID>ae159ac1-a13e-5128-8fb7-3afaec477d00</cim:IdentifiedObject.mRID>
    <cim:ConnectivityNode.ConnectivityNodeContainer rdf:resource="#_a15e7a22-11b7-56f5-bfe4-190610adf0b0"/>
  </cim:ConnectivityNode>
  <cim:Terminal rdf:ID="_07239a84-3278-5cf6-a550-932dd90b677c">
    <cim:IdentifiedObject.mRID>07239a84-3278-5cf6-a550-932dd90b677c</cim:IdentifiedObject.mRID>
    <cim:Terminal.ConductingEquipment rdf:resource="#_0663338e-a187-537b-ae65-fdac19774c6c"/>
    <cim:Terminal.ConnectivityNode rdf:resource="#_ae159ac1-a13e-5128-8fb7-3afaec477d00"/>
    <cim:Terminal.sequenceNumber>1</cim:Terminal.sequenceNumber>
  </cim:Terminal>
  <cim:ConnectivityNode rdf:ID="_45a2ecbf-ff58-572e-9983-1002455369e2">
    <cim:IdentifiedObject.mRID>45a2ecbf-ff58-572e-9983-1002455369e2</cim:IdentifiedObject.mRID>
    <cim:ConnectivityNode.ConnectivityNodeContainer rdf:resource="#_a15e7a22-11b7-56f5-bfe4-190610adf0b0"/>
  </cim:ConnectivityNode>
  <cim:Terminal rdf:ID="_735779b1-5cb0-5c90-9f9d-fcf1df3e5f45">
    <cim:IdentifiedObject.mRID>735779b1-5cb0-5c90-9f9d-fcf1df3e5f45</cim:IdentifiedObject.mRID>
    <cim:Terminal.ConductingEquipment rdf:resource="#_9341c867-aae5-52b9-859e-147a13a6b332"/>
    <cim:Terminal.ConnectivityNode rdf:resource="#_45a2ecbf-ff58-572e-9983-1002455369e2"/>
    <cim:Terminal.sequenceNumber>2</cim:Terminal.sequenceNumber>
  </cim:Terminal>
  <cim:Terminal rdf:ID="_341b525b-a44b-5bf2-bf40-20b37a888c95">
    <cim:IdentifiedObject.mRID>341b525b-a44b-5bf2-bf40-20b37a888c95</cim:IdentifiedObject.mRID>
    <cim:Terminal.ConductingEquipment rdf:resource="#_93185a15-0963-58e3-b744-28d63c30d899"/>
    <cim:Terminal.ConnectivityNode rdf:resource="#_45a2ecbf-ff58-572e-9983-1002455369e2"/>
    <cim:Terminal.sequenceNumber>1</cim:Terminal.sequenceNumber>
  </cim:Terminal>
  <cim:PowerTransformerEnd rdf:ID="_bfd7c027-a32c-5888-839c-af04a6f32d79">
    <cim:IdentifiedObject.mRID>bfd7c027-a32c-5888-839c-af04a6f32d79</cim:IdentifiedObject.mRID>
    <cim:PowerTransformerEnd.PowerTransformer rdf:resource="#_93185a15-0963-58e3-b744-28d63c30d899"/>
    <cim:TransformerEnd.endNumber>1</cim:TransformerEnd.endNumber>
    <cim:TransformerEnd.Terminal rdf:resource="#_341b525b-a44b-5bf2-bf40-20b37a888c95"/>
    <cim:PowerTransformerEnd.ratedS>315000</cim:PowerTransformerEnd.ratedS>
    <cim:PowerTransformerEnd.ratedU>11000</cim:PowerTransformerEnd.ratedU>
    <cim:TransformerEnd.BaseVoltage rdf:resource="#_6890e289-f3f8-5807-9e5b-f077ac483ef1"/>
  </cim:PowerTransformerEnd>
  <cim:ConnectivityNode rdf:ID="_79d6306e-65fa-54a3-86a9-fb96cc997a35">
    <cim:IdentifiedObject.mRID>79d6306e-65fa-54a3-86a9-fb96cc997a35</cim:IdentifiedObject.mRID>
    <cim:ConnectivityNode.ConnectivityNodeContainer rdf:resource="#_8c11e9ef-cb53-51b8-9110-817746f020b7"/>
  </cim:ConnectivityNode>
  <cim:Terminal rdf:ID="_d5022325-9aad-578e-b09a-f4dda91fc9e4">
    <cim:IdentifiedObject.mRID>d5022325-9aad-578e-b09a-f4dda91fc9e4</cim:IdentifiedObject.mRID>
    <cim:Terminal.ConductingEquipment rdf:resource="#_d35d2d90-dbe6-5181-98d8-16345cf64746"/>
    <cim:Terminal.ConnectivityNode rdf:resource="#_79d6306e-65fa-54a3-86a9-fb96cc997a35"/>
    <cim:Terminal.sequenceNumber>2</cim:Terminal.sequenceNumber>
  </cim:Terminal>
  <cim:Terminal rdf:ID="_04353a97-0aba-521e-8eca-277ed969a714">
    <cim:IdentifiedObject.mRID>04353a97-0aba-521e-8eca-277ed969a714</cim:IdentifiedObject.mRID>
    <cim:Terminal.ConductingEquipment rdf:resource="#_c7e7ab3a-d814-5614-8258-8c197ff29391"/>
    <cim:Terminal.ConnectivityNode rdf:resource="#_79d6306e-65fa-54a3-86a9-fb96cc997a35"/>
    <cim:Terminal.sequenceNumber>1</cim:Terminal.sequenceNumber>
  </cim:Terminal>
</rdf:RDF>
```

*End of the VoltEdge CIM Export Guide.*

