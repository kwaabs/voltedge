import { BookOpen, Database, FileUp, Map, QrCode, Siren, Wrench, Zap, ZapOff } from "lucide-react"
import { NavLink, useLocation } from "react-router"

import { AdminNavIcon, adminNavItems } from "@/components/layout/admin-nav"
import {
  Sidebar,
  SidebarContent,
  SidebarGroup,
  SidebarGroupContent,
  SidebarGroupLabel,
  SidebarHeader,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
  useSidebar,
} from "@/components/ui/sidebar"
import { usePermission } from "@/hooks/use-auth"

/**
 * The map is the app's home surface -- everything else here is a
 * secondary, less-frequently-used view (bulk browse/inspect, not the
 * primary GIS canvas), grouped separately and visually demoted rather
 * than competing with it. See docs/phase-3-map-loop.md's IA notes.
 */
const dataItems = [
  { to: "/features", label: "Feature classes", icon: Database, perm: "features:read" },
  { to: "/imports", label: "Import data", icon: FileUp, perm: "features:write" },
  { to: "/outages", label: "Outages", icon: Siren, perm: "oms:read" },
  { to: "/faults", label: "Faults", icon: ZapOff, perm: "faults:read" },
  { to: "/works", label: "Works", icon: Wrench, perm: "wam:read" },
  { to: "/t", label: "Resolve tag", icon: QrCode, perm: "tags:read" },
]

export function AppSidebar() {
  const { state } = useSidebar()
  const { pathname } = useLocation()
  const can = usePermission()

  // Only surface a link the caller could actually use -- Slice C would 403
  // the rest, so hiding them keeps the nav honest rather than offering dead
  // ends. The API remains the real gate; this just mirrors it.
  const visibleData = dataItems.filter((item) => can(item.perm))
  // The admin menu now lives on its own /admin landing page (it had grown to
  // fifteen entries -- too many for the sidebar). Show the single entry point
  // whenever the caller can reach at least one admin destination.
  const canAdmin = adminNavItems.some((item) => can(item.perm))

  return (
    <Sidebar>
      <SidebarHeader>
        <div className="flex h-9 items-center gap-2 px-2">
          <div className="bg-primary text-primary-foreground flex size-6 shrink-0 items-center justify-center rounded-md">
            <Zap className="size-4" />
          </div>
          {state === "expanded" && (
            <span className="truncate font-semibold tracking-tight">VoltEdge</span>
          )}
        </div>
      </SidebarHeader>

      <SidebarContent>
        <SidebarGroup>
          <SidebarGroupContent>
            <SidebarMenu>
              <SidebarMenuItem>
                <SidebarMenuButton asChild isActive={pathname === "/"} tooltip="Map">
                  <NavLink to="/" end>
                    <Map />
                    <span>Map</span>
                  </NavLink>
                </SidebarMenuButton>
              </SidebarMenuItem>
            </SidebarMenu>
          </SidebarGroupContent>
        </SidebarGroup>

        {visibleData.length > 0 && (
          <SidebarGroup>
            <SidebarGroupLabel>Data</SidebarGroupLabel>
            <SidebarGroupContent>
              <SidebarMenu>
                {visibleData.map((item) => {
                  const isActive = pathname.startsWith(item.to)
                  return (
                    <SidebarMenuItem key={item.to}>
                      <SidebarMenuButton asChild isActive={isActive} tooltip={item.label}>
                        <NavLink to={item.to}>
                          <item.icon />
                          <span>{item.label}</span>
                        </NavLink>
                      </SidebarMenuButton>
                    </SidebarMenuItem>
                  )
                })}
              </SidebarMenu>
            </SidebarGroupContent>
          </SidebarGroup>
        )}

        {canAdmin && (
          <SidebarGroup>
            <SidebarGroupContent>
              <SidebarMenu>
                <SidebarMenuItem>
                  <SidebarMenuButton
                    asChild
                    isActive={pathname === "/admin" || pathname.startsWith("/admin/")}
                    tooltip="Admin"
                  >
                    <NavLink to="/admin">
                      <AdminNavIcon />
                      <span>Admin</span>
                    </NavLink>
                  </SidebarMenuButton>
                </SidebarMenuItem>
              </SidebarMenu>
            </SidebarGroupContent>
          </SidebarGroup>
        )}

        <SidebarGroup className="mt-auto">
          <SidebarGroupContent>
            <SidebarMenu>
              {/* Documentation is open to every signed-in user; the server
                  filters the manual list, hiding the administrative guides
                  from a caller without the permission (see api/internal/docs),
                  so no route-level gate is needed here. */}
              <SidebarMenuItem>
                <SidebarMenuButton
                  asChild
                  isActive={pathname === "/docs" || pathname.startsWith("/docs/")}
                  tooltip="Documentation"
                >
                  <NavLink to="/docs">
                    <BookOpen />
                    <span>Documentation</span>
                  </NavLink>
                </SidebarMenuButton>
              </SidebarMenuItem>
            </SidebarMenu>
          </SidebarGroupContent>
        </SidebarGroup>
      </SidebarContent>
    </Sidebar>
  )
}
