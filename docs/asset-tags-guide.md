# The VoltEdge Asset Tags Guide

*How to tag, print, and scan the plant in your network — and how the tagging
system works underneath.*

---

## How to read this guide

This guide answers two questions that usually belong to two different people, so —
like the [Substation Guide](substation-guide.md) — it braids them into one text,
chapter by chapter:

- **The ▶ operator lane** — the step-by-step, "open this tab, press that button,
  hold your phone here" path for actually using asset tags: registering a legacy
  number, printing a label, scanning it in the field, managing tags at scale from
  the admin console. If you only need to *use* tags, read just these parts.
- **The ∑ model lane** — the "why it is shaped this way" reference: the code
  scheme and its check character, the database trigger that mints a tag for every
  feature, what "resolve" really does, and the visibility rules a scan obeys. If
  you administer VoltEdge, integrate with it, or need to justify a design choice,
  these are for you.

Skip the **∑** parts freely; the **▶** parts stand alone. This guide is the
how-to companion to the internals note [`docs/asset-tagging-module.md`](asset-tagging-module.md);
where this guide says "and the trigger tidies it up," that note has the SQL.

This guide assumes you have met the core VoltEdge concepts — feature classes,
the map, the feature panel, permissions, and territory — in the
[User Handbook](user-handbook.md). Asset tags also get a shorter treatment in
Handbook Chapter 13; this is the full manual.

### The cast

The recurring VoltEdge characters carry through, each holding a real set of
permissions the examples respect exactly:

| Who | Role | Shows up when… |
| --- | --- | --- |
| **Femi** | Field technician (`tags:read`, `wam:write`) | Scanning labels on site, offline — the main character of Chapters 5–6 |
| **Dana** | Network editor / maintenance planner (`tags:write`) | Registering legacy numbers, setting primaries, reissuing damaged labels |
| **Priya** | Read-only viewer / dispatcher (`tags:read`) | Scanning to *identify* an asset; the reminder of where the write boundary sits |
| **Ada** | System administrator (`admin`) | Running the tag estate from the Admin → Asset Tags console |
| **Tom** | Network analyst | Occasionally, finding features *by* their tag in the query engine |

### The running example

One asset threads through the whole guide: **TX-Feeder7-014**, a 315 kVA
11 kV/415 V distribution transformer on **Feeder 7** out of **Oakwood Zone** — the
same transformer the [Engineering Studies](engineering-studies.md) guide analyses.
It sits in the `DIST_XFMR` feature class, it was surveyed years ago under the
legacy asset number **OLD-4471**, and by the end of this guide it will carry a
printed VoltEdge label that Femi can scan to open it on his phone with no signal.

> **Legend used throughout:** 🔒 marks an action that needs a specific permission
> (named inline). 🗺️ marks something that also depends on your **territory
> access**. 💡 is a tip. ⚠️ is a caution.

### Chapter roadmap

1. **[The asset-tag mental model](#chapter-1--the-asset-tag-mental-model)** ← *this chapter* — what a tag is, what it is not, one-feature-many-codes-one-primary, and the three lifecycle states.
2. **Anatomy of a tag & how minting works** — the code scheme and check character, the registry trigger that mints a tag for every feature, the backfill.
3. **Working with a feature's tags** — the Tags tab: register a legacy code, set primary, void, reissue.
4. **Printing labels** — the millimetre-true label, the print sheet, bulk printing, and the print count.
5. **Scanning & resolving** — the QR deep link, the scope- and draft-aware resolve, and what a scan actually lands you on.
6. **In the field, offline** — the field pack's tag index, offline-first resolution, and the online fallback.
7. **The Admin → Asset Tags console** — browsing, searching, the KPI strip, inline lifecycle, register, and bulk print at estate scale.
8. **Finding features by tag, and the agent** — the query-engine tag filter and the nine MCP tools; permissions and territory in full.
9. **Appendix** — status/source reference, the lifecycle diagram, the permission matrix, and a one-page cheat sheet.

---

## Chapter 1 — The asset-tag mental model

This chapter builds the vocabulary the rest of the guide assumes. Read it once. If
you take only three ideas away, take these: an asset tag is an **identity label
over a feature that already exists**, not a new record about it; a feature can
carry **many** codes but exactly **one is primary** (the one that gets printed);
and a code has a **lifecycle** — it can be active, retired, or replaced — that
mirrors the messy reality of labels in the field.

### 1.1 What an asset tag is ▶

An **asset tag** is a short, scannable code — something like `VE-7Q3K2` — minted
for a feature in your network, printed on a small label as a **QR code, a Code128
barcode, and human-readable text**, and physically stuck on the plant: the
transformer tank, the pole, the switch cubicle, the pillar door.

Its entire job is to close the loop between the **thing in the field** and the
**record in VoltEdge**. A field worker standing in front of TX-Feeder7-014 with a
phone points the camera at the label, and VoltEdge opens that exact transformer —
ready to read its details, attach an inspection, book a defect, or raise a fault.
No hunting on a map, no keying an ID, no ambiguity about *which* of three
transformers on the pole you meant.

That is the whole promise, and it is worth stating what makes it trustworthy:
**every feature has one**. You never have to wonder whether an asset was tagged —
the system mints a tag for a feature the instant that feature exists, by any path
it could have been created (Chapter 2). Tagging is not a project you run; it is a
property of the data.

### 1.2 What an asset tag is *not* ∑

The distinction that keeps the model clean is this: an asset tag is **not a GIS
feature** and **not an operational record**. VoltEdge has three families of thing,
and a tag is none of them — it is a thin fourth layer that *points at* the first:

- A **feature** is a row in the network model — TX-Feeder7-014 is a `DIST_XFMR`
  with a location, terminals, a rated kVA, upstream and downstream connections. It
  shows up in traces and load flow.
- An **operational record** — a *defect* on that transformer, an *outage* that
  de-energised it, a *fault* event — is a thing that *happened to* the asset.
  These earn their own database schemas (`wam`, `faults`, and OMS) because each is
  new information *about* the asset.
- An **asset tag** is the opposite of an operational record. It carries no new
  information about the transformer — no condition, no event, no measurement. It
  is purely an **index entry**: "this code means that feature." It is *keyed by*
  the asset rather than being a record *about* it.

That single design decision — tags are an identity layer, not a record — is why
they don't get their own schema and why a tag has no independent life. A tag with
no feature behind it is meaningless, so when a feature is truly purged, its tags
are cleaned up automatically (Chapter 2). A defect survives its asset for the
audit trail; a tag does not, because a code that resolves to nothing is just a
dead end.

💡 The mental shortcut: **features and records are nouns with a story; a tag is
just a name you can scan.**

### 1.3 One feature, many codes, one primary ▶

Here is the idea that surprises people first: a single feature can carry **several
tags at once**. That is not a bug — it is the model bending to fit reality.

Real plant already has numbers on it. TX-Feeder7-014 was surveyed years ago and
someone painted **OLD-4471** on the tank. When VoltEdge was rolled out it minted
its own code, **VE-7Q3K2**, for that transformer. Both are legitimate ways to name
the same asset, and a field worker might read either one. So the transformer
carries both:

```
   TX-Feeder7-014  (a DIST_XFMR)
     ├── VE-7Q3K2   generated,  PRIMARY     ← the VoltEdge-minted code, printed on the new label
     └── OLD-4471   external                ← the legacy number, registered so it still resolves
```

Every code on a feature falls into one of two **sources**:

- **`generated`** — minted by VoltEdge itself. Every feature gets exactly one of
  these at birth, and its code always begins `VE-`.
- **`external`** — a pre-existing number (a legacy asset ID, a manufacturer serial,
  a code from the system VoltEdge replaced) that someone **registered** against the
  feature so that scanning or searching it lands on the right record. VoltEdge does
  not invent these — a person types them in (Chapter 3).

Among all the codes on a feature, **exactly one is marked primary**. The primary is
"the code that gets printed" — the one that goes on the official VoltEdge label
affixed to the plant. A feature has **at most one** primary at any moment (the
database enforces this literally — §2.4), and by default it is the generated code,
because that is the one VoltEdge controls and can reissue if the label is damaged.

⚠️ You can make a registered external code the primary if your organisation would
rather print the legacy number people already know — but there is still only ever
**one** primary. Promoting a new primary automatically demotes the old one; the two
never coexist.

### 1.4 The three lifecycle states ∑

The second surprising idea: a code does not simply exist or not-exist. Labels live
in the physical world, where you cannot un-print a sticker or guarantee that the
damaged one was peeled off. So a code has a **status** that models what is true
*in the field*, and there are exactly three:

| status | Resolves on scan? | What it means |
| --- | --- | --- |
| **`active`** | ✅ yes | The code is live and correct. The normal state. |
| **`superseded`** | ✅ **yes** | The code was **replaced** by a reissue, but it *still resolves* — because an old label may still be stuck on the asset in the field, and a scan of it should still find the right feature. |
| **`void`** | ❌ **no** | The code was **retired as wrong** and deliberately does **not** resolve — scanning it returns "not found," because it should never have pointed anywhere. |

The difference between `superseded` and `void` is the crux, and it is entirely
about **what you want a scan of the old sticker to do**:

- Dana reissues TX-Feeder7-014's label because the printed QR was scratched
  illegible. The system mints a **fresh** code and marks the old one
  **`superseded`**. If a colleague later scans a surviving old label, it *still*
  opens the transformer — the old number was never *wrong*, just replaced. (Chapter
  3 covers reissue.)
- By contrast, if a code was minted against the *wrong* feature, or an external
  number was keyed in against the wrong asset, you **void** it. A voided code is a
  dead end forever — and critically, **a voided code is never reused**; the code
  space only ever moves forward, so an old label that resurfaces can never
  silently resolve to some unrelated new asset.

💡 One-line summary of the trio: **active = use it; superseded = old-but-still-valid;
void = wrong, dead, and never recycled.**

There is one guard rail that ties status back to §1.3: **you cannot void the
primary code.** The primary is by definition the one that is printed and in service,
so voiding it would leave the asset with no printable identity. If you need to
retire the primary, you *reissue* it (which supersedes it and mints a replacement
that becomes the new primary) — you never simply void it. Chapter 3 walks this.

### 1.5 Where tags live, and who can touch them ∑

Tags occupy a deliberately small footprint:

- **Storage.** Every code is one row in a single table, `runtime.asset_tags` — the
  `runtime` schema, alongside the cross-class feature index it depends on, **not** a
  schema of its own (Chapter 2 explains why that is the right call). A row records
  the code, which feature it points at (by class and id), its source, whether it is
  primary, its status, and how many times its label has been printed.
- **The API surface.** A small set of endpoints mounted at **`/tags`** handles
  resolving, listing, registering, and the lifecycle actions. There is deliberately
  **no** "create a generated tag" endpoint — minting is automatic and belongs to
  the database, not to a caller (Chapter 2).
- **Two permissions gate everything:**
  - **`tags:read`** 🔒 — the viewer / field-technician tier. It lets you **scan and
    resolve** a code, list a feature's tags, and print a label. Femi and Priya hold
    it.
  - **`tags:write`** 🔒 — the editor tier. It lets you **register** a legacy code,
    **set** the primary, **void**, **reissue**, and record a print. Dana holds it;
    Ada holds it through the admin role.

🗺️ Crucially, reading a tag is **territory- and visibility-aware**. Holding
`tags:read` does not let you resolve *any* code in the system — only codes on
features **you can already see**. If TX-Feeder7-014 is outside your assigned
territory, scanning its label returns "not found," exactly as if the transformer
did not exist for you. This is not a special rule bolted onto tags; a resolve runs
through the very same read-scope and private-draft checks that govern every other
way you might reach a feature (Chapter 5 shows how, and why "not found" — rather
than "forbidden" — is the deliberate answer).

### 1.6 The shape of the rest of this guide

With the model in hand, the guide follows a tag through its life:

- **Chapter 2** is the one deep ∑ chapter — how a code is built and how the system
  guarantees every feature gets one. Read it if you want to trust the machinery;
  skip it if you just want to use tags.
- **Chapters 3–4** are Dana's: managing a feature's tags and printing labels.
- **Chapters 5–6** are Femi's: scanning and resolving, online and offline in the
  field.
- **Chapter 7** is Ada's: running the whole tag estate from the admin console.
- **Chapter 8** covers finding features *by* tag and doing all of this through the
  API and the assistant, with the full permission picture.
- **Chapter 9** is the reference card.

Next: **Chapter 2 — Anatomy of a tag & how minting works**, where we open up
`VE-7Q3K2` and see why it is shaped the way it is.
