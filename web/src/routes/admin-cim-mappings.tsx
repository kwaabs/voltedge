import { type ReactNode, useMemo, useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
import {
  AlertTriangle,
  BookOpen,
  Check,
  ChevronDown,
  ChevronRight,
  Copy,
  FileCode2,
  HelpCircle,
  Loader2,
  Pencil,
  Plus,
  Trash2,
} from "lucide-react"

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
  type AdminCimRefClass,
  type AdminFeatureClassSummary,
  useAdminCimAttributeMappings,
  useAdminCimClassMappings,
  useAdminCimCoverage,
  useAdminCimDictionaryAttributes,
  useAdminCimDictionaryClasses,
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

// Plain-language labels + a one-line "what it does" hint for the two enums an
// operator has to choose from. Grounded in how the encoder actually treats each
// value (api/internal/export/cim.go writeFeature + applyLiteral), so the editor
// never assumes the reader already knows what "conducting_junction" or
// "domain_map" means. `help` is the single biggest sophistication-reducer here.
const CIM_KIND_META: Record<(typeof CIM_KINDS)[number], { label: string; help: string }> = {
  conducting_edge: {
    label: "Line / edge",
    help: "A cable or line whose shape becomes the CIM segment — the only kind that computes length and impedance.",
  },
  conducting_junction: {
    label: "Node / point device",
    help: "A point of equipment — transformer, switch, capacitor bank — wired into the network.",
  },
  container: {
    label: "Container",
    help: "A boundary that holds equipment — substation, voltage level, bay.",
  },
  asset: {
    label: "Asset",
    help: "Physical equipment with a location but no electrical connectivity of its own.",
  },
  protection: {
    label: "Protection",
    help: "A protective device emitted by name only, with no map geometry — relay, fuse.",
  },
  skip: {
    label: "Skip — don't export",
    help: "Explicitly excluded from CIM export; also silences the unmapped-class warning.",
  },
}

const TRANSFORM_META: Record<(typeof TRANSFORMS)[number], { label: string; help: string }> = {
  direct: { label: "Direct copy", help: "Emit the field value unchanged." },
  scale: { label: "Scale ×factor", help: "Multiply by the scale factor — e.g. kVA → VA (×1000)." },
  domain_map: { label: "Lookup table", help: 'Translate coded values via the value map, e.g. "11KV" → 11000.' },
  boolean: { label: "Boolean", help: "Look the value up in the value map and emit CIM true/false." },
  from_geometry: { label: "From geometry", help: "Derive the value from the feature's shape (e.g. conductor length)." },
  material_spec: { label: "Material spec", help: "Look up engineering values (r, x) from the coded material spec." },
  phase: { label: "Phase code", help: "Translate the phase code to a CIM SinglePhaseKind." },
}

const kindLabel = (k: string) => CIM_KIND_META[k as (typeof CIM_KINDS)[number]]?.label ?? k
const transformLabel = (t: string) => TRANSFORM_META[t as (typeof TRANSFORMS)[number]]?.label ?? t

/** A labelled <select> whose options carry friendly labels, with a one-line hint
 * under it describing the currently-selected value -- so the operator never has to
 * already know what an enum value means to pick the right one. */
function HelpSelect({
  label,
  value,
  onChange,
  options,
}: {
  label: string
  value: string
  onChange: (v: string) => void
  options: Record<string, { label: string; help: string }>
}) {
  return (
    <div className="flex flex-col gap-1">
      <label className={labelClass}>
        {label} <span className="text-destructive">*</span>
      </label>
      <select className={inputClass} value={value} onChange={(e) => onChange(e.target.value)}>
        {Object.entries(options).map(([k, meta]) => (
          <option key={k} value={k}>
            {meta.label}
          </option>
        ))}
      </select>
      {options[value] && <p className="text-muted-foreground text-xs">{options[value].help}</p>}
    </div>
  )
}

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
// CIM vocabulary fields (reference dictionary, migration 0120)
//
// The cim_class / cim_attribute targets used to be raw free-text boxes with no
// validation or discoverability. These fields back them with the CIM reference
// dictionary: a datalist offers the real CIM names (in-profile subset first) and
// an inline hint says whether what's typed is a known CIM target, in the export
// profile, or not in the vocabulary at all. It never BLOCKS a free-text value --
// the dictionary is a superset that may still miss a newer/edge target, so an
// unknown value is a warning, not an error (the server-side CHECKs/validation
// remain the hard gate).
// ---------------------------------------------------------------------------

/** The class part of a dotted CIM attribute target (before the first dot), e.g.
 * "BaseVoltage" from "BaseVoltage.nominalVoltage". Empty when there's no dot. */
function cimClassPart(attr: string): string {
  const dot = attr.indexOf(".")
  return dot > 0 ? attr.slice(0, dot) : ""
}

/** One-line status under a CIM vocab input: green when the value is a known
 * in-profile target, neutral when known but outside the default profile (wider
 * CIM), amber when not found in the dictionary at all. */
function CimVocabHint({ status, text }: { status: "ok" | "wider" | "unknown" | "none"; text: string }) {
  if (status === "none") return null
  if (status === "unknown") {
    return (
      <p className="text-amber-600 dark:text-amber-500 flex items-center gap-1 text-xs">
        <AlertTriangle className="size-3 shrink-0" />
        {text}
      </p>
    )
  }
  if (status === "ok") {
    return (
      <p className="text-emerald-600 dark:text-emerald-500 flex items-center gap-1 text-xs">
        <Check className="size-3 shrink-0" />
        {text}
      </p>
    )
  }
  return <p className="text-muted-foreground text-xs">{text}</p>
}

/** Small amber "not in dictionary" chip for a list row whose free-text CIM target
 * isn't found in the reference vocabulary -- a soft nudge to check the spelling,
 * never a hard error (the value still exports; the encoder is the real gate). */
function UnknownCimChip({ label = "unknown" }: { label?: string }) {
  return (
    <span className="ml-2 inline-flex items-center gap-1 rounded bg-amber-500/10 px-1.5 py-0.5 text-xs text-amber-600 dark:text-amber-500">
      <AlertTriangle className="size-3" />
      {label}
    </span>
  )
}

/** CIM class target field: free-text input backed by the reference dictionary --
 * a datalist of real CIM class names (in-profile first) plus an inline known/
 * in-profile/unknown hint. */
function CimClassField({ value, onChange }: { value: string; onChange: (v: string) => void }) {
  const dict = useAdminCimDictionaryClasses()
  const classes = dict.data?.cim_ref_classes
  const byName = useMemo(() => {
    const m = new Map<string, AdminCimRefClass>()
    classes?.forEach((c) => m.set(c.name, c))
    return m
  }, [classes])
  // Offer the in-profile subset as suggestions; fall back to the full list only
  // if this profile has no membership yet, so the datalist stays light.
  const suggestions = useMemo(() => {
    const inProfile = classes?.filter((c) => c.in_profile) ?? []
    return inProfile.length > 0 ? inProfile : (classes ?? [])
  }, [classes])

  const trimmed = value.trim()
  const match = trimmed ? byName.get(trimmed) : undefined
  const status: "ok" | "wider" | "unknown" | "none" = !trimmed || dict.isPending
    ? "none"
    : match
      ? match.in_profile
        ? "ok"
        : "wider"
      : "unknown"
  const hint =
    status === "ok"
      ? match?.is_abstract
        ? "Known CIM class, but abstract — prefer a concrete subclass."
        : "Known CIM class, in the export profile."
      : status === "wider"
        ? "Known CIM class, but outside the default profile (wider CIM)."
        : "Not in the CIM reference dictionary — check the spelling, or it may be outside the parsed schema."

  return (
    <div className="flex flex-col gap-1">
      <label className={labelClass}>
        CIM class <span className="text-destructive">*</span>
      </label>
      <input
        className={inputClass}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder="e.g. ACLineSegment, PowerTransformer, Breaker"
        list="cim-class-suggestions"
        autoComplete="off"
      />
      <datalist id="cim-class-suggestions">
        {suggestions.map((c) => (
          <option key={c.name} value={c.name}>
            {c.description ? c.description.slice(0, 80) : c.parent_name ? `subclass of ${c.parent_name}` : ""}
          </option>
        ))}
      </datalist>
      <CimVocabHint status={status} text={hint} />
    </div>
  )
}

// VoltEdge sentinel attribute targets: intentionally NOT real CIM. The encoder
// special-cases these (e.g. secondary routes to PowerTransformerEnd winding 2),
// so the validator must not flag them as unknown vocabulary. Keep in step with
// the export encoder's sentinel handling in api/internal/export.
const CIM_SENTINEL_ATTRIBUTES = new Set(["BaseVoltage.nominalVoltage.secondary"])

/** CIM attribute target field: free-text dotted "Class.attribute" input backed by
 * the reference dictionary. Once a class part is typed, that class's attributes --
 * INCLUDING inherited ones -- are offered as suggestions and the inline hint
 * validates the whole target. Because CIM RDF/XML serializes a property under the
 * class that declares it, an inherited target (e.g. RatioTapChanger.step, where
 * step is declared on TapChanger) is valid but non-canonical: the hint says so and
 * offers a one-click rewrite to the declaring-class form a strict consumer keeps. */
function CimAttributeField({ value, onChange }: { value: string; onChange: (v: string) => void }) {
  const classDict = useAdminCimDictionaryClasses()
  const knownClass = useMemo(
    () => new Set(classDict.data?.cim_ref_classes.map((c) => c.name)),
    [classDict.data],
  )

  const trimmed = value.trim()
  const classPart = cimClassPart(trimmed)
  const attrDict = useAdminCimDictionaryAttributes(classPart || null)
  const attrs = attrDict.data?.cim_ref_attributes

  const suggestions = useMemo(() => {
    if (!classPart) return []
    const inProfile = attrs?.filter((a) => a.in_profile) ?? []
    return (inProfile.length > 0 ? inProfile : (attrs ?? [])).map((a) => `${classPart}.${a.name}`)
  }, [attrs, classPart])

  const attrPart = trimmed.slice(classPart.length + 1)
  const matched = attrs?.find((a) => a.name === attrPart)
  const loading = classDict.isPending || (!!classPart && attrDict.isPending)

  let status: "ok" | "wider" | "unknown" | "none" = "none"
  let hint = ""
  // The declaring-class form to rewrite an inherited target to, when one applies.
  let canonical: string | null = null
  if (trimmed && !loading) {
    if (CIM_SENTINEL_ATTRIBUTES.has(trimmed)) {
      status = "wider"
      hint = "VoltEdge sentinel target — handled specially by the encoder, not a real CIM property."
    } else if (!classPart) {
      status = "unknown"
      hint = "Use ClassName.attribute — e.g. BaseVoltage.nominalVoltage."
    } else if (!knownClass.has(classPart)) {
      status = "unknown"
      hint = `"${classPart}" is not a known CIM class in the reference dictionary.`
    } else if (!matched) {
      status = "unknown"
      hint = `"${attrPart}" is not an attribute of ${classPart} or its superclasses.`
    } else if (matched.inherited && matched.declaring_class && matched.declaring_class !== classPart) {
      // Valid by inheritance, but CIM RDF/XML emits it under the declaring class;
      // a strict consumer drops the subclass-qualified form. Nudge to canonical.
      canonical = `${matched.declaring_class}.${attrPart}`
      status = "wider"
      hint = `Inherited from ${matched.declaring_class}. CIM serializes it as ${canonical}; a strict consumer may drop ${classPart}.${attrPart}.`
    } else if (matched.in_profile) {
      status = "ok"
      hint = `Known CIM attribute of ${classPart}, in the export profile.`
    } else {
      status = "wider"
      hint = `Known CIM attribute of ${classPart}, but outside the default profile (wider CIM).`
    }
  }

  return (
    <div className="flex flex-col gap-1">
      <label className={labelClass}>
        CIM attribute <span className="text-destructive">*</span>
      </label>
      <input
        className={inputClass}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder="e.g. BaseVoltage.nominalVoltage"
        list="cim-attribute-suggestions"
        autoComplete="off"
      />
      <datalist id="cim-attribute-suggestions">
        {suggestions.map((s) => (
          <option key={s} value={s} />
        ))}
      </datalist>
      <CimVocabHint status={status} text={hint} />
      {canonical && (
        <button
          type="button"
          className="text-primary self-start text-xs underline underline-offset-2"
          onClick={() => onChange(canonical!)}
        >
          Use {canonical}
        </button>
      )}
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
      <CimClassField value={cimClass} onChange={setCimClass} />
      <HelpSelect label="CIM kind" value={cimKind} onChange={setCimKind} options={CIM_KIND_META} />
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
  prefill,
  onDone,
}: {
  existing: AdminCimAttributeMapping | null
  classes: AdminFeatureClassSummary[] | undefined
  // Feature class code to preselect when adding a field from within a class group
  // -- ignored on edit (the key is fixed there).
  prefill?: string
  onDone: () => void
}) {
  const [featureClassCode, setFeatureClassCode] = useState(existing?.feature_class_code ?? prefill ?? "")
  const [fieldName, setFieldName] = useState(existing?.field_name ?? "")
  const [cimAttribute, setCimAttribute] = useState(existing?.cim_attribute ?? "")
  const [transform, setTransform] = useState(existing?.transform ?? TRANSFORMS[0])
  const [scaleFactor, setScaleFactor] = useState(existing?.scale_factor != null ? String(existing.scale_factor) : "")
  const [unit, setUnit] = useState(existing?.unit ?? "")
  const [valueMap, setValueMap] = useState(existing?.value_map ? JSON.stringify(existing.value_map, null, 2) : "")
  const [saving, setSaving] = useState(false)
  const queryClient = useQueryClient()

  // Progressive disclosure: a scale factor only matters for `scale`, a value map
  // only for `domain_map`/`boolean`. We show (and require) each field only for the
  // transforms that use it, so the form never asks for something irrelevant.
  const needsScale = transform === "scale"
  const needsValueMap = transform === "domain_map" || transform === "boolean"
  // Validate the value map whenever it's non-empty so a stray blob can't slip
  // through -- an unparseable one blocks submit rather than reaching the server.
  const valueMapValid = valueMap.trim() === "" || tryParseJson(valueMap) !== undefined
  const canSave =
    featureClassCode.trim().length > 0 &&
    fieldName.trim().length > 0 &&
    cimAttribute.trim().length > 0 &&
    transform.trim().length > 0 &&
    valueMapValid &&
    (!needsScale || scaleFactor.trim() !== "") &&
    (!needsValueMap || valueMap.trim() !== "")

  async function handleSave() {
    setSaving(true)
    try {
      const parsedValueMap = valueMap.trim() === "" ? null : tryParseJson(valueMap)
      // Only send the parameters the chosen transform actually uses, so switching
      // transforms never leaves a stale scale factor / value map on the row.
      const common: Record<string, unknown> = {
        cim_attribute: cimAttribute.trim(),
        transform,
        scale_factor: needsScale && scaleFactor.trim() !== "" ? Number(scaleFactor) : null,
        unit: unit.trim() || null,
        value_map: needsValueMap ? (parsedValueMap ?? null) : null,
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
      <CimAttributeField value={cimAttribute} onChange={setCimAttribute} />
      <HelpSelect label="Transform" value={transform} onChange={setTransform} options={TRANSFORM_META} />
      {needsScale && (
        <div className="flex flex-col gap-1">
          <label className={labelClass}>
            Scale factor <span className="text-destructive">*</span>
          </label>
          <input
            type="number"
            className={inputClass}
            value={scaleFactor}
            onChange={(e) => setScaleFactor(e.target.value)}
            placeholder="e.g. 1000 for kVA → VA"
          />
        </div>
      )}
      {needsValueMap && (
        <div className="flex flex-col gap-1">
          <label className={labelClass}>
            Value map (JSON) <span className="text-destructive">*</span>
          </label>
          <textarea
            className="border-input bg-background focus-visible:border-ring focus-visible:ring-ring/50 h-24 w-full rounded-md border px-2 py-1 font-mono text-sm outline-none focus-visible:ring-[3px]"
            value={valueMap}
            onChange={(e) => setValueMap(e.target.value)}
            placeholder={'{"11KV": 11000, "33KV": 33000}'}
          />
          {!valueMapValid && <p className="text-destructive text-xs">Not valid JSON.</p>}
        </div>
      )}
      <div className="flex flex-col gap-1">
        <label className={labelClass}>Unit</label>
        <input className={inputClass} value={unit} onChange={(e) => setUnit(e.target.value)} placeholder="e.g. m, V, ohm" />
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
// CIM vocabulary browser
// ---------------------------------------------------------------------------

/** A read-only browser over the whole CIM reference dictionary (migration 0120,
 * generated by cmd/cimdict from schema/cim/): every class in a searchable list and,
 * on selection, its properties (own + inherited). This is distinct from the pickers
 * inside the mapping dialogs, which only autocomplete the in-profile subset --
 * here the ENTIRE vocabulary is shown with an in-profile badge and a
 * narrow-to-profile toggle, so an operator can look up what CIM offers before
 * mapping to it. Rendered only when its panel is open, so it doesn't fetch until
 * asked. A profile selector chooses which export target the in_profile flag and
 * narrow-to-profile filter track (defaulting to the default profile). */
function CimDictionaryBrowser() {
  // Which export target the in_profile flag + narrow-to-profile filter track. An
  // empty choice follows the default profile; the server also treats an omitted
  // profile as the default, so `activeProfile || undefined` is passed through.
  const profilesQuery = useAdminCimProfiles()
  const profiles = useMemo(() => profilesQuery.data?.cim_profiles ?? [], [profilesQuery.data])
  const defaultProfileCode = useMemo(
    () => profiles.find((p) => p.is_default)?.code ?? profiles[0]?.code ?? "",
    [profiles],
  )
  const [profileChoice, setProfileChoice] = useState("")
  const activeProfile = profileChoice || defaultProfileCode

  const classesQuery = useAdminCimDictionaryClasses(activeProfile || undefined)
  const [search, setSearch] = useState("")
  const [inProfileOnly, setInProfileOnly] = useState(false)
  const [selected, setSelected] = useState<string | null>(null)
  const attrsQuery = useAdminCimDictionaryAttributes(selected, activeProfile || undefined)

  const allClasses = classesQuery.data?.cim_ref_classes
  const filtered = useMemo(() => {
    if (!allClasses) return []
    const q = search.trim().toLowerCase()
    return allClasses.filter((c) => {
      if (inProfileOnly && !c.in_profile) return false
      if (q === "") return true
      return c.name.toLowerCase().includes(q) || (c.parent_name?.toLowerCase().includes(q) ?? false)
    })
  }, [allClasses, search, inProfileOnly])

  const selectedClass = useMemo(() => allClasses?.find((c) => c.name === selected) ?? null, [allClasses, selected])

  if (classesQuery.isPending) return <LoadingRow label="CIM dictionary" />
  if (classesQuery.isError || !allClasses) {
    return (
      <div className="text-sm">
        <ErrorRow label="the CIM dictionary" />
        <p className="text-muted-foreground mt-1 text-xs">
          The reference tables may not be seeded yet &mdash; run{" "}
          <span className="font-mono">task seed-cim-reference</span>.
        </p>
      </div>
    )
  }

  return (
    <div className="grid gap-3 md:grid-cols-[minmax(0,1fr)_minmax(0,1.4fr)]">
      {/* Left: profile selector + searchable class list */}
      <div className="flex min-w-0 flex-col gap-2">
        {profiles.length > 0 && (
          <div className="flex flex-col gap-1">
            <label className={labelClass}>Profile</label>
            <select
              className={inputClass}
              value={activeProfile}
              onChange={(e) => setProfileChoice(e.target.value)}
            >
              {profiles.map((p) => (
                <option key={p.code} value={p.code}>
                  {p.name}
                  {p.is_default ? " (default)" : ""}
                </option>
              ))}
            </select>
            <p className="text-muted-foreground text-xs">Sets which target the “in profile” badge and filter track.</p>
          </div>
        )}
        <input
          className={inputClass}
          placeholder="Search CIM classes…"
          value={search}
          onChange={(e) => setSearch(e.target.value)}
        />
        <label className="text-muted-foreground flex items-center gap-2 text-xs">
          <input type="checkbox" checked={inProfileOnly} onChange={(e) => setInProfileOnly(e.target.checked)} />
          In this profile only
        </label>
        <p className="text-muted-foreground text-xs">
          {filtered.length} of {allClasses.length} classes
        </p>
        <div className="max-h-96 overflow-y-auto rounded-md border">
          {filtered.length === 0 ? (
            <p className="text-muted-foreground p-2 text-sm">No classes match.</p>
          ) : (
            filtered.map((c) => (
              <button
                key={c.name}
                type="button"
                onClick={() => setSelected(c.name)}
                className={`flex w-full items-center gap-2 px-2 py-1 text-left ${
                  selected === c.name ? "bg-primary/10" : "hover:bg-muted/50"
                }`}
              >
                <span className="min-w-0 flex-1 truncate">
                  <span className="font-mono text-xs">{c.name}</span>
                  {c.is_abstract && <span className="text-muted-foreground ml-1 text-xs">(abstract)</span>}
                </span>
                {c.in_profile && (
                  <span className="bg-primary/10 text-primary shrink-0 rounded px-1.5 py-0.5 text-xs">in profile</span>
                )}
              </button>
            ))
          )}
        </div>
      </div>

      {/* Right: selected class detail + its attributes */}
      <div className="min-w-0">
        {!selected || !selectedClass ? (
          <p className="text-muted-foreground p-2 text-sm">Select a class to see its attributes.</p>
        ) : (
          <div className="flex flex-col gap-2">
            <div>
              <div className="flex flex-wrap items-center gap-2">
                <span className="font-mono text-sm font-medium">{selectedClass.name}</span>
                {selectedClass.in_profile && (
                  <span className="bg-primary/10 text-primary rounded px-1.5 py-0.5 text-xs">in profile</span>
                )}
                {selectedClass.is_abstract && (
                  <span className="text-muted-foreground rounded border px-1.5 py-0.5 text-xs">abstract</span>
                )}
              </div>
              {selectedClass.parent_name && (
                <p className="text-muted-foreground text-xs">
                  extends <span className="font-mono">{selectedClass.parent_name}</span>
                </p>
              )}
              {selectedClass.description && (
                <p className="text-muted-foreground mt-1 text-xs">{selectedClass.description}</p>
              )}
            </div>

            {attrsQuery.isPending ? (
              <LoadingRow label="attributes" />
            ) : attrsQuery.isError || !attrsQuery.data ? (
              <ErrorRow label="attributes" />
            ) : attrsQuery.data.cim_ref_attributes.length === 0 ? (
              <p className="text-muted-foreground text-sm">This class declares no attributes.</p>
            ) : (
              <div className="max-h-96 overflow-y-auto rounded-md border">
                <table className="w-full text-sm">
                  <thead className="bg-muted/50 text-muted-foreground sticky top-0 text-left text-xs">
                    <tr>
                      <th className="px-2 py-1 font-medium">Attribute</th>
                      <th className="px-2 py-1 font-medium">Type / range</th>
                      <th className="px-2 py-1 font-medium">Mult.</th>
                    </tr>
                  </thead>
                  <tbody>
                    {attrsQuery.data.cim_ref_attributes.map((a) => {
                      // Show the canonical (declaring-class-qualified) target when the
                      // property is inherited, since that's the form the encoder should
                      // emit; the plain name suffices when the class declares it itself.
                      const inheritedElsewhere = a.declaring_class && a.declaring_class !== selectedClass.name
                      return (
                        <tr key={a.name} className="border-t align-top">
                          <td className="px-2 py-1">
                            <span className="font-mono text-xs">
                              {inheritedElsewhere ? `${a.declaring_class}.${a.name}` : a.name}
                            </span>
                            {a.inherited && <span className="text-muted-foreground ml-1 text-[10px]">inherited</span>}
                            {a.in_profile && <span className="text-primary ml-1 text-[10px]">in profile</span>}
                            {a.description && <p className="text-muted-foreground text-xs">{a.description}</p>}
                          </td>
                          <td className="px-2 py-1 text-xs">
                            {a.is_association ? "→ " : ""}
                            {a.range || (a.is_association ? "(association)" : "literal")}
                          </td>
                          <td className="text-muted-foreground px-2 py-1 text-xs">{a.multiplicity || "—"}</td>
                        </tr>
                      )
                    })}
                  </tbody>
                </table>
              </div>
            )}
          </div>
        )}
      </div>
    </div>
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
  const cimDict = useAdminCimDictionaryClasses()
  const queryClient = useQueryClient()
  const canWrite = usePermission()("metadata:write")

  const classes = featureClassData?.feature_classes

  // Every CIM class name in the reference dictionary, for flagging existing rows
  // whose free-text target isn't in the vocabulary. Undefined while the dictionary
  // is still loading (or if it failed) -- treated as "can't judge", so no chip.
  const knownClassNames = useMemo(() => {
    if (!cimDict.data) return undefined
    return new Set(cimDict.data.cim_ref_classes.map((c) => c.name))
  }, [cimDict.data])

  // Friendly feature-class name by code, so a group header can read
  // "Distribution Transformer DIST_XFMR" instead of just the code.
  const classNameByCode = useMemo(() => {
    const m = new Map<string, string>()
    for (const c of classes ?? []) m.set(c.code, c.name)
    return m
  }, [classes])

  // Class-centric grouping: gather each feature class's class mapping(s) and its
  // field mappings under one group, so an operator sees a class and everything CIM
  // knows about it in one place -- instead of two disconnected flat lists they have
  // to mentally join by code. A group with attrs but no classMaps is an orphan (its
  // fields are dropped because the class itself isn't mapped) -- surfaced as such.
  const groups = useMemo(() => {
    const byCode = new Map<string, { classMaps: AdminCimClassMapping[]; attrs: AdminCimAttributeMapping[] }>()
    const ensure = (code: string) => {
      let g = byCode.get(code)
      if (!g) {
        g = { classMaps: [], attrs: [] }
        byCode.set(code, g)
      }
      return g
    }
    for (const m of classMappings.data?.cim_class_mappings ?? []) ensure(m.feature_class_code).classMaps.push(m)
    for (const a of attributeMappings.data?.cim_attribute_mappings ?? []) ensure(a.feature_class_code).attrs.push(a)
    return [...byCode.entries()].map(([code, g]) => ({ code, ...g })).sort((a, b) => a.code.localeCompare(b.code))
  }, [classMappings.data, attributeMappings.data])

  // Which class groups are expanded to reveal their field mappings.
  const [expanded, setExpanded] = useState<Set<string>>(() => new Set())
  const toggleGroup = (code: string) =>
    setExpanded((prev) => {
      const next = new Set(prev)
      if (next.has(code)) next.delete(code)
      else next.add(code)
      return next
    })

  // The collapsible "How this works" explainer -- collapsed by default so it never
  // shouts at someone who already knows the drill.
  const [showHelp, setShowHelp] = useState(false)
  // The collapsible CIM vocabulary browser -- also collapsed (and only mounts its
  // data fetch) until opened.
  const [showDict, setShowDict] = useState(false)

  // Dialog targets: "new" opens a create dialog, a row opens an edit dialog. The
  // class form also takes an optional prefill (from the coverage card's "Map").
  const [profileForm, setProfileForm] = useState<{ existing: AdminCimProfile | null } | null>(null)
  const [classForm, setClassForm] = useState<{ existing: AdminCimClassMapping | null; prefill?: string } | null>(null)
  const [attributeForm, setAttributeForm] = useState<{
    existing: AdminCimAttributeMapping | null
    prefill?: string
  } | null>(null)

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

        {/* Collapsible plain-language explainer -- three steps, no CIM jargon,
            collapsed by default. */}
        <div className="rounded-md border">
          <button
            type="button"
            className="hover:bg-muted/50 flex w-full items-center gap-2 rounded-md px-3 py-2 text-left text-sm font-medium"
            onClick={() => setShowHelp((v) => !v)}
          >
            <HelpCircle className="size-4 shrink-0 text-muted-foreground" />
            <span className="flex-1">How this works</span>
            {showHelp ? (
              <ChevronDown className="size-4 text-muted-foreground" />
            ) : (
              <ChevronRight className="size-4 text-muted-foreground" />
            )}
          </button>
          {showHelp && (
            <div className="text-muted-foreground border-t px-3 py-3 text-sm">
              <p>Export walks every feature and turns it into CIM/XML in three steps:</p>
              <ol className="mt-2 flex flex-col gap-2">
                <li>
                  <span className="text-foreground font-medium">1. Profile</span> &mdash; the export target (which CIM
                  flavour and namespaces the file uses). One profile is the default when a request names none.
                </li>
                <li>
                  <span className="text-foreground font-medium">2. Map a class</span> &mdash; say what CIM object a
                  feature class becomes, e.g. a distribution transformer &rarr;{" "}
                  <span className="font-mono text-xs">PowerTransformer</span>, and what <em>kind</em> of thing it is
                  (a line, a point device, a container&hellip;). A class with no mapping is silently left out of the
                  export &mdash; the amber panel below lists those.
                </li>
                <li>
                  <span className="text-foreground font-medium">3. Add field mappings</span> &mdash; for each class,
                  carry its fields across to CIM attributes, e.g.{" "}
                  <span className="font-mono text-xs">rated_kva</span> &rarr;{" "}
                  <span className="font-mono text-xs">PowerTransformerEnd.ratedS</span>. A transform can convert the
                  value on the way (scale it, look it up in a table&hellip;).
                </li>
              </ol>
              <p className="mt-2">
                A CIM attribute is written{" "}
                <span className="font-mono text-xs">Class.attribute</span> &mdash; use the class that <em>declares</em>{" "}
                the attribute, and the picker will suggest the canonical one for you.
              </p>
            </div>
          )}
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
            prefill={attributeForm.prefill}
            onDone={() => setAttributeForm(null)}
          />
        )}
        {previewClass && <PreviewDialog classCode={previewClass} onDone={() => setPreviewClass(null)} />}

        {/* Read-only browser over the whole TTL-derived CIM vocabulary -- collapsed
            by default; the browser only fetches once opened. */}
        <Card>
          <CardHeader className="pb-2">
            <button
              type="button"
              className="hover:bg-muted/50 -mx-2 flex items-center gap-2 rounded-md px-2 py-1 text-left"
              onClick={() => setShowDict((v) => !v)}
            >
              <BookOpen className="text-muted-foreground size-4 shrink-0" />
              <div className="min-w-0 flex-1">
                <CardTitle className="text-sm">Browse CIM vocabulary</CardTitle>
                <CardDescription>
                  Every CIM class + attribute from the reference dictionary &mdash; look one up before you map to it.
                </CardDescription>
              </div>
              {showDict ? (
                <ChevronDown className="text-muted-foreground size-4" />
              ) : (
                <ChevronRight className="text-muted-foreground size-4" />
              )}
            </button>
          </CardHeader>
          {showDict && (
            <CardContent className="pt-0">
              <CimDictionaryBrowser />
            </CardContent>
          )}
        </Card>

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

        {/* 2. Mappings, grouped by feature class -- each class shows the CIM class
            it becomes with its field mappings nested beneath, so the two former
            flat lists (class + attribute) read as one thing per class. */}
        <Card>
          <CardHeader className="flex flex-row items-center gap-2 pb-2">
            <div className="min-w-0 flex-1">
              <CardTitle className="text-sm">Mappings</CardTitle>
              <CardDescription>
                Each feature class, the CIM class it becomes, and the fields carried across. Click a class to see its
                fields.
              </CardDescription>
            </div>
            {canWrite && (
              <Button size="sm" onClick={() => setClassForm({ existing: null })}>
                <Plus className="size-4" />
                Map a class
              </Button>
            )}
          </CardHeader>
          <CardContent className="pt-0">
            {classMappings.isPending || attributeMappings.isPending ? (
              <LoadingRow label="mappings" />
            ) : classMappings.isError ||
              attributeMappings.isError ||
              !classMappings.data ||
              !attributeMappings.data ? (
              <ErrorRow label="mappings" />
            ) : groups.length === 0 ? (
              <EmptyRow label="mappings" />
            ) : (
              <div className="flex flex-col gap-1.5">
                {groups.map((g) => {
                  const isOpen = expanded.has(g.code)
                  const primary = g.classMaps[0]
                  const className = classNameByCode.get(g.code)
                  const classIssue =
                    primary != null &&
                    primary.cim_kind !== "skip" &&
                    knownClassNames !== undefined &&
                    primary.cim_class.trim() !== "" &&
                    !knownClassNames.has(primary.cim_class.trim())
                  const attrIssues =
                    knownClassNames === undefined
                      ? 0
                      : g.attrs.filter((a) => {
                          const part = cimClassPart(a.cim_attribute.trim())
                          return part !== "" && !knownClassNames.has(part)
                        }).length
                  return (
                    <div key={g.code} className="rounded-md border">
                      {/* Header row: expand toggle + class summary + class actions */}
                      <div className="flex items-center gap-2 px-2 py-1.5">
                        <button
                          type="button"
                          className="hover:bg-muted flex min-w-0 flex-1 items-center gap-2 rounded px-1 py-0.5 text-left"
                          onClick={() => toggleGroup(g.code)}
                        >
                          {isOpen ? (
                            <ChevronDown className="text-muted-foreground size-4 shrink-0" />
                          ) : (
                            <ChevronRight className="text-muted-foreground size-4 shrink-0" />
                          )}
                          <span className="min-w-0 flex-1 truncate text-sm">
                            {className && <span className="font-medium">{className} </span>}
                            <span className="font-mono text-xs">{g.code}</span>
                            {primary ? (
                              <>
                                <span className="text-muted-foreground"> &rarr; </span>
                                <span className="font-medium">{primary.cim_class}</span>{" "}
                                <span className="text-muted-foreground text-xs">{kindLabel(primary.cim_kind)}</span>
                                {classIssue && <UnknownCimChip />}
                              </>
                            ) : (
                              <span className="ml-2 inline-flex items-center gap-1 rounded bg-amber-500/10 px-1.5 py-0.5 text-xs text-amber-600 dark:text-amber-500">
                                <AlertTriangle className="size-3" />
                                no class mapping &mdash; fields dropped
                              </span>
                            )}
                          </span>
                          <span className="text-muted-foreground shrink-0 text-xs">
                            {g.attrs.length} {g.attrs.length === 1 ? "field" : "fields"}
                            {attrIssues > 0 && (
                              <span className="text-amber-600 dark:text-amber-500"> · {attrIssues} to fix</span>
                            )}
                          </span>
                        </button>
                        {primary && (
                          <Button
                            variant="ghost"
                            size="icon"
                            className="size-7"
                            title="CIM/XML preview"
                            onClick={() => setPreviewClass(g.code)}
                          >
                            <FileCode2 className="size-4" />
                          </Button>
                        )}
                        {canWrite &&
                          (primary ? (
                            <>
                              <Button
                                variant="ghost"
                                size="icon"
                                className="size-7"
                                title="Edit class mapping"
                                onClick={() => setClassForm({ existing: primary })}
                              >
                                <Pencil className="size-4" />
                              </Button>
                              <Button
                                variant="ghost"
                                size="icon"
                                className="text-destructive size-7"
                                title="Delete class mapping"
                                onClick={() => setDeleteClass(primary)}
                              >
                                <Trash2 className="size-4" />
                              </Button>
                            </>
                          ) : (
                            <Button
                              variant="ghost"
                              size="sm"
                              className="h-7"
                              onClick={() => setClassForm({ existing: null, prefill: g.code })}
                            >
                              Map class
                            </Button>
                          ))}
                      </div>

                      {/* Expanded body: any per-subtype class maps, then field mappings. */}
                      {isOpen && (
                        <div className="border-t py-1.5 pl-8 pr-2">
                          {g.classMaps.slice(1).map((m) => (
                            <div
                              key={m.id}
                              className="hover:bg-muted/50 flex items-center gap-2 rounded-md px-2 py-1"
                            >
                              <span className="min-w-0 flex-1 truncate text-sm">
                                <span className="text-muted-foreground text-xs">subtype {m.subtype_code}</span>
                                <span className="text-muted-foreground"> &rarr; </span>
                                <span className="font-medium">{m.cim_class}</span>{" "}
                                <span className="text-muted-foreground text-xs">{kindLabel(m.cim_kind)}</span>
                              </span>
                              {canWrite && (
                                <>
                                  <Button
                                    variant="ghost"
                                    size="icon"
                                    className="size-7"
                                    title="Edit"
                                    onClick={() => setClassForm({ existing: m })}
                                  >
                                    <Pencil className="size-4" />
                                  </Button>
                                  <Button
                                    variant="ghost"
                                    size="icon"
                                    className="text-destructive size-7"
                                    title="Delete"
                                    onClick={() => setDeleteClass(m)}
                                  >
                                    <Trash2 className="size-4" />
                                  </Button>
                                </>
                              )}
                            </div>
                          ))}

                          {g.attrs.length === 0 ? (
                            <p className="text-muted-foreground px-2 py-1 text-xs">
                              No field mappings &mdash; only identity (name + mRID) is exported for this class.
                            </p>
                          ) : (
                            g.attrs.map((m) => {
                              const attrIssue =
                                knownClassNames !== undefined &&
                                cimClassPart(m.cim_attribute.trim()) !== "" &&
                                !knownClassNames.has(cimClassPart(m.cim_attribute.trim()))
                              return (
                                <div
                                  key={m.id}
                                  className="hover:bg-muted/50 flex items-center gap-2 rounded-md px-2 py-1"
                                >
                                  <span className="min-w-0 flex-1 truncate text-sm">
                                    <span className="font-mono text-xs">{m.field_name}</span>
                                    <span className="text-muted-foreground"> &rarr; </span>
                                    <span className="font-medium">{m.cim_attribute}</span>{" "}
                                    <span className="text-muted-foreground text-xs">{transformLabel(m.transform)}</span>
                                    {attrIssue && <UnknownCimChip label="unknown class" />}
                                  </span>
                                  {canWrite && (
                                    <>
                                      <Button
                                        variant="ghost"
                                        size="icon"
                                        className="size-7"
                                        title="Edit"
                                        onClick={() => setAttributeForm({ existing: m })}
                                      >
                                        <Pencil className="size-4" />
                                      </Button>
                                      <Button
                                        variant="ghost"
                                        size="icon"
                                        className="text-destructive size-7"
                                        title="Delete"
                                        onClick={() => setDeleteAttribute(m)}
                                      >
                                        <Trash2 className="size-4" />
                                      </Button>
                                    </>
                                  )}
                                </div>
                              )
                            })
                          )}

                          {canWrite && (
                            <Button
                              variant="ghost"
                              size="sm"
                              className="mt-1 h-7"
                              onClick={() => setAttributeForm({ existing: null, prefill: g.code })}
                            >
                              <Plus className="size-4" />
                              Add field mapping
                            </Button>
                          )}
                        </div>
                      )}
                    </div>
                  )
                })}
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
