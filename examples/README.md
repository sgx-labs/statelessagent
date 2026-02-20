# SAME Example Configurations

Example configuration files for different setups. Copy and adapt for your environment.

## Embedding Providers

| File | Description |
|------|-------------|
| `config-ollama.toml` | Ollama (local, default) — requires [Ollama](https://ollama.com) |
| `config-openai.toml` | OpenAI API — requires `OPENAI_API_KEY` |
| `config-openai-compatible.toml` | Any OpenAI-compatible server (LM Studio, vLLM, llama.cpp, OpenRouter) |
| `config-keyword-only.toml` | Keyword-only mode — no embedding model needed |
| `config-raspberry-pi.toml` | Tuned for low-resource hardware |

## MCP Integration

| File | Description |
|------|-------------|
| `claude-code-mcp.json` | Claude Code MCP server configuration |
| `cursor-mcp.json` | Cursor MCP server configuration (via npx) |

## Usage

Copy a config file to one of these locations:

- **Per-vault:** `<vault>/.same/config.toml` (recommended)
- **Global:** `~/.config/same/config.toml`

Then run `same init` in your vault directory.
