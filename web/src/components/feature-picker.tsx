import { Search, X } from "lucide-react"
import { useState } from "react"

import { inputClass } from "@/components/map/field-utils"
import { MIN_QUERY_LENGTH, useFeatureNameSearch } from "@/hooks/use-feature-name-search"
import { cn } from "@/lib/utils"

export interface PickedFeature {
  featureClass: string
  featureId: string
  name: string
}

/**
 * A single-line feature-name search that fans out across feature classes
 * (useFeatureNameSearch) and calls onPick with the chosen feature -- the shared
 * extraction of the pattern the map's search box, the nodal prompt and the
 * diagram "add from map" picker each grew their own copy of. Lets a form replace
 * a raw-UUID `<input>` with "search a service point / device by name".
 *
 * `classCodes` scopes the fan-out (e.g. only SERVICE_POINT + METER, or only the
 * switchable device classes) so a picker only offers the right kind of feature.
 * `exclude` hides features already chosen. When `selected` is set the box shows
 * the chosen feature as a chip with a clear button instead of the search field.
 */
export function FeaturePicker({
  placeholder,
  classCodes,
  exclude,
  selected,
  onPick,
  onClear,
}: {
  placeholder: string
  classCodes?: string[]
  exclude?: Set<string>
  selected?: PickedFeature | null
  onPick: (picked: PickedFeature) => void
  onClear?: () => void
}) {
  const [query, setQuery] = useState("")
  const [focused, setFocused] = useState(false)
  const { results, loading } = useFeatureNameSearch(query, classCodes)
  const querying = query.trim().length >= MIN_QUERY_LENGTH
  const filtered = exclude ? results.filter((r) => !exclude.has(`${r.featureClassCode}:${r.featureId}`)) : results

  // Once a feature is chosen, show it as a removable chip rather than the search
  // box, so the form reads "this is what you picked" (not a blank field).
  if (selected) {
    return (
      <div className={cn(inputClass, "flex items-center justify-between gap-2")}>
        <span className="flex min-w-0 flex-col">
          <span className="truncate text-sm">{selected.name}</span>
          <span className="text-muted-foreground truncate text-xs">
            {selected.featureClass} · {selected.featureId.slice(0, 8)}
          </span>
        </span>
        {onClear && (
          <button
            type="button"
            onClick={onClear}
            className="text-muted-foreground hover:text-foreground shrink-0"
            title="Clear selection"
          >
            <X className="size-4" />
          </button>
        )}
      </div>
    )
  }

  return (
    <div className="relative">
      <Search className="text-muted-foreground pointer-events-none absolute top-1/2 left-2 size-3.5 -translate-y-1/2" />
      <input
        type="text"
        className={cn(inputClass, "w-full pl-7")}
        placeholder={placeholder}
        value={query}
        onChange={(e) => setQuery(e.target.value)}
        onFocus={() => setFocused(true)}
        onBlur={() => setFocused(false)}
      />
      {focused && (
        <div
          className="bg-popover absolute z-10 mt-1 max-h-44 w-full overflow-y-auto rounded-md border shadow-lg"
          onMouseDown={(e) => e.preventDefault()}
        >
          {!querying && (
            <p className="text-muted-foreground px-2 py-1.5 text-xs">
              Type at least {MIN_QUERY_LENGTH} characters to search…
            </p>
          )}
          {querying && loading && <p className="text-muted-foreground px-2 py-1.5 text-xs">Searching…</p>}
          {querying && !loading && filtered.length === 0 && (
            <p className="text-muted-foreground px-2 py-1.5 text-xs">No matches</p>
          )}
          {querying &&
            filtered.map((r) => (
              <button
                key={`${r.featureClassCode}:${r.featureId}`}
                type="button"
                onClick={() => {
                  onPick({ featureClass: r.featureClassCode, featureId: r.featureId, name: r.name })
                  setQuery("")
                  setFocused(false)
                }}
                className="hover:bg-accent flex w-full flex-col items-start rounded-sm px-2 py-1 text-left"
              >
                <span className="text-sm">{r.name}</span>
                <span className="text-muted-foreground text-xs">{r.className}</span>
              </button>
            ))}
        </div>
      )}
    </div>
  )
}
