import type { ReactNode } from "react"
import { Loader2, RefreshCw, ServerCrash } from "lucide-react"
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
  const { data, isPending, isError, error, refetch, isFetching } = useSession()

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
      <div className="flex min-h-screen flex-col items-center justify-center gap-6 px-6 text-center">
        {/* An animated "reaching for the server" mark: radar rings ping
            outward from a gently drifting server icon -- a live, hopeful
            picture of the client trying to re-establish contact, rather than a
            dead error glyph. Decorative, so hidden from assistive tech; the
            heading below carries the meaning. Motion collapses to nothing under
            prefers-reduced-motion (see index.css). */}
        <div className="relative flex size-28 items-center justify-center" aria-hidden="true">
          <span className="border-primary/40 absolute size-16 animate-ping rounded-full border [animation-duration:2.4s]" />
          <span className="border-primary/30 absolute size-16 animate-ping rounded-full border [animation-delay:0.8s] [animation-duration:2.4s]" />
          <span className="border-primary/20 absolute size-16 animate-ping rounded-full border [animation-delay:1.6s] [animation-duration:2.4s]" />
          <span className="bg-card relative flex size-16 animate-float items-center justify-center rounded-full border shadow-sm">
            <ServerCrash className="text-muted-foreground size-7" />
          </span>
        </div>

        <div className="flex flex-col items-center gap-3">
          <h1 className="animate-fade-up text-lg font-semibold [animation-delay:0.05s]">Can't reach the server</h1>
          <p className="text-muted-foreground animate-fade-up max-w-sm text-sm [animation-delay:0.15s]">
            The session couldn't be verified. This is usually a temporary connection problem, not a sign-in issue.
          </p>
          <Button
            variant="outline"
            size="sm"
            onClick={() => refetch()}
            disabled={isFetching}
            className="animate-fade-up mt-1 [animation-delay:0.25s]"
          >
            {isFetching ? (
              <Loader2 className="size-4 animate-spin" />
            ) : (
              <RefreshCw className="size-4" />
            )}
            {isFetching ? "Retrying…" : "Try again"}
          </Button>
        </div>
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
