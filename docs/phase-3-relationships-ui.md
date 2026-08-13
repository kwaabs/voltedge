# Phase 3 (slice 14) — establishing structural relationships from the map

**Goal:** let a user create a structural/spatial relationship (e.g. a
pole supporting a conductor span) directly from the map, the same way
the connect panel already establishes electrical connectivity --
closing the first of the two small, high-leverage items picked up from
`docs/open-items.md` after the four-slice CRUD initiative wrapped.
**Status:** done and verified against a real Postgres/Martin/API/Vite
stack.

## Why this slice exists, and what it needed that didn't exist yet

Structural relationships (`POST /relationships`, cardinality-enforced,
`api/internal/relationship`) have existed on the backend since Phase 2
(`docs/phase-2-relationships.md`), and the feature panel's
Relationships tab has shown them, read-only, since the connectivity UI
slice (`docs/phase-3-connectivity-ui.md`). That slice's own "What's
still open" section flagged exactly this gap and exactly how cheap it
would be to close: `connect-store.ts` already had a `ConnectMode` of
`"relationship"` alongside `"connectivity"`, and
`connect-confirm-panel.tsx`'s header already branched its title text on
`mode` -- both built in anticipation of this slice, sitting unused until
now. Confirming this before writing anything (rather than assuming the
scaffolding still matched) was the only real investigation this slice
needed.

## What was built

- **`src/components/map/connect-confirm-panel.tsx`** -- `handleConfirm`
  now branches on `mode`: `"connectivity"` keeps its existing
  `POST /connectivity` call unchanged; `"relationship"` calls
  `POST /relationships` with `{ origin, destination, kind }` -- no
  terminal fields, since `relationship_classes` has no terminal concept
  the way `network_rules` does. `EndpointPicker` gained a `showTerminal`
  prop (`mode === "connectivity"`) rather than trying to infer whether
  to show a terminal dropdown from whether the feature class happens to
  have terminals -- a relationship workflow never shows one, full stop,
  even for a feature class that does have terminals for its
  connectivity workflow. A relationship-only "Kind" text input appears
  between the two endpoints and the Confirm/Cancel row, always optional:
  `api/internal/relationship/rules.go`'s `matchClass` only requires it
  to disambiguate when more than one `relationship_classes` row matches
  the same origin/destination pairing, and rejects an ambiguous request
  with a message that already names the valid kinds inline -- surfaced
  through the same toast every other rejection in this app already
  uses, rather than building a second UI just to list them out.
- **`src/components/map/feature-panel.tsx`** -- the Relationships tab
  gained a "New relationship" button, identical placement and styling
  to the Terminals tab's existing "New connection" button, calling
  `useConnectStore.getState().start("relationship", selected)`.

## How this was validated

Driven against a real stack via Playwright, with real seeded `POLE`,
`MV_CONDUCTOR`, `DIST_XFMR`, and `SUBSTATION_YARD` features already on
the map.

- "New relationship" opens the same floating panel "New connection"
  does, titled correctly, with no terminal dropdown under either
  endpoint -- confirmed by contrast against starting "New connection"
  from the same feature, which does show terminal dropdowns for a
  feature class that has any (`DIST_XFMR`'s T1/T2/T3).
- The optional Kind field renders and accepts input.
- Picking a second feature and confirming with no `relationship_classes`
  row authorizing that particular pairing correctly failed with a real
  400 and a toast (zero-trust default, same principle
  `docs/phase-2-relationships.md` established on the backend) -- the
  panel stayed open afterward so the attempt could be retried, matching
  the connectivity workflow's own existing failure behavior rather than
  silently closing on a rejected request.
- Cancel/Escape discarded the workflow cleanly with no request sent.

## Decisions made

- **Ambiguous-kind resolution stays in the toast, not a dedicated
  picker.** `matchClass`'s rejection message already lists every valid
  kind for the pairing; building a second, structured "pick a kind"
  control would duplicate information the backend already computes and
  already surfaces, for a case (more than one `relationship_classes` row
  for the same feature-class pairing) this app's seed data doesn't
  currently exercise at all.
- **`showTerminal` as an explicit prop, not inferred from the feature
  class's own terminals.** A relationship workflow has no terminal
  concept regardless of what the feature class involved happens to
  support elsewhere -- inferring it from "does this feature have
  terminals" would have been answering the wrong question.

## What's still open

- **Peer-picker disambiguation for connect-mode target-picking** --
  unchanged from `docs/phase-3-connectivity-ui.md`; picking a
  relationship's destination on the map still always takes the topmost
  hit at that pixel.
- **Revoking a connection or relationship from the UI** -- both
  `DELETE` endpoints exist on the backend; neither has a trigger in the
  panel yet.
- Everything else tracked in `docs/open-items.md` is unchanged by this
  slice.

## Try it yourself

Same stack as the rest of Phase 3's slices -- `task infra-up && task
migrate && task api-dev`, then `cd web && npm run dev`. Open
`http://localhost:5173`:

- Select a feature, open its Relationships tab, click "New
  relationship". Note the absence of a terminal dropdown compared to
  "New connection" in the Terminals tab.
- Click a second feature, optionally type a Kind, click Confirm.
  Without a matching `relationship_classes` row for that pairing,
  expect a rejection toast -- the workflow stays open to retry.

`npm run build && npm run lint` and `go build ./... && go vet ./... &&
gofmt -l .` should all finish clean.
