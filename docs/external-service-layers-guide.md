# External Service Layers — overlaying data from other systems

**What this is.** Bring a *third-party* map service — an Esri ArcGIS layer, an OGC WMS/WFS, an XYZ tile set — onto the VoltEdge map as an **overlay**, sitting over your own network. Councils' cadastre, a imagery basemap provider, a neighbouring utility's WMS, your own GIS server's FeatureServer: if it speaks one of the supported protocols, it can ride on top of your features.

In the app this is called **"Service layers."** An **administrator** registers each service once (Admin → Map settings); every map user then sees it as a toggle in the **Layers panel** and can switch it on, set its opacity, and — if the admin allowed it — click it to read its attributes. VoltEdge fetches everything through its **own server-side proxy**, so the browser never talks to the third party directly and any credentials stay sealed on the server.

> **Two roles.** Registering/editing a service needs **`metadata:write`** (an administrator). *Seeing and toggling* the layers on the map needs only **`features:read`** (any map user).

---

## 1. The service types you can add

Seven protocols, split into **raster** (image tiles — you see a picture) and **vector** (real features you can click and identify):

| Type (in the admin form) | Family | What it is | You provide |
|---|---|---|---|
| **WMS** | raster | OGC Web Map Service (`GetMap`) | the GetMap base URL + the **layer name(s)** |
| **WMTS** | raster | OGC tiled WMS | the WMTS base URL + the **layer identifier** |
| **XYZ raster tiles** | raster | A `{z}/{x}/{y}` tile template | the template URL (must contain `{z}`, `{x}`, `{y}`) |
| **Esri MapServer** | raster | ArcGIS dynamic map service (`/export`) | the MapServer root URL (+ optional sub-layer ids) |
| **Vector tiles (MVT)** | vector | Mapbox Vector Tiles | a `{z}/{x}/{y}` template (+ optional style URL) |
| **Esri FeatureServer** | vector | ArcGIS feature service | the `FeatureServer/{id}` URL (+ optional layer id) |
| **WFS** | vector | OGC Web Feature Service (`GetFeature`) | the WFS base URL + the **typeName** |

Notes that save you a support call:
- **`layer_ids` is required** for WMS, WMTS and WFS — without the layer/typeName the service doesn't know *what* to draw.
- **XYZ and vector-tile URLs must contain** `{z}`/`{x}`/`{y}` placeholders.
- There is **no plain "GeoJSON URL" type and no TMS** — use WFS or Esri FeatureServer for vector-from-a-URL.
- **Esri FeatureServer** is read as GeoJSON (`f=geojson`), which needs **ArcGIS 10.4+**.

---

## 2. For administrators — register a service

**Where:** Admin → **Map settings** → the **Service layers** card (beside the basemap manager). It lists every service, enabled or not, with per-row **enable/disable**, **edit** and **delete**.

### 2.1 The fields

A service definition carries:

- **Code** — a short, stable, lowercase-kebab id (`council-cadastre`), immutable once set; it's how the layer is remembered per browser.
- **Label** + optional **description** — what users see in the Layers panel.
- **Service type** + **URL** (required), and **layer ids** (required for WMS/WMTS/WFS).
- **Rendering**: `image_format` (e.g. `png`), `version` (WMS defaults 1.3.0), `crs` (default `EPSG:3857`), optional **style URL** (vector tiles only).
- **Coverage**: `extent` ([minLon, minLat, maxLon, maxLat] in WGS84) for zoom-to, `min_zoom` / `max_zoom`, and **default opacity** (0–1).
- **`is_queryable`** — whether users can **click to identify** this layer (§4). Off by default.
- **Attribution** — one or more {label, link} credits, shown in the status bar (required by most providers' terms).
- **`sort_order`**, **`is_enabled`**.

### 2.2 Auto-fill with **Discover**

For WMS, WMTS, WFS and the two Esri types, the form has a **Discover** button. It fetches the service's `GetCapabilities` (or Esri `?f=json`), parses it, and offers dropdowns to **auto-fill** the layer id(s), version, image format, CRS/matrix set and extent — so you're picking from what the service actually offers rather than typing blind. (XYZ and vector-tile layers have nothing to discover.) Discover also *proves the service is reachable and your credentials work*, though it's an auto-fill, not a formal pass/fail "test connection."

### 2.3 Authentication & credentials

Set **auth type** to one of `none` / `api_key` / `basic` / `bearer`:
- **api_key** — a key placed in a named **header** or **query param**.
- **basic** — a username + password.
- **bearer** — a token sent as `Authorization: Bearer …`.

The **secret itself is write-only**: you type it in, VoltEdge **seals it with AES-256-GCM** server-side, and it is **never returned** — the API only ever reports `has_credential: true`. Editing is three-state: leave the field blank to keep the stored secret, clear it to remove it, type a new one to replace it.

> ⚠️ **An authenticated layer needs a server encryption key.** If the server's `secretbox` key isn't configured, storing a credential is refused with a clear error, and an auth'd layer can't be fetched. Set the key before adding any authenticated service.

> ⚠️ **An authenticated layer is always proxied** (below) — the browser can't hold a server secret. Only a *public, CORS-friendly* raster layer can be served direct (`proxied = false`); everything else routes through VoltEdge.

---

## 3. For users — turn a layer on

There's no separate "add a layer" picker: every **enabled** service already appears as a **"Service layers"** section in the map's **Layers panel**, below your own layers. For each one:

1. **Toggle it on** — a checkbox (off by default; overlays are opt-in). A Globe icon and a small type badge tell you what it is.
2. **Set opacity** — when a layer is on, an opacity slider appears beside it (handy for seeing your network *through* a raster overlay).
3. **Zoom to it** — if the admin set an extent, the layer carries its coverage.

Your choices (which overlays are on, and any opacity you dragged) are remembered **in your browser** — the same way pinned locations and hidden layers are. They're *not* synced to your account across devices, and users **can't reorder** the layers (order is the admin's `sort_order`).

---

## 4. Identify — click a third-party layer to read it

If the admin marked a service **queryable**, you can click it on the map to read its attributes — the same gesture as clicking one of your own features, but it opens a **read-only** panel (there's nothing of yours to edit on someone else's data).

- It only fires when your click **misses every VoltEdge feature** but lands on a visible, queryable service layer — your own network is always identified first.
- The panel shows the click location and groups results by layer; if several features stack under the click, a small **n / N** stepper walks them.
- **How it reads, per type:**
  - **WMS** → a server `GetFeatureInfo` round-trip.
  - **Esri MapServer** → a server `identify` round-trip.
  - **WFS / Esri FeatureServer / vector tiles** → a **client-side hit-test** of the features already drawn on screen (no server call) — so it only finds what's currently rendered in view.
- One failing layer never sinks the rest — it just shows "Could not identify…" for that group.

---

## 5. How it stays safe — the proxy & SSRF model

Every request to a third party goes through VoltEdge's **own same-origin proxy** (`/service-layers/{id}/…`), not from your browser. That does three jobs: it keeps the **upstream URL and credential server-side**, it **sidesteps CORS** (every tile is same-origin, so nothing taints the map canvas), and it lets the server **vet every outbound address.**

The outbound vetting is real SSRF hardening:
- Only **public** hosts are allowed — loopback, private ranges (RFC 1918/ULA), link-local, **CGNAT (100.64/10)** and **NAT64** are blocked, checked **after DNS resolution and on every redirect hop**, which defeats DNS-rebinding.
- Redirects are capped, and on a cross-host redirect all headers are stripped to a safe allow-list — so a custom `X-Api-Key` can't leak to an attacker-controlled host.
- A non-`none` auth type with an empty secret is an **error**, never a silent unauthenticated call.

Operational limits: the proxy has a **20-second timeout** and streams at most **32 MiB** per response (Discover caps at 8 MiB). There's no server-side response cache — VoltEdge relays the upstream `Cache-Control`, so your browser's own tile cache does the work.

---

## 6. The honest edges

Flag these before you lean on a layer:

- **CRS:** the map is EPSG:3857. Vector data is requested/parsed as WGS84 and reprojected. A service that offers **only** a non-3857/non-4326 grid is **not yet reprojected** — pick a 3857 or 4326 option in Discover where you can.
- **WMTS** uses a best-effort Web-Mercator tile grid rather than reading the service's exact matrix set — fine for standard providers, imperfect for exotic grids.
- **Vector styling** is a single flat default style for all vector features; a Mapbox-GL `style_url` is accepted but **not yet applied**, and there's no per-attribute theming.
- **Esri FeatureServer** needs **10.4+** (GeoJSON output); WFS is capped at 5,000 features, Esri-feature loads by on-screen extent.
- **Identify** only works where the admin set `is_queryable`, where the service actually supports GetFeatureInfo/identify (raster) or the features are on screen (vector), and — for a non-proxied direct layer — where the third party sends permissive CORS headers.
- **Persistence is per-browser**, not per-account; users can't reorder.
- **Credentials are useless without the server encryption key** (§2.3).

---

## 7. Permissions at a glance

| Action | Permission |
|---|---|
| See service layers in the map Layers panel; toggle / opacity | `features:read` |
| Open Admin → Map settings → Service layers | `metadata:write` |
| Create / edit / delete a service, and run **Discover** | `metadata:write` |

---

## 8. Related

- **Everyday Operations** (Chapter 11) — the **Identify** tool in the "which tool?" trio: this feature *is* the read side of a service layer.
- **Data Import Guide** — the *other* "data from elsewhere": import **copies** rows into your own feature classes (permanent, validated, yours); a service layer is a **live overlay** you never own. Use import to take ownership; use a service layer to reference someone else's live data.
- **Map Settings & Basemaps** — the admin surface these layers live beside.
