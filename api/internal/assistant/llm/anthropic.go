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

// anthropicConfig configures the Anthropic Messages backend. BaseURL is the API
// root (default https://api.anthropic.com); the adapter appends /v1/messages.
// APIKey is required (hosted, no anonymous access). MaxTokens caps the model's
// output -- required by the Messages API, and small for a router.
type anthropicConfig struct {
	BaseURL   string
	Model     string
	APIKey    string
	Timeout   time.Duration
	MaxTokens int
}

// anthropicVersion is the required API version header. Pinned rather than
// tracking "latest" so a server-side change can't silently alter the wire shape
// this adapter maps.
const anthropicVersion = "2023-06-01"

// anthropic is a Provider that speaks the Anthropic Messages dialect. It differs
// from the OpenAI dialect in three ways this adapter absorbs: the system prompt
// is a top-level field (not a message), tool calls and their results are content
// *blocks* (tool_use / tool_result) rather than a side channel, and a tool
// result is carried on a user message, not a dedicated "tool" role. Everything
// above this file sees only the neutral llm types.
type anthropic struct {
	cfg    anthropicConfig
	client *http.Client
}

func newAnthropic(cfg anthropicConfig) *anthropic {
	return &anthropic{cfg: cfg, client: &http.Client{Timeout: cfg.Timeout}}
}

func (p *anthropic) Name() string { return "anthropic(" + p.cfg.Model + ")" }

// --- wire types (Anthropic Messages) -------------------------------------

type anthropicRequest struct {
	Model       string               `json:"model"`
	MaxTokens   int                  `json:"max_tokens"`
	System      string               `json:"system,omitempty"`
	Messages    []anthropicMessage   `json:"messages"`
	Tools       []anthropicTool      `json:"tools,omitempty"`
	ToolChoice  *anthropicToolChoice `json:"tool_choice,omitempty"`
	Temperature float64              `json:"temperature"`
}

type anthropicMessage struct {
	Role    string             `json:"role"`
	Content []anthropicContent `json:"content"`
}

// anthropicContent is one content block. A block is exactly one of text /
// tool_use / tool_result; the unused fields stay omitempty so each block
// serializes to just its own shape.
type anthropicContent struct {
	Type string `json:"type"`
	// text
	Text string `json:"text,omitempty"`
	// tool_use
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
	// tool_result
	ToolUseID string `json:"tool_use_id,omitempty"`
	Content   string `json:"content,omitempty"`
}

type anthropicTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type anthropicToolChoice struct {
	Type string `json:"type"` // "auto"
}

type anthropicResponse struct {
	Content []struct {
		Type  string          `json:"type"`
		Text  string          `json:"text"`
		ID    string          `json:"id"`
		Name  string          `json:"name"`
		Input json.RawMessage `json:"input"`
	} `json:"content"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// Complete runs one turn against /v1/messages. It lifts the system prompt out of
// the message list, translates the rest into Anthropic's block shape, advertises
// the tools with tool_choice auto, and maps the response's content blocks back
// into our Completion (text blocks concatenated, tool_use blocks as ToolCalls).
func (p *anthropic) Complete(ctx context.Context, req Request) (*Completion, error) {
	system, messages := toAnthropicMessages(req.Messages)
	body := anthropicRequest{
		Model:       p.cfg.Model,
		MaxTokens:   p.cfg.MaxTokens,
		System:      system,
		Messages:    messages,
		Temperature: routerTemperature,
	}
	if len(req.Tools) > 0 {
		body.Tools = toAnthropicTools(req.Tools)
		body.ToolChoice = &anthropicToolChoice{Type: "auto"}
	}

	buf, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("llm: encode request: %w", err)
	}

	url := strings.TrimRight(p.cfg.BaseURL, "/") + "/v1/messages"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(buf))
	if err != nil {
		return nil, fmt.Errorf("llm: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-Api-Key", p.cfg.APIKey)
	httpReq.Header.Set("Anthropic-Version", anthropicVersion)

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

	var parsed anthropicResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("llm: decode response: %w", err)
	}
	if parsed.Error != nil {
		return nil, fmt.Errorf("llm: %s error: %s", p.Name(), parsed.Error.Message)
	}

	out := &Completion{}
	var text strings.Builder
	for _, block := range parsed.Content {
		switch block.Type {
		case "text":
			text.WriteString(block.Text)
		case "tool_use":
			input := block.Input
			if len(strings.TrimSpace(string(input))) == 0 {
				input = json.RawMessage("{}")
			}
			out.ToolCalls = append(out.ToolCalls, ToolCall{ID: block.ID, Name: block.Name, Args: input})
		}
	}
	out.Text = text.String()
	return out, nil
}

// toAnthropicMessages splits our flat message list into Anthropic's shape: the
// system prompt (all RoleSystem messages joined) as a top-level string, and the
// rest as user/assistant messages of content blocks. Consecutive tool results
// are coalesced into one user message -- the Messages API requires the results
// of a single assistant turn's tool calls to arrive together as tool_result
// blocks in one user message, not as separate messages.
func toAnthropicMessages(msgs []Message) (string, []anthropicMessage) {
	var system strings.Builder
	var out []anthropicMessage

	for _, m := range msgs {
		switch m.Role {
		case RoleSystem:
			if system.Len() > 0 {
				system.WriteString("\n\n")
			}
			system.WriteString(m.Content)

		case RoleUser:
			out = append(out, anthropicMessage{
				Role:    "user",
				Content: []anthropicContent{{Type: "text", Text: m.Content}},
			})

		case RoleAssistant:
			var blocks []anthropicContent
			if m.Content != "" {
				blocks = append(blocks, anthropicContent{Type: "text", Text: m.Content})
			}
			for _, tc := range m.ToolCalls {
				input := tc.Args
				if len(input) == 0 {
					input = json.RawMessage("{}")
				}
				blocks = append(blocks, anthropicContent{Type: "tool_use", ID: tc.ID, Name: tc.Name, Input: input})
			}
			out = append(out, anthropicMessage{Role: "assistant", Content: blocks})

		case RoleTool:
			block := anthropicContent{Type: "tool_result", ToolUseID: m.ToolCallID, Content: m.Content}
			// Append to the preceding user message when it is already a
			// tool-result batch; otherwise start a new user message.
			if n := len(out); n > 0 && out[n-1].Role == "user" && isToolResultBatch(out[n-1]) {
				out[n-1].Content = append(out[n-1].Content, block)
			} else {
				out = append(out, anthropicMessage{Role: "user", Content: []anthropicContent{block}})
			}
		}
	}
	return system.String(), out
}

// isToolResultBatch reports whether a user message holds tool_result blocks (so
// a further tool result coalesces into it). The orchestrator only ever produces
// a user message that is either a single plain-text turn or a tool-result batch,
// so checking the first block's type is sufficient.
func isToolResultBatch(m anthropicMessage) bool {
	return len(m.Content) > 0 && m.Content[0].Type == "tool_result"
}

func toAnthropicTools(tools []ToolSpec) []anthropicTool {
	out := make([]anthropicTool, 0, len(tools))
	for _, t := range tools {
		out = append(out, anthropicTool{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.Schema,
		})
	}
	return out
}
