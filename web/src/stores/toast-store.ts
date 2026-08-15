import { useEffect } from "react"
import { create } from "zustand"

export interface Toast {
  id: number
  message: string
  variant: "error" | "warning"
}

const AUTO_DISMISS_MS = 6000

/**
 * Not persisted -- a toast has no meaning across a reload. Every error
 * surfaced anywhere in the app (a failed query, a rejected write) goes
 * through `pushError` instead of being rendered inline in whatever
 * panel or form triggered it, so the user always sees it in the same
 * place (top-right, via src/components/ui/toaster.tsx) rather than
 * having to notice a small red line of text wherever it happened to
 * appear. `id` is a monotonic counter, not the error's own identity --
 * two consecutive failures with the same message are still two
 * separate toasts, each dismissed independently.
 *
 * `pushWarning` is for a request that *succeeded* but came back with
 * something worth flagging -- e.g. a relationship's own
 * plausibility_warning (api/internal/relationship/plausibility.go) --
 * distinct from `pushError`'s red destructive styling since nothing
 * actually failed.
 */
interface ToastState {
  toasts: Toast[]
  pushError: (message: string) => void
  pushWarning: (message: string) => void
  dismiss: (id: number) => void
}

let nextId = 1

function push(set: (fn: (state: ToastState) => Partial<ToastState>) => void, message: string, variant: Toast["variant"]) {
  const id = nextId++
  set((state) => ({ toasts: [...state.toasts, { id, message, variant }] }))
  setTimeout(() => {
    set((state) => ({ toasts: state.toasts.filter((t) => t.id !== id) }))
  }, AUTO_DISMISS_MS)
}

export const useToastStore = create<ToastState>((set) => ({
  toasts: [],
  pushError: (message) => push(set, message, "error"),
  pushWarning: (message) => push(set, message, "warning"),
  dismiss: (id) => set((state) => ({ toasts: state.toasts.filter((t) => t.id !== id) })),
}))

/**
 * Surfaces a failed TanStack Query as a top-right toast instead of
 * inline text wherever the query is used -- shared by every panel/form
 * in this app that reads a query's own `isError`/`error` rather than
 * handling a request imperatively (an imperative POST/DELETE calls
 * `pushError` directly from its own catch block instead). Fires once
 * per distinct error, not on every render, since `error`'s identity
 * only changes when the query actually re-fails.
 */
export function useErrorToast(isError: boolean, error: unknown, prefix: string) {
  useEffect(() => {
    if (!isError) return
    useToastStore.getState().pushError(`${prefix}${error instanceof Error ? `: ${error.message}` : "."}`)
  }, [isError, error, prefix])
}
