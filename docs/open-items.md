# Open items log

A single, current list of what's deliberately left undone in each phase
-- and, right alongside it, what's already been closed -- so "what's
outstanding" is answerable by glancing at one **Status** column instead
of cross-referencing a separate section or rereading a dozen phase
docs. Replaces the old approach of only ever recording an open item in
prose at the bottom of whichever phase doc happened to close that
slice; those per-slice "What's still open" sections still exist and
still carry the full *why* for each item, this doc is just the index.

**Status** is `Open` or `Done`. A row never gets deleted when it
closes -- it flips to `Done` and stays, so scrolling a phase's table
shows its whole recent trajectory, not just a snapshot. Update this
doc, not just the closing slice's own doc, whenever an item here gets
built or a new one is deliberately deferred. Open rows are listed
first within each phase, Done rows after (most recently closed first)
-- so scanning top-to-bottom surfaces what's actionable before what's
already finished.

Each item still carries a rough size (S/M/L) and a one-line value
note for open rows, same framing used to prioritize what to pick up
next; a closed row keeps its size/value for context but it's no longer
load-bearing. This log's own rows start once a phase has *some* mix of
open and closed items worth tracking side by side -- it's not a full
changelog back to each phase's original build (that history is
already in `README.md` and each phase's own write-up doc).

**Last updated:** 2026-08-13, after Phase 4's second slice (universal
feature names + the trace result table).

## Phase 1 — Metadata core

| Item | Status | Size | Value | Detail |
|---|---|---|---|---|
| Multi-class atomic publish | Open | M | Compiling two related feature classes today is two separate `metadata_versions` rows/transactions; a real "validate everything, compile everything, apply as one unit" workflow doesn't exist yet. | `docs/phase-1-metadata-core.md` |
| Publish-time validation beyond individual `CHECK` failures | Open | S–M | E.g. "every feature class needs a default `symbology_rules` row before publish" isn't enforced anywhere. | `docs/phase-1-metadata-core.md` |
| Computed fields (`is_computed = true`) | Open | M | Compiles to a plain nullable column today, not a generated one (e.g. `length_m` via `ST_Length(geom)`) -- nowhere in the metadata yet to store the expression. | `docs/phase-1-metadata-core.md` |

## Phase 2 — CRUD / connectivity / relationships engine

| Item | Status | Size | Value | Detail |
|---|---|---|---|---|
| No filtering or sorting on list | Open | S | List is a bounded, offset-paginated scan ordered by `created_at, id` only -- fine for demo data, not for a real feature count. | `docs/phase-2-crud-engine.md` |
| Attachments (RustFS-backed) | Open | L | A genuinely different design surface (upload flow, size/type limits) not yet worked through in enough detail to build. | `docs/phase-2-crud-engine.md`, `docs/phase-2-relationships.md` |
| Diagrams | Open | L | Same status as attachments -- still ahead per the original Phase 2 plan. | `docs/phase-2-crud-engine.md` |
| `numeric` fields round-trip through Go `float64` | Open | S | Not exact-decimal-safe; worth revisiting if a field ever needs currency-grade precision. | `docs/phase-2-crud-engine.md` |
| No `PATCH` for a connection or relationship | Open | S–M | Both are re-established or revoked, never edited in place -- fine for now, but a wrong terminal/kind pick means delete-and-redo. | `docs/phase-2-relationships.md` |
| `ST_DWithin` plausibility check for relationships | Open | S | `docs/metadata-core.md`'s POLE sample mentions this as a soft geometric-plausibility check alongside the explicit establish requirement; not implemented -- nothing warns when two related features are implausibly far apart. | `docs/phase-2-relationships.md` |

## Phase 3 — Map loop

| Item | Status | Size | Value | Detail |
|---|---|---|---|---|
| Metadata-driven symbology resolution | Open | M–L | The core "looks like a real GIS" gap -- every feature of a given geometry type looks identical regardless of feature class today. Resolution is genuinely multi-axis (class+subtype+domain-value specificity, zoom-gated, map-vs-diagram context) and `icon_ref` implies a sprite pipeline that doesn't exist yet -- real design work. | `docs/phase-3-map-loop.md` |
| Azure AD SSO + backend auth | Open | L | Legitimately greenfield -- no session/token handling, no user table, no frontend auth state anywhere (`README.md`: "Auth and permissions are deliberately not modeled yet"). A prerequisite for Phase 6 access control more than something the map loop itself needs. | `docs/phase-3-ui-shell.md` |
| Real Martin soft-delete-aware fix | Open | L, unresolved unknown | Today's fix (`deleted-features-store.ts`) is purely client-side and session-local -- a fresh tab or a different user still sees a soft-deleted feature. The real fix (compiler-generated `deleted_at IS NULL` view, Martin serving from it) is blocked on an unconfirmed question: Martin auto-discovers tiles by table name, so the view would need to be published under the same name the table currently uses -- needs a spike before it can be sized honestly. | `docs/phase-3-delete-confirmation.md` |
| Metadata-driven `snapping_rules` | Open | M | `docs/phase-3-snapping.md` shipped a plain OL `Snap` interaction against real feature geometry; the metadata table (per-feature-class-pair tolerance/eligibility) is still schema-only, deliberately deferred as a distinct, later layer on top of what exists now. | `docs/phase-3-snapping.md` |
| Peer-picker disambiguation for connect-mode target-picking | Open | S | Picking a connection/relationship *target* on the map always takes the topmost hit at that pixel; normal feature selection already has a peer picker for exactly this (stacked/overlapping features), the connect workflow doesn't reuse it yet. | `docs/phase-3-connectivity-ui.md` |
| Revoking a connection from the UI | Open | S | `DELETE /connectivity/{id}` exists on the backend (Phase 2); nothing in the Terminals tab triggers it -- a wrong connection today has to be fixed via curl. | `docs/phase-3-connectivity-ui.md` |
| No loading affordance on the highlight halo | Open | S | A connection/relationship row click that targets an unfetched feature takes a beat before its halo appears on the map, no spinner in the meantime (the attribute panel's own tab already shows one for the same fetch). | `docs/phase-3-map-aesthetics.md` |
| Floating panel resize/minimize | Open | S–M | The panel undocked into a movable floating window (see the Done row below); resizing and minimizing it were flagged together as later scope when undocking was first pinned, and remain out of scope -- fixed `w-96 h-64` for now. | `docs/phase-3-floating-panel.md` |
| Polygon vertex editing, real re-verification | Open | S | `docs/phase-3-edit-geometry.md` shipped `Modify`-based vertex editing for line/polygon alike but only exercised it against a line (no seeded polygon existed at the time). A real `SUBSTATION_YARD` polygon now exists in the demo dataset -- worth a quick pass to actually confirm the polygon path, not just the shared-code argument for it. | `docs/phase-3-edit-geometry.md` |
| Undockable/floating attribute panel | Done | S–M | The attribute panel adopted the connect-confirm-panel's drag pattern and became a `fixed`-positioned floating window instead of a docked, full-width row. | `docs/phase-3-floating-panel.md` |
| Structural relationships from the connect panel | Done | S | The connect panel now branches on mode to call `POST /relationships` (no terminal pickers, an optional `kind`) instead of always assuming connectivity; a matching "New relationship" trigger was added to the Relationships tab. | `docs/phase-3-relationships-ui.md` |

## Phase 4 — Network model

| Item | Status | Size | Value | Detail |
|---|---|---|---|---|
| Isolation and loop/mesh detection | Open | M | `docs/metadata-core.md`'s own framing: each composes two of the four primitive traces already built (upstream+downstream for isolation, connected run from two sources for loop detection) and post-processes the result -- deliberately deferred until the primitives have more real-world exercise, not designed speculatively alongside them. | `docs/phase-4-trace-engine.md` |
| `cross_transformers`/`respect_phase` enforcement | Open | M–L | Both accepted on a `trace_configurations` row and threaded through, not enforced -- real design questions (what marks a transformer boundary in a `network_role`-driven model that doesn't currently distinguish "transformer" from "any other junction"? how does phase capability intersect hop to hop?) deserve their own pass, not answers invented under time pressure. | `docs/phase-4-trace-engine.md` |
| No persistent/cached graph | Open | S–M, revisit only if needed | Every hop queries `runtime.feature_connectivity` fresh, matching `api/internal/connectivity`'s own stateless-per-request style -- fine at current seed-data scale; `docs/metadata-core.md` describes an in-memory graph as a longer-term role, not something the current data size gives any signal is actually needed yet. | `docs/phase-4-trace-engine.md` |
| No endpoint to discover which `trace_configurations` exist | Open | S | `feature-panel.tsx`'s two trigger buttons hardcode `DOWNSTREAM_TRACE`/`UPSTREAM_TRACE` by code. Fine for four fixed, seeded configs; would need a real listing endpoint if `trace_configurations` ever becomes something an admin authors freely. | `docs/phase-4-trace-engine.md` |
| Per-feature name not shown in connect panel / connection rows | Open | S | `connect-confirm-panel.tsx`'s `EndpointPicker` and the Terminals/Relationships tab's counterpart rows still show only the feature class name, never a per-feature name -- `EndpointPicker` already fetches the full feature record, so this is display-only work, not a new fetch. | `docs/phase-4-feature-names.md` |
| No search/filter by feature name | Open | S–M | Names are display-only right now -- nothing lets a user find a feature *by* its name (e.g. a search box). | `docs/phase-4-feature-names.md` |
| Universal feature name + trace result table | Done | — | `name` is now a system column every compiled feature class gets (same precedent as `deleted_at`), settable from the create/edit forms, shown as the floating panel header's primary label once set. The trace result banner became a real data table (feature class, id, name per row, barrier rows visually distinct, clickable). | `docs/phase-4-feature-names.md` |
| Core trace engine primitives | Done | — | `api/internal/trace` walks `runtime.feature_connectivity` per a named `trace_configurations` profile (`connected`/`downstream`/`upstream`/`shortest_path`), real barrier-stopping against a seeded `DIST_SWITCH`'s live `device_state`. Two "Trace downstream"/"Trace upstream" buttons trigger it and highlight the result on the map. | `docs/phase-4-trace-engine.md` |

## Phases 5-7

Not started; see `README.md`'s phase list for their one-line scope
(diagrams/attribute groups/MCP; domain expansion + hardening; field
collection). Too far out to itemize yet.
