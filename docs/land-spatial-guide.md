# Land & Spatial Management

**Written for:** GIS and land officers who record easements, parcels and corridors; vegetation managers who keep trees off the lines; control engineers who need to know what land an asset sits on; and engineers who want the geometry and the formulas behind it.

The network runs across **land** — land someone owns, land a **wayleave or easement** grants the right to cross, a **corridor** kept clear, and **vegetation** that must be held back from the conductors. VoltEdge records those as feature classes and then **derives the relationships from the geometry itself**: *which assets sit inside this easement?*, *what land covers this pole?*, *what trees breach clearance on this feeder?* — and turns the last of those into a **trim work order** and a **due list**.

```
  LAND CLASSES ─► SPATIAL ENGINE ─► VEGETATION CLEARANCE ─► DISPATCH ─► DUE TRACKING
   easement/       what's inside /    trees within the        raise a WAM   easements expiring,
   parcel/         covering / near    clearance a voltage      trim order    trims overdue
   corridor/       (from the shapes)  requires → trim list     (one click)
   vegetation
```

Two layers, as elsewhere:
- **▶ Operator** — the feature panel's **Land use** tab: what overlaps/covers/neighbours a feature, the vegetation breaching a conductor (or a whole feeder), a one-click trim order, and an expiry/trim-due badge.
- **∑ Engineer** — the four classes, the PostGIS predicates, the per-voltage clearance rule, and the date queries.

**Legend.** 🔒 a permission. 💡 a tip. ⚠️ an honest limitation. **▶** operate lane; **∑** model lane.

> **Grounding note.** Drawn from the shipped code: the classes in `seed/land_spatial_example.sql`; the engine in `api/internal/spatial` (`spatial.go` the overlap/proximity query, `encroachment.go` the clearance scan, `trim.go` the WAM dispatch, `maintenance.go` the due queries); endpoints in `spatial/http.go` (mounted at `/spatial`, features:read); MCP tools in `agenttools/tools_spatial.go`; UI in `web/src/components/map/feature-spatial-tab.tsx` + `web/src/hooks/use-spatial.ts`. The engine reads the compiled `gis.<code>` tables directly — geometry-derived, so nothing has to be authored first.

---

## 1. The four land classes

All polygons, `network_role 'none'` (they are land/legal/environmental context, not nodes power flows through). Seeded `draft` — **publish them from /admin** after `task compile`.

| Class | What it is | Key fields |
|---|---|---|
| **LAND_PARCEL** | a cadastral land unit | `parcel_ref`, `owner_name`, `land_use`, `area_sqm` |
| **EASEMENT** | the legal right to keep the network's assets on the land | `easement_ref`, `easement_type`, `easement_status`, `grantor`, `granted_date`, **`expiry_date`**, `consideration` |
| **UTILITY_CORRIDOR** | the managed strip (right-of-way) the network runs through | `corridor_type`, `corridor_ref`, `width_m`, `managed` |
| **VEGETATION_ZONE** | a patch of vegetation, for clearance / trim analysis | `veg_type`, `species`, `height_m`, `condition`, **`last_trimmed`**, **`trim_cycle_months`** |

💡 Draw them on the map like any polygon feature. The bolded date fields drive the **due tracking** in §5.

---

## 2. The spatial engine — relationships from the geometry

This is the new capability. VoltEdge's connectivity and relationships are **authored** (you wire them). The spatial engine instead **derives** a relationship from the shapes, across every compiled `gis.*` table, so a pole never has to be *linked* to an easement for the question "is it on one?" to have an answer.

`GET /spatial/related?feature_class=&feature_id=&mode=&distance=&classes=&limit=` returns the features spatially related to one reference feature, in one of four **modes**:

| Mode | Predicate | Use |
|---|---|---|
| **intersects** (default) | `ST_Intersects` (symmetric) | anything whose shape overlaps the reference |
| **contains** | reference `ST_Contains` target | the assets **inside** an easement/corridor/parcel |
| **contained_by** | target `ST_Contains` reference | the land **covering** an asset (its easements/parcels) |
| **within** | `ST_DWithin` on `::geography` | features **within N metres** (geodesic), nearest first with distances |

`classes` narrows which classes to search (default: every geometric class); `limit` caps the result (default 500).

> ∑ **Safe by construction.** Class codes in a request resolve to their physical table through metadata (a whitelist), feature ids are bound parameters, and the reference geometry is fetched by a subquery — a table name is never taken from the request and the geometry never round-trips through the app.

> **How to — ▶** Open any feature → the **Land use** tab → pick **Overlapping / Inside this / Covering this / Nearby** (set a distance for Nearby). Results group by class; click a row to open that feature, hover to highlight it on the map.

---

## 3. Vegetation clearance — the trim list

The reliability (and wildfire) workhorse: find the vegetation **too close** to a conductor — within the clearance that conductor's **voltage** requires.

`GET /spatial/vegetation-encroachments?feature_class=&feature_id=` (one conductor) **or** `?circuit_id=` (a whole feeder). For each conductor it joins `VEGETATION_ZONE` within the widest possible clearance (geodesic `ST_DWithin`), reads the conductor's `voltage_class`, and keeps only the vegetation inside **that** conductor's clearance.

The clearance rule (`encroachment.go`):

| Voltage | Default clearance | |
|---|---|---|
| 400 V | 1.0 m | `&clearance=` overrides all of these with a uniform value; |
| 11 kV | 2.5 m | an unknown voltage falls back to **2.0 m**. |
| 33 kV | 3.0 m | ⚠️ These values are **illustrative** — tune |
| 132 kV | 4.5 m | `defaultClearanceByVoltage` to your standard. |

Each breach reports the conductor, the vegetation (species/height/type), the **actual distance**, the **required clearance**, and the **breach** (how much too close) — worst breach first.

> **How to — ▶** Select a conductor → the Land use tab's **Vegetation clearance** section lists the breaches. Toggle **This conductor / Whole feeder** to widen the scan to the conductor's circuit.

---

## 4. Dispatch — raise a trim order

One click closes detect→dispatch. `POST /spatial/vegetation-encroachments/work-order` (🔒 **wam:write**, escalated in-handler) raises a **preventive WAM work order** whose asset is the vegetation zone (what gets cut); the conductor and the measured breach go in the title/description, and the **priority is derived** from the breach: near-contact (≤ 0.5 m) is **urgent**, a big breach (≥ 1 m, or well under 40 % of the gap) is **high**, else **medium**.

> **How to — ▶** On a breach row, click **Raise trim order** (needs wam:write). It lands on the **WAM work board** like any other order, and the row shows **✓ order raised**.

The order flows through the normal WAM lifecycle (see **Works, Inspections & Maintenance**).

---

## 5. Due tracking — stay ahead

The proactive side: dates, not shapes.

- **Easement expiry** — `GET /spatial/easements/expiring?within_days=` (default 90): every EASEMENT whose `expiry_date` is within the window, **already-expired ones included** (a negative `days_until_expiry`), soonest first. The wayleave-renewal report.
- **Trim due** — `GET /spatial/vegetation/trim-due?within_days=` (default 30): every VEGETATION_ZONE whose next trim (`last_trimmed + trim_cycle_months`) falls within the window, **most overdue first**. The vegetation-management schedule.

Both **degrade to an empty list** when the class isn't seeded/compiled, so a deployment without the land classes isn't an error.

> **How to — ▶** Open an EASEMENT or a VEGETATION_ZONE → the Land use tab shows a badge: *"Easement expires in 40 days"* / *"expired 12 days ago"*; *"Trim overdue by 25 days"* / *"due in 10 days"* (red / amber / muted), computed from the feature's own dates.

---

## 6. Who may do what

| Action | Permission |
|---|---|
| Run spatial queries, clearance scans, due reports | `features:read` |
| Draw / edit the land classes | `features:write` |
| Raise a trim work order | `wam:write` (on top of features:read) |

The `/spatial` mount is read-gated on `features:read`; the one write (the trim order) escalates to `wam:write` in-handler.

---

## 7. Endpoints & agent tools

| Verb & path | Tool |
|---|---|
| `GET /spatial/related` | `spatial_related` |
| `GET /spatial/vegetation-encroachments` | `vegetation_encroachments` |
| `POST /spatial/vegetation-encroachments/work-order` | `raise_vegetation_trim_order` |
| `GET /spatial/easements/expiring` | `easements_expiring` |
| `GET /spatial/vegetation/trim-due` | `vegetation_trim_due` |

---

## 8. Honest limits

- ⚠️ **The land classes seed `draft`.** Apply `seed/land_spatial_example.sql`, `task compile` each, then **publish** them from /admin before they're usable.
- ⚠️ **The per-voltage clearance values are illustrative.** Tune `defaultClearanceByVoltage` to your regulatory standard, or pass a uniform `clearance` per request.
- ⚠️ **No standalone due-report panel yet.** The per-feature badge ships in the UI; the network-wide expiry/trim-due lists are reachable via the API and the agent tools (the hooks exist for a future panel).
- ⚠️ **No breach map overlay yet.** The clearance list is in the panel (per conductor or per feeder); drawing the breaches on the map is a follow-up.
- ⚠️ **A whole-network spatial scan is not offered** — the `related` query takes one reference feature, the clearance scan a conductor or a circuit. Scope keeps it fast.
- ⚠️ Like the other operational modules, the land records are **not territory-scoped**.

---

## Related guides

- **[Works, Inspections & Maintenance](works-inspections-maintenance-guide.md)** — the work board a trim order lands on, and the inspection/defect lifecycle around it.
- **[Switching & Protection](switching-and-protection-guide.md)** — the conductors and their `voltage_class`, which the clearance rule reads.
- **[Feature Catalog](feature-catalog.md)** — the per-class reference, including these land classes once seeded.
