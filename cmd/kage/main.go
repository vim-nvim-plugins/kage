package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"sync"

	"github.com/vim-nvim-plugins/kage/internal/config"
	"github.com/vim-nvim-plugins/kage/internal/protocol"
	"github.com/vim-nvim-plugins/kage/internal/provider"
	"github.com/vim-nvim-plugins/kage/internal/session"
)

const version = "0.3.0"

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "--version":
			fmt.Println(version)
			return
		case "--help":
			printHelp()
			return
		case "test-provider":
			if len(os.Args) < 3 {
				fmt.Fprintln(os.Stderr, "usage: kage test-provider <name>")
				os.Exit(1)
			}
			runTestProvider(os.Args[2])
			return
		default:
			fmt.Fprintf(os.Stderr, "unknown argument: %s\n", os.Args[1])
			printHelp()
			os.Exit(1)
		}
	}

	runProtocol()
}

func printHelp() {
	fmt.Print(`kage - AI coding assistant helper binary

Usage:
  kage                        protocol mode (stdin/stdout NDJSON) — used by the vim plugin
  kage test-provider <name>   smoke test a configured provider
  kage --version
  kage --help
`)
}

// server holds runtime state for a protocol session.
type server struct {
	enc      *json.Encoder
	cfg      *config.Config
	provider provider.Provider

	mu       sync.Mutex
	sessions map[string]*session.Session
}

func (srv *server) emit(msg any) {
	srv.enc.Encode(msg) //nolint:errcheck
}

func (srv *server) emitError(sess, message string, fatal bool) {
	srv.emit(protocol.ErrorMsg{Type: "error", Session: sess, Message: message, Fatal: fatal})
}

func (srv *server) getOrCreateSession(id string) *session.Session {
	srv.mu.Lock()
	defer srv.mu.Unlock()
	s, ok := srv.sessions[id]
	if !ok {
		s = session.New(id)
		srv.sessions[id] = s
	}
	return s
}

// runProtocol is the main loop: read NDJSON from stdin, dispatch, write NDJSON to stdout.
func runProtocol() {
	cfg, err := config.Load("")
	if err != nil {
		// Non-fatal: log and continue with defaults
		slog.Warn("config load failed, using defaults", "err", err)
		cfg, _ = config.Load("/dev/null") // returns defaults
	}

	srv := &server{
		enc:      json.NewEncoder(os.Stdout),
		cfg:      cfg,
		sessions: make(map[string]*session.Session),
	}

	// Build default provider
	if cfg.DefaultProvider != "" {
		p, err := buildProvider(cfg.DefaultProvider, cfg)
		if err != nil {
			slog.Warn("default provider unavailable", "provider", cfg.DefaultProvider, "err", err)
		} else {
			srv.provider = p
		}
	}

	srv.emit(protocol.ReadyMsg{Type: "ready"})

	scanner := bufio.NewScanner(os.Stdin)
	// Increase buffer for large context blocks
	scanner.Buffer(make([]byte, 4*1024*1024), 4*1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var envelope struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(line, &envelope); err != nil {
			slog.Warn("malformed input line, ignoring", "err", err)
			continue
		}

		switch envelope.Type {
		case "hello":
			// Already sent ready; nothing more to do.
		case "config":
			srv.handleConfig(line)
		case "chat":
			srv.handleChat(line)
		case "inline":
			srv.handleInline(line)
		case "cancel":
			srv.handleCancel(line)
		case "shutdown":
			return
		default:
			slog.Info("unknown message type, ignoring", "type", envelope.Type)
		}
	}

	if err := scanner.Err(); err != nil {
		slog.Error("stdin read error", "err", err)
		os.Exit(1)
	}
}

func (srv *server) handleConfig(line []byte) {
	var msg protocol.ConfigMsg
	if err := json.Unmarshal(line, &msg); err != nil {
		srv.emitError("", "malformed config message", false)
		return
	}
	// Override provider at runtime
	if msg.Provider != "" {
		// Build an ephemeral config entry if model/endpoint were specified inline
		pc, _ := srv.cfg.Provider(msg.Provider)
		if msg.Model != "" {
			pc.Model = msg.Model
		}
		if msg.Endpoint != "" {
			pc.Endpoint = msg.Endpoint
		}
		srv.cfg.Providers[msg.Provider] = pc

		p, err := buildProvider(msg.Provider, srv.cfg)
		if err != nil {
			srv.emitError("", fmt.Sprintf("provider %q: %v", msg.Provider, err), false)
			return
		}
		if srv.provider != nil {
			srv.provider.Close()
		}
		srv.provider = p
	}
}

func (srv *server) handleChat(line []byte) {
	var msg protocol.ChatMsg
	if err := json.Unmarshal(line, &msg); err != nil {
		srv.emitError("", "malformed chat message", false)
		return
	}
	if srv.provider == nil {
		srv.emitError(msg.Session, "no provider configured", false)
		return
	}

	sess := srv.getOrCreateSession(msg.Session)
	p := srv.provider

	// Run in goroutine so we can handle cancel while streaming
	go sess.Chat(p, msg.Prompt, msg.Context)
}

func (srv *server) handleInline(line []byte) {
	var msg protocol.InlineMsg
	if err := json.Unmarshal(line, &msg); err != nil {
		srv.emitError("", "malformed inline message", false)
		return
	}
	if srv.provider == nil {
		srv.emitError(msg.Session, "no provider configured", false)
		return
	}

	sess := srv.getOrCreateSession(msg.Session)
	p := srv.provider

	// Treat the selection as a context block prepended to the prompt.
	ctx := append([]protocol.ContextBlock{msg.Selection}, msg.Context...)
	go sess.Chat(p, msg.Prompt, ctx)
}

func (srv *server) handleCancel(line []byte) {
	var msg protocol.CancelMsg
	if err := json.Unmarshal(line, &msg); err != nil {
		return
	}
	srv.mu.Lock()
	s, ok := srv.sessions[msg.Session]
	srv.mu.Unlock()
	if ok {
		s.Cancel()
	}
}

// runTestProvider sends a simple prompt to the named provider and streams the response to stdout.
func runTestProvider(name string) {
	cfg, err := config.Load("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
		os.Exit(1)
	}

	p, err := buildProvider(name, cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	defer p.Close()

	req := provider.Request{
		Messages: []provider.Message{
			{
				Role: "user",
				Content: []provider.ContentBlock{
					{Type: "text", Text: "Reply with exactly: hello from kage"},
				},
			},
		},
	}

	out := make(chan provider.Event, 16)

	go func() {
		if err := p.Stream(context.Background(), req, out); err != nil {
			fmt.Fprintf(os.Stderr, "stream error: %v\n", err)
		}
		close(out)
	}()

	for ev := range out {
		switch ev.Type {
		case "text":
			fmt.Print(ev.Text)
		case "end":
			fmt.Println()
			if ev.Usage != nil {
				fmt.Fprintf(os.Stderr, "usage: %d in / %d out tokens\n", ev.Usage.InputTokens, ev.Usage.OutputTokens)
			}
		case "error":
			fmt.Fprintf(os.Stderr, "error: %v\n", ev.Err)
			os.Exit(1)
		}
	}
}

// buildProvider constructs the right Provider implementation for a config entry.
// If the provider name is not in the config, a zero ProviderConfig is used so
// that built-in providers (anthropic, openai) work without a config file entry.
func buildProvider(name string, cfg *config.Config) (provider.Provider, error) {
	pc, _ := cfg.Provider(name) // zero value is fine; defaults fill in below

	switch {
	case pc.Type == "acp":
		if pc.Command == "" {
			return nil, fmt.Errorf("provider %q: acp type requires a command", name)
		}
		return provider.NewACP(name, pc.Command, pc.Env), nil

	case name == "anthropic" || pc.Type == "anthropic":
		apiKey := pc.APIKey()
		if apiKey == "" {
			apiKey = os.Getenv("ANTHROPIC_API_KEY")
		}
		if apiKey == "" {
			return nil, fmt.Errorf("provider %q: no API key (set ANTHROPIC_API_KEY or api_key_env in config)", name)
		}
		return provider.NewAnthropic(name, pc.Endpoint, pc.Model, apiKey), nil

	case name == "openai" || pc.Type == "openai" || pc.Inherits == "openai":
		apiKey := pc.APIKey()
		if apiKey == "" {
			apiKey = os.Getenv("OPENAI_API_KEY")
		}
		return provider.NewOpenAI(name, pc.Endpoint, pc.Model, apiKey), nil

	default:
		// Ollama (and any other HTTP endpoint without a recognized type)
		endpoint := pc.Endpoint
		if endpoint == "" {
			endpoint = "http://localhost:11434"
		}
		model := pc.Model
		if model == "" {
			model = "llama3.2"
		}
		return provider.NewOllama(name, endpoint, model), nil
	}
}
