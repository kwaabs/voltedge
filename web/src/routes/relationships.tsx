import { Link2 } from "lucide-react"

import { PlaceholderPage } from "@/routes/placeholder-page"

export function RelationshipsPage() {
  return (
    <PlaceholderPage
      icon={Link2}
      title="Relationships"
      description="Establish and inspect structural relationships, gated on relationship_classes."
    />
  )
}
