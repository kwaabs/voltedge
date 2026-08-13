# Open items log

A single, current list of what's deliberately left undone in each phase,
replacing the old approach of only ever recording an open item in prose
at the bottom of whichever phase doc happened to close that slice. Those
per-slice "What's still open" sections still exist and still carry the
full *why* for each item -- this doc is the index that stays current
across all of them, so "what's left" never has to be reconstructed by
rereading a dozen files. Update this doc, not just the closing slice's
own doc, whenever an item here gets built or a new one is deliberately
deferred.

Each item carries a rough size (S/M/L) and a one-line value note, same
framing used to prioritize which of these to pick up next. Closed items
move to "Recently closed" for one entry, then drop off -- git history
and the phase docs are the permanent record, not this file.

**Last updated:** 2026-08-13, after closing two Phase 3 items
(structural relationships from the connect panel; the floating/
draggable attribute panel).

## Phase 1 — Metadata core

| Item | Size | Value | Detail |
|---|---|---|---|
| Multi-class atomic publish | M | Compiling two related feature classes today is two separate `metadata_versions` rows/transactions; a real "validate everything, compile everything, apply as one unit" workflow doesn't exist yet. | `docs/phase-1-metadata-core.md` |
| Publish-time validation beyond individual `CHECK` failures | S–M | E.g. "every feature class needs a default `symbology_rules` row before publish" isn't enforced anywhere. | `docs/phase-1-metadata-core.md` |
| Computed fields (`is_computed = true`) | M | Compiles to a plain nullable column today, not a generated one (e.g. `length_m` via `ST_Length(geom)`) -- nowhere in the metadata yet to store the expression. | `docs/phase-1-metadata-core.md` |

## Phase 2 — CRUD / connectivity / relationships engine

| Item | Size | Value | Detail |
|---|---|---|---|
| No filtering or sorting on list | S | List is a bounded, offset-paginated scan ordered by `created_at, id` only -- fine for demo data, not for a real feature count. | `docs/phase-2-crud-engine.md` |
| Attachments (RustFS-backed) | L | A genuinely different design surface (upload flow, size/type limits) not yet worked through in enough detail to build. | `docs/phase-2-crud-engine.md`, `docs/phase-2-relationships.md` |
| Diagrams | L | Same status as attachments -- still ahead per the original Phase 2 plan. | `docs/phase-2-crud-engine.md` |
| `numeric` fields round-trip through Go `float64` | S | Not exact-decimal-safe; worth revisiting if a field ever needs currency-grade precision. | `docs/phase-2-crud-engine.md` |
| No `PATCH` for a connection or relationship | S–M | Both are re-established or revoked, never edited in place -- fine for now, but a wrong terminal/kind pick means delete-and-redo. | `docs/phase-2-relationships.md` |
| `ST_DWithin` plausibility check for relationships | S | `docs/metadata-core.md`'s POLE sample mentions this as a soft geometric-plausibility check alongside the explicit establish requirement; not implemented -- nothing warns when two related features are implausibly far apart. | `docs/phase-2-relationships.md` |

## Phase 3 — Map loop

| Item | Size | Value | Detail |
|---|---|---|---|
| Metadata-driven symbology resolution | M–L | The core "looks like a real GIS" gap -- every feature of a given geometry type looks identical regardless of feature class today. Resolution is genuinely multi-axis (class+subtype+domain-value specificity, zoom-gated, map-vs-diagram context) and `icon_ref` implies a sprite pipeline that doesn't exist yet -- real design work. | `docs/phase-3-map-loop.md` |
| Azure AD SSO + backend auth | L | Legitimately greenfield -- no session/token handling, no user table, no frontend auth state anywhere (`README.md`: "Auth and permissions are deliberately not modeled yet"). A prerequisite for Phase 6 access control more than something the map loop itself needs. | `docs/phase-3-ui-shell.md` |
| Real Martin soft-delete-aware fix | L, unresolved unknown | Today's fix (`deleted-features-store.ts`) is purely client-side and session-local -- a fresh tab or a different user still sees a soft-deleted feature. The real fix (compiler-generated `deleted_at IS NULL` view, Martin serving from it) is blocked on an unconfirmed question: Martin auto-discovers tiles by table name, so the view would need to be published under the same name the table currently uses -- needs a spike before it can be sized honestly. | `docs/phase-3-delete-confirmation.md` |
| Metadata-driven `snapping_rules` | M | `docs/phase-3-snapping.md` shipped a plain OL `Snap` interaction against real feature geometry; the metadata table (per-feature-class-pair tolerance/eligibility) is still schema-only, deliberately deferred as a distinct, later layer on top of what exists now. | `docs/phase-3-snapping.md` |
| Peer-picker disambiguation for connect-mode target-picking | S | Picking a connection/relationship *target* on the map always takes the topmost hit at that pixel; normal feature selection already has a peer picker for exactly this (stacked/overlapping features), the connect workflow doesn't reuse it yet. | `docs/phase-3-connectivity-ui.md` |
| Revoking a connection from the UI | S | `DELETE /connectivity/{id}` exists on the backend (Phase 2); nothing in the Terminals tab triggers it -- a wrong connection today has to be fixed via curl. | `docs/phase-3-connectivity-ui.md` |
| No loading affordance on the highlight halo | S | A connection/relationship row click that targets an unfetched feature takes a beat before its halo appears on the map, no spinner in the meantime (the attribute panel's own tab already shows one for the same fetch). | `docs/phase-3-map-aesthetics.md` |
| Floating panel resize/minimize | S–M | The panel undocked into a movable floating window this session (see "Recently closed"); resizing and minimizing it were flagged together as later scope when undocking was first pinned, and remain out of scope -- fixed `w-96 h-64` for now. | `docs/phase-3-floating-panel.md` |
| Polygon vertex editing, real re-verification | S | `docs/phase-3-edit-geometry.md` shipped `Modify`-based vertex editing for line/polygon alike but only exercised it against a line (no seeded polygon existed at the time). A real `SUBSTATION_YARD` polygon now exists in the demo dataset -- worth a quick pass to actually confirm the polygon path, not just the shared-code argument for it. | `docs/phase-3-edit-geometry.md` |

## Phase 4 — Network model

Not started. Per `README.md`: the trace engine, built against the
`terminals`/`network_rules`/`trace_configurations` metadata (Phase 1)
and the connectivity Phase 2 establishes. Not itemized here yet --
scoping this out is the natural next step once the Phase 3 items above
are worked through or deliberately deferred further.

## Phases 5-7

Not started; see `README.md`'s phase list for their one-line scope
(diagrams/attribute groups/MCP; domain expansion + hardening; field
collection). Too far out to itemize yet.

## Recently closed

- **Structural relationships from the connect panel** (this session) --
  `connect-confirm-panel.tsx` now branches on `connect-store.ts`'s
  `mode` to call `POST /relationships` (no terminal pickers, an
  optional `kind` field) instead of always assuming connectivity;
  `feature-panel.tsx`'s Relationships tab gained a matching "New
  relationship" trigger.
- **Undockable/floating attribute panel** (this session) --
  `feature-panel.tsx` adopted `connect-confirm-panel.tsx`'s drag
  pattern (header pointer-down, `position` state) and become a
  `fixed`-positioned floating window instead of a docked, full-width,
  fixed-height row between the map and the status bar.
