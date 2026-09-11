import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"

import { ApiError, apiFetch } from "@/lib/api"

/**
 * Wire types + hooks for the attachments API (api/internal/attachment/routes.go),
 * behind attachments:read / attachments:write. An attachment is one file in
 * RustFS plus one metadata row, scoped to a feature by the (class code, feature
 * id) pair -- there's no shared feature table, so every path carries both.
 *
 * Upload is the server's deliberate three-step dance: register a pending row and
 * get a short-lived presigned PUT URL, PUT the raw bytes straight to RustFS
 * (never through the Go process), then commit so the server confirms the object
 * landed and generates an image thumbnail. Only committed (`ready`) rows list.
 */
export interface Attachment {
  id: string
  feature_class: string
  feature_id: string
  filename: string
  content_type: string
  byte_size: number
  kind: "image" | "document"
  status: "pending" | "ready"
  created_at: string
  /** Inline presigned URL for a ready image's thumbnail; absent for documents
   *  (and for an image whose thumbnail failed to generate -- show a placeholder). */
  thumb_url?: string
}

interface RegisterResponse {
  attachment: Attachment
  upload_url: string
  upload_content_type: string
}

function attachmentsKey(code: string | undefined, id: string | undefined) {
  return ["attachments", code, id] as const
}

/** A feature's committed attachments (images + documents), newest first. */
export function useAttachments(code: string | undefined, id: string | undefined) {
  return useQuery<{ attachments: Attachment[] }, ApiError>({
    queryKey: attachmentsKey(code, id),
    queryFn: () => apiFetch<{ attachments: Attachment[] }>(`/attachments/${code}/${id}`),
    enabled: Boolean(code && id),
    retry: false,
  })
}

/**
 * The full upload flow for one file: register -> presigned PUT -> commit. The PUT
 * goes direct to RustFS with a plain fetch (a cross-origin presigned URL -- no
 * cookies, no CSRF, no apiFetch), then commit runs back through the API. On
 * success the feature's attachment list is invalidated so the grid refreshes.
 */
export function useUploadAttachment(code: string, id: string) {
  const queryClient = useQueryClient()
  return useMutation<Attachment, ApiError, File>({
    mutationFn: async (file) => {
      const registered = await apiFetch<RegisterResponse>(`/attachments/${code}/${id}`, {
        method: "POST",
        body: JSON.stringify({
          filename: file.name,
          content_type: file.type || "application/octet-stream",
          byte_size: file.size,
        }),
      })

      // Raw PUT straight to RustFS. Must NOT ride same-origin cookies (the
      // presigned URL is a different origin and self-authorizes), so it's a
      // bare fetch rather than apiFetch.
      const put = await fetch(registered.upload_url, {
        method: "PUT",
        body: file,
        headers: { "Content-Type": registered.upload_content_type },
      })
      if (!put.ok) {
        throw new ApiError(put.status, `Upload failed (${put.status})`)
      }

      return apiFetch<Attachment>(`/attachments/${code}/${id}/${registered.attachment.id}/commit`, {
        method: "POST",
      })
    },
    onSuccess: () => queryClient.invalidateQueries({ queryKey: attachmentsKey(code, id) }),
  })
}

/** Soft-deletes an attachment, then refreshes the feature's list. */
export function useDeleteAttachment(code: string, id: string) {
  const queryClient = useQueryClient()
  return useMutation<void, ApiError, string>({
    mutationFn: (attachmentId) =>
      apiFetch<void>(`/attachments/${code}/${id}/${attachmentId}`, { method: "DELETE" }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: attachmentsKey(code, id) }),
  })
}

/**
 * Resolves a fresh presigned view/download URL for one attachment -- inline for
 * an image (opens in a new tab / lightbox), attachment-disposition under the
 * original filename for a document. Not a hook: called imperatively from a click,
 * since the URL is short-lived and shouldn't be prefetched into cache.
 */
export async function fetchAttachmentUrl(code: string, id: string, attachmentId: string): Promise<string> {
  const { url } = await apiFetch<{ url: string }>(`/attachments/${code}/${id}/${attachmentId}/url`)
  return url
}
