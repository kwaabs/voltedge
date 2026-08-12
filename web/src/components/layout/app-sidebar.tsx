import { Database, Link2, Map, Settings, Waypoints, Zap } from "lucide-react"
import { NavLink, useLocation } from "react-router"

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

/**
 * The map is the app's home surface -- everything else here is a
 * secondary, less-frequently-used view (bulk browse/inspect, not the
 * primary GIS canvas), grouped separately and visually demoted rather
 * than competing with it. See docs/phase-3-map-loop.md's IA notes.
 */
const dataItems = [
  { to: "/features", label: "Feature classes", icon: Database },
  { to: "/connectivity", label: "Connectivity", icon: Waypoints },
  { to: "/relationships", label: "Relationships", icon: Link2 },
]

export function AppSidebar() {
  const { state } = useSidebar()
  const { pathname } = useLocation()

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

        <SidebarGroup>
          <SidebarGroupLabel>Data</SidebarGroupLabel>
          <SidebarGroupContent>
            <SidebarMenu>
              {dataItems.map((item) => {
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

        <SidebarGroup className="mt-auto">
          <SidebarGroupContent>
            <SidebarMenu>
              <SidebarMenuItem>
                <SidebarMenuButton asChild isActive={pathname === "/settings"} tooltip="Settings">
                  <NavLink to="/settings">
                    <Settings />
                    <span>Settings</span>
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
