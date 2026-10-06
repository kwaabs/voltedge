# VoltEdge Field Tool — Field Crew Guide

**Who this is for:** field technicians and crews who use the VoltEdge app on a
phone or tablet to view and update the network **in the field, with no signal**,
and sync their work back when they return to coverage.

If you manage packs, tokens, or the servers behind this app, see the companion
**Field Tool Admin Guide** instead.

---

## 1. What the field tool is

The field tool is an **offline-first** app. Before you head out, you pull a
**pack** — a self-contained copy of one work area — onto your device. Out in the
field you view the network, edit it, create and delete features, wire
connections, take photos, run inspections, raise faults, and scan asset tags —
**all with no network connection.** Everything you do is saved on the device.
When you get back to coverage, you **sync**, and your work is sent to the office
system.

The golden rule: **your edits are never lost.** They live on the device until the
server has confirmed it received them, and even then the app tells you whether
each one was *accepted* or *needs your attention*.

```
   AT THE DEPOT (online)          IN THE FIELD (offline)            BACK AT THE DEPOT (online)
  ┌────────────────────┐        ┌────────────────────────┐        ┌────────────────────────┐
  │  Download a pack    │        │  View the network      │        │  Sync                  │
  │  for your work area │ ─────► │  Edit / create / delete│ ─────► │  Your work is sent to  │
  │  (needs signal)     │        │  Photos / GPS / tags   │        │  the office; outcomes  │
  │                     │        │  (saved on the device) │        │  come back to you      │
  └────────────────────┘        └────────────────────────┘        └────────────────────────┘
       needs signal                   NO signal needed                   needs signal
```

You only need a connection at the **start** (to download your work area) and at
the **end** (to sync). Everything in between is fully offline.

---

## 2. How it works, at a glance

When you download a pack, two files come onto your device together:

| File | What it is | What you notice |
|------|-----------|-----------------|
| **The map tiles** | A pre-drawn map of your work area | The map draws instantly, even with no signal |
| **The data pack** | The real features, with full detail, and the connectivity/relationship links between them | Tapping a feature shows its full record; you can edit it |

A third file is created the first time you make an edit: a **local edit store**
that holds your offline changes until they sync. You never manage it directly —
it's just where your unsynced work is kept safe.

**The important idea:** the map you see is a *picture*, but the feature you *tap*
and *edit* is the real, full-detail record. So you can trust the detail panel and
the edit form even though the map itself is a lightweight drawing.

---

## 3. Your work area: the pack

A **pack** is a snapshot of one bounded area at one moment in time.

- **It's bounded.** You choose the area (a box on the map) and which layers to
  include. A pack covers *that* area — not the whole network. For a different
  area, you download a different pack.
- **It's a snapshot.** The pack reflects the network as it was **at the moment it
  was built**. If the office changes something after that, your pack won't show
  the change until you download a fresh pack. (This matters when you sync — see
  §*Syncing* and §*When an edit needs attention*.)
- **It carries only what you're allowed to see.** The extras a pack ships —
  connectivity links, relationships, the work board, the fault board, asset tags
  — depend on your account's permissions. If your pack doesn't show a work board
  or fault board, your account isn't set up for them; ask your administrator.

### Zooming in and out

The map tiles are drawn for a range of zoom levels. You can zoom **out** to see
your whole work area and navigate between jobs, and zoom **in** past the tile
detail — the app scales the map up so you can get right on top of a feature, and
tapping still lands on the exact, full-precision record. So don't worry if the
map looks a little coarse when you're zoomed in very far: the data underneath is
exact.

---

## 4. Reading the map — the colour code

As you work, features change colour to show their state. Learn this legend — it's
how the app tells you what's saved and what still needs to sync.

```
  ┌──────────────┬───────────────────────────────────────────────────────────┐
  │  RED outline  │  The feature you have SELECTED right now                   │
  ├──────────────┼───────────────────────────────────────────────────────────┤
  │  AMBER        │  A packed feature you have EDITED offline, not yet synced  │
  │               │  (a hollow amber ring on a point = you MOVED it)           │
  ├──────────────┼───────────────────────────────────────────────────────────┤
  │  GREEN        │  A feature you CREATED offline, not yet synced             │
  ├──────────────┼───────────────────────────────────────────────────────────┤
  │  BLUE dot     │  A point you are in the middle of PLACING (a draft)        │
  ├──────────────┼───────────────────────────────────────────────────────────┤
  │  (hidden)     │  A feature you DELETED offline is removed from the map     │
  └──────────────┴───────────────────────────────────────────────────────────┘
```

Alongside the colours, the detail panel shows a small **"Edited offline — not yet
synced"** chip on any feature with pending work, so you can tell at a glance which
records are still waiting to go up.

**Rule of thumb:** *amber or green on the map = unsynced work.* When everything is
synced and you've downloaded a fresh pack, the amber and green clear.

---

## 5. What happens when you sync — the lifecycle

Syncing is **two stages**, and understanding the difference saves confusion:

1. **Captured** — your device hands the edit to the server, and the server stores
   it durably. At this point the edit is *safe on the server* but **not yet
   applied** to the live network.
2. **Applied** — moments later, the server processes the edit against the live
   network. This is where it's actually accepted — or held back.

So after a sync, each edit moves through this path:

```
     your device                    the server
   ┌───────────┐   sync    ┌─────────────┐   the office system
   │  PENDING   │ ───────► │  CAPTURED    │ ──────────────────────────────┐
   │ (on device)│          │ (safe, queued)│                              │
   └───────────┘          └─────────────┘                                 ▼
                                                     ┌─────────────────────────────────┐
                                                     │  APPLIED     ✓  it landed        │
                                                     │  REJECTED    ✗  it couldn't apply │  ◄─ needs
                                                     │  CONFLICT    ⚠  someone edited it  │     your
                                                     │              first                │     attention
                                                     │  SUPERSEDED  –  a newer edit of   │
                                                     │              yours replaced it    │
                                                     └─────────────────────────────────┘
```

- **Applied** — done. The office now has your change.
- **Rejected** — the edit couldn't be applied (for example the data failed a
  rule, or a connection you drew isn't allowed). It lands in your **"needs
  attention"** list with a reason.
- **Conflict** — someone in the office changed the same feature *after* your pack
  was built, so applying your edit would overwrite their change. It's held for a
  supervisor to resolve, and shows in your "needs attention" list.
- **Superseded** — you edited the same feature twice offline; only your latest
  edit is kept. This happens silently and needs nothing from you.

Because syncing captures first and applies second, a dropped connection mid-sync
is safe: whatever was captured stays captured, and you just sync again to send the
rest.

---

## 6. When an edit needs attention

If an edit comes back **Rejected** or **Conflict**, the app flags it (a badge/count
on the sync control, and a "needs attention" list). For each one you'll see the
reason. What to do:

- **Rejected** — read the reason. Usually it's a data problem (a required field, a
  bad value, or a connection the network rules don't allow). Fix the feature and
  edit it again — that submits a fresh attempt. Then acknowledge the old one so it
  drops off the list.
- **Conflict** — the office changed this feature after your pack was built. You
  can't resolve this yourself; a supervisor decides whether to keep your version
  or theirs. Acknowledge it once they've sorted it, or re-do the edit on a fresh
  pack if asked.

Acknowledging a flagged edit only clears it from *your* list — the office keeps
the full record either way.

---

## 7. Getting set up — the Connection screen

Before you can download anything you need to point the app at your server and sign
in. Open **Connection** (the gear icon on the home screen).

1. **fieldsync base URL** — the address of your server, e.g. `http://10.0.2.2:3706`
   on an emulator or `http://<LAN-ip>:3706` on a real device. Your administrator
   gives you this. Tap **Save base URL** if you change it.
2. Enter your **Email** and **Password** (the eye icon shows/hides the password)
   and tap **Sign in**. The button reads *Signing in…* while it works.

When you're signed in the screen shows **Signed in as …** with your name. You stay
signed in — you don't log in every time. Use **Sign out** only when handing the
device to someone else (it keeps the base URL, just drops your login).

> You never type or see a "token" — signing in handles that for you.

---

## 8. Getting your work area — downloading a pack

On the home screen (**VoltEdge Field**) packs are shown in two lists:

- **On this device** — packs already downloaded, ready to use offline.
- **Available from server** — packs the server has built (or is building) for you.

### Requesting a new pack

1. Tap **New pack**.
2. Set the **Bounding box** — the easiest way is **Draw on map**: on the
   **Draw bounding box** screen, tap two opposite corners of your work area (or
   **Use current view**), then **Use this box**. (You can also type the four
   numbers.)
3. Under **Feature classes**, tick the layers you need. Use the search box
   (*Filter by name or code*) and the per-group **All** / **None** toggles. The
   count shows *N selected*.
4. Tap **Request build**.

Your pack now appears under **Available from server** and moves through:

```
   queued  ──►  running  ──►  succeeded     (then: tap Download)
      │             │             │
   (waiting)   (being built)   ready       ──► failed  (tap to see why)
```

Pull down to refresh the list. When it shows **succeeded**, tap the **Download**
icon — a progress ring shows the transfer. When it finishes it opens the pack.

> **Downloads and syncs need signal; everything in between does not.** Download
> your packs at the depot on wifi before you head out — a pack can be tens of MB.

### Using and managing a pack

- Tap a downloaded pack to open its **Pack detail** (summary, feature classes, and
  **Open map**).
- From a pack's **⋮** menu you can **Open map** or **Delete** it from the device.
- **Cancel** a build that's still queued/running with the stop icon.

---

## 9. Looking at a feature — the detail panel

Open a pack and tap **Open map**. The workspace looks like this:

```
  TABLET (wide)                             PHONE (narrow)
  ┌───────────────────────┬─────────────┐   ┌───────────────────────┐
  │                       │  Feature     │   │                        │
  │        MAP            │  detail      │   │          MAP           │
  │                       │  panel       │   │                        │
  │                       │  (360 px)    │   │                        │
  │                       │              │   ├────────────────────────┤
  │                       │              │   │  detail  (bottom sheet) │
  └───────────────────────┴─────────────┘   └───────────────────────┘
```

**Tap a feature** to select it (it gets a **red** outline) and open its detail. If
several features sit on the same spot, a **"N features here"** sheet lets you pick
the right one — so a line hidden under a point stays reachable.

The detail panel shows:

- **Identity** — ID, Class, Status, Subtype, Territory.
- **Attributes (N)** — the feature's fields (humanized names like *Voltage class*).
- **Photos**, **GPS survey quality**, **Connections (N)**, **Relationships (N)**.
- Tappable **neighbour rows** under Connections/Relationships — tap one to jump to
  that feature and **walk the network hop by hop**.

The action icons across the top of the panel are where you **Edit attributes**,
**Edit geometry**, **Connect**, **Relate**, **Delete**, and (for a feature you
created offline) **Discard**.

---

## 10. Editing a feature's attributes

1. Select the feature and tap **Edit attributes** (the pencil).
2. The panel becomes a form. Controls match each field's type:
   - a **switch** for yes/no fields,
   - a **dropdown** (*Select…*) for coded fields — you pick from the allowed list,
   - a **date picker** for dates,
   - a **number** keyboard for integer/decimal fields,
   - plain text otherwise.
3. Fields marked with a red **\*** are **required** for this feature's subtype —
   Save is blocked until they're filled (an empty one shows *Required*).
4. Fields under **LOCKED AFTER PUBLISH** (with a padlock) can't be changed once the
   feature is published — they're shown read-only for reference.
5. Tap **Save** (or **Cancel** to discard).

The feature now shows a **hollow amber ring** on the map and an **"Edited offline
— not yet synced"** chip in the panel. It stays that way until you sync.

> Revert a field back to its original value and it drops out of your pending
> change automatically — if you undo *every* change, the pending edit disappears.

---

## 11. Moving a feature — the geometry editor

1. Select the feature and tap **Edit geometry** (the location-pencil).
2. A bar appears at the bottom of the map:
   - **Point:** *Drag the point to move it.*
   - **Line/polygon:** drag any **blue** vertex to move it; tap a **white**
     midpoint to **add** a vertex; select a vertex and use **Delete selected
     point** to remove one (down to the minimum — 2 for a line, 3 for a polygon).
3. **Undo** steps back one change; **Cancel** throws the edit away.
4. Tap **Save**.

A moved feature shows as a **filled amber dot** (its original position is hidden so
you only see where it is now). Like an attribute edit, it's pending until you sync.

> **Snapping:** turn on **Snapping** in Map settings and a placed point, dragged
> vertex, or inserted midpoint snaps onto the nearest existing feature vertex — so
> a new connection lines up exactly with the asset it joins.

---

## 12. Creating a feature — point, line, polygon

1. Tap the **Create** button on the map.
2. Choose the class from **Create a feature**. (Classes you can't draw are listed
   greyed-out as *… — not supported*.)
3. **Draw the geometry** — a banner at the top guides you:

   ```
   POINT    │ Tap the map (or Use my location) to place it.      │ then Place
   LINE     │ Tap to add points, then Place.                     │ (≥2 points)
   POLYGON  │ Tap to add points; the ring closes automatically.  │ (≥3 points)
   ```

   - **Use my location** drops the point/vertex at your GPS position.
   - For lines/polygons you can **Record a GPS track** and *walk* the line, then
     **Stop GPS tracking**. **Undo last point** removes the last one.
4. Tap **Place**.
5. Fill the **create form**: pick a **Subtype** first (it decides which fields are
   required), an optional **Name**, then the attributes — same controls as editing.
6. Tap **Create**.

The new feature appears **filled green** on the map and is selected. It's yours and
pending until you sync. To fix it before syncing, select it and **Edit** (the form
reopens); to abandon it, use **Discard** — a created feature has no server copy, so
discarding simply removes it.

---

## 13. Deleting a feature

1. Select a packed feature and tap **Delete feature**.
2. Confirm on the **Delete feature?** dialog (*… will be marked deleted and
   soft-deleted on the server when you sync. You can undo before then.*).

The feature **disappears from the map** and the panel shows a *Marked deleted
offline …* banner. Changed your mind? Select it again and tap **Undo delete** —
it comes back. (A feature you *created* offline is removed with **Discard**
instead, since there's nothing on the server to delete.)

---

## 14. Wiring connections and relationships

Both work the same way: start on one feature, then tap the other.

### Connections (electrical links)

1. Select a feature and tap **Connect to another feature**. *(This action is
   hidden for features that aren't part of the electrical network.)*
2. If the feature has named **terminals**, pick which one on the **Terminal on …**
   sheet (or **No specific terminal**).
3. A green banner reads *Tap the feature to connect to …* — tap the target
   feature.

The connection is queued and drawn as a **green dashed "marching-ants" line**
(it reads like current flowing, distinct from a real solid conductor). If the pack
carries network rules and the connection isn't allowed, you'll get a
**Connection not allowed** message explaining why — pick a valid pair instead.

### Relationships (non-electrical links, e.g. containment)

1. Select a feature and tap **Relate to another feature**.
2. A purple banner reads *Tap the feature to relate to …* — tap the target.
3. If the pair supports more than one kind, pick it on the **Relationship to …**
   sheet.

The relationship is drawn as a **purple dashed line**. A pair no class permits
shows **Relationship not allowed**.

> Connections and relationships you make offline are **advisory on the device** and
> **re-checked by the server when you sync** — so an edge the server ultimately
> rejects comes back in your "needs attention" list.

---

## 15. Using GPS and external GNSS receivers

Tap the **GPS quality** pill (top-left of the map) to expand the live readout:

- **Fix quality** — *RTK fixed / RTK float / DGPS / GPS fix / No fix*, with a
  coloured tier: **green** Excellent/Good, **amber** Moderate, **red** Poor.
- **Accuracy**, **Satellites** (used / in view), **PDOP/HDOP/VDOP**, altitude, and
  whether it's the **Internal** or **External receiver**.

Wherever you see **Use my location** (placing a point, tracing a line), it uses
this fix, and the quality at that moment is saved with the feature (visible later
under **GPS survey quality**).

### Using an external (Bluetooth) receiver

For survey-grade accuracy, pair a Bluetooth GNSS receiver in your phone's system
settings first, then in the app open the **GPS source** picker (the antenna icon,
or **GPS source** in Map settings):

- **Phone GPS** — the built-in receiver (always available).
- **Paired Bluetooth receivers** — pick yours; the readout then shows *External
  receiver* and its richer fix data.

> External receivers are **Android-only**; on iOS the app uses the built-in GPS.
> If Bluetooth is off or permission hasn't been granted, the picker tells you
> exactly what to do (**Allow Bluetooth**, or pair the device in system settings).

---

## 16. Taking photos

In a feature's detail panel, find the **Photos (N)** section and tap **Add**:

- **Take a photo** — use the camera, or
- **Choose from gallery**.

Each photo shows a small badge: **amber "Not uploaded"** until it syncs, then
**green "Uploaded"**. Tap a photo to view it full-screen (pinch to zoom). Your
offline captures open from the device even with no signal; already-synced photos
fetch the full-resolution original when you're online.

Photos sync alongside your other changes (see §19).

---

## 17. Inspections, defects and faults

If your pack includes **Work & Asset Management** or **Faults** data, you'll see
**Work & assets** and **Faults** entry points (on the Pack detail screen, and as
badges on the map). These are your offline work board and fault board.

### Work & assets (the Work screen)

Three tabs: **Work**, **Defects**, **Inspections**.

- **Work** — the work orders assigned to you. Open one to **Start work**,
  **Complete** (with optional *Completion notes*), **Run inspection**, or **Raise
  defect**.
- **Defects** — open defects on your assets. Tap **Defect** to capture a new one:
  pick the asset, then fill **Severity**, **Title \***, type, description — and
  attach a photo. Use **Update status** (*Mark work ordered / resolved /
  dismissed*) on an existing one.
- **Inspections** — the active checklists. Open one, pick the asset, answer each
  item (**Pass** / **Fail**, or a value/rating, with an optional note), add
  **Overall notes**, and tap **Record inspection**. *A failed item that carries a
  severity automatically raises a defect when you sync.*

### Faults (the Faults screen)

The open faults on your assets. Tap **Fault** to raise one: pick the asset (or
**No asset — report by location** to pin your GPS position), choose a **Fault
type**, fill **Title \*** and the rest, and **Save fault**. On an existing fault
use **Update status** (*investigating / cleared / closed*) or **Book defect** to
turn it into a WAM defect.

> Everything you capture here — defects, inspections, faults — is **saved offline**
> and goes up on the next sync, exactly like a feature edit. A **"Not synced"** /
> **"Captured offline"** / **"Raised offline"** pill marks what's still pending.

### Reporting without an asset

For a problem on something not in the system yet, choose **No asset — report by
location**. The app pins your current **GPS position** so the office can find it.

---

## 18. Scanning an asset tag

Tap **Scan asset tag** (the QR icon on the map). Point the camera at the **QR or
barcode** on the asset's tag — the app reads it and jumps to that feature on the
map. Use **Torch** in the dark, **Switch camera** if needed, or **Enter code
manually** to type a code like `VE-7F3K2Q`.

Outcomes:

- **Resolved …** — the feature is selected on the map.
- **Resolved … — superseded label** — a reissued tag; it still works.
- **No asset found for tag …** — the code isn't in this pack.
- **Tag … (outside the downloaded pack)** — the asset exists but isn't in your
  work area; build a pack covering it to work with it offline.

Scanning works **offline** against the pack's tag index first, then checks online
if the pack doesn't have it.

---

## 19. Syncing your work

When you're back in coverage, send your work up. The map's top bar shows a **sync
badge** with your **pending count** (and a **red "needs attention"** badge if any
earlier edit was flagged). Tap it to open the pending sheet.

The sheet lists everything waiting — edits, creates, deletes, connections,
relationships, WAM/fault captures, and photos — each with its own **Push this
change** button, plus a single **Sync N changes** button at the bottom to send
them all.

```
   ┌─ Pending changes ───────────────────────────────┐
   │  ⚠ 1 change needs attention        [Acknowledge] │
   │  ─────────────────────────────────────────────── │
   │  ✎ POLE · 2 fields changed          [Push]        │
   │  ＋ MV_CONDUCTOR · new feature       [Push]        │
   │  🗑 DIST_XFMR · delete               [Push]        │
   │  📷 photo.jpg · POLE · photo         [Upload]      │
   │  ─────────────────────────────────────────────── │
   │            [  Sync 4 changes  ]                    │
   └─────────────────────────────────────────────────┘
```

After syncing you'll see a result like **"Synced 4 changes."** (with
*" · 1 needs attention"* if the server flagged any). Remember the two-stage model
from §5: a successful sync means your work is **safely captured** on the server;
the office applies it moments later, and anything it can't apply comes back to your
**needs attention** list.

- A dropped connection mid-sync is safe — re-open and **Sync** again; already-sent
  changes won't double up.
- You don't have to sync everything at once — **Push this change** sends one item.

---

## 20. Troubleshooting

| Problem | What's happening / what to do |
|---------|-------------------------------|
| **The map is blank** | Zoom/pan to your work area — the pack covers only the area you drew. If still blank, the pack may have no features in view; check the pack's class list. |
| **"Set the fieldsync base URL and sign in…"** | You're not signed in. Open **Connection**, set the base URL, and sign in. |
| **New pack build shows "failed"** | Tap it to read the reason. Common causes: the area has no features, or it's **too large** (narrow the box or pick fewer classes). |
| **Can't download / "Download failed"** | You've lost signal, or the server host isn't reachable. Downloads need a connection; try again on wifi. |
| **An edit came back "rejected"** | Read the reason in **needs attention**, fix the feature, and edit it again (that resubmits). Then **Acknowledge** the old one. |
| **An edit came back "conflict"** | The office changed that feature after your pack was built. A supervisor resolves it — re-do your edit on a fresh pack if asked. |
| **A photo won't open** | Already-synced photos need a connection to fetch the full-resolution image; your own offline captures always open. |
| **Scanned tag "outside the pack"** | The asset is real but not in your work area — download a pack that covers it. |
| **"GPS unavailable — check location permission"** | Grant the app location permission in system settings. For a Bluetooth receiver, also allow Bluetooth and pair it first. |
| **My amber/green markers won't clear** | They clear once the edits are synced **and** you download a fresh pack (the current pack predates your changes, so it still shows your local copy). |

---

## 21. Quick reference

**Map colours**

| Colour | Meaning |
|--------|---------|
| 🔴 Red outline | Selected feature |
| 🟠 Amber (hollow ring) | Edited offline, not synced |
| 🟠 Amber (filled dot) | Moved offline, not synced |
| 🟢 Green (filled) | Created offline, not synced |
| 🟢 Green dashed line | Connection made offline |
| 🟣 Purple dashed line | Relationship made offline |
| 🔵 Blue | A point/line you're drawing; vertex handles |
| (hidden) | Deleted offline |

**Sync states**

| State | Meaning |
|-------|---------|
| Pending | On your device, not sent yet |
| Captured | Sent and safe on the server, not applied yet |
| Applied ✓ | Accepted into the office system |
| Rejected ✗ | Couldn't apply — needs your attention |
| Conflict ⚠ | Office changed it first — a supervisor resolves it |
| Superseded – | Replaced by your own newer edit (nothing to do) |

**Key actions**

| To… | Do this |
|-----|---------|
| Select a feature | Tap it (pick from *N features here* if stacked) |
| Edit attributes | Select → **Edit attributes** → **Save** |
| Move it | Select → **Edit geometry** → drag → **Save** |
| Create one | **Create** → class → draw → **Place** → **Create** |
| Delete one | Select → **Delete feature** → confirm (**Undo delete** to reverse) |
| Connect / relate | **Connect to** / **Relate to another feature** → tap target |
| Place at GPS | **Use my location** while drawing |
| Add a photo | Detail panel → **Photos** → **Add** |
| Scan a tag | **Scan asset tag** |
| Send your work | Sync badge → **Sync N changes** |

**Remember:** amber or green on the map = unsynced work. Sync before you consider a
job finished, and check the **needs attention** list for anything the office
couldn't apply.

