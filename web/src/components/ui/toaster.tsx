import { AlertCircle, X } from "lucide-react"

import { useToastStore } from "@/stores/toast-store"

/**
 * Fixed top-right stack, mounted once in src/routes/map.tsx alongside
 * this app's other floating overlays -- every error anywhere in the app
 * goes through useToastStore.pushError instead of being rendered inline
 * in whatever panel or form triggered it, so it always shows up here.
 * Offset below the top nav row (New feature / Layers buttons, also
 * top-right) rather than sharing its exact corner, so a toast never
 * sits directly on top of those controls. Error-only for now (no
 * success/info variants) since that's the only kind of toast this app
 * currently has a use for; the store's shape (`message` only, no
 * `variant`) would need to grow before that changes.
 */
export function Toaster() {
  const toasts = useToastStore((s) => s.toasts)
  const dismiss = useToastStore((s) => s.dismiss)

  if (toasts.length === 0) return null

  return (
    <div className="pointer-events-none fixed top-14 right-3 z-[100] flex w-80 flex-col gap-2">
      {toasts.map((toast) => (
        <div
          key={toast.id}
          className="bg-destructive text-destructive-foreground pointer-events-auto flex items-start gap-2 rounded-md border border-transparent px-3 py-2 text-sm shadow-lg"
        >
          <AlertCircle className="mt-0.5 size-4 shrink-0" />
          <p className="flex-1 break-words">{toast.message}</p>
          <button
            onClick={() => dismiss(toast.id)}
            className="shrink-0 opacity-80 hover:opacity-100"
            aria-label="Dismiss"
          >
            <X className="size-3.5" />
          </button>
        </div>
      ))}
    </div>
  )
}
