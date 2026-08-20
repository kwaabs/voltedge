import { ShieldX } from "lucide-react"
import { Link } from "react-router"

import { Button } from "@/components/ui/button"

/**
 * The 403 surface. Slice C's RBAC middleware returns 403 when a signed-in
 * user lacks the permission a route requires; RequirePermission (used on
 * permission-scoped routes) renders this instead of the page. Distinct
 * from 404 on purpose: the resource exists, the user just isn't allowed --
 * so we point at contacting an administrator, not at a wrong URL.
 */
export function ForbiddenPage() {
  return (
    <div className="flex flex-1 flex-col items-center justify-center gap-3 text-center">
      <ShieldX className="text-muted-foreground size-8" />
      <p className="text-muted-foreground text-sm font-medium">403</p>
      <h1 className="text-xl font-semibold">You don't have access</h1>
      <p className="text-muted-foreground max-w-sm text-sm">
        Your account isn't authorized for this. If you think it should be, ask an administrator to grant the role.
      </p>
      <Button asChild variant="outline" size="sm" className="mt-2">
        <Link to="/">Back to the map</Link>
      </Button>
    </div>
  )
}
