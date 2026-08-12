import { Database } from "lucide-react"

import { PlaceholderPage } from "@/routes/placeholder-page"

export function FeaturesPage() {
  return (
    <PlaceholderPage
      icon={Database}
      title="Feature classes"
      description="Browse and edit compiled feature classes (POLE, MV_CONDUCTOR, DIST_XFMR, ...)."
    />
  )
}
