import { ChevronDown, LogOut, Zap } from "lucide-react"
import { useState } from "react"
import { useLocation, useNavigate } from "react-router"

import { ConfirmDeleteDialog } from "@/components/admin/confirm-delete-dialog"
import { ActivityBell } from "@/components/layout/activity-bell"
import { ThemeToggle } from "@/components/layout/theme-toggle"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { Separator } from "@/components/ui/separator"
import { SidebarTrigger } from "@/components/ui/sidebar"
import { useLogout, useSession } from "@/hooks/use-auth"

const TITLES: Record<string, string> = {
  "/": "Map",
  "/features": "Feature classes",
  "/admin/users": "Users",
}

/** Two-letter initials for the avatar: first name's initial + last name's
 * initial ("Justice Danso" -> "JD"). A single word gives its first letter, and
 * a blank name falls back to the email's first character, so there's always
 * something to show. */
function initials(name: string, email: string): string {
  const parts = name.trim().split(/\s+/).filter(Boolean)
  if (parts.length >= 2) return (parts[0][0] + parts[parts.length - 1][0]).toUpperCase()
  if (parts.length === 1) return parts[0][0].toUpperCase()
  return (email.trim()[0] ?? "?").toUpperCase()
}

/**
 * The header's account control, wired to the real session (Slice D). The
 * shell only renders behind RequireSession, so a user is always present
 * here; the menu shows who's signed in and offers sign-out, which revokes
 * the session server-side and returns to the login screen.
 */
function AccountMenu() {
  const { data: user } = useSession()
  const logout = useLogout()
  const navigate = useNavigate()
  const [confirmSignOut, setConfirmSignOut] = useState(false)

  if (!user) return null

  // Revoke the session, then land on the login screen whether or not the revoke
  // call succeeded (the local cache is cleared either way). Returns a promise so
  // the confirm dialog can show its spinner until the sign-out settles.
  async function handleSignOut() {
    await new Promise<void>((resolve) => {
      logout.mutate(undefined, {
        onSettled: () => {
          navigate("/login", { replace: true })
          resolve()
        },
      })
    })
  }

  return (
    <>
      <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button
          variant="ghost"
          size="sm"
          title={user.display_name}
          aria-label={`Account: ${user.display_name}`}
          // `group` so the chevron can react to the menu's open state (Radix puts
          // data-state="open" on this trigger): ChevronDown rotates 180° when open,
          // reading as an up-chevron -- down to expand, up to collapse.
          className="group gap-1 px-1"
        >
          {/* Round initials avatar. Green in LIGHT MODE ONLY; the dark: overrides
              restore the neutral look so dark mode is unchanged. */}
          <span className="flex size-8 items-center justify-center rounded-full border border-green-600 bg-green-600 text-xs font-semibold text-white dark:border-input dark:bg-background dark:text-foreground">
            {initials(user.display_name, user.email)}
          </span>
          {/* No explicit colour -- inherit the header's text (slate-100 in the
              light blue bar, foreground in dark) like the bell/theme icons, so it
              stays visible on the dark header. */}
          <ChevronDown className="size-4 opacity-80 transition-transform group-data-[state=open]:rotate-180" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-64">
        <DropdownMenuLabel className="flex flex-col gap-0.5">
          <span className="truncate font-medium">{user.display_name}</span>
          <span className="text-muted-foreground truncate text-xs font-normal">{user.email}</span>
        </DropdownMenuLabel>

        <div className="px-2 py-1.5">
          <div className="text-muted-foreground text-[10px] font-medium uppercase tracking-wide">Roles</div>
          {user.roles.length > 0 ? (
            <div className="mt-1 flex flex-wrap gap-1">
              {user.roles.map((r) => (
                <span key={r} className="bg-muted text-muted-foreground rounded px-1.5 py-0.5 text-xs">
                  {r}
                </span>
              ))}
            </div>
          ) : (
            <div className="text-muted-foreground mt-1 text-xs">No role assigned</div>
          )}
        </div>

        {user.territories.length > 0 && (
          <div className="px-2 pb-1.5">
            <div className="text-muted-foreground text-[10px] font-medium uppercase tracking-wide">
              Granted territories
            </div>
            <div className="mt-1 flex flex-col gap-0.5">
              {user.territories.map((t) => (
                <span key={t.territory_id} className="truncate text-xs">
                  {t.territory_name} <span className="text-muted-foreground">· {t.level_code}</span>
                </span>
              ))}
            </div>
          </div>
        )}

        <DropdownMenuSeparator />
        {/* Open a confirm dialog rather than signing out immediately; the menu
            closes on select and the dialog opens off `confirmSignOut`. */}
        <DropdownMenuItem disabled={logout.isPending} onSelect={() => setConfirmSignOut(true)}>
          <LogOut className="size-4" />
          Sign out
        </DropdownMenuItem>
      </DropdownMenuContent>
      </DropdownMenu>

      <ConfirmDeleteDialog
        open={confirmSignOut}
        onOpenChange={setConfirmSignOut}
        title="Sign out?"
        description="You’ll be returned to the sign-in screen and will need to sign in again to continue."
        confirmLabel="Sign out"
        onConfirm={handleSignOut}
      />
    </>
  )
}

export function AppHeader() {
  const { pathname } = useLocation()
  const title = TITLES[pathname] ?? "VoltEdge"

  // Light and refined: the "grey/basic" feel came from an empty bar (just the
  // page title floating over a hard border), not from a lack of colour. So the
  // Medium dark-blue chrome ("not too dark") in LIGHT MODE ONLY: a solid blue
  // surface (oklch L0.45) with light text -- real app-frame presence, and the
  // copper brand mark pops against blue (near complementary). Dark mode is left
  // untouched via `dark:` overrides back to the neutral theme tokens. Ghost icon
  // buttons (SidebarTrigger/ThemeToggle) inherit the light text; the outline
  // account button stays a readable chip. Soft shadow lifts the bar off the map.
  return (
    <header className="bg-[oklch(0.45_0.10_252)] text-slate-100 dark:bg-background dark:text-foreground sticky top-0 z-10 flex h-14 shrink-0 items-center gap-3 border-b border-white/10 dark:border-border px-3 shadow-sm">
      <SidebarTrigger />
      {/* Brand lockup -- copper bolt mark + wordmark; the app's constant
          identity, kept solid so it reads as the logo. */}
      <div className="flex items-center gap-2">
        <span className="bg-primary text-primary-foreground flex size-6 items-center justify-center rounded-md shadow-sm">
          <Zap className="size-3.5" />
        </span>
        <span className="text-sm font-semibold tracking-tight">VoltEdge</span>
      </div>
      <Separator orientation="vertical" className="h-5 bg-white/20 dark:bg-border" />
      {/* Page context -- lighter than the brand so the hierarchy reads
          identity first, "where am I" second. */}
      <span className="text-slate-300 dark:text-muted-foreground text-sm font-medium">{title}</span>
      <div className="ml-auto flex items-center gap-1">
        <ActivityBell />
        <ThemeToggle />
        <AccountMenu />
      </div>
    </header>
  )
}
