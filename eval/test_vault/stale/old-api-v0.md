---
title: "API v0 Documentation (Deprecated)"
domain: backend
tags: [api, v0, deprecated, stale]
confidence: 0.2
content_type: reference
trust_state: stale
review_by: "2025-08-01"
---

# API v0 Documentation (Deprecated)

> **DEPRECATED**: API v0 has been sunset as of November 2025. All clients should migrate to API v1. See `decisions/api-versioning.md` for the versioning strategy.

## v0 Endpoints (No Longer Available)

### `GET /api/notes`
List all notes. No pagination, no filtering.

### `GET /api/notes/:id`
Get a single note by ID (numeric).

### `POST /api/notes`
Create a note. Body: raw markdown text (no frontmatter support).

### `GET /api/search?q=term`
Keyword-only search. No semantic search, no ranking.

## Breaking Changes in v1

| v0 | v1 | Change |
|----|-----|--------|
| `/api/notes` | `/v1/notes` | URL prefix changed |
| Numeric IDs | Path-based IDs | Notes identified by file path, not DB ID |
| Raw text body | Frontmatter + body | Structured metadata support |
| Keyword search | Hybrid search | Vector + keyword with ranking |
| No auth | JWT required | All endpoints require authentication |
| No rate limiting | Tiered rate limits | See API versioning decision |

## Migration Guide

1. Update base URL from `/api/` to `/v1/`
2. Replace numeric note IDs with file paths
3. Add frontmatter to note creation requests
4. Implement JWT authentication flow
5. Handle rate limit responses (429)
