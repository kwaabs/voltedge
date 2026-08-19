import { Button } from "@/components/ui/button"
import { useUnitsStore } from "@/stores/units-store"

/**
 * Flips the shared metric/imperial display preference (units-store.ts).
 * Lives in the measure toolbar's active banner, but drives every
 * measurement read-out in the app -- the measure tool's live tooltips
 * and the feature panel's Length/Area rows both re-format the instant
 * this changes.
 */
export function UnitsToggleButton() {
  const units = useUnitsStore((s) => s.units)

  return (
    <Button
      variant="ghost"
      size="sm"
      onClick={() => useUnitsStore.getState().toggle()}
      title="Toggle units (metric / imperial)"
    >
      {units === "metric" ? "Metric" : "Imperial"}
    </Button>
  )
}
