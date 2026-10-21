import { create } from "zustand"

/**
 * Global edit/view mode for the spatial surfaces (map + diagram canvas).
 *
 * VoltEdge used to have no mode at all: if you held `features:write`, every
 * editing affordance was always on, interleaved with the view/analysis tools, so
 * you could never be in a "just looking, can't break anything" state. This flag
 * is that state. It is deliberately **UX only** -- the server still enforces every
 * permission, so this never weakens security; it prevents *accidental* edits and
 * declutters the view.
 *
 * Deliberately NOT persisted: every session starts in **View** (edit is opt-in),
 * the safety default the product chose. The `features:write` permission decides
 * whether the Edit toggle is even offered; this flag decides whether, having the
 * toggle, the user has turned editing on. Compose the two through `useEditing()`
 * (hooks/use-editing.ts) rather than reading this flag raw -- a viewer without the
 * permission must never be treated as "editing" even if the flag were somehow set.
 */
interface EditModeState {
  editMode: boolean
  /**
   * A uuid minted each time edit mode is turned ON, cleared when turned off. The
   * api layer sends it as the X-Edit-Session header on every mutating request
   * while editing, so the server can stamp a batch of changes with one session id
   * (feature_history.edit_session_id) and the edit-sessions report can group them
   * back together. null whenever not editing.
   */
  sessionId: string | null
  setEditMode: (on: boolean) => void
  toggle: () => void
}

/** Fresh session id per edit-mode span. crypto.randomUUID is available in every
 * browser this app targets; a timestamp-based fallback keeps it from throwing in
 * an exotic context rather than ever colliding in practice. */
function newSessionId(): string {
  try {
    return crypto.randomUUID()
  } catch {
    return `00000000-0000-4000-8000-${Date.now().toString(16).padStart(12, "0").slice(-12)}`
  }
}

export const useEditModeStore = create<EditModeState>((set) => ({
  editMode: false,
  sessionId: null,
  setEditMode: (on) => set({ editMode: on, sessionId: on ? newSessionId() : null }),
  toggle: () => set((s) => ({ editMode: !s.editMode, sessionId: s.editMode ? null : newSessionId() })),
}))
