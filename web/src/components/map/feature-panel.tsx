import { Fragment } from "react"
import { Loader2, X } from "lucide-react"

import { Button } from "@/components/ui/button"
import { useFeature } from "@/hooks/use-feature"
import { useFeatureClasses } from "@/hooks/use-feature-classes"
import { useSelectionStore } from "@/stores/selection-store"

/** "condition_rating" -> "Condition rating" -- cheap and clearly better
 * than the raw column name; not a stand-in for a real per-field display
 * label, which isn't modeled in metadata.fields yet. */
function formatFieldLabel(key: string): string {
  const spaced = key.replace(/_/g, " ")
  return spaced.charAt(0).toUpperCase() + spaced.slice(1)
}

function formatValue(value: unknown): string {
  if (value === null || value === undefined || value === "") return "—"
  if (typeof value === "boolean") return value ? "Yes" : "No"
  return String(value)
}

/**
 * Docked to the bottom of the map (between the canvas and the status
 * bar), not floating -- undocking into a movable/resizable/minimizable
 * window is its own later slice, built on top of this one rather than
 * attempted alongside click-to-select. Renders nothing when no feature
 * is selected, so it doesn't reserve space it isn't using.
 */
export function FeaturePanel() {
  const selected = useSelectionStore((s) => s.selected)
  const clear = useSelectionStore((s) => s.clear)
  const { data: featureClasses } = useFeatureClasses()
  const { data: feature, isPending, isError, error } = useFeature(
    selected?.featureClassCode,
    selected?.featureId,
  )

  if (!selected) return null

  const className =
    featureClasses?.feature_classes.find((fc) => fc.code === selected.featureClassCode)?.name ??
    selected.featureClassCode

  return (
    <div className="bg-background flex h-64 shrink-0 flex-col border-t">
      <div className="flex items-center justify-between border-b px-3 py-2">
        <div className="min-w-0">
          <p className="truncate text-sm font-medium">{className}</p>
          <p className="text-muted-foreground truncate font-mono text-xs">{selected.featureId}</p>
        </div>
        <Button variant="ghost" size="icon" className="size-7 shrink-0" onClick={clear}>
          <X className="size-4" />
          <span className="sr-only">Close</span>
        </Button>
      </div>

      <div className="flex-1 overflow-y-auto p-3">
        {isPending && (
          <div className="text-muted-foreground flex items-center gap-2 text-sm">
            <Loader2 className="size-4 animate-spin" />
            Loading feature...
          </div>
        )}
        {isError && (
          <p className="text-destructive text-sm">
            Could not load this feature{error instanceof Error ? `: ${error.message}` : "."}
          </p>
        )}
        {feature && (
          <dl className="grid grid-cols-[max-content_1fr] gap-x-6 gap-y-1.5 text-sm">
            <dt className="text-muted-foreground">Status</dt>
            <dd className="capitalize">{feature.status}</dd>

            {feature.deleted_at && (
              <>
                <dt className="text-muted-foreground">Deleted</dt>
                <dd>{new Date(feature.deleted_at).toLocaleString()}</dd>
              </>
            )}

            {feature.subtype !== undefined && (
              <>
                <dt className="text-muted-foreground">Subtype</dt>
                <dd>{feature.subtype}</dd>
              </>
            )}

            {Object.entries(feature.attributes).map(([key, value]) => (
              <Fragment key={key}>
                <dt className="text-muted-foreground">{formatFieldLabel(key)}</dt>
                <dd>{formatValue(value)}</dd>
              </Fragment>
            ))}

            <dt className="text-muted-foreground">Updated</dt>
            <dd>{new Date(feature.updated_at).toLocaleString()}</dd>
          </dl>
        )}
      </div>
    </div>
  )
}
