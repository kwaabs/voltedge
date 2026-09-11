import { useState } from "react"
import { Loader2 } from "lucide-react"

import { inputClass } from "@/components/map/field-utils"
import { Button } from "@/components/ui/button"
import { useAdminDomains } from "@/hooks/use-admin"
import { useDiagramTemplates } from "@/hooks/use-diagram-templates"
import { useCreateDiagramInstance } from "@/hooks/use-diagrams"
import { ApiError, apiFetch } from "@/lib/api"
import { useDiagramStore } from "@/stores/diagram-store"
import { useToastStore } from "@/stores/toast-store"

const labelClass = "text-xs font-medium"

// The domain code api/internal/diagram/sql.go resolves diagram_type
// against -- see that package's own doc comment. Not a new lookup table,
// an ordinary coded domain any admin can author today via
// admin-domains.tsx; this form just reads its current values rather than
// assuming ONE_LINE (the seed example) is the only one that'll ever exist.
const DIAGRAM_TYPE_DOMAIN = "DIAGRAM_TYPE"

interface CreateDiagramFormProps {
  featureClassCode: string
  featureId: string
  onDone: () => void
}

/**
 * The one real gap left in "author every diagram-related thing through
 * the UI": is_diagram_container and DIAGRAM_TYPE's own values were
 * already fully editable via the existing feature-class/domains admin
 * pages -- creating the *first* diagram_instances row for a container
 * feature that doesn't have one yet was the only piece with no UI
 * trigger at all, even though POST /diagrams has been a real, generic,
 * curl-verified endpoint since Phase 5. Shown in place of the normal
 * tab body (feature-panel.tsx) rather than a separate dialog, matching
 * how this panel already swaps its body for feature-edit-form.tsx
 * during an edit session.
 */
export function CreateDiagramForm({ featureClassCode, featureId, onDone }: CreateDiagramFormProps) {
  const { data: domainsData, isPending: domainsPending } = useAdminDomains()
  const diagramTypeValues = domainsData?.domains.find((d) => d.code === DIAGRAM_TYPE_DOMAIN)?.values ?? []
  const [diagramType, setDiagramType] = useState("")
  const [name, setName] = useState("")
  const [templateId, setTemplateId] = useState("")
  const templates = useDiagramTemplates()
  const { mutateAsync, isPending: saving } = useCreateDiagramInstance()

  const canCreate = diagramType.length > 0 && !saving

  async function handleCreate() {
    try {
      const created = await mutateAsync({
        container: { feature_class: featureClassCode, feature_id: featureId },
        diagram_type: diagramType,
        name: name.trim() || undefined,
      })
      if (templateId) {
        // Applying a template is offered ONLY here, at creation: a template is a
        // starting skeleton for a fresh diagram, never something layered onto an
        // existing one. The diagram itself is already created, so a template
        // failure just toasts and still opens the (blank) diagram.
        try {
          await apiFetch(`/diagrams/${created.id}/apply-template`, {
            method: "POST",
            body: JSON.stringify({ template_id: templateId }),
          })
        } catch (err) {
          useToastStore
            .getState()
            .pushError(err instanceof ApiError ? err.message : "The diagram was created, but its template could not be applied.")
        }
      }
      useDiagramStore.getState().open(created.id)
      onDone()
    } catch (err) {
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not create this diagram.")
    }
  }

  if (!domainsPending && diagramTypeValues.length === 0) {
    return (
      <div className="flex flex-col gap-2">
        <p className="text-muted-foreground text-sm">
          No diagram types exist yet. Create a <span className="font-mono">DIAGRAM_TYPE</span> domain (e.g. a
          value like <span className="font-mono">ONE_LINE</span>) on the Domains admin page first.
        </p>
        <Button variant="outline" size="sm" className="self-start" onClick={onDone}>
          Back
        </Button>
      </div>
    )
  }

  return (
    <div className="flex flex-col gap-3">
      <p className="text-muted-foreground text-sm">This feature can host a diagram, but doesn't have one yet.</p>
      <div className="flex flex-col gap-1">
        <label className={labelClass}>
          Diagram type <span className="text-destructive">*</span>
        </label>
        <select className={inputClass} value={diagramType} onChange={(e) => setDiagramType(e.target.value)}>
          <option value="">—</option>
          {diagramTypeValues.map((v) => (
            <option key={v.code} value={v.code}>
              {v.label}
            </option>
          ))}
        </select>
      </div>
      <div className="flex flex-col gap-1">
        <label className={labelClass}>Name</label>
        <input
          className={inputClass}
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder="optional"
        />
      </div>
      {templates.length > 0 && (
        <div className="flex flex-col gap-1">
          <label className={labelClass}>Start from template</label>
          <select className={inputClass} value={templateId} onChange={(e) => setTemplateId(e.target.value)}>
            <option value="">Blank diagram</option>
            {templates.map((t) => (
              <option key={t.id} value={t.id}>
                {t.name}
              </option>
            ))}
          </select>
          <p className="text-muted-foreground text-xs">Drops the template's furniture and empty slots to bind — only offered here, at creation.</p>
        </div>
      )}
      <div className="flex justify-end gap-2">
        <Button variant="outline" size="sm" onClick={onDone} disabled={saving}>
          Cancel
        </Button>
        <Button size="sm" onClick={handleCreate} disabled={!canCreate}>
          {saving && <Loader2 className="size-4 animate-spin" />}
          Create
        </Button>
      </div>
    </div>
  )
}
