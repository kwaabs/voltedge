# VoltEdge Data Import Guide — bringing data in from other systems

**Who this is for.** Two audiences, two halves:

- **Administrators** author reusable **import profiles** — the saved recipe that says "a file (or table) shaped *like this* maps onto *this* feature class *that* way." Authoring a profile needs **`metadata:write`**.
- **Operators** run those profiles from the **Import data** page: pick a profile, point it at a source, pre-flight it, and start the load. Running an import needs **`features:write`** (it creates/updates features).

You don't need both roles. An admin sets a profile up once; any operator can then run it against new files without touching the mapping.

> **What it is.** A production ETL path that reads from files *or* live databases, validates every row against the **same rules a hand-entered feature obeys**, and writes through VoltEdge's normal create/update engine — so imported features get territory assignment, write-scope enforcement, and full history exactly like features drawn on the map. There is no "import shortcut" that bypasses validation.

---

## 1. The big picture — three moving parts

```
   ADMIN                         OPERATOR                        BACKGROUND
   ─────                         ────────                        ──────────
  author a PROFILE   ──────▶   pick profile + source   ──────▶   import WORKER
  (map columns →               → dry-run (validate)              streams every row,
   feature class)              → start the run                   writes via the
   metadata:write              features:write                    normal CRUD engine
```

1. **Profile (admin, once).** The reusable mapping: target feature class, source format, how to match rows, how geometry is built, and one row per *source column → target field*.
2. **Run (operator, each time).** Choose the profile, upload a file (or, for a live database, nothing to upload), **dry-run** to catch problems, then **enqueue** the job.
3. **Worker (automatic).** A separate background process (`importworker`) claims the job and applies every row — one transaction per row — so the page you started it from doesn't have to stay open.

---

## 2. What you can import from

Nine source formats, in three families. The family decides *what you hand VoltEdge*.

| Family | Formats | You supply | Notes |
|---|---|---|---|
| **Uploaded file (parsed in your browser)** | `csv`, `geojson`, `shapefile`, `kml`, `dxf` | a **file** | The browser converts it to a line-per-feature stream before upload. A shapefile is uploaded as its **`.zip`**; KML may be **`.kmz`**. A **CSV** arrives attribute-only — its geometry is built from lon/lat or WKT columns you nominate on the profile. |
| **Uploaded database file (read on the server)** | `geopackage` (`.gpkg`), `spatialite` (`.sqlite`) | a **file** + (optionally) a **layer name** | These hold *multiple* layers. If the file has exactly one feature layer it's used automatically; otherwise pick the layer. |
| **Live database connection (no file)** | `postgis`, `mssql` | **connection details** on the profile | VoltEdge opens a **read-only** outbound connection and streams a chosen table, or a read-only `SELECT` you provide. |

> **Not supported:** Esri File Geodatabase (FGDB) — it needs a native GDAL dependency and is deliberately deferred. Everything else in the table above is live.

---

## 3. For administrators — authoring an import profile

**Where:** Admin navigation → **Data import** (needs `metadata:read` to see it, `metadata:write` to edit). This is the mapping editor; it's separate from the operator **Import data** page.

### 3.1 Create the profile

A profile is keyed by a short **code** and targets **one feature class** (optionally pinned to **one subtype**). Set:

- **Source format** — one of the nine in §2.
- **Write mode** —
  - **`insert_only`** — every row becomes a new feature.
  - **`upsert`** — match each row against existing features on your **match fields**; update a single match, insert when there's none.
- **Match fields** (upsert only) — the field(s) that identify "the same feature" (e.g. an external asset number). Every match field must carry a value on each row, or the row errors.
- **Geometry mode** —
  - **`none`** — attribute-only (no geometry).
  - **`native`** — the source's own geometry (GeoPackage/SpatiaLite/PostGIS/MSSQL blobs, or inline GeoJSON).
  - **`lonlat`** — a Point built from two numeric columns you name.
  - **`wkt`** — geometry parsed from a WKT text column.
- **Connection** (live-DB formats only) — host, port (PostGIS defaults 5432, MSSQL 1433), database, user, a **write-only password** (sealed server-side; never shown back — the editor only reports whether a credential is stored), SSL mode (`disable` / `require` / `verify-full`, default `require`), and either a source **table** or a custom read-only **query**.

> **Re-targeting a profile's class is not allowed** — it would orphan the field mappings. To point a profile at a different class, delete it and create a new one.

For a live-DB profile, use **Test connection** to confirm the credentials work, then the **table picker** to choose the source table.

### 3.2 Map the columns

Add **field mappings**, one per *source column → target field*. Each mapping carries a **transform**:

| Transform | What it does |
|---|---|
| **`direct`** | Copy the source cell as-is. |
| **`constant`** | Write a fixed literal (no source column) — e.g. stamp every row `status = "in_service"`. |
| **`scale`** | Multiply a numeric source value by a factor — e.g. metres → millimetres. |
| **`domain_map`** | Translate source values via a lookup table (`{"OH":"overhead","UG":"underground"}`). **A source value with no entry in the map is a per-row error** — so a stray code is caught, not silently dropped. |

Notes that save you time:
- **There is no auto-map.** You create each mapping explicitly — which is also what makes the profile reusable and auditable.
- **Subtype is pinned on the profile**, not mapped per column.
- **Geometry is not a field mapping** — it's the profile's geometry mode + columns (§3.1).
- A mapping whose target field was later removed/deprecated is **silently dropped** (the import doesn't break when the model changes).
- **Blank means "no value."** A blank source cell leaves the target field unset rather than writing an empty string.
- Domain membership, subtype validity, number ranges and geometry type are **not** checked at map time — they're enforced per row at dry-run and load (§5), by the very same validator a hand-created feature uses.

---

## 4. For operators — running an import

**Where:** sidebar → **Import data** (`/imports`, needs `features:write`).

### 4.1 The steps

1. **Pick a profile.** Only active profiles you're allowed to run appear.
2. **Point it at the source.**
   - *File formats:* choose the file. Browser-parsed formats (`csv/geojson/shapefile/kml/dxf`) show a short **preview**; GeoPackage/SpatiaLite upload the raw file. For a multi-layer file, pick the **layer**.
   - *Live DB:* nothing to upload — the profile already carries the connection.
3. **Dry-run (pre-flight).** Validate a **sample** of rows *without writing anything* (§5.1). Read the per-row report, fix the profile or the source, and dry-run again until it's clean.
4. **Start the run.** Enqueue the job. You get a **job id** back immediately — the load runs in the background.
5. **Watch it.** The job list shows your recent runs; open one to see live **tallies** (total / inserted / updated / skipped / errored) and a **per-row error report**. Terminal states are **succeeded**, **failed**, **cancelled**.

> **Your territory scope is captured when you start the run** and applied by the worker — so an import writes only where *you* are allowed to write, even though a background process does the work.

---

## 5. What actually happens to each row

### 5.1 Dry-run — validation without writes

The dry-run runs each sampled row through the **real** pipeline — apply the mappings, assemble the geometry, then `ValidateFeature` — the identical in-memory checks a normal create runs: required fields present, coded-domain values legal, numbers in range, geometry of the right type. For upsert it also confirms every match field has a value. It is deliberately **create-shaped**: it tells you whether each row *would validate*, not whether it matches an existing feature. The report gives you, per row: valid/invalid, the error if any, the resolved attributes, and whether it has geometry — plus overall valid/invalid counts and the resolved target class, subtype, write mode, geometry mode and match fields.

### 5.2 The committed load

The worker streams **every** row (no file-size cap) and, per row:

1. builds attributes (mappings + type coercion),
2. assembles + reprojects geometry,
3. calls the normal **`CreateFeature`** (insert) or **`UpdateFeature`** (upsert match),

each in **its own transaction**. This matters: **the load is *not* all-or-nothing.** A bad row rolls back only itself and the stream keeps going; good rows still land. Every failed row is recorded with its row number and the reason, visible on the job status.

Because imports go through the normal CRUD engine, each written feature gets:
- **territory** auto-assignment (for features with geometry) and **Phase-58 write-scope** enforcement against your snapshotted scope,
- a full **feature history** create/update record,
- `created_by` / `updated_by` stamping.

(It does **not** auto-build connectivity — connectivity is a separate step/engine. An import brings the features in; wiring them is done afterward, on the map or via the connectivity tools.)

### 5.3 Upsert matching

For `upsert`, each row is matched by `SELECT … WHERE <each match field> = <value> AND not deleted`:
- **no match** → insert,
- **exactly one** → update,
- **more than one** → **ambiguous: the row is skipped and reported** (never applied to a guess).

### 5.4 Re-running is safe

Insert ids are **deterministic** (derived from the job id + row number), and creates use `ON CONFLICT (id) DO NOTHING`. So re-running (or a worker reclaiming an interrupted job) is a **no-op for already-inserted rows** rather than a duplicate; upsert rows simply re-match and update. If a worker loses its lease mid-stream the job is safely reclaimed by another.

---

## 6. Geometry & reprojection

Whatever the source's coordinate system, geometry is **reprojected to the target feature class's SRID** (usually EPSG:4326) on the way in:

- **`native`** blobs (GeoPackage GPB, SpatiaLite, WKB, PostGIS/MSSQL geometry) are decoded per row and transformed to the class SRID.
- **`lonlat`** builds a Point from two numeric columns.
- **`wkt`** parses a WKT string.
- MSSQL **geography** columns get their lat/lon axis order corrected automatically.

So you don't pre-project your data — nominate the geometry mode and VoltEdge lands it in the right frame.

---

## 7. Safety, limits & the honest edges

- **Per-row transactions, not all-or-nothing** (§5.2) — partial success is normal and visible; there's no single rollback of a whole file.
- **Live-DB connections are hardened.** Outbound connections are **read-only** (PostGIS is opened `read_only` with statement/idle timeouts; custom queries must be a single `SELECT` with no writes or DDL), and host resolution is **SSRF-guarded** — private/loopback/link-local/cloud-metadata ranges are blocked unless explicitly allow-listed, and the validated IP is dialled to defeat DNS-rebinding. Passwords are sealed server-side and never returned. (MSSQL has no session read-only switch, so use a **read-only login** for it.)
- **Error reporting** — per-row errors are capped at 500 on the status view; the tallies always reflect the whole run. A *malformed stream row* (which desyncs parsing) fails the whole job; so do job-level problems (missing profile, an uncompiled target class, an unreadable upload).
- **No auto-map, no class re-target** (§3.2, §3.1) — both deliberate, for reusable auditable profiles.
- **Ambiguous upsert rows are skipped, not guessed** (§5.3).
- **FGDB is not supported** (§2).

---

## 8. Permissions at a glance

| Action | Permission |
|---|---|
| See the **Data import** admin editor | `metadata:read` |
| Create / edit / delete an import **profile** and its mappings | `metadata:write` |
| Test a live-DB profile's connection / list its tables | `metadata:write` |
| See the **Import data** run page and your job history | `features:read` |
| Upload a source, dry-run, and **start a run** | `features:write` |

---

## 9. Related

- **Data Dictionary** — every feature class and field, so you know the target names your mappings resolve against.
- **Everyday Operations** — creating and editing features by hand (the same validation path an import uses).
- **Access Control** — territories and write-scope, which govern where an import may write.
- `docs/etl-import-engine.md` — the engineering/design reference (data model, migrations, architecture) behind this guide.
