# Faults module

A **fault** is an **electrical fault event on the network** — an earth fault, a
phase-to-phase short, a protection operation. It is not a GIS feature drawn on
the map, and it is neither of the two operational records that already exist:

- **OMS** (`docs/phase-49-oms.md`, migration 0087) tracks the customer-facing
  **outage** an incident represents — who is off supply, ETR, SAIDI/SAIFI. Its
  cause is free text.
- **WAM** (migration 0096) tracks a slow-degrading asset **condition** — a
  defect — and the corrective work order that fixes it.

The fault event is the **causal hinge** between them. It **explains** an incident
(the outage the fault caused) and **triggers** a defect (the asset failure booked
for repair):

```
   FAULT EVENT (electrical event on the network)
   ├── explains ──►  oms.incident   (the outage it caused,   bare incident_id)
   └── triggers ──►  wam.defect  ──► corrective work_order    (bare defect_id)
```

Previously "faults" were conflated into `wam.defects`; this module elevates the
fault event to a first-class record so protection performance (the device that
operated, the clearing time, the fault current) has somewhere to live and the
OMS↔WAM link has a subject.

## Why its own schema and package

A fault is an operational record about the assets on the map, not a feature on
it — the same reasoning oms / wam / circuit follow. So faults owns its own
`faults` schema (migration 0106) and Go package (`api/internal/faults`), outside
the metadata compiler.

**Cross-schema convention (matches oms 0087, wam 0096):** references to a
per-class feature table (`asset_id`, `protective_device_id`) or to a sibling
subsystem's record (`incident_id` → oms.incidents, `defect_id` → wam.defects)
are **bare uuids, not enforced FKs** — loose coupling between subsystems we don't
want to chain migrations to. This table has no within-schema references, so it
carries no real FKs.

## Data model (migration 0106)

`faults.fault_events` — one row per fault event:

- **Anchor** (`fault_events_anchor_ck`): a fault must be locatable by *something*
  — an asset (`asset_class` + `asset_id`, all-or-nothing), a `location` pin, or
  the `incident_id` it caused. It never floats with none of the three.
- **Electrical:** `fault_type` (`SLG` / `LL` / `LLG` / `LLL` / `LLLG` / `earth` /
  `unknown` — the vocabulary the fault-current/protection engines speak),
  `cause` (free text, no taxonomy — same stance as `wam.defects.defect_type`),
  `fault_current_a`.
- **Protection:** `protective_device_class` + `_id` (the device that operated,
  all-or-nothing, bare ref — mirrors `oms.incidents.predicted_device_*`),
  `clearing_time_ms`.
- **Lifecycle:** `status` (`detected` → `investigating` → `cleared` → `closed`);
  `cleared_at` kept consistent by `fault_events_cleared_ck` (a cleared/closed
  fault has a clearing time and only those do — the discipline oms/wam keep).
- **Links:** `incident_id` (bare → oms), `defect_id` (bare → wam).
- **Provenance:** `detected_by` (scada / protection_relay / field_crew /
  customer_call / model…), `detected_at`.

`location` is written through `ST_SetSRID(ST_MakePoint(…), 4326)` and read back
via `ST_X`/`ST_Y` onto scan-only `longitude`/`latitude` — the split oms/wam/crud
use.

## Permissions (migration 0107)

A new `faults:read` / `faults:write` capability pair (constants
`auth.PermFaults{Read,Write}`), gated method-aware on the `/faults` mount. A
fault straddles both subsystems but is neither an outage nor an asset-condition
record, so it earns its own capability — a control engineer can log and analyse
faults without holding outage-dispatch or work-order rights. Grants mirror
oms/wam: viewer/analyst/editor + admins read; editor + admins write.

## The two composites (the genuinely new logic)

Mirroring WAM's `RaiseWorkOrderFromDefect`, one action wires a fault to each
neighbour, atomically:

- **`RaiseFaultFromIncident`** (`POST /faults/from-incident/{id}`) — mint a fault
  for an OMS incident and link it. The fault inherits the incident's predicted
  protective device (as the device that operated) and cause, anchored by the
  `incident_id`. The incident is read cross-schema by SQL (no oms Go import) —
  the loose coupling kept at the DB level.
- **`BookDefectFromFault`** (`POST /faults/{id}/defect`) — mint a WAM defect for
  the asset the fault hit and link it back (`defect_id`). The defect is sourced
  `outage` and carries the fault's `incident_id`, so it flows straight into WAM's
  existing `RaiseWorkOrderFromDefect` loop — closing event → repair without a new
  WAM path. Reuses `wam.CreateDefect` wholesale (faults depends on wam
  one-directionally — the natural layering, faults orchestrating above both). A
  fault must carry an asset or a location for this (an incident-only fault has
  nothing concrete to book against).

## Audit trail (migration 0109)

`faults.fault_event_history` is an **append-only log of every change to a fault**
— the faults analogue of `gis.feature_history` (migration 0067), built the same
way. One immutable row per write records **who** (`actor_id`, a soft ref to
`iam.users` resolved by `LEFT JOIN` at read time), **when** (`at`), **what kind**
of change (`action` — `created` / `status_changed` / `updated` / `defect_booked`),
and the **full fault state after** the change (`snapshot` jsonb). It answers
"who moved this fault to cleared, and when?" — which the `fault_events` row alone,
carrying only latest-state timestamps, could not.

The write path (`api/internal/faults/history.go`, `recordFaultHistory`) inserts a
row from `CreateFault` / `SetFaultStatus` / `UpdateFault` / `BookDefectFromFault`,
in the **same connection or transaction** as the fault write, so the audit row
lands with the change (and, inside the two composites' transactions, commits
atomically or rolls back with it). No foreign keys, on purpose: `fault_id` is a
bare uuid so the trail outlives the fault, and `actor_id` a soft ref so deleting a
user never erases the record of what they did. `GET /faults/{id}/history` returns
the timeline newest-first with the actor resolved to a display name / email; the
Faults board shows it under a per-row **History** toggle.

## REST surface (`/faults`)

```
GET   /faults                     the fault board (?status=, ?open=true, ?fault_type=, ?asset_class=&asset_id=, ?incident_id=)
GET   /faults/map                 faults with a placeable point, for the map overlay (same filters)
POST  /faults                     log a fault event
GET   /faults/{id}                one fault event
GET   /faults/{id}/history        the fault's audit trail (who changed what, when)
GET   /faults/{id}/impact         the fault's de-energized downstream section (for the overlay)
PATCH /faults/{id}                patch investigation findings (classification, cause, device, timings)
POST  /faults/{id}/status         transition (detected/investigating/cleared/closed)
POST  /faults/{id}/defect         book a WAM defect from the fault
POST  /faults/from-incident/{id}  raise a fault from an OMS incident
```

## Map overlay (Slice 3)

A fault is not a GIS feature, so it has no compiled table and no tile function --
it can't be a Martin layer like the network classes. Instead it rides the same
in-memory, app-fed overlay plumbing the OMS affected-area highlight uses.

- **Placing it on the asset.** `GET /faults/map` returns every fault that
  resolves to a point, and — for an asset-anchored fault — the **asset's own
  geometry** (`ST_AsGeoJSON(geom)`) so the overlay highlights the actual asset
  rather than a derived point. A location-anchored fault keeps its own pin (no
  asset geometry). Resolution is per-class in `api/internal/faults/geometry.go`
  (`assetGeoms` -> `ListFaultsForMap`, returning `FaultMapMarker` = the fault plus
  its GeoJSON `geometry`): the class's `physical_table` is looked up in
  `metadata.feature_classes`, then geometry is batch-resolved by id. The **marker
  point** is `ST_ClosestPoint(geom, ST_Centroid(geom))` — a point guaranteed to
  sit *on* the geometry for any type (a line's on-line midpoint, a polygon's edge,
  a point itself), unlike a bare centroid, which for a **curved or multipart line
  floats mid-span or off the line entirely** (the reason a plain triangle was
  wrong for line assets). A fault with no resolvable point (incident-only, or an
  asset that's uncompiled / deleted / geometry-less and carries no pin) is omitted
  — it has nothing to draw; asset geometry is preferred over a location pin when
  both are present.
- **The overlay layer.** A persistent, user-toggled layer (unlike the OMS
  incident-driven highlight): a **"Faults" row in the layers panel** whose
  per-status sub-toggles double as the legend. `faultStyleForFeature`
  (`lib/map-layers.ts`) is **geometry-aware**: a **point** asset (or a
  location-only pin) is a status-colored warning **triangle**, offset up-and-right
  of the point (`FAULT_MARKER_DISPLACEMENT`) so it doesn't hide the feature's own
  symbol; a **line** asset is
  the whole faulted run **highlighted** in the status color over a white casing; a
  **polygon** asset is a status-colored outline + translucent fill. A line/area
  fault draws ONLY its highlight (no triangle on top — the highlight already shows
  what faulted); the highlight itself stays clickable (it's in the one marker
  layer the click handler hit-tests). Color is one axis — status (hot red
  `detected` down to grey `closed`), fault type kept for later detail rather than a
  second color. Default shows the LIVE statuses (`detected` + `investigating`),
  matching the board's "Open only" default; `cleared`/`closed` are one click away
  in the legend. State lives in `stores/fault-overlay-store.ts` (`visible` +
  `statuses` persist).
- **Pulsing live faults.** The LIVE faults (`detected` + `investigating`) pulse a
  "radar ping" that fades as it grows — an expanding ring around a point fault's
  triangle, or an expanding glow stroke around a line/area fault's highlight — to
  draw the eye to faults still needing attention; `cleared`/`closed` stay steady
  (not urgent). It is drawn per frame on the marker layer's `postrender` via
  `getVectorContext` (the base marker/highlight underneath stays static), and the
  loop self-sustains
  by calling `map.render()` only while something is actually pulsing, so it costs
  nothing when the overlay is hidden or shows no live faults. A **"Pulse live
  faults"** legend toggle (`pulse`, persisted, default on) switches it off, and the
  OS `prefers-reduced-motion` setting suppresses it regardless.
- **Clicking a marker** draws that fault's impact area and, when the fault is
  asset-anchored, selects the underlying asset -- which highlights it in place and
  opens its `FeaturePanel` (whose Faults tab shows this very fault). "Marker +
  highlight", reusing the existing selection path rather than a new popup.
- **The impact area.** `GET /faults/{id}/impact` traces the downstream service
  points fed through the fault's **protective device** (`FaultImpactArea`, reusing
  the shared `trace` engine directly -- a lower layer, so no `oms` import, keeping
  the faults->oms decoupling intact) and returns them in the same shape as
  `oms.AffectedArea`, so the map draws them with the same rose halos. A fault with
  no protective device yields an empty set (just the marker). The endpoint fires
  only when a marker is clicked, keyed on a per-click focus token so the view
  fits the cone once.

## Build slices

- **S1 — core (this slice)**: migration 0106 (`faults` schema + `fault_events`),
  migration 0107 (permissions), the `api/internal/faults` package (CRUD + both
  composites + REST), the `/faults` mount, and a portable demo seed
  (`seed/faults_example.sql`, `task seed-faults` — three location-anchored
  events spanning the lifecycle, depends only on 0106).
- **S2 — frontend** *(done)*: the `use-faults` hook (`web/src/hooks/use-faults.ts`),
  a Faults board page (`web/src/routes/faults.tsx`, like `works.tsx`) at `/faults`
  gated `faults:read` with a sidebar entry, a "Faults" tab on the map attribute
  panel (`web/src/components/map/feature-faults-tab.tsx`) showing the selected
  asset's fault history, the "Raise fault" action on the OMS incident row
  (`web/src/routes/outages.tsx`, gated `faults:write`) and "Book defect" on a
  fault (both the board and the map tab). Write controls self-gate on
  `faults:write`, matching the outage and work boards.
- **S3 — map overlay** *(done)*: the fault-event map overlay above -- `GET
  /faults/map` + `GET /faults/{id}/impact` (`api/internal/faults/geometry.go`),
  the `fault-overlay-store`, a status-colored marker layer + a rose impact layer
  in `voltedge-map.tsx`, `faultStyleForFeature`/`FAULT_STATUS_COLORS` in
  `lib/map-layers.ts`, and the "Faults" overlay row + per-status legend in
  `layers-panel.tsx`. Clicking a marker draws its impact and opens the anchoring
  asset's panel. The fault-caused **reliability roll-up** (fault-attributed SAIFI
  contribution) is still to come.

## Owed

`go build` + `npm build`, apply migrations 0106/0107 **and 0109** (`task
migrate`), compile nothing (no gis change), `task seed-faults` to populate the
board and overlay. S1 (backend), S2 (frontend), the audit trail (0109) and S3
(the map overlay) are all built; only the builds/migrations/seed above remain to
run. The remaining unbuilt piece is the fault-caused **reliability roll-up**.
Follow-ups: mirror the History toggle onto the map attribute-panel Faults tab,
and territory-scope the overlay to the caller's access.
