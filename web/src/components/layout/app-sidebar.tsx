import { Cloud, Database, Inbox, LandPlot, Layers, Link2, Map, MapPin, Package, Rows3, Settings, Shapes, Stamp, Tags, UsersRound, Waypoints, Workflow, Wrench, Zap } from "lucide-react"
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
import { usePermission } from "@/hooks/use-auth"

/**
 * The map is the app's home surface -- everything else here is a
 * secondary, less-frequently-used view (bulk browse/inspect, not the
 * primary GIS canvas), grouped separately and visually demoted rather
 * than competing with it. See docs/phase-3-map-loop.md's IA notes.
 */
const dataItems = [
  { to: "/features", label: "Feature classes", icon: Database, perm: "features:read" },
  { to: "/connectivity", label: "Connectivity", icon: Waypoints, perm: "connectivity:read" },
  { to: "/relationships", label: "Relationships", icon: Link2, perm: "relationships:read" },
]

/**
 * Metadata configuration -- authoring what a feature class *is*
 * (subtypes, fields, domains, field groups), distinct from `dataItems`
 * above, which browse/edit real feature *data*. See
 * docs/phase-6-admin-metadata.md for why this is its own section: the
 * first real write path into metadata.* (every table it touches was
 * seed-SQL/migration-only before this), deliberately scoped to
 * additive-only operations the schema compiler already supports safely.
 */
const adminItems = [
  { to: "/admin/feature-classes", label: "Feature classes", icon: Layers, perm: "metadata:read" },
  { to: "/admin/domains", label: "Domains", icon: Tags, perm: "metadata:read" },
  { to: "/admin/field-groups", label: "Field groups", icon: Rows3, perm: "metadata:read" },
  { to: "/admin/diagrams", label: "Diagram rules", icon: Workflow, perm: "metadata:read" },
  { to: "/admin/materials", label: "Materials", icon: Wrench, perm: "metadata:read" },
  { to: "/admin/external-systems", label: "External systems", icon: Cloud, perm: "metadata:read" },
  { to: "/admin/symbology-icons", label: "Symbology icons", icon: Shapes, perm: "metadata:read" },
  { to: "/admin/stencils", label: "Stencil promotion", icon: Stamp, perm: "metadata:read" },
  { to: "/admin/territories", label: "Territories", icon: LandPlot, perm: "territories:write" },
  { to: "/admin/circuits", label: "Circuits", icon: Zap, perm: "circuits:write" },
  { to: "/admin/field-packs", label: "Field packs", icon: Package, perm: "admin:field-packs" },
  { to: "/admin/sync-review", label: "Sync review", icon: Inbox, perm: "sync:review" },
  { to: "/admin/map-settings", label: "Map settings", icon: MapPin, perm: "metadata:write" },
  { to: "/admin/users", label: "Users", icon: UsersRound, perm: "admin:users" },
]

export function AppSidebar() {
  const { state } = useSidebar()
  const { pathname } = useLocation()
  const can = usePermission()

  // Only surface a link the caller could actually use -- Slice C would 403
  // the rest, so hiding them keeps the nav honest rather than offering dead
  // ends. The API remains the real gate; this just mirrors it.
  const visibleData = dataItems.filter((item) => can(item.perm))
  const visibleAdmin = adminItems.filter((item) => can(item.perm))

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

        {visibleAdmin.length > 0 && (
          <SidebarGroup>
            <SidebarGroupLabel>Admin</SidebarGroupLabel>
            <SidebarGroupContent>
              <SidebarMenu>
                {visibleAdmin.map((item) => {
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
