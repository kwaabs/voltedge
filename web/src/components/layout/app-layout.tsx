import { Outlet } from "react-router"

import { AppHeader } from "@/components/layout/app-header"
import { AppSidebar } from "@/components/layout/app-sidebar"
import { SidebarInset, SidebarProvider } from "@/components/ui/sidebar"

/**
 * No padding here, deliberately -- the map route needs to fill this
 * area edge to edge. Non-map pages apply their own padding internally
 * (see PlaceholderPage) instead of relying on a wrapper every route
 * shares, since exactly one route (the map) needs to opt out of it.
 */
export function AppLayout() {
  return (
    <SidebarProvider>
      <AppSidebar />
      <SidebarInset>
        <AppHeader />
        <div className="min-h-0 flex-1 overflow-hidden">
          <Outlet />
        </div>
      </SidebarInset>
    </SidebarProvider>
  )
}
