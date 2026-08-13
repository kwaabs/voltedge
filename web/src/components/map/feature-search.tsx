import { useEffect, useRef, useState } from "react"
import { Loader2, Search, X } from "lucide-react"

import { Button } from "@/components/ui/button"
import { useFeatureClasses } from "@/hooks/use-feature-classes"
import { apiFetch } from "@/lib/api"
import { cn } from "@/lib/utils"
import { useSelectionStore } from "@/stores/selection-store"

const inputClass =
  "border-input bg-background h-8 w-56 rounded-md border pl-7 pr-2 text-sm outline-none focus-visible:border-ring focus-visible:ring-ring/50 focus-visible:ring-[3px]"

function shortId(id: string): string {
  return id.length > 8 ? `${id.slice(0, 8)}…` : id
}

interface ResultRow {
  featureClassCode: string
  className: string
  featureId: string
  name: string
}

const MIN_QUERY_LENGTH = 2
const DEBOUNCE_MS = 300
const PER_CLASS_LIMIT = 8

/**
 * Search-by-name across every feature class -- there's no single
 * cross-class search endpoint (each feature class is its own physical
 * table, per the compiler), so this fans out one GET /features/{code}?
 * name= per class in parallel and merges client-side, same reasoning
 * layers-panel.tsx already fans out per Martin catalog entry. Only
 * feature-panel.tsx-style name display was worth a per-row fetch
 * (bounded, small lists); a name search is the opposite shape --
 * unbounded classes, so results are capped per class
 * (PER_CLASS_LIMIT) and the query itself is debounced and
 * length-gated to keep the fan-out cheap while typing.
 */
export function FeatureSearch() {
  const [open, setOpen] = useState(false)
  const [query, setQuery] = useState("")
  const [loading, setLoading] = useState(false)
  const [results, setResults] = useState<ResultRow[]>([])
  const { data: featureClasses } = useFeatureClasses()
  const containerRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (!open) return
    function handlePointerDown(e: PointerEvent) {
      if (containerRef.current && !containerRef.current.contains(e.target as Node)) {
        setOpen(false)
      }
    }
    window.addEventListener("pointerdown", handlePointerDown)
    return () => window.removeEventListener("pointerdown", handlePointerDown)
  }, [open])

  useEffect(() => {
    const trimmed = query.trim()
    if (trimmed.length < MIN_QUERY_LENGTH || !featureClasses) {
      setResults([])
      setLoading(false)
      return
    }

    let cancelled = false
    setLoading(true)
    const classes = featureClasses.feature_classes

    const timer = setTimeout(async () => {
      const perClass = await Promise.all(
        classes.map(async (fc) => {
          try {
            const res = await apiFetch<{ features: { id: string; name: string | null }[] }>(
              `/features/${fc.code}?name=${encodeURIComponent(trimmed)}&limit=${PER_CLASS_LIMIT}`,
            )
            return res.features
              .filter((f) => f.name)
              .map((f) => ({ featureClassCode: fc.code, className: fc.name, featureId: f.id, name: f.name as string }))
          } catch {
            // A feature class whose search fails (e.g. not yet
            // compiled) is just excluded, not worth failing the whole
            // search over one class.
            return []
          }
        }),
      )
      if (!cancelled) {
        setResults(perClass.flat())
        setLoading(false)
      }
    }, DEBOUNCE_MS)

    return () => {
      cancelled = true
      clearTimeout(timer)
    }
  }, [query, featureClasses])

  function handleSelect(row: ResultRow) {
    useSelectionStore.getState().select(row.featureClassCode, row.featureId, { focus: true })
    setOpen(false)
    setQuery("")
  }

  return (
    <div ref={containerRef} className="relative">
      <div className="relative">
        <Search className="text-muted-foreground pointer-events-none absolute top-1/2 left-2 size-3.5 -translate-y-1/2" />
        <input
          type="text"
          className={cn(inputClass, "bg-background/90 shadow-sm")}
          placeholder="Search by name…"
          value={query}
          onFocus={() => setOpen(true)}
          onChange={(e) => {
            setQuery(e.target.value)
            setOpen(true)
          }}
        />
        {query && (
          <Button
            variant="ghost"
            size="icon"
            className="absolute top-1/2 right-0.5 size-6 -translate-y-1/2"
            onClick={() => {
              setQuery("")
              setResults([])
            }}
          >
            <X className="size-3.5" />
            <span className="sr-only">Clear search</span>
          </Button>
        )}
      </div>

      {open && query.trim().length >= MIN_QUERY_LENGTH && (
        <div className="bg-popover text-popover-foreground absolute top-full right-0 z-10 mt-1 max-h-64 w-72 overflow-y-auto rounded-md border shadow-lg">
          {loading ? (
            <div className="text-muted-foreground flex items-center gap-2 p-3 text-sm">
              <Loader2 className="size-4 animate-spin" />
              Searching…
            </div>
          ) : results.length === 0 ? (
            <p className="text-muted-foreground p-3 text-sm">No named features match "{query.trim()}".</p>
          ) : (
            results.map((row) => (
              <button
                key={`${row.featureClassCode}:${row.featureId}`}
                type="button"
                onClick={() => handleSelect(row)}
                className="hover:bg-accent hover:text-accent-foreground block w-full border-b p-2 text-left text-sm transition-colors last:border-b-0"
              >
                <p className="truncate font-medium">{row.name}</p>
                <p className="text-muted-foreground truncate text-xs">
                  {row.className} · {shortId(row.featureId)}
                </p>
              </button>
            ))
          )}
        </div>
      )}
    </div>
  )
}
