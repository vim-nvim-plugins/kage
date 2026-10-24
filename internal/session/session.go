package session

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"

	"github.com/vim-nvim-plugins/kage/internal/protocol"
	"github.com/vim-nvim-plugins/kage/internal/provider"
)

// Session holds the conversation history for one chat thread.
type Session struct {
	ID       string
	messages []provider.Message
	pinned   []protocol.ContextBlock // files added via KageAddFile

	mu     sync.Mutex
	cancel context.CancelFunc
}

func New(id string) *Session {
	return &Session{ID: id}
}

// AddPinned adds a file to be included in every subsequent request.
func (s *Session) AddPinned(b protocol.ContextBlock) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pinned = append(s.pinned, b)
}

// Cancel aborts any in-flight stream for this session.
func (s *Session) Cancel() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		s.cancel()
	}
}

// Chat sends a user prompt (with optional context blocks) to the provider and
// streams events back to stdout as protocol messages.
func (s *Session) Chat(p provider.Provider, prompt string, ctxBlocks []protocol.ContextBlock) {
	s.mu.Lock()
	if s.cancel != nil {
		s.cancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.mu.Unlock()

	defer cancel()

	userContent := s.buildUserContent(prompt, ctxBlocks)
	userMsg := provider.Message{Role: "user", Content: userContent}

	s.mu.Lock()
	s.messages = append(s.messages, userMsg)
	msgs := make([]provider.Message, len(s.messages))
	copy(msgs, s.messages)
	s.mu.Unlock()

	emit(protocol.StreamStartMsg{Type: "stream_start", Session: s.ID})

	req := provider.Request{Messages: msgs}
	out := make(chan provider.Event, 32)

	var streamErr error
	done := make(chan struct{})
	go func() {
		defer close(done)
		streamErr = p.Stream(ctx, req, out)
		close(out)
	}()

	var assistantText string
	var usage *protocol.Usage

	for ev := range out {
		switch ev.Type {
		case "text":
			assistantText += ev.Text
			emit(protocol.StreamTextMsg{Type: "stream_text", Session: s.ID, Text: ev.Text})
		case "thinking":
			emit(protocol.StreamThinkingMsg{Type: "stream_thinking", Session: s.ID, Text: ev.Text})
		case "end":
			if ev.Usage != nil {
				usage = &protocol.Usage{
					Input:  ev.Usage.InputTokens,
					Output: ev.Usage.OutputTokens,
				}
			}
		case "error":
			emitError(s.ID, fmt.Sprintf("provider error: %v", ev.Err), false)
		}
	}

	<-done

	if streamErr != nil && ctx.Err() == nil {
		emitError(s.ID, fmt.Sprintf("stream: %v", streamErr), false)
	}

	// Record assistant turn in history
	if assistantText != "" {
		s.mu.Lock()
		s.messages = append(s.messages, provider.Message{
			Role:    "assistant",
			Content: []provider.ContentBlock{{Type: "text", Text: assistantText}},
		})
		s.mu.Unlock()
	}

	emit(protocol.StreamEndMsg{Type: "stream_end", Session: s.ID, Usage: usage})
}

// buildUserContent assembles the content blocks for a user turn.
// Pinned files come first, then any per-request context blocks, then the prompt.
func (s *Session) buildUserContent(prompt string, extra []protocol.ContextBlock) []provider.ContentBlock {
	s.mu.Lock()
	pinned := make([]protocol.ContextBlock, len(s.pinned))
	copy(pinned, s.pinned)
	s.mu.Unlock()

	var blocks []provider.ContentBlock

	for _, b := range append(pinned, extra...) {
		var label string
		switch b.Kind {
		case "current_buffer":
			label = fmt.Sprintf("[current file: %s]\n", b.Path)
		case "file":
			label = fmt.Sprintf("[file: %s]\n", b.Path)
		case "selection":
			label = fmt.Sprintf("[selection from %s, lines %d-%d]\n", b.Path, b.Start, b.End)
		}
		text := label
		if b.Language != "" {
			text += fmt.Sprintf("```%s\n%s\n```", b.Language, b.Content)
		} else {
			text += b.Content
		}
		blocks = append(blocks, provider.ContentBlock{Type: "text", Text: text})
	}

	blocks = append(blocks, provider.ContentBlock{Type: "text", Text: prompt})
	return blocks
}

func emit(msg any) {
	json.NewEncoder(os.Stdout).Encode(msg) //nolint:errcheck
}

func emitError(session, message string, fatal bool) {
	emit(protocol.ErrorMsg{
		Type:    "error",
		Session: session,
		Message: message,
		Fatal:   fatal,
	})
}
