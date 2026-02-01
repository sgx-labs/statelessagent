# The Stateless Agent

**Every AI session starts from zero. Not anymore.**

An opinionated starter kit that turns [Claude Code](https://docs.anthropic.com/en/docs/claude-code) + [Obsidian](https://obsidian.md) into a persistent, searchable second brain. Vault structure, semantic search, slash commands, and cross-session memory — all wired together so Claude picks up where it left off.

---

## Quick Start

```bash
git clone https://github.com/sgx-labs/statelessagent.git
cd stateless-agent
node install.mjs
```

The interactive installer checks prerequisites, sets up dependencies, and walks you through optional features (semantic search, Gemini Vision, Firecrawl).

After setup:

1. Open the folder in Obsidian
2. Run `claude` in this directory
3. Then `/init-bootstrap` to personalize your vault

---

## What's Included

### Vault Structure (PARA Method)

```
00_Inbox/          Capture everything here first
01_Projects/       Active work with deadlines
02_Areas/          Ongoing responsibilities
03_Resources/      Reference material by topic
04_Archives/       Completed / inactive items
05_Attachments/    Images, PDFs, media
06_Metadata/       Templates, reference docs
07_Journal/        Daily notes and session logs
```

### Slash Commands

| Command | Description |
|---------|-------------|
| `/init-bootstrap` | Personalize your vault (name, projects, areas, tools) |
| `/install-command` | Add a `stateless-agent` shell alias to launch from anywhere |
| `/upgrade` | Pull latest changes while preserving your customizations |
| `/thinking-partner` | Collaborative exploration and questioning |
| `/research-assistant` | Deep research with source ingestion |
| `/daily-review` | End-of-day review and planning |
| `/weekly-synthesis` | Weekly pattern recognition |
| `/inbox-processor` | Organize inbox items using PARA |

### MCP Integrations

- **Vault Search** — Semantic search over your notes via Ollama embeddings + LanceDB
- **Gemini Vision** — Analyze images, PDFs, and video with Google's Gemini API
- **Firecrawl** — Save any webpage as searchable markdown in your vault

### Agent Roles

Specialized modes Claude can adopt for different tasks:

- **Vault Architect** — Structure, migrations, templates
- **Stateless Agent** — System updates, MCP configuration
- **Thinking Partner** — Questioning, synthesis (no unsolicited outlines)
- **Research Librarian** — Web content ingestion, organization
- **Ops & Hygiene** — Maintenance, drift detection, cleanup

---

## Requirements

- **Node.js** 18+
- **pnpm** (recommended) or npm
- **Claude Code** CLI (`npm install -g @anthropic-ai/claude-code`)
- **Python 3.12+** (for semantic search)
- **Ollama** (for local embeddings)

---

## Project Structure

```
.claude/              Claude Code commands, hooks, config
  commands/           Slash command definitions
  hooks/              Session hooks (welcome, skill discovery)
  mcp-servers/        MCP server implementations
.config/              ESLint, Prettier config
.scripts/             Shell scripts (firecrawl, transcripts, vault stats)
  vault-search/       Semantic search MCP server (Python)
templates/            Vault structure templates
install.mjs           Interactive TUI installer
package.json          Dependencies and npm scripts
```

---

## How It Works

1. **Claude Code** runs in your vault directory and reads `CLAUDE.md` for context
2. **Obsidian** is the UI — you read, write, and organize notes there
3. **Obsidian Sync** handles note synchronization across devices
4. **Git** tracks only code and config (`.claude/`, `.scripts/`, `.config/`, `package.json`)
5. **MCP servers** give Claude tools like semantic search and vision

Git and Obsidian Sync coexist without overlap. Notes never enter git. Dotfiles never enter Sync.

---

## Acknowledgments

Built on the foundation of [Statelessagent](https://github.com/heyitsnoah/statelessagent) by [Noah Brier](https://github.com/heyitsnoah). The original project pioneered the Claude Code + Obsidian integration pattern — the vault structure, slash commands, upgrade system, and PARA-based workflow all trace back to Noah's work.

Additional inspiration from [Statelessagent MCP](https://github.com/ProfSynapse/statelessagent-mcp) by ProfSynapse.

---

## License

[MIT](LICENSE)
