import { useState } from "react"
import { ChevronDown, ChevronRight, ClipboardCheck, Loader2 } from "lucide-react"

import { RecordInspectionDialog } from "@/components/wam/record-inspection-dialog"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { type InspectionResult, type WorkOrder, useInspection, useInspections } from "@/hooks/use-wam"

// The Works-page Inspections card: record an inspection (shared dialog) and browse
// recorded inspections. Template AUTHORING moved to Admin → Inspection templates
// (configuration, metadata:write); recording stays here under wam:write.

function humanize(s: string): string {
  return s.replace(/_/g, " ")
}

function shortId(id: string): string {
  return id.slice(0, 8)
}

function formatWhen(iso: string): string {
  return new Date(iso).toLocaleString()
}

const RESULT_BADGE: Record<InspectionResult, string> = {
  pass: "bg-emerald-100 text-emerald-800 dark:bg-emerald-950 dark:text-emerald-300",
  fail: "bg-rose-100 text-rose-800 dark:bg-rose-950 dark:text-rose-300",
  conditional: "bg-amber-100 text-amber-800 dark:bg-amber-950 dark:text-amber-300",
}

function Badge({ text, cls }: { text: string; cls: string }) {
  return <span className={`rounded px-1.5 py-0.5 text-xs font-medium capitalize ${cls}`}>{humanize(text)}</span>
}

function InspectionRow({ id, summary }: { id: string; summary: string }) {
  const [open, setOpen] = useState(false)
  const detail = useInspection(open ? id : null)
  const items = detail.data?.items ?? []

  return (
    <div className="rounded-md">
      <button
        type="button"
        onClick={() => setOpen((o) => !o)}
        className="hover:bg-muted/40 flex w-full items-center gap-2 rounded-md px-2 py-2 text-left"
      >
        {open ? <ChevronDown className="size-4 shrink-0" /> : <ChevronRight className="size-4 shrink-0" />}
        <span className="text-sm">{summary}</span>
      </button>
      {open && (
        <div className="text-muted-foreground flex flex-col gap-1 px-8 pb-2 text-xs">
          {detail.isPending ? (
            <span className="flex items-center gap-2">
              <Loader2 className="size-3 animate-spin" /> Loading…
            </span>
          ) : items.length === 0 ? (
            <span>No recorded items.</span>
          ) : (
            items.map((it) => (
              <div key={it.id} className="flex items-center gap-2">
                <span className={it.passed === false ? "text-rose-600 dark:text-rose-400" : it.passed ? "text-emerald-600 dark:text-emerald-400" : ""}>
                  {it.passed === false ? "✗" : it.passed ? "✓" : "—"}
                </span>
                <span className="text-foreground">{it.prompt}</span>
                {it.value_numeric != null && <span>· {it.value_numeric}</span>}
                {it.value_text && <span>· {it.value_text}</span>}
                {it.defect_id && <span className="text-rose-600 dark:text-rose-400">· defect {shortId(it.defect_id)}</span>}
              </div>
            ))
          )}
        </div>
      )}
    </div>
  )
}

/** The Works-page Inspections card: record + browse recorded inspections. */
export function InspectionsPanel({ canWrite, workOrders }: { canWrite: boolean; workOrders: WorkOrder[] }) {
  const inspections = useInspections()
  const [recording, setRecording] = useState(false)
  const inspectionRows = inspections.data?.inspections ?? []

  return (
    <>
      <Card>
        <CardHeader className="pb-2">
          <div className="flex items-center justify-between gap-2">
            <div>
              <CardTitle className="flex items-center gap-2 text-sm">
                <ClipboardCheck className="size-4" />
                Inspections
              </CardTitle>
              <CardDescription>
                {inspectionRows.length === 0 ? "No inspections recorded." : `${inspectionRows.length} recorded.`}
              </CardDescription>
            </div>
            {canWrite && (
              <Button size="sm" onClick={() => setRecording(true)}>
                <ClipboardCheck className="size-4" />
                Record inspection
              </Button>
            )}
          </div>
        </CardHeader>
        <CardContent className="pt-0">
          {inspections.isPending ? (
            <div className="text-muted-foreground flex items-center gap-2 text-sm">
              <Loader2 className="size-4 animate-spin" />
              Loading inspections…
            </div>
          ) : inspections.isError ? (
            <p className="text-destructive text-sm">Could not load inspections.</p>
          ) : inspectionRows.length > 0 ? (
            <div className="flex flex-col">
              {inspectionRows.map((insp) => (
                <div key={insp.id} className="flex items-center gap-2">
                  <Badge text={insp.overall_result} cls={RESULT_BADGE[insp.overall_result]} />
                  <div className="min-w-0 flex-1">
                    <InspectionRow
                      id={insp.id}
                      summary={`${insp.asset_class} · ${shortId(insp.asset_id)}${insp.inspected_by ? ` · ${insp.inspected_by}` : ""} · ${formatWhen(insp.inspected_at)}`}
                    />
                  </div>
                </div>
              ))}
            </div>
          ) : (
            <p className="text-muted-foreground text-sm">No inspections recorded yet. Record one against an asset.</p>
          )}
        </CardContent>
      </Card>

      {recording && <RecordInspectionDialog workOrders={workOrders} onClose={() => setRecording(false)} />}
    </>
  )
}
