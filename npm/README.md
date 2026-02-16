# SAME — Stateless Agent Memory Engine

Persistent memory for AI coding agents. Local-first, private, no cloud.

Your AI agent forgets everything between sessions. SAME fixes that. It indexes your project notes locally, captures decisions as you work, and surfaces relevant context at session start. A 6-gate relevance chain decides when to inject context and when to stay quiet — about 80% of prompts get no injection at all.

## What it does

- **Semantic search** over your markdown notes via local Ollama embeddings (falls back to keyword search without Ollama)
- **Federated search** across multiple registered vaults
- **Session handoffs** — your agent writes what it did, the next session picks up where it left off
- **Decision log** — decisions are saved and surfaced automatically in future sessions
- **`same ask`** — RAG chat over your vault with source citations
- **`same demo`** — try it in 60 seconds, creates a sandbox, cleans up after

## 12 MCP Tools

| Tool | Type | Description |
|------|------|-------------|
| `search_notes` | read | Semantic + keyword search across your vault |
| `search_notes_filtered` | read | Search with domain, workstream, and tag filters |
| `search_across_vaults` | read | Federated search across multiple vaults |
| `find_similar_notes` | read | Find notes related to a given note |
| `get_note` | read | Read full note content |
| `get_session_context` | read | Pinned notes, latest handoff, recent decisions |
| `recent_activity` | read | Recently modified notes |
| `index_stats` | read | Vault health and index statistics |
| `reindex` | read | Re-scan and re-index notes |
| `save_note` | write | Create or update a note in the vault |
| `save_decision` | write | Log a project decision |
| `create_handoff` | write | Create a session handoff note |

## MCP Configuration

Add to your MCP client config (Claude Code, Cursor, Windsurf, Claude Desktop):

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

## Install

```bash
# Via npm/npx (downloads the Go binary automatically)
npx -y @sgx-labs/same version

# Or via shell script
curl -fsSL https://statelessagent.com/install.sh | bash
```

## CLI Usage

```bash
# Initialize SAME in your project
same init

# Search your notes
same search "authentication approach"

# Ask a question with cited answers
same ask "what did we decide about the database schema?"

# Try the interactive demo
same demo
```

## Privacy

- ~10MB Go binary. SQLite + Ollama on localhost.
- Zero outbound network calls. No telemetry. No analytics. No accounts. No API keys.
- Your notes never leave your machine.

## Platform Support

| Platform | Architecture | Status |
|----------|-------------|--------|
| macOS | Apple Silicon (arm64) | Supported |
| macOS | Intel (x64) | Via Rosetta |
| Linux | x64 | Supported |
| Windows | x64 | Supported |

## Links

- [Website](https://statelessagent.com)
- [Documentation](https://statelessagent.com/docs)
- [GitHub](https://github.com/sgx-labs/statelessagent)
- [Discord](https://discord.gg/9KfTkcGs7g)
- [Report a Bug](https://github.com/sgx-labs/statelessagent/issues/new?template=bug_report.md)

## License

BSL 1.1 — converts to Apache 2.0 on 2030-02-02.
