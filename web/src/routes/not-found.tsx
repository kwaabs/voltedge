import { Link } from "react-router"

import { Button } from "@/components/ui/button"

export function NotFoundPage() {
  return (
    <div className="flex flex-1 flex-col items-center justify-center gap-3 text-center">
      <p className="text-muted-foreground text-sm font-medium">404</p>
      <h1 className="text-xl font-semibold">Page not found</h1>
      <Button asChild variant="outline" size="sm" className="mt-2">
        <Link to="/">Back to the map</Link>
      </Button>
    </div>
  )
}
