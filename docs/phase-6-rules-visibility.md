# Phase 6 — rules visibility for a selected feature

**Goal:** answer "what network rules, relationship rules, and snapping
rules apply to this feature's class" from the feature panel, without
opening a terminal or reading SQL. The third of three admin gaps
surfaced by the same walkthrough that produced field-setting editing
(`docs/phase-6-admin-metadata.md`'s "Editing an existing field's
settings" section) and domain-value editing (its "Editing and deleting
domain values" section) — scoped into its own slice because, unlike
those two, this one isn't an admin *write* capability at all. It's a
read-only view for anyone using the map, and it touches code outside
`api/internal/admin` entirely.

**Status:** backend curl-verified end to end against a real Postgres
(including a from-scratch `snapping_rules` seed, since that table had
never held a single row anywhere in this project before this slice).
Frontend Playwright-verified against the same stack, a real Martin
tileserver, and `npm run dev`.

## The gap, and why it existed

Three tables have always enforced legality only at write time, never
let anyone browse what they contain:

- `metadata.network_rules` — `api/internal/connectivity` loads exactly
  one from/to pair right before validating a proposed connection
  (`metadata.LoadNetworkRules`).
- `metadata.relationship_classes` — `api/internal/relationship` does
  the same for a proposed relationship (`metadata.LoadRelationshipClasses`).
- `metadata.snapping_rules` — worse off still: no Go model existed for
  it at all before this slice, just the SQL table from Phase 1. The
  frontend's own snap toggle (`snap-store.ts`/`snap-toggle-button.tsx`)
  is a client-side on/off switch, not driven by this table's own
  per-class-pair tolerances — the table has sat empty and unread since
  the day it was created.

The feature panel's existing Terminals/Relationships tabs only ever
show what's actually been *established* for one specific feature
instance — real connections, real relationships. Nothing anywhere
answered "what's *allowed*" in the abstract, for a class as a whole.

## Backend

- **`api/internal/metadata/models.go`** — new `SnappingRule` struct
  mirroring `metadata.snapping_rules` (migrations/0013), the third
  rule-shaped model alongside `NetworkRule`/`RelationshipClass`.
- **`api/internal/metadata/load.go`** — three new class-scoped loaders,
  each the browsing counterpart to an existing pair-scoped one:
  `LoadNetworkRulesForClass`, `LoadRelationshipClassesForClass`,
  `LoadSnappingRulesForClass`. Each does `WHERE from_x = ? OR to_x = ?`
  (`origin`/`destination` for relationships) instead of requiring both
  sides — the existing pair-scoped loaders exist to validate one
  specific proposed write; these exist to answer "everywhere this
  class appears" for display.
- **`api/internal/rules`** (new package) — `Handler(db) http.HandlerFunc`
  serving `GET /feature-classes/{code}/rules`. Loads all three rule
  sets for the named class, then batch-resolves every referenced
  feature class/subtype/terminal/domain-value id into its readable
  code/name in one pass (a `resolver` type, the same
  fetch-everything-once trick `api/internal/admin/list.go`'s
  `countByFeatureClass` already uses, just across four lookup tables
  instead of one) rather than a query per rule. Each rule in the
  response carries a `direction` field ("outgoing"/"incoming" for
  network rules, "origin"/"destination" for relationships, "from"/"to"
  for snapping) computed relative to the requested class, so the
  frontend doesn't have to re-derive which side of an asymmetric row
  the class is on.
- **`api/cmd/server/main.go`** — one new route,
  `GET /feature-classes/{code}/rules`, next to the existing
  `/schema` route it's structurally similar to (a read/resolve
  endpoint, no write path).

This is read-only in every sense: no admin endpoint writes to
`snapping_rules` either, matching how `network_rules` and
`relationship_classes` have never had one — all three are still
authored by hand (`seed/*.sql`/`migrations/*.sql`), same as when this
project started.

## Frontend

- **`web/src/hooks/use-feature-class-rules.ts`** (new) — wraps
  `GET /feature-classes/{code}/rules`, cached by `featureClassCode`
  alone (not per-feature-instance) since every feature of the same
  class gets the same answer — unlike `useConnections`/
  `useRelationships`, which are keyed on a specific feature id because
  they read what that one instance actually has.
- **`web/src/components/map/feature-panel.tsx`** — a fourth tab,
  "Rules" (`ListChecks` icon), alongside Attributes/Terminals/
  Relationships. `RulesTab` renders three sections (Network rules,
  Relationship rules, Snapping rules), each only shown when non-empty,
  with a per-row direction badge and the resolved counterpart class/
  subtype/terminal. Locks the same way Terminals/Relationships already
  do while mid-edit on Attributes (the existing `locked` check is
  `tab.id !== "attributes"`, so a new tab id needed no changes there).

## How this was validated

Backend, against the local, non-Docker Postgres 16 + PostGIS install
this session already had running:

1. `go build ./... && go vet ./... && gofmt -l .` clean.
2. `GET /feature-classes/MV_CONDUCTOR/rules` against real seed data —
   confirmed six real `network_rules` rows resolve with the correct
   `direction`, terminal codes, and `phase_rule`/`is_valid` flags
   (including a genuinely disabled rule); two real
   `relationship_classes` rows resolve with `kind`/`cardinality`.
3. Confirmed the same request from `DIST_XFMR`'s side shows the exact
   same rows with `direction` correctly flipped.
4. `snapping_rules` had zero rows anywhere in this project before this
   slice — seeded a real one by hand (a `SNAP_TARGET_TYPE` coded domain
   via the admin API, then one `snapping_rules` row via `psql`, the
   same way every other seed table in this project has been authored)
   to prove the resolution path actually works, not just that it
   compiles against an empty table. Confirmed it resolves correctly
   from both the `from`-side and `to`-side class, then removed the
   scratch data.
5. Confirmed an unknown feature class code 404s.

Frontend: `npx tsc --noEmit` and `npm run lint` (oxlint) both clean.
Playwright-verified against the same stack plus a real, non-Docker
Martin binary: searched for a real `MV_CONDUCTOR` feature by name,
selected it, opened the new Rules tab, and confirmed all three
sections rendered with the correct resolved data (matching the curl
output above exactly) with zero browser console errors from the
application itself.

## Try it yourself

```bash
# Every network/relationship/snapping rule where MV_CONDUCTOR appears
# on either side, resolved to readable class/subtype/terminal codes
curl http://localhost:3704/feature-classes/MV_CONDUCTOR/rules
```

Then in the browser: select any feature on the map (click it, or use
the "Search by name…" box), and open the "Rules" tab in its panel.

## What's still out of scope

- **Editing any of these three rule tables** — still fully unsupported,
  same as before this slice. This is a read path only; `network_rules`,
  `relationship_classes`, and `snapping_rules` are all still
  hand-authored via `seed/*.sql`/`migrations/*.sql`.
- **Subtype/terminal-level filtering in the UI** — the response already
  carries `from_subtype`/`to_subtype`/`to_terminal` when a rule is
  scoped that specifically, and the Rules tab displays them, but there's
  no separate "rules for subtype X only" view — every rule for the
  class is shown together regardless of subtype scoping.
