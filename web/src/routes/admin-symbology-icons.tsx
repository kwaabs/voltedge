import { useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { Loader2, Pencil, Plus, Shapes, Trash2 } from "lucide-react"

import { ConfirmDeleteDialog } from "@/components/admin/confirm-delete-dialog"
import { FormDialog } from "@/components/admin/form-dialog"
import { inputClass } from "@/components/map/field-utils"
import { Button } from "@/components/ui/button"
import { Card, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { usePermission } from "@/hooks/use-auth"
import { type AdminSymbologyIcon, useAdminSymbologyIcons } from "@/hooks/use-admin"
import { ApiError, apiFetch } from "@/lib/api"
import { useToastStore } from "@/stores/toast-store"

const labelClass = "text-xs font-medium"

/** A checkbox-transparent preview swatch color -- the tint applied to an
 * icon's {{COLOR}} placeholder in the live preview so an author sees how a
 * tintable symbol will actually render. Full-color artwork (no
 * placeholder) ignores it, exactly as it does on the map. */
const PREVIEW_DEFAULT_COLOR = "#2563eb"

/** Render arbitrary (possibly unsaved) SVG markup to a tinted data URI --
 * the same {{COLOR}} substitution iconDataUri does, but for raw text
 * straight out of the editor rather than a resolved icon_ref, so the
 * preview updates as you type before anything is saved. */
function previewUri(svg: string, color: string): string {
  return `data:image/svg+xml;utf8,${encodeURIComponent(svg.replaceAll("{{COLOR}}", color))}`
}

/** The shared preview + svg editor used by both the New and Edit forms --
 * a live thumbnail next to the markup, plus a tint control so an author
 * can check a {{COLOR}} symbol against a real color. */
function IconEditor({
  svg,
  onSvgChange,
  previewColor,
  onPreviewColorChange,
}: {
  svg: string
  onSvgChange: (v: string) => void
  previewColor: string
  onPreviewColorChange: (v: string) => void
}) {
  const canPreview = svg.trim().length > 0 && svg.includes("<svg")
  return (
    <div className="flex flex-col gap-3">
      <div className="flex items-start gap-3">
        <div className="flex shrink-0 flex-col items-center gap-1.5">
          <div className="bg-muted/40 flex size-20 items-center justify-center rounded-md border">
            {canPreview ? (
              <img src={previewUri(svg, previewColor)} alt="icon preview" className="size-14" />
            ) : (
              <Shapes className="text-muted-foreground size-6" />
            )}
          </div>
          <input
            type="color"
            value={previewColor}
            onChange={(e) => onPreviewColorChange(e.target.value)}
            className="h-7 w-12 rounded-md border"
            title="Preview tint (only affects {{COLOR}} symbols)"
          />
        </div>
        <div className="flex flex-1 flex-col gap-1">
          <label className={labelClass}>
            SVG markup <span className="text-destructive">*</span>
          </label>
          <textarea
            className={`${inputClass} h-28 resize-y py-1.5 font-mono text-xs`}
            value={svg}
            onChange={(e) => onSvgChange(e.target.value)}
            placeholder={`<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24">...`}
            spellCheck={false}
          />
          <p className="text-muted-foreground text-xs">
            Use <span className="font-mono">{"{{COLOR}}"}</span> where a rule&apos;s color should be substituted (leave
            it out for full-color artwork). Must carry explicit width/height, not just a viewBox.
          </p>
        </div>
      </div>
    </div>
  )
}

/** Edit an existing icon's markup in place -- the icon_ref (the identity
 * a rule references) never changes here, only its artwork. Invalidates
 * both the admin list and the public map registry so the change is live
 * everywhere at once. */
function EditIconRow({ icon, onDone }: { icon: AdminSymbologyIcon; onDone: () => void }) {
  const [svg, setSvg] = useState(icon.svg)
  const [previewColor, setPreviewColor] = useState(PREVIEW_DEFAULT_COLOR)
  const [saving, setSaving] = useState(false)
  const queryClient = useQueryClient()

  function close() {
    setSvg(icon.svg)
    setPreviewColor(PREVIEW_DEFAULT_COLOR)
    onDone()
  }

  async function handleSave() {
    setSaving(true)
    try {
      await apiFetch(`/admin/symbology-icons/${icon.icon_ref}`, {
        method: "PATCH",
        body: JSON.stringify({ svg }),
      })
      queryClient.invalidateQueries({ queryKey: ["admin", "symbology-icons"] })
      queryClient.invalidateQueries({ queryKey: ["symbology-icons"] })
      close()
    } catch (err) {
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not update this icon.")
    } finally {
      setSaving(false)
    }
  }

  return (
    <FormDialog
      open
      onOpenChange={(next) => (next ? undefined : close())}
      title={`Edit icon ${icon.icon_ref}`}
      onSubmit={handleSave}
      submitLabel="Save"
      canSubmit={svg.trim().length > 0}
      saving={saving}
      className="max-w-2xl"
    >
      <IconEditor svg={svg} onSvgChange={setSvg} previewColor={previewColor} onPreviewColorChange={setPreviewColor} />
    </FormDialog>
  )
}

/**
 * The icon library -- every icon_ref -> SVG row of metadata.symbology_icons
 * (migrations/0023), the artwork a symbology rule's style_def.icon_ref
 * points at. Lets an author add or edit icons visually, with a live tinted
 * preview, instead of writing raw INSERTs (the DB stays directly editable
 * for anyone who prefers SQL). See docs/phase-3-symbology-icon-table.md.
 * The five seeded shapes always resolve as a fallback even before any DB
 * icon is authored, so a fresh library is never broken.
 */
export function AdminSymbologyIconsPage() {
  const { data, isPending, isError } = useAdminSymbologyIcons()
  const queryClient = useQueryClient()
  // Create/edit/delete here are metadata:write on the server; this page opens
  // on metadata:read, so gate the write controls to match.
  const canWrite = usePermission()("metadata:write")

  const [open, setOpen] = useState(false)
  const [iconRef, setIconRef] = useState("")
  const [svg, setSvg] = useState("")
  const [previewColor, setPreviewColor] = useState(PREVIEW_DEFAULT_COLOR)
  const [saving, setSaving] = useState(false)
  const [editingRef, setEditingRef] = useState<string | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<AdminSymbologyIcon | null>(null)

  function close() {
    setOpen(false)
    setIconRef("")
    setSvg("")
    setPreviewColor(PREVIEW_DEFAULT_COLOR)
  }

  const editingIcon = data?.symbology_icons.find((icon) => icon.icon_ref === editingRef) ?? null

  // Lower-kebab, matching iconRefPattern in api/internal/admin/validate.go
  // -- reject early so the field can't drift a name a rule could never
  // spell.
  const canSave = /^[a-z][a-z0-9-]*$/.test(iconRef) && svg.trim().length > 0

  async function handleSave() {
    setSaving(true)
    try {
      await apiFetch("/admin/symbology-icons", {
        method: "POST",
        body: JSON.stringify({ icon_ref: iconRef.trim(), svg }),
      })
      queryClient.invalidateQueries({ queryKey: ["admin", "symbology-icons"] })
      queryClient.invalidateQueries({ queryKey: ["symbology-icons"] })
      close()
    } catch (err) {
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not create this icon.")
    } finally {
      setSaving(false)
    }
  }

  return (
    <div className="h-full overflow-y-auto p-4">
      <div className="flex w-full flex-col gap-4">
        <div className="flex items-center justify-between">
          <div>
            <h1 className="text-lg font-semibold">Symbology icons</h1>
            <p className="text-muted-foreground text-sm">
              The artwork behind every symbology rule&apos;s icon. A rule references an icon by name; this is where that
              name resolves to real SVG markup, added or edited without a deploy.
            </p>
          </div>
          {canWrite && (
            <Button size="sm" onClick={() => setOpen(true)}>
              <Plus className="size-4" />
              New icon
            </Button>
          )}
        </div>

        <FormDialog
          open={open}
          onOpenChange={(next) => (next ? setOpen(true) : close())}
          title="New icon"
          description="A lower-kebab name and its SVG markup."
          onSubmit={handleSave}
          submitLabel="Create"
          canSubmit={canSave}
          saving={saving}
          className="max-w-2xl"
        >
          <div className="flex flex-col gap-1">
            <label className={labelClass}>
              Name <span className="text-destructive">*</span>
            </label>
            <input
              className={`${inputClass} font-mono`}
              value={iconRef}
              onChange={(e) => setIconRef(e.target.value.toLowerCase())}
              placeholder="e.g. iec-disconnector"
            />
          </div>
          <IconEditor svg={svg} onSvgChange={setSvg} previewColor={previewColor} onPreviewColorChange={setPreviewColor} />
        </FormDialog>

        {isPending ? (
          <div className="text-muted-foreground flex items-center gap-2 text-sm">
            <Loader2 className="size-4 animate-spin" />
            Loading icons...
          </div>
        ) : isError || !data ? (
          <p className="text-destructive text-sm">Could not load symbology icons.</p>
        ) : data.symbology_icons.length === 0 ? (
          <p className="text-muted-foreground text-sm">
            No icons in the database yet — the built-in fallback shapes still render until you add some.
          </p>
        ) : (
          <div className="flex flex-col gap-2">
            {data.symbology_icons.map((icon) => (
              <Card key={icon.icon_ref}>
                <CardHeader className="flex flex-row items-center gap-3 py-3">
                  <div className="bg-muted/40 flex size-10 shrink-0 items-center justify-center rounded-md border">
                    <img src={previewUri(icon.svg, PREVIEW_DEFAULT_COLOR)} alt={icon.icon_ref} className="size-7" />
                  </div>
                  <div className="min-w-0 flex-1">
                    <CardTitle className="font-mono text-sm">{icon.icon_ref}</CardTitle>
                    <CardDescription className="text-xs">
                      {icon.svg.includes("{{COLOR}}") ? "Tintable" : "Full-color"}
                    </CardDescription>
                  </div>
                  {canWrite && (
                    <>
                      <Button
                        variant="ghost"
                        size="icon"
                        className="size-7"
                        title="Edit"
                        onClick={() => setEditingRef(icon.icon_ref)}
                      >
                        <Pencil className="size-4" />
                      </Button>
                      <Button
                        variant="ghost"
                        size="icon"
                        className="text-destructive size-7"
                        onClick={() => setDeleteTarget(icon)}
                      >
                        <Trash2 className="size-4" />
                      </Button>
                    </>
                  )}
                </CardHeader>
              </Card>
            ))}
          </div>
        )}

        {editingIcon && <EditIconRow icon={editingIcon} onDone={() => setEditingRef(null)} />}
      </div>

      <ConfirmDeleteDialog
        open={deleteTarget !== null}
        title={`Delete ${deleteTarget?.icon_ref ?? "this icon"}?`}
        description="Only possible while no symbology rule references this icon -- if any does, deletion is rejected with a clear error instead. This can't be undone."
        onOpenChange={(open) => !open && setDeleteTarget(null)}
        onConfirm={async () => {
          if (!deleteTarget) return
          await apiFetch(`/admin/symbology-icons/${deleteTarget.icon_ref}`, { method: "DELETE" })
          queryClient.invalidateQueries({ queryKey: ["admin", "symbology-icons"] })
          queryClient.invalidateQueries({ queryKey: ["symbology-icons"] })
        }}
      />
    </div>
  )
}
