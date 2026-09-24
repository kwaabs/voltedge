# VoltEdge User Handbook

*A practical, task-oriented guide to using VoltEdge — the electrical-network GIS — from every user's perspective.*

---

## About this handbook

This handbook is written for the people who **use** VoltEdge day to day: network viewers, analysts, editors, field crews, and supervisors. It explains what you can see and do, how to do it step by step, and — crucially — how your permissions and your assigned territory shape what appears on your screen.

It is deliberately **how-to first**. Where something is gated by a permission you may or may not hold, the handbook says so plainly ("this needs the *editor* role") rather than diving into the internals. If you administer VoltEdge — configure permissions, compile feature classes, run resets — the companion **[Admin & Break-Glass Guide](admin-break-glass-guide.md)** is the reference for you.

A note on scope: this handbook documents **everything that has been built**, including capabilities that are still being rolled out. If a screen or button described here isn't visible to you yet, the most common reasons are (1) you don't hold the permission it needs, (2) it's outside your territory, or (3) your installation is on a build that predates it — ask your administrator.

### Table of contents

1. **[Introduction & core concepts](#chapter-1--introduction--core-concepts)** ← *this chapter*
2. Getting started — signing in and the map workspace
3. Permissions & what you can see
4. Browsing & viewing the network
5. Searching & querying
6. Creating & editing features
7. Connectivity & relationships
8. Tracing & network analysis
9. Operations modules (OMS, WAM, Faults)
10. Diagrams & annotations
11. The field app (offline)
12. External map layers & the map assistant
13. Asset tags & scannable labels
14. Appendix — permission reference & quick reference

> **Legend used throughout:** 🔒 marks an action that requires a specific permission (named inline). 🗺️ marks something that also depends on your **territory access**. 💡 is a tip. ⚠️ is a caution.

---

## Chapter 1 — Introduction & core concepts

This chapter builds the mental model you need before touching any button. Read it once; the rest of the handbook assumes the vocabulary defined here.

### 1.1 What VoltEdge is

VoltEdge is a **geographic information system (GIS) purpose-built for electrical distribution and transmission networks**. In plain terms, it is a living map of your network — every pole, transformer, switch, cable, busbar, and service connection — combined with the electrical *intelligence* that ties those objects together: what feeds what, which circuit a device belongs to, and what happens when a fault occurs.

Where a general-purpose map tool stores dumb points and lines, VoltEdge understands that a transformer has terminals, that those terminals connect to conductors, and that power flows from a source through that chain to a customer. That understanding is what lets VoltEdge do the things a spreadsheet-and-CAD workflow cannot:

- **Trace** the network — "what is upstream of this fault?", "what will I de-energise if I open this switch?"
- **Analyse** it electrically — load flow, fault current, protection coordination, hosting capacity, arc-flash.
- **Operate** it — track outages (OMS), defects and work (WAM), and fault events, all pinned to the same map.
- **Take it to the field** — build an offline pack of an area, edit it on a phone with no signal, and sync back.

VoltEdge is **metadata-driven**. There is no hard-coded list of "the twenty kinds of asset we support." An administrator defines a *feature class* (say, `DIST_XFMR` for distribution transformers) — its fields, its map symbol, its rules — and the system compiles that definition into a working part of the product. As a user you don't see this machinery; you just see that your organisation's asset types are all there. But it's worth knowing, because it explains why two VoltEdge installations can look quite different, and why "the fields on a transformer" are whatever your organisation decided they should be.

### 1.2 Who this handbook is for, and how to read it

You will get value from this handbook whether you are:

- A **viewer** who needs to find assets and read their details.
- An **analyst** who runs traces and engineering studies.
- An **editor** who creates and maintains the network.
- A **field surveyor, enumerator, or technician** capturing and updating data on site.
- A **supervisor** overseeing a team's work.

Each of those corresponds to a role with a defined set of permissions (Chapter 3 lays them out in full). You don't need to memorise them — the handbook flags the gate on each action as it comes up. If a described feature is greyed out or absent for you, Chapter 3 is where you confirm whether it's a permission you're missing.

**How to read it:** Chapters 1–3 are foundational — read them in order. Chapters 4 onward are task references you can jump into as needed; each is self-contained.

### 1.3 The core data model

Everything in VoltEdge is built from a small set of concepts. Understanding how they fit together is the single most useful thing in this handbook.

#### 1.3.1 Feature classes

A **feature class** is a *type* of network object — the template. Examples: distribution transformer, MV switch, LV conductor, pole, service point, busbar. Each feature class has:

- A **code** (a short identifier such as `DIST_XFMR`, `MV_SWITCH`, `POLE`) and a human-readable **name**.
- A **geometry type** — point, line, or polygon — or *none* at all (some classes model non-spatial objects).
- A set of **fields** (attributes) that its features carry — for a transformer that might be rated kVA, primary/secondary voltage, vector group, and so on.
- Optional **subtypes** (see 1.3.3) and a **map symbol** (its symbology).

You browse the catalogue of feature classes on the **Features** page (Chapter 4), and every feature on the map belongs to exactly one class.

#### 1.3.2 Features

A **feature** is one real object — *this* transformer, *that* span of cable. It is an instance of a feature class. A feature has:

- An **identity** — a stable internal id, and an optional human-assigned **name/label**.
- A **geometry** — its location and shape on the map (unless its class is non-spatial).
- **Attribute values** — the actual numbers and codes for this object's fields.
- A **status** — principally *draft* or *published* (see 1.5).
- A **territory** stamp — which part of the network hierarchy it falls in (see 1.3.8), assigned automatically from its location.
- Housekeeping — who created and last updated it, and when.

When this handbook says "click a feature," it means selecting one of these objects on the map or in a list to open its **feature panel** (Chapter 4), where all of the above is laid out.

#### 1.3.3 Subtypes

Many feature classes come in variants that share most fields but differ in a few. A **subtype** captures that. For example, a single conductor class might have *overhead* and *underground* subtypes; a switch class might distinguish *load-break* from *isolator*. Subtypes matter to you in three practical ways:

- When you **create** a feature, you often pick its subtype first — it can change which fields are required.
- The **map symbol** can differ by subtype, so overhead and underground conductors look different.
- A feature's subtype is **editable** after creation (Chapter 6) — for instance converting an overhead conductor to underground is a subtype change, provided the feature still satisfies the new subtype's required fields.

#### 1.3.4 Attributes and fields

A **field** is one named property defined on a feature class (e.g. `rated_kva`). An **attribute value** is what a particular feature stores in that field. Fields have a **data type** — text, number, integer, date, timestamp, boolean, or a *coded domain* (a fixed pick-list, like a status or a material code). The data type governs what you can do with the field:

- Numeric and date fields support range comparisons (greater-than, between) in the Query Builder.
- Coded-domain fields show as dropdowns and display a friendly label rather than the raw code.
- Some fields are **required** (you can't save without them), some have **default values**, and a special few are **computed** (derived by the system and read-only — for example a circuit reference that is filled in only by tracing, never typed).

#### 1.3.5 Geometry and the map

A feature's **geometry** is its footprint on the earth. VoltEdge stores geometry accurately in a geographic database (PostGIS) and draws it on an interactive map. Three things worth knowing as a user:

- **Point / line / polygon.** A transformer is a point, a cable is a line, a substation yard is a polygon. The geometry type is fixed by the feature class.
- **Some features can be map-optional.** A class can be configured so a feature may exist without a map location (it might live only on a schematic diagram instead). Chapter 10 covers diagrams.
- **The map is your primary workspace.** Nearly every task — finding, creating, editing, tracing, analysing — starts from the map. Chapter 2 tours it.

#### 1.3.6 Terminals and connectivity

This is where VoltEdge stops being a picture and becomes a network model.

- A **terminal** is a connection point on a device — the primary and secondary bushings of a transformer, the ways of a switch, the ends of a cable. Terminals are what electricity flows *through*.
- **Connectivity** is an explicit, recorded electrical connection between terminals — "this cable end is connected to that transformer's LV terminal." Connectivity is not inferred from things merely touching on the map; it is **established** deliberately (Chapter 7) so the model is exact and auditable.

Because connectivity is explicit, VoltEdge can walk the network with confidence: every trace and every electrical analysis rides on this terminal-to-terminal graph. Viewing connectivity needs the *connectivity:read* capability; establishing or revoking it needs *connectivity:commit* — both usually part of an editor's role.

#### 1.3.7 Relationships

Where connectivity is strictly *electrical*, a **relationship** records a *structural or spatial* association between two features that isn't a current-carrying connection. Examples: a transformer is **contained by** a substation bay; a cable is **supported by** a duct; two features have a **spatial support** association. Relationships:

- Have a **kind** (containment, support, and so on) drawn from the valid kinds allowed between those two classes — when you create one, VoltEdge offers only the kinds that make sense for the source and target (Chapter 7).
- Can be **established and revoked** like connectivity, gated by *relationships:read* / *relationships:commit*.
- Power features like guided assemblies, which stitch several features into a real-world grouping in one action.

#### 1.3.8 Territories

A **territory** is a node in your organisation's geographic hierarchy — region → district → and so on down. Territories do two jobs:

1. **They organise the network.** Every geographic feature is automatically stamped with the deepest territory its location falls inside, so you can slice the network by area.
2. **They control access.** 🗺️ Your account can be **granted** one or more territory nodes. When territory enforcement is switched on, you see and edit only the features inside your granted territories (and everything beneath them in the hierarchy). This is the single most important reason two users of the same system see different maps. Chapter 3 explains this in depth.

Territories are also the basis of the **territory scope** option in querying (Chapter 5) — "count/only-show features in this district and its sub-districts."

#### 1.3.9 Circuits

A **circuit** is a named electrical grouping — the run of network fed as a unit, typically from a source through a feeder. Circuits let you ask "what belongs to Circuit 12?" and label the map accordingly. A feature's circuit is modelled by an explicit membership (its `circuit_id`), while a denormalised circuit *reference* label is **derived automatically by tracing** and is read-only — you never type it, which is what keeps it consistent. Managing circuits (create/rename/delete, edit voltage and source) needs the *circuits:write* capability.

### 1.4 How the pieces connect — a worked mental model

Put the concepts together with a single example. Imagine a suburban feeder:

- A **source** at a primary substation energises an **MV conductor** (a line feature).
- That conductor's terminals are **connected** to an **MV switch**, then onward to a **distribution transformer**'s primary **terminal**.
- The transformer is **contained by** (a *relationship*) a pole-mount structure, and belongs to (a *circuit*) "Feeder 7".
- Its secondary terminal connects to an **LV conductor**, which reaches a **service point** — a customer.
- Every one of those features is stamped with the **territory** it sits in, and every one is either a working **published** record or someone's in-progress **draft**.

From that model, VoltEdge can **trace** upstream from the customer to the source, **analyse** the voltage the customer sees under load, and — if the transformer fails — record a **fault**, open an **outage**, and dispatch **work**, all against the same objects. The chapters ahead show you how to do each of those things.

### 1.5 The draft → published lifecycle

Features move through a simple lifecycle you'll meet constantly:

- **Draft** — a feature that is being worked on and is not yet part of the official, published network. New features generally start here.
- **Published** — a feature that is live and visible as part of the network of record.

You **publish** a draft to promote it, and can **unpublish** to pull it back. Two refinements matter for everyday work and are covered fully in Chapter 6:

- **Private drafts.** 🗺️ When enabled, a feature you're drafting that has *never* been published is private to **you and your management chain** — colleagues at large don't see your half-finished work until you publish it.
- **Private edits over published features.** You can stage a private, unpublished **edit or delete** of an already-live feature — a personal working copy that only you see — and apply or discard it later, without disturbing what everyone else sees in the meantime.

### 1.6 Roles at a glance

Your **role** (or roles) determines your permissions. You may hold more than one; your abilities are the union of them all. The full catalogue is in Chapter 3, but here is the shape of it so the gates flagged in later chapters make sense:

| Role | In one line |
|---|---|
| **Viewer** | Read the network, its connectivity, relationships, diagrams, attachments, and the operations boards; add map annotations. |
| **Analyst** | Everything a viewer can do, plus run traces and engineering analyses. |
| **Editor** | Everything an analyst can do, plus create/edit/publish features, commit connectivity and relationships, upload attachments, and act on outages/work/faults. |
| **Supervisor** | Everything an editor can do, plus delete whole diagrams. |
| **Admin** | Everything, including configuring the system. |
| **Break-glass admin** | An emergency super-user — see the Admin Guide. |
| **Field surveyor / enumerator / technician** | Focused field-capture roles for the offline app (read + capture; enumerators add feature edits; technicians add work and faults). |

💡 If you're ever unsure why you can't do something, the quickest answer is: check your role against Chapter 3, then check whether the feature is inside your territory (Chapter 3, §territory access).

---

---

## Chapter 2 — Getting started: signing in and the map workspace

This chapter gets you from a login screen to a confident tour of the map — the workspace where most of your work happens. By the end you'll know how to sign in, read the map, control what's drawn, and move around the app.

### 2.1 Signing in

VoltEdge is a secure system: you always start by identifying yourself. How you sign in depends on how your organisation configured VoltEdge — you may see one or both of the options below.

#### 2.1.1 Signing in with email and password (local sign-in)

The most direct route: enter your **email address** and **password** on the sign-in screen and submit.

- Your password is verified against your VoltEdge account. There is no self-service "create account" — accounts are provisioned for you by an administrator (see the Admin Guide).
- ⚠️ **Local sign-in can be switched off** organisation-wide. If your organisation has standardised on single sign-on (below), the email/password box may be hidden or disabled, and you'll use SSO instead. (One category of account — the emergency *break-glass admin* — can always sign in locally regardless of that switch; ordinary users cannot.)

#### 2.1.2 Signing in with single sign-on (SSO)

If your organisation uses **Microsoft (Azure Entra ID)** or **Google** identity, you'll see a "Sign in with Microsoft/Google" button instead of, or alongside, the password box.

- Click it, complete your identity provider's login (including any multi-factor prompt), and you're returned to VoltEdge already signed in.
- With Google, your organisation may restrict SSO to a specific email domain — an account outside that domain will be refused even if the Google login itself succeeds.
- Your VoltEdge permissions are still governed by the role(s) an administrator assigned to your account; SSO establishes *who you are*, not *what you can do*.

#### 2.1.3 Your session — staying signed in

Once in, VoltEdge keeps you signed in with a **session**:

- Your session stays alive as long as you keep using VoltEdge. After a period of inactivity it times out and you'll be asked to sign in again (the idle window is a matter of hours by default).
- There is also an absolute maximum lifetime (measured in weeks) after which you'll re-authenticate regardless of activity.
- 💡 If you're bounced to the sign-in screen mid-task, it's almost always a timed-out session — sign back in and you'll return to where you were.

#### 2.1.4 Signing out

Use the sign-out control in the application header/account menu. This ends your session immediately. On a shared machine, always sign out when you're done.

#### 2.1.5 If you can't sign in

- **Wrong email/password** — double-check both; passwords are case-sensitive. Repeated failure means your administrator must reset your password.
- **"Local sign-in is disabled"** — your organisation expects you to use the SSO button; use that instead.
- **SSO refused** — you may be outside an allowed email domain, or your account may not exist in VoltEdge yet. Contact your administrator.
- **Signed in but everything is empty** — you may hold no role yet (so nothing is permitted) or be scoped to a territory with no data. Chapter 3 explains both; the fix is with your administrator.

### 2.2 The application layout

After sign-in you land in the main application. Its layout is consistent everywhere:

- **A header bar** across the top — the VoltEdge brand, the current context, and your account menu (including sign-out). It carries a live **status area** in some contexts.
- **Navigation** to the major areas — the **Map** (your home base), the **Features** browser, the operations boards (**Outages**, **Works**, **Faults**), data pages like **Connectivity** and **Relationships**, the **Imports** area, and — if you have the permissions — a single **Admin** entry that opens the administration landing page.
- **The main work area**, which fills the rest of the screen. On the map route this is the interactive map plus its floating panels and toolbars.

What you actually see in the navigation is **filtered by your permissions** — an area you can't use isn't shown. If a colleague has a menu entry you don't, that's a difference in roles, not a bug.

### 2.3 A tour of the map workspace

Open the **Map** — it's the default landing area. This is where you'll spend most of your time.

#### 2.3.1 Panning and zooming

- **Pan** by click-dragging the map.
- **Zoom** with the mouse wheel, the trackpad, or the on-screen `+` / `−` controls. Zoom deeply — the map supports very high zoom levels for close inspection.
- As you zoom, the map redraws: some feature classes and some labels only appear once you're zoomed in far enough (an administrator sets those thresholds so a country-wide view isn't buried under detail). If you "can't find" a class of asset, try zooming in.

#### 2.3.2 Box zoom

For a precise jump to an area, use the **box-zoom** tool on the map toolbar: activate it, then drag a rectangle around the region you want, and the map zooms to fit exactly that box. It's the fastest way to frame a specific street, substation, or feeder.

#### 2.3.3 Where the map opens, and how it remembers your view

- The **first** time you open the map in a session, it starts at your organisation's configured default centre and zoom (an administrator sets this on the Map settings page).
- After that, VoltEdge **remembers your last map centre and zoom** as you navigate away to other pages and back — return to the map and you're where you left off (this memory lasts for the current session).

### 2.4 The status bar

Along the bottom of the map sits a **status bar** — a quiet strip of live readouts that tells you exactly where and how you're looking:

- **Cursor position** — the longitude/latitude under your pointer, updating as you move.
- **Zoom** — the current zoom level as a live "Zoom: N.N" readout, handy when you're matching the zoom thresholds mentioned above.
- **Projection** and **basemap** — the coordinate system in use and which background map is showing.

The status bar belongs to the map: in a split map-and-diagram view it spans only the map, and it disappears if you maximise a diagram (Chapter 10).

### 2.5 Basemaps

The **basemap** is the background imagery beneath your network — streets, satellite, or a plain backdrop.

- Switch basemaps from the map's basemap control / layers panel. The available basemaps are curated by an administrator.
- One choice is a **blank basemap** — no background tiles at all — useful when you want your network to stand alone with nothing behind it. (It adapts to light/dark mode so it never glares white in a dark theme.)

### 2.6 The layers panel

The **layers panel** is your control board for *what is drawn* on the map. Open it from the map toolbar. It's organised into sections:

#### 2.6.1 Feature-class layers

Each network feature class (transformers, switches, conductors, …) is a layer you can show or hide. Toggle a class off to declutter; toggle it on to bring it back. This is the first place to look when an expected asset type "isn't on the map" — it may simply be switched off, or hidden by the zoom thresholds from §2.3.1.

#### 2.6.2 Operational overlays

The operations modules surface on the map as their own toggleable overlays:

- **Outages (OMS)** — incident locations and their predicted impact.
- **Faults** — fault events shown as status-coloured markers, with impact "halos."

These sit in the layers panel alongside the feature classes so you can bring live operational context onto the network at will. Chapter 9 covers the modules themselves.

#### 2.6.3 External service layers and opacity

If your organisation has configured **external map layers** — third-party imagery or data served over standards like WMS, WMTS, XYZ tiles, Esri services, WFS, or vector tiles — they appear in their own section of the layers panel. You can toggle each one and adjust its **opacity** so it blends with, rather than obscures, your network. Some are **clickable to identify** — click a feature on the external layer to read its details. Chapter 12 covers external layers in full.

### 2.7 The map toolbar and tools

The map toolbar carries the tools you act *with*. Alongside box-zoom (§2.3.2) and the layers/basemap controls, one to know from the start:

- **Select tool.** 🖱️ A highlight-only multi-select for grabbing several features at once *without* opening their detail panels. Click features to toggle them into the selection, or drag a **marquee** box to add everything inside it. Press **Escape** to clear and exit. It's distinct from simply clicking a single feature (which opens that feature's detail panel — Chapter 4). Use Select when you want to visually gather a set of assets; use a plain click when you want to inspect or edit one.

Other tools — drawing new features, editing geometry, tracing, querying — are introduced in their own chapters (6, 5, 8) since each is a task in itself.

### 2.8 Floating panels

Much of VoltEdge's detail appears in **floating panels** over the map — the feature panel, trace results, analysis results, the query builder, the assistant, and more. They share consistent behaviour:

- They **float** over the map so you keep your geographic context.
- Most are **draggable** (grab the header to reposition) and **resizable** (drag the grip to resize), so you can arrange your workspace.
- They carry a subtle accent styling so they read as VoltEdge panels rather than blending into the map.

You'll rarely have to think about panels as such — but knowing they move and resize saves frustration when one covers something you need to see.

### 2.9 Light and dark mode

VoltEdge supports light and dark themes. The whole interface — including the blank basemap and the floating panels — adapts, so you can work comfortably in either. Switch via the theme control in the interface.

### 2.10 Moving between the map and the rest of the app

The map is the hub, and many other pages **link back to it**:

- From the **Features** browser, an operations board, or a search result, a "**Show on map**" action jumps to the map, pans to that feature, and opens its panel (Chapters 4 and 9).
- Conversely, the map's panels link out to related records.

You can navigate freely — the map remembers your view (§2.3.3), so hopping to a data page and back doesn't lose your place.

---

---

## Chapter 3 — Permissions & what you can see

This is the chapter that explains *why your VoltEdge looks the way it does*. Two independent systems decide what you can see and do:

1. **Permissions** (via your **role**) — *which capabilities* you have: read features, edit them, run analyses, manage circuits, and so on.
2. **Territory access** — *which slice of the network* those capabilities apply to.

They stack. You need the right permission **and** the feature must be within your territory. Get comfortable with both and almost every "why can't I…?" answers itself.

### 3.1 How permissions work

- A **permission** is a single, named capability — for example "read features" or "commit connectivity."
- Permissions aren't granted to you one by one. Instead you're assigned one or more **roles**, and each role carries a bundle of permissions.
- Your effective abilities are the **union of every permission across every role you hold.** Hold two roles and you get everything either one grants.
- Hold **no** role and you can do nothing — not even view. If you sign in and the whole app looks empty or locked down, an administrator likely hasn't assigned you a role yet.

You don't set your own permissions; an administrator does (see the Admin Guide). What follows is how to read what you've got.

### 3.2 The roles, in plain terms

VoltEdge ships with a standard ladder of roles plus a set of field-focused ones. Each rung *includes everything below it* and adds more.

#### 3.2.1 Viewer — read the network

A viewer can **look at everything but change almost nothing**:

- View features on the map and read their attributes; browse the feature catalogue.
- View **connectivity** and **relationships** (how things are wired and grouped).
- View **diagrams** (schematics).
- View and **download attachments** (photos, documents on features).
- View the operations boards — **outages, work, and faults** — read-only.
- **Add and edit map annotations** (redlining/markup). This is the one authoring power a plain viewer has — annotations are a personal/team markup layer, not network data, so they're granted early.

A viewer **cannot** create or edit features, commit connectivity, run analyses, or reach any administration.

#### 3.2.2 Analyst — viewer plus engineering studies

An analyst is a viewer who can also **interrogate** the network:

- **Run traces** — isolation, feeder, section, supply-path (Chapter 8).
- **Run analyses** — power flow, fault current, protection coordination, hosting capacity, arc-flash, EV headroom, and the rest (Chapter 8).

An analyst still can't *change* the network — reading and computing only.

#### 3.2.3 Editor — analyst plus maintaining the network

The editor is the workhorse role. On top of everything an analyst can do:

- **Create, edit, publish/unpublish, delete, and restore features** (Chapter 6).
- **Commit and revoke connectivity** between terminals (Chapter 7).
- **Establish and revoke relationships** (Chapter 7).
- **Create and edit diagrams**, and place features on them (Chapter 10).
- **Upload, replace, and delete attachments** (Chapter 6/9).
- **Act on operations** — log and manage **outages**, **work orders/defects**, and **fault events** (Chapter 9).

An editor deliberately **cannot** reach system configuration (that's *metadata:write*, an admin capability) — so an editor shapes the *data*, not the *system*.

#### 3.2.4 Supervisor — editor plus diagram deletion

A supervisor is an editor with one extra: the ability to **delete an entire diagram** (not just remove placements from it). It sits between editor and admin for teams that want that one destructive diagram power held a rung higher.

#### 3.2.5 Admin — everything

The **admin** role holds **every** permission, including all of §3.3's administrative capabilities: editing and compiling the metadata catalogue, managing users and their roles, configuring territories and the access rollout, map settings, symbology, CIM export mappings, the import engine, field packs, and the sync-review queue. Admins are covered by the companion Admin Guide.

#### 3.2.6 Break-glass admin — the emergency super-user

A special account that can always sign in locally (even when local sign-in is switched off) and is intended for emergency recovery. For everyday purposes treat it as "admin, plus break-glass sign-in." Its full nature — and the important detail that its power comes from holding the all-permissions role, not from a magic flag — is documented in the Admin Guide.

#### 3.2.7 The field roles

Three roles exist specifically for the **offline field app** (Chapter 11). A "field worker" is simply a normal user holding one of these roles plus a login token:

| Field role | Can do |
|---|---|
| **Field surveyor** | Read features and connectivity; **read and upload attachments** (photos). Pure capture/inspection. |
| **Field enumerator** | Everything a surveyor can, **plus create/edit features**. |
| **Field technician** | Everything an enumerator can, **plus log and manage work (WAM) and faults**. |

These roles are read-and-capture focused — they don't include analyses, connectivity commit, or any administration.

#### 3.2.8 Worked example — three colleagues, three very different VoltEdges

To make the ladder concrete, imagine three people at the same utility, all signed in to the same VoltEdge at the same moment, looking at the same substation.

- **Priya (viewer, control-room dispatcher).** Priya opens Feeder 7's distribution transformer. She can read every attribute, see that it's wired to an MV switch upstream and an LV board downstream, open its photos, and read its fault history. She spots that the outage board shows an active incident nearby. What she *can't* do: she has no **Edit** button on the transformer, no **Establish connection** action, and the Trace/Analyse menu is absent — she holds `features:read` and the read permissions, but not `features:write`, `trace:run`, or `analysis:run`. The one thing she *can* create is a **map annotation** — she draws a red circle round the affected area and types a note for her shift, because a plain viewer holds `annotations:write`.

- **Tom (analyst, planning engineer).** Tom sees everything Priya sees, but his transformer panel now has a working **Trace** and **Analyse** menu. He runs a supply-path trace from a complaining customer up to the source, then a voltage-profile analysis to check the customer is inside ANSI limits. He still has **no Edit button** — he can interrogate the network all day but cannot change a single value, because he holds `trace:run` and `analysis:run` but not `features:write`.

- **Dana (editor, field-data supervisor).** Dana has Tom's whole toolkit plus the authoring powers. She corrects the transformer's rated kVA, publishes the change, establishes a missing connection to a newly-built cable, uploads a nameplate photo, and logs a defect against a cracked bushing. The only doors closed to her are the **Admin** ones — she cannot reach feature-class configuration, user management, or symbology, because those need `metadata:write` / `admin:*`, which stop at the editor rung.

Same system, same substation, same instant — three genuinely different experiences, produced entirely by the roles on each account. This is the single most important thing to internalise about VoltEdge access.

### 3.3 The capability map — what each permission unlocks

If you ever want to reason about a *specific* capability rather than a whole role, this is the plain-language map of every permission VoltEdge has. (The exact permission strings — the technical identifiers — are listed here too, and again in the Appendix, because you'll occasionally see them in error messages.)

| Capability (permission string) | What it lets you do | Typically held by |
|---|---|---|
| **Read features** (`features:read`) | See feature data — map layers, attributes, the feature catalogue, imports/export/tiles, the feature registry. The baseline for seeing anything. | Everyone from viewer up; all field roles |
| **Edit features** (`features:write`) | Create, edit, publish/unpublish, delete, restore features; run data imports. | Editor+, field enumerator/technician |
| **View connectivity** (`connectivity:read`) | See committed electrical connections. | Viewer+, field roles |
| **Commit connectivity** (`connectivity:commit`) | Establish/revoke connections between terminals. | Editor+ |
| **View relationships** (`relationships:read`) | See structural/spatial relationships. | Viewer+ |
| **Commit relationships** (`relationships:commit`) | Establish/revoke relationships. | Editor+ |
| **View diagrams** (`diagrams:read`) | Open schematic diagrams. | Viewer+ |
| **Edit diagrams** (`diagrams:write`) | Create/rename diagrams; place/remove features. | Editor+ |
| **Delete diagrams** (`diagrams:delete`) | Delete an entire diagram and its placements. | Supervisor+ |
| **View annotations** (`annotations:read`) | See map annotations/redlining. | Viewer+ |
| **Edit annotations** (`annotations:write`) | Create/edit/delete map annotations. | Viewer+ (yes — even a plain viewer) |
| **Run traces** (`trace:run`) | Run network traces. | Analyst+ |
| **Run analyses** (`analysis:run`) | Run power-flow/fault/coordination/hosting/etc. | Analyst+ |
| **Read metadata** (`metadata:read`) | View the configuration catalogue and external-systems list. | Viewer+ |
| **Edit metadata** (`metadata:write`) | Configure the system — feature classes, symbology, map settings, service layers, private-draft rollout, basemaps. **Admin only.** | Admin |
| **Auth settings** (`admin:auth-settings`) | Change authentication settings (incl. the local-sign-in switch). | Admin |
| **Manage users** (`admin:users`) | Create/manage users, assign roles, set territory grants and the manager edge. | Admin |
| **Edit territories** (`territories:write`) | Create/rename/re-parent/delete territories and edit boundaries; toggle the enforcement rollout. | Admin |
| **Unscoped territory access** (`territories:unscoped`) | **Bypass territory scoping entirely** — see the whole network regardless of grants. | Admins / privileged accounts |
| **Manage circuits** (`circuits:write`) | Create/rename/delete circuits, edit voltage & source. | Admin (or as granted) |
| **Manage field packs** (`admin:field-packs`) | List/disable/delete *any* user's offline field packs. | Admin |
| **Read attachments** (`attachments:read`) | View/download feature attachments. | Viewer+, field roles |
| **Write attachments** (`attachments:write`) | Upload/replace/delete attachments. | Editor+, field roles |
| **Review sync** (`sync:review`) | View the field-sync review queue. | Admin (or as granted) |
| **Post sync** (`sync:post`) | Approve/reject reviewed field-sync operations. | Admin (or as granted) |
| **Read outages** (`oms:read`) | View the outage board. | Viewer+ |
| **Write outages** (`oms:write`) | Log trouble calls, run prediction, confirm/restore/cancel incidents. | Editor+ |
| **Read work** (`wam:read`) | View the work board (defects/orders/inspections). | Viewer+, field technician |
| **Write work** (`wam:write`) | Log defects, raise/schedule/assign/complete work orders. | Editor+, field technician |
| **Read faults** (`faults:read`) | View the fault board. | Viewer+, field technician |
| **Write faults** (`faults:write`) | Log fault events, link outages, raise defects from faults. | Editor+, field technician |

💡 **Reading an error message.** If VoltEdge ever says an action is "forbidden" or names a permission like `features:write`, match it against this table to see which role would grant it — then ask your administrator for that role (or check whether the block is actually a *territory* one, next section).

### 3.4 Territory access — which slice of the network is yours

Permissions decide *what you can do*; **territory access** decides *where you can do it*. This is the second, spatial layer of access, and it's the usual reason two people with the same role see different maps.

#### 3.4.1 The idea

Your organisation's network is divided into a **hierarchy of territories** — regions, districts, and so on. Your account can be **granted** one or more territory nodes. When territory enforcement is switched on, you work only within your granted territories.

#### 3.4.2 Grants cascade *down* the hierarchy

A grant on a territory node automatically covers **that node and everything beneath it**. Granting you a *region* gives you every district and sub-area inside it — you don't need a separate grant per child. Grants name a node; the descendants are worked out for you at the moment you look.

#### 3.4.3 Management cascades *up* the org chart

There's a second hierarchy: the **management/org tree**. If you **manage** other people, you automatically inherit the **union of every territory grant your reports hold** — transitively, all the way down your reporting line. A regional manager who directly holds nothing can still see everything their teams can, simply by managing them. (You never inherit *upward* from your own manager — only downward through the people who report to you.)

#### 3.4.4 What enforcement actually does

When enforcement is on and you are a scoped user, the territory constraint is applied everywhere network data is read or written:

- **On the map and in lists** — only features in your territories are drawn or listed.
- **Opening a specific feature** — a feature outside your territory behaves as if it doesn't exist (you get a "not found," never a tantalising "forbidden," so the system doesn't even leak that it's there).
- **Editing** — trying to change a feature outside your scope is refused.
- **Map tiles, exports, queries, aggregates, and history** — all obey the same boundary, so you can't slip past it through a side door.

#### 3.4.5 Fail-closed — the safe default

Territory access is **fail-closed**, which has two consequences worth internalising:

- **"Scoped to nothing" means you see nothing** — not "no filter, see everything." If your grants resolve to an empty set, your map is empty by design.
- **A feature with no territory stamp is invisible to a scoped user.** Every geographic feature normally gets stamped automatically from its location; one that somehow has no territory simply won't appear for scoped users (an administrator can backfill missing stamps).

So if your map is unexpectedly bare, the question is usually "what territories am I granted?" — ask your administrator.

#### 3.4.6 The unscoped bypass

Some accounts hold the **unscoped** capability (`territories:unscoped`), which **switches territory limits off for that account entirely** — they see and work with the whole network regardless of grants. Admins and privileged accounts typically have it. If you have it, §3.4.1–3.4.5 simply don't constrain you.

#### 3.4.7 Revocation is immediate

If your grant to a territory is removed, your access to that area **disappears at once** — there's no cached lag. This is deliberate, so access can be pulled the moment it should be.

#### 3.4.8 Is enforcement even on?

Territory enforcement is a **rollout switch** an administrator controls, and it ships **off**. So in a fresh or un-rolled-out installation, *everyone with the read permission sees the whole network* and none of §3.4 constrains you. Once an administrator turns it on and starts granting territories, the picture above takes effect. If you're not sure whether your installation enforces territories, that's a question for your administrator.

#### 3.4.9 Worked example — how a grant plays out

Picture a simple territory hierarchy:

```
Northern Region
├── Riverside District
│   ├── Oakwood Zone
│   └── Millbrook Zone
└── Hillside District
    ├── Ashford Zone
    └── Beacon Zone
```

Now follow four people, assuming enforcement is **on**:

- **Sam is granted `Riverside District`.** Sam sees Riverside *and everything beneath it* — Oakwood and Millbrook — because grants cascade down (§3.4.2). A transformer in Oakwood opens normally. A transformer in **Ashford** (under Hillside) reports **"not found"** when Sam tries to open it, and never appears on Sam's map, in Sam's lists, in Sam's exports, or in Sam's query counts — the boundary is applied everywhere (§3.4.4).

- **Mia is granted `Oakwood Zone` only.** Mia's world is a single zone. Everything else — even the rest of Riverside — is invisible. If Mia runs an "all transformers" count (Chapter 5), the number she gets is *the Oakwood count*, not the network total, because the territory filter is applied to aggregates too.

- **Raj is granted nothing, but manages Sam and Mia.** Even with zero direct grants, Raj inherits the **union** of his reports' access (§3.4.3): Riverside (from Sam) plus Oakwood (already inside Riverside, from Mia). Raj effectively sees all of Riverside District. Promote someone new under Raj who holds a Hillside grant, and Raj's view instantly widens to include Hillside too — no change to Raj's own account needed.

- **Alex holds the unscoped capability.** Alex sees the entire Northern Region and every other region, ignoring all of the above (§3.4.6).

Two failure modes fall straight out of this example:

- **Sam's grant is revoked mid-shift.** The moment an administrator removes Sam's Riverside grant, Sam's map goes empty and open features become inaccessible — revocation is immediate (§3.4.7), by design.
- **A feature was created with no territory stamp.** Suppose a bulk import loaded a transformer without a location. That transformer has no territory, so it's invisible to Sam, Mia, *and* Raj — only unscoped Alex (or an administrator who backfills the stamp) can see it. If a scoped user reports "a feature exists but I can't find it anywhere," a missing territory stamp is a prime suspect (§3.4.5).

### 3.5 Private drafts and private edits — who sees your in-progress work

VoltEdge lets you work on features without immediately exposing that work to everyone. Two related mechanisms, both of which an administrator enables via a rollout switch (they also ship **off**), and both of which stack **on top of** territory access:

#### 3.5.1 Private drafts (never-published work)

A feature you're drafting that has **never been published** is private to **you and your management chain**:

- **You**, the author, always see your own draft.
- **Anyone who manages you** (up your reporting line) can also see it — so a supervisor isn't blind to a team member's unfinished work, and nothing is orphaned if you're away.
- **Everyone else** does not see it until you **publish** it, at which point it becomes normal network data.

This lets you build up a feature over several sessions without cluttering colleagues' maps or exposing half-entered data.

#### 3.5.2 Private edits over published features

You can also stage a private **edit or delete of a feature that is already published** — a personal working copy layered over the live record:

- The **public feature stays exactly as everyone else sees it.** Your staged change shows **only to you**.
- Unlike private drafts, a private edit is visible to **you alone** — not your management chain — because it's a private workspace over a record your managers can already see fine.
- When you're ready you **apply** it (making it real) or **discard** it (dropping it with no trace). A staged *delete* hides the row from your own view while it stays live for everyone else until you apply.

Chapter 6 shows the actual buttons; the point here is the *visibility* rule: your staged edits are yours until you commit them.

#### 3.5.3 How this interacts with territory

Both mechanisms are **combined with** territory access, never instead of it. A private draft is visible only to someone who is *both* an authorised viewer (you, or your management chain for never-published drafts; you alone for staged edits) *and* within the right territory scope. Unscoped and break-glass accounts see through both.

#### 3.5.4 Worked example — a survey that isn't ready to share

Dana (the editor from §3.2.8) is capturing a new housing-estate extension over several days, with private drafts enabled.

- **Day 1.** Dana creates six new service points and a length of LV cable as **drafts**. None have ever been published, so they're private to Dana and her management chain. Her teammate Tom, panning across the same street, sees nothing new — his map is unchanged. Dana's manager, however, *can* see the six drafts if she looks, so the work isn't hidden from oversight and won't be orphaned if Dana is off sick.

- **Day 3.** Dana finishes and **publishes** the batch. Instantly they become ordinary network features and appear for Tom, Priya, and everyone else within that territory.

Now a different situation — a private *edit* over something already live:

- Feeder 7's published transformer has its rated kVA recorded as 500. Dana believes it's actually 315 but wants to verify against the nameplate before committing. She stages a **private edit** changing it to 315. On **Dana's** screen the transformer now reads 315; on **everyone else's** — including her manager's — it still reads the published 500, because staged edits are visible to their author *alone* (§3.5.2). After a site visit confirms 315, Dana **applies** the edit and the public value updates for all. Had the nameplate said 500 after all, she'd simply **discard** it, leaving no trace.

The takeaway: never-published drafts are a *shared-with-my-chain* workspace; staged edits over live features are a *strictly-personal* workspace — and both still obey territory.

### 3.6 "Why can't I…?" — a quick diagnosis

When something you expect isn't available, walk this short list:

1. **Is it a permission gap?** Match the action to §3.3. If your role doesn't grant it, that's your answer — request the role. *Example:* the **Edit** button is missing on every feature everywhere → you almost certainly lack `features:write` (you're a viewer or analyst), not a territory problem.
2. **Is it a territory limit?** If you *can* do the action elsewhere but not *here* (a feature won't open, or your map is empty in one area), it's likely §3.4 — check your territory grants. *Example:* transformers open fine in Riverside but "not found" in Hillside → you're granted Riverside only.
3. **Is it hidden, not forbidden?** A layer toggled off (§2.6.1) or a zoom threshold (§2.3.1) can make data look "missing" when you actually have full access. *Example:* "the LV cables vanished" right after you zoomed out → they're below their display-zoom threshold; zoom back in.
4. **Is it someone else's private work?** A draft you can't see may simply be a colleague's unpublished private draft (§3.5). *Example:* a teammate says "I just added it" but you can't see it → it's still their private draft; it'll appear when they publish.
5. **Is it a stale build?** Occasionally a described feature isn't in your installation's current build yet. Your administrator can confirm.

💡 The fastest triage question is: **"can I do this action *anywhere*?"** If **no**, it's a permission gap (step 1). If **yes, just not here**, it's territory or visibility (steps 2–4).

---

---

## Chapter 4 — Browsing & viewing the network

This chapter is about *reading* the network — finding assets, opening them, and understanding everything a feature can tell you. Everything here needs only **read** permissions (`features:read` and the matching read permission for each sub-area), so it's available to every role from viewer up. 🗺️ What you can see is still bounded by your territory (Chapter 3).

### 4.1 Finding and opening a feature on the map

The map is the fastest way to reach a feature:

- **Click a feature** on the map to select it and open its **feature panel** — the detail window for that one object.
- If several features overlap where you click, VoltEdge opens the **topmost** one; zoom in to separate them, or use the tools in later chapters to disambiguate.
- To find something you can't see yet, combine the earlier tools: toggle its class on in the layers panel (§2.6.1), zoom past any display threshold (§2.3.1), and pan or box-zoom (§2.3.2) to the area.

💡 If you know *what* you're looking for but not *where* it is, jump ahead to Chapter 5 (searching & querying) — you can query for it and then "Show on map."

### 4.2 The feature panel

The **feature panel** is the heart of viewing. It floats over the map (draggable and resizable — §2.8) and is organised into tabs. Not every feature has every tab — a class with no terminals won't show a Terminals tab — but the set below is what you'll encounter.

#### 4.2.1 The header — identity at a glance

The top of the panel shows the feature's **name/label**, its **feature class**, its **subtype** (if any), and its **status** (draft or published). This is your orientation: *what* this is and *where it stands* in the lifecycle. From the header you also reach the panel's actions menu (edit, trace, analyse, and more — those are covered in Chapters 5–8).

#### 4.2.2 Attributes — the feature's data

The **Attributes** view lists every field on the feature with its value:

- **Coded fields** show a friendly label (e.g. a status name), not the raw stored code.
- **Empty fields** are shown as blank/none rather than hidden, so you can see what *could* be filled in.
- **Computed fields** (like a derived circuit reference) are shown read-only — they're filled in by the system, never typed.

This is a read view; editing attributes is Chapter 6 and needs `features:write`.

#### 4.2.3 Terminals — the connection points

For a feature that has them, the **Terminals** tab lists the device's terminals (a transformer's primary/secondary, a switch's ways, a cable's ends). Terminals are what connectivity attaches to (§1.3.6). From here you can see each terminal and, with the right permission, work with its connections.

#### 4.2.4 Connections — what it's electrically wired to

The **Connections** view shows the committed electrical connectivity at this feature's terminals — what's wired to what. Each connection can be navigated to the feature on the other end. Establishing or revoking a connection needs `connectivity:commit` and is covered in Chapter 7; here you're reading the wiring.

#### 4.2.5 Relationships — structural and spatial associations

The **Relationships** tab lists the non-electrical associations this feature participates in — what contains it, what it supports, and so on (§1.3.7). Each row can be navigated to the related feature. If you hold `relationships:commit`, each row also offers an **un-link (revoke)** action to remove that relationship (Chapter 7); a viewer sees the relationships read-only.

#### 4.2.6 Operations tabs — faults (and related activity)

Where a feature has operational activity, the panel surfaces it. Most notably a **Faults** tab lists fault events recorded against this asset, giving you the operational context of a piece of equipment right beside its data. From here (with the right permission) you can act — e.g. raise a fault — but as a viewer you read the history. Chapter 9 covers the operations modules in full.

#### 4.2.7 History — how this feature changed over time

The **History** view is the audit trail: who changed what, and when. 🗺️ Note that history respects privacy — entries relating to other people's private drafts or private edits are hidden from you, so what you see in history is consistent with what you're allowed to see everywhere else (Chapter 3).

#### 4.2.8 Attachments — photos and documents

The **Attachments** tab holds files pinned to the feature — site photos, datasheets, permits. Reading and downloading them needs `attachments:read` (viewers have it); uploading, replacing, or deleting needs `attachments:write` (editors and field roles). Field crews commonly attach photos on site, which then appear here (Chapter 11).

#### 4.2.9 External links — pointers to other systems

A feature can carry **external links** — references out to related records in other systems your organisation runs. Where present, they appear in the panel so you can jump from the asset to its record elsewhere.

#### 4.2.10 Worked example — reading a distribution transformer end to end

Suppose a customer on Feeder 7 has reported flickering lights, and Priya (viewer) wants to understand the transformer that serves them. Here's how the panel tells the whole story, tab by tab:

1. **She finds it.** Priya zooms to the street, clicks the transformer symbol, and its panel opens bottom-right. The **header** reads *"TX-Feeder7-014 · Distribution Transformer · Pad-mount · Published."* Straight away she knows what it is and that it's live network data, not a draft.

2. **Attributes.** The **Attributes** tab shows rated kVA = 315, primary = 11 kV, secondary = 415 V, vector group = Dyn11, and a **circuit reference** of "Feeder 7" shown greyed-out — she notes that's a *computed* value, set by tracing, so it's reliable and not hand-typed.

3. **Terminals.** The **Terminals** tab lists a primary (HV) terminal and a secondary (LV) terminal — the two points current flows through.

4. **Connections.** The **Connections** tab shows the HV terminal wired to an MV switch upstream and the LV terminal wired to an LV distribution board downstream. Priya clicks the upstream connection and the panel navigates to the MV switch — she can walk the wiring by hand this way.

5. **Relationships.** Back on the transformer, the **Relationships** tab shows it is *contained by* a pad-mount enclosure — the structural context that connectivity alone wouldn't tell her.

6. **Faults.** The **Faults** tab shows one fault event logged against this asset three weeks ago, since resolved. Useful background for a flicker complaint.

7. **History.** The **History** tab shows the rated-kVA value was corrected last month by Dana — consistent with what everyone can see, because any private in-progress edits by others would be hidden from Priya here.

8. **Attachments.** The **Attachments** tab has a nameplate photo a field crew uploaded; Priya opens it to confirm the rating matches.

In five minutes, without editing anything, Priya has reconstructed the asset's identity, electrical context, structure, operational history, and documentary evidence — all from one panel. That is the feature panel's job.

### 4.3 The Features browser

Sometimes you want to work from a **list** rather than the map — to scan a whole class of asset, or reach a feature you can't easily spot geographically. That's the **Features** page (in the main navigation).

#### 4.3.1 Browsing feature classes

The Features browser presents your organisation's **feature classes**. Pick a class to see its features as a table. This is also the quickest way to answer "what kinds of asset does our VoltEdge hold?" — the catalogue is right there.

#### 4.3.2 The per-class list

Within a class you get a scrollable, readable table of its features — names, key attributes, and status. Use it to review a class systematically, spot outliers, or confirm counts. (For richer questions — filtering by attribute, counting by category, spatial scope — use the Query Builder in Chapter 5.)

#### 4.3.3 "Show on map"

Any feature in the list that has a map location offers a **Show on map** action (a map-pin button on the row). Click it and VoltEdge switches to the map, **pans to that feature, and opens its panel** — the bridge from list to map. (The action only appears for features that actually have geometry; a purely non-spatial record has nothing to show on the map.)

#### 4.3.4 Worked example — from a list scan to a feature on the map

Tom (analyst) has been asked "do we have any 11 kV switches that were never given a name?" He works from the list, not the map:

1. He opens the **Features** page and picks the **MV Switch** class.
2. The table lists every MV switch he has access to. Scanning the *name* column, he spots a handful of blank rows.
3. He clicks **Show on map** on the first blank one. VoltEdge jumps to the map, pans to that switch, and opens its panel — Tom now has it in geographic context and can see which feeder it's on.

For a handful of rows, eyeballing the list is fine. For a precise answer across thousands of features — "*count* the unnamed 11 kV switches," or "list every switch missing a name *in this district*" — the Query Builder in Chapter 5 is the right tool, and it can drill straight from that count to the very rows on the map.

### 4.4 Selecting versus highlighting

Two different "select" behaviours exist, and knowing which is which saves confusion:

- **Click a single feature → opens its panel.** This is *selection for inspection* — you want to look at or work on that one object. (This is the behaviour used by "Show on map" and by ordinary map clicks.)
- **The Select tool → highlights many, opens nothing.** As introduced in §2.7, the Select tool gathers a *set* of features (click-to-toggle or marquee-drag) and simply highlights them, without opening any panel. Use it when you want to visually group or count assets on the map rather than inspect one.

💡 Rule of thumb: **plain click when you want details; Select tool when you want a group.**

*Example.* Dana wants to visually confirm how many poles line one street before a survey. She activates the **Select tool** and drags a marquee down the street — every pole inside the box lights up highlighted, and she can eyeball the count, with no panels popping open to get in her way. If she'd instead plain-clicked each pole, she'd have opened (and had to close) a dozen feature panels one after another. Later, wanting the *details* of one particular pole, she switches back to a plain click and its panel opens.

### 4.5 Reading values correctly

A few conventions help you interpret what you see:

- **Coded values show labels.** Wherever a field is a coded domain, the panel and lists show the human label; the underlying code appears only in places like query conditions and error messages.
- **Blank means empty, not hidden.** An empty field is genuinely empty for that feature.
- **Draft vs published** is always shown in the header — if a feature looks unfamiliar or provisional, check whether it's still a draft.

### 4.6 What you *won't* see (and why that's correct)

Reading the network faithfully means also understanding its intentional gaps (all from Chapter 3):

- **Out-of-territory features** simply aren't there for a scoped user — and an out-of-scope feature you try to open by other means reports "not found."
- **Other people's private drafts** don't appear until published (unless you're in their management chain).
- **Layers toggled off** and **zoom thresholds** hide data you *do* have access to — check these first when something's "missing" before assuming an access problem.

---

---

## Chapter 5 — Searching & querying

Chapter 4 was about reading *one* feature you'd already found. This chapter is about answering *questions across many features* — "which transformers are overloaded?", "how many switches of each type are in this district?", "what's actually sitting within 500 m of this fault?" — and then jumping straight from the answer to the features on the map.

The tool for all of this is the **Query Builder**, a guided panel on the map. Reading it needs only `features:read`, so every role from viewer up can use it. 🗺️ As always, your results are bounded by your territory and by draft visibility (Chapter 3) — a count is a count *of what you're allowed to see*.

### 5.1 Opening the Query Builder

Open the Query Builder from the map (the query/search tool). It appears as a floating panel (draggable and resizable — §2.8). At the top you choose **what you're querying** and **how**:

- A **feature-class selector** — pick the class you want to ask about (transformers, switches, conductors…), *or* the special **"All feature classes"** option (§5.6).
- A **mode toggle** — **Filter** (find and list features) or **Aggregate** (count and summarise them). The toggle is hidden when you pick "All feature classes", because that mode is always a count.

The panel is *guided*: it only offers operators and fields that make sense for what you've chosen, so you can't build an invalid query. Everything you build runs against the live database when you press **Run**.

### 5.2 The two modes at a glance

| Mode | Answers | Returns |
|---|---|---|
| **Filter** | "*Which* features match these conditions?" | A list of matching features (and highlights them on the map) |
| **Aggregate** | "*How many* — overall, or broken down by a field? And what's the sum/average/min/max?" | Counts and roll-up numbers |
| **All feature classes** | "*What is here, and how many of each kind?*" | A count per feature class |

You'll move between them constantly — often *aggregate first* to get the shape of the answer ("300 transformers, 40 of them over 90 % loaded"), then *drill down* (§5.7) into the rows behind an interesting number.

### 5.3 Filter mode — finding the features that match

Filter mode builds a set of **conditions** and returns the features that satisfy them.

#### 5.3.1 Anatomy of a condition

A condition is three parts: **field**, **operator**, **value**.

> `rated_kva` **greater than** `500`

- **Field** — any of the feature class's own fields (rated kVA, material, install date…) plus the universal ones every feature has: **name**, **status**, **created/updated date**, and **subtype**. You pick from a list, so you can't misname a field.
- **Operator** — the comparison (see §5.3.2). The panel offers only operators valid for that field's type.
- **Value** — what you're comparing against. For a coded field you pick from its allowed values; for a number you type a number; some operators take two values or none.

#### 5.3.2 The operators, by field type

Which operators you get depends on the field's data type — this is deliberate, so a nonsensical comparison (a "greater-than" on a name) is refused rather than silently misbehaving.

| Operator | Meaning | Valid on |
|---|---|---|
| **equals / not equals** | exact match / exclusion | any field |
| **greater than / ≥ / less than / ≤** | ordered comparison | numbers, integers, dates, timestamps only |
| **between** | within a range (inclusive), two values | numbers, integers, dates, timestamps only |
| **contains / starts with** | case-insensitive text match | text fields only |
| **is one of (in)** | matches any value in a list | any field |
| **is empty / is not empty** | field has no value / has a value | any field |

💡 "Greater than" on a **text** field (like a name) is intentionally *not offered* — it's almost always a mistake. If you want partial-name matching, use **contains** or **starts with**.

*Examples of single conditions:*
- `status` **equals** `retired`
- `install_date` **before (less than)** `2005-01-01`
- `name` **contains** `Feeder 7`
- `rated_kva` **between** `315` and `1000`
- `circuit_ref` **is empty** (find features tracing hasn't assigned to a circuit)
- `material` **is one of** `[XLPE, PILC]`

#### 5.3.3 Combining conditions — match *all* vs match *any*

Add several conditions and choose how they combine:

- **Match all** (AND) — a feature must satisfy *every* condition. This is the default and the common case.
- **Match any** (OR) — a feature qualifies if it satisfies *at least one* condition.

*Example (match all):* `rated_kva` ≥ `500` **AND** `status` = `in_service` → large, live transformers.
*Example (match any):* `material` = `PILC` **OR** `install_date` < `1990-01-01` → "anything old-technology *or* simply old."

#### 5.3.4 Nested groups — expressing `(A AND B) OR C`

Real questions sometimes mix AND and OR. The builder lets you nest **groups**, each with its own match-all/any, so you can express things a flat list can't:

> **( `rated_kva` ≥ `1000` AND `status` = `in_service` )  OR  ( `flagged_for_replacement` = `true` )**

Read as: "big in-service transformers, *plus* anything already flagged for replacement, whatever its size." Each group is evaluated as a unit, so the OR at the top can't accidentally loosen the AND inside. You can nest a few levels deep — far more than any realistic question needs. (There are generous safety caps on total conditions and nesting depth so a malformed query can't misbehave; you'll never hit them in normal use.)

#### 5.3.5 Running a filter and reading the results

Press **Run**. VoltEdge returns the matching features and does two helpful things at once:

- **Lists them** in the panel, each row showing its name/status and key details.
- **Highlights them on the map**, so you see *where* your matches are, not just *that* they exist.
- Each result row offers a **zoom-to** action — click it and the map flies to that feature and opens its panel.

A filter returns up to a few hundred rows at a time (a sensible page size); if you're matching more than that, tighten your conditions or add a spatial scope (§5.5) — and if you really just want the *number*, that's what Aggregate mode is for.

### 5.4 Aggregate mode — counting and summarising

Aggregate mode answers "how many" and "how much" instead of listing rows. It reuses the *same* conditions as filter mode to narrow the set first, then folds what's left into numbers.

#### 5.4.1 A simple total

With no grouping, Aggregate gives you a single **total** — the count of features matching your conditions (and scope).

*Example:* conditions `status` = `in_service`, no grouping → "**1,284** transformers in service." Add `rated_kva` ≥ `500` → "**212** of them are 500 kVA or larger."

#### 5.4.2 Count by a field ("group by")

Choose a **Group by** field and you get *one row per distinct value*, each with its count, ordered biggest-first. This is the "breakdown" question.

*Example:* Group MV switches **by subtype** →

| Subtype | Count |
|---|---|
| Load-break | 412 |
| Isolator | 305 |
| Ring-main unit | 88 |
| *(unset)* | 6 |

Group by **status**, by **material**, by **subtype**, by any coded or plain field. The *(unset)* bucket captures features with no value — often exactly what you're hunting for (the six switches with no subtype recorded).

#### 5.4.3 Secondary roll-ups — sum, average, min, max

Beyond the count, you can add one **aggregate function** over a **numeric field**: **sum**, **average**, **minimum**, or **maximum**. It's reported overall and per group.

*Example:* Group transformers by circuit, count them, and **sum** their `rated_kva` → per-circuit transformer count *and* installed capacity, so you can see at a glance which circuit carries the most kVA. Or **max** `install_date` per district to find the most recently built area.

💡 A grouped aggregate is capped at a large number of group rows, but the **total is always the true total** even if the groups list is capped — so the headline number never lies, even when the breakdown is truncated.

### 5.5 Spatial scope — asking "…here"

Any query or aggregate can be constrained to a **place**. This is what turns "how many transformers" into "how many transformers *in this district*" or "*within 500 m of this pole*." Pick the scope from the panel:

#### 5.5.1 No scope (everywhere)

The default — your query runs across the whole network you can see (still bounded by your territory access, of course).

#### 5.5.2 Territory scope

Choose a **territory** from the dropdown and the query is limited to that territory *and everything beneath it* in the hierarchy (§3.4.2) — the same subtree logic as access grants. "In Riverside District" automatically includes Oakwood and Millbrook zones.

*Example:* Aggregate transformers, group by subtype, territory scope = "Riverside District" → the subtype breakdown for just that district and its zones.

#### 5.5.3 Radius scope — draw a circle on the map

Radius scope answers "within *X* metres of *this exact spot*." How to use it:

1. Choose **Radius** as the scope.
2. Click **Pick point on map** — the cursor becomes a crosshair.
3. Click the spot on the map you want to centre on. VoltEdge drops a centre marker and draws a true-to-scale circle (a real geodesic circle in metres, not a rough square).
4. Set the radius — pick a preset (100 / 250 / 500 / 1000 / 2000 m) or type an exact distance. The default is 250 m.
5. Press **Run**.

The distance is genuine ground distance in metres, so a 500 m radius really means 500 m on the earth. Press **Escape** to clear the circle if you want to reposition. This is the tool for incident work — "what's *right here*?"

### 5.6 "All feature classes" — what's here, and how many of each

Often the useful question at a location isn't about one class — it's "**what is here at all?**" Select **All feature classes** and VoltEdge counts *every* class of feature in your chosen scope and returns a **breakdown by class**, biggest first, with a grand total.

- It's designed to pair with a **radius** (or territory) scope: drop a circle on a fault location and instantly see "23 features here — 8 LV service points, 6 poles, 4 LV cables, 2 switches, 1 transformer, …".
- A class you can't read contributes nothing rather than breaking the sweep, so the result is always *what you're entitled to see*.
- Because it's inherently a count across classes, there's no filter/aggregate toggle here — it's always a count.

*Example:* An engineer investigating a third-party dig-in drops a 100 m radius on the reported location, selects All feature classes, and immediately has an inventory of everything potentially affected — the fastest possible "what's at risk here?"

### 5.7 Drill-down — from a count straight to the rows behind it

A count is only the start of the story. **Every number in a result is a doorway**: click it and VoltEdge re-runs the query for exactly that slice and shows you the underlying features — listed in the panel *and* highlighted on the map, with per-row zoom-to. It preserves your full narrowing (your conditions **and** your spatial scope), so the rows you get are precisely the ones that made up the number.

You can drill from:

- **A simple total** — a "Show these features" action turns the headline count into its rows.
- **A grouped row** — click the "412 load-break switches" row and you get those 412 features (your other conditions and scope still applied).
- **An all-classes row** — click "6 poles" in the radius breakdown and you get those six poles on the map.

A **Back to counts** control returns you to the summary, and closing the drill clears the highlight. This count → rows path is the everyday loop: *aggregate to understand the shape, drill to act on the specifics.*

*Example (the whole loop):* Dana wants to plan PILC-cable replacement in Riverside.
1. **Aggregate** LV cables, group by `material`, territory scope = Riverside → sees "PILC: 137."
2. **Clicks** the PILC row → those 137 cables list in the panel and light up on the map.
3. **Clicks** a row → the map flies to that cable and opens its panel so she can check its condition.
4. **Exports** the 137 to CSV (§5.8) for the replacement programme.

One question, answered as a number, opened into a map, turned into a worklist — without leaving the panel.

### 5.8 Exporting results to CSV

Most result views — a filter list, an aggregate breakdown, an all-classes count, or a drill-down list — offer an **Export CSV** action. Use it to take a count or a set of matches into a spreadsheet, a report, or a work-planning tool. The export reflects exactly what's on screen: the same rows, the same narrowing.

### 5.9 Practical tips, limits, and gotchas

- **Your results respect your access.** Every count and list is filtered by your territory and by draft visibility (Chapter 3). Two users running the identical query in an enforced installation can legitimately get different numbers — each sees their own slice. If a total looks low, check whether you're territory-scoped.
- **Aggregate first, drill second.** For anything involving numbers, start in Aggregate mode; it's faster and gives you the lay of the land before you pull potentially thousands of rows.
- **Use scope to keep lists manageable.** A filter returns a page of rows at a time. If you're matching more than that, add a territory or radius scope, or tighten conditions — or switch to Aggregate if you only wanted the count.
- **The *(unset)* / "is empty" pattern is your data-quality friend.** Grouping by a field, or filtering with **is empty**, is the quickest way to find records missing a value — unnamed switches, un-circuited cables, transformers with no rating.
- **Radius is true metres.** A 500 m circle is 500 m on the ground, drawn as a proper geodesic circle — trust it for real distance work.
- **Computed fields are queryable but not editable.** You can filter or group by a derived field like `circuit_ref`, but you'll never set it here — it's produced by tracing (Chapter 8).

---

---

## Chapter 6 — Creating & editing features

Chapters 4 and 5 were about *reading* the network. This chapter is where you *change* it — creating new assets, correcting data, moving geometry, and moving features through their lifecycle. 🔒 Everything here needs the **Edit features** capability (`features:write`), so it's available to editors and above, and to field enumerators/technicians. 🗺️ And you can only edit features inside your territory (Chapter 3) — an out-of-scope feature can't be changed even if you can somehow see it.

If you're a viewer or analyst and the buttons in this chapter are missing, that's expected — see §3.6.

### 6.1 Before you start — the two things that gate every edit

Two checks decide whether an edit will work, and it's worth having them front of mind:

1. **Do you hold `features:write`?** If not, there's no Edit button anywhere. (Request the editor role — §3.3.)
2. **Is the feature in your territory?** If enforcement is on and the feature is outside your scope, creating or changing it is refused (§3.4.4). Creating a *new* feature stamps it with the territory of wherever you place it, so you can only create where you're allowed to.

With those in hand, the rest of the chapter is mechanics.

### 6.2 Creating a new feature

#### 6.2.1 Starting a new feature

Begin from the map's **new-feature** control. You'll choose a feature class to create, and then place it on the map by drawing its geometry. (For multi-feature real-world units — a pole with its transformer, cutouts, and LV board — don't build them one by one; use a **guided assembly**, Chapter 7, which creates and wires the whole kit in one action.)

#### 6.2.2 Choosing the class and subtype

- **Class** first — what kind of asset this is (distribution transformer, MV switch, LV conductor…).
- **Subtype** next, if the class has them (§1.3.3). This matters *before* you fill in attributes, because **the subtype can change which fields are required.** Pick the overhead vs. underground conductor, the load-break vs. isolator switch, up front, and the form adjusts what it asks of you.

#### 6.2.3 Drawing the geometry

You place the feature by drawing its footprint on the map, in the shape its class demands:

- **Point** classes (transformers, poles, service points) — click once to drop the point.
- **Line** classes (conductors, cables) — click each vertex along the route, then finish the line.
- **Polygon** classes (yards, zones) — click the boundary vertices and close the shape.

Some classes are **map-optional** — they *can* be created without a location (to live only on a schematic diagram, Chapter 10) — but most network assets are placed on the map as you create them.

#### 6.2.4 Filling in the attributes

With the class and subtype set, complete the feature's fields:

- **Required fields** must be filled before you can save — the form won't let a required value through empty. Which fields are required depends on the **subtype** you chose (§6.2.2).
- **Defaults** — some fields pre-fill with a sensible default you can accept or change.
- **Coded fields** appear as dropdowns of their allowed values, so you can't type an invalid code.
- **Computed fields are not shown** on the form at all — values like the derived circuit reference are produced by the system (tracing), never typed, so they're simply absent from the create form.
- **Name** is the optional human label (§1.3.2). You can leave it blank and assign one later.

#### 6.2.5 Territory — assigned for you, or overridden

When you save a geographic feature, VoltEdge **automatically stamps it with the territory** its location falls in — the deepest matching node (§3.4). You normally don't think about this; it just happens from where you drew it. If you need to, you can **override** the territory assignment explicitly at creation (still validated to a sensible level), but the auto-assignment is right the vast majority of the time.

#### 6.2.6 Saving — it starts as a draft

Save, and your new feature is created as a **draft** (§1.5). It's yours to refine, and — if private drafts are enabled — private to you and your management chain until you publish (§6.5). Nothing is exposed to the wider organisation until you promote it (§6.4).

*Example:* Dana adds a new pad-mount transformer on a new street.
1. New-feature → **Distribution Transformer**, subtype **Pad-mount**.
2. She clicks the location; VoltEdge drops the point and stamps it into the correct zone automatically.
3. The form now requires rated kVA and voltages (because pad-mount requires them); she fills 315 kVA, 11 kV / 415 V, and picks the vector group from the dropdown. The circuit-reference field isn't shown — that'll be set when the feature is traced.
4. She saves. The transformer exists as a **draft**, ready to be wired up (Chapter 7) and published.

### 6.3 Editing an existing feature

Open a feature (Chapter 4) and use its **Edit** action. What you can change:

#### 6.3.1 Editing attributes

Change any editable field and save. Only the values you actually change are written; everything else is left exactly as it was. Required-field rules still apply — you can't blank out a value the feature's subtype requires. Computed fields remain read-only.

#### 6.3.2 Editing geometry — moving and reshaping

To correct *where* or *what shape* a feature is, use the **geometry-edit** tools:

- **Move** a point to its true position; **drag vertices** of a line or polygon to reshape it; **add or remove vertices** along a route.
- Editing a feature's geometry can **re-evaluate its territory** — move a feature across a territory boundary and its stamp updates to match its new location.
- For a **map-optional** class, geometry can also be **cleared** entirely (demoting the feature to diagram-only) — an option that exists only where the class permits it.

#### 6.3.3 Changing the subtype

A feature's **subtype is editable** — you're not stuck with the one chosen at creation. The classic case is converting an **overhead conductor to underground** (a subtype swap within the one conductor class) rather than deleting and recreating.

There's one rule to know: because a different subtype can require different fields, a subtype change is **accepted only if the feature still satisfies the new subtype's required fields.** VoltEdge checks this using everything available — the values already on the feature, any values you're setting in the same edit, and the new subtype's defaults. If together they cover the new required fields, the change goes through; if something required would be left empty, the change is refused with a clear message so you can supply the missing value. (A class with no subtypes naturally can't have its subtype changed.)

*Example:* Tom converts a span recorded as overhead to underground. The underground subtype additionally requires a `burial_depth`. Because the existing feature has no burial depth, VoltEdge blocks the swap until Tom supplies one in the same edit — then it succeeds.

#### 6.3.4 Renaming or clearing the name

The **name** is a plain optional label: set it, change it, or clear it (an empty name means "no label"). Leaving it untouched in an edit changes nothing.

### 6.4 The draft → published lifecycle

Features live as **draft** or **published** (§1.5). Promoting and demoting them:

#### 6.4.1 Publishing

**Publish** promotes a draft to the live network of record. Once published, a feature is normal shared data — visible to everyone with access to its territory, part of traces, analyses, exports, and tiles. Publish when the feature is complete and correct.

#### 6.4.2 Unpublishing

**Unpublish** pulls a published feature back out of the live network (for example if it was published prematurely). Use it sparingly — other work may reference the feature.

### 6.5 Private drafts — keeping never-published work to yourself

If your administrator has enabled private drafts (§3.5.1), a feature you create and **have never published** is automatically private to **you and your management chain**. There's nothing extra to switch on per feature — drafting *is* the privacy. Colleagues at large won't see it until you **publish** (§6.4.1), at which point it becomes shared network data.

This is what lets you build something up over days (like Dana's housing-estate survey in §3.5.4) without cluttering everyone else's map or exposing half-entered data.

### 6.6 Staged edits and deletes over published features

Separately, you can stage a **private edit or delete of a feature that is already published** — a personal working copy layered over the live record (§3.5.2). This is the tool for "I think this needs changing but I want to verify first."

How it works in the panel:

- Make your edit (or request a delete) as a **staged** change. A **banner** appears on the feature showing you have a pending private change, with **Apply** and **Discard** actions, and the feature carries a small **badge** in lists so you can find your staged work.
- **Only you** see the staged version; everyone else — including your manager — continues to see the published record unchanged.
- **Apply** commits your change to the live feature for everyone; **Discard** drops it with no trace. A staged *delete* hides the feature from *your* view while it stays live for others until you apply.

*Example:* Dana believes Feeder 7's transformer rating is wrong. She stages an edit from 500 to 315 kVA. Her panel shows 315 with an Apply/Discard banner; Tom still sees 500. After the nameplate confirms 315 on site, she clicks **Apply** and the public value updates. (If it hadn't, she'd click **Discard**.)

### 6.7 Splitting a line feature

Sometimes one recorded **line** should really be two — a single long cable that needs a joint partway, or an overhead span that a newly-installed pole now divides. **Split** cuts one line into two at a point you choose, so you don't have to delete and redraw.

⚠️ **Split applies only to line features.** There's no "split" for a point or a polygon — there's no first/second half to make — so the option appears only on line classes. It also works only on a *simple* line (a single unbranched run), not a multi-part one.

How it works, and what happens to each half:

1. **Choose where to cut.** You pick a point *along the line* to split at. The point must be in the interior — a cut on or right next to an endpoint is refused (one half would be zero-length, which isn't a split at all). In practice you click on the line where the joint or new pole sits.
2. **The original stays as the near half.** The feature you split is *shortened* to the near portion and keeps **everything else exactly as it was** — all its attributes, its connections, its relationships, and its territory are left untouched. It's still the same record, just a shorter line.
3. **The far half becomes a brand-new feature.** The far portion is created as a *new* feature, and you fill it out just like any create — its own subtype, name, and attributes — while its **geometry is computed for you** from the cut (you don't redraw it). Being a normal create, it gets its own territory assignment from where it lies, and validates against its subtype's required fields.
4. **Re-connect the new part.** Because connections stayed on the original, the new far-half feature starts unconnected; VoltEdge helps you re-connect it (the connection suggestions re-run on both halves — Chapter 7), so stitching it back into the network is quick.

You also can't split a **deleted** line — restore it first (§6.8).

*Example:* Dana installs a new pole partway along an existing 400 m LV span. She opens the span, chooses **Split**, and clicks the line at the pole. The original span shrinks to the near side (keeping its attributes and its existing connections); the far side becomes a new LV conductor she names and fills in, already sitting in the right territory. She then connects the new segment through the pole. No geometry was hand-redrawn.

### 6.8 Deleting and restoring

- **Delete** removes a feature you no longer want. Deletion is **soft** — the feature is marked deleted and drops out of the map, lists, and queries, but it isn't obliterated.
- Because deletes are soft, an accidental deletion can be **restored**. If you delete the wrong thing, it can be brought back rather than rebuilt from scratch.

⚠️ Even so, treat delete with care on published network data — other features may connect to or reference what you're removing.

### 6.9 When two people edit the same feature — concurrent-edit protection

VoltEdge guards against two people silently overwriting each other. When you open a feature to edit, VoltEdge remembers the version you started from. If someone else saves a change to that feature *before* you save yours, your save is **refused with a conflict notice** rather than quietly clobbering their work. You then re-open the feature (now showing their change) and re-apply your edit on top.

- If you see a "this feature changed since you opened it" style conflict, that's this protection working — reload and redo your change; nothing was lost.
- This only kicks in for genuinely concurrent edits to the *same* feature; ordinary editing is unaffected.

### 6.10 A note on assemblies

When what you're creating is a *composite* of several wired-together features — a pole-mount transformer kit, a packaged substation, a ring-main unit, a feeder tie — creating them one feature at a time is slow and error-prone. VoltEdge provides **guided assemblies** that build and connect the whole thing in one guided action. Because assemblies are as much about *connectivity* as creation, they're covered in Chapter 7.

### 6.11 Worked example — from empty street to published, connected asset

Follow a new distribution transformer through its whole first day:

1. **Create (draft).** Dana adds the pad-mount transformer (§6.2 example) — it exists as a private draft, correctly territory-stamped.
2. **Refine.** She notices she picked the wrong vector group, opens **Edit**, corrects the single field, and saves — only that field changes.
3. **Reshape a neighbour.** The new transformer sits mid-span on an existing LV cable, so she **splits** the cable at the transformer (§6.7), giving two cable segments to wire in.
4. **Wire it up.** Using connectivity (Chapter 7), she connects the transformer's terminals to the upstream switch and the two LV segments.
5. **Verify privately.** Unsure of the rating, she stages a **private edit** to the kVA and confirms it on site before **applying** (§6.6).
6. **Publish.** With everything correct, she **publishes** the transformer (§6.4.1). It's now live for Priya, Tom, and everyone else in that territory, and will pick up its circuit reference when the feeder is next traced.

### 6.12 Tips and gotchas

- **Pick the subtype first.** It changes which fields are required — choosing it up front saves re-doing the form.
- **You can't blank a required field.** Edits that would leave a subtype's required field empty are refused; supply the value in the same edit.
- **Geometry edits can move a feature between territories.** If you drag a feature across a boundary, expect its territory stamp — and therefore who can see it — to change.
- **Conflicts mean someone beat you to it.** A conflict notice isn't an error in your work; reload and re-apply.
- **Delete is reversible, but references aren't automatically fixed.** Prefer editing/splitting over delete-and-recreate where you can.
- **New features are private drafts (if enabled).** Don't be surprised colleagues can't see your new work until you publish.

---

---

## Chapter 7 — Connectivity & relationships

This chapter is where the map becomes a *network*. Drawing a transformer next to a cable tells VoltEdge nothing about how they're wired — connections and relationships are recorded **deliberately and explicitly**, which is exactly why VoltEdge can trace and analyse with confidence (§1.3.6). Here you'll learn to wire features together, associate them structurally, un-do both, and — the big time-saver — build whole pre-wired assemblies in one action.

🔒 Viewing needs the read permissions (`connectivity:read`, `relationships:read`), which every role from viewer up has. **Committing** — establishing or revoking — needs `connectivity:commit` / `relationships:commit`, which editors and above hold. 🗺️ You can only wire features within your territory (Chapter 3).

### 7.1 Two kinds of link, and why the difference matters

VoltEdge records two fundamentally different associations between features. Keeping them straight is the key to this chapter:

| | **Connectivity** | **Relationship** |
|---|---|---|
| Models | An **electrical** connection — current flows through it | A **structural / spatial** association — no current flows |
| Between | Two **terminals** | Two **features** |
| Example | Transformer LV terminal ↔ LV cable end | Transformer *contained by* a pad-mount enclosure |
| Feeds | Traces and electrical analyses | Grouping, context, assemblies |
| Gate to change | `connectivity:commit` | `relationships:commit` |

A simple test: **"does electricity pass through this link?"** If yes, it's connectivity. If it's "this thing sits inside / is carried by / supports that thing," it's a relationship.

### 7.2 Terminals — where connections attach

Connectivity doesn't attach to a feature as a whole; it attaches to a feature's **terminals** — its defined connection points (§1.3.6). A distribution transformer has a primary (HV) terminal and a secondary (LV) terminal; a switch has ways; a cable has ends. You'll find a feature's terminals on the **Terminals** tab of its panel (§4.2.3). When you make a connection, you're really joining *this terminal* of one feature to *that terminal* of another — which is what lets the model know current enters the transformer at the HV bushing and leaves at the LV.

### 7.3 Establishing a connection

To wire two features together electrically:

1. Start the connection from one feature's terminal (from its **Terminals** or **Connections** view).
2. Choose the **other feature and terminal** to join it to.
3. A **confirmation panel** appears summarising the connection you're about to make — the two features and the specific terminals. Confirm it.

Once committed, the connection is live: it appears on both features' **Connections** tabs (§4.2.4), and — crucially — it becomes part of the electrical graph that **traces and analyses ride on** (Chapter 8). A transformer with no committed connection to its feeder simply won't appear "fed" in a trace, however close it is on the map.

*Example:* Dana has just created a new transformer (§6.2 example). On its **Terminals** tab she starts a connection from the **HV terminal**, picks the upstream **MV switch**'s downstream way, and confirms. She repeats from the **LV terminal** to the LV board. The transformer is now electrically part of Feeder 7 — a supply-path trace from a downstream customer will now walk up through it.

### 7.4 Viewing and revoking connections

- **View** — every connection shows on the **Connections** tab of both features it joins, and each is navigable to the feature on the other end (§4.2.4).
- **Revoke** — to remove a wrong or obsolete connection, use the **revoke** action on the connection; a confirmation dialog guards the un-wiring so you don't drop a connection by accident. Revoking needs `connectivity:commit`.

⚠️ Revoking a connection changes the electrical model — a downstream section may become "unfed" in traces. Revoke deliberately, and re-trace afterwards to confirm the network looks as you intend.

### 7.5 Relationships

A **relationship** records a structural or spatial association that isn't a current-carrying connection (§1.3.7) — containment (this is inside that), support (this is carried by that), spatial support, and so on.

#### 7.5.1 Establishing a relationship — the guided kind picker

When you create a relationship you choose its **kind**, and VoltEdge is deliberately strict about which kinds make sense between two particular classes. Rather than a free-text box inviting typos, the **kind is a guided dropdown** driven by the valid kinds allowed *from the source class to the target class* (its relationship rules), directional and de-duplicated. Its behaviour adapts to how many valid kinds there are:

- **No valid kinds** between those two classes → the tool **blocks** the relationship and tells you those classes aren't meant to be related that way (with a note that forcing it would be wrong). This stops you inventing nonsensical associations.
- **Exactly one valid kind** → it's **filled in for you**, read-only — there's no choice to make, so you don't have to make one.
- **Two or more valid kinds** → you **must pick** from the dropdown; the tool won't proceed until you've chosen the right one.

(If the rules can't be loaded for some reason, the tool safely falls back to letting you type the kind, so you're never blocked outright.) Changing the target feature resets the kind, since the valid set depends on what you're relating to.

*Example:* Dana relates her new transformer to its enclosure. She picks the enclosure as the target; because a transformer-in-enclosure has exactly one sensible kind (*containment*), VoltEdge fills it in read-only and she just confirms. When she later relates a cable to a duct that supports it, two kinds are valid, so she's required to choose the correct one.

#### 7.5.2 Viewing and revoking relationships

Every relationship a feature participates in is listed on its **Relationships** tab (§4.2.5), navigable to the related feature. To remove one, each row has an **un-link (revoke)** action guarded by a confirmation dialog — the direct counterpart to revoking a connection. Revoking needs `relationships:commit`.

### 7.6 Guided assemblies — building whole pre-wired kits in one action

Most real network units aren't a single feature — a pole-mount transformer installation is a pole *supporting* a cutout, a transformer, an arrester, and an LV board, all *wired* in a particular way. Building that feature-by-feature (create pole, create transformer, create each connection, create each relationship…) is slow and easy to get wrong.

**Guided assemblies** solve this: one guided action **creates all the features and makes all their connections and relationships together**, correctly, as a single unit. 🔒 Because an assembly both creates features *and* commits connectivity and relationships, it needs the corresponding permissions together (`features:write` plus `connectivity:commit` and `relationships:commit`) — i.e. an editor.

#### 7.6.1 The assemblies available

You reach them from the map's **Assemblies** menu. The built kits include:

- **Ring-main unit (RMU)** — an RMU built as a star of switch-ways around the unit.
- **Packaged / movable substation** — a chain of MV switch → distribution transformer → LV busbar, wired in series, as one packaged unit (permanent or mobile).
- **Pole-mount transformer kit** — a pole *supporting* a cutout, transformer, arrester, and LV board, wired appropriately.
- **Pad-mount / URD kit** — a pad-mount transformer with its LV link box.
- **Duct line** — a run of duct as a line assembly.
- **Bus coupler** — a coupler tying two busbars.
- **Feeder tie (normally-open)** — a cross-feeder tie (see §7.6.3).

Each is driven by a template that knows the kit's structure — which feature is the structural parent, which pieces it holds, and how they wire together — so the result is consistent every time.

#### 7.6.2 Using an assembly

1. Open the **Assemblies** menu and pick the kit.
2. Place / point it on the map and provide the guided inputs it asks for (the pieces and any needed attributes).
3. Confirm. VoltEdge creates every feature in the kit and establishes all its internal connections and relationships in one go.

The result is a set of features that are already wired to each other; you then connect the *whole unit* into the surrounding network (e.g. the packaged substation's incoming MV to the feeder) as normal (§7.3).

#### 7.6.3 The feeder tie (normally-open) in particular

A **feeder tie** is a common, specific composite: a one-click normally-open link between **two different feeders**, via a switch. VoltEdge creates the tie switch and the two connections (feeder A → switch, switch → feeder B) and **forces the switch to normally-open / open** — because a tie exists to be *closed only during switching*, not to parallel two feeders in normal operation. This is the cross-feeder counterpart to the bus coupler (which ties two busbars on the same site). Ties are what contingency and back-feed studies (Chapter 8) reason about.

*Example:* To give Feeder 7 a back-feed option from Feeder 9, Tom uses **Feeder tie (NOP)**, picks the two feeders and the tie point; VoltEdge creates the normally-open switch already wired between them. In day-to-day traces the tie stays open (Feeder 7 isn't fed from Feeder 9), but a contingency analysis can now consider closing it if Feeder 7's own source is lost.

### 7.7 Worked example — wiring a new transformer into the network, end to end

Bringing Chapters 6 and 7 together, here is the connectivity half of Dana's new-transformer job (continuing §6.11):

1. **The pieces exist.** Dana created the pad-mount transformer (§6.2) and split the LV cable into two segments at it (§6.7).
2. **Wire the incoming.** From the transformer's **HV terminal** she establishes a connection to the upstream MV switch and confirms (§7.3).
3. **Wire the outgoing.** From the **LV terminal** she connects to each of the two LV cable segments the split produced — the new far-half segment started unconnected, so this is where it rejoins the network.
4. **Record the structure.** She relates the transformer to its **pad-mount enclosure** (*containment*, filled in automatically — §7.5.1).
5. **Verify.** A supply-path trace (Chapter 8) from a customer downstream now walks up through the transformer to the source — proof the wiring is right. Had she missed a connection, the trace would stop short, pinpointing the gap.
6. **Publish** the transformer (§6.4) now that it's correctly wired.

Had this been a standard pole-mount installation rather than a single transformer, steps 1–4 would have collapsed into one **Assembly** action.

### 7.8 Tips and gotchas

- **"Touching" is not "connected."** Two features overlapping on the map are electrically unrelated until you *establish* a connection. If a trace doesn't reach something, a missing connection is the first suspect.
- **Connect terminals, not features.** Always connect the *right* terminals — HV to the upstream side, LV to the downstream side — or your traces and analyses will be wrong even though a connection exists.
- **Let the kind picker guide you.** If the relationship kind dropdown blocks you, those two classes genuinely aren't meant to relate that way — that's the system protecting your data model, not a bug.
- **Use assemblies for anything multi-part.** They're faster *and* they can't forget an internal connection the way hand-wiring can.
- **A tie is meant to be open.** Don't "fix" a feeder tie that shows as open/normally-open — that's its whole purpose. Its closure is a *switching* action, and its value is analysed, not left closed.
- **Re-trace after revoking.** Removing a connection or relationship can quietly change what's fed; confirm with a trace afterwards.

---

---

## Chapter 8 — Tracing & network analysis

This is where VoltEdge pays back the effort of building an explicit, connected model (Chapter 7). Once features are wired, VoltEdge can **walk the network** to answer topology questions (traces) and **compute its electrical behaviour** (analyses). 🔒 Traces need `trace:run` and analyses need `analysis:run` — both held by the **analyst** role and above (Chapter 3). 🗺️ They operate over the network you can see, within your territory.

Everything in this chapter rides on connectivity: a trace can only follow connections that have been *established*, and an analysis can only compute over a model that's *wired and attributed*. If results look wrong, the model — not the maths — is almost always the reason (§8.6).

### 8.1 Traces versus analyses — two different questions

| | **Trace** | **Analysis** |
|---|---|---|
| Answers | "**What is connected to what** — upstream, downstream, isolatable?" | "**How does it behave electrically** — voltages, fault currents, capacity?" |
| Produces | A *set of features* (a path or region) | *Numbers* (per-node voltages, currents, limits) |
| Depends on | Connectivity + device open/closed state | Connectivity + electrical attributes (ratings, impedances, loads) |
| Permission | `trace:run` | `analysis:run` |

A trace is topological — it follows the wiring. An analysis is quantitative — it solves the physics over that wiring. You often trace *first* to understand a region, then analyse it.

### 8.2 Where you launch them, and how the menu is built

Both traces and analyses live on the feature panel's **Trace / analysis** menu — the button with the *route* icon and a small down-chevron in the panel header (§4.2.1). You always start *from a feature*: the one whose panel is open becomes the trace's or analysis's origin. Each result opens in its own **floating panel** you can drag and resize (§2.8).

The menu is assembled top-to-bottom like this:

1. **Named trace configurations** — the traces your administrator has defined (see §8.3.1). These are *not* hard-coded; VoltEdge lists the ones that are actually **runnable from the feature you have open**. If none exist for your installation you'll see *"No trace configurations exist"* here.
2. **Feeder trace** and **Section trace** — the two "what's in this stretch / on this feeder?" traces (§8.3.5), shown when the feature supports them.
3. **Isolate from here** — the switching-plan trace (§8.3.4), shown when the feature is a switching device.
4. **Supply path (to source)** — always present (§8.3.3).
5. **The numeric analyses**, grouped into three cascading sub-menus — *Load flow*, *Fault & protection*, and *Capacity & planning* (§8.4) — so the list stays scannable as the analysis suite grows.

💡 The whole menu is disabled while a trace or analysis is already running from that panel — VoltEdge runs one at a time from a given starting feature, so let the current one finish (or close its panel) before launching another.

### 8.3 Traces — walking the network

A **trace** starts at a feature and walks the connectivity graph (Chapter 7) to gather the features that are topologically related to it. A trace is **read-only** — it never changes anything, it only reports what the connectivity you've already established implies. Everything below rides on that connectivity: a trace can only follow connections that have actually been *established*.

#### 8.3.1 Configurable named traces — the heart of the trace menu

The top of the menu is populated from **trace configurations** — named trace "profiles" your administrator defines centrally (things like *Trace downstream*, *Trace upstream*, *Trace connected*, or any custom walk your utility needs). This is a deliberate design choice: rather than shipping a fixed list of trace buttons, VoltEdge lets the set of available traces be **configured as data**, so new trace types appear in everyone's menu without a code change.

Two things follow from this:

- **You only see the traces that make sense from where you are.** VoltEdge filters the configured traces down to those *runnable from the open feature*. Start on a source and you'll see the downstream walks; start on a leaf and the menu reflects that. If a trace you expected is missing, it's usually because it doesn't apply to that feature type.
- **The exact names are your installation's.** The examples in this handbook (downstream, upstream, supply-path) are the common ones, but your menu shows whatever your administrator has named and enabled.

Pick a configuration and VoltEdge runs that named walk from your feature, returning the gathered features (§8.3.6).

*Example:* Dana opens a segment of MV cable on Feeder 7 and sees **Trace downstream** and **Trace upstream** in the menu. Running **Trace downstream** gathers everything the cable feeds from that point onward; **Trace upstream** gathers everything back toward the source. Priya, opening a service point at the very end of an LV run, sees the same menu but the downstream trace returns almost nothing — there's little past a service — which is itself the answer.

#### 8.3.2 Barriers — why a trace stops where it does

A trace doesn't run forever; it stops at **barriers**. The most important barrier is an **open device**: an open switch, breaker, or tie breaks electrical continuity, so a trace won't cross it. This is what makes traces *operationally* meaningful — they reflect the network *as it is actually switched right now*, not merely as it's built.

- The boundary devices a trace respects are the **controllers** — switches and similar operable devices — so opening a switch genuinely bounds an isolation or section trace.
- A feature's **operational state** (in-service, isolated, etc.) also shapes what a trace crosses.

⚠️ A trace that "stops early" is almost always *correct* — it's reflecting a real open point. Before assuming the trace is broken, check the **device states** along the path; a forgotten open switch is the usual explanation.

#### 8.3.3 Supply-path trace — "what feeds this?"

The **Supply path (to source)** trace answers the everyday question "**where does this point get its power from?**" Starting at a consumption point (a service, a downstream device), it walks **upstream, barrier-free**, and lays out the whole feeding chain in order:

> LV switching devices → the **serving distribution transformer** → the MV devices → any **power transformer** → the **source**.

It deliberately ignores open-device barriers on the way up, because the question is "what *supplies* this?" — the physical feeding chain — not "what's energised right now." Along the way it bridges through transformers so the chain reads as one continuous path from the customer to the grid source.

*Example:* A customer on Feeder 7 reports low voltage. Tom opens their service point and runs **Supply path (to source)**. He gets the ordered chain: service → LV cable → distribution transformer **TX-Feeder7-014 (315 kVA, 11 kV/415 V)** → MV switch → … → source. In seconds he knows every asset in that customer's supply and exactly where to look next — and he can feed that same feeder straight into a **Voltage profile** analysis (§8.5).

#### 8.3.4 Isolation trace — "what must I open to make this dead?"

**Isolate from here** finds the devices you'd operate to **de-energise** a chosen switching device — the isolating boundary around it, plus the set that goes dead. It's the switching-planning question: "to work safely here, which switches must be opened, and what loses supply when I do?"

Under the hood VoltEdge composes this from **two ordinary traces** — a downstream walk (what goes dead) and an upstream walk (the protective boundary that must be opened) — and reports both together. Because it respects controllers as barriers, the boundary it returns is the real set of operable devices, not an idealised one.

💡 **Isolate from here** only appears when the open feature is a switching device — isolation is defined *around a device you can operate*. Open a switch to plan a job on the cable just downstream of it.

#### 8.3.5 Section and feeder traces — "what's in this stretch / on this feeder?"

- A **Section trace** gathers the run of network between the nearest bounding devices **upstream and downstream** of your feature — the electrical "section" it sits in. This is the "what shares a fate with this cable, out to the next switches either side?" question.
- A **Feeder trace** gathers the **whole feeder** a feature belongs to, *regardless of where along it you start*: VoltEdge first finds the network root(s) upstream, then walks downstream from there to collect the entire feeder. So you get the same feeder whether you start at its head or a spur near its tail.

Both answer "what's affected?" questions — the extent of network that shares a fate with your starting feature — at two different scales (a section versus the entire feeder).

#### 8.3.6 Reading trace results

A trace opens a **results table** listing every feature it gathered, in a floating panel you can drag and resize (§2.8). From the table you can navigate to each feature, and the trace **highlights its features on the map**, so you grasp the path or region geographically as well as in the list. Use the table to review the members and the map highlight to see the shape. 🗺️ A trace only ever returns features **within your territory** — out-of-scope assets simply aren't part of the result (Chapter 3).

### 8.4 Network analyses — computing the electrical behaviour

Where a trace *gathers features*, an **analysis** *solves for numbers* over the connected model. There are **twelve** analyses, all read-only, grouped into the three sub-menus described below. This section documents every one: what question it answers, what it needs from the model, and what it gives back.

#### 8.4.1 What an analysis needs to be meaningful

An analysis is only as good as the model beneath it. For trustworthy results the relevant features need:

- **Correct connectivity** (Chapter 7) — every analysis solves over the *wired* graph. A missing connection silently truncates the network the analysis can see.
- **The electrical attributes it reads** — transformer ratings and impedances, conductor specs (resistance, ampacity), source parameters, loads, generator nameplates. Missing or wrong values produce missing or wrong results. Where VoltEdge *must* assume a value it uses a sensible standard one and **tells you it did**, in the explanation strip (§8.4.6).

Analyses are **mutually exclusive on the map** — you run one at a time, and its panel shows that run's results. Running a new one replaces the last.

#### 8.4.2 Family 1 — Load flow: "what are the voltages and flows?"

This family solves how power flows and what voltage every node sees under load. There are five entries, from the simplest radial solve up to a full nodal engine, plus an automatic router.

- **Load flow (auto)…** — the one to reach for when unsure. It **classifies the topology once** and auto-selects the right engine: a radial tree goes to the fast ladder solver, a few loops go to the mesh-compensation solver, and a dense mesh (or a request needing multiple sources / regulated nodes) goes to the nodal Y-bus solver. The result **tells you which engine actually ran**, and you can force a specific one if you have a reason to.
- **Load flow (radial)…** — the base **forward-backward sweep** for a tree-shaped feeder. Returns **per-node voltage** and **per-branch loading**. Fast and exact for radial networks (the overwhelming majority of distribution feeders).
- **Voltage profile…** — runs the same radial sweep but reports it as a **per-node per-unit voltage** against the **ANSI C84.1 band (0.95–1.05 pu)**, plus a **series I²R loss** summary for the feeder. This is the go-to for "is everyone in-spec, and where is it worst?" It names the worst node and the loss hotspots.
- **Meshed load flow…** — for **weakly-meshed LV** (a ring main, a closed link box). It solves a radial spanning tree first, then **closes each loop by compensation** so the circulating flow a purely radial solve can't represent is accounted for. Returns the same per-node voltage/loading as load flow, plus the loops it closed.
- **Nodal load flow…** — a **distinct nodal (Y-bus) engine** for general, denser networks. It assembles a per-unit **admittance matrix over the whole graph** and solves it (Gauss-Seidel by default, with an optional Newton-Raphson solver for tougher cases), so a densely meshed LV network is **solved directly rather than refused**. Handles a slack source and PQ buses, with loads and embedded generation modelled as negative-demand injections.

💡 **Which load-flow should I pick?** Use **auto** by default. Choose **radial** or **voltage profile** for an ordinary feeder when you want the plain sweep or the ANSI check specifically; **meshed** when you know you've closed a ring; **nodal** only for genuinely dense/multi-source networks or when you want the Newton solver.

#### 8.4.3 Family 2 — Fault & protection: "what happens under fault, and does protection clear it?"

- **Fault current…** — accumulates the **series impedance from a source** down the network and returns the **symmetrical bolted-fault current at every downstream node** (three-phase). Ask for a **single-line-to-ground** fault (`slg`) and it additionally builds the **zero-sequence network** and returns the SLG fault current per node. Where **embedded generation** is present, generators act as a **second fault source** and their infeed is superposed onto the grid contribution — the result splits out the grid vs generator contribution.
- **Coordination…** — grades the **time-overcurrent protective devices on the source→fault path** for a fault at a chosen node. For each backup/primary pair it checks the **backup clears its primary by at least the coordination time interval (CTI)**, so you can see where grading is tight or fails.
- **Run ampacity…** — first-principles **IEEE 738 steady-state thermal ampacity** for **one conductor feature**. Given the conductor's specification (and weather/loading assumptions), it computes the current the conductor can carry at its rated temperature. Use it to sanity-check a cable or line against the loading a load-flow reports on it. *(This is the analysis the earlier draft of this chapter omitted — it sits here under Fault & protection.)*
- **Arc-flash…** *(where surfaced in your build)* — a **derived** IEEE 1584 study that reuses the fault engine (bolted fault current and voltage) and the coordination engine's clearing-time evaluators to compute **incident energy (cal/cm²)**, the **arc-flash boundary**, and the **NFPA 70E PPE category** per node. It uses declared equipment geometry (electrode configuration, gap, working distance, grounding) where those fields are populated, and standard IEEE-typical geometry otherwise — and says which it used.

#### 8.4.4 Family 3 — Capacity & planning: "how much more can it take, and what if…?"

- **Contingency…** — an **N-1 / back-feed restoration** study. For each **normally-open tie** on a feeder it simulates **closing the tie** and reports the **outaged load the donor feeder would pick up**, plus any **post-transfer voltage violation or thermal overload**. Restoration options are ranked around the available ties, so "could we back-feed this?" becomes a concrete, checked answer.
- **Hosting capacity…** — a **DER hosting** study. For a node (or **every interconnection node on a feeder**) it **binary-searches the largest generator injection** the network can accept before the **first over-voltage or thermal overload binds**, and names which constraint bound and where. The DER is modelled as injected power and the network re-solved at each step of the search.
- **EV headroom…** — the **under-voltage mirror** of hosting capacity. For a node (or every connection node on a feeder) it binary-searches the **largest additional charging load** the feeder can absorb before the **first under-voltage (0.95 pu floor) or thermal overload binds**. Use it to answer "how much more EV charging can this feeder take?"

#### 8.4.5 Reading an analysis result

Each analysis opens a **result panel** (draggable and resizable — §2.8) with its numbers: typically **per-node figures** with the **worst/binding point highlighted**, and a **headline outcome** — in-spec or not, the fault level in kA, capacity in kW, the PPE category, and so on. Where it's meaningful the result is also reflected on the **map** (e.g. impact halos, highlighted nodes).

#### 8.4.6 "How this was calculated" — the explanation strip

Every analysis panel carries a collapsible **"How this was calculated"** strip at the bottom, collapsed by default. Expand it and VoltEdge explains **this specific run** in plain language built from its real figures — the **worst node**, the **solver/engine used**, the **clearing device**, the **binding constraint**, and any **assumptions it had to make** (a standard weather value for ampacity, IEEE-typical geometry for arc-flash, and so on). It exists so a result is never a black box: you can see *why* the number is what it is and which input drove it. Open it for any result you intend to act on — it names exactly what you'd need to defend or challenge the figure.

### 8.5 Worked examples

**A low-voltage complaint, start to finish (Tom, analyst):**
1. **Trace.** From the complaining customer's service point on Feeder 7, Tom runs **Supply path (to source)** and reads the chain up to the source, confirming TX-Feeder7-014 serves them.
2. **Analyse.** He runs **Voltage profile** on that feeder. The customer's node comes back at **0.93 pu** — below the 0.95 ANSI floor — and it's flagged as the worst node.
3. **Check the conductor.** Suspecting the final LV run, he opens that cable and runs **Run ampacity**; the segment's rating is fine, so the problem is voltage drop, not thermal.
4. **Explain.** He expands **How this was calculated**: the binding constraint is that LV cable's impedance under peak load, with losses concentrated on the final segment.
5. **Act.** He now has an evidence-backed case — upgrade or rebalance that segment — instead of a guess.

**A DER connection request (Tom, analyst):**
1. A customer asks to connect rooftop solar at a point on Feeder 7.
2. Tom runs **Hosting capacity** at that point. Result: **45 kW** before the binding constraint — **overvoltage at midday** — is hit.
3. The explanation strip confirms *overvoltage*, not *thermal*, is the limit — so a voltage-management measure (not a reconductor) is the cheaper way to let the customer have more, if they want it.

**Planning a back-feed (Dana, editor + Tom, analyst):**
1. Dana has built a normally-open tie from Feeder 7 to Feeder 9 (§7.6.3).
2. Tom runs **Contingency** assuming Feeder 7's source is lost. The study ranks **closing the Feeder 9 tie** as a viable restoration path and reports the load Feeder 9 would pick up — flagging whether that transfer would overload Feeder 9 or push any node out of band.
3. "Could we back-feed?" is now a checked answer with the numbers to back it.

**A protection check (Tom, analyst):**
1. Tom runs **Fault current** (three-phase) at a mid-feeder node to get the available fault level, then asks for the **`slg`** variant to get the single-line-to-ground level too.
2. He runs **Coordination** for a fault at the same node. The result shows each device on the source→fault path and whether each backup clears its primary by at least the CTI — pinpointing a pair where grading is marginal.

### 8.6 Tips and gotchas

- **Garbage in, garbage out.** An analysis over an incomplete or mis-wired model gives confident-looking *wrong* numbers. If a result seems off, audit the **connectivity** (Chapter 7) and the **attributes** (ratings, impedances, loads) before doubting the engine.
- **Traces are only as complete as your connectivity.** A supply-path or feeder trace that misses an asset usually means a **connection was never established**, not that the trace failed.
- **Open devices bound traces — on purpose.** A trace that stops early is normally reflecting a genuinely open switch: the correct answer for the network *as switched*.
- **Your menu is your installation's.** The named traces at the top come from your administrator's **trace configurations** and are filtered to what's runnable from the open feature — so two different features can show two different trace lists.
- **Use Load flow (auto) unless you have a reason not to.** It reads the topology and picks the right engine, and tells you which one it ran.
- **Read the explanation strip.** For any result you'll act on, expand **How this was calculated** — it names the binding constraint, the engine, and the assumptions, which is exactly what you need to defend or challenge the number.
- **One at a time.** Analyses are mutually exclusive on the map, and the trace/analysis menu is disabled while one is running from that panel — let it finish or close its panel first.
- **Re-run after model changes.** If you edit connectivity or attributes (Chapters 6–7), any open trace/analysis panels are now stale — run them again.
- 🗺️ **Scope still applies.** Traces and analyses only ever see features **within your territory** (Chapter 3); an out-of-scope asset is invisible to them just as it is on the map.

---

*Next chapter: **Operations modules** — Outage Management (OMS), Work & Asset Management (WAM), and the Faults module, and how each surfaces on the map. Tell me to proceed.*

---

## Chapter 9 — Operations modules (OMS, WAM, Faults)

Chapters 4–8 were about the network *as a model* — the assets, how they're wired, and what the physics says. This chapter is about **what happens to that network over time**: the lights go out and a crew is dispatched (OMS), an inspector finds a cracked bushing and a repair is scheduled (WAM), a cable faults to earth and a protective device trips (Faults). These three **operations modules** record the *operational life* of the network, and they're wired together so that one real-world event — a storm, a failure — flows through all three as a single connected story.

### 9.1 The shape of an operations module — shared ideas first

Before the three modules individually, four ideas they all share. Grasp these once and each module reads the same way.

#### 9.1.1 Operational records are not map features

Everything in Chapters 4–7 was a **GIS feature** — a transformer, a cable — compiled from metadata and drawn on the map. The records in this chapter are different: an outage, a defect, a work order, a fault event are **operational transactions *about*** those features, not features themselves. They live in their own database areas (OMS, WAM, Faults each own a separate schema, deliberately outside the feature compiler) and they behave differently:

- They **won't appear in the Features browser** (Chapter 4) or the query engine (Chapter 5) — those are for GIS features.
- They each have their **own page** in the left sidebar, and their **own read/write permissions**.
- They **attach to assets** by reference — an "asset class + asset id" pointer — rather than being drawn as geometry. This is why they can surface *on* a feature (in its panel) without *being* a feature.

💡 The practical upshot: to see the network, use the map and the Features browser; to see what's *happening to* it, use the three pages this chapter covers — or the operational tabs on a feature's panel (§4.2.9).

#### 9.1.2 Three pages, three permission pairs

Each module is one sidebar entry, gated by its **read** permission, and each write action needs the matching **write** permission (Chapter 3):

| Module | Sidebar | Icon | Read 🔒 | Write 🔒 |
|---|---|---|---|---|
| Outage Management (OMS) | **Outages** | siren | `oms:read` | `oms:write` |
| Faults | **Faults** | zap-off | `faults:read` | `faults:write` |
| Work & Asset Management (WAM) | **Works** | wrench | `wam:read` | `wam:write` |

If you don't hold a module's `…:read`, its sidebar entry simply isn't there for you (§3.6). Holding read but not write means you can *see* the boards but the create/act buttons are disabled — the familiar read-only pattern from Chapter 4.

#### 9.1.3 Every record has a lifecycle (a status that only moves forward)

Each record type walks a defined **status lifecycle**, and the buttons you see depend on where it is. You'll meet each module's lifecycle below, but the pattern is always the same: a record is *created* in an early status, *advances* through operational states as work happens, and *ends* in a terminal one (restored, resolved, closed, cancelled). Knowing the lifecycle tells you what's expected next.

#### 9.1.4 The operations triangle — how the three interlock

The three modules aren't silos. A **fault** is the causal hinge between the other two:

> A **fault event** *explains* an **outage** (the incident it caused) and *triggers* a **defect** (the asset failure booked for repair).

So a single failure can become: an **OMS incident** (customers are dark) ↔ a **Faults event** (the earth fault that caused it) ↔ a **WAM defect** (the damaged asset to fix) → a **WAM work order** (the crew job). VoltEdge provides one-click composites to create these links, so you don't re-key the same event three times. §9.5 walks a full storm-to-repair example through the whole triangle; each module section below shows its own corner.

### 9.2 Outage Management (OMS) — the "Outages" page

🔒 `oms:read` to view, `oms:write` to act. OMS answers "**who's off supply, why, and when will they be back?**" It's a **pure operational overlay** — nothing OMS does changes the live network model or a device's real state; it *records and predicts* outages alongside the network.

#### 9.2.1 Trouble calls — the intake

A **trouble call** is one customer report of lost or degraded supply. Three types capture what they're reporting:

- **No supply** — completely dark.
- **Partial** — some supply, e.g. one phase.
- **Flicker** — momentary interruptions.

A call can reference a **known service point** *or* carry only a **raw address / dropped map location** — intake shouldn't stall because the caller isn't matched to an asset yet. A call walks **open → associated** (linked to an incident) **→ cleared** (customer confirmed back, or false alarm), or **cancelled**. Only a call resolved to a real service point feeds the prediction rollup (§9.2.3).

*Example:* During a squall, **Priya** (dispatcher) logs three "no supply" calls on the Outages page. Two match service points on Feeder 7; the third is just an address she pins on the map. All three sit at **open**, waiting to be rolled into an incident.

#### 9.2.2 Incidents — the outage itself

An **incident** is one outage — inferred by the engine or raised by hand. Key fields:

- **Type** — **unplanned** (a failure) or **planned** (a booked interruption, §9.2.6).
- **Predicted device** — the protective device the engine *blames* for the outage (a feature class + id), with a **confidence** score (0–1) when it was inferred; blank when a human raised it directly.
- **ETR** — estimated time to restore, to communicate to customers.
- **Affected customers / service points** — the scale of the outage.
- **Crew reference** — who's dispatched.

Its lifecycle: **predicted → confirmed → dispatched → restored** (planned outages start at **scheduled**), with **cancelled** available throughout. `restored_at` empty means still out.

#### 9.2.3 The prediction rollup — blaming a device

This is OMS's cleverest move. Rather than making a dispatcher guess which device tripped, the **prediction rollup** takes the open trouble calls, walks *upstream* from each affected service point, and finds the **common protective device** whose operation would explain them all — then writes a single **predicted** incident blaming that device, with a confidence score.

Crucially, prediction **touches nothing in the live network** — it doesn't open a switch or change a device state; it only writes the OMS overlay. A human still has to confirm it.

*Example (continued):* Priya runs **Predict**. VoltEdge groups her two matched Feeder 7 calls and blames the upstream recloser **REC-F7-03** at confidence 0.86, creating one predicted incident of ~120 affected customers — instead of Priya eyeballing the map.

#### 9.2.4 Confirm, dispatch, restore

From a predicted incident Priya can **confirm** it (a real outage, not noise), track **dispatch**, and finally **restore** it when supply is back — which stamps the restore time and starts the clock ticking on the reliability figures (§9.2.8). **Cancel** handles a false prediction. Each action is one button on the incident, gated by `oms:write`.

#### 9.2.5 Affected area — who's downstream

For any incident, **Affected area** computes the set of downstream service points and customers the blamed device feeds — the blast radius of the outage. It's the same downstream logic as a trace (Chapter 8), applied to "who loses power if this device is open."

#### 9.2.6 Planned outages

Not every outage is a surprise. A **planned outage** is booked ahead with a **planned start/end** window; it enters at **scheduled** and is worked like any incident, but it's the switching program (next) that makes it safe to execute.

#### 9.2.7 Switching orders — the crew's program

A **switching order** is the ordered **open/close program** a crew executes to isolate an incident's work area and later restore it. It walks **draft → issued → in_progress → completed** (or cancelled). Each **step** has:

- a **phase** — **isolate** (make the work area dead) or **restore** (bring it back), and
- an **action** — **open**, **close**, **earth**, **verify**, or **note**, against a named device (verify/note steps carry no device).

VoltEdge can **generate** a switching order for an incident, and the operator **executes each step** in turn (stamping when it's done). This turns "which switches, in what order?" into a checked, auditable program.

*Example:* For a planned Feeder 7 job, Priya generates a switching order: *step 1 — open REC-F7-03 (isolate); step 2 — earth the work area (isolate); … step 8 — remove earths, close REC-F7-03 (restore)*. The crew ticks each off as they go.

#### 9.2.8 Reliability indices — SAIDI, SAIFI, CAIDI, ENS

OMS rolls its restored incidents up into the standard distribution **reliability indices** — SAIDI, SAIFI, CAIDI, and Energy Not Supplied — computed from the per-service-point interruptions each incident recorded. To keep history honest, the **customer count is frozen** at interruption time, so later re-modelling of a service point can't retroactively change a past index. 🔒 This is the number regulators and planners (Tom) live by; find it on the Outages page's indices view.

#### 9.2.9 OMS on the map

Outages aren't drawn as features, but they surface on the map: an **outage-highlight banner** appears when you focus an incident, and its **affected area** highlights the downstream network. From an incident you can **Show on map** to jump to the blamed device — the same pattern the Features browser uses (§4.3).

### 9.3 Faults — the "Faults" page

🔒 `faults:read` / `faults:write`. A **fault event** is an **electrical fault on the network** — an earth fault, a phase-to-phase short, a protection operation. It's not a GIS feature; it's the **causal hinge** of the operations triangle (§9.1.4).

#### 9.3.1 What a fault event captures

- **Fault type** — the electrical classification the analysis engines already speak: **SLG** (single-line-to-ground), **LL** (line-to-line), **LLG** (double-line-to-ground), **LLL** / **LLLG** (three-phase / -to-ground), a generic **earth**, or **unknown** until classified.
- **Fault current (A)** and **clearing time (ms)** — what actually flowed and how long it took to clear (these tie back to the Fault-current and Coordination analyses of Chapter 8).
- **Protective device** — the device that operated to clear it.
- **Affected asset** — the feature the fault occurred on (optional; a fault known only by its incident may carry none).

Its lifecycle: **detected → investigating → cleared** (off the network) **→ closed** (follow-up wrapped up). `cleared_at` empty means still on the network.

#### 9.3.2 Logging a fault — two ways in

- **From scratch** on the Faults board — log a fault you know about, picking the asset with the asset-ID picker (backed by the cross-class feature registry, so you can name any feature).
- **From an outage** — on the Outages page an incident carries a **Raise fault** button that mints a fault event already linked to that incident (the *fault explains outage* edge). No re-keying.

#### 9.3.3 From fault to defect — booking the repair

When a fault reveals damaged plant, **Book defect** on the fault mints a **WAM defect** and links it back to the fault (the *fault triggers defect* edge). The single failure now exists as a fault *and* a scheduled repair, connected — and it de-duplicates, so booking twice won't create two defects.

#### 9.3.4 History — the audit trail

Every fault carries a **history** view: an append-only trail of what changed and when (status transitions, links made). For an event that may be reviewed after the fact, that provenance matters.

#### 9.3.5 Faults on the map and on features

Faults have a proper **map overlay**: status-coloured, clickable markers at each fault's location, with **impact halos** showing the affected area (reusing the OMS impact plumbing). Toggle it from the **Faults row in the layers panel** (§2.6). And every asset's feature panel has a **Faults tab** (§4.2.9) listing the fault events recorded against *that* equipment — the operational history of the piece, right beside its attributes.

*Example:* **Tom** (analyst) opens the fault behind Priya's Feeder 7 outage. It's an **SLG** at ~4.2 kA, cleared by REC-F7-03 in 240 ms. He confirms the cause was a failed surge arrester, hits **Book defect**, and the arrester now has a WAM repair queued — outage, fault, and defect all linked.

### 9.4 Work & Asset Management (WAM) — the "Works" page

🔒 `wam:read` / `wam:write`. WAM answers "**what's wrong with our assets, and what work is planned to fix or maintain them?**" It has three parts — **defects** (what's wrong), **work orders** (the jobs), and **inspections** (the checklists that find problems) — and they form a self-feeding loop.

#### 9.4.1 Defects — "what is wrong"

A **defect** is one asset-health finding, attached to a feature. It carries:

- **Severity** — **low / medium / high / critical**, driving triage.
- **Source** — how it was found: **inspection**, **outage**, **condition monitoring**, or **ad hoc**.
- **Status** — **open → work_ordered** (a corrective job was raised) **→ resolved**, or **dismissed**.

From an open defect, **Raise work order** creates the corrective job that will fix it (next). A defect can also link back to the **OMS incident** it explains (when its source is an outage) — another edge of the triangle.

#### 9.4.2 Work orders — one typed engine

WAM deliberately uses **one work-order engine** for every kind of job, distinguished by **work type**:

- **inspection** — go and run a checklist,
- **corrective** — fix a known defect,
- **preventive** — scheduled maintenance,
- **construction** — build/install work.

All four share one lifecycle — **draft → scheduled → assigned → in_progress → completed** (or cancelled) — and one **priority** scale (**low / medium / high / urgent**). A work order can be **assigned to an internal field worker** (picked from the assignable-users list) *or* labelled with a **free-text external crew**, and carries a **scheduled-for** date and a **due-by** date. A corrective order links to the **defect** it addresses; when completed you record **completion notes**.

#### 9.4.3 Inspection templates — reusable checklists

An **inspection template** is a named, reusable checklist you build once and run many times. It has a stable code, an optional **asset-class scope** (so a "pole-mounted transformer" checklist only offers itself for those), an **active** toggle, and an ordered list of **items**. Each item captures its answer as one of four **response types**:

- **pass/fail**, **rating**, **numeric**, or **free text**.

The powerful part: an item can carry a **fail severity**. If an inspector marks that item *failing*, VoltEdge **auto-raises a defect** at that severity — so a checklist doesn't just record a problem, it *starts the repair loop*.

#### 9.4.4 Recording an inspection — closing the loop

An **inspection** is one execution of a template against an asset, usually while completing an **inspection** work order. The inspector answers each item; the result rolls up to an overall **pass / fail / conditional**. Any failing, severity-bearing item **spawns a defect automatically** — which can then be triaged and turned into a **corrective work order** (§9.4.1–2). That's the WAM loop: *inspection → defect → corrective work order → resolved.* Item answers are **snapshotted** onto the record, so evolving the template later can't rewrite history.

#### 9.4.5 WAM on the feature panel — the "Works" tab

Every asset's feature panel has a **Works tab** (§4.2.9) showing that asset's **defects** and its **inspection history** (read-only there — you record new inspections from the Works page against a work order). So standing on a transformer you can see, in one place, its faults (Faults tab) *and* its defects and inspections (Works tab).

*Example:* **Dana** (field supervisor) runs the "Distribution transformer – annual" template against TX-Feeder7-014. The item *"Surge arrester intact?"* is marked **fail** (severity: high). VoltEdge auto-raises a **high** defect on the transformer. Back at the desk Dana triages it, hits **Raise work order** → a **corrective**, **high**-priority order assigned to a crew, due next week. When they fix it, the order is **completed** and the defect **resolved** — the whole loop captured.

### 9.5 The operations triangle end-to-end

Putting §§9.2–9.4 together as one story — the same Feeder 7 failure through all three modules:

1. **Calls (OMS).** Customers ring in; Priya logs trouble calls.
2. **Predict (OMS).** The rollup blames recloser REC-F7-03 and writes a predicted **incident** (~120 customers). Priya **confirms** and dispatches a crew.
3. **Fault (Faults).** From the incident, Priya hits **Raise fault** → a **fault event** linked to the outage. Tom classifies it (SLG, 4.2 kA, cleared 240 ms) and diagnoses a failed arrester.
4. **Defect (Faults → WAM).** Tom hits **Book defect** on the fault → a WAM **defect** on the arrester, linked to both fault and outage.
5. **Repair (WAM).** Dana raises a **corrective work order** from the defect, assigns a crew, and completes it — **resolving** the defect.
6. **Restore + measure (OMS).** Supply is back; Priya **restores** the incident, and it flows into the **reliability indices** Tom reports on.

One failure, one connected record — no corner re-keyed. That connectedness is the whole point of keeping the three modules linked rather than separate spreadsheets.

### 9.6 Tips and gotchas

- **These aren't map features.** Don't look for outages, defects, or faults in the Features browser or the query engine — they have their own pages and their own permissions.
- **Each module has its own permission pair.** You might hold `oms:read` but not `wam:write`; the sidebar and buttons reflect exactly what you hold (§3.6).
- **Prediction changes nothing live.** The OMS rollup writes an overlay incident; it never opens a switch or alters a device's real state. A human confirms and acts.
- **Use the composites, don't re-key.** Raise-fault-from-outage and book-defect-from-fault (and raise-work-order-from-defect) create the links for you — that's what keeps the triangle honest.
- **Reliability figures are frozen on purpose.** SAIDI/SAIFI et al. snapshot customer counts at interruption time; changing a service point later won't rewrite past indices.
- **Failing inspection items raise defects automatically** — but only if the template item was given a fail severity. If a "fail" didn't spawn a defect, check the template.
- **Asset links are references, not hard joins.** A defect/fault/order points at an asset by class+id; the operational record is about the asset but lives separately from it.
- **Field crews work these offline too.** Outages, faults, defects, and inspections can be taken into the field app, worked without signal, and synced back — that's Chapter 11.

---

*Next chapter: **Diagrams & annotations** — schematic diagrams, placing features on them, and marking up the map. Tell me to proceed.*

---

## Chapter 10 — Diagrams & annotations

Everything so far has lived on the **geographic map** — assets drawn where they physically are. But two everyday jobs don't fit a geographic map. First, an operator reading a substation needs a **schematic** — a clean one-line diagram where layout follows the *electrical* logic, not the ground. Second, anyone planning or reviewing needs to **scribble on the map** — circle a work area, drop a note, draw an arrow — without altering the network. VoltEdge gives you both: **diagrams** and **map annotations**. Neither is a GIS feature, and each has its own permissions and its own feel.

### 10.1 Two ways to mark up the network (and how they differ from features)

Three things can appear "on your screen" that look similar but are fundamentally different:

| | **GIS feature** (Ch 4–7) | **Diagram** (§10.2) | **Map annotation** (§10.3) |
|---|---|---|---|
| What it is | A real network asset | A schematic *view* of assets | Freehand markup / redlining |
| Where it's drawn | Its geographic location | Its own local x/y "paper" space | On top of the geographic map |
| Is it the asset? | Yes | It *references* assets (a second view of them) | No — it references nothing |
| Shows in Features browser / query? | Yes | No | No |
| Permission 🔒 | `features:read` / `features:write` | `diagrams:read` / `diagrams:write` (+ `diagrams:delete`) | `annotations:read` / `annotations:write` |

The key mental model: a **diagram is another view of real features** (place a transformer on a one-line and you're looking at the *same* transformer, just schematically), whereas an **annotation is pure markup** that points at nothing. Get this distinction and the rest of the chapter follows.

### 10.2 Schematic diagrams

🔒 `diagrams:read` to view, `diagrams:write` to build, and `diagrams:delete` (a *separate, higher* permission — see §10.2.9) to delete a whole diagram.

#### 10.2.1 What a diagram is

A **diagram** is a schematic drawing — most commonly a **one-line (single-line) diagram** — attached to a **container feature**. A container is any feature whose class is flagged as a *diagram container* (a substation, a switchboard — the kinds of asset that "contain" an internal arrangement worth schematising). Each diagram has:

- a **container** (the feature it belongs to),
- a **diagram type** — a domain code such as `ONE_LINE`, chosen at creation, and
- an optional **name**.

Critically, a diagram has its **own local coordinate space** — an abstract "sheet of paper" with its own x/y — *entirely independent* of where anything sits on the map. Moving a transformer's symbol on the one-line does **not** move the transformer on the map; they're two views of one asset. This is what lets a schematic be tidy and legible while the geography stays geographic.

#### 10.2.2 Opening a diagram and the split view

Diagrams open **beside the map** in a split view: the map on one side, the diagram canvas on the other. You can **maximise** the diagram to work on it full-width, and restore to the split when you want to see both. Each open diagram canvas has **its own bottom status bar** showing the **cursor's x/y in that diagram's space** (distinct from the map's coordinate readout) — and because that status bar belongs to the map/diagram column, it disappears when a diagram is maximised (§2.4). Open two diagrams and each keeps its own independent coordinate space and status bar.

#### 10.2.3 Creating a diagram

With `diagrams:write`, the create-diagram form asks for the three things above: the **container feature**, the **diagram type**, and an optional **name**. VoltEdge only lets you create a diagram against a class that's actually a diagram container — that single flag is the one gate it enforces before creating.

#### 10.2.4 Placing features on a diagram

The heart of a diagram is its **placements** — real features positioned on the schematic:

- **Drop a feature** onto the canvas and it's placed at an **x / y** with a **rotation**. Point-type features anchor at a single x/y; **line-type** features can carry a **hand-drawn path** — an ordered polyline in the diagram's local space — so a cable or busbar can be drawn as a run, not just a dot.
- **Placing the same feature again moves it** — it's an idempotent "one placement per feature per diagram", not a duplicate. So dragging a symbol around just updates where it sits.
- A placement is **the same feature** you'd find on the map. Click it and you get that feature's identity and attributes; editing the feature (Chapter 6) is editing the one asset, whichever view you're in.

**Label control.** Each placement's name label can be **hidden**, and its position fine-tuned — nudged (an x/y offset from its natural anchor), **rotated**, and **scaled** — so a dense schematic stays readable without labels colliding.

**Sectionalising a drawn busbar.** A busbar on a one-line is drawn as a *bar* (a line) even though it's a point feature. To split that bar into two independent busbars — say, to model opening a bus-section — use **sectionalize** on the placement: VoltEdge cuts the drawn bar at your point *in the diagram's own space*, shortens the original to the near half, mints a **second busbar feature** for the far half, and **divides the original bus's connectivity** between the two halves by where each connection sits along the bar — all in one transaction. It's the diagram-space cousin of the map's line-split (§6.7), but purpose-built because a diagram click has no lat/long.

#### 10.2.5 Diagram furniture — elements

Not everything on a schematic is a real feature. **Elements** are diagram *furniture* that reference no asset — the lines, labels, and symbols that make a drawing readable. The kinds VoltEdge draws:

- **text**, **box** — a note and a rectangle;
- **line** — a plain schematic line; **bus** and **tap** — a heavy busbar and its right-angle taps; **control** — a dashed, arrow-headed control/trip signal line;
- **symbol** — a stencil glyph from the symbol palette;
- **title_block**, **legend**, **slot** — template furniture (§10.2.7).

The polyline kinds (**line, bus, tap, control**) are drawn as paths of two or more points; the rest anchor at an x/y with an optional width/height. Every element carries a **style** (stroke, fill, dash, colour, font) so you control its appearance, and a **z-index** so you can layer furniture behind or in front of placements. Symbols come from a **stencil** palette so common schematic glyphs are a click away.

#### 10.2.6 Diagram layers

A diagram has its **own layers menu** — toggle the visibility of categories of content within the drawing, the same idea as the map's layers panel (§2.6) but scoped to that diagram. Handy for hiding furniture while you rewire, or isolating one class of placement.

#### 10.2.7 Templates — reuse a layout

Standardised drawings (every substation's one-line looks broadly alike) shouldn't be redrawn each time. **Save as template** captures a diagram's furniture and placements into a reusable **skeleton**, where each placed feature becomes a **slot** — an unbound placeholder that records *what kind* of feature belongs there. **Apply template** drops that skeleton onto a diagram, and you bind each slot to the actual feature. Templates are named, listed, and can be **deleted**; administrators manage them from the admin Diagrams area.

#### 10.2.8 Exporting a diagram

A finished diagram can be **exported to DXF** (the CAD interchange format), so a schematic built in VoltEdge drops into a drawing-office toolchain.

#### 10.2.9 Deleting a diagram — a higher bar

Deleting a whole diagram **cascades to all its placements and elements**, so it's deliberately escalated: it needs `diagrams:delete`, a *separate* permission held only by **supervisor, admin, and break-glass-admin** roles — **not editor** (Chapter 3). An editor can build and edit diagrams all day but cannot destroy one. (Removing an individual placement or element only needs the ordinary `diagrams:write`.)

*Worked example — a substation one-line (Dana, editor):*
1. Dana opens **Riverside Primary** (a substation, a diagram container) and creates a `ONE_LINE` diagram named "Riverside 11kV".
2. She places the incoming feeder, the two power transformers, the busbar, and the outgoing MV switches — dragging each symbol until the schematic reads cleanly, hiding a couple of colliding labels.
3. She draws the busbar as a **bar**, adds a **title_block** and a **legend**, and a **control** line for a trip signal.
4. Later, a bus-section is opened for maintenance; Tom **sectionalises** the busbar on the diagram, and the two halves take their share of the connectivity automatically.
5. She **saves it as a template** so the next substation starts from the same skeleton, and **exports DXF** for the drawing office. She *can't* delete the diagram herself — that needs a supervisor.

### 10.3 Map annotations (redlining)

🔒 `annotations:read` / `annotations:write`. An **annotation** is markup drawn **on the map** — VoltEdge's redlining layer. It sits *over* the network but is not part of it: it references no feature and never changes one.

#### 10.3.1 What an annotation is

Every annotation has a **kind**, and the kind fixes its geometry:

- **text** — a note, anchored at a **point**;
- **box** — a rectangle (a **polygon**), e.g. to ring an area;
- **line / arrow** — a **line** to connect or point at something.

The **kind is immutable** after you create it (a text is a point, a box a polygon — changing the kind would contradict the stored shape). Each annotation carries:

- **text** — required for a `text` annotation, an optional caption on the others;
- a **style** blob — stroke, fill, dash, colour, font, and **arrowheads** for lines;
- a **z-index** — to stack overlapping annotations;
- a **territory** — assigned by where you draw it (see §10.3.5).

#### 10.3.2 Creating annotations

With `annotations:write`, the **new-annotation menu** lets you pick a kind and draw it on the map — drop a text note, drag out a box, or draw a line/arrow — then type its text. It goes straight onto the redlining layer.

#### 10.3.3 Editing and styling

Selecting an annotation opens the **annotation edit panel**, where you can adjust its **geometry**, change its **text**, restyle it (colour, stroke, dash, fill, arrowheads, font), reorder it with **z-index**, or **delete** it. Because the kind is fixed, the panel validates any reshaped geometry against the original kind (you can move a box's corners, not turn it into a line).

#### 10.3.4 Visibility — the zoom band

Annotations obey a **global visible-zoom band** set by an administrator on the Map settings page: outside that zoom range, all annotations hide, so redlining meant for close-in work doesn't clutter a zoomed-out overview. This is separate from the three feature-zoom levers (§2.6) — it's a single band that every annotation inherits.

#### 10.3.5 Territory scope

An annotation is **spatially assigned to a territory** by where it's drawn, so the same access rules as features apply (Chapter 3): a territory-scoped user sees and manages annotations within their territory. Redlining doesn't escape the spatial model.

*Worked example — flagging a work area (Priya, dispatcher):*
1. A planned outage is coming on Feeder 7. Priya draws a **box** around the affected block and styles it amber.
2. She adds a **text** note — "Planned outage Sat 06:00–10:00, ref SW-1187" — and an **arrow** pointing at the switch to be opened.
3. The markup rides on the map for everyone with `annotations:read` in that territory, at the working zoom levels — context the field crew and control room share, with nothing in the network model touched.

### 10.4 Which one do I use?

- **The asset is real and belongs in the network?** It's a **feature** — create it (Chapter 6), don't draw it as furniture or annotation.
- **You need a clean electrical schematic of a substation/board?** Build a **diagram**, and *place* the real features on it.
- **You just need to mark, note, or highlight something on the map, changing nothing?** Use a **map annotation**.

A giveaway: if you'd ever want to *query, trace, or analyse* it (Chapters 5, 8), it must be a feature. Diagrams and annotations are for *seeing and communicating*, not for the network model itself.

### 10.5 Tips and gotchas

- **A diagram is a view, not a copy.** Editing a placed feature edits the one real asset; moving its symbol on the schematic changes only the drawing. The two coordinate spaces are independent by design.
- **Diagrams and annotations won't show in the Features browser or query engine** (Chapters 4–5) — those are for features. Look for diagrams beside the map, annotations on the redlining layer.
- **Deleting a diagram needs `diagrams:delete`** (supervisor/admin/break-glass only). If the delete option is greyed out, that's why — an editor can build but not destroy whole diagrams.
- **An annotation's kind is fixed.** If you drew a box and wanted a line, delete it and draw the line — you can't convert one to the other.
- **Can't see annotations you expect?** Check the **zoom band** (you may be zoomed outside it) and your **territory** — both can hide them legitimately.
- **Sectionalise, not split, on a diagram.** The map's line-split (§6.7) needs real geometry; a drawn busbar on a one-line is sectionalised in diagram space instead.
- **Templates save rework.** Standardise your substation one-lines once as a template, then apply-and-bind for each new site.
- **Export to DXF** when a schematic needs to leave VoltEdge for a CAD toolchain.

---

*Next chapter: **The field app (offline)** — taking work into the field on the Flutter app, working without signal, and syncing back. Tell me to proceed.*

---

## Chapter 11 — The field app (offline)

Everything so far has assumed a browser and a connection. In the field — down a rural feeder, inside a basement substation, on a hilltop with no bars — you have neither. VoltEdge's companion **mobile app** (built with Flutter, for phones and tablets) is designed for exactly that: **download your work area while you have signal, then survey, edit, inspect, log faults, and capture precise GPS entirely offline, and sync it all back when you're in range again.** It's not a separate system — it's the same VoltEdge data and the same permissions, in an offline-first field client.

### 11.1 What the field app is for, and who uses it

The app serves the **field roles** from Chapter 3 — a **field-surveyor** capturing and correcting assets, a **field-technician** working defects and inspections, a **field-enumerator** collecting data, a **fault-response crew** chasing faults. The shape of a day is always the same three beats:

> **Pack** (download your area while connected) → **Work offline** (no signal needed) → **Sync** (push it back when you reconnect).

Two principles hold throughout:

- **Your permissions travel with you.** The app can only pack and sync what your roles allow — a surveyor without `wam:read` simply gets no work-management data in their pack (§11.3.3). There's no "field mode" that grants extra rights.
- **The field is offline-first, not offline-only.** You connect briefly to build a pack and to sync; everything *between* those is local.

### 11.2 Signing in

On the app's **Connection / Settings** screen you enter the **server address**, your **email**, and your **password**. That's the same account you use on the web — the app exchanges your credentials for a **bearer token** behind the scenes, so there's no separate field login to remember. You stay signed in on the device until you **sign out**. 🔒 Everything you can do in the app is bounded by the roles on that account.

### 11.3 Field packs — your offline work area

#### 11.3.1 What a pack is

A **field pack** is a self-contained bundle for one **bounded area**. It's built from two artifacts:

- an **offline basemap** (an MBTiles tileset) so the map still draws with no signal, and
- a **data pack** (a local SQLite database) that is the offline **"edit-truth"** — the features and operational records you'll work on, and the store your edits are written into.

A pack is **always bounded by a bounding box** — there is no "download the whole network" pack; you pack an *area*. It also carries a **snapshot time**: the pack reflects the network *as it was at the moment it was built* (§11.6 on staleness).

#### 11.3.2 Requesting a pack

The **New pack** screen lets you define three things:

1. **The area.** Draw a bounding box **on a map** by tapping two corners, tap **"Use current view"** to pack what's on screen, or type the four coordinates directly (the bbox picker fills them either way).
2. **The feature classes** to include.
3. **The modules** to include — connectivity, relationships, **Works (WAM)**, and **Faults** — each an on/off inclusion.

You submit, and the server **builds the pack asynchronously** (it's a queued job, so a large area doesn't block your device); when it's ready you **download** it. From then on it works with no connection.

#### 11.3.3 What rides in a pack — and what can't

A pack's contents are **frozen from *your* rights at the moment you request it**. The build runs detached on the server with no ability to re-check you, so it packs *exactly and only* what your roles authorised — which means a pack can never leak beyond your permissions:

- **Features** in the bbox (from your `features:read`), including any **private drafts** you're allowed to see (Chapter 3).
- **Connectivity** and **relationships** — *only if* you hold `connectivity:read` / `relationships:read`. A feature-only viewer gets a pack with no connectivity table — still fully usable for viewing and attribute/move edits, just without the graph.
- **Works (WAM)** — *only if* you hold `wam:read`: the **open defects** on the packed assets, **your own open work orders** (your field "work board"), and the **active inspection templates**, so you can work jobs and run checklists offline.
- **Faults** — *only if* you hold `faults:read`: the **open fault events** on the packed assets, so a fault crew sees live faults offline.

WAM and Faults are **independent** — a pack can carry either, both, or neither, which is how one mechanism serves a survey-only, a works-only, a fault-only, or an all-rounder crew. Ask for a module you can't read and you don't get an error and a leak — you get a pack without that module.

#### 11.3.4 Managing your packs

The app lists your packs; you can **download**, **open**, or **cancel** a pending build. Opening a pack takes you to its **offline map** with buttons through to its modules. (On the admin side, a pack can be **disabled** to stop it cluttering a tablet without deleting its data — an administrator declutter, not something you do in the field.)

### 11.4 Working offline

Once a pack is downloaded, the connection can drop entirely. Everything below writes to the local data pack and waits for sync.

#### 11.4.1 The offline map

The pack's **MBTiles basemap** draws the backdrop with no signal, and your packed **features render on top**, styled like the web map. Pan, zoom, and **tap a feature** to open it.

#### 11.4.2 Viewing, editing, and creating features

From a feature you can **view its details**, **edit its attributes**, **move its geometry**, and **create new features** in the field — the mobile counterparts of Chapter 6. Every change is stored **locally** in the data pack and queued for sync; nothing is sent until you reconnect.

#### 11.4.3 High-accuracy GPS (GNSS)

Field capture is only as good as its position, so the app supports **GNSS** beyond the phone's built-in GPS. You can pick your **GNSS source** — the internal receiver, or an **external Bluetooth GNSS receiver** (survey-grade), whose **NMEA** stream the app parses — and watch a live **readout** of position and fix quality. When you capture or move a feature you can **attach a GPS capture**, and that position *and its quality snapshot* ride back with the edit on sync, so the office sees not just where, but how accurately. A feature's **GPS-capture history** is available too.

#### 11.4.4 Photos and attachments

You can **capture photos** against a feature in the field — evidence of a defect, a nameplate, an as-built. They're stored with the pack and **uploaded on sync**.

#### 11.4.5 Works (WAM) offline — the field work board

If your pack included WAM, the **Works** screen is your offline job board (Chapter 9's WAM, in the field):

- see and triage **defects**, and **create** new ones or **change their status**;
- work **your assigned work orders**;
- **record inspections** against the packed **templates** — and, exactly as on the web, a **failing checklist item with a severity auto-raises a defect**, closing the inspection→defect loop offline.

#### 11.4.6 Faults offline

If your pack included Faults, the **Faults** screen lets a crew **see the open faults** in the area, **raise new** fault events, **update status** (mark one cleared), and **book a defect** from a fault — the Chapter 9 fault workflow, offline. All of it queues for sync.

### 11.5 Syncing back

#### 11.5.1 How sync works

When you reconnect, **sync** pushes your queued operations to the server in one call. Those operations span everything you did offline:

- **Features** — create, update attributes, delete, establish connection, establish relationship (each optionally carrying its GPS capture);
- **Works** — create a defect, change a defect's status, update a work order, record an inspection;
- **Faults** — create a fault, change a fault's status, book a defect from a fault.

Photos upload alongside. Sync is **idempotent** — each operation carries a device-generated id, so if a flaky connection makes you sync twice, the server recognises the duplicates and won't double-apply them. It's safe to retry.

#### 11.5.2 The two-stage review — why your edit isn't instantly live

Field edits **don't post straight into the live database.** Sync lands your operations in a **review queue** (the sync inbox); a reviewer with the right permissions (`sync:review` to see the queue, `sync:post` to apply it) checks them and **posts** them into the live network. This is a deliberate quality gate: field work is reviewed before it becomes the system of record, so a mistaken move or a mis-keyed attribute is caught, not silently authoritative. In practice you sync, and your supervisor (or you, if you hold the review permissions) approves the batch.

#### 11.5.3 Sync status

The app reports **sync status** so you can see what's still **pending**, what's been accepted, and what needs attention — so you know your day's work is safely captured before you pack up.

### 11.6 Tips and gotchas

- **Pack before you lose signal.** The one thing you must do connected is build and download the pack. Everything else can wait for reconnection — but you can't pack an area you can't reach.
- **A pack is a point-in-time snapshot.** If the office rewires or re-attributes the area after you packed it, your pack is stale — **re-pack** to pick up changes. (Delta refresh is a future capability; today you rebuild.)
- **Your rights bound the pack.** Missing connectivity, Works, or Faults in the field usually means your account lacks that read permission — not a bug. Check your roles (Chapter 3).
- **Packs are area-bounded.** There's no whole-network download — draw the bbox around the job.
- **Sync is safe to retry.** Idempotency means a repeated sync won't duplicate your work; if a sync fails midway, just sync again.
- **Your edits await review.** Don't expect a field change to appear live on the web instantly — it posts once a reviewer approves it (`sync:review`/`sync:post`). That's the quality gate, working as intended.
- **Use an external GNSS receiver for survey work.** When position accuracy matters, pair a Bluetooth GNSS unit and pick it as the source; the fix quality rides back with every capture.
- **WAM and Faults are independent inclusions.** Pack only what the crew needs — a survey-only pack skips both; a fault crew's pack can carry Faults without Works.

*Worked example — a survey day (Dana, field supervisor):*
1. Before leaving depot (on Wi-Fi), Dana opens **New pack**, taps two corners around the Oakwood Zone on the map, includes the LV classes plus **Works** and **Faults**, and downloads the pack.
2. On site with no signal, she corrects three misplaced pillars using an **external Bluetooth GNSS** receiver for centimetre positions, **photographs** a cracked pillar, and **records an inspection** whose failing "door secure?" item auto-raises a defect.
3. A crew member on the same pack **raises a fault** on a blown fuse and marks it cleared after replacing it.
4. Back in range, Dana **syncs**. Everything — the moves with their GPS captures, the photo, the auto-raised defect, the fault — lands in the **review queue**. Her supervisor **posts** the batch, and it becomes live network truth.

---

*Next chapter: **External map layers & the assistant** — bringing third-party layers onto the map, click-to-identify, and the map assistant. Tell me to proceed.*

---

## Chapter 12 — External map layers & the assistant

Two features round out the map. The first looks *outward*: **external map layers** let you overlay third-party geographic data — aerial imagery, cadastral boundaries, someone else's asset service — beneath and around your own network, so your assets sit in real-world context. The second makes the map *conversational*: the **assistant** lets you ask questions and drive VoltEdge in plain language, safely bounded by exactly what you're allowed to do. Different as they are, they share one design principle you'll see throughout: **VoltEdge trusts neither the outside service nor the language model — your permissions and your explicit confirmation stay in charge.**

### 12.1 External map layers (service layers)

A **service layer** is an external/third-party map service, catalogued by an administrator and rendered as a **toggleable overlay** on the geographic map. Where the **basemap** (§2.5) is the single tileset *under* everything, a service layer is an overlay that **stacks** on top — and you can have several.

#### 12.1.1 The service types VoltEdge speaks

VoltEdge can consume seven kinds of external map service, so most public and enterprise sources "just work" once catalogued:

- **OGC WMS** (Web Map Service) — rendered map images;
- **OGC WMTS** (Web Map Tile Service) — pre-tiled map images;
- **XYZ raster** — the common `{z}/{x}/{y}` image-tile scheme;
- **XYZ vector tiles** (MVT) — vector tiles;
- **Esri MapServer** and **Esri FeatureServer** — ArcGIS services (raster and feature);
- **OGC WFS** (Web Feature Service) — vector features as GeoJSON.

Raster types (WMS/WMTS/XYZ/Esri MapServer) draw as image overlays; the vector types (WFS, Esri FeatureServer, vector tiles) draw as actual features fetched for your current view.

#### 12.1.2 Using service layers on the map

🔒 With `features:read` (i.e. anyone with map access), enabled service layers appear as their **own section in the layers panel** (§2.6), alongside your feature classes. There you can:

- **toggle each layer** on or off,
- **set its opacity** (each layer remembers its own), so an aerial can sit at 60% under your network, and
- see its **attribution** — the required credit line — in the map **status bar** (§2.4).

They obey their configured **min/max zoom**, so a detailed layer only draws when you're zoomed in enough. Layers stack in a defined order, letting you build up context — imagery, then boundaries, then your network on top.

#### 12.1.3 Click-to-identify — querying an external layer

Some service layers are marked **queryable**. On those, you can **click a spot** and VoltEdge asks the *external* service what's there — a **WMS GetFeatureInfo** call, an **Esri identify**, or a client-side hit-test for vector layers — and shows the result in a read-only **identify panel**. So you can click a cadastral overlay and read the parcel's attributes, without those features ever entering your database. It's **read-only**: identify shows you the external service's data; it never writes anything into VoltEdge. Non-queryable layers simply don't respond to identify clicks.

#### 12.1.4 Managing the catalog (administrators)

🔒 Adding, editing, and removing service layers needs `metadata:write` — it's an administrative catalog task, done from the **Service layers** card in the admin area. For each layer an administrator sets its **type, URL, layer id(s), image format, version, CRS, zoom range, default opacity, whether it's queryable, its attribution,** a **sort order,** and an **enabled** flag. To spare all that hand-entry, VoltEdge offers **auto-discovery**: point it at a WMS/WMTS **GetCapabilities** document or an Esri service and it reads the service's own metadata and **auto-fills** the layer name, version, format, CRS (or WMTS matrix set), and extent. Disabling a layer hides it from everyone's map without deleting its configuration.

#### 12.1.5 Authentication and the proxy — private and secured layers

Many public services need no credentials, but an enterprise one often does. VoltEdge supports **optional per-layer authentication** — a username, an auth header, or a query-parameter key. The secret is **encrypted at rest and write-only**: once saved it's never returned to the browser (the admin UI only shows *that* a credential exists, never its value).

For secured or cross-origin layers, VoltEdge routes requests through its **own same-origin proxy**, hardened against SSRF (server-side request forgery). This matters for two reasons: the third-party service's **credentials never reach the browser** (the server injects them), and the external **URL and response are mediated by VoltEdge** rather than called directly from your machine. So a private layer stays private, and a hostile-looking URL can't be used to make VoltEdge's server reach somewhere it shouldn't.

*Worked example — context for a planning study (Tom, analyst):*
Tom is assessing a new connection. He turns on the **aerial imagery** WMS at 70% opacity to see ground truth, adds the **cadastral boundaries** WFS to see parcels, and leaves his network on top. He clicks a parcel — VoltEdge **identifies** it against the cadastral service and shows the owner and lot number in the read-only panel — all without importing a single external feature into VoltEdge.

### 12.2 The map assistant

The **assistant** is VoltEdge's in-app helper: a "second front door" onto the very same actions the rest of the app exposes, reached in plain language. It has two halves — deterministic **suggestions** that need no AI at all, and an AI **chat** that routes freeform requests to the right tool.

#### 12.2.1 What it is — and, importantly, what it isn't

The assistant runs **in-app, behind your ordinary session**, and **every action it takes is bounded by your RBAC** (Chapter 3). It's wired to the same large catalogue of **tools** the automation interface (MCP) exposes — well over a hundred of them, spanning features, connectivity and relationships, tracing and analysis, OMS/WAM/Faults, diagrams, annotations, and more — so it can drive most of what you can do by hand.

Three things it is *not*, and these are the safety guarantees worth internalising:

- **Not a way around your permissions.** The assistant is only ever offered the tools *you already hold the permission to run*; it can't propose an action outside your RBAC, and the permission is re-checked when the action actually runs. The language model is **never** the security boundary — your permissions are.
- **Not an auto-writer.** It will happily *read* to answer you, but it **never silently changes anything**. Any change comes back as a **proposed action you must confirm** — your click is the commit.
- **Not an oracle.** The model's only job is to *route* — pick the right tool and fill its arguments. All the real reasoning (the trace, the aggregate, the rule check) is done by VoltEdge's deterministic engines, exactly as when you click the buttons yourself.

#### 12.2.2 Suggestion chips — help without AI

The simplest half needs no language model. When you **edit or snap a feature** on the map (say you drag a new cable so its end meets a transformer), VoltEdge asks its connectivity and relationship engines for the **ranked, rule-legal** things that feature could connect or relate to, and offers each as a **confirmation chip**. Click one and it commits that connection or relationship using the same establish path as the manual flow (Chapter 7) — nothing auto-writes. 🔒 These chips are gated on your **commit** permissions: if you can't establish connections, you won't see connect chips (you'd have nothing to click). It's the assistant catching "you probably meant to wire these together" and offering the one-click.

#### 12.2.3 Chat — driving VoltEdge in plain language

The richer half is **chat**. You type a request; a small language model reads it and **picks the right tool and fills in its arguments** — but only from the tools you're permitted to run. From there:

- **Read requests run immediately.** "How many distribution transformers are in the Oakwood Zone?" routes to the aggregate tool, runs, and the assistant answers — chaining a couple of reads if it needs to, within a safety cap so it can't loop forever.
- **Write requests never auto-run.** "Connect this switch to that busbar" comes back as a **proposed action** for you to confirm; only when you confirm does it execute, and the write permission is **re-enforced** at that moment. Same "human click is the commit" contract as the chips.

So chat is fast and safe: instant for questions, confirm-first for changes.

#### 12.2.4 What you can ask

Because it's wired to the full tool catalogue, useful asks include:

- **Questions:** "How many faults are open on Feeder 7?", "What's the total rated capacity of transformers in this territory?" (Chapter 5's querying, in words).
- **Find things:** "Show me feature TX-Feeder7-014."
- **Actions (confirm-first):** "Log a high-severity defect on this transformer", "Connect the two feeders with a normally-open tie."
- **Analysis:** kicking off a trace or an analysis from a named feature (Chapter 8).

Everything it does, you could do by hand — it just gets you there in a sentence.

#### 12.2.5 Availability

Chat needs a **configured language model**; a **status** indicator tells you whether it's available in your installation. The **suggestion chips work regardless** — they're deterministic and need no model — so even with no AI configured, the "you probably meant to connect these" help is still there.

*Worked example — a quick answer and a quick fix (Priya, dispatcher):*
Priya asks the assistant, *"How many trouble calls are open right now?"* — it routes to a read tool and answers **7** instantly. She then drags a newly-built service so it snaps to a pillar; a **suggestion chip** offers "connect SERVICE-… to PILLAR-…", and she clicks to commit. Later she types *"raise a fault on this cable"*; the assistant proposes the action, she reviews and **confirms**, and only then is the fault created — with her permission checked one more time.

### 12.3 The shared safety model

It's worth naming the pattern both features follow, because it's the same one:

- **Nothing external is trusted.** External layers are mediated by an SSRF-hardened proxy and their identify results are read-only; the assistant's language model is a router, never a source of authority.
- **Your permissions bound everything.** You only see and use layers and tools your roles permit.
- **Nothing changes without your explicit confirmation.** Identify never writes; assistant writes are always confirm-first.

### 12.4 Tips and gotchas

- **Service layers are overlays, not imports.** An external layer draws on your map and can be identified, but its features never enter your database — you can't trace, analyse, or edit them. Import them (Chapter 6 tooling / admin ETL) if you need them as real features.
- **Layer not showing?** Check it's **enabled**, that you're within its **zoom range**, and its **opacity** isn't at zero.
- **Only queryable layers identify.** If clicking a layer does nothing, it isn't marked queryable.
- **Credentials are write-only.** An admin can set a layer's secret but never read it back — re-enter it to change it.
- **The assistant can't exceed your rights.** If it says it can't do something, that's usually your permissions (Chapter 3), not a failure — the model is only ever offered the tools you hold.
- **Assistant writes are always confirm-first.** If you expected something to "just happen," look for the proposed-action confirmation — it's waiting for your click.
- **No AI configured? You still get suggestion chips.** The deterministic half of the assistant doesn't depend on a language model.

---

*Next chapter: **Asset tags & scannable labels** — how every asset carries a scannable code, how to print the labels, and how a scan in the field or on the web lands you back on the record.*

---

## Chapter 13 — Asset tags & scannable labels

Every feature in VoltEdge carries at least one **asset tag**: a short, scannable code you can print on a durable label and stick on the physical plant — the transformer, the pole, the pillar. Scan that label later, from a phone in the field or a camera at your desk, and VoltEdge takes you straight back to the record. This chapter explains where those codes come from, how to read and manage them, how to print labels one at a time or in bulk, and how a scan resolves — including offline, in the field, with no signal.

If you have used a warehouse or logistics system, the idea is familiar: put a barcode on the thing, scan the thing to find its record. What makes VoltEdge's version worth a chapter is that the codes are **minted automatically for every asset** (you never have to remember to create one), a single asset can carry **several** codes (its VoltEdge code *and* whatever legacy number was already painted on it), and a scan is **scope-aware** — it only ever resolves to a record you're allowed to see.

> **Legend recap:** 🔒 needs a permission (named inline). 🗺️ also depends on territory. 💡 tip. ⚠️ caution. The two permissions this whole chapter turns on are **`tags:read`** (scan, resolve, view a feature's tags) and **`tags:write`** (register a legacy code, set the primary, void, reissue, record a print).

### 14.1 What an asset tag is

#### 14.1.1 The code

An asset tag's **code** looks like `VE-7Q3KM` — a `VE-` prefix, a short body, and one trailing check character. Three properties are worth knowing:

- **It's short and human-readable.** The body is written in an alphabet that deliberately **omits the ambiguous letters** `I`, `L`, `O` and `U`, so nobody standing at a pole in the rain misreads a `1` for an `I` or a `0` for an `O`. If you ever have to key a code by hand, that's the payoff.
- **The last character is a check character.** It's computed from the rest of the code, so if you mistype a single character the system can tell the code is invalid rather than silently resolving to the *wrong* asset. A tag that "doesn't resolve" when you're sure you read it right is usually a single-character slip — read it again.
- **It's globally unique and never reused.** No two assets ever share a code, and a code that has been retired is never handed out again. A code identifies one asset for all time.

You don't create these codes and you can't choose them — VoltEdge mints them (13.1.3).

#### 14.1.2 One asset, many codes — and one *primary*

A feature can carry **more than one** tag:

- The **system-minted code** (`source: generated`) that VoltEdge created for it automatically.
- Any number of **legacy / external codes** (`source: external`) — the asset number already stencilled on the kit before VoltEdge existed, a manufacturer serial, a code from the system you migrated from. You register these yourself (13.4).

Exactly **one** of a feature's tags is marked **primary**. The primary is *the one that gets printed* on the label you affix. By default the primary is the generated code, but you can promote a legacy code to primary (13.4.3) — handy when an asset already wears a well-known number in the field and you'd rather keep printing that than slap a new one over it. Whichever is primary, **all** of a feature's active codes still resolve when scanned.

#### 14.1.3 Where codes come from — automatic minting

You never have to "add a tag" to a new asset. The moment a feature comes into existence — however it was created: drawn on the map, built by an assembly, imported by the ETL engine, or inserted by a seed script — VoltEdge mints it a primary code automatically. When the module was first switched on, **every asset that already existed** was back-filled with a code in the same motion, so there is no such thing as an untagged feature.

💡 **A soft-deleted asset keeps its tag.** If a feature is deleted the ordinary way (soft delete — it can be restored), the label already on the pole stays valid; scanning it still finds the record. Only a permanent purge removes an asset's codes for good, at which point the code simply stops resolving.

#### 14.1.4 The three symbols on a label

A printed VoltEdge label carries the **same code three ways**, so it can be read by whatever's to hand:

- A **QR code** — point any phone camera at it. It encodes a web link, so a scan can open straight in a browser.
- A **Code128 barcode** — for a warehouse-style scanner gun.
- The **code as text**, in a clear monospace font, with a line of context (the asset's class and circuit) — the manual fallback when all else fails and someone has to type it.

You never choose between them; every label has all three.

### 14.2 Viewing a feature's tags — the Tags tab

Open any feature's panel (click it on the map, or use *Show on map* from the Features browser) and you'll find a **Tags** tab 🔒 `tags:read` alongside the others. It lists every code the feature carries:

- Each row shows the **code**, its **source** (generated or external), its **status** (active / superseded / void — see 13.6), and a **Primary** badge on the one that prints.
- A **copy** button on each row puts the code on your clipboard.
- If you hold 🔒 `tags:write`, each active row also offers **Set primary**, **Reissue**, and **Void** actions, and there's a small form to **register a legacy code** (13.4).
- A **Print** button opens the label sheet (13.3) — available to *everyone* who can see the tab, because printing a label is a read-level act.

*Worked example — Tom confirms an asset (Tom, editor):*
A crew radios in that they're standing at "the transformer on Millbrook Road" but there are two nearby. Tom clicks the first on the map, opens its **Tags** tab, and reads out the primary code `VE-7Q3KM`. The crew says the label on their unit reads `VE-9F4TP`. Different asset — Tom clicks the second transformer, sees the matching code, and now both he and the crew are certain they're talking about the same box before anyone opens a link.

### 14.3 Printing labels

#### 14.3.1 Printing one label

On a feature's **Tags** tab, click **Print** next to a tag (or the feature's Print button). VoltEdge opens a **label sheet** — a clean, full-screen view of the label(s) with a small toolbar — and you print it with your browser's normal print dialog (there's a **Print** button in the toolbar; **Esc** or **Close** dismisses it). The label is drawn at true physical size, so as long as you don't let your printer "scale to fit", it comes out the right size for the stock.

💡 **The QR and barcode print crisply because they're drawn as vector art, not photos.** Print at your printer's native resolution; don't screenshot the sheet.

#### 14.3.2 Bulk printing from the Features browser 🔒 `tags:read`

When you're labelling a whole batch of assets — a newly-built feeder, an entire class after an import — printing one at a time is tedious. The **Features** browser (Chapter 4) lets you print a sheet of many labels at once:

1. Open **Features** and expand the class you want (say, `DIST_XFMR`).
2. A **checkbox** appears on each row, plus a **select-all** box in the header (it shows a partial state when only some rows are ticked).
3. Tick the assets you want, then click **Print tags (N)** in the selection bar.
4. VoltEdge fetches the **primary** code for each selected feature and lays them all out on one printable sheet — each label captioned with the asset's name and class — ready for your print dialog.

The sheet prints **one label per feature** — the primary code, the one meant to go on the plant. If a selected feature happens to have no active tag (rare — every feature is minted one), it's simply absent from the sheet, and a small note on the sheet tells you how many were skipped so the count never silently surprises you.

*Worked example — Dana labels a new feeder (Dana, editor):*
Dana has just built and published a 40-span LV feeder with its pillars and service points. She opens **Features**, expands `LV_PILLAR`, clicks **select-all**, and hits **Print tags (18)**. One sheet of eighteen labels comes out of the office printer; the crew takes it to site and works down the feeder sticking each on its pillar. Later she repeats it for the service points. What would have been fifty trips through a single-print dialog is two.

💡 **Printing records a count, not a lock.** Each print bumps a per-tag *print count* so you can see how often a label's been run off (useful when you suspect duplicates in the field). Recording that bump needs 🔒 `tags:write`; if you only hold `tags:read` you can still print — VoltEdge just doesn't update the counter.

### 14.4 Managing tags 🔒 `tags:write`

Everything in this section needs the **`tags:write`** permission (editor tier and up). Viewers and field scanners can read and print but not change tags.

#### 14.4.1 Registering a legacy / external code

When an asset already wears a number from a previous system or the manufacturer, record it so a scan of the *old* label still finds the record. On the **Tags** tab, use the **register** form: type the existing code, optionally tick **make primary**, and add it. It's stored as an `external` tag and resolves from then on, exactly like the generated one.

💡 **Register before you migrate labels, not after.** If you're moving off an old asset-numbering scheme, register the legacy numbers first; then every scan of an old label keeps working while you re-label at your own pace.

#### 14.4.2 Reissuing a damaged tag

If a label is damaged, faded, or lost, **Reissue** it. Reissue does two things atomically: it marks the old code **superseded** (it still resolves — the old label may linger on other copies in the field) and **mints a fresh code** to print and affix. You keep a clean history: the record shows the code was replaced, not erased.

#### 14.4.3 Setting the primary

**Set primary** promotes a tag to be the one that prints. Only one tag per feature is primary at a time — promoting one automatically demotes the previous. Use it when you want a legacy number, not the VoltEdge code, to be the asset's code-of-record on new labels.

#### 14.4.4 Voiding a tag

**Void** retires a code entirely: a voided code **stops resolving** (a scan of it returns "not found", as if it never existed). Use it for a code created in error or a number that should no longer point anywhere.

⚠️ **You can't void the primary.** Because the primary is the code that prints, voiding it would leave the asset with nothing to label. Promote another tag to primary first (or reissue), then void the old one.

Here's how the four statuses relate:

```
   register ─►  ACTIVE ──set primary──►  ACTIVE (primary)
                  │  ▲                       │
            reissue │  │ (old →)             │ (can't void a primary)
                  ▼  │                       ▼
             SUPERSEDED (still resolves)   void ─►  VOID (never resolves)
```

### 14.5 Resolving a scan on the web

A QR code encodes a link of the form `…/t/<code>`. Open it in a browser — or use the **Resolve tag** item in the sidebar 🔒 `tags:read` — and VoltEdge:

1. Looks the code up, **scope-aware**: if the code belongs to an asset outside your territory 🗺️ or a private draft you can't see, it reports **not found** rather than revealing that the asset exists (13.7).
2. On a hit, **selects the feature and drops you onto the map** focused on it, with its panel open — the same landing as *Show on map* from the Features browser.
3. On the bare `/t` page, or on any miss, shows a **manual code-entry box** so someone can key a code from the printed text when a camera won't cooperate.

💡 **The web resolver is the fastest desk workflow.** A dispatcher holding a photo of a label can type the code into **Resolve tag** and be looking at the asset in two seconds — no map hunting.

### 14.6 Scanning in the field (offline-first)

The field app (Chapter 11) turns tag scanning into the primary way a crew finds an asset on site — and it works **with no signal**.

#### 14.6.1 What ships in the pack

When a field pack is built by someone who holds 🔒 `tags:read`, the pack includes a **tag index** — the codes for the assets inside the pack's area (active and superseded; voided codes are left out, matching the online resolver). That index is what makes offline resolution possible.

#### 14.6.2 Scanning

Tap the **scan** button (the QR icon) on the field map. The camera reads both **QR codes and Code128 barcodes**; there's a torch toggle, a camera switch, and a **manual-entry** fallback. VoltEdge:

1. Resolves the code **against the pack first** — instant, no network. If the code is on an asset in your pack, it selects it on the field map immediately.
2. If it's not in the pack **and** you have a connection, it falls back to an **online** resolve.
3. If a code resolves online to an asset that isn't in your pack, VoltEdge names it in a dialog but can't place it on your offline map — it's outside the area you downloaded.

Once an asset is selected, you're in the ordinary field workflow: open it, attach an inspection photo, book a defect, or raise a fault — all from its panel.

*Worked example — a technician on a no-signal feeder (field technician):*
Priya's crew is deep in a valley with no bars. They reach a pillar with a cracked door and a faded label. The technician taps **scan**, the phone reads the QR, and because the pillar is inside the pack the record opens **instantly offline**. They photograph the crack, book a defect against the asset, and move on. Back in coverage that evening the defect syncs to the office queue — but the scan-to-record step never needed the network at all.

### 14.7 How resolution respects your access

A scan is a *read*, and it obeys the same two fences every other read does (Chapter 3):

- **Territory scope** 🗺️ — you resolve only codes on assets inside your granted territories (unless you hold `territories:unscoped`).
- **Draft visibility** — a code on someone else's private draft resolves for them, not for you.

Crucially, an out-of-scope or non-existent code returns the **same** "not found" — VoltEdge never reveals that an asset exists by treating "you can't see it" differently from "there's no such code". A voided code also returns not-found; a superseded one still resolves.

### 14.8 Asset tags for the AI assistant

Everything in this chapter is available to the **map assistant** (Chapter 12) and to MCP-connected tools, bounded by your own permissions:

- **Resolve a scanned code**, **list a feature's tags**, and fetch the **primary code for a batch** of features — reads, needing `tags:read`.
- **Register**, **set-primary**, **void**, **reissue**, and **mark-printed** — writes, needing `tags:write` (void is flagged as destructive and, like every assistant write, is confirm-first).
- **Find features by tag** — search a class for features whose tag matches a code (exact or prefix) or that simply *have* (or *lack*) a tag of a given kind. This needs both `features:read` (to return the records) and `tags:read` (to read the tags).

The last one is also a first-class part of querying, covered next.

### 14.9 Finding features by their tags (querying)

The structured query builder (Chapter 5) can filter on tags, so "which assets in this class carry a legacy code?" or "find the asset whose tag starts `VE-7Q3`" become one-click searches.

In the **query panel**, in Filter mode, tick the **Tag** control and set:

- **Presence** — *have* a tag (the default) or are *missing* one.
- **Source** — any, only *generated*, or only *external* (e.g. "which assets still carry a legacy number?").
- **Code** — an exact code, or a **prefix** match (type `VE-7Q3` with *prefix* selected to sweep a range).

Run the query and the results table gains a **Tag** column showing each matched feature's primary code, so a code search shows you exactly which record a scan would land on. Voided codes never match — the filter only ever considers live and superseded tags, the same set a scan can resolve.

*Worked example — Dana audits a migration (Dana, editor):*
After importing an old feeder, Dana wants to know which transformers still lack a registered legacy number so she can chase them down. She opens the query panel on `DIST_XFMR`, ticks **Tag**, sets presence to **missing** and source to **external**, and runs it. The results are exactly the units with no legacy code registered yet — her worklist for the week.

### 14.10 Tips and gotchas

- **Every asset already has a tag.** You never create the first one; VoltEdge minted it. Your job is printing, registering legacy numbers, and lifecycle (reissue/void).
- **A code that won't resolve is usually a mis-key.** The check character exists precisely to reject a single wrong character — read the printed text again rather than assuming the tag is broken.
- **Can't void the primary.** Promote or reissue first (13.4.4).
- **Superseded ≠ void.** A reissued (superseded) code *still resolves* so old labels in the field keep working; a voided code does not.
- **Scanning is scope-aware.** "Not found" can mean "outside your territory", not "doesn't exist" (13.7) — a colleague with wider access may resolve the same code.
- **Field scan works offline** only for assets inside your pack; pull a fresh pack for a new area (Chapter 11).
- **Printing is read-level; managing is write-level.** Anyone who can see a tag can print it; registering/void/reissue/set-primary need `tags:write`.

---

*Next chapter: **Appendix** — the complete permission reference, the role-to-permission map (now including `tags:read` / `tags:write`), and a quick-reference of every capability by chapter.*

---

## Chapter 14 — Appendix: the complete reference

This closing chapter is the one to keep a bookmark on. Chapters 1–13 taught you *how* to do things; this one is the **lookup**: every permission, every role, exactly what each role can do, and a capability-to-chapter index so you can jump straight to the right section. Everything here is drawn from VoltEdge's actual authorization tables, so if a colleague asks "can a viewer do X?", the answer is in §14.4.

### 14.1 How authorization works — the one-paragraph recap

VoltEdge follows **"authorization as data."** There is a fixed catalogue of **permissions** — stable `resource:action` strings like `features:read`. A **role** is a named *bundle* of permissions. A **user** holds one or more roles, and their effective permission set is the **union** across those roles. Every protected route checks one permission with a simple membership test: for read-style requests it needs the `:read`-side permission, for mutating requests the write-side one. On top of that sit two extra layers you'll meet again below: **territory scoping** (a spatial fence ANDed onto what your permissions already allow) and the **break-glass** flag (an emergency account that overrides everything). Adding a new distinction is a data row, not a code change — which is why the tables below are the whole truth.

### 14.2 The permission catalogue — all 33 permissions

Grouped by area. The string in the first column is the exact key the system checks.

| Permission | Governs |
|---|---|
| `features:read` | View feature data: map layers, attributes, and feature-class schema. |
| `features:write` | Create, edit, publish, unpublish, delete, and restore features. |
| `connectivity:read` | View committed electrical connectivity. |
| `connectivity:commit` | Establish or revoke electrical connectivity. |
| `relationships:read` | View committed structural/spatial relationships. |
| `relationships:commit` | Establish or revoke structural/spatial relationships. |
| `diagrams:read` | View schematic diagrams and their placements. |
| `diagrams:write` | Create and rename diagrams and place or remove features on them. |
| `diagrams:delete` | Delete an entire schematic diagram, including all of its placements. |
| `annotations:read` | View map annotations (the redlining/markup layer). |
| `annotations:write` | Create, edit, and delete map annotations. |
| `trace:run` | Run network traces (isolation, feeder, section, supply-path, configurable). |
| `analysis:run` | Run power-flow, fault-current, ampacity, and the other engineering analyses. |
| `metadata:read` | View the metadata catalog: classes, domains, field groups, symbology, materials. |
| `metadata:write` | Edit and compile the metadata catalog and symbology. |
| `admin:auth-settings` | Change auth settings, including the local-auth break-glass switch. |
| `admin:users` | Manage user accounts and role assignments. |
| `territories:write` | Create, rename, re-parent, delete territories, and edit their boundaries. |
| `circuits:write` | Create, rename, delete circuits, and edit their voltage and source feature. |
| `admin:field-packs` | List every user's field packs, and disable or delete them. |
| `attachments:read` | View and download files attached to features. |
| `attachments:write` | Upload, replace, and delete files attached to features. |
| `sync:review` | View the field-sync review queue (device ops held for QA). |
| `sync:post` | Post or reject a reviewed field-sync op, and set the per-class apply policy. |
| `oms:read` | View the outage board: trouble calls, predicted and active incidents. |
| `oms:write` | Log trouble calls, run prediction, and confirm/restore/cancel incidents. |
| `wam:read` | View the work board: defects, work orders, inspections. |
| `wam:write` | Log defects, raise/schedule/assign work orders, record completion. |
| `faults:read` | View the fault board: fault events and their outage/defect links. |
| `faults:write` | Log fault events, link them to outages, and raise defects from them. |
| `tags:read` | Resolve scanned asset tags and view the tags on a feature. |
| `tags:write` | Register legacy tags, set the primary, void, and reissue asset tags. |
| `territories:unscoped` | Bypass territory scoping — see and edit every feature regardless of grants. |

### 14.3 The nine roles

| Role | In a sentence |
|---|---|
| **Viewer** | Read-only across the whole app: features, connectivity, relationships, diagrams, the catalog, annotations, attachments, and the three operational boards. |
| **Analyst** | Everything a viewer can see, **plus running traces and analyses**. |
| **Editor** | Everything an analyst can do, **plus editing** features, connectivity, relationships, diagrams, annotations, attachments, and the three operational boards. |
| **Supervisor** | An editor who can **also delete whole diagrams**. (See the gotcha in §14.5.) |
| **Administrator** | **Full access** — every permission, including the metadata catalog, users, auth settings, territories, circuits, field-pack admin, and sync review. |
| **Break-glass admin** | The same full permission set as admin, **plus** the emergency local-login flag and an unconditional override (§14.7). |
| **Field surveyor** | Read-only field capture: view features and connectivity, take photos/attachments. Cannot edit features. |
| **Field enumerator** | Surveyor **plus creating/editing features** in the field. |
| **Field technician** | Enumerator **plus the work board and faults** — log defects, run work orders, record inspections, log/clear faults. |

A person typically holds **one** role, but roles stack: assign both and they get the union.

### 14.4 The role → permission matrix

The definitive grid. **✓** = the role holds that permission. Column keys: **V** Viewer · **An** Analyst · **Ed** Editor · **Sup** Supervisor · **Adm** Administrator · **BG** Break-glass admin · **FS** Field surveyor · **FE** Field enumerator · **FT** Field technician.

| Permission | V | An | Ed | Sup | Adm | BG | FS | FE | FT |
|---|:-:|:-:|:-:|:-:|:-:|:-:|:-:|:-:|:-:|
| `features:read` | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |
| `features:write` |   |   | ✓ | ✓ | ✓ | ✓ |   | ✓ | ✓ |
| `connectivity:read` | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |
| `connectivity:commit` |   |   | ✓ | ✓ | ✓ | ✓ |   |   |   |
| `relationships:read` | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |   |   |   |
| `relationships:commit` |   |   | ✓ | ✓ | ✓ | ✓ |   |   |   |
| `diagrams:read` | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |   |   |   |
| `diagrams:write` |   |   | ✓ | ✓ | ✓ | ✓ |   |   |   |
| `diagrams:delete` |   |   |   | ✓ | ✓ | ✓ |   |   |   |
| `annotations:read` | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |   |   |   |
| `annotations:write` |   |   | ✓ | ✓ | ✓ | ✓ |   |   |   |
| `trace:run` |   | ✓ | ✓ | ✓ | ✓ | ✓ |   |   |   |
| `analysis:run` |   | ✓ | ✓ | ✓ | ✓ | ✓ |   |   |   |
| `metadata:read` | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |   |   |   |
| `metadata:write` |   |   |   |   | ✓ | ✓ |   |   |   |
| `admin:auth-settings` |   |   |   |   | ✓ | ✓ |   |   |   |
| `admin:users` |   |   |   |   | ✓ | ✓ |   |   |   |
| `territories:write` |   |   |   |   | ✓ | ✓ |   |   |   |
| `circuits:write` |   |   |   |   | ✓ | ✓ |   |   |   |
| `admin:field-packs` |   |   |   |   | ✓ | ✓ |   |   |   |
| `attachments:read` | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |
| `attachments:write` |   |   | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |
| `sync:review` |   |   |   |   | ✓ | ✓ |   |   |   |
| `sync:post` |   |   |   |   | ✓ | ✓ |   |   |   |
| `oms:read` | ✓ | ✓ | ✓ |   | ✓ | ✓ |   |   |   |
| `oms:write` |   |   | ✓ |   | ✓ | ✓ |   |   |   |
| `wam:read` | ✓ | ✓ | ✓ |   | ✓ | ✓ |   |   | ✓ |
| `wam:write` |   |   | ✓ |   | ✓ | ✓ |   |   | ✓ |
| `faults:read` | ✓ | ✓ | ✓ |   | ✓ | ✓ |   |   | ✓ |
| `faults:write` |   |   | ✓ |   | ✓ | ✓ |   |   | ✓ |
| `tags:read` | ✓ | ✓ | ✓ |   | ✓ | ✓ |   |   | ✓ |
| `tags:write` |   |   | ✓ |   | ✓ | ✓ |   |   |   |
| `territories:unscoped` |   |   |   |   | ✓ | ✓ |   |   |   |

### 14.5 Reading the matrix — the non-obvious bits

⚠️ **Supervisor does not automatically see the operational boards.** Look closely at the Sup column: it has `diagrams:delete` (that's the whole reason the role exists) and everything editor had *at the time the role was created* — but it is **blank for `oms:*`, `wam:*`, `faults:*`, and `tags:*`**. That's not an oversight in this table; it's how the role was defined. Supervisor was seeded as a *snapshot* of the editor role plus diagram-deletion, and the outage, works, fault, and asset-tag capabilities were added to the system **later**. Because those later grants named viewer/analyst/editor/admin/break-glass explicitly — not supervisor — a plain supervisor can delete diagrams but **cannot open Outages, Works, or Faults**. If you need someone who can both delete diagrams *and* run the operational boards, assign them **both** the supervisor and editor roles (remember, roles union). This is the single most surprising row-reading in the matrix, so it's worth internalising.

💡 **Field roles are deliberately narrow.** The three field tiers hold only what a device needs: features, connectivity-read, and attachments for all of them; feature-editing from enumerator up; the work board, faults, and asset-tag scanning (`tags:read`) for the technician — scanning an asset in the field is the technician's whole point. They intentionally lack relationships, diagrams, tracing, analysis, the metadata catalog, annotations, and OMS — a field tool is for capture and works, not planning-office tasks.

💡 **`territories:unscoped` is the fast-path bypass, not a super-power.** It only means "territory scoping doesn't apply to me." It doesn't grant any *capability* — an admin still needs `features:write` to edit. It exists so full-access roles read the map without the scoping predicate slowing them down (§14.6).

### 14.6 Territory scoping — the spatial layer on top

Everything above is the *capability* layer. VoltEdge has a second, orthogonal layer: **territory scoping** (introduced in Chapter 3). Even with `features:write`, you can only touch features **inside the territories you're granted** — a grant on a node in the territory tree expands **down** to its whole subtree, and a manager's reach is the **union** of every grant held by the people who report up to them. Scoping is **ANDed** onto your permissions: *permission says what you may do; territory says where.* Holders of `territories:unscoped` (admin and break-glass) skip the fence entirely. Scoping only bites when the installation's territory-enforcement flag is switched on; until then, permissions alone decide.

*Worked recap (the territory cast):* **Sam** is granted the *Riverside District* node, so he can edit every zone and plot beneath it — Oakwood Zone, Millbrook Zone — without a separate grant for each. **Mia** manages Sam and Raj; her effective reach is the union of theirs, so she sees Riverside *and* whatever Raj holds, with no direct grant of her own. **Alex**, an admin with `territories:unscoped`, sees the entire network regardless.

### 14.7 The break-glass admin — what it is and isn't

Break-glass admin holds the *same* permission set as an ordinary administrator, but with two differences you can see in the system's behaviour:

- **Emergency local login.** A break-glass account can sign in with a local email + password even when normal single-sign-on is unavailable — it's the "in case of emergency, break glass" account that keeps you from being locked out of your own system.
- **Unconditional override.** Break-glass sits *above* the whole RBAC-plus-territory model: it can see and act on everything, including private drafts and territory-scoped data, without needing any special grant.

Because of that reach, break-glass is meant to be a rarely-used, closely-guarded account — its sign-ins are specifically audit-logged. The **companion admin & break-glass developer guide** (`docs/admin-break-glass-guide.md`) is the reference for operating VoltEdge from that seat: seeding, migrations, the metadata compiler, factory reset, and the rest. This handbook stops at the everyday roles; that guide picks up where the "full access" column ends.

### 14.8 Capability → chapter → permission quick reference

The reverse index: "I want to do *X* — where's it documented and what do I need?"

| I want to… | Chapter | Permission needed |
|---|---|---|
| Find my way around the map, layers, basemap, status bar | 2 | `features:read` |
| Understand roles, permissions, territory scope | 3, 13 | — |
| Browse & filter feature data; query/aggregate; radius/territory scope | 5 | `features:read` |
| Create, edit, publish, delete, restore features | 6 | `features:write` (+ territory) |
| Change a feature's subtype | 6 | `features:write` |
| Private drafts (author + management chain only) | 6 | `features:write` |
| Establish/revoke electrical connectivity | 7 | `connectivity:commit` |
| Establish/revoke structural/spatial relationships | 7 | `relationships:commit` |
| Guided assemblies, feeder ties, bus couplers | 7 | `features:write` + `connectivity:commit` |
| Run traces (feeder/section/isolation/supply-path/configurable) | 8 | `trace:run` |
| Run analyses (load flow, fault, ampacity, hosting, EV, arc-flash…) | 8 | `analysis:run` |
| Run the outage board (trouble calls, incidents, switching orders, indices) | 9 | `oms:read` / `oms:write` |
| Log & clear faults; raise fault from incident; book defect from fault | 9 | `faults:read` / `faults:write` |
| Run the work board (defects, work orders, inspections) | 9 | `wam:read` / `wam:write` |
| Create & edit schematic diagrams; place features; sectionalise; DXF export | 10 | `diagrams:read` / `diagrams:write` |
| Delete an entire diagram | 10 | `diagrams:delete` |
| Redline the map (annotations: text/box/line-arrow) | 10 | `annotations:read` / `annotations:write` |
| Attach photos/documents to features | 6, 11 | `attachments:read` / `attachments:write` |
| Scan/resolve an asset tag; view a feature's tags; print labels | 13 | `tags:read` |
| Register a legacy code; set primary; void; reissue | 13 | `tags:write` |
| Bulk-print labels from the Features browser | 4, 13 | `tags:read` |
| Find features by tag (query filter) | 5, 13 | `features:read` + `tags:read` |
| Take VoltEdge into the field offline (packs, capture, sync) | 11 | role-dependent field tier |
| Review & post field-sync ops (QA queue) | 11 | `sync:review` / `sync:post` |
| Overlay third-party map layers; click-to-identify | 12 | `features:read` (use) / `metadata:write` (manage) |
| Use the map assistant (suggestions + chat) | 12 | bounded by your own permissions |
| Edit the metadata catalog & compile symbology | admin guide | `metadata:write` |
| Manage users & role assignments | admin guide | `admin:users` |
| Manage territories / circuits | admin guide | `territories:write` / `circuits:write` |
| Manage everyone's field packs | admin guide | `admin:field-packs` |
| Change auth / break-glass settings | admin guide | `admin:auth-settings` |

### 14.9 Glossary

- **Feature** — a real network asset (transformer, cable, switch), an instance of a *feature class*, living in that class's table.
- **Feature class** — a metadata-defined asset type with its own attributes, symbology, and rules; compiled from the catalog.
- **Subtype** — a variant within a class (e.g. overhead vs underground conductor) that can change the required fields.
- **Connectivity** — electrical wiring between features' terminals; distinct from…
- **Relationship** — a structural or spatial link (containment, support, spatial-support) that carries no current.
- **Trace** — a graph walk over connectivity from a starting feature, stopping at *barriers* (open devices/controllers).
- **Analysis** — a numeric engineering calculation (power flow, fault current, ampacity, hosting capacity…) run on the network.
- **Territory** — a node in the administrative-area tree used both to classify features and to scope access.
- **Circuit** — a named electrical circuit a feature can belong to; `circuit_ref` is its derived label.
- **Asset tag** — a short scannable code (`VE-…`) minted for every feature and printed as a QR/Code128/text label affixed on the plant; a scan resolves it back to the record. A feature may carry many codes, one *primary* (Chapter 13).
- **OMS / WAM / Faults** — the three operational boards: outages, works/defects, and fault events — the "operations triangle."
- **Diagram** — a container-bound schematic in its own local coordinate space; features are *placed* onto it.
- **Annotation** — free map markup (redlining); points at no feature.
- **Field pack** — a bbox-bounded offline bundle (basemap + editable data) whose contents are frozen from the requester's permissions.
- **Service layer** — an external/third-party map service overlaid on the map.
- **Break-glass** — the emergency full-override admin account.
- **RBAC** — role-based access control: the permission/role/user model of this chapter.

### 14.10 Closing

That completes the VoltEdge **User Handbook**. You've gone from orientation (Chapters 1–4), through working with data and the network (5–8), the operational boards and schematics (9–10), the field tool (11), external layers and the assistant (12), asset tags and labels (13), and now this reference (14). Keep §14.4 and §14.8 within reach — between them they answer almost every "can I…?" and "where's…?" question that comes up day to day.

For everything on the *other* side of the "full access" column — running, seeding, compiling, and administering VoltEdge itself — turn to the companion **Admin & break-glass developer guide**.

*End of the User Handbook.*
