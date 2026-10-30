import { useState } from "react"
import { Loader2, Pencil, Plus, ShieldCheck, Trash2 } from "lucide-react"

import { ConfirmDeleteDialog } from "@/components/admin/confirm-delete-dialog"
import { FormDialog } from "@/components/admin/form-dialog"
import { inputClass } from "@/components/map/field-utils"
import { Button } from "@/components/ui/button"
import { usePermission } from "@/hooks/use-auth"
import { useFeatureNameSearch } from "@/hooks/use-feature-name-search"
import {
  PROTECTION_DEVICE_CLASSES,
  type ProtectionSettings,
  type ProtectionSettingsBody,
  resolveProtectionDevice,
  useDeleteProtectionSettings,
  useProtectionSettingsRegister,
  useUpsertProtectionSettings,
} from "@/hooks/use-protection-settings"
import { ApiError } from "@/lib/api"
import { useToastStore } from "@/stores/toast-store"

const labelClass = "text-xs font-medium"

interface FormFields {
  relayModel: string
  ctRatio: string
  phasePickupA: string
  phaseTimeDial: string
  phaseCurve: string
  phaseInstA: string
  groundPickupA: string
  groundTimeDial: string
  groundCurve: string
  groundInstA: string
  recloseOperations: string
  recloseIntervals: string
  notes: string
}

const EMPTY_FIELDS: FormFields = {
  relayModel: "",
  ctRatio: "",
  phasePickupA: "",
  phaseTimeDial: "",
  phaseCurve: "",
  phaseInstA: "",
  groundPickupA: "",
  groundTimeDial: "",
  groundCurve: "",
  groundInstA: "",
  recloseOperations: "",
  recloseIntervals: "",
  notes: "",
}

const n = (v: number | null): string => (v != null ? String(v) : "")

function fieldsFrom(s: ProtectionSettings | null): FormFields {
  if (!s) return { ...EMPTY_FIELDS }
  return {
    relayModel: s.relay_model ?? "",
    ctRatio: s.ct_ratio ?? "",
    phasePickupA: n(s.phase_pickup_a),
    phaseTimeDial: n(s.phase_time_dial),
    phaseCurve: s.phase_curve ?? "",
    phaseInstA: n(s.phase_instantaneous_a),
    groundPickupA: n(s.ground_pickup_a),
    groundTimeDial: n(s.ground_time_dial),
    groundCurve: s.ground_curve ?? "",
    groundInstA: n(s.ground_instantaneous_a),
    recloseOperations: n(s.reclose_operations),
    recloseIntervals: s.reclose_intervals ?? "",
    notes: s.notes ?? "",
  }
}

const numOrNull = (v: string): number | null => {
  const t = v.trim()
  if (t === "") return null
  const x = Number(t)
  return Number.isFinite(x) ? x : null
}
const strOrNull = (v: string): string | null => (v.trim() === "" ? null : v.trim())

function bodyFrom(f: FormFields): ProtectionSettingsBody {
  return {
    relay_model: strOrNull(f.relayModel),
    ct_ratio: strOrNull(f.ctRatio),
    phase_pickup_a: numOrNull(f.phasePickupA),
    phase_time_dial: numOrNull(f.phaseTimeDial),
    phase_curve: strOrNull(f.phaseCurve),
    phase_instantaneous_a: numOrNull(f.phaseInstA),
    ground_pickup_a: numOrNull(f.groundPickupA),
    ground_time_dial: numOrNull(f.groundTimeDial),
    ground_curve: strOrNull(f.groundCurve),
    ground_instantaneous_a: numOrNull(f.groundInstA),
    reclose_operations: numOrNull(f.recloseOperations),
    reclose_intervals: strOrNull(f.recloseIntervals),
    notes: strOrNull(f.notes),
  }
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex flex-col gap-1">
      <label className={labelClass}>{label}</label>
      {children}
    </div>
  )
}

function NumInput({ value, onChange }: { value: string; onChange: (v: string) => void }) {
  return <input type="number" step="any" className={inputClass} value={value} onChange={(e) => onChange(e.target.value)} />
}

/** Find the protective device to attach settings to (new records only). Two
 * ways in: a name search SCOPED to protective/switching classes (so a transformer
 * can't be picked), or a pasted feature id (GUID) resolved server-side — which
 * also rejects an id that belongs to a non-protection feature. */
function DevicePicker({ onPick }: { onPick: (d: { cls: string; id: string; label: string }) => void }) {
  const [q, setQ] = useState("")
  const { results, loading } = useFeatureNameSearch(q, PROTECTION_DEVICE_CLASSES)
  const [guid, setGuid] = useState("")
  const [resolving, setResolving] = useState(false)

  async function useGuid() {
    const id = guid.trim()
    if (id === "") return
    setResolving(true)
    try {
      const d = await resolveProtectionDevice(id)
      onPick({
        cls: d.feature_class_code,
        id: d.feature_id,
        label: d.name ? `${d.name} (${d.feature_class_code})` : `${d.feature_class_code} ${d.feature_id.slice(0, 8)}`,
      })
    } catch (err) {
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not resolve that feature id.")
    } finally {
      setResolving(false)
    }
  }

  return (
    <div className="flex flex-col gap-2">
      <div className="flex flex-col gap-1">
        <label className={labelClass}>
          Device <span className="text-destructive">*</span>
        </label>
        <input
          className={inputClass}
          value={q}
          onChange={(e) => setQ(e.target.value)}
          placeholder="Search a protective / switching device by name…"
          autoFocus
        />
        {q.trim().length >= 2 && (
          <div className="max-h-44 overflow-auto rounded-md border">
            {loading ? (
              <div className="text-muted-foreground p-2 text-xs">Searching…</div>
            ) : results.length === 0 ? (
              <div className="text-muted-foreground p-2 text-xs">No protective / switching device matches.</div>
            ) : (
              results.map((r) => (
                <button
                  key={`${r.featureClassCode}:${r.featureId}`}
                  type="button"
                  onClick={() => onPick({ cls: r.featureClassCode, id: r.featureId, label: `${r.name} (${r.className})` })}
                  className="hover:bg-muted/60 block w-full px-2 py-1.5 text-left text-xs"
                >
                  <span className="font-medium">{r.name}</span>{" "}
                  <span className="text-muted-foreground">({r.className})</span>
                </button>
              ))
            )}
          </div>
        )}
      </div>

      <div className="text-muted-foreground flex items-center gap-2 text-[11px]">
        <span className="bg-border h-px flex-1" />
        or paste a feature ID
        <span className="bg-border h-px flex-1" />
      </div>

      <div className="flex gap-2">
        <input
          className={inputClass}
          value={guid}
          onChange={(e) => setGuid(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter") {
              e.preventDefault()
              void useGuid()
            }
          }}
          placeholder="Feature ID (GUID)"
        />
        <Button type="button" variant="outline" size="sm" disabled={resolving || guid.trim() === ""} onClick={() => void useGuid()}>
          {resolving ? "…" : "Use"}
        </Button>
      </div>
      <p className="text-muted-foreground text-[11px]">
        Only a breaker, recloser, fuse, switch, isolator, sectionalizer or relay can carry protection settings — a
        transformer or line id is rejected.
      </p>
    </div>
  )
}

function SettingsFields({ value, onChange }: { value: FormFields; onChange: (v: FormFields) => void }) {
  const set = (patch: Partial<FormFields>) => onChange({ ...value, ...patch })
  return (
    <div className="flex flex-col gap-3">
      <div className="grid grid-cols-2 gap-3">
        <Field label="Relay model">
          <input className={inputClass} value={value.relayModel} onChange={(e) => set({ relayModel: e.target.value })} placeholder="e.g. SEL-751" />
        </Field>
        <Field label="CT ratio">
          <input className={inputClass} value={value.ctRatio} onChange={(e) => set({ ctRatio: e.target.value })} placeholder="e.g. 600/5" />
        </Field>
      </div>

      <div className="flex flex-col gap-2">
        <p className="text-muted-foreground text-xs font-semibold uppercase">Phase overcurrent (51 / 50)</p>
        <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
          <Field label="Pickup (A)">
            <NumInput value={value.phasePickupA} onChange={(v) => set({ phasePickupA: v })} />
          </Field>
          <Field label="Time dial">
            <NumInput value={value.phaseTimeDial} onChange={(v) => set({ phaseTimeDial: v })} />
          </Field>
          <Field label="Curve">
            <input className={inputClass} value={value.phaseCurve} onChange={(e) => set({ phaseCurve: e.target.value })} placeholder="IEC_SI…" />
          </Field>
          <Field label="Instantaneous (A)">
            <NumInput value={value.phaseInstA} onChange={(v) => set({ phaseInstA: v })} />
          </Field>
        </div>
      </div>

      <div className="flex flex-col gap-2">
        <p className="text-muted-foreground text-xs font-semibold uppercase">Ground / earth fault (51N / 50N)</p>
        <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
          <Field label="Pickup (A)">
            <NumInput value={value.groundPickupA} onChange={(v) => set({ groundPickupA: v })} />
          </Field>
          <Field label="Time dial">
            <NumInput value={value.groundTimeDial} onChange={(v) => set({ groundTimeDial: v })} />
          </Field>
          <Field label="Curve">
            <input className={inputClass} value={value.groundCurve} onChange={(e) => set({ groundCurve: e.target.value })} placeholder="IEC_SI…" />
          </Field>
          <Field label="Instantaneous (A)">
            <NumInput value={value.groundInstA} onChange={(v) => set({ groundInstA: v })} />
          </Field>
        </div>
      </div>

      <div className="flex flex-col gap-2">
        <p className="text-muted-foreground text-xs font-semibold uppercase">Reclosing</p>
        <div className="grid grid-cols-2 gap-3">
          <Field label="Operations to lockout">
            <NumInput value={value.recloseOperations} onChange={(v) => set({ recloseOperations: v })} />
          </Field>
          <Field label="Reclose intervals">
            <input className={inputClass} value={value.recloseIntervals} onChange={(e) => set({ recloseIntervals: e.target.value })} placeholder="0, 5, 15 s" />
          </Field>
        </div>
      </div>

      <Field label="Notes">
        <input className={inputClass} value={value.notes} onChange={(e) => set({ notes: e.target.value })} />
      </Field>
    </div>
  )
}

function SettingsDialog({ existing, onClose }: { existing: ProtectionSettings | null; onClose: () => void }) {
  const [device, setDevice] = useState<{ cls: string; id: string; label: string } | null>(
    existing
      ? { cls: existing.feature_class_code, id: existing.feature_id, label: `${existing.feature_class_code} ${existing.feature_id.slice(0, 8)}` }
      : null,
  )
  const [fields, setFields] = useState<FormFields>(() => fieldsFrom(existing))
  const [saving, setSaving] = useState(false)
  const upsert = useUpsertProtectionSettings()

  async function handleSave() {
    if (!device) return
    setSaving(true)
    try {
      await upsert.mutateAsync({ featureClass: device.cls, featureId: device.id, body: bodyFrom(fields) })
      onClose()
    } catch (err) {
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not save these settings.")
    } finally {
      setSaving(false)
    }
  }

  return (
    <FormDialog
      open
      onOpenChange={(next) => next || onClose()}
      title={existing ? "Edit protection settings" : "New protection settings"}
      description="The as-built relay / overcurrent settings for a protective device."
      onSubmit={handleSave}
      submitLabel={existing ? "Save" : "Create"}
      canSubmit={device != null}
      saving={saving}
      className="max-w-3xl"
    >
      {!device ? (
        <DevicePicker onPick={(d) => setDevice(d)} />
      ) : (
        <div className="bg-muted/40 flex items-center justify-between gap-2 rounded-md border px-3 py-1.5 text-sm">
          <span>
            Device: <span className="font-medium">{device.label}</span>
          </span>
          {!existing && (
            <button type="button" className="text-primary text-xs hover:underline" onClick={() => setDevice(null)}>
              Change
            </button>
          )}
        </div>
      )}
      {device && <SettingsFields value={fields} onChange={setFields} />}
    </FormDialog>
  )
}

/**
 * The protection-settings register (api/internal/protection, migration 0185): the
 * as-built relay / overcurrent settings per protective device — pickups, time
 * dials, instantaneous, CT ratio and reclose scheme — that a TCC curve does not
 * capture. View needs features:read; add/edit needs features:write.
 */
export function AdminProtectionSettingsPage() {
  const { data, isPending, isError } = useProtectionSettingsRegister()
  const canWrite = usePermission()("features:write")
  const del = useDeleteProtectionSettings()

  const [editing, setEditing] = useState<ProtectionSettings | "new" | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<ProtectionSettings | null>(null)

  const rows = data?.settings ?? []
  const fmt = (v: number | null) => (v != null ? String(v) : "—")

  return (
    <div className="h-full overflow-y-auto p-4">
      <div className="flex w-full flex-col gap-4">
        <div className="flex items-center justify-between">
          <div>
            <h1 className="flex items-center gap-2 text-lg font-semibold">
              <ShieldCheck className="size-5" />
              Protection settings
            </h1>
            <p className="text-muted-foreground text-sm">
              The as-built relay / overcurrent settings per protective device — the numeric pickups, time dials,
              instantaneous and reclose scheme a TCC curve does not capture.
            </p>
          </div>
          {canWrite && (
            <Button size="sm" onClick={() => setEditing("new")}>
              <Plus className="size-4" />
              New
            </Button>
          )}
        </div>

        {isPending ? (
          <div className="text-muted-foreground flex items-center gap-2 text-sm">
            <Loader2 className="size-4 animate-spin" />
            Loading settings…
          </div>
        ) : isError || !data ? (
          <p className="text-destructive text-sm">Could not load the protection-settings register.</p>
        ) : rows.length === 0 ? (
          <p className="text-muted-foreground text-sm">
            No protection settings recorded yet. Add one for a protective device to start the register.
          </p>
        ) : (
          <div className="overflow-x-auto rounded-md border">
            <table className="w-full text-xs">
              <thead className="bg-muted/50 text-muted-foreground">
                <tr className="[&>th]:px-2 [&>th]:py-1.5 [&>th]:text-left [&>th]:font-medium [&>th]:whitespace-nowrap">
                  <th>Device</th>
                  <th>Relay</th>
                  <th>CT</th>
                  <th>Ph pickup</th>
                  <th>Ph TD</th>
                  <th>Ph curve</th>
                  <th>Ph inst</th>
                  <th>Gnd pickup</th>
                  <th>Gnd TD</th>
                  <th>Reclose</th>
                  {canWrite && <th></th>}
                </tr>
              </thead>
              <tbody>
                {rows.map((s) => (
                  <tr key={s.id} className="border-border/60 border-t [&>td]:px-2 [&>td]:py-1.5 [&>td]:whitespace-nowrap">
                    <td>
                      <span className="font-medium">{s.feature_class_code}</span>{" "}
                      <span className="text-muted-foreground font-mono">{s.feature_id.slice(0, 8)}</span>
                      {s.source && s.source !== "manual" && (
                        <span
                          className="ml-1.5 rounded bg-sky-500/15 px-1 py-0.5 text-[10px] font-medium text-sky-700 dark:text-sky-400"
                          title={`Ingested from ${s.source}`}
                        >
                          {s.source}
                        </span>
                      )}
                    </td>
                    <td>{s.relay_model ?? "—"}</td>
                    <td>{s.ct_ratio ?? "—"}</td>
                    <td className="tabular-nums">{fmt(s.phase_pickup_a)}</td>
                    <td className="tabular-nums">{fmt(s.phase_time_dial)}</td>
                    <td>{s.phase_curve ?? "—"}</td>
                    <td className="tabular-nums">{fmt(s.phase_instantaneous_a)}</td>
                    <td className="tabular-nums">{fmt(s.ground_pickup_a)}</td>
                    <td className="tabular-nums">{fmt(s.ground_time_dial)}</td>
                    <td className="tabular-nums">{s.reclose_operations != null ? `${s.reclose_operations} ops` : "—"}</td>
                    {canWrite && (
                      <td className="text-right">
                        <div className="flex justify-end">
                          <Button variant="ghost" size="icon" className="size-7" title="Edit" onClick={() => setEditing(s)}>
                            <Pencil className="size-4" />
                          </Button>
                          <Button
                            variant="ghost"
                            size="icon"
                            className="text-destructive size-7"
                            title="Delete"
                            onClick={() => setDeleteTarget(s)}
                          >
                            <Trash2 className="size-4" />
                          </Button>
                        </div>
                      </td>
                    )}
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>

      {editing && <SettingsDialog existing={editing === "new" ? null : editing} onClose={() => setEditing(null)} />}

      <ConfirmDeleteDialog
        open={deleteTarget !== null}
        title="Delete these protection settings?"
        description="Removes the recorded settings for this device. This can't be undone."
        onOpenChange={(open) => !open && setDeleteTarget(null)}
        onConfirm={async () => {
          if (!deleteTarget) return
          await del.mutateAsync({ featureClass: deleteTarget.feature_class_code, featureId: deleteTarget.feature_id })
        }}
      />
    </div>
  )
}
