import { LogOut, User } from "lucide-react"
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
  "/connectivity": "Connectivity",
  "/relationships": "Relationships",
  "/admin/users": "Users",
  "/settings": "Settings",
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
        <Button variant="outline" size="sm" className="gap-1.5">
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

  return (
    <header className="bg-background/95 supports-[backdrop-filter]:bg-background/60 sticky top-0 z-10 flex h-14 shrink-0 items-center gap-2 border-b backdrop-blur">
      <div className="flex w-full items-center gap-2 px-3">
        <SidebarTrigger />
        <Separator orientation="vertical" className="h-5" />
        <span className="text-sm font-medium">{title}</span>
        <div className="ml-auto flex items-center gap-1">
          <ThemeToggle />
          <AccountMenu />
        </div>
      </div>
    </header>
  )
}
