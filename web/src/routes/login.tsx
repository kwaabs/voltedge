import { type FormEvent, useState } from "react"
import { Loader2, LogIn, Zap } from "lucide-react"
import { Navigate, useLocation, useSearchParams } from "react-router"

import { inputClass } from "@/components/map/field-utils"
import { Button } from "@/components/ui/button"
import { type AuthProvider, useLogin, useProviders, useSession } from "@/hooks/use-auth"

/**
 * The SSO login endpoints are plain, top-level navigations to our own
 * server -- never a client-side SDK (no MSAL.js / gapi). The button is an
 * anchor to /api/auth/oidc/{provider}/login, which 302s to the IdP; the
 * whole OAuth/OIDC flow stays server-side (see docs/phase-10-auth.md).
 */
function providerHref(key: string): string {
  return `/api/auth/oidc/${encodeURIComponent(key)}/login`
}

/** Brand mark per provider, falling back to a generic key icon. */
function ProviderIcon({ providerKey }: { providerKey: string }) {
  if (providerKey === "azuread") {
    return (
      <svg viewBox="0 0 23 23" className="size-4" aria-hidden>
        <path fill="#f35325" d="M1 1h10v10H1z" />
        <path fill="#81bc06" d="M12 1h10v10H12z" />
        <path fill="#05a6f0" d="M1 12h10v10H1z" />
        <path fill="#ffba08" d="M12 12h10v10H12z" />
      </svg>
    )
  }
  if (providerKey === "google") {
    return (
      <svg viewBox="0 0 48 48" className="size-4" aria-hidden>
        <path fill="#ea4335" d="M24 9.5c3.5 0 6.6 1.2 9 3.6l6.7-6.7C35.6 2.4 30.2 0 24 0 14.6 0 6.4 5.4 2.5 13.3l7.9 6.1C12.2 13.7 17.6 9.5 24 9.5z" />
        <path fill="#4285f4" d="M46.5 24.5c0-1.6-.1-3.1-.4-4.5H24v9h12.7c-.6 3-2.3 5.5-4.8 7.2l7.4 5.7C43.9 37.6 46.5 31.6 46.5 24.5z" />
        <path fill="#fbbc05" d="M10.4 28.4c-.5-1.5-.8-3.1-.8-4.9s.3-3.4.8-4.9l-7.9-6.1C.9 16 0 19.9 0 24s.9 8 2.5 11.5l7.9-7.1z" />
        <path fill="#34a853" d="M24 48c6.5 0 11.9-2.1 15.9-5.8l-7.4-5.7c-2.1 1.4-4.7 2.2-8.5 2.2-6.4 0-11.8-4.2-13.6-9.9l-7.9 6.1C6.4 42.6 14.6 48 24 48z" />
      </svg>
    )
  }
  return <LogIn className="size-4" />
}

/** Maps the ?error reasons the SSO callback redirects with to a message. */
const SSO_ERRORS: Record<string, string> = {
  unknown_provider: "That sign-in provider isn't configured.",
  expired: "The sign-in attempt expired. Please try again.",
  state: "The sign-in attempt couldn't be verified. Please try again.",
  sso: "Single sign-on failed. Please try again.",
  denied: "Your account isn't allowed to sign in this way.",
  no_account: "No VoltEdge account matches that identity. Ask an administrator to create one first.",
}

export function LoginPage() {
  const location = useLocation()
  const [searchParams] = useSearchParams()
  const { data: session } = useSession()
  const { data: providers, isPending: providersPending } = useProviders()
  const login = useLogin()

  const [email, setEmail] = useState("")
  const [password, setPassword] = useState("")

  // Where to land after a successful sign-in: back to wherever the guard
  // bounced us from, or the map.
  const from = (location.state as { from?: string } | null)?.from ?? "/"

  // Already signed in (e.g. navigated to /login directly) -> straight in.
  if (session) return <Navigate to={from} replace />

  const ssoError = searchParams.get("error")
  const ssoMessage = ssoError ? (SSO_ERRORS[ssoError] ?? "Sign-in failed. Please try again.") : null

  const canSubmit = email.trim().length > 0 && password.length > 0 && !login.isPending

  function handleSubmit(e: FormEvent) {
    e.preventDefault()
    if (!canSubmit) return
    login.mutate({ email: email.trim(), password })
  }

  const ssoProviders: AuthProvider[] = providers?.providers ?? []

  return (
    <div className="bg-muted/30 flex min-h-screen items-center justify-center p-4">
      <div className="bg-background w-full max-w-sm rounded-xl border p-6 shadow-sm">
        <div className="mb-6 flex flex-col items-center gap-2 text-center">
          <div className="bg-primary text-primary-foreground flex size-10 items-center justify-center rounded-lg">
            <Zap className="size-5" />
          </div>
          <h1 className="text-lg font-semibold">Sign in to VoltEdge</h1>
          <p className="text-muted-foreground text-sm">Use your organization account.</p>
        </div>

        {ssoMessage && (
          <p className="border-destructive/40 bg-destructive/10 text-destructive mb-4 rounded-md border px-3 py-2 text-sm">
            {ssoMessage}
          </p>
        )}

        {ssoProviders.length > 0 && (
          <div className="mb-4 flex flex-col gap-2">
            {ssoProviders.map((p) => (
              <Button key={p.key} asChild variant="outline" className="w-full justify-center gap-2">
                <a href={providerHref(p.key)}>
                  <ProviderIcon providerKey={p.key} />
                  Sign in with {p.display_name}
                </a>
              </Button>
            ))}
          </div>
        )}

        {ssoProviders.length > 0 && (
          <div className="text-muted-foreground my-4 flex items-center gap-3 text-xs">
            <span className="bg-border h-px flex-1" />
            or
            <span className="bg-border h-px flex-1" />
          </div>
        )}

        <form onSubmit={handleSubmit} className="flex flex-col gap-3">
          <div className="flex flex-col gap-1">
            <label className="text-xs font-medium" htmlFor="login-email">
              Email
            </label>
            <input
              id="login-email"
              type="email"
              autoComplete="username"
              className={inputClass}
              value={email}
              onChange={(e) => setEmail(e.target.value)}
              placeholder="you@yourutility.com"
            />
          </div>
          <div className="flex flex-col gap-1">
            <label className="text-xs font-medium" htmlFor="login-password">
              Password
            </label>
            <input
              id="login-password"
              type="password"
              autoComplete="current-password"
              className={inputClass}
              value={password}
              onChange={(e) => setPassword(e.target.value)}
            />
          </div>

          {login.isError && (
            <p className="text-destructive text-sm">{login.error.message}</p>
          )}

          <Button type="submit" className="mt-1 w-full" disabled={!canSubmit}>
            {login.isPending && <Loader2 className="size-4 animate-spin" />}
            Sign in
          </Button>
        </form>

        {!providersPending && providers && !providers.local_auth_enabled && (
          <p className="text-muted-foreground mt-4 text-center text-xs">
            Password sign-in is currently restricted to break-glass accounts. Use SSO, or contact an administrator.
          </p>
        )}
      </div>
    </div>
  )
}
