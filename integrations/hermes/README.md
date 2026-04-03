# SAME Memory Provider for Hermes Agent

Native [Hermes Agent](https://github.com/NousResearch/hermes-agent) memory provider plugin powered by [SAME](https://statelessagent.com).

## What it does

- **Auto-recall**: Searches your SAME vault before every turn and injects relevant context
- **Decision logging**: `same_save_decision` tool for structured decision tracking
- **Session handoffs**: Automatically creates handoff notes when sessions end
- **Memory mirroring**: Built-in Hermes memory writes are mirrored to your SAME vault
- **Compression safety**: Extracts facts before context window compression
- **Trust state**: Search results include provenance and trust metadata

## Prerequisites

- [SAME](https://statelessagent.com) binary installed (`same` on PATH)
- A vault initialized with `same init`
- Hermes Agent v0.7.0+

## Vault Setup

Before your first index, add a `.sameignore` to your vault root. Without it,
framework docs, dependency source code, and build artifacts dominate search
results — your own project knowledge gets buried.

```bash
# Copy the Hermes Agent template into your vault
cp /path/to/statelessagent/templates/sameignore/hermes-agent.sameignore \
   $SAME_VAULT_PATH/.sameignore

# Then reindex so the ignore rules take effect
same reindex
```

The `hermes-agent.sameignore` template ignores:

```
# Hermes Agent runtime — framework internals, not your knowledge
hermes-agent/

# SAME's own data directory — never index your index
.hermes/

# Claude Code config and session data
.claude/

# Test directories — fixtures drown out real results
test/
tests/

# Node.js dependencies — tens of thousands of library files
node_modules/

# Git internals
.git/

# Python bytecode cache — not human-readable
__pycache__/
*.pyc
*.pyo

# Python virtual environments — third-party library source
venv/
.venv/
env/
```

For other stacks (Python projects, Node.js apps, monorepos) see
`templates/sameignore/` in the statelessagent repo. The `default.sameignore`
is installed automatically by `same init` and covers the most common cases.

**Why this matters:** A fresh vault indexed without `.sameignore` will surface
`node_modules/some-package/README.md` and `.hermes/session-cache` files ahead
of your actual project notes. The ignore file is the single highest-leverage
thing you can do before first use.

## Install

```bash
# Symlink into Hermes plugins directory
ln -s /path/to/statelessagent/integrations/hermes ~/.hermes/plugins/memory/same

# Or copy
cp -r /path/to/statelessagent/integrations/hermes ~/.hermes/plugins/memory/same
```

## Configure

```bash
# Via Hermes wizard
hermes memory setup

# Or manually
hermes config set memory.provider same

# Set vault path (required)
export SAME_VAULT_PATH=/path/to/your/vault
```

Or create `~/.hermes/same/config.json`:

```json
{
  "vault_path": "/path/to/your/vault",
  "binary": "same",
  "agent": "hermes"
}
```

## Tools exposed

| Tool | Description |
|------|-------------|
| `same_search` | Search knowledge vault with semantic search |
| `same_save_note` | Save a markdown note with provenance tracking |
| `same_save_decision` | Log a project decision |
| `same_get_note` | Read full note content |
| `same_health` | Check vault health and index status |

## Architecture

The plugin starts `same mcp` as a persistent subprocess and communicates via JSON-RPC 2.0 over stdio. This gives access to all SAME tools through a single process with no repeated startup cost.

If the MCP subprocess dies between turns, it auto-restarts on the next turn.

## vs MCP integration

You can also use SAME as an MCP server in Hermes (add to `config.yaml` under `mcp.servers`). The native plugin approach is better because:

- **Automatic prefetch** on every turn (MCP requires explicit tool calls)
- **Session handoffs** created automatically on exit
- **Memory mirroring** from built-in Hermes memory
- **Compression safety** — facts saved before context window compression
- **No race conditions** — plugin runs in the sequential execution path
