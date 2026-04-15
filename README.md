# vibe

AI coding assistant for Vim and Neovim. Chat with your model about code and
perform inline edits — in classic Vim, Neovim, with local or hosted models.

**Status:** v0.1 — chat with Ollama works end-to-end. Inline diff UI,
Anthropic/OpenAI providers, and ACP mode (Claude Code, Codex CLI) are in
progress.

---

## Why another plugin?

[magenta.nvim](https://github.com/dlants/magenta.nvim) is excellent for
Neovim users who want deep agentic features and are happy with Anthropic's
API. Vibe exists for a different set of constraints:

- Runs in **Vim 8.2 and Neovim** — not Neovim-only
- **No npm.** One Go binary, one Vimscript file, nothing else to install
- **Local models first** — Ollama works out of the box, any OpenAI-compatible
  endpoint via config
- **ACP mode** — front-ends Claude Code, Codex CLI, Gemini CLI as backends
  directly, so you get the full agent loop without leaving Vim
- **Provider choice** — Anthropic, OpenAI, Cerebras, or bring your own via
  the `inherits` config key

If you're on Neovim and want the most capable agentic experience and are
okay with a Node runtime, use magenta. If you want something that works
everywhere, runs local models, or fronts an ACP agent — that's vibe.

---

## Requirements

- Vim 8.2+ or Neovim
- Go 1.22+ (to build the binary)
- A running provider — Ollama by default

---

## Install

### 1. Build the binary

```sh
git clone https://github.com/zblauser/vibe
cd vibe
go build -o vibe ./cmd/vibe
mv vibe ~/.local/bin/   # or anywhere on your PATH
```

### 2. Install the plugin

With [vim-plug](https://github.com/junegunn/vim-plug):

```vim
Plug 'zblauser/vibe', {'rtp': 'plugin'}
```

Or manually — copy `plugin/vibe.vim` into `~/.vim/plugin/` (Vim) or
`~/.config/nvim/plugin/` (Neovim).

### 3. Configure a provider

Create `~/.config/vibe/config.toml`:

```toml
default_provider = "ollama"

[providers.ollama]
endpoint = "http://localhost:11434"
model    = "llama3.2"
```

See [Provider setup](#provider-setup) below for other providers.

---

## Usage

| Command | Description |
|---|---|
| `:Vibe [prompt]` | Open chat, optionally send an initial prompt with current buffer as context |
| `:VibeChat` | Toggle the chat split |
| `:'<,'>VibeEdit [prompt]` | Send a visual selection for inline edit |
| `:VibeInline [prompt]` | Send current line for inline edit |
| `:VibeCancel` | Cancel the current stream |
| `:VibeProvider <name>` | Switch provider for this session |
| `:VibeAddFile <path>` | Pin a file into every subsequent request |
| `:VibeStatus` | Show current provider, model, session, pinned files |

**In the chat buffer:**

| Key | Action |
|---|---|
| `<CR>` | Send the line starting with `> ` as a prompt |
| `q` | Hide the chat buffer (session is preserved) |
| `<C-c>` | Cancel the current stream |

---

## What works in v0.1

- `:VibeChat` opens a chat split; type on a `> ` line and press `<CR>`
- Current buffer is automatically sent as context with every message
- `:'<,'>VibeEdit` and `:VibeAddFile` send selections and pinned files
- Streaming responses render chunk-by-chunk
- Session history is kept — the model remembers the conversation
- `:VibeProvider` switches providers mid-session
- Ollama is the only working provider in this release

## What's coming

- **v0.2:** Inline diff UI (accept/reject edit proposals in a proper diff view),
  Anthropic + OpenAI + Cerebras providers
- **v0.3:** ACP mode — front Claude Code, Codex CLI, Gemini CLI as backends;
  tool approval UX

---

## Smoke test

```sh
vibe test-provider ollama
# Should print the model's response and token usage
```

---

## Provider setup

### Ollama (default, works in v0.1)

```sh
ollama pull llama3.2
```

```toml
[providers.ollama]
endpoint = "http://localhost:11434"
model    = "llama3.2"
```

### Anthropic (v0.2)

```toml
[providers.anthropic]
api_key_env = "ANTHROPIC_API_KEY"
model       = "claude-sonnet-4-6"
```

### OpenAI (v0.2)

```toml
[providers.openai]
api_key_env = "OPENAI_API_KEY"
model       = "gpt-4o"
```

### Custom OpenAI-compatible endpoint (v0.2)

```toml
[providers.cerebras]
inherits    = "openai"
endpoint    = "https://api.cerebras.ai/v1"
api_key_env = "CEREBRAS_API_KEY"
model       = "llama3.1-8b"
```

### ACP agents — Claude Code, Codex CLI (v0.3)

```toml
[providers.claude-code]
type    = "acp"
command = "claude-code-acp"
```

---

## Project layout

```
cmd/vibe/           binary entry point
internal/protocol/  NDJSON message types (binary ↔ plugin)
internal/provider/  provider interface + implementations
internal/session/   conversation state + streaming
internal/config/    config.toml loader
plugin/vibe.vim     the entire Vim plugin (one file)
doc/vibe.txt        :help vibe
```

---

## License

MIT
