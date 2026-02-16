# SAME — Product Hunt Launch Plan

## Tagline

**Your AI forgets everything between sessions. Not anymore.**

## One-liner

Persistent memory for AI coding agents — local-first, private, one binary.

## Description (240 words)

Every time you start a new session with Claude Code, Cursor, or any AI coding tool, your agent starts from zero. Decisions you made yesterday? Gone. Context from last week? Gone.

SAME (Stateless Agent Memory Engine) gives your AI persistent memory from your existing markdown notes. No cloud. No API keys. One binary.

**How it works:**
- Point SAME at any folder of `.md` files
- Your notes get indexed locally via SQLite + Ollama embeddings
- When your AI starts a session, relevant context surfaces automatically
- Decisions get extracted and remembered. Handoffs survive between sessions.

**Key features:**
- 12 MCP tools for any AI client (Claude Code, Cursor, Windsurf)
- Semantic + keyword search with published benchmarks (P=99.5%, MRR=0.949)
- Session handoffs, decision logs, crash recovery
- Multi-agent coordination via file claims
- Pre-built "seed vaults" — expert knowledge in one command
- Web dashboard for browsing your knowledge base
- Works without Ollama (keyword fallback)

**Privacy by design:**
- ~10MB Go binary. Zero outbound network calls.
- No telemetry. No analytics. No accounts.
- `_PRIVATE/` directory never indexed, never committed.
- Everything stays on your machine.

**Open source** under BSL 1.1 (converts to Apache 2.0 in 2030).

## Topics

- Developer Tools
- Artificial Intelligence
- Open Source
- Productivity
- MCP

## First Comment

Hey Product Hunt! 👋

I built SAME because I was tired of re-explaining my project decisions to Claude Code every single session. The AI would forget architectural choices, coding conventions, even what we built yesterday.

SAME solves this with a simple approach: your existing markdown notes become your AI's memory. No new note format, no cloud sync, no vendor lock-in. Just point it at a folder of `.md` files and your AI remembers.

The technical approach:
- Local embeddings via Ollama (or keyword-only mode with zero dependencies)
- SQLite + sqlite-vec for vector search
- 6 Claude Code hooks for automatic context surfacing
- 12 MCP tools that work with any compatible client

I've been using it daily for 3 months across multiple projects. The difference is night and day — sessions that used to start with 10 minutes of context-setting now pick up exactly where I left off.

Try it in 60 seconds:
```
curl -fsSL statelessagent.com/install.sh | bash
same demo
```

Would love your feedback! I'm active on our Discord (link in the README) and responding to all GitHub issues.

## Maker Info

- **Maker**: @sgx-labs
- **Website**: https://statelessagent.com
- **GitHub**: https://github.com/sgx-labs/statelessagent
- **Discord**: https://discord.gg/9KfTkcGs7g

## Media

- Screenshot: `same demo` output showing semantic search
- Screenshot: Web dashboard with note cards and agent badges
- Screenshot: Claude Code session with SAME context injection
- GIF: `same init` → `same search` → results in <10 seconds

## Launch Timing

- **Day**: Tuesday or Wednesday (highest PH traffic)
- **Time**: 12:01 AM PT (Product Hunt resets at midnight PT)
- **Pre-launch**: Share on Twitter/X, Discord, relevant subreddits 24h before

## Upvote Strategy

1. Personal network notification 24h before
2. Discord community announcement
3. Twitter/X thread with demo GIF
4. r/ClaudeAI, r/cursor, r/LocalLLaMA posts (value-first, not promotional)
5. Hacker News "Show HN" post (separate from PH, stagger by 1 day)
