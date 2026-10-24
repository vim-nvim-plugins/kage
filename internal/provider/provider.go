package provider

import "context"

// Provider is the interface every backend must implement.
type Provider interface {
	Name() string
	Stream(ctx context.Context, req Request, out chan<- Event) error
	SupportsTools() bool
	Close() error
}

type Request struct {
	Messages []Message
	Tools    []Tool // nil if SupportsTools() is false or not needed
	System   string
}

type Message struct {
	Role    string // "user" | "assistant" | "tool"
	Content []ContentBlock
}

type ContentBlock struct {
	Type    string // "text" | "tool_use" | "tool_result" | "thinking"
	Text    string
	ToolUse *ToolUse
	Result  *ToolResult
}

type ToolUse struct {
	ID    string
	Name  string
	Input map[string]any
}

type ToolResult struct {
	ToolUseID string
	Content   string
	IsError   bool
}

type Tool struct {
	Name        string
	Description string
	InputSchema map[string]any
}

type Event struct {
	Type     string // "text" | "thinking" | "tool_call" | "end" | "error"
	Text     string
	ToolCall *ToolUse
	Usage    *Usage
	Err      error
}

type Usage struct {
	InputTokens  int
	OutputTokens int
}
