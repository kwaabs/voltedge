import { useMemo, useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { Ban, Loader2, Plus, Trash2, Workflow } from "lucide-react"

import { inputClass } from "@/components/map/field-utils"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { usePermission } from "@/hooks/use-auth"
import { useAdminDiagramPlacementRules, useAdminDomains, useAdminFeatureClasses } from "@/hooks/use-admin"
import { ApiError, apiFetch } from "@/lib/api"
import { useToastStore } from "@/stores/toast-store"

const DIAGRAM_TYPE_DOMAIN = "DIAGRAM_TYPE"
const RULES_QUERY_KEY = ["admin", "diagram-placement-rules"]

/**
 * Authors the per-(container class × diagram type) placement allow-list
 * (metadata.diagram_placement_rules, migration 0068). Placement is gated
 * first by the global is_diagram_placeable flag; this page narrows a
 * specific pairing on top of that. The rule set is ADDITIVE/opt-in: a
 * pairing with no rows accepts any placeable class (unchanged from before
 * an allow-list existed), and adding the first rule flips it to "only
 * these". Removing the last rule reverts it to unconstrained.
 *
 * Diagram types themselves are ordinary DIAGRAM_TYPE domain values,
 * authored on the Domains page -- this page only reads them.
 */
export function AdminDiagramsPage() {
  const { data: classesData, isPending: classesPending } = useAdminFeatureClasses()
  const { data: domainsData, isPending: domainsPending } = useAdminDomains()
  const { data: rulesData, isPending: rulesPending, isError } = useAdminDiagramPlacementRules()
  const queryClient = useQueryClient()
  // Add/remove are metadata:write on the server; this page opens on
  // metadata:read, so gate the write controls to match (same as the other
  // metadata admin pages).
  const canWrite = usePermission()("metadata:write")

  const containers = useMemo(
    () => (classesData?.feature_classes ?? []).filter((c) => c.is_diagram_container),
    [classesData],
  )
  const placeables = useMemo(
    () => (classesData?.feature_classes ?? []).filter((c) => c.is_diagram_placeable),
    [classesData],
  )
  const diagramTypes = useMemo(
    () => domainsData?.domains.find((d) => d.code === DIAGRAM_TYPE_DOMAIN)?.values ?? [],
    [domainsData],
  )
  const rules = rulesData?.diagram_placement_rules ?? []

  const [container, setContainer] = useState("")
  const [diagramType, setDiagramType] = useState("")
  const [addClass, setAddClass] = useState("")
  const [busy, setBusy] = useState(false)

  // Rules for the currently selected pairing -- the allow-list being edited.
  const pairingRules = useMemo(
    () => rules.filter((r) => r.container_class === container && r.diagram_type === diagramType),
    [rules, container, diagramType],
  )
  const allowedCodes = new Set(pairingRules.map((r) => r.feature_class))
  // Candidates to add: placeable classes not already on this pairing's list.
  const addCandidates = placeables.filter((c) => !allowedCodes.has(c.code))

  const pairingChosen = container !== "" && diagramType !== ""

  async function handleAdd() {
    if (!pairingChosen || addClass === "") return
    setBusy(true)
    try {
      await apiFetch("/admin/diagram-placement-rules", {
        method: "POST",
        body: JSON.stringify({
          container_class: container,
          diagram_type: diagramType,
          feature_class: addClass,
        }),
      })
      queryClient.invalidateQueries({ queryKey: RULES_QUERY_KEY })
      setAddClass("")
    } catch (err) {
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not allow this feature.")
    } finally {
      setBusy(false)
    }
  }

  async function handleRemove(id: string) {
    setBusy(true)
    try {
      await apiFetch(`/admin/diagram-placement-rules/${id}`, { method: "DELETE" })
      queryClient.invalidateQueries({ queryKey: RULES_QUERY_KEY })
    } catch (err) {
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not remove this rule.")
    } finally {
      setBusy(false)
    }
  }

  // All configured pairings (those that actually carry rules), grouped for
  // an at-a-glance overview under the editor.
  const configured = useMemo(() => {
    const byPairing = new Map<
      string,
      { container: string; containerName: string; type: string; typeLabel: string; features: typeof rules }
    >()
    for (const r of rules) {
      const key = `${r.container_class}::${r.diagram_type}`
      const existing = byPairing.get(key)
      if (existing) existing.features.push(r)
      else
        byPairing.set(key, {
          container: r.container_class,
          containerName: r.container_name,
          type: r.diagram_type,
          typeLabel: r.diagram_type_label,
          features: [r],
        })
    }
    return [...byPairing.values()]
  }, [rules])

  const loading = classesPending || domainsPending || rulesPending

  return (
    <div className="h-full overflow-y-auto p-4">
      <div className="flex w-full flex-col gap-4">
        <div>
          <h1 className="text-lg font-semibold">Diagram placement rules</h1>
          <p className="text-muted-foreground text-sm">
            Restrict which features can be placed on a diagram, per container class and diagram type. A pairing with no
            rules accepts any placeable feature; adding a rule limits it to exactly the allowed features. Diagram types
            are managed under <span className="font-medium">Domains</span> (the{" "}
            <span className="font-mono">DIAGRAM_TYPE</span> domain).
          </p>
        </div>

        {loading ? (
          <div className="text-muted-foreground flex items-center gap-2 text-sm">
            <Loader2 className="size-4 animate-spin" />
            Loading…
          </div>
        ) : isError ? (
          <p className="text-destructive text-sm">Could not load diagram placement rules.</p>
        ) : diagramTypes.length === 0 ? (
          <p className="text-muted-foreground text-sm">
            No diagram types exist yet. Create a <span className="font-mono">DIAGRAM_TYPE</span> domain value (e.g.{" "}
            <span className="font-mono">ONE_LINE</span> or <span className="font-mono">CHAMBER_SCHEMATIC</span>) on the
            Domains page first.
          </p>
        ) : containers.length === 0 ? (
          <p className="text-muted-foreground text-sm">
            No diagram containers exist yet. Mark a feature class as a diagram container (
            <span className="font-mono">is_diagram_container</span>) first.
          </p>
        ) : (
          <>
            {/* Pairing picker + allow-list editor. */}
            <Card>
              <CardHeader className="py-3">
                <CardTitle className="text-sm">Edit an allow-list</CardTitle>
                <CardDescription>Pick a container and diagram type, then choose what may be placed on it.</CardDescription>
              </CardHeader>
              <CardContent className="flex flex-col gap-4">
                <div className="grid grid-cols-2 gap-3">
                  <div className="flex flex-col gap-1">
                    <label className="text-xs font-medium">Container class</label>
                    <select className={inputClass} value={container} onChange={(e) => setContainer(e.target.value)}>
                      <option value="">Select…</option>
                      {containers.map((c) => (
                        <option key={c.code} value={c.code}>
                          {c.name} ({c.code})
                        </option>
                      ))}
                    </select>
                  </div>
                  <div className="flex flex-col gap-1">
                    <label className="text-xs font-medium">Diagram type</label>
                    <select className={inputClass} value={diagramType} onChange={(e) => setDiagramType(e.target.value)}>
                      <option value="">Select…</option>
                      {diagramTypes.map((t) => (
                        <option key={t.code} value={t.code}>
                          {t.label} ({t.code})
                        </option>
                      ))}
                    </select>
                  </div>
                </div>

                {pairingChosen && (
                  <div className="flex flex-col gap-2">
                    {pairingRules.length === 0 ? (
                      <p className="text-muted-foreground rounded-md border border-dashed p-3 text-sm">
                        <span className="font-medium">Unrestricted.</span> Any placeable feature can be placed on this
                        diagram. Add a feature below to restrict it to only the allowed features.
                      </p>
                    ) : (
                      <>
                        <p className="text-muted-foreground text-xs">
                          Only these features may be placed on a{" "}
                          <span className="font-medium">{diagramTypes.find((t) => t.code === diagramType)?.label}</span>{" "}
                          under a <span className="font-medium">{containers.find((c) => c.code === container)?.name}</span>
                          :
                        </p>
                        <div className="flex flex-col gap-1.5">
                          {pairingRules.map((r) => (
                            <div
                              key={r.id}
                              className="flex items-center gap-2 rounded-md border px-3 py-1.5 text-sm"
                            >
                              <Workflow className="text-muted-foreground size-4" />
                              <span className="flex-1">{r.feature_class_name}</span>
                              <span className="text-muted-foreground font-mono text-xs">{r.feature_class}</span>
                              {canWrite && (
                                <Button
                                  variant="ghost"
                                  size="icon"
                                  className="text-destructive size-7"
                                  disabled={busy}
                                  title="Disallow"
                                  onClick={() => handleRemove(r.id)}
                                >
                                  <Trash2 className="size-4" />
                                </Button>
                              )}
                            </div>
                          ))}
                        </div>
                      </>
                    )}

                    {canWrite && (
                      <div className="mt-1 flex items-end gap-2">
                        <div className="flex flex-1 flex-col gap-1">
                          <label className="text-xs font-medium">Allow a feature</label>
                          <select
                            className={inputClass}
                            value={addClass}
                            onChange={(e) => setAddClass(e.target.value)}
                          >
                            <option value="">Select a placeable feature…</option>
                            {addCandidates.map((c) => (
                              <option key={c.code} value={c.code}>
                                {c.name} ({c.code})
                              </option>
                            ))}
                          </select>
                        </div>
                        <Button size="sm" disabled={busy || addClass === ""} onClick={handleAdd}>
                          <Plus className="size-4" />
                          Add
                        </Button>
                      </div>
                    )}
                    {canWrite && addCandidates.length === 0 && pairingRules.length > 0 && (
                      <p className="text-muted-foreground text-xs">
                        Every placeable feature is already allowed on this pairing.
                      </p>
                    )}
                  </div>
                )}
              </CardContent>
            </Card>

            {/* At-a-glance overview of every configured (restricted) pairing. */}
            <div className="flex flex-col gap-2">
              <h2 className="text-sm font-semibold">Configured restrictions</h2>
              {configured.length === 0 ? (
                <p className="text-muted-foreground flex items-center gap-2 text-sm">
                  <Ban className="size-4" />
                  No restrictions yet — every diagram accepts any placeable feature.
                </p>
              ) : (
                configured.map((p) => (
                  <Card key={`${p.container}::${p.type}`}>
                    <CardHeader className="py-3">
                      <CardTitle className="text-sm">
                        {p.containerName} · {p.typeLabel}
                      </CardTitle>
                      <CardDescription className="font-mono text-xs">
                        {p.container} × {p.type}
                      </CardDescription>
                    </CardHeader>
                    <CardContent className="flex flex-wrap gap-1.5 pb-3">
                      {p.features.map((f) => (
                        <span
                          key={f.id}
                          className="bg-muted rounded-full px-2 py-0.5 text-xs"
                          title={f.feature_class}
                        >
                          {f.feature_class_name}
                        </span>
                      ))}
                    </CardContent>
                  </Card>
                ))
              )}
            </div>
          </>
        )}
      </div>
    </div>
  )
}
