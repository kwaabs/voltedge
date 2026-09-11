import { Fragment, useMemo, useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { AlertTriangle, Loader2, Shapes } from "lucide-react"
import { Link } from "react-router"

import { inputClass } from "@/components/map/field-utils"
import { Card, CardHeader } from "@/components/ui/card"
import { usePermission } from "@/hooks/use-auth"
import {
  type AdminDiagramStencil,
  type AdminFeatureClassSummary,
  useAdminDiagramStencils,
  useAdminFeatureClasses,
  useAdminSymbologyIcons,
} from "@/hooks/use-admin"
import { ApiError, apiFetch } from "@/lib/api"
import { useToastStore } from "@/stores/toast-store"

/** The tint applied to a stencil's {{COLOR}} placeholder in the list preview
 * -- the same neutral slate the canvas paints an un-recolored symbol, so a
 * glyph here reads the way it does when stamped. Full-color artwork ignores
 * it, exactly as on the canvas. */
const PREVIEW_COLOR = "#334155"

/** Render a stencil's SVG markup to a tinted data URI -- the same {{COLOR}}
 * substitution iconDataUri does, inlined here so the editor can show the real
 * glyph next to each catalog row without pulling in the map's icon registry. */
function glyphUri(svg: string, color: string): string {
  return `data:image/svg+xml;utf8,${encodeURIComponent(svg.replaceAll("{{COLOR}}", color))}`
}

/**
 * One catalog row: the glyph, its label/category, and a picker for the
 * feature class a stamped symbol of it promotes into (Phase 25). Saving is a
 * single PATCH on change (no dialog -- there's exactly one field), invalidating
 * both the admin list and the public ["diagram-stencils"] the canvas palette
 * reads, so a mapping goes live on the canvas immediately. Only geometry-none
 * classes are offered (the backend enforces the same rule); a chosen class
 * that isn't diagram-placeable yet gets a warning, since promotion of it will
 * fail at placement time until that flag is set on the Feature Classes page.
 */
function StencilRow({
  stencil,
  glyphSvg,
  noneGeometryClasses,
  canWrite,
}: {
  stencil: AdminDiagramStencil
  glyphSvg: string | undefined
  noneGeometryClasses: AdminFeatureClassSummary[]
  canWrite: boolean
}) {
  const [saving, setSaving] = useState(false)
  const queryClient = useQueryClient()
  const current = stencil.promote_class ?? ""

  // The picker offers every geometry-none class. If this stencil is already
  // mapped to a class that somehow isn't in that set (e.g. authored by SQL
  // against a spatial class before this editor existed), keep the current
  // value selectable so the row still shows -- and clearing it stays possible.
  const codes = new Set(noneGeometryClasses.map((c) => c.code))
  const selectedClass = noneGeometryClasses.find((c) => c.code === current)
  const showsOrphanCurrent = current !== "" && !codes.has(current)
  const notPlaceable = selectedClass !== undefined && !selectedClass.is_diagram_placeable

  async function onChange(next: string) {
    setSaving(true)
    try {
      await apiFetch(`/admin/symbology-stencils/${stencil.icon_ref}`, {
        method: "PATCH",
        body: JSON.stringify({ promote_class: next }),
      })
      queryClient.invalidateQueries({ queryKey: ["admin", "symbology-stencils"] })
      queryClient.invalidateQueries({ queryKey: ["diagram-stencils"] })
    } catch (err) {
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not update this mapping.")
    } finally {
      setSaving(false)
    }
  }

  return (
    <Card>
      <CardHeader className="flex flex-row items-center gap-3 py-3">
        <div className="bg-muted/40 flex size-10 shrink-0 items-center justify-center rounded-md border">
          {glyphSvg ? (
            <img src={glyphUri(glyphSvg, PREVIEW_COLOR)} alt={stencil.icon_ref} className="size-7" />
          ) : (
            <Shapes className="text-muted-foreground size-5" />
          )}
        </div>
        <div className="min-w-0 flex-1">
          <div className="text-sm font-medium">{stencil.label}</div>
          <div className="text-muted-foreground font-mono text-xs">{stencil.icon_ref}</div>
        </div>
        <div className="flex flex-col items-end gap-1">
          <div className="flex items-center gap-2">
            {saving && <Loader2 className="text-muted-foreground size-3.5 animate-spin" />}
            <select
              className={`${inputClass} w-56`}
              value={current}
              disabled={!canWrite || saving}
              onChange={(e) => onChange(e.target.value)}
            >
              <option value="">— decoration only —</option>
              {showsOrphanCurrent && <option value={current}>{current}</option>}
              {noneGeometryClasses.map((c) => (
                <option key={c.code} value={c.code}>
                  {c.name} ({c.code})
                </option>
              ))}
            </select>
          </div>
          {notPlaceable && (
            <div className="text-muted-foreground flex items-center gap-1 text-xs">
              <AlertTriangle className="text-amber-500 size-3.5" />
              <span>
                Not diagram-placeable yet — enable it on{" "}
                <Link to={`/admin/feature-classes/${current}`} className="underline">
                  Feature Classes
                </Link>{" "}
                or promotion will fail.
              </span>
            </div>
          )}
        </div>
      </CardHeader>
    </Card>
  )
}

/**
 * The stencil promote-mapping editor (Phase 25 Slice 2). Every diagram stencil
 * (metadata.diagram_stencils) can name a feature class it promotes into: on the
 * diagram canvas, double-clicking a stamped symbol of a mapped stencil turns it
 * into a real feature of that class. This page is where those mappings are set,
 * point-and-click, instead of by seed SQL. The target must be a geometry-none
 * (diagram-native) class; the picker only offers those, and the server enforces
 * it. See docs/phase-25-stencil-promotion.md.
 */
export function AdminStencilsPage() {
  const { data, isPending, isError } = useAdminDiagramStencils()
  const { data: classesData } = useAdminFeatureClasses()
  const { data: iconsData } = useAdminSymbologyIcons()
  // Set/clear here is metadata:write on the server; this page opens on
  // metadata:read, so gate the picker to match (see admin-symbology-icons.tsx).
  const canWrite = usePermission()("metadata:write")

  const glyphByRef = useMemo(() => {
    const map: Record<string, string> = {}
    for (const icon of iconsData?.symbology_icons ?? []) map[icon.icon_ref] = icon.svg
    return map
  }, [iconsData])

  const noneGeometryClasses = useMemo(
    () => (classesData?.feature_classes ?? []).filter((c) => c.geometry_type === "none"),
    [classesData],
  )

  // Preserve the server's category, sort_order ordering while grouping into
  // category sections for display (the list arrives already ordered).
  const groups = useMemo(() => {
    const out: { category: string; stencils: AdminDiagramStencil[] }[] = []
    for (const s of data?.stencils ?? []) {
      const last = out[out.length - 1]
      if (last && last.category === s.category) last.stencils.push(s)
      else out.push({ category: s.category, stencils: [s] })
    }
    return out
  }, [data])

  return (
    <div className="h-full overflow-y-auto p-4">
      <div className="mx-auto flex max-w-3xl flex-col gap-4">
        <div>
          <h1 className="text-lg font-semibold">Stencil promotion</h1>
          <p className="text-muted-foreground text-sm">
            Map a diagram stencil to a feature class to make it promotable: on the diagram, double-clicking a stamped
            symbol turns it into a real feature of that class. Only diagram-native (geometry-less) classes can be
            targets.
          </p>
        </div>

        {isPending ? (
          <div className="text-muted-foreground flex items-center gap-2 text-sm">
            <Loader2 className="size-4 animate-spin" />
            Loading stencils...
          </div>
        ) : isError || !data ? (
          <p className="text-destructive text-sm">Could not load stencils.</p>
        ) : data.stencils.length === 0 ? (
          <p className="text-muted-foreground text-sm">No stencils in the catalog yet.</p>
        ) : noneGeometryClasses.length === 0 ? (
          <p className="text-muted-foreground text-sm">
            No geometry-less feature classes exist yet, so there&apos;s nothing a stencil could promote into. Create a{" "}
            <span className="font-mono">geometry_type = none</span> class first on Feature Classes.
          </p>
        ) : (
          <div className="flex flex-col gap-4">
            {groups.map((group) => (
              <Fragment key={group.category}>
                <div className="text-muted-foreground text-xs font-semibold tracking-wide uppercase">
                  {group.category}
                </div>
                <div className="flex flex-col gap-2">
                  {group.stencils.map((stencil) => (
                    <StencilRow
                      key={stencil.icon_ref}
                      stencil={stencil}
                      glyphSvg={glyphByRef[stencil.icon_ref]}
                      noneGeometryClasses={noneGeometryClasses}
                      canWrite={canWrite}
                    />
                  ))}
                </div>
              </Fragment>
            ))}
          </div>
        )}
      </div>
    </div>
  )
}
