// Package llm is the assistant chat surface's model seam (Phase 19 Slice 3,
// docs/phase-19-assistant.md). It defines the one interface the chat
// orchestrator depends on -- Provider -- plus the small, transport-neutral
// message/tool/completion types that cross it, and an env-driven constructor
// that wires up whichever backend is configured.
//
// The orchestrator does not know or care which model answers: it hands the
// Provider the running conversation and the session-scoped tool catalog and
// gets back either free text or a set of tool calls to route. That keeps the
// model a cheap, swappable router (parse intent, pick a tool, fill its args)
// while the deterministic engines behind agenttools do the real domain work.
//
// Slice 3 ships one implementation, the OpenAI-compatible adapter (openai.go),
// because that dialect is what a small local model on Ollama or LM Studio
// speaks natively -- the locked "small local model first" decision. Hosted
// Anthropic/OpenAI adapters are Slice 4; they only have to satisfy this same
// interface, so nothing above this package changes when they land.
package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Role is the author of a conversation message, using the OpenAI-compatible
// vocabulary the adapter maps onto the wire. "tool" carries a tool's result
// back to the model on the next turn of the orchestrator's routing loop.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// Message is one turn in the conversation handed to a Provider. Most messages
// are just Role+Content. An assistant message that requested tools also carries
// ToolCalls (so a subsequent turn can reference them by id); a tool-result
// message sets ToolCallID to say which call it answers. Keeping all four fields
// on one struct lets the orchestrator build the whole transcript as a flat
// slice the adapter translates in one pass.
type Message struct {
	Role       Role
	Content    string
	ToolCalls  []ToolCall // set on an assistant message that requested tools
	ToolCallID string     // set on a tool message: the call it is the result of
}

// ToolSpec advertises one callable tool to the model: its name, a description
// the model reasons over, and the JSON Schema of its arguments. The orchestrator
// builds these straight from the session-scoped agenttools catalog, so the model
// is only ever offered tools the logged-in user is actually allowed to run.
type ToolSpec struct {
	Name        string
	Description string
	Schema      json.RawMessage
}

// ToolCall is the model's request to invoke a tool: a stable id (echoed back on
// the matching tool-result message), the tool name, and the raw JSON arguments
// the model filled in. Args is raw so the orchestrator can pass it straight to
// agenttools.Execute without an intermediate reshape.
type ToolCall struct {
	ID   string
	Name string
	Args json.RawMessage
}

// Completion is one model turn's output: free text (Text) and/or a set of tool
// calls the model wants run. The orchestrator routes the tool calls -- read
// tools inline, write tools into confirmation proposals -- and uses Text as the
// assistant's spoken reply when there are no (more) calls to make.
type Completion struct {
	Text      string
	ToolCalls []ToolCall
}

// Request is a single completion request: the full conversation so far plus the
// tools the model may call this turn. It is intentionally minimal -- the model
// is a router, not a creative writer, so there are no sampling knobs beyond the
// deterministic temperature the adapter pins internally.
type Request struct {
	Messages []Message
	Tools    []ToolSpec
}

// Provider is the one thing the chat orchestrator depends on: run a single model
// turn and return its completion. Name identifies the backend in logs and error
// messages. Every backend (local OpenAI-compatible now; hosted adapters later)
// implements exactly this.
type Provider interface {
	Complete(ctx context.Context, req Request) (*Completion, error)
	Name() string
}

// Defaults for the model call and each backend. Small local models on modest
// hardware can take a while on a cold prompt, so the timeout is generous.
const (
	defaultTimeout     = 60 * time.Second
	defaultOpenAIModel = "llama3.1"

	// The Anthropic backend has a real base URL and, being a hosted chat model,
	// needs an explicit output cap. A small, fast model is the right default for
	// a tool-router; Haiku fits that.
	defaultAnthropicBaseURL = "https://api.anthropic.com"
	defaultAnthropicModel   = "claude-haiku-4-5-20251001"
	defaultAnthropicMaxTok  = 1024
)

// NewFromEnv builds the configured Provider from the environment, following the
// codebase's per-package NewXFromEnv convention (no central config package).
//
// It returns (nil, nil) when the assistant chat is simply not configured -- no
// ASSISTANT_LLM_PROVIDER and no ASSISTANT_LLM_BASE_URL -- so the server boots
// fine without a model and the chat endpoints report themselves disabled rather
// than erroring. It returns a non-nil error only when a provider is asked for
// but its required variables are missing or malformed (an unknown provider, a
// bad timeout, an Anthropic backend with no key), a misconfiguration worth
// failing loudly on.
//
// Recognised variables:
//
//	ASSISTANT_LLM_PROVIDER  "openai" (default) or "anthropic". Empty is treated
//	                        as "openai" when a base URL is set, else disabled.
//	ASSISTANT_LLM_BASE_URL  API base. Required for openai (e.g. Ollama's
//	                        http://localhost:11434/v1, LM Studio's :1234/v1, or
//	                        https://api.openai.com/v1 for hosted OpenAI); optional
//	                        for anthropic (defaults to https://api.anthropic.com).
//	ASSISTANT_LLM_MODEL     model to request (default "llama3.1" for openai,
//	                        a small Claude for anthropic). Must be tool-calling capable.
//	ASSISTANT_LLM_API_KEY   bearer key; optional for a local openai server,
//	                        required for hosted OpenAI and for anthropic.
//	ASSISTANT_LLM_TIMEOUT   per-call timeout as a Go duration (default 60s).
//	ASSISTANT_LLM_MAX_TOKENS  output cap for anthropic (default 1024); ignored by openai.
func NewFromEnv() (Provider, error) {
	providerName := strings.ToLower(strings.TrimSpace(os.Getenv("ASSISTANT_LLM_PROVIDER")))
	baseURL := os.Getenv("ASSISTANT_LLM_BASE_URL")

	// Disabled: nothing names a provider and no base URL implies the default one.
	if providerName == "" && baseURL == "" {
		return nil, nil
	}
	if providerName == "" {
		providerName = "openai"
	}

	timeout, err := envDuration("ASSISTANT_LLM_TIMEOUT", defaultTimeout)
	if err != nil {
		return nil, err
	}
	apiKey := os.Getenv("ASSISTANT_LLM_API_KEY")
	model := os.Getenv("ASSISTANT_LLM_MODEL")

	switch providerName {
	case "openai", "openai-compatible":
		if baseURL == "" {
			return nil, fmt.Errorf("ASSISTANT_LLM_PROVIDER=%s requires ASSISTANT_LLM_BASE_URL", providerName)
		}
		if model == "" {
			model = defaultOpenAIModel
		}
		return newOpenAICompatible(openAIConfig{BaseURL: baseURL, Model: model, APIKey: apiKey, Timeout: timeout}), nil

	case "anthropic":
		if apiKey == "" {
			return nil, fmt.Errorf("ASSISTANT_LLM_PROVIDER=anthropic requires ASSISTANT_LLM_API_KEY")
		}
		if baseURL == "" {
			baseURL = defaultAnthropicBaseURL
		}
		if model == "" {
			model = defaultAnthropicModel
		}
		maxTokens, err := envInt("ASSISTANT_LLM_MAX_TOKENS", defaultAnthropicMaxTok)
		if err != nil {
			return nil, err
		}
		return newAnthropic(anthropicConfig{BaseURL: baseURL, Model: model, APIKey: apiKey, Timeout: timeout, MaxTokens: maxTokens}), nil

	default:
		return nil, fmt.Errorf("unknown ASSISTANT_LLM_PROVIDER %q (want \"openai\" or \"anthropic\")", providerName)
	}
}

// envDuration reads a Go-duration env var, returning def when unset and an error
// when set but unparseable.
func envDuration(key string, def time.Duration) (time.Duration, error) {
	raw := os.Getenv(key)
	if raw == "" {
		return def, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("%s %q is not a valid duration: %w", key, raw, err)
	}
	return d, nil
}

// envInt reads an integer env var, returning def when unset and an error when
// set but unparseable.
func envInt(key string, def int) (int, error) {
	raw := os.Getenv(key)
	if raw == "" {
		return def, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s %q is not a valid integer: %w", key, raw, err)
	}
	return n, nil
}
