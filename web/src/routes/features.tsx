import { useMemo, useState } from "react"
import { useNavigate } from "react-router"
import { ChevronDown, ChevronRight, Database, Loader2, MapPinned, MapPinOff, Search } from "lucide-react"

import { formatFieldLabel, formatValue, inputClass } from "@/components/map/field-utils"
import { Badge } from "@/components/ui/badge"
import { type FeatureRecord } from "@/hooks/use-feature"
import { type FeatureClassSummary, useFeatureClasses } from "@/hooks/use-feature-classes"
import { useFeatureClassSchema } from "@/hooks/use-feature-class-schema"
import { useFeatureRows } from "@/hooks/use-feature-rows"
import { cn } from "@/lib/utils"
import { useSelectionStore } from "@/stores/selection-store"

/**
 * The feature-classes browser: a UI home for every compiled feature class,
 * and the only place a geometry 'none' class's instances (PROTECTIVE_RELAY,
 * BAY, SUBSTATION_PANEL, STATION_BATTERY, ...) are visible at all -- they
 * never render on the map. Left: pick a class; right: page its rows and
 * expand any one to read its full, schema-labelled attributes.
 */
export function FeaturesPage() {
  const { data, isPending, isError } = useFeatureClasses()
  const [query, setQuery] = useState("")
  const [selected, setSelected] = useState<string | undefined>(undefined)

  const classes = useMemo(() => {
    const all = data?.feature_classes ?? []
    const q = query.trim().toLowerCase()
    const filtered = q
      ? all.filter((c) => c.code.toLowerCase().includes(q) || c.name.toLowerCase().includes(q))
      : all
    return [...filtered].sort((a, b) => a.name.localeCompare(b.name))
  }, [data, query])

  const selectedClass = useMemo(
    () => classes.find((c) => c.code === selected) ?? data?.feature_classes.find((c) => c.code === selected),
    [classes, data, selected],
  )

  return (
    <div className="flex h-full">
      {/* Left: class list */}
      <div className="flex w-72 shrink-0 flex-col border-r">
        <div className="border-b p-3">
          <h1 className="mb-2 flex items-center gap-2 text-sm font-semibold">
            <Database className="size-4" />
            Feature classes
          </h1>
          <div className="relative">
            <Search className="text-muted-foreground pointer-events-none absolute left-2 top-1/2 size-4 -translate-y-1/2" />
            <input
              className={cn(inputClass, "pl-8")}
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder="Search classes..."
            />
          </div>
        </div>

        <div className="flex-1 overflow-y-auto p-2">
          {isPending ? (
            <div className="text-muted-foreground flex items-center gap-2 p-2 text-sm">
              <Loader2 className="size-4 animate-spin" />
              Loading...
            </div>
          ) : isError ? (
            <p className="text-destructive p-2 text-sm">Could not load feature classes.</p>
          ) : classes.length === 0 ? (
            <p className="text-muted-foreground p-2 text-sm">No classes match "{query}".</p>
          ) : (
            <div className="flex flex-col gap-0.5">
              {classes.map((c) => (
                <ClassRow
                  key={c.code}
                  fc={c}
                  active={c.code === selected}
                  onClick={() => setSelected(c.code)}
                />
              ))}
            </div>
          )}
        </div>
      </div>

      {/* Right: rows of the selected class */}
      <div className="flex-1 overflow-y-auto">
        {selectedClass ? (
          <ClassRows fc={selectedClass} />
        ) : (
          <div className="text-muted-foreground flex h-full items-center justify-center p-6 text-sm">
            Select a feature class to browse its rows.
          </div>
        )}
      </div>
    </div>
  )
}

/** True when the class has no map geometry -- its rows only exist here. */
function isGeometryless(fc: FeatureClassSummary): boolean {
  return fc.geometry_type === "none"
}

function ClassRow({
  fc,
  active,
  onClick,
}: {
  fc: FeatureClassSummary
  active: boolean
  onClick: () => void
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={cn(
        "hover:bg-muted/60 flex flex-col items-start gap-0.5 rounded-md px-2 py-1.5 text-left",
        active && "bg-muted",
      )}
    >
      <span className="flex w-full items-center gap-1.5">
        <span className="truncate text-sm font-medium">{fc.name}</span>
        {isGeometryless(fc) && <MapPinOff className="text-muted-foreground size-3 shrink-0" />}
      </span>
      <span className="text-muted-foreground font-mono text-[10px]">{fc.code}</span>
    </button>
  )
}

function ClassRows({ fc }: { fc: FeatureClassSummary }) {
  const { data: schema } = useFeatureClassSchema(fc.code)
  const { data, isPending, isError } = useFeatureRows(fc.code)
  const [expanded, setExpanded] = useState<string | undefined>(undefined)

  const subtypeName = useMemo(() => {
    const map = new Map<number, string>()
    for (const s of schema?.subtypes ?? []) map.set(s.code, s.name)
    return map
  }, [schema])

  const rows = data?.features ?? []

  return (
    <div className="flex flex-col">
      <div className="bg-background sticky top-0 z-10 border-b p-3">
        <div className="flex items-center gap-2">
          <h2 className="text-sm font-semibold">{fc.name}</h2>
          <span className="text-muted-foreground font-mono text-xs">{fc.code}</span>
        </div>
        <div className="mt-1 flex flex-wrap items-center gap-1.5">
          <Badge variant="outline" className="text-[10px]">
            {isGeometryless(fc) ? "no geometry" : fc.geometry_type}
          </Badge>
          <Badge variant="outline" className="text-[10px]">
            network: {fc.network_role}
          </Badge>
          {!isPending && (
            <span className="text-muted-foreground text-xs">
              {rows.length} row{rows.length === 1 ? "" : "s"}
            </span>
          )}
        </div>
      </div>

      <div className="p-3">
        {isPending ? (
          <div className="text-muted-foreground flex items-center gap-2 text-sm">
            <Loader2 className="size-4 animate-spin" />
            Loading rows...
          </div>
        ) : isError ? (
          <p className="text-destructive text-sm">Could not load rows for {fc.code}.</p>
        ) : rows.length === 0 ? (
          <p className="text-muted-foreground text-sm">No rows yet for this class.</p>
        ) : (
          <div className="overflow-hidden rounded-md border">
            {rows.map((row) => (
              <RowItem
                key={row.id}
                classCode={fc.code}
                row={row}
                subtypeLabel={row.subtype != null ? (subtypeName.get(row.subtype) ?? String(row.subtype)) : "—"}
                fieldOrder={schema?.fields.map((f) => f.name) ?? Object.keys(row.attributes)}
                expanded={expanded === row.id}
                onToggle={() => setExpanded((cur) => (cur === row.id ? undefined : row.id))}
              />
            ))}
          </div>
        )}
      </div>
    </div>
  )
}

function RowItem({
  classCode,
  row,
  subtypeLabel,
  fieldOrder,
  expanded,
  onToggle,
}: {
  classCode: string
  row: FeatureRecord
  subtypeLabel: string
  fieldOrder: string[]
  expanded: boolean
  onToggle: () => void
}) {
  const navigate = useNavigate()
  // Present only when THIS row actually carries geometry -- the list endpoint
  // returns it (ST_AsGeoJSON) for geometric classes and omits it for
  // geometry-'none' classes and unplaced (diagram-only) rows, so a row with
  // nothing to pan to never offers the button.
  const hasGeometry = row.geometry != null

  // Select this feature (focus: true pans/zooms the map to it even when it's
  // off-screen or not yet loaded -- selection-store's focusToken) then jump to
  // the map. Same store-then-navigate pattern the OMS "Show on map" uses.
  function showOnMap() {
    useSelectionStore.getState().select(classCode, row.id, { focus: true })
    navigate("/")
  }

  return (
    <div className="border-b last:border-b-0">
      <div className="hover:bg-muted/50 flex w-full items-center gap-2 px-2 py-1.5">
        <button
          type="button"
          onClick={onToggle}
          className="flex min-w-0 flex-1 items-center gap-2 text-left"
        >
          {expanded ? (
            <ChevronDown className="text-muted-foreground size-4 shrink-0" />
          ) : (
            <ChevronRight className="text-muted-foreground size-4 shrink-0" />
          )}
          <span className="truncate text-sm font-medium">{row.name ?? "(unnamed)"}</span>
          <span className="text-muted-foreground ml-auto shrink-0 text-xs">{subtypeLabel}</span>
          <Badge variant="outline" className="shrink-0 text-[10px]">
            {row.status}
          </Badge>
        </button>
        {hasGeometry && (
          <button
            type="button"
            onClick={showOnMap}
            title="Show on map"
            aria-label="Show on map"
            className="text-muted-foreground hover:text-foreground hover:bg-muted shrink-0 rounded p-1"
          >
            <MapPinned className="size-4" />
          </button>
        )}
      </div>

      {expanded && (
        <dl className="bg-muted/30 grid grid-cols-[minmax(8rem,auto)_1fr] gap-x-4 gap-y-1 px-8 py-2 text-xs">
          <dt className="text-muted-foreground">ID</dt>
          <dd className="font-mono break-all">{row.id}</dd>
          {fieldOrder.map((name) => (
            <FieldPair key={name} label={formatFieldLabel(name)} value={row.attributes[name]} />
          ))}
          <dt className="text-muted-foreground">Created</dt>
          <dd>{formatValue(row.created_at)}</dd>
          <dt className="text-muted-foreground">Updated</dt>
          <dd>{formatValue(row.updated_at)}</dd>
        </dl>
      )}
    </div>
  )
}

function FieldPair({ label, value }: { label: string; value: unknown }) {
  return (
    <>
      <dt className="text-muted-foreground">{label}</dt>
      <dd className="break-words">{formatValue(value)}</dd>
    </>
  )
}
