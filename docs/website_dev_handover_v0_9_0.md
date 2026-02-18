# Website Dev Handoff - SAME v0.9.0 (Comprehensive)

Updated: 2026-02-18
Audience: website engineering, docs, launch copy, support
Product repo: `/Users/seangleason/code/statelessagent`
Release target: v0.9.0

## 1) Executive Summary

SAME v0.9.0 is a trust-and-control release with a strong new "wow" layer:

- Knowledge Graph is now first-class in CLI and dashboard.
- Runtime is provider-flexible (not Ollama-only).
- Security and filesystem boundary checks were hardened across critical write paths.
- Self-update now verifies checksums before install.

Primary message for the website:

"SAME is local-first memory for AI work. It is explicit, inspectable, and safer by default."

## 2) Core Positioning for This Release

Use this order in homepage and launch materials:

1. Human-first and local-first.
2. Trust and operational safety.
3. Useful graph and memory workflows.
4. Provider flexibility.

One-line candidates:

- "Persistent local memory for AI sessions, with explicit controls and graph insight."
- "Your notes stay yours. SAME helps your agent remember, connect, and continue."
- "Local-first AI memory with safer defaults and practical graph workflows."

## 3) What Actually Shipped

Use only these claims.

### 3.1 Knowledge Graph

- New command group: `same graph`
- Subcommands:
  - `same graph stats`
  - `same graph query`
  - `same graph path`
  - `same graph rebuild`
- Web dashboard includes:
  - graph highlights (nodes, edges, avg degree, top relationships)
  - note-level "Knowledge Connections" paths
- Graph tutorial lesson: `same tutorial graph`
- Schema migration v6 adds graph tables (`graph_nodes`, `graph_edges`)

### 3.2 Provider Flexibility

- Chat and graph extraction support:
  - `auto`
  - `ollama`
  - `openai`
  - `openai-compatible`
  - `none` (keyword/lite mode workflows where applicable)
- Graph LLM policy is explicit:
  - `SAME_GRAPH_LLM=off|local-only|on`
  - Default is `off`
  - `local-only` gates to localhost chat endpoints
- Diagnostics are provider-neutral:
  - `same status`
  - `same doctor`

### 3.3 Security and Hardening

- Self-update verifies downloaded binary against `sha256sums.txt`.
- Path-boundary hardening landed across:
  - MCP write paths
  - web API path validation
  - vault feed containment
  - seed extraction/install/remove flows
- `_PRIVATE/` remains excluded from index/search/web output.
- Multiple silent failure paths now surface explicit errors or warnings.
- Lock handling improved for config/init lockfile cleanup and stale-lock recovery.

### 3.4 Reliability

- `same watch` handles rename/delete churn more consistently.
- Reindex fallback behavior improved for keyword-only and embedding-failure paths.
- CLI `--vault` precedence fixed.
- Graph cleanup stays in sync with delete/force-clear note lifecycle.

## 4) Copy Guidelines (Important)

Do:

- Emphasize local-first and user ownership.
- Emphasize explicit controls, not "magic AI."
- Say graph is practical and useful now, then state it will continue improving.
- Keep claims concrete and verifiable.

Do not:

- Claim fully automatic perfect knowledge graph extraction.
- Claim zero setup for every provider stack.
- Claim cloud privacy guarantees for third-party providers.
- Claim "replaces all note taking/search workflows."

## 5) Website IA and Content Updates

Recommended page updates:

1. Homepage hero
2. Feature section
3. Trust/security section
4. "How it works" section
5. Changelog/release notes page
6. Docs quickstart and troubleshooting page

### 5.1 Homepage Hero

Headline options:

- "Local-first memory for AI coding sessions"
- "Your AI can continue where you left off"

Subheadline options:

- "SAME stores durable project memory in your vault, adds graph relationships, and keeps control explicit."
- "Persistent session context, graph connections, and safer defaults for solo builders and small teams."

Primary CTA:

- "Get Started"

Secondary CTA:

- "See v0.9.0 Changes"

### 5.2 Feature Section (Suggested Cards)

1. Persistent Memory
- "Decisions, handoffs, and note context survive across sessions."

2. Knowledge Graph
- "Trace relationships across notes, files, and decisions in CLI and dashboard."

3. Provider Flex Runtime
- "Run with Ollama, OpenAI-compatible local servers, OpenAI, or keyword-first flows."

4. Safety by Default
- "Checksum-verified updates, path containment guards, and private path filtering."

### 5.3 Trust Section

Suggested bullets:

- "Local-first architecture; your markdown remains the source of truth."
- "Dashboard binds to localhost only."
- "`_PRIVATE/` content is not indexed and not surfaced through web endpoints."
- "Updates are verified against release checksums before install."

## 6) Recommended Transparency Note (Optional)

If publishing a values/process note:

"While testing a new multi-agent workflow, an AI-generated research memo was accidentally pushed. It was removed after human review. We do not condone vote manipulation or deceptive launch tactics, and we ask for honest feedback only."

Keep this short, factual, and one-time.

## 7) Web Dashboard Contract (for Website/Docs Accuracy)

Runtime behavior:

- Local read-only dashboard launched by:
  - `same web`
  - default address `127.0.0.1:4078`
  - optional `--port` and `--open`

Security behavior:

- localhost-only middleware (loopback host check)
- response headers include:
  - `X-Frame-Options: DENY`
  - `X-Content-Type-Options: nosniff`
  - CSP restricting to self and inline script/style

## 8) API Endpoints Used by Dashboard

Base: same process as dashboard server.

1. `GET /api/status`
- Returns note/chunk counts, search mode, db size, version, vault name/path.

2. `GET /api/notes/recent?limit=20`
- Recent notes, private paths filtered.

3. `GET /api/notes`
- All notes metadata, private paths filtered.

4. `GET /api/notes/{path}`
- Full note payload (size-capped), path validated.

5. `GET /api/search?q=...&top_k=...`
- Search results and mode (`semantic` or `keyword`).

6. `GET /api/pinned`
- Pinned notes, private paths filtered.

7. `GET /api/related/{path}`
- Related notes for selected note (empty if vectors unavailable).

8. `GET /api/graph/stats`
- Graph aggregates.
- Includes `available: false` fallback if graph tables are absent.

9. `GET /api/graph/connections/{path}?depth=2&dir=forward`
- Path-based graph traversals from note.
- Includes `hint` field for "no node yet" style cases.

Path validation:

- traversal, absolute paths, hidden-dot segments, and Windows drive-prefix absolute paths are rejected.
- `_PRIVATE/` access returns not found behavior.

## 9) Current Dashboard UX Capabilities

Implemented:

- Pages: Dashboard, Search, Browse, Decisions, Handoffs, Note viewer
- Keyboard:
  - `/` or `s` focuses Search
  - `Esc` clears search
  - arrow keys navigate result list
  - Enter opens selected result
- Insight cards with actionable CLI buttons
- Graph highlights and note-level graph connection rendering
- Mobile responsive layout (sidebar collapses)
- Print-friendly note view

## 10) Known Limits to Communicate Clearly

Use in docs/FAQ:

1. Graph quality depends on note structure and link quality.
2. Graph LLM enrichment is opt-in (default off).
3. Keyword mode is useful but less semantically rich than embedding mode.
4. Related-note quality is lower when vectors are unavailable.
5. Dashboard is read-only by design.

## 11) Release Notes Copy Pack

### 11.1 Short "What is new" block

"v0.9.0 adds a practical knowledge graph to SAME, makes provider/runtime behavior more explicit, and hardens safety boundaries across update, path handling, and write flows."

### 11.2 Security/trust block

"This release strengthens trust defaults: checksum-verified self-update, stricter path containment checks, private-path filtering, and clearer error surfacing where previous flows could fail silently."

### 11.3 Graph block

"You can now inspect and traverse knowledge relationships in both CLI (`same graph`) and the local dashboard, including note-level path views with relationship labels."

## 12) QA Matrix for Website Team

Run before publishing docs/copy:

1. Verify commands referenced on site exist and match exact syntax.
2. Verify screenshots/GIFs match current UI labels and layout.
3. Verify all security claims map to shipped behavior.
4. Verify no copy implies cloud-required operation.
5. Verify provider table includes `openai-compatible` and `none` behavior correctly.
6. Verify graph screenshots include realistic data and not empty-state only.

Suggested smoke flow:

1. `make precheck`
2. `./build/same reindex`
3. `./build/same web --open`
4. `./build/same graph stats`
5. Open note with connections in dashboard
6. Confirm `_PRIVATE/` notes do not appear

## 13) Asset List to Prepare

1. Hero screenshot: Dashboard with stats and graph highlights
2. Search screenshot: semantic result list with relevance bars
3. Graph screenshot: note-level "Knowledge Connections"
4. CLI screenshot: `same graph stats` and `same graph query`
5. Trust screenshot: `same doctor` plus update verification mention

## 14) Handoff to Support/Community

Support-ready points:

1. If search feels weak:
- run `same doctor`
- run `same reindex`
- confirm provider/runtime mode in `same status`

2. If graph looks empty:
- run `same reindex`
- confirm note links and references exist
- check `same graph stats`

3. If dashboard is inaccessible:
- use localhost URL printed by `same web`
- avoid non-loopback hostname access

## 15) Next Recommended Website Iteration (Post-Release)

1. Add an interactive "How graph works" mini demo.
2. Add provider setup recipes:
- Ollama
- OpenAI-compatible local servers (llama.cpp, LM Studio, vLLM)
- keyword-only/lite mode
3. Add a "human-first principles" page excerpting design context.

## 16) Source of Truth in Repo

- Release summary: `/Users/seangleason/code/statelessagent/docs/release_brief_v0_9_0.md`
- Full changelog: `/Users/seangleason/code/statelessagent/CHANGELOG.md`
- Product philosophy: `/Users/seangleason/code/statelessagent/docs/design_context.md`
- Web server implementation: `/Users/seangleason/code/statelessagent/internal/web/server.go`
- Dashboard frontend: `/Users/seangleason/code/statelessagent/internal/web/static/index.html`

