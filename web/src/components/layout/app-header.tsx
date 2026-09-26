import { LogOut, User, Zap } from "lucide-react"
import { useLocation, useNavigate } from "react-router"

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

  if (!user) return null

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button
          variant="outline"
          size="sm"
          // Green account chip in LIGHT MODE ONLY -- the `dark:` overrides restore
          // the neutral outline look (bg-background / border-input / text-foreground
          // + accent hover) so dark mode is unchanged.
          className="gap-1.5 border-green-600 bg-green-600 text-white hover:bg-green-700 hover:text-white dark:border-input dark:bg-background dark:text-foreground dark:hover:bg-accent dark:hover:text-accent-foreground"
        >
          <User className="size-4" />
          <span className="max-w-[12rem] truncate">{user.display_name}</span>
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-56">
        <DropdownMenuLabel className="flex flex-col gap-0.5">
          <span className="truncate font-medium">{user.display_name}</span>
          <span className="text-muted-foreground truncate text-xs font-normal">{user.email}</span>
        </DropdownMenuLabel>
        <DropdownMenuSeparator />
        <DropdownMenuItem
          disabled={logout.isPending}
          onSelect={(e) => {
            e.preventDefault()
            logout.mutate(undefined, {
              // Land on the login screen whether or not the revoke call
              // succeeded -- the local cache is cleared either way.
              onSettled: () => navigate("/login", { replace: true }),
            })
          }}
        >
          <LogOut className="size-4" />
          Sign out
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
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
        <ThemeToggle />
        <AccountMenu />
      </div>
    </header>
  )
}
