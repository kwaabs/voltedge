import { useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { KeyRound, Link as LinkIcon, Loader2, Pencil, Plus, Trash2 } from "lucide-react"

import { ConfirmDeleteDialog } from "@/components/admin/confirm-delete-dialog"
import { FormDialog } from "@/components/admin/form-dialog"
import { inputClass } from "@/components/map/field-utils"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { usePermission } from "@/hooks/use-auth"
import { type ExternalSystem, useExternalSystems } from "@/hooks/use-external-systems"
import { ApiError, apiFetch } from "@/lib/api"
import { useToastStore } from "@/stores/toast-store"

const labelClass = "text-xs font-medium"

interface FormState {
  name: string
  baseUrl: string
  authType: string
  authHeader: string
  authUsername: string
  credential: string
  responseFormat: string
  pathTemplate: string
}

const EMPTY_FORM: FormState = {
  name: "",
  baseUrl: "",
  authType: "none",
  authHeader: "",
  authUsername: "",
  credential: "",
  responseFormat: "json",
  pathTemplate: "",
}

function formFrom(sys: ExternalSystem): FormState {
  return {
    name: sys.name,
    baseUrl: sys.base_url,
    authType: sys.auth_type,
    authHeader: sys.auth_header ?? "",
    authUsername: sys.auth_username ?? "",
    credential: "",
    responseFormat: sys.response_format,
    pathTemplate: sys.path_template ?? "",
  }
}

/** The auth/response half of the form, shared by create and edit. On edit,
 * `existingCredential` drives the "leave blank to keep" hint so an admin
 * doesn't have to re-enter a secret just to change a name. */
function SystemFormFields({
  value,
  onChange,
  existingCredential,
}: {
  value: FormState
  onChange: (v: FormState) => void
  existingCredential?: boolean
}) {
  const needsHeader = value.authType === "api_key"
  const needsUsername = value.authType === "basic"
  const needsSecret = value.authType !== "none"
  const secretLabel = value.authType === "basic" ? "Password" : value.authType === "bearer" ? "Bearer token" : "API key"

  return (
    <div className="flex flex-col gap-3">
      <div className="flex flex-col gap-1">
        <label className={labelClass}>
          Base URL <span className="text-destructive">*</span>
        </label>
        <input
          className={inputClass}
          value={value.baseUrl}
          onChange={(e) => onChange({ ...value, baseUrl: e.target.value })}
          placeholder="https://cis.example.com/api"
        />
      </div>
      <div className="grid grid-cols-2 gap-3">
        <div className="flex flex-col gap-1">
          <label className={labelClass}>Authentication</label>
          <select
            className={inputClass}
            value={value.authType}
            onChange={(e) => onChange({ ...value, authType: e.target.value })}
          >
            <option value="none">None</option>
            <option value="api_key">API key (header)</option>
            <option value="basic">Basic (username + password)</option>
            <option value="bearer">Bearer token</option>
          </select>
        </div>
        <div className="flex flex-col gap-1">
          <label className={labelClass}>Response format</label>
          <select
            className={inputClass}
            value={value.responseFormat}
            onChange={(e) => onChange({ ...value, responseFormat: e.target.value })}
          >
            <option value="json">JSON</option>
            <option value="xml">XML (legacy)</option>
          </select>
        </div>
      </div>

      <div className="flex flex-col gap-1">
        <label className={labelClass}>Live-data path template</label>
        <input
          className={inputClass}
          value={value.pathTemplate}
          onChange={(e) => onChange({ ...value, pathTemplate: e.target.value })}
          placeholder="e.g. /meters/{id}/consumption"
        />
        <p className="text-muted-foreground text-xs">
          Optional. Appended to the base URL to fetch a feature's live data; <code>{"{id}"}</code> (and any{" "}
          <code>{"{field}"}</code>) are filled from the feature's reference for this system. Leave blank for a
          push-only or link-only system.
        </p>
      </div>

      {needsHeader && (
        <div className="flex flex-col gap-1">
          <label className={labelClass}>
            Header name <span className="text-destructive">*</span>
          </label>
          <input
            className={inputClass}
            value={value.authHeader}
            onChange={(e) => onChange({ ...value, authHeader: e.target.value })}
            placeholder="e.g. X-API-Key"
          />
        </div>
      )}
      {needsUsername && (
        <div className="flex flex-col gap-1">
          <label className={labelClass}>
            Username <span className="text-destructive">*</span>
          </label>
          <input
            className={inputClass}
            value={value.authUsername}
            onChange={(e) => onChange({ ...value, authUsername: e.target.value })}
          />
        </div>
      )}
      {needsSecret && (
        <div className="flex flex-col gap-1">
          <label className={labelClass}>
            {secretLabel}
            {!existingCredential && <span className="text-destructive"> *</span>}
          </label>
          <input
            type="password"
            autoComplete="new-password"
            className={inputClass}
            value={value.credential}
            onChange={(e) => onChange({ ...value, credential: e.target.value })}
            placeholder={existingCredential ? "Leave blank to keep the current secret" : ""}
          />
          <p className="text-muted-foreground text-xs">Stored encrypted; never shown again after saving.</p>
        </div>
      )}
    </div>
  )
}

/** Body shared by create and edit -- returns only the fields the server
 * accepts, omitting a blank credential on edit so it's left untouched. */
function toBody(form: FormState, { isEdit }: { isEdit: boolean }): Record<string, unknown> {
  const body: Record<string, unknown> = {
    name: form.name.trim(),
    base_url: form.baseUrl.trim(),
    auth_type: form.authType,
    auth_header: form.authType === "api_key" ? form.authHeader.trim() : "",
    auth_username: form.authType === "basic" ? form.authUsername.trim() : "",
    response_format: form.responseFormat,
    path_template: form.pathTemplate.trim(),
  }
  // Credential: always send on create; on edit only when the admin typed a
  // new one (blank => keep the stored secret).
  if (!isEdit || form.credential.trim() !== "") {
    body.credential = form.credential
  }
  return body
}

function EditSystemRow({ system, onDone }: { system: ExternalSystem; onDone: () => void }) {
  const [form, setForm] = useState<FormState>(formFrom(system))
  const [saving, setSaving] = useState(false)
  const queryClient = useQueryClient()

  const canSave = form.name.trim().length > 0 && form.baseUrl.trim().length > 0

  async function handleSave() {
    setSaving(true)
    try {
      await apiFetch(`/external-systems/${system.code}`, {
        method: "PATCH",
        body: JSON.stringify(toBody(form, { isEdit: true })),
      })
      queryClient.invalidateQueries({ queryKey: ["external-systems"] })
      onDone()
    } catch (err) {
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not update this system.")
    } finally {
      setSaving(false)
    }
  }

  return (
    <FormDialog
      open
      onOpenChange={(next) => next || onDone()}
      title={`Edit ${system.code}`}
      description="Update the connection and authentication for this external system."
      onSubmit={handleSave}
      submitLabel="Save"
      canSubmit={canSave}
      saving={saving}
      className="max-w-xl"
    >
      <div className="flex flex-col gap-1">
        <label className={labelClass}>Name</label>
        <input className={inputClass} value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} />
      </div>
      <SystemFormFields value={form} onChange={setForm} existingCredential={system.has_credential} />
    </FormDialog>
  )
}

/**
 * The external-systems catalog (Phase 32): systems a feature's kind='api'
 * links fetch live data from. Config like Materials/Domains, gated
 * metadata:read to view and metadata:write to change. Credentials are
 * encrypted server-side and never returned -- a card shows only whether one
 * is set.
 */
export function AdminExternalSystemsPage() {
  const { data, isPending, isError } = useExternalSystems()
  const queryClient = useQueryClient()
  const canWrite = usePermission()("metadata:write")

  const [open, setOpen] = useState(false)
  const [code, setCode] = useState("")
  const [form, setForm] = useState<FormState>(EMPTY_FORM)
  const [saving, setSaving] = useState(false)
  const [editingCode, setEditingCode] = useState<string | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<ExternalSystem | null>(null)

  function close() {
    setOpen(false)
    setCode("")
    setForm(EMPTY_FORM)
  }

  const canSave = code.trim().length > 0 && form.name.trim().length > 0 && form.baseUrl.trim().length > 0

  async function handleSave() {
    setSaving(true)
    try {
      await apiFetch("/external-systems", {
        method: "POST",
        body: JSON.stringify({ code: code.trim(), ...toBody(form, { isEdit: false }) }),
      })
      queryClient.invalidateQueries({ queryKey: ["external-systems"] })
      close()
    } catch (err) {
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not create this system.")
    } finally {
      setSaving(false)
    }
  }

  return (
    <div className="h-full overflow-y-auto p-4">
      <div className="flex w-full flex-col gap-4">
        <div className="flex items-center justify-between">
          <div>
            <h1 className="text-lg font-semibold">External systems</h1>
            <p className="text-muted-foreground text-sm">
              Systems a feature's live (API) links fetch data from — a CIS/billing API, a work-order system, a document
              service. Credentials are stored encrypted and never shown again.
            </p>
          </div>
          {canWrite && (
            <Button size="sm" onClick={() => setOpen(true)}>
              <Plus className="size-4" />
              New system
            </Button>
          )}
        </div>

        <FormDialog
          open={open}
          onOpenChange={(next) => (next ? setOpen(true) : close())}
          title="New external system"
          description="A stable code, a base URL, and how requests to it authenticate."
          onSubmit={handleSave}
          submitLabel="Create"
          canSubmit={canSave}
          saving={saving}
          className="max-w-xl"
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
                placeholder="e.g. CIS"
              />
            </div>
            <div className="flex flex-col gap-1">
              <label className={labelClass}>
                Name <span className="text-destructive">*</span>
              </label>
              <input
                className={inputClass}
                value={form.name}
                onChange={(e) => setForm({ ...form, name: e.target.value })}
                placeholder="e.g. Billing system"
              />
            </div>
          </div>
          <SystemFormFields value={form} onChange={setForm} />
        </FormDialog>

        {isPending ? (
          <div className="text-muted-foreground flex items-center gap-2 text-sm">
            <Loader2 className="size-4 animate-spin" />
            Loading external systems...
          </div>
        ) : isError || !data ? (
          <p className="text-destructive text-sm">Could not load external systems.</p>
        ) : data.external_systems.length === 0 ? (
          <p className="text-muted-foreground text-sm">No external systems yet.</p>
        ) : (
          <div className="flex flex-col gap-2">
            {data.external_systems.map((sys) =>
              editingCode === sys.code ? (
                <EditSystemRow key={sys.code} system={sys} onDone={() => setEditingCode(null)} />
              ) : (
                <Card key={sys.code}>
                  <CardHeader className="flex flex-row items-center gap-2 pb-2">
                    <LinkIcon className="text-muted-foreground size-4" />
                    <div className="min-w-0 flex-1">
                      <CardTitle className="text-sm">{sys.name}</CardTitle>
                      <CardDescription className="font-mono text-xs">
                        {sys.code} · {sys.auth_type} · {sys.response_format.toUpperCase()}
                      </CardDescription>
                    </div>
                    {canWrite && (
                      <>
                        <Button
                          variant="ghost"
                          size="icon"
                          className="size-7"
                          title="Edit"
                          onClick={() => setEditingCode(sys.code)}
                        >
                          <Pencil className="size-4" />
                        </Button>
                        <Button
                          variant="ghost"
                          size="icon"
                          className="text-destructive size-7"
                          onClick={() => setDeleteTarget(sys)}
                        >
                          <Trash2 className="size-4" />
                        </Button>
                      </>
                    )}
                  </CardHeader>
                  <CardContent className="flex flex-wrap items-center gap-2 pt-0">
                    <span className="text-muted-foreground truncate font-mono text-xs">{sys.base_url}</span>
                    {sys.auth_type !== "none" && (
                      <span className="bg-muted text-muted-foreground flex items-center gap-1 rounded-md px-1.5 py-0.5 text-xs">
                        <KeyRound className="size-3" />
                        {sys.has_credential ? "credential set" : "no credential"}
                      </span>
                    )}
                  </CardContent>
                </Card>
              ),
            )}
          </div>
        )}
      </div>

      <ConfirmDeleteDialog
        open={deleteTarget !== null}
        title={`Delete ${deleteTarget?.code ?? "this system"}?`}
        description="Removes the system and its stored credential. Any live (API) links pointing at it will stop resolving. This can't be undone."
        onOpenChange={(open) => !open && setDeleteTarget(null)}
        onConfirm={async () => {
          if (!deleteTarget) return
          await apiFetch(`/external-systems/${deleteTarget.code}`, { method: "DELETE" })
          queryClient.invalidateQueries({ queryKey: ["external-systems"] })
        }}
      />
    </div>
  )
}
