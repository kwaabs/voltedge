import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"

import { ApiError, apiFetch } from "@/lib/api"

// Client for the completeness / business-rule catalog behind the attribute audit
// (api/internal/connectivity rules.go). A rule says a field is EXPECTED to be
// populated on a class (optionally only for one subtype); violations surface in
// the validation-issue queue. Editing needs metadata:write; reading is open to
// the /connectivity mount's read gate.

export interface ValidationRule {
  id: string
  feature_class_code: string
  field_name: string
  subtype_code: number | null
  severity: "error" | "warning"
  message: string | null
  sort_order: number
  active: boolean
}

export function useValidationRules(featureClass: string | undefined, includeInactive = true) {
  return useQuery({
    queryKey: ["validation-rules", featureClass ?? "", includeInactive],
    queryFn: () =>
      apiFetch<{ rules: ValidationRule[] }>(
        `/connectivity/rules?class=${encodeURIComponent(featureClass!)}${includeInactive ? "&include_inactive=true" : ""}`,
      ),
    enabled: !!featureClass,
    staleTime: 60_000,
  })
}

export interface ValidationRuleBody {
  feature_class_code?: string
  field_name?: string
  subtype_code?: number | null
  severity: "error" | "warning"
  message?: string | null
  sort_order: number
  active?: boolean
}

function useRulesMutation<TBody>(fn: (body: TBody) => Promise<unknown>) {
  const queryClient = useQueryClient()
  return useMutation<unknown, ApiError, TBody>({
    mutationFn: fn,
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["validation-rules"] }),
  })
}

export function useCreateValidationRule() {
  return useRulesMutation<ValidationRuleBody>((body) =>
    apiFetch("/connectivity/rules", { method: "POST", body: JSON.stringify(body) }),
  )
}

export function useUpdateValidationRule() {
  return useRulesMutation<{ id: string; body: ValidationRuleBody }>(({ id, body }) =>
    apiFetch(`/connectivity/rules/${id}`, { method: "PATCH", body: JSON.stringify(body) }),
  )
}

export function useDeleteValidationRule() {
  return useRulesMutation<string>((id) => apiFetch(`/connectivity/rules/${id}`, { method: "DELETE" }))
}
