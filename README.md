# The Stateless Agent

**A reference architecture for giving AI agents persistent memory using Obsidian + Claude Code.**

LLM agents are stateless. Every session starts from zero — no memory of prior decisions, no awareness of work in progress, no continuity across devices. Larger context windows don't solve this. Platform memory systems lock you in and give you no visibility into what's stored.

This project takes a different approach: **externalize agent memory into a structured, locally-hosted knowledge base that the agent reads from and writes to across sessions.** Your data stays on your device. You control what persists. Everything is human-readable markdown.

> The system works not because it is sophisticated, but because it is simple enough to sustain.

---

## The Problem

Modern AI coding agents (Claude Code, GitHub Copilot, Cursor) discard their working memory at the end of every session. This creates compounding costs:

- **Context recovery**: 10-15 minutes per session re-establishing project state and prior decisions
- **Decision amnesia**: Architectural decisions relitigated because no one remembers the rationale
- **Knowledge fragmentation**: Insights trapped in session transcripts that are never revisited
- **Cross-device discontinuity**: Each machine's agent operates in isolation

The industry response has been expanding context windows and building cloud memory. Both have limits — cost scales linearly with context size, the "lost in the middle" effect degrades retrieval in long contexts, and cloud memory is opaque, non-portable, and provider-locked.

## The Architecture

The system embeds an LLM agent within a structured Obsidian vault that serves as persistent, searchable, human-curated long-term memory. Five layers separate concerns:

```
┌─────────────────────────────────────────────────┐
│  Layer 1: Bootstrap (CLAUDE.md)                  │
│  Governance rules, ownership model, session      │
│  discipline — the agent's operating contract     │
├─────────────────────────────────────────────────┤
│  Layer 2: Lifecycle Hooks                        │
│  Session start automation, skill auto-discovery, │
│  context injection at prompt time                │
├─────────────────────────────────────────────────┤
│  Layer 3: Behavioral Tuning (Operator Profile)   │
│  Your cognitive style, friction patterns,        │
│  communication preferences, challenge mandate    │
├─────────────────────────────────────────────────┤
│  Layer 4: Workflows (Slash Commands)             │
│  Repeatable multi-step processes with tool       │
│  restrictions and safety guardrails              │
├─────────────────────────────────────────────────┤
│  Layer 5: Domain Knowledge (Skills)              │
│  Auto-loaded reference material via progressive  │
│  disclosure — only loads when relevant            │
└─────────────────────────────────────────────────┘
         │                          │
         ▼                          ▼
┌─────────────────┐  ┌──────────────────────────┐
│  Vault Storage   │  │  Semantic Search Layer    │
│  (Obsidian)      │  │  (Ollama + LanceDB + MCP)│
│  Markdown files  │  │  Local embeddings         │
│  YAML frontmatter│  │  Vector similarity        │
│  Folder taxonomy │  │  Filtered retrieval       │
└─────────────────┘  └──────────────────────────┘
```

### Cross-Session Continuity

The memory model rests on three pillars:

1. **Session handoffs** — At session end, the agent writes a structured note (what was done, decisions made, current state, next steps). The next session reads it. Estimated savings: 10-15 minutes of context recovery per session.

2. **Decision log** — Every non-trivial architectural decision is logged with rationale and rejected alternatives. Prevents the most expensive form of context loss: relitigating resolved decisions.

3. **Tiered staleness management** — Not all knowledge has equal shelf life. Architecture decisions persist forever. Project context decays over 30 days. Session handoffs archive after 30 days. Human-curated, not automated.

### The 60% Threshold

The system is designed for 60% adoption. Below 40%, the overhead isn't worth it. Above 80%, maintenance exceeds benefit. 100% is a myth. The parts that survive daily contact with reality are the parts worth keeping. Three new habits (handoffs, decisions, staleness review) is the maximum to adopt at once.

---

## What's Included

| Category | Details |
|----------|---------|
| **Vault structure** | Modified PARA methodology with numbered folders, hub notes, execution dashboards |
| **20 slash commands** | Daily review, weekly synthesis, inbox processing, research, ingestion, and more |
| **6 auto-loading skills** | Obsidian markdown, JSON Canvas, Obsidian Bases, git worktrees, debugging, skill creation |
| **Semantic search** | Local-only vector search — Ollama embeddings, LanceDB storage, MCP server interface |
| **Vision pipeline** | Image OCR, document summarization, video analysis via Gemini MCP |
| **Web capture** | Single URL and batch scraping via Firecrawl API |
| **Session memory** | Handoff protocol, decision logs, tiered staleness management |
| **Security architecture** | Dual ownership model, 3-section `.gitignore`, `_PRIVATE/` containment boundary |
| **Cross-machine sync** | Multi-device handoff protocol via Obsidian Sync + Git coexistence |
| **Operator profile** | Teach your agent your cognitive style, friction patterns, and when to push back |
| **Agent roles** | Thinking Partner, Vault Architect, Research Librarian, Ops Agent, and more |
| **Upgrade system** | AI-powered semantic merge that preserves your customizations during updates |

### Key Design Decisions

- **Dual ownership**: Git owns code and config. Obsidian Sync owns notes. They never overlap. This eliminates sync conflicts.
- **Local-first search**: Embeddings generated on-device, stored locally, queried locally. No data leaves your machine.
- **Human-curated memory**: The human, not the agent, manages what persists between sessions. Slower than automation, but produces visible, auditable memory states.
- **Progressive disclosure**: Skills load only when context matches. Dozens of skills without inflating every session's token cost.

---

## Quick Start

```bash
git clone https://github.com/sgx-labs/statelessagent.git
cd statelessagent
node install.mjs
```

The interactive installer checks prerequisites, sets up dependencies, and walks you through optional features (semantic search, Gemini Vision, Firecrawl).

After setup:

1. Open the folder in Obsidian
2. Run `claude` in this directory
3. Then `/init-bootstrap` to personalize your vault

### Manual Setup

If you prefer to skip the installer:

1. **Clone and install:**
   ```bash
   git clone https://github.com/sgx-labs/statelessagent.git
   cd statelessagent
   pnpm install
   ```

2. **Copy vault structure templates:**
   ```bash
   cp -r templates/vault-structure/* .
   ```

3. **Configure CLAUDE.md** — edit the template with your machine paths and vault details.

4. **Set up MCP servers:**
   ```bash
   cp .mcp.json.example .mcp.json
   # Edit .mcp.json with your paths and API keys
   ```

5. **Set up semantic search:**
   ```bash
   # macOS
   brew install ollama && .scripts/vault-search/setup-mac.sh

   # Linux
   curl -fsSL https://ollama.com/install.sh | sh && .scripts/vault-search/setup-linux.sh

   # Windows (PowerShell) — install Ollama from ollama.com/download first
   .\.scripts\vault-search\setup-windows.ps1
   ```

6. **Open in Obsidian** and start using Claude Code.

## Requirements

- [Claude Code](https://claude.ai/code) (CLI)
- [Obsidian](https://obsidian.md/) (knowledge base)
- [Node.js](https://nodejs.org/) v18+ and pnpm
- [Ollama](https://ollama.com/) (for local semantic search)
- Python 3.12+ (for vault-search MCP server)

### Optional

- [Obsidian Sync](https://obsidian.md/sync) — cross-device note sync
- [Gemini API key](https://aistudio.google.com/apikey) — vision and document analysis
- [Firecrawl API key](https://firecrawl.dev) — web capture
- [Tailscale](https://tailscale.com/) — mobile SSH access

---

## Project Structure

```
statelessagent/
├── .claude/                    # Agent configuration
│   ├── commands/               # 20 slash commands
│   ├── skills/                 # 6 auto-loading skills
│   ├── hooks/                  # Lifecycle automation
│   ├── mcp-servers/            # Gemini Vision MCP server
│   └── settings.json           # Hooks configuration
├── .scripts/                   # Helper scripts
│   ├── vault-search/           # Semantic search MCP server (Python)
│   ├── firecrawl-scrape.sh     # Single URL web capture
│   ├── firecrawl-batch.sh      # Batch web capture
│   └── vault-stats.sh          # Vault statistics
├── docs/                       # Architecture and methodology
│   ├── whitepaper.md           # Technical whitepaper (peer-review ready)
│   ├── guide.md                # Full methodology guide (1900+ lines)
│   ├── research.md             # AI memory systems survey
│   └── systems-thinking-lens.md
├── templates/                  # Vault structure templates
│   └── vault-structure/        # Full PARA folder structure
├── install.mjs                 # Interactive TUI installer
├── CLAUDE.md                   # Vault governance template
├── .mcp.json.example           # MCP config template
└── package.json                # Scripts and dependencies
```

---

## Documentation

| Document | Description |
|----------|-------------|
| **[Technical Whitepaper](docs/whitepaper.md)** | The stateless agent problem, reference architecture, tiered memory model, cross-session continuity analysis, comparison with platform and open-source memory systems. Peer-review ready. |
| **[Methodology Guide](docs/guide.md)** | Complete guide to building and operating the system — vault structure, 5-layer config stack, commands, skills, hooks, semantic search, security, cross-machine coordination, daily workflows. |
| **[AI Memory Research](docs/research.md)** | Survey of AI memory systems: platform memory (ChatGPT, Gemini, Claude), open-source frameworks (Mem0, Letta/MemGPT, LangMem), MCP ecosystem, session continuity patterns. |
| **[Systems Thinking Lens](docs/systems-thinking-lens.md)** | Analytical frameworks for maturing from builder to systems steward. |

---

## Commands

| Command | Purpose |
|---------|---------|
| `/init-bootstrap` | Interactive vault setup wizard |
| `/thinking-partner` | Collaborative exploration — asks questions, challenges assumptions |
| `/daily-review` | End-of-day reflection and next-day planning |
| `/weekly-synthesis` | Pattern recognition across the week |
| `/research-assistant` | Deep research with web search and synthesis |
| `/inbox-processor` | Triage and route inbox items to workstreams |
| `/ingest` | Process media files through Gemini Vision analysis |
| `/create-command` | Meta-command: build new slash commands |
| `/upgrade` | Semantic-aware system updates that preserve customizations |
| `/install-command` | Add a shell alias to launch from anywhere |
| `/release` | Version bump, changelog, tag, push |
| `/log-feedback` | Record behavioral feedback to the operator profile |
| `/de-ai-ify` | Strip AI jargon from content |
| `/pragmatic-review` | YAGNI/KISS-focused code review |
| `/pull-request` | Create PRs with full context |
| `/add-frontmatter` | Add or update YAML frontmatter |
| `/download-attachment` | Download and organize files from URLs |
| `/setup-gemini` | Configure Gemini Vision MCP |
| `/setup-firecrawl` | Configure Firecrawl web capture |
| `/rollback-upgrade` | Roll back a failed upgrade |

---

## How It Compares

| Property | Platform Memory (ChatGPT, Gemini, Claude) | This System |
|----------|------------------------------------------|-------------|
| **Setup cost** | Zero | Moderate |
| **Maintenance** | Automatic | Human-curated |
| **Transparency** | Low — opaque extraction and storage | Full — all memory is human-readable markdown |
| **Portability** | Locked to provider | Knowledge layer is provider-independent |
| **Privacy** | Data sent to provider infrastructure | Fully local (search, embeddings, storage) |
| **Staleness visibility** | Low — no provenance on stored facts | High — dates, review-by fields, frontmatter |
| **Cross-device** | Automatic (tied to account) | Requires sync infrastructure |
| **User control** | Delete/edit stored facts | Full read/write/search/archive |

The tradeoff is explicit: platform memory requires zero effort but gives you low control. This system requires consistent human habits but gives you full control and transparency. See the [whitepaper](docs/whitepaper.md) for detailed analysis.

---

## Limitations

This section exists because honest systems documentation should include what doesn't work.

- **No spontaneous recall.** The agent cannot surface information from prior sessions unless it is searched for or explicitly loaded.
- **No proactive surfacing.** The agent cannot say "this reminds me of something from last week" without a search bringing it up.
- **No voice consistency.** Different sessions may give slightly different advice on the same topic. The decision log mitigates but does not eliminate this.
- **Human discipline is the single point of failure.** The agent can write handoffs, but the human must prompt for them. If handoffs are skipped for a week, context degrades — but the system degrades gracefully, not catastrophically.

---

## Acknowledgments

Built on the foundation of [Statelessagent](https://github.com/heyitsnoah/statelessagent) by [Noah Brier](https://github.com/heyitsnoah). The original project pioneered the Claude Code + Obsidian integration pattern. This version has been substantially re-engineered with session memory and handoff protocols, local semantic search, Gemini Vision integration, cross-machine coordination, operator profiles, a security architecture with dual ownership, and accompanying research documentation.

Additional inspiration from [Statelessagent MCP](https://github.com/ProfSynapse/statelessagent-mcp) by ProfSynapse.

## License

[MIT](LICENSE)
