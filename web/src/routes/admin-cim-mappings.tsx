import { type ReactNode, useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { AlertTriangle, Check, Copy, FileCode2, Loader2, Pencil, Plus, Trash2 } from "lucide-react"

import { ConfirmDeleteDialog } from "@/components/admin/confirm-delete-dialog"
import { FormDialog } from "@/components/admin/form-dialog"
import { inputClass } from "@/components/map/field-utils"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { usePermission } from "@/hooks/use-auth"
import {
  type AdminCimAttributeMapping,
  type AdminCimClassMapping,
  type AdminCimProfile,
  type AdminFeatureClassSummary,
  useAdminCimAttributeMappings,
  useAdminCimClassMappings,
  useAdminCimCoverage,
  useAdminCimPreview,
  useAdminCimProfiles,
  useAdminFeatureClasses,
} from "@/hooks/use-admin"
import { ApiError, apiFetch } from "@/lib/api"
import { useToastStore } from "@/stores/toast-store"

const labelClass = "text-xs font-medium"

// Enum whitelists mirror the 0034/0035 CHECK constraints (and the S1 Go
// validation) -- the editor only ever offers a legal value, so a POST/PATCH
// can't be bounced for an out-of-range kind/transform/policy.
const CIM_KINDS = ["conducting_edge", "conducting_junction", "container", "asset", "protection", "skip"] as const
const TRANSFORMS = ["direct", "scale", "domain_map", "boolean", "from_geometry", "material_spec", "phase"] as const
const UNMAPPED_POLICIES = ["drop", "carry"] as const

/** Feature classes are shared reference data across all three CIM tables; the
 * summary payload carries no subtypes/field lists, so subtype_code and field_name
 * stay free-text (see the page's helper note). */
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

/** Create (existing null) or edit one CIM profile. On edit the code is fixed --
 * it's the PATCH path key and the mapping tables reference it -- so it's shown
 * read-only. */
function ProfileDialog({ existing, onDone }: { existing: AdminCimProfile | null; onDone: () => void }) {
  const [code, setCode] = useState(existing?.code ?? "")
  const [name, setName] = useState(existing?.name ?? "")
  const [cimNamespace, setCimNamespace] = useState(existing?.cim_namespace ?? "")
  const [profileUri, setProfileUri] = useState(existing?.profile_uri ?? "")
  const [modelAuthority, setModelAuthority] = useState(existing?.model_authority ?? "")
  const [coordinateCrs, setCoordinateCrs] = useState(existing?.coordinate_crs ?? "")
  const [unmappedPolicy, setUnmappedPolicy] = useState(existing?.unmapped_attr_policy ?? "drop")
  const [isDefault, setIsDefault] = useState(existing?.is_default ?? false)
  const [saving, setSaving] = useState(false)
  const queryClient = useQueryClient()

  const canSave =
    code.trim().length > 0 &&
    name.trim().length > 0 &&
    cimNamespace.trim().length > 0 &&
    profileUri.trim().length > 0 &&
    modelAuthority.trim().length > 0

  async function handleSave() {
    setSaving(true)
    try {
      const body: Record<string, unknown> = {
        name: name.trim(),
        cim_namespace: cimNamespace.trim(),
        profile_uri: profileUri.trim(),
        model_authority: modelAuthority.trim(),
        coordinate_crs: coordinateCrs.trim() || undefined,
        unmapped_attr_policy: unmappedPolicy,
        is_default: isDefault,
      }
      if (existing) {
        await apiFetch(`/admin/cim-profiles/${existing.code}`, { method: "PATCH", body: JSON.stringify(body) })
      } else {
        body.code = code.trim()
        await apiFetch("/admin/cim-profiles", { method: "POST", body: JSON.stringify(body) })
      }
      queryClient.invalidateQueries({ queryKey: ["admin", "cim-profiles"] })
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
      title={existing ? <>Edit profile <span className="font-mono">{existing.code}</span></> : "New CIM profile"}
      description="A CIM/XML export target -- the namespace/authority URIs a CIM document header needs."
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
            placeholder="e.g. CDPSM_CIM16"
          />
        </div>
        <div className="flex flex-col gap-1">
          <label className={labelClass}>
            Name <span className="text-destructive">*</span>
          </label>
          <input className={inputClass} value={name} onChange={(e) => setName(e.target.value)} placeholder="e.g. CDPSM / CIM16" />
        </div>
      </div>
      <div className="flex flex-col gap-1">
        <label className={labelClass}>
          CIM namespace <span className="text-destructive">*</span>
        </label>
        <input
          className={inputClass}
          value={cimNamespace}
          onChange={(e) => setCimNamespace(e.target.value)}
          placeholder="http://iec.ch/TC57/2013/CIM-schema-cim16#"
        />
      </div>
      <div className="flex flex-col gap-1">
        <label className={labelClass}>
          Profile URI <span className="text-destructive">*</span>
        </label>
        <input className={inputClass} value={profileUri} onChange={(e) => setProfileUri(e.target.value)} />
      </div>
      <div className="flex flex-col gap-1">
        <label className={labelClass}>
          Model authority <span className="text-destructive">*</span>
        </label>
        <input className={inputClass} value={modelAuthority} onChange={(e) => setModelAuthority(e.target.value)} />
      </div>
      <div className="grid grid-cols-2 gap-3">
        <div className="flex flex-col gap-1">
          <label className={labelClass}>Coordinate CRS</label>
          <input
            className={inputClass}
            value={coordinateCrs}
            onChange={(e) => setCoordinateCrs(e.target.value)}
            placeholder="urn:ogc:def:crs:EPSG::4326"
          />
        </div>
        <div className="flex flex-col gap-1">
          <label className={labelClass}>Unmapped attribute policy</label>
          <select className={inputClass} value={unmappedPolicy} onChange={(e) => setUnmappedPolicy(e.target.value)}>
            {UNMAPPED_POLICIES.map((p) => (
              <option key={p} value={p}>
                {p}
              </option>
            ))}
          </select>
        </div>
      </div>
      <label className="flex items-center gap-2 text-xs font-medium">
        <input type="checkbox" checked={isDefault} onChange={(e) => setIsDefault(e.target.checked)} />
        Default profile (used when an export request names none)
      </label>
    </FormDialog>
  )
}

// ---------------------------------------------------------------------------
// Class mappings
// ---------------------------------------------------------------------------

/** Create (existing null) or edit one class mapping. The backend PATCH only
 * accepts cim_class + cim_kind, so on edit the feature class / subtype key is
 * fixed and shown read-only. */
function ClassMappingDialog({
  existing,
  classes,
  prefill,
  onDone,
}: {
  existing: AdminCimClassMapping | null
  classes: AdminFeatureClassSummary[] | undefined
  // Feature class code to preselect when creating from the coverage card's "Map"
  // action -- ignored on edit (the key is fixed there).
  prefill?: string
  onDone: () => void
}) {
  const [featureClassCode, setFeatureClassCode] = useState(existing?.feature_class_code ?? prefill ?? "")
  const [subtypeCode, setSubtypeCode] = useState(existing?.subtype_code != null ? String(existing.subtype_code) : "")
  const [cimClass, setCimClass] = useState(existing?.cim_class ?? "")
  const [cimKind, setCimKind] = useState(existing?.cim_kind ?? CIM_KINDS[0])
  const [saving, setSaving] = useState(false)
  const queryClient = useQueryClient()

  const canSave = featureClassCode.trim().length > 0 && cimClass.trim().length > 0 && cimKind.trim().length > 0

  async function handleSave() {
    setSaving(true)
    try {
      if (existing) {
        await apiFetch(`/admin/cim-class-mappings/${existing.id}`, {
          method: "PATCH",
          body: JSON.stringify({ cim_class: cimClass.trim(), cim_kind: cimKind }),
        })
      } else {
        await apiFetch("/admin/cim-class-mappings", {
          method: "POST",
          body: JSON.stringify({
            feature_class_code: featureClassCode,
            subtype_code: subtypeCode.trim() === "" ? null : Number(subtypeCode),
            cim_class: cimClass.trim(),
            cim_kind: cimKind,
          }),
        })
      }
      queryClient.invalidateQueries({ queryKey: ["admin", "cim-class-mappings"] })
      // A new/edited class mapping can move a class out of the unmapped list.
      queryClient.invalidateQueries({ queryKey: ["admin", "cim-coverage"] })
      onDone()
    } catch (err) {
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not save this class mapping.")
    } finally {
      setSaving(false)
    }
  }

  return (
    <FormDialog
      open
      onOpenChange={(next) => next || onDone()}
      title={existing ? "Edit class mapping" : "New class mapping"}
      description="Map a feature class (optionally one subtype) to a CIM class + kind."
      onSubmit={handleSave}
      submitLabel={existing ? "Save" : "Create"}
      canSubmit={canSave}
      saving={saving}
    >
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
              placeholder="blank = applies to the whole class"
            />
          </div>
        </>
      )}
      <div className="flex flex-col gap-1">
        <label className={labelClass}>
          CIM class <span className="text-destructive">*</span>
        </label>
        <input
          className={inputClass}
          value={cimClass}
          onChange={(e) => setCimClass(e.target.value)}
          placeholder="e.g. ACLineSegment, PowerTransformer, Breaker"
        />
      </div>
      <div className="flex flex-col gap-1">
        <label className={labelClass}>
          CIM kind <span className="text-destructive">*</span>
        </label>
        <select className={inputClass} value={cimKind} onChange={(e) => setCimKind(e.target.value)}>
          {CIM_KINDS.map((k) => (
            <option key={k} value={k}>
              {k}
            </option>
          ))}
        </select>
      </div>
    </FormDialog>
  )
}

// ---------------------------------------------------------------------------
// Attribute mappings
// ---------------------------------------------------------------------------

/** Create (existing null) or edit one attribute mapping. On edit the feature
 * class / field key is fixed and shown read-only (the backend keys on field_id).
 * value_map is JSON, parsed on submit -- invalid JSON blocks the save. */
function AttributeMappingDialog({
  existing,
  classes,
  onDone,
}: {
  existing: AdminCimAttributeMapping | null
  classes: AdminFeatureClassSummary[] | undefined
  onDone: () => void
}) {
  const [featureClassCode, setFeatureClassCode] = useState(existing?.feature_class_code ?? "")
  const [fieldName, setFieldName] = useState(existing?.field_name ?? "")
  const [cimAttribute, setCimAttribute] = useState(existing?.cim_attribute ?? "")
  const [transform, setTransform] = useState(existing?.transform ?? TRANSFORMS[0])
  const [scaleFactor, setScaleFactor] = useState(existing?.scale_factor != null ? String(existing.scale_factor) : "")
  const [unit, setUnit] = useState(existing?.unit ?? "")
  const [valueMap, setValueMap] = useState(existing?.value_map ? JSON.stringify(existing.value_map, null, 2) : "")
  const [saving, setSaving] = useState(false)
  const queryClient = useQueryClient()

  // value_map is only meaningful for domain_map/boolean, but validate whenever
  // it's non-empty so a stray blob can't slip through -- an unparseable one
  // blocks submit rather than reaching the server.
  const valueMapValid = valueMap.trim() === "" || tryParseJson(valueMap) !== undefined
  const canSave =
    featureClassCode.trim().length > 0 &&
    fieldName.trim().length > 0 &&
    cimAttribute.trim().length > 0 &&
    transform.trim().length > 0 &&
    valueMapValid

  async function handleSave() {
    setSaving(true)
    try {
      const parsedValueMap = valueMap.trim() === "" ? null : tryParseJson(valueMap)
      const common: Record<string, unknown> = {
        cim_attribute: cimAttribute.trim(),
        transform,
        scale_factor: scaleFactor.trim() === "" ? null : Number(scaleFactor),
        unit: unit.trim() || null,
        value_map: parsedValueMap ?? null,
      }
      if (existing) {
        await apiFetch(`/admin/cim-attribute-mappings/${existing.id}`, {
          method: "PATCH",
          body: JSON.stringify(common),
        })
      } else {
        await apiFetch("/admin/cim-attribute-mappings", {
          method: "POST",
          body: JSON.stringify({ feature_class_code: featureClassCode, field_name: fieldName.trim(), ...common }),
        })
      }
      queryClient.invalidateQueries({ queryKey: ["admin", "cim-attribute-mappings"] })
      onDone()
    } catch (err) {
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not save this attribute mapping.")
    } finally {
      setSaving(false)
    }
  }

  return (
    <FormDialog
      open
      onOpenChange={(next) => next || onDone()}
      title={existing ? "Edit attribute mapping" : "New attribute mapping"}
      description="Map a feature field to a CIM attribute (dotted Class.attribute) with a transform."
      onSubmit={handleSave}
      submitLabel={existing ? "Save" : "Create"}
      canSubmit={canSave}
      saving={saving}
    >
      {existing ? (
        <div className="flex flex-col gap-1">
          <label className={labelClass}>Feature field</label>
          <div className="text-sm">
            <span className="font-mono">{existing.feature_class_code}</span>
            <span className="text-muted-foreground"> · {existing.field_name}</span>
          </div>
        </div>
      ) : (
        <>
          <FeatureClassPicker value={featureClassCode} onChange={setFeatureClassCode} classes={classes} />
          <div className="flex flex-col gap-1">
            <label className={labelClass}>
              Field name <span className="text-destructive">*</span>
            </label>
            <input
              className={inputClass}
              value={fieldName}
              onChange={(e) => setFieldName(e.target.value)}
              placeholder="e.g. nominal_voltage"
            />
          </div>
        </>
      )}
      <div className="flex flex-col gap-1">
        <label className={labelClass}>
          CIM attribute <span className="text-destructive">*</span>
        </label>
        <input
          className={inputClass}
          value={cimAttribute}
          onChange={(e) => setCimAttribute(e.target.value)}
          placeholder="e.g. BaseVoltage.nominalVoltage"
        />
      </div>
      <div className="grid grid-cols-2 gap-3">
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
        <div className="flex flex-col gap-1">
          <label className={labelClass}>Scale factor</label>
          <input
            type="number"
            className={inputClass}
            value={scaleFactor}
            onChange={(e) => setScaleFactor(e.target.value)}
            placeholder="for transform 'scale'"
          />
        </div>
      </div>
      <div className="flex flex-col gap-1">
        <label className={labelClass}>Unit</label>
        <input className={inputClass} value={unit} onChange={(e) => setUnit(e.target.value)} placeholder="e.g. m, V, ohm" />
      </div>
      <div className="flex flex-col gap-1">
        <label className={labelClass}>Value map (JSON)</label>
        <textarea
          className="border-input bg-background focus-visible:border-ring focus-visible:ring-ring/50 h-24 w-full rounded-md border px-2 py-1 font-mono text-sm outline-none focus-visible:ring-[3px]"
          value={valueMap}
          onChange={(e) => setValueMap(e.target.value)}
          placeholder={'for transform domain_map/boolean, e.g. {"11KV": 11000, "33KV": 33000}'}
        />
        {!valueMapValid && <p className="text-destructive text-xs">Not valid JSON.</p>}
      </div>
    </FormDialog>
  )
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

// ---------------------------------------------------------------------------
// CIM/XML preview
// ---------------------------------------------------------------------------

/** A real-sample CIM/XML preview for one class: fetches one actual feature run
 * through the real export encoder and shows the byte-exact CIM/XML. Read-only, so
 * it uses the plain Dialog (Esc/outside-click close) rather than FormDialog. When
 * the class has nothing to render (no instance, or a geometry-less class the CIM
 * export never emits) the server returns sampled=false + a note, shown here. */
function PreviewDialog({ classCode, onDone }: { classCode: string; onDone: () => void }) {
  const preview = useAdminCimPreview(classCode)
  const [copied, setCopied] = useState(false)

  async function copyXml(xml: string) {
    try {
      await navigator.clipboard.writeText(xml)
      setCopied(true)
      setTimeout(() => setCopied(false), 1500)
    } catch {
      useToastStore.getState().pushError("Could not copy to the clipboard.")
    }
  }

  return (
    <Dialog open onOpenChange={(next) => next || onDone()}>
      <DialogContent className="sm:max-w-[793px]">
        <DialogHeader>
          <DialogTitle>
            CIM/XML preview <span className="font-mono text-sm">{classCode}</span>
          </DialogTitle>
          <DialogDescription>
            One real feature of this class, rendered through the actual CIM export encoder &mdash; the exact CIM/XML a
            full export produces for it.
          </DialogDescription>
        </DialogHeader>
        {preview.isPending ? (
          <LoadingRow label="preview" />
        ) : preview.isError || !preview.data ? (
          <ErrorRow label="preview" />
        ) : !preview.data.sampled ? (
          <p className="text-muted-foreground text-sm">{preview.data.note || "Nothing to preview for this class."}</p>
        ) : (
          <div className="flex min-w-0 flex-col gap-2">
            <div className="text-muted-foreground flex items-center justify-between gap-2 text-xs">
              <span className="min-w-0 truncate">
                sampled feature <span className="font-mono">{preview.data.feature_id}</span>
              </span>
              <Button variant="outline" size="sm" className="h-7 shrink-0" onClick={() => copyXml(preview.data.xml)}>
                {copied ? <Check className="size-3.5" /> : <Copy className="size-3.5" />}
                {copied ? "Copied" : "Copy"}
              </Button>
            </div>
            {/* w-full + min-w-0 lets the flex/grid ancestors shrink so overflow-auto
                scrolls the long xmlns line INSIDE the box instead of widening the
                dialog past its max width. */}
            <pre className="bg-muted max-h-[60vh] w-full min-w-0 overflow-auto rounded-md p-3 font-mono text-xs whitespace-pre">
              {preview.data.xml}
            </pre>
          </div>
        )}
      </DialogContent>
    </Dialog>
  )
}

// ---------------------------------------------------------------------------
// Page
// ---------------------------------------------------------------------------

/**
 * The CIM export mapping editor -- read+write CRUD over the three metadata tables
 * (cim_profiles / cim_class_mappings / cim_attribute_mappings, migration 0034)
 * that drive Phase 16 CIM/XML export, previously SQL/seed-only. Every mutation is
 * gated on metadata:write server-side; this page opens on metadata:read, so the
 * write controls are gated to match (see admin-domains.tsx). See
 * docs/phase-55-cim-mapping-admin.md.
 */
export function AdminCimMappingsPage() {
  const profiles = useAdminCimProfiles()
  const classMappings = useAdminCimClassMappings()
  const attributeMappings = useAdminCimAttributeMappings()
  const coverage = useAdminCimCoverage()
  const { data: featureClassData } = useAdminFeatureClasses()
  const queryClient = useQueryClient()
  const canWrite = usePermission()("metadata:write")

  const classes = featureClassData?.feature_classes

  // Dialog targets: "new" opens a create dialog, a row opens an edit dialog. The
  // class form also takes an optional prefill (from the coverage card's "Map").
  const [profileForm, setProfileForm] = useState<{ existing: AdminCimProfile | null } | null>(null)
  const [classForm, setClassForm] = useState<{ existing: AdminCimClassMapping | null; prefill?: string } | null>(null)
  const [attributeForm, setAttributeForm] = useState<{ existing: AdminCimAttributeMapping | null } | null>(null)

  const [deleteProfile, setDeleteProfile] = useState<AdminCimProfile | null>(null)
  const [deleteClass, setDeleteClass] = useState<AdminCimClassMapping | null>(null)
  const [deleteAttribute, setDeleteAttribute] = useState<AdminCimAttributeMapping | null>(null)

  // Read-only CIM/XML preview target -- a feature class code, or null when closed.
  const [previewClass, setPreviewClass] = useState<string | null>(null)

  return (
    <div className="h-full overflow-y-auto p-4">
      <div className="flex w-full flex-col gap-4">
        <div>
          <h1 className="text-lg font-semibold">CIM export mapping</h1>
          <p className="text-muted-foreground text-sm">
            How feature classes and their fields become CIM/XML on export -- the profile targets, the class-to-CIM-class
            mappings, and the field-to-CIM-attribute mappings the CIM export reads.
          </p>
        </div>

        {profileForm && <ProfileDialog existing={profileForm.existing} onDone={() => setProfileForm(null)} />}
        {classForm && (
          <ClassMappingDialog
            existing={classForm.existing}
            classes={classes}
            prefill={classForm.prefill}
            onDone={() => setClassForm(null)}
          />
        )}
        {attributeForm && (
          <AttributeMappingDialog
            existing={attributeForm.existing}
            classes={classes}
            onDone={() => setAttributeForm(null)}
          />
        )}
        {previewClass && <PreviewDialog classCode={previewClass} onDone={() => setPreviewClass(null)} />}

        {/* 0. Coverage safety net (Phase 55 Slice 2): feature classes with no
            class mapping are silently dropped from export -- surface them so an
            operator maps or explicitly skips each one. */}
        <Card className="border-amber-500/40">
          <CardHeader className="flex flex-row items-center gap-2 pb-2">
            <AlertTriangle className="size-4 shrink-0 text-amber-500" />
            <div className="min-w-0 flex-1">
              <CardTitle className="text-sm">Unmapped classes</CardTitle>
              <CardDescription>
                Feature classes with no class mapping &mdash; silently excluded from CIM export until mapped or explicitly
                skipped.
              </CardDescription>
            </div>
          </CardHeader>
          <CardContent className="pt-0">
            {coverage.isPending ? (
              <LoadingRow label="coverage" />
            ) : coverage.isError || !coverage.data ? (
              <ErrorRow label="coverage" />
            ) : coverage.data.unmapped_classes.length === 0 ? (
              <p className="text-muted-foreground text-sm">
                Every feature class is mapped or explicitly skipped &mdash; nothing is dropped from export.
              </p>
            ) : (
              <div className="flex flex-col gap-1">
                {coverage.data.unmapped_classes.map((c) => (
                  <div key={c.code} className="hover:bg-muted/50 flex items-center gap-2 rounded-md px-2 py-1">
                    <span className="min-w-0 flex-1 truncate text-sm">
                      <span className="font-medium">{c.name}</span> <span className="font-mono text-xs">{c.code}</span>
                      <span className="text-muted-foreground text-xs">
                        {" "}
                        · {c.geometry_type} · {c.network_role}
                      </span>
                    </span>
                    {canWrite && (
                      <Button
                        variant="ghost"
                        size="sm"
                        className="h-7"
                        onClick={() => setClassForm({ existing: null, prefill: c.code })}
                      >
                        Map…
                      </Button>
                    )}
                  </div>
                ))}
              </div>
            )}
          </CardContent>
        </Card>

        {/* 1. Profiles */}
        <Card>
          <CardHeader className="flex flex-row items-center gap-2 pb-2">
            <div className="min-w-0 flex-1">
              <CardTitle className="text-sm">Profiles</CardTitle>
              <CardDescription>Export targets -- one CIM/XML flavour each.</CardDescription>
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
            ) : profiles.data.cim_profiles.length === 0 ? (
              <EmptyRow label="profiles" />
            ) : (
              <div className="flex flex-col gap-1">
                {profiles.data.cim_profiles.map((p) => (
                  <RowShell
                    key={p.code}
                    canWrite={canWrite}
                    onEdit={() => setProfileForm({ existing: p })}
                    onDelete={() => setDeleteProfile(p)}
                  >
                    <span className="min-w-0 flex-1 truncate text-sm">
                      <span className="font-medium">{p.name}</span> <span className="font-mono text-xs">{p.code}</span>
                      {p.is_default && (
                        <span className="bg-primary/10 text-primary ml-2 rounded px-1.5 py-0.5 text-xs">default</span>
                      )}
                    </span>
                  </RowShell>
                ))}
              </div>
            )}
          </CardContent>
        </Card>

        {/* 2. Class mappings */}
        <Card>
          <CardHeader className="flex flex-row items-center gap-2 pb-2">
            <div className="min-w-0 flex-1">
              <CardTitle className="text-sm">Class mappings</CardTitle>
              <CardDescription>Feature class (optionally per subtype) &rarr; CIM class + kind.</CardDescription>
            </div>
            {canWrite && (
              <Button size="sm" onClick={() => setClassForm({ existing: null })}>
                <Plus className="size-4" />
                New class mapping
              </Button>
            )}
          </CardHeader>
          <CardContent className="pt-0">
            {classMappings.isPending ? (
              <LoadingRow label="class mappings" />
            ) : classMappings.isError || !classMappings.data ? (
              <ErrorRow label="class mappings" />
            ) : classMappings.data.cim_class_mappings.length === 0 ? (
              <EmptyRow label="class mappings" />
            ) : (
              <div className="flex flex-col gap-1">
                {classMappings.data.cim_class_mappings.map((m) => (
                  <RowShell
                    key={m.id}
                    canWrite={canWrite}
                    onPreview={() => setPreviewClass(m.feature_class_code)}
                    onEdit={() => setClassForm({ existing: m })}
                    onDelete={() => setDeleteClass(m)}
                  >
                    <span className="min-w-0 flex-1 truncate text-sm">
                      <span className="font-mono text-xs">{m.feature_class_code}</span>
                      {m.subtype_code != null && (
                        <span className="text-muted-foreground text-xs"> · subtype {m.subtype_code}</span>
                      )}
                      <span className="text-muted-foreground"> &rarr; </span>
                      <span className="font-medium">{m.cim_class}</span>{" "}
                      <span className="text-muted-foreground text-xs">{m.cim_kind}</span>
                    </span>
                  </RowShell>
                ))}
              </div>
            )}
          </CardContent>
        </Card>

        {/* 3. Attribute mappings */}
        <Card>
          <CardHeader className="flex flex-row items-center gap-2 pb-2">
            <div className="min-w-0 flex-1">
              <CardTitle className="text-sm">Attribute mappings</CardTitle>
              <CardDescription>Feature field &rarr; CIM attribute, with a coercion transform.</CardDescription>
            </div>
            {canWrite && (
              <Button size="sm" onClick={() => setAttributeForm({ existing: null })}>
                <Plus className="size-4" />
                New attribute mapping
              </Button>
            )}
          </CardHeader>
          <CardContent className="pt-0">
            {attributeMappings.isPending ? (
              <LoadingRow label="attribute mappings" />
            ) : attributeMappings.isError || !attributeMappings.data ? (
              <ErrorRow label="attribute mappings" />
            ) : attributeMappings.data.cim_attribute_mappings.length === 0 ? (
              <EmptyRow label="attribute mappings" />
            ) : (
              <div className="flex flex-col gap-1">
                {attributeMappings.data.cim_attribute_mappings.map((m) => (
                  <RowShell
                    key={m.id}
                    canWrite={canWrite}
                    onEdit={() => setAttributeForm({ existing: m })}
                    onDelete={() => setDeleteAttribute(m)}
                  >
                    <span className="min-w-0 flex-1 truncate text-sm">
                      <span className="font-mono text-xs">
                        {m.feature_class_code}.{m.field_name}
                      </span>
                      <span className="text-muted-foreground"> &rarr; </span>
                      <span className="font-medium">{m.cim_attribute}</span>{" "}
                      <span className="text-muted-foreground text-xs">{m.transform}</span>
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
        description="Removes this CIM export target. This can't be undone."
        onOpenChange={(open) => !open && setDeleteProfile(null)}
        onConfirm={async () => {
          if (!deleteProfile) return
          await apiFetch(`/admin/cim-profiles/${deleteProfile.code}`, { method: "DELETE" })
          queryClient.invalidateQueries({ queryKey: ["admin", "cim-profiles"] })
        }}
      />

      <ConfirmDeleteDialog
        open={deleteClass !== null}
        title="Delete this class mapping?"
        description="The feature class reverts to being excluded from CIM export unless another mapping covers it. This can't be undone."
        onOpenChange={(open) => !open && setDeleteClass(null)}
        onConfirm={async () => {
          if (!deleteClass) return
          await apiFetch(`/admin/cim-class-mappings/${deleteClass.id}`, { method: "DELETE" })
          queryClient.invalidateQueries({ queryKey: ["admin", "cim-class-mappings"] })
          // Deleting a class's only mapping returns it to the unmapped list.
          queryClient.invalidateQueries({ queryKey: ["admin", "cim-coverage"] })
        }}
      />

      <ConfirmDeleteDialog
        open={deleteAttribute !== null}
        title="Delete this attribute mapping?"
        description="The field is no longer carried into CIM export. This can't be undone."
        onOpenChange={(open) => !open && setDeleteAttribute(null)}
        onConfirm={async () => {
          if (!deleteAttribute) return
          await apiFetch(`/admin/cim-attribute-mappings/${deleteAttribute.id}`, { method: "DELETE" })
          queryClient.invalidateQueries({ queryKey: ["admin", "cim-attribute-mappings"] })
        }}
      />
    </div>
  )
}

// ---------------------------------------------------------------------------
// Small shared row helpers (the three sections share list chrome)
// ---------------------------------------------------------------------------

function RowShell({
  children,
  canWrite,
  onPreview,
  onEdit,
  onDelete,
}: {
  children: ReactNode
  canWrite: boolean
  // Optional CIM/XML preview action -- read-only, so it shows regardless of
  // canWrite (only class-mapping rows pass it; profiles/attributes don't).
  onPreview?: () => void
  onEdit: () => void
  onDelete: () => void
}) {
  return (
    <div className="hover:bg-muted/50 flex items-center gap-2 rounded-md px-2 py-1">
      {children}
      {onPreview && (
        <Button variant="ghost" size="icon" className="size-7" title="CIM/XML preview" onClick={onPreview}>
          <FileCode2 className="size-4" />
        </Button>
      )}
      {canWrite && (
        <>
          <Button variant="ghost" size="icon" className="size-7" title="Edit" onClick={onEdit}>
            <Pencil className="size-4" />
          </Button>
          <Button variant="ghost" size="icon" className="text-destructive size-7" title="Delete" onClick={onDelete}>
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
