# Changelog

## v0.6.0 — Reliability, Privacy & Polish

Self-diagnosing retrieval, pinned notes, keyword fallback, vault privacy structure, RAG chat, interactive demo, and a full polish pass.

### Added

- **`same ask`** — ask questions, get answers FROM your notes with source citations. Uses a local Ollama LLM to synthesize answers from semantically relevant notes. Auto-detects the best available chat model. 100% local, no cloud APIs. Example: `same ask "what did we decide about authentication?"`
- **`same demo`** — interactive demo that creates a temporary vault with 6 realistic sample notes, indexes them, runs search, and showcases `same ask`. Works without Ollama (keyword-only mode). See SAME in action in under 60 seconds.
- **`same tutorial`** — modular learn-by-doing system with 6 lessons: semantic search, decisions, pinning, privacy tiers, RAG chat, and session handoffs. Run all lessons (`same tutorial`) or jump to any topic (`same tutorial search`, `same tutorial pin`). Creates real notes and runs real commands — you learn the CLI by using it.
- **SAME Lite (keyword-only mode)** — SAME now works without Ollama. When Ollama is unavailable, `same init` offers keyword-only mode using SQLite FTS5. All features work — search, ask, demo, tutorial — with keyword matching instead of semantic search. Install Ollama later and `same reindex` upgrades to full semantic mode. Zero dependencies beyond the binary.
- **Project-aware init** — `same init` now detects existing project documentation (README.md, docs/, ARCHITECTURE.md, CLAUDE.md, .cursorrules, ADR/) and offers to index them. Zero new notes required — your project already has context.
- **`same pin`** — pin important notes so they're always included in every session: `same pin path/to/note.md`, `same pin list`, `same pin remove path/to/note.md`. Pinned notes inject with maximum priority regardless of query.
- **`same repair`** — one-command database recovery: backs up `same.db`, force-rebuilds the index, and confirms. The go-to command when something breaks.
- **`same feedback`** — manual thumbs-up/down for notes: `same feedback "path" up` boosts retrieval confidence; `same feedback "path" down` penalizes. Supports glob-style paths.
- **Vault seed structure** — `same init` now creates a three-tier privacy directory structure: `sessions/` (handoffs), `_PRIVATE/` (never indexed, never committed), plus a `.gitignore` template enforcing privacy boundaries
- **FTS5 keyword fallback** — when Ollama is down or slow, context surfacing falls back to SQLite FTS5 full-text search instead of failing silently
- **Doctor retrieval diagnostics** — 8 new `same doctor` checks: embedding config mismatch, SQLite PRAGMA integrity, retrieval utilization rate, config file validity, hook installation, DB integrity, index freshness, log file size
- **Schema migration system** — `schema_meta` table with version-gated migrations; `GetMeta()`/`SetMeta()` for metadata storage; auto-migrates between schema versions
- **Embedding mismatch guard** — detects when embedding provider/model/dimensions change without reindexing; surfaces clear guidance
- **Hook execution timeout** — 10-second timeout prevents hung Ollama from blocking prompts; returns `<same-diagnostic>` on timeout
- **AI-facing diagnostics** — when hooks fail (DB missing, Ollama down), the AI sees `<same-diagnostic>` blocks with suggested user actions instead of silent failure
- **Ollama retry with backoff** — 3 attempts with exponential backoff (0/2/4s) for 5xx and network errors
- **Usage data pruning** — records older than 90 days pruned during reindex
- **Configurable noise filtering** — `[vault] noise_paths` in config.toml or `SAME_NOISE_PATHS` env var
- **23 new tests** — store (milestones, pins, delete, recent, access count, tags), search (keyword, content term, fuzzy title, hybrid), indexer (chunking, frontmatter parsing, vault walking)

### Fixed

- **Replaced all panics with errors** — `OllamaURL()` and `validateLocalhostOnly()` now return errors instead of crashing
- **TOML `skip_dirs` now applied** — `LoadConfig()` applies `[vault] skip_dirs` to the global `SkipDirs` map
- **Verbose log permissions** — changed from 0o644 to 0o600 (owner-only)

### Changed

- **Schema version 2** — adds FTS5 virtual table for keyword fallback; auto-migrates from v1
- **Context surfacing resilience** — embedding failures trigger keyword fallback instead of returning errors
- **CLI descriptions rewritten** — all user-facing commands use outcome language (e.g. "Scan your notes and rebuild the search index" instead of "Index vault into SQLite")
- **README overhauled** — pain-first hero section, outcome-focused features, three-tier privacy table, updated CLI reference
- **MCP tool descriptions improved** — all 6 tools rewritten with agent-oriented "when to use" guidance
- **Error messages friendlier** — "escapes vault boundary" → "outside your notes folder"; timeouts and connection failures include actionable guidance
- **Box is now default display** — `full` mode shows the cyan Unicode box automatically
- **Noise filtering off by default** — add `noise_paths` to config if you want path-based filtering

---

## v0.5.4 — Windows Installer Overhaul

Fixed critical Windows installation issues.

### Fixed

- **PowerShell 5.1 compatibility** — ANSI escape codes now work in Windows PowerShell (not just PS7)
- **TLS 1.2 enforcement** — Installer works on older Windows systems
- **PATH works immediately** — No need to restart terminal after install
- **Better Ollama detection** — Checks process and API, not just PATH
- **Windows Defender guidance** — Clear instructions when antivirus blocks the binary
- **Unblock downloaded file** — Removes "downloaded from internet" security flag

### Added

- PowerShell version display during install
- Corporate proxy detection hint in error messages
- Execution policy bypass instructions on website
- Windows added to site structured data (SEO)

---

## v0.5.3 — Push Protection & Display Fixes

Safety rails for multi-agent workflows.

### Added

- **Push protection** — Prevents accidental pushes to wrong repos when running multiple agent instances
  - `same push-allow [repo]` creates one-time push ticket
  - `same guard settings set push-protect on` enables with auto-hook install
  - `same guard settings set push-timeout N` configures ticket expiry (10-300s)
  - Works across multiple Claude instances sharing same machine
- **Visual feedback box** — Unicode box output showing surfaced notes, match terms, and token counts
- **CI setup for vibe coders** — `same ci init` creates GitHub Actions workflow
  - Auto-detects project type (Go, Node, Python)
  - `same ci explain` teaches what CI is
  - Educational output guides users through next steps

### Changed

- Context surfacing output uses the visual feedback box for `full` mode
- Guard settings now show push protection status and hook installation state

---

## v0.5.2 — Self-Update

One-command updates, no more curl.

### Added

- **`same update`** — Check for and install the latest version from GitHub releases
  - Detects platform (darwin-arm64, linux-amd64, windows-amd64)
  - Downloads correct binary
  - Replaces itself atomically
  - `--force` flag to reinstall even if on latest
- Handles dev builds gracefully (warns instead of failing)

### Changed

- Version check now suggests `same update` instead of curl command

---

## v0.5.1 — Onboarding & UX Polish

Better first-run experience and vibe-coder friendly commands.

### Added

- **Welcome notes** — 3 example notes copied to `.same/welcome/` during init, demonstrating recommended format and providing searchable onboarding content
- **Profile system** — `same profile use precise|balanced|broad` to adjust precision vs coverage tradeoffs, with token usage warnings
- **Display modes** — `same display full|compact|quiet` to control output verbosity
- **Experience level question** — Setup asks if you're new to coding or experienced, sets appropriate defaults
- **Cloud sync warning** — Detects Dropbox, iCloud, OneDrive, Google Drive and warns about database conflicts
- **Large vault time estimates** — Shows estimated indexing time for 500+ note vaults
- **Dependency checks** — Verifies Go 1.25+ and CGO with platform-specific install instructions
- **ASCII art banner** — STATELESS AGENT logo with red gradient in installer

### Changed

- Default display mode is now "full" (verbose box) instead of compact
- Installer has friendlier messaging and visual polish

## v0.5.0 — Public Launch

Landing page, branded CLI, multi-provider embeddings, and feedback loop.

### Added

- **Multi-provider embedding support** — pluggable embedding backend with Ollama (default) and OpenAI providers. Configure via `[embedding]` config section or `SAME_EMBED_PROVIDER` / `SAME_EMBED_MODEL` / `SAME_EMBED_API_KEY` env vars
- **Feedback loop** — notes surfaced during a session that the agent actually references get an access count boost, improving future retrieval confidence
- **Landing page** at statelessagent.com — dark terminal aesthetic, install-first design
- **Branded CLI output** — STATELESS AGENT ASCII art with red gradient, section headers, boxed summaries, and footer across `same init`, `same status`, and `same doctor`
- **Post-init explanation** — completion message now explains what SAME does: context surfacing, decision extraction, handoffs, feedback loop, staleness checks
- **Donations** — Buy Me a Coffee + GitHub Sponsors links in README and landing page

### Changed

- **Embedding architecture** — `embedding.Client` replaced with `embedding.Provider` interface; all call sites updated
- **README overhauled** — sell first, document second; collapsed `<details>` sections for CLI reference, configuration, and MCP; streamlined FAQ
- **install.sh** now also available at `statelessagent.com/install.sh`

## v0.4.0 — Public Release Polish

Eval-driven optimization, security hardening, CLI improvements.

### Added

- Composite scoring: semantic + recency + confidence signal blending
- Distance threshold and composite threshold tuning via config
- Eval harness for measuring retrieval quality
- Security: prompt injection pattern scanning in context snippets
- `same budget` command for context utilization stats
- Config file support (`.same/config.toml`) with `same config show/edit`

### Changed

- Default distance threshold tuned from 15.0 to 16.2 based on eval results
- Hook output formatting improvements

## v0.3.0 — Standalone Release

SAME is now a standalone Go project, decoupled from any specific vault infrastructure.

### Breaking Changes

- **Data directory moved**: `.scripts/same/data/` → `.same/data/`. Run `same reindex --force` after updating.
- **Plugins path moved**: `.scripts/same/plugins.json` → `.same/plugins.json`.
- **Go module renamed**: now `github.com/sgx-labs/statelessagent`.
- **Default handoff directory**: Now `sessions`. Override with `SAME_HANDOFF_DIR`.
- **Default decision log**: Now `decisions.md`. Override with `SAME_DECISION_LOG`.

### Added

- Multi-tool vault detection: recognizes `.same`, `.obsidian`, `.logseq`, `.foam`, `.dendron` markers
- `SAME_DATA_DIR` env var to override data directory location
- `SAME_HANDOFF_DIR` env var to override handoff directory
- `SAME_DECISION_LOG` env var to override decision log path
- `SAME_SKIP_DIRS` env var to add custom skip directories
- Security: `_PRIVATE/` exclusion from indexing and context surfacing
- Security: Ollama localhost-only validation
- Security: Prompt injection detection in context surfacing snippets
- Security: `same doctor` checks for private content leaks and Ollama binding
- MCP server name changed from `vault-search` to `same`

### Removed

- Obsidian-specific vault detection fallback
- Personal path defaults
- Node.js/Python infrastructure (package.json, vault-search Python server)
- Raycast scripts, eval harness, docs (deferred to separate repos)

## v0.2.0

- Initial Go rewrite of SAME
- Vector search with sqlite-vec
- Claude Code hooks (context surfacing, decision extraction, handoff generation, staleness check)
- MCP server with 6 tools
- Composite scoring (semantic + recency + confidence)
- Vault registry for multi-vault support
- File watcher for auto-reindex
- Budget tracking for context utilization
