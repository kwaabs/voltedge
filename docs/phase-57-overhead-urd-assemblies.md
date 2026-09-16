# Phase 57 — Overhead & URD assemblies (a general kit engine)

## What & why

Two Tier-1 guided assemblies (see the assembly backlog):

1. **Pole-mounted overhead distribution transformer** — place one `POLE` and get its
   whole pole-top kit: an `MV_FUSE` drop-out cutout, an `MV_ARRESTER` (shunt),
   a pole-mounted `DIST_XFMR`, and an `LV_BUSBAR` board on the secondary — all
   spatially supported by the pole and internally wired.
2. **Pad-mounted transformer + link box (URD)** — place one pad-mounted `DIST_XFMR`
   and get its `LV_LINK_BOX` feeder pillar wired to the secondary.

Decisions (2026-09-14, AskUserQuestion):
- Pole-mount kit = **full** (cutout + arrester + transformer + LV board).
- LV feeder pillar as its own assembly = **skipped** — `LV_LINK_BOX` (subtype
  `feeder_pillar`) already models a feeder pillar as one multi-terminal feature;
  a thin "place one" assembly would add nothing.
- URD "link box" = the **switchable LV pillar** (`LV_LINK_BOX`), not a cable joint.

**No new feature classes.** Every part already exists (`POLE`, `DIST_XFMR`,
`MV_FUSE`, `MV_ARRESTER`, `LV_BUSBAR`, `LV_LINK_BOX`). What's new is relationship
classes, a couple of internal network legs, and the template recipes.

## Why a NEW general engine, not a third packagedsub

RMU is a **star** (N ways → one shared bus). packagedsub is a **chain** (wire each
item to the *previous*). The pole-mount is neither: it **branches** — the arrester
taps the transformer's primary node and the LV board hangs off the secondary, so
two items both wire back to the transformer, not to "the previous item." And the
pole holds the kit by `spatial_support` (the pole is `network_role='none'`, NOT a
container), not by containment.

So rather than write a third near-identical package, Phase 57 introduces ONE
**general kit-assembly engine** (`api/internal/assembly`, `POST /assemblies`) whose
template lets each item name, independently:
- its **structural** parent + relationship **kind** (`support_kind`,
  `support_parent_position`) — `spatial_support` for the pole kit, NULL for the
  URD pillar (pure electrical adjacency);
- its **electrical** target by **position** (`wire_to_position`; `0` = the anchor,
  `≥1` = an earlier item), plus the two terminals (`upstream_terminal` on the
  target, `own_terminal` on this item).

Both Tier-1 assemblies — and future Tier-2/3 kits (recloser, capacitor bank,
BESS…) — become pure seed templates with **zero new Go**. RMU and packagedsub are
left untouched (no reason to risk working code); they could fold in later.

The anchor is a normal feature (not a container), so unlike RMU we do NOT remove
`POLE`/`DIST_XFMR` from plain placement — you still place ordinary poles and
transformers; this is the extra "with its kit" affordance (like coupler/tie).

## Data model (migration 0093)

`metadata.assembly_templates` — one recipe per (anchor class, code):
`anchor_feature_class_id`, `code`, `name`, `anchor_subtype_code` (nullable — when
set, forces the anchor subtype, e.g. pad-mount = `DIST_XFMR` subtype 2; NULL lets
the user pick, e.g. pole material). UNIQUE(anchor_feature_class_id, code).

`metadata.assembly_items` — one per component, `position` 1..N (anchor is
conceptual position 0): `component_feature_class_id`, `subtype_code`, `attributes`
(jsonb, supplies the component's required coded fields so the fanned-out feature
passes the same create validation), `support_kind`, `support_parent_position`,
`wire_to_position`, `own_terminal`, `upstream_terminal`, offsets.
Targets must reference position 0 or an earlier item (single forward pass).

## The two recipes

**POLE_MOUNT_STD** (anchor `POLE`, subtype user-picked):
| pos | component | subtype | support→ | wire_to | wiring |
|-----|-----------|---------|----------|---------|--------|
| 1 | MV_FUSE | 1 expulsion | pole (spatial_support) | — | (fed externally: MV line → T1) |
| 2 | DIST_XFMR | 1 pole_mounted | pole | 1 | MV_FUSE.T2 → DIST_XFMR.T1 |
| 3 | MV_ARRESTER | 1 gapless_mov | pole | 2 | DIST_XFMR.T1 → MV_ARRESTER.T1 (shunt tap) |
| 4 | LV_BUSBAR | 1 lv_distribution_board | pole | 2 | DIST_XFMR.T2 → LV_BUSBAR.T1 |

New network legs seeded: `MV_FUSE.T2 → DIST_XFMR.T1`, `DIST_XFMR.T1 → MV_ARRESTER.T1`
(`DIST_XFMR.T2 → LV_BUSBAR.T1` already ships from packagedsub — idempotent re-add).
New relationship classes: `POLE spatial_support {MV_FUSE, DIST_XFMR, MV_ARRESTER,
LV_BUSBAR}` (one_to_many; `spatial_support` kind already in the 0010 CHECK).

**PAD_MOUNT_URD** (anchor `DIST_XFMR`, `anchor_subtype_code`=2 pad_mounted):
| pos | component | subtype | support→ | wire_to | wiring |
|-----|-----------|---------|----------|---------|--------|
| 1 | LV_LINK_BOX | 1 feeder_pillar | — (none) | 0 anchor | DIST_XFMR.T2 → LV_LINK_BOX.T1 |

New network leg: `DIST_XFMR.T2 → LV_LINK_BOX.T1`.

Component default attributes cover required fields: MV_FUSE/LV_LINK_BOX
`{device_state:OPEN, condition_rating:GOOD}` (born isolated/planned, same as
packagedsub); DIST_XFMR `{primary_voltage:11KV, condition_rating:GOOD}`;
MV_ARRESTER `{condition_rating:GOOD}`; LV_BUSBAR `{phase:L1L2L3N}`. A downstream
trace walks cutout→transformer→board (pole) / transformer→pillar (URD); the OPEN
cutout/pillar halts it until commissioned.

## Endpoint contract

- `GET /assemblies/options` → `[{anchor_feature_class, anchor_feature_class_name,
  config, name, anchor_subtype?}]`
- `POST /assemblies` `{anchor_feature_class, config, subtype?, name?, geometry,
  territory_id?, attributes}` → `{anchor, components:[{feature_class, feature,
  connection_id?, support_relationship_id?, offset_x, offset_y, rotation}]}`

Mount under the method-aware features gate (`features:read`/`features:write`), POST
inner-gated `relationships:commit` + `connectivity:commit` — same layered gate as
RMU/packagedsub.

## Owed verification (Bash blocked here — user runs)

`cd api && go build ./...`; `cd web && npm run build`; apply migration 0093 +
`task seed-overhead-pole-transformer` + `task seed-pad-mount-urd` (their component
classes are already seeded/compiled); place both via the Assemblies menu and
confirm the kits are created, spatially supported (pole), and wired; a downstream
trace walks the chain and halts at the OPEN cutout/pillar. Nothing committed.
