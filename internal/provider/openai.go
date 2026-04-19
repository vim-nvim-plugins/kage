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
	openaiDefaultEndpoint = "https://api.openai.com"
	openaiDefaultModel    = "gpt-4o"
)

// OpenAI implements the OpenAI chat-completions API with streaming.
// It also works with any OpenAI-compatible endpoint (Groq, Cerebras, Together, etc.).
type OpenAI struct {
	name     string
	endpoint string
	model    string
	apiKey   string
	client   *http.Client
}

func NewOpenAI(name, endpoint, model, apiKey string) *OpenAI {
	if endpoint == "" {
		endpoint = openaiDefaultEndpoint
	}
	if model == "" {
		model = openaiDefaultModel
	}
	return &OpenAI{
		name:     name,
		endpoint: endpoint,
		model:    model,
		apiKey:   apiKey,
		client:   &http.Client{Timeout: 10 * time.Minute},
	}
}

func (o *OpenAI) Name() string        { return o.name }
func (o *OpenAI) SupportsTools() bool { return true }
func (o *OpenAI) Close() error        { return nil }

type openaiRequest struct {
	Model         string          `json:"model"`
	Messages      []openaiMessage `json:"messages"`
	Stream        bool            `json:"stream"`
	StreamOptions *struct {
		IncludeUsage bool `json:"include_usage"`
	} `json:"stream_options,omitempty"`
}

type openaiMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func (o *OpenAI) Stream(ctx context.Context, req Request, out chan<- Event) error {
	msgs := make([]openaiMessage, 0, len(req.Messages)+1)
	if req.System != "" {
		msgs = append(msgs, openaiMessage{Role: "system", Content: req.System})
	}
	for _, m := range req.Messages {
		var sb strings.Builder
		for _, b := range m.Content {
			if b.Type == "text" {
				sb.WriteString(b.Text)
			}
		}
		msgs = append(msgs, openaiMessage{Role: m.Role, Content: sb.String()})
	}

	payload := openaiRequest{
		Model:    o.model,
		Messages: msgs,
		Stream:   true,
		StreamOptions: &struct {
			IncludeUsage bool `json:"include_usage"`
		}{IncludeUsage: true},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("openai: marshal: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, o.endpoint+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("openai: new request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if o.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+o.apiKey)
	}

	resp, err := o.client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("openai: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		json.NewDecoder(resp.Body).Decode(&e) //nolint:errcheck
		return fmt.Errorf("openai: HTTP %d: %s", resp.StatusCode, e.Error.Message)
	}

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
		if data == "[DONE]" {
			break
		}

		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
			Usage *struct {
				PromptTokens     int `json:"prompt_tokens"`
				CompletionTokens int `json:"completion_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}

		if len(chunk.Choices) > 0 && chunk.Choices[0].Delta.Content != "" {
			out <- Event{Type: "text", Text: chunk.Choices[0].Delta.Content}
		}
		if chunk.Usage != nil {
			inputTokens = chunk.Usage.PromptTokens
			outputTokens = chunk.Usage.CompletionTokens
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("openai: read stream: %w", err)
	}

	out <- Event{Type: "end", Usage: &Usage{InputTokens: inputTokens, OutputTokens: outputTokens}}
	return nil
}
