import { useMemo, useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { useNavigate } from "react-router"
import { ChevronDown, ChevronRight, Database, FileClock, Loader2, MapPinned, MapPinOff, Printer, Search, Trash2 } from "lucide-react"

import { ConfirmDeleteDialog } from "@/components/admin/confirm-delete-dialog"
import { ClassWipeDialog } from "@/components/map/class-wipe-dialog"
import { formatFieldLabel, formatValue, inputClass } from "@/components/map/field-utils"
import { TagPrintSheet, type PrintTag } from "@/components/map/tag-print-sheet"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { useMarkManyPrinted, usePrimaryTagsBatch } from "@/hooks/use-asset-tags"
import { usePermission } from "@/hooks/use-auth"
import { type FeatureRecord } from "@/hooks/use-feature"
import { type FeatureClassSummary, useFeatureClasses } from "@/hooks/use-feature-classes"
import { useFeatureClassSchema } from "@/hooks/use-feature-class-schema"
import { useFeatureRows } from "@/hooks/use-feature-rows"
import { apiFetch } from "@/lib/api"
import { cn } from "@/lib/utils"
import { useDeletedFeaturesStore } from "@/stores/deleted-features-store"
import { useSelectionStore } from "@/stores/selection-store"
import { useToastStore } from "@/stores/toast-store"

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
    // Definite height (viewport minus the h-14 app header) rather than h-full:
    // the app shell wrapper is min-h-svh, so a long class list would grow the
    // document and the sidebar's overflow-y-auto would never engage. Capping
    // here bounds both panes so each scrolls internally.
    <div className="flex h-[calc(100svh-3.5rem)]">
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
          // Keyed by class so switching classes remounts and resets its local
          // state (expanded row + the bulk-print selection).
          <ClassRows key={selectedClass.code} fc={selectedClass} />
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

  // Bulk asset-tag print. Only a tags:read holder sees the checkboxes + print
  // control (printing a label is a read); the print_count bump on the printed
  // sheet additionally needs tags:write, so a read-only user can still print, it
  // just won't count. See use-asset-tags.
  const canReadTags = usePermission()("tags:read")
  const canWriteTags = usePermission()("tags:write")
  // Bulk class wipe (the "nuke") is admin-tier: only a features:wipe holder sees
  // the Danger zone control. The server re-checks the same permission and
  // territory scope, so this gate is a UI convenience, not the security boundary.
  const canWipeClass = usePermission()("features:wipe")
  // Bulk soft-delete of the selected rows is an ordinary features:write action
  // (same DELETE the map's single-feature delete uses); the server re-checks
  // write scope per feature, so this gate is a UI convenience.
  const canDelete = usePermission()("features:write")
  const queryClient = useQueryClient()
  const [wipeOpen, setWipeOpen] = useState(false)
  const [deleteOpen, setDeleteOpen] = useState(false)
  const [selectedIds, setSelectedIds] = useState<Set<string>>(new Set())
  const [printTags, setPrintTags] = useState<PrintTag[] | null>(null)
  const [shortfall, setShortfall] = useState(0)
  const primaryBatch = usePrimaryTagsBatch()
  const markManyPrinted = useMarkManyPrinted()

  const subtypeName = useMemo(() => {
    const map = new Map<number, string>()
    for (const s of schema?.subtypes ?? []) map.set(s.code, s.name)
    return map
  }, [schema])

  const rows = data?.features ?? []

  // Feature name by id, so a printed label's subtitle names the asset a crew is
  // sticking it on rather than a bare uuid.
  const nameById = useMemo(() => {
    const m = new Map<string, string>()
    for (const r of rows) m.set(r.id, r.name ?? "(unnamed)")
    return m
  }, [rows])

  const allSelected = rows.length > 0 && selectedIds.size === rows.length
  const someSelected = selectedIds.size > 0

  function toggleOne(id: string, checked: boolean) {
    setSelectedIds((cur) => {
      const next = new Set(cur)
      if (checked) next.add(id)
      else next.delete(id)
      return next
    })
  }

  function toggleAll(checked: boolean) {
    setSelectedIds(checked ? new Set(rows.map((r) => r.id)) : new Set())
  }

  // Soft-delete every selected row (restorable server-side, the same DELETE the
  // map's single delete uses). allSettled so a partial failure (a row outside
  // the caller's write scope) doesn't abandon the rows that did delete: we refresh
  // the list and keep only the failures selected, then surface the shortfall. A
  // thrown message is caught by ConfirmDeleteDialog and shown as a toast.
  async function deleteSelected() {
    const ids = [...selectedIds]
    if (ids.length === 0) return
    const results = await Promise.allSettled(
      ids.map((id) => apiFetch(`/features/${fc.code}/${id}`, { method: "DELETE" })),
    )
    const failedIds = ids.filter((_, i) => results[i].status === "rejected")
    ids.forEach((id, i) => {
      if (results[i].status === "fulfilled") useDeletedFeaturesStore.getState().markDeleted(id)
    })
    queryClient.invalidateQueries({ queryKey: ["feature-rows", fc.code] })
    queryClient.invalidateQueries({ queryKey: ["diagram-placements"] })
    if (failedIds.length > 0) {
      setSelectedIds(new Set(failedIds))
      throw new Error(
        `Deleted ${ids.length - failedIds.length} of ${ids.length}; ${failedIds.length} could not be deleted (permission or territory scope).`,
      )
    }
    setSelectedIds(new Set())
    useToastStore.getState().pushSuccess(`Deleted ${ids.length} feature${ids.length === 1 ? "" : "s"}.`)
  }

  async function printSelected() {
    const ids = [...selectedIds]
    if (ids.length === 0) return
    try {
      const tags = await primaryBatch.mutateAsync({ featureClassCode: fc.code, featureIds: ids })
      const pts: PrintTag[] = tags.map((t) => ({
        code: t.tag_code,
        subtitle: `${nameById.get(t.feature_id) ?? t.feature_id.slice(0, 8)} · ${fc.code}`,
      }))
      // A feature with no active tag is absent from the result; report the gap.
      setShortfall(ids.length - pts.length)
      setPrintTags(pts)
    } catch {
      // The mutation records the error (primaryBatch.isError); the bar surfaces it.
    }
  }

  return (
    <div className="flex flex-col">
      <div className="bg-background sticky top-0 z-10 border-b p-3">
        <div className="flex items-center gap-2">
          <h2 className="text-sm font-semibold">{fc.name}</h2>
          <span className="text-muted-foreground font-mono text-xs">{fc.code}</span>
          {canWipeClass && (
            <Button
              variant="ghost"
              size="sm"
              className="text-destructive hover:bg-destructive/10 hover:text-destructive ml-auto"
              onClick={() => setWipeOpen(true)}
              title="Bulk soft-delete / purge every feature in this class"
            >
              <Trash2 className="size-4" />
              Wipe class
            </Button>
          )}
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

        {/* Selection bar -- shown to a tags:read holder (bulk print) and/or a
            features:write holder (bulk delete), once rows load. */}
        {(canReadTags || canDelete) && !isPending && !isError && rows.length > 0 && (
          <div className="mt-2 flex flex-wrap items-center gap-3 border-t pt-2">
            <label className="text-muted-foreground flex items-center gap-1.5 text-xs">
              <input
                type="checkbox"
                checked={allSelected}
                ref={(el) => {
                  if (el) el.indeterminate = someSelected && !allSelected
                }}
                onChange={(e) => toggleAll(e.target.checked)}
              />
              Select all
            </label>
            {someSelected && (
              <>
                <span className="text-muted-foreground text-xs">{selectedIds.size} selected</span>
                {canReadTags && (
                  <Button size="sm" onClick={printSelected} disabled={primaryBatch.isPending}>
                    {primaryBatch.isPending ? (
                      <Loader2 className="size-4 animate-spin" />
                    ) : (
                      <Printer className="size-4" />
                    )}
                    Print tags ({selectedIds.size})
                  </Button>
                )}
                {canDelete && (
                  <Button
                    size="sm"
                    variant="outline"
                    className="text-destructive hover:bg-destructive/10 hover:text-destructive"
                    onClick={() => setDeleteOpen(true)}
                  >
                    <Trash2 className="size-4" />
                    Delete ({selectedIds.size})
                  </Button>
                )}
                <Button variant="ghost" size="sm" onClick={() => setSelectedIds(new Set())}>
                  Clear
                </Button>
              </>
            )}
            {primaryBatch.isError && (
              <span className="text-destructive text-xs">Could not load tags. Try again.</span>
            )}
          </div>
        )}
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
                selectable={canReadTags || canDelete}
                selected={selectedIds.has(row.id)}
                onSelectChange={(checked) => toggleOne(row.id, checked)}
                subtypeLabel={row.subtype != null ? (subtypeName.get(row.subtype) ?? String(row.subtype)) : "—"}
                fieldOrder={schema?.fields.map((f) => f.name) ?? Object.keys(row.attributes)}
                expanded={expanded === row.id}
                onToggle={() => setExpanded((cur) => (cur === row.id ? undefined : row.id))}
              />
            ))}
          </div>
        )}
      </div>

      {canWipeClass && (
        <ClassWipeDialog
          featureClassCode={fc.code}
          featureClassName={fc.name}
          open={wipeOpen}
          onOpenChange={setWipeOpen}
        />
      )}

      <ConfirmDeleteDialog
        open={deleteOpen}
        title={`Delete ${selectedIds.size} selected feature${selectedIds.size === 1 ? "" : "s"}?`}
        description="This soft-deletes the selected features — they disappear from the map and lists immediately. They can be restored later (from the map's deleted-features view), but not from here."
        confirmLabel={`Delete ${selectedIds.size}`}
        onOpenChange={setDeleteOpen}
        onConfirm={deleteSelected}
      />

      {printTags && (
        <TagPrintSheet
          tags={printTags}
          note={
            shortfall > 0
              ? `${shortfall} selected feature${shortfall === 1 ? "" : "s"} had no tag and ${shortfall === 1 ? "was" : "were"} skipped.`
              : undefined
          }
          onClose={() => {
            setPrintTags(null)
            setShortfall(0)
          }}
          onPrinted={() => {
            if (canWriteTags && printTags.length > 0) {
              markManyPrinted.mutate({ codes: printTags.map((t) => t.code) })
            }
          }}
        />
      )}
    </div>
  )
}

function RowItem({
  classCode,
  row,
  selectable,
  selected,
  onSelectChange,
  subtypeLabel,
  fieldOrder,
  expanded,
  onToggle,
}: {
  classCode: string
  row: FeatureRecord
  selectable: boolean
  selected: boolean
  onSelectChange: (checked: boolean) => void
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
        {selectable && (
          <input
            type="checkbox"
            className="shrink-0"
            checked={selected}
            onChange={(e) => onSelectChange(e.target.checked)}
            aria-label={`Select ${row.name ?? "feature"} for tag printing`}
          />
        )}
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
          {/* Private-drafts Phase B: the acting user has a private, unpublished
              edit/delete of this published row that only they see. */}
          {row.pending_private_edit && (
            <Badge
              variant="outline"
              className="border-primary/40 text-primary shrink-0 gap-1 text-[10px]"
              title={
                row.pending_private_delete
                  ? "You have a private, unpublished deletion of this feature"
                  : "You have a private, unpublished edit of this feature"
              }
            >
              <FileClock className="size-3" />
              {row.pending_private_delete ? "Private delete" : "Private edit"}
            </Badge>
          )}
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
