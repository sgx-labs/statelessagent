---
title: "Data Flow Architecture"
domain: engineering
tags: [data-flow, pipeline, architecture]
confidence: 0.8
content_type: architecture
---

# Data Flow Architecture

## User Request Flow

```
Client → API Gateway → Auth Middleware → Rate Limiter → Handler → Service → Database
                                                                      ↓
                                                              Response (JSON)
```

### Request Lifecycle

1. **Client** sends HTTPS request with JWT in Authorization header
2. **API Gateway** validates TLS, extracts route
3. **Auth Middleware** validates JWT, extracts claims, attaches to context
4. **Rate Limiter** checks Redis sliding window counter
5. **Handler** validates input, calls service layer
6. **Service** executes business logic, queries database
7. **Response** marshaled as JSON, returned to client

## Indexing Data Flow

```
File system change → Watcher → Debouncer → Parser → Chunker → Embedder → SQLite Writer
```

### Indexing Pipeline Details

1. **Watcher** detects file creation, modification, deletion via fsnotify
2. **Debouncer** batches rapid changes (500ms window)
3. **Parser** extracts frontmatter and body from markdown
4. **Chunker** splits text at heading boundaries (max 2000 tokens per chunk)
5. **Embedder** generates 768-dim vectors via Ollama (or OpenAI)
6. **SQLite Writer** atomically replaces note chunks in the vault database

## Search Data Flow

```
Query → Embed query → Vector KNN → Keyword fallback → Hybrid merge → Rank → Deduplicate → Return top-k
```

## Event Flow

- Note mutations publish events to Redis pub/sub channel `vault:changes`
- Dashboard clients subscribe to SSE endpoint `/v1/events`
- Graph edges auto-updated when note links change
