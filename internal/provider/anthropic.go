package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const (
	anthropicDefaultEndpoint = "https://api.anthropic.com"
	anthropicDefaultModel    = "claude-sonnet-4-6"
	anthropicVersion         = "2023-06-01"
)

type Anthropic struct {
	name     string
	endpoint string
	model    string
	apiKey   string
	client   *http.Client
}

func NewAnthropic(name, endpoint, model, apiKey string) *Anthropic {
	if endpoint == "" {
		endpoint = anthropicDefaultEndpoint
	}
	if model == "" {
		model = anthropicDefaultModel
	}
	return &Anthropic{
		name:     name,
		endpoint: endpoint,
		model:    model,
		apiKey:   apiKey,
		client:   &http.Client{Timeout: 10 * time.Minute},
	}
}

func (a *Anthropic) Name() string        { return a.name }
func (a *Anthropic) SupportsTools() bool { return true }
func (a *Anthropic) Close() error        { return nil }

type anthropicRequest struct {
	Model     string             `json:"model"`
	MaxTokens int                `json:"max_tokens"`
	System    string             `json:"system,omitempty"`
	Messages  []anthropicMessage `json:"messages"`
	Stream    bool               `json:"stream"`
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func (a *Anthropic) Stream(ctx context.Context, req Request, out chan<- Event) error {
	if a.apiKey == "" {
		return fmt.Errorf("anthropic: no API key (set ANTHROPIC_API_KEY or api_key_env in config)")
	}

	msgs := make([]anthropicMessage, 0, len(req.Messages))
	for _, m := range req.Messages {
		var sb strings.Builder
		for _, b := range m.Content {
			if b.Type == "text" {
				sb.WriteString(b.Text)
			}
		}
		msgs = append(msgs, anthropicMessage{Role: m.Role, Content: sb.String()})
	}

	body, err := json.Marshal(anthropicRequest{
		Model:     a.model,
		MaxTokens: 8192,
		System:    req.System,
		Messages:  msgs,
		Stream:    true,
	})
	if err != nil {
		return fmt.Errorf("anthropic: marshal: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, a.endpoint+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("anthropic: new request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", a.apiKey)
	httpReq.Header.Set("anthropic-version", anthropicVersion)

	resp, err := a.client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("anthropic: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		json.NewDecoder(resp.Body).Decode(&e) //nolint:errcheck
		return fmt.Errorf("anthropic: HTTP %d: %s", resp.StatusCode, e.Error.Message)
	}

	return a.readSSE(ctx, resp, out)
}

func (a *Anthropic) readSSE(ctx context.Context, resp *http.Response, out chan<- Event) error {
	scanner := bufio.NewScanner(resp.Body)
	var inputTokens, outputTokens int

	for scanner.Scan() {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := line[6:]

		// Use a single envelope struct covering all event shapes we care about.
		var ev struct {
			Type    string `json:"type"`
			Delta   struct {
				Type     string `json:"type"`
				Text     string `json:"text"`
				Thinking string `json:"thinking"`
			} `json:"delta"`
			Message struct {
				Usage struct {
					InputTokens int `json:"input_tokens"`
				} `json:"usage"`
			} `json:"message"`
			Usage struct {
				OutputTokens int `json:"output_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			continue
		}

		switch ev.Type {
		case "message_start":
			inputTokens = ev.Message.Usage.InputTokens
		case "content_block_delta":
			switch ev.Delta.Type {
			case "text_delta":
				out <- Event{Type: "text", Text: ev.Delta.Text}
			case "thinking_delta":
				out <- Event{Type: "thinking", Text: ev.Delta.Thinking}
			}
		case "message_delta":
			if ev.Usage.OutputTokens > 0 {
				outputTokens = ev.Usage.OutputTokens
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("anthropic: read stream: %w", err)
	}

	out <- Event{Type: "end", Usage: &Usage{InputTokens: inputTokens, OutputTokens: outputTokens}}
	return nil
}
