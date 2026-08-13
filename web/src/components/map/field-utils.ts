/** "condition_rating" -> "Condition rating" -- cheap and clearly better
 * than the raw column name; not a stand-in for a real per-field display
 * label, which isn't modeled in metadata.fields yet. */
export function formatFieldLabel(key: string): string {
  const spaced = key.replace(/_/g, " ")
  return spaced.charAt(0).toUpperCase() + spaced.slice(1)
}

export const inputClass =
  "border-input bg-background h-8 w-full rounded-md border px-2 text-sm outline-none focus-visible:border-ring focus-visible:ring-ring/50 focus-visible:ring-[3px]"

/** Shared by feature-panel.tsx's read-only attribute list and
 * feature-edit-form.tsx's locked-field display -- the same "how do we
 * print this value" question, asked in two places. */
export function formatValue(value: unknown): string {
  if (value === null || value === undefined || value === "") return "—"
  if (typeof value === "boolean") return value ? "Yes" : "No"
  return String(value)
}
