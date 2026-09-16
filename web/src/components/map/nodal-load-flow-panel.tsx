import { useEffect, useState } from "react"
import { Search, X } from "lucide-react"

import { AnalysisExplanation, Formula } from "@/components/map/analysis-explanation"
import { inputClass } from "@/components/map/field-utils"
import { Button } from "@/components/ui/button"
import type { FeatureRecord } from "@/hooks/use-feature"
import { useFeatureClasses } from "@/hooks/use-feature-classes"
import { useFeatureNameSearch, MIN_QUERY_LENGTH } from "@/hooks/use-feature-name-search"
import { useFloatingPanel } from "@/hooks/use-floating-panel"
import { ApiError, apiFetch } from "@/lib/api"
import { cn } from "@/lib/utils"
import type { NodalLoadFlowResult, NodalRegulatedGen } from "@/stores/nodal-load-flow-store"
import { useNodalLoadFlowStore } from "@/stores/nodal-load-flow-store"
import { useSelectionStore } from "@/stores/selection-store"
import type { TraceFeatureRef } from "@/stores/trace-store"
import { useToastStore } from "@/stores/toast-store"

function shortId(id: string): string {
  return id.length > 8 ? `${id.slice(0, 8)}…` : id
}

function refKey(ref: TraceFeatureRef): string {
  return `${ref.feature_class}:${ref.feature_id}`
}

function formatNumber(n: number, digits = 1): string {
  return n.toLocaleString(undefined, { minimumFractionDigits: digits, maximumFractionDigits: digits })
}

/** A single-line feature-name search that fans out across every feature class
 * (useFeatureNameSearch) and calls onPick with the chosen feature. Backs both
 * S3 pickers in the nodal prompt -- extra grid-infeed sources and grid-forming
 * (regulated) generators -- reusing the exact search the map's own search box
 * uses. `exclude` hides features already chosen (and the start itself). */
function FeaturePicker({
  placeholder,
  exclude,
  onPick,
}: {
  placeholder: string
  exclude: Set<string>
  onPick: (ref: TraceFeatureRef, name: string) => void
}) {
  const [query, setQuery] = useState("")
  const [focused, setFocused] = useState(false)
  const { results, loading } = useFeatureNameSearch(query)
  // The dropdown opens whenever the box is focused (so it reads as a picker, not
  // a bare text field); `querying` gates the actual search vs. the "type to
  // search" hint. Blur closes it, but the results panel preventDefaults its
  // mousedown so a pick keeps focus and the onClick still fires.
  const querying = query.trim().length >= MIN_QUERY_LENGTH
  const filtered = results.filter((r) => !exclude.has(`${r.featureClassCode}:${r.featureId}`))

  return (
    <div className="relative">
      <Search className="text-muted-foreground pointer-events-none absolute top-1/2 left-2 size-3.5 -translate-y-1/2" />
      <input
        type="text"
        className={cn(inputClass, "w-full pl-7")}
        placeholder={placeholder}
        value={query}
        onChange={(e) => setQuery(e.target.value)}
        onFocus={() => setFocused(true)}
        onBlur={() => setFocused(false)}
      />
      {focused && (
        <div
          className="bg-popover absolute z-10 mt-1 max-h-44 w-full overflow-y-auto rounded-md border shadow-lg"
          onMouseDown={(e) => e.preventDefault()}
        >
          {!querying && (
            <p className="text-muted-foreground px-2 py-1.5 text-xs">
              Type at least {MIN_QUERY_LENGTH} characters to search…
            </p>
          )}
          {querying && loading && <p className="text-muted-foreground px-2 py-1.5 text-xs">Searching…</p>}
          {querying && !loading && filtered.length === 0 && (
            <p className="text-muted-foreground px-2 py-1.5 text-xs">No matches</p>
          )}
          {querying &&
            filtered.map((r) => (
              <button
                key={`${r.featureClassCode}:${r.featureId}`}
                type="button"
                onClick={() => {
                  onPick({ feature_class: r.featureClassCode, feature_id: r.featureId }, r.name)
                  setQuery("")
                }}
                className="hover:bg-accent flex w-full flex-col items-start rounded-sm px-2 py-1 text-left"
              >
                <span className="text-sm">{r.name}</span>
                <span className="text-muted-foreground text-xs">{r.className}</span>
              </button>
            ))}
        </div>
      )}
    </div>
  )
}

/** A node's own voltage base (nominal_base_v) as a compact level label --
 * "11 kV" / "400 V" -- so MV nodes and the LV nodes below a transformer read
 * apart at a glance (the nodal solver rebases voltage across each transformer). */
function formatVoltageLevel(volts: number): string {
  if (volts >= 1000) return `${formatNumber(volts / 1000, volts % 1000 === 0 ? 0 : 1)} kV`
  return `${formatNumber(volts, 0)} V`
}

/**
 * Floating panel for api/internal/powerflow's nodal (Y-bus) load-flow engine
 * (POST /analysis/nodal-load-flow, Phase 52 Tier 2 -- see
 * docs/phase-52-nodal-powerflow.md). Unlike the radial /load-flow panel this
 * drives a distinct solver that assembles a per-unit admittance matrix over the
 * GENERAL graph and solves it by Gauss-Seidel, so a densely meshed LV network
 * is solved directly rather than refused. Same combined prompt+result shape and
 * bottom-center slot as the other analysis panels; the analyses are mutually
 * exclusive, so feature-panel.tsx's triggers clear each other's stores.
 *
 * The prompt asks only for nominal_voltage_v (the slack-bus voltage base, the
 * same un-guessable input the radial load flow needs). The result adds the
 * solver's convergence telemetry -- a non-converged solve is flagged, not
 * silently trusted -- above the same per-node voltage/current/loading table.
 */
/**
 * The dynamic "how this was calculated" body for a nodal (Y-bus) run: which
 * solver ran, whether it converged (and in how many iterations / to what
 * mismatch tolerance), the slack and PV bus counts, and the worst-voltage bus.
 * Every figure is read off THIS result so an engineer can verify the run.
 */
function NodalLoadFlowExplanation({
  result,
  classNameFor,
}: {
  result: NodalLoadFlowResult
  classNameFor: (code: string) => string
}) {
  const pvNodes = result.nodes.filter((n) => n.role === "pv")
  const busNodes = result.nodes.filter((n) => n.role === "bus" || n.role === "slack" || n.role === "pv")
  const worst = busNodes.reduce<(typeof busNodes)[number] | null>((lo, n) => {
    if (n.nominal_base_v <= 0 || n.voltage_v <= 0) return lo
    if (lo === null || n.voltage_v / n.nominal_base_v < lo.voltage_v / lo.nominal_base_v) return n
    return lo
  }, null)
  const worstPu = worst ? worst.voltage_v / worst.nominal_base_v : 0

  return (
    <AnalysisExplanation>
      <p>
        A per-unit <b>nodal (Y-bus)</b> solve: a bus admittance matrix <code>Y</code> is assembled over the
        general graph and <code>Y·V = I</code> is solved iteratively for every bus voltage, so a densely meshed
        network is solved directly rather than reduced to a radial tree.
      </p>
      <p>
        Solver: <b>{result.solver}</b>.{" "}
        {result.converged ? (
          <>
            It <b>converged</b> in <b>{result.iterations}</b> iteration{result.iterations === 1 ? "" : "s"} (max
            power mismatch <code>{result.max_mismatch_pu.toExponential(1)} pu</code>).
          </>
        ) : (
          <>
            It <b>did not converge</b> after <b>{result.iterations}</b> iteration{result.iterations === 1 ? "" : "s"}{" "}
            -- the max power mismatch was <code>{result.max_mismatch_pu.toExponential(1)} pu</code>, so treat the
            voltages below as <b>unconverged</b>.
          </>
        )}
      </p>
      <p>
        <b>{result.bus_count}</b> bus{result.bus_count === 1 ? "" : "es"}, <b>{result.branch_count}</b> branch
        {result.branch_count === 1 ? "" : "es"}, <b>{result.slack_count}</b> slack source
        {result.slack_count === 1 ? "" : "s"}
        {pvNodes.length > 0 && (
          <>
            {" "}
            and <b>{pvNodes.length}</b> PV-regulated bus(es)
          </>
        )}
        .
        {worst && (
          <>
            {" "}
            The lowest bus voltage is <b>{formatNumber(worstPu, 3)} pu</b> ({formatNumber(worst.voltage_v)} V, -
            {formatNumber(worst.voltage_drop_pct, 2)}%) at {classNameFor(worst.feature_class)} (
            {formatVoltageLevel(worst.nominal_base_v)} base).
          </>
        )}
      </p>
      <Formula>{`Y·V = I  solved by ${result.solver}
max mismatch = ${result.max_mismatch_pu.toExponential(1)} pu  ${result.converged ? "≤ tol   → converged in " : "> tol   → did NOT converge ("}${result.iterations} iteration${result.iterations === 1 ? "" : "s"}${result.converged ? "" : ")"}${
        worst
          ? `\nworst bus:    V_pu = V / V_base = ${formatNumber(worst.voltage_v)} V / ${formatNumber(worst.nominal_base_v, 0)} V = ${formatNumber(worstPu, 3)} pu`
          : ""
      }`}</Formula>
      {(result.overloaded.length > 0 || result.barriers.length > 0) && (
        <p>
          Honesty flags:{" "}
          {result.overloaded.length > 0 && (
            <>
              <b>{result.overloaded.length}</b> element(s) over 100% loading.{" "}
            </>
          )}
          {result.barriers.length > 0 && (
            <>
              <b>{result.barriers.length}</b> open device(s) stopped the walk.
            </>
          )}
        </p>
      )}
    </AnalysisExplanation>
  )
}

export function NodalLoadFlowPanel() {
  const armed = useNodalLoadFlowStore((s) => s.armed)
  const armedStart = useNodalLoadFlowStore((s) => s.armedStart)
  const loading = useNodalLoadFlowStore((s) => s.loading)
  const result = useNodalLoadFlowStore((s) => s.result)
  const { data: featureClasses } = useFeatureClasses()
  const [voltageInput, setVoltageInput] = useState("11000")
  // S5 solver selector. Gauss-Seidel is the default (the proven engine whose
  // numbers S2/S3 were hand-verified against); "newton" opts into the
  // Newton-Raphson solver, which converges quadratically on stiff meshed grids.
  const [solver, setSolver] = useState<"gauss-seidel" | "newton">("gauss-seidel")
  // Each row's human-assigned name + circuit membership, resolved per feature
  // the same way trace-result-table.tsx does -- the solver response carries only
  // feature_class/feature_id, and a feature's own name and Phase 37 circuit ride
  // on its attributes, so one GET /features/{class}/{id} per row fills both the
  // Name and Circuit columns. Keyed by `class:id`; a row whose fetch fails just
  // shows "—" rather than failing the whole table.
  const [details, setDetails] = useState<Record<string, { name: string | null; circuit: string | null }>>({})
  // S3 multi-slack + PV inputs. `sources` are extra grid-infeed slack buses
  // beyond the armed start; `regulated` are grid-forming (PV) generators forced
  // or overridden for this run (the "Both" model -- an asset voltage_setpoint_pu
  // already makes a generator PV without any entry here). pickedNames caches the
  // human name of every picked feature (keyed `class:id`) so the chips read.
  const [sources, setSources] = useState<TraceFeatureRef[]>([])
  const [regulated, setRegulated] = useState<NodalRegulatedGen[]>([])
  const [pickedNames, setPickedNames] = useState<Record<string, string>>({})
  const fp = useFloatingPanel(760, "max-h-80")

  useEffect(() => {
    if (!result) {
      setDetails({})
      return
    }
    let cancelled = false
    const refs = [
      ...result.nodes.map((n) => ({ feature_class: n.feature_class, feature_id: n.feature_id })),
      ...result.barriers,
    ]
    async function loadDetails() {
      const entries = await Promise.all(
        refs.map(async (ref) => {
          const key = `${ref.feature_class}:${ref.feature_id}`
          try {
            const feature = await apiFetch<FeatureRecord>(`/features/${ref.feature_class}/${ref.feature_id}`)
            const circuit =
              (feature.attributes?.["circuit_ref"] as string | undefined) ??
              (feature.attributes?.["circuit_id"] as string | undefined) ??
              null
            return [key, { name: feature.name, circuit }] as const
          } catch {
            return [key, { name: null, circuit: null }] as const
          }
        }),
      )
      if (!cancelled) setDetails(Object.fromEntries(entries))
    }
    setDetails({})
    loadDetails()
    return () => {
      cancelled = true
    }
  }, [result])

  if (!armed && !result) return null

  const classNameFor = (code: string) => featureClasses?.feature_classes.find((fc) => fc.code === code)?.name ?? code
  // Features already spoken for -- the start, every extra source, every
  // regulated generator -- so neither picker offers them twice.
  const excludeKeys = new Set<string>([
    ...(armedStart ? [refKey(armedStart)] : []),
    ...sources.map(refKey),
    ...regulated.map((g) => refKey(g.feature)),
  ])
  const barrierKeys = new Set((result?.barriers ?? []).map((b) => `${b.feature_class}:${b.feature_id}`))
  const overloadedKeys = new Set((result?.overloaded ?? []).map((o) => `${o.feature_class}:${o.feature_id}`))

  async function runNodalLoadFlow() {
    const start = useNodalLoadFlowStore.getState().armedStart
    if (!start) return
    const nominalVoltageV = Number(voltageInput)
    if (!Number.isFinite(nominalVoltageV) || nominalVoltageV <= 0) {
      useToastStore.getState().pushError("Nominal voltage must be a positive number.")
      return
    }

    useNodalLoadFlowStore.getState().setLoading(true)
    try {
      const response = await apiFetch<NodalLoadFlowResult>("/analysis/nodal-load-flow", {
        method: "POST",
        body: JSON.stringify({
          start,
          nominal_voltage_v: nominalVoltageV,
          ...(solver === "newton" && { solver }),
          ...(sources.length > 0 && { sources }),
          ...(regulated.length > 0 && { regulated }),
        }),
      })
      useNodalLoadFlowStore.getState().setResult(response)
    } catch (err) {
      useNodalLoadFlowStore.getState().setLoading(false)
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not run this nodal load flow.")
    }
  }

  return (
    <div ref={fp.panelRef} style={fp.shellStyle} className={fp.shellClassName}>
      <div onPointerDown={fp.onHeaderPointerDown} className={fp.headerClassName}>
        <div className="flex min-w-0 items-center gap-1.5">
          {fp.grip}
          <p className="truncate text-sm font-medium">Nodal load flow</p>
        </div>
        <Button variant="ghost" size="icon" className="size-6 shrink-0" onClick={() => useNodalLoadFlowStore.getState().clear()}>
          <X className="size-3.5" />
          <span className="sr-only">Close</span>
        </Button>
      </div>

      {!result ? (
        <div className="flex flex-col gap-3 p-3">
          <div className="flex items-center gap-2">
            <label className="text-muted-foreground shrink-0 text-sm">Nominal voltage (V)</label>
            <input
              type="number"
              className={cn(inputClass, "w-28")}
              value={voltageInput}
              onChange={(e) => setVoltageInput(e.target.value)}
              autoFocus
            />
            <label className="text-muted-foreground shrink-0 text-sm">Solver</label>
            <select
              className={cn(inputClass, "w-32")}
              value={solver}
              onChange={(e) => setSolver(e.target.value as "gauss-seidel" | "newton")}
            >
              <option value="gauss-seidel">Gauss-Seidel</option>
              <option value="newton">Newton-Raphson</option>
            </select>
            <Button size="sm" className="ml-auto" onClick={runNodalLoadFlow} disabled={loading}>
              {loading ? "Running…" : "Run"}
            </Button>
          </div>

          {/* S3: extra grid-infeed sources (multi-slack). The armed start is
              always a slack; these add parallel infeeds that share the load. */}
          <div className="flex flex-col gap-1.5">
            <label className="text-muted-foreground text-xs font-medium">Additional sources (multi-slack)</label>
            {sources.length > 0 && (
              <div className="flex flex-wrap gap-1.5">
                {sources.map((s) => (
                  <span key={refKey(s)} className="bg-muted flex items-center gap-1 rounded px-2 py-0.5 text-xs">
                    {pickedNames[refKey(s)] ?? shortId(s.feature_id)}
                    <button
                      type="button"
                      className="text-muted-foreground hover:text-foreground"
                      onClick={() => setSources((prev) => prev.filter((p) => refKey(p) !== refKey(s)))}
                    >
                      <X className="size-3" />
                    </button>
                  </span>
                ))}
              </div>
            )}
            <FeaturePicker
              placeholder="Search a feature to add as a source…"
              exclude={excludeKeys}
              onPick={(ref, name) => {
                setSources((prev) => [...prev, ref])
                setPickedNames((prev) => ({ ...prev, [refKey(ref)]: name }))
              }}
            />
          </div>

          {/* S3: grid-forming (PV) generators. Picking one forces PV for this
              run; the per-unit voltage input overrides the setpoint (blank =
              the generator's own voltage_setpoint_pu asset field, else 1.0). */}
          <div className="flex flex-col gap-1.5">
            <label className="text-muted-foreground text-xs font-medium">Regulated generators (PV)</label>
            {regulated.length > 0 && (
              <div className="flex flex-col gap-1">
                {regulated.map((g) => (
                  <div key={refKey(g.feature)} className="bg-muted flex items-center gap-2 rounded px-2 py-1 text-xs">
                    <span className="min-w-0 flex-1 truncate">{pickedNames[refKey(g.feature)] ?? shortId(g.feature.feature_id)}</span>
                    <input
                      type="number"
                      step="0.01"
                      placeholder="V pu"
                      className={cn(inputClass, "h-6 w-16 text-xs")}
                      value={g.voltage_pu ?? ""}
                      onChange={(e) => {
                        const v = e.target.value === "" ? undefined : Number(e.target.value)
                        setRegulated((prev) =>
                          prev.map((p) => (refKey(p.feature) === refKey(g.feature) ? { ...p, voltage_pu: v } : p)),
                        )
                      }}
                    />
                    <button
                      type="button"
                      className="text-muted-foreground hover:text-foreground"
                      onClick={() => setRegulated((prev) => prev.filter((p) => refKey(p.feature) !== refKey(g.feature)))}
                    >
                      <X className="size-3" />
                    </button>
                  </div>
                ))}
              </div>
            )}
            <FeaturePicker
              placeholder="Search a generator to regulate…"
              exclude={excludeKeys}
              onPick={(ref, name) => {
                setRegulated((prev) => [...prev, { feature: ref }])
                setPickedNames((prev) => ({ ...prev, [refKey(ref)]: name }))
              }}
            />
          </div>
        </div>
      ) : (
        <>
        <div className={cn("flex flex-col overflow-hidden", fp.resized ? "min-h-0 flex-1" : "max-h-[21rem]")}>
          <div className="text-muted-foreground flex flex-wrap items-center gap-x-4 gap-y-1 border-b px-3 py-1.5 text-xs">
            <span>{formatNumber(result.nominal_voltage_v, 0)} V nominal</span>
            <span>
              {result.bus_count} bus{result.bus_count === 1 ? "" : "es"} · {result.branch_count} branch
              {result.branch_count === 1 ? "" : "es"}
              {result.slack_count > 1 ? ` · ${result.slack_count} sources` : ""}
            </span>
            <span className={cn(!result.converged && "text-destructive font-medium")}>
              {result.converged
                ? `${result.solver} · converged in ${result.iterations}`
                : `${result.solver} · did not converge (${result.iterations})`}
            </span>
            {!result.converged && <span className="text-destructive">max Δ {result.max_mismatch_pu.toExponential(1)} pu</span>}
            <Button
              variant="ghost"
              size="sm"
              className="ml-auto h-6 px-2 text-xs"
              onClick={() => useNodalLoadFlowStore.getState().rearm()}
            >
              Run again
            </Button>
          </div>
          <div className={cn("overflow-y-auto", fp.resized && "min-h-0 flex-1")}>
            <table className="w-full text-sm">
              <thead className="text-muted-foreground sticky top-0 border-b text-left text-xs">
                <tr>
                  <th className="bg-popover px-3 py-1.5 font-medium">Feature</th>
                  <th className="bg-popover px-3 py-1.5 font-medium">Name</th>
                  <th className="bg-popover px-3 py-1.5 font-medium">Circuit</th>
                  <th className="bg-popover px-3 py-1.5 font-medium">Level</th>
                  <th className="bg-popover px-3 py-1.5 font-medium">Voltage</th>
                  <th className="bg-popover px-3 py-1.5 font-medium">Current</th>
                  <th className="bg-popover px-3 py-1.5 font-medium">Loading</th>
                </tr>
              </thead>
              <tbody>
                {result.nodes.map((node) => {
                  const key = `${node.feature_class}:${node.feature_id}`
                  const overloaded = overloadedKeys.has(key)
                  const isBusNode = node.role === "bus" || node.role === "slack" || node.role === "pv"
                  return (
                    <tr
                      key={key}
                      onClick={() => useSelectionStore.getState().select(node.feature_class, node.feature_id, { focus: true })}
                      className={cn(
                        "hover:bg-accent cursor-pointer border-b last:border-b-0",
                        overloaded && "bg-destructive/10 text-destructive hover:bg-destructive/15",
                      )}
                      title={overloaded ? "Exceeds this element's own rating" : undefined}
                    >
                      <td className="px-3 py-1.5">
                        <div className="flex items-center gap-1.5">
                          <span>{classNameFor(node.feature_class)}</span>
                          {(node.role === "slack" || node.role === "pv") && (
                            <span className="text-muted-foreground rounded border px-1 text-[10px] uppercase">{node.role}</span>
                          )}
                        </div>
                        <div className="text-muted-foreground font-mono text-xs">{shortId(node.feature_id)}</div>
                      </td>
                      <td className="px-3 py-1.5">{details[key]?.name || "—"}</td>
                      <td className="text-muted-foreground px-3 py-1.5">{details[key]?.circuit || "—"}</td>
                      <td className="text-muted-foreground px-3 py-1.5 text-xs whitespace-nowrap">
                        {node.nominal_base_v > 0 ? formatVoltageLevel(node.nominal_base_v) : "—"}
                      </td>
                      <td className="px-3 py-1.5">
                        <div>{node.voltage_v > 0 ? `${formatNumber(node.voltage_v)}V` : "—"}</div>
                        {isBusNode ? (
                          <>
                            <div className="text-muted-foreground text-xs">-{formatNumber(node.voltage_drop_pct, 2)}%</div>
                            {node.supplied_kw !== undefined && (
                              <div className="text-muted-foreground text-xs">
                                {formatNumber(node.supplied_kw)} kW
                                {node.reactive_kvar !== undefined ? ` · ${formatNumber(node.reactive_kvar)} kVAR` : ""} in
                              </div>
                            )}
                          </>
                        ) : (
                          node.through_kw !== undefined && (
                            <div className="text-muted-foreground text-xs">{formatNumber(node.through_kw)} kW through</div>
                          )
                        )}
                      </td>
                      <td className="px-3 py-1.5">{node.current_amps !== undefined ? `${formatNumber(node.current_amps)}A` : "—"}</td>
                      <td className="px-3 py-1.5">{node.loading_pct !== undefined ? `${formatNumber(node.loading_pct)}%` : "—"}</td>
                    </tr>
                  )
                })}
                {barrierKeys.size > 0 &&
                  result.barriers.map((b) => (
                    <tr
                      key={`${b.feature_class}:${b.feature_id}`}
                      onClick={() => useSelectionStore.getState().select(b.feature_class, b.feature_id, { focus: true })}
                      className="bg-muted/50 text-muted-foreground hover:bg-muted cursor-pointer border-b last:border-b-0"
                      title="Nodal load flow stopped here (open device)"
                    >
                      <td className="px-3 py-1.5">
                        <div>{classNameFor(b.feature_class)}</div>
                        <div className="font-mono text-xs">{shortId(b.feature_id)}</div>
                      </td>
                      <td className="px-3 py-1.5">{details[`${b.feature_class}:${b.feature_id}`]?.name || "—"}</td>
                      <td className="px-3 py-1.5" colSpan={5}>
                        Stopped here (open device)
                      </td>
                    </tr>
                  ))}
              </tbody>
            </table>
          </div>
        </div>
        <NodalLoadFlowExplanation result={result} classNameFor={classNameFor} />
        </>
      )}
      {fp.resizeHandle}
    </div>
  )
}
