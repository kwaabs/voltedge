import { useEffect, useMemo, useState } from "react"
import {
  ChevronLeft,
  ChevronRight,
  Loader2,
  Plus,
  Printer,
  RefreshCw,
  Search,
  Star,
  Tags,
  Trash2,
} from "lucide-react"

import { FormDialog } from "@/components/admin/form-dialog"
import { inputClass } from "@/components/map/field-utils"
import { TagPrintSheet, type PrintTag } from "@/components/map/tag-print-sheet"
import { Button } from "@/components/ui/button"
import { usePermission } from "@/hooks/use-auth"
import { useFeatureClasses } from "@/hooks/use-feature-classes"
import {
  type TagRow,
  type TagSource,
  type TagStatus,
  useMarkManyPrinted,
  useRegisterTag,
  useReissueTag,
  useSetPrimaryTag,
  useTagList,
  useTagStats,
  useVoidTag,
} from "@/hooks/use-asset-tags"
import { ApiError } from "@/lib/api"
import { useToastStore } from "@/stores/toast-store"

const PAGE_SIZE = 50

const labelClass = "text-xs font-medium"

/** One KPI in the stats strip. */
function StatChip({ label, value, tone }: { label: string; value: number; tone?: string }) {
  return (
    <div className="bg-card flex min-w-[7rem] flex-col rounded-lg border px-3 py-2">
      <span className={`text-lg font-semibold tabular-nums ${tone ?? ""}`}>{value.toLocaleString()}</span>
      <span className="text-muted-foreground text-xs">{label}</span>
    </div>
  )
}

/** Lifecycle-state badge, coloured to match the tone the stats strip uses. */
function StatusBadge({ status }: { status: TagStatus }) {
  const tone =
    status === "active"
      ? "bg-emerald-500/15 text-emerald-700 dark:text-emerald-400"
      : status === "void"
        ? "bg-destructive/15 text-destructive"
        : "bg-amber-500/15 text-amber-700 dark:text-amber-400"
  return <span className={`rounded-md px-1.5 py-0.5 text-xs font-medium ${tone}`}>{status}</span>
}

/**
 * The asset-tag management console (Admin → Asset Tags). Where /t is the
 * lightweight scan-resolve interstitial (one code in, one feature out), this is
 * the operator's whole-population view: a KPI strip, a filterable/paged table of
 * every tag the caller may see (territory- and draft-scoped on the server, the
 * same gate a feature read applies), inline lifecycle actions per row (promote /
 * void / reissue / print), a bulk label print, and a register-legacy-number form.
 *
 * Reads open on tags:read (the route gate); the lifecycle + register + print-count
 * writes are tags:write, so those controls self-gate to match the server.
 */
export function AdminAssetTagsPage() {
  const canWrite = usePermission()("tags:write")

  // --- filters (search is debounced so the list isn't refetched per keystroke) ---
  const [searchInput, setSearchInput] = useState("")
  const [search, setSearch] = useState("")
  const [status, setStatus] = useState<TagStatus | "">("")
  const [source, setSource] = useState<TagSource | "">("")
  const [classCode, setClassCode] = useState("")
  const [primaryOnly, setPrimaryOnly] = useState(false)
  const [offset, setOffset] = useState(0)

  useEffect(() => {
    const id = setTimeout(() => setSearch(searchInput.trim()), 300)
    return () => clearTimeout(id)
  }, [searchInput])

  // Any filter change resets to the first page -- paging an old offset into a
  // smaller filtered result would land on an empty page.
  useEffect(() => setOffset(0), [search, status, source, classCode, primaryOnly])

  const stats = useTagStats()
  const list = useTagList({
    search,
    status,
    source,
    feature_class_code: classCode,
    primary_only: primaryOnly,
    limit: PAGE_SIZE,
    offset,
  })
  const classes = useFeatureClasses()

  const classNameOf = useMemo(() => {
    const m = new Map<string, string>()
    for (const c of classes.data?.feature_classes ?? []) m.set(c.code, c.name)
    return m
  }, [classes.data])

  // --- selection for bulk print ---
  const [selected, setSelected] = useState<Set<string>>(new Set())
  const rows = list.data?.rows ?? []
  const allOnPageSelected = rows.length > 0 && rows.every((r) => selected.has(r.tag_code))

  function toggleOne(code: string, on: boolean) {
    setSelected((cur) => {
      const next = new Set(cur)
      if (on) next.add(code)
      else next.delete(code)
      return next
    })
  }
  function togglePage(on: boolean) {
    setSelected((cur) => {
      const next = new Set(cur)
      for (const r of rows) {
        if (on) next.add(r.tag_code)
        else next.delete(r.tag_code)
      }
      return next
    })
  }

  // --- print sheet (single row or bulk) ---
  const [printTags, setPrintTags] = useState<PrintTag[] | null>(null)
  const markManyPrinted = useMarkManyPrinted()

  function classLabel(row: Pick<TagRow, "class_name" | "feature_class_code">): string {
    return row.class_name ?? classNameOf.get(row.feature_class_code) ?? row.feature_class_code
  }

  function printRows(subset: TagRow[]) {
    if (subset.length === 0) return
    setPrintTags(subset.map((r) => ({ code: r.tag_code, subtitle: classLabel(r) })))
  }

  // --- lifecycle mutations ---
  const setPrimary = useSetPrimaryTag()
  const voidTag = useVoidTag()
  const reissue = useReissueTag()

  async function runAction(p: Promise<unknown>, failMsg: string) {
    try {
      await p
    } catch (err) {
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : failMsg)
    }
  }

  const [registerOpen, setRegisterOpen] = useState(false)

  const total = list.data?.total ?? 0
  const from = total === 0 ? 0 : offset + 1
  const to = Math.min(offset + PAGE_SIZE, total)

  return (
    <div className="h-full overflow-y-auto p-4">
      <div className="flex w-full flex-col gap-4">
        <div className="flex items-start justify-between gap-2">
          <div>
            <h1 className="flex items-center gap-2 text-lg font-semibold">
              <Tags className="size-5" />
              Asset tags
            </h1>
            <p className="text-muted-foreground text-sm">
              Every feature carries one or more scannable tag codes. Browse and search the whole population, register a
              legacy asset number, print labels, and manage each tag's lifecycle. Scoped to your territories — you see
              only tags whose feature you could open.
            </p>
          </div>
          {canWrite && (
            <Button size="sm" onClick={() => setRegisterOpen(true)}>
              <Plus className="size-4" />
              Register number
            </Button>
          )}
        </div>

        {/* KPI strip */}
        <div className="flex flex-wrap gap-2">
          {stats.isPending ? (
            <div className="text-muted-foreground flex items-center gap-2 text-sm">
              <Loader2 className="size-4 animate-spin" />
              Loading totals…
            </div>
          ) : stats.isError || !stats.data ? (
            <p className="text-destructive text-sm">Could not load tag totals.</p>
          ) : (
            <>
              <StatChip label="Active" value={stats.data.active} tone="text-emerald-600 dark:text-emerald-400" />
              <StatChip label="Unprinted" value={stats.data.unprinted} tone="text-amber-600 dark:text-amber-400" />
              <StatChip label="Void" value={stats.data.void} tone="text-destructive" />
              <StatChip label="Superseded" value={stats.data.superseded} />
              <StatChip label="External" value={stats.data.external} />
              <StatChip label="System-minted" value={stats.data.generated} />
              <StatChip label="Total" value={stats.data.total} />
            </>
          )}
        </div>

        {/* Filter bar */}
        <div className="flex flex-wrap items-end gap-3">
          <div className="flex min-w-[14rem] flex-1 flex-col gap-1">
            <label className={labelClass}>Search code</label>
            <div className="relative">
              <Search className="text-muted-foreground pointer-events-none absolute top-1/2 left-2 size-4 -translate-y-1/2" />
              <input
                className={`${inputClass} pl-8`}
                value={searchInput}
                onChange={(e) => setSearchInput(e.target.value)}
                placeholder="Whole or partial tag code…"
              />
            </div>
          </div>
          <div className="flex flex-col gap-1">
            <label className={labelClass}>Status</label>
            <select className={inputClass} value={status} onChange={(e) => setStatus(e.target.value as TagStatus | "")}>
              <option value="">All</option>
              <option value="active">Active</option>
              <option value="void">Void</option>
              <option value="superseded">Superseded</option>
            </select>
          </div>
          <div className="flex flex-col gap-1">
            <label className={labelClass}>Source</label>
            <select className={inputClass} value={source} onChange={(e) => setSource(e.target.value as TagSource | "")}>
              <option value="">All</option>
              <option value="generated">System-minted</option>
              <option value="external">External / legacy</option>
            </select>
          </div>
          <div className="flex flex-col gap-1">
            <label className={labelClass}>Feature class</label>
            <select className={inputClass} value={classCode} onChange={(e) => setClassCode(e.target.value)}>
              <option value="">All classes</option>
              {(classes.data?.feature_classes ?? []).map((c) => (
                <option key={c.code} value={c.code}>
                  {c.name}
                </option>
              ))}
            </select>
          </div>
          <label className="flex items-center gap-2 pb-2 text-xs font-medium">
            <input type="checkbox" checked={primaryOnly} onChange={(e) => setPrimaryOnly(e.target.checked)} />
            Primary only
          </label>
        </div>

        {/* Bulk-action bar */}
        <div className="flex min-h-8 items-center gap-2 text-sm">
          {selected.size > 0 ? (
            <>
              <span className="text-muted-foreground">{selected.size} selected</span>
              <Button
                size="sm"
                variant="outline"
                onClick={() => printRows(rows.filter((r) => selected.has(r.tag_code)))}
              >
                <Printer className="size-4" />
                Print selected
              </Button>
              <Button size="sm" variant="ghost" onClick={() => setSelected(new Set())}>
                Clear
              </Button>
            </>
          ) : (
            <span className="text-muted-foreground text-xs">Select rows to print a sheet of labels.</span>
          )}
          <div className="ml-auto flex items-center gap-2">
            <Button size="sm" variant="ghost" onClick={() => void list.refetch()} title="Refresh">
              <RefreshCw className={`size-4 ${list.isFetching ? "animate-spin" : ""}`} />
            </Button>
          </div>
        </div>

        {/* Table */}
        {list.isPending ? (
          <div className="text-muted-foreground flex items-center gap-2 text-sm">
            <Loader2 className="size-4 animate-spin" />
            Loading tags…
          </div>
        ) : list.isError ? (
          <p className="text-destructive text-sm">Could not load tags.</p>
        ) : rows.length === 0 ? (
          <p className="text-muted-foreground text-sm">No tags match these filters.</p>
        ) : (
          <div className="overflow-x-auto rounded-lg border">
            <table className="w-full text-sm">
              <thead className="bg-muted/50 text-muted-foreground text-xs">
                <tr>
                  <th className="w-8 px-2 py-2">
                    <input
                      type="checkbox"
                      checked={allOnPageSelected}
                      onChange={(e) => togglePage(e.target.checked)}
                      title="Select all on this page"
                    />
                  </th>
                  <th className="px-2 py-2 text-left font-medium">Code</th>
                  <th className="px-2 py-2 text-left font-medium">Class</th>
                  <th className="px-2 py-2 text-left font-medium">Feature</th>
                  <th className="px-2 py-2 text-left font-medium">Source</th>
                  <th className="px-2 py-2 text-left font-medium">Status</th>
                  <th className="px-2 py-2 text-right font-medium">Prints</th>
                  <th className="px-2 py-2 text-right font-medium">Actions</th>
                </tr>
              </thead>
              <tbody>
                {rows.map((row) => {
                  const busy =
                    (setPrimary.isPending && setPrimary.variables?.code === row.tag_code) ||
                    (voidTag.isPending && voidTag.variables?.code === row.tag_code) ||
                    (reissue.isPending && reissue.variables?.code === row.tag_code)
                  return (
                    <tr key={row.tag_code} className="hover:bg-muted/30 border-t">
                      <td className="px-2 py-1.5 align-middle">
                        <input
                          type="checkbox"
                          checked={selected.has(row.tag_code)}
                          onChange={(e) => toggleOne(row.tag_code, e.target.checked)}
                        />
                      </td>
                      <td className="px-2 py-1.5 align-middle">
                        <span className="flex items-center gap-1 font-mono text-xs">
                          {row.is_primary && (
                            <Star className="size-3.5 fill-amber-400 text-amber-500" aria-label="primary" />
                          )}
                          {row.tag_code}
                        </span>
                      </td>
                      <td className="px-2 py-1.5 align-middle">{classLabel(row)}</td>
                      <td className="text-muted-foreground max-w-[12rem] truncate px-2 py-1.5 align-middle font-mono text-xs">
                        {row.feature_id}
                      </td>
                      <td className="px-2 py-1.5 align-middle text-xs">
                        {row.source === "external" ? "External" : "Minted"}
                      </td>
                      <td className="px-2 py-1.5 align-middle">
                        <StatusBadge status={row.status} />
                      </td>
                      <td className="px-2 py-1.5 text-right align-middle tabular-nums">{row.print_count}</td>
                      <td className="px-2 py-1.5 align-middle">
                        <div className="flex items-center justify-end gap-1">
                          {busy && <Loader2 className="text-muted-foreground size-3.5 animate-spin" />}
                          <Button
                            size="icon"
                            variant="ghost"
                            className="size-7"
                            title="Print label"
                            onClick={() => printRows([row])}
                          >
                            <Printer className="size-4" />
                          </Button>
                          {canWrite && !row.is_primary && row.status === "active" && (
                            <Button
                              size="icon"
                              variant="ghost"
                              className="size-7"
                              title="Set as primary"
                              disabled={busy}
                              onClick={() =>
                                runAction(setPrimary.mutateAsync({ code: row.tag_code }), "Could not set primary.")
                              }
                            >
                              <Star className="size-4" />
                            </Button>
                          )}
                          {canWrite && row.status !== "superseded" && (
                            <Button
                              size="icon"
                              variant="ghost"
                              className="size-7"
                              title="Reissue (mint a fresh code, supersede this one)"
                              disabled={busy}
                              onClick={() =>
                                runAction(reissue.mutateAsync({ code: row.tag_code }), "Could not reissue.")
                              }
                            >
                              <RefreshCw className="size-4" />
                            </Button>
                          )}
                          {canWrite && row.status !== "void" && !row.is_primary && (
                            <Button
                              size="icon"
                              variant="ghost"
                              className="text-destructive size-7"
                              title="Void (retire) this tag"
                              disabled={busy}
                              onClick={() => runAction(voidTag.mutateAsync({ code: row.tag_code }), "Could not void.")}
                            >
                              <Trash2 className="size-4" />
                            </Button>
                          )}
                        </div>
                      </td>
                    </tr>
                  )
                })}
              </tbody>
            </table>
          </div>
        )}

        {/* Pager */}
        {total > 0 && (
          <div className="flex items-center justify-between text-sm">
            <span className="text-muted-foreground">
              Showing {from.toLocaleString()}–{to.toLocaleString()} of {total.toLocaleString()}
            </span>
            <div className="flex items-center gap-1">
              <Button
                size="sm"
                variant="outline"
                disabled={offset === 0}
                onClick={() => setOffset((o) => Math.max(0, o - PAGE_SIZE))}
              >
                <ChevronLeft className="size-4" />
                Prev
              </Button>
              <Button
                size="sm"
                variant="outline"
                disabled={to >= total}
                onClick={() => setOffset((o) => o + PAGE_SIZE)}
              >
                Next
                <ChevronRight className="size-4" />
              </Button>
            </div>
          </div>
        )}
      </div>

      {registerOpen && (
        <RegisterNumberDialog
          classes={(classes.data?.feature_classes ?? []).map((c) => ({ code: c.code, name: c.name }))}
          onClose={() => setRegisterOpen(false)}
        />
      )}

      {printTags && (
        <TagPrintSheet
          tags={printTags}
          onClose={() => setPrintTags(null)}
          onPrinted={() => {
            if (canWrite && printTags.length > 0) {
              markManyPrinted.mutate({ codes: printTags.map((t) => t.code) })
            }
          }}
        />
      )}
    </div>
  )
}

/** Register a pre-existing / legacy asset number against a known feature. The
 * number is recorded source='external'; make-primary promotes it over the
 * system-minted code (the promoted code is the one that gets printed). */
function RegisterNumberDialog({
  classes,
  onClose,
}: {
  classes: { code: string; name: string }[]
  onClose: () => void
}) {
  const register = useRegisterTag()
  const [classCode, setClassCode] = useState("")
  const [featureId, setFeatureId] = useState("")
  const [tagCode, setTagCode] = useState("")
  const [makePrimary, setMakePrimary] = useState(false)
  const [saving, setSaving] = useState(false)

  const canSave = classCode.trim() !== "" && featureId.trim() !== "" && tagCode.trim() !== ""

  async function handleSave() {
    setSaving(true)
    try {
      await register.mutateAsync({
        feature_class_code: classCode.trim(),
        feature_id: featureId.trim(),
        tag_code: tagCode.trim(),
        make_primary: makePrimary,
      })
      useToastStore.getState().pushSuccess(`Registered ${tagCode.trim()}.`)
      onClose()
    } catch (err) {
      useToastStore
        .getState()
        .pushError(err instanceof ApiError ? err.message : "Could not register this number.")
    } finally {
      setSaving(false)
    }
  }

  return (
    <FormDialog
      open
      onOpenChange={(next) => next || onClose()}
      title="Register existing asset number"
      description="Record a pre-existing / legacy number a utility already paints on this asset, so that number also resolves to the feature."
      onSubmit={handleSave}
      submitLabel="Register"
      canSubmit={canSave}
      saving={saving}
    >
      <div className="flex flex-col gap-1">
        <label className={labelClass}>
          Feature class <span className="text-destructive">*</span>
        </label>
        <select className={inputClass} value={classCode} onChange={(e) => setClassCode(e.target.value)}>
          <option value="">Select a class…</option>
          {classes.map((c) => (
            <option key={c.code} value={c.code}>
              {c.name}
            </option>
          ))}
        </select>
      </div>
      <div className="flex flex-col gap-1">
        <label className={labelClass}>
          Feature id <span className="text-destructive">*</span>
        </label>
        <input
          className={inputClass}
          value={featureId}
          onChange={(e) => setFeatureId(e.target.value)}
          placeholder="The feature's uuid"
        />
      </div>
      <div className="flex flex-col gap-1">
        <label className={labelClass}>
          Legacy / external code <span className="text-destructive">*</span>
        </label>
        <input
          className={inputClass}
          value={tagCode}
          onChange={(e) => setTagCode(e.target.value)}
          placeholder="The number painted on the asset"
        />
      </div>
      <label className="flex items-center gap-2 text-xs font-medium">
        <input type="checkbox" checked={makePrimary} onChange={(e) => setMakePrimary(e.target.checked)} />
        Make this the primary (printed) code
      </label>
    </FormDialog>
  )
}
