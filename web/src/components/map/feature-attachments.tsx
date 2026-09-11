import { useRef, useState } from "react"
import { Check, Download, FileText, Image as ImageIcon, Loader2, Paperclip, Pencil, Trash2, Upload } from "lucide-react"

import { ConfirmDeleteDialog } from "@/components/admin/confirm-delete-dialog"
import { Button } from "@/components/ui/button"
import {
  type Attachment,
  fetchAttachmentUrl,
  useAttachments,
  useDeleteAttachment,
  useUploadAttachment,
} from "@/hooks/use-attachments"
import { usePermission } from "@/hooks/use-auth"
import { ApiError } from "@/lib/api"
import { cn } from "@/lib/utils"
import { useErrorToast, useToastStore } from "@/stores/toast-store"

interface FeatureAttachmentsProps {
  featureClassCode: string
  featureId: string
}

/** Human-readable byte size -- KB/MB with one decimal, matching how the pack and
 *  export UIs render their own sizes. */
function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  const kb = bytes / 1024
  if (kb < 1024) return `${kb.toFixed(1)} KB`
  return `${(kb / 1024).toFixed(1)} MB`
}

/**
 * The Files tab (feature-panel.tsx): a feature's attached images and documents.
 * Images render as a thumbnail grid (from the server-generated thumb_url);
 * documents as a labelled list. Browsing is always read-only -- opening and
 * downloading files needs no special mode.
 *
 * Adding and removing files is a deliberate, opt-in mode, not something you can
 * do just by having a feature selected: a "Manage files" toggle (gated on
 * attachments:write, so a viewer/analyst never even sees it) flips the tab into
 * a manage state that reveals Upload and per-file Delete. This mirrors "you edit
 * a feature's data on purpose, not by browsing it" -- the Files tab can't join
 * the feature's own attribute-edit session (switching tabs mid-edit would drop
 * the form's unsaved values, so it stays locked during that), so this is its own
 * local edit state instead. Deleting always routes through a confirmation dialog
 * -- removing a file is destructive and there's no undo.
 *
 * The web accepts images AND documents (the mobile field tool is images-only);
 * `kind` is inferred server-side from the content type, so the file input here
 * doesn't restrict the accept list beyond what's sensible.
 */
export function FeatureAttachments({ featureClassCode, featureId }: FeatureAttachmentsProps) {
  const { data, isPending, isError, error } = useAttachments(featureClassCode, featureId)
  useErrorToast(isError, error, "Could not load attachments")
  const canWrite = usePermission()("attachments:write")

  const upload = useUploadAttachment(featureClassCode, featureId)
  const del = useDeleteAttachment(featureClassCode, featureId)
  const fileInputRef = useRef<HTMLInputElement>(null)
  // Which attachment's presigned URL is currently being resolved for an open --
  // just for the per-row spinner, since fetchAttachmentUrl is a bare async call.
  const [opening, setOpening] = useState<string | null>(null)
  // Manage mode: false = read-only browse (the default), true = Upload + Delete
  // revealed. Only ever entered with attachments:write. Resets whenever this
  // component unmounts, which the panel forces on any tab or feature change
  // (feature-panel.tsx lands back on Attributes for each new selection), so you
  // always re-open Files in the safe read-only state.
  const [managing, setManaging] = useState(false)
  // The attachment awaiting delete confirmation (null = dialog closed). Delete
  // never fires straight from the trash button -- it opens this dialog first.
  const [pendingDelete, setPendingDelete] = useState<Attachment | null>(null)
  const showControls = canWrite && managing

  async function handleFiles(files: FileList | null) {
    if (!files || files.length === 0) return
    // Sequentially -- each upload is its own register/PUT/commit round trip, and
    // serialising keeps the presigned URLs (and the toast on failure) simple.
    for (const file of Array.from(files)) {
      try {
        await upload.mutateAsync(file)
      } catch (err) {
        useToastStore.getState().pushError(
          err instanceof ApiError ? `Could not upload ${file.name}: ${err.message}` : `Could not upload ${file.name}.`,
        )
      }
    }
    // Reset so re-selecting the same file fires onChange again.
    if (fileInputRef.current) fileInputRef.current.value = ""
  }

  async function handleOpen(a: Attachment) {
    setOpening(a.id)
    try {
      const url = await fetchAttachmentUrl(featureClassCode, featureId, a.id)
      window.open(url, "_blank", "noopener,noreferrer")
    } catch (err) {
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not open this file.")
    } finally {
      setOpening(null)
    }
  }

  if (isPending) {
    return (
      <div className="text-muted-foreground flex items-center gap-2 text-sm">
        <Loader2 className="size-4 animate-spin" />
        Loading files...
      </div>
    )
  }
  if (isError) return null

  const attachments = data?.attachments ?? []
  const images = attachments.filter((a) => a.kind === "image")
  const documents = attachments.filter((a) => a.kind === "document")

  return (
    <div className="flex flex-col gap-3">
      {canWrite && (
        <div className="flex items-center gap-2">
          <input
            ref={fileInputRef}
            type="file"
            multiple
            className="hidden"
            onChange={(e) => handleFiles(e.target.files)}
          />
          {managing ? (
            <>
              <Button
                variant="outline"
                size="sm"
                disabled={upload.isPending}
                onClick={() => fileInputRef.current?.click()}
              >
                {upload.isPending ? <Loader2 className="size-4 animate-spin" /> : <Upload className="size-4" />}
                {upload.isPending ? "Uploading..." : "Upload files"}
              </Button>
              <Button
                variant="ghost"
                size="sm"
                disabled={upload.isPending}
                onClick={() => setManaging(false)}
              >
                <Check className="size-4" />
                Done
              </Button>
            </>
          ) : (
            <Button variant="outline" size="sm" onClick={() => setManaging(true)}>
              <Pencil className="size-4" />
              Manage files
            </Button>
          )}
        </div>
      )}

      {attachments.length === 0 && (
        <p className="text-muted-foreground flex items-center gap-2 text-sm">
          <Paperclip className="size-4" />
          No files attached.
        </p>
      )}

      {images.length > 0 && (
        <div className="flex flex-col gap-1.5">
          <p className="text-muted-foreground flex items-center gap-1.5 text-xs font-medium">
            <ImageIcon className="size-3.5" />
            Images
          </p>
          <div className="grid grid-cols-3 gap-1.5">
            {images.map((a) => (
              <div key={a.id} className="group relative aspect-square overflow-hidden rounded-md border">
                <button
                  type="button"
                  onClick={() => handleOpen(a)}
                  title={a.filename}
                  className="bg-muted/40 hover:bg-muted flex size-full items-center justify-center"
                >
                  {a.thumb_url ? (
                    <img src={a.thumb_url} alt={a.filename} className="size-full object-cover" />
                  ) : (
                    <ImageIcon className="text-muted-foreground size-6" />
                  )}
                  {opening === a.id && (
                    <span className="absolute inset-0 flex items-center justify-center bg-black/30">
                      <Loader2 className="size-5 animate-spin text-white" />
                    </span>
                  )}
                </button>
                {showControls && (
                  <button
                    type="button"
                    onClick={() => setPendingDelete(a)}
                    disabled={del.isPending}
                    title="Delete"
                    className="absolute top-1 right-1 rounded bg-black/50 p-1 text-white opacity-0 transition-opacity group-hover:opacity-100 hover:bg-black/70"
                  >
                    <Trash2 className="size-3.5" />
                    <span className="sr-only">Delete image</span>
                  </button>
                )}
              </div>
            ))}
          </div>
        </div>
      )}

      {documents.length > 0 && (
        <div className="flex flex-col gap-1.5">
          <p className="text-muted-foreground flex items-center gap-1.5 text-xs font-medium">
            <FileText className="size-3.5" />
            Documents
          </p>
          <div className="flex flex-col gap-1.5">
            {documents.map((a) => (
              <div
                key={a.id}
                className={cn(
                  "group flex items-center gap-2 rounded-md border p-2 text-sm transition-colors",
                  "hover:bg-accent hover:text-accent-foreground",
                )}
              >
                <FileText className="text-muted-foreground size-4 shrink-0" />
                <button type="button" onClick={() => handleOpen(a)} className="min-w-0 flex-1 text-left">
                  <p className="truncate font-medium">{a.filename}</p>
                  <p className="text-muted-foreground text-xs">{formatBytes(a.byte_size)}</p>
                </button>
                {opening === a.id ? (
                  <Loader2 className="text-muted-foreground size-4 shrink-0 animate-spin" />
                ) : (
                  <Download className="text-muted-foreground size-4 shrink-0" />
                )}
                {showControls && (
                  <button
                    type="button"
                    onClick={() => setPendingDelete(a)}
                    disabled={del.isPending}
                    title="Delete"
                    className="text-muted-foreground hover:bg-destructive/10 hover:text-destructive shrink-0 rounded p-1"
                  >
                    <Trash2 className="size-3.5" />
                    <span className="sr-only">Delete document</span>
                  </button>
                )}
              </div>
            ))}
          </div>
        </div>
      )}

      <ConfirmDeleteDialog
        open={pendingDelete !== null}
        title="Delete this file?"
        description={
          pendingDelete
            ? `"${pendingDelete.filename}" will be removed from this feature. This can't be undone.`
            : ""
        }
        onOpenChange={(open) => !open && setPendingDelete(null)}
        onConfirm={async () => {
          if (!pendingDelete) return
          await del.mutateAsync(pendingDelete.id)
        }}
      />
    </div>
  )
}
