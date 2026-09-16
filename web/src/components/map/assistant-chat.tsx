import { useEffect, useRef, useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { Check, Loader2, Send, Sparkles, X } from "lucide-react"

import { Button } from "@/components/ui/button"
import { ApiError, apiFetch } from "@/lib/api"
import { cn } from "@/lib/utils"
import {
  type ChatMessage,
  type ChatProposedAction,
  useAssistantChatStore,
} from "@/stores/assistant-chat-store"
import { useToastStore } from "@/stores/toast-store"

/**
 * The assistant chat panel (Phase 19 Slice 3, docs/phase-19-assistant.md): a
 * floating conversation window over the map, opened from the toolbar's Assistant
 * button (which only appears when the server has a model configured -- see
 * use-assistant.ts). It renders the transcript from assistant-chat-store.ts and
 * sends each new turn through POST /assistant/chat.
 *
 * A model reply may carry proposed write actions. Those render as confirmation
 * cards inline in the assistant's bubble; nothing writes until the user clicks
 * Confirm, which POSTs the proposal verbatim to /assistant/execute -- the shared
 * agenttools choke point that re-checks the user's commit permission. This is
 * the same "the human's click is the commit" contract as the Slice 1 suggestion
 * chips, just reached through freeform chat instead of a geometry edit.
 */
export function AssistantChat() {
  const open = useAssistantChatStore((s) => s.open)
  const messages = useAssistantChatStore((s) => s.messages)
  const pending = useAssistantChatStore((s) => s.pending)
  const send = useAssistantChatStore((s) => s.send)
  const [draft, setDraft] = useState("")
  const scrollRef = useRef<HTMLDivElement | null>(null)

  // Keep the newest message in view as the conversation grows.
  useEffect(() => {
    if (open && scrollRef.current) {
      scrollRef.current.scrollTop = scrollRef.current.scrollHeight
    }
  }, [open, messages, pending])

  if (!open) return null

  function submit() {
    const text = draft
    setDraft("")
    void send(text)
  }

  return (
    <div className="bg-popover text-popover-foreground fixed right-4 bottom-16 z-40 flex h-[28rem] w-96 flex-col overflow-hidden rounded-lg border border-border/70 border-t-2 border-t-primary shadow-xl ring-1 ring-black/5">
      <div className="border-primary/15 flex items-center justify-between border-b bg-gradient-to-b from-accent/50 to-accent/20 px-3 py-2">
        <span className="flex items-center gap-1.5 text-sm font-medium">
          <Sparkles className="size-4" />
          Assistant
        </span>
        <div className="flex items-center gap-2">
          <button
            onClick={() => useAssistantChatStore.getState().clear()}
            className="text-muted-foreground hover:text-foreground text-xs"
            title="Clear conversation"
          >
            Clear
          </button>
          <button
            onClick={() => useAssistantChatStore.getState().setOpen(false)}
            className="text-muted-foreground hover:text-foreground"
            title="Close"
          >
            <X className="size-4" />
            <span className="sr-only">Close assistant</span>
          </button>
        </div>
      </div>

      <div ref={scrollRef} className="flex-1 space-y-3 overflow-y-auto px-3 py-3">
        {messages.length === 0 && (
          <p className="text-muted-foreground px-1 text-sm">
            Ask about your network, or describe a change to make. I'll look things up and propose any edits for you to
            confirm.
          </p>
        )}
        {messages.map((message, index) => (
          <MessageBubble key={index} message={message} messageIndex={index} />
        ))}
        {pending && (
          <div className="text-muted-foreground flex items-center gap-2 px-1 text-sm">
            <Loader2 className="size-4 animate-spin" />
            Thinking…
          </div>
        )}
      </div>

      <div className="border-t p-2">
        <div className="flex items-end gap-2">
          <textarea
            value={draft}
            onChange={(e) => setDraft(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter" && !e.shiftKey) {
                e.preventDefault()
                submit()
              }
            }}
            rows={2}
            placeholder="Message the assistant…"
            className="border-input bg-background focus-visible:ring-ring max-h-32 flex-1 resize-none rounded-md border px-2 py-1.5 text-sm focus-visible:ring-1 focus-visible:outline-none"
          />
          <Button size="sm" onClick={submit} disabled={pending || draft.trim() === ""}>
            <Send className="size-4" />
            <span className="sr-only">Send</span>
          </Button>
        </div>
      </div>
    </div>
  )
}

function MessageBubble({ message, messageIndex }: { message: ChatMessage; messageIndex: number }) {
  const isUser = message.role === "user"
  return (
    <div className={cn("flex flex-col gap-2", isUser ? "items-end" : "items-start")}>
      {message.content && (
        <div
          className={cn(
            "max-w-[85%] rounded-md px-3 py-2 text-sm whitespace-pre-wrap",
            isUser
              ? "bg-primary text-primary-foreground"
              : message.error
                ? "bg-destructive/10 text-destructive"
                : "bg-muted",
          )}
        >
          {message.content}
        </div>
      )}
      {message.toolRuns && message.toolRuns.length > 0 && (
        <p className="text-muted-foreground px-1 text-xs">
          Looked up: {message.toolRuns.map((run) => run.tool).join(", ")}
        </p>
      )}
      {message.actions?.map((action, actionIndex) => (
        <ProposalCard
          key={`${action.tool}-${actionIndex}`}
          action={action}
          messageIndex={messageIndex}
          actionIndex={actionIndex}
        />
      ))}
    </div>
  )
}

function ProposalCard({
  action,
  messageIndex,
  actionIndex,
}: {
  action: ChatProposedAction
  messageIndex: number
  actionIndex: number
}) {
  const resolveAction = useAssistantChatStore((s) => s.resolveAction)
  const queryClient = useQueryClient()
  const [busy, setBusy] = useState(false)

  async function confirm() {
    setBusy(true)
    try {
      // The confirmed write runs through the shared agenttools choke point,
      // which re-enforces the tool's commit permission server-side. A relationship
      // write may return a soft plausibility_warning (never blocks) -- surface it.
      const res = await apiFetch<{ result?: { plausibility_warning?: string } }>("/assistant/execute", {
        method: "POST",
        body: JSON.stringify({ tool: action.tool, args: action.args }),
      })

      // The write could have created a feature, a connection, or a relationship;
      // refresh all three read layers rather than guessing which changed.
      queryClient.invalidateQueries({ queryKey: ["connections"] })
      queryClient.invalidateQueries({ queryKey: ["relationships"] })
      queryClient.invalidateQueries({ queryKey: ["features"] })

      if (res.result?.plausibility_warning) {
        useToastStore.getState().pushWarning(res.result.plausibility_warning)
      }
      useToastStore.getState().pushSuccess("Done.")
      resolveAction(messageIndex, actionIndex)
    } catch (err) {
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not complete that action.")
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="bg-background flex w-[85%] flex-col gap-2 rounded-md border p-3 shadow-sm">
      <div className="flex flex-col gap-0.5">
        <p className="text-sm font-medium">{action.title}</p>
        <p className="text-muted-foreground truncate font-mono text-xs">{action.detail || action.tool}</p>
      </div>
      <div className="flex justify-end gap-2">
        <Button variant="ghost" size="sm" onClick={() => resolveAction(messageIndex, actionIndex)} disabled={busy}>
          Dismiss
        </Button>
        <Button size="sm" onClick={confirm} disabled={busy}>
          {busy ? <Loader2 className="size-4 animate-spin" /> : <Check className="size-4" />}
          Confirm
        </Button>
      </div>
    </div>
  )
}
