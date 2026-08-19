import { useSymbologyIcons, useKnownIconRefs } from "@/hooks/use-symbology-icons"
import { iconDataUri } from "@/lib/symbology-icons"
import { cn } from "@/lib/utils"

/**
 * A visual icon_ref picker -- a grid of the real rendered thumbnails
 * (tinted to the current color) rather than a bare text <select> of
 * names. Every icon the admin can pick is resolved through the exact same
 * iconDataUri path the map and legend use (the DB set unioned with the
 * hardcoded fallbacks, useKnownIconRefs), so what you see here is what a
 * feature will render -- no separate preview code to drift out of sync.
 *
 * Reused by both the feature-class create form's default-style picker and
 * the per-class rule editor, so "choose an icon" looks and behaves the
 * same in both places. `color` only tints icons that carry a {{COLOR}}
 * placeholder; full-color artwork renders as-is (see symbology-icons.ts).
 */
export function IconPicker({
  value,
  color = "#000000",
  onChange,
}: {
  value: string
  color?: string
  onChange: (iconRef: string) => void
}) {
  const icons = useSymbologyIcons()
  const refs = useKnownIconRefs()

  return (
    <div className="flex flex-wrap gap-1.5">
      {refs.map((ref) => (
        <button
          key={ref}
          type="button"
          title={ref}
          aria-pressed={value === ref}
          onClick={() => onChange(ref)}
          className={cn(
            "bg-background hover:bg-accent flex size-9 shrink-0 items-center justify-center rounded-md border",
            value === ref && "border-ring ring-ring/50 ring-2",
          )}
        >
          <img src={iconDataUri(ref, color, icons)} alt={ref} className="size-6" />
        </button>
      ))}
    </div>
  )
}
