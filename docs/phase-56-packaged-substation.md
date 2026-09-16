# Phase 56 — Packaged (movable) substation

**Requested 2026-09-14** ("a movable substation that has everything — could be permanent or temporary"). This is the open "Point 2 (packaged/container substations)" from Phase 45. A packaged substation (aka unit / compact / container / kiosk / skid / mobile secondary substation) is a factory-built enclosure that bundles, pre-wired, an MV switch + a distribution transformer + an LV board, and is placed and moved as ONE physical unit.

## Decisions (AskUserQuestion 2026-09-14)
- **Full RMU-style fan-out** — placing one point auto-creates and wires the whole internal kit in one transaction ("has everything" out of the box), NOT an empty container the crew fills by hand.
- **Permanent vs temporary/mobile = subtypes** (not a flat field), so the two can diverge in required-field rules / templates later.

## Why a NEW class shaped like RMU, not a SUBSTATION subtype
The reconnaissance (see below) established:
- **Container mechanics are fully generic.** Any class with `network_role='container'` + seeded `containment` relationship_classes works with the runtime relationship engine AND the CIM exporter automatically (contained equipment gets `Equipment.EquipmentContainer` → the container). No new plumbing.
- **RMU is a ready-made blueprint** (`api/internal/rmu` + `metadata.rmu_templates`, migration 0073): place one point → fan out the internal kit + wire containment + wire internal connectivity, all in one rollback-safe tx, driven entirely by a per-`(class, config)` template table. Nothing hardcodes a class code / terminal.
- **SUBSTATION is the wrong parent.** It is a drawn **polygon** site boundary with `network_role='none'` (CIM `Substation`). A thing that "moves as one unit" is a point-placed live network object; a SUBSTATION subtype would inherit polygon geometry + the non-network role, fighting the requirement.
- **Movability/tenure is net-new.** The only lifecycle axis is `operational_state` (PLANNED→IN_SERVICE→ISOLATED→RETIRED, already on every physical asset incl. SUBSTATION); it covers installed/removed but not permanent-vs-temporary. That distinction becomes the **subtype**.

So `PACKAGED_SUBSTATION` is a **point / `container` / MV** class shaped exactly like RMU, with its own fan-out endpoint.

## The one structural difference from RMU: chain, not star
RMU fans out a **star** — N switched ways all wired to ONE internal bus (`rmu_template_items.bus_terminal` → the single bus). A packaged substation is a **series chain**: MV switch → transformer → LV board. So it needs a chain-capable template: each item wires to the *previous* item, not to a shared bus. New template tables (migration 0092) generalize the RMU template to an ordered chain.

Internal chain (default config `COMPACT`):
```
(external MV cable) → MV_SWITCH.T1
                      MV_SWITCH.T2  ─┐
                                     ├─ leg 1: MV_SWITCH.T2 ↔ DIST_XFMR.T1 (HV/primary)
                      DIST_XFMR.T1 ─┘
                      DIST_XFMR.T2  ─┐  (LV/secondary)
                                     ├─ leg 2: DIST_XFMR.T2 ↔ LV_BUSBAR.T1 (incoming)
                      LV_BUSBAR.T1 ─┘
                      LV_BUSBAR.T2  → (external LV feeders)
```
Terminal facts confirmed pre-design: DIST_XFMR = T1 primary / T2 secondary / T3 ground; LV_BUSBAR = T1 incoming / T2 bar; MV_SWITCH = T1 cable / T2 bus. Neither internal leg has an existing `network_rule`, so the seed adds `MV_SWITCH T2 → DIST_XFMR T1` and `DIST_XFMR T2 → LV_BUSBAR T1` (both `phase_rule` NULL, like the RMU internal leg, so no voltage-field match is required at wiring time).

## Slice plan

- **S1 — Metadata (class + subtypes + fields + containment + template tables + seed).**
  - Migration `0092_create_packaged_substation_templates.sql`: `metadata.packaged_substation_templates` (header, keyed `(packaged_feature_class_id, config_value_id)`, names nothing extra — the chain lives in items) + `metadata.packaged_substation_items` (ordered chain: `component_feature_class_id`, `subtype_code`, `attributes` jsonb, `own_terminal` nullable, `upstream_terminal` nullable, `position`, `offset_x/y/rotation`). Generalizes RMU's `bus_terminal` to a two-terminal per-item link (`upstream_terminal` on the previous item ↔ `own_terminal` on this item; NULL = terminal-less bus-style convergence).
  - Seed `packaged_substation_example.sql` (task `seed-packaged-substation`): `PACKAGED_SUBSTATION` class (point / 4326 / `container` / MV / `is_diagram_container`), subtypes `permanent` + `mobile`, `package_config` field (domain `PACKAGED_SUB_CONFIG`, value `COMPACT`, required), containment classes (`PACKAGED_SUBSTATION contains MV_SWITCH / DIST_XFMR / LV_BUSBAR`, all one_to_many), the two internal `network_rules` above, and the CIM mapping row (`→ Substation`, `container` — it IS a self-contained mini-substation). `operational_state` auto-applies via its own seed. Depends on mv_lv_switch / connectivity (DIST_XFMR) / lv_metering (LV_BUSBAR) seeds + `task compile CODE=PACKAGED_SUBSTATION`.
  - Seed `packaged_substation_templates.sql` (task `seed-packaged-substation-templates`): the `COMPACT` chain — MV_SWITCH (pos 1) → DIST_XFMR (pos 2, upstream T2 / own T1) → LV_BUSBAR (pos 3, upstream T2 / own T1), each with default attributes so the fanned-out feature passes create validation.

- **S2 — Fan-out endpoint (`api/internal/packagedsub`).** Copy the RMU trio: `http.go` (`GET /options`, `POST /` gated features:write + relationships:commit + connectivity:commit), `fanout.go` (`CreatePackagedSubstation`: RunInTx → `crud.CreateFeature` the enclosure → loop chain items, create each via `crud.CreateFeature`, `relationship.EstablishRelationship` containment, and for position>0 `connectivity.EstablishConnection` prev.`upstream_terminal` ↔ this.`own_terminal`), `options.go` (`ListPackagedSubOptions` = templates × tenure subtypes). New `metadata.LoadPackagedSubTemplate` in `metadata/load.go` (+ types in `models.go`), the chain analogue of `LoadRMUTemplate`. Mounted in `api/cmd/server/main.go`.

- **S3 — Frontend.** Mirror the RMU assembly flow: `use-packaged-substations.ts` (options hook + POST), `draw-store` `startPackagedSubstationDrawing(class, config, subtype)`, a "Packaged substation" section in `new-assembly-menu.tsx` (one item per config × tenure subtype), and the map draw + Save→POST wiring in `voltedge-map.tsx`.

## Owed verification (Bash blocked here — user runs)
`cd api && go build ./...`, `cd web && npm run build`; apply migration 0092 + the two seeds via psql; `task compile CODE=PACKAGED_SUBSTATION`; then place a packaged substation through the Assemblies menu and confirm the enclosure + MV_SWITCH + DIST_XFMR + LV_BUSBAR are created, contained, and wired (a downstream trace from the MV cable should walk switch → transformer → LV board; an OPEN MV switch should halt it). CIM export spot-check: the packaged substation emits as a `Substation` container with its three contained pieces referencing it.
