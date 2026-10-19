import { inputClass } from "@/components/map/field-utils"
import { useProtectionCurves, type ProtectionCurve } from "@/hooks/use-protection-curves"

interface CurvePickerProps {
  /** The current curve code (a metadata.protection_curves.code) or undefined. */
  value: unknown
  onChange: (value: unknown) => void
  /** Which curves to offer: "ampere" for a fuse's curve_ref, "multiple" for a
   * relay curve. The stored value is the curve's code either way. */
  quantity: "ampere" | "multiple"
}

/** A curve's display label -- name plus its standard when that adds information
 * (a bare "FUSE" standard is just noise next to the name). */
function curveLabel(c: ProtectionCurve): string {
  return c.standard && c.standard !== "FUSE" ? `${c.name} (${c.standard})` : c.name
}

/**
 * The control the create/edit form renders for a curve-code field (a fuse's
 * `curve_ref`) instead of a bare text box: a select over the protection-curve
 * catalog (useProtectionCurves), filtered to the curves that fit the field.
 * The stored value is the curve's code -- exactly what a free-text box held, so
 * the coordination engine's code-join is unchanged -- but it can only ever be a
 * code that actually resolves, which kills the silent "typo -> curve never
 * found -> operate time unresolved" failure the free text allowed.
 *
 * Special-cased by field name in feature-field-input.tsx, the same way
 * circuit_id rides a CircuitPicker. Degrades sanely: while the catalog loads
 * the box is disabled, and a value already stored but not in the (filtered)
 * list stays selectable so an edit form round-trips it untouched.
 */
export function CurvePicker({ value, onChange, quantity }: CurvePickerProps) {
  const { data, isLoading, isError } = useProtectionCurves(quantity)
  const curves = data?.curves ?? []
  const selected = typeof value === "string" ? value : ""
  const known = curves.some((c) => c.code === selected)

  return (
    <select
      className={inputClass}
      value={selected}
      disabled={isLoading}
      onChange={(e) => onChange(e.target.value || undefined)}
    >
      <option value="">
        {isLoading ? "Loading curves…" : isError ? "(couldn't load curves)" : "—"}
      </option>
      {selected && !known && <option value={selected}>{selected} (current)</option>}
      {curves.map((c) => (
        <option key={c.code} value={c.code}>
          {curveLabel(c)}
        </option>
      ))}
    </select>
  )
}
