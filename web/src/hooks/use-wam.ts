import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"

import { ApiError, apiFetch } from "@/lib/api"

// Client mirror of the Work & Asset Management API (api/internal/wam). A defect
// or work order is an operational record about the assets, not feature data, so
// it has its own /wam surface gated by wam:read / wam:write.

export type Severity = "low" | "medium" | "high" | "critical"
export type DefectStatus = "open" | "work_ordered" | "resolved" | "dismissed"
export type DefectSource = "inspection" | "outage" | "condition_monitoring" | "ad_hoc"
export type WorkType = "inspection" | "corrective" | "preventive" | "construction"
export type WorkStatus = "draft" | "scheduled" | "assigned" | "in_progress" | "completed" | "cancelled"
export type Priority = "low" | "medium" | "high" | "urgent"

/** One asset-health finding (wam.defects). asset_class/asset_id name the
 * affected feature; incident_id links an outage-sourced defect to its OMS
 * incident. Longitude/Latitude are the projected point (null when no pin). */
export interface Defect {
  id: string
  asset_class: string
  asset_id: string
  defect_type: string | null
  severity: Severity
  status: DefectStatus
  source: DefectSource
  incident_id: string | null
  title: string
  description: string | null
  longitude: number | null
  latitude: number | null
  reported_by: string | null
  reported_at: string
  resolved_at: string | null
  notes: string | null
  created_at: string
  updated_at: string
}

/** One typed unit of work (wam.work_orders). defect_id links a corrective order
 * to the defect it addresses; asset_class/asset_id name the primary asset. */
export interface WorkOrder {
  id: string
  reference: string | null
  work_type: WorkType
  status: WorkStatus
  priority: Priority
  title: string
  description: string | null
  asset_class: string | null
  asset_id: string | null
  longitude: number | null
  latitude: number | null
  /** Where the order was raised from: "validation" (a data-quality fix),
   * "vegetation" (a clearance trim), etc., or null for a plain manual order. */
  source: string | null
  defect_id: string | null
  /** Served OMS incident (bare ref → oms.incidents) when raised from an outage. */
  incident_id: string | null
  assigned_to: string | null
  assigned_to_user_id: string | null
  /** Assigned field crew (bare ref → oms.crews); `crew_name` is joined for display.
   * A crew-assigned order reaches every member's field pack. */
  crew_id: string | null
  crew_name: string | null
  /** Tasked external subcontractor (bare ref → wam.subcontractors); `subcontractor_name`
   * joined for display. Recorded responsibility only — no field-pack fan-out. */
  subcontractor_id: string | null
  subcontractor_name: string | null
  /** Read-only mirror tag (migration 0182): null = locally owned (editable); a
   * system_code = mirrored from that external WAM — read-only here. */
  origin_system: string | null
  external_id: string | null
  scheduled_for: string | null
  due_by: string | null
  started_at: string | null
  completed_at: string | null
  completion_notes: string | null
  created_at: string
  updated_at: string
}

interface DefectsResponse {
  defects: Defect[]
}
interface WorkOrdersResponse {
  work_orders: WorkOrder[]
}

const WAM_KEY = ["wam"] as const

/** The defect board, newest first. `openOnly` narrows to still-open defects;
 * severity/asset filters are optional. assetClass+assetId scope the board to a
 * single feature (the map's Works tab passes both to show that asset's defects). */
export function useDefects(
  opts: { status?: DefectStatus; severity?: Severity; assetClass?: string; assetId?: string; openOnly?: boolean } = {},
) {
  const params = new URLSearchParams()
  if (opts.openOnly) params.set("open", "true")
  else if (opts.status) params.set("status", opts.status)
  if (opts.severity) params.set("severity", opts.severity)
  if (opts.assetClass) params.set("asset_class", opts.assetClass)
  if (opts.assetId) params.set("asset_id", opts.assetId)
  const query = params.toString() ? `?${params.toString()}` : ""
  return useQuery({
    queryKey: [
      ...WAM_KEY,
      "defects",
      opts.status ?? "all",
      opts.severity ?? "",
      opts.assetClass ?? "",
      opts.assetId ?? "",
      opts.openOnly ?? false,
    ],
    queryFn: () => apiFetch<DefectsResponse>(`/wam/defects${query}`),
    staleTime: 15_000,
  })
}

/** The work board, newest first. `openOnly` narrows to orders that are neither
 * completed nor cancelled; type/assignee filters are optional. assetClass+assetId
 * scope the board to a single feature (the map's Works tab passes both). */
export function useWorkOrders(
  opts: {
    status?: WorkStatus
    workType?: WorkType
    assignedTo?: string
    assignedToUserId?: string
    /** Scope to the caller's own assignments ("my work orders" on the field tool). */
    mine?: boolean
    assetClass?: string
    assetId?: string
    /** Scope to the restoration work raised for one OMS incident. */
    incidentId?: string
    /** Scope to the work assigned to one crew. */
    crewId?: string
    /** Scope to the work tasked to one subcontractor. */
    subcontractorId?: string
    openOnly?: boolean
    /** Opt-in auto-refresh (ms) so a board left open picks up a newly-assigned
     * order without a manual reload. Left unset on the drill-down call sites,
     * which don't need to poll. */
    refetchMs?: number
  } = {},
  enabled = true,
) {
  const params = new URLSearchParams()
  if (opts.openOnly) params.set("open", "true")
  else if (opts.status) params.set("status", opts.status)
  if (opts.workType) params.set("work_type", opts.workType)
  if (opts.assignedTo) params.set("assigned_to", opts.assignedTo)
  if (opts.mine) params.set("mine", "true")
  else if (opts.assignedToUserId) params.set("assigned_to_user_id", opts.assignedToUserId)
  if (opts.assetClass) params.set("asset_class", opts.assetClass)
  if (opts.assetId) params.set("asset_id", opts.assetId)
  if (opts.incidentId) params.set("incident_id", opts.incidentId)
  if (opts.crewId) params.set("crew_id", opts.crewId)
  if (opts.subcontractorId) params.set("subcontractor_id", opts.subcontractorId)
  const query = params.toString() ? `?${params.toString()}` : ""
  return useQuery({
    queryKey: [
      ...WAM_KEY,
      "work-orders",
      opts.status ?? "all",
      opts.workType ?? "",
      opts.assignedTo ?? "",
      opts.mine ? "mine" : opts.assignedToUserId ?? "",
      opts.assetClass ?? "",
      opts.assetId ?? "",
      opts.incidentId ?? "",
      opts.crewId ?? "",
      opts.subcontractorId ?? "",
      opts.openOnly ?? false,
    ],
    queryFn: () => apiFetch<WorkOrdersResponse>(`/wam/work-orders${query}`),
    staleTime: 15_000,
    refetchInterval: opts.refetchMs,
    enabled,
  })
}

/** One field worker a work order can be assigned to (a wam:write holder). */
export interface AssignableUser {
  id: string
  display_name: string
  email: string
}
interface AssignableUsersResponse {
  users: AssignableUser[]
}

/** Field staff a work order can be assigned to, for the assignee picker. */
export function useAssignableUsers() {
  return useQuery({
    queryKey: [...WAM_KEY, "assignable-users"],
    queryFn: () => apiFetch<AssignableUsersResponse>("/wam/assignable-users"),
    staleTime: 60_000,
  })
}

/** An external firm the utility tasks field work to (GET /wam/subcontractors).
 * NOT a crew — it has no members/status/packs; `open_work_order_count` is its
 * current tasked load. */
export interface Subcontractor {
  id: string
  name: string
  contact_name: string | null
  phone: string | null
  email: string | null
  notes: string | null
  active: boolean
  open_work_order_count: number
  created_at: string
  updated_at: string
}

/** The subcontractor registry. `includeInactive` also returns retired firms. */
export function useSubcontractors(opts: { includeInactive?: boolean } = {}) {
  const query = opts.includeInactive ? "?include_inactive=true" : ""
  return useQuery({
    queryKey: [...WAM_KEY, "subcontractors", opts.includeInactive ?? false],
    queryFn: () => apiFetch<{ subcontractors: Subcontractor[] }>(`/wam/subcontractors${query}`),
    staleTime: 30_000,
  })
}

export interface SubcontractorBody {
  name: string
  contact_name?: string
  phone?: string
  email?: string
  notes?: string
}

export function useCreateSubcontractor() {
  return useWamMutation<SubcontractorBody>((body) =>
    apiFetch("/wam/subcontractors", { method: "POST", body: JSON.stringify(body) }),
  )
}

export function useUpdateSubcontractor() {
  return useWamMutation<{ id: string; body: SubcontractorBody }>(({ id, body }) =>
    apiFetch(`/wam/subcontractors/${id}`, { method: "PATCH", body: JSON.stringify(body) }),
  )
}

export function useSetSubcontractorActive() {
  return useWamMutation<{ id: string; active: boolean }>(({ id, active }) =>
    apiFetch(`/wam/subcontractors/${id}/active`, { method: "POST", body: JSON.stringify({ active }) }),
  )
}

// --- Mutations -------------------------------------------------------------
// All gated by wam:write on the server; the page self-gates the controls. Each
// invalidates the whole /wam cache, since raising a work order from a defect
// touches both the defect board and the work board.

function useWamMutation<TBody>(fn: (body: TBody) => Promise<unknown>) {
  const queryClient = useQueryClient()
  return useMutation<unknown, ApiError, TBody>({
    mutationFn: fn,
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: WAM_KEY })
    },
  })
}

/** Log an asset defect. asset_class/asset_id and title are required (the server
 * enforces it). A lat/lng pin and incident_id (for an outage-sourced defect)
 * are optional. */
export interface CreateDefectBody {
  asset_class: string
  asset_id: string
  defect_type?: string
  severity?: Severity
  source?: DefectSource
  incident_id?: string
  title: string
  description?: string
  longitude?: number
  latitude?: number
  reported_by?: string
  notes?: string
}

export function useCreateDefect() {
  return useWamMutation<CreateDefectBody>((body) =>
    apiFetch("/wam/defects", { method: "POST", body: JSON.stringify(body) }),
  )
}

/** Transition a defect (open / work_ordered / resolved / dismissed). */
export function useSetDefectStatus() {
  return useWamMutation<{ id: string; status: DefectStatus }>(({ id, status }) =>
    apiFetch(`/wam/defects/${id}/status`, { method: "POST", body: JSON.stringify({ status }) }),
  )
}

/** Raise a corrective work order for a defect (flips it to work_ordered). All
 * overrides default from the defect. */
export interface RaiseWorkOrderBody {
  title?: string
  priority?: Priority
  description?: string
  assigned_to?: string
  assigned_to_user_id?: string
  crew_id?: string
  subcontractor_id?: string
  scheduled_for?: string
  due_by?: string
}

export function useRaiseWorkOrder() {
  return useWamMutation<{ defectId: string; body?: RaiseWorkOrderBody }>(({ defectId, body }) =>
    apiFetch(`/wam/defects/${defectId}/work-order`, { method: "POST", body: JSON.stringify(body ?? {}) }),
  )
}

/** Create a work order. work_type and title are required. */
export interface CreateWorkOrderBody {
  reference?: string
  work_type: WorkType
  priority?: Priority
  title: string
  description?: string
  asset_class?: string
  asset_id?: string
  longitude?: number
  latitude?: number
  defect_id?: string
  assigned_to?: string
  assigned_to_user_id?: string
  crew_id?: string
  subcontractor_id?: string
  scheduled_for?: string
  due_by?: string
}

export function useCreateWorkOrder() {
  return useWamMutation<CreateWorkOrderBody>((body) =>
    apiFetch("/wam/work-orders", { method: "POST", body: JSON.stringify(body) }),
  )
}

/** Patch a work order (status/priority/assignment/schedule/completion). Only
 * the given fields are written; status drives the started/completed timestamps
 * server-side. */
export interface UpdateWorkOrderBody {
  status?: WorkStatus
  priority?: Priority
  title?: string
  description?: string
  assigned_to?: string
  assigned_to_user_id?: string
  /** "" clears the crew; a uuid assigns it. */
  crew_id?: string
  /** "" clears the subcontractor; a uuid tasks it. */
  subcontractor_id?: string
  scheduled_for?: string
  due_by?: string
  completion_notes?: string
}

export function useUpdateWorkOrder() {
  return useWamMutation<{ id: string; body: UpdateWorkOrderBody }>(({ id, body }) =>
    apiFetch(`/wam/work-orders/${id}`, { method: "PATCH", body: JSON.stringify(body) }),
  )
}

// --- Inspections (Slice 2) -------------------------------------------------
// A reusable checklist (template) filled out against an asset (an inspection).
// A failing checklist item that carries a fail_severity auto-raises a defect on
// the server, so recording an inspection can touch the defect board too --
// hence these also invalidate the whole /wam cache.

export type ResponseType = "pass_fail" | "rating" | "numeric" | "text"
export type InspectionResult = "pass" | "fail" | "conditional"

export interface InspectionTemplateItem {
  id: string
  template_id: string
  position: number
  prompt: string
  response_type: ResponseType
  fail_severity: Severity | null
  guidance: string | null
  created_at: string
  updated_at: string
}

export interface InspectionTemplate {
  id: string
  code: string
  name: string
  description: string | null
  asset_class: string | null
  active: boolean
  created_at: string
  updated_at: string
  items?: InspectionTemplateItem[]
}

export interface InspectionItemResult {
  id: string
  inspection_id: string
  template_item_id: string | null
  position: number
  prompt: string
  response_type: ResponseType
  passed: boolean | null
  value_text: string | null
  value_numeric: number | null
  defect_id: string | null
  notes: string | null
  created_at: string
}

export interface Inspection {
  id: string
  work_order_id: string | null
  template_id: string | null
  asset_class: string
  asset_id: string
  overall_result: InspectionResult
  inspected_by: string | null
  inspected_at: string
  notes: string | null
  longitude: number | null
  latitude: number | null
  created_at: string
  updated_at: string
  items?: InspectionItemResult[]
}

interface TemplatesResponse {
  templates: InspectionTemplate[]
}
interface InspectionsResponse {
  inspections: Inspection[]
}

/** The checklist templates. `activeOnly` hides retired ones (the record picker). */
export function useInspectionTemplates(opts: { activeOnly?: boolean } = {}) {
  const query = opts.activeOnly ? "?active=true" : ""
  return useQuery({
    queryKey: [...WAM_KEY, "templates", opts.activeOnly ?? false],
    queryFn: () => apiFetch<TemplatesResponse>(`/wam/inspection-templates${query}`),
    staleTime: 30_000,
  })
}

/** One template with its ordered items; disabled until an id is given. */
export function useInspectionTemplate(id: string | null) {
  return useQuery({
    queryKey: [...WAM_KEY, "template", id],
    queryFn: () => apiFetch<InspectionTemplate>(`/wam/inspection-templates/${id}`),
    enabled: !!id,
    staleTime: 30_000,
  })
}

/** Recorded inspections, newest first, optionally scoped to an asset / order. */
export function useInspections(
  opts: { assetClass?: string; assetId?: string; workOrderId?: string; templateId?: string } = {},
) {
  const params = new URLSearchParams()
  if (opts.assetClass) params.set("asset_class", opts.assetClass)
  if (opts.assetId) params.set("asset_id", opts.assetId)
  if (opts.workOrderId) params.set("work_order_id", opts.workOrderId)
  if (opts.templateId) params.set("template_id", opts.templateId)
  const query = params.toString() ? `?${params.toString()}` : ""
  return useQuery({
    queryKey: [...WAM_KEY, "inspections", opts.assetClass ?? "", opts.assetId ?? "", opts.workOrderId ?? "", opts.templateId ?? ""],
    queryFn: () => apiFetch<InspectionsResponse>(`/wam/inspections${query}`),
    staleTime: 15_000,
  })
}

/** One inspection with its item results; disabled until an id is given. */
export function useInspection(id: string | null) {
  return useQuery({
    queryKey: [...WAM_KEY, "inspection", id],
    queryFn: () => apiFetch<Inspection>(`/wam/inspections/${id}`),
    enabled: !!id,
    staleTime: 15_000,
  })
}

export interface CreateTemplateItemBody {
  position?: number
  prompt: string
  response_type?: ResponseType
  fail_severity?: Severity
  guidance?: string
}
export interface CreateTemplateBody {
  code: string
  name: string
  description?: string
  asset_class?: string
  items: CreateTemplateItemBody[]
}

export function useCreateInspectionTemplate() {
  return useWamMutation<CreateTemplateBody>((body) =>
    apiFetch("/wam/inspection-templates", { method: "POST", body: JSON.stringify(body) }),
  )
}

export function useSetTemplateActive() {
  return useWamMutation<{ id: string; active: boolean }>(({ id, active }) =>
    apiFetch(`/wam/inspection-templates/${id}/active`, { method: "POST", body: JSON.stringify({ active }) }),
  )
}

/** One answered checklist item. `passed` false on an item with a fail_severity
 * auto-raises a defect; value_text/value_numeric hold a note or reading. */
export interface RecordInspectionItemBody {
  template_item_id?: string
  position?: number
  prompt: string
  response_type?: ResponseType
  passed?: boolean
  value_text?: string
  value_numeric?: number
  fail_severity?: Severity
  notes?: string
}
export interface RecordInspectionBody {
  work_order_id?: string
  template_id?: string
  asset_class: string
  asset_id: string
  inspected_by?: string
  inspected_at?: string
  notes?: string
  overall_result?: InspectionResult
  complete_work_order?: boolean
  items: RecordInspectionItemBody[]
}

export function useRecordInspection() {
  return useWamMutation<RecordInspectionBody>((body) =>
    apiFetch("/wam/inspections", { method: "POST", body: JSON.stringify(body) }),
  )
}
