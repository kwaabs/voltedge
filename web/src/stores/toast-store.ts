import { useEffect } from "react"
import { create } from "zustand"

export interface Toast {
  id: number
  message: string
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
 */
interface ToastState {
  toasts: Toast[]
  pushError: (message: string) => void
  dismiss: (id: number) => void
}

let nextId = 1

export const useToastStore = create<ToastState>((set) => ({
  toasts: [],
  pushError: (message) => {
    const id = nextId++
    set((state) => ({ toasts: [...state.toasts, { id, message }] }))
    setTimeout(() => {
      set((state) => ({ toasts: state.toasts.filter((t) => t.id !== id) }))
    }, AUTO_DISMISS_MS)
  },
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
