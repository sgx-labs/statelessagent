---
title: "Caching Strategy"
domain: backend
tags: [caching, redis, performance, architecture]
confidence: 0.8
content_type: architecture
---

# Caching Strategy

## Cache Layers

### 1. SQLite Page Cache (In-Process)

- 64MB page cache via `PRAGMA cache_size = -64000`
- 256MB memory-mapped I/O via `PRAGMA mmap_size`
- Keeps hot database pages in memory for fast reads
- No explicit invalidation needed — SQLite manages this internally

### 2. Query Result Cache (Application)

- LRU cache with 1000 entry limit
- 5-minute TTL per entry
- Keyed by normalized query string + search options
- Invalidated on reindex operations

### 3. Embedding Cache (Application)

- Caches recent query embeddings to avoid redundant API calls
- 100 entry LRU, 30-minute TTL
- Saves ~50ms per repeated query on Ollama, ~200ms on OpenAI

### 4. Redis Cache (Distributed)

- Rate limit counters: sliding window with 60-second granularity
- Session data: refresh token metadata, 7-day TTL
- Auth deny-list: revoked token IDs, TTL matches token expiry

## Cache Invalidation

- Reindex clears the query result cache
- Note mutation clears cached results containing that note's path
- Auth events (logout, password change) clear session cache
- Redis TTLs handle expiration of rate limits and sessions

## Performance Impact

| Operation | Without cache | With cache |
|-----------|--------------|------------|
| Search query | 85ms | 5ms (cache hit) |
| Query embedding | 50-200ms | 0ms (cache hit) |
| Rate limit check | 2ms | 2ms (Redis always) |
| Auth validation | 5ms | 5ms (JWT is stateless) |

## Monitoring

- Cache hit ratio tracked via metrics endpoint
- Alert if hit ratio drops below 40% (indicates working set exceeds cache)
- Redis memory usage monitored via `INFO memory`
