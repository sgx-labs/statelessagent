# Security Policy

## Supported Versions

| Version | Supported          |
| ------- | ------------------ |
| 0.12.1  | :white_check_mark: |
| 0.12.x  | :white_check_mark: |
| 0.11.x  | :white_check_mark: |
| 0.10.x  | Security fixes only |
| < 0.10  | :x:                |

## Reporting a Vulnerability

If you discover a security vulnerability in SAME, please report it responsibly:

**Email:** dev@thirty3labs.com

**What to include:**
- Description of the vulnerability
- Steps to reproduce
- Potential impact
- Any suggested fixes (optional)

**Response timeline:**
- Acknowledgment within 48 hours
- Initial assessment within 7 days
- Fix timeline communicated based on severity

**Please do not:**
- Open public GitHub issues for security vulnerabilities
- Exploit vulnerabilities beyond proof-of-concept
- Share vulnerability details before a fix is released

## Security Model

SAME is designed with a local-first security model:

### Data Locality
- All data (embeddings, database, config) stays on your machine
- No telemetry, analytics, or external API calls from SAME itself
- The only network calls are to Ollama (localhost) or optionally OpenAI (if configured)

### Ollama URL Validation
- Ollama URL is validated to be localhost-only (`127.0.0.1`, `localhost`, `::1`, `host.docker.internal`)
- `host.docker.internal` is allowed for container environments (Docker, OrbStack, Codespaces)
- Prevents SSRF attacks via malicious config

### Private Content Exclusion
- Directories named `_PRIVATE` are excluded from indexing
- Private content is never surfaced to AI agents
- Configurable skip patterns via `skip_dirs`

### Prompt Injection Protection
- Surfaced snippets are scanned for prompt injection patterns before injection
- Uses [go-promptguard](https://github.com/mdombrov-33/go-promptguard) for detection
- Suspicious content is blocked from context surfacing
- XML-like structural tags (`<vault-context>`, `<session-bootstrap>`, etc.) are neutralized in all output paths
- LLM-specific injection delimiters (`[INST]`, `<<SYS>>`, CDATA) are neutralized (v0.8.3)
- MCP `get_note`, `get_session_context`, and all search handlers sanitize output before returning to agents (v0.8.3)
- Write rate limiting (30 ops/min) prevents abuse via prompt-injected write loops

### Path Traversal Protection
- MCP `get_note` tool validates paths stay within vault boundary
- Symlink resolution verifies real path stays inside vault (v0.8.3)
- Provenance source paths validated before file reads (v0.12.0)
- Source divergence checks validate stored paths before reads (v0.12.0)
- Relative path components (`..`) are rejected
- Null bytes in paths are rejected
- Windows drive-letter paths are rejected regardless of host OS

### Memory Integrity (v0.12.0)
- Provenance tracking records SHA256 hashes of source files at capture time
- Trust state (`validated`, `stale`, `contradicted`, `unknown`) affects search ranking
- Staleness and divergence context tags are sanitized against prompt injection
- YAML frontmatter values are sanitized to prevent injection via newlines

### Input Validation
- All user inputs are validated before processing
- SQL queries use parameterized statements (no injection risk)
- FTS5 query terms are sanitized to prevent FTS operator injection
- Agent attribution values are validated (length, no control chars, no newlines)
- MCP write operations enforce 100KB content limit
- Plugin commands are validated against shell metacharacters, path traversal, and null bytes

### Eval Data Boundary

Evaluation test fixtures (ground truth, test queries, expected results) must **never** reference real vault content:

- No real `_PRIVATE/` paths or note titles
- No real client names, project names, or business terms
- No real vault note content or snippets

Eval data must be either **entirely synthetic** or use a **purpose-built demo vault** with public sample data.

## Known Limitations

1. **Trust boundary:** Content surfaced to your AI tool is sent to that tool's API. SAME doesn't control what happens after context is injected.

2. **Embedding model:** If using OpenAI embeddings, your note content is sent to OpenAI's API. Use Ollama for fully local operation.

3. **No encryption at rest:** The SQLite database is not encrypted. Use disk encryption if needed.

### `_PRIVATE/` Directory Protection
- `_PRIVATE/` directories are excluded from indexing at the file walker level
- All search handlers (vector, FTS5, keyword, federated) filter `_PRIVATE/` paths at the SQL level
- MCP `recent_activity` and `get_session_context` filter `_PRIVATE/` paths in application code (v0.8.3)
- Hook context surfacing filters `_PRIVATE/` paths before injection
- Web dashboard filters `_PRIVATE/` paths from all views

## Security Checklist

Run `same doctor` to verify:
- [x] Ollama URL is localhost-only
- [x] Private directories are excluded
- [x] Database is accessible only to current user
- [x] Vector search is functioning
- [x] Context surfacing respects skip patterns
- [x] Web dashboard binds to localhost only
- [x] MCP write operations are rate-limited
