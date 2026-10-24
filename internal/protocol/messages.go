package protocol

// Messages from plugin → binary

type HelloMsg struct {
	Type         string   `json:"type"` // "hello"
	Version      string   `json:"version"`
	Capabilities []string `json:"capabilities"`
}

type ConfigMsg struct {
	Type     string `json:"type"` // "config"
	Provider string `json:"provider"`
	Model    string `json:"model,omitempty"`
	Endpoint string `json:"endpoint,omitempty"`
}

type ContextBlock struct {
	Kind     string `json:"kind"` // "current_buffer" | "file" | "selection"
	Path     string `json:"path"`
	Language string `json:"language,omitempty"`
	Content  string `json:"content"`
	Start    int    `json:"start,omitempty"`
	End      int    `json:"end,omitempty"`
}

type ChatMsg struct {
	Type    string         `json:"type"` // "chat"
	Session string         `json:"session"`
	Prompt  string         `json:"prompt"`
	Context []ContextBlock `json:"context,omitempty"`
}

type InlineMsg struct {
	Type      string         `json:"type"` // "inline"
	Session   string         `json:"session"`
	Prompt    string         `json:"prompt"`
	File      string         `json:"file"`
	Selection ContextBlock   `json:"selection"`
	Context   []ContextBlock `json:"context,omitempty"`
}

type CancelMsg struct {
	Type    string `json:"type"` // "cancel"
	Session string `json:"session"`
}

type ApproveMsg struct {
	Type     string `json:"type"` // "approve"
	Session  string `json:"session"`
	ToolCall string `json:"tool_call"`
	Decision string `json:"decision"` // "allow" | "allow_always" | "reject" | "reject_always"
}

type ShutdownMsg struct {
	Type string `json:"type"` // "shutdown"
}

// Messages from binary → plugin

type ReadyMsg struct {
	Type string `json:"type"` // "ready"
}

type ErrorMsg struct {
	Type    string `json:"type"` // "error"
	Session string `json:"session,omitempty"`
	Message string `json:"message"`
	Fatal   bool   `json:"fatal"`
}

type StreamStartMsg struct {
	Type    string `json:"type"` // "stream_start"
	Session string `json:"session"`
}

type StreamTextMsg struct {
	Type    string `json:"type"` // "stream_text"
	Session string `json:"session"`
	Text    string `json:"text"`
}

type StreamThinkingMsg struct {
	Type    string `json:"type"` // "stream_thinking"
	Session string `json:"session"`
	Text    string `json:"text"`
}

type Usage struct {
	Input  int `json:"input"`
	Output int `json:"output"`
}

type StreamEndMsg struct {
	Type    string `json:"type"` // "stream_end"
	Session string `json:"session"`
	Usage   *Usage `json:"usage,omitempty"`
}

type ToolCallMsg struct {
	Type             string         `json:"type"` // "tool_call"
	Session          string         `json:"session"`
	ID               string         `json:"id"`
	Name             string         `json:"name"`
	Input            map[string]any `json:"input"`
	RequiresApproval bool           `json:"requires_approval"`
}

type ToolResultMsg struct {
	Type    string `json:"type"` // "tool_result"
	Session string `json:"session"`
	ID      string `json:"id"`
	Output  string `json:"output"`
	IsError bool   `json:"is_error"`
}

type EditProposalMsg struct {
	Type    string `json:"type"` // "edit_proposal"
	Session string `json:"session"`
	File    string `json:"file"`
	Old     string `json:"old"`
	New     string `json:"new"`
	Format  string `json:"format"` // "diff" | "whole" | "udiff"
}
