# Phase 3 (slice 15) — the attribute panel undocks

**Goal:** turn the feature attribute panel from a fixed, docked strip
between the map and the status bar into a floating, draggable window --
the second of the two small, high-leverage items picked up from
`docs/open-items.md` after the four-slice CRUD initiative wrapped.
**Status:** done and verified against a real Postgres/Martin/API/Vite
stack.

## Why this slice exists, and what it needed that didn't exist yet

`feature-panel.tsx`'s own doc comment had flagged this as deliberately
deferred since the very first version of the panel
(`docs/phase-3-map-loop.md`): docked to the bottom of the map, spanning
its full width, fixed at `h-64`. The pattern to undock it already
existed and had already been proven working elsewhere in this app --
`connect-confirm-panel.tsx`'s header-drag mechanics (pointer-down
captures the panel's current position, `pointermove`/`pointerup`
listeners on `window` update it, released on pointer-up) needed no
redesign, just the same `position` state and `handleHeaderPointerDown`
copied onto the attribute panel. The one real design question was
default placement and behavior across selections, not mechanics.

## What was built

- **`src/components/map/feature-panel.tsx`** -- both of the panel's two
  render paths (an unsaved new-feature draft, and a selected existing
  feature) now render as a `fixed`, `w-96 h-64` rounded/bordered/
  shadowed box instead of a docked `h-64 shrink-0` row in the page's
  flex column. Default position is the bottom-right corner
  (`bottom-10 right-4`, clear of the status bar's own `h-7` row below
  it) until the user drags it elsewhere, same `!position` fallback
  className pattern `connect-confirm-panel.tsx` already used. A small
  `GripHorizontal` icon sits in the header next to the feature name/id,
  signaling it's draggable the way `connect-confirm-panel.tsx`'s own
  cursor-grab header already implied without an explicit icon --
  worth the icon here since this panel is the primary, most-used
  surface in the app, not a transient workflow confirmation.
- **`src/routes/map.tsx`** -- unchanged. `FeaturePanel` no longer
  participates in the page's flex column at all (a `fixed`-positioned
  element is removed from normal document flow), so the map's own
  `flex-1` region now fills the space the docked panel used to reserve
  -- a side effect of the CSS change itself, not something that needed
  separate wiring. OpenLayers' `Map` already resize-observes its target
  element (this app never called `updateSize()` manually, and the
  docked panel already mounted/unmounted the reserved space on every
  select/deselect before this slice, so this resize path was already
  exercised, just less often).
- **`position` deliberately isn't reset on a new selection.** This is
  the one place this slice's behavior diverges from
  `connect-confirm-panel.tsx`'s own drag pattern: that panel resets
  `position` to `null` (back to its default corner) every time a fresh
  connect/relate workflow starts, because its terminal/kind picks
  shouldn't carry over into an unrelated new session either. The
  attribute panel has no equivalent per-session state to reset --
  a user who's dragged it out of the way once almost certainly wants it
  to stay there across every subsequent click, not snap back to the
  corner on each new selection. React already preserves a component's
  own `useState` across re-renders (this component doesn't unmount when
  nothing is selected, it just returns `null` for that render), so this
  didn't need any code beyond simply not writing a reset effect.

## How this was validated

Driven against a real stack via Playwright, with real seeded features
already on the map.

- Selecting a feature shows the panel as a floating box in its default
  bottom-right corner, clear of the status bar, with the grip icon
  visible in the header.
- Dragging by the header moved the panel and it stayed at the dropped
  position.
- Selecting a different feature while the panel was in a dragged
  position left the position unchanged -- only the panel's content
  (name, id, tabs) updated to the newly selected feature.
- Closing the panel and reselecting a feature reopened it at whichever
  position it had last been left, not the default corner.
- Both themes render the panel and its drag handle legibly.

## Decisions made

- **Fixed `w-96 h-64`, no resize or minimize.** Undocking was the
  specific, requested piece; resizing and minimizing were flagged
  together as a distinct later concern when this was first pinned back
  in `docs/phase-3-map-loop.md`, and stay there -- adding either now
  would have doubled this slice's scope for something not actually
  asked for yet.
- **Position persists across selections, unlike the connect panel's
  per-workflow reset.** The two panels serve different mental models: a
  connect/relate workflow is a short-lived session that should start
  fresh every time; the attribute panel is the primary surface a user
  keeps positioned wherever suits their screen for the whole session.

## What's still open

- **Resize and minimize** -- explicitly still deferred, as above.
- Everything else tracked in `docs/open-items.md` is unchanged by this
  slice.

## Try it yourself

Same stack as the rest of Phase 3's slices -- `task infra-up && task
migrate && task api-dev`, then `cd web && npm run dev`. Open
`http://localhost:5173`:

- Select a feature -- the attribute panel now floats near the
  bottom-right corner instead of docking full-width at the bottom.
- Drag it by its header (grip icon) anywhere on screen, then select a
  different feature -- it stays put, only the content changes.

`npm run build && npm run lint` and `go build ./... && go vet ./... &&
gofmt -l .` should all finish clean.
