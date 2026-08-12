# Phase 3 (slice 1) — App shell: layout, theming, routing

**Goal:** the foundation the map, forms, and every other Phase 3 view get
built into — a real layout (header + collapsible icon sidebar), a real
theme system (light/dark/system, a distinct palette, a distinct font),
and routing/state wiring — with no map and no auth yet. Both are
deliberately separate later slices; see "What's still open."
**Status:** done and verified in a real browser: theme resolution
(explicit + system, including a live OS-preference change), sidebar
collapse-to-icons, mobile responsiveness (sidebar becomes a slide-in
sheet), state persistence across reload, and zero console errors across
every route. See "Try it yourself" below.

## What was built

- **`web/`** — Vite + React 19 + TypeScript scaffold (`npm create vite@latest web -- --template react-ts`), builds and lints clean (`tsc -b && vite build`, `oxlint`).
- **Tailwind CSS v4** via `@tailwindcss/vite` — no separate PostCSS config needed, one plugin line in `vite.config.ts`.
- **shadcn/ui-style components, hand-authored** (`src/components/ui/`) — `button`, `card`, `badge`, `separator`, `sheet`, `tooltip`, `dropdown-menu`, `avatar`, and the full `sidebar` primitive (provider, trigger, collapsible-to-icon body, mobile sheet variant). See "Why hand-authored, not the shadcn CLI" below. `components.json` is in place so `npx shadcn add <component>` works normally on a real dev machine going forward.
- **Theme** (`src/index.css`) — OKLCH color tokens for every shadcn-standard variable (`background`, `foreground`, `card`, `primary`, `sidebar`, `chart-1..5`, etc.), defined twice: once under `:root` (light) and once under `.dark`. A deep slate neutral (not pure black/white in either mode) plus a copper/amber `primary` accent — a deliberate nod to "Volt" (electrical conductor) and a way to avoid the generic blue-SaaS default. Typography is IBM Plex Sans (UI) + IBM Plex Mono (data/IDs), self-hosted via `@fontsource` (latin subset only), not loaded from a CDN.
- **Theme resolution** (`src/stores/theme-store.ts` + `src/hooks/use-theme-effect.ts`) — a Zustand store holds the preference (`light`/`dark`/`system`, persisted to `localStorage`); a small hook resolves `system` against `matchMedia("(prefers-color-scheme: dark)")` and toggles the `.dark` class on `<html>`, live, if the OS preference changes mid-session. See "Zustand-backed theme, not next-themes" below.
- **Layout** (`src/components/layout/`) — `AppSidebar` (branded header, nav with active-route highlighting, icons-only when collapsed, tooltips on hover while collapsed), `AppHeader` (sidebar trigger, theme toggle, a sign-in button stub), `AppLayout` (wires `SidebarProvider` + both, with `<Outlet />` for routed content).
- **Sidebar collapse state** (`src/stores/ui-store.ts`) — another Zustand store, persisted, so the desktop expanded/collapsed choice survives a reload. The *mobile* drawer's open/closed state is deliberately **not** in a store — it's transient per-navigation UI state with nothing meaningful to persist, so it stays local `useState` inside `SidebarProvider`. Not everything storable needs to be stored globally; see "Decisions made."
- **Routing** — `react-router` v7, five routes (`/`, `/features`, `/connectivity`, `/relationships`, `/settings`) plus a catch-all 404, all under the shared `AppLayout`.
- **Server state wiring** — `@tanstack/react-query`, proven with one real call: `useHealth()` (`src/hooks/use-health.ts`) polls the Go API's `/healthz` every 30s and the dashboard renders a live "API healthy"/"API unreachable" badge from it. `src/lib/api.ts` is a thin `fetch` wrapper (`/api` prefix, JSON in/out, a typed `ApiError`) every future API hook builds on.
- **Dev proxy** (`vite.config.ts`) — `/api/*` proxies to `localhost:3704` (the Go API's port, per `.env.example`), so the browser never needs CORS configured on the API and every API call in the frontend can just say `/api/...` regardless of environment.
- **Placeholder pages** — `/features`, `/connectivity`, `/relationships`, `/settings` render a shared `PlaceholderPage` component (icon + description + "built in a later slice" note) rather than being empty routes -- so navigating the shell already demonstrates the intended information architecture.

## Why hand-authored components, not the shadcn CLI

`npx shadcn init`/`add` fetch component source and registry metadata
from `ui.shadcn.com` at run time — that's blocked by this sandbox's
network policy (`ui.shadcn.com:443` gets an explicit `403 policy
denial`, confirmed via the proxy status endpoint), so the CLI itself
can't run here. This is a sandbox-specific restriction, not a real
architectural constraint — `npx shadcn add <component>` should work
normally on an actual dev machine, and `components.json` is already in
place for exactly that.

What actually got built instead: every component in `src/components/ui/`
by hand, matching shadcn's well-published, stable component source
(Radix primitives + `class-variance-authority` + Tailwind, the same
patterns shadcn's own registry generates) — not a improvised
substitute, the same code shadcn would have scaffolded, just typed
directly rather than fetched.

## Zustand-backed theme, not next-themes

The obvious alternative for light/dark/system theming in a
shadcn-flavored app is `next-themes`. Skipped it here on purpose:
resolving "system" and flipping the `.dark` class is maybe 15 lines,
and the user's own direction for this phase was "use Zustand a lot" —
pulling in a separate library for something this small, when the
project already leans on Zustand for exactly this kind of small piece
of persisted client state (see `ui-store.ts`), would be adding a
dependency to avoid writing the thing the rest of the app already does
its own way. The store holds the *preference*; the DOM side effect
(resolving `system`, toggling the class) lives in a hook, not the
store, so state and side effect stay cleanly separated.

## Decisions made

- **Zustand for client/UI state, TanStack Query for server state** —
  the theme preference and sidebar-collapsed flag are genuine client
  state with no server counterpart, so they're Zustand stores. Anything
  that comes from the API (starting with the health check, and every
  feature/connectivity/relationship view after it) goes through Query
  instead, so caching, loading states, and refetching aren't hand-rolled
  inside a store. "Use Zustand for everything storable" is read as
  "everything that's actually client state," not literally everything,
  including data the API already owns and can go stale independently of
  any user action.
- **Copper/amber primary accent, not the shadcn default neutral/blue** —
  ties back to "Volt" (electrical conductor) and reads as a distinct,
  intentional identity rather than the extremely common default
  SaaS-blue look every shadcn starter has out of the box.
- **IBM Plex Sans/Mono, self-hosted via `@fontsource`, latin subset
  only** — a deliberately less-generic choice than Inter (the
  ubiquitous default), with a geometric, technical/engineering feel
  that fits a utility GIS tool. Self-hosted (not a Google Fonts `<link>`
  or CDN) so the app has no runtime dependency on an external font host
  and works offline in dev. Latin-only trims the font payload
  significantly (~13 files instead of ~70) — cyrillic/greek/vietnamese
  subsets are one import line away if this app ever needs to support
  those locales.
- **Mobile sidebar-open state stays local, not in a store** — genuinely
  transient (which drawer is open right now, for this navigation),
  nothing meaningful persists across a reload or is useful to read from
  elsewhere in the app. Contrast with the desktop collapsed/expanded
  choice, which *is* a real, meaningful, worth-persisting preference.
- **The health check as this slice's one real API call** — proves the
  whole chain (Vite dev proxy → Go API → TanStack Query → rendered UI)
  actually works, rather than taking it on faith while building forms
  that don't exist yet. Small, real, and end-to-end beats a bigger
  fake.
- **Sign-in is a header stub, not a real login screen** — Azure AD SSO
  is its own later slice (needs a real tenant app registration; see
  "What's still open"). The button exists now so the header's layout
  doesn't shift once real auth lands.

## How this was validated

Ran the actual Vite dev server against the actual Go API (real Postgres
behind it, migrations applied) and drove it with a headless Chromium
via Playwright:

- **System theme, no explicit choice** — a browser context with
  `colorScheme: "dark"` and no stored preference rendered dark
  immediately (confirms `useThemeEffect`'s default `matchMedia` read,
  not just the persisted-choice path).
- **Explicit light and explicit dark, via the header dropdown** —
  toggled through the `ThemeToggle` UI itself (not by calling the store
  directly), confirmed `<html>` gained/lost the `.dark` class each time.
- **Persistence across reload** — set dark + collapsed the sidebar,
  reloaded the page, confirmed both survived (`localStorage` round-trip
  through Zustand's `persist` middleware, not just in-memory state).
- **Sidebar collapse** — confirmed the collapsed width is exactly
  `3rem` (the icon-only width), the active nav item's accent background
  survives the collapse, and the group label ("Data") disappears
  without leaving a layout gap.
- **Mobile viewport (390×844)** — confirmed the sidebar doesn't render
  as a squeezed column but becomes a full slide-in `Sheet` triggered by
  the same header button, and cards reflow to a single column.
- **Every route** (`/`, `/features`, `/connectivity`, `/relationships`,
  `/settings`, and a nonexistent path) — navigated all six and
  confirmed **zero console errors or warnings** across the entire run,
  not just "the page rendered something."
- **The live health badge** — confirmed it read "API healthy" (green)
  against a real running API, proving the dev proxy and Query wiring
  both work, not just that the component compiles.

## What's still open

- **Azure AD SSO** — needs a real tenant app registration (client ID,
  tenant ID, redirect URI) that only the user can create in their Azure
  portal; this slice deliberately scaffolds nothing SSO-specific yet
  beyond the header's sign-in button placeholder, so there's nothing to
  half-wire against placeholders that would need revisiting anyway.
- **No backend auth** — matches the project's existing "auth
  deliberately not modeled yet" stance (see root `README.md`). The
  frontend login gate (once built) will not itself lock down the Go
  API; that's an explicit, separate decision for a later slice, not an
  oversight here.
- **The map** — OpenLayers, Martin tile config, metadata-driven
  symbology resolution. This slice is the shell the map view mounts
  into, not the map itself.
- **Snapping** — depends on the map existing first.
- **No real data views yet** — `/features`, `/connectivity`,
  `/relationships` are placeholders. Building them out means designing
  list/detail/edit views against the Phase 2 API, which is its own
  slice(s).
- **No `SidebarMenuSub`/nested nav, no command palette, no breadcrumbs**
  — not needed yet with four flat nav items; add when the nav actually
  grows past what a flat list can express clearly.

## Try it yourself

```
task infra-up
task migrate
task api-dev
```
From a second terminal:
```
cd web
npm install
npm run dev
```
Open `http://localhost:5173`. You should see:
- A soft off-white dashboard (light mode) or deep slate (dark/system,
  depending on your OS setting) — never pure white or pure black.
- A green "API healthy" badge on the dashboard card, confirming the
  Vite dev proxy is actually reaching the Go API at `localhost:3704`.
  ("API unreachable" instead means the API isn't running — start it
  with `task api-dev`.)
- The theme toggle (top right, sun/moon/monitor icon) switching between
  Light / Dark / System live, no reload needed.
- The sidebar trigger (top left, the panel icon) collapsing the sidebar
  to icons-only and back; hover an icon while collapsed to see its
  tooltip.
- Reload the page after changing the theme or collapsing the sidebar —
  both choices persist.
- Resize the window below ~768px (or open dev tools' device toolbar):
  the sidebar trigger now opens a slide-in drawer instead of a fixed
  column.

`npm run build && npm run lint` should both finish clean with no
errors or warnings.
