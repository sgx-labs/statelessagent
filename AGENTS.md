# SAME — Operational Guide for Agents

> This file tells automated agents how to build, test, and work with this codebase.
> Keep it brief. Update only with confirmed operational learnings.

## Build & Test

```bash
# Required: CGO is needed for sqlite3 + sqlite-vec
export CGO_ENABLED=1

# Build
make build                  # → build/same

# Test (all packages, race detector, verbose)
make test                   # → go test -race ./... -v -count=1

# Vet
go vet ./...                # Must be clean before commit

# Coverage check
go test -cover ./...        # Per-package coverage percentages
```

## Git

```bash
# Identity (REQUIRED — never use personal identity)
git config user.name "sgx-labs"
git config user.email "dev@sgx-labs.dev"

# Commit format
git commit -m "Refactor: extract searchCmd to search_cmd.go"

# NEVER push, tag, or release. Commits only.
```

## Project Structure

```
cmd/same/
  main.go              # CLI entry point, command registration, shared helpers
  ask_cmd.go           # RAG question-answering
  bench_cmd.go         # Search performance benchmarks
  ci_cmd.go            # CI workflow generation
  claim_cmd.go         # Advisory multi-agent file claims
  config_cmd.go        # Config show/edit
  demo_cmd.go          # Interactive demo
  display_cmd.go       # Display mode switching
  doctor_cmd.go        # 19 diagnostic checks
  feedback_cmd.go      # Note relevance feedback
  graph_cmd.go         # Knowledge graph query/path/stats/rebuild
  guard_cmd.go         # Push protection
  hooks_cmd.go         # Hook status listing
  index_cmd.go         # Reindex, stats, watch, migrate
  init_cmd.go          # First-run setup
  log_cmd.go           # Activity log
  mcp_cmd.go           # MCP server launch + budget report
  model_cmd.go         # Embedding model selection
  pin_cmd.go           # Pin/unpin notes
  plugin_cmd.go        # Plugin management
  repair_cmd.go        # Database recovery
  search_cmd.go        # Search + federated search + related notes
  seed_cmd.go          # Seed vault install/remove/list/info
  status_cmd.go        # Vault status overview
  tutorial_cmd.go      # Interactive tutorial
  update_cmd.go        # Self-update
  vault_cmd.go         # Multi-vault management + feed
  web_cmd.go           # Local dashboard server (`same web`)

internal/
  hooks/               # Claude Code hook handlers (20 files)
    runner.go             # Hook execution engine
    context_surfacing.go  # UserPromptSubmit: surface relevant notes
    session_bootstrap.go  # SessionStart: orient with handoff + decisions
    session_recovery.go   # SessionStart: crash recovery cascade
    staleness_check.go    # SessionStart: flag outdated notes
    decision_extractor.go # Stop: extract decisions from transcript
    handoff_generator.go  # Stop: generate session handoff
    feedback.go           # Stop: track which notes were used
    search_strategies.go  # Search dispatch (hybrid, FTS5, keyword)
    term_extraction.go    # Query term extraction
    text_processing.go    # Snippet/text utilities + sanitization
    injection.go          # Prompt injection detection (go-promptguard)
    embed.go              # Embedding helpers for hooks
    plugins.go            # Plugin system
    instance_registry.go  # Multi-instance tracking
    session_continuity.go # Session state persistence
    conversation_mode.go  # Conversation mode detection
    topic_change.go       # Topic change detection
    graduation.go         # Feature discovery hints
    verbose_logging.go    # Debug logging
  store/               # SQLite + sqlite-vec DB layer
    db.go                 # DB open/close, schema migration, helpers
    notes.go              # Note CRUD operations
    search.go             # Vector, hybrid, FTS5, federated search
    pins.go               # Pinned notes
    claims.go             # Advisory file claim operations
    ranking.go            # Composite scoring (DO NOT CHANGE constants)
    milestones.go         # Feature discovery milestones
    sessions.go           # Session recovery data
    usage.go              # Usage tracking and pruning
  config/              # Configuration, paths, vault registry
  embedding/           # Multi-provider embedding client (Ollama, OpenAI)
    provider.go           # Provider interface + factory
    ollama.go             # Ollama embedding provider
    openai.go             # OpenAI-compatible embedding provider
  graph/               # Knowledge graph schema, extraction, and traversal
    graph.go              # Node/edge CRUD + recursive CTE traversals
    extraction.go         # Regex + optional LLM entity/relationship extraction
    migration.go          # Graph schema SQL + population helpers
    llm.go                # Ollama-backed JSON extractor for graph enrichment
  indexer/             # Vault file indexer + chunker + frontmatter parser
    indexer.go            # Main indexer, reindex, single-file index
    chunker.go            # Markdown chunking by heading
    frontmatter.go        # YAML frontmatter parser
  mcp/                 # MCP server — 12 tools (search, write, session mgmt)
    server.go             # Tool registration, handlers, helpers
    git.go                # Git context collection for session context
  memory/              # Decision/handoff extraction, budget reports
    decisions.go          # Decision extraction from transcripts
    handoff.go            # Handoff generation
    transcript.go         # Transcript parsing
    budget.go             # Token budget tracking
    confidence.go         # Confidence scoring
    staleness.go          # Staleness detection
  seed/                # Seed vault installer
    manifest.go           # Seed manifest registry + caching
    download.go           # Tarball download + extraction
    install.go            # Install/remove orchestration
    security.go           # Tarball extraction hardening
  setup/               # Init flow, hook installation
  ollama/              # Ollama HTTP client (generate)
  guard/               # PII scanner, push protection
    guard.go              # Main PII scanner
    patterns.go           # PII regex patterns
    allowlist.go          # User-defined allowlist
    blocklist.go          # Blocked pattern list
    audit.go              # Audit log
    output.go             # Formatted output
    reviewed.go           # Reviewed files tracking
    settings.go           # Guard configuration
  cli/                 # CLI formatting utilities (colors, box, header/footer)
  web/                 # Local read-only web dashboard
    server.go             # HTTP server, API handlers, security middleware
    static.go             # Embedded HTML asset
  watcher/             # File watcher for auto-reindex
```

## Known Issues

- sqlite-vec deprecation warnings on macOS are expected (SDK version mismatch)
- FTS5 is NOT available in test environment — always guard with `db.FTSAvailable()`
- Tests use 768-dimension vectors to match nomic-embed-text
- `store.OpenMemory()` for in-memory test databases
- `config.VaultOverride` to point at test vault paths

## Repo Boundaries

- `internal/seed/` and `cmd/same/seed_cmd.go` are product feature code and belong in this repo
- Seed vault content belongs in the separate repo: `https://github.com/sgx-labs/seed-vaults`
- `docs/design_context.md` is an allowed public philosophy/architecture document (must remain PII-free and launch-tactic-free)
- Never commit internal planning/research artifacts (`.research/`, `research/`, `sessions/`, local run logs, local tokens/config)

## Do NOT Change

- Constants in `internal/store/ranking.go` (maxDistance, minComposite, gapCap)
- Eval thresholds (P=0.995, Coverage=0.905, MRR=0.949)
- Any search scoring behavior
- Hook output format (agents depend on the XML structure)
