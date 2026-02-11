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
  main.go              # CLI entry point + commands (being decomposed)
  guard_cmd.go         # Extracted: guard command (894 lines) — PATTERN TO FOLLOW
  ci_cmd.go            # Extracted: ci command (336 lines) — PATTERN TO FOLLOW

internal/
  hooks/               # Claude Code hook handlers
    context_surfacing.go  # 2150 lines (being decomposed)
    session_recovery.go   # Crash recovery cascade
    runner.go             # Hook execution engine
    plugins.go            # Plugin system
  store/               # SQLite + sqlite-vec DB layer
    db.go, notes.go, search.go, pins.go, ranking.go
  config/              # Configuration, paths, vault override
  embedding/           # Multi-provider embedding client
  indexer/             # Vault file indexer
  mcp/                 # MCP server implementation
  memory/              # Decision/handoff extraction
  setup/               # Init flow
  ollama/              # Ollama HTTP client
  guard/               # PII scanner
  cli/                 # CLI utilities
  watcher/             # File watcher
```

## Command Extraction Pattern

When extracting a command from main.go to its own file:

1. New file: `cmd/same/{name}_cmd.go`
2. Package: `package main`
3. Move: the `func xxxCmd() *cobra.Command` + helpers called ONLY by that command
4. Keep in main.go: shared helpers used by multiple commands (e.g., `newEmbedProvider`, `compareSemver`, `openStoreForCmd`)
5. Imports: only what the moved code needs

## Known Issues

- sqlite-vec deprecation warnings on macOS are expected (SDK version mismatch)
- FTS5 is NOT available in test environment — always guard with `db.FTSAvailable()`
- Tests use 768-dimension vectors to match nomic-embed-text
- `store.OpenMemory()` for in-memory test databases
- `config.VaultOverride` to point at test vault paths

## Do NOT Change

- Constants in `internal/store/ranking.go` (maxDistance, minComposite, gapCap)
- Eval thresholds (P=0.995, Coverage=0.905, MRR=0.949)
- Any search scoring behavior
- Hook output format (agents depend on the XML structure)
