---
title: "SAME — For AI Agents"
tags: [same, reference, ai-agent, mcp]
content_type: hub
---

# For AI Agents

This note is for you, the AI assistant. It explains what SAME is and how to use it effectively.

## What Is SAME?

SAME (Stateless Agent Memory Engine) gives you persistent memory across sessions. The user's notes are indexed and automatically surfaced when relevant to the conversation.

You don't need to do anything special — relevant context appears automatically. But you can also actively search when needed.

## Available MCP Tools

If SAME is registered as an MCP server, you have these tools:

| Tool | Use Case |
|------|----------|
| `search_notes` | Semantic search — find notes by meaning, not just keywords |
| `search_notes_filtered` | Search with filters (domain, workstream, tags) |
| `get_note` | Read a specific note by path |
| `find_similar_notes` | Find notes related to a given note |
| `reindex` | Re-index the vault (rarely needed) |
| `index_stats` | Check how many notes are indexed |

### Example Usage

```
search_notes("authentication decisions")
search_notes_filtered("API design", domain="engineering")
get_note("decisions/auth-system.md")
find_similar_notes("architecture/overview.md", top_k=5)
```

## How To Be Effective

### 1. Search First, Ask Later

Before asking the user to explain something, search their notes:

```
search_notes("authentication approach")
search_notes("project architecture")
search_notes("coding preferences")
```

Their past decisions and context are probably already documented.

### 2. Chain Your Searches

If one search reveals related topics, follow up:

```
# First search reveals they use JWT
search_notes("JWT implementation")
# Then search for related security decisions
search_notes("security decisions")
```

### 3. Dig Deeper When Needed

If the user says something like "it's definitely in my notes" or "search harder":

```
# Default search returns 5 results
search_notes("authentication", top_k=5)

# Dig deeper with more results
search_notes("authentication", top_k=15)

# Try alternate phrasings
search_notes("auth")
search_notes("login")
search_notes("user authentication")
```

**When to dig deeper:**
- User says the answer should be in their notes
- Initial search returned nothing relevant
- Topic might use different terminology

**Tip:** If you find nothing, tell the user: "I searched for X but didn't find it. Want me to try different terms, or is this something we should document?"

### 3. Always Acknowledge What You Used

When surfaced context helps, tell the user:
- "Based on your Auth System Decision note, I see you chose JWT..."
- "Your Architecture Overview mentions PostgreSQL, so I'll use that..."
- "I found your API Design Decisions — following those patterns..."

This builds trust and helps them know SAME is working.

### 4. Create Notes That Help Future Sessions

When documenting decisions or creating notes:

```markdown
---
title: "Decision: Use Redis for Caching"
tags: [architecture, caching, decision]
content_type: decision
---

# Decision: Use Redis for Caching

## Context
We need caching for API responses.

## Decision
Use Redis with 5-minute TTL.

## Rationale
- Already have Redis for sessions
- Simple key-value fits our use case
- Team has Redis experience
```

**Key principles:**
- State decisions explicitly in the title
- Include WHY, not just WHAT
- Use specific, searchable terms
- Add relevant tags

### 5. Know the Content Types

Use `content_type` in frontmatter to help ranking:
- `decision` — Gets boosted, represents recorded decisions
- `hub` — Gets boosted, central reference documents
- `handoff` — Session summaries, boosted for recency
- `note` — Default, no special treatment

## How Context Gets Surfaced

When the user sends a message:
1. SAME embeds their prompt
2. Searches for semantically similar note chunks
3. Ranks by relevance + recency + confidence
4. Injects top matches into the conversation

You'll see surfaced context prefixed with something like:
```
[SAME surfaced from your notes]
• Note Title (0.82) — "snippet of content..."
```

## Writing for Future Sessions

When you create or update notes, write them so your future self (or another AI session) can understand:

- State decisions explicitly: "Decision: Use JWT, not sessions"
- Include rationale: "Because we need stateless auth"
- Be specific: "PostgreSQL 15" not "the database"
- Use consistent terminology throughout the project

---

## Suggested CLAUDE.md Addition

Add this to your project's CLAUDE.md to help future AI sessions:

```markdown
# SAME Memory System

This project uses SAME for persistent AI memory. Your context is automatically surfaced from notes.

## Before Asking Questions

Search first: `search_notes("topic")` — the answer may already be documented.

## When Making Decisions

1. Search for prior decisions on the topic
2. Document new decisions in a note with `content_type: decision`
3. Reference the note path so future sessions can find it

## Available Tools

- `search_notes(query)` — Semantic search
- `get_note(path)` — Read a specific note
- `find_similar_notes(path)` — Find related notes

## Note Locations

- Decisions: `decisions/` or `decisions.md`
- Session handoffs: `sessions/`
- Architecture: Check for "architecture" or "hub" notes
```
