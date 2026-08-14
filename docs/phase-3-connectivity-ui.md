# Phase 3 (slice 8) — establish electrical connectivity from the map

**Goal:** let a user establish a real electrical connection between two
features' terminals from the map itself, through the zero-trust
`POST /connectivity` engine Phase 2 already built
(`docs/phase-2-connectivity.md`) — nothing in the UI could reach it
before this slice; every connection in the system so far had been
created by seed SQL or curl. Structural relationships (the same engine's
sibling, `POST /relationships`) are deliberately out of scope here — see
"What's still open."
**Status:** done and verified against a real Postgres/Martin/API/Vite
stack — a real connection established through the UI end to end, both
features' Terminals tabs confirmed showing it without a reload, cancel
and Escape both verified to fully abort, both themes checked. Two real
bugs were caught and fixed during verification — see "How this was
validated." A follow-on slice later closed this doc's own "Revoking a
connection from the UI" open item — see "Revoke, added later" below.

## Why this slice exists

The user asked for it directly, and gave the interaction shape up
front: *"establish electric connectivity between features through their
terminals. user should select a feature, trigger a connectivity floating
panel (movable) then select the terminal of the next feature to connect
to. confirm connection then commit to db."* They also asked about
structural relationships in the same message, and agreed with the
recommendation to sequence connectivity first — relationships reuses
most of the same plumbing (`docs/phase-2-relationships.md`'s engine
mirrors `docs/phase-2-connectivity.md`'s shape almost exactly), so
finishing and hardening one workflow before starting the second was
worth doing. Asked where the workflow should start, the user picked
starting it from inside the existing Terminals/Relationships tabs (a
"+ New connection" button at the top of the tab that already lists a
feature's connections), rather than a separate toolbar tool.

## A backend gap closed first: the frontend didn't know a feature class's terminals

`GET /feature-classes/{code}/schema` (built in the prior create-feature
slice) already returned subtypes and fields, but nothing exposed a
feature class's terminals — the named connection points
(`metadata.terminals`, e.g. `DIST_XFMR`'s `T1`/`T2`/`T3`) a connectivity
picker needs to offer.

- **`terminalSchema`** (new, `api/cmd/server/main.go`) — one entry per
  terminal: `code`, `role`, `phase_capable`, and `subtype_code` (omitted
  when the terminal applies to every subtype of the feature class, set
  when it's scoped to one specific subtype — mirroring
  `metadata.Terminal.SubtypeID`'s own nil-means-generic convention).
  `featureClassSchemaResponse` gained a `terminals` field built the same
  way as `subtypes`/`fields` — from `metadata.Load`, the same loader
  already backing every other part of this endpoint and `api/internal/
  connectivity`'s own rule matching, so the UI's idea of "what terminals
  does this feature have" can never drift from the engine's. Verified
  directly against real `DIST_XFMR` metadata via curl before any
  frontend code consumed it.
- **`terminalsForSubtype(schema, subtypeCode)`** (new,
  `src/hooks/use-feature-class-schema.ts`) — the terminals a *specific*
  subtype can connect through, mirroring
  `api/internal/metadata`'s `ResolveTerminal`: a terminal scoped to the
  subtype wins over a generic one with the same code. Getting this
  wrong client-side would mean the picker offering a terminal the
  server would actually reject (or silently preferring the wrong one of
  two same-coded terminals) — worth the one small function to keep the
  two resolution rules in lockstep rather than hoping they stay that way
  by accident.

## What was built

- **`src/stores/connect-store.ts`** (new) — the workflow's state
  machine: `mode` (`"connectivity" | "relationship" | null`, the latter
  unused until the relationships slice), `source` (locked in the moment
  `start()` is called — the feature selected when "+ New connection" was
  clicked), `target` (set once a second feature is clicked on the map).
  Not persisted — an in-progress workflow has no meaning across a
  reload, same reasoning as `draw-store.ts`.
- **`src/components/map/feature-panel.tsx`**'s `TerminalsTab` gained a
  "+ New connection" button (top of the tab, always shown once a feature
  is selected, above the existing connection list) that calls
  `useConnectStore.getState().start("connectivity", selected)`.
- **`src/components/map/voltedge-map.tsx`** gained a connect-mode branch
  in the existing click handler, checked right after the existing
  draw-mode guard: while `connectState.mode` is set and no `target` is
  picked yet, a click hit-tests the topmost feature under the cursor
  (no peer-picker disambiguation in this first version — a deliberate
  scope cut) and calls `pickTarget`; a click on empty space is ignored
  rather than treated as cancel, so an imprecise click can't silently
  abandon the workflow — only Escape (a new effect, added right after
  the existing draft-clearing one) or the panel's own Cancel/X can. The
  map cursor also switches to crosshair while connect mode is active,
  mirroring the existing draw-mode crosshair, so hovering the map gives
  a visual cue that a click means something different right now (see
  "How this was validated" below — this wasn't there in the first
  draft).
- **`src/components/map/connect-confirm-panel.tsx`** (new) — the
  floating, draggable confirm panel, mounted once in `src/routes/
  map.tsx` alongside the other map overlays. Renders as soon as `mode`
  and `source` are set (not waiting on `target` too — see "How this was
  validated"): the source feature's real name/id and a terminal
  `<select>` (only rendered when `terminalsForSubtype` returns anything
  for that feature's actual subtype — an edge like `MV_CONDUCTOR` never
  will), a dashed placeholder in the target's spot reading "Click a
  second feature on the map to connect to" until one is picked, then the
  same endpoint display for the target once it is. Dragged by its
  header via raw pointer events (`pointerdown` captures the start
  offset, `window`-level `pointermove`/`pointerup` for the duration of
  one drag, removed on `pointerup`) — no new dependency, same
  hand-authored-overlay pattern as the peer picker. Confirm is disabled
  until a target exists; on click it `POST`s to `/connectivity` with
  each side's feature class/id and chosen terminal (or none), then
  invalidates both features' `useConnections` query cache
  (`["connections", featureClassCode, featureId]`) so both Terminals
  tabs reflect the new connection without a reload, and calls `cancel()`
  to close itself. A rejected request (the zero-trust engine's own 400,
  e.g. no matching `network_rules` row) shows its real message in the
  panel instead of closing it, so the user can pick a different pairing
  or terminal without losing their place.

## How this was validated — and two real bugs this caught

Driven against a real stack (Postgres, a real Martin binary, the Go
API, the Vite dev server) via Playwright, not assumed from the code.

**First pass** covered the intended happy path plus edge cases: select a
feature, open Terminals, click "+ New connection," drag the panel,
click a second feature, Escape to cancel and confirm normal selection
resumes afterward, Confirm a real pairing, check both themes. Two real
bugs surfaced:

1. **No feedback that connect mode had even started.** The panel's
   first version returned `null` until *both* `source` and `target`
   were set — so clicking "+ New connection" did nothing visible at
   all; the map cursor didn't change either, since only draw mode was
   checked. A user with no prior knowledge of the workflow had no way
   to tell anything had happened. Fixed by rendering the panel as soon
   as `source` alone is set (placeholder + disabled Confirm in the
   target's spot, described above) and extending the existing
   draw-mode crosshair check to also cover connect mode.
2. **Stale terminal/error state leaked across separate connect
   sessions.** Because the panel never unmounts (it renders `null`
   rather than being conditionally mounted by its parent), its
   `sourceTerminal`/`targetTerminal`/`error` state persisted across
   calls to `start()`. Concretely: pick terminal `T1` on a `DIST_XFMR`
   source, get a legality error confirming a bad pairing, cancel, then
   start a *new* session from a different feature — the old error stayed
   visible, and worse, `T1` could get silently resubmitted as that new
   feature's terminal even if the new feature doesn't have one, producing
   a confusing, unrelated rejection (`terminal "T1" is not defined for
   MV_CONDUCTOR`). Fixed with an effect that resets all three whenever a
   session starts, keyed on `mode` plus the source feature's identity —
   so both an outright new session (`mode` transitioning from `null`)
   and a same-session-still-open re-click of "+ New connection" on a
   different feature (source identity changing) both trigger a clean
   reset.

**Second, focused pass** re-verified just those two fixes against the
same repro steps (source-only render + crosshair cursor; terminal `T1`
selected → rejected pairing → cancel → new session from a different
feature → confirm no leftover error or terminal) — both confirmed fixed,
no new issues. The one real connection created during the first pass's
successful-Confirm check (`MV_CONDUCTOR` → `DIST_XFMR.T1`, the only
`network_rules`-authorized direction in the seed data) was revoked
afterward via `DELETE /connectivity/{id}` — connections have no
soft-delete lifecycle, so that endpoint (already built and tested in
Phase 2) is the correct cleanup path — leaving `runtime.feature_
connectivity` empty again.

## Revoke, added later

The one item this slice originally left open that had no real design
work left to do -- `DELETE /connectivity/{id}` (`api/internal/
connectivity`) had existed since Phase 2, exercised only by this
slice's own test cleanup, never by an actual UI control.

- **`src/components/map/revoke-connection-dialog.tsx`** (new) -- same
  `AlertDialog`-based, can't-dismiss-mid-request pattern as
  `delete-feature-dialog.tsx` (a revoke is a hard, permanent remove
  with no soft-delete/restore pair, same as a feature delete, so it
  gets the same explicit-choice guard). On confirm, `DELETE
  /connectivity/{id}`, then `queryClient.invalidateQueries({queryKey:
  ["connections"]})` -- a bare prefix, not scoped to one feature's
  `["connections", featureClassCode, featureId]` key, since a revoke
  changes both ends' lists and the counterpart's Terminals tab may
  already be warm in cache from an earlier selection.
- **`feature-panel.tsx`'s `TerminalsTab`** -- each connection row
  changed from a single `<button>` (whole row navigates to the
  counterpart) to a `<div>` wrapping that same button plus a sibling
  icon button (`Unlink2`, `sr-only` "Revoke connection" text) that opens
  the dialog for that row's connection -- restructured specifically to
  avoid a `<button>` nested inside a `<button>`, which is invalid HTML.

Verified live: the icon appears on every row, Cancel and Escape both
leave the connection untouched (confirmed surviving a reload), a real
revoke removes the row immediately on the feature whose panel is open
*and* on the counterpart's own Terminals tab without a reload (proving
the broad `["connections"]` invalidation actually reaches both sides),
and the removal survives a hard reload (a real backend delete, not a
client-side hide). The one connection permanently revoked during
testing (`DIST_XFMR` "Substation Power Transformer" terminal T2 ->
`MV_CONDUCTOR` "Feeder source to S0") was re-established through the
existing "+ New connection" flow, same two features and terminal,
before verification finished -- topologically and terminal-wise
identical to the original; only the connection's own `id` and
`established_at` differ, unavoidable for a real delete-and-recreate.

## Decisions made

- **No peer-picker disambiguation when picking a target.** Selecting a
  feature elsewhere in this app already handles overlapping features via
  a peer picker (`docs/phase-3-map-loop.md`); connect-mode's target pick
  deliberately doesn't reuse it yet, always taking the topmost hit — a
  scope cut for this first version, not a design decision to keep
  permanently (see "What's still open").
- **A click on empty space while picking a target is ignored, not
  treated as cancel.** Only Escape or an explicit Cancel/X aborts the
  workflow — an imprecise click missing the intended feature shouldn't
  silently discard a workflow the user is still actively engaged in.
- **The panel renders as soon as a source exists, not only once both
  endpoints are picked.** Directly follows from bug #1 above — a
  multi-step workflow needs to show its own progress, not stay invisible
  until the last step.
- **Attempt-then-disambiguate for relationships (next slice), not a new
  preview endpoint.** Not built in this slice, but worth recording here
  since it shapes the boundary: `api/internal/relationship/rules.go`'s
  `matchClass` already returns a specific 400 listing valid `kind`
  values when a request omits `kind` and multiple `relationship_classes`
  rows match the pairing — the relationships UI can reuse this panel's
  plumbing and only show a kind-picker reactively when that error comes
  back, no new backend work needed.

## What's still open

- **Structural relationships** — the sibling workflow the user asked
  about in the same request, deliberately sequenced after connectivity.
  `connect-store.ts` already has `mode: "relationship"` and
  `connect-confirm-panel.tsx` already branches its header text on
  `mode`, precisely so this reuses almost everything: a "+ New
  relationship" button in `RelationshipsTab`, no terminal dropdowns, and
  the reactive `kind`-disambiguation described above.
- **Peer-picker disambiguation for target-picking** — currently always
  takes the topmost hit; overlapping features can't be distinguished the
  way normal selection already handles it.
- **Floating/resizable/minimizable panel** — pinned by the user
  elsewhere in this project; this panel is draggable only, matching that
  existing scope cut.

## Try it yourself

Same stack as `docs/phase-3-create-feature.md`'s "Try it yourself" —
`task infra-up && task migrate && task api-dev`, then `cd web && npm run
dev`. Open `http://localhost:5173`:

- Click a feature on the map, open its **Terminals** tab, and click
  **"+ New connection."** A floating panel appears near the top-right
  showing that feature's name/id (and a terminal dropdown, if its
  subtype has any) — drag it by its header if it's in the way.
- The map cursor becomes a crosshair. Click a second feature anywhere on
  the map — the panel updates to show both endpoints, each with its own
  terminal dropdown where applicable, and an arrow between them.
- Click **Confirm** to establish the connection (`POST /connectivity`),
  or **Cancel**/the panel's **X**/**Escape** to abort. A rejected pairing
  (no matching, active `network_rules` row — see
  `docs/phase-2-connectivity.md`) shows its real error in the panel
  instead of closing it.
- After a successful Confirm, reopen either feature's Terminals tab —
  the new connection shows up in both, no reload needed.
- Click the small unlink icon on any connection row to revoke it —
  confirm in the dialog, and it disappears from both features' Terminals
  tabs immediately, no reload needed.

`npm run build && npm run lint` and `go build ./... && go vet ./... &&
gofmt -l .` should all finish clean.
