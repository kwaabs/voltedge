import { useMemo } from "react"

import { inputClass } from "@/components/map/field-utils"
import { useCircuits, type Circuit, type CircuitNetwork } from "@/hooks/use-circuits"

interface CircuitPickerProps {
  /** The current circuit_id (a gis.circuits uuid) or undefined for none. */
  value: unknown
  onChange: (value: unknown) => void
  /** The owning feature class's network (Phase 37 Slice 2). When set, only
   * that network's circuits are offered -- an MV apparatus joins an MV circuit.
   * When absent (a class not network-classified) every circuit is offered,
   * grouped by network so the two namespaces stay visually distinct. */
  network?: CircuitNetwork
}

/** A circuit's display label -- name plus its voltage when known, so two
 * circuits that only differ by voltage are still tellable apart. */
function circuitLabel(c: Circuit): string {
  return c.voltage_kv != null ? `${c.name} (${c.voltage_kv} kV)` : c.name
}

/**
 * The control the create/edit form renders for a `circuit_id` field instead of
 * a bare uuid text box: a select over the gis.circuits catalog (useCircuits),
 * scoped to the class's network. The stored value is the circuit's uuid; the
 * option text is the human name (+voltage). Membership is normally set by
 * Slice 3's assign-by-trace -- this is the manual override / initial pick.
 *
 * The catalog is small reference data already cached by useCircuits, so no
 * search box is needed; a plain grouped/filtered <select> is enough.
 */
export function CircuitPicker({ value, onChange, network }: CircuitPickerProps) {
  const { data, isLoading, isError } = useCircuits()

  const { scoped, grouped } = useMemo(() => {
    const all = data?.circuits ?? []
    if (network) {
      return { scoped: all.filter((c) => c.network === network), grouped: null }
    }
    // No class network -- show all, grouped by namespace.
    const byNetwork: Record<CircuitNetwork, Circuit[]> = { MV: [], LV: [] }
    for (const c of all) byNetwork[c.network].push(c)
    return { scoped: null, grouped: byNetwork }
  }, [data, network])

  const selected = typeof value === "string" ? value : ""

  // A referenced circuit that isn't in the (possibly network-filtered) list --
  // e.g. its network no longer matches the class -- would otherwise render as a
  // blank select and be silently dropped on save. Keep it selectable so an
  // edit form round-trips the existing membership untouched.
  const known = data?.circuits?.some((c) => c.id === selected) ?? false

  return (
    <select
      className={inputClass}
      value={selected}
      disabled={isLoading}
      onChange={(e) => onChange(e.target.value || undefined)}
    >
      <option value="">{isLoading ? "Loading circuits…" : isError ? "(couldn't load circuits)" : "—"}</option>
      {selected && !known && <option value={selected}>(current circuit)</option>}
      {scoped &&
        scoped.map((c) => (
          <option key={c.id} value={c.id}>
            {circuitLabel(c)}
          </option>
        ))}
      {grouped &&
        (["MV", "LV"] as const).map((net) =>
          grouped[net].length === 0 ? null : (
            <optgroup key={net} label={net}>
              {grouped[net].map((c) => (
                <option key={c.id} value={c.id}>
                  {circuitLabel(c)}
                </option>
              ))}
            </optgroup>
          ),
        )}
    </select>
  )
}
