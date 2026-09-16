import { ArrowLeft } from "lucide-react"
import { NavLink, Outlet } from "react-router"

/**
 * Chrome shared by every admin sub-page: a slim "Back to Admin" bar above the
 * page content, so any admin destination has a one-click route back to the
 * /admin landing page. Wraps all admin/* routes except the landing page
 * itself (see app.tsx). The bar is shrink-0 and the Outlet fills the rest, so
 * a child page's own `h-full overflow-y-auto` still scrolls within the area
 * below the bar rather than pushing it off-screen.
 */
export function AdminLayout() {
  return (
    <div className="flex h-full flex-col">
      <div className="flex h-10 shrink-0 items-center border-b px-4">
        <NavLink
          to="/admin"
          className="text-muted-foreground hover:text-foreground flex items-center gap-1.5 text-sm font-medium transition-colors"
        >
          <ArrowLeft className="size-4" />
          Back to Admin
        </NavLink>
      </div>
      <div className="min-h-0 flex-1">
        <Outlet />
      </div>
    </div>
  )
}
