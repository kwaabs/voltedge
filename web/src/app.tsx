import { BrowserRouter, Route, Routes } from "react-router"

import { AppLayout } from "@/components/layout/app-layout"
import { MapLayout } from "@/components/layout/map-layout"
import { RequirePermission, RequireSession } from "@/components/layout/require-session"
import { Toaster } from "@/components/ui/toaster"
import { useThemeEffect } from "@/hooks/use-theme-effect"
import { AdminCircuitsPage } from "@/routes/admin-circuits"
import { AdminDomainsPage } from "@/routes/admin-domains"
import { AdminExternalSystemsPage } from "@/routes/admin-external-systems"
import { AdminFeatureClassDetailPage } from "@/routes/admin-feature-class-detail"
import { AdminFeatureClassNewPage } from "@/routes/admin-feature-class-new"
import { AdminFeatureClassesPage } from "@/routes/admin-feature-classes"
import { AdminDiagramsPage } from "@/routes/admin-diagrams"
import { AdminFieldGroupsPage } from "@/routes/admin-field-groups"
import { AdminMapSettingsPage } from "@/routes/admin-map-settings"
import { AdminFieldPacksPage } from "@/routes/admin-field-packs"
import { AdminMaterialsPage } from "@/routes/admin-materials"
import { AdminStencilsPage } from "@/routes/admin-stencils"
import { AdminSymbologyIconsPage } from "@/routes/admin-symbology-icons"
import { AdminTerritoriesPage } from "@/routes/admin-territories"
import { AdminUsersPage } from "@/routes/admin-users"
import { SyncReviewPage } from "@/routes/sync-review"
import { ConnectivityPage } from "@/routes/connectivity"
import { FeaturesPage } from "@/routes/features"
import { LoginPage } from "@/routes/login"
import { MapPage } from "@/routes/map"
import { NotFoundPage } from "@/routes/not-found"
import { RelationshipsPage } from "@/routes/relationships"
import { SettingsPage } from "@/routes/settings"

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
            <Route path="connectivity" element={<ConnectivityPage />} />
            <Route path="relationships" element={<RelationshipsPage />} />
            <Route path="admin/feature-classes" element={<AdminFeatureClassesPage />} />
            {/* The create wizard is a pure-write page -- nothing to view --
                so it gets a route-level metadata:write gate rather than
                per-control gating. A metadata:read user reaching it by URL
                (its sidebar/list entry points are already hidden) gets the
                403 page instead of a form every submit would reject. */}
            <Route
              path="admin/feature-classes/new"
              element={
                <RequirePermission perm="metadata:write">
                  <AdminFeatureClassNewPage />
                </RequirePermission>
              }
            />
            <Route path="admin/feature-classes/:code" element={<AdminFeatureClassDetailPage />} />
            <Route path="admin/domains" element={<AdminDomainsPage />} />
            <Route path="admin/field-groups" element={<AdminFieldGroupsPage />} />
            <Route path="admin/diagrams" element={<AdminDiagramsPage />} />
            <Route path="admin/materials" element={<AdminMaterialsPage />} />
            <Route path="admin/external-systems" element={<AdminExternalSystemsPage />} />
            <Route path="admin/symbology-icons" element={<AdminSymbologyIconsPage />} />
            <Route path="admin/stencils" element={<AdminStencilsPage />} />
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
            <Route path="settings" element={<SettingsPage />} />
            <Route path="*" element={<NotFoundPage />} />
          </Route>
        </Route>
      </Routes>
    </BrowserRouter>
  )
}
