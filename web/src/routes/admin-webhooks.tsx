import { useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
import {
  Check,
  ChevronDown,
  ChevronRight,
  Copy,
  DownloadCloud,
  FlaskConical,
  KeyRound,
  Loader2,
  Pencil,
  Plus,
  RefreshCw,
  Send,
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
  type OutboundDelivery,
  type WebhookAuthMode,
  type WebhookDelivery,
  type WebhookDirection,
  type WebhookEndpoint,
  type WebhookPullAuthType,
  type WebhookTarget,
  useOutboundDeliveries,
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
  pullUrl: string
  pullAuthType: WebhookPullAuthType
  pullAuthHeader: string
  pullAuthQueryParam: string
  pullAuthUsername: string
  /** Outbound subscription: where we POST events, which event types it hears, and
   * optional class / territory scoping + an envelope mapping (one item per line
   * for the list fields). */
  targetUrl: string
  eventTypes: string
  classFilter: string
  territoryId: string
  outboundMappingText: string
  mappingText: string
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
  pullUrl: "",
  pullAuthType: "none",
  pullAuthHeader: "",
  pullAuthQueryParam: "",
  pullAuthUsername: "",
  targetUrl: "",
  eventTypes: "",
  classFilter: "",
  territoryId: "",
  outboundMappingText: "",
  mappingText: "",
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
    pullUrl: ep.config?.pull_url ?? "",
    pullAuthType: ep.config?.pull_auth?.type ?? "none",
    pullAuthHeader: ep.config?.pull_auth?.header ?? "",
    pullAuthQueryParam: ep.config?.pull_auth?.query_param ?? "",
    pullAuthUsername: ep.config?.pull_auth?.username ?? "",
    targetUrl: ep.config?.target_url ?? "",
    eventTypes: (ep.config?.event_types ?? []).join("\n"),
    classFilter: (ep.config?.class_filter ?? []).join("\n"),
    territoryId: ep.config?.territory_id ?? "",
    outboundMappingText: ep.config?.mapping ? JSON.stringify(ep.config.mapping, null, 2) : "",
    mappingText: ep.mapping ? JSON.stringify(ep.mapping, null, 2) : "",
  }
}

/** A blank mapping is fine (native payloads); a non-blank one must be valid JSON.
 * Returns an error string to show under the field, or null when it's acceptable. */
function mappingParseError(text: string): string | null {
  const t = text.trim()
  if (!t) return null
  try {
    JSON.parse(t)
    return null
  } catch {
    return "Field mapping must be valid JSON (or leave it blank for native payloads)."
  }
}

/** True when this endpoint's auth mode verifies an HMAC signature (so a signing
 * secret is required). Bearer-only endpoints authenticate by token and need none. */
function needsSecret(mode: WebhookAuthMode): boolean {
  return mode === "hmac" || mode === "both"
}

/** A pull endpoint needs a stored credential whenever its outbound auth is not
 * "none" (api_key / basic / bearer all carry a secret). */
function pullAuthNeedsSecret(t: WebhookPullAuthType): boolean {
  return t !== "none"
}

/** Whether THIS form needs a stored secret at all: an HMAC push, an authenticated
 * pull (whose credential rides the same sealed column), or an outbound
 * subscription (which always signs each delivery with it). */
function formNeedsSecret(form: FormState): boolean {
  if (form.direction === "pull") return pullAuthNeedsSecret(form.pullAuthType)
  if (form.direction === "outbound") return true
  return needsSecret(form.authMode)
}

/** The event types an outbound subscription hears, parsed from the textarea (one
 * per line, commas also accepted; blanks dropped). */
function parseEventTypes(text: string): string[] {
  return text
    .split(/[\n,]/)
    .map((s) => s.trim())
    .filter(Boolean)
}

/** feature and reading endpoints both write feature rows (a reading is modelled
 * as a feature), so both offer a default feature_class fallback. work/fault
 * endpoints drive their own engines and have no feature class. */
function usesFeatureClass(target: WebhookTarget): boolean {
  return target === "feature" || target === "reading"
}

/** Body shared by create and edit. On edit the signing secret is omitted when
 * left blank (keep the stored one); config carries the feature_class fallback for
 * feature/reading endpoints and, for a pull endpoint, the pull_url + pull_auth. */
function toBody(form: FormState, { isEdit }: { isEdit: boolean }): Record<string, unknown> {
  const config: Record<string, unknown> = {}
  if (usesFeatureClass(form.target) && form.featureClass) {
    config.feature_class = form.featureClass
  }
  if (form.direction === "pull") {
    config.pull_url = form.pullUrl.trim()
    const auth: Record<string, unknown> = { type: form.pullAuthType }
    if (form.pullAuthType === "api_key") {
      auth.header = form.pullAuthHeader.trim()
      auth.query_param = form.pullAuthQueryParam.trim()
    }
    if (form.pullAuthType === "basic") {
      auth.username = form.pullAuthUsername.trim()
    }
    config.pull_auth = auth
  }
  if (form.direction === "outbound") {
    config.target_url = form.targetUrl.trim()
    const types = parseEventTypes(form.eventTypes)
    if (types.length > 0) config.event_types = types
    const classes = parseEventTypes(form.classFilter)
    if (classes.length > 0) config.class_filter = classes
    if (form.territoryId.trim()) config.territory_id = form.territoryId.trim()
    // The envelope mapping lives in config.mapping (not the endpoint.mapping
    // column, which is the inbound spec). Callers gate on its JSON validity first.
    if (form.outboundMappingText.trim()) config.mapping = JSON.parse(form.outboundMappingText)
  }

  const body: Record<string, unknown> = {
    name: form.name.trim(),
    active: form.active,
    direction: form.direction,
    target: form.target,
    auth_mode: form.authMode,
    service_user_id: form.serviceUserId.trim(),
    config,
    // Mapping: send the parsed spec, or explicit null to clear a stored one. On
    // create null is a no-op; on edit it removes any existing mapping. (Callers
    // gate on mappingParseError first, so JSON.parse is safe here.)
    mapping: form.mappingText.trim() ? JSON.parse(form.mappingText) : null,
  }
  // Secret: on create always send when the shape wants one (an HMAC push, or an
  // authenticated pull); on edit only send when the admin typed a new one, so a
  // blank leaves the stored secret intact.
  if (formNeedsSecret(form) && (!isEdit || form.signingSecret.trim() !== "")) {
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
            <option value="pull">Pull (we fetch on demand)</option>
            <option value="outbound">Outbound (we POST our events to them)</option>
          </select>
        </div>
        {value.direction !== "outbound" && (
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
        )}
      </div>

      {value.direction !== "outbound" && usesFeatureClass(value.target) && (
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

      {value.direction === "pull" ? (
        <PullFields value={value} onChange={onChange} hasSecret={hasSecret} />
      ) : value.direction === "outbound" ? (
        <OutboundFields value={value} onChange={onChange} hasSecret={hasSecret} />
      ) : (
        <>
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
        </>
      )}

      {value.direction !== "outbound" && <MappingField value={value} onChange={onChange} />}

      <label className="flex items-center gap-2 text-xs font-medium">
        <input
          type="checkbox"
          checked={value.active}
          onChange={(e) => onChange({ ...value, active: e.target.checked })}
        />
        Active (a paused endpoint is skipped — no pushes, pulls, or outbound deliveries)
      </label>
    </div>
  )
}

const MAPPING_SAMPLE = `{
  "items_path": "",
  "fields": {
    "op": { "const": "upsert" },
    "external_id": { "path": "id" },
    "feature_class": { "const": "MV_CONDUCTOR" },
    "subtype": { "path": "type", "map": { "OH": 1, "UG": 2 } },
    "geometry": { "path": "geom" },
    "attributes": {
      "fields": {
        "voltage_class": { "path": "voltage" },
        "condition_rating": { "path": "condition", "default": "GOOD" }
      }
    }
  }
}`

/** Optional field-mapping: a raw JSON spec that reshapes a sender's own payload
 * into native VoltEdge changes on the server (parseChanges/applyMapping). Blank =
 * the endpoint accepts native payloads only. Collapsed unless a spec is present. */
function MappingField({ value, onChange }: { value: FormState; onChange: (v: FormState) => void }) {
  const [open, setOpen] = useState(value.mappingText.trim().length > 0)
  const error = mappingParseError(value.mappingText)

  return (
    <div className="flex flex-col gap-1">
      <button
        type="button"
        className="hover:text-foreground text-muted-foreground flex items-center gap-1 self-start text-xs font-medium"
        onClick={() => setOpen((v) => !v)}
      >
        {open ? <ChevronDown className="size-3.5" /> : <ChevronRight className="size-3.5" />}
        Field mapping (advanced)
        {value.mappingText.trim() && !open && (
          <span className="bg-muted text-muted-foreground rounded px-1 py-0.5 text-[10px]">set</span>
        )}
      </button>
      {open && (
        <>
          <p className="text-muted-foreground text-xs">
            Leave blank to accept VoltEdge-native payloads. To ingest a sender's own JSON, describe how each native
            field is drawn from it — <span className="font-mono">const</span> / <span className="font-mono">path</span>{" "}
            (dot-path) / <span className="font-mono">map</span> per field, with{" "}
            <span className="font-mono">items_path</span> pointing at the array of records. Author field names to match
            the target's native change.
          </p>
          <textarea
            className={`${inputClass} h-48 font-mono text-xs`}
            value={value.mappingText}
            onChange={(e) => onChange({ ...value, mappingText: e.target.value })}
            placeholder="(native payloads — no mapping)"
            spellCheck={false}
          />
          <div className="flex items-center gap-3">
            <button
              type="button"
              className="text-primary text-xs hover:underline"
              onClick={() => onChange({ ...value, mappingText: MAPPING_SAMPLE })}
            >
              Insert sample
            </button>
            {value.mappingText.trim() && (
              <button
                type="button"
                className="text-muted-foreground text-xs hover:underline"
                onClick={() => onChange({ ...value, mappingText: "" })}
              >
                Clear
              </button>
            )}
          </div>
          {error && <p className="text-destructive text-xs">{error}</p>}
        </>
      )}
    </div>
  )
}

/** The pull-direction half of the form: where VoltEdge fetches from and how it
 * authenticates. The credential rides the same sealed signing_secret column as an
 * HMAC push secret, so it's only shown when the pull auth type needs one. */
function PullFields({
  value,
  onChange,
  hasSecret,
}: {
  value: FormState
  onChange: (v: FormState) => void
  hasSecret?: boolean
}) {
  return (
    <>
      <div className="flex flex-col gap-1">
        <label className={labelClass}>
          Pull URL <span className="text-destructive">*</span>
        </label>
        <input
          className={inputClass}
          value={value.pullUrl}
          onChange={(e) => onChange({ ...value, pullUrl: e.target.value })}
          placeholder="https://example.com/api/export.json"
        />
        <p className="text-muted-foreground text-xs">
          VoltEdge GETs this URL when you press <span className="font-medium">Pull now</span>. It must be a public
          http(s) address (internal/private hosts are refused) and return a native VoltEdge changes payload.
        </p>
      </div>

      <div className="flex flex-col gap-1">
        <label className={labelClass}>Pull authentication</label>
        <select
          className={inputClass}
          value={value.pullAuthType}
          onChange={(e) => onChange({ ...value, pullAuthType: e.target.value as WebhookPullAuthType })}
        >
          <option value="none">None (public URL)</option>
          <option value="bearer">Bearer token</option>
          <option value="api_key">API key (header or query param)</option>
          <option value="basic">Basic auth (username + password)</option>
        </select>
      </div>

      {value.pullAuthType === "api_key" && (
        <div className="grid grid-cols-2 gap-3">
          <div className="flex flex-col gap-1">
            <label className={labelClass}>Header name</label>
            <input
              className={inputClass}
              value={value.pullAuthHeader}
              onChange={(e) => onChange({ ...value, pullAuthHeader: e.target.value })}
              placeholder="X-Api-Key"
            />
          </div>
          <div className="flex flex-col gap-1">
            <label className={labelClass}>…or query param</label>
            <input
              className={inputClass}
              value={value.pullAuthQueryParam}
              onChange={(e) => onChange({ ...value, pullAuthQueryParam: e.target.value })}
              placeholder="api_key"
            />
          </div>
        </div>
      )}

      {value.pullAuthType === "basic" && (
        <div className="flex flex-col gap-1">
          <label className={labelClass}>Username</label>
          <input
            className={inputClass}
            value={value.pullAuthUsername}
            onChange={(e) => onChange({ ...value, pullAuthUsername: e.target.value })}
            placeholder="username"
          />
        </div>
      )}

      {pullAuthNeedsSecret(value.pullAuthType) && (
        <div className="flex flex-col gap-1">
          <label className={labelClass}>
            {value.pullAuthType === "basic" ? "Password" : value.pullAuthType === "api_key" ? "API key" : "Token"}
            {!hasSecret && <span className="text-destructive"> *</span>}
          </label>
          <input
            type="password"
            autoComplete="new-password"
            className={inputClass}
            value={value.signingSecret}
            onChange={(e) => onChange({ ...value, signingSecret: e.target.value })}
            placeholder={hasSecret ? "Leave blank to keep the stored credential" : ""}
          />
          <p className="text-muted-foreground text-xs">
            Stored encrypted; never shown again. Requires the server's encryption key to be configured.
          </p>
        </div>
      )}
    </>
  )
}

/** The outbound half of the form: where VoltEdge POSTs its events, which event
 * types this subscription hears, and the HMAC signing secret (always required —
 * every outbound delivery is signed so the subscriber can verify it). */
function OutboundFields({
  value,
  onChange,
  hasSecret,
}: {
  value: FormState
  onChange: (v: FormState) => void
  hasSecret?: boolean
}) {
  return (
    <>
      <div className="flex flex-col gap-1">
        <label className={labelClass}>
          Target URL <span className="text-destructive">*</span>
        </label>
        <input
          className={inputClass}
          value={value.targetUrl}
          onChange={(e) => onChange({ ...value, targetUrl: e.target.value })}
          placeholder="https://cis.example.com/hooks/voltedge"
        />
        <p className="text-muted-foreground text-xs">
          VoltEdge POSTs each matching event here. Must be a public http(s) address (internal/private hosts are
          refused). The receiver should verify the <span className="font-mono">X-VoltEdge-Signature</span> header and
          dedupe on <span className="font-mono">X-VoltEdge-Delivery</span>.
        </p>
      </div>

      <div className="flex flex-col gap-1">
        <label className={labelClass}>Event types</label>
        <textarea
          className={`${inputClass} h-24 font-mono text-xs`}
          value={value.eventTypes}
          onChange={(e) => onChange({ ...value, eventTypes: e.target.value })}
          placeholder={"oms.incident.raised\nwam.workorder.completed\nfaults.event.cleared"}
          spellCheck={false}
        />
        <p className="text-muted-foreground text-xs">
          One event type per line (e.g. <span className="font-mono">oms.incident.raised</span>,{" "}
          <span className="font-mono">wam.workorder.completed</span>, <span className="font-mono">feature.created</span>
          ). Leave blank to receive <span className="font-medium">every</span> event.
        </p>
      </div>

      <div className="grid grid-cols-2 gap-3">
        <div className="flex flex-col gap-1">
          <label className={labelClass}>Feature class filter</label>
          <textarea
            className={`${inputClass} h-20 font-mono text-xs`}
            value={value.classFilter}
            onChange={(e) => onChange({ ...value, classFilter: e.target.value })}
            placeholder={"DIST_XFMR\nMV_CONDUCTOR"}
            spellCheck={false}
          />
          <p className="text-muted-foreground text-xs">
            Limit <span className="font-mono">feature.*</span> events to these class codes (one per line). Blank = all
            classes.
          </p>
        </div>
        <div className="flex flex-col gap-1">
          <label className={labelClass}>Territory scope</label>
          <input
            className={inputClass}
            value={value.territoryId}
            onChange={(e) => onChange({ ...value, territoryId: e.target.value })}
            placeholder="territory UUID (optional)"
          />
          <p className="text-muted-foreground text-xs">
            Only events whose feature carries this <span className="font-mono">territory_id</span> are sent. Blank = all
            territories.
          </p>
        </div>
      </div>

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
          Stored encrypted; never shown again. Each delivery is signed{" "}
          <span className="font-mono">HMAC-SHA256(secret, "&lt;timestamp&gt;.&lt;body&gt;")</span>. Requires the
          server's encryption key to be configured.
        </p>
      </div>

      <OutboundMappingField value={value} onChange={onChange} />
    </>
  )
}

const OUTBOUND_MAPPING_SAMPLE = `{
  "fields": {
    "id": { "path": "entity.id" },
    "kind": { "path": "entity.kind" },
    "type": { "path": "event_type" },
    "at": { "path": "occurred_at" },
    "record": { "path": "data" }
  }
}`

/** Optional envelope mapping (outbound): reshape the VoltEdge event envelope into
 * whatever shape the receiver expects, before it is signed and sent. Same grammar
 * as the inbound mapping (const / path / map / default / nested fields), but it
 * runs on the single envelope object. Blank = send the native envelope. */
function OutboundMappingField({ value, onChange }: { value: FormState; onChange: (v: FormState) => void }) {
  const [open, setOpen] = useState(value.outboundMappingText.trim().length > 0)
  const error = mappingParseError(value.outboundMappingText)

  return (
    <div className="flex flex-col gap-1">
      <button
        type="button"
        className="hover:text-foreground text-muted-foreground flex items-center gap-1 self-start text-xs font-medium"
        onClick={() => setOpen((v) => !v)}
      >
        {open ? <ChevronDown className="size-3.5" /> : <ChevronRight className="size-3.5" />}
        Payload mapping (advanced)
        {value.outboundMappingText.trim() && !open && (
          <span className="bg-muted text-muted-foreground rounded px-1 py-0.5 text-[10px]">set</span>
        )}
      </button>
      {open && (
        <>
          <p className="text-muted-foreground text-xs">
            Leave blank to POST VoltEdge's native envelope (
            <span className="font-mono">{"{ event_id, event_type, occurred_at, actor, entity, data }"}</span>). To
            reshape it for a receiver that expects its own shape, describe each output field with{" "}
            <span className="font-mono">const</span> / <span className="font-mono">path</span> (dot-path into the
            envelope) / <span className="font-mono">map</span> / <span className="font-mono">default</span>.
          </p>
          <textarea
            className={`${inputClass} h-48 font-mono text-xs`}
            value={value.outboundMappingText}
            onChange={(e) => onChange({ ...value, outboundMappingText: e.target.value })}
            placeholder="(native envelope — no mapping)"
            spellCheck={false}
          />
          <div className="flex items-center gap-3">
            <button
              type="button"
              className="text-primary text-xs hover:underline"
              onClick={() => onChange({ ...value, outboundMappingText: OUTBOUND_MAPPING_SAMPLE })}
            >
              Insert sample
            </button>
            {value.outboundMappingText.trim() && (
              <button
                type="button"
                className="text-muted-foreground text-xs hover:underline"
                onClick={() => onChange({ ...value, outboundMappingText: "" })}
              >
                Clear
              </button>
            )}
          </div>
          {error && <p className="text-destructive text-xs">{error}</p>}
        </>
      )}
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
    (form.direction !== "pull" || form.pullUrl.trim().length > 0) &&
    (form.direction !== "outbound" || form.targetUrl.trim().length > 0) &&
    mappingParseError(form.mappingText) === null &&
    mappingParseError(form.outboundMappingText) === null &&
    (!formNeedsSecret(form) || endpoint.has_signing_secret || form.signingSecret.trim().length > 0)

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

/** One outbound delivery, its error/timing detail collapsed until expanded. */
function OutboundDeliveryRow({ endpointId, delivery }: { endpointId: string; delivery: OutboundDelivery }) {
  const [open, setOpen] = useState(false)
  const [replaying, setReplaying] = useState(false)
  const queryClient = useQueryClient()
  const terminal = delivery.status === "dead" || delivery.status === "succeeded"

  async function handleReplay() {
    setReplaying(true)
    try {
      await apiFetch(`/webhook-endpoints/${endpointId}/outbound-deliveries/${delivery.id}/replay`, { method: "POST" })
      queryClient.invalidateQueries({ queryKey: ["webhook-endpoints", endpointId, "outbound-deliveries"] })
      useToastStore.getState().pushSuccess("Replay queued — watch the log for the re-send.")
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
          <span className="truncate font-mono text-xs">{delivery.event_type}</span>
          <span className="text-muted-foreground truncate text-xs">{new Date(delivery.created_at).toLocaleString()}</span>
          {delivery.attempts > 0 && (
            <span className="text-muted-foreground text-xs">· {delivery.attempts} attempt(s)</span>
          )}
          {delivery.response_code != null && (
            <span className="text-muted-foreground font-mono text-xs">· HTTP {delivery.response_code}</span>
          )}
        </button>
        {terminal && (
          <Button
            variant="ghost"
            size="icon"
            className="size-7"
            title="Re-send this delivery"
            onClick={handleReplay}
            disabled={replaying}
          >
            {replaying ? <Loader2 className="size-4 animate-spin" /> : <RefreshCw className="size-4" />}
          </Button>
        )}
      </div>
      {open && (
        <div className="border-border/60 flex flex-col gap-1 border-t px-2 py-2 text-xs">
          {delivery.error && <p className="text-destructive">{delivery.error}</p>}
          <p className="text-muted-foreground font-mono">
            entity: {delivery.entity_kind} / {delivery.entity_id}
          </p>
          {delivery.status === "queued" && (
            <p className="text-muted-foreground">next attempt: {new Date(delivery.next_attempt_at).toLocaleString()}</p>
          )}
          {delivery.finished_at && (
            <p className="text-muted-foreground">finished: {new Date(delivery.finished_at).toLocaleString()}</p>
          )}
        </div>
      )}
    </div>
  )
}

/** The outbound delivery log for one subscription, polled while open. */
function OutboundDeliveriesPanel({ endpointId }: { endpointId: string }) {
  const { data, isPending, isError } = useOutboundDeliveries(endpointId)

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
        <OutboundDeliveryRow key={d.id} endpointId={endpointId} delivery={d} />
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
  const mapped = endpoint.mapping != null
  const [payload, setPayload] = useState(mapped ? "{\n\n}" : TEST_TEMPLATE)
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
      description={
        mapped
          ? "Apply the sender's own payload inline (as the service user) — it's mapped to native first. Failures dead-letter and won't be retried."
          : "Apply a native payload inline (as the service user) without sending a real push. Failures dead-letter and won't be retried."
      }
      onSubmit={handleRun}
      submitLabel={running ? "Running…" : "Run"}
      canSubmit={!running && payload.trim().length > 0}
      saving={running}
      className="max-w-2xl"
    >
      <div className="flex flex-col gap-1">
        <label className={labelClass}>{mapped ? "Payload (the sender's own JSON — mapped to native)" : "Payload (VoltEdge native JSON)"}</label>
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
  const [pulling, setPulling] = useState(false)
  const [pinging, setPinging] = useState(false)
  const queryClient = useQueryClient()

  async function handlePing() {
    setPinging(true)
    try {
      const res = await apiFetch<{ target_url: string; status_code?: number; delivered: boolean; error?: string }>(
        `/webhook-endpoints/${endpoint.id}/ping`,
        { method: "POST" },
      )
      if (res.delivered) {
        useToastStore.getState().pushSuccess(`Ping delivered${res.status_code ? ` (HTTP ${res.status_code})` : ""}.`)
      } else {
        useToastStore
          .getState()
          .pushError(`Ping failed${res.status_code ? ` (HTTP ${res.status_code})` : ""}: ${res.error ?? "no response"}`)
      }
    } catch (err) {
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "The ping failed.")
    } finally {
      setPinging(false)
    }
  }

  async function handlePull() {
    setPulling(true)
    try {
      const res = await apiFetch<{ delivery_id: string; result: unknown }>(
        `/webhook-endpoints/${endpoint.id}/pull`,
        { method: "POST" },
      )
      const applied = Array.isArray(res.result) ? res.result.length : 0
      queryClient.invalidateQueries({ queryKey: ["webhook-endpoints", endpoint.id, "deliveries"] })
      setShowLog(true)
      useToastStore.getState().pushSuccess(`Pulled — ${applied} change(s) applied. See the delivery log.`)
    } catch (err) {
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "The pull failed.")
    } finally {
      setPulling(false)
    }
  }

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
            {endpoint.direction === "outbound"
              ? `outbound · ${
                  endpoint.config?.event_types?.length
                    ? `${endpoint.config.event_types.length} event type(s)`
                    : "all events"
                }`
              : `${endpoint.direction} · ${endpoint.target} · ${endpoint.auth_mode}`}
          </CardDescription>
        </div>
        {canWrite && (
          <>
            {endpoint.direction === "pull" && (
              <Button
                variant="ghost"
                size="icon"
                className="size-7"
                title="Pull now (fetch and apply)"
                onClick={handlePull}
                disabled={pulling}
              >
                {pulling ? <Loader2 className="size-4 animate-spin" /> : <DownloadCloud className="size-4" />}
              </Button>
            )}
            {endpoint.direction === "outbound" && (
              <Button
                variant="ghost"
                size="icon"
                className="size-7"
                title="Ping (send a synthetic test event now)"
                onClick={handlePing}
                disabled={pinging}
              >
                {pinging ? <Loader2 className="size-4 animate-spin" /> : <Send className="size-4" />}
              </Button>
            )}
            {endpoint.direction !== "outbound" && (
              <Button
                variant="ghost"
                size="icon"
                className="size-7"
                title="Test (paste and apply)"
                onClick={() => setTesting(true)}
              >
                <FlaskConical className="size-4" />
              </Button>
            )}
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
        {endpoint.direction === "pull" && endpoint.config?.pull_url && (
          <div className="flex items-center gap-2">
            <DownloadCloud className="text-muted-foreground size-3.5 shrink-0" />
            <span className="text-muted-foreground truncate font-mono text-xs">{endpoint.config.pull_url}</span>
          </div>
        )}
        {endpoint.direction === "outbound" && endpoint.config?.target_url && (
          <div className="flex items-center gap-2">
            <Send className="text-muted-foreground size-3.5 shrink-0" />
            <span className="text-muted-foreground truncate font-mono text-xs">{endpoint.config.target_url}</span>
          </div>
        )}
        <div className="flex flex-wrap items-center gap-2">
          {endpoint.direction === "push" && needsSecret(endpoint.auth_mode) && (
            <span className="bg-muted text-muted-foreground flex items-center gap-1 rounded-md px-1.5 py-0.5 text-xs">
              <KeyRound className="size-3" />
              {endpoint.has_signing_secret ? "signing secret set" : "no signing secret"}
            </span>
          )}
          {endpoint.direction === "pull" && endpoint.config?.pull_auth?.type && endpoint.config.pull_auth.type !== "none" && (
            <span className="bg-muted text-muted-foreground flex items-center gap-1 rounded-md px-1.5 py-0.5 text-xs">
              <KeyRound className="size-3" />
              {endpoint.has_signing_secret ? "credential set" : "no credential"}
            </span>
          )}
          {endpoint.direction === "outbound" && (
            <span className="bg-muted text-muted-foreground flex items-center gap-1 rounded-md px-1.5 py-0.5 text-xs">
              <KeyRound className="size-3" />
              {endpoint.has_signing_secret ? "signing secret set" : "no signing secret"}
            </span>
          )}
          {endpoint.direction === "outbound" &&
            (endpoint.config?.event_types ?? []).map((t) => (
              <span key={t} className="bg-muted text-muted-foreground rounded-md px-1.5 py-0.5 font-mono text-xs">
                {t}
              </span>
            ))}
          {endpoint.direction === "outbound" && endpoint.config?.territory_id && (
            <span className="bg-muted text-muted-foreground rounded-md px-1.5 py-0.5 text-xs">1 territory</span>
          )}
          {endpoint.direction === "outbound" && endpoint.config?.mapping != null && (
            <span className="bg-muted text-muted-foreground rounded-md px-1.5 py-0.5 text-xs">mapped</span>
          )}
          {endpoint.config?.feature_class && (
            <span className="bg-muted text-muted-foreground rounded-md px-1.5 py-0.5 font-mono text-xs">
              {endpoint.config.feature_class}
            </span>
          )}
          {endpoint.mapping != null && (
            <span className="bg-muted text-muted-foreground rounded-md px-1.5 py-0.5 text-xs">mapped</span>
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
        {showLog &&
          (endpoint.direction === "outbound" ? (
            <OutboundDeliveriesPanel endpointId={endpoint.id} />
          ) : (
            <DeliveriesPanel endpointId={endpoint.id} />
          ))}
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
    (form.direction !== "pull" || form.pullUrl.trim().length > 0) &&
    (form.direction !== "outbound" || form.targetUrl.trim().length > 0) &&
    mappingParseError(form.mappingText) === null &&
    mappingParseError(form.outboundMappingText) === null &&
    (!formNeedsSecret(form) || form.signingSecret.trim().length > 0)

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
              Inbound endpoints external systems push to (or we pull from) — each delivery applied as the endpoint's
              service user — and outbound subscriptions VoltEdge POSTs its own events to (incidents, work orders, faults)
              as they happen, HMAC-signed and retried.
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
