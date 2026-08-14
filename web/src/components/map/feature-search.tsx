import { useEffect, useRef, useState } from "react"
import { Loader2, Search, X } from "lucide-react"

import { Button } from "@/components/ui/button"
import { type FeatureSearchResult, MIN_QUERY_LENGTH, useFeatureNameSearch } from "@/hooks/use-feature-name-search"
import { cn } from "@/lib/utils"
import { useSelectionStore } from "@/stores/selection-store"

const inputClass =
  "border-input bg-background h-8 w-56 rounded-md border pl-7 pr-2 text-sm outline-none focus-visible:border-ring focus-visible:ring-ring/50 focus-visible:ring-[3px]"

function shortId(id: string): string {
  return id.length > 8 ? `${id.slice(0, 8)}…` : id
}

/** The map toolbar's search-by-name box -- see use-feature-name-search.ts
 * for the actual cross-class fan-out this wraps. */
export function FeatureSearch() {
  const [open, setOpen] = useState(false)
  const [query, setQuery] = useState("")
  const { results, loading } = useFeatureNameSearch(query)
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

  function handleSelect(row: FeatureSearchResult) {
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
            onClick={() => setQuery("")}
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
