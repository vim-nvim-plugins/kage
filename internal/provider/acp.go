package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// ACP (Agent Command Protocol) runs a local subprocess as a provider.
// The subprocess receives a single JSON line on stdin and writes NDJSON events to stdout.
//
// Input (one line):
//
//	{"messages":[{"role":"user","content":"..."}],"system":"..."}
//
// Output (one JSON object per line):
//
//	{"type":"text","text":"..."}
//	{"type":"thinking","text":"..."}
//	{"type":"end","usage":{"input":N,"output":N}}
//	{"type":"error","message":"..."}
//
// The subprocess is spawned fresh for each request via `sh -c <command>`.
// It inherits the parent process environment, with any keys in the provider's
// `env` config block added (or overriding) on top.
type ACP struct {
	name    string
	command string
	env     map[string]string
}

func NewACP(name, command string, env map[string]string) *ACP {
	return &ACP{name: name, command: command, env: env}
}

func (a *ACP) Name() string        { return a.name }
func (a *ACP) SupportsTools() bool { return false }
func (a *ACP) Close() error        { return nil }

type acpRequest struct {
	Messages []acpMessage `json:"messages"`
	System   string       `json:"system,omitempty"`
}

type acpMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func (a *ACP) Stream(ctx context.Context, req Request, out chan<- Event) error {
	if a.command == "" {
		return fmt.Errorf("acp provider %q: no command configured", a.name)
	}

	msgs := make([]acpMessage, 0, len(req.Messages))
	for _, m := range req.Messages {
		var sb strings.Builder
		for _, b := range m.Content {
			if b.Type == "text" {
				sb.WriteString(b.Text)
			}
		}
		msgs = append(msgs, acpMessage{Role: m.Role, Content: sb.String()})
	}

	body, err := json.Marshal(acpRequest{Messages: msgs, System: req.System})
	if err != nil {
		return fmt.Errorf("acp: marshal: %w", err)
	}

	cmd := exec.CommandContext(ctx, "sh", "-c", a.command)

	// Inherit the parent environment, then apply configured overrides.
	// Build a map first so duplicate keys resolve correctly (overrides win).
	envMap := make(map[string]string, len(os.Environ())+len(a.env))
	for _, e := range os.Environ() {
		k, v, _ := strings.Cut(e, "=")
		envMap[k] = v
	}
	for k, v := range a.env {
		envMap[k] = v
	}
	envSlice := make([]string, 0, len(envMap))
	for k, v := range envMap {
		envSlice = append(envSlice, k+"="+v)
	}
	cmd.Env = envSlice

	cmd.Stdin = bytes.NewReader(body)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("acp: stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("acp: start %q: %w", a.command, err)
	}

	streamErr := readACPEvents(ctx, stdout, out)

	if waitErr := cmd.Wait(); waitErr != nil && ctx.Err() == nil && streamErr == nil {
		return fmt.Errorf("acp: subprocess exited: %w", waitErr)
	}
	return streamErr
}

func readACPEvents(ctx context.Context, r io.Reader, out chan<- Event) error {
	scanner := bufio.NewScanner(r)
	var inputTokens, outputTokens int

	for scanner.Scan() {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var ev struct {
			Type    string `json:"type"`
			Text    string `json:"text"`
			Message string `json:"message"`
			Usage   *struct {
				Input  int `json:"input"`
				Output int `json:"output"`
			} `json:"usage"`
		}
		if err := json.Unmarshal(line, &ev); err != nil {
			continue
		}

		switch ev.Type {
		case "text":
			out <- Event{Type: "text", Text: ev.Text}
		case "thinking":
			out <- Event{Type: "thinking", Text: ev.Text}
		case "error":
			out <- Event{Type: "error", Err: fmt.Errorf("%s", ev.Message)}
		case "end":
			if ev.Usage != nil {
				inputTokens = ev.Usage.Input
				outputTokens = ev.Usage.Output
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("acp: read: %w", err)
	}

	out <- Event{Type: "end", Usage: &Usage{InputTokens: inputTokens, OutputTokens: outputTokens}}
	return nil
}
