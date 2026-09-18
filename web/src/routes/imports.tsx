import { type ReactNode, useEffect, useMemo, useState } from "react"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import {
  AlertTriangle,
  ArrowRight,
  CheckCircle2,
  FileCheck2,
  FileUp,
  Loader2,
  Play,
  XCircle,
} from "lucide-react"

import { inputClass } from "@/components/map/field-utils"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { ApiError } from "@/lib/api"
import {
  type EtlDryRunReport,
  type EtlImportJob,
  type EtlImportProfile,
  ETL_IMPORT_ACCEPT,
  type ParsedEtlSource,
  enqueueImportJob,
  getImportJob,
  listImportJobs,
  listRunnableProfiles,
  parseEtlSource,
  runImportDryRun,
  sampleFeatures,
  uploadEtlArtifact,
} from "@/lib/etl-import"
import { useToastStore } from "@/stores/toast-store"

// The run wizard (S5, docs/etl-import-engine.md): the features:write surface a
// data operator uses to run one of the admin-authored import profiles over a
// source file. It is deliberately profile-DRIVEN, not a live column mapper --
// the column->field mapping and the match/upsert policy are authored once in the
// admin editor (/admin/imports) and reused here. So the wizard's job is: pick a
// saved profile, pick a matching file, pre-flight the mapping over a sample
// (dry-run, writes nothing), then run it and watch the load progress.
//
// Every source format is normalized in the browser to one canonical NDJSON
// artifact (see lib/etl-import.ts), so the wizard never has to understand the
// file's format beyond a sanity check that it matches what the profile expects.

const TERMINAL_STATUSES = new Set(["succeeded", "failed", "cancelled"])
const PREVIEW_ROWS = 8

/** Human summary of what a profile's write policy does, for the picker + banner. */
function writePolicyText(p: EtlImportProfile): string {
  return p.write_mode === "upsert"
    ? `Upsert — match existing on ${p.match_fields.join(", ") || "(no key!)"} , else insert`
    : "Insert only — every row is a new feature"
}

export function ImportRunPage() {
  const queryClient = useQueryClient()

  // Step 1: which saved profile to run.
  const profilesQuery = useQuery({
    queryKey: ["import-run", "profiles"],
    queryFn: listRunnableProfiles,
    staleTime: 60_000,
  })
  const [profileCode, setProfileCode] = useState<string | null>(null)
  const profile = profilesQuery.data?.find((p) => p.code === profileCode) ?? null

  // Step 2: the picked + normalized source file.
  const [fileName, setFileName] = useState<string | null>(null)
  const [sourceCrs, setSourceCrs] = useState("")
  const [parsing, setParsing] = useState(false)
  const [parsed, setParsed] = useState<ParsedEtlSource | null>(null)

  // Step 3: the sample pre-flight report.
  const [dryRunning, setDryRunning] = useState(false)
  const [dryReport, setDryReport] = useState<EtlDryRunReport | null>(null)

  // Step 4: the enqueued run we're polling.
  const [validateOnly, setValidateOnly] = useState(false)
  const [enqueuing, setEnqueuing] = useState(false)
  const [activeJobId, setActiveJobId] = useState<string | null>(null)

  const jobQuery = useQuery({
    queryKey: ["import-run", "job", activeJobId],
    queryFn: () => getImportJob(activeJobId as string),
    enabled: !!activeJobId,
    refetchInterval: (query) => {
      const status = query.state.data?.status
      return status && TERMINAL_STATUSES.has(status) ? false : 1500
    },
  })
  const job = jobQuery.data ?? null
  const jobDone = job != null && TERMINAL_STATUSES.has(job.status)

  const recentJobsQuery = useQuery({
    queryKey: ["import-run", "recent"],
    queryFn: listImportJobs,
    staleTime: 10_000,
  })

  // ---- transitions --------------------------------------------------------

  /** Choosing (or re-choosing) a profile resets every downstream step. */
  function chooseProfile(code: string) {
    setProfileCode(code)
    setFileName(null)
    setParsed(null)
    setDryReport(null)
    setActiveJobId(null)
  }

  async function handleFile(file: File | undefined) {
    if (!file || !profile) return
    setParsed(null)
    setDryReport(null)
    setActiveJobId(null)
    setFileName(file.name)
    setParsing(true)
    try {
      const result = await parseEtlSource(file, sourceCrs.trim() ? { reprojectFrom: sourceCrs.trim() } : {})
      setParsed(result)
    } catch (err) {
      setFileName(null)
      useToastStore.getState().pushError(err instanceof Error ? err.message : "Could not read that file.")
    } finally {
      setParsing(false)
    }
  }

  async function handleDryRun() {
    if (!profile || !parsed) return
    setDryRunning(true)
    try {
      const report = await runImportDryRun(profile.code, sampleFeatures(parsed))
      setDryReport(report)
    } catch (err) {
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "The pre-flight check failed.")
    } finally {
      setDryRunning(false)
    }
  }

  async function handleRun() {
    if (!profile || !parsed || !fileName) return
    setEnqueuing(true)
    try {
      const objectKey = await uploadEtlArtifact(parsed.features, fileName)
      const jobId = await enqueueImportJob(profile.code, objectKey, validateOnly)
      setActiveJobId(jobId)
      // The new run belongs at the top of the history list once it settles.
      queryClient.invalidateQueries({ queryKey: ["import-run", "recent"] })
    } catch (err) {
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not start the import.")
    } finally {
      setEnqueuing(false)
    }
  }

  // When a run reaches a terminal state, refresh the history panel once so the
  // finished run (with its final tallies) appears there.
  useEffect(() => {
    if (jobDone) queryClient.invalidateQueries({ queryKey: ["import-run", "recent"] })
  }, [jobDone, job?.id, queryClient])

  // ---- derived warnings ---------------------------------------------------

  // The profile's source_format may be a v2 live-DB value (postgis/mssql) that no
  // file parses to; for the file wizard we just compare the two file formats.
  const formatMismatch = profile != null && parsed != null && profile.source_format !== parsed.format
  const missingGeomColumns = useMemo(() => geometryColumnGaps(profile, parsed), [profile, parsed])

  const stepDone = { profile: !!profile, source: !!parsed, preflight: !!dryReport }

  return (
    <div className="h-full overflow-y-auto p-4">
      <div className="mx-auto flex w-full max-w-3xl flex-col gap-4">
        <div>
          <h1 className="text-lg font-semibold">Run an import</h1>
          <p className="text-muted-foreground text-sm">
            Load an external source file into a feature class using a saved import profile. The mapping and match
            policy are authored in{" "}
            <span className="font-medium">Admin → Data import</span>; here you pick one, pre-flight it, and run it.
          </p>
        </div>

        <Stepper done={stepDone} running={!!activeJobId} />

        {/* Step 1 — profile */}
        <Card>
          <CardHeader className="pb-2">
            <CardTitle className="text-sm">1. Choose an import profile</CardTitle>
            <CardDescription>Each profile targets one feature class with a fixed column mapping.</CardDescription>
          </CardHeader>
          <CardContent className="pt-0">
            {profilesQuery.isPending ? (
              <InlineLoading label="profiles" />
            ) : profilesQuery.isError ? (
              <p className="text-destructive text-sm">Could not load import profiles.</p>
            ) : (profilesQuery.data?.length ?? 0) === 0 ? (
              <p className="text-muted-foreground text-sm">
                No active import profiles. Create one under Admin → Data import first.
              </p>
            ) : (
              <div className="flex flex-col gap-1">
                {profilesQuery.data?.map((p) => (
                  <button
                    key={p.code}
                    type="button"
                    onClick={() => chooseProfile(p.code)}
                    className={`flex flex-col items-start gap-0.5 rounded-md border px-3 py-2 text-left ${
                      p.code === profileCode ? "border-primary bg-primary/5" : "hover:bg-muted/50"
                    }`}
                  >
                    <span className="text-sm font-medium">
                      {p.name} <span className="text-muted-foreground font-mono text-xs">{p.code}</span>
                    </span>
                    <span className="text-muted-foreground text-xs">
                      → {p.feature_class_code}
                      {p.subtype_code != null && ` · subtype ${p.subtype_code}`} · {p.source_format} ·{" "}
                      {p.write_mode === "upsert" ? "upsert" : "insert only"}
                    </span>
                  </button>
                ))}
              </div>
            )}
          </CardContent>
        </Card>

        {/* Step 2 — source file */}
        {profile && (
          <Card>
            <CardHeader className="pb-2">
              <CardTitle className="text-sm">2. Pick the source file</CardTitle>
              <CardDescription>
                {profile.name} expects a <span className="font-medium">{profile.source_format}</span> file.{" "}
                {writePolicyText(profile)}.
              </CardDescription>
            </CardHeader>
            <CardContent className="flex flex-col gap-3 pt-0">
              <div className="flex flex-wrap items-center gap-2">
                <label className="cursor-pointer">
                  <input
                    type="file"
                    accept={ETL_IMPORT_ACCEPT}
                    className="hidden"
                    onChange={(e) => handleFile(e.target.files?.[0])}
                  />
                  <span className="border-input bg-background hover:bg-muted/50 inline-flex items-center gap-2 rounded-md border px-3 py-1.5 text-sm">
                    <FileUp className="size-4" />
                    {fileName ? "Choose a different file" : "Choose file"}
                  </span>
                </label>
                {parsing && <InlineLoading label="file" />}
                {fileName && !parsing && <span className="text-muted-foreground text-sm">{fileName}</span>}
              </div>

              {/* Shapefile/DXF may carry no CRS -- let the operator name one to
                  reproject from (mirrors the map overlay import). */}
              <div className="flex flex-col gap-1">
                <label className="text-muted-foreground text-xs">
                  Source CRS (optional — for a shapefile with no .prj, or DXF)
                </label>
                <input
                  className={`${inputClass} max-w-xs`}
                  value={sourceCrs}
                  onChange={(e) => setSourceCrs(e.target.value)}
                  placeholder="e.g. EPSG:27700"
                />
              </div>

              {parsed && (
                <div className="flex flex-col gap-2">
                  <div className="flex flex-wrap items-center gap-2 text-sm">
                    <Badge variant="secondary">{parsed.features.length} rows</Badge>
                    <Badge variant="secondary">{parsed.columns.length} columns</Badge>
                    <Badge variant="secondary">{parsed.format}</Badge>
                  </div>

                  {formatMismatch && (
                    <WarnBanner>
                      This file parsed as <span className="font-medium">{parsed.format}</span>, but the profile expects{" "}
                      <span className="font-medium">{profile.source_format}</span>. You can still run it, but double-check
                      it's the right file.
                    </WarnBanner>
                  )}
                  {missingGeomColumns.length > 0 && (
                    <WarnBanner>
                      The profile builds geometry from column{missingGeomColumns.length > 1 ? "s" : ""}{" "}
                      <span className="font-mono">{missingGeomColumns.join(", ")}</span>, which{" "}
                      {missingGeomColumns.length > 1 ? "are" : "is"} not in this file. Those rows will have no geometry.
                    </WarnBanner>
                  )}

                  <SamplePreview parsed={parsed} />
                </div>
              )}
            </CardContent>
          </Card>
        )}

        {/* Step 3 — pre-flight */}
        {profile && parsed && (
          <Card>
            <CardHeader className="pb-2">
              <CardTitle className="text-sm">3. Pre-flight check</CardTitle>
              <CardDescription>
                Validates the first {Math.min(PREVIEW_ROWS + 12, parsed.features.length)} rows through the real mapping
                and feature rules — writes nothing.
              </CardDescription>
            </CardHeader>
            <CardContent className="flex flex-col gap-3 pt-0">
              <div>
                <Button size="sm" variant="outline" onClick={handleDryRun} disabled={dryRunning}>
                  {dryRunning ? <Loader2 className="size-4 animate-spin" /> : <FileCheck2 className="size-4" />}
                  Run pre-flight
                </Button>
              </div>
              {dryReport && <DryRunResult report={dryReport} />}
            </CardContent>
          </Card>
        )}

        {/* Step 4 — run */}
        {profile && parsed && (
          <Card>
            <CardHeader className="pb-2">
              <CardTitle className="text-sm">4. Run the import</CardTitle>
              <CardDescription>
                Uploads the normalized file and streams every row into{" "}
                <span className="font-mono">{profile.feature_class_code}</span>.
              </CardDescription>
            </CardHeader>
            <CardContent className="flex flex-col gap-3 pt-0">
              <label className="flex items-center gap-2 text-sm">
                <input
                  type="checkbox"
                  checked={validateOnly}
                  onChange={(e) => setValidateOnly(e.target.checked)}
                  disabled={!!activeJobId && !jobDone}
                />
                Validate the whole file only (don't write anything)
              </label>
              <div>
                <Button size="sm" onClick={handleRun} disabled={enqueuing || (!!activeJobId && !jobDone)}>
                  {enqueuing || (!!activeJobId && !jobDone) ? (
                    <Loader2 className="size-4 animate-spin" />
                  ) : (
                    <Play className="size-4" />
                  )}
                  {validateOnly
                    ? `Validate ${parsed.features.length} rows`
                    : `Import ${parsed.features.length} rows`}
                </Button>
              </div>
              {job && <RunProgress job={job} />}
            </CardContent>
          </Card>
        )}

        {/* Recent runs */}
        <Card>
          <CardHeader className="pb-2">
            <CardTitle className="text-sm">Recent runs</CardTitle>
            <CardDescription>Your most recent import runs.</CardDescription>
          </CardHeader>
          <CardContent className="pt-0">
            {recentJobsQuery.isPending ? (
              <InlineLoading label="runs" />
            ) : (recentJobsQuery.data?.length ?? 0) === 0 ? (
              <p className="text-muted-foreground text-sm">No runs yet.</p>
            ) : (
              <div className="flex flex-col gap-1">
                {recentJobsQuery.data?.map((j) => (
                  <div
                    key={j.id}
                    className="flex items-center gap-2 rounded-md px-2 py-1 text-sm hover:bg-muted/50"
                  >
                    <StatusBadge status={j.status} />
                    <span className="font-mono text-xs">{j.feature_class_code}</span>
                    {j.dry_run && <span className="text-muted-foreground text-xs">(validate)</span>}
                    <span className="text-muted-foreground ml-auto text-xs">
                      {j.inserted_count} ins · {j.updated_count} upd · {j.skipped_count} skip · {j.error_count} err
                    </span>
                  </div>
                ))}
              </div>
            )}
          </CardContent>
        </Card>
      </div>
    </div>
  )
}

// ---------------------------------------------------------------------------
// Presentational helpers
// ---------------------------------------------------------------------------

/** The four-step progress indicator across the top. */
function Stepper({
  done,
  running,
}: {
  done: { profile: boolean; source: boolean; preflight: boolean }
  running: boolean
}) {
  const steps = [
    { n: 1, label: "Profile", done: done.profile },
    { n: 2, label: "Source", done: done.source },
    { n: 3, label: "Pre-flight", done: done.preflight },
    { n: 4, label: "Run", done: running },
  ]
  return (
    <div className="flex items-center gap-1 text-xs">
      {steps.map((s, i) => (
        <div key={s.n} className="flex items-center gap-1">
          <span
            className={`flex size-5 items-center justify-center rounded-full text-[10px] font-semibold ${
              s.done ? "bg-primary text-primary-foreground" : "bg-muted text-muted-foreground"
            }`}
          >
            {s.done ? "✓" : s.n}
          </span>
          <span className={s.done ? "font-medium" : "text-muted-foreground"}>{s.label}</span>
          {i < steps.length - 1 && <ArrowRight className="text-muted-foreground size-3" />}
        </div>
      ))}
    </div>
  )
}

function SamplePreview({ parsed }: { parsed: ParsedEtlSource }) {
  const cols = parsed.columns.slice(0, 8)
  const rows = parsed.features.slice(0, PREVIEW_ROWS)
  return (
    <div className="overflow-x-auto rounded-md border">
      <table className="w-full text-xs">
        <thead className="bg-muted/50">
          <tr>
            {cols.map((c) => (
              <th key={c} className="px-2 py-1 text-left font-medium">
                {c}
              </th>
            ))}
            {parsed.columns.length > cols.length && <th className="px-2 py-1 text-left font-medium">…</th>}
          </tr>
        </thead>
        <tbody>
          {rows.map((f, i) => (
            <tr key={i} className="border-t">
              {cols.map((c) => (
                <td key={c} className="text-muted-foreground max-w-[16rem] truncate px-2 py-1">
                  {String(f.properties[c] ?? "")}
                </td>
              ))}
              {parsed.columns.length > cols.length && <td className="px-2 py-1">…</td>}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

function DryRunResult({ report }: { report: EtlDryRunReport }) {
  const invalidRows = report.rows.filter((r) => !r.valid)
  return (
    <div className="flex flex-col gap-2">
      <div className="flex flex-wrap items-center gap-2 text-sm">
        <span className="inline-flex items-center gap-1">
          <CheckCircle2 className="size-4 text-green-600" />
          {report.valid} valid
        </span>
        {report.invalid > 0 && (
          <span className="inline-flex items-center gap-1">
            <XCircle className="text-destructive size-4" />
            {report.invalid} invalid
          </span>
        )}
        <span className="text-muted-foreground">of {report.total} sampled</span>
      </div>
      {invalidRows.length > 0 ? (
        <div className="border-destructive/40 flex flex-col gap-1 rounded-md border p-2">
          {invalidRows.slice(0, 20).map((r) => (
            <div key={r.row_number} className="text-xs">
              <span className="font-mono">row {r.row_number}</span>{" "}
              <span className="text-destructive">{r.error}</span>
            </div>
          ))}
          {invalidRows.length > 20 && (
            <div className="text-muted-foreground text-xs">…and {invalidRows.length - 20} more.</div>
          )}
        </div>
      ) : (
        <p className="text-muted-foreground text-xs">
          Every sampled row maps cleanly. Geometry present on{" "}
          {report.rows.filter((r) => r.has_geometry).length} of {report.rows.length}.
        </p>
      )}
    </div>
  )
}

function RunProgress({ job }: { job: EtlImportJob }) {
  const done = TERMINAL_STATUSES.has(job.status)
  const pct = job.total_rows && job.total_rows > 0
    ? Math.round(((job.inserted_count + job.updated_count + job.skipped_count + job.error_count) / job.total_rows) * 100)
    : null
  return (
    <div className="flex flex-col gap-2 rounded-md border p-3">
      <div className="flex items-center gap-2 text-sm">
        <StatusBadge status={job.status} />
        {!done && <Loader2 className="text-muted-foreground size-4 animate-spin" />}
        {pct != null && !done && <span className="text-muted-foreground text-xs">{pct}%</span>}
      </div>
      <div className="grid grid-cols-4 gap-2 text-center text-sm">
        <Tally label="inserted" value={job.inserted_count} />
        <Tally label="updated" value={job.updated_count} />
        <Tally label="skipped" value={job.skipped_count} />
        <Tally label="errors" value={job.error_count} tone={job.error_count > 0 ? "bad" : undefined} />
      </div>
      {job.error && (
        <WarnBanner>
          <span className="font-medium">Run failed:</span> {job.error}
        </WarnBanner>
      )}
      {job.errors && job.errors.length > 0 && (
        <div className="flex max-h-48 flex-col gap-1 overflow-y-auto rounded-md border p-2">
          {job.errors.map((e) => (
            <div key={e.row_number} className="text-xs">
              <span className="font-mono">row {e.row_number}</span>{" "}
              <span className="text-destructive">{e.message}</span>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}

function Tally({ label, value, tone }: { label: string; value: number; tone?: "bad" }) {
  return (
    <div className="flex flex-col">
      <span className={`text-lg font-semibold ${tone === "bad" ? "text-destructive" : ""}`}>{value}</span>
      <span className="text-muted-foreground text-xs">{label}</span>
    </div>
  )
}

function StatusBadge({ status }: { status: EtlImportJob["status"] }) {
  const map: Record<EtlImportJob["status"], { label: string; className: string }> = {
    queued: { label: "queued", className: "bg-muted text-muted-foreground" },
    running: { label: "running", className: "bg-blue-100 text-blue-700 dark:bg-blue-950 dark:text-blue-300" },
    succeeded: { label: "succeeded", className: "bg-green-100 text-green-700 dark:bg-green-950 dark:text-green-300" },
    failed: { label: "failed", className: "bg-destructive/15 text-destructive" },
    cancelled: { label: "cancelled", className: "bg-muted text-muted-foreground" },
  }
  const s = map[status]
  return <span className={`rounded px-1.5 py-0.5 text-xs font-medium ${s.className}`}>{s.label}</span>
}

function WarnBanner({ children }: { children: ReactNode }) {
  return (
    <div className="flex items-start gap-2 rounded-md border border-amber-300 bg-amber-50 p-2 text-xs text-amber-900 dark:border-amber-900 dark:bg-amber-950 dark:text-amber-200">
      <AlertTriangle className="mt-0.5 size-4 shrink-0" />
      <div>{children}</div>
    </div>
  )
}

function InlineLoading({ label }: { label: string }) {
  return (
    <div className="text-muted-foreground flex items-center gap-2 text-sm">
      <Loader2 className="size-4 animate-spin" />
      Loading {label}…
    </div>
  )
}

// ---------------------------------------------------------------------------
// Pure helpers
// ---------------------------------------------------------------------------

/** Which geometry source columns the profile needs but the file lacks -- a soft
 * warning, since the load simply leaves those rows geometry-less (and validation
 * then reports it if the class requires geometry). */
function geometryColumnGaps(profile: EtlImportProfile | null, parsed: ParsedEtlSource | null): string[] {
  if (!profile || !parsed) return []
  const have = new Set(parsed.columns)
  const need: string[] = []
  if (profile.geometry_mode === "lonlat") {
    if (profile.geometry_lon_column) need.push(profile.geometry_lon_column)
    if (profile.geometry_lat_column) need.push(profile.geometry_lat_column)
  } else if (profile.geometry_mode === "wkt") {
    if (profile.geometry_wkt_column) need.push(profile.geometry_wkt_column)
  }
  return need.filter((c) => !have.has(c))
}
