import { useEffect, useMemo, useRef, useState } from "react"
import { ChevronDown, Loader2 } from "lucide-react"

import { inputClass } from "@/components/map/field-utils"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import {
  DropdownMenu,
  DropdownMenuCheckboxItem,
  DropdownMenuContent,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { useEnqueueExport } from "@/hooks/use-export-jobs"
import { useFeatureClasses } from "@/hooks/use-feature-classes"
import { type TerritorySummary, useTerritories } from "@/hooks/use-territories"
import { ApiError } from "@/lib/api"
import { CRS_GROUPS } from "@/lib/crs"
import { cn } from "@/lib/utils"
import { useExportStore } from "@/stores/export-store"
import { useLayersStore } from "@/stores/layers-store"
import { useMapViewStore } from "@/stores/map-view-store"
import { useToastStore } from "@/stores/toast-store"

type FormatKey = "kmz" | "shp" | "dxf" | "cim"

const FORMATS: { value: FormatKey; label: string; hint: string }[] = [
  { value: "kmz", label: "KMZ", hint: "Google Earth" },
  { value: "shp", label: "Shapefile", hint: "zipped .shp/.dbf, for GIS" },
  { value: "dxf", label: "DXF", hint: "AutoCAD / CAD" },
  { value: "cim", label: "CIM/XML", hint: "CDPSM RDF/XML, for power-system tools" },
]

/**
 * Formats whose geometry the server can reproject to a chosen target CRS on the
 * way out (via PostGIS ST_Transform, see the export reprojection engine):
 * Shapefile stamps the .prj, DXF plots the reprojected metres, and CIM writes
 * its GL PositionPoints in the target CRS and declares it on the CoordinateSystem.
 * KMZ is excluded -- KML is spec-locked to WGS 84 -- so it keeps the stored
 * EPSG:4326 and the picker is hidden for it. The backend enforces the same rule
 * (api/internal/export/export.go reprojectableFormats).
 */
const REPROJECTABLE_FORMATS: ReadonlySet<FormatKey> = new Set(["shp", "dxf", "cim"])

/**
 * The three export scopes. "view" clips to the current map viewport (needs a
 * reported bbox); "whole" exports every feature of each selected class; and
 * "territory" clips to a chosen set of territories -- expanded server-side to
 * their subtrees, since a feature is stamped to the DEEPEST territory that
 * contains it (see api/internal/territory/descendants.go).
 */
type Scope = "view" | "whole" | "territory"

interface TerritoryRow {
  t: TerritorySummary
  depth: number
}

/**
 * Flattens the territory adjacency list into a depth-first, indented order
 * for the picker: roots first (parent_id === null), each followed by its
 * subtree, siblings sorted by name. `depth` drives the indent. Any territory
 * whose parent isn't in the list is treated as a root so it can never be
 * orphaned out of the picker.
 */
function flattenTerritories(list: TerritorySummary[]): TerritoryRow[] {
  const byParent = new Map<string | null, TerritorySummary[]>()
  const ids = new Set(list.map((t) => t.id))
  for (const t of list) {
    const key = t.parent_id && ids.has(t.parent_id) ? t.parent_id : null
    const bucket = byParent.get(key)
    if (bucket) bucket.push(t)
    else byParent.set(key, [t])
  }
  for (const bucket of byParent.values()) bucket.sort((a, b) => a.name.localeCompare(b.name))

  const out: TerritoryRow[] = []
  const walk = (parent: string | null, depth: number) => {
    for (const t of byParent.get(parent) ?? []) {
      out.push({ t, depth })
      walk(t.id, depth + 1)
    }
  }
  walk(null, 0)
  return out
}

/**
 * The geographic export dialog (Phase 14 Slices 4-5). Pick a format, pick
 * which feature classes to export -- pre-checked to whatever's currently
 * visible on the map -- and pick a scope: clip to the current viewport
 * (default), export each class's whole extent, or clip to a set of chosen
 * territories (expanded server-side to their subtrees). Since Phase 17 an
 * export is an async job: this POSTs to /export, which enqueues the work and
 * returns a job id, then it hands off to the jobs panel (which polls for the
 * download link). The class list is filtered to geometry-bearing classes (a
 * geometry-less class can't be placed on a map, and the endpoint would 400 it
 * anyway).
 */
export function ExportDialog() {
  const open = useExportStore((s) => s.open)
  const { data: classes } = useFeatureClasses()
  const { data: territories } = useTerritories()
  const hidden = useLayersStore((s) => s.hidden)
  const bbox = useMapViewStore((s) => s.bbox)
  const enqueue = useEnqueueExport()

  const [format, setFormat] = useState<FormatKey>("kmz")
  // Target CRS to reproject into on export; null keeps the stored EPSG:4326.
  // Only meaningful for the reprojectable formats -- gated in the body below.
  const [targetSrid, setTargetSrid] = useState<number | null>(null)
  const [scope, setScope] = useState<Scope>("view")
  const [selected, setSelected] = useState<Record<string, boolean>>({})
  const [selectedTerritories, setSelectedTerritories] = useState<Record<string, boolean>>({})
  const exporting = enqueue.isPending

  const exportable = useMemo(
    () =>
      (classes?.feature_classes ?? [])
        .filter((c) => c.geometry_type !== "none")
        .sort((a, b) => a.name.localeCompare(b.name)),
    [classes],
  )

  // Seed the checkboxes from the map's current visibility, once per time the
  // dialog opens (source id is the lowercased class code, see lib/martin.ts; a
  // class absent from `hidden` is visible). Re-running only on open -- not on
  // every `hidden` change -- so toggling a checkbox here doesn't get clobbered
  // by an unrelated layer toggle while the dialog is up.
  const seededRef = useRef(false)
  useEffect(() => {
    if (!open) {
      seededRef.current = false
      return
    }
    if (seededRef.current || exportable.length === 0) return
    const next: Record<string, boolean> = {}
    for (const c of exportable) next[c.code] = !hidden[c.code.toLowerCase()]
    setSelected(next)
    // Default to a view-clipped export, but fall back to whole-dataset when
    // the map hasn't reported a viewport yet (nothing to clip to).
    setScope(bbox != null ? "view" : "whole")
    setSelectedTerritories({})
    setTargetSrid(null)
    seededRef.current = true
  }, [open, exportable, hidden, bbox])

  const selectedCodes = exportable.filter((c) => selected[c.code]).map((c) => c.code)
  const allSelected = exportable.length > 0 && selectedCodes.length === exportable.length

  const territoryRows = useMemo(
    () => flattenTerritories(territories?.territories ?? []),
    [territories],
  )
  const selectedTerritoryIds = territoryRows.filter((r) => selectedTerritories[r.t.id]).map((r) => r.t.id)

  // Clipping needs a viewport bbox; if the map hasn't reported one yet, only a
  // whole-dataset (or territory) export is possible.
  const canClip = bbox != null
  const clip = scope === "view" && canClip
  // The target-CRS picker only shows for formats the server will reproject; the
  // others always emit the stored EPSG:4326, so a target would be meaningless
  // (and the backend would 400 it).
  const supportsCrs = REPROJECTABLE_FORMATS.has(format)
  // A territory export must name at least one territory, or it would resolve to
  // "match nothing" and produce empty files.
  const territoryReady = scope !== "territory" || selectedTerritoryIds.length > 0

  function close() {
    useExportStore.getState().closeDialog()
  }

  function toggleAll() {
    const next: Record<string, boolean> = {}
    for (const c of exportable) next[c.code] = !allSelected
    setSelected(next)
  }

  async function handleExport() {
    if (selectedCodes.length === 0 || !territoryReady) return
    try {
      const body: {
        format: FormatKey
        feature_classes: string[]
        bbox?: number[]
        territories?: string[]
        target_srid?: number
      } = {
        format,
        feature_classes: selectedCodes,
      }
      if (clip && bbox) body.bbox = bbox
      if (scope === "territory") body.territories = selectedTerritoryIds
      // Only carry a target CRS for the reprojectable formats; otherwise the
      // export keeps the stored EPSG:4326.
      if (supportsCrs && targetSrid != null) body.target_srid = targetSrid
      await enqueue.mutateAsync(body)
      // The export now runs server-side; close the config dialog and open the
      // jobs panel, which polls for the artifact and surfaces its download link.
      close()
      useExportStore.getState().openJobsPanel()
      useToastStore
        .getState()
        .pushSuccess(
          `Export queued for ${selectedCodes.length} layer${selectedCodes.length === 1 ? "" : "s"}.`,
        )
    } catch (err) {
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Export failed.")
    }
  }

  return (
    <Dialog open={open} onOpenChange={(next) => !exporting && !next && close()}>
      <DialogContent className="max-w-md">
        <DialogHeader>
          <DialogTitle>Export</DialogTitle>
          <DialogDescription>
            Download the selected layers with their geometry and attributes.
          </DialogDescription>
        </DialogHeader>

        <div className="space-y-4">
          {/* Format */}
          <div className="space-y-1.5">
            <label htmlFor="export-format" className="text-sm font-medium">
              Format
            </label>
            <select
              id="export-format"
              className={inputClass}
              value={format}
              onChange={(e) => setFormat(e.target.value as FormatKey)}
              disabled={exporting}
            >
              {FORMATS.map((f) => (
                <option key={f.value} value={f.value}>
                  {f.label} — {f.hint}
                </option>
              ))}
            </select>
          </div>

          {/* Coordinate system -- only for formats the server can reproject */}
          {supportsCrs && (
            <div className="space-y-1.5">
              <label htmlFor="export-crs" className="text-sm font-medium">
                Coordinate system
              </label>
              <select
                id="export-crs"
                className={inputClass}
                value={targetSrid == null ? "" : String(targetSrid)}
                onChange={(e) =>
                  setTargetSrid(e.target.value === "" ? null : Number(e.target.value))
                }
                disabled={exporting}
              >
                <option value="">WGS 84 · EPSG:4326 (as stored — no reprojection)</option>
                {CRS_GROUPS.map((g) => {
                  // 4326 is the "as stored" default above; drop it from the list.
                  const items = g.items.filter((c) => c.code !== "EPSG:4326")
                  if (items.length === 0) return null
                  return (
                    <optgroup key={g.label} label={g.label}>
                      {items.map((c) => (
                        <option key={c.code} value={c.code.slice(5)}>
                          {c.name} · {c.code}
                        </option>
                      ))}
                    </optgroup>
                  )
                })}
              </select>
              <p className="text-muted-foreground text-xs">
                Reprojected on export. CAD and GIS targets usually want a
                projected CRS in metres (e.g. the local UTM zone), not degrees.
              </p>
            </div>
          )}

          {/* Scope */}
          <div className="space-y-1.5">
            <label htmlFor="export-scope" className="text-sm font-medium">
              Scope
            </label>
            <select
              id="export-scope"
              className={inputClass}
              value={scope}
              onChange={(e) => setScope(e.target.value as Scope)}
              disabled={exporting}
            >
              <option value="view" disabled={!canClip}>
                Current view {canClip ? "— features in the current map view" : "— unavailable"}
              </option>
              <option value="whole">Whole dataset — every feature of each selected class</option>
              <option value="territory" disabled={territoryRows.length === 0}>
                Territories {territoryRows.length === 0 ? "— none defined" : "— chosen territories and beneath"}
              </option>
            </select>

            {scope === "territory" && (
              <div className="space-y-1">
                <DropdownMenu>
                  <DropdownMenuTrigger asChild>
                    <button
                      type="button"
                      disabled={exporting}
                      className={cn(
                        inputClass,
                        "flex items-center justify-between gap-2 text-left disabled:opacity-50",
                      )}
                    >
                      <span
                        className={cn("truncate", selectedTerritoryIds.length === 0 && "text-muted-foreground")}
                      >
                        {selectedTerritoryIds.length === 0
                          ? "Select territories…"
                          : `${selectedTerritoryIds.length} territor${selectedTerritoryIds.length === 1 ? "y" : "ies"} selected`}
                      </span>
                      <ChevronDown className="size-4 shrink-0 opacity-50" />
                    </button>
                  </DropdownMenuTrigger>
                  <DropdownMenuContent
                    align="start"
                    className="max-h-64 w-[var(--radix-dropdown-menu-trigger-width)] overflow-y-auto"
                  >
                    {territoryRows.map(({ t, depth }) => (
                      <DropdownMenuCheckboxItem
                        key={t.id}
                        checked={Boolean(selectedTerritories[t.id])}
                        onCheckedChange={(v) =>
                          setSelectedTerritories((s) => ({ ...s, [t.id]: v === true }))
                        }
                        // Keep the menu open so several territories can be picked
                        // in one visit instead of reopening per toggle.
                        onSelect={(e) => e.preventDefault()}
                      >
                        <span
                          className="flex flex-1 items-center gap-2"
                          style={{ paddingLeft: `${depth * 14}px` }}
                        >
                          <span className="flex-1 truncate">{t.name}</span>
                          <span className="text-muted-foreground text-xs">{t.level_name}</span>
                        </span>
                      </DropdownMenuCheckboxItem>
                    ))}
                  </DropdownMenuContent>
                </DropdownMenu>
                {!territoryReady && (
                  <p className="text-destructive text-xs">Select at least one territory.</p>
                )}
              </div>
            )}
          </div>

          {/* Layers */}
          <fieldset className="space-y-1.5">
            <div className="flex items-center justify-between">
              <legend className="text-sm font-medium">Layers</legend>
              {exportable.length > 0 && (
                <button
                  type="button"
                  onClick={toggleAll}
                  disabled={exporting}
                  className="text-muted-foreground hover:text-foreground text-xs underline-offset-2 hover:underline"
                >
                  {allSelected ? "Clear all" : "Select all"}
                </button>
              )}
            </div>
            {exportable.length === 0 ? (
              <p className="text-muted-foreground text-sm">No exportable layers.</p>
            ) : (
              <div className="max-h-56 space-y-1 overflow-y-auto rounded-md border p-2">
                {exportable.map((c) => (
                  <label key={c.code} className="flex items-center gap-2 text-sm">
                    <input
                      type="checkbox"
                      checked={Boolean(selected[c.code])}
                      onChange={(e) => setSelected((s) => ({ ...s, [c.code]: e.target.checked }))}
                      disabled={exporting}
                    />
                    <span className="flex-1 truncate">{c.name}</span>
                    <span className="text-muted-foreground text-xs">{c.geometry_type}</span>
                  </label>
                ))}
              </div>
            )}
          </fieldset>
        </div>

        <DialogFooter>
          <Button
            type="button"
            variant="outline"
            onClick={(e) => {
              e.preventDefault()
              close()
            }}
            disabled={exporting}
          >
            Cancel
          </Button>
          <Button
            type="button"
            onClick={(e) => {
              e.preventDefault()
              handleExport()
            }}
            disabled={exporting || selectedCodes.length === 0 || !territoryReady}
          >
            {exporting && <Loader2 className="size-4 animate-spin" />}
            Export{selectedCodes.length > 0 ? ` (${selectedCodes.length})` : ""}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
