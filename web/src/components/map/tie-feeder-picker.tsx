import { useEffect, useState } from "react"
import { Loader2, X } from "lucide-react"

import { inputClass } from "@/components/map/field-utils"
import { apiFetch } from "@/lib/api"

/** One picked feeder: its class + id (what POST /ties needs) plus the name to
 * show in the chip. */
export interface FeederPick {
  featureClass: string
  featureId: string
  name: string
}

const MIN_QUERY_LENGTH = 2
const DEBOUNCE_MS = 300
const PER_CLASS_LIMIT = 8

/**
 * A search-and-pick input for one of a feeder tie's two conductors (Phase 50
 * Slice 2). Scoped to the tie option's eligible feeder classes
 * (`feederFeatureClasses`, usually just MV_CONDUCTOR), it fans out one GET
 * /features/{code}?name= per class in parallel and merges -- the exact shape
 * coupler-bus-picker.tsx uses, narrowed to the conductor classes so the tie
 * picker never surfaces a switch or a busbar. A picked feeder renders as a
 * clearable chip; `excludeId` drops the other picked feeder from the results so
 * a tie can't be wired feeder-to-itself.
 */
export function TieFeederPicker({
  label,
  feederFeatureClasses,
  value,
  onChange,
  excludeId,
}: {
  label: string
  feederFeatureClasses: string[]
  value: FeederPick | null
  onChange: (v: FeederPick | null) => void
  excludeId?: string
}) {
  const [query, setQuery] = useState("")
  const [loading, setLoading] = useState(false)
  const [results, setResults] = useState<FeederPick[]>([])

  useEffect(() => {
    const trimmed = query.trim()
    if (value || trimmed.length < MIN_QUERY_LENGTH || feederFeatureClasses.length === 0) {
      setResults([])
      setLoading(false)
      return
    }
    let cancelled = false
    setLoading(true)
    const timer = setTimeout(async () => {
      const perClass = await Promise.all(
        feederFeatureClasses.map(async (code) => {
          try {
            const res = await apiFetch<{ features: { id: string; name: string | null }[] }>(
              `/features/${code}?name=${encodeURIComponent(trimmed)}&limit=${PER_CLASS_LIMIT}`,
            )
            return res.features
              .filter((f) => f.name)
              .map((f) => ({ featureClass: code, featureId: f.id, name: f.name as string }))
          } catch {
            // A feeder class whose search fails (e.g. not yet compiled) is just
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
  }, [query, value, excludeId, feederFeatureClasses])

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
            placeholder="Search a conductor by name…"
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
