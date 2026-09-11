# Phase 21 — field tool verification checklist

A single tick-through pass for the accumulated "code-complete, verification owed"
slices (6–18 + 14b + the flutter_secure_storage restore). Everything here needs a
real toolchain / device — Claude's environment has Bash blocked, so none of it was
run in-session. Work top-down: the build gates must pass before the device smokes
are meaningful. See `docs/phase-21-field-tool.md` for the per-slice design detail
each check maps to.

Device of record: SM-T395 (the tablet the offline render was proven on 2026-08-24).

## 1. Build / static gates

- [ ] **API:** `go build ./...` (from `api/`) — clean. Covers the shared-crud
      write path (`crud.CreateFeature`/`UpdateFeature`/`DeleteFeature`), `sync.go`,
      the fieldpack builder.
- [ ] **API:** `go vet ./...` — no new vet findings.
- [ ] **Mobile:** `flutter pub get` — resolves. **If `flutter_secure_storage: ^9.2.2`
      won't resolve against sdk ^3.12.2, bump the caret** (any version builds on AGP 8).
- [ ] **Mobile:** `flutter analyze` — no errors. Pay attention to the two files
      touched this session: `src/map/pack_style.dart` (per-rule sub-layers) and
      `src/config/settings_repository.dart` (keystore restore).
- [ ] **Mobile:** `flutter run` builds & installs — **this is the flutter_secure_storage
      build-confirmation** (a native plugin was re-added; the prior removal was a
      build issue, now believed resolved on AGP 8 / builtInKotlin=false).

## 2. Pack rebuild & versions (do once, up front)

Most map smokes require a **freshly built pack** — several features ship data in
the pack, and an old pack silently renders the pre-feature fallback.

- [ ] Rebuild a pack via `cmd/fieldsync`; confirm the build log reports
      `symbology_count > 0` (and `icon`/`relationship`/`network` counts as applicable).
- [ ] Confirm the pack schema version is the latest (**v7** per the memory note —
      symbology/icons v4/v5 + relationship_classes/network_rules v7).
- [ ] Seed, for the symbology checks in §4: at least one class with **dash** rules
      (e.g. overhead = solid vs underground = dashed) plus a **dotted** and a
      **dashdot** rule, and one rule carrying **`min_zoom`/`max_zoom`** bounds.
- [ ] Download the fresh pack to the device (a pre-v4 pack must still render — see §7).

## 3. Auth / settings (flutter_secure_storage restore)

- [ ] Paste a minted bearer token in Settings → **kill & relaunch the app → still
      signed in** (token now read from the keystore, not the JSON file).
- [ ] Sign out / clear token → relaunch → **signed out** (keystore entry deleted).
- [ ] **Migration:** on a device that already had a token from the old JSON-only
      build, upgrade to this build → **still signed in** (token migrated JSON→keystore;
      `ve_settings.json` no longer contains a `token` key).
- [ ] Base URL, snapping, basemap, network-edges toggles still persist across relaunch
      (these stayed in `ve_settings.json`).

## 4. Symbology on the map (Slices 6–8, **18**)

- [ ] **Colors (6):** features draw in their metadata colors (subtype/domain), not one
      flat per-class color; most-specific rule wins.
- [ ] **Point icons (7):** points draw as their resolved shapes; an iconless point
      still draws (circle fallback).
- [ ] **Custom SVG icons (8):** an admin-authored shape (e.g. the transformer
      double-circle) renders from the pack SVG, not a built-in circle.
- [ ] **Dash (18):** dashed / dotted / dashdot lines render per rule (solid stays
      solid). Dotted reads as round dots.
- [ ] **Zoom bounds (18):** a feature governed by a zoom-bounded rule **appears only
      within** its `min_zoom..max_zoom` and disappears outside it (accepted limitation:
      it *hides* rather than falling through to a less-specific rule).
- [ ] **Regression (18):** most-specific-wins still resolves colors correctly, and a
      class with **no** rules still draws one flat color.

## 5. Offline editing

- [ ] **Attribute edit (3a):** edit a packed feature's attributes → amber pending
      overlay + "not yet synced" chip; merged values shown in panel.
- [ ] **Create point (3d):** place-a-point flow → subtype-first form → green tappable
      overlay.
- [ ] **Create line (9):** trace a multi-vertex line (Undo/Place) → same create form
      + green overlay.
- [ ] **Create polygon (17):** trace ≥3 corners (filled ring + closing-outline preview;
      Place enabled at 3) → green **filled** overlay, tappable.
- [ ] **Vertex editor — point & line (10):** drag a vertex, insert via a midpoint,
      delete a vertex (blocked at the minimum) → Save → amber moved overlay in the new
      shape, original hidden; the filled amber `edits-moved-circle` shows for a moved point.
- [ ] **Vertex editor — polygon (17):** same, including a midpoint on the **ring-closing**
      edge and delete blocked at 3 corners; edit a **packed** polygon too.
- [ ] **Regression (18 filter compose):** while a feature is being moved/edited, its
      original stays hidden under the overlay on a symbology class that now has per-rule
      sub-layers (the `_applyHidden` compose fix).
- [ ] **Delete (15):** delete a **packed** feature → vanishes + red "Deleted 1" +
      pending delete row; Undo restores; an offline **create** shows **Discard** (not Delete).

## 6. Connectivity, relationships, network overlay

- [ ] **Connectivity gating (19):** select a **POLE** (`network_role = 'none'`) → **no**
      Terminals tab (web) / **no** Connect (hub) action (mobile); Relationships/Relate still
      shown. Select an **edge** or **junction** → the connect affordance is back. An old
      pre-column mobile pack still shows Connect (no crash).
- [ ] **Connections (13b):** establish a connection offline → green animated
      ("marching ants") edge to the neighbour; op appears in the pending sheet.
- [ ] **Relationships (14):** establish a relationship → dashed purple edge; pending op.
- [ ] **Relationship kind picker (14b):** offline kind selection + rule-guidance when
      `relationship_classes` is in the pack.
- [ ] **Network-links toggle (16 / settings):** toggling "Network links" draws/hides
      the selected feature's connectivity+relationship graph as map edges; neighbours
      still listed in the detail panel when off; your own un-synced links keep drawing.

## 7. Sync + map plumbing

- [ ] **Sync push (3e / 11):** with several pending ops (create/update/move/delete/
      connect/relate), Sync pushes them; server upserts; ops marked synced; idempotent
      re-sync is a no-op. Batching: an outbox > 200 ops splits into ≤200-op batches,
      acked per batch (durable partial progress).
- [ ] **Basemap switch:** changing basemap hot-swaps the style **without re-framing**
      the camera; overlays + symbology re-apply; offline "none" still renders pack-only.
- [ ] **Snapping (settings):** with snapping on, a placed/dragged/inserted coordinate
      snaps onto a nearby existing vertex; off reports the raw coordinate.
- [ ] **GPS placement (5b):** place a feature at the current fix; recenter-on-me works.
- [ ] **Backward-compat:** an **old (pre-v4) pack** still opens and renders (placeholder
      per-class colors, no dash/zoom/icons) — no crash.

## Notes / known limitations (not bugs — don't file)

- Per-**subtype** point zoom isn't honored (only the class-default rule's zoom bounds
  apply to a point layer) — MapLibre can't gate a data-driven `case` on zoom.
- A zoom-gated line/polygon rule **hides** its features at that zoom rather than
  falling through to a less-specific rule — MapLibre can't gate a filter on zoom.
- Delete tombstones aren't auto-pruned after a fresh-pack sync (no positive
  confirmed-deleted signal); cleaned when the pack dir is deleted.
