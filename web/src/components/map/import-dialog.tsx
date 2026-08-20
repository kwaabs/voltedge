import { useState } from "react"
import { Loader2 } from "lucide-react"

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
import { CRS_GROUPS } from "@/lib/crs"
import { IMPORT_ACCEPT, type InspectResult, inspectFile, parseFile } from "@/lib/import-formats"
import { useImportedOverlaysStore } from "@/stores/imported-overlays-store"
import { useImportStore } from "@/stores/import-store"
import { useToastStore } from "@/stores/toast-store"

/** Above this serialized size an overlay is loaded for the session only (it
 * can't be persisted without risking the ~5 MB localStorage quota). Held a
 * little under 5 MB for headroom against other persisted stores. */
const MAX_PERSIST_BYTES = 4 * 1024 * 1024

/**
 * The data-import dialog (Phase 15). Pick a GeoJSON, KMZ/KML, or shapefile
 * (`.zip`) file and drop it on the map as a read-only overlay -- it's never
 * written to the database. The parsed features are normalized to a canonical
 * 4326 GeoJSON FeatureCollection (see import-formats.ts, reprojected via the
 * proj4 engine in crs.ts when needed) and stored (persisted) in
 * imported-overlays-store; the map renders them as an amber, dashed reference
 * layer. Slice 4 adds DXF behind the same dialog and source-CRS control.
 */
export function ImportDialog() {
  const open = useImportStore((s) => s.open)
  const [file, setFile] = useState<File | null>(null)
  const [name, setName] = useState("")
  const [busy, setBusy] = useState(false)
  const [inspect, setInspect] = useState<InspectResult | null>(null)
  const [inspecting, setInspecting] = useState(false)
  // Source CRS for formats that need one (a shapefile with no .prj). Defaults to
  // WGS 84 -- the neutral "coordinates are already lat/lon" choice.
  const [sourceCrs, setSourceCrs] = useState("EPSG:4326")

  function close() {
    setFile(null)
    setName("")
    setInspect(null)
    setSourceCrs("EPSG:4326")
    useImportStore.getState().closeDialog()
  }

  async function onPick(picked: File | null) {
    setFile(picked)
    setInspect(null)
    // Default the overlay name to the filename without its extension, but only
    // if the user hasn't already typed one.
    if (picked && !name) setName(picked.name.replace(/\.[^.]+$/, ""))
    if (!picked) return

    // Peek at format/CRS so the dialog can show a detected projection or ask for
    // one before the user commits to importing.
    setInspecting(true)
    try {
      const result = await inspectFile(picked)
      setInspect(result)
      if (result.detectedCrs) setSourceCrs(result.detectedCrs.code)
    } catch {
      setInspect(null)
    } finally {
      setInspecting(false)
    }
  }

  async function handleImport() {
    if (!file) return
    setBusy(true)
    try {
      // Only reproject ourselves when the format couldn't self-georeference
      // (a shapefile with no .prj); otherwise the parser already yields 4326.
      const reprojectFrom = inspect?.requiresCrs ? sourceCrs : undefined
      const { format, geojson, featureCount } = await parseFile(file, { reprojectFrom })

      // Size guard: measure the serialized GeoJSON (what actually gets stored,
      // uniform across formats). A large overlay still loads, but only for this
      // session -- persisting it could blow the localStorage quota.
      const ephemeral = JSON.stringify(geojson).length > MAX_PERSIST_BYTES

      useImportedOverlaysStore.getState().add({
        name: name.trim() || file.name,
        format,
        featureCount,
        geojson,
        ephemeral,
      })

      const toast = useToastStore.getState()
      if (ephemeral) {
        toast.pushSuccess(
          `Imported ${featureCount} features (too large to save -- will be lost on reload).`,
        )
      } else {
        toast.pushSuccess(`Imported ${featureCount} features.`)
      }
      close()
    } catch (err) {
      useToastStore.getState().pushError(err instanceof Error ? err.message : "Import failed.")
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={(next) => !busy && !next && close()}>
      <DialogContent className="max-w-md">
        <DialogHeader>
          <DialogTitle>Import overlay</DialogTitle>
          <DialogDescription>
            Overlay a GeoJSON, KMZ, shapefile, or DXF on the map. It's a read-only reference layer --
            not saved to the database.
          </DialogDescription>
        </DialogHeader>

        <div className="space-y-4">
          <div className="space-y-1.5">
            <label htmlFor="import-file" className="text-sm font-medium">
              File
            </label>
            <input
              id="import-file"
              type="file"
              accept={IMPORT_ACCEPT}
              onChange={(e) => onPick(e.target.files?.[0] ?? null)}
              disabled={busy}
              className="text-muted-foreground file:bg-secondary file:text-secondary-foreground hover:file:bg-secondary/80 block w-full text-sm file:mr-3 file:cursor-pointer file:rounded-md file:border-0 file:px-3 file:py-1.5 file:text-sm file:font-medium"
            />
            <p className="text-muted-foreground text-xs">
              GeoJSON (.geojson, .json), KMZ/KML (.kmz, .kml), a zipped shapefile (.zip), or DXF
              (.dxf).
            </p>
          </div>

          {/* CRS feedback: a detected projection (shapefile with a .prj) shown
              read-only, or a required picker when the file carries no
              georeference. GeoJSON/KMZ are 4326 by spec, so nothing shows. */}
          {inspecting && <p className="text-muted-foreground text-xs">Detecting projection…</p>}
          {inspect?.detectedCrs && (
            <p className="text-muted-foreground text-xs">
              Projection: <span className="text-foreground">{inspect.detectedCrs.name}</span>{" "}
              (detected from .prj)
            </p>
          )}
          {inspect?.requiresCrs && (
            <div className="space-y-1.5">
              <label htmlFor="import-crs" className="text-sm font-medium">
                Source CRS
              </label>
              <select
                id="import-crs"
                value={sourceCrs}
                onChange={(e) => setSourceCrs(e.target.value)}
                disabled={busy}
                className={inputClass}
              >
                {CRS_GROUPS.map((group) => (
                  <optgroup key={group.label} label={group.label}>
                    {group.items.map((crs) => (
                      <option key={crs.code} value={crs.code}>
                        {crs.name} ({crs.code})
                      </option>
                    ))}
                  </optgroup>
                ))}
              </select>
              <p className="text-muted-foreground text-xs">
                {inspect.format === "dxf"
                  ? "DXF drawings carry no coordinate system — pick the CRS the drawing was made in."
                  : "This file has no projection info — choose the CRS its coordinates are in."}
              </p>
            </div>
          )}

          <div className="space-y-1.5">
            <label htmlFor="import-name" className="text-sm font-medium">
              Name
            </label>
            <input
              id="import-name"
              type="text"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="Overlay name"
              disabled={busy}
              className={inputClass}
            />
          </div>
        </div>

        <DialogFooter>
          <Button
            type="button"
            variant="outline"
            onClick={(e) => {
              e.preventDefault()
              close()
            }}
            disabled={busy}
          >
            Cancel
          </Button>
          <Button
            type="button"
            onClick={(e) => {
              e.preventDefault()
              handleImport()
            }}
            disabled={busy || !file || inspecting}
          >
            {busy && <Loader2 className="size-4 animate-spin" />}
            Import
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
