import type { ReactNode } from "react"
import { Loader2 } from "lucide-react"
import { Navigate, Outlet, useLocation } from "react-router"

import { ForbiddenPage } from "@/routes/forbidden"
import { Button } from "@/components/ui/button"
import { isUnauthenticated, usePermission, useSession } from "@/hooks/use-auth"

/**
 * The trust gate for the whole app shell. Every route except /login sits
 * behind this: Slice C gates every API route, so an unauthenticated
 * browser would otherwise just render a shell full of 401s. Instead we
 * resolve GET /auth/me once here and:
 *
 *   - loading           -> a centered spinner (no flash of the shell)
 *   - 401 (no session)  -> redirect to /login, remembering where we were
 *                          so login can send us back
 *   - other error       -> a real fault (server/network), shown as such --
 *                          NOT a redirect, since that would hide an outage
 *                          behind an infinite login bounce
 *   - session present   -> render the app (Outlet)
 */
export function RequireSession() {
  const location = useLocation()
  const { data, isPending, isError, error, refetch } = useSession()

  if (isPending) {
    return (
      <div className="flex min-h-screen items-center justify-center">
        <Loader2 className="text-muted-foreground size-6 animate-spin" />
      </div>
    )
  }

  if (isError) {
    if (isUnauthenticated(error)) {
      return <Navigate to="/login" replace state={{ from: location.pathname + location.search }} />
    }
    return (
      <div className="flex min-h-screen flex-col items-center justify-center gap-3 text-center">
        <h1 className="text-lg font-semibold">Can't reach the server</h1>
        <p className="text-muted-foreground max-w-sm text-sm">
          The session couldn't be verified. This is usually a temporary connection problem, not a sign-in issue.
        </p>
        <Button variant="outline" size="sm" onClick={() => refetch()}>
          Try again
        </Button>
      </div>
    )
  }

  if (!data) {
    return <Navigate to="/login" replace state={{ from: location.pathname + location.search }} />
  }

  return <Outlet />
}

/**
 * Route-level RBAC gate. Renders its children only if the current session
 * holds `perm`; otherwise shows the 403 page in place. Used for
 * permission-scoped pages (e.g. user management behind admin:users) so the
 * client mirrors what the server would enforce -- the API stays the real
 * gate, this just avoids sending the user into a page that would only 403.
 * This sits inside RequireSession, so a session is guaranteed here.
 */
export function RequirePermission({ perm, children }: { perm: string; children: ReactNode }) {
  const can = usePermission()
  if (!can(perm)) return <ForbiddenPage />
  return <>{children}</>
}
