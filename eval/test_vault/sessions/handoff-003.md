---
title: "Session Handoff 003 — Search Performance Optimization"
domain: backend
tags: [handoff, search, performance, session]
confidence: 0.85
content_type: handoff
---

# Session Handoff 003 — Search Performance Optimization

**Date:** 2025-11-22
**Agent:** claude-code
**Duration:** 3 hours

## What was accomplished

- Profiled search queries — vector search was 340ms p99, now down to 85ms
- Added composite index on (path, chunk_id) which eliminated a full table scan
- Implemented query result caching with LRU eviction (max 1000 entries, 5-min TTL)
- Benchmarked with 10k notes: search latency now under 100ms consistently

## What's in progress

- Hybrid search scoring needs tuning — keyword results sometimes outrank semantically better vector matches
- The LRU cache invalidation on reindex is not wired up yet

## Key findings

- The biggest bottleneck was the JOIN between vault_notes and vault_notes_vec — the composite index fixed it
- nomic-embed-text embeddings have good quality but the 768-dimension vectors make the index larger than expected
- FTS5 fallback path was accidentally doing a full table scan (fixed)

## Performance numbers

| Metric | Before | After |
|--------|--------|-------|
| Vector search p50 | 120ms | 35ms |
| Vector search p99 | 340ms | 85ms |
| Hybrid search p50 | 200ms | 60ms |
| Memory usage (10k notes) | 450MB | 380MB |

## Next steps

1. Wire up cache invalidation on reindex
2. Tune hybrid search scoring weights
3. Add search latency metrics to the dashboard
4. Consider dimensionality reduction if index size becomes a problem
