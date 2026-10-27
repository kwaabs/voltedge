import { type FormEvent, useEffect, useRef, useState } from "react"
import { Activity, Loader2, LogIn, Map, Radio, Zap } from "lucide-react"
import { Navigate, useLocation, useSearchParams } from "react-router"

import { inputClass } from "@/components/map/field-utils"
import { Button } from "@/components/ui/button"
import { type AuthProvider, useLogin, useProviders, useRevealBreakGlass, useSession } from "@/hooks/use-auth"

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
  const reveal = useRevealBreakGlass()

  const [email, setEmail] = useState("")
  const [password, setPassword] = useState("")
  // Break-glass reveal code (soft second factor): when required, the password
  // form stays hidden until `code` is entered and the server OKs it (`revealed`).
  const [code, setCode] = useState("")
  const [revealed, setRevealed] = useState(false)
  // The break-glass code box is NOT advertised: when it's required, the screen
  // looks SSO-only until an admin performs the known gesture (three taps on the
  // brand mark), which flips `codeUnlocked`. Security still rests on the
  // rate-limited server check + the password that's also required -- this just
  // stops the emergency door being painted a target for drive-by guessing.
  const [codeUnlocked, setCodeUnlocked] = useState(false)
  const logoTaps = useRef(0)
  const logoTimer = useRef<ReturnType<typeof setTimeout> | null>(null)
  // Guesses since the box was revealed; 3 wrong re-conceals (a speed bump on
  // top of the server rate limit). A single typo just clears the field.
  const wrongTries = useRef(0)

  // Where to land after a successful sign-in: back to wherever the guard
  // bounced us from, or the map.
  const from = (location.state as { from?: string } | null)?.from ?? "/"

  // Already signed in (e.g. navigated to /login directly) -> straight in.
  if (session) return <Navigate to={from} replace />

  const ssoError = searchParams.get("error")
  const ssoMessage = ssoError ? (SSO_ERRORS[ssoError] ?? "Sign-in failed. Please try again.") : null

  const codeRequired = providers?.break_glass_code_required ?? false
  const showPasswordForm = !codeRequired || revealed

  // Three taps on the brand mark (within ~1.2s) reveal the gated code box. A
  // no-op once the form is already shown (or no code is required), so an
  // ordinary visitor never surfaces anything by fidgeting with the logo.
  function handleLogoTap() {
    if (!codeRequired || revealed || codeUnlocked) return
    logoTaps.current += 1
    if (logoTimer.current) clearTimeout(logoTimer.current)
    if (logoTaps.current >= 3) {
      logoTaps.current = 0
      setCodeUnlocked(true)
      return
    }
    logoTimer.current = setTimeout(() => {
      logoTaps.current = 0
    }, 1200)
  }

  const canSubmit = email.trim().length > 0 && password.length > 0 && !login.isPending

  function handleSubmit(e: FormEvent) {
    e.preventDefault()
    if (!canSubmit) return
    // When a reveal code gated this form, it must also ride the login (the
    // server re-verifies it -- the password alone isn't enough).
    login.mutate({ email: email.trim(), password, break_glass_code: codeRequired ? code.trim() : undefined })
  }

  // Re-hide the code box and reset the gesture -- on idle, or after too many
  // wrong codes. Back to the plain SSO-only screen; the gesture must be redone.
  function conceal() {
    setCodeUnlocked(false)
    setRevealed(false)
    setCode("")
    logoTaps.current = 0
    wrongTries.current = 0
    reveal.reset() // drop any stale "Invalid code." so a fresh gesture is clean
  }

  // Once the box is open, auto-conceal after 5 min of no typing. The effect
  // re-arms on every keystroke in any field (code/email/password all in deps),
  // so an admin actively signing in is never cut off; a walked-away screen is.
  useEffect(() => {
    if (!codeUnlocked) return
    const t = setTimeout(conceal, 5 * 60 * 1000)
    return () => clearTimeout(t)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [codeUnlocked, revealed, code, email, password])

  function handleReveal(e: FormEvent) {
    e.preventDefault()
    if (code.trim().length === 0 || reveal.isPending) return
    reveal.mutate(
      { code: code.trim() },
      {
        onSuccess: () => {
          wrongTries.current = 0
          setRevealed(true)
        },
        onError: () => {
          // Clear the field so the next attempt starts clean; after 3 wrong
          // the box collapses and the gesture must be redone.
          wrongTries.current += 1
          setCode("")
          if (wrongTries.current >= 3) conceal()
        },
      },
    )
  }

  const ssoProviders: AuthProvider[] = providers?.providers ?? []

  return (
    <div className="flex min-h-screen w-full">
      {/* Left: branded hero. Its own blue/copper surface (fixed, not theme-tinted)
          so the login always reads as the product, with soft glows + a faint grid
          for flair. Hidden below lg -- the form carries a compact brand header there. */}
      <div className="relative hidden flex-1 flex-col justify-between overflow-hidden bg-gradient-to-br from-[oklch(0.34_0.09_256)] via-[oklch(0.44_0.11_252)] to-[oklch(0.40_0.13_270)] p-12 text-white lg:flex">
        <div aria-hidden className="pointer-events-none absolute inset-0">
          {/* copper glow + cool glow */}
          <div className="bg-primary/30 absolute -top-24 -left-24 size-96 rounded-full blur-3xl" />
          <div className="absolute right-0 bottom-0 size-[28rem] rounded-full bg-sky-300/10 blur-3xl" />
          {/* faint engineering grid */}
          <div
            className="absolute inset-0 opacity-[0.06]"
            style={{
              backgroundImage:
                "linear-gradient(to right, white 1px, transparent 1px), linear-gradient(to bottom, white 1px, transparent 1px)",
              backgroundSize: "44px 44px",
            }}
          />
        </div>

        <div className="relative flex items-center gap-3">
          <span
            onClick={handleLogoTap}
            className="bg-primary text-primary-foreground flex size-11 cursor-default items-center justify-center rounded-xl shadow-lg shadow-black/25 select-none"
          >
            <Zap className="size-6" />
          </span>
          <span className="text-xl font-semibold tracking-tight">VoltEdge</span>
        </div>

        <div className="relative max-w-md">
          <h2 className="text-4xl leading-tight font-semibold tracking-tight">
            The network,
            <br />
            end to end.
          </h2>
          <p className="mt-4 text-base text-white/70">
            Map, trace, and operate your distribution grid — from the substation down to the service point.
          </p>
          <ul className="mt-8 flex flex-col gap-3 text-sm text-white/85">
            {[
              { icon: Map, label: "Live connectivity, tracing & load flow" },
              { icon: Activity, label: "Outage management & switching" },
              { icon: Radio, label: "Field-ready work packs & SCADA/AMI signals" },
            ].map(({ icon: Icon, label }) => (
              <li key={label} className="flex items-center gap-3">
                <span className="flex size-7 shrink-0 items-center justify-center rounded-lg bg-white/10 ring-1 ring-white/15">
                  <Icon className="size-3.5" />
                </span>
                {label}
              </li>
            ))}
          </ul>
        </div>

        <p className="relative text-xs text-white/45">Distribution GIS · Operations · Field</p>
      </div>

      {/* Right: the sign-in form. */}
      <div className="bg-muted/30 relative flex flex-1 items-center justify-center p-4">
        <div className="w-full max-w-sm">
          <div className="mb-6 flex flex-col items-center gap-2 text-center lg:items-start lg:text-left">
            <div
              onClick={handleLogoTap}
              className="bg-primary text-primary-foreground mb-1 flex size-11 cursor-default items-center justify-center rounded-xl shadow-sm select-none lg:hidden"
            >
              <Zap className="size-6" />
            </div>
            <h1 className="text-2xl font-semibold tracking-tight">Welcome back</h1>
            <p className="text-muted-foreground text-sm">Sign in to your VoltEdge account.</p>
          </div>

          <div className="bg-background rounded-xl border p-6 shadow-sm">
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

        {ssoProviders.length > 0 && (showPasswordForm || codeUnlocked) && (
          <div className="text-muted-foreground my-4 flex items-center gap-3 text-xs">
            <span className="bg-border h-px flex-1" />
            or
            <span className="bg-border h-px flex-1" />
          </div>
        )}

        {showPasswordForm ? (
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

            {login.isError && <p className="text-destructive text-sm">{login.error.message}</p>}

            <Button type="submit" className="mt-1 w-full" disabled={!canSubmit}>
              {login.isPending && <Loader2 className="size-4 animate-spin" />}
              Sign in
            </Button>
          </form>
        ) : codeUnlocked ? (
          // Local sign-in is off, a reveal code is set, AND the admin performed
          // the gesture: show the code box. The server re-checks the code at
          // login, so this is the soft gate; typing it just reveals the form.
          <form onSubmit={handleReveal} className="flex flex-col gap-3">
            <div className="flex flex-col gap-1">
              <label className="text-xs font-medium" htmlFor="bg-code">
                Break-glass access
              </label>
              <input
                id="bg-code"
                type="password"
                autoComplete="off"
                className={inputClass}
                value={code}
                onChange={(e) => setCode(e.target.value)}
                placeholder="Enter the break-glass code"
                autoFocus
              />
              <p className="text-muted-foreground text-xs">
                Password sign-in is restricted. Enter the break-glass code to continue, then sign in with a
                break-glass account.
              </p>
            </div>
            {reveal.isError && <p className="text-destructive text-sm">Invalid code.</p>}
            <Button type="submit" className="mt-1 w-full" disabled={code.trim().length === 0 || reveal.isPending}>
              {reveal.isPending && <Loader2 className="size-4 animate-spin" />}
              Unlock
            </Button>
          </form>
        ) : null}

            {!providersPending && providers && !providers.local_auth_enabled && !codeRequired && (
              <p className="text-muted-foreground mt-4 text-center text-xs">
                Password sign-in is currently restricted to break-glass accounts. Use SSO, or contact an administrator.
              </p>
            )}
          </div>
        </div>
      </div>
    </div>
  )
}
