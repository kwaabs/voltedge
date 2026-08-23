import { useQuery } from "@tanstack/react-query"

import { apiFetch } from "@/lib/api"

/**
 * Assistant capability probe (Phase 19 Slice 3, docs/phase-19-assistant.md).
 * Freeform chat needs a model configured on the server (ASSISTANT_LLM_BASE_URL);
 * the deterministic suggestions do not. The frontend reads GET /assistant/status
 * to decide whether to show the chat entry point at all, so a deployment with no
 * model simply never surfaces a chat box rather than offering one that errors on
 * first use. It rarely changes within a session, so it's cached indefinitely.
 */
export interface AssistantStatus {
  chat_enabled: boolean
  model?: string
}

export function useAssistantStatus() {
  return useQuery({
    queryKey: ["assistant", "status"],
    queryFn: () => apiFetch<AssistantStatus>("/assistant/status"),
    staleTime: Infinity,
  })
}
