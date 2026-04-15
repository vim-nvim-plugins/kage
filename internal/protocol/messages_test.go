package protocol

import (
	"encoding/json"
	"testing"
)

func TestRoundtrip(t *testing.T) {
	cases := []struct {
		name string
		msg  any
		typ  string
	}{
		{
			name: "hello",
			msg:  HelloMsg{Type: "hello", Version: "0.1.0", Capabilities: []string{"chat"}},
			typ:  "hello",
		},
		{
			name: "ready",
			msg:  ReadyMsg{Type: "ready"},
			typ:  "ready",
		},
		{
			name: "stream_text",
			msg:  StreamTextMsg{Type: "stream_text", Session: "s1", Text: "hello\nworld"},
			typ:  "stream_text",
		},
		{
			name: "error",
			msg:  ErrorMsg{Type: "error", Message: "something went wrong", Fatal: true},
			typ:  "error",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b, err := json.Marshal(tc.msg)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}

			// Must be single line — no embedded newlines outside of escaped \n
			for i, c := range b {
				if c == '\n' {
					t.Errorf("raw newline at byte %d in marshaled output: %s", i, b)
				}
			}

			// Must round-trip through a generic map and preserve type field
			var m map[string]any
			if err := json.Unmarshal(b, &m); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if m["type"] != tc.typ {
				t.Errorf("type field: got %q want %q", m["type"], tc.typ)
			}
		})
	}
}

func TestNewlineEscaping(t *testing.T) {
	// Newlines in text fields must be escaped as \n, not literal newlines
	msg := StreamTextMsg{Type: "stream_text", Session: "s1", Text: "line1\nline2"}
	b, _ := json.Marshal(msg)
	for _, c := range b {
		if c == '\n' {
			t.Errorf("literal newline in JSON output — would break NDJSON framing")
		}
	}
}
