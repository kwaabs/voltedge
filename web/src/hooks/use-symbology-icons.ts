import { useQuery } from "@tanstack/react-query"
import { useMemo } from "react"

import { apiFetch } from "@/lib/api"
import { FALLBACK_ICON_REFS, type IconRegistry } from "@/lib/symbology-icons"

interface SymbologyIconsResponse {
  icons: { icon_ref: string; svg: string }[]
}

/**
 * The whole symbology-icon set at once (GET /symbology/icons), keyed by
 * icon_ref -- the SVG markup behind every symbology rule's icon_ref
 * (metadata.symbology_icons, migrations/0023). The map needs it available
 * as soon as it starts rendering tiles, so like useAllSymbology this is a
 * single, long-cached global fetch, not on-demand per icon.
 *
 * The DB set is merged OVER the frontend's hardcoded fallback, so a
 * feature always resolves to *some* real shape even before this lands or
 * when a rule references a ref the DB happens not to have (see
 * symbology-icons.ts). Memoized on the raw payload so the map's
 * layer-creation effect only rebuilds when the icon set actually changes,
 * not on every render (same reasoning as useAllSymbology's memo).
 */
export function useSymbologyIcons(): IconRegistry {
  const { data } = useQuery({
    queryKey: ["symbology-icons"],
    queryFn: () => apiFetch<SymbologyIconsResponse>("/symbology/icons"),
    staleTime: 5 * 60_000,
  })

  return useMemo(() => {
    const out: IconRegistry = {}
    for (const icon of data?.icons ?? []) out[icon.icon_ref] = icon.svg
    return out
  }, [data])
}

/** Every icon_ref an admin rule editor can offer -- the DB's own refs
 * unioned with the hardcoded fallbacks, so the fallback shapes stay
 * pickable even before any DB icon is authored. */
export function useKnownIconRefs(): string[] {
  const icons = useSymbologyIcons()
  return useMemo(() => {
    const refs = new Set<string>(FALLBACK_ICON_REFS)
    for (const ref of Object.keys(icons)) refs.add(ref)
    return [...refs].sort()
  }, [icons])
}
