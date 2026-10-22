import { useMutation } from "@tanstack/react-query"

import { apiFetch } from "@/lib/api"
import type { ConnectivityAuditFinding, ConnectivityAuditResult } from "@/stores/connectivity-audit-store"

// Runs the connectivity + data-quality audit (GET /connectivity/audit, which
// AuditAll merges both into) SCOPED to a specific set of features -- the "did my
// edits break anything" check, as opposed to the whole-database sweep the Network
// Audit panel runs. The endpoint already honours ?feature_ids=; this just drives
// it from a chosen set (e.g. the features in My edits) and merges the results.

// Keep each request's URL well under proxy limits by batching the ids.
const CHUNK = 100

function findingKey(f: ConnectivityAuditFinding): string {
  return [f.kind, f.connection_id ?? "", f.from_id ?? "", f.to_id ?? ""].join("|")
}

export interface ScopedAuditResult {
  findings: ConnectivityAuditFinding[]
  truncated: boolean
  scoped: number
}

/** Audit just the given features. Batches the ids, de-duplicates findings that
 * span two batches, and reports how many features were in scope. */
export function useAuditFeatures() {
  return useMutation<ScopedAuditResult, Error, string[]>({
    mutationFn: async (ids) => {
      const seen = new Set<string>()
      const findings: ConnectivityAuditFinding[] = []
      let truncated = false
      for (let i = 0; i < ids.length; i += CHUNK) {
        const params = new URLSearchParams({ feature_ids: ids.slice(i, i + CHUNK).join(",") })
        const report = await apiFetch<ConnectivityAuditResult>(`/connectivity/audit?${params.toString()}`)
        if (report.summary?.truncated) truncated = true
        for (const f of report.findings ?? []) {
          const k = findingKey(f)
          if (!seen.has(k)) {
            seen.add(k)
            findings.push(f)
          }
        }
      }
      // Errors first, then warnings, then info -- worst surfaced at the top.
      const rank = (s: string) => (s === "error" ? 0 : s === "warning" ? 1 : 2)
      findings.sort((a, b) => rank(a.severity) - rank(b.severity))
      return { findings, truncated, scoped: ids.length }
    },
  })
}
