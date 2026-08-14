import { BrowserRouter, Route, Routes } from "react-router"

import { AppLayout } from "@/components/layout/app-layout"
import { MapLayout } from "@/components/layout/map-layout"
import { useThemeEffect } from "@/hooks/use-theme-effect"
import { AdminDomainsPage } from "@/routes/admin-domains"
import { AdminFeatureClassNewPage } from "@/routes/admin-feature-class-new"
import { AdminFeatureClassesPage } from "@/routes/admin-feature-classes"
import { AdminFieldGroupsPage } from "@/routes/admin-field-groups"
import { ConnectivityPage } from "@/routes/connectivity"
import { FeaturesPage } from "@/routes/features"
import { MapPage } from "@/routes/map"
import { NotFoundPage } from "@/routes/not-found"
import { RelationshipsPage } from "@/routes/relationships"
import { SettingsPage } from "@/routes/settings"

export function App() {
  useThemeEffect()

  return (
    <BrowserRouter>
      <Routes>
        <Route element={<MapLayout />}>
          <Route index element={<MapPage />} />
        </Route>
        <Route element={<AppLayout />}>
          <Route path="features" element={<FeaturesPage />} />
          <Route path="connectivity" element={<ConnectivityPage />} />
          <Route path="relationships" element={<RelationshipsPage />} />
          <Route path="admin/feature-classes" element={<AdminFeatureClassesPage />} />
          <Route path="admin/feature-classes/new" element={<AdminFeatureClassNewPage />} />
          <Route path="admin/domains" element={<AdminDomainsPage />} />
          <Route path="admin/field-groups" element={<AdminFieldGroupsPage />} />
          <Route path="settings" element={<SettingsPage />} />
          <Route path="*" element={<NotFoundPage />} />
        </Route>
      </Routes>
    </BrowserRouter>
  )
}
