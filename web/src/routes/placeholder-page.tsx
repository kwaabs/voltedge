import type { LucideIcon } from "lucide-react"

import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"

export function PlaceholderPage({
  icon: Icon,
  title,
  description,
}: {
  icon: LucideIcon
  title: string
  description: string
}) {
  return (
    <div className="h-full overflow-y-auto p-4">
      <Card>
        <CardHeader>
          <div className="text-muted-foreground mb-2 flex size-10 items-center justify-center rounded-lg border">
            <Icon className="size-5" />
          </div>
          <CardTitle>{title}</CardTitle>
          <CardDescription>{description}</CardDescription>
        </CardHeader>
        <CardContent>
          <p className="text-muted-foreground text-sm">
            This view is built in a later Phase 3 slice -- this pass is the map itself
            (base layer + your compiled feature classes rendered from Martin).
          </p>
        </CardContent>
      </Card>
    </div>
  )
}
