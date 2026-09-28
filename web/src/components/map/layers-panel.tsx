import { Globe, Info, LandPlot, Layers, PencilLine, Shapes, Siren, Tag, Trash2, ZapOff } from "lucide-react"

import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuCheckboxItem,
  DropdownMenuContent,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { useAllLabelDefs, useAllSymbology } from "@/hooks/use-all-symbology"
import { usePermission } from "@/hooks/use-auth"
import { useCatalog } from "@/hooks/use-catalog"
import { useFeatureClasses } from "@/hooks/use-feature-classes"
import { useSymbologyIcons } from "@/hooks/use-symbology-icons"
import { useServiceLayers } from "@/hooks/use-service-layers"
import { useTerritoryLevels } from "@/hooks/use-territories"
import { FAULT_STATUS_COLORS, INCIDENT_STATUS_COLORS, territoryColorForRank } from "@/lib/map-layers"
import { featureClassSourceIds } from "@/lib/martin"
import { isRenderableServiceType } from "@/lib/service-layers-source"
import { iconDataUri } from "@/lib/symbology-icons"
import { resolveSymbology } from "@/lib/symbology"
import { cn } from "@/lib/utils"
import { FAULT_STATUS_ORDER, useFaultOverlayStore } from "@/stores/fault-overlay-store"
import { INCIDENT_STATUS_ORDER, useOmsOverlayStore } from "@/stores/oms-overlay-store"
import { useImportedOverlaysStore } from "@/stores/imported-overlays-store"
import { useLayersStore } from "@/stores/layers-store"
import { useMapAnnotationsStore } from "@/stores/map-annotations-store"
import { useServiceLayersStore } from "@/stores/service-layers-store"
import { useTerritoryOverlayStore } from "@/stores/territory-overlay-store"
import { useToolbarMenuStore } from "@/stores/toolbar-menu-store"

/** The mandatory class-level default rule's preview -- an icon for a
 * point class (icon_ref set), or a plain color swatch for a line/
 * polygon class (no icon_ref concept for a geometry that isn't a
 * discrete point, see symbology-icons.ts). Resolved with no subtype/
 * attributes, same as asking "what does this class look like with
 * nothing more specific known" -- exactly a legend entry's job. `null`
 * when the class has no symbology_rules at all yet (not seeded), so
 * the row just shows its name with no swatch rather than a broken one. */
function LegendSwatch({
  classCode,
  rules,
  icons,
}: {
  classCode: string
  rules: ReturnType<typeof useAllSymbology>
  icons: ReturnType<typeof useSymbologyIcons>
}) {
  const styleDef = resolveSymbology(rules[classCode], undefined, {}, "map")
  if (!styleDef) return null
  if (styleDef.icon_ref) {
    return <img src={iconDataUri(styleDef.icon_ref, styleDef.color, icons)} alt="" className="size-4 shrink-0" />
  }
  const color = styleDef.fill_color ?? styleDef.stroke_color ?? styleDef.color
  if (!color) return null
  return <span className="size-3 shrink-0 rounded-sm" style={{ backgroundColor: color }} />
}

/**
 * A real layer visibility control, not the read-only "N layers" count
 * this replaced -- one checkbox per Martin catalog entry (i.e. per
 * compiled feature class), independent of whichever page/route you're
 * on. onSelect is prevented on each item so toggling one layer doesn't
 * close the menu, letting several be flipped in one go. Each row also
 * shows that class's resolved default symbology (see LegendSwatch) --
 * the map's own legend, piggybacked on this existing control instead
 * of a separate standalone panel.
 */
export function LayersPanel() {
  const { data: catalog } = useCatalog()
  const { data: featureClasses } = useFeatureClasses()
  const symbologyByClassCode = useAllSymbology()
  const labelDefsByClassCode = useAllLabelDefs()
  const symbologyIcons = useSymbologyIcons()
  const hidden = useLayersStore((s) => s.hidden)
  const toggle = useLayersStore((s) => s.toggle)
  const labelsHidden = useLayersStore((s) => s.labelsHidden)
  const toggleLabels = useLayersStore((s) => s.toggleLabels)
  const territoryOverlayVisible = useTerritoryOverlayStore((s) => s.visible)
  const toggleTerritoryOverlay = useTerritoryOverlayStore((s) => s.toggle)
  const hiddenLevels = useTerritoryOverlayStore((s) => s.hiddenLevels)
  const toggleTerritoryLevel = useTerritoryOverlayStore((s) => s.toggleLevel)
  const territoryLabelsVisible = useTerritoryOverlayStore((s) => s.showLabels)
  const toggleTerritoryLabels = useTerritoryOverlayStore((s) => s.toggleLabels)
  const territoryInspectLocked = useTerritoryOverlayStore((s) => s.inspectLocked)
  const toggleTerritoryInspectLock = useTerritoryOverlayStore((s) => s.toggleInspectLock)
  const importedOverlays = useImportedOverlaysStore((s) => s.overlays)
  const toggleImported = useImportedOverlaysStore((s) => s.toggle)
  const removeImported = useImportedOverlaysStore((s) => s.remove)
  const annotationsVisible = useMapAnnotationsStore((s) => s.visible)
  const toggleAnnotations = useMapAnnotationsStore((s) => s.toggle)
  const faultOverlayVisible = useFaultOverlayStore((s) => s.visible)
  const toggleFaultOverlay = useFaultOverlayStore((s) => s.toggleVisible)
  const faultStatuses = useFaultOverlayStore((s) => s.statuses)
  const setFaultStatus = useFaultOverlayStore((s) => s.setStatus)
  const faultPulse = useFaultOverlayStore((s) => s.pulse)
  const setFaultPulse = useFaultOverlayStore((s) => s.setPulse)
  const omsOverlayVisible = useOmsOverlayStore((s) => s.visible)
  const toggleOmsOverlay = useOmsOverlayStore((s) => s.toggleVisible)
  const omsStatuses = useOmsOverlayStore((s) => s.statuses)
  const setOmsStatus = useOmsOverlayStore((s) => s.setStatus)
  const omsPulse = useOmsOverlayStore((s) => s.pulse)
  const setOmsPulse = useOmsOverlayStore((s) => s.setPulse)
  const { data: serviceLayerRecords } = useServiceLayers()
  const serviceLayerVisible = useServiceLayersStore((s) => s.visible)
  const toggleServiceLayer = useServiceLayersStore((s) => s.toggle)
  const serviceLayerOpacity = useServiceLayersStore((s) => s.opacity)
  const setServiceLayerOpacity = useServiceLayersStore((s) => s.setOpacity)
  const can = usePermission()
  const canReadFaults = can("faults:read")
  const canReadOms = can("oms:read")
  const { data: territoryLevels } = useTerritoryLevels()
  // Territories are auto-published by Martin but aren't a feature class --
  // they live in the Overlays section below, not this list (see
  // featureClassSourceIds).
  const sourceIds = featureClassSourceIds(catalog)
  // Only one toolbar dropdown open at a time -- see toolbar-menu-store.ts.
  const menuOpen = useToolbarMenuStore((s) => s.open === "layers")
  const setMenuOpen = useToolbarMenuStore((s) => s.setOpen)

  // Top-first (utility -> plot), matching the map's own rank ordering.
  const levels = [...(territoryLevels?.levels ?? [])].sort((a, b) => a.rank - b.rank)

  // Martin's source id is always the lowercased feature class code
  // (see api/internal/compiler/compiler.go: tableName :=
  // strings.ToLower(fc.Code)) -- this is what maps a bare tile source
  // id like "mv_conductor" back to its real display name.
  const nameByLowercaseCode = new Map(
    (featureClasses?.feature_classes ?? []).map((fc) => [fc.code.toLowerCase(), fc.name]),
  )

  return (
    <DropdownMenu open={menuOpen} onOpenChange={(o) => setMenuOpen("layers", o)}>
      <DropdownMenuTrigger asChild>
        <Button variant="outline" size="sm" className="bg-background/90 gap-1.5 shadow-sm">
          <Layers className="size-4 text-indigo-600" />
          Layers
        </Button>
      </DropdownMenuTrigger>
      {/* Cap the menu at a bounded height and scroll -- the feature-class list
          grows with every compiled class (20+ here) and would otherwise run off
          the bottom of the viewport. max-h-[70vh] keeps it usable on short
          screens; the whole list (headers included) scrolls as one. */}
      <DropdownMenuContent align="end" className="max-h-[70vh] w-72 overflow-y-auto">
        <DropdownMenuLabel>Feature classes</DropdownMenuLabel>
        <DropdownMenuSeparator />
        {sourceIds.length === 0 ? (
          <p className="text-muted-foreground px-2 py-1.5 text-sm">No layers compiled yet</p>
        ) : (
          sourceIds.map((id) => {
            // Labels are only meaningful for a class that actually has a
            // label template (Phase 30); the toggle is hidden otherwise so
            // it never offers to turn off something that was never on.
            const hasLabel = Boolean(labelDefsByClassCode[id.toUpperCase()]?.template)
            const labelsOn = !labelsHidden[id]
            return (
              <DropdownMenuCheckboxItem
                key={id}
                checked={!hidden[id]}
                onSelect={(e) => e.preventDefault()}
                onCheckedChange={() => toggle(id)}
              >
                <span className="flex min-w-0 flex-1 items-center gap-1.5">
                  <LegendSwatch classCode={id.toUpperCase()} rules={symbologyByClassCode} icons={symbologyIcons} />
                  <span className="truncate">{nameByLowercaseCode.get(id) ?? id}</span>
                  {hasLabel && (
                    <button
                      type="button"
                      aria-label={labelsOn ? `Hide ${nameByLowercaseCode.get(id) ?? id} labels` : `Show ${nameByLowercaseCode.get(id) ?? id} labels`}
                      title={labelsOn ? "Hide labels" : "Show labels"}
                      className={cn(
                        "ml-auto shrink-0",
                        labelsOn ? "text-foreground" : "text-muted-foreground/50",
                      )}
                      onClick={(e) => {
                        // Stop the row's own visibility toggle / menu close.
                        e.preventDefault()
                        e.stopPropagation()
                        toggleLabels(id)
                      }}
                    >
                      <Tag className="size-3.5" />
                    </button>
                  )}
                </span>
              </DropdownMenuCheckboxItem>
            )
          })
        )}

        {/* The territory boundary overlay (Phase 11 Slice H) -- a single
            non-feature-class layer, so its own section rather than a row
            in the list above. Same no-close-on-toggle behavior. */}
        <DropdownMenuLabel>Overlays</DropdownMenuLabel>
        <DropdownMenuSeparator />
        <DropdownMenuCheckboxItem
          checked={territoryOverlayVisible}
          onSelect={(e) => e.preventDefault()}
          onCheckedChange={() => toggleTerritoryOverlay()}
        >
          <span className="flex min-w-0 flex-1 items-center gap-1.5">
            <LandPlot className="text-primary size-4 shrink-0" />
            <span className="truncate">Territories</span>
            {/* Labels + inspect-on-click as trailing icon buttons on the parent
                row (not their own lines), only meaningful while the overlay is
                on. Each toggles its own state; clicking one must NOT fall through
                to the row's visibility checkbox / close the menu -- preventDefault
                + stopPropagation on onClick does that (matching the per-class
                Labels button above; no onPointerDown, which would defeat Radix's
                own select guard and let the row toggle anyway). */}
            {territoryOverlayVisible && (
              <span className="ml-auto flex shrink-0 items-center gap-1.5">
                <button
                  type="button"
                  aria-pressed={territoryLabelsVisible}
                  title={territoryLabelsVisible ? "Hide territory name labels" : "Show territory name labels"}
                  className={cn(territoryLabelsVisible ? "text-foreground" : "text-muted-foreground/50")}
                  onClick={(e) => {
                    e.preventDefault()
                    e.stopPropagation()
                    toggleTerritoryLabels()
                  }}
                >
                  <Tag className="size-3.5" />
                </button>
                <button
                  type="button"
                  aria-pressed={!territoryInspectLocked}
                  title={
                    territoryInspectLocked
                      ? "Click-to-inspect is off — turn it on to open a territory's details"
                      : "Click a territory to inspect its details — click here to lock that off"
                  }
                  className={cn(territoryInspectLocked ? "text-muted-foreground/40" : "text-primary")}
                  onClick={(e) => {
                    e.preventDefault()
                    e.stopPropagation()
                    toggleTerritoryInspectLock()
                  }}
                >
                  <Info className="size-3.5" />
                </button>
              </span>
            )}
          </span>
        </DropdownMenuCheckboxItem>

        {/* Per-level toggles (Phase 11 Slice J) -- only meaningful while the
            overlay is on, so shown only then, indented under the master
            switch. Each hides/shows one level (e.g. districts) without
            touching the others; the swatch matches territoryColorForRank so
            the row reads as the same color drawn on the map. */}
        {territoryOverlayVisible &&
          levels.map((level) => (
            <DropdownMenuCheckboxItem
              key={level.code}
              className="pl-11"
              checked={!hiddenLevels[level.code]}
              onSelect={(e) => e.preventDefault()}
              onCheckedChange={() => toggleTerritoryLevel(level.code)}
            >
              <span className="flex min-w-0 items-center gap-1.5">
                <span
                  className="size-3 shrink-0 rounded-sm"
                  style={{ backgroundColor: territoryColorForRank(level.rank) }}
                />
                <span className="truncate">{level.name}</span>
              </span>
            </DropdownMenuCheckboxItem>
          ))}

        {/* The map-annotation (redlining) overlay (Phase 36) -- a single
            non-feature layer of free-form markup, so a row in this Overlays
            section beside Territories. Same no-close-on-toggle behavior. */}
        <DropdownMenuCheckboxItem
          checked={annotationsVisible}
          onSelect={(e) => e.preventDefault()}
          onCheckedChange={() => toggleAnnotations()}
        >
          <span className="flex min-w-0 items-center gap-1.5">
            <PencilLine className="size-4 shrink-0" style={{ color: "#dc2626" }} />
            <span className="truncate">Annotations</span>
          </span>
        </DropdownMenuCheckboxItem>

        {/* The fault-event overlay (Faults Slice 3) -- fault markers colored by
            status, drawn from /faults/map. A row in this Overlays section, gated
            on faults:read (the data endpoint is too). The per-status toggles
            below double as the legend, defaulting to the live statuses; the map
            filters client-side so revealing cleared/closed never refetches. */}
        {canReadFaults && (
          <DropdownMenuCheckboxItem
            checked={faultOverlayVisible}
            onSelect={(e) => e.preventDefault()}
            onCheckedChange={() => toggleFaultOverlay()}
          >
            <span className="flex min-w-0 items-center gap-1.5">
              <ZapOff className="size-4 shrink-0" style={{ color: FAULT_STATUS_COLORS.detected }} />
              <span className="truncate">Faults</span>
            </span>
          </DropdownMenuCheckboxItem>
        )}

        {canReadFaults &&
          faultOverlayVisible &&
          FAULT_STATUS_ORDER.map((status) => (
            <DropdownMenuCheckboxItem
              key={status}
              className="pl-11"
              checked={faultStatuses[status]}
              onSelect={(e) => e.preventDefault()}
              onCheckedChange={(checked) => setFaultStatus(status, checked === true)}
            >
              <span className="flex min-w-0 items-center gap-1.5">
                <span
                  className="size-3 shrink-0 rounded-sm"
                  style={{ backgroundColor: FAULT_STATUS_COLORS[status] }}
                />
                <span className="truncate capitalize">{status}</span>
              </span>
            </DropdownMenuCheckboxItem>
          ))}

        {/* Pulse the live (detected/investigating) fault markers to draw the eye.
            The OS "reduce motion" setting suppresses it regardless. */}
        {canReadFaults && faultOverlayVisible && (
          <DropdownMenuCheckboxItem
            className="pl-11"
            checked={faultPulse}
            onSelect={(e) => e.preventDefault()}
            onCheckedChange={(checked) => setFaultPulse(checked === true)}
          >
            <span className="truncate">Pulse live faults</span>
          </DropdownMenuCheckboxItem>
        )}

        {/* The OMS outage overlay -- the twin of the fault overlay above. Incident
            markers colored by status, drawn from /oms/incidents/map on their
            predicted device; clicking one draws both outage cones (frozen affected
            + live de-energized). Gated on oms:read (the data endpoint is too). The
            per-status toggles below double as the legend, defaulting to the open
            statuses; the map filters client-side so revealing restored/cancelled
            never refetches. */}
        {canReadOms && (
          <DropdownMenuCheckboxItem
            checked={omsOverlayVisible}
            onSelect={(e) => e.preventDefault()}
            onCheckedChange={() => toggleOmsOverlay()}
          >
            <span className="flex min-w-0 items-center gap-1.5">
              <Siren className="size-4 shrink-0" style={{ color: INCIDENT_STATUS_COLORS.confirmed }} />
              <span className="truncate">Outages</span>
            </span>
          </DropdownMenuCheckboxItem>
        )}

        {canReadOms &&
          omsOverlayVisible &&
          INCIDENT_STATUS_ORDER.map((status) => (
            <DropdownMenuCheckboxItem
              key={status}
              className="pl-11"
              checked={omsStatuses[status]}
              onSelect={(e) => e.preventDefault()}
              onCheckedChange={(checked) => setOmsStatus(status, checked === true)}
            >
              <span className="flex min-w-0 items-center gap-1.5">
                <span
                  className="size-3 shrink-0 rounded-sm"
                  style={{ backgroundColor: INCIDENT_STATUS_COLORS[status] }}
                />
                <span className="truncate capitalize">{status}</span>
              </span>
            </DropdownMenuCheckboxItem>
          ))}

        {/* Pulse the live (predicted/confirmed/dispatched) incident markers to
            draw the eye. The OS "reduce motion" setting suppresses it regardless. */}
        {canReadOms && omsOverlayVisible && (
          <DropdownMenuCheckboxItem
            className="pl-11"
            checked={omsPulse}
            onSelect={(e) => e.preventDefault()}
            onCheckedChange={(checked) => setOmsPulse(checked === true)}
          >
            <span className="truncate">Pulse live outages</span>
          </DropdownMenuCheckboxItem>
        )}

        {/* External service layers (Slice 2) -- admin-catalogued 3rd-party
            WMS/WMTS/XYZ/Esri overlays. Only shown once at least one enabled layer
            exists. Each row is an opt-in visibility toggle (default off) with an
            indented opacity slider while visible. A type this build can't render
            yet (vector: wfs/esri_feature/vector_tile -> Slice 3) shows a disabled
            row so the catalog stays visible without offering a no-op toggle. */}
        {serviceLayerRecords && serviceLayerRecords.length > 0 && (
          <>
            <DropdownMenuLabel>Service layers</DropdownMenuLabel>
            <DropdownMenuSeparator />
            {serviceLayerRecords.map((layer) => {
              const renderable = isRenderableServiceType(layer.service_type)
              const isVisible = Boolean(serviceLayerVisible[layer.id])
              const opacity = serviceLayerOpacity[layer.id] ?? layer.default_opacity
              return (
                <div key={layer.id}>
                  <DropdownMenuCheckboxItem
                    checked={renderable && isVisible}
                    disabled={!renderable}
                    onSelect={(e) => e.preventDefault()}
                    onCheckedChange={() => renderable && toggleServiceLayer(layer.id)}
                  >
                    <span className="flex min-w-0 flex-1 items-center gap-1.5">
                      <Globe className="size-4 shrink-0 text-sky-600" />
                      <span className="truncate" title={layer.label}>
                        {layer.label}
                      </span>
                      <span className="text-muted-foreground ml-auto shrink-0 text-[10px] uppercase">
                        {renderable ? layer.service_type.replace("_", " ") : "soon"}
                      </span>
                    </span>
                  </DropdownMenuCheckboxItem>

                  {/* Opacity slider -- a plain row, not a menu item, so it can be
                      dragged; stop pointer/click propagation so dragging it never
                      toggles the row above or closes the menu. */}
                  {renderable && isVisible && (
                    <div
                      className="flex items-center gap-2 py-1.5 pr-2 pl-11"
                      onPointerDown={(e) => e.stopPropagation()}
                      onClick={(e) => e.stopPropagation()}
                    >
                      <input
                        type="range"
                        min={0}
                        max={1}
                        step={0.05}
                        value={opacity}
                        aria-label={`${layer.label} opacity`}
                        className="accent-primary h-1 flex-1 cursor-pointer"
                        onChange={(e) => setServiceLayerOpacity(layer.id, Number(e.target.value))}
                      />
                      <span className="text-muted-foreground w-8 shrink-0 text-right text-xs tabular-nums">
                        {Math.round(opacity * 100)}%
                      </span>
                    </div>
                  )}
                </div>
              )
            })}
          </>
        )}

        {/* Imported overlays (Phase 15 data import) -- user-supplied reference
            layers, each toggleable and removable. Only shown once at least one
            has been imported. The trash button removes the overlay outright
            (stopPropagation so it doesn't also toggle the row's checkbox). */}
        {importedOverlays.length > 0 && (
          <>
            <DropdownMenuLabel>Imported</DropdownMenuLabel>
            <DropdownMenuSeparator />
            {importedOverlays.map((overlay) => (
              <DropdownMenuCheckboxItem
                key={overlay.id}
                checked={overlay.visible}
                onSelect={(e) => e.preventDefault()}
                onCheckedChange={() => toggleImported(overlay.id)}
              >
                <span className="flex min-w-0 flex-1 items-center gap-1.5">
                  <Shapes className="size-4 shrink-0" style={{ color: "#f59e0b" }} />
                  <span className="truncate" title={`${overlay.name} (${overlay.featureCount})`}>
                    {overlay.name}
                  </span>
                  <span className="text-muted-foreground ml-auto shrink-0 text-xs">
                    {overlay.featureCount}
                  </span>
                  <button
                    type="button"
                    aria-label={`Remove ${overlay.name}`}
                    className="text-muted-foreground hover:text-destructive shrink-0"
                    onClick={(e) => {
                      e.preventDefault()
                      e.stopPropagation()
                      removeImported(overlay.id)
                    }}
                  >
                    <Trash2 className="size-3.5" />
                  </button>
                </span>
              </DropdownMenuCheckboxItem>
            ))}
          </>
        )}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
