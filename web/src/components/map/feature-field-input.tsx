import { CircuitPicker } from "@/components/map/circuit-picker"
import { inputClass } from "@/components/map/field-utils"
import type { CircuitNetwork } from "@/hooks/use-circuits"
import type { FieldSchema } from "@/hooks/use-feature-class-schema"

interface FieldInputProps {
  field: FieldSchema
  value: unknown
  onChange: (value: unknown) => void
  /** The owning feature class's network (Phase 37 Slice 2), threaded through
   * only so the `circuit_id` field can render a network-scoped circuit picker.
   * Ignored by every other field. */
  network?: CircuitNetwork
}

/**
 * One form control per field, picked from its metadata data_type -- the
 * same contract api/internal/crud/validate.go's validateValue enforces
 * server-side, so a value that passes here should never bounce off a
 * 400 for its type alone. A coded domain always wins into a select,
 * regardless of the underlying data_type. Shared by
 * feature-create-form.tsx and feature-edit-form.tsx -- both render the
 * same field/data_type contract, just against a different starting
 * value and a different save verb (POST vs PATCH).
 */
export function FieldInput({ field, value, onChange, network }: FieldInputProps) {
  // circuit_id (Phase 37 Slice 2) is a uuid field, but a raw uuid text box is
  // useless -- a circuit is picked from the catalog by name, scoped to the
  // class's network. Special-cased by field name (the same way circuit_ref
  // rode the generic text path); everything else flows by data_type below.
  if (field.name === "circuit_id") {
    return <CircuitPicker value={value} onChange={onChange} network={network} />
  }

  if (field.domain_values) {
    return (
      <select
        className={inputClass}
        value={typeof value === "string" ? value : ""}
        onChange={(e) => onChange(e.target.value || undefined)}
      >
        <option value="">—</option>
        {field.domain_values.map((v) => (
          <option key={v} value={v}>
            {v}
          </option>
        ))}
      </select>
    )
  }

  switch (field.data_type) {
    case "boolean":
      return (
        <input
          type="checkbox"
          className="size-4"
          checked={Boolean(value)}
          onChange={(e) => onChange(e.target.checked)}
        />
      )
    case "integer":
    case "numeric":
      return (
        <input
          type="number"
          className={inputClass}
          value={typeof value === "number" ? value : ""}
          onChange={(e) => onChange(e.target.value === "" ? undefined : Number(e.target.value))}
          min={field.range_min}
          max={field.range_max}
        />
      )
    case "date":
      return (
        <input
          type="date"
          className={inputClass}
          value={typeof value === "string" ? value : ""}
          onChange={(e) => onChange(e.target.value || undefined)}
        />
      )
    case "timestamp":
      return (
        <input
          type="datetime-local"
          className={inputClass}
          value={typeof value === "string" ? value : ""}
          onChange={(e) => onChange(e.target.value ? new Date(e.target.value).toISOString() : undefined)}
        />
      )
    default:
      return (
        <input
          type="text"
          className={inputClass}
          value={typeof value === "string" ? value : ""}
          onChange={(e) => onChange(e.target.value || undefined)}
        />
      )
  }
}
