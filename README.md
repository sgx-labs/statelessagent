# SAME — Stateless Agent Memory Engine

[![License: BSL 1.1](https://img.shields.io/badge/License-BSL_1.1-blue.svg)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.25+-00ADD8.svg)](https://go.dev)
[![Latest Release](https://img.shields.io/github/v/release/sgx-labs/statelessagent)](https://github.com/sgx-labs/statelessagent/releases)
[![GitHub Stars](https://img.shields.io/github/stars/sgx-labs/statelessagent)](https://github.com/sgx-labs/statelessagent)

> Every AI session starts from zero. **Not anymore.**

Tired of re-explaining your project architecture, your coding decisions, and where you left off — every single session? SAME gives your AI agent persistent memory across sessions, entirely on your machine.

**Your AI remembers your decisions. Your architecture. Where you left off. Automatically.**

### Why SAME

SAME is a memory engine — a foundation you control and customize to fit your workflow.

- **Local-first** — your notes, decisions, and context never leave your machine. No cloud APIs, no accounts, no API keys
- **Works immediately** — keyword search runs out of the box. Add [Ollama](https://ollama.ai) for semantic search that understands meaning, not just keywords (recommended)
- **Single binary** — one `curl` command, no runtimes, no Docker, no package managers
- **Yours to build on** — SQLite + MCP. Swap embedding providers, adjust retrieval, connect any MCP client. SAME adapts to you, not the other way around

## Install

```bash
curl -fsSL statelessagent.com/install.sh | bash
```

<details>
<summary><strong>Windows (PowerShell)</strong></summary>

```powershell
irm statelessagent.com/install.ps1 | iex
```

If blocked by execution policy, run first: `Set-ExecutionPolicy RemoteSigned -Scope CurrentUser`

</details>

<details>
<summary><strong>Manual install (or have your AI do it)</strong></summary>

If you'd rather not pipe to bash, or you're having an AI assistant install for you:

**macOS (Apple Silicon):**
```bash
mkdir -p ~/.local/bin
curl -fsSL https://github.com/sgx-labs/statelessagent/releases/latest/download/same-darwin-arm64 -o ~/.local/bin/same
chmod +x ~/.local/bin/same
export PATH="$HOME/.local/bin:$PATH"  # add to ~/.zshrc to persist
same init --yes
```

**macOS (Intel):** Build from source (see below) or use Rosetta: `arch -arm64 ./same-darwin-arm64`

**Linux (x86_64):**
```bash
mkdir -p ~/.local/bin
curl -fsSL https://github.com/sgx-labs/statelessagent/releases/latest/download/same-linux-amd64 -o ~/.local/bin/same
chmod +x ~/.local/bin/same
export PATH="$HOME/.local/bin:$PATH"
same init --yes
```

**Build from source (any platform):**
```bash
git clone --depth 1 https://github.com/sgx-labs/statelessagent.git
cd statelessagent && make install
same init --yes
```

</details>

Install [Ollama](https://ollama.ai) for the full semantic search experience (recommended). SAME also works without Ollama using keyword search — you're never blocked.

## What happens when you use SAME

- **Your AI picks up where you left off** — session handoffs are generated automatically, so the next session knows what happened in the last one.
- **Decisions stick** — architectural choices, coding patterns, and project preferences are extracted and remembered. No more "we already decided to use JWT."
- **The right notes surface at the right time** — semantic search finds relevant context from your notes and injects it into your AI's context window. No manual copy-pasting.
- **Notes your AI actually uses get boosted** — a built-in feedback loop tracks which notes the agent references, improving retrieval over time.
- **Ask questions, get answers** — `same ask` uses a local LLM to answer questions from your notes with source citations. "ChatGPT for your notes" — 100% local.
- **Pin critical context** — `same pin` ensures your most important notes are always included, regardless of what you're working on.
- **When something breaks, SAME tells you why** — `same doctor` runs 15 diagnostic checks and tells you exactly what to fix.
- **Everything stays on your machine** — Ollama embeddings + SQLite. No cloud, no API keys, no accounts.

## How it works

```
Your Notes  →  Ollama  →  SQLite  →  Agent Remembers
  (.md)       (embed)    (search)    (hooks / MCP)
```

SAME indexes your markdown notes into a local SQLite database with vector embeddings. When you use an AI coding tool, SAME's hooks automatically surface relevant context. Decisions get extracted, handoffs get generated, and the next session picks up where you left off.

## Quick start

```bash
same demo              # see it in action first (no notes needed)
cd ~/my-project && same init   # or set up your own project
same ask "what did we decide about auth?"  # ask questions, get answers
```

`same demo` creates a temporary vault and walks you through search and RAG — all in under 60 seconds. Works without Ollama.

`same init` finds your notes (including existing README.md, docs/, etc.), indexes them, and configures your AI tools. Works with or without Ollama.

`same tutorial` — 6 hands-on lessons covering search, decisions, pinning, privacy, RAG, and session handoffs. Run `same tutorial search` to try just one.

## Works with

| Tool | Integration |
|------|-------------|
| **Claude Code** | Hooks + MCP (full experience) |
| **Cursor** | MCP |
| **Windsurf** | MCP |
| **Obsidian** | Vault detection |
| **Logseq** | Vault detection |
| **Any MCP client** | 6 search and retrieval tools |

SAME works with any directory of `.md` files. No Obsidian required.

Use `same init --mcp-only` to skip Claude Code hooks and just register the MCP server.

## Privacy by design

SAME creates a three-tier privacy structure in your vault:

| Directory | Indexed by SAME? | Committed to git? | Use for |
|-----------|-----------------|-------------------|---------|
| Your notes | Yes | Your choice | Project docs, decisions, research |
| `_PRIVATE/` | **No** | **No** | API keys, credentials, truly secret notes |
| `research/` | Yes | **No** (gitignored) | Research, analysis, strategy — searchable but local-only |

`same init` creates a `.gitignore` that enforces these boundaries automatically. Privacy is structural — filesystem-level, not policy-based.

## Built with

Go · SQLite + sqlite-vec · Ollama / OpenAI

<details>
<summary><strong>CLI Reference</strong></summary>

| Command | Description |
|---------|-------------|
| `same init` | Set up SAME for your project (start here) |
| `same demo` | See SAME in action with sample notes |
| `same tutorial` | Learn SAME features hands-on (6 lessons) |
| `same ask <question>` | Ask a question, get answers from your notes (RAG) |
| `same status` | See what SAME is tracking |
| `same doctor` | Check system health and diagnose issues |
| `same search <query>` | Search your notes from the command line |
| `same related <path>` | Find notes related to a given note |
| `same pin <path>` | Always include a note in every session |
| `same pin list` | Show all pinned notes |
| `same pin remove <path>` | Unpin a note |
| `same feedback <path> up\|down` | Tell SAME which notes are helpful (or not) |
| `same repair` | Back up database and force-rebuild index |
| `same reindex [--force]` | Scan notes and rebuild the search index |
| `same display full\|compact\|quiet` | Control output verbosity |
| `same profile use precise\|balanced\|broad` | Adjust precision vs coverage |
| `same config show` | Show effective configuration |
| `same config edit` | Open config in $EDITOR |
| `same setup hooks` | Install/update Claude Code hooks |
| `same setup mcp` | Register MCP server |
| `same log` | Recent SAME activity |
| `same stats` | Show how many notes are indexed |
| `same watch` | Auto-reindex on file changes |
| `same budget` | Context utilization report |
| `same vault list\|add\|remove` | Manage multiple vaults |
| `same version [--check]` | Version and update check |
| `same update` | Update to the latest version |

</details>

<details>
<summary><strong>Configuration</strong></summary>

SAME uses `.same/config.toml`, generated by `same init`:

```toml
[vault]
path = "/home/user/notes"
# skip_dirs = [".venv", "build"]
# noise_paths = ["experiments/", "raw_outputs/"]
handoff_dir = "sessions"
decision_log = "decisions.md"

[ollama]
url = "http://localhost:11434"

[embedding]
provider = "ollama"           # "ollama" (default) or "openai"
model = "nomic-embed-text"    # or "text-embedding-3-small" for openai
# api_key = ""                # required for openai, or set SAME_EMBED_API_KEY

[memory]
max_token_budget = 800
max_results = 2
distance_threshold = 16.2
composite_threshold = 0.65

[hooks]
context_surfacing = true
decision_extractor = true
handoff_generator = true
feedback_loop = true
staleness_check = true
```

Configuration priority (highest wins):

1. CLI flags (`--vault`)
2. Environment variables (`VAULT_PATH`, `OLLAMA_URL`, `SAME_*`)
3. Config file (`.same/config.toml`)
4. Built-in defaults

| Variable | Default | Description |
|----------|---------|-------------|
| `VAULT_PATH` | auto-detect | Path to your markdown notes |
| `OLLAMA_URL` | `http://localhost:11434` | Ollama API (must be localhost) |
| `SAME_DATA_DIR` | `<vault>/.same/data` | Database location |
| `SAME_HANDOFF_DIR` | `sessions` | Handoff notes directory |
| `SAME_DECISION_LOG` | `decisions.md` | Decision log path |
| `SAME_EMBED_PROVIDER` | `ollama` | Embedding provider (`ollama` or `openai`) |
| `SAME_EMBED_MODEL` | `nomic-embed-text` | Embedding model name |
| `SAME_EMBED_API_KEY` | *(none)* | API key (required for `openai` provider) |
| `SAME_SKIP_DIRS` | *(none)* | Extra dirs to skip (comma-separated) |
| `SAME_NOISE_PATHS` | *(none)* | Paths filtered from context surfacing (comma-separated) |

</details>

<details>
<summary><strong>MCP Server</strong></summary>

SAME exposes 6 tools via MCP:

| Tool | Description |
|------|-------------|
| `search_notes` | Semantic search |
| `search_notes_filtered` | Search with domain/workstream/tag filters |
| `get_note` | Read full note by path |
| `find_similar_notes` | Find related notes |
| `reindex` | Re-index the vault |
| `index_stats` | Index statistics |

</details>

<details>
<summary><strong>Display Modes</strong></summary>

Control how much SAME shows when surfacing context:

| Mode | Command | Description |
|------|---------|-------------|
| **full** | `same display full` | Box with note titles, match terms, token counts (default) |
| **compact** | `same display compact` | One-line summary: "surfaced 2 of 847 memories" |
| **quiet** | `same display quiet` | Silent — context injected with no visual output |

Display mode is saved to `.same/config.toml` and takes effect on the next prompt.

</details>

<details>
<summary><strong>Push Protection (Guard)</strong></summary>

Prevent accidental git pushes when running multiple AI agents on the same machine.

```bash
# Enable push protection
same guard settings set push-protect on

# Before pushing, explicitly allow it
same push-allow

# Check guard status
same guard status
```

When enabled, a pre-push git hook blocks pushes unless a one-time ticket has been created via `same push-allow`. Tickets expire after 30 seconds by default (configurable via `same guard settings set push-timeout N`).

</details>

<details>
<summary><strong>Troubleshooting</strong></summary>

**"No vault found"**
SAME can't find your notes directory. Fix:
- Run `same init` from inside your notes folder
- Or set `VAULT_PATH=/path/to/notes` in your environment
- Or use `same vault add myproject /path/to/notes`

**"Ollama not responding"**
The embedding provider is unreachable. Fix:
- Check if Ollama is running (look for the llama icon)
- Test with: `curl http://localhost:11434/api/tags`
- If using a non-default port, set `OLLAMA_URL=http://localhost:<port>`
- SAME will automatically fall back to keyword search if Ollama is temporarily down

**Hooks not firing**
Context isn't being surfaced during Claude Code sessions. Fix:
- Run `same setup hooks` to reinstall hooks
- Verify with `same status` (hooks should show as "active")
- Check `.claude/settings.json` exists in your project

**Context not surfacing**
Hooks fire but no notes appear. Fix:
- Run `same doctor` to diagnose all 15 checks
- Run `same reindex` if your notes have changed
- Try `same search "your query"` to test search directly
- Check if display mode is set to "quiet": `same config show`

**"Cannot open SAME database"**
The SQLite database is missing or corrupted. Fix:
- Run `same repair` to back up and rebuild automatically
- Or run `same init` to set up from scratch
- Or run `same reindex --force` to rebuild the index

</details>

## FAQ

**Do I need Obsidian?**
No. Any directory of `.md` files works.

**Do I need Ollama?**
Recommended, not required. Ollama gives you semantic search — SAME understands *meaning*, not just keywords. Without Ollama, SAME falls back to keyword search (FTS5) so you're never blocked. You can also use OpenAI embeddings (`SAME_EMBED_PROVIDER=openai`). If Ollama goes down temporarily, SAME falls back to keywords automatically.

**Does it slow down my prompts?**
50-200ms. Embedding is the bottleneck — search and scoring take <5ms.

**Is my data sent anywhere?**
SAME is fully local. Context surfaced to your AI tool is sent to that tool's API as part of your conversation, same as pasting it manually.

**How much disk space?**
5-15MB for a few hundred notes.

**Can I use multiple vaults?**
Yes. `same vault add work ~/work-notes && same vault default work`.

## Security & Privacy

- All data stays local — no external API calls except Ollama on localhost
- Ollama URL validated to localhost-only
- `_PRIVATE/` directories excluded from indexing and context surfacing
- `research/` indexed but gitignored by default — your AI can search it, but it never leaves your machine
- Snippets scanned for prompt injection patterns before injection
- Path traversal blocked in MCP `get_note` tool
- **Push protection** — `same guard settings set push-protect on` requires explicit `same push-allow` before git push (prevents accidental pushes when running multiple agents)

## Building from Source

```bash
git clone https://github.com/sgx-labs/statelessagent.git
cd statelessagent && make install
```

Requires Go 1.25+ and CGO.

## Support

[Buy me a coffee](https://buymeacoffee.com/sgxlabs) · [GitHub Sponsors](https://github.com/sponsors/sgx-labs)

## License

BSL 1.1 — free for personal, educational, hobby, research, and evaluation use. Change date: 2030-02-02 (converts to Apache 2.0). See [LICENSE](LICENSE).
