import { inputClass } from "@/components/map/field-utils"
import type { FieldSchema } from "@/hooks/use-feature-class-schema"

interface FieldInputProps {
  field: FieldSchema
  value: unknown
  onChange: (value: unknown) => void
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
export function FieldInput({ field, value, onChange }: FieldInputProps) {
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
