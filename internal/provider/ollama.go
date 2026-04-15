package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type Ollama struct {
	name     string
	endpoint string
	model    string
	client   *http.Client
}

func NewOllama(name, endpoint, model string) *Ollama {
	return &Ollama{
		name:     name,
		endpoint: endpoint,
		model:    model,
		client:   &http.Client{Timeout: 5 * time.Minute},
	}
}

func (o *Ollama) Name() string        { return o.name }
func (o *Ollama) SupportsTools() bool { return false }
func (o *Ollama) Close() error        { return nil }

func (o *Ollama) Stream(ctx context.Context, req Request, out chan<- Event) error {
	body, err := o.buildRequest(req)
	if err != nil {
		return fmt.Errorf("ollama: build request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, o.endpoint+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("ollama: new request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := o.client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("ollama: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error string `json:"error"`
		}
		json.NewDecoder(resp.Body).Decode(&e) //nolint:errcheck
		return fmt.Errorf("ollama: server returned %d: %s", resp.StatusCode, e.Error)
	}

	scanner := bufio.NewScanner(resp.Body)
	var totalInput, totalOutput int

	for scanner.Scan() {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		var chunk ollamaChunk
		if err := json.Unmarshal(scanner.Bytes(), &chunk); err != nil {
			return fmt.Errorf("ollama: parse chunk: %w", err)
		}

		if chunk.Message.Content != "" {
			out <- Event{Type: "text", Text: chunk.Message.Content}
		}

		if chunk.Done {
			totalInput = chunk.PromptEvalCount
			totalOutput = chunk.EvalCount
			break
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("ollama: read stream: %w", err)
	}

	out <- Event{
		Type: "end",
		Usage: &Usage{
			InputTokens:  totalInput,
			OutputTokens: totalOutput,
		},
	}
	return nil
}

// ollamaRequest is the wire format for /api/chat
type ollamaRequest struct {
	Model    string          `json:"model"`
	Messages []ollamaMessage `json:"messages"`
	Stream   bool            `json:"stream"`
}

type ollamaMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ollamaChunk is one line in the streaming response
type ollamaChunk struct {
	Message struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"message"`
	Done            bool `json:"done"`
	PromptEvalCount int  `json:"prompt_eval_count"`
	EvalCount       int  `json:"eval_count"`
}

func (o *Ollama) buildRequest(req Request) ([]byte, error) {
	msgs := make([]ollamaMessage, 0, len(req.Messages)+1)

	// Prepend system message if present
	if req.System != "" {
		msgs = append(msgs, ollamaMessage{Role: "system", Content: req.System})
	}

	for _, m := range req.Messages {
		content := ""
		for _, block := range m.Content {
			if block.Type == "text" {
				content += block.Text
			}
		}
		msgs = append(msgs, ollamaMessage{Role: m.Role, Content: content})
	}

	return json.Marshal(ollamaRequest{
		Model:    o.model,
		Messages: msgs,
		Stream:   true,
	})
}
