import { useMemo, useState } from "react"
import { Coins, Loader2, Pencil, Plus, Trash2 } from "lucide-react"
import { Link } from "react-router"

import { ConfirmDeleteDialog } from "@/components/admin/confirm-delete-dialog"
import { FormDialog } from "@/components/admin/form-dialog"
import { inputClass } from "@/components/map/field-utils"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { useFeatureClasses } from "@/hooks/use-feature-classes"
import { useFeatureClassSchema } from "@/hooks/use-feature-class-schema"
import {
  type CostRate,
  type Currency,
  useCostRates,
  useCreateCostRate,
  useCreateCurrency,
  useCurrencies,
  useDeleteCostRate,
  useDeleteCurrency,
  useUpdateCostRate,
  useUpdateCurrency,
} from "@/hooks/use-cost-catalog"
import { ApiError } from "@/lib/api"
import { useToastStore } from "@/stores/toast-store"

const labelClass = "text-xs font-medium"
const UNIT_BASES: CostRate["unit_basis"][] = ["each", "per_km"]

function toastError(err: unknown, fallback: string) {
  useToastStore.getState().pushError(err instanceof ApiError ? err.message : fallback)
}

/** Parses a numeric input -> number, blank/invalid -> undefined. */
function parseNumber(raw: string): number | undefined {
  const trimmed = raw.trim()
  if (trimmed === "") return undefined
  const n = Number(trimmed)
  return Number.isFinite(n) ? n : undefined
}

// --- Currencies ------------------------------------------------------------

function CurrencyDialog({ existing, onClose }: { existing: Currency | null; onClose: () => void }) {
  const isEdit = existing !== null
  const [code, setCode] = useState(existing?.code ?? "")
  const [name, setName] = useState(existing?.name ?? "")
  const [isBase, setIsBase] = useState(existing?.is_base ?? false)
  const [perBase, setPerBase] = useState(existing ? String(existing.per_base) : "")
  const create = useCreateCurrency()
  const update = useUpdateCurrency()
  const saving = create.isPending || update.isPending

  // The base currency's conversion factor is pinned to 1 (it is the unit every
  // stored cost is denominated in) -- so the field is forced and locked.
  const effectivePerBase = isBase ? 1 : parseNumber(perBase)
  const canSave =
    code.trim().length > 0 &&
    name.trim().length > 0 &&
    effectivePerBase !== undefined &&
    effectivePerBase > 0 &&
    !saving

  function handleSave() {
    if (isEdit) {
      const body: { name?: string; per_base?: number; is_base?: boolean } = {}
      if (name.trim() !== existing.name) body.name = name.trim()
      if (effectivePerBase !== existing.per_base) body.per_base = effectivePerBase
      if (isBase !== existing.is_base) body.is_base = isBase
      update.mutate(
        { code: existing.code, body },
        { onSuccess: onClose, onError: (err) => toastError(err, "Could not update the currency.") },
      )
    } else {
      create.mutate(
        { code: code.trim().toUpperCase(), name: name.trim(), per_base: effectivePerBase as number, is_base: isBase },
        { onSuccess: onClose, onError: (err) => toastError(err, "Could not create the currency.") },
      )
    }
  }

  return (
    <FormDialog
      open
      onOpenChange={(next) => next || onClose()}
      title={isEdit ? "Edit currency" : "New currency"}
      description="A presentation currency for the Bill of Quantities. per_base converts the base currency to this one for display."
      onSubmit={handleSave}
      submitLabel={isEdit ? "Save" : "Create"}
      canSubmit={canSave}
      saving={saving}
    >
      <div className="flex flex-col gap-1">
        <label className={labelClass}>
          Code <span className="text-destructive">*</span>
        </label>
        <input
          className={inputClass}
          value={code}
          onChange={(e) => setCode(e.target.value)}
          placeholder="e.g. GHS"
          disabled={isEdit}
          autoFocus={!isEdit}
        />
        {isEdit && <span className="text-muted-foreground text-xs">Fixed once created.</span>}
      </div>

      <div className="flex flex-col gap-1">
        <label className={labelClass}>
          Name <span className="text-destructive">*</span>
        </label>
        <input
          className={inputClass}
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder="e.g. Ghana Cedi"
        />
      </div>

      <label className="flex items-center gap-2 text-sm">
        <input type="checkbox" checked={isBase} onChange={(e) => setIsBase(e.target.checked)} />
        Base currency (every stored cost is denominated in this)
      </label>

      <div className="flex flex-col gap-1">
        <label className={labelClass}>
          Per base unit <span className="text-destructive">*</span>
        </label>
        <input
          className={inputClass}
          type="number"
          min="0"
          step="any"
          value={isBase ? "1" : perBase}
          onChange={(e) => setPerBase(e.target.value)}
          placeholder="e.g. 12"
          disabled={isBase}
        />
        <span className="text-muted-foreground text-xs">
          How many of this currency equal one base unit. The base currency is always 1.
        </span>
      </div>
    </FormDialog>
  )
}

function CurrencyRow({
  currency,
  onEdit,
  onDelete,
}: {
  currency: Currency
  onEdit: (c: Currency) => void
  onDelete: (c: Currency) => void
}) {
  return (
    <div className="hover:bg-muted/50 group flex items-center gap-2 rounded-md px-2 py-1.5">
      <span className="truncate text-sm font-medium">{currency.code}</span>
      <span className="text-muted-foreground truncate text-sm">{currency.name}</span>
      {currency.is_base ? (
        <span className="rounded bg-emerald-500/15 px-1.5 py-0.5 text-xs text-emerald-600 dark:text-emerald-400">
          base
        </span>
      ) : (
        <span className="bg-muted text-muted-foreground rounded px-1.5 py-0.5 text-xs">
          {currency.per_base} / base
        </span>
      )}
      <div className="ml-auto flex shrink-0 items-center gap-0.5 opacity-0 transition-opacity group-hover:opacity-100 focus-within:opacity-100">
        <Button variant="ghost" size="icon" className="size-7" title="Edit" onClick={() => onEdit(currency)}>
          <Pencil className="size-4" />
        </Button>
        {!currency.is_base && (
          <Button
            variant="ghost"
            size="icon"
            className="text-destructive size-7"
            title="Delete"
            onClick={() => onDelete(currency)}
          >
            <Trash2 className="size-4" />
          </Button>
        )}
      </div>
    </div>
  )
}

// --- Cost rates ------------------------------------------------------------

function CostRateDialog({ existing, onClose }: { existing: CostRate | null; onClose: () => void }) {
  const isEdit = existing !== null
  const [classCode, setClassCode] = useState(existing?.feature_class_code ?? "")
  const [subtype, setSubtype] = useState<string>(existing?.subtype_code != null ? String(existing.subtype_code) : "")
  const [unitBasis, setUnitBasis] = useState<CostRate["unit_basis"]>(existing?.unit_basis ?? "each")
  const [unitRate, setUnitRate] = useState(existing ? String(existing.unit_rate) : "")
  const [notes, setNotes] = useState(existing?.notes ?? "")

  const { data: classesData } = useFeatureClasses()
  const classes = useMemo(() => classesData?.feature_classes ?? [], [classesData])
  const { data: schema } = useFeatureClassSchema(classCode || undefined)
  const subtypes = schema?.subtypes ?? []

  const create = useCreateCostRate()
  const update = useUpdateCostRate()
  const saving = create.isPending || update.isPending

  const rate = parseNumber(unitRate)
  const canSave = classCode.length > 0 && rate !== undefined && rate >= 0 && !saving

  function handleSave() {
    const subtypeCode = subtype === "" ? null : Number(subtype)
    if (isEdit) {
      const body: {
        subtype_code?: number | null
        clear_subtype?: boolean
        unit_basis?: CostRate["unit_basis"]
        unit_rate?: number
        notes?: string | null
      } = {}
      const existingSubtype = existing.subtype_code ?? null
      if (subtypeCode !== existingSubtype) {
        if (subtypeCode === null) body.clear_subtype = true
        else body.subtype_code = subtypeCode
      }
      if (unitBasis !== existing.unit_basis) body.unit_basis = unitBasis
      if (rate !== existing.unit_rate) body.unit_rate = rate
      if ((notes.trim() || null) !== (existing.notes ?? null)) body.notes = notes.trim() || null
      update.mutate(
        { id: existing.id, body },
        { onSuccess: onClose, onError: (err) => toastError(err, "Could not update the cost rate.") },
      )
    } else {
      create.mutate(
        {
          feature_class_code: classCode,
          subtype_code: subtypeCode,
          unit_basis: unitBasis,
          unit_rate: rate as number,
          notes: notes.trim() || null,
        },
        { onSuccess: onClose, onError: (err) => toastError(err, "Could not create the cost rate.") },
      )
    }
  }

  return (
    <FormDialog
      open
      onOpenChange={(next) => next || onClose()}
      title={isEdit ? "Edit cost rate" : "New cost rate"}
      description="A unit rate, in the base currency, for a feature class (optionally one subtype). The BoQ multiplies it by the scoped quantity."
      onSubmit={handleSave}
      submitLabel={isEdit ? "Save" : "Create"}
      canSubmit={canSave}
      saving={saving}
    >
      <div className="flex flex-col gap-1">
        <label className={labelClass}>
          Feature class <span className="text-destructive">*</span>
        </label>
        <select
          className={inputClass}
          value={classCode}
          onChange={(e) => {
            setClassCode(e.target.value)
            setSubtype("")
          }}
          disabled={isEdit}
        >
          <option value="">Select a class...</option>
          {classes.map((c) => (
            <option key={c.code} value={c.code}>
              {c.name}
            </option>
          ))}
        </select>
        {isEdit && <span className="text-muted-foreground text-xs">Fixed once created.</span>}
      </div>

      <div className="flex flex-col gap-1">
        <label className={labelClass}>Subtype</label>
        <select
          className={inputClass}
          value={subtype}
          onChange={(e) => setSubtype(e.target.value)}
          disabled={classCode === ""}
        >
          <option value="">Class default (all subtypes)</option>
          {subtypes.map((s) => (
            <option key={s.code} value={s.code}>
              {s.name}
            </option>
          ))}
        </select>
      </div>

      <div className="flex flex-col gap-1">
        <label className={labelClass}>Unit basis</label>
        <select
          className={inputClass}
          value={unitBasis}
          onChange={(e) => setUnitBasis(e.target.value as CostRate["unit_basis"])}
        >
          {UNIT_BASES.map((b) => (
            <option key={b} value={b}>
              {b === "each" ? "each (per item)" : "per_km (per km of length)"}
            </option>
          ))}
        </select>
      </div>

      <div className="flex flex-col gap-1">
        <label className={labelClass}>
          Unit rate <span className="text-destructive">*</span>
        </label>
        <input
          className={inputClass}
          type="number"
          min="0"
          step="any"
          value={unitRate}
          onChange={(e) => setUnitRate(e.target.value)}
          placeholder="in the base currency"
        />
      </div>

      <div className="flex flex-col gap-1">
        <label className={labelClass}>Notes</label>
        <input className={inputClass} value={notes} onChange={(e) => setNotes(e.target.value)} />
      </div>
    </FormDialog>
  )
}

function CostRateRow({
  rate,
  classNameMap,
  onEdit,
  onDelete,
}: {
  rate: CostRate
  classNameMap: Record<string, string>
  onEdit: (r: CostRate) => void
  onDelete: (r: CostRate) => void
}) {
  return (
    <div className="hover:bg-muted/50 group flex items-center gap-2 rounded-md px-2 py-1.5">
      <span className="truncate text-sm font-medium">{classNameMap[rate.feature_class_code] ?? rate.feature_class_code}</span>
      {rate.subtype_code != null ? (
        <span className="bg-muted text-muted-foreground rounded px-1.5 py-0.5 text-xs">subtype {rate.subtype_code}</span>
      ) : (
        <span className="bg-muted text-muted-foreground rounded px-1.5 py-0.5 text-xs">default</span>
      )}
      <span className="text-muted-foreground text-xs">
        {rate.unit_rate} / {rate.unit_basis === "each" ? "item" : "km"}
      </span>
      <div className="ml-auto flex shrink-0 items-center gap-0.5 opacity-0 transition-opacity group-hover:opacity-100 focus-within:opacity-100">
        <Button variant="ghost" size="icon" className="size-7" title="Edit" onClick={() => onEdit(rate)}>
          <Pencil className="size-4" />
        </Button>
        <Button
          variant="ghost"
          size="icon"
          className="text-destructive size-7"
          title="Delete"
          onClick={() => onDelete(rate)}
        >
          <Trash2 className="size-4" />
        </Button>
      </div>
    </div>
  )
}

export function AdminCostCatalogPage() {
  const currencies = useCurrencies()
  const costRates = useCostRates()
  const { data: classesData } = useFeatureClasses()

  const [currencyDialog, setCurrencyDialog] = useState<{ existing: Currency | null } | null>(null)
  const [deletingCurrency, setDeletingCurrency] = useState<Currency | null>(null)
  const delCurrency = useDeleteCurrency()

  const [rateDialog, setRateDialog] = useState<{ existing: CostRate | null } | null>(null)
  const [deletingRate, setDeletingRate] = useState<CostRate | null>(null)
  const delRate = useDeleteCostRate()

  const classNameMap = useMemo(() => {
    const map: Record<string, string> = {}
    for (const c of classesData?.feature_classes ?? []) map[c.code] = c.name
    return map
  }, [classesData])

  const currencyRows = currencies.data?.currencies ?? []
  const rateRows = costRates.data?.cost_rates ?? []

  return (
    <div className="h-full overflow-y-auto p-4">
      <div className="flex w-full flex-col gap-4">
        <div>
          <h1 className="flex items-center gap-2 text-lg font-semibold">
            <Coins className="size-5" />
            Cost catalog
          </h1>
          <p className="text-muted-foreground text-sm">
            The pricing backbone for the Bill of Quantities. Set presentation currencies and the unit-rate price book
            here; conductor per-km costs live on their{" "}
            <Link to="/admin/materials" className="underline">
              material specs
            </Link>
            , and individual features can carry a one-off override from their feature panel.
          </p>
        </div>

        {/* Currencies */}
        <Card>
          <CardHeader className="flex-row items-center justify-between pb-2">
            <div>
              <CardTitle className="text-sm">Currencies</CardTitle>
              <CardDescription>
                Every stored cost is in the single base currency; others convert for display.
              </CardDescription>
            </div>
            <Button variant="outline" size="sm" onClick={() => setCurrencyDialog({ existing: null })}>
              <Plus className="size-4" />
              Add currency
            </Button>
          </CardHeader>
          <CardContent className="pt-0">
            {currencies.isPending ? (
              <div className="text-muted-foreground flex items-center gap-2 text-sm">
                <Loader2 className="size-4 animate-spin" />
                Loading...
              </div>
            ) : currencies.isError ? (
              <p className="text-destructive text-sm">Could not load currencies.</p>
            ) : currencyRows.length === 0 ? (
              <p className="text-muted-foreground text-sm">No currencies yet.</p>
            ) : (
              <div className="flex flex-col">
                {currencyRows.map((c) => (
                  <CurrencyRow
                    key={c.code}
                    currency={c}
                    onEdit={(cur) => setCurrencyDialog({ existing: cur })}
                    onDelete={setDeletingCurrency}
                  />
                ))}
              </div>
            )}
          </CardContent>
        </Card>

        {/* Cost rates */}
        <Card>
          <CardHeader className="flex-row items-center justify-between pb-2">
            <div>
              <CardTitle className="text-sm">Unit-rate price book</CardTitle>
              <CardDescription>
                Per-item ('each') or per-km rates keyed by class + subtype. A class-default row covers every subtype
                without a more specific one.
              </CardDescription>
            </div>
            <Button variant="outline" size="sm" onClick={() => setRateDialog({ existing: null })}>
              <Plus className="size-4" />
              Add rate
            </Button>
          </CardHeader>
          <CardContent className="pt-0">
            {costRates.isPending ? (
              <div className="text-muted-foreground flex items-center gap-2 text-sm">
                <Loader2 className="size-4 animate-spin" />
                Loading...
              </div>
            ) : costRates.isError ? (
              <p className="text-destructive text-sm">Could not load cost rates.</p>
            ) : rateRows.length === 0 ? (
              <p className="text-muted-foreground text-sm">No cost rates yet -- every BoQ line will report as unpriced.</p>
            ) : (
              <div className="flex flex-col">
                {rateRows.map((r) => (
                  <CostRateRow
                    key={r.id}
                    rate={r}
                    classNameMap={classNameMap}
                    onEdit={(rate) => setRateDialog({ existing: rate })}
                    onDelete={setDeletingRate}
                  />
                ))}
              </div>
            )}
          </CardContent>
        </Card>
      </div>

      {currencyDialog && (
        <CurrencyDialog
          key={currencyDialog.existing?.code ?? "new"}
          existing={currencyDialog.existing}
          onClose={() => setCurrencyDialog(null)}
        />
      )}
      {rateDialog && (
        <CostRateDialog
          key={rateDialog.existing?.id ?? "new"}
          existing={rateDialog.existing}
          onClose={() => setRateDialog(null)}
        />
      )}

      <ConfirmDeleteDialog
        open={deletingCurrency !== null}
        title={`Delete ${deletingCurrency?.code ?? ""}?`}
        description="This can't be undone. Prices stored in the base currency are unaffected."
        confirmLabel="Delete"
        onOpenChange={(open) => !open && setDeletingCurrency(null)}
        onConfirm={async () => {
          if (!deletingCurrency) return
          await delCurrency.mutateAsync({ code: deletingCurrency.code })
        }}
      />

      <ConfirmDeleteDialog
        open={deletingRate !== null}
        title="Delete cost rate?"
        description="This can't be undone. Features priced off this rate will report as unpriced until another rate covers them."
        confirmLabel="Delete"
        onOpenChange={(open) => !open && setDeletingRate(null)}
        onConfirm={async () => {
          if (!deletingRate) return
          await delRate.mutateAsync({ id: deletingRate.id })
        }}
      />
    </div>
  )
}
