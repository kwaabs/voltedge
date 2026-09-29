import { BrowserRouter, Route, Routes } from "react-router"

import { AdminLayout } from "@/components/layout/admin-layout"
import { AppLayout } from "@/components/layout/app-layout"
import { MapLayout } from "@/components/layout/map-layout"
import { RequirePermission, RequireSession } from "@/components/layout/require-session"
import { Toaster } from "@/components/ui/toaster"
import { useThemeEffect } from "@/hooks/use-theme-effect"
import { AdminAssetTagsPage } from "@/routes/admin-asset-tags"
import { AdminCimMappingsPage } from "@/routes/admin-cim-mappings"
import { AdminImportsPage } from "@/routes/admin-imports"
import { AdminHomePage } from "@/routes/admin-home"
import { AdminCircuitsPage } from "@/routes/admin-circuits"
import { AdminDomainsPage } from "@/routes/admin-domains"
import { AdminExternalSystemsPage } from "@/routes/admin-external-systems"
import { AdminFeatureClassDetailPage } from "@/routes/admin-feature-class-detail"
import { AdminFeatureClassNewPage } from "@/routes/admin-feature-class-new"
import { AdminFeatureClassesPage } from "@/routes/admin-feature-classes"
import { AdminDiagramsPage } from "@/routes/admin-diagrams"
import { AdminFieldGroupsPage } from "@/routes/admin-field-groups"
import { AdminMapSettingsPage } from "@/routes/admin-map-settings"
import { AdminWebhooksPage } from "@/routes/admin-webhooks"
import { AdminFieldPacksPage } from "@/routes/admin-field-packs"
import { AdminMaterialsPage } from "@/routes/admin-materials"
import { AdminStencilsPage } from "@/routes/admin-stencils"
import { AdminSymbologyIconsPage } from "@/routes/admin-symbology-icons"
import { AdminTerritoriesPage } from "@/routes/admin-territories"
import { AdminUsersPage } from "@/routes/admin-users"
import { SyncReviewPage } from "@/routes/sync-review"
import { DocsPage } from "@/routes/docs"
import { FeaturesPage } from "@/routes/features"
import { FaultsPage } from "@/routes/faults"
import { ImportRunPage } from "@/routes/imports"
import { LoginPage } from "@/routes/login"
import { MapPage } from "@/routes/map"
import { NotFoundPage } from "@/routes/not-found"
import { OutagesPage } from "@/routes/outages"
import { TagResolvePage } from "@/routes/tag-resolve"
import { WorksPage } from "@/routes/works"

/**
 * Toaster is mounted once here, above every route, not inside
 * map.tsx's own route content -- it was previously only rendered by
 * MapPage, so any pushError/pushWarning call on any other route
 * (/features, /admin/*, ...) silently had nowhere to render, a real
 * bug caught by Playwright-verifying the admin delete rejection toast.
 * It's `position: fixed` (see toaster.tsx), so mounting it here instead
 * of inside either layout has no layout impact -- it just needs to
 * exist exactly once, regardless of which route is active.
 */
export function App() {
  useThemeEffect()

  return (
    <BrowserRouter>
      <Toaster />
      <Routes>
        {/* Public: the only route reachable without a session. */}
        <Route path="/login" element={<LoginPage />} />

        {/* Everything else sits behind the session gate (Slice C gates the
            API to match; RequireSession bounces an unauthenticated browser
            to /login rather than rendering a shell full of 401s). */}
        <Route element={<RequireSession />}>
          <Route element={<MapLayout />}>
            <Route index element={<MapPage />} />
          </Route>
          <Route element={<AppLayout />}>
            <Route path="features" element={<FeaturesPage />} />
            {/* The outage board is gated at the route by oms:read (view); the
                log/predict/lifecycle controls inside self-gate on oms:write, so
                a read-only viewer sees the board without the action buttons. */}
            <Route
              path="outages"
              element={
                <RequirePermission perm="oms:read">
                  <OutagesPage />
                </RequirePermission>
              }
            />
            {/* The work board is gated at the route by wam:read (view); the
                log/create/lifecycle controls inside self-gate on wam:write, so
                a read-only viewer sees the board without the action buttons
                (the same arrangement the outage board uses above). */}
            <Route
              path="works"
              element={
                <RequirePermission perm="wam:read">
                  <WorksPage />
                </RequirePermission>
              }
            />
            {/* The fault board is gated at the route by faults:read (view); the
                log/lifecycle/book-defect controls inside self-gate on
                faults:write, matching the outage and work boards above. */}
            <Route
              path="faults"
              element={
                <RequirePermission perm="faults:read">
                  <FaultsPage />
                </RequirePermission>
              }
            />
            {/* Running an import writes features, so the whole wizard is a
                pure-write surface -- a route-level features:write gate (its nav
                entry is already hidden from a read-only user), matching the
                feature-class create wizard's arrangement. */}
            <Route
              path="imports"
              element={
                <RequirePermission perm="features:write">
                  <ImportRunPage />
                </RequirePermission>
              }
            />
            {/* Asset-tag scan / deep-link landing (S4). A scanned QR points a
                phone at /t/<code>; both this and the bare /t manual-entry form
                need tags:read to hit the resolver, so gate the route (its nav
                entry is already hidden from a caller without it). */}
            <Route
              path="t"
              element={
                <RequirePermission perm="tags:read">
                  <TagResolvePage />
                </RequirePermission>
              }
            />
            <Route
              path="t/:code"
              element={
                <RequirePermission perm="tags:read">
                  <TagResolvePage />
                </RequirePermission>
              }
            />
            {/* The admin landing page -- the single sidebar entry that fans
                out to every admin destination. It self-filters its cards to
                the caller's permissions (see admin-home.tsx), so it needs no
                route-level gate; the individual destinations keep theirs. */}
            <Route path="admin" element={<AdminHomePage />} />
            {/* Every admin sub-page sits under a shared layout that adds a
                "Back to Admin" bar routing to the landing page above; that
                landing page is deliberately left outside this wrapper. */}
            <Route element={<AdminLayout />}>
            {/* The metadata catalog pages all sit behind a metadata:write gate,
                mirroring the backend /admin, /external-systems and
                /webhook-endpoints mounts (which now require metadata:write to
                read, not just to write). A viewer/analyst/editor who deep-links
                one of these URLs gets the clean 403 page instead of a shell that
                loads nothing (every fetch would 403) -- "the API stays the real
                gate", this just makes the client honest about it. Pages with
                their own capability (asset-tags, territories, circuits, users,
                field-packs) keep their own perm and are deliberately left out. */}
            <Route
              path="admin/feature-classes"
              element={
                <RequirePermission perm="metadata:write">
                  <AdminFeatureClassesPage />
                </RequirePermission>
              }
            />
            <Route
              path="admin/feature-classes/new"
              element={
                <RequirePermission perm="metadata:write">
                  <AdminFeatureClassNewPage />
                </RequirePermission>
              }
            />
            <Route
              path="admin/feature-classes/:code"
              element={
                <RequirePermission perm="metadata:write">
                  <AdminFeatureClassDetailPage />
                </RequirePermission>
              }
            />
            <Route
              path="admin/domains"
              element={
                <RequirePermission perm="metadata:write">
                  <AdminDomainsPage />
                </RequirePermission>
              }
            />
            <Route
              path="admin/cim-mappings"
              element={
                <RequirePermission perm="metadata:write">
                  <AdminCimMappingsPage />
                </RequirePermission>
              }
            />
            <Route
              path="admin/asset-tags"
              element={
                <RequirePermission perm="tags:read">
                  <AdminAssetTagsPage />
                </RequirePermission>
              }
            />
            <Route
              path="admin/imports"
              element={
                <RequirePermission perm="metadata:write">
                  <AdminImportsPage />
                </RequirePermission>
              }
            />
            <Route
              path="admin/field-groups"
              element={
                <RequirePermission perm="metadata:write">
                  <AdminFieldGroupsPage />
                </RequirePermission>
              }
            />
            <Route
              path="admin/diagrams"
              element={
                <RequirePermission perm="metadata:write">
                  <AdminDiagramsPage />
                </RequirePermission>
              }
            />
            <Route
              path="admin/materials"
              element={
                <RequirePermission perm="metadata:write">
                  <AdminMaterialsPage />
                </RequirePermission>
              }
            />
            <Route
              path="admin/external-systems"
              element={
                <RequirePermission perm="metadata:write">
                  <AdminExternalSystemsPage />
                </RequirePermission>
              }
            />
            <Route
              path="admin/webhooks"
              element={
                <RequirePermission perm="metadata:write">
                  <AdminWebhooksPage />
                </RequirePermission>
              }
            />
            <Route
              path="admin/symbology-icons"
              element={
                <RequirePermission perm="metadata:write">
                  <AdminSymbologyIconsPage />
                </RequirePermission>
              }
            />
            <Route
              path="admin/stencils"
              element={
                <RequirePermission perm="metadata:write">
                  <AdminStencilsPage />
                </RequirePermission>
              }
            />
            <Route
              path="admin/territories"
              element={
                <RequirePermission perm="territories:write">
                  <AdminTerritoriesPage />
                </RequirePermission>
              }
            />
            <Route
              path="admin/circuits"
              element={
                <RequirePermission perm="circuits:write">
                  <AdminCircuitsPage />
                </RequirePermission>
              }
            />
            <Route
              path="admin/users"
              element={
                <RequirePermission perm="admin:users">
                  <AdminUsersPage />
                </RequirePermission>
              }
            />
            <Route
              path="admin/field-packs"
              element={
                <RequirePermission perm="admin:field-packs">
                  <AdminFieldPacksPage />
                </RequirePermission>
              }
            />
            {/* The default map view is admin config -- a pure-write page (the
                only thing to see is the form you edit), so a route-level
                metadata:write gate, matching its sidebar entry. */}
            <Route
              path="admin/map-settings"
              element={
                <RequirePermission perm="metadata:write">
                  <AdminMapSettingsPage />
                </RequirePermission>
              }
            />
            {/* The review queue is gated at the route by sync:review (view); the
                post/reject/policy-set controls inside self-gate on sync:post, so a
                reviewer who can see but not act gets a read-only queue. */}
            <Route
              path="admin/sync-review"
              element={
                <RequirePermission perm="sync:review">
                  <SyncReviewPage />
                </RequirePermission>
              }
            />
            </Route>
            {/* The documentation viewer is open to every signed-in user; the
                server filters the manual list and refuses restricted guides
                per-document (see api/internal/docs), so no route-level gate. */}
            <Route path="docs" element={<DocsPage />} />
            <Route path="docs/:id" element={<DocsPage />} />
            <Route path="*" element={<NotFoundPage />} />
          </Route>
        </Route>
      </Routes>
    </BrowserRouter>
  )
}
