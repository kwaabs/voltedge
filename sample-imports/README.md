# Sample import files (ETL import engine v1)

Test fixtures for the file-based import engine (S1–S5). All four files describe
the **same 5 service points around Accra**, so you can run the same logical
import through every source format and geometry mode.

They target the **`SERVICE_POINT`** feature class on purpose: it's a plain point
class (4326) whose only attribute is `customers_served` (integer, nullable) and
which has no required coded fields to trip over — the cleanest possible target.

> Prereq: `SERVICE_POINT` must be seeded **and compiled** in your dev DB:
> `psql -f seed/service_point_example.sql` (after its deps) then
> `task compile CODE=SERVICE_POINT`. If the class isn't compiled the wizard's
> profile picker won't offer it.

## The deliberate twist: the source column is *not* the field name

Every file calls the customer count **`num_customers`**, but the target field is
**`customers_served`**. That's intentional — it exercises the column→field
mapping instead of an identity pass-through. There's also a **`site_ref`** column
that maps to *nothing* on `SERVICE_POINT`; leave it unmapped to confirm unmapped
source columns are simply ignored (it's the kind of column you'd later use as an
upsert match key on a class that has a business-key field).

## Files & the geometry mode each one exercises

| File | Format | Geometry mode to use | Notes |
|------|--------|----------------------|-------|
| `service_points.csv` | CSV | **lonlat** — lon col `lon`, lat col `lat` | geometry assembled server-side from two columns |
| `service_points_wkt.csv` | CSV | **wkt** — wkt col `geom_wkt` | `POINT(lon lat)`; parsed via `ST_GeomFromText` |
| `service_points.geojson` | GeoJSON | **native** | geometry comes from the file (browser normalizes to 4326 GeoJSON) |
| `service_points.kml` | KML | **native** | attributes come from `<ExtendedData>` |

## Step 1 — author an import profile (`/admin/imports`, needs `metadata:write`)

Create one profile per format you want to test (or just start with the CSV one):

- **Code / name:** e.g. `sp_csv_lonlat` / "Service points (CSV lon/lat)"
- **Feature class:** `SERVICE_POINT` — **Subtype:** `residential` (1)
- **Source format:** match the file (`csv` / `geojson` / `kml`)
- **Write mode:** `insert_only` (simplest first pass)
- **Geometry mode:** per the table above (+ fill the lon/lat or wkt column names)

Then add **one field mapping** to that profile:
- Field `customers_served` ← source column `num_customers`, transform `direct`.

(You can also try `transform: scale` with factor `1` to sanity-check the numeric
coercion path, or add nothing and import geometry-only.)

## Step 2 — run it (`/imports`, needs `features:write`)

1. Pick the profile.
2. Pick the matching file — you'll get a row/column count, an 8-row preview, and
   soft warnings if the file's format or geometry columns don't line up with the
   profile.
3. **Pre-flight (dry-run):** validates every sampled row through the real mapping
   + CRUD checks and writes nothing. Expect 5 valid / 0 invalid.
4. **Run.** Watch the live inserted/updated/skipped/error tallies. Tick
   **"validate only"** to do a full-file dry-run with no writes.

After a real run, confirm the 5 points on the **Map** (or `/features` →
SERVICE_POINT → Show on map) around Accra.

## Testing upsert (optional)

`insert_only` re-runs are idempotent (deterministic per-row IDs → `ON CONFLICT
(id) DO NOTHING`), so re-importing the same file inserts nothing new.

True **match-on-field upsert** needs a stable business-key column to match on;
`SERVICE_POINT` doesn't ship one (`site_ref` is unmapped, `customers_served` is a
poor key). To exercise upsert properly, point a profile at a class that has a
text key field and set that field as the profile's **match field** — the engine
will `SELECT` on it: 0 hits → insert, 1 → update, >1 → skipped+reported.

## Not covered here

Shapefile (`.zip`) and DXF are binary and can't be hand-written as text fixtures;
generate those from QGIS/ArcGIS if you want to exercise the `.prj`-less
source-CRS reprojection path in the wizard.
