import { Outlet } from "react-router"

import { AppHeader } from "@/components/layout/app-header"
import { AppSidebar } from "@/components/layout/app-sidebar"
import { SidebarProvider } from "@/components/ui/sidebar"

/**
 * The map route's own layout -- deliberately not AppLayout. A docked
 * sidebar permanently reserving map width was the wrong pattern (a
 * map-first app keeps the canvas edge to edge; navigation floats over
 * it, it doesn't push it aside). SidebarProvider's `overlay` prop
 * forces AppSidebar to always render as a Sheet drawer instead of a
 * docked column, so the exact same nav content and component get
 * reused here -- there's no second, parallel nav UI to maintain, just
 * a different mount context for the same one. See
 * docs/phase-3-map-loop.md.
 */
export function MapLayout() {
  return (
    <SidebarProvider overlay>
      <AppSidebar />
      <div className="flex h-svh w-full flex-col">
        <AppHeader />
        <div className="min-h-0 flex-1">
          <Outlet />
        </div>
      </div>
    </SidebarProvider>
  )
}
