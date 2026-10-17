import { useEffect, useState } from "react"
import { Loader2, Map as MapIcon, Save } from "lucide-react"

import { BasemapsManager } from "@/components/admin/basemaps-manager"
import { ServiceLayersManager } from "@/components/admin/service-layers-manager"
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
  // Optional operating-area bounds (service-territory bbox). Strings; all four blank
  // = unbounded (map roams the world). All four together or none — a partial bbox
  // can't constrain a view.
  const [bMinLng, setBMinLng] = useState("")
  const [bMinLat, setBMinLat] = useState("")
  const [bMaxLng, setBMaxLng] = useState("")
  const [bMaxLat, setBMaxLat] = useState("")

  useEffect(() => {
    if (!data) return
    setLng(String(data.center_lng))
    setLat(String(data.center_lat))
    setZoom(String(data.zoom))
    setAnnMin(data.annotation_min_zoom === null ? "" : String(data.annotation_min_zoom))
    setAnnMax(data.annotation_max_zoom === null ? "" : String(data.annotation_max_zoom))
    setBMinLng(data.bounds_min_lng === null ? "" : String(data.bounds_min_lng))
    setBMinLat(data.bounds_min_lat === null ? "" : String(data.bounds_min_lat))
    setBMaxLng(data.bounds_max_lng === null ? "" : String(data.bounds_max_lng))
    setBMaxLat(data.bounds_max_lat === null ? "" : String(data.bounds_max_lat))
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

  // Operating bounds: all four blank (unbounded), or all four filled with a valid,
  // non-inverted bbox inside the geographic domain.
  const bFilled = [bMinLng, bMinLat, bMaxLng, bMaxLat].filter((s) => s.trim() !== "").length
  const bMinLngN = Number(bMinLng)
  const bMinLatN = Number(bMinLat)
  const bMaxLngN = Number(bMaxLng)
  const bMaxLatN = Number(bMaxLat)
  const boundsComplete = bFilled === 4
  const boundsValid =
    bFilled === 0 ||
    (boundsComplete &&
      [bMinLngN, bMaxLngN].every((n) => Number.isFinite(n) && n >= -180 && n <= 180) &&
      [bMinLatN, bMaxLatN].every((n) => Number.isFinite(n) && n >= -90 && n <= 90) &&
      bMinLngN < bMaxLngN &&
      bMinLatN < bMaxLatN)

  const canSave = lngValid && latValid && zoomValid && annValid && boundsValid && !update.isPending

  async function handleSave() {
    if (!canSave) return
    try {
      await update.mutateAsync({
        center_lng: lngNum,
        center_lat: latNum,
        zoom: zoomNum,
        annotation_min_zoom: annMinBound,
        annotation_max_zoom: annMaxBound,
        bounds_min_lng: boundsComplete ? bMinLngN : null,
        bounds_min_lat: boundsComplete ? bMinLatN : null,
        bounds_max_lng: boundsComplete ? bMaxLngN : null,
        bounds_max_lat: boundsComplete ? bMaxLatN : null,
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

            <div className="border-t pt-4">
              <h2 className="text-sm font-medium">Operating area (map bounds)</h2>
              <p className="text-muted-foreground text-sm">
                Constrain the map to the utility&apos;s service territory — a lon/lat box the map won&apos;t pan or
                zoom outside, so no tiles are fetched for empty areas. Enter all four corners to bound it, or
                leave all blank to let the map roam the whole world (the default). You can read corner
                coordinates off the map&apos;s lat/lng readout.
              </p>
              <div className="mt-3 grid grid-cols-1 gap-3 sm:grid-cols-2">
                <div className="flex flex-col gap-1">
                  <label className="text-xs font-medium">Min longitude (west)</label>
                  <input
                    type="number"
                    step="any"
                    className={inputClass}
                    value={bMinLng}
                    onChange={(e) => setBMinLng(e.target.value)}
                    placeholder="none"
                    aria-invalid={!boundsValid}
                  />
                </div>
                <div className="flex flex-col gap-1">
                  <label className="text-xs font-medium">Min latitude (south)</label>
                  <input
                    type="number"
                    step="any"
                    className={inputClass}
                    value={bMinLat}
                    onChange={(e) => setBMinLat(e.target.value)}
                    placeholder="none"
                    aria-invalid={!boundsValid}
                  />
                </div>
                <div className="flex flex-col gap-1">
                  <label className="text-xs font-medium">Max longitude (east)</label>
                  <input
                    type="number"
                    step="any"
                    className={inputClass}
                    value={bMaxLng}
                    onChange={(e) => setBMaxLng(e.target.value)}
                    placeholder="none"
                    aria-invalid={!boundsValid}
                  />
                </div>
                <div className="flex flex-col gap-1">
                  <label className="text-xs font-medium">Max latitude (north)</label>
                  <input
                    type="number"
                    step="any"
                    className={inputClass}
                    value={bMaxLat}
                    onChange={(e) => setBMaxLat(e.target.value)}
                    placeholder="none"
                    aria-invalid={!boundsValid}
                  />
                </div>
              </div>
              {!boundsValid && (
                <span className="text-destructive text-xs">
                  Enter all four corners (or none), each within range, with min below max.
                </span>
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

        {/* The external service-layer catalog -- 3rd-party WMS/WMTS/XYZ/vector-
            tile/Esri/WFS overlays the map can stack on the basemap. Same
            standalone shape as the basemap manager (own query + write path in
            hooks/use-service-layers.ts). */}
        <ServiceLayersManager />
      </div>
    </div>
  )
}
