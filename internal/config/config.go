package config

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type ProviderConfig struct {
	Type      string            // "acp" for ACP providers, empty means HTTP
	Inherits  string            // shallow-merge from another provider key
	Endpoint  string
	Model     string
	APIKeyEnv string
	Command   string            // ACP-only
	Env       map[string]string // ACP-only
}

type Config struct {
	DefaultProvider string
	Providers       map[string]ProviderConfig
}

// Load reads the config file, applying provider inheritance.
// If the file doesn't exist, built-in defaults are returned.
func Load(path string) (*Config, error) {
	if path == "" {
		path = defaultPath()
	}

	cfg := &Config{
		Providers: make(map[string]ProviderConfig),
	}

	if _, err := os.Stat(path); os.IsNotExist(err) {
		cfg.DefaultProvider = "ollama"
		cfg.Providers["ollama"] = ProviderConfig{
			Endpoint: "http://localhost:11434",
			Model:    "llama3.2",
		}
		return cfg, nil
	}

	if err := parseFile(path, cfg); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}

	if err := applyInheritance(cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}

// Provider returns a resolved ProviderConfig for the given name.
func (c *Config) Provider(name string) (ProviderConfig, error) {
	p, ok := c.Providers[name]
	if !ok {
		return ProviderConfig{}, fmt.Errorf("config: unknown provider %q", name)
	}
	return p, nil
}

// APIKey resolves the API key by reading the env var named in APIKeyEnv.
func (p *ProviderConfig) APIKey() string {
	if p.APIKeyEnv == "" {
		return ""
	}
	return os.Getenv(p.APIKeyEnv)
}

func defaultPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = filepath.Join(os.Getenv("HOME"), ".config")
	}
	return filepath.Join(dir, "vibe", "config.toml")
}

// parseFile is a minimal TOML parser for the subset vibe needs:
//   - Top-level key = "value" pairs
//   - Section headers [section] and [section.subsection]
//   - String values (quoted or unquoted)
//   - Inline tables for env: {KEY = "val", ...}
//   - Comments (#)
//
// It does not handle multi-line strings, arrays of tables, or other TOML
// features we don't use.
func parseFile(path string, cfg *Config) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	// current section path, e.g. ["providers", "ollama"]
	var section []string
	// current provider being built when section depth == 2 under "providers"
	var provName string
	var prov ProviderConfig

	flush := func() {
		if provName != "" {
			cfg.Providers[provName] = prov
		}
	}

	scanner := bufio.NewScanner(f)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())

		// Skip empty lines and comments
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Section header
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			flush()
			provName = ""
			prov = ProviderConfig{}

			inner := line[1 : len(line)-1]
			section = strings.Split(inner, ".")

			if len(section) == 2 && section[0] == "providers" {
				provName = section[1]
				if existing, ok := cfg.Providers[provName]; ok {
					prov = existing
				}
			}
			continue
		}

		// Key = value
		idx := strings.IndexByte(line, '=')
		if idx < 0 {
			return fmt.Errorf("line %d: expected key = value, got %q", lineNo, line)
		}
		key := strings.TrimSpace(line[:idx])
		val := strings.TrimSpace(line[idx+1:])
		val = unquote(val)

		if len(section) == 0 {
			// Top-level keys
			switch key {
			case "default_provider":
				cfg.DefaultProvider = val
			}
			continue
		}

		if len(section) == 2 && section[0] == "providers" {
			// Provider keys
			switch key {
			case "type":
				prov.Type = val
			case "inherits":
				prov.Inherits = val
			case "endpoint":
				prov.Endpoint = val
			case "model":
				prov.Model = val
			case "api_key_env":
				prov.APIKeyEnv = val
			case "command":
				prov.Command = val
			case "env":
				// Inline table: { KEY = "val", KEY2 = "val2" }
				env, err := parseInlineTable(val)
				if err != nil {
					return fmt.Errorf("line %d: env: %w", lineNo, err)
				}
				prov.Env = env
			}
			continue
		}
	}

	flush()
	return scanner.Err()
}

// unquote strips surrounding double-quotes if present.
func unquote(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return s[1 : len(s)-1]
	}
	return s
}

// parseInlineTable parses a TOML inline table: { KEY = "val", KEY2 = "val2" }
func parseInlineTable(s string) (map[string]string, error) {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "{") || !strings.HasSuffix(s, "}") {
		return nil, fmt.Errorf("expected { ... }, got %q", s)
	}
	inner := s[1 : len(s)-1]
	result := make(map[string]string)
	for _, pair := range strings.Split(inner, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		idx := strings.IndexByte(pair, '=')
		if idx < 0 {
			return nil, fmt.Errorf("expected key = value in inline table, got %q", pair)
		}
		k := strings.TrimSpace(pair[:idx])
		v := unquote(strings.TrimSpace(pair[idx+1:]))
		result[k] = v
	}
	return result, nil
}

func applyInheritance(cfg *Config) error {
	for name, p := range cfg.Providers {
		if p.Inherits == "" {
			continue
		}
		base, ok := cfg.Providers[p.Inherits]
		if !ok {
			return fmt.Errorf("config: provider %q inherits unknown provider %q", name, p.Inherits)
		}
		if base.Inherits != "" {
			return fmt.Errorf("config: provider %q inherits %q which also inherits — chaining not supported", name, p.Inherits)
		}
		cfg.Providers[name] = mergeProvider(base, p)
	}
	return nil
}

func mergeProvider(base, override ProviderConfig) ProviderConfig {
	if override.Type == "" {
		override.Type = base.Type
	}
	if override.Endpoint == "" {
		override.Endpoint = base.Endpoint
	}
	if override.Model == "" {
		override.Model = base.Model
	}
	if override.APIKeyEnv == "" {
		override.APIKeyEnv = base.APIKeyEnv
	}
	if override.Command == "" {
		override.Command = base.Command
	}
	return override
}
