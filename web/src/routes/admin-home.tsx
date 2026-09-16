import { NavLink } from "react-router"

import { filterAdminNav } from "@/components/layout/admin-nav"
import { Card, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { usePermission } from "@/hooks/use-auth"

/**
 * The admin landing page. The admin menu had grown to fifteen entries --
 * enough that stacking them all in the sidebar crowded out everything else --
 * so the sidebar now carries a single "Admin" link here, and every
 * destination lives on this page as a card. Groups and per-item permission
 * gating come from the shared admin-nav module, so this stays in lockstep
 * with the sidebar and the API's own gates (an item the caller can't use is
 * hidden, never a dead end).
 */
export function AdminHomePage() {
  const can = usePermission()
  const groups = filterAdminNav(can)

  return (
    <div className="h-full overflow-y-auto p-4">
      <div className="mx-auto flex max-w-5xl flex-col gap-6">
        <div>
          <h1 className="text-lg font-semibold">Administration</h1>
          <p className="text-muted-foreground text-sm">
            Configure metadata, symbology, access and field operations. Only the areas you have permission for are
            shown.
          </p>
        </div>

        {groups.length === 0 ? (
          <p className="text-muted-foreground text-sm">You don't have access to any admin areas.</p>
        ) : (
          groups.map((group) => (
            <section key={group.label} className="flex flex-col gap-3">
              <h2 className="text-muted-foreground text-xs font-medium tracking-wide uppercase">{group.label}</h2>
              <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3">
                {group.items.map((item) => (
                  <NavLink key={item.to} to={item.to} className="group focus-visible:outline-none">
                    <Card className="hover:border-primary/50 hover:bg-accent/40 group-focus-visible:ring-ring gap-3 py-4 transition-colors group-focus-visible:ring-2">
                      <CardHeader className="gap-1.5">
                        <CardTitle className="flex items-center gap-2 text-sm">
                          <span className="bg-primary/10 text-primary flex size-7 shrink-0 items-center justify-center rounded-md">
                            <item.icon className="size-4" />
                          </span>
                          {item.label}
                        </CardTitle>
                        <CardDescription className="text-xs">{item.description}</CardDescription>
                      </CardHeader>
                    </Card>
                  </NavLink>
                ))}
              </div>
            </section>
          ))
        )}
      </div>
    </div>
  )
}
