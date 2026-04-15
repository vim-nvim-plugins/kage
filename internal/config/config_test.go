package config

import (
	"os"
	"testing"
)

func TestParseBasic(t *testing.T) {
	f, err := os.CreateTemp("", "vibe-config-*.toml")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())

	f.WriteString(`
default_provider = "ollama"

[providers.ollama]
endpoint = "http://localhost:11434"
model = "llama3.2"

[providers.anthropic]
api_key_env = "ANTHROPIC_API_KEY"
model = "claude-sonnet-4-6"

[providers.cerebras]
inherits = "openai"
endpoint = "https://api.cerebras.ai/v1"
api_key_env = "CEREBRAS_API_KEY"
model = "llama3.1-8b"

[providers.openai]
api_key_env = "OPENAI_API_KEY"
model = "gpt-4o"
`)
	f.Close()

	cfg, err := Load(f.Name())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.DefaultProvider != "ollama" {
		t.Errorf("default_provider: got %q want %q", cfg.DefaultProvider, "ollama")
	}

	ollama, err := cfg.Provider("ollama")
	if err != nil {
		t.Fatal(err)
	}
	if ollama.Endpoint != "http://localhost:11434" {
		t.Errorf("ollama endpoint: got %q", ollama.Endpoint)
	}
	if ollama.Model != "llama3.2" {
		t.Errorf("ollama model: got %q", ollama.Model)
	}

	anth, err := cfg.Provider("anthropic")
	if err != nil {
		t.Fatal(err)
	}
	if anth.APIKeyEnv != "ANTHROPIC_API_KEY" {
		t.Errorf("anthropic api_key_env: got %q", anth.APIKeyEnv)
	}

	// Inheritance: cerebras should inherit openai's api_key_env if not set,
	// but cerebras sets its own — so it should keep its own values
	cerebras, err := cfg.Provider("cerebras")
	if err != nil {
		t.Fatal(err)
	}
	if cerebras.Endpoint != "https://api.cerebras.ai/v1" {
		t.Errorf("cerebras endpoint: got %q", cerebras.Endpoint)
	}
	if cerebras.APIKeyEnv != "CEREBRAS_API_KEY" {
		t.Errorf("cerebras api_key_env: got %q", cerebras.APIKeyEnv)
	}
}

func TestDefaults(t *testing.T) {
	cfg, err := Load("/nonexistent/path/config.toml")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DefaultProvider != "ollama" {
		t.Errorf("default provider: got %q", cfg.DefaultProvider)
	}
	if _, err := cfg.Provider("ollama"); err != nil {
		t.Errorf("default ollama missing: %v", err)
	}
}

func TestComments(t *testing.T) {
	f, err := os.CreateTemp("", "vibe-config-*.toml")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	f.WriteString(`
# this is a comment
default_provider = "ollama" # inline comment — not supported but shouldn't crash badly

[providers.ollama]
# another comment
model = "llama3.2"
`)
	f.Close()

	cfg, err := Load(f.Name())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	p, _ := cfg.Provider("ollama")
	if p.Model != "llama3.2" {
		t.Errorf("model: got %q", p.Model)
	}
}
