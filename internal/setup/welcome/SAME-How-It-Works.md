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
Your Notes  →  Ollama  →  SQLite  →  AI Remembers
   (.md)       (embed)    (search)    (hooks/MCP)
```

## What Gets Surfaced

When you send a prompt, SAME:

1. Embeds your prompt using Ollama (locally)
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

## Privacy

- All processing happens locally via Ollama
- Your notes never leave your machine
- The only external calls are to your AI provider (Anthropic, OpenAI, etc.) as part of your normal conversation

## Commands

```bash
same status    # See what's indexed
same doctor    # Health check
same search    # Test search from CLI
same profile   # Adjust precision vs coverage
same display   # Control output verbosity
```
