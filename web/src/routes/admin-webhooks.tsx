import { useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
import {
  Check,
  ChevronDown,
  ChevronRight,
  Copy,
  FlaskConical,
  KeyRound,
  Loader2,
  Pencil,
  Plus,
  RefreshCw,
  Trash2,
  Webhook,
} from "lucide-react"

import { ConfirmDeleteDialog } from "@/components/admin/confirm-delete-dialog"
import { FormDialog } from "@/components/admin/form-dialog"
import { inputClass } from "@/components/map/field-utils"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { usePermission } from "@/hooks/use-auth"
import { useFeatureClasses } from "@/hooks/use-feature-classes"
import { useAdminUsers } from "@/hooks/use-users"
import {
  type WebhookAuthMode,
  type WebhookDelivery,
  type WebhookDirection,
  type WebhookEndpoint,
  type WebhookTarget,
  useWebhookDeliveries,
  useWebhookEndpoints,
} from "@/hooks/use-webhooks"
import { ApiError, apiFetch } from "@/lib/api"
import { useToastStore } from "@/stores/toast-store"

const labelClass = "text-xs font-medium"

interface FormState {
  name: string
  active: boolean
  direction: WebhookDirection
  target: WebhookTarget
  authMode: WebhookAuthMode
  signingSecret: string
  serviceUserId: string
  featureClass: string
}

const EMPTY_FORM: FormState = {
  name: "",
  active: true,
  direction: "push",
  target: "feature",
  authMode: "bearer",
  signingSecret: "",
  serviceUserId: "",
  featureClass: "",
}

function formFrom(ep: WebhookEndpoint): FormState {
  return {
    name: ep.name,
    active: ep.active,
    direction: ep.direction,
    target: ep.target,
    authMode: ep.auth_mode,
    signingSecret: "",
    serviceUserId: ep.service_user_id,
    featureClass: ep.config?.feature_class ?? "",
  }
}

/** True when this endpoint's auth mode verifies an HMAC signature (so a signing
 * secret is required). Bearer-only endpoints authenticate by token and need none. */
function needsSecret(mode: WebhookAuthMode): boolean {
  return mode === "hmac" || mode === "both"
}

/** feature and reading endpoints both write feature rows (a reading is modelled
 * as a feature), so both offer a default feature_class fallback. work/fault
 * endpoints drive their own engines and have no feature class. */
function usesFeatureClass(target: WebhookTarget): boolean {
  return target === "feature" || target === "reading"
}

/** Body shared by create and edit. On edit the signing secret is omitted when
 * left blank (keep the stored one); config carries the feature_class fallback
 * for single-class feature endpoints. */
function toBody(form: FormState, { isEdit }: { isEdit: boolean }): Record<string, unknown> {
  const body: Record<string, unknown> = {
    name: form.name.trim(),
    active: form.active,
    direction: form.direction,
    target: form.target,
    auth_mode: form.authMode,
    service_user_id: form.serviceUserId.trim(),
    config: usesFeatureClass(form.target) && form.featureClass ? { feature_class: form.featureClass } : {},
  }
  // Secret: on create always send (empty is fine for bearer); on edit only send
  // when the admin typed a new one, so a blank leaves the stored secret intact.
  if (needsSecret(form.authMode) && (!isEdit || form.signingSecret.trim() !== "")) {
    body.signing_secret = form.signingSecret
  }
  return body
}

/** The connection/auth half of the form, shared by create and edit. */
function EndpointFormFields({
  value,
  onChange,
  hasSecret,
}: {
  value: FormState
  onChange: (v: FormState) => void
  hasSecret?: boolean
}) {
  const featureClasses = useFeatureClasses()
  const users = useAdminUsers()
  const activeUsers = (users.data?.users ?? []).filter((u) => u.status === "active")

  return (
    <div className="flex flex-col gap-3">
      <div className="grid grid-cols-2 gap-3">
        <div className="flex flex-col gap-1">
          <label className={labelClass}>Direction</label>
          <select
            className={inputClass}
            value={value.direction}
            onChange={(e) => onChange({ ...value, direction: e.target.value as WebhookDirection })}
          >
            <option value="push">Push (external system sends to us)</option>
            <option value="pull">Pull (we fetch — coming soon)</option>
          </select>
        </div>
        <div className="flex flex-col gap-1">
          <label className={labelClass}>Target</label>
          <select
            className={inputClass}
            value={value.target}
            onChange={(e) => onChange({ ...value, target: e.target.value as WebhookTarget })}
          >
            <option value="feature">Feature</option>
            <option value="reading">Reading (recorded as a feature)</option>
            <option value="work">Work order</option>
            <option value="fault">Fault event</option>
          </select>
        </div>
      </div>

      {usesFeatureClass(value.target) && (
        <div className="flex flex-col gap-1">
          <label className={labelClass}>Default feature class</label>
          <select
            className={inputClass}
            value={value.featureClass}
            onChange={(e) => onChange({ ...value, featureClass: e.target.value })}
          >
            <option value="">— none (each change must name its feature_class) —</option>
            {featureClasses.data?.feature_classes.map((c) => (
              <option key={c.code} value={c.code}>
                {c.name} ({c.code})
              </option>
            ))}
          </select>
          <p className="text-muted-foreground text-xs">
            Used when an incoming change omits <span className="font-mono">feature_class</span>. Leave blank for a
            multi-class integration.
          </p>
        </div>
      )}

      <div className="flex flex-col gap-1">
        <label className={labelClass}>
          Service user <span className="text-destructive">*</span>
        </label>
        {users.isError ? (
          <>
            <input
              className={inputClass}
              value={value.serviceUserId}
              onChange={(e) => onChange({ ...value, serviceUserId: e.target.value })}
              placeholder="user UUID"
            />
            <p className="text-muted-foreground text-xs">
              Ingested writes run as this user, inheriting their permissions and territory scope.
            </p>
          </>
        ) : (
          <>
            <select
              className={inputClass}
              value={value.serviceUserId}
              onChange={(e) => onChange({ ...value, serviceUserId: e.target.value })}
            >
              <option value="">— select a user —</option>
              {activeUsers.map((u) => (
                <option key={u.id} value={u.id}>
                  {u.display_name} ({u.email})
                </option>
              ))}
            </select>
            <p className="text-muted-foreground text-xs">
              Ingested writes run as this user, inheriting their permissions and territory scope.
            </p>
          </>
        )}
      </div>

      <div className="flex flex-col gap-1">
        <label className={labelClass}>Authentication</label>
        <select
          className={inputClass}
          value={value.authMode}
          onChange={(e) => onChange({ ...value, authMode: e.target.value as WebhookAuthMode })}
        >
          <option value="bearer">Bearer token (an API token of the service user)</option>
          <option value="hmac">HMAC signature (shared signing secret)</option>
          <option value="both">Both (accept either)</option>
        </select>
      </div>

      {needsSecret(value.authMode) && (
        <div className="flex flex-col gap-1">
          <label className={labelClass}>
            Signing secret
            {!hasSecret && <span className="text-destructive"> *</span>}
          </label>
          <input
            type="password"
            autoComplete="new-password"
            className={inputClass}
            value={value.signingSecret}
            onChange={(e) => onChange({ ...value, signingSecret: e.target.value })}
            placeholder={hasSecret ? "Leave blank to keep the current secret" : ""}
          />
          <p className="text-muted-foreground text-xs">
            Stored encrypted; never shown again. Senders sign{" "}
            <span className="font-mono">HMAC-SHA256(secret, "&lt;timestamp&gt;.&lt;body&gt;")</span>. Requires the
            server's encryption key to be configured.
          </p>
        </div>
      )}

      <label className="flex items-center gap-2 text-xs font-medium">
        <input
          type="checkbox"
          checked={value.active}
          onChange={(e) => onChange({ ...value, active: e.target.checked })}
        />
        Active (a paused endpoint refuses and drops incoming pushes)
      </label>
    </div>
  )
}

function EditEndpointRow({ endpoint, onDone }: { endpoint: WebhookEndpoint; onDone: () => void }) {
  const [form, setForm] = useState<FormState>(formFrom(endpoint))
  const [saving, setSaving] = useState(false)
  const queryClient = useQueryClient()

  const canSave =
    form.name.trim().length > 0 &&
    form.serviceUserId.trim().length > 0 &&
    (!needsSecret(form.authMode) || endpoint.has_signing_secret || form.signingSecret.trim().length > 0)

  async function handleSave() {
    setSaving(true)
    try {
      await apiFetch(`/webhook-endpoints/${endpoint.id}`, {
        method: "PATCH",
        body: JSON.stringify(toBody(form, { isEdit: true })),
      })
      queryClient.invalidateQueries({ queryKey: ["webhook-endpoints"] })
      onDone()
    } catch (err) {
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not update this endpoint.")
    } finally {
      setSaving(false)
    }
  }

  return (
    <FormDialog
      open
      onOpenChange={(next) => next || onDone()}
      title={`Edit ${endpoint.name}`}
      description="Update how this endpoint authenticates and where ingested changes land."
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
      <EndpointFormFields value={form} onChange={setForm} hasSecret={endpoint.has_signing_secret} />
    </FormDialog>
  )
}

const STATUS_STYLES: Record<string, string> = {
  queued: "bg-muted text-muted-foreground",
  running: "bg-blue-500/15 text-blue-600 dark:text-blue-400",
  succeeded: "bg-emerald-500/15 text-emerald-600 dark:text-emerald-400",
  failed: "bg-amber-500/15 text-amber-600 dark:text-amber-400",
  dead: "bg-destructive/15 text-destructive",
}

function StatusBadge({ status }: { status: string }) {
  return (
    <span className={`rounded-md px-1.5 py-0.5 font-mono text-xs ${STATUS_STYLES[status] ?? "bg-muted"}`}>
      {status}
    </span>
  )
}

/** One delivery, its payload/result/error collapsed until expanded. */
function DeliveryRow({ endpointId, delivery }: { endpointId: string; delivery: WebhookDelivery }) {
  const [open, setOpen] = useState(false)
  const [replaying, setReplaying] = useState(false)
  const queryClient = useQueryClient()

  async function handleReplay() {
    setReplaying(true)
    try {
      await apiFetch(`/webhook-endpoints/${endpointId}/deliveries/${delivery.id}/replay`, { method: "POST" })
      queryClient.invalidateQueries({ queryKey: ["webhook-endpoints", endpointId, "deliveries"] })
      useToastStore.getState().pushSuccess("Replay queued — watch the log for the new delivery.")
    } catch (err) {
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not replay this delivery.")
    } finally {
      setReplaying(false)
    }
  }

  return (
    <div className="border-border/60 rounded-md border">
      <div className="flex items-center gap-2 px-2 py-1.5">
        <button
          type="button"
          className="hover:text-foreground text-muted-foreground flex min-w-0 flex-1 items-center gap-2"
          onClick={() => setOpen((v) => !v)}
        >
          {open ? <ChevronDown className="size-3.5" /> : <ChevronRight className="size-3.5" />}
          <StatusBadge status={delivery.status} />
          <span className="text-muted-foreground font-mono text-xs">{delivery.source}</span>
          <span className="truncate text-xs">{new Date(delivery.created_at).toLocaleString()}</span>
          {delivery.attempts > 0 && (
            <span className="text-muted-foreground text-xs">· {delivery.attempts} attempt(s)</span>
          )}
        </button>
        <Button
          variant="ghost"
          size="icon"
          className="size-7"
          title="Replay this delivery"
          onClick={handleReplay}
          disabled={replaying}
        >
          {replaying ? <Loader2 className="size-4 animate-spin" /> : <RefreshCw className="size-4" />}
        </Button>
      </div>
      {open && (
        <div className="border-border/60 flex flex-col gap-2 border-t px-2 py-2">
          {delivery.error && <p className="text-destructive text-xs">{delivery.error}</p>}
          {delivery.result !== undefined && delivery.result !== null && (
            <div>
              <p className={labelClass}>Result</p>
              <pre className="bg-muted/50 overflow-x-auto rounded p-2 text-xs">
                {JSON.stringify(delivery.result, null, 2)}
              </pre>
            </div>
          )}
          <div>
            <p className={labelClass}>Payload</p>
            <pre className="bg-muted/50 overflow-x-auto rounded p-2 text-xs">
              {JSON.stringify(delivery.payload, null, 2)}
            </pre>
          </div>
        </div>
      )}
    </div>
  )
}

/** The delivery log for one endpoint, polled while open. */
function DeliveriesPanel({ endpointId }: { endpointId: string }) {
  const { data, isPending, isError } = useWebhookDeliveries(endpointId)

  if (isPending) {
    return (
      <div className="text-muted-foreground flex items-center gap-2 py-2 text-xs">
        <Loader2 className="size-3.5 animate-spin" />
        Loading deliveries…
      </div>
    )
  }
  if (isError || !data) {
    return <p className="text-destructive py-2 text-xs">Could not load deliveries.</p>
  }
  if (data.deliveries.length === 0) {
    return <p className="text-muted-foreground py-2 text-xs">No deliveries yet.</p>
  }
  return (
    <div className="flex flex-col gap-1.5 py-2">
      {data.deliveries.map((d) => (
        <DeliveryRow key={d.id} endpointId={endpointId} delivery={d} />
      ))}
    </div>
  )
}

const TEST_TEMPLATE = `{
  "changes": [
    {
      "op": "upsert",
      "external_id": "EXT-001",
      "subtype": 1,
      "attributes": {}
    }
  ]
}`

/** The paste-and-apply tester: POST a native payload, applied inline as the
 * service user, so an admin can validate an integration against the live model. */
function TesterDialog({ endpoint, onClose }: { endpoint: WebhookEndpoint; onClose: () => void }) {
  const [payload, setPayload] = useState(TEST_TEMPLATE)
  const [running, setRunning] = useState(false)
  const [result, setResult] = useState<unknown>(null)
  const [error, setError] = useState<string | null>(null)
  const queryClient = useQueryClient()

  async function handleRun() {
    setRunning(true)
    setResult(null)
    setError(null)
    try {
      const body = JSON.parse(payload)
      const res = await apiFetch<{ delivery_id: string; result: unknown }>(
        `/webhook-endpoints/${endpoint.id}/test`,
        { method: "POST", body: JSON.stringify(body) },
      )
      setResult(res.result)
      queryClient.invalidateQueries({ queryKey: ["webhook-endpoints", endpoint.id, "deliveries"] })
    } catch (err) {
      if (err instanceof SyntaxError) {
        setError("Payload is not valid JSON.")
      } else {
        setError(err instanceof ApiError ? err.message : "The tester call failed.")
      }
    } finally {
      setRunning(false)
    }
  }

  return (
    <FormDialog
      open
      onOpenChange={(next) => next || onClose()}
      title={`Test ${endpoint.name}`}
      description="Apply a native payload inline (as the service user) without sending a real push. Failures dead-letter and won't be retried."
      onSubmit={handleRun}
      submitLabel={running ? "Running…" : "Run"}
      canSubmit={!running && payload.trim().length > 0}
      saving={running}
      className="max-w-2xl"
    >
      <div className="flex flex-col gap-1">
        <label className={labelClass}>Payload (VoltEdge native JSON)</label>
        <textarea
          className={`${inputClass} h-56 font-mono text-xs`}
          value={payload}
          onChange={(e) => setPayload(e.target.value)}
          spellCheck={false}
        />
      </div>
      {error && <p className="text-destructive text-xs">{error}</p>}
      {result !== null && (
        <div className="flex flex-col gap-1">
          <label className="flex items-center gap-1 text-xs font-medium text-emerald-600 dark:text-emerald-400">
            <Check className="size-3.5" /> Applied
          </label>
          <pre className="bg-muted/50 overflow-x-auto rounded p-2 text-xs">{JSON.stringify(result, null, 2)}</pre>
        </div>
      )}
    </FormDialog>
  )
}

function EndpointCard({
  endpoint,
  canWrite,
  onEdit,
  onDelete,
}: {
  endpoint: WebhookEndpoint
  canWrite: boolean
  onEdit: () => void
  onDelete: () => void
}) {
  const [showLog, setShowLog] = useState(false)
  const [testing, setTesting] = useState(false)
  const [copied, setCopied] = useState(false)

  async function copyUrl() {
    const url = `${window.location.origin}${endpoint.receive_url}`
    try {
      await navigator.clipboard.writeText(url)
      setCopied(true)
      setTimeout(() => setCopied(false), 1500)
    } catch {
      useToastStore.getState().pushError("Could not copy to the clipboard.")
    }
  }

  return (
    <Card>
      <CardHeader className="flex flex-row items-center gap-2 pb-2">
        <Webhook className="text-muted-foreground size-4" />
        <div className="min-w-0 flex-1">
          <CardTitle className="flex items-center gap-2 text-sm">
            {endpoint.name}
            {!endpoint.active && (
              <span className="bg-muted text-muted-foreground rounded px-1.5 py-0.5 text-xs">paused</span>
            )}
          </CardTitle>
          <CardDescription className="font-mono text-xs">
            {endpoint.direction} · {endpoint.target} · {endpoint.auth_mode}
          </CardDescription>
        </div>
        {canWrite && (
          <>
            <Button
              variant="ghost"
              size="icon"
              className="size-7"
              title="Test (paste and apply)"
              onClick={() => setTesting(true)}
            >
              <FlaskConical className="size-4" />
            </Button>
            <Button variant="ghost" size="icon" className="size-7" title="Edit" onClick={onEdit}>
              <Pencil className="size-4" />
            </Button>
            <Button variant="ghost" size="icon" className="text-destructive size-7" title="Delete" onClick={onDelete}>
              <Trash2 className="size-4" />
            </Button>
          </>
        )}
      </CardHeader>
      <CardContent className="flex flex-col gap-2 pt-0">
        {endpoint.direction === "push" && (
          <div className="flex items-center gap-2">
            <span className="text-muted-foreground truncate font-mono text-xs">{endpoint.receive_url}</span>
            <Button
              variant="ghost"
              size="icon"
              className="size-6"
              title="Copy full receive URL"
              onClick={copyUrl}
            >
              {copied ? <Check className="size-3.5 text-emerald-500" /> : <Copy className="size-3.5" />}
            </Button>
          </div>
        )}
        <div className="flex flex-wrap items-center gap-2">
          {needsSecret(endpoint.auth_mode) && (
            <span className="bg-muted text-muted-foreground flex items-center gap-1 rounded-md px-1.5 py-0.5 text-xs">
              <KeyRound className="size-3" />
              {endpoint.has_signing_secret ? "signing secret set" : "no signing secret"}
            </span>
          )}
          {endpoint.config?.feature_class && (
            <span className="bg-muted text-muted-foreground rounded-md px-1.5 py-0.5 font-mono text-xs">
              {endpoint.config.feature_class}
            </span>
          )}
        </div>
        <button
          type="button"
          className="hover:text-foreground text-muted-foreground flex items-center gap-1 self-start text-xs"
          onClick={() => setShowLog((v) => !v)}
        >
          {showLog ? <ChevronDown className="size-3.5" /> : <ChevronRight className="size-3.5" />}
          Delivery log
        </button>
        {showLog && <DeliveriesPanel endpointId={endpoint.id} />}
      </CardContent>

      {testing && <TesterDialog endpoint={endpoint} onClose={() => setTesting(false)} />}
    </Card>
  )
}

/**
 * Inbound webhooks (migration 0124): endpoints external systems push feature
 * (later reading/work/fault) changes to. Each delivery is queued and applied
 * through the normal crud path as the endpoint's service user. Gated
 * metadata:read to view, metadata:write to change — the same admin capability
 * as external systems.
 */
export function AdminWebhooksPage() {
  const { data, isPending, isError } = useWebhookEndpoints()
  const queryClient = useQueryClient()
  const canWrite = usePermission()("metadata:write")

  const [open, setOpen] = useState(false)
  const [form, setForm] = useState<FormState>(EMPTY_FORM)
  const [saving, setSaving] = useState(false)
  const [editingId, setEditingId] = useState<string | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<WebhookEndpoint | null>(null)

  function close() {
    setOpen(false)
    setForm(EMPTY_FORM)
  }

  const canSave =
    form.name.trim().length > 0 &&
    form.serviceUserId.trim().length > 0 &&
    (!needsSecret(form.authMode) || form.signingSecret.trim().length > 0)

  async function handleSave() {
    setSaving(true)
    try {
      await apiFetch("/webhook-endpoints", {
        method: "POST",
        body: JSON.stringify(toBody(form, { isEdit: false })),
      })
      queryClient.invalidateQueries({ queryKey: ["webhook-endpoints"] })
      close()
    } catch (err) {
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not create this endpoint.")
    } finally {
      setSaving(false)
    }
  }

  return (
    <div className="h-full overflow-y-auto p-4">
      <div className="flex w-full flex-col gap-4">
        <div className="flex items-center justify-between">
          <div>
            <h1 className="text-lg font-semibold">Webhooks</h1>
            <p className="text-muted-foreground text-sm">
              Inbound endpoints external systems push changes to. Each delivery is queued and applied as the endpoint's
              service user — inheriting that user's permissions, territory scope, validation, and history.
            </p>
          </div>
          {canWrite && (
            <Button size="sm" onClick={() => setOpen(true)}>
              <Plus className="size-4" />
              New endpoint
            </Button>
          )}
        </div>

        <FormDialog
          open={open}
          onOpenChange={(next) => (next ? setOpen(true) : close())}
          title="New webhook endpoint"
          description="A name, where changes land, who they run as, and how senders authenticate."
          onSubmit={handleSave}
          submitLabel="Create"
          canSubmit={canSave}
          saving={saving}
          className="max-w-xl"
        >
          <div className="flex flex-col gap-1">
            <label className={labelClass}>
              Name <span className="text-destructive">*</span>
            </label>
            <input
              className={inputClass}
              value={form.name}
              onChange={(e) => setForm({ ...form, name: e.target.value })}
              placeholder="e.g. SCADA feature sync"
            />
          </div>
          <EndpointFormFields value={form} onChange={setForm} />
        </FormDialog>

        {isPending ? (
          <div className="text-muted-foreground flex items-center gap-2 text-sm">
            <Loader2 className="size-4 animate-spin" />
            Loading webhooks…
          </div>
        ) : isError || !data ? (
          <p className="text-destructive text-sm">Could not load webhooks.</p>
        ) : data.endpoints.length === 0 ? (
          <p className="text-muted-foreground text-sm">No webhook endpoints yet.</p>
        ) : (
          <div className="flex flex-col gap-2">
            {data.endpoints.map((ep) =>
              editingId === ep.id ? (
                <EditEndpointRow key={ep.id} endpoint={ep} onDone={() => setEditingId(null)} />
              ) : (
                <EndpointCard
                  key={ep.id}
                  endpoint={ep}
                  canWrite={canWrite}
                  onEdit={() => setEditingId(ep.id)}
                  onDelete={() => setDeleteTarget(ep)}
                />
              ),
            )}
          </div>
        )}
      </div>

      <ConfirmDeleteDialog
        open={deleteTarget !== null}
        title={`Delete ${deleteTarget?.name ?? "this endpoint"}?`}
        description="Removes the endpoint, its stored secret, and its delivery log. Senders pointing at its URL will start getting 404s. This can't be undone."
        onOpenChange={(open) => !open && setDeleteTarget(null)}
        onConfirm={async () => {
          if (!deleteTarget) return
          await apiFetch(`/webhook-endpoints/${deleteTarget.id}`, { method: "DELETE" })
          queryClient.invalidateQueries({ queryKey: ["webhook-endpoints"] })
        }}
      />
    </div>
  )
}
