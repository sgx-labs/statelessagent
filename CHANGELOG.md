# Changelog

## v0.7.3 — Bootstrap & Vault UX

### Fixed

- **Session-bootstrap hook not wired** — `same init` now installs all 6 hooks including `session-bootstrap`. Previously only 5 of 6 were configured, so session orientation context was never delivered on SessionStart.
- **Wrong vault when CWD is a vault** — vault resolution now checks CWD for vault markers *before* falling back to the registry default. If you're standing in a vault directory, that vault is used regardless of what the registry default is.
- **`same init` didn't set default vault** — running `same init` in a new directory now always sets that vault as the registry default, not just on first-ever init.
- **`same status` vault display unclear** — vault section now shows the active vault prominently with its resolution source (auto-detected from cwd, registry default, --vault flag). Registered vaults list uses `→` for active and `*` for default, sorted alphabetically. JSON output now includes all 6 hooks.

---

## v0.7.0 — Cross-Vault Federation

Search across all your vaults from one place. Manage multiple vaults from the CLI. Propagate notes between vaults with privacy guards.

### Added

- **Federated search** — `same search --all` searches every registered vault in one query. `same search --vaults work,personal` searches specific vaults. Results merged by score with graceful degradation (semantic → FTS5 → keyword per vault). 50-vault limit.
- **MCP: `search_across_vaults`** — federated search tool for any MCP client. Brings the total to **12 MCP tools**.
- **`same vault` command group** — `same vault list`, `same vault add <alias> <path>`, `same vault remove <alias>`, `same vault default <alias>` for managing the vault registry (`~/.config/same/vaults.json`).
- **`same vault feed`** — one-way note propagation between vaults. Copy notes from a source vault into the current vault's `fed/<alias>/` directory. Includes PII guard (scans for email/phone/SSN patterns), symlink rejection, 10MB file size limit, self-feed prevention, and `--dry-run` mode.
- **`store.AllNotes()`** — returns all chunk_id=0 notes excluding `_PRIVATE/`, ordered by modified date.
- **20 new tests** — `sanitizeAlias` (14 cases), `safeFeedPath` (19 cases), `FederatedSearch` (empty query, too many vaults, private note exclusion, mixed vault health, graceful skip).
- **Progressive feature discovery** — CLI teaches new capabilities at the moment they become relevant. `vault add` hints about `--all` when 2+ vaults registered. `search` hints about `same related`. `reindex` hints about `same watch`. `status` shows available vaults and `same ask` when a chat model is detected. `doctor` validates vault registry health (16 checks total).
- **Pinned notes in session bootstrap** — pinned notes now survive context compaction. Previously only surfaced during per-prompt context, pinned notes are now included at session start (Priority 1, 2000-char budget) so your AI always has your most important context.

### Fixed

- **Vault registry Save() merge bug** — `same vault remove` silently failed because Save() re-read the registry from disk and merged back deleted entries. Removed merge logic so removes take effect immediately.
- **MCP works without Ollama** — MCP server now starts and serves search results even when Ollama is unavailable. Search falls back gracefully: HybridSearch → FTS5 → keyword. Previously, `npx @sgx-labs/same mcp` refused to start without Ollama, breaking MCP client setups.
- **MCP create_handoff overwrite** — multiple handoffs on the same day no longer overwrite each other; uses minute-level timestamps for uniqueness.
- **MCP registry manifests** — `server.json` updated to official MCP registry schema (`$schema`, `isRequired`, `registryBaseUrl`). `smithery.yaml` uses `npx -y` instead of bare `same`. npm `package.json` adds `mcpName` for registry auto-discovery.
- **Hook output format** — Stop and SessionStart events now use `systemMessage` for correct Claude Code rendering.
- **URL corrections** — all references to ollama.ai updated to ollama.com across install scripts, CLI, and tutorial. Discord invite links synced across all files.
- **`same status` hook display** — now shows all 6 hooks instead of 4.

### Changed

- **Brand refresh** — ASCII banner updated from 12-line "STATELESS AGENT" red gradient to compact "SAME" blue gradient with "Stateless Agent Memory Engine" subtitle. Updated across CLI, install.sh, and install.ps1.

### Codebase

- **CLI decomposed** — `cmd/same/main.go` split into 18 focused files: `search_cmd.go`, `vault_cmd.go`, `ask_cmd.go`, `status_cmd.go`, `doctor_cmd.go`, `display_cmd.go`, `demo_cmd.go`, `tutorial_cmd.go`, and more. Main.go now handles only command registration.
- **Hooks decomposed** — `context_surfacing.go` split into `search_strategies.go`, `term_extraction.go`, `text_processing.go`, `verbose_logging.go`.
- **Test coverage expanded** — new test suites for hooks runner, session recovery, session bootstrap, instance registry, indexer, memory, setup, MCP handlers, and store edge cases.
- **AGENTS.md** — contributor guide for AI coding agents.

### Security

- **MCP server hardening (15 fixes)** — query length limits (10K chars), file size guard on `get_note` (1MB), write rate limiting (30 ops/min sliding window), snippet sanitization neutralizes 12 prompt-injection tag patterns before returning results to AI clients. `save_decision` and `create_handoff` use `IndexSingleFile` instead of full reindex to prevent DoS. Decision titles sanitized. Append-mode provenance tracking. Federated search resolves aliases from registry map directly.
- **MCP tool annotations** — all 12 tools declare `readOnlyHint`, `destructiveHint`, and `idempotentHint` per the MCP 2025-06-18 spec. Helps MCP clients enforce least-privilege.
- **Defense-in-depth `_PRIVATE/` filtering** — case-insensitive `UPPER(path) NOT LIKE` added to `VectorSearch`, `VectorSearchRaw`, and `ContentTermSearch` SQL queries. Pinned notes skip `_PRIVATE/` paths. Hooks use case-insensitive `isPrivatePath()`.
- **SQL LIKE injection prevention** — `escapeLIKE()` helper escapes `%`, `_`, and `\` in user-supplied search terms across `KeywordSearch` and `ContentTermSearch`.
- **Expanded tag sanitization** — context injection now neutralizes 12 tag types (added `system-reminder`, `system`, `instructions`, `tool_result`, `tool_use`, `IMPORTANT`) to block stored prompt injection.
- **Vault feed hardening** — `sanitizeAlias()` strips path separators, traversal characters, and null bytes. `safeFeedPath()` blocks absolute paths, traversal, private/hidden directories, and null bytes. Federated search error messages use aliases only (no raw filesystem paths).
- **Self-closing tag injection** — `neutralizeTags()` (MCP) and `sanitizeContextTags()` (hooks) now neutralize self-closing tags (`<tag/>`) and tags with attributes (`<tag attr="...">`), closing prompt injection vectors.
- **Bootstrap context injection** — session bootstrap output is now sanitized before wrapping in `<session-bootstrap>` tags, preventing stored prompt injection via crafted handoff/decision content.
- **LIKE injection in title search** — `KeywordSearchTitleMatch` now uses `escapeLIKE()` to prevent SQL wildcard injection via `%` and `_` characters.

---

## v0.6.1 — Hardening, Recovery & Visibility

Security hardening, crash recovery, search improvements, UX polish, and every hook now proves it's working.

### Added

- **SessionStart crash recovery** — 3-tier priority cascade recovers context even when terminal is closed without Stop firing: handoff (full, completeness 1.0) → instance registry (partial, 0.4) → session index (minimal, 0.3). Schema v3 adds `session_recovery` table for telemetry.
- **Hook status lines** — every hook now prints a one-line receipt to stderr: session recovery source, decisions extracted, handoffs saved, stale notes flagged, referenced notes boosted. `same display quiet` silences all output.
- **HybridSearch for MCP and CLI** — `search_notes`, `search_notes_filtered`, and `same search` now use HybridSearch (semantic + keyword + fuzzy title) instead of raw VectorSearch. Better results for partial matches and typos.
- **Command groups** — `same --help` organizes commands into 5 groups: Essential, Search & Browse, Configuration, Advanced, Other
- **`same hooks`** — new command showing all 6 hooks with name, event, status, and description
- **`--json` flag** — `same status --json` and `same doctor --json` for machine-readable output
- **Star ratings** — search results show `★★★★☆ 85%` instead of raw scores. `--verbose` for raw numbers.
- **98 new tests** — security (plugin injection, path traversal, symlinks), edge cases (empty inputs, large inputs, concurrent access), store operations
- **NPM distribution** — `npx @sgx-labs/same mcp --vault /path` for MCP clients. Zero-dependency wrapper downloads prebuilt binary from GitHub Releases at install time. Release workflow auto-publishes to npm on tag push.

### Security

22 vulnerabilities fixed (3 critical, 8 high, 11 medium):

- **CRITICAL: Plugin command injection** — `validatePlugin()` with shell metachar regex, path traversal block, exec permission check
- **CRITICAL: Hard-coded vec0 768 dims** — `EmbeddingDim()` now dynamic per provider/model
- **CRITICAL: OpenAI gets Ollama URL** — conditional BaseURL, only set for Ollama provider
- **HIGH: SSRF in init** — localhost validation (127.0.0.1/::1/localhost) before HTTP
- **HIGH: Symlink escape** — `EvalSymlinks()` + ancestor walk in safeVaultPath and config
- **HIGH: Settings destruction** — return error on malformed JSON instead of silent overwrite
- **HIGH: Embedding mismatch** — use resolved model name in SetEmbeddingMeta
- **HIGH: PII via os.Hostname()** — SHA-256 hash → `machine-a1b2c3d4` format
- **FTS5 query injection** — `sanitizeFTS5Term()` strips `*`, `^`, `-`, `"` and other operators
- **Case-insensitive `_PRIVATE/`** — `UPPER(n.path) NOT LIKE` across all SQL queries
- **JSON error sanitization** — vault paths → directory name only, no raw hostnames in errors
- **File permissions** — all MCP/config writes to 0o600

### Fixed

- **Embedding pipeline** — OpenAI provider URL, retry logic, all-zero vector detection, API key leak prevention, dimension validation
- **Search quality** — absolute+relative scoring blend, per-note token cap (400), FTS5 OR instead of AND, composite indexes for common queries
- **`same scope` → `same status`** — referenced non-existent command
- **Indexer double-read** — eliminated redundant file reads during indexing
- **IncrementAccessCount** — batch update instead of per-path

### Changed

- **README rewritten** — pain-first opening, architecture diagram, feature matrix, competitor comparison table, honest benchmarks (removed aspirational token claim)
- **Schema v3** — adds `session_recovery` table; auto-migrates from v2
- **MCP tool list updated** — setup now shows all 11 tools with accurate descriptions

---

## v0.6.0 — Reliability, Privacy & Polish

Self-diagnosing retrieval, pinned notes, keyword fallback, vault privacy structure, RAG chat, interactive demo, write-side MCP tools, security hardening, and a full polish pass.

### Added

- **Write-side MCP tools** — 5 new MCP tools bring the total to 11. Your AI can now save notes, log decisions, and create session handoffs — not just read:
  - `save_note` — create or update markdown notes (auto-indexed, dot-dir protected, 100KB limit)
  - `save_decision` — log structured decisions with status and date
  - `create_handoff` — session handoffs with summary, pending items, and blockers
  - `get_session_context` — one-call orientation: pinned notes + latest handoff + recent activity + stats
  - `recent_activity` — recently modified notes (clamped to 50)
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
- **Embedding mismatch guard** — detects when embedding provider/model/dimensions change without reindexing; surfaces clear guidance; `Provider` interface gains `Model()` method
- **Hook execution timeout** — 10-second timeout prevents hung Ollama from blocking prompts; returns `<same-diagnostic>` on timeout
- **AI-facing diagnostics** — when hooks fail (DB missing, Ollama down), the AI sees `<same-diagnostic>` blocks with suggested user actions instead of silent failure
- **Ollama retry with backoff** — 3 attempts with exponential backoff (0/2/4s) for 5xx and network errors
- **Usage data pruning** — records older than 90 days pruned during reindex
- **Configurable noise filtering** — `[vault] noise_paths` in config.toml or `SAME_NOISE_PATHS` env var
- **MCP directory manifests** — `server.json` (official MCP registry), `smithery.yaml` (Smithery.ai) for directory submissions
- **GitHub Sponsors** — `.github/FUNDING.yml` configuration
- **MCP server test coverage** — 22 tests for `safeVaultPath`, `filterPrivatePaths`, `clampTopK`, and helpers
- **45+ new tests** — store, search, indexer, config, and MCP packages

### Security

11 fixes from 6 rounds of pre-release security auditing:

- **Dot-path blocking in MCP** — `save_note` can no longer overwrite `.same/config.toml`, `.git/`, `.gitignore`
- **DB path PII fix** — `index_stats` returns `same.db` not the full filesystem path
- **MCP error sanitization** — all MCP error messages changed to static strings; no internal paths leak to AI
- **`find_similar_notes` path validation** — now validates through `safeVaultPath`
- **Write size limits** — 100KB max on `save_decision` and `create_handoff` content
- **`<plugin-context>` tag sanitization** — opening tag now stripped (was only stripping closing tag)
- **Config file permissions** — all config writes changed from 0o644 to 0o600 (5 occurrences)
- **Backup file permissions** — `same repair` backup changed to 0o600
- **OLLAMA_URL scheme validation** — blocks `file://`, `ftp://`; only `http`/`https` allowed
- **Empty input validation** — `same search`, `same ask`, `same feedback` reject empty input
- **Plugin timeout safety** — `cmd.Process` nil check before Kill()

### Fixed

- **Replaced all panics with errors** — `OllamaURL()` and `validateLocalhostOnly()` now return errors instead of crashing
- **TOML `skip_dirs` now applied** — `LoadConfig()` applies `[vault] skip_dirs` to the global `SkipDirs` map
- **Verbose log permissions** — changed from 0o644 to 0o600 (owner-only)
- **Noise path filter** — uses `HasPrefix` instead of `Contains` to prevent false matches

### Changed

- **Go 1.25** — standardized across go.mod, CI, release workflow, install scripts, README
- **Schema version 2** — adds FTS5 virtual table for keyword fallback; auto-migrates from v1
- **Context surfacing resilience** — embedding failures trigger keyword fallback instead of returning errors
- **CLI descriptions rewritten** — all user-facing commands use outcome language (e.g. "Scan your notes and rebuild the search index" instead of "Index vault into SQLite")
- **README restructured** — `same demo` above the fold, MCP tools table promoted, numbers section, SAME Lite callout, eval methodology
- **MCP tool descriptions improved** — all 11 tools with agent-oriented "when to use" guidance
- **Error messages friendlier** — "escapes vault boundary" → "outside your notes folder"; timeouts and connection failures include actionable guidance
- **Box is now default display** — `full` mode shows the cyan Unicode box automatically
- **Noise filtering off by default** — add `noise_paths` to config if you want path-based filtering
- **Intel Mac install** — install.sh uses ARM binary + Rosetta instead of non-existent darwin-amd64

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
