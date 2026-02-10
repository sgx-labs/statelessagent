# Changelog

## v0.6.0 — Production Polish

Error handling, AI diagnostics, and reliability improvements.

### Fixed

- **Replaced all panics with errors** — `OllamaURL()` and `validateLocalhostOnly()` now return errors instead of crashing the process on bad URLs
- **Verbose log permissions** — changed from 0o644 to 0o600 (owner-only)

### Added

- **AI-facing diagnostics** — when hooks fail (DB missing, Ollama down), the AI agent now sees `<same-diagnostic>` blocks with suggested user actions instead of silent failure
- **Ollama retry with backoff** — 3 attempts with exponential backoff (0/2/4s) for 5xx and network errors; 4xx errors fail immediately
- **Verbose log rotation** — logs rotate at 5MB (keeps last 1MB) to prevent unbounded growth
- **5 new `same doctor` checks** — config file validity, hook installation, DB integrity, index freshness, log file size
- **Tests for config and embedding packages** — `config_test.go` and `ollama_test.go` covering URL validation, retry behavior, model defaults, and error constants

### Changed

- **Consistent error messages** — all vault/database errors use shared `ErrNoVault`, `ErrNoDatabase`, `ErrOllamaNotLocal` constants
- **README expanded** — added display modes, push protection, and troubleshooting sections

---

## v0.5.6 — Box Mode Default

The visual feedback box is now the default display for `full` mode — no env var needed.

### Changed

- **Box is now default** — `full` display mode shows the cyan Unicode box automatically (previously required `SAME_BOX=1`)
- **Setup messaging updated** — experience level descriptions now mention the visual box and how to switch modes
- **Removed `SAME_BOX` env var** — no longer needed; `same display full` always shows the box

---

## v0.5.5 — Configurable Noise Filtering

Removed hardcoded vault structure assumptions. SAME no longer expects specific folder names.

### Changed

- **Session bootstrap** walks the entire vault for decision files instead of hardcoded directories
- **Noise path filtering** is now user-configurable via `[vault] noise_paths` in config.toml or `SAME_NOISE_PATHS` env var (defaults to empty — no paths filtered)
- Genericized all test fixtures and code comments

### Added

- `noise_paths` config field and `SAME_NOISE_PATHS` env var for filtering low-value paths from context surfacing

### Breaking

- Noise filtering no longer applies by default. If you relied on implicit filtering, add `noise_paths = ["experiments/", "raw_outputs/"]` to your `[vault]` config.

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
- **Dependency checks** — Verifies Go 1.23+ and CGO with platform-specific install instructions
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
