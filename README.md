<!-- mcp-name: io.github.sgx-labs/same -->
# SAME — Persistent Memory for AI Coding Agents

[![License: BSL 1.1](https://img.shields.io/badge/License-BSL_1.1-blue.svg)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.25+-00ADD8.svg)](https://go.dev)
[![Latest Release](https://img.shields.io/github/v/release/sgx-labs/statelessagent)](https://github.com/sgx-labs/statelessagent/releases)
[![GitHub Stars](https://img.shields.io/github/stars/sgx-labs/statelessagent)](https://github.com/sgx-labs/statelessagent)
[![MCP Tools](https://img.shields.io/badge/MCP_Tools-12-8A2BE2.svg)](#mcp-server)
[![Discord](https://img.shields.io/discord/1468523556076785757?color=5865F2&label=Discord&logo=discord&logoColor=white)](https://discord.gg/9KfTkcGs7g)

**Your AI forgets everything between sessions. SAME fixes that.**

SAME gives Claude Code, Cursor, Windsurf, and any MCP client persistent memory. It indexes your markdown notes, surfaces relevant context automatically, and records decisions and handoffs so your AI picks up where it left off.

One binary. Fully local. No cloud. No telemetry. Mac, Linux, Windows, Raspberry Pi.

## Install

```bash
curl -fsSL https://statelessagent.com/install.sh | bash
```

Or via npm: `npm install -g @sgx-labs/same`

## See It Work (30 seconds)

```bash
same demo
```

```
Indexing 12 sample notes...
Searching: "authentication decision"

  1. decisions/auth-strategy.md (score: 0.94)
     "We chose JWT with refresh tokens for..."

  2. notes/api-security.md (score: 0.87)
     "Auth middleware validates tokens at..."

Asking: "what did we decide about authentication?"

  Based on your notes, you decided to use JWT with refresh
  tokens (decisions/auth-strategy.md). The API middleware
  validates tokens at the gateway level (notes/api-security.md).

No accounts. No API keys. Everything runs locally.
```

## Quickstart

```bash
# 1. Point SAME at your project
cd ~/my-project && same init

# 2. Test search
same search "authentication decision"

# 3. Done. Your AI now has memory.
# Start Claude Code, Cursor, or any MCP client.
```

`same init` sets up hooks and MCP tools automatically. Your AI gets relevant context on every session start.

## Key Features

- **Your AI remembers everything** -- Decisions, handoffs, and context survive across sessions. Close your terminal, switch projects, come back tomorrow. Nothing gets lost.

- **Works with your tools** -- 12 MCP tools for Claude Code, Cursor, Windsurf, or any MCP client. Search, save decisions, create handoffs without leaving your editor.

- **Safe for teams** -- Multiple AI agents on the same codebase won't step on each other. File claims, push protection, and attribution built in.

- **Instant expertise** -- 17 pre-built knowledge vaults with 880+ expert notes. One command to install. Your AI gets domain knowledge in seconds.

- **Connected knowledge** -- See how decisions, files, and notes relate to each other. Ask "what depends on this?" and get real answers. Powered by SQLite.

## How It Works

```
Your Notes (.md)  -->  Embeddings  -->  SQLite  -->  Your AI Tool
                       (local or        (search      (Claude Code,
                        cloud)           + rank)      Cursor, etc.)
```

Your markdown notes get embedded and stored in SQLite. When your AI starts a session, SAME surfaces relevant context via hooks or MCP. Decisions get extracted. Handoffs get generated. The next session picks up where the last one stopped.

**No Ollama? No problem.** SAME runs with zero external dependencies using keyword search (SQLite FTS5). Add Ollama later for semantic search -- `same reindex` upgrades instantly.

## Why SAME

| Without SAME | With SAME |
|-------------|-----------|
| Re-explain everything each session | AI picks up where you left off |
| "Didn't we decide to use JWT?" | Decision surfaces automatically |
| Close terminal = context lost | Handoff recovers the session |
| Copy-paste notes into chat | `same ask` with source citations |
| Context compacted mid-task | Pinned notes survive compaction |

## The Numbers

| Metric | Value |
|--------|-------|
| Retrieval precision | **99.5%** (105 ground-truth test cases) |
| MRR | **0.949** (right note first, almost every time) |
| Prompt overhead | **<200ms** |
| Binary size | **~10MB** |
| Setup time | **<60 seconds** |

## Add to Your AI Tool

### Claude Code (recommended)

```bash
same init    # installs 6 hooks + MCP automatically
```

### Cursor / Windsurf / Any MCP Client

Add to your MCP config (`.mcp.json`, Cursor settings, etc.):

```json
{
  "mcpServers": {
    "same": {
      "command": "npx",
      "args": ["-y", "@sgx-labs/same", "mcp", "--vault", "/path/to/your/notes"]
    }
  }
}
```

12 MCP tools available instantly. Works without Ollama (keyword fallback).

## MCP Server

| Tool | What it does |
|------|-------------|
| `search_notes` | Semantic search across your knowledge base |
| `search_notes_filtered` | Search with domain/tag/agent filters |
| `search_across_vaults` | Federated search across multiple vaults |
| `get_note` | Read full note content by path |
| `find_similar_notes` | Discover related notes |
| `get_session_context` | Pinned notes + latest handoff + git state |
| `recent_activity` | Recently modified notes |
| `save_note` | Create or update a note |
| `save_decision` | Log a structured project decision |
| `create_handoff` | Write a session handoff |
| `reindex` | Re-scan and re-index the vault |
| `index_stats` | Index health and statistics |

## SeedVaults

Pre-built knowledge vaults. One command to install.

```bash
same seed list                              # browse available seeds
same seed install claude-code-power-user    # install one
```

| Seed | Notes | What you get |
|------|:-----:|-------------|
| `same-getting-started` | 18 | Learn SAME itself — the universal on-ramp |
| `claude-code-power-user` | 50 | Claude Code workflows and operational patterns |
| `ai-agent-architecture` | 56 | Agent design, orchestration, memory strategies |
| `api-design-patterns` | 56 | REST, GraphQL, auth, rate limiting, and more |
| `typescript-fullstack-patterns` | 55 | Full-stack TypeScript patterns and best practices |
| `engineering-management-playbook` | 59 | Engineering leadership and team management |
| `personal-productivity-os` | 117 | GTD, time blocking, habit systems |
| `security-audit-framework` | 61 | Security review checklists and frameworks |

Plus 9 more. [Browse all 17 seeds on GitHub.](https://github.com/sgx-labs/seed-vaults)

## Privacy

All data stays on your machine. SAME creates a three-tier privacy structure:

| Directory | Indexed | Committed | Use for |
|-----------|:-------:|:---------:|---------|
| Your notes | Yes | Your choice | Docs, decisions, research |
| `_PRIVATE/` | No | No | API keys, credentials |
| `research/` | Yes | No | Strategy, analysis |

No telemetry. No cloud. Path traversal blocked. Config files written with owner-only permissions.

## More

<details>
<summary><strong>Full CLI Reference</strong></summary>

| Command | Description |
|---------|-------------|
| `same init` | Set up SAME for your project |
| `same demo` | See SAME in action with sample notes |
| `same tutorial` | 7 hands-on lessons |
| `same ask <question>` | Ask a question, get cited answers |
| `same search <query>` | Search your notes |
| `same search --all <query>` | Search across all vaults |
| `same status` | See what SAME is tracking |
| `same doctor` | Run 19 diagnostic checks |
| `same claim <path> --agent <name>` | Advisory file ownership for multi-agent |
| `same pin <path>` | Always include a note in sessions |
| `same graph stats` | Knowledge graph diagnostics |
| `same web` | Local web dashboard |
| `same seed list` | Browse available seed vaults |
| `same seed install <name>` | Install a seed vault |
| `same vault list\|add\|remove\|default` | Manage multiple vaults |
| `same guard settings set push-protect on` | Enable push protection |
| `same reindex [--force]` | Rebuild search index |
| `same repair` | Back up and rebuild database |
| `same update` | Update to latest version |
| `same completion [bash\|zsh\|fish]` | Shell completions |

</details>

<details>
<summary><strong>Configuration</strong></summary>

SAME uses `.same/config.toml`, generated by `same init`:

```toml
[vault]
path = "/home/user/notes"
handoff_dir = "sessions"
decision_log = "decisions.md"

[embedding]
provider = "ollama"           # "ollama", "openai", "openai-compatible", or "none"
model = "nomic-embed-text"

[memory]
max_token_budget = 800
max_results = 2
```

Supported embedding models: `nomic-embed-text` (default), `snowflake-arctic-embed2`, `mxbai-embed-large`, `all-minilm`, `text-embedding-3-small` (OpenAI), and more.

Configuration priority (highest wins): CLI flags > Environment variables > Config file > Defaults

</details>

<details>
<summary><strong>Install Options</strong></summary>

```bash
# macOS / Linux
curl -fsSL https://statelessagent.com/install.sh | bash

# npm (all platforms)
npm install -g @sgx-labs/same

# Windows PowerShell
irm https://statelessagent.com/install.ps1 | iex

# Docker
git clone --depth 1 https://github.com/sgx-labs/statelessagent.git
cd statelessagent && docker build -t same .

# Build from source (requires Go 1.25+)
git clone --depth 1 https://github.com/sgx-labs/statelessagent.git
cd statelessagent && make install
```

</details>

<details>
<summary><strong>Troubleshooting</strong></summary>

Start with `same doctor` -- it runs 19 checks and tells you what's wrong.

**"No vault found"** -- Run `same init` from inside your notes folder, or set `VAULT_PATH=/path/to/notes`.

**"Ollama not responding"** -- SAME falls back to keyword search automatically. Test with `curl http://localhost:11434/api/tags`.

**Hooks not firing** -- Run `same setup hooks` to reinstall. Verify with `same status`.

**Database issues** -- Run `same repair` to back up and rebuild.

</details>

<details>
<summary><strong>SAME vs. Alternatives</strong></summary>

| | SAME | mem0 | Letta | CLAUDE.md |
|---|:---:|:---:|:---:|:---:|
| Setup | 1 command | pip + config | pip or Docker | Edit file |
| Runtime deps | None | Python + vector DB | Python + SQLAlchemy | None |
| Offline | Full | Not default | With local models | Yes |
| Cloud required | No | Default yes | No | No |
| Telemetry | None | Default ON | Yes | None |
| MCP tools | 12 | 9 | Client only | No |
| Knowledge graph | Built-in | Requires Neo4j | No | No |
| Runs on Pi | Yes (~10MB) | No | No | Yes |

</details>

<details>
<summary><strong>Eval Methodology</strong></summary>

Retrieval benchmarked against 105 ground-truth test cases with known-relevant notes.

| Metric | Value |
|--------|-------|
| Precision | 99.5% |
| Coverage | 90.5% |
| MRR | 0.949 |
| BAD cases | 0 |

All evaluation uses synthetic vault data. No user data used.

</details>

## Links

- [Website](https://statelessagent.com)
- [Telegram Bot Plugin](https://github.com/sgx-labs/same-telegram)
- [SeedVaults](https://github.com/sgx-labs/seed-vaults)
- [Discord](https://discord.gg/9KfTkcGs7g)
- [Changelog](CHANGELOG.md)

## Contributing

Contributions welcome. [Open an issue](https://github.com/sgx-labs/statelessagent/issues) or start a [discussion](https://github.com/sgx-labs/statelessagent/discussions).

```bash
git clone https://github.com/sgx-labs/statelessagent.git
cd statelessagent
make build && make test
```

See [SECURITY.md](SECURITY.md) for security-related reports.

## Support

[Buy me a coffee](https://buymeacoffee.com/sgxlabs) | [GitHub Sponsors](https://github.com/sponsors/sgx-labs)

## License

BSL 1.1. Free for personal, educational, hobby, research, and evaluation use. Converts to Apache 2.0 on 2030-02-02. See [LICENSE](LICENSE).

---

<a href="https://glama.ai/mcp/servers/@sgx-labs/statelessagent">
  <img width="380" height="200" src="https://glama.ai/mcp/servers/@sgx-labs/statelessagent/badge" />
</a>
