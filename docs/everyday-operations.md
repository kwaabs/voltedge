# VoltEdge Everyday Operations — the task cookbook

The hands-on companion to the [User Handbook](user-handbook.md). Where the
handbook explains *what VoltEdge is* and *why it behaves as it does*, this book is
organised around **what you actually do** — one recipe per task. Open a feature,
draw a new one, fix its shape, measure a span, snap to a pole, connect two assets,
drop a label. If you've ever thought *"how do I just…"*, this is the book that
answers it.

## How to use this book

Every recipe is built the same way, so you can read it at whatever depth you need:

- **▶ Quick steps** — a terse numbered list. When you know the tool and just need
  the sequence, read only this and go.
- **Worked example** — the same task carried out in full by one of our recurring
  characters, with the *why* behind each step. Read this the first time, or when a
  quick step doesn't behave as you expected.
- **How it works** and **Tips & gotchas** — the grounding and the sharp edges, so
  the recipe holds up when the network gets messy.

You do **not** have to read this book front to back. Jump to the task you have in
hand. Chapter 1 is the exception — read it once to learn the workspace and the
tools every other chapter assumes.

### The cast

The same people appear throughout, so a recipe reads like watching a colleague
rather than parsing a spec:

| Who | Role | Everyday permissions |
| --- | --- | --- |
| **Dana** | Network editor | `features:read` + `features:write` — creates, edits, connects, deletes |
| **Priya** | Viewer / planner | `features:read` — browses, measures, queries, traces (no edits) |
| **Tom** | Field & commissioning | works in the field app *and* the web map; scans, verifies, places assets |
| **Ada** | Data administrator | the admin surfaces; sets defaults, manages territories and metadata |

Running location: the **Oakwood** zone — Feeder 7, a scatter of distribution
transformers, and the primary substation Dana and Tom keep tidy.

### Legend

- 💡 a tip that saves time
- ⚠️ a caution — something that surprises people
- 🖱️ a mouse / on-screen interaction
- ⌨️ a keyboard action
- 🗺️ something specific to the map

### Chapter roadmap

1. **[The map & your tools](#chapter-1--the-map--your-tools)** — the workspace, getting around, the layers panel, and a tour of every everyday tool.
2. **Finding & opening things** — search, click-to-open, the Features browser, and select-vs-highlight.
3. **Creating features** — every geometry type, from a point transformer to a multi-vertex line.
4. **Editing attributes** — changing a feature's values, the subtype, and required-field rules.
5. **Editing geometry** — moving, reshaping, splitting a line, and deleting/restoring.
6. **Measuring & drawing** — ad-hoc distance and area, units, and reading a feature's own length/area.
7. **Snapping & precision** — snapping to existing assets so connections actually line up.
8. **Connecting & relating** — terminals, connections, relationships, and guided assemblies.
9. **Annotating the map** — map annotations, labels, attachments, and external links.
10. **Personalising & getting unstuck** — basemaps, dark mode, remembered view, units, and a "why can't I…?" list.
11. **Attaching files & querying the network** — hang photos/documents on a feature, and ask the data questions: filter, count, group, scope by area, drill down, and export.

---

## Chapter 1 — The map & your tools

The map is where most of an operator's day happens. This chapter is the one piece
of orientation the rest of the book leans on: how to move around, how to control
what's drawn, and what each tool on the toolbar is *for*. Read it once; after that
every other recipe will name a tool and assume you know where it lives.

### 1.1 The workspace at a glance 🗺️

Open **Map** — it's where VoltEdge lands you. Four things frame everything you'll
do:

- **The map canvas** — your network drawn over a basemap, filling the screen.
- **The toolbar** — the buttons and menus you *act with* (create, select, measure,
  snap…). §1.5 tours them.
- **Floating panels** — detail that appears *over* the map (a feature's panel,
  trace results, the query builder). They move and resize; §1.6.
- **The status bar** — a quiet strip along the bottom with live readouts of where
  and how you're looking; §1.4.

> For the *concepts* behind the workspace — signing in, the data model,
> permissions — see [User Handbook, Chapter 2](user-handbook.md). This book stays
> on the doing.

### 1.2 Getting around: pan, zoom, box-zoom

**▶ Quick steps**

1. **Pan** — 🖱️ click-drag the map.
2. **Zoom** — mouse wheel / trackpad, or the on-screen `+` / `−`.
3. **Jump to an area precisely** — click **Box-zoom** on the toolbar, then 🖱️ drag
   a rectangle around the street or substation; the map fits exactly that box.
4. **Straighten the map** — if you've rotated it off north, click **Reset north**
   (the compass button) to animate back to north-up.

**Worked example.** Priya needs to look at a specific pole-top on Feeder 7. She
*could* wheel-zoom in stages, but instead she clicks **Box-zoom** and drags a small
rectangle around the junction — one gesture and the map frames exactly that
corner. Done inspecting, she notices she'd nudged the map's rotation while
panning; the compass button has appeared, so she clicks it and the view snaps back
to north-up.

**How it works.** 🗺️ As you zoom, the map redraws, and **some feature classes and
labels only appear once you're zoomed in far enough** — an administrator sets those
thresholds so a country-wide view isn't buried in detail. 💡 If a class of asset
"isn't on the map," the two first things to check are (a) are you zoomed in past
its threshold, and (b) is its layer switched on (§1.3).

💡 **Where the map opens.** The *first* time you open the map in a session it starts
at your organisation's configured default. After that, VoltEdge **remembers your
last centre and zoom** as you move between pages — hop to the Features browser and
back and you're right where you left off (for the current session).

### 1.3 Controlling what's drawn: the layers panel & basemaps

**▶ Quick steps**

1. Open the **Layers** panel from the toolbar.
2. **Show/hide a feature class** — toggle transformers, switches, conductors, … on
   or off to declutter.
3. **Bring in operational overlays** — toggle **Outages** and **Faults** to lay
   live context over the network.
4. **Adjust an external layer** — for any configured third-party layer, toggle it
   and drag its **opacity** so it blends rather than blots out your network.
5. **Switch the background** — use the **basemap** picker (bottom of the map) for
   streets, satellite, or a plain/blank backdrop.

**Worked example.** Dana is about to add a transformer and wants a clean canvas.
She opens the Layers panel and switches off half a dozen classes she doesn't need,
leaving only conductors, poles, and transformers visible. She flips the basemap
from streets to the **blank** backdrop so nothing competes with her network —
and because the blank basemap adapts to the theme, it stays dark in her dark-mode
session rather than glaring white.

**How it works.** The layers panel is your control board for *what is drawn*. It's
the first place to look when an expected asset type is missing — it may simply be
toggled off here, or hidden by the zoom thresholds from §1.2. Feature classes,
operational overlays (OMS/Faults), and any external service layers each get their
own section.

### 1.4 The status bar

Along the bottom sits a strip of live readouts:

- **Cursor position** — the longitude/latitude under your pointer, updating as you
  move.
- **Zoom** — a live `Zoom: N.N`, handy when you're matching those class/label
  thresholds.
- **Projection** and **basemap** — the coordinate system and which background is
  showing.

⚠️ The status bar belongs to the *map*: in a split map-and-diagram view it spans
only the map half, and it's replaced by the diagram's own status bar when you're
working on a diagram.

### 1.5 Your everyday toolbar — a tour

These are the tools you'll reach for daily. Each has its own recipe later; here's
what each one *is*, so the later chapters can be brief.

| Tool | Icon/label | What it's for | Recipe |
| --- | --- | --- | --- |
| **New feature** | ＋ menu | Draw a brand-new asset — pick a class, then place its geometry | Ch 3 |
| **New assembly** | kit menu | Drop a whole pre-wired kit (e.g. a pole-mounted transformer) in one action | Ch 8 |
| **Select** | 🖱️ marquee | Highlight several features at once *without* opening panels | §1.5.1 |
| **Measure** | Ruler | Ad-hoc distance or area on the map — ephemeral, nothing is saved | Ch 6 |
| **Snap** | magnet toggle | Make your cursor stick to existing features while drawing/editing | Ch 7 |
| **Box-zoom** | dashed box | Zoom to a dragged rectangle (§1.2) | — |
| **Radius query** | circle | Ask "what's within N metres of here?" (a spatial query) | Ch 2 |
| **Units** | m/ft toggle | Switch measurement units (appears while measuring) | Ch 6 |
| **Reset north** | compass | Animate a rotated map back to north-up (§1.2) | — |
| **Layers / Basemap** | stack / map | Control what's drawn and the background (§1.3) | — |

#### 1.5.1 Select vs. a plain click 🖱️

This trips people up, so learn it now:

- **A plain single click** on a feature **opens its detail panel** — this is how you
  inspect or edit *one* thing (Chapter 2).
- **The Select tool** is a **highlight-only multi-select** — click features to
  toggle them into a set, or drag a **marquee** box to add everything inside it, all
  *without* opening any panel. ⌨️ Press **Escape** to clear and exit.

Use a plain click when you want to *work on one* feature; use **Select** when you
want to visually *gather a set* — for example, eyeballing every transformer along a
street before running a query on them.

### 1.6 Floating panels — they move and resize

Much of VoltEdge's detail appears in **floating panels** over the map: the feature
panel, trace and analysis results, the query builder, the assistant. They share
behaviour worth knowing on day one:

- They **float** over the map so you keep your geographic context.
- Most are **draggable** (grab the header to reposition) and **resizable** (drag the
  grip), so you can arrange your workspace.
- 💡 When a panel covers something you need to see, you don't have to close it —
  just drag it aside.

### 1.7 Light/dark mode, and picking up where you left off

- **Theme** — VoltEdge has light and dark modes; the whole interface, including the
  blank basemap and the panels, adapts. Switch via the theme control.
- **Remembered view** — as noted in §1.2, the map keeps your last centre and zoom
  for the session, so navigating away and back doesn't lose your place.

### 1.8 One-card recap

| I want to… | Do this |
| --- | --- |
| Move around | Drag to pan; wheel to zoom; **Box-zoom** for a precise jump |
| Face north again | **Reset north** (compass) |
| Hide clutter | **Layers** panel — toggle classes off |
| Change the background | **Basemap** picker (bottom of map) |
| Inspect one feature | 🖱️ single-click it → its panel opens |
| Grab several at once | **Select** tool → click / marquee → Escape to clear |
| Measure something | **Measure** → Distance or Area (Ch 6) |
| See where my cursor is | Read the **status bar** (bottom) |

Next: **Chapter 2 — Finding & opening things**, where we turn "I know it's out
there somewhere" into an open feature panel — by search, by click, and through the
Features browser.

---

## Chapter 2 — Finding & opening things

Before you can edit, measure, or connect a thing, you have to *get to it*. VoltEdge
gives you several routes to the same destination — an open feature — and the right
one depends on how you know the asset: by where it is, by what class it is, by an
attribute, by proximity, or by the tag stuck to its side. This chapter is a menu of
those routes. Pick the one that matches what you already know.

### 2.0 Which route? — a chooser

| I know… | Fastest route | Section |
| --- | --- | --- |
| **Where it is** on the map | 🖱️ Single-click it | §2.1 |
| **What class** it is (and want to page its instances) | The **Features browser** | §2.2 |
| **An attribute** ("all 315 kVA transformers", "status = de-energised") | The **Query builder**, Filter mode | §2.3 |
| **Roughly where** ("everything within 200 m of here") | The **Query builder**, Radius scope | §2.4 |
| **The tag code** on the asset | Scan or type the code | §2.5 |
| It has **no map geometry** at all (a relay, a bay, a panel) | The **Features browser** — the *only* place it's visible | §2.2 |

### 2.1 Open one feature by clicking it 🖱️

**▶ Quick steps**

1. Zoom to where the asset is (Box-zoom from §1.2 is quick).
2. 🖱️ **Single-click** the feature.
3. Its **detail panel** opens over the map — attributes, terminals, connections,
   relationships, history, tags.
4. If several features are stacked under your click, a small **disambiguation
   list** appears instead — click the one you meant.

**Worked example.** Priya wants to read the nameplate of a transformer she can see
on Feeder 7. She box-zooms to the pole, single-clicks the transformer symbol, and
its panel slides in — she reads the rating without changing anything, because a
plain click *opens*, it doesn't edit. A few streets over she clicks again but the
transformer sits right on top of a fuse and a pole; instead of guessing, VoltEdge
shows her a short **candidates** list of the three stacked features and she picks
the transformer from it.

**How it works.** A plain click writes the hit into the selection store, which the
feature panel reads and opens. When a single click lands on **several** overlapping
features, VoltEdge deliberately does *not* guess — it shows the stacked candidates
for you to disambiguate (the same picker connect-mode uses for choosing a target).
A click that resolves to exactly one feature — including picking one out of the
candidates list — goes straight to the open panel.

- 💡 A single click is always **safe**: it only *opens*. Nothing is written until
  you deliberately edit (Chapter 4) or move geometry (Chapter 5).
- ⚠️ **Select ≠ click.** The **Select** tool (§1.5.1) highlights features *without*
  opening any panel. If clicking doesn't open a panel, check you're not still in the
  Select tool — press **Escape** to leave it.
- 🗺️ Only classes that **have geometry** and are **switched on** and **past their
  zoom threshold** are clickable on the map. Can't click what you can't see —
  revisit §1.2–§1.3, or use the browser (§2.2).

### 2.2 Page a class in the Features browser

The **Features browser** (left-nav → **Features**) is the home for *every compiled
feature class*, and the **only** place the geometry-less classes ever appear —
protective relays, bays, substation panels, station batteries and the like never
draw on the map, so this is where you read them.

**▶ Quick steps**

1. Open **Features** from the sidebar.
2. In the left list, **filter by class** — type in the *"Search classes…"* box (it
   matches the class **name/code**, not individual features).
3. Click a class to load its **rows** on the right.
4. **Expand** any row (the chevron) to read its full, schema-labelled attributes.
5. For a row that has geometry, click **Show on map** (the pin icon) to jump to the
   map with that feature selected and centred.

**Worked example.** Dana needs to audit every protective relay in Oakwood, but
relays have no map symbol — they're pure records. She opens the Features browser,
types "relay" to filter the class list down to `PROTECTIVE_RELAY`, and clicks it.
The right pane pages through every relay; she expands three of them to compare their
settings inline. Later she's in the browser looking at a transformer row and wants
to see *where* it is, so she clicks its **Show on map** pin — VoltEdge selects that
transformer and drops her on the map centred right on it, even though it wasn't in
her last viewport.

**How it works.** The left search box is a **class filter**, not a feature search —
it narrows the ~dozens of classes so you can find the one you want, then you page
*that* class's rows. A row only offers **Show on map** when it actually carries
geometry; geometry-less classes are flagged with a struck-through pin so you know up
front they live only in the browser. **Show on map** uses the same *select-then-jump*
mechanism as OMS "Show on map": it selects the feature (asking the map to pan/zoom to
it even if off-screen) and navigates you to the map.

- 💡 A row's **status** badge (and any *Private edit / Private delete* badge) shows
  right on the row, so you can spot a de-energised or privately-edited asset while
  paging.
- ⚠️ Searching here will **never** find a feature by its *name* or attributes — for
  that, use the Query builder (§2.3).

### 2.3 Find features by an attribute — the Query builder (Filter mode)

When you know *something about* the features but not where they are — a rating, a
status, a name fragment — build a query.

**▶ Quick steps**

1. On the map, open the **Query** builder.
2. Pick the **feature class** to query.
3. In **Filter** mode, add one or more **conditions** — a field, an operator
   (`equals`, `not equals`, `<`, `≤`, `>`, `≥`, `contains`, `starts with`, …), and a
   value. Nest groups with **AND/OR** if you need to.
4. **Run** the query.
5. Click a result row's **zoom** button to select that feature and fly the map to
   it; use **Download** to export the result set.

**Worked example.** Tom is commissioning and needs every 315 kVA distribution
transformer on the network. He opens the Query builder, chooses `DIST_XFMR`, adds
the condition *rated capacity **equals** 315*, and runs it. A list comes back; he
clicks the zoom button beside each row to hop the map straight to that transformer,
verifying each in turn. When he's done he hits **Download** to keep the list.

**How it works.** The operators and nesting the panel offers mirror the backend
query engine exactly — the UI never sends a condition or a nesting depth the server
would reject (groups nest up to five deep). Every result row carries a **zoom**
button that selects the feature *with focus*, so the map pans and zooms to it even if
it wasn't loaded — the same behaviour as the trace results table. Results respect
your **territory access**, so you only ever get features you're allowed to see.

- 💡 Switch the panel to **Aggregate** mode for *counts* instead of rows — count,
  count-by-field, or sum/avg/min/max of a numeric field, plus an *all-classes*
  breakdown of "what's on the map within this scope." Drill a count down to the rows
  behind it and highlight them on the map.
- 💡 There's a dedicated **tag** filter, too — "features that *have* a tag" or match
  a tag code/prefix. See the [Asset Tags Guide](asset-tags-guide.md), find-by-tag.

### 2.4 Find everything near a point — the Radius scope

"What's within N metres of here?" is a spatial question, answered by giving a query
a **radius scope** rather than a new panel.

**▶ Quick steps**

1. Open the **Query** builder and go to **Aggregate** mode (or the All-classes
   breakdown).
2. Set the **scope** to **Radius of a point**.
3. 🖱️ Click a **centre** on the map and choose a **radius** (there are presets).
4. **Run** — you get the count/breakdown of everything inside the circle, which
   stays drawn while the scope is active.

**Worked example.** After a car hits a pole, Priya needs to know what plant sits
within 150 m of it. She opens the Query builder, switches to the all-classes
breakdown, sets the scope to **Radius of a point**, clicks the pole as the centre and
picks 150 m. VoltEdge draws the circle and returns a per-class tally of everything
inside it — three spans of conductor, one transformer, two poles — so she can brief
the crew before they roll.

**How it works.** Radius scope shares the map's radius-query store: selecting it arms
the map so your next click sets the centre, and the geodesic circle is drawn only
while that scope is active — leave the scope and the circle clears. The scope is sent
to the same aggregate endpoint as a `{kind: "radius", center, radius_m}` object, so
the count you see is computed server-side against real geometry, not eyeballed.

> Radius querying used to be a standalone map tool; it now lives inside the Query
> builder as a **scope**, alongside **territory** scope ("everything under this part
> of the tree"). One panel, three scopes: none, territory, radius.

### 2.5 Find a feature by its tag

If the asset in front of you carries a VoltEdge tag, the code *is* the address.

**▶ Quick steps**

1. **In the field app:** scan the QR/barcode, or type the `VE-…` code.
2. **On the web:** open the tag deep-link `/t/<code>`, or use the **tag** filter in
   the Query builder.
3. You land on the resolved feature — subject to your territory and draft access.

**Worked example.** Tom finds an unlabelled-looking transformer but spots a faded
tag reading `VE-7Q3K2`. In the field app he scans it; the app resolves the code to
the transformer's record and opens it, working offline first and falling back to the
server if needed.

**How it works.** This is a whole subsystem with its own manual — minting, printing,
scanning, offline resolution and querying by tag are covered end-to-end in the
**[Asset Tags Guide](asset-tags-guide.md)**. The one thing to carry here: a tag
resolves through the same **territory + draft** gate as everything else, so a code
never leaks a feature you aren't allowed to see.

### 2.6 One-card recap

| I want to… | Do this |
| --- | --- |
| Open a feature I can see | 🖱️ Single-click it → panel opens |
| Choose between stacked features | Pick from the candidates list |
| Read a geometry-less class (relay, bay, panel) | **Features** browser → pick the class |
| Jump from a browser row to the map | **Show on map** (pin icon) |
| Find features by an attribute | **Query** → Filter mode → conditions → Run |
| Count / roll up features | **Query** → Aggregate mode |
| Find everything near a point | **Query** → scope **Radius of a point** |
| Find by tag | Scan / `/t/<code>` / Query **tag** filter |
| Highlight a set without opening panels | **Select** tool (§1.5.1) |

Next: **Chapter 3 — Creating features**, where we go from an empty patch of map to a
placed, valid asset — one recipe per geometry type.

---

## Chapter 3 — Creating features

Putting a new asset on the map is the editor's bread and butter. Every creation
follows the **same two-step shape** — *draw the geometry, then fill the form* — so
once you've done one you've done them all; the only thing that changes is whether
you're placing a point, a line, or an area. This chapter walks each shape, the form
that follows, and the small niceties (nudge before you commit, duplicate an existing
asset) that make the day faster.

> 🔒 Creating features needs **`features:write`**. Priya (viewer) doesn't see the
> **New feature** button at all; Dana (editor) does. Everything in this chapter is
> Dana's or Tom's to do. 🗺️ You can only create **within your territory** — a save
> outside your access is refused.

### 3.0 The shape of every create

Two steps, always in this order:

1. **Draw the geometry** — pick the class from **New feature**, then click the map to
   lay down its point, line, or area. This creates an unsaved **draft**.
2. **Fill the form & save** — a create form opens in place of the feature panel:
   name (optional), subtype (if the class has them), and the class's create-time
   fields. **Save** commits it; **Cancel** throws the draft away.

Nothing is written to the network until you press **Save** — right up to that moment
a draft is yours alone, and you can even nudge its position (§3.5).

### 3.1 Create a point feature (transformer, pole, switch…)

**▶ Quick steps**

1. Click **New feature** on the toolbar.
2. Find the class — the list is grouped by category; type in **Search classes…** to
   filter by name or code (e.g. `busbar`, or `MV_`).
3. Click the class. The banner reads **"Click the map to place the new feature."**
4. 🖱️ **Click** the spot on the map. The create form opens.
5. Fill **Name** (optional), **Subtype** (if asked), and any **required** fields.
6. **Save**.

**Worked example.** Dana is adding a new 315 kVA distribution transformer to a pole on
Feeder 7. She clicks **New feature**, types "dist" to filter down to `DIST_XFMR`, and
picks it. The banner tells her to click the map; she clicks the pole location and the
create form appears. She types the name *TX-Feeder7-014*, chooses the subtype, sets
the required primary-voltage field, and hits **Save**. The transformer appears on the
map, its panel opens, and a moment later the assistant quietly suggests the
connections it could now make (§3.8) — because she drew it right next to the pole and
conductor.

**How it works.** Picking a class starts a *draw session* (the map owns the actual
click-to-place). Finishing the placement turns it into a **draft**; the shared create
form reads the class's schema and shows only the fields marked **visible on create**.
On **Save** it POSTs the name, subtype, geometry and those attributes to the create
endpoint, then selects the new feature so its panel opens.

- 💡 New features are **born PLANNED** — their lifecycle/`operational_state` defaults
  to *Planned*, so a freshly drawn asset doesn't masquerade as energised. Change it
  when you commission (Chapter 4).
- ⌨️ Press **Escape** or click the banner's **✕** to abandon a draw before you've
  placed anything.

### 3.2 Create a line feature (a conductor, a cable run)

A line is a *sequence of clicks*, finished with a double-click.

**▶ Quick steps**

1. **New feature** → pick a line class (e.g. an overhead conductor). Banner:
   **"Click to add points, double-click to finish the line."**
2. 🖱️ **Click** each vertex along the route.
3. 🖱️ **Double-click** the last point to finish.
4. Fill the form and **Save**.

**Worked example.** Tom is drawing an LV service run from the new transformer to a
pillar. He picks the conductor class and starts clicking: the transformer end, a bend
at the kerb, the pillar end — then double-clicks to close the line. Because he wants
the run to actually *connect*, he'd flicked on **Snap** (Chapter 7) before starting,
so his first click locked onto the transformer's exact point rather than landing a
metre off. The form opens; he sets the required fields and saves. The assistant
immediately offers to wire the conductor to the transformer and the pillar it snapped
to.

**How it works.** While a line (or polygon) draw is live, two extra controls appear in
the banner beside **Cancel**: **Snap** (make the cursor stick to existing features —
Chapter 7) and **Ortho** (constrain segments to right angles / straight runs). Each
click adds a vertex; the double-click ends the geometry and drops you into the same
create form as a point.

- 💡 **Snap is the difference between a line that connects and one that only looks
  connected.** Turn it on *before* you place the first vertex when the ends should
  meet existing plant.
- ⚠️ A misclick mid-line isn't fatal — finish the line roughly, then reshape its
  vertices afterward (Chapter 5), or **Cancel** and redraw.

### 3.3 Create an area feature (a polygon)

Same as a line, but the shape closes into an area.

**▶ Quick steps**

1. **New feature** → pick a polygon class. Banner: **"Click to add points,
   double-click to finish the polygon."**
2. 🖱️ Click each corner of the boundary.
3. 🖱️ **Double-click** to close and finish.
4. Fill the form and **Save**.

**Worked example.** Dana outlines a substation compound. She picks the polygon class,
clicks the four corners of the fence line, and double-clicks to close it. The area
fills in, the form opens, she names it and saves. **Ortho** helped her keep the fence
corners square as she went.

**How it works.** Identical to a line — vertices by click, finish by double-click —
except the geometry is closed into a ring. **Snap** and **Ortho** are available in the
banner just as they are for lines.

### 3.4 Filling the create form

The form is the same regardless of shape. Read this once; it applies to every recipe
above.

- **Name** — optional, a human-readable label. Leave it blank and the feature still
  saves.
- **Subtype** — if the class has subtypes, this is **required** (marked with a red
  `*`); the picker opens on *"Select…"*. The subtype you choose decides which
  attribute fields become required.
- **Attribute fields** — only the class's **create-time** fields show here (not every
  field the class owns). A field marked `*` is **required *for the chosen subtype***,
  so the required set can change the moment you switch subtype.
- **Save** stays disabled until every required piece is present — a missing subtype or
  a blank required field keeps it greyed out.

**Worked example.** Dana starts a `MV_CONDUCTOR` and the form shows a subtype picker.
The moment she chooses *Overhead*, three fields sprout red asterisks (conductor size,
construction, span) that weren't required a second earlier; had she picked
*Underground* the required set would differ. She fills them, **Save** lights up, and
she commits.

- 💡 If **Save** won't enable, scan for a red `*` you haven't filled — most often the
  **Subtype** itself.
- ⚠️ The form only offers **create-time** fields by design; other attributes are set
  later from the feature panel (Chapter 4). You're not missing anything.

### 3.5 Nudge a draft before you commit 🖱️

You don't have to place a point perfectly on the first click.

**▶ Quick steps**

1. After placing a point/line/polygon (form open, not yet saved)…
2. 🖱️ **Drag** the draft's geometry on the map to reposition it.
3. Your typed form values **stay put** through the drag.
4. **Save** when it's where you want it.

**Worked example.** Dana drops the transformer, then notices it's sitting a little off
the pole. Rather than cancel and start over, she drags the draft marker onto the pole;
the name and fields she'd already typed are untouched. Satisfied, she saves.

**How it works.** A draft's geometry can be moved right up until Save, and the form
deliberately preserves what you've entered while you drag — only a genuinely *new*
draft resets the form.

### 3.6 Duplicate an existing feature

The fastest way to make "another one like that."

**▶ Quick steps**

1. Open the feature you want to copy (single-click it — Chapter 2).
2. In its panel, choose **Duplicate**.
3. A draft opens **pre-filled** with the original's name, subtype and attributes.
4. Place it (drag to position), adjust anything, and **Save**.

**Worked example.** Dana needs three more identical LV pillars along a street. She
opens the first, hits **Duplicate**, and a new draft appears already carrying its
subtype and attributes — she just drags it to the next location and saves, twice more.
Three consistent pillars in a fraction of the time.

**How it works.** Duplicate seeds a fresh draft from the source feature's values, then
runs it through the *same* create form and save path as a blank draw — so validation,
the visible-on-create filter, and the post-save select all behave identically. Copied
attributes the create validator wouldn't accept (computed/invisible ones) are dropped
automatically.

### 3.7 What you *can't* create from the map here

Two honest exclusions:

- **Geometry-less classes** (relays, bays, panels, station batteries) have no map
  shape, so they're **not** in the **New feature** list. They come into being another
  way — as part of a composite (a switchgear *panel* fans out its devices), or by
  digitising onto a **diagram**. See the substation and feature-catalog guides.
- **Assemblies / kits** (a Ring Main Unit, a packaged substation, a pole-mounted
  transformer kit, a cable duct, a bus coupler, a feeder tie) are **not** placed as
  bare features here — drawing one plainly would skip the wiring that makes it useful.
  They live on the separate **New assembly** menu and are covered in **Chapter 8**.

### 3.8 After you save: the connect suggestions

The moment a feature with real geometry lands, VoltEdge's assistant looks at what's
nearby and **suggests the connections and relationships you could now make** — the new
conductor to the transformer it touches, the transformer to its pole. You don't have
to accept them, but it's the natural next step; connecting is Chapter 8.

### 3.9 One-card recap

| I want to… | Do this |
| --- | --- |
| Place a point asset | **New feature** → class → click the map → form → **Save** |
| Draw a conductor/cable | **New feature** → line class → click vertices → **double-click** to finish |
| Draw an area | **New feature** → polygon class → click corners → **double-click** |
| Make the ends actually connect | Turn on **Snap** *before* the first click (Ch 7) |
| Keep segments square | Turn on **Ortho** while drawing a line/polygon |
| Fix a required-field block | Fill every red `*` — start with **Subtype** |
| Reposition before saving | 🖱️ Drag the draft; typed values survive |
| Make another like this one | Open it → **Duplicate** → place → **Save** |
| Abandon a draw | **Escape** / banner **✕** / **Cancel** |
| Place a whole kit (RMU, pad-mount…) | **New assembly** menu (Ch 8) |

Next: **Chapter 4 — Editing attributes**, where we change a saved feature's values —
its fields, its subtype, and the required-field rules that come with a subtype change.

---

## Chapter 4 — Editing attributes

A feature is rarely right — or rarely stays right — the moment it's drawn. You
commission it, re-rate it, rename it, correct a typo, swap it from overhead to
underground. This chapter is about changing a *saved* feature's values: its name, its
fields, and its subtype. Geometry — moving and reshaping — is Chapter 5; here we stay
on the attributes, though the same **Save** commits both at once if you've done both.

> 🔒 Reading a feature needs only `features:read`; **saving an edit needs
> `features:write`**. Priya can open any feature and read every value; the edits in
> this chapter are Dana's and Tom's. 🗺️ You can only edit features **within your
> territory**.

### 4.0 How editing works

Editing is **commit-on-save**, exactly like creating: your changes live in the form
until you press **Save**, and only then are they written in one request. Nothing is
staged on the server as you type.

**▶ Quick steps**

1. Open the feature (single-click it — Chapter 2).
2. Click the **Edit** (pencil) button in the panel header.
3. The read-only Attributes tab is replaced by the **edit form**, seeded with the
   feature's current values.
4. Change what you need; **Save** commits, **Cancel** discards.

### 4.1 Change a field value

**▶ Quick steps**

1. Open the feature → **Edit** (pencil).
2. Change one or more fields.
3. **Save**.

**Worked example.** A distribution transformer on Feeder 7 was mislabelled as 200 kVA;
it's actually 315. Dana single-clicks it, hits the pencil, and the edit form opens with
every current value in place. She corrects the rating field and clicks **Save**. The
panel returns to read-only showing the new value, and the map redraws — even though she
only touched an attribute — because a value change can shift how the feature is drawn.

**How it works.** The form shows the class's **update-time** fields (which can differ
from the create-time set). On **Save** it PATCHes **only the fields you actually
changed** — untouched values aren't resent. After a successful save VoltEdge refreshes
the map tile for that feature, because an attribute-only change (say a switch's
`device_state`) can resolve to different **symbology**, so the map must show the new
look, not just a moved shape.

- 💡 **Save only lights up when something's changed.** If it's greyed out, you haven't
  altered anything yet (or a required field is now blank — §4.3).
- 💡 You can change **just** the name, **just** an attribute, **just** the subtype,
  **just** the geometry (Chapter 5), or any mix — one **Save** commits the lot.

### 4.2 Rename a feature (or clear its name)

**▶ Quick steps**

1. Open → **Edit**.
2. Type a new **Name**, or **clear the box** to remove the name entirely.
3. **Save**.

**Worked example.** Dana renames a freshly commissioned transformer from a working
label to its official *TX-Feeder7-014*. Later she opens a different asset whose name
was entered by mistake, backspaces the **Name** box empty, and saves — the feature
falls back to showing its class name and id, no stray label left behind.

**How it works.** A present name is set to exactly what you typed (trimmed); an
explicitly emptied box **clears** the name rather than leaving whitespace. With a name,
the panel promotes it to the headline and demotes the class/id beneath; without one, it
shows the class name and short id as before.

### 4.3 Change the subtype

Some corrections are a change of *kind* — an overhead conductor rebuilt as underground,
for instance. Subtype is editable, and it can change which fields are **required**.

**▶ Quick steps**

1. Open → **Edit**.
2. Pick a new value in the **Subtype** dropdown.
3. Any fields the new subtype requires sprout a red `*` — **fill them**.
4. **Save**.

**Worked example.** A span on Feeder 7 that was modelled overhead has been put
underground in a road-crossing rebuild. It's one `MV_CONDUCTOR` class with overhead and
underground subtypes, so Dana doesn't delete and redraw — she opens the conductor, hits
**Edit**, and switches the subtype from *Overhead* to *Underground*. A hint appears —
*"Changing the subtype — fill any newly required fields (marked *) before saving"* — and
two fields that weren't required before now show asterisks. She fills them and saves;
**Save** stayed disabled until she did.

**How it works.** Required-ness follows the **currently selected** subtype, not the one
stored on disk — so the moment you switch, the form demands the new subtype's required
set, exactly what the server re-checks on save. The server validates the new subtype's
required fields against the *merge* of the existing row plus your edits, so the values
you fill in ride along in the same PATCH.

- ⚠️ If **Save** won't enable after a subtype change, a newly-required field is still
  blank — look for the red `*`.

### 4.4 Fields that lock once published

Not every field stays editable forever. A **published** feature shows certain fields
**read-only**, marked *"(locked — published)"*.

**Worked example.** Tom opens a published pole to tweak it and notices one identity
field is shown as plain text with a *(locked — published)* note instead of an input.
That field was deliberately frozen at publish time; the rest of the pole's attributes
he can still edit and save normally.

**How it works.** A field whose class marks it *not editable after publish* is rendered
read-only on a published feature — the form shows it that way rather than offering an
input a **Save** would just bounce with a 400. The client mirrors the server's own rule,
so what you see editable is exactly what the server will accept.

- 💡 Locked fields are a property of the **class design**, not your permissions — no one
  edits them on a published feature through the normal form. (Changing them is an
  administrative concern; see the Admin & Break-Glass guide.)

### 4.5 When someone edited it first — the concurrency guard

Two people can open the same feature. VoltEdge makes sure the second save doesn't
silently wipe the first.

**Worked example.** Dana opens a transformer and starts editing. Meanwhile a colleague
saves a change to the same transformer. When Dana clicks **Save**, she gets a message
that the feature changed since she opened it, and the panel refreshes to show the
current values — but **her in-progress edits are kept** so she can review them against
the new state and re-save deliberately, rather than losing her work or clobbering theirs.

**How it works.** The save carries the `updated_at` the form loaded as an
optimistic-concurrency token. If the server sees the feature has moved on, it rejects
the save with a **409** and the app surfaces the reload-and-reapply message, refreshes
the underlying feature, and leaves your edits in the form. Every other error just shows
the server's message.

### 4.6 What a successful save does

So you know what to expect after clicking **Save**:

- The feature's cached values are refreshed, and the panel returns to **read-only**
  showing the new state.
- The **map tile refreshes** — always, not only for geometry — because a value change
  can change symbology.
- If you changed **geometry** (Chapter 5) in the same edit, the assistant then offers
  any connections/relationships the new position makes possible (Chapter 8). An
  attribute-only edit skips that — its connectivity candidates haven't moved.

### 4.7 One-card recap

| I want to… | Do this |
| --- | --- |
| Edit a feature | Open it → **Edit** (pencil) → change → **Save** |
| Change one value | Edit → change the field → **Save** (only changed fields are sent) |
| Rename it | Edit → type a new **Name** → **Save** |
| Remove its name | Edit → clear the **Name** box → **Save** |
| Change its kind | Edit → new **Subtype** → fill new `*` fields → **Save** |
| Understand a greyed-out field | It's *(locked — published)* — frozen by class design |
| Recover from "it changed since you opened it" | Review the refreshed values, then re-save |
| Know why Save is disabled | Nothing changed yet, or a required `*` is blank |

Next: **Chapter 5 — Editing geometry**, where we move and reshape a feature on the map,
split a line, and delete (and restore) a feature.

---

## Chapter 5 — Editing geometry

Where Chapter 4 changed a feature's *values*, this chapter changes its *shape and
place*: nudge a mislocated transformer onto its pole, reshape a conductor around a new
obstacle, cut one long span into two, take a feature off the map, or delete it
outright. Geometry editing shares the same **Edit** mode and the same **Save** as
attribute editing — so a single Save can commit a move *and* a value change together.

> 🔒 All of this needs **`features:write`** and works only **within your territory**.
> Priya (viewer) can watch; Dana and Tom do the moving.

### 5.0 The shape of a geometry edit

**▶ Quick steps**

1. Open the feature → **Edit** (pencil).
2. A small **geometry toolbar** floats at the top-centre of the map (drag it aside if
   it's in the way). Which buttons it shows depends on the feature's shape.
3. Pick a tool — **Move**, **Edit vertices**, or **Split** — and work on the map.
4. **Save** in the edit form commits the new geometry; **Cancel** discards it.

**How it works.** The map tools only ever produce a *candidate* geometry — dragging a
vertex or moving the shape doesn't write anything by itself. That candidate is held
until the edit form's **Save** turns it into a real update, the same commit-on-save
rule every write in VoltEdge follows. (**Split** is the one exception — see §5.4.)
While you're editing, the panel's Terminals and Relationships tabs are locked, so you
can't accidentally switch away and drop an unsaved change.

⚠️ The geometry toolbar appears **only for classes that have map geometry**. A
geometry-less class (a relay, a bay) has nothing to move here.

### 5.1 Move a feature 🖱️

**▶ Quick steps**

1. Open → **Edit**.
2. On the geometry toolbar, click **Move**.
3. 🖱️ **Drag** the feature to its correct position.
4. **Save**.

**Worked example.** Dana placed a transformer a few metres off its pole. She opens it,
hits **Edit**, clicks **Move**, and drags the whole symbol onto the pole. She'd turned
on **Snap** (in the same toolbar) so it locked cleanly onto the pole's point rather than
landing near it. She clicks **Save**; the transformer settles into place, and because
its geometry changed, the assistant offers to (re)connect it to the pole and conductor
it now touches (Chapter 8).

**How it works.** **Move** is a *translate* interaction — it slides the entire geometry
as one. For a **point** feature it's the only tool on the toolbar: a point has no
"vertex vs. whole feature" distinction, so moving the point *is* the edit.

- 💡 Clicking the **active** tool again turns it off. Move and Edit-vertices are
  mutually exclusive — you can't have both grabbing the same drag.

### 5.2 Reshape a line or area (edit vertices)

Lines and polygons carry a chain of vertices you can push around, add to, or thin out.

**▶ Quick steps**

1. Open a line/polygon feature → **Edit**.
2. Click **Edit vertices** on the geometry toolbar.
3. On the map:
   - 🖱️ **Drag a vertex** to move it.
   - 🖱️ **Drag a segment** (its midpoint) to **add** a new vertex there.
   - ⌨️🖱️ **Alt+click a vertex** to **delete** it.
4. **Save**.

**Worked example.** A road-widening has pushed a conductor off its old alignment. Tom
opens the conductor, hits **Edit**, and clicks **Edit vertices**. He drags the two
vertices that crossed the new kerb line back onto the verge, grabs a straight segment
midpoint to add a vertex around a new pole, and Alt+clicks a redundant kink to remove
it. The on-toolbar hint spells out those three gestures as he works. **Save** commits
the reshaped line in one PATCH.

**How it works.** **Edit vertices** is a *modify* interaction and shows only for line
and polygon classes (a point has no vertices to edit). The three gestures — move / add
/ delete — are exactly what the toolbar's inline hint states: *"Drag a vertex to move
it, a segment to add one, Alt+click to delete one."*

### 5.3 Snap while you edit

Every geometry tool honours **Snap**, which lives right on the geometry toolbar. Turn
it on before you drag when an endpoint or vertex must land *exactly* on another feature
— a conductor end on a transformer, a vertex on a pole. Snapping is its own chapter
(Chapter 7); the one thing to carry here is that it's available for **Move** and **Edit
vertices** alike.

- 💡 A move or reshape that *looks* connected but was done with Snap off often isn't —
  the endpoints sit a fraction apart. Snap makes the join real.

### 5.4 Split a line into two features

Sometimes one span should become two — a new tee-off point, a change of construction
partway along, a maintenance boundary. **Split** cuts a line at a point you click.

**▶ Quick steps**

1. Open the **line** feature → **Edit**.
2. Click **Split** (scissors) on the geometry toolbar.
3. 🖱️ **Click the line** where you want the cut.
4. A create form opens for the **new far half** — fill it in (it's pre-seeded from the
   original) and **Save**.

**Worked example.** A long MV span on Feeder 7 needs a mid-point tee for a new lateral.
Dana opens the span, hits **Edit**, clicks **Split**, and clicks the line at the tee
location. The original keeps the near half — along with all its connections and
relationships — and a create form opens for the far half, pre-filled with the span's own
subtype and attributes. She adjusts what differs and saves; now there are two conductors
meeting at the tee, ready to connect the lateral.

**How it works.** Split is **not** a candidate-geometry edit — it's the one geometry
tool that commits through the *create* path. Clicking the line projects your click onto
it and opens a seeded draft for the far half; **Save** POSTs to the split endpoint,
which truncates the original to the near half and creates the new part, its geometry
computed server-side from the cut point. The original keeps its connections; the brand-
new far half is the piece the assistant then offers to re-connect. **Split shows only
for a line** — a point or polygon has no "two parts" to cut into.

### 5.5 Take a feature off the map (without deleting it)

Some classes can exist *without* map geometry — living only on a diagram. If a class is
**geometry-optional**, you can demote a placed feature to diagram-only.

**▶ Quick steps**

1. Open the feature → **More actions** (⋯) → **Remove map geometry**.
2. Confirm.

**Worked example.** Dana has a piece of switchgear that's better represented on its
station schematic than as a map point. Its class is geometry-optional, so its ⋯ menu
offers **Remove map geometry**. She confirms; the feature vanishes from the map but is
otherwise **untouched** — its attributes, terminal connections, relationships and
diagram placements all survive. It simply no longer has a map location.

**How it works.** This PATCHes the geometry to null, which the server allows only on a
geometry-optional class. The option appears only when the class is geometry-optional
*and* the feature currently has geometry. ⚠️ It's reversible in principle by re-placing
the feature, but there's **no one-click undo** from here — treat it as deliberate.

### 5.6 Delete a feature

Deletion is a **soft-delete**: reversible in principle, but it removes the feature from
the map immediately and is gated behind an explicit confirmation.

**▶ Quick steps**

1. Open the feature → **More actions** (⋯) → **Delete feature**.
2. Read the confirmation, then click **Delete**.

**Worked example.** A pole that was recorded twice needs the duplicate removed. Dana
opens the stray one, opens the ⋯ menu, and chooses **Delete feature**. A confirmation
dialog explains it soft-deletes and disappears immediately; she clicks **Delete**. The
feature drops off the map at once, the panel closes, and it also disappears from any
diagram that carried it.

**How it works.** Delete issues a soft-delete: the row is retained server-side but the
feature is hidden from the map straight away and its selection is cleared. The
confirmation is an **alert dialog** on purpose — it won't dismiss on an outside click,
and it can't be escaped mid-request, so a destructive action is always a deliberate
one.

- ⚠️ Deleting a feature does **not** clean up around it the way you might assume — think
  before removing something with live connections. Prefer marking a feature's lifecycle
  (Chapter 4) over deleting it when it still exists in the real world.

### 5.7 Restore a deleted feature

Soft-delete means the data isn't gone — but be clear-eyed about where restore lives.

**How it works.** A soft-deleted feature **can be restored**, but **not from the map
panel** — the delete dialog says as much ("It can be restored later, but not from
here"). Restoration is a deliberate, out-of-band action: it's exposed as an API
endpoint (and to the agent tools), typically used by an administrator or via automation,
rather than a button in the feature panel. If you deleted something in error, that's the
route back — not a click on the map.

- 💡 Because there's no in-app restore button, treat the **Delete** confirmation as the
  real decision point. If you're unsure, don't delete.

### 5.8 One-card recap

| I want to… | Do this |
| --- | --- |
| Move a feature | Edit → **Move** → drag → **Save** |
| Move a point | Edit → **Move** (the only tool for a point) → drag → **Save** |
| Reshape a line/area | Edit → **Edit vertices** → drag / segment-drag / **Alt+click** → **Save** |
| Make an endpoint land exactly on another | Turn on **Snap** first (Ch 7) |
| Cut a line in two | Edit → **Split** → click the line → fill the far-half form → **Save** |
| Take a feature off the map (keep the record) | ⋯ → **Remove map geometry** (geometry-optional classes) |
| Delete a feature | ⋯ → **Delete feature** → confirm (soft-delete, immediate) |
| Undo a delete | Not from the map — restore via the API / an administrator |

Next: **Chapter 6 — Measuring & drawing**, where we measure ad-hoc distances and areas
on the map, switch units, and read a feature's own length or area.

---

## Chapter 6 — Measuring & drawing

Chapters 3–5 changed the network. This chapter changes *nothing* — it's about
**getting numbers off the map**: how long is that proposed lateral, how far is the
nearest pole, how big is that easement, what's the exact length of a conductor already
drawn. VoltEdge gives you two different answers to "how long / how big", and the first
skill is knowing which one you want:

- The **Measure** tool draws a throwaway line or shape *you* trace, and tells you its
  length or area. It measures **anything** — a gap between two features, a route that
  isn't drawn yet, a clearance — because you're sketching it live. Nothing is saved.
- The **feature panel** tells you a feature's **own** length or area, straight off its
  saved geometry. No tool, no tracing — the number is just *there* when you open it.

> 💡 Everyone can measure. Reading numbers off the map needs only **`features:read`**,
> so Priya measures exactly as freely as Dana. Measuring writes nothing, touches no
> feature, and needs no territory grant — you can measure over ground you can't edit.

### 6.0 Which do I want — the Measure tool or the panel?

| I want to know… | Use | Why |
| --- | --- | --- |
| The distance between two things | **Measure → Distance** | You trace the gap; it isn't a feature |
| The length of a route *not drawn yet* | **Measure → Distance** | Sketch the path, read the running total |
| The area of a patch of ground | **Measure → Area** | You trace the boundary |
| The length of a conductor **already on the map** | **Open it → panel** | Its own geometry already knows |
| The area of a saved polygon (a zone, an easement) | **Open it → panel** | Same — no need to re-trace it |

The rule of thumb: **if the thing you're measuring is a saved feature, open it — don't
re-trace it.** Re-tracing invites a slightly-wrong number; the panel reports the real
one.

### 6.1 Measure a distance 📏

**▶ Quick steps**

1. On the map toolbar, click **Measure** (the ruler) → choose **Distance**.
2. 🖱️ **Click** each point along the run you want to measure.
3. A **live label** trails your cursor, updating the running length as you go.
4. 🖱️ **Double-click** to finish; the final length **freezes** in place.
5. Click **Measure** again (or press **Escape**) to put the tool away — which clears
   every measurement.

**Worked example.** Dana is scoping a new service run from a pole on Feeder 7 to a
customer's meter position. Nothing's drawn yet, so there's no feature to open — she
needs the raw distance. She clicks **Measure**, picks **Distance** from the little
dropdown, and clicks the pole, then the corner the cable will turn at, then the meter
position. As she moves between clicks a small label rides her cursor showing the length
so far — `47 m`, then `82 m` as she adds the dog-leg. She double-clicks at the meter; the
label settles to `86 m` and stops following her. That's her cable length estimate — she
never created anything. When she's done she clicks **Measure** again to disarm, and the
`86 m` label disappears with the tool.

**How it works.** The Measure tool arms a *throwaway* drawing layer that never touches
real feature data — it's the same kind of scratch interaction the box-zoom tool uses.
While you draw, a live tooltip **Overlay** trails the sketch and re-reads the length on
every vertex and pointer move, so the number is always current. The **banner** at the
top of the map reminds you of the gesture: *"Click to add points, double-click to
finish — measuring distance."*

- 💡 The live label stays **blank until you have a real span** — i.e. until you've
  placed a first point and started the second leg. A zero-length "measurement" shows
  nothing rather than a misleading `0 m`.
- ⚠️ Measurements are **ephemeral**. Disarming the tool (clicking Measure again,
  pressing Escape, or leaving the map) clears *all* of them. They are never saved and
  don't survive a page reload — copy any number you need to keep.

### 6.2 Measure an area 📐

**▶ Quick steps**

1. **Measure** (ruler) → choose **Area**.
2. 🖱️ **Click** each corner of the region.
3. The live label shows the enclosed area, updating as you add corners.
4. 🖱️ **Double-click** to close the ring and freeze the area.

**Worked example.** Tom needs the rough area of a compound where a pad-mount substation
will sit, to sanity-check an access easement. He clicks **Measure → Area** and clicks
the four corners of the compound. The label climbs as the ring grows — the tool always
measures the ring *he has actually laid down*, ignoring the auto-closing segment
OpenLayers draws back to the start. He double-clicks the last corner; the area freezes
at `1,240 m²`. Reading it as hectares would help him compare against the easement
schedule — one click on the units toggle (§6.4) reprints it as `0.12 ha`.

**How it works.** Area mode arms the same scratch tool as a polygon draw, and reports
`getArea` of the current ring. Like distance, it's geodesic (§6.6), so the number is
right regardless of where on the globe the compound is. The frozen label is anchored at
the polygon's interior point so it sits *inside* the shape you traced.

### 6.3 Lay down several measurements at once

You don't have to disarm between measurements. After you double-click to finish one, its
label **freezes in place** and a **fresh** measurement immediately arms — so you can walk
a whole feeder dropping span-by-span distances without ever leaving the tool.

**Worked example.** Dana wants the length of three consecutive spans on Feeder 7. She
arms **Measure → Distance**, traces the first span and double-clicks (`63 m` freezes),
then straight away traces the second (`58 m`), then the third (`71 m`). Three labels now
sit on the map at once. She reads them off, then clicks **Measure** to disarm — and all
three clear together.

- 💡 This is the fast way to compare several gaps side by side. Just remember they all
  vanish the moment you put the tool away.

### 6.4 Switch units — metric ⇄ imperial

**▶ Quick steps**

1. In the **Measure** toolbar (or wherever a length/area is shown), click the **units
   toggle**.
2. Every length and area on screen — live measurements, frozen labels, **and** feature-
   panel figures — re-prints in the new unit system.

**Worked example.** Priya is comparing VoltEdge figures against a contractor's imperial
drawings. She clicks the units toggle once; the measurement she'd just taken flips from
`86 m` to `282 ft`, and when she opens a conductor its panel now reads its length in feet
and miles too. Her choice **sticks** — next time she signs in, VoltEdge is still in
imperial for her.

**How it works.** Units live in a **persisted** setting (metric is the default — VoltEdge
does all its internal maths in metres either way). The toggle is deliberately global: it
re-formats *already-frozen* measurement labels in place and drives the feature panel's
Length/Area rows, so you never see two different unit systems on screen at once. The
formatter rolls the number up sensibly as it grows:

| System | Length rolls up | Area rolls up |
| --- | --- | --- |
| **Metric** | m → **km** past 1,000 m | m² → **ha** past 10,000 m² → **km²** past 1,000,000 m² |
| **Imperial** | ft → **mi** past 5,280 ft | ft² → **acres** past ~43,560 ft² |

So a short run reads `86 m`, a feeder reads `3.2 km`; a compound reads `1,240 m²`, an
easement reads `0.12 ha`.

### 6.5 Read a feature's own length or area — no tool needed

For anything **already on the map**, don't measure it — **open it**. A line feature's
panel shows its **Length**; a polygon feature's panel shows its **Area**. The number
comes straight from the saved geometry, so it's the real one, not a re-traced
approximation.

**▶ Quick steps**

1. Click the feature to open its panel (Chapter 2).
2. Read the **Length** (lines) or **Area** (polygons) row.
3. Toggle units (§6.4) if you want the other system.

**Worked example.** Dana needs the exact length of an existing MV span for a material
take-off. Rather than trace it with the Measure tool — which would only approximate it —
she clicks the span to open its panel and reads **Length: 312 m** directly. It's exact
because it's computed from the conductor's own vertices. She flips to imperial and it
reprints as `1,024 ft` for the contractor.

**How it works.** The panel computes length for a line (or a polygon's **perimeter**) and
area for a polygon from the feature's stored GeoJSON, using the same geodesic engine as
the Measure tool. A **point** feature has neither a length nor an area, so its panel
simply doesn't show those rows. This is why the Measure tool has *nothing to arm* for an
existing feature: the answer is already in the panel.

- 💡 Length on a **polygon** means its outer-ring **perimeter**; area is only defined for
  polygons. There's no "area" row on a line and no "length-only" oddity to trip over.

### 6.6 How measurement is computed (and why it's trustworthy)

Both the Measure tool and the panel measure **geodesically** — on the curved earth, not
on the flat web-map projection. VoltEdge measures the real geometry with a haversine-
based engine (`getLength` / `getArea`), so the numbers stay correct **regardless of
latitude**. This matters: a naive measurement on a Web-Mercator map exaggerates
distances the further you are from the equator — a 1 km span near the poles can *look*
like several. VoltEdge doesn't have that error; a kilometre is a kilometre whether
you're in Accra or Reykjavík.

- 💡 That's also why you can trust the panel's Length for a material take-off. It's not
  the map-projected length — it's the ground length.

### 6.7 One-card recap

| I want to… | Do this |
| --- | --- |
| Measure a gap or an un-drawn route | **Measure → Distance** → click points → double-click |
| Measure a patch of ground | **Measure → Area** → click corners → double-click |
| Take several measurements | Keep the tool armed — each double-click freezes one |
| Clear all measurements | Disarm **Measure** (click it again / **Escape**) |
| Switch metric ⇄ imperial | Click the **units toggle** (sticks across sessions) |
| Get a saved line's exact length | **Open it** → read **Length** in the panel |
| Get a saved polygon's area | **Open it** → read **Area** in the panel |

Next: **Chapter 7 — Snapping & precision**, where we make endpoints land *exactly* on
their neighbours and draw clean right angles — the difference between geometry that
merely looks connected and geometry that is.

---

## Chapter 7 — Snapping & precision

This chapter is about the two aids that turn rough clicking into **exact** placement:
**Snap**, which pulls your cursor onto existing geometry so an endpoint lands *precisely*
on its neighbour, and **Ortho**, which constrains your drawing to clean right-angle
(and 45°) steps. Both are small toggles, and both make the difference between a network
that *looks* right and one that *is* right.

> 🖱️ Snap and Ortho are drawing aids, so they matter to whoever is drawing — Dana and
> Tom, with **`features:write`**. But they change *nothing* about permissions: they only
> shape where your next click lands.

### 7.0 Why precision matters here

VoltEdge is a connectivity model, not a picture. Two conductors "meet" at a transformer
only if their endpoints are at the **same coordinate** — a fraction of a metre apart and
they're merely near each other, not joined. You can't always see the gap at map zoom,
but a trace can: an almost-connection is an open circuit. **Snap** is how you guarantee
the join is real; you saw its effect in Chapters 3 and 5, and here's the full picture.

### 7.1 Snap — land exactly on existing geometry 🧲

**▶ Quick steps**

1. Look for the **Snap** toggle (the **magnet**) — it's on the drawing banner *and* on
   the geometry-edit toolbar.
2. Snap is **on by default**. Leave it on when you want endpoints and vertices to grab
   onto existing features; click it off when you deliberately want free placement.
3. Draw or drag as normal — near an existing vertex or line, your point **jumps** onto
   it.

**Worked example.** Dana is drawing a new LV conductor from a distribution transformer.
With **Snap** on (its default), she starts the line near the transformer's point and the
first vertex **snaps** exactly onto it — no squinting, no zoom-to-check. She traces to the
first pole; each pole she passes offers its point as a snap target, so her vertices land
dead on them. When she saves, the assistant's re-connect suggestion is clean because the
endpoints genuinely coincide with the transformer and poles. Later she needs to place a
label point *near* but not *on* a busy junction, so she clicks the magnet **off** for
that one placement, drops it in clear space, and clicks it back on.

**How it works.** Snap builds a set of **snap candidates** from the features currently
loaded in view and pulls your cursor onto the nearest vertex or segment within a small
tolerance. Because the map draws features as vector *tiles* (which the snap engine can't
read directly), VoltEdge rebuilds a plain snap-source from the loaded tiles whenever a
snapping session starts and as you pan (on map settle) — so the candidates always match
what you can see. The toggle state is **shared** everywhere snapping applies: flip it
while drawing and it's still flipped when you next edit geometry.

- 💡 Snap is **session state**, not a saved preference — it starts **on** every time you
  load the app. If a placement isn't snapping the way you expect, first check the magnet
  is lit.
- ⚠️ Snap can only grab **what's loaded in view**. If a target feature is scrolled off-
  screen or hidden by a layer toggle, there's no candidate to snap to — bring it into
  view first.

### 7.2 Where Snap is available — and where it isn't

Snap arms itself only for the gestures it can actually influence:

| Activity | Snap applies? |
| --- | --- |
| **Drawing** a new point / line / polygon (Chapter 3) | ✅ Yes |
| **Move** a feature (Chapter 5) | ✅ Yes |
| **Edit vertices** of a line/polygon (Chapter 5) | ✅ Yes |
| **Measuring** distance / area (Chapter 6) | ✅ Yes — snap your measurement onto real vertices |
| **Split** a line (Chapter 5) | ❌ No — a single click has no drag to bend |

That last row is the one to remember: **Split** is a single click that projects onto the
line you're cutting, so there's no in-progress gesture for Snap to steer — it doesn't arm
there, and it doesn't need to.

- 💡 Snapping while **measuring** is genuinely useful: to measure the exact gap between
  two poles, turn Snap on and click *on* each pole — your measurement endpoints land on
  their real coordinates, so the distance is pole-to-pole, not click-to-click.

### 7.3 Ortho — clean right angles and 45° steps 📐

When you draw a line or polygon, **Ortho** constrains each new segment to a **45° step**
off the previous vertex — so you get clean right angles, straight runs, and tidy
diagonals instead of hand-wavy near-straight lines.

**▶ Quick steps**

1. Start drawing a **line or polygon** (Chapter 3).
2. On the drawing banner, click the **Ortho** toggle (the right-angle / corner icon).
3. Each segment you draw now locks to the nearest **45°** direction from the last point.
4. Toggle it off any time to draw freehand again.

**Worked example.** Tom is drawing the rectangular outline of a switchyard fence. He
starts the polygon, clicks **Ortho** on, and traces the four sides — each one snaps to a
perfect horizontal or vertical, so the corners come out square without him nudging
anything. For a diagonal guy-wire run he leaves Ortho on too: the 45° step gives him a
clean diagonal rather than an almost-45° wobble. When he needs to follow an irregular
boundary, he clicks Ortho off.

**How it works.** Ortho reads its toggle **live** as you draw and bends the *in-progress*
segment to the nearest 45° direction from the previous vertex. It only exists for **line
and polygon** drawing — a single point has no direction to constrain, so the Ortho toggle
doesn't appear when you're placing a point. Its title spells out the behaviour:
*"Right-angle drawing on (45° steps)."*

### 7.4 Snap and Ortho together

Snap and Ortho are independent and combine cleanly:

- **Ortho** decides the *direction* of each segment (locks it to 45° steps).
- **Snap** decides the *endpoint* (pulls the vertex onto real geometry).

Drawing a feeder along a road, you might keep **Ortho on** for straight, tidy runs and
**Snap on** so the ends still land exactly on the poles — the segment stays straight
*and* the join is real. If the two ever fight (a snap target that isn't on a 45° line),
Snap's pull to real geometry is usually what you want for the *endpoint*; toggle Ortho
off for that one vertex if the angle constraint gets in the way.

- 💡 Both toggles are per-session and start at their defaults each load: **Snap on**,
  **Ortho off**. Ortho is the one you opt *into* for a specific tidy-drawing job.

### 7.5 One-card recap

| I want to… | Do this |
| --- | --- |
| Land an endpoint exactly on a feature | Leave **Snap** (magnet) **on**, draw near the target |
| Place something *near* but not *on* geometry | Turn **Snap off** for that placement |
| Measure the true gap between two poles | **Snap on**, click *on* each pole |
| Draw square corners / straight runs | Turn **Ortho on** while drawing a line/polygon |
| Draw a clean 45° diagonal | **Ortho on** — it snaps to 45° steps |
| Draw a freehand irregular boundary | Turn **Ortho off** |
| Cut a line precisely | Just click on it — **Split** needs no snap |

Next: **Chapter 8 — Connecting & relating**, where we wire features together at their
terminals, establish relationships, and lay down whole pre-wired assemblies in one move —
turning the precise geometry of these last chapters into a connected, traceable network.

---

## Chapter 8 — Connecting & relating

This is the most technical chapter in the book, and the most important, so it goes
slowly and explains *why* before *how*. Everything up to now has been about individual
features — their shape, place, and attributes. But VoltEdge isn't a drawing; it's a
**network model**, and a model is only as good as the links between its parts. A
transformer that *looks* joined to a conductor but isn't will never appear in a trace,
never carry load flow, never de-energise in an outage. This chapter is how you make the
joins **real**.

There are **two completely different kinds of link** in VoltEdge, and confusing them is
the single most common modelling mistake. Read §8.0 carefully — the rest of the chapter
depends on it.

> 🔒 Establishing or revoking any link needs **`features:write`** and works only **within
> your territory** — the server enforces both regardless of which button you click. Priya
> (viewer) can *open* the Terminals and Relationships tabs and read what's connected, but
> the **New** and **revoke** actions are Dana's and Tom's. Viewing needs only
> `features:read`.

### 8.0 The two kinds of link — connectivity vs. relationship

| | **Connectivity** (electrical) | **Relationship** (structural) |
| --- | --- | --- |
| **What it means** | Current can flow between these two features | These two features are *associated* — one supports, contains, routes, or belongs to the other |
| **Example** | An MV conductor's end wired into a transformer's HV terminal | A transformer *mounted on* a pole; a cable *routed through* a duct |
| **Carries a terminal?** | **Yes** — which terminal current enters/leaves through | **No** — there's no terminal concept at all |
| **Shows up in a trace?** | **Yes** — traces, load flow, outages all walk connectivity | **No** — it's metadata, not a current path |
| **Lives in the panel's…** | **Terminals** tab | **Relationships** tab |
| **The API route** | `POST /connectivity` | `POST /relationships` |

The one-line rule: **if you'd expect current to flow across the link, it's a
connection; if it's a physical or organisational association, it's a relationship.** A
pole doesn't conduct the transformer's power — so "mounted on a pole" is a *relationship*,
not a connection, even though the two are physically touching. The conductor feeding that
transformer *does* conduct — so that's a *connection*.

- ⚠️ Getting this wrong is silent. A relationship where you needed a connection won't
  error — it'll just quietly leave a gap in every trace. When a trace stops short of a
  feature you *know* is wired, the first thing to check is whether the link is on the
  **Terminals** tab (real connectivity) or only on **Relationships**.

### 8.1 Where connectivity lives — and which features can have it

Open a feature and look at its tabs. The **Terminals** tab (the plug icon) is where
electrical connections live — but it **only appears for features that are part of the
network model**. In VoltEdge terms, a feature has a *network role*, and only two roles
can carry connectivity:

- An **edge** (a wire — MV_CONDUCTOR, a cable) — it *connects* two junctions but has no
  terminals of its own.
- A **junction** (a device or node — a transformer, switch, busbar) — it *carries the
  terminals* that edges plug into.

Everything else — a **container** (a substation boundary, a bay) or a **geometry-less**
class (a relay) — has no place in the current path, so it has **no Terminals tab at
all**. If you can't find the tab, that's why: the feature isn't a connectable network
element. (The trace button is hidden for the same reason — a non-network feature never
appears in a trace.)

- 💡 This is also why the terminal dropdowns you'll meet below sometimes show nothing:
  an **edge** like a conductor has no terminals to pick, so its side of a connection is
  just "wire it in", while a **junction** like a switch offers its `T1 (source)` /
  `T2 (load)` terminals to choose from.

### 8.2 Task — connect two features 🔌

**▶ Quick steps**

1. Click the **source** feature to open its panel → **Terminals** tab.
2. Click **+ New connection**. A small floating **New connection** panel appears, and the
   map cursor becomes a crosshair.
3. 🖱️ **Click the second feature** on the map (or diagram) — this becomes the *target*.
4. In the panel, pick the **terminal** on each side that has terminals (leave "No
   terminal" for a plain wire).
5. *(Optional but recommended)* Click **Check for loop** to see whether a live path
   already joins these two.
6. Click **Confirm**.

**Worked example.** Dana has just drawn a new MV switch on Feeder 7 and needs to wire it
between two conductor sections. She clicks the switch to open it, goes to the **Terminals**
tab, and clicks **+ New connection**. The floating panel shows the switch as the source
and the cursor turns into a crosshair. She clicks the first conductor on the map; it drops
into the target slot. Because the switch is a junction with terminals, its side of the
panel offers a dropdown — she picks `T1 (source)`; the conductor is an edge, so its side
shows no terminal picker ("wire it in" is the whole story). Before committing she clicks
**Check for loop**; the panel reports *"No existing live path between these"* — good, this
isn't accidentally closing a ring. She clicks **Confirm**. The connection appears
instantly in the Terminals tab as an **Outgoing** link *via T1*, tagged with the network
rule that permitted it. She repeats for `T2 (load)` and the downstream conductor, and the
switch is now fully in-circuit — a trace will walk straight through it.

**How it works.** The two ends are the **source** (locked in the moment you click **+ New
connection**) and the **target** (set when you click a second feature). If your target
click lands on **stacked features**, VoltEdge pops the same candidate picker you met in
Chapter 2 so you choose exactly which one you meant — connectivity is too important to
guess. The terminal dropdown on each side is **scoped to that feature's subtype**, so you
only ever see terminals that actually exist on the thing you're wiring. **Confirm** POSTs
to `/connectivity` with the two ends and their chosen terminals; the server checks the
pairing against its **network rules** (which classes may connect to which, through which
terminals) and stores the link — which is why each row in the tab names the rule that
allowed it.

- 💡 **Check for loop** runs a special trace (`LOOP_CHECK_TRACE`) that **respects real
  switch state** — a topological path through a *currently-open* switch doesn't count as a
  live loop, because right now nothing flows through it. So a "no live path" result means
  it's safe to close *today*; it doesn't promise the ring can never be closed elsewhere.
- ⌨️ Press **Escape** at any point to abandon the workflow without connecting anything.
- ⚠️ If the server rejects the pairing (no network rule permits it), the panel **stays
  open** with the error in a toast, so you can pick a different terminal or target without
  starting over.

### 8.3 Task — let the assistant connect it for you ✨

You don't always have to pick source, target, and terminals by hand. Whenever you
**create or reshape** a feature (Chapters 3 and 5), VoltEdge's assistant looks at what's
now nearby and proposes the connections (and relationships) that make sense — you just
confirm them.

**▶ Quick steps**

1. Create or reshape a feature and **Save**.
2. A **Suggestions** panel appears at the bottom-left, one row per nearby feature.
3. Expand a row if it offers several options; adjust the **terminal** on a connection if
   you want.
4. Click **+** (or **Establish**) to commit — or **✕** to dismiss a suggestion.

**Worked example.** Tom moves a transformer so it now sits on its pole and touches the LV
main (Chapter 5). On Save, the Suggestions panel lists the pole (*"3 m away"*) and the LV
conductor. The conductor row proposes a connection with a rule-suggested terminal already
filled in; Tom leaves it and clicks **+** — the link is established and the row clears.
The pole row proposes a *relationship* ("mounted on"), which he also accepts. Two correct
links, no manual source/target picking.

**How it works.** This is a genuine time-saver, not a black box, and three things about it
matter:

- **It's not an LLM guessing.** The backend proposes links from the same **network rules**
  and **relationship rules** the manual workflow validates against, pre-builds the exact
  request body, and hands it to you. The distance to each candidate is shown so you can
  judge plausibility.
- **Your click is still the commit.** Pressing **+** POSTs to the *very same*
  `/connectivity` (or `/relationships`) route as the manual panel — same permission gate,
  same actor attribution. Nothing is written until you click.
- **A "split" suggestion is different.** If your new point landed *on* a line, the
  assistant may offer to **split** that line — clicking it doesn't write immediately;
  it opens the manual-split create form (Chapter 5) pre-filled from the line, so the new
  far-half's attributes get your eyes before the cut commits.

- 💡 The suggestions are advisory. Dismissing them changes nothing, and you can always
  wire a link by hand later (§8.2). They appear because *geometry changed* — an
  attribute-only edit doesn't trigger them.

### 8.4 Task — revoke a connection

**▶ Quick steps**

1. Open the feature → **Terminals** tab.
2. On the connection's row, click the **unlink** icon (🔗✕).
3. Confirm in the dialog.

**Worked example.** A conductor was wired to the wrong busbar terminal. Dana opens it,
finds the row on the Terminals tab, and clicks the unlink icon. A confirmation dialog
warns that this **permanently** removes the electrical connection. She confirms; the link
disappears from both features' Terminals tabs at once.

**How it works.** Revoking issues `DELETE /connectivity/{id}`. Unlike deleting a *feature*
(a soft-delete you can restore — Chapter 5), revoking a connection is a **hard, permanent
removal** — there's no restore pair. That's why it uses the same deliberate **alert
dialog** as a feature delete: it won't dismiss on an outside click and can't be escaped
mid-request. The dialog says it plainly: *"It can be re-established later, but not from
here"* — meaning you'd wire it again from scratch (§8.2), not undo this.

### 8.5 Relationships — the structural layer

Switch to the **Relationships** tab (the link icon). This is where the *non-electrical*
associations live — the physical and organisational facts that a trace doesn't care about
but a complete model needs: a transformer **mounted on** a pole, a cable **routed
through** a duct, a device **part of** an assembly. Relationships are **directional** and
**typed by a "kind"**, and they're governed by the model's **relationship rules**, which
declare exactly which classes may relate to which, in which direction, and with what
cardinality (one-to-one, one-to-many, …).

### 8.6 Task — relate two features 🔗

**▶ Quick steps**

1. Open the **source** feature → **Relationships** tab → **+ New relationship**.
2. 🖱️ Click the **target** feature on the map.
3. Choose the **Kind** if the panel asks you to (see below).
4. Click **Confirm**.

**Worked example.** Dana needs to record that her new transformer is mounted on its pole.
She opens the transformer, goes to **Relationships**, clicks **+ New relationship**, and
clicks the pole. The panel looks at the model's rules for this pairing and finds exactly
one valid kind — `mounted_on` — so there's nothing to choose; it shows the kind read-only
and she just clicks **Confirm**. The relationship appears on both features' Relationships
tabs, labelled with its class, kind, and cardinality.

Later she relates a cable to a duct and the panel offers **two** valid kinds; now the
**Kind** dropdown is required, and Confirm stays disabled until she picks one. A third
time she tries to relate two features the model doesn't allow to relate at all — the panel
tells her plainly *"No relationship class links … (in either direction)"* and won't let
her Confirm.

**How it works — the Kind picker, in detail.** When you pick a target, the panel reads the
source class's **relationship rules** and filters them to this exact pairing. What happens
next depends on how many valid kinds it finds:

| Kinds that fit this pairing | What the panel does |
| --- | --- |
| **0** | Blocks the establish and tells you no rule links these classes — it would only 400 anyway |
| **exactly 1** | Auto-picks it, shows it read-only ("*(only kind for this pairing)*"), sends the request with the kind omitted for the server to resolve |
| **2 or more** | Shows a required **Kind** dropdown; Confirm is disabled until you choose |

Two more subtleties worth knowing:

- **Direction can be flipped for you.** Relationship rules are *one-way* (origin →
  destination). If only the *reverse* direction is defined for your pairing — say the rule
  is written pole → transformer but you started from the transformer — VoltEdge quietly
  **stores it the correct way round** and tells you so: *"Stored as Pole → Transformer
  (relationships are one-way; this pairing is only defined in that direction)."* The
  real-world link is the same either way; only the stored direction has to match the rule.
- **The plausibility check is a nudge, never a block.** After a relationship is created,
  the server runs a *soft geometric sanity check* (are these two features even near each
  other?). If it looks off, the panel **stays open** with an amber *"Relationship created
  — worth a second look"* note and two buttons: **Undo** (which really does
  `DELETE /relationships/{id}` to back it out) or **Keep**. The relationship is already
  saved — this just makes "double-check the pairing" actionable instead of a toast that
  flashes past.

- ⚠️ If the rules happen to fail to load (a transient network hiccup), the Kind field
  falls back to a **free-text box** so you're never blocked — the server still validates
  whatever you type.

### 8.7 Task — revoke a relationship

**▶ Quick steps**

1. Open the feature → **Relationships** tab.
2. Click the **unlink** icon on the relationship's row.
3. Confirm.

**How it works.** Same shape as revoking a connection (§8.4): `DELETE
/relationships/{id}` through the same deliberate alert dialog. It's the counterpart to the
**Undo** button on a just-created relationship's plausibility warning — same call, just
reached from the tab later instead of immediately after creating it.

### 8.8 Guided assemblies — place *and* wire in one move

Wiring device-by-device (§8.2) is right for one-off links, but a lot of real network is
built in **standard, repeated arrangements** — a ring main unit's internal switches and
transformer, a packaged substation's MV-switch-to-LV-board chain, an overhead pole with
its cutout/transformer/arrester kit, a cable duct and its ways. VoltEdge builds these as
**guided assemblies**: you place *one* anchor feature, and a server-side composite fans
out the rest **already wired together**.

**▶ Quick steps**

1. On the map toolbar, open the **Assemblies** menu (the boxes icon).
2. *(Optional)* Type in the search box to narrow the list.
3. Pick the assembly you want.
4. Draw its anchor on the map exactly as you'd draw a normal feature (a point for most; a
   line for a duct).
5. Fill the create form and **Save** — the Save runs the whole composite.

**Worked example.** Dana needs a ring main unit at a new tee on Feeder 7. Instead of
placing an RMU point and then hand-wiring its internal switches and transformer, she opens
**Assemblies**, picks *"Ring Main Unit — 2 switches + transformer"*, and clicks the map to
place it. She fills the form and Saves; the server creates the RMU **and** its child
switches and transformer, wired to each other in the right order. What would have been a
dozen manual connections is one placement.

**How it works.** Every item in the Assemblies menu **places a feature and then runs a
multi-step server composite** — that's the whole reason it's a separate menu from plain
**New feature**. The menu is organised into sections; the kit placements all commit through
the one generic assembly endpoint, and the wiring placements through their own:

| Assembly | What it does | Endpoint |
| --- | --- | --- |
| **Assembly kits** | Places one anchor (a pole, a pad-mount transformer, an **RMU** enclosure, or a **packaged-substation** enclosure) and fans out its pre-wired kit — switches, transformer, arrester, LV board, cables — around it. Each kit is a recipe: a ring main unit (`2W_1T` / `2W_2T`), a compact packaged substation (permanent / mobile), a pole-mount transformer, a pad-mount URD unit, and so on. | `POST /assemblies` |
| **Cable duct** | Draws the duct as a **line** and fans out the `DUCT_WAY` children its way-count sets | `POST /ducts` |
| **Bus coupler** | Places the breaker as a point, then wires it **between two buses you pick** | `POST /couplers` |
| **Feeder tie (NOP)** | Places the switch, wires it **across two feeders**, and forces it **normally-open** | `POST /ties` |

> RMUs and packaged substations used to have their own menu sections and endpoints
> (`/rmus`, `/packaged-substations`); those engines were folded into the one generic
> assembly engine, so they are now ordinary kits in the **Assembly kits** section.

A few things worth understanding:

- **The anchor classes stay in the plain menu too.** Placing a *"pole with its kit"* here
  doesn't remove ordinary poles from **New feature** — you still place bare poles,
  breakers, and switches there. The Assemblies menu is the *"…and wire its standard
  arrangement"* affordance on top.
- **Couplers and ties open a picker.** Because a coupler must land *between two buses* (and
  a tie *across two feeders*), placing one prompts you to choose the two ends from the
  eligible classes — the composite then wires the device between them. A feeder tie is
  additionally **born `NORMALLY_OPEN`**, exactly as a real tie should be.
- **The menu only shows what's seeded.** If a section is empty (no RMU configs seeded, no
  duct classes), it simply doesn't appear — and the whole Assemblies button hides while a
  draw is already in progress, so it never competes with the placement banner.

- 💡 Assemblies are the fastest way to build correct connectivity, because the wiring is
  done *for* you and always follows the model's rules. When you're laying down anything
  standard, reach for **Assemblies** before wiring by hand.

### 8.9 Attach a street light to a pole 💡

Some street lights aren't traditional lamp columns — they're just a **luminaire bracketed
onto an existing distribution pole**. VoltEdge models that as a one-step action *on the
pole*: it creates the lamp, links it to the pole, and (where it can) wires it to the pole's
LV — all at once. The lamp has **no map geometry of its own**; it rides the pole, so if the
pole moves the lamp goes with it.

**▶ Quick steps**

1. Place the pole first if it isn't there yet — an ordinary point create (**§3.1**).
2. With **editing on**, open the pole's panel (click it on the map).
3. Open the **⋯ More** menu in the panel header → **Attach street light**.
4. Pick the lamp's **phase**, **luminaire type** (LED / SON / SOX / metal-halide) and
   **condition**, then **Attach**.
5. Read the toast: it tells you whether the lamp was also **wired to the pole's LV**, or
   attached and left for you to wire.

**Worked example.** Priya is adding a side-street lamp to an existing pole on Feeder 7. She
turns editing on, clicks the pole, opens **⋯ More → Attach street light**, picks *LED*,
*L1N*, *Good*, and hits **Attach**. There's one LV conductor running along that pole, so
VoltEdge connects the lamp to it automatically and the toast says *"attached and wired."* On
the next pole there are two LV runs, so it can't tell which to use — the lamp is attached and
the toast says *"two LV conductors in range — wire it manually,"* which she then does from the
lamp's **Terminals** tab (§8.2).

**How it works.** Like a guided assembly, this runs a small server composite: it creates a
geometry-less pole-mounted `STREET_LIGHT`, records a **POLE supports STREET_LIGHT** link, and
then **auto-wires** — but only when it's unambiguous. It looks for an `LV_CONDUCTOR` passing
within about **10 m** of the pole: **exactly one, phase-compatible → it wires it**; zero,
several, or a phase mismatch → it attaches the lamp and tells you to wire it yourself (it
never guesses the wrong line). The auto-wire step needs the connection permission; without it
the lamp is simply attached unwired. The lamp then behaves like any other LV leaf — it shows
in the pole's **Relationships**, carries a load, and appears in traces.

### 8.10 One-card recap

| I want to… | Do this |
| --- | --- |
| Wire two features electrically | Source → **Terminals** → **+ New connection** → click target → pick terminals → **Confirm** |
| Check a connection won't loop the network | In the New-connection panel, **Check for loop** before Confirm |
| Accept a suggested link | On the **Suggestions** panel, adjust terminal if needed → **+** |
| Remove a connection | **Terminals** tab → **unlink** icon → confirm (permanent) |
| Record a physical/structural association | Source → **Relationships** → **+ New relationship** → click target → pick **Kind** → **Confirm** |
| Fix a relationship flagged "worth a second look" | **Undo** (back it out) or **Keep** on the amber note |
| Remove a relationship | **Relationships** tab → **unlink** icon → confirm |
| Build a standard arrangement pre-wired | **Assemblies** menu → pick kit → place → **Save** |
| Attach a lamp to a pole | Open the pole (editing on) → **⋯ More → Attach street light** → pick phase/type/condition → **Attach** (auto-wires if one LV is in range) |
| Decide connection vs. relationship | Would current flow? → **connection**. Physical/organisational? → **relationship** |

Next: **Chapter 9 — Annotating the map**, where we add the non-network layer — text
annotations, labels, attachments, and external links — that documents the network without
being part of it.

---

## Chapter 9 — Annotating the map

Chapters 3–8 built the **network** — features and the links between them. This chapter is
about everything that *documents* the network without *being* part of it. None of it
appears in a trace, a load flow, or a CIM export; it exists to make the map legible to the
humans reading it: a redline circling a problem, a name floating next to a pole, a
photograph of a nameplate, a link to the billing record.

There are **four distinct surfaces**, and they're easy to confuse because they all "add
information to the map". Here's the map of the territory before we walk it:

| Surface | What it is | Attached to | Lives in |
| --- | --- | --- | --- |
| **Redline annotations** | Free-drawn markup — text notes, boxes, lines, arrows | The *map itself* (a coordinate), not a feature | The **Annotate** menu; an overlay you toggle in Layers |
| **Feature labels** | The feature's own name/attributes, drawn beside it | A *feature class* (every feature of it) | The **Layers** panel, per class |
| **Attachments** | Images and documents — photos, PDFs, drawings | *One feature* | The feature panel's **Files** tab |
| **External links** | Hyperlinks and live-data links to other systems | *One feature* | The feature panel's **Links** tab |

The dividing line: **annotations and labels live on the map; attachments and links live on
a feature.** You reach the first two from the map toolbar and Layers; the last two by
opening a feature.

### 9.1 Redline annotations — the markup layer

A **redline** is free-drawn markup that floats on the map at a coordinate you choose. It
belongs to *no* feature — it's a note *about* the map. There are four kinds, and you place
them from the **Annotate** menu (the red pencil):

| Kind | Icon | Use it for |
| --- | --- | --- |
| **Text note** | `T` | A caption, a warning, a name — a single point of text |
| **Box / area** | ▢ | Circling or shading a region — a work zone, an area of concern |
| **Line** | — | A freehand path — a proposed route, a boundary |
| **Arrow** | ↗ | Pointing at something; can be double-headed for "between these" |

> 🔒 Drawing and editing redlines needs **`annotations:write`**. Without it the **Annotate**
> button simply isn't there — but you can still *see* everyone's annotations by turning the
> overlay on in **Layers**. Redlines are a shared layer, not a private scratchpad.

### 9.2 Task — add a text note ✍️

**▶ Quick steps**

1. Click **Annotate** (the red pencil) → **Text note**.
2. A banner reads *"Click the map to place the note."*
3. 🖱️ Click where you want it.
4. The annotation editor opens: type the **note text**, adjust colour/size/style, **Save**.

**Worked example.** Dana wants to flag a pole on Feeder 7 that field crews keep
misidentifying. She clicks **Annotate → Text note**, clicks the map just beside the pole,
and the editor opens. She types *"NOT the recloser pole — see WO-4471"*, bumps the font
size up, sets it bold and red, and clicks **Save**. The note sits on the map for anyone
with the overlay on. Later she grabs its **Rotate** tool and drags on the map to angle the
text along the road.

**How it works.** A text note is a single **point**. Its text is **required** — the server
rejects an empty note, and the editor's Save stays disabled until you type something. Style
knobs specific to text — **font size, bold, italic, rotation, colour** — are the only ones
shown for this kind. Rotation is special: it's set by the in-map **Rotate** gesture (drag
to spin, the angle updates live), and the editor shows the current angle as a read-out
rather than a slider.

### 9.3 Task — draw a box, line, or arrow 🖊️

**▶ Quick steps**

1. **Annotate** → **Box / area**, **Line**, or **Arrow**.
2. Follow the banner: for a box/line, 🖱️ click corners/points and **double-click** to
   finish; for an arrow, click the **start** then the **end**.
3. The editor opens — set colour, **stroke width**, and **line style** (solid / dashed /
   dotted / dash-dot); add an optional caption; **Save**.

**Worked example.** Tom is marking a planned work zone. He picks **Box / area**, clicks the
four corners around the compound, and double-clicks to close it. In the editor he sets a
red outline, switches the line style to **dashed**, picks a fill colour and drags **fill
opacity** to 20% so the map shows through, and captions it *"Outage zone — Sat"*. For the
access route he adds an **Arrow** from the gate to the pad; to show two-way access he
toggles **Double-headed** so it gets an arrowhead at both ends.

**How it works.** Each kind carries only the style knobs it can use — a **box** adds
**fill colour + fill opacity**, an **arrow** adds the **double-headed** toggle, and all
three stroke kinds share **colour / stroke width / line style**. A caption is optional on
these (unlike a text note's required text). Everything you set is **staged locally and
committed in one Save** — the editor doesn't write on every keystroke, the same
commit-on-save rule features follow.

### 9.4 Task — edit or delete an existing annotation

**▶ Quick steps**

1. Turn the **annotations overlay** on (Layers) if it isn't, then click the annotation.
2. The editor reopens. To move or reshape it:
   - **Move** — drag the whole thing (offered for every kind).
   - **Reshape** — drag its vertices (boxes, lines, arrows).
   - **Rotate** — drag to spin (text notes only).
3. Change any style; **Save**. Or click **Delete** to remove it.

**How it works.** Selecting an annotation is itself gated behind the write path, so the
editor never needs its own permission check — if you could open it, you can edit it. The
in-map gesture (Move / Reshape / Rotate) **stages** a candidate geometry exactly like
feature geometry editing (Chapter 5); **Save** commits it in one PATCH alongside any style
changes. **Delete** removes the annotation outright — there's no soft-delete here, so it's
gone when you confirm.

- 💡 A text note has no vertices, so it offers **Rotate** where the shapes offer
  **Reshape**. Every kind can **Move**.

### 9.5 Feature labels — the network naming itself

A **label** is different from an annotation: you don't draw it, and it isn't a note *about*
the map — it's the **feature's own identity drawn beside it**, generated from a per-class
template. A pole class might label each pole with its name; a conductor class might label
each span with its voltage. Labels are turned on and off **per feature class** in the
**Layers** panel.

**▶ Quick steps**

1. Open the **Layers** panel.
2. Under **Feature classes**, find the class.
3. Click its **label toggle** to show or hide that class's labels on the map.

**Worked example.** Priya is presenting Feeder 7 and wants pole names visible but not the
clutter of every conductor's voltage. In **Layers**, she toggles **labels on** for the pole
class and **off** for the conductor class. The map now prints pole names beside each pole
and nothing beside the spans.

**How it works.** The label toggle **only appears for classes that actually have a label
template** — a class with nothing to say has no toggle. This is a **display preference**,
not data: turning labels on or off changes what *you* see, writes nothing, and needs no
permission — Priya (viewer) controls her own labels exactly as Dana does. It's independent
per class, so you dial in precisely the naming you want for the view you're building.

- 💡 Reach for a **label** when you want the network to *name itself* uniformly (every pole,
  every span); reach for a **text note** (§9.2) when you want to say something specific
  about *one* spot that isn't any feature's name.

### 9.6 Attachments — the Files tab

Open any feature and switch to the **Files** tab (the paperclip). This is where a feature's
**images and documents** live — a photo of the nameplate, a scanned drawing, a PDF spec.
Images show as a **thumbnail grid**; documents as a **labelled list** with their size.

Browsing files is **always read-only** — anyone with `features:read` can open and download
them, no special mode needed. *Adding and removing* files is a deliberate, opt-in mode.

### 9.7 Task — attach or remove files 📎

**▶ Quick steps**

1. Open the feature → **Files** tab.
2. Click **Manage files** (only visible with **`attachments:write`**).
3. Click **Upload files**, pick one or more; they upload one by one.
4. To remove one, click its **trash** icon and confirm.
5. Click **Done** to leave manage mode.

**Worked example.** Tom is commissioning a transformer and photographs its rating plate in
the field. Back at the desk he opens the transformer → **Files**, clicks **Manage files**,
then **Upload files**, and selects three photos plus the test-sheet PDF. They upload
sequentially; the photos appear as thumbnails, the PDF as a document row showing *"412.0
KB"*. He clicks a wrong photo's trash icon, confirms the deletion, and clicks **Done** —
the tab drops back to safe read-only browsing.

**How it works.** **Manage files** only appears with `attachments:write`, so a viewer or
analyst never even sees it — the write control is hidden, not merely refused. Each upload is
its own register → upload → commit round trip, done **sequentially** so a failure is clear
per file. Deleting always routes through a **confirmation dialog** — removing a file can't
be undone. The tab **resets to read-only** every time you reopen it (switching features or
tabs lands you back on Attributes), so you never leave it hanging in manage mode.

- 💡 The web app takes **images and documents**; the offline field tool is images-only. The
  file's kind (image vs document) is inferred server-side from its content type — you don't
  choose it.

### 9.8 External links — the Links tab

Switch to the **Links** tab (the outbound-arrow icon). This is where a feature points at
**other systems** — the record that lives somewhere else. There are two kinds:

- A **hyperlink** — a plain label + `http(s)` URL. Clicking it opens the target in a new
  tab (safely — the external page gets no handle back to VoltEdge).
- A **live-data link** ("Live data") — a link to a **catalogued external system**, which
  can **fetch and show the live payload** inline through a server-side proxy, rather than
  just navigating away.

A feature can hold as many as you like; the tab is a plain list with an inline add row and a
per-row remove.

### 9.9 Task — add or remove an external link 🔗

**▶ Quick steps**

1. Open the feature → **Links** tab → **Add link**.
2. Enter a **Label** (e.g. *"Billing account"*).
3. For a **hyperlink**, paste the `https://…` URL. For **Live data**, pick the **system**
   and enter its **resource path**.
4. Click **Add link**. Remove any link later with its **trash** icon.

**Worked example.** Dana links a service point to its billing record. On the **Links** tab
she clicks **Add link**, labels it *"Billing account"*, pastes the CIS URL, and adds it — it
now shows as a clickable row that opens billing in a new tab. For a second feature she uses
**Live data**: she picks the *"SCADA"* system from the dropdown, enters the resource path
`points/7Q3K2`, and adds it. That row carries a **Load** button; clicking it fetches the
live reading through VoltEdge's proxy and shows the normalized JSON inline, with a
**Refresh** to re-pull and the fetch time stamped.

**How it works.** A **hyperlink** needs only a label and URL. The **Live data** option only
appears if you can read the external-systems catalogue (**`metadata:read`**) *and* at least
one system is configured — otherwise you just get the hyperlink form, since there'd be no
system to point at. Live-data rows don't fetch until you press **Load** (and re-fetch on
**Refresh**), so opening the tab is cheap; the request goes through the **server-side
proxy**, so the browser never talks to the external system directly and its credentials
stay server-side.

- 💡 Use a **hyperlink** to *jump to* another system's page; use a **live-data link** to
  *see that system's current value* without leaving VoltEdge.

### 9.10 One-card recap

| I want to… | Do this |
| --- | --- |
| Circle or flag a spot on the map | **Annotate** → Box / Line / Arrow → draw → style → **Save** |
| Write a free note on the map | **Annotate** → **Text note** → click → type → **Save** |
| Move/reshape/restyle a redline | Click it → **Move** / **Reshape** / **Rotate** → **Save** |
| Show or hide a class's names on the map | **Layers** → the class's **label toggle** |
| See a photo/document on a feature | Open it → **Files** tab (read-only) |
| Attach or delete files | **Files** → **Manage files** → Upload / trash (needs `attachments:write`) |
| Link a feature to another system's page | **Links** → **Add link** → hyperlink |
| Pull another system's live value inline | **Links** → **Add link** → **Live data** → **Load** |

---

## Chapter 10 — Personalising & getting unstuck

Nine chapters in, you can find things, create them, edit their attributes and geometry,
measure, snap, connect, relate and annotate. This closing chapter is about the *space
around* all of that: first, the handful of settings that make the map feel like **yours** —
theme, basemap, units, which layers you see, where your panels sit — and then a
**troubleshooting guide** for the moments the app doesn't do what you expect, so you're
never stuck wondering whether it's you or it.

It splits cleanly in two:

- **Part A — Making it yours (§10.1–§10.6).** Every personal setting, what it changes, and —
  the part people get wrong — *whether it survives a reload*. Some choices stick forever,
  some last only until you leave the map, some vanish the moment you disarm a tool. Knowing
  which is which saves a lot of "why did that reset?".
- **Part B — Getting unstuck (§10.7–§10.12).** The five things that most often look like
  bugs and almost never are: a **missing button**, a **rejected edit**, a **missing Terminals
  tab**, a **connection that didn't take**, and a **map that looks wrong**. Each has a plain
  cause and a one-line fix.

None of the personalisation in Part A needs any permission — these are *display* preferences
stored in **your browser**, not data on the server, so Priya (a viewer) tunes her map exactly
as freely as Dana (an editor) tunes hers. Part B is where permissions and territory come back
into the story, because "the button is missing" is usually the app quietly telling you that
you don't hold something.

---

### Part A — Making it yours

#### 10.1 Light, dark, or match the OS

VoltEdge ships a **light** theme and a **dark** theme, and by default follows your operating
system's own light/dark setting so it changes with everything else on your machine.

**▶ Quick steps**

1. In the top **header**, click the **theme button** (a sun ☀️ in light mode, a moon 🌙 in
   dark mode, a monitor 🖥️ when following the system).
2. Pick **Light**, **Dark**, or **System**. A ✓ marks the active choice.
3. The whole app switches instantly. Your choice is remembered across reloads.

**Worked example — Priya moves to the night shift**

Priya is picking up evening dispatch and the bright white map is tiring in a dim control
room. She clicks the **sun** icon in the header and chooses **Dark**. The map chrome, panels
and status bar all flip to the dark surface at once; the map tiles stay the same, but the
blank basemap (if she'd chosen it) now shows a near-black background rather than glaring
white (that specific fix is why the blank basemap tracks the theme — see §10.11). When she
signs in again tomorrow morning, the app is still dark, because her explicit choice was
saved. If she'd rather it *followed* her laptop — light by day, dark by night, automatically
— she picks **System** instead, and from then on the app matches whatever her OS is set to.

**How it works**

The theme button writes one of three values — `light`, `dark`, or `system` — into a small
persisted browser setting (`voltedge-theme`). Only the *preference* is stored there;
actually resolving `system` against your OS and switching the page is done separately, so the
app can react the instant your OS flips between light and dark without you touching anything.
`System` is also the default the very first time you ever open VoltEdge.

- 💡 **Light**/**Dark** are explicit and stick until you change them. **System** hands the
  decision back to your OS — pick it if you use an automatic day/night schedule.

---

#### 10.2 Choose your basemap (and going blank)

The **basemap** is the background map under your network — streets, a lighter reference style,
or nothing at all. It's picked from the **status bar** at the bottom-right of the map.

**▶ Quick steps**

1. In the **status bar** (bottom of the map), click the **basemap name** at the far right
   (a small map 🗺️ icon beside it).
2. Choose a style from the list — e.g. **OpenStreetMap**, a lighter reference style, or
   **Blank (no basemap)**.
3. The background swaps immediately. Your choice is remembered across reloads.

**Worked example — Tom clears the clutter for a screenshot**

Tom needs a clean picture of Feeder 7's topology for a report, and the street detail behind it
is noise. He clicks the basemap name in the status bar — it currently reads **OpenStreetMap** —
and picks **Blank (no basemap)** from the bottom of the list. The streets vanish; his cables,
switches and transformers now sit on a plain background (dark, because he's in dark mode). He
takes his screenshot, then switches back to **OpenStreetMap** so the next person to open the
map gets the usual streets. Because the choice is per-browser, his switch to blank didn't
change anything for Dana on her machine.

**How it works**

The basemaps on offer come from the server — an administrator (Ada) curates the list, and each
row carries its own required **attribution**, which the status bar shows automatically at the
left of the bottom-right cluster whenever a real basemap is active. **Blank** is the one entry
that *isn't* a server row: it's a built-in "no tiles" choice the picker always appends, and
selecting it simply hides the tile layer. Your selection (`voltedge-basemap`) is persisted, so
it survives a reload; if an admin ever removes the exact basemap you'd chosen, the map quietly
falls back to the first available one rather than breaking.

- 💡 **Blank** is the fastest way to a clean export or a distraction-free trace — the network
  stands alone with nothing behind it.
- ⚠️ The basemap is a *reference* layer only. Nothing you do to it — switching it, blanking it —
  touches your features or their geometry.

---

#### 10.3 Metric or imperial

Every distance and area the app *shows you* — the live measure tooltips from Chapter 6, and a
feature's own computed **Length**/**Area** rows — formats against a single **units**
preference. Flip it in one place and it flips everywhere.

**▶ Quick steps**

1. Start the **Measure** tool (status bar → **Measure**), and while measuring click the
   **Metric / Imperial** toggle in its inline controls; *or* use the units toggle wherever a
   measurement is shown.
2. Everything re-labels at once — metres/kilometres and hectares/km² become feet/miles and
   acres, or the reverse.
3. Your choice is remembered across reloads.

**Worked example — Dana quotes a span in the customer's units**

Dana is measuring a proposed overhead extension for a customer who thinks in feet. She's been
working in metric all morning. She starts **Measure → Distance**, and in the measuring
controls clicks the units toggle from **Metric** to **Imperial**. The live tooltip on her span
immediately re-reads in feet (and would roll up to miles past a mile); when she later opens an
existing cable and looks at its **Length** row in the feature panel, that too is now in
imperial, because both read the same preference. She finishes the quote, and — since imperial
is now her saved default — it's still imperial when she reopens VoltEdge next week, until she
switches back.

**How it works**

Units is a **durable** preference (`voltedge-units`), unlike the session-only measure/draw
stores it sits beside — which units you think in is a lasting thing, not a per-session one. The
default is **metric**, because the geometry maths underneath is metric end to end (see §6.6 on
why the geodesic figures are trustworthy). The toggle only changes *formatting*; the stored
geometry and the underlying calculation are identical either way.

- 💡 One toggle, everywhere: there's no separate "measure units" and "feature units" — the live
  ruler and the panel's Length/Area rows are the same preference.

---

#### 10.4 Show only the layers you care about

The **Layers** panel is where you decide which feature classes are drawn, and — separately —
whether each class shows its **labels** (the name text §9.5 covered). Both are personal display
choices; neither touches data or needs any permission.

**▶ Quick steps**

1. Open the **Layers** panel.
2. **Hide/show a class:** clear or set the checkbox on its row. Cleared = that class's features
   disappear from the map.
3. **Hide/show a class's labels:** use the row's **label toggle** (only present when the class
   *has* a label template). Off = features stay drawn but their names don't.
4. Both settings are remembered across reloads.

**Worked example — Priya declutters to trace a feeder**

Priya wants to follow Feeder 7's medium-voltage backbone without the low-voltage service mesh
and every pole label crowding the view. In **Layers**, she clears the checkboxes on the
LV-service classes — they vanish from the map — leaving just the MV conductors, switches and
transformers. The MV lines are still busy with text, so on the conductor class's row she flips
the **label toggle** off: the cables stay drawn, but their names stop cluttering the trace. She
walks the feeder cleanly. Tomorrow the panel remembers exactly this arrangement, because both
the hidden-classes and hidden-labels choices are persisted per-browser.

**How it works**

The Layers panel stores only the **exceptions** — a class you've hidden, or whose labels you've
suppressed. Anything with no entry is visible with labels *by default*, which is deliberate: a
brand-new feature class an admin compiles shows up correctly without the panel having to know
about it in advance. Visibility and labels are **orthogonal** — a layer can be visible with its
labels off, or (less usefully) hidden with labels notionally on. The label toggle only appears
for classes that actually *have* a label template (Phase 30); a class with no template has
nothing to toggle, so the control is simply absent — see §10.7 if you're hunting for a toggle
that isn't there.

- 💡 Hiding a class is a **view** change, not a filter on data — the features still exist, still
  answer queries, and still take part in traces; you just aren't drawing them.
- 💡 The same panel is where the territory overlay, annotations, faults and external service
  layers are toggled — it's the one place for "what's drawn on my map."

---

#### 10.5 Move, resize, and dismiss floating panels

Several tools open as **floating panels** — the query builder, the connect/relate confirm
panel (§8.2, §8.6), the geometry-edit toolbar (§5) — small windows that float over the map so
they don't push it aside. They all behave the same way.

**▶ Quick steps**

1. **Move** a panel: drag it by its **header/title bar**.
2. **Resize** it: drag the **grip in its bottom-right corner**.
3. **Close** it: click the **✕** in its top-right.
4. A panel can't be dragged off-screen — it's always kept fully in view.

**Worked example — Dana parks the connect panel out of the way**

Dana is wiring a new switch into Feeder 7 (§8.2). The connect-confirm panel opens over the very
junction she needs to click. She grabs its title bar and drags it down to the bottom-left
corner, clear of the work area, then tugs the bottom-right grip to make it a little shorter so
it covers even less. She picks her terminals, confirms the connection, and closes the panel
with the ✕. Next time a floating panel opens, it opens at its **default** position and size
again — the app doesn't remember where she parked this one.

**How it works**

Floating panels share one draggable/resizable shell. Dragging the header repositions it;
dragging the bottom-right grip resizes it (down to a sensible minimum so it can't collapse to
nothing); the ✕ closes it. Whenever you move or resize — and even if you resize the browser
window — the panel is **clamped to stay fully on screen**, so it can never end up half-off an
edge where you can't reach its header. The one thing panels *don't* do is remember their
position: each panel's placement is view-only and resets to the default the next time it opens.
That's the deliberate exception to "settings stick" — see the table in §10.6.

- 💡 If a panel is ever covering the exact spot you need to click, just **drag it aside** by
  the header rather than closing it — your in-progress choices stay put.

---

#### 10.6 Finding your way around — and what sticks

Beyond themes and layers, the map has a cluster of small navigation aids, most of them in the
**status bar** along the bottom. None need a permission.

**The status bar, left to right:**

| Readout / control | What it tells you or does |
| --- | --- |
| **API health** dot | Green = the server is reachable; red = it isn't (a first thing to check if *nothing* is loading). |
| **Lat / Lon** | The coordinates under your cursor, updating as you move — handy for reading off a location. |
| **Zoom** | The current zoom, to one decimal (it's continuous, so "12.3" is honest where "12" would fib). |
| **Box zoom** | Arm a drag-a-rectangle zoom — **Zoom in** (fit the box to the screen) or **Zoom out**. |
| **Measure** | The ad-hoc ruler from Chapter 6, with its inline Snap and units toggles. |
| **EPSG:3857** | The map's working projection (fixed, shown for reference). |
| **Go to lat, lng** | Type a `lat, lng` and press Enter — the map flies there. |
| **Pins (n)** | Your saved location bookmarks — open the manager to add, revisit or remove them. |
| **Basemap** + attribution | The basemap picker (§10.2) and the active basemap's required credit. |

**Two more aids off the status bar:**

- **Reset north.** If the map gets **rotated** off north — easy to do by accident with a
  two-finger twist on a touchpad or touchscreen — a small **north arrow** appears just below
  the zoom control at the top-left. It leans to show where north currently is; click it to spin
  the view back to north-up. It's *only* there when the map is actually rotated, so if you can't
  find it, your map is already north-up.
- **A remembered view.** When you leave the map for another page and come back, it returns to
  the **centre and zoom you left** rather than snapping back to the default — so nipping to the
  Works board and back doesn't lose your place.

**▶ Quick steps — jump to a coordinate and bookmark it**

1. In the status bar, type a `lat, lng` into **Go to lat, lng** and press **Enter** — the map
   flies there.
2. To keep that spot, open **Pins**, add a pin with a name.
3. Revisit it any time from the **Pins** manager; pins are saved per-browser.

**Worked example — Tom bookmarks a recurring inspection site**

Tom keeps coming back to the same rural transformer bank for commissioning checks. He pastes
its coordinates — copied from a work order — straight into **Go to lat, lng** and hits Enter;
the map flies out to it. He opens **Pins**, names the pin "T-bank, Oakwood N", and saves it.
For the rest of the week he reaches it in one click from the Pins list instead of hunting the
map. When he's done with that job he removes the pin. None of this touched the network — pins
are his own private bookmarks, stored only in his browser.

**What sticks, and what doesn't**

This is the part worth internalising. VoltEdge stores personal settings at **three different
lifetimes**, and knowing which is which explains almost every "why did that reset?":

| Setting | Lifetime | Survives a full page reload? |
| --- | --- | --- |
| **Theme** (light/dark/system) | Persisted (browser) | ✅ Yes |
| **Basemap** choice | Persisted (browser) | ✅ Yes |
| **Units** (metric/imperial) | Persisted (browser) | ✅ Yes |
| **Layers** hidden + label toggles | Persisted (browser) | ✅ Yes |
| **Pins** (bookmarks) | Persisted (browser) | ✅ Yes |
| **Remembered map view** (centre + zoom) | In-memory | ❌ No — reload starts at the admin default |
| **Floating-panel position/size** | View-only | ❌ No — resets to default each time it opens |
| **Snap / Ortho** (Ch 7), **Measure** (Ch 6), an in-progress **draw/edit/connect** | Session-only | ❌ No — and cleared the moment you disarm the tool |

- 💡 The rule of thumb: **display preferences** you'd want to keep (theme, basemap, units,
  layers, pins) are saved for good; **where you are and what you're doing** (view, open panels,
  armed tools) is not — those are meant to start fresh. A *route change* keeps your view; a
  *full reload* returns to the default.
- 💡 **Snap defaults to ON** every session (§7.1) — a reload doesn't turn it off, but neither
  does it remember if you turned it off; it starts on again. If a connection looks wrong, §10.10
  is the place to check.

---

### Part B — Getting unstuck

The rest of this chapter is a field guide to the five things that most often *look* like the
app is broken and almost always aren't. In each case there's a plain cause and a one-line fix.

#### 10.7 "The button I need isn't there"

This is the single most common confusion, and it has exactly **three** causes. Work down them
in order:

1. **You don't hold the permission.** VoltEdge hides an action you can't perform rather than
   showing it disabled — so a missing **Edit** pencil, **New feature** menu, **Annotate** menu,
   **Manage files** button, **Add link**, or the connect/relate buttons usually means you lack
   the permission behind it. The pattern throughout this book: creating and editing features
   need **`features:write`**; drawing redlines needs **`annotations:write`**; managing files
   needs **`attachments:write`**; live-data links need **`metadata:read`**. Priya, a pure
   viewer, simply doesn't see any of these — and that's the design, not a fault.
2. **It's outside your territory.** Even *with* the right permission, write actions are gated to
   your **territory access** (🗺️). A feature that belongs to a zone you can't edit will open
   read-only. If you can *see* something but not *change* it, territory is the usual reason.
3. **Your build predates it.** If a button described here is missing for *everyone*, including
   an administrator, your VoltEdge deployment may be older than the feature. That's the honest
   third possibility, and it's the one to raise with whoever runs your instance.

**Worked example — Priya and Dana look at the same switch**

Priya opens a switch on Feeder 7 and sees its attributes, terminals, files and history — but no
**Edit** pencil and no **Files → Manage files**. She wonders if something's broken. It isn't:
she holds `features:read` but not `features:write` or `attachments:write`, so the *authoring*
controls are simply not rendered for her. Dana opens the identical switch and sees the pencil
and the Manage-files button — because she holds the write permissions **and** the switch is in
a territory she can edit. When Dana opens a switch in a neighbouring zone she has no access to,
*she* sees it read-only too, for reason 2. Same feature, three different sets of buttons, all
correct.

- 💡 A quick self-test: if **you** can't do something but a colleague with more access can, it's
  reason 1 or 2 (permission or territory). If **nobody** can, it's reason 3 (build).

Two class-specific "missing controls" have their own sections because their cause is different
from the three above: a **missing Terminals tab** (§10.9, it's about the feature's *role*, not
your permissions) and a **missing label toggle** in Layers (§10.4, the class has no label
template).

#### 10.8 "My edit was rejected — someone changed it first"

Occasionally you save an edit and instead of success you're told the feature changed since you
opened it. That's not a lost edit — it's a **concurrency guard** doing its job.

**What's happening.** When you start editing, the app notes the feature's state at that moment.
On save it checks that nobody else has modified the feature in the meantime. If someone has —
say Tom updated the same switch from the field while Dana had it open — the save is refused so
the second writer can't silently clobber the first.

**The fix.** Your in-progress edit **isn't discarded** — your typed values stay in the form.
Close and reopen the feature (or otherwise refresh it) so you're looking at the current, saved
state, then re-apply your change on top of it and save again. Now you're editing from the
latest version and the save goes through.

- 💡 This only fires on a genuine collision — two people (or a person and a field sync) touching
  the *same* feature in an overlapping window. It's rare, and it's protecting you.
- ⚠️ Don't try to force it by hammering Save — reopen the feature so you can *see* what the other
  person changed before you decide how to merge your edit with it.

#### 10.9 "This feature has no Terminals tab"

You open a feature to wire it up and there's no **Terminals** tab at all. This isn't a
permission problem and it isn't a bug.

**Why.** The Terminals tab — and the connectivity workflow behind it (§8.1–§8.2) — only appears
for features that play an electrical **network role** of **edge** (a line the trace walks
*along*, like a conductor) or **junction** (a point the trace walks *through*, like a switch).
Features that are **containers** (a substation boundary, a bay) or have **no geometry** don't
carry terminals, so the tab is correctly absent. It's the same rule that decides whether the
Trace button shows.

**What to do instead.** If two such features genuinely belong together, you almost certainly
want a **relationship** (§8.6), not a connection — a structural "part of / associated with" link
that doesn't need terminals. Open the **Relationships** tab (which *is* there) and relate them.

- 💡 Rule of thumb: if the trace should flow *through* it, it's a network feature and has
  Terminals; if it merely *contains* or *describes* other things, it's related, not connected.

#### 10.10 "My connection didn't take — the vertices won't land"

A cable you drew *looks* connected to a switch but a trace stops dead there, or the plausibility
check (§8.6) grumbles that two things you meant to join aren't actually touching. The usual
culprit is **snapping**.

**Why.** Two features are electrically joined only when you make the join explicitly (§8.2) —
but even a geometric coincidence relies on the endpoints being *exactly* on top of each other,
and by eye you'll always be a pixel or two off. **Snap** (Chapter 7) is what pulls your vertex
precisely onto the existing feature so there's no invisible gap. Snap defaults to **on**, but
it's **session-only** — if you (or a habit) turned it off earlier, it stays off for the rest of
that session, and every vertex you place after that is wherever your cursor happened to be.

**The fix.**

1. Check the **Snap** (magnet) toggle — in the draw/edit banner, or in the Measure controls in
   the status bar. If it's off, turn it back on.
2. Re-place the offending endpoint so it snaps onto the target feature (§7.1). You'll feel it
   catch.
3. Make sure you actually **created the connection** (§8.2) — snapping the geometry together is
   necessary but a trace follows *explicit* connectivity, not mere overlap.

- ⚠️ **Snap off is the classic source of "fake" connections** — geometry that looks joined at
  full zoom but has a hair-fine gap. If a trace ends one feature too early, suspect this first.
- 💡 The assistant's suggestions (§8.3) and the loop check (§8.2) are your safety nets here: use
  them to confirm the join is real rather than trusting the picture.

#### 10.11 "The map looks wrong"

A grab-bag of visual "that's not right" moments, each with a quick cause:

- **Everything's tilted.** The view has been **rotated** off north (a two-finger twist is easy
  to trigger by accident). Click the **reset-north** arrow at the top-left (§10.6) to spin back
  to north-up. It only appears *because* you're rotated.
- **The background went solid, not blank-white.** In **dark mode**, the **Blank** basemap shows
  a near-black background rather than white — that's intentional, so a no-tiles map doesn't
  glare white in a dark room. It's not a failed load; it's the blank basemap tracking your
  theme (§10.1–§10.2).
- **My view jumped back to the default.** You did a **full page reload**. The remembered
  centre/zoom (§10.6) survives *navigating* between pages but not a hard reload — after a reload
  the map opens at the administrator's default extent by design. Just pan/zoom back, or use a
  **pin** (§10.6) if it's somewhere you return to.
- **A whole class vanished.** Check the **Layers** panel (§10.4) — you (or a saved preference
  from last session) may have that class **hidden**. Its checkbox will be clear; set it to bring
  the class back. Remember hiding is persisted, so a class you hid last week is still hidden
  today.
- **Nothing is loading at all.** Glance at the **API health** dot at the far left of the status
  bar. If it's **red**, the app can't reach the server — that's an infrastructure issue to
  raise, not something a map setting will fix.

#### 10.12 A first-aid checklist

When something's off and you're not sure which section applies, run down this list — it resolves
the large majority of "is it broken?" moments in seconds:

1. **Is the API dot green?** Red = server unreachable; stop here and report it.
2. **A button's missing?** → §10.7: permission, then territory, then build.
3. **An edit was rejected?** → §10.8: someone changed it first; reopen and re-apply.
4. **No Terminals tab?** → §10.9: it's not a network feature; relate it instead.
5. **A connection/trace stops short?** → §10.10: check Snap, re-place the vertex, confirm the
   connection.
6. **Map tilted / background odd / view jumped / a layer gone?** → §10.11.
7. **A setting keeps resetting?** → §10.6's table: it's session-only or view-only by design.

- 💡 Nearly everything in Part B comes down to one of three quiet, deliberate behaviours:
  VoltEdge **hides what you can't do** (permissions/territory), **refuses to let two people
  clobber each other** (concurrency), and **keeps some things fresh on purpose** (view, panels,
  armed tools). Once those three click, the app stops surprising you.

### 10.13 One-card recap

| I want to… | Do this |
| --- | --- |
| Switch light / dark / follow OS | **Header** → theme button → Light / Dark / System |
| Change or blank the background map | **Status bar** → basemap name → pick a style / **Blank** |
| Work in feet & miles (or metres) | Toggle **Metric / Imperial** at any measurement |
| Hide a class, or hide its names | **Layers** → class checkbox / class **label toggle** |
| Move a panel off my work area | **Drag its header**; resize from the bottom-right grip |
| Fly to a coordinate / save the spot | **Status bar** → **Go to lat, lng** → **Pins** to bookmark |
| Get north back after a twist | Click the **reset-north** arrow (top-left) |
| Understand why a button's missing | Permission → territory → build (§10.7) |
| Recover a rejected edit | Reopen the feature, re-apply your change, save (§10.8) |
| Fix a connection that won't trace | Turn **Snap** on, re-place the vertex, confirm the link (§10.10) |

---

## Chapter 11 — Attaching files & querying the network

Two everyday jobs that both live on the map, and that people often go looking for as a pair: **hanging files on a feature** (a nameplate photo, a test sheet, a scanned drawing) and **asking the data a question** (every 315 kVA transformer; what's within 150 m; how many spans of cable in this feeder). You've met pieces of both already — attaching files sits on a feature's **Files** tab (first seen in §9.6), and the **Query builder** turned up under *finding things* (§2.3–2.4). This chapter is the one place that covers them end to end.

> The quick "find a feature" paths in **Chapter 2** and the file basics in **§9.6–9.7** still stand — this chapter is the deeper reference, not a replacement. If you only need "find features matching X," §2.3 is the short version.

### 11.1 Attachments — the Files tab 📎

Open any feature and switch to the **Files** tab (the paperclip). A feature can hold **images and documents** — photos as a **thumbnail grid**, documents (PDF, drawings) as a **labelled list** with their size. The field app is **images only**; the web app takes both, and the file's *kind* is worked out from its content type — you never pick it.

- **Browsing is always open** — anyone who can read the feature can open and download its files. No special mode.
- **Adding and removing is opt-in** and gated on **`attachments:write`** (editors and admins). A viewer or analyst never even sees the controls.

### 11.2 Task — attach, view & remove files

**▶ Quick steps**

1. Select the feature on the map → the feature panel opens → **Files** tab.
2. **View:** click an image thumbnail to open the full picture in a new tab; click a document row to download it.
3. **Add/remove:** click **Manage files** (only there with `attachments:write`) → **Upload files**, pick one or more (a file picker — multiple files upload one after another). Remove one with its **trash** icon and confirm. Click **Done** to leave manage mode.

**Worked example.** Tom commissions a transformer and photographs its rating plate. He opens the transformer → **Files** → **Manage files** → **Upload files**, and selects three photos plus the test-sheet PDF. They upload in turn; the photos land as thumbnails, the PDF as a row reading *"412.0 KB."* He trashes one blurry photo, confirms, and clicks **Done** — the tab drops back to safe, read-only browsing.

**How it works.** Files don't travel through VoltEdge itself — your browser uploads each one **directly to object storage** over a short-lived signed link (register → upload → confirm, per file, so a failure is clear file-by-file), and downloads the same way. Images are **thumbnailed server-side** on upload (small JPEG previews) so the grid is cheap to load; documents show an icon. The tab **resets to read-only** every time you reopen it, so you never leave it hanging in manage mode.

- 💡 Use documents for anything that isn't a picture — a PDF spec, a CAD drawing, a scanned permit. Only images get thumbnails; documents list by name and size.
- ⚠️ A delete can't be undone — that's why it always asks first.

### 11.3 Attachments in the field

In the **field app**, the Files tab is a **Photos** section: capture from the camera or gallery **offline**, and the photos queue in a local outbox and upload on your next **Sync**. Offline, you still see the thumbnails baked into your downloaded field pack; the full-resolution image is fetched when you're back online. (Mobile is images-only — documents are a desk job.)

### 11.4 Querying the network — the Query builder at a glance 🗺️

Open the **Query** button on the map toolbar (the magnifying glass). The **Query builder** is a floating panel with two modes plus a whole-network option:

- **Filter** — *list the features that match these conditions.* "Every `DIST_XFMR` with rated capacity = 315."
- **Aggregate** — *count/total instead of list.* "How many? grouped by what? sum of which field?"
- **All feature classes (count by class)** — *what's on the map in this scope,* tallied per class.

Three things hold across all of it:

- **One class at a time.** A query targets a single feature class (or the all-classes count) — there are no cross-class joins.
- **Results respect your access.** You only ever get features your **territory** (and draft) access allows — a query can't leak something you couldn't open by hand.
- **Results live on the map.** Matches **highlight all at once** (in pink) and each result row has a **zoom-to** button that flies the map to that feature with focus — even if it wasn't loaded. Close the panel and the highlight clears with it.

### 11.5 Task — Filter: find features by their attributes

**▶ Quick steps**

1. Open **Query** → **Filter** mode → pick the **feature class**.
2. Add **conditions**: a **field**, an **operator**, and a **value**. The operator list is trimmed to what the field's type allows:
   - `equals` / `not equals` — any field
   - `<` `≤` `>` `≥` / `between` — numbers and dates
   - `contains` / `starts with` — text
   - `in` (a list of values) — any field
   - `is empty` / `is not empty` — any field
3. Combine with **AND / OR**, and nest **groups** for `(A AND B) OR C` (up to five deep, fifty conditions total).
4. *(Optional)* add a **tag filter** — features that *have* a tag, or whose tag matches a code/prefix.
5. **Run.** Matches list in the panel and highlight on the map.

**Worked example.** Dana needs every underground MV cable over 240 mm² that's still *draft*. She picks `MV_CONDUCTOR`, adds *construction **equals** underground* **AND** *csa **>** 240* **AND** *status **equals** draft*, and runs it. Eleven spans come back and light up pink; she zooms to each from the list to review it.

> This is the same builder §2.3 introduces — here with the full operator set and nesting spelled out.

### 11.6 Task — Aggregate: count, group, and total

When you want *a number,* not a list.

**▶ Quick steps**

1. Open **Query** → switch to **Aggregate** mode → pick the class.
2. *(Optional)* **Group by** a field — a per-value breakdown instead of one grand total.
3. Choose the roll-up: **Count**, or **Sum / Average / Minimum / Maximum** of a numeric field.
4. **Run** — you get the figure(s), computed server-side against the real data.

**Worked example.** A planner wants the installed transformer capacity per feeder. She picks `DIST_XFMR`, **groups by** `circuit`, rolls up **Sum** of `rated_capacity`, and runs it — a table of feeder → total kVA, straight from the database.

- 💡 For "what's out there at all," use **All feature classes (count by class)** — a per-class tally of everything in the current scope, handy after an incident to brief a crew on what plant is involved.

### 11.7 Task — scope a query in space (territory / radius)

Aggregate queries (and the all-classes count) can be **scoped**:

- **Everywhere** — the default.
- **Territory** — everything under a chosen part of the territory tree.
- **Radius of a point** — "within N metres of *here*": switch the scope to **Radius**, 🖱️ click a **centre** on the map, pick a **radius** (presets or type metres). VoltEdge draws the geodesic circle and counts what's genuinely inside it.

**Worked example.** After a car hits a pole, Priya needs what sits within 150 m. All-classes count → scope **Radius of a point** → click the pole → 150 m. VoltEdge draws the circle and returns the per-class tally inside it — three spans of conductor, one transformer, two poles. (This is the §2.4 radius scope, in its home.)

- ⚠️ Spatial scope is an **Aggregate**/all-classes feature. Plain **Filter** mode has no draw-an-area filter — if you need "features inside this shape," count them with a radius scope, or use the **Select** marquee (§11.10) to grab them by hand.

### 11.8 Task — drill from a count down to the rows

Every count is a doorway, not a dead end.

**▶ Quick steps**

1. Run any **Aggregate** or **all-classes** query.
2. **Click a number** — the grand total, a group-by bucket, or a class in the breakdown.
3. The builder re-runs as a **Filter** with that narrowing applied and lists the **actual features** behind the count — which also highlight on the map.
4. **Back to counts** returns you to the summary.

**Worked example.** From the "capacity per feeder" table (§11.6), the planner clicks the row for Feeder 7 — the panel lists the individual transformers that make up that total and lights them up, so she can see *which* assets, not just the sum.

### 11.9 Export query results

Any result set exports to **CSV**, generated in your browser (no round trip):

- a **Filter** list → `{class}-query.csv` (id, name, status, and every attribute column),
- an **Aggregate** result → `{class}-aggregate.csv`,
- the **all-classes** breakdown → `feature-classes-in-scope.csv`,
- a **drill-down** list → `{class}-features.csv`.

**How it works.** The download is built from the rows already on screen, so it's instant and matches exactly what you're looking at — no server export job, no waiting.

### 11.10 "Querying on the map" — three tools, three jobs

People say "query the map" for three different things. Keep them straight:

| Tool | What it's for | Writes anything? |
|---|---|---|
| **Query builder** (§11.4+) | Ask the database a structured question about **your** features — filter, count, scope, drill, export. | No |
| **Select** marquee (§1.5) | **Hand-pick** features by rubber-banding a box on the map — a visual multi-select that feeds the **Bill of Quantities** and the temporary one-line. | No |
| **Identify** | Click to read attributes of a **third-party service layer** (an external WMS/WFS/Esri layer you've added) — *not* your own features. | No |

- 💡 Rule of thumb: **Query builder** when you can *describe* what you want ("all 315 kVA transformers"); **Select** marquee when you can *see* what you want and just need to box it; **Identify** only for someone else's map layer.
- ⚠️ The Select marquee and the Query builder are separate — a marquee selection doesn't become a query, and a query's results aren't the marquee set. They just happen to both highlight on the map.

### 11.11 One-card recap

| I want to… | Do this |
| --- | --- |
| See a feature's photos/documents | Open it → **Files** tab (read-only) |
| Attach or delete files | **Files** → **Manage files** → Upload / trash (needs `attachments:write`) |
| Capture a site photo in the field | Field app → **Files** → camera/gallery → uploads on **Sync** |
| List features matching conditions | **Query** → **Filter** → class → conditions → **Run** |
| Count / total / group features | **Query** → **Aggregate** → group-by + Count/Sum/Avg/Min/Max |
| Count what's within N m of a point | **Query** → **Aggregate**/all-classes → scope **Radius** → click centre |
| See the rows behind a count | Run an aggregate → **click the number** |
| Export what I'm looking at | Any result → **Download** (CSV) |
| Box-select features to price them | **Select** tool → rubber-band → feeds Bill of Quantities |

---

## That's the cookbook

You've reached the end. Across eleven chapters this book has walked every hands-on thing you do on
the VoltEdge map — reading it and its tools, finding and opening features, **creating** them in
every geometry, editing their **attributes** and their **geometry**, **measuring** and drawing,
**snapping** for precision, **connecting** and **relating** (and the guided assemblies that do
several joins at once), **annotating** with redlines, labels, files and links, tuning the map to
how you work and getting yourself unstuck when it doesn't behave, and finally **attaching files**
and **querying** the network — filtering, counting, scoping by area and drilling into the answers.

Where this book gives you the **exact sequence**, its companion — the **[User
Handbook](user-handbook.md)** — gives you the *why*: the mental model of feature classes,
terminals, territory and permissions that everything here rests on. When a recipe here mentions
a permission or a network role and you want to understand it properly, that's the book to open.
For the deeper technical lanes — what a study computes, how a substation is modelled end to end,
or the exact fields of one class — the **[Engineering Studies](engineering-studies.md)**,
**[Substation Guide](substation-guide.md)** and **[Feature Catalog](feature-catalog.md)** are
waiting on the shelf.

Keep this one open on a second monitor while you work. That's what it's for.
