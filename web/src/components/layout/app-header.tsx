import { LogIn } from "lucide-react"

import { Button } from "@/components/ui/button"
import { Separator } from "@/components/ui/separator"
import { SidebarTrigger } from "@/components/ui/sidebar"
import { ThemeToggle } from "@/components/layout/theme-toggle"

/**
 * Login is a stub for this slice -- Azure AD SSO is its own later slice
 * (see docs/phase-3-ui-shell.md "What's still open"). This button exists
 * so the header's final layout doesn't shift once real auth lands.
 */
export function AppHeader() {
  return (
    <header className="bg-background/95 supports-[backdrop-filter]:bg-background/60 sticky top-0 z-10 flex h-14 shrink-0 items-center gap-2 border-b backdrop-blur">
      <div className="flex w-full items-center gap-2 px-3">
        <SidebarTrigger />
        <Separator orientation="vertical" className="h-5" />
        <span className="text-sm font-medium">Dashboard</span>
        <div className="ml-auto flex items-center gap-1">
          <ThemeToggle />
          <Button variant="outline" size="sm" className="gap-1.5">
            <LogIn className="size-4" />
            Sign in
          </Button>
        </div>
      </div>
    </header>
  )
}
