import { Waypoints } from "lucide-react"

import { PlaceholderPage } from "@/routes/placeholder-page"

export function ConnectivityPage() {
  return (
    <PlaceholderPage
      icon={Waypoints}
      title="Connectivity"
      description="Establish and inspect electrical connections, gated on network_rules."
    />
  )
}
