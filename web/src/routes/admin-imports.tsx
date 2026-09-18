import { type ReactNode, useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { Loader2, Pencil, Plus, Trash2 } from "lucide-react"

import { ConfirmDeleteDialog } from "@/components/admin/confirm-delete-dialog"
import { FormDialog } from "@/components/admin/form-dialog"
import { inputClass } from "@/components/map/field-utils"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { usePermission } from "@/hooks/use-auth"
import {
  type AdminFeatureClassSummary,
  type AdminImportFieldMapping,
  type AdminImportProfile,
  useAdminFeatureClasses,
  useAdminImportFieldMappings,
  useAdminImportProfiles,
} from "@/hooks/use-admin"
import { ApiError, apiFetch } from "@/lib/api"
import { useToastStore } from "@/stores/toast-store"

const labelClass = "text-xs font-medium"

// Enum whitelists mirror migration 0102's CHECK constraints (and the S2 Go
// validation) so the editor only ever offers a legal value. The two live-DB
// formats (postgis/mssql) are pre-widened in the schema for v2 but not offered
// here -- v1 is file uploads only (docs/etl-import-engine.md).
const SOURCE_FORMATS = ["csv", "geojson", "shapefile", "kml", "dxf"] as const
const WRITE_MODES = ["insert_only", "upsert"] as const
const GEOMETRY_MODES = ["none", "native", "lonlat", "wkt"] as const
const STATUSES = ["active", "deprecated"] as const
const TRANSFORMS = ["direct", "constant", "scale", "domain_map"] as const

/** Splits the comma/whitespace-separated match-field input into a clean list. */
function parseMatchFields(text: string): string[] {
  return text
    .split(/[\s,]+/)
    .map((s) => s.trim())
    .filter((s) => s !== "")
}

/** Parse or undefined -- undefined signals invalid to callers (null is a legal
 * JSON value, so it can't double as the failure sentinel). */
function tryParseJson(text: string): Record<string, unknown> | undefined {
  try {
    return JSON.parse(text) as Record<string, unknown>
  } catch {
    return undefined
  }
}

/** Shared feature-class dropdown -- the summary payload carries no field lists,
 * so field/match names stay free-text (validated server-side against the class). */
function FeatureClassPicker({
  value,
  onChange,
  classes,
}: {
  value: string
  onChange: (v: string) => void
  classes: AdminFeatureClassSummary[] | undefined
}) {
  return (
    <div className="flex flex-col gap-1">
      <label className={labelClass}>
        Feature class <span className="text-destructive">*</span>
      </label>
      <select className={inputClass} value={value} onChange={(e) => onChange(e.target.value)}>
        <option value="">select a feature class</option>
        {classes?.map((c) => (
          <option key={c.code} value={c.code}>
            {c.name} ({c.code})
          </option>
        ))}
      </select>
    </div>
  )
}

// ---------------------------------------------------------------------------
// Profiles
// ---------------------------------------------------------------------------

/** Create (existing null) or edit one import profile. On edit the code and the
 * target class/subtype are fixed (the code is the PATCH key and the field
 * mappings reference the class's fields), so they're shown read-only. */
function ProfileDialog({
  existing,
  classes,
  onDone,
}: {
  existing: AdminImportProfile | null
  classes: AdminFeatureClassSummary[] | undefined
  onDone: () => void
}) {
  const [code, setCode] = useState(existing?.code ?? "")
  const [name, setName] = useState(existing?.name ?? "")
  const [featureClassCode, setFeatureClassCode] = useState(existing?.feature_class_code ?? "")
  const [subtypeCode, setSubtypeCode] = useState(existing?.subtype_code != null ? String(existing.subtype_code) : "")
  const [sourceFormat, setSourceFormat] = useState<string>(existing?.source_format ?? SOURCE_FORMATS[0])
  const [writeMode, setWriteMode] = useState<string>(existing?.write_mode ?? WRITE_MODES[0])
  const [matchFields, setMatchFields] = useState((existing?.match_fields ?? []).join(", "))
  const [geometryMode, setGeometryMode] = useState<string>(existing?.geometry_mode ?? "native")
  const [lonColumn, setLonColumn] = useState(existing?.geometry_lon_column ?? "")
  const [latColumn, setLatColumn] = useState(existing?.geometry_lat_column ?? "")
  const [wktColumn, setWktColumn] = useState(existing?.geometry_wkt_column ?? "")
  const [status, setStatus] = useState<string>(existing?.status ?? "active")
  const [saving, setSaving] = useState(false)
  const queryClient = useQueryClient()

  const parsedMatch = parseMatchFields(matchFields)
  // Mirror the migration's cross-field CHECKs so the dialog blocks a save the
  // server would bounce: upsert needs a match key; lonlat needs both columns;
  // wkt needs its column.
  const geometryOk =
    geometryMode !== "lonlat"
      ? geometryMode !== "wkt" || wktColumn.trim() !== ""
      : lonColumn.trim() !== "" && latColumn.trim() !== ""
  const canSave =
    code.trim().length > 0 &&
    name.trim().length > 0 &&
    featureClassCode.trim().length > 0 &&
    (writeMode !== "upsert" || parsedMatch.length > 0) &&
    geometryOk

  async function handleSave() {
    setSaving(true)
    try {
      const body: Record<string, unknown> = {
        name: name.trim(),
        source_format: sourceFormat,
        write_mode: writeMode,
        match_fields: parsedMatch,
        geometry_mode: geometryMode,
        geometry_lon_column: geometryMode === "lonlat" ? lonColumn.trim() : "",
        geometry_lat_column: geometryMode === "lonlat" ? latColumn.trim() : "",
        geometry_wkt_column: geometryMode === "wkt" ? wktColumn.trim() : "",
        status,
      }
      if (existing) {
        await apiFetch(`/admin/import-profiles/${existing.code}`, { method: "PATCH", body: JSON.stringify(body) })
      } else {
        body.code = code.trim()
        body.feature_class_code = featureClassCode
        body.subtype_code = subtypeCode.trim() === "" ? null : Number(subtypeCode)
        await apiFetch("/admin/import-profiles", { method: "POST", body: JSON.stringify(body) })
      }
      queryClient.invalidateQueries({ queryKey: ["admin", "import-profiles"] })
      onDone()
    } catch (err) {
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not save this profile.")
    } finally {
      setSaving(false)
    }
  }

  return (
    <FormDialog
      open
      onOpenChange={(next) => next || onDone()}
      title={existing ? <>Edit profile <span className="font-mono">{existing.code}</span></> : "New import profile"}
      description="A reusable import for one target feature class: source format, write policy, and geometry assembly."
      onSubmit={handleSave}
      submitLabel={existing ? "Save" : "Create"}
      canSubmit={canSave}
      saving={saving}
    >
      <div className="grid grid-cols-2 gap-3">
        <div className="flex flex-col gap-1">
          <label className={labelClass}>
            Code <span className="text-destructive">*</span>
          </label>
          <input
            className={inputClass}
            value={code}
            onChange={(e) => setCode(e.target.value.toUpperCase())}
            disabled={existing !== null}
            placeholder="e.g. POLES_FROM_GIS"
          />
        </div>
        <div className="flex flex-col gap-1">
          <label className={labelClass}>
            Name <span className="text-destructive">*</span>
          </label>
          <input className={inputClass} value={name} onChange={(e) => setName(e.target.value)} placeholder="e.g. Poles from legacy GIS" />
        </div>
      </div>

      {existing ? (
        <div className="flex flex-col gap-1">
          <label className={labelClass}>Feature class</label>
          <div className="text-sm">
            <span className="font-mono">{existing.feature_class_code}</span>
            {existing.subtype_code != null && (
              <span className="text-muted-foreground"> · subtype {existing.subtype_code}</span>
            )}
          </div>
        </div>
      ) : (
        <>
          <FeatureClassPicker value={featureClassCode} onChange={setFeatureClassCode} classes={classes} />
          <div className="flex flex-col gap-1">
            <label className={labelClass}>Subtype code</label>
            <input
              type="number"
              className={inputClass}
              value={subtypeCode}
              onChange={(e) => setSubtypeCode(e.target.value)}
              placeholder="blank = don't pin a subtype"
            />
          </div>
        </>
      )}

      <div className="grid grid-cols-2 gap-3">
        <div className="flex flex-col gap-1">
          <label className={labelClass}>Source format</label>
          <select className={inputClass} value={sourceFormat} onChange={(e) => setSourceFormat(e.target.value)}>
            {SOURCE_FORMATS.map((f) => (
              <option key={f} value={f}>
                {f}
              </option>
            ))}
          </select>
        </div>
        <div className="flex flex-col gap-1">
          <label className={labelClass}>Write mode</label>
          <select className={inputClass} value={writeMode} onChange={(e) => setWriteMode(e.target.value)}>
            {WRITE_MODES.map((m) => (
              <option key={m} value={m}>
                {m}
              </option>
            ))}
          </select>
        </div>
      </div>

      {writeMode === "upsert" && (
        <div className="flex flex-col gap-1">
          <label className={labelClass}>
            Match fields <span className="text-destructive">*</span>
          </label>
          <input
            className={inputClass}
            value={matchFields}
            onChange={(e) => setMatchFields(e.target.value)}
            placeholder="target field names, comma-separated — e.g. external_ref"
          />
          <p className="text-muted-foreground text-xs">
            The natural key an upsert matches an existing feature on. 0 hits inserts, 1 updates, more than 1 is a row
            error.
          </p>
        </div>
      )}

      <div className="flex flex-col gap-1">
        <label className={labelClass}>Geometry mode</label>
        <select className={inputClass} value={geometryMode} onChange={(e) => setGeometryMode(e.target.value)}>
          {GEOMETRY_MODES.map((m) => (
            <option key={m} value={m}>
              {m}
            </option>
          ))}
        </select>
        <p className="text-muted-foreground text-xs">
          none: geometry-less class · native: the file already carries geometry (GeoJSON/Shapefile/KML/DXF) · lonlat:
          two numeric columns · wkt: one WKT column.
        </p>
      </div>

      {geometryMode === "lonlat" && (
        <div className="grid grid-cols-2 gap-3">
          <div className="flex flex-col gap-1">
            <label className={labelClass}>
              Longitude column <span className="text-destructive">*</span>
            </label>
            <input className={inputClass} value={lonColumn} onChange={(e) => setLonColumn(e.target.value)} placeholder="e.g. lon" />
          </div>
          <div className="flex flex-col gap-1">
            <label className={labelClass}>
              Latitude column <span className="text-destructive">*</span>
            </label>
            <input className={inputClass} value={latColumn} onChange={(e) => setLatColumn(e.target.value)} placeholder="e.g. lat" />
          </div>
        </div>
      )}
      {geometryMode === "wkt" && (
        <div className="flex flex-col gap-1">
          <label className={labelClass}>
            WKT column <span className="text-destructive">*</span>
          </label>
          <input className={inputClass} value={wktColumn} onChange={(e) => setWktColumn(e.target.value)} placeholder="e.g. geom_wkt" />
        </div>
      )}

      {existing && (
        <div className="flex flex-col gap-1">
          <label className={labelClass}>Status</label>
          <select className={inputClass} value={status} onChange={(e) => setStatus(e.target.value)}>
            {STATUSES.map((s) => (
              <option key={s} value={s}>
                {s}
              </option>
            ))}
          </select>
        </div>
      )}
    </FormDialog>
  )
}

// ---------------------------------------------------------------------------
// Field mappings
// ---------------------------------------------------------------------------

/** Create (existing null) or edit one field mapping of a profile. On edit the
 * target field is fixed (the backend keys on field_id). The transform decides
 * which parameter input is shown and required. */
function FieldMappingDialog({
  profileCode,
  existing,
  onDone,
}: {
  profileCode: string
  existing: AdminImportFieldMapping | null
  onDone: () => void
}) {
  const [fieldName, setFieldName] = useState(existing?.field_name ?? "")
  const [transform, setTransform] = useState<string>(existing?.transform ?? TRANSFORMS[0])
  const [sourceColumn, setSourceColumn] = useState(existing?.source_column ?? "")
  const [constantValue, setConstantValue] = useState(existing?.constant_value ?? "")
  const [scaleFactor, setScaleFactor] = useState(existing?.scale_factor != null ? String(existing.scale_factor) : "")
  const [valueMap, setValueMap] = useState(existing?.value_map ? JSON.stringify(existing.value_map, null, 2) : "")
  const [saving, setSaving] = useState(false)
  const queryClient = useQueryClient()

  const valueMapValid = valueMap.trim() === "" || tryParseJson(valueMap) !== undefined
  // Mirror the per-transform CHECKs: every transform except constant needs a
  // source column; each parameterized transform needs its own parameter.
  const canSave =
    fieldName.trim().length > 0 &&
    (transform === "constant" || sourceColumn.trim() !== "") &&
    (transform !== "constant" || constantValue.trim() !== "") &&
    (transform !== "scale" || scaleFactor.trim() !== "") &&
    (transform !== "domain_map" || (valueMap.trim() !== "" && valueMapValid)) &&
    valueMapValid

  async function handleSave() {
    setSaving(true)
    try {
      const parsedValueMap = valueMap.trim() === "" ? null : tryParseJson(valueMap)
      const common: Record<string, unknown> = {
        transform,
        source_column: transform === "constant" ? (sourceColumn.trim() || null) : sourceColumn.trim(),
        constant_value: transform === "constant" ? constantValue : null,
        scale_factor: transform === "scale" ? Number(scaleFactor) : null,
        value_map: transform === "domain_map" ? parsedValueMap : null,
      }
      if (existing) {
        await apiFetch(`/admin/import-field-mappings/${existing.id}`, { method: "PATCH", body: JSON.stringify(common) })
      } else {
        await apiFetch(`/admin/import-profiles/${profileCode}/field-mappings`, {
          method: "POST",
          body: JSON.stringify({ field_name: fieldName.trim(), ...common }),
        })
      }
      queryClient.invalidateQueries({ queryKey: ["admin", "import-field-mappings", profileCode] })
      onDone()
    } catch (err) {
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not save this field mapping.")
    } finally {
      setSaving(false)
    }
  }

  return (
    <FormDialog
      open
      onOpenChange={(next) => next || onDone()}
      title={existing ? "Edit field mapping" : "New field mapping"}
      description="Map one source column to one target field, with a coercion transform."
      onSubmit={handleSave}
      submitLabel={existing ? "Save" : "Create"}
      canSubmit={canSave}
      saving={saving}
    >
      {existing ? (
        <div className="flex flex-col gap-1">
          <label className={labelClass}>Target field</label>
          <div className="text-sm font-mono">{existing.field_name}</div>
        </div>
      ) : (
        <div className="flex flex-col gap-1">
          <label className={labelClass}>
            Target field name <span className="text-destructive">*</span>
          </label>
          <input
            className={inputClass}
            value={fieldName}
            onChange={(e) => setFieldName(e.target.value)}
            placeholder="e.g. pole_height_m"
          />
        </div>
      )}

      <div className="flex flex-col gap-1">
        <label className={labelClass}>
          Transform <span className="text-destructive">*</span>
        </label>
        <select className={inputClass} value={transform} onChange={(e) => setTransform(e.target.value)}>
          {TRANSFORMS.map((t) => (
            <option key={t} value={t}>
              {t}
            </option>
          ))}
        </select>
      </div>

      {transform !== "constant" && (
        <div className="flex flex-col gap-1">
          <label className={labelClass}>
            Source column <span className="text-destructive">*</span>
          </label>
          <input
            className={inputClass}
            value={sourceColumn}
            onChange={(e) => setSourceColumn(e.target.value)}
            placeholder="column name in the source file"
          />
        </div>
      )}
      {transform === "constant" && (
        <div className="flex flex-col gap-1">
          <label className={labelClass}>
            Constant value <span className="text-destructive">*</span>
          </label>
          <input
            className={inputClass}
            value={constantValue}
            onChange={(e) => setConstantValue(e.target.value)}
            placeholder="written verbatim on every row"
          />
        </div>
      )}
      {transform === "scale" && (
        <div className="flex flex-col gap-1">
          <label className={labelClass}>
            Scale factor <span className="text-destructive">*</span>
          </label>
          <input
            type="number"
            className={inputClass}
            value={scaleFactor}
            onChange={(e) => setScaleFactor(e.target.value)}
            placeholder="e.g. 1000 for km → m"
          />
        </div>
      )}
      {transform === "domain_map" && (
        <div className="flex flex-col gap-1">
          <label className={labelClass}>
            Value map (JSON) <span className="text-destructive">*</span>
          </label>
          <textarea
            className="border-input bg-background focus-visible:border-ring focus-visible:ring-ring/50 h-24 w-full rounded-md border px-2 py-1 font-mono text-sm outline-none focus-visible:ring-[3px]"
            value={valueMap}
            onChange={(e) => setValueMap(e.target.value)}
            placeholder={'source value → coded value, e.g. {"11kV": "HV_11KV", "33kV": "HV_33KV"}'}
          />
          {!valueMapValid && <p className="text-destructive text-xs">Not valid JSON.</p>}
        </div>
      )}
    </FormDialog>
  )
}

// ---------------------------------------------------------------------------
// Page
// ---------------------------------------------------------------------------

/**
 * The ETL import mapping editor -- read+write CRUD over the two config tables
 * (metadata.import_profiles / import_field_mappings, migration 0102) that drive
 * the admin import engine (docs/etl-import-engine.md). A profile targets one
 * feature class; its field mappings (shown when a profile is selected) bind
 * source columns to that class's fields. Every mutation is gated on
 * metadata:write server-side; this page opens on metadata:read, so the write
 * controls are gated to match (the same discipline as admin-cim-mappings.tsx).
 * Running an import (S3+) is a separate, features:write flow.
 */
export function AdminImportsPage() {
  const profiles = useAdminImportProfiles()
  const { data: featureClassData } = useAdminFeatureClasses()
  const queryClient = useQueryClient()
  const canWrite = usePermission()("metadata:write")

  const classes = featureClassData?.feature_classes

  const [selectedCode, setSelectedCode] = useState<string | null>(null)
  const mappings = useAdminImportFieldMappings(selectedCode)

  const [profileForm, setProfileForm] = useState<{ existing: AdminImportProfile | null } | null>(null)
  const [mappingForm, setMappingForm] = useState<{ existing: AdminImportFieldMapping | null } | null>(null)
  const [deleteProfile, setDeleteProfile] = useState<AdminImportProfile | null>(null)
  const [deleteMapping, setDeleteMapping] = useState<AdminImportFieldMapping | null>(null)

  const selectedProfile = profiles.data?.import_profiles.find((p) => p.code === selectedCode) ?? null

  return (
    <div className="h-full overflow-y-auto p-4">
      <div className="flex w-full flex-col gap-4">
        <div>
          <h1 className="text-lg font-semibold">Data import</h1>
          <p className="text-muted-foreground text-sm">
            Reusable import profiles that map an external source file onto a feature class, then insert or upsert the
            rows into the master tables. Author the mapping here; run an import from the import wizard.
          </p>
        </div>

        {profileForm && (
          <ProfileDialog existing={profileForm.existing} classes={classes} onDone={() => setProfileForm(null)} />
        )}
        {mappingForm && selectedCode && (
          <FieldMappingDialog
            profileCode={selectedCode}
            existing={mappingForm.existing}
            onDone={() => setMappingForm(null)}
          />
        )}

        {/* 1. Profiles */}
        <Card>
          <CardHeader className="flex flex-row items-center gap-2 pb-2">
            <div className="min-w-0 flex-1">
              <CardTitle className="text-sm">Import profiles</CardTitle>
              <CardDescription>One reusable import per target class — select one to map its fields.</CardDescription>
            </div>
            {canWrite && (
              <Button size="sm" onClick={() => setProfileForm({ existing: null })}>
                <Plus className="size-4" />
                New profile
              </Button>
            )}
          </CardHeader>
          <CardContent className="pt-0">
            {profiles.isPending ? (
              <LoadingRow label="profiles" />
            ) : profiles.isError || !profiles.data ? (
              <ErrorRow label="profiles" />
            ) : profiles.data.import_profiles.length === 0 ? (
              <EmptyRow label="profiles" />
            ) : (
              <div className="flex flex-col gap-1">
                {profiles.data.import_profiles.map((p) => (
                  <RowShell
                    key={p.code}
                    selected={p.code === selectedCode}
                    onSelect={() => setSelectedCode(p.code)}
                    canWrite={canWrite}
                    onEdit={() => setProfileForm({ existing: p })}
                    onDelete={() => setDeleteProfile(p)}
                  >
                    <span className="min-w-0 flex-1 truncate text-sm">
                      <span className="font-medium">{p.name}</span> <span className="font-mono text-xs">{p.code}</span>
                      <span className="text-muted-foreground text-xs">
                        {" "}
                        · {p.feature_class_code}
                        {p.subtype_code != null && ` · subtype ${p.subtype_code}`} · {p.source_format} · {p.write_mode}
                      </span>
                      {p.status === "deprecated" && (
                        <span className="bg-muted text-muted-foreground ml-2 rounded px-1.5 py-0.5 text-xs">
                          deprecated
                        </span>
                      )}
                    </span>
                  </RowShell>
                ))}
              </div>
            )}
          </CardContent>
        </Card>

        {/* 2. Field mappings for the selected profile */}
        <Card>
          <CardHeader className="flex flex-row items-center gap-2 pb-2">
            <div className="min-w-0 flex-1">
              <CardTitle className="text-sm">
                Field mappings
                {selectedProfile && <span className="text-muted-foreground font-mono text-xs"> · {selectedProfile.code}</span>}
              </CardTitle>
              <CardDescription>Source column &rarr; target field, with a coercion transform.</CardDescription>
            </div>
            {canWrite && selectedCode && (
              <Button size="sm" onClick={() => setMappingForm({ existing: null })}>
                <Plus className="size-4" />
                New field mapping
              </Button>
            )}
          </CardHeader>
          <CardContent className="pt-0">
            {!selectedCode ? (
              <p className="text-muted-foreground text-sm">Select a profile above to see and edit its field mappings.</p>
            ) : mappings.isPending ? (
              <LoadingRow label="field mappings" />
            ) : mappings.isError || !mappings.data ? (
              <ErrorRow label="field mappings" />
            ) : mappings.data.import_field_mappings.length === 0 ? (
              <EmptyRow label="field mappings" />
            ) : (
              <div className="flex flex-col gap-1">
                {mappings.data.import_field_mappings.map((m) => (
                  <RowShell
                    key={m.id}
                    canWrite={canWrite}
                    onEdit={() => setMappingForm({ existing: m })}
                    onDelete={() => setDeleteMapping(m)}
                  >
                    <span className="min-w-0 flex-1 truncate text-sm">
                      <span className="text-muted-foreground text-xs">
                        {m.transform === "constant" ? "(constant)" : m.source_column}
                      </span>
                      <span className="text-muted-foreground"> &rarr; </span>
                      <span className="font-mono text-xs">{m.field_name}</span>
                      <span className="text-muted-foreground text-xs"> · {m.transform}</span>
                    </span>
                  </RowShell>
                ))}
              </div>
            )}
          </CardContent>
        </Card>
      </div>

      <ConfirmDeleteDialog
        open={deleteProfile !== null}
        title={`Delete profile ${deleteProfile?.code ?? ""}?`}
        description="Removes this import profile and all its field mappings. Past import runs keep their record. This can't be undone."
        onOpenChange={(open) => !open && setDeleteProfile(null)}
        onConfirm={async () => {
          if (!deleteProfile) return
          await apiFetch(`/admin/import-profiles/${deleteProfile.code}`, { method: "DELETE" })
          if (deleteProfile.code === selectedCode) setSelectedCode(null)
          queryClient.invalidateQueries({ queryKey: ["admin", "import-profiles"] })
        }}
      />

      <ConfirmDeleteDialog
        open={deleteMapping !== null}
        title="Delete this field mapping?"
        description="The source column is no longer written to this field. This can't be undone."
        onOpenChange={(open) => !open && setDeleteMapping(null)}
        onConfirm={async () => {
          if (!deleteMapping) return
          await apiFetch(`/admin/import-field-mappings/${deleteMapping.id}`, { method: "DELETE" })
          queryClient.invalidateQueries({ queryKey: ["admin", "import-field-mappings", deleteMapping.profile_code] })
        }}
      />
    </div>
  )
}

// ---------------------------------------------------------------------------
// Small shared row helpers
// ---------------------------------------------------------------------------

function RowShell({
  children,
  canWrite,
  selected,
  onSelect,
  onEdit,
  onDelete,
}: {
  children: ReactNode
  canWrite: boolean
  // A selectable row (profiles) highlights when active and clicks to select;
  // field-mapping rows omit both.
  selected?: boolean
  onSelect?: () => void
  onEdit: () => void
  onDelete: () => void
}) {
  return (
    <div
      className={`flex items-center gap-2 rounded-md px-2 py-1 ${selected ? "bg-primary/10" : "hover:bg-muted/50"} ${
        onSelect ? "cursor-pointer" : ""
      }`}
      onClick={onSelect}
    >
      {children}
      {canWrite && (
        <>
          <Button
            variant="ghost"
            size="icon"
            className="size-7"
            title="Edit"
            onClick={(e) => {
              e.stopPropagation()
              onEdit()
            }}
          >
            <Pencil className="size-4" />
          </Button>
          <Button
            variant="ghost"
            size="icon"
            className="text-destructive size-7"
            title="Delete"
            onClick={(e) => {
              e.stopPropagation()
              onDelete()
            }}
          >
            <Trash2 className="size-4" />
          </Button>
        </>
      )}
    </div>
  )
}

function LoadingRow({ label }: { label: string }) {
  return (
    <div className="text-muted-foreground flex items-center gap-2 text-sm">
      <Loader2 className="size-4 animate-spin" />
      Loading {label}...
    </div>
  )
}

function ErrorRow({ label }: { label: string }) {
  return <p className="text-destructive text-sm">Could not load {label}.</p>
}

function EmptyRow({ label }: { label: string }) {
  return <p className="text-muted-foreground text-sm">No {label} yet.</p>
}
