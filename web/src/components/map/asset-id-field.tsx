import { useEffect } from "react"
import { CheckCircle2, Loader2, XCircle } from "lucide-react"

import { inputClass } from "@/components/map/field-utils"
import { useFeatureRegistryLookup } from "@/hooks/use-feature-registry"

/** An asset-id text input that resolves the id against runtime.feature_registry
 * as the user types (or pastes) it, so they get immediate confirmation of what
 * the id is -- and its class auto-fills -- instead of blindly typing a uuid into
 * a bare (asset_class, asset_id) reference and only finding out at submit whether
 * it exists. The first consumer of the feature-registry index.
 *
 * On a successful resolve it calls onResolveClass with the feature's class code
 * so a paired asset-class field can populate itself. The lookup is disabled until
 * the id is at least 8 chars (see the hook), so it doesn't 404 on every keystroke.
 */
export function AssetIdField({
  value,
  onChange,
  onResolveClass,
  placeholder,
}: {
  value: string
  onChange: (id: string) => void
  onResolveClass?: (classCode: string) => void
  placeholder?: string
}) {
  const trimmed = value.trim()
  const { data, isFetching } = useFeatureRegistryLookup(trimmed)

  // Push the resolved class up to the paired asset-class field once the lookup
  // lands -- in an effect, never during render (a child must not setState on its
  // parent while rendering). onResolveClass no-ops when the class is unchanged.
  const resolvedClass = data?.feature_class_code
  useEffect(() => {
    if (resolvedClass && onResolveClass) onResolveClass(resolvedClass)
  }, [resolvedClass, onResolveClass])

  return (
    <div className="flex flex-col gap-1">
      <input
        className={inputClass}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder={placeholder ?? "UUID of the asset"}
        spellCheck={false}
      />
      {trimmed.length >= 8 && (
        <div className="flex items-center gap-1.5 text-xs">
          {isFetching ? (
            <span className="flex items-center gap-1.5 text-muted-foreground">
              <Loader2 className="h-3 w-3 animate-spin" /> Resolving…
            </span>
          ) : data ? (
            <span className="flex items-center gap-1.5 text-emerald-600 dark:text-emerald-400">
              <CheckCircle2 className="h-3 w-3" />
              {data.class_name ?? data.feature_class_code}
              {data.deleted_at && <span className="text-amber-600 dark:text-amber-400">(deleted)</span>}
            </span>
          ) : (
            <span className="flex items-center gap-1.5 text-muted-foreground">
              <XCircle className="h-3 w-3" /> No asset with this id
            </span>
          )}
        </div>
      )}
    </div>
  )
}
