import { Fragment, useEffect, useState } from "react"
import { Info, Link2, Loader2, Plug, Plus, X } from "lucide-react"

import { FeatureCreateForm } from "@/components/map/feature-create-form"
import { Button } from "@/components/ui/button"
import { useConnections } from "@/hooks/use-connections"
import { useFeature } from "@/hooks/use-feature"
import { useFeatureClasses } from "@/hooks/use-feature-classes"
import { useRelationships } from "@/hooks/use-relationships"
import { cn } from "@/lib/utils"
import { useConnectStore } from "@/stores/connect-store"
import { useDrawStore } from "@/stores/draw-store"
import { useSelectionStore } from "@/stores/selection-store"

type TabId = "attributes" | "terminals" | "relationships"

const TABS: { id: TabId; label: string; icon: typeof Info }[] = [
  { id: "attributes", label: "Attributes", icon: Info },
  { id: "terminals", label: "Terminals", icon: Plug },
  { id: "relationships", label: "Relationships", icon: Link2 },
]

/** "condition_rating" -> "Condition rating" -- cheap and clearly better
 * than the raw column name; not a stand-in for a real per-field display
 * label, which isn't modeled in metadata.fields yet. */
function formatFieldLabel(key: string): string {
  const spaced = key.replace(/_/g, " ")
  return spaced.charAt(0).toUpperCase() + spaced.slice(1)
}

function formatValue(value: unknown): string {
  if (value === null || value === undefined || value === "") return "—"
  if (typeof value === "boolean") return value ? "Yes" : "No"
  return String(value)
}

function shortId(id: string): string {
  return id.length > 8 ? `${id.slice(0, 8)}…` : id
}

function AttributesTab() {
  const selected = useSelectionStore((s) => s.selected)
  const { data: feature, isPending, isError, error } = useFeature(
    selected?.featureClassCode,
    selected?.featureId,
  )

  if (isPending) {
    return (
      <div className="text-muted-foreground flex items-center gap-2 text-sm">
        <Loader2 className="size-4 animate-spin" />
        Loading feature...
      </div>
    )
  }
  if (isError) {
    return (
      <p className="text-destructive text-sm">
        Could not load this feature{error instanceof Error ? `: ${error.message}` : "."}
      </p>
    )
  }
  if (!feature) return null

  return (
    <dl className="grid grid-cols-[max-content_1fr] gap-x-6 gap-y-1.5 text-sm">
      <dt className="text-muted-foreground">Status</dt>
      <dd className="capitalize">{feature.status}</dd>

      {feature.deleted_at && (
        <>
          <dt className="text-muted-foreground">Deleted</dt>
          <dd>{new Date(feature.deleted_at).toLocaleString()}</dd>
        </>
      )}

      {feature.subtype !== undefined && (
        <>
          <dt className="text-muted-foreground">Subtype</dt>
          <dd>{feature.subtype}</dd>
        </>
      )}

      {Object.entries(feature.attributes).map(([key, value]) => (
        <Fragment key={key}>
          <dt className="text-muted-foreground">{formatFieldLabel(key)}</dt>
          <dd>{formatValue(value)}</dd>
        </Fragment>
      ))}

      <dt className="text-muted-foreground">Updated</dt>
      <dd>{new Date(feature.updated_at).toLocaleString()}</dd>
    </dl>
  )
}

function TerminalsTab() {
  const selected = useSelectionStore((s) => s.selected)
  const { data: featureClasses } = useFeatureClasses()
  const { data, isPending, isError, error } = useConnections(
    selected?.featureClassCode,
    selected?.featureId,
  )

  const newConnectionButton = selected && (
    <Button
      variant="outline"
      size="sm"
      className="self-start"
      onClick={() => useConnectStore.getState().start("connectivity", selected)}
    >
      <Plus className="size-4" />
      New connection
    </Button>
  )

  if (isPending) {
    return (
      <div className="text-muted-foreground flex items-center gap-2 text-sm">
        <Loader2 className="size-4 animate-spin" />
        Loading connections...
      </div>
    )
  }
  if (isError) {
    return (
      <p className="text-destructive text-sm">
        Could not load connections{error instanceof Error ? `: ${error.message}` : "."}
      </p>
    )
  }
  if (!selected || !data || data.connections.length === 0) {
    return (
      <div className="flex flex-col items-start gap-2">
        {newConnectionButton}
        <p className="text-muted-foreground text-sm">No terminal connections.</p>
      </div>
    )
  }

  return (
    <div className="flex flex-col items-start gap-2">
      {newConnectionButton}
      <div className="flex w-full flex-col gap-2">
        {data.connections.map((conn) => {
          const isFrom =
            conn.from.feature_class === selected.featureClassCode && conn.from.feature_id === selected.featureId
          const counterpart = isFrom ? conn.to : conn.from
          const thisTerminal = isFrom ? conn.from.terminal : conn.to.terminal
          const counterpartName =
            featureClasses?.feature_classes.find((fc) => fc.code === counterpart.feature_class)?.name ??
            counterpart.feature_class

          return (
            <div key={conn.id} className="w-full rounded-md border p-2 text-sm">
              <div className="flex items-center justify-between gap-2">
                <span className="font-medium">{counterpartName}</span>
                <span className="text-muted-foreground shrink-0 text-xs">{isFrom ? "Outgoing" : "Incoming"}</span>
              </div>
              <p className="text-muted-foreground font-mono text-xs">{shortId(counterpart.feature_id)}</p>
              <p className="text-muted-foreground mt-1 text-xs">
                {thisTerminal && <>via {thisTerminal} · </>}
                {conn.network_rule_name}
              </p>
            </div>
          )
        })}
      </div>
    </div>
  )
}

function RelationshipsTab() {
  const selected = useSelectionStore((s) => s.selected)
  const { data: featureClasses } = useFeatureClasses()
  const { data, isPending, isError, error } = useRelationships(
    selected?.featureClassCode,
    selected?.featureId,
  )

  if (isPending) {
    return (
      <div className="text-muted-foreground flex items-center gap-2 text-sm">
        <Loader2 className="size-4 animate-spin" />
        Loading relationships...
      </div>
    )
  }
  if (isError) {
    return (
      <p className="text-destructive text-sm">
        Could not load relationships{error instanceof Error ? `: ${error.message}` : "."}
      </p>
    )
  }
  if (!selected || !data || data.relationships.length === 0) {
    return <p className="text-muted-foreground text-sm">No relationships.</p>
  }

  return (
    <div className="flex flex-col gap-2">
      {data.relationships.map((rel) => {
        const isOrigin =
          rel.origin.feature_class === selected.featureClassCode && rel.origin.feature_id === selected.featureId
        const counterpart = isOrigin ? rel.destination : rel.origin
        const counterpartName =
          featureClasses?.feature_classes.find((fc) => fc.code === counterpart.feature_class)?.name ??
          counterpart.feature_class

        return (
          <div key={rel.id} className="rounded-md border p-2 text-sm">
            <div className="flex items-center justify-between gap-2">
              <span className="font-medium">{counterpartName}</span>
              <span className="text-muted-foreground shrink-0 text-xs">{isOrigin ? "Origin" : "Destination"}</span>
            </div>
            <p className="text-muted-foreground font-mono text-xs">{shortId(counterpart.feature_id)}</p>
            <p className="text-muted-foreground mt-1 text-xs">
              {rel.relationship_class_name} · {rel.kind} · {rel.cardinality}
            </p>
          </div>
        )
      })}
    </div>
  )
}

/**
 * Docked to the bottom of the map (between the canvas and the status
 * bar), not floating -- undocking into a movable/resizable/minimizable
 * window is its own later slice, pinned for now. Renders nothing when
 * no feature is selected, so it doesn't reserve space it isn't using.
 *
 * Side tabs, not top or bottom ones -- this panel is short and wide
 * (fixed h-64, spans the map's width), so a vertical rail spends the
 * plentiful horizontal space instead of costing height a bottom tab
 * strip would take directly out of the already-cramped content area.
 */
export function FeaturePanel() {
  const selected = useSelectionStore((s) => s.selected)
  const clear = useSelectionStore((s) => s.clear)
  const draft = useDrawStore((s) => s.draft)
  const clearDraft = useDrawStore((s) => s.clearDraft)
  const { data: featureClasses } = useFeatureClasses()
  const [activeTab, setActiveTab] = useState<TabId>("attributes")

  // Land back on Attributes for every newly selected feature -- staying
  // on, say, Relationships while the id underneath changes would show
  // the new feature's relationships under a tab the user didn't
  // deliberately choose for it.
  useEffect(() => {
    setActiveTab("attributes")
  }, [selected?.featureClassCode, selected?.featureId])

  // An unsaved draft (see draw-store.ts) takes over the whole panel,
  // no tabs -- a feature with no id yet has no terminals or
  // relationships to show. Checked before `selected` so starting a new
  // draw while another feature is already selected still shows the
  // form, not that older feature's tabs.
  if (draft) {
    const className =
      featureClasses?.feature_classes.find((fc) => fc.code === draft.featureClassCode)?.name ??
      draft.featureClassCode

    return (
      <div className="bg-background flex h-64 shrink-0 flex-col border-t">
        <div className="flex items-center justify-between border-b px-3 py-2">
          <div className="min-w-0">
            <p className="truncate text-sm font-medium">New {className}</p>
            <p className="text-muted-foreground truncate text-xs">Unsaved</p>
          </div>
          <Button variant="ghost" size="icon" className="size-7 shrink-0" onClick={clearDraft}>
            <X className="size-4" />
            <span className="sr-only">Cancel</span>
          </Button>
        </div>
        <div className="flex-1 overflow-y-auto p-3">
          <FeatureCreateForm />
        </div>
      </div>
    )
  }

  if (!selected) return null

  const className =
    featureClasses?.feature_classes.find((fc) => fc.code === selected.featureClassCode)?.name ??
    selected.featureClassCode

  return (
    <div className="bg-background flex h-64 shrink-0 flex-col border-t">
      <div className="flex items-center justify-between border-b px-3 py-2">
        <div className="min-w-0">
          <p className="truncate text-sm font-medium">{className}</p>
          <p className="text-muted-foreground truncate font-mono text-xs">{selected.featureId}</p>
        </div>
        <Button variant="ghost" size="icon" className="size-7 shrink-0" onClick={clear}>
          <X className="size-4" />
          <span className="sr-only">Close</span>
        </Button>
      </div>

      <div className="flex min-h-0 flex-1">
        <div className="flex w-28 shrink-0 flex-col gap-0.5 border-r p-1">
          {TABS.map((tab) => (
            <button
              key={tab.id}
              type="button"
              onClick={() => setActiveTab(tab.id)}
              className={cn(
                "flex items-center gap-2 rounded-sm px-2 py-1.5 text-left text-xs font-medium",
                activeTab === tab.id
                  ? "bg-accent text-accent-foreground"
                  : "text-muted-foreground hover:bg-accent/50",
              )}
            >
              <tab.icon className="size-3.5 shrink-0" />
              {tab.label}
            </button>
          ))}
        </div>
        <div className="flex-1 overflow-y-auto p-3">
          {activeTab === "attributes" && <AttributesTab />}
          {activeTab === "terminals" && <TerminalsTab />}
          {activeTab === "relationships" && <RelationshipsTab />}
        </div>
      </div>
    </div>
  )
}
