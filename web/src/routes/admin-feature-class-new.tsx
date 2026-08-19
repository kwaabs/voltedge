import { useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { CheckCircle2, ChevronLeft, Loader2, Plus, Trash2 } from "lucide-react"
import { useNavigate } from "react-router"

import { IconPicker } from "@/components/admin/icon-picker"
import { inputClass } from "@/components/map/field-utils"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { useAdminDomains, useAdminFieldGroups } from "@/hooks/use-admin"
import { ApiError, apiFetch } from "@/lib/api"
import { LINE_DASH_STYLES } from "@/lib/line-dash"
import { useToastStore } from "@/stores/toast-store"

const GEOMETRY_TYPES = ["point", "line", "polygon", "none"] as const
const NETWORK_ROLES = ["none", "edge", "junction", "container"] as const
const DATA_TYPES = ["text", "numeric", "integer", "boolean", "date", "timestamp", "uuid", "jsonb"] as const
const TERMINAL_ROLES = ["primary", "secondary", "ground", "other"] as const

interface DraftSubtype {
  code: number
  name: string
}

interface DraftField {
  name: string
  dataType: (typeof DATA_TYPES)[number]
  domainCode: string
  groupCode: string
  nullable: boolean
  requiredForSubtypes: number[]
}

interface DraftTerminal {
  code: string
  role: (typeof TERMINAL_ROLES)[number]
  phaseCapable: boolean
  subtypeCode: number | ""
}

const labelClass = "text-xs font-medium"

/**
 * Builds and submits one createFeatureClassRequest -- everything a
 * brand-new feature class needs in one transaction (see
 * api/internal/admin/create.go), the same shape every hand-written
 * seed/*.sql file has built by hand so far. Domains and field groups
 * aren't created inline here -- see fieldRequest's own doc comment in
 * that package -- so this form's domain/group pickers only ever offer
 * what already exists (create one first via /admin/domains or
 * /admin/field-groups if it's missing).
 */
export function AdminFeatureClassNewPage() {
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const { data: domainsData } = useAdminDomains()
  const { data: groupsData } = useAdminFieldGroups()

  const [code, setCode] = useState("")
  const [name, setName] = useState("")
  const [geometryType, setGeometryType] = useState<(typeof GEOMETRY_TYPES)[number]>("point")
  const [srid, setSrid] = useState(4326)
  const [networkRole, setNetworkRole] = useState<(typeof NETWORK_ROLES)[number]>("none")
  const [isDiagramContainer, setIsDiagramContainer] = useState(false)

  const [subtypes, setSubtypes] = useState<DraftSubtype[]>([])
  const [fields, setFields] = useState<DraftField[]>([])
  const [terminals, setTerminals] = useState<DraftTerminal[]>([])

  const [symbologyEnabled, setSymbologyEnabled] = useState(true)
  const [iconRef, setIconRef] = useState("circle")
  const [color, setColor] = useState("#2563eb")
  const [width, setWidth] = useState(3)
  const [dash, setDash] = useState<string>("solid")

  const [saving, setSaving] = useState(false)
  const [createdCode, setCreatedCode] = useState<string | null>(null)
  const [compiling, setCompiling] = useState(false)

  const needsTerminals = networkRole === "edge" || networkRole === "junction"

  const canSave =
    code.trim().length > 0 &&
    name.trim().length > 0 &&
    (geometryType === "none" || srid > 0) &&
    fields.every((f) => f.name.trim().length > 0) &&
    subtypes.every((s) => s.name.trim().length > 0) &&
    terminals.every((t) => t.code.trim().length > 0)

  async function handleSave() {
    setSaving(true)
    try {
      const created = await apiFetch<{ code: string }>("/admin/feature-classes", {
        method: "POST",
        body: JSON.stringify({
          code: code.trim().toUpperCase(),
          name: name.trim(),
          geometry_type: geometryType,
          srid: geometryType === "none" ? null : srid,
          network_role: networkRole,
          is_diagram_container: isDiagramContainer,
          subtypes: subtypes.map((s) => ({ code: s.code, name: s.name.trim() })),
          fields: fields.map((f) => ({
            name: f.name.trim(),
            data_type: f.dataType,
            domain_code: f.domainCode || null,
            group_code: f.groupCode || null,
            nullable: f.nullable,
            visible_on_create: true,
            visible_on_update: true,
            editable_after_publish: true,
            required_for_subtypes: f.requiredForSubtypes,
          })),
          terminals: needsTerminals
            ? terminals.map((t) => ({
                code: t.code.trim(),
                role: t.role,
                phase_capable: t.phaseCapable,
                subtype_code: t.subtypeCode === "" ? null : t.subtypeCode,
              }))
            : [],
          symbology: symbologyEnabled
            ? {
                icon_ref: iconRef,
                color,
                width: geometryType === "line" ? width : null,
                dash: geometryType === "line" && dash !== "solid" ? dash : null,
              }
            : null,
        }),
      })
      queryClient.invalidateQueries({ queryKey: ["admin", "feature-classes"] })
      setCreatedCode(created.code)
    } catch (err) {
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not create this feature class.")
    } finally {
      setSaving(false)
    }
  }

  async function handleCompile() {
    if (!createdCode) return
    setCompiling(true)
    try {
      await apiFetch(`/admin/feature-classes/${createdCode}/compile`, { method: "POST" })
      queryClient.invalidateQueries({ queryKey: ["admin", "feature-classes"] })
      queryClient.invalidateQueries({ queryKey: ["feature-classes"] })
      navigate("/admin/feature-classes")
    } catch (err) {
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not compile this class.")
    } finally {
      setCompiling(false)
    }
  }

  if (createdCode) {
    return (
      <div className="h-full overflow-y-auto p-4">
        <div className="mx-auto flex max-w-2xl flex-col items-center gap-4 py-16 text-center">
          <CheckCircle2 className="text-primary size-10" />
          <div>
            <h1 className="text-lg font-semibold">{createdCode} created</h1>
            <p className="text-muted-foreground text-sm">
              Its metadata is saved as a draft. Compile it now to create the real physical table, or leave it a
              draft and compile later from the feature classes list.
            </p>
          </div>
          <div className="flex gap-2">
            <Button variant="outline" onClick={() => navigate("/admin/feature-classes")}>
              Leave as draft
            </Button>
            <Button onClick={handleCompile} disabled={compiling}>
              {compiling && <Loader2 className="size-4 animate-spin" />}
              Compile now
            </Button>
          </div>
        </div>
      </div>
    )
  }

  return (
    <div className="h-full overflow-y-auto p-4">
      <div className="mx-auto flex max-w-3xl flex-col gap-4">
        <div className="flex items-center gap-2">
          <Button variant="ghost" size="icon" className="size-7" onClick={() => navigate("/admin/feature-classes")}>
            <ChevronLeft className="size-4" />
          </Button>
          <div>
            <h1 className="text-lg font-semibold">New feature class</h1>
            <p className="text-muted-foreground text-sm">
              Everything a class needs in one step -- subtypes, fields, terminals, a default map style.
            </p>
          </div>
        </div>

        <Card>
          <CardHeader>
            <CardTitle className="text-base">Basic info</CardTitle>
          </CardHeader>
          <CardContent className="flex flex-col gap-3">
            <div className="grid grid-cols-2 gap-3">
              <div className="flex flex-col gap-1">
                <label className={labelClass}>
                  Code <span className="text-destructive">*</span>
                </label>
                <input
                  className={inputClass}
                  value={code}
                  onChange={(e) => setCode(e.target.value.toUpperCase())}
                  placeholder="e.g. STREET_LIGHT"
                />
              </div>
              <div className="flex flex-col gap-1">
                <label className={labelClass}>
                  Name <span className="text-destructive">*</span>
                </label>
                <input className={inputClass} value={name} onChange={(e) => setName(e.target.value)} placeholder="e.g. Street Light" />
              </div>
            </div>
            <div className="grid grid-cols-3 gap-3">
              <div className="flex flex-col gap-1">
                <label className={labelClass}>Geometry type</label>
                <select className={inputClass} value={geometryType} onChange={(e) => setGeometryType(e.target.value as typeof geometryType)}>
                  {GEOMETRY_TYPES.map((g) => (
                    <option key={g} value={g}>
                      {g}
                    </option>
                  ))}
                </select>
              </div>
              {geometryType !== "none" && (
                <div className="flex flex-col gap-1">
                  <label className={labelClass}>SRID</label>
                  <input type="number" className={inputClass} value={srid} onChange={(e) => setSrid(Number(e.target.value))} />
                </div>
              )}
              <div className="flex flex-col gap-1">
                <label className={labelClass}>Network role</label>
                <select className={inputClass} value={networkRole} onChange={(e) => setNetworkRole(e.target.value as typeof networkRole)}>
                  {NETWORK_ROLES.map((n) => (
                    <option key={n} value={n}>
                      {n}
                    </option>
                  ))}
                </select>
              </div>
            </div>
            <label className="flex items-center gap-2 text-sm">
              <input type="checkbox" checked={isDiagramContainer} onChange={(e) => setIsDiagramContainer(e.target.checked)} />
              Can host a one-line/schematic diagram (is_diagram_container)
            </label>
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle className="text-base">Subtypes</CardTitle>
            <CardDescription>Optional -- a schema-level partition, e.g. overhead vs. underground.</CardDescription>
          </CardHeader>
          <CardContent className="flex flex-col gap-2">
            {subtypes.map((s, i) => (
              <div key={i} className="flex items-center gap-2">
                <input
                  type="number"
                  className={`${inputClass} w-20`}
                  value={s.code}
                  onChange={(e) => {
                    const next = [...subtypes]
                    next[i] = { ...next[i], code: Number(e.target.value) }
                    setSubtypes(next)
                  }}
                  placeholder="code"
                />
                <input
                  className={inputClass}
                  value={s.name}
                  onChange={(e) => {
                    const next = [...subtypes]
                    next[i] = { ...next[i], name: e.target.value }
                    setSubtypes(next)
                  }}
                  placeholder="name, e.g. overhead"
                />
                <Button variant="ghost" size="icon" className="size-8 shrink-0" onClick={() => setSubtypes(subtypes.filter((_, si) => si !== i))}>
                  <Trash2 className="size-4" />
                </Button>
              </div>
            ))}
            <Button
              variant="outline"
              size="sm"
              className="self-start"
              onClick={() => setSubtypes([...subtypes, { code: subtypes.length + 1, name: "" }])}
            >
              <Plus className="size-4" />
              Add subtype
            </Button>
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle className="text-base">Fields</CardTitle>
            <CardDescription>The attributes this class carries.</CardDescription>
          </CardHeader>
          <CardContent className="flex flex-col gap-3">
            {fields.map((f, i) => (
              <div key={i} className="flex flex-col gap-2 rounded-md border p-2">
                <div className="grid grid-cols-4 gap-2">
                  <input
                    className={inputClass}
                    value={f.name}
                    onChange={(e) => {
                      const next = [...fields]
                      next[i] = { ...next[i], name: e.target.value.toLowerCase().replace(/\s+/g, "_") }
                      setFields(next)
                    }}
                    placeholder="field_name"
                  />
                  <select
                    className={inputClass}
                    value={f.dataType}
                    onChange={(e) => {
                      const next = [...fields]
                      next[i] = { ...next[i], dataType: e.target.value as DraftField["dataType"] }
                      setFields(next)
                    }}
                  >
                    {DATA_TYPES.map((d) => (
                      <option key={d} value={d}>
                        {d}
                      </option>
                    ))}
                  </select>
                  <select
                    className={inputClass}
                    value={f.domainCode}
                    onChange={(e) => {
                      const next = [...fields]
                      next[i] = { ...next[i], domainCode: e.target.value }
                      setFields(next)
                    }}
                  >
                    <option value="">No domain</option>
                    {domainsData?.domains.map((d) => (
                      <option key={d.code} value={d.code}>
                        {d.name}
                      </option>
                    ))}
                  </select>
                  <select
                    className={inputClass}
                    value={f.groupCode}
                    onChange={(e) => {
                      const next = [...fields]
                      next[i] = { ...next[i], groupCode: e.target.value }
                      setFields(next)
                    }}
                  >
                    <option value="">No group</option>
                    {groupsData?.field_groups.map((g) => (
                      <option key={g.code} value={g.code}>
                        {g.name}
                      </option>
                    ))}
                  </select>
                </div>
                <div className="flex flex-wrap items-center gap-3 text-sm">
                  <label className="flex items-center gap-1.5">
                    <input
                      type="checkbox"
                      checked={!f.nullable}
                      onChange={(e) => {
                        const next = [...fields]
                        next[i] = { ...next[i], nullable: !e.target.checked }
                        setFields(next)
                      }}
                    />
                    Not nullable
                  </label>
                  {subtypes.length > 0 && (
                    <div className="flex flex-wrap items-center gap-2">
                      <span className="text-muted-foreground">Required for:</span>
                      {subtypes.map((s) => (
                        <label key={s.code} className="flex items-center gap-1">
                          <input
                            type="checkbox"
                            checked={f.requiredForSubtypes.includes(s.code)}
                            onChange={(e) => {
                              const next = [...fields]
                              next[i] = {
                                ...next[i],
                                requiredForSubtypes: e.target.checked
                                  ? [...next[i].requiredForSubtypes, s.code]
                                  : next[i].requiredForSubtypes.filter((c) => c !== s.code),
                              }
                              setFields(next)
                            }}
                          />
                          {s.name || s.code}
                        </label>
                      ))}
                    </div>
                  )}
                  <Button
                    variant="ghost"
                    size="sm"
                    className="text-destructive ml-auto"
                    onClick={() => setFields(fields.filter((_, fi) => fi !== i))}
                  >
                    <Trash2 className="size-4" />
                    Remove
                  </Button>
                </div>
              </div>
            ))}
            <Button
              variant="outline"
              size="sm"
              className="self-start"
              onClick={() =>
                setFields([
                  ...fields,
                  { name: "", dataType: "text", domainCode: "", groupCode: "", nullable: true, requiredForSubtypes: [] },
                ])
              }
            >
              <Plus className="size-4" />
              Add field
            </Button>
          </CardContent>
        </Card>

        {needsTerminals && (
          <Card>
            <CardHeader>
              <CardTitle className="text-base">Terminals</CardTitle>
              <CardDescription>Named connection points -- what network_rules and connectivity attach to.</CardDescription>
            </CardHeader>
            <CardContent className="flex flex-col gap-2">
              {terminals.map((t, i) => (
                <div key={i} className="flex items-center gap-2">
                  <input
                    className={`${inputClass} w-20`}
                    value={t.code}
                    onChange={(e) => {
                      const next = [...terminals]
                      next[i] = { ...next[i], code: e.target.value }
                      setTerminals(next)
                    }}
                    placeholder="T1"
                  />
                  <select
                    className={inputClass}
                    value={t.role}
                    onChange={(e) => {
                      const next = [...terminals]
                      next[i] = { ...next[i], role: e.target.value as DraftTerminal["role"] }
                      setTerminals(next)
                    }}
                  >
                    {TERMINAL_ROLES.map((r) => (
                      <option key={r} value={r}>
                        {r}
                      </option>
                    ))}
                  </select>
                  <label className="flex shrink-0 items-center gap-1.5 text-sm">
                    <input
                      type="checkbox"
                      checked={t.phaseCapable}
                      onChange={(e) => {
                        const next = [...terminals]
                        next[i] = { ...next[i], phaseCapable: e.target.checked }
                        setTerminals(next)
                      }}
                    />
                    Phase-capable
                  </label>
                  {subtypes.length > 0 && (
                    <select
                      className={inputClass}
                      value={t.subtypeCode}
                      onChange={(e) => {
                        const next = [...terminals]
                        next[i] = { ...next[i], subtypeCode: e.target.value === "" ? "" : Number(e.target.value) }
                        setTerminals(next)
                      }}
                    >
                      <option value="">Every subtype</option>
                      {subtypes.map((s) => (
                        <option key={s.code} value={s.code}>
                          {s.name || s.code}
                        </option>
                      ))}
                    </select>
                  )}
                  <Button variant="ghost" size="icon" className="size-8 shrink-0" onClick={() => setTerminals(terminals.filter((_, ti) => ti !== i))}>
                    <Trash2 className="size-4" />
                  </Button>
                </div>
              ))}
              <Button
                variant="outline"
                size="sm"
                className="self-start"
                onClick={() => setTerminals([...terminals, { code: "", role: "primary", phaseCapable: false, subtypeCode: "" }])}
              >
                <Plus className="size-4" />
                Add terminal
              </Button>
            </CardContent>
          </Card>
        )}

        <Card>
          <CardHeader className="flex flex-row items-center justify-between">
            <div>
              <CardTitle className="text-base">Default map style</CardTitle>
              <CardDescription>Optional -- how this class renders on the map until a finer-grained rule is authored.</CardDescription>
            </div>
            <label className="flex items-center gap-1.5 text-sm">
              <input type="checkbox" checked={symbologyEnabled} onChange={(e) => setSymbologyEnabled(e.target.checked)} />
              Set one
            </label>
          </CardHeader>
          {symbologyEnabled && (
            <CardContent className="flex flex-col gap-3">
              <div className="flex items-center gap-3">
                <div className="flex flex-col gap-1">
                  <label className={labelClass}>Color</label>
                  <input
                    type="color"
                    value={color}
                    onChange={(e) => setColor(e.target.value)}
                    className="h-8 w-12 shrink-0 rounded-md border"
                  />
                </div>
                {geometryType === "line" && (
                  <>
                    <div className="flex flex-col gap-1">
                      <label className={labelClass}>Width</label>
                      <input
                        type="number"
                        className={`${inputClass} w-20`}
                        value={width}
                        onChange={(e) => setWidth(Number(e.target.value))}
                        placeholder="width"
                      />
                    </div>
                    <div className="flex flex-col gap-1">
                      <label className={labelClass}>Dash</label>
                      <select className={inputClass} value={dash} onChange={(e) => setDash(e.target.value)}>
                        {LINE_DASH_STYLES.map((d) => (
                          <option key={d} value={d}>
                            {d}
                          </option>
                        ))}
                      </select>
                    </div>
                  </>
                )}
              </div>
              <div className="flex flex-col gap-1">
                <label className={labelClass}>Icon</label>
                <IconPicker value={iconRef} color={color} onChange={setIconRef} />
              </div>
            </CardContent>
          )}
        </Card>

        <div className="flex justify-end gap-2 pb-4">
          <Button variant="outline" onClick={() => navigate("/admin/feature-classes")} disabled={saving}>
            Cancel
          </Button>
          <Button onClick={handleSave} disabled={!canSave || saving}>
            {saving && <Loader2 className="size-4 animate-spin" />}
            Create feature class
          </Button>
        </div>
      </div>
    </div>
  )
}
