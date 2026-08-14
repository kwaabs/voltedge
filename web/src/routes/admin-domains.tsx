import { useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { Loader2, Plus, Tags, Trash2, X } from "lucide-react"

import { inputClass } from "@/components/map/field-utils"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { useAdminDomains } from "@/hooks/use-admin"
import { ApiError, apiFetch } from "@/lib/api"
import { useToastStore } from "@/stores/toast-store"

interface DraftValue {
  code: string
  label: string
}

/**
 * A domain is shared reference data -- fields across many feature
 * classes point at the same one (see docs/metadata-core.md) -- so it
 * gets its own page rather than being authored inline inside the
 * feature-class wizard. Coded domains only: a range domain's own bounds
 * aren't modeled by api/internal/admin yet (see that package's own doc
 * comment), so this form only ever builds the values list.
 */
export function AdminDomainsPage() {
  const { data, isPending, isError } = useAdminDomains()
  const queryClient = useQueryClient()

  const [open, setOpen] = useState(false)
  const [code, setCode] = useState("")
  const [name, setName] = useState("")
  const [values, setValues] = useState<DraftValue[]>([{ code: "", label: "" }])
  const [saving, setSaving] = useState(false)

  function reset() {
    setCode("")
    setName("")
    setValues([{ code: "", label: "" }])
  }

  const canSave =
    code.trim().length > 0 &&
    name.trim().length > 0 &&
    values.length > 0 &&
    values.every((v) => v.code.trim().length > 0 && v.label.trim().length > 0)

  async function handleSave() {
    setSaving(true)
    try {
      await apiFetch("/admin/domains", {
        method: "POST",
        body: JSON.stringify({
          code: code.trim(),
          name: name.trim(),
          domain_type: "coded",
          values: values.map((v, i) => ({ code: v.code.trim(), label: v.label.trim(), sort_order: i + 1 })),
        }),
      })
      queryClient.invalidateQueries({ queryKey: ["admin", "domains"] })
      setOpen(false)
      reset()
    } catch (err) {
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not create this domain.")
    } finally {
      setSaving(false)
    }
  }

  return (
    <div className="h-full overflow-y-auto p-4">
      <div className="mx-auto flex max-w-3xl flex-col gap-4">
        <div className="flex items-center justify-between">
          <div>
            <h1 className="text-lg font-semibold">Domains</h1>
            <p className="text-muted-foreground text-sm">
              Reusable coded-value vocabularies -- fields across any feature class can constrain their values to one
              of these.
            </p>
          </div>
          {!open && (
            <Button size="sm" onClick={() => setOpen(true)}>
              <Plus className="size-4" />
              New domain
            </Button>
          )}
        </div>

        {open && (
          <Card>
            <CardHeader className="flex flex-row items-start justify-between">
              <div>
                <CardTitle>New domain</CardTitle>
                <CardDescription>A stable code, a display name, and its legal values.</CardDescription>
              </div>
              <Button
                variant="ghost"
                size="icon"
                className="size-7"
                onClick={() => {
                  setOpen(false)
                  reset()
                }}
                disabled={saving}
              >
                <X className="size-4" />
              </Button>
            </CardHeader>
            <CardContent className="flex flex-col gap-3">
              <div className="grid grid-cols-2 gap-3">
                <div className="flex flex-col gap-1">
                  <label className="text-xs font-medium">
                    Code <span className="text-destructive">*</span>
                  </label>
                  <input
                    className={inputClass}
                    value={code}
                    onChange={(e) => setCode(e.target.value.toUpperCase())}
                    placeholder="e.g. LAMP_TYPE"
                  />
                </div>
                <div className="flex flex-col gap-1">
                  <label className="text-xs font-medium">
                    Name <span className="text-destructive">*</span>
                  </label>
                  <input
                    className={inputClass}
                    value={name}
                    onChange={(e) => setName(e.target.value)}
                    placeholder="e.g. Street light lamp type"
                  />
                </div>
              </div>

              <div className="flex flex-col gap-2">
                <label className="text-xs font-medium">
                  Values <span className="text-destructive">*</span>
                </label>
                {values.map((v, i) => (
                  <div key={i} className="flex items-center gap-2">
                    <input
                      className={inputClass}
                      value={v.code}
                      onChange={(e) => {
                        const next = [...values]
                        next[i] = { ...next[i], code: e.target.value }
                        setValues(next)
                      }}
                      placeholder="code, e.g. LED"
                    />
                    <input
                      className={inputClass}
                      value={v.label}
                      onChange={(e) => {
                        const next = [...values]
                        next[i] = { ...next[i], label: e.target.value }
                        setValues(next)
                      }}
                      placeholder="label, e.g. LED"
                    />
                    <Button
                      variant="ghost"
                      size="icon"
                      className="size-8 shrink-0"
                      onClick={() => setValues(values.filter((_, vi) => vi !== i))}
                      disabled={values.length === 1}
                    >
                      <Trash2 className="size-4" />
                    </Button>
                  </div>
                ))}
                <Button
                  variant="outline"
                  size="sm"
                  className="self-start"
                  onClick={() => setValues([...values, { code: "", label: "" }])}
                >
                  <Plus className="size-4" />
                  Add value
                </Button>
              </div>

              <div className="flex justify-end gap-2 pt-1">
                <Button
                  variant="outline"
                  size="sm"
                  onClick={() => {
                    setOpen(false)
                    reset()
                  }}
                  disabled={saving}
                >
                  Cancel
                </Button>
                <Button size="sm" onClick={handleSave} disabled={!canSave || saving}>
                  {saving && <Loader2 className="size-4 animate-spin" />}
                  Create
                </Button>
              </div>
            </CardContent>
          </Card>
        )}

        {isPending ? (
          <div className="text-muted-foreground flex items-center gap-2 text-sm">
            <Loader2 className="size-4 animate-spin" />
            Loading domains...
          </div>
        ) : isError || !data ? (
          <p className="text-destructive text-sm">Could not load domains.</p>
        ) : data.domains.length === 0 ? (
          <p className="text-muted-foreground text-sm">No domains yet.</p>
        ) : (
          <div className="flex flex-col gap-2">
            {data.domains.map((d) => (
              <Card key={d.code}>
                <CardHeader className="flex flex-row items-center gap-2 pb-2">
                  <Tags className="text-muted-foreground size-4" />
                  <div className="min-w-0 flex-1">
                    <CardTitle className="text-sm">{d.name}</CardTitle>
                    <CardDescription className="font-mono text-xs">{d.code}</CardDescription>
                  </div>
                  <span className="text-muted-foreground text-xs">{d.domain_type}</span>
                </CardHeader>
                {d.values && d.values.length > 0 && (
                  <CardContent className="flex flex-wrap gap-1 pt-0">
                    {d.values.map((v) => (
                      <span
                        key={v.code}
                        className="bg-muted text-muted-foreground rounded-md px-1.5 py-0.5 text-xs"
                        title={v.code}
                      >
                        {v.label}
                      </span>
                    ))}
                  </CardContent>
                )}
              </Card>
            ))}
          </div>
        )}
      </div>
    </div>
  )
}
