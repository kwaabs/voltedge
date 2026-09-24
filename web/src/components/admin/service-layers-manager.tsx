import { useState } from "react"
import { Eye, EyeOff, Loader2, Pencil, Plus, Search, Trash2, X } from "lucide-react"

import { ConfirmDeleteDialog } from "@/components/admin/confirm-delete-dialog"
import { FormDialog } from "@/components/admin/form-dialog"
import { inputClass } from "@/components/map/field-utils"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import type { AttributionLink } from "@/hooks/use-basemaps"
import {
  type CreateServiceLayerInput,
  type ServiceAuthType,
  type ServiceLayerRecord,
  type ServiceType,
  type UpdateServiceLayerInput,
  useAllServiceLayers,
  useCreateServiceLayer,
  useDeleteServiceLayer,
  useDiscoverServiceLayer,
  useUpdateServiceLayer,
} from "@/hooks/use-service-layers"
import { ApiError } from "@/lib/api"
import {
  type Discovery,
  capabilitiesUrl,
  parseCapabilities,
  supportsDiscovery,
} from "@/lib/service-layers-capabilities"
import { useToastStore } from "@/stores/toast-store"

const labelClass = "text-xs font-medium"

/** The service types, with a human label and a one-line hint about what `url` and
 * `layer_ids` mean for each. Order groups raster first, then vector. */
const SERVICE_TYPES: { value: ServiceType; label: string; hint: string }[] = [
  { value: "wms", label: "WMS (OGC raster)", hint: "url = GetMap/base endpoint · layer_ids = LAYERS (comma-separated)" },
  { value: "wmts", label: "WMTS (OGC raster tiles)", hint: "url = WMTS endpoint · layer_ids = layer identifier" },
  { value: "xyz", label: "XYZ raster tiles", hint: "url = tile template with {z}/{x}/{y}" },
  { value: "vector_tile", label: "Vector tiles (MVT)", hint: "url = tile template with {z}/{x}/{y} · style_url optional" },
  { value: "esri_map", label: "Esri MapServer (raster)", hint: "url = MapServer root · layer_ids = sublayers (optional)" },
  { value: "esri_feature", label: "Esri FeatureServer (vector)", hint: "url = FeatureServer/{id} · layer_ids = layer id (optional)" },
  { value: "wfs", label: "WFS (OGC vector)", hint: "url = WFS base · layer_ids = typeName" },
]

const AUTH_TYPES: { value: ServiceAuthType; label: string }[] = [
  { value: "none", label: "None (public)" },
  { value: "api_key", label: "API key (header or query param)" },
  { value: "basic", label: "Basic (username + password)" },
  { value: "bearer", label: "Bearer token" },
]

/** Types that need a per-layer selector (WMS LAYERS / WMTS layer id / WFS typeName). */
const LAYER_ID_REQUIRED: ServiceType[] = ["wms", "wmts", "wfs"]
/** Types whose url is a {z}/{x}/{y} tile template. */
const TILE_TEMPLATE: ServiceType[] = ["xyz", "vector_tile"]

function toastError(err: unknown, fallback: string) {
  useToastStore.getState().pushError(err instanceof ApiError ? err.message : fallback)
}

function parseNum(raw: string): number | null {
  const t = raw.trim()
  if (t === "") return null
  const n = Number(t)
  return Number.isFinite(n) ? n : null
}

/** Parses "minLon,minLat,maxLon,maxLat" into a 4-number bbox, or undefined when
 * blank or not four numbers (the server treats undefined as "leave unset"). */
function parseExtent(raw: string): number[] | undefined {
  const t = raw.trim()
  if (t === "") return undefined
  const parts = t.split(/[\s,]+/).map(Number)
  if (parts.length !== 4 || parts.some((n) => !Number.isFinite(n))) return undefined
  return parts
}

/** A small repeatable label/href editor for a layer's attribution (same shape as
 * the basemap editor). Blank-label rows are dropped on save. */
function AttributionEditor({
  links,
  onChange,
}: {
  links: AttributionLink[]
  onChange: (next: AttributionLink[]) => void
}) {
  function setAt(i: number, patch: Partial<AttributionLink>) {
    onChange(links.map((l, idx) => (idx === i ? { ...l, ...patch } : l)))
  }
  return (
    <div className="flex flex-col gap-1">
      <label className={labelClass}>Attribution</label>
      <div className="flex flex-col gap-2">
        {links.map((link, i) => (
          <div key={i} className="flex items-center gap-1.5">
            <input
              className={inputClass}
              value={link.label}
              onChange={(e) => setAt(i, { label: e.target.value })}
              placeholder="© Provider"
            />
            <input
              className={inputClass}
              value={link.href}
              onChange={(e) => setAt(i, { href: e.target.value })}
              placeholder="https://…"
            />
            <Button
              type="button"
              variant="ghost"
              size="icon"
              className="text-muted-foreground size-7 shrink-0"
              title="Remove"
              onClick={() => onChange(links.filter((_, idx) => idx !== i))}
            >
              <X className="size-4" />
            </Button>
          </div>
        ))}
        <div>
          <Button
            type="button"
            variant="outline"
            size="sm"
            onClick={() => onChange([...links, { label: "", href: "" }])}
          >
            <Plus className="size-4" />
            Add credit
          </Button>
        </div>
      </div>
    </div>
  )
}

interface FormState {
  label: string
  description: string
  serviceType: ServiceType
  url: string
  layerIds: string
  styleUrl: string
  imageFormat: string
  version: string
  crs: string
  extent: string
  maxZoom: string
  minZoom: string
  defaultOpacity: string
  isQueryable: boolean
  proxied: boolean
  attribution: AttributionLink[]
  authType: ServiceAuthType
  authHeader: string
  authQueryParam: string
  authUsername: string
  credential: string
  sortOrder: string
  isEnabled: boolean
}

const EMPTY_FORM: FormState = {
  label: "",
  description: "",
  serviceType: "wms",
  url: "",
  layerIds: "",
  styleUrl: "",
  imageFormat: "",
  version: "",
  crs: "EPSG:3857",
  extent: "",
  maxZoom: "",
  minZoom: "0",
  defaultOpacity: "1",
  isQueryable: false,
  proxied: true,
  attribution: [],
  authType: "none",
  authHeader: "",
  authQueryParam: "",
  authUsername: "",
  credential: "",
  sortOrder: "0",
  isEnabled: true,
}

/** A "Discover" affordance under the URL: fetches the service's capabilities
 * (Slice 5) and offers dropdowns that fill layer_ids / version / image format /
 * CRS-or-matrix-set / extent from what it advertises. Only rendered for types
 * with a discovery protocol; xyz and vector_tile have none. */
function DiscoverySection({
  state,
  set,
  layerId,
}: {
  state: FormState
  set: (patch: Partial<FormState>) => void
  layerId?: string
}) {
  const discover = useDiscoverServiceLayer()
  const [result, setResult] = useState<Discovery | null>(null)
  const [error, setError] = useState<string | null>(null)

  if (!supportsDiscovery(state.serviceType)) return null

  const canRun = state.url.trim() !== "" && !discover.isPending
  const isWmts = state.serviceType === "wmts"
  const crsLabel = isWmts ? "Tile matrix set" : "CRS"

  function run() {
    setError(null)
    const url = capabilitiesUrl(state.serviceType, state.url, state.version.trim() || undefined)
    if (!url) return
    // Pass an inline secret only when the admin typed one; otherwise an edit's
    // stored credential is used server-side via layerId.
    const auth =
      state.authType !== "none" && state.credential.trim() !== ""
        ? {
            type: state.authType,
            header: state.authHeader.trim(),
            query_param: state.authQueryParam.trim(),
            username: state.authUsername.trim(),
            secret: state.credential,
          }
        : undefined
    discover.mutate(
      { url, id: layerId, auth },
      {
        onSuccess: (resp) => {
          try {
            setResult(parseCapabilities(state.serviceType, resp.body))
          } catch {
            setResult(null)
            setError("Could not parse the service's capabilities document.")
          }
        },
        onError: (err) => {
          setResult(null)
          setError(err instanceof ApiError ? err.message : "Discovery request failed.")
        },
      },
    )
  }

  return (
    <div className="border-border/70 bg-muted/30 flex flex-col gap-2 rounded-md border p-3">
      <div className="flex items-center justify-between gap-2">
        <span className="text-muted-foreground text-xs">
          Auto-fill fields from the service's capabilities.
        </span>
        <Button type="button" variant="outline" size="sm" onClick={run} disabled={!canRun}>
          {discover.isPending ? <Loader2 className="size-4 animate-spin" /> : <Search className="size-4" />}
          Discover
        </Button>
      </div>

      {error && <span className="text-destructive text-xs">{error}</span>}

      {result && (
        <div className="flex flex-col gap-2">
          {result.layers.length > 0 && (
            <div className="flex flex-col gap-1">
              <label className={labelClass}>Layer ({result.layers.length} found)</label>
              <select
                className={inputClass}
                value=""
                onChange={(e) => e.target.value && set({ layerIds: e.target.value })}
              >
                <option value="">Choose a layer to set…</option>
                {result.layers.map((l) => (
                  <option key={l.id} value={l.id}>
                    {l.title === l.id ? l.id : `${l.title} (${l.id})`}
                  </option>
                ))}
              </select>
            </div>
          )}

          <div className="grid grid-cols-3 gap-2">
            {result.versions.length > 0 && (
              <div className="flex flex-col gap-1">
                <label className={labelClass}>Version</label>
                <select
                  className={inputClass}
                  value={state.version}
                  onChange={(e) => set({ version: e.target.value })}
                >
                  <option value="">—</option>
                  {result.versions.map((v) => (
                    <option key={v} value={v}>
                      {v}
                    </option>
                  ))}
                </select>
              </div>
            )}
            {result.imageFormats.length > 0 && (
              <div className="flex flex-col gap-1">
                <label className={labelClass}>Image format</label>
                <select
                  className={inputClass}
                  value={state.imageFormat}
                  onChange={(e) => set({ imageFormat: e.target.value })}
                >
                  <option value="">—</option>
                  {result.imageFormats.map((f) => (
                    <option key={f} value={f}>
                      {f}
                    </option>
                  ))}
                </select>
              </div>
            )}
            {(isWmts ? result.matrixSets : result.crsOptions).length > 0 && (
              <div className="flex flex-col gap-1">
                <label className={labelClass}>{crsLabel}</label>
                <select className={inputClass} value={state.crs} onChange={(e) => set({ crs: e.target.value })}>
                  <option value="">—</option>
                  {(isWmts ? result.matrixSets : result.crsOptions).map((c) => (
                    <option key={c} value={c}>
                      {c}
                    </option>
                  ))}
                </select>
              </div>
            )}
          </div>

          {result.extent && (
            <div>
              <Button
                type="button"
                variant="outline"
                size="sm"
                onClick={() => set({ extent: result.extent!.join(", ") })}
              >
                Use advertised extent
              </Button>
            </div>
          )}

          {result.layers.length === 0 && !result.note && (
            <span className="text-muted-foreground text-xs">No layers advertised by this service.</span>
          )}
          {result.note && <span className="text-muted-foreground text-xs">{result.note}</span>}
        </div>
      )}
    </div>
  )
}

/** The shared field block for create and edit -- everything except the code
 * (immutable, so create owns it) and the credential (create vs. edit differ). */
function FormFields({
  state,
  set,
  hasCredential,
  layerId,
}: {
  state: FormState
  set: (patch: Partial<FormState>) => void
  /** Edit only: a secret is already stored, so the credential field is optional. */
  hasCredential?: boolean
  /** Edit only: lets discovery reuse the layer's stored credential server-side. */
  layerId?: string
}) {
  const typeHint = SERVICE_TYPES.find((t) => t.value === state.serviceType)?.hint ?? ""
  const layerRequired = LAYER_ID_REQUIRED.includes(state.serviceType)
  const isTileTemplate = TILE_TEMPLATE.includes(state.serviceType)
  const isVectorTile = state.serviceType === "vector_tile"
  const isRaster = ["wms", "wmts", "xyz", "esri_map"].includes(state.serviceType)

  return (
    <>
      <div className="grid grid-cols-2 gap-3">
        <div className="flex flex-col gap-1">
          <label className={labelClass}>
            Label <span className="text-destructive">*</span>
          </label>
          <input
            className={inputClass}
            value={state.label}
            onChange={(e) => set({ label: e.target.value })}
            placeholder="e.g. Cadastral parcels"
          />
        </div>
        <div className="flex flex-col gap-1">
          <label className={labelClass}>
            Service type <span className="text-destructive">*</span>
          </label>
          <select
            className={inputClass}
            value={state.serviceType}
            onChange={(e) => set({ serviceType: e.target.value as ServiceType })}
          >
            {SERVICE_TYPES.map((t) => (
              <option key={t.value} value={t.value}>
                {t.label}
              </option>
            ))}
          </select>
        </div>
      </div>

      <div className="flex flex-col gap-1">
        <label className={labelClass}>
          URL <span className="text-destructive">*</span>
        </label>
        <input
          className={inputClass}
          value={state.url}
          onChange={(e) => set({ url: e.target.value })}
          placeholder={isTileTemplate ? "https://…/{z}/{x}/{y}.png" : "https://…/service"}
        />
        <span className="text-muted-foreground text-xs">{typeHint}</span>
      </div>

      <DiscoverySection state={state} set={set} layerId={layerId} />

      <div className="flex flex-col gap-1">
        <label className={labelClass}>
          Layer(s) {layerRequired && <span className="text-destructive">*</span>}
        </label>
        <input
          className={inputClass}
          value={state.layerIds}
          onChange={(e) => set({ layerIds: e.target.value })}
          placeholder={layerRequired ? "required for this type" : "optional"}
        />
      </div>

      {isVectorTile && (
        <div className="flex flex-col gap-1">
          <label className={labelClass}>Style URL</label>
          <input
            className={inputClass}
            value={state.styleUrl}
            onChange={(e) => set({ styleUrl: e.target.value })}
            placeholder="https://…/style.json (optional)"
          />
        </div>
      )}

      <div className="grid grid-cols-3 gap-3">
        <div className="flex flex-col gap-1">
          <label className={labelClass}>CRS</label>
          <input
            className={inputClass}
            value={state.crs}
            onChange={(e) => set({ crs: e.target.value })}
            placeholder="EPSG:3857"
          />
        </div>
        {isRaster && (
          <div className="flex flex-col gap-1">
            <label className={labelClass}>Image format</label>
            <input
              className={inputClass}
              value={state.imageFormat}
              onChange={(e) => set({ imageFormat: e.target.value })}
              placeholder="image/png"
            />
          </div>
        )}
        <div className="flex flex-col gap-1">
          <label className={labelClass}>Version</label>
          <input
            className={inputClass}
            value={state.version}
            onChange={(e) => set({ version: e.target.value })}
            placeholder="e.g. 1.3.0"
          />
        </div>
      </div>

      <div className="flex flex-col gap-1">
        <label className={labelClass}>Extent (EPSG:4326)</label>
        <input
          className={inputClass}
          value={state.extent}
          onChange={(e) => set({ extent: e.target.value })}
          placeholder="minLon, minLat, maxLon, maxLat (optional)"
        />
        <span className="text-muted-foreground text-xs">Coverage box for zoom-to. Blank = unknown/global.</span>
      </div>

      <div className="grid grid-cols-3 gap-3">
        <div className="flex flex-col gap-1">
          <label className={labelClass}>Min zoom</label>
          <input
            className={inputClass}
            type="number"
            min="0"
            max="24"
            value={state.minZoom}
            onChange={(e) => set({ minZoom: e.target.value })}
            placeholder="0"
          />
        </div>
        <div className="flex flex-col gap-1">
          <label className={labelClass}>Max zoom</label>
          <input
            className={inputClass}
            type="number"
            min="0"
            max="24"
            value={state.maxZoom}
            onChange={(e) => set({ maxZoom: e.target.value })}
            placeholder="none"
          />
        </div>
        <div className="flex flex-col gap-1">
          <label className={labelClass}>Opacity</label>
          <input
            className={inputClass}
            type="number"
            min="0"
            max="1"
            step="0.1"
            value={state.defaultOpacity}
            onChange={(e) => set({ defaultOpacity: e.target.value })}
            placeholder="1"
          />
        </div>
      </div>

      <AttributionEditor links={state.attribution} onChange={(attribution) => set({ attribution })} />

      {/* Auth -- optional per layer. 'none' hides the rest. */}
      <div className="border-border flex flex-col gap-3 rounded-md border p-3">
        <div className="flex flex-col gap-1">
          <label className={labelClass}>Authentication</label>
          <select
            className={inputClass}
            value={state.authType}
            onChange={(e) => set({ authType: e.target.value as ServiceAuthType })}
          >
            {AUTH_TYPES.map((a) => (
              <option key={a.value} value={a.value}>
                {a.label}
              </option>
            ))}
          </select>
        </div>

        {state.authType === "api_key" && (
          <div className="grid grid-cols-2 gap-3">
            <div className="flex flex-col gap-1">
              <label className={labelClass}>Header name</label>
              <input
                className={inputClass}
                value={state.authHeader}
                onChange={(e) => set({ authHeader: e.target.value })}
                placeholder="X-API-Key"
              />
            </div>
            <div className="flex flex-col gap-1">
              <label className={labelClass}>…or query param</label>
              <input
                className={inputClass}
                value={state.authQueryParam}
                onChange={(e) => set({ authQueryParam: e.target.value })}
                placeholder="token"
              />
            </div>
          </div>
        )}

        {state.authType === "basic" && (
          <div className="flex flex-col gap-1">
            <label className={labelClass}>
              Username <span className="text-destructive">*</span>
            </label>
            <input
              className={inputClass}
              value={state.authUsername}
              onChange={(e) => set({ authUsername: e.target.value })}
              placeholder="username"
            />
          </div>
        )}

        {state.authType !== "none" && (
          <div className="flex flex-col gap-1">
            <label className={labelClass}>
              {state.authType === "basic" ? "Password" : "Secret / token"}
              {!hasCredential && <span className="text-destructive"> *</span>}
            </label>
            <input
              className={inputClass}
              type="password"
              value={state.credential}
              onChange={(e) => set({ credential: e.target.value })}
              placeholder={hasCredential ? "•••••• (leave blank to keep, clear to remove)" : "enter secret"}
              autoComplete="new-password"
            />
            <span className="text-muted-foreground text-xs">
              Stored encrypted; never shown again. Sent server-side only, never to the browser.
            </span>
          </div>
        )}
      </div>

      <div className="grid grid-cols-2 gap-3">
        <div className="flex flex-col gap-1">
          <label className={labelClass}>Sort order</label>
          <input
            className={inputClass}
            type="number"
            value={state.sortOrder}
            onChange={(e) => set({ sortOrder: e.target.value })}
            placeholder="0"
          />
          <span className="text-muted-foreground text-xs">Lower shows first in the layers panel.</span>
        </div>
        <div className="flex flex-col gap-1">
          <label className={labelClass}>Options</label>
          <label className="flex items-center gap-2 text-sm">
            <input type="checkbox" checked={state.isEnabled} onChange={(e) => set({ isEnabled: e.target.checked })} />
            Enabled (shown on the map)
          </label>
          <label className="flex items-center gap-2 text-sm">
            <input type="checkbox" checked={state.proxied} onChange={(e) => set({ proxied: e.target.checked })} />
            Proxy through the server
          </label>
          <label className="flex items-center gap-2 text-sm">
            <input
              type="checkbox"
              checked={state.isQueryable}
              onChange={(e) => set({ isQueryable: e.target.checked })}
            />
            Queryable (click-to-identify)
          </label>
        </div>
      </div>

      <div className="flex flex-col gap-1">
        <label className={labelClass}>Description</label>
        <input
          className={inputClass}
          value={state.description}
          onChange={(e) => set({ description: e.target.value })}
          placeholder="optional note"
        />
      </div>
    </>
  )
}

/** The editable fields shared by create and update bodies, from the string form. */
function toShared(state: FormState) {
  return {
    label: state.label.trim(),
    description: state.description.trim(),
    service_type: state.serviceType,
    url: state.url.trim(),
    layer_ids: state.layerIds.trim(),
    style_url: state.styleUrl.trim(),
    image_format: state.imageFormat.trim(),
    version: state.version.trim(),
    crs: state.crs.trim() || "EPSG:3857",
    extent: parseExtent(state.extent),
    max_zoom: parseNum(state.maxZoom),
    min_zoom: parseNum(state.minZoom) ?? 0,
    default_opacity: parseNum(state.defaultOpacity) ?? 1,
    is_queryable: state.isQueryable,
    attribution: state.attribution
      .map((l) => ({ label: l.label.trim(), href: l.href.trim() }))
      .filter((l) => l.label !== ""),
    proxied: state.proxied,
    auth_type: state.authType,
    auth_header: state.authHeader.trim(),
    auth_query_param: state.authQueryParam.trim(),
    auth_username: state.authUsername.trim(),
    sort_order: parseNum(state.sortOrder) ?? 0,
    is_enabled: state.isEnabled,
  }
}

/** Client-side gate mirroring the server's required-field rules, so the Save
 * button only enables for a submittable form (the server re-checks). */
function formValid(state: FormState, hasCredential: boolean): boolean {
  if (state.label.trim() === "" || state.url.trim() === "") return false
  if (TILE_TEMPLATE.includes(state.serviceType)) {
    const u = state.url
    if (!(u.includes("{z}") && u.includes("{x}") && u.includes("{y}"))) return false
  }
  if (LAYER_ID_REQUIRED.includes(state.serviceType) && state.layerIds.trim() === "") return false
  if (state.authType === "api_key" && state.authHeader.trim() === "" && state.authQueryParam.trim() === "")
    return false
  if (state.authType === "basic" && state.authUsername.trim() === "") return false
  // A non-'none' layer needs a secret: supplied now, or already stored (edit).
  if (state.authType !== "none" && state.credential.trim() === "" && !hasCredential) return false
  return true
}

function CreateServiceLayerDialog({ onClose }: { onClose: () => void }) {
  const [code, setCode] = useState("")
  const [state, setState] = useState<FormState>(EMPTY_FORM)
  const create = useCreateServiceLayer()
  const set = (patch: Partial<FormState>) => setState((s) => ({ ...s, ...patch }))

  const codeValid = /^[a-z0-9-]+$/.test(code)
  const canSave = codeValid && formValid(state, false) && !create.isPending

  function handleSave() {
    const shared = toShared(state)
    const body: CreateServiceLayerInput = { code: code.trim(), ...shared }
    if (state.authType !== "none" && state.credential.trim() !== "") body.credential = state.credential
    create.mutate(body, {
      onSuccess: onClose,
      onError: (err) => toastError(err, "Could not create the service layer."),
    })
  }

  return (
    <FormDialog
      open
      onOpenChange={(next) => next || onClose()}
      title="New service layer"
      description="Adds an external service the map can overlay (WMS/WMTS/XYZ/vector tiles/Esri/WFS)."
      onSubmit={handleSave}
      submitLabel="Create"
      canSubmit={canSave}
      saving={create.isPending}
    >
      <div className="flex flex-col gap-1">
        <label className={labelClass}>
          Code <span className="text-destructive">*</span>
        </label>
        <input
          className={inputClass}
          value={code}
          onChange={(e) => setCode(e.target.value)}
          placeholder="e.g. cadastre-wms"
          autoFocus
        />
        <span className="text-muted-foreground text-xs">
          Stable id (lowercase letters, digits, hyphens). Fixed once created.
        </span>
      </div>
      <FormFields state={state} set={set} />
    </FormDialog>
  )
}

function EditServiceLayerDialog({ layer, onClose }: { layer: ServiceLayerRecord; onClose: () => void }) {
  const [state, setState] = useState<FormState>({
    label: layer.label,
    description: layer.description ?? "",
    serviceType: layer.service_type,
    url: layer.url,
    layerIds: layer.layer_ids ?? "",
    styleUrl: layer.style_url ?? "",
    imageFormat: layer.image_format ?? "",
    version: layer.version ?? "",
    crs: layer.crs,
    extent: layer.extent ? layer.extent.join(", ") : "",
    maxZoom: layer.max_zoom == null ? "" : String(layer.max_zoom),
    minZoom: String(layer.min_zoom),
    defaultOpacity: String(layer.default_opacity),
    isQueryable: layer.is_queryable,
    proxied: layer.proxied,
    attribution: layer.attribution.map((l) => ({ ...l })),
    authType: layer.auth_type,
    authHeader: layer.auth_header ?? "",
    authQueryParam: layer.auth_query_param ?? "",
    authUsername: layer.auth_username ?? "",
    credential: "",
    sortOrder: String(layer.sort_order),
    isEnabled: layer.is_enabled,
  })
  const update = useUpdateServiceLayer()
  const set = (patch: Partial<FormState>) => setState((s) => ({ ...s, ...patch }))
  const canSave = formValid(state, layer.has_credential) && !update.isPending

  function handleSave() {
    const shared = toShared(state)
    const body: UpdateServiceLayerInput = { ...shared }
    if (shared.max_zoom == null) body.clear_max_zoom = true
    // Credential is three-state: send only when the field was touched (non-empty),
    // so leaving it blank keeps the stored secret.
    if (state.authType !== "none" && state.credential.trim() !== "") body.credential = state.credential
    update.mutate(
      { id: layer.id, body },
      { onSuccess: onClose, onError: (err) => toastError(err, "Could not update the service layer.") },
    )
  }

  return (
    <FormDialog
      open
      onOpenChange={(next) => next || onClose()}
      title="Edit service layer"
      description={`Layer "${layer.code}". Code is fixed.`}
      onSubmit={handleSave}
      submitLabel="Save"
      canSubmit={canSave}
      saving={update.isPending}
    >
      <FormFields state={state} set={set} hasCredential={layer.has_credential} layerId={layer.id} />
    </FormDialog>
  )
}

function ServiceLayerRow({
  layer,
  onEdit,
  onDelete,
}: {
  layer: ServiceLayerRecord
  onEdit: (l: ServiceLayerRecord) => void
  onDelete: (l: ServiceLayerRecord) => void
}) {
  const update = useUpdateServiceLayer()

  function toggleEnabled() {
    update.mutate(
      { id: layer.id, body: { is_enabled: !layer.is_enabled } },
      { onError: (err) => toastError(err, "Could not change the service layer.") },
    )
  }

  return (
    <div className="hover:bg-muted/50 group flex items-center gap-2 rounded-md px-2 py-1.5">
      <span className={`truncate text-sm font-medium ${layer.is_enabled ? "" : "text-muted-foreground"}`}>
        {layer.label}
      </span>
      <span className="bg-muted text-muted-foreground rounded px-1.5 py-0.5 font-mono text-xs">{layer.code}</span>
      <span className="bg-muted text-muted-foreground rounded px-1.5 py-0.5 text-xs uppercase">
        {layer.service_type.replace("_", " ")}
      </span>
      {layer.auth_type !== "none" && (
        <span className="bg-muted text-muted-foreground rounded px-1.5 py-0.5 text-xs">auth</span>
      )}
      {!layer.is_enabled && (
        <span className="bg-muted text-muted-foreground rounded px-1.5 py-0.5 text-xs">disabled</span>
      )}
      <div className="ml-auto flex shrink-0 items-center gap-0.5 opacity-0 transition-opacity group-hover:opacity-100 focus-within:opacity-100">
        <Button
          variant="ghost"
          size="icon"
          className="size-7"
          title={layer.is_enabled ? "Disable" : "Enable"}
          onClick={toggleEnabled}
          disabled={update.isPending}
        >
          {layer.is_enabled ? <Eye className="size-4" /> : <EyeOff className="size-4" />}
        </Button>
        <Button variant="ghost" size="icon" className="size-7" title="Edit" onClick={() => onEdit(layer)}>
          <Pencil className="size-4" />
        </Button>
        <Button
          variant="ghost"
          size="icon"
          className="text-destructive size-7"
          title="Delete"
          onClick={() => onDelete(layer)}
        >
          <Trash2 className="size-4" />
        </Button>
      </div>
    </div>
  )
}

/**
 * The service-layer catalog manager, embedded in the Map settings page beside the
 * basemap manager. Lists every layer (enabled and disabled) and lets an admin add
 * external WMS/WMTS/XYZ/vector-tile/Esri/WFS overlays, edit any, toggle
 * visibility, and delete them -- all without a rebuild, since the map reads this
 * list at runtime (hooks/use-service-layers.ts).
 */
export function ServiceLayersManager() {
  const { data, isPending, isError } = useAllServiceLayers()
  const [creating, setCreating] = useState(false)
  const [editing, setEditing] = useState<ServiceLayerRecord | null>(null)
  const [deleting, setDeleting] = useState<ServiceLayerRecord | null>(null)
  const del = useDeleteServiceLayer()

  return (
    <Card>
      <CardHeader className="flex-row items-center justify-between pb-2">
        <div>
          <CardTitle className="text-sm">Service layers</CardTitle>
          <CardDescription>
            External/3rd-party overlays the map can stack on the basemap — WMS, WMTS, XYZ or vector tiles, Esri
            MapServer/FeatureServer, and WFS. Auth is optional per layer.
          </CardDescription>
        </div>
        <Button size="sm" onClick={() => setCreating(true)}>
          <Plus className="size-4" />
          New service layer
        </Button>
      </CardHeader>
      <CardContent className="pt-0">
        {isPending ? (
          <div className="text-muted-foreground flex items-center gap-2 text-sm">
            <Loader2 className="size-4 animate-spin" />
            Loading service layers...
          </div>
        ) : isError ? (
          <p className="text-destructive text-sm">Could not load service layers.</p>
        ) : data && data.length > 0 ? (
          <div className="flex flex-col">
            {data.map((l) => (
              <ServiceLayerRow key={l.id} layer={l} onEdit={setEditing} onDelete={setDeleting} />
            ))}
          </div>
        ) : (
          <p className="text-muted-foreground text-sm">
            No service layers yet. Add one to overlay an external WMS/Esri/WFS source on the map.
          </p>
        )}
      </CardContent>

      {creating && <CreateServiceLayerDialog onClose={() => setCreating(false)} />}
      {editing && <EditServiceLayerDialog key={editing.id} layer={editing} onClose={() => setEditing(null)} />}

      <ConfirmDeleteDialog
        open={deleting !== null}
        title={`Delete ${deleting?.label ?? ""}?`}
        description="This removes the layer from the map's layers panel. This can't be undone."
        confirmLabel="Delete"
        onOpenChange={(open) => !open && setDeleting(null)}
        onConfirm={async () => {
          if (!deleting) return
          await del.mutateAsync(deleting.id)
        }}
      />
    </Card>
  )
}
