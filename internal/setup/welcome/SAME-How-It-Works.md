---
title: "SAME — How It Works"
tags: [same, reference, onboarding]
content_type: hub
---

# How SAME Works

SAME (Stateless Agent Memory Engine) gives your AI coding agent persistent memory across sessions.

## The Problem

Every AI session starts from zero. You explain your project, your preferences, your decisions — then the context window fills up and it all disappears. Next session, you start over.

## The Solution

SAME indexes your markdown notes locally and automatically surfaces relevant context when you chat with your AI. No manual copy-pasting. No token waste.

```
Your Notes  →  Embeddings  →  SQLite  →  AI Remembers
   (.md)      (local/cloud)   (search)    (hooks/MCP)
```

## What Gets Surfaced

When you send a prompt, SAME:

1. Embeds your prompt using your configured provider (local or cloud)
2. Searches for semantically similar note chunks
3. Ranks by relevance + recency + confidence
4. Injects the top matches into your conversation

The AI sees a snippet like:

```
[SAME surfaced from your notes]
• Project Architecture (0.82) — "We decided to use SQLite for..."
• API Design Decisions (0.76) — "REST endpoints follow..."
```

## What SAME Tracks

- **Decisions** — Extracted from conversations automatically
- **Handoffs** — Session summaries for continuity
- **Usage patterns** — Notes that help get boosted over time
- **Staleness** — Old notes get flagged for review

## Token Costs & Vault Growth

**Good news: More notes ≠ more tokens per query.**

SAME surfaces a fixed number of results regardless of vault size:

| Setting | Default | Effect |
|---------|---------|--------|
| `max_results` | 2 | Only top 2 notes surfaced |
| `max_token_budget` | 800 | Cap on injected tokens |

A vault with 100 notes costs the same per-query as one with 10,000 notes. More notes just means better search precision.

**What grows over time:**
- Database size (`.same/data/vault.db`)
- Handoff files in `sessions/` folder
- Decision log (`decisions.md`)

**Maintenance tips:**
- Old handoffs can be archived/deleted (they're just markdown)
- Stale notes get flagged — review and update or delete them
- Use `same profile use precise` if surfacing too much

**Profiles for token control:**
- `precise` — Stricter matching, fewer tokens
- `balanced` — Default
- `broad` — More context, ~2x tokens

## Privacy

- Notes and local database stay on your machine
- Local embedding providers keep semantic indexing fully local
- Cloud embedding/chat providers send requests only to the providers you configure

## Commands

```bash
same status    # See what's indexed
same doctor    # Health check
same search    # Test search from CLI
same profile   # Adjust precision vs coverage
same display   # Control output verbosity
```
