import { Settings } from "lucide-react"

import { PlaceholderPage } from "@/routes/placeholder-page"

export function SettingsPage() {
  return (
    <PlaceholderPage
      icon={Settings}
      title="Settings"
      description="Account, theme, and app preferences."
    />
  )
}
