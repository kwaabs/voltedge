import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"

import { apiFetch } from "@/lib/api"

/** One analysis study as the admin screen sees it for a class. `applicable` is
 * the code floor (its role makes it meaningful); `disabled` is the admin prune.
 * A study is OFFERED when applicable && !disabled. An inapplicable study can
 * never be enabled — it's shown read-only. */
export interface AnalysisStudyRow {
  id: string
  name: string
  category: string
  applicable: boolean
  disabled: boolean
}

/** The whole study suite with per-class applicable/disabled flags. Needs
 * metadata:write. */
export function useClassAnalysisStudies(code: string | undefined) {
  return useQuery({
    queryKey: ["admin", "feature-classes", code, "analysis-studies"],
    queryFn: () => apiFetch<{ studies: AnalysisStudyRow[] }>(`/admin/feature-classes/${code}/analysis-studies`),
    enabled: Boolean(code),
  })
}

/** Replace the pruned-study set for a class (PUT). The server stores only known,
 * applicable ids, so an empty array re-enables every applicable study. */
export function useSetClassAnalysisStudies(code: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (disabledStudyIds: string[]) =>
      apiFetch(`/admin/feature-classes/${code}/analysis-studies`, {
        method: "PUT",
        body: JSON.stringify({ disabled_study_ids: disabledStudyIds }),
      }),
    onSuccess: () =>
      qc.invalidateQueries({ queryKey: ["admin", "feature-classes", code, "analysis-studies"] }),
  })
}
