import { create } from "zustand"

import { ApiError, apiFetch } from "@/lib/api"

/**
 * The in-app assistant's chat state (Phase 19 Slice 3, docs/phase-19-assistant.md)
 * -- the Tier-1, LLM-backed surface that sits beside the Slice 1 deterministic
 * suggestions. The user types a freeform request; the backend orchestrator
 * (POST /assistant/chat) routes it over a small model, runs any read tools
 * inline, and answers -- returning a spoken reply plus, for anything that would
 * write, a set of *proposed actions* the user must confirm. Nothing writes
 * until a confirm click POSTs the proposal to /assistant/execute; the model
 * never commits on its own, exactly like the suggestion chips.
 *
 * The frontend owns the visible transcript and resends it on each turn (the
 * backend keeps no chat state between calls). Not persisted -- a conversation
 * has no meaning across a reload, same reasoning as the other map stores. The
 * confirm/execute step lives in the panel component (assistant-chat.tsx), which
 * has the query client to refresh the map after a write; this store just holds
 * the transcript and prunes a proposal once it's been acted on.
 */
export interface ChatProposedAction {
  tool: string
  title: string
  detail?: string
  // The exact tool arguments POSTed verbatim to /assistant/execute on confirm.
  // Opaque here: the frontend forwards what the orchestrator assembled, never
  // reshaping it.
  args: unknown
}

export interface ChatToolRun {
  tool: string
  ok: boolean
}

export interface ChatMessage {
  role: "user" | "assistant"
  content: string
  // Present only on an assistant turn: writes awaiting the user's confirmation,
  // and the reads the orchestrator ran to answer (shown for transparency).
  actions?: ChatProposedAction[]
  toolRuns?: ChatToolRun[]
  // Marks an assistant bubble that reports a failure (model down, etc.) so the
  // panel can style it as an error rather than a normal reply.
  error?: boolean
}

interface ChatResponse {
  reply: string
  actions?: ChatProposedAction[]
  tool_runs?: ChatToolRun[]
}

interface AssistantChatState {
  open: boolean
  messages: ChatMessage[]
  pending: boolean
  toggle: () => void
  setOpen: (open: boolean) => void
  send: (text: string) => Promise<void>
  /** Remove one proposal after it's confirmed or dismissed. */
  resolveAction: (messageIndex: number, actionIndex: number) => void
  clear: () => void
}

export const useAssistantChatStore = create<AssistantChatState>((set, get) => ({
  open: false,
  messages: [],
  pending: false,
  toggle: () => set((s) => ({ open: !s.open })),
  setOpen: (open) => set({ open }),
  send: async (text) => {
    const trimmed = text.trim()
    if (!trimmed || get().pending) return

    // Append the user turn first so the transcript we send includes it and the
    // UI shows it immediately while the model works.
    set((s) => ({ messages: [...s.messages, { role: "user", content: trimmed }], pending: true }))

    try {
      const payload = get().messages.map((m) => ({ role: m.role, content: m.content }))
      const res = await apiFetch<ChatResponse>("/assistant/chat", {
        method: "POST",
        body: JSON.stringify({ messages: payload }),
      })
      set((s) => ({
        messages: [
          ...s.messages,
          { role: "assistant", content: res.reply, actions: res.actions, toolRuns: res.tool_runs },
        ],
        pending: false,
      }))
    } catch (err) {
      const message =
        err instanceof ApiError ? err.message : "The assistant is unavailable right now. Try again in a moment."
      set((s) => ({
        messages: [...s.messages, { role: "assistant", content: message, error: true }],
        pending: false,
      }))
    }
  },
  resolveAction: (messageIndex, actionIndex) =>
    set((state) => ({
      messages: state.messages.map((m, i) =>
        i === messageIndex ? { ...m, actions: m.actions?.filter((_, j) => j !== actionIndex) } : m,
      ),
    })),
  clear: () => set({ messages: [] }),
}))
