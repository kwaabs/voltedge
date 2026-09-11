import { useEffect, useState } from "react"
import { Loader2, X } from "lucide-react"

import { inputClass } from "@/components/map/field-utils"
import { apiFetch } from "@/lib/api"

/** One picked bus: its class + id (what POST /couplers needs) plus the name to
 * show in the chip. */
export interface BusPick {
  featureClass: string
  featureId: string
  name: string
}

const MIN_QUERY_LENGTH = 2
const DEBOUNCE_MS = 300
const PER_CLASS_LIMIT = 8

/**
 * A search-and-pick input for one of a bus coupler's two buses (Phase
 * Bus-coupler Slice 2). Scoped to the coupler option's eligible bus classes
 * (`busFeatureClasses`, usually just MV_BUSBAR), it fans out one GET
 * /features/{code}?name= per class in parallel and merges -- the exact
 * shape use-feature-name-search.ts uses for the map's global search, but
 * narrowed to the bus classes so the coupler picker never surfaces a breaker or
 * a conductor. A picked bus renders as a clearable chip; `excludeId` drops the
 * other picked bus from the results so a coupler can't be wired bus-to-itself.
 */
export function CouplerBusPicker({
  label,
  busFeatureClasses,
  value,
  onChange,
  excludeId,
}: {
  label: string
  busFeatureClasses: string[]
  value: BusPick | null
  onChange: (v: BusPick | null) => void
  excludeId?: string
}) {
  const [query, setQuery] = useState("")
  const [loading, setLoading] = useState(false)
  const [results, setResults] = useState<BusPick[]>([])

  useEffect(() => {
    const trimmed = query.trim()
    if (value || trimmed.length < MIN_QUERY_LENGTH || busFeatureClasses.length === 0) {
      setResults([])
      setLoading(false)
      return
    }
    let cancelled = false
    setLoading(true)
    const timer = setTimeout(async () => {
      const perClass = await Promise.all(
        busFeatureClasses.map(async (code) => {
          try {
            const res = await apiFetch<{ features: { id: string; name: string | null }[] }>(
              `/features/${code}?name=${encodeURIComponent(trimmed)}&limit=${PER_CLASS_LIMIT}`,
            )
            return res.features
              .filter((f) => f.name)
              .map((f) => ({ featureClass: code, featureId: f.id, name: f.name as string }))
          } catch {
            // A bus class whose search fails (e.g. not yet compiled) is just
            // excluded, not worth failing the whole pick over one class.
            return []
          }
        }),
      )
      if (!cancelled) {
        setResults(perClass.flat().filter((r) => r.featureId !== excludeId))
        setLoading(false)
      }
    }, DEBOUNCE_MS)
    return () => {
      cancelled = true
      clearTimeout(timer)
    }
  }, [query, value, excludeId, busFeatureClasses])

  return (
    <div className="flex flex-col gap-1">
      <label className="text-xs font-medium">
        {label} <span className="text-destructive">*</span>
      </label>
      {value ? (
        <div className="border-input flex items-center justify-between gap-2 rounded-md border px-2 py-1.5 text-sm">
          <span className="truncate">{value.name}</span>
          <button
            type="button"
            className="text-muted-foreground hover:text-foreground shrink-0"
            onClick={() => {
              onChange(null)
              setQuery("")
            }}
          >
            <X className="size-3.5" />
            <span className="sr-only">Clear</span>
          </button>
        </div>
      ) : (
        <div className="relative">
          <input
            type="text"
            className={inputClass}
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="Search a busbar by name…"
          />
          {loading && (
            <Loader2 className="text-muted-foreground absolute top-1/2 right-2 size-3.5 -translate-y-1/2 animate-spin" />
          )}
          {results.length > 0 && (
            <div className="bg-popover absolute z-10 mt-1 max-h-48 w-full overflow-y-auto rounded-md border shadow-md">
              {results.map((r) => (
                <button
                  type="button"
                  key={`${r.featureClass}:${r.featureId}`}
                  className="hover:bg-accent block w-full truncate px-2 py-1.5 text-left text-sm"
                  onClick={() => {
                    onChange(r)
                    setQuery("")
                    setResults([])
                  }}
                >
                  {r.name}
                </button>
              ))}
            </div>
          )}
        </div>
      )}
    </div>
  )
}
