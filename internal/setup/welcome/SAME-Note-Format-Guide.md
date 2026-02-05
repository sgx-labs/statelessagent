---
title: "SAME — Note Format Guide"
tags: [same, reference, formatting]
content_type: hub
---

# Note Format Guide

Structure your notes so SAME (and your AI) can find and use them effectively.

## Frontmatter

YAML frontmatter at the top of your note helps with filtering and ranking:

```yaml
---
title: "Project Architecture Decisions"
tags: [architecture, decisions, backend]
content_type: decision
domain: engineering
workstream: api-redesign
---
```

### Recommended Fields

| Field | Purpose | Example |
|-------|---------|---------|
| `title` | Display name in search results | `"API Design Notes"` |
| `tags` | Searchable categories | `[api, design, rest]` |
| `content_type` | Ranking boost | `hub`, `decision`, `handoff`, `note` |
| `domain` | Filter by area | `engineering`, `product`, `ops` |
| `workstream` | Filter by project | `api-redesign`, `mobile-app` |

### Content Types

- **hub** — Central reference documents (get boosted)
- **decision** — Recorded decisions (get boosted)
- **handoff** — Session summaries (get boosted for recency)
- **note** — Default, no special treatment

## Headings

Use H2 (`##`) headings to create logical chunks. SAME splits long notes at H2 boundaries for better search precision.

```markdown
## Authentication

We use JWT tokens with 24-hour expiry...

## Database

PostgreSQL for production, SQLite for development...
```

## Writing for AI Retrieval

1. **Be specific** — "We chose PostgreSQL because..." beats "Database stuff"
2. **State decisions clearly** — "Decision: Use REST, not GraphQL"
3. **Include context** — Why, not just what
4. **Use consistent terminology** — If you call it "auth" everywhere, don't switch to "authentication"

## Examples

### Good: Decision Note

```markdown
---
title: "Auth System Decision"
tags: [auth, security, decision]
content_type: decision
---

# Auth System Decision

## Context

We need user authentication for the API.

## Decision

Use JWT with httpOnly cookies. Tokens expire after 24 hours.

## Rationale

- Stateless (no server-side sessions)
- Secure against XSS (httpOnly)
- Industry standard
```

### Good: Project Hub

```markdown
---
title: "API Project Hub"
tags: [api, project, hub]
content_type: hub
---

# API Project Hub

Central reference for the API redesign project.

## Architecture

REST endpoints, PostgreSQL, deployed on Railway.

## Key Decisions

- [[Auth System Decision]]
- [[Rate Limiting Approach]]

## Current Status

Phase 2: Implementing user endpoints.
```
