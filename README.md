<!-- mcp-name: io.github.sgx-labs/same -->
# SAME — Stateless Agent Memory Engine

[![License: BSL 1.1](https://img.shields.io/badge/License-BSL_1.1-blue.svg)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.25+-00ADD8.svg)](https://go.dev)
[![Latest Release](https://img.shields.io/github/v/release/sgx-labs/statelessagent)](https://github.com/sgx-labs/statelessagent/releases)
[![GitHub Stars](https://img.shields.io/github/stars/sgx-labs/statelessagent)](https://github.com/sgx-labs/statelessagent)
[![MCP Tools](https://img.shields.io/badge/MCP_Tools-12-8A2BE2.svg)](#mcp-tools)
[![Discord](https://img.shields.io/discord/1468523556076785757?color=5865F2&label=Discord&logo=discord&logoColor=white)](https://discord.gg/9KfTkcGs7g)

> **Your AI forgets everything between sessions. Not anymore.**

Every time you start a new session with Claude Code, Cursor, or any AI coding tool, your agent starts from zero. Decisions you made yesterday? Gone. Context from last week? Gone. That architectural choice you spent 30 minutes discussing? You'll explain it again.

SAME gives your AI persistent memory from your existing markdown notes (any folder of `.md` files — no Obsidian required). No cloud. No API keys. One binary.

## See it in 60 seconds

```bash
curl -fsSL statelessagent.com/install.sh | bash
same demo
```

`same demo` creates a temporary vault with sample notes, runs semantic search, and shows your AI answering questions from your notes — all locally, no accounts, no API keys.

---

## Quickstart

```bash
# 1. Install (pick one)
curl -fsSL statelessagent.com/install.sh | bash   # direct binary
npm install -g @sgx-labs/same                      # or via npm

# 2. Point SAME at your project
cd ~/my-project && same init

# 3. Ask your notes a question
same ask "what did we decide about authentication?"

# 4. Your AI now remembers (hooks + MCP tools active)
# Start Claude Code, Cursor, or any MCP client — context surfaces automatically
```

That's it. Your AI now has memory.

---

## Human-First Philosophy

SAME is built around one constraint: your knowledge belongs to you.

- Notes are plain markdown, so your system remains useful even without AI.
- The tool is designed to help you learn by using it, not lock you into opaque automation.
- Local-first defaults keep individuals in control of data and workflow.
- The goal is a better human+AI relationship: symbiotic, practical, and durable.

This is a digital multitool for builders: solo developers, creatives, researchers,
and small teams who want capability without giving up ownership.

Design details and architecture rationale: [`docs/design_context.md`](docs/design_context.md)

---

## Add to Your AI Tool

### Claude Code (hooks + MCP — full experience)

```bash
same init          # sets up hooks + MCP in one step
```

SAME installs 6 Claude Code hooks automatically. Context surfaces on every session start. Decisions extracted on stop. No config file to edit.

### Claude Code / Cursor / Windsurf (MCP only)

Or add manually to your MCP config (`.mcp.json`, `.claude/settings.json`, Cursor MCP settings):

```json
{
  "mcpServers": {
    "same": {
      "command": "npx",
      "args": ["-y", "@sgx-labs/same", "mcp", "--vault", "/absolute/path/to/your/notes"]
    }
  }
}
```

Replace `/absolute/path/to/your/notes` with the actual path to your project or notes directory. 12 tools available instantly. Works without Ollama (keyword fallback).

---

## Why SAME

| Problem | Without SAME | With SAME |
|---------|-------------|-----------|
| New session starts | Re-explain everything | AI picks up where you left off |
| "Didn't we decide to use JWT?" | Re-debate for 10 minutes | Decision surfaces automatically |
| Switch between projects | Manually copy context | Each project has its own memory |
| Close terminal accidentally | All context lost | Next session recovers via handoff |
| Ask about your own notes | Copy-paste into chat | `same ask` with source citations |
| Context compacted mid-task | AI restarts from scratch | Pinned notes + handoffs survive compaction |

## The Numbers

| Metric | Value | What it means |
|--------|-------|---------------|
| Retrieval precision | **99.5%** | When SAME surfaces a note, it's almost always the right one |
| MRR | **0.949** | The right note surfaces first, almost every time |
| Coverage | **90.5%** | 9 out of 10 relevant notes found |
| Prompt overhead | **<200ms** | You won't notice it |
| Binary size | **~10MB** | Smaller than most npm packages |
| Setup time | **<60 seconds** | One curl command |

*Benchmarked against 105 ground-truth test cases. [Methodology](#eval-methodology)*

---

## How It Works

```
┌─────────────┐     ┌──────────┐     ┌──────────┐     ┌─────────────────┐
│  Your Notes │     │  Ollama  │     │  SQLite  │     │  Your AI Tool   │
│   (.md)     │────>│ (embed)  │────>│ (search) │────>│ Claude / Cursor │
│             │     │ local    │     │ + FTS5   │     │ via Hooks + MCP │
└─────────────┘     └──────────┘     └──────────┘     └─────────────────┘
                                          │                    │
                                     ┌────▼────┐          ┌────▼────┐
                                     │ Ranking │          │  Write  │
                                     │ Engine  │          │  Side   │
                                     └─────────┘          └─────────┘
                                     semantic +           decisions,
                                     recency +            handoffs,
                                     confidence           notes
```

Your markdown notes are embedded locally via Ollama and stored in a SQLite database with vector search. When your AI tool starts a session, SAME surfaces relevant context automatically. Decisions get extracted. Handoffs get generated. The next session picks up where you left off. Everything stays on your machine.

**No Ollama? No problem.** SAME Lite runs with zero external dependencies. Keyword search via SQLite FTS5 powers all features. Install Ollama later and `same reindex` upgrades to semantic mode instantly.

---

## Features

| Feature | Description | Requires Ollama? |
|---------|-------------|:-:|
| Semantic search | Find notes by meaning, not keywords | Yes |
| Keyword search (FTS5) | Full-text search fallback | No |
| `same ask` (RAG) | Ask questions, get cited answers from your notes | Yes (chat model) |
| Session handoffs | Auto-generated continuity notes | No |
| Session recovery | Crash-safe — next session picks up even if terminal closed | No |
| Decision extraction | Architectural choices remembered across sessions | No |
| Pinned notes | Critical context always included | No |
| File claims (`same claim`) | Advisory read/write ownership for multi-agent coordination | No |
| Knowledge graph (`same graph`) | Traverse note/file/agent/decision relationships | No* |
| Context surfacing | Relevant notes injected into AI prompts | No* |
| `same demo` | Try SAME in 60 seconds | No |
| `same tutorial` | 6 hands-on lessons | No |
| `same doctor` | 18 diagnostic checks | No |
| Push protection | Safety rails for multi-agent workflows | No |
| `same seed install` | One-command install of pre-built knowledge vaults | No* |
| Cross-vault federation | Search across all vaults at once | No* |
| MCP server (12 tools) | Works with any MCP client | No* |
| Privacy tiers | `_PRIVATE/` never indexed, `research/` never committed | No |

*Semantic mode requires Ollama; keyword fallback is automatic.

---

## Knowledge Graph

Explore relationships in your vault with the `graph` command group:

```bash
same graph stats
same graph query --type note --node "notes/architecture.md" --depth 2
same graph path --from-type note --from "notes/architecture.md" --to-type file --to "internal/store/db.go"
same graph rebuild
```

Graph data is built from indexed notes and stays local in SQLite. `_PRIVATE/` files remain excluded because they are never indexed.

---

## Seed Vaults

Pre-built knowledge vaults that give your AI expert-level context in one command.

```bash
same seed install claude-code-power-user
```

| Seed | Notes | What you get |
|------|:-----:|-------------|
| `claude-code-power-user` | 52 | Master-level Claude Code patterns, workflows, and tricks |
| `ai-agent-architecture` | 58 | Agent design patterns, orchestration, memory strategies |
| `personal-productivity-os` | 118 | GTD, time blocking, habit systems, review frameworks |

10 seeds available — 622+ notes of expert knowledge. Browse with `same seed list`.

[Browse all seeds](https://github.com/sgx-labs/seed-vaults)

---

## MCP Tools

SAME exposes **12 tools** via MCP for any compatible client.

### Read

| Tool | What it does |
|------|-------------|
| `search_notes` | Semantic search across your knowledge base |
| `search_notes_filtered` | Search with domain/workstream/tag/agent filters |
| `search_across_vaults` | Federated search across multiple vaults |
| `get_note` | Read full note content by path |
| `find_similar_notes` | Discover related notes by similarity |
| `get_session_context` | Pinned notes + latest handoff + recent activity + git state + active claims |
| `recent_activity` | Recently modified notes |
| `reindex` | Re-scan and re-index the vault |
| `index_stats` | Index health and statistics |

### Write

| Tool | What it does |
|------|-------------|
| `save_note` | Create or update a markdown note (auto-indexed, optional `agent` attribution) |
| `save_decision` | Log a structured project decision (optional `agent` attribution) |
| `create_handoff` | Write a session handoff for the next session (optional `agent` attribution) |

Your AI can now write to its own memory, not just read from it. Decisions persist. Handoffs survive. Every session builds on the last.

---

## Works With

| Tool | Integration | Experience |
|------|-------------|------------|
| **Claude Code** | Hooks + MCP | Full (automatic context surfacing + 12 tools) |
| **Cursor** | MCP | 12 tools for search, write, session management |
| **Windsurf** | MCP | 12 tools for search, write, session management |
| **Obsidian** | Vault detection | Indexes your existing vault |
| **Logseq** | Vault detection | Indexes your existing vault |
| **Any MCP client** | MCP server | 12 tools via stdio transport |

SAME works with any directory of `.md` files. No Obsidian required.

Use `same init --mcp-only` to skip Claude Code hooks and just register the MCP server.

---

## SAME vs. Alternatives

| | SAME | mem0 | Letta | Basic Memory | doobidoo |
|---|:---:|:---:|:---:|:---:|:---:|
| **Setup** | 1 command | pip + config | Docker + PG | pip + config | pip + ChromaDB |
| **Runtime deps** | None | Python + LLM API | Docker + PG + LLM | Python | Python + ChromaDB |
| **Offline capable** | Full (Lite mode) | No | No | Partial | Yes |
| **Cloud required** | No | Default yes | Yes | No | No |
| **Telemetry** | None | Default ON | Unknown | None | None |
| **MCP tools** | 12 | 4-6 | 0 (REST) | 7+ | 24 |
| **Hook integration** | Yes (Claude Code) | No | No | No | No |
| **Session continuity** | Handoffs + pins + recovery | Session-scoped | Core feature | No | No |
| **Published benchmarks** | P=0.995, MRR=0.949 | Claims "26% better" | None | None | None |
| **Binary size** | ~10MB | ~100MB+ (Python) | ~500MB+ (Docker) | ~50MB+ | ~80MB+ |
| **Language** | Go | Python | Python | Python | Python |
| **License** | BSL 1.1 [1] | Apache 2.0 | Apache 2.0 | MIT | MIT |

[1] BSL 1.1: Free for personal, educational, hobby, research, and evaluation use. Converts to Apache 2.0 on 2030-02-02.

---

## Privacy by Design

SAME creates a three-tier privacy structure:

| Directory | Indexed? | Committed? | Use for |
|-----------|:--------:|:----------:|---------|
| Your notes | Yes | Your choice | Docs, decisions, research |
| `_PRIVATE/` | No | No | API keys, credentials, secrets |
| `research/` | Yes | No | Strategy, analysis — searchable but local-only |

Privacy is structural — filesystem-level, not policy-based. `same init` creates a `.gitignore` that enforces these boundaries automatically.

**Security hardening:** Path traversal blocked across all tools. Dot-directory writes blocked. Symlink escapes prevented. Error messages sanitized — no internal paths leak to AI. Config files written with owner-only permissions (0o600). Ollama URL validated to localhost-only. Prompt injection patterns scanned before context injection. Push protection available for multi-agent workflows.

---

## Install

```bash
# macOS / Linux
curl -fsSL statelessagent.com/install.sh | bash

# Or via npm (any platform — downloads prebuilt binary)
npm install -g @sgx-labs/same

# Windows (PowerShell)
irm statelessagent.com/install.ps1 | iex
```

If blocked by execution policy, run first: `Set-ExecutionPolicy RemoteSigned -Scope CurrentUser`

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

Requires Go 1.25+ and CGO.

</details>

---

<details>
<summary><strong>CLI Reference</strong></summary>

| Command | Description |
|---------|-------------|
| `same init` | Set up SAME for your project (start here) |
| `same demo` | See SAME in action with sample notes |
| `same tutorial` | Learn SAME features hands-on (6 lessons) |
| `same ask <question>` | Ask a question, get cited answers from your notes |
| `same search <query>` | Search your notes |
| `same search --all <query>` | Search across all registered vaults |
| `same related <path>` | Find related notes |
| `same status` | See what SAME is tracking |
| `same doctor` | Run 18 diagnostic checks |
| `same claim <path> --agent <name>` | Create an advisory write claim for a file |
| `same claim --read <path> --agent <name>` | Declare a read dependency on a file |
| `same claim --list` | Show active read/write claims |
| `same claim --release <path> [--agent <name>]` | Release claims for a file |
| `same pin <path>` | Always include a note in every session |
| `same pin list` | Show pinned notes |
| `same pin remove <path>` | Unpin a note |
| `same feedback <path> up\|down` | Rate note helpfulness |
| `same repair` | Back up and rebuild the database |
| `same reindex [--force]` | Rebuild the search index |
| `same display full\|compact\|quiet` | Control output verbosity |
| `same profile use precise\|balanced\|broad` | Adjust precision vs. coverage |
| `same model` | Show current embedding model and alternatives |
| `same model use <name>` | Switch embedding model |
| `same config show` | Show configuration |
| `same config edit` | Open config in editor |
| `same setup hooks` | Install Claude Code hooks |
| `same setup mcp` | Register MCP server |
| `same hooks` | Show hook status and descriptions |
| `same seed list` | Browse available seed vaults |
| `same seed install <name>` | Download and install a seed vault |
| `same seed info <name>` | Show seed details |
| `same seed remove <name>` | Uninstall a seed vault |
| `same vault list\|add\|remove\|default` | Manage multiple vaults |
| `same vault rename <old> <new>` | Rename a vault alias |
| `same vault feed <source>` | Propagate notes from another vault (with PII guard) |
| `same guard settings set push-protect on` | Enable push protection |
| `same push-allow` | One-time push authorization |
| `same watch` | Auto-reindex on file changes |
| `same budget` | Context utilization report |
| `same log` | Recent SAME activity |
| `same stats` | Index statistics |
| `same update` | Update to latest version |
| `same version [--check]` | Version and update check |

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
provider = "ollama"           # "ollama" (default), "openai", or "openai-compatible"
model = "nomic-embed-text"    # see supported models below
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

Supported embedding models (auto-detected dimensions):

| Model | Dims | Notes |
|-------|------|-------|
| `nomic-embed-text` | 768 | Default. Great balance of quality and speed |
| `snowflake-arctic-embed2` | 768 | Recommended upgrade. Best retrieval in its size class |
| `mxbai-embed-large` | 1024 | Highest overall MTEB average |
| `all-minilm` | 384 | Lightweight (~90MB). Good for constrained hardware |
| `snowflake-arctic-embed` | 1024 | v1 large model |
| `embeddinggemma` | 768 | Google's Gemma-based embeddings |
| `qwen3-embedding` | 1024 | Qwen3 with 32K context |
| `nomic-embed-text-v2-moe` | 768 | MoE upgrade from nomic |
| `bge-m3` | 1024 | Multilingual (BAAI) |
| `text-embedding-3-small` | 1536 | OpenAI cloud API |

Any model not listed works too — set dimensions explicitly with `SAME_EMBED_DIMS`.

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
| `SAME_EMBED_PROVIDER` | `ollama` | Embedding provider (`ollama`, `openai`, or `openai-compatible`) |
| `SAME_EMBED_MODEL` | `nomic-embed-text` | Embedding model name |
| `SAME_EMBED_BASE_URL` | *(provider default)* | Base URL for embedding API (e.g. `http://localhost:8080` for local servers) |
| `SAME_EMBED_API_KEY` | *(none)* | API key (required for `openai`, optional for `openai-compatible`) |
| `SAME_SKIP_DIRS` | *(none)* | Extra dirs to skip (comma-separated) |
| `SAME_NOISE_PATHS` | *(none)* | Paths filtered from context surfacing (comma-separated) |

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

Start with `same doctor` — it runs 18 checks and tells you exactly what's wrong.

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
- Run `same doctor` to diagnose all 18 checks
- Run `same reindex` if your notes have changed
- Try `same search "your query"` to test search directly
- Check if display mode is set to "quiet": `same config show`

**"Cannot open SAME database"**
The SQLite database is missing or corrupted. Fix:
- Run `same repair` to back up and rebuild automatically
- Or run `same init` to set up from scratch
- Or run `same reindex --force` to rebuild the index

</details>

<details>
<summary><strong>Eval Methodology</strong></summary>

SAME's retrieval is tuned against 105 ground-truth test cases — real queries paired with known-relevant notes.

| Metric | Value | Meaning |
|--------|-------|---------|
| Precision | 99.5% | Surfaced notes are almost always relevant |
| Coverage | 90.5% | Finds ~9/10 relevant notes |
| MRR | 0.949 | Most relevant note is usually first |
| BAD cases | 0 | Zero irrelevant top results |

Tuning constants: `maxDistance=16.3`, `minComposite=0.70`, `gapCap=0.65`. Shared between hooks and MCP via `ranking.go`.

All evaluation uses synthetic vault data with known relevance judgments. No user data is used.

</details>

---

## FAQ

**Do I need Obsidian?** No. Any directory of `.md` files works.

**Do I need Ollama?** Recommended, not required. Semantic search understands meaning; without Ollama, SAME falls back to keyword search (FTS5). You can also use OpenAI embeddings (`SAME_EMBED_PROVIDER=openai`) or any OpenAI-compatible server like llama.cpp, VLLM, or LM Studio (`SAME_EMBED_PROVIDER=openai-compatible`). If your embedding server goes down temporarily, SAME falls back to keywords automatically.

**Does it slow down my prompts?** 50-200ms. Embedding is the bottleneck — search and scoring take <5ms.

**Is my data sent anywhere?** SAME is fully local. Context surfaced to your AI tool is sent to that tool's API as part of your conversation, same as pasting it manually.

**How much disk space?** 5-15MB for a few hundred notes.

**What are seeds?** Pre-built knowledge vaults. Install one and your AI has expert-level context immediately. `same seed list` to browse, `same seed install <name>` to install. All local, all free.

**Can I use multiple vaults?** Yes. `same vault add work ~/work-notes && same vault default work`. Search across all of them with `same search --all "your query"` or via the `search_across_vaults` MCP tool.

---

## Community

[Discord](https://discord.gg/9KfTkcGs7g) · [GitHub Discussions](https://github.com/sgx-labs/statelessagent/discussions) · [Report a Bug](https://github.com/sgx-labs/statelessagent/issues/new?template=bug_report.md)

## Support

[Buy me a coffee](https://buymeacoffee.com/sgxlabs) · [GitHub Sponsors](https://github.com/sponsors/sgx-labs)

## Built with

Go · SQLite + sqlite-vec · Ollama / OpenAI

## License

Source available under BSL 1.1. Free for personal, educational, hobby, research, and evaluation use. Converts to Apache 2.0 on 2030-02-02. See [LICENSE](LICENSE).

---

<a href="https://glama.ai/mcp/servers/@sgx-labs/statelessagent">
  <img width="380" height="200" src="https://glama.ai/mcp/servers/@sgx-labs/statelessagent/badge" />
</a>
