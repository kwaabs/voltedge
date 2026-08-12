import { BrowserRouter, Route, Routes } from "react-router"

import { AppLayout } from "@/components/layout/app-layout"
import { useThemeEffect } from "@/hooks/use-theme-effect"
import { ConnectivityPage } from "@/routes/connectivity"
import { DashboardPage } from "@/routes/dashboard"
import { FeaturesPage } from "@/routes/features"
import { NotFoundPage } from "@/routes/not-found"
import { RelationshipsPage } from "@/routes/relationships"
import { SettingsPage } from "@/routes/settings"

export function App() {
  useThemeEffect()

  return (
    <BrowserRouter>
      <Routes>
        <Route element={<AppLayout />}>
          <Route index element={<DashboardPage />} />
          <Route path="features" element={<FeaturesPage />} />
          <Route path="connectivity" element={<ConnectivityPage />} />
          <Route path="relationships" element={<RelationshipsPage />} />
          <Route path="settings" element={<SettingsPage />} />
          <Route path="*" element={<NotFoundPage />} />
        </Route>
      </Routes>
    </BrowserRouter>
  )
}
