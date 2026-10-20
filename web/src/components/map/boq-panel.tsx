import { useEffect, useMemo, useRef, useState } from "react"
import { ChevronDown, Download, FileSpreadsheet, Loader2, Printer } from "lucide-react"

import { inputClass } from "@/components/map/field-utils"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import {
  DropdownMenu,
  DropdownMenuCheckboxItem,
  DropdownMenuContent,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { FloatingPanel } from "@/components/ui/floating-panel"
import { useFeatureClasses } from "@/hooks/use-feature-classes"
import {
  type AssetRegisterRequest,
  type AssetRegisterResult,
  type AssetRegisterRow,
  type BoqRequest,
  type BoqResult,
  useAssetRegister,
  usePriceBoQ,
} from "@/hooks/use-boq"
import { type TerritorySummary, useTerritories } from "@/hooks/use-territories"
import { apiFetch, ApiError } from "@/lib/api"
import { boqPdfFilename, buildBoqPdf } from "@/lib/boq-print"
import { cn } from "@/lib/utils"
import { useBoqStore } from "@/stores/boq-store"
import { useMapSelectStore } from "@/stores/map-select-store"
import { type FeatureRef, useSelectionStore } from "@/stores/selection-store"
import { useToastStore } from "@/stores/toast-store"
import { useTraceStore } from "@/stores/trace-store"

/**
 * The four BoQ scopes the panel can price. "whole"/"territory" scan whole
 * classes (optionally narrowed to a territory subtree, server-expanded); "trace"
 * and "selection" price an explicit feature set handed over as feature_refs --
 * the trace result's reached features, or the map's current selection (the
 * Select-tool's multi-pick set, else the single panel selection). The
 * engine also supports radius/bbox scopes, reachable via the API but not wired
 * into this panel yet (they need a map draw/point gesture).
 */
type ScopeMode = "whole" | "territory" | "selection" | "trace"

interface TerritoryRow {
  t: TerritorySummary
  depth: number
}

/** Depth-first indented flatten for the territory picker (copied from the export
 * dialog): roots first, each followed by its subtree, siblings by name; any
 * territory whose parent isn't in the list is treated as a root so it's never
 * orphaned out. `depth` drives the indent. */
function flattenTerritories(list: TerritorySummary[]): TerritoryRow[] {
  const byParent = new Map<string | null, TerritorySummary[]>()
  const ids = new Set(list.map((t) => t.id))
  for (const t of list) {
    const key = t.parent_id && ids.has(t.parent_id) ? t.parent_id : null
    const bucket = byParent.get(key)
    if (bucket) bucket.push(t)
    else byParent.set(key, [t])
  }
  for (const bucket of byParent.values()) bucket.sort((a, b) => a.name.localeCompare(b.name))

  const out: TerritoryRow[] = []
  const walk = (parent: string | null, depth: number) => {
    for (const t of byParent.get(parent) ?? []) {
      out.push({ t, depth })
      walk(t.id, depth + 1)
    }
  }
  walk(null, 0)
  return out
}

/**
 * The Bill-of-Quantities panel (Slice 1). Pick a scope -- the whole network (by
 * class), a territory subtree, the current map selection, or the last trace
 * result -- pick a display currency, and price it: POST /boq counts the scoped
 * quantities and multiplies by catalog rates (material cost_per_km for lines,
 * cost_rates 'each' for discrete assets), returning a grouped, totalled schedule.
 * Priced lines subtotal per class under a grand total; quantities with no rate
 * surface in a separate "Unpriced" list so a costing gap is visible, and the
 * whole schedule exports client-side as a PDF sheet (lib/boq-print) or CSV.
 * Always mounted, self-gates on the store's `open`.
 */
export function BoqPanel() {
  const open = useBoqStore((s) => s.open)
  // The panel produces one of two outputs from the same scope: the priced,
  // rolled-up schedule ("schedule") or the per-feature itemised asset register
  // ("register"). The scope controls are shared; only the action, result area and
  // valuation toggle differ.
  const [output, setOutput] = useState<"schedule" | "register">("schedule")
  const [mode, setMode] = useState<ScopeMode>("whole")
  const [selectedClasses, setSelectedClasses] = useState<Record<string, boolean>>({})
  const [territoryId, setTerritoryId] = useState("")
  const [displayCurrency, setDisplayCurrency] = useState("")
  const [result, setResult] = useState<BoqResult | null>(null)
  // Asset-register output + its "value the assets too" toggle (off by default:
  // the register is a plain inventory unless costs are asked for).
  const [includeValuation, setIncludeValuation] = useState(false)
  const [registerResult, setRegisterResult] = useState<AssetRegisterResult | null>(null)
  // The server-built multi-sheet XLSX is a streamed download, not a cached
  // result -- just a busy flag for the button.
  const [xlsxBusy, setXlsxBusy] = useState(false)
  // The in-app PDF preview: a blob URL for the built sheet plus its download
  // name, or null when the dialog is closed. Revoked on close so the blob is
  // never leaked.
  const [preview, setPreview] = useState<{ url: string; filename: string } | null>(null)

  const { data: classesData } = useFeatureClasses()
  const { data: territoriesData } = useTerritories()
  const price = usePriceBoQ()
  const register = useAssetRegister()

  const territoryRows = useMemo(
    () => flattenTerritories(territoriesData?.territories ?? []),
    [territoriesData],
  )
  const classes = classesData?.feature_classes ?? []
  const checkedClasses = useMemo(
    () => Object.keys(selectedClasses).filter((c) => selectedClasses[c]),
    [selectedClasses],
  )

  const traceRefs = useTraceStore((s) => s.result?.features)
  // The "Current selection" scope prices the Select-tool's multi-pick set when it
  // has any members (the marquee/box highlight -- useMapSelectStore), else falls
  // back to the single panel-opening selection (useSelectionStore). Previously it
  // was wired only to the single-feature store, so a marquee selection left the
  // scope disabled even though features were highlighted.
  const singleSelected = useSelectionStore((s) => s.selected)
  const multiSelected = useMapSelectStore((s) => s.selected)
  const selectionRefs: FeatureRef[] =
    multiSelected.length > 0 ? multiSelected : singleSelected ? [singleSelected] : []

  // The panel is always mounted (it self-gates on `open`), so without this its
  // priced `result` and PDF preview would survive a close and the next open would
  // show a stale estimate. Clear them whenever the panel closes, so every open
  // starts from a clean slate.
  useEffect(() => {
    if (!open) {
      setResult(null)
      setRegisterResult(null)
      setPreview((prev) => {
        if (prev) URL.revokeObjectURL(prev.url)
        return null
      })
    }
  }, [open])

  if (!open) return null

  // The scope half of a request -- shared by the priced schedule and the asset
  // register. Returns null when the chosen scope has nothing to resolve (no
  // territory / no selection / no trace) so the action button can be disabled.
  const buildScope = (): Pick<BoqRequest, "feature_classes" | "scope" | "feature_refs"> | null => {
    switch (mode) {
      case "whole":
        // Empty class set = every class (the engine's default).
        return checkedClasses.length > 0 ? { feature_classes: checkedClasses } : {}
      case "territory":
        if (!territoryId) return null
        return {
          ...(checkedClasses.length > 0 ? { feature_classes: checkedClasses } : {}),
          scope: { kind: "territory", territory_id: territoryId },
        }
      case "selection":
        if (selectionRefs.length === 0) return null
        return { feature_refs: selectionRefs.map((r) => ({ class: r.featureClassCode, id: r.featureId })) }
      case "trace":
        if (!traceRefs || traceRefs.length === 0) return null
        return { feature_refs: traceRefs.map((f) => ({ class: f.feature_class, id: f.feature_id })) }
    }
  }

  // Build the priced-schedule request for the current form, or null when the
  // scope has nothing to price.
  const buildRequest = (currency: string): BoqRequest | null => {
    const scope = buildScope()
    if (!scope) return null
    const base: BoqRequest = { ...scope }
    if (currency) base.display_currency = currency
    return base
  }

  // Build the asset-register request. Valuation (and so the currency) only ride
  // along when the toggle is on.
  const buildRegisterRequest = (currency: string): AssetRegisterRequest | null => {
    const scope = buildScope()
    if (!scope) return null
    const base: AssetRegisterRequest = { ...scope }
    if (includeValuation) {
      base.include_valuation = true
      if (currency) base.display_currency = currency
    }
    return base
  }

  const runPrice = (currency: string) => {
    const req = buildRequest(currency)
    if (!req) return
    price.mutate(req, {
      onSuccess: (res) => {
        setResult(res)
        // Adopt the currency the server actually priced in (base when the
        // requested one was blank/unknown) so the picker stays in sync.
        setDisplayCurrency(res.currency)
      },
      onError: (err) =>
        useToastStore
          .getState()
          .pushError(err instanceof ApiError ? err.message : "Could not price this scope."),
    })
  }

  const runRegister = (currency: string) => {
    const req = buildRegisterRequest(currency)
    if (!req) return
    register.mutate(req, {
      onSuccess: (res) => {
        setRegisterResult(res)
        if (res.currency) setDisplayCurrency(res.currency)
      },
      onError: (err) =>
        useToastStore
          .getState()
          .pushError(err instanceof ApiError ? err.message : "Could not build the register."),
    })
  }

  // Build the multi-sheet XLSX (one sheet per class, full per-class attribute
  // columns) for the current register scope as an async job: enqueue it, poll
  // until the worker has built it and stored it in RustFS, then download it
  // straight from the store via the presigned URL the job hands back. Uses the
  // same scope/valuation as the on-screen register.
  const downloadRegisterXlsx = async () => {
    const req = buildRegisterRequest(displayCurrency)
    if (!req) return
    setXlsxBusy(true)
    try {
      const { job_id } = await apiFetch<{ job_id: string }>("/boq/asset-register.xlsx", {
        method: "POST",
        body: JSON.stringify(req),
      })
      const deadline = Date.now() + 5 * 60 * 1000 // give up after 5 minutes
      for (;;) {
        await new Promise((r) => setTimeout(r, 1500))
        const job = await apiFetch<{
          status: string
          download_url?: string
          filename?: string
          error?: string
        }>(`/boq/asset-register/jobs/${job_id}`)
        if (job.status === "succeeded" && job.download_url) {
          const a = document.createElement("a")
          a.href = job.download_url
          a.download = job.filename || "asset-register.xlsx"
          document.body.appendChild(a)
          a.click()
          document.body.removeChild(a)
          break
        }
        if (job.status === "failed" || job.status === "cancelled") {
          throw new ApiError(500, job.error || "The register export failed.")
        }
        if (Date.now() > deadline) {
          throw new ApiError(504, "The register is taking longer than expected — check back shortly.")
        }
      }
    } catch (err) {
      useToastStore
        .getState()
        .pushError(err instanceof ApiError ? err.message : "Could not build the spreadsheet.")
    } finally {
      setXlsxBusy(false)
    }
  }

  const canRun = buildScope() !== null
  const isPending = output === "schedule" ? price.isPending : register.isPending

  // The currency picker is driven by whichever result is showing. In register
  // mode it only matters when valuation is on (otherwise there's no money).
  const currencyOptions =
    output === "schedule" ? (result?.currencies ?? []) : (registerResult?.currencies ?? [])
  const showCurrency =
    output === "schedule" ? currencyOptions.length > 0 : includeValuation && currencyOptions.length > 0

  // A human-readable description of the priced scope for the PDF title block,
  // mirroring the scope select's own wording.
  const scopeLabel = (): string => {
    switch (mode) {
      case "whole":
        return checkedClasses.length === 0
          ? "Whole network — all classes"
          : `Whole network — ${checkedClasses.length} class${checkedClasses.length === 1 ? "" : "es"}`
      case "territory": {
        const t = territoryRows.find((r) => r.t.id === territoryId)?.t
        return t ? `Territory — ${t.name} and beneath` : "Territory"
      }
      case "selection":
        return selectionRefs.length > 0
          ? `Current selection — ${selectionRefs.length} feature${selectionRefs.length === 1 ? "" : "s"}`
          : "Current selection"
      case "trace":
        return `Trace result — ${traceRefs?.length ?? 0} features`
    }
  }

  // Build the schedule PDF and open it in the in-app preview dialog. Revokes any
  // prior blob URL first so repeated clicks never leak one.
  const openPreview = () => {
    if (!result) return
    const url = buildBoqPdf(result, { scopeLabel: scopeLabel() }).output("bloburl") as unknown as string
    setPreview((prev) => {
      if (prev) URL.revokeObjectURL(prev.url)
      return { url, filename: boqPdfFilename(result) }
    })
  }

  const closePreview = () =>
    setPreview((prev) => {
      if (prev) URL.revokeObjectURL(prev.url)
      return null
    })

  return (
    <>
    <FloatingPanel
      title="Bill of Quantities"
      onClose={() => useBoqStore.getState().closePanel()}
      initialRect={{ x: window.innerWidth - 560, y: 72, width: 520, height: 520 }}
      minWidth={380}
      minHeight={280}
    >
      <div className="flex h-full flex-col">
        {/* Controls */}
        <div className="space-y-2.5 border-b p-3">
          {/* Output mode: the same scope, as a priced schedule or an itemised
              per-feature asset register. */}
          <div className="bg-muted/60 grid grid-cols-2 gap-1 rounded-md p-0.5">
            <button
              type="button"
              className={cn(
                "rounded px-2 py-1 text-xs font-medium transition-colors",
                output === "schedule" ? "bg-background shadow-sm" : "text-muted-foreground hover:text-foreground",
              )}
              onClick={() => setOutput("schedule")}
            >
              Priced schedule
            </button>
            <button
              type="button"
              className={cn(
                "flex items-center justify-center gap-1 rounded px-2 py-1 text-xs font-medium transition-colors",
                output === "register" ? "bg-background shadow-sm" : "text-muted-foreground hover:text-foreground",
              )}
              onClick={() => setOutput("register")}
            >
              <FileSpreadsheet className="size-3.5" /> Asset register
            </button>
          </div>

          <div className="space-y-1">
            <label htmlFor="boq-scope" className="text-sm font-medium">
              Scope
            </label>
            <select
              id="boq-scope"
              className={inputClass}
              value={mode}
              onChange={(e) => setMode(e.target.value as ScopeMode)}
            >
              <option value="whole">Whole network — every feature of each chosen class</option>
              <option value="territory" disabled={territoryRows.length === 0}>
                Territory {territoryRows.length === 0 ? "— none defined" : "— a territory and beneath"}
              </option>
              <option value="selection" disabled={selectionRefs.length === 0}>
                Current selection{" "}
                {selectionRefs.length > 0
                  ? `— ${selectionRefs.length} feature${selectionRefs.length === 1 ? "" : "s"}`
                  : "— nothing selected"}
              </option>
              <option value="trace" disabled={!traceRefs || traceRefs.length === 0}>
                Trace result{" "}
                {traceRefs && traceRefs.length > 0 ? `— ${traceRefs.length} features` : "— run a trace first"}
              </option>
            </select>
          </div>

          {(mode === "whole" || mode === "territory") && (
            <>
              {mode === "territory" && (
                <div className="space-y-1">
                  <label htmlFor="boq-territory" className="text-sm font-medium">
                    Territory
                  </label>
                  <select
                    id="boq-territory"
                    className={inputClass}
                    value={territoryId}
                    onChange={(e) => setTerritoryId(e.target.value)}
                  >
                    <option value="">Select a territory…</option>
                    {territoryRows.map(({ t, depth }) => (
                      <option key={t.id} value={t.id}>
                        {" ".repeat(depth * 2)}
                        {t.name} ({t.level_name})
                      </option>
                    ))}
                  </select>
                </div>
              )}

              <div className="space-y-1">
                <label className="text-sm font-medium">Classes</label>
                <DropdownMenu>
                  <DropdownMenuTrigger asChild>
                    <button
                      type="button"
                      className={cn(
                        inputClass,
                        "flex items-center justify-between gap-2 text-left",
                      )}
                    >
                      <span
                        className={cn("truncate", checkedClasses.length === 0 && "text-muted-foreground")}
                      >
                        {checkedClasses.length === 0
                          ? "All classes"
                          : `${checkedClasses.length} class${checkedClasses.length === 1 ? "" : "es"} selected`}
                      </span>
                      <ChevronDown className="size-4 shrink-0 opacity-50" />
                    </button>
                  </DropdownMenuTrigger>
                  <DropdownMenuContent
                    align="start"
                    className="max-h-64 w-[var(--radix-dropdown-menu-trigger-width)] overflow-y-auto"
                  >
                    {classes.map((c) => (
                      <DropdownMenuCheckboxItem
                        key={c.code}
                        checked={Boolean(selectedClasses[c.code])}
                        onCheckedChange={(v) =>
                          setSelectedClasses((s) => ({ ...s, [c.code]: v === true }))
                        }
                        onSelect={(e) => e.preventDefault()}
                      >
                        <span className="flex-1 truncate">{c.name}</span>
                      </DropdownMenuCheckboxItem>
                    ))}
                  </DropdownMenuContent>
                </DropdownMenu>
                <p className="text-muted-foreground text-xs">
                  Leave empty to {output === "schedule" ? "price" : "list"} every class.
                </p>
              </div>
            </>
          )}

          {output === "register" && (
            <label className="flex cursor-pointer items-center gap-2 text-sm">
              <input
                type="checkbox"
                className="size-4 accent-current"
                checked={includeValuation}
                onChange={(e) => setIncludeValuation(e.target.checked)}
              />
              <span>Include valuation</span>
              <span className="text-muted-foreground text-xs">— each asset's catalog cost &amp; value</span>
            </label>
          )}

          <div className="flex items-end gap-2">
            {showCurrency && (
              <div className="min-w-0 flex-1 space-y-1">
                <label htmlFor="boq-currency" className="text-sm font-medium">
                  Currency
                </label>
                <select
                  id="boq-currency"
                  className={inputClass}
                  value={displayCurrency}
                  onChange={(e) => {
                    const next = e.target.value
                    setDisplayCurrency(next)
                    // Re-run in the new currency straightaway (conversion is
                    // server-side off the same base figures).
                    if (output === "schedule") runPrice(next)
                    else runRegister(next)
                  }}
                  disabled={isPending}
                >
                  {currencyOptions.map((c) => (
                    <option key={c.code} value={c.code}>
                      {c.code} — {c.name}
                      {c.is_base ? " (base)" : ""}
                    </option>
                  ))}
                </select>
              </div>
            )}
            <Button
              size="sm"
              className="shadow-sm"
              disabled={!canRun || isPending}
              onClick={() => (output === "schedule" ? runPrice(displayCurrency) : runRegister(displayCurrency))}
            >
              {isPending ? <Loader2 className="size-3.5 animate-spin" /> : null}
              {output === "schedule" ? "Price" : "Build register"}
            </Button>
            {output === "schedule" && result && (
              <>
                <Button
                  size="sm"
                  variant="secondary"
                  className="shadow-sm"
                  onClick={openPreview}
                  title="Preview this schedule as a printable PDF"
                >
                  <Printer className="size-3.5" /> PDF
                </Button>
                <Button
                  size="sm"
                  variant="secondary"
                  className="shadow-sm"
                  onClick={() => downloadCsv(result)}
                  title="Download this schedule as CSV"
                >
                  <Download className="size-3.5" /> CSV
                </Button>
              </>
            )}
            {output === "register" && registerResult && registerResult.rows.length > 0 && (
              <>
                <Button
                  size="sm"
                  variant="secondary"
                  className="shadow-sm"
                  onClick={() => downloadRegisterCsv(registerResult)}
                  title="Download this asset register as CSV (flat, one sheet)"
                >
                  <Download className="size-3.5" /> CSV
                </Button>
                <Button
                  size="sm"
                  variant="secondary"
                  className="shadow-sm"
                  disabled={xlsxBusy}
                  onClick={downloadRegisterXlsx}
                  title="Build an Excel workbook (one sheet per class, full attribute columns) in the background; it downloads when ready"
                >
                  {xlsxBusy ? (
                    <Loader2 className="size-3.5 animate-spin" />
                  ) : (
                    <FileSpreadsheet className="size-3.5" />
                  )}{" "}
                  {xlsxBusy ? "Building…" : "XLSX"}
                </Button>
              </>
            )}
          </div>
        </div>

        {/* Results */}
        <div className="min-h-0 flex-1 overflow-auto p-3">
          {output === "schedule" ? (
            price.isPending && !result ? (
              <p className="text-muted-foreground text-sm">Pricing…</p>
            ) : !result ? (
              <p className="text-muted-foreground text-sm">
                Choose a scope and press Price to build the schedule.
              </p>
            ) : (
              <BoqSchedule result={result} />
            )
          ) : register.isPending && !registerResult ? (
            <p className="text-muted-foreground text-sm">Building register…</p>
          ) : !registerResult ? (
            <p className="text-muted-foreground text-sm">
              Choose a scope and press Build register to list the assets.
            </p>
          ) : (
            <AssetRegisterView result={registerResult} />
          )}
        </div>
      </div>
    </FloatingPanel>
    <BoqPreviewDialog preview={preview} onClose={closePreview} />
    </>
  )
}

/** The in-app PDF preview: the built schedule embedded in an <iframe> (the
 * browser's own PDF viewer), with explicit Print and Download controls so the
 * sheet never leaves the app to be seen. The iframe's viewer carries its own
 * toolbar too; the buttons are the reliable path. */
function BoqPreviewDialog({
  preview,
  onClose,
}: {
  preview: { url: string; filename: string } | null
  onClose: () => void
}) {
  const frameRef = useRef<HTMLIFrameElement>(null)

  return (
    <Dialog open={preview !== null} onOpenChange={(o) => !o && onClose()}>
      {preview && (
        <DialogContent className="flex h-[88vh] w-[min(94vw,900px)] max-w-none flex-col gap-3 p-4">
          <DialogHeader>
            <DialogTitle>Bill of Quantities — preview</DialogTitle>
            <DialogDescription>
              Review the schedule, then print or download it.
            </DialogDescription>
          </DialogHeader>
          <iframe
            ref={frameRef}
            src={preview.url}
            title="Bill of Quantities PDF preview"
            className="min-h-0 w-full flex-1 rounded border bg-white"
          />
          <DialogFooter>
            <DialogClose asChild>
              <Button variant="ghost" size="sm">
                Close
              </Button>
            </DialogClose>
            <a href={preview.url} download={preview.filename}>
              <Button variant="secondary" size="sm" className="shadow-sm">
                <Download className="size-3.5" /> Download
              </Button>
            </a>
            <Button
              size="sm"
              className="shadow-sm"
              onClick={() => frameRef.current?.contentWindow?.print()}
            >
              <Printer className="size-3.5" /> Print
            </Button>
          </DialogFooter>
        </DialogContent>
      )}
    </Dialog>
  )
}

/** The priced schedule: lines grouped by class with per-class subtotals, a grand
 * total, then the unpriced buckets. */
function BoqSchedule({ result }: { result: BoqResult }) {
  const groups = useMemo(() => {
    const byClass = new Map<string, { name: string; lines: BoqResult["lines"]; subtotal: number }>()
    for (const l of result.lines) {
      const g = byClass.get(l.feature_class) ?? {
        name: l.feature_class_name,
        lines: [],
        subtotal: 0,
      }
      g.lines.push(l)
      g.subtotal += l.line_total
      byClass.set(l.feature_class, g)
    }
    return [...byClass.values()]
  }, [result.lines])

  const money = (n: number) =>
    `${n.toLocaleString(undefined, { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`

  const qty = (n: number) =>
    n.toLocaleString(undefined, { maximumFractionDigits: 3 })

  return (
    <div className="space-y-4 text-sm">
      {result.lines.length === 0 ? (
        <p className="text-muted-foreground">No priced quantities in this scope.</p>
      ) : (
        <div className="overflow-hidden rounded-md border">
          <table className="w-full border-collapse">
            <thead className="bg-muted/50 text-muted-foreground text-xs">
              <tr>
                <th className="px-2 py-1.5 text-left font-medium">Item</th>
                <th className="px-2 py-1.5 text-right font-medium">Qty</th>
                <th className="px-2 py-1.5 text-left font-medium">Unit</th>
                <th className="px-2 py-1.5 text-right font-medium">Rate</th>
                <th className="px-2 py-1.5 text-right font-medium">Total</th>
              </tr>
            </thead>
            <tbody>
              {groups.map((g) => (
                <ClassGroup key={g.name} g={g} money={money} qty={qty} />
              ))}
            </tbody>
            <tfoot>
              <tr className="border-t-2 font-semibold">
                <td className="px-2 py-1.5" colSpan={4}>
                  Grand total ({result.currency})
                </td>
                <td className="px-2 py-1.5 text-right tabular-nums">{money(result.total)}</td>
              </tr>
            </tfoot>
          </table>
        </div>
      )}

      <p className="text-muted-foreground text-xs">
        {result.priced_count} priced line{result.priced_count === 1 ? "" : "s"} · totals in{" "}
        {result.currency} ({result.currency_name})
        {result.currency !== result.base_currency && <> · base {result.base_currency}</>}
      </p>

      {result.unpriced.length > 0 && (
        <div className="space-y-1.5">
          <h4 className="text-amber-700 text-sm font-semibold dark:text-amber-500">
            Unpriced ({result.unpriced.length})
          </h4>
          <p className="text-muted-foreground text-xs">
            Counted but no catalog rate applied — set a rate to include these.
          </p>
          <ul className="space-y-1">
            {result.unpriced.map((u, i) => (
              <li key={i} className="rounded border border-amber-200 bg-amber-50 p-2 text-xs dark:border-amber-900/50 dark:bg-amber-950/30">
                <div className="flex items-center justify-between gap-2">
                  <span className="font-medium">
                    {u.feature_class_name}
                    {u.subtype_name ? ` · ${u.subtype_name}` : ""}
                    {u.material_code ? ` · ${u.material_code}` : ""}
                  </span>
                  <span className="tabular-nums whitespace-nowrap">
                    {qty(u.quantity)} {u.unit}
                  </span>
                </div>
                <p className="text-muted-foreground mt-0.5">{u.reason}</p>
              </li>
            ))}
          </ul>
        </div>
      )}
    </div>
  )
}

function ClassGroup({
  g,
  money,
  qty,
}: {
  g: { name: string; lines: BoqResult["lines"]; subtotal: number }
  money: (n: number) => string
  qty: (n: number) => string
}) {
  return (
    <>
      <tr className="bg-muted/30">
        <td className="px-2 py-1 font-medium" colSpan={5}>
          {g.name}
        </td>
      </tr>
      {g.lines.map((l, i) => (
        <tr key={i} className="border-t">
          <td className="px-2 py-1 pl-4">
            {l.subtype_name || l.material_name || l.material_code ? (
              <span>
                {[l.subtype_name, l.material_name || l.material_code].filter(Boolean).join(" · ")}
              </span>
            ) : (
              <span className="text-muted-foreground">(all)</span>
            )}
          </td>
          <td className="px-2 py-1 text-right tabular-nums">{qty(l.quantity)}</td>
          <td className="px-2 py-1">{l.unit}</td>
          <td className="px-2 py-1 text-right tabular-nums">{money(l.unit_rate)}</td>
          <td className="px-2 py-1 text-right tabular-nums">{money(l.line_total)}</td>
        </tr>
      ))}
      <tr className="border-t bg-muted/20">
        <td className="px-2 py-1 pl-4 text-right text-xs font-medium text-muted-foreground" colSpan={4}>
          Subtotal
        </td>
        <td className="px-2 py-1 text-right text-xs font-semibold tabular-nums">{money(g.subtotal)}</td>
      </tr>
    </>
  )
}

/** Builds a CSV of the schedule and triggers a client-side download (blob +
 * anchor, copied from the query panel). Priced lines first, then unpriced. */
function downloadCsv(result: BoqResult) {
  const esc = (v: string | number | undefined) => {
    const s = v == null ? "" : String(v)
    return /[",\n]/.test(s) ? `"${s.replace(/"/g, '""')}"` : s
  }
  const rows: string[] = []
  rows.push(
    ["Class", "Subtype", "Material", "Basis", "Quantity", "Unit", `Rate (${result.currency})`, `Total (${result.currency})`, "Status"]
      .map(esc)
      .join(","),
  )
  for (const l of result.lines) {
    rows.push(
      [
        l.feature_class_name,
        l.subtype_name ?? "",
        l.material_name || l.material_code || "",
        l.unit_basis,
        l.quantity,
        l.unit,
        l.unit_rate,
        l.line_total,
        "priced",
      ]
        .map(esc)
        .join(","),
    )
  }
  for (const u of result.unpriced) {
    rows.push(
      [
        u.feature_class_name,
        u.subtype_name ?? "",
        u.material_code ?? "",
        u.unit_basis,
        u.quantity,
        u.unit,
        "",
        "",
        `unpriced: ${u.reason}`,
      ]
        .map(esc)
        .join(","),
    )
  }
  rows.push(["Grand total", "", "", "", "", "", "", result.total, ""].map(esc).join(","))

  const csv = rows.join("\n")
  const blob = new Blob([csv], { type: "text/csv;charset=utf-8" })
  const url = URL.createObjectURL(blob)
  const a = document.createElement("a")
  a.href = url
  a.download = `bill-of-quantities-${result.currency}.csv`
  document.body.appendChild(a)
  a.click()
  document.body.removeChild(a)
  URL.revokeObjectURL(url)
}

/** The itemised asset register: one row per feature, grouped by class, with a
 * compact in-panel table (the full column set ships in the CSV). When valuation
 * was requested each asset carries a value and the footer a total. */
function AssetRegisterView({ result }: { result: AssetRegisterResult }) {
  const valued = result.include_valuation
  const money = (n: number | undefined) =>
    n == null ? "" : n.toLocaleString(undefined, { minimumFractionDigits: 2, maximumFractionDigits: 2 })
  const qty = (n: number) => n.toLocaleString(undefined, { maximumFractionDigits: 3 })
  // A point's location, or a line's two endpoints (start → end).
  const loc = (r: AssetRegisterRow) => {
    if (r.latitude == null || r.longitude == null) return "—"
    const from = `${r.latitude.toFixed(5)}, ${r.longitude.toFixed(5)}`
    if (r.to_latitude != null && r.to_longitude != null)
      return `${from} → ${r.to_latitude.toFixed(5)}, ${r.to_longitude.toFixed(5)}`
    return from
  }

  return (
    <div className="space-y-3 text-sm">
      {result.truncated && (
        <p className="rounded border border-amber-200 bg-amber-50 p-2 text-xs text-amber-800 dark:border-amber-900/50 dark:bg-amber-950/30 dark:text-amber-300">
          Capped at {result.count.toLocaleString()} assets — narrow the scope to see them all.
        </p>
      )}
      {result.rows.length === 0 ? (
        <p className="text-muted-foreground">No assets in this scope.</p>
      ) : (
        <div className="overflow-hidden rounded-md border">
          <table className="w-full border-collapse">
            <thead className="bg-muted/50 text-muted-foreground text-xs">
              <tr>
                <th className="px-2 py-1.5 text-left font-medium">Asset</th>
                <th className="px-2 py-1.5 text-left font-medium">Type / material</th>
                <th className="px-2 py-1.5 text-right font-medium">Qty</th>
                <th className="px-2 py-1.5 text-left font-medium">Location</th>
                {valued && <th className="px-2 py-1.5 text-right font-medium">Value</th>}
              </tr>
            </thead>
            <tbody>
              {result.rows.map((r) => (
                <tr key={`${r.feature_class}:${r.feature_id}`} className="border-t align-top">
                  <td className="px-2 py-1">
                    <div className="font-medium">{r.name || r.feature_id.slice(0, 8)}</div>
                    <div className="text-muted-foreground text-xs">{r.feature_class_name}</div>
                  </td>
                  <td className="px-2 py-1">
                    {[r.subtype_name, r.material_name || r.material_code].filter(Boolean).join(" · ") || (
                      <span className="text-muted-foreground">—</span>
                    )}
                  </td>
                  <td className="px-2 py-1 text-right tabular-nums whitespace-nowrap">
                    {qty(r.quantity)} {r.unit}
                  </td>
                  <td className="px-2 py-1 text-muted-foreground text-xs tabular-nums">{loc(r)}</td>
                  {valued && (
                    <td className="px-2 py-1 text-right tabular-nums">
                      {r.priced_source === "unpriced" ? (
                        <span className="text-amber-700 dark:text-amber-500" title="No catalog rate">
                          —
                        </span>
                      ) : (
                        <span title={r.priced_source === "override" ? "Hand-set override" : "Catalog rate"}>
                          {money(r.value)}
                        </span>
                      )}
                    </td>
                  )}
                </tr>
              ))}
            </tbody>
            {valued && result.total_value != null && (
              <tfoot>
                <tr className="border-t-2 font-semibold">
                  <td className="px-2 py-1.5" colSpan={4}>
                    Total value ({result.currency})
                  </td>
                  <td className="px-2 py-1.5 text-right tabular-nums">{money(result.total_value)}</td>
                </tr>
              </tfoot>
            )}
          </table>
        </div>
      )}

      <p className="text-muted-foreground text-xs">
        {result.count.toLocaleString()} asset{result.count === 1 ? "" : "s"}
        {valued && result.currency ? ` · valued in ${result.currency} (${result.currency_name})` : ""}
        {" · CSV = flat sheet; XLSX = one sheet per class with full attributes."}
      </p>
    </div>
  )
}

/** Builds a CSV of the asset register (the full flat column set) and triggers a
 * client-side download. Valuation columns ride along only when they were
 * requested. */
function downloadRegisterCsv(result: AssetRegisterResult) {
  const esc = (v: string | number | undefined) => {
    const s = v == null ? "" : String(v)
    return /[",\n]/.test(s) ? `"${s.replace(/"/g, '""')}"` : s
  }
  const valued = result.include_valuation
  const header = [
    "Asset ID",
    "Class",
    "Subtype",
    "Name",
    "Status",
    "Material",
    "Quantity",
    "Unit",
    "Longitude",
    "Latitude",
    "To longitude",
    "To latitude",
    "Territory",
    "Circuit",
    "Ownership",
    "External refs",
    "Created",
  ]
  if (valued) header.push(`Unit cost (${result.currency})`, `Value (${result.currency})`, "Priced")

  const rows: string[] = [header.map(esc).join(",")]
  for (const r of result.rows) {
    const cells: (string | number | undefined)[] = [
      r.feature_id,
      r.feature_class_name,
      r.subtype_name ?? "",
      r.name ?? "",
      r.status ?? "",
      r.material_name || r.material_code || "",
      r.quantity,
      r.unit,
      r.longitude ?? "",
      r.latitude ?? "",
      r.to_longitude ?? "",
      r.to_latitude ?? "",
      r.territory_name || r.territory_id || "",
      r.circuit_id ?? "",
      r.ownership ?? "",
      r.external_ref ?? "",
      r.created_at ?? "",
    ]
    if (valued) cells.push(r.unit_rate ?? "", r.value ?? "", r.priced_source ?? "")
    rows.push(cells.map(esc).join(","))
  }
  if (valued && result.total_value != null) {
    const totalRow: (string | number)[] = new Array(header.length).fill("")
    totalRow[0] = "Total value"
    totalRow[header.length - 2] = result.total_value
    rows.push(totalRow.map(esc).join(","))
  }

  const csv = rows.join("\n")
  const blob = new Blob([csv], { type: "text/csv;charset=utf-8" })
  const url = URL.createObjectURL(blob)
  const a = document.createElement("a")
  a.href = url
  a.download = `asset-register${valued && result.currency ? `-${result.currency}` : ""}.csv`
  document.body.appendChild(a)
  a.click()
  document.body.removeChild(a)
  URL.revokeObjectURL(url)
}
