import { AlertCircle, AlertTriangle, X } from "lucide-react"

import { useToastStore } from "@/stores/toast-store"

/**
 * Fixed top-right stack, mounted once in src/app.tsx above every
 * route -- every error or warning anywhere in the app goes through
 * useToastStore.pushError/pushWarning instead of being rendered inline
 * in whatever panel or form triggered it, so it always shows up here.
 * Was previously mounted only inside MapPage (src/routes/map.tsx), so
 * any toast triggered from any other route (/features, /admin/*, ...)
 * silently had nowhere to render -- caught by Playwright-verifying an
 * admin delete rejection. Offset below the top nav row (New feature /
 * Layers buttons, also top-right on the map route) rather than sharing
 * its exact corner, so a toast never sits directly on top of those
 * controls. `warning` uses amber, not the destructive red `error`
 * gets -- a plausibility_warning-style toast is telling the user to
 * double-check something that already succeeded, not that a request
 * failed.
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
          className={`pointer-events-auto flex items-start gap-2 rounded-md border border-transparent px-3 py-2 text-sm shadow-lg ${
            toast.variant === "warning"
              ? "bg-amber-500 text-white dark:bg-amber-600"
              : "bg-destructive text-destructive-foreground"
          }`}
        >
          {toast.variant === "warning" ? (
            <AlertTriangle className="mt-0.5 size-4 shrink-0" />
          ) : (
            <AlertCircle className="mt-0.5 size-4 shrink-0" />
          )}
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
