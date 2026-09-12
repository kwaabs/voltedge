import { useEffect, useState } from "react"
import { Loader2, Map as MapIcon, Save } from "lucide-react"

import { BasemapsManager } from "@/components/admin/basemaps-manager"
import { inputClass } from "@/components/map/field-utils"
import { Button } from "@/components/ui/button"
import { useMapSettings, useUpdateMapSettings } from "@/hooks/use-map-settings"
import { useToastStore } from "@/stores/toast-store"

/**
 * The default map view an admin sets -- the center lon/lat and zoom the
 * geographic map opens on when the app starts (GET/PUT /map-settings, backed by
 * iam.system_settings; see docs/phase-39-map-settings.md). Before this it was a
 * hardcoded London placeholder in voltedge-map.tsx; now a deployment opens on
 * its own network without a rebuild.
 *
 * A flat three-field form -- longitude, latitude, zoom -- since the setting is
 * three scalars. Editing here changes only what *new* map sessions open on;
 * sessions already open keep their current viewport.
 */
export function AdminMapSettingsPage() {
  const { data, isPending, isError } = useMapSettings()
  const update = useUpdateMapSettings()

  // Local, string-backed form state seeded from the loaded setting. Kept as
  // strings so a partially-typed number (e.g. "-0.") doesn't fight the input;
  // parsed on save.
  const [lng, setLng] = useState("")
  const [lat, setLat] = useState("")
  const [zoom, setZoom] = useState("")
  // The optional global annotation visible-zoom band. Kept as strings; blank
  // means "no bound on that side" (persisted as JSON null), so an empty field
  // is a valid, meaningful state here rather than an error.
  const [annMin, setAnnMin] = useState("")
  const [annMax, setAnnMax] = useState("")

  useEffect(() => {
    if (!data) return
    setLng(String(data.center_lng))
    setLat(String(data.center_lat))
    setZoom(String(data.zoom))
    setAnnMin(data.annotation_min_zoom === null ? "" : String(data.annotation_min_zoom))
    setAnnMax(data.annotation_max_zoom === null ? "" : String(data.annotation_max_zoom))
  }, [data])

  const lngNum = Number(lng)
  const latNum = Number(lat)
  const zoomNum = Number(zoom)

  // Mirror the server's validation (api/internal/mapsettings) so the button
  // guides rather than round-trips to a 400.
  const lngValid = lng.trim() !== "" && Number.isFinite(lngNum) && lngNum >= -180 && lngNum <= 180
  const latValid = lat.trim() !== "" && Number.isFinite(latNum) && latNum >= -90 && latNum <= 90
  const zoomValid = zoom.trim() !== "" && Number.isFinite(zoomNum) && zoomNum >= 0 && zoomNum <= 22

  // Annotation band bounds are each optional: blank is valid (no bound). A
  // filled bound must be a 0..22 number, and a filled min must not exceed a
  // filled max -- the same rules the server enforces.
  const annMinBound = annMin.trim() === "" ? null : Number(annMin)
  const annMaxBound = annMax.trim() === "" ? null : Number(annMax)
  const annMinValid = annMinBound === null || (Number.isFinite(annMinBound) && annMinBound >= 0 && annMinBound <= 22)
  const annMaxValid = annMaxBound === null || (Number.isFinite(annMaxBound) && annMaxBound >= 0 && annMaxBound <= 22)
  const annRangeValid = annMinBound === null || annMaxBound === null || annMinBound <= annMaxBound
  const annValid = annMinValid && annMaxValid && annRangeValid

  const canSave = lngValid && latValid && zoomValid && annValid && !update.isPending

  async function handleSave() {
    if (!canSave) return
    try {
      await update.mutateAsync({
        center_lng: lngNum,
        center_lat: latNum,
        zoom: zoomNum,
        annotation_min_zoom: annMinBound,
        annotation_max_zoom: annMaxBound,
      })
      useToastStore.getState().pushSuccess("Map settings saved.")
    } catch {
      useToastStore.getState().pushError("Could not save the map settings.")
    }
  }

  return (
    <div className="h-full overflow-y-auto p-4">
      <div className="mx-auto flex max-w-2xl flex-col gap-4">
        <div className="flex items-center gap-2">
          <MapIcon className="text-muted-foreground size-5" />
          <div>
            <h1 className="text-lg font-semibold">Map settings</h1>
            <p className="text-muted-foreground text-sm">
              The center and zoom the map opens on at startup. Applies to new map sessions; open maps keep
              their current view.
            </p>
          </div>
        </div>

        {isPending ? (
          <div className="text-muted-foreground flex items-center gap-2 text-sm">
            <Loader2 className="size-4 animate-spin" />
            Loading map settings...
          </div>
        ) : isError ? (
          <p className="text-destructive text-sm">Could not load map settings.</p>
        ) : (
          <div className="flex flex-col gap-4 rounded-lg border p-4">
            <div className="grid grid-cols-1 gap-3 sm:grid-cols-3">
              <div className="flex flex-col gap-1">
                <label className="text-xs font-medium">
                  Longitude <span className="text-destructive">*</span>
                </label>
                <input
                  type="number"
                  step="any"
                  className={inputClass}
                  value={lng}
                  onChange={(e) => setLng(e.target.value)}
                  placeholder="-0.127"
                  aria-invalid={!lngValid}
                />
                {!lngValid && <span className="text-destructive text-xs">-180 to 180</span>}
              </div>
              <div className="flex flex-col gap-1">
                <label className="text-xs font-medium">
                  Latitude <span className="text-destructive">*</span>
                </label>
                <input
                  type="number"
                  step="any"
                  className={inputClass}
                  value={lat}
                  onChange={(e) => setLat(e.target.value)}
                  placeholder="51.507"
                  aria-invalid={!latValid}
                />
                {!latValid && <span className="text-destructive text-xs">-90 to 90</span>}
              </div>
              <div className="flex flex-col gap-1">
                <label className="text-xs font-medium">
                  Zoom <span className="text-destructive">*</span>
                </label>
                <input
                  type="number"
                  step="any"
                  className={inputClass}
                  value={zoom}
                  onChange={(e) => setZoom(e.target.value)}
                  placeholder="15"
                  aria-invalid={!zoomValid}
                />
                {!zoomValid && <span className="text-destructive text-xs">0 to 22</span>}
              </div>
            </div>

            <div className="border-t pt-4">
              <h2 className="text-sm font-medium">Annotation visibility</h2>
              <p className="text-muted-foreground text-sm">
                The zoom band map annotations (notes, redlines) show within. Outside it the whole
                annotations overlay hides, so markup doesn&apos;t clutter an overview zoom. Leave a
                field blank for no bound on that side; leave both blank to always show annotations.
              </p>
              <div className="mt-3 grid grid-cols-1 gap-3 sm:grid-cols-2">
                <div className="flex flex-col gap-1">
                  <label className="text-xs font-medium">Min zoom</label>
                  <input
                    type="number"
                    step="any"
                    className={inputClass}
                    value={annMin}
                    onChange={(e) => setAnnMin(e.target.value)}
                    placeholder="none"
                    aria-invalid={!annMinValid || !annRangeValid}
                  />
                  {!annMinValid && <span className="text-destructive text-xs">0 to 22</span>}
                </div>
                <div className="flex flex-col gap-1">
                  <label className="text-xs font-medium">Max zoom</label>
                  <input
                    type="number"
                    step="any"
                    className={inputClass}
                    value={annMax}
                    onChange={(e) => setAnnMax(e.target.value)}
                    placeholder="none"
                    aria-invalid={!annMaxValid || !annRangeValid}
                  />
                  {!annMaxValid && <span className="text-destructive text-xs">0 to 22</span>}
                </div>
              </div>
              {!annRangeValid && (
                <span className="text-destructive text-xs">Min zoom must not exceed max zoom</span>
              )}
            </div>

            <div className="flex justify-end">
              <Button size="sm" onClick={handleSave} disabled={!canSave}>
                {update.isPending ? <Loader2 className="size-4 animate-spin" /> : <Save className="size-4" />}
                Save
              </Button>
            </div>
          </div>
        )}

        {/* The basemap catalog -- what the map's basemap picker offers. It has
            its own loading/error state and write path (hooks/use-basemaps.ts),
            so it lives beside the default-view form rather than under its
            query. */}
        <BasemapsManager />
      </div>
    </div>
  )
}
