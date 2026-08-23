package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// openAIConfig configures the OpenAI-compatible adapter. BaseURL is the API
// root that exposes /chat/completions (Ollama's http://host:11434/v1, LM
// Studio's http://host:1234/v1, or the hosted OpenAI base). APIKey is optional;
// local servers accept any or none, hosted ones require a real key.
type openAIConfig struct {
	BaseURL string
	Model   string
	APIKey  string
	Timeout time.Duration
}

// openAICompatible is a Provider that speaks the OpenAI Chat Completions dialect
// -- the lingua franca a small local model exposes through Ollama or LM Studio.
// It translates our transport-neutral Message/ToolSpec/Completion types to and
// from that wire shape and does nothing else: no retries, no streaming, one
// request per turn, because the orchestrator drives the routing loop above it.
type openAICompatible struct {
	cfg    openAIConfig
	client *http.Client
}

func newOpenAICompatible(cfg openAIConfig) *openAICompatible {
	return &openAICompatible{
		cfg:    cfg,
		client: &http.Client{Timeout: cfg.Timeout},
	}
}

func (p *openAICompatible) Name() string { return "openai-compatible(" + p.cfg.Model + ")" }

// routerTemperature pins sampling to 0: the model's job here is deterministic
// routing (pick a tool, fill its args), not prose, so the least-random decoding
// is what we want.
const routerTemperature = 0

// --- wire types (OpenAI Chat Completions) --------------------------------

type chatCompletionsRequest struct {
	Model       string     `json:"model"`
	Messages    []wireMsg  `json:"messages"`
	Tools       []wireTool `json:"tools,omitempty"`
	ToolChoice  string     `json:"tool_choice,omitempty"`
	Temperature float64    `json:"temperature"`
	Stream      bool       `json:"stream"`
}

type wireMsg struct {
	Role       string         `json:"role"`
	Content    string         `json:"content"`
	ToolCalls  []wireToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
}

type wireTool struct {
	Type     string           `json:"type"` // always "function"
	Function wireToolFunction `json:"function"`
}

type wireToolFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type wireToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"` // "function"
	Function struct {
		Name string `json:"name"`
		// Arguments is a JSON *string* in this dialect, not an object.
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type chatCompletionsResponse struct {
	Choices []struct {
		Message struct {
			Content   string         `json:"content"`
			ToolCalls []wireToolCall `json:"tool_calls"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// Complete runs one turn against the /chat/completions endpoint. It advertises
// the tools with tool_choice:"auto" so a small model is free to answer directly
// when no tool is needed, and maps the response back into our Completion --
// pulling out any tool calls (parsing each call's JSON-string arguments into raw
// JSON) and the free-text reply.
func (p *openAICompatible) Complete(ctx context.Context, req Request) (*Completion, error) {
	body := chatCompletionsRequest{
		Model:       p.cfg.Model,
		Messages:    toWireMessages(req.Messages),
		Temperature: routerTemperature,
	}
	if len(req.Tools) > 0 {
		body.Tools = toWireTools(req.Tools)
		body.ToolChoice = "auto"
	}

	buf, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("llm: encode request: %w", err)
	}

	url := strings.TrimRight(p.cfg.BaseURL, "/") + "/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(buf))
	if err != nil {
		return nil, fmt.Errorf("llm: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if p.cfg.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+p.cfg.APIKey)
	}

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("llm: call %s: %w", p.Name(), err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("llm: read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("llm: %s returned %d: %s", p.Name(), resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var parsed chatCompletionsResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("llm: decode response: %w", err)
	}
	if parsed.Error != nil {
		return nil, fmt.Errorf("llm: %s error: %s", p.Name(), parsed.Error.Message)
	}
	if len(parsed.Choices) == 0 {
		return nil, fmt.Errorf("llm: %s returned no choices", p.Name())
	}

	msg := parsed.Choices[0].Message
	out := &Completion{Text: msg.Content}
	for _, tc := range msg.ToolCalls {
		args := json.RawMessage(tc.Function.Arguments)
		if len(strings.TrimSpace(tc.Function.Arguments)) == 0 {
			// A model sometimes emits a call with empty arguments; normalise to
			// an empty object so downstream json decoding never chokes on "".
			args = json.RawMessage("{}")
		}
		out.ToolCalls = append(out.ToolCalls, ToolCall{
			ID:   tc.ID,
			Name: tc.Function.Name,
			Args: args,
		})
	}
	return out, nil
}

func toWireMessages(msgs []Message) []wireMsg {
	out := make([]wireMsg, 0, len(msgs))
	for _, m := range msgs {
		wm := wireMsg{Role: string(m.Role), Content: m.Content, ToolCallID: m.ToolCallID}
		for _, tc := range m.ToolCalls {
			wtc := wireToolCall{ID: tc.ID, Type: "function"}
			wtc.Function.Name = tc.Name
			wtc.Function.Arguments = string(tc.Args)
			wm.ToolCalls = append(wm.ToolCalls, wtc)
		}
		out = append(out, wm)
	}
	return out
}

func toWireTools(tools []ToolSpec) []wireTool {
	out := make([]wireTool, 0, len(tools))
	for _, t := range tools {
		out = append(out, wireTool{
			Type: "function",
			Function: wireToolFunction{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.Schema,
			},
		})
	}
	return out
}
